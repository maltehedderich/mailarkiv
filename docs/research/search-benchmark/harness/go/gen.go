package main

// Deterministic synthetic mail. No real mail is read or written.

import (
	"bufio"
	"fmt"
	"math"
	"math/rand/v2"
	"os"
	"sort"
	"strings"
)

const (
	vocabSize  = 400_000
	dateStart  = 1667260800 // 2022-11-01
	dateSpan   = 4 * 365 * 86400
	numPhrases = 20
)

var syll = strings.Fields(`an be ber bei ch da de der die ein en er es ge hen in ist ka ko kon la le lich lo ma me men mit na ne nung pa per pro ra re rech ri ro sch se so ste ta te ten ter tion tra un ung ver vor wa we wei zu zen ment que eau oui par pour les des tre mar sta gra fin ges auf aus bau bur cal dor fel gen hof jan kur lan mon nor ort pol rat sal tor ull vol win yan zug äu ör üb ée ès`)

func buildVocab() []string {
	r := rand.New(rand.NewPCG(42, 1))
	seen := make(map[string]struct{}, vocabSize)
	out := make([]string, 0, vocabSize)
	for len(out) < vocabSize {
		// Frequent words are short, as in natural language.
		n := 2
		switch {
		case len(out) > 300_000:
			n = 5
		case len(out) > 50_000:
			n = 4
		case len(out) > 2_000:
			n = 3
		}
		n += r.IntN(2)
		var b strings.Builder
		for i := 0; i < n; i++ {
			b.WriteString(syll[r.IntN(len(syll))])
		}
		w := b.String()
		if w[0] == 'x' {
			continue
		}
		if _, ok := seen[w]; ok {
			continue
		}
		seen[w] = struct{}{}
		out = append(out, w)
	}
	return out
}

type person struct{ display, last string }

func buildPeople(vocab []string, n int) []person {
	r := rand.New(rand.NewPCG(7, 7))
	ps := make([]person, n)
	for i := range ps {
		first := vocab[3000+r.IntN(4000)]
		last := vocab[8000+i%30000] + "son"
		dom := vocab[1000+r.IntN(500)]
		ps[i] = person{fmt.Sprintf("%s %s <%s-%s@%s.example>", first, last, first, last, dom), last}
	}
	return ps
}

func title(w string) string {
	if w[0] >= 'a' && w[0] <= 'z' {
		return string(w[0]-32) + w[1:]
	}
	return w
}

func uniqueToken(doc, k uint64) string {
	x := (doc*0x9E3779B97F4A7C15 + k*0xC2B2AE3D27D4EB4F) ^ (doc >> 3)
	x ^= x >> 29
	x *= 0xBF58476D1CE4E5B9
	return fmt.Sprintf("x%016x", x)
}

func runGen(outDir string, total, dfAt int) {
	vocab := buildVocab()
	people := buildPeople(vocab, 20_050)
	r := rand.New(rand.NewPCG(2026, 11))
	wz := rand.NewZipf(r, 1.05, 2.7, vocabSize-1)
	pz := rand.NewZipf(r, 1.2, 3, uint64(len(people)-1))

	f, err := os.Create(outDir + "/corpus.tsv")
	if err != nil {
		panic(err)
	}
	defer f.Close()
	w := bufio.NewWriterSize(f, 1<<22)
	defer w.Flush()

	df := make([]int32, vocabSize)
	stamp := make([]int32, vocabSize)
	var totalBytes int64
	writeWords := func(b *strings.Builder, n int, doc int32) {
		for i := 0; i < n; i++ {
			k := wz.Uint64()
			if stamp[k] != doc {
				stamp[k] = doc
				df[k]++
			}
			word := vocab[k]
			if r.IntN(10) == 0 {
				word = title(word)
			}
			if i > 0 {
				if i%13 == 0 {
					b.WriteString(". ")
				} else {
					b.WriteByte(' ')
				}
			}
			b.WriteString(word)
		}
	}

	var uniqueSamples []string
	var b strings.Builder
	for d := 0; d < total; d++ {
		doc := int32(d + 1)
		date := dateStart + r.Int64N(dateSpan)
		from := people[pz.Uint64()].display
		nTo := 1 + r.IntN(4)
		tos := make([]string, nTo)
		for i := range tos {
			tos[i] = people[pz.Uint64()].display
		}

		b.Reset()
		if r.IntN(10) < 3 {
			b.WriteString("Re: ")
		}
		writeWords(&b, 3+r.IntN(9), doc)
		subject := b.String()

		b.Reset()
		// Log-normal length: median about 245 words, long tail, capped.
		words := int(math.Exp(5.5 + r.NormFloat64()))
		if words > 30_000 {
			words = 30_000
		}
		if words < 5 {
			words = 5
		}
		writeWords(&b, words, doc)
		// High-cardinality tokens real mail carries: tickets, references, tracking IDs.
		for k := uint64(0); k < 3; k++ {
			b.WriteByte(' ')
			b.WriteString(uniqueToken(uint64(d), k))
		}
		for p := 0; p < numPhrases; p++ {
			if r.IntN(200) == 0 {
				fmt.Fprintf(&b, " %s %s", vocab[3000+2*p], vocab[3000+2*p+1])
			}
		}
		body := b.String()

		att := ""
		if r.IntN(4) == 0 {
			n := 1 + r.IntN(3)
			names := make([]string, n)
			for i := range names {
				names[i] = fmt.Sprintf("%s-%s-%d.pdf", vocab[wz.Uint64()], vocab[wz.Uint64()], 2022+r.IntN(5))
			}
			att = strings.Join(names, " ")
		}
		if d%50_000 == 12_345 && len(uniqueSamples) < 20 {
			uniqueSamples = append(uniqueSamples, uniqueToken(uint64(d), 0))
		}
		n, _ := fmt.Fprintf(w, "%d\t%d\t%s\t%s\t%s\t%s\t%s\n", d, date, from, strings.Join(tos, ", "), subject, body, att)
		totalBytes += int64(n)

		if d+1 == dfAt {
			writeQueries(outDir+"/queries.tsv", vocab, people, df, dfAt, uniqueSamples)
		}
		if (d+1)%500_000 == 0 {
			fmt.Fprintf(os.Stderr, "generated %d, %.1f GiB\n", d+1, float64(totalBytes)/(1<<30))
		}
	}
	fmt.Fprintf(os.Stderr, "done: %d docs, %.2f GiB, mean %.0f bytes\n", total, float64(totalBytes)/(1<<30), float64(totalBytes)/float64(total))
}

// writeQueries picks terms by measured document frequency in the first dfAt docs.
func writeQueries(path string, vocab []string, people []person, df []int32, n int, uniq []string) {
	f, err := os.Create(path)
	if err != nil {
		panic(err)
	}
	defer f.Close()
	w := bufio.NewWriter(f)
	defer w.Flush()

	type rd struct {
		rank int
		df   int32
	}
	all := make([]rd, len(df))
	for i := range df {
		all[i] = rd{i, df[i]}
	}
	sort.Slice(all, func(a, b int) bool { return all[a].df > all[b].df })
	near := func(target float64, count int) []string {
		t := int32(target * float64(n))
		i := sort.Search(len(all), func(i int) bool { return all[i].df <= t })
		out := []string{}
		for j := i; j < len(all) && len(out) < count; j += 3 {
			out = append(out, vocab[all[j].rank])
		}
		return out
	}
	common := near(0.60, 20)
	mid := near(0.01, 20)
	rare := near(0.0001, 20)
	q := func(class, sortBy string, from, to int64, clauses ...string) {
		fmt.Fprintf(w, "%s\t%s\t%d\t%d\t%s\n", class, sortBy, from, to, strings.Join(clauses, ";;"))
	}
	t := func(s string) string { return "text|term|" + s }
	p := func(s string) string { return "text|prefix|" + s }
	runes4 := func(s string, k int) string {
		rs := []rune(s)
		if len(rs) > k {
			rs = rs[:k]
		}
		return string(rs)
	}
	for _, s := range uniq {
		q("unique_token", "date", 0, 0, t(s))
	}
	for i := 0; i < 20; i++ {
		q("rare_0.01pct", "date", 0, 0, t(rare[i]))
		q("mid_1pct", "date", 0, 0, t(mid[i]))
		q("common_60pct", "date", 0, 0, t(common[i]))
		q("and_common_mid", "date", 0, 0, t(common[i]), t(mid[i]))
		q("prefix_4char", "date", 0, 0, p(runes4(vocab[200+37*i], 4)))
		q("prefix_2char", "date", 0, 0, p(runes4(vocab[50+37*i], 2)))
		q("typed_term_term_prefix", "date", 0, 0, t(common[i]), t(mid[i]), p(runes4(vocab[200+37*i], 3)))
		q("phrase", "date", 0, 0, fmt.Sprintf("body|phrase|%s %s", vocab[3000+2*i], vocab[3000+2*i+1]))
		year := int64(365 * 86400)
		q("sender_90days", "date", dateStart+int64(i)*year/10, dateStart+int64(i)*year/10+90*86400, "from|term|"+people[5+i].last)
		q("mid_1year", "date", dateStart+year, dateStart+2*year, t(mid[i]))
		q("attachment_name", "date", 0, 0, "att|term|"+vocab[300+i*11])
		q("mid_1pct_bm25", "score", 0, 0, t(mid[i]))
		q("common_60pct_bm25", "score", 0, 0, t(common[i]))
		q("and_common_mid_bm25", "score", 0, 0, t(common[i]), t(mid[i]))
		q("count_common", "count", 0, 0, t(common[i]))
	}
}
