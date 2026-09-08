-- name: ListCatalogEntries :many
SELECT id, name, description, domain, country, status, repo_url, pipeline_name, requestor_email, jira_id, aws_last_modified, aws_last_invoked, created_at, updated_at
FROM catalog
WHERE is_active = true
ORDER BY name ASC;

-- name: GetCatalogEntryByName :one
SELECT id, name, description, domain, country, status, repo_url, pipeline_name, requestor_email, jira_id, aws_last_modified, aws_last_invoked, created_at, updated_at
FROM catalog
WHERE name = $1 AND is_active = true;

-- name: GetCatalogEntryByID :one
SELECT id, name, description, domain, country, status, repo_url, pipeline_name, requestor_email, jira_id, aws_last_modified, aws_last_invoked, created_at, updated_at
FROM catalog
WHERE id = $1 AND is_active = true;

-- name: UpsertCatalogEntry :one
INSERT INTO catalog (name, description, domain, country, status, repo_url, pipeline_name, requestor_email, jira_id, aws_last_modified, aws_last_invoked)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11)
ON CONFLICT (name) DO UPDATE SET
    description = EXCLUDED.description,
    domain = EXCLUDED.domain,
    country = EXCLUDED.country,
    status = EXCLUDED.status,
    repo_url = EXCLUDED.repo_url,
    pipeline_name = EXCLUDED.pipeline_name,
    requestor_email = EXCLUDED.requestor_email,
    jira_id = EXCLUDED.jira_id,
    aws_last_modified = EXCLUDED.aws_last_modified,
    aws_last_invoked = EXCLUDED.aws_last_invoked,
    updated_at = NOW()
RETURNING id, name, description, domain, country, status, repo_url, pipeline_name, requestor_email, jira_id, aws_last_modified, aws_last_invoked, created_at, updated_at;

-- name: UpdateCatalogEntry :exec
UPDATE catalog SET
    name = $2,
    description = $3,
    domain = $4,
    country = $5,
    status = $6,
    repo_url = $7,
    pipeline_name = $8,
    requestor_email = $9,
    jira_id = $10,
    aws_last_modified = $11,
    aws_last_invoked = $12,
    updated_at = NOW()
WHERE id = $1;

-- name: DeleteCatalogEntry :exec
UPDATE catalog SET is_active = false, updated_at = NOW() WHERE id = $1;

-- name: ListCatalogEntriesPaginated :many
SELECT id, name, description, domain, country, status, repo_url, pipeline_name, requestor_email, jira_id, aws_last_modified, aws_last_invoked, created_at, updated_at
FROM catalog
WHERE is_active = true
ORDER BY name ASC
LIMIT $1 OFFSET $2;

-- name: CountCatalogEntries :one
SELECT COUNT(*) FROM catalog WHERE is_active = true;

