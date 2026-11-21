package archive

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"math"
	"os"
	"reflect"
	"slices"
	"testing"
	"time"
)

func text(s string) *string { return &s }

// readSummary is a result for a message that was read with one problem.
func readSummary(listDate time.Time) ProcessingResult {
	return ProcessingResult{
		State:    ProcessedWithProblems,
		ListDate: listDate,
		Summary: &Summary{
			SenderName:     text("Anna Schmidt"),
			SenderAddress:  text("anna@example.test"),
			MoreSenders:    2,
			Subject:        text("Quartalsbericht"),
			DateHeader:     text("Mon, 02 Nov 2026 09:14:55 +0100"),
			HasAttachments: true,
		},
		Problems: []Problem{
			{Position: "2", DeclaredType: text("application/pdf"), Kind: "invalid_encoding"},
			{Position: "", Kind: "malformed_header"},
		},
	}
}

func (f *fixture) record(t *testing.T, messageID int64, result ProcessingResult) bool {
	t.Helper()
	changed, err := f.archive.RecordProcessed(context.Background(), messageID, result)
	if err != nil {
		t.Fatalf("RecordProcessed(%d): %v", messageID, err)
	}
	return changed
}

type storedProcessing struct {
	state    string
	seq      sql.NullInt64
	listDate int64
	summary  int
	problems int
}

func (f *fixture) storedProcessing(t *testing.T, messageID int64) storedProcessing {
	t.Helper()
	var got storedProcessing
	err := f.db.Reader.QueryRow(`
SELECT processing_state, processed_seq, list_date,
       (SELECT COUNT(*) FROM message_summary WHERE message_id = m.id),
       (SELECT COUNT(*) FROM message_problem WHERE message_id = m.id)
FROM message m WHERE id = ?`, messageID).Scan(&got.state, &got.seq, &got.listDate, &got.summary, &got.problems)
	if err != nil {
		t.Fatal(err)
	}
	return got
}

func TestRecordProcessed(t *testing.T) {
	f := newFixture(t)
	ids := []int64{f.importNew(t, "a"), f.importNew(t, "b"), f.importNew(t, "c")}
	listDate := time.Date(2026, 11, 2, 8, 14, 55, 0, time.UTC)

	if !f.record(t, ids[1], readSummary(listDate)) {
		t.Fatal("the first result changed nothing")
	}
	got := f.storedProcessing(t, ids[1])
	want := storedProcessing{"processed_with_problems", sql.NullInt64{Int64: 1, Valid: true}, listDate.UnixMilli(), 1, 2}
	if got != want {
		t.Errorf("stored %+v, want %+v", got, want)
	}

	var summary Summary
	var reason sql.NullString
	err := f.db.Reader.QueryRow(`
SELECT sender_name, sender_address, more_senders, subject, date_header, date_reason, has_attachments
FROM message_summary WHERE message_id = ?`, ids[1]).Scan(&summary.SenderName, &summary.SenderAddress,
		&summary.MoreSenders, &summary.Subject, &summary.DateHeader, &reason, &summary.HasAttachments)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(summary, *readSummary(listDate).Summary) || reason.Valid {
		t.Errorf("summary %+v with reason %v, want %+v and no reason", summary, reason, *readSummary(listDate).Summary)
	}
	var declared sql.NullString
	if err := f.db.Reader.QueryRow(
		"SELECT declared_type FROM message_problem WHERE message_id = ? AND position = ''", ids[1]).Scan(&declared); err != nil {
		t.Fatal(err)
	}
	if declared.Valid {
		t.Errorf("the whole message's problem has the declared type %q, want NULL", declared.String)
	}

	// A second result for the same message changes nothing.
	again := ProcessingResult{State: Processed, ListDate: time.UnixMilli(0), Summary: &Summary{}}
	if f.record(t, ids[1], again) {
		t.Error("a second result changed the message")
	}
	if got := f.storedProcessing(t, ids[1]); got != want {
		t.Errorf("after a second result, stored %+v, want %+v", got, want)
	}

	// An unreadable message gets no summary, keeps its first archive time,
	// and takes the next number.
	firstArchived := f.storedProcessing(t, ids[0]).listDate
	unreadable := ProcessingResult{
		State:    Unreadable,
		ListDate: time.UnixMilli(firstArchived),
		Problems: []Problem{{Position: "", Kind: "malformed_header"}},
	}
	f.record(t, ids[0], unreadable)
	got = f.storedProcessing(t, ids[0])
	want = storedProcessing{"unreadable", sql.NullInt64{Int64: 2, Valid: true}, firstArchived, 0, 1}
	if got != want {
		t.Errorf("unreadable stored %+v, want %+v", got, want)
	}

	// A message read without problems.
	dated := ProcessingResult{State: Processed, ListDate: time.UnixMilli(firstArchived), Summary: &Summary{
		DateReason: text("missing"),
	}}
	f.record(t, ids[2], dated)
	got = f.storedProcessing(t, ids[2])
	want = storedProcessing{"processed", sql.NullInt64{Int64: 3, Valid: true}, firstArchived, 1, 0}
	if got != want {
		t.Errorf("processed stored %+v, want %+v", got, want)
	}

	highest, err := f.archive.HighestProcessedSeq(context.Background())
	if err != nil || highest != 3 {
		t.Errorf("HighestProcessedSeq = %d, %v; want 3", highest, err)
	}
}

func TestRecordProcessedRefusesNotProcessed(t *testing.T) {
	f := newFixture(t)
	id := f.importNew(t, "a")
	_, err := f.archive.RecordProcessed(context.Background(), id, ProcessingResult{State: NotProcessed})
	if err == nil {
		t.Error("recording the state not_processed succeeded")
	}
}

func TestHighestProcessedSeqWhenEmpty(t *testing.T) {
	f := newFixture(t)
	f.importNew(t, "a")
	if highest, err := f.archive.HighestProcessedSeq(context.Background()); err != nil || highest != 0 {
		t.Errorf("HighestProcessedSeq = %d, %v; want 0", highest, err)
	}
}

func TestCountNotProcessed(t *testing.T) {
	f := newFixture(t)
	var ids []int64
	for i := range 3 {
		ids = append(ids, f.importNew(t, fmt.Sprint(i)))
	}
	f.record(t, ids[1], readSummary(testNow))
	if count, err := f.archive.CountNotProcessed(context.Background()); err != nil || count != 2 {
		t.Errorf("CountNotProcessed = %d, %v; want 2", count, err)
	}
}

func TestNotProcessed(t *testing.T) {
	f := newFixture(t)
	var ids []int64
	for i := range 5 {
		ids = append(ids, f.importNew(t, fmt.Sprint(i)))
	}
	f.record(t, ids[3], readSummary(testNow))

	got, err := f.archive.NotProcessed(context.Background(), 10, nil)
	if err != nil {
		t.Fatal(err)
	}
	if want := []int64{ids[4], ids[2], ids[1], ids[0]}; !slices.Equal(got, want) {
		t.Errorf("NotProcessed = %v, want %v, newest capture first", got, want)
	}

	got, err = f.archive.NotProcessed(context.Background(), 2, map[int64]bool{ids[4]: true, ids[1]: true})
	if err != nil {
		t.Fatal(err)
	}
	if want := []int64{ids[2], ids[0]}; !slices.Equal(got, want) {
		t.Errorf("NotProcessed with two skipped = %v, want %v", got, want)
	}
}

func TestProcessedAfter(t *testing.T) {
	f := newFixture(t)
	var ids []int64
	for i := range 4 {
		ids = append(ids, f.importNew(t, fmt.Sprint(i)))
	}
	listDate := time.Date(2026, 11, 2, 8, 14, 55, 0, time.UTC)
	// Processed in an order unlike the IDs, so the sequence decides.
	f.record(t, ids[2], readSummary(listDate))
	f.record(t, ids[0], ProcessingResult{State: Unreadable, ListDate: testNow.Truncate(time.Millisecond)})
	f.record(t, ids[3], readSummary(listDate))

	got, err := f.archive.ProcessedAfter(context.Background(), 1, 10)
	if err != nil {
		t.Fatal(err)
	}
	want := []ProcessedMessage{
		{ID: ids[0], Seq: 2, State: Unreadable, FirstArchivedAt: testNow.Truncate(time.Millisecond), ListDate: testNow.Truncate(time.Millisecond)},
		{ID: ids[3], Seq: 3, State: ProcessedWithProblems, FirstArchivedAt: testNow.Truncate(time.Millisecond), ListDate: listDate, Summary: readSummary(listDate).Summary},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("ProcessedAfter(1, 10) = %+v, want %+v", got, want)
	}

	got, err = f.archive.ProcessedAfter(context.Background(), 0, 1)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].ID != ids[2] || got[0].Seq != 1 {
		t.Errorf("ProcessedAfter(0, 1) = %+v, want message %d with sequence 1", got, ids[2])
	}
}

func TestRows(t *testing.T) {
	f := newFixture(t)
	ids := []int64{f.importNew(t, "a"), f.importNew(t, "b"), f.importNew(t, "c")}
	listDate := time.Date(2026, 11, 2, 8, 14, 55, 0, time.UTC)
	f.record(t, ids[1], readSummary(listDate))

	rows, err := f.archive.Rows(context.Background(), []int64{ids[2], ids[0], ids[1]})
	if err != nil {
		t.Fatal(err)
	}
	var order []int64
	for _, row := range rows {
		order = append(order, row.ID)
	}
	if want := []int64{ids[2], ids[0], ids[1]}; !slices.Equal(order, want) {
		t.Fatalf("Rows gave %v, want %v in the order given", order, want)
	}
	waiting, read := rows[0], rows[2]
	if waiting.Processing != NotProcessed || waiting.Summary != nil || !waiting.ListDateIsArchiveTime ||
		!waiting.ListDate.Equal(testNow.Truncate(time.Millisecond)) {
		t.Errorf("a message not processed reads as %+v", waiting)
	}
	if read.Processing != ProcessedWithProblems || read.ListDateIsArchiveTime || !read.ListDate.Equal(listDate) ||
		!reflect.DeepEqual(read.Summary, readSummary(listDate).Summary) {
		t.Errorf("a processed message reads as %+v", read)
	}
	if read.Latest == nil || read.ObservationCount != 1 {
		t.Errorf("a processed message lost its history: %+v", read)
	}

	// The list reads rows the same way.
	page, err := f.archive.List(context.Background(), 1)
	if err != nil {
		t.Fatal(err)
	}
	for _, item := range page.Items {
		if item.ID == ids[1] && !reflect.DeepEqual(item, read) {
			t.Errorf("the list shows %+v, Rows %+v", item, read)
		}
	}
}

func TestRowsMarkArchiveTime(t *testing.T) {
	f := newFixture(t)
	id := f.importNew(t, "a")
	f.record(t, id, ProcessingResult{
		State: Processed, ListDate: testNow.Truncate(time.Millisecond),
		Summary: &Summary{DateReason: text("ahead")},
	})
	rows, err := f.archive.Rows(context.Background(), []int64{id})
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 || !rows[0].ListDateIsArchiveTime {
		t.Errorf("a list date chosen for a date ahead reads as %+v, want the archive time mark", rows)
	}
}

func signalled(signal <-chan struct{}) bool {
	select {
	case <-signal:
		return true
	default:
		return false
	}
}

func TestSignals(t *testing.T) {
	f := newFixture(t)
	body := []byte("Subject: Signal\r\n\r\nBody\r\n")

	stored := f.importBytes(t, body, nil).(Stored)
	if !signalled(f.archive.CaptureSignal()) {
		t.Error("a new message didn't fire the capture signal")
	}
	if signalled(f.archive.RestoreSignal()) {
		t.Error("a new message fired the restore signal")
	}

	f.importBytes(t, body, nil)
	if signalled(f.archive.CaptureSignal()) || signalled(f.archive.RestoreSignal()) {
		t.Error("an import of known bytes fired a signal")
	}

	if err := os.Remove(f.archive.blobPath(stored.Message.ContentHash)); err != nil {
		t.Fatal(err)
	}
	if restored := f.importBytes(t, body, nil).(Stored); restored.Outcome != Restored {
		t.Fatalf("import after removing the original = %v, want Restored", restored.Outcome)
	}
	if !signalled(f.archive.RestoreSignal()) {
		t.Error("restoring an original didn't fire the restore signal")
	}
	if signalled(f.archive.CaptureSignal()) {
		t.Error("restoring an original fired the capture signal")
	}

	// Signals never block a capture: many captures leave one signal.
	for i := range 3 {
		f.importNew(t, fmt.Sprint(i))
	}
	if !signalled(f.archive.CaptureSignal()) || signalled(f.archive.CaptureSignal()) {
		t.Error("three captures didn't leave exactly one waiting signal")
	}
}

// Processing dates a message from the original it opens.
func TestOpenVerifiedGivesFirstArchiveTime(t *testing.T) {
	f := newFixture(t)
	id := f.importNew(t, "a")
	original, err := f.archive.OpenVerified(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	defer original.File.Close()
	if want := testNow.Truncate(time.Millisecond); !original.FirstArchivedAt.Equal(want) {
		t.Errorf("FirstArchivedAt = %s, want %s", original.FirstArchivedAt, want)
	}
}

// unreadableAt is the result for a message no worker could read.
func unreadableAt(listDate time.Time) ProcessingResult {
	return ProcessingResult{State: Unreadable, ListDate: listDate, Problems: []Problem{{Position: "", Kind: "took_too_long"}}}
}

// problemIDs returns the IDs on one page of the problem list, in order, and
// the total.
func (f *fixture) problemIDs(t *testing.T, page int) ([]int64, int) {
	t.Helper()
	listed, err := f.archive.ProblemList(context.Background(), page)
	if err != nil {
		t.Fatalf("ProblemList(%d): %v", page, err)
	}
	ids := make([]int64, len(listed.Items))
	for i, item := range listed.Items {
		ids[i] = item.ID
	}
	return ids, listed.Total
}

// The problem list holds the messages processed with problems or that
// couldn't be read, in the list's order. The newest list dates belong to
// messages it leaves out.
func TestProblemList(t *testing.T) {
	f := newFixture(t)
	ids := make([]int64, 6)
	for i := range ids {
		ids[i] = f.importNew(t, fmt.Sprint(i))
	}
	f.record(t, ids[0], unreadableAt(testNow))
	f.record(t, ids[1], readSummary(testNow))
	f.record(t, ids[2], ProcessingResult{State: Processed, ListDate: testNow, Summary: &Summary{}})
	f.record(t, ids[3], readSummary(testNow))
	f.record(t, ids[5], unreadableAt(testNow))
	dates := []struct{ listDate, firstArchived int64 }{
		{5_000, 1_000},
		{9_000, 1_000},  // The newest problem, so first.
		{10_000, 1_000}, // Processed without problems.
		{5_000, 2_000},  // The same list date as ids[0], archived later, so before it.
		{11_000, 1_000}, // Not processed yet.
		{3_000, 4_000},
	}
	for i, date := range dates {
		f.setDates(t, ids[i], date.listDate, date.firstArchived)
	}

	got, total := f.problemIDs(t, 1)
	want := []int64{ids[1], ids[3], ids[0], ids[5]}
	if !slices.Equal(got, want) || total != len(want) {
		t.Fatalf("problem list = %v with a total of %d, want %v and %d", got, total, want, len(want))
	}

	// The problem list reads rows as the message list does.
	page, err := f.archive.ProblemList(context.Background(), 1)
	if err != nil {
		t.Fatal(err)
	}
	rows, err := f.archive.Rows(context.Background(), want)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(page.Items, rows) {
		t.Errorf("the problem list shows %+v, Rows %+v", page.Items, rows)
	}
	if unreadable := page.Items[2]; unreadable.Processing != Unreadable || unreadable.Summary != nil ||
		!unreadable.ListDateIsArchiveTime || unreadable.Latest == nil {
		t.Errorf("an unreadable message reads as %+v", unreadable)
	}
}

// The problem list pages as the message list does, and counts only its own
// rows.
func TestProblemListPages(t *testing.T) {
	const problems = 55
	f := newFixture(t)
	for i := range problems {
		f.record(t, f.importNew(t, fmt.Sprint(i)), unreadableAt(testNow))
	}
	f.importNew(t, "waiting")
	f.record(t, f.importNew(t, "read"), ProcessingResult{State: Processed, ListDate: testNow, Summary: &Summary{}})

	var walked []int64
	for page, size := range []int{50, 5, 0} {
		ids, total := f.problemIDs(t, page+1)
		if len(ids) != size || total != problems {
			t.Fatalf("page %d has %d items with a total of %d, want %d and %d", page+1, len(ids), total, size, problems)
		}
		walked = append(walked, ids...)
	}
	for i := 1; i < len(walked); i++ {
		if walked[i] >= walked[i-1] {
			t.Fatalf("message %d follows message %d, want each ID below the one before", walked[i], walked[i-1])
		}
	}

	if ids, total := f.problemIDs(t, math.MaxInt); len(ids) != 0 || total != problems {
		t.Errorf("the last possible page has %d items with a total of %d, want 0 and %d", len(ids), total, problems)
	}
	if _, err := f.archive.ProblemList(context.Background(), 0); err == nil {
		t.Error("page 0 gave no error")
	}
}

func TestProcessingCounts(t *testing.T) {
	f := newFixture(t)
	if counts, err := f.archive.ProcessingCounts(context.Background()); err != nil || counts != (ProcessingCounts{}) {
		t.Errorf("an empty archive counts %+v, %v; want all 0", counts, err)
	}

	ids := make([]int64, 7)
	for i := range ids {
		ids[i] = f.importNew(t, fmt.Sprint(i))
	}
	f.record(t, ids[0], unreadableAt(testNow))
	f.record(t, ids[2], readSummary(testNow))
	f.record(t, ids[3], ProcessingResult{State: Processed, ListDate: testNow, Summary: &Summary{}})
	f.record(t, ids[5], readSummary(testNow))
	f.record(t, ids[6], readSummary(testNow))

	counts, err := f.archive.ProcessingCounts(context.Background())
	want := ProcessingCounts{NotProcessed: 2, ProcessedWithProblems: 3, Unreadable: 1}
	if err != nil || counts != want {
		t.Errorf("ProcessingCounts = %+v, %v; want %+v", counts, err, want)
	}
}

func TestReadMessage(t *testing.T) {
	f := newFixture(t)
	waiting, read := f.importNew(t, "a"), f.importNew(t, "b")
	listDate := time.Date(2026, 11, 2, 8, 14, 55, 0, time.UTC)
	result := readSummary(listDate)
	// Recorded out of order: part numbers compare as numbers, one part at a
	// time, and the whole message comes last.
	result.Problems = []Problem{
		{Position: "", Kind: "too_many_parts"},
		{Position: "10", Kind: "unknown_charset"},
		{Position: "2.1", Kind: "invalid_encoding"},
		{Position: "2", DeclaredType: text("text/plain"), Kind: "unknown_charset"},
		{Position: "2", DeclaredType: text("text/plain"), Kind: "body_cut"},
		{Position: "1.10", Kind: "invalid_encoding"},
		{Position: "1.9", Kind: "invalid_encoding"},
	}
	f.record(t, read, result)

	message, problems, err := f.archive.ReadMessage(context.Background(), read)
	if err != nil {
		t.Fatal(err)
	}
	rows, err := f.archive.Rows(context.Background(), []int64{read})
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(message, rows[0]) {
		t.Errorf("ReadMessage gave %+v, the list row is %+v", message, rows[0])
	}
	// Within a part, kinds go in the order of the wire words' table.
	want := []Problem{
		{Position: "1.9", Kind: "invalid_encoding"},
		{Position: "1.10", Kind: "invalid_encoding"},
		{Position: "2", DeclaredType: text("text/plain"), Kind: "unknown_charset"},
		{Position: "2", DeclaredType: text("text/plain"), Kind: "body_cut"},
		{Position: "2.1", Kind: "invalid_encoding"},
		{Position: "10", Kind: "unknown_charset"},
		{Position: "", Kind: "too_many_parts"},
	}
	if !reflect.DeepEqual(problems, want) {
		t.Errorf("problems %+v, want %+v", problems, want)
	}

	message, problems, err = f.archive.ReadMessage(context.Background(), waiting)
	if err != nil || message.Processing != NotProcessed || problems != nil {
		t.Errorf("a message not processed reads as %+v with problems %+v, %v", message, problems, err)
	}

	if _, _, err := f.archive.ReadMessage(context.Background(), 999); !errors.Is(err, ErrNotFound) {
		t.Errorf("an unknown ID gave %v, want ErrNotFound", err)
	}
}
