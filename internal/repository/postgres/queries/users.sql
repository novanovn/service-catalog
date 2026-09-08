-- name: GetUserByEmail :one
SELECT * FROM users
WHERE email = $1 LIMIT 1;

-- name: CreateUser :one
INSERT INTO users (
    email, full_name, password_hash, role
) VALUES (
    $1, $2, $3, $4
)
RETURNING id, email, full_name, role, is_active, created_at, updated_at;

-- name: ListUsers :many
SELECT * FROM users
ORDER BY created_at DESC;
