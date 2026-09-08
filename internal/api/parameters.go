package api

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"
)

// SystemParam represents a system parameter entry (Country, Product/Domain, or Environment)
type SystemParam struct {
	ID          string `json:"id"`
	Code        string `json:"code"`
	Name        string `json:"name"`
	Description string `json:"description"`
	IsActive    bool   `json:"is_active"`
}

// ParameterStore is an in-memory thread-safe store for system parameters
type ParameterStore struct {
	mu           sync.RWMutex
	Countries    []SystemParam
	Domains      []SystemParam
	Environments []SystemParam
	Shelves      []SystemParam
}

// SystemParams holds the global system parameter configuration
var SystemParams = &ParameterStore{
	Countries: []SystemParam{
		{ID: "cnt-1", Code: "id", Name: "Indonesia (ID)", Description: "Oona Indonesia Entity", IsActive: true},
		{ID: "cnt-2", Code: "ph", Name: "Philippines (PH)", Description: "Oona Philippines Entity", IsActive: true},
		{ID: "cnt-3", Code: "all", Name: "Global / Shared", Description: "Regional Cross-Entity Automation", IsActive: true},
	},
	Domains: []SystemParam{
		{ID: "dom-1", Code: "integration", Name: "Integration", Description: "Core Integration Middleware", IsActive: true},
		{ID: "dom-2", Code: "dtc", Name: "DTC", Description: "Direct to Consumer Channels", IsActive: true},
		{ID: "dom-3", Code: "kahoona", Name: "Kahoona", Description: "Agent & Broker Portal Ecosystem", IsActive: true},
		{ID: "dom-4", Code: "core", Name: "Core Systems", Description: "Policy Admin & Claims Engine", IsActive: true},
		{ID: "dom-5", Code: "devops", Name: "DevOps & Shared", Description: "Automation & Infrastructure Tools", IsActive: true},
	},
	Environments: []SystemParam{
		{ID: "env-1", Code: "dev", Name: "Development (DEV)", Description: "Isolated Dev sandbox environment", IsActive: true},
		{ID: "env-2", Code: "sit", Name: "System Integration Test (SIT)", Description: "Automated SIT testing environment", IsActive: true},
		{ID: "env-3", Code: "uat", Name: "User Acceptance Test (UAT)", Description: "Staging and user validation", IsActive: true},
		{ID: "env-4", Code: "prod", Name: "Production (PROD)", Description: "Live production cluster", IsActive: true},
	},
	Shelves: []SystemParam{
		{ID: "sh-1", Code: "ph:neuron", Name: "Neuron Integration Suite", Description: "Philippines Neuron Insurance Core & Renewal Services", IsActive: true},
		{ID: "sh-2", Code: "ph:dtc", Name: "DTC 2.0 & Care Router", Description: "Philippines Direct to Consumer & Care Router Gateway", IsActive: true},
		{ID: "sh-3", Code: "ph:kahoona", Name: "Kahoona B2C Ecosystem", Description: "Philippines Kahoona Agent & Broker Quotation Portal", IsActive: true},
		{ID: "sh-4", Code: "id:coreplus", Name: "Coreplus Integration Suite", Description: "Indonesia Coreplus Database & Policy Aggregation System", IsActive: true},
		{ID: "sh-5", Code: "id:dtc", Name: "DTC 2.0 Indonesia", Description: "Indonesia Direct to Consumer Policy Platform", IsActive: true},
		{ID: "sh-6", Code: "all:devops", Name: "DevOps & Shared Automation", Description: "Multi-country Cloud Watchdogs, Replicators & DMS Lambdas", IsActive: true},
	},
}

func (ps *ParameterStore) GetActiveShelves() []SystemParam {
	if DB != nil {
		if params, err := DB.ListSystemParametersByCategory(context.Background(), "shelf"); err == nil && len(params) > 0 {
			var res []SystemParam
			for _, p := range params {
				res = append(res, SystemParam{
					ID:          fmt.Sprintf("%x-%x-%x-%x-%x", p.ID.Bytes[0:4], p.ID.Bytes[4:6], p.ID.Bytes[6:8], p.ID.Bytes[8:10], p.ID.Bytes[10:16]),
					Code:        p.KeyName,
					Name:        p.Value,
					Description: p.Description.String,
					IsActive:    p.IsActive,
				})
			}
			return res
		}
	}
	ps.mu.RLock()
	defer ps.mu.RUnlock()
	var res []SystemParam
	for _, item := range ps.Shelves {
		if item.IsActive {
			res = append(res, item)
		}
	}
	return res
}

func (ps *ParameterStore) GetActiveShelvesByCountry(country string) []SystemParam {
	allShelves := ps.GetActiveShelves()
	if country == "" || strings.EqualFold(country, "all") {
		return allShelves
	}
	countryLower := strings.ToLower(country)
	var res []SystemParam
	for _, s := range allShelves {
		parts := strings.Split(s.Code, ":")
		if len(parts) > 1 {
			if strings.EqualFold(parts[0], countryLower) || strings.EqualFold(parts[0], "all") {
				res = append(res, s)
			}
		} else {
			res = append(res, s)
		}
	}
	return res
}

func (ps *ParameterStore) GetAllShelves() []SystemParam {
	if DB != nil {
		if params, err := DB.ListSystemParameters(context.Background()); err == nil && len(params) > 0 {
			var res []SystemParam
			for _, p := range params {
				if p.Category == "shelf" {
					res = append(res, SystemParam{
						ID:          fmt.Sprintf("%x-%x-%x-%x-%x", p.ID.Bytes[0:4], p.ID.Bytes[4:6], p.ID.Bytes[6:8], p.ID.Bytes[8:10], p.ID.Bytes[10:16]),
						Code:        p.KeyName,
						Name:        p.Value,
						Description: p.Description.String,
						IsActive:    p.IsActive,
					})
				}
			}
			if len(res) > 0 {
				return res
			}
		}
	}
	ps.mu.RLock()
	defer ps.mu.RUnlock()
	res := make([]SystemParam, len(ps.Shelves))
	copy(res, ps.Shelves)
	return res
}

func (ps *ParameterStore) GetActiveCountries() []SystemParam {
	if DB != nil {
		if params, err := DB.ListSystemParametersByCategory(context.Background(), "country"); err == nil && len(params) > 0 {
			var res []SystemParam
			for _, p := range params {
				res = append(res, SystemParam{
					ID:          fmt.Sprintf("%x-%x-%x-%x-%x", p.ID.Bytes[0:4], p.ID.Bytes[4:6], p.ID.Bytes[6:8], p.ID.Bytes[8:10], p.ID.Bytes[10:16]),
					Code:        p.KeyName,
					Name:        p.Value,
					Description: p.Description.String,
					IsActive:    p.IsActive,
				})
			}
			return res
		}
	}
	ps.mu.RLock()
	defer ps.mu.RUnlock()
	var res []SystemParam
	for _, item := range ps.Countries {
		if item.IsActive {
			res = append(res, item)
		}
	}
	return res
}

func (ps *ParameterStore) GetActiveDomains() []SystemParam {
	if DB != nil {
		if params, err := DB.ListSystemParametersByCategory(context.Background(), "domain"); err == nil && len(params) > 0 {
			var res []SystemParam
			for _, p := range params {
				res = append(res, SystemParam{
					ID:          fmt.Sprintf("%x-%x-%x-%x-%x", p.ID.Bytes[0:4], p.ID.Bytes[4:6], p.ID.Bytes[6:8], p.ID.Bytes[8:10], p.ID.Bytes[10:16]),
					Code:        p.KeyName,
					Name:        p.Value,
					Description: p.Description.String,
					IsActive:    p.IsActive,
				})
			}
			return res
		}
	}
	ps.mu.RLock()
	defer ps.mu.RUnlock()
	var res []SystemParam
	for _, item := range ps.Domains {
		if item.IsActive {
			res = append(res, item)
		}
	}
	return res
}

func (ps *ParameterStore) GetActiveEnvironments() []SystemParam {
	if DB != nil {
		if params, err := DB.ListSystemParametersByCategory(context.Background(), "environment"); err == nil && len(params) > 0 {
			var res []SystemParam
			for _, p := range params {
				res = append(res, SystemParam{
					ID:          fmt.Sprintf("%x-%x-%x-%x-%x", p.ID.Bytes[0:4], p.ID.Bytes[4:6], p.ID.Bytes[6:8], p.ID.Bytes[8:10], p.ID.Bytes[10:16]),
					Code:        p.KeyName,
					Name:        p.Value,
					Description: p.Description.String,
					IsActive:    p.IsActive,
				})
			}
			return res
		}
	}
	ps.mu.RLock()
	defer ps.mu.RUnlock()
	var res []SystemParam
	for _, item := range ps.Environments {
		if item.IsActive {
			res = append(res, item)
		}
	}
	return res
}

func (ps *ParameterStore) GetAllCountries() []SystemParam {
	if DB != nil {
		if params, err := DB.ListSystemParameters(context.Background()); err == nil && len(params) > 0 {
			var res []SystemParam
			for _, p := range params {
				if p.Category == "country" {
					res = append(res, SystemParam{
						ID:          fmt.Sprintf("%x-%x-%x-%x-%x", p.ID.Bytes[0:4], p.ID.Bytes[4:6], p.ID.Bytes[6:8], p.ID.Bytes[8:10], p.ID.Bytes[10:16]),
						Code:        p.KeyName,
						Name:        p.Value,
						Description: p.Description.String,
						IsActive:    p.IsActive,
					})
				}
			}
			if len(res) > 0 {
				return res
			}
		}
	}
	ps.mu.RLock()
	defer ps.mu.RUnlock()
	res := make([]SystemParam, len(ps.Countries))
	copy(res, ps.Countries)
	return res
}

func (ps *ParameterStore) GetAllDomains() []SystemParam {
	if DB != nil {
		if params, err := DB.ListSystemParameters(context.Background()); err == nil && len(params) > 0 {
			var res []SystemParam
			for _, p := range params {
				if p.Category == "domain" {
					res = append(res, SystemParam{
						ID:          fmt.Sprintf("%x-%x-%x-%x-%x", p.ID.Bytes[0:4], p.ID.Bytes[4:6], p.ID.Bytes[6:8], p.ID.Bytes[8:10], p.ID.Bytes[10:16]),
						Code:        p.KeyName,
						Name:        p.Value,
						Description: p.Description.String,
						IsActive:    p.IsActive,
					})
				}
			}
			if len(res) > 0 {
				return res
			}
		}
	}
	ps.mu.RLock()
	defer ps.mu.RUnlock()
	res := make([]SystemParam, len(ps.Domains))
	copy(res, ps.Domains)
	return res
}

func (ps *ParameterStore) GetAllEnvironments() []SystemParam {
	if DB != nil {
		if params, err := DB.ListSystemParameters(context.Background()); err == nil && len(params) > 0 {
			var res []SystemParam
			for _, p := range params {
				if p.Category == "environment" {
					res = append(res, SystemParam{
						ID:          fmt.Sprintf("%x-%x-%x-%x-%x", p.ID.Bytes[0:4], p.ID.Bytes[4:6], p.ID.Bytes[6:8], p.ID.Bytes[8:10], p.ID.Bytes[10:16]),
						Code:        p.KeyName,
						Name:        p.Value,
						Description: p.Description.String,
						IsActive:    p.IsActive,
					})
				}
			}
			if len(res) > 0 {
				return res
			}
		}
	}
	ps.mu.RLock()
	defer ps.mu.RUnlock()
	res := make([]SystemParam, len(ps.Environments))
	copy(res, ps.Environments)
	return res
}



// AddParameterHandler handles adding a new system parameter (Countries, Domains, Environments)
func AddParameterHandler(w http.ResponseWriter, r *http.Request) {
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

	newID := fmt.Sprintf("%s-%d", category[:3], time.Now().UnixNano())
	param := SystemParam{
		ID:          newID,
		Code:        code,
		Name:        name,
		Description: desc,
		IsActive:    true,
	}

	SystemParams.mu.Lock()
	switch category {
	case "countries", "country":
		SystemParams.Countries = append(SystemParams.Countries, param)
	case "domains", "domain", "products":
		SystemParams.Domains = append(SystemParams.Domains, param)
	case "environments", "environment", "envs":
		SystemParams.Environments = append(SystemParams.Environments, param)
	case "shelves", "shelf":
		SystemParams.Shelves = append(SystemParams.Shelves, param)
	default:
		SystemParams.mu.Unlock()
		w.Header().Set("HX-Trigger", `{"showToast": {"message": "Unknown category", "type": "error"}}`)
		w.WriteHeader(http.StatusBadRequest)
		return
	}
	SystemParams.mu.Unlock()

	w.Header().Set("HX-Trigger", `{"showToast": {"message": "Parameter added successfully!", "type": "success"}}`)
	if r.Header.Get("HX-Request") == "true" {
		w.Header().Set("HX-Redirect", "/admin/parameters")
		w.WriteHeader(http.StatusOK)
		return
	}
	http.Redirect(w, r, "/admin/parameters", http.StatusSeeOther)
}


