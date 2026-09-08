package api

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
)

func TestGitHubWebhookHandler_Ping(t *testing.T) {
	req := httptest.NewRequest(http.MethodPost, "/api/v1/webhooks/github", bytes.NewBufferString(`{}`))
	req.Header.Set("X-GitHub-Event", "ping")
	rec := httptest.NewRecorder()

	GitHubWebhookHandler(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 OK for ping, got %d", rec.Code)
	}
}

func TestGitHubWebhookHandler_PushAndSignature(t *testing.T) {
	secret := "test-webhook-secret-123"
	os.Setenv("GITHUB_WEBHOOK_SECRET", secret)
	defer os.Unsetenv("GITHUB_WEBHOOK_SECRET")

	payload := `{
		"ref": "refs/heads/main",
		"after": "abc1234567890",
		"pusher": { "name": "devops-engineer" },
		"repository": { "full_name": "oona-insurance/oona-dtc-country-terraform-iac" },
		"commits": [
			{
				"id": "abc1234567890",
				"message": "feat: update health renewal config",
				"modified": [
					"02-app-setup/integration/ph/uat/services/health-renewal-svc-clone/terraform.tfvars"
				]
			}
		]
	}`

	// Compute HMAC
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(payload))
	sig := "sha256=" + hex.EncodeToString(mac.Sum(nil))

	req := httptest.NewRequest(http.MethodPost, "/api/v1/webhooks/github", bytes.NewBufferString(payload))
	req.Header.Set("X-GitHub-Event", "push")
	req.Header.Set("X-Hub-Signature-256", sig)
	rec := httptest.NewRecorder()

	GitHubWebhookHandler(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 OK, got %d. Body: %s", rec.Code, rec.Body.String())
	}

	GlobalWebhookStatusMu.RLock()
	defer GlobalWebhookStatusMu.RUnlock()

	if GlobalWebhookStatus.LastPusher != "devops-engineer" {
		t.Errorf("expected pusher devops-engineer, got %s", GlobalWebhookStatus.LastPusher)
	}
	if len(GlobalWebhookStatus.ServicesUpdated) != 1 || GlobalWebhookStatus.ServicesUpdated[0] != "health-renewal-svc-clone" {
		t.Errorf("expected service health-renewal-svc-clone detected, got %v", GlobalWebhookStatus.ServicesUpdated)
	}
}
