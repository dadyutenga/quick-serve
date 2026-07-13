package middleware

import (
	"crypto/rand"
	"encoding/base64"
	"strings"

	"golang.org/x/crypto/bcrypt"
)

// GenerateToken returns a 32-byte random token, base64url-encoded (URL-safe).
func GenerateToken() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	// base64 URL encoding without padding — similar to base62 for practical purposes
	return strings.TrimRight(base64.RawURLEncoding.EncodeToString(b), "="), nil
}

func HashToken(token string) (string, error) {
	h, err := bcrypt.GenerateFromPassword([]byte(token), bcrypt.DefaultCost)
	if err != nil {
		return "", err
	}
	return string(h), nil
}

func VerifyToken(token, hash string) bool {
	if token == "" || hash == "" {
		return false
	}
	return bcrypt.CompareHashAndPassword([]byte(hash), []byte(token)) == nil
}
