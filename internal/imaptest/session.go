package imaptest

import (
	"bytes"
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/emersion/go-imap/v2"
	"github.com/emersion/go-imap/v2/imapserver"
	"github.com/emersion/go-imap/v2/imapserver/imapmemserver"
)

// session logs each command and runs the hook before the in-memory session
// sees it. LOGIN checks the password here, so a test can change it.
type session struct {
	server *Server
	conn   *imapserver.Conn
	number int
	// inner is nil until LOGIN succeeds. The protocol layer calls no other
	// method before then, except Close.
	inner *imapmemserver.UserSession
	// selected is the open folder's canonical name, or empty.
	selected string
}

var _ imapserver.SessionIMAP4rev2 = (*session)(nil)

// record logs the command and runs the hook. A stall or hang-up leaves the
// connection unusable, so the error it returns reaches nobody.
func (s *session) record(name, mailbox string) error {
	flood, err := s.server.record(Command{Session: s.number, Name: name, Mailbox: mailbox})
	switch {
	case errors.Is(err, ErrStall):
		s.server.waitForClose(s.conn.NetConn())
		return err
	case errors.Is(err, ErrHangUp):
		return errors.Join(err, s.conn.NetConn().Close())
	case err != nil:
		return err
	case flood > 0:
		return s.writeFlood(flood)
	}
	return nil
}

// floodLine is one LIST reply of 1 KiB, numbered by its argument.
var floodLine = "* LIST () \"/\" \"Flood %08d " + strings.Repeat("x", 1024-len("* LIST () \"/\" \"Flood 00000000 \"\r\n")) + "\"\r\n"

// writeFlood writes LIST replies of at least size bytes straight to the
// connection. Each response before them ended with a flush, so nothing is
// left buffered.
func (s *session) writeFlood(size int) error {
	var flood bytes.Buffer
	for i := 0; flood.Len() < size; i++ {
		fmt.Fprintf(&flood, floodLine, i)
	}
	_, err := s.conn.NetConn().Write(flood.Bytes())
	return err
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
	data, err := s.inner.Select(mailbox, options)
	if err != nil {
		return nil, err
	}
	s.selected = canonical(mailbox)
	return data, nil
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
	if err := s.inner.List(w, ref, patterns, options); err != nil {
		return err
	}
	for _, entry := range s.server.listed() {
		if !slices.ContainsFunc(patterns, func(pattern string) bool {
			return imapserver.MatchList(entry.name, '/', ref, pattern)
		}) {
			continue
		}
		if err := w.WriteList(&imap.ListData{Mailbox: entry.name, Delim: '/', Attrs: entry.attrs}); err != nil {
			return err
		}
	}
	return nil
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
	s.selected = ""
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

// Fetch counts body fetches, waits at holds, and answers faulted messages
// itself before the in-memory server answers the rest.
func (s *session) Fetch(w *imapserver.FetchWriter, numSet imap.NumSet, options *imap.FetchOptions) error {
	if err := s.record(uidPrefix(numSet, "FETCH"), ""); err != nil {
		return err
	}
	uids, err := s.matching(numSet)
	if err != nil {
		return err
	}
	body := len(options.BodySection) > 0 || len(options.BinarySection) > 0
	faults, holds := s.server.fetching(s.selected, uids, body)
	for _, hold := range holds {
		if err := hold.wait(s.server.closed); err != nil {
			return err
		}
	}
	if len(faults) == 0 {
		return s.inner.Fetch(w, numSet, options)
	}

	var rest imap.UIDSet
	for _, uid := range uids {
		message, ok := faults[uid]
		if !ok {
			rest.AddNum(uid)
			continue
		}
		if err := s.writeFaulted(message, options); err != nil {
			return err
		}
	}
	if len(rest) == 0 {
		return nil
	}
	return s.inner.Fetch(w, rest, options)
}

// matching returns the UIDs of the selected folder's messages in numSet.
func (s *session) matching(numSet imap.NumSet) ([]imap.UID, error) {
	// The in-memory server resolves "*" in the set it searches in place, so
	// it gets a copy.
	criteria := &imap.SearchCriteria{}
	switch set := numSet.(type) {
	case imap.UIDSet:
		criteria.UID = []imap.UIDSet{slices.Clone(set)}
	case imap.SeqSet:
		criteria.SeqNum = []imap.SeqSet{slices.Clone(set)}
	default:
		return nil, fmt.Errorf("imaptest: unknown number set %T", numSet)
	}
	data, err := s.inner.Search(imapserver.NumKindUID, criteria, &imap.SearchOptions{})
	if err != nil {
		return nil, err
	}
	return data.AllUIDs(), nil
}

// writeFaulted writes a faulted message's FETCH response straight to the
// connection, because the protocol layer can't write NIL or a cut literal.
// Each response before it ended with a flush, so nothing is left buffered.
func (s *session) writeFaulted(m faulted, options *imap.FetchOptions) error {
	if options.Envelope || options.Flags || options.BodyStructure != nil || options.ModSeq ||
		len(options.BinarySection) > 0 || len(options.BinarySectionSize) > 0 {
		return errors.New("imaptest: a faulted message serves only UID, RFC822.SIZE, INTERNALDATE, and BODY[]")
	}
	for _, section := range options.BodySection {
		if section.Specifier != imap.PartSpecifierNone || len(section.Part) > 0 || section.Partial != nil {
			return errors.New("imaptest: a faulted message serves only the whole BODY[]")
		}
	}
	body := len(options.BodySection) > 0
	if body && m.fault.Body == BodyMissing {
		return nil
	}

	found, err := s.inner.Search(imapserver.NumKindSeq, &imap.SearchCriteria{UID: []imap.UIDSet{imap.UIDSetNum(m.message.uid)}}, &imap.SearchOptions{})
	if err != nil {
		return err
	}
	seqNums := found.AllSeqNums()
	if len(seqNums) != 1 {
		return fmt.Errorf("imaptest: UID %d has no sequence number", m.message.uid)
	}

	var items []string
	if options.UID {
		items = append(items, fmt.Sprintf("UID %d", m.message.uid))
	}
	if options.RFC822Size {
		size := int64(len(m.message.body))
		if m.fault.Size != 0 {
			size = m.fault.Size
		}
		items = append(items, fmt.Sprintf("RFC822.SIZE %d", size))
	}
	if options.InternalDate && !m.fault.NoInternalDate {
		items = append(items, fmt.Sprintf("INTERNALDATE %q", m.message.received.Format(internalDateLayout)))
	}

	var response bytes.Buffer
	fmt.Fprintf(&response, "* %d FETCH (%s", seqNums[0], strings.Join(items, " "))
	if body {
		if len(items) > 0 {
			response.WriteString(" ")
		}
		switch m.fault.Body {
		case BodyServed:
			fmt.Fprintf(&response, "BODY[] {%d}\r\n", len(m.message.body))
			response.Write(m.message.body)
		case BodyNil:
			response.WriteString("BODY[] NIL")
		case BodyEmpty:
			response.WriteString("BODY[] {0}\r\n")
		case BodyCut:
			fmt.Fprintf(&response, "BODY[] {%d}\r\n", m.fault.Announced)
			response.Write(m.message.body)
			conn := s.conn.NetConn()
			_, err := conn.Write(response.Bytes())
			return errors.Join(err, conn.Close(), errors.New("imaptest: the literal was cut"))
		case BodyStalled:
			fmt.Fprintf(&response, "BODY[] {%d}\r\n", len(m.message.body))
			response.Write(m.message.body[:len(m.message.body)/2])
			conn := s.conn.NetConn()
			if _, err := conn.Write(response.Bytes()); err != nil {
				return err
			}
			s.server.waitForClose(conn)
			return errors.New("imaptest: the literal was stalled")
		}
	}
	response.WriteString(")\r\n")
	_, err = s.conn.NetConn().Write(response.Bytes())
	return err
}

// internalDateLayout is INTERNALDATE's format, RFC 9051's date-time, whose
// day is padded with a space.
const internalDateLayout = "_2-Jan-2006 15:04:05 -0700"

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
