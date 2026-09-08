-- 0006_audit_logs.sql: Centralized Audit Trail Table for Compliance
CREATE TABLE IF NOT EXISTS audit_logs (
    id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    action      VARCHAR(50) NOT NULL,        -- CREATE, UPDATE, DELETE, APPROVE, REJECT, EXPORT, RESTORE
    entity_type VARCHAR(50) NOT NULL,        -- ticket, catalog, user, integration, parameter, backup
    entity_id   VARCHAR(255),                -- UUID or identifier of affected entity
    user_id     UUID,                        -- Who performed the action
    user_email  VARCHAR(255),                -- Denormalized for quick reads
    ip_address  VARCHAR(45),                 -- Client IP
    details     JSONB,                       -- Arbitrary context (old value, new value, etc.)
    created_at  TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_audit_logs_entity ON audit_logs(entity_type, entity_id);
CREATE INDEX IF NOT EXISTS idx_audit_logs_user ON audit_logs(user_id);
CREATE INDEX IF NOT EXISTS idx_audit_logs_created ON audit_logs(created_at DESC);
