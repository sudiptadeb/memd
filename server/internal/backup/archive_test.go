package backup

import (
	"bytes"
	"errors"
	"testing"
)

func TestEncryptDecryptRoundTrip(t *testing.T) {
	plain := []byte("the quick brown fox jumps over the lazy dog")
	blob, err := Encrypt("correct horse battery staple", plain)
	if err != nil {
		t.Fatalf("Encrypt: %v", err)
	}
	if !bytes.HasPrefix(blob, []byte(magic)) {
		t.Fatalf("archive does not start with magic: %q", blob[:8])
	}
	got, err := Decrypt("correct horse battery staple", blob)
	if err != nil {
		t.Fatalf("Decrypt: %v", err)
	}
	if !bytes.Equal(got, plain) {
		t.Fatalf("round trip mismatch: %q", got)
	}
}

func TestDecryptRejectsWrongPassphrase(t *testing.T) {
	blob, err := Encrypt("correct horse battery staple", []byte("payload"))
	if err != nil {
		t.Fatalf("Encrypt: %v", err)
	}
	if _, err := Decrypt("incorrect horse", blob); !errors.Is(err, ErrWrongPassphrase) {
		t.Fatalf("wrong passphrase: err=%v, want ErrWrongPassphrase", err)
	}
}

func TestDecryptRejectsTruncatedBlob(t *testing.T) {
	blob, err := Encrypt("correct horse battery staple", []byte("payload that is long enough to truncate"))
	if err != nil {
		t.Fatalf("Encrypt: %v", err)
	}
	for _, n := range []int{len(blob) - 1, headerSize + 3, headerSize, len(magic) + 2} {
		if _, err := Decrypt("correct horse battery staple", blob[:n]); !errors.Is(err, ErrWrongPassphrase) {
			t.Errorf("truncated to %d bytes: err=%v, want ErrWrongPassphrase", n, err)
		}
	}
	if _, err := Decrypt("correct horse battery staple", []byte("hello")); !errors.Is(err, ErrNotArchive) {
		t.Errorf("non-archive: err=%v, want ErrNotArchive", err)
	}
}

func TestDecryptRejectsTamperedHeader(t *testing.T) {
	blob, err := Encrypt("correct horse battery staple", []byte("payload"))
	if err != nil {
		t.Fatalf("Encrypt: %v", err)
	}
	blob[len(magic)] ^= 0xff // flip a salt byte
	if _, err := Decrypt("correct horse battery staple", blob); !errors.Is(err, ErrWrongPassphrase) {
		t.Fatalf("tampered salt: err=%v, want ErrWrongPassphrase", err)
	}
}
