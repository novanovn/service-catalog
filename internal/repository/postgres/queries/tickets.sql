-- name: CreateTicket :one
INSERT INTO tickets (
    created_by, repo_url, domain, country, service_name, pipeline_name, jira_issue_id, description
) VALUES (
    $1, $2, $3, $4, $5, $6, $7, $8
)
RETURNING id, status, created_at;

-- name: GetTicketByID :one
SELECT * FROM tickets
WHERE id = $1 LIMIT 1;

-- name: ListTickets :many
SELECT * FROM tickets
ORDER BY created_at DESC;

-- name: UpdateTicketStatus :one
UPDATE tickets 
SET status = $2, updated_at = NOW()
WHERE id = $1
RETURNING *;

-- name: ListTicketsByStatus :many
SELECT * FROM tickets
WHERE status = $1
ORDER BY created_at DESC;

-- name: ListTicketsPaginated :many
SELECT * FROM tickets
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
RETURNING *;

-- name: GetTicketAIAnalysis :one
SELECT id, service_name, ai_analysis, ai_analyzed_at
FROM tickets
WHERE id = $1 LIMIT 1;

