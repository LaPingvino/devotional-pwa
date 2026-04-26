#!/usr/bin/env python3
"""Build TMP prayer matching reference document with structural properties."""
import subprocess, csv, io, os

dolt_dir = os.path.expanduser("~/bahaiwritings")

def dq(q):
    r = subprocess.run(["dolt", "sql", "-q", q, "--result-format", "csv"],
                       capture_output=True, text=True, cwd=dolt_dir)
    return list(csv.DictReader(io.StringIO(r.stdout)))

# Get all new TMPs with full text
tmps = dq("""
SELECT phelps, language, source_id, text
FROM writings
WHERE phelps LIKE 'TMP0%' AND CAST(SUBSTRING(phelps, 4) AS UNSIGNED) >= 975
ORDER BY language, phelps""")

# Get English categories
en_cats = {}
for r in dq("""
SELECT category_name, GROUP_CONCAT(DISTINCT phelps_code ORDER BY order_in_category) as codes
FROM prayer_book_structure WHERE source_language='en:bpnet'
GROUP BY category_name"""):
    en_cats[r['category_name']] = [c.strip() for c in r['codes'].split(',')]

# For each English prayer, get properties
en_props = {}
for r in dq("""
SELECT phelps, LENGTH(text) as len, text FROM writings
WHERE language='en' AND source='bahaiprayers.net'
AND phelps NOT LIKE 'TMP%'"""):
    text = r['text']
    paras = len([p for p in text.split('\n\n') if p.strip()])
    first_real = ''
    for line in text.strip().split('\n'):
        line = line.strip()
        if line and not line.startswith('#') and not line.startswith('*'):
            first_real = line[:80]
            break
    en_props[r['phelps'].strip()] = {
        'len': int(r['len']),
        'paras': paras,
        'first_line': first_real,
    }

# Category translations (header text → English category keyword)
cat_translations = {
    # Tuvaluan
    'MOTU KEATEA': 'Morning', 'AFIAFI': 'Evening', 'VALUAPO': 'Midnight',
    'FILEMU': 'Peace', 'TINO KATOA': 'Unity', 'FAKAGATA MASAKI': 'Healing',
    'FAFINE': 'Women', 'TALAVOU': 'Youth', 'TAMALIKI': 'Children',
    'TAUSAGA FOOU': 'Naw-Rúz', 'KAAIGA': 'Families', 'TOFOOGA': 'Tests',
    'FAKAMAGALO': 'Forgiveness', 'PUIPUIIGA': 'Protection',
    'TE TUPE': 'Detachment', 'TAVAEEGA MO TE FAKAFETAI': 'Praise',
    'PALATAISO': 'Paradise', 'TAVINI MO TE MAE': 'Departed',
    'MAUTAKITAKI I TE FEAGAIIGA': 'Covenant',
    'GALUEGA': 'Teaching', 'TALAIIGA': 'Teaching',
    'TE ANAPOGI': 'Fasting', 'TE LAGO MO TE FEASOASOANI': 'Aid',
    'TUPU AKA FAK-TE-AGAAGA': 'Spiritual',
    'FAKAPILIPILI KI TE ATUA': 'Nearness',
    'MATULO MO OLOTOU KAAIGA': 'Parents',
    'MANUMAALO O TE FAKATOKAAGA': 'Victory',
    'TALO FAKA-PITOA TOETOE': 'Obligatory',
    'FAKATASITASIIGA': 'Steadfastness',
    'LUKUUGA FAKA-TE-AGAAGA': 'Assembly',
    'MATUA FAKATALI O FANAU': 'Children',
    'FEALOFANI': 'Unity', 'FALEPUIPUI': 'Protection',
    'KAAIGA FAKA-TE-AGAAGA O SEFULU IVA O ASO': 'Gatherings',
    # Bau Bidayuh
    'Doa Sipagi Onu': 'Morning', 'Doa Puasa': 'Fasting',
    'Doa Kahwent': 'Marriage', 'Doa Ngajar': 'Teaching',
    'Doa Ganyuk Ulah Rohani': 'Spiritual',
    'Doa Piminien': 'Steadfastness',
    'Doa Pinulung Daang Pinguji': 'Tests',
    "Doa Togap-Totod Daang Wa'adat": 'Covenant',
    'Doa Togap Binaan': 'Steadfastness',
    'Doa Pibatue Duoh Pinulung': 'Aid',
    'Doa Nyinung Nyabal': 'Evening',
    "Doa Sa'ant Onak Opot Duoh Bujang Donak": 'Children',
    "Doa Sa'ant Manusia": 'Humanity',
    "Doa Sa'ant Boli": 'Healing',
    'Doa Ngudung/Bitapod/Bigupul': 'Gatherings',
    'Doa Pimujul Agama': 'Victory',
    "Doa Sa'ant Nya'a Dek Kobos": 'Marriage',
    "Doa Sa'ant Pingoma": 'Forgiveness',
    'Doa Mudi Duoh Kesyukuran': 'Praise',
    'Onu Pingosah (Ayyam-I-Ha)': 'Intercalary',
    # Generic
    'Niños': 'Children', 'Protección': 'Protection',
    'Reuniones': 'Gatherings', 'Jóvenes': 'Youth',
    'Mujeres': 'Women', 'Ayuno': 'Fasting',
    'Enseñanza': 'Teaching', 'Constancia': 'Steadfastness',
    'Perdón': 'Forgiveness', 'Iluminación': 'Enlightenment',
    'Asamblea Espiritual': 'Assembly', 'Geedzeco': 'Marriage',
    'América': 'America', 'Tabla de Aḥmad': 'Tablet of Ahmad',
    'Schutz': 'Protection', 'Heilung': 'Healing',
    'Standhaftigkeit': 'Steadfastness',
    'Für die Verstorbenen': 'Departed',
    'Cercanía a Dios': 'Nearness', 'Difuntos': 'Departed',
    'Lob und Dank': 'Praise', 'Beistand': 'Aid',
}

lines = ["# TMP Prayer Matching Reference", ""]
lines.append("Each TMP prayer below shows: language, category (from text header),")
lines.append("text length, paragraph count, and top 5 candidate English codes")
lines.append("ranked by length similarity within the matching category.")
lines.append("")
lines.append("**How to match**: Compare length ratio (~1.0 is ideal), paragraph count,")
lines.append("and the English first line against what you know about the prayer.")
lines.append("Category headers in the source language narrow the search significantly.")
lines.append("")

cur_lang = None
for tmp in tmps:
    lang = tmp['language']
    if lang != cur_lang:
        cur_lang = lang
        lines.append(f"\n---\n## {cur_lang}\n")

    text = tmp['text']
    # Extract category from ## header
    header = ""
    for line in text.split('\n'):
        line = line.strip()
        if line.startswith('##'):
            header = line.lstrip('# ').strip()
            break

    tlen = len(text)
    paras = len([p for p in text.split('\n\n') if p.strip()])

    # Get first non-header line
    first_real = ''
    for line in text.strip().split('\n'):
        line = line.strip()
        if line and not line.startswith('#') and not line.startswith('*') and not line.startswith('('):
            first_real = line[:80]
            break

    # Find English category keyword
    en_keyword = cat_translations.get(header, '')
    if not en_keyword and header:
        # Try partial match
        for k, v in cat_translations.items():
            if k.lower() in header.lower() or header.lower() in k.lower():
                en_keyword = v
                break

    # Find candidate English codes from matching categories
    candidates = []
    if en_keyword:
        for cat_name, codes in en_cats.items():
            if en_keyword.lower() in cat_name.lower():
                for c in codes:
                    if c in en_props:
                        p = en_props[c]
                        ratio = tlen / p['len'] if p['len'] > 0 else 0
                        if 0.2 < ratio < 5.0:
                            candidates.append((c, p['len'], p['paras'], p['first_line'], ratio))

    candidates.sort(key=lambda x: abs(x[4] - 1.0))

    lines.append(f"### {tmp['phelps']} ({cur_lang}, src:{tmp['source_id']})")
    lines.append(f"- **Header**: {header} → {en_keyword if en_keyword else '?'}")
    lines.append(f"- **Length**: {tlen} chars, {paras} paras")
    lines.append(f"- **First line**: `{first_real}`")
    if candidates:
        lines.append(f"- **Candidates** (len ratio ≈ 1.0 best):")
        for c, clen, cparas, cfirst, ratio in candidates[:5]:
            lines.append(f"  - `{c}` len={clen} p={cparas} ratio={ratio:.2f} — {cfirst[:60]}")
    else:
        lines.append(f"- **No candidates** (category: '{en_keyword}' not matched)")
    lines.append("")

out = os.path.expanduser("~/prayermatching/TMP-matching-reference.md")
with open(out, "w") as f:
    f.write('\n'.join(lines))
print(f"Written {len(tmps)} TMP entries to {out}")
