-- Oona Dev Portal Schema (Secure Design)

-- Enable pgcrypto for UUID generation
CREATE EXTENSION IF NOT EXISTS pgcrypto;

-- Enums
CREATE TYPE user_role AS ENUM ('developer', 'devops', 'admin');
CREATE TYPE ticket_status AS ENUM (
    'DRAFT', 
    'SCANNING', 
    'WAITING_INFRA', 
    'INFRA_DETECTED', 
    'JENKINS_READY', 
    'LIVE', 
    'REJECTED_SECURITY'
);

-- Users Table
CREATE TABLE users (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    email VARCHAR(255) UNIQUE NOT NULL,
    full_name VARCHAR(255) NOT NULL,
    password_hash VARCHAR(255) NOT NULL, -- Will store bcrypt hashes only
    role user_role NOT NULL DEFAULT 'developer',
    is_active BOOLEAN NOT NULL DEFAULT true,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

-- Tickets Table (Onboarding Requests)
CREATE TABLE tickets (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    created_by UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    repo_url VARCHAR(255) NOT NULL,
    domain VARCHAR(50) NOT NULL,
    country VARCHAR(10) NOT NULL,
    service_name VARCHAR(100) NOT NULL,
    pipeline_name VARCHAR(255) NOT NULL, -- Auto-generated: lmd-oona-{country}-{domain}-{service_name}
    jira_issue_id VARCHAR(50),           -- Optional Jira ID (e.g. OONA-1234)
    status ticket_status NOT NULL DEFAULT 'DRAFT',
    description TEXT NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

-- Ticket ENV Variables
CREATE TABLE ticket_envs (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    ticket_id UUID NOT NULL REFERENCES tickets(id) ON DELETE CASCADE,
    env_target VARCHAR(20) NOT NULL, -- uat, preprod, prod
    key_name VARCHAR(100) NOT NULL,
    value TEXT NOT NULL,
    is_secret BOOLEAN NOT NULL DEFAULT false, -- If true, value should be masked in UI
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

-- Service Catalog (Live Services)
CREATE TABLE catalog (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    ticket_id UUID REFERENCES tickets(id) ON DELETE SET NULL,
    name VARCHAR(255) UNIQUE NOT NULL,
    description TEXT NOT NULL,
    owner_id UUID REFERENCES users(id) ON DELETE SET NULL,
    swagger_url VARCHAR(255),
    is_active BOOLEAN NOT NULL DEFAULT true,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

-- Indexes for performance
CREATE INDEX idx_tickets_status ON tickets(status);
CREATE INDEX idx_ticket_envs_ticket_id ON ticket_envs(ticket_id);

-- Insert Default Admin User (Password: admin123)
-- bcrypt hash for 'admin123'
INSERT INTO users (email, full_name, password_hash, role) 
VALUES ('admin@oona-insurance.com', 'System Admin', '$2a$10$7qgHLF6j.LPW8cMtqfBk4.YmyOGKo4jXt9cH2Ep5EFu9r8ethrRuq', 'admin')
ON CONFLICT (email) DO NOTHING;

