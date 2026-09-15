package api

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
	"service-catalog/internal/auth"
	"service-catalog/internal/models"
	db "service-catalog/internal/repository/postgres/generated"
)

// DB holds the connection to PostgreSQL mapped by SQLC
var DB *db.Queries

// DBPool holds the pgxpool connection pool for transactions
var DBPool *pgxpool.Pool

// CreateTicketRequest represents the JSON payload expected from API clients (e.g., Slack Bot, Jira Webhook)
type CreateTicketRequest struct {
	RepoURL     string `json:"repo_url"`
	Domain      string `json:"domain"`       // e.g., "integration"
	Country     string `json:"country"`      // e.g., "ph"
	ServiceName string `json:"service_name"` // e.g., "health-renewal-svc"
	JiraIssueID string `json:"jira_issue_id,omitempty"`
	Description string `json:"description"`
}

// CreateTicketHandler handles programmatic ticket creation via JSON API or HTMX form submit
func CreateTicketHandler(w http.ResponseWriter, r *http.Request) {
	// Parse JSON or Form Body
	var req CreateTicketRequest
	if strings.Contains(r.Header.Get("Content-Type"), "application/json") {
		_ = json.NewDecoder(r.Body).Decode(&req)
	} else {
		_ = r.ParseForm()
		req.RepoURL = r.FormValue("repo_url")
		req.Domain = r.FormValue("domain")
		req.Country = r.FormValue("country")
		req.ServiceName = r.FormValue("service_name")
		req.JiraIssueID = r.FormValue("jira_issue_id")
		req.Description = r.FormValue("description")
	}

	req.RepoURL = strings.TrimSpace(req.RepoURL)
	req.Domain = strings.TrimSpace(req.Domain)
	req.Country = strings.TrimSpace(req.Country)
	req.ServiceName = strings.TrimSpace(req.ServiceName)
	req.JiraIssueID = strings.TrimSpace(req.JiraIssueID)
	req.Description = strings.TrimSpace(req.Description)

	// Validate required fields and formats
	if req.Domain == "" || req.Country == "" || req.ServiceName == "" || req.RepoURL == "" {
		http.Error(w, `{"error": "domain, country, service_name, and repo_url are required"}`, http.StatusBadRequest)
		return
	}

	if len(req.ServiceName) > 100 || len(req.Domain) > 50 || len(req.Country) > 10 {
		http.Error(w, `{"error": "input length exceeds maximum allowed limit"}`, http.StatusBadRequest)
		return
	}

	if !strings.HasPrefix(req.RepoURL, "http://") && !strings.HasPrefix(req.RepoURL, "https://") && !strings.HasPrefix(req.RepoURL, "git@") {
		http.Error(w, `{"error": "invalid repository URL format"}`, http.StatusBadRequest)
		return
	}

	// Pipeline name follows existing_github_repo_id in terraform.tfvars when present.
	// Never interpolate raw country codes ("PH"/"ID") from system_parameters into the job name.
	pipelineName := ResolveCanonicalPipelineName(r.Context(), req.Domain, req.Country, req.ServiceName, "main", "")

	// Fetch user claims from JWT middleware
	claims, ok := r.Context().Value(userCtxKey).(*auth.Claims)
	if !ok {
		http.Error(w, `{"error": "unauthorized"}`, http.StatusUnauthorized)
		return
	}

	// Insert into Database
	var createdID string
	var status string
	
	if DB != nil {
		var userID pgtype.UUID
		_ = userID.Scan(claims.UserID)
		
		ticket, err := DB.CreateTicket(r.Context(), db.CreateTicketParams{
			CreatedBy:    userID,
			RepoUrl:      req.RepoURL,
			Domain:       req.Domain,
			Country:      req.Country,
			ServiceName:  req.ServiceName,
			PipelineName: pipelineName,
			JiraIssueID:  pgtype.Text{String: req.JiraIssueID, Valid: req.JiraIssueID != ""},
			Description:  req.Description,
		})
		if err != nil {
			log.Printf("ERROR: Failed to create ticket in DB: %v (user=%s, service=%s)", err, claims.UserID, req.ServiceName)
			http.Error(w, `{"error": "failed to record ticket into database"}`, http.StatusInternalServerError)
			return
		}
		
		// Convert UUID back to string
		bytes, _ := ticket.ID.Value()
		if bytes != nil {
			if b, ok := bytes.([]byte); ok && len(b) == 16 {
				createdID = fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:16])
			}
		}
		if createdID == "" {
			createdID = fmt.Sprintf("%x-%x-%x-%x-%x", ticket.ID.Bytes[0:4], ticket.ID.Bytes[4:6], ticket.ID.Bytes[6:8], ticket.ID.Bytes[8:10], ticket.ID.Bytes[10:16])
		}
		status = string(ticket.Status)

		// Record Audit Log
		RecordAudit(r.Context(), r, "CREATE", "ticket", createdID, map[string]interface{}{
			"service_name":  req.ServiceName,
			"domain":        req.Domain,
			"country":       req.Country,
			"pipeline_name": pipelineName,
			"repo_url":      req.RepoURL,
		})
	} else {
		// Fallback for mocked mode
		createdID = "TKT-001-MOCK"
		status = models.StatusScanning
	}

	// Stub response
	response := map[string]interface{}{
		"status":  "success",
		"message": "Ticket created and sent to scanner.",
		"data": map[string]string{
			"ticket_id":     createdID,
			"pipeline_name": pipelineName,
			"created_by":    claims.Email,
			"state":         status,
		},
	}

	// Let's pretend we also enqueued the Trivy Asynq worker here
	// asynqClient.Enqueue(tasks.NewTrivyScanTask(createdID, req.RepoURL))

	if r.Header.Get("HX-Request") != "" {
		w.Header().Set("HX-Redirect", "/tickets?toast=created")
		w.Header().Set("HX-Trigger", fmt.Sprintf(`{"showToast": {"message": "Ticket created successfully for %s", "type": "success"}}`, req.ServiceName))
		w.WriteHeader(http.StatusCreated)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	json.NewEncoder(w).Encode(response)
}

// DeleteTicketHandler handles DELETE /api/v1/tickets/{id}
func DeleteTicketHandler(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	if id == "" {
		w.Header().Set("HX-Trigger", `{"showToast": {"message": "Ticket ID is required", "type": "error"}}`)
		w.WriteHeader(http.StatusBadRequest)
		return
	}

	claims, _ := r.Context().Value(userCtxKey).(*auth.Claims)
	deletedServiceName := ""

	if DB != nil {
		var ticketUUID pgtype.UUID
		err := ticketUUID.Scan(id)
		if err == nil && ticketUUID.Valid {
			// Find ticket first to get service_name
			if ticket, tErr := DB.GetTicketByID(r.Context(), ticketUUID); tErr == nil {
				deletedServiceName = ticket.ServiceName
			}
			_ = DB.DeleteTicket(r.Context(), ticketUUID)
		} else {
			// Try lookup by listing all tickets and matching id/service_name
			if tickets, tErr := DB.ListTickets(r.Context()); tErr == nil {
				for _, t := range tickets {
					tIDStr := fmt.Sprintf("%x-%x-%x-%x-%x", t.ID.Bytes[0:4], t.ID.Bytes[4:6], t.ID.Bytes[6:8], t.ID.Bytes[8:10], t.ID.Bytes[10:16])
					if tIDStr == id || strings.EqualFold(t.ServiceName, id) {
						deletedServiceName = t.ServiceName
						_ = DB.DeleteTicket(r.Context(), t.ID)
						break
					}
				}
			}
		}

		// Also remove from CatalogStore if present
		if deletedServiceName != "" {
			ServiceCatalog.Delete(deletedServiceName)
		}

		// Record Audit
		userEmail := "admin@oona-insurance.com"
		if claims != nil && claims.Email != "" {
			userEmail = claims.Email
		}
		RecordAudit(r.Context(), r, "DELETE", "ticket", id, map[string]interface{}{
			"deleted_by":   userEmail,
			"service_name": deletedServiceName,
		})
	}

	w.Header().Set("HX-Trigger", `{"showToast": {"message": "Ticket deleted successfully", "type": "success"}}`)
	if r.Header.Get("HX-Request") == "true" {
		w.Header().Set("HX-Redirect", "/tickets?toast=deleted")
		w.WriteHeader(http.StatusOK)
		return
	}
	http.Redirect(w, r, "/tickets?toast=deleted", http.StatusSeeOther)
}
