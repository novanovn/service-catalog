-- Migration 0012: User-to-Shelf Ownership (Folder-Level RBAC)
--
-- With 300+ repositories, a flat catalog view is unusable at scale. This migration
-- attaches each non-admin/devops user to one or more shelves (folders), so their
-- default catalog view is scoped to only the folders relevant to their team.
--
-- Admin and DevOps roles are NOT restricted by this column (they retain global
-- visibility regardless of assigned_shelves content) — see application-layer
-- authorization in internal/api/catalog.go / views_catalog.go.

ALTER TABLE users ADD COLUMN IF NOT EXISTS assigned_shelves TEXT[] NOT NULL DEFAULT '{}';

-- Backfill: existing admin/devops accounts get an explicit wildcard marker for clarity
-- in the UI (shows "All Folders (Global)" instead of an empty list).
UPDATE users SET assigned_shelves = ARRAY['*'] WHERE role IN ('admin', 'devops') AND assigned_shelves = '{}';

-- Index to support "find all users attached to shelf X" queries (shelf management UI)
CREATE INDEX IF NOT EXISTS idx_users_assigned_shelves ON users USING GIN (assigned_shelves);
