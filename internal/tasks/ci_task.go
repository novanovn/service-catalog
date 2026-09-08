package tasks

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os"

	"github.com/hibiken/asynq"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/oona-insurance/dev-portal/internal/api"
	"github.com/oona-insurance/dev-portal/internal/auth"
	db "github.com/oona-insurance/dev-portal/internal/repository/postgres/generated"
	"github.com/oona-insurance/dev-portal/internal/worker/ci"
)

const (
	TypeCreatePipeline = "ci:create_pipeline"
)

type CreatePipelinePayload struct {
	TicketID      string
	PipelineName  string
	JenkinsFolder string
	RepoURL       string
	IntegrationID string // UUID of the integration (Jenkins/GitLab) settings from DB
	TriggeredBy   string // Name or Email of the DevOps who clicked approve
}

func NewCreatePipelineTask(ticketID, pipelineName, jenkinsFolder, repoURL, integrationID, triggeredBy string) (*asynq.Task, error) {
	payload, err := json.Marshal(CreatePipelinePayload{
		TicketID:      ticketID,
		PipelineName:  pipelineName,
		JenkinsFolder: jenkinsFolder,
		RepoURL:       repoURL,
		IntegrationID: integrationID,
		TriggeredBy:   triggeredBy,
	})
	if err != nil {
		return nil, err
	}
	return asynq.NewTask(TypeCreatePipeline, payload, asynq.Queue("aws_jenkins"), asynq.MaxRetry(3)), nil
}

func HandleCreatePipelineTask(ctx context.Context, t *asynq.Task) error {
	var p CreatePipelinePayload
	if err := json.Unmarshal(t.Payload(), &p); err != nil {
		return fmt.Errorf("json unmarshal failed: %v: %w", err, asynq.SkipRetry)
	}

	// 1. Fetch Integration settings from DB or environment variables
	provider := "jenkins"
	baseURL := os.Getenv("JENKINS_URL")
	if baseURL == "" {
		baseURL = "https://automation.oona-insurance.com"
	}
	authUser := os.Getenv("JENKINS_USER")
	authToken := os.Getenv("JENKINS_API_TOKEN")

	if api.DB != nil {
		integrations, err := api.DB.GetActiveIntegrations(ctx)
		if err == nil {
			for _, ig := range integrations {
				if ig.Provider == db.IntegrationProviderJenkins {
					baseURL = ig.BaseUrl
					if ig.AuthUser.Valid && ig.AuthUser.String != "" {
						authUser = ig.AuthUser.String
					}
					if decrypted, err := auth.Decrypt(ig.AuthToken); err == nil && decrypted != "" {
						authToken = decrypted
					}
					break
				}
			}
		}
	}

	// 2. Instantiate the Jenkins CI Engine
	engine := ci.NewJenkinsEngine(ci.IntegrationConfig{
		BaseURL:   baseURL,
		AuthUser:  authUser,
		AuthToken: authToken,
	})

	// 3. Create the Job unless it already exists (recreate / approve retry).
	exists, existsErr := engine.JobExists(ctx, p.JenkinsFolder, p.PipelineName)
	if existsErr != nil {
		log.Printf("Jenkins existence probe failed for %s: %v — attempting createItem", p.PipelineName, existsErr)
	}
	if exists {
		log.Printf("Jenkins job already exists: %s in folder %s — skipping createItem", p.PipelineName, p.JenkinsFolder)
	} else {
		log.Printf("Creating %s Pipeline: %s in folder: %s for %s", provider, p.PipelineName, p.JenkinsFolder, p.RepoURL)
		if err := engine.CreatePipeline(ctx, p.JenkinsFolder, p.PipelineName, p.RepoURL); err != nil {
			return fmt.Errorf("failed to create pipeline: %v", err)
		}
	}

	// 4. Trigger Initial Build to initialize Multibranch scanning
	log.Printf("Triggering initial branch scan build for %s in folder: %s (Approved by: %s)", p.PipelineName, p.JenkinsFolder, p.TriggeredBy)
	if err := engine.TriggerBuild(ctx, p.JenkinsFolder, p.PipelineName, p.TriggeredBy); err != nil {
		log.Printf("Warning: Created job but failed to trigger build: %v", err)
	}

	// Recreate from catalog has no ticket — do not invent a LIVE transition.
	if p.TicketID == "" {
		return nil
	}

	// 5. Update Ticket Status to LIVE and ensure Catalog entry is active & synced
	if api.DB != nil {
		var ticketUUID pgtype.UUID
		if err := ticketUUID.Scan(p.TicketID); err == nil {
			ticket, err := api.DB.UpdateTicketStatus(ctx, db.UpdateTicketStatusParams{
				ID:     ticketUUID,
				Status: db.TicketStatusLIVE,
			})
			if err != nil {
				log.Printf("Failed to update ticket status to LIVE: %v", err)
			} else {
				// Upsert into active catalog table
				_, catErr := api.DB.UpsertCatalogEntry(ctx, db.UpsertCatalogEntryParams{
					Name:            ticket.ServiceName,
					Description:     ticket.Description,
					Domain:          pgtype.Text{String: ticket.Domain, Valid: true},
					Country:         pgtype.Text{String: ticket.Country, Valid: true},
					Status:          "LIVE",
					RepoUrl:         pgtype.Text{String: ticket.RepoUrl, Valid: true},
					PipelineName:    pgtype.Text{String: p.PipelineName, Valid: true},
					RequestorEmail:  pgtype.Text{String: "admin@oona-insurance.com", Valid: true},
					JiraID:          ticket.JiraIssueID,
					AwsLastModified: pgtype.Text{String: "Just now (Approved)", Valid: true},
					AwsLastInvoked:  pgtype.Text{String: "Healthy (200 OK)", Valid: true},
				})
				if catErr != nil {
					log.Printf("Failed to upsert approved service into catalog: %v", catErr)
				}
			}
		}
	}

	return nil
}
