package mailparse

import (
	"mime"
	"net/mail"
	"net/textproto"
	"strings"

	"golang.org/x/net/html/charset"
)

var wordDecoder = &mime.WordDecoder{CharsetReader: charset.NewReaderLabel}

// readFields reads the header fields the archive shows and indexes.
func readFields(m *Message, header textproto.MIMEHeader, problems *problemList) {
	if from, ok := firstValue(header, "From"); ok && strings.TrimSpace(from) != "" {
		addresses, err := parseAddresses(from)
		if err != nil || len(addresses) == 0 {
			text := decodeHeader(from)
			m.FromText = &text
			problems.add("", "", MalformedHeader)
		} else {
			m.From = addresses
		}
	}
	if subject, ok := firstValue(header, "Subject"); ok {
		decoded := decodeHeader(subject)
		m.Subject = &decoded
	}
	if date, ok := firstValue(header, "Date"); ok {
		decoded := decodeHeader(date)
		m.Date = &decoded
	}
	m.To = recipients(header, "To")
	m.Cc = recipients(header, "Cc")
	m.Bcc = recipients(header, "Bcc")
}

// firstValue is the first instance of a header field.
func firstValue(header textproto.MIMEHeader, name string) (string, bool) {
	values := header[textproto.CanonicalMIMEHeaderKey(name)]
	if len(values) == 0 {
		return "", false
	}
	return values[0], true
}

// recipients reads an address field. When it isn't an address list, its
// decoded text stays as a name, so its words can still be searched.
func recipients(header textproto.MIMEHeader, name string) []Address {
	// Some mail repeats an address field, so every instance counts.
	value := strings.Join(header[textproto.CanonicalMIMEHeaderKey(name)], ", ")
	if strings.TrimSpace(value) == "" {
		return nil
	}
	addresses, err := parseAddresses(value)
	if err != nil {
		return []Address{{Name: decodeHeader(value)}}
	}
	return addresses
}

func parseAddresses(value string) ([]Address, error) {
	parser := mail.AddressParser{WordDecoder: wordDecoder}
	list, err := parser.ParseList(value)
	if err != nil {
		return nil, err
	}
	var addresses []Address
	for _, address := range list {
		addresses = append(addresses, Address{Name: validUTF8(address.Name), Address: validUTF8(address.Address)})
	}
	return addresses, nil
}

// decodeHeader decodes RFC 2047 encoded words. A value it can't decode stays
// as written, with invalid UTF-8 replaced.
func decodeHeader(value string) string {
	decoded, err := wordDecoder.DecodeHeader(value)
	if err != nil {
		decoded = value
	}
	return validUTF8(decoded)
}
