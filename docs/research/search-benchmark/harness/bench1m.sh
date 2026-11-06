#!/bin/bash
S=$1; cd "$S" || exit 1
G=$S/spike/spike-go-f; T=$S/spike/rs/target/release/tspike; TM=$S/spike/rs/target/x86_64-unknown-linux-musl/release/tspike
D=$S/data/m; R=$S/results; N=1000000
mkdir -p "$D"
lim() { systemd-run --user --scope -q -p MemoryMax=3G -p MemorySwapMax=0 -p CPUQuota=300% nice -n 15 ionice -c3 "$@"; }
evict() { find "$1" -type f -print0 | while IFS= read -r -d '' f; do sync -f "$f"; dd if="$f" iflag=nocache count=0 2>/dev/null; done; }

echo "== $(date +%T) warm queries at 200k"
for e in bleve fts; do lim "$G" query $e data/s/$e data/s/queries.tsv 5 60 > "$R/200k-$e.jsonl" 2>&1; done
lim "$T" query data/s/tantivy data/s/queries.tsv 5 60 > "$R/200k-tantivy.jsonl" 2>&1

echo "== $(date +%T) musl allocator check, Tantivy 200k"
rm -rf "$D/musl"; lim "$TM" index "$D/musl" data/s/corpus.tsv 0 200000 5000 1 > "$R/200k-tantivy-musl.jsonl" 2>&1
lim "$TM" query "$D/musl" data/s/queries.tsv 5 30 >> "$R/200k-tantivy-musl.jsonl" 2>&1; rm -rf "$D/musl"

echo "== $(date +%T) generate 5M corpus"
[ -s "$D/corpus.tsv" ] || nice -n 15 "$G" gen "$D" 5000000 1000000 2>&1 | tail -1
sed -n '1000001,1003000p;1003001q' "$D/corpus.tsv" > "$D/update.tsv"

echo "== $(date +%T) tantivy"
rm -rf "$D/tantivy"; lim "$T" index "$D/tantivy" "$D/corpus.tsv" 0 $N 5000 1 > "$R/1m-tantivy.jsonl" 2>"$R/1m-tantivy.err"; echo "rc=$?"
du -sm "$D/tantivy" | sed 's/^/du_MiB /' >> "$R/1m-tantivy.jsonl"; find "$D/tantivy" -name '*.idx' | wc -l | sed 's/^/segments /' >> "$R/1m-tantivy.jsonl"
evict "$D/tantivy"; lim "$T" query "$D/tantivy" "$D/queries.tsv" 1 60 > "$R/1m-tantivy-cold.jsonl" 2>&1
lim "$T" query "$D/tantivy" "$D/queries.tsv" 5 120 > "$R/1m-tantivy-warm.jsonl" 2>&1
cp -r "$D/tantivy" "$D/tantivy-u"; lim "$T" update "$D/tantivy-u" "$D/update.tsv" 0 >> "$R/1m-tantivy.jsonl" 2>&1; rm -rf "$D/tantivy-u"
cp -r "$D/tantivy" "$D/tantivy-fm"; lim "$T" forcemerge "$D/tantivy-fm" >> "$R/1m-tantivy.jsonl" 2>&1; rm -rf "$D/tantivy-fm"

echo "== $(date +%T) bleve"
rm -rf "$D/bleve"; GOMEMLIMIT=2400MiB lim "$G" index bleve "$D/bleve" "$D/corpus.tsv" 0 $N 5000 1 > "$R/1m-bleve.jsonl" 2>"$R/1m-bleve.err"; echo "rc=$?"
du -sm "$D/bleve" | sed 's/^/du_MiB /' >> "$R/1m-bleve.jsonl"; find "$D/bleve" -name '*.zap' | wc -l | sed 's/^/segments /' >> "$R/1m-bleve.jsonl"
evict "$D/bleve"; GOMEMLIMIT=2400MiB lim "$G" query bleve "$D/bleve" "$D/queries.tsv" 1 60 > "$R/1m-bleve-cold.jsonl" 2>&1
GOMEMLIMIT=2400MiB lim "$G" query bleve "$D/bleve" "$D/queries.tsv" 5 120 > "$R/1m-bleve-warm.jsonl" 2>&1
cp -r "$D/bleve" "$D/bleve-u"; GOMEMLIMIT=2400MiB lim "$G" update bleve "$D/bleve-u" "$D/update.tsv" 0 >> "$R/1m-bleve.jsonl" 2>&1; rm -rf "$D/bleve-u"
cp -r "$D/bleve" "$D/bleve-fm"; GOMEMLIMIT=2400MiB timeout -s KILL 1500 systemd-run --user --scope -q -p MemoryMax=3G -p MemorySwapMax=0 -p CPUQuota=300% nice -n 15 ionice -c3 "$G" forcemerge bleve "$D/bleve-fm" 2>/dev/null | tail -1 >> "$R/1m-bleve.jsonl"; rm -rf "$D/bleve-fm"

echo "== $(date +%T) fts5 without prefix index"
rm -rf "$D/fts"; FTS_NOPREFIX=1 GOMEMLIMIT=2400MiB timeout -s KILL 5400 systemd-run --user --scope -q -p MemoryMax=3G -p MemorySwapMax=0 -p CPUQuota=300% nice -n 15 ionice -c3 "$G" index fts "$D/fts" "$D/corpus.tsv" 0 $N 5000 1 > "$R/1m-fts.jsonl" 2>"$R/1m-fts.err"; echo "rc=$?"
du -sm "$D/fts" | sed 's/^/du_MiB /' >> "$R/1m-fts.jsonl"; tail -2 "$R/1m-fts.err" | cut -c1-120
evict "$D/fts"; lim "$G" query fts "$D/fts" "$D/queries.tsv" 1 60 > "$R/1m-fts-cold.jsonl" 2>&1
lim "$G" query fts "$D/fts" "$D/queries.tsv" 5 120 > "$R/1m-fts-warm.jsonl" 2>&1
cp -r "$D/fts" "$D/fts-u"; lim "$G" update fts "$D/fts-u" "$D/update.tsv" 0 >> "$R/1m-fts.jsonl" 2>&1; rm -rf "$D/fts-u"
echo "== $(date +%T) done"
