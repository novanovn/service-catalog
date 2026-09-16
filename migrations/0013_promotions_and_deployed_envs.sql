-- Migration 0013: Promotion Tickets & Multi-Environment Catalog Tracking
--
-- Adds support for promoting services between lifecycle environments (UAT -> PreProd -> Prod)
-- without mutating or overwriting the original Day-1 onboarding ticket audit trail.

ALTER TABLE tickets ADD COLUMN IF NOT EXISTS target_env VARCHAR(20) NOT NULL DEFAULT 'uat';
ALTER TABLE tickets ADD COLUMN IF NOT EXISTS ticket_type VARCHAR(50) NOT NULL DEFAULT 'ONBOARDING';

ALTER TABLE catalog ADD COLUMN IF NOT EXISTS deployed_envs TEXT[] NOT NULL DEFAULT '{uat}';

-- Backfill existing catalog records to have uat deployed
UPDATE catalog SET deployed_envs = ARRAY['uat'] WHERE deployed_envs = '{}' OR deployed_envs IS NULL;

-- Index tickets by type & target_env for rapid filtering in approvals dashboard
CREATE INDEX IF NOT EXISTS idx_tickets_target_env ON tickets(target_env);
CREATE INDEX IF NOT EXISTS idx_tickets_ticket_type ON tickets(ticket_type);
