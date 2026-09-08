-- name: CreateTicketReview :one
INSERT INTO ticket_reviews (
    ticket_id, status, comment, reviewed_by, security_acknowledged, security_notes
) VALUES (
    $1, $2, $3, $4, $5, $6
)
RETURNING id, ticket_id, status, comment, reviewed_by, security_acknowledged, security_notes, created_at;

-- name: GetTicketReviewByTicketID :one
SELECT id, ticket_id, status, comment, reviewed_by, security_acknowledged, security_notes, created_at
FROM ticket_reviews
WHERE ticket_id = $1
ORDER BY created_at DESC
LIMIT 1;

-- name: ListTicketReviews :many
SELECT id, ticket_id, status, comment, reviewed_by, security_acknowledged, security_notes, created_at
FROM ticket_reviews
ORDER BY created_at DESC;
