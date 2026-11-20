package processing

import (
	"context"
	"errors"
	"fmt"
	"io"
	"time"

	"github.com/maltehedderich/mailarkiv/internal/mailparse"
)

// Address is one mailbox. Name is empty when it has none. When a field
// isn't an address list, its decoded text is the Name and Address is empty.
type Address struct {
	Name    string
	Address string
}

// Content is what the index needs from one original.
type Content struct {
	Subject *string
	From    []Address
	// Recipients holds To, Cc, and Bcc.
	Recipients      []Address
	AttachmentNames []string
	// Text holds the bodies' words, when they were asked for.
	Text []string
}

// Read parses message id's original in a background worker. An original
// that is missing or damaged gives archive.ErrOriginalMissing or
// archive.ErrOriginalDamaged, and its failure event is recorded. Workers
// that stop on it give a *WorkerStoppedError.
func (p *Processor) Read(ctx context.Context, id int64, withText bool) (Content, error) {
	answer, _, err := p.readOriginal(ctx, id, withText)
	if err != nil {
		return Content{}, fmt.Errorf("read message %d: %w", id, err)
	}
	m := answer.Message
	content := Content{Subject: m.Subject, Text: answer.Text}
	content.From = addresses(m.From)
	if m.FromText != nil {
		content.From = []Address{{Name: *m.FromText}}
	}
	for _, list := range [][]mailparse.Address{m.To, m.Cc, m.Bcc} {
		content.Recipients = append(content.Recipients, addresses(list)...)
	}
	for _, attachment := range m.Attachments {
		if attachment.Name != nil {
			content.AttachmentNames = append(content.AttachmentNames, *attachment.Name)
		}
	}
	return content, nil
}

func addresses(list []mailparse.Address) []Address {
	var out []Address
	for _, address := range list {
		out = append(out, Address{Name: address.Name, Address: address.Address})
	}
	return out
}

// readOriginal opens message id's original and parses it in a worker. It
// also returns the time of the message's first entry. A worker that dies may
// have met bad luck rather than the message, so a fresh one tries once more.
// A worker killed at the limit isn't tried again. Each stop is logged.
func (p *Processor) readOriginal(ctx context.Context, id int64, withText bool) (readAnswer, time.Time, error) {
	original, err := p.archive.OpenVerified(ctx, id)
	if err != nil {
		return readAnswer{}, time.Time{}, err
	}
	defer original.File.Close()
	for attempt := 1; ; attempt++ {
		answer, err := p.pool.read(ctx, original.File, original.Size, withText)
		stopped, ok := errors.AsType[*WorkerStoppedError](err)
		if !ok {
			return answer, original.FirstArchivedAt, err
		}
		p.logger.Error("processing_worker_stopped", "message_id", id, "exit", stopped.Exit)
		if stopped.TookTooLong || attempt == 2 {
			return readAnswer{}, original.FirstArchivedAt, stopped
		}
		if _, err := original.File.Seek(0, io.SeekStart); err != nil {
			return readAnswer{}, original.FirstArchivedAt, fmt.Errorf("read the original again: %w", err)
		}
	}
}
