#!/usr/bin/env python3
"""
Match transliteration prayers (fa-translit, ar-translit) to their originals (fa, ar).

Strategy:
- Transliterations are romanized versions of Arabic/Persian scripts
- They should match 1:1 with their original script versions
- Use order-based matching first (if sources are consistent)
- Use Gemini for verification/disambiguation
"""

import subprocess
import csv
import json
import time
from pathlib import Path

DATA_DIR = Path('/home/joop/prayermatching/data')
DB_DIR = Path('/home/joop/prayermatching/bahaiwritings')


def run_dolt_query(query):
    """Execute Dolt query and return results."""
    result = subprocess.run(
        ['dolt', 'sql', '-q', query, '-r', 'csv'],
        cwd=DB_DIR,
        capture_output=True,
        text=True
    )
    if result.returncode != 0:
        print(f"Query error: {result.stderr}")
        return []

    lines = result.stdout.strip().split('\n')
    if len(lines) < 2:
        return []

    return list(csv.DictReader(lines))


def call_gemini(prompt):
    """Call Gemini CLI."""
    try:
        result = subprocess.run(
            ['gemini'],
            input=prompt,
            capture_output=True,
            text=True,
            timeout=60
        )
        if result.returncode == 0:
            lines = [l.strip() for l in result.stdout.strip().split('\n') if l.strip()]
            return lines[-1] if lines else None
    except Exception as e:
        print(f"Gemini error: {e}")
    return None


def main():
    print("=" * 70)
    print("Transliteration Prayer Matching")
    print("=" * 70)

    # Get transliteration prayers
    print("\nFetching transliteration prayers...")

    fa_translit = run_dolt_query("""
        SELECT version, phelps, LEFT(text, 300) as text
        FROM writings
        WHERE language = 'fa-translit'
        ORDER BY version
    """)
    print(f"  fa-translit: {len(fa_translit)} prayers")

    ar_translit = run_dolt_query("""
        SELECT version, phelps, LEFT(text, 300) as text
        FROM writings
        WHERE language = 'ar-translit'
        ORDER BY version
    """)
    print(f"  ar-translit: {len(ar_translit)} prayers")

    # Get original prayers with phelps codes
    print("\nFetching original prayers...")

    fa_originals = run_dolt_query("""
        SELECT version, phelps, LEFT(text, 300) as text
        FROM writings
        WHERE language = 'fa' AND phelps IS NOT NULL AND phelps != ''
        ORDER BY version
    """)
    print(f"  fa originals with phelps: {len(fa_originals)} prayers")

    ar_originals = run_dolt_query("""
        SELECT version, phelps, LEFT(text, 300) as text
        FROM writings
        WHERE language = 'ar' AND phelps IS NOT NULL AND phelps != ''
        ORDER BY version
    """)
    print(f"  ar originals with phelps: {len(ar_originals)} prayers")

    # Check counts
    print("\n" + "=" * 70)
    print("ANALYSIS")
    print("=" * 70)

    fa_unmatched = [p for p in fa_translit if not p['phelps'] or p['phelps'] == '']
    ar_unmatched = [p for p in ar_translit if not p['phelps'] or p['phelps'] == '']

    print(f"fa-translit unmatched: {len(fa_unmatched)} / {len(fa_translit)}")
    print(f"ar-translit unmatched: {len(ar_unmatched)} / {len(ar_translit)}")

    if len(fa_translit) == len(fa_originals):
        print("\n✅ fa-translit count matches fa originals - likely 1:1 correspondence")
    else:
        print(f"\n⚠️ Count mismatch: {len(fa_translit)} translit vs {len(fa_originals)} originals")

    if len(ar_translit) == len(ar_originals):
        print("✅ ar-translit count matches ar originals - likely 1:1 correspondence")
    else:
        print(f"⚠️ Count mismatch: {len(ar_translit)} translit vs {len(ar_originals)} originals")

    # For this phase, we'll use Gemini to match transliterations to originals
    # The transliteration is a romanization, so we need semantic matching

    print("\n" + "=" * 70)
    print("MATCHING STRATEGY")
    print("=" * 70)
    print("""
Since transliterations are romanized versions of Arabic/Persian:
1. We cannot do simple text matching
2. We need to use the ORDER of prayers if sources are consistent
3. Or use Gemini to match transliteration to original script

For now, let's check if the transliterations already have phelps codes
that might help us understand the structure.
""")

    # Check what phelps codes exist in transliterations
    fa_translit_with_phelps = [p for p in fa_translit if p['phelps'] and p['phelps'] != '']
    ar_translit_with_phelps = [p for p in ar_translit if p['phelps'] and p['phelps'] != '']

    print(f"fa-translit with phelps: {len(fa_translit_with_phelps)}")
    print(f"ar-translit with phelps: {len(ar_translit_with_phelps)}")

    if fa_translit_with_phelps:
        print("\nSample fa-translit with phelps:")
        for p in fa_translit_with_phelps[:3]:
            print(f"  {p['phelps']}: {p['text'][:80]}...")

    # Save analysis for later processing
    analysis = {
        'fa_translit_total': len(fa_translit),
        'fa_translit_unmatched': len(fa_unmatched),
        'ar_translit_total': len(ar_translit),
        'ar_translit_unmatched': len(ar_unmatched),
        'fa_originals': len(fa_originals),
        'ar_originals': len(ar_originals),
    }

    with open(DATA_DIR / 'transliteration_analysis.json', 'w') as f:
        json.dump(analysis, f, indent=2)

    print(f"\nAnalysis saved to {DATA_DIR / 'transliteration_analysis.json'}")


if __name__ == '__main__':
    main()
