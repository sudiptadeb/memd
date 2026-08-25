package tunnel

import (
	"encoding/base64"
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func testKey(t *testing.T) []byte {
	t.Helper()
	key := TokenKey("unit-test-secret", "")
	if key == nil {
		t.Fatal("TokenKey returned nil for explicit secret")
	}
	return key
}

// craftToken signs arbitrary claims so tests can produce expired or odd
// payloads that MintToken refuses to create.
func craftToken(key []byte, claims Claims) string {
	payload, _ := json.Marshal(claims)
	body := tokenVersion + "." + base64.RawURLEncoding.EncodeToString(payload)
	return body + "." + base64.RawURLEncoding.EncodeToString(sign(key, body))
}

func TestMintAndParseRoundTrip(t *testing.T) {
	key := testKey(t)
	token, expires, err := MintToken(key, "user-1", "laptop", time.Hour)
	if err != nil {
		t.Fatalf("MintToken: %v", err)
	}
	if until := time.Until(expires); until < 55*time.Minute || until > 65*time.Minute {
		t.Errorf("expiry %v not ~1h away", expires)
	}
	claims, err := ParseToken(key, token)
	if err != nil {
		t.Fatalf("ParseToken: %v", err)
	}
	if claims.UserID != "user-1" || claims.Label != "laptop" {
		t.Errorf("claims = %+v", claims)
	}
	if len(claims.Nonce) != 16 {
		t.Errorf("nonce %q: want 16 hex chars", claims.Nonce)
	}
	if !claims.Expiry().Equal(expires.Truncate(time.Second)) {
		t.Errorf("claims expiry %v != minted expiry %v", claims.Expiry(), expires)
	}
}

func TestParseTokenRejections(t *testing.T) {
	key := testKey(t)
	otherKey := TokenKey("a-different-secret", "")
	valid, _, err := MintToken(key, "user-1", "laptop", time.Hour)
	if err != nil {
		t.Fatalf("MintToken: %v", err)
	}
	parts := strings.SplitN(valid, ".", 3)

	// flipPayloadChar alters one payload character while keeping base64 valid.
	tamperedPayload := parts[0] + "." + flipChar(parts[1]) + "." + parts[2]
	tamperedSig := parts[0] + "." + parts[1] + "." + flipChar(parts[2])

	expired := craftToken(key, Claims{
		UserID: "user-1", IssuedAt: time.Now().Add(-2 * time.Hour).Unix(),
		ExpiresAt: time.Now().Add(-time.Hour).Unix(), Nonce: "0011223344556677",
	})
	noUser := craftToken(key, Claims{
		ExpiresAt: time.Now().Add(time.Hour).Unix(), Nonce: "0011223344556677",
	})

	cases := []struct {
		name  string
		key   []byte
		token string
	}{
		{"empty", key, ""},
		{"not a token", key, "hello"},
		{"wrong version", key, "v2." + parts[1] + "." + parts[2]},
		{"missing signature", key, parts[0] + "." + parts[1]},
		{"tampered payload", key, tamperedPayload},
		{"tampered signature", key, tamperedSig},
		{"truncated signature", key, parts[0] + "." + parts[1] + "." + parts[2][:10]},
		{"wrong key", otherKey, valid},
		{"nil key", nil, valid},
		{"expired", key, expired},
		{"missing user id", key, noUser},
		{"invalid base64 payload", key, parts[0] + ".!!!." + parts[2]},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := ParseToken(tc.key, tc.token); err == nil {
				t.Errorf("ParseToken accepted %s token", tc.name)
			}
		})
	}
}

// flipChar changes the first character it can to a different base64url char.
func flipChar(s string) string {
	for i, c := range s {
		repl := byte('A')
		if c == 'A' {
			repl = 'B'
		}
		return s[:i] + string(repl) + s[i+1:]
	}
	return s
}

func TestMintTTLBounds(t *testing.T) {
	key := testKey(t)
	cases := []struct {
		name    string
		ttl     time.Duration
		wantTTL time.Duration
	}{
		{"zero selects default", 0, DefaultTokenTTL},
		{"negative selects default", -time.Hour, DefaultTokenTTL},
		{"above max is clamped", 500 * 24 * time.Hour, MaxTokenTTL},
		{"explicit within range", 48 * time.Hour, 48 * time.Hour},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, expires, err := MintToken(key, "user-1", "", tc.ttl)
			if err != nil {
				t.Fatalf("MintToken: %v", err)
			}
			got := time.Until(expires)
			if got > tc.wantTTL+time.Minute || got < tc.wantTTL-time.Minute {
				t.Errorf("ttl %v: expiry %v away, want ~%v", tc.ttl, got.Round(time.Minute), tc.wantTTL)
			}
		})
	}
}

func TestMintRequiresKeyAndUser(t *testing.T) {
	if _, _, err := MintToken(nil, "user-1", "", 0); err == nil {
		t.Error("MintToken accepted nil key")
	}
	if _, _, err := MintToken(testKey(t), "", "", 0); err == nil {
		t.Error("MintToken accepted empty user id")
	}
}

func TestTokenKeyPrecedence(t *testing.T) {
	if TokenKey("", "") != nil {
		t.Error("TokenKey with no secrets should be nil (feature disabled)")
	}
	explicit := TokenKey("rc-secret", "session-secret")
	derived := TokenKey("", "session-secret")
	if explicit == nil || derived == nil {
		t.Fatal("TokenKey returned nil for configured secrets")
	}
	if string(explicit) == string(derived) {
		t.Error("explicit rc secret must take precedence over derivation")
	}
	// Derivation is deterministic, and distinct from the raw session secret so
	// the session-cookie key is never reused directly.
	if string(derived) != string(TokenKey("", "session-secret")) {
		t.Error("derived key is not deterministic")
	}
	if string(derived) == "session-secret" {
		t.Error("derived key must not equal the session secret")
	}
}

func TestTokenAgentID(t *testing.T) {
	key := testKey(t)
	a, _, _ := MintToken(key, "user-1", "same", time.Hour)
	b, _, _ := MintToken(key, "user-1", "same", time.Hour)
	if a == b {
		t.Fatal("two mints produced identical tokens (nonce broken)")
	}
	idA, idB := TokenAgentID(a), TokenAgentID(b)
	if idA == idB {
		t.Error("distinct tokens must be distinct agents")
	}
	if len(idA) != 64 {
		t.Errorf("agent id %q: want 64 hex chars", idA)
	}
	if idA != TokenAgentID(a) {
		t.Error("agent id must be stable for a token")
	}
	if shortID(idA) != string(idA[:8]) {
		t.Errorf("shortID = %q", shortID(idA))
	}
}
