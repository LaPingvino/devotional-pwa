// build_tmp_reference.go — Generate a matching reference for TMP prayers
// with structural properties (length, paragraphs, category) and candidate codes.
//
// Usage: go run build_tmp_reference.go --dolt-dir ~/bahaiwritings --out ~/prayermatching/TMP-matching-reference.md
package main

import (
	"encoding/csv"
	"flag"
	"fmt"
	"io"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
)

var (
	doltDir = flag.String("dolt-dir", filepath.Join(os.Getenv("HOME"), "bahaiwritings"), "Path to dolt repo")
	outFile = flag.String("out", filepath.Join(os.Getenv("HOME"), "prayermatching/TMP-matching-reference.md"), "Output file")
)

func doltQuery(q string) []map[string]string {
	cmd := exec.Command("dolt", "sql", "-q", q, "--result-format", "csv")
	cmd.Dir = *doltDir
	out, err := cmd.Output()
	if err != nil {
		fmt.Fprintf(os.Stderr, "dolt query failed: %v\nQuery: %s\n", err, q)
		return nil
	}
	r := csv.NewReader(strings.NewReader(string(out)))
	r.LazyQuotes = true
	r.FieldsPerRecord = -1
	headers, err := r.Read()
	if err != nil {
		return nil
	}
	var rows []map[string]string
	for {
		record, err := r.Read()
		if err == io.EOF {
			break
		}
		if err != nil {
			continue
		}
		row := map[string]string{}
		for i, h := range headers {
			if i < len(record) {
				row[h] = record[i]
			}
		}
		rows = append(rows, row)
	}
	return rows
}

func countParas(text string) int {
	n := 0
	for _, p := range strings.Split(text, "\n\n") {
		if strings.TrimSpace(p) != "" {
			n++
		}
	}
	return n
}

func firstRealLine(text string) string {
	for _, line := range strings.Split(text, "\n") {
		line = strings.TrimSpace(line)
		if line != "" && !strings.HasPrefix(line, "#") && !strings.HasPrefix(line, "*") && !strings.HasPrefix(line, "(") {
			if len(line) > 80 {
				return line[:80]
			}
			return line
		}
	}
	return ""
}

func extractHeader(text string) string {
	for _, line := range strings.Split(text, "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "##") {
			return strings.TrimSpace(strings.TrimLeft(line, "# "))
		}
	}
	return ""
}

// Category translations: source header → English keyword
var catTranslations = map[string]string{
	// Tuvaluan
	"MOTU KEATEA": "Morning", "AFIAFI": "Evening", "VALUAPO": "Evening",
	"FILEMU": "Peace", "TINO KATOA": "Unity", "FAKAGATA MASAKI": "Healing",
	"FAFINE": "Women", "TALAVOU": "Youth", "TAMALIKI": "Children",
	"TAUSAGA FOOU": "Naw-Rúz", "KAAIGA": "Families", "TOFOOGA": "Tests",
	"FAKAMAGALO": "Forgiveness", "PUIPUIIGA": "Protection",
	"TE TUPE": "Detachment", "TAVAEEGA MO TE FAKAFETAI": "Praise",
	"PALATAISO": "Paradise", "TAVINI MO TE MAE": "Departed",
	"MAUTAKITAKI I TE FEAGAIIGA": "Covenant",
	"GALUEGA": "Teaching", "TALAIIGA": "Teaching",
	"TE ANAPOGI": "Fasting", "TE LAGO MO TE FEASOASOANI": "Aid",
	"TUPU AKA FAK-TE-AGAAGA": "Spiritual",
	"FAKAPILIPILI KI TE ATUA": "Nearness",
	"MATULO MO OLOTOU KAAIGA": "Parents",
	"MANUMAALO O TE FAKATOKAAGA": "Victory",
	"FAKATASITASIIGA": "Steadfastness",
	"FEALOFANI": "Unity", "FALEPUIPUI": "Protection",
	"KAAIGA FAKA-TE-AGAAGA O SEFULU IVA O ASO": "Gatherings",
	// Bau Bidayuh
	"Doa Sipagi Onu": "Morning", "Doa Puasa": "Fasting",
	"Doa Kahwent": "Marriage", "Doa Ngajar": "Teaching",
	"Doa Ganyuk Ulah Rohani": "Spiritual",
	"Doa Piminien": "Steadfastness",
	"Doa Pinulung Daang Pinguji": "Tests",
	"Doa Togap-Totod Daang Wa'adat": "Covenant",
	"Doa Togap Binaan": "Steadfastness",
	"Doa Pibatue Duoh Pinulung": "Aid",
	"Doa Nyinung Nyabal": "Evening",
	"Doa Sa'ant Onak Opot Duoh Bujang Donak": "Children",
	"Doa Sa'ant Manusia": "Humanity",
	"Doa Sa'ant Boli": "Healing",
	"Doa Ngudung/Bitapod/Bigupul": "Gatherings",
	"Doa Pimujul Agama": "Victory",
	"Doa Sa'ant Nya'a Dek Kobos": "Marriage",
	"Doa Sa'ant Pingoma": "Forgiveness",
	"Doa Mudi Duoh Kesyukuran": "Praise",
	"Onu Pingosah (Ayyam-I-Ha)": "Intercalary",
	// Spanish/European
	"Niños": "Children", "Protección": "Protection",
	"Reuniones": "Gatherings", "Jóvenes": "Youth",
	"Mujeres": "Women", "Ayuno": "Fasting",
	"Enseñanza": "Teaching", "Constancia": "Steadfastness",
	"Perdón": "Forgiveness", "Asamblea Espiritual": "Assembl",
	"América": "America",
	"Otras oraciones reveladas por 'Abdu'l-Bahá": "Additional",
	"Otras oraciones reveladas por Bahá'u'lláh": "Additional",
	"Oraciones de 'Abdu'l-Bahá (26)": "Additional",
	// German
	"Schutz": "Protection", "Heilung": "Healing",
	"Standhaftigkeit": "Steadfastness", "Beistand": "Aid",
	"Für die Verstorbenen": "Departed", "Lob und Dank": "Praise",
	"Cercanía a Dios": "Nearness", "Difuntos": "Departed",
	// Kyrgyz
	"Коргоо": "Protection", "БАЙЛАНБАСТЫК": "Detachment",
	"ОКУУНУ ТАРКАТУУ": "Teaching", "НИКЕ": "Marriage",
	"НООРУЗ": "Naw-Rúz", "ОСУЯТКА БЕК БОЛУУ": "Covenant",
	"ТАЙБАСТЫК": "Mysticism", "РУХАНИЙ ЖЫЙЫНДАР": "Gatherings",
	"Жыйындар": "Gatherings", "Адамзат": "Humanity",
	"БИРИМДИК": "Unity", "КУДАЙ ИШИНИН САЛТАНАТЫ": "Victory",
	// Korean - romanized
	// Japanese - uses CJK headers
	// Chinese - uses CJK headers
}

type enPrayer struct {
	phelps    string
	length    int
	paras     int
	firstLine string
}

type candidate struct {
	code      string
	length    int
	paras     int
	firstLine string
	ratio     float64
}

func main() {
	flag.Parse()

	fmt.Fprintln(os.Stderr, "Loading TMPs...")
	tmps := doltQuery(`
		SELECT phelps, language, source_id, text
		FROM writings
		WHERE phelps LIKE 'TMP0%' AND CAST(SUBSTRING(phelps, 4) AS UNSIGNED) >= 975
		ORDER BY language, phelps`)

	// Load original codes from dolt diff
	fmt.Fprintln(os.Stderr, "Loading original codes from diff...")
	diffRows := doltQuery(`
		SELECT from_phelps, to_phelps
		FROM dolt_diff_writings
		WHERE to_phelps LIKE 'TMP0%' AND CAST(SUBSTRING(to_phelps, 4) AS UNSIGNED) >= 975
		AND from_phelps IS NOT NULL AND from_phelps <> ''`)
	originalCode := map[string]string{}
	for _, r := range diffRows {
		originalCode[r["to_phelps"]] = r["from_phelps"]
	}

	// Load PBS position info for original codes
	fmt.Fprintln(os.Stderr, "Loading prayer book positions...")
	pbsRows := doltQuery(`
		SELECT source_language, phelps_code, category_name, order_in_category
		FROM prayer_book_structure
		ORDER BY source_language, category_name, order_in_category`)
	// Map: lang+code -> category+position
	type pbsInfo struct {
		category string
		position int
	}
	pbsMap := map[string][]pbsInfo{}
	for _, r := range pbsRows {
		key := r["source_language"] + "|" + r["phelps_code"]
		pos := 0
		fmt.Sscanf(r["order_in_category"], "%d", &pos)
		pbsMap[key] = append(pbsMap[key], pbsInfo{r["category_name"], pos})
	}

	fmt.Fprintln(os.Stderr, "Loading English categories...")
	catRows := doltQuery(`
		SELECT category_name, GROUP_CONCAT(DISTINCT phelps_code ORDER BY order_in_category) as codes
		FROM prayer_book_structure WHERE source_language='en:bpnet'
		GROUP BY category_name`)
	enCats := map[string][]string{}
	for _, r := range catRows {
		enCats[r["category_name"]] = strings.Split(r["codes"], ",")
	}

	fmt.Fprintln(os.Stderr, "Loading English prayer properties...")
	enRows := doltQuery(`
		SELECT phelps, LENGTH(text) as len, text FROM writings
		WHERE language='en' AND source='bahaiprayers.net'
		AND phelps NOT LIKE 'TMP%'`)
	enProps := map[string]enPrayer{}
	for _, r := range enRows {
		l := 0
		fmt.Sscanf(r["len"], "%d", &l)
		enProps[strings.TrimSpace(r["phelps"])] = enPrayer{
			phelps:    strings.TrimSpace(r["phelps"]),
			length:    l,
			paras:     countParas(r["text"]),
			firstLine: firstRealLine(r["text"]),
		}
	}

	// Load existing lang+phelps combos to flag "already in language"
	fmt.Fprintln(os.Stderr, "Loading existing language assignments...")
	existRows := doltQuery(`
		SELECT DISTINCT language, phelps FROM writings
		WHERE phelps NOT LIKE 'TMP%'`)
	existsInLang := map[string]bool{}
	for _, r := range existRows {
		existsInLang[r["language"]+"|"+r["phelps"]] = true
	}

	fmt.Fprintf(os.Stderr, "Generating reference for %d TMPs...\n", len(tmps))

	var sb strings.Builder
	sb.WriteString("# TMP Prayer Matching Reference\n\n")
	sb.WriteString("Each TMP shows: category (from header), text length, paragraph count,\n")
	sb.WriteString("and top 5 candidate English codes ranked by length similarity.\n\n")
	sb.WriteString("**Match by**: opening phrase correspondence (most reliable), then length ratio.\n")
	sb.WriteString("Category headers narrow the search. Candidates marked [IN LANG] already exist —\n")
	sb.WriteString("if the TMP text differs from existing, the candidate is WRONG for this TMP.\n\n")

	curLang := ""
	for _, tmp := range tmps {
		lang := tmp["language"]
		if lang != curLang {
			curLang = lang
			sb.WriteString(fmt.Sprintf("\n---\n## %s\n\n", curLang))
		}

		text := tmp["text"]
		header := extractHeader(text)
		tlen := len(text)
		paras := countParas(text)
		first := firstRealLine(text)

		// Translate header
		enKeyword := catTranslations[header]
		if enKeyword == "" {
			// Partial match
			headerLower := strings.ToLower(header)
			for k, v := range catTranslations {
				if strings.Contains(strings.ToLower(k), headerLower) || strings.Contains(headerLower, strings.ToLower(k)) {
					enKeyword = v
					break
				}
			}
		}

		// Find candidates — filter by author if we know the original code
		origCode := originalCode[tmp["phelps"]]
		authorPrefix := ""
		if origCode != "" {
			if strings.HasPrefix(origCode, "ABU") {
				authorPrefix = "ABU"
			} else if strings.HasPrefix(origCode, "AB") {
				authorPrefix = "AB"
			} else if strings.HasPrefix(origCode, "BH") {
				authorPrefix = "BH"
			} else if strings.HasPrefix(origCode, "BB") {
				authorPrefix = "BB"
			}
		}
		var cands []candidate
		seen := map[string]bool{}
		// Search primary category + all categories (broader net)
		searchKeywords := []string{}
		if enKeyword != "" {
			searchKeywords = append(searchKeywords, enKeyword)
		}
		// Also search by author across ALL categories (catches cross-category prayers)
		for catName, codes := range enCats {
			matched := false
			for _, kw := range searchKeywords {
				if strings.Contains(strings.ToLower(catName), strings.ToLower(kw)) {
					matched = true
					break
				}
			}
			// For primary category matches OR if no keyword, search all by author+length
			if matched || enKeyword == "" {
				for _, c := range codes {
					c = strings.TrimSpace(c)
					if seen[c] {
						continue
					}
					if authorPrefix != "" && !strings.HasPrefix(c, authorPrefix) {
						continue
					}
					if p, ok := enProps[c]; ok {
						ratio := float64(tlen) / float64(p.length)
						if ratio > 0.3 && ratio < 3.0 {
							cands = append(cands, candidate{c, p.length, p.paras, p.firstLine, ratio})
							seen[c] = true
						}
					}
				}
			}
		}
		sort.Slice(cands, func(i, j int) bool {
			return math.Abs(cands[i].ratio-1.0) < math.Abs(cands[j].ratio-1.0)
		})

		sb.WriteString(fmt.Sprintf("### %s (%s, src:%s)\n", tmp["phelps"], curLang, tmp["source_id"]))
		if origCode != "" {
			// Extract author prefix
			author := "?"
			if strings.HasPrefix(origCode, "BH") {
				author = "Bahá'u'lláh"
			} else if strings.HasPrefix(origCode, "AB") && !strings.HasPrefix(origCode, "ABU") {
				author = "'Abdu'l-Bahá"
			} else if strings.HasPrefix(origCode, "ABU") {
				author = "'Abdu'l-Bahá (utterance)"
			} else if strings.HasPrefix(origCode, "BB") {
				author = "The Báb"
			}
			sb.WriteString(fmt.Sprintf("- **Was**: `%s` (%s) — same tablet, different section\n", origCode, author))
			// Show PBS categories for original code in this language
			key := curLang + "|" + origCode
			if infos, ok := pbsMap[key]; ok {
				cats := []string{}
				for _, info := range infos {
					cats = append(cats, fmt.Sprintf("%s (#%d)", info.category, info.position))
				}
				sb.WriteString(fmt.Sprintf("- **PBS categories**: %s\n", strings.Join(cats, ", ")))
			}
		}
		sb.WriteString(fmt.Sprintf("- **Header**: %s → %s\n", header, enKeyword))
		sb.WriteString(fmt.Sprintf("- **Length**: %d chars, %d paras\n", tlen, paras))
		if len(first) > 80 {
			first = first[:80]
		}
		sb.WriteString(fmt.Sprintf("- **First line**: `%s`\n", first))
		if len(cands) > 0 {
			sb.WriteString("- **Candidates**:\n")
			n := 5
			if len(cands) < n {
				n = len(cands)
			}
			for _, c := range cands[:n] {
				fl := c.firstLine
				if len(fl) > 60 {
					fl = fl[:60]
				}
				inLang := ""
				if existsInLang[curLang+"|"+c.code] {
					inLang = " **[IN LANG]**"
				}
				sb.WriteString(fmt.Sprintf("  - `%s` len=%d p=%d ratio=%.2f%s — %s\n", c.code, c.length, c.paras, c.ratio, inLang, fl))
			}
		} else {
			sb.WriteString(fmt.Sprintf("- **No candidates** (category: '%s')\n", enKeyword))
		}
		sb.WriteString("\n")
	}

	if err := os.WriteFile(*outFile, []byte(sb.String()), 0644); err != nil {
		fmt.Fprintf(os.Stderr, "Error writing: %v\n", err)
		os.Exit(1)
	}
	fmt.Fprintf(os.Stderr, "Written %d TMPs to %s\n", len(tmps), *outFile)
}
