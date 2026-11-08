package identity

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestPasswordHashRoundTrip(t *testing.T) {
	p := newPasswords()
	hash, err := p.hash(context.Background(), goodPassword)
	if err != nil {
		t.Fatal(err)
	}

	for _, tt := range []struct {
		password string
		want     bool
	}{
		{goodPassword, true},
		{goodPassword + " ", false},
		{"Correct horse battery", false},
	} {
		got, err := p.verify(hash, tt.password)
		if err != nil {
			t.Fatal(err)
		}
		if got != tt.want {
			t.Errorf("verify(%q) = %v, want %v", tt.password, got, tt.want)
		}
	}

	again, err := p.hash(context.Background(), goodPassword)
	if err != nil {
		t.Fatal(err)
	}
	if again == hash {
		t.Error("two hashes of one password are equal, so the salt isn't random")
	}
}

// Unknown addresses verify against the dummy hash, so it has to parse and use
// the same settings as a real one.
func TestDummyHashCostsTheSame(t *testing.T) {
	p := newPasswords()
	real, err := p.hash(context.Background(), goodPassword)
	if err != nil {
		t.Fatal(err)
	}
	realSettings, err := parseHash(real)
	if err != nil {
		t.Fatal(err)
	}
	dummySettings, err := parseHash(dummyHash)
	if err != nil {
		t.Fatalf("the dummy hash doesn't parse: %v", err)
	}
	if dummySettings.time != realSettings.time || dummySettings.memory != realSettings.memory ||
		dummySettings.threads != realSettings.threads || len(dummySettings.key) != len(realSettings.key) {
		t.Fatalf("dummy settings %+v differ from a real hash's %+v", dummySettings, realSettings)
	}
}

// At most two hashes run at once, which caps memory at 128 MiB. A caller
// waiting for a slot leaves when its context ends.
func TestWaiterLeavesWhenItsContextEnds(t *testing.T) {
	p := newPasswords()
	// Take both slots, as two hashes in progress would.
	p.slots <- struct{}{}
	p.slots <- struct{}{}

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	done := make(chan error, 1)
	go func() {
		_, err := p.hash(ctx, goodPassword)
		done <- err
	}()

	select {
	case err := <-done:
		if !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("hash = %v, want the context's error", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the waiter didn't leave when its context ended")
	}
}
