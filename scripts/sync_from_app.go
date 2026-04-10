// sync_from_app.go — Scrape bahaiprayers.app and generate SQL INSERTs for the Dolt database.
//
// Outputs:
//   /tmp/app_sync_inserts.sql    — INSERT statements for new prayers (with phelps where matched)
//   /tmp/app_sync_report.txt     — summary: languages processed, counts, errors
//   /tmp/app_sync_unmatched.txt  — entries needing match.go run
//
// Usage:
//   go run sync_from_app.go
//   go run sync_from_app.go --dry-run          # report only, no files written
//   go run sync_from_app.go --lang de          # only process German
//   go run sync_from_app.go --cat 201          # only process Aid and Assistance
//   go run sync_from_app.go --no-cache         # bypass disk cache
package main

import (
	"bufio"
	"bytes"
	"crypto/md5"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode"
)

const (
	appBase  = "https://www.bahaiprayers.app"
	doltDir  = "/home/joop/bahaiwritings"
	cacheDir = "/tmp/bpapp_cache"
)

var httpClient = &http.Client{Timeout: 15 * time.Second}

// Hard-coded language list (no API for language discovery)
var appLanguages = []string{
	"de", "en", "es", "eo", "fr", "gux", "it", "gil", "hu", "mh",
	"nl", "pl", "pt", "ro", "sv", "be", "ru", "fa", "ar", "zh", "ja",
	// "vi" excluded: app returns English fallback (same as zh-Hans issue)
	"no", "sr", "eu",
}

// ISO code mapping: bahaiprayers.app code → DB code
var isoFix = map[string]string{
	"zh": "zh-Hans",
}

// Category definitions
type Category struct {
	ID   int
	Name string
}

var categories = []Category{
	{101, "Obligatory Prayers"},
	{200, "General Prayers"},
	{201, "Aid and Assistance"},
	{202, "America"},
	{203, "Children"},
	{204, "The Departed"},
	{205, "Detachment"},
	{206, "Divine Springtime"},
	{207, "Evening"},
	{208, "Families"},
	{209, "Firmness in the Covenant"},
	{210, "Forgiveness"},
	{211, "The Fund"},
	{212, "Gatherings"},
	{213, "Healing"},
	{214, "Humanity"},
	{215, "Manifestation of God"},
	{216, "Marriage"},
	{217, "Morning"},
	{218, "Nearness to God"},
	{219, "Paradise"},
	{220, "Praise and Gratitude"},
	{221, "Prison"},
	{222, "Protection"},
	{223, "Sacrifice"},
	{224, "Service"},
	{225, "Spiritual Growth"},
	{226, "Steadfastness"},
	{227, "Teaching"},
	{228, "Tests and Difficulties"},
	{229, "Trials"},
	{230, "Triumph of the Cause"},
	{231, "Unity"},
	{232, "Women"},
	{233, "Youth"},
	{301, "Special Tablets"},
	{302, "The Fast"},
	{303, "Huququ'llah: The Right of God"},
	{304, "Spiritual Assembly"},
	{305, "Occasional Prayers"},
	{401, "Hidden Words - Part I (Arabic)"},
	{403, "Hidden Words - Part II (Persian)"},
	{501, "Additional Prayers by Baha'u'llah"},
	{502, "Additional Prayers by 'Abdu'l-Baha"},
}

var catByID = func() map[int]Category {
	m := make(map[int]Category)
	for _, c := range categories {
		m[c.ID] = c
	}
	return m
}()

// ---- DB types ---------------------------------------------------------------

type DBEntry struct {
	Phelps   string
	Name     string
	Version  string
	FullText string
}

// ---- HTML → plain text (from sync_from_api.go) -----------------------------

var htmlTag = regexp.MustCompile(`<[^>]+>`)
var multiNL = regexp.MustCompile(`\n{3,}`)

func htmlToText(h string) string {
	// Insert newlines before block-level closing/opening tags
	h = regexp.MustCompile(`(?i)<(/?(p|br|h[1-6]|li)[^>]*)>`).
		ReplaceAllStringFunc(h, func(m string) string { return "\n" + m })
	// Strip all tags
	h = htmlTag.ReplaceAllString(h, "")
	// Decode common HTML entities
	h = strings.NewReplacer(
		"&amp;", "&", "&lt;", "<", "&gt;", ">",
		"&quot;", `"`, "&#39;", "'", "&nbsp;", " ",
		"&#x27;", "'", "&#x2019;", "\u2019",
	).Replace(h)
	// Normalize whitespace
	h = multiNL.ReplaceAllString(h, "\n\n")
	return strings.TrimSpace(h)
}

// ---- Fuzzy matching helpers (from sync_from_api.go) -------------------------

func isCJK(r rune) bool {
	return (r >= 0x4e00 && r <= 0x9fff) || // CJK Unified
		(r >= 0x3040 && r <= 0x30ff) || // Hiragana/Katakana
		(r >= 0xac00 && r <= 0xd7af) // Hangul
}

func textTokens(text string) map[string]bool {
	total, cjk := 0, 0
	for _, r := range text {
		total++
		if isCJK(r) {
			cjk++
		}
	}
	tokens := make(map[string]bool)
	if total > 0 && float64(cjk)/float64(total) > 0.3 {
		runes := []rune(text)
		for i := 0; i+2 < len(runes); i++ {
			tokens[string(runes[i:i+3])] = true
		}
	} else {
		cur := &strings.Builder{}
		flush := func() {
			if cur.Len() > 0 {
				tokens[strings.ToLower(cur.String())] = true
				cur.Reset()
			}
		}
		for _, r := range text {
			if unicode.IsLetter(r) || unicode.IsDigit(r) || r == '\'' {
				cur.WriteRune(r)
			} else {
				flush()
			}
		}
		flush()
	}
	return tokens
}

func jaccard(a, b map[string]bool) float64 {
	if len(a) == 0 || len(b) == 0 {
		return 0
	}
	intersect := 0
	for k := range a {
		if b[k] {
			intersect++
		}
	}
	union := len(a) + len(b) - intersect
	return float64(intersect) / float64(union)
}

func fingerprint(text string, n int) string {
	t := regexp.MustCompile(`\s+`).ReplaceAllString(text, " ")
	t = strings.TrimSpace(strings.ToLower(t))
	if len(t) > n {
		t = t[:n]
	}
	return t
}

// ---- Dolt query (from sync_from_api.go) ------------------------------------

type DoltJSONResult struct {
	Rows []map[string]interface{} `json:"rows"`
}

func queryDolt(query string) []map[string]interface{} {
	cmd := exec.Command("dolt", "sql", "-q", query, "--result-format", "json")
	cmd.Dir = doltDir
	var out bytes.Buffer
	var errBuf bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &errBuf
	if err := cmd.Run(); err != nil {
		fmt.Fprintf(os.Stderr, "Dolt error: %v\n%s\n", err, errBuf.String())
		return nil
	}
	var result DoltJSONResult
	if err := json.Unmarshal(out.Bytes(), &result); err != nil {
		fmt.Fprintf(os.Stderr, "JSON parse error: %v\n", err)
		return nil
	}
	return result.Rows
}

func str(v interface{}) string {
	if v == nil {
		return ""
	}
	return fmt.Sprintf("%v", v)
}

// ---- SQL helpers (from sync_from_api.go) ------------------------------------

var sqlUnsafe = strings.NewReplacer(`\`, `\\`, `'`, `\'`)

func sqlEsc(s string) string { return sqlUnsafe.Replace(s) }

// ---- Matching difficulty (from sync_from_api.go) ----------------------------

var hardLangs = map[string]bool{
	"ur": true, "hy": true, "ml": true, "kn": true, "bn": true,
	"th": true, "ja": true, "ko": true, "zh-Hans": true, "zh-Hant": true,
	"hi": true, "ta": true, "te": true, "gu": true, "mr": true,
	"am": true, "km": true, "lo": true, "mn": true, "fa": true, "ar": true,
}
var creoleLangs = map[string]bool{
	"bi": true, "tpi": true, "fj": true, "srn": true, "ht": true,
}
var indigenousLangs = map[string]bool{
	"lg": true, "gil": true, "hz": true, "lkt": true, "dak": true,
	"meu": true, "kiw": true, "moh": true, "gwi": true, "hur": true, "oj": true,
	"nai-CA": true, "nai-US": true,
}

func matchDifficulty(iso string) string {
	if hardLangs[iso] {
		return "HARD (script/non-Latin — use --translate)"
	}
	if creoleLangs[iso] {
		return "MEDIUM (English creole — Gemini with hint)"
	}
	if indigenousLangs[iso] {
		return "HARD (indigenous — check for English glosses first)"
	}
	return "EASY (European — standard Gemini pass)"
}

// ---- File writing (from sync_from_api.go) -----------------------------------

func writeFile(path string, lines ...string) {
	f, err := os.Create(path)
	if err != nil {
		panic(err)
	}
	defer f.Close()
	w := bufio.NewWriter(f)
	for _, l := range lines {
		fmt.Fprintln(w, l)
	}
	w.Flush()
}

// ---- HTTP fetching with disk cache ------------------------------------------

var noCache bool

func cacheKey(url string) string {
	h := md5.Sum([]byte(url))
	return fmt.Sprintf("%x", h)
}

// fetchURL fetches a URL. Returns body and true, or empty and false on 404/error.
// Uses disk cache unless --no-cache. Returns (body, fromCache, err).
func fetchURL(url string) (string, bool, error) {
	hash := cacheKey(url)
	cachePath := cacheDir + "/" + hash + ".html"

	// Try cache
	if !noCache {
		if data, err := os.ReadFile(cachePath); err == nil {
			return string(data), true, nil
		}
	}

	// Fetch from network
	req, _ := http.NewRequest("GET", url, nil)
	req.Header.Set("User-Agent", "bahaiprayers-sync/1.0")
	resp, err := httpClient.Do(req)
	if err != nil {
		return "", false, err
	}
	defer resp.Body.Close()

	if resp.StatusCode == 404 {
		return "", false, nil
	}
	if resp.StatusCode != 200 {
		return "", false, fmt.Errorf("HTTP %d for %s", resp.StatusCode, url)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", false, err
	}

	// Write to cache
	os.MkdirAll(cacheDir, 0755)
	_ = os.WriteFile(cachePath, body, 0644)

	return string(body), false, nil
}

// ---- HTML scraping helpers --------------------------------------------------

var rePrayerLink = regexp.MustCompile(`/prayer\?id=(\d+)`)

func extractPrayerIDs(html string) []int {
	matches := rePrayerLink.FindAllStringSubmatch(html, -1)
	seen := make(map[int]bool)
	var ids []int
	for _, m := range matches {
		id, err := strconv.Atoi(m[1])
		if err != nil {
			continue
		}
		if !seen[id] {
			seen[id] = true
			ids = append(ids, id)
		}
	}
	return ids
}

// ScrapedPrayer holds data extracted from a prayer page.
type ScrapedPrayer struct {
	ID          int
	Title       string
	Author      string
	Instruction string // recitation instruction from h3
	Body        string // paragraphs joined, excluding author
}

var reH1 = regexp.MustCompile(`(?is)<h1[^>]*>(.*?)</h1>`)
var reH3 = regexp.MustCompile(`(?is)<h3[^>]*>(.*?)</h3>`)
var rePAuthor = regexp.MustCompile(`(?is)<p[^>]*class="author"[^>]*>(.*?)</p>`)
var rePrayerDiv = regexp.MustCompile(`(?is)<div[^>]*id="prayer"[^>]*>(.*?)</div>`)
var reParas = regexp.MustCompile(`(?is)<p(?:\s[^>]*)?>(.+?)</p>`)

func scrapePrayerPage(html string) ScrapedPrayer {
	var sp ScrapedPrayer

	// Extract from within #prayer div if possible; fall back to full page
	content := html
	if m := rePrayerDiv.FindStringSubmatch(html); m != nil {
		content = m[1]
	}

	// Title from h1
	if m := reH1.FindStringSubmatch(content); m != nil {
		sp.Title = htmlToText(m[1])
	}

	// Instruction from h3
	if m := reH3.FindStringSubmatch(content); m != nil {
		sp.Instruction = htmlToText(m[1])
	}

	// Author from p.author
	if m := rePAuthor.FindStringSubmatch(content); m != nil {
		sp.Author = htmlToText(m[1])
	}

	// All <p> elements (excluding p.author)
	// First, remove the author paragraph so it doesn't appear in body
	bodyHTML := rePAuthor.ReplaceAllString(content, "")
	paraMatches := reParas.FindAllStringSubmatch(bodyHTML, -1)
	var paras []string
	for _, pm := range paraMatches {
		text := htmlToText(pm[1])
		if strings.TrimSpace(text) != "" {
			paras = append(paras, text)
		}
	}
	sp.Body = strings.Join(paras, "\n\n")

	return sp
}

// ---- Hidden Words phelps assignment -----------------------------------------

func hiddenWordsPhelps(catID, prayerID int) string {
	// Prayer IDs are either:
	//   8-digit: 10_000_000 + catID*1000 + n*10  (old app format)
	//   6-digit: catID*1000 + n*10               (new app format, 2026+)
	// n starts at 1 for preamble; offset/10 - 1 = verseNum.
	base8 := 10_000_000 + catID*1000
	base6 := catID * 1000
	var offset int
	if prayerID >= base8 {
		offset = prayerID - base8
	} else if prayerID > base6 {
		offset = prayerID - base6
	} else {
		return ""
	}
	if offset <= 0 || offset%10 != 0 {
		return ""
	}
	verseNum := offset/10 - 1 // 0 = preamble, 1 = verse #1, …
	switch catID {
	case 401: // Arabic Hidden Words — BH00386 (71 verses + preamble)
		if verseNum == 0 {
			return "BH00386A00"
		}
		if verseNum <= 71 {
			return fmt.Sprintf("BH00386A%02d", verseNum)
		}
	case 403: // Persian Hidden Words — BH00113 (82 verses + preamble + epilogue)
		if verseNum == 0 {
			return "BH00113P00"
		}
		if verseNum <= 82 {
			return fmt.Sprintf("BH00113P%02d", verseNum)
		}
		// verseNum 83+ = epilogue/closing inscription — leave unmatched for manual coding
	}
	return ""
}

// ---- Cross-reference matching -----------------------------------------------

type OldEntry struct {
	FP     string
	Tokens map[string]bool
	Phelps string
	Name   string
}

func buildIndex(entries map[string]DBEntry) []OldEntry {
	var out []OldEntry
	for _, e := range entries {
		fp := fingerprint(e.FullText, 150)
		tokens := textTokens(e.FullText)
		out = append(out, OldEntry{fp, tokens, e.Phelps, e.Name})
	}
	return out
}

func crossMatch(rawText string, oldEntries []OldEntry) (phelps, method string) {
	if len(oldEntries) == 0 {
		return "", ""
	}
	fp := fingerprint(rawText, 150)

	// Level 1: exact fingerprint
	for _, old := range oldEntries {
		if old.FP == fp {
			return old.Phelps, "exact"
		}
	}

	// Level 2: prefix match (first 80 chars)
	short := fp
	if len(short) > 80 {
		short = short[:80]
	}
	for _, old := range oldEntries {
		oldShort := old.FP
		if len(oldShort) > 80 {
			oldShort = oldShort[:80]
		}
		if short == oldShort {
			return old.Phelps, "prefix"
		}
	}

	// Level 3: Jaccard >= 0.55
	newTok := textTokens(rawText)
	bestScore := 0.0
	var bestOld *OldEntry
	for i := range oldEntries {
		score := jaccard(newTok, oldEntries[i].Tokens)
		if score > bestScore {
			bestScore = score
			bestOld = &oldEntries[i]
		}
	}
	if bestScore >= 0.55 && bestOld != nil {
		return bestOld.Phelps, fmt.Sprintf("fuzzy(%.2f)", bestScore)
	}

	return "", ""
}

// ---- DB loading -------------------------------------------------------------

func loadExistingAppEntries() map[string]map[string]bool {
	fmt.Println("  Loading existing bahaiprayers.app entries...")
	rows := queryDolt(
		"SELECT language, source_id FROM writings WHERE source='bahaiprayers.app'",
	)
	byLang := make(map[string]map[string]bool)
	for _, row := range rows {
		lang := str(row["language"])
		sid := str(row["source_id"])
		if byLang[lang] == nil {
			byLang[lang] = make(map[string]bool)
		}
		byLang[lang][sid] = true
	}
	return byLang
}

func loadDBEntriesBySource(source string) map[string]map[string]DBEntry {
	fmt.Printf("  Loading DB entries for source=%s...\n", source)
	rows := queryDolt(
		fmt.Sprintf(
			"SELECT language, source_id, phelps, name, version, text "+
				"FROM writings WHERE source='%s' "+
				"ORDER BY language, CAST(source_id AS UNSIGNED)", source),
	)
	byLang := make(map[string]map[string]DBEntry)
	for _, row := range rows {
		lang := str(row["language"])
		sid := str(row["source_id"])
		if byLang[lang] == nil {
			byLang[lang] = make(map[string]DBEntry)
		}
		rawText := str(row["text"])
		lines := strings.Split(rawText, "\n")
		var bodyLines []string
		for _, l := range lines {
			if !strings.HasPrefix(l, "## ") {
				bodyLines = append(bodyLines, l)
			}
		}
		body := strings.TrimSpace(strings.Join(bodyLines, "\n"))
		byLang[lang][sid] = DBEntry{
			Phelps:   str(row["phelps"]),
			Name:     str(row["name"]),
			Version:  str(row["version"]),
			FullText: body,
		}
	}
	return byLang
}

// Check if a language exists in the DB languages table
func languageExists(iso string) bool {
	rows := queryDolt(fmt.Sprintf(
		"SELECT langcode FROM languages WHERE langcode='%s'", sqlEsc(iso)))
	return len(rows) > 0
}

// cacheHit returns cached body without fetching. Use to check cache before consuming rate token.
func cacheHit(url string) (string, bool) {
	if noCache {
		return "", false
	}
	hash := cacheKey(url)
	cachePath := cacheDir + "/" + hash + ".html"
	if data, err := os.ReadFile(cachePath); err == nil {
		return string(data), true
	}
	return "", false
}

// fetchJob is one prayer page to fetch.
type fetchJob struct {
	lang    string
	dbISO   string
	cat     Category
	pid     int
}

// fetchResult is the outcome of a fetchJob.
type fetchResult struct {
	job  fetchJob
	html string
	err  error
}

const numFetchWorkers = 8

// parallelFetch fetches all jobs using a worker pool with a shared rate limiter.
// Cached pages skip the rate limiter entirely.
func parallelFetch(jobs []fetchJob, rateLim <-chan time.Time) []fetchResult {
	jobCh := make(chan fetchJob, len(jobs))
	resCh := make(chan fetchResult, len(jobs))

	var wg sync.WaitGroup
	for i := 0; i < numFetchWorkers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for job := range jobCh {
				url := fmt.Sprintf("%s/prayer?id=%d&to=%s", appBase, job.pid, job.lang)
				if html, ok := cacheHit(url); ok {
					resCh <- fetchResult{job, html, nil}
					continue
				}
				<-rateLim // wait for token only for network requests
				html, _, err := fetchURL(url)
				resCh <- fetchResult{job, html, err}
			}
		}()
	}

	for _, j := range jobs {
		jobCh <- j
	}
	close(jobCh)

	go func() {
		wg.Wait()
		close(resCh)
	}()

	results := make([]fetchResult, 0, len(jobs))
	for r := range resCh {
		results = append(results, r)
	}
	return results
}

// ---- Main -------------------------------------------------------------------

func main() {
	// Parse flags
	dryRun := false
	var filterLang string
	var filterCat int
	for i := 1; i < len(os.Args); i++ {
		switch os.Args[i] {
		case "--dry-run":
			dryRun = true
		case "--no-cache":
			noCache = true
		case "--lang":
			if i+1 < len(os.Args) {
				filterLang = os.Args[i+1]
				i++
			}
		case "--cat":
			if i+1 < len(os.Args) {
				filterCat, _ = strconv.Atoi(os.Args[i+1])
				i++
			}
		}
	}

	// Load existing app entries for de-duplication
	existingApp := loadExistingAppEntries()

	// Load bahaiprayers.net entries for cross-reference matching
	netByLang := loadDBEntriesBySource("bahaiprayers.net")

	// Filter categories
	catsToProcess := categories
	if filterCat > 0 {
		catsToProcess = nil
		if c, ok := catByID[filterCat]; ok {
			catsToProcess = []Category{c}
		} else {
			fmt.Fprintf(os.Stderr, "Unknown category ID: %d\n", filterCat)
			os.Exit(1)
		}
	}

	// Filter languages
	langsToProcess := appLanguages
	if filterLang != "" {
		langsToProcess = []string{filterLang}
	}

	// Ensure cache directory
	os.MkdirAll(cacheDir, 0755)

	var (
		insertLines   []string
		reportLines   []string
		unmatchedLines []string
		totalInserted int
		totalSkipped  int
		totalErrors   int
		totalHWCoded  int
		totalXRef     int
		newLangs      []string
		langCounts    = make(map[string]int) // iso → count of inserts
	)

	// Check for new languages
	for _, lang := range langsToProcess {
		dbISO := lang
		if fix, ok := isoFix[lang]; ok {
			dbISO = fix
		}
		if !languageExists(dbISO) {
			newLangs = append(newLangs, fmt.Sprintf("%s (app code) → %s (DB code)", lang, dbISO))
		}
	}

	sort.Strings(langsToProcess)

	// Pre-build cross-reference index per language
	xrefByLang := make(map[string][]OldEntry)
	for _, lang := range langsToProcess {
		dbISO := lang
		if fix, ok := isoFix[lang]; ok {
			dbISO = fix
		}
		if netEntries, ok := netByLang[dbISO]; ok {
			xrefByLang[dbISO] = buildIndex(netEntries)
		}
	}

	// Global rate limiter: one token every 200ms (5 req/s)
	rateLimTicker := time.NewTicker(200 * time.Millisecond)
	defer rateLimTicker.Stop()
	rateLim := rateLimTicker.C

	// === Phase 1: fetch category pages → collect prayer jobs ===
	fmt.Println("Phase 1: fetching category pages...")
	type catJob struct {
		lang  string
		dbISO string
		cat   Category
	}
	var catJobs []catJob
	for _, lang := range langsToProcess {
		dbISO := lang
		if fix, ok := isoFix[lang]; ok {
			dbISO = fix
		}
		for _, cat := range catsToProcess {
			catJobs = append(catJobs, catJob{lang, dbISO, cat})
		}
	}

	type catResult struct {
		jobs    []fetchJob
		skipped int
	}
	catJobCh := make(chan catJob, len(catJobs))
	catResCh := make(chan catResult, len(catJobs))
	var catWg sync.WaitGroup
	for i := 0; i < numFetchWorkers; i++ {
		catWg.Add(1)
		go func() {
			defer catWg.Done()
			for cj := range catJobCh {
				url := fmt.Sprintf("%s/category?id=%d&l=%s", appBase, cj.cat.ID, cj.lang)
				html, ok := cacheHit(url)
				if !ok {
					<-rateLim
					var err error
					html, _, err = fetchURL(url)
					if err != nil {
						fmt.Fprintf(os.Stderr, "  WARN cat %s/%d: %v\n", cj.lang, cj.cat.ID, err)
						catResCh <- catResult{}
						continue
					}
				}
				if html == "" {
					catResCh <- catResult{}
					continue
				}
				prayerIDs := extractPrayerIDs(html)
				sort.Ints(prayerIDs)
				var jobs []fetchJob
				skipped := 0
				existing := existingApp[cj.dbISO]
				for _, pid := range prayerIDs {
					if existing != nil && existing[strconv.Itoa(pid)] {
						skipped++
						continue
					}
					jobs = append(jobs, fetchJob{cj.lang, cj.dbISO, cj.cat, pid})
				}
				catResCh <- catResult{jobs, skipped}
			}
		}()
	}
	for _, cj := range catJobs {
		catJobCh <- cj
	}
	close(catJobCh)
	go func() { catWg.Wait(); close(catResCh) }()

	// Collect all prayer jobs; use a map to deduplicate (same pid can appear in multiple cats)
	seen := make(map[string]bool) // "dbISO/pid"
	var allJobs []fetchJob
	for res := range catResCh {
		totalSkipped += res.skipped
		for _, j := range res.jobs {
			key := fmt.Sprintf("%s/%d", j.dbISO, j.pid)
			if seen[key] {
				continue
			}
			seen[key] = true
			allJobs = append(allJobs, j)
		}
	}
	fmt.Printf("Phase 1 done: %d prayer pages to fetch (%d already in DB)\n", len(allJobs), totalSkipped)

	// === Phase 2: parallel-fetch all prayer pages ===
	fmt.Printf("Phase 2: fetching %d prayer pages with %d workers...\n", len(allJobs), numFetchWorkers)
	allResults := parallelFetch(allJobs, rateLim)
	fmt.Printf("Phase 2 done: %d results\n", len(allResults))

	// === Phase 3: process results → generate SQL ===
	langInsertCounts := make(map[string]int)
	// Track within-run dedup (parallelFetch may return a pid multiple times if it appeared in >1 cat)
	processedPID := make(map[string]bool)

	for _, res := range allResults {
		if res.err != nil {
			fmt.Fprintf(os.Stderr, "  WARN prayer %d/%s: %v\n", res.job.pid, res.job.lang, res.err)
			totalErrors++
			continue
		}
		if res.html == "" {
			continue // 404
		}

		pidKey := fmt.Sprintf("%s/%d", res.job.dbISO, res.job.pid)
		if processedPID[pidKey] {
			continue
		}
		processedPID[pidKey] = true

		job := res.job
		sidStr := strconv.Itoa(job.pid)

		sp := scrapePrayerPage(res.html)
		sp.ID = job.pid

		title := sp.Title
		if title == "" {
			title = job.cat.Name
		}

		var textParts []string
		if sp.Instruction != "" {
			textParts = append(textParts, "*"+sp.Instruction+"*")
		}
		if sp.Body != "" {
			textParts = append(textParts, sp.Body)
		}
		bodyText := strings.Join(textParts, "\n\n")
		fullText := "## " + title + "\n\n" + bodyText

		phelps := ""
		if job.cat.ID == 401 || job.cat.ID == 403 {
			phelps = hiddenWordsPhelps(job.cat.ID, job.pid)
			if phelps != "" {
				totalHWCoded++
			}
		}

		matchMethod := ""
		if phelps == "" {
			if idx, ok := xrefByLang[job.dbISO]; ok {
				phelps, matchMethod = crossMatch(bodyText, idx)
				if phelps != "" {
					totalXRef++
				}
			}
		}

		link := fmt.Sprintf("%s/prayer?id=%d&to=%s", appBase, job.pid, job.lang)
		phelpsSql := "NULL"
		if phelps != "" {
			phelpsSql = "'" + sqlEsc(phelps) + "'"
		}
		insert := fmt.Sprintf(
			"INSERT INTO writings (version, source, source_id, language, name, type, text, link, phelps, is_verified) "+
				"VALUES (uuid(), 'bahaiprayers.app', '%s', '%s', '%s', 'prayer', '%s', '%s', %s, 0);",
			sqlEsc(sidStr), sqlEsc(job.dbISO), sqlEsc(title), sqlEsc(fullText), sqlEsc(link), phelpsSql,
		)
		insertLines = append(insertLines, insert)
		langInsertCounts[job.dbISO]++
		totalInserted++

		if phelps == "" {
			unmatchedLines = append(unmatchedLines,
				fmt.Sprintf("  %s/%s  cat=%d(%s)  title=%s", job.dbISO, sidStr, job.cat.ID, job.cat.Name, title))
		}
		if matchMethod != "" {
			reportLines = append(reportLines,
				fmt.Sprintf("  %s/%s → %s [%s]", job.dbISO, sidStr, phelps, matchMethod))
		} else if phelps != "" {
			reportLines = append(reportLines,
				fmt.Sprintf("  %s/%s → %s [HW-auto]", job.dbISO, sidStr, phelps))
		}
	}

	// Print per-language summary
	for _, lang := range langsToProcess {
		dbISO := lang
		if fix, ok := isoFix[lang]; ok {
			dbISO = fix
		}
		n := langInsertCounts[dbISO]
		if n > 0 {
			langCounts[dbISO] = n
		}
		fmt.Printf("  %s: %d new\n", dbISO, n)
	}

	// Build summary
	changedLangs := make([]string, 0, len(langCounts))
	for iso := range langCounts {
		changedLangs = append(changedLangs, iso)
	}
	sort.Strings(changedLangs)

	summary := []string{
		"bahaiprayers.app SYNC REPORT",
		fmt.Sprintf("Date: %s", time.Now().Format("2006-01-02 15:04")),
		"",
		fmt.Sprintf("Languages processed:     %d", len(langsToProcess)),
		fmt.Sprintf("Languages with inserts:  %d: %s", len(changedLangs), strings.Join(changedLangs, ", ")),
		fmt.Sprintf("Total new inserts:       %d", totalInserted),
		fmt.Sprintf("  Hidden Words coded:    %d", totalHWCoded),
		fmt.Sprintf("  Cross-ref matched:     %d", totalXRef),
		fmt.Sprintf("  Unmatched (need work): %d", totalInserted-totalHWCoded-totalXRef),
		fmt.Sprintf("Skipped (already in DB): %d", totalSkipped),
		fmt.Sprintf("Errors:                  %d", totalErrors),
	}

	if len(newLangs) > 0 {
		summary = append(summary, "")
		summary = append(summary, "NEW LANGUAGES (not in DB languages table):")
		for _, nl := range newLangs {
			summary = append(summary, "  "+nl)
		}
		summary = append(summary, "  NOTE: You may need to INSERT into the languages table before applying inserts.")
	}

	// Print summary
	fmt.Println()
	for _, l := range summary {
		fmt.Println(l)
	}

	if dryRun {
		fmt.Println("\n[dry-run: no files written]")
		return
	}

	if totalInserted == 0 {
		fmt.Println("\nNo new prayers to insert. No files written.")
		return
	}

	// Write inserts SQL
	sqlHeader := []string{
		"-- New prayers from bahaiprayers.app scrape",
		"-- Apply with: grep '^INSERT' /tmp/app_sync_inserts.sql | dolt sql",
		"",
		"SET FOREIGN_KEY_CHECKS=0;",
		"",
	}
	sqlFooter := []string{
		"",
		"SET FOREIGN_KEY_CHECKS=1;",
	}
	allSQL := append(sqlHeader, insertLines...)
	allSQL = append(allSQL, sqlFooter...)
	writeFile("/tmp/app_sync_inserts.sql", allSQL...)

	// Write report
	allReport := append(summary, "")
	allReport = append(allReport, "DETAIL:")
	allReport = append(allReport, reportLines...)
	writeFile("/tmp/app_sync_report.txt", allReport...)

	// Write unmatched
	unmatchedHeader := []string{
		"PRAYERS NEEDING PHELPS MATCHING (run match.go)",
		strings.Repeat("=", 60),
		"",
	}

	// Group unmatched by difficulty
	type UnmatchedGroup struct {
		ISO     string
		Diff    string
		Prayers []string
	}
	unmatchedByISO := make(map[string][]string)
	for _, line := range unmatchedLines {
		// Extract ISO from "  iso/..." format
		parts := strings.SplitN(strings.TrimSpace(line), "/", 2)
		if len(parts) >= 1 {
			unmatchedByISO[parts[0]] = append(unmatchedByISO[parts[0]], line)
		}
	}

	var unmatchedOutput []string
	unmatchedOutput = append(unmatchedOutput, unmatchedHeader...)

	for _, label := range []string{"EASY", "MEDIUM", "HARD"} {
		first := true
		isos := make([]string, 0)
		for iso := range unmatchedByISO {
			if strings.HasPrefix(matchDifficulty(iso), label) {
				isos = append(isos, iso)
			}
		}
		sort.Strings(isos)
		for _, iso := range isos {
			prayers := unmatchedByISO[iso]
			if first {
				unmatchedOutput = append(unmatchedOutput,
					fmt.Sprintf("\n%s LANGUAGES", label),
					strings.Repeat("-", 40))
				first = false
			}
			unmatchedOutput = append(unmatchedOutput,
				fmt.Sprintf("\n%s — %d prayers  [%s]", iso, len(prayers), matchDifficulty(iso)),
				fmt.Sprintf("  Run: go run match.go --lang %s", iso),
			)
			unmatchedOutput = append(unmatchedOutput, prayers...)
		}
	}
	writeFile("/tmp/app_sync_unmatched.txt", unmatchedOutput...)

	fmt.Println("\nOutputs:")
	fmt.Println("  /tmp/app_sync_inserts.sql    — INSERT statements")
	fmt.Println("  /tmp/app_sync_report.txt     — full sync report")
	fmt.Println("  /tmp/app_sync_unmatched.txt  — prayers needing matching")
	fmt.Println("\nNext steps:")
	fmt.Println("  grep '^SET\\|^INSERT' /tmp/app_sync_inserts.sql | dolt sql")
	fmt.Println("  # Then run match.go for unmatched languages")
}

// Silence unused import warnings
var _ io.Reader
