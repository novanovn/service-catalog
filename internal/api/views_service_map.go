package api

import (
	"encoding/json"
	"net/http"
	"strings"

	"service-catalog/internal/auth"
)

// RenderServiceMap renders the Oona All-Accounts Network & Workload Topology Map
func RenderServiceMap(w http.ResponseWriter, r *http.Request) {
	tmpl, err := parsePage("service_map.html")
	if err != nil {
		http.Error(w, "Failed to load template: "+err.Error(), http.StatusInternalServerError)
		return
	}

	claims, _ := r.Context().Value(userCtxKey).(*auth.Claims)

	data := struct {
		Title string
		User  *auth.Claims
	}{
		Title: "Service Map",
		User:  claims,
	}

	tmpl.ExecuteTemplate(w, "base", data)
}

// LayoutItem represents metadata of a saved layout
type LayoutItem struct {
	Name      string `json:"name"`
	UpdatedBy string `json:"updated_by"`
	UpdatedAt string `json:"updated_at"`
}

// ListTopologyLayoutsHandler handles GET /api/v1/topology/layouts
func ListTopologyLayoutsHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	if DBPool == nil {
		w.Write([]byte(`[]`))
		return
	}

	rows, err := DBPool.Query(r.Context(), "SELECT layout_name, updated_by, updated_at::text FROM topology_layouts ORDER BY layout_name ASC")
	if err != nil {
		w.Write([]byte(`[]`))
		return
	}
	defer rows.Close()

	var list []LayoutItem
	for rows.Next() {
		var item LayoutItem
		if err := rows.Scan(&item.Name, &item.UpdatedBy, &item.UpdatedAt); err == nil {
			list = append(list, item)
		}
	}

	if list == nil {
		list = []LayoutItem{}
	}

	json.NewEncoder(w).Encode(list)
}

// GetTopologyLayoutHandler handles GET /api/v1/topology/layout?name=...
func GetTopologyLayoutHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	if DBPool == nil {
		w.Write([]byte(`{}`))
		return
	}

	name := r.URL.Query().Get("name")
	if name == "" {
		name = "default"
	}

	var positionsJSON []byte
	err := DBPool.QueryRow(r.Context(), "SELECT positions FROM topology_layouts WHERE layout_name = $1", name).Scan(&positionsJSON)
	if err != nil {
		// Not found or empty
		w.Write([]byte(`{}`))
		return
	}

	w.Write(positionsJSON)
}

// SaveLayoutRequest represents payload for saving a named layout
type SaveLayoutRequest struct {
	Name      string          `json:"name"`
	Positions json.RawMessage `json:"positions"`
}

// SaveTopologyLayoutHandler handles POST /api/v1/topology/layout
func SaveTopologyLayoutHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	if DBPool == nil {
		http.Error(w, `{"error":"Database not connected"}`, http.StatusInternalServerError)
		return
	}

	claims, _ := r.Context().Value(userCtxKey).(*auth.Claims)
	userEmail := "system"
	if claims != nil && claims.Email != "" {
		userEmail = claims.Email
	}

	var req SaveLayoutRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, `{"error":"Invalid JSON payload"}`, http.StatusBadRequest)
		return
	}

	layoutName := strings.TrimSpace(req.Name)
	if layoutName == "" {
		layoutName = "default"
	}

	if len(req.Positions) == 0 {
		http.Error(w, `{"error":"Positions cannot be empty"}`, http.StatusBadRequest)
		return
	}

	query := `
		INSERT INTO topology_layouts (layout_name, positions, updated_by, updated_at)
		VALUES ($1, $2, $3, NOW())
		ON CONFLICT (layout_name)
		DO UPDATE SET positions = EXCLUDED.positions, updated_by = EXCLUDED.updated_by, updated_at = NOW()
	`

	_, err := DBPool.Exec(r.Context(), query, layoutName, req.Positions, userEmail)
	if err != nil {
		http.Error(w, `{"error":"Failed to save layout: `+err.Error()+`"}`, http.StatusInternalServerError)
		return
	}

	w.WriteHeader(http.StatusOK)
	w.Write([]byte(`{"status":"success","message":"Layout '` + layoutName + `' saved successfully","name":"` + layoutName + `"}`))
}

// ResetTopologyLayoutHandler handles DELETE /api/v1/topology/layout?name=...
func ResetTopologyLayoutHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	if DBPool == nil {
		w.Write([]byte(`{"status":"success"}`))
		return
	}

	name := r.URL.Query().Get("name")
	if name == "" {
		name = "default"
	}

	_, err := DBPool.Exec(r.Context(), "DELETE FROM topology_layouts WHERE layout_name = $1", name)
	if err != nil {
		http.Error(w, `{"error":"Failed to delete layout"}`, http.StatusInternalServerError)
		return
	}

	w.WriteHeader(http.StatusOK)
	w.Write([]byte(`{"status":"success","message":"Layout '` + name + `' reset successfully"}`))
}
