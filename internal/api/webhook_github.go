package api

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"os"
	"regexp"
	"strings"
	"sync"
	"time"
)

// WebhookSyncStatus stores the latest GitHub Webhook execution telemetry
type WebhookSyncStatus struct {
	LastEventAt     time.Time `json:"last_event_at"`
	LastCommitSHA   string    `json:"last_commit_sha"`
	LastPusher      string    `json:"last_pusher"`
	ServicesUpdated []string  `json:"services_updated"`
	Status          string    `json:"status"`
}

var (
	GlobalWebhookStatus   WebhookSyncStatus
	GlobalWebhookStatusMu sync.RWMutex
)

// GitHubPushPayload defines the relevant fields from GitHub push webhooks
type GitHubPushPayload struct {
	Ref        string `json:"ref"`
	After      string `json:"after"`
	Pusher     struct {
		Name  string `json:"name"`
		Email string `json:"email"`
	} `json:"pusher"`
	Repository struct {
		Name     string `json:"name"`
		FullName string `json:"full_name"`
	} `json:"repository"`
	Commits []struct {
		ID       string   `json:"id"`
		Message  string   `json:"message"`
		Added    []string `json:"added"`
		Removed  []string `json:"removed"`
		Modified []string `json:"modified"`
	} `json:"commits"`
}

// GitHubWebhookHandler handles incoming git push events from GitHub for IaC repos
func GitHubWebhookHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method Not Allowed", http.StatusMethodNotAllowed)
		return
	}

	event := r.Header.Get("X-GitHub-Event")
	if event == "ping" {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"status":"pong","message":"GitHub Webhook connected successfully"}`))
		return
	}

	body, err := io.ReadAll(r.Body)
	if err != nil {
		slog.Error("Failed to read webhook body", "error", err)
		http.Error(w, "Cannot read payload", http.StatusBadRequest)
		return
	}
	defer r.Body.Close()

	// Verify HMAC signature if GITHUB_WEBHOOK_SECRET is configured
	secret := os.Getenv("GITHUB_WEBHOOK_SECRET")
	sigHeader := r.Header.Get("X-Hub-Signature-256")
	if secret != "" {
		if !verifyGitHubSignature(body, secret, sigHeader) {
			slog.Warn("GitHub webhook signature verification failed", "signature", sigHeader)
			http.Error(w, "Invalid signature", http.StatusUnauthorized)
			return
		}
	}

	var payload GitHubPushPayload
	if err := json.Unmarshal(body, &payload); err != nil {
		slog.Error("Failed to parse GitHub push payload", "error", err)
		http.Error(w, "Invalid JSON payload", http.StatusBadRequest)
		return
	}

	// Regex to extract service name from Oona Monorepo IaC path:
	// "02-app-setup/{domain}/{country}/{env}/services/{service_name}/..."
	pathRegex := regexp.MustCompile(`02-app-setup/[^/]+/[^/]+/[^/]+/services/([^/]+)/`)

	uniqueServices := make(map[string]bool)
	for _, commit := range payload.Commits {
		allFiles := append(commit.Added, commit.Modified...)
		allFiles = append(allFiles, commit.Removed...)

		for _, filePath := range allFiles {
			matches := pathRegex.FindStringSubmatch(filePath)
			if len(matches) > 1 {
				svc := strings.TrimSpace(matches[1])
				if svc != "" {
					uniqueServices[svc] = true
				}
			}
		}
	}

	var affectedList []string
	for svc := range uniqueServices {
		affectedList = append(affectedList, svc)
		// Invalidate RAM cache for this service so next view gets 100% fresh data
		InvalidateServiceCache(svc)
	}

	GlobalWebhookStatusMu.Lock()
	GlobalWebhookStatus = WebhookSyncStatus{
		LastEventAt:     time.Now(),
		LastCommitSHA:   payload.After,
		LastPusher:      payload.Pusher.Name,
		ServicesUpdated: affectedList,
		Status:          "SUCCESS",
	}
	GlobalWebhookStatusMu.Unlock()

	slog.Info("GitHub Webhook processed successfully", 
		"repo", payload.Repository.FullName, 
		"pusher", payload.Pusher.Name,
		"affected_services", affectedList,
	)

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(map[string]interface{}{
		"status":            "success",
		"services_detected": affectedList,
		"message":           "Cache invalidated and sync recorded",
	})
}

// InvalidateServiceCache clears in-memory caches for a specific service
func InvalidateServiceCache(serviceName string) {
	cleanName := strings.TrimSuffix(serviceName, "-clone")
	cleanShortName := strings.TrimPrefix(cleanName, "lmd-oona-ph-integration-")
	cleanShortName = strings.TrimPrefix(cleanShortName, "lmd-oona-id-integration-")
	cleanShortName = strings.TrimPrefix(cleanShortName, "lmd-oona-")

	tfvarsCacheMu.Lock()
	for k := range tfvarsCache {
		if strings.Contains(k, cleanShortName) || strings.Contains(k, serviceName) {
			delete(tfvarsCache, k)
		}
	}
	tfvarsCacheMu.Unlock()

	tfvarsRawContentCacheMu.Lock()
	for k := range tfvarsRawContentCache {
		if strings.Contains(k, cleanShortName) || strings.Contains(k, serviceName) {
			delete(tfvarsRawContentCache, k)
		}
	}
	tfvarsRawContentCacheMu.Unlock()

	slog.Info("Invalidated RAM cache for service", "service", serviceName)
}

func verifyGitHubSignature(payload []byte, secret string, signatureHeader string) bool {
	if !strings.HasPrefix(signatureHeader, "sha256=") {
		return false
	}
	actualSig := strings.TrimPrefix(signatureHeader, "sha256=")
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(payload)
	expectedSig := hex.EncodeToString(mac.Sum(nil))

	return hmac.Equal([]byte(actualSig), []byte(expectedSig))
}
