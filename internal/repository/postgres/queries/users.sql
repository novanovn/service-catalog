-- name: GetUserByEmail :one
SELECT * FROM users
WHERE email = $1 LIMIT 1;

-- name: CreateUser :one
INSERT INTO users (
    email, full_name, password_hash, role, assigned_shelves
) VALUES (
    $1, $2, $3, $4, $5
)
RETURNING id, email, full_name, role, is_active, assigned_shelves, created_at, updated_at;

-- name: ListUsers :many
SELECT * FROM users
ORDER BY created_at DESC;

-- name: UpdateUserShelves :one
UPDATE users
SET assigned_shelves = $2, updated_at = NOW()
WHERE id = $1
RETURNING id, email, full_name, role, is_active, assigned_shelves, created_at, updated_at;

-- name: DeleteUser :exec
DELETE FROM users WHERE id = $1;
