package api

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

// ── Local mount tests (no HTTP) ────────────────────────────────────────────

func TestFetchLambdaFunctionsForRepo_LocalMount(t *testing.T) {
	// Set up a fake repo checkout with a minimal package.json.
	tmpDir := t.TempDir()
	repoDir := filepath.Join(tmpDir, "lmd-oona-ph-integration-demo-quote-svc")
	if err := os.MkdirAll(repoDir, 0o755); err != nil {
		t.Fatal(err)
	}
	pkgJSON := `{
	  "functions": {
	    "calc-svc": { "name": "demo-quote-svc-calculator-v1-svc", "handler": "dist/index.calcHandler" }
	  }
	}`
	if err := os.WriteFile(filepath.Join(repoDir, "package.json"), []byte(pkgJSON), 0o644); err != nil {
		t.Fatal(err)
	}

	// Reset the package-level cache so earlier test runs don't interfere.
	resetLambdaFnCache()

	// Point SERVICE_REPOS_DIR at the tmpdir so the local-mount path is exercised.
	t.Setenv("SERVICE_REPOS_DIR", tmpDir)

	got := FetchLambdaFunctionsForRepo(context.Background(), "lmd-oona-ph-integration-demo-quote-svc")
	if len(got) != 1 {
		t.Fatalf("expected 1 function from local mount, got %d", len(got))
	}
	if got[0].Name != "demo-quote-svc-calculator-v1-svc" {
		t.Errorf("name mismatch: %q", got[0].Name)
	}
}

// ── GitHub API fallback tests ──────────────────────────────────────────────

func TestFetchLambdaFunctionsForRepo_GitHubFallback(t *testing.T) {
	resetLambdaFnCache()

	// No local mount — SERVICE_REPOS_DIR empty.
	t.Setenv("SERVICE_REPOS_DIR", "")

	hitCount := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hitCount++
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{
		  "functions": {
		    "svc-a": { "name": "fn-a-v1-svc", "handler": "dist/index.aHandler" },
		    "svc-b": { "name": "fn-b-v1-svc", "handler": "dist/index.bHandler" }
		  }
		}`))
	}))
	defer srv.Close()

	// Override the GitHub base URL so we don't hit the real API.
	t.Setenv("SERVICE_REPO_GITHUB_BASE_URL", srv.URL)

	got := FetchLambdaFunctionsForRepo(context.Background(), "lmd-oona-ph-integration-test")
	if len(got) != 2 {
		t.Fatalf("expected 2 functions via GitHub fallback, got %d", len(got))
	}
	if hitCount != 1 {
		t.Errorf("expected exactly 1 HTTP hit, got %d", hitCount)
	}
}

func TestFetchLambdaFunctionsForRepo_GitHubNotFound(t *testing.T) {
	resetLambdaFnCache()
	t.Setenv("SERVICE_REPOS_DIR", "")

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()
	t.Setenv("SERVICE_REPO_GITHUB_BASE_URL", srv.URL)

	got := FetchLambdaFunctionsForRepo(context.Background(), "no-such-repo")
	if got != nil {
		t.Errorf("expected nil for 404 repo, got %d entries", len(got))
	}
}

// ── Cache tests ────────────────────────────────────────────────────────────

func TestFetchLambdaFunctionsForRepo_CachePreventsSecondHTTPHit(t *testing.T) {
	resetLambdaFnCache()
	t.Setenv("SERVICE_REPOS_DIR", "")

	hitCount := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		hitCount++
		w.Write([]byte(`{"functions":{"x":{"name":"fn-x","handler":"dist/index.x"}}}`))
	}))
	defer srv.Close()
	t.Setenv("SERVICE_REPO_GITHUB_BASE_URL", srv.URL)

	ctx := context.Background()
	FetchLambdaFunctionsForRepo(ctx, "cached-repo")
	FetchLambdaFunctionsForRepo(ctx, "cached-repo")
	FetchLambdaFunctionsForRepo(ctx, "cached-repo")

	if hitCount != 1 {
		t.Errorf("cache should prevent repeated HTTP hits; got %d", hitCount)
	}
}

func TestFetchLambdaFunctionsForRepo_CachesNilToo(t *testing.T) {
	resetLambdaFnCache()
	t.Setenv("SERVICE_REPOS_DIR", "")

	hitCount := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		hitCount++
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()
	t.Setenv("SERVICE_REPO_GITHUB_BASE_URL", srv.URL)

	ctx := context.Background()
	FetchLambdaFunctionsForRepo(ctx, "absent-repo")
	FetchLambdaFunctionsForRepo(ctx, "absent-repo")

	if hitCount != 1 {
		t.Errorf("nil results must also be cached; got %d hits", hitCount)
	}
}

// ── Helpers ────────────────────────────────────────────────────────────────

// resetLambdaFnCache clears the package-level cache between tests.
// It accesses the unexported variables declared in lambda_functions.go.
func resetLambdaFnCache() {
	lambdaFnCacheMu.Lock()
	lambdaFnCache = make(map[string]*lambdaFnCacheEntry)
	lambdaFnCacheMu.Unlock()
}

// Export for test: the variables below must match the ones in lambda_functions.go.
// (They are in the same package, so this compiles.)
