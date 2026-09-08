-- Migration 0007: Add gemini to integration_provider enum
ALTER TYPE integration_provider ADD VALUE IF NOT EXISTS 'gemini';
