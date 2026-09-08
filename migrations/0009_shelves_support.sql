-- Migration 0009: Add Shelves (Product Suites / Integration Groups) Master Data & Catalog Mapping

-- 1. Insert Default Shelves linked by Country & Domain Category
INSERT INTO system_parameters (category, key_name, value, description, is_active) VALUES
    ('shelf', 'ph:neuron', 'Neuron Integration Suite', 'Philippines Neuron Insurance Core & Renewal Services', true),
    ('shelf', 'ph:dtc', 'DTC 2.0 & Care Router', 'Philippines Direct to Consumer & Care Router Gateway', true),
    ('shelf', 'ph:kahoona', 'Kahoona B2C Ecosystem', 'Philippines Kahoona Agent & Broker Quotation Portal', true),
    ('shelf', 'id:coreplus', 'Coreplus Integration Suite', 'Indonesia Coreplus Database & Policy Aggregation System', true),
    ('shelf', 'id:dtc', 'DTC 2.0 Indonesia', 'Indonesia Direct to Consumer Policy Platform', true),
    ('shelf', 'all:devops', 'DevOps & Shared Automation', 'Multi-country Cloud Watchdogs, Replicators & DMS Lambdas', true)
ON CONFLICT (category, key_name) DO UPDATE SET
    value = EXCLUDED.value,
    description = EXCLUDED.description,
    is_active = EXCLUDED.is_active,
    updated_at = NOW();

-- 2. Optional Shelf column on catalog and tickets tables (backward compatible fallback)
ALTER TABLE catalog ADD COLUMN IF NOT EXISTS shelf_code VARCHAR(100);
ALTER TABLE tickets ADD COLUMN IF NOT EXISTS shelf_code VARCHAR(100);

-- 3. Auto-populate existing catalog entries based on Country & Domain
UPDATE catalog SET shelf_code = 'ph:neuron' WHERE (LOWER(country) = 'ph' OR country IS NULL) AND (LOWER(domain) = 'integration' OR domain IS NULL);
UPDATE catalog SET shelf_code = 'id:coreplus' WHERE LOWER(country) = 'id' AND LOWER(domain) = 'integration';
UPDATE catalog SET shelf_code = 'ph:dtc' WHERE LOWER(country) = 'ph' AND LOWER(domain) = 'dtc';
UPDATE catalog SET shelf_code = 'id:dtc' WHERE LOWER(country) = 'id' AND LOWER(domain) = 'dtc';
UPDATE catalog SET shelf_code = 'all:devops' WHERE shelf_code IS NULL;

-- 4. Auto-populate existing tickets table
UPDATE tickets SET shelf_code = 'ph:neuron' WHERE (LOWER(country) = 'ph' OR country IS NULL) AND (LOWER(domain) = 'integration' OR domain IS NULL);
UPDATE tickets SET shelf_code = 'id:coreplus' WHERE LOWER(country) = 'id' AND LOWER(domain) = 'integration';
UPDATE tickets SET shelf_code = 'ph:dtc' WHERE LOWER(country) = 'ph' AND LOWER(domain) = 'dtc';
UPDATE tickets SET shelf_code = 'id:dtc' WHERE LOWER(country) = 'id' AND LOWER(domain) = 'dtc';
UPDATE tickets SET shelf_code = 'all:devops' WHERE shelf_code IS NULL;
