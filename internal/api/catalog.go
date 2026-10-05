package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"service-catalog/internal/auth"
	"service-catalog/internal/notify"
	db "service-catalog/internal/repository/postgres/generated"
)

const catalogStoreFilePath = "internal/docs/cache/catalog_store.json"

// CatalogEntry represents a service in the catalog
type CatalogEntry struct {
	ID              string   `json:"id"`
	Name            string   `json:"name"`
	PipelineName    string   `json:"pipeline_name"`
	Description     string   `json:"description"`
	Domain          string   `json:"domain"`
	Country         string   `json:"country"`
	Status          string   `json:"status"`
	RepoURL         string   `json:"repo_url"`
	RequestorEmail  string   `json:"requestor_email"`
	JiraID          string   `json:"jira_id"`
	AWSLastModified string   `json:"aws_last_modified"`
	AWSLastInvoked  string   `json:"aws_last_invoked"`
	CreatedAt       string   `json:"created_at"`
	DeployedEnvs    []string `json:"deployed_envs"`
}

// CatalogStore is a thread-safe store for catalog entries with disk persistence
type CatalogStore struct {
	mu      sync.RWMutex
	Entries []CatalogEntry
}

func (cs *CatalogStore) loadFromDisk() {
	data, err := os.ReadFile(catalogStoreFilePath)
	if err != nil || len(data) == 0 {
		return
	}
	var loaded []CatalogEntry
	if err := json.Unmarshal(data, &loaded); err == nil && len(loaded) > 0 {
		cs.Entries = loaded
	}
}

func (cs *CatalogStore) saveToDisk() {
	_ = os.MkdirAll("internal/docs/cache", 0755)
	data, err := json.MarshalIndent(cs.Entries, "", "  ")
	if err == nil {
		_ = os.WriteFile(catalogStoreFilePath, data, 0644)
	}
}

// ServiceCatalog holds the global catalog data
var ServiceCatalog = &CatalogStore{
	Entries: []CatalogEntry{},
}

func init() {
	ServiceCatalog.loadFromDisk()
}

// ListAll returns all catalog entries, reading from DB if available and merging with in-memory updates
func (cs *CatalogStore) ListAll() []CatalogEntry {
	cs.mu.RLock()
	defer cs.mu.RUnlock()

	// If in-memory entries exist, create a map of in-memory entries by ID and Name
	memEntries := make([]CatalogEntry, len(cs.Entries))
	copy(memEntries, cs.Entries)

	if DB != nil {
		entries, err := DB.ListCatalogEntries(context.Background())
		if err == nil && len(entries) > 0 {
			var result []CatalogEntry
			dbSeen := make(map[string]bool)

			for _, e := range entries {
				var idStr string
				if e.ID.Valid {
					idStr = fmt.Sprintf("%x-%x-%x-%x-%x", e.ID.Bytes[0:4], e.ID.Bytes[4:6], e.ID.Bytes[6:8], e.ID.Bytes[8:10], e.ID.Bytes[10:16])
				} else {
					idStr = e.Name
				}

				// Find matching in-memory entry for metadata overrides (RequestorEmail, JiraID, AWS Telemetry, updated fields)
				reqEmail := "admin@oona-insurance.com"
				if e.RequestorEmail.Valid && e.RequestorEmail.String != "" {
					reqEmail = e.RequestorEmail.String
				}
				jiraID := "OONA-1001"
				if e.JiraID.Valid && e.JiraID.String != "" {
					jiraID = e.JiraID.String
				}
				lastMod := "2026-08-12 14:22 UTC"
				if e.AwsLastModified.Valid && e.AwsLastModified.String != "" {
					lastMod = e.AwsLastModified.String
				}
				lastInv := "15:58 WIB (200 OK)"
				if e.AwsLastInvoked.Valid && e.AwsLastInvoked.String != "" {
					lastInv = e.AwsLastInvoked.String
				}
				name := e.Name
				desc := e.Description
				domain := e.Domain.String
				country := e.Country.String
				status := e.Status
				repoURL := e.RepoUrl.String
				pipelineName := e.PipelineName.String

				for _, mem := range memEntries {
					if mem.ID == idStr || strings.EqualFold(mem.Name, e.Name) || strings.EqualFold(mem.ID, idStr) {
						if mem.Name != "" {
							name = mem.Name
						}
						if mem.Description != "" {
							desc = mem.Description
						}
						if mem.Domain != "" {
							domain = mem.Domain
						}
						if mem.Country != "" {
							country = mem.Country
						}
						if mem.RepoURL != "" {
							repoURL = mem.RepoURL
						}
						if mem.RequestorEmail != "" {
							reqEmail = mem.RequestorEmail
						}
						if mem.JiraID != "" {
							jiraID = mem.JiraID
						}
						if mem.AWSLastModified != "" {
							lastMod = mem.AWSLastModified
						}
						if mem.AWSLastInvoked != "" {
							lastInv = mem.AWSLastInvoked
						}
						break
					}
				}

				dbSeen[strings.ToLower(name)] = true
				dbSeen[strings.ToLower(e.Name)] = true

				result = append(result, CatalogEntry{
					ID:              idStr,
					Name:            name,
					PipelineName:    pipelineName,
					Description:     desc,
					Domain:          domain,
					Country:         country,
					Status:          status,
					RepoURL:         repoURL,
					RequestorEmail:  reqEmail,
					JiraID:          jiraID,
					AWSLastModified: lastMod,
					AWSLastInvoked:  lastInv,
					CreatedAt:       e.CreatedAt.Time.Format("2006-01-02"),
					DeployedEnvs:    e.DeployedEnvs,
				})
			}

			// Also append any in-memory entries that aren't in DB yet
			for _, mem := range memEntries {
				if !dbSeen[strings.ToLower(mem.Name)] {
					result = append(result, mem)
				}
			}

			return result
		}
	}

	return memEntries
}

// FindByName returns a catalog entry by service name
func (cs *CatalogStore) FindByName(name string) (CatalogEntry, bool) {
	all := cs.ListAll()
	for _, e := range all {
		if e.Name == name {
			return e, true
		}
	}
	return CatalogEntry{}, false
}

// FindByNameOrID returns a catalog entry by ID, exact name, or matching clean name
func (cs *CatalogStore) FindByNameOrID(param string) (CatalogEntry, bool) {
	all := cs.ListAll()

	// 1. First pass: exact match by ID or exact Name
	for _, e := range all {
		if e.ID == param || e.Name == param {
			return e, true
		}
	}

	// 2. Second pass: clean name match
	paramClean := strings.TrimSuffix(param, "-clone")
	paramClean = strings.TrimPrefix(paramClean, "lmd-oona-ph-integration-")
	paramClean = strings.TrimPrefix(paramClean, "lmd-oona-id-integration-")
	paramClean = strings.TrimPrefix(paramClean, "lmd-oona-")

	for _, e := range all {
		eClean := strings.TrimSuffix(e.Name, "-clone")
		eClean = strings.TrimPrefix(eClean, "lmd-oona-ph-integration-")
		eClean = strings.TrimPrefix(eClean, "lmd-oona-id-integration-")
		eClean = strings.TrimPrefix(eClean, "lmd-oona-")
		if eClean == paramClean {
			return e, true
		}
	}

	// 3. Fallback: if entries exist, return first
	if len(all) > 0 {
		return all[0], true
	}

	return CatalogEntry{}, false
}

// Delete removes a catalog entry by ID or Name, and deletes from PostgreSQL database
func (cs *CatalogStore) Delete(id string) bool {
	cs.mu.Lock()
	defer cs.mu.Unlock()

	found := false
	deletedName := ""

	// 1. Remove from in-memory entries
	for i, e := range cs.Entries {
		if e.ID == id || strings.EqualFold(e.Name, id) || strings.EqualFold(e.ID, id) {
			deletedName = e.Name
			cs.Entries = append(cs.Entries[:i], cs.Entries[i+1:]...)
			found = true
			break
		}
	}

	// 2. Delete from PostgreSQL if database is active
	if DB != nil {
		ctx := context.Background()
		var uuidVal pgtype.UUID
		err := uuidVal.Scan(id)
		if err == nil && uuidVal.Valid {
			_ = DB.DeleteCatalogEntry(ctx, uuidVal)
			found = true
		} else {
			// If not a valid UUID string, try looking up by name and delete
			entry, err := DB.GetCatalogEntryByName(ctx, id)
			if err == nil && entry.ID.Valid {
				_ = DB.DeleteCatalogEntry(ctx, entry.ID)
				found = true
			} else if deletedName != "" {
				entry2, err2 := DB.GetCatalogEntryByName(ctx, deletedName)
				if err2 == nil && entry2.ID.Valid {
					_ = DB.DeleteCatalogEntry(ctx, entry2.ID)
					found = true
				}
			}
		}
	}

	if found {
		cs.saveToDisk()
	}

	return found
}

// Add inserts a new catalog entry
func (cs *CatalogStore) Add(entry CatalogEntry) {
	cs.mu.Lock()
	defer cs.mu.Unlock()
	cs.Entries = append(cs.Entries, entry)

	if DB != nil {
		_, _ = DB.UpsertCatalogEntry(context.Background(), db.UpsertCatalogEntryParams{
			Name:            entry.Name,
			Description:     entry.Description,
			Domain:          pgtype.Text{String: entry.Domain, Valid: entry.Domain != ""},
			Country:         pgtype.Text{String: entry.Country, Valid: entry.Country != ""},
			Status:          entry.Status,
			RepoUrl:         pgtype.Text{String: entry.RepoURL, Valid: entry.RepoURL != ""},
			PipelineName:    pgtype.Text{String: entry.PipelineName, Valid: entry.PipelineName != ""},
			RequestorEmail:  pgtype.Text{String: entry.RequestorEmail, Valid: entry.RequestorEmail != ""},
			JiraID:          pgtype.Text{String: entry.JiraID, Valid: entry.JiraID != ""},
			AwsLastModified: pgtype.Text{String: entry.AWSLastModified, Valid: entry.AWSLastModified != ""},
			AwsLastInvoked:  pgtype.Text{String: entry.AWSLastInvoked, Valid: entry.AWSLastInvoked != ""},
		})
	}
}

// Archive marks a catalog entry as ARCHIVED (soft delete) in memory and DB
func (cs *CatalogStore) Archive(id string) (CatalogEntry, bool) {
	cs.mu.Lock()
	defer cs.mu.Unlock()

	var archived CatalogEntry
	found := false

	// 1. Remove from in-memory active entries
	for i, e := range cs.Entries {
		if e.ID == id || strings.EqualFold(e.Name, id) {
			cs.Entries[i].Status = "ARCHIVED"
			archived = cs.Entries[i]
			cs.Entries = append(cs.Entries[:i], cs.Entries[i+1:]...)
			found = true
			break
		}
	}

	// 2. Mark as archived in PostgreSQL
	if DB != nil {
		ctx := context.Background()
		var uuidVal pgtype.UUID
		err := uuidVal.Scan(id)
		if err == nil && uuidVal.Valid {
			_ = DB.ArchiveCatalogEntry(ctx, uuidVal)
			found = true
		} else {
			entry, err := DB.GetCatalogEntryByName(ctx, id)
			if err == nil && entry.ID.Valid {
				_ = DB.ArchiveCatalogEntry(ctx, entry.ID)
				if archived.Name == "" {
					archived.Name = entry.Name
					archived.Domain = entry.Domain.String
					archived.Country = entry.Country.String
				}
				found = true
			} else if archived.Name != "" {
				entry2, err2 := DB.GetCatalogEntryByName(ctx, archived.Name)
				if err2 == nil && entry2.ID.Valid {
					_ = DB.ArchiveCatalogEntry(ctx, entry2.ID)
					found = true
				}
			}
		}
	}

	if found {
		cs.saveToDisk()
	}

	return archived, found
}

// Restore brings an archived catalog entry back to active LIVE status
func (cs *CatalogStore) Restore(id string) (CatalogEntry, bool) {
	cs.mu.Lock()
	defer cs.mu.Unlock()

	var restored CatalogEntry
	found := false

	if DB != nil {
		ctx := context.Background()
		var uuidVal pgtype.UUID
		err := uuidVal.Scan(id)
		if err != nil || !uuidVal.Valid {
			rows, _ := DB.ListArchivedCatalogEntries(ctx)
			for _, r := range rows {
				if strings.EqualFold(r.Name, id) {
					uuidVal = r.ID
					break
				}
			}
		}

		if uuidVal.Valid {
			if err := DB.RestoreCatalogEntry(ctx, uuidVal); err == nil {
				found = true
				if e, err := DB.GetCatalogEntryByID(ctx, uuidVal); err == nil {
					restored = CatalogEntry{
						ID:              fmt.Sprintf("%x-%x-%x-%x-%x", e.ID.Bytes[0:4], e.ID.Bytes[4:6], e.ID.Bytes[6:8], e.ID.Bytes[8:10], e.ID.Bytes[10:16]),
						Name:            e.Name,
						Description:     e.Description,
						Domain:          e.Domain.String,
						Country:         e.Country.String,
						Status:          "LIVE",
						RepoURL:         e.RepoUrl.String,
						PipelineName:    e.PipelineName.String,
						RequestorEmail:  e.RequestorEmail.String,
						JiraID:          e.JiraID.String,
						AWSLastModified: e.AwsLastModified.String,
						AWSLastInvoked:  e.AwsLastInvoked.String,
						CreatedAt:       e.CreatedAt.Time.Format("2006-01-02 15:04"),
						DeployedEnvs:    e.DeployedEnvs,
					}
					cs.Entries = append(cs.Entries, restored)
				}
			}
		}
	}

	if found {
		cs.saveToDisk()
	}

	return restored, found
}

// PermanentDelete permanently removes a catalog entry from DB and memory
func (cs *CatalogStore) PermanentDelete(id string) (string, bool) {
	cs.mu.Lock()
	defer cs.mu.Unlock()

	deletedName := ""
	found := false

	for i, e := range cs.Entries {
		if e.ID == id || strings.EqualFold(e.Name, id) {
			deletedName = e.Name
			cs.Entries = append(cs.Entries[:i], cs.Entries[i+1:]...)
			found = true
			break
		}
	}

	if DB != nil {
		ctx := context.Background()
		var uuidVal pgtype.UUID
		err := uuidVal.Scan(id)
		if err != nil || !uuidVal.Valid {
			rows, _ := DB.ListArchivedCatalogEntries(ctx)
			for _, r := range rows {
				if strings.EqualFold(r.Name, id) {
					uuidVal = r.ID
					deletedName = r.Name
					break
				}
			}
		} else if deletedName == "" {
			rows, _ := DB.ListArchivedCatalogEntries(ctx)
			for _, r := range rows {
				if r.ID == uuidVal {
					deletedName = r.Name
					break
				}
			}
		}

		if uuidVal.Valid {
			if err := DB.PermanentlyDeleteCatalogEntry(ctx, uuidVal); err == nil {
				found = true
			}
		}

		if deletedName != "" {
			_ = DB.DeleteTicketsByServiceName(ctx, deletedName)
		}
	}

	if deletedName != "" {
		// Purge Valkey / Redis cache for Trivy reports and scan schedules
		rdb := getTrivyRedisClient()
		if rdb != nil {
			ctx := context.Background()
			pattern := fmt.Sprintf("trivy:report:%s:*", deletedName)
			keys, _ := rdb.Keys(ctx, pattern).Result()
			if len(keys) > 0 {
				_ = rdb.Del(ctx, keys...).Err()
			}
			_ = rdb.Del(ctx, fmt.Sprintf("scan_schedule:%s", deletedName)).Err()
		}

		// Clear in-memory caches
		trivyScanHtmlCacheMu.Lock()
		for k := range trivyScanHtmlCache {
			if strings.Contains(k, deletedName) {
				delete(trivyScanHtmlCache, k)
			}
		}
		trivyScanHtmlCacheMu.Unlock()

		tfvarsCacheMu.Lock()
		for k := range tfvarsCache {
			if strings.Contains(k, deletedName) {
				delete(tfvarsCache, k)
			}
		}
		tfvarsCacheMu.Unlock()

		// Purge disk cache for TechDocs & JSON
		_ = os.RemoveAll(filepath.Join("internal/docs/cache", deletedName))
		_ = os.RemoveAll(filepath.Join("internal/docs/cache", "lmd-oona-ph-integration-"+deletedName))
		_ = os.RemoveAll(filepath.Join("internal/docs/cache", "lmd-oona-id-integration-"+deletedName))
	}

	if found {
		cs.saveToDisk()
	}

	return deletedName, found
}

// ArchiveCatalogHandler handles POST /api/v1/catalog/{id}/archive (Lead, DevOps, Admin)
func ArchiveCatalogHandler(w http.ResponseWriter, r *http.Request) {
	claims, _ := r.Context().Value(userCtxKey).(*auth.Claims)
	if claims == nil {
		http.Error(w, `{"error": "Unauthorized"}`, http.StatusUnauthorized)
		return
	}

	// RBAC: Lead, DevOps, and Admin can archive
	if claims.Role != "admin" && claims.Role != "devops" && claims.Role != "lead" {
		w.Header().Set("HX-Trigger", `{"showToast": {"message": "Hanya Lead, DevOps, atau Admin yang dapat mengarsipkan service", "type": "error"}}`)
		http.Error(w, `{"error": "Forbidden: Requires Lead, DevOps, or Admin role"}`, http.StatusForbidden)
		return
	}

	id := chi.URLParam(r, "id")
	if id == "" {
		w.Header().Set("HX-Trigger", `{"showToast": {"message": "Service ID is required", "type": "error"}}`)
		http.Error(w, `{"error": "Service ID is required"}`, http.StatusBadRequest)
		return
	}

	reason := strings.TrimSpace(r.FormValue("reason"))
	if reason == "" {
		reason = "Archived via service catalog detail action"
	}

	archived, found := ServiceCatalog.Archive(id)
	if !found {
		w.Header().Set("HX-Trigger", `{"showToast": {"message": "Service tidak ditemukan", "type": "error"}}`)
		http.Error(w, `{"error": "Service not found"}`, http.StatusNotFound)
		return
	}

	serviceName := archived.Name
	if serviceName == "" {
		serviceName = id
	}

	// 1. Audit Logging
	RecordAudit(r.Context(), r, "ARCHIVE_SERVICE", "catalog", serviceName, map[string]interface{}{
		"actor_email": claims.Email,
		"actor_role":  claims.Role,
		"service_id":  archived.ID,
		"domain":      archived.Domain,
		"country":     archived.Country,
		"reason":      reason,
	})

	// 2. Notification to MS Teams (Alert if lead or devops archives a service)
	teamsTitle := fmt.Sprintf("⚠️ Service Archived: %s", serviceName)
	teamsMsg := fmt.Sprintf("User **%s** (Role: `%s`) telah memindahkan service **%s** (%s/%s) ke Archived Services.\n\n**Alasan:** %s",
		claims.Email, claims.Role, serviceName, archived.Country, archived.Domain, reason)
	_ = notify.SendToTeams(teamsTitle, teamsMsg, "D97706") // Amber theme color

	w.Header().Set("HX-Trigger", fmt.Sprintf(`{"showToast": {"message": "Service '%s' berhasil dipindahkan ke Archived Services", "type": "warning"}}`, serviceName))
	if r.Header.Get("HX-Request") == "true" {
		w.Header().Set("HX-Redirect", "/catalog")
		w.WriteHeader(http.StatusOK)
		return
	}
	http.Redirect(w, r, "/catalog", http.StatusSeeOther)
}

// RestoreCatalogHandler handles POST /api/v1/admin/catalog/{id}/restore (Strictly Admin)
func RestoreCatalogHandler(w http.ResponseWriter, r *http.Request) {
	claims, _ := r.Context().Value(userCtxKey).(*auth.Claims)
	if claims == nil || claims.Role != "admin" {
		w.Header().Set("HX-Trigger", `{"showToast": {"message": "Akses ditolak: Hanya Admin yang dapat me-restore service", "type": "error"}}`)
		http.Error(w, `{"error": "Forbidden: Admin role required"}`, http.StatusForbidden)
		return
	}

	id := chi.URLParam(r, "id")
	if id == "" {
		http.Error(w, `{"error": "Service ID is required"}`, http.StatusBadRequest)
		return
	}

	restored, found := ServiceCatalog.Restore(id)
	if !found {
		w.Header().Set("HX-Trigger", `{"showToast": {"message": "Service tidak ditemukan di arsip", "type": "error"}}`)
		http.Error(w, `{"error": "Service not found in archive"}`, http.StatusNotFound)
		return
	}

	serviceName := restored.Name
	if serviceName == "" {
		serviceName = id
	}

	// 1. Audit Logging
	RecordAudit(r.Context(), r, "RESTORE_SERVICE", "catalog", serviceName, map[string]interface{}{
		"actor_email": claims.Email,
		"actor_role":  claims.Role,
		"service_id":  restored.ID,
		"domain":      restored.Domain,
		"country":     restored.Country,
	})

	// 2. Notification to MS Teams
	teamsTitle := fmt.Sprintf("♻️ Service Restored: %s", serviceName)
	teamsMsg := fmt.Sprintf("Admin **%s** telah me-restore service **%s** kembali ke katalog aktif.", claims.Email, serviceName)
	_ = notify.SendToTeams(teamsTitle, teamsMsg, "10B981") // Emerald theme color

	w.Header().Set("HX-Trigger", fmt.Sprintf(`{"showToast": {"message": "Service '%s' berhasil di-restore ke katalog", "type": "success"}}`, serviceName))
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte(`{"status": "restored"}`))
}

// PermanentDeleteCatalogHandler handles DELETE /api/v1/admin/catalog/{id}/permanent (Strictly Admin)
func PermanentDeleteCatalogHandler(w http.ResponseWriter, r *http.Request) {
	claims, _ := r.Context().Value(userCtxKey).(*auth.Claims)
	if claims == nil || claims.Role != "admin" {
		w.Header().Set("HX-Trigger", `{"showToast": {"message": "Akses ditolak: Hanya Admin yang dapat menghapus permanen", "type": "error"}}`)
		http.Error(w, `{"error": "Forbidden: Admin role required"}`, http.StatusForbidden)
		return
	}

	id := chi.URLParam(r, "id")
	if id == "" {
		http.Error(w, `{"error": "Service ID is required"}`, http.StatusBadRequest)
		return
	}

	deletedName, found := ServiceCatalog.PermanentDelete(id)
	if !found {
		w.Header().Set("HX-Trigger", `{"showToast": {"message": "Service tidak ditemukan", "type": "error"}}`)
		http.Error(w, `{"error": "Service not found"}`, http.StatusNotFound)
		return
	}

	if deletedName == "" {
		deletedName = id
	}

	// 1. Audit Logging
	RecordAudit(r.Context(), r, "PERMANENT_DELETE_SERVICE", "catalog", deletedName, map[string]interface{}{
		"actor_email": claims.Email,
		"actor_role":  claims.Role,
	})

	// 2. Notification to MS Teams
	teamsTitle := fmt.Sprintf("🗑️ Service Permanently Deleted: %s", deletedName)
	teamsMsg := fmt.Sprintf("Admin **%s** telah menghapus permanen service **%s** dari database.", claims.Email, deletedName)
	_ = notify.SendToTeams(teamsTitle, teamsMsg, "EF4444") // Rose/Red theme color

	w.Header().Set("HX-Trigger", fmt.Sprintf(`{"showToast": {"message": "Service '%s' berhasil dihapus permanen", "type": "info"}}`, deletedName))
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte(`{"status": "permanently_deleted"}`))
}

// DeleteCatalogHandler handles DELETE /api/v1/catalog/{id}
func DeleteCatalogHandler(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	if id == "" {
		w.Header().Set("HX-Trigger", `{"showToast": {"message": "Service ID is required", "type": "error"}}`)
		w.WriteHeader(http.StatusBadRequest)
		return
	}

	found := ServiceCatalog.Delete(id)
	if !found {
		w.Header().Set("HX-Trigger", `{"showToast": {"message": "Service not found", "type": "error"}}`)
		w.WriteHeader(http.StatusNotFound)
		return
	}

	w.Header().Set("HX-Trigger", fmt.Sprintf(`{"showToast": {"message": "Service deleted successfully", "type": "success"}}`))
	if r.Header.Get("HX-Request") == "true" {
		w.Header().Set("HX-Redirect", "/catalog")
		w.WriteHeader(http.StatusOK)
		return
	}
	http.Redirect(w, r, "/catalog", http.StatusSeeOther)
}

// Update updates an existing catalog entry by ID and persists to DB
func (cs *CatalogStore) Update(id string, updated CatalogEntry) bool {
	cs.mu.Lock()
	defer cs.mu.Unlock()

	found := false
	for i, e := range cs.Entries {
		if e.ID == id || strings.EqualFold(e.Name, id) || strings.EqualFold(e.Name, updated.Name) || fmt.Sprintf("svc-%s", id) == e.ID {
			updated.ID = e.ID
			if updated.CreatedAt == "" {
				updated.CreatedAt = e.CreatedAt
			}
			if updated.AWSLastModified == "" {
				updated.AWSLastModified = e.AWSLastModified
			}
			if updated.AWSLastInvoked == "" {
				updated.AWSLastInvoked = e.AWSLastInvoked
			}
			cs.Entries[i] = updated
			found = true
			break
		}
	}

	if !found {
		if updated.ID == "" {
			updated.ID = id
		}
		if updated.AWSLastModified == "" {
			updated.AWSLastModified = "2026-08-12 14:22 UTC"
		}
		if updated.AWSLastInvoked == "" {
			updated.AWSLastInvoked = "15:58 WIB (200 OK)"
		}
		cs.Entries = append(cs.Entries, updated)
	}

	if DB != nil {
		_, _ = DB.UpsertCatalogEntry(context.Background(), db.UpsertCatalogEntryParams{
			Name:            updated.Name,
			Description:     updated.Description,
			Domain:          pgtype.Text{String: updated.Domain, Valid: updated.Domain != ""},
			Country:         pgtype.Text{String: updated.Country, Valid: updated.Country != ""},
			Status:          updated.Status,
			RepoUrl:         pgtype.Text{String: updated.RepoURL, Valid: updated.RepoURL != ""},
			PipelineName:    pgtype.Text{String: updated.PipelineName, Valid: updated.PipelineName != ""},
			RequestorEmail:  pgtype.Text{String: updated.RequestorEmail, Valid: updated.RequestorEmail != ""},
			JiraID:          pgtype.Text{String: updated.JiraID, Valid: updated.JiraID != ""},
			AwsLastModified: pgtype.Text{String: updated.AWSLastModified, Valid: updated.AWSLastModified != ""},
			AwsLastInvoked:  pgtype.Text{String: updated.AWSLastInvoked, Valid: updated.AWSLastInvoked != ""},
		})
	}

	cs.saveToDisk()
	return true
}

// UpdateStatus updates the status of a catalog entry by ID or Name
func (cs *CatalogStore) UpdateStatus(idOrName string, status string) bool {
	return cs.UpdateStatusAndPipeline(idOrName, status, "")
}

// UpdateStatusAndPipeline updates the status and pipeline name of a catalog entry by ID or Name
func (cs *CatalogStore) UpdateStatusAndPipeline(idOrName string, status string, pipelineName string) bool {
	cs.mu.Lock()
	defer cs.mu.Unlock()

	for i, e := range cs.Entries {
		if e.ID == idOrName || e.Name == idOrName || fmt.Sprintf("svc-%s", idOrName) == e.ID {
			cs.Entries[i].Status = status
			if pipelineName != "" {
				cs.Entries[i].PipelineName = pipelineName
			}

			if DB != nil {
				_, _ = DB.UpsertCatalogEntry(context.Background(), db.UpsertCatalogEntryParams{
					Name:            cs.Entries[i].Name,
					Description:     cs.Entries[i].Description,
					Domain:          pgtype.Text{String: cs.Entries[i].Domain, Valid: cs.Entries[i].Domain != ""},
					Country:         pgtype.Text{String: cs.Entries[i].Country, Valid: cs.Entries[i].Country != ""},
					Status:          status,
					RepoUrl:         pgtype.Text{String: cs.Entries[i].RepoURL, Valid: cs.Entries[i].RepoURL != ""},
					PipelineName:    pgtype.Text{String: cs.Entries[i].PipelineName, Valid: cs.Entries[i].PipelineName != ""},
					RequestorEmail:  pgtype.Text{String: cs.Entries[i].RequestorEmail, Valid: cs.Entries[i].RequestorEmail != ""},
					JiraID:          pgtype.Text{String: cs.Entries[i].JiraID, Valid: cs.Entries[i].JiraID != ""},
					AwsLastModified: pgtype.Text{String: cs.Entries[i].AWSLastModified, Valid: cs.Entries[i].AWSLastModified != ""},
					AwsLastInvoked:  pgtype.Text{String: cs.Entries[i].AWSLastInvoked, Valid: cs.Entries[i].AWSLastInvoked != ""},
				})
			}
			return true
		}
	}
	return false
}

// AddDeployedEnv marks an environment (e.g. "preprod", "prod") as active/deployed for a service
func (cs *CatalogStore) AddDeployedEnv(idOrName, env string) bool {
	cs.mu.Lock()
	defer cs.mu.Unlock()

	envLower := strings.ToLower(strings.TrimSpace(env))
	if envLower == "" {
		return false
	}

	for i, e := range cs.Entries {
		if e.ID == idOrName || e.Name == idOrName || fmt.Sprintf("svc-%s", idOrName) == e.ID {
			found := false
			for _, existing := range cs.Entries[i].DeployedEnvs {
				if strings.EqualFold(existing, envLower) {
					found = true
					break
				}
			}
			if !found {
				cs.Entries[i].DeployedEnvs = append(cs.Entries[i].DeployedEnvs, envLower)
				cs.saveToDisk()
			}
			return true
		}
	}
	return false
}

// UpdateCatalogHandler handles POST /api/v1/catalog/{id}/edit
func UpdateCatalogHandler(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	if id == "" {
		w.Header().Set("HX-Trigger", `{"showToast": {"message": "Service ID is required", "type": "error"}}`)
		w.WriteHeader(http.StatusBadRequest)
		return
	}

	if err := r.ParseForm(); err != nil {
		w.Header().Set("HX-Trigger", `{"showToast": {"message": "Invalid form data", "type": "error"}}`)
		w.WriteHeader(http.StatusBadRequest)
		return
	}

	name := strings.TrimSpace(r.FormValue("name"))
	desc := strings.TrimSpace(r.FormValue("description"))
	domain := strings.TrimSpace(r.FormValue("domain"))
	country := strings.TrimSpace(strings.ToUpper(r.FormValue("country")))
	repoURL := strings.TrimSpace(r.FormValue("repo_url"))
	requestorEmail := strings.TrimSpace(r.FormValue("requestor_email"))
	jiraID := strings.TrimSpace(strings.ToUpper(r.FormValue("jira_id")))

	if name == "" {
		w.Header().Set("HX-Trigger", `{"showToast": {"message": "Service Name is required", "type": "error"}}`)
		w.WriteHeader(http.StatusBadRequest)
		return
	}

	claims, _ := r.Context().Value(userCtxKey).(*auth.Claims)

	// Status is derived automatically from ticket DB - preserve existing
	existingEntry, _ := ServiceCatalog.FindByNameOrID(id)
	autoStatus := existingEntry.Status
	if autoStatus == "" {
		autoStatus = "LIVE"
	}

	if requestorEmail == "" {
		if existingEntry.RequestorEmail != "" {
			requestorEmail = existingEntry.RequestorEmail
		} else if claims != nil && claims.Email != "" {
			requestorEmail = claims.Email
		} else {
			requestorEmail = "admin@oona-insurance.com"
		}
	}

	if jiraID == "" {
		if existingEntry.JiraID != "" {
			jiraID = existingEntry.JiraID
		} else {
			jiraID = "OONA-1001"
		}
	}

	pipelineName := existingEntry.PipelineName
	if pipelineName == "" {
		pipelineName = "lmd-oona-ph-integration-health-renewal-svc-clone"
	}

	updated := CatalogEntry{
		ID:              id,
		Name:            name,
		Description:     desc,
		Domain:          domain,
		Country:         country,
		Status:          autoStatus,
		RepoURL:         repoURL,
		PipelineName:    pipelineName,
		RequestorEmail:  requestorEmail,
		JiraID:          jiraID,
		AWSLastModified: existingEntry.AWSLastModified,
		AWSLastInvoked:  existingEntry.AWSLastInvoked,
	}

	ServiceCatalog.Update(id, updated)

	w.Header().Set("HX-Trigger", `{"showToast": {"message": "Service catalog updated successfully!", "type": "success"}}`)
	redirectURL := "/catalog/" + url.PathEscape(name) + "?toast=updated"
	if r.Header.Get("HX-Request") == "true" {
		w.Header().Set("HX-Redirect", redirectURL)
		w.WriteHeader(http.StatusOK)
		return
	}
	http.Redirect(w, r, redirectURL, http.StatusSeeOther)
}

// DeriveStatusFromTickets maps real DB ticket status to a human-readable catalog status.
// This is the single source of truth for catalog service status.
func DeriveStatusFromTickets(ticketStatus string) string {
	switch strings.ToUpper(ticketStatus) {
	case "DRAFT":
		return "DRAFT"
	case "SCANNING":
		return "SCANNING"
	case "WAITING_INFRA":
		return "PENDING_INFRA"
	case "INFRA_DETECTED":
		return "PENDING_REVIEW"
	case "JENKINS_READY":
		return "DEPLOYING"
	case "LIVE":
		return "LIVE"
	case "REJECTED_SECURITY":
		return "REJECTED"
	default:
		return "PENDING_INFRA"
	}
}

// RequestPipelineHandler handles POST /api/v1/catalog/{service}/request-pipeline
func RequestPipelineHandler(w http.ResponseWriter, r *http.Request) {
	serviceParam := chi.URLParam(r, "service")
	if serviceParam == "" {
		w.Header().Set("HX-Trigger", `{"showToast": {"message": "Service name is required", "type": "error"}}`)
		w.WriteHeader(http.StatusBadRequest)
		return
	}

	claims, _ := r.Context().Value(userCtxKey).(*auth.Claims)

	req := CreateTicketRequest{
		RepoURL:     fmt.Sprintf("https://github.com/oona-insurance/%s.git", serviceParam),
		Domain:      "integration",
		Country:     "ph",
		ServiceName: serviceParam,
		Description: fmt.Sprintf("DevOps Request: Provision Terraform IaC & Jenkins Pipeline for %s", serviceParam),
	}

	pipelineName := fmt.Sprintf("lmd-oona-%s-%s-%s", req.Country, req.Domain, req.ServiceName)

	if DB != nil {
		var userID pgtype.UUID
		if claims != nil {
			userID.Scan(claims.UserID)
		}
		_, _ = DB.CreateTicket(r.Context(), db.CreateTicketParams{
			CreatedBy:    userID,
			RepoUrl:      req.RepoURL,
			Domain:       req.Domain,
			Country:      req.Country,
			ServiceName:  req.ServiceName,
			PipelineName: pipelineName,
			Description:  req.Description,
		})
	}

	w.Header().Set("HX-Trigger", `{"showToast": {"message": "Infra & Pipeline Request submitted! Sent to DevOps Approval Queue (/approvals).", "type": "success"}}`)
	if r.Header.Get("HX-Request") == "true" {
		w.Header().Set("HX-Redirect", "/approvals")
		w.WriteHeader(http.StatusOK)
		return
	}
	http.Redirect(w, r, "/approvals", http.StatusSeeOther)
}

// RequestPromotionHandler handles POST /api/v1/catalog/{service}/promote
func RequestPromotionHandler(w http.ResponseWriter, r *http.Request) {
	serviceParam := chi.URLParam(r, "service")
	if serviceParam == "" {
		http.Error(w, `{"error": "service name is required"}`, http.StatusBadRequest)
		return
	}

	_ = r.ParseForm()
	targetEnv := strings.ToLower(strings.TrimSpace(r.FormValue("target_env")))
	if targetEnv != "preprod" && targetEnv != "prod" {
		w.Header().Set("HX-Trigger", `{"showToast": {"message": "Invalid target environment. Only PreProd or Prod allowed.", "type": "error"}}`)
		http.Error(w, `{"error": "target_env must be preprod or prod"}`, http.StatusBadRequest)
		return
	}

	description := strings.TrimSpace(r.FormValue("description"))
	jiraID := strings.TrimSpace(r.FormValue("jira_issue_id"))
	branch := strings.TrimSpace(r.FormValue("branch"))
	if branch == "" {
		branch = "main"
	}

	claims, _ := r.Context().Value(userCtxKey).(*auth.Claims)

	// Fetch service details from CatalogStore or DB
	var repoURL, domain, country, pipelineName string
	if entry, found := ServiceCatalog.FindByNameOrID(serviceParam); found {
		repoURL = entry.RepoURL
		domain = entry.Domain
		country = entry.Country
		pipelineName = entry.PipelineName
	}
	if repoURL == "" {
		repoURL = fmt.Sprintf("https://github.com/oona-insurance/%s.git", serviceParam)
	}
	if domain == "" {
		domain = "integration"
	}
	if country == "" {
		country = "ph"
	}
	if pipelineName == "" {
		pipelineName = ResolveCanonicalPipelineName(r.Context(), domain, country, serviceParam, branch, "")
	}

	if description == "" {
		description = fmt.Sprintf("Promotion Request: Release %s to %s environment (Branch: %s)", serviceParam, strings.ToUpper(targetEnv), branch)
	}

	var newTicketID string
	if DBPool != nil {
		var userID pgtype.UUID
		if claims != nil {
			_ = userID.Scan(claims.UserID)
		}
		var returnedUUID pgtype.UUID
		err := DBPool.QueryRow(r.Context(), `
			INSERT INTO tickets (
				created_by, repo_url, domain, country, service_name, pipeline_name, jira_issue_id, description, target_env, ticket_type, status
			) VALUES (
				$1, $2, $3, $4, $5, $6, $7, $8, $9, 'PROMOTION', 'INFRA_DETECTED'
			)
			RETURNING id
		`, userID, repoURL, domain, country, serviceParam, pipelineName, pgtype.Text{String: jiraID, Valid: jiraID != ""}, description, targetEnv).Scan(&returnedUUID)
		if err == nil {
			newTicketID = fmt.Sprintf("%x-%x-%x-%x-%x", returnedUUID.Bytes[0:4], returnedUUID.Bytes[4:6], returnedUUID.Bytes[6:8], returnedUUID.Bytes[8:10], returnedUUID.Bytes[10:16])
		}
	}

	// Record audit
	RecordAudit(r.Context(), r, "REQUEST_PROMOTION", "ticket", newTicketID, map[string]interface{}{
		"service_name": serviceParam,
		"target_env":   targetEnv,
		"branch":       branch,
		"jira_id":      jiraID,
	})

	redirectURL := "/approvals"
	if newTicketID != "" {
		redirectURL = "/approvals/" + newTicketID
	}

	w.Header().Set("HX-Trigger", fmt.Sprintf(`{"showToast": {"message": "Promotion Request to %s created! Sent to DevOps Approval Queue.", "type": "success"}}`, strings.ToUpper(targetEnv)))
	if r.Header.Get("HX-Request") == "true" {
		w.Header().Set("HX-Redirect", redirectURL)
		w.WriteHeader(http.StatusOK)
		return
	}
	http.Redirect(w, r, redirectURL, http.StatusSeeOther)
}
