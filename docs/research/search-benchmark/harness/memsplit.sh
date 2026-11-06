#!/bin/bash
# Runs a command under the same limits and records peak anonymous and file-backed RSS.
out=$1; shift
systemd-run --user --scope -q -p MemoryMax=3G -p MemorySwapMax=0 -p CPUQuota=300% nice -n 15 ionice -c3 "$@" > "$out.stdout" 2>&1 &
pid=$!; pa=0; pf=0; t0=$(date +%s)
while kill -0 $pid 2>/dev/null; do
  read a f < <(awk '/^RssAnon/{a=$2}/^RssFile/{f=$2}END{print a+0, f+0}' /proc/$pid/status 2>/dev/null)
  [ "${a:-0}" -gt $pa ] && pa=$a; [ "${f:-0}" -gt $pf ] && pf=$f
  sleep 1
done
wait $pid; rc=$?
echo "rc=$rc secs=$(( $(date +%s)-t0 )) peak_anon_MiB=$((pa/1024)) peak_file_MiB=$((pf/1024))" | tee "$out"
