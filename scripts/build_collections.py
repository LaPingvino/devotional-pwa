#!/usr/bin/env python3
"""Build the writing_collections table from bahai-library.com Phelps maps.

For each curated bahai-library.com page, scrape its "Inventory #" footer
which lists position → Phelps-code references in the form
   ABU0653[PT#01 p.001]
or with an excerpt marker
   BH00064x[APBH#01 p.001]
The "x" indicates the code is for an EXCERPT of a longer tablet — a
3-letter mnemonic must be appended when known. For now we store the
base code + is_excerpt flag; mnemonics can be filled in later.

Schema:
   writing_collections (
       collection_key VARCHAR(50) NOT NULL,   -- our key, e.g. 'paristalks'
       position INT NOT NULL,                 -- 1-based ordering
       phelps VARCHAR(16),                    -- canonical Phelps base code
       page_ref VARCHAR(20),                  -- 'p.001' etc. from bahai-library
       is_excerpt TINYINT(1) DEFAULT 0,       -- the 'x' marker
       mnemonic VARCHAR(10),                  -- mnemonic suffix when excerpt
       title VARCHAR(255),                    -- optional override title for entry
       source_url VARCHAR(255),
       PRIMARY KEY (collection_key, position)
   )

Usage:
  python3 scripts/build_collections.py [--only paristalks]
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

# Each entry: (collection_key, abbrev_in_brackets, bahai_library_url)
# `abbrev` is the prefix used inside [..#..] on bahai-library, e.g. 'PT' for
# Paris Talks. Most often equals collection_key.upper(); included explicitly
# for safety since some pages use different abbrevs (e.g. 'PUP', 'APBH').
SOURCES = [
    ("paristalks", "PT",   "https://bahai-library.com/abdul-baha_paris_talks"),
    ("summons",    "SLH",  "https://bahai-library.com/bahaullah_summons_lord_hosts"),
    ("esw",        "ESW",  "https://bahai-library.com/bahaullah_epistle_son_wolf"),
    ("svfv",       "SVFV", "https://bahai-library.com/bahaullah_seven_valleys"),
    ("gems",       "GDM",  "https://bahai-library.com/bahaullah_gems_divine_mysteries"),
    ("tabernacle", "TU",   "https://bahai-library.com/bahaullah_tabernacle_unity"),
    ("call",       "CDB",  "https://bahai-library.com/bahaullah_call_divine_beloved"),
    ("days",       "DOR",  "https://bahai-library.com/bahaullah_days_remembrance"),
    ("tablets",    "TB",   "https://bahai-library.com/bahaullah_tablets_revealed_aqdas"),
    ("swab",       "SWAB", "https://bahai-library.com/abdul-baha_selections_writings"),
    ("memorials",  "MOF",  "https://bahai-library.com/abdul-baha_memorials_faithful"),
    ("sdc",        "SDC",  "https://bahai-library.com/abdul-baha_secret_divine_civilization"),
    ("tn",         "TN",   "https://bahai-library.com/abdul-baha_travelers_narrative"),
    ("wt",         "WT",   "https://bahai-library.com/abdul-baha_will_testament"),
    ("divineplan", "TDP",  "https://bahai-library.com/abdul-baha_tablets_divine_plan"),
    ("hague",      "TH",   "https://bahai-library.com/abdul-baha_to_hague"),
    ("light",      "LW",   "https://bahai-library.com/abdul-baha_light_world"),
    ("pup",        "PUP",  "https://bahai-library.com/abdul-baha_promulgation_universal_peace"),
    ("gpb",        "GPB",  "https://bahai-library.com/shoghieffendi_god_passes_by"),
    ("wob",        "WOB",  "https://bahai-library.com/shoghieffendi_world_order"),
    ("adj",        "ADJ",  "https://bahai-library.com/shoghieffendi_advent_divine_justice"),
    ("pdc",        "PDC",  "https://bahai-library.com/shoghieffendi_promised_day_come"),
    ("apbh",       "APBH", "https://bahai-library.com/bahaullah_additional_prayers"),
    ("apab",       "APAB", "https://bahai-library.com/abdul-baha_additional_prayers"),
]


def fetch(url, delay=1.0):
    req = urllib.request.Request(url, headers={"User-Agent": UA})
    try:
        with urllib.request.urlopen(req, timeout=30) as resp:
            content = resp.read().decode("utf-8", errors="replace")
        time.sleep(delay)
        # Some bahai-library pages just serve a redirect meta. Follow it.
        m = re.search(r'URL=([^"\s]+)', content[:500])
        if m and 'redirecting' in content[:200].lower():
            return fetch(m.group(1), delay)
        return content
    except urllib.error.HTTPError as e:
        sys.stderr.write(f"  fetch HTTP {e.code} {url}\n")
        return None
    except Exception as e:
        sys.stderr.write(f"  fetch error {url}: {e}\n")
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


def ensure_table():
    dolt("""
CREATE TABLE IF NOT EXISTS writing_collections (
    collection_key VARCHAR(50) NOT NULL,
    position INT NOT NULL,
    phelps VARCHAR(16),
    page_ref VARCHAR(20),
    is_excerpt TINYINT(1) DEFAULT 0,
    mnemonic VARCHAR(10),
    title VARCHAR(255),
    source_url VARCHAR(255),
    PRIMARY KEY (collection_key, position),
    KEY (phelps)
);
""")


def parse_codes(html, abbrev):
    """Extract (position, phelps, is_excerpt, page_ref).

    Two formats supported:
      A) Annotated:  <code>[<ABBREV>#<NUM> p.<PAGE>]  — explicit position+page
      B) Bare list:  <code> in 'Inventory #' table row, ordered. Position
         is inferred from document order.
    """
    pat_annotated = rf'((?:AB|ABU|BB|BH|UH)\d{{4,5}})(x?)\[\s*{re.escape(abbrev)}\s*#?\s*(\d+)\s+p\.(\d+)\]'
    matches = re.findall(pat_annotated, html)
    if matches:
        by_pos = {}
        for code, x, num, page in matches:
            n = int(num)
            if n not in by_pos:
                by_pos[n] = (code, x == 'x', f"p.{page}")
        return [(n, by_pos[n][0], by_pos[n][1], by_pos[n][2]) for n in sorted(by_pos)]

    # Fall back to bare-code extraction from the 'Inventory #' table cell.
    # Anchor on "Inventory #" label, take the next <td>…</td> block.
    m = re.search(
        r'Inventory\s*#.*?<td[^>]*class="metadatacontent"[^>]*>(.*?)</td>',
        html, re.DOTALL | re.IGNORECASE,
    )
    if not m:
        return []
    cell = m.group(1)
    # Sequence of codes with optional 'x' marker
    bare = re.findall(r'((?:AB|ABU|BB|BH|UH)\d{4,5})(x?)', cell)
    # Dedup adjacent dupes (link wrapper + visible text). But keep order.
    out = []
    last = None
    for code, x in bare:
        key = (code, x)
        if last == key:
            continue
        last = key
        out.append((len(out) + 1, code, x == 'x', None))
    return out


def main():
    ap = argparse.ArgumentParser()
    ap.add_argument("--only", help="Only process this collection_key")
    ap.add_argument("--dry-run", action="store_true")
    args = ap.parse_args()

    if not args.dry_run:
        ensure_table()
        print("Table ensured.")

    for key, abbrev, url in SOURCES:
        if args.only and args.only != key:
            continue
        print(f"\n=== {key} ({abbrev}) ===")
        h = fetch(url)
        if not h:
            print("  fetch failed, skipping")
            continue
        rows = parse_codes(h, abbrev)
        print(f"  {len(rows)} positions found  ({url})")
        if not rows:
            continue

        if args.dry_run:
            for r in rows[:3]:
                print(f"  position {r[0]}: {r[1]} excerpt={r[2]} {r[3]}")
            continue

        # Clear and re-insert
        dolt(f"DELETE FROM writing_collections WHERE collection_key='{key}';")
        values = []
        for position, phelps, is_excerpt, page_ref in rows:
            page_ref_sql = f"'{page_ref}'" if page_ref else "NULL"
            values.append(
                f"('{key}',{position},'{phelps}',{page_ref_sql},{1 if is_excerpt else 0},NULL,NULL,'{url}')"
            )
        stmt = (
            "INSERT INTO writing_collections "
            "(collection_key, position, phelps, page_ref, is_excerpt, mnemonic, title, source_url) "
            "VALUES " + ",".join(values) + ";"
        )
        dolt(stmt)
        print(f"  inserted {len(rows)} rows into writing_collections")

    print("\nDone.")


if __name__ == "__main__":
    main()
