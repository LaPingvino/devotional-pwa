#!/usr/bin/env python3
"""match_apbh.py — Deterministic forward-incipit matcher for APBH residuals.

For each APBH PIN, look up its inventory "First line (translated)" and find
the bpapp residual whose opening text matches that incipit (substring or
high-overlap). This is much more precise than the previous reverse search.

Excerpt entries (marked x in the APBH index) have a different incipit than
their parent tablet; we still try a substring match (the inventory sometimes
includes the excerpt text), but most excerpts will need manual or PDF-based
mapping.

Outputs bpapp_<L>_apbh.sql with INSERT IGNORE statements + skip notes.
"""
import argparse
import csv
import json
import re
import sys
import uuid
from pathlib import Path

ROOT = Path(__file__).resolve().parent.parent
INVENTORY_CSV = Path.home() / "prayermatching/data/inventory_export.csv"

# (apbh_num, base_phelps, is_excerpt). Excerpt entries get a 3-letter
# mnemonic suffix appended manually in a follow-up pass.
APBH = [
    ("01", "BH00064", True),  ("02", "BH10742", False), ("04", "BH10506", False),
    ("05", "BH01873", True),  ("06", "BH01873", True),  ("07", "BH11086", False),
    ("08", "BH10588", False), ("09", "BH10149", False), ("10", "BH01393", True),
    ("11", "BH03315", False), ("12", "BH00248", True),  ("13", "BH10328", False),
    ("14", "BH03386", True),  ("15", "BH02274", True),  ("16", "BH10889", False),
    ("17", "BH02153", True),  ("18", "BH02467", False), ("20", "BH10507", False),
    ("21", "BH11641", False), ("22", "BH02112", True),  ("23", "BH00483", True),
    ("24", "BB00633", False),
]


# Known excerpt incipits (EN). Inventory only has parent-tablet incipits,
# so for `x`-marked APBH entries we need an explicit excerpt opener. Add
# more here as collection-page mappings are confirmed.
# Keyed by BRL_APBH# (the gappy 1-24 numbering with #03/#19 gaps).
# Each entry: (3-letter mnemonic for sub-code, normalized incipit for matching).
# Mnemonic == "" for non-excerpts (no sub-code needed).
# Verified by cross-checking against bahai-library.com inventory pages.
EXCERPT_INCIPITS_EN = {
    # Mnemonics aligned with pre-existing sub-codes already in the writings table
    # where present (PUR/SON/SAN/MOU/PRA/TEA), to avoid duplicate-mnemonic drift.
    "01": ("PUR", "i swear by thy glory o my god i am astonished"),
    "05": ("SON", "he is god exalted is he the lord of wisdom and utterance"),
    # "06": ("DOV", "...") — BH01873 DOV; incipit unknown
    "14": ("TEA", "praise be to thee o my god that thou didst graciously remember me through thy most exalted pen"),
    "15": ("PRA", "o god my god i yield thee thanks for having guided me unto thy straight path"),
    "16": ("",    "although this wretched state in which i am"),       # BH10889 not excerpt
    "17": ("MOU", "o my lord my master and the goal of my desire i have heard that thou hast declared this to be a day"),
    "23": ("SAN", "o thou by whose name the sea of joy moveth"),
}
SKIP_APBH_EN = {"06"}


def normalize(s):
    s = s.lower()
    s = re.sub(r"[^\w\s]", " ", s)
    s = re.sub(r"\s+", " ", s)
    return s.strip()


def load_inventory():
    """PIN → longest "First line (translated)" seen."""
    by_pin = {}
    with INVENTORY_CSV.open(encoding="utf-8", errors="replace") as f:
        rdr = csv.DictReader(f)
        for row in rdr:
            pin = row.get("PIN", "")
            tr = row.get("First line (translated)", "") or ""
            if pin and tr and (pin not in by_pin or len(tr) > len(by_pin[pin])):
                by_pin[pin] = tr
    return by_pin


def cache_text(lang, bpapp_id):
    p = ROOT / "bpapp_cache" / lang / f"prayer_{bpapp_id}.json"
    if not p.exists():
        return ""
    try:
        return json.loads(p.read_text())["full_text"]
    except Exception:
        return ""


def cache_load(lang, bpapp_id):
    p = ROOT / "bpapp_cache" / lang / f"prayer_{bpapp_id}.json"
    if not p.exists():
        return None
    try:
        return json.loads(p.read_text())
    except Exception:
        return None


def sql_esc(s):
    return (s or "").replace("'", "''")


def fix_dropcap_glue(text):
    """The bpapp dropcap unwrap leaves "Iswear"/"Omy"/"Athou" where the
    dropcap was a single-letter word ("I"/"O"/"A") followed by lowercase.
    Insert the missing space."""
    if not text:
        return text
    return re.sub(r"^([IOA])([a-z])", r"\1 \2", text)


def overlap_ratio(a, b):
    """Word-set overlap of first ~12 words of each."""
    aw = a.split()[:12]
    bw = b.split()[:12]
    if not aw or not bw:
        return 0.0
    sa, sb = set(aw), set(bw)
    return len(sa & sb) / max(len(sa), len(sb))


def process(lang, category_pattern, src_id_start):
    review = ROOT / f"bpapp_{lang}_review.tsv"
    if not review.exists():
        return 0, src_id_start
    inv = load_inventory()
    inv_norm = {k: normalize(v) for k, v in inv.items()}
    out_path = ROOT / f"bpapp_{lang}_apbh.sql"

    # Collect candidate residuals in the APBH-like category
    cands = []
    with review.open(encoding="utf-8", errors="replace") as f:
        rdr = csv.DictReader(f, delimiter="\t")
        for r in rdr:
            if not re.search(category_pattern, r["category"]):
                continue
            text = cache_text(lang, r["bpapp_id"]) or r["start_text"]
            if not text:
                continue
            cands.append({
                "bpapp_id": r["bpapp_id"],
                "category": r["category"],
                "norm": normalize(text),
                "cat_order": r.get("cat_order") or "0",
                "ord_in_cat": r.get("ord_in_cat") or "0",
            })

    matches = []  # (apbh_num, phelps, is_excerpt, cand, score, reason)
    used_cands = set()
    used_phelps = set()

    # Pass 1: forward incipit match — each PIN looks for a residual whose
    # opening words contain the inventory incipit
    for apbh_num, base_phelps, is_excerpt in APBH:
        if lang == "en" and apbh_num in SKIP_APBH_EN:
            continue
        # Excerpt overrides take priority over the inventory's parent incipit.
        # Excerpts WITHOUT a known mnemonic are skipped — emitting an excerpt
        # under its parent code would silently shadow the full tablet later.
        override = EXCERPT_INCIPITS_EN.get(apbh_num) if lang == "en" else None
        mnemonic = override[0] if override else ""
        if is_excerpt and not mnemonic:
            continue
        inv_text = (override[1] if override else "") or inv_norm.get(base_phelps, "")
        if not inv_text:
            continue
        probe = inv_text[:80]
        best = None
        for c in cands:
            if c["bpapp_id"] in used_cands:
                continue
            n = c["norm"]
            score = 0.0
            reason = ""
            # strong: residual starts with inventory incipit (first 60+ chars)
            head = n[:300]
            for L in (80, 60, 40):
                if len(probe) >= L and n.startswith(probe[:L]):
                    score = 0.95 + L / 1000.0
                    reason = f"incipit-prefix-{L}"
                    break
                if len(n) >= L and inv_text.startswith(n[:L]):
                    score = 0.92 + L / 1000.0
                    reason = f"prefix-rev-{L}"
                    break
                # bpapp often has a "He is God..." invocation prefix; allow
                # the inventory probe to appear as a substring near the start.
                if len(probe) >= L and probe[:L] in head:
                    off = head.find(probe[:L])
                    score = 0.90 + L / 1000.0 - off / 5000.0
                    reason = f"incipit-substr-{L}@{off}"
                    break
            if not score:
                # fall back to first-N-words overlap
                ov = overlap_ratio(probe, n)
                if ov >= 0.7:
                    score = 0.6 + ov * 0.2
                    reason = f"overlap-{ov:.2f}"
            if score and (best is None or score > best[0]):
                best = (score, c, reason)
        if best and best[0] >= 0.7:
            score, c, reason = best
            if base_phelps in used_phelps and not is_excerpt:
                continue  # avoid double-mapping non-excerpt phelps to two residuals
            sub_phelps = base_phelps + mnemonic if (is_excerpt and mnemonic) else base_phelps
            matches.append((apbh_num, base_phelps, sub_phelps, is_excerpt, c, score, reason))
            used_cands.add(c["bpapp_id"])
            used_phelps.add(sub_phelps)

    sid = src_id_start
    n_match = 0
    rows_total = len(cands)
    with out_path.open("w") as out:
        out.write(f"-- APBH forward-incipit matches for {lang}:bpapp\n")
        out.write(f"-- {len(matches)}/{rows_total} mapped\n")
        out.write("SET FOREIGN_KEY_CHECKS=0;\n\n")
        for apbh_num, base_phelps, sub_phelps, is_excerpt, c, score, reason in matches:
            cat_ord = 0
            ord_in_cat = 0
            try: cat_ord = int(c["cat_order"])
            except: pass
            try: ord_in_cat = int(c["ord_in_cat"])
            except: pass
            note = (f"APBH#{apbh_num}" + (" excerpt" if is_excerpt else "") +
                    f"; bpapp_id={c['bpapp_id']}; {reason}={score:.2f}")
            cat_esc = c["category"].replace("'", "''")
            # For excerpts: ensure a writings row exists under the sub-code so
            # PBS can FK to it. Carry the bpapp text + title.
            if is_excerpt and sub_phelps != base_phelps:
                cache = cache_load(lang, c["bpapp_id"])
                text = fix_dropcap_glue((cache or {}).get("full_text", ""))[:8000]
                name = (cache or {}).get("title", "") or ""
                ver = str(uuid.uuid4())
                out.write(
                    f"INSERT IGNORE INTO writings "
                    f"(version, source, source_id, language, phelps, name, text) VALUES "
                    f"('{ver}', 'bahaiprayers.app', '{sql_esc(c['bpapp_id'])}', '{lang}', "
                    f"'{sub_phelps}', '{sql_esc(name)}', '{sql_esc(text)}');\n"
                )
            out.write(
                f"INSERT IGNORE INTO prayer_book_structure "
                f"(source_id, source_language, version, phelps_code, "
                f"category_name, category_order, order_in_category, notes) VALUES "
                f"({sid}, '{lang}:bpapp', 'bpapp', '{sub_phelps}', "
                f"'{cat_esc}', {cat_ord}, {ord_in_cat}, '{note}');\n"
            )
            sid += 1
            n_match += 1
        # Note unmapped APBH entries and unmapped residuals
        out.write("\n-- Unmapped APBH PINs:\n")
        mapped_pins = {m[1] for m in matches}
        for apbh_num, base, exc in APBH:
            if base not in mapped_pins:
                out.write(f"--   APBH#{apbh_num} {base}{'x' if exc else ''}\n")
        out.write("\n-- Unmapped residuals in this category:\n")
        for c in cands:
            if c["bpapp_id"] not in used_cands:
                out.write(f"--   bpapp_id={c['bpapp_id']}: {c['norm'][:90]}\n")
        out.write("\nSET FOREIGN_KEY_CHECKS=1;\n")
    print(f"[{lang}] {n_match}/{rows_total} APBH matches → {out_path.name}")
    return n_match, sid


def main():
    ap = argparse.ArgumentParser()
    ap.add_argument("--langs", required=True)
    ap.add_argument("--src-id-start", type=int, default=60000)
    ap.add_argument("--category", default=r"Additional Prayers Revealed by Bah",
                    help="regex matched against the review TSV's category column")
    args = ap.parse_args()
    sid = args.src_id_start
    total = 0
    for L in args.langs.split(","):
        n, sid = process(L, args.category, sid)
        total += n
    print(f"Total: {total} APBH matches")


if __name__ == "__main__":
    main()
