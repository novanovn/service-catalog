package api

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/oona-insurance/dev-portal/internal/auth"
	db "github.com/oona-insurance/dev-portal/internal/repository/postgres/generated"
)

// RecordAudit asynchronously records a mutation audit trail into PostgreSQL
func RecordAudit(ctx context.Context, r *http.Request, action, entityType, entityID string, details map[string]interface{}) {
	if DB == nil {
		return
	}

	// Extract claims if present
	var userID pgtype.UUID
	var userEmail string
	if claims, ok := r.Context().Value(userCtxKey).(*auth.Claims); ok && claims != nil {
		_ = userID.Scan(claims.UserID)
		userEmail = claims.Email
	}

	ip := r.RemoteAddr
	if realIP := r.Header.Get("X-Forwarded-For"); realIP != "" {
		ip = realIP
	} else if realIP := r.Header.Get("X-Real-IP"); realIP != "" {
		ip = realIP
	}

	var detailsJSON []byte
	if details != nil {
		detailsJSON, _ = json.Marshal(details)
	}

	// Non-blocking write to prevent audit failure from disrupting user workflow
	go func() {
		bgCtx := context.Background()
		err := DB.InsertAuditLog(bgCtx, db.InsertAuditLogParams{
			Action:      action,
			EntityType:  entityType,
			EntityID:    pgtype.Text{String: entityID, Valid: entityID != ""},
			UserID:      userID,
			UserEmail:   pgtype.Text{String: userEmail, Valid: userEmail != ""},
			IpAddress:   pgtype.Text{String: ip, Valid: ip != ""},
			Details:     detailsJSON,
		})
		if err != nil {
			slog.Warn("Failed to record audit log",
				"action", action,
				"entity_type", entityType,
				"entity_id", entityID,
				"error", err,
			)
		}
	}()
}
