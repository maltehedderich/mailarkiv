package archive

import (
	"context"
	"fmt"
	"slices"
	"testing"
	"time"
)

// importNew imports bytes no other message has and returns the message's ID.
func (f *fixture) importNew(t *testing.T, label string) int64 {
	t.Helper()
	stored, ok := f.importBytes(t, fmt.Appendf(nil, "Subject: %s\r\n\r\nBody\r\n", label), nil).(Stored)
	if !ok || stored.Outcome != Archived {
		t.Fatalf("import %s = %#v, want Stored with Archived", label, stored)
	}
	return stored.Message.ID
}

// setDates writes a message's list date and first archive time, in Unix
// milliseconds, as processing and capture would.
func (f *fixture) setDates(t *testing.T, messageID, listDate, firstArchived int64) {
	t.Helper()
	_, err := f.db.Writer.Exec("UPDATE message SET list_date = ?, first_archived_at = ? WHERE id = ?",
		listDate, firstArchived, messageID)
	if err != nil {
		t.Fatal(err)
	}
}

// listedIDs returns the IDs on one page of the list, in order, and the total.
func (f *fixture) listedIDs(t *testing.T, page int) ([]int64, int) {
	t.Helper()
	listed, err := f.archive.List(context.Background(), page)
	if err != nil {
		t.Fatalf("List(%d): %v", page, err)
	}
	ids := make([]int64, len(listed.Items))
	for i, item := range listed.Items {
		ids[i] = item.ID
	}
	return ids, listed.Total
}

// The list is ordered by list date, then first archive time, then ID, each
// newest first. The dates are chosen so that neither time alone, nor the ID,
// gives the expected order.
func TestListOrder(t *testing.T) {
	f := newFixture(t)
	ids := make([]int64, 5)
	for i := range ids {
		ids[i] = f.importNew(t, fmt.Sprint(i))
	}
	dates := []struct{ listDate, firstArchived int64 }{
		{5_000, 1_000},
		{9_000, 1_000}, // The newest list date, so first.
		{5_000, 2_000}, // The same list date as ids[0], archived later, so before it.
		{3_000, 4_000}, // The same dates as ids[4], with a lower ID, so after it.
		{3_000, 4_000},
	}
	for i, date := range dates {
		f.setDates(t, ids[i], date.listDate, date.firstArchived)
	}

	got, total := f.listedIDs(t, 1)
	want := []int64{ids[1], ids[2], ids[0], ids[4], ids[3]}
	if !slices.Equal(got, want) || total != len(ids) {
		t.Fatalf("list = %v with a total of %d, want %v and %d", got, total, want, len(ids))
	}
}

// Messages captured with one clock reading share both dates, so the ID alone
// orders them, and walking the pages lists each once.
func TestListPagesThroughEqualDates(t *testing.T) {
	const messages = 120
	f := newFixture(t)
	for i := range messages {
		f.importNew(t, fmt.Sprint(i))
	}

	var walked []int64
	for page, size := range []int{50, 50, 20, 0} {
		ids, total := f.listedIDs(t, page+1)
		if len(ids) != size || total != messages {
			t.Fatalf("page %d has %d items with a total of %d, want %d and %d", page+1, len(ids), total, size, messages)
		}
		walked = append(walked, ids...)
	}

	if len(walked) != messages {
		t.Fatalf("the pages listed %d messages, want %d", len(walked), messages)
	}
	for i := 1; i < len(walked); i++ {
		if walked[i] >= walked[i-1] {
			t.Fatalf("message %d follows message %d, want each ID below the one before", walked[i], walked[i-1])
		}
	}
}

// Importing a message again adds an entry but doesn't move it in the list.
func TestListKeepsAMessageImportedAgain(t *testing.T) {
	f := newFixture(t)
	var ids []int64
	for i, label := range []string{"first", "second", "third"} {
		f.archive.now = func() time.Time { return testNow.Add(time.Duration(i) * time.Minute) }
		ids = append(ids, f.importNew(t, label))
	}
	want := []int64{ids[2], ids[1], ids[0]}
	if got, _ := f.listedIDs(t, 1); !slices.Equal(got, want) {
		t.Fatalf("list = %v, want %v", got, want)
	}

	f.archive.now = func() time.Time { return testNow.Add(time.Hour) }
	again, ok := f.importBytes(t, []byte("Subject: first\r\n\r\nBody\r\n"), name("again.eml")).(Stored)
	if !ok || again.Outcome != AlreadyArchived || again.Message.ID != ids[0] {
		t.Fatalf("second import = %#v, want Stored with AlreadyArchived for message %d", again, ids[0])
	}
	if got, _ := f.listedIDs(t, 1); !slices.Equal(got, want) {
		t.Fatalf("list after importing %d again = %v, want %v", ids[0], got, want)
	}
}
