// scrape_bpapp.go — scrape bahaiprayers.app to build PBS rows for <lang>:bpapp.
//
// Usage:
//   go run scripts/scrape_bpapp.go --lang en --out /tmp/bpapp_en.sql
//   go run scripts/scrape_bpapp.go --lang en --dry-run            # scan only
//
// Output: SQL with REPLACE INTO languages and INSERT INTO prayer_book_structure.
// The script does NOT touch the database; review SQL before applying.
//
// Phelps mapping strategy:
//   1. Look up the bpapp source_id directly in writings (if already imported,
//      we know its phelps).
//   2. If not found, search writings of the same language for a near-match
//      on the first line of text. Conservative — leaves unmatched as TMP.
//
// Rate-limited to 1 req/200ms. Saves intermediate state per category so a
// killed run can resume by re-running.

package main

import (
	"bytes"
	"flag"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"
)

const (
	baseURL    = "https://bahaiprayers.app"
	userAgent  = "holywritings.net bpapp-scraper / contact joop@kiefte.net"
	throttleMs = 200
)

var (
	flagLang   = flag.String("lang", "", "Language code on bahaiprayers.app (en, fr, eo, …)")
	flagOut    = flag.String("out", "", "SQL output file (default: /tmp/bpapp_<lang>.sql)")
	flagDolt   = flag.String("dolt-dir", filepath.Join(os.Getenv("HOME"), "bahaiwritings"), "Dolt repo for phelps lookup")
	flagDry    = flag.Bool("dry-run", false, "Scan and report only, don't write SQL")
	flagSrcBase = flag.Int("src-base", 20000, "Starting source_id for new PBS rows (must not collide)")
)

// Polite HTTP client with throttling.
var (
	httpClient = &http.Client{Timeout: 30 * time.Second}
	lastFetch  time.Time
)

func fetch(u string) ([]byte, error) {
	wait := time.Until(lastFetch.Add(throttleMs * time.Millisecond))
	if wait > 0 {
		time.Sleep(wait)
	}
	lastFetch = time.Now()
	req, err := http.NewRequest("GET", u, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", userAgent)
	resp, err := httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("HTTP %d for %s", resp.StatusCode, u)
	}
	return io.ReadAll(resp.Body)
}

// ── Parsing helpers ────────────────────────────────────────────────────

// Extract category links from the homepage `/?l=<lang>`. Each category link
// is /category?id=<N>&l=<lang> with the category name as the link text.
var (
	// Categories: <a class="" href="/category?id=ID&amp;l=LANG"> <div>NAME</div>
	// Capture the id, then find the inner <div>NAME</div>.
	reCategoryLink = regexp.MustCompile(`href="/category\?id=(\d+)[^"]*">\s*<div[^>]*>([^<]+)</div>`)
	rePrayerLink   = regexp.MustCompile(`<a\s+href="/prayer\?id=(\d+)[^"]*">`)
	rePrayerStart  = regexp.MustCompile(`<span\s+class="prayerItemPrayerText"[^>]*>([^<]+)</span>`)
	rePrayerName   = regexp.MustCompile(`<span\s+class="prayerItemPrayerTitle"[^>]*>([^<]+)</span>`)
	// On a single-prayer page, the body sits inside <div id="prayer">…</div>
	rePrayerBody   = regexp.MustCompile(`<div\s+id="prayer"[^>]*>([\s\S]*?)</div>\s*<div\s+id="`)
	reTagStrip     = regexp.MustCompile(`<[^>]+>`)
	reWS           = regexp.MustCompile(`\s+`)
)

type Category struct {
	ID   string
	Name string
	Ord  int // position on the homepage
}

type PrayerEntry struct {
	ID           string // bpapp source_id, e.g. "213000"
	StartText    string // first line as shown in the list
	Name         string // optional title
	Phelps       string // resolved phelps code, "" if unmatched
	OrderInCat   int
}

func parseCategories(body []byte) []Category {
	matches := reCategoryLink.FindAllSubmatch(body, -1)
	seen := map[string]bool{}
	cats := []Category{}
	for _, m := range matches {
		id := string(m[1])
		name := strings.TrimSpace(decodeHTML(string(m[2])))
		if seen[id] || name == "" {
			continue
		}
		seen[id] = true
		cats = append(cats, Category{ID: id, Name: name, Ord: len(cats) + 1})
	}
	return cats
}

func parsePrayerList(body []byte) []PrayerEntry {
	matches := rePrayerLink.FindAllSubmatch(body, -1)
	prayers := []PrayerEntry{}
	for i, m := range matches {
		prayers = append(prayers, PrayerEntry{
			ID:         string(m[1]),
			OrderInCat: i + 1,
		})
	}
	// Try to grab start-text for each prayer; matched in document order.
	starts := rePrayerStart.FindAllSubmatch(body, -1)
	for i, s := range starts {
		if i < len(prayers) {
			prayers[i].StartText = stripTags(decodeHTML(string(s[1])))
		}
	}
	return prayers
}

func stripTags(s string) string {
	s = reTagStrip.ReplaceAllString(s, " ")
	return strings.TrimSpace(reWS.ReplaceAllString(s, " "))
}

// decodeHTML handles the entities the bpapp HTML uses (&amp; &#x27; &#xE1; etc).
func decodeHTML(s string) string {
	r := strings.NewReplacer(
		"&amp;", "&",
		"&lt;", "<",
		"&gt;", ">",
		"&quot;", "\"",
		"&#39;", "'", "&#x27;", "'", "&apos;", "'",
		"&nbsp;", " ",
	)
	s = r.Replace(s)
	// Numeric entities (decimal + hex)
	reNum := regexp.MustCompile(`&#(x?[0-9a-fA-F]+);`)
	s = reNum.ReplaceAllStringFunc(s, func(m string) string {
		ent := m[2 : len(m)-1]
		var n int
		if strings.HasPrefix(ent, "x") || strings.HasPrefix(ent, "X") {
			fmt.Sscanf(ent[1:], "%x", &n)
		} else {
			fmt.Sscanf(ent, "%d", &n)
		}
		if n > 0 && n <= 0x10FFFF {
			return string(rune(n))
		}
		return m
	})
	return s
}

// ── Phelps lookup (via Dolt) ───────────────────────────────────────────

// existingMappings returns source_id → phelps for bpapp rows already in the DB.
func existingMappings(lang string) map[string]string {
	out := runDolt(`SELECT source_id, phelps FROM writings WHERE source='bahaiprayers.app' AND language=? AND phelps IS NOT NULL`, lang)
	m := map[string]string{}
	for _, row := range out {
		if len(row) >= 2 {
			m[row[0]] = row[1]
		}
	}
	return m
}

// firstLineIndex returns a normalized-first-line → []phelps map for the given
// language, drawn from all writings rows with a phelps. Used as a fallback when
// we can't directly find the bpapp source_id in the writings table.
func firstLineIndex(lang string) map[string][]string {
	out := runDolt(`SELECT phelps, SUBSTRING(text, 1, 200) FROM writings WHERE language=? AND phelps IS NOT NULL AND phelps NOT LIKE 'TMP%'`, lang)
	idx := map[string][]string{}
	for _, row := range out {
		if len(row) < 2 {
			continue
		}
		key := normalizeForMatch(row[1])
		if key == "" {
			continue
		}
		idx[key] = append(idx[key], row[0])
	}
	return idx
}

// normalizeForMatch strips markdown headers, punctuation, casing, and limits
// to first ~10 words for fuzzy first-line matching.
func normalizeForMatch(s string) string {
	// drop markdown headers
	s = regexp.MustCompile(`(?m)^#+\s.*$`).ReplaceAllString(s, "")
	// drop common preambles
	s = regexp.MustCompile(`(?i)^\s*(li|he|il|er)\s+estas\s+dio[.\s]*`).ReplaceAllString(s, "")
	s = strings.ToLower(s)
	s = regexp.MustCompile(`[^\p{L}\p{N}\s]`).ReplaceAllString(s, " ")
	s = reWS.ReplaceAllString(s, " ")
	s = strings.TrimSpace(s)
	words := strings.Fields(s)
	if len(words) > 10 {
		words = words[:10]
	}
	return strings.Join(words, " ")
}

func resolvePhelps(p PrayerEntry, exact map[string]string, fuzzy map[string][]string) string {
	if got, ok := exact[p.ID]; ok && got != "" {
		return got
	}
	key := normalizeForMatch(p.StartText)
	if key == "" {
		return ""
	}
	// Try a prefix-match: index entries that START with the bpapp first-line key.
	// (The bpapp start-text is usually shorter than the full first line.)
	for stored, codes := range fuzzy {
		if strings.HasPrefix(stored, key) || strings.HasPrefix(key, stored) {
			if len(codes) == 1 {
				return codes[0]
			}
		}
	}
	return ""
}

// ── Dolt shell-out ────────────────────────────────────────────────────

func runDolt(query string, args ...string) [][]string {
	full := query
	for _, a := range args {
		full = strings.Replace(full, "?", "'"+sqlEsc(a)+"'", 1)
	}
	cmd := exec.Command("dolt", "sql", "-q", full, "-r", "csv")
	cmd.Dir = *flagDolt
	out, err := cmd.Output()
	if err != nil {
		log.Fatalf("dolt sql: %v\nQuery: %s", err, full)
	}
	lines := bytes.Split(bytes.TrimRight(out, "\n"), []byte("\n"))
	if len(lines) <= 1 {
		return nil
	}
	rows := make([][]string, 0, len(lines)-1)
	for _, ln := range lines[1:] { // skip CSV header
		fields := splitCSV(string(ln))
		rows = append(rows, fields)
	}
	return rows
}

// splitCSV — minimal CSV row parser; handles quoted fields with commas.
func splitCSV(line string) []string {
	var out []string
	var cur strings.Builder
	inQ := false
	for i := 0; i < len(line); i++ {
		c := line[i]
		switch {
		case c == '"' && inQ && i+1 < len(line) && line[i+1] == '"':
			cur.WriteByte('"')
			i++
		case c == '"':
			inQ = !inQ
		case c == ',' && !inQ:
			out = append(out, cur.String())
			cur.Reset()
		default:
			cur.WriteByte(c)
		}
	}
	out = append(out, cur.String())
	return out
}

func sqlEsc(s string) string { return strings.ReplaceAll(s, "'", "''") }

// ── Main ───────────────────────────────────────────────────────────────

func main() {
	flag.Parse()
	if *flagLang == "" {
		log.Fatal("--lang required")
	}
	if *flagOut == "" {
		*flagOut = filepath.Join("/tmp", "bpapp_"+*flagLang+".sql")
	}

	bookCode := *flagLang + ":bpapp"
	bookName := "Bahá'í Prayers App (" + *flagLang + ")"

	log.Printf("Scraping %s for %s …", baseURL, bookCode)

	homeBody, err := fetch(baseURL + "/?l=" + *flagLang)
	if err != nil {
		log.Fatalf("homepage: %v", err)
	}
	cats := parseCategories(homeBody)
	log.Printf("  %d categories", len(cats))
	if len(cats) == 0 {
		log.Fatal("no categories parsed; did the HTML structure change?")
	}

	exact := existingMappings(*flagLang)
	fuzzy := firstLineIndex(*flagLang)
	log.Printf("  %d existing bpapp→phelps mappings, %d fuzzy first-line keys", len(exact), len(fuzzy))

	type pbsRow struct {
		sourceID   int
		phelps     string
		category   string
		catOrder   int
		ordInCat   int
		bpappID    string
		startText  string
	}
	var rows []pbsRow
	srcID := *flagSrcBase
	mapped, unmapped := 0, 0
	for _, cat := range cats {
		body, err := fetch(baseURL + "/category?id=" + cat.ID + "&l=" + *flagLang)
		if err != nil {
			log.Printf("  cat %s (%s): %v", cat.ID, cat.Name, err)
			continue
		}
		prayers := parsePrayerList(body)
		log.Printf("  [%2d] %s (%d prayers)", cat.Ord, cat.Name, len(prayers))
		for _, p := range prayers {
			ph := resolvePhelps(p, exact, fuzzy)
			if ph != "" {
				mapped++
			} else {
				unmapped++
			}
			rows = append(rows, pbsRow{
				sourceID:  srcID,
				phelps:    ph,
				category:  cat.Name,
				catOrder:  cat.Ord,
				ordInCat:  p.OrderInCat,
				bpappID:   p.ID,
				startText: p.StartText,
			})
			srcID++
		}
	}

	log.Printf("Total %d prayers; %d mapped, %d unmapped", len(rows), mapped, unmapped)

	if *flagDry {
		log.Printf("Dry run — not writing SQL")
		// Show unmapped sample
		shown := 0
		for _, r := range rows {
			if r.phelps == "" && shown < 10 {
				log.Printf("  UNMAPPED bpapp=%s [%s] %.60s", r.bpappID, r.category, r.startText)
				shown++
			}
		}
		return
	}

	f, err := os.Create(*flagOut)
	if err != nil {
		log.Fatal(err)
	}
	defer f.Close()
	w := bufWriter{f}

	w.printf("-- bahaiprayers.app PBS for %s\n", bookCode)
	w.printf("-- Generated: %s\n", time.Now().Format(time.RFC3339))
	w.printf("-- Source crawl: %s/?l=%s\n", baseURL, *flagLang)
	w.printf("-- %d prayers; %d mapped to phelps, %d unmapped (skipped)\n--\n", len(rows), mapped, unmapped)
	w.printf("REPLACE INTO languages (langcode, inlang, name) VALUES ('%s', 'en', '%s');\n\n",
		bookCode, sqlEsc(bookName))
	w.printf("SET FOREIGN_KEY_CHECKS=0;\n\n")

	// Sort by source_id for deterministic output.
	sort.Slice(rows, func(i, j int) bool { return rows[i].sourceID < rows[j].sourceID })

	for _, r := range rows {
		if r.phelps == "" {
			w.printf("-- SKIPPED bpapp=%s [%s ord=%d] %s\n",
				r.bpappID, r.category, r.ordInCat, truncate(r.startText, 80))
			continue
		}
		w.printf("INSERT INTO prayer_book_structure "+
			"(source_id, source_language, version, phelps_code, "+
			"category_name, category_order, order_in_category, notes) VALUES "+
			"(%d, '%s', 'bpapp', '%s', '%s', %d, %d, 'bpapp_id=%s; %s');\n",
			r.sourceID, bookCode, r.phelps, sqlEsc(r.category),
			r.catOrder, r.ordInCat, r.bpappID, sqlEsc(truncate(r.startText, 60)))
	}
	w.printf("\nSET FOREIGN_KEY_CHECKS=1;\n")
	w.printf("\n-- Apply: grep -E '^(REPLACE|INSERT|SET)' %s | dolt sql\n", *flagOut)
	log.Printf("Wrote %s", *flagOut)
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}

type bufWriter struct{ io.Writer }

func (b bufWriter) printf(f string, args ...any) {
	fmt.Fprintf(b.Writer, f, args...)
}
