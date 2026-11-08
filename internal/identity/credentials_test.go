package identity

import (
	"context"
	"errors"
	"strings"
	"testing"
)

func TestCredentialRefusalPreservesAccountAndSession(t *testing.T) {
	for _, tt := range []struct {
		name     string
		address  string
		password string
		want     error
	}{
		{"password too long", "admin@example.test", strings.Repeat("a", 4097), ErrPasswordTooLong},
		{"multibyte password too long", "admin@example.test", strings.Repeat("ä", 2049), ErrPasswordTooLong},
		{"invalid password encoding", "admin@example.test", goodPassword + "\xff", ErrPasswordEncoding},
		{"address too long", strings.Repeat("a", 1012) + "@example.test", goodPassword, ErrAddressTooLong},
		// U+023A takes two UTF-8 bytes; its lowercase U+2C65 takes three.
		{"lowercase address too long", strings.Repeat("Ⱥ", 338) + "@example.test", goodPassword, ErrAddressTooLong},
	} {
		t.Run(tt.name, func(t *testing.T) {
			f := newFixture(t)
			if err := f.identity.CreateAccount(context.Background(), tt.address, tt.password); !errors.Is(err, tt.want) {
				t.Fatalf("CreateAccount = %v, want %v", err, tt.want)
			}
			if got := len(f.storedAccounts(t)); got != 0 {
				t.Fatalf("refused creation stored %d accounts", got)
			}

			f.create(t, "admin@example.test", goodPassword)
			token := f.startSession(t, "admin@example.test", goodPassword)
			before := f.storedAccounts(t)["admin@example.test"]
			if err := f.identity.SetPassword(context.Background(), tt.address, tt.password); !errors.Is(err, tt.want) {
				t.Fatalf("SetPassword = %v, want %v", err, tt.want)
			}
			if after := f.storedAccounts(t)["admin@example.test"]; after != before {
				t.Fatal("refused replacement changed the password hash")
			}
			f.assertSession(t, token, "admin@example.test")
			if err := f.signIn("admin@example.test", goodPassword); err != nil {
				t.Fatalf("old password after refused replacement: %v", err)
			}
		})
	}
}
