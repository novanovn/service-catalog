package tasks

import (
	"context"
	"encoding/json"
	"fmt"
	"log"

	"github.com/hibiken/asynq"
	"service-catalog/internal/docs"
)

const (
	TypeSyncTechDocs = "docs:sync_git"
)

type SyncTechDocsPayload struct {
	ServiceName string `json:"service_name"`
	RepoURL     string `json:"repo_url"`
	DocPath     string `json:"doc_path"`
}

// NewSyncTechDocsTask creates a new Asynq task to background fetch and cache TechDocs
func NewSyncTechDocsTask(svcName, repoURL, docPath string) (*asynq.Task, error) {
	payload, err := json.Marshal(SyncTechDocsPayload{
		ServiceName: svcName,
		RepoURL:     repoURL,
		DocPath:     docPath,
	})
	if err != nil {
		return nil, err
	}
	return asynq.NewTask(TypeSyncTechDocs, payload, asynq.Queue("default"), asynq.MaxRetry(3)), nil
}

// HandleSyncTechDocsTask processes the background TechDocs git sync job
func HandleSyncTechDocsTask(ctx context.Context, t *asynq.Task) error {
	var p SyncTechDocsPayload
	if err := json.Unmarshal(t.Payload(), &p); err != nil {
		return fmt.Errorf("json unmarshal failed: %v: %w", err, asynq.SkipRetry)
	}

	log.Printf("[TechDocs Sync] Pre-fetching TechDocs for service %s (repo: %s, path: %s)", p.ServiceName, p.RepoURL, p.DocPath)

	_, err := docs.FetchTechDoc(ctx, p.RepoURL, p.DocPath)
	if err != nil {
		return fmt.Errorf("techdocs sync failed for service %s: %w", p.ServiceName, err)
	}

	log.Printf("[TechDocs Sync] Successfully synced TechDocs cache for service %s", p.ServiceName)
	return nil
}
