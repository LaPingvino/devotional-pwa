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
// We use AddUTF8FontFromBytes (reads the file ourselves) rather than AddUTF8Font
// to avoid gofpdf's internal font-directory path prepending.
func loadFonts(pdf *gofpdf.Fpdf, lang, fontDir string) *fontInfo {
	fi := &fontInfo{loaded: map[string]bool{}}

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
	loadTTF := func(family, filename string) bool {
		if fi.loaded[family] {
			return true
		}
		path := findFont(filename)
		if path == "" {
			return false
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
	switch lang {
	case "ar", "fa", "ur":
		if loadTTF("NotoNaskhArabic", "NotoNaskhArabic-Regular.ttf") {
			fi.bodyFont = "NotoNaskhArabic"
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
	if isRTL && fi.loaded["NotoNaskhArabic"] {
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
func newPDF(title, fontDir string, langs []string) (*pdfCtx, *fontInfo) {
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
	fi := loadFonts(pdf, primaryLang, fontDir)

	// For combined PDFs, always ensure Arabic font is loaded too.
	if len(langs) > 1 && !fi.loaded["NotoNaskhArabic"] {
		loadTTFInto(pdf, fi, "NotoNaskhArabic", "NotoNaskhArabic-Regular.ttf", fontDir)
	}

	pageW, _ := pdf.GetPageSize()
	lm, _, rm, _ := pdf.GetMargins()
	contentW := pageW - lm - rm
	return &pdfCtx{pdf: pdf, fi: fi, contentW: contentW, lm: lm}, fi
}

// loadTTFInto is a helper that loads one font into an existing Fpdf+fontInfo.
func loadTTFInto(pdf *gofpdf.Fpdf, fi *fontInfo, family, filename, fontDir string) {
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

	ctx, fi := newPDF(title, fontDir, []string{lang})
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

// renderCombinedPDF generates one PDF containing all supplied languages,
// each preceded by a language divider page.
func renderCombinedPDF(langPrayers []langSection, title, phelpsBaseURL string,
	translations map[string][]string, outFile, fontDir string) {

	// Collect all language codes for font pre-loading
	var langCodes []string
	total := 0
	for _, ls := range langPrayers {
		langCodes = append(langCodes, ls.lang)
		total += len(ls.prayers)
	}

	ctx, fi := newPDF(title, fontDir, langCodes)
	pdf := ctx.pdf
	contentW := ctx.contentW

	// Cover page
	pdf.AddPage()
	pdf.SetY(75)
	ctx.headColor()
	pdf.SetFont(fi.bodyFont, "", 24)
	pdf.MultiCell(contentW, 12, title, "", "C", false)
	pdf.SetFont(fi.bodyFont, "", 14)
	ctx.metaColor()
	pdf.MultiCell(contentW, 8, "All Languages", "", "C", false)
	pdf.Ln(6)
	pdf.SetFont(fi.monoFont, "", 9)
	pdf.MultiCell(contentW, 5, fmt.Sprintf("%d prayers · %d languages", total, len(langPrayers)), "", "C", false)
	ctx.bodyColor()

	for _, ls := range langPrayers {
		// Language divider page
		pdf.AddPage()
		pdf.SetY(90)
		ctx.headColor()
		pdf.SetFont(fi.bodyFont, "", 20)
		pdf.MultiCell(contentW, 10, ls.lname, "", "C", false)
		pdf.SetFont(fi.monoFont, "", 10)
		ctx.metaColor()
		pdf.MultiCell(contentW, 6, fmt.Sprintf("%s · %d prayers", ls.lang, len(ls.prayers)), "", "C", false)
		ctx.bodyColor()

		pdf.AddPage()
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

// ── EPUB renderer (pandoc) ─────────────────────────────────────────────────────

func renderEPUB(htmlContent, outFile, tmpTag, title, lang string) {
	tmpFile := fmt.Sprintf("/tmp/prayers_%s_epub.html", tmpTag)
	if err := os.WriteFile(tmpFile, []byte(htmlContent), 0644); err != nil {
		fmt.Fprintf(os.Stderr, "Temp write error: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("  Converting to EPUB...\n")
	cmd := exec.Command("pandoc",
		"--metadata", "title="+title,
		"--metadata", "lang="+lang,
		"-f", "html",
		"-t", "epub",
		"--toc",
		"--toc-depth=1",
		"-o", outFile,
		tmpFile,
	)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		fmt.Fprintf(os.Stderr, "  pandoc error (EPUB skipped): %v\n", err)
	} else {
		fmt.Printf("  Written: %s\n", outFile)
	}
	os.Remove(tmpFile)
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
	Phelps       string
	HTML         template.HTML
	Translations string
	PhelpsLink   string
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

const htmlTmpl = `<!DOCTYPE html>
<html lang="{{.Lang}}" dir="{{.Dir}}">
<head>
<meta charset="UTF-8">
<title>{{.Title}}</title>
<style>
body { font-family: "Noto Serif", serif; font-size: 11pt; line-height: 1.7; direction: {{.Dir}}; }
h1.cat { font-size: 14pt; margin-top: 2em; }
.prayer { margin-bottom: 2em; }
.meta { font-size: 8pt; color: #aaa; font-family: monospace; }
p.verse { margin-left: 1.5em; font-style: italic; }
p.note { font-size: 9pt; color: #666; }
.trans { font-size: 8pt; color: #bbb; font-style: italic; }
</style>
</head>
<body>
<h1>{{.Title}}</h1>
{{range .Categories}}
<div>
  {{if .Name}}<h1 class="cat">{{.Name}}</h1>{{end}}
  {{range .Prayers}}
  <div class="prayer">
    <div class="meta">{{.Phelps}}</div>
    {{.HTML}}
    {{if .Translations}}<p class="trans">{{.Translations}}</p>{{end}}
  </div>
  {{end}}
</div>
{{end}}
</body>
</html>`

func generateHTML(prayers []Prayer, lang, title, phelpsBaseURL string, translations map[string][]string) string {
	dir := "ltr"
	if rtlLangs[lang] {
		dir = "rtl"
	}
	var categories []CategorySection
	catIdx := map[string]int{}
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
			for _, l := range ls {
				if l != lang {
					transLangs = append(transLangs, l)
				}
			}
		}
		transNote := ""
		if len(transLangs) > 0 {
			transNote = "Also in: " + strings.Join(transLangs, ", ")
		}
		phelpsLink := ""
		if phelpsBaseURL != "" {
			phelpsLink = phelpsBaseURL + strings.ToLower(basePINKey(p.Phelps)) + "/"
		}
		categories[idx].Prayers = append(categories[idx].Prayers, PrayerPage{
			Phelps:       p.Phelps,
			HTML:         markdownToHTML(p.Text),
			Translations: transNote,
			PhelpsLink:   phelpsLink,
		})
	}
	data := struct {
		Title      string
		Lang       string
		Dir        string
		Categories []CategorySection
	}{Title: title, Lang: lang, Dir: dir, Categories: categories}
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
	combinedMode := flag.Bool("combined", false, "Generate a single combined PDF/EPUB for all languages")
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

	// Combined mode: one PDF/EPUB containing all languages
	if *combinedMode {
		var sections []langSection
		for _, l := range langs {
			fmt.Printf("Loading %s...\n", l)
			prayers := queryPrayers(*db, *source, l)
			if len(prayers) == 0 {
				continue
			}
			lname := langName(*db, l)
			sections = append(sections, langSection{lang: l, lname: lname, prayers: prayers})
		}
		fmt.Printf("Building combined output for %d languages...\n", len(sections))
		combinedBase := filepath.Join(dir, "prayers_all")
		if *output != "" {
			combinedBase = strings.TrimSuffix(*output, filepath.Ext(*output))
		}
		combinedTitle := *title + " — All Languages"
		if !*epubMode {
			renderCombinedPDF(sections, combinedTitle, *phelpsBase, translationsMap, combinedBase+".pdf", *fontDir)
		}
		if *epubMode || *bothMode {
			// Build a mega-HTML for EPUB
			var buf strings.Builder
			for _, ls := range sections {
				docTitle := *title + " — " + ls.lname
				buf.WriteString(generateHTML(ls.prayers, ls.lang, docTitle, *phelpsBase, translationsMap))
			}
			renderEPUB(buf.String(), combinedBase+".epub", "all", combinedTitle, "mul")
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
			html := generateHTML(prayers, l, docTitle, *phelpsBase, translationsMap)
			renderHTML(html, baseName+".html")
		} else if *epubMode {
			html := generateHTML(prayers, l, docTitle, *phelpsBase, translationsMap)
			renderEPUB(html, baseName+".epub", l, docTitle, l)
		} else if *bothMode {
			renderPDFGo(prayers, l, docTitle, lname, *phelpsBase, translationsMap, baseName+".pdf", *fontDir)
			html := generateHTML(prayers, l, docTitle, *phelpsBase, translationsMap)
			renderEPUB(html, baseName+".epub", l, docTitle, l)
		} else {
			renderPDFGo(prayers, l, docTitle, lname, *phelpsBase, translationsMap, baseName+".pdf", *fontDir)
		}
	}
}
