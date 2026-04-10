#!/usr/bin/env python3
"""
Convert transliteration prayer codes (fa-translit, ar-translit).

Transliteration entries are romanized versions of Arabic/Persian prayers
that already exist in the database. They are NOT new prayers requiring
matching — they share the exact same source_id as their original-script
counterpart. The fix is a direct phelps copy via source_id JOIN.

Two cases handled:
1. source_id match with a uniquely-coded original → copy phelps directly
2. source_id matches multiple different originals (conflict) → skip, report
"""

import subprocess
import csv
import sys
from pathlib import Path

DB_DIR = Path('/home/joop/bahaiwritings')


def run_dolt(query, write=False):
    result = subprocess.run(
        ['dolt', 'sql', '-q', query] + (['-r', 'csv'] if not write else []),
        cwd=DB_DIR,
        capture_output=True,
        text=True,
    )
    if result.returncode != 0:
        print(f"SQL error: {result.stderr.strip()}", file=sys.stderr)
        return None if write else []
    if write:
        return True
    lines = result.stdout.strip().split('\n')
    return list(csv.DictReader(lines)) if len(lines) >= 2 else []


def convert_language(translit_lang, original_lang, dry_run=False):
    print(f"\n{'='*60}")
    print(f"Converting {translit_lang} → copy from {original_lang}")
    print('='*60)

    # Find unmatched transliterations that have a uniquely-coded original
    rows = run_dolt(f"""
        SELECT t.version, t.source_id,
               o.phelps AS original_phelps,
               LEFT(t.text, 80) AS translit_text,
               LEFT(o.text, 80) AS original_text
        FROM writings t
        JOIN writings o
          ON o.source_id = t.source_id
         AND o.language = '{original_lang}'
         AND o.phelps IS NOT NULL
         AND o.phelps <> ''
        WHERE t.language = '{translit_lang}'
          AND (t.phelps IS NULL OR t.phelps = '')
        GROUP BY t.version, t.source_id, o.phelps, t.text, o.text
        HAVING COUNT(DISTINCT o.phelps) = 1
    """)

    if not rows:
        print("  No convertible rows found.")
        return 0, []

    print(f"  Found {len(rows)} rows to convert.")

    conflicts = run_dolt(f"""
        SELECT t.version, t.source_id,
               GROUP_CONCAT(DISTINCT o.phelps) AS conflicting_codes
        FROM writings t
        JOIN writings o
          ON o.source_id = t.source_id
         AND o.language = '{original_lang}'
         AND o.phelps IS NOT NULL
         AND o.phelps <> ''
        WHERE t.language = '{translit_lang}'
          AND (t.phelps IS NULL OR t.phelps = '')
        GROUP BY t.version, t.source_id
        HAVING COUNT(DISTINCT o.phelps) > 1
    """)

    if conflicts:
        print(f"  Skipping {len(conflicts)} rows with conflicting originals:")
        for c in conflicts:
            print(f"    source_id={c['source_id']} has codes: {c['conflicting_codes']}")

    converted = 0
    for row in rows:
        version = row['version']
        phelps = row['original_phelps']
        if dry_run:
            print(f"  [DRY RUN] {version[:8]}... → {phelps}")
            print(f"    translit: {row['translit_text']}")
            print(f"    original: {row['original_text']}")
        else:
            ok = run_dolt(
                f"UPDATE writings SET phelps = '{phelps}' WHERE version = '{version}'",
                write=True
            )
            if ok:
                converted += 1

    return converted, conflicts


def main():
    dry_run = '--dry-run' in sys.argv

    if dry_run:
        print("DRY RUN — no changes will be made")

    total = 0
    all_conflicts = []

    for translit_lang, original_lang in [('fa-translit', 'fa'), ('ar-translit', 'ar')]:
        converted, conflicts = convert_language(translit_lang, original_lang, dry_run)
        total += converted
        all_conflicts.extend(conflicts)
        if not dry_run:
            print(f"  Converted: {converted}")

    print(f"\n{'='*60}")
    print(f"Total converted: {total}")
    if all_conflicts:
        print(f"Skipped conflicts: {len(all_conflicts)} (manual review needed)")
    print('='*60)


if __name__ == '__main__':
    main()
