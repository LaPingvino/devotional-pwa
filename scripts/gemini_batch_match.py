#!/usr/bin/env python
"""
gemini_batch_match.py — identify Bahá'í prayer phelps codes using Gemini CLI batching.

For each unresolved (NULL/empty phelps) prayer, Gemini identifies the official English
opening phrase. We then match that phrase against the Phelps inventory.

Usage:
  python scripts/gemini_batch_match.py [--lang de] [--batch-size 12] [--dry-run]

Outputs:
  SQL UPDATE statements to stdout
  Skipped/uncertain prayers to stderr
"""

import csv
import json
import os
import re
import subprocess
import sys
import argparse
import time
from collections import defaultdict

INVENTORY_CSV = "/home/joop/prayermatching/data/inventory_export.csv"
PRAYERS_CSV = "/tmp/unresolved_prayers.csv"
BAHAIWRITINGS_DIR = os.path.expanduser('~/bahaiwritings')

# Codes that are tablets/addresses, not standalone prayers.
# Gemini frequently false-matches prayer openings against these — skip them all.
FALSE_POSITIVE_CODES = {
    'ABU0030',   # "I come from distant countries… O Thou forgiving Lord!" (address)
    'ABU0196',   # "I wish to speak upon divine unity… O Thou kind Lord!" (address)
    'ABU0394',   # "You are welcome… O Thou merciful God! O Thou mighty!" (address)
    'AB00049',   # Letter to Americans — matches teaching-journey prayer openings
}

GEMINI_PROMPT_TEMPLATE = """\
Answer from your existing knowledge only — do not search the web.

Below are Bahá'í prayers in {language}. For each, give the EXACT official English \
opening phrase (first 8-10 words) as translated by Shoghi Effendi or the Universal \
House of Justice.

Return ONLY a JSON array, no markdown fences:
[{{"id":"SOURCE_ID","en":"exact english opening phrase","conf":"H"}}]

conf "H" = confident, "L" = uncertain. Omit prayers you cannot identify at all.

Prayers:
{prayers}"""

GEMINI_PROMPT_TRANSLATE = """\
Answer from your existing knowledge only — do not search the web.

Below are Bahá'í prayers in {language}. For each prayer, provide a literal English \
translation of the first 10-15 words. If you recognize the official English translation \
(by Shoghi Effendi or the Universal House of Justice), use that instead and mark conf "H". \
Otherwise translate literally and mark conf "L".

Return ONLY a JSON array, no markdown fences:
[{{"id":"SOURCE_ID","en":"english translation of opening","conf":"H or L"}}]

Prayers:
{prayers}"""

GEMINI_PROMPT_PRETRANSLATE = """\
Answer from your existing knowledge only — do not search the web.

Below are Bahá'í prayers with machine-translated English. Each entry shows the original text \
and a rough English translation. Using the English translation as a guide, identify the EXACT \
official English opening phrase (first 8-10 words) as published by Shoghi Effendi or the \
Universal House of Justice. If you can identify it confidently, mark conf "H". \
If uncertain, provide the machine translation as-is and mark conf "L".

Return ONLY a JSON array, no markdown fences:
[{{"id":"SOURCE_ID","en":"exact official english opening","conf":"H"}}]

Prayers:
{prayers}"""


def load_lang_phelps_texts(lang):
    """Load existing (phelps -> set of text prefixes) for a language from the DB.
    Used to detect duplicate assignments: if a phelps is already assigned to a
    different prayer text in this language, block it as a likely false positive."""
    result = subprocess.run(
        ['dolt', 'sql', '-q',
         f"SELECT phelps, LEFT(text,120) as t FROM writings WHERE source='bahaiprayers.net' AND language='{lang}' AND phelps IS NOT NULL AND phelps <> ''",
         '--result-format', 'csv'],
        capture_output=True, text=True, cwd=BAHAIWRITINGS_DIR
    )
    phelps_texts = defaultdict(set)
    for row in csv.DictReader(result.stdout.splitlines()):
        if row.get('phelps') and row.get('t'):
            # Normalize text prefix for comparison
            phelps_texts[row['phelps']].add(normalize_en(row['t'][:60]))
    return phelps_texts


def load_inventory():
    """Returns dict: normalized_en_phrase -> list of PINs."""
    inv_by_phrase = {}
    inv_by_pin = {}
    with open(INVENTORY_CSV, newline='', encoding='utf-8') as f:
        for row in csv.DictReader(f):
            pin = row['PIN'].strip()
            en = row.get('First line (translated)', '').strip()
            inv_by_pin[pin] = en
            if en:
                key = normalize_en(en)
                if key not in inv_by_phrase:
                    inv_by_phrase[key] = []
                inv_by_phrase[key].append(pin)
    return inv_by_phrase, inv_by_pin


def normalize_en(text):
    """Lowercase, strip punctuation, collapse spaces."""
    text = text.lower()
    text = re.sub(r"[^\w\s']", ' ', text)
    return re.sub(r'\s+', ' ', text).strip()


def match_phrase_to_inventory(en_phrase, inv_by_phrase, min_words=5):
    """
    Find inventory PINs whose English first-line contains the given phrase.
    Tries progressively shorter substrings (full phrase → 7 words → 5 words).
    Returns list of (pin, matched_words) sorted by matched_words desc.
    """
    phrase_norm = normalize_en(en_phrase)
    words = phrase_norm.split()
    if len(words) < min_words:
        return []

    results = []
    # Try from full length down to min_words
    for length in range(len(words), min_words - 1, -1):
        for offset in range(0, min(len(words) - length + 1, 4)):
            sub = ' '.join(words[offset:offset + length])
            if len(sub) < 15:
                continue
            for key, pins in inv_by_phrase.items():
                if sub in key:
                    for pin in pins:
                        if pin not in [r[0] for r in results]:
                            results.append((pin, length))
        if results:
            break  # Stop at longest match that finds something

    return sorted(results, key=lambda x: -x[1])


def verify_match(en_phrase, pin, inv_by_pin, min_verify_words=6):
    """
    Verify that Gemini's phrase actually appears in the full inventory entry.
    Checks if the first min_verify_words words of Gemini's phrase appear
    as a contiguous substring in the full inventory first-line text.
    Returns (verified: bool, matched_words: int).
    """
    full_inv = normalize_en(inv_by_pin.get(pin, ''))
    if not full_inv:
        return False, 0
    phrase_norm = normalize_en(en_phrase)
    words = phrase_norm.split()
    for length in range(len(words), min_verify_words - 1, -1):
        sub = ' '.join(words[:length])
        if len(sub) < 20:
            continue
        if sub in full_inv:
            return True, length
    return False, 0


_STOP_WORDS = {
    'o', 'my', 'thy', 'thine', 'the', 'a', 'an', 'is', 'are', 'was',
    'i', 'of', 'in', 'to', 'and', 'for', 'thou', 'thee', 'ye', 'we',
    'be', 'on', 'at', 'by', 'not', 'so', 'he', 'she', 'it', 'his',
    'all', 'as', 'do', 'us', 'me', 'am', 'no', 'that', 'who', 'our',
}


def generate_mnemonic(phrase):
    """Generate 3-letter mnemonic from first significant word in phrase."""
    words = re.sub(r"[^\w\s]", '', phrase).split()
    for word in words:
        if word.lower() not in _STOP_WORDS and len(word) >= 3:
            return re.sub(r'[^A-Z]', '', word.upper())[:3]
    letters = ''.join(w[0].upper() for w in words if w and w[0].isalpha())[:3]
    return letters.ljust(3, 'X')


def load_subpassage_index():
    """
    Load existing sub-passage codes and their English opening text from the writings table.
    Sub-passage codes match pattern: base_pin (2-3 letters + 4-5 digits) + 3+ uppercase letters.
    Returns dict: base_pin -> list of (sub_code, normalized_en_opening).
    """
    sub_index = defaultdict(list)
    try:
        result = subprocess.run(
            ['dolt', 'sql', '-q',
             "SELECT DISTINCT phelps, LEFT(text, 200) as text FROM writings "
             "WHERE phelps REGEXP '^[A-Z]{2,3}[0-9]{4,5}[A-Z]{3}' "
             "AND language='en' AND source='bahaiprayers.net' ORDER BY phelps",
             '--result-format', 'csv'],
            capture_output=True, text=True, cwd=BAHAIWRITINGS_DIR, timeout=30
        )
        seen = set()
        for row in csv.DictReader(result.stdout.splitlines()):
            code = row.get('phelps', '').strip()
            text = row.get('text', '').strip()
            m = re.match(r'^([A-Z]{2,3}[0-9]{4,5})([A-Z]{3,})$', code)
            if m and code not in seen:
                seen.add(code)
                sub_index[m.group(1)].append((code, normalize_en(text[:200])))
    except Exception as e:
        print(f"Warning: could not load sub-passage index: {e}", file=sys.stderr)
    return sub_index


def find_best_subpassage(en_phrase, base_pin, sub_index, min_overlap=4):
    """
    Find the best matching sub-passage code for en_phrase under base_pin.
    Uses word overlap against the English opening stored in inventory_fulltext.
    Returns (sub_code, score) or None if no good match.
    """
    candidates = sub_index.get(base_pin, [])
    if not candidates:
        return None
    phrase_words = set(normalize_en(en_phrase).split()) - _STOP_WORDS
    if len(phrase_words) < 2:
        return None
    best_code, best_score = None, 0
    for code, norm_en in candidates:
        inv_words = set(norm_en.split()) - _STOP_WORDS
        overlap = len(phrase_words & inv_words)
        if overlap > best_score:
            best_code, best_score = code, overlap
    if best_code and best_score >= min_overlap:
        return best_code, best_score
    return None


def call_gemini(prompt, retries=2, delay=5):
    """Call gemini CLI in non-interactive mode. Returns stdout string."""
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


def extract_json(text):
    """Extract JSON array from gemini response (handles markdown fences)."""
    # Strip markdown fences if present
    text = re.sub(r'```[a-z]*\n?', '', text).strip()
    # Find first [ ... ]
    match = re.search(r'\[[\s\S]*\]', text)
    if not match:
        return []
    try:
        return json.loads(match.group(0))
    except json.JSONDecodeError:
        return []


def lang_name(code):
    names = {
        'de': 'German', 'pt': 'Portuguese', 'fr': 'French', 'no': 'Norwegian',
        'sv': 'Swedish', 'da': 'Danish', 'fi': 'Finnish', 'nl': 'Dutch',
        'hr': 'Croatian', 'sr': 'Serbian', 'sk': 'Slovak', 'cs': 'Czech',
        'sl': 'Slovenian', 'pl': 'Polish', 'hu': 'Hungarian', 'ro': 'Romanian',
        'bg': 'Bulgarian', 'ru': 'Russian', 'uk': 'Ukrainian', 'be': 'Belarusian',
        'sq': 'Albanian', 'lv': 'Latvian', 'lt': 'Lithuanian', 'et': 'Estonian',
        'id': 'Indonesian', 'ms': 'Malay', 'tl': 'Filipino', 'vi': 'Vietnamese',
        'th': 'Thai', 'ko': 'Korean', 'ja': 'Japanese',
        'zh-Hans': 'Chinese (Simplified)', 'zh-Hant': 'Chinese (Traditional)',
        'hi': 'Hindi', 'ur': 'Urdu', 'bn': 'Bengali', 'ml': 'Malayalam',
        'kn': 'Kannada', 'hy': 'Armenian', 'az': 'Azerbaijani', 'ky': 'Kyrgyz',
        'ka': 'Georgian', 'am': 'Amharic', 'sw': 'Swahili', 'af': 'Afrikaans',
        'is': 'Icelandic', 'ca': 'Catalan', 'es': 'Spanish', 'it': 'Italian',
        'el': 'Greek', 'tr': 'Turkish', 'he': 'Hebrew', 'mk': 'Macedonian',
        'hz': 'Herero', 'fj': 'Fijian', 'lg': 'Luganda', 'mg': 'Malagasy',
        'lkt': 'Lakota', 'ht': 'Haitian Creole', 'cy': 'Welsh', 'co': 'Corsican',
    }
    return names.get(code, code)


def pretranslate_text(text, from_lang, delay=1.0):
    """Pre-translate text using translate-shell (trans). Returns English string or None."""
    try:
        result = subprocess.run(
            ['trans', '-b', f'{from_lang}:en', text[:400]],
            capture_output=True, text=True, timeout=30
        )
        time.sleep(delay)
        if result.returncode == 0 and result.stdout.strip():
            return result.stdout.strip()
    except (subprocess.TimeoutExpired, FileNotFoundError, Exception):
        pass
    return None


def format_batch(prayers, batch_num, total_batches, romanize=False, pretranslate_lang=None):
    lines = []
    if romanize:
        try:
            from unidecode import unidecode
        except ImportError:
            romanize = False
    for i, p in enumerate(prayers, 1):
        text = p['text'].strip()
        # Strip leading section header lines (* ... or # ...)
        text = re.sub(r'^[\*#][^\n]*\n*', '', text, flags=re.MULTILINE).strip()
        # Collapse newlines and multiple spaces
        text = re.sub(r'\s+', ' ', text).strip()
        text = text[:300]
        if romanize:
            text = unidecode(text)
        if pretranslate_lang:
            translated = pretranslate_text(text, pretranslate_lang)
            if translated:
                lines.append(f"[{i}] id={p['source_id']}\nOriginal: {text[:150]}\nEnglish: {translated[:200]}")
            else:
                lines.append(f"[{i}] id={p['source_id']}\n{text[:180]}")
        else:
            lines.append(f"[{i}] id={p['source_id']}\n{text[:180]}")
    return '\n\n'.join(lines)


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument('--lang', help='Process only this language code (e.g. de)')
    parser.add_argument('--batch-size', type=int, default=12)
    parser.add_argument('--dry-run', action='store_true', help='Print prompts only')
    parser.add_argument('--min-match-words', type=int, default=5,
                        help='Minimum words required for inventory match')
    parser.add_argument('--min-verify-words', type=int, default=6,
                        help='Minimum words of Gemini phrase that must appear in full inventory entry')
    parser.add_argument('--retry-conf-l', action='store_true',
                        help='Include conf:L results in output (flagged)')
    parser.add_argument('--translate', action='store_true',
                        help='Use translation prompt instead of knowledge-only (for hard languages)')
    parser.add_argument('--romanize', action='store_true',
                        help='Transliterate text to ASCII via unidecode before sending to Gemini (helps for Hangul, Devanagari, Thai, etc.)')
    parser.add_argument('--pretranslate', action='store_true',
                        help='Pre-translate prayer text to English via translate-shell (trans) before sending to Gemini. Useful for languages Gemini refuses to translate (e.g. Korean).')
    args = parser.parse_args()

    print("Loading inventory...", file=sys.stderr)
    inv_by_phrase, inv_by_pin = load_inventory()
    print(f"  {len(inv_by_phrase)} phrases indexed", file=sys.stderr)

    print("Loading sub-passage index...", file=sys.stderr)
    sub_index = load_subpassage_index()
    sub_count = sum(len(v) for v in sub_index.values())
    print(f"  {sub_count} sub-passage codes across {len(sub_index)} base PINs", file=sys.stderr)

    print("Loading unresolved prayers...", file=sys.stderr)
    prayers_by_lang = defaultdict(list)
    with open(PRAYERS_CSV, newline='', encoding='utf-8') as f:
        for row in csv.DictReader(f):
            lang = row['language']
            if args.lang and lang != args.lang:
                continue
            prayers_by_lang[lang].append(row)

    total_prayers = sum(len(v) for v in prayers_by_lang.values())
    print(f"  {total_prayers} prayers across {len(prayers_by_lang)} languages", file=sys.stderr)
    print(file=sys.stderr)

    all_matches = []      # (source_id, lang, pin, matched_words, conf)
    all_uncertain = []    # (source_id, lang, en_phrase) — conf:L or no inv match
    all_unknown = []      # source_ids Gemini couldn't identify

    for lang, prayers in sorted(prayers_by_lang.items()):
        lname = lang_name(lang)
        batches = [prayers[i:i+args.batch_size]
                   for i in range(0, len(prayers), args.batch_size)]
        print(f"\n{lname} ({lang}): {len(prayers)} prayers, {len(batches)} batches",
              file=sys.stderr)

        answered_ids = set()
        # Load existing phelps→text mapping to detect same-code duplicates
        phelps_texts_db = load_lang_phelps_texts(lang)

        for bi, batch in enumerate(batches):
            pretranslate_lang = lang if (args.pretranslate and not args.dry_run) else None
            batch_text = format_batch(batch, bi, len(batches), romanize=args.romanize,
                                      pretranslate_lang=pretranslate_lang)
            if args.pretranslate:
                template = GEMINI_PROMPT_PRETRANSLATE
            elif args.translate:
                template = GEMINI_PROMPT_TRANSLATE
            else:
                template = GEMINI_PROMPT_TEMPLATE
            prompt = template.format(language=lname, prayers=batch_text)

            if args.dry_run:
                print(f"--- PROMPT ({lang} batch {bi+1}/{len(batches)}) ---")
                print(prompt[:500])
                print()
                continue

            print(f"  Batch {bi+1}/{len(batches)}...", end=' ', file=sys.stderr)
            raw = call_gemini(prompt)

            if not raw:
                print("FAILED (no response)", file=sys.stderr)
                for p in batch:
                    all_unknown.append((p['source_id'], lang))
                continue

            results = extract_json(raw)
            if not results:
                print(f"FAILED (no JSON in: {raw[:100]!r})", file=sys.stderr)
                for p in batch:
                    all_unknown.append((p['source_id'], lang))
                continue

            identified = 0
            matched = 0
            for item in results:
                sid = str(item.get('id', '')).strip()
                en_phrase = item.get('en', '').strip()
                conf = item.get('conf', 'H').upper()

                if not sid or not en_phrase:
                    continue

                answered_ids.add(sid)
                identified += 1

                # Flag conf:L entries
                if conf == 'L' and not args.retry_conf_l:
                    all_uncertain.append((sid, lang, en_phrase))
                    continue

                # Match against inventory
                inv_matches = match_phrase_to_inventory(
                    en_phrase, inv_by_phrase, args.min_match_words)

                if not inv_matches:
                    print(f"\n    No inventory match for id={sid}: {en_phrase!r}",
                          file=sys.stderr)
                    all_uncertain.append((sid, lang, en_phrase))
                    continue

                top_pin, top_words = inv_matches[0]

                # Reject false-positive tablet/address codes
                if top_pin in FALSE_POSITIVE_CODES:
                    print(f"\n    FALSE-POSITIVE blocked id={sid}: {top_pin} "
                          f"({en_phrase[:60]!r})", file=sys.stderr)
                    all_uncertain.append((sid, lang, en_phrase))
                    continue

                # Reject duplicate assignments: if this code already appears in
                # this language for a different prayer text, it's a false positive
                prayer_text_key = normalize_en(
                    next((p['text'] for p in batch if p['source_id'] == sid), '')[:60]
                )
                existing_texts = phelps_texts_db.get(top_pin, set())
                if existing_texts and prayer_text_key not in existing_texts:
                    print(f"\n    DUPLICATE-BLOCK id={sid}: {top_pin} already used "
                          f"for different text in {lang}", file=sys.stderr)
                    all_uncertain.append((sid, lang, en_phrase))
                    continue

                # Verify the match: Gemini's phrase must appear in full inventory text
                verified, verify_words = verify_match(
                    en_phrase, top_pin, inv_by_pin, args.min_verify_words)
                base_pin = top_pin
                is_new_subcode = False
                if not verified:
                    print(f"\n    VERIFY WARN id={sid}: {en_phrase!r} not in {top_pin} "
                          f"({inv_by_pin.get(top_pin, '')[:60]!r})",
                          file=sys.stderr)
                    # Check if this base PIN has sub-passage codes (writing contains
                    # multiple distinct prayers). If so, find/create a specific sub-code.
                    sub_match = find_best_subpassage(en_phrase, base_pin, sub_index)
                    if sub_match:
                        sub_code, sub_score = sub_match
                        top_pin = sub_code
                        print(f"\n    SUB-PASSAGE id={sid}: matched existing {sub_code} "
                              f"(overlap={sub_score})", file=sys.stderr)
                    elif sub_index.get(base_pin):
                        # Sub-codes exist for this writing but none matched well —
                        # generate a new distinguishing code
                        mnemonic = generate_mnemonic(en_phrase)
                        top_pin = f"{base_pin}{mnemonic}"
                        is_new_subcode = True
                        existing = [c for c, _ in sub_index[base_pin]]
                        print(f"\n    SUB-PASSAGE NEW id={sid}: generated {top_pin} "
                              f"(existing: {existing})", file=sys.stderr)

                all_matches.append((sid, lang, top_pin, top_words, conf,
                                    en_phrase, inv_by_pin.get(base_pin, ''),
                                    verified, is_new_subcode))

                # Warn if multiple inventory matches
                if len(inv_matches) > 1:
                    other_pins = [p for p, _ in inv_matches[1:3]]
                    print(f"\n    MULTI-MATCH id={sid}: {top_pin} (also: {other_pins})",
                          file=sys.stderr)
                matched += 1

            # Unknown = in batch but not returned by Gemini
            for p in batch:
                if p['source_id'] not in answered_ids:
                    all_unknown.append((p['source_id'], lang))

            print(f"identified={identified}, matched={matched}", file=sys.stderr)

            # Small delay between calls to avoid rate limiting
            if bi < len(batches) - 1:
                time.sleep(1)

    if args.dry_run:
        return

    # Output SQL UPDATE statements
    print("\n-- SQL UPDATE statements --")
    print(f"-- Total matches: {len(all_matches)}")
    print(f"-- Uncertain (no inv match or conf:L): {len(all_uncertain)}")
    print(f"-- Unknown (Gemini couldn't identify): {len(all_unknown)}")
    print()

    for sid, lang, pin, words, conf, en_phrase, inv_en, verified, is_new_subcode in all_matches:
        flags = []
        if conf == 'L':
            flags.append("CONF:L")
        sub_resolved = len(pin) > 7  # pin corrected to a sub-passage code
        if not verified and not sub_resolved:
            flags.append("VERIFY-WARN")
        if is_new_subcode:
            flags.append("NEW-SUBCODE")
        flag_str = ("-- " + " ".join(flags) + " ") if flags else ""
        print(f"{flag_str}-- id={sid} ({lang}) en=\"{en_phrase[:60]}\" inv=\"{inv_en[:60]}\"")
        print(f"UPDATE writings SET phelps='{pin}' WHERE source_id='{sid}' "
              f"AND language='{lang}' AND source='bahaiprayers.net';")
        print()

    # Write retry list
    if all_uncertain:
        print("\n-- RETRY LIST (uncertain / no inventory match) --")
        for sid, lang, en_phrase in all_uncertain:
            print(f"-- {lang}/{sid}: {en_phrase!r}")

    if all_unknown:
        print("\n-- UNKNOWN (Gemini couldn't identify) --")
        for sid, lang in all_unknown:
            print(f"-- {lang}/{sid}")


if __name__ == '__main__':
    main()
