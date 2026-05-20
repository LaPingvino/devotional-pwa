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
        "url": "https://www.bahai.org/library/authoritative-texts/abdul-baha/light-of-the-world/{n}",
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


def _normalize_text(inner):
    text = re.sub(r"<[^>]+>", "", inner)
    text = re.sub(r"(\w)\d+(\s)", r"\1\2", text)
    text = re.sub(r"(\w)\d+$", r"\1", text)
    text = (text.replace("&nbsp;", " ").replace("&#160;", " ")
                .replace("&mdash;", "—").replace("&rsquo;", "’")
                .replace("&lsquo;", "‘").replace("&ldquo;", "“")
                .replace("&rdquo;", "”").replace("&amp;", "&"))
    text = re.sub(r"&[a-z]+;", " ", text)
    return re.sub(r"\s+", " ", text).strip()


def parse_section(html):
    """Parse a bahai.org reader section.

    Two markup conventions are handled:
      A. Multi-tablet pages — each tablet starts at <p class="brl-global-title">
         (Paris Talks, Memorials of the Faithful).
      B. Single-tablet pages — the whole section is one tablet titled by
         <h2 class="…brl-title…"> (Summons of the Lord of Hosts, Tabernacle
         of Unity, etc.). The section's body paragraphs all belong to that
         one tablet.

    Returns a list of (title, [paragraphs]).
    """
    # Trim to the reader-canvas region so we don't pick up sidebar chrome
    # like "Please enter your search terms." (paragraphs with no class).
    main_html = html
    canvas_m = re.search(r'<div[^>]*class="[^"]*reader-canvas[^"]*"', html, re.IGNORECASE)
    if canvas_m:
        main_html = html[canvas_m.start():]
    blocks = re.findall(r'<p(\s[^>]*)?>(.*?)</p>', main_html, re.DOTALL | re.IGNORECASE)

    def class_of(attrs):
        m = re.search(r'class="([^"]+)"', attrs or "")
        return m.group(1) if m else ""

    has_global_titles = any("brl-global-title" in class_of(a) for a, _ in blocks)
    has_selection_numbers = any("brl-global-selection-number" in class_of(a) for a, _ in blocks)

    if has_global_titles:
        # Convention A
        talks = []
        cur_title = None
        cur_paras = []
        for attrs, inner in blocks:
            cls_m = re.search(r'class="([^"]+)"', attrs or "")
            cls = cls_m.group(1) if cls_m else ""
            text = _normalize_text(inner)
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

    # Convention C (selection-number-only boundaries, e.g. Light of the World):
    # parking — per-page numbers reset to 1 each section, so aligning to the
    # bahai-library cumulative 1..N map needs caller-side bookkeeping that
    # isn't worth adding for one work right now.

    # Convention B — single tablet per page (or a continuation of one).
    # Pull title from h2.brl-title if present; else from the page's H1
    # (used for works like SDC where the first section has no brl-title
    # but is still the start of the main text); else mark as a continuation
    # so the caller can merge into the previous entry.
    title_m = re.search(
        r'<h2[^>]*class="[^"]*brl-title[^"]*"[^>]*>(.*?)</h2>',
        html, re.DOTALL | re.IGNORECASE,
    )
    title = _normalize_text(title_m.group(1)) if title_m else ""
    # Allow H1 as a one-time work-level title — used only when the first
    # section of a multi-section work needs a starting title. Subsequent
    # sections without a brl-title still come back as "__cont__".

    paras = []
    for attrs, inner in blocks:
        cls_m = re.search(r'class="([^"]+)"', attrs or "")
        cls = cls_m.group(1) if cls_m else ""
        # Skip chrome paragraphs
        if any(skip in cls for skip in (
            "btn-downloads-info", "footer-copyright", "result-snippet",
            "result-source", "small",
        )):
            continue
        text = _normalize_text(inner)
        if len(text) >= MIN_PARA_LEN:
            paras.append(text)
    if not paras:
        return []
    return [(title or "__cont__", paras)]


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
        # If the page yields no entries but has a meaningful H1, treat
        # the entire page as one tablet with the H1 as the title (covers
        # works whose first content section omits the brl-title).
        if not s or (len(s) == 1 and s[0][0] == "__cont__" and not all_talks):
            h1_m = re.search(r"<h1[^>]*>(.*?)</h1>", h, re.DOTALL | re.IGNORECASE)
            if h1_m:
                h1 = _normalize_text(h1_m.group(1))
                if h1 and s and s[0][1]:
                    s = [(h1, s[0][1])]
        # Merge __cont__ sentinel entries into the previous real entry
        # so works that span multiple URL sections accumulate correctly.
        for title, paras in s:
            if title == "__cont__" and all_talks:
                prev_title, prev_paras = all_talks[-1]
                all_talks[-1] = (prev_title, prev_paras + paras)
            elif title and title != "__cont__":
                all_talks.append((title, paras))
        n_real = sum(1 for t, _ in s if t and t != "__cont__")
        n_cont = sum(1 for t, _ in s if t == "__cont__")
        n_paras = sum(len(p) for _, p in s)
        print(f"  section {n}: {n_real} new + {n_cont} cont, {n_paras} paragraphs")

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
    used_bases = set()  # phelps base codes already claimed by an earlier entry
    real_num = 0
    for idx, (title, paras) in enumerate(all_talks, start=1):
        if is_subheader(title):
            skipped.append((idx, title))
            continue
        real_num += 1
        phelps_base = find_by_title(title, title_idx)
        match_via = "title"
        if not phelps_base and paras:
            cand = find_by_first_para(paras[0], fl_idx)
            if cand and cand not in used_bases:
                phelps_base = cand
                match_via = "first_line"
        if not phelps_base:
            cand = code_map.get(real_num)
            if cand and cand not in used_bases:
                phelps_base = cand
                match_via = "position"
        if phelps_base and phelps_base in used_bases:
            # Conflict — another entry already claimed this code. Mint instead
            # of corrupting the data with a duplicate.
            phelps_base = None
        if not phelps_base:
            phelps_base = f"{cfg['x_prefix']}{real_num:04d}"
            match_via = "minted"

        used_bases.add(phelps_base)
        if match_via == "title":
            title_matched.append((real_num, phelps_base, title))
        elif match_via == "first_line":
            title_matched.append((real_num, phelps_base, title + " (via first-line)"))
        elif match_via == "position":
            position_used.append((real_num, phelps_base, title))
        else:
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
