-- name: InsertAuditLog :exec
INSERT INTO audit_logs (
    action, entity_type, entity_id, user_id, user_email, ip_address, details
) VALUES (
    $1, $2, $3, $4, $5, $6, $7
);

-- name: ListAuditLogs :many
SELECT id, action, entity_type, entity_id, user_id, user_email, ip_address, details, created_at
FROM audit_logs
ORDER BY created_at DESC
LIMIT $1 OFFSET $2;

-- name: CountAuditLogs :one
SELECT COUNT(*) FROM audit_logs;
