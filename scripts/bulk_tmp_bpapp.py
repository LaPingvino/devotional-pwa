#!/usr/bin/env python3
"""bulk_tmp_bpapp.py — Allocate TMP codes for unmatched bpapp prayers.

For weak-coverage languages (mh, gil, sv), the writings table simply
doesn't have most prayers. Mark them as TMP for deferred matching:
- INSERT a writings row carrying the bpapp text + a fresh TMP code
- INSERT a PBS row pointing to that writings row under <lang>:bpapp

Output: one SQL file per language. Caller reviews, then applies via dolt.
"""
import argparse
import csv
import json
import sys
from pathlib import Path

ROOT = Path(__file__).resolve().parent.parent

LANGS_DEFAULT = ["mh", "gil", "sv"]
SRC_BASE = 50000  # PBS source_id base; well above existing


def sql_esc(s: str) -> str:
    return (s or "").replace("'", "''")


def cache_load(lang: str, bpapp_id: str):
    p = ROOT / "bpapp_cache" / lang / f"prayer_{bpapp_id}.json"
    if not p.exists():
        return None
    try:
        return json.loads(p.read_text())
    except Exception:
        return None


def process_lang(lang: str, tmp_start: int, src_id_start: int) -> tuple[int, int]:
    """Returns (tmp_used, src_id_used)."""
    review = ROOT / f"bpapp_{lang}_review.tsv"
    if not review.exists():
        print(f"[{lang}] no review file", file=sys.stderr)
        return tmp_start, src_id_start
    out_path = ROOT / f"bpapp_{lang}_tmp.sql"
    with review.open(encoding="utf-8", errors="replace") as f, out_path.open("w") as out:
        rows = list(csv.DictReader(f, delimiter="\t"))
        out.write(f"-- Bulk TMP allocation for {lang}:bpapp residual\n")
        out.write(f"-- {len(rows)} prayers without phelps mapping in writings\n\n")
        out.write("SET FOREIGN_KEY_CHECKS=0;\n\n")
        tmp = tmp_start
        sid = src_id_start
        n = 0
        for r in rows:
            cache = cache_load(lang, r["bpapp_id"])
            if not cache or not cache.get("full_text"):
                continue
            phelps = f"TMP{tmp:05d}"
            tmp += 1
            text = cache["full_text"][:8000]
            name = cache.get("title") or ""
            cat = r["category"]
            cat_ord = 9999  # we don't have it preserved here; matcher tracks it elsewhere
            try:
                cat_ord = int(r.get("cat_order") or 9999)
            except Exception:
                pass
            ord_in_cat = 9999
            # 1. writings row carrying the actual text under the new TMP code
            out.write(
                f"INSERT IGNORE INTO writings "
                f"(source, source_id, language, phelps, name, text) VALUES "
                f"('bahaiprayers.app', '{sql_esc(r['bpapp_id'])}', '{lang}', "
                f"'{phelps}', '{sql_esc(name)}', '{sql_esc(text)}');\n"
            )
            # 2. PBS row linking the prayer to the bpapp prayerbook
            out.write(
                f"INSERT IGNORE INTO prayer_book_structure "
                f"(source_id, source_language, version, phelps_code, "
                f"category_name, category_order, order_in_category, notes) VALUES "
                f"({sid}, '{lang}:bpapp', 'bpapp', '{phelps}', '{sql_esc(cat)}', "
                f"{cat_ord}, {ord_in_cat}, 'TMP-allocated by bulk_tmp_bpapp.py');\n"
            )
            sid += 1
            n += 1
        out.write("\nSET FOREIGN_KEY_CHECKS=1;\n")
        print(f"[{lang}] wrote {out_path}: {n} TMP allocations (TMP{tmp_start:05d}..TMP{tmp-1:05d})")
        return tmp, sid


def main():
    ap = argparse.ArgumentParser()
    ap.add_argument("--langs", default=",".join(LANGS_DEFAULT))
    ap.add_argument("--tmp-start", type=int, required=True,
                    help="First free TMP number (check `MAX(SUBSTRING(phelps,4))`)")
    ap.add_argument("--src-id-start", type=int, default=SRC_BASE)
    args = ap.parse_args()
    tmp = args.tmp_start
    sid = args.src_id_start
    for L in args.langs.split(","):
        tmp, sid = process_lang(L, tmp, sid)
    print(f"\nDone. TMP range used: TMP{args.tmp_start:05d}..TMP{tmp-1:05d}")
    print(f"Source_id range: {args.src_id_start}..{sid-1}")


if __name__ == "__main__":
    main()
