package api

import (
	"context"
	"os"
	"sync"
	"time"

	"service-catalog/internal/auth"
	db "service-catalog/internal/repository/postgres/generated"
)

var (
	activeGitHubTokenCache    string
	activeGitHubTokenCachedAt time.Time
	activeGitHubTokenCacheMu  sync.RWMutex
)

// InvalidateGitHubTokenCache clears the in-memory cached GitHub token so updates in admin UI reflect immediately
func InvalidateGitHubTokenCache() {
	activeGitHubTokenCacheMu.Lock()
	activeGitHubTokenCache = ""
	activeGitHubTokenCacheMu.Unlock()
}

// GetActiveGitHubToken returns the decrypted GitHub personal access token configured in the database.
// Resolution order:
//  1. In-memory cache (5 min TTL)
//  2. Database integrations table where provider is github_actions or terraform_repo (decrypted via AES-256)
//  3. GITHUB_TOKEN environment variable fallback
func GetActiveGitHubToken(ctx context.Context) string {
	activeGitHubTokenCacheMu.RLock()
	if activeGitHubTokenCache != "" && time.Since(activeGitHubTokenCachedAt) < 5*time.Minute {
		token := activeGitHubTokenCache
		activeGitHubTokenCacheMu.RUnlock()
		return token
	}
	activeGitHubTokenCacheMu.RUnlock()

	var token string
	if DB != nil {
		if items, err := DB.GetActiveIntegrations(ctx); err == nil {
			for _, ig := range items {
				if ig.Provider == db.IntegrationProviderGithubActions || ig.Provider == db.IntegrationProviderTerraformRepo {
					if dec, errDec := auth.Decrypt(ig.AuthToken); errDec == nil && dec != "" {
						token = dec
						break
					}
				}
			}
		}
	}

	if token == "" {
		token = os.Getenv("GITHUB_TOKEN")
	}

	if token != "" {
		activeGitHubTokenCacheMu.Lock()
		activeGitHubTokenCache = token
		activeGitHubTokenCachedAt = time.Now()
		activeGitHubTokenCacheMu.Unlock()
	}

	return token
}
