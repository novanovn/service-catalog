package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/hibiken/asynq"
	"github.com/oona-insurance/dev-portal/internal/auth"
	db "github.com/oona-insurance/dev-portal/internal/repository/postgres/generated"
	"github.com/oona-insurance/dev-portal/internal/worker/ci"
)

const defaultJenkinsFolder = "AWS Lambda Projects"

type jenkinsJobProbe struct {
	exists bool
	err    error
	at     time.Time
}

var (
	jenkinsJobProbeCache   = make(map[string]jenkinsJobProbe)
	jenkinsJobProbeCacheMu sync.Mutex
)

func loadJenkinsEngine(ctx context.Context) *ci.JenkinsEngine {
	cfg := ci.IntegrationConfig{
		BaseURL:   os.Getenv("JENKINS_URL"),
		AuthUser:  os.Getenv("JENKINS_USER"),
		AuthToken: os.Getenv("JENKINS_API_TOKEN"),
	}
	if cfg.BaseURL == "" {
		cfg.BaseURL = "https://automation.oona-insurance.com"
	}

	if DB != nil {
		if integrations, err := DB.GetActiveIntegrations(ctx); err == nil {
			for _, ig := range integrations {
				if ig.Provider != db.IntegrationProviderJenkins {
					continue
				}
				cfg.BaseURL = ig.BaseUrl
				if ig.AuthUser.Valid && ig.AuthUser.String != "" {
					cfg.AuthUser = ig.AuthUser.String
				}
				if decrypted, err := auth.Decrypt(ig.AuthToken); err == nil && decrypted != "" {
					cfg.AuthToken = decrypted
				} else if ig.AuthToken != "" {
					cfg.AuthToken = ig.AuthToken
				}
				break
			}
		}
	}

	return ci.NewJenkinsEngine(cfg)
}

func jenkinsJobProbeKey(folder, job string) string {
	if folder == "" {
		folder = defaultJenkinsFolder
	}
	return folder + "|" + job
}

func InvalidateJenkinsJobProbe(folder, job string) {
	jenkinsJobProbeCacheMu.Lock()
	delete(jenkinsJobProbeCache, jenkinsJobProbeKey(folder, job))
	jenkinsJobProbeCacheMu.Unlock()
}

// ProbeJenkinsJob reports whether the Jenkins multibranch job currently exists.
// known=false means Jenkins could not be reached (do not treat as missing).
func ProbeJenkinsJob(ctx context.Context, jobName string) (exists bool, known bool) {
	jobName = strings.TrimSpace(jobName)
	if jobName == "" {
		return false, true
	}

	key := jenkinsJobProbeKey(defaultJenkinsFolder, jobName)
	jenkinsJobProbeCacheMu.Lock()
	if cached, ok := jenkinsJobProbeCache[key]; ok && time.Since(cached.at) < 45*time.Second {
		jenkinsJobProbeCacheMu.Unlock()
		if cached.err != nil {
			return false, false
		}
		return cached.exists, true
	}
	jenkinsJobProbeCacheMu.Unlock()

	probeCtx, cancel := context.WithTimeout(ctx, 2500*time.Millisecond)
	defer cancel()

	exists, err := loadJenkinsEngine(probeCtx).JobExists(probeCtx, defaultJenkinsFolder, jobName)
	jenkinsJobProbeCacheMu.Lock()
	jenkinsJobProbeCache[key] = jenkinsJobProbe{exists: exists, err: err, at: time.Now()}
	jenkinsJobProbeCacheMu.Unlock()
	if err != nil {
		return false, false
	}
	return exists, true
}

func findCatalogService(param string) (CatalogEntry, bool) {
	param = strings.TrimSpace(param)
	if param == "" {
		return CatalogEntry{}, false
	}
	for _, e := range ServiceCatalog.ListAll() {
		if e.ID == param || strings.EqualFold(e.Name, param) {
			return e, true
		}
	}
	return CatalogEntry{}, false
}

func enqueueCreatePipeline(pipelineName, folder, repoURL, triggeredBy string) {
	if folder == "" {
		folder = defaultJenkinsFolder
	}
	redisAddr := os.Getenv("VALKEY_URL")
	if redisAddr == "" {
		redisAddr = "localhost:6379"
	}
	client := asynq.NewClient(asynq.RedisClientOpt{
		Addr:     redisAddr,
		Password: os.Getenv("VALKEY_PASSWORD"),
	})
	defer client.Close()

	payload, _ := json.Marshal(struct {
		PipelineName  string
		JenkinsFolder string
		RepoURL       string
		TriggeredBy   string
	}{
		PipelineName:  pipelineName,
		JenkinsFolder: folder,
		RepoURL:       repoURL,
		TriggeredBy:   triggeredBy,
	})
	task := asynq.NewTask("ci:create_pipeline", payload, asynq.Queue("aws_jenkins"), asynq.MaxRetry(3))
	_, _ = client.Enqueue(task)
}

// RecreatePipelineHandler re-creates a deleted Jenkins multibranch job from an existing catalog entry.
// Admin only. Job name is taken from terraform.tfvars existing_github_repo_id, not tickets.pipeline_name.
func RecreatePipelineHandler(w http.ResponseWriter, r *http.Request) {
	serviceParam := strings.TrimSpace(chi.URLParam(r, "service"))
	if serviceParam == "" {
		http.Error(w, "service is required", http.StatusBadRequest)
		return
	}

	entry, found := findCatalogService(serviceParam)
	if !found {
		http.Error(w, "catalog service not found", http.StatusNotFound)
		return
	}

	pipelineName := ResolveCanonicalPipelineName(r.Context(), entry.Domain, entry.Country, entry.Name, "main", entry.PipelineName)
	if pipelineName == "" {
		http.Error(w, "unable to resolve jenkins job name from terraform.tfvars", http.StatusBadRequest)
		return
	}

	claims, _ := r.Context().Value(userCtxKey).(*auth.Claims)
	triggeredBy := "admin"
	if claims != nil && claims.Email != "" {
		triggeredBy = claims.Email
	}

	repoURL := entry.RepoURL
	if repoURL == "" {
		repoURL = fmt.Sprintf("https://github.com/oona-insurance/%s", pipelineName)
	}

	enqueueCreatePipeline(pipelineName, defaultJenkinsFolder, repoURL, triggeredBy)
	InvalidateJenkinsJobProbe(defaultJenkinsFolder, pipelineName)
	RecordAudit(r.Context(), r, "RECREATE", "jenkins_pipeline", entry.Name, map[string]interface{}{
		"pipeline_name": pipelineName,
		"repo_url":      repoURL,
	})

	msg := fmt.Sprintf("Jenkins job %s queued for recreation.", pipelineName)
	w.Header().Set("HX-Trigger", fmt.Sprintf(`{"showToast": {"message": %q, "type": "success"}}`, msg))
	if r.Header.Get("HX-Request") == "true" {
		w.Header().Set("HX-Redirect", "/catalog/"+entry.Name+"?toast=pipeline-requeued")
	}
	w.WriteHeader(http.StatusOK)
}
