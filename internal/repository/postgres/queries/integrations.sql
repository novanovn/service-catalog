-- name: GetIntegrationByID :one
SELECT * FROM integrations
WHERE id = $1 LIMIT 1;

-- name: ListIntegrations :many
SELECT * FROM integrations
ORDER BY created_at DESC;

-- name: GetActiveIntegrations :many
SELECT * FROM integrations
WHERE is_active = true
ORDER BY created_at ASC;

-- name: CreateIntegration :one
INSERT INTO integrations (
    name, provider, base_url, auth_user, auth_token, is_active
) VALUES (
    $1, $2, $3, $4, $5, true
)
RETURNING id, name;

-- name: DeleteIntegration :exec
DELETE FROM integrations
WHERE id = $1;

-- name: UpdateIntegration :exec
UPDATE integrations
SET name = $2,
    provider = $3,
    base_url = $4,
    auth_user = $5,
    auth_token = CASE WHEN $6 != '' THEN $6 ELSE auth_token END,
    is_active = $7,
    updated_at = NOW()
WHERE id = $1;

