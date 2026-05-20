#!/usr/bin/env python3
"""Import Paris Talks content into the writings table using the canonical
ABU phelps codes already populated in writing_collections.

Pipeline:
1. Read writing_collections WHERE collection_key='paristalks' → list of
   (position, phelps) pairs. Positions 41 and 43 are absent in bahai-
   library's index; we keep that gap as-is.
2. Scrape bahai.org/paris-talks/2..8 for content. Each <p class="brl-
   global-title"> marks the start of a new talk. Body paragraphs follow
   until the next title.
3. Bahai.org has 60 talks: 1..59 numbered + 1 appendix ("Tablet Revealed
   by 'Abdu'l-Bahá", which has its own AB code outside the PT# series).
   We import only the numbered talks 1..59 that have a matching PT# in
   the collection (so positions 41 and 43 stay un-imported).
4. Each scraped paragraph becomes one writings row with extended phelps:
   <ABU base 7 chars> + <3-digit paragraph index>. type='paristalks',
   source='bahai.org/paris-talks'.

Run:
  python3 scripts/import_paristalks.py [--dry-run]
"""
import argparse
import re
import subprocess
import sys
import time
import urllib.error
import urllib.request

DOLT_DIR = "/home/joop/bahaiwritings"
UA = "Mozilla/5.0 (compatible; BahaiTextAligner/1.0)"
MIN_PARA_LEN = 30
BAHAI_ORG_TMPL = "https://www.bahai.org/library/authoritative-texts/abdul-baha/paris-talks/{n}"
CONTENT_SECTIONS = range(2, 9)


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


def load_collection():
    """Return dict {position: phelps} for paristalks."""
    out = dolt("SELECT position, phelps FROM writing_collections WHERE collection_key='paristalks' ORDER BY position")
    rows = out.strip().splitlines()[1:]
    m = {}
    for row in rows:
        parts = row.split(",")
        if len(parts) >= 2:
            m[int(parts[0])] = parts[1].strip()
    return m


def parse_section(html):
    """Return list of (title, [paragraphs]) for each talk in this HTML page.

    A talk starts at <p class="brl-head brl-global-title">…</p>. Body
    paragraphs follow until the next title. Numeric markers and other
    brl-head paragraphs are skipped.
    """
    blocks = re.findall(r'<p(\s[^>]*)?>(.*?)</p>', html, re.DOTALL | re.IGNORECASE)
    talks = []
    cur_title = None
    cur_paras = []
    for attrs, inner in blocks:
        cls_m = re.search(r'class="([^"]+)"', attrs or "")
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
            if cur_title is not None:
                talks.append((cur_title, cur_paras))
            cur_title = text
            cur_paras = []
        elif "brl-head" in cls:
            continue
        else:
            if cur_title is None:
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

    print("Loading paristalks collection from Dolt…")
    code_map = load_collection()  # {position: phelps}
    print(f"  {len(code_map)} positions in collection")
    if not code_map:
        sys.exit("collection is empty; run build_collections.py first")

    print("\nScraping bahai.org content sections…")
    all_talks = []
    for n in CONTENT_SECTIONS:
        url = BAHAI_ORG_TMPL.format(n=n)
        h = fetch(url)
        if not h:
            print(f"  section {n}: FAILED")
            continue
        s = parse_section(h)
        print(f"  section {n}: {len(s)} talks, {sum(len(p) for _, p in s)} paragraphs")
        all_talks.extend(s)
    print(f"\nTotal talks scraped: {len(all_talks)}")

    # For each scraped talk, find its phelps code by:
    #   1. Position in writing_collections (canonical bahai-library map)
    #   2. Title prefix match against inventory.Title (recovers PT#41, #43,
    #      and the appendix tablet which bahai-library doesn't number)
    #   3. Mint XPT placeholder only when both fail (exceptional)
    print("\nLooking up inventory titles for fallback matches…")
    inv_out = dolt(
        "SELECT PIN, COALESCE(Title,''), COALESCE(`First line (translated)`,'') "
        "FROM inventory "
        "WHERE (PIN LIKE 'AB%' OR PIN LIKE 'ABU%') "
        "ORDER BY PIN"
    )
    import csv as _csv, io as _io
    inv_by_title_prefix = {}   # normalized title prefix → PIN
    inv_by_firstline = []      # (normalized first 60-char first-line, PIN)
    for row in _csv.reader(_io.StringIO(inv_out)):
        if len(row) < 3:
            continue
        pin = row[0]
        if row[1]:
            t = row[1].lower()
            if ':' in t:
                t = t.split(':', 1)[0]
            t = re.sub(r"[‘’“”\"',.()—\-]", " ", t)
            t = re.sub(r"\s+", " ", t).strip()
            if t and t not in inv_by_title_prefix:
                inv_by_title_prefix[t] = pin
        if row[2]:
            fl = row[2].lower()
            fl = re.sub(r"[‘’“”\"',.!?()—\-]", " ", fl)
            fl = re.sub(r"\s+", " ", fl).strip()
            if len(fl) >= 30:
                inv_by_firstline.append((fl[:80], pin))

    def find_by_title(talk_title):
        t = talk_title.lower()
        if ':' in t:
            t = t.split(':', 1)[0]
        t = re.sub(r"[‘’“”\"',.()—\-]", " ", t)
        t = re.sub(r"\s+", " ", t).strip()
        return inv_by_title_prefix.get(t)

    def find_by_first_para(text):
        """Match a talk's first paragraph against inventory first-lines.
        Slide a 6-word window across each inventory first-line and check
        substring containment in the scraped paragraph (and vice versa).
        Handles salutation differences ("O Thou my beloved daughter! Thine
        eloquent..." vs "Thine eloquent...").
        """
        if not text or len(text) < 30:
            return None
        norm = text.lower()
        norm = re.sub(r"[‘’“”\"',.!?()—\-]", " ", norm)
        norm = re.sub(r"\s+", " ", norm).strip()
        for fl, pin in inv_by_firstline:
            words = fl.split()
            for i in range(0, max(1, len(words) - 5)):
                window = " ".join(words[i:i + 6])
                if len(window) >= 25 and window in norm:
                    return pin
        return None

    # Title match dominates: bahai.org has subheaders (location markers like
    # "4 Avenue de Camoëns, Paris,") that share the .brl-global-title class
    # with real talks, throwing off pure position alignment. Title matching
    # against inventory gives canonical codes; position is only used as a
    # last-resort fallback for talks whose title isn't in inventory.
    # Collection-position-1 codes (which we trust because they came from
    # bahai-library annotations) are also used when title-match returns the
    # same code or no result.
    values = []
    minted = []
    title_matched = []
    position_used = []
    skipped_non_talks = []
    real_pt_num = 0  # canonical PT# (skips subheaders so position lines up with bahai-library)
    for idx, (title, paras) in enumerate(all_talks, start=1):
        # Detect non-talk subheaders: short titles that look like addresses or
        # locations (start with digits, contain street keywords). Skip them.
        if re.match(r'^\d+\s+(Avenue|Rue|Boulevard|Place|Street)', title) or \
           (re.search(r'\bRue\s+[A-Z]', title) and len(title.split()) < 6 and ',' in title):
            skipped_non_talks.append((idx, title))
            continue
        real_pt_num += 1
        phelps_base = find_by_title(title)
        source_method = "title"
        if not phelps_base and paras:
            phelps_base = find_by_first_para(paras[0])
            if phelps_base:
                source_method = "first_line"
                title_matched.append((real_pt_num, phelps_base, title + " (via first-line)"))
        if not phelps_base:
            phelps_base = code_map.get(real_pt_num)
            if phelps_base:
                source_method = "position"
                position_used.append((real_pt_num, phelps_base, title))
            else:
                phelps_base = f"XPT{real_pt_num:04d}"
                source_method = "minted"
                minted.append((real_pt_num, phelps_base, title))
        elif source_method == "title":
            title_matched.append((real_pt_num, phelps_base, title))
        for para_idx, text in enumerate(paras, start=1):
            ext = f"{phelps_base}{para_idx:03d}"
            text_html = f"<p>{sql_escape(text)}</p>"
            name = f"Talk {real_pt_num}. {title}"
            version = f"paristalks:{ext}:en"
            values.append(
                f"('{version}','{ext}','en','{sql_escape(name)}',"
                f"'paristalks','{text_html}','bahai.org/paris-talks','{phelps_base}',1)"
            )

    print(f"\nReady to insert {len(values)} rows")
    print(f"  {len(title_matched)} talks matched by inventory title")
    print(f"  {len(position_used)} talks matched by collection position")
    print(f"  {len(minted)} talks minted X-prefix codes (true gaps)")
    print(f"  {len(skipped_non_talks)} bahai.org subheaders skipped (not real talks)")
    if minted:
        print("Minted:")
        for pos, code, t in minted:
            print(f"  PT#{pos}: {code} — {t[:60]}")
    if skipped_non_talks:
        print("Skipped subheaders:")
        for pos, t in skipped_non_talks:
            print(f"  idx{pos}: {t[:70]}")

    if args.dry_run:
        for v in values[:3]:
            print("  " + v[:200])
        return

    if not values:
        return
    # Clear any prior import for this type before re-inserting
    print("\nClearing existing type='paristalks' rows…")
    dolt("SET FOREIGN_KEY_CHECKS=0; DELETE FROM writings WHERE type='paristalks'; SET FOREIGN_KEY_CHECKS=1;")

    ins_prefix = (
        "INSERT INTO writings (version, phelps, language, name, type, text, source, source_id, is_verified) VALUES\n"
    )
    BATCH = 200
    total = len(values)
    for i in range(0, total, BATCH):
        chunk = values[i:i + BATCH]
        stmt = ins_prefix + ",\n".join(chunk) + ";"
        dolt("SET FOREIGN_KEY_CHECKS=0;" + stmt + "SET FOREIGN_KEY_CHECKS=1;")
        print(f"  inserted rows {i}–{min(i + BATCH, total)} ({total} total)")

    print("Done.")


if __name__ == "__main__":
    main()
