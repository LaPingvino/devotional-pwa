#!/usr/bin/env python3
"""bulk_tmp_unmapped.py — Allocate TMP codes (TMPNNNN, 4-digit) for every
bpapp residual that doesn't yet have a PBS row.

Improvements over bulk_tmp_bpapp.py:
- Uses canonical 4-digit TMP format (TMPNNNN, total 7 chars), matching
  the standardized format in writings.
- Handles all langs in one pass.
- Skips bpapp_ids that already have a PBS row pointing to a valid phelps.
- Generates BOTH writings INSERT (with bpapp text + new TMP) and PBS INSERT.
"""
import argparse
import csv
import json
import re
import subprocess
import sys
import uuid
from pathlib import Path

ROOT = Path(__file__).resolve().parent.parent
DOLT_DIR = Path.home() / "bahaiwritings"


def dolt_query(sql):
    r = subprocess.run(["dolt", "sql", "-r", "json", "-q", sql],
                       capture_output=True, text=True, cwd=str(DOLT_DIR), timeout=120)
    if r.returncode != 0:
        return []
    out = json.loads(r.stdout) if r.stdout.strip() else {}
    return out.get("rows", [])


def sql_esc(s):
    return (s or "").replace("'", "''")


def fix_dropcap(text):
    if not text:
        return text
    return re.sub(r"^([IOA])([a-z])", r"\1 \2", text)


def main():
    ap = argparse.ArgumentParser()
    ap.add_argument("--langs", required=True, help="comma-separated lang codes (or 'all')")
    ap.add_argument("--out", required=True)
    ap.add_argument("--src-id-start", type=int, default=80000)
    args = ap.parse_args()

    # 1. Find next free TMP number
    rows = dolt_query("SELECT MAX(CAST(SUBSTRING(phelps,4) AS UNSIGNED)) AS n "
                      "FROM writings WHERE phelps REGEXP '^TMP[0-9]+$'")
    next_tmp = (rows[0].get("n", 0) if rows else 0) + 1
    print(f"Starting at TMP{next_tmp:04d}")

    # 2. Find which bpapp_ids already have PBS rows (per source_language)
    rows = dolt_query("SELECT source_language, notes FROM prayer_book_structure "
                      "WHERE source_language LIKE '%:bpapp' AND notes LIKE '%bpapp_id=%'")
    mapped = {}  # source_lang → set of bpapp_ids
    for r in rows:
        sl = r.get("source_language", "")
        m = re.search(r"bpapp_id=(\d+)", r.get("notes", "") or "")
        if m:
            mapped.setdefault(sl, set()).add(m.group(1))

    # 3. Resolve langs
    if args.langs == "all":
        langs = sorted({p.name.replace("bpapp_", "").replace("_review.tsv", "")
                        for p in ROOT.glob("bpapp_*_review.tsv")})
    else:
        langs = args.langs.split(",")

    # 4. For each lang, walk review and emit SQL for unmapped
    sid = args.src_id_start
    tmp_n = next_tmp
    out = open(args.out, "w")
    out.write("-- Bulk TMP allocation for unmapped bpapp residuals\n")
    out.write(f"-- Starting at TMP{next_tmp:04d}\n\n")
    total = 0
    per_lang = {}
    for L in langs:
        review = ROOT / f"bpapp_{L}_review.tsv"
        if not review.exists():
            continue
        already = mapped.get(f"{L}:bpapp", set())
        n = 0
        with review.open(encoding="utf-8", errors="replace") as f:
            rdr = csv.DictReader(f, delimiter="\t")
            for r in rdr:
                bid = r["bpapp_id"]
                if bid in already:
                    continue
                cache_p = ROOT / f"bpapp_cache/{L}" / f"prayer_{bid}.json"
                if not cache_p.exists():
                    continue
                try:
                    cache = json.loads(cache_p.read_text())
                except Exception:
                    continue
                text = fix_dropcap(cache.get("full_text", ""))[:8000]
                if not text:
                    continue
                name = cache.get("title", "") or ""
                cat = r.get("category", "")
                phelps = f"TMP{tmp_n:04d}"
                tmp_n += 1
                ver = str(uuid.uuid4())
                out.write(
                    f"INSERT IGNORE INTO writings "
                    f"(version, source, source_id, language, phelps, name, text) VALUES "
                    f"('{ver}', 'bahaiprayers.app', '{sql_esc(bid)}', '{L}', "
                    f"'{phelps}', '{sql_esc(name)}', '{sql_esc(text)}');\n"
                )
                cat_esc = sql_esc(cat)
                note = f"TMP-allocated by bulk_tmp_unmapped; bpapp_id={bid}"
                out.write(
                    f"INSERT IGNORE INTO prayer_book_structure "
                    f"(source_id, source_language, version, phelps_code, "
                    f"category_name, category_order, order_in_category, notes) VALUES "
                    f"({sid}, '{L}:bpapp', 'bpapp', '{phelps}', "
                    f"'{cat_esc}', 0, 0, '{note}');\n"
                )
                sid += 1
                n += 1
                total += 1
        per_lang[L] = n
        print(f"[{L}] {n} TMP allocations")
    out.close()
    print(f"\nTotal: {total} TMP allocations across {len(per_lang)} langs")
    print(f"TMP range: TMP{next_tmp:04d}..TMP{tmp_n-1:04d}")
    print(f"Output: {args.out}")


if __name__ == "__main__":
    main()
