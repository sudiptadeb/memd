package tunnel

import (
	"crypto/hkdf"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"strings"
	"time"
)

// Tunnel tokens are stateless HMAC-signed bearer strings:
//
//	v1.<base64url(payload)>.<base64url(HMAC-SHA256(key, "v1."+payloadB64))>
//
// The agent never parses one — to it the token is opaque. The format is a
// server-side implementation detail of memd; only mint and verify here.
// There is no revocation store: expiry is the retirement mechanism, and
// re-pairing mints a fresh token.

const (
	tokenVersion = "v1"

	// DefaultTokenTTL applies when a mint request carries no TTL.
	DefaultTokenTTL = 30 * 24 * time.Hour
	// MaxTokenTTL is the hard cap a mint request cannot exceed. It is set far
	// enough out that a long-lived agent can be paired once and left alone;
	// expiry stops being the retirement mechanism at that point, and rotating
	// the signing secret becomes the only way to revoke such a token.
	MaxTokenTTL = 36500 * 24 * time.Hour // 100 years

	// hkdfInfo is the domain-separation string used when the token key is
	// derived from MEMD_SESSION_SECRET instead of a dedicated secret.
	hkdfInfo = "memd-rc-token-v1"
)

// ErrInvalidToken covers every verification failure: malformed structure, bad
// signature, or expiry. Callers get one indistinct error on purpose.
var ErrInvalidToken = errors.New("tunnel: invalid token")

// Claims is the signed token payload. Compact JSON keys keep the pasted token
// short.
type Claims struct {
	UserID    string `json:"u"`
	Label     string `json:"l"`
	IssuedAt  int64  `json:"iat"`
	ExpiresAt int64  `json:"exp"`
	Nonce     string `json:"n"`
}

// Expiry returns the expiry instant.
func (c Claims) Expiry() time.Time { return time.Unix(c.ExpiresAt, 0) }

// TokenKey derives the HMAC key for tunnel tokens. Precedence:
//
//  1. rcSecret (MEMD_RC_TOKEN_SECRET) used directly,
//  2. else derived from sessionSecret (MEMD_SESSION_SECRET) via HKDF-SHA256
//     with a fixed info string, so the tunnel key is independent of the
//     session-cookie key even though both stem from one deployment secret,
//  3. else nil — the rc feature must stay disabled.
func TokenKey(rcSecret, sessionSecret string) []byte {
	if s := strings.TrimSpace(rcSecret); s != "" {
		sum := sha256.Sum256([]byte(s))
		return sum[:]
	}
	if s := sessionSecret; s != "" {
		key, err := hkdf.Key(sha256.New, []byte(s), nil, hkdfInfo, 32)
		if err != nil {
			// Unreachable with SHA-256 and a 32-byte length; treat as disabled.
			return nil
		}
		return key
	}
	return nil
}

// MintToken signs a fresh token for userID. ttl <= 0 selects the default; any
// value is clamped to MaxTokenTTL. It returns the token and its expiry.
func MintToken(key []byte, userID, label string, ttl time.Duration) (string, time.Time, error) {
	if len(key) == 0 {
		return "", time.Time{}, errors.New("tunnel: no token key configured")
	}
	if userID == "" {
		return "", time.Time{}, errors.New("tunnel: user id is required")
	}
	if ttl <= 0 {
		ttl = DefaultTokenTTL
	}
	if ttl > MaxTokenTTL {
		ttl = MaxTokenTTL
	}
	var nonce [8]byte
	if _, err := rand.Read(nonce[:]); err != nil {
		return "", time.Time{}, err
	}
	now := time.Now()
	expires := now.Add(ttl)
	payload, err := json.Marshal(Claims{
		UserID:    userID,
		Label:     label,
		IssuedAt:  now.Unix(),
		ExpiresAt: expires.Unix(),
		Nonce:     hex.EncodeToString(nonce[:]),
	})
	if err != nil {
		return "", time.Time{}, err
	}
	body := tokenVersion + "." + base64.RawURLEncoding.EncodeToString(payload)
	return body + "." + base64.RawURLEncoding.EncodeToString(sign(key, body)), expires, nil
}

// ParseToken verifies a token's signature (constant-time) and expiry, and
// returns its claims.
func ParseToken(key []byte, token string) (Claims, error) {
	if len(key) == 0 {
		return Claims{}, ErrInvalidToken
	}
	version, rest, ok := strings.Cut(token, ".")
	if !ok || version != tokenVersion {
		return Claims{}, ErrInvalidToken
	}
	payloadB64, sigB64, ok := strings.Cut(rest, ".")
	if !ok || strings.Contains(sigB64, ".") {
		return Claims{}, ErrInvalidToken
	}
	// Strict decoding rejects non-canonical base64, so a token altered only in
	// unused trailing bits cannot still verify.
	sig, err := base64.RawURLEncoding.Strict().DecodeString(sigB64)
	if err != nil {
		return Claims{}, ErrInvalidToken
	}
	if !hmac.Equal(sig, sign(key, tokenVersion+"."+payloadB64)) {
		return Claims{}, ErrInvalidToken
	}
	payload, err := base64.RawURLEncoding.Strict().DecodeString(payloadB64)
	if err != nil {
		return Claims{}, ErrInvalidToken
	}
	var claims Claims
	if err := json.Unmarshal(payload, &claims); err != nil {
		return Claims{}, ErrInvalidToken
	}
	if claims.UserID == "" || claims.ExpiresAt <= 0 {
		return Claims{}, ErrInvalidToken
	}
	if !time.Now().Before(claims.Expiry()) {
		return Claims{}, ErrInvalidToken
	}
	return claims, nil
}

func sign(key []byte, body string) []byte {
	mac := hmac.New(sha256.New, key)
	mac.Write([]byte(body))
	return mac.Sum(nil)
}

// TokenAgentID keys the hub by sha256(token): distinct tokens are distinct
// agents even when minted by the same user.
func TokenAgentID(token string) AgentID {
	sum := sha256.Sum256([]byte(token))
	return AgentID(hex.EncodeToString(sum[:]))
}

// shortID is the loggable form of an agent identity: the first 8 hex chars of
// sha256(token). Never log the token itself.
func shortID(id AgentID) string {
	if len(id) > 8 {
		return string(id[:8])
	}
	return string(id)
}

// shortInstance is the loggable form of an agent-process instance id.
func shortInstance(instance string) string {
	if instance == "" {
		return "legacy"
	}
	if len(instance) > 8 {
		return instance[:8]
	}
	return instance
}
