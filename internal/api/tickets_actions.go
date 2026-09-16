package api

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
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
	"service-catalog/internal/worker/aws"
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
	forceApprove := r.FormValue("force_approve") == "true" || r.FormValue("bypass_aws") == "true"

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

	var pipelineName string
	var repoURL string
	var integrationID string
	var ticketServiceName string
	var ticketDomain string
	var ticketCountry string
	var ticketUUID pgtype.UUID
	var targetEnv string = "uat"
	var ticketType string = "ONBOARDING"

	if DB != nil {
		if err := ticketUUID.Scan(ticketID); err == nil {
			if tkt, err := DB.GetTicketByID(r.Context(), ticketUUID); err == nil {
				pipelineName = tkt.PipelineName
				repoURL = tkt.RepoUrl
				ticketServiceName = tkt.ServiceName
				ticketDomain = tkt.Domain
				ticketCountry = tkt.Country
				if tkt.TargetEnv != "" {
					targetEnv = strings.ToLower(tkt.TargetEnv)
				}
				if tkt.TicketType != "" {
					ticketType = tkt.TicketType
				}
				bytes, _ := tkt.IntegrationID.Value()
				if bytes != nil {
					integrationID = fmt.Sprintf("%x-%x-%x-%x-%x", bytes.([]byte)[0:4], bytes.([]byte)[4:6], bytes.([]byte)[6:8], bytes.([]byte)[8:10], bytes.([]byte)[10:16])
				}
			} else {
				slog.Error("approve: GetTicketByID failed", "ticket_id", ticketID, "err", err)
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

	// =========================================================================
	// PRE-FLIGHT GATE: Check if Lambda exists in AWS before Jenkins Pipeline is created
	// =========================================================================
	targetFunctionName := pipelineName
	targetAccountID := ""
	countryLower := strings.ToLower(ticketCountry)
	if countryLower == "ph" {
		targetAccountID = "471112995648" // PH-DTC-UAT / Account scope
	} else if countryLower == "id" {
		targetAccountID = "794038209116" // ID-DTC-UAT / Account scope
	}

	tfPath, _ := ResolveTerraformPathForEnv(r.Context(), ticketDomain, ticketCountry, targetEnv, ticketServiceName, "main")

	// Pre-flight AWS Lambda check runs regardless of forceApprove so bypass events
	// can be accurately audited (did the operator override a REAL gap, or a false negative?).
	lambdaCheckPerformed := false
	lambdaExists := true
	checkCtx, cancel := context.WithTimeout(r.Context(), 3*time.Second)
	defer cancel()

	awsClient, awsErr := aws.NewAWSClientForAccount(checkCtx, targetAccountID)
	if awsErr == nil && awsClient != nil {
		exists, _ := awsClient.CheckLambdaExists(checkCtx, targetFunctionName)
		lambdaCheckPerformed = true
		lambdaExists = exists
	}

	if !forceApprove && lambdaCheckPerformed && !lambdaExists {
		reasonMsg := fmt.Sprintf("Fungsi Lambda '%s' belum ditemukan di AWS Console. Silakan buat resource terlebih dahulu oleh DevOps via 'terraform apply'.", targetFunctionName)

		w.Header().Set("Content-Type", "application/json")
		triggerPayload, _ := json.Marshal(map[string]interface{}{
			"resourceNotReady": map[string]interface{}{
				"ticket_id":     ticketID,
				"function_name": targetFunctionName,
				"region":        "ap-southeast-3",
				"account_id":    targetAccountID,
				"country":       ticketCountry,
				"domain":        ticketDomain,
				"service_name":  ticketServiceName,
				"tf_path":       tfPath,
				"reason":        reasonMsg,
			},
		})
		w.Header().Set("HX-Trigger", string(triggerPayload))
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(map[string]string{
			"status":        "resource_not_ready",
			"function_name": targetFunctionName,
			"message":       reasonMsg,
		})
		return
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

	if DB != nil && ticketUUID.Valid {
		persistOK := false
		if DBPool != nil {
			tx, txErr := DBPool.Begin(r.Context())
			if txErr != nil {
				slog.Error("approve: begin tx failed", "ticket_id", ticketID, "err", txErr)
			} else {
				qtx := DB.WithTx(tx)
				_, revErr := qtx.CreateTicketReview(r.Context(), db.CreateTicketReviewParams{
					TicketID:             ticketUUID,
					Status:               "APPROVED",
					Comment:              reviewComment,
					ReviewedBy:           triggeredBy,
					SecurityAcknowledged: securityAck,
					SecurityNotes:        securityNotes,
				})
				if revErr != nil {
					_ = tx.Rollback(r.Context())
					slog.Error("approve: CreateTicketReview failed", "ticket_id", ticketID, "err", revErr)
				} else {
					ticket, statErr := qtx.UpdateTicketStatus(r.Context(), db.UpdateTicketStatusParams{
						ID:     ticketUUID,
						Status: db.TicketStatusJENKINSREADY,
					})
					if statErr != nil {
						_ = tx.Rollback(r.Context())
						slog.Error("approve: UpdateTicketStatus failed", "ticket_id", ticketID, "err", statErr)
					} else if commitErr := tx.Commit(r.Context()); commitErr != nil {
						slog.Error("approve: commit failed", "ticket_id", ticketID, "err", commitErr)
					} else {
						persistOK = true
						pipelineName = ticket.PipelineName
						repoURL = ticket.RepoUrl
						ticketServiceName = ticket.ServiceName
						ticketDomain = ticket.Domain
						ticketCountry = ticket.Country
						if ticket.TargetEnv != "" {
							targetEnv = strings.ToLower(ticket.TargetEnv)
						}
						if ticket.TicketType != "" {
							ticketType = ticket.TicketType
						}
						bytes, _ := ticket.IntegrationID.Value()
						if bytes != nil {
							integrationID = fmt.Sprintf("%x-%x-%x-%x-%x", bytes.([]byte)[0:4], bytes.([]byte)[4:6], bytes.([]byte)[6:8], bytes.([]byte)[8:10], bytes.([]byte)[10:16])
						}
						RecordAudit(r.Context(), r, "APPROVE", "ticket", ticketID, map[string]interface{}{
							"pipeline_name": pipelineName,
							"comment":       reviewComment,
							"security_ack":  securityAck,
							"force_approve": forceApprove,
							"ticket_type":   ticketType,
							"target_env":    targetEnv,
						})
						if forceApprove && lambdaCheckPerformed && !lambdaExists {
							RecordAudit(r.Context(), r, "BYPASS_PREFLIGHT_CHECK", "ticket", ticketID, map[string]interface{}{
								"pipeline_name":  pipelineName,
								"function_name":  targetFunctionName,
								"account_id":     targetAccountID,
								"region":         "ap-southeast-3",
								"tf_path":        tfPath,
								"lambda_existed": false,
								"warning":        "Operator bypassed AWS pre-flight gate while Lambda function was confirmed NOT present. Jenkins build may fail with ResourceNotFoundException.",
							})
						}
					}
				}
			}
		} else {
			_, revErr := DB.CreateTicketReview(r.Context(), db.CreateTicketReviewParams{
				TicketID:             ticketUUID,
				Status:               "APPROVED",
				Comment:              reviewComment,
				ReviewedBy:           triggeredBy,
				SecurityAcknowledged: securityAck,
				SecurityNotes:        securityNotes,
			})
			if revErr != nil {
				slog.Error("approve: CreateTicketReview (no-tx) failed", "ticket_id", ticketID, "err", revErr)
			}
			ticket, err := DB.UpdateTicketStatus(r.Context(), db.UpdateTicketStatusParams{
				ID:     ticketUUID,
				Status: db.TicketStatusJENKINSREADY,
			})
			if err != nil {
				slog.Error("approve: UpdateTicketStatus (no-tx) failed", "ticket_id", ticketID, "err", err)
			} else {
				persistOK = true
				pipelineName = ticket.PipelineName
				repoURL = ticket.RepoUrl
				ticketServiceName = ticket.ServiceName
				ticketDomain = ticket.Domain
				ticketCountry = ticket.Country
				if ticket.TargetEnv != "" {
					targetEnv = strings.ToLower(ticket.TargetEnv)
				}
				if ticket.TicketType != "" {
					ticketType = ticket.TicketType
				}
				bytes, _ := ticket.IntegrationID.Value()
				if bytes != nil {
					integrationID = fmt.Sprintf("%x-%x-%x-%x-%x", bytes.([]byte)[0:4], bytes.([]byte)[4:6], bytes.([]byte)[6:8], bytes.([]byte)[8:10], bytes.([]byte)[10:16])
				}
			}
		}

		if !persistOK {
			w.Header().Set("Content-Type", "application/json")
			w.Header().Set("HX-Trigger", `{"showToast": {"message": "Approval failed to persist. Check server logs.", "type": "error"}}`)
			w.WriteHeader(http.StatusInternalServerError)
			_ = json.NewEncoder(w).Encode(map[string]string{"error": "failed to persist ticket approval"})
			return
		}
	}

	ServiceCatalog.UpdateStatusAndPipeline(ticketID, "LIVE", pipelineName)

	envToMark := strings.ToLower(targetEnv)
	if envToMark == "" {
		envToMark = "uat"
	}
	if ticketType == "PROMOTION" || envToMark == "uat" {
		if DB != nil && ticketServiceName != "" {
			if err := DB.AddCatalogDeployedEnv(r.Context(), db.AddCatalogDeployedEnvParams{
				ServiceName: ticketServiceName,
				Env:         envToMark,
			}); err != nil {
				slog.Error("approve: AddCatalogDeployedEnv failed", "service", ticketServiceName, "env", envToMark, "err", err)
			}
		}
		ServiceCatalog.AddDeployedEnv(ticketServiceName, envToMark)
	}

	// Enqueue Asynq task TypeCreatePipeline only for onboarding (multibranch project creation)
	if ticketType != "PROMOTION" {
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
	}

	toastMsg := "Ticket Approved! Pipeline item queued for creation in Jenkins."
	redirectURL := "/approvals?toast=approved"
	if ticketType == "PROMOTION" {
		toastMsg = fmt.Sprintf("Promotion to %s Approved! Environment status is now active.", strings.ToUpper(targetEnv))
		redirectURL = fmt.Sprintf("/catalog/%s?env=%s", ticketServiceName, targetEnv)
	}

	w.Header().Set("HX-Trigger", fmt.Sprintf(`{"showToast": {"message": "%s", "type": "success"}}`, toastMsg))
	w.Header().Set("HX-Redirect", redirectURL)
	w.WriteHeader(http.StatusOK)
}

// VerifyTicketLambdaHandler checks whether the ticket's AWS Lambda function exists right now in AWS Console
func VerifyTicketLambdaHandler(w http.ResponseWriter, r *http.Request) {
	ticketID := chi.URLParam(r, "id")
	if ticketID == "" {
		http.Error(w, `{"error": "ticket ID is required"}`, http.StatusBadRequest)
		return
	}

	w.Header().Set("Content-Type", "application/json")

	var pipelineName string
	var ticketServiceName string
	var ticketDomain string
	var ticketCountry string

	if DB != nil {
		var ticketUUID pgtype.UUID
		if err := ticketUUID.Scan(ticketID); err == nil {
			if tkt, err := DB.GetTicketByID(r.Context(), ticketUUID); err == nil {
				pipelineName = tkt.PipelineName
				ticketServiceName = tkt.ServiceName
				ticketDomain = tkt.Domain
				ticketCountry = tkt.Country
			}
		}
	}

	if ticketServiceName != "" {
		pipelineName = ResolveCanonicalPipelineName(r.Context(), ticketDomain, ticketCountry, ticketServiceName, "main", pipelineName)
	}

	targetFunctionName := pipelineName
	targetAccountID := ""
	countryLower := strings.ToLower(ticketCountry)
	if countryLower == "ph" {
		targetAccountID = "471112995648"
	} else if countryLower == "id" {
		targetAccountID = "794038209116"
	}

	tfPath, _ := ResolveTerraformPath(r.Context(), ticketDomain, ticketCountry, ticketServiceName, "main")

	checkCtx, cancel := context.WithTimeout(r.Context(), 3*time.Second)
	defer cancel()

	awsClient, err := aws.NewAWSClientForAccount(checkCtx, targetAccountID)
	if err != nil {
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"exists":        false,
			"function_name": targetFunctionName,
			"region":        "ap-southeast-3",
			"account_id":    targetAccountID,
			"tf_path":       tfPath,
			"reason":        "Resource belum ditemukan di AWS Console.",
		})
		return
	}

	exists, _ := awsClient.CheckLambdaExists(checkCtx, targetFunctionName)
	reasonMsg := ""
	if !exists {
		reasonMsg = fmt.Sprintf("Lambda '%s' belum ditemukan di AWS. Silakan buat resource via 'terraform apply'.", targetFunctionName)
	}

	_ = json.NewEncoder(w).Encode(map[string]interface{}{
		"exists":        exists,
		"function_name": targetFunctionName,
		"region":        "ap-southeast-3",
		"account_id":    targetAccountID,
		"tf_path":       tfPath,
		"reason":        reasonMsg,
	})
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
