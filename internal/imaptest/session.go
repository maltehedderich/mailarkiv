package imaptest

import (
	"github.com/emersion/go-imap/v2"
	"github.com/emersion/go-imap/v2/imapserver"
	"github.com/emersion/go-imap/v2/imapserver/imapmemserver"
)

// session logs each command and runs the hook before the in-memory session
// sees it. LOGIN checks the password here, so a test can change it.
type session struct {
	server *Server
	number int
	// inner is nil until LOGIN succeeds. The protocol layer calls no other
	// method before then, except Close.
	inner *imapmemserver.UserSession
}

var _ imapserver.SessionIMAP4rev2 = (*session)(nil)

func (s *session) record(name, mailbox string) error {
	return s.server.record(Command{Session: s.number, Name: name, Mailbox: mailbox})
}

// uidPrefix names the command the way the client sent it.
func uidPrefix(numSet imap.NumSet, name string) string {
	if _, ok := numSet.(imap.UIDSet); ok {
		return "UID " + name
	}
	return name
}

func (s *session) Close() error {
	return s.inner.Close()
}

func (s *session) Login(userName, password string) error {
	if err := s.record("LOGIN", ""); err != nil {
		return err
	}
	if !s.server.accepts(userName, password) {
		return imapserver.ErrAuthFailed
	}
	s.inner = imapmemserver.NewUserSession(s.server.user)
	return nil
}

func (s *session) Select(mailbox string, options *imap.SelectOptions) (*imap.SelectData, error) {
	name := "SELECT"
	if options != nil && options.ReadOnly {
		name = "EXAMINE"
	}
	if err := s.record(name, mailbox); err != nil {
		return nil, err
	}
	return s.inner.Select(mailbox, options)
}

func (s *session) Create(mailbox string, options *imap.CreateOptions) error {
	if err := s.record("CREATE", mailbox); err != nil {
		return err
	}
	if err := s.inner.Create(mailbox, options); err != nil {
		return err
	}
	s.server.trackFolder(mailbox, "")
	return nil
}

func (s *session) Delete(mailbox string) error {
	if err := s.record("DELETE", mailbox); err != nil {
		return err
	}
	if err := s.inner.Delete(mailbox); err != nil {
		return err
	}
	s.server.trackFolder("", mailbox)
	return nil
}

func (s *session) Rename(mailbox, newName string, options *imap.RenameOptions) error {
	if err := s.record("RENAME", mailbox); err != nil {
		return err
	}
	if err := s.inner.Rename(mailbox, newName, options); err != nil {
		return err
	}
	s.server.trackFolder(newName, mailbox)
	return nil
}

func (s *session) Subscribe(mailbox string) error {
	if err := s.record("SUBSCRIBE", mailbox); err != nil {
		return err
	}
	return s.inner.Subscribe(mailbox)
}

func (s *session) Unsubscribe(mailbox string) error {
	if err := s.record("UNSUBSCRIBE", mailbox); err != nil {
		return err
	}
	return s.inner.Unsubscribe(mailbox)
}

func (s *session) List(w *imapserver.ListWriter, ref string, patterns []string, options *imap.ListOptions) error {
	if err := s.record("LIST", ""); err != nil {
		return err
	}
	return s.inner.List(w, ref, patterns, options)
}

func (s *session) Status(mailbox string, options *imap.StatusOptions) (*imap.StatusData, error) {
	if err := s.record("STATUS", mailbox); err != nil {
		return nil, err
	}
	return s.inner.Status(mailbox, options)
}

func (s *session) Append(mailbox string, r imap.LiteralReader, options *imap.AppendOptions) (*imap.AppendData, error) {
	if err := s.record("APPEND", mailbox); err != nil {
		return nil, err
	}
	return s.inner.Append(mailbox, r, options)
}

// Poll runs after most commands, not at a client's request, so it isn't
// logged.
func (s *session) Poll(w *imapserver.UpdateWriter, allowExpunge bool) error {
	return s.inner.Poll(w, allowExpunge)
}

func (s *session) Idle(w *imapserver.UpdateWriter, stop <-chan struct{}) error {
	if err := s.record("IDLE", ""); err != nil {
		return err
	}
	return s.inner.Idle(w, stop)
}

func (s *session) Namespace() (*imap.NamespaceData, error) {
	if err := s.record("NAMESPACE", ""); err != nil {
		return nil, err
	}
	return s.inner.Namespace()
}

// Unselect isn't logged. The protocol layer calls it itself before a SELECT
// or EXAMINE while a folder is open, so it can't tell a client's UNSELECT
// apart.
func (s *session) Unselect() error {
	return s.inner.Unselect()
}

// Expunge also logs CLOSE as EXPUNGE, because the protocol layer expunges
// through it, even in a folder opened with EXAMINE.
func (s *session) Expunge(w *imapserver.ExpungeWriter, uids *imap.UIDSet) error {
	name := "EXPUNGE"
	if uids != nil {
		name = "UID EXPUNGE"
	}
	if err := s.record(name, ""); err != nil {
		return err
	}
	return s.inner.Expunge(w, uids)
}

func (s *session) Search(kind imapserver.NumKind, criteria *imap.SearchCriteria, options *imap.SearchOptions) (*imap.SearchData, error) {
	name := "SEARCH"
	if kind == imapserver.NumKindUID {
		name = "UID SEARCH"
	}
	if err := s.record(name, ""); err != nil {
		return nil, err
	}
	return s.inner.Search(kind, criteria, options)
}

func (s *session) Fetch(w *imapserver.FetchWriter, numSet imap.NumSet, options *imap.FetchOptions) error {
	if err := s.record(uidPrefix(numSet, "FETCH"), ""); err != nil {
		return err
	}
	return s.inner.Fetch(w, numSet, options)
}

func (s *session) Store(w *imapserver.FetchWriter, numSet imap.NumSet, flags *imap.StoreFlags, options *imap.StoreOptions) error {
	if err := s.record(uidPrefix(numSet, "STORE"), ""); err != nil {
		return err
	}
	return s.inner.Store(w, numSet, flags, options)
}

func (s *session) Copy(numSet imap.NumSet, dest string) (*imap.CopyData, error) {
	if err := s.record(uidPrefix(numSet, "COPY"), dest); err != nil {
		return nil, err
	}
	return s.inner.Copy(numSet, dest)
}

// Move is served even though CAPABILITY doesn't list MOVE, because the
// protocol layer accepts it from any session that has the method. Logging
// it lets a test prove collection never sends it.
func (s *session) Move(w *imapserver.MoveWriter, numSet imap.NumSet, dest string) error {
	if err := s.record(uidPrefix(numSet, "MOVE"), dest); err != nil {
		return err
	}
	return s.inner.Move(w, numSet, dest)
}
