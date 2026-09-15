package ci

import (
	"bytes"
	"context"
	"fmt"
	"html"
	"net/http"
	"net/url"
	"strings"
	"time"

	"service-catalog/internal/auth"
)

// PipelineEngine is the universal interface for CI/CD integrations
type PipelineEngine interface {
	CreatePipeline(ctx context.Context, folderName, jobName, repoURL string) error
	TriggerBuild(ctx context.Context, folderName, jobName string, triggeredBy string) error
}

// IntegrationConfig holds the decrypted credentials from the database
type IntegrationConfig struct {
	BaseURL    string
	AuthUser   string // Optional (used by Jenkins)
	AuthToken  string // Plain text (Decrypted from DB)
	BuildToken string // Optional token for build authentication
}

// --- JENKINS IMPLEMENTATION ---

// jenkinsHTTPClient is a package-level pooled HTTP client for Jenkins interactions.
var jenkinsHTTPClient = &http.Client{
	Timeout: 15 * time.Second,
	Transport: &http.Transport{
		MaxIdleConns:        50,
		MaxIdleConnsPerHost: 10,
		IdleConnTimeout:     90 * time.Second,
	},
}

type JenkinsEngine struct {
	Config IntegrationConfig
}

func NewJenkinsEngine(cfg IntegrationConfig) *JenkinsEngine {
	return &JenkinsEngine{Config: cfg}
}

func parseGitHubURL(repoURL string) (string, string) {
	cleanURL := strings.TrimSuffix(repoURL, ".git")
	if strings.Contains(cleanURL, "git@github.com:") {
		parts := strings.Split(cleanURL, "git@github.com:")
		if len(parts) > 1 {
			subParts := strings.Split(parts[1], "/")
			if len(subParts) >= 2 {
				return subParts[0], subParts[1]
			}
		}
	} else if strings.Contains(cleanURL, "github.com/") {
		parts := strings.Split(cleanURL, "github.com/")
		if len(parts) > 1 {
			subParts := strings.Split(parts[1], "/")
			if len(subParts) >= 2 {
				return subParts[0], subParts[1]
			}
		}
	}
	return "oona-insurance", "health-renewal-svc-clone"
}

func buildJenkinsFolderURL(baseURL, folderName, endpoint string) string {
	if folderName == "" {
		folderName = "AWS Lambda Projects"
	}
	parts := strings.Split(folderName, "/")
	var jobPaths []string
	for _, p := range parts {
		if p = strings.TrimSpace(p); p != "" {
			jobPaths = append(jobPaths, "job/"+url.PathEscape(p))
		}
	}
	if len(jobPaths) == 0 {
		jobPaths = []string{"job/AWS%20Lambda%20Projects"}
	}
	return fmt.Sprintf("%s/%s/%s", strings.TrimSuffix(baseURL, "/"), strings.Join(jobPaths, "/"), endpoint)
}

func (j *JenkinsEngine) CreatePipeline(ctx context.Context, folderName, jobName, repoURL string) error {
	owner, repo := parseGitHubURL(repoURL)

	// Ensure repositoryUrl has .git suffix as in live Jenkins config
	fullRepoURL := repoURL
	if !strings.HasSuffix(fullRepoURL, ".git") {
		fullRepoURL = fullRepoURL + ".git"
	}

	// Sanitize XML template parameters to prevent XML injection
	escJobName := html.EscapeString(jobName)
	escRepo := html.EscapeString(repo)
	escOwner := html.EscapeString(owner)
	escFullRepoURL := html.EscapeString(fullRepoURL)

	// Construct the Oona Multibranch Pipeline XML template matching 1:1 live Jenkins jobs.
	xmlTemplate := fmt.Sprintf(`<?xml version='1.1' encoding='UTF-8'?>
<org.jenkinsci.plugins.workflow.multibranch.WorkflowMultiBranchProject plugin="workflow-multibranch">
  <actions/>
  <description>%s</description>
  <displayName>%s</displayName>
  <properties>
    <org.jenkinsci.plugins.docker.workflow.declarative.FolderConfig plugin="docker-workflow">
      <dockerLabel></dockerLabel>
      <registry plugin="docker-commons"/>
    </org.jenkinsci.plugins.docker.workflow.declarative.FolderConfig>
  </properties>
  <folderViews class="jenkins.branch.MultiBranchProjectViewHolder" plugin="branch-api">
    <owner class="org.jenkinsci.plugins.workflow.multibranch.WorkflowMultiBranchProject" reference="../.."/>
  </folderViews>
  <healthMetrics/>
  <icon class="jenkins.branch.MetadataActionFolderIcon" plugin="branch-api">
    <owner class="org.jenkinsci.plugins.workflow.multibranch.WorkflowMultiBranchProject" reference="../.."/>
  </icon>
  <orphanedItemStrategy class="com.cloudbees.hudson.plugins.folder.computed.DefaultOrphanedItemStrategy" plugin="cloudbees-folder">
    <pruneDeadBranches>true</pruneDeadBranches>
    <daysToKeep>-1</daysToKeep>
    <numToKeep>-1</numToKeep>
    <abortBuilds>false</abortBuilds>
  </orphanedItemStrategy>
  <triggers/>
  <disabled>false</disabled>
  <sources class="jenkins.branch.MultiBranchProject$BranchSourceList" plugin="branch-api">
    <data>
      <jenkins.branch.BranchSource>
        <source class="org.jenkinsci.plugins.github_branch_source.GitHubSCMSource" plugin="github-branch-source">
          <id>github-source-%s</id>
          <apiUri>https://api.github.com</apiUri>
          <credentialsId>jenkins-github-token-userpass</credentialsId>
          <repoOwner>%s</repoOwner>
          <repository>%s</repository>
          <repositoryUrl>%s</repositoryUrl>
          <traits>
            <org.jenkinsci.plugins.github__branch__source.BranchDiscoveryTrait>
              <strategyId>3</strategyId>
            </org.jenkinsci.plugins.github__branch__source.BranchDiscoveryTrait>
          </traits>
        </source>
        <strategy class="jenkins.branch.DefaultBranchPropertyStrategy">
          <properties class="empty-list"/>
        </strategy>
      </jenkins.branch.BranchSource>
    </data>
    <owner class="org.jenkinsci.plugins.workflow.multibranch.WorkflowMultiBranchProject" reference="../.."/>
  </sources>
  <factory class="org.jenkinsci.plugins.workflow.multibranch.WorkflowBranchProjectFactory">
    <owner class="org.jenkinsci.plugins.workflow.multibranch.WorkflowMultiBranchProject" reference="../.."/>
    <scriptPath>Jenkinsfile</scriptPath>
  </factory>
</org.jenkinsci.plugins.workflow.multibranch.WorkflowMultiBranchProject>`, escJobName, escJobName, escRepo, escOwner, escRepo, escFullRepoURL)

	url := buildJenkinsFolderURL(j.Config.BaseURL, folderName, "createItem?name="+url.QueryEscape(jobName))
	req, err := http.NewRequestWithContext(ctx, "POST", url, bytes.NewBufferString(xmlTemplate))
	if err != nil {
		return err
	}

	req.SetBasicAuth(j.Config.AuthUser, j.Config.AuthToken)
	req.Header.Set("Content-Type", "application/xml")

	resp, err := jenkinsHTTPClient.Do(req)
	if err != nil {
		return fmt.Errorf("jenkins api call failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		return fmt.Errorf("jenkins returned status code %d", resp.StatusCode)
	}

	return nil
}

func (j *JenkinsEngine) TriggerBuild(ctx context.Context, folderName, jobName string, triggeredBy string) error {
	// Send parameters to Jenkins to track WHO approved the deploy in the portal
	params := url.Values{}
	params.Set("TRIGGERED_BY_PORTAL", triggeredBy)
	if j.Config.BuildToken != "" {
		params.Set("token", j.Config.BuildToken)
	}

	buildURL := buildJenkinsFolderURL(j.Config.BaseURL, folderName, "job/"+url.PathEscape(jobName)+"/buildWithParameters?"+params.Encode())

	req, err := http.NewRequestWithContext(ctx, "POST", buildURL, nil)
	if err != nil {
		return err
	}
	req.SetBasicAuth(j.Config.AuthUser, j.Config.AuthToken)

	resp, err := jenkinsHTTPClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		return fmt.Errorf("failed to trigger jenkins build, status %d", resp.StatusCode)
	}

	return nil
}

// JobExists probes a Jenkins job under folderName without fetching console logs.
// 200 = present, 404 = deleted/never created. Any other status is returned as an error
// so callers can distinguish "missing" from "Jenkins unreachable".
func (j *JenkinsEngine) JobExists(ctx context.Context, folderName, jobName string) (bool, error) {
	jobName = strings.TrimSpace(jobName)
	if jobName == "" {
		return false, fmt.Errorf("job name is required")
	}

	endpoint := "job/" + url.PathEscape(jobName) + "/api/json?tree=name"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, buildJenkinsFolderURL(j.Config.BaseURL, folderName, endpoint), nil)
	if err != nil {
		return false, err
	}
	req.SetBasicAuth(j.Config.AuthUser, j.Config.AuthToken)

	resp, err := jenkinsHTTPClient.Do(req)
	if err != nil {
		return false, fmt.Errorf("jenkins connection failed: %v", err)
	}
	defer resp.Body.Close()

	switch resp.StatusCode {
	case http.StatusOK:
		return true, nil
	case http.StatusNotFound:
		return false, nil
	default:
		return false, fmt.Errorf("jenkins returned status code %d", resp.StatusCode)
	}
}

// --- GITLAB IMPLEMENTATION (Future-Proofing) ---

type GitlabEngine struct {
	Config IntegrationConfig
}

func (g *GitlabEngine) CreatePipeline(ctx context.Context, folderName, jobName, repoURL string) error {
	// Gitlab usually automatically creates pipelines upon repo creation/commit if .gitlab-ci.yml exists.
	// We might only need to trigger it or configure webhook here.
	fmt.Println("GitLab integration invoked (Stub)")
	return nil
}

func (g *GitlabEngine) TriggerBuild(ctx context.Context, folderName, jobName string, triggeredBy string) error {
	return nil
}

// --- FACTORY METHOD ---

// GetEngine instantiates the correct CI/CD engine based on the database configuration
func GetEngine(provider, baseURL, authUser, encryptedToken string) (PipelineEngine, error) {
	// Decrypt the token that was saved by Admin in the DB
	plainToken, err := auth.Decrypt(encryptedToken)
	if err != nil {
		return nil, fmt.Errorf("failed to decrypt integration token: %v", err)
	}

	cfg := IntegrationConfig{
		BaseURL:   baseURL,
		AuthUser:  authUser,
		AuthToken: plainToken,
	}

	switch provider {
	case "jenkins":
		return NewJenkinsEngine(cfg), nil
	case "gitlab":
		return &GitlabEngine{Config: cfg}, nil
	default:
		return nil, fmt.Errorf("unknown ci provider: %s", provider)
	}
}
