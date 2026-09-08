package auth

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"sync"
)

var (
	encryptionKey   []byte
	encryptionKeyMu sync.RWMutex
)

// SetEncryptionKey sets the 32-byte key used for AES-256 encryption.
// The key must be exactly 32 bytes long.
func SetEncryptionKey(key []byte) error {
	if len(key) != 32 {
		return fmt.Errorf("AES encryption key must be exactly 32 bytes long, got %d bytes", len(key))
	}
	encryptionKeyMu.Lock()
	defer encryptionKeyMu.Unlock()
	encryptionKey = make([]byte, 32)
	copy(encryptionKey, key)
	return nil
}

func getEncryptionKey() ([]byte, error) {
	encryptionKeyMu.RLock()
	defer encryptionKeyMu.RUnlock()
	if len(encryptionKey) != 32 {
		return nil, errors.New("AES encryption key is not initialized or invalid (must be exactly 32 bytes)")
	}
	keyCopy := make([]byte, 32)
	copy(keyCopy, encryptionKey)
	return keyCopy, nil
}

// Encrypt encrypts plain text string into base64 encoded cipher text
func Encrypt(plaintext string) (string, error) {
	if plaintext == "" {
		return "", nil
	}

	key, err := getEncryptionKey()
	if err != nil {
		return "", err
	}

	block, err := aes.NewCipher(key)
	if err != nil {
		return "", err
	}

	aesGCM, err := cipher.NewGCM(block)
	if err != nil {
		return "", err
	}

	nonce := make([]byte, aesGCM.NonceSize())
	if _, err = io.ReadFull(rand.Reader, nonce); err != nil {
		return "", err
	}

	ciphertext := aesGCM.Seal(nonce, nonce, []byte(plaintext), nil)
	return base64.StdEncoding.EncodeToString(ciphertext), nil
}

// Decrypt decrypts base64 encoded cipher text back to plain text
func Decrypt(encryptedString string) (string, error) {
	if encryptedString == "" {
		return "", nil
	}

	enc, err := base64.StdEncoding.DecodeString(encryptedString)
	if err != nil {
		return "", err
	}

	key, err := getEncryptionKey()
	if err != nil {
		return "", err
	}

	block, err := aes.NewCipher(key)
	if err != nil {
		return "", err
	}

	aesGCM, err := cipher.NewGCM(block)
	if err != nil {
		return "", err
	}

	nonceSize := aesGCM.NonceSize()
	if len(enc) < nonceSize {
		return "", errors.New("ciphertext too short")
	}

	nonce, ciphertext := enc[:nonceSize], enc[nonceSize:]
	plaintext, err := aesGCM.Open(nil, nonce, ciphertext, nil)
	if err != nil {
		return "", err
	}

	return string(plaintext), nil
}
