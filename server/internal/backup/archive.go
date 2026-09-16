// Package backup produces and restores encrypted snapshots of memd's own
// database and pushes them to a Git repository on a daily schedule.
//
// An archive is a tar.gz payload (manifest.json, memd.db, env) sealed with
// AES-256-GCM under a key derived from an admin-chosen passphrase with
// Argon2id. The on-disk layout is:
//
//	magic  "MEMDBK1\n"                    8 bytes
//	salt                                  16 bytes
//	argon2 time, memory (KiB), threads    3 x uint32 big-endian
//	nonce                                 12 bytes
//	ciphertext + GCM tag                  rest
//
// Everything before the ciphertext is bound in as GCM additional data, so a
// tampered header fails the same way a wrong passphrase does.
package backup

import (
	"bytes"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/binary"
	"errors"
	"fmt"
	"io"

	"golang.org/x/crypto/argon2"
)

const (
	magic = "MEMDBK1\n"

	saltSize  = 16
	nonceSize = 12
	keySize   = 32

	// Argon2id parameters used when sealing. Decrypt honours whatever the
	// header carries (within the caps below) so these can change later
	// without breaking old archives.
	argonTime    uint32 = 3
	argonMemory  uint32 = 64 * 1024 // KiB
	argonThreads uint8  = 1

	// Caps on header-supplied parameters so a crafted archive cannot make a
	// restore allocate unbounded memory or spin for hours.
	maxArgonTime   uint32 = 32
	maxArgonMemory uint32 = 1024 * 1024 // 1 GiB in KiB
	maxArgonThread uint32 = 16

	headerSize = len(magic) + saltSize + 3*4 + nonceSize
)

// ErrWrongPassphrase is returned when the ciphertext does not authenticate:
// the passphrase is wrong or the archive was damaged. The two cases are
// indistinguishable by design.
var ErrWrongPassphrase = errors.New("wrong passphrase or corrupted archive")

// ErrNotArchive is returned when the blob does not start with the memd
// backup magic.
var ErrNotArchive = errors.New("not a memd backup archive")

// Encrypt seals plaintext under passphrase and returns the archive bytes.
func Encrypt(passphrase string, plaintext []byte) ([]byte, error) {
	if passphrase == "" {
		return nil, errors.New("passphrase required")
	}
	salt := make([]byte, saltSize)
	if _, err := io.ReadFull(rand.Reader, salt); err != nil {
		return nil, err
	}
	nonce := make([]byte, nonceSize)
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return nil, err
	}
	header := make([]byte, 0, headerSize)
	header = append(header, magic...)
	header = append(header, salt...)
	header = binary.BigEndian.AppendUint32(header, argonTime)
	header = binary.BigEndian.AppendUint32(header, argonMemory)
	header = binary.BigEndian.AppendUint32(header, uint32(argonThreads))
	header = append(header, nonce...)

	key := argon2.IDKey([]byte(passphrase), salt, argonTime, argonMemory, argonThreads, keySize)
	gcm, err := newGCM(key)
	if err != nil {
		return nil, err
	}
	out := make([]byte, len(header), len(header)+len(plaintext)+gcm.Overhead())
	copy(out, header)
	return gcm.Seal(out, nonce, plaintext, header), nil
}

// Decrypt opens an archive produced by Encrypt. A wrong passphrase, a
// truncated blob or any other damage yields ErrWrongPassphrase; a blob that is
// not an archive at all yields ErrNotArchive.
func Decrypt(passphrase string, blob []byte) ([]byte, error) {
	if len(blob) < len(magic) || !bytes.Equal(blob[:len(magic)], []byte(magic)) {
		return nil, ErrNotArchive
	}
	if len(blob) < headerSize {
		return nil, ErrWrongPassphrase
	}
	header := blob[:headerSize]
	p := len(magic)
	salt := header[p : p+saltSize]
	p += saltSize
	t := binary.BigEndian.Uint32(header[p:])
	mem := binary.BigEndian.Uint32(header[p+4:])
	threads := binary.BigEndian.Uint32(header[p+8:])
	p += 12
	nonce := header[p : p+nonceSize]
	if t == 0 || t > maxArgonTime || mem == 0 || mem > maxArgonMemory || threads == 0 || threads > maxArgonThread {
		return nil, fmt.Errorf("%w: unsupported key derivation parameters", ErrWrongPassphrase)
	}
	key := argon2.IDKey([]byte(passphrase), salt, t, mem, uint8(threads), keySize)
	gcm, err := newGCM(key)
	if err != nil {
		return nil, err
	}
	plain, err := gcm.Open(nil, nonce, blob[headerSize:], header)
	if err != nil {
		return nil, ErrWrongPassphrase
	}
	return plain, nil
}

func newGCM(key []byte) (cipher.AEAD, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	return cipher.NewGCM(block)
}
