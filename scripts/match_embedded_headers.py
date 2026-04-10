#!/usr/bin/env python3
"""
match_embedded_headers.py - match prayers that contain embedded English clues.

Many prayers in obscure languages contain English text in:
  - ## Header lines (e.g. "## O God Guide Me", "## MARIT (Marriage)")
  - * Gloss lines (e.g. "* Dawn Prayer", "* Short Obligatory Prayer")
  - Inline English text (e.g. "Blessed is the Spot")

This script extracts those clues and matches directly against the Phelps
inventory without Gemini, then outputs SQL UPDATE statements.

Usage:
  python scripts/match_embedded_headers.py [--lang hz] [--dry-run]
"""

import csv
import re
import sys
import argparse
from collections import defaultdict

INVENTORY_CSV = "/home/joop/prayermatching/data/inventory_export.csv"
PRAYERS_CSV = "/tmp/unresolved_prayers.csv"

# Well-known prayer name → phelps code (for category-based matching)
KNOWN_PRAYER_NAMES = {
    "blessed is the spot": "BH00074BLE",  # sub-passage of BH00074
    "remover of difficulties": "BB00623",
    "short obligatory prayer": "BH05849",
    "medium obligatory prayer": "BH08838",
    "long obligatory prayer": "BH08600",
    "dawn prayer": "BH11209",
    "morning prayer": None,  # look up in inventory
    "healing prayer": None,
    "tablet of ahmad": "BH02806",
    "tablet of the holy mariner": "BH07682",
    "unity prayer": "AB00788",
    "marriage prayer": "AB03461MAR",
    "guide me": "AB04427GUI",  # O God, guide me, protect me ('Abdu'l-Bahá, sub-passage of AB04427)
    "children": None,
    "youth": None,
    "fast": None,
    "burial": None,
    "fund": None,
}


def load_inventory():
    inv_by_phrase = {}
    inv_by_pin = {}
    with open(INVENTORY_CSV, newline='', encoding='utf-8') as f:
        for row in csv.DictReader(f):
            pin = row['PIN'].strip()
            en = row.get('First line (translated)', '').strip()
            inv_by_pin[pin] = en
            if en:
                key = normalize(en)
                if key not in inv_by_phrase:
                    inv_by_phrase[key] = []
                inv_by_phrase[key].append(pin)
    return inv_by_phrase, inv_by_pin


def normalize(text):
    text = text.lower()
    text = re.sub(r"[^\w\s']", ' ', text)
    return re.sub(r'\s+', ' ', text).strip()


def extract_english_clues(text):
    """
    Extract candidate English phrases from embedded headers and glosses.
    Returns list of strings, best candidates first.
    """
    clues = []
    lines = text.split('\n')

    for line in lines:
        line = line.strip()

        # * Gloss lines — typically "* English prayer name" or "* English description"
        m = re.match(r'^\*\s*(.+)', line)
        if m:
            candidate = m.group(1).strip()
            # Keep if it looks English (mostly ASCII letters)
            ascii_ratio = sum(1 for c in candidate if ord(c) < 128) / max(len(candidate), 1)
            if ascii_ratio > 0.8 and len(candidate) > 4:
                clues.append(candidate)

        # ## Header lines — may be "## ENGLISH TITLE" or "##(ENGLISH)"
        m = re.match(r'^#+\s*(.+)', line)
        if m:
            candidate = m.group(1).strip()
            # Strip parenthetical suffixes like "##(DAWN)" → "DAWN"
            candidate = re.sub(r'^\((.+)\)$', r'\1', candidate)
            # Strip trailing parenthetical like "Title (English)" → keep both
            paren = re.search(r'\(([^)]+)\)', candidate)
            if paren:
                clues.append(paren.group(1).strip())
            ascii_ratio = sum(1 for c in candidate if ord(c) < 128) / max(len(candidate), 1)
            if ascii_ratio > 0.8 and len(candidate) > 4:
                clues.append(candidate)

    # Also scan for "Blessed is the Spot" type phrases inline
    blessed = re.search(r'Blessed is the Spot', text, re.IGNORECASE)
    if blessed:
        clues.insert(0, "Blessed is the Spot")

    return clues


def match_clue_to_inventory(clue, inv_by_phrase, inv_by_pin, min_words=3):
    """Try known names first, then phrase search."""
    norm = normalize(clue)

    # Check known prayer names dict
    for known, pin in KNOWN_PRAYER_NAMES.items():
        if known in norm or norm in known:
            if pin:
                return pin, known, 'known'

    # Try phrase substring match against inventory
    words = norm.split()
    for length in range(min(len(words), 8), min_words - 1, -1):
        for offset in range(0, min(len(words) - length + 1, 3)):
            sub = ' '.join(words[offset:offset + length])
            if len(sub) < 12:
                continue
            for key, pins in inv_by_phrase.items():
                if sub in key:
                    return pins[0], sub, 'phrase'
    return None, None, None


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument('--lang', help='Filter to specific language code')
    parser.add_argument('--dry-run', action='store_true')
    args = parser.parse_args()

    inv_by_phrase, inv_by_pin = load_inventory()

    prayers = []
    with open(PRAYERS_CSV, newline='', encoding='utf-8') as f:
        for row in csv.DictReader(f):
            if args.lang and row['language'] != args.lang:
                continue
            prayers.append(row)

    print(f"-- Embedded header matching for "
          f"{args.lang or 'all'}: {len(prayers)} prayers", file=sys.stderr)

    matched = []
    unmatched = []

    for row in prayers:
        sid = row['source_id']
        lang = row['language']
        text = row['text']

        clues = extract_english_clues(text)
        if not clues:
            unmatched.append((sid, lang, '(no English clues found)'))
            continue

        pin = None
        matched_clue = None
        method = None
        for clue in clues:
            pin, matched_clue, method = match_clue_to_inventory(
                clue, inv_by_phrase, inv_by_pin)
            if pin:
                break

        if pin:
            inv_display = inv_by_pin.get(pin, '')[:60]
            print(f"  {lang}/{sid}: clue={repr(clues[0][:50])} → {pin} "
                  f"({method}) inv={repr(inv_display)}", file=sys.stderr)
            matched.append((sid, lang, pin))
        else:
            print(f"  {lang}/{sid}: clues={[c[:30] for c in clues[:3]]} → NO MATCH",
                  file=sys.stderr)
            unmatched.append((sid, lang, clues[0] if clues else ''))

    print(f"\n-- Matched: {len(matched)}, Unmatched: {len(unmatched)}", file=sys.stderr)

    if matched and not args.dry_run:
        print("\n-- SQL UPDATE statements --")
        for sid, lang, pin in matched:
            print(f"UPDATE writings SET phelps='{pin}' WHERE source_id='{sid}' "
                  f"AND language='{lang}' AND source='bahaiprayers.net';")

    if unmatched:
        print("\n-- UNMATCHED --", file=sys.stderr)
        for sid, lang, clue in unmatched:
            print(f"-- {lang}/{sid}: {clue}", file=sys.stderr)


if __name__ == '__main__':
    main()
