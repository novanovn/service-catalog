-- name: CreateTicket :one
INSERT INTO tickets (
    created_by, repo_url, domain, country, service_name, pipeline_name, jira_issue_id, description
) VALUES (
    $1, $2, $3, $4, $5, $6, $7, $8
)
RETURNING id, status, created_at;

-- name: GetTicketByID :one
SELECT id, created_by, repo_url, domain, country, service_name, pipeline_name, jira_issue_id, status, description, created_at, updated_at, integration_id, ai_analysis, ai_analyzed_at, shelf_code, target_env, ticket_type
FROM tickets
WHERE id = $1 LIMIT 1;

-- name: ListTickets :many
SELECT id, created_by, repo_url, domain, country, service_name, pipeline_name, jira_issue_id, status, description, created_at, updated_at, integration_id, ai_analysis, ai_analyzed_at, shelf_code, target_env, ticket_type
FROM tickets
ORDER BY created_at DESC;

-- name: UpdateTicketStatus :one
UPDATE tickets
SET status = $2, updated_at = NOW()
WHERE id = $1
RETURNING id, created_by, repo_url, domain, country, service_name, pipeline_name, jira_issue_id, status, description, created_at, updated_at, integration_id, ai_analysis, ai_analyzed_at, shelf_code, target_env, ticket_type;

-- name: ListTicketsByStatus :many
SELECT id, created_by, repo_url, domain, country, service_name, pipeline_name, jira_issue_id, status, description, created_at, updated_at, integration_id, ai_analysis, ai_analyzed_at, shelf_code, target_env, ticket_type
FROM tickets
WHERE status = $1
ORDER BY created_at DESC;

-- name: ListTicketsPaginated :many
SELECT id, created_by, repo_url, domain, country, service_name, pipeline_name, jira_issue_id, status, description, created_at, updated_at, integration_id, ai_analysis, ai_analyzed_at, shelf_code, target_env, ticket_type
FROM tickets
ORDER BY created_at DESC
LIMIT $1 OFFSET $2;

-- name: CountTickets :one
SELECT COUNT(*) FROM tickets;

-- name: CountTicketsByStatus :one
SELECT COUNT(*) FROM tickets
WHERE status = $1;

-- name: DeleteTicket :exec
DELETE FROM tickets
WHERE id = $1;

-- name: UpdateTicketAIAnalysis :one
UPDATE tickets
SET ai_analysis = $2, ai_analyzed_at = NOW(), updated_at = NOW()
WHERE id = $1
RETURNING id, created_by, repo_url, domain, country, service_name, pipeline_name, jira_issue_id, status, description, created_at, updated_at, integration_id, ai_analysis, ai_analyzed_at, shelf_code, target_env, ticket_type;

-- name: GetTicketAIAnalysis :one
SELECT id, service_name, ai_analysis, ai_analyzed_at
FROM tickets
WHERE id = $1 LIMIT 1;
