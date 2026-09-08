package auth

import (
	"testing"
)

func TestSetJWTSecret(t *testing.T) {
	shortSecret := []byte("short")
	if err := SetJWTSecret(shortSecret); err == nil {
		t.Fatalf("Expected error when setting secret < 32 bytes, got nil")
	}

	validSecret := []byte("12345678901234567890123456789012")
	if err := SetJWTSecret(validSecret); err != nil {
		t.Fatalf("Failed to set valid JWT secret: %v", err)
	}
}

func TestGenerateAndValidateToken(t *testing.T) {
	validSecret := []byte("12345678901234567890123456789012")
	if err := SetJWTSecret(validSecret); err != nil {
		t.Fatalf("Failed to set JWT secret: %v", err)
	}

	tokenStr, err := GenerateToken("user123", "user@oona.com", "admin")
	if err != nil {
		t.Fatalf("GenerateToken failed: %v", err)
	}

	claims, err := ValidateToken(tokenStr)
	if err != nil {
		t.Fatalf("ValidateToken failed: %v", err)
	}

	if claims.UserID != "user123" || claims.Email != "user@oona.com" || claims.Role != "admin" {
		t.Fatalf("Claims mismatch: got %+v", claims)
	}
}
