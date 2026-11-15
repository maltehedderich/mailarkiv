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

// CheckInput is a mailbox to check before it is added. Nothing in it has
// been checked.
type CheckInput struct {
	Host     string
	Port     int64
	Security string
	UserName string
	Password string
}

// MailboxChange holds the sign-in details that a check or an edit of a
// stored source changes. Nil keeps the stored value, and so does an empty
// password.
type MailboxChange struct {
	Port     *int64
	Security *string
	Password *string
}

// EditInput is a change to a stored source. Nil keeps the stored value.
// Host and UserName can't change, so any value for them breaks their rule.
// Folders is the full list of chosen folders.
type EditInput struct {
	Name     *string
	Host     *string
	UserName *string
	MailboxChange
	Folders *[]string
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
	name    string
	box     mailbox
	userKey string
	folders []string
}

// edit is an EditInput that keeps every input rule. Nil keeps the stored
// value.
type edit struct {
	name    *string
	change  mailboxChange
	folders []string
}

// mailboxChange is a MailboxChange that keeps every input rule. password is
// nil when the stored one stays.
type mailboxChange struct {
	port     *int
	security *Security
	password *string
}

// rules collects the fields that broke a rule, in the order they were
// checked.
type rules struct{ broken []string }

func (r *rules) check(ok bool, field string) {
	if !ok {
		r.broken = append(r.broken, field)
	}
}

func (r *rules) err() error {
	if r.broken == nil {
		return nil
	}
	return &InvalidFieldsError{Fields: r.broken}
}

// parseInput checks every rule and returns every broken field.
func parseInput(in SourceInput) (newSource, error) {
	var r rules
	name, ok := parseName(in.Name)
	r.check(ok, "name")
	box := parseMailbox(&r, CheckInput{Host: in.Host, Port: in.Port, Security: in.Security, UserName: in.UserName, Password: in.Password})
	r.check(validFolders(in.Folders), "folders")
	if err := r.err(); err != nil {
		return newSource{}, err
	}
	return newSource{name: name, box: box, userKey: strings.ToLower(box.userName), folders: in.Folders}, nil
}

// parseCheck checks a mailbox's details.
func parseCheck(in CheckInput) (mailbox, error) {
	var r rules
	box := parseMailbox(&r, in)
	if err := r.err(); err != nil {
		return mailbox{}, err
	}
	return box, nil
}

// parseMailbox checks the sign-in details, in the order the API lists them.
// The host comes back lowercase.
func parseMailbox(r *rules, in CheckInput) mailbox {
	in.Host = trimSourceField(in.Host)
	in.UserName = trimSourceField(in.UserName)
	r.check(validHost(in.Host), "host")
	r.check(validPort(in.Port), "port")
	security, err := parseSecurity(in.Security)
	r.check(err == nil, "security")
	r.check(validUserName(in.UserName), "user_name")
	r.check(validPassword(in.Password), "password")
	return mailbox{
		host: strings.ToLower(in.Host), port: int(in.Port), security: security,
		userName: in.UserName, password: in.Password,
	}
}

// parseChange checks the fields a change sets.
func parseChange(in MailboxChange) (mailboxChange, error) {
	var r rules
	change := mailboxChange{port: changedPort(&r, in.Port), security: changedSecurity(&r, in.Security)}
	change.password = changedPassword(&r, in.Password)
	if err := r.err(); err != nil {
		return mailboxChange{}, err
	}
	return change, nil
}

// parseEdit checks the fields an edit sets, and refuses a host or user name.
func parseEdit(in EditInput) (edit, error) {
	var r rules
	var out edit
	if in.Name != nil {
		name, ok := parseName(*in.Name)
		r.check(ok, "name")
		out.name = &name
	}
	r.check(in.Host == nil, "host")
	out.change.port = changedPort(&r, in.Port)
	out.change.security = changedSecurity(&r, in.Security)
	r.check(in.UserName == nil, "user_name")
	out.change.password = changedPassword(&r, in.Password)
	if in.Folders != nil {
		r.check(validFolders(*in.Folders), "folders")
		out.folders = *in.Folders
	}
	if err := r.err(); err != nil {
		return edit{}, err
	}
	return out, nil
}

func changedPort(r *rules, port *int64) *int {
	if port == nil {
		return nil
	}
	r.check(validPort(*port), "port")
	n := int(*port)
	return &n
}

func changedSecurity(r *rules, name *string) *Security {
	if name == nil {
		return nil
	}
	security, err := parseSecurity(*name)
	r.check(err == nil, "security")
	return &security
}

func changedPassword(r *rules, password *string) *string {
	if password == nil || *password == "" {
		return nil
	}
	r.check(validPassword(*password), "password")
	return password
}

// trimSourceField removes pasted padding, never password or folder characters.
func trimSourceField(value string) string {
	return strings.TrimFunc(value, func(r rune) bool {
		return unicode.IsSpace(r) || r == '\u200b' || r == '\ufeff'
	})
}

// parseName trims pasted padding from name and checks what is left.
func parseName(name string) (string, bool) {
	trimmed := trimSourceField(name)
	return trimmed, utf8.ValidString(name) && between(utf8.RuneCountInString(trimmed), 1, 100)
}

func validPort(port int64) bool { return port >= 1 && port <= 65535 }

func validUserName(userName string) bool {
	return utf8.ValidString(userName) && between(len(userName), 1, 1024) &&
		!strings.ContainsFunc(userName, unicode.IsControl)
}

func validPassword(password string) bool {
	return utf8.ValidString(password) && between(len(password), 1, 1024)
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
