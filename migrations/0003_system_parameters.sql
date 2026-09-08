-- Migration 0003: System Parameters Database Schema & Master Data

CREATE TABLE IF NOT EXISTS system_parameters (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    category VARCHAR(50) NOT NULL,
    key_name VARCHAR(100) NOT NULL,
    value VARCHAR(255) NOT NULL,
    description TEXT,
    is_active BOOLEAN NOT NULL DEFAULT true,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    CONSTRAINT uk_system_parameters_category_key UNIQUE (category, key_name)
);

CREATE INDEX IF NOT EXISTS idx_system_parameters_category ON system_parameters(category);

-- Seed Default Master Data
INSERT INTO system_parameters (category, key_name, value, description) VALUES
    ('country', 'ID', 'Indonesia', 'Oona Indonesia Entity'),
    ('country', 'PH', 'Philippines', 'Oona Philippines Entity'),
    ('product', 'Health', 'Health Insurance', 'Health Insurance Product & Services'),
    ('product', 'Motor', 'Motor Insurance', 'Motor Insurance Product & Services'),
    ('product', 'Travel', 'Travel Insurance', 'Travel Insurance Product & Services'),
    ('product', 'Claims', 'Claims Service', 'Claims Processing & Management'),
    ('product', 'Policy', 'Policy Service', 'Policy Administration Engine'),
    ('product', 'Payment', 'Payment Gateway', 'Payment Gateway Integrations'),
    ('environment', 'UAT', 'User Acceptance Testing', 'UAT Staging Environment'),
    ('environment', 'PreProd', 'Pre-Production', 'Pre-Production Staging Environment'),
    ('environment', 'Prod', 'Production', 'Live Production Environment')
ON CONFLICT (category, key_name) DO NOTHING;
