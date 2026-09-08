# Oona Developer Portal — Full Codebase Review

This is a Go-based internal developer portal (IDP) that automates Lambda service onboarding at Oona Insurance, driving a state machine from DRAFT through security scanning, Terraform IaC detection, and Jenkins pipeline creation to LIVE. The architecture is sound for an MVP: Chi HTTP router for the API/UI tier, Asynq over Valkey for the background worker tier, PostgreSQL as the system of record, and a clean separation between the HTTP layer and the worker engine.

Watch for: **hardcoded secrets in source code** (JWT signing key and AES encryption key are literal strings committed to the repo — confirmed); **the database layer is entirely missing** (every DB call is a comment stub, meaning the state machine cannot actually advance — confirmed); and **the Trivy scan result is silently discarded** without updating ticket status even when vulnerabilities are found — confirmed.

**Verdict**: NEEDS_CHANGES

---

## High-level view

The JWT secret (`OONA_SUPER_SECRET_KEY_CHANGE_IN_PROD`) and the AES-256 encryption key (`OONA_AES256_VAULT_KEY_DO_NOT_SHARE!`) are hardcoded as package-level `var` declarations in `auth/jwt.go` and `auth/crypto.go`. Rotating either requires a code change and redeployment, and both are already committed to version control.

The database layer declared in `sqlc.yaml` has no generated code and no query files. Every handler and every worker task stubs out DB calls with comments. The state machine transition logic is correct, but nothing writes or reads ticket state from PostgreSQL at runtime — the portal is a UI demo until this is built.

The Trivy scan worker runs correctly and counts CRITICAL/HIGH vulnerabilities, but returns `nil` on both pass and fail paths. Ticket status is never persisted, and no notification task is enqueued on rejection. The same gap affects every worker: `HandleVerifyInfraGitTask`, `HandleCheckAWSLambdaTask`, and `HandleCreatePipelineTask` all reach their success path and do nothing with it.

The `infra/parser.go` HCL parser always returns a hardcoded slice. The infra detection test passes because it only exercises the file-existence check, not the parsing — a false confidence signal that will surface only when the stub is replaced.

The Jenkins log handler hardcodes `DUMMY_TOKEN_FROM_DB` and constructs the pipeline name from literal country/domain strings. Log lines are injected into HTML without escaping, creating a XSS vector if Jenkins console output contains user-controlled content from commit messages or echo steps.

The auth cookie has `Secure: false` hardcoded. The `docker-compose.yml` embeds `secretpassword` as a plaintext Postgres password in the environment block and in `DB_DSN`. The `RequireRole` middleware is implemented but applied to zero routes, meaning a developer-role JWT holder can hit the approve endpoint once it's wired up.

---

<details>
<summary>Issues (12)</summary>

1. **Hardcoded JWT signing key** — `jwtSecret` in `auth/jwt.go` is a literal string. Move to an env var (`JWT_SECRET`); fail fast at startup if unset. Rotating requires a code change today.

2. **Hardcoded AES encryption key** — `encryptionKey` in `auth/crypto.go` is a 35-byte literal; all integration tokens in the DB are encrypted with this key. Load from env var, enforce exactly 32 bytes, fail fast if wrong.

3. **Database layer entirely missing** — `sqlc.yaml` points at a queries directory that doesn't exist; no code generated, no DB connection initialized in `main.go`. The portal cannot persist or read any state at runtime.

4. **Trivy result silently discarded** — `HandleTrivyScanTask` returns `nil` on both pass and fail paths. Ticket status is never updated; the developer receives no feedback on rejection. Enqueue a `TypeNotifyEmail` or `TypeNotifyTeams` task on rejection and update status once the DB layer exists.

5. **HCL parser is a stub** — `infra/parser.go` returns `["DB_HOST", "DB_PASSWORD"]` unconditionally. The passing infra task test gives no signal about actual env-var extraction. The real parser needs unit tests with HCL fixture files covering missing blocks, empty maps, and malformed syntax.

6. **Hardcoded credential in logs handler** — `logs_handler.go` uses `AuthToken: "DUMMY_TOKEN_FROM_DB"` and hardcoded `"ph"` / `"integration"` values. No real Jenkins token will ever be retrieved until this is replaced with a DB lookup keyed on the service record.

7. **XSS in Jenkins log output** — `FetchJenkinsLogs` wraps raw log lines in `<span>` tags without `html.EscapeString`. Jenkins console output can contain user-controlled content from commit messages or Jenkinsfile echo steps.

8. **`Secure: false` cookie hardcoded** — `HandleLogin` commits the JWT to HTTP transport in production. Drive this from an env var (`APP_ENV` or `HTTPS_ENABLED`) rather than a code comment to remember.

9. **`RequireRole` middleware never applied** — Defined in `middleware.go`, used on zero routes. The approve endpoint and all `/admin/*` routes need `r.With(RequireRole("devops", "admin"))` applied before the handlers are real.

10. **`TRIGGERED_BY_PORTAL` value unescaped in URL** — `TriggerBuild` interpolates the approver's email directly into a query string. Emails with `+` corrupt the parameter. Use `url.Values.Encode()`.

11. **docker-compose plaintext Postgres password** — `secretpassword` hardcoded in compose and in `DB_DSN`. Use `${POSTGRES_PASSWORD}` with a gitignored `.env` file, especially since the same compose file mounts `~/.aws` into the worker container.

12. **Committed build artifact** — The `server` binary at the repo root should be gitignored. The Dockerfile builds from source; there is no deployment reason to commit the binary.

</details>

---

<details>
<summary>Details</summary>

### Hardcoded secrets: JWT key and AES vault key

`auth/jwt.go` declares `var jwtSecret = []byte("OONA_SUPER_SECRET_KEY_CHANGE_IN_PROD")` at package scope. `auth/crypto.go` declares `var encryptionKey = []byte("OONA_AES256_VAULT_KEY_DO_NOT_SHARE!")`. The comment in `crypto.go` correctly identifies that this must come from an env var, but the current fallback is the literal key rather than a startup failure — meaning production will silently use the committed key unless the env var is explicitly set.

```go
// Correct pattern — in main.go or a config init
secret := os.Getenv("JWT_SECRET")
if len(secret) < 32 {
    log.Fatal("JWT_SECRET env var must be at least 32 bytes")
}
auth.SetJWTSecret([]byte(secret))
```

The AES key has a secondary issue: the literal is 35 bytes, but `crypto.go` slices it to `[:32]` in both `Encrypt` and `Decrypt`. This is internally consistent but silent — the last 3 bytes are always ignored. When moved to an env var, the contract must be documented as exactly 32 bytes; a 35-byte env value would silently produce different ciphertext than the hardcoded key, breaking decryption of anything already stored in the DB.

### Trivy scan: silent pass-through on rejection

When `criticalCount > 0 || highCount > 5`, the handler logs the rejection message and returns `nil`:

```go
// trivy_task.go
rejectMsg := fmt.Errorf("rejected: found %d CRITICAL and %d HIGH vulnerabilities", criticalCount, highCount)
log.Println(rejectMsg)
// Here we would update DB: db.UpdateTicketStatus(p.TicketID, "REJECTED_SECURITY")
return nil
```

Returning `nil` is the right Asynq signal — the scan process itself succeeded. But without the DB write, a ticket with 10 critical CVEs sits in `SCANNING` forever with no developer feedback. The notification worker (`tasks/notification.go`) is already implemented; the rejection branch just needs to enqueue a `TypeNotifyTeams` task before returning.

The pass threshold (`highCount > 5`) also has no documented rationale. Five HIGH vulnerabilities is a non-obvious policy cutoff that should be a named constant with a comment explaining the business decision.

### XSS in Jenkins log rendering

`FetchJenkinsLogs` constructs colored HTML by string-interpolating raw log lines:

```go
styledLogs.WriteString(fmt.Sprintf("<span class='text-red-400'>%s</span>\n", line))
```

The `line` variable comes directly from Jenkins console output, which includes anything echoed by Jenkinsfile steps — including content derived from git commit messages, branch names, or test output. None of these are HTML-encoded. The resulting string is inserted into a `<pre>` block via `fmt.Sprintf("%s", styledLogs)` in `logs_handler.go`.

Fix: `html.EscapeString(line)` before the Sprintf in every branch of the colorizer loop.

### `RequireRole` never applied

`RequireRole` in `middleware.go` is implemented correctly — it reads claims from context and rejects on role mismatch. No route in `router.go` uses it. The DevOps approval endpoint and all admin routes run under `AuthMiddleware` only, which validates the JWT but ignores the role claim. When the approve handler becomes real, any logged-in user can approve their own ticket:

```go
// router.go — current state
r.Post("/api/v1/tickets/{id}/approve", DummyHandler("Approve Jenkins Creation"))

// Should be
r.With(RequireRole("devops", "admin")).Post("/api/v1/tickets/{id}/approve", ApproveTicketHandler)
```

The same applies to `/admin/integrations` and `/admin/users`.

### `TRIGGERED_BY_PORTAL` URL injection

```go
// engine.go TriggerBuild
url := fmt.Sprintf("...buildWithParameters?TRIGGERED_BY_PORTAL=%s", triggeredBy)
```

`triggeredBy` is the email from JWT claims. Emails containing `+` (e.g. `user+alias@oona-insurance.com`) will be truncated or malform the query string. Jenkins will receive a different value than intended or reject the request entirely.

```go
params := url.Values{}
params.Set("TRIGGERED_BY_PORTAL", triggeredBy)
fullURL := fmt.Sprintf("%s/job/AWS%%20Lambda%%20Projects/job/%s/buildWithParameters?%s",
    j.Config.BaseURL, jobName, params.Encode())
```

### Logout via GET

`HandleLogout` is registered as `r.Get("/logout", ...)`. A third-party page can silently log out any authenticated user by including `<img src="https://portal.../logout">`. Because `SameSite: Lax` is set on the cookie, this doesn't allow login CSRF, but it does allow logout CSRF which disrupts workflows. Change to a `POST` endpoint with the HTMX form in the UI sending a POST, or add a `Referer` header check as a lightweight mitigation.

### Test coverage gaps

The existing tests (`crypto_test.go` round-trip, `infra_task_test.go` file-detection) are well-written. Missing coverage:

- State machine: no test for the `INFRA_DETECTED → JENKINS_READY` role guard — the only transition that enforces `ErrUnauthorized`, and the core of the DevOps approval gate
- JWT validation: no test for expired token, wrong signing method, or malformed header — `ValidateToken` has three distinct failure modes
- `AuthMiddleware`: cookie path, Authorization header path, and the HTML-redirect vs JSON-401 branch decision are all untested
- Trivy policy: no test that a scan with CRITICAL vulns causes `REJECTED_SECURITY` rather than advancing the ticket
- `RequireRole`: no test that a developer-role token is rejected on a devops-only route

</details>

---

<details>
<summary>File map</summary>

| File | What it does |
|---|---|
| `cmd/server/main.go` | Entrypoint; dual-mode `api` / `worker` via CLI arg |
| `internal/api/router.go` | Chi router setup; all routes defined here |
| `internal/api/middleware.go` | JWT auth middleware + `RequireRole` RBAC (unused) |
| `internal/api/views.go` | All HTML handlers: login, dashboard, catalog, approvals, admin |
| `internal/api/tickets.go` | `POST /api/v1/tickets` JSON handler |
| `internal/api/logs_handler.go` | HTMX endpoint for Jenkins log tail (hardcoded credentials) |
| `internal/auth/jwt.go` | JWT generation and validation (hardcoded secret) |
| `internal/auth/crypto.go` | AES-256-GCM encrypt/decrypt (hardcoded key) |
| `internal/auth/crypto_test.go` | Round-trip test for encrypt/decrypt |
| `internal/core/statemachine.go` | Ticket state transition validator |
| `internal/models/models.go` | Ticket and user role constants and structs |
| `internal/docs/markdown.go` | Goldmark markdown renderer; stub git fetch |
| `internal/notify/smtp.go` | SMTP email sender (TLS + STARTTLS fallback) |
| `internal/notify/teams.go` | MS Teams webhook sender |
| `internal/tasks/trivy_task.go` | Asynq task: runs trivy CLI, evaluates CVE policy |
| `internal/tasks/infra_task.go` | Asynq task: git-clones Terraform repo, checks for tfvars |
| `internal/tasks/aws_task.go` | Asynq task: polls AWS Lambda existence (MaxRetry 50) |
| `internal/tasks/ci_task.go` | Asynq task: creates Jenkins multibranch pipeline |
| `internal/tasks/notification.go` | Asynq tasks for Teams + email notifications |
| `internal/tasks/infra_task_test.go` | Tests file-detection logic; parser stub not exercised |
| `internal/worker/server.go` | Asynq server with queue priorities |
| `internal/worker/aws/client.go` | AWS SDK Lambda client (GetFunction + Invoke) |
| `internal/worker/ci/engine.go` | Jenkins + GitLab `PipelineEngine` implementations |
| `internal/worker/ci/jenkins_logs.go` | Jenkins console log fetcher with HTML colorization (XSS) |
| `internal/worker/ci/jira.go` | Jira `IssueTracker` implementation |
| `internal/worker/infra/parser.go` | HCL tfvars parser — stub, always returns hardcoded slice |
| `migrations/0001_init.sql` | Core schema: users, tickets, ticket_envs, catalog |
| `migrations/0002_integrations.sql` | Integrations table; adds `integration_id` FK to tickets |
| `sqlc.yaml` | SQLC config (queries directory missing; no code generated) |
| `docker-compose.yml` | Postgres + Valkey + api + worker; plaintext password in env |
| `Dockerfile` | Multi-stage build; non-root user; git in final image |
| `Dockerfile.dev` | Dev image; Go 1.23 (mismatches go.mod 1.24) |
| `internal/templates/*.html` | Go HTML templates: layout, login, dashboard, catalog, approvals, admin |
| `server` | Committed binary — should be gitignored |

</details>
