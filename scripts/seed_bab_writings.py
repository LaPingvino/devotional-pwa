#!/usr/bin/env python3
"""
seed_bab_writings.py — Fetch Báb's writings from bahai.org and align to BB Phelps codes.

Strategy:
1. Fetch each section of "Selections from the Writings of the Báb" from bahai.org
2. Extract paragraphs, clean footnotes/HTML
3. Use landmark matching: compare each paragraph's opening against BB inventory first-lines
4. When a paragraph matches a BB code, accumulate its text until the next match
5. Insert aligned texts into inventory_fulltext

Usage:
  python3 scripts/seed_bab_writings.py [--dry-run] [--verbose]
"""

import csv
import json
import re
import subprocess
import sys
import time
import argparse
import urllib.request
from collections import defaultdict

DOLT_DIR = "/home/joop/bahaiwritings"
INVENTORY_CSV = "/home/joop/prayermatching/data/inventory_export.csv"
SOURCE_TAG = "bahai.org/bab"

# All sections of Selections from the Writings of the Báb
# Section 1 = intro/TOC, sections 2-8 = actual text
BAB_SELECTIONS_URLS = [
    "https://www.bahai.org/library/authoritative-texts/the-bab/selections-writings-bab/2",
    "https://www.bahai.org/library/authoritative-texts/the-bab/selections-writings-bab/3",
    "https://www.bahai.org/library/authoritative-texts/the-bab/selections-writings-bab/4",
    "https://www.bahai.org/library/authoritative-texts/the-bab/selections-writings-bab/5",
    "https://www.bahai.org/library/authoritative-texts/the-bab/selections-writings-bab/6",
    "https://www.bahai.org/library/authoritative-texts/the-bab/selections-writings-bab/7",
    "https://www.bahai.org/library/authoritative-texts/the-bab/selections-writings-bab/8",
]

# Minimum chars for a paragraph to be considered real content
MIN_PARA_LEN = 30
# Minimum chars of normalized text that must match inventory first-line
MIN_MATCH_CHARS = 30


def normalize(text):
    """Lowercase, strip punctuation, collapse whitespace."""
    text = text.lower()
    text = re.sub(r"[^\w\s']", ' ', text)
    return re.sub(r'\s+', ' ', text).strip()


def fetch_url(url, delay=1.5):
    """Fetch URL content as text."""
    req = urllib.request.Request(url, headers={
        'User-Agent': 'Mozilla/5.0 (compatible; BahaiTextAligner/1.0)'
    })
    try:
        with urllib.request.urlopen(req, timeout=30) as resp:
            content = resp.read().decode('utf-8', errors='replace')
        time.sleep(delay)
        return content
    except Exception as e:
        print(f"  ERROR fetching {url}: {e}", file=sys.stderr)
        return None


def extract_paragraphs(html):
    """Extract clean text paragraphs from HTML. Strips tags, footnote markers."""
    # Remove script/style/nav/header/footer blocks
    html = re.sub(r'<(script|style|nav|header|footer)[^>]*>.*?</\1>',
                  '', html, flags=re.DOTALL | re.IGNORECASE)
    # Extract p-tag content
    raw_paras = re.findall(r'<p[^>]*>(.*?)</p>', html, re.DOTALL | re.IGNORECASE)
    paras = []
    for p in raw_paras:
        # Strip HTML tags
        text = re.sub(r'<[^>]+>', '', p)
        # Remove footnote superscripts (digits at word boundaries or end of sentence)
        text = re.sub(r'(\w)\d+(\s)', r'\1\2', text)
        text = re.sub(r'(\w)\d+$', r'\1', text)
        # Normalize whitespace and HTML entities
        text = text.replace('&nbsp;', ' ').replace('&#160;', ' ')
        text = text.replace('&mdash;', '\u2014').replace('&rsquo;', '\u2019')
        text = text.replace('&ldquo;', '\u201c').replace('&rdquo;', '\u201d')
        text = re.sub(r'&[a-z]+;', ' ', text)
        text = re.sub(r'\s+', ' ', text).strip()
        if len(text) >= MIN_PARA_LEN:
            paras.append(text)
    return paras


def load_bb_inventory():
    """Load BB codes and their normalized first-lines from inventory CSV."""
    bb = {}  # normalized_prefix -> list of (pin, full_first_line)
    with open(INVENTORY_CSV, newline='', encoding='utf-8') as f:
        for row in csv.DictReader(f):
            pin = row['PIN'].strip()
            en = row.get('First line (translated)', '').strip()
            if not pin.startswith('BB') or pin.startswith('BBU') or not en:
                continue
            # Use first MIN_MATCH_CHARS chars for matching
            key = normalize(en[:MIN_MATCH_CHARS])
            if key not in bb:
                bb[key] = []
            bb[key].append((pin, en))
    return bb


def match_paragraph_to_pin(para, bb_index):
    """
    Try to match a paragraph's opening to a BB inventory entry.
    Returns (pin, full_first_line) or None.
    Tries progressively shorter prefixes.
    """
    norm = normalize(para)
    for length in range(MIN_MATCH_CHARS, 15, -3):
        prefix = norm[:length]
        if len(prefix.split()) < 4:
            break
        if prefix in bb_index:
            return bb_index[prefix][0]  # return first match
        # Also try checking if inventory key starts with this prefix
        for key, entries in bb_index.items():
            if key.startswith(prefix) or prefix.startswith(key[:20]):
                if len(prefix) >= 20 and len(key) >= 20:
                    return entries[0]
    return None


def dolt_query(sql, as_json=False):
    """Run a dolt SQL query."""
    import tempfile, os
    fmt = ['--result-format', 'json'] if as_json else ['--result-format', 'csv']
    with tempfile.NamedTemporaryFile(mode='w', suffix='.sql', delete=False) as f:
        f.write(sql)
        fname = f.name
    try:
        result = subprocess.run(
            ['dolt', 'sql'] + fmt + ['--file', fname],
            capture_output=True, text=True, cwd=DOLT_DIR
        )
    finally:
        os.unlink(fname)
    if result.returncode != 0:
        print(f"SQL error: {result.stderr[:200]}", file=sys.stderr)
        return None
    if as_json and result.stdout.strip():
        return json.loads(result.stdout)
    return result.stdout


def chunk_text(text, size=900):
    """Split text into chunks of at most size chars, preferring sentence breaks."""
    text = text.strip()
    chunks = []
    while len(text) > size:
        window = text[:size]
        match = None
        for pattern in (r'[.!?]\s+', r'\s+'):
            for m in re.finditer(pattern, window):
                match = m
            if match:
                break
        cut = match.end() if match else size
        chunks.append(text[:cut].rstrip())
        text = text[cut:].lstrip()
    if text:
        chunks.append(text)
    return chunks


def escape_sql(s):
    return s.replace("'", "''").replace("\\", "\\\\")


def get_existing_pins():
    """Get set of (phelps, language) already in inventory_fulltext from this source."""
    result = dolt_query(
        f"SELECT phelps FROM inventory_fulltext WHERE source='{SOURCE_TAG}'"
    )
    existing = set()
    if result:
        for line in result.splitlines()[1:]:
            pin = line.strip().strip('"')
            if pin:
                existing.add(pin)
    return existing


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument('--dry-run', action='store_true',
                        help='Show what would be matched without inserting')
    parser.add_argument('--verbose', '-v', action='store_true')
    parser.add_argument('--min-match', type=int, default=MIN_MATCH_CHARS,
                        help='Minimum chars for landmark matching')
    args = parser.parse_args()

    print("Loading BB inventory...", file=sys.stderr)
    bb_index = load_bb_inventory()
    print(f"  {sum(len(v) for v in bb_index.values())} BB codes, "
          f"{len(bb_index)} unique prefixes", file=sys.stderr)

    if not args.dry_run:
        existing = get_existing_pins()
        print(f"  {len(existing)} BB codes already in inventory_fulltext from this source",
              file=sys.stderr)
    else:
        existing = set()

    # Collect all paragraphs from all sections
    all_paras = []
    for url in BAB_SELECTIONS_URLS:
        section = url.split('/')[-1]
        print(f"Fetching section {section}...", file=sys.stderr)
        html = fetch_url(url)
        if not html:
            continue
        paras = extract_paragraphs(html)
        print(f"  {len(paras)} paragraphs extracted", file=sys.stderr)
        all_paras.extend(paras)

    print(f"\nTotal paragraphs: {len(all_paras)}", file=sys.stderr)

    # Landmark segmentation: find paragraphs that match BB codes,
    # accumulate text until the next match
    segments = []      # list of (pin, first_line, [paragraphs])
    current_pin = None
    current_first_line = None
    current_paras = []

    for para in all_paras:
        match = match_paragraph_to_pin(para, bb_index)
        if match:
            pin, first_line = match
            if current_pin and current_paras:
                segments.append((current_pin, current_first_line, current_paras[:]))
            current_pin = pin
            current_first_line = first_line
            current_paras = [para]
            if args.verbose:
                print(f"  MATCH {pin}: {para[:70]!r}", file=sys.stderr)
        elif current_pin:
            # Continuation of current passage — but be careful not to
            # accumulate boilerplate (skip very short or UI-looking paras)
            if len(para) >= 40:
                current_paras.append(para)

    # Don't forget the last segment
    if current_pin and current_paras:
        segments.append((current_pin, current_first_line, current_paras[:]))

    print(f"\nMatched {len(segments)} BB codes from {len(all_paras)} paragraphs",
          file=sys.stderr)

    if args.dry_run:
        print("\nDRY RUN — matched passages:")
        for pin, first_line, paras in segments:
            full_text = ' '.join(paras)
            print(f"\n{pin}: {first_line[:60]}")
            print(f"  Text ({len(full_text)} chars): {full_text[:120]}...")
        return

    # Insert into inventory_fulltext
    inserts = []
    skipped = 0
    for pin, first_line, paras in segments:
        if pin in existing:
            skipped += 1
            continue
        full_text = ' '.join(paras)
        chunks = chunk_text(full_text)
        for i, chunk in enumerate(chunks):
            inserts.append(
                f"('{escape_sql(pin)}', 'en', {i}, "
                f"'{escape_sql(chunk)}', '{SOURCE_TAG}')"
            )

    print(f"Inserting {len(inserts)} chunks ({skipped} skipped as already present)...")

    batch_size = 50
    for i in range(0, len(inserts), batch_size):
        batch = inserts[i:i+batch_size]
        sql = ("INSERT IGNORE INTO inventory_fulltext "
               "(phelps, language, part, text, source) VALUES "
               + ", ".join(batch))
        dolt_query(sql)
        print(f"  Batch {i // batch_size + 1}/{(len(inserts) + batch_size - 1) // batch_size}")

    # Report
    result = dolt_query(
        "SELECT COUNT(*) as n, COUNT(DISTINCT phelps) as pins "
        "FROM inventory_fulltext WHERE phelps LIKE 'BB%'"
    )
    print(f"\nDone. BB entries in inventory_fulltext now: {result.strip()}")


if __name__ == '__main__':
    main()
