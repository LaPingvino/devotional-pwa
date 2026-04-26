// scrape_bpapp.go — two-phase tool for ingesting bahaiprayers.app.
//
// PHASE 1 (--phase fetch): Crawl the site for one language, save raw prayer
// data to bpapp_cache/<lang>/. Polite throttle, resumable (skips files
// already on disk). Run overnight per language.
//
// PHASE 2 (--phase match): Read the cache, fuzzy-match each prayer against
// the local writings table by text content. Emit:
//   - bpapp_<lang>.sql        — high-confidence PBS inserts ready to apply
//   - bpapp_<lang>_review.tsv — uncertain cases for manual curation
//                               (bpapp_id | category | title | text_first_200
//                                | candidate_phelps | score | …)
//
// The two phases are independent: re-run match as often as you want without
// hitting the network. Refining the matching strategy is purely local work.
//
// Usage:
//   go run scripts/scrape_bpapp.go --phase fetch --lang en
//   go run scripts/scrape_bpapp.go --phase match --lang en
//   go run scripts/scrape_bpapp.go --phase match --lang en --out /tmp/eo.sql
//
// Cache dir defaults to bpapp_cache/ in the working dir.

package main

import (
	"bytes"
	"encoding/json"
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
	flagPhase    = flag.String("phase", "", "fetch | match")
	flagLang     = flag.String("lang", "", "Language code on bahaiprayers.app")
	flagCacheDir = flag.String("cache-dir", "bpapp_cache", "Where prayer JSONs are cached")
	flagOut      = flag.String("out", "", "Output SQL path (default: bpapp_<lang>.sql)")
	flagReview   = flag.String("review", "", "Review TSV path (default: bpapp_<lang>_review.tsv)")
	flagDolt     = flag.String("dolt-dir", filepath.Join(os.Getenv("HOME"), "bahaiwritings"), "Dolt repo")
	flagSrcBase  = flag.Int("src-base", 20000, "Starting source_id for new PBS rows")
	flagMinScore = flag.Float64("min-score", 0.85, "Confidence threshold for auto-mapping (0-1)")
	flagForce    = flag.Bool("force", false, "Re-fetch cached pages")
)

// ── HTTP with throttle ────────────────────────────────────────────────

var (
	httpClient = &http.Client{Timeout: 30 * time.Second}
	lastFetch  time.Time
)

func httpGet(u string) ([]byte, error) {
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

// ── Parsing ────────────────────────────────────────────────────────────

var (
	reCategoryLink = regexp.MustCompile(`href="/category\?id=(\d+)[^"]*">\s*<div[^>]*>([^<]+)</div>`)
	rePrayerLink   = regexp.MustCompile(`<a\s+href="/prayer\?id=(\d+)[^"]*">`)
	rePrayerStart  = regexp.MustCompile(`<span\s+class="prayerItemPrayerText"[^>]*>([^<]+)</span>`)
	rePrayerName   = regexp.MustCompile(`<span\s+class="prayerItemPrayerTitle"[^>]*>([^<]+)</span>`)
	// Single-prayer page: full text lives inside <div id="prayer">…</div>.
	// The single-prayer page wraps text in <div id="prayer">…</div>. The
	// next sibling varies (sometimes <div id="prayer-translation*">, sometimes
	// just </div><footer>…). Capture greedily-but-stopping-at-footer so we
	// don't accidentally swallow upsell banners.
	rePrayerBody = regexp.MustCompile(`(?s)<div\s+id="prayer"[^>]*>(.*?)</div>\s*(?:</div>\s*)?(?:<footer|<div\s+id="prayer-translation|<div\s+class="appUpsell)`)
	rePageTitle  = regexp.MustCompile(`(?s)<h1[^>]*>(.*?)</h1>`)
	reTagStrip   = regexp.MustCompile(`<[^>]+>`)
	reWS         = regexp.MustCompile(`\s+`)
)

type Category struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	Ord  int    `json:"ord"`
}

type CachedPrayer struct {
	BpappID    string `json:"bpapp_id"`
	CategoryID string `json:"cat_id"`
	CategoryNm string `json:"cat_name"`
	CatOrder   int    `json:"cat_order"`
	OrderInCat int    `json:"ord_in_cat"`
	Title      string `json:"title"`
	StartText  string `json:"start_text"`
	FullText   string `json:"full_text"`
	SourceURL  string `json:"source_url"`
	FetchedAt  string `json:"fetched_at"`
}

func stripTags(s string) string {
	s = reTagStrip.ReplaceAllString(s, " ")
	return strings.TrimSpace(reWS.ReplaceAllString(s, " "))
}

func decodeHTML(s string) string {
	r := strings.NewReplacer(
		"&amp;", "&", "&lt;", "<", "&gt;", ">", "&quot;", "\"",
		"&#39;", "'", "&#x27;", "'", "&apos;", "'", "&nbsp;", " ",
	)
	s = r.Replace(s)
	reNum := regexp.MustCompile(`&#(x?[0-9a-fA-F]+);`)
	return reNum.ReplaceAllStringFunc(s, func(m string) string {
		ent := m[2 : len(m)-1]
		var n int
		if strings.HasPrefix(strings.ToLower(ent), "x") {
			fmt.Sscanf(ent[1:], "%x", &n)
		} else {
			fmt.Sscanf(ent, "%d", &n)
		}
		if n > 0 && n <= 0x10FFFF {
			return string(rune(n))
		}
		return m
	})
}

// ── PHASE 1: fetch ────────────────────────────────────────────────────

func phaseFetch() {
	langDir := filepath.Join(*flagCacheDir, *flagLang)
	must(os.MkdirAll(langDir, 0755))

	// Homepage → categories
	homePath := filepath.Join(langDir, "homepage.html")
	homeBody, err := readOrFetch(homePath, baseURL+"/?l="+*flagLang)
	if err != nil {
		log.Fatalf("homepage: %v", err)
	}
	cats := parseCategories(homeBody)
	if len(cats) == 0 {
		log.Fatal("no categories parsed; HTML structure may have changed")
	}
	log.Printf("Found %d categories for %s", len(cats), *flagLang)
	must(writeJSON(filepath.Join(langDir, "categories.json"), cats))

	// Walk each category, collect prayer ids in order
	type catList struct {
		Cat       Category   `json:"category"`
		PrayerIDs []string   `json:"prayer_ids"`
		Starts    []string   `json:"starts"` // first-line text per prayer (for sanity check)
		Titles    []string   `json:"titles"`
	}
	allCats := []catList{}
	allPrayerIDs := map[string]Category{}
	prayerOrders := map[string]int{}
	prayerStarts := map[string]string{}
	prayerTitles := map[string]string{}

	for _, cat := range cats {
		catBody, err := readOrFetch(
			filepath.Join(langDir, "cat_"+cat.ID+".html"),
			fmt.Sprintf("%s/category?id=%s&l=%s", baseURL, cat.ID, *flagLang),
		)
		if err != nil {
			log.Printf("  cat %s: %v", cat.ID, err)
			continue
		}
		ids, starts, titles := parsePrayerList(catBody)
		log.Printf("  [%2d] %-40.40s — %d prayers", cat.Ord, cat.Name, len(ids))
		cl := catList{Cat: cat, PrayerIDs: ids, Starts: starts, Titles: titles}
		allCats = append(allCats, cl)
		for i, pid := range ids {
			if _, seen := allPrayerIDs[pid]; !seen {
				allPrayerIDs[pid] = cat
				prayerOrders[pid] = i + 1
				if i < len(starts) {
					prayerStarts[pid] = starts[i]
				}
				if i < len(titles) {
					prayerTitles[pid] = titles[i]
				}
			}
		}
	}
	must(writeJSON(filepath.Join(langDir, "structure.json"), allCats))
	log.Printf("Total %d unique prayer IDs", len(allPrayerIDs))

	// Fetch full text for each prayer (one request per prayer)
	fetched, skipped, errs := 0, 0, 0
	ids := make([]string, 0, len(allPrayerIDs))
	for id := range allPrayerIDs {
		ids = append(ids, id)
	}
	sort.Strings(ids)

	for i, pid := range ids {
		path := filepath.Join(langDir, "prayer_"+pid+".json")
		if !*flagForce {
			if _, err := os.Stat(path); err == nil {
				skipped++
				continue
			}
		}
		url := fmt.Sprintf("%s/prayer?id=%s&l=%s", baseURL, pid, *flagLang)
		body, err := httpGet(url)
		if err != nil {
			log.Printf("  prayer %s: %v", pid, err)
			errs++
			continue
		}
		fullText := extractPrayerBody(body)
		title := extractPageTitle(body)
		cat := allPrayerIDs[pid]
		cp := CachedPrayer{
			BpappID:    pid,
			CategoryID: cat.ID,
			CategoryNm: cat.Name,
			CatOrder:   cat.Ord,
			OrderInCat: prayerOrders[pid],
			Title:      title,
			StartText:  prayerStarts[pid],
			FullText:   fullText,
			SourceURL:  url,
			FetchedAt:  time.Now().UTC().Format(time.RFC3339),
		}
		if err := writeJSON(path, cp); err != nil {
			log.Printf("  write %s: %v", path, err)
			errs++
			continue
		}
		fetched++
		if (i+1)%25 == 0 {
			log.Printf("  …fetched %d, skipped %d, errors %d (%d/%d)", fetched, skipped, errs, i+1, len(ids))
		}
	}
	log.Printf("Done. Fetched %d, skipped %d, errors %d", fetched, skipped, errs)
}

func readOrFetch(path, url string) ([]byte, error) {
	if !*flagForce {
		if b, err := os.ReadFile(path); err == nil {
			return b, nil
		}
	}
	body, err := httpGet(url)
	if err != nil {
		return nil, err
	}
	return body, os.WriteFile(path, body, 0644)
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

func parsePrayerList(body []byte) (ids, starts, titles []string) {
	for _, m := range rePrayerLink.FindAllSubmatch(body, -1) {
		ids = append(ids, string(m[1]))
	}
	for _, m := range rePrayerStart.FindAllSubmatch(body, -1) {
		starts = append(starts, decodeHTML(stripTags(string(m[1]))))
	}
	// titles list is sparser — only "named" prayers have one. Pad with "".
	titlesAtPos := map[int]string{}
	allMatches := rePrayerName.FindAllSubmatch(body, -1)
	_ = titlesAtPos
	for i, m := range allMatches {
		if i < len(ids) {
			titles = append(titles, decodeHTML(stripTags(string(m[1]))))
		}
	}
	for len(titles) < len(ids) {
		titles = append(titles, "")
	}
	return
}

func extractPrayerBody(body []byte) string {
	m := rePrayerBody.FindSubmatch(body)
	if len(m) < 2 {
		return ""
	}
	// Convert <p>…</p> to paragraphs separated by \n\n; strip everything else
	html := string(m[1])
	html = regexp.MustCompile(`(?i)</p>\s*<p[^>]*>`).ReplaceAllString(html, "\n\n")
	html = regexp.MustCompile(`(?i)<p[^>]*>`).ReplaceAllString(html, "")
	html = regexp.MustCompile(`(?i)</p>`).ReplaceAllString(html, "")
	return decodeHTML(stripTags(html))
}

func extractPageTitle(body []byte) string {
	m := rePageTitle.FindSubmatch(body)
	if len(m) < 2 {
		return ""
	}
	return decodeHTML(stripTags(string(m[1])))
}

// ── PHASE 2: match ────────────────────────────────────────────────────

type Candidate struct {
	Phelps string
	Score  float64
	Reason string
}

type writingRow struct {
	phelps   string
	sourceID string
	source   string
	text     string
	normText string
	ngrams   map[string]bool
}

func phaseMatch() {
	langDir := filepath.Join(*flagCacheDir, *flagLang)
	cached, err := loadCache(langDir)
	if err != nil {
		log.Fatalf("load cache: %v", err)
	}
	log.Printf("Loaded %d cached prayers from %s", len(cached), langDir)

	// Fetch all (phelps, text) for this language from the writings table.
	// Build several fast lookup indexes for scoring.
	rows := runDolt(fmt.Sprintf(
		`SELECT phelps, source_id, source, SUBSTRING(text, 1, 800) FROM writings `+
			`WHERE language='%s' AND phelps IS NOT NULL AND phelps NOT LIKE 'TMP%%'`,
		sqlEsc(*flagLang)))

	var corpus []writingRow
	bpappBySID := map[string]string{} // bpapp source_id → phelps
	for _, r := range rows {
		if len(r) < 4 {
			continue
		}
		nt := normalizeText(r[3])
		corpus = append(corpus, writingRow{
			phelps: r[0], sourceID: r[1], source: r[2],
			text: r[3], normText: nt, ngrams: makeNgrams(nt, 4),
		})
		if r[2] == "bahaiprayers.app" {
			bpappBySID[r[1]] = r[0]
		}
	}
	log.Printf("Loaded %d writings rows for %s; %d already-mapped bpapp source_ids", len(corpus), *flagLang, len(bpappBySID))

	if *flagOut == "" {
		*flagOut = "bpapp_" + *flagLang + ".sql"
	}
	if *flagReview == "" {
		*flagReview = "bpapp_" + *flagLang + "_review.tsv"
	}

	sqlF, err := os.Create(*flagOut)
	must(err)
	defer sqlF.Close()
	tsvF, err := os.Create(*flagReview)
	must(err)
	defer tsvF.Close()

	bookCode := *flagLang + ":bpapp"
	bookName := "Bahá'í Prayers App (" + *flagLang + ")"
	fmt.Fprintf(sqlF, "-- bahaiprayers.app PBS inserts for %s\n", bookCode)
	fmt.Fprintf(sqlF, "-- Generated %s; min-score=%.2f\n--\n", time.Now().Format(time.RFC3339), *flagMinScore)
	fmt.Fprintf(sqlF, "REPLACE INTO languages (langcode, inlang, name) VALUES ('%s', 'en', '%s');\nSET FOREIGN_KEY_CHECKS=0;\n\n", bookCode, sqlEsc(bookName))

	fmt.Fprintf(tsvF, "bpapp_id\tcategory\ttitle\tstart_text\ttop_phelps\ttop_score\trunner_up\trunner_score\treason\n")

	// Sort cached prayers by (cat_order, ord_in_cat) for stable output
	sort.Slice(cached, func(i, j int) bool {
		if cached[i].CatOrder != cached[j].CatOrder {
			return cached[i].CatOrder < cached[j].CatOrder
		}
		return cached[i].OrderInCat < cached[j].OrderInCat
	})

	srcID := *flagSrcBase
	mapped, review := 0, 0
	for _, cp := range cached {
		// Step 1: exact bpapp_id → phelps lookup
		var top, runner *Candidate
		if ph, ok := bpappBySID[cp.BpappID]; ok && ph != "" {
			top = &Candidate{Phelps: ph, Score: 1.0, Reason: "exact bpapp_id"}
		} else {
			// Step 2: text similarity scoring across the corpus
			needle := normalizeText(cp.FullText)
			if needle == "" {
				needle = normalizeText(cp.StartText)
			}
			ngs := makeNgrams(needle, 4)
			top, runner = bestMatches(needle, ngs, corpus)
		}

		if top != nil && top.Score >= *flagMinScore {
			fmt.Fprintf(sqlF,
				"INSERT INTO prayer_book_structure "+
					"(source_id, source_language, version, phelps_code, "+
					"category_name, category_order, order_in_category, notes) VALUES "+
					"(%d, '%s', 'bpapp', '%s', '%s', %d, %d, 'bpapp_id=%s; score=%.2f; %s');\n",
				srcID, bookCode, top.Phelps, sqlEsc(cp.CategoryNm),
				cp.CatOrder, cp.OrderInCat, cp.BpappID, top.Score, top.Reason)
			srcID++
			mapped++
		} else {
			topPh, topSc, runPh, runSc, reason := "", 0.0, "", 0.0, ""
			if top != nil {
				topPh, topSc, reason = top.Phelps, top.Score, top.Reason
			}
			if runner != nil {
				runPh, runSc = runner.Phelps, runner.Score
			}
			fmt.Fprintf(tsvF, "%s\t%s\t%s\t%s\t%s\t%.3f\t%s\t%.3f\t%s\n",
				cp.BpappID,
				cp.CategoryNm,
				strings.ReplaceAll(cp.Title, "\t", " "),
				truncate(strings.ReplaceAll(cp.StartText, "\t", " "), 120),
				topPh, topSc, runPh, runSc, reason)
			review++
		}
	}
	fmt.Fprintf(sqlF, "\nSET FOREIGN_KEY_CHECKS=1;\n")
	log.Printf("Done. %d auto-mapped → %s; %d need review → %s", mapped, *flagOut, review, *flagReview)
}

// ── Matching helpers ──────────────────────────────────────────────────

// normalizeText: lowercases, strips diacritics (lite), removes markdown
// headings and HTML, collapses whitespace, drops punctuation. Used to
// produce a stable form for both bpapp text and writings text comparison.
func normalizeText(s string) string {
	s = regexp.MustCompile(`(?m)^#+\s.*$`).ReplaceAllString(s, "")
	s = reTagStrip.ReplaceAllString(s, " ")
	s = strings.ToLower(s)
	s = regexp.MustCompile(`[^\p{L}\p{N}\s]`).ReplaceAllString(s, " ")
	s = reWS.ReplaceAllString(s, " ")
	return strings.TrimSpace(s)
}

func makeNgrams(s string, n int) map[string]bool {
	out := map[string]bool{}
	if len(s) < n {
		return out
	}
	for i := 0; i <= len(s)-n; i++ {
		out[s[i:i+n]] = true
	}
	return out
}

func jaccard(a, b map[string]bool) float64 {
	if len(a) == 0 || len(b) == 0 {
		return 0
	}
	inter := 0
	smaller, larger := a, b
	if len(b) < len(a) {
		smaller, larger = b, a
	}
	for k := range smaller {
		if larger[k] {
			inter++
		}
	}
	union := len(a) + len(b) - inter
	if union == 0 {
		return 0
	}
	return float64(inter) / float64(union)
}

func bestMatches(needle string, ngs map[string]bool, corpus []writingRow) (top, runner *Candidate) {
	scored := make([]Candidate, 0, len(corpus))
	for _, w := range corpus {
		// Cheap pre-check: substring presence of first ~30 chars of needle.
		probe := needle
		if len(probe) > 30 {
			probe = probe[:30]
		}
		if probe != "" && strings.Contains(w.normText, probe) {
			scored = append(scored, Candidate{Phelps: w.phelps, Score: 0.95, Reason: "substring head"})
			continue
		}
		// Otherwise jaccard over 4-grams.
		j := jaccard(ngs, w.ngrams)
		if j >= 0.3 {
			scored = append(scored, Candidate{Phelps: w.phelps, Score: j, Reason: fmt.Sprintf("jaccard4=%.2f", j)})
		}
	}
	if len(scored) == 0 {
		return nil, nil
	}
	sort.Slice(scored, func(i, j int) bool { return scored[i].Score > scored[j].Score })
	// Collapse multiple hits to the same phelps (take the highest score)
	seen := map[string]bool{}
	dedup := []Candidate{}
	for _, s := range scored {
		if seen[s.Phelps] {
			continue
		}
		seen[s.Phelps] = true
		dedup = append(dedup, s)
		if len(dedup) >= 5 {
			break
		}
	}
	top = &dedup[0]
	if len(dedup) > 1 {
		runner = &dedup[1]
	}
	// If the second-best is very close, it's ambiguous — penalize the top
	// so the caller may push to manual review.
	if runner != nil && top.Score-runner.Score < 0.05 {
		top.Score *= 0.7
		top.Reason = "ambiguous (" + top.Reason + ")"
	}
	return top, runner
}

func loadCache(dir string) ([]CachedPrayer, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	var out []CachedPrayer
	for _, e := range entries {
		if !strings.HasPrefix(e.Name(), "prayer_") || !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		b, err := os.ReadFile(filepath.Join(dir, e.Name()))
		if err != nil {
			continue
		}
		var cp CachedPrayer
		if err := json.Unmarshal(b, &cp); err != nil {
			continue
		}
		out = append(out, cp)
	}
	return out, nil
}

// ── Dolt + helpers ─────────────────────────────────────────────────────

func runDolt(query string) [][]string {
	cmd := exec.Command("dolt", "sql", "-q", query, "-r", "csv")
	cmd.Dir = *flagDolt
	out, err := cmd.Output()
	if err != nil {
		log.Fatalf("dolt sql: %v\nQuery: %s", err, query)
	}
	lines := bytes.Split(bytes.TrimRight(out, "\n"), []byte("\n"))
	if len(lines) <= 1 {
		return nil
	}
	rows := make([][]string, 0, len(lines)-1)
	for _, ln := range lines[1:] {
		rows = append(rows, splitCSV(string(ln)))
	}
	return rows
}

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

func writeJSON(path string, v any) error {
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer f.Close()
	enc := json.NewEncoder(f)
	enc.SetIndent("", "  ")
	return enc.Encode(v)
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}

func must(err error) {
	if err != nil {
		log.Fatal(err)
	}
}

// ── Main ───────────────────────────────────────────────────────────────

func main() {
	flag.Parse()
	if *flagLang == "" {
		log.Fatal("--lang required")
	}
	switch *flagPhase {
	case "fetch":
		phaseFetch()
	case "match":
		phaseMatch()
	default:
		log.Fatal("--phase fetch|match required")
	}
}
