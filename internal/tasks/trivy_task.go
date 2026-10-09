package tasks

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/hibiken/asynq"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/redis/go-redis/v9"
	"service-catalog/internal/api"
	"service-catalog/internal/notify"
	db "service-catalog/internal/repository/postgres/generated"
)

const (
	TypeTrivyScan = "security:trivy_scan"
	// MaxHighVulns defines the maximum allowed HIGH vulnerabilities before triggering policy failure
	MaxHighVulns = 5
)

// TrivyScanPayload holds the data for the background scan
type TrivyScanPayload struct {
	TicketID    string `json:"ticket_id"`
	RepoURL     string `json:"repo_url"`
	ServiceName string `json:"service_name,omitempty"`
}

// TrivyJSONReport models the expected output structure from the trivy CLI tool
type TrivyJSONReport struct {
	Results []struct {
		Target          string `json:"Target"`
		Vulnerabilities []struct {
			VulnerabilityID  string `json:"VulnerabilityID"`
			Severity         string `json:"Severity"`
			InstalledVersion string `json:"InstalledVersion"`
			FixedVersion     string `json:"FixedVersion"`
		} `json:"Vulnerabilities"`
	} `json:"Results"`
}

// NewTrivyScanTask creates the Asynq task to scan a repository
func NewTrivyScanTask(ticketID, repoURL string) (*asynq.Task, error) {
	payload, err := json.Marshal(TrivyScanPayload{
		TicketID: ticketID,
		RepoURL:  repoURL,
	})
	if err != nil {
		return nil, err
	}
	// MaxRetry is low. If Trivy fails 3 times, the repo might be invalid or inaccessible.
	return asynq.NewTask(TypeTrivyScan, payload, asynq.Queue("trivy_scan"), asynq.MaxRetry(3)), nil
}

// HandleTrivyScanTask executes the actual security scanning logic
func HandleTrivyScanTask(ctx context.Context, t *asynq.Task) error {
	var p TrivyScanPayload
	if err := json.Unmarshal(t.Payload(), &p); err != nil {
		return fmt.Errorf("json unmarshal failed: %v: %w", err, asynq.SkipRetry)
	}

	// Read GitHub Token from database integration (or environment fallback)
	githubToken := api.GetActiveGitHubToken(ctx)
	if githubToken == "" {
		log.Println("WARNING: GITHUB_TOKEN is not set. Trivy might fail on private repositories.")
	}

	// Resolve service name if not provided directly
	if p.ServiceName == "" && p.TicketID != "" && api.DB != nil {
		var ticketUUID pgtype.UUID
		if errScan := ticketUUID.Scan(p.TicketID); errScan == nil {
			if tkt, errGet := api.DB.GetTicketByID(ctx, ticketUUID); errGet == nil {
				p.ServiceName = tkt.ServiceName
			}
		}
	}

	log.Printf("Starting Trivy scan for ticket %s (service: %s) on repo %s", p.TicketID, p.ServiceName, p.RepoURL)

	localPath := api.ResolveLocalServiceRepoPath(p.ServiceName, p.RepoURL)

	var cmd *exec.Cmd
	if localPath != "" {
		log.Printf("Trivy fast local scan target found at %s", localPath)
		cmd = exec.CommandContext(ctx, "trivy", "fs",
			"--format", "json",
			"--scanners", "vuln,secret",
			"--skip-db-update",
			"--quiet",
			localPath,
		)
	} else {
		log.Printf("Trivy remote scan targeting %s", p.RepoURL)
		cmd = exec.CommandContext(ctx, "trivy", "repo",
			"--format", "json",
			"--scanners", "vuln,secret",
			"--skip-db-update",
			"--quiet",
			p.RepoURL,
		)
		if githubToken != "" {
			cmd.Env = append(os.Environ(), fmt.Sprintf("GITHUB_TOKEN=%s", githubToken))
		}
	}

	// Trivy will return a non-zero exit code if it finds issues, but we only care about the JSON output
	outputData, err := cmd.Output()
	
	// Check if the command failed because the binary is missing or auth failed
	if err != nil && len(outputData) == 0 {
		return fmt.Errorf("trivy execution failed: %v", err)
	}

	// Cache scan results in Valkey so approval page loads immediately with ready scan data
	if len(outputData) > 0 && p.ServiceName != "" {
		redisAddr := os.Getenv("VALKEY_URL")
		if redisAddr == "" {
			redisAddr = "localhost:6379"
		}
		rdb := redis.NewClient(&redis.Options{
			Addr:     redisAddr,
			Password: os.Getenv("VALKEY_PASSWORD"),
		})
		defer rdb.Close()

		cleanName := strings.TrimSuffix(p.ServiceName, "-clone")
		cacheKeys := []string{
			"trivy:json:" + p.ServiceName + ":main",
			"trivy:json:" + cleanName + ":main",
			"trivy:json:lmd-oona-ph-integration-" + cleanName + ":main",
			"trivy:json:lmd-oona-ph-integration-" + p.ServiceName + ":main",
			"trivy:json:lmd-oona-id-integration-" + cleanName + ":main",
		}
		for _, ck := range cacheKeys {
			_ = rdb.Set(ctx, ck, string(outputData), 2*time.Hour).Err()
		}
	}

	// Parse JSON
	var report TrivyJSONReport
	if err := json.Unmarshal(outputData, &report); err != nil {
		return fmt.Errorf("failed to parse trivy json output: %v", err)
	}

	// Evaluate Security Policy
	criticalCount := 0
	highCount := 0

	for _, res := range report.Results {
		for _, vuln := range res.Vulnerabilities {
			if vuln.Severity == "CRITICAL" {
				criticalCount++
			} else if vuln.Severity == "HIGH" {
				highCount++
			}
		}
	}

	log.Printf("Trivy Scan Results for %s: %d CRITICAL, %d HIGH", p.TicketID, criticalCount, highCount)

	if criticalCount > 0 || highCount > MaxHighVulns {
		// Reject the ticket
		rejectMsg := fmt.Sprintf("rejected: found %d CRITICAL and %d HIGH vulnerabilities (max high allowed: %d)", criticalCount, highCount, MaxHighVulns)
		log.Printf("[REJECTED] Ticket %s security scan failed: %s", p.TicketID, rejectMsg)
		
		if api.DB != nil {
			var ticketUUID pgtype.UUID
			if err := ticketUUID.Scan(p.TicketID); err == nil {
				_, err := api.DB.UpdateTicketStatus(ctx, db.UpdateTicketStatusParams{
					ID:     ticketUUID,
					Status: db.TicketStatusREJECTEDSECURITY,
				})
				if err != nil {
					log.Printf("Failed to update ticket status to REJECTED_SECURITY: %v", err)
				}
			}
		}

		// Enqueue notification task for developer feedback
		title := fmt.Sprintf("Security Scan Failed for Ticket %s", p.TicketID)
		msg := fmt.Sprintf("Repo %s failed Trivy scan: %d CRITICAL, %d HIGH vulnerabilities.", p.RepoURL, criticalCount, highCount)
		color := "FF0000" // Red

		redisAddr := os.Getenv("VALKEY_URL")
		if redisAddr == "" {
			redisAddr = "localhost:6379"
		}
		redisPassword := os.Getenv("VALKEY_PASSWORD")
		client := asynq.NewClient(asynq.RedisClientOpt{
			Addr:     redisAddr,
			Password: redisPassword,
		})
		defer client.Close()

		notifyTask, err := NewNotifyTeamsTask(title, msg, color)
		if err == nil {
			if _, err := client.Enqueue(notifyTask); err != nil {
				log.Printf("Failed to enqueue Teams notification task, falling back to direct send: %v", err)
				_ = notify.SendToTeams(title, msg, color)
			}
		} else {
			_ = notify.SendToTeams(title, msg, color)
		}

		return nil // Return nil because the scan process finished, even though repo failed security policy check
	}

	// Pass the security check!
	log.Printf("[SUCCESS] Repo %s passed security checks. Moving to WAITING_INFRA.", p.RepoURL)
	if api.DB != nil {
		var ticketUUID pgtype.UUID
		if err := ticketUUID.Scan(p.TicketID); err == nil {
			_, err := api.DB.UpdateTicketStatus(ctx, db.UpdateTicketStatusParams{
				ID:     ticketUUID,
				Status: db.TicketStatusWAITINGINFRA,
			})
			if err != nil {
				log.Printf("Failed to update ticket status to WAITING_INFRA: %v", err)
			}
		}
	}

	return nil
}
