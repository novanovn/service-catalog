# Oona Dev Portal — Gemini Fix Prompt

## Konteks Proyek

Ini adalah **Oona Internal Developer Portal (IDP)** berbasis Go yang mengotomatisasi onboarding Lambda service di Oona Insurance. Tech stack:

- **Language**: Go 1.24
- **HTTP Router**: Chi v5
- **Background Worker**: Asynq over Valkey (Redis-compatible)
- **Database**: PostgreSQL (via pgx v5 + SQLC)
- **Frontend**: Go HTML Templates + HTMX + Tailwind CSS
- **Module**: `github.com/oona-insurance/dev-portal`

**Lokasi project**: `internal/` dengan struktur:
- `api/` — HTTP handlers, middleware, router
- `auth/` — JWT + AES crypto
- `core/` — state machine
- `models/` — struct dan constants
- `tasks/` — Asynq background workers
- `worker/ci/` — Jenkins/GitLab engine
- `worker/infra/` — HCL/Terraform parser
- `worker/aws/` — AWS Lambda client
- `notify/` — SMTP + Teams webhook
- `repository/postgres/` — SQLC generated code + queries
- `templates/` — HTML templates

---

## Semua Temuan yang Harus Difix

---

### 🔴 CRITICAL — Fix Wajib Sebelum Deploy

---

#### FIX-01: Hardcoded JWT Secret Key

**File**: `internal/auth/jwt.go`

**Masalah**: JWT signing key hardcoded sebagai literal string di source code. Siapapun dengan akses repo bisa forge JWT token valid.

```go
// SEKARANG (salah):
var jwtSecret = []byte("OONA_SUPER_SECRET_KEY_CHANGE_IN_PROD")
```

**Yang harus dilakukan**:
1. Hapus variable package-level `jwtSecret`
2. Buat fungsi `SetJWTSecret(secret []byte)` dan simpan ke private variable
3. Di `cmd/server/main.go`, baca dari env var `JWT_SECRET` saat startup
4. Jika `JWT_SECRET` kosong atau kurang dari 32 karakter → `log.Fatal("JWT_SECRET env var must be at least 32 bytes")`
5. Panggil `auth.SetJWTSecret([]byte(secret))` sebelum router diinisialisasi

---

#### FIX-02: Hardcoded AES-256 Encryption Key

**File**: `internal/auth/crypto.go`

**Masalah**: AES encryption key hardcoded. Key 35 byte di-slice `[:32]` secara diam-diam — jika key diubah panjangnya, semua data terenkripsi di DB tidak bisa didekripsi.

```go
// SEKARANG (salah):
var encryptionKey = []byte("OONA_AES256_VAULT_KEY_DO_NOT_SHARE!")
```

**Yang harus dilakukan**:
1. Hapus variable package-level `encryptionKey`
2. Buat fungsi `SetEncryptionKey(key []byte)` dengan validasi `len(key) != 32 → return error`
3. Di `cmd/server/main.go`, baca dari env var `AES_ENCRYPTION_KEY`
4. Validasi panjang tepat 32 byte. Jika tidak → `log.Fatal("AES_ENCRYPTION_KEY must be exactly 32 bytes")`
5. Gunakan key yang di-set via `SetEncryptionKey()` di seluruh fungsi `Encrypt()` dan `Decrypt()`

---

#### FIX-03: Database Layer Tidak Ada — Wajib Diimplementasi

**Files**: `internal/repository/postgres/queries/` (kosong), `cmd/server/main.go`

**Masalah**: `sqlc.yaml` sudah dikonfigurasi tapi direktori queries kosong, tidak ada generated code, tidak ada DB connection. Portal tidak bisa menyimpan atau membaca state apapun.

**Yang harus dilakukan**:

**Step 1** — Buat file SQL queries di `internal/repository/postgres/queries/tickets.sql`:
```sql
-- name: CreateTicket :one
INSERT INTO tickets (id, created_by, repo_url, domain, country, service_name, pipeline_name, status, description)
VALUES (gen_random_uuid(), $1, $2, $3, $4, $5, $6, 'DRAFT', $7)
RETURNING *;

-- name: GetTicketByID :one
SELECT * FROM tickets WHERE id = $1;

-- name: ListTickets :many
SELECT * FROM tickets ORDER BY created_at DESC;

-- name: UpdateTicketStatus :one
UPDATE tickets SET status = $2, updated_at = NOW() WHERE id = $1 RETURNING *;

-- name: ListTicketsByStatus :many
SELECT * FROM tickets WHERE status = $1 ORDER BY created_at DESC;
```

**Step 2** — Buat file SQL queries di `internal/repository/postgres/queries/users.sql`:
```sql
-- name: GetUserByEmail :one
SELECT * FROM users WHERE email = $1;

-- name: CreateUser :one
INSERT INTO users (id, email, password_hash, role, full_name)
VALUES (gen_random_uuid(), $1, $2, $3, $4)
RETURNING *;

-- name: ListUsers :many
SELECT * FROM users ORDER BY created_at DESC;
```

**Step 3** — Buat file SQL queries di `internal/repository/postgres/queries/integrations.sql`:
```sql
-- name: GetIntegrationByID :one
SELECT * FROM integrations WHERE id = $1;

-- name: ListIntegrations :many
SELECT * FROM integrations ORDER BY created_at DESC;

-- name: CreateIntegration :one
INSERT INTO integrations (id, name, provider, base_url, auth_user, auth_token_encrypted, is_active)
VALUES (gen_random_uuid(), $1, $2, $3, $4, $5, true)
RETURNING *;
```

**Step 4** — Jalankan `sqlc generate` untuk generate kode Go

**Step 5** — Update `cmd/server/main.go` untuk init DB connection:
```go
// Tambahkan di startup:
dbURL := os.Getenv("DB_DSN")
if dbURL == "" {
    log.Fatal("DB_DSN environment variable is required")
}
conn, err := pgx.Connect(context.Background(), dbURL)
if err != nil {
    log.Fatalf("Unable to connect to database: %v", err)
}
defer conn.Close(context.Background())
queries := db.New(conn)
// Inject queries ke handlers
```

---

#### FIX-04: Login Masih Hardcoded — Harus Pakai DB

**File**: `internal/api/views.go` — fungsi `HandleLogin`

**Masalah**: Login menggunakan hardcoded credential `admin@oona-insurance.com` / `admin123`. Tidak ada pengecekan ke database.

```go
// SEKARANG (salah):
if email == "admin@oona-insurance.com" && password == "admin123" {
```

**Yang harus dilakukan**:
1. Inject `*db.Queries` ke handler (via struct atau closure)
2. Panggil `queries.GetUserByEmail(ctx, email)` 
3. Gunakan `bcrypt.CompareHashAndPassword(user.PasswordHash, []byte(password))` untuk verifikasi
4. Jika valid, generate JWT dengan UserID, Email, dan Role dari DB
5. Tambahkan dependency `golang.org/x/crypto` untuk bcrypt

---

### 🔴 HIGH — Fix Segera

---

#### FIX-05: Trivy Scan Result Tidak Pernah Disimpan

**File**: `internal/tasks/trivy_task.go`

**Masalah**: Scan berjalan, menghitung CVE, tapi status ticket tidak pernah diupdate dan developer tidak pernah dapat notifikasi.

**Yang harus dilakukan**:
1. Inject `*db.Queries` ke handler (via Asynq server setup)
2. Pada path **reject** (`criticalCount > 0 || highCount > 5`):
   ```go
   db.UpdateTicketStatus(ctx, db.UpdateTicketStatusParams{ID: uuid, Status: "REJECTED_SECURITY"})
   // Enqueue notifikasi Teams/email
   ```
3. Pada path **pass**:
   ```go
   db.UpdateTicketStatus(ctx, db.UpdateTicketStatusParams{ID: uuid, Status: "WAITING_INFRA"})
   ```
4. Buat konstanta `MaxHighVulns = 5` dengan komentar yang menjelaskan kebijakan bisnis ini
5. Sama untuk worker lainnya: `HandleVerifyInfraGitTask`, `HandleCheckAWSLambdaTask`, `HandleCreatePipelineTask`

---

#### FIX-06: `RequireRole` Middleware Tidak Dipakai di Mana Pun

**File**: `internal/api/router.go`

**Masalah**: `RequireRole` sudah diimplementasi di `middleware.go` tapi tidak digunakan di satu pun route.

**Yang harus dilakukan**, update `router.go`:
```go
// Endpoint approve → devops + admin only
r.With(RequireRole("devops", "admin")).Post("/api/v1/tickets/{id}/approve", ApproveTicketHandler)

// Admin routes → admin only
r.With(RequireRole("admin")).Get("/admin/integrations", RenderAdminIntegrations)
r.With(RequireRole("admin")).Get("/admin/users", RenderAdminUsers)
r.With(RequireRole("admin")).Post("/api/v1/admin/integrations", CreateIntegrationHandler)
r.With(RequireRole("admin")).Post("/api/v1/admin/users", CreateUserHandler)

// Approvals → devops + admin only
r.With(RequireRole("devops", "admin")).Get("/approvals", RenderApprovalDashboard)
```

---

#### FIX-07: XSS di Jenkins Log Output

**File**: `internal/worker/ci/jenkins_logs.go`

**Masalah**: Raw log lines dari Jenkins dimasukkan langsung ke HTML `<span>` tanpa escaping.

```go
// SEKARANG (berbahaya):
styledLogs.WriteString(fmt.Sprintf("<span class='text-red-400'>%s</span>\n", line))
```

**Yang harus dilakukan**:
1. Import `html` package
2. Escape semua `line` sebelum dimasukkan ke template:
```go
import "html"

// Ganti SEMUA Sprintf di loop colorization:
styledLogs.WriteString(fmt.Sprintf("<span class='text-red-400'>%s</span>\n", html.EscapeString(line)))
styledLogs.WriteString(fmt.Sprintf("<span class='text-yellow-400'>%s</span>\n", html.EscapeString(line)))
styledLogs.WriteString(fmt.Sprintf("<span class='text-green-400 font-bold'>%s</span>\n", html.EscapeString(line)))
styledLogs.WriteString(fmt.Sprintf("<span class='text-blue-300'>%s</span>\n", html.EscapeString(line)))
// Default branch:
styledLogs.WriteString(html.EscapeString(line) + "\n")
```

---

#### FIX-08: Hardcoded Jenkins Credential di Logs Handler

**File**: `internal/api/logs_handler.go`

**Masalah**: `AuthToken: "DUMMY_TOKEN_FROM_DB"` dan hardcoded `"ph"`, `"integration"`.

**Yang harus dilakukan**:
1. Inject `*db.Queries` ke handler
2. Dari `service` URL param, lookup service di DB untuk dapat `integration_id`
3. Dari `integration_id`, ambil integration record, decrypt token: `auth.Decrypt(integration.AuthTokenEncrypted)`
4. Reconstruct pipeline name dari data service di DB, bukan hardcoded country/domain
5. Gunakan real token untuk `cfg.AuthToken`

---

### 🟡 MEDIUM — Fix di Sprint Ini

---

#### FIX-09: Cookie `Secure` Hardcoded False

**File**: `internal/api/views.go` — fungsi `HandleLogin`

**Yang harus dilakukan**:
```go
// Baca dari env:
isProduction := os.Getenv("APP_ENV") == "production"

http.SetCookie(w, &http.Cookie{
    Name:     "oona_token",
    Value:    tokenString,
    Path:     "/",
    HttpOnly: true,
    Secure:   isProduction,  // true di production, false di dev
    MaxAge:   86400,
    SameSite: http.SameSiteLaxMode,
})
```

---

#### FIX-10: URL Injection di Jenkins Trigger

**File**: `internal/worker/ci/engine.go` — fungsi `TriggerBuild`

**Masalah**: Email dari JWT di-interpolate langsung ke query string. Email dengan `+` akan corrupt parameter.

**Yang harus dilakukan**:
```go
// Ganti string interpolation dengan url.Values:
params := url.Values{}
params.Set("TRIGGERED_BY_PORTAL", triggeredBy)
params.Set("token", j.Config.BuildToken)
fullURL := fmt.Sprintf("%s/job/AWS%%20Lambda%%20Projects/job/%s/buildWithParameters?%s",
    j.Config.BaseURL, jobName, params.Encode())
```

---

#### FIX-11: HCL Parser Adalah Stub

**File**: `internal/worker/infra/parser.go`

**Masalah**: Selalu return `["DB_HOST", "DB_PASSWORD"]` hardcoded tanpa parsing file.

**Yang harus dilakukan**:
1. Implementasi parsing HCL menggunakan `hclsyntax` untuk extract `env_vars` block dari `functions` map
2. Buat unit test dengan fixture `.tfvars` yang mencakup: happy path, key tidak ada, HCL malformed, env_vars kosong
3. Hapus `_ = file` stub

---

#### FIX-12: Logout via GET — CSRF Vulnerable

**File**: `internal/api/router.go` dan `internal/api/views.go`

**Masalah**: `GET /logout` rawan silent logout CSRF via `<img>` tag.

**Yang harus dilakukan**:
1. Ganti route ke `r.Post("/logout", HandleLogout)`
2. Update `layout.html` — ganti `<a href="/logout">` menjadi form POST:
```html
<form method="POST" action="/logout">
    <button type="submit" class="block px-4 py-2 text-sm text-red-600 hover:bg-red-50 font-medium w-full text-left">
        Sign out
    </button>
</form>
```

---

### 🔴 ROUTE & MENU BUGS — Fix Wajib untuk UI Berfungsi

---

#### FIX-13: Ticket Form Submit ke Route yang Salah (404)

**File**: `internal/templates/ticket_form.html`

**Masalah**: Form submit ke `hx-post="/tickets"` tapi route yang ada adalah `POST /api/v1/tickets`.

**Yang harus dilakukan**:
```html
<!-- SEKARANG (salah): -->
<form hx-post="/tickets" hx-target="body" ...>

<!-- HARUS JADI: -->
<form hx-post="/api/v1/tickets" hx-target="#form-result" hx-swap="innerHTML" ...>
```
Tambahkan `<div id="form-result"></div>` di bawah form untuk menampilkan response sukses/error.

---

#### FIX-14: Route Admin Tidak Ada (404 saat Submit)

**File**: `internal/api/router.go` dan buat handler baru

**Masalah**: 
- `admin_users.html` submit ke `POST /api/v1/admin/users` — route tidak ada
- `admin_integrations.html` submit ke `POST /api/v1/admin/integrations` — route tidak ada

**Yang harus dilakukan**:
1. Tambahkan di `router.go`:
```go
r.With(RequireRole("admin")).Post("/api/v1/admin/users", CreateUserHandler)
r.With(RequireRole("admin")).Post("/api/v1/admin/integrations", CreateIntegrationHandler)
```
2. Buat `CreateUserHandler` di `views.go` atau file baru `admin_handlers.go`:
   - Parse form fields (full_name, email, role, password)
   - Hash password dengan bcrypt
   - Insert ke DB via `queries.CreateUser()`
   - Return HTMX response sukses
3. Buat `CreateIntegrationHandler`:
   - Parse form fields (name, provider, base_url, auth_user, auth_token)
   - Encrypt auth_token: `auth.Encrypt(authToken)`
   - Insert ke DB via `queries.CreateIntegration()`
   - Return HTMX response sukses

---

#### FIX-15: Dashboard Tidak Ada di Sidebar

**File**: `internal/templates/layout.html`

**Masalah**: Route `/dashboard` ada dan berfungsi, tapi tidak ada link di sidebar. User tidak bisa navigasi kembali ke dashboard.

**Yang harus dilakukan**, tambahkan menu item pertama di sidebar sebelum "Service Catalog":
```html
<a href="/dashboard" class="flex items-center px-4 py-3 text-slate-300 hover:bg-slate-800 hover:text-white rounded-lg transition-colors">
    <svg class="w-5 h-5 flex-shrink-0" fill="none" stroke="currentColor" viewBox="0 0 24 24">
        <path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M3 12l2-2m0 0l7-7 7 7M5 10v10a1 1 0 001 1h3m10-11l2 2m-2-2v10a1 1 0 01-1 1h-3m-6 0a1 1 0 001-1v-4a1 1 0 011-1h2a1 1 0 011 1v4a1 1 0 001 1m-6 0h6"></path>
    </svg>
    <span class="ml-3 sidebar-text">Dashboard</span>
</a>
```

---

#### FIX-16: Approve Handler Masih DummyHandler — HTMX Swap Akan Rusak UI

**File**: `internal/api/router.go` dan buat handler baru

**Masalah**: `DummyHandler` return JSON, tapi template menggunakan `hx-swap="outerHTML"` yang mengharapkan HTML fragment.

**Yang harus dilakukan**:
1. Buat `ApproveTicketHandler` di file baru `internal/api/tickets_actions.go`:
   - Ambil ticket ID dari URL param
   - Cek role user dari JWT claims (`devops` atau `admin`)
   - Update status ticket ke `JENKINS_READY` via DB
   - Enqueue Asynq task `TypeCreatePipeline`
   - Return HTML fragment (bukan JSON) untuk HTMX swap:
   ```go
   w.Header().Set("Content-Type", "text/html")
   w.Write([]byte(`<li class="px-4 py-5 text-sm text-green-600 font-medium">✓ Pipeline creation queued successfully.</li>`))
   ```
2. Update route di `router.go` untuk mengganti `DummyHandler`

---

#### FIX-17: `RenderServiceDetail` Hardcoded — Tidak Pakai URL Param

**File**: `internal/api/views.go` — fungsi `RenderServiceDetail`

**Masalah**: Semua detail page menampilkan data `health-renewal-svc / Integration / PH` tanpa melihat URL `{service}` param.

**Yang harus dilakukan**:
1. Ambil service name dari URL: `serviceName := chi.URLParam(r, "service")`
2. Lookup service dari DB via `queries.GetServiceByName(ctx, serviceName)` (buat query ini)
3. Populate `data` struct dari DB result
4. Jika tidak ditemukan → return 404
5. Untuk MVP: jika DB belum siap, minimal gunakan URL param untuk `ServiceName` di template

---

#### FIX-18: Halaman List Tickets Belum Ada

**File**: `internal/api/router.go`, buat handler dan template baru

**Masalah**: `dashboard.html` punya link `<a href="/tickets">View all</a>` tapi route ini tidak terdaftar dan tidak ada template-nya.

**Yang harus dilakukan**:
1. Tambah route: `r.Get("/tickets", RenderTicketList)`
2. Buat handler `RenderTicketList` yang mengambil semua tickets dari DB
3. Buat template `internal/templates/ticket_list.html` dengan table semua tiket dan status badge per state machine

---

#### FIX-19: Cancel Button Tidak Berfungsi di Ticket Form

**File**: `internal/templates/ticket_form.html`

**Masalah**: `<button type="button">Cancel</button>` tidak punya handler, klik tidak melakukan apa-apa.

**Yang harus dilakukan**:
```html
<button type="button" onclick="window.history.back()" class="text-sm font-semibold leading-6 text-gray-900">
    Cancel
</button>
```

---

#### FIX-20: Catalog Card Kedua dan Seterusnya Pakai `href="#"`

**File**: `internal/templates/catalog_list.html`

**Masalah**: Kartu service kedua "View Details" link ke `href="#"` — tidak konsisten dengan kartu pertama.

**Yang harus dilakukan**:
Ganti semua `href="#"` pada catalog cards menjadi link dinamis. Untuk MVP dengan hardcoded data:
```html
<a href="/catalog/coreplus-aggregation-flow" ...>View Details</a>
```
Untuk implementasi nyata: data catalog harus dari DB, di-loop di template dengan link dinamis `href="/catalog/{{ .ServiceName }}"`.

---

### 🟢 LOW / HOUSEKEEPING

---

#### FIX-21: Plaintext Password di docker-compose.yml

**File**: `docker-compose.yml`

**Yang harus dilakukan**:
1. Ganti semua value hardcoded dengan env var references:
```yaml
environment:
  POSTGRES_PASSWORD: ${POSTGRES_PASSWORD}
  DB_DSN: postgresql://oona:${POSTGRES_PASSWORD}@postgres:5432/oona_portal
```
2. Buat file `.env.example` dengan placeholder values
3. Tambahkan `.env` ke `.gitignore`

---

#### FIX-22: Binary `server` Ter-commit ke Repo

**File**: `.gitignore` (buat jika belum ada)

**Yang harus dilakukan**:
```
# .gitignore
server
*.exe
*.test
*.out
vendor/
.env
```
Kemudian: `git rm --cached server`

---

#### FIX-23: Dockerfile.dev Pakai Go 1.23, go.mod Require 1.24

**File**: `Dockerfile.dev`

**Yang harus dilakukan**:
```dockerfile
# Ganti:
FROM golang:1.23-alpine
# Menjadi:
FROM golang:1.24-alpine
```

---

#### FIX-24: Profile dan Settings Dropdown Masih Dead Link

**File**: `internal/templates/layout.html`

**Yang harus dilakukan** (minimal untuk MVP):
```html
<!-- Ganti href="#" dengan link yang jelas atau disable: -->
<a href="/profile" class="block px-4 py-2 text-sm text-gray-700 hover:bg-gray-100">Your Profile</a>
<a href="/settings" class="block px-4 py-2 text-sm text-gray-700 hover:bg-gray-100">Settings</a>
```
Dan tambahkan route + placeholder handler untuk keduanya, atau hapus dari menu jika belum diimplementasi.

---

## Urutan Pengerjaan yang Disarankan

```
Phase 1 (Blocking — tanpa ini portal tidak bisa dipakai):
  FIX-01 → FIX-02 → FIX-03 → FIX-04 → FIX-13 → FIX-14 → FIX-15

Phase 2 (Core workflow):
  FIX-05 → FIX-06 → FIX-08 → FIX-16 → FIX-17 → FIX-18

Phase 3 (Security hardening):
  FIX-07 → FIX-09 → FIX-10 → FIX-11 → FIX-12

Phase 4 (Polish & housekeeping):
  FIX-19 → FIX-20 → FIX-21 → FIX-22 → FIX-23 → FIX-24
```

---

## Catatan untuk Gemini

1. Semua perubahan harus tetap dalam module `github.com/oona-insurance/dev-portal`
2. Gunakan pattern yang sudah ada di codebase (Chi router, HTMX response, `parsePage()` untuk template)
3. Jangan ganti arsitektur — ini adalah iterasi MVP, bukan rewrite
4. Setiap fix yang menyentuh DB harus handle kondisi `DB == nil` sebagai graceful fallback (legacy mock mode)
5. HTML response untuk HTMX endpoints harus menggunakan class Tailwind yang sudah ada di templates lain
6. Test file yang sudah ada (`crypto_test.go`, `infra_task_test.go`) harus tetap pass setelah perubahan

