package collect

import (
	"context"
	"errors"
	"strings"

	"github.com/emersion/go-imap/v2"
)

// maxReply is the most characters of a server's reply kept.
const maxReply = 1000

// step is the part of a session an error came from.
type step int

const (
	dialing step = iota
	// securing is the TLS handshake, or for STARTTLS the plain greeting, the
	// upgrade, and the handshake after it.
	securing
	// greeting is the wait for the greeting after an implicit TLS handshake.
	greeting
	signingIn
	commanding
)

// classify gives err, from step of a session, its sync category, and keeps
// the server's NO or BAD reply. A failure keeps the category it has.
//
// A passed deadline is timed_out at any step. Otherwise a dial error is
// server_not_reachable, any failure while securing is
// secure_connection_failed, and NO to LOGIN is sign_in_rejected unless its
// code says the server is unavailable. Everything else is server_error.
func classify(ctx context.Context, wrapped *conn, at step, err error) error {
	var failed *failure
	if errors.As(err, &failed) {
		return err
	}
	f := &failure{err: err, reply: serverReply(err)}
	switch {
	case timedOut(ctx, wrapped, err):
		f.category = TimedOut
	case at == dialing:
		f.category = ServerNotReachable
	case at == securing:
		f.category = SecureConnectionFailed
	case at == signingIn && rejected(err):
		f.category = SignInRejected
	default:
		f.category = ServerError
	}
	return f
}

// timedOut reports whether a deadline ended the step: the wrapper's, the
// check limit's, or the dial's. The library wraps read errors with %v, so
// after the dial only the wrapper's record shows a timeout.
func timedOut(ctx context.Context, wrapped *conn, err error) bool {
	if wrapped != nil {
		return wrapped.timedOut() || errors.Is(ctx.Err(), context.DeadlineExceeded)
	}
	return isTimeout(err) || errors.Is(ctx.Err(), context.DeadlineExceeded)
}

// rejected reports whether err is a NO to LOGIN that refuses the sign-in. RFC
// 5530 marks only UNAVAILABLE as temporary.
func rejected(err error) bool {
	var imapErr *imap.Error
	return errors.As(err, &imapErr) &&
		imapErr.Type == imap.StatusResponseTypeNo &&
		imapErr.Code != imap.ResponseCodeUnavailable
}

// serverReply returns the code and text of a NO or BAD in err, as the server
// sent them, or "" when there is none.
func serverReply(err error) string {
	var imapErr *imap.Error
	if !errors.As(err, &imapErr) {
		return ""
	}
	reply := imapErr.Text
	if imapErr.Code != "" {
		reply = "[" + string(imapErr.Code) + "] " + reply
	}
	return shorten(strings.ToValidUTF8(reply, "�"), maxReply)
}

// shorten cuts s to at most max characters, keeping its start and end around
// an ellipsis.
func shorten(s string, max int) string {
	runes := []rune(s)
	if len(runes) <= max {
		return s
	}
	head := (max - 1) / 2
	tail := max - 1 - head
	return string(runes[:head]) + "…" + string(runes[len(runes)-tail:])
}
