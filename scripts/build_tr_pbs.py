#!/usr/bin/env python3
"""Parse Turkish prayer book HTML from Archive.org to build prayer_book_structure.

Strategy: The HTML page order matches DB source_id order. Extract paragraphs,
identify category headers, then match each DB prayer opening to find its position
in the HTML. Track the last-seen category header for each match."""

import subprocess, csv, io, os, re, sys
from difflib import SequenceMatcher

TMPDIR = os.environ.get("TMPDIR", "/tmp")
dolt_dir = os.path.expanduser("~/bahaiwritings")

def dq(q):
    r = subprocess.run(["dolt", "sql", "-q", q, "--result-format", "csv"],
                       capture_output=True, text=True, cwd=dolt_dir)
    return list(csv.DictReader(io.StringIO(r.stdout)))

# Read UTF-8 converted HTML (run: iconv -f windows-1254 -t utf-8 tr_dua.html > tr_dua_utf8.html)
html_path = os.path.join(TMPDIR, "tr_dua_utf8.html")
with open(html_path) as f:
    html = f.read()

# Extract paragraphs
raw_paras = re.findall(r'<p[^>]*>(.*?)</p>', html, re.DOTALL | re.I)
paras = []
for p in raw_paras:
    t = re.sub(r'<[^>]+>', '', p)
    t = t.replace('&amp;', '&').replace('&lt;', '<').replace('&gt;', '>')
    t = t.replace('&nbsp;', ' ').replace('&#39;', "'").replace('&quot;', '"')
    t = re.sub(r'\s+', ' ', t).strip()
    if t and len(t) > 1:
        paras.append(t)

print(f"Extracted {len(paras)} paragraphs from HTML", file=sys.stderr)

# Category headers → English categories
category_map = {
    "SABAH DUASI": "Morning",
    "SABAH VE AKŞAM DUASI": "Morning and Evening",
    "UYKUYA YATMA DUASI": "Evening",
    "YATARKEN OKUNACAK DUA": "Evening",
    "EVDEN ÇIKMA DUASI": "Protection",
    "YOLCULUĞA ÇIKARKEN OKUNACAK DUA": "Protection",
    "ŞİFA İÇİN": "Healing",
    "DARLIK VE BORÇ DUASI": "Tests and Difficulties",
    "ÖZEL DUALAR": "Special Prayers",
    "BÜYÜK NAMAZ": "Long Obligatory Prayer",
    "ORTA NAMAZ": "Medium Obligatory Prayer",
    "KÜÇÜK NAMAZ": "Short Obligatory Prayer",
    "CENAZE NAMAZI": "Burial Prayer",
    "ULU ADIMLA": "Tablets",
    "ORUÇ LEVHİ": "Fasting",
    "ORUÇ DUASI": "Fasting",
    "RIZVÂN BAYRAMI DUASI": "Ridván",
    "NİKÂH DUASI": "Marriage",
    "BAHAİ NİKÂH HUTBESİ": "Marriage",
    "AHMED LEVHİ": "Tablet of Ahmad",
    "KERMİL LEVHİ": "Tablet of Carmel",
    "NEVZÂDIN ADIYLA": "Naw-Rúz",
}

author_headers = {
    "Hz. BAHAULLAH'ın MÜNÂCÂTLARI",
    "Hz. ABDÜLBAHA'nın DUALARI",
    "Hz. BÂB'ın DUALARI",
}

# Load DB prayers
db_prayers = dq("""
SELECT source_id, phelps, LEFT(text, 150) as text
FROM writings WHERE language='tr' AND source='bahaiprayers.org'
ORDER BY CAST(source_id AS UNSIGNED)""")
print(f"Loaded {len(db_prayers)} prayers from DB", file=sys.stderr)

# Normalize for comparison
def norm(s):
    s = re.sub(r'[""''\"\'`]', '', s)
    s = re.sub(r'\s+', ' ', s).strip().lower()
    return s[:80]

# Walk through HTML paragraphs, track categories, match DB prayers sequentially
current_cat_tr = ""
current_cat_en = "Uncategorized"
db_ptr = 0
results = []
para_ptr = 0

while para_ptr < len(paras) and db_ptr < len(db_prayers):
    p = paras[para_ptr]

    # Check if this is an author section header (skip)
    if any(ah in p for ah in author_headers):
        para_ptr += 1
        continue

    # Check if this is a category header
    if p.strip() in category_map:
        current_cat_tr = p.strip()
        current_cat_en = category_map[current_cat_tr]
        print(f"  Category: {current_cat_tr} → {current_cat_en}", file=sys.stderr)
        para_ptr += 1
        continue

    # Check if this is the main title
    if p.strip() in ("BAHAİ DUALARI", "BAHAI DUALARI"):
        para_ptr += 1
        continue

    # Try to match this paragraph against current DB prayer
    db_entry = db_prayers[db_ptr]
    db_text = db_entry["text"].strip()
    # Strip ## header from DB text
    if db_text.startswith("##"):
        db_text = db_text.split("\n", 1)[-1].strip() if "\n" in db_text else db_text

    p_norm = norm(p)
    db_norm = norm(db_text)

    score = SequenceMatcher(None, p_norm[:60], db_norm[:60]).ratio()

    if score >= 0.5:
        # Match found
        results.append({
            "source_id": db_entry["source_id"],
            "phelps": db_entry["phelps"],
            "cat_tr": current_cat_tr,
            "cat_en": current_cat_en,
            "score": score,
        })
        db_ptr += 1
        para_ptr += 1
    else:
        # This paragraph doesn't match — it's either a continuation, invocation,
        # or instruction text. Skip it and try the next paragraph.
        para_ptr += 1

# Any remaining DB entries get the last known category
while db_ptr < len(db_prayers):
    results.append({
        "source_id": db_prayers[db_ptr]["source_id"],
        "phelps": db_prayers[db_ptr]["phelps"],
        "cat_tr": current_cat_tr,
        "cat_en": current_cat_en,
        "score": 0.0,
    })
    db_ptr += 1

matched_well = sum(1 for r in results if r["score"] >= 0.5)
print(f"\nMatched {matched_well}/{len(results)} with good score", file=sys.stderr)
print(f"Unmatched (tail): {len(results) - matched_well}", file=sys.stderr)

# Write CSV
out_path = os.path.join(TMPDIR, "tr_pbs_matched.csv")
with open(out_path, "w", newline="") as f:
    w = csv.writer(f)
    w.writerow(["source_id", "phelps", "category_turkish", "category_english",
                "order_in_category", "match_score"])
    cat_counter = {}
    for r in results:
        cat = r["cat_en"]
        cat_counter[cat] = cat_counter.get(cat, 0) + 1
        w.writerow([
            r["source_id"], r["phelps"], r["cat_tr"], r["cat_en"],
            cat_counter[cat], f"{r['score']:.2f}",
        ])

print(f"\nWritten to {out_path}", file=sys.stderr)

# Summary
print("\n=== Category summary ===", file=sys.stderr)
cats = {}
for r in results:
    cats.setdefault(r["cat_en"], []).append((r["source_id"], r["phelps"], r["score"]))
for cat in cats:
    entries = cats[cat]
    good = sum(1 for _, _, s in entries if s >= 0.5)
    sids = [f"{sid}({p})" for sid, p, _ in entries[:3]]
    print(f"  {cat}: {len(entries)} prayers ({good} matched) — {', '.join(sids)}...", file=sys.stderr)

# Show unmatched
print("\n=== Unmatched entries ===", file=sys.stderr)
for r in results:
    if r["score"] < 0.5:
        db_text = ""
        for d in db_prayers:
            if d["source_id"] == r["source_id"]:
                db_text = d["text"][:60]
                break
        print(f"  sid={r['source_id']} ({r['phelps']}) → {r['cat_en']} [score={r['score']:.2f}] {db_text}", file=sys.stderr)
