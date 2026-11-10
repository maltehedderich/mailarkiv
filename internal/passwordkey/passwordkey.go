// Package passwordkey creates and loads the key file that seals IMAP
// passwords, and seals and opens them. ADR 0008 records the format and the
// cipher. It imports no other module.
package passwordkey

import (
	"bytes"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

const (
	prefix  = "mailarkiv-key-v1:"
	keySize = 32
	// maxFileSize bounds what Load reads. A valid file is 62 bytes.
	maxFileSize = 1024
	// additionalData starts the data each seal is bound to. The host and user
	// name follow, each after a zero byte.
	additionalData = "mailarkiv-imap-password-v1"
)

// Load returns an error matching exactly one of these.
var (
	ErrMissing    = errors.New("no key file exists at that path")
	ErrUnreadable = errors.New("the key file can't be read")
	// ErrInvalid never carries the file's content, so it can reach the log.
	ErrInvalid = errors.New("the file isn't a MailArkiv key")
)

// ErrPasswordUnreadable means a sealed password didn't open: another key,
// another host or user name, or damaged bytes.
var ErrPasswordUnreadable = errors.New("the password doesn't open with this key")

// Key seals and opens passwords. It is safe for concurrent use.
type Key struct {
	aead cipher.AEAD
}

// Create writes a new random key to path with mode 0600. It never replaces
// a file: an existing one gives an error matching fs.ErrExist and stays as
// it was. It doesn't create a missing parent directory.
func Create(path string) error {
	raw := make([]byte, keySize)
	if _, err := rand.Read(raw); err != nil {
		return fmt.Errorf("make a key: %w", err)
	}
	line := prefix + base64.StdEncoding.EncodeToString(raw) + "\n"

	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return err
	}
	_, err = file.WriteString(line)
	if err == nil {
		err = file.Sync()
	}
	if closeErr := file.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		// A partial key would fail Load later, far from this cause.
		return errors.Join(err, os.Remove(path))
	}
	// The directory entry must be durable too, or a crash can lose the file
	// after this reports success.
	return syncDir(filepath.Dir(path))
}

func syncDir(dir string) error {
	handle, err := os.Open(dir)
	if err != nil {
		return err
	}
	err = handle.Sync()
	return errors.Join(err, handle.Close())
}

// Load reads the key at path. The file must hold exactly the key line that
// Create writes.
func Load(path string) (*Key, error) {
	file, err := os.Open(path)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, fmt.Errorf("%w: %w", ErrMissing, err)
	}
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrUnreadable, err)
	}
	defer file.Close()

	// One byte past the limit tells a file at the limit from a longer one.
	content, err := io.ReadAll(io.LimitReader(file, maxFileSize+1))
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrUnreadable, err)
	}
	raw, ok := parse(content)
	if !ok {
		return nil, ErrInvalid
	}
	return newKey(raw)
}

// parse returns the key bytes of a key line. It re-encodes what it decoded
// and compares, because the decoder skips line breaks and so would accept
// "\r\n" or a second line.
func parse(content []byte) ([]byte, bool) {
	if len(content) > maxFileSize {
		return nil, false
	}
	encoded, found := bytes.CutPrefix(content, []byte(prefix))
	if !found {
		return nil, false
	}
	encoded, found = bytes.CutSuffix(encoded, []byte("\n"))
	if !found {
		return nil, false
	}
	raw, err := base64.StdEncoding.Strict().DecodeString(string(encoded))
	if err != nil || len(raw) != keySize || base64.StdEncoding.EncodeToString(raw) != string(encoded) {
		return nil, false
	}
	return raw, true
}

func newKey(raw []byte) (*Key, error) {
	block, err := aes.NewCipher(raw)
	if err != nil {
		return nil, fmt.Errorf("prepare the key: %w", err)
	}
	aead, err := cipher.NewGCMWithRandomNonce(block)
	if err != nil {
		return nil, fmt.Errorf("prepare the key: %w", err)
	}
	return &Key{aead: aead}, nil
}

// Seal encrypts password and binds it to host and userName, ignoring case,
// so it opens only for that source. The result holds a random nonce, the
// ciphertext, and the tag.
func (k *Key) Seal(password, host, userName string) []byte {
	return k.aead.Seal(nil, nil, []byte(password), boundTo(host, userName))
}

// Open returns the password sealed for host and userName, or an error
// matching ErrPasswordUnreadable.
func (k *Key) Open(sealed []byte, host, userName string) (string, error) {
	password, err := k.aead.Open(nil, nil, sealed, boundTo(host, userName))
	if err != nil {
		return "", ErrPasswordUnreadable
	}
	return string(password), nil
}

// boundTo is the additional data for a source. The input rules allow no zero
// byte in a host or user name, so the parts can't run together.
func boundTo(host, userName string) []byte {
	return []byte(additionalData + "\x00" + strings.ToLower(host) + "\x00" + strings.ToLower(userName))
}

// Inside reports whether path is dataDir or lies under it, after making
// both absolute and resolving symbolic links. A path that doesn't exist yet
// resolves through its longest existing parent. A hard link isn't detected.
func Inside(dataDir, path string) (bool, error) {
	resolvedDir, err := resolve(dataDir)
	if err != nil {
		return false, fmt.Errorf("resolve the data directory: %w", err)
	}
	resolvedPath, err := resolve(path)
	if err != nil {
		return false, fmt.Errorf("resolve the key path: %w", err)
	}
	relative, err := filepath.Rel(resolvedDir, resolvedPath)
	if err != nil {
		return false, err
	}
	outside := relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator))
	return !outside, nil
}

// resolve makes path absolute and resolves its symbolic links. The parts
// that don't exist yet are joined back on unchanged.
func resolve(path string) (string, error) {
	current, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	var missing []string
	for {
		resolved, err := filepath.EvalSymlinks(current)
		if err == nil {
			return filepath.Join(append([]string{resolved}, missing...)...), nil
		}
		if !errors.Is(err, fs.ErrNotExist) {
			return "", err
		}
		parent := filepath.Dir(current)
		if parent == current {
			return "", err
		}
		missing = append([]string{filepath.Base(current)}, missing...)
		current = parent
	}
}
