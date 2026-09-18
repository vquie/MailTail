package api

import (
	"crypto/pbkdf2"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"fmt"
	"strconv"
	"strings"
)

const (
	passwordHashPrefix        = "mailtail-pbkdf2-sha256-v2"
	legacyPasswordHashPrefix  = "mailtail-pwhash-v1"
	passwordHashIterations    = 600000
	maxPasswordHashIterations = 2000000
	passwordSaltSize          = 16
	passwordHashSize          = 32
)

func hashPassword(password string) (string, error) {
	salt := make([]byte, passwordSaltSize)
	if _, err := rand.Read(salt); err != nil {
		return "", err
	}

	sum, err := pbkdf2.Key(sha256.New, password, salt, passwordHashIterations, passwordHashSize)
	if err != nil {
		return "", err
	}
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
	if len(parts) != 4 || (parts[0] != passwordHashPrefix && parts[0] != legacyPasswordHashPrefix) {
		return false
	}

	iterations, err := strconv.Atoi(parts[1])
	if err != nil || iterations <= 0 || iterations > maxPasswordHashIterations {
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

	var actual []byte
	if parts[0] == passwordHashPrefix {
		actual, err = pbkdf2.Key(sha256.New, password, salt, iterations, len(expected))
		if err != nil {
			return false
		}
	} else {
		actual = deriveLegacyPasswordHash([]byte(password), salt, iterations)
	}
	return subtle.ConstantTimeCompare(actual, expected) == 1
}

func passwordHashNeedsUpgrade(encoded string) bool {
	return !strings.HasPrefix(encoded, passwordHashPrefix+"$")
}

func deriveLegacyPasswordHash(password, salt []byte, iterations int) []byte {
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
