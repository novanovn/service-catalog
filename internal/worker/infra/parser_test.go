package infra

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestParseEnvVarsFromTFVars_FileNotFound(t *testing.T) {
	_, err := ParseEnvVarsFromTFVars("/non/existent/path/terraform.tfvars")
	if err == nil {
		t.Fatal("expected error for non-existent file, got nil")
	}
}

func TestExtractTFVarValueFromHCL_ExistingGithubRepoID(t *testing.T) {
	hclContent := []byte(`
	existing_github_repo_id = "lmd-oona-ph-integration-health-renewal-svc"
	aws_region              = "ap-southeast-1"
	`)

	val := ExtractTFVarValueFromHCL(hclContent, "existing_github_repo_id")
	expected := "lmd-oona-ph-integration-health-renewal-svc"
	if val != expected {
		t.Errorf("expected %s, got %s", expected, val)
	}
}

func TestParseEnvVarsFromTFVars_NestedFunctionsMap(t *testing.T) {
	tempDir := t.TempDir()
	filePath := filepath.Join(tempDir, "terraform.tfvars")
	hclContent := `
	functions = {
		"health-renewal-svc" = {
			env_vars = {
				DB_HOST     = "localhost"
				DB_PASSWORD = "secret_password"
			}
		}
	}
	`
	if err := os.WriteFile(filePath, []byte(hclContent), 0644); err != nil {
		t.Fatalf("failed to write test file: %v", err)
	}

	keys, err := ParseEnvVarsFromTFVars(filePath)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	expected := []string{"DB_HOST", "DB_PASSWORD"}
	if !reflect.DeepEqual(keys, expected) {
		t.Errorf("expected %v, got %v", expected, keys)
	}
}

func TestParseEnvVarsFromTFVars_TopLevelEnvVarsMap(t *testing.T) {
	tempDir := t.TempDir()
	filePath := filepath.Join(tempDir, "terraform.tfvars")
	hclContent := `
	env_vars = {
		"API_KEY" = "12345"
		"DB_PORT" = "5432"
	}
	`
	if err := os.WriteFile(filePath, []byte(hclContent), 0644); err != nil {
		t.Fatalf("failed to write test file: %v", err)
	}

	keys, err := ParseEnvVarsFromTFVars(filePath)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	expected := []string{"API_KEY", "DB_PORT"}
	if !reflect.DeepEqual(keys, expected) {
		t.Errorf("expected %v, got %v", expected, keys)
	}
}

func TestParseEnvVarsFromTFVars_EnvVarsBlock(t *testing.T) {
	tempDir := t.TempDir()
	filePath := filepath.Join(tempDir, "terraform.tfvars")
	hclContent := `
	env_vars {
		REDIS_URL = "redis://localhost:6379"
	}
	`
	if err := os.WriteFile(filePath, []byte(hclContent), 0644); err != nil {
		t.Fatalf("failed to write test file: %v", err)
	}

	keys, err := ParseEnvVarsFromTFVars(filePath)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	expected := []string{"REDIS_URL"}
	if !reflect.DeepEqual(keys, expected) {
		t.Errorf("expected %v, got %v", expected, keys)
	}
}

func TestParseEnvVarsFromTFVars_KeyValueFallback(t *testing.T) {
	tempDir := t.TempDir()
	filePath := filepath.Join(tempDir, "terraform.tfvars")
	hclContent := `
	APP_NAME = "my-app"
	STAGE    = "production"
	`
	if err := os.WriteFile(filePath, []byte(hclContent), 0644); err != nil {
		t.Fatalf("failed to write test file: %v", err)
	}

	keys, err := ParseEnvVarsFromTFVars(filePath)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	expected := []string{"APP_NAME", "STAGE"}
	if !reflect.DeepEqual(keys, expected) {
		t.Errorf("expected %v, got %v", expected, keys)
	}
}

func TestParseEnvVarsFromTFVars_EmptyMap(t *testing.T) {
	tempDir := t.TempDir()
	filePath := filepath.Join(tempDir, "terraform.tfvars")
	hclContent := `
	functions = {
		"my-service" = {
			env_vars = {}
		}
	}
	`
	if err := os.WriteFile(filePath, []byte(hclContent), 0644); err != nil {
		t.Fatalf("failed to write test file: %v", err)
	}

	keys, err := ParseEnvVarsFromTFVars(filePath)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(keys) != 0 {
		t.Errorf("expected empty slice, got %v", keys)
	}
}

func TestParseEnvVarsFromTFVars_EmptyFile(t *testing.T) {
	tempDir := t.TempDir()
	filePath := filepath.Join(tempDir, "terraform.tfvars")
	if err := os.WriteFile(filePath, []byte(""), 0644); err != nil {
		t.Fatalf("failed to write test file: %v", err)
	}

	keys, err := ParseEnvVarsFromTFVars(filePath)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(keys) != 0 {
		t.Errorf("expected empty slice, got %v", keys)
	}
}

func TestParseEnvVarsFromTFVars_MalformedSyntax(t *testing.T) {
	tempDir := t.TempDir()
	filePath := filepath.Join(tempDir, "terraform.tfvars")
	hclContent := `
	functions = { invalid hcl syntax {{{
	`
	if err := os.WriteFile(filePath, []byte(hclContent), 0644); err != nil {
		t.Fatalf("failed to write test file: %v", err)
	}

	_, err := ParseEnvVarsFromTFVars(filePath)
	if err == nil {
		t.Fatal("expected error for malformed HCL, got nil")
	}
}

func TestParseEnvVarsFromTFVars_MultipleFunctions(t *testing.T) {
	tempDir := t.TempDir()
	filePath := filepath.Join(tempDir, "terraform.tfvars")
	hclContent := `
	functions = {
		"func1" = {
			env_vars = {
				COMMON_VAR = "val1"
				FUNC1_VAR  = "val2"
			}
		}
		"func2" = {
			env_vars = {
				COMMON_VAR = "val1"
				FUNC2_VAR  = "val3"
			}
		}
	}
	`
	if err := os.WriteFile(filePath, []byte(hclContent), 0644); err != nil {
		t.Fatalf("failed to write test file: %v", err)
	}

	keys, err := ParseEnvVarsFromTFVars(filePath)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	expected := []string{"COMMON_VAR", "FUNC1_VAR", "FUNC2_VAR"}
	if !reflect.DeepEqual(keys, expected) {
		t.Errorf("expected %v, got %v", expected, keys)
	}
}
