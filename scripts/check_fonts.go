// check_fonts.go — Detect language codes whose text uses non-Latin scripts
// but are absent from the langScriptFont map in gen_pdf.go.
//
// Usage:
//
//	go run scripts/check_fonts.go --db ./bahaiwritings
//
// For every language in the database it samples up to 500 characters,
// detects which Unicode blocks are present, infers the required script font,
// and reports any mismatch with the known mappings in gen_pdf.go.
package main

import (
	"encoding/csv"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"sort"
	"strings"
)

// knownMappings mirrors langScriptFont from gen_pdf.go.
// Update both files together when adding new scripts.
var knownMappings = map[string]string{
	// Perso-Arabic
	"ar": "NotoNaskhArabic", "fa": "NotoNaskhArabic",
	"ur": "NotoNaskhArabic", "ug": "NotoNaskhArabic",
	"kas": "NotoNaskhArabic", "snd": "NotoNaskhArabic",
	"pus": "NotoNaskhArabic", "ckb": "NotoNaskhArabic",
	"bal": "NotoNaskhArabic", "brh": "NotoNaskhArabic",
	"tuk": "NotoNaskhArabic", "pan-b": "NotoNaskhArabic", "raj-b": "NotoNaskhArabic",
	// CJK
	"zh-Hans": "NotoSerifCJK", "zh-Hant": "NotoSerifCJK",
	"ja": "NotoSerifCJK", "ko": "NotoSerifCJK",
	// Devanagari
	"hi": "NotoSerifDevanagari", "mr": "NotoSerifDevanagari", "ne": "NotoSerifDevanagari",
	"san": "NotoSerifDevanagari",
	"mr-b": "NotoSerifDevanagari",   // mr-c is romanized Latin
	"bho-b": "NotoSerifDevanagari", "bho-d": "NotoSerifDevanagari", // bho-c is romanized
	"gbm": "NotoSerifDevanagari", "gbm-b": "NotoSerifDevanagari",
	"raj": "NotoSerifDevanagari", "raj-c": "NotoSerifDevanagari", "raj-d": "NotoSerifDevanagari",
	"unr": "NotoSerifDevanagari", "unr-b": "NotoSerifDevanagari", "unr-c": "NotoSerifDevanagari",
	"gon": "NotoSerifDevanagari", "kfy": "NotoSerifDevanagari", "kru": "NotoSerifDevanagari",
	"ory-b": "NotoSerifDevanagari",
	// Bengali
	"bn": "NotoSerifBengali",
	"ben": "NotoSerifBengali", "ben-b": "NotoSerifBengali", "ben-c": "NotoSerifBengali",
	// Other Indic
	"ta": "NotoSerifTamil",
	"te": "NotoSerifTelugu", "lmn": "NotoSerifTelugu",
	"ml": "NotoSerifMalayalam",
	"kn": "NotoSerifKannada", "tcy": "NotoSerifKannada",
	"gu": "NotoSerifGujarati", "dng": "NotoSerifGujarati",
	"pa": "NotoSerifGurmukhi", "pan": "NotoSerifGurmukhi", // pan-b is Arabic
	"si": "NotoSerifSinhala", "sin": "NotoSerifSinhala",
	"ory": "NotoSerifOriya", "kxu": "NotoSerifOriya", // ory-b uses Devanagari
	// Southeast Asian
	"th": "NotoSerifThai",
	"lo": "NotoSerifLao",
	"km": "NotoSerifKhmer",
	"my": "NotoSerifMyanmar",
	// Other non-Latin
	"he": "NotoSerifHebrew",
	"am": "NotoSerifEthiopic", "ti": "NotoSerifEthiopic",
	"hy": "NotoSerifArmenian",
	"bod": "NotoSerifTibetan",
	"dv": "NotoSansThaana", // dv-b is romanized Latin
}

// scriptDetect maps a Unicode range to a human-readable script name.
type scriptRange struct {
	lo, hi rune
	name   string
	font   string // recommended font family
}

var scriptRanges = []scriptRange{
	{0x0600, 0x06FF, "Arabic", "NotoNaskhArabic"},
	{0x0750, 0x077F, "Arabic Supplement", "NotoNaskhArabic"},
	{0xFB50, 0xFDFF, "Arabic Presentation Forms-A", "NotoNaskhArabic"},
	{0xFE70, 0xFEFF, "Arabic Presentation Forms-B", "NotoNaskhArabic"},
	{0x0900, 0x097F, "Devanagari", "NotoSerifDevanagari"},
	{0x0980, 0x09FF, "Bengali", "NotoSerifBengali"},
	{0x0A00, 0x0A7F, "Gurmukhi", "NotoSerifGurmukhi"},
	{0x0A80, 0x0AFF, "Gujarati", "NotoSerifGujarati"},
	{0x0B00, 0x0B7F, "Oriya", "NotoSerifOriya"},
	{0x0B80, 0x0BFF, "Tamil", "NotoSerifTamil"},
	{0x0C00, 0x0C7F, "Telugu", "NotoSerifTelugu"},
	{0x0C80, 0x0CFF, "Kannada", "NotoSerifKannada"},
	{0x0D00, 0x0D7F, "Malayalam", "NotoSerifMalayalam"},
	{0x0D80, 0x0DFF, "Sinhala", "NotoSerifSinhala"},
	{0x0E00, 0x0E7F, "Thai", "NotoSerifThai"},
	{0x0E80, 0x0EFF, "Lao", "NotoSerifLao"},
	{0x0F00, 0x0FFF, "Tibetan", "NotoSerifTibetan"},
	{0x1000, 0x109F, "Myanmar", "NotoSerifMyanmar"},
	{0x10A0, 0x10FF, "Georgian", "NotoSerifGeorgian"},
	{0x1100, 0x11FF, "Hangul Jamo", "NotoSerifCJK"},
	{0x1200, 0x137F, "Ethiopic", "NotoSerifEthiopic"},
	{0x0530, 0x058F, "Armenian", "NotoSerifArmenian"},
	{0x0590, 0x05FF, "Hebrew", "NotoSerifHebrew"},
	{0x0780, 0x07BF, "Thaana", "NotoSansThaana"},
	{0x1780, 0x17FF, "Khmer", "NotoSerifKhmer"},
	{0x1C50, 0x1C7F, "Ol Chiki", "NotoSansOlChiki"},
	{0x3000, 0x9FFF, "CJK", "NotoSerifCJK"},
	{0xAC00, 0xD7AF, "Hangul Syllables", "NotoSerifCJK"},
	{0x20000, 0x2A6DF, "CJK Extension B", "NotoSerifCJK"},
}

func detectScript(text string) (script, font string) {
	counts := map[string]int{}
	fonts := map[string]string{}
	for _, r := range text {
		if r < 0x0250 { // Latin/common — skip
			continue
		}
		for _, sr := range scriptRanges {
			if r >= sr.lo && r <= sr.hi {
				counts[sr.name]++
				fonts[sr.name] = sr.font
				break
			}
		}
	}
	if len(counts) == 0 {
		return "", ""
	}
	// Pick the script with the most characters
	best, bestN := "", 0
	for s, n := range counts {
		if n > bestN {
			best, bestN = s, n
		}
	}
	return best, fonts[best]
}

func doltCSV(dbPath, sql string) [][]string {
	cmd := exec.Command("dolt", "sql", "-q", sql, "--result-format", "csv")
	cmd.Dir = dbPath
	out, err := cmd.Output()
	if err != nil {
		fmt.Fprintf(os.Stderr, "dolt error: %v\n", err)
		os.Exit(1)
	}
	r := csv.NewReader(strings.NewReader(string(out)))
	r.LazyQuotes = true
	rows, _ := r.ReadAll()
	if len(rows) < 2 {
		return nil
	}
	return rows[1:]
}

func main() {
	db := flag.String("db", "", "Path to dolt repo (required)")
	flag.Parse()
	if *db == "" {
		fmt.Fprintln(os.Stderr, "Usage: go run check_fonts.go --db ./bahaiwritings")
		os.Exit(1)
	}

	// Get all distinct language codes
	langs := doltCSV(*db, "SELECT DISTINCT language FROM writings ORDER BY language")

	type result struct {
		lang    string
		script  string
		font    string
		mapped  string
		problem string
	}
	var problems []result
	var ok []result

	// Scripts we know about but don't yet have fonts for — suppress as known gaps.
	knownUnsupported := map[string]string{
		"nyo": "false positive: Latin prayer with Ethiopic chars in appended language label",
	}

	for _, row := range langs {
		if len(row) == 0 {
			continue
		}
		lang := row[0]

		if reason, skip := knownUnsupported[lang]; skip {
			fmt.Printf("  SKIP %s: %s\n", lang, reason)
			continue
		}

		// Sample up to 2000 chars from actual prayer text (skip header lines)
		rows := doltCSV(*db, fmt.Sprintf(
			"SELECT SUBSTRING(text,1,500) FROM writings WHERE language='%s' LIMIT 5",
			strings.ReplaceAll(lang, "'", "''")))
		var sample strings.Builder
		for _, r := range rows {
			if len(r) > 0 {
				sample.WriteString(r[0])
			}
		}

		script, detectedFont := detectScript(sample.String())
		mapped := knownMappings[lang] // "" means falls back to NotoSerif (Latin)

		var problem string
		if script == "" {
			// Latin-only — check it's NOT mapped to a non-Latin font
			if mapped != "" {
				problem = fmt.Sprintf("WRONG: mapped to %s but text appears Latin-only", mapped)
			}
		} else {
			// Non-Latin script detected
			if mapped == "" {
				problem = fmt.Sprintf("MISSING: detected %s (%s) but no font mapping", script, detectedFont)
			} else if mapped != detectedFont {
				// Allow some mismatches that are intentional (e.g. Arabic variants)
				arabicFonts := map[string]bool{"NotoNaskhArabic": true}
				if !(arabicFonts[mapped] && arabicFonts[detectedFont]) {
					problem = fmt.Sprintf("MISMATCH: mapped to %s but detected %s (%s)", mapped, script, detectedFont)
				}
			}
		}

		r := result{lang, script, detectedFont, mapped, problem}
		if problem != "" {
			problems = append(problems, r)
		} else {
			ok = append(ok, r)
		}
	}

	sort.Slice(problems, func(i, j int) bool { return problems[i].lang < problems[j].lang })

	if len(problems) == 0 {
		fmt.Printf("✓ All %d languages have correct font mappings.\n", len(ok))
		return
	}

	fmt.Printf("Font mapping issues found (%d of %d languages):\n\n", len(problems), len(ok)+len(problems))
	fmt.Printf("%-12s %-25s %s\n", "LANG", "DETECTED SCRIPT", "PROBLEM")
	fmt.Println(strings.Repeat("-", 80))
	for _, p := range problems {
		fmt.Printf("%-12s %-25s %s\n", p.lang, p.script, p.problem)
	}
	fmt.Printf("\n%d OK, %d need attention\n", len(ok), len(problems))
	os.Exit(1)
}
