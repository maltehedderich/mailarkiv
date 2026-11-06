#[cfg(feature = "mi")]
#[global_allocator]
static GLOBAL: mimalloc::MiMalloc = mimalloc::MiMalloc;

use std::fs::File;
use std::io::{BufRead, BufReader, Write};
use std::ops::Bound;
use std::path::Path;
use std::time::{Duration, Instant};

use tantivy::collector::{Count, TopDocs};
use tantivy::query::{BooleanQuery, Occur, PhraseQuery, Query, RangeQuery, RegexQuery, TermQuery};
use tantivy::schema::{Field, IndexRecordOption, Schema, FAST, INDEXED, TEXT};
use tantivy::{DocAddress, Index, IndexReader, IndexWriter, Order, ReloadPolicy, TantivyDocument, Term};

struct F { id: Field, date: Field, from: Field, to: Field, subject: Field, body: Field, att: Field }

fn schema() -> (Schema, F) {
    let mut b = Schema::builder();
    let f = F {
        id: b.add_u64_field("id", FAST | INDEXED),
        date: b.add_i64_field("date", FAST | INDEXED),
        from: b.add_text_field("from", TEXT),
        to: b.add_text_field("to", TEXT),
        subject: b.add_text_field("subject", TEXT),
        body: b.add_text_field("body", TEXT),
        att: b.add_text_field("att", TEXT),
    };
    (b.build(), f)
}

fn fields(s: &Schema) -> F {
    let g = |n: &str| s.get_field(n).unwrap();
    F { id: g("id"), date: g("date"), from: g("from"), to: g("to"), subject: g("subject"), body: g("body"), att: g("att") }
}

fn mem() -> String {
    let s = std::fs::read_to_string("/proc/self/status").unwrap();
    let get = |k: &str| s.lines().find(|l| l.starts_with(k)).map(|l| l.split_whitespace().nth(1).unwrap().parse::<u64>().unwrap() / 1024).unwrap_or(0);
    format!("\"VmHWM_MiB\":{},\"VmRSS_MiB\":{}", get("VmHWM:"), get("VmRSS:"))
}

fn dir_size(p: &Path) -> u64 {
    std::fs::read_dir(p).map(|d| d.flatten().map(|e| e.metadata().map(|m| m.len()).unwrap_or(0)).sum()).unwrap_or(0)
}

fn for_docs(path: &str, skip: usize, n: usize, mut f: impl FnMut(Vec<&str>) -> tantivy::Result<()>) -> tantivy::Result<()> {
    let mut r = BufReader::with_capacity(1 << 20, File::open(path)?);
    let mut line = String::new();
    let mut i = 0usize;
    loop {
        line.clear();
        if r.read_line(&mut line)? == 0 || i >= skip + n { return Ok(()); }
        if i >= skip {
            let p: Vec<&str> = line.trim_end_matches('\n').splitn(7, '\t').collect();
            f(p)?;
        }
        i += 1;
    }
}

fn to_doc(p: &[&str], f: &F) -> TantivyDocument {
    let mut d = TantivyDocument::default();
    d.add_u64(f.id, p[0].parse().unwrap());
    d.add_i64(f.date, p[1].parse().unwrap());
    d.add_text(f.from, p[2]);
    d.add_text(f.to, p[3]);
    d.add_text(f.subject, p[4]);
    d.add_text(f.body, p[5]);
    d.add_text(f.att, p[6]);
    d
}

fn cmd_index(dir: &str, corpus: &str, skip: usize, n: usize, batch: usize, create: bool, budget: usize, ack: Option<&str>) {
    let t0 = Instant::now();
    std::fs::create_dir_all(dir).unwrap();
    let (s, _) = schema();
    let index = if create { Index::create_in_dir(dir, s).unwrap() } else { Index::open_in_dir(dir).unwrap() };
    let f = fields(&index.schema());
    let mut w: IndexWriter = index.writer(budget).unwrap();
    let q_ok = std::sync::Arc::new(std::sync::atomic::AtomicU64::new(0));
    let q_err = std::sync::Arc::new(std::sync::atomic::AtomicU64::new(0));
    if let Ok(qp) = std::env::var("QUERY_WHILE") {
        let qs = read_queries(&qp);
        let idx2 = index.clone();
        let (ok2, err2) = (q_ok.clone(), q_err.clone());
        std::thread::spawn(move || {
            let f2 = fields(&idx2.schema());
            let reader: IndexReader = idx2.reader_builder().reload_policy(ReloadPolicy::Manual).try_into().unwrap();
            loop {
                if let Err(e) = reader.reload() { if err2.fetch_add(1, std::sync::atomic::Ordering::Relaxed) == 0 { eprintln!("reload failed: {e}"); } }
                let s = reader.searcher();
                for q in qs.iter().take(80) {
                    let c = TopDocs::with_limit(50).order_by_fast_field::<i64>("date", Order::Desc);
                    match s.search(&build(q, &f2), &c) {
                        Ok(_) => { ok2.fetch_add(1, std::sync::atomic::Ordering::Relaxed); }
                        Err(e) => { if err2.fetch_add(1, std::sync::atomic::Ordering::Relaxed) == 0 { eprintln!("query failed: {e}"); } }
                    }
                }
            }
        });
    }
    let mut ack_f = ack.map(|p| File::create(p).unwrap());
    let mut done = 0usize;
    let mut pending = 0usize;
    let mut last = Instant::now();
    let res = for_docs(corpus, skip, n, |p| {
        w.add_document(to_doc(&p, &f))?;
        pending += 1;
        if pending == batch {
            w.commit()?;
            done += pending;
            pending = 0;
            if let Some(a) = ack_f.as_mut() { writeln!(a, "{done}")?; a.sync_all()?; }
            if last.elapsed() > Duration::from_secs(60) {
                last = Instant::now();
                eprintln!("tantivy {} docs {:.0}s {}", done, t0.elapsed().as_secs_f64(), mem());
            }
        }
        Ok(())
    });
    let res = res.and_then(|_| { if pending > 0 { w.commit()?; done += pending; } Ok(()) });
    if let Err(e) = res {
        println!("{{\"engine\":\"tantivy\",\"stage\":\"index\",\"error\":{:?},\"docs_acked\":{},\"elapsed_s\":{:.1},\"queries_ok\":{},\"queries_failed\":{}}}", e.to_string(), done, t0.elapsed().as_secs_f64(), q_ok.load(std::sync::atomic::Ordering::Relaxed), q_err.load(std::sync::atomic::Ordering::Relaxed));
        std::process::exit(3);
    }
    let indexed = t0.elapsed().as_secs_f64();
    w.wait_merging_threads().unwrap();
    let total = t0.elapsed().as_secs_f64();
    println!("{{\"engine\":\"tantivy\",\"stage\":\"index\",\"docs\":{},\"batch\":{},\"index_s\":{:.1},\"close_s\":{:.1},\"docs_per_s\":{:.0},\"dir_MiB\":{},{}}}", done, batch, indexed, total - indexed, done as f64 / total, dir_size(Path::new(dir)) >> 20, mem());
}

struct Q { class: String, sort: String, from: i64, to: i64, clauses: Vec<(String, String, String)> }

fn read_queries(path: &str) -> Vec<Q> {
    std::fs::read_to_string(path).unwrap().lines().map(|l| {
        let p: Vec<&str> = l.split('\t').collect();
        let clauses = p[4].split(";;").map(|c| {
            let x: Vec<&str> = c.splitn(3, '|').collect();
            (x[0].to_string(), x[1].to_string(), x[2].to_lowercase())
        }).collect();
        Q { class: p[0].into(), sort: p[1].into(), from: p[2].parse().unwrap(), to: p[3].parse().unwrap(), clauses }
    }).collect()
}

fn leaf(field: Field, op: &str, v: &str, scored: bool) -> Box<dyn Query> {
    let rec = if scored { IndexRecordOption::WithFreqs } else { IndexRecordOption::Basic };
    match op {
        "term" => Box::new(TermQuery::new(Term::from_field_text(field, v), rec)),
        "prefix" => Box::new(RegexQuery::from_pattern(&format!("{}.*", v), field).unwrap()),
        "phrase" => Box::new(PhraseQuery::new(v.split_whitespace().map(|t| Term::from_field_text(field, t)).collect())),
        _ => panic!(),
    }
}

fn build(q: &Q, f: &F) -> Box<dyn Query> {
    let scored = q.sort == "score";
    let mut must: Vec<(Occur, Box<dyn Query>)> = vec![];
    for (scope, op, v) in &q.clauses {
        let qq: Box<dyn Query> = match scope.as_str() {
            "text" => Box::new(BooleanQuery::new(vec![
                (Occur::Should, leaf(f.body, op, v, scored)),
                (Occur::Should, leaf(f.subject, op, v, scored)),
            ])),
            "body" => leaf(f.body, op, v, scored),
            "from" => leaf(f.from, op, v, scored),
            "to" => leaf(f.to, op, v, scored),
            "att" => leaf(f.att, op, v, scored),
            "subject" => leaf(f.subject, op, v, scored),
            _ => panic!(),
        };
        must.push((Occur::Must, qq));
    }
    if q.from != 0 {
        must.push((Occur::Must, Box::new(RangeQuery::new(
            Bound::Included(Term::from_field_i64(f.date, q.from)),
            Bound::Included(Term::from_field_i64(f.date, q.to)),
        ))));
    }
    if must.len() == 1 { must.pop().unwrap().1 } else { Box::new(BooleanQuery::new(must)) }
}

fn read_ids(reader: &IndexReader, addrs: impl Iterator<Item = DocAddress>) -> u64 {
    let s = reader.searcher();
    let mut x = 0u64;
    for a in addrs {
        let c = s.segment_reader(a.segment_ord).fast_fields().u64("id").unwrap();
        x ^= c.first(a.doc_id).unwrap_or(0);
    }
    x
}

fn cmd_query(dir: &str, queries: &str, reps: usize, budget: u64) {
    let t0 = Instant::now();
    let index = Index::open_in_dir(dir).unwrap();
    let f = fields(&index.schema());
    let reader = index.reader_builder().reload_policy(ReloadPolicy::Manual).try_into().unwrap();
    let searcher = reader.searcher();
    println!("{{\"engine\":\"tantivy\",\"stage\":\"open\",\"open_s\":{:.3},\"docs\":{},\"segments\":{},{}}}", t0.elapsed().as_secs_f64(), searcher.num_docs(), searcher.segment_readers().len(), mem());
    let qs = read_queries(queries);
    let mut order: Vec<String> = vec![];
    for q in &qs { if !order.contains(&q.class) { order.push(q.class.clone()); } }
    let mut sink = 0u64;
    for class in order {
        let cq: Vec<&Q> = qs.iter().filter(|q| q.class == class).collect();
        let start = Instant::now();
        let mut ts: Vec<f64> = vec![];
        let mut hits: Vec<usize> = vec![];
        'l: for _ in 0..reps {
            for q in &cq {
                let t = Instant::now();
                let query = build(q, &f);
                let h = match q.sort.as_str() {
                    "date" => {
                        let c = TopDocs::with_limit(50).order_by_fast_field::<i64>("date", Order::Desc);
                        let (n, top) = searcher.search(&query, &(Count, c)).unwrap();
                        sink ^= read_ids(&reader, top.iter().map(|(_, a)| *a));
                        n
                    }
                    "score" => {
                        let c = TopDocs::with_limit(50).order_by_score();
                        let (n, top) = searcher.search(&query, &(Count, c)).unwrap();
                        sink ^= read_ids(&reader, top.iter().map(|(_, a)| *a));
                        n
                    }
                    _ => searcher.search(&query, &Count).unwrap(),
                };
                ts.push(t.elapsed().as_secs_f64() * 1000.0);
                hits.push(h);
                if start.elapsed().as_secs() > budget { break 'l; }
            }
        }
        ts.sort_by(|a, b| a.partial_cmp(b).unwrap());
        hits.sort();
        let pct = |p: f64| ts[((ts.len() - 1) as f64 * p) as usize];
        println!("{{\"engine\":\"tantivy\",\"class\":\"{}\",\"n\":{},\"p50_ms\":{:.3},\"p95_ms\":{:.3},\"max_ms\":{:.3},\"median_hits\":{},\"errors\":0}}", class, ts.len(), pct(0.5), pct(0.95), ts[ts.len() - 1], hits[hits.len() / 2]);
    }
    println!("{{\"engine\":\"tantivy\",\"stage\":\"query_end\",\"sink\":{},{}}}", sink, mem());
}

fn cmd_update(dir: &str, corpus: &str, skip: usize, budget: usize) {
    let index = Index::open_in_dir(dir).unwrap();
    let f = fields(&index.schema());
    let mut w: IndexWriter = index.writer(budget).unwrap();
    let (bs, rounds) = (100usize, 30usize);
    let mut add: Vec<f64> = vec![];
    let mut ids: Vec<u64> = vec![];
    for r in 0..rounds {
        let mut docs = vec![];
        for_docs(corpus, skip + r * bs, bs, |p| { docs.push(to_doc(&p, &f)); if docs.len() == 1 { ids.push(p[0].parse().unwrap()); } Ok(()) }).unwrap();
        let t = Instant::now();
        for d in docs { w.add_document(d).unwrap(); }
        w.commit().unwrap();
        add.push(t.elapsed().as_secs_f64() * 1000.0);
    }
    let t = Instant::now();
    for id in &ids {
        w.delete_term(Term::from_field_u64(f.id, *id));
        w.commit().unwrap();
    }
    let del = t.elapsed().as_secs_f64() * 1000.0 / ids.len() as f64;
    add.sort_by(|a, b| a.partial_cmp(b).unwrap());
    w.wait_merging_threads().unwrap();
    println!("{{\"engine\":\"tantivy\",\"stage\":\"update\",\"batch100_p50_ms\":{:.1},\"batch100_p95_ms\":{:.1},\"delete_one_avg_ms\":{:.1}}}", add[add.len() / 2], add[(add.len() - 1) * 95 / 100], del);
}

fn write_bytes() -> u64 {
    std::fs::read_to_string("/proc/self/io").unwrap().lines().find(|l| l.starts_with("write_bytes:")).map(|l| l[12..].trim().parse().unwrap()).unwrap_or(0)
}

fn cmd_soak(dir: &str, corpus: &str, skip: usize, add_s: u64, idle_s: u64, budget: usize) {
    let index = Index::open_in_dir(dir).unwrap();
    let f = fields(&index.schema());
    let mut w: IndexWriter = index.writer(budget).unwrap();
    let t0 = Instant::now();
    let mut off = skip;
    let mut errs = 0;
    while t0.elapsed().as_secs() < add_s {
        for_docs(corpus, off, 200, |p| { if let Err(e) = w.add_document(to_doc(&p, &f)) { errs += 1; if errs == 1 { eprintln!("add error: {e}"); } } Ok(()) }).unwrap();
        off += 200;
        if let Err(e) = w.commit() { errs += 1; if errs <= 2 { eprintln!("commit error: {e}"); } }
    }
    let w0 = write_bytes();
    std::thread::sleep(Duration::from_secs(idle_s));
    let reader: IndexReader = index.reader().unwrap();
    println!("{{\"engine\":\"tantivy\",\"stage\":\"soak\",\"added\":{},\"add_errors\":{},\"idle_write_MiB\":{},\"idle_seconds\":{},\"count\":{},\"dir_MiB\":{}}}", off - skip, errs, (write_bytes() - w0) >> 20, idle_s, reader.searcher().num_docs(), dir_size(Path::new(dir)) >> 20);
}

fn cmd_forcemerge(dir: &str, budget: usize) {
    let index = Index::open_in_dir(dir).unwrap();
    let ids = index.searchable_segment_ids().unwrap();
    let mut w: IndexWriter = index.writer(budget).unwrap();
    let t0 = Instant::now();
    let r = w.merge(&ids).wait();
    let err = match &r { Ok(_) => "none".to_string(), Err(e) => e.to_string() };
    let wm = w.wait_merging_threads();
    let reader: IndexReader = index.reader().unwrap();
    println!("{{\"engine\":\"tantivy\",\"stage\":\"forcemerge\",\"segments_in\":{},\"seconds\":{:.1},\"error\":{:?},\"wait_err\":{:?},\"write_MiB\":{},\"count\":{},\"dir_MiB\":{}}}", ids.len(), t0.elapsed().as_secs_f64(), err, wm.err().map(|e| e.to_string()), write_bytes() >> 20, reader.searcher().num_docs(), dir_size(Path::new(dir)) >> 20);
}

fn main() {
    let a: Vec<String> = std::env::args().collect();
    let budget: usize = std::env::var("TANTIVY_BUDGET").ok().and_then(|v| v.parse().ok()).unwrap_or(256_000_000);
    let u = |s: &str| s.parse::<usize>().unwrap();
    match a[1].as_str() {
        "index" => cmd_index(&a[2], &a[3], u(&a[4]), u(&a[5]), u(&a[6]), a[7] == "1", budget, a.get(8).map(|s| s.as_str())),
        "query" => cmd_query(&a[2], &a[3], u(&a[4]), u(&a[5]) as u64),
        "update" => cmd_update(&a[2], &a[3], u(&a[4]), budget),
        "forcemerge" => cmd_forcemerge(&a[2], budget),
        "soak" => cmd_soak(&a[2], &a[3], u(&a[4]), u(&a[5]) as u64, u(&a[6]) as u64, budget),
        "count" => {
            let i = Index::open_in_dir(&a[2]).unwrap();
            let r: IndexReader = i.reader().unwrap();
            println!("{} ok", r.searcher().num_docs());
        }
        "validate" => {
            let i = Index::open_in_dir(&a[2]).unwrap();
            println!("{:?}", i.validate_checksum());
        }
        _ => panic!("unknown command"),
    }
}
