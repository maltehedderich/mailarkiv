package processing

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"time"

	"github.com/maltehedderich/mailarkiv/internal/archive"
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

// Viewer reads originals for the message view through the interactive pool.
// Unlike processing, it doesn't try again after a worker dies: the view
// would wait past its deadline.
type Viewer struct {
	archive *archive.Archive
	pool    *Pool
	logger  *slog.Logger
}

// NewViewer returns a viewer that reads originals through interactive.
func NewViewer(a *archive.Archive, interactive *Pool, logger *slog.Logger) *Viewer {
	return &Viewer{archive: a, pool: interactive, logger: logger}
}

var (
	// ErrNoBody means the message has no body of the kind asked for.
	ErrNoBody = errors.New("the message has no such body")
	// ErrNoAttachment means the message lists no attachment at the position.
	ErrNoAttachment = errors.New("the message lists no attachment there")
)

// MessageView is what the message view shows of one message.
type MessageView struct {
	Message archive.Message
	// Problems go in part order, with the whole message last.
	Problems []archive.Problem
	// Parsed is nil unless the message is processed or processed with
	// problems.
	Parsed *Parsed
}

// Parsed is what a worker read from the original for the view.
type Parsed struct {
	Subject           *string
	From, To, Cc, Bcc []Address
	Encrypted         bool
	// HTML and Text are nil when the message lacks that body.
	HTML        *HTMLBody
	Text        *TextBody
	Attachments []Attachment
}

// HTMLBody describes the HTML body.
type HTMLBody struct {
	// Truncated is true when the body passed 2,000,000 bytes.
	Truncated bool
	// RemoteImages is true when it references http: or https: images.
	RemoteImages bool
	// EmbeddedImagesLeftOut is true when an embedded image wasn't placed
	// because of the 10,000,000-byte limit.
	EmbeddedImagesLeftOut bool
}

// TextBody describes the plain text body.
type TextBody struct {
	Truncated bool
}

// Attachment is one part other than the displayed bodies.
type Attachment struct {
	Position string
	// Name is nil when the part gives none.
	Name *string
	// DeclaredType is "" when the part declares none.
	DeclaredType string
	// Size is decoded bytes.
	Size    int64
	Damaged bool
	// Kind is file, message, or embedded_image.
	Kind string
}

// AttachmentFile is what a download sends before the bytes.
type AttachmentFile struct {
	// Name is the cleaned file name.
	Name string
	Size int64
}

// Content reads message id for the view. The catalog answers for a message
// that isn't processed or couldn't be read. Otherwise a worker parses the
// original. Errors are archive.ErrNotFound, archive.ErrOriginalMissing,
// archive.ErrOriginalDamaged, ErrBusy, and *WorkerStoppedError.
func (v *Viewer) Content(ctx context.Context, id int64) (MessageView, error) {
	message, problems, err := v.archive.ReadMessage(ctx, id)
	if err != nil {
		return MessageView{}, err
	}
	view := MessageView{Message: message, Problems: problems}
	if message.Processing != archive.Processed && message.Processing != archive.ProcessedWithProblems {
		return view, nil
	}
	var answer readAnswer
	err = v.withOriginal(ctx, id, func(original archive.Original) error {
		var err error
		answer, err = v.pool.readFor(ctx, workerRequest{Op: "read", Size: original.Size, View: true}, original.File)
		return err
	})
	if err != nil {
		return MessageView{}, err
	}
	view.Parsed = parsed(answer)
	return view, nil
}

func parsed(answer readAnswer) *Parsed {
	m := answer.Message
	p := &Parsed{
		Subject:   m.Subject,
		From:      addresses(m.From),
		To:        addresses(m.To),
		Cc:        addresses(m.Cc),
		Bcc:       addresses(m.Bcc),
		Encrypted: m.Encrypted,
	}
	if m.FromText != nil {
		p.From = []Address{{Name: *m.FromText}}
	}
	if view := answer.View; view != nil {
		if view.HTML != nil {
			p.HTML = &HTMLBody{
				Truncated:             view.HTML.Truncated,
				RemoteImages:          view.HTML.RemoteImages,
				EmbeddedImagesLeftOut: view.HTML.EmbeddedImagesLeftOut,
			}
		}
		if view.Text != nil {
			p.Text = &TextBody{Truncated: view.Text.Truncated}
		}
	}
	for _, a := range m.Attachments {
		p.Attachments = append(p.Attachments, Attachment{
			Position: a.Position, Name: a.Name, DeclaredType: a.DeclaredType,
			Size: a.Size, Damaged: a.Damaged, Kind: string(a.Kind),
		})
	}
	return p
}

// Text returns message id's plain text body, at most 2,000,000 bytes.
// ErrNoBody means it has none. Other errors are those of Content.
func (v *Viewer) Text(ctx context.Context, id int64) (string, error) {
	return v.body(ctx, id, "text")
}

// HTML returns message id's HTML body rewritten for the preview frame.
// ErrNoBody means it has none. Other errors are those of Content.
func (v *Viewer) HTML(ctx context.Context, id int64) (string, error) {
	return v.body(ctx, id, "html")
}

func (v *Viewer) body(ctx context.Context, id int64, op string) (string, error) {
	var answer bodyAnswer
	err := v.withOriginal(ctx, id, func(original archive.Original) error {
		return v.pool.exchange(ctx, workerRequest{Op: op, Size: original.Size}, original.File, func(reply io.Reader, _ func()) error {
			return ReadFrame(reply, &answer)
		})
	})
	if err != nil {
		return "", err
	}
	if answer.Body == nil {
		return "", ErrNoBody
	}
	return *answer.Body, nil
}

// Attachment decodes the attachment at position in message id and writes its
// bytes to the writer open returns. open runs once the name and size are
// known, before any byte. An error after open means the bytes stopped short.
// ErrNoAttachment means the message lists none there, and open didn't run.
// Other errors are those of Content.
func (v *Viewer) Attachment(ctx context.Context, id int64, position string, open func(AttachmentFile) (io.Writer, error)) error {
	found := false
	err := v.withOriginal(ctx, id, func(original archive.Original) error {
		request := workerRequest{Op: "attachment", Size: original.Size, Position: position}
		return v.pool.exchange(ctx, request, original.File, func(reply io.Reader, streaming func()) error {
			var header attachmentAnswer
			if err := ReadFrame(reply, &header); err != nil {
				return err
			}
			if !header.Found {
				return nil
			}
			found = true
			w, err := open(AttachmentFile{Name: header.Name, Size: header.Size})
			if err != nil {
				return &sinkError{err: err}
			}
			streaming()
			sink := &sinkWriter{writer: w}
			if _, err := io.CopyN(sink, reply, header.Size); err != nil {
				if sink.err != nil {
					return &sinkError{err: sink.err}
				}
				return err
			}
			return nil
		})
	})
	if err == nil && !found {
		return ErrNoAttachment
	}
	return err
}

// withOriginal opens message id's original, checked against its hash, and
// runs read on it. A worker that stops is logged. A stored file that fails
// after its check is recorded as damaged, as a download records it.
func (v *Viewer) withOriginal(ctx context.Context, id int64, read func(archive.Original) error) error {
	original, err := v.archive.OpenVerified(ctx, id)
	if err != nil {
		return err
	}
	defer original.File.Close()
	err = read(original)
	if stopped, ok := errors.AsType[*WorkerStoppedError](err); ok {
		v.logger.Error("processing_worker_stopped", "message_id", id, "exit", stopped.Exit)
	}
	if _, ok := errors.AsType[*sourceError](err); ok {
		v.logger.Error("read a stored original", "message_id", id, "error", err.Error())
		v.archive.RecordDamaged(original)
		return fmt.Errorf("%w: %w", archive.ErrOriginalDamaged, err)
	}
	return err
}
