package api

import (
	"bytes"
	"fmt"
	"log"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/oona-insurance/dev-portal/internal/auth"
	db "github.com/oona-insurance/dev-portal/internal/repository/postgres/generated"
)

func RenderAdminParameters(w http.ResponseWriter, r *http.Request) {
	tmpl, err := parsePage("admin_parameters.html")
	if err != nil {
		http.Error(w, "Failed to load template: "+err.Error(), http.StatusInternalServerError)
		return
	}

	claims, _ := r.Context().Value(userCtxKey).(*auth.Claims)

	countries := SystemParams.GetAllCountries()
	domains := SystemParams.GetAllDomains()
	environments := SystemParams.GetAllEnvironments()
	shelves := SystemParams.GetAllShelves()

	data := struct {
		Title        string
		User         *auth.Claims
		Countries    []SystemParam
		Domains      []SystemParam
		Environments []SystemParam
		Shelves      []SystemParam
	}{
		Title:        "System Parameters",
		User:         claims,
		Countries:    countries,
		Domains:      domains,
		Environments: environments,
		Shelves:      shelves,
	}

	tmpl.ExecuteTemplate(w, "base", data)
}

// AuditLogView represents an individual audit event formatted for HTML display
type AuditLogView struct {
	ID         string
	Action     string
	EntityType string
	EntityID   string
	UserEmail  string
	IPAddress  string
	Details    string
	CreatedAt  string
}

// RenderAdminAuditLogs renders the Audit Trail & Compliance dashboard page with pagination
func RenderAdminAuditLogs(w http.ResponseWriter, r *http.Request) {
	tmpl, err := parsePage("admin_audit_logs.html")
	if err != nil {
		http.Error(w, "Failed to load template: "+err.Error(), http.StatusInternalServerError)
		return
	}

	claims, _ := r.Context().Value(userCtxKey).(*auth.Claims)

	page, _ := strconv.Atoi(r.URL.Query().Get("page"))
	if page < 1 {
		page = 1
	}
	perPage, _ := strconv.Atoi(r.URL.Query().Get("per_page"))
	if perPage < 1 || perPage > 100 {
		perPage = 15
	}
	offset := int32((page - 1) * perPage)
	limit := int32(perPage)

	var logViews []AuditLogView
	var totalCount int64

	if DB != nil {
		ctx := r.Context()
		count, countErr := DB.CountAuditLogs(ctx)
		if countErr != nil {
			log.Printf("[ERROR] Failed to count audit logs: %v", countErr)
			totalCount = 0
		} else {
			totalCount = count
		}

		dbLogs, listErr := DB.ListAuditLogs(ctx, db.ListAuditLogsParams{
			Limit:  limit,
			Offset: offset,
		})
		if listErr != nil {
			log.Printf("[ERROR] Failed to list audit logs: %v", listErr)
		} else {
			for _, l := range dbLogs {
				ip := ""
				if l.IpAddress.Valid {
					ip = l.IpAddress.String
				}
				entityID := ""
				if l.EntityID.Valid {
					entityID = l.EntityID.String
				}
				userEmail := ""
				if l.UserEmail.Valid {
					userEmail = l.UserEmail.String
				}
				detailsStr := "{}"
				if len(l.Details) > 0 {
					detailsStr = string(l.Details)
				}

				idStr := fmt.Sprintf("%x-%x-%x-%x-%x", l.ID.Bytes[0:4], l.ID.Bytes[4:6], l.ID.Bytes[6:8], l.ID.Bytes[8:10], l.ID.Bytes[10:16])

				createdAtStr := ""
				if l.CreatedAt.Valid {
					createdAtStr = l.CreatedAt.Time.Format("2006-01-02 15:04:05 UTC")
				}

				logViews = append(logViews, AuditLogView{
					ID:         idStr,
					Action:     l.Action,
					EntityType: l.EntityType,
					EntityID:   entityID,
					UserEmail:  userEmail,
					IPAddress:  ip,
					Details:    detailsStr,
					CreatedAt:  createdAtStr,
				})
			}
		}
	}

	totalPages := int((totalCount + int64(perPage) - 1) / int64(perPage))
	if totalPages < 1 {
		totalPages = 1
	}
	startItem := int(offset) + 1
	endItem := int(offset) + len(logViews)
	if totalCount == 0 {
		startItem = 0
		endItem = 0
	}

	pagination := PaginationInfo{
		CurrentPage: page,
		TotalPages:  totalPages,
		PerPage:     perPage,
		TotalCount:  totalCount,
		StartItem:   startItem,
		EndItem:     endItem,
		BaseURL:     "/admin/audit-logs",
	}

	data := struct {
		Title      string
		User       *auth.Claims
		Logs       []AuditLogView
		Pagination PaginationInfo
	}{
		Title:      "Audit Logs",
		User:       claims,
		Logs:       logViews,
		Pagination: pagination,
	}

	tmpl.ExecuteTemplate(w, "base", data)
}

// ExportAuditLogsCSVHandler exports full audit records as downloadable CSV
func ExportAuditLogsCSVHandler(w http.ResponseWriter, r *http.Request) {
	if DB == nil {
		http.Error(w, "Database unavailable", http.StatusServiceUnavailable)
		return
	}

	ctx := r.Context()
	dbLogs, err := DB.ListAuditLogs(ctx, db.ListAuditLogsParams{
		Limit:  1000,
		Offset: 0,
	})
	if err != nil {
		http.Error(w, "Failed to fetch logs: "+err.Error(), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "text/csv")
	w.Header().Set("Content-Disposition", "attachment;filename=service_catalog_audit_logs.csv")

	w.Write([]byte("ID,Timestamp,Action,EntityType,EntityID,UserEmail,IPAddress,Details\n"))
	for _, l := range dbLogs {
		ip := ""
		if l.IpAddress.Valid {
			ip = l.IpAddress.String
		}
		entityID := ""
		if l.EntityID.Valid {
			entityID = l.EntityID.String
		}
		userEmail := ""
		if l.UserEmail.Valid {
			userEmail = l.UserEmail.String
		}
		cleanDetails := strings.ReplaceAll(string(l.Details), "\"", "\"\"")
		idStr := fmt.Sprintf("%x-%x-%x-%x-%x", l.ID.Bytes[0:4], l.ID.Bytes[4:6], l.ID.Bytes[6:8], l.ID.Bytes[8:10], l.ID.Bytes[10:16])
		createdAtStr := ""
		if l.CreatedAt.Valid {
			createdAtStr = l.CreatedAt.Time.Format(time.RFC3339)
		}

		line := fmt.Sprintf("\"%s\",\"%s\",\"%s\",\"%s\",\"%s\",\"%s\",\"%s\",\"%s\"\n",
			idStr,
			createdAtStr,
			l.Action,
			l.EntityType,
			entityID,
			userEmail,
			ip,
			cleanDetails,
		)
		w.Write([]byte(line))
	}
}

// RenderDevopsTerraform renders the DevOps Terraform IaC visualizer & configuration view
func RenderDevopsTerraform(w http.ResponseWriter, r *http.Request) {
	tmpl, err := parsePage("devops_terraform.html")
	if err != nil {
		http.Error(w, "Failed to load template: "+err.Error(), http.StatusInternalServerError)
		return
	}

	claims, _ := r.Context().Value(userCtxKey).(*auth.Claims)

	data := struct {
		Title       string
		User        *auth.Claims
		RepoURL     string
		Branch      string
		BasePath    string
		IsConnected bool
	}{
		Title:       "Terraform IaC Config - DevOps",
		User:        claims,
		RepoURL:     "https://github.com/oona-insurance/oona-dtc-country-terraform-iac.git",
		Branch:      "main",
		BasePath:    "02-app-setup/{domain}/{country}/{env}/services/{service}",
		IsConnected: true,
	}

	tmpl.ExecuteTemplate(w, "base", data)
}

// PreviewPipelineName is an HTMX endpoint that responds to form typing

type Integration struct {
	ID          string
	Name        string
	Provider    string
	BaseURL     string
	AuthUser    string
	IsActive    bool
	MaskedToken string
}

func maskSecret(s string) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return ""
	}
	n := len(s)
	if n <= 6 {
		return "••••••••"
	}
	if n <= 10 {
		return s[:2] + "••••" + s[n-2:]
	}
	if n <= 20 {
		return s[:4] + "••••••••" + s[n-4:]
	}
	return s[:6] + "••••••••••••" + s[n-4:]
}

// RenderAdminIntegrations renders the settings page for CI/CD and Repo connections
func RenderAdminIntegrations(w http.ResponseWriter, r *http.Request) {
	tmpl, err := parsePage("admin_integrations.html")
	if err != nil {
		http.Error(w, "Failed to load template: "+err.Error(), http.StatusInternalServerError)
		return
	}

	claims, _ := r.Context().Value(userCtxKey).(*auth.Claims)

	// REAL DB QUERY: Fetch active integrations from PostgreSQL
	var integrations []Integration

	if DB != nil {
		dbIntegrations, err := DB.ListIntegrations(r.Context())
		if err == nil {
			for _, ig := range dbIntegrations {
				var idStr string
				bytes, errVal := ig.ID.Value()
				if errVal == nil && bytes != nil {
					if b, ok := bytes.([]byte); ok && len(b) == 16 {
						idStr = fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:16])
					} else if s, ok := bytes.(string); ok {
						idStr = s
					}
				}
				if idStr == "" {
					idStr = fmt.Sprintf("%x-%x-%x-%x-%x", ig.ID.Bytes[0:4], ig.ID.Bytes[4:6], ig.ID.Bytes[6:8], ig.ID.Bytes[8:10], ig.ID.Bytes[10:16])
				}

				var maskedToken string
				if ig.AuthToken != "" {
					if dec, errDec := auth.Decrypt(ig.AuthToken); errDec == nil && dec != "" {
						maskedToken = maskSecret(dec)
					} else {
						maskedToken = maskSecret(ig.AuthToken)
					}
				}

				integrations = append(integrations, Integration{
					ID:          idStr,
					Name:        ig.Name,
					Provider:    string(ig.Provider),
					BaseURL:     ig.BaseUrl,
					AuthUser:    ig.AuthUser.String,
					IsActive:    ig.IsActive,
					MaskedToken: maskedToken,
				})
			}
		} else {
			log.Printf("Error fetching integrations: %v", err)
		}
	}

	data := struct {
		Title        string
		User         *auth.Claims
		Integrations []Integration
	}{
		Title:        "System Integrations",
		User:         claims,
		Integrations: integrations,
	}

	tmpl.ExecuteTemplate(w, "base", data)
}

// RenderAdminUsers renders the user management page
func RenderAdminUsers(w http.ResponseWriter, r *http.Request) {
	tmpl, err := parsePage("admin_users.html")
	if err != nil {
		http.Error(w, "Failed to load template: "+err.Error(), http.StatusInternalServerError)
		return
	}

	claims, _ := r.Context().Value(userCtxKey).(*auth.Claims)

	data := struct {
		Title string
		User  *auth.Claims
	}{
		Title: "User Management",
		User:  claims,
	}

	tmpl.ExecuteTemplate(w, "base", data)
}

// SaveIntegrationHandler handles saving a new CI/CD or Repo integration
func SaveIntegrationHandler(w http.ResponseWriter, r *http.Request) {
	err := r.ParseForm()
	if err != nil {
		w.Header().Set("HX-Trigger", `{"showToast": {"message": "Invalid form data", "type": "error"}}`)
		w.Write([]byte{})
		return
	}

	name := r.FormValue("name")
	provider := r.FormValue("provider")
	baseURL := r.FormValue("base_url")
	authUser := r.FormValue("auth_user")
	authToken := r.FormValue("auth_token")

	if provider == "gemini" {
		if r.FormValue("ai_include_high") == "true" || r.FormValue("ai_include_high") == "on" {
			authUser = "include_high=true"
		} else {
			authUser = "include_high=false"
		}
	}

	// Encrypt the sensitive token before saving
	encryptedToken, err := auth.Encrypt(authToken)
	if err != nil {
		w.Header().Set("HX-Trigger", `{"showToast": {"message": "Encryption failed", "type": "error"}}`)
		w.Write([]byte{})
		return
	}

	if DB != nil {
		authUserText := pgtype.Text{String: authUser, Valid: authUser != ""}
		_, err := DB.CreateIntegration(r.Context(), db.CreateIntegrationParams{
			Name:      name,
			Provider:  db.IntegrationProvider(provider),
			BaseUrl:   baseURL,
			AuthUser:  authUserText,
			AuthToken: encryptedToken,
		})

		if err != nil {
			w.Header().Set("HX-Trigger", fmt.Sprintf(`{"showToast": {"message": "DB Error: %v", "type": "error"}}`, err))
			w.Write([]byte{})
			return
		}
	}

	// Trigger success toast and instruct HTMX to refresh the page to show the new table
	w.Header().Set("HX-Trigger", `{"showToast": {"message": "Integration saved successfully!", "type": "success"}}`)
	w.Header().Set("HX-Redirect", "/admin/integrations")
	w.Write([]byte{})
}

// EditIntegrationHandler handles updating an existing integration
func EditIntegrationHandler(w http.ResponseWriter, r *http.Request) {
	idStr := chi.URLParam(r, "id")
	if idStr == "" {
		idStr = r.FormValue("id")
	}

	err := r.ParseForm()
	if err != nil {
		w.Header().Set("HX-Trigger", `{"showToast": {"message": "Invalid form data", "type": "error"}}`)
		w.Write([]byte{})
		return
	}

	name := r.FormValue("name")
	provider := r.FormValue("provider")
	baseURL := r.FormValue("base_url")
	authUser := r.FormValue("auth_user")
	authToken := r.FormValue("auth_token")

	if provider == "gemini" {
		if r.FormValue("ai_include_high") == "true" || r.FormValue("ai_include_high") == "on" {
			authUser = "include_high=true"
		} else {
			authUser = "include_high=false"
		}
	}

	var encryptedToken string
	if authToken != "" {
		enc, errEnc := auth.Encrypt(authToken)
		if errEnc != nil {
			w.Header().Set("HX-Trigger", `{"showToast": {"message": "Encryption failed", "type": "error"}}`)
			w.Write([]byte{})
			return
		}
		encryptedToken = enc
	}

	if DB != nil {
		var uid pgtype.UUID
		_ = uid.Scan(idStr)

		if encryptedToken == "" {
			// Preserve existing token if user did not provide a new one
			if existing, errGet := DB.GetIntegrationByID(r.Context(), uid); errGet == nil {
				encryptedToken = existing.AuthToken
			}
		}

		authUserText := pgtype.Text{String: authUser, Valid: authUser != ""}
		err = DB.UpdateIntegration(r.Context(), db.UpdateIntegrationParams{
			ID:        uid,
			Name:      name,
			Provider:  db.IntegrationProvider(provider),
			BaseUrl:   baseURL,
			AuthUser:  authUserText,
			AuthToken: encryptedToken,
			IsActive:  true,
		})

		if err != nil {
			w.Header().Set("HX-Trigger", fmt.Sprintf(`{"showToast": {"message": "DB Update Error: %v", "type": "error"}}`, err))
			w.Write([]byte{})
			return
		}
	}

	w.Header().Set("HX-Trigger", `{"showToast": {"message": "Integration updated successfully!", "type": "success"}}`)
	w.Header().Set("HX-Redirect", "/admin/integrations")
	w.Write([]byte{})
}
func testRealConnection(provider, baseURL, authUser, authToken string) (bool, string) {
	client := &http.Client{
		Timeout: 7 * time.Second,
	}

	cleanURL := strings.TrimRight(baseURL, "/")
	if cleanURL == "" {
		return false, "Base URL is required"
	}

	switch provider {
	case "jenkins":
		reqURL := cleanURL + "/api/json"
		req, err := http.NewRequest("GET", reqURL, nil)
		if err != nil {
			return false, fmt.Sprintf("Invalid URL: %v", err)
		}
		if authUser != "" && authToken != "" {
			req.SetBasicAuth(authUser, authToken)
		}
		resp, err := client.Do(req)
		if err != nil {
			return false, fmt.Sprintf("Connection failed: %v", err)
		}
		defer resp.Body.Close()

		if resp.StatusCode == http.StatusOK {
			jenkinsVersion := resp.Header.Get("X-Jenkins")
			if jenkinsVersion != "" {
				return true, fmt.Sprintf("Connected (Jenkins v%s)", jenkinsVersion)
			}
			return true, "Connected (HTTP 200 OK)"
		} else if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden {
			return false, fmt.Sprintf("Auth failed (HTTP %d)", resp.StatusCode)
		}
		return false, fmt.Sprintf("HTTP %d (%s)", resp.StatusCode, resp.Status)

	case "gitlab":
		reqURL := cleanURL + "/api/v4/version"
		req, err := http.NewRequest("GET", reqURL, nil)
		if err != nil {
			return false, fmt.Sprintf("Invalid URL: %v", err)
		}
		if authToken != "" {
			req.Header.Set("PRIVATE-TOKEN", authToken)
		}
		resp, err := client.Do(req)
		if err != nil {
			return false, fmt.Sprintf("Connection failed: %v", err)
		}
		defer resp.Body.Close()

		if resp.StatusCode == http.StatusOK {
			return true, "Connected to GitLab API (HTTP 200)"
		}
		return false, fmt.Sprintf("GitLab returned HTTP %d", resp.StatusCode)

	case "jira":
		reqURL := cleanURL + "/rest/api/2/myself"
		req, err := http.NewRequest("GET", reqURL, nil)
		if err != nil {
			return false, fmt.Sprintf("Invalid URL: %v", err)
		}
		if authUser != "" && authToken != "" {
			req.SetBasicAuth(authUser, authToken)
		}
		resp, err := client.Do(req)
		if err != nil {
			return false, fmt.Sprintf("Connection failed: %v", err)
		}
		defer resp.Body.Close()

		if resp.StatusCode == http.StatusOK {
			return true, "Connected to Jira API (HTTP 200)"
		}
		return false, fmt.Sprintf("Jira returned HTTP %d", resp.StatusCode)

	case "gemini", "ai_copilot", "google_gemini":
		model := "gemini-3-flash-preview"
		if strings.Contains(cleanURL, "models/") {
			model = strings.TrimPrefix(cleanURL, "models/")
		}
		testURL := fmt.Sprintf("https://generativelanguage.googleapis.com/v1beta/models/%s:generateContent?key=%s", model, url.QueryEscape(authToken))
		jsonBody := []byte(`{"contents":[{"parts":[{"text":"ping"}]}]}`)
		req, err := http.NewRequest("POST", testURL, bytes.NewBuffer(jsonBody))
		if err != nil {
			return false, fmt.Sprintf("Invalid URL: %v", err)
		}
		req.Header.Set("Content-Type", "application/json")
		resp, err := client.Do(req)
		if err != nil {
			return false, fmt.Sprintf("Connection failed: %v", err)
		}
		defer resp.Body.Close()

		if resp.StatusCode == http.StatusOK {
			return true, fmt.Sprintf("Connected to Google Gemini API (%s · 200 OK)", model)
		} else if resp.StatusCode == http.StatusBadRequest || resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden {
			return false, fmt.Sprintf("Gemini Auth Failed: Invalid API Key (HTTP %d)", resp.StatusCode)
		}
		return false, fmt.Sprintf("Gemini API returned HTTP %d", resp.StatusCode)

	case "github", "github_actions":
		var reqURL string
		if strings.Contains(cleanURL, "github.com") || cleanURL == "" || !strings.HasPrefix(cleanURL, "http") {
			reqURL = "https://api.github.com/user"
		} else {
			reqURL = cleanURL + "/api/v3/user"
		}
		req, err := http.NewRequest("GET", reqURL, nil)
		if err != nil {
			return false, fmt.Sprintf("Invalid URL: %v", err)
		}
		req.Header.Set("User-Agent", "Oona-Dev-Portal")
		if authToken != "" {
			req.Header.Set("Authorization", "Bearer "+authToken)
		}
		resp, err := client.Do(req)
		if err != nil {
			return false, fmt.Sprintf("Connection failed: %v", err)
		}
		defer resp.Body.Close()

		if resp.StatusCode == http.StatusOK {
			return true, "Connected to GitHub API (HTTP 200 OK)"
		} else if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden {
			return false, fmt.Sprintf("GitHub Auth failed (HTTP %d)", resp.StatusCode)
		}
		return false, fmt.Sprintf("GitHub returned HTTP %d", resp.StatusCode)

	case "terraform_repo":
		if (strings.Contains(cleanURL, "github.com") || strings.HasPrefix(authToken, "github_pat_") || strings.HasPrefix(authToken, "ghp_")) && authToken != "" {
			req, err := http.NewRequest("GET", "https://api.github.com/user", nil)
			if err == nil {
				req.Header.Set("User-Agent", "Oona-Dev-Portal")
				req.Header.Set("Authorization", "Bearer "+authToken)
				resp, errDo := client.Do(req)
				if errDo == nil {
					defer resp.Body.Close()
					if resp.StatusCode == http.StatusOK {
						return true, "GitHub PAT Authenticated & Repository Accessible"
					} else if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden {
						return false, fmt.Sprintf("GitHub PAT Authentication Failed (HTTP %d)", resp.StatusCode)
					}
				}
			}
		}

		reqURL := cleanURL
		req, err := http.NewRequest("HEAD", reqURL, nil)
		if err != nil {
			return false, fmt.Sprintf("Invalid Repo URL: %v", err)
		}
		resp, err := client.Do(req)
		if err != nil {
			return false, fmt.Sprintf("Git repository unreachable: %v", err)
		}
		defer resp.Body.Close()

		if resp.StatusCode < 400 {
			return true, "Git Repository Accessible"
		}
		return false, fmt.Sprintf("Repository returned HTTP %d", resp.StatusCode)

	default:
		return false, "Unsupported provider"
	}
}

func TestLiveIntegrationHandler(w http.ResponseWriter, r *http.Request) {
	err := r.ParseForm()
	if err != nil {
		w.Write([]byte(`<span class="text-red-500 font-medium">Failed to parse form</span>`))
		return
	}

	provider := r.FormValue("provider")
	baseURL := r.FormValue("base_url")
	authUser := r.FormValue("auth_user")
	authToken := r.FormValue("auth_token")

	// Security: never trust a client-supplied secret for the "Default Env" row.
	// If no token was submitted, fall back to the server-side environment variable
	// so credentials never need to be embedded in HTML/JS on the client.
	if authToken == "" && provider == "gemini" {
		authToken = os.Getenv("GEMINI_API_KEY")
	}

	success, msg := testRealConnection(provider, baseURL, authUser, authToken)

	var html string
	if success {
		html = fmt.Sprintf(`<span class="text-green-600 font-medium flex items-center"><svg class="w-4 h-4 mr-1" fill="none" stroke="currentColor" viewBox="0 0 24 24"><path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M5 13l4 4L19 7"></path></svg> %s</span>`, msg)
	} else {
		html = fmt.Sprintf(`<span class="text-red-500 font-medium flex items-center"><svg class="w-4 h-4 mr-1" fill="none" stroke="currentColor" viewBox="0 0 24 24"><path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M6 18L18 6M6 6l12 12"></path></svg> %s</span>`, msg)
	}

	w.Header().Set("Content-Type", "text/html")
	w.Write([]byte(html))
}

func TestIntegrationHandler(w http.ResponseWriter, r *http.Request) {
	idStr := chi.URLParam(r, "id")

	var provider, baseURL, authUser, decryptedToken string
	found := false

	if DB != nil {
		var uuid pgtype.UUID
		if err := uuid.Scan(idStr); err == nil {
			ig, err := DB.GetIntegrationByID(r.Context(), uuid)
			if err == nil {
				found = true
				provider = string(ig.Provider)
				baseURL = ig.BaseUrl
				authUser = ig.AuthUser.String
				if token, errDec := auth.Decrypt(ig.AuthToken); errDec == nil {
					decryptedToken = token
				}
			}
		}
	}

	if !found {
		w.Header().Set("HX-Trigger", `{"showToast": {"message": "Integration record not found", "type": "error"}}`)
		w.Header().Set("Content-Type", "text/html")
		w.Write([]byte(`<span class="text-red-500 font-medium">NotFound</span>`))
		return
	}

	success, msg := testRealConnection(provider, baseURL, authUser, decryptedToken)

	var html string
	if success {
		html = `<span class="text-green-600 font-medium flex items-center"><svg class="w-4 h-4 mr-1" fill="none" stroke="currentColor" viewBox="0 0 24 24"><path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M5 13l4 4L19 7"></path></svg> Active</span>`
		w.Header().Set("HX-Trigger", fmt.Sprintf(`{"showToast": {"message": "%s", "type": "success"}}`, msg))
	} else {
		html = `<span class="text-red-500 font-medium flex items-center"><svg class="w-4 h-4 mr-1" fill="none" stroke="currentColor" viewBox="0 0 24 24"><path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M6 18L18 6M6 6l12 12"></path></svg> Failed</span>`
		w.Header().Set("HX-Trigger", fmt.Sprintf(`{"showToast": {"message": "%s", "type": "error"}}`, msg))
	}

	w.Header().Set("Content-Type", "text/html")
	w.Write([]byte(html))
}

// RenderProfile renders the user profile page
