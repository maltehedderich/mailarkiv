#!/bin/bash
S=$1; cd "$S" || exit 1
G=$S/spike/spike-go-f; TG=$S/spike/rs/target/release/tspike; C=$S/data/s/corpus.tsv; F=$S/data/f2; Q=$S/data/s/queries.tsv
rm -rf "$F" "$S/data/f"; mkdir -p "$F"

echo "################ A: kill -9 of the real indexer process"
for e in bleve fts tantivy; do
 for round in 1 2 3; do
  d=$F/kill-$e-$round; ack=$F/ack-$e-$round
  if [ $e = tantivy ]; then "$TG" index "$d" "$C" 0 200000 1000 1 "$ack" > "$F/k.out" 2>&1 &
  else "$G" index $e "$d" "$C" 0 200000 1000 1 "$ack" > "$F/k.out" 2>&1 & fi
  pid=$!
  sleep $((20 + round * 7))
  kill -9 $pid; wait $pid 2>/dev/null
  acked=$(tail -1 "$ack")
  if [ $e = tantivy ]; then after=$("$TG" count "$d" 2>&1 | tail -1 | cut -c1-200); else after=$("$G" count $e "$d" 2>&1 | tail -1 | cut -c1-200); fi
  if [ $e = tantivy ]; then r=$(timeout -s KILL 120 "$TG" index "$d" "$C" "$acked" 2000 500 0 2>&1 | tail -1 | cut -c1-200); fin=$("$TG" count "$d" 2>&1 | tail -1); else r=$(timeout -s KILL 120 "$G" index $e "$d" "$C" "$acked" 2000 500 0 2>&1 | tail -1 | cut -c1-200); fin=$("$G" count $e "$d" 2>&1 | tail -1); fi
  echo "$e round=$round acked=$acked count_after_kill=[$after] expected_after_resume>=$((acked+2000)) count_after_resume=[$fin]"
  echo "   resume: $r"
  rm -rf "$d"
 done
done

echo "################ B: disk full WITH concurrent searches (OpenCloud's panic path)"
for e in bleve fts tantivy; do
  echo "== $e"
  if [ $e = tantivy ]; then BIN="/spike/rs/target/x86_64-unknown-linux-musl/release/tspike"; I="$BIN index /idx/e /corpus.tsv 0 200000 1000 1 /ack/a"; N="$BIN count /idx/e"; R="$BIN index /idx/e /corpus.tsv \$A 1000 500 0";
  else BIN="/spike/spike-go-f"; I="$BIN index $e /idx/e /corpus.tsv 0 200000 1000 1 /ack/a"; N="$BIN count $e /idx/e"; R="$BIN index $e /idx/e /corpus.tsv \$A 1000 500 0"; fi
  docker run --rm --network none --memory=3g --cpus=2 --user "$(id -u):$(id -g)" -e QUERY_WHILE=/q.tsv \
    --tmpfs /idx:size=128m,uid=$(id -u),gid=$(id -g),mode=700 --tmpfs /ack:size=8m,uid=$(id -u),gid=$(id -g),mode=700 \
    -v "$S/spike:/spike:ro" -v "$C:/corpus.tsv:ro" -v "$Q:/q.tsv:ro" alpine:latest sh -c "
      dd if=/dev/zero of=/idx/filler bs=1M count=24 2>/dev/null
      timeout -s KILL 240 $I > /ack/out 2>&1; echo first_run_rc=\$?
      grep -E '^panic:|panicked|fatal error|goroutine [0-9]+ \[running|nil map' /ack/out | head -6 | cut -c1-200
      echo panic_lines=\$(grep -c -E '^panic:|panicked|fatal error' /ack/out); tail -1 /ack/out | cut -c1-300
      A=\$(tail -1 /ack/a); echo last_ack=\$A; rm -f /idx/filler
      echo count_after: \$(timeout -s KILL 60 $N 2>&1 | tail -1 | cut -c1-200)
      unset QUERY_WHILE; timeout -s KILL 90 $R > /ack/out2 2>&1; echo resume_rc=\$?; tail -1 /ack/out2 | cut -c1-200
      echo count_after_resume: \$(timeout -s KILL 30 $N 2>&1 | tail -1 | cut -c1-200)
    " 2>&1
done

echo "################ C0: force merge on clean copies (cost to purge deleted terms, 200k docs)"
for e in bleve fts tantivy; do
  d=$F/clean-$e; cp -r "$S/data/s/$e" "$d"
  if [ $e = tantivy ]; then timeout -s KILL 500 "$TG" forcemerge "$d" 2>&1 | tail -1 | cut -c1-300; else timeout -s KILL 500 "$G" forcemerge $e "$d" 2>&1 | tail -1 | cut -c1-300; fi
  rm -rf "$d"
done

echo "################ C1: Bleve, corrupt smallest segment, force a merge through it"
for frac in 0.25 0.50 0.75; do
  d=$F/cb; rm -rf "$d"; cp -r "$S/data/s/bleve" "$d"
  v=$(find "$d/store" -name '*.zap' -printf '%s %p\n' | sort -n | awk 'NR==1{print $2}')
  python3 -I "$S/spike/corrupt.py" "$v" $frac
  timeout -s KILL 200 "$G" forcemerge bleve "$d" > "$F/m.out" 2>&1; echo "rc=$? (137 means killed by the 200 s limit)"
  grep -E 'async error|^panic:|fatal error' "$F/m.out" | head -3 | cut -c1-240; echo panic_lines=$(grep -c -E '^panic:|fatal error' "$F/m.out"); grep 'write_MiB' "$F/m.out" | tail -2 | cut -c1-280
  rm -rf "$d"
done

echo "################ C2: Tantivy, corrupt one segment file, checksum, query, force merge"
for ext in idx term pos fast; do
  d=$F/ct; rm -rf "$d"; cp -r "$S/data/s/tantivy" "$d"
  v=$(find "$d" -name "*.$ext" -printf '%s %p\n' | sort -n | awk '$1>100000{print $2; exit}')
  python3 -I "$S/spike/corrupt.py" "$v" 0.50
  echo "checksum: $("$TG" validate "$d" 2>&1 | tail -1 | cut -c1-120)"
  timeout -s KILL 120 "$TG" query "$d" "$Q" 1 10 > "$F/m.out" 2>&1; echo "query_rc=$? classes=$(grep -c '"class"' "$F/m.out")"; grep -m2 -E 'panicked|called `Result' "$F/m.out" | cut -c1-240
  timeout -s KILL 200 "$TG" forcemerge "$d" > "$F/m2.out" 2>&1; echo "merge_rc=$?"; grep -m2 -E 'panicked|Error' "$F/m2.out" | cut -c1-240; tail -1 "$F/m2.out" | cut -c1-300
  rm -rf "$d"
done

echo "################ C3: FTS5, corrupt middle of file, optimize"
d=$F/cf; rm -rf "$d"; cp -r "$S/data/s/fts" "$d"
python3 -I "$S/spike/corrupt.py" "$d/search.db" 0.50
timeout -s KILL 500 "$G" forcemerge fts "$d" 2>&1 | tail -1 | cut -c1-300
rm -rf "$d"
echo "################ done"
