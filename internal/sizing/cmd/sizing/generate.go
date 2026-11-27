package main

import (
	"bytes"
	"encoding/base64"
	"fmt"
	"html"
	"math"
	"math/rand/v2"
	"mime"
	"net/mail"
	"slices"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
)

// Common words of each language, most frequent first. Each message is in one
// language, and draws from its list and then from the invented words.
var (
	germanWords = strings.Fields(`der die und in den von zu das mit sich des auf für ist im dem nicht
		ein eine als auch es an werden aus er hat dass sie nach wird bei einer um am sind noch wie
		einem über einen so zum war haben nur oder aber vor zur bis mehr durch man sein wurde sei
		hier können wir ich ihr uns bitte danke gerne heute morgen woche termin angebot rechnung
		bericht projekt kunde auftrag lieferung frage antwort anfrage zahlung vertrag unterlagen
		besprechung team grüße freundlichen viele liebe hallo sehr geehrte damen herren anbei
		gesendet erhalten prüfen melden rückmeldung übersicht stand nächste letzte schon wieder
		dann jetzt immer kurz gut neue neuen alle allen etwas ganz natürlich leider gleich später
		möchte könnten würde sollten müssen gemacht geschickt besprochen abgestimmt freigabe
		änderung änderungen größe straße büro geschäftsführung mitarbeiter abteilung quartal monat
		jahr zeit tag datum preis kosten budget planung entwurf version dokument datei liste tabelle
		präsentation protokoll`)
	englishWords = strings.Fields(`the of and to a in is it you that he was for on are with as i his
		they be at one have this from or had by but some what there we can out other were all your
		when up use word how said an each she which do their time if will way about many then them
		would write like so these her long make thing see him two has look more day could go come
		did my no most who over know than call first people may down side been now find please
		thanks regards best meeting report invoice project customer order delivery question answer
		request payment contract documents team hello dear attached sent received review update
		schedule tomorrow today week next last already again just always short good new everyone
		something quite course unfortunately later should must done shared discussed agreed
		approval change changes office management staff department quarter month year date price
		cost budget planning draft version document file list spreadsheet presentation minutes`)
	firstNames = strings.Fields(`Anna Jürgen Sabine Thomas Michael Julia Peter Monika Stefan Katrin
		Lukas Sophie Jan Laura Felix Lena Tobias Miriam Björn Jörg Günter Ute Dörte Emma James
		Olivia Oliver Emily Henry Grace Daniel Sarah David Chloe Mark Lucy Paul Hannah`)
	lastNames = strings.Fields(`Müller Schmidt Schneider Fischer Weber Meyer Wagner Becker Schulz
		Hoffmann Schäfer Koch Bauer Richter Klein Wolf Schröder Neumann Schwarz Zimmermann Jäger
		Krüger Böhm Köhler Smith Johnson Brown Taylor Wilson Davies Evans Roberts Walker Wright
		Hughes Green Hall Wood Clarke`)
	// Invented words join two to four of these, so the vocabulary has as
	// many distinct terms as a real archive's long tail.
	syllables = strings.Fields(`an be ber bin da de der di do ein el en er fa fe fer ge gen ha hal
		he hei in ka kal ke kon ku la lan le li lo ma mar me mi mo na ne ni no on pa pe per pla pro
		ra re ri ro sa sche se sen si so sta ste stra ta te ter ti to tra tri um un ver wa we wer zei
		zu bur dor fin gor hel kir lun mor nor pol quin rus sel tor ul vin wol xan yor zel`)
	attachmentWords = []string{"Rechnung", "Angebot", "Vertrag", "Bericht", "Protokoll",
		"Lieferschein", "Präsentation", "Planung", "Invoice", "Report", "Statement", "Quote",
		"Minutes", "Contract", "Summary", "Budget"}
	attachmentTypes = []struct{ extension, contentType string }{
		{"pdf", "application/pdf"},
		{"xlsx", "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet"},
		{"docx", "application/vnd.openxmlformats-officedocument.wordprocessingml.document"},
		{"zip", "application/zip"},
		{"png", "image/png"},
	}
)

const (
	inventedWords = 300_000
	domainCount   = 2_000
	peopleCount   = 20_000
	// vocabularySeed is fixed, so the words, people, and domains that
	// measure searches for don't depend on the seed that chose the messages.
	vocabularySeed = 0x6d61696c61726b69

	// wordZipf shapes word frequencies: the most common word is in nearly
	// every message, and an invented word near the end of the list in about
	// 1 in 10,000.
	wordZipf = 1.1
	// senderZipf and senderOffset give the most frequent sender about 2% of
	// all mail.
	senderZipf   = 1.1
	senderOffset = 10

	// Body lengths follow a log-normal curve around this median, which
	// brings the average message to about 8 KB.
	medianBodyWords = 640
	bodyWordsSigma  = 0.6
	maxBodyWords    = 20_000
	lineWidth       = 76
)

// Dates spread evenly over the four years before this.
var datesEnd = time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC)

const datesSpan = 4 * 365 * 24 * time.Hour

// vocabulary is what every generated message draws from. It is the same for
// every seed.
type vocabulary struct {
	invented []string
	domains  []string
	people   []mail.Address
}

func newVocabulary() *vocabulary {
	r := rand.New(rand.NewPCG(vocabularySeed, 0))
	seen := make(map[string]bool, inventedWords)
	for _, word := range slices.Concat(germanWords, englishWords, syllables) {
		seen[word] = true
	}
	v := &vocabulary{invented: make([]string, 0, inventedWords)}
	for len(v.invented) < inventedWords {
		var word strings.Builder
		for range 2 + r.IntN(3) {
			word.WriteString(syllables[r.IntN(len(syllables))])
		}
		if !seen[word.String()] {
			seen[word.String()] = true
			v.invented = append(v.invented, word.String())
		}
	}
	// A prime step from the middle of the list gives distinct, mid-frequency
	// words for domain names.
	for d := range domainCount {
		v.domains = append(v.domains, v.invented[(50_000+d*7919)%inventedWords]+".example.test")
	}
	for range peopleCount {
		first := firstNames[r.IntN(len(firstNames))]
		last := lastNames[r.IntN(len(lastNames))]
		domain := v.domains[r.IntN(len(v.domains))]
		v.people = append(v.people, mail.Address{
			Name:    first + " " + last,
			Address: asciiLower(first) + "." + asciiLower(last) + "@" + domain,
		})
	}
	return v
}

// asciiLower spells umlauts as German addresses do.
func asciiLower(s string) string {
	return strings.NewReplacer("ä", "ae", "ö", "oe", "ü", "ue", "ß", "ss").Replace(strings.ToLower(s))
}

// token is a word that only message i holds, as a ticket number would be.
// The fixed width keeps one token from being a prefix of another.
func token(i int) string { return fmt.Sprintf("ref%07d", i) }

// generator makes message i the same way every time for the same seed.
type generator struct {
	seed  uint64
	vocab *vocabulary
}

func newGenerator(seed uint64, vocab *vocabulary) *generator {
	return &generator{seed: seed, vocab: vocab}
}

// language is one message's word source.
type language struct {
	words    []string
	greeting string
	closing  string
	topic    string
}

var (
	german  = language{words: germanWords, greeting: "Hallo", closing: "Viele Grüße", topic: "Vorgang"}
	english = language{words: englishWords, greeting: "Hi", closing: "Best regards", topic: "Reference"}
)

// draft holds one message's random choices.
type draft struct {
	r     *rand.Rand
	lang  language
	vocab *vocabulary
	words *rand.Zipf
}

// word returns a word by frequency rank: the language's own words first,
// then the invented ones.
func (d *draft) word() string {
	rank := int(d.words.Uint64())
	if rank < len(d.lang.words) {
		return d.lang.words[rank]
	}
	return d.vocab.invented[rank-len(d.lang.words)]
}

func (d *draft) sentence(n int) string {
	words := make([]string, n)
	for i := range words {
		words[i] = d.word()
	}
	words[0] = capitalize(words[0])
	return strings.Join(words, " ") + "."
}

func capitalize(s string) string {
	r, size := utf8.DecodeRuneInString(s)
	return string(unicode.ToUpper(r)) + s[size:]
}

// paragraphs returns the body's paragraphs, about n words in all.
func (d *draft) paragraphs(n int) []string {
	var paragraphs []string
	for n > 0 {
		var sentences []string
		for range 3 + d.r.IntN(4) {
			words := min(6+d.r.IntN(11), max(n, 1))
			sentences = append(sentences, d.sentence(words))
			n -= words
			if n <= 0 {
				break
			}
		}
		paragraphs = append(paragraphs, strings.Join(sentences, " "))
	}
	return paragraphs
}

// message returns message i as the bytes of an .eml file.
func (g *generator) message(i int) []byte {
	r := rand.New(rand.NewPCG(g.seed, uint64(i)))
	d := &draft{r: r, lang: german, vocab: g.vocab}
	if r.IntN(2) == 1 {
		d.lang = english
	}
	d.words = rand.NewZipf(r, wordZipf, 1, uint64(len(d.lang.words)+len(g.vocab.invented)-1))
	senders := rand.NewZipf(r, senderZipf, senderOffset, uint64(len(g.vocab.people)-1))

	sender := g.vocab.people[senders.Uint64()]
	recipients := g.people(r, 1+r.IntN(3))
	var copies []mail.Address
	if r.IntN(10) < 3 {
		copies = g.people(r, 1+r.IntN(2))
	}
	subject := d.sentence(3 + r.IntN(7))
	subject = subject[:len(subject)-1]
	switch r.IntN(10) {
	case 0, 1:
		subject = "Re: " + subject
	case 2:
		subject = "Fwd: " + subject
	}
	date := datesEnd.Add(-time.Duration(r.Int64N(int64(datesSpan))))
	zone := time.FixedZone("", []int{3600, 7200, 0, -5 * 3600}[r.IntN(4)])

	bodyWords := int(math.Exp(math.Log(medianBodyWords) + bodyWordsSigma*r.NormFloat64()))
	bodyWords = min(max(bodyWords, 20), maxBodyWords)
	paragraphs := slices.Concat(
		[]string{fmt.Sprintf("%s %s,", d.lang.greeting, strings.Fields(recipients[0].Name)[0]),
			fmt.Sprintf("%s %s.", d.lang.topic, token(i))},
		d.paragraphs(bodyWords),
		[]string{d.lang.closing + ",", sender.Name})
	withHTML := r.IntN(10) < 3
	withAttachment := r.IntN(10) == 0

	var b bytes.Buffer
	header := func(name, value string) { fmt.Fprintf(&b, "%s: %s\r\n", name, value) }
	header("From", sender.String())
	header("To", addressList(recipients))
	if copies != nil {
		header("Cc", addressList(copies))
	}
	header("Subject", mime.QEncoding.Encode("utf-8", subject))
	header("Date", date.In(zone).Format(time.RFC1123Z))
	header("Message-ID", fmt.Sprintf("<sizing.%d.%d@example.test>", g.seed, i))
	header("MIME-Version", "1.0")

	mixed := fmt.Sprintf("=_mixed_%d", i)
	if withAttachment {
		header("Content-Type", mime.FormatMediaType("multipart/mixed", map[string]string{"boundary": mixed}))
		b.WriteString("\r\n")
		fmt.Fprintf(&b, "--%s\r\n", mixed)
	}
	if withHTML {
		alternative := fmt.Sprintf("=_alternative_%d", i)
		header("Content-Type", mime.FormatMediaType("multipart/alternative", map[string]string{"boundary": alternative}))
		b.WriteString("\r\n")
		fmt.Fprintf(&b, "--%s\r\n", alternative)
		writeText(&b, paragraphs)
		fmt.Fprintf(&b, "\r\n--%s\r\n", alternative)
		writeHTML(&b, paragraphs)
		fmt.Fprintf(&b, "\r\n--%s--\r\n", alternative)
	} else {
		writeText(&b, paragraphs)
	}
	if withAttachment {
		fmt.Fprintf(&b, "\r\n--%s\r\n", mixed)
		writeAttachment(&b, r)
		fmt.Fprintf(&b, "\r\n--%s--\r\n", mixed)
	}
	return b.Bytes()
}

func (g *generator) people(r *rand.Rand, n int) []mail.Address {
	list := make([]mail.Address, n)
	for i := range list {
		list[i] = g.vocab.people[r.IntN(len(g.vocab.people))]
	}
	return list
}

func addressList(list []mail.Address) string {
	formatted := make([]string, len(list))
	for i, address := range list {
		formatted[i] = address.String()
	}
	return strings.Join(formatted, ", ")
}

// writeText writes a text/plain part's header and its body, wrapped.
func writeText(b *bytes.Buffer, paragraphs []string) {
	b.WriteString("Content-Type: text/plain; charset=utf-8\r\nContent-Transfer-Encoding: 8bit\r\n\r\n")
	for i, paragraph := range paragraphs {
		if i > 0 {
			b.WriteString("\r\n")
		}
		for _, line := range wrap(paragraph) {
			b.WriteString(line)
			b.WriteString("\r\n")
		}
	}
}

func writeHTML(b *bytes.Buffer, paragraphs []string) {
	b.WriteString("Content-Type: text/html; charset=utf-8\r\nContent-Transfer-Encoding: 8bit\r\n\r\n")
	b.WriteString("<html><head><style>body{font-family:sans-serif}p{margin:0 0 1em}</style></head><body>\r\n")
	for _, paragraph := range paragraphs {
		b.WriteString("<p>")
		for _, line := range wrap(html.EscapeString(paragraph)) {
			b.WriteString(line)
			b.WriteString("\r\n")
		}
		b.WriteString("</p>\r\n")
	}
	b.WriteString(`<p><a href="https://www.example.test/">www.example.test</a></p></body></html>` + "\r\n")
}

// writeAttachment writes a small file of random bytes with a name such as
// Rechnung_2025-0412.pdf.
func writeAttachment(b *bytes.Buffer, r *rand.Rand) {
	kind := attachmentTypes[r.IntN(len(attachmentTypes))]
	name := fmt.Sprintf("%s_%d-%04d.%s", attachmentWords[r.IntN(len(attachmentWords))],
		2022+r.IntN(5), r.IntN(10_000), kind.extension)
	content := make([]byte, 2048+r.IntN(8192))
	for i := range content {
		content[i] = byte(r.Uint32())
	}
	fmt.Fprintf(b, "Content-Type: %s\r\n", kind.contentType)
	fmt.Fprintf(b, "Content-Disposition: %s\r\n", mime.FormatMediaType("attachment", map[string]string{"filename": name}))
	b.WriteString("Content-Transfer-Encoding: base64\r\n\r\n")
	encoded := base64.StdEncoding.EncodeToString(content)
	for len(encoded) > lineWidth {
		b.WriteString(encoded[:lineWidth])
		b.WriteString("\r\n")
		encoded = encoded[lineWidth:]
	}
	b.WriteString(encoded)
	b.WriteString("\r\n")
}

// wrap breaks text at spaces into lines of at most lineWidth bytes, or one
// longer word.
func wrap(text string) []string {
	var lines []string
	line := ""
	for _, word := range strings.Fields(text) {
		switch {
		case line == "":
			line = word
		case len(line)+1+len(word) <= lineWidth:
			line += " " + word
		default:
			lines = append(lines, line)
			line = word
		}
	}
	if line != "" {
		lines = append(lines, line)
	}
	return lines
}
