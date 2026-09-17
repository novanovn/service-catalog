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
	"service-catalog/internal/auth"
	"service-catalog/internal/docs"
	db "service-catalog/internal/repository/postgres/generated"
	"service-catalog/internal/worker/infra"
)

// SubShelfDomain represents a functional domain sub-shelf within a country main-shelf.
type SubShelfDomain struct {
	Code        string         // e.g. "ph:neuron", "ph:dtc", "all:devops"
	Name        string         // e.g. "Neuron Integration Suite"
	DomainKey   string         // e.g. "neuron", "dtc", "kahoona"
	Description string         // e.g. "Philippines Neuron Insurance Core & Renewal Services"
	Services    []CatalogEntry // services belonging to this domain
	Total       int
}

// MainShelfCountry represents a country main shelf (e.g. Philippines, Indonesia).
type MainShelfCountry struct {
	Code       string           // "ph" | "id"
	Name       string           // "Philippines" | "Indonesia"
	Flag       string           // 🇵🇭 | 🇮🇩
	Total      int
	IsMine     bool
	SubShelves []SubShelfDomain
}

type CatalogBreadcrumb struct {
	Label    string
	URL      string
	IsActive bool
}

// getUserShelfScope returns (assignedShelves, hasGlobalAccess) for the given claims.
// Admin/DevOps roles and any user with a "*" entry get global access (see everything,
// nothing pinned as "mine" specifically). A nil claims (unauthenticated/test path)
// also gets global access so existing behavior is preserved when auth is not wired up.
func getUserShelfScope(ctx context.Context, claims *auth.Claims) (shelves []string, hasGlobalAccess bool) {
	if claims == nil {
		return nil, true
	}
	if claims.Role == "admin" || claims.Role == "devops" {
		return nil, true
	}
	if DB == nil {
		return nil, true
	}
	user, err := DB.GetUserByEmail(ctx, claims.Email)
	if err != nil {
		// Fail open to global access rather than locking someone out on a lookup error
		return nil, true
	}
	for _, s := range user.AssignedShelves {
		if s == "*" {
			return nil, true
		}
	}
	return user.AssignedShelves, false
}

func RenderCatalogList(w http.ResponseWriter, r *http.Request) {
	tmpl, err := parsePage("catalog_list.html")
	if err != nil {
		http.Error(w, "Failed to load template: "+err.Error(), http.StatusInternalServerError)
		return
	}

	claims, _ := r.Context().Value(userCtxKey).(*auth.Claims)

	// Parse query params
	showAllCountries := r.URL.Query().Get("all") == "true"
	selectedCountryParam := strings.ToLower(strings.TrimSpace(r.URL.Query().Get("country")))
	selectedDomainParam := strings.ToLower(strings.TrimSpace(r.URL.Query().Get("domain")))

	myShelves, hasGlobalAccess := getUserShelfScope(r.Context(), claims)
	// Shelf codes are "<country>:<domain>" (e.g. "id:coreplus"). Per current design,
	// folder assignment scopes access at the COUNTRY level: being assigned any shelf
	// under "id" grants visibility into all of PH/ID's services regardless of domain,
	// since domain is now a per-row badge rather than a separate folder level.
	myCountrySet := make(map[string]bool)
	for _, s := range myShelves {
		parts := strings.SplitN(strings.ToLower(s), ":", 2)
		if len(parts) > 0 && parts[0] != "" && parts[0] != "all" {
			myCountrySet[parts[0]] = true
		}
	}
	hasCountryScope := !hasGlobalAccess && len(myCountrySet) > 0

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

	// --- New hierarchy: Main Shelf (Country) -> Sub Shelf (Domain) -> Service ---
	countryOrder := []string{"ph", "id"}
	countryLabels := map[string]struct{ Name, Flag string }{
		"ph": {"Philippines", "🇵🇭"},
		"id": {"Indonesia", "🇮🇩"},
	}

	type shelfDef struct {
		Code        string
		Name        string
		DomainKey   string
		Description string
	}

	countryShelvesMap := make(map[string][]shelfDef)
	for _, c := range countryOrder {
		activeShelves := SystemParams.GetActiveShelvesByCountry(c)
		for _, s := range activeShelves {
			parts := strings.SplitN(s.Code, ":", 2)
			domKey := ""
			if len(parts) == 2 {
				domKey = strings.ToLower(parts[1])
			}
			countryShelvesMap[c] = append(countryShelvesMap[c], shelfDef{
				Code:        s.Code,
				Name:        s.Name,
				DomainKey:   domKey,
				Description: s.Description,
			})
		}
	}

	resolveShelfCode := func(countryCode, domainName string) string {
		c := strings.ToLower(strings.TrimSpace(countryCode))
		if c == "" {
			c = "ph"
		}
		d := strings.ToLower(strings.TrimSpace(domainName))
		if c == "ph" {
			if d == "neuron" || d == "integration" {
				return "ph:neuron"
			}
			if d == "dtc" {
				return "ph:dtc"
			}
			if d == "kahoona" {
				return "ph:kahoona"
			}
		} else if c == "id" {
			if d == "coreplus" || d == "integration" || d == "core" {
				return "id:coreplus"
			}
			if d == "dtc" {
				return "id:dtc"
			}
		}
		if d == "devops" {
			return "all:devops"
		}
		return fmt.Sprintf("%s:%s", c, d)
	}

	serviceBuckets := make(map[string][]CatalogEntry)
	dynamicShelves := make(map[string]shelfDef)

	for _, svc := range allServices {
		c := strings.ToLower(strings.TrimSpace(svc.Country))
		if c == "" {
			c = "ph"
		}
		sCode := resolveShelfCode(c, svc.Domain)
		serviceBuckets[sCode] = append(serviceBuckets[sCode], svc)

		known := false
		for _, def := range countryShelvesMap[c] {
			if def.Code == sCode {
				known = true
				break
			}
		}
		if !known {
			cleanDom := svc.Domain
			if cleanDom == "" {
				cleanDom = "Core"
			} else {
				cleanDom = strings.ToUpper(cleanDom[:1]) + cleanDom[1:]
			}
			dynamicShelves[sCode] = shelfDef{
				Code:        sCode,
				Name:        fmt.Sprintf("%s Domain Suite", cleanDom),
				DomainKey:   strings.ToLower(cleanDom),
				Description: fmt.Sprintf("%s microservices and APIs", cleanDom),
			}
		}
	}

	var allMainShelves []MainShelfCountry
	for _, c := range countryOrder {
		meta := countryLabels[c]
		var subShelves []SubShelfDomain
		countryTotal := 0

		for _, def := range countryShelvesMap[c] {
			svcs := serviceBuckets[def.Code]
			subShelves = append(subShelves, SubShelfDomain{
				Code:        def.Code,
				Name:        def.Name,
				DomainKey:   def.DomainKey,
				Description: def.Description,
				Services:    svcs,
				Total:       len(svcs),
			})
			countryTotal += len(svcs)
		}

		for sCode, def := range dynamicShelves {
			if strings.HasPrefix(sCode, c+":") {
				svcs := serviceBuckets[sCode]
				subShelves = append(subShelves, SubShelfDomain{
					Code:        def.Code,
					Name:        def.Name,
					DomainKey:   def.DomainKey,
					Description: def.Description,
					Services:    svcs,
					Total:       len(svcs),
				})
				countryTotal += len(svcs)
			}
		}

		// Prioritize sub-shelves that contain active services at the top,
		// then sort alphabetically by domain name.
		sort.SliceStable(subShelves, func(i, j int) bool {
			if (subShelves[i].Total > 0) != (subShelves[j].Total > 0) {
				return subShelves[i].Total > subShelves[j].Total
			}
			return subShelves[i].Name < subShelves[j].Name
		})

		allMainShelves = append(allMainShelves, MainShelfCountry{
			Code:       c,
			Name:       meta.Name,
			Flag:       meta.Flag,
			Total:      countryTotal,
			IsMine:     myCountrySet[c],
			SubShelves: subShelves,
		})
	}

	var visibleCountries []MainShelfCountry
	var hiddenCountries []MainShelfCountry
	for _, ms := range allMainShelves {
		if hasCountryScope && ms.IsMine {
			visibleCountries = append(visibleCountries, ms)
		} else if hasCountryScope {
			hiddenCountries = append(hiddenCountries, ms)
		} else {
			visibleCountries = append(visibleCountries, ms)
		}
	}

	otherCount := len(hiddenCountries)
	if hasCountryScope && showAllCountries {
		visibleCountries = append(visibleCountries, hiddenCountries...)
	}

	level := "root"
	var activeCountry *MainShelfCountry
	var activeSubShelf *SubShelfDomain
	var breadcrumbs []CatalogBreadcrumb

	breadcrumbs = append(breadcrumbs, CatalogBreadcrumb{
		Label:    "All Main Shelves",
		URL:      "/catalog",
		IsActive: selectedCountryParam == "",
	})

	if selectedCountryParam != "" {
		for i := range visibleCountries {
			if strings.EqualFold(visibleCountries[i].Code, selectedCountryParam) {
				activeCountry = &visibleCountries[i]
				break
			}
		}
		if activeCountry != nil {
			level = "country"
			breadcrumbs = append(breadcrumbs, CatalogBreadcrumb{
				Label:    activeCountry.Flag + " " + activeCountry.Name + " Main Shelf",
				URL:      fmt.Sprintf("/catalog?country=%s", activeCountry.Code),
				IsActive: selectedDomainParam == "",
			})

			if selectedDomainParam != "" {
				for i := range activeCountry.SubShelves {
					shelf := &activeCountry.SubShelves[i]
					if strings.EqualFold(shelf.Code, selectedDomainParam) ||
						strings.EqualFold(shelf.DomainKey, selectedDomainParam) ||
						strings.HasSuffix(strings.ToLower(shelf.Code), ":"+selectedDomainParam) {
						activeSubShelf = shelf
						break
					}
				}
				if activeSubShelf != nil {
					level = "domain"
					breadcrumbs = append(breadcrumbs, CatalogBreadcrumb{
						Label:    "📂 " + activeSubShelf.Name,
						URL:      fmt.Sprintf("/catalog?country=%s&domain=%s", activeCountry.Code, activeSubShelf.DomainKey),
						IsActive: true,
					})
				}
			}
		}
	}

	data := struct {
		Title               string
		User                *auth.Claims
		Level               string // "root", "country", "domain"
		Breadcrumbs         []CatalogBreadcrumb
		Services            []CatalogEntry
		Countries           []MainShelfCountry
		ActiveCountry       *MainShelfCountry
		ActiveSubShelf      *SubShelfDomain
		TotalServices       int
		HasCountryScope     bool
		ShowAllCountries    bool
		OtherCountriesCount int
	}{
		Title:               "Service Catalog",
		User:                claims,
		Level:               level,
		Breadcrumbs:         breadcrumbs,
		Services:            allServices,
		Countries:           visibleCountries,
		ActiveCountry:       activeCountry,
		ActiveSubShelf:      activeSubShelf,
		TotalServices:       len(allServices),
		HasCountryScope:     hasCountryScope,
		ShowAllCountries:    showAllCountries,
		OtherCountriesCount: otherCount,
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

	selectedEnv := strings.ToLower(strings.TrimSpace(r.URL.Query().Get("env")))
	if selectedEnv != "uat" && selectedEnv != "preprod" && selectedEnv != "prod" {
		selectedEnv = "uat"
	}

	candidateBranches := []string{branch, "ci/portal"}

	// Check whether each environment is marked deployed in catalog records.
	// Presence of a terraform.tfvars file is NOT enough — that only means IaC was
	// committed. Castan pills stay disabled until a promotion ticket is approved
	// (or the env is already in catalog.deployed_envs).
	checkEnvActive := func(envName string) bool {
		envLower := strings.ToLower(envName)
		if DBPool != nil {
			var hasEnv bool
			_ = DBPool.QueryRow(r.Context(), `
				SELECT EXISTS(
					SELECT 1 FROM catalog 
					WHERE (LOWER(name) = LOWER($1) OR id::text = $1)
					AND $2 = ANY(deployed_envs)
				)
			`, serviceParam, envLower).Scan(&hasEnv)
			if hasEnv {
				return true
			}
		}

		if entry, found := ServiceCatalog.FindByNameOrID(serviceParam); found {
			for _, de := range entry.DeployedEnvs {
				if strings.EqualFold(de, envLower) {
					return true
				}
			}
		}
		return false
	}
	uatActive := checkEnvActive("uat")
	preprodActive := checkEnvActive("preprod")
	prodActive := checkEnvActive("prod")

	// If the requested environment is not deployed, fallback to an active one so users
	// can't select an un-deployed environment directly via query param
	if selectedEnv == "preprod" && !preprodActive {
		if uatActive {
			selectedEnv = "uat"
		} else if prodActive {
			selectedEnv = "prod"
		}
	} else if selectedEnv == "prod" && !prodActive {
		if uatActive {
			selectedEnv = "uat"
		} else if preprodActive {
			selectedEnv = "preprod"
		}
	}

	terraformPath := fmt.Sprintf("02-app-setup/%s/%s/%s/services/%s", domainLower, countryLower, selectedEnv, serviceName)
	terraformURL := fmt.Sprintf("https://github.com/oona-insurance/oona-dtc-country-terraform-iac/tree/%s/%s", branch, terraformPath)
	terraformExists := false
	tfRepoID := ""

	// Multi-branch scanner: fast lookup across active branch and ci/portal for selected environment
	for _, br := range candidateBranches {
		if resolvedP, found := ResolveTerraformPathForEnv(r.Context(), domain, country, selectedEnv, serviceName, br); found {
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

	if (selectedEnv == "uat" && uatActive) || (selectedEnv == "preprod" && preprodActive) || (selectedEnv == "prod" && prodActive) {
		awsLastModified = time.Now().UTC().Format("2006-01-02 15:04:05 UTC")
		awsLastInvoked = fmt.Sprintf("Active in %s (Ready to invoke)", strings.ToUpper(selectedEnv))
	} else {
		awsLastModified = fmt.Sprintf("Not deployed in %s", strings.ToUpper(selectedEnv))
		awsLastInvoked = fmt.Sprintf("No active instance in %s", strings.ToUpper(selectedEnv))
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
		SelectedEnv       string
		UATActive         bool
		PreProdActive     bool
		ProdActive        bool
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
		SelectedEnv:       selectedEnv,
		UATActive:         uatActive,
		PreProdActive:     preprodActive,
		ProdActive:        prodActive,
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
