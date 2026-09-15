package tasks

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/hibiken/asynq"
	"service-catalog/internal/notify"
)

const (
	TypeNotifyTeams = "notify:teams"
	TypeNotifyEmail = "notify:email"
)

// TeamsPayload holds the data for MS Teams webhook
type TeamsPayload struct {
	Title   string
	Message string
	Color   string // Hex color code
}

// EmailPayload holds the data for SMTP email
type EmailPayload struct {
	ToEmail string
	Subject string
	Body    string
}

// --- Task Creators (Called from HTTP API or other Workers) ---

// NewNotifyTeamsTask creates an asynchronous task to ping MS Teams
func NewNotifyTeamsTask(title, message, color string) (*asynq.Task, error) {
	payload, err := json.Marshal(TeamsPayload{Title: title, Message: message, Color: color})
	if err != nil {
		return nil, err
	}
	// MaxRetry: 3. If teams is down, it will retry 3 times with exponential backoff on default queue
	return asynq.NewTask(TypeNotifyTeams, payload, asynq.Queue("default"), asynq.MaxRetry(3)), nil
}

// NewNotifyEmailTask creates an asynchronous task to send an email
func NewNotifyEmailTask(toEmail, subject, body string) (*asynq.Task, error) {
	payload, err := json.Marshal(EmailPayload{ToEmail: toEmail, Subject: subject, Body: body})
	if err != nil {
		return nil, err
	}
	return asynq.NewTask(TypeNotifyEmail, payload, asynq.Queue("default"), asynq.MaxRetry(3)), nil
}

// --- Processors (Called by Worker Server) ---

// HandleNotifyTeamsTask processes the teams notification queue
func HandleNotifyTeamsTask(ctx context.Context, t *asynq.Task) error {
	var p TeamsPayload
	if err := json.Unmarshal(t.Payload(), &p); err != nil {
		return fmt.Errorf("json unmarshal failed: %v: %w", err, asynq.SkipRetry) // Don't retry bad JSON
	}

	return notify.SendToTeams(p.Title, p.Message, p.Color)
}

// HandleNotifyEmailTask processes the email notification queue
func HandleNotifyEmailTask(ctx context.Context, t *asynq.Task) error {
	var p EmailPayload
	if err := json.Unmarshal(t.Payload(), &p); err != nil {
		return fmt.Errorf("json unmarshal failed: %v: %w", err, asynq.SkipRetry)
	}

	return notify.SendEmail(p.ToEmail, p.Subject, p.Body)
}
