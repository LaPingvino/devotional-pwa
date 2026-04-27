#!/usr/bin/env python3
"""match_hidden_words.py — Map bpapp Hidden Words residuals to BH00386A##
(Arabic) or BH00113P## (Persian) phelps codes by parsing the HW number
directly from the bpapp text.

Works across all languages: numerals can be ASCII digits (1, 2, 3...) or
Arabic-Indic (٠-٩) or Persian (۰-۹) or Devanagari etc — normalized below.

The category-name → part assignment uses a per-language map; add new
markers as needed.
"""
import argparse
import csv
import json
import re
import sys
from pathlib import Path

ROOT = Path(__file__).resolve().parent.parent

# Per-language category markers → (parent_phelps, sub-letter)
# Substring match against the residual's category column.
PART_MARKERS = {
    "en":     {"From the Arabic": ("BH00386", "A"), "From the Persian": ("BH00113", "P")},
    "de":     {"Arabischen": ("BH00386", "A"), "Persischen": ("BH00113", "P")},
    "es":     {"árabe": ("BH00386", "A"), "Árabe": ("BH00386", "A"), "persa": ("BH00113", "P"), "Persa": ("BH00113", "P")},
    "fr":     {"arabe": ("BH00386", "A"), "Arabe": ("BH00386", "A"), "persan": ("BH00113", "P"), "Persan": ("BH00113", "P")},
    "it":     {"arab": ("BH00386", "A"), "Arab": ("BH00386", "A"), "persi": ("BH00113", "P"), "Persi": ("BH00113", "P")},
    "nl":     {"Arabisch": ("BH00386", "A"), "Perzisch": ("BH00113", "P")},
    "pt":     {"Árabe": ("BH00386", "A"), "Pers": ("BH00113", "P")},
    "pl":     {"arabsk": ("BH00386", "A"), "Arabsk": ("BH00386", "A"), "persk": ("BH00113", "P"), "Persk": ("BH00113", "P")},
    "ru":     {"Арабск": ("BH00386", "A"), "арабск": ("BH00386", "A"), "Персидск": ("BH00113", "P"), "персидск": ("BH00113", "P")},
    "hu":     {"arab": ("BH00386", "A"), "Arab": ("BH00386", "A"), "perzs": ("BH00113", "P"), "Perzs": ("BH00113", "P")},
    "ro":     {"arab": ("BH00386", "A"), "persan": ("BH00113", "P")},
    "fi":     {"arab": ("BH00386", "A"), "pers": ("BH00113", "P")},
    "sv":     {"arab": ("BH00386", "A"), "pers": ("BH00113", "P")},
    "ar":     {"العربية": ("BH00386", "A"), "الفارسية": ("BH00113", "P")},
    "fa":     {"عربی": ("BH00386", "A"), "فارسی": ("BH00113", "P")},
    "zh":     {"上卷": ("BH00386", "A"), "下卷": ("BH00113", "P")},
    "ja":     {"アラビア": ("BH00386", "A"), "ペルシア": ("BH00113", "P")},
    "ko":     {"아랍": ("BH00386", "A"), "페르시아": ("BH00113", "P")},
    "eo":     {"Arab": ("BH00386", "A"), "Pers": ("BH00113", "P")},
}

# Numeral systems: maps each non-ASCII digit char to its 0-9 value
DIGIT_TRANS = str.maketrans({
    # Arabic-Indic
    "٠": "0", "١": "1", "٢": "2", "٣": "3", "٤": "4",
    "٥": "5", "٦": "6", "٧": "7", "٨": "8", "٩": "9",
    # Extended Arabic-Indic (Persian/Urdu)
    "۰": "0", "۱": "1", "۲": "2", "۳": "3", "۴": "4",
    "۵": "5", "۶": "6", "۷": "7", "۸": "8", "۹": "9",
    # CJK fullwidth
    "０": "0", "１": "1", "２": "2", "３": "3", "４": "4",
    "５": "5", "６": "6", "７": "7", "８": "8", "９": "9",
})


def extract_hw_number(text):
    """Extract the HW number from a text starting with '- N -' or 'N.' or
    similar, handling non-ASCII numerals."""
    if not text:
        return None
    t = text.translate(DIGIT_TRANS)
    # Try several patterns
    patterns = [
        r"^\s*-\s*(\d+)\s*-",
        r"^\s*(\d+)\s*\.",
        r"^\s*(\d+)[\.\:、)]",
        r"^\s*\(\s*(\d+)\s*\)",
    ]
    for p in patterns:
        m = re.match(p, t)
        if m:
            return int(m.group(1))
    return None


def main():
    ap = argparse.ArgumentParser()
    ap.add_argument("--langs", required=True, help="comma-separated lang codes")
    ap.add_argument("--out", required=True)
    ap.add_argument("--src-id-start", type=int, default=70000)
    args = ap.parse_args()

    sid = args.src_id_start
    out_lines = []
    out_lines.append("-- Hidden Words by-number matches across langs")
    total = 0
    per_lang_n = {}
    for lang in args.langs.split(","):
        review = ROOT / f"bpapp_{lang}_review.tsv"
        if not review.exists():
            continue
        markers = PART_MARKERS.get(lang)
        if not markers:
            print(f"[{lang}] no PART_MARKERS — skip", file=sys.stderr)
            continue
        n_hits = 0
        with review.open(encoding="utf-8", errors="replace") as f:
            rdr = csv.DictReader(f, delimiter="\t")
            for r in rdr:
                cat = r.get("category", "")
                parent_letter = None
                for marker, info in markers.items():
                    if marker in cat:
                        parent_letter = info
                        break
                if not parent_letter:
                    continue
                parent, letter = parent_letter
                bid = r["bpapp_id"]
                cache_p = ROOT / f"bpapp_cache/{lang}" / f"prayer_{bid}.json"
                if not cache_p.exists():
                    continue
                try:
                    cache = json.loads(cache_p.read_text())
                except Exception:
                    continue
                text = cache.get("full_text") or r.get("start_text", "")
                n = extract_hw_number(text)
                if n is None:
                    continue
                phelps = f"{parent}{letter}{n:02d}"
                cat_esc = cat.replace("'", "''")
                note = f"HW#{letter}{n:02d}; bpapp_id={bid}; HW-number-direct"
                out_lines.append(
                    f"INSERT IGNORE INTO prayer_book_structure "
                    f"(source_id, source_language, version, phelps_code, "
                    f"category_name, category_order, order_in_category, notes) VALUES "
                    f"({sid}, '{lang}:bpapp', 'bpapp', '{phelps}', "
                    f"'{cat_esc}', 0, 0, '{note}');"
                )
                sid += 1
                n_hits += 1
                total += 1
        per_lang_n[lang] = n_hits
        print(f"[{lang}] {n_hits} HW matches")
    Path(args.out).write_text("\n".join(out_lines) + "\n")
    print(f"Total: {total} HW matches across {len(per_lang_n)} langs → {args.out}")


if __name__ == "__main__":
    main()
