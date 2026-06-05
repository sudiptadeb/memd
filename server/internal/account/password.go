package account

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"golang.org/x/crypto/argon2"
)

const (
	passwordScheme  = "argon2id"
	argonMemory     = 19 * 1024
	argonTime       = 2
	argonThreads    = 1
	argonSaltLen    = 16
	argonKeyLen     = 32
	maxArgonMemory  = 256 * 1024
	maxArgonTime    = 10
	maxArgonThreads = 4
	maxArgonKeyLen  = 64
)

var errBadPasswordHash = errors.New("invalid password hash")

func hashPassword(password string) (string, error) {
	if password == "" {
		return "", errors.New("password is required")
	}
	salt := make([]byte, argonSaltLen)
	if _, err := rand.Read(salt); err != nil {
		return "", err
	}
	key := argon2.IDKey([]byte(password), salt, argonTime, argonMemory, argonThreads, argonKeyLen)
	enc := base64.RawStdEncoding
	return fmt.Sprintf("$%s$v=%d$m=%d,t=%d,p=%d$%s$%s",
		passwordScheme,
		argon2.Version,
		argonMemory,
		argonTime,
		argonThreads,
		enc.EncodeToString(salt),
		enc.EncodeToString(key),
	), nil
}

func verifyPassword(password, encoded string) (bool, error) {
	params, salt, expected, err := parsePasswordHash(encoded)
	if err != nil {
		return false, err
	}
	actual := argon2.IDKey([]byte(password), salt, params.time, params.memory, params.threads, uint32(len(expected)))
	if subtle.ConstantTimeCompare(actual, expected) != 1 {
		return false, nil
	}
	return true, nil
}

type argonParams struct {
	memory  uint32
	time    uint32
	threads uint8
}

func parsePasswordHash(encoded string) (argonParams, []byte, []byte, error) {
	parts := strings.Split(encoded, "$")
	if len(parts) != 6 || parts[0] != "" || parts[1] != passwordScheme {
		return argonParams{}, nil, nil, errBadPasswordHash
	}
	if parts[2] != "v="+strconv.Itoa(argon2.Version) {
		return argonParams{}, nil, nil, errBadPasswordHash
	}
	params, err := parseArgonParams(parts[3])
	if err != nil {
		return argonParams{}, nil, nil, err
	}
	enc := base64.RawStdEncoding
	salt, err := enc.DecodeString(parts[4])
	if err != nil {
		return argonParams{}, nil, nil, errBadPasswordHash
	}
	key, err := enc.DecodeString(parts[5])
	if err != nil {
		return argonParams{}, nil, nil, errBadPasswordHash
	}
	if len(salt) == 0 || len(key) == 0 || len(key) > maxArgonKeyLen {
		return argonParams{}, nil, nil, errBadPasswordHash
	}
	return params, salt, key, nil
}

func parseArgonParams(raw string) (argonParams, error) {
	var out argonParams
	seen := map[string]bool{}
	for _, part := range strings.Split(raw, ",") {
		k, v, ok := strings.Cut(part, "=")
		if !ok {
			return out, errBadPasswordHash
		}
		seen[k] = true
		n, err := strconv.ParseUint(v, 10, 32)
		if err != nil || n == 0 {
			return out, errBadPasswordHash
		}
		switch k {
		case "m":
			out.memory = uint32(n)
		case "t":
			out.time = uint32(n)
		case "p":
			if n > 255 {
				return out, errBadPasswordHash
			}
			out.threads = uint8(n)
		default:
			return out, errBadPasswordHash
		}
	}
	if !seen["m"] || !seen["t"] || !seen["p"] {
		return out, errBadPasswordHash
	}
	if out.memory > maxArgonMemory || out.time > maxArgonTime || out.threads > maxArgonThreads {
		return out, errBadPasswordHash
	}
	return out, nil
}
