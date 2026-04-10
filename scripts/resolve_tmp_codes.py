#!/usr/bin/env python3
"""
Resolve TMP codes to real inventory PINs using text matching.

Optimized version: Uses word-based indexing for fast candidate filtering.
"""

import csv
import re
import unicodedata
import json
from difflib import SequenceMatcher
from collections import defaultdict

# Thresholds
HIGH_CONFIDENCE_THRESHOLD = 0.80
MEDIUM_CONFIDENCE_THRESHOLD = 0.60

DATA_DIR = '/home/joop/prayermatching/data'


def normalize_text(text):
    """Normalize text for comparison."""
    if not text:
        return ""

    text = unicodedata.normalize('NFKD', text)

    # Remove common prefixes
    text = re.sub(r'^(هو\s*الله|هُوَاللّه|هُواللّه|He is God!?|O God[,!]?\s*my God[!]?)\s*', '', text, flags=re.IGNORECASE)
    text = re.sub(r'^#+\s*', '', text, flags=re.MULTILINE)

    # Remove Arabic diacritics
    text = re.sub(r'[\u064B-\u065F\u0670]', '', text)

    # Normalize Arabic letters
    text = re.sub(r'[إأآا]', 'ا', text)
    text = re.sub(r'[ىي]', 'ی', text)
    text = re.sub(r'ة', 'ه', text)

    # Normalize whitespace and punctuation
    text = re.sub(r'\s+', ' ', text).strip()
    text = re.sub(r'[.,!?;:،؟\-–—\[\]\(\)\'\"…]+', ' ', text)
    text = re.sub(r'\s+', ' ', text).strip()

    return text.lower()


def get_words(text, n=10):
    """Get first N significant words from text."""
    normalized = normalize_text(text)
    words = [w for w in normalized.split() if len(w) > 2]
    return set(words[:n])


def similarity(text1, text2):
    """Calculate similarity ratio between two texts."""
    if not text1 or not text2:
        return 0.0
    n1, n2 = normalize_text(text1)[:300], normalize_text(text2)[:300]
    return SequenceMatcher(None, n1, n2).ratio()


def load_tmp_prayers():
    """Load TMP-coded prayers from CSV."""
    prayers = []
    with open(f'{DATA_DIR}/tmp_prayers_export.csv', 'r', encoding='utf-8') as f:
        reader = csv.DictReader(f)
        for row in reader:
            prayers.append(row)
    return prayers


def load_inventory_indexed():
    """Load inventory with word-based index for fast lookup."""
    entries = []
    word_index = defaultdict(set)  # word -> set of entry indices

    with open(f'{DATA_DIR}/inventory_export.csv', 'r', encoding='utf-8') as f:
        reader = csv.DictReader(f)
        for i, row in enumerate(reader):
            entries.append(row)

            # Index by words in both fields
            for field in ['First line (original)', 'First line (translated)']:
                text = row.get(field, '')
                if text:
                    for word in get_words(text, 15):
                        word_index[word].add(i)

    return entries, word_index


def find_candidates(prayer_text, word_index, entries, lang_code, max_candidates=100):
    """Find candidate entries using word overlap."""
    prayer_words = get_words(prayer_text, 15)

    # Count how many words match each entry
    candidate_scores = defaultdict(int)
    for word in prayer_words:
        for idx in word_index.get(word, []):
            candidate_scores[idx] += 1

    # Get top candidates by word overlap
    sorted_candidates = sorted(candidate_scores.items(), key=lambda x: x[1], reverse=True)
    return [entries[idx] for idx, _ in sorted_candidates[:max_candidates]]


def find_best_matches(prayer_text, candidates, lang_code, top_n=5):
    """Find best matches from candidates using full similarity."""
    if not candidates:
        return []

    match_field = 'First line (translated)' if lang_code == 'en' else 'First line (original)'

    matches = []
    for entry in candidates:
        inv_text = entry.get(match_field, '')
        if not inv_text:
            continue

        score = similarity(prayer_text, inv_text)
        if score > 0.25:
            matches.append({
                'pin': entry['PIN'],
                'score': score,
                'matched_text': inv_text[:150]
            })

    matches.sort(key=lambda x: x['score'], reverse=True)
    return matches[:top_n]


def main():
    print("=" * 70)
    print("TMP Code Resolution Tool (Optimized)")
    print("=" * 70)

    # Load data
    print("\nLoading TMP-coded prayers...")
    tmp_prayers = load_tmp_prayers()
    print(f"Found {len(tmp_prayers)} prayers with TMP codes")

    # Group by language
    by_language = defaultdict(list)
    for prayer in tmp_prayers:
        by_language[prayer['language']].append(prayer)

    print("\nBreakdown by language:")
    for lang, prayers in sorted(by_language.items()):
        print(f"  {lang}: {len(prayers)}")

    print("\nLoading and indexing inventory...")
    inventory, word_index = load_inventory_indexed()
    print(f"Indexed {len(inventory)} entries with {len(word_index)} unique words")

    # Process source languages
    results = {
        'high_confidence': [],
        'medium_confidence': [],
        'low_confidence': [],
        'no_match': []
    }

    for lang in ['en', 'ar', 'fa']:
        if lang not in by_language:
            continue

        print(f"\n{'=' * 50}")
        print(f"Processing {lang.upper()} ({len(by_language[lang])} prayers)")
        print('=' * 50)

        for prayer in by_language[lang]:
            tmp_code = prayer['phelps']
            text = prayer['text']
            version = prayer['version']

            # Fast candidate filtering
            candidates = find_candidates(text, word_index, inventory, lang)

            # Detailed matching on candidates
            matches = find_best_matches(text, candidates, lang)

            if matches:
                best = matches[0]
                result = {
                    'tmp_code': tmp_code,
                    'language': lang,
                    'version': version,
                    'prayer_text': text[:200] if text else '',
                    'best_pin': best['pin'],
                    'confidence': best['score'],
                    'matched_text': best['matched_text'],
                    'alternatives': [m['pin'] for m in matches[1:3]]
                }

                if best['score'] >= HIGH_CONFIDENCE_THRESHOLD:
                    results['high_confidence'].append(result)
                    print(f"  ✅ {tmp_code} -> {best['pin']} ({best['score']:.1%})")
                elif best['score'] >= MEDIUM_CONFIDENCE_THRESHOLD:
                    results['medium_confidence'].append(result)
                    print(f"  🟡 {tmp_code} -> {best['pin']}? ({best['score']:.1%})")
                else:
                    results['low_confidence'].append(result)
                    print(f"  🔴 {tmp_code} -> {best['pin']}? ({best['score']:.1%})")
            else:
                results['no_match'].append({
                    'tmp_code': tmp_code,
                    'language': lang,
                    'version': version,
                    'prayer_text': text[:200] if text else ''
                })
                print(f"  ❌ {tmp_code} - no candidates")

    # Summary
    print("\n" + "=" * 70)
    print("SUMMARY")
    print("=" * 70)
    high = len(results['high_confidence'])
    med = len(results['medium_confidence'])
    low = len(results['low_confidence'])
    none = len(results['no_match'])
    total = high + med + low + none

    print(f"High confidence (>= {HIGH_CONFIDENCE_THRESHOLD:.0%}):   {high:3d} ({100*high/total:.1f}%)")
    print(f"Medium confidence ({MEDIUM_CONFIDENCE_THRESHOLD:.0%}-{HIGH_CONFIDENCE_THRESHOLD:.0%}): {med:3d} ({100*med/total:.1f}%)")
    print(f"Low confidence (< {MEDIUM_CONFIDENCE_THRESHOLD:.0%}):    {low:3d} ({100*low/total:.1f}%)")
    print(f"No match:                       {none:3d} ({100*none/total:.1f}%)")
    print(f"Total processed:                {total:3d}")

    # Save results
    with open(f'{DATA_DIR}/tmp_resolution_results.json', 'w', encoding='utf-8') as f:
        json.dump(results, f, indent=2, ensure_ascii=False)

    # Generate SQL for high-confidence
    with open(f'{DATA_DIR}/tmp_resolution_high_confidence.sql', 'w', encoding='utf-8') as f:
        f.write("-- High-confidence TMP code resolutions\n\n")
        for r in results['high_confidence']:
            pin = r['best_pin'].replace("'", "''")
            ver = r['version'].replace("'", "''")
            f.write(f"UPDATE writings SET phelps = '{pin}' WHERE version = '{ver}' AND language = '{r['language']}';\n")
            f.write(f"-- {r['tmp_code']} -> {r['best_pin']} ({r['confidence']:.1%})\n\n")

    # Save items needing LLM review
    llm_items = results['medium_confidence'] + results['low_confidence'] + results['no_match']
    with open(f'{DATA_DIR}/tmp_for_llm_review.json', 'w', encoding='utf-8') as f:
        json.dump(llm_items, f, indent=2, ensure_ascii=False)

    print(f"\nFiles saved to {DATA_DIR}/")
    print(f"  - tmp_resolution_results.json (all results)")
    print(f"  - tmp_resolution_high_confidence.sql ({high} auto-apply)")
    print(f"  - tmp_for_llm_review.json ({len(llm_items)} need LLM/manual review)")


if __name__ == '__main__':
    main()
