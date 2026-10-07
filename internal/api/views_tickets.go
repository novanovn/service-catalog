package api

import (
	"context"
	"encoding/json"
	"fmt"
	"html/template"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"service-catalog/internal/auth"
	db "service-catalog/internal/repository/postgres/generated"
	"service-catalog/internal/worker/infra"
)

func RenderTicketForm(w http.ResponseWriter, r *http.Request) {
	tmpl, err := parsePage("ticket_form.html")
	if err != nil {
		http.Error(w, "Failed to load template: "+err.Error(), http.StatusInternalServerError)
		return
	}

	claims, _ := r.Context().Value(userCtxKey).(*auth.Claims)

	data := struct {
		Title     string
		User      *auth.Claims
		Countries []SystemParam
		Domains   []SystemParam
	}{
		Title:     "Onboard Service",
		User:      claims,
		Countries: SystemParams.GetActiveCountries(),
		Domains:   SystemParams.GetActiveDomains(),
	}

	tmpl.ExecuteTemplate(w, "base", data)
}

// RenderAdminParameters renders the System Parameters management page

func PreviewPipelineName(w http.ResponseWriter, r *http.Request) {
	r.ParseForm()
	country := strings.ToLower(r.FormValue("country"))
	domain := strings.ToLower(r.FormValue("domain"))
	serviceName := strings.ToLower(r.FormValue("service_name"))

	if country == "" {
		country = "id"
	}
	if domain == "" {
		domain = "integration"
	}
	if serviceName == "" {
		serviceName = "..."
	}

	// The OONA Standard Rule
	pipelineName := fmt.Sprintf("lmd-oona-%s-%s-%s", country, domain, serviceName)
	tfPath := fmt.Sprintf("02-app-setup/%s/%s/uat/services/%s", domain, country, serviceName)

	html := fmt.Sprintf(`
		<div>
			Jenkins Pipeline Name: <strong class="font-mono text-gray-900">%s</strong>
			<br/>
			Expected Terraform Path: <span class="font-mono text-gray-600 text-xs">%s</span>
		</div>
	`, pipelineName, tfPath)

	w.Header().Set("Content-Type", "text/html")
	w.Write([]byte(html))
}

type GitHubBranchInfo struct {
	Name string `json:"name"`
}

type JenkinsJobItem struct {
	Class string `json:"_class"`
	Name  string `json:"name"`
	URL   string `json:"url"`
}

type JenkinsTreeResponse struct {
	Jobs []JenkinsJobItem `json:"jobs"`
}

// FetchLiveJenkinsFolders connects to Jenkins REST API (automation.oona-insurance.com) and fetches root folders dynamically
func FetchLiveJenkinsFolders(ctx context.Context) []string {
	baseURL := os.Getenv("JENKINS_URL")
	if baseURL == "" {
		baseURL = "https://automation.oona-insurance.com"
	}
	baseURL = strings.TrimSuffix(baseURL, "/")

	apiURL := fmt.Sprintf("%s/api/json?tree=jobs[name,url,_class]", baseURL)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, apiURL, nil)
	if err != nil {
		return defaultJenkinsFolders()
	}

	req.Header.Set("User-Agent", "Oona-Dev-Portal/1.0")
	req.Header.Set("Accept", "application/json")

	authUser := os.Getenv("JENKINS_USER")
	authToken := os.Getenv("JENKINS_TOKEN")
	if authUser != "" && authToken != "" {
		req.SetBasicAuth(authUser, authToken)
	}

	client := &http.Client{Timeout: 5 * time.Second}
	resp, err := client.Do(req)
	if err != nil || resp.StatusCode != http.StatusOK {
		return defaultJenkinsFolders()
	}
	defer resp.Body.Close()

	var tree JenkinsTreeResponse
	if err := json.NewDecoder(resp.Body).Decode(&tree); err != nil {
		return defaultJenkinsFolders()
	}

	var folders []string
	for _, job := range tree.Jobs {
		if strings.Contains(job.Class, "Folder") || strings.Contains(strings.ToLower(job.Name), "project") || strings.Contains(strings.ToLower(job.Name), "prod") {
			folders = append(folders, job.Name)
		}
	}

	if len(folders) == 0 {
		return defaultJenkinsFolders()
	}

	return folders
}

func defaultJenkinsFolders() []string {
	return []string{
		"AWS Lambda Projects",
		"AWS FE S3 Projects",
		"AWS AEM Projects",
		"AWS-AEM-Prod",
	}
}

var (
	branchesCacheMu   sync.RWMutex
	branchesCache     []string
	branchesFetchedAt time.Time
)

// FetchLiveTerraformBranches connects to GitHub REST API and fetches active repository branches in oona-dtc-country-terraform-iac
func FetchLiveTerraformBranches(ctx context.Context) []string {
	return fetchLiveTerraformBranchesInternal(ctx, false)
}

// RefreshLiveTerraformBranches forces a cache bypass and re-fetches branches from GitHub
func RefreshLiveTerraformBranches(ctx context.Context) []string {
	return fetchLiveTerraformBranchesInternal(ctx, true)
}

func fetchLiveTerraformBranchesInternal(ctx context.Context, forceRefresh bool) []string {
	if !forceRefresh {
		branchesCacheMu.RLock()
		if len(branchesCache) > 0 && time.Since(branchesFetchedAt) < 10*time.Minute {
			res := make([]string, len(branchesCache))
			copy(res, branchesCache)
			branchesCacheMu.RUnlock()
			return res
		}
		branchesCacheMu.RUnlock()
	}

	// 1. Prefer the locally mounted IaC repo — the GitHub repo is private and the
	// container has no GITHUB_TOKEN, so the REST API call below always 404s.
	// The local mount, however, is a full clone with every remote branch ref,
	// so this is both faster and actually reflects what was just pushed.
	if names := listLocalGitBranches(ctx); len(names) > 0 {
		branchesCacheMu.Lock()
		branchesCache = names
		branchesFetchedAt = time.Now()
		branchesCacheMu.Unlock()
		return names
	}

	// 2. Fall back to the GitHub REST API (works if GITHUB_TOKEN is configured
	// and the repo is reachable, e.g. in environments without a local mount).
	targetURL := "https://api.github.com/repos/oona-insurance/oona-dtc-country-terraform-iac/branches?per_page=100"

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, targetURL, nil)
	if err != nil {
		return []string{"main", "dev", "staging", "uat"}
	}

	req.Header.Set("User-Agent", "Oona-Dev-Portal/1.0")
	token := GetActiveGitHubToken(ctx)
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}

	client := &http.Client{Timeout: 3 * time.Second}
	resp, err := client.Do(req)
	if err != nil || resp.StatusCode != http.StatusOK {
		return []string{"main", "dev", "staging", "uat"}
	}
	defer resp.Body.Close()

	var branchList []GitHubBranchInfo
	if err := json.NewDecoder(resp.Body).Decode(&branchList); err != nil {
		return []string{"main", "dev", "staging", "uat"}
	}

	var names []string
	for _, b := range branchList {
		if b.Name != "" && !strings.HasPrefix(b.Name, "agent/") && !strings.HasPrefix(b.Name, "ci/") {
			names = append(names, b.Name)
		}
	}

	if len(names) == 0 {
		return []string{"main", "dev", "staging", "uat"}
	}

	branchesCacheMu.Lock()
	branchesCache = names
	branchesFetchedAt = time.Now()
	branchesCacheMu.Unlock()

	return names
}

// listLocalGitBranches reads remote branch refs directly from the locally mounted
// Terraform IaC repo (git for-each-ref — no network, no GitHub token required).
// This reflects branches immediately after `git push`, unlike the GitHub API path
// which 404s against this private repo without a configured token.
func listLocalGitBranches(ctx context.Context) []string {
	localDirs := []string{
		os.Getenv("TERRAFORM_IAC_DIR"),
		"/terraform-iac",
		"../oona-dtc-country-terraform-iac",
		"/Users/novanhariman/Documents/Ngulik/oona-dtc-country-terraform-iac",
	}

	for _, dir := range localDirs {
		if dir == "" {
			continue
		}
		if fi, err := os.Stat(filepath.Join(dir, ".git")); err != nil || !fi.IsDir() {
			continue
		}

		// Refresh remote refs if the mount is writable; ignore failures on read-only mounts.
		fetchCtx, cancelFetch := context.WithTimeout(ctx, 4*time.Second)
		_ = exec.CommandContext(fetchCtx, "git", "-C", dir, "fetch", "origin", "--prune", "--quiet").Run()
		cancelFetch()

		listCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
		cmd := exec.CommandContext(listCtx, "git", "-C", dir, "for-each-ref",
			"--sort=-committerdate", "--format=%(refname:short)", "refs/remotes/origin/")
		out, err := cmd.Output()
		cancel()
		if err != nil {
			continue
		}

		var names []string
		seen := make(map[string]bool)
		for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
			line = strings.TrimSpace(line)
			line = strings.TrimPrefix(line, "origin/")
			if line == "" || line == "HEAD" || line == "origin" || seen[line] {
				continue
			}
			// Skip ephemeral agent/bot branches and experimental ci/* branches per workflow standard
			if strings.HasPrefix(line, "agent/") || strings.HasPrefix(line, "ci/") {
				continue
			}
			seen[line] = true
			names = append(names, line)
		}
		if len(names) > 0 {
			// main is the single source of truth for CI/CD; always pin it first
			// regardless of its position in the commit-date-sorted local list.
			filtered := make([]string, 0, len(names))
			filtered = append(filtered, "main")
			for _, n := range names {
				if n != "main" {
					filtered = append(filtered, n)
				}
			}
			// Cap the list so the dropdown stays usable — main plus the dozen
			// most recently touched branches is enough for a review workflow.
			const maxBranches = 12
			if len(filtered) > maxBranches {
				filtered = filtered[:maxBranches]
			}
			return filtered
		}
	}
	return nil
}

var (
	repoIDCache   = make(map[string]string)
	repoIDCacheMu sync.RWMutex
)

// FetchExistingRepoIDFromTFVars attempts to read terraform.tfvars from local disk or GitHub for a given terraform path and extract existing_github_repo_id (with TTL cache)
func FetchExistingRepoIDFromTFVars(ctx context.Context, terraformPath string, branch string) string {
	if branch == "" {
		branch = "main"
	}
	cleanPath := strings.TrimPrefix(terraformPath, "/")
	cacheKey := fmt.Sprintf("%s:%s", branch, cleanPath)

	repoIDCacheMu.RLock()
	if cachedVal, found := repoIDCache[cacheKey]; found {
		repoIDCacheMu.RUnlock()
		return cachedVal
	}
	repoIDCacheMu.RUnlock()

	var bodyBytes []byte

	// 1. Check local mounted repository or local filesystem fallback
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
		candidateFile := filepath.Join(dir, cleanPath, "terraform.tfvars")
		if data, err := os.ReadFile(candidateFile); err == nil && len(data) > 0 {
			bodyBytes = data
			break
		}
	}

	// 2. If not found locally, query GitHub API
	if len(bodyBytes) == 0 {
		tfvarsPath := fmt.Sprintf("%s/terraform.tfvars", cleanPath)
		targetURL := fmt.Sprintf("https://api.github.com/repos/oona-insurance/oona-dtc-country-terraform-iac/contents/%s?ref=%s", tfvarsPath, url.QueryEscape(branch))

		req, err := http.NewRequestWithContext(ctx, http.MethodGet, targetURL, nil)
		if err == nil {
			req.Header.Set("User-Agent", "Oona-Dev-Portal/1.0")
			req.Header.Set("Accept", "application/vnd.github.v3.raw")
			token := GetActiveGitHubToken(ctx)
			if token != "" {
				req.Header.Set("Authorization", "Bearer "+token)
			}

			client := &http.Client{Timeout: 1500 * time.Millisecond}
			resp, doErr := client.Do(req)
			if doErr == nil && resp.StatusCode == http.StatusOK {
				defer resp.Body.Close()
				bodyBytes, _ = io.ReadAll(resp.Body)
			}
		}
	}

	if len(bodyBytes) == 0 {
		return ""
	}

	extracted := infra.ExtractTFVarValueFromHCL(bodyBytes, "existing_github_repo_id")
	if extracted == "" {
		re := regexp.MustCompile(`(?m)^\s*existing_github_repo_id\s*=\s*["']([^"']+)["']`)
		matches := re.FindStringSubmatch(string(bodyBytes))
		if len(matches) > 1 {
			extracted = strings.TrimSpace(matches[1])
		}
	}

	repoIDCacheMu.Lock()
	repoIDCache[cacheKey] = extracted
	repoIDCacheMu.Unlock()

	return extracted
}

// ResolveAWSAccountID maps country and domain to the target AWS Account ID
func ResolveAWSAccountID(country, domain string) string {
	if strings.ToUpper(country) == "PH" {
		if strings.EqualFold(domain, "integration") {
			return "381492025569" // PH-Integration-UAT
		}
		return "471112995648" // PH-DTC-UAT
	}
	return "794038209116" // ID-DTC-UAT / Account scope
}

// JenkinsJobNameFromRepoID turns terraform.tfvars existing_github_repo_id into a Jenkins job name.
// Values are stored as either a bare repo ("lmd-oona-ph-integration-health-renewal-svc")
// or an org-qualified id ("oona-insurance/lmd-..."). Placeholder template ids are ignored.
func JenkinsJobNameFromRepoID(repoID string) string {
	repoID = strings.TrimSpace(repoID)
	if repoID == "" {
		return ""
	}
	repoID = strings.TrimSuffix(repoID, ".git")
	if i := strings.LastIndex(repoID, "/"); i >= 0 {
		repoID = strings.TrimSpace(repoID[i+1:])
	}
	repoID = strings.TrimSuffix(repoID, ".git")
	if repoID == "" || repoID == "change-me-repo" {
		return ""
	}
	return repoID
}

// ResolveCanonicalPipelineName is the display/create source of truth for Jenkins job names:
// existing_github_repo_id in terraform.tfvars, then a saved ticket/catalog value, then the
// lowercase lmd-oona-{country}-{domain}-{service} convention. Country codes in system_parameters
// are "PH"/"ID"; they must never be interpolated raw into a job name.
func ResolveCanonicalPipelineName(ctx context.Context, domain, country, serviceName, branch, saved string) string {
	if branch == "" {
		branch = "main"
	}
	if resolved, found := ResolveTerraformPath(ctx, domain, country, serviceName, branch); found {
		if id := JenkinsJobNameFromRepoID(FetchExistingRepoIDFromTFVars(ctx, resolved, branch)); id != "" {
			return id
		}
	}
	if saved = strings.TrimSpace(saved); saved != "" {
		return saved
	}
	domainLower := strings.ToLower(strings.TrimSpace(domain))
	countryLower := strings.ToLower(strings.TrimSpace(country))
	if domainLower == "" {
		domainLower = "integration"
	}
	if countryLower == "" {
		countryLower = "ph"
	}
	return fmt.Sprintf("lmd-oona-%s-%s-%s", countryLower, domainLower, serviceName)
}

// ResolveTerraformPath checks multiple candidate folder names to find where the Terraform path exists on a branch in UAT
func ResolveTerraformPath(ctx context.Context, domain, country, serviceName, branch string) (string, bool) {
	return ResolveTerraformPathForEnv(ctx, domain, country, "uat", serviceName, branch)
}

// ResolveTerraformPathForEnv checks candidate folder names for a specific environment (uat, preprod, prod)
func ResolveTerraformPathForEnv(ctx context.Context, domain, country, env, serviceName, branch string) (string, bool) {
	domainLower := strings.ToLower(domain)
	countryLower := strings.ToLower(country)
	if domainLower == "" {
		domainLower = "integration"
	}
	if countryLower == "" {
		countryLower = "ph"
	}
	envLower := strings.ToLower(env)
	if envLower == "" {
		envLower = "uat"
	}

	cleanName := strings.TrimSuffix(serviceName, "-clone")
	cleanShortName := strings.TrimPrefix(cleanName, "lmd-oona-ph-integration-")
	cleanShortName = strings.TrimPrefix(cleanShortName, "lmd-oona-id-integration-")
	cleanShortName = strings.TrimPrefix(cleanShortName, "lmd-oona-")

	candidatePaths := []string{
		fmt.Sprintf("02-app-setup/%s/%s/%s/services/%s", domainLower, countryLower, envLower, serviceName),
		fmt.Sprintf("02-app-setup/%s/%s/%s/services/%s", domainLower, countryLower, envLower, cleanName),
		fmt.Sprintf("02-app-setup/%s/%s/%s/services/%s", domainLower, countryLower, envLower, cleanShortName),
	}

	for _, p := range candidatePaths {
		if CheckTerraformPathExists(ctx, p, branch) {
			return p, true
		}
	}

	return candidatePaths[0], false
}

type tfCacheItem struct {
	exists    bool
	timestamp time.Time
}

var (
	tfPathCache   = make(map[string]tfCacheItem)
	tfPathCacheMu sync.RWMutex
)

// FlushTerraformPathCache clears the in-memory Terraform path resolution cache
func FlushTerraformPathCache() {
	tfPathCacheMu.Lock()
	tfPathCache = make(map[string]tfCacheItem)
	tfPathCacheMu.Unlock()
}

// CheckTerraformPathExists checks if a folder path exists locally on disk or in the GitHub Terraform IaC repository
func CheckTerraformPathExists(ctx context.Context, path string, branch string) bool {
	if branch == "" {
		branch = "main"
	}
	cleanPath := strings.TrimPrefix(path, "/")
	cacheKey := fmt.Sprintf("%s:%s", branch, cleanPath)

	tfPathCacheMu.RLock()
	if cached, found := tfPathCache[cacheKey]; found {
		// Positive hits cache for 10 minutes.
		// Negative misses (exists == false) only cache for 5 seconds so newly pushed / merged commits are detected promptly.
		ttl := 10 * time.Minute
		if !cached.exists {
			ttl = 5 * time.Second
		}
		if time.Since(cached.timestamp) < ttl {
			tfPathCacheMu.RUnlock()
			return cached.exists
		}
	}
	tfPathCacheMu.RUnlock()

	// 1. Check local mounted repository or local filesystem first
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
		// If dir is a git repository, check whether the path exists on that specific branch
		gitDir := filepath.Join(dir, ".git")
		if _, err := os.Stat(gitDir); err == nil {
			var gitBranchRef string
			if branch == "main" || branch == "master" {
				gitBranchRef = "main"
			} else {
				// Check local branch first, then remote origin branch
				gitBranchRef = branch
			}

			// Try git cat-file -e <ref>:<path>/terraform.tfvars
			cmd := exec.CommandContext(ctx, "git", "-C", dir, "cat-file", "-e", fmt.Sprintf("%s:%s/terraform.tfvars", gitBranchRef, cleanPath))
			if err := cmd.Run(); err == nil {
				tfPathCacheMu.Lock()
				tfPathCache[cacheKey] = tfCacheItem{exists: true, timestamp: time.Now()}
				tfPathCacheMu.Unlock()
				return true
			}

			// Try origin/<branch>
			cmdOrigin := exec.CommandContext(ctx, "git", "-C", dir, "cat-file", "-e", fmt.Sprintf("origin/%s:%s/terraform.tfvars", branch, cleanPath))
			if err := cmdOrigin.Run(); err == nil {
				tfPathCacheMu.Lock()
				tfPathCache[cacheKey] = tfCacheItem{exists: true, timestamp: time.Now()}
				tfPathCacheMu.Unlock()
				return true
			}

			// If git repo exists locally, the path definitely does not exist on this specific branch
			tfPathCacheMu.Lock()
			tfPathCache[cacheKey] = tfCacheItem{exists: false, timestamp: time.Now()}
			tfPathCacheMu.Unlock()
			return false
		}

		candidateFolder := filepath.Join(dir, cleanPath)
		candidateTFVars := filepath.Join(dir, cleanPath, "terraform.tfvars")
		if fi, err := os.Stat(candidateTFVars); err == nil && !fi.IsDir() {
			tfPathCacheMu.Lock()
			tfPathCache[cacheKey] = tfCacheItem{exists: true, timestamp: time.Now()}
			tfPathCacheMu.Unlock()
			return true
		}
		if fi, err := os.Stat(candidateFolder); err == nil && fi.IsDir() {
			tfPathCacheMu.Lock()
			tfPathCache[cacheKey] = tfCacheItem{exists: true, timestamp: time.Now()}
			tfPathCacheMu.Unlock()
			return true
		}
	}

	// 2. Query GitHub API with Fallback
	targetURL := fmt.Sprintf("https://api.github.com/repos/oona-insurance/oona-dtc-country-terraform-iac/contents/%s?ref=%s", cleanPath, url.QueryEscape(branch))

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, targetURL, nil)
	if err != nil {
		return false
	}

	req.Header.Set("User-Agent", "Oona-Dev-Portal/1.0")
	token := GetActiveGitHubToken(ctx)
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}

	client := &http.Client{
		Timeout: 1500 * time.Millisecond,
	}

	resp, err := client.Do(req)
	if err != nil {
		return false
	}
	defer resp.Body.Close()

	exists := (resp.StatusCode == http.StatusOK)

	tfPathCacheMu.Lock()
	tfPathCache[cacheKey] = tfCacheItem{
		exists:    exists,
		timestamp: time.Now(),
	}
	tfPathCacheMu.Unlock()

	return exists
}

type PaginationInfo struct {
	CurrentPage int
	TotalPages  int
	PerPage     int
	TotalCount  int64
	StartItem   int
	EndItem     int
	BaseURL     string
}

// RenderTicketList renders the list of onboarding tickets
func RenderTicketList(w http.ResponseWriter, r *http.Request) {
	tmpl, err := parsePage("ticket_list.html")
	if err != nil {
		http.Error(w, "Failed to load template: "+err.Error(), http.StatusInternalServerError)
		return
	}

	claims, _ := r.Context().Value(userCtxKey).(*auth.Claims)

	// Parse pagination query params
	page, _ := strconv.Atoi(r.URL.Query().Get("page"))
	if page < 1 {
		page = 1
	}
	perPage, _ := strconv.Atoi(r.URL.Query().Get("per_page"))
	if perPage < 1 || perPage > 100 {
		perPage = 20
	}
	offset := int32((page - 1) * perPage)
	limit := int32(perPage)

	type TicketItem struct {
		ID             string
		TicketCode     string
		JiraID         string
		ServiceName    string
		Domain         string
		Country        string
		Status         string
		RequestorEmail string
		CreatedAt      string
	}

	var tickets []TicketItem
	var totalCount int64

	if DB != nil {
		count, cErr := DB.CountTickets(r.Context())
		if cErr == nil {
			totalCount = count
		}

		dbTickets, err := DB.ListTicketsPaginated(r.Context(), db.ListTicketsPaginatedParams{
			Limit:  limit,
			Offset: offset,
		})
		if err == nil {
			for idx, t := range dbTickets {
				var idStr string
				bytes, _ := t.ID.Value()
				if bytes != nil {
					if b, ok := bytes.([]byte); ok && len(b) == 16 {
						idStr = fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:16])
					}
				}
				if idStr == "" {
					idStr = fmt.Sprintf("%x-%x-%x-%x-%x", t.ID.Bytes[0:4], t.ID.Bytes[4:6], t.ID.Bytes[6:8], t.ID.Bytes[8:10], t.ID.Bytes[10:16])
				}

				ticketTime := time.Now()
				createdAtStr := "N/A"
				if t.CreatedAt.Valid {
					ticketTime = t.CreatedAt.Time
					createdAtStr = t.CreatedAt.Time.Format("2006-01-02 15:04")
				}

				// Format Ticket Code: SC-DDMMYY-001 (Service Catalog - TanggalBulanTahun - Sequence)
				seq := int(totalCount) - int(offset) - idx
				if seq < 1 {
					seq = idx + 1
				}
				ticketCode := fmt.Sprintf("SC-%s-%03d", ticketTime.Format("020106"), seq)

				reqEmail := "admin@oona-insurance.com"
				jiraID := ""
				if t.JiraIssueID.Valid && t.JiraIssueID.String != "" {
					jiraID = t.JiraIssueID.String
				}

				if entry, found := ServiceCatalog.FindByNameOrID(t.ServiceName); found {
					if entry.RequestorEmail != "" {
						reqEmail = entry.RequestorEmail
					}
					if entry.JiraID != "" && jiraID == "" {
						jiraID = entry.JiraID
					}
				}

				tickets = append(tickets, TicketItem{
					ID:             idStr,
					TicketCode:     ticketCode,
					JiraID:         jiraID,
					ServiceName:    t.ServiceName,
					Domain:         t.Domain,
					Country:        strings.ToUpper(t.Country),
					Status:         string(t.Status),
					RequestorEmail: reqEmail,
					CreatedAt:      createdAtStr,
				})
			}
		}
	}

	// No fallback mock data — strictly reflect PostgreSQL database tickets state

	totalPages := int((totalCount + int64(perPage) - 1) / int64(perPage))
	if totalPages < 1 {
		totalPages = 1
	}
	startItem := int(offset) + 1
	endItem := int(offset) + len(tickets)
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
		BaseURL:     "/tickets",
	}

	data := struct {
		Title      string
		User       *auth.Claims
		Tickets    []TicketItem
		Pagination PaginationInfo
	}{
		Title:      "Onboarding Tickets",
		User:       claims,
		Tickets:    tickets,
		Pagination: pagination,
	}

	tmpl.ExecuteTemplate(w, "base", data)
}

type ApprovalItem struct {
	ID              string
	ServiceName     string
	CleanName       string
	PipelineName    string
	Domain          string
	Country         string
	TerraformPath   string
	TerraformExists bool
	TargetBranch    string
	Status          string
	ReviewComment   string
	ReviewedBy      string
	ReviewedAt      string
	RequestorEmail  string
	JiraID          string
	RepoURL         string
	HasAIAnalysis   bool
	AIAnalysisHTML  template.HTML
	AIAnalyzedAt    string
	TicketType      string
	TargetEnv       string
}

// RenderApprovalDashboard renders the DevOps approval screen dynamically linked to PostgreSQL Tickets & Service Catalog
func RenderApprovalDashboard(w http.ResponseWriter, r *http.Request) {
	tmpl, err := parsePage("approval_dashboard.html")
	if err != nil {
		http.Error(w, "Failed to load template: "+err.Error(), http.StatusInternalServerError)
		return
	}

	claims, _ := r.Context().Value(userCtxKey).(*auth.Claims)

	// Collect unique items to approve from PostgreSQL tickets as primary source
	type pendingSource struct {
		ID             string
		ServiceName    string
		Domain         string
		Country        string
		Status         string
		RequestorEmail string
		JiraID         string
		RepoURL        string
		CreatedAt      string
		TicketType     string
		TargetEnv      string
	}

	var sources []pendingSource
	seenServices := make(map[string]bool)

	if DBPool != nil {
		rows, err := DBPool.Query(r.Context(), `
			SELECT id, service_name, domain, country, status::text, jira_issue_id, repo_url, created_at, target_env::text, ticket_type::text
			FROM tickets
			ORDER BY created_at DESC
		`)
		if err == nil {
			defer rows.Close()
			for rows.Next() {
				var (
					tID                                            pgtype.UUID
					tSvc, tDomain, tCountry, tStatus, tRepo        string
					tJira, tTargetEnv, tTicketType                 pgtype.Text
					tCreatedAt                                     pgtype.Timestamptz
				)
				if errScan := rows.Scan(&tID, &tSvc, &tDomain, &tCountry, &tStatus, &tJira, &tRepo, &tCreatedAt, &tTargetEnv, &tTicketType); errScan == nil {
					idStr := fmt.Sprintf("%x-%x-%x-%x-%x", tID.Bytes[0:4], tID.Bytes[4:6], tID.Bytes[6:8], tID.Bytes[8:10], tID.Bytes[10:16])
					jiraID := ""
					if tJira.Valid {
						jiraID = tJira.String
					}
					targetEnv := "uat"
					if tTargetEnv.Valid && tTargetEnv.String != "" {
						targetEnv = tTargetEnv.String
					}
					ticketType := "ONBOARDING"
					if tTicketType.Valid && tTicketType.String != "" {
						ticketType = tTicketType.String
					}

					reqEmail := "admin@oona-insurance.com"
					if entry, found := ServiceCatalog.FindByNameOrID(tSvc); found {
						if entry.RequestorEmail != "" {
							reqEmail = entry.RequestorEmail
						}
						if entry.JiraID != "" && jiraID == "" {
							jiraID = entry.JiraID
						}
					}

					createdAtStr := time.Now().Format("2006-01-02 15:04")
					if tCreatedAt.Valid {
						createdAtStr = tCreatedAt.Time.Format("2006-01-02 15:04")
					}

					sources = append(sources, pendingSource{
						ID:             idStr,
						ServiceName:    tSvc,
						Domain:         tDomain,
						Country:        strings.ToUpper(tCountry),
						Status:         tStatus,
						RequestorEmail: reqEmail,
						JiraID:         jiraID,
						RepoURL:        tRepo,
						CreatedAt:      createdAtStr,
						TicketType:     ticketType,
						TargetEnv:      targetEnv,
					})
					seenServices[strings.ToLower(tSvc)] = true
				}
			}
		}
	}

	// Also add any ServiceCatalog entry not present in tickets
	for _, svc := range ServiceCatalog.ListAll() {
		if !seenServices[strings.ToLower(svc.Name)] {
			sources = append(sources, pendingSource{
				ID:             svc.ID,
				ServiceName:    svc.Name,
				Domain:         svc.Domain,
				Country:        svc.Country,
				Status:         svc.Status,
				RequestorEmail: svc.RequestorEmail,
				JiraID:         svc.JiraID,
				RepoURL:        svc.RepoURL,
				CreatedAt:      svc.CreatedAt,
			})
			seenServices[strings.ToLower(svc.Name)] = true
		}
	}

	approvalItems := make([]ApprovalItem, len(sources))
	var wg sync.WaitGroup
	wg.Add(len(sources))

	for idx, src := range sources {
		go func(i int, s pendingSource) {
			defer wg.Done()

			cleanName := strings.TrimSuffix(s.ServiceName, "-clone")
			cleanName = strings.TrimPrefix(cleanName, "lmd-oona-ph-integration-")
			cleanName = strings.TrimPrefix(cleanName, "lmd-oona-id-integration-")
			cleanName = strings.TrimPrefix(cleanName, "lmd-oona-")

			domainLower := strings.ToLower(s.Domain)
			countryLower := strings.ToLower(s.Country)
			if domainLower == "" {
				domainLower = "integration"
			}
			if countryLower == "" {
				countryLower = "ph"
			}

			tEnv := s.TargetEnv
			if tEnv == "" {
				tEnv = "uat"
			}
			pipelineName := fmt.Sprintf("lmd-oona-%s-%s-%s", countryLower, domainLower, cleanName)
			tfPath := fmt.Sprintf("02-app-setup/%s/%s/%s/services/%s", domainLower, countryLower, tEnv, cleanName)

			candidateBranches := []string{"main", "dev", "staging", "uat"}
			detectedBranch := "main"
			tfExists := false

			for _, br := range candidateBranches {
				resolvedP, found := ResolveTerraformPathForEnv(r.Context(), s.Domain, s.Country, tEnv, s.ServiceName, br)
				if found {
					tfExists = true
					detectedBranch = br
					tfPath = resolvedP
					tfRepoID := FetchExistingRepoIDFromTFVars(r.Context(), resolvedP, br)
					if tfRepoID != "" {
						pipelineName = tfRepoID
					}
					break
				}
			}

			itemStatus := "PENDING"
			if s.Status == "LIVE" || s.Status == "APPROVED" || s.Status == "JENKINS_READY" {
				itemStatus = "APPROVED"
			} else if s.Status == "REJECTED_SECURITY" || s.Status == "REJECTED" {
				itemStatus = "REJECTED"
			}

			itemComment := ""
			reviewedBy := "devops-engineer"
			reviewedAt := time.Now().Format("2006-01-02 15:04 MST")

			// Check if custom review was saved
			if rev, ok := TicketReviews.Get(s.ID); ok {
				itemStatus = rev.Status
				itemComment = rev.Comment
				if rev.ReviewedBy != "" {
					reviewedBy = rev.ReviewedBy
				}
				if !rev.ReviewedAt.IsZero() {
					reviewedAt = rev.ReviewedAt.Format("2006-01-02 15:04 MST")
				}
			}

			itemReq := "admin@oona-insurance.com"
			if s.RequestorEmail != "" {
				itemReq = s.RequestorEmail
			}

			itemJira := s.JiraID
			if itemJira == "" {
				itemJira = "-"
			}

			approvalItems[i] = ApprovalItem{
				ID:              s.ID,
				ServiceName:     s.ServiceName,
				CleanName:       cleanName,
				PipelineName:    pipelineName,
				Domain:          s.Domain,
				Country:         s.Country,
				TerraformPath:   tfPath,
				TerraformExists: tfExists,
				TargetBranch:    detectedBranch,
				Status:          itemStatus,
				ReviewComment:   itemComment,
				ReviewedBy:      reviewedBy,
				ReviewedAt:      reviewedAt,
				RequestorEmail:  itemReq,
				JiraID:          itemJira,
				RepoURL:         s.RepoURL,
				TicketType:      s.TicketType,
				TargetEnv:       s.TargetEnv,
			}
		}(idx, src)
	}

	wg.Wait()

	data := struct {
		Title         string
		User          *auth.Claims
		ApprovalItems []ApprovalItem
	}{
		Title:         "DevOps Approvals",
		User:          claims,
		ApprovalItems: approvalItems,
	}

	tmpl.ExecuteTemplate(w, "base", data)
}

type VulnInfo struct {
	TargetFile       string `json:"TargetFile"`
	VulnerabilityID  string `json:"VulnerabilityID"`
	PkgName          string `json:"PkgName"`
	InstalledVersion string `json:"InstalledVersion"`
	FixedVersion     string `json:"FixedVersion"`
	Severity         string `json:"Severity"`
	Title            string `json:"Title"`
	PrimaryURL       string `json:"PrimaryURL"`
}

func (v VulnInfo) OfficialURL() string {
	if v.PrimaryURL != "" {
		return v.PrimaryURL
	}
	if strings.HasPrefix(v.VulnerabilityID, "CVE-") {
		return "https://nvd.nist.gov/vuln/detail/" + v.VulnerabilityID
	}
	if strings.HasPrefix(v.VulnerabilityID, "GHSA-") {
		return "https://github.com/advisories/" + v.VulnerabilityID
	}
	return "https://avd.aquasec.com/nvd/" + strings.ToLower(v.VulnerabilityID)
}

type VulnSummary struct {
	Critical  int
	High      int
	Medium    int
	Low       int
	Total     int
	ScannedAt string
	IsCached  bool
}

// RefreshBranchesHandler forces a re-fetch of live branches and flushes all terraform caches.
// Used by the manual refresh button next to the Branch selector in the approval detail page.
func RefreshBranchesHandler(w http.ResponseWriter, r *http.Request) {
	FlushTerraformPathCache()
	FlushTFVarsContentCache()
	branches := RefreshLiveTerraformBranches(r.Context())
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]interface{}{
		"branches": branches,
		"count":    len(branches),
	})
}

// ResolveLocalServiceRepoPath finds the local filesystem directory for a service repo across standard mount paths
func ResolveLocalServiceRepoPath(serviceName, repoURL string) string {
	cleanServiceName := strings.TrimSuffix(serviceName, "-clone")
	repoNameFromURL := JenkinsJobNameFromRepoID(repoURL)

	baseDirs := []string{
		os.Getenv("SERVICE_REPOS_DIR"),
		"/repo/PH",
		"/repo/ID",
		"/repo/demo",
		"/repo/Partner",
		"/repo",
		"/service-repos",
		"../repo/PH",
		"../repo/ID",
		"../repo/demo",
		"../repo",
		"/Users/novanhariman/Documents/Ngulik/repo/PH",
		"/Users/novanhariman/Documents/Ngulik/repo/ID",
		"/Users/novanhariman/Documents/Ngulik/repo",
		"/Users/novanhariman/Documents/oona/repo/PH",
		"/Users/novanhariman/Documents/oona/repo/ID",
		"/Users/novanhariman/Documents/oona/repo",
	}

	nameCandidates := []string{
		repoNameFromURL,
		serviceName,
		cleanServiceName,
		"lmd-oona-ph-integration-" + serviceName,
		"lmd-oona-ph-integration-" + cleanServiceName,
		"lmd-oona-id-integration-" + serviceName,
		"lmd-oona-id-integration-" + cleanServiceName,
		"lmd-oona-" + serviceName,
		"lmd-oona-" + cleanServiceName,
	}

	for _, base := range baseDirs {
		if base == "" {
			continue
		}
		for _, name := range nameCandidates {
			if name == "" {
				continue
			}
			p := filepath.Join(base, name)
			if fi, err := os.Stat(p); err == nil && fi.IsDir() {
				return p
			}
		}
	}
	return ""
}

// FunctionAuditItem holds reconciled function specifications comparing package.json vs terraform.tfvars
type FunctionAuditItem struct {
	AWSName         string `json:"aws_name"`
	PkgKey          string `json:"pkg_key"`
	TFKey           string `json:"tf_key"`
	PkgHandler      string `json:"pkg_handler"`
	TFHandler       string `json:"tf_handler"`
	Runtime         string `json:"runtime"`
	MemorySize      int    `json:"memory_size"`
	Timeout         int    `json:"timeout"`
	VPCAttach       bool   `json:"vpc_attach"`
	APIGatewayRoute string `json:"api_gateway_route"`
	InSync          bool   `json:"in_sync"`
	StatusBadge     string `json:"status_badge"` // "IN_SYNC", "HANDLER_DRIFT", "MISSING_IN_TF", "MISSING_IN_PKG"
	StatusMessage   string `json:"status_message"`
}

// ManifestAuditSummary summarizes the cross-verification between application package.json and IaC terraform.tfvars
type ManifestAuditSummary struct {
	TotalFunctions int                 `json:"total_functions"`
	InSyncCount    int                 `json:"in_sync_count"`
	HasDrift       bool                `json:"has_drift"`
	DriftWarnings  []string            `json:"drift_warnings"`
	Items          []FunctionAuditItem `json:"items"`
}

// AuditManifestVsTerraform cross-checks declared functions in package.json against functions in terraform.tfvars
func AuditManifestVsTerraform(
	pkgFuncs []LambdaFunctionRef,
	tfvarsContent string,
	country, domain, targetEnv, cleanName string,
) ManifestAuditSummary {
	countryLower := strings.ToLower(country)
	domainLower := strings.ToLower(domain)
	envLower := strings.ToLower(targetEnv)

	tfServiceName := infra.ExtractTFVarValueFromHCL([]byte(tfvarsContent), "service_name")
	if tfServiceName == "" {
		tfServiceName = cleanName
	}

	tfFuncMap := infra.ParseAllFunctionConfigsFromHCL([]byte(tfvarsContent))
	if tfFuncMap == nil {
		tfFuncMap = make(map[string]infra.FunctionConfig)
	}

	matchedTFKeys := make(map[string]bool)
	var items []FunctionAuditItem
	var warnings []string

	// 1. Process all functions declared in package.json
	for _, pf := range pkgFuncs {
		item := FunctionAuditItem{
			AWSName:    pf.Name,
			PkgKey:     pf.Key,
			PkgHandler: pf.Handler,
			Runtime:    "nodejs24.x",
			MemorySize: 256,
			Timeout:    30,
		}

		var foundTFKey string
		var tfCfg infra.FunctionConfig

		for tfKey, cfg := range tfFuncMap {
			expectedName := fmt.Sprintf("%s-%s-%s-%s-%s", countryLower, domainLower, envLower, tfServiceName, tfKey)
			if pf.Name == expectedName || pf.Key == tfKey || strings.HasSuffix(pf.Name, tfKey) {
				foundTFKey = tfKey
				tfCfg = cfg
				break
			}
		}

		if foundTFKey != "" {
			matchedTFKeys[foundTFKey] = true
			item.TFKey = foundTFKey
			item.TFHandler = tfCfg.Handler
			if tfCfg.Runtime != "" {
				item.Runtime = tfCfg.Runtime
			}
			if tfCfg.MemorySize > 0 {
				item.MemorySize = tfCfg.MemorySize
			}
			if tfCfg.Timeout > 0 {
				item.Timeout = tfCfg.Timeout
			}
			item.VPCAttach = tfCfg.VPCAttach
			if tfCfg.TriggerMethod != "" {
				item.APIGatewayRoute = fmt.Sprintf("%s %s", tfCfg.TriggerMethod, tfCfg.TriggerPath)
			} else if len(tfCfg.APIGatewayARNs) > 0 {
				item.APIGatewayRoute = "API Gateway Triggered"
			}

			// Check handler drift
			if item.PkgHandler != "" && item.TFHandler != "" && item.PkgHandler != item.TFHandler {
				item.InSync = false
				item.StatusBadge = "HANDLER_DRIFT"
				item.StatusMessage = fmt.Sprintf("Handler mismatch: package.json (%s) vs terraform (%s)", item.PkgHandler, item.TFHandler)
				warnings = append(warnings, fmt.Sprintf("%s: handler mismatch (%s vs %s)", pf.Key, item.PkgHandler, item.TFHandler))
			} else {
				item.InSync = true
				item.StatusBadge = "IN_SYNC"
				item.StatusMessage = "Naming & handler in sync with terraform.tfvars"
			}
		} else {
			item.InSync = false
			item.StatusBadge = "MISSING_IN_TF"
			item.StatusMessage = "Declared in package.json but not provisioned in terraform.tfvars functions map"
			warnings = append(warnings, fmt.Sprintf("%s: missing in terraform.tfvars", pf.Key))
		}

		items = append(items, item)
	}

	// 2. Check any remaining functions defined in terraform.tfvars that were not in package.json
	for tfKey, cfg := range tfFuncMap {
		if matchedTFKeys[tfKey] {
			continue
		}
		expectedName := fmt.Sprintf("%s-%s-%s-%s-%s", countryLower, domainLower, envLower, tfServiceName, tfKey)
		item := FunctionAuditItem{
			AWSName:       expectedName,
			TFKey:         tfKey,
			TFHandler:     cfg.Handler,
			Runtime:       cfg.Runtime,
			MemorySize:    cfg.MemorySize,
			Timeout:       cfg.Timeout,
			VPCAttach:     cfg.VPCAttach,
			InSync:        false,
			StatusBadge:   "MISSING_IN_PKG",
			StatusMessage: "Defined in terraform.tfvars but not declared in package.json manifest",
		}
		if cfg.TriggerMethod != "" {
			item.APIGatewayRoute = fmt.Sprintf("%s %s", cfg.TriggerMethod, cfg.TriggerPath)
		}
		warnings = append(warnings, fmt.Sprintf("%s: defined in terraform.tfvars but missing in package.json", tfKey))
		items = append(items, item)
	}

	// 3. Fallback for single-function (legacy without functions map in either)
	if len(items) == 0 {
		defaultName := fmt.Sprintf("lmd-oona-%s-%s-%s", countryLower, domainLower, cleanName)
		items = append(items, FunctionAuditItem{
			AWSName:       defaultName,
			PkgKey:        "default",
			TFKey:         "default",
			Runtime:       "nodejs24.x",
			MemorySize:    256,
			Timeout:       30,
			InSync:        true,
			StatusBadge:   "IN_SYNC",
			StatusMessage: "Single standard Lambda function",
		})
	}

	inSyncCount := 0
	for _, it := range items {
		if it.InSync {
			inSyncCount++
		}
	}

	return ManifestAuditSummary{
		TotalFunctions: len(items),
		InSyncCount:    inSyncCount,
		HasDrift:       len(warnings) > 0,
		DriftWarnings:  warnings,
		Items:          items,
	}
}

// RenderApprovalDetail renders the dedicated full-page DevOps review workspace for a specific ticket/service
func RenderApprovalDetail(w http.ResponseWriter, r *http.Request) {
	tmpl, err := parsePage("approval_detail.html")
	if err != nil {
		http.Error(w, "Failed to load template: "+err.Error(), http.StatusInternalServerError)
		return
	}

	claims, _ := r.Context().Value(userCtxKey).(*auth.Claims)
	ticketIDParam := chi.URLParam(r, "id")

	var (
		serviceName    string
		domain         = "Integration"
		country        = "PH"
		status         = "PENDING"
		reqEmail       = "admin@oona-insurance.com"
		jiraID         = "-"
		repoURL        = "https://github.com/oona-insurance/lmd-oona-ph-integration-health-renewal-svc-clone"
		reviewComment  = ""
		reviewedBy     = "devops-engineer"
		reviewedAt     = time.Now().Format("2006-01-02 15:04 MST")
		actualTicketID = ticketIDParam
		hasAIAnalysis  = false
		aiAnalysisHTML = template.HTML("")
		aiAnalyzedAt   = ""
		ticketType     = "ONBOARDING"
		targetEnv      = "uat"
	)

	// 1. Try finding ticket from PostgreSQL DB
	if DB != nil {
		var ticketUUID pgtype.UUID
		if errUUID := ticketUUID.Scan(ticketIDParam); errUUID == nil {
			if t, errGet := DB.GetTicketByID(r.Context(), ticketUUID); errGet == nil {
				serviceName = t.ServiceName
				domain = t.Domain
				country = strings.ToUpper(t.Country)
				status = string(t.Status)
				if t.JiraIssueID.Valid && t.JiraIssueID.String != "" {
					jiraID = t.JiraIssueID.String
				}
				repoURL = t.RepoUrl
				if t.AiAnalysis.Valid && len(t.AiAnalysis.String) > 0 {
					hasAIAnalysis = true
					aiAnalysisHTML = template.HTML(t.AiAnalysis.String)
					if t.AiAnalyzedAt.Valid {
						aiAnalyzedAt = t.AiAnalyzedAt.Time.Format("2006-01-02 15:04 MST")
					}
				}

				if t.TargetEnv != "" {
					targetEnv = strings.ToLower(t.TargetEnv)
				}
				if t.TicketType != "" {
					ticketType = t.TicketType
				}
			}
		}
	}

	// 2. Fallback to ServiceCatalog if not found or by service name
	if serviceName == "" {
		if entry, found := ServiceCatalog.FindByNameOrID(ticketIDParam); found {
			serviceName = entry.Name
			domain = entry.Domain
			country = entry.Country
			status = entry.Status
			reqEmail = entry.RequestorEmail
			if entry.JiraID != "" {
				jiraID = entry.JiraID
			}
			repoURL = entry.RepoURL
			actualTicketID = entry.ID
		} else {
			serviceName = ticketIDParam
		}
	}

	cleanName := strings.TrimSuffix(serviceName, "-clone")
	cleanName = strings.TrimPrefix(cleanName, "lmd-oona-ph-integration-")
	cleanName = strings.TrimPrefix(cleanName, "lmd-oona-id-integration-")
	cleanName = strings.TrimPrefix(cleanName, "lmd-oona-")

	domainLower := strings.ToLower(domain)
	countryLower := strings.ToLower(country)
	if domainLower == "" {
		domainLower = "integration"
	}
	if countryLower == "" {
		countryLower = "ph"
	}

	pipelineName := fmt.Sprintf("lmd-oona-%s-%s-%s", countryLower, domainLower, cleanName)
	tfPath := fmt.Sprintf("02-app-setup/%s/%s/%s/services/%s", domainLower, countryLower, targetEnv, cleanName)

	candidateBranches := []string{"main", "dev", "staging", "uat"}
	detectedBranch := "main"
	tfExists := false

	for _, br := range candidateBranches {
		resolvedP, found := ResolveTerraformPathForEnv(r.Context(), domain, country, targetEnv, serviceName, br)
		if found {
			tfExists = true
			detectedBranch = br
			tfPath = resolvedP
			tfRepoID := FetchExistingRepoIDFromTFVars(r.Context(), resolvedP, br)
			if tfRepoID != "" {
				pipelineName = tfRepoID
			}
			break
		}
	}

	itemStatus := "PENDING"
	if status == "LIVE" || status == "APPROVED" || status == "JENKINS_READY" {
		itemStatus = "APPROVED"
	} else if status == "REJECTED_SECURITY" || status == "REJECTED" {
		itemStatus = "REJECTED"
	}

	if rev, ok := TicketReviews.Get(actualTicketID); ok {
		itemStatus = rev.Status
		reviewComment = rev.Comment
		if rev.ReviewedBy != "" {
			reviewedBy = rev.ReviewedBy
		}
		if !rev.ReviewedAt.IsZero() {
			reviewedAt = rev.ReviewedAt.Format("2006-01-02 15:04 MST")
		}
	}

	approvalItem := ApprovalItem{
		ID:              actualTicketID,
		ServiceName:     serviceName,
		CleanName:       cleanName,
		PipelineName:    pipelineName,
		Domain:          domain,
		Country:         country,
		TerraformPath:   tfPath,
		TerraformExists: tfExists,
		TargetBranch:    detectedBranch,
		Status:          itemStatus,
		ReviewComment:   reviewComment,
		ReviewedBy:      reviewedBy,
		ReviewedAt:      reviewedAt,
		RequestorEmail:  reqEmail,
		JiraID:          jiraID,
		RepoURL:         repoURL,
		HasAIAnalysis:   hasAIAnalysis,
		AIAnalysisHTML:  aiAnalysisHTML,
		AIAnalyzedAt:    aiAnalyzedAt,
		TicketType:      ticketType,
		TargetEnv:       targetEnv,
	}

	// 3. Read live terraform.tfvars content
	tfvarsContent := FetchTFVarsContent(r.Context(), tfPath, detectedBranch)
	if tfvarsContent == "" {
		// Fallback check local file directly
		localDirs := []string{
			os.Getenv("TERRAFORM_IAC_DIR"),
			"/terraform-iac",
			"../oona-dtc-country-terraform-iac",
			"/Users/novanhariman/Documents/oona/oona-dtc-country-terraform-iac",
		}
		for _, d := range localDirs {
			if d == "" {
				continue
			}
			candidateFile := filepath.Join(d, tfPath, "terraform.tfvars")
			if b, err := os.ReadFile(candidateFile); err == nil && len(b) > 0 {
				tfvarsContent = string(b)
				break
			}
		}
	}
	if tfvarsContent == "" {
		tfvarsContent = "# terraform.tfvars is not yet provisioned in " + tfPath + "\n# Push configuration to GitHub repository to link automatically."
	}

	// 4. Fetch Real Trivy Vulnerability Findings (with Valkey/Memory Cache check first)
	var vulnList []VulnInfo
	var summary VulnSummary

	cleanServiceName := strings.TrimSuffix(serviceName, "-clone")
	cacheKeys := []string{
		"trivy:json:" + serviceName + ":" + detectedBranch,
		"trivy:json:" + cleanServiceName + ":" + detectedBranch,
		"trivy:json:lmd-oona-ph-integration-" + cleanServiceName + ":" + detectedBranch,
		"trivy:json:lmd-oona-ph-integration-" + serviceName + ":" + detectedBranch,
		"trivy:json:lmd-oona-id-integration-" + cleanServiceName + ":" + detectedBranch,
	}

	var cachedJSON string
	rdb := getTrivyRedisClient()
	if rdb != nil {
		for _, ck := range cacheKeys {
			if val, errGet := rdb.Get(r.Context(), ck).Result(); errGet == nil && val != "" {
				cachedJSON = val
				break
			}
		}
	}

	var outputData []byte
	if cachedJSON != "" {
		outputData = []byte(cachedJSON)
	} else {
		// Scan with generous timeout to ensure local Trivy finishes reliably
		scanCtx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()

		localPath := ResolveLocalServiceRepoPath(serviceName, repoURL)

		var cmd *exec.Cmd
		if localPath != "" {
			cmd = exec.CommandContext(scanCtx, "trivy", "fs", "--scanners", "vuln", "--skip-db-update", "--quiet", "--format", "json", localPath)
		} else {
			cmd = exec.CommandContext(scanCtx, "trivy", "repo", "--scanners", "vuln", "--skip-db-update", "--quiet", "--branch", detectedBranch, "--format", "json", "--", repoURL)
			githubToken := GetActiveGitHubToken(r.Context())
			if githubToken != "" {
				cmd.Env = append(os.Environ(), "GITHUB_TOKEN="+githubToken)
			}
		}

		if out, errCmd := cmd.Output(); errCmd == nil && len(out) > 0 {
			outputData = out
			if rdb != nil {
				for _, ck := range cacheKeys {
					_ = rdb.Set(r.Context(), ck, string(out), 30*time.Minute).Err()
				}
			}
		}
	}

	wibLoc := time.FixedZone("WIB", 7*3600)
	if cachedJSON != "" {
		summary.IsCached = true
	}

	if len(outputData) > 0 {
		var report struct {
			CreatedAt string `json:"CreatedAt"`
			Results   []struct {
				Target          string     `json:"Target"`
				Vulnerabilities []VulnInfo `json:"Vulnerabilities"`
			} `json:"Results"`
		}
		if errJSON := json.Unmarshal(outputData, &report); errJSON == nil {
			if report.CreatedAt != "" {
				if t, errT := time.Parse(time.RFC3339, report.CreatedAt); errT == nil {
					summary.ScannedAt = t.In(wibLoc).Format("2006-01-02 15:04 WIB")
				}
			}
			for _, res := range report.Results {
				for _, v := range res.Vulnerabilities {
					v.TargetFile = res.Target
					vulnList = append(vulnList, v)
					switch v.Severity {
					case "CRITICAL":
						summary.Critical++
					case "HIGH":
						summary.High++
					case "MEDIUM":
						summary.Medium++
					case "LOW":
						summary.Low++
					}
					summary.Total++
				}
			}
		}
	}

	if summary.ScannedAt == "" && len(outputData) > 0 {
		summary.ScannedAt = time.Now().In(wibLoc).Format("2006-01-02 15:04 WIB")
	}
	if summary.ScannedAt == "" {
		summary.ScannedAt = "Not scanned yet"
	}

	availableBranches := FetchLiveTerraformBranches(r.Context())
	// Always ensure the currently detected/target branch appears in the list
	hasTargetBranch := false
	for _, b := range availableBranches {
		if b == detectedBranch {
			hasTargetBranch = true
			break
		}
	}
	if !hasTargetBranch {
		availableBranches = append([]string{detectedBranch}, availableBranches...)
	}

	repoLookupName := JenkinsJobNameFromRepoID(repoURL)
	if repoLookupName == "" {
		repoLookupName = pipelineName
	}
	declaredFunctions := FetchLambdaFunctionsForRepo(r.Context(), repoLookupName)
	if len(declaredFunctions) == 0 && serviceName != "" {
		declaredFunctions = FetchLambdaFunctionsForRepo(r.Context(), serviceName)
	}
	targetAccountID := ResolveAWSAccountID(country, domain)
	isMultiFunction := len(declaredFunctions) > 1

	auditReport := AuditManifestVsTerraform(declaredFunctions, tfvarsContent, country, domain, targetEnv, cleanName)

	data := struct {
		Title             string
		User              *auth.Claims
		Item              ApprovalItem
		AvailableBranches []string
		TFVarsContent     string
		VulnList          []VulnInfo
		VulnSummary       VulnSummary
		Functions         []LambdaFunctionRef
		IsMultiFunction   bool
		TargetAccountID   string
		Audit             ManifestAuditSummary
	}{
		Title:             serviceName + " - DevOps Approval Review",
		User:              claims,
		Item:              approvalItem,
		AvailableBranches: availableBranches,
		TFVarsContent:     tfvarsContent,
		VulnList:          vulnList,
		VulnSummary:       summary,
		Functions:         declaredFunctions,
		IsMultiFunction:   isMultiFunction,
		TargetAccountID:   targetAccountID,
		Audit:             auditReport,
	}

	tmpl.ExecuteTemplate(w, "base", data)
}

// Integration represents the data model for UI
