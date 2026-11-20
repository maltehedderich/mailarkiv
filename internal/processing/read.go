package processing

import (
	"context"
	"fmt"

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
// archive.ErrOriginalDamaged, and its failure event is recorded.
func (p *Processor) Read(ctx context.Context, id int64, withText bool) (Content, error) {
	original, err := p.archive.OpenVerified(ctx, id)
	if err != nil {
		return Content{}, fmt.Errorf("read message %d: %w", id, err)
	}
	defer original.File.Close()
	answer, err := p.pool.read(ctx, original.File, original.Size, withText)
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
