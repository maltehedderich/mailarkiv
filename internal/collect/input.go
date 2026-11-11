package collect

import (
	"errors"
	"net/netip"
	"strings"
	"unicode"
	"unicode/utf8"
)

// SourceInput is a new source as an administrator entered it. Nothing in it
// has been checked.
type SourceInput struct {
	Name     string
	Host     string
	Port     int64
	Security string
	UserName string
	Password string
	Folders  []string
}

// InvalidFieldsError names each field that broke an input rule, in the
// order SourceInput declares them, by the API's field names.
type InvalidFieldsError struct{ Fields []string }

func (e *InvalidFieldsError) Error() string {
	return "invalid fields: " + strings.Join(e.Fields, ", ")
}

// ErrNoKey means serve has no key, so no password can be stored or read.
var ErrNoKey = errors.New("collection needs the key")

// ErrNotFound means no active source has the ID.
var ErrNotFound = errors.New("no source has that ID")

// newSource is a SourceInput that keeps every input rule.
type newSource struct {
	name     string
	host     string // Lowercase.
	port     int
	security Security
	userName string
	userKey  string
	password string
	folders  []string
}

func (s newSource) mailbox() mailbox {
	return mailbox{host: s.host, port: s.port, security: s.security, userName: s.userName, password: s.password}
}

// parseInput checks every rule and returns every broken field.
func parseInput(in SourceInput) (newSource, error) {
	var broken []string
	out := newSource{}

	name := strings.TrimSpace(in.Name)
	if !utf8.ValidString(in.Name) || !between(utf8.RuneCountInString(name), 1, 100) {
		broken = append(broken, "name")
	}
	out.name = name

	if !validHost(in.Host) {
		broken = append(broken, "host")
	}
	out.host = strings.ToLower(in.Host)

	if in.Port < 1 || in.Port > 65535 {
		broken = append(broken, "port")
	}
	out.port = int(in.Port)

	switch in.Security {
	case TLS.String():
		out.security = TLS
	case StartTLS.String():
		out.security = StartTLS
	default:
		broken = append(broken, "security")
	}

	if !utf8.ValidString(in.UserName) || !between(len(in.UserName), 1, 1024) ||
		strings.ContainsFunc(in.UserName, unicode.IsControl) {
		broken = append(broken, "user_name")
	}
	out.userName = in.UserName
	out.userKey = strings.ToLower(in.UserName)

	if !utf8.ValidString(in.Password) || !between(len(in.Password), 1, 1024) {
		broken = append(broken, "password")
	}
	out.password = in.Password

	if !validFolders(in.Folders) {
		broken = append(broken, "folders")
	}
	out.folders = in.Folders

	if broken != nil {
		return newSource{}, &InvalidFieldsError{Fields: broken}
	}
	return out, nil
}

func between(n, low, high int) bool { return n >= low && n <= high }

// validHost accepts an IP address without brackets or zone, or a DNS name of
// ASCII letters, digits, and hyphens in non-empty labels. A non-ASCII name
// must come in its xn-- form.
func validHost(host string) bool {
	if !between(len(host), 1, 253) {
		return false
	}
	if addr, err := netip.ParseAddr(host); err == nil {
		return addr.Zone() == ""
	}
	for label := range strings.SplitSeq(host, ".") {
		if label == "" {
			return false
		}
		for _, r := range label {
			if !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '-') {
				return false
			}
		}
	}
	return true
}

func validFolders(folders []string) bool {
	if !between(len(folders), 1, 1000) {
		return false
	}
	seen := make(map[string]bool, len(folders))
	for _, folder := range folders {
		if seen[folder] || !utf8.ValidString(folder) || !between(len(folder), 1, 1000) {
			return false
		}
		seen[folder] = true
	}
	return true
}
