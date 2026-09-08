package ci

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"
)

// IssueTracker is the interface for connecting to Jira/Linear/etc.
type IssueTracker interface {
	GetTicketDetails(ctx context.Context, issueID string) (IssueDetails, error)
	AddComment(ctx context.Context, issueID, comment string) error
	TransitionStatus(ctx context.Context, issueID, status string) error
}

type IssueDetails struct {
	Title       string
	Description string
	Assignee    string
	Status      string
}

// --- JIRA IMPLEMENTATION ---

type JiraEngine struct {
	Config IntegrationConfig
}

func NewJiraEngine(cfg IntegrationConfig) *JiraEngine {
	return &JiraEngine{Config: cfg}
}

// AddComment posts a comment to a Jira ticket (e.g. "Jenkins Pipeline Created: http...")
func (j *JiraEngine) AddComment(ctx context.Context, issueID, comment string) error {
	url := fmt.Sprintf("%s/rest/api/2/issue/%s/comment", j.Config.BaseURL, issueID)
	
	payload := map[string]string{"body": comment}
	bodyData, _ := json.Marshal(payload)

	req, err := http.NewRequestWithContext(ctx, "POST", url, bytes.NewBuffer(bodyData))
	if err != nil {
		return err
	}

	req.SetBasicAuth(j.Config.AuthUser, j.Config.AuthToken)
	req.Header.Set("Content-Type", "application/json")

	client := &http.Client{Timeout: 5 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("jira api call failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		return fmt.Errorf("jira returned status code %d", resp.StatusCode)
	}

	return nil
}

func (j *JiraEngine) GetTicketDetails(ctx context.Context, issueID string) (IssueDetails, error) {
	// Stub implementation. We will call GET /rest/api/2/issue/{issueID}
	fmt.Printf("Fetching details for Jira Ticket: %s\n", issueID)
	return IssueDetails{
		Title:       "Auto-fetched from Jira",
		Description: "This description was pulled from Jira API",
	}, nil
}

func (j *JiraEngine) TransitionStatus(ctx context.Context, issueID, status string) error {
	return nil
}

// GetIssueTracker returns the configured issue tracker engine
func GetIssueTracker(provider, baseURL, authUser, plainToken string) (IssueTracker, error) {
	cfg := IntegrationConfig{
		BaseURL:   baseURL,
		AuthUser:  authUser,
		AuthToken: plainToken,
	}

	switch provider {
	case "jira":
		return NewJiraEngine(cfg), nil
	default:
		return nil, fmt.Errorf("unknown issue tracker provider: %s", provider)
	}
}
