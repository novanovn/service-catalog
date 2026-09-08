package api

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/oona-insurance/dev-portal/internal/auth"
)

func TestJenkinsJobNameFromRepoID(t *testing.T) {
	t.Parallel()

	cases := map[string]string{
		"lmd-oona-ph-integration-health-renewal-svc":                         "lmd-oona-ph-integration-health-renewal-svc",
		"oona-insurance/lmd-oona-ph-integration-health-renewal-svc":          "lmd-oona-ph-integration-health-renewal-svc",
		"oona-insurance/change-me-repo":                                      "",
		"change-me-repo":                                                     "",
		"":                                                                   "",
		"  oona-insurance/lmd-oona-ph-integration-health-renewal-svc-clone  ": "lmd-oona-ph-integration-health-renewal-svc-clone",
	}
	for in, want := range cases {
		if got := JenkinsJobNameFromRepoID(in); got != want {
			t.Errorf("JenkinsJobNameFromRepoID(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestRecreatePipelineAdminOnly(t *testing.T) {
	router := SetupRouter()
	ts := httptest.NewServer(router)
	defer ts.Close()

	ServiceCatalog.Add(CatalogEntry{
		ID:           "svc-recreate-test",
		Name:         "health-renewal-svc-clone",
		Domain:       "Integration",
		Country:      "PH",
		RepoURL:      "https://github.com/oona-insurance/lmd-oona-ph-integration-health-renewal-svc-clone",
		PipelineName: "lmd-oona-PH-integration-health-renewal-svc-clone",
		Status:       "LIVE",
	})

	csrf := GenerateCSRFToken()
	csrfCookie := &http.Cookie{Name: "oona_csrf", Value: csrf, Path: "/"}

	post := func(role string) *http.Response {
		t.Helper()
		token, err := auth.GenerateToken(role+"-user", role+"@oona-insurance.com", role)
		if err != nil {
			t.Fatalf("token: %v", err)
		}
		req, _ := http.NewRequest(http.MethodPost, ts.URL+"/api/v1/catalog/health-renewal-svc-clone/recreate-pipeline", nil)
		req.AddCookie(&http.Cookie{Name: "oona_token", Value: token, Path: "/"})
		req.AddCookie(csrfCookie)
		req.Header.Set("X-CSRF-Token", csrf)
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatalf("POST recreate-pipeline as %s: %v", role, err)
		}
		return resp
	}

	for _, role := range []string{"developer", "infra", "devops"} {
		resp := post(role)
		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		if resp.StatusCode != http.StatusForbidden {
			t.Errorf("%s recreate got HTTP %d, want 403; body=%s", role, resp.StatusCode, strings.TrimSpace(string(body)))
		}
	}

	adminResp := post("admin")
	body, _ := io.ReadAll(adminResp.Body)
	adminResp.Body.Close()
	if adminResp.StatusCode != http.StatusOK {
		t.Fatalf("admin recreate got HTTP %d, want 200; body=%s", adminResp.StatusCode, strings.TrimSpace(string(body)))
	}
}
