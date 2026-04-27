#!/usr/bin/env python3
"""match_apbh.py — Resolve "Additional Prayers Revealed by Bahá'u'lláh"
bpapp residuals against the APBH index + inventory_export.csv.

The bpapp prayerbook has a category "Additional Prayers Revealed by
Bahá'u'lláh" (and the 'Abdu'l-Bahá analogue) full of excerpts from
longer tablets. Those excerpts aren't separate writings rows, so the
text-similarity matcher misses them. This script:
  1. Reads the APBH BH-code list (from the memory file)
  2. For each bpapp residual in that category, fetches the inventory
     entry for each candidate BH and checks whether the residual's
     opening phrase appears.
  3. Emits PBS INSERTs for confirmed matches.

Run AFTER scrape_bpapp --phase match has populated bpapp_<L>_review.tsv.
Outputs bpapp_<L>_apbh.sql for review and apply.
"""
import argparse
import csv
import json
import re
import sys
from pathlib import Path

ROOT = Path(__file__).resolve().parent.parent
INVENTORY_CSV = Path.home() / "prayermatching/data/inventory_export.csv"

# (apbh_num, base_phelps, is_excerpt). The two #5x and #6x both point
# to BH01873; we attach a 3-letter suffix from a per-prayer hint.
APBH = [
    ("01", "BH00064", True),  ("02", "BH10742", False), ("04", "BH10506", False),
    ("05", "BH01873", True),  ("06", "BH01873", True),  ("07", "BH11086", False),
    ("08", "BH10588", False), ("09", "BH10149", False), ("10", "BH01393", True),
    ("11", "BH03315", False), ("12", "BH00248", True),  ("13", "BH10328", False),
    ("14", "BH03386", True),  ("15", "BH02274", True),  ("16", "BH10889", False),
    ("17", "BH02153", True),  ("18", "BH02467", False), ("20", "BH10507", False),
    ("21", "BH11641", False), ("22", "BH02112", True),  ("23", "BH00483", True),
    ("24", "BB00633", False),
]


def normalize(s):
    s = s.lower()
    s = re.sub(r"[^\w\s]", " ", s)
    s = re.sub(r"\s+", " ", s)
    return s.strip()


def load_inventory():
    """Return dict: PIN → translated text blob (concatenated rows)."""
    by_pin = {}
    with INVENTORY_CSV.open(encoding="utf-8", errors="replace") as f:
        rdr = csv.DictReader(f)
        for row in rdr:
            pin = row.get("PIN", "")
            tr = row.get("First line (translated)", "") or ""
            if pin:
                by_pin.setdefault(pin, []).append(tr)
    return {k: " ".join(v) for k, v in by_pin.items()}


def cache_text(lang, bpapp_id):
    p = ROOT / "bpapp_cache" / lang / f"prayer_{bpapp_id}.json"
    if not p.exists():
        return ""
    try:
        return json.loads(p.read_text())["full_text"]
    except Exception:
        return ""


def process(lang, category_pattern, src_id_start):
    review = ROOT / f"bpapp_{lang}_review.tsv"
    if not review.exists():
        return 0, 0
    inv = load_inventory()
    inv_norm = {k: normalize(v) for k, v in inv.items()}
    out_path = ROOT / f"bpapp_{lang}_apbh.sql"
    n_match = 0
    rows_total = 0
    seen_phelps_per_book = {}
    with review.open(encoding="utf-8", errors="replace") as f, out_path.open("w") as out:
        rdr = csv.DictReader(f, delimiter="\t")
        out.write(f"-- APBH excerpt matches for {lang}:bpapp\n")
        out.write("SET FOREIGN_KEY_CHECKS=0;\n\n")
        sid = src_id_start
        for r in rdr:
            if not re.search(category_pattern, r["category"]):
                continue
            rows_total += 1
            text = cache_text(lang, r["bpapp_id"]) or r["start_text"]
            if not text:
                continue
            n = normalize(text)
            # Try first 80, 60, 40 chars of needle as substring in inventory entry
            best = None
            for length in (80, 60, 40):
                if len(n) < length:
                    continue
                probe = n[:length]
                for apbh_num, base_phelps, is_excerpt in APBH:
                    haystack = inv_norm.get(base_phelps, "")
                    if probe in haystack:
                        best = (apbh_num, base_phelps, is_excerpt)
                        break
                if best:
                    break
            if not best:
                continue
            apbh_num, base_phelps, is_excerpt = best
            # Avoid mapping two bpapp prayers to the same phelps within the same
            # book (PBS PK is (cat, phelps, source_lang)). Skip duplicates.
            key = (lang, r["category"], base_phelps)
            if key in seen_phelps_per_book:
                # would conflict; comment instead
                out.write(f"-- SKIP duplicate: bpapp_id={r['bpapp_id']} also maps to {base_phelps} (already used)\n")
                continue
            seen_phelps_per_book[key] = True
            cat_ord, ord_in_cat = 0, 0
            try: cat_ord = int(r.get("cat_order") or 0)
            except: pass
            try: ord_in_cat = int(r.get("ord_in_cat") or 0)
            except: pass
            note = f"APBH#{apbh_num}" + (" excerpt" if is_excerpt else "") + f"; bpapp_id={r['bpapp_id']}"
            out.write(
                f"INSERT IGNORE INTO prayer_book_structure "
                f"(source_id, source_language, version, phelps_code, "
                f"category_name, category_order, order_in_category, notes) VALUES "
                f"({sid}, '{lang}:bpapp', 'bpapp', '{base_phelps}', "
                f"'{r['category'].replace(chr(39), chr(39)+chr(39))}', "
                f"{cat_ord}, {ord_in_cat}, '{note}');\n"
            )
            sid += 1
            n_match += 1
        out.write("\nSET FOREIGN_KEY_CHECKS=1;\n")
    print(f"[{lang}] {n_match}/{rows_total} APBH matches → {out_path.name}")
    return n_match, sid


def main():
    ap = argparse.ArgumentParser()
    ap.add_argument("--langs", required=True, help="comma-separated lang codes")
    ap.add_argument("--src-id-start", type=int, default=60000)
    ap.add_argument("--category", default=r"Additional Prayers Revealed by Bah",
                    help="regex matched against the review TSV's category column")
    args = ap.parse_args()
    sid = args.src_id_start
    total = 0
    for L in args.langs.split(","):
        n, sid = process(L, args.category, sid)
        total += n
    print(f"Total: {total} APBH matches across {args.langs}")


if __name__ == "__main__":
    main()
