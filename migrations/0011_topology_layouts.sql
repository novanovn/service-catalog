-- Migration 0011: Topology Layouts Persistence
CREATE TABLE IF NOT EXISTS topology_layouts (
    layout_name VARCHAR(100) PRIMARY KEY,
    positions JSONB NOT NULL,
    updated_by VARCHAR(255) NOT NULL DEFAULT 'system',
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
