package collect

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/emersion/go-imap/v2"
)

// ServerFolder is a folder the server lists.
type ServerFolder struct {
	Name string
	// Messages is nil when the server reported no count.
	Messages *int
}

// Check signs in with new details and lists the server's folders. It
// returns an *InvalidFieldsError, ErrNoKey, a *SourceExistsError, or a
// *CheckFailedError. A duplicate is refused before connecting. Signing in
// and listing have the check limit.
func (c *Collector) Check(ctx context.Context, in CheckInput) ([]ServerFolder, error) {
	box, err := parseCheck(in)
	if err != nil {
		return nil, err
	}
	if c.deps.Key == nil {
		return nil, ErrNoKey
	}
	if err := refuseDuplicate(ctx, c.deps.DB.Reader, box.host, strings.ToLower(box.userName)); err != nil {
		return nil, err
	}
	return c.listFolders(ctx, box)
}

// CheckSource is Check for a stored source, with the stored port, security,
// and password for each field change leaves out. It saves nothing. A stored
// password that doesn't open breaks the password rule, because the check
// needs one.
func (c *Collector) CheckSource(ctx context.Context, id int64, in MailboxChange) ([]ServerFolder, error) {
	change, err := parseChange(in)
	if err != nil {
		return nil, err
	}
	stored, err := c.loadStored(ctx, id)
	if err != nil {
		return nil, err
	}
	box, err := c.mailboxFor(stored, change)
	if err != nil {
		return nil, err
	}
	return c.listFolders(ctx, box)
}

// storedSource is a source's sign-in details as stored.
type storedSource struct {
	box    mailbox // Without the password.
	sealed []byte
}

// loadStored reads an active source's sign-in details, or returns
// ErrNotFound.
func (c *Collector) loadStored(ctx context.Context, id int64) (storedSource, error) {
	var stored storedSource
	var security string
	err := c.deps.DB.Reader.QueryRowContext(ctx, `
SELECT host, port, security, user_name, sealed_password FROM imap_source WHERE id = ? AND removed_at IS NULL`, id).
		Scan(&stored.box.host, &stored.box.port, &security, &stored.box.userName, &stored.sealed)
	if errors.Is(err, sql.ErrNoRows) {
		return storedSource{}, ErrNotFound
	}
	if err != nil {
		return storedSource{}, fmt.Errorf("read source %d: %w", id, err)
	}
	if stored.box.security, err = parseSecurity(security); err != nil {
		return storedSource{}, fmt.Errorf("read source %d: %w", id, err)
	}
	return stored, nil
}

// mailboxFor applies change to the stored details, with the stored password
// unless change has one. It returns ErrNoKey, or an *InvalidFieldsError when
// the stored password is needed and doesn't open.
func (c *Collector) mailboxFor(stored storedSource, change mailboxChange) (mailbox, error) {
	if c.deps.Key == nil {
		return mailbox{}, ErrNoKey
	}
	box := stored.box
	if change.port != nil {
		box.port = *change.port
	}
	if change.security != nil {
		box.security = *change.security
	}
	if change.password != nil {
		box.password = *change.password
		return box, nil
	}
	password, err := openPassword(c.deps.Key, stored.sealed, box.host, box.userName)
	if err != nil {
		return mailbox{}, &InvalidFieldsError{Fields: []string{"password"}}
	}
	box.password = password
	return box, nil
}

// listFolders signs in to box and lists its folders, within the check
// limit.
func (c *Collector) listFolders(ctx context.Context, box mailbox) ([]ServerFolder, error) {
	check, cancel := context.WithTimeout(ctx, c.cfg.check)
	defer cancel()
	session, err := signIn(check, c.cfg, c.deps.Roots, box)
	if err != nil {
		return nil, checkFailed(err)
	}
	folders, err := session.folders()
	if err != nil {
		session.close()
		return nil, checkFailed(err)
	}
	// The list is complete, which is all the check needs.
	_ = session.logout()
	return folders, nil
}

// folders lists every folder that can be opened, INBOX first, then in the
// server's order, each with its message count. The counts come with LIST
// when the server offers LIST-STATUS, and from STATUS otherwise.
func (s *session) folders() ([]ServerFolder, error) {
	var caps imap.CapSet
	if err := s.do(func() error {
		var err error
		caps, err = s.client.Capability().Wait()
		return err
	}); err != nil {
		return nil, err
	}
	var options *imap.ListOptions
	listStatus := caps.Has(imap.CapListStatus)
	if listStatus {
		options = &imap.ListOptions{ReturnStatus: &imap.StatusOptions{NumMessages: true}}
	}
	var listed []*imap.ListData
	if err := s.do(func() error {
		var err error
		listed, err = s.client.List("", "*", options).Collect()
		return err
	}); err != nil {
		return nil, err
	}

	folders := []ServerFolder{}
	for _, data := range listed {
		if slices.Contains(data.Attrs, imap.MailboxAttrNoSelect) || slices.Contains(data.Attrs, imap.MailboxAttrNonExistent) {
			continue
		}
		folder := ServerFolder{Name: data.Mailbox}
		if listStatus {
			folder.Messages = messageCount(data.Status)
		} else {
			count, err := s.messageCount(data.Mailbox)
			if err != nil {
				return nil, err
			}
			folder.Messages = count
		}
		folders = append(folders, folder)
	}
	// INBOX's name is case-insensitive. The sort keeps the server's order
	// for the rest.
	slices.SortStableFunc(folders, func(a, b ServerFolder) int {
		return inboxFirst(a.Name) - inboxFirst(b.Name)
	})
	return folders, nil
}

func inboxFirst(name string) int {
	if strings.EqualFold(name, "INBOX") {
		return 0
	}
	return 1
}

// messageCount asks STATUS for a folder's count. A server that refuses
// gives no count, which doesn't fail the check.
func (s *session) messageCount(name string) (*int, error) {
	var data *imap.StatusData
	err := s.do(func() error {
		var err error
		data, err = s.client.Status(name, &imap.StatusOptions{NumMessages: true}).Wait()
		var refused *imap.Error
		if errors.As(err, &refused) {
			data = nil
			return nil
		}
		return err
	})
	if err != nil {
		return nil, err
	}
	return messageCount(data), nil
}

func messageCount(data *imap.StatusData) *int {
	if data == nil || data.NumMessages == nil {
		return nil
	}
	n := int(*data.NumMessages)
	return &n
}
