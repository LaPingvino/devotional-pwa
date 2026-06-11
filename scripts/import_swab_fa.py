#!/usr/bin/env python3
"""Import the Persian original of Selections from the Writings of 'Abdu'l-Bahá
(منتخباتى از مكاتيب حضرت عبدالبهاء) from bahai.org as fa triangulation anchors.

The bahai.org reading interface is a JS app (the /N pages are shells), but the
full-text .xhtml download contains everything. Each selection is delimited by a
`<p class="ub">` paragraph holding a Persian numeral (۱, ۲, ۳…); that numeral is
the SWAB selection number. Our en SWAB rows (source=bahai.org/ab-selections,
name="SWAB N") already map each selection N → an AB base PIN, so we attach the
Persian selection text to that base code.

Stores one fa row per selection:
  phelps = AB base PIN (7-char)   language = fa   name = "SWAB N"
  source = bahai.org/ab-selections   link = .xhtml URL#sel<N>
Idempotent: skips a (base, name) that already has an ab-selections fa row.

Usage:  python3 scripts/import_swab_fa.py [--apply]   (default dry-run)
"""
import argparse, html, re, subprocess, sys, urllib.request, uuid

DOLT_DIR = "/home/joop/bahaiwritings"
XHTML_URL = ("https://www.bahai.org/fa/library/authoritative-texts/abdul-baha/"
             "selections-writings-abdul-baha/selections-writings-abdul-baha.xhtml")
SOURCE = "bahai.org/ab-selections"
UA = "Mozilla/5.0 (compatible; BahaiTextAligner/1.0)"

FA2 = {'۰':'0','۱':'1','۲':'2','۳':'3','۴':'4','۵':'5','۶':'6','۷':'7','۸':'8','۹':'9'}
def faint(s):
    d = ''.join(FA2.get(c, '') for c in s)
    return int(d) if d else None

def clean(b):
    return html.unescape(re.sub(r'<[^>]+>', '', b)).replace('‌', ' ').strip()

def dolt(query, csv=False):
    args = ["dolt", "sql"] + (["--result-format", "csv"] if csv else [])
    r = subprocess.run(args, cwd=DOLT_DIR, capture_output=True, text=True, input=query)
    if r.returncode != 0:
        sys.stderr.write(f"DOLT ERROR: {r.stderr}\nQUERY: {query[:200]}\n"); sys.exit(1)
    return r.stdout

def fetch(url):
    req = urllib.request.Request(url, headers={"User-Agent": UA})
    return urllib.request.urlopen(req, timeout=60).read().decode("utf-8")

def parse_selections(xhtml):
    """Return ordered list of (sel_num, full_fa_text). Filters non-selection
    numerals: a marker must be a short ub-class numeral AND the running sequence
    must stay monotonic 1..~240 (drops stray footnote numbers like ۲۰۲۱)."""
    ps = re.findall(r'<p([^>]*)>(.*?)</p>', xhtml, re.S)
    sels, cur, seen = [], None, set()
    for attr, body in ps:
        cls = re.search(r'class="([^"]+)"', attr)
        cls = cls.group(1).split() if cls else []
        txt = clean(body)
        n = faint(txt) if (txt and len(txt) <= 4) else None
        # selection markers are ub-class numerals in 1..240 (footnotes aren't ub;
        # drops stray in-text numbers like ۲۰۲۱ that exceed the SWAB count)
        is_marker = ('ub' in cls) and (n is not None) and (1 <= n <= 240) and (n not in seen)
        if is_marker:
            if cur:
                sels.append(cur)
            cur = [n, []]; seen.add(n)
        elif cur is not None and txt:
            cur[1].append(txt)
    if cur:
        sels.append(cur)
    return [(n, "\n".join(parts)) for n, parts in sels if parts]

def main():
    ap = argparse.ArgumentParser()
    ap.add_argument("--apply", action="store_true")
    args = ap.parse_args()

    # selection N -> AB base, from the already-imported en SWAB
    out = dolt("SELECT DISTINCT name, SUBSTRING(phelps,1,7) FROM writings "
               "WHERE source='bahai.org/ab-selections' AND language='en' "
               "AND name LIKE 'SWAB %'", csv=True)
    def selnum(name):
        # "SWAB 227" -> 227 ; "SWAB 129 11" -> 129
        m = re.match(r'SWAB\s+(\d+)', name)
        return int(m.group(1)) if m else None
    sel2base = {}
    for line in out.strip().splitlines()[1:]:
        parts = line.rsplit(",", 1)
        if len(parts) == 2:
            sn = selnum(parts[0])
            if sn is not None:
                sel2base[sn] = parts[1]   # base is identical across a selection's paragraphs

    # selection numbers that already have a fa ab-selections row (idempotency)
    out = dolt("SELECT DISTINCT name FROM writings WHERE source='bahai.org/ab-selections' "
               "AND language='fa' AND name LIKE 'SWAB %'", csv=True)
    have_fa = {selnum(l.strip()) for l in out.strip().splitlines()[1:]}
    have_fa.discard(None)

    sels = parse_selections(fetch(XHTML_URL))
    print(f"parsed {len(sels)} fa selections (#{sels[0][0]}..#{sels[-1][0]}); "
          f"en map covers {len(sel2base)} selections; "
          f"{len(have_fa)} already have fa")

    rows, skipped_nobase, skipped_have = [], [], []
    for n, text in sels:
        name = f"SWAB {n}"
        base = sel2base.get(n)
        if not base:
            skipped_nobase.append(n); continue
        if n in have_fa:
            skipped_have.append(n); continue
        rows.append((base, name, text))

    print(f"to insert: {len(rows)} | skip(no en base): {len(skipped_nobase)} "
          f"{skipped_nobase[:8]} | skip(already fa): {len(skipped_have)}")
    if rows[:2]:
        for base, name, text in rows[:2]:
            print(f"  e.g. {name} -> {base}  ({len(text)} ch)  {text[:55]}")

    if not args.apply:
        print("\n[dry-run] re-run with --apply to insert")
        return

    # Insert in batches. fa text has no apostrophes issue? escape just in case.
    def esc(s): return s.replace("\\", "\\\\").replace("'", "\\'")
    vals = []
    for base, name, text in rows:
        v = str(uuid.uuid4())
        # build CONCAT for newlines so it's a single line per row
        parts = text.split("\n")
        textsql = "CONCAT(" + ",CHAR(10),".join("'" + esc(p) + "'" for p in parts) + ")" if len(parts) > 1 else "'" + esc(text) + "'"
        vals.append((base, name, v, textsql))
    BATCH = 40
    inserted = 0
    for i in range(0, len(vals), BATCH):
        chunk = vals[i:i+BATCH]
        stmts = ["SET FOREIGN_KEY_CHECKS=0;"]
        for base, name, v, textsql in chunk:
            stmts.append(
                "INSERT INTO writings (phelps,language,version,name,type,text,source,link,is_verified) "
                f"VALUES ('{base}','fa','{v}','{esc(name)}','swab',{textsql},"
                f"'{SOURCE}','{XHTML_URL}',0);")
        stmts.append("SET FOREIGN_KEY_CHECKS=1;")
        dolt("\n".join(stmts))
        inserted += len(chunk)
        print(f"  inserted {inserted}/{len(vals)}")
    print(f"DONE: {inserted} fa SWAB anchors added")

if __name__ == "__main__":
    main()
