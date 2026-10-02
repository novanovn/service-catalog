-- Migration 0014: Add 'lead' role to user_role ENUM
-- Enables Technical Lead / Domain Lead role for service governance and archiving

ALTER TYPE user_role ADD VALUE IF NOT EXISTS 'lead' AFTER 'developer';
