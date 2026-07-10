package auth

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"fmt"
	"strconv"
	"strings"

	"golang.org/x/crypto/argon2"
)

const (
	argonMemory      = 64 * 1024
	argonIterations  = 3
	argonParallelism = 2
	saltLength       = 16
	keyLength        = 32
)

func HashPassword(password string) (string, error) {
	if password == "" {
		return "", fmt.Errorf("password must not be empty")
	}
	salt := make([]byte, saltLength)
	if _, err := rand.Read(salt); err != nil {
		return "", fmt.Errorf("generate password salt: %w", err)
	}
	hash := argon2.IDKey([]byte(password), salt, argonIterations, argonMemory, argonParallelism, keyLength)
	return fmt.Sprintf("$argon2id$v=%d$m=%d,t=%d,p=%d$%s$%s",
		argon2.Version, argonMemory, argonIterations, argonParallelism,
		base64.RawStdEncoding.EncodeToString(salt),
		base64.RawStdEncoding.EncodeToString(hash)), nil
}

func VerifyPassword(encoded, password string) bool {
	params, salt, expected, err := parsePHC(encoded)
	if err != nil {
		return false
	}
	actual := argon2.IDKey([]byte(password), salt, params.iterations, params.memory, params.parallelism, uint32(len(expected)))
	return subtle.ConstantTimeCompare(actual, expected) == 1
}

type argonParams struct {
	memory      uint32
	iterations  uint32
	parallelism uint8
}

func parsePHC(encoded string) (argonParams, []byte, []byte, error) {
	parts := strings.Split(encoded, "$")
	if len(parts) != 6 || parts[1] != "argon2id" || parts[2] != fmt.Sprintf("v=%d", argon2.Version) {
		return argonParams{}, nil, nil, fmt.Errorf("invalid Argon2id PHC string")
	}
	values := strings.Split(parts[3], ",")
	if len(values) != 3 {
		return argonParams{}, nil, nil, fmt.Errorf("invalid Argon2id parameters")
	}
	memory, err := parseParameter(values[0], "m", 32)
	if err != nil || memory < 8*1024 || memory > 1024*1024 {
		return argonParams{}, nil, nil, fmt.Errorf("invalid Argon2id memory")
	}
	iterations, err := parseParameter(values[1], "t", 32)
	if err != nil || iterations < 1 || iterations > 20 {
		return argonParams{}, nil, nil, fmt.Errorf("invalid Argon2id iterations")
	}
	parallelism, err := parseParameter(values[2], "p", 8)
	if err != nil || parallelism < 1 || parallelism > 32 {
		return argonParams{}, nil, nil, fmt.Errorf("invalid Argon2id parallelism")
	}
	salt, err := base64.RawStdEncoding.DecodeString(parts[4])
	if err != nil || len(salt) < 8 {
		return argonParams{}, nil, nil, fmt.Errorf("invalid Argon2id salt")
	}
	hash, err := base64.RawStdEncoding.DecodeString(parts[5])
	if err != nil || len(hash) < 16 || len(hash) > 64 {
		return argonParams{}, nil, nil, fmt.Errorf("invalid Argon2id hash")
	}
	return argonParams{uint32(memory), uint32(iterations), uint8(parallelism)}, salt, hash, nil
}

func parseParameter(value, name string, bits int) (uint64, error) {
	prefix := name + "="
	if !strings.HasPrefix(value, prefix) {
		return 0, fmt.Errorf("missing %s", name)
	}
	return strconv.ParseUint(strings.TrimPrefix(value, prefix), 10, bits)
}
