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
//   --db      Path to dolt repo     (default: ~/prayermatching/bahaiwritings)
//   --lang    Language code         (default: en; use "all" for all languages)
//   --source  Prayer source         (default: bahaiprayers.net)
//   --output  Output file           (default: prayers_LANG.pdf / .epub)
//   --out-dir Output directory      (default: current dir; used with --lang all)
//   --html-only  Skip weasyprint, output HTML only
//   --epub    Generate EPUB via pandoc (instead of PDF)
//   --both    Generate both PDF and EPUB
//   --index   Generate first-lines concordance index instead of full prayers
//   --title   PDF/EPUB title        (default: "Bahá'í Prayers")
//   --phelps-base-url  Base URL for phelps inventory links (e.g. https://site.example/phelps/)

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
)

// RTL script languages
var rtlLangs = map[string]bool{
	"ar": true, "fa": true, "ur": true, "he": true,
	"ug": true, // Uyghur
}

type Prayer struct {
	Phelps        string
	Text          string
	Name          string
	Language      string
	Category      string
	CategoryOrder int
	OrderInCat    int
	Translations  []string // other language codes that also have this prayer
}

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

// markdownToHTML converts the simple markdown used in prayer texts to HTML.
// Supported: ## heading, # heading, * verse line, ! note, blank line = <br>
func markdownToHTML(text string) template.HTML {
	lines := strings.Split(text, "\n")
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
			t := template.HTMLEscapeString(strings.TrimPrefix(trimmed, "## "))
			buf.WriteString(fmt.Sprintf("<h3 class=\"prayer-sub-header\">%s</h3>\n", t))

		case strings.HasPrefix(trimmed, "# "):
			closePara()
			t := template.HTMLEscapeString(strings.TrimPrefix(trimmed, "# "))
			buf.WriteString(fmt.Sprintf("<h2 class=\"prayer-header\">%s</h2>\n", t))

		case strings.HasPrefix(trimmed, "* "):
			closePara()
			t := template.HTMLEscapeString(strings.TrimPrefix(trimmed, "* "))
			buf.WriteString(fmt.Sprintf("<p class=\"verse\">%s</p>\n", t))

		case strings.HasPrefix(trimmed, "! "):
			closePara()
			t := template.HTMLEscapeString(strings.TrimPrefix(trimmed, "! "))
			buf.WriteString(fmt.Sprintf("<p class=\"note\"><em>%s</em></p>\n", t))

		case trimmed == "":
			if inParagraph {
				closePara()
			} else if i > 0 && i < len(lines)-1 {
				buf.WriteString("<br>\n")
			}

		default:
			if inParagraph {
				buf.WriteString("<br>\n")
				buf.WriteString(template.HTMLEscapeString(trimmed))
			} else {
				openPara()
				buf.WriteString(template.HTMLEscapeString(trimmed))
			}
		}
	}
	closePara()
	return template.HTML(buf.String())
}

// slugify converts a category name to an HTML-safe ID
func slugify(s string) string {
	re := regexp.MustCompile(`[^a-z0-9]+`)
	return re.ReplaceAllString(strings.ToLower(s), "-")
}

// CategorySection groups prayers under a category header
type CategorySection struct {
	Name    string
	Slug    string
	Prayers []PrayerPage
}

type PrayerPage struct {
	Phelps       string
	HTML         template.HTML
	Translations string // "Also in: en, fr, de" or ""
	PhelpsLink   string // URL to inventory for this code
}

type PageData struct {
	Title      string
	Lang       string
	LangName   string
	Dir        string
	IndentSide string
	Count      int
	Categories []CategorySection
	HasCats    bool
}

// HTML document template
const htmlTmpl = `<!DOCTYPE html>
<html lang="{{.Lang}}" dir="{{.Dir}}">
<head>
<meta charset="UTF-8">
<title>{{.Title}}</title>
<style>
  @page {
    size: A4;
    margin: 2.5cm 3cm 2.5cm 3cm;
    @bottom-center {
      content: counter(page);
      font-family: serif;
      font-size: 9pt;
      color: #666;
    }
  }
  body {
    font-family: "Noto Serif", "Linux Libertine O", "DejaVu Serif", serif;
    font-size: 11pt;
    line-height: 1.7;
    color: #1a1a1a;
    background: white;
    direction: {{.Dir}};
  }
  .title-page {
    page-break-after: always;
    text-align: center;
    padding-top: 8cm;
    padding-bottom: 8cm;
  }
  .title-page h1 {
    font-size: 28pt;
    font-weight: normal;
    letter-spacing: 0.05em;
    margin-bottom: 0.5em;
    color: #2c3e50;
  }
  .title-page .subtitle {
    font-size: 14pt;
    color: #7f8c8d;
    font-style: italic;
  }
  .title-page .stats {
    margin-top: 2em;
    font-size: 10pt;
    color: #95a5a6;
  }
  .toc {
    page-break-after: always;
    padding-top: 1cm;
  }
  .toc h2 {
    font-size: 16pt;
    font-weight: normal;
    border-bottom: 1px solid #ccc;
    padding-bottom: 0.3em;
    margin-bottom: 1em;
    color: #2c3e50;
  }
  .toc ol {
    list-style: none;
    padding: 0;
    margin: 0;
  }
  .toc li {
    padding: 0.15em 0;
    font-size: 10pt;
    border-bottom: 1px dotted #ddd;
  }
  .toc a {
    color: #2c3e50;
    text-decoration: none;
  }
  .category-section {
    margin-top: 2em;
  }
  h1.category-header {
    font-size: 16pt;
    font-weight: normal;
    color: #2c3e50;
    border-bottom: 2px solid #2c3e50;
    padding-bottom: 0.2em;
    margin-top: 2.5em;
    margin-bottom: 1.2em;
    page-break-after: avoid;
  }
  .prayer {
    page-break-inside: avoid;
    break-inside: avoid;
    margin-bottom: 2.5em;
    padding-bottom: 1.5em;
    border-bottom: 1px solid #e8e8e8;
  }
  .prayer:last-child {
    border-bottom: none;
  }
  .prayer-meta {
    font-size: 8pt;
    color: #aaa;
    margin-bottom: 0.3em;
    font-family: "DejaVu Sans Mono", monospace;
  }
  .prayer-header {
    font-size: 10pt;
    font-style: italic;
    color: #666;
    margin: 0 0 0.3em 0;
    font-weight: normal;
  }
  .prayer-sub-header {
    font-size: 10pt;
    color: #888;
    margin: 0 0 0.3em 0;
    font-weight: normal;
  }
  p {
    margin: 0 0 0.6em 0;
  }
  p.verse {
    margin-{{.IndentSide}}: 1.5em;
    font-style: italic;
    color: #333;
  }
  p.note {
    font-size: 9pt;
    color: #666;
    margin-{{.IndentSide}}: 1em;
  }
  .trans-note {
    font-size: 8pt;
    color: #bbb;
    margin-top: 0.3em;
    font-style: italic;
  }
</style>
</head>
<body>

<div class="title-page">
  <h1>{{.Title}}</h1>
  {{if .LangName}}<div class="subtitle">{{.LangName}}</div>{{end}}
  <div class="stats">{{.Count}} prayers · phelps.io inventory</div>
</div>

{{if .HasCats}}
<div class="toc">
  <h2>Contents</h2>
  <ol>
    {{range .Categories}}{{if .Name}}<li><a href="#cat-{{.Slug}}">{{.Name}}</a></li>
    {{end}}{{end}}
  </ol>
</div>
{{end}}

{{range .Categories}}
<div class="category-section">
  {{if .Name}}<h1 class="category-header" id="cat-{{.Slug}}">{{.Name}}</h1>{{end}}
  {{range .Prayers}}
  <div class="prayer">
    <div class="prayer-meta">{{.Phelps}}{{if .PhelpsLink}} · <a href="{{.PhelpsLink}}">↗</a>{{end}}</div>
    {{.HTML}}
    {{if .Translations}}<p class="trans-note">{{.Translations}}</p>{{end}}
  </div>
  {{end}}
</div>
{{end}}

</body>
</html>
`

// IndexEntry represents one row in the first-lines concordance
type IndexEntry struct {
	Phelps    string
	FirstLine string
	LangCount int
}

const indexTmpl = `<!DOCTYPE html>
<html lang="en" dir="ltr">
<head>
<meta charset="UTF-8">
<title>{{.Title}}</title>
<style>
  @page {
    size: A4;
    margin: 2cm 2cm 2cm 2cm;
    @bottom-center {
      content: counter(page);
      font-family: serif;
      font-size: 8pt;
      color: #999;
    }
  }
  body {
    font-family: "Noto Sans", "DejaVu Sans", sans-serif;
    font-size: 8.5pt;
    line-height: 1.4;
    color: #1a1a1a;
  }
  h1 { font-size: 18pt; font-weight: normal; text-align: center; margin-bottom: 0.3em; color: #2c3e50; }
  .subtitle { text-align: center; color: #7f8c8d; font-size: 10pt; margin-bottom: 2em; }
  table { width: 100%; border-collapse: collapse; }
  thead th { background: #2c3e50; color: white; padding: 4px 6px; text-align: left; font-size: 8pt; }
  tr:nth-child(even) { background: #f8f8f8; }
  td { padding: 3px 6px; border-bottom: 1px solid #eee; vertical-align: top; }
  td.phelps { font-family: "DejaVu Sans Mono", monospace; font-size: 7.5pt; color: #555; white-space: nowrap; width: 9em; }
  td.langs { text-align: center; color: #888; width: 2.5em; font-size: 7.5pt; }
</style>
</head>
<body>
<h1>{{.Title}}</h1>
<div class="subtitle">{{.Count}} prayers — first-lines concordance</div>
<table>
  <thead><tr><th>Phelps</th><th>First line (English)</th><th title="Languages">L</th></tr></thead>
  <tbody>
  {{range .Entries}}
  <tr>
    <td class="phelps">{{.Phelps}}</td>
    <td>{{.FirstLine}}</td>
    <td class="langs">{{.LangCount}}</td>
  </tr>
  {{end}}
  </tbody>
</table>
</body>
</html>
`

type IndexData struct {
	Title   string
	Count   int
	Entries []IndexEntry
}

// firstLine returns the first meaningful text line from a prayer text (strips headers)
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

func generateIndex(dbPath, source, title string) string {
	qCount := fmt.Sprintf(
		"SELECT phelps, COUNT(DISTINCT language) as lang_count FROM writings WHERE source='%s' AND phelps IS NOT NULL AND phelps <> '' GROUP BY phelps ORDER BY phelps",
		source)
	countRows := doltCSV(dbPath, qCount)

	qEn := fmt.Sprintf(
		"SELECT phelps, text FROM writings WHERE source='%s' AND language='en' AND phelps IS NOT NULL AND phelps <> '' ORDER BY phelps",
		source)
	enRows := doltCSV(dbPath, qEn)
	enText := make(map[string]string, len(enRows))
	for _, row := range enRows {
		if len(row) < 2 {
			continue
		}
		if _, exists := enText[row[0]]; !exists {
			enText[row[0]] = row[1]
		}
	}

	var entries []IndexEntry
	for _, row := range countRows {
		if len(row) < 2 {
			continue
		}
		lc := 0
		fmt.Sscanf(row[1], "%d", &lc)
		entries = append(entries, IndexEntry{
			Phelps:    row[0],
			FirstLine: firstLine(enText[row[0]]),
			LangCount: lc,
		})
	}

	data := IndexData{Title: title, Count: len(entries), Entries: entries}
	tmpl := template.Must(template.New("index").Parse(indexTmpl))
	var buf strings.Builder
	if err := tmpl.Execute(&buf, data); err != nil {
		fmt.Fprintf(os.Stderr, "index template error: %v\n", err)
		os.Exit(1)
	}
	return buf.String()
}

// queryTranslations builds a map of phelps code → list of other language codes
func queryTranslations(dbPath, source string) map[string][]string {
	rows := doltCSV(dbPath, fmt.Sprintf(
		"SELECT phelps, language FROM writings WHERE source='%s' AND phelps IS NOT NULL AND phelps <> '' ORDER BY phelps, language",
		source))
	m := map[string][]string{}
	for _, row := range rows {
		if len(row) < 2 {
			continue
		}
		if !strings.HasSuffix(row[1], "-translit") {
			m[row[0]] = append(m[row[0]], row[1])
		}
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
	rows := doltCSV(dbPath, fmt.Sprintf(
		"SELECT name FROM languages WHERE langcode = '%s' LIMIT 1", lang))
	if len(rows) == 0 || len(rows[0]) == 0 {
		return ""
	}
	return rows[0][0]
}

// basePINKey strips trailing 3-char alpha mnemonic suffix
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

func generateHTML(prayers []Prayer, lang, title, lname, phelpsBaseURL string, translations map[string][]string) string {
	dir := "ltr"
	indentSide := "left"
	if rtlLangs[lang] {
		dir = "rtl"
		indentSide = "right"
	}

	// Group prayers into category sections
	var categories []CategorySection
	catIdx := map[string]int{} // category name → index in categories
	uncatKey := "\x00" // sentinel for uncategorized

	for _, p := range prayers {
		catName := p.Category
		key := catName
		if catName == "" {
			key = uncatKey
		}
		if _, seen := catIdx[key]; !seen {
			catIdx[key] = len(categories)
			if catName == "" {
				categories = append(categories, CategorySection{Name: "", Slug: ""})
			} else {
				categories = append(categories, CategorySection{
					Name: catName,
					Slug: slugify(catName),
				})
			}
		}
		idx := catIdx[key]

		// Build translations note (exclude current language)
		var transLangs []string
		if langs, ok := translations[p.Phelps]; ok {
			for _, l := range langs {
				if l != lang {
					transLangs = append(transLangs, l)
				}
			}
		}
		transNote := ""
		if len(transLangs) > 0 {
			transNote = "Also in: " + strings.Join(transLangs, ", ")
		}

		// Build phelps inventory link
		phelpsLink := ""
		if phelpsBaseURL != "" {
			base := strings.ToLower(basePINKey(p.Phelps))
			phelpsLink = phelpsBaseURL + base + "/"
		}

		categories[idx].Prayers = append(categories[idx].Prayers, PrayerPage{
			Phelps:       p.Phelps,
			HTML:         markdownToHTML(p.Text),
			Translations: transNote,
			PhelpsLink:   phelpsLink,
		})
	}

	// Check if we have any real category names (for ToC)
	hasCats := false
	for _, c := range categories {
		if c.Name != "" {
			hasCats = true
			break
		}
	}

	data := struct {
		Title      string
		Lang       string
		LangName   string
		Dir        string
		IndentSide string
		Count      int
		Categories []CategorySection
		HasCats    bool
	}{
		Title:      title,
		Lang:       lang,
		LangName:   lname,
		Dir:        dir,
		IndentSide: indentSide,
		Count:      len(prayers),
		Categories: categories,
		HasCats:    hasCats,
	}

	tmpl := template.Must(template.New("page").Parse(htmlTmpl))
	var buf strings.Builder
	if err := tmpl.Execute(&buf, data); err != nil {
		fmt.Fprintf(os.Stderr, "template error: %v\n", err)
		os.Exit(1)
	}
	return buf.String()
}

func renderPDF(htmlContent, outFile, tmpTag string) {
	tmpFile := fmt.Sprintf("/tmp/prayers_%s.html", tmpTag)
	if err := os.WriteFile(tmpFile, []byte(htmlContent), 0644); err != nil {
		fmt.Fprintf(os.Stderr, "Temp write error: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("  Converting to PDF...\n")
	cmd := exec.Command("weasyprint", tmpFile, outFile)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		fmt.Fprintf(os.Stderr, "  weasyprint error: %v\n", err)
		os.Remove(tmpFile)
		os.Exit(1)
	}
	os.Remove(tmpFile)
	fmt.Printf("  Written: %s\n", outFile)
}

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
		fmt.Fprintf(os.Stderr, "  pandoc error: %v\n", err)
		os.Remove(tmpFile)
		return // don't exit — EPUB is optional
	}
	os.Remove(tmpFile)
	fmt.Printf("  Written: %s\n", outFile)
}

func renderHTML(htmlContent, outFile string) {
	if err := os.WriteFile(outFile, []byte(htmlContent), 0644); err != nil {
		fmt.Fprintf(os.Stderr, "Write error: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("  Written: %s\n", outFile)
}

func main() {
	home, _ := os.UserHomeDir()
	defaultDB := filepath.Join(home, "prayermatching", "bahaiwritings")

	db          := flag.String("db", defaultDB, "Path to dolt repo")
	lang        := flag.String("lang", "en", "Language code (or 'all')")
	source      := flag.String("source", "bahaiprayers.net", "Prayer source")
	output      := flag.String("output", "", "Output file")
	outDir      := flag.String("out-dir", "", "Output directory (used with --lang all)")
	htmlOnly    := flag.Bool("html-only", false, "Output HTML only")
	epubMode    := flag.Bool("epub", false, "Generate EPUB via pandoc")
	bothMode    := flag.Bool("both", false, "Generate both PDF and EPUB")
	indexMode   := flag.Bool("index", false, "Generate first-lines concordance index")
	title       := flag.String("title", "Bahá'í Prayers", "Document title")
	phelpsBase  := flag.String("phelps-base-url", "", "Base URL for phelps inventory links")
	flag.Parse()

	// Index mode
	if *indexMode {
		indexTitle := *title + " — First Lines Index"
		htmlContent := generateIndex(*db, *source, indexTitle)
		outFile := *output
		if outFile == "" {
			if *htmlOnly {
				outFile = "prayers_index.html"
			} else {
				outFile = "prayers_index.pdf"
			}
		}
		if *htmlOnly {
			renderHTML(htmlContent, outFile)
		} else {
			renderPDF(htmlContent, outFile, "index")
		}
		return
	}

	// Resolve languages to process
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

	// Pre-load all translations (one query for all languages)
	fmt.Println("Loading translation index...")
	translationsMap := queryTranslations(*db, *source)

	// Determine output directory
	dir := *outDir
	if dir == "" {
		dir = "."
	}
	if err := os.MkdirAll(dir, 0755); err != nil {
		fmt.Fprintf(os.Stderr, "mkdir error: %v\n", err)
		os.Exit(1)
	}

	for _, l := range langs {
		fmt.Printf("Fetching %s prayers...\n", l)

		prayers := queryPrayers(*db, *source, l)
		if len(prayers) == 0 {
			fmt.Printf("  No resolved prayers for %s, skipping.\n", l)
			continue
		}
		fmt.Printf("  %d prayers\n", len(prayers))

		lname := langName(*db, l)
		docTitle := *title
		if lname != "" {
			docTitle = *title + " — " + lname
		}

		htmlContent := generateHTML(prayers, l, docTitle, lname, *phelpsBase, translationsMap)

		// Determine output file base (without extension)
		var baseName string
		if *output != "" && len(langs) == 1 {
			// Single language with explicit output: use as-is (strip any extension)
			baseName = strings.TrimSuffix(*output, filepath.Ext(*output))
		} else {
			baseName = filepath.Join(dir, "prayers_"+l)
		}

		if *htmlOnly {
			renderHTML(htmlContent, baseName+".html")
		} else if *epubMode {
			renderEPUB(htmlContent, baseName+".epub", l, docTitle, l)
		} else if *bothMode {
			renderPDF(htmlContent, baseName+".pdf", l)
			renderEPUB(htmlContent, baseName+".epub", l, docTitle, l)
		} else {
			renderPDF(htmlContent, baseName+".pdf", l)
		}
	}
}
