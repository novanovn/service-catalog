package docs

import (
	"context"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestRenderMarkdownToHTML(t *testing.T) {
	inputMD := []byte(`
# Overview Heading

Welcome to Oona Dev Portal.

## Feature List
| Feature | Status |
| --- | --- |
| TechDocs | Active |

### Tasks
* [x] GFM Support
* [ ] Advanced Search
`)

	html, err := RenderMarkdownToHTML(inputMD)
	if err != nil {
		t.Fatalf("RenderMarkdownToHTML failed: %v", err)
	}

	// Check HTML output contains auto heading IDs
	if !strings.Contains(html, `id="overview-heading"`) {
		t.Errorf("Expected auto heading ID 'overview-heading' in HTML, got:\n%s", html)
	}
	if !strings.Contains(html, `id="feature-list"`) {
		t.Errorf("Expected auto heading ID 'feature-list' in HTML, got:\n%s", html)
	}

	// Check table rendering (GFM extension)
	if !strings.Contains(html, "<table>") || !strings.Contains(html, "<th>Feature</th>") {
		t.Errorf("Expected HTML table from GFM extension, got:\n%s", html)
	}

	// Check task list rendering (GFM extension)
	if !strings.Contains(html, `type="checkbox"`) {
		t.Errorf("Expected task list checkboxes in HTML, got:\n%s", html)
	}
}

func TestExtractTOC(t *testing.T) {
	inputMD := []byte(`
# Main Title
Intro text

## Getting Started
Some setup steps...

### Prerequisites
Node.js and Go

#### Ignored Deep Heading
This should not appear in TOC.

## Getting Started
Duplicate heading test.
`)

	toc := ExtractTOC(inputMD)

	if len(toc) != 4 {
		t.Fatalf("Expected 4 TOC items (excluding h4), got %d items", len(toc))
	}

	// Item 0: # Main Title
	if toc[0].Level != 1 || toc[0].Title != "Main Title" || toc[0].ID != "main-title" {
		t.Errorf("Unexpected TOC item 0: %+v", toc[0])
	}

	// Item 1: ## Getting Started
	if toc[1].Level != 2 || toc[1].Title != "Getting Started" || toc[1].ID != "getting-started" {
		t.Errorf("Unexpected TOC item 1: %+v", toc[1])
	}

	// Item 2: ### Prerequisites
	if toc[2].Level != 3 || toc[2].Title != "Prerequisites" || toc[2].ID != "prerequisites" {
		t.Errorf("Unexpected TOC item 2: %+v", toc[2])
	}

	// Item 3: ## Getting Started (Duplicate)
	if toc[3].Level != 2 || toc[3].Title != "Getting Started" || !strings.HasPrefix(toc[3].ID, "getting-started") {
		t.Errorf("Unexpected TOC item 3 for duplicate heading: %+v", toc[3])
	}
}

func TestFetchTechDocCachingAndTTL(t *testing.T) {
	cache := NewTechDocsCache(50 * time.Millisecond)

	key := "test-repo:README.md"
	content := []byte("# Cached Content")

	// Set cache entry
	cache.Set(key, content)

	// Immediate Get should succeed
	got, ok := cache.Get(key)
	if !ok || string(got) != string(content) {
		t.Fatalf("Expected cache hit, got ok=%v content=%s", ok, string(got))
	}

	// Wait for TTL expiry
	time.Sleep(60 * time.Millisecond)

	// Get after TTL should expire
	_, ok = cache.Get(key)
	if ok {
		t.Fatalf("Expected cache entry to be expired after TTL")
	}

	// Test Clear
	cache.Set(key, content)
	cache.Clear()
	if _, ok := cache.Get(key); ok {
		t.Fatalf("Expected cache to be empty after Clear()")
	}
}

func TestFetchTechDocLocalFile(t *testing.T) {
	tmpDir := t.TempDir()
	docPath := "DOCS.md"
	fullPath := filepath.Join(tmpDir, docPath)

	expectedContent := []byte("# Local File Documentation\nHello world.")
	if err := os.WriteFile(fullPath, expectedContent, 0644); err != nil {
		t.Fatalf("Failed to write temp doc file: %v", err)
	}

	cache := NewTechDocsCache(1 * time.Minute)
	data, err := FetchTechDocWithCache(context.Background(), tmpDir, docPath, cache)
	if err != nil {
		t.Fatalf("FetchTechDocWithCache failed for local file: %v", err)
	}

	if string(data) != string(expectedContent) {
		t.Errorf("Expected content %q, got %q", string(expectedContent), string(data))
	}

	// Verify cached
	cached, ok := cache.Get(tmpDir + ":" + docPath)
	if !ok || string(cached) != string(expectedContent) {
		t.Errorf("Expected local file content to be cached")
	}
}

func TestFetchTechDocHTTPAndFallback(t *testing.T) {
	// Mock HTTP Server using IPv4 listener to avoid sandbox IPv6 bind restriction
	l, err := net.Listen("tcp4", "127.0.0.1:0")
	if err == nil {
		server := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path == "/README.md" {
				w.WriteHeader(http.StatusOK)
				w.Write([]byte("# Remote HTTP TechDoc"))
				return
			}
			w.WriteHeader(http.StatusNotFound)
		}))
		server.Listener = l
		server.Start()
		defer server.Close()

		cache := NewTechDocsCache(1 * time.Minute)

		// Fetch from HTTP mock server
		data, err := FetchTechDocWithCache(context.Background(), server.URL, "README.md", cache)
		if err != nil {
			t.Fatalf("FetchTechDocWithCache failed for HTTP mock: %v", err)
		}
		if !strings.Contains(string(data), "# Remote HTTP TechDoc") {
			t.Errorf("Expected remote HTTP content, got: %s", string(data))
		}
	} else {
		t.Logf("Skipping live HTTP server test due to local net listener error: %v", err)
	}

	// Fallback test: invalid / offline URL should return safe default content
	cache := NewTechDocsCache(1 * time.Minute)
	fallbackData, err := FetchTechDocWithCache(context.Background(), "https://invalid-non-existent-domain-12345.com", "README.md", cache)
	if err != nil {
		t.Fatalf("FetchTechDocWithCache error on offline fallback: %v", err)
	}

	if !strings.Contains(string(fallbackData), "Oona Health Renewal Service") || !strings.Contains(string(fallbackData), "Offline fallback documentation loaded") {
		t.Errorf("Expected safe default fallback content, got:\n%s", string(fallbackData))
	}
}
