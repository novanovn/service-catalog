-- Migration 0010: Per-Service Security Scan Schedules
CREATE TABLE IF NOT EXISTS service_scan_schedules (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    service_name VARCHAR(255) NOT NULL UNIQUE,
    is_enabled BOOLEAN NOT NULL DEFAULT true,
    target_branches VARCHAR(255) NOT NULL DEFAULT 'main,uat',
    schedule_time VARCHAR(10) NOT NULL DEFAULT '01:30',
    frequency VARCHAR(20) NOT NULL DEFAULT 'daily',
    timezone VARCHAR(50) NOT NULL DEFAULT 'Asia/Jakarta',
    last_run_at TIMESTAMP WITH TIME ZONE,
    last_status VARCHAR(50) DEFAULT 'IDLE',
    created_at TIMESTAMP WITH TIME ZONE DEFAULT NOW(),
    updated_at TIMESTAMP WITH TIME ZONE DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_scan_schedules_enabled ON service_scan_schedules(is_enabled);

-- Insert default schedule for existing catalog services
INSERT INTO service_scan_schedules (service_name, is_enabled, target_branches, schedule_time, frequency, timezone)
VALUES 
    ('health-renewal-svc-clone', true, 'main,uat', '01:30', 'daily', 'Asia/Jakarta')
ON CONFLICT (service_name) DO NOTHING;
