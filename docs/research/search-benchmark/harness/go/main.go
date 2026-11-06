package main

import (
	"bufio"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	"github.com/blevesearch/bleve/v2"
	"github.com/blevesearch/bleve/v2/analysis/analyzer/custom"
	"github.com/blevesearch/bleve/v2/analysis/token/lowercase"
	"github.com/blevesearch/bleve/v2/analysis/tokenizer/unicode"
	"github.com/blevesearch/bleve/v2/index/scorch"
	"github.com/blevesearch/bleve/v2/index/scorch/mergeplan"
	"github.com/blevesearch/bleve/v2/search/query"
	_ "modernc.org/sqlite"
)

type doc struct {
	ID                          uint64
	Date                        int64
	From, To, Subject, Body, At string
}

func readCorpus(path string, skip, n int, fn func(doc) error) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 1<<20), 1<<27)
	i := 0
	for sc.Scan() {
		if i < skip {
			i++
			continue
		}
		if i >= skip+n {
			break
		}
		p := strings.SplitN(sc.Text(), "\t", 7)
		id, _ := strconv.ParseUint(p[0], 10, 64)
		date, _ := strconv.ParseInt(p[1], 10, 64)
		if err := fn(doc{id, date, p[2], p[3], p[4], p[5], p[6]}); err != nil {
			return err
		}
		i++
	}
	return sc.Err()
}

type clause struct{ scope, op, value string }
type q struct {
	class, sortBy string
	from, to      int64
	clauses       []clause
}

func readQueries(path string) []q {
	data, err := os.ReadFile(path)
	if err != nil {
		panic(err)
	}
	var out []q
	for _, line := range strings.Split(strings.TrimSpace(string(data)), "\n") {
		p := strings.Split(line, "\t")
		from, _ := strconv.ParseInt(p[2], 10, 64)
		to, _ := strconv.ParseInt(p[3], 10, 64)
		var cs []clause
		for _, c := range strings.Split(p[4], ";;") {
			x := strings.SplitN(c, "|", 3)
			cs = append(cs, clause{x[0], x[1], strings.ToLower(x[2])})
		}
		out = append(out, q{p[0], p[1], from, to, cs})
	}
	return out
}

var re = regexp.MustCompile(`(VmHWM|VmRSS):\s+(\d+) kB`)

func mem() map[string]float64 {
	b, _ := os.ReadFile("/proc/self/status")
	m := map[string]float64{}
	for _, x := range re.FindAllStringSubmatch(string(b), -1) {
		v, _ := strconv.ParseFloat(x[2], 64)
		m[x[1]+"_MiB"] = float64(int(v / 1024))
	}
	return m
}

func dirSize(dir string) int64 {
	var t int64
	filepath.Walk(dir, func(_ string, i os.FileInfo, e error) error {
		if e == nil && !i.IsDir() {
			t += i.Size()
		}
		return nil
	})
	return t
}

func emit(m map[string]any) {
	b, _ := json.Marshal(m)
	fmt.Println(string(b))
}

func pct(s []float64, p float64) float64 {
	return s[int(float64(len(s)-1)*p)]
}

// engine abstracts only this spike's two Go engines.
type engine interface {
	add(batch []doc) error
	del(ids []uint64, dates []int64) error
	search(x q) (int, error)
	count() (uint64, error)
	close() error
}

// ---- Bleve ----

type bleveEngine struct{ idx bleve.Index }

var asyncErrs atomic.Int64

func bleveConfig() map[string]interface{} {
	return map[string]interface{}{"asyncErrorCallbackName": "spike"}
}

func openBleve(dir string, create bool) *bleveEngine {
	scorch.RegistryAsyncErrorCallbacks["spike"] = func(err error, path string) {
		if asyncErrs.Add(1) <= 5 {
			fmt.Fprintf(os.Stderr, "bleve async error: %v\n", err)
		}
	}
	if !create {
		idx, err := bleve.OpenUsing(dir, bleveConfig())
		if err != nil {
			panic(err)
		}
		return &bleveEngine{idx}
	}
	im := bleve.NewIndexMapping()
	if err := im.AddCustomAnalyzer("lc", map[string]interface{}{
		"type": custom.Name, "tokenizer": unicode.Name, "token_filters": []string{lowercase.Name},
	}); err != nil {
		panic(err)
	}
	im.DefaultAnalyzer = "lc"
	im.StoreDynamic = false
	im.IndexDynamic = false
	im.DocValuesDynamic = false
	dm := bleve.NewDocumentStaticMapping()
	for _, name := range []string{"from", "to", "subject", "body", "att"} {
		fm := bleve.NewTextFieldMapping()
		fm.Analyzer = "lc"
		fm.Store = false
		fm.IncludeInAll = false
		fm.IncludeTermVectors = true // required for phrase queries
		fm.DocValues = false
		dm.AddFieldMappingsAt(name, fm)
	}
	dm2 := bleve.NewNumericFieldMapping()
	dm2.Store = false
	dm2.IncludeInAll = false
	dm2.DocValues = true
	dm.AddFieldMappingsAt("date", dm2)
	im.DefaultMapping = dm
	idx, err := bleve.NewUsing(dir, im, bleve.Config.DefaultIndexType, bleve.Config.DefaultKVStore, bleveConfig())
	if err != nil {
		panic(err)
	}
	return &bleveEngine{idx}
}

func (e *bleveEngine) add(batch []doc) error {
	b := e.idx.NewBatch()
	for _, d := range batch {
		err := b.Index(strconv.FormatUint(d.ID, 10), map[string]interface{}{
			"date": float64(d.Date), "from": d.From, "to": d.To, "subject": d.Subject, "body": d.Body, "att": d.At,
		})
		if err != nil {
			return err
		}
	}
	return e.idx.Batch(b)
}

func (e *bleveEngine) del(ids []uint64, _ []int64) error {
	b := e.idx.NewBatch()
	for _, id := range ids {
		b.Delete(strconv.FormatUint(id, 10))
	}
	return e.idx.Batch(b)
}

func leaf(field, op, v string) query.Query {
	switch op {
	case "term":
		t := bleve.NewTermQuery(v)
		t.SetField(field)
		return t
	case "prefix":
		t := bleve.NewPrefixQuery(v)
		t.SetField(field)
		return t
	case "phrase":
		return bleve.NewPhraseQuery(strings.Fields(v), field)
	}
	panic(op)
}

var fieldOf = map[string]string{"body": "body", "from": "from", "to": "to", "subject": "subject", "att": "att"}

func (e *bleveEngine) search(x q) (int, error) {
	var must []query.Query
	for _, c := range x.clauses {
		if c.scope == "text" {
			must = append(must, bleve.NewDisjunctionQuery(leaf("body", c.op, c.value), leaf("subject", c.op, c.value)))
		} else {
			must = append(must, leaf(fieldOf[c.scope], c.op, c.value))
		}
	}
	if x.from != 0 {
		a, b := float64(x.from), float64(x.to)
		t := true
		r := bleve.NewNumericRangeInclusiveQuery(&a, &b, &t, &t)
		r.SetField("date")
		must = append(must, r)
	}
	var root query.Query = bleve.NewConjunctionQuery(must...)
	if len(must) == 1 {
		root = must[0]
	}
	size := 50
	if x.sortBy == "count" {
		size = 0
	}
	req := bleve.NewSearchRequestOptions(root, size, 0, false)
	if x.sortBy == "date" {
		req.SortBy([]string{"-date"})
		req.Score = "none"
	}
	if x.sortBy == "count" {
		req.Score = "none"
	}
	res, err := e.idx.Search(req)
	if err != nil {
		return 0, err
	}
	return int(res.Total), nil
}

func (e *bleveEngine) count() (uint64, error) { return e.idx.DocCount() }
func (e *bleveEngine) close() error           { return e.idx.Close() }

// ---- SQLite FTS5 ----

type ftsEngine struct {
	db     *sql.DB
	dbPath string
}

func openFTS(dir string, create bool) *ftsEngine {
	os.MkdirAll(dir, 0o755)
	p := dir + "/search.db"
	prefix := "prefix='2 3 4',"
	if os.Getenv("FTS_NOPREFIX") == "1" {
		prefix = ""
	}
	db, err := sql.Open("sqlite", "file:"+p+"?_pragma=journal_mode(WAL)&_pragma=synchronous(NORMAL)&_pragma=cache_size(-64000)&_pragma=busy_timeout(30000)")
	if err != nil {
		panic(err)
	}
	db.SetMaxOpenConns(1)
	if create {
		_, err = db.Exec(`CREATE VIRTUAL TABLE mail USING fts5(subject, body, from_addr, to_addr, att,
			content='', contentless_delete=1, detail=full, ` + prefix + `
			tokenize="unicode61 remove_diacritics 0")`)
		if err != nil {
			panic(err)
		}
		if _, err = db.Exec(`INSERT INTO mail(mail, rank) VALUES('secure-delete', 1)`); err != nil {
			panic(err)
		}
	}
	return &ftsEngine{db, p}
}

func rowid(date int64, id uint64) int64 { return date<<24 | int64(id&0xFFFFFF) }

func (e *ftsEngine) add(batch []doc) error {
	tx, err := e.db.Begin()
	if err != nil {
		return err
	}
	st, err := tx.Prepare(`INSERT INTO mail(rowid, subject, body, from_addr, to_addr, att) VALUES(?,?,?,?,?,?)`)
	if err != nil {
		tx.Rollback()
		return err
	}
	defer st.Close()
	for _, d := range batch {
		if _, err := st.Exec(rowid(d.Date, d.ID), d.Subject, d.Body, d.From, d.To, d.At); err != nil {
			tx.Rollback()
			return err
		}
	}
	return tx.Commit()
}

func (e *ftsEngine) del(ids []uint64, dates []int64) error {
	tx, err := e.db.Begin()
	if err != nil {
		return err
	}
	for i := range ids {
		if _, err := tx.Exec(`DELETE FROM mail WHERE rowid=?`, rowid(dates[i], ids[i])); err != nil {
			tx.Rollback()
			return err
		}
	}
	return tx.Commit()
}

func ftsExpr(cs []clause) string {
	var parts []string
	for _, c := range cs {
		col := map[string]string{"text": "{subject body}", "body": "body", "from": "from_addr", "to": "to_addr", "subject": "subject", "att": "att"}[c.scope]
		switch c.op {
		case "term", "phrase":
			parts = append(parts, fmt.Sprintf(`%s : "%s"`, col, c.value))
		case "prefix":
			parts = append(parts, fmt.Sprintf(`%s : "%s"*`, col, c.value))
		}
	}
	return strings.Join(parts, " AND ")
}

func (e *ftsEngine) search(x q) (int, error) {
	expr := ftsExpr(x.clauses)
	args := []any{expr}
	where := "mail MATCH ?"
	if x.from != 0 {
		where += " AND rowid BETWEEN ? AND ?"
		args = append(args, x.from<<24, x.to<<24|0xFFFFFF)
	}
	var sqlText string
	switch x.sortBy {
	case "date":
		sqlText = "SELECT rowid FROM mail WHERE " + where + " ORDER BY rowid DESC LIMIT 50"
	case "score":
		sqlText = "SELECT rowid FROM mail WHERE " + where + " ORDER BY rank LIMIT 50"
	case "count":
		var n int
		err := e.db.QueryRow("SELECT count(*) FROM mail WHERE "+where, args...).Scan(&n)
		return n, err
	}
	rows, err := e.db.Query(sqlText, args...)
	if err != nil {
		return 0, err
	}
	defer rows.Close()
	n := 0
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return 0, err
		}
		n++
	}
	return n, rows.Err()
}

func (e *ftsEngine) count() (uint64, error) {
	var n uint64
	err := e.db.QueryRow(`SELECT count(*) FROM mail_docsize`).Scan(&n)
	return n, err
}
func (e *ftsEngine) close() error { return e.db.Close() }

// ---- Commands ----

func open(name, dir string, create bool) engine {
	if name == "bleve" {
		return openBleve(dir, create)
	}
	return openFTS(dir, create)
}

func cmdIndex(name, dir, corpus string, skip, n, batchSize int, create bool, ack string) {
	t0 := time.Now()
	e := open(name, dir, create)
	var ackF *os.File
	if ack != "" {
		ackF, _ = os.Create(ack)
	}
	var qOK, qErr atomic.Int64
	if qp := os.Getenv("QUERY_WHILE"); qp != "" {
		if f, ok := e.(*ftsEngine); ok {
			f.db.SetMaxOpenConns(2)
		}
		qs := readQueries(qp)
		go func() {
			for {
				for _, x := range qs[:80] {
					if _, err := e.search(x); err != nil {
						if qErr.Add(1) == 1 {
							fmt.Fprintln(os.Stderr, "query during indexing failed:", err)
						}
					} else {
						qOK.Add(1)
					}
				}
			}
		}()
	}
	batch := make([]doc, 0, batchSize)
	done := 0
	flush := func() error {
		if err := e.add(batch); err != nil {
			return err
		}
		done += len(batch)
		batch = batch[:0]
		if ackF != nil {
			fmt.Fprintln(ackF, done)
			ackF.Sync()
		}
		return nil
	}
	last := time.Now()
	err := readCorpus(corpus, skip, n, func(d doc) error {
		batch = append(batch, d)
		if len(batch) == batchSize {
			if err := flush(); err != nil {
				return err
			}
			if time.Since(last) > 60*time.Second {
				last = time.Now()
				fmt.Fprintf(os.Stderr, "%s %d docs %.0fs rss=%v\n", name, done, time.Since(t0).Seconds(), mem())
			}
		}
		return nil
	})
	if err == nil && len(batch) > 0 {
		err = flush()
	}
	if err != nil {
		emit(map[string]any{"engine": name, "stage": "index", "error": err.Error(), "docs_acked": done, "elapsed_s": time.Since(t0).Seconds(), "async_errors": asyncErrs.Load(), "queries_ok": qOK.Load(), "queries_failed": qErr.Load()})
		os.Exit(3)
	}
	indexed := time.Since(t0)
	if err := e.close(); err != nil {
		panic(err)
	}
	r := map[string]any{"engine": name, "stage": "index", "docs": done, "batch": batchSize, "index_s": indexed.Seconds(), "close_s": time.Since(t0).Seconds() - indexed.Seconds(), "docs_per_s": float64(done) / time.Since(t0).Seconds(), "dir_MiB": dirSize(dir) >> 20, "async_errors": asyncErrs.Load()}
	for k, v := range mem() {
		r[k] = v
	}
	emit(r)
}

func cmdQuery(name, dir, queries string, reps int, budget time.Duration) {
	t0 := time.Now()
	e := open(name, dir, false)
	cnt, err := e.count()
	if err != nil {
		panic(err)
	}
	emit(map[string]any{"engine": name, "stage": "open", "open_s": time.Since(t0).Seconds(), "docs": cnt, "rss": mem()})
	qs := readQueries(queries)
	byClass := map[string][]q{}
	var order []string
	for _, x := range qs {
		if _, ok := byClass[x.class]; !ok {
			order = append(order, x.class)
		}
		byClass[x.class] = append(byClass[x.class], x)
	}
	for _, class := range order {
		start := time.Now()
		var ts []float64
		var hits []int
		errs := 0
	loop:
		for r := 0; r < reps; r++ {
			for _, x := range byClass[class] {
				t := time.Now()
				h, err := e.search(x)
				if err != nil {
					errs++
					if errs == 1 {
						fmt.Fprintf(os.Stderr, "%s %s error: %v\n", name, class, err)
					}
				}
				ts = append(ts, float64(time.Since(t).Microseconds())/1000)
				hits = append(hits, h)
				if time.Since(start) > budget {
					break loop
				}
			}
		}
		sort.Float64s(ts)
		sort.Ints(hits)
		emit(map[string]any{"engine": name, "class": class, "n": len(ts), "p50_ms": pct(ts, .5), "p95_ms": pct(ts, .95), "max_ms": ts[len(ts)-1], "median_hits": hits[len(hits)/2], "errors": errs})
	}
	r := map[string]any{"engine": name, "stage": "query_end", "async_errors": asyncErrs.Load()}
	for k, v := range mem() {
		r[k] = v
	}
	emit(r)
	if err := e.close(); err != nil {
		panic(err)
	}
}

// cmdUpdate measures small durable batches and deletes on a built index.
func cmdUpdate(name, dir, corpus string, skip int) {
	e := open(name, dir, false)
	const bs, rounds = 100, 30
	var add []float64
	var ids []uint64
	var dates []int64
	for r := 0; r < rounds; r++ {
		var batch []doc
		readCorpus(corpus, skip+r*bs, bs, func(d doc) error { batch = append(batch, d); return nil })
		t := time.Now()
		if err := e.add(batch); err != nil {
			panic(err)
		}
		add = append(add, float64(time.Since(t).Microseconds())/1000)
		ids = append(ids, batch[0].ID)
		dates = append(dates, batch[0].Date)
	}
	t := time.Now()
	for i := range ids {
		if err := e.del(ids[i:i+1], dates[i:i+1]); err != nil {
			panic(err)
		}
	}
	delMs := float64(time.Since(t).Microseconds()) / 1000 / float64(len(ids))
	sort.Float64s(add)
	emit(map[string]any{"engine": name, "stage": "update", "batch100_p50_ms": pct(add, .5), "batch100_p95_ms": pct(add, .95), "delete_one_avg_ms": delMs})
	e.close()
}

func writeBytes() int64 {
	b, _ := os.ReadFile("/proc/self/io")
	for _, l := range strings.Split(string(b), "\n") {
		if strings.HasPrefix(l, "write_bytes:") {
			v, _ := strconv.ParseInt(strings.TrimSpace(l[12:]), 10, 64)
			return v
		}
	}
	return 0
}

// cmdSoak adds small batches, then idles, and reports background writes and errors.
func cmdSoak(name, dir, corpus string, skip, addSeconds, idleSeconds int) {
	e := open(name, dir, false)
	t0 := time.Now()
	off := skip
	addErrs := 0
	for time.Since(t0) < time.Duration(addSeconds)*time.Second {
		var batch []doc
		readCorpus(corpus, off, 200, func(d doc) error { batch = append(batch, d); return nil })
		off += 200
		if err := e.add(batch); err != nil {
			addErrs++
			if addErrs == 1 {
				fmt.Fprintln(os.Stderr, "add error:", err)
			}
		}
	}
	w0 := writeBytes()
	time.Sleep(time.Duration(idleSeconds) * time.Second)
	c, cerr := e.count()
	emit(map[string]any{"engine": name, "stage": "soak", "added": off - skip, "add_errors": addErrs, "async_errors": asyncErrs.Load(), "idle_write_MiB": (writeBytes() - w0) >> 20, "idle_seconds": idleSeconds, "count": c, "count_err": fmt.Sprint(cerr), "dir_MiB": dirSize(dir) >> 20})
	e.close()
}

// cmdForceMerge rewrites the index into one segment: the only way to purge deleted terms.
func cmdForceMerge(name, dir string) {
	e := open(name, dir, false)
	t0 := time.Now()
	go func() {
		for range time.Tick(20 * time.Second) {
			fmt.Fprintf(os.Stderr, "t=%.0fs write_MiB=%d async_errors=%d\n", time.Since(t0).Seconds(), writeBytes()>>20, asyncErrs.Load())
		}
	}()
	var err error
	switch v := e.(type) {
	case *bleveEngine:
		adv, aerr := v.idx.Advanced()
		if aerr != nil {
			panic(aerr)
		}
		opts := mergeplan.SingleSegmentMergePlanOptions
		err = adv.(*scorch.Scorch).ForceMerge(context.Background(), &opts)
	case *ftsEngine:
		_, err = v.db.Exec(`INSERT INTO mail(mail) VALUES('optimize')`)
	}
	c, cerr := e.count()
	emit(map[string]any{"engine": name, "stage": "forcemerge", "seconds": time.Since(t0).Seconds(), "error": fmt.Sprint(err), "async_errors": asyncErrs.Load(), "write_MiB": writeBytes() >> 20, "count": c, "count_err": fmt.Sprint(cerr), "dir_MiB": dirSize(dir) >> 20})
	e.close()
}

func main() {
	a := os.Args
	atoi := func(s string) int { v, _ := strconv.Atoi(s); return v }
	switch a[1] {
	case "gen": // gen outDir total dfAt
		runGen(a[2], atoi(a[3]), atoi(a[4]))
	case "index": // index engine dir corpus skip n batch create(0/1) [ackfile]
		ack := ""
		if len(a) > 9 {
			ack = a[9]
		}
		cmdIndex(a[2], a[3], a[4], atoi(a[5]), atoi(a[6]), atoi(a[7]), a[8] == "1", ack)
	case "query": // query engine dir queries reps budgetSeconds
		cmdQuery(a[2], a[3], a[4], atoi(a[5]), time.Duration(atoi(a[6]))*time.Second)
	case "update": // update engine dir corpus skip
		cmdUpdate(a[2], a[3], a[4], atoi(a[5]))
	case "forcemerge": // forcemerge engine dir
		cmdForceMerge(a[2], a[3])
	case "soak": // soak engine dir corpus skip addSeconds idleSeconds
		cmdSoak(a[2], a[3], a[4], atoi(a[5]), atoi(a[6]), atoi(a[7]))
	case "integrity": // integrity engine dir
		if a[2] == "fts" {
			e := openFTS(a[3], false)
			_, err := e.db.Exec(`INSERT INTO mail(mail, rank) VALUES('integrity-check', 0)`)
			var qc string
			e.db.QueryRow(`PRAGMA quick_check`).Scan(&qc)
			fmt.Println("fts5 integrity-check:", err, "quick_check:", qc)
			e.close()
		}
	case "count":
		e := open(a[2], a[3], false)
		c, err := e.count()
		fmt.Println(c, err)
		e.close()
	default:
		panic("unknown command")
	}
}
