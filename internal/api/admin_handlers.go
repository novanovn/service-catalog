package api

import (
	"fmt"
	"log"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"service-catalog/internal/auth"
	db "service-catalog/internal/repository/postgres/generated"
	"golang.org/x/crypto/bcrypt"
)

// validUserRoles are the only role values accepted by CreateUserHandler.
var validUserRoles = map[string]bool{
	"developer": true,
	"devops":    true,
	"admin":     true,
}

// normalizeAssignedShelves cleans and dedupes the raw shelf codes submitted from
// the "Attach to Shelves" checkbox list. Admin/DevOps roles get an implicit global
// wildcard ("*") since folder scoping does not apply to them.
func normalizeAssignedShelves(role string, rawShelves []string) []string {
	if role == "admin" || role == "devops" {
		return []string{"*"}
	}
	seen := make(map[string]bool)
	var out []string
	for _, s := range rawShelves {
		s = strings.TrimSpace(strings.ToLower(s))
		if s == "" || seen[s] {
			continue
		}
		seen[s] = true
		out = append(out, s)
	}
	return out
}

// CreateUserHandler handles POST /api/v1/admin/users
func CreateUserHandler(w http.ResponseWriter, r *http.Request) {
	err := r.ParseForm()
	if err != nil {
		w.Header().Set("Content-Type", "text/html")
		w.WriteHeader(http.StatusBadRequest)
		w.Write([]byte(`
			<div class="p-4 mb-4 text-sm text-red-800 rounded-lg bg-red-50 border border-red-200" role="alert">
				<span class="font-bold">Error!</span> Invalid form data provided.
			</div>
		`))
		return
	}

	fullName := r.FormValue("full_name")
	email := r.FormValue("email")
	role := strings.ToLower(strings.TrimSpace(r.FormValue("role")))
	password := r.FormValue("password")
	rawShelves := r.Form["shelves"]

	if email == "" || fullName == "" || password == "" {
		w.Header().Set("Content-Type", "text/html")
		w.WriteHeader(http.StatusBadRequest)
		w.Write([]byte(`
			<div class="p-4 mb-4 text-sm text-red-800 rounded-lg bg-red-50 border border-red-200" role="alert">
				<span class="font-bold">Error!</span> Full name, email, and password are required.
			</div>
		`))
		return
	}

	if !validUserRoles[role] {
		w.Header().Set("Content-Type", "text/html")
		w.WriteHeader(http.StatusBadRequest)
		w.Write([]byte(`
			<div class="p-4 mb-4 text-sm text-red-800 rounded-lg bg-red-50 border border-red-200" role="alert">
				<span class="font-bold">Error!</span> Invalid role selected.
			</div>
		`))
		return
	}

	assignedShelves := normalizeAssignedShelves(role, rawShelves)

	// Hash password using bcrypt
	hashedPassword, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		w.Header().Set("Content-Type", "text/html")
		w.WriteHeader(http.StatusInternalServerError)
		w.Write([]byte(fmt.Sprintf(`
			<div class="p-4 mb-4 text-sm text-red-800 rounded-lg bg-red-50 border border-red-200" role="alert">
				<span class="font-bold">Error!</span> Password hashing failed: %v
			</div>
		`, err)))
		return
	}

	if DB != nil {
		_, err = DB.CreateUser(r.Context(), db.CreateUserParams{
			Email:           email,
			FullName:        fullName,
			PasswordHash:    string(hashedPassword),
			Role:            db.UserRole(role),
			AssignedShelves: assignedShelves,
		})
		if err != nil {
			w.Header().Set("Content-Type", "text/html")
			w.WriteHeader(http.StatusInternalServerError)
			w.Write([]byte(fmt.Sprintf(`
				<div class="p-4 mb-4 text-sm text-red-800 rounded-lg bg-red-50 border border-red-200" role="alert">
					<span class="font-bold">Database Error!</span> Could not create user: %v
				</div>
			`, err)))
			return
		}
		RecordAudit(r.Context(), r, "CREATE", "user", email, map[string]interface{}{
			"full_name":        fullName,
			"role":             role,
			"assigned_shelves": assignedShelves,
		})
	}

	if r.Header.Get("HX-Request") == "true" {
		w.Header().Set("HX-Redirect", "/admin/users")
		w.WriteHeader(http.StatusOK)
		return
	}
	http.Redirect(w, r, "/admin/users", http.StatusSeeOther)
}

// UpdateUserShelvesHandler handles POST /api/v1/admin/users/{id}/shelves
// Re-assigns which shelves (folders) a developer/devops user can see by default
// in the catalog. Admin and DevOps roles are always forced to wildcard access.
func UpdateUserShelvesHandler(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	if id == "" {
		http.Error(w, "User ID is required", http.StatusBadRequest)
		return
	}

	if err := r.ParseForm(); err != nil {
		w.Header().Set("HX-Trigger", `{"showToast": {"message": "Invalid form data", "type": "error"}}`)
		w.WriteHeader(http.StatusBadRequest)
		return
	}

	rawShelves := r.Form["shelves"]

	if DB == nil {
		w.Header().Set("HX-Trigger", `{"showToast": {"message": "Database unavailable", "type": "error"}}`)
		w.WriteHeader(http.StatusServiceUnavailable)
		return
	}

	var uid pgtype.UUID
	if err := uid.Scan(id); err != nil {
		w.Header().Set("HX-Trigger", `{"showToast": {"message": "Invalid user ID", "type": "error"}}`)
		w.WriteHeader(http.StatusBadRequest)
		return
	}

	// Look up the user's current role so admin/devops keep their forced wildcard.
	existing, listErr := DB.ListUsers(r.Context())
	role := "developer"
	if listErr == nil {
		for _, u := range existing {
			if u.ID == uid {
				role = string(u.Role)
				break
			}
		}
	}

	assignedShelves := normalizeAssignedShelves(role, rawShelves)

	updated, err := DB.UpdateUserShelves(r.Context(), db.UpdateUserShelvesParams{
		ID:              uid,
		AssignedShelves: assignedShelves,
	})
	if err != nil {
		w.Header().Set("HX-Trigger", `{"showToast": {"message": "Failed to update shelf assignment", "type": "error"}}`)
		w.WriteHeader(http.StatusInternalServerError)
		return
	}

	RecordAudit(r.Context(), r, "UPDATE_SHELVES", "user", updated.Email, map[string]interface{}{
		"assigned_shelves": assignedShelves,
	})

	w.Header().Set("HX-Trigger", `{"showToast": {"message": "Folder assignment updated!", "type": "success"}}`)
	if r.Header.Get("HX-Request") == "true" {
		w.Header().Set("HX-Redirect", "/admin/users")
		w.WriteHeader(http.StatusOK)
		return
	}
	http.Redirect(w, r, "/admin/users", http.StatusSeeOther)
}

// DeleteUserHandler handles DELETE /api/v1/admin/users/{id}
func DeleteUserHandler(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	if id == "" {
		http.Error(w, "User ID is required", http.StatusBadRequest)
		return
	}

	if DB != nil {
		var uid pgtype.UUID
		if err := uid.Scan(id); err == nil {
			if err := DB.DeleteUser(r.Context(), uid); err != nil {
				log.Printf("ERROR: DeleteUser DB delete failed: %v", err)
				w.Header().Set("HX-Trigger", `{"showToast": {"message": "Failed to delete user", "type": "error"}}`)
				w.WriteHeader(http.StatusInternalServerError)
				return
			}
			RecordAudit(r.Context(), r, "DELETE", "user", id, map[string]interface{}{
				"user_id": id,
			})
		}
	}

	w.Header().Set("HX-Trigger", `{"showToast": {"message": "User deleted successfully", "type": "success"}}`)
	if r.Header.Get("HX-Request") == "true" {
		w.WriteHeader(http.StatusOK)
		return
	}
	http.Redirect(w, r, "/admin/users", http.StatusSeeOther)
}

// CreateIntegrationHandler handles POST /api/v1/admin/integrations
func CreateIntegrationHandler(w http.ResponseWriter, r *http.Request) {
	err := r.ParseForm()
	if err != nil {
		w.Header().Set("Content-Type", "text/html")
		w.WriteHeader(http.StatusBadRequest)
		w.Write([]byte(`
			<div class="p-4 mb-4 text-sm text-red-800 rounded-lg bg-red-50 border border-red-200" role="alert">
				<span class="font-bold">Error!</span> Invalid form data provided.
			</div>
		`))
		return
	}

	name := r.FormValue("name")
	provider := r.FormValue("provider")
	baseURL := r.FormValue("base_url")
	authUser := r.FormValue("auth_user")
	authToken := r.FormValue("auth_token")

	if name == "" || provider == "" || baseURL == "" {
		w.Header().Set("Content-Type", "text/html")
		w.WriteHeader(http.StatusBadRequest)
		w.Write([]byte(`
			<div class="p-4 mb-4 text-sm text-red-800 rounded-lg bg-red-50 border border-red-200" role="alert">
				<span class="font-bold">Error!</span> Connection name, provider, and base URL are required.
			</div>
		`))
		return
	}

	// Encrypt the sensitive token before saving
	encryptedToken, err := auth.Encrypt(authToken)
	if err != nil {
		w.Header().Set("Content-Type", "text/html")
		w.WriteHeader(http.StatusInternalServerError)
		w.Write([]byte(fmt.Sprintf(`
			<div class="p-4 mb-4 text-sm text-red-800 rounded-lg bg-red-50 border border-red-200" role="alert">
				<span class="font-bold">Error!</span> Encryption failed: %v
			</div>
		`, err)))
		return
	}

	if DB != nil {
		authUserText := pgtype.Text{String: authUser, Valid: authUser != ""}
		_, err = DB.CreateIntegration(r.Context(), db.CreateIntegrationParams{
			Name:      name,
			Provider:  db.IntegrationProvider(provider),
			BaseUrl:   baseURL,
			AuthUser:  authUserText,
			AuthToken: encryptedToken,
		})
		if err != nil {
			log.Printf("ERROR: CreateIntegration DB insert failed: %v", err)
			w.Header().Set("Content-Type", "text/html")
			w.WriteHeader(http.StatusInternalServerError)
			w.Write([]byte(fmt.Sprintf(`
				<div class="p-4 mb-4 text-sm text-red-800 rounded-lg bg-red-50 border border-red-200" role="alert">
					<span class="font-bold">Database Error!</span> Could not create integration: %v
				</div>
			`, err)))
			return
		}
	}

	if r.Header.Get("HX-Request") == "true" {
		w.Header().Set("HX-Redirect", "/admin/integrations")
		w.WriteHeader(http.StatusOK)
		return
	}
	http.Redirect(w, r, "/admin/integrations", http.StatusSeeOther)
}

// DeleteIntegrationHandler handles DELETE /api/v1/admin/integrations/{id}
func DeleteIntegrationHandler(w http.ResponseWriter, r *http.Request) {
	idStr := chi.URLParam(r, "id")
	if idStr == "" {
		http.Error(w, "Integration ID is required", http.StatusBadRequest)
		return
	}

	if DB != nil {
		var uuid pgtype.UUID
		if err := uuid.Scan(idStr); err == nil {
			if err := DB.DeleteIntegration(r.Context(), uuid); err != nil {
				http.Error(w, fmt.Sprintf("Failed to delete integration: %v", err), http.StatusInternalServerError)
				return
			}
		}
	}

	if r.Header.Get("HX-Request") == "true" {
		w.WriteHeader(http.StatusOK)
		return
	}

	http.Redirect(w, r, "/admin/integrations", http.StatusSeeOther)
}

// CreateParameterHandler handles POST /api/v1/admin/parameters
func CreateParameterHandler(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		w.Header().Set("HX-Trigger", `{"showToast": {"message": "Invalid form data", "type": "error"}}`)
		w.WriteHeader(http.StatusBadRequest)
		return
	}

	category := strings.TrimSpace(strings.ToLower(r.FormValue("category")))
	code := strings.TrimSpace(strings.ToLower(r.FormValue("code")))
	name := strings.TrimSpace(r.FormValue("name"))
	desc := strings.TrimSpace(r.FormValue("description"))

	if category == "" || code == "" || name == "" {
		w.Header().Set("HX-Trigger", `{"showToast": {"message": "Category, Code, and Name are required", "type": "error"}}`)
		w.WriteHeader(http.StatusBadRequest)
		return
	}

	normCategory := category
	switch category {
	case "countries", "country":
		normCategory = "country"
	case "domains", "domain", "products", "product":
		normCategory = "domain"
	case "environments", "environment", "envs", "env":
		normCategory = "environment"
	default:
		w.Header().Set("HX-Trigger", `{"showToast": {"message": "Unknown category", "type": "error"}}`)
		w.WriteHeader(http.StatusBadRequest)
		return
	}

	catPrefix := normCategory
	if len(normCategory) >= 3 {
		catPrefix = normCategory[:3]
	}
	newID := fmt.Sprintf("%s-%d", catPrefix, time.Now().UnixNano())

	// Persist to PostgreSQL if connected
	if DB != nil {
		created, err := DB.CreateSystemParameter(r.Context(), db.CreateSystemParameterParams{
			Category:    normCategory,
			KeyName:     code,
			Value:       name,
			Description: pgtype.Text{String: desc, Valid: desc != ""},
			IsActive:    true,
		})
		if err == nil {
			newID = fmt.Sprintf("%x-%x-%x-%x-%x", created.ID.Bytes[0:4], created.ID.Bytes[4:6], created.ID.Bytes[6:8], created.ID.Bytes[8:10], created.ID.Bytes[10:16])
		} else {
			log.Printf("Warning: Failed to persist system parameter to DB: %v", err)
		}
	}

	param := SystemParam{
		ID:          newID,
		Code:        code,
		Name:        name,
		Description: desc,
		IsActive:    true,
	}

	SystemParams.mu.Lock()
	switch normCategory {
	case "country":
		SystemParams.Countries = append(SystemParams.Countries, param)
	case "domain":
		SystemParams.Domains = append(SystemParams.Domains, param)
	case "environment":
		SystemParams.Environments = append(SystemParams.Environments, param)
	}
	SystemParams.mu.Unlock()

	w.Header().Set("HX-Trigger", `{"showToast": {"message": "Parameter created successfully!", "type": "success"}}`)
	if r.Header.Get("HX-Request") == "true" {
		w.Header().Set("HX-Redirect", "/admin/parameters")
		w.WriteHeader(http.StatusOK)
		return
	}
	http.Redirect(w, r, "/admin/parameters", http.StatusSeeOther)
}

// ToggleParameterHandler handles POST /api/v1/admin/parameters/{id}/toggle
func ToggleParameterHandler(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	if id == "" {
		http.Error(w, "Parameter ID is required", http.StatusBadRequest)
		return
	}

	found := false

	// Update in PostgreSQL if connected
	if DB != nil {
		var uuid pgtype.UUID
		if err := uuid.Scan(id); err == nil {
			if param, err := DB.GetSystemParameterByID(r.Context(), uuid); err == nil {
				_, _ = DB.UpdateSystemParameter(r.Context(), db.UpdateSystemParameterParams{
					Category:    param.Category,
					KeyName:     param.KeyName,
					Value:       param.Value,
					Description: param.Description,
					IsActive:    !param.IsActive,
				})
				found = true
			}
		}
	}

	SystemParams.mu.Lock()
	for i := range SystemParams.Countries {
		if SystemParams.Countries[i].ID == id {
			SystemParams.Countries[i].IsActive = !SystemParams.Countries[i].IsActive
			found = true
			break
		}
	}
	if !found {
		for i := range SystemParams.Domains {
			if SystemParams.Domains[i].ID == id {
				SystemParams.Domains[i].IsActive = !SystemParams.Domains[i].IsActive
				found = true
				break
			}
		}
	}
	if !found {
		for i := range SystemParams.Environments {
			if SystemParams.Environments[i].ID == id {
				SystemParams.Environments[i].IsActive = !SystemParams.Environments[i].IsActive
				found = true
				break
			}
		}
	}
	SystemParams.mu.Unlock()

	if !found {
		w.Header().Set("HX-Trigger", `{"showToast": {"message": "Parameter not found", "type": "error"}}`)
		w.WriteHeader(http.StatusNotFound)
		return
	}

	w.Header().Set("HX-Trigger", `{"showToast": {"message": "Parameter status updated!", "type": "success"}}`)
	if r.Header.Get("HX-Request") == "true" {
		w.Header().Set("HX-Redirect", "/admin/parameters?toast=updated")
		w.WriteHeader(http.StatusOK)
		return
	}
	http.Redirect(w, r, "/admin/parameters", http.StatusSeeOther)
}

// DeleteParameterHandler handles DELETE /api/v1/admin/parameters/{id}
func DeleteParameterHandler(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	if id == "" {
		http.Error(w, "Parameter ID is required", http.StatusBadRequest)
		return
	}

	claims, _ := r.Context().Value(userCtxKey).(*auth.Claims)
	if claims == nil || claims.Role != "admin" {
		w.Header().Set("HX-Trigger", `{"showToast": {"message": "Forbidden: Only administrators can delete shelves", "type": "error"}}`)
		http.Error(w, "Forbidden: Only administrators can delete shelves", http.StatusForbidden)
		return
	}

	found := false

	// Delete from PostgreSQL if connected
	if DB != nil {
		var uuid pgtype.UUID
		if err := uuid.Scan(id); err == nil {
			if err := DB.DeleteSystemParameter(r.Context(), uuid); err == nil {
				found = true
			}
		}
	}

	SystemParams.mu.Lock()
	var updatedCountries []SystemParam
	for _, item := range SystemParams.Countries {
		if item.ID == id {
			found = true
		} else {
			updatedCountries = append(updatedCountries, item)
		}
	}
	SystemParams.Countries = updatedCountries

	var updatedShelves []SystemParam
	for _, item := range SystemParams.Shelves {
		if item.ID == id {
			found = true
		} else {
			updatedShelves = append(updatedShelves, item)
		}
	}
	SystemParams.Shelves = updatedShelves

	var updatedDomains []SystemParam
	for _, item := range SystemParams.Domains {
		if item.ID == id {
			found = true
		} else {
			updatedDomains = append(updatedDomains, item)
		}
	}
	SystemParams.Domains = updatedDomains

	var updatedEnvs []SystemParam
	for _, item := range SystemParams.Environments {
		if item.ID == id {
			found = true
		} else {
			updatedEnvs = append(updatedEnvs, item)
		}
	}
	SystemParams.Environments = updatedEnvs
	SystemParams.mu.Unlock()

	if !found {
		w.Header().Set("HX-Trigger", `{"showToast": {"message": "Shelf / parameter not found", "type": "error"}}`)
		w.WriteHeader(http.StatusNotFound)
		return
	}

	redirectURL := r.FormValue("redirect_url")
	if redirectURL == "" {
		redirectURL = r.Header.Get("Referer")
	}
	if redirectURL == "" {
		redirectURL = "/catalog"
	}

	w.Header().Set("HX-Trigger", `{"showToast": {"message": "Shelf deleted successfully!", "type": "success"}}`)
	if r.Header.Get("HX-Request") == "true" {
		w.Header().Set("HX-Redirect", redirectURL)
		w.WriteHeader(http.StatusOK)
		return
	}
	http.Redirect(w, r, redirectURL, http.StatusSeeOther)
}

// EditParameterHandler handles POST /api/v1/admin/parameters/{id}/edit
func EditParameterHandler(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	if id == "" {
		http.Error(w, "Parameter ID is required", http.StatusBadRequest)
		return
	}

	if err := r.ParseForm(); err != nil {
		w.Header().Set("HX-Trigger", `{"showToast": {"message": "Invalid form data", "type": "error"}}`)
		w.WriteHeader(http.StatusBadRequest)
		return
	}

	code := strings.TrimSpace(strings.ToLower(r.FormValue("code")))
	name := strings.TrimSpace(r.FormValue("name"))
	desc := strings.TrimSpace(r.FormValue("description"))

	if code == "" || name == "" {
		w.Header().Set("HX-Trigger", `{"showToast": {"message": "Code and Name are required", "type": "error"}}`)
		w.WriteHeader(http.StatusBadRequest)
		return
	}

	found := false

	// Update in PostgreSQL if connected
	if DB != nil {
		var uuid pgtype.UUID
		if err := uuid.Scan(id); err == nil {
			if param, err := DB.GetSystemParameterByID(r.Context(), uuid); err == nil {
				_, _ = DB.UpdateSystemParameter(r.Context(), db.UpdateSystemParameterParams{
					Category:    param.Category,
					KeyName:     code,
					Value:       name,
					Description: pgtype.Text{String: desc, Valid: desc != ""},
					IsActive:    param.IsActive,
				})
				found = true
			}
		}
	}

	SystemParams.mu.Lock()
	for i := range SystemParams.Countries {
		if SystemParams.Countries[i].ID == id {
			SystemParams.Countries[i].Code = code
			SystemParams.Countries[i].Name = name
			SystemParams.Countries[i].Description = desc
			found = true
			break
		}
	}
	if !found {
		for i := range SystemParams.Shelves {
			if SystemParams.Shelves[i].ID == id {
				SystemParams.Shelves[i].Code = code
				SystemParams.Shelves[i].Name = name
				SystemParams.Shelves[i].Description = desc
				found = true
				break
			}
		}
	}
	if !found {
		for i := range SystemParams.Domains {
			if SystemParams.Domains[i].ID == id {
				SystemParams.Domains[i].Code = code
				SystemParams.Domains[i].Name = name
				SystemParams.Domains[i].Description = desc
				found = true
				break
			}
		}
	}
	if !found {
		for i := range SystemParams.Environments {
			if SystemParams.Environments[i].ID == id {
				SystemParams.Environments[i].Code = code
				SystemParams.Environments[i].Name = name
				SystemParams.Environments[i].Description = desc
				found = true
				break
			}
		}
	}
	SystemParams.mu.Unlock()

	if !found {
		w.Header().Set("HX-Trigger", `{"showToast": {"message": "Shelf / parameter not found", "type": "error"}}`)
		w.WriteHeader(http.StatusNotFound)
		return
	}

	redirectURL := r.FormValue("redirect_url")
	if redirectURL == "" {
		redirectURL = r.Header.Get("Referer")
	}
	if redirectURL == "" {
		redirectURL = "/catalog"
	}

	w.Header().Set("HX-Trigger", `{"showToast": {"message": "Shelf updated successfully!", "type": "success"}}`)
	if r.Header.Get("HX-Request") == "true" {
		w.Header().Set("HX-Redirect", redirectURL)
		w.WriteHeader(http.StatusOK)
		return
	}
	http.Redirect(w, r, redirectURL, http.StatusSeeOther)
}

