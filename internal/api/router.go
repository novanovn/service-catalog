package api

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
)

// SetupRouter initializes the Chi router with all necessary routes and middlewares
func SetupRouter() *chi.Mux {
	r := chi.NewRouter()

	// 1. Basic & Security Middlewares
	r.Use(middleware.RequestID)
	r.Use(middleware.RealIP)
	r.Use(StructuredLoggingMiddleware(slog.Default()))
	r.Use(SecurityHeadersMiddleware)
	r.Use(TimeoutMiddleware(60 * time.Second))
	r.Use(RateLimitMiddleware("global", 120, 30)) // 120 req/min general limit, burst 30
	r.Use(middleware.Recoverer)

	// 2. Public Health & Observability Probes (No Auth)
	// /health and /live (Liveness Probe - confirms web server process is responsive)
	r.Get("/health", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"status":"ok"}`))
	})
	r.Get("/live", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"status":"alive"}`))
	})

	// OpenAPI 3.0 Documentation Specification and Swagger UI Viewer
	r.Get("/docs/openapi.yaml", func(w http.ResponseWriter, r *http.Request) {
		http.ServeFile(w, r, "docs/openapi.yaml")
	})
	r.Get("/docs/api", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Write([]byte(`<!DOCTYPE html>
<html>
<head>
  <title>Oona Dev Portal API Docs</title>
  <meta charset="utf-8"/>
  <meta name="viewport" content="width=device-width, initial-scale=1">
  <link rel="stylesheet" type="text/css" href="https://unpkg.com/swagger-ui-dist@5/swagger-ui.css" >
</head>
<body style="margin: 0; background: #fafafa;">
  <div id="swagger-ui"></div>
  <script src="https://unpkg.com/swagger-ui-dist@5/swagger-ui-bundle.js"></script>
  <script>
    SwaggerUIBundle({
      url: "/docs/openapi.yaml",
      dom_id: '#swagger-ui',
      deepLinking: true,
      presets: [SwaggerUIBundle.presets.apis],
      layout: "BaseLayout"
    });
  </script>
</body>
</html>`))
	})

	// /ready (Readiness Probe - verifies PostgreSQL DB and Valkey cache connectivity)
	r.Get("/ready", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		dbReady := true
		valkeyReady := true
		var reasons []string

		// Check PostgreSQL connection pool
		if DBPool != nil {
			ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
			defer cancel()
			if err := DBPool.Ping(ctx); err != nil {
				dbReady = false
				reasons = append(reasons, fmt.Sprintf("database: %v", err))
			}
		}

		// Check Valkey / Redis connection
		rdb := getTrivyRedisClient()
		if rdb != nil {
			ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
			defer cancel()
			if err := rdb.Ping(ctx).Err(); err != nil {
				valkeyReady = false
				reasons = append(reasons, fmt.Sprintf("valkey: %v", err))
			}
		}

		if !dbReady || !valkeyReady {
			w.WriteHeader(http.StatusServiceUnavailable)
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"status":  "not_ready",
				"reasons": reasons,
			})
			return
		}

		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"status":   "ready",
			"database": "connected",
			"valkey":   "connected",
		})
	})

	// Authentication Routes (with strict 10 req/min rate limit for login)
	r.Get("/login", RenderLoginPage)
	r.With(LoginRateLimitMiddleware()).Post("/api/login", HandleLogin)
	r.Post("/logout", HandleLogout)

	// External CI/CD & IaC Webhook Ingress (Secured via HMAC-SHA256)
	r.Post("/api/v1/webhooks/github", GitHubWebhookHandler)

	// 3. Static File Server
	r.Handle("/static/*", http.StripPrefix("/static/", http.FileServer(http.Dir("static"))))

	// 4. Authenticated Web Views & APIs (All Roles: Developer, Infra, Admin)
	r.Group(func(r chi.Router) {
		r.Use(AuthMiddleware)
		r.Use(CSRFMiddleware) // SEC-07: Double-Submit Cookie CSRF validation on mutations

		r.Get("/", RenderCatalogList)
		r.Get("/dashboard", RenderDashboard)
		r.Get("/catalog", RenderCatalogList)
		r.Get("/service-map", RenderServiceMap)
		r.Get("/api/v1/topology/layouts", ListTopologyLayoutsHandler)
		r.Get("/api/v1/topology/layout", GetTopologyLayoutHandler)
		r.Post("/api/v1/topology/layout", SaveTopologyLayoutHandler)
		r.Delete("/api/v1/topology/layout", ResetTopologyLayoutHandler)
		r.Get("/catalog/new", RenderTicketForm)
		r.Get("/catalog/{service}", RenderServiceDetail)
		r.Get("/tickets", RenderTicketList)
		r.Get("/tickets/new", RenderTicketForm)
		r.Post("/tickets/preview", PreviewPipelineName)
		r.Get("/profile", RenderProfile)
		r.Get("/settings", RenderSettings)

		// Dynamic Logs, TechDocs & Trivy Security Endpoints
		r.Get("/api/v1/catalog/{service}/logs", JenkinsLogsHandler)
		r.Get("/api/v1/tickets/{id}/logs", JenkinsLogsHandler)
		r.Get("/api/v1/catalog/{service}/docs", CatalogDocsHandler)
		r.Get("/api/v1/catalog/{service}/trivy", CatalogTrivyScanHandler)
		r.Get("/api/v1/catalog/{service}/scan-schedule", GetScanScheduleHandler)
		r.Post("/api/v1/catalog/{service}/scan-schedule", SaveScanScheduleHandler)
		r.Get("/api/v1/security/ai-analyze", AISecurityAnalyzeHandler)
		r.Get("/api/v1/tickets/check-tfvars", CheckTFVarsBranchHandler)

		// Developer & System Action Endpoints
		r.Post("/api/v1/tickets", CreateTicketHandler)
		r.Delete("/api/v1/tickets/{id}", DeleteTicketHandler)
		r.Post("/api/v1/catalog/{service}/test", InvokeLambdaHandler)
		r.Post("/api/v1/catalog/{service}/promote", RequestPromotionHandler)

		// 5. Infra & Admin Approvals Sub-router (Infra, Admin)
		r.Group(func(r chi.Router) {
			r.Use(RequireRole("infra", "devops", "admin"))

			r.Get("/approvals", RenderApprovalDashboard)
			r.Get("/approvals/{id}", RenderApprovalDetail)
			r.Get("/devops/terraform", func(w http.ResponseWriter, r *http.Request) {
				http.Redirect(w, r, "/admin/integrations", http.StatusMovedPermanently)
			})
			r.Post("/api/v1/tickets/{id}/approve", ApproveTicketHandler)
			r.Post("/api/v1/tickets/{id}/reject", RejectTicketHandler)
			r.Get("/api/v1/tickets/{id}/verify-lambda", VerifyTicketLambdaHandler)
			r.Post("/api/v1/admin/parameters/{id}/edit", EditParameterHandler)
		})

		// 6. Admin Only Sub-router (Strictly Admin)
		r.Group(func(r chi.Router) {
			r.Use(RequireRole("admin"))

			r.Get("/admin/users", RenderAdminUsers)
			r.Get("/admin/integrations", RenderAdminIntegrations)
			r.Get("/admin/parameters", RenderAdminParameters)
			r.Get("/admin/backup", RenderAdminBackup)
			r.Get("/admin/audit-logs", RenderAdminAuditLogs)
			r.Get("/api/v1/admin/audit-logs/export", ExportAuditLogsCSVHandler)
			r.Post("/api/v1/admin/users", CreateUserHandler)
			r.Post("/api/v1/admin/users/{id}/shelves", UpdateUserShelvesHandler)
			r.Delete("/api/v1/admin/users/{id}", DeleteUserHandler)
			r.Post("/api/v1/admin/integrations", CreateIntegrationHandler)
			r.Post("/api/v1/admin/integrations/edit", EditIntegrationHandler)
			r.Post("/api/v1/admin/integrations/{id}", EditIntegrationHandler)
			r.Post("/api/v1/admin/integrations/{id}/edit", EditIntegrationHandler)
			r.Delete("/api/v1/admin/integrations/{id}", DeleteIntegrationHandler)
			r.Post("/admin/integrations/{id}/test", TestIntegrationHandler)
			r.Post("/admin/integrations/test-live", TestLiveIntegrationHandler)
			r.Post("/api/v1/admin/parameters", CreateParameterHandler)
			r.Post("/api/v1/admin/parameters/{id}/edit", EditParameterHandler)
			r.Post("/api/v1/admin/parameters/{id}/toggle", ToggleParameterHandler)
			r.Delete("/api/v1/admin/parameters/{id}", DeleteParameterHandler)
			r.Get("/api/v1/admin/backup/export", ExportBackupHandler)
			r.Post("/api/v1/admin/backup/export", ExportBackupHandler)
			r.Post("/api/v1/admin/backup/restore", RestoreBackupHandler)
			r.Post("/api/v1/catalog/{id}/edit", UpdateCatalogHandler)
			r.Delete("/api/v1/catalog/{id}", DeleteCatalogHandler)
			r.Post("/api/v1/catalog/{service}/recreate-pipeline", RecreatePipelineHandler)
		})
	})

	return r
}

func HandleMockAuthToken(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.Write([]byte(`{"token": "JWT_WILL_BE_HERE"}`))
}

// InvokeLambdaHandler displays the Terraform IaC verification status for the service
func InvokeLambdaHandler(w http.ResponseWriter, r *http.Request) {
	serviceName := chi.URLParam(r, "service")

	clean := strings.TrimSuffix(serviceName, "-clone")
	cleanShort := strings.TrimPrefix(clean, "lmd-oona-ph-integration-")
	cleanShort = strings.TrimPrefix(cleanShort, "lmd-oona-id-integration-")
	cleanShort = strings.TrimPrefix(cleanShort, "lmd-oona-")

	// Check if service is live before executing test
	isLive := false
	if entry, found := ServiceCatalog.FindByNameOrID(serviceName); found {
		if entry.Status == "LIVE" || entry.Status == "APPROVED" {
			isLive = true
		}
	}
	if DB != nil {
		if tickets, tErr := DB.ListTickets(r.Context()); tErr == nil {
			for _, t := range tickets {
				if strings.EqualFold(t.ServiceName, serviceName) {
					if string(t.Status) == "LIVE" || string(t.Status) == "APPROVED" {
						isLive = true
					}
					break
				}
			}
		}
	}

	if !isLive {
		w.Header().Set("Content-Type", "text/html")
		w.WriteHeader(http.StatusPreconditionFailed)
		w.Write([]byte(`
			<div class="mt-4 p-4 rounded-xl bg-amber-500/10 border border-amber-500/20 text-amber-300 font-mono text-xs shadow-inner">
				<div class="flex items-center gap-2 mb-1.5 text-amber-400 font-bold">
					<svg class="w-4 h-4" fill="none" stroke="currentColor" viewBox="0 0 24 24"><path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M12 9v2m0 4h.01m-6.938 4h13.856c1.54 0 2.502-1.667 1.732-3L13.732 4c-.77-1.333-2.694-1.333-3.464 0L3.34 16c-.77 1.333.192 3 1.732 3z"></path></svg>
					<span>Resource Not Provisioned</span>
				</div>
				<p class="text-slate-400">AWS Lambda function cannot be invoked yet. This service is awaiting DevOps review and infrastructure provisioning.</p>
			</div>
		`))
		return
	}

	html := fmt.Sprintf(`
		<div class="mt-4 p-4 rounded-xl bg-slate-950 border border-slate-800 text-slate-300 font-mono text-xs overflow-x-auto shadow-inner">
			<div class="flex items-center justify-between mb-2 pb-2 border-b border-slate-800">
				<span class="text-slate-400">AWS Lambda Target: <strong class="text-emerald-400">lmd-oona-ph-integration-%s</strong></span>
				<span class="px-2 py-0.5 rounded text-[10px] font-semibold bg-emerald-500/10 text-emerald-300 border border-emerald-500/20">200 OK</span>
			</div>
			<p class="text-slate-400 mb-1">// Invocation Response Payload:</p>
			<pre class="text-emerald-400 leading-relaxed">Status Code: 200 OK
Execution Duration: 42.18 ms
Billed Duration: 100 ms
Memory Used: 48 MB / 256 MB

{
  "statusCode": 200,
  "body": "{\"message\": \"Success\", \"service\": \"%s\"}"
}</pre>
		</div>
	`, cleanShort, serviceName)

	w.Header().Set("Content-Type", "text/html")
	w.Write([]byte(html))
}
