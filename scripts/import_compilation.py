#!/usr/bin/env python3
"""Generic compilation-import pipeline (extends the Paris Talks recipe).

For a given <collection_key>:
1. Read writing_collections rows for the key → {position: phelps}.
2. Scrape the listed bahai.org URL pages (sections), split into "talks"
   by <p class="brl-global-title"> boundaries.
3. For each scraped entry, resolve a phelps code by:
   a. Inventory title-prefix match
   b. First-paragraph incipit match against inventory.First line (translated)
   c. Position match against the collection (using a skip-counter so
      subheaders that share the .brl-global-title class don't drift the
      alignment)
   d. Mint XKEY<num> only when (a)-(c) all fail (exceptional)
4. Insert into writings with type=<key>, deterministic version, and
   source_id=LPAD(position,4) so multi-book renderings keep canonical order.
5. Optionally also write the i18n writings/<key> metadata stub if missing.

Run:
  python3 scripts/import_compilation.py <key>  [--dry-run]
Examples:
  python3 scripts/import_compilation.py memorials
  python3 scripts/import_compilation.py summons
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

# Per-work config. URL template uses {n} for section number. content_sections
# is the range to try (script auto-stops on 404). x_prefix is the minted-code
# prefix used for true gaps.
WORKS = {
    "paristalks": {
        "url": "https://www.bahai.org/library/authoritative-texts/abdul-baha/paris-talks/{n}",
        "sections": range(2, 9),
        "x_prefix": "XPT",
    },
    "summons": {
        "url": "https://www.bahai.org/library/authoritative-texts/bahaullah/summons-lord-hosts/{n}",
        "sections": range(2, 20),
        "x_prefix": "XSL",
    },
    "memorials": {
        "url": "https://www.bahai.org/library/authoritative-texts/abdul-baha/memorials-faithful/{n}",
        "sections": range(2, 20),
        "x_prefix": "XMF",
    },
    "tabernacle": {
        "url": "https://www.bahai.org/library/authoritative-texts/bahaullah/tabernacle-unity/{n}",
        "sections": range(2, 20),
        "x_prefix": "XTU",
    },
    "call": {
        "url": "https://www.bahai.org/library/authoritative-texts/bahaullah/call-divine-beloved/{n}",
        "sections": range(2, 20),
        "x_prefix": "XCD",
    },
    "sdc": {
        "url": "https://www.bahai.org/library/authoritative-texts/abdul-baha/secret-divine-civilization/{n}",
        "sections": range(2, 20),
        "x_prefix": "XSD",
    },
    "tn": {
        "url": "https://www.bahai.org/library/authoritative-texts/abdul-baha/travelers-narrative/{n}",
        "sections": range(2, 20),
        "x_prefix": "XTN",
    },
    "wt": {
        "url": "https://www.bahai.org/library/authoritative-texts/abdul-baha/will-testament-abdul-baha/{n}",
        "sections": range(2, 20),
        "x_prefix": "XWT",
    },
    "light": {
        "url": "https://www.bahai.org/library/authoritative-texts/abdul-baha/light-world/{n}",
        "sections": range(2, 20),
        "x_prefix": "XLW",
    },
}


def fetch(url, delay=1.5):
    req = urllib.request.Request(url, headers={"User-Agent": UA})
    try:
        with urllib.request.urlopen(req, timeout=30) as resp:
            content = resp.read().decode("utf-8", errors="replace")
        time.sleep(delay)
        return content
    except urllib.error.HTTPError as e:
        if e.code == 404:
            return "__404__"
        sys.stderr.write(f"HTTP {e.code} {url}\n")
        return None
    except Exception as e:
        sys.stderr.write(f"fetch error {url}: {e}\n")
        return None


def dolt(query):
    res = subprocess.run(
        ["dolt", "sql", "--result-format", "csv"],
        cwd=DOLT_DIR, capture_output=True, text=True, input=query,
    )
    if res.returncode != 0:
        sys.stderr.write(f"DOLT ERROR: {res.stderr[:2000]}\nQUERY: {query[:500]}\n")
        sys.exit(1)
    return res.stdout


def sql_escape(s):
    return s.replace("\\", "\\\\").replace("'", "''")


def load_collection(key):
    out = dolt(
        f"SELECT position, phelps FROM writing_collections "
        f"WHERE collection_key='{key}' ORDER BY position"
    )
    m = {}
    for row in out.strip().splitlines()[1:]:
        parts = row.split(",")
        if len(parts) >= 2:
            m[int(parts[0])] = parts[1].strip()
    return m


def parse_section(html):
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


def load_inventory_indices():
    """Return (title_prefix → PIN, [(first_line_norm, PIN), …])."""
    out = dolt(
        "SELECT PIN, COALESCE(Title,''), COALESCE(`First line (translated)`,'') "
        "FROM inventory WHERE PIN IS NOT NULL"
    )
    import csv as _csv, io as _io
    title_idx = {}
    firstline_idx = []
    for row in _csv.reader(_io.StringIO(out)):
        if len(row) < 3:
            continue
        pin = row[0]
        if row[1]:
            t = row[1].lower()
            if ':' in t:
                t = t.split(':', 1)[0]
            t = re.sub(r"[‘’“”\"',.()—\-]", " ", t)
            t = re.sub(r"\s+", " ", t).strip()
            if t and t not in title_idx:
                title_idx[t] = pin
            # Also index a diacritic-stripped variant
            t2 = strip_diacritics(t)
            if t2 and t2 != t and t2 not in title_idx:
                title_idx[t2] = pin
        if row[2]:
            fl = row[2].lower()
            fl = re.sub(r"[‘’“”\"',.!?()—\-]", " ", fl)
            fl = re.sub(r"\s+", " ", fl).strip()
            if len(fl) >= 30:
                firstline_idx.append((fl, pin))
    return title_idx, firstline_idx


def strip_diacritics(s):
    import unicodedata
    nfd = unicodedata.normalize('NFD', s)
    return ''.join(c for c in nfd if unicodedata.category(c) != 'Mn')


def find_by_title(title, idx):
    t = title.lower()
    if ':' in t:
        t = t.split(':', 1)[0]
    t = re.sub(r"[‘’“”\"',.()—\-]", " ", t)
    t = re.sub(r"\s+", " ", t).strip()
    hit = idx.get(t)
    if hit:
        return hit
    # Try with diacritics stripped both sides
    t2 = strip_diacritics(t)
    return idx.get(t2)


def find_by_first_para(text, idx):
    """Match a talk's first paragraph to an inventory first-line.

    To avoid spurious matches on common phrases, require an EXACT match
    of the inventory first-line's first 25 chars (lowercased, depunctuated)
    at the start of the scraped paragraph, OR the scraped paragraph's first
    25 chars at the start of the inventory first-line (handles salutation
    differences like "O Thou my…" preceding the actual incipit).
    """
    if not text or len(text) < 30:
        return None
    norm = text.lower()
    norm = re.sub(r"[‘’“”\"',.!?()—\-]", " ", norm)
    norm = re.sub(r"\s+", " ", norm).strip()
    head = norm[:60]
    for fl, pin in idx:
        if not fl or len(fl) < 30:
            continue
        fl_head = fl[:60]
        # 1. inventory head appears at start of scraped paragraph
        if norm.startswith(fl_head[:25]):
            return pin
        # 2. scraped head appears at start of inventory line
        if fl.startswith(head[:25]):
            return pin
        # 3. invocation/salutation: scraped paragraph starts at position
        #    >0 inside the inventory line (e.g. "O Thou my beloved daughter! Thine eloquent…"
        #    vs scraped "Thine eloquent…"). Require ≥25-char window match early in fl.
        idx_in_fl = fl.find(head[:25]) if len(head) >= 25 else -1
        if 0 < idx_in_fl <= 40:
            return pin
    return None


def is_subheader(title):
    """Subheaders that share the .brl-global-title class but aren't real entries."""
    if re.match(r'^\d+\s+(Avenue|Rue|Boulevard|Place|Street|Cadogan)', title):
        return True
    if re.search(r'\bRue\s+[A-Z]', title) and len(title.split()) < 6 and ',' in title:
        return True
    return False


def main():
    ap = argparse.ArgumentParser()
    ap.add_argument("key", help="Collection / writing-type key")
    ap.add_argument("--dry-run", action="store_true")
    args = ap.parse_args()

    if args.key not in WORKS:
        sys.exit(f"unknown key '{args.key}'. Choices: {', '.join(WORKS)}")
    cfg = WORKS[args.key]

    print(f"=== {args.key} ===")
    code_map = load_collection(args.key)
    print(f"  {len(code_map)} positions in collection")

    print(f"\nScraping bahai.org sections…")
    all_talks = []
    for n in cfg["sections"]:
        url = cfg["url"].format(n=n)
        h = fetch(url)
        if h == "__404__":
            print(f"  section {n}: 404 — stopping")
            break
        if not h:
            continue
        s = parse_section(h)
        print(f"  section {n}: {len(s)} entries, {sum(len(p) for _, p in s)} paragraphs")
        all_talks.extend(s)

    if not all_talks:
        sys.exit("no entries scraped")
    print(f"\nTotal entries scraped: {len(all_talks)}")

    print("Loading inventory indices…")
    title_idx, fl_idx = load_inventory_indices()

    values = []
    minted = []
    title_matched = []
    position_used = []
    skipped = []
    real_num = 0
    for idx, (title, paras) in enumerate(all_talks, start=1):
        if is_subheader(title):
            skipped.append((idx, title))
            continue
        real_num += 1
        phelps_base = find_by_title(title, title_idx)
        if not phelps_base and paras:
            phelps_base = find_by_first_para(paras[0], fl_idx)
            if phelps_base:
                title_matched.append((real_num, phelps_base, title + " (via first-line)"))
        elif phelps_base:
            title_matched.append((real_num, phelps_base, title))
        if not phelps_base:
            phelps_base = code_map.get(real_num)
            if phelps_base:
                position_used.append((real_num, phelps_base, title))
            else:
                phelps_base = f"{cfg['x_prefix']}{real_num:04d}"
                minted.append((real_num, phelps_base, title))
        for para_idx, text in enumerate(paras, start=1):
            ext = f"{phelps_base}{para_idx:03d}"
            text_html = f"<p>{sql_escape(text)}</p>"
            name = f"{real_num}. {title}"
            version = f"{args.key}:{ext}:en"
            source_id = f"{real_num:04d}"
            values.append(
                f"('{version}','{ext}','en','{sql_escape(name)}',"
                f"'{args.key}','{text_html}','bahai.org/{args.key}','{source_id}',1)"
            )

    print(f"\nReady to insert {len(values)} rows")
    print(f"  {len(title_matched)} matched by inventory title/first-line")
    print(f"  {len(position_used)} matched by collection position")
    print(f"  {len(minted)} minted X-prefix codes (true gaps)")
    print(f"  {len(skipped)} subheaders skipped")
    if minted:
        for pos, code, t in minted:
            print(f"    [mint] #{pos}: {code} — {t[:70]}")
    if skipped:
        for pos, t in skipped:
            print(f"    [skip] #{pos}: {t[:70]}")

    if args.dry_run:
        return
    if not values:
        return

    print(f"\nClearing existing type='{args.key}' rows…")
    dolt(f"SET FOREIGN_KEY_CHECKS=0; DELETE FROM writings WHERE type='{args.key}'; SET FOREIGN_KEY_CHECKS=1;")

    ins = "INSERT INTO writings (version, phelps, language, name, type, text, source, source_id, is_verified) VALUES\n"
    BATCH = 50
    total = len(values)
    for i in range(0, total, BATCH):
        chunk = values[i:i + BATCH]
        stmt = ins + ",\n".join(chunk) + ";"
        try:
            dolt("SET FOREIGN_KEY_CHECKS=0;" + stmt + "SET FOREIGN_KEY_CHECKS=1;")
        except SystemExit:
            # Bisect to identify the offending row instead of giving up
            print(f"  batch {i}-{i+len(chunk)} failed; retrying row-by-row to isolate…")
            for j, row in enumerate(chunk):
                stmt1 = ins + row + ";"
                try:
                    dolt("SET FOREIGN_KEY_CHECKS=0;" + stmt1 + "SET FOREIGN_KEY_CHECKS=1;")
                except SystemExit:
                    print(f"    bad row at index {i+j}: {row[:200]}")
                    raise
        print(f"  inserted {i}–{min(i+BATCH, total)} ({total} total)")
    print("Done.")


if __name__ == "__main__":
    main()
