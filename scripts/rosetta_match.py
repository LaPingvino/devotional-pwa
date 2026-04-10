#!/usr/bin/env python3
"""
rosetta_match.py - Match prayers for rare languages using cross-language context.

Workflow:
  1. --build-vocab LANG  : extract vocabulary & category mappings from matched prayers
  2. (default) LANG      : match unresolved prayers using rosetta context + Gemini

The rosetta_vocab table stores:
  - category: prayer book section headers (e.g. "Ataei" = "Children")
  - address : opening address forms (e.g. "O te Atua" = "O God")
  - deity   : words for God/Lord/Thou etc.
  - keyword : distinctive prayer words
  - phrase  : multi-word phrases with known meaning
"""

import sys, os, csv, re, json, subprocess, argparse, time
from collections import defaultdict

GEMINI_MODEL = "gemini-2.5-flash-lite"
DOLT_DIR = os.path.expanduser("~/bahaiwritings")
INVENTORY_CSV = os.path.expanduser("~/prayermatching/data/inventory_export.csv")
UNRESOLVED_CSV = "/tmp/unresolved_prayers.csv"

# Reference languages for cross-language examples (well-matched, diverse)
REFERENCE_LANGS = ['en', 'de', 'fr', 'sw', 'sm', 'id', 'pt', 'nl']

# Known English category names in the prayer book
KNOWN_EN_CATEGORIES = [
    "Aid and Assistance", "Children", "Departed", "Detachment", "The Fast",
    "Forgiveness", "Gatherings", "Healing", "Marriage", "Morning", "Evening",
    "Protection", "Praise and Gratitude", "Ridván", "Spiritual Growth",
    "Steadfastness", "Teaching", "Tests and Difficulties", "Women", "Youth",
    "Obligatory Prayers", "Short Obligatory Prayer", "Medium Obligatory Prayer",
    "Long Obligatory Prayer", "Additional Prayers Revealed by Bahá'u'lláh",
    "Additional Prayers Revealed by 'Abdu'l‑Bahá", "Huqúqu'lláh",
    "Tablets", "Writings of the Báb", "Other",
]


def dolt_query(sql):
    r = subprocess.run(
        ["dolt", "sql", "-q", sql, "--result-format", "csv"],
        capture_output=True, text=True, cwd=DOLT_DIR
    )
    if r.returncode != 0:
        print(f"  [dolt error] {r.stderr.strip()}", file=sys.stderr)
        return []
    return list(csv.DictReader(r.stdout.splitlines()))


def dolt_exec(sql):
    r = subprocess.run(
        ["dolt", "sql", "-q", sql],
        capture_output=True, text=True, cwd=DOLT_DIR
    )
    if r.returncode != 0:
        print(f"  [dolt exec error] {r.stderr.strip()}", file=sys.stderr)
    return r.returncode == 0


def gemini_call(prompt, retries=3):
    for attempt in range(retries):
        try:
            r = subprocess.run(
                ["gemini", "-m", GEMINI_MODEL],
                input=prompt, capture_output=True, text=True, timeout=90
            )
            text = r.stdout.strip()
            if text:
                return text
        except subprocess.TimeoutExpired:
            print(f"  [timeout] Gemini timed out (attempt {attempt+1})", file=sys.stderr)
        time.sleep(3 * (attempt + 1))
    return ""


def load_inventory():
    inv = {}
    with open(INVENTORY_CSV) as f:
        for row in csv.DictReader(f):
            pin = row.get('PIN', '').strip()
            first_en = (row.get('First line (translated)') or row.get('First line (original)') or '').strip()
            if pin:
                inv[pin] = first_en
    return inv


def load_rosetta_vocab(lang):
    rows = dolt_query(f"SELECT local_term, en_meaning, term_type FROM rosetta_vocab WHERE language='{lang}'")
    vocab = {'category': {}, 'address': {}, 'deity': {}, 'keyword': {}, 'phrase': {}}
    for r in rows:
        ttype = r.get('term_type', 'keyword')
        vocab[ttype][r['local_term']] = r['en_meaning']
    return vocab


def save_vocab_entry(lang, local_term, en_meaning, term_type, source_phelps=None):
    local_term_esc = local_term.replace("'", "''")
    en_meaning_esc = en_meaning.replace("'", "''")
    src = f"'{source_phelps}'" if source_phelps else "NULL"
    dolt_exec(
        f"INSERT INTO rosetta_vocab (language, local_term, en_meaning, term_type, source_phelps) "
        f"VALUES ('{lang}', '{local_term_esc}', '{en_meaning_esc}', '{term_type}', {src}) "
        f"ON DUPLICATE KEY UPDATE en_meaning=VALUES(en_meaning), term_type=VALUES(term_type)"
    )


def get_category_phelps(en_category):
    """Get ordered phelps codes for an English category."""
    rows = dolt_query(
        f"SELECT phelps_code FROM prayer_book_structure WHERE source_language='en' "
        f"AND category_name='{en_category.replace(chr(39), chr(39)+chr(39))}' ORDER BY order_in_category"
    )
    return [r['phelps_code'] for r in rows]


def get_reference_openings(phelps_codes, ref_langs=None):
    """Get opening phrases for candidate phelps codes in reference languages."""
    if not phelps_codes:
        return {}
    if ref_langs is None:
        ref_langs = REFERENCE_LANGS
    codes_str = "','".join(phelps_codes)
    langs_str = "','".join(ref_langs)
    rows = dolt_query(
        f"SELECT phelps, language, LEFT(text,120) as opening FROM writings "
        f"WHERE phelps IN ('{codes_str}') AND language IN ('{langs_str}') "
        f"AND source='bahaiprayers.net' ORDER BY phelps, language"
    )
    result = defaultdict(dict)
    for r in rows:
        # Clean markdown headers from opening
        opening = re.sub(r'^## .+\n\n?', '', r['opening'], flags=re.MULTILINE).strip()
        result[r['phelps']][r['language']] = opening
    return result


def annotate_with_vocab(text, vocab):
    """Mark up prayer text with known vocabulary translations."""
    annotations = []
    # Check all vocab types for matches
    for ttype in ['deity', 'address', 'phrase', 'keyword']:
        for local, en in sorted(vocab[ttype].items(), key=lambda x: -len(x[0])):
            if local.lower() in text.lower():
                annotations.append(f"  {local!r} = {en!r}")
    return annotations


def build_vocab_for_lang(lang, args):
    """Phase 1: extract vocabulary from matched prayers."""
    print(f"\n=== Building Rosetta vocabulary for {lang} ===", file=sys.stderr)

    # Step A: decode category headers
    rows = dolt_query(
        f"SELECT DISTINCT SUBSTRING_INDEX(text, '\\n', 1) as header FROM writings "
        f"WHERE language='{lang}' AND source='bahaiprayers.net'"
    )
    raw_headers = [r['header'].strip() for r in rows if r['header'].strip().startswith('##')]
    headers = [h[2:].strip() for h in raw_headers]  # strip ##

    # Skip headers already in vocab
    existing_cats = dolt_query(f"SELECT local_term FROM rosetta_vocab WHERE language='{lang}' AND term_type='category'")
    existing_set = {r['local_term'] for r in existing_cats}
    headers = [h for h in headers if h not in existing_set]

    if headers:
        print(f"  Decoding {len(headers)} new category headers...", file=sys.stderr)
        headers_text = "\n".join(f"- {h}" for h in headers)
        cats_str = "\n".join(KNOWN_EN_CATEGORIES)
        prompt = (
            f"These are prayer book chapter headings in language code '{lang}'.\n"
            f"Match each heading to the closest English Bahá'í prayer category name from the list below.\n"
            f"If none matches, write 'Other'.\n\n"
            f"Known English categories:\n{cats_str}\n\n"
            f"Headings to decode:\n{headers_text}\n\n"
            f"Reply ONLY with valid JSON like: {{\"Ataei\": \"Children\", \"Te Mare\": \"Marriage\"}}"
        )
        response = gemini_call(prompt)
        resp_clean = re.sub(r'```(?:json)?\s*', '', response).replace('```', '').strip()
        m = re.search(r'\{[\s\S]+\}', resp_clean)
        if m:
            try:
                mapping = json.loads(m.group())
                for local, en in mapping.items():
                    save_vocab_entry(lang, local, en, 'category')
                    print(f"    {local!r} -> {en!r}", file=sys.stderr)
            except json.JSONDecodeError:
                print(f"  [warn] JSON parse failed: {response[:200]}", file=sys.stderr)
        time.sleep(2)

    # Step B: extract vocabulary from matched prayers
    matched = dolt_query(
        f"SELECT source_id, phelps, LEFT(text,400) as text FROM writings "
        f"WHERE language='{lang}' AND source='bahaiprayers.net' "
        f"AND phelps IS NOT NULL AND phelps <> '' LIMIT 30"
    )
    if not matched:
        print(f"  No matched prayers to extract vocabulary from.", file=sys.stderr)
        return

    print(f"  Extracting vocabulary from {len(matched)} matched prayers...", file=sys.stderr)
    inv = load_inventory()

    # Batch matched prayers for vocab extraction
    batch_size = 5
    for i in range(0, len(matched), batch_size):
        batch = matched[i:i+batch_size]
        pairs = []
        for row in batch:
            en_first = inv.get(row['phelps'], '?')
            # Clean header from text
            text = re.sub(r'^## .+\n\n?', '', row['text'], flags=re.MULTILINE).strip()
            pairs.append(f"[{row['phelps']}] English first line: \"{en_first}\"\n{lang} text: {text[:200]}")

        pairs_text = "\n\n".join(pairs)
        prompt = (
            f"Below are Bahá'í prayers in language '{lang}', each paired with its English first line.\n"
            f"Extract a mini-dictionary of key vocabulary: words for God, Lord, O (address), Thy, Thou,\n"
            f"and any other common prayer terms you can identify from the pairs.\n\n"
            f"{pairs_text}\n\n"
            f"Reply ONLY with valid JSON mapping {lang} terms to English meanings.\n"
            f"Include: words for God/Lord/Thou/Thy, common address forms (O God! O Lord!),\n"
            f"and key terms like 'servant', 'praise', 'glory', 'mercy', 'forgiveness'.\n"
            f"Example: {{\"Atuau\": \"my God\", \"te Uea\": \"the Lord\", \"Ko\": \"Thou\"}}"
        )
        response = gemini_call(prompt)
        resp_clean2 = re.sub(r'```(?:json)?\s*', '', response).replace('```', '').strip()
        m = re.search(r'\{[\s\S]+\}', resp_clean2)
        if m:
            try:
                vocab_map = json.loads(m.group())
                for local, en in vocab_map.items():
                    if len(local) >= 2 and len(en) >= 2:
                        # Determine term type
                        en_lower = en.lower()
                        if any(w in en_lower for w in ['god', 'lord', 'thou', 'thy', 'thee', 'he is']):
                            ttype = 'deity'
                        elif en_lower.startswith('o '):
                            ttype = 'address'
                        else:
                            ttype = 'keyword'
                        save_vocab_entry(lang, local, en, ttype)
                        print(f"    [{ttype}] {local!r} = {en!r}", file=sys.stderr)
            except json.JSONDecodeError:
                print(f"  [warn] JSON parse failed for batch {i}", file=sys.stderr)
        time.sleep(3)

    print(f"  Vocabulary build complete for {lang}.", file=sys.stderr)


def match_prayers_for_lang(lang, args):
    """Phase 2: match unresolved prayers using rosetta context."""
    # Load vocabulary
    vocab = load_rosetta_vocab(lang)
    cat_map = vocab['category']  # local_header -> en_category
    print(f"\n=== Rosetta matching for {lang} ===", file=sys.stderr)
    print(f"  Vocab: {sum(len(v) for v in vocab.values())} terms, "
          f"{len(cat_map)} category mappings", file=sys.stderr)

    # Load inventory
    inv = load_inventory()

    # Load unresolved prayers for this language
    unresolved = []
    with open(UNRESOLVED_CSV) as f:
        for row in csv.DictReader(f):
            if row.get('language') == lang:
                unresolved.append(row)
    print(f"  {len(unresolved)} unresolved prayers", file=sys.stderr)

    if not unresolved:
        return

    # Group by category header
    by_category = defaultdict(list)
    for row in unresolved:
        text = row.get('text', '')
        header_match = re.match(r'^## (.+)', text.strip())
        header = header_match.group(1).strip() if header_match else '_none_'
        by_category[header].append(row)

    total_matches = 0
    total_uncertain = 0
    results = []

    for local_header, prayers in sorted(by_category.items()):
        # Find English category
        en_cat = cat_map.get(local_header)
        if not en_cat:
            # Try partial match
            for key, val in cat_map.items():
                if key.lower() in local_header.lower() or local_header.lower() in key.lower():
                    en_cat = val
                    break

        # Get candidate phelps for this category
        candidates = []
        if en_cat and en_cat != 'Other':
            candidates = get_category_phelps(en_cat)[:12]  # cap at 12 to keep prompt small
        if not candidates:
            en_cat = en_cat or local_header

        # Get cross-language examples for candidates
        ref_openings = get_reference_openings(candidates) if candidates else {}

        print(f"\n  Category: {local_header!r} -> {en_cat!r} "
              f"({len(candidates)} candidates, {len(prayers)} prayers)", file=sys.stderr)

        # Build candidate list for prompt
        if candidates:
            cand_lines = []
            for code in candidates:
                en_first = inv.get(code, '?')[:70]
                refs = ref_openings.get(code, {})
                # Prefer sm/id/sw for Pacific/SE Asian languages; limit to 2 refs
                pref = [l for l in ['sm', 'id', 'sw', 'de', 'fr', 'pt'] if l in refs][:2]
                ref_str = " | ".join(f"{l}: {refs[l][:45]!r}" for l in pref)
                cand_lines.append(f"  {code}: \"{en_first}\"" + (f" [{ref_str}]" if ref_str else ""))
            cand_text = "\n".join(cand_lines)
        else:
            print(f"    [skip] no category candidates for {local_header!r}", file=sys.stderr)
            total_uncertain += len(prayers)
            continue

        # Match each prayer in this category
        batch_size = args.batch_size
        for i in range(0, len(prayers), batch_size):
            batch = prayers[i:i+batch_size]

            prayer_blocks = []
            for row in batch:
                sid = row['source_id']
                text = row.get('text', '')
                # Strip header
                text_clean = re.sub(r'^## .+\n\n?', '', text, flags=re.MULTILINE).strip()
                # Annotate with known vocab
                annots = annotate_with_vocab(text_clean, vocab)
                annot_str = ("\nKnown terms: " + "; ".join(annots[:6])) if annots else ""
                prayer_blocks.append(
                    f"[id={sid}]{annot_str}\n{text_clean[:250]}"
                )

            prayers_text = "\n\n---\n\n".join(prayer_blocks)

            prompt = (
                f"You are matching Bahá'í prayers in language '{lang}' to their Phelps inventory codes.\n\n"
                f"Prayer book section: \"{local_header}\" (= \"{en_cat}\")\n\n"
                f"Candidate prayers for this section (Phelps code: English first line [cross-language examples]):\n"
                f"{cand_text}\n\n"
                f"For each prayer below, pick the BEST matching candidate using:\n"
                f"- The opening address to God (O God!, O Lord!, etc.)\n"
                f"- The main theme/request (forgiveness, healing, children, etc.)\n"
                f"- Known vocabulary annotations provided\n"
                f"- Cross-language examples where available\n\n"
                f"Prayers to identify:\n\n{prayers_text}\n\n"
                f"Reply with JSON: {{\"SOURCE_ID\": \"CODE_or_VERIFY:CODE\", ...}}\n"
                f"- Use exact inventory code (e.g. BH01234) if confident\n"
                f"- Use VERIFY:CODE (e.g. VERIFY:BH01234) if you think it matches but are not certain\n"
                f"- Use NULL only if the prayer clearly does NOT fit any candidate\n"
                f"Pick the best candidate from the list — do not leave things NULL just because you are unsure."
            )

            response = gemini_call(prompt)
            # Extract JSON from response (handle code blocks and trailing text)
            resp_clean = re.sub(r'```(?:json)?\s*', '', response).replace('```', '').strip()
            id_map = None
            for m in re.finditer(r'\{[^{}]+\}', resp_clean):
                try:
                    id_map = json.loads(m.group())
                    break
                except json.JSONDecodeError:
                    pass
            if id_map is None:
                # Try full greedy match as fallback
                m2 = re.search(r'\{[\s\S]+\}', resp_clean)
                if m2:
                    try:
                        id_map = json.loads(m2.group())
                    except json.JSONDecodeError:
                        pass
            if id_map is None:
                print(f"    [warn] No valid JSON in response: {response[:80]!r}", file=sys.stderr)
                for row in batch:
                    total_uncertain += 1
                continue

            for row in batch:
                sid = row['source_id']
                raw = id_map.get(sid) or id_map.get(str(sid)) or ''
                raw = raw.strip()
                verify = raw.upper().startswith('VERIFY:')
                matched_code = raw[7:].strip() if verify else raw
                # Strip prompt-echo garbage (e.g. "phelps_code:BH00074BLE")
                matched_code = re.sub(r'(?i)^(?:phelps[_\-]?code|pin|code):', '', matched_code).strip()
                if matched_code and matched_code.upper() not in ('NULL', 'NONE', ''):
                    en_first = inv.get(matched_code, '?')
                    text_open = row.get('text', '')[:60].replace('\n', ' ')
                    results.append({
                        'source_id': sid, 'language': lang,
                        'phelps': matched_code, 'en_first': en_first,
                        'text_open': text_open, 'en_cat': en_cat,
                        'verify': verify,
                    })
                    tag = 'VERIFY' if verify else 'MATCH'
                    print(f"    {tag} id={sid}: {matched_code} \"{en_first[:50]}\"", file=sys.stderr)
                    total_matches += 1
                else:
                    print(f"    no match id={sid}", file=sys.stderr)
                    total_uncertain += 1

            time.sleep(2)

    # Output SQL
    print(f"\n-- SQL UPDATE statements for {lang} (rosetta match) --", flush=True)
    print(f"-- Total matches: {total_matches}", flush=True)
    print(f"-- No match: {total_uncertain}", flush=True)
    print(flush=True)
    if results:
        print("SET FOREIGN_KEY_CHECKS=0;")
    for r in results:
        verify_tag = "-- VERIFY-WARN -- " if r.get('verify') else ""
        print(f"{verify_tag}-- {r['en_cat']} -- id={r['source_id']} ({lang}) en=\"{r['text_open'][:50]}\" match=\"{r['en_first'][:50]}\"")
        print(
            f"UPDATE writings SET phelps='{r['phelps']}' "
            f"WHERE source_id='{r['source_id']}' AND language='{lang}' AND source='bahaiprayers.net';"
        )
        print()
    if results:
        print("SET FOREIGN_KEY_CHECKS=1;")

    if total_uncertain > 0:
        print(f"\n-- RETRY LIST --")
        for row in unresolved:
            sid = row['source_id']
            if not any(r['source_id'] == sid for r in results):
                print(f"-- {lang}/{sid}: {row.get('text','')[:60].replace(chr(10),' ')!r}")


def main():
    parser = argparse.ArgumentParser(description='Rosetta Stone prayer matcher')
    parser.add_argument('--lang', required=True, help='Language code to process')
    parser.add_argument('--build-vocab', action='store_true',
                        help='Build/update vocabulary from matched prayers (run first)')
    parser.add_argument('--batch-size', type=int, default=3)
    args = parser.parse_args()

    lang = args.lang

    if args.build_vocab:
        build_vocab_for_lang(lang, args)
        print(f"\nVocabulary built. Run without --build-vocab to match prayers.", file=sys.stderr)
    else:
        # Check if we have vocab; if not, suggest building it first
        vocab = load_rosetta_vocab(lang)
        total_vocab = sum(len(v) for v in vocab.values())
        if total_vocab == 0:
            print(f"[warn] No vocabulary for {lang}. Consider running --build-vocab first.", file=sys.stderr)
        match_prayers_for_lang(lang, args)


if __name__ == '__main__':
    main()
