package collect

import (
	"context"
	"errors"
	"testing"

	"github.com/maltehedderich/mailarkiv/internal/imaptest"
)

func TestSourceTrimsPastedEdgesBeforeCheckAndSave(t *testing.T) {
	ctx := context.Background()
	const padding = " \t\u0085\u00a0\u200b\ufeff"
	const user = "anna office@example.test"
	const password = " \u200bMARKER-PW-spaces\ufeff "
	const folder = " Drafts "
	double, err := imaptest.New(imaptest.Options{Security: imaptest.ImplicitTLS, User: user, Password: password})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { double.Close() })
	addFolders(t, double, folder)
	f := newFixture(t, double)
	collector := f.start(t)
	input := sourceFor(t, double, "tls", folder)
	host := input.Host
	input.Host = padding + host + padding
	input.Name = padding + "Anna Office" + padding
	input.UserName = padding + user + padding
	input.Password = password
	check := CheckInput{Host: input.Host, Port: input.Port, Security: input.Security, UserName: input.UserName, Password: input.Password}
	if _, err := collector.Check(ctx, check); err != nil {
		t.Fatalf("Check with pasted padding: %v", err)
	}
	source, err := collector.Add(ctx, input)
	if err != nil {
		t.Fatalf("Add with pasted padding: %v", err)
	}
	stored := waitForSuccess(t, collector, source.ID, f.clock)
	if stored.Host != host || stored.Name != "Anna Office" || stored.UserName != user {
		t.Fatalf("saved labels = %q, %q, %q", stored.Host, stored.Name, stored.UserName)
	}
	if len(stored.Folders) != 1 || stored.Folders[0].Name != folder {
		t.Fatalf("folder name was changed: %+v", stored.Folders)
	}
	// Rechecking uses the decrypted password; its surrounding characters matter.
	if _, err := collector.CheckSource(ctx, source.ID, MailboxChange{}); err != nil {
		t.Fatalf("CheckSource with stored password: %v", err)
	}
	input.Host, input.UserName = host, user
	_, err = collector.Add(ctx, input)
	var exists *SourceExistsError
	if !errors.As(err, &exists) || exists.ID != source.ID {
		t.Fatalf("Add without padding = %v, want duplicate", err)
	}
	_, err = collector.Check(ctx, check)
	if !errors.As(err, &exists) || exists.ID != source.ID {
		t.Fatalf("Check with padding = %v, want duplicate", err)
	}
}
