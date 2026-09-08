package core

import (
	"errors"

	"github.com/oona-insurance/dev-portal/internal/models"
)

var (
	ErrInvalidTransition = errors.New("invalid state transition")
	ErrUnauthorized      = errors.New("unauthorized action for this role")
)

// TicketStateMachine handles the strict transitions of a ticket
type TicketStateMachine struct{}

func NewTicketStateMachine() *TicketStateMachine {
	return &TicketStateMachine{}
}

// Transition evaluates if a status move is allowed based on the user's role and current state
func (sm *TicketStateMachine) Transition(currentStatus string, targetStatus string, userRole string) error {
	switch currentStatus {
	
	case models.StatusDraft:
		// Developer can submit ticket, triggering SCANNING
		if targetStatus == models.StatusScanning && userRole == models.RoleDeveloper {
			return nil
		}

	case models.StatusScanning:
		// System (Worker) can move it to WAITING_INFRA or REJECTED
		if targetStatus == models.StatusWaitingInfra || targetStatus == models.StatusRejectedSecurity {
			return nil // Usually System/Admin role does this
		}

	case models.StatusWaitingInfra:
		// System (Git Verifier Worker) detects Terraform and moves it to INFRA_DETECTED
		if targetStatus == models.StatusInfraDetected {
			return nil
		}

	case models.StatusInfraDetected:
		// ONLY DevOps can approve the creation of Jenkins Pipeline
		if targetStatus == models.StatusJenkinsReady {
			if userRole != models.RoleDevOps && userRole != models.RoleAdmin {
				return ErrUnauthorized
			}
			return nil
		}

	case models.StatusJenkinsReady:
		// System (Jenkins Integration Worker) marks it LIVE once pipeline is created
		if targetStatus == models.StatusLive {
			return nil
		}
	}

	return ErrInvalidTransition
}
