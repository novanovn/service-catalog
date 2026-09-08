-- Migration: 0008_ticket_ai_analysis.sql
-- Description: Add ai_analysis TEXT and ai_analyzed_at TIMESTAMPTZ columns to tickets table for persistent AI triage reports

ALTER TABLE tickets ADD COLUMN IF NOT EXISTS ai_analysis TEXT;
ALTER TABLE tickets ADD COLUMN IF NOT EXISTS ai_analyzed_at TIMESTAMPTZ;

-- Index for querying tickets with AI analysis
CREATE INDEX IF NOT EXISTS idx_tickets_ai_analyzed_at ON tickets(ai_analyzed_at);
