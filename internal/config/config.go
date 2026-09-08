package config

import (
	"fmt"
	"os"
	"strconv"
)

// Config holds all centralized runtime configuration
type Config struct {
	AppEnv            string
	Port              string
	DB_DSN            string
	ValkeyURL         string
	ValkeyPassword    string
	JWTSecret         string
	AESEncryptionKey  string
	GithubToken       string
	JenkinsURL        string
	JenkinsUser       string
	JenkinsAPIToken   string
	TfRepoPath        string
	WorkerConcurrency int
}

// Load reads configuration from environment variables with strong validation
func Load() (*Config, error) {
	cfg := &Config{
		AppEnv:           getEnv("APP_ENV", "development"),
		Port:             getEnv("PORT", "8080"),
		DB_DSN:           os.Getenv("DB_DSN"),
		ValkeyURL:        getEnv("VALKEY_URL", "localhost:6379"),
		ValkeyPassword:   os.Getenv("VALKEY_PASSWORD"),
		JWTSecret:        os.Getenv("JWT_SECRET"),
		AESEncryptionKey: os.Getenv("AES_ENCRYPTION_KEY"),
		GithubToken:      os.Getenv("GITHUB_TOKEN"),
		JenkinsURL:       getEnv("JENKINS_URL", "https://automation.oona-insurance.com"),
		JenkinsUser:      os.Getenv("JENKINS_USER"),
		JenkinsAPIToken:  os.Getenv("JENKINS_API_TOKEN"),
		TfRepoPath:       os.Getenv("OONA_TF_REPO_PATH"),
	}

	concurrencyStr := getEnv("WORKER_CONCURRENCY", "15")
	concurrency, err := strconv.Atoi(concurrencyStr)
	if err != nil || concurrency <= 0 {
		concurrency = 15
	}
	cfg.WorkerConcurrency = concurrency

	// Validate critical configuration for production grade
	if len(cfg.JWTSecret) < 32 {
		return nil, fmt.Errorf("JWT_SECRET is required and must be at least 32 characters long (current: %d)", len(cfg.JWTSecret))
	}

	if len(cfg.AESEncryptionKey) != 32 {
		return nil, fmt.Errorf("AES_ENCRYPTION_KEY is required and must be exactly 32 characters long (current: %d)", len(cfg.AESEncryptionKey))
	}

	return cfg, nil
}

// IsProduction returns true if running in production mode
func (c *Config) IsProduction() bool {
	return c.AppEnv == "production"
}

func getEnv(key, defaultVal string) string {
	if val := os.Getenv(key); val != "" {
		return val
	}
	return defaultVal
}
