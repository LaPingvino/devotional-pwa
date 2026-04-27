#!/usr/bin/env python3
"""remap_tmp_hw.py — Move TMP-coded Hidden Words rows to their canonical
BH00386A## (Arabic) or BH00113P## (Persian) phelps codes.

For each TMP-coded PBS row in a Hidden Words category, parses the HW
number from the writings text and emits UPDATE statements for both the
writings row (phelps) and the PBS row (phelps_code).

Categories per language are matched by substring against PART_MARKERS
(extends scripts/match_hidden_words.py with mh/gil/es/sv markers).
"""
import argparse
import json
import re
import subprocess
import sys
from pathlib import Path

DOLT_DIR = Path.home() / "bahaiwritings"

PART_MARKERS = {
    "en": {"From the Arabic": ("BH00386", "A"), "From the Persian": ("BH00113", "P")},
    "de": {"Arabischen": ("BH00386", "A"), "Persischen": ("BH00113", "P")},
    "es": {"árabe": ("BH00386", "A"), "Árabe": ("BH00386", "A"),
           "Parte I": ("BH00386", "A"), "persa": ("BH00113", "P"),
           "Parte II": ("BH00113", "P"), "Persa": ("BH00113", "P")},
    "fr": {"arabe": ("BH00386", "A"), "Arabe": ("BH00386", "A"),
           "persan": ("BH00113", "P"), "Persan": ("BH00113", "P")},
    "it": {"arab": ("BH00386", "A"), "Arab": ("BH00386", "A"),
           "persi": ("BH00113", "P"), "Persi": ("BH00113", "P")},
    "nl": {"Arabisch": ("BH00386", "A"), "Perzisch": ("BH00113", "P")},
    "pt": {"Árabe": ("BH00386", "A"), "Pers": ("BH00113", "P")},
    "pl": {"arabsk": ("BH00386", "A"), "Arabsk": ("BH00386", "A"),
           "persk": ("BH00113", "P"), "Persk": ("BH00113", "P")},
    "ru": {"Арабск": ("BH00386", "A"), "арабск": ("BH00386", "A"),
           "Персидск": ("BH00113", "P"), "персидск": ("BH00113", "P")},
    "hu": {"arab": ("BH00386", "A"), "Arab": ("BH00386", "A"),
           "perzs": ("BH00113", "P"), "Perzs": ("BH00113", "P")},
    "ro": {"arab": ("BH00386", "A"), "persan": ("BH00113", "P")},
    "fi": {"arab": ("BH00386", "A"), "pers": ("BH00113", "P")},
    "sv": {"arab": ("BH00386", "A"), "Arab": ("BH00386", "A"),
           "pers": ("BH00113", "P"), "Pers": ("BH00113", "P")},
    "ar": {"العربية": ("BH00386", "A"), "الفارسية": ("BH00113", "P")},
    "fa": {"عربی": ("BH00386", "A"), "فارسی": ("BH00113", "P")},
    "zh": {"上卷": ("BH00386", "A"), "下卷": ("BH00113", "P")},
    "ja": {"アラビア": ("BH00386", "A"), "ペルシア": ("BH00113", "P")},
    "ko": {"아랍": ("BH00386", "A"), "페르시아": ("BH00113", "P")},
    "eo": {"Arab": ("BH00386", "A"), "Pers": ("BH00113", "P")},
    "mh": {"kajin Arabic": ("BH00386", "A"), "Jen Kajin Persia": ("BH00113", "P"),
           "Kajin Arabic": ("BH00386", "A"), "Kajin Persia": ("BH00113", "P")},
    "gil": {"Man te Arabic": ("BH00386", "A"), "man te Persian": ("BH00113", "P"),
            "te Arabic": ("BH00386", "A"), "te Persian": ("BH00113", "P")},
}

DIGIT_TRANS = str.maketrans({
    "٠": "0", "١": "1", "٢": "2", "٣": "3", "٤": "4",
    "٥": "5", "٦": "6", "٧": "7", "٨": "8", "٩": "9",
    "۰": "0", "۱": "1", "۲": "2", "۳": "3", "۴": "4",
    "۵": "5", "۶": "6", "۷": "7", "۸": "8", "۹": "9",
    "０": "0", "１": "1", "２": "2", "３": "3", "４": "4",
    "５": "5", "６": "6", "７": "7", "８": "8", "９": "9",
})


def extract_hw_number(text):
    if not text:
        return None
    # strip HTML tags
    t = re.sub(r"<[^>]+>", "", text)
    t = t.translate(DIGIT_TRANS)
    for p in (r"^\s*-\s*(\d+)\s*-", r"^\s*(\d+)\s*\.", r"^\s*(\d+)[\.\:、)]"):
        m = re.match(p, t)
        if m:
            return int(m.group(1))
    return None


def dolt_query(sql):
    r = subprocess.run(["dolt", "sql", "-r", "json", "-q", sql],
                       capture_output=True, text=True, cwd=str(DOLT_DIR), timeout=120)
    if r.returncode != 0:
        return []
    out = json.loads(r.stdout) if r.stdout.strip() else {}
    return out.get("rows", [])


def main():
    ap = argparse.ArgumentParser()
    ap.add_argument("--langs", required=True)
    ap.add_argument("--out", required=True)
    args = ap.parse_args()

    out = open(args.out, "w")
    out.write("-- Remap TMP-coded HW rows to canonical BH00386A##/BH00113P##\n\n")
    total = 0
    per_lang = {}
    skipped = []
    for L in args.langs.split(","):
        markers = PART_MARKERS.get(L)
        if not markers:
            print(f"[{L}] no markers", file=sys.stderr)
            continue
        # Build category WHERE clause
        cat_clauses = " OR ".join(f"pbs.category_name LIKE '%{m.replace(chr(39), chr(39)+chr(39))}%'"
                                   for m in markers)
        sql = (f"SELECT pbs.phelps_code, pbs.category_name, w.text "
               f"FROM prayer_book_structure pbs "
               f"JOIN writings w ON pbs.phelps_code=w.phelps AND w.language='{L}' "
               f"WHERE pbs.source_language='{L}:bpapp' "
               f"AND pbs.phelps_code LIKE 'TMP%' "
               f"AND ({cat_clauses})")
        rows = dolt_query(sql)
        n = 0
        seen_targets = set()
        for r in rows:
            old_phelps = r.get("phelps_code", "")
            cat = r.get("category_name", "")
            text = r.get("text", "")
            # find matching marker
            parent_letter = None
            for marker, info in markers.items():
                if marker in cat:
                    parent_letter = info
                    break
            if not parent_letter:
                continue
            parent, letter = parent_letter
            n_hw = extract_hw_number(text)
            if n_hw is None:
                skipped.append((L, old_phelps, cat[:30], text[:60]))
                continue
            target = f"{parent}{letter}{n_hw:02d}"
            if target in seen_targets:
                # Duplicate target — skip to avoid PBS PK conflict
                skipped.append((L, old_phelps, f"DUP {target}", cat[:40]))
                continue
            seen_targets.add(target)
            out.write(f"-- {L}: {old_phelps} -> {target} ({cat[:40]})\n")
            out.write(f"UPDATE writings SET phelps='{target}' "
                      f"WHERE phelps='{old_phelps}' AND language='{L}';\n")
            cat_esc = cat.replace("'", "''")
            out.write(f"UPDATE prayer_book_structure SET phelps_code='{target}', "
                      f"notes='HW#{letter}{n_hw:02d}; remapped from {old_phelps}' "
                      f"WHERE source_language='{L}:bpapp' AND phelps_code='{old_phelps}';\n")
            n += 1
            total += 1
        per_lang[L] = n
        print(f"[{L}] {n} remaps")
    out.close()
    if skipped:
        print(f"\nSkipped {len(skipped)} (no HW# found or duplicate target):")
        for s in skipped[:8]:
            print(f"  {s}")
    print(f"\nTotal: {total} TMP→HW remaps")


if __name__ == "__main__":
    main()
