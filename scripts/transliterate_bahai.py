#!/usr/bin/env python3
"""
transliterate_bahai.py — regenerate fa-translit / ar-translit texts
using Gemini's knowledge of Bahá'í prayers and the BWNS transliteration system.

For each translit entry we have:
  - The original script text (fa or ar)
  - The phelps code (identifies the prayer)

Gemini can produce proper Bahá'í romanization since it knows these prayers.

Usage:
  # Export source data first:
  dolt sql -q "SELECT w_tr.source_id, w_tr.phelps, w_orig.text
               FROM writings w_tr
               JOIN writings w_orig ON w_tr.source_id=w_orig.source_id
                 AND w_tr.source=w_orig.source
               WHERE w_tr.language='fa-translit' AND w_orig.language='fa'
               AND w_tr.phelps IS NOT NULL AND w_tr.phelps NOT LIKE 'TMP%'
               ORDER BY CAST(w_tr.source_id AS UNSIGNED)" --result-format csv > /tmp/fa_for_translit.csv

  python scripts/transliterate_bahai.py --input /tmp/fa_for_translit.csv --lang fa
  python scripts/transliterate_bahai.py --input /tmp/ar_for_translit.csv --lang ar

Outputs:
  SQL UPDATE statements to stdout
"""

import csv
import json
import re
import subprocess
import sys
import argparse
import time

GEMINI_PROMPT = """\
Answer from your existing knowledge only — do not search the web.

The following is a Bahá'í prayer in {lang_name} (Phelps code: {phelps}).
Provide a proper Bahá'í transliteration following the BWNS system:
  - Long vowels: á, í, ú
  - Emphatics: ṣ, ḍ, ṭ, ẓ, ḥ
  - kh (خ), gh (غ), sh (ش)
  - ʻayn: use ' (left single quotation mark / U+2018)
  - Hamzah: use ' (right single quotation mark / U+2019)
  - Keep line breaks, markdown headers (## or *) as-is
  - Persian: v for و, p for پ, ch for چ, zh for ژ, g for گ
  - Arabic: w for و

Return ONLY the transliteration text, nothing else.

Original {lang_name} text:
{text}"""


def call_gemini(prompt, retries=2, delay=5):
    for attempt in range(retries + 1):
        try:
            result = subprocess.run(
                ['gemini', '-m', 'gemini-2.5-flash-lite', '-p', prompt],
                capture_output=True, text=True, timeout=120
            )
            if result.returncode == 0 and result.stdout.strip():
                return result.stdout.strip()
            if attempt < retries:
                time.sleep(delay)
        except subprocess.TimeoutExpired:
            print(f" TIMEOUT", end='', file=sys.stderr)
            if attempt < retries:
                time.sleep(delay)
    return None


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument('--input', required=True, help='CSV with source_id,phelps,text columns')
    parser.add_argument('--lang', required=True, choices=['fa', 'ar'],
                        help='Source language (fa=Persian, ar=Arabic)')
    parser.add_argument('--dry-run', action='store_true')
    parser.add_argument('--start-id', type=int, default=0,
                        help='Skip source_ids below this (for resuming)')
    args = parser.parse_args()

    lang_name = 'Persian' if args.lang == 'fa' else 'Arabic'
    translit_lang = f'{args.lang}-translit'

    rows = []
    with open(args.input, newline='', encoding='utf-8') as f:
        for row in csv.DictReader(f):
            sid = int(row['source_id'])
            if sid < args.start_id:
                continue
            if not row.get('text', '').strip():
                continue
            rows.append(row)

    print(f"Processing {len(rows)} {lang_name} prayers for transliteration", file=sys.stderr)

    results = []
    errors = []

    for i, row in enumerate(rows):
        sid = row['source_id']
        phelps = row['phelps']
        text = row['text'].strip()

        print(f"  [{i+1}/{len(rows)}] source_id={sid} ({phelps})...", end=' ', file=sys.stderr)

        if args.dry_run:
            print(f"DRY RUN", file=sys.stderr)
            continue

        prompt = GEMINI_PROMPT.format(
            lang_name=lang_name,
            phelps=phelps,
            text=text[:600],  # Limit to avoid oversized prompts
        )

        translit = call_gemini(prompt)
        if not translit:
            print(f"FAILED", file=sys.stderr)
            errors.append(sid)
            time.sleep(2)
            continue

        # Basic sanity: result should be mostly Latin + diacritics, not Arabic script
        arabic_chars = len(re.findall(r'[\u0600-\u06FF]', translit))
        latin_chars = len(re.findall(r'[a-zA-Záíúāīūḥṣḍṭẓ]', translit, re.IGNORECASE))
        if arabic_chars > latin_chars * 0.5:
            print(f"WARN: result looks like Arabic script still", file=sys.stderr)
            errors.append(sid)
            continue

        print(f"OK ({len(translit)} chars)", file=sys.stderr)
        results.append((sid, phelps, translit))

        # Rate limiting
        time.sleep(1.5)

    # Output SQL
    print(f"\n-- Transliteration UPDATE statements ({lang_name} → {translit_lang})")
    print(f"-- Total: {len(results)} / {len(rows)} processed")
    print(f"-- Failed: {len(errors)}")
    print()

    for sid, phelps, translit in results:
        # Escape single quotes for SQL
        escaped = translit.replace("'", "''")
        print(f"-- source_id={sid} phelps={phelps}")
        print(f"UPDATE writings SET text='{escaped}' WHERE source_id='{sid}' "
              f"AND language='{translit_lang}' AND source='bahaiprayers.net';")
        print()

    if errors:
        print(f"-- FAILED source_ids: {', '.join(str(e) for e in errors)}")


if __name__ == '__main__':
    main()
