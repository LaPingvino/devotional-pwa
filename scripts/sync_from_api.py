#!/usr/bin/env python3
"""
Sync bahaiprayers.net prayers with the Dolt database.

For each language:
  - Fetches current prayers from the API
  - Compares with DB entries (by source_id)
  - Handles full replacements (all IDs changed but same content)
  - Carries phelps codes over when content matches closely enough

Outputs:
  /tmp/sync_report.txt     human-readable summary with matching difficulty hints
  /tmp/sync_inserts.sql    INSERT statements for new prayers (with phelps where carried)
  /tmp/sync_deletes.sql    DELETE statements for removed prayers (commented, review first!)
  /tmp/sync_unmatched.txt  prayers that need new phelps matching, by difficulty
"""

import csv
import json
import re
import subprocess
import sys
import urllib.request
import uuid
from html.parser import HTMLParser

API_BASE = "https://BahaiPrayers.net/api/prayer/"
LANG_CSV  = "/home/joop/bahaiprayers-static/rel/lang.csv"
SOURCE    = "bahaiprayers.net"

AUTHOR_MAP = {1: "Báb", 2: "Bahá'u'lláh", 3: "ʻAbdu'l-Bahá"}

# Languages needing special matching effort (from project context)
HARD_MATCH_LANGS = {'ur', 'hy', 'ml', 'kn', 'bn', 'th', 'ja', 'ko', 'zh-Hans', 'zh-Hant', 'hi', 'ta', 'te', 'gu', 'mr', 'am', 'km', 'lo', 'mn', 'fa', 'ar'}
CREOLE_LANGS     = {'bi', 'tpi', 'fj', 'srn', 'ht'}
INDIGENOUS_LANGS = {'lg', 'gil', 'hz', 'lkt', 'dak', 'meu', 'kiw', 'moh', 'gwi', 'hur', 'oj'}


# ---------------------------------------------------------------------------
# HTML → plain text
# ---------------------------------------------------------------------------
class HTMLStripper(HTMLParser):
    def __init__(self):
        super().__init__()
        self.parts = []

    def handle_starttag(self, tag, attrs):
        if tag in ('p', 'br', 'h1', 'h2', 'h3', 'h4', 'h5', 'h6', 'li'):
            if self.parts and self.parts[-1] != '\n':
                self.parts.append('\n')

    def handle_endtag(self, tag):
        if tag in ('p', 'h1', 'h2', 'h3', 'h4', 'h5', 'h6', 'li'):
            if self.parts and self.parts[-1] != '\n':
                self.parts.append('\n')

    def handle_data(self, data):
        self.parts.append(data)

    def get_text(self):
        text = ''.join(self.parts)
        text = re.sub(r'\n{3,}', '\n\n', text)
        return text.strip()


def html_to_text(html: str) -> str:
    s = HTMLStripper()
    s.feed(html)
    return s.get_text()


def text_fingerprint(text: str, chars=150) -> str:
    """Normalised first N chars for content-matching."""
    t = re.sub(r'\s+', ' ', text).strip()
    return t[:chars].lower()


def word_set(text: str) -> set:
    """All unique tokens in text. For CJK: character 3-grams. Otherwise: words."""
    # Detect if text is primarily CJK (Chinese/Japanese)
    cjk_count = sum(1 for c in text if '\u4e00' <= c <= '\u9fff' or '\u3040' <= c <= '\u30ff')
    if cjk_count > len(text) * 0.3:
        # Use character 3-grams for CJK
        t = re.sub(r'\s+', '', text)
        return {t[i:i+3] for i in range(len(t)-2)} if len(t) >= 3 else set(text)
    else:
        words = re.findall(r"[\w']+", text.lower())
        return set(words)


def jaccard(a: set, b: set) -> float:
    if not a or not b:
        return 0.0
    return len(a & b) / len(a | b)


# ---------------------------------------------------------------------------
# API helpers
# ---------------------------------------------------------------------------
def fetch_json(url: str):
    try:
        req = urllib.request.Request(url, headers={'User-Agent': 'bahaiprayers-sync/1.0'})
        with urllib.request.urlopen(req, timeout=30) as r:
            return json.loads(r.read().decode('utf-8'))
    except Exception as e:
        print(f"  WARN: fetch failed for {url}: {e}", file=sys.stderr)
        return None


def fetch_languages():
    data = fetch_json(API_BASE + "Languages")
    return data or []


def fetch_prayers_for_lang(lang_id: int):
    url = API_BASE + f"prayersystembylanguage?html=true&languageid={lang_id}"
    data = fetch_json(url)
    if not data:
        return []
    return data.get("Prayers", [])


# ---------------------------------------------------------------------------
# Load lang.csv
# ---------------------------------------------------------------------------
def load_lang_csv():
    mapping = {}
    with open(LANG_CSV, newline='', encoding='utf-8') as f:
        reader = csv.DictReader(f)
        for row in reader:
            try:
                api_id = int(row['id'])
                mapping[api_id] = {
                    'iso':  row['iso'],
                    'name': row['english'],
                    'rtl':  row['rtl'].strip() == 'rtl',
                }
            except (ValueError, KeyError):
                pass
    return mapping


# ---------------------------------------------------------------------------
# Load existing DB entries
# ---------------------------------------------------------------------------
def load_db_entries():
    """Returns dict: iso → { source_id: {phelps, name, text_fp, version} }"""
    # Use JSON output to handle multi-line text fields safely
    result = subprocess.run(
        ['dolt', 'sql', '-q',
         "SELECT language, source_id, phelps, name, version, text "
         "FROM writings WHERE source='bahaiprayers.net' "
         "ORDER BY language, CAST(source_id AS UNSIGNED)",
         '--result-format', 'json'],
        capture_output=True, text=True,
        cwd='/home/joop/prayermatching/bahaiwritings'
    )
    if result.returncode != 0:
        print(f"ERROR loading DB: {result.stderr[:200]}", file=sys.stderr)
        return {}
    data = json.loads(result.stdout)
    rows = data.get('rows', [])
    by_lang = {}
    for row in rows:
        lang = row.get('language', '')
        sid  = row.get('source_id', '')
        if lang not in by_lang:
            by_lang[lang] = {}
        raw_text = row.get('text') or ''
        # Strip the ## header line if present
        lines = raw_text.split('\n')
        body = '\n'.join(l for l in lines if not l.startswith('## ')).strip()
        by_lang[lang][sid] = {
            'phelps':    row.get('phelps') or '',
            'name':      row.get('name') or '',
            'version':   row.get('version') or '',
            'text_fp':   text_fingerprint(body),
            'full_text': body,
        }
    return by_lang


# ---------------------------------------------------------------------------
# SQL helpers
# ---------------------------------------------------------------------------
def sql_escape(s: str) -> str:
    return s.replace("\\", "\\\\").replace("'", "\\'")


def make_insert(prayer: dict, iso: str, lang_id: int,
                prayer_name: str, phelps: str = None) -> str:
    pid      = prayer['Id']
    raw_html = prayer.get('Text', '')
    text     = html_to_text(raw_html)

    if prayer_name:
        full_text = f"## {prayer_name}\n\n{text}"
    else:
        full_text = text

    link     = f"https://bahaiprayers.net/Book/Single/{lang_id}/{pid}"
    new_uuid = str(uuid.uuid4())
    phelps_val = f"'{sql_escape(phelps)}'" if phelps else "NULL"

    return (
        f"INSERT INTO writings "
        f"(version, source, source_id, language, name, type, text, link, phelps, is_verified) VALUES ("
        f"'{new_uuid}', "
        f"'bahaiprayers.net', "
        f"'{pid}', "
        f"'{sql_escape(iso)}', "
        f"'{sql_escape(prayer_name)}', "
        f"'prayer', "
        f"'{sql_escape(full_text)}', "
        f"'{sql_escape(link)}', "
        f"{phelps_val}, "
        f"1);"
    )


def matching_difficulty(iso: str) -> str:
    if iso in HARD_MATCH_LANGS:
        return "HARD (script/non-Latin — use --translate)"
    if iso in CREOLE_LANGS:
        return "MEDIUM (English creole — Gemini with hint)"
    if iso in INDIGENOUS_LANGS:
        return "HARD (indigenous — check for English glosses first)"
    return "EASY (European — standard Gemini pass)"


# ---------------------------------------------------------------------------
# Main
# ---------------------------------------------------------------------------
def main():
    print("Loading lang.csv...", flush=True)
    lang_map = load_lang_csv()

    print("Loading DB entries...", flush=True)
    db_by_lang = load_db_entries()

    print("Fetching language list from API...", flush=True)
    api_langs = fetch_languages()
    api_lang_by_id = {l['Id']: l for l in api_langs}

    inserts    = ["-- New prayers from bahaiprayers.net API sync\n-- Generated by sync_from_api.py\n"]
    deletes    = ["-- REMOVED prayers (commented out — review before uncommenting)\n"]
    report     = []
    unmatched  = {}   # iso → list of {sid, difficulty}

    total_new = total_removed = total_carried = total_unmatched = 0
    langs_changed = []

    for api_id, lang_info in sorted(lang_map.items()):
        iso  = lang_info['iso']
        name = lang_info['name']

        if api_id not in api_lang_by_id:
            continue
        api_count = api_lang_by_id[api_id].get('PrayerCount', 0)
        if api_count == 0:
            continue

        print(f"  Checking {iso} ({name}), API says {api_count} prayers...", flush=True)
        api_prayers = fetch_prayers_for_lang(api_id)
        if not api_prayers:
            continue

        api_by_sid  = {str(p['Id']): p for p in api_prayers}
        db_entries  = db_by_lang.get(iso, {})

        new_ids     = set(api_by_sid.keys()) - set(db_entries.keys())
        removed_ids = set(db_entries.keys()) - set(api_by_sid.keys())
        overlap     = set(api_by_sid.keys()) & set(db_entries.keys())

        if not new_ids and not removed_ids:
            continue   # fully in sync

        langs_changed.append(iso)

        # Detect full replacement: no overlap, old entries existed
        is_full_replacement = (len(overlap) == 0 and len(db_entries) > 0 and len(new_ids) > 0)

        section = [f"\n{'='*60}",
                   f"{iso} ({name}): {len(new_ids)} new, {len(removed_ids)} removed",
                   f"  DB: {len(db_entries)}  API: {len(api_prayers)}  Overlap: {len(overlap)}"]

        if is_full_replacement:
            section.append(f"  *** FULL REPLACEMENT DETECTED — building content fingerprint map ***")

        # Build fingerprint + word-set map from old DB entries (for content-matching)
        old_fp_to_entry  = {}   # fingerprint → entry info
        old_ws_to_entry  = []   # list of (word_set, entry info) for fuzzy fallback
        if removed_ids:
            for sid in removed_ids:
                fp = db_entries[sid]['text_fp']
                entry = {
                    'old_sid': sid,
                    'phelps':  db_entries[sid]['phelps'],
                    'name':    db_entries[sid]['name'],
                }
                if fp:
                    old_fp_to_entry[fp] = entry
                # Use full text for word-set (better accuracy than fingerprint)
                full = db_entries[sid].get('full_text', fp)
                ws = word_set(full or fp)
                if ws:
                    old_ws_to_entry.append((ws, entry))

        # Process new prayers
        lang_inserts = [f"\n-- {iso} ({name}) — {len(new_ids)} new prayers"]
        lang_deletes = [f"\n-- {iso} ({name}) — {len(removed_ids)} removed prayers"]

        new_unmatched = []

        for sid in sorted(new_ids, key=int):
            p = api_by_sid[sid]
            raw_text  = html_to_text(p.get('Text', ''))
            fp        = text_fingerprint(raw_text)
            api_name  = p.get('FirstTagName', '')

            # Try to carry phelps from old entry with matching content
            carried_phelps = None
            carried_name   = None
            match_method   = None
            if old_fp_to_entry or old_ws_to_entry:
                # Level 1: exact fingerprint match
                if fp in old_fp_to_entry:
                    old = old_fp_to_entry[fp]
                    carried_phelps = old['phelps'] or None
                    carried_name   = old['name'] or api_name
                    match_method   = "exact"
                else:
                    # Level 2: prefix match (first 80 normalised chars)
                    short = fp[:80]
                    for old_fp, old in old_fp_to_entry.items():
                        if old_fp[:80] == short:
                            carried_phelps = old['phelps'] or None
                            carried_name   = old['name'] or api_name
                            match_method   = "prefix"
                            break

                # Level 3: Jaccard similarity ≥ 0.55 on full texts
                if not match_method and old_ws_to_entry:
                    new_ws = word_set(raw_text)
                    best_score, best_old = 0.0, None
                    for (ows, old) in old_ws_to_entry:
                        score = jaccard(new_ws, ows)
                        if score > best_score:
                            best_score, best_old = score, old
                    if best_score >= 0.55:
                        carried_phelps = best_old['phelps'] or None
                        carried_name   = best_old['name'] or api_name
                        match_method   = f"fuzzy({best_score:.2f})"

            prayer_name = carried_name or api_name

            if carried_phelps:
                total_carried += 1
                section.append(f"  + {sid} → phelps carried: {carried_phelps}  [{match_method}]")
            elif match_method:
                # Content matched but old phelps was NULL — still useful to note
                section.append(f"  ~ {sid} → content matched [{match_method}] but old phelps was NULL")
                total_unmatched += 1
                new_unmatched.append({'sid': sid, 'name': api_name})
            else:
                total_unmatched += 1
                new_unmatched.append({'sid': sid, 'name': api_name})

            lang_inserts.append(make_insert(p, iso, api_id, prayer_name, carried_phelps))

        total_new += len(new_ids)

        # Process removed prayers → commented DELETEs
        for sid in sorted(removed_ids, key=int):
            phelps = db_entries[sid]['phelps']
            lang_deletes.append(
                f"-- DELETE FROM writings WHERE source='bahaiprayers.net' "
                f"AND language='{iso}' AND source_id='{sid}'; -- phelps={phelps or 'NULL'}"
            )
        total_removed += len(removed_ids)

        inserts.extend(lang_inserts)
        deletes.extend(lang_deletes)
        report.extend(section)

        if new_unmatched:
            diff = matching_difficulty(iso)
            unmatched[iso] = {'name': name, 'prayers': new_unmatched, 'difficulty': diff}

    # Write outputs
    summary = [
        "bahaiprayers.net SYNC REPORT",
        f"Languages with changes:  {len(langs_changed)}: {', '.join(langs_changed)}",
        f"New prayers total:       {total_new}",
        f"  phelps carried over:   {total_carried}",
        f"  need new matching:     {total_unmatched}",
        f"Removed prayers total:   {total_removed}",
    ]

    with open("/tmp/sync_report.txt", "w") as f:
        f.write("\n".join(summary) + "\n")
        f.write("\n".join(report) + "\n")

    with open("/tmp/sync_inserts.sql", "w") as f:
        f.write("\n".join(inserts) + "\n")

    with open("/tmp/sync_deletes.sql", "w") as f:
        f.write("\n".join(deletes) + "\n")

    with open("/tmp/sync_unmatched.txt", "w") as f:
        f.write("PRAYERS NEEDING NEW PHELPS MATCHING\n")
        f.write("="*60 + "\n\n")
        # Group by difficulty
        for diff_label in ["EASY", "MEDIUM", "HARD"]:
            group = {iso: v for iso, v in unmatched.items() if v['difficulty'].startswith(diff_label)}
            if not group:
                continue
            f.write(f"\n{diff_label} LANGUAGES\n{'-'*40}\n")
            for iso, info in sorted(group.items()):
                f.write(f"\n{iso} ({info['name']}) — {len(info['prayers'])} prayers\n")
                f.write(f"  Strategy: {info['difficulty']}\n")
                f.write(f"  Command:  python3 ~/prayermatching/scripts/gemini_batch_match.py --lang {iso}")
                if iso in HARD_MATCH_LANGS:
                    f.write(" --translate")
                elif iso in CREOLE_LANGS:
                    f.write(" --translate")
                f.write("\n")
                for p in info['prayers']:
                    f.write(f"    {iso}/{p['sid']}  {p['name']}\n")

    print("\n" + "\n".join(summary))
    print(f"\nOutputs:")
    print(f"  /tmp/sync_report.txt    — full language-by-language report")
    print(f"  /tmp/sync_inserts.sql   — INSERT statements (apply with: dolt sql < /tmp/sync_inserts.sql)")
    print(f"  /tmp/sync_deletes.sql   — removed entries (COMMENTED — review before uncommenting)")
    print(f"  /tmp/sync_unmatched.txt — prayers needing new matching, grouped by difficulty")


if __name__ == "__main__":
    main()
