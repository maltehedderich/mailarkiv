package mailparse

import "strings"

// Summary is what a list row shows, with each value cut to the length the
// catalog keeps. The view reads the full values from the original.
type Summary struct {
	// SenderName is nil when the first sender gives no name, or one equal to
	// the address ignoring case. When From isn't an address list, it holds
	// the decoded header text and SenderAddress is nil.
	SenderName     *string
	SenderAddress  *string
	MoreSenders    int
	Subject        *string
	DateHeader     *string
	HasAttachments bool
}

// Summary returns the row values for a message that was read.
func (m Message) Summary() Summary {
	var s Summary
	switch {
	case len(m.From) > 0:
		first := m.From[0]
		if first.Name != "" && !strings.EqualFold(first.Name, first.Address) {
			s.SenderName = cutPointer(&first.Name, maxSenderName)
		}
		s.SenderAddress = cutPointer(&first.Address, maxAddress)
		s.MoreSenders = len(m.From) - 1
	case m.FromText != nil:
		s.SenderName = cutPointer(m.FromText, maxSenderName)
	}
	s.Subject = cutPointer(m.Subject, maxSubject)
	s.DateHeader = cutPointer(m.Date, maxDateHeader)
	for _, attachment := range m.Attachments {
		if attachment.Kind != KindEmbeddedImage {
			s.HasAttachments = true
		}
	}
	return s
}

func cutPointer(value *string, limit int) *string {
	if value == nil {
		return nil
	}
	cut := cutRunes(*value, limit)
	return &cut
}
