package tasks

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"time"

	"github.com/hibiken/asynq"
	"github.com/jackc/pgx/v5/pgtype"
	"service-catalog/internal/api"
	db "service-catalog/internal/repository/postgres/generated"
	"service-catalog/internal/worker/infra"
)

const (
	TypeVerifyInfraGit = "infra:verify_git"
)

type VerifyInfraPayload struct {
	TicketID      string
	Domain        string
	Country       string
	Env           string
	ServiceName   string
	IntegrationID string // UUID for the Terraform Repo settings in DB
}

func NewVerifyInfraGitTask(ticketID, domain, country, env, svcName, integrationID string) (*asynq.Task, error) {
	payload, err := json.Marshal(VerifyInfraPayload{
		TicketID:      ticketID,
		Domain:        domain,
		Country:       country,
		Env:           env,
		ServiceName:   svcName,
		IntegrationID: integrationID,
	})
	if err != nil {
		return nil, err
	}
	return asynq.NewTask(TypeVerifyInfraGit, payload, asynq.Queue("git_verify"), asynq.MaxRetry(15)), nil
}

func HandleVerifyInfraGitTask(ctx context.Context, t *asynq.Task) error {
	var p VerifyInfraPayload
	if err := json.Unmarshal(t.Payload(), &p); err != nil {
		return fmt.Errorf("json unmarshal failed: %v: %w", err, asynq.SkipRetry)
	}

	// 1. Build the expected path based on Oona's strict convention
	expectedRelativePath := fmt.Sprintf("02-app-setup/%s/%s/%s/services/%s/terraform.tfvars",
		p.Domain, p.Country, p.Env, p.ServiceName)

	var hclContent []byte
	var fetchedSuccessfully bool

	// 2. Check if an override path is provided for local dev / testing
	if overridePath := os.Getenv("OONA_TF_REPO_PATH"); overridePath != "" {
		fullPath := filepath.Join(overridePath, expectedRelativePath)
		if src, err := os.ReadFile(fullPath); err == nil {
			hclContent = src
			fetchedSuccessfully = true
		}
	}

	// 3. Primary Production Method: GitHub Contents API (Instant HTTP GET, no git clone, no disk I/O)
	if !fetchedSuccessfully {
		targetURL := fmt.Sprintf("https://api.github.com/repos/oona-insurance/oona-dtc-country-terraform-iac/contents/%s?ref=main", expectedRelativePath)
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, targetURL, nil)
		if err == nil {
			req.Header.Set("User-Agent", "Oona-Dev-Portal-Worker/1.0")
			req.Header.Set("Accept", "application/vnd.github.v3.raw")
			token := os.Getenv("GITHUB_TOKEN")
			if token != "" {
				req.Header.Set("Authorization", "Bearer "+token)
			}

			client := &http.Client{Timeout: 5 * time.Second}
			resp, errDo := client.Do(req)
			if errDo == nil {
				defer resp.Body.Close()
				if resp.StatusCode == http.StatusOK {
					if bodyBytes, errRead := io.ReadAll(resp.Body); errRead == nil {
						hclContent = bodyBytes
						fetchedSuccessfully = true
						log.Printf("[GitHub API] Successfully retrieved %s via REST API (%d bytes)", expectedRelativePath, len(hclContent))
					}
				} else if resp.StatusCode == http.StatusNotFound {
					return fmt.Errorf("tfvars not found on GitHub at %s (HTTP 404). Waiting for DevOps to push...", expectedRelativePath)
				}
			}
		}
	}

	// 4. Fallback if GitHub API was blocked or not configured: Shallow Clone (depth=1)
	if !fetchedSuccessfully {
		repoURL := "https://github.com/oona-insurance/oona-dtc-country-terraform-iac.git"
		gitHubToken := os.Getenv("GITHUB_TOKEN")
		cloneURL := repoURL
		if gitHubToken != "" {
			cloneURL = fmt.Sprintf("https://%s@%s", gitHubToken, repoURL[8:])
		}

		tempWorkspace := filepath.Join(os.TempDir(), fmt.Sprintf("tf-repo-sync-%s", p.TicketID))
		defer os.RemoveAll(tempWorkspace)

		log.Printf("Fallback: Shallow cloning Infra Repo to %s", tempWorkspace)
		cmd := exec.CommandContext(ctx, "git", "clone", "--depth", "1", cloneURL, tempWorkspace)
		if err := cmd.Run(); err != nil {
			return fmt.Errorf("tfvars not found via API and fallback git clone failed: %v", err)
		}

		fullPath := filepath.Join(tempWorkspace, expectedRelativePath)
		src, err := os.ReadFile(fullPath)
		if err != nil {
			return fmt.Errorf("tfvars not found at %s. Waiting for DevOps to push...", expectedRelativePath)
		}
		hclContent = src
	}

	// 5. Parse the ENVs in memory directly from raw HCL bytes
	foundKeys, err := infra.ParseEnvVarsFromBytes(hclContent, "terraform.tfvars")
	if err != nil {
		return fmt.Errorf("found file but failed to parse HCL: %v", err)
	}

	log.Printf("[SUCCESS] Infra Detected for Ticket %s. Found %d ENV keys.", p.TicketID, len(foundKeys))
	
	if api.DB != nil {
		var ticketUUID pgtype.UUID
		if err := ticketUUID.Scan(p.TicketID); err == nil {
			_, err := api.DB.UpdateTicketStatus(ctx, db.UpdateTicketStatusParams{
				ID:     ticketUUID,
				Status: db.TicketStatusINFRADETECTED,
			})
			if err != nil {
				log.Printf("Failed to update ticket status to INFRA_DETECTED: %v", err)
			}
		}
	}

	return nil
}
