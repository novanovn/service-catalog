package api

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

// LambdaFunctionRef is one deployed AWS Lambda declared in a service repo's
// package.json `functions` manifest.
//
// The neuron-claude-ai-standard-boilerplate template writes this manifest into
// every scaffolded service. For a multi-function repo the manifest is the only
// place that maps the repo to the real Lambda names Terraform will create —
// those names are NOT derivable from the repo name. Example, for repo
// lmd-oona-ph-integration-demo-quote-svc:
//
//	demo-quote-svc-calculator-v1-svc   (handler dist/index.demoQuoteCalculatorHandler)
//	demo-quote-svc-fetcher-v1-hdr      (handler dist/index.demoQuoteFetcherHandler)
type LambdaFunctionRef struct {
	// Key is the manifest key, e.g. "calculator-svc". Identifier only.
	Key string `json:"key"`
	// Name is the real AWS Lambda function name devops' Terraform must create.
	Name string `json:"name"`
	// Handler is the built entry point, e.g. "dist/index.demoQuoteCalculatorHandler".
	Handler string `json:"handler"`
}

// ParseLambdaFunctionsFromPackageJSON extracts the `functions` manifest from a
// service repo's package.json.
//
// It returns nil for any input it cannot use — malformed JSON, a missing or
// empty `functions` field, or a `functions` value that is not an object. Callers
// MUST treat nil as "unknown, fall back to the legacy single-name behaviour"
// rather than as an error: a repo whose manifest cannot be read must never be
// blocked from approval on that basis alone.
//
// Individual unusable entries (no name, blank name) are skipped rather than
// discarding the whole manifest, so one bad entry cannot hide its valid siblings.
//
// Results are sorted by Name because Go randomises map iteration order, and an
// unstable order would make the Pre-Flight UI reshuffle between reloads.
func ParseLambdaFunctionsFromPackageJSON(data []byte) []LambdaFunctionRef {
	if len(data) == 0 {
		return nil
	}

	var pkg struct {
		Functions map[string]struct {
			Name    string `json:"name"`
			Handler string `json:"handler"`
		} `json:"functions"`
	}
	// A non-object `functions` value (array, string, number) fails here and is
	// treated the same as a missing manifest.
	if err := json.Unmarshal(data, &pkg); err != nil {
		return nil
	}

	refs := make([]LambdaFunctionRef, 0, len(pkg.Functions))
	for key, fn := range pkg.Functions {
		name := strings.TrimSpace(fn.Name)
		if name == "" {
			// Without a deployable name there is nothing to verify against AWS.
			continue
		}
		refs = append(refs, LambdaFunctionRef{
			Key:     strings.TrimSpace(key),
			Name:    name,
			Handler: strings.TrimSpace(fn.Handler),
		})
	}

	if len(refs) == 0 {
		return nil
	}

	sort.Slice(refs, func(i, j int) bool { return refs[i].Name < refs[j].Name })
	return refs
}

// ── Fetch package.json from a service repo (local mount → GitHub API) ─────

// lambdaFnCacheEntry wraps a result so we can cache nil (repo not found /
// no manifest) without ambiguity — a nil entry means "not yet looked up",
// an entry with Refs == nil means "looked up, nothing usable".
type lambdaFnCacheEntry struct {
	Refs []LambdaFunctionRef
}

var (
	lambdaFnCacheMu sync.RWMutex
	lambdaFnCache   = make(map[string]*lambdaFnCacheEntry)
)

// FetchLambdaFunctionsForRepo resolves a service repo's declared Lambda
// functions by reading its package.json.
//
// Resolution order (mirrors FetchExistingRepoIDFromTFVars):
//  1. Local checkout: SERVICE_REPOS_DIR / /service-repos / ../
//  2. GitHub Contents API (raw): oona-insurance/{repoName}/contents/package.json
//
// The result is cached for the process lifetime — package.json does not
// change between Pre-Flight recheck clicks within a single approval session.
//
// Returns nil when the manifest is absent, unreadable, or unparseable.
// Callers MUST treat nil as "use legacy single-name behaviour" — a fetch
// failure must never block an approval.
func FetchLambdaFunctionsForRepo(ctx context.Context, repoName string) []LambdaFunctionRef {
	repoName = strings.TrimSpace(repoName)
	if repoName == "" {
		return nil
	}

	// ── Cache check ──────────────────────────────────────────────────
	lambdaFnCacheMu.RLock()
	if entry, found := lambdaFnCache[repoName]; found {
		lambdaFnCacheMu.RUnlock()
		return entry.Refs // may be nil — that's a cached "not found"
	}
	lambdaFnCacheMu.RUnlock()

	refs := fetchLambdaFunctionsUncached(ctx, repoName)

	// ── Cache store (including nil) ──────────────────────────────────
	lambdaFnCacheMu.Lock()
	lambdaFnCache[repoName] = &lambdaFnCacheEntry{Refs: refs}
	lambdaFnCacheMu.Unlock()

	return refs
}

// fetchLambdaFunctionsUncached does the actual I/O: local filesystem first,
// then GitHub API. Separated from the cache layer for testability.
func fetchLambdaFunctionsUncached(ctx context.Context, repoName string) []LambdaFunctionRef {
	var data []byte

	// ── 1. Try local checkout ────────────────────────────────────────
	localDirs := []string{
		os.Getenv("SERVICE_REPOS_DIR"),
		"/repo/demo",
		"/repo/PH",
		"/repo/ID",
		"/repo",
		"/service-repos",
		"../repo/demo",
		"../repo/PH",
		"../repo/ID",
		"../repo",
		"../",
	}
	for _, dir := range localDirs {
		if dir == "" {
			continue
		}
		candidate := filepath.Join(dir, repoName, "package.json")
		if fileData, err := os.ReadFile(candidate); err == nil && len(fileData) > 0 {
			data = fileData
			break
		}
	}

	// ── 2. Fall back to GitHub Contents API ──────────────────────────
	if len(data) == 0 {
		baseURL := os.Getenv("SERVICE_REPO_GITHUB_BASE_URL")
		if baseURL == "" {
			baseURL = "https://api.github.com/repos/oona-insurance"
		}
		targetURL := fmt.Sprintf("%s/%s/contents/package.json?ref=main", baseURL, repoName)

		req, err := http.NewRequestWithContext(ctx, http.MethodGet, targetURL, nil)
		if err != nil {
			return nil
		}
		req.Header.Set("User-Agent", "Oona-Dev-Portal/1.0")
		req.Header.Set("Accept", "application/vnd.github.v3.raw")
		if token := os.Getenv("GITHUB_TOKEN"); token != "" {
			req.Header.Set("Authorization", "Bearer "+token)
		}

		client := &http.Client{Timeout: 3 * time.Second}
		resp, doErr := client.Do(req)
		if doErr != nil {
			return nil
		}
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			return nil
		}
		data, _ = io.ReadAll(resp.Body)
	}

	return ParseLambdaFunctionsFromPackageJSON(data)
}
