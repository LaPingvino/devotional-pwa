#!/usr/bin/env python3
"""match_compilation.py — Forward-incipit matcher for any bahai-library
compilation (APBH, APAB, HW, etc).

Reads a JSON config describing the compilation:
  {
    "tag": "APBH",                    # used in PBS notes
    "out_suffix": "apbh",             # output filename: bpapp_<L>_<suffix>.sql
    "category_re": "Additional Prayers Revealed by Bah",
    "inventory": [                    # ordered: [pos, phelps, is_excerpt]
       ["01", "BH00064", true], ...
    ],
    "excerpt_incipits": {             # per-language overrides
      "en": {
        "01": ["PUR", "i swear by thy glory ..."],
        ...
      }
    },
    "skip": {                         # per-language positions to skip entirely
      "en": ["06"]
    }
  }

For each entry, looks up its inventory "First line (translated)" (or override
incipit), finds the bpapp residual whose opening text contains it. Emits
sub-coded writings rows for excerpts; uses parent codes for non-excerpts.
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


def normalize(s):
    s = s.lower()
    s = re.sub(r"[^\w\s]", " ", s)
    s = re.sub(r"\s+", " ", s)
    return s.strip()


def load_inventory():
    by_pin = {}
    with INVENTORY_CSV.open(encoding="utf-8", errors="replace") as f:
        rdr = csv.DictReader(f)
        for row in rdr:
            pin = row.get("PIN", "")
            tr = row.get("First line (translated)", "") or ""
            if pin and tr and (pin not in by_pin or len(tr) > len(by_pin[pin])):
                by_pin[pin] = tr
    return by_pin


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
    """The bpapp dropcap unwrap leaves "Iswear"/"Omy" where the dropcap
    was a single-letter word ("I"/"O"/"A") followed by lowercase."""
    if not text:
        return text
    return re.sub(r"^([IOA])([a-z])", r"\1 \2", text)


def overlap_ratio(a, b):
    aw = a.split()[:12]
    bw = b.split()[:12]
    if not aw or not bw:
        return 0.0
    sa, sb = set(aw), set(bw)
    return len(sa & sb) / max(len(sa), len(sb))


def process(lang, cfg, src_id_start):
    review = ROOT / f"bpapp_{lang}_review.tsv"
    if not review.exists():
        return 0, src_id_start
    inv = load_inventory()
    inv_norm = {k: normalize(v) for k, v in inv.items()}
    cat_re = re.compile(cfg["category_re"])
    overrides = cfg.get("excerpt_incipits", {}).get(lang, {})
    skip = set(cfg.get("skip", {}).get(lang, []))
    inventory = cfg["inventory"]
    out_path = ROOT / f"bpapp_{lang}_{cfg['out_suffix']}.sql"

    cands = []
    with review.open(encoding="utf-8", errors="replace") as f:
        rdr = csv.DictReader(f, delimiter="\t")
        for r in rdr:
            if not cat_re.search(r["category"]):
                continue
            cache = cache_load(lang, r["bpapp_id"])
            text = (cache or {}).get("full_text") or r["start_text"]
            if not text:
                continue
            cands.append({
                "bpapp_id": r["bpapp_id"],
                "category": r["category"],
                "norm": normalize(text),
                "raw_len": len(text),
                "cat_order": r.get("cat_order") or "0",
                "ord_in_cat": r.get("ord_in_cat") or "0",
            })

    matches = []
    used_cands = set()
    used_phelps = set()

    for pos, base_phelps, is_excerpt in inventory:
        if pos in skip:
            continue
        override = overrides.get(pos)
        mnemonic = override[0] if override else ""
        if is_excerpt and not mnemonic:
            continue  # no parent-code shadowing of excerpts
        inv_text = (override[1] if override else "") or inv_norm.get(base_phelps, "")
        if not inv_text:
            continue
        probe = inv_text[:80]
        inv_len = len(inv_text)
        best = None
        for c in cands:
            if c["bpapp_id"] in used_cands:
                continue
            n = c["norm"]
            score = 0.0
            reason = ""
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
                if len(probe) >= L and probe[:L] in head:
                    off = head.find(probe[:L])
                    score = 0.90 + L / 1000.0 - off / 5000.0
                    reason = f"incipit-substr-{L}@{off}"
                    break
            if not score:
                ov = overlap_ratio(probe, n)
                if ov >= 0.7:
                    score = 0.6 + ov * 0.2
                    reason = f"overlap-{ov:.2f}"
            if score and (best is None or score > best[0]):
                best = (score, c, reason)
        if best and best[0] >= 0.7:
            score, c, reason = best
            if base_phelps in used_phelps and not is_excerpt:
                continue
            sub_phelps = base_phelps + mnemonic if (is_excerpt and mnemonic) else base_phelps
            matches.append((pos, base_phelps, sub_phelps, is_excerpt, c, score, reason))
            used_cands.add(c["bpapp_id"])
            used_phelps.add(sub_phelps)

    sid = src_id_start
    n_match = 0
    rows_total = len(cands)
    tag = cfg["tag"]
    with out_path.open("w") as out:
        out.write(f"-- {tag} forward-incipit matches for {lang}:bpapp\n")
        out.write(f"-- {len(matches)}/{rows_total} mapped\n")
        out.write("SET FOREIGN_KEY_CHECKS=0;\n\n")
        for pos, base_phelps, sub_phelps, is_excerpt, c, score, reason in matches:
            cat_ord = 0
            ord_in_cat = 0
            try: cat_ord = int(c["cat_order"])
            except: pass
            try: ord_in_cat = int(c["ord_in_cat"])
            except: pass
            note = (f"{tag}#{pos}" + (" excerpt" if is_excerpt else "") +
                    f"; bpapp_id={c['bpapp_id']}; {reason}={score:.2f}")
            cat_esc = c["category"].replace("'", "''")
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
        out.write(f"\n-- Unmapped {tag} PINs:\n")
        mapped_pins = {m[1] for m in matches}
        for pos, base, exc in inventory:
            if base not in mapped_pins:
                out.write(f"--   {tag}#{pos} {base}{'x' if exc else ''}\n")
        out.write("\n-- Unmapped residuals in this category:\n")
        for c in cands:
            if c["bpapp_id"] not in used_cands:
                out.write(f"--   bpapp_id={c['bpapp_id']}: {c['norm'][:90]}\n")
        out.write("\nSET FOREIGN_KEY_CHECKS=1;\n")
    print(f"[{lang}] {n_match}/{rows_total} {tag} matches → {out_path.name}")
    return n_match, sid


def main():
    ap = argparse.ArgumentParser()
    ap.add_argument("--config", required=True, help="JSON config (e.g. configs/apbh.json)")
    ap.add_argument("--langs", required=True)
    ap.add_argument("--src-id-start", type=int, default=60000)
    args = ap.parse_args()
    cfg = json.loads(Path(args.config).read_text())
    sid = args.src_id_start
    total = 0
    for L in args.langs.split(","):
        n, sid = process(L, cfg, sid)
        total += n
    print(f"Total: {total} {cfg['tag']} matches")


if __name__ == "__main__":
    main()
