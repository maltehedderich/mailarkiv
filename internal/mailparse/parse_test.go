package mailparse

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func readFixture(t *testing.T, name string) Message {
	t.Helper()
	original, err := os.ReadFile(filepath.Join("testdata", "plain", name))
	if err != nil {
		t.Fatal(err)
	}
	return Read(original)
}

func text(s string) *string { return &s }

func TestRead(t *testing.T) {
	tests := []struct {
		fixture string
		want    Message
	}{
		{
			fixture: "plain-text.eml",
			want: Message{
				From:    []Address{{Name: "Anna Schmidt", Address: "anna@example.test"}},
				Subject: text("Wochenplan"),
				Date:    text("Mon, 02 Nov 2026 09:14:55 +0100"),
				To:      []Address{{Name: "Team", Address: "team@example.test"}},
				Plain:   &Body{Position: "1", Content: "Hallo Team,\nder Wochenplan steht.\n"},
			},
		},
		{
			fixture: "html-only.eml",
			want: Message{
				From:    []Address{{Name: "Bernd Weber", Address: "bernd@example.test"}},
				Subject: text("Bericht"),
				Date:    text("Tue, 03 Nov 2026 10:00:00 +0000"),
				To:      []Address{{Address: "anna@example.test"}},
				HTML: &Body{Position: "1", Content: "<html><head><title>Kopfzeile</title>" +
					"<style>.versteckt { color: red }</style></head>\n" +
					"<body><p>Der <b>Quartals</b>bericht liegt bei.</p><p>Erster</p><div>Zweiter</div>\n" +
					"<script>Geheim</script></body></html>\n"},
			},
		},
		{
			fixture: "alternative.eml",
			want: Message{
				From:    []Address{{Name: "Carla Braun", Address: "carla@example.test"}},
				Subject: text("Kuchen"),
				Date:    text("Wed, 04 Nov 2026 12:00:00 +0000"),
				To:      []Address{{Address: "anna@example.test"}},
				Plain:   &Body{Position: "1", Content: "Apfelkuchen am Freitag."},
				HTML:    &Body{Position: "2", Content: "<p>Birnenkuchen am Freitag.</p>"},
			},
		},
		{
			fixture: "encoded-subject.eml",
			want: Message{
				From:    []Address{{Name: "Jürgen Müller", Address: "juergen@example.test"}},
				Subject: text("Grüße aus Köln"),
				Date:    text("Thu, 05 Nov 2026 08:30:00 +0100"),
				To:      []Address{{Address: "anna@example.test"}},
				Plain:   &Body{Position: "1", Content: "Schöne Grüße von Familie Müller.\n"},
			},
		},
		{
			fixture: "name-equals-address.eml",
			want: Message{
				From:    []Address{{Name: "ANNA@EXAMPLE.TEST", Address: "anna@example.test"}},
				Subject: text("Name equals address"),
				Date:    text("Fri, 06 Nov 2026 08:00:00 +0000"),
				To:      []Address{{Address: "team@example.test"}},
				Plain:   &Body{Position: "1", Content: "Body.\n"},
			},
		},
		{
			fixture: "three-senders.eml",
			want: Message{
				From: []Address{
					{Name: "Anna Schmidt", Address: "anna@example.test"},
					{Name: "Bob", Address: "bob@example.test"},
					{Address: "carol@example.test"},
				},
				Subject: text("Drei Absender"),
				Date:    text("Fri, 06 Nov 2026 09:00:00 +0000"),
				To:      []Address{{Address: "team@example.test"}},
				Plain:   &Body{Position: "1", Content: "Body.\n"},
			},
		},
		{
			fixture: "no-from.eml",
			want: Message{
				Subject: text("Ohne Absender"),
				Date:    text("Fri, 06 Nov 2026 10:00:00 +0000"),
				To:      []Address{{Address: "team@example.test"}},
				Plain:   &Body{Position: "1", Content: "Body.\n"},
			},
		},
		{
			fixture: "no-subject.eml",
			want: Message{
				From:  []Address{{Address: "anna@example.test"}},
				Date:  text("Fri, 06 Nov 2026 11:00:00 +0000"),
				To:    []Address{{Address: "team@example.test"}},
				Plain: &Body{Position: "1", Content: "Body.\n"},
			},
		},
		{
			fixture: "no-date.eml",
			want: Message{
				From:    []Address{{Address: "anna@example.test"}},
				Subject: text("Ohne Datum"),
				To:      []Address{{Address: "team@example.test"}},
				Plain:   &Body{Position: "1", Content: "Body.\n"},
			},
		},
		{
			fixture: "unreadable-date.eml",
			want: Message{
				From:    []Address{{Address: "anna@example.test"}},
				Subject: text("Unlesbares Datum"),
				Date:    text("sometime next week"),
				To:      []Address{{Address: "team@example.test"}},
				Plain:   &Body{Position: "1", Content: "Body.\n"},
			},
		},
		{
			fixture: "attachment.eml",
			want: Message{
				From:    []Address{{Name: "Dora Klein", Address: "dora@example.test"}},
				Subject: text("Vertrag"),
				Date:    text("Sat, 07 Nov 2026 14:00:00 +0000"),
				To:      []Address{{Address: "anna@example.test"}},
				Plain:   &Body{Position: "1", Content: "Anbei der Vertrag."},
				// The disposition's filename wins over the type's name.
				Attachments: []Attachment{{
					Position: "2", Name: text("Vertrag_2026.pdf"), DeclaredType: "application/pdf",
					Size: 46, Kind: KindFile,
				}},
			},
		},
		{
			fixture: "signature-logo.eml",
			want: Message{
				From:    []Address{{Name: "Emil Roth", Address: "emil@example.test"}},
				Subject: text("Mit Logo"),
				Date:    text("Sat, 07 Nov 2026 15:00:00 +0000"),
				To:      []Address{{Address: "anna@example.test"}},
				Plain:   &Body{Position: "1.1", Content: "Gruss, Emil"},
				HTML: &Body{Position: "1.2",
					Content: `<p>Gruss, Emil</p><img src="cid:logo@example.test" alt="Logo">`},
				Attachments: []Attachment{{
					Position: "2", Name: text("logo.png"), DeclaredType: "image/png",
					Size: 22, Kind: KindEmbeddedImage,
				}},
			},
		},
		{
			fixture: "attached-message.eml",
			want: Message{
				From:    []Address{{Name: "Frieda Lang", Address: "frieda@example.test"}},
				Subject: text("Weitergeleitet"),
				Date:    text("Sun, 08 Nov 2026 09:00:00 +0000"),
				To:      []Address{{Address: "anna@example.test"}},
				Plain:   &Body{Position: "1", Content: "Siehe unten."},
				Attachments: []Attachment{{
					Position: "2", DeclaredType: "message/rfc822", Size: 179, Kind: KindMessage,
				}},
			},
		},
		{
			fixture: "recipients.eml",
			want: Message{
				From:    []Address{{Address: "anna@example.test"}},
				Subject: text("Empfaenger"),
				Date:    text("Sun, 08 Nov 2026 10:00:00 +0000"),
				To: []Address{
					{Name: "Team", Address: "team@example.test"},
					{Address: "bob@example.test"},
				},
				Cc:    []Address{{Name: "Carla Braun", Address: "carla@example.test"}},
				Bcc:   []Address{{Address: "geheim@example.test"}},
				Plain: &Body{Position: "1", Content: "Body.\n"},
			},
		},
		{
			fixture: "encoded-filename.eml",
			want: Message{
				From:    []Address{{Address: "anna@example.test"}},
				Subject: text("Encoded file names"),
				Date:    text("Sun, 08 Nov 2026 14:00:00 +0000"),
				To:      []Address{{Address: "team@example.test"}},
				Plain:   &Body{Position: "1", Content: "Zwei Dateien."},
				Attachments: []Attachment{
					{Position: "2", Name: text("Grüße.txt"), DeclaredType: "text/plain", Size: 5, Kind: KindFile},
					{Position: "3", Name: text("Köln.bin"), DeclaredType: "application/octet-stream", Size: 3, Kind: KindFile},
				},
			},
		},
		{
			fixture: "mueller-upper.eml",
			want: Message{
				From:    []Address{{Address: "anna@example.test"}},
				Subject: text("Brief zwei"),
				Date:    text("Sun, 08 Nov 2026 12:00:00 +0000"),
				To:      []Address{{Address: "team@example.test"}},
				Plain:   &Body{Position: "1", Content: "HERR MÜLLER KOMMT MORGEN.\n"},
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.fixture, func(t *testing.T) {
			got := readFixture(t, tt.fixture)
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("Read gave\n%s\nwant\n%s", describe(got), describe(tt.want))
			}
		})
	}
}

func TestPartNumbers(t *testing.T) {
	nested := strings.Join([]string{
		"From: anna@example.test",
		`Content-Type: multipart/mixed; boundary="outer"`,
		"",
		"--outer",
		"Content-Type: text/plain",
		"",
		"Eins",
		"--outer",
		`Content-Type: multipart/mixed; boundary="inner"`,
		"",
		"--inner",
		"Content-Type: application/octet-stream",
		"",
		"a",
		"--inner",
		"Content-Type: application/zip",
		"",
		"bb",
		"--inner--",
		"--outer",
		"Content-Type: application/pdf",
		"",
		"ccc",
		"--outer--",
		"",
	}, "\r\n")
	got := Read([]byte(nested))
	var positions []string
	for _, attachment := range got.Attachments {
		positions = append(positions, attachment.Position)
	}
	if want := []string{"2.1", "2.2", "3"}; !reflect.DeepEqual(positions, want) {
		t.Errorf("attachment positions %q, want %q", positions, want)
	}
	if got.Plain == nil || got.Plain.Position != "1" {
		t.Errorf("plain body %+v, want part 1", got.Plain)
	}
}

func TestText(t *testing.T) {
	tests := []struct {
		fixture string
		want    []string
	}{
		{"plain-text.eml", []string{"Hallo Team,\nder Wochenplan steht.\n"}},
		// Words come from the HTML's text only: not from script, style, or
		// head, and block elements keep words apart.
		{"html-only.eml", []string{"Der Quartalsbericht liegt bei. Erster Zweiter"}},
		{"alternative.eml", []string{"Apfelkuchen am Freitag.", "Birnenkuchen am Freitag."}},
		{"attachment.eml", []string{"Anbei der Vertrag."}},
	}
	for _, tt := range tests {
		t.Run(tt.fixture, func(t *testing.T) {
			if got := readFixture(t, tt.fixture).Text(); !reflect.DeepEqual(got, tt.want) {
				t.Errorf("Text() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestHTMLText(t *testing.T) {
	tests := []struct {
		name, html, want string
	}{
		{"entities", "<p>M&uuml;ller &amp; S&ouml;hne</p>", "Müller & Söhne"},
		{"line break", "eins<br>zwei", "eins zwei"},
		{"table cells", "<table><tr><td>links</td><td>rechts</td></tr></table>", "links rechts"},
		{"list items", "<ul><li>a</li><li>b</li></ul>", "a b"},
		{"inline markup", "<span>Quartals</span><i>bericht</i>", "Quartalsbericht"},
		{"style in body", "<style>p{}</style>sichtbar", "sichtbar"},
		{"comment", "vor<!-- versteckt -->her", "vorher"},
		{"whitespace", "  a \n\t b  ", "a b"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := HTMLText(tt.html); got != tt.want {
				t.Errorf("HTMLText(%q) = %q, want %q", tt.html, got, tt.want)
			}
		})
	}
}

func TestSummary(t *testing.T) {
	t.Run("fixtures", func(t *testing.T) {
		tests := []struct {
			fixture string
			want    Summary
		}{
			{"plain-text.eml", Summary{
				SenderName: text("Anna Schmidt"), SenderAddress: text("anna@example.test"),
				Subject: text("Wochenplan"), DateHeader: text("Mon, 02 Nov 2026 09:14:55 +0100"),
			}},
			// A name that only repeats the address isn't shown.
			{"name-equals-address.eml", Summary{
				SenderAddress: text("anna@example.test"),
				Subject:       text("Name equals address"), DateHeader: text("Fri, 06 Nov 2026 08:00:00 +0000"),
			}},
			{"three-senders.eml", Summary{
				SenderName: text("Anna Schmidt"), SenderAddress: text("anna@example.test"), MoreSenders: 2,
				Subject: text("Drei Absender"), DateHeader: text("Fri, 06 Nov 2026 09:00:00 +0000"),
			}},
			{"no-from.eml", Summary{
				Subject: text("Ohne Absender"), DateHeader: text("Fri, 06 Nov 2026 10:00:00 +0000"),
			}},
			{"attachment.eml", Summary{
				SenderName: text("Dora Klein"), SenderAddress: text("dora@example.test"),
				Subject: text("Vertrag"), DateHeader: text("Sat, 07 Nov 2026 14:00:00 +0000"),
				HasAttachments: true,
			}},
			// An embedded image alone isn't an attachment for the row.
			{"signature-logo.eml", Summary{
				SenderName: text("Emil Roth"), SenderAddress: text("emil@example.test"),
				Subject: text("Mit Logo"), DateHeader: text("Sat, 07 Nov 2026 15:00:00 +0000"),
			}},
			{"attached-message.eml", Summary{
				SenderName: text("Frieda Lang"), SenderAddress: text("frieda@example.test"),
				Subject: text("Weitergeleitet"), DateHeader: text("Sun, 08 Nov 2026 09:00:00 +0000"),
				HasAttachments: true,
			}},
		}
		for _, tt := range tests {
			t.Run(tt.fixture, func(t *testing.T) {
				got := readFixture(t, tt.fixture).Summary()
				if !reflect.DeepEqual(got, tt.want) {
					t.Errorf("Summary() = %s, want %s", describe(got), describe(tt.want))
				}
			})
		}
	})

	t.Run("cut on a character boundary", func(t *testing.T) {
		message := Message{
			From:    []Address{{Name: strings.Repeat("ä", 501), Address: strings.Repeat("ö", 316) + "@a.de"}},
			Subject: text(strings.Repeat("€", 1001)),
			Date:    text(strings.Repeat("ü", 201)),
		}
		want := Summary{
			SenderName:    text(strings.Repeat("ä", 500)),
			SenderAddress: text(strings.Repeat("ö", 316) + "@a.d"),
			Subject:       text(strings.Repeat("€", 1000)),
			DateHeader:    text(strings.Repeat("ü", 200)),
		}
		if got := message.Summary(); !reflect.DeepEqual(got, want) {
			t.Errorf("Summary() = %s, want %s", describe(got), describe(want))
		}
	})

	t.Run("values at the limits stay whole", func(t *testing.T) {
		name, subject := strings.Repeat("ä", 500), strings.Repeat("€", 1000)
		message := Message{From: []Address{{Name: name, Address: "a@example.test"}}, Subject: &subject}
		got := message.Summary()
		if got.SenderName == nil || *got.SenderName != name || got.Subject == nil || *got.Subject != subject {
			t.Errorf("Summary() = %s, want the name and subject unchanged", describe(got))
		}
	})

	t.Run("name compared before the cut", func(t *testing.T) {
		address := strings.Repeat("a", 330) + "@example.test"
		message := Message{From: []Address{{Name: strings.ToUpper(address), Address: address}}}
		if got := message.Summary(); got.SenderName != nil {
			t.Errorf("SenderName = %q, want nil for a name equal to the address", *got.SenderName)
		}
	})
}

// Imports accept a file that starts with an mbox From line, so Read skips it.
func TestReadSkipsMboxFromLine(t *testing.T) {
	got := Read([]byte("From MAILER-DAEMON Mon Nov  2 2026\nFrom: anna@example.test\nSubject: Mbox\n\nBody\n"))
	want := Message{
		From:    []Address{{Address: "anna@example.test"}},
		Subject: text("Mbox"),
		Plain:   &Body{Position: "1", Content: "Body\n"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("Read gave %s, want %s", describe(got), describe(want))
	}
}

func TestEncrypted(t *testing.T) {
	tests := []struct {
		name, header, body string
		want               bool
	}{
		{
			name:   "OpenPGP",
			header: `Content-Type: multipart/encrypted; protocol="application/pgp-encrypted"; boundary="enc"`,
			body: "--enc\r\nContent-Type: application/pgp-encrypted\r\n\r\nVersion: 1\r\n" +
				"--enc\r\nContent-Type: application/octet-stream; name=\"encrypted.asc\"\r\n\r\n" +
				"-----BEGIN PGP MESSAGE-----\r\nc3ludGhldGlj\r\n-----END PGP MESSAGE-----\r\n--enc--\r\n",
			want: true,
		},
		{
			name:   "S/MIME enveloped",
			header: "Content-Type: application/pkcs7-mime; smime-type=enveloped-data; name=\"smime.p7m\"\r\nContent-Transfer-Encoding: base64",
			body:   "c3ludGhldGlj\r\n",
			want:   true,
		},
		{
			name:   "S/MIME enveloped, parameter in capitals",
			header: "Content-Type: Application/PKCS7-MIME; Smime-Type=\"Enveloped-Data\"\r\nContent-Transfer-Encoding: base64",
			body:   "c3ludGhldGlj\r\n",
			want:   true,
		},
		{
			name:   "S/MIME signed data",
			header: "Content-Type: application/pkcs7-mime; smime-type=signed-data; name=\"smime.p7m\"\r\nContent-Transfer-Encoding: base64",
			body:   "c3ludGhldGlj\r\n",
			want:   false,
		},
		{
			name:   "plain text",
			header: "Content-Type: text/plain; charset=utf-8",
			body:   "Hallo\r\n",
			want:   false,
		},
		{
			// Only the top part counts.
			name:   "an encrypted part inside",
			header: `Content-Type: multipart/mixed; boundary="mix"`,
			body: "--mix\r\nContent-Type: text/plain\r\n\r\nAnbei\r\n" +
				"--mix\r\nContent-Type: multipart/encrypted; protocol=\"application/pgp-encrypted\"; boundary=\"enc\"\r\n\r\n" +
				"--enc\r\nContent-Type: application/pgp-encrypted\r\n\r\nVersion: 1\r\n" +
				"--enc\r\nContent-Type: application/octet-stream\r\n\r\nx\r\n--enc--\r\n--mix--\r\n",
			want: false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			original := "From: anna@example.test\r\nSubject: Geheim\r\nMIME-Version: 1.0\r\n" + tt.header + "\r\n\r\n" + tt.body
			if got := Read([]byte(original)).Encrypted; got != tt.want {
				t.Errorf("Encrypted = %t, want %t", got, tt.want)
			}
		})
	}
}

// Signatures, encrypted parts, and the parts of an encrypted message are
// listed like any other attachment.
func TestSignedAndEncryptedParts(t *testing.T) {
	tests := []struct {
		name, original string
		want           []Attachment
	}{
		{
			name: "OpenPGP signature",
			original: "From: anna@example.test\r\nMIME-Version: 1.0\r\n" +
				"Content-Type: multipart/signed; micalg=pgp-sha256; protocol=\"application/pgp-signature\"; boundary=\"sig\"\r\n\r\n" +
				"--sig\r\nContent-Type: text/plain; charset=utf-8\r\n\r\nUnterschrieben.\r\n" +
				"--sig\r\nContent-Type: application/pgp-signature; name=\"signature.asc\"\r\n\r\n" +
				"-----BEGIN PGP SIGNATURE-----\r\nc3ludGhldGlj\r\n-----END PGP SIGNATURE-----\r\n--sig--\r\n",
			want: []Attachment{{Position: "2", Name: text("signature.asc"), DeclaredType: "application/pgp-signature", Size: 72, Kind: KindFile}},
		},
		{
			name: "S/MIME signature",
			original: "From: anna@example.test\r\nMIME-Version: 1.0\r\n" +
				"Content-Type: multipart/signed; protocol=\"application/pkcs7-signature\"; micalg=sha-256; boundary=\"sig\"\r\n\r\n" +
				"--sig\r\nContent-Type: text/plain; charset=utf-8\r\n\r\nUnterschrieben.\r\n" +
				"--sig\r\nContent-Type: application/pkcs7-signature; name=\"smime.p7s\"\r\n" +
				"Content-Disposition: attachment; filename=\"smime.p7s\"\r\nContent-Transfer-Encoding: base64\r\n\r\n" +
				"c3ludGhldGlj\r\n--sig--\r\n",
			want: []Attachment{{Position: "2", Name: text("smime.p7s"), DeclaredType: "application/pkcs7-signature", Size: 9, Kind: KindFile}},
		},
		{
			name: "OpenPGP encrypted",
			original: "From: anna@example.test\r\nMIME-Version: 1.0\r\n" +
				"Content-Type: multipart/encrypted; protocol=\"application/pgp-encrypted\"; boundary=\"enc\"\r\n\r\n" +
				"--enc\r\nContent-Type: application/pgp-encrypted\r\n\r\nVersion: 1\r\n" +
				"--enc\r\nContent-Type: application/octet-stream; name=\"encrypted.asc\"\r\n\r\nc3ludGhldGlj\r\n--enc--\r\n",
			want: []Attachment{
				{Position: "1", DeclaredType: "application/pgp-encrypted", Size: 10, Kind: KindFile},
				{Position: "2", Name: text("encrypted.asc"), DeclaredType: "application/octet-stream", Size: 12, Kind: KindFile},
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := Read([]byte(tt.original)).Attachments
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("attachments\n%s\nwant\n%s", describe(got), describe(tt.want))
			}
		})
	}
}

func TestBodyTruncated(t *testing.T) {
	long := strings.Repeat("a", MaxBody+1)
	original := "From: anna@example.test\r\nMIME-Version: 1.0\r\nContent-Type: multipart/alternative; boundary=\"alt\"\r\n\r\n" +
		"--alt\r\nContent-Type: text/plain\r\n\r\n" + long + "\r\n" +
		"--alt\r\nContent-Type: text/html\r\n\r\n<p>kurz</p>\r\n--alt--\r\n"
	got := Read([]byte(original))
	if got.Plain == nil || !got.Plain.Truncated || len(got.Plain.Content) != MaxBody {
		t.Errorf("plain body truncated %v, want true at %d bytes", got.Plain != nil && got.Plain.Truncated, MaxBody)
	}
	if got.HTML == nil || got.HTML.Truncated {
		t.Errorf("HTML body %s, want it whole", describe(got.HTML))
	}
}
