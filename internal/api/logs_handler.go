// ... continuing inside views.go ...
package api

import (
	"context"
	"fmt"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/oona-insurance/dev-portal/internal/auth"
	db "github.com/oona-insurance/dev-portal/internal/repository/postgres/generated"
	"github.com/oona-insurance/dev-portal/internal/worker/ci"
)

// JenkinsLogsHandler is an HTMX endpoint to fetch and display Jenkins build logs
func JenkinsLogsHandler(w http.ResponseWriter, r *http.Request) {
	serviceName := chi.URLParam(r, "service")
	if serviceName == "" {
		serviceName = chi.URLParam(r, "id")
	}

	branch := strings.TrimSpace(r.URL.Query().Get("branch"))
	if branch == "" {
		branch = "uat"
	}
	
	// Default integration configuration
	cfg := ci.IntegrationConfig{
		BaseURL:   "https://automation.oona-insurance.com",
		AuthUser:  "svc_oona_portal",
		AuthToken: "DUMMY_TOKEN_FROM_DB", 
	}

	domain, country, savedPipeline := "integration", "ph", ""
	if entry, found := ServiceCatalog.FindByNameOrID(serviceName); found {
		if entry.Name != "" {
			serviceName = entry.Name
		}
		if entry.Domain != "" {
			domain = entry.Domain
		}
		if entry.Country != "" {
			country = entry.Country
		}
		savedPipeline = entry.PipelineName
	}

	if DB != nil {
		// Lookup active integrations from DB for Jenkins credentials
		integrations, err := DB.GetActiveIntegrations(r.Context())
		if err == nil {
			for _, ig := range integrations {
				if ig.Provider == db.IntegrationProviderJenkins {
					cfg.BaseURL = ig.BaseUrl
					if ig.AuthUser.Valid {
						cfg.AuthUser = ig.AuthUser.String
					}
					if decryptedToken, err := auth.Decrypt(ig.AuthToken); err == nil && decryptedToken != "" {
						cfg.AuthToken = decryptedToken
					} else if ig.AuthToken != "" {
						cfg.AuthToken = ig.AuthToken
					}
					break
				}
			}
		}

		tickets, err := DB.ListTickets(r.Context())
		if err == nil {
			for _, t := range tickets {
				if strings.EqualFold(t.ServiceName, serviceName) {
					if t.Domain != "" {
						domain = t.Domain
					}
					if t.Country != "" {
						country = t.Country
					}
					if t.PipelineName != "" {
						savedPipeline = t.PipelineName
					}
					break
				}
			}
		}
	}

	pipelineName := ResolveCanonicalPipelineName(r.Context(), domain, country, serviceName, "main", savedPipeline)
	
	jenkins := ci.NewJenkinsEngine(cfg)

	// Fetch tail logs (last 500 lines) for the selected branch
	styledLogs, err := jenkins.FetchJenkinsLogs(context.Background(), pipelineName, branch, 500)
	
	if err != nil {
		styledLogs = fmt.Sprintf("<span class='text-red-400'>Error retrieving logs for branch '%s': %v</span>", branch, err)
	}

	uatSel, mainSel, devSel, stagingSel := "", "", "", ""
	switch branch {
	case "main":
		mainSel = "selected"
	case "dev":
		devSel = "selected"
	case "staging":
		stagingSel = "selected"
	default:
		uatSel = "selected"
	}

	html := fmt.Sprintf(`
		<div class="bg-slate-950 rounded-xl overflow-hidden shadow-inner ring-1 ring-white/10">
			<div class="px-4 py-3 bg-slate-900 border-b border-slate-800 flex justify-between items-center flex-wrap gap-2">
				<div class="flex items-center space-x-2">
					<div class="w-3 h-3 rounded-full bg-red-500"></div>
					<div class="w-3 h-3 rounded-full bg-yellow-500"></div>
					<div class="w-3 h-3 rounded-full bg-green-500"></div>
					<span class="ml-2 text-xs font-mono text-slate-300">
						console &middot; <span class="text-teal-400 font-bold">Jenkins: %s</span> &middot; branch: <span class="text-amber-400 font-bold">%s</span>
					</span>
				</div>
				<div class="flex items-center space-x-3">
					<select 
						hx-get="/api/v1/catalog/%s/logs" 
						hx-target="#logs-container" 
						name="branch" 
						class="bg-slate-800 text-xs text-slate-200 border border-slate-700 rounded px-2 py-1 focus:ring-1 focus:ring-teal-500">
						<option value="uat" %s>Branch: uat</option>
						<option value="main" %s>Branch: main</option>
						<option value="dev" %s>Branch: dev</option>
						<option value="staging" %s>Branch: staging</option>
					</select>
					<button 
						hx-get="/api/v1/catalog/%s/logs?branch=%s" 
						hx-target="#logs-container" 
						class="text-xs text-teal-400 hover:text-teal-300 flex items-center bg-slate-800 px-2.5 py-1 rounded border border-slate-700">
						<svg class="w-3 h-3 mr-1" fill="none" stroke="currentColor" viewBox="0 0 24 24"><path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M4 4v5h.582m15.356 2A8.001 8.001 0 004.582 9m0 0H9m11 11v-5h-.581m0 0a8.003 8.003 0 01-15.357-2m15.357 2H15"></path></svg>
						Refresh
					</button>
				</div>
			</div>
			<div class="p-4 overflow-x-auto max-h-[500px] overflow-y-auto">
				<pre class="text-xs font-mono text-gray-300 whitespace-pre-wrap">%s</pre>
			</div>
		</div>
	`, pipelineName, branch, serviceName, uatSel, mainSel, devSel, stagingSel, serviceName, branch, styledLogs)

	w.Header().Set("Content-Type", "text/html")
	w.Write([]byte(html))
}
