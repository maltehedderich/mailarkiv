#!/bin/bash
# Finishes the 1M comparison and runs Tantivy (static mimalloc build) at the 5M target.
S=$1; cd "$S" || exit 1
G=$S/spike/spike-go-f; T=$S/spike/rs/target/release/tspike; TMI=$S/spike/rs/target-mi/release/tspike
D=$S/data/m; R=$S/results; N=1000000
Q=$D/queries.tsv
lim() { systemd-run --user --scope -q -p MemoryMax=3G -p MemorySwapMax=0 -p CPUQuota=300% nice -n 15 ionice -c3 "$@"; }
limt() { secs=$1; shift; timeout -s KILL "$secs" systemd-run --user --scope -q -p MemoryMax=3G -p MemorySwapMax=0 -p CPUQuota=300% nice -n 15 ionice -c3 "$@"; }
evict() { find "$1" -type f -print0 | while IFS= read -r -d '' f; do sync -f "$f"; dd if="$f" iflag=nocache count=0 2>/dev/null; done; }

echo "== $(date +%T) fresh-process warm-OS pass, Tantivy and Bleve at 1M"
lim "$T" query "$D/tantivy" "$Q" 1 60 > "$R/1m-tantivy-osw.jsonl" 2>&1
GOMEMLIMIT=2400MiB lim "$G" query bleve "$D/bleve" "$Q" 1 60 > "$R/1m-bleve-osw.jsonl" 2>&1

echo "== $(date +%T) bleve update and force merge"
rm -rf "$D/bleve-u"; cp -r "$D/bleve" "$D/bleve-u"
GOMEMLIMIT=2400MiB lim "$G" update bleve "$D/bleve-u" "$D/update.tsv" 0 >> "$R/1m-bleve.jsonl" 2>&1; echo "update rc=$?"; rm -rf "$D/bleve-u"
rm -rf "$D/bleve-fm"; cp -r "$D/bleve" "$D/bleve-fm"
GOMEMLIMIT=2400MiB limt 1500 "$G" forcemerge bleve "$D/bleve-fm" 2>"$R/1m-bleve-fm.err" | tail -1 >> "$R/1m-bleve.jsonl"; echo "forcemerge rc=${PIPESTATUS[0]}"; rm -rf "$D/bleve-fm"

echo "== $(date +%T) fts5 at 1M without prefix index"
rm -rf "$D/fts"
FTS_NOPREFIX=1 GOMEMLIMIT=2400MiB limt 5400 "$G" index fts "$D/fts" "$D/corpus.tsv" 0 $N 5000 1 > "$R/1m-fts.jsonl" 2>"$R/1m-fts.err"; echo "index rc=$?"
du -sm "$D/fts" | sed 's/^/du_MiB /' >> "$R/1m-fts.jsonl"; tail -1 "$R/1m-fts.err" | cut -c1-120
evict "$D/fts"; lim "$G" query fts "$D/fts" "$Q" 1 60 > "$R/1m-fts-cold.jsonl" 2>&1
lim "$G" query fts "$D/fts" "$Q" 1 60 > "$R/1m-fts-osw.jsonl" 2>&1
lim "$G" query fts "$D/fts" "$Q" 5 120 > "$R/1m-fts-warm.jsonl" 2>&1
rm -rf "$D/fts-u"; cp -r "$D/fts" "$D/fts-u"; lim "$G" update fts "$D/fts-u" "$D/update.tsv" 0 >> "$R/1m-fts.jsonl" 2>&1; echo "update rc=$?"; rm -rf "$D/fts-u"

echo "== $(date +%T) tantivy at 5M, static mimalloc build"
rm -rf "$D/t5"
limt 7200 "$TMI" index "$D/t5" "$D/corpus.tsv" 0 5000000 5000 1 > "$R/5m-tantivy.jsonl" 2>"$R/5m-tantivy.err"; echo "index rc=$?"
du -sm "$D/t5" | sed 's/^/du_MiB /' >> "$R/5m-tantivy.jsonl"; find "$D/t5" -name '*.idx' | wc -l | sed 's/^/segments /' >> "$R/5m-tantivy.jsonl"
lim "$TMI" count "$D/t5" | sed 's/^/count /' >> "$R/5m-tantivy.jsonl"
evict "$D/t5"; lim "$TMI" query "$D/t5" "$Q" 1 60 > "$R/5m-tantivy-cold.jsonl" 2>&1
lim "$TMI" query "$D/t5" "$Q" 1 60 > "$R/5m-tantivy-osw.jsonl" 2>&1
lim "$TMI" query "$D/t5" "$Q" 5 120 > "$R/5m-tantivy-warm.jsonl" 2>&1
t0=$(date +%s); lim "$TMI" validate "$D/t5" 2>&1 | tail -1 | cut -c1-200 | sed 's/^/validate /' >> "$R/5m-tantivy.jsonl"; echo "validate_s $(( $(date +%s)-t0 ))" >> "$R/5m-tantivy.jsonl"
rm -rf "$D/t5-u"; cp -r "$D/t5" "$D/t5-u"; lim "$TMI" update "$D/t5-u" "$D/update.tsv" 0 >> "$R/5m-tantivy.jsonl" 2>&1; echo "update rc=$?"; rm -rf "$D/t5-u"
rm -rf "$D/t5-fm"; cp -r "$D/t5" "$D/t5-fm"; limt 3600 "$TMI" forcemerge "$D/t5-fm" >> "$R/5m-tantivy.jsonl" 2>&1; echo "forcemerge rc=$?"
lim "$TMI" query "$D/t5-fm" "$Q" 1 60 > "$R/5m-tantivy-fm.jsonl" 2>&1; rm -rf "$D/t5-fm"
echo "== $(date +%T) done"
