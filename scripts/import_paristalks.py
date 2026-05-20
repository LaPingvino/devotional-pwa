#!/usr/bin/env python3
"""Import Paris Talks into the writings table using REAL ABU phelps codes
from bahai-library.com's position→code map.

Strategy:
1. Fetch bahai-library.com/abdul-baha_paris_talks → extract position N → ABU code.
2. Fetch bahai.org paris-talks/2..8 (the content pages; section 1 is TOC).
3. Split each section into talks by class="brl-global-title" boundary.
4. For talk N, base phelps = bahai-library code for PT#N.
5. Each paragraph → 11-char phelps = <base 7-char> + <3-digit paragraph idx>.
6. Insert with type='paristalks', source='bahai.org/paris-talks'.

Also writes an `i18n` row for writings/paristalks so gen_hugo_data.go picks it up.

Run:
  python3 scripts/import_paristalks.py [--dry-run]
"""
import argparse
import json
import re
import subprocess
import sys
import time
import urllib.error
import urllib.request

DOLT_DIR = "/home/joop/bahaiwritings"
UA = "Mozilla/5.0 (compatible; BahaiTextAligner/1.0)"
MIN_PARA_LEN = 30
BAHAI_LIB_URL = "https://bahai-library.com/abdul-baha_paris_talks"
BAHAI_ORG_TMPL = "https://www.bahai.org/library/authoritative-texts/abdul-baha/paris-talks/{n}"
CONTENT_SECTIONS = range(2, 9)  # 2..8 inclusive


def fetch(url, delay=1.5):
    req = urllib.request.Request(url, headers={"User-Agent": UA})
    try:
        with urllib.request.urlopen(req, timeout=30) as resp:
            content = resp.read().decode("utf-8", errors="replace")
        time.sleep(delay)
        return content
    except Exception as e:
        sys.stderr.write(f"fetch error {url}: {e}\n")
        return None


def dolt(query):
    res = subprocess.run(
        ["dolt", "sql", "--result-format", "csv"],
        cwd=DOLT_DIR, capture_output=True, text=True, input=query,
    )
    if res.returncode != 0:
        sys.stderr.write(f"DOLT ERROR: {res.stderr}\nQUERY: {query[:200]}\n")
        sys.exit(1)
    return res.stdout


def sql_escape(s):
    return s.replace("\\", "\\\\").replace("'", "''")


def fetch_code_map():
    """Return list of (talk_num, phelps_code, has_x). Position N = index N+1."""
    h = fetch(BAHAI_LIB_URL)
    if not h:
        sys.exit("could not fetch bahai-library page")
    # Pattern: ABU0653[PT#01 p.001] or with x marker as ABU0653x[PT#...]
    matches = re.findall(
        r'(ABU?\d{4,5}|AB\d{5})(x?)\[PT#?(\d+)\s+p\.\d+\]', h
    )
    # Dedup by talk number (each appears twice in the page)
    by_talk = {}
    for code, x, tnum in matches:
        n = int(tnum)
        if n not in by_talk:
            by_talk[n] = (code, x == 'x')
    out = [(n, by_talk[n][0], by_talk[n][1]) for n in sorted(by_talk)]
    return out


def parse_section(html):
    """Return list of (title, [paragraphs]) for each talk in this HTML page.

    A talk starts at <p class="brl-head brl-global-title">…</p>. Body
    paragraphs follow until the next title or end of content. Selection
    numbers (brl-global-selection-number) are skipped.
    """
    # Find all <p>…</p> blocks with class attribute
    # Capture class and inner text together so we can keep order
    # Accept <p>, <p class="...">, <p id="...">, etc.
    blocks = re.findall(
        r'<p(\s[^>]*)?>(.*?)</p>', html, re.DOTALL | re.IGNORECASE
    )
    talks = []
    cur_title = None
    cur_paras = []
    for attrs, inner in blocks:
        cls_m = re.search(r'class="([^"]+)"', attrs)
        cls = cls_m.group(1) if cls_m else ""
        text = re.sub(r"<[^>]+>", "", inner)
        text = re.sub(r"(\w)\d+(\s)", r"\1\2", text)
        text = re.sub(r"(\w)\d+$", r"\1", text)
        text = (text.replace("&nbsp;", " ").replace("&#160;", " ")
                    .replace("&mdash;", "—").replace("&rsquo;", "’")
                    .replace("&lsquo;", "‘").replace("&ldquo;", "“")
                    .replace("&rdquo;", "”").replace("&amp;", "&"))
        text = re.sub(r"&[a-z]+;", " ", text)
        text = re.sub(r"\s+", " ", text).strip()
        if not text:
            continue

        if "brl-global-title" in cls:
            # New talk
            if cur_title is not None:
                talks.append((cur_title, cur_paras))
            cur_title = text
            cur_paras = []
        elif "brl-global-selection-number" in cls:
            # Skip the "1, 2, 3…" markers
            continue
        elif "brl-head" in cls:
            # Skip other heading-style paragraphs (book chapters etc.)
            continue
        else:
            if cur_title is None:
                # Pre-title content (preface). Skip; not part of a coded talk.
                continue
            if len(text) >= MIN_PARA_LEN:
                cur_paras.append(text)

    if cur_title is not None:
        talks.append((cur_title, cur_paras))
    return talks


def main():
    ap = argparse.ArgumentParser()
    ap.add_argument("--dry-run", action="store_true")
    args = ap.parse_args()

    print("Fetching bahai-library code map…")
    code_map = fetch_code_map()
    print(f"  got {len(code_map)} talks mapped")
    # dict: talk number → (phelps, has_x)
    code_by_n = {n: (code, x) for n, code, x in code_map}

    print("\nScraping bahai.org content sections…")
    all_talks = []  # list of (title, [paras])
    for n in CONTENT_SECTIONS:
        url = BAHAI_ORG_TMPL.format(n=n)
        h = fetch(url)
        if not h:
            print(f"  section {n}: FAILED")
            continue
        talks = parse_section(h)
        print(f"  section {n}: {len(talks)} talks, {sum(len(p) for _, p in talks)} paragraphs")
        all_talks.extend(talks)

    print(f"\nTotal talks scraped: {len(all_talks)}")
    print(f"Total bahai-library codes: {len(code_map)}")

    if len(all_talks) != len(code_map):
        print(f"  WARNING: count mismatch ({len(all_talks)} vs {len(code_map)})")
        print(f"  Will pair by position up to min({len(all_talks)},{len(code_map)})")

    # Build SQL inserts
    values = []
    paired = min(len(all_talks), len(code_map))
    for i in range(paired):
        talk_num = i + 1
        title, paras = all_talks[i]
        phelps_base, has_x = code_by_n.get(talk_num, (None, False))
        if not phelps_base:
            print(f"  skip talk {talk_num}: no code")
            continue
        # If excerpt-only, append 3-letter mnemonic from title's distinctive word.
        # For Paris Talks, no x markers were seen — but handle defensively.
        suffix = ""
        if has_x:
            # First 3 letters of last word in title, uppercase
            words = re.findall(r"[A-Za-z]+", title)
            if words:
                suffix = words[-1][:3].upper()
        base_full = phelps_base + suffix
        for para_idx, text in enumerate(paras, start=1):
            ext = f"{base_full}{para_idx:03d}"
            text_html = f"<p>{sql_escape(text)}</p>"
            name = f"{talk_num}. {title}"
            version = f"paristalks:{ext}:en"
            values.append(
                f"('{version}','{ext}','en','{sql_escape(name)}',"
                f"'paristalks','{text_html}','bahai.org/paris-talks','{phelps_base}',1)"
            )

    print(f"\nReady to insert {len(values)} rows")
    if args.dry_run:
        print("DRY RUN; first 3 values:")
        for v in values[:3]:
            print("  " + v[:200])
        return

    if not values:
        return
    ins_prefix = (
        "INSERT INTO writings (version, phelps, language, name, type, text, source, source_id, is_verified) VALUES\n"
    )
    BATCH = 500
    for i in range(0, len(values), BATCH):
        chunk = values[i:i + BATCH]
        stmt = ins_prefix + ",\n".join(chunk) + ";"
        dolt("SET FOREIGN_KEY_CHECKS=0;" + stmt + "SET FOREIGN_KEY_CHECKS=1;")
        print(f"  inserted rows {i}–{i + len(chunk)}")

    # i18n metadata
    i18n_val = {
        "author": "'Abdu'l-Bahá",
        "author_prefix": "AB",
        "db_type": "paristalks",
        "flags": {"single_book": True, "show_names": True, "split_paras": False},
        "order": 17,
        "title": "Paris Talks",
    }
    val_json = json.dumps(i18n_val, ensure_ascii=False)
    dolt(
        f"INSERT INTO i18n (`key`, language, value) VALUES "
        f"('writings/paristalks', 'en', '{sql_escape(val_json)}') "
        f"ON DUPLICATE KEY UPDATE value=VALUES(value);"
    )
    print("Wrote i18n: writings/paristalks")


if __name__ == "__main__":
    main()
