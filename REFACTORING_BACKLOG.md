# Refactoring & Engineering Backlog (REFACTORING_BACKLOG.md)

**Target Repository:** `oona-dev-portal`  
**Purpose:** Technical guide for refactoring debt, modularizing Go handlers, and completing feature enhancements across AI coding agents (Hermes / Antigravity / Cursor).

---

## 🎯 Backlog Item 1: Modularize `internal/api/views.go` (3,735 lines) — [COMPLETED ✅]

### 🔴 Problem Statement
`internal/api/views.go` sebelumnya berisi seluruh HTTP view handlers, authentication logic, dashboard telemetry, catalog queries, ticket workflow, admin suites, dan AI security triage dalam 1 file monolith raksasa (>3,700 baris).

### 🛠️ Solution & File Decomposition [COMPLETED]
File `views.go` telah berhasil dipecah menjadi 7 controller modular di bawah package `api`:

| Target File | Lines | Responsibilities & Handlers |
| :--- | :--- | :--- |
| `internal/api/views_base.go` | ~37 | `getTemplateDir()`, `parsePage()`, template function map (`add`, `sub`). |
| `internal/api/views_auth.go` | ~195 | `RenderLoginPage()`, `HandleLogin()`, `HandleLogout()`, `RenderProfile()`, `RenderSettings()`. |
| `internal/api/views_dashboard.go` | ~197 | `RenderDashboard()`, KPI stat cards telemetry, recent tickets, security badge calculations. |
| `internal/api/views_catalog.go` | ~793 | `RenderCatalogList()`, `RenderServiceDetail()`, `CatalogDocsHandler()`, `FetchEnvironmentVarsForService()`, `FetchTFVarsContent()`. |
| `internal/api/views_tickets.go` | ~1,187 | `RenderTicketForm()`, `RenderTicketList()`, `RenderApprovalDashboard()`, `RenderApprovalDetail()`, `PreviewPipelineName()`, `ResolveTerraformPath()`. |
| `internal/api/views_admin.go` | ~743 | `RenderAdminUsers()`, `RenderAdminIntegrations()`, `RenderAdminParameters()`, `RenderAdminAuditLogs()`, `ExportAuditLogsCSVHandler()`, `TestIntegrationHandler()`. |
| `internal/api/views_ai.go` | ~677 | `AISecurityAnalyzeHandler()`, `CatalogTrivyScanHandler()`, Gemini AI prompt engine, CVSS parser, live remediation CLI generator. |

### ✅ Verification Results:
- `go build ./...` ➔ **PASSED (0 error)**
- `go test -v ./internal/api/...` ➔ **ALL TESTS PASS (100% Green)**

---

## 🎯 High-Priority Backlog Item 2: UI Modernization (Interactive SVG Service Topology) — [COMPLETED ✅]

### 🔴 Current State
Visualisasi dependensi microservice di `internal/templates/catalog_detail.html` dan `dashboard.html` sebelumnya berupa static HTML cards.

### 🛠️ Enhancement Completed
1. Mengintegrasikan **SVG-based dynamic gradient sparklines** pada Dashboard KPI cards (`dashboard.html`).
2. Topology flow chart di `catalog_detail.html` telah diperkuat dengan multi-tier node flow (Ingress, Hero Runtime, EventBus, RDS DB, dan External APIs) dengan animated gradient dash stream lines (`animate-dash`).
3. Seluruh template HTML telah divalidasi dan lolos parsing Go `html/template` suite.

---

## 🎯 Backlog Item 5: Upgrade Flat Icons to Lucide Icons Standard — [COMPLETED ✅]

### 🔴 Problem Statement
Icon navigasi dan header sebelumnya menggunakan raw SVG statis yang tidak seragam ukurannya dan kurang modern.

### 🛠️ Enhancement Completed
1. Menambahkan library resmi **Lucide Icons** di `internal/templates/layout.html`.
2. Menyelaraskan seluruh icon sidebar & menu navigasi sesuai semantiknya:
   - **Dashboard** ➔ `<i data-lucide="layout-dashboard"></i>`
   - **Service Catalog** ➔ `<i data-lucide="server"></i>`
   - **Ticket List** ➔ `<i data-lucide="ticket"></i>`
   - **Onboard Service** ➔ `<i data-lucide="plus-circle"></i>`
   - **DevOps Approvals** ➔ `<i data-lucide="check-check"></i>`
   - **User Management** ➔ `<i data-lucide="users"></i>`
   - **System Integrations** ➔ `<i data-lucide="blocks"></i>`
   - **System Parameters** ➔ `<i data-lucide="sliders"></i>`
   - **Backup & Restore** ➔ `<i data-lucide="database-backup"></i>`
   - **Audit Logs** ➔ `<i data-lucide="scroll-text"></i>`
3. Menambahkan listener otomatis `htmx:afterSwap` untuk me-render ulang Lucide icons setiap kali terjadi partial swap HTMX.

---

## 🎯 Backlog Item 3: Live AWS Lambda Invocation SDK Integration

### 🔴 Current State
`InvokeLambdaHandler` di `internal/api/router.go` mengembalikan simulated JSON response.

### 🛠️ Enhancement Plan
1. Sambungkan `InvokeLambdaHandler` ke `internal/worker/aws/client.go`.
2. Gunakan `aws-sdk-go-v2` (`lambda.NewFromConfig(cfg).Invoke(...)`) untuk mengeksekusi Lambda test payload di environment UAT/Preprod.
3. Tampilkan durasi eksekusi riil, billed duration, dan cloud response payload ke HTMX container.

---

## 🎯 Backlog Item 4: Monorepo Dynamic Git Fetching for Cloud Deployments

### 🔴 Current State
`docker-compose.yml` mengandalkan local mount `../oona-dtc-country-terraform-iac:/terraform-iac:ro`.

### 🛠️ Enhancement Plan
1. Refactor `internal/worker/infra/parser.go` untuk mendukung sparse-checkout in-memory via GitHub API dengan GitHub Token yang terkonfigurasi.
2. Mengizinkan portal berjalan penuh di AWS ECS Fargate tanpa dependensi filesystem lokal.

---

## 🎯 Backlog Item 6: Scheduled Trivy Security Scanner with Timezone & Branch Control — [COMPLETED ✅]

### 🔴 Problem Statement
Scan kerentanan CVE sebelumnya hanya berjalan secara manual (on-demand) per service.

### 🛠️ Enhancement Completed
1. **Database Migration:** Dibuat tabel `service_scan_schedules` di PostgreSQL (`migrations/0010_scan_schedules.sql`).
2. **Timezone Lock:** Terkunci pasti ke `Asia/Jakarta` (WIB, UTC+7) sesuai AWS Jakarta region standard.
3. **Queue-Based Sequential Execution:** Scheduler di background worker (`internal/worker/scheduler.go`) memasukkan task ke antrean Asynq queue `trivy_scan` (FIFO).
4. **Per-Repo Customization:** Tiap microservice memiliki konfigurasi tersendiri (jam eksekusi WIB, frekuensi Daily/Weekly, dan target branch `main`/`uat`/`dev`/`staging`).
5. **Interactive UI:** Form HTMX langsung terintegrasi di tab `#vulnerabilities` pada halaman detail catalog.

---

## 🎯 Backlog Item 7: GitHub Organization Webhook Ingress (Event-Driven Scan)

### 🔴 Problem Statement
Saat portal di-deploy ke Cloud/AWS dengan public ingress, developer mengharapkan feedback keamanan real-time setiap kali melakukan push / merge PR ke branch utama (`main`/`uat`).

### 🛠️ Enhancement Plan
1. Buat endpoint `POST /api/v1/webhooks/github` dengan verifikasi signature HMAC SHA-256 (`X-Hub-Signature-256`).
2. Filter event `push` hanya untuk branch penting (`main`, `uat`, `staging`) dan cocokkan nama repo dengan service yang terdaftar di catalog.
3. Enqueue `trivy_scan` task secara otomatis ke Asynq worker.
4. *(Status: Backlog Deferred hingga portal memiliki public domain/ingress)*.
