-- name: GetUserByEmail :one
SELECT id, email, full_name, password_hash, role, is_active, created_at, updated_at, assigned_shelves
FROM users
WHERE email = $1 LIMIT 1;

-- name: GetUserByID :one
SELECT id, email, full_name, password_hash, role, is_active, created_at, updated_at, assigned_shelves
FROM users
WHERE id = $1 LIMIT 1;

-- name: CreateUser :one
INSERT INTO users (
    email, full_name, password_hash, role, assigned_shelves
) VALUES (
    $1, $2, $3, $4, $5
)
RETURNING id, email, full_name, role, is_active, assigned_shelves, created_at, updated_at;

-- name: ListUsers :many
SELECT id, email, full_name, password_hash, role, is_active, created_at, updated_at, assigned_shelves
FROM users
ORDER BY created_at DESC;

-- name: UpdateUserShelves :one
UPDATE users
SET assigned_shelves = $2, updated_at = NOW()
WHERE id = $1
RETURNING id, email, full_name, role, is_active, assigned_shelves, created_at, updated_at;

-- name: UpdateUser :one
UPDATE users
SET 
    full_name = $2,
    role = $3,
    is_active = $4,
    password_hash = CASE 
        WHEN sqlc.arg('password_hash')::text != '' THEN sqlc.arg('password_hash')::text 
        ELSE password_hash 
    END,
    updated_at = NOW()
WHERE id = $1
RETURNING id, email, full_name, role, is_active, assigned_shelves, created_at, updated_at;

-- name: DeleteUser :exec
DELETE FROM users WHERE id = $1;
