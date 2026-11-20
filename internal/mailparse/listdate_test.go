package mailparse

import (
	"testing"
	"time"
)

func TestListDateRule(t *testing.T) {
	firstArchivedAt := time.Date(2026, 11, 6, 15, 50, 52, 0, time.UTC)
	nineUTC := time.Date(2026, 11, 6, 9, 0, 0, 0, time.UTC)
	tests := []struct {
		name            string
		header          *string
		firstArchivedAt time.Time
		want            time.Time
		reason          DateReason
	}{
		{"missing", nil, firstArchivedAt, firstArchivedAt, DateMissing},
		{"unreadable", text("sometime next week"), firstArchivedAt, firstArchivedAt, DateUnreadable},
		{"empty", text(""), firstArchivedAt, firstArchivedAt, DateUnreadable},
		{
			"exactly 24 hours ahead",
			text("Sat, 07 Nov 2026 15:50:52 +0000"), firstArchivedAt,
			time.Date(2026, 11, 7, 15, 50, 52, 0, time.UTC), DateFromHeader,
		},
		{
			"24 hours and 1 ms ahead",
			text("Sat, 07 Nov 2026 15:50:52 +0000"), firstArchivedAt.Add(-time.Millisecond),
			firstArchivedAt.Add(-time.Millisecond), DateAhead,
		},
		{
			"three days ahead",
			text("Mon, 09 Nov 2026 15:50:52 +0000"), firstArchivedAt,
			firstArchivedAt, DateAhead,
		},
		{"1970", text("Thu, 01 Jan 1970 00:00:00 +0000"), firstArchivedAt, time.Unix(0, 0), DateFromHeader},
		{"plus one hour", text("Fri, 06 Nov 2026 10:00:00 +0100"), firstArchivedAt, nineUTC, DateFromHeader},
		{"minus seven hours", text("Fri, 06 Nov 2026 02:00:00 -0700"), firstArchivedAt, nineUTC, DateFromHeader},
		{"UTC", text("Fri, 06 Nov 2026 09:00:00 +0000"), firstArchivedAt, nineUTC, DateFromHeader},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, reason := ListDate(tt.header, tt.firstArchivedAt)
			if !got.Equal(tt.want) || reason != tt.reason {
				t.Errorf("ListDate = %s, %q; want %s, %q",
					got.UTC().Format(time.RFC3339Nano), reason, tt.want.UTC().Format(time.RFC3339Nano), tt.reason)
			}
		})
	}
}

func TestDateReasonWords(t *testing.T) {
	for reason, want := range map[DateReason]string{
		DateFromHeader: "", DateMissing: "missing", DateUnreadable: "unreadable", DateAhead: "ahead",
	} {
		if string(reason) != want {
			t.Errorf("reason %q, want %q", reason, want)
		}
	}
}
