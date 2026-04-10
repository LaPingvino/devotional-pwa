#!/usr/bin/env python3
"""
regenerate_translit.py — regenerate broken ar-translit / fa-translit texts
using unidecode as a mechanical fallback.

The existing translit texts in the DB were imported from broken data.
This script replaces them with unidecode-based ASCII approximations
of the original Arabic/Persian source texts.

Usage:
  python3 scripts/regenerate_translit.py [--lang ar-translit|fa-translit] [--dry-run]
"""

import subprocess
import json
import sys
import argparse
import tempfile
import os
from unidecode import unidecode

DOLT_DIR = "/home/joop/prayermatching/bahaiwritings"


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


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument('--lang', default='all',
                        help='Language to regenerate: ar-translit, fa-translit, or all')
    parser.add_argument('--dry-run', action='store_true')
    args = parser.parse_args()

    langs = ['ar-translit', 'fa-translit'] if args.lang == 'all' else [args.lang]

    for lang in langs:
        # Get the source language (ar or fa)
        source_lang = lang.replace('-translit', '')

        print(f"\n=== {lang} ===")

        # Fetch all source-language rows paired with their translit version
        rows = dolt_query(
            f"SELECT src.source_id, src.text as src_text, tr.text as tr_text, tr.version "
            f"FROM writings src "
            f"JOIN writings tr ON tr.source_id = src.source_id "
            f"  AND tr.source = src.source AND tr.language = '{lang}' "
            f"WHERE src.source = 'bahaiprayers.net' "
            f"  AND src.language = '{source_lang}' "
            f"  AND src.text IS NOT NULL AND src.text <> '' "
            f"ORDER BY CAST(src.source_id AS UNSIGNED)"
        )
        if not rows:
            print(f"No rows found for {lang}")
            continue

        pairs = rows.get("rows", [])
        print(f"Found {len(pairs)} {lang} entries")

        updates = 0
        skipped = 0
        for row in pairs:
            src_text = row.get("src_text", "") or ""
            tr_text = row.get("tr_text", "") or ""
            version = row.get("version", "")
            source_id = row.get("source_id", "")

            if not src_text.strip():
                skipped += 1
                continue

            new_translit = unidecode(src_text).strip()

            if not new_translit:
                skipped += 1
                continue

            if args.dry_run:
                print(f"  id={source_id}: {src_text[:60]!r} → {new_translit[:60]!r}")
                updates += 1
                continue

            sql = (f"UPDATE writings SET text='{escape_sql(new_translit)}' "
                   f"WHERE version='{escape_sql(version)}'")
            dolt_query(sql, as_json=False)
            updates += 1

        print(f"  Updated: {updates}, Skipped: {skipped}")

    if not args.dry_run and updates > 0:
        print("\nDone. Run: cd ~/prayermatching/bahaiwritings && dolt add writings && dolt commit -m 'Regenerate ar/fa translit via unidecode'")


if __name__ == "__main__":
    main()
