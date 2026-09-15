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
	"github.com/jackc/pgx/v5/pgtype"
	"service-catalog/internal/auth"
	db "service-catalog/internal/repository/postgres/generated"
)

type TicketReviewRecord struct {
	TicketID             string    `json:"ticket_id"`
	Status               string    `json:"status"` // APPROVED, REJECTED
	Comment              string    `json:"comment"`
	ReviewedBy           string    `json:"reviewed_by"`
	ReviewedAt           time.Time `json:"reviewed_at"`
	SecurityAcknowledged bool      `json:"security_acknowledged"`
	SecurityNotes        string    `json:"security_notes"`
}

type TicketReviewStore struct {
	mu      sync.RWMutex
	reviews map[string]*TicketReviewRecord
}

var TicketReviews = &TicketReviewStore{
	reviews: make(map[string]*TicketReviewRecord),
}

func (s *TicketReviewStore) Set(record *TicketReviewRecord) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.reviews[record.TicketID] = record
}

func (s *TicketReviewStore) Get(ticketID string) (*TicketReviewRecord, bool) {
	s.mu.RLock()
	rec, ok := s.reviews[ticketID]
	s.mu.RUnlock()
	if ok && rec != nil {
		return rec, true
	}

	if DB != nil {
		var ticketUUID pgtype.UUID
		if err := ticketUUID.Scan(ticketID); err == nil {
			if review, err := DB.GetTicketReviewByTicketID(context.Background(), ticketUUID); err == nil {
				record := &TicketReviewRecord{
					TicketID:             ticketID,
					Status:               review.Status,
					Comment:              review.Comment,
					ReviewedBy:           review.ReviewedBy,
					ReviewedAt:           review.CreatedAt.Time,
					SecurityAcknowledged: review.SecurityAcknowledged,
					SecurityNotes:        review.SecurityNotes,
				}
				s.Set(record)
				return record, true
			}
		}
	}
	return nil, false
}

// ApproveTicketHandler handles approving ticket and queueing CI/CD pipeline creation
func ApproveTicketHandler(w http.ResponseWriter, r *http.Request) {
	ticketID := chi.URLParam(r, "id")
	if ticketID == "" {
		http.Error(w, `{"error": "ticket ID is required"}`, http.StatusBadRequest)
		return
	}

	_ = r.ParseForm()
	reviewComment := strings.TrimSpace(r.FormValue("comment"))
	securityAck := r.FormValue("security_ack") == "true" || r.FormValue("security_ack") == "on" || r.FormValue("security_ack") == "1"
	securityNotes := strings.TrimSpace(r.FormValue("security_notes"))

	var userRole string
	var userEmail string

	if roleVal, ok := r.Context().Value(auth.UserRoleKey).(string); ok && roleVal != "" {
		userRole = roleVal
	}

	claims, hasClaims := r.Context().Value(userCtxKey).(*auth.Claims)
	if hasClaims {
		if userRole == "" {
			userRole = claims.Role
		}
		userEmail = claims.Email
	}

	if userRole != "devops" && userRole != "admin" && userRole != "infra" {
		http.Error(w, "Forbidden: infra, devops or admin role required for ticket approval", http.StatusForbidden)
		return
	}

	triggeredBy := userEmail
	if triggeredBy == "" {
		triggeredBy = userRole
	}

	jenkinsFolder := strings.TrimSpace(r.FormValue("jenkins_folder"))
	if jenkinsFolder == "" {
		jenkinsFolder = "AWS Lambda Projects"
	}

	// Save Review Record with Security Risk Acknowledgement in-memory
	TicketReviews.Set(&TicketReviewRecord{
		TicketID:             ticketID,
		Status:               "APPROVED",
		Comment:              reviewComment,
		ReviewedBy:           triggeredBy,
		ReviewedAt:           time.Now(),
		SecurityAcknowledged: securityAck,
		SecurityNotes:        securityNotes,
	})

	ServiceCatalog.UpdateStatus(ticketID, "LIVE")

	var pipelineName string
	var repoURL string
	var integrationID string
	var ticketServiceName string
	var ticketDomain string
	var ticketCountry string

	if DB != nil {
		var ticketUUID pgtype.UUID
		if err := ticketUUID.Scan(ticketID); err == nil {
			if DBPool != nil {
				// Execute atomically in a database transaction
				tx, txErr := DBPool.Begin(r.Context())
				if txErr == nil {
					qtx := DB.WithTx(tx)
					_, revErr := qtx.CreateTicketReview(r.Context(), db.CreateTicketReviewParams{
						TicketID:             ticketUUID,
						Status:               "APPROVED",
						Comment:              reviewComment,
						ReviewedBy:           triggeredBy,
						SecurityAcknowledged: securityAck,
						SecurityNotes:        securityNotes,
					})

					if revErr == nil {
						ticket, statErr := qtx.UpdateTicketStatus(r.Context(), db.UpdateTicketStatusParams{
							ID:     ticketUUID,
							Status: db.TicketStatusJENKINSREADY,
						})
						if statErr == nil {
							_ = tx.Commit(r.Context())
							pipelineName = ticket.PipelineName
							repoURL = ticket.RepoUrl
							ticketServiceName = ticket.ServiceName
							ticketDomain = ticket.Domain
							ticketCountry = ticket.Country
							bytes, _ := ticket.IntegrationID.Value()
							if bytes != nil {
								integrationID = fmt.Sprintf("%x-%x-%x-%x-%x", bytes.([]byte)[0:4], bytes.([]byte)[4:6], bytes.([]byte)[6:8], bytes.([]byte)[8:10], bytes.([]byte)[10:16])
							}
							// Record audit trail
							RecordAudit(r.Context(), r, "APPROVE", "ticket", ticketID, map[string]interface{}{
								"pipeline_name": pipelineName,
								"comment":       reviewComment,
								"security_ack":  securityAck,
							})
						} else {
							_ = tx.Rollback(r.Context())
						}
					} else {
						_ = tx.Rollback(r.Context())
					}
				}
			} else {
				// Fallback if DBPool pointer is nil
				_, _ = DB.CreateTicketReview(r.Context(), db.CreateTicketReviewParams{
					TicketID:             ticketUUID,
					Status:               "APPROVED",
					Comment:              reviewComment,
					ReviewedBy:           triggeredBy,
					SecurityAcknowledged: securityAck,
					SecurityNotes:        securityNotes,
				})
				ticket, err := DB.UpdateTicketStatus(r.Context(), db.UpdateTicketStatusParams{
					ID:     ticketUUID,
					Status: db.TicketStatusJENKINSREADY,
				})
				if err == nil {
					pipelineName = ticket.PipelineName
					repoURL = ticket.RepoUrl
					ticketServiceName = ticket.ServiceName
					ticketDomain = ticket.Domain
					ticketCountry = ticket.Country
					bytes, _ := ticket.IntegrationID.Value()
					if bytes != nil {
						integrationID = fmt.Sprintf("%x-%x-%x-%x-%x", bytes.([]byte)[0:4], bytes.([]byte)[4:6], bytes.([]byte)[6:8], bytes.([]byte)[8:10], bytes.([]byte)[10:16])
					}
				}
			}
		}
	}

	lookupKey := ticketServiceName
	if lookupKey == "" {
		lookupKey = ticketID
	}
	if entry, ok := ServiceCatalog.FindByNameOrID(lookupKey); ok {
		if ticketDomain == "" {
			ticketDomain = entry.Domain
		}
		if ticketCountry == "" {
			ticketCountry = entry.Country
		}
		if ticketServiceName == "" {
			ticketServiceName = entry.Name
		}
		if repoURL == "" {
			repoURL = entry.RepoURL
		}
		if pipelineName == "" {
			pipelineName = entry.PipelineName
		}
	}

	if ticketServiceName != "" {
		pipelineName = ResolveCanonicalPipelineName(r.Context(), ticketDomain, ticketCountry, ticketServiceName, "main", pipelineName)
	}

	if pipelineName == "" {
		pipelineName = "lmd-oona-ph-integration-health-renewal-svc"
		if repoURL == "" {
			repoURL = "https://github.com/oona-insurance/lmd-oona-ph-integration-health-renewal-svc"
		}
		integrationID = "mock-integration-id"
	}

	ServiceCatalog.UpdateStatusAndPipeline(ticketID, "LIVE", pipelineName)

	// Enqueue Asynq task TypeCreatePipeline
	redisAddr := os.Getenv("VALKEY_URL")
	if redisAddr == "" {
		redisAddr = "localhost:6379"
	}
	redisPassword := os.Getenv("VALKEY_PASSWORD")

	asynqClient := asynq.NewClient(asynq.RedisClientOpt{
		Addr:     redisAddr,
		Password: redisPassword,
	})
	defer asynqClient.Close()

	payload, _ := json.Marshal(struct {
		TicketID      string
		PipelineName  string
		JenkinsFolder string
		RepoURL       string
		IntegrationID string
		TriggeredBy   string
	}{
		TicketID:      ticketID,
		PipelineName:  pipelineName,
		JenkinsFolder: jenkinsFolder,
		RepoURL:       repoURL,
		IntegrationID: integrationID,
		TriggeredBy:   triggeredBy,
	})

	task := asynq.NewTask("ci:create_pipeline", payload, asynq.Queue("aws_jenkins"), asynq.MaxRetry(3))
	_, _ = asynqClient.Enqueue(task)

	w.Header().Set("HX-Trigger", `{"showToast": {"message": "Ticket Approved! Pipeline item queued for creation in Jenkins.", "type": "success"}}`)
	w.Header().Set("HX-Redirect", "/approvals?toast=approved")
	w.WriteHeader(http.StatusOK)
}

// RejectTicketHandler handles rejecting a ticket request with a mandatory DevOps reason/comment
func RejectTicketHandler(w http.ResponseWriter, r *http.Request) {
	ticketID := chi.URLParam(r, "id")
	if ticketID == "" {
		http.Error(w, `{"error": "ticket ID is required"}`, http.StatusBadRequest)
		return
	}

	_ = r.ParseForm()
	rejectionComment := strings.TrimSpace(r.FormValue("comment"))

	if rejectionComment == "" {
		w.Header().Set("HX-Trigger", `{"showToast": {"message": "A rejection reason / comment is required before rejecting!", "type": "error"}}`)
		http.Error(w, "Rejection comment is required", http.StatusBadRequest)
		return
	}

	claims, _ := r.Context().Value(userCtxKey).(*auth.Claims)
	reviewedBy := "DevOps Admin"
	if claims != nil {
		reviewedBy = claims.Email
	}

	// Save Review Record with REJECTED status
	TicketReviews.Set(&TicketReviewRecord{
		TicketID:   ticketID,
		Status:     "REJECTED",
		Comment:    rejectionComment,
		ReviewedBy: reviewedBy,
		ReviewedAt: time.Now(),
	})

	ServiceCatalog.UpdateStatus(ticketID, "REJECTED_SECURITY")

	if DB != nil {
		var ticketUUID pgtype.UUID
		if err := ticketUUID.Scan(ticketID); err == nil {
			_, _ = DB.CreateTicketReview(r.Context(), db.CreateTicketReviewParams{
				TicketID:             ticketUUID,
				Status:               "REJECTED",
				Comment:              rejectionComment,
				ReviewedBy:           reviewedBy,
				SecurityAcknowledged: false,
				SecurityNotes:        "",
			})
			_, _ = DB.UpdateTicketStatus(r.Context(), db.UpdateTicketStatusParams{
				ID:     ticketUUID,
				Status: db.TicketStatusREJECTEDSECURITY,
			})
			RecordAudit(r.Context(), r, "REJECT", "ticket", ticketID, map[string]interface{}{
				"comment":     rejectionComment,
				"reviewed_by": reviewedBy,
			})
		}
	}

	w.Header().Set("HX-Trigger", `{"showToast": {"message": "Ticket Rejected. Rejection reason recorded and sent to Developer.", "type": "warning"}}`)
	w.Header().Set("HX-Redirect", "/approvals?toast=rejected")
	w.WriteHeader(http.StatusOK)
}

// ResubmitTicketHandler handles Developer resubmitting a fixed ticket
func ResubmitTicketHandler(w http.ResponseWriter, r *http.Request) {
	ticketID := chi.URLParam(r, "id")
	if ticketID == "" {
		http.Error(w, `{"error": "ticket ID is required"}`, http.StatusBadRequest)
		return
	}

	claims, _ := r.Context().Value(userCtxKey).(*auth.Claims)
	resubmittedBy := "Developer"
	if claims != nil {
		resubmittedBy = claims.Email
	}

	resubmitComment := fmt.Sprintf("Resubmitted by %s after fixes.", resubmittedBy)

	// Reset review record to pending
	TicketReviews.Set(&TicketReviewRecord{
		TicketID:   ticketID,
		Status:     "PENDING_DEVOPS",
		Comment:    resubmitComment,
		ReviewedBy: resubmittedBy,
		ReviewedAt: time.Now(),
	})

	ServiceCatalog.UpdateStatus(ticketID, "PENDING_INFRA")

	if DB != nil {
		var ticketUUID pgtype.UUID
		if err := ticketUUID.Scan(ticketID); err == nil {
			_, _ = DB.CreateTicketReview(r.Context(), db.CreateTicketReviewParams{
				TicketID:             ticketUUID,
				Status:               "PENDING_DEVOPS",
				Comment:              resubmitComment,
				ReviewedBy:           resubmittedBy,
				SecurityAcknowledged: false,
				SecurityNotes:        "",
			})
			_, _ = DB.UpdateTicketStatus(r.Context(), db.UpdateTicketStatusParams{
				ID:     ticketUUID,
				Status: db.TicketStatusWAITINGINFRA,
			})
		}
	}

	w.Header().Set("HX-Trigger", `{"showToast": {"message": "Ticket Resubmitted successfully! Sent back to DevOps Approval Queue.", "type": "success"}}`)
	if r.Header.Get("HX-Request") == "true" {
		w.Header().Set("HX-Redirect", "/approvals")
		w.WriteHeader(http.StatusOK)
		return
	}
	http.Redirect(w, r, "/approvals", http.StatusSeeOther)
}

// CheckTFVarsBranchHandler handles GET /api/v1/tickets/check-tfvars?path=...&branch=...&service=...&domain=...&country=...
func CheckTFVarsBranchHandler(w http.ResponseWriter, r *http.Request) {
	pathParam := strings.TrimSpace(r.URL.Query().Get("path"))
	branch := strings.TrimSpace(r.URL.Query().Get("branch"))
	serviceName := strings.TrimSpace(r.URL.Query().Get("service"))
	domain := strings.TrimSpace(r.URL.Query().Get("domain"))
	country := strings.TrimSpace(r.URL.Query().Get("country"))

	if branch == "" {
		branch = "main"
	}

	var resolvedPath string
	var exists bool

	if pathParam != "" && pathParam != "undefined" {
		cleanParam := strings.TrimSuffix(pathParam, "/terraform.tfvars")
		if CheckTerraformPathExists(r.Context(), cleanParam, branch) {
			resolvedPath = cleanParam
			exists = true
		} else {
			// Custom path provided but not found - respect user's input, do NOT fallback
			resolvedPath = cleanParam
			exists = false
		}
	} else {
		// No custom path - auto-resolve from service name
		resolvedPath, exists = ResolveTerraformPath(r.Context(), domain, country, serviceName, branch)
	}

	repoID := FetchExistingRepoIDFromTFVars(r.Context(), resolvedPath, branch)
	if repoID == "" && serviceName != "" {
		cleanName := strings.TrimSuffix(serviceName, "-clone")
		cleanName = strings.TrimPrefix(cleanName, "lmd-oona-ph-integration-")
		cleanName = strings.TrimPrefix(cleanName, "lmd-oona-id-integration-")
		cleanName = strings.TrimPrefix(cleanName, "lmd-oona-")
		repoID = fmt.Sprintf("lmd-oona-ph-integration-%s", cleanName)
	}

	tfvarsContent := ""
	if exists {
		tfvarsContent = FetchTFVarsContent(r.Context(), resolvedPath, branch)
	}
	if tfvarsContent == "" {
		tfvarsContent = fmt.Sprintf("# terraform.tfvars is not provisioned on branch '%s' in %s\n# Select the correct branch or push configuration to GitHub.", branch, resolvedPath)
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"exists":         exists,
		"resolved_path":  resolvedPath,
		"pipeline_name":  repoID,
		"branch":         branch,
		"tfvars_content": tfvarsContent,
	})
}

// JenkinsFoldersHandler handles GET /api/v1/jenkins/folders to return live scanned Jenkins folders
func JenkinsFoldersHandler(w http.ResponseWriter, r *http.Request) {
	folders := FetchLiveJenkinsFolders(r.Context())
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"folders": folders,
	})
}
