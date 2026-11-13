// Package imaptest runs an IMAP server for tests: the go-imap in-memory
// server on 127.0.0.1, behind implicit TLS or STARTTLS, with a certificate
// from a test CA made at each run. A hook sees each command that reaches a
// session before the in-memory server does, and can fail it.
//
// Only tests and the test server under cmd/ import this package.
package imaptest

import (
	"bytes"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"log"
	"math/big"
	"net"
	"os"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/emersion/go-imap/v2"
	"github.com/emersion/go-imap/v2/imapserver"
	"github.com/emersion/go-imap/v2/imapserver/imapmemserver"
)

// Security is how a client reaches the server.
type Security int

const (
	// ImplicitTLS starts TLS before the greeting, as on port 993.
	ImplicitTLS Security = iota + 1
	// StartTLS greets in plain text and refuses LOGIN until STARTTLS, as on
	// port 143.
	StartTLS
)

// Certificate is the certificate the server presents.
type Certificate int

const (
	// Trusted is signed by the CA in CAPEM and names localhost and 127.0.0.1.
	Trusted Certificate = iota
	// OtherCA is signed by a CA that CAPEM doesn't hold.
	OtherCA
	// Expired is signed by the CA in CAPEM and expired an hour ago.
	Expired
	// OtherName is signed by the CA in CAPEM and names only
	// other.example.test.
	OtherName
)

// Options configure a server. Security, User, and Password are required.
type Options struct {
	Security    Security
	User        string
	Password    string
	Certificate Certificate
	// OnlyTLS11 accepts TLS 1.1 and nothing else.
	OnlyTLS11 bool
	// NoStartTLS leaves STARTTLS out of the capabilities of a StartTLS
	// server, which then answers STARTTLS with NO.
	NoStartTLS bool
}

// Command is one command that reached a session. CAPABILITY, NOOP,
// STARTTLS, LOGOUT, and UNSELECT don't, nor does any other command the
// protocol layer answers itself.
type Command struct {
	// Session counts sessions from 1, in the order the server opened them.
	Session int
	// Name is the command, such as "LOGIN", "EXAMINE", or "UID FETCH".
	Name string
	// Mailbox is the folder the command names, if it names one. For COPY and
	// MOVE it is the destination.
	Mailbox string
}

// Hook sees each command before the in-memory server does. A non-nil error
// fails the command: an *imap.Error reaches the client with its type, code,
// and text, ErrStall and ErrHangUp do what they say, and any other error
// reaches it as NO [SERVERBUG]. A hook runs on the session's goroutine.
type Hook func(Command) error

// No returns an error that a hook can use to answer NO.
func No(code imap.ResponseCode, text string) error {
	return &imap.Error{Type: imap.StatusResponseTypeNo, Code: code, Text: text}
}

// Bad returns an error that a hook can use to answer BAD.
func Bad(code imap.ResponseCode, text string) error {
	return &imap.Error{Type: imap.StatusResponseTypeBad, Code: code, Text: text}
}

var (
	// ErrStall, returned by a hook, leaves the command unanswered until the
	// client closes the connection or the server closes.
	ErrStall = errors.New("imaptest: the command was stalled")
	// ErrHangUp, returned by a hook, closes the connection without an answer.
	ErrHangUp = errors.New("imaptest: the server hung up")
)

// Server is an IMAP server for one user. It is safe for concurrent use.
type Server struct {
	addr     string
	caPEM    []byte
	listener net.Listener
	imap     *imapserver.Server
	served   chan error
	user     *imapmemserver.User
	name     string

	// closed ends every held fetch and stall when the server closes.
	closed    chan struct{}
	closeOnce sync.Once
	stopOnce  sync.Once
	stopErr   error

	mu       sync.Mutex
	password string
	sessions int
	commands []Command
	hook     Hook
	// folders tracks the folder names, because the in-memory server lists
	// them only to a session.
	folders map[string]bool
	// added holds what AddMessage put in each folder, in UID order, because
	// the in-memory server returns bodies only to a session.
	added       map[string][]addedMessage
	faults      map[messageKey]Fault
	holds       map[messageKey]*Hold
	bodyFetches map[messageKey]int
	// floods holds, by command name, how many bytes of LIST replies to send
	// before answering.
	floods           map[string]int
	stallHandshake   bool
	withholdGreeting bool
}

// messageKey names a message under its folder's current numbering.
type messageKey struct {
	folder string
	uid    imap.UID
}

type addedMessage struct {
	uid      imap.UID
	body     []byte
	received time.Time
}

// New starts a server with an empty INBOX.
func New(options Options) (*Server, error) {
	if options.Security != ImplicitTLS && options.Security != StartTLS {
		return nil, fmt.Errorf("imaptest: unknown security %d", options.Security)
	}
	if options.User == "" || options.Password == "" {
		return nil, errors.New("imaptest: a user and a password are required")
	}

	if options.NoStartTLS && options.Security != StartTLS {
		return nil, errors.New("imaptest: NoStartTLS needs StartTLS")
	}
	caPEM, certificate, err := newCertificates(time.Now(), options.Certificate)
	if err != nil {
		return nil, err
	}

	s := &Server{
		caPEM:       caPEM,
		served:      make(chan error, 1),
		closed:      make(chan struct{}),
		name:        options.User,
		password:    options.Password,
		folders:     map[string]bool{},
		added:       map[string][]addedMessage{},
		faults:      map[messageKey]Fault{},
		holds:       map[messageKey]*Hold{},
		bodyFetches: map[messageKey]int{},
		floods:      map[string]int{},
		// The session checks the password itself, so the user's own is never
		// used. A random one keeps it from matching anything.
		user: imapmemserver.NewUser(options.User, rand.Text()),
	}
	if err := s.AddFolder("INBOX"); err != nil {
		return nil, err
	}

	tlsConfig := &tls.Config{
		Certificates:       []tls.Certificate{certificate},
		MinVersion:         tls.VersionTLS12,
		GetConfigForClient: s.handshaking,
	}
	if options.OnlyTLS11 {
		tlsConfig.MinVersion = tls.VersionTLS11
		tlsConfig.MaxVersion = tls.VersionTLS11
	}
	serverOptions := &imapserver.Options{
		NewSession: s.greet,
		Caps:       imap.CapSet{imap.CapIMAP4rev1: {}},
		// Tests close connections on purpose, and the server logs each one.
		Logger: log.New(io.Discard, "", 0),
	}

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return nil, err
	}
	s.addr = listener.Addr().String()
	switch options.Security {
	case ImplicitTLS:
		listener = tls.NewListener(listener, tlsConfig)
	case StartTLS:
		if !options.NoStartTLS {
			serverOptions.TLSConfig = tlsConfig
		}
	}

	s.listener = listener
	s.imap = imapserver.New(serverOptions)
	go func() { s.served <- s.imap.Serve(listener) }()
	return s, nil
}

// Close stops the server and closes every connection. A held fetch or a
// stall ends with an error.
func (s *Server) Close() error {
	s.closeOnce.Do(func() { close(s.closed) })
	return errors.Join(s.StopAccepting(), s.imap.Close())
}

// StopAccepting closes the listener, so a new connection is refused. Open
// connections stay.
func (s *Server) StopAccepting() error {
	s.stopOnce.Do(func() {
		// Closing the listener first ends Serve whether or not it has begun
		// to accept. Closing the server first races with Serve, which then
		// returns an error and leaves the listener open.
		s.stopErr = errors.Join(s.listener.Close(), <-s.served)
	})
	return s.stopErr
}

// Addr is the server's address, such as 127.0.0.1:41923.
func (s *Server) Addr() string { return s.addr }

// CAPEM is the test CA's certificate in PEM. The server's certificate names
// localhost and 127.0.0.1.
func (s *Server) CAPEM() []byte { return bytes.Clone(s.caPEM) }

// WriteCA writes CAPEM to path, for a process that reads SSL_CERT_FILE.
func (s *Server) WriteCA(path string) error {
	return os.WriteFile(path, s.caPEM, 0o600)
}

// SetPassword changes the password that LOGIN accepts from now on.
func (s *Server) SetPassword(password string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.password = password
}

// SetHook replaces the hook. A nil hook lets every command through.
func (s *Server) SetHook(hook Hook) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.hook = hook
}

// Commands returns every command that reached a session, in order.
func (s *Server) Commands() []Command {
	s.mu.Lock()
	defer s.mu.Unlock()
	return slices.Clone(s.commands)
}

// Logins counts the LOGIN commands that reached a session.
func (s *Server) Logins() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	var n int
	for _, command := range s.commands {
		if command.Name == "LOGIN" {
			n++
		}
	}
	return n
}

// StallHandshake makes each TLS handshake from now on wait, at connect or
// after STARTTLS, until the client closes the connection or the server
// closes. False lets them through again.
func (s *Server) StallHandshake(stall bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.stallHandshake = stall
}

// WithholdGreeting makes each connection from now on wait before the
// greeting, after any TLS handshake, until the client closes it or the server
// closes. False greets again.
func (s *Server) WithholdGreeting(withhold bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.withholdGreeting = withhold
}

// FloodList makes each command named command send LIST replies of at least
// size bytes in all, for folders that don't exist, before its answer. Zero
// stops it.
func (s *Server) FloodList(command string, size int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if size == 0 {
		delete(s.floods, command)
		return
	}
	s.floods[command] = size
}

// AddFolder creates a folder. A folder created again after DeleteFolder has
// a new UIDVALIDITY.
func (s *Server) AddFolder(name string) error {
	if err := s.user.Create(name, nil); err != nil {
		return err
	}
	s.trackFolder(name, "")
	return nil
}

// DeleteFolder deletes a folder and its messages, with their faults, holds,
// and body fetch counts.
func (s *Server) DeleteFolder(name string) error {
	if err := s.user.Delete(name); err != nil {
		return err
	}
	s.trackFolder("", name)
	return nil
}

// trackFolder records that added now exists and removed doesn't. Either may
// be empty. The in-memory server drops a trailing delimiter from new names.
func (s *Server) trackFolder(added, removed string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if removed != "" {
		delete(s.folders, removed)
		delete(s.added, canonical(removed))
		s.forgetFolder(canonical(removed))
	}
	if added != "" {
		s.folders[strings.TrimRight(added, "/")] = true
	}
}

// forgetFolder drops what the server keeps per message of folder. The
// caller holds mu.
func (s *Server) forgetFolder(folder string) {
	for key := range s.faults {
		if key.folder == folder {
			delete(s.faults, key)
		}
	}
	for key := range s.holds {
		if key.folder == folder {
			delete(s.holds, key)
		}
	}
	for key := range s.bodyFetches {
		if key.folder == folder {
			delete(s.bodyFetches, key)
		}
	}
}

// AddMessage appends body to folder with received as its INTERNALDATE, and
// returns its UID. INTERNALDATE carries whole seconds. A zero time means now.
//
// It refuses a body that BODY[] wouldn't return byte for byte. The
// in-memory server rewrites the header when it serves one, so a header
// must end each line with CRLF, and a body must have a header.
func (s *Server) AddMessage(folder string, body []byte, received time.Time) (imap.UID, error) {
	served := imapserver.ExtractBodySection(bytes.NewReader(body), &imap.FetchItemBodySection{})
	if !bytes.Equal(served, body) {
		return 0, errors.New("imaptest: BODY[] would return other bytes than the body added")
	}
	if received.IsZero() {
		received = time.Now()
	}
	received = received.Truncate(time.Second)
	data, err := s.user.Append(folder, bytes.NewReader(body), &imap.AppendOptions{Time: received})
	if err != nil {
		return 0, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	folder = canonical(folder)
	s.added[folder] = append(s.added[folder], addedMessage{uid: data.UID, body: bytes.Clone(body), received: received})
	return data.UID, nil
}

// UIDValidity returns folder's UIDVALIDITY, as EXAMINE reports it.
func (s *Server) UIDValidity(folder string) (uint32, error) {
	view, err := s.open(folder)
	if err != nil {
		return 0, err
	}
	defer view.Close()
	return view.selected.UIDValidity, nil
}

// DeleteMessage expunges the message with uid from folder.
func (s *Server) DeleteMessage(folder string, uid imap.UID) error {
	view, err := s.open(folder)
	if err != nil {
		return err
	}
	defer view.Close()

	target := imap.UIDSetNum(uid)
	found, err := view.Search(imapserver.NumKindUID, &imap.SearchCriteria{UID: []imap.UIDSet{target}}, &imap.SearchOptions{})
	if err != nil {
		return err
	}
	if found.Count != 1 {
		return fmt.Errorf("imaptest: %s has no message with UID %d", folder, uid)
	}
	// Silent, so the store writes no response and needs no writer.
	deleted := &imap.StoreFlags{Op: imap.StoreFlagsAdd, Silent: true, Flags: []imap.Flag{imap.FlagDeleted}}
	if err := view.Store(nil, target, deleted, &imap.StoreOptions{}); err != nil {
		return err
	}
	if err := view.Expunge(nil, &target); err != nil {
		return err
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	key := messageKey{canonical(folder), uid}
	s.added[key.folder] = slices.DeleteFunc(s.added[key.folder], func(m addedMessage) bool { return m.uid == uid })
	delete(s.faults, key)
	delete(s.holds, key)
	return nil
}

// Flags returns every message's flags, by folder and UID. A message with no
// flags has an empty list.
func (s *Server) Flags() (map[string]map[imap.UID][]imap.Flag, error) {
	s.mu.Lock()
	folders := make([]string, 0, len(s.folders))
	for name := range s.folders {
		folders = append(folders, name)
	}
	s.mu.Unlock()

	snapshot := map[string]map[imap.UID][]imap.Flag{}
	for _, folder := range folders {
		flags, err := s.folderFlags(folder)
		if err != nil {
			return nil, fmt.Errorf("imaptest: read the flags in %s: %w", folder, err)
		}
		snapshot[folder] = flags
	}
	return snapshot, nil
}

func (s *Server) folderFlags(folder string) (map[imap.UID][]imap.Flag, error) {
	view, err := s.open(folder)
	if err != nil {
		return nil, err
	}
	defer view.Close()

	flags := map[imap.UID][]imap.Flag{}
	all, err := view.Search(imapserver.NumKindUID, &imap.SearchCriteria{}, &imap.SearchOptions{})
	if err != nil {
		return nil, err
	}
	for _, uid := range all.AllUIDs() {
		flags[uid] = []imap.Flag{}
	}
	// SELECT reports every flag in use in the folder.
	for _, flag := range view.selected.Flags {
		marked, err := view.Search(imapserver.NumKindUID, &imap.SearchCriteria{Flag: []imap.Flag{flag}}, &imap.SearchOptions{})
		if err != nil {
			return nil, err
		}
		for _, uid := range marked.AllUIDs() {
			flags[uid] = append(flags[uid], spelled(flag))
		}
	}
	return flags, nil
}

// spelled returns a system flag as RFC 9051 spells it. IMAP flags ignore
// case, and the in-memory server stores them in lower case.
func spelled(flag imap.Flag) imap.Flag {
	for _, system := range []imap.Flag{imap.FlagSeen, imap.FlagAnswered, imap.FlagFlagged, imap.FlagDeleted, imap.FlagDraft} {
		if strings.EqualFold(string(flag), string(system)) {
			return system
		}
	}
	return flag
}

// folderView is a session of the double's own, outside the command log.
type folderView struct {
	*imapmemserver.UserSession
	selected *imap.SelectData
}

func (s *Server) open(folder string) (*folderView, error) {
	session := imapmemserver.NewUserSession(s.user)
	selected, err := session.Select(folder, &imap.SelectOptions{ReadOnly: true})
	if err != nil {
		return nil, err
	}
	return &folderView{UserSession: session, selected: selected}, nil
}

func (v *folderView) Close() {
	// UserSession.Close only releases the view and never fails.
	_ = v.UserSession.Close()
}

// record logs command and runs the hook outside the lock, so a hook may
// call the server. It also returns the bytes of LIST replies to flood the
// command with.
func (s *Server) record(command Command) (flood int, err error) {
	s.mu.Lock()
	s.commands = append(s.commands, command)
	hook := s.hook
	flood = s.floods[command.Name]
	s.mu.Unlock()
	if hook == nil {
		return flood, nil
	}
	return flood, hook(command)
}

// greet starts a session, after a stall if the greeting is withheld.
func (s *Server) greet(conn *imapserver.Conn) (imapserver.Session, *imapserver.GreetingData, error) {
	s.mu.Lock()
	withhold := s.withholdGreeting
	s.mu.Unlock()
	if !withhold {
		return s.newSession(conn), nil, nil
	}
	netConn := conn.NetConn()
	// With implicit TLS the handshake would wait for the greeting, so it
	// runs first, and only the greeting is withheld.
	if encrypted, ok := netConn.(*tls.Conn); ok {
		if err := encrypted.Handshake(); err != nil {
			return nil, nil, err
		}
	}
	s.waitForClose(netConn)
	return nil, nil, errors.New("imaptest: the greeting was withheld")
}

// handshaking stalls a TLS handshake when StallHandshake says so. A nil
// config keeps the server's own.
func (s *Server) handshaking(hello *tls.ClientHelloInfo) (*tls.Config, error) {
	s.mu.Lock()
	stall := s.stallHandshake
	s.mu.Unlock()
	if !stall {
		return nil, nil
	}
	s.waitForClose(hello.Conn)
	return nil, errors.New("imaptest: the handshake was stalled")
}

// waitForClose blocks until the client closes conn or the server closes, and
// discards what the client sends meanwhile. The connection can't be used
// after.
func (s *Server) waitForClose(conn net.Conn) {
	// The protocol layer sets read deadlines of its own.
	_ = conn.SetReadDeadline(time.Time{})
	ended := make(chan struct{})
	go func() {
		defer close(ended)
		// Ends with the connection, which is the point.
		_, _ = io.Copy(io.Discard, conn)
	}()
	select {
	case <-ended:
	case <-s.closed:
		conn.Close()
		<-ended
	}
}

func (s *Server) accepts(userName, password string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return userName == s.name && password == s.password
}

func (s *Server) newSession(conn *imapserver.Conn) *session {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.sessions++
	return &session{server: s, conn: conn, number: s.sessions}
}

// newCertificates makes a CA, valid from an hour before now for a day, and a
// server certificate of the kind asked for.
func newCertificates(now time.Time, kind Certificate) (caPEM []byte, server tls.Certificate, err error) {
	ca, caKey, err := newCA(now)
	if err != nil {
		return nil, tls.Certificate{}, err
	}
	caPEM = pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: ca.Raw})

	serverTemplate := &x509.Certificate{
		SerialNumber: serial(),
		Subject:      pkix.Name{CommonName: "localhost"},
		DNSNames:     []string{"localhost"},
		IPAddresses:  []net.IP{net.IPv4(127, 0, 0, 1)},
		NotBefore:    now.Add(-time.Hour),
		NotAfter:     now.Add(24 * time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}
	switch kind {
	case Trusted:
	case OtherCA:
		if ca, caKey, err = newCA(now); err != nil {
			return nil, tls.Certificate{}, err
		}
	case Expired:
		serverTemplate.NotBefore = now.Add(-48 * time.Hour)
		serverTemplate.NotAfter = now.Add(-time.Hour)
	case OtherName:
		serverTemplate.Subject.CommonName = "other.example.test"
		serverTemplate.DNSNames = []string{"other.example.test"}
		serverTemplate.IPAddresses = nil
	default:
		return nil, tls.Certificate{}, fmt.Errorf("imaptest: unknown certificate %d", kind)
	}

	serverKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, tls.Certificate{}, err
	}
	serverDER, err := x509.CreateCertificate(rand.Reader, serverTemplate, ca, &serverKey.PublicKey, caKey)
	if err != nil {
		return nil, tls.Certificate{}, err
	}
	return caPEM, tls.Certificate{Certificate: [][]byte{serverDER}, PrivateKey: serverKey}, nil
}

func newCA(now time.Time) (*x509.Certificate, *ecdsa.PrivateKey, error) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, nil, err
	}
	template := &x509.Certificate{
		SerialNumber:          serial(),
		Subject:               pkix.Name{CommonName: "MailArkiv test CA"},
		NotBefore:             now.Add(-time.Hour),
		NotAfter:              now.Add(24 * time.Hour),
		IsCA:                  true,
		BasicConstraintsValid: true,
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageDigitalSignature,
	}
	der, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	if err != nil {
		return nil, nil, err
	}
	ca, err := x509.ParseCertificate(der)
	return ca, key, err
}

// serial returns a random 128-bit serial number. rand.Int fails only if
// its reader does, and crypto/rand's never does.
func serial() *big.Int {
	n, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 128))
	if err != nil {
		panic(err)
	}
	return n
}
