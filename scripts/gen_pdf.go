// gen_pdf.go — Generate prayer book PDFs and EPUBs from the local Dolt database
//
// Usage:
//   go run gen_pdf.go [flags]
//   go run gen_pdf.go --lang en --output prayers_en.pdf
//   go run gen_pdf.go --lang ar --output prayers_ar.pdf
//   go run gen_pdf.go --lang en --epub --output prayers_en.epub
//   go run gen_pdf.go --lang all --out-dir ./static/downloads
//   go run gen_pdf.go --html-only --lang fr --output prayers_fr.html
//   go run gen_pdf.go --index --output index.pdf     # first-lines concordance
//
// Flags:
//   --db       Path to dolt repo       (default: ~/prayermatching/bahaiwritings)
//   --lang     Language code           (default: en; use "all" for all languages)
//   --source   Prayer source           (default: bahaiprayers.net)
//   --output   Output file             (default: prayers_LANG.pdf / .epub)
//   --out-dir  Output directory        (default: current dir; used with --lang all)
//   --font-dir Directory with Noto .ttf fonts  (default: fonts/ relative to script)
//   --html-only  Output HTML only (for EPUB via pandoc)
//   --epub     Generate EPUB via pandoc
//   --both     Generate both PDF and EPUB
//   --index    Generate first-lines concordance index
//   --title    Document title          (default: "Bahá'í Prayers")
//   --phelps-base-url  Base URL for phelps inventory links

package main

import (
	"archive/zip"
	"encoding/csv"
	"flag"
	"fmt"
	"html/template"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/phpdave11/gofpdf"
)

// ── RTL language set ───────────────────────────────────────────────────────────

var rtlLangs = map[string]bool{
	"ar": true, "fa": true, "ur": true, "he": true,
	"ug": true, // Uyghur
}

// ── Data types ─────────────────────────────────────────────────────────────────

type Prayer struct {
	Phelps        string
	Text          string
	Name          string
	Language      string
	Category      string
	CategoryOrder int
	OrderInCat    int
}

// ── Dolt query helper ──────────────────────────────────────────────────────────

func doltCSV(dbPath, sql string) [][]string {
	cmd := exec.Command("dolt", "sql", "-q", sql, "--result-format", "csv")
	cmd.Dir = dbPath
	out, err := cmd.Output()
	if err != nil {
		fmt.Fprintf(os.Stderr, "dolt error: %v\n%s\n", err, string(out))
		os.Exit(1)
	}
	r := csv.NewReader(strings.NewReader(string(out)))
	r.LazyQuotes = true
	rows, err := r.ReadAll()
	if err != nil {
		fmt.Fprintf(os.Stderr, "csv parse: %v\n", err)
		os.Exit(1)
	}
	if len(rows) < 1 {
		return nil
	}
	return rows[1:] // skip header
}

// ── Text sanitization ──────────────────────────────────────────────────────────

// sanitizeText strips control characters and Private Use Area codepoints that
// cause rendering warnings in PDF generators.
func sanitizeText(s string) string {
	return strings.Map(func(r rune) rune {
		if r < 0x20 && r != '\t' && r != '\n' && r != '\r' {
			return -1
		}
		if r == 0x7F || (r >= 0x80 && r <= 0x9F) {
			return -1
		}
		if r >= 0xF000 && r <= 0xF8FF { // PUA (Wingdings, Zapf Dingbats, etc.)
			return -1
		}
		return r
	}, s)
}

// ── Embedded Arabic shaper ─────────────────────────────────────────────────────
//
// Inline implementation of Arabic contextual letter-form substitution.
// For the full, well-commented version with UBA Bidi support, see:
//   ~/prayermatching/arabicshaper/arabicshaper.go
//
// This simplified version uses Unicode block ranges for RTL detection instead
// of golang.org/x/text/unicode/bidi, so it adds no external dependency here.

type arabicForms [4]rune // [isolated, final, initial, medial]

var arabicFormTable = map[rune]arabicForms{
	// Right-joining (isolated + final only)
	0x0621: {0xFE80, 0, 0, 0},
	0x0622: {0xFE81, 0xFE82, 0, 0},
	0x0623: {0xFE83, 0xFE84, 0, 0},
	0x0624: {0xFE85, 0xFE86, 0, 0},
	0x0625: {0xFE87, 0xFE88, 0, 0},
	0x0627: {0xFE8D, 0xFE8E, 0, 0},
	0x0629: {0xFE93, 0xFE94, 0, 0},
	0x062F: {0xFEA9, 0xFEAA, 0, 0},
	0x0630: {0xFEAB, 0xFEAC, 0, 0},
	0x0631: {0xFEAD, 0xFEAE, 0, 0},
	0x0632: {0xFEAF, 0xFEB0, 0, 0},
	0x0648: {0xFEED, 0xFEEE, 0, 0},
	0x0649: {0xFEEF, 0xFEF0, 0, 0},
	// Dual-joining (all four forms)
	0x0626: {0xFE89, 0xFE8A, 0xFE8B, 0xFE8C},
	0x0628: {0xFE8F, 0xFE90, 0xFE91, 0xFE92},
	0x062A: {0xFE95, 0xFE96, 0xFE97, 0xFE98},
	0x062B: {0xFE99, 0xFE9A, 0xFE9B, 0xFE9C},
	0x062C: {0xFE9D, 0xFE9E, 0xFE9F, 0xFEA0},
	0x062D: {0xFEA1, 0xFEA2, 0xFEA3, 0xFEA4},
	0x062E: {0xFEA5, 0xFEA6, 0xFEA7, 0xFEA8},
	0x0633: {0xFEB1, 0xFEB2, 0xFEB3, 0xFEB4},
	0x0634: {0xFEB5, 0xFEB6, 0xFEB7, 0xFEB8},
	0x0635: {0xFEB9, 0xFEBA, 0xFEBB, 0xFEBC},
	0x0636: {0xFEBD, 0xFEBE, 0xFEBF, 0xFEC0},
	0x0637: {0xFEC1, 0xFEC2, 0xFEC3, 0xFEC4},
	0x0638: {0xFEC5, 0xFEC6, 0xFEC7, 0xFEC8},
	0x0639: {0xFEC9, 0xFECA, 0xFECB, 0xFECC},
	0x063A: {0xFECD, 0xFECE, 0xFECF, 0xFED0},
	0x0641: {0xFED1, 0xFED2, 0xFED3, 0xFED4},
	0x0642: {0xFED5, 0xFED6, 0xFED7, 0xFED8},
	0x0643: {0xFED9, 0xFEDA, 0xFEDB, 0xFEDC},
	0x0644: {0xFEDD, 0xFEDE, 0xFEDF, 0xFEE0},
	0x0645: {0xFEE1, 0xFEE2, 0xFEE3, 0xFEE4},
	0x0646: {0xFEE5, 0xFEE6, 0xFEE7, 0xFEE8},
	0x0647: {0xFEE9, 0xFEEA, 0xFEEB, 0xFEEC},
	0x064A: {0xFEF1, 0xFEF2, 0xFEF3, 0xFEF4},
	0x0640: {0x0640, 0x0640, 0x0640, 0x0640}, // Tatweel (transparent)
	// Persian/Urdu extensions
	0x067E: {0xFB56, 0xFB57, 0xFB58, 0xFB59},
	0x0686: {0xFB7A, 0xFB7B, 0xFB7C, 0xFB7D},
	0x0698: {0xFB8A, 0xFB8B, 0, 0},
	0x06A9: {0xFB8E, 0xFB8F, 0xFB90, 0xFB91},
	0x06AF: {0xFB92, 0xFB93, 0xFB94, 0xFB95},
	0x06CC: {0xFBFC, 0xFBFD, 0xFBFE, 0xFBFF},
	0x06C1: {0xFBA6, 0xFBA7, 0xFBA8, 0xFBA9},
	0x06D2: {0xFBAE, 0xFBAF, 0, 0},
	0x0679: {0xFB66, 0xFB67, 0xFB68, 0xFB69},
	0x0688: {0xFB88, 0xFB89, 0, 0},
	0x0691: {0xFB8C, 0xFB8D, 0, 0},
}

var lamAlifTable = map[rune][2]rune{
	0x0622: {0xFEF5, 0xFEF6},
	0x0623: {0xFEF7, 0xFEF8},
	0x0625: {0xFEF9, 0xFEFA},
	0x0627: {0xFEFB, 0xFEFC},
}

type joiningType int

const (
	joinNone        joiningType = iota
	joinRight
	joinDual
	joinTransparent
)

func arabicJoining(r rune) joiningType {
	if r >= 0x064B && r <= 0x065F {
		return joinTransparent
	}
	if r == 0x0670 || r == 0x0640 {
		return joinTransparent
	}
	forms, ok := arabicFormTable[r]
	if !ok {
		return joinNone
	}
	if forms[2] == 0 && forms[3] == 0 {
		return joinRight
	}
	return joinDual
}

func prevJoining(runes []rune, i int) joiningType {
	for j := i - 1; j >= 0; j-- {
		if jt := arabicJoining(runes[j]); jt != joinTransparent {
			return jt
		}
	}
	return joinNone
}

func nextJoining(runes []rune, i int) joiningType {
	for j := i + 1; j < len(runes); j++ {
		if jt := arabicJoining(runes[j]); jt != joinTransparent {
			return jt
		}
	}
	return joinNone
}

func shapeArabicRun(s string) string {
	runes := []rune(s)
	out := make([]rune, 0, len(runes))
	for i := 0; i < len(runes); i++ {
		r := runes[i]
		forms, ok := arabicFormTable[r]
		if !ok {
			out = append(out, r)
			continue
		}
		if r == 0x0644 {
			var transparents []rune
			j := i + 1
			for j < len(runes) && arabicJoining(runes[j]) == joinTransparent {
				transparents = append(transparents, runes[j])
				j++
			}
			if j < len(runes) {
				if lamAlef, isAlef := lamAlifTable[runes[j]]; isAlef {
					pj := prevJoining(runes, i)
					var lig rune
					if pj == joinDual || pj == joinRight {
						lig = lamAlef[1]
					} else {
						lig = lamAlef[0]
					}
					out = append(out, lig)
					out = append(out, transparents...)
					i = j
					continue
				}
			}
		}
		jt := arabicJoining(r)
		pj := prevJoining(runes, i)
		nj := nextJoining(runes, i)
		connectsRight := pj == joinDual || pj == joinRight
		connectsLeft := (jt == joinDual) && (nj == joinDual || nj == joinRight)
		var formIdx int
		switch {
		case connectsLeft && connectsRight:
			formIdx = 3
		case connectsLeft:
			formIdx = 2
		case connectsRight:
			formIdx = 1
		default:
			formIdx = 0
		}
		chosen := forms[formIdx]
		if chosen == 0 {
			if formIdx == 3 && forms[1] != 0 {
				chosen = forms[1]
			} else {
				chosen = forms[0]
			}
		}
		out = append(out, chosen)
	}
	return string(out)
}

func reverseRTLRun(s string) string {
	runes := []rune(s)
	type cluster []rune
	var clusters []cluster
	for i := 0; i < len(runes); {
		cl := cluster{runes[i]}
		i++
		for i < len(runes) && unicode.Is(unicode.M, runes[i]) {
			cl = append(cl, runes[i])
			i++
		}
		clusters = append(clusters, cl)
	}
	for l, r := 0, len(clusters)-1; l < r; l, r = l+1, r-1 {
		clusters[l], clusters[r] = clusters[r], clusters[l]
	}
	var b strings.Builder
	for _, cl := range clusters {
		for _, r := range cl {
			b.WriteRune(r)
		}
	}
	return b.String()
}

// isRTLChar returns true if rune r is in an RTL Unicode block.
func isRTLChar(r rune) bool {
	return (r >= 0x0590 && r <= 0x06FF) || // Hebrew + Arabic
		(r >= 0x0750 && r <= 0x077F) || // Arabic Supplement
		(r >= 0x08A0 && r <= 0x08FF) || // Arabic Extended-A
		(r >= 0xFB50 && r <= 0xFDFF) || // Arabic Presentation Forms-A
		(r >= 0xFE70 && r <= 0xFEFF) // Arabic Presentation Forms-B
}

// shapeText shapes and reverses a full string for RTL rendering.
// Each line is processed: RTL runs are shaped and reversed, LTR runs kept as-is.
func shapeText(s string) string {
	if !strings.ContainsRune(s, 0) { // quick sanity
		_ = utf8.ValidString(s)
	}
	var lines []string
	for _, line := range strings.Split(s, "\n") {
		lines = append(lines, shapeTextLine(line))
	}
	return strings.Join(lines, "\n")
}

func shapeTextLine(line string) string {
	if line == "" {
		return line
	}
	runes := []rune(line)
	// Classify each rune
	const (
		clsRTL     = 'R'
		clsLTR     = 'L'
		clsNeutral = 'N'
	)
	class := make([]byte, len(runes))
	for i, r := range runes {
		if isRTLChar(r) {
			class[i] = clsRTL
		} else if r == ' ' || r == '\t' || (r >= '0' && r <= '9') {
			class[i] = clsNeutral
		} else {
			class[i] = clsLTR
		}
	}
	// Detect paragraph direction from first strong char
	paraDir := byte(clsLTR)
	for _, c := range class {
		if c == clsRTL {
			paraDir = clsRTL
			break
		}
		if c == clsLTR {
			break
		}
	}
	// Resolve neutrals
	last := paraDir
	for i, c := range class {
		if c == clsNeutral {
			class[i] = last
		} else {
			last = c
		}
	}
	// Build runs
	type run struct {
		runes []rune
		rtl   bool
	}
	var runs []run
	if len(runes) > 0 {
		cur := run{rtl: class[0] == clsRTL}
		for i, r := range runes {
			isRTL := class[i] == clsRTL
			if isRTL != cur.rtl {
				runs = append(runs, cur)
				cur = run{rtl: isRTL}
			}
			cur.runes = append(cur.runes, r)
		}
		runs = append(runs, cur)
	}
	// Shape RTL runs
	for i := range runs {
		if runs[i].rtl {
			t := shapeArabicRun(string(runs[i].runes))
			runs[i].runes = []rune(reverseRTLRun(t))
		}
	}
	// Concatenate in visual order (RTL paragraph: reverse run order)
	var b strings.Builder
	if paraDir == clsRTL {
		for i := len(runs) - 1; i >= 0; i-- {
			b.WriteString(string(runs[i].runes))
		}
	} else {
		for _, r := range runs {
			b.WriteString(string(r.runes))
		}
	}
	return b.String()
}

// ── Font loading ───────────────────────────────────────────────────────────────

// fontInfo tracks which fonts have been loaded into a gofpdf instance.
type fontInfo struct {
	bodyFont string
	monoFont string
	loaded   map[string]bool
	langFont map[string]string // lang code -> preferred font family for this lang
}

// langScriptFont maps language codes to their required Noto font family names.
// Languages absent from this map fall back to NotoSerif (covers Latin/Cyrillic/Greek).
var langScriptFont = map[string]string{
	// Perso-Arabic
	"ar": "NotoNaskhArabic", "fa": "NotoNaskhArabic",
	"ur": "NotoNaskhArabic", "ug": "NotoNaskhArabic", "dih": "NotoNaskhArabic",
	// CJK (one font covers all CJK variants)
	"zh-Hans": "NotoSerifCJK", "zh-Hant": "NotoSerifCJK",
	"ja": "NotoSerifCJK", "ko": "NotoSerifCJK",
	// Devanagari
	"hi": "NotoSerifDevanagari", "mr": "NotoSerifDevanagari", "ne": "NotoSerifDevanagari",
	// Other Indic
	"bn": "NotoSerifBengali",
	"ta": "NotoSerifTamil",
	"te": "NotoSerifTelugu",
	"ml": "NotoSerifMalayalam",
	"kn": "NotoSerifKannada",
	"gu": "NotoSerifGujarati",
	"pa": "NotoSerifGurmukhi",
	"si": "NotoSerifSinhala",
	// Southeast Asian
	"th": "NotoSerifThai",
	"lo": "NotoSerifLao",
	"km": "NotoSerifKhmer",
	"my": "NotoSerifMyanmar",
	// Other non-Latin
	"he": "NotoSerifHebrew",
	"am": "NotoSerifEthiopic", "ti": "NotoSerifEthiopic",
}

// scriptFontFile maps a font family name to its TTF/OTF filename.
var scriptFontFile = map[string]string{
	"NotoNaskhArabic":    "NotoNaskhArabic-Regular.ttf",
	"NotoSerifCJK":       "NotoSerifCJKsc-VF.ttf",
	"NotoSerifDevanagari": "NotoSerifDevanagari-Regular.ttf",
	"NotoSerifBengali":   "NotoSerifBengali-Regular.ttf",
	"NotoSerifTamil":     "NotoSerifTamil-Regular.ttf",
	"NotoSerifTelugu":    "NotoSerifTelugu-Regular.ttf",
	"NotoSerifMalayalam": "NotoSerifMalayalam-Regular.ttf",
	"NotoSerifKannada":   "NotoSerifKannada-Regular.ttf",
	"NotoSerifGujarati":  "NotoSerifGujarati-Regular.ttf",
	"NotoSerifGurmukhi":  "NotoSerifGurmukhi-Regular.ttf",
	"NotoSerifSinhala":   "NotoSerifSinhala-Regular.ttf",
	"NotoSerifThai":      "NotoSerifThai-Regular.ttf",
	"NotoSerifLao":       "NotoSerifLao-Regular.ttf",
	"NotoSerifKhmer":     "NotoSerifKhmer-Regular.ttf",
	"NotoSerifMyanmar":   "NotoSerifMyanmar-Regular.ttf",
	"NotoSerifHebrew":    "NotoSerifHebrew-Regular.ttf",
	"NotoSerifEthiopic":  "NotoSerifEthiopic-Regular.ttf",
}

// collectRunes returns the set of all Unicode codepoints used in the given
// prayers (text, name, and category fields).  Used to drive font subsetting.
// If isRTL is true, texts are shaped first so that Arabic Presentation Forms
// (U+FExx/FBxx) are included instead of the base Arabic codepoints — the
// renderer outputs shaped forms and the subset font must contain them.
func collectRunes(prayers []Prayer, isRTL bool) map[rune]bool {
	runes := map[rune]bool{}
	shape := func(s string) string {
		if isRTL {
			return shapeText(s)
		}
		return s
	}
	for _, p := range prayers {
		for _, r := range shape(p.Text) {
			runes[r] = true
		}
		for _, r := range shape(p.Name) {
			runes[r] = true
		}
		for _, r := range shape(p.Category) {
			runes[r] = true
		}
	}
	// Always include printable ASCII so metadata text (Phelps codes, "Also in:",
	// language lists) renders correctly even when the primary script font is
	// subsetted to a non-Latin script (e.g. NotoNaskhArabic for Arabic PDFs).
	for r := rune(0x20); r <= rune(0x7E); r++ {
		runes[r] = true
	}
	return runes
}

// mergeRunes merges multiple rune sets into one.
func mergeRunes(sets ...map[rune]bool) map[rune]bool {
	out := map[rune]bool{}
	for _, s := range sets {
		for r := range s {
			out[r] = true
		}
	}
	return out
}

// subsetTTF uses pyftsubset (from Python fonttools) to create a minimal TTF
// containing only the codepoints in runes.  Returns the path to the subsetted
// file (in os.TempDir()) on success, or "" if pyftsubset is unavailable or
// fails (callers fall back to the full font in that case).
//
// All fonts used should already be in TrueType (glyf) format; CFF-based OTF
// fonts are not supported by gofpdf.  CJK uses NotoSerifCJKsc-VF.ttf
// (variable TrueType) whose glyf table is read directly by gofpdf.
func subsetTTF(inputPath string, runes map[rune]bool) string {
	if _, err := exec.LookPath("pyftsubset"); err != nil {
		return ""
	}
	if len(runes) == 0 {
		return ""
	}
	// Build a comma-separated hex unicode list.
	codes := make([]string, 0, len(runes))
	for r := range runes {
		if r > 0 {
			codes = append(codes, fmt.Sprintf("%04X", r))
		}
	}
	sort.Strings(codes)

	outPath := filepath.Join(os.TempDir(), "prayerbook_subset_"+filepath.Base(inputPath))
	cmd := exec.Command("pyftsubset", inputPath,
		"--unicodes="+strings.Join(codes, ","),
		"--output-file="+outPath,
		"--layout-features=*", // preserve GSUB/GPOS (ligatures, marks, etc.)
	)
	if out, err := cmd.CombinedOutput(); err != nil {
		fmt.Fprintf(os.Stderr, "  pyftsubset %s: %v\n%s\n", filepath.Base(inputPath), err, out)
		return ""
	}
	orig, _ := os.Stat(inputPath)
	sub, _ := os.Stat(outPath)
	if orig != nil && sub != nil {
		fmt.Fprintf(os.Stderr, "  subset %s: %dKB → %dKB\n",
			filepath.Base(inputPath), orig.Size()/1024, sub.Size()/1024)
	}

	return outPath
}

// loadFonts finds and registers the appropriate Unicode fonts for the given
// language.  Returns the font family name to use for body text.
//
// Font search order:
//  1. --font-dir flag value
//  2. fonts/ relative to the current working directory
//  3. ~/.local/share/fonts/noto/
//  4. /usr/share/fonts/truetype/noto/
//  5. Built-in Helvetica (ASCII fallback, no external file needed)
//
// If runes is non-nil, fonts are subsetted via pyftsubset before embedding.
// We use AddUTF8FontFromBytes (reads the file ourselves) rather than AddUTF8Font
// to avoid gofpdf's internal font-directory path prepending.
func loadFonts(pdf *gofpdf.Fpdf, lang, fontDir string, runes map[rune]bool) *fontInfo {
	fi := &fontInfo{loaded: map[string]bool{}, langFont: map[string]string{}}

	home, _ := os.UserHomeDir()
	searchDirs := []string{fontDir, "fonts"}
	if home != "" {
		searchDirs = append(searchDirs,
			filepath.Join(home, ".local/share/fonts/noto"),
			filepath.Join(home, ".local/share/fonts"),
		)
	}
	searchDirs = append(searchDirs,
		"/usr/share/fonts/truetype/noto",
		"/usr/share/fonts/opentype/noto",
		"/usr/local/share/fonts",
	)

	findFont := func(filename string) string {
		for _, dir := range searchDirs {
			if dir == "" {
				continue
			}
			path := filepath.Join(dir, filename)
			if _, err := os.Stat(path); err == nil {
				return path
			}
		}
		return ""
	}

	// Read the font file and register with gofpdf via bytes (avoids internal path mangling).
	// If runes is non-nil, pyftsubset is attempted to reduce the embedded font size.
	loadTTF := func(family, filename string) bool {
		if fi.loaded[family] {
			return true
		}
		path := findFont(filename)
		if path == "" {
			return false
		}
		// Try subsetting first; fall back to full font if unavailable.
		if subPath := subsetTTF(path, runes); subPath != "" {
			path = subPath
		}
		data, err := os.ReadFile(path)
		if err != nil {
			fmt.Fprintf(os.Stderr, "  font read error %s: %v\n", path, err)
			return false
		}
		pdf.AddUTF8FontFromBytes(family, "", data)
		fi.loaded[family] = true
		fmt.Fprintf(os.Stderr, "  font: %s ← %s\n", family, filepath.Base(path))
		return true
	}

	// Language → preferred font
	if wantFamily, ok := langScriptFont[lang]; ok {
		if wantFile, fok := scriptFontFile[wantFamily]; fok {
			if loadTTF(wantFamily, wantFile) {
				fi.bodyFont = wantFamily
				fi.langFont[lang] = wantFamily
			}
		}
	}

	// Default body font: NotoSerif covers Latin, Cyrillic, Greek, Vietnamese, etc.
	if fi.bodyFont == "" {
		if loadTTF("NotoSerif", "NotoSerif-Regular.ttf") {
			fi.bodyFont = "NotoSerif"
		} else {
			fi.bodyFont = "Helvetica" // built-in, ASCII only
		}
	}

	fi.monoFont = "Courier" // built-in monospace for phelps codes
	return fi
}

// ── PDF segment types ──────────────────────────────────────────────────────────

type segStyle int

const (
	segNormal    segStyle = iota
	segVerse              // indented, italic
	segNote               // smaller, grey
	segHeader             // larger, italic heading within a prayer
	segSubheader          // small italic heading
)

type pdfSeg struct {
	text  string
	style segStyle
}

// markdownToSegs converts the prayer markdown format to a list of PDF segments.
// For RTL languages, each text segment is pre-shaped and reversed here.
func markdownToSegs(text string, isRTL bool) []pdfSeg {
	lines := strings.Split(sanitizeText(text), "\n")
	var segs []pdfSeg
	var pending []string

	shape := func(s string) string {
		if isRTL {
			return shapeText(s)
		}
		return s
	}

	flushPending := func() {
		if len(pending) > 0 {
			combined := strings.Join(pending, " ")
			pending = nil
			segs = append(segs, pdfSeg{text: shape(combined), style: segNormal})
		}
	}

	for _, line := range lines {
		t := strings.TrimSpace(line)
		switch {
		case strings.HasPrefix(t, "## "):
			flushPending()
			segs = append(segs, pdfSeg{text: shape(strings.TrimPrefix(t, "## ")), style: segSubheader})
		case strings.HasPrefix(t, "# "):
			flushPending()
			segs = append(segs, pdfSeg{text: shape(strings.TrimPrefix(t, "# ")), style: segHeader})
		case strings.HasPrefix(t, "* "):
			flushPending()
			segs = append(segs, pdfSeg{text: shape(strings.TrimPrefix(t, "* ")), style: segVerse})
		case strings.HasPrefix(t, "! "):
			flushPending()
			segs = append(segs, pdfSeg{text: strings.TrimPrefix(t, "! "), style: segNote})
		case t == "":
			flushPending()
		default:
			pending = append(pending, t)
		}
	}
	flushPending()
	return segs
}

// ── gofpdf PDF renderer ────────────────────────────────────────────────────────

// pdfCtx holds shared rendering state passed between helpers.
type pdfCtx struct {
	pdf      *gofpdf.Fpdf
	fi       *fontInfo
	contentW float64
	lm       float64 // left margin
}

// bodyColor / metaColor / headColor / noteColor set PDF text colour.
func (c *pdfCtx) bodyColor() { c.pdf.SetTextColor(26, 26, 26) }
func (c *pdfCtx) metaColor() { c.pdf.SetTextColor(160, 160, 160) }
func (c *pdfCtx) headColor() { c.pdf.SetTextColor(44, 62, 80) }
func (c *pdfCtx) noteColor() { c.pdf.SetTextColor(100, 100, 100) }

// renderPrayerSection renders a group of prayers (one language section) into
// the already-open pdf.  bodyFont is chosen by the caller to match the script.
func renderPrayerSection(ctx *pdfCtx, prayers []Prayer, lang, phelpsBaseURL string,
	translations map[string][]string) {

	pdf := ctx.pdf
	fi := ctx.fi
	lm := ctx.lm
	contentW := ctx.contentW
	lineH := 6.5

	isRTL := rtlLangs[lang]
	align := "L"
	if isRTL {
		align = "R"
	}

	// Determine which body font to use for this language.
	bodyFont := fi.bodyFont
	if langF, ok := fi.langFont[lang]; ok && fi.loaded[langF] {
		bodyFont = langF
	} else if wantFamily, ok := langScriptFont[lang]; ok && fi.loaded[wantFamily] {
		bodyFont = wantFamily
	} else if isRTL && fi.loaded["NotoNaskhArabic"] {
		bodyFont = "NotoNaskhArabic"
	} else if fi.loaded["NotoSerif"] {
		bodyFont = "NotoSerif"
	}

	// Group by category
	type catGroup struct {
		name    string
		prayers []Prayer
	}
	var groups []catGroup
	catIdx := map[string]int{}
	for _, p := range prayers {
		key := p.Category
		if key == "" {
			key = "\x00"
		}
		if idx, seen := catIdx[key]; seen {
			groups[idx].prayers = append(groups[idx].prayers, p)
		} else {
			catIdx[key] = len(groups)
			groups = append(groups, catGroup{name: p.Category, prayers: []Prayer{p}})
		}
	}

	for _, grp := range groups {
		if grp.name != "" {
			catName := grp.name
			if isRTL {
				catName = shapeText(catName)
			}
			ctx.headColor()
			pdf.SetFont(bodyFont, "", 14)
			pdf.MultiCell(contentW, 8, catName, "", align, false)
			y := pdf.GetY()
			pdf.SetDrawColor(44, 62, 80)
			pdf.Line(lm, y, lm+contentW, y)
			pdf.SetDrawColor(200, 200, 200)
			pdf.Ln(5)
			ctx.bodyColor()
		}

		for _, p := range grp.prayers {
			pdf.SetFont(fi.monoFont, "", 7)
			ctx.metaColor()
			pdf.CellFormat(contentW, 4, p.Phelps, "", 1, align, false, 0, "")
			ctx.bodyColor()

			for _, seg := range markdownToSegs(p.Text, isRTL) {
				switch seg.style {
				case segHeader:
					pdf.SetFont(bodyFont, "", 10)
					ctx.noteColor()
					pdf.MultiCell(contentW, 5.5, seg.text, "", align, false)
					pdf.Ln(0.5)
					ctx.bodyColor()
				case segSubheader:
					pdf.SetFont(bodyFont, "", 9)
					ctx.noteColor()
					pdf.MultiCell(contentW, 5, seg.text, "", align, false)
					pdf.Ln(0.5)
					ctx.bodyColor()
				case segVerse:
					indentW := 8.0
					effectiveW := contentW - indentW
					if !isRTL {
						pdf.SetX(lm + indentW)
					}
					pdf.SetFont(bodyFont, "", 11)
					pdf.MultiCell(effectiveW, lineH, seg.text, "", align, false)
					pdf.Ln(0.5)
				case segNote:
					pdf.SetFont(bodyFont, "", 9)
					ctx.noteColor()
					pdf.MultiCell(contentW, 5, seg.text, "", align, false)
					pdf.Ln(0.5)
					ctx.bodyColor()
				default:
					pdf.SetFont(bodyFont, "", 11)
					pdf.MultiCell(contentW, lineH, seg.text, "", align, false)
					pdf.Ln(0.5)
				}
			}

			var transLangs []string
			if ls, ok := translations[p.Phelps]; ok {
				for _, l := range ls {
					if l != lang {
						transLangs = append(transLangs, l)
					}
				}
			}
			if len(transLangs) > 0 {
				pdf.SetFont(bodyFont, "", 8)
				ctx.metaColor()
				pdf.MultiCell(contentW, 4, "Also in: "+strings.Join(transLangs, ", "), "", align, false)
				ctx.bodyColor()
			}

			pdf.Ln(2)
			y := pdf.GetY()
			pdf.Line(lm, y, lm+contentW, y)
			pdf.Ln(4)
		}
	}
}

// newPDF creates a configured Fpdf and returns a pdfCtx with geometry pre-computed.
// runes, if non-nil, is passed to font loaders so they can subset the TTF files
// via pyftsubset before embedding (reducing the combined PDF size significantly).
func newPDF(title, fontDir string, langs []string, runes map[rune]bool) (*pdfCtx, *fontInfo) {
	pdf := gofpdf.New("P", "mm", "A4", "")
	pdf.SetMargins(25, 22, 22)
	pdf.SetAutoPageBreak(true, 22)
	pdf.SetCreator("Bahá'í Prayer Book Generator", true)
	pdf.SetTitle(title, true)

	// For combined docs load all scripts; for single docs pick the right font.
	// loadFonts selects a primary bodyFont; we also pre-load the Arabic font
	// so it's available for renderPrayerSection when switching scripts.
	primaryLang := ""
	if len(langs) == 1 {
		primaryLang = langs[0]
	}
	fi := loadFonts(pdf, primaryLang, fontDir, runes)

	// For combined PDFs, load all required script fonts based on the language list.
	if len(langs) > 1 {
		// Track which font families we still need to load
		neededFamilies := map[string]bool{}
		for _, l := range langs {
			if family, ok := langScriptFont[l]; ok {
				neededFamilies[family] = true
			}
		}
		for family := range neededFamilies {
			if !fi.loaded[family] {
				if file, ok := scriptFontFile[family]; ok {
					loadTTFInto(pdf, fi, family, file, fontDir, runes)
				}
			}
			// Record per-language font mapping on fi
			for _, l := range langs {
				if langScriptFont[l] == family && fi.loaded[family] {
					fi.langFont[l] = family
				}
			}
		}
	}

	pageW, _ := pdf.GetPageSize()
	lm, _, rm, _ := pdf.GetMargins()
	contentW := pageW - lm - rm
	return &pdfCtx{pdf: pdf, fi: fi, contentW: contentW, lm: lm}, fi
}

// loadTTFInto is a helper that loads one font into an existing Fpdf+fontInfo.
// If runes is non-nil, pyftsubset is used to embed only the needed codepoints.
func loadTTFInto(pdf *gofpdf.Fpdf, fi *fontInfo, family, filename, fontDir string, runes map[rune]bool) {
	if fi.loaded[family] {
		return
	}
	home, _ := os.UserHomeDir()
	searchDirs := []string{fontDir, "fonts"}
	if home != "" {
		searchDirs = append(searchDirs,
			filepath.Join(home, ".local/share/fonts/noto"),
			filepath.Join(home, ".local/share/fonts"),
		)
	}
	searchDirs = append(searchDirs, "/usr/share/fonts/truetype/noto", "/usr/share/fonts/opentype/noto")
	for _, dir := range searchDirs {
		if dir == "" {
			continue
		}
		path := filepath.Join(dir, filename)
		if _, err := os.Stat(path); err != nil {
			continue
		}
		if subPath := subsetTTF(path, runes); subPath != "" {
			path = subPath
		}
		data, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		pdf.AddUTF8FontFromBytes(family, "", data)
		fi.loaded[family] = true
		fmt.Fprintf(os.Stderr, "  font: %s ← %s\n", family, filepath.Base(path))
		return
	}
}

// renderPDFGo generates a single-language prayer book PDF using gofpdf.
func renderPDFGo(prayers []Prayer, lang, title, lname, phelpsBaseURL string,
	translations map[string][]string, outFile, fontDir string) {

	runes := collectRunes(prayers, rtlLangs[lang])
	ctx, fi := newPDF(title, fontDir, []string{lang}, runes)
	pdf := ctx.pdf
	contentW := ctx.contentW

	// Title page
	pdf.AddPage()
	pdf.SetY(75)
	ctx.headColor()
	pdf.SetFont(fi.bodyFont, "", 24)
	pdf.MultiCell(contentW, 12, title, "", "C", false)
	if lname != "" {
		pdf.SetFont(fi.bodyFont, "", 14)
		ctx.metaColor()
		pdf.MultiCell(contentW, 8, lname, "", "C", false)
	}
	pdf.Ln(6)
	pdf.SetFont(fi.monoFont, "", 9)
	ctx.metaColor()
	pdf.MultiCell(contentW, 5, fmt.Sprintf("%d prayers", len(prayers)), "", "C", false)
	ctx.bodyColor()

	pdf.AddPage()
	renderPrayerSection(ctx, prayers, lang, phelpsBaseURL, translations)

	if err := pdf.OutputFileAndClose(outFile); err != nil {
		fmt.Fprintf(os.Stderr, "PDF write error: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("  Written: %s\n", outFile)
}

// renderCombinedPDF generates one PDF containing all supplied languages.
// No cover page or divider pages — each language starts on its own page
// with a compact inline header to keep the combined file size down.
func renderCombinedPDF(langPrayers []langSection, title, phelpsBaseURL string,
	translations map[string][]string, outFile, fontDir string) {

	// Collect all language codes and all runes for font subsetting.
	var langCodes []string
	var runeSets []map[rune]bool
	for _, ls := range langPrayers {
		langCodes = append(langCodes, ls.lang)
		runeSets = append(runeSets, collectRunes(ls.prayers, rtlLangs[ls.lang]))
	}
	allRunes := mergeRunes(runeSets...)

	ctx, fi := newPDF(title, fontDir, langCodes, allRunes)
	pdf := ctx.pdf
	contentW := ctx.contentW

	for _, ls := range langPrayers {
		pdf.AddPage()
		// Compact language header at top of each section
		ctx.headColor()
		pdf.SetFont(fi.bodyFont, "", 14)
		lname := ls.lname
		if lname == "" {
			lname = ls.lang
		}
		pdf.MultiCell(contentW, 8, lname, "", "C", false)
		pdf.SetFont(fi.monoFont, "", 8)
		ctx.metaColor()
		pdf.MultiCell(contentW, 5, fmt.Sprintf("%s · %d prayers", ls.lang, len(ls.prayers)), "", "C", false)
		pdf.Ln(3)
		y := pdf.GetY()
		pdf.Line(ctx.lm, y, ctx.lm+contentW, y)
		pdf.Ln(4)
		ctx.bodyColor()

		renderPrayerSection(ctx, ls.prayers, ls.lang, phelpsBaseURL, translations)
	}

	if err := pdf.OutputFileAndClose(outFile); err != nil {
		fmt.Fprintf(os.Stderr, "Combined PDF write error: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("  Written: %s\n", outFile)
}

type langSection struct {
	lang    string
	lname   string
	prayers []Prayer
}

// ── EPUB renderer (Go-native, no pandoc) ────────────────────────────────────────

func renderEPUB(htmlContent, outFile, tmpTag, title, lang string) {
	fmt.Printf("  Generating EPUB...\n")
	f, err := os.Create(outFile)
	if err != nil {
		fmt.Fprintf(os.Stderr, "  EPUB create error: %v\n", err)
		return
	}
	defer f.Close()
	w := zip.NewWriter(f)
	defer w.Close()

	// mimetype must be first and stored uncompressed (EPUB spec §3.4)
	mh := &zip.FileHeader{Name: "mimetype", Method: zip.Store}
	mh.Modified = time.Now()
	mw, _ := w.CreateHeader(mh)
	mw.Write([]byte("application/epub+zip")) //nolint:errcheck

	// META-INF/container.xml
	cw, _ := w.Create("META-INF/container.xml")
	cw.Write([]byte(`<?xml version="1.0" encoding="UTF-8"?>` + "\n" + //nolint:errcheck
		`<container version="1.0" xmlns="urn:oasis:names:tc:opendocument:xmlns:container">` + "\n" +
		`  <rootfiles>` + "\n" +
		`    <rootfile full-path="OEBPS/content.opf" media-type="application/oebps-package+xml"/>` + "\n" +
		`  </rootfiles>` + "\n" +
		`</container>`))

	// OEBPS/content.xhtml — HTML body converted to valid XHTML
	dir := "ltr"
	if rtlLangs[lang] {
		dir = "rtl"
	}
	bodyRe := regexp.MustCompile(`(?si)<body[^>]*>(.*)</body>`)
	body := htmlContent
	if m := bodyRe.FindStringSubmatch(htmlContent); len(m) > 1 {
		body = m[1]
	}
	body = strings.ReplaceAll(body, "<br>", "<br/>")
	body = strings.ReplaceAll(body, "<hr>", "<hr/>")
	xhtml := `<?xml version="1.0" encoding="UTF-8"?>` + "\n" +
		`<!DOCTYPE html>` + "\n" +
		`<html xmlns="http://www.w3.org/1999/xhtml" xml:lang="` + lang + `" lang="` + lang + `" dir="` + dir + `">` + "\n" +
		`<head><meta charset="UTF-8"/>` + "\n" +
		`<title>` + template.HTMLEscapeString(title) + `</title>` + "\n" +
		`<style>` + "\n" +
		`body { font-family: "Noto Serif", serif; font-size: 11pt; line-height: 1.7; direction: ` + dir + `; }` + "\n" +
		`h1, h2.cat { font-size: 14pt; margin-top: 2em; }` + "\n" +
		`h3.cat { font-size: 12pt; margin-top: 1.5em; }` + "\n" +
		`.prayer { margin-bottom: 2em; }` + "\n" +
		`.meta { font-size: 8pt; color: #aaa; font-family: monospace; }` + "\n" +
		`p.verse { margin-left: 1.5em; font-style: italic; }` + "\n" +
		`p.note { font-size: 9pt; color: #666; }` + "\n" +
		`.trans { font-size: 8pt; color: #bbb; font-style: italic; }` + "\n" +
		`</style></head>` + "\n" +
		`<body>` + "\n" + body + "\n" + `</body></html>`
	xw, _ := w.Create("OEBPS/content.xhtml")
	xw.Write([]byte(xhtml)) //nolint:errcheck

	// OEBPS/nav.xhtml — minimal navigation document (EPUB3 required)
	headRe := regexp.MustCompile(`<h[123][^>]*class="cat"[^>]*>([^<]+)</h[123]>`)
	headMatches := headRe.FindAllStringSubmatch(htmlContent, -1)
	var navItems strings.Builder
	for _, m := range headMatches {
		navItems.WriteString("    <li><a href=\"content.xhtml\">" + template.HTMLEscapeString(strings.TrimSpace(m[1])) + "</a></li>\n")
	}
	if navItems.Len() == 0 {
		navItems.WriteString("    <li><a href=\"content.xhtml\">" + template.HTMLEscapeString(title) + "</a></li>\n")
	}
	nav := `<?xml version="1.0" encoding="UTF-8"?>` + "\n" +
		`<!DOCTYPE html>` + "\n" +
		`<html xmlns="http://www.w3.org/1999/xhtml" xmlns:epub="http://www.idpf.org/2007/ops">` + "\n" +
		`<head><meta charset="UTF-8"/><title>` + template.HTMLEscapeString(title) + `</title></head>` + "\n" +
		`<body><nav epub:type="toc"><h1>` + template.HTMLEscapeString(title) + `</h1>` + "\n" +
		`<ol>` + "\n" + navItems.String() + `</ol></nav></body></html>`
	nw, _ := w.Create("OEBPS/nav.xhtml")
	nw.Write([]byte(nav)) //nolint:errcheck

	// OEBPS/content.opf — package metadata
	modified := time.Now().UTC().Format("2006-01-02T15:04:05Z")
	uid := "bahai-prayers-" + tmpTag
	opf := `<?xml version="1.0" encoding="UTF-8"?>` + "\n" +
		`<package version="3.0" xmlns="http://www.idpf.org/2007/opf" unique-identifier="uid">` + "\n" +
		`  <metadata xmlns:dc="http://purl.org/dc/elements/1.1/">` + "\n" +
		`    <dc:identifier id="uid">` + uid + `</dc:identifier>` + "\n" +
		`    <dc:title>` + template.HTMLEscapeString(title) + `</dc:title>` + "\n" +
		`    <dc:language>` + lang + `</dc:language>` + "\n" +
		`    <dc:creator>Bahá'í Writings</dc:creator>` + "\n" +
		`    <meta property="dcterms:modified">` + modified + `</meta>` + "\n" +
		`  </metadata>` + "\n" +
		`  <manifest>` + "\n" +
		`    <item id="nav" href="nav.xhtml" media-type="application/xhtml+xml" properties="nav"/>` + "\n" +
		`    <item id="content" href="content.xhtml" media-type="application/xhtml+xml"/>` + "\n" +
		`  </manifest>` + "\n" +
		`  <spine><itemref idref="content"/></spine>` + "\n" +
		`</package>`
	ow, _ := w.Create("OEBPS/content.opf")
	ow.Write([]byte(opf)) //nolint:errcheck

	fmt.Printf("  Written: %s\n", outFile)
}

func renderHTML(htmlContent, outFile string) {
	if err := os.WriteFile(outFile, []byte(htmlContent), 0644); err != nil {
		fmt.Fprintf(os.Stderr, "Write error: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("  Written: %s\n", outFile)
}

// ── HTML generator (for EPUB source) ──────────────────────────────────────────

func markdownToHTML(text string) template.HTML {
	lines := strings.Split(sanitizeText(text), "\n")
	var buf strings.Builder
	inParagraph := false
	closePara := func() {
		if inParagraph {
			buf.WriteString("</p>\n")
			inParagraph = false
		}
	}
	openPara := func() {
		if !inParagraph {
			buf.WriteString("<p>")
			inParagraph = true
		}
	}
	for i, line := range lines {
		trimmed := strings.TrimSpace(line)
		switch {
		case strings.HasPrefix(trimmed, "## "):
			closePara()
			buf.WriteString(fmt.Sprintf("<h3>%s</h3>\n", template.HTMLEscapeString(strings.TrimPrefix(trimmed, "## "))))
		case strings.HasPrefix(trimmed, "# "):
			closePara()
			buf.WriteString(fmt.Sprintf("<h2>%s</h2>\n", template.HTMLEscapeString(strings.TrimPrefix(trimmed, "# "))))
		case strings.HasPrefix(trimmed, "* "):
			closePara()
			buf.WriteString(fmt.Sprintf("<p class=\"verse\">%s</p>\n", template.HTMLEscapeString(strings.TrimPrefix(trimmed, "* "))))
		case strings.HasPrefix(trimmed, "! "):
			closePara()
			buf.WriteString(fmt.Sprintf("<p class=\"note\"><em>%s</em></p>\n", template.HTMLEscapeString(strings.TrimPrefix(trimmed, "! "))))
		case trimmed == "":
			if inParagraph {
				closePara()
			} else if i > 0 && i < len(lines)-1 {
				buf.WriteString("<br>\n")
			}
		default:
			if inParagraph {
				buf.WriteString("<br>\n" + template.HTMLEscapeString(trimmed))
			} else {
				openPara()
				buf.WriteString(template.HTMLEscapeString(trimmed))
			}
		}
	}
	closePara()
	return template.HTML(buf.String())
}

func slugify(s string) string {
	re := regexp.MustCompile(`[^a-z0-9]+`)
	return re.ReplaceAllString(strings.ToLower(s), "-")
}

type CategorySection struct {
	Name    string
	Slug    string
	Prayers []PrayerPage
}

type PrayerPage struct {
	Phelps     string
	ID         string
	HTML       template.HTML
	TransLinks template.HTML
}

func basePINKey(pin string) string {
	n := len(pin)
	if n < 4 {
		return pin
	}
	suffix := pin[n-3:]
	for _, c := range suffix {
		if c < 'A' || c > 'Z' {
			return pin
		}
	}
	if pin[n-4] >= '0' && pin[n-4] <= '9' {
		return pin[:n-3]
	}
	return pin
}

const htmlTmpl = `{{if not .Fragment}}<!DOCTYPE html>
<html lang="{{.Lang}}" dir="{{.Dir}}">
<head>
<meta charset="UTF-8">
<title>{{.Title}}</title>
<style>
body { font-family: "Noto Serif", serif; font-size: 11pt; line-height: 1.7; direction: {{.Dir}}; }
h1.cat { font-size: 14pt; margin-top: 2em; }
h2.cat { font-size: 14pt; margin-top: 2em; }
h3.cat { font-size: 12pt; margin-top: 1.5em; }
.prayer { margin-bottom: 2em; }
.meta { font-size: 8pt; color: #aaa; font-family: monospace; }
p.verse { margin-left: 1.5em; font-style: italic; }
p.note { font-size: 9pt; color: #666; }
.trans { font-size: 8pt; color: #bbb; font-style: italic; }
</style>
</head>
<body>
<h1>{{.Title}}</h1>
{{else}}<section lang="{{.Lang}}" dir="{{.Dir}}">
<h2>{{.Title}}</h2>
{{end}}{{range .Categories}}
<div>
  {{if .Name}}{{if $.Fragment}}<h3 class="cat">{{.Name}}</h3>{{else if $.FlatEPUB}}<h2 class="cat">{{.Name}}</h2>{{else}}<h1 class="cat">{{.Name}}</h1>{{end}}{{end}}
  {{range .Prayers}}
  <div class="prayer"{{if .ID}} id="{{.ID}}"{{end}}>
    <div class="meta">{{.Phelps}}</div>
    {{.HTML}}
    {{if .TransLinks}}<p class="trans">{{.TransLinks}}</p>{{end}}
  </div>
  {{end}}
</div>
{{end}}{{if not .Fragment}}</body>
</html>{{else}}</section>
{{end}}`

func generateHTML(prayers []Prayer, lang, title, phelpsBaseURL string, translations map[string][]string, flatEPUB bool, fragment bool, includedLangs map[string]bool) string {
	dir := "ltr"
	if rtlLangs[lang] {
		dir = "rtl"
	}
	var categories []CategorySection
	catIdx := map[string]int{}
	seenIDs := map[string]bool{}
	for _, p := range prayers {
		key := p.Category
		if key == "" {
			key = "\x00"
		}
		if _, seen := catIdx[key]; !seen {
			catIdx[key] = len(categories)
			categories = append(categories, CategorySection{Name: p.Category, Slug: slugify(p.Category)})
		}
		idx := catIdx[key]
		var transLangs []string
		if ls, ok := translations[p.Phelps]; ok {
			seen := map[string]bool{}
			for _, l := range ls {
				if l != lang && !seen[l] && (includedLangs == nil || includedLangs[l]) {
					seen[l] = true
					transLangs = append(transLangs, l)
				}
			}
		}
		phelpsKey := strings.ToLower(p.Phelps)
		var id string
		var transLinks template.HTML
		if fragment {
			candidate := lang + "-" + phelpsKey
			if !seenIDs[candidate] {
				seenIDs[candidate] = true
				id = candidate
			}
			if len(transLangs) > 0 {
				var parts []string
				for _, tl := range transLangs {
					anchor := tl + "-" + phelpsKey
					parts = append(parts, fmt.Sprintf(`<a href="#%s">%s</a>`, anchor, template.HTMLEscapeString(tl)))
				}
				transLinks = template.HTML("Also in: " + strings.Join(parts, ", "))
			}
		} else if len(transLangs) > 0 {
			transLinks = template.HTML("Also in: " + template.HTMLEscapeString(strings.Join(transLangs, ", ")))
		}
		categories[idx].Prayers = append(categories[idx].Prayers, PrayerPage{
			Phelps:     p.Phelps,
			ID:         id,
			HTML:       markdownToHTML(p.Text),
			TransLinks: transLinks,
		})
	}
	data := struct {
		Title      string
		Lang       string
		Dir        string
		Categories []CategorySection
		FlatEPUB   bool
		Fragment   bool
	}{Title: title, Lang: lang, Dir: dir, Categories: categories, FlatEPUB: flatEPUB, Fragment: fragment}
	tmpl := template.Must(template.New("p").Parse(htmlTmpl))
	var buf strings.Builder
	if err := tmpl.Execute(&buf, data); err != nil {
		fmt.Fprintf(os.Stderr, "template error: %v\n", err)
		os.Exit(1)
	}
	return buf.String()
}

// ── Index mode ─────────────────────────────────────────────────────────────────

type IndexEntry struct {
	Phelps    string
	FirstLine string
	LangCount int
}

func firstLine(text string) string {
	for _, line := range strings.Split(text, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		line = strings.TrimPrefix(line, "* ")
		line = strings.TrimPrefix(line, "! ")
		runes := []rune(line)
		if len(runes) > 100 {
			return string(runes[:100]) + "…"
		}
		return line
	}
	return ""
}

const indexTmpl = `<!DOCTYPE html>
<html lang="en"><head><meta charset="UTF-8"><title>{{.Title}}</title>
<style>
body{font-family:sans-serif;font-size:9pt}
table{width:100%;border-collapse:collapse}
th{background:#2c3e50;color:white;padding:3px 6px;text-align:left}
td{padding:2px 6px;border-bottom:1px solid #eee}
</style></head><body>
<h1>{{.Title}}</h1>
<p>{{.Count}} prayers</p>
<table><thead><tr><th>Phelps</th><th>First line</th><th>L</th></tr></thead><tbody>
{{range .Entries}}<tr><td>{{.Phelps}}</td><td>{{.FirstLine}}</td><td>{{.LangCount}}</td></tr>
{{end}}</tbody></table></body></html>`

func generateIndex(dbPath, source, title string) string {
	countRows := doltCSV(dbPath, fmt.Sprintf(
		"SELECT phelps, COUNT(DISTINCT language) as lc FROM writings WHERE source='%s' AND phelps IS NOT NULL AND phelps <> '' GROUP BY phelps ORDER BY phelps", source))
	enRows := doltCSV(dbPath, fmt.Sprintf(
		"SELECT phelps, text FROM writings WHERE source='%s' AND language='en' AND phelps IS NOT NULL ORDER BY phelps", source))
	enText := map[string]string{}
	for _, r := range enRows {
		if len(r) >= 2 {
			if _, e := enText[r[0]]; !e {
				enText[r[0]] = r[1]
			}
		}
	}
	var entries []IndexEntry
	for _, r := range countRows {
		if len(r) < 2 {
			continue
		}
		lc := 0
		fmt.Sscanf(r[1], "%d", &lc)
		entries = append(entries, IndexEntry{Phelps: r[0], FirstLine: firstLine(enText[r[0]]), LangCount: lc})
	}
	data := struct {
		Title   string
		Count   int
		Entries []IndexEntry
	}{title, len(entries), entries}
	tmpl := template.Must(template.New("i").Parse(indexTmpl))
	var buf strings.Builder
	tmpl.Execute(&buf, data)
	return buf.String()
}

// ── Data queries ───────────────────────────────────────────────────────────────

func queryTranslations(dbPath, source string) map[string][]string {
	rows := doltCSV(dbPath, fmt.Sprintf(
		"SELECT phelps, language FROM writings WHERE source='%s' AND phelps IS NOT NULL AND phelps <> '' ORDER BY phelps, language", source))
	m := map[string][]string{}
	for _, row := range rows {
		if len(row) < 2 || strings.HasSuffix(row[1], "-translit") {
			continue
		}
		m[row[0]] = append(m[row[0]], row[1])
	}
	return m
}

func queryPrayers(dbPath, source, lang string) []Prayer {
	safe := strings.ReplaceAll(lang, "'", "''")
	rows := doltCSV(dbPath, fmt.Sprintf(`
SELECT w.phelps, w.text, COALESCE(w.name,''),
       COALESCE(pbs.category_name,''),
       COALESCE(pbs.category_order,'0'),
       COALESCE(pbs.order_in_category,'0')
FROM writings w
LEFT JOIN prayer_book_structure pbs
    ON pbs.phelps_code = w.phelps AND pbs.source_language = 'en'
WHERE w.language = '%s' AND w.source = '%s'
    AND w.phelps IS NOT NULL AND w.phelps <> ''
ORDER BY COALESCE(pbs.category_order,9999),
         COALESCE(pbs.order_in_category,9999),
         w.phelps
`, safe, source))
	var out []Prayer
	for _, row := range rows {
		if len(row) < 6 {
			continue
		}
		catOrd, ordInCat := 0, 0
		fmt.Sscanf(row[4], "%d", &catOrd)
		fmt.Sscanf(row[5], "%d", &ordInCat)
		out = append(out, Prayer{
			Phelps:        row[0],
			Text:          row[1],
			Name:          row[2],
			Language:      lang,
			Category:      row[3],
			CategoryOrder: catOrd,
			OrderInCat:    ordInCat,
		})
	}
	return out
}

func langName(dbPath, lang string) string {
	rows := doltCSV(dbPath, fmt.Sprintf("SELECT name FROM languages WHERE langcode='%s' LIMIT 1", lang))
	if len(rows) == 0 || len(rows[0]) == 0 {
		return ""
	}
	return rows[0][0]
}

// ── main ───────────────────────────────────────────────────────────────────────

func main() {
	home, _ := os.UserHomeDir()
	defaultDB := filepath.Join(home, "prayermatching", "bahaiwritings")
	defaultFontDir := "fonts" // relative to CWD; override with --font-dir

	db         := flag.String("db", defaultDB, "Path to dolt repo")
	lang       := flag.String("lang", "en", "Language code (or 'all')")
	source     := flag.String("source", "bahaiprayers.net", "Prayer source")
	output     := flag.String("output", "", "Output file")
	outDir     := flag.String("out-dir", "", "Output directory")
	fontDir    := flag.String("font-dir", defaultFontDir, "Directory with Noto .ttf fonts")
	htmlOnly    := flag.Bool("html-only", false, "Output HTML only (for EPUB pipeline)")
	epubMode    := flag.Bool("epub", false, "Generate EPUB via pandoc")
	bothMode    := flag.Bool("both", false, "Generate both PDF and EPUB")
	indexMode   := flag.Bool("index", false, "Generate first-lines concordance index")
	combinedMode  := flag.Bool("combined", false, "Generate combined PDF/EPUB for all languages")
	title      := flag.String("title", "Bahá'í Prayers", "Document title")
	phelpsBase := flag.String("phelps-base-url", "", "Base URL for phelps links")
	flag.Parse()

	// Index mode
	if *indexMode {
		html := generateIndex(*db, *source, *title+" — First Lines Index")
		outFile := *output
		if outFile == "" {
			outFile = "prayers_index.html"
		}
		renderHTML(html, outFile)
		return
	}

	// Resolve languages
	var langs []string
	if *lang == "all" {
		rows := doltCSV(*db, fmt.Sprintf(
			"SELECT DISTINCT language FROM writings WHERE source='%s' AND phelps IS NOT NULL AND phelps <> '' ORDER BY language",
			*source))
		for _, row := range rows {
			if len(row) > 0 && !strings.HasSuffix(row[0], "-translit") {
				langs = append(langs, row[0])
			}
		}
	} else {
		langs = []string{*lang}
	}
	sort.Strings(langs)

	fmt.Println("Loading translation index...")
	translationsMap := queryTranslations(*db, *source)

	dir := *outDir
	if dir == "" {
		dir = "."
	}
	if err := os.MkdirAll(dir, 0755); err != nil {
		fmt.Fprintf(os.Stderr, "mkdir: %v\n", err)
		os.Exit(1)
	}

	// nonLatinScript lists language codes that use a non-Latin writing system.
	// All other languages are grouped into the "latin" volume (which also covers
	// Cyrillic, Greek and other European scripts — essentially "non-Asian/non-RTL").
	// The split keeps each PDF under Cloudflare Pages' 25 MiB per-file limit.
	nonLatinScript := map[string]bool{
		// Arabic / Perso-Arabic
		"ar": true, "fa": true, "ur": true, "ug": true, "dih": true,
		// CJK
		"zh-Hans": true, "zh-Hant": true, "ja": true, "ko": true,
		// Devanagari
		"hi": true, "mr": true, "ne": true,
		// Other Indic scripts
		"bn": true, "ta": true, "te": true, "ml": true,
		"kn": true, "gu": true, "pa": true, "si": true,
		// Southeast Asian
		"th": true, "lo": true, "km": true, "my": true,
		// Other non-Latin
		"he": true, "am": true, "ti": true,
	}

	// Combined mode: PDFs split by script family + a single EPUB for all
	if *combinedMode {
		var allSections, latinSections, otherSections []langSection
		for _, l := range langs {
			fmt.Printf("Loading %s...\n", l)
			prayers := queryPrayers(*db, *source, l)
			if len(prayers) == 0 {
				continue
			}
			lname := langName(*db, l)
			sec := langSection{lang: l, lname: lname, prayers: prayers}
			allSections = append(allSections, sec)
			if nonLatinScript[l] {
				otherSections = append(otherSections, sec)
			} else {
				latinSections = append(latinSections, sec)
			}
			// Include English in both volumes as a reference
			if l == "en" {
				otherSections = append(otherSections, sec)
			}
		}
		fmt.Printf("Combined: %d languages total (%d latin, %d non-latin)\n",
			len(allSections), len(latinSections), len(otherSections))

		combinedTitle := *title + " — All Languages"
		outBase := filepath.Join(dir, "prayers_all")

		if !*epubMode {
			if len(latinSections) > 0 {
				renderCombinedPDF(latinSections,
					*title+" — Latin & European Scripts",
					*phelpsBase, translationsMap,
					outBase+"_latin.pdf", *fontDir)
			}
			if len(otherSections) > 0 {
				renderCombinedPDF(otherSections,
					*title+" — Asian & Other Scripts",
					*phelpsBase, translationsMap,
					outBase+"_other.pdf", *fontDir)
			}
		}
		if *epubMode || *bothMode {
			// Single EPUB with all languages — one HTML doc, one chapter
			var buf strings.Builder
			buf.WriteString(`<!DOCTYPE html>
<html lang="mul"><head>
<meta charset="UTF-8">
<title>` + template.HTMLEscapeString(combinedTitle) + `</title>
<style>
body { font-family: "Noto Serif", serif; font-size: 11pt; line-height: 1.7; }
h2.cat { font-size: 14pt; margin-top: 2em; }
h3.cat { font-size: 12pt; margin-top: 1.5em; }
.prayer { margin-bottom: 2em; }
.meta { font-size: 8pt; color: #aaa; font-family: monospace; }
p.verse { margin-left: 1.5em; font-style: italic; }
p.note { font-size: 9pt; color: #666; }
.trans { font-size: 8pt; color: #bbb; font-style: italic; }
</style>
</head>
<body>
<h1>` + template.HTMLEscapeString(combinedTitle) + `</h1>
`)
			includedLangs := map[string]bool{}
			for _, ls := range allSections {
				includedLangs[ls.lang] = true
			}
			for _, ls := range allSections {
				docTitle := *title + " — " + ls.lname
				buf.WriteString(generateHTML(ls.prayers, ls.lang, docTitle, *phelpsBase, translationsMap, false, true, includedLangs))
			}
			buf.WriteString("</body>\n</html>")
			renderEPUB(buf.String(), outBase+".epub", "all", combinedTitle, "mul")
		}
		return
	}

	for _, l := range langs {
		fmt.Printf("Processing %s...\n", l)
		prayers := queryPrayers(*db, *source, l)
		if len(prayers) == 0 {
			fmt.Printf("  No resolved prayers, skipping.\n")
			continue
		}
		fmt.Printf("  %d prayers\n", len(prayers))

		lname := langName(*db, l)
		docTitle := *title
		if lname != "" {
			docTitle = *title + " — " + lname
		}

		var baseName string
		if *output != "" && len(langs) == 1 {
			baseName = strings.TrimSuffix(*output, filepath.Ext(*output))
		} else {
			baseName = filepath.Join(dir, "prayers_"+l)
		}

		if *htmlOnly {
			html := generateHTML(prayers, l, docTitle, *phelpsBase, translationsMap, false, false, nil)
			renderHTML(html, baseName+".html")
		} else if *epubMode {
			html := generateHTML(prayers, l, docTitle, *phelpsBase, nil, true, false, nil)
			renderEPUB(html, baseName+".epub", l, docTitle, l)
		} else if *bothMode {
			renderPDFGo(prayers, l, docTitle, lname, *phelpsBase, translationsMap, baseName+".pdf", *fontDir)
			html := generateHTML(prayers, l, docTitle, *phelpsBase, nil, true, false, nil)
			renderEPUB(html, baseName+".epub", l, docTitle, l)
		} else {
			renderPDFGo(prayers, l, docTitle, lname, *phelpsBase, translationsMap, baseName+".pdf", *fontDir)
		}
	}
}
