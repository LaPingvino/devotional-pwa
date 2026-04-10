#!/usr/bin/env python3
"""
seed_from_bahai_library.py — Fetch Bahá'í authoritative texts from bahai.org
and align them to Phelps inventory codes.

Strategy:
- Download the full XHTML of each book (single file, no JS required)
- Parse discrete prayer/section units from the XHTML structure
- Match each unit's text against inventory first-lines using:
    1. Long-prefix matching (up to 150 chars) — very high precision
    2. Sliding-window matching — catches shared formula openers
    3. Short-prefix fallback (30 chars) — catches anything remaining

Usage:
  python3 scripts/seed_from_bahai_library.py [--source SOURCE] [--dry-run] [--verbose]
  python3 scripts/seed_from_bahai_library.py --list
"""

import csv
import json
import os
import re
import subprocess
import sys
import time
import argparse
import urllib.request
import urllib.error

DOLT_DIR = "/home/joop/bahaiwritings"
INVENTORY_CSV = "/home/joop/prayermatching/data/inventory_export.csv"

# How we identify section elements in XHTML:
#   'el'    — element tag ('div' or 'p')
#   'class' — start of class attribute to match (e.g. 'dd' matches class="dd", class="dd xc" etc.)
#
# XHTML download URLs discovered by parsing the sidebar of section /1 pages.

SOURCES = {
    'bah-prayers-med': {
        'name': "Prayers and Meditations by Bah\u00e1\u2019u\u2019ll\u00e1h",
        'xhtml_url': ('https://www.bahai.org/library/authoritative-texts/'
                      'bahaullah/prayers-meditations/'
                      'prayers-meditations.xhtml?db5b07ad'),
        'section_el': 'div', 'section_class': 'dd',
        'pin_prefixes': ['BH'],
        'source_tag': 'bahai.org/bah-prayers-med',
    },
    'bah-gleanings': {
        'name': "Gleanings from the Writings of Bah\u00e1\u2019u\u2019ll\u00e1h",
        'xhtml_url': ('https://www.bahai.org/library/authoritative-texts/'
                      'bahaullah/gleanings-writings-bahaullah/'
                      'gleanings-writings-bahaullah.xhtml?883e1870'),
        'section_el': 'div', 'section_class': 'dd',
        'pin_prefixes': ['BH'],
        'source_tag': 'bahai.org/bah-gleanings',
    },
    'bah-hidden-words': {
        'name': "Hidden Words of Bah\u00e1\u2019u\u2019ll\u00e1h",
        'xhtml_url': ('https://www.bahai.org/library/authoritative-texts/'
                      'bahaullah/hidden-words/'
                      'hidden-words.xhtml?8c1bd035'),
        'section_el': 'p', 'section_class': 'dd',
        'pin_prefixes': ['BH'],
        'source_tag': 'bahai.org/bah-hidden-words',
        # Systematic sub-codes: each aphorism gets base_pin + letter + zero-padded number.
        # Split point between Arabic and Persian: first para containing 'In the Name of
        # the Lord of Utterance' marks the start of the Persian section.
        # Arabic: BH00386A01–BH00386A71; Persian: BH00113P01–BH00113P82.
        'numbered_groups': [
            # Arabic ends just before Persian #1 ("O Ye People that Have Minds...")
            # The preamble "In the Name of the Lord of Utterance" is <60 chars and
            # filtered out, so we split on the text of the first Persian aphorism.
            ('BH00386', 'A', None, 'O Ye People that Have Minds to Know'),
            ('BH00113', 'P', 'O Ye People that Have Minds to Know', None),
        ],
    },
    'bah-bahai-prayers': {
        'name': "Bah\u00e1\u2019\u00ed Prayers (official prayer book)",
        'xhtml_url': ('https://www.bahai.org/library/authoritative-texts/'
                      'prayers/bahai-prayers/'
                      'bahai-prayers.xhtml?e4ba1dff'),
        'section_el': 'div', 'section_class': 'dd',
        'pin_prefixes': ['BH', 'BB', 'AB'],
        'source_tag': 'bahai.org/bahai-prayers',
    },
    'bab-writings': {
        'name': "Selections from the Writings of the B\u00e1b",
        'xhtml_url': ('https://www.bahai.org/library/authoritative-texts/'
                      'the-bab/selections-writings-bab/'
                      'selections-writings-bab.xhtml?81c12259'),
        'section_el': 'div', 'section_class': 'dd',
        'pin_prefixes': ['BB'],
        'source_tag': 'bahai.org/bab',
    },
    'bah-aqdas': {
        'name': "The Most Holy Book (Kit\u00e1b-i-Aqdas)",
        'xhtml_url': ('https://www.bahai.org/library/authoritative-texts/'
                      'bahaullah/kitab-i-aqdas/'
                      'kitab-i-aqdas.xhtml?ec8d869c'),
        'section_el': 'div', 'section_class': 'dd',
        'pin_prefixes': ['BH'],
        'source_tag': 'bahai.org/bah-aqdas',
        # The Aqdas is indexed as a whole under BH00001.
        'fixed_groups': [
            ('BH00001', None, None),
        ],
    },
    'ab-selections': {
        'name': "Selections from the Writings of \u02bbAbdu\u2019l-Bah\u00e1",
        'xhtml_url': ('https://www.bahai.org/library/authoritative-texts/'
                      'abdul-baha/selections-writings-abdul-baha/'
                      'selections-writings-abdul-baha.xhtml?4dae3842'),
        'section_el': 'div', 'section_class': 'dd',
        'pin_prefixes': ['AB'],
        'source_tag': 'bahai.org/ab-selections',
    },
}

# Minimum meaningful text length for a section to be considered
MIN_SECTION_LEN = 60


# ── Text utilities ────────────────────────────────────────────────────────────

def normalize(text):
    """Lowercase, strip punctuation, collapse whitespace."""
    text = text.lower()
    text = re.sub(r"[^\w\s']", ' ', text)
    return re.sub(r'\s+', ' ', text).strip()


def clean_html(html_fragment):
    """Strip tags and normalize whitespace from an HTML fragment."""
    text = re.sub(r'<[^>]+>', '', html_fragment)
    text = text.replace('\u00a0', ' ').replace('&nbsp;', ' ')
    text = text.replace('&mdash;', '\u2014').replace('&rsquo;', '\u2019')
    text = text.replace('&ldquo;', '\u201c').replace('&rdquo;', '\u201d')
    text = re.sub(r'&[a-z]+;', ' ', text)
    # Remove footnote superscript numbers after LETTER chars (not after digits,
    # to avoid stripping the trailing digit from section numbers like "71").
    text = re.sub(r'([a-zA-Z])\d+(\s)', r'\1\2', text)
    text = re.sub(r'([a-zA-Z])\d+$', r'\1', text)
    return re.sub(r'\s+', ' ', text).strip()


def chunk_text(text, size=900):
    """Split text into ≤size-char chunks at sentence boundaries."""
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


# ── XHTML fetching / parsing ──────────────────────────────────────────────────

def fetch_xhtml(url):
    """Download an XHTML file from bahai.org. Returns text or None."""
    req = urllib.request.Request(url, headers={
        'User-Agent': 'Mozilla/5.0 (compatible; BahaiTextAligner/1.0)'
    })
    try:
        with urllib.request.urlopen(req, timeout=60) as resp:
            return resp.read().decode('utf-8', errors='replace')
    except Exception as e:
        print(f"  ERROR fetching {url}: {e}", file=sys.stderr)
        return None


def parse_all_paragraphs(html):
    """Extract every <p> tag as a clean text string, in document order."""
    paras = []
    for m in re.finditer(r'<p\b[^>]*>(.*?)</p>', html, re.DOTALL | re.IGNORECASE):
        text = clean_html(m.group(1))
        if len(text) >= 20 and not _NUMERAL_RE.match(text):
            paras.append(text)
    return paras


def apply_fixed_groups(paragraphs, fixed_groups):
    """
    Split a flat paragraph list into groups defined by text split-points.

    fixed_groups: list of (pin, start_text_or_None, end_text_or_None)
      start_text — first paragraph whose text CONTAINS this string starts the group
      end_text   — last paragraph before the one containing this string

    Returns list of (pin, full_text).
    """
    # Build a list of split-points: (paragraph_index, pin_for_group_starting_here)
    # Group 0 starts at index 0, group 1 starts at the paragraph that matches group[1]'s start_text.
    result = []
    n = len(paragraphs)

    def find_para(text_fragment, from_idx=0):
        """Return the index of the first paragraph containing text_fragment."""
        frag_norm = text_fragment.lower()
        for i in range(from_idx, n):
            if frag_norm in paragraphs[i].lower():
                return i
        return None

    # Resolve group boundaries
    boundaries = []  # list of (start_idx, end_idx_exclusive, pin)
    for pin, start_text, end_text in fixed_groups:
        start_idx = 0 if start_text is None else find_para(start_text)
        if start_idx is None:
            print(f"  WARNING: split text not found: {start_text!r:.50}", file=sys.stderr)
            continue
        end_idx = n if end_text is None else find_para(end_text, start_idx)
        if end_idx is None:
            end_idx = n
        boundaries.append((start_idx, end_idx, pin))

    for start_idx, end_idx, pin in boundaries:
        group_paras = paragraphs[start_idx:end_idx]
        text = ' '.join(group_paras).strip()
        if text:
            result.append((pin, text))

    return result


# Leading-number stripper: "71 O Son of Man! ..." → "O Son of Man! ..."
_LEADING_NUM_RE = re.compile(r'^\d+\s+')


def apply_numbered_groups(sections, numbered_groups):
    """
    Assign systematic sub-passage codes to individual sections.

    numbered_groups: list of (base_pin, letter, start_text_or_None, end_text_or_None)
      - base_pin: e.g. 'BH00386'
      - letter:   e.g. 'A' (for Arabic) or 'P' (for Persian)
      - start/end_text: split points (same semantics as apply_fixed_groups)

    Each section in the range gets a code: base_pin + letter + zero_padded_number
    e.g. BH00386A01, BH00386A02, ..., BH00386A71
    Numbers are zero-padded to 2 digits.

    Also strips leading aphorism numbers from section text ('71 O Son of Man!' → 'O Son of Man!').

    Returns list of (sub_code, clean_text).
    """
    n = len(sections)
    result = []

    def find_section(text_fragment, from_idx=0):
        frag = text_fragment.lower()
        for i in range(from_idx, n):
            if frag in sections[i].lower():
                return i
        return None

    for base_pin, letter, start_text, end_text in numbered_groups:
        start_idx = 0 if start_text is None else find_section(start_text)
        if start_idx is None:
            print(f"  WARNING: split text not found: {start_text!r:.50}", file=sys.stderr)
            continue
        end_idx = n if end_text is None else find_section(end_text, start_idx)
        if end_idx is None:
            end_idx = n

        group = sections[start_idx:end_idx]
        for num, text in enumerate(group, start=1):
            # Strip leading aphorism number if present
            clean = _LEADING_NUM_RE.sub('', text).strip()
            if not clean:
                continue
            sub_code = f"{base_pin}{letter}{num:02d}"
            result.append((sub_code, clean))

    return result


# Numeral-only lines to skip (roman numerals, section markers)
_NUMERAL_RE = re.compile(
    r'^[\s\u2013\u2014\-\u2010]*(([IVXLCDM]+|\d+)\.?[\s\u2013\u2014\-]*)+$',
    re.IGNORECASE
)


def parse_sections(html, el, cls_prefix):
    """
    Extract discrete text sections from XHTML.

    el         — 'div' or 'p'
    cls_prefix — start of class attribute value to match (e.g. 'dd')

    Returns list of section texts (each is the full joined text of that section).
    """
    # Pattern: <el class="cls_prefix..." ...>...</el>
    # Note: div sections may span multiple nested </div> — we use a simple
    # depth-counted approach to avoid needing a full HTML parser.

    sections = []

    if el == 'p':
        # Simple case: self-contained paragraph
        pattern = re.compile(
            rf'<p\b[^>]*\bclass="[^"]*\b{re.escape(cls_prefix)}\b[^"]*"[^>]*>(.*?)</p>',
            re.DOTALL | re.IGNORECASE
        )
        for m in pattern.finditer(html):
            text = clean_html(m.group(1))
            if len(text) >= MIN_SECTION_LEN and not _NUMERAL_RE.match(text):
                sections.append(text)
        return sections

    # For divs: find opening tag, then scan for balanced closing </div>
    open_pat = re.compile(
        rf'<div\b[^>]*\bclass="{re.escape(cls_prefix)}[^"]*"[^>]*>',
        re.IGNORECASE
    )
    for m in open_pat.finditer(html):
        start = m.end()
        # Walk forward counting div depth
        depth = 1
        pos = start
        while depth > 0 and pos < len(html):
            next_open = html.find('<div', pos)
            next_close = html.find('</div>', pos)
            if next_close == -1:
                break
            if next_open != -1 and next_open < next_close:
                depth += 1
                pos = next_open + 4
            else:
                depth -= 1
                if depth == 0:
                    section_html = html[start:next_close]
                    # Extract all p-tag texts within this div
                    paras = []
                    for pm in re.finditer(r'<p[^>]*>(.*?)</p>', section_html,
                                          re.DOTALL | re.IGNORECASE):
                        t = clean_html(pm.group(1))
                        if len(t) >= 20 and not _NUMERAL_RE.match(t):
                            paras.append(t)
                    if paras:
                        text = ' '.join(paras)
                        if len(text) >= MIN_SECTION_LEN:
                            sections.append(text)
                pos = next_close + 6

    return sections


# ── Inventory loading ─────────────────────────────────────────────────────────

def load_inventory(pin_prefixes=None):
    """
    Load Phelps codes and normalized first-lines from inventory CSV.

    Returns a dict: normalized_prefix → [(pin, original_en)]

    Keys are added at multiple lengths (30 to full line length in steps),
    so lookups at different prefix lengths all work with the same dict.
    """
    if isinstance(pin_prefixes, str):
        pin_prefixes = [pin_prefixes]

    entries = []
    with open(INVENTORY_CSV, newline='', encoding='utf-8') as f:
        for row in csv.DictReader(f):
            pin = row['PIN'].strip()
            en = row.get('First line (translated)', '').strip()
            if not en:
                continue
            if pin_prefixes:
                if not any(pin.startswith(p) for p in pin_prefixes):
                    continue
            # Skip "U" tablet-address codes (false-positive sources)
            if re.match(r'^[A-Z]{2,3}U\d', pin):
                continue
            entries.append((pin, en))

    inv_index = {}
    for pin, en in entries:
        norm = normalize(en)
        # Add keys at multiple lengths for flexible matching
        prev_key = None
        for length in [30, 50, 70, 90, 110, 130, 150, len(norm)]:
            if length > len(norm):
                length = len(norm)
            key = norm[:length]
            if key == prev_key:
                break
            prev_key = key
            if len(key) < 20:
                continue
            if key not in inv_index:
                inv_index[key] = []
            inv_index[key].append((pin, en))

    return inv_index


def common_prefix_len(a, b):
    """Length of common prefix between two strings."""
    n = min(len(a), len(b))
    for i in range(n):
        if a[i] != b[i]:
            return i
    return n


def match_text_to_pin(text, inv_index, verbose=False):
    """
    Match a prayer/section text to a Phelps PIN.

    Strategy (in order):
    1. Long-prefix match (150→30 chars) of the text opening
    2. Sliding-window match: try starting 1–15 words into the text
       (handles formula openers like 'I beseech Thee, O my God, by...')

    When multiple inventory entries share a prefix, pick the one whose
    full first-line shares the longest prefix with our text.

    Returns (pin, match_length_hint) or (None, 0).
    """
    norm = normalize(text[:500])
    words = norm.split()

    def pick_best(matches, norm_text):
        if len(matches) == 1:
            return matches[0][0]
        best = max(matches, key=lambda m: common_prefix_len(norm_text, normalize(m[1])))
        return best[0]

    # Strategy 1: progressively shorter prefix from the opening
    for length in range(min(150, len(norm)), 25, -5):
        prefix = norm[:length]
        if len(prefix.split()) < 5:
            continue
        if prefix in inv_index:
            return pick_best(inv_index[prefix], norm), length

    # Strategy 2: sliding window — start from word offset into the text
    # Useful when the prayer has a shared opening formula but a unique body
    for start_word in range(1, min(20, len(words) - 8), 2):
        tail = ' '.join(words[start_word:])
        for length in [100, 80, 60, 40]:
            prefix = tail[:length]
            if len(prefix.split()) < 5:
                break
            if prefix in inv_index:
                return pick_best(inv_index[prefix], norm), length

    return None, 0


# ── Dolt database ─────────────────────────────────────────────────────────────

def dolt_query(sql, as_json=False):
    """Run a dolt SQL query via temp file."""
    import tempfile
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


def get_existing_pins(source_tag):
    """Return set of phelps codes already seeded from this source."""
    result = dolt_query(
        f"SELECT phelps FROM inventory_fulltext WHERE source='{escape_sql(source_tag)}'"
    )
    existing = set()
    if result:
        for line in result.splitlines()[1:]:
            pin = line.strip().strip('"')
            if pin:
                existing.add(pin)
    return existing


# ── Core processing ───────────────────────────────────────────────────────────

def process_source(source_cfg, args):
    """Download, parse, match, and (optionally) insert a single source."""
    print(f"\n{'='*60}", file=sys.stderr)
    print(f"Source:  {source_cfg['name']}", file=sys.stderr)
    print(f"Tag:     {source_cfg['source_tag']}", file=sys.stderr)
    print(f"XHTML:   {source_cfg['xhtml_url'][:70]}...", file=sys.stderr)
    print(f"{'='*60}", file=sys.stderr)

    pin_prefixes = source_cfg['pin_prefixes']
    source_tag = source_cfg['source_tag']

    # Load inventory
    print(f"Loading inventory (prefixes={pin_prefixes})...", file=sys.stderr)
    inv_index = load_inventory(pin_prefixes)
    total_codes = len({e[0] for entries in inv_index.values() for e in entries})
    print(f"  {total_codes} codes, {len(inv_index)} index keys", file=sys.stderr)

    if not args.dry_run:
        existing = get_existing_pins(source_tag)
        print(f"  {len(existing)} already seeded from {source_tag}", file=sys.stderr)
    else:
        existing = set()

    # Download XHTML
    print(f"Downloading XHTML...", file=sys.stderr)
    html = fetch_xhtml(source_cfg['xhtml_url'])
    if not html:
        print("  Failed — skipping", file=sys.stderr)
        return 0

    # ── Numbered-group mode (systematic sub-codes, e.g. Hidden Words A01-A71) ──
    if 'numbered_groups' in source_cfg:
        sections = parse_sections(html, source_cfg['section_el'],
                                  source_cfg['section_class'])
        print(f"  {len(sections)} sections parsed (numbered-group mode)", file=sys.stderr)
        pairs = apply_numbered_groups(sections, source_cfg['numbered_groups'])
        matched = [(pin, text, 0) for pin, text in pairs]
        print(f"  {len(matched)} numbered sub-codes generated "
              f"({matched[0][0] if matched else '?'} … "
              f"{matched[-1][0] if matched else '?'})", file=sys.stderr)

        if args.dry_run:
            print(f"\nDRY RUN (numbered groups) — {source_cfg['name']}:")
            for pin, text, _ in matched[:5]:
                print(f"  {pin}: {text[:80]}...")
            if len(matched) > 5:
                print(f"  ... ({len(matched)} total)")
            return len(matched)

    # ── Fixed-group mode (whole-work entries like Aqdas) ─────────────────────
    elif 'fixed_groups' in source_cfg:
        all_paras = parse_all_paragraphs(html)
        print(f"  {len(all_paras)} paragraphs parsed (fixed-group mode)", file=sys.stderr)
        groups = apply_fixed_groups(all_paras, source_cfg['fixed_groups'])
        matched = [(pin, text, 0) for pin, text in groups]
        print(f"  {len(matched)} fixed-group PINs: "
              f"{[m[0] for m in matched]}", file=sys.stderr)

        if args.dry_run:
            print(f"\nDRY RUN (fixed groups) — {source_cfg['name']}:")
            for pin, text, _ in matched:
                print(f"  {pin}: {len(text)} chars — {text[:80]}...")
            return len(matched)

    # ── Standard section-matching mode ──────────────────────────────────────
    else:
        sections = parse_sections(
            html,
            source_cfg['section_el'],
            source_cfg['section_class']
        )
        print(f"  {len(sections)} sections parsed", file=sys.stderr)

        if not sections:
            print("  No sections found — check section_el/section_class", file=sys.stderr)
            return 0

        matched = []     # [(pin, text, match_len)]
        unmatched = 0
        seen_pins = set()  # deduplicate: first match wins per PIN

        for i, section_text in enumerate(sections):
            pin, mlen = match_text_to_pin(section_text, inv_index, args.verbose)
            if pin:
                if args.verbose:
                    print(f"  [{i+1}/{len(sections)}] MATCH {pin} (at {mlen}c): "
                          f"{section_text[:60]!r}", file=sys.stderr)
                if pin not in seen_pins:
                    matched.append((pin, section_text, mlen))
                    seen_pins.add(pin)
            else:
                unmatched += 1
                if args.verbose:
                    print(f"  [{i+1}/{len(sections)}] no match: {section_text[:80]!r}",
                          file=sys.stderr)

        print(f"Matched {len(matched)} unique PINs, {unmatched} unmatched "
              f"(from {len(sections)} sections)", file=sys.stderr)

        if args.dry_run:
            print(f"\nDRY RUN — {source_cfg['name']}:")
            for pin, text, mlen in matched:
                print(f"  {pin} (match@{mlen}c): {text[:80]}...")
            return len(matched)

    # Insert into inventory_fulltext (bahai.org takes priority over other sources)
    inserts = []
    skipped = 0
    pins_to_replace = []
    for pin, text, _ in matched:
        if pin in existing:
            skipped += 1
            continue
        pins_to_replace.append(pin)
        for i, chunk in enumerate(chunk_text(text)):
            inserts.append(
                f"('{escape_sql(pin)}', 'en', {i}, "
                f"'{escape_sql(chunk)}', '{escape_sql(source_tag)}')"
            )

    # Delete any existing English entries for these PINs from other sources
    # so bahai.org text takes priority
    if pins_to_replace:
        for i in range(0, len(pins_to_replace), 50):
            batch_pins = pins_to_replace[i:i+50]
            pin_list = ", ".join(f"'{escape_sql(p)}'" for p in batch_pins)
            dolt_query(
                f"DELETE FROM inventory_fulltext WHERE language='en' "
                f"AND phelps IN ({pin_list}) AND source <> '{escape_sql(source_tag)}'"
            )

    print(f"Inserting {len(inserts)} chunks ({skipped} already present)...")

    batch_size = 50
    for i in range(0, len(inserts), batch_size):
        batch = inserts[i:i + batch_size]
        sql = ("INSERT IGNORE INTO inventory_fulltext "
               "(phelps, language, part, text, source) VALUES "
               + ", ".join(batch))
        dolt_query(sql)
        n = i // batch_size + 1
        total = (len(inserts) + batch_size - 1) // batch_size
        print(f"  Batch {n}/{total}")

    return len(matched)


def print_summary():
    """Print current inventory_fulltext counts by prefix."""
    print("\nInventory fulltext after run:")
    for prefix in ('BH', 'BB', 'AB'):
        result = dolt_query(
            f"SELECT COUNT(*) as chunks, COUNT(DISTINCT phelps) as codes "
            f"FROM inventory_fulltext WHERE phelps LIKE '{prefix}%'"
        )
        if result:
            lines = result.strip().splitlines()
            if len(lines) >= 2:
                print(f"  {prefix}: {lines[1].strip()}")


# ── Entry point ───────────────────────────────────────────────────────────────

def main():
    parser = argparse.ArgumentParser(
        description='Seed inventory_fulltext from bahai.org authoritative texts')
    parser.add_argument('--source', default='bah-prayers-med',
                        choices=list(SOURCES.keys()) + ['all'],
                        help='Which source to process (default: bah-prayers-med)')
    parser.add_argument('--dry-run', action='store_true',
                        help='Show what would be matched without inserting')
    parser.add_argument('--verbose', '-v', action='store_true',
                        help='Print each match/no-match as found')
    parser.add_argument('--list', action='store_true',
                        help='List available sources and exit')
    args = parser.parse_args()

    if args.list:
        print("Available sources:")
        for key, cfg in SOURCES.items():
            print(f"  {key:<22} {cfg['name']}")
        return

    sources_to_process = (list(SOURCES.values()) if args.source == 'all'
                          else [SOURCES[args.source]])

    total_matched = 0
    for source_cfg in sources_to_process:
        matched = process_source(source_cfg, args)
        total_matched += matched

    if not args.dry_run:
        print_summary()

    print(f"\nDone. Total unique PINs matched: {total_matched}")


if __name__ == '__main__':
    main()
