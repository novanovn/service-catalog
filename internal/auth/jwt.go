package auth

import (
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

var (
	jwtSecret   []byte
	jwtSecretMu sync.RWMutex
)

// SetJWTSecret sets the secret key used for signing and validating JWT tokens.
// The secret key must be at least 32 bytes long.
func SetJWTSecret(secret []byte) error {
	if len(secret) < 32 {
		return fmt.Errorf("JWT secret must be at least 32 bytes long, got %d bytes", len(secret))
	}
	jwtSecretMu.Lock()
	defer jwtSecretMu.Unlock()
	jwtSecret = make([]byte, len(secret))
	copy(jwtSecret, secret)
	return nil
}

func getJWTSecret() ([]byte, error) {
	jwtSecretMu.RLock()
	defer jwtSecretMu.RUnlock()
	if len(jwtSecret) < 32 {
		return nil, errors.New("JWT secret is not initialized or invalid (must be >= 32 bytes)")
	}
	secretCopy := make([]byte, len(jwtSecret))
	copy(secretCopy, jwtSecret)
	return secretCopy, nil
}

type ContextKey string

const UserRoleKey ContextKey = "user_role"

// Claims represents the standard JWT claims for Oona Dev Portal
type Claims struct {
	UserID string `json:"user_id"`
	Email  string `json:"email"`
	Role   string `json:"role"`
	jwt.RegisteredClaims
}

// GenerateToken creates a new JWT for a logged-in user
func GenerateToken(userID, email, role string) (string, error) {
	secret, err := getJWTSecret()
	if err != nil {
		return "", err
	}

	expirationTime := time.Now().Add(24 * time.Hour)

	claims := &Claims{
		UserID: userID,
		Email:  email,
		Role:   role,
		RegisteredClaims: jwt.RegisteredClaims{
			ExpiresAt: jwt.NewNumericDate(expirationTime),
			IssuedAt:  jwt.NewNumericDate(time.Now()),
			Issuer:    "oona-dev-portal",
		},
	}

	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	return token.SignedString(secret)
}

// ValidateToken parses and validates a JWT token string
func ValidateToken(tokenString string) (*Claims, error) {
	secret, err := getJWTSecret()
	if err != nil {
		return nil, err
	}

	claims := &Claims{}

	token, err := jwt.ParseWithClaims(tokenString, claims, func(token *jwt.Token) (interface{}, error) {
		if _, ok := token.Method.(*jwt.SigningMethodHMAC); !ok {
			return nil, fmt.Errorf("unexpected signing method: %v", token.Header["alg"])
		}
		return secret, nil
	})

	if err != nil || !token.Valid {
		return nil, fmt.Errorf("invalid token")
	}

	return claims, nil
}
