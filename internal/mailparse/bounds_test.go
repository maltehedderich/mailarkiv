package mailparse

import (
	"fmt"
	"reflect"
	"strconv"
	"testing"
	"time"
)

func TestParseBounds(t *testing.T) {
	t.Run("800 levels", func(t *testing.T) {
		original := nestedMessage(800, "\r\n")
		start := time.Now()
		got := Read(original)
		if elapsed := time.Since(start); elapsed > time.Second {
			t.Errorf("Read took %s, want at most 1 s", elapsed)
		}
		want := []Problem{{Position: depthPosition(21), DeclaredType: "multipart/mixed", Kind: NestedTooDeeply}}
		if got.Unreadable || !reflect.DeepEqual(got.Problems, want) {
			t.Errorf("unreadable %t with problems %s, want %s", got.Unreadable, describe(got.Problems), describe(want))
		}
	})

	t.Run("12,000 parts", func(t *testing.T) {
		got := Read(flatMessage(12_000, flatOptions{eol: "\r\n", quoted: true}))
		want := []Problem{{Position: "1001", DeclaredType: "text/plain", Kind: TooManyParts}}
		if got.Unreadable || !reflect.DeepEqual(got.Problems, want) {
			t.Errorf("unreadable %t with problems %s, want %s", got.Unreadable, describe(got.Problems), describe(want))
		}
		if wantBody := (&Body{Position: "1", Content: "Teil 1"}); !reflect.DeepEqual(got.Plain, wantBody) {
			t.Errorf("plain body %s, want %s", describe(got.Plain), describe(wantBody))
		}
		// Parts 2 to 1,000 are text too, and each is an attachment.
		if len(got.Attachments) != 999 {
			t.Fatalf("%d attachments, want 999", len(got.Attachments))
		}
		for i, attachment := range got.Attachments {
			if want := strconv.Itoa(i + 2); attachment.Position != want || attachment.Size != int64(len(fmt.Sprintf("Teil %s", want))) {
				t.Fatalf("attachment %d is %s, want part %s with its text", i, describe(attachment), want)
			}
		}
	})
}
