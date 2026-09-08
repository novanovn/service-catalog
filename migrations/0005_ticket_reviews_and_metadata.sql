-- Migration: 0005_ticket_reviews_and_metadata.sql
-- Description: Create ticket_reviews table for approval audit trails, add metadata columns to catalog, and add high-performance database indexes.

-- 1. Create Ticket Reviews Table for Persistent Approvals & Rejections
CREATE TABLE IF NOT EXISTS ticket_reviews (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    ticket_id UUID NOT NULL REFERENCES tickets(id) ON DELETE CASCADE,
    status VARCHAR(50) NOT NULL, -- APPROVED, REJECTED
    comment TEXT NOT NULL DEFAULT '',
    reviewed_by VARCHAR(255) NOT NULL,
    security_acknowledged BOOLEAN NOT NULL DEFAULT FALSE,
    security_notes TEXT NOT NULL DEFAULT '',
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

-- 2. Add Missing Metadata Columns to Catalog Table
ALTER TABLE catalog ADD COLUMN IF NOT EXISTS requestor_email VARCHAR(255) DEFAULT '';
ALTER TABLE catalog ADD COLUMN IF NOT EXISTS jira_id VARCHAR(100) DEFAULT '';
ALTER TABLE catalog ADD COLUMN IF NOT EXISTS aws_last_modified VARCHAR(100) DEFAULT '';
ALTER TABLE catalog ADD COLUMN IF NOT EXISTS aws_last_invoked VARCHAR(100) DEFAULT '';

-- 3. Add High-Performance Database Indexes for Foreign Keys and High-Frequency Filters
CREATE INDEX IF NOT EXISTS idx_tickets_created_by ON tickets(created_by);
CREATE INDEX IF NOT EXISTS idx_tickets_integration_id ON tickets(integration_id);
CREATE INDEX IF NOT EXISTS idx_tickets_service_name ON tickets(service_name);
CREATE INDEX IF NOT EXISTS idx_system_parameters_category_active ON system_parameters(category, is_active);
CREATE INDEX IF NOT EXISTS idx_ticket_reviews_ticket_id ON ticket_reviews(ticket_id);
CREATE INDEX IF NOT EXISTS idx_catalog_is_active_name ON catalog(is_active, name);
