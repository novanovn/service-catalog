package api

import (
	"encoding/json"
	"fmt"
	"html"
	"net/http"
	"os"
	"time"

	"service-catalog/internal/auth"
	db "service-catalog/internal/repository/postgres/generated"
)

type FullBackupBundle struct {
	Version          string               `json:"version"`
	ExportedAt       time.Time            `json:"exported_at"`
	ExportedBy       string               `json:"exported_by"`
	CatalogEntries   []CatalogEntry       `json:"catalog_entries"`
	Users            []db.User            `json:"users,omitempty"`
	SystemParameters []db.SystemParameter `json:"system_parameters,omitempty"`
	Integrations     []db.Integration    `json:"integrations,omitempty"`
	Tickets          []db.Ticket          `json:"tickets,omitempty"`
	Reviews          []*TicketReviewRecord `json:"reviews,omitempty"`
}

// RenderAdminBackup renders the Admin Backup & Restore Management page
func RenderAdminBackup(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	var claims *auth.Claims
	if c, ok := r.Context().Value(userCtxKey).(*auth.Claims); ok {
		claims = c
	}

	var usersCount int64 = 0
	var paramsCount int64 = 0
	var integrationsCount int64 = 0
	var ticketsCount int64 = 0

	if DB != nil {
		if users, err := DB.ListUsers(ctx); err == nil {
			usersCount = int64(len(users))
		}
		if params, err := DB.ListSystemParameters(ctx); err == nil {
			paramsCount = int64(len(params))
		}
		if ints, err := DB.ListIntegrations(ctx); err == nil {
			integrationsCount = int64(len(ints))
		}
		if tix, err := DB.ListTickets(ctx); err == nil {
			ticketsCount = int64(len(tix))
		}
	}

	ServiceCatalog.mu.RLock()
	catalogCount := int64(len(ServiceCatalog.Entries))
	ServiceCatalog.mu.RUnlock()

	data := map[string]interface{}{
		"Title":             "Full Backup & Restore",
		"User":              claims,
		"ActiveTab":         "admin_backup",
		"CatalogCount":      catalogCount,
		"UsersCount":        usersCount,
		"ParamsCount":       paramsCount,
		"IntegrationsCount": integrationsCount,
		"TicketsCount":      ticketsCount,
	}

	tmpl, err := parsePage("admin_backup.html")
	if err != nil {
		http.Error(w, fmt.Sprintf("Template error: %v", err), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "text/html")
	_ = tmpl.ExecuteTemplate(w, "base", data)
}

// ExportBackupHandler handles GET/POST /api/v1/admin/backup/export
func ExportBackupHandler(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	userEmail := "admin@oona-insurance.com"
	if claims, ok := r.Context().Value(userCtxKey).(*auth.Claims); ok && claims.Email != "" {
		userEmail = claims.Email
	}

	bundle := FullBackupBundle{
		Version:    "1.0.0",
		ExportedAt: time.Now(),
		ExportedBy: userEmail,
	}

	// 1. Export Catalog Entries
	ServiceCatalog.mu.RLock()
	bundle.CatalogEntries = append([]CatalogEntry{}, ServiceCatalog.Entries...)
	ServiceCatalog.mu.RUnlock()

	// 2. Export DB Entities
	if DB != nil {
		if users, err := DB.ListUsers(ctx); err == nil {
			sanitizedUsers := make([]db.User, len(users))
			for i, u := range users {
				u.PasswordHash = "[REDACTED]"
				sanitizedUsers[i] = u
			}
			bundle.Users = sanitizedUsers
		}
		if params, err := DB.ListSystemParameters(ctx); err == nil {
			bundle.SystemParameters = params
		}
		if ints, err := DB.ListIntegrations(ctx); err == nil {
			bundle.Integrations = ints
		}
		if tix, err := DB.ListTickets(ctx); err == nil {
			bundle.Tickets = tix
		}
	}

	// 3. Export Ticket Reviews
	TicketReviews.mu.RLock()
	for _, rec := range TicketReviews.reviews {
		if rec != nil {
			bundle.Reviews = append(bundle.Reviews, rec)
		}
	}
	TicketReviews.mu.RUnlock()

	jsonData, err := json.MarshalIndent(bundle, "", "  ")
	if err != nil {
		http.Error(w, fmt.Sprintf("Failed to generate backup JSON: %v", err), http.StatusInternalServerError)
		return
	}

	filename := fmt.Sprintf("oona_portal_backup_%s.json", time.Now().Format("20060102_150405"))

	// Persist a server-side copy in internal/docs/cache/backups/
	_ = os.MkdirAll("internal/docs/cache/backups", 0755)
	_ = os.WriteFile("internal/docs/cache/backups/"+filename, jsonData, 0644)

	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=\"%s\"", filename))
	w.Header().Set("Content-Length", fmt.Sprintf("%d", len(jsonData)))
	w.Write(jsonData)
}

// RestoreBackupHandler handles POST /api/v1/admin/backup/restore
func RestoreBackupHandler(w http.ResponseWriter, r *http.Request) {
	// Parse uploaded multipart file
	err := r.ParseMultipartForm(10 << 20) // 10MB limit
	if err != nil {
		renderRestoreError(w, "Failed to parse uploaded backup file")
		return
	}

	file, _, err := r.FormFile("backup_file")
	if err != nil {
		renderRestoreError(w, "Backup file is required")
		return
	}
	defer file.Close()

	var bundle FullBackupBundle
	if err := json.NewDecoder(file).Decode(&bundle); err != nil {
		renderRestoreError(w, fmt.Sprintf("Invalid backup JSON format: %v", err))
		return
	}

	if bundle.Version == "" {
		renderRestoreError(w, "Unrecognized or corrupt backup file format")
		return
	}

	// 1. Restore Catalog Entries & save to disk
	if len(bundle.CatalogEntries) > 0 {
		ServiceCatalog.mu.Lock()
		ServiceCatalog.Entries = bundle.CatalogEntries
		ServiceCatalog.saveToDisk()
		ServiceCatalog.mu.Unlock()
	}

	// 2. Restore Ticket Reviews in memory
	if len(bundle.Reviews) > 0 {
		TicketReviews.mu.Lock()
		for _, rec := range bundle.Reviews {
			if rec != nil && rec.TicketID != "" {
				TicketReviews.reviews[rec.TicketID] = rec
			}
		}
		TicketReviews.mu.Unlock()
	}

	ctx := r.Context()
	restoredDBMsg := ""

	// 3. Restore DB Users & System Parameters if DB connection active
	if DB != nil {
		restoredUsers := 0
		for _, u := range bundle.Users {
			if u.Email != "" && u.PasswordHash != "" && u.PasswordHash != "[REDACTED]" {
				_, _ = DB.CreateUser(ctx, db.CreateUserParams{
					Email:        u.Email,
					FullName:     u.FullName,
					PasswordHash: u.PasswordHash,
					Role:         u.Role,
				})
				restoredUsers++
			}
		}
		restoredParams := 0
		for _, p := range bundle.SystemParameters {
			if p.Category != "" && p.KeyName != "" {
				_, _ = DB.CreateSystemParameter(ctx, db.CreateSystemParameterParams{
					Category:    p.Category,
					KeyName:     p.KeyName,
					Value:       p.Value,
					Description: p.Description,
					IsActive:    p.IsActive,
				})
				restoredParams++
			}
		}
		restoredInts := 0
		for _, ig := range bundle.Integrations {
			if ig.Name != "" && ig.BaseUrl != "" {
				_, _ = DB.CreateIntegration(ctx, db.CreateIntegrationParams{
					Name:      ig.Name,
					Provider:  ig.Provider,
					BaseUrl:   ig.BaseUrl,
					AuthUser:  ig.AuthUser,
					AuthToken: ig.AuthToken,
				})
				restoredInts++
			}
		}
		restoredDBMsg = fmt.Sprintf("Database entities (%d users, %d params, %d integrations) synchronized.", restoredUsers, restoredParams, restoredInts)
	}

	// Send success HTMX Toast / Response
	w.Header().Set("Content-Type", "text/html")
	w.Header().Set("HX-Trigger", `{"showToast": {"message": "Full system backup restored successfully! Database & Catalog synchronized.", "type": "success"}}`)
	w.Write([]byte(fmt.Sprintf(`
		<div class="p-4 mb-4 text-sm text-emerald-800 rounded-xl bg-emerald-50 border border-emerald-200 shadow-sm flex items-center justify-between" role="alert">
			<div class="flex items-start space-x-3">
				<svg class="w-5 h-5 text-emerald-600 flex-shrink-0 mt-0.5" fill="none" stroke="currentColor" viewBox="0 0 24 24"><path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M5 13l4 4L19 7"></path></svg>
				<div>
					<span class="font-bold text-emerald-900">System Restore Successful!</span>
					<p class="text-xs text-emerald-700 mt-0.5">%d Catalog Services restored. %s</p>
				</div>
			</div>
			<a href="/admin/backup" class="px-3 py-1.5 bg-emerald-600 hover:bg-emerald-500 text-white rounded-md text-xs font-semibold shadow-xs">Reload View</a>
		</div>
	`, len(bundle.CatalogEntries), restoredDBMsg)))
}

func renderRestoreError(w http.ResponseWriter, message string) {
	w.Header().Set("Content-Type", "text/html")
	w.WriteHeader(http.StatusBadRequest)
	w.Write([]byte(fmt.Sprintf(`
		<div class="p-4 mb-4 text-sm text-red-800 rounded-xl bg-red-50 border border-red-200 shadow-sm flex items-center space-x-3" role="alert">
			<svg class="w-5 h-5 text-red-600 flex-shrink-0" fill="none" stroke="currentColor" viewBox="0 0 24 24"><path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M12 8v4m0 4h.01M21 12a9 9 0 11-18 0 9 9 0 0118 0z"></path></svg>
			<div>
				<span class="font-bold text-red-900">Restore Failed</span>
				<p class="text-xs text-red-700 mt-0.5">%s</p>
			</div>
		</div>
	`, html.EscapeString(message))))
}
