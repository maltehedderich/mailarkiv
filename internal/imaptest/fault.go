package imaptest

import (
	"errors"
	"fmt"
	"slices"
	"strings"
	"sync"

	"github.com/emersion/go-imap/v2"
)

// Body is what a faulted message returns for BODY[].
type Body int

const (
	// BodyServed returns the message's bytes.
	BodyServed Body = iota
	// BodyNil returns BODY[] NIL.
	BodyNil
	// BodyMissing returns no FETCH response for the message.
	BodyMissing
	// BodyEmpty returns an empty literal.
	BodyEmpty
	// BodyCut announces a literal of Fault.Announced bytes, sends the
	// message's bytes, which are fewer, and closes the connection.
	BodyCut
	// BodyStalled announces the message's literal, sends the first half of
	// its bytes, and waits until the client closes the connection or the
	// server closes.
	BodyStalled
)

// Fault changes what a FETCH returns for one message. The zero Fault
// changes nothing. A faulted message answers only UID, RFC822.SIZE,
// INTERNALDATE, and BODY[], and fails a FETCH that asks for anything else.
type Fault struct {
	// Size replaces RFC822.SIZE unless it is zero.
	Size int64
	// NoInternalDate leaves INTERNALDATE out.
	NoInternalDate bool
	Body           Body
	// Announced is the literal length BodyCut announces. It must exceed the
	// message's size.
	Announced int64
}

// Hold stops each body fetch of one message until Release.
type Hold struct {
	reached     chan struct{}
	reachOnce   sync.Once
	release     chan struct{}
	releaseOnce sync.Once
}

// Reached is closed once a body fetch waits at the hold.
func (h *Hold) Reached() <-chan struct{} { return h.reached }

// Release lets every body fetch of the message through, now and later.
func (h *Hold) Release() {
	h.releaseOnce.Do(func() { close(h.release) })
}

// wait blocks until Release, or fails when the server closes.
func (h *Hold) wait(closed <-chan struct{}) error {
	h.reachOnce.Do(func() { close(h.reached) })
	select {
	case <-h.release:
		return nil
	case <-closed:
		return errors.New("imaptest: the server closed during a held fetch")
	}
}

// canonical names a folder the way the server keeps it: INBOX in upper case,
// with no trailing delimiter.
func canonical(folder string) string {
	if strings.EqualFold(folder, "INBOX") {
		return "INBOX"
	}
	return strings.TrimRight(folder, "/")
}

// SetFault changes what a FETCH returns for a message that AddMessage added.
// The zero Fault removes the message's fault.
func (s *Server) SetFault(folder string, uid imap.UID, fault Fault) error {
	key := messageKey{canonical(folder), uid}
	s.mu.Lock()
	defer s.mu.Unlock()
	message, ok := s.addedLocked(key)
	if !ok {
		return fmt.Errorf("imaptest: AddMessage added no message with UID %d to %s", uid, folder)
	}
	if fault.Body == BodyCut && fault.Announced <= int64(len(message.body)) {
		return fmt.Errorf("imaptest: a cut literal must announce more than the message's %d bytes", len(message.body))
	}
	if fault == (Fault{}) {
		delete(s.faults, key)
		return nil
	}
	s.faults[key] = fault
	return nil
}

// HoldBody stops each body fetch of a message until the returned hold is
// released or the server closes.
func (s *Server) HoldBody(folder string, uid imap.UID) *Hold {
	hold := &Hold{reached: make(chan struct{}), release: make(chan struct{})}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.holds[messageKey{canonical(folder), uid}] = hold
	return hold
}

// BodyFetches counts the FETCH commands that asked for a message's body
// since it got its UID.
func (s *Server) BodyFetches(folder string, uid imap.UID) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.bodyFetches[messageKey{canonical(folder), uid}]
}

// Renumber recreates a folder with a new UIDVALIDITY and adds its messages
// again, newest first, so most get a new UID. It returns each old UID's new
// one. Faults follow their messages. Holds, flags, and body fetch counts
// don't. The folder must hold only messages AddMessage added.
func (s *Server) Renumber(folder string) (map[imap.UID]imap.UID, error) {
	folder = canonical(folder)
	view, err := s.open(folder)
	if err != nil {
		return nil, err
	}
	count := view.selected.NumMessages
	view.Close()

	s.mu.Lock()
	messages := slices.Clone(s.added[folder])
	faults := map[imap.UID]Fault{}
	for key, fault := range s.faults {
		if key.folder == folder {
			faults[key.uid] = fault
		}
	}
	s.mu.Unlock()
	if int(count) != len(messages) {
		return nil, fmt.Errorf("imaptest: %s holds messages AddMessage didn't add", folder)
	}

	if err := s.DeleteFolder(folder); err != nil {
		return nil, err
	}
	if err := s.AddFolder(folder); err != nil {
		return nil, err
	}
	renumbered := make(map[imap.UID]imap.UID, len(messages))
	for _, message := range slices.Backward(messages) {
		uid, err := s.AddMessage(folder, message.body, message.received)
		if err != nil {
			return nil, err
		}
		renumbered[message.uid] = uid
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	for old, fault := range faults {
		s.faults[messageKey{folder, renumbered[old]}] = fault
	}
	return renumbered, nil
}

// addedLocked finds a message AddMessage added. The caller holds mu.
func (s *Server) addedLocked(key messageKey) (addedMessage, bool) {
	index := slices.IndexFunc(s.added[key.folder], func(m addedMessage) bool { return m.uid == key.uid })
	if index < 0 {
		return addedMessage{}, false
	}
	return s.added[key.folder][index], true
}

// faulted is a message whose FETCH the session answers itself.
type faulted struct {
	fault   Fault
	message addedMessage
}

// fetching counts a body fetch of each UID in folder, and returns the holds
// it must wait at and the faulted messages among the UIDs.
func (s *Server) fetching(folder string, uids []imap.UID, body bool) (map[imap.UID]faulted, []*Hold) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var holds []*Hold
	faults := map[imap.UID]faulted{}
	for _, uid := range uids {
		key := messageKey{folder, uid}
		if body {
			s.bodyFetches[key]++
			if hold, ok := s.holds[key]; ok {
				holds = append(holds, hold)
			}
		}
		if fault, ok := s.faults[key]; ok {
			message, _ := s.addedLocked(key)
			faults[uid] = faulted{fault: fault, message: message}
		}
	}
	return faults, holds
}
