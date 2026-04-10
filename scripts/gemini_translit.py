#!/usr/bin/env python3
"""
gemini_translit.py — convert unidecode-based ar/fa translit to proper Bahá'í romanization.

Uses Gemini to refine the unidecode ASCII approximations into standard Bahá'í
transliteration with correct diacritical marks (ā, ū, ī, ḥ, ṭ, ẓ, ṣ, ḍ, etc.).

Usage:
  python3 scripts/gemini_translit.py [--lang ar-translit|fa-translit|all] [--dry-run] [--batch-size 4]
"""

import subprocess
import json
import sys
import argparse
import tempfile
import os
import time
import re

DOLT_DIR = "/home/joop/prayermatching/bahaiwritings"

GEMINI_PROMPT = """\
Answer from your existing knowledge only — do not search the web.

Below are Bahá'í {source_lang} prayer texts with their rough unidecode ASCII approximations.
Convert each to standard Bahá'í academic transliteration using:
  - Long vowels: ā (long a), ū (long u), ī (long i)
  - Emphatic consonants: ḥ ṭ ẓ ṣ ḍ
  - ʻayn: ʻ (left single quote)  — for Arabic ع
  - Hamza: ' (apostrophe) — for Arabic ء
  - kh, sh, gh for خ ش غ
  - th, dh for ث ذ
  - Persian: p, ch, zh, g for پ چ ژ گ
  - Use standard Bahá'í spellings: Iláhí, Illáh, Raḥím, etc. where recognizable

For each prayer, return the FULL transliteration of the source text (not just the opening).
Keep line breaks and paragraph structure. Preserve any #/*/! markers at start of lines.

Return ONLY a JSON array, no markdown fences:
[{{"id":"SOURCE_ID","translit":"full transliterated text"}}]

Prayers (source | unidecode hint):
{prayers}"""


def dolt_query(sql, as_json=True):
    fmt = ["--result-format", "json"] if as_json else []
    with tempfile.NamedTemporaryFile(mode='w', suffix='.sql', delete=False) as f:
        f.write(sql)
        fname = f.name
    try:
        result = subprocess.run(
            ["dolt", "sql"] + fmt + ["--file", fname],
            capture_output=True, text=True, cwd=DOLT_DIR
        )
    finally:
        os.unlink(fname)
    if result.returncode != 0:
        print(f"SQL error: {result.stderr}", file=sys.stderr)
        return None
    if as_json and result.stdout.strip():
        return json.loads(result.stdout)
    return result.stdout


def escape_sql(s):
    return s.replace("'", "''").replace("\\", "\\\\")


def call_gemini(prompt, retries=2, delay=5):
    for attempt in range(retries + 1):
        try:
            result = subprocess.run(
                ['gemini', '-m', 'gemini-2.5-flash-lite', '-p', prompt],
                capture_output=True, text=True, timeout=180
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


def parse_json_response(text):
    """Extract JSON array from Gemini response."""
    text = text.strip()
    # Strip markdown fences if present
    text = re.sub(r'^```(?:json)?\s*', '', text, flags=re.MULTILINE)
    text = re.sub(r'```\s*$', '', text, flags=re.MULTILINE)
    text = text.strip()
    try:
        return json.loads(text)
    except json.JSONDecodeError:
        # Try to find array within text
        m = re.search(r'\[.*\]', text, re.DOTALL)
        if m:
            try:
                return json.loads(m.group())
            except json.JSONDecodeError:
                pass
    return None


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument('--lang', default='all',
                        help='ar-translit, fa-translit, or all')
    parser.add_argument('--dry-run', action='store_true')
    parser.add_argument('--batch-size', type=int, default=4)
    args = parser.parse_args()

    langs = ['ar-translit', 'fa-translit'] if args.lang == 'all' else [args.lang]

    for lang in langs:
        source_lang = lang.replace('-translit', '')
        lang_name = 'Arabic' if source_lang == 'ar' else 'Persian (Farsi)'

        print(f"\n=== {lang} ===", file=sys.stderr)

        # Fetch source text + current translit text + version
        rows = dolt_query(
            f"SELECT src.source_id, src.text as src_text, tr.text as tr_text, tr.version "
            f"FROM writings src "
            f"JOIN writings tr ON tr.source_id = src.source_id "
            f"  AND tr.source = src.source AND tr.language = '{lang}' "
            f"WHERE src.source = 'bahaiprayers.net' "
            f"  AND src.language = '{source_lang}' "
            f"  AND src.text IS NOT NULL AND src.text <> '' "
            f"  AND tr.text IS NOT NULL AND tr.text <> '' "
            f"ORDER BY CAST(src.source_id AS UNSIGNED)"
        )
        if not rows:
            print(f"No rows found for {lang}", file=sys.stderr)
            continue

        pairs = rows.get("rows", [])
        print(f"Found {len(pairs)} {lang} entries", file=sys.stderr)

        # Split into batches
        batches = [pairs[i:i+args.batch_size] for i in range(0, len(pairs), args.batch_size)]
        updates = 0
        errors = 0

        for bi, batch in enumerate(batches, 1):
            print(f"  Batch {bi}/{len(batches)}...", end='', file=sys.stderr)

            # Format batch: source text | unidecode hint (first 120 chars each)
            prayer_lines = []
            for item in batch:
                sid = item['source_id']
                src = (item.get('src_text') or '').strip()[:300]
                hint = (item.get('tr_text') or '').strip()[:200]
                prayer_lines.append(f"[id={sid}]\nSource: {src}\nUnidecode: {hint}")

            prayers_text = '\n\n'.join(prayer_lines)
            prompt = GEMINI_PROMPT.format(
                source_lang=lang_name,
                prayers=prayers_text
            )

            if args.dry_run:
                print(f" (dry-run, {len(batch)} items)", file=sys.stderr)
                for item in batch:
                    print(f"  id={item['source_id']}: {item.get('src_text','')[:50]!r}")
                continue

            raw = call_gemini(prompt)
            if not raw:
                print(f" FAILED", file=sys.stderr)
                errors += len(batch)
                continue

            parsed = parse_json_response(raw)
            if not parsed:
                print(f" NO JSON", file=sys.stderr)
                errors += len(batch)
                continue

            # Build a version lookup from this batch
            version_map = {item['source_id']: item['version'] for item in batch}

            batch_updates = 0
            for entry in parsed:
                sid = str(entry.get('id', ''))
                translit = (entry.get('translit') or '').strip()
                if not sid or not translit:
                    continue
                version = version_map.get(sid)
                if not version:
                    continue
                sql = (f"UPDATE writings SET text='{escape_sql(translit)}' "
                       f"WHERE version='{escape_sql(version)}'")
                dolt_query(sql, as_json=False)
                batch_updates += 1
                print(f"-- {lang}/{sid}: {translit[:60]!r}")

            updates += batch_updates
            print(f" updated={batch_updates}", file=sys.stderr)

        print(f"\n{lang}: Total updated={updates}, errors={errors}", file=sys.stderr)

    if not args.dry_run:
        print("\nDone. Run: dolt add writings && dolt commit -m 'Refine ar/fa translit via Gemini'")


if __name__ == "__main__":
    main()
