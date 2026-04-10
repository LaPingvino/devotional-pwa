#!/usr/bin/env python3
"""
Resolve TMP codes using Gemini LLM for semantic matching.

Strategy:
1. For each TMP prayer, get top 3 candidate PINs based on word overlap
2. Ask Gemini to verify if any candidate is a match
3. If yes, record the match; if uncertain/no, flag for manual review
"""

import subprocess
import json
import csv
import re
import sys
import time
from pathlib import Path
from collections import defaultdict

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
        print(f"Query error: {result.stderr}", file=sys.stderr)
        return []

    lines = result.stdout.strip().split('\n')
    if len(lines) < 2:
        return []

    reader = csv.DictReader(lines)
    return list(reader)


def call_gemini(prompt, retries=2):
    """Call Gemini CLI and return response."""
    for attempt in range(retries + 1):
        try:
            result = subprocess.run(
                ['gemini'],
                input=prompt,
                capture_output=True,
                text=True,
                timeout=90
            )
            if result.returncode == 0:
                # Get last non-empty line (the actual response)
                lines = [l.strip() for l in result.stdout.strip().split('\n') if l.strip()]
                return lines[-1] if lines else None
            else:
                print(f"  Gemini returned code {result.returncode}", file=sys.stderr)
        except subprocess.TimeoutExpired:
            print(f"  Gemini timeout (attempt {attempt + 1})", file=sys.stderr)
        except Exception as e:
            print(f"  Gemini error: {e}", file=sys.stderr)

        if attempt < retries:
            time.sleep(2)

    return None


def get_candidates_for_prayer(prayer_text, lang, inventory, top_n=3):
    """Get top candidate PINs using word overlap."""

    # Normalize prayer text
    text = prayer_text.lower()
    text = re.sub(r'[^\w\s]', ' ', text)
    words = set(w for w in text.split() if len(w) > 3)

    # Score each inventory entry
    scores = []
    field = 'first_translated' if lang == 'en' else 'first_original'

    for pin, entry in inventory.items():
        inv_text = entry.get(field, '').lower()
        inv_text = re.sub(r'[^\w\s]', ' ', inv_text)
        inv_words = set(w for w in inv_text.split() if len(w) > 3)

        if not inv_words:
            continue

        overlap = len(words & inv_words)
        if overlap > 0:
            scores.append((pin, overlap, entry.get(field, '')))

    scores.sort(key=lambda x: x[1], reverse=True)
    return scores[:top_n]


def verify_match_with_gemini(prayer_text, candidates, lang):
    """Ask Gemini to verify if any candidate matches the prayer."""

    if not candidates:
        return None, "NO_CANDIDATES"

    prompt = f"""You are matching Bahá'í sacred writings. Given a prayer and candidate inventory entries, determine which (if any) is the same writing.

PRAYER TEXT (first 400 chars):
"{prayer_text[:400]}"

CANDIDATE INVENTORY ENTRIES:
"""

    for i, (pin, score, first_line) in enumerate(candidates, 1):
        prompt += f"\n{i}. PIN {pin}: \"{first_line[:250]}\""

    prompt += """

RULES:
- Match based on meaning and content, not exact wording
- Translation variations are expected (Thee/You, slight rephrasing)
- If none match, say NONE

ANSWER with just the PIN of the match (e.g., "AB12345") or "NONE" if no match:"""

    response = call_gemini(prompt)
    if not response:
        return None, "GEMINI_ERROR"

    # Parse response
    response = response.strip().upper()

    if response == "NONE" or "NONE" in response:
        return None, "NO_MATCH"

    # Extract PIN from response
    pin_match = re.search(r'(AB|BH|BB)\d{5}', response)
    if pin_match:
        matched_pin = pin_match.group()
        # Verify it's one of our candidates
        candidate_pins = [c[0] for c in candidates]
        if matched_pin in candidate_pins:
            return matched_pin, "VERIFIED"
        else:
            return matched_pin, "UNEXPECTED_PIN"

    return None, f"UNCLEAR_RESPONSE:{response[:50]}"


def main():
    print("=" * 70)
    print("TMP Code Resolution with Gemini LLM")
    print("=" * 70)

    # Load inventory
    print("\nLoading inventory...")
    inventory = {}
    with open(DATA_DIR / 'inventory_export.csv', 'r', encoding='utf-8') as f:
        reader = csv.DictReader(f)
        for row in reader:
            inventory[row['PIN']] = {
                'first_original': row.get('First line (original)', ''),
                'first_translated': row.get('First line (translated)', ''),
            }
    print(f"  Loaded {len(inventory)} inventory entries")

    # Get TMP prayers from database
    print("\nFetching TMP prayers...")
    tmp_prayers = run_dolt_query("""
        SELECT phelps, language, version, text
        FROM writings
        WHERE phelps LIKE 'TMP%' AND language IN ('en', 'ar', 'fa')
        ORDER BY phelps
    """)
    print(f"  Found {len(tmp_prayers)} source language TMP codes")

    # Process each prayer
    results = {
        'verified': [],
        'no_match': [],
        'uncertain': [],
        'error': []
    }

    for i, prayer in enumerate(tmp_prayers):
        tmp_code = prayer['phelps']
        lang = prayer['language']
        version = prayer['version']
        text = prayer['text']

        print(f"\n[{i+1}/{len(tmp_prayers)}] {tmp_code} ({lang})...", end=' ', flush=True)

        # Get candidates
        candidates = get_candidates_for_prayer(text, lang, inventory)

        # Verify with Gemini
        matched_pin, status = verify_match_with_gemini(text, candidates, lang)

        result = {
            'tmp_code': tmp_code,
            'language': lang,
            'version': version,
            'matched_pin': matched_pin,
            'status': status,
            'candidates': [c[0] for c in candidates]
        }

        if status == "VERIFIED" and matched_pin:
            results['verified'].append(result)
            print(f"✅ -> {matched_pin}")
        elif status == "NO_MATCH":
            results['no_match'].append(result)
            print(f"❌ No match")
        elif "ERROR" in status or "UNCLEAR" in status:
            results['error'].append(result)
            print(f"⚠️ {status}")
        else:
            results['uncertain'].append(result)
            print(f"🟡 {status}")

        # Rate limiting
        time.sleep(1)

    # Summary
    print("\n" + "=" * 70)
    print("SUMMARY")
    print("=" * 70)
    print(f"Verified matches:  {len(results['verified'])}")
    print(f"No match found:    {len(results['no_match'])}")
    print(f"Uncertain:         {len(results['uncertain'])}")
    print(f"Errors:            {len(results['error'])}")

    # Save results
    results_file = DATA_DIR / 'tmp_gemini_results.json'
    with open(results_file, 'w', encoding='utf-8') as f:
        json.dump(results, f, indent=2, ensure_ascii=False)
    print(f"\nResults saved to: {results_file}")

    # Generate SQL for verified matches
    sql_file = DATA_DIR / 'tmp_gemini_verified.sql'
    with open(sql_file, 'w', encoding='utf-8') as f:
        f.write("-- Gemini-verified TMP code resolutions\n")
        f.write(f"-- Generated: {time.strftime('%Y-%m-%d %H:%M:%S')}\n")
        f.write(f"-- Verified: {len(results['verified'])} matches\n\n")

        for r in results['verified']:
            pin = r['matched_pin'].replace("'", "''")
            ver = r['version'].replace("'", "''")
            lang = r['language']
            f.write(f"UPDATE writings SET phelps = '{pin}' ")
            f.write(f"WHERE version = '{ver}' AND language = '{lang}';\n")

    print(f"SQL saved to: {sql_file}")


if __name__ == '__main__':
    main()
