-- Migration: Add missing columns to catalog table for full service catalog persistence
-- These columns were previously only tracked in-memory and lost on restart

ALTER TABLE catalog ADD COLUMN IF NOT EXISTS repo_url VARCHAR(512) DEFAULT '';
ALTER TABLE catalog ADD COLUMN IF NOT EXISTS domain VARCHAR(100) DEFAULT '';
ALTER TABLE catalog ADD COLUMN IF NOT EXISTS country VARCHAR(10) DEFAULT '';
ALTER TABLE catalog ADD COLUMN IF NOT EXISTS status VARCHAR(50) NOT NULL DEFAULT 'PENDING_INFRA';
ALTER TABLE catalog ADD COLUMN IF NOT EXISTS pipeline_name VARCHAR(255) DEFAULT '';

-- Seed the default service entry if it doesn't exist
INSERT INTO catalog (name, description, domain, country, status, repo_url, pipeline_name)
VALUES (
    'health-renewal-svc-clone',
    'Generates a Neuron QR code, submits a health renewal full quote to Insuremo, records the outcome in NEURON_DB_NAME for traceability, and publishes a renewal reminder onto the common reminder service''s event bus.',
    'Integration',
    'PH',
    'LIVE',
    'https://github.com/oona-insurance/lmd-oona-ph-integration-health-renewal-svc-clone',
    'lmd-oona-ph-integration-health-renewal-svc-clone'
)
ON CONFLICT (name) DO UPDATE SET
    repo_url = EXCLUDED.repo_url,
    domain = EXCLUDED.domain,
    country = EXCLUDED.country,
    status = EXCLUDED.status,
    pipeline_name = EXCLUDED.pipeline_name;
