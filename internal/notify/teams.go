package notify

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"time"
)

// TeamsMessage represents the payload structure for MS Teams Webhook
type TeamsMessage struct {
	Type       string `json:"@type"`
	Context    string `json:"@context"`
	ThemeColor string `json:"themeColor"`
	Title      string `json:"title"`
	Text       string `json:"text"`
}

// SendToTeams sends a notification to Oona's Microsoft Teams channel via Webhook
func SendToTeams(title, message, color string) error {
	webhookURL := os.Getenv("TEAMS_WEBHOOK_URL")
	if webhookURL == "" {
		// Log warning but don't fail, maybe they prefer SMTP only
		fmt.Println("Warning: TEAMS_WEBHOOK_URL is not set. Skipping MS Teams notification.")
		return nil
	}

	payload := TeamsMessage{
		Type:       "MessageCard",
		Context:    "http://schema.org/extensions",
		ThemeColor: color, // "0072C6" for Blue, "FF0000" for Red, "228B22" for Green
		Title:      title,
		Text:       message,
	}

	body, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("failed to marshal teams payload: %v", err)
	}

	client := &http.Client{Timeout: 5 * time.Second}
	req, err := http.NewRequest("POST", webhookURL, bytes.NewBuffer(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("failed to send teams notification: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		return fmt.Errorf("MS Teams webhook returned status code: %d", resp.StatusCode)
	}

	return nil
}
