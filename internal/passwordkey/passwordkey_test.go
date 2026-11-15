package passwordkey_test

import (
	"bytes"
	"encoding/base64"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/maltehedderich/mailarkiv/internal/passwordkey"
)

const prefix = "mailarkiv-key-v1:"

// keyLine matches the one line a key file holds: the prefix, 32 bytes in
// standard padded base64, which is 43 characters and one "=", and a newline.
var keyLine = regexp.MustCompile(`^mailarkiv-key-v1:[A-Za-z0-9+/]{43}=\n$`)

func createKey(t *testing.T) (*passwordkey.Key, string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "mailarkiv.key")
	if err := passwordkey.Create(path); err != nil {
		t.Fatalf("Create: %v", err)
	}
	key, err := passwordkey.Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	return key, path
}

func TestCreate(t *testing.T) {
	_, path := createKey(t)

	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(content) != 62 || !keyLine.Match(content) {
		t.Fatalf("the file holds %d bytes that don't form the key line", len(content))
	}
	raw, err := base64.StdEncoding.DecodeString(strings.TrimSuffix(strings.TrimPrefix(string(content), prefix), "\n"))
	if err != nil || len(raw) != 32 {
		t.Fatalf("the key decodes to %d bytes, %v, want 32", len(raw), err)
	}
	if bytes.Equal(raw, make([]byte, 32)) {
		t.Error("the key is all zero bytes")
	}

	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if mode := info.Mode().Perm(); mode != 0o600 {
		t.Errorf("mode = %o, want 600", mode)
	}

	t.Run("two keys differ", func(t *testing.T) {
		_, other := createKey(t)
		second, err := os.ReadFile(other)
		if err != nil {
			t.Fatal(err)
		}
		if bytes.Equal(content, second) {
			t.Error("two created keys are the same")
		}
	})

	t.Run("an existing file is kept", func(t *testing.T) {
		err := passwordkey.Create(path)
		if !errors.Is(err, fs.ErrExist) {
			t.Fatalf("Create on an existing file = %v, want an error matching fs.ErrExist", err)
		}
		after, readErr := os.ReadFile(path)
		if readErr != nil {
			t.Fatal(readErr)
		}
		if !bytes.Equal(after, content) {
			t.Error("Create changed an existing key")
		}
	})

	t.Run("a missing parent directory", func(t *testing.T) {
		parent := filepath.Join(t.TempDir(), "missing")
		err := passwordkey.Create(filepath.Join(parent, "mailarkiv.key"))
		if !errors.Is(err, fs.ErrNotExist) {
			t.Fatalf("Create = %v, want an error matching fs.ErrNotExist", err)
		}
		if _, err := os.Stat(parent); !errors.Is(err, fs.ErrNotExist) {
			t.Errorf("the parent directory exists after the refusal: %v", err)
		}
	})
}

// line builds key file content from raw key bytes and an encoding.
func line(encoding *base64.Encoding, raw []byte) string {
	return prefix + encoding.EncodeToString(raw) + "\n"
}

// kinds are the three errors Load may return. Each error must match exactly one.
var kinds = []error{passwordkey.ErrMissing, passwordkey.ErrUnreadable, passwordkey.ErrInvalid}

func assertKind(t *testing.T, err, want error) {
	t.Helper()
	if !errors.Is(err, want) {
		t.Fatalf("Load = %v, want %v", err, want)
	}
	for _, kind := range kinds {
		if kind != want && errors.Is(err, kind) {
			t.Errorf("Load = %v, which also matches %v", err, kind)
		}
	}
}

func writeFile(t *testing.T, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "mailarkiv.key")
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestLoad(t *testing.T) {
	// 0xff bytes encode to "/", so the URL-safe form differs from the standard one.
	raw := bytes.Repeat([]byte{0xff}, 32)
	valid := line(base64.StdEncoding, raw)

	t.Run("a valid line", func(t *testing.T) {
		path := writeFile(t, valid)
		first, err := passwordkey.Load(path)
		if err != nil {
			t.Fatalf("Load: %v", err)
		}
		second, err := passwordkey.Load(path)
		if err != nil {
			t.Fatal(err)
		}
		opened, err := second.Open(first.Seal("secret", "imap.example.test", "anna@example.test"), "imap.example.test", "anna@example.test")
		if err != nil || opened != "secret" {
			t.Errorf("a key loaded twice opens %q, %v, want the password", opened, err)
		}
	})

	t.Run("no file", func(t *testing.T) {
		_, err := passwordkey.Load(filepath.Join(t.TempDir(), "missing.key"))
		assertKind(t, err, passwordkey.ErrMissing)
	})

	t.Run("a directory at the path", func(t *testing.T) {
		_, err := passwordkey.Load(t.TempDir())
		assertKind(t, err, passwordkey.ErrUnreadable)
	})

	const marker = "MARKER-KEY-7a2e"
	invalid := []struct {
		name    string
		content string
	}{
		{"empty", ""},
		{"1,025 bytes", valid + marker + strings.Repeat("A", 1025-len(valid)-len(marker))},
		{"no final newline", strings.TrimSuffix(valid, "\n")},
		{"CRLF", strings.TrimSuffix(valid, "\n") + "\r\n"},
		{"a wrong prefix", "mailarkiv-key-v2:" + strings.TrimPrefix(valid, prefix)},
		{"31 key bytes", line(base64.StdEncoding, raw[:31])},
		{"33 key bytes", line(base64.StdEncoding, append(bytes.Clone(raw), 0xff))},
		{"unpadded base64", line(base64.RawStdEncoding, raw)},
		{"the URL-safe alphabet", line(base64.URLEncoding, raw)},
		{"a second line", valid + marker + "\n"},
	}
	for _, tt := range invalid {
		t.Run(tt.name, func(t *testing.T) {
			_, err := passwordkey.Load(writeFile(t, tt.content))
			assertKind(t, err, passwordkey.ErrInvalid)
			for _, part := range strings.FieldsFunc(tt.content, func(r rune) bool { return r == ':' || r == '\r' || r == '\n' }) {
				if len(part) >= 8 && strings.Contains(err.Error(), part) {
					t.Errorf("the error %q holds part of the file", err)
				}
			}
		})
	}
}

func TestKeyLocation(t *testing.T) {
	base := t.TempDir()
	data := filepath.Join(base, "data")
	if err := os.Mkdir(data, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(data, "existing.key"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(base, "data-other"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(data, "existing.key"), filepath.Join(base, "link-to-key")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(data, filepath.Join(base, "link-to-data")); err != nil {
		t.Fatal(err)
	}
	notYet := filepath.Join(base, "not-yet")

	tests := []struct {
		name    string
		dataDir string
		path    string
		inside  bool
	}{
		{"the data directory itself", data, data, true},
		{"a new file under it", data, filepath.Join(data, "mailarkiv.key"), true},
		{"an existing file under it", data, filepath.Join(data, "existing.key"), true},
		{"a file deeper under it", data, filepath.Join(data, "a", "b", "mailarkiv.key"), true},
		{"a path through ..", data, base + "/data/../data/mailarkiv.key", true},
		{"a link to a file in it", data, filepath.Join(base, "link-to-key"), true},
		{"a path under a link to it", data, filepath.Join(base, "link-to-data", "mailarkiv.key"), true},
		{"a missing path under a link to it", data, filepath.Join(base, "link-to-data", "a", "b", "mailarkiv.key"), true},
		{"a data directory named through a link", filepath.Join(base, "link-to-data"), filepath.Join(data, "mailarkiv.key"), true},
		{"a directory beside it with the same start", data, filepath.Join(base, "data-other", "mailarkiv.key"), false},
		{"a missing directory beside it", data, filepath.Join(base, "database", "mailarkiv.key"), false},
		{"the parent directory", data, base, false},
		{"a file beside it", data, filepath.Join(base, "mailarkiv.key"), false},
		{"a missing data directory, a key under it", notYet, filepath.Join(notYet, "mailarkiv.key"), true},
		{"a missing data directory, a key beside it", notYet, filepath.Join(base, "mailarkiv.key"), false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			inside, err := passwordkey.Inside(tt.dataDir, tt.path)
			if err != nil {
				t.Fatalf("Inside: %v", err)
			}
			if inside != tt.inside {
				t.Errorf("Inside(%s, %s) = %v, want %v", tt.dataDir, tt.path, inside, tt.inside)
			}
		})
	}

	t.Run("relative paths", func(t *testing.T) {
		t.Chdir(base)
		for path, want := range map[string]bool{"data/mailarkiv.key": true, "data-other/mailarkiv.key": false, "./data/../data": true} {
			inside, err := passwordkey.Inside("data", path)
			if err != nil || inside != want {
				t.Errorf("Inside(data, %s) = %v, %v, want %v", path, inside, err, want)
			}
		}
	})
}

func TestSealBoundToSource(t *testing.T) {
	key, _ := createKey(t)
	const (
		password = "secret"
		host     = "imap.example.test"
		user     = "anna@example.test"
	)
	sealed := key.Seal(password, host, user)

	if len(sealed) != 34 {
		t.Errorf("a 6-byte password sealed to %d bytes, want 34: a 12-byte nonce and a 16-byte tag", len(sealed))
	}
	if bytes.Contains(sealed, []byte(password)) {
		t.Error("the sealed value holds the password")
	}
	if opened, err := key.Open(sealed, host, user); err != nil || opened != password {
		t.Fatalf("Open = %q, %v, want the password", opened, err)
	}
	if again := key.Seal(password, host, user); bytes.Equal(again, sealed) {
		t.Error("two seals of one password are the same")
	}

	other, _ := createKey(t)
	refusals := []struct {
		name   string
		key    *passwordkey.Key
		sealed []byte
		host   string
		user   string
	}{
		{"another key", other, sealed, host, user},
		{"another host", key, sealed, "mail.example.test", user},
		{"another user name", key, sealed, host, "ben@example.test"},
		{"a shortened value", key, sealed[:10], host, user},
		{"no value", key, nil, host, user},
	}
	for i := range sealed {
		flipped := bytes.Clone(sealed)
		flipped[i] ^= 0x01
		refusals = append(refusals, struct {
			name   string
			key    *passwordkey.Key
			sealed []byte
			host   string
			user   string
		}{"a flipped byte", key, flipped, host, user})
	}
	for _, tt := range refusals {
		t.Run(tt.name, func(t *testing.T) {
			opened, err := tt.key.Open(tt.sealed, tt.host, tt.user)
			if !errors.Is(err, passwordkey.ErrPasswordUnreadable) || opened != "" {
				t.Fatalf("Open = %q, %v, want ErrPasswordUnreadable", opened, err)
			}
		})
	}

	t.Run("host and user name ignore case", func(t *testing.T) {
		mixed := key.Seal(password, "IMAP.Example.Test", "Anna@Example.Test")
		if opened, err := key.Open(mixed, host, user); err != nil || opened != password {
			t.Errorf("Open in lowercase = %q, %v", opened, err)
		}
		if opened, err := key.Open(sealed, "IMAP.Example.Test", "Anna@Example.Test"); err != nil || opened != password {
			t.Errorf("Open in mixed case = %q, %v", opened, err)
		}
	})
}
