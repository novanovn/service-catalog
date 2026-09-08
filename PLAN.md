# Oona Developer Portal & Service Catalog (Master Blueprint & Task Tracker)

**Document Version:** 2.1  
**Last Updated:** September 2026  
**Status:** In Active Evolution (Phase 5: Refactoring & UI/UX Modernization)

---

## 🏗️ 1. Realized Architecture & Tech Stack

This portal serves as an Internal Developer Platform (IDP) and automated microservice onboarding orchestration engine at Oona Insurance.

*   **Backend:** Go 1.24+ with `go-chi/chi/v5` Router + Standard Library `html/template`
*   **Database:** PostgreSQL 15+ (Type-safe queries generated via `sqlc`)
*   **Cache & Message Broker:** Valkey 8.1+ (Redis-compatible, persistent with AOF/RDB)
*   **Background Workers:** `hibiken/asynq` with 4 prioritized queues (`aws_jenkins:6`, `git_verify:3`, `trivy_scan:1`, `default:2`)
*   **Frontend:** Hypermedia-Driven Application (HDA) via HTMX 1.9+, Tailwind CSS, and Vanilla JS
*   **Security & Encryption:** AES-256-GCM for integration credentials, double-submit cookie CSRF, rate-limiting middleware, and Trivy shift-left vulnerability scanning.
*   **AI DevSecOps Engine:** Google Gemini AI integration (`gemini-3-flash-preview` / `gemini-1.5-flash`) for automated CVE blast-radius analysis and remediation triaging.

---

## ⚙️ 2. Core Onboarding Lifecycle (State Machine)

1. **`[DRAFT]` / `[PENDING]`**: Developer submits service metadata (Domain, Country, Service Name, Repo URL, Environment variables).
2. **`[SCANNING]`**: Asynq background worker executes `trivy repo` security audit on the source code.
3. **`[WAITING_INFRA]`**: Portal verifies whether DevOps pushed the IaC structure to `oona-dtc-country-terraform-iac`.
4. **`[INFRA_DETECTED]`**: Worker parses `terraform.tfvars` using strict Oona conventions (`02-app-setup/{domain}/{country}/{env}/services/{service_name}/terraform.tfvars`) via AST HCL parser.
5. **`[JENKINS_READY]`**: DevOps approves ticket in portal. Asynq worker posts XML payload to Jenkins REST API to auto-generate Multibranch Pipeline in folder `AWS Lambda Projects`.
6. **`[LIVE]`**: Service automatically transitions into the Service Catalog with live TechDocs, environment variable diffs, and health status telemetry.

---

## 📋 3. Development Progress & Checklist Tracker

### Phase 0: Foundation & Core Infrastructure
- [x] **FND-01**: Setup Git repo & `docker-compose.yml` (Postgres, Valkey, API server, Asynq worker).
- [x] **FND-02**: Database schema migration scripts (`0001_initial_schema.sql` through `0008_ticket_ai_analysis.sql`) and `sqlc` model generation.
- [x] **FND-03**: Secure AES-256-GCM crypto engine for storing third-party secrets (GitHub, Jenkins, Jira tokens).

### Phase 1: Authentication & HTTP Routing
- [x] **API-01**: Chi v5 Router setup with structured JSON logging (`slog`), recovery, and security headers.
- [x] **API-02**: JWT session authentication with role-based access control (RBAC: `developer`, `infra`, `admin`).
- [x] **API-03**: Global and per-route rate limiters + Double-submit cookie CSRF protection (`CSRFMiddleware`).
- [x] **API-04**: Swagger UI & OpenAPI 3.0 specification (`/docs/api`, `/docs/openapi.yaml`).

### Phase 2: Asynq Background Automation Engines
- [x] **WRK-01**: Prioritized Asynq worker server with concurrency pools.
- [x] **WRK-02**: `trivy_scan` worker: Scans GitHub repositories, extracts CVE findings, and caches results.
- [x] **WRK-03**: `git_verify` worker: AST HCL parser (`internal/worker/infra/parser.go`) validating strict `terraform.tfvars` pathing.
- [x] **WRK-04**: `aws_jenkins` worker: Jenkins REST API integration for automated multibranch pipeline generation.
- [x] **WRK-05**: `sync_techdocs` worker: Markdown sync for in-portal TechDocs rendering.
- [x] **WRK-06**: Gemini AI Security Analyst: Live CVE summary, CVSS scoring, and remediation CLI generation.

### Phase 3: Web UI & Hypermedia (HTMX + Go Templates)
- [x] **UI-01**: Root layout, navigation, and toast notifications (`layout.html`, `dashboard.html`).
- [x] **UI-02**: Developer service onboarding form with dynamic multi-environment inputs (`ticket_form.html`).
- [x] **UI-03**: DevOps approval dashboard & security detail inspector with Gemini slide-over (`approval_dashboard.html`, `approval_detail.html`).
- [x] **UI-04**: Interactive service catalog with 2-column TechDocs viewer & env diffs (`catalog_list.html`, `catalog_detail.html`).
- [x] **UI-05**: Admin management suite for Users, Parameters, Audit Logs, and Backup/Restore (`admin_*.html`).

---

## 🚀 4. Active Backlog & Next Milestones (Phase 5)

See detailed implementation plan in [**`REFACTORING_BACKLOG.md`**](./REFACTORING_BACKLOG.md).

- [x] **Task BKG-01:** Modularize monolithic `internal/api/views.go` (3,735 lines) into domain-specific view controllers.
- [x] **Task BKG-02:** Modernize UI/UX with interactive microservice topology mesh graph and SVG gradient sparklines.
- [x] **Task BKG-05:** Upgrade and align all Portal UI icons to modern Lucide Icons standard (`<i data-lucide="..."></i>`) with dynamic HTMX swap re-initialization.
- [ ] **Task BKG-03:** Live AWS SDK v2 integration for real-time Lambda execution tests across UAT/Preprod.
- [ ] **Task BKG-04:** Replace local filesystem IaC repo volume mounting with dynamic in-memory Git shallow clones.
- [x] **Task BKG-06:** Scheduled Trivy Security Scan Engine with custom time, target branch selection, and `Asia/Jakarta` (WIB, UTC+7) timezone awareness (Queue-based FIFO).
- [ ] **Task BKG-07:** GitHub Organization Webhook Handler (`POST /api/v1/webhooks/github`) for automated event-driven scans upon branch pushes (Deferred for Cloud Ingress deployment).
