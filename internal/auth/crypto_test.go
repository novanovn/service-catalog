package auth

import (
	"testing"
)

func TestSetEncryptionKey(t *testing.T) {
	shortKey := []byte("too_short")
	if err := SetEncryptionKey(shortKey); err == nil {
		t.Fatalf("Expected error when setting key shorter than 32 bytes, got nil")
	}

	validKey := []byte("12345678901234567890123456789012")
	if err := SetEncryptionKey(validKey); err != nil {
		t.Fatalf("Failed to set valid encryption key: %v", err)
	}
}

func TestEncryptDecrypt(t *testing.T) {
	testKey := []byte("12345678901234567890123456789012")
	if err := SetEncryptionKey(testKey); err != nil {
		t.Fatalf("Failed to initialize encryption key: %v", err)
	}

	plain := "my_jenkins_token_123"
	encrypted, err := Encrypt(plain)
	if err != nil {
		t.Fatalf("Encryption failed: %v", err)
	}
	decrypted, err := Decrypt(encrypted)
	if err != nil {
		t.Fatalf("Decryption failed: %v", err)
	}
	if decrypted != plain {
		t.Fatalf("Expected %s, got %s", plain, decrypted)
	}
}
