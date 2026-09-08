package tasks

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/hibiken/asynq"
)

func TestHandleVerifyInfraGitTask(t *testing.T) {
	// 1. Setup Mock Environment
	tempDir := t.TempDir()
	os.Setenv("OONA_TF_REPO_PATH", tempDir)
	defer os.Unsetenv("OONA_TF_REPO_PATH")

	// Create Oona standard directory structure
	serviceDir := filepath.Join(tempDir, "02-app-setup", "integration", "ph", "uat", "services", "health-renewal-svc")
	err := os.MkdirAll(serviceDir, 0755)
	if err != nil {
		t.Fatalf("Failed to create mock dir: %v", err)
	}

	// Create dummy tfvars file
	tfvarsPath := filepath.Join(serviceDir, "terraform.tfvars")
	mockHCL := `
	functions = {
		"func" = {
			env_vars = {
				DB_HOST = "localhost"
			}
		}
	}
	`
	err = os.WriteFile(tfvarsPath, []byte(mockHCL), 0644)
	if err != nil {
		t.Fatalf("Failed to write mock tfvars: %v", err)
	}

	// 2. Prepare Task Payload
	payload, _ := json.Marshal(VerifyInfraPayload{
		TicketID:      "TKT-TEST",
		Domain:        "integration",
		Country:       "ph",
		Env:           "uat",
		ServiceName:   "health-renewal-svc",
		IntegrationID: "MOCK-INT-123",
	})
	task := asynq.NewTask(TypeVerifyInfraGit, payload)

	// 3. Execute Handler
	err = HandleVerifyInfraGitTask(context.Background(), task)

	// 4. Assert Success
	if err != nil {
		t.Errorf("Expected nil error (success), got: %v", err)
	}
}

func TestHandleVerifyInfraGitTask_NotFound(t *testing.T) {
	tempDir := t.TempDir()
	os.Setenv("OONA_TF_REPO_PATH", tempDir) // Empty dir, no tfvars

	payload, _ := json.Marshal(VerifyInfraPayload{
		TicketID:      "TKT-TEST-MISSING",
		Domain:        "integration",
		Country:       "id",
		Env:           "uat",
		ServiceName:   "missing-svc",
		IntegrationID: "MOCK-INT-123",
	})
	task := asynq.NewTask(TypeVerifyInfraGit, payload)

	err := HandleVerifyInfraGitTask(context.Background(), task)

	// Assert Failure (Should return error to trigger Asynq retry)
	if err == nil {
		t.Error("Expected error because tfvars is missing, got nil")
	}
}
