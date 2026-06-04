package api

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"fmt"
	"strconv"
	"strings"
)

const (
	passwordHashPrefix     = "mailtail-pwhash-v1"
	passwordHashIterations = 120000
	passwordSaltSize       = 16
)

func hashPassword(password string) (string, error) {
	salt := make([]byte, passwordSaltSize)
	if _, err := rand.Read(salt); err != nil {
		return "", err
	}

	sum := derivePasswordHash([]byte(password), salt, passwordHashIterations)
	return fmt.Sprintf(
		"%s$%d$%s$%s",
		passwordHashPrefix,
		passwordHashIterations,
		base64.RawStdEncoding.EncodeToString(salt),
		base64.RawStdEncoding.EncodeToString(sum),
	), nil
}

func verifyPassword(password, encoded string) bool {
	parts := strings.Split(encoded, "$")
	if len(parts) != 4 || parts[0] != passwordHashPrefix {
		return false
	}

	iterations, err := strconv.Atoi(parts[1])
	if err != nil || iterations <= 0 {
		return false
	}
	salt, err := base64.RawStdEncoding.DecodeString(parts[2])
	if err != nil {
		return false
	}
	expected, err := base64.RawStdEncoding.DecodeString(parts[3])
	if err != nil {
		return false
	}

	actual := derivePasswordHash([]byte(password), salt, iterations)
	return subtle.ConstantTimeCompare(actual, expected) == 1
}

func derivePasswordHash(password, salt []byte, iterations int) []byte {
	block := make([]byte, 0, len(salt)+len(password))
	block = append(block, salt...)
	block = append(block, password...)

	sum := sha256.Sum256(block)
	out := sum[:]
	for i := 1; i < iterations; i++ {
		next := sha256.Sum256(append(append([]byte{}, out...), salt...))
		out = next[:]
	}
	derived := make([]byte, len(out))
	copy(derived, out)
	return derived
}
