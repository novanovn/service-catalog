package ci

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestJenkinsEngineJobExists(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/job/AWS%20Lambda%20Projects/job/lmd-oona-ph-integration-health-renewal-svc/api/json", func(w http.ResponseWriter, r *http.Request) {
		user, pass, ok := r.BasicAuth()
		if !ok || user != "svc" || pass != "token" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"name":"lmd-oona-ph-integration-health-renewal-svc"}`))
	})
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Path, "missing-job") {
			http.NotFound(w, r)
			return
		}
		w.WriteHeader(http.StatusInternalServerError)
	})
	ts := httptest.NewServer(mux)
	defer ts.Close()

	eng := NewJenkinsEngine(IntegrationConfig{
		BaseURL:   ts.URL,
		AuthUser:  "svc",
		AuthToken: "token",
	})
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	exists, err := eng.JobExists(ctx, "AWS Lambda Projects", "lmd-oona-ph-integration-health-renewal-svc")
	if err != nil || !exists {
		t.Fatalf("expected existing job, got exists=%v err=%v", exists, err)
	}

	exists, err = eng.JobExists(ctx, "AWS Lambda Projects", "missing-job")
	if err != nil || exists {
		t.Fatalf("expected missing job, got exists=%v err=%v", exists, err)
	}

	exists, err = eng.JobExists(ctx, "AWS Lambda Projects", "broken-job")
	if err == nil || exists {
		t.Fatalf("expected probe error, got exists=%v err=%v", exists, err)
	}
}
