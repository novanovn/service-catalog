-- Integration Settings Table (Admin Only)

CREATE TYPE integration_provider AS ENUM ('jenkins', 'gitlab', 'github_actions', 'jira', 'terraform_repo');

CREATE TABLE integrations (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    name VARCHAR(100) NOT NULL,            -- e.g. "Oona Main Jenkins"
    provider integration_provider NOT NULL,
    base_url VARCHAR(255) NOT NULL,
    auth_user VARCHAR(100),                -- For Jenkins (Username)
    auth_token VARCHAR(500) NOT NULL,      -- AES Encrypted Token (Jenkins API Token / Gitlab PAT)
    is_active BOOLEAN NOT NULL DEFAULT true,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

-- Associate Tickets with specific integration engines
ALTER TABLE tickets 
ADD COLUMN integration_id UUID REFERENCES integrations(id);
