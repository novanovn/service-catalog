# lmd-oona-ph-integration-health-renewal-svc

Generates a Neuron QR code, submits a health renewal full quote to Insuremo, records the
outcome (identifiers + full request/response) in `NEURON_DB_NAME` for traceability, and publishes
a renewal reminder onto the common reminder service's event bus.

## Functions

| Lambda name | Trigger | Description |
|-------------|---------|--------------|
| `neuron-health-renewal-create-quote-v1-svc` | API Gateway `POST /v1/health-service/renewal/create-quote` | Generates a QR code via Neuron, submits `renewalFullQuote` to Insuremo, persists the result to `health_renewal_quotes`, and publishes a reminder event (`NETCORE_*`) built from the Insuremo response. |

**Note:** the "common reminder service" target does not exist yet. `NETCORE_EB_NAME` is a
placeholder SSM parameter — set it once that service is built and its bus is provisioned. Same
placeholder used by `lmd-oona-ph-integration-tesla-renewal-svc`'s reminder lambda, but with a
health-specific `NETCORE_CREATE_REMINDER_EB_DETAIL_TYPE`.

**Note:** there is no separate reminder-triggering lambda or API Gateway route for this
domain — the reminder is built and published inline as part of `create-quote`, right after a
successful `renewalFullQuote` call, using that response's own contact/product/date fields (no
extra Insuremo call, no `{key}` encryption). A publish failure is logged but does not fail the
request — Insuremo and `NEURON_DB_NAME` are already the source of truth for the quote by that
point (see `publishRenewalReminder` in `src/create-quote/services/create-quote.service.ts`).

**Note:** Insuremo's `renewalFullQuote` response shape is unconfirmed — the contact/name/date
fields `src/create-quote/services/create-reminder.service.ts` reads off it (`mobile`, `email`,
`fullName`, `firstName`, `lastName`, `startDate`, `endDate`) are best-guess, marked with `// TODO`
comments. `reminder_renewal_detaillink` is left as an empty string pending a product decision on
what a customer-facing link should point to now that there's no dedicated serving endpoint.
Confirm with Insuremo/Care before going live.

## Flow

1. Receive the Care team's request payload from API Gateway (shape not yet finalized — treated as
   an open object; see `src/create-quote/interfaces/create-quote.interface.ts`).
2. Call Neuron's QR code API (`NEURON_SHARED_BASE_URL` + `NEURON_QR_CODE_PATH`) to get a base64 QR code.
3. Call Insuremo's `renewalFullQuote` (`INSUREMO_API_BASE_URL` + `INSUREMO_RENEWAL_FULL_QUOTE_CONTEXT_PATH`)
   with the original payload plus the QR code.
4. On a successful Insuremo call, map that response's contact/name/date fields into the common
   reminder service's payload shape and publish it to `NETCORE_EB_NAME` (best-effort — logged,
   not fatal, on failure).
5. Persist one row to `health_renewal_quotes` in `NEURON_DB_NAME` — PPNO, ReferenceID, CARE
   Quotation No, InsureMO Proposal No, Remarks, and the full request/response payloads. This is
   written even if the Insuremo call fails, so every attempt is auditable.
6. Respond via `oona-common-libs`' `BaseService`: `baseResponse(HttpStatusCode.OK, 'Successfully
   created health renewal quote')` on success (the caller doesn't need Insuremo's response body
   back), and `handlingErrorResponse(err)` on any thrown error (handled in `src/index.ts`).

## Known open items (flag before going to a real environment)

- **`src/create-quote/index.ts` currently returns a dummy success response and never calls
  `processCreateQuote`** (bypasses Neuron QR, Insuremo, the DB write, and now the reminder
  publish too — see the "Temporary dummy response" comment in that file). The reminder-publish
  code added to `processCreateQuote` won't run in production until that bypass is removed; that's
  a separate, deliberate decision for the team to make, not done as part of this change.
- **Neuron QR API auth is unconfirmed.** No existing service in this org calls this endpoint yet.
  `src/create-quote/services/neuron-qr-code.service.ts` sends a generic header from `NEURON_API_AUTH_HEADER_NAME`
  / `NEURON_API_AUTH_SECRET_NAME` if both are configured; confirm the real scheme with Neuron/Care
  and adjust.
- **Field mapping is best-guess.** Care's request payload and Insuremo's `renewalFullQuote`
  response shape aren't finalized. `src/create-quote/services/create-quote.service.ts` and
  `src/create-quote/services/insuremo.service.ts` extract `ppno`/`referenceId`/`careQuotationNo` (request) and
  `proposalNo`/`message` (response) by best-guess key names; `src/create-quote/services/create-reminder.service.ts`
  similarly guesses at contact/name/date field names. All marked with `// TODO` comments — the
  full raw payloads are always stored regardless, so nothing is lost while these are pending.

## Local development

```bash
cp .env.template .env
# fill in .env values
npm install
npm run build
npm run test
```

## Deployment

Deployed via Jenkins (`nodeLambdaTerraformPipeline`). All secrets are sourced from AWS Secrets Manager /
SSM Parameter Store — never commit values to this repo. There is no `serverless.yml`; devops
provisions the Lambda, API Gateway route, VPC config, and env vars via Terraform.

### Devops handoff

- **Lambda name / handler:** `neuron-health-renewal-create-quote-v1-svc` / `dist/index.healthCreateQuoteHandler`
- **Trigger:** API Gateway `POST /v1/health-service/renewal/create-quote`
- **VPC:** needs reachability to the Postgres instance backing `NEURON_DB_NAME` — same
  security group/subnets as `neuron-callback-care-policy-svc` / `neuron-database-svc`
- **IAM:** `secretsmanager:GetSecretValue` for `INSUREMO_KEY_SECRET_NAME` and the (placeholder)
  Neuron auth secret; `events:PutEvents` on `NETCORE_EB_NAME` for the reminder publish.

## SSM parameters / env vars

See `.env.template` for the full list:

- `NEURON_SHARED_BASE_URL`, `NEURON_QR_CODE_PATH`, `NEURON_API_AUTH_HEADER_NAME`,
  `NEURON_API_AUTH_SECRET_NAME`
- `INSUREMO_API_BASE_URL`, `INSUREMO_RENEWAL_FULL_QUOTE_CONTEXT_PATH`, `INSUREMO_KEY_SECRET_NAME`,
  `INSUREMO_MO_TENANT_ID`, `INSUREMO_EBAO_TENANT_ID`
- `NEURON_DB_HOST`, `NEURON_DB_PORT`, `NEURON_DB_USERNAME`, `NEURON_DB_PASSWORD`,
  `NEURON_DB_NAME`, `NEURON_DB_SCHEMA`
- `NETCORE_EB_NAME`, `NETCORE_CREATE_REMINDER_EB_SOURCE`, `NETCORE_CREATE_REMINDER_EB_DETAIL_TYPE`
  — common reminder service event bus (placeholder, not built yet)
- `REMINDER_PORTAL_ID`, `REMINDER_PRODUCT_CODE` — reminder payload fields

## Database

`sql/001_create_health_renewal_quotes.up.sql` creates the `health_renewal_quotes` table in
`NEURON_DB_NAME` (hand to DBA/infra to run manually — no migration tool is configured for this
DB anywhere in the org). Rollback: `sql/001_create_health_renewal_quotes.down.sql`.
