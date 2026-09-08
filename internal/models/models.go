package models

import (
	"time"
)

// Constants for Ticket Statuses
const (
	StatusDraft            = "DRAFT"
	StatusScanning         = "SCANNING"
	StatusWaitingInfra     = "WAITING_INFRA"
	StatusInfraDetected    = "INFRA_DETECTED"
	StatusJenkinsReady     = "JENKINS_READY"
	StatusLive             = "LIVE"
	StatusRejectedSecurity = "REJECTED_SECURITY"
)

// Ticket represents the onboarding request payload
type Ticket struct {
	ID           string    `json:"id"`
	CreatedBy    string    `json:"created_by"`
	RepoURL      string    `json:"repo_url"`
	Domain       string    `json:"domain"`
	Country      string    `json:"country"`
	ServiceName  string    `json:"service_name"`
	PipelineName string    `json:"pipeline_name"`
	Status       string    `json:"status"`
	Description  string    `json:"description"`
	CreatedAt    time.Time `json:"created_at"`
}

// UserRole constants
const (
	RoleDeveloper = "developer"
	RoleDevOps    = "devops"
	RoleAdmin     = "admin"
)
