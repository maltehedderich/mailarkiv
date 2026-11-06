"""Print the benchmark's query tables as Markdown, or fill @@name@@ markers in a file.

Usage: uv run --no-project report.py RESULTS_DIR [FILE_TO_FILL]
"""

import json
import pathlib
import sys

LABELS = {
    "unique_token": "Unique token",
    "rare_0.01pct": "Rare word, in 0.01% of mail",
    "mid_1pct": "Less common word, in 1%",
    "common_60pct": "Common word, in about 40%",
    "and_common_mid": "Common and less common word",
    "prefix_4char": "4-character prefix",
    "prefix_2char": "2-character prefix",
    "typed_term_term_prefix": "Two words and a 3-character prefix",
    "phrase": "Phrase",
    "sender_90days": "Sender within 90 days",
    "mid_1year": "Less common word within one year",
    "attachment_name": "Attachment filename",
    "mid_1pct_bm25": "Less common word, by relevance",
    "common_60pct_bm25": "Common word, by relevance",
    "and_common_mid_bm25": "Both words, by relevance",
    "count_common": "Count of a common word",
}


def load(path):
    rows = {}
    for line in path.read_text().splitlines():
        if not line.startswith("{"):
            continue
        try:
            row = json.loads(line)
        except ValueError:
            continue  # The Rust harness prints a few lines that aren't strict JSON.
        if "class" in row:
            rows[row["class"]] = row
    return rows


def number(value):
    if value < 1:
        return f"{value:.2f}"
    if value < 10:
        return f"{value:.1f}"
    return f"{value:.0f}"


def cell(row):
    star = "" if row["n"] in (20, 100) else "\\*"
    return f"{number(row['p50_ms'])} / {number(row['p95_ms'])}{star}"


def table(results, columns, hits_from):
    loaded = [(name, load(results / f"{stem}.jsonl")) for name, stem in columns]
    hits = load(results / f"{hits_from}.jsonl")
    names = [name for name, _ in loaded]
    lines = [
        "| Query | Hits | " + " | ".join(names) + " |",
        "| --- | --- | " + " | ".join("---" for _ in names) + " |",
    ]
    for key, label in LABELS.items():
        cells = [cell(rows[key]) for _, rows in loaded]
        lines.append(f"| {label} | {hits[key]['median_hits']:,} | " + " | ".join(cells) + " |")
    return "\n".join(lines)


def main():
    results = pathlib.Path(sys.argv[1])
    tables = {
        "1m-warm": table(results, [("Bleve", "1m-bleve-warm"), ("FTS5", "1m-fts-warm"), ("Tantivy", "1m-tantivy-warm")], "1m-tantivy-warm"),
        "1m-fresh": table(results, [("Bleve", "1m-bleve-osw"), ("FTS5", "1m-fts-osw"), ("Tantivy", "1m-tantivy-osw")], "1m-tantivy-osw"),
        "1m-cold": table(results, [("Bleve", "1m-bleve-cold"), ("FTS5", "1m-fts-cold"), ("Tantivy", "1m-tantivy-cold")], "1m-tantivy-cold"),
        "5m": table(results, [("Cold", "5m-tantivy-cold"), ("Fresh process", "5m-tantivy-osw"), ("Warm", "5m-tantivy-warm")], "5m-tantivy-warm"),
        "200k": table(results, [("Bleve", "200k-bleve"), ("FTS5 with prefix indexes", "200k-fts"), ("Tantivy, glibc", "200k-tantivy"), ("Tantivy, static", "200k-tantivy-mi")], "200k-tantivy"),
    }
    if len(sys.argv) > 2:
        target = pathlib.Path(sys.argv[2])
        text = target.read_text()
        for name, body in tables.items():
            text = text.replace(f"@@{name}@@", body)
        target.write_text(text)
    else:
        for name, body in tables.items():
            print(f"## {name}\n\n{body}\n")


main()
