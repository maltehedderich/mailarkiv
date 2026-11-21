// Package mailparse turns the untrusted bytes of one original into the fields,
// bodies, attachments, and problems the archive shows and indexes. It imports
// no other module, so code that reads hostile input can't reach the catalog or
// the disk. The server runs it in worker processes, never in its own.
package mailparse

import (
	"bytes"
	"errors"
	"strings"

	"github.com/jhillyerd/enmime/v2"
)

// Message is what Read found in one original.
type Message struct {
	// Unreadable is true when the message header couldn't be read. Only
	// Problems is set then.
	Unreadable bool      `json:"unreadable,omitempty"`
	From       []Address `json:"from,omitempty"`
	// Encrypted is true when the top part is multipart/encrypted, or
	// application/pkcs7-mime with smime-type=enveloped-data.
	Encrypted bool `json:"encrypted,omitempty"`
	// FromText is the decoded From header when it isn't an address list.
	FromText *string `json:"from_text,omitempty"`
	Subject  *string `json:"subject,omitempty"`
	// Date is the Date header's decoded text. ListDate reads it.
	Date        *string      `json:"date,omitempty"`
	To          []Address    `json:"to,omitempty"`
	Cc          []Address    `json:"cc,omitempty"`
	Bcc         []Address    `json:"bcc,omitempty"`
	Plain       *Body        `json:"plain,omitempty"`
	HTML        *Body        `json:"html,omitempty"`
	Attachments []Attachment `json:"attachments,omitempty"`
	Problems    []Problem    `json:"problems,omitempty"`
}

// Address is one mailbox. Name is empty when the address has none.
type Address struct {
	Name    string `json:"name,omitempty"`
	Address string `json:"address"`
}

// Body is a displayed body, decoded to UTF-8 and at most MaxBody bytes.
type Body struct {
	Position string `json:"position"`
	Content  string `json:"content"`
	// Truncated is true when the body passed MaxBody.
	Truncated bool `json:"truncated,omitempty"`
}

func newParser() *enmime.Parser {
	return enmime.NewParser(
		enmime.SkipMalformedParts(true),
		// We read text out of HTML ourselves, with the tokenizer the preview
		// uses, so the index and the preview agree.
		enmime.DisableTextConversion(true),
		// Trust a declared character set instead of guessing over it.
		enmime.DisableCharacterDetection(true),
		// scanParts keeps messages below this. It stays as a backstop.
		enmime.MaxMIMEParts(maxParts),
	)
}

// Read parses one original. It never fails: what it can't read becomes a
// problem on the message, and a header it can't read makes the message
// Unreadable.
func Read(original []byte) Message {
	m, _ := parse(original)
	return m
}

// ReadView parses one original for the message view: what Read finds, and
// the HTML body rewritten for the preview, or nil without one.
func ReadView(original []byte) (Message, *Preview) {
	m, parts := parse(original)
	if m.HTML == nil {
		return m, nil
	}
	preview := rewrite(m.HTML.Content, newImagePlacer(parts))
	return m, &preview
}

// AttachmentContent parses one original and returns the attachment Read
// lists at position, with its decoded bytes. A damaged part gives the bytes
// that decoded. ok is false when Read lists no attachment there.
func AttachmentContent(original []byte, position string) (attachment Attachment, content []byte, ok bool) {
	m, parts := parse(original)
	for _, listed := range m.Attachments {
		if listed.Position != position {
			continue
		}
		for _, part := range parts {
			if part.position == position {
				return listed, part.part.Content, true
			}
		}
	}
	return Attachment{}, nil, false
}

// parse reads one original and also returns its leaves, which hold the
// decoded parts.
func parse(original []byte) (Message, []leaf) {
	input, bounds := scanParts(withoutMboxFromLine(original))
	root, err := newParser().ReadParts(bytes.NewReader(input))
	if err != nil {
		kind := MalformedHeader
		if _, ok := errors.AsType[*enmime.TooManyPartsError](err); ok {
			kind = TooManyParts
		}
		return Message{Unreadable: true, Problems: []Problem{{Kind: kind}}}, nil
	}

	var m Message
	var problems problemList
	readFields(&m, root.Header, &problems)
	m.Encrypted = encrypted(root)

	leaves := walkParts(root, &problems)
	plain, html := chooseBodies(leaves)
	if plain != nil {
		m.Plain = body(plain, &problems)
	}
	if html != nil {
		m.HTML = body(html, &problems)
	}
	m.Attachments = attachments(leaves, plain, html, m.HTML)
	for _, bound := range bounds {
		problems.add(bound.Position, bound.DeclaredType, bound.Kind)
	}
	m.Problems = problems.items
	return m, leaves
}

// encrypted reports whether the top part holds an encrypted message.
func encrypted(root *enmime.Part) bool {
	switch strings.ToLower(root.ContentType) {
	case "multipart/encrypted":
		return true
	case "application/pkcs7-mime":
		return strings.EqualFold(headerParameter(root, "Content-Type", "smime-type"), "enveloped-data")
	}
	return false
}

// withoutMboxFromLine skips a first line starting with "From ", which an
// import accepts and isn't a header field.
func withoutMboxFromLine(original []byte) []byte {
	if !bytes.HasPrefix(original, []byte("From ")) {
		return original
	}
	end := bytes.IndexByte(original, '\n')
	if end < 0 {
		return nil
	}
	return original[end+1:]
}

// Text returns the words of the displayed bodies for the index: the plain
// body as it is, and the text of the HTML body.
func (m Message) Text() []string {
	var texts []string
	if m.Plain != nil {
		texts = append(texts, m.Plain.Content)
	}
	if m.HTML != nil {
		texts = append(texts, HTMLText(m.HTML.Content))
	}
	return texts
}
