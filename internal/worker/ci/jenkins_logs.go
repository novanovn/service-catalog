package ci

import (
	"context"
	"fmt"
	"html"
	"io"
	"net/http"
	"strings"
	"time"
)

// FetchJenkinsLogs retrieves the tail of the console output from the latest Jenkins build
// In a multibranch environment, it queries the specific branch (usually 'uat' or 'main')
func (j *JenkinsEngine) FetchJenkinsLogs(ctx context.Context, jobName string, branchName string, lines int) (string, error) {
	// Construct the URL. Multibranch pipeline structure:
	// /job/AWS Lambda Projects/job/{repo_name}/job/{branch_name}/lastBuild/consoleText
	url := fmt.Sprintf("%s/job/AWS%%20Lambda%%20Projects/job/%s/job/%s/lastBuild/consoleText", 
		j.Config.BaseURL, jobName, branchName)

	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return "", err
	}

	req.SetBasicAuth(j.Config.AuthUser, j.Config.AuthToken)

	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return "", fmt.Errorf("jenkins connection failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == 404 {
		return "No build logs found. The pipeline might not have run yet, or the branch does not exist.", nil
	}
	if resp.StatusCode >= 400 {
		return "", fmt.Errorf("jenkins returned HTTP %d", resp.StatusCode)
	}

	// Read the log body
	bodyBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("failed to read log body: %v", err)
	}

	logText := string(bodyBytes)
	
	// Fast tail implementation: Split by newline and take the last N lines
	logLines := strings.Split(logText, "\n")
	if len(logLines) > lines {
		// Truncate to the last 'lines' count
		logLines = logLines[len(logLines)-lines:]
		logLines = append([]string{"... [LOGS TRUNCATED FOR DISPLAY] ..."}, logLines...)
	}

	// Basic colorization / highlighting for terminal feel in HTML
	var styledLogs strings.Builder
	for _, line := range logLines {
		if strings.Contains(line, "ERROR") || strings.Contains(line, "FAILED") || strings.Contains(line, "Exception") {
			styledLogs.WriteString(fmt.Sprintf("<span class='text-red-400'>%s</span>\n", html.EscapeString(line)))
		} else if strings.Contains(line, "WARN") {
			styledLogs.WriteString(fmt.Sprintf("<span class='text-yellow-400'>%s</span>\n", html.EscapeString(line)))
		} else if strings.Contains(line, "SUCCESS") || strings.Contains(line, "Finished: SUCCESS") {
			styledLogs.WriteString(fmt.Sprintf("<span class='text-green-400 font-bold'>%s</span>\n", html.EscapeString(line)))
		} else if strings.HasPrefix(line, "[Pipeline]") {
			styledLogs.WriteString(fmt.Sprintf("<span class='text-blue-300'>%s</span>\n", html.EscapeString(line)))
		} else {
			styledLogs.WriteString(html.EscapeString(line) + "\n")
		}
	}

	return styledLogs.String(), nil
}
