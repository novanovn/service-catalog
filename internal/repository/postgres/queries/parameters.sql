-- name: GetSystemParameterByID :one
SELECT id, category, key_name, value, description, is_active, created_at, updated_at FROM system_parameters
WHERE id = $1 LIMIT 1;

-- name: GetSystemParameterByCategoryAndKey :one
SELECT id, category, key_name, value, description, is_active, created_at, updated_at FROM system_parameters
WHERE category = $1 AND key_name = $2 LIMIT 1;

-- name: ListSystemParameters :many
SELECT id, category, key_name, value, description, is_active, created_at, updated_at FROM system_parameters
ORDER BY category ASC, key_name ASC;

-- name: ListSystemParametersByCategory :many
SELECT id, category, key_name, value, description, is_active, created_at, updated_at FROM system_parameters
WHERE category = $1 AND is_active = true
ORDER BY key_name ASC;

-- name: CreateSystemParameter :one
INSERT INTO system_parameters (
    category, key_name, value, description, is_active
) VALUES (
    $1, $2, $3, $4, $5
)
RETURNING id, category, key_name, value, description, is_active, created_at, updated_at;

-- name: UpdateSystemParameter :one
UPDATE system_parameters
SET value = $3, description = $4, is_active = $5, updated_at = NOW()
WHERE category = $1 AND key_name = $2
RETURNING id, category, key_name, value, description, is_active, created_at, updated_at;

-- name: UpdateSystemParameterByID :one
UPDATE system_parameters
SET key_name = $2, value = $3, description = $4, is_active = $5, updated_at = NOW()
WHERE id = $1
RETURNING id, category, key_name, value, description, is_active, created_at, updated_at;

-- name: DeleteSystemParameter :exec
DELETE FROM system_parameters
WHERE id = $1;
