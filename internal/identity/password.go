package identity

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"

	"golang.org/x/crypto/argon2"
)

// Argon2id with RFC 9106's second recommended settings. ADR 0005 records why.
// The stored hash names its settings, so they can change later.
const (
	argonTime    = 3
	argonMemory  = 64 * 1024 // KiB, so 64 MiB
	argonThreads = 4
	saltLength   = 16
	keyLength    = 32

	// maxConcurrentHashes caps memory at 128 MiB, whoever is signing in.
	maxConcurrentHashes = 2
)

// dummyHash is checked when an address has no account, so a wrong address
// costs as much as a wrong password and timing doesn't reveal which accounts
// exist (FR-003). It uses the same settings as a real hash. It was made once
// from a random password that nobody kept.
const dummyHash = "$argon2id$v=19$m=65536,t=3,p=4$AdqoK30EWJl3L/XfTB7IjQ$dvWz38cxuZnHILcHJPZO56yPxtrLfSTfBOO5Ml0W0bw"

var errMalformedHash = errors.New("the stored password hash isn't Argon2id in PHC format")

// passwords hashes and checks passwords, at most maxConcurrentHashes at a time.
type passwords struct {
	slots chan struct{}
}

func newPasswords() *passwords {
	return &passwords{slots: make(chan struct{}, maxConcurrentHashes)}
}

// acquire waits for a slot. A caller whose context ends while it waits leaves
// the queue.
func (p *passwords) acquire(ctx context.Context) error {
	select {
	case p.slots <- struct{}{}:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (p *passwords) release() {
	<-p.slots
}

// hash waits for a slot and returns the password's Argon2id hash in PHC format,
// with a new salt.
func (p *passwords) hash(ctx context.Context, password string) (string, error) {
	salt := make([]byte, saltLength)
	// crypto/rand.Read never returns an error. It crashes the program instead.
	_, _ = rand.Read(salt)

	if err := p.acquire(ctx); err != nil {
		return "", err
	}
	defer p.release()
	key := argon2.IDKey([]byte(password), salt, argonTime, argonMemory, argonThreads, keyLength)
	return fmt.Sprintf("$argon2id$v=%d$m=%d,t=%d,p=%d$%s$%s",
		argon2.Version, argonMemory, argonTime, argonThreads,
		base64.RawStdEncoding.EncodeToString(salt),
		base64.RawStdEncoding.EncodeToString(key)), nil
}

// verify reports whether password matches the stored hash, comparing in
// constant time. The caller holds a slot.
func (p *passwords) verify(stored, password string) (bool, error) {
	parsed, err := parseHash(stored)
	if err != nil {
		return false, err
	}
	key := argon2.IDKey([]byte(password), parsed.salt, parsed.time, parsed.memory, parsed.threads, uint32(len(parsed.key)))
	return subtle.ConstantTimeCompare(key, parsed.key) == 1, nil
}

type settings struct {
	time    uint32
	memory  uint32
	threads uint8
	salt    []byte
	key     []byte
}

// parseHash reads $argon2id$v=19$m=…,t=…,p=…$<salt>$<key>.
func parseHash(stored string) (settings, error) {
	parts := strings.Split(stored, "$")
	if len(parts) != 6 || parts[0] != "" || parts[1] != "argon2id" {
		return settings{}, errMalformedHash
	}
	var version int
	if _, err := fmt.Sscanf(parts[2], "v=%d", &version); err != nil || version != argon2.Version {
		return settings{}, errMalformedHash
	}
	var s settings
	if _, err := fmt.Sscanf(parts[3], "m=%d,t=%d,p=%d", &s.memory, &s.time, &s.threads); err != nil {
		return settings{}, errMalformedHash
	}
	salt, err := base64.RawStdEncoding.DecodeString(parts[4])
	if err != nil {
		return settings{}, errMalformedHash
	}
	key, err := base64.RawStdEncoding.DecodeString(parts[5])
	if err != nil || len(key) == 0 {
		return settings{}, errMalformedHash
	}
	s.salt, s.key = salt, key
	if s.time == 0 || s.memory == 0 || s.threads == 0 {
		return settings{}, errMalformedHash
	}
	return s, nil
}
