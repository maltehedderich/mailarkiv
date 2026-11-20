package mailparse

import (
	"net/mail"
	"time"
)

// DateReason says why the list date isn't the Date header's time.
type DateReason string

const (
	DateFromHeader DateReason = ""
	DateMissing    DateReason = "missing"
	DateUnreadable DateReason = "unreadable"
	DateAhead      DateReason = "ahead"
)

// maxAhead is how far past the first archive time a Date header may be. Any
// later is a wrong clock, and the message would stay at the top of the list.
const maxAhead = 24 * time.Hour

// ListDate is the time the list sorts a message by: the Date header's time,
// or the first archive time when the header is missing, can't be read, or is
// more than 24 hours after it. A date however far in the past is kept.
func ListDate(date *string, firstArchivedAt time.Time) (time.Time, DateReason) {
	if date == nil {
		return firstArchivedAt, DateMissing
	}
	parsed, err := mail.ParseDate(*date)
	if err != nil {
		return firstArchivedAt, DateUnreadable
	}
	if parsed.Sub(firstArchivedAt) > maxAhead {
		return firstArchivedAt, DateAhead
	}
	return parsed, DateFromHeader
}
