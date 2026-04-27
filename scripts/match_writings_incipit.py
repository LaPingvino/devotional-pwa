#!/usr/bin/env python3
"""match_writings_incipit.py — For each unmapped bpapp residual in a given
category, search the writings table for any phelps whose text starts with
(or contains near the start) the bpapp incipit.

This catches matches that bahai-library's compilation inventory misses:
XAB compilation codes, sub-coded AB excerpts (AB00495OPE etc), and any
other writings rows the original matcher missed for whatever reason.

Reads bpapp_<L>_review.tsv, filters by category regex, and for each
residual queries dolt for incipit matches. Emits PBS INSERTs for hits.
"""
import argparse
import csv
import json
import re
import subprocess
from pathlib import Path

ROOT = Path(__file__).resolve().parent.parent
DOLT_DIR = Path.home() / "bahaiwritings"

STOPWORDS = {
    "en": {"o", "the", "a", "an", "of", "to", "and", "in", "on", "for", "is", "i", "we", "be", "by", "thy", "thou", "thee", "art", "my"},
    "de": {"o", "der", "die", "das", "den", "dem", "des", "ein", "eine", "und", "in", "von", "zu", "ist", "ich", "wir", "du", "dein", "deine", "mein", "meine", "auf", "an"},
    "es": {"o", "el", "la", "los", "las", "un", "una", "y", "de", "en", "a", "para", "es", "yo", "soy", "tu", "tuyo", "mi", "que", "del"},
    "fr": {"o", "le", "la", "les", "un", "une", "des", "et", "de", "du", "à", "en", "pour", "est", "je", "nous", "tu", "ton", "ta", "tes", "mon", "ma", "mes", "qui", "que"},
    "pt": {"o", "a", "os", "as", "um", "uma", "e", "de", "do", "da", "dos", "das", "em", "para", "é", "eu", "nós", "tu", "teu", "tua", "meu", "minha", "que"},
    "it": {"o", "il", "la", "i", "gli", "le", "un", "una", "e", "di", "del", "in", "per", "è", "io", "noi", "tu", "tuo", "tua", "mio", "mia", "che"},
    "nl": {"o", "de", "het", "een", "en", "van", "in", "op", "is", "ik", "wij", "u", "uw", "mijn", "dat", "die"},
    "pl": {"o", "i", "w", "na", "z", "do", "od", "po", "jest", "ja", "my", "ty", "twój", "twoja", "twoje", "mój", "moja", "moje", "że", "który"},
    "ru": {"о", "и", "в", "на", "с", "по", "от", "к", "это", "я", "мы", "ты", "твой", "твоя", "мой", "моя", "что", "который"},
    "ar": {"و", "في", "من", "على", "إلى", "هو", "أنت", "أنا", "نحن", "يا", "ال"},
    "fa": {"و", "در", "از", "به", "که", "این", "آن", "تو", "من", "ما", "ای"},
    "ja": set(),  # CJK has no word boundaries; matcher will fall back to whole-text LIKE
    "zh-Hans": set(),
    "zh-Hant": set(),
}


def normalize_first_words(text, n_words=10):
    """Take first n_words real words from text, lowercased, no punctuation.
    Skip leading invocation patterns ("He is God.", "He is the X.", "## ...")."""
    if not text:
        return ""
    # strip markdown headers
    text = re.sub(r"^#+\s+[^\n]*\n+", "", text, flags=re.M)
    # strip common bpapp invocation prefixes (greedy, up to a sentence boundary)
    text = re.sub(r"^(He is God\.|He is the [A-Za-z\-]+\.?|He is God,[^.!]*\.|In the Name of [^.!]+!)\s*",
                  "", text, count=1)
    # Unicode-aware word extraction: any sequence of letter chars (covers
    # Latin, Cyrillic, Arabic/Persian, Hebrew, Greek, etc).
    words = re.findall(r"[^\W\d_]+", text, flags=re.UNICODE)
    return " ".join(w.lower() for w in words[:n_words])


def cache_load(lang, bpapp_id):
    p = ROOT / "bpapp_cache" / lang / f"prayer_{bpapp_id}.json"
    if not p.exists():
        return None
    try:
        return json.loads(p.read_text())
    except Exception:
        return None


def dolt_query(sql):
    """Run a SELECT via dolt sql; returns list of row dicts (parsed from |-format)."""
    try:
        r = subprocess.run(["dolt", "sql", "-r", "json", "-q", sql],
                           capture_output=True, text=True, cwd=str(DOLT_DIR), timeout=60)
        if r.returncode != 0:
            return []
        out = json.loads(r.stdout) if r.stdout.strip() else {}
        return out.get("rows", [])
    except Exception as e:
        print(f"  dolt error: {e}")
        return []


LANG_ALIASES = {"zh": ["zh-Hans", "zh-Hant"]}


def lang_clause(lang):
    """Return SQL fragment for matching one or more language codes."""
    aliases = LANG_ALIASES.get(lang, [lang])
    if len(aliases) == 1:
        return f"language='{aliases[0]}'"
    return "language IN (" + ",".join(f"'{a}'" for a in aliases) + ")"


def search_phelps_cjk(lang, full_text):
    """CJK fallback: search for the first ~12-char substring (skipping
    invocation prefix patterns like '上帝至高無上' or initial whitespace)."""
    if not full_text:
        return []
    # Strip leading "He is God"-equivalents in CJK (heuristic: short sentence
    # ending in . / ! / 。 within first 30 chars)
    import re as _re
    m = _re.match(r"^[^。!.]{0,40}[。!.]\s*", full_text)
    body = full_text[m.end():] if m else full_text
    body = _re.sub(r"\s+", "", body)
    needle = body[:14]
    if len(needle) < 8:
        return []
    needle_esc = needle.replace("'", "''")
    sql = (f"SELECT phelps, source, source_id, LEFT(text, 200) AS head "
           f"FROM writings WHERE {lang_clause(lang)} AND phelps IS NOT NULL "
           f"AND text LIKE '%{needle_esc}%' LIMIT 8")
    rows = dolt_query(sql)
    # Synthesize "head" + score-able structure for ranking
    return [{"phelps": r.get("phelps", ""), "source": r.get("source", ""),
             "source_id": r.get("source_id", ""), "head": r.get("head", "")} for r in rows]


def search_phelps_for_incipit(lang, incipit_words):
    """Find candidate phelps codes whose EN/lang writings start with any of
    the first ~6 distinctive words of the incipit. Returns list of (phelps, text_head)."""
    if len(incipit_words.split()) < 4:
        return []
    # AND on each of the first ~6 distinctive words (skipping stopwords)
    # — punctuation between words breaks any single substring approach.
    stop = STOPWORDS.get(lang, STOPWORDS["en"])
    words = [w for w in incipit_words.split() if w not in stop][:6]
    if len(words) < 3:
        words = incipit_words.split()[:6]
    if not words:
        return []
    clauses = " AND ".join(f"LOWER(text) LIKE '%{w}%'" for w in (w.replace("'", "''") for w in words))
    sql = (f"SELECT phelps, source, source_id, LEFT(text, 200) AS head "
           f"FROM writings WHERE {lang_clause(lang)} AND phelps IS NOT NULL "
           f"AND {clauses} LIMIT 12")
    return dolt_query(sql)


def main():
    ap = argparse.ArgumentParser()
    ap.add_argument("--lang", required=True)
    ap.add_argument("--category-re", required=True)
    ap.add_argument("--exclude-phelps", default="",
                    help="comma-separated phelps prefixes already mapped (skip)")
    ap.add_argument("--out", required=True)
    args = ap.parse_args()

    review = ROOT / f"bpapp_{args.lang}_review.tsv"
    cat_re = re.compile(args.category_re)
    exclude = set(args.exclude_phelps.split(",")) if args.exclude_phelps else set()

    # Also exclude bpapp_ids already in PBS for this lang+category
    sql = (f"SELECT phelps_code, notes FROM prayer_book_structure "
           f"WHERE source_language='{args.lang}:bpapp' AND notes LIKE '%bpapp_id=%'")
    already = dolt_query(sql)
    already_ids = set()
    for r in already:
        m = re.search(r"bpapp_id=(\d+)", r.get("notes", "") or "")
        if m:
            already_ids.add(m.group(1))

    out = open(args.out, "w")
    out.write(f"-- writings-incipit matches for {args.lang}:bpapp / cat re={args.category_re}\n\n")

    n_searched = 0
    n_hit = 0
    with review.open(encoding="utf-8", errors="replace") as f:
        rdr = csv.DictReader(f, delimiter="\t")
        for r in rdr:
            if not cat_re.search(r["category"]):
                continue
            if r["bpapp_id"] in already_ids:
                continue
            cache = cache_load(args.lang, r["bpapp_id"])
            text = (cache or {}).get("full_text") or r["start_text"]
            incipit = normalize_first_words(text, 10)
            n_searched += 1
            if args.lang in ("ja", "zh", "zh-Hans", "zh-Hant", "ko"):
                cands = search_phelps_cjk(args.lang, text)
                # for CJK, set incipit to first chars for the proximity scorer
                incipit = re.sub(r"\s+", " ", text)[:80].lower()
            elif not incipit:
                continue
            else:
                cands = search_phelps_for_incipit(args.lang, incipit)
            # filter out already-used phelps and excluded prefixes
            # Rank by: how many of the incipit's first-N words appear, in order,
            # within the candidate's first ~400 chars. Strong match = all-in-order
            # near the beginning.
            inc_words = [w for w in incipit.split() if len(w) > 2][:10]
            ranked = []
            for c in cands:
                p = c.get("phelps", "")
                if not p or any(p.startswith(x) for x in exclude):
                    continue
                head = (c.get("head", "") or "").lower()[:500]
                # find positions of each word in head, in order
                pos = 0
                hit = 0
                last = -1
                for w in inc_words:
                    idx = head.find(w, pos)
                    if idx >= 0:
                        hit += 1
                        last = idx
                        pos = idx + len(w)
                score = hit / max(1, len(inc_words))
                # bonus: all words within first 250 chars
                if hit == len(inc_words) and last < 250:
                    score += 0.3
                ranked.append((score, c))
            ranked.sort(key=lambda x: -x[0])
            useful = [c for s, c in ranked if s >= 0.6]
            if not useful:
                out.write(f"-- NO MATCH bpapp_id={r['bpapp_id']}: {incipit[:80]}\n")
                continue
            out.write(f"\n-- bpapp_id={r['bpapp_id']}: {incipit[:80]}\n")
            for s, c in ranked[:5]:
                marker = "*" if s >= 0.9 else ("?" if s < 0.7 else " ")
                out.write(f"--  {marker} score={s:.2f} {c['phelps']} (src={c.get('source','')}/{c.get('source_id','')})\n")
            top = useful[0]
            top_score = ranked[0][0]
            confidence = "HIGH" if top_score >= 0.9 else ("LOW" if top_score < 0.7 else "MED")
            out.write(f"-- SUGGESTED ({confidence}): phelps={top['phelps']} score={top_score:.2f}\n")
            n_hit += 1
    out.close()
    print(f"[{args.lang}] searched={n_searched} hits={n_hit}; review {args.out}")


if __name__ == "__main__":
    main()
