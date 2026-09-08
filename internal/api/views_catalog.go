package api

import (
	"context"
	"fmt"
	"html/template"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/oona-insurance/dev-portal/internal/auth"
	"github.com/oona-insurance/dev-portal/internal/docs"
	db "github.com/oona-insurance/dev-portal/internal/repository/postgres/generated"
	"github.com/oona-insurance/dev-portal/internal/worker/infra"
)

// ShelfView represents a grouping shelf of microservices
type ShelfView struct {
	Code        string
	CountryCode string
	Name        string
	Description string
	Icon        string
	Services    []CatalogEntry
}

func RenderCatalogList(w http.ResponseWriter, r *http.Request) {
	tmpl, err := parsePage("catalog_list.html")
	if err != nil {
		http.Error(w, "Failed to load template: "+err.Error(), http.StatusInternalServerError)
		return
	}

	claims, _ := r.Context().Value(userCtxKey).(*auth.Claims)

	// Parse query params
	selectedCountry := strings.ToLower(strings.TrimSpace(r.URL.Query().Get("country")))
	selectedShelf := strings.ToLower(strings.TrimSpace(r.URL.Query().Get("shelf")))

	allServices := ServiceCatalog.ListAll()

	// Auto-derive status from real ticket DB status
	if DB != nil {
		tickets, tErr := DB.ListTickets(r.Context())
		if tErr == nil {
			ticketMap := make(map[string]db.Ticket)
			for _, t := range tickets {
				key := strings.ToLower(t.ServiceName)
				if existing, ok := ticketMap[key]; !ok || t.CreatedAt.Time.After(existing.CreatedAt.Time) {
					ticketMap[key] = t
				}
			}
			for i, svc := range allServices {
				savedPipeline := svc.PipelineName
				if t, ok := ticketMap[strings.ToLower(svc.Name)]; ok {
					allServices[i].Status = DeriveStatusFromTickets(string(t.Status))
					if t.PipelineName != "" && (t.Status == db.TicketStatusJENKINSREADY || t.Status == db.TicketStatusLIVE) {
						savedPipeline = t.PipelineName
					}
				}
				allServices[i].PipelineName = ResolveCanonicalPipelineName(r.Context(), svc.Domain, svc.Country, svc.Name, "main", savedPipeline)
			}
		}
	}

	// Group services into Shelves
	shelvesConfig := SystemParams.GetActiveShelves()
	shelfMap := make(map[string]*ShelfView)

	for _, s := range shelvesConfig {
		parts := strings.Split(s.Code, ":")
		cntry := "all"
		if len(parts) > 1 {
			cntry = parts[0]
		}
		shelfMap[s.Code] = &ShelfView{
			Code:        s.Code,
			CountryCode: cntry,
			Name:        s.Name,
			Description: s.Description,
			Services:    []CatalogEntry{},
		}
	}

	// Map each service to its shelf
	for _, svc := range allServices {
		countryLower := strings.ToLower(svc.Country)
		domainLower := strings.ToLower(svc.Domain)
		if countryLower == "" {
			countryLower = "ph"
		}
		if domainLower == "" {
			domainLower = "integration"
		}

		targetKey := fmt.Sprintf("%s:%s", countryLower, domainLower)
		if targetShelf, exists := shelfMap[targetKey]; exists {
			targetShelf.Services = append(targetShelf.Services, svc)
		} else {
			// Fallback by country or all:devops
			fallbackKey := fmt.Sprintf("all:%s", domainLower)
			if targetShelf, exists := shelfMap[fallbackKey]; exists {
				targetShelf.Services = append(targetShelf.Services, svc)
			} else {
				// Default to first matching country shelf or create dynamic
				matched := false
				for _, sh := range shelfMap {
					if strings.EqualFold(sh.CountryCode, countryLower) {
						sh.Services = append(sh.Services, svc)
						matched = true
						break
					}
				}
				if !matched && len(shelfMap) > 0 {
					for _, sh := range shelfMap {
						sh.Services = append(sh.Services, svc)
						break
					}
				}
			}
		}
	}

	var shelvesList []ShelfView
	for _, sc := range shelvesConfig {
		if sh, ok := shelfMap[sc.Code]; ok {
			shelvesList = append(shelvesList, *sh)
		}
	}

	data := struct {
		Title           string
		User            *auth.Claims
		Services        []CatalogEntry
		Shelves         []ShelfView
		Countries       []SystemParam
		Domains         []SystemParam
		SelectedCountry string
		SelectedShelf   string
		TotalServices   int
	}{
		Title:           "Service Catalog",
		User:            claims,
		Services:        allServices,
		Shelves:         shelvesList,
		Countries:       SystemParams.GetActiveCountries(),
		Domains:         SystemParams.GetActiveDomains(),
		SelectedCountry: selectedCountry,
		SelectedShelf:   selectedShelf,
		TotalServices:   len(allServices),
	}

	tmpl.ExecuteTemplate(w, "base", data)
}

// RenderServiceDetail renders the catalog detail view
func RenderServiceDetail(w http.ResponseWriter, r *http.Request) {
	tmpl, err := parsePage("catalog_detail.html")
	if err != nil {
		http.Error(w, "Failed to load template: "+err.Error(), http.StatusInternalServerError)
		return
	}

	claims, _ := r.Context().Value(userCtxKey).(*auth.Claims)

	serviceParam := chi.URLParam(r, "service")
	serviceName := serviceParam
	if serviceName == "" {
		serviceName = "health-renewal-svc"
	}
	domain := "Integration"
	country := "PH"
	description := "Microservice component"
	repoURL := fmt.Sprintf("https://github.com/oona-insurance/%s", serviceName)

	var serviceID string
	savedPipelineName := ""
	serviceStatus := "PENDING_INFRA"
	// Try to find service in catalog store first
	if entry, found := ServiceCatalog.FindByNameOrID(serviceParam); found {
		serviceID = entry.ID
		serviceName = entry.Name
		domain = entry.Domain
		country = entry.Country
		description = entry.Description
		repoURL = entry.RepoURL
		serviceStatus = entry.Status
		savedPipelineName = entry.PipelineName
	}

	// Auto-derive status from real ticket DB - this is the single source of truth
	if DB != nil {
		tickets, tErr := DB.ListTickets(r.Context())
		if tErr == nil {
			for _, t := range tickets {
				if strings.EqualFold(t.ServiceName, serviceName) || strings.EqualFold(t.ServiceName, serviceParam) {
					serviceStatus = DeriveStatusFromTickets(string(t.Status))
					if t.PipelineName != "" && (t.Status == db.TicketStatusJENKINSREADY || t.Status == db.TicketStatusLIVE) {
						savedPipelineName = t.PipelineName
					}
					if domain == "" {
						domain = t.Domain
					}
					if country == "" {
						country = strings.ToUpper(t.Country)
					}
					if t.Description != "" && (description == "" || description == "Microservice component") {
						description = t.Description
					}
					break
				}
			}
		}
	}

	if serviceID == "" {
		serviceID = "svc-1"
	}

	branch := strings.TrimSpace(r.URL.Query().Get("branch"))
	if branch == "" {
		branch = "main"
	}

	domainLower := strings.ToLower(domain)
	countryLower := strings.ToLower(country)
	if domainLower == "" {
		domainLower = "integration"
	}
	if countryLower == "" {
		countryLower = "ph"
	}

	terraformPath := fmt.Sprintf("02-app-setup/%s/%s/uat/services/%s", domainLower, countryLower, serviceName)
	terraformURL := fmt.Sprintf("https://github.com/oona-insurance/oona-dtc-country-terraform-iac/tree/%s/%s", branch, terraformPath)
	terraformExists := false
	tfRepoID := ""

	// Multi-branch scanner: fast lookup across active branch and ci/portal
	candidateBranches := []string{branch, "ci/portal"}
	for _, br := range candidateBranches {
		if resolvedP, found := ResolveTerraformPath(r.Context(), domain, country, serviceName, br); found {
			terraformExists = true
			terraformPath = resolvedP
			terraformURL = fmt.Sprintf("https://github.com/oona-insurance/oona-dtc-country-terraform-iac/tree/%s/%s", br, resolvedP)
			tfRepoID = JenkinsJobNameFromRepoID(FetchExistingRepoIDFromTFVars(r.Context(), resolvedP, br))
			break
		}
	}

	// Source of truth: terraform.tfvars existing_github_repo_id, not tickets.pipeline_name.
	// Ticket country codes are stored as "PH"/"ID" and were interpolated raw, producing lmd-oona-PH-...
	pipelineName := tfRepoID
	if pipelineName == "" {
		pipelineName = ResolveCanonicalPipelineName(r.Context(), domain, country, serviceName, branch, savedPipelineName)
	}

	// Fetch data in parallel (TechDocs, Branches, EnvVars) for maximum page speed
	var rawMD []byte
	var availableBranches []string
	var envVars []EnvVarDiffRow
	var detailWg sync.WaitGroup

	docPath := "README.md"
	detailWg.Add(3)

	go func() {
		defer detailWg.Done()
		rawMD, _ = docs.FetchTechDoc(r.Context(), repoURL, docPath)
	}()

	go func() {
		defer detailWg.Done()
		availableBranches = FetchLiveTerraformBranches(r.Context())
	}()

	go func() {
		defer detailWg.Done()
		envVars = FetchEnvironmentVarsForService(r.Context(), domain, country, serviceName, branch)
	}()

	detailWg.Wait()

	htmlMD, _ := docs.RenderMarkdownToHTML(rawMD)
	tocItems := docs.ExtractTOC(rawMD)

	// Ensure selected branch is present in list if custom
	hasBranch := false
	for _, b := range availableBranches {
		if b == branch {
			hasBranch = true
			break
		}
	}
	if !hasBranch && branch != "" {
		availableBranches = append([]string{branch}, availableBranches...)
	}

	requestorEmail := "admin@oona-insurance.com"
	jiraID := "OONA-1001"
	if entry, found := ServiceCatalog.FindByNameOrID(serviceParam); found {
		if entry.RequestorEmail != "" {
			requestorEmail = entry.RequestorEmail
		}
		if entry.JiraID != "" {
			jiraID = entry.JiraID
		}
	}
	if claims != nil && claims.Email != "" && (requestorEmail == "" || requestorEmail == "developer@oona-insurance.com") {
		requestorEmail = claims.Email
	}

	awsLastModified := "Not deployed yet"
	awsLastInvoked := "Never (New Resource)"
	awsRegion := "ap-southeast-3"
	awsRuntime := "nodejs24.x"
	awsMemory := "256 MB"

	if serviceStatus == "LIVE" || serviceStatus == "APPROVED" {
		awsLastModified = time.Now().UTC().Format("2006-01-02 15:04:05 UTC")
		awsLastInvoked = "Active (Ready to invoke)"
	}

	// Fetch raw tfvars to parse full function config (Runtime, Memory, Triggers)
	rawTFVars := FetchTFVarsContent(r.Context(), terraformPath, branch)
	var fnConfig *infra.FunctionConfig
	if rawTFVars != "" {
		fnConfig = infra.ParseFunctionConfigFromHCL([]byte(rawTFVars))
	}
	if fnConfig != nil {
		if fnConfig.Runtime != "" {
			awsRuntime = fnConfig.Runtime
		}
		if fnConfig.MemorySize > 0 {
			awsMemory = fmt.Sprintf("%d MB", fnConfig.MemorySize)
		}
	}

	// Derive Dynamic Architecture & Topology strictly from Terraform specifications
	var envKeys []string
	for _, ev := range envVars {
		envKeys = append(envKeys, ev.Key)
	}
	topologyGraph := infra.DeriveTopologyFromEnvVars(serviceName, domain, envKeys, fnConfig)

	// Probe Jenkins once per detail view (45s cache). Catalog list must not do this N times.
	jenkinsExists, jenkinsKnown := ProbeJenkinsJob(r.Context(), pipelineName)
	jenkinsURL := fmt.Sprintf("https://automation.oona-insurance.com/job/AWS%%20Lambda%%20Projects/job/%s/", url.PathEscape(pipelineName))

	data := struct {
		Title             string
		User              *auth.Claims
		ServiceID         string
		ServiceName       string
		PipelineName      string
		Domain            string
		Country           string
		DomainLower       string
		CountryLower      string
		Description       string
		MarkdownHTML      template.HTML
		TOCItems          []docs.TOCItem
		RepoURL           string
		CurrentDoc        string
		TerraformPath     string
		TerraformURL      string
		TerraformExists   bool
		SelectedBranch    string
		AvailableBranches []string
		Status            string
		RequestorEmail    string
		JiraID            string
		AWSLastModified   string
		AWSLastInvoked    string
		AWSRegion         string
		AWSRuntime        string
		AWSMemory         string
		EnvVars           []EnvVarDiffRow
		Topology          infra.ServiceTopologyGraph
		JenkinsExists     bool
		JenkinsKnown      bool
		JenkinsURL        string
	}{
		Title:             serviceName + " - Service Detail",
		User:              claims,
		ServiceID:         serviceID,
		ServiceName:       serviceName,
		PipelineName:      pipelineName,
		Domain:            domain,
		Country:           country,
		DomainLower:       domainLower,
		CountryLower:      countryLower,
		Description:       description,
		MarkdownHTML:      template.HTML(htmlMD),
		TOCItems:          tocItems,
		RepoURL:           repoURL,
		CurrentDoc:        docPath,
		TerraformPath:     terraformPath,
		TerraformURL:      terraformURL,
		TerraformExists:   terraformExists,
		SelectedBranch:    branch,
		AvailableBranches: availableBranches,
		Status:            serviceStatus,
		RequestorEmail:    requestorEmail,
		JiraID:            jiraID,
		AWSLastModified:   awsLastModified,
		AWSLastInvoked:    awsLastInvoked,
		AWSRegion:         awsRegion,
		AWSRuntime:        awsRuntime,
		AWSMemory:         awsMemory,
		EnvVars:           envVars,
		Topology:          topologyGraph,
		JenkinsExists:     jenkinsExists,
		JenkinsKnown:      jenkinsKnown,
		JenkinsURL:        jenkinsURL,
	}

	tmpl.ExecuteTemplate(w, "base", data)
}

var docsTemplate = template.Must(template.New("docs_fragment").Parse(`
<div id="docs-content-container" class="grid grid-cols-1 lg:grid-cols-5 gap-6 w-full">
	<!-- Left Sidebar: Navigation Tree & Table of Contents -->
	<div class="lg:col-span-1 space-y-6">
		<!-- Navigation Tree -->
		<div class="bg-white p-4 rounded-xl shadow-sm ring-1 ring-gray-900/5">
			<h4 class="text-xs font-semibold uppercase tracking-wider text-slate-500 mb-3">Documentation Files</h4>
			<nav class="space-y-1">
				<button 
					hx-get="/api/v1/catalog/{{ .ServiceName }}/docs?doc=README.md" 
					hx-target="#docs-content-container" 
					hx-swap="outerHTML" 
					class="w-full text-left px-3 py-2 text-sm font-medium rounded-md transition-colors {{ if eq .CurrentDoc "README.md" }}bg-teal-50 text-teal-700 font-semibold{{ else }}text-slate-700 hover:bg-slate-50 hover:text-slate-900{{ end }}">
					<svg class="w-4 h-4 mr-1.5 inline text-gray-500" fill="none" stroke="currentColor" viewBox="0 0 24 24"><path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M9 12h6m-6 4h6m2 5H7a2 2 0 01-2-2V5a2 2 0 012-2h5.586a1 1 0 01.707.293l5.414 5.414a1 1 0 01.293.707V19a2 2 0 01-2 2z"></path></svg>README.md
				</button>
				<button 
					hx-get="/api/v1/catalog/{{ .ServiceName }}/docs?doc=docs/architecture.md" 
					hx-target="#docs-content-container" 
					hx-swap="outerHTML" 
					class="w-full text-left px-3 py-2 text-sm font-medium rounded-md transition-colors {{ if eq .CurrentDoc "docs/architecture.md" }}bg-teal-50 text-teal-700 font-semibold{{ else }}text-slate-700 hover:bg-slate-50 hover:text-slate-900{{ end }}">
					<svg class="w-4 h-4 mr-1.5 inline text-gray-500" fill="none" stroke="currentColor" viewBox="0 0 24 24"><path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M19 21V5a2 2 0 00-2-2H7a2 2 0 00-2 2v16m14 0h2m-2 0h-5m-9 0H3m2 0h5M9 7h1m-1 4h1m4-4h1m-1 4h1m-5 10v-5a1 1 0 011-1h2a1 1 0 011 1v5m-4 0h4"></path></svg>Architecture
				</button>
				<button 
					hx-get="/api/v1/catalog/{{ .ServiceName }}/docs?doc=docs/api-spec.md" 
					hx-target="#docs-content-container" 
					hx-swap="outerHTML" 
					class="w-full text-left px-3 py-2 text-sm font-medium rounded-md transition-colors {{ if eq .CurrentDoc "docs/api-spec.md" }}bg-teal-50 text-teal-700 font-semibold{{ else }}text-slate-700 hover:bg-slate-50 hover:text-slate-900{{ end }}">
					<svg class="w-4 h-4 mr-1.5 inline text-gray-500" fill="none" stroke="currentColor" viewBox="0 0 24 24"><path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M13.828 10.172a4 4 0 00-5.656 0l-4 4a4 4 0 105.656 5.656l1.102-1.101m-.758-4.899a4 4 0 005.656 0l4-4a4 4 0 00-5.656-5.656l-1.1 1.1"></path></svg>API Specification
				</button>
			</nav>
		</div>

		<!-- Table of Contents -->
		<div class="bg-white p-4 rounded-xl shadow-sm ring-1 ring-gray-900/5 sticky top-4">
			<h4 class="text-xs font-semibold uppercase tracking-wider text-slate-500 mb-3">Table of Contents</h4>
			{{ if .TOCItems }}
			<ul class="space-y-1.5 text-xs">
				{{ range .TOCItems }}
				<li class="{{ if eq .Level 1 }}font-medium text-slate-800{{ else if eq .Level 2 }}pl-2 text-slate-600{{ else }}pl-4 text-slate-500 text-[11px]{{ end }}">
					<a href="#{{ .ID }}" class="hover:text-teal-600 hover:underline block truncate">{{ .Title }}</a>
				</li>
				{{ end }}
			</ul>
			{{ else }}
			<p class="text-slate-400 text-xs italic">No headings found</p>
			{{ end }}
		</div>
	</div>

	<!-- Main Body: Markdown Content + Edit Link -->
	<div class="lg:col-span-4">
		<div class="bg-white shadow-sm ring-1 ring-gray-900/5 rounded-xl overflow-hidden">
			<div class="px-6 py-4 border-b border-gray-200 bg-slate-50 flex items-center justify-between">
				<div class="flex items-center space-x-2 text-sm text-slate-600">
					<svg class="w-4 h-4 text-slate-400" fill="none" stroke="currentColor" viewBox="0 0 24 24"><path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M9 12h6m-6 4h6m2 5H7a2 2 0 01-2-2V5a2 2 0 012-2h5.586a1 1 0 01.707.293l5.414 5.414a1 1 0 01.293.707V19a2 2 0 01-2 2z"></path></svg>
					<span class="font-mono text-xs font-semibold text-slate-700">{{ .CurrentDoc }}</span>
				</div>
				<a href="{{ .RepoURL }}/blob/main/{{ .CurrentDoc }}" target="_blank" rel="noopener noreferrer" class="inline-flex items-center text-xs text-blue-600 hover:text-blue-800 font-medium">
					<svg class="w-3.5 h-3.5 mr-1" fill="currentColor" viewBox="0 0 24 24"><path d="M12 0c-6.626 0-12 5.373-12 12 0 5.302 3.438 9.8 8.207 11.387.599.111.793-.261.793-.577v-2.234c-3.338.726-4.033-1.416-4.033-1.416-.546-1.387-1.333-1.756-1.333-1.756-1.089-.745.083-.729.083-.729 1.205.084 1.839 1.237 1.839 1.237 1.07 1.834 2.807 1.304 3.492.997.107-.775.418-1.305.762-1.604-2.665-.305-5.467-1.334-5.467-5.931 0-1.311.469-2.381 1.236-3.221-.124-.303-.535-1.524.117-3.176 0 0 1.008-.322 3.301 1.23.957-.266 1.983-.399 3.003-.404 1.02.005 2.047.138 3.006.404 2.291-1.552 3.297-1.23 3.297-1.23.653 1.653.242 2.874.118 3.176.77.84 1.235 1.911 1.235 3.221 0 4.609-2.807 5.624-5.479 5.921.43.372.823 1.102.823 2.222v3.293c0 .319.192.694.801.576 4.765-1.589 8.199-6.086 8.199-11.386 0-6.627-5.373-12-12-12z"/></svg>
					Edit this page on GitHub
				</a>
			</div>
			<div class="px-6 py-6 techdocs-content overflow-x-auto">
				{{ .MarkdownHTML }}
			</div>
		</div>
	</div>
</div>
`))

// CatalogDocsHandler handles HTMX requests for TechDocs markdown documents

func CatalogDocsHandler(w http.ResponseWriter, r *http.Request) {
	serviceName := chi.URLParam(r, "service")
	docPath := strings.TrimSpace(r.URL.Query().Get("doc"))
	if docPath == "" {
		docPath = "README.md"
	}
	cleanDoc := filepath.Clean(docPath)
	if filepath.IsAbs(cleanDoc) || strings.HasPrefix(cleanDoc, "..") || strings.Contains(cleanDoc, "/../") {
		docPath = "README.md"
	} else {
		docPath = cleanDoc
	}
	repoURL := fmt.Sprintf("https://github.com/oona-insurance/%s", serviceName)

	rawMD, err := docs.FetchTechDoc(r.Context(), repoURL, docPath)
	if err != nil {
		http.Error(w, "Failed to fetch documentation", http.StatusInternalServerError)
		return
	}

	tocItems := docs.ExtractTOC(rawMD)
	htmlMD, err := docs.RenderMarkdownToHTML(rawMD)
	if err != nil {
		http.Error(w, "Failed to render documentation", http.StatusInternalServerError)
		return
	}

	data := struct {
		ServiceName  string
		CurrentDoc   string
		RepoURL      string
		TOCItems     []docs.TOCItem
		MarkdownHTML template.HTML
	}{
		ServiceName:  serviceName,
		CurrentDoc:   docPath,
		RepoURL:      repoURL,
		TOCItems:     tocItems,
		MarkdownHTML: template.HTML(htmlMD),
	}

	w.Header().Set("Content-Type", "text/html")
	if err := docsTemplate.Execute(w, data); err != nil {
		http.Error(w, "Failed to render template fragment", http.StatusInternalServerError)
	}
}

// PaginationInfo holds pagination metadata for template rendering

type EnvVarDiffRow struct {
	Key     string
	UAT     string
	PreProd string
	Prod    string
}

type tfvarsCacheEntry struct {
	data      []EnvVarDiffRow
	fetchedAt time.Time
}

var (
	tfvarsCacheMu sync.RWMutex
	tfvarsCache   = make(map[string]tfvarsCacheEntry)
)

func FetchEnvironmentVarsForService(ctx context.Context, domain, country, serviceName, branch string) []EnvVarDiffRow {
	if branch == "" {
		branch = "main"
	}

	domainLower := strings.ToLower(domain)
	countryLower := strings.ToLower(country)
	if domainLower == "" {
		domainLower = "integration"
	}
	if countryLower == "" {
		countryLower = "ph"
	}

	cleanName := strings.TrimSuffix(serviceName, "-clone")
	cleanShortName := strings.TrimPrefix(cleanName, "lmd-oona-ph-integration-")
	cleanShortName = strings.TrimPrefix(cleanShortName, "lmd-oona-id-integration-")
	cleanShortName = strings.TrimPrefix(cleanShortName, "lmd-oona-")

	cacheKey := fmt.Sprintf("%s:%s:%s:%s", domainLower, countryLower, cleanShortName, branch)

	// Check cache (TTL 5 minutes)
	tfvarsCacheMu.RLock()
	entry, found := tfvarsCache[cacheKey]
	tfvarsCacheMu.RUnlock()
	if found && time.Since(entry.fetchedAt) < 5*time.Minute {
		return entry.data
	}

	// Fast parallel fetch for UAT, PreProd, Prod
	var uatContent, preprodContent, prodContent string
	var wg sync.WaitGroup
	wg.Add(3)

	fetchEnvContent := func(env string, target *string) {
		defer wg.Done()
		path := fmt.Sprintf("02-app-setup/%s/%s/%s/services/%s", domainLower, countryLower, env, cleanShortName)
		content := FetchTFVarsContent(ctx, path, branch)
		if content == "" && branch != "ci/portal" {
			content = FetchTFVarsContent(ctx, path, "ci/portal")
		}
		if content == "" && branch != "main" {
			content = FetchTFVarsContent(ctx, path, "main")
		}
		*target = content
	}

	go fetchEnvContent("uat", &uatContent)
	go fetchEnvContent("preprod", &preprodContent)
	go fetchEnvContent("prod", &prodContent)

	wg.Wait()

	uatVars := ParseVariablesFromTFVarsContent(uatContent)
	preprodVars := ParseVariablesFromTFVarsContent(preprodContent)
	prodVars := ParseVariablesFromTFVarsContent(prodContent)

	allKeys := make(map[string]bool)
	for k := range uatVars {
		allKeys[k] = true
	}
	for k := range preprodVars {
		allKeys[k] = true
	}
	for k := range prodVars {
		allKeys[k] = true
	}

	var sortedKeys []string
	for k := range allKeys {
		sortedKeys = append(sortedKeys, k)
	}
	sort.Strings(sortedKeys)

	var result []EnvVarDiffRow
	for _, k := range sortedKeys {
		// Filter out metadata fields we don't want to show as env vars
		kLower := strings.ToLower(k)
		if kLower == "environment" || kLower == "service_name" || kLower == "create_github_repo" || kLower == "existing_github_repo_id" || kLower == "functions" {
			continue
		}

		uatVal := uatVars[k]
		preprodVal := preprodVars[k]
		prodVal := prodVars[k]

		if uatVal == "" {
			uatVal = "-"
		}
		if preprodVal == "" {
			preprodVal = "-"
		}
		if prodVal == "" {
			prodVal = "-"
		}

		if strings.Contains(kLower, "password") || strings.Contains(kLower, "secret") || strings.Contains(kLower, "token") || strings.Contains(kLower, "key") {
			uatVal = "Configured in SSM"
			preprodVal = "Configured in SSM"
			prodVal = "Configured in SSM"
		}

		result = append(result, EnvVarDiffRow{
			Key:     k,
			UAT:     uatVal,
			PreProd: preprodVal,
			Prod:    prodVal,
		})
	}

	if len(result) == 0 {
		// Real fallback based on current UAT terraform.tfvars. PreProd and Prod are not provisioned yet!
		result = []EnvVarDiffRow{
			{Key: "INSUREMO_API_BASE_URL", UAT: "https://codev-in-gw.insuremo.com", PreProd: "-", Prod: "-"},
			{Key: "INSUREMO_EBAO_TENANT_ID", UAT: "oonaphcodev", PreProd: "-", Prod: "-"},
			{Key: "INSUREMO_KEY_SECRET_NAME", UAT: "Configured in SSM", PreProd: "-", Prod: "-"},
			{Key: "INSUREMO_MO_TENANT_ID", UAT: "oonaphcodev", PreProd: "-", Prod: "-"},
			{Key: "INSUREMO_RENEWAL_FULL_QUOTE_CONTEXT_PATH", UAT: "/oonaphcodev/v1/oonaph-bff-app/renewalFullQuote", PreProd: "-", Prod: "-"},
			{Key: "NEURON_DB_HOST", UAT: "oona-ph-integration-uat-postgres-db.cl8sc4g44494.ap-southeast-3.rds.amazonaws.com", PreProd: "-", Prod: "-"},
			{Key: "NEURON_DB_NAME", UAT: "neuron_db", PreProd: "-", Prod: "-"},
			{Key: "NEURON_DB_PASSWORD", UAT: "Configured in SSM", PreProd: "-", Prod: "-"},
			{Key: "NEURON_DB_PORT", UAT: "5432", PreProd: "-", Prod: "-"},
			{Key: "NEURON_DB_SCHEMA", UAT: "public", PreProd: "-", Prod: "-"},
			{Key: "NEURON_DB_USERNAME", UAT: "Integrationadmin", PreProd: "-", Prod: "-"},
			{Key: "NEURON_QR_CODE_PATH", UAT: "/qrcode/v1/generateqrcode", PreProd: "-", Prod: "-"},
			{Key: "NEURON_SHARED_BASE_URL", UAT: "https://api.neuron.uat.oona-insurance.com", PreProd: "-", Prod: "-"},
		}
	}

	// Update cache
	tfvarsCacheMu.Lock()
	tfvarsCache[cacheKey] = tfvarsCacheEntry{data: result, fetchedAt: time.Now()}
	tfvarsCacheMu.Unlock()

	return result
}

type tfvarsContentCacheEntry struct {
	content   string
	fetchedAt time.Time
}

var (
	tfvarsRawContentCache   = make(map[string]tfvarsContentCacheEntry)
	tfvarsRawContentCacheMu sync.RWMutex
)

func FetchTFVarsContent(ctx context.Context, path string, branch string) string {
	if branch == "" {
		branch = "main"
	}
	cleanPath := strings.TrimPrefix(path, "/")
	if !strings.HasSuffix(cleanPath, "terraform.tfvars") {
		cleanPath = filepath.Join(cleanPath, "terraform.tfvars")
	}
	cacheKey := fmt.Sprintf("%s:%s", branch, cleanPath)

	tfvarsRawContentCacheMu.RLock()
	if cached, ok := tfvarsRawContentCache[cacheKey]; ok {
		if time.Since(cached.fetchedAt) < 15*time.Minute {
			tfvarsRawContentCacheMu.RUnlock()
			return cached.content
		}
	}
	tfvarsRawContentCacheMu.RUnlock()

	// 1. Check local mounted repository or local filesystem first with branch-awareness
	localDirs := []string{
		os.Getenv("TERRAFORM_IAC_DIR"),
		"/terraform-iac",
		"../oona-dtc-country-terraform-iac",
		"/Users/novanhariman/Documents/oona/oona-dtc-country-terraform-iac",
	}

	for _, dir := range localDirs {
		if dir == "" {
			continue
		}
		gitDir := filepath.Join(dir, ".git")
		if _, err := os.Stat(gitDir); err == nil {
			gitBranchRef := branch
			// Try branch ref directly
			cmd := exec.CommandContext(ctx, "git", "-C", dir, "show", fmt.Sprintf("%s:%s", gitBranchRef, cleanPath))
			if out, err := cmd.Output(); err == nil && len(out) > 0 {
				contentStr := string(out)
				tfvarsRawContentCacheMu.Lock()
				tfvarsRawContentCache[cacheKey] = tfvarsContentCacheEntry{content: contentStr, fetchedAt: time.Now()}
				tfvarsRawContentCacheMu.Unlock()
				return contentStr
			}

			// Try origin/<branch>
			cmdOrigin := exec.CommandContext(ctx, "git", "-C", dir, "show", fmt.Sprintf("origin/%s:%s", branch, cleanPath))
			if out, err := cmdOrigin.Output(); err == nil && len(out) > 0 {
				contentStr := string(out)
				tfvarsRawContentCacheMu.Lock()
				tfvarsRawContentCache[cacheKey] = tfvarsContentCacheEntry{content: contentStr, fetchedAt: time.Now()}
				tfvarsRawContentCacheMu.Unlock()
				return contentStr
			}

			// If local git repo is available, don't fall through to non-branch disk read
			tfvarsRawContentCacheMu.Lock()
			tfvarsRawContentCache[cacheKey] = tfvarsContentCacheEntry{content: "", fetchedAt: time.Now()}
			tfvarsRawContentCacheMu.Unlock()
			return ""
		}

		candidateFile := filepath.Join(dir, cleanPath)
		if data, err := os.ReadFile(candidateFile); err == nil && len(data) > 0 {
			contentStr := string(data)
			tfvarsRawContentCacheMu.Lock()
			tfvarsRawContentCache[cacheKey] = tfvarsContentCacheEntry{content: contentStr, fetchedAt: time.Now()}
			tfvarsRawContentCacheMu.Unlock()
			return contentStr
		}
	}

	targetURL := fmt.Sprintf("https://api.github.com/repos/oona-insurance/oona-dtc-country-terraform-iac/contents/%s?ref=%s", cleanPath, url.QueryEscape(branch))

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, targetURL, nil)
	if err != nil {
		return ""
	}

	req.Header.Set("User-Agent", "Oona-Dev-Portal/1.0")
	req.Header.Set("Accept", "application/vnd.github.v3.raw")
	token := os.Getenv("GITHUB_TOKEN")
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}

	client := &http.Client{Timeout: 1500 * time.Millisecond}
	resp, err := client.Do(req)
	if err != nil || resp.StatusCode != http.StatusOK {
		tfvarsRawContentCacheMu.Lock()
		tfvarsRawContentCache[cacheKey] = tfvarsContentCacheEntry{content: "", fetchedAt: time.Now()}
		tfvarsRawContentCacheMu.Unlock()
		return ""
	}
	defer resp.Body.Close()

	bodyBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		return ""
	}

	contentStr := string(bodyBytes)
	tfvarsRawContentCacheMu.Lock()
	tfvarsRawContentCache[cacheKey] = tfvarsContentCacheEntry{content: contentStr, fetchedAt: time.Now()}
	tfvarsRawContentCacheMu.Unlock()

	return contentStr
}

func ParseVariablesFromTFVarsContent(content string) map[string]string {
	result := make(map[string]string)
	if content == "" {
		return result
	}

	reAttr := regexp.MustCompile(`(?m)^\s*([a-zA-Z0-9_\-]+)\s*=\s*["']?([^"'\r\n]+)["']?`)
	matches := reAttr.FindAllStringSubmatch(content, -1)
	for _, m := range matches {
		if len(m) == 3 {
			result[strings.ToUpper(m[1])] = strings.TrimSpace(m[2])
		}
	}

	reEnvVars := regexp.MustCompile(`(?s)env_vars\s*=\s*\{(.*?)\}`)
	envMatches := reEnvVars.FindStringSubmatch(content)
	if len(envMatches) > 1 {
		inner := envMatches[1]
		reMapItem := regexp.MustCompile(`(?m)\s*([a-zA-Z0-9_\-]+)\s*=\s*["']([^"']+)["']`)
		mapMatches := reMapItem.FindAllStringSubmatch(inner, -1)
		for _, mm := range mapMatches {
			if len(mm) == 3 {
				result[strings.ToUpper(mm[1])] = mm[2]
			}
		}
	}

	return result
}
