// sync_from_api.go — Sync bahaiprayers.net prayers with the Dolt database.
//
// Compares current API state with DB and outputs:
//   /tmp/sync_report.txt     — human-readable summary with matching difficulty hints
//   /tmp/sync_inserts.sql    — INSERT statements for new prayers (with phelps where carried)
//   /tmp/sync_deletes.sql    — DELETE statements for removed prayers (commented, review first!)
//   /tmp/sync_unmatched.txt  — prayers needing new phelps matching, grouped by difficulty
//
// Usage:
//   go run sync_from_api.go
//   go run sync_from_api.go --dry-run      # report only, no files written
package main

import (
	"bufio"
	"bytes"
	"encoding/csv"
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
	"unicode"
)

const (
	apiBase = "https://BahaiPrayers.net/api/prayer/"
	langCSV = "/home/joop/bahaiprayers-static/rel/lang.csv"
	doltDir = "/home/joop/prayermatching/bahaiwritings"
)

// ---- API types (from prayers-to-md) ----------------------------------------

type APIPrayer struct {
	Id           int    `json:"Id"`
	AuthorId     int    `json:"AuthorId"`
	LanguageId   int    `json:"LanguageId"`
	Text         string `json:"Text"`
	FirstTagName string `json:"FirstTagName"`
}

type APIPrayerBook struct {
	Prayers []APIPrayer `json:"Prayers"`
}

type APILanguage struct {
	Id          int    `json:"Id"`
	English     string `json:"English"`
	PrayerCount int    `json:"PrayerCount"`
}

// ---- DB types ---------------------------------------------------------------

type DBEntry struct {
	Phelps   string
	Name     string
	Version  string
	FullText string
}

// ---- Language mapping -------------------------------------------------------

type LangInfo struct {
	ISO  string
	Name string
	RTL  bool
}

func loadLangCSV() map[int]LangInfo {
	f, err := os.Open(langCSV)
	if err != nil {
		panic(err)
	}
	defer f.Close()
	r := csv.NewReader(f)
	r.FieldsPerRecord = 7
	rows, err := r.ReadAll()
	if err != nil {
		panic(err)
	}
	m := make(map[int]LangInfo)
	for _, row := range rows[1:] { // skip header
		id, err := strconv.Atoi(row[0])
		if err != nil {
			continue
		}
		m[id] = LangInfo{
			ISO:  row[1],
			Name: row[4],
			RTL:  strings.TrimSpace(row[6]) == "rtl",
		}
	}
	return m
}

// ---- HTTP helpers -----------------------------------------------------------

func getJSON(url string, out interface{}) error {
	req, _ := http.NewRequest("GET", url, nil)
	req.Header.Set("User-Agent", "bahaiprayers-sync/1.0")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	return json.NewDecoder(resp.Body).Decode(out)
}

func fetchLanguages() []APILanguage {
	var langs []APILanguage
	if err := getJSON(apiBase+"Languages", &langs); err != nil {
		panic(err)
	}
	return langs
}

func fetchPrayers(langID int) []APIPrayer {
	url := fmt.Sprintf("%sprayersystembylanguage?html=true&languageid=%d", apiBase, langID)
	var book APIPrayerBook
	if err := getJSON(url, &book); err != nil {
		fmt.Fprintf(os.Stderr, "  WARN: fetch failed for lang %d: %v\n", langID, err)
		return nil
	}
	return book.Prayers
}

// ---- HTML → plain text ------------------------------------------------------

var htmlTag   = regexp.MustCompile(`<[^>]+>`)
var multiNL   = regexp.MustCompile(`\n{3,}`)
var blockTags = map[string]bool{
	"p": true, "br": true, "h1": true, "h2": true,
	"h3": true, "h4": true, "h5": true, "h6": true, "li": true,
}

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

// ---- Fuzzy matching helpers -------------------------------------------------

func isCJK(r rune) bool {
	return (r >= 0x4e00 && r <= 0x9fff) || // CJK Unified
		(r >= 0x3040 && r <= 0x30ff) || // Hiragana/Katakana
		(r >= 0xac00 && r <= 0xd7af) // Hangul
}

func textTokens(text string) map[string]bool {
	// Count CJK chars
	total, cjk := 0, 0
	for _, r := range text {
		total++
		if isCJK(r) {
			cjk++
		}
	}
	tokens := make(map[string]bool)
	if total > 0 && float64(cjk)/float64(total) > 0.3 {
		// CJK: use character 3-grams
		runes := []rune(text)
		for i := 0; i+2 < len(runes); i++ {
			tokens[string(runes[i:i+3])] = true
		}
	} else {
		// Word tokens (Unicode-aware)
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

// ---- Dolt query -------------------------------------------------------------

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

func loadDBEntries() map[string]map[string]DBEntry {
	fmt.Println("  Loading DB entries...")
	rows := queryDolt(
		"SELECT language, source_id, phelps, name, version, text " +
			"FROM writings WHERE source='bahaiprayers.net' " +
			"ORDER BY language, CAST(source_id AS UNSIGNED)",
	)
	byLang := make(map[string]map[string]DBEntry)
	for _, row := range rows {
		lang := str(row["language"])
		sid  := str(row["source_id"])
		if byLang[lang] == nil {
			byLang[lang] = make(map[string]DBEntry)
		}
		rawText := str(row["text"])
		// Strip ## header line
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

// ---- SQL generation ---------------------------------------------------------

var sqlUnsafe = strings.NewReplacer(`\`, `\\`, `'`, `\'`)

func sqlEsc(s string) string { return sqlUnsafe.Replace(s) }

func makeInsert(p APIPrayer, iso string, langID int, prayerName, phelps string) string {
	text := htmlToText(p.Text)
	var fullText string
	if prayerName != "" {
		fullText = "## " + prayerName + "\n\n" + text
	} else {
		fullText = text
	}
	link := fmt.Sprintf("https://bahaiprayers.net/Book/Single/%d/%d", langID, p.Id)
	phelpsSql := "NULL"
	if phelps != "" {
		phelpsSql = "'" + sqlEsc(phelps) + "'"
	}
	// Use uuid() as Dolt default for version
	return fmt.Sprintf(
		"INSERT INTO writings (version, source, source_id, language, name, type, text, link, phelps, is_verified) "+
			"VALUES (uuid(), 'bahaiprayers.net', '%d', '%s', '%s', 'prayer', '%s', '%s', %s, 1);",
		p.Id, sqlEsc(iso), sqlEsc(prayerName), sqlEsc(fullText), sqlEsc(link), phelpsSql,
	)
}

// ---- Matching difficulty ----------------------------------------------------

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

func matchCommand(iso string) string {
	cmd := fmt.Sprintf("python3 ~/prayermatching/scripts/gemini_batch_match.py --lang %s", iso)
	if hardLangs[iso] || creoleLangs[iso] {
		cmd += " --translate"
	}
	return cmd
}

// ---- Main -------------------------------------------------------------------

type Unmatched struct {
	Name     string
	Prayers  []string
	Diff     string
}

func main() {
	dryRun := len(os.Args) > 1 && os.Args[1] == "--dry-run"

	fmt.Println("Loading lang.csv...")
	langMap := loadLangCSV() // apiID → LangInfo

	dbByLang := loadDBEntries() // iso → { sid → DBEntry }

	fmt.Println("Fetching language list from API...")
	apiLangs := fetchLanguages()
	apiLangByID := make(map[int]APILanguage)
	for _, l := range apiLangs {
		apiLangByID[l.Id] = l
	}

	// Sort API IDs for deterministic output
	apiIDs := make([]int, 0, len(langMap))
	for id := range langMap {
		apiIDs = append(apiIDs, id)
	}
	sort.Ints(apiIDs)

	var (
		insertLines  []string
		deleteLines  []string
		reportLines  []string
		unmatched    = make(map[string]*Unmatched)
		totalNew     int
		totalRemoved int
		totalCarried int
		totalUnmatch int
		langsChanged []string
	)

	for _, apiID := range apiIDs {
		li, ok := langMap[apiID]
		if !ok {
			continue
		}
		iso, name := li.ISO, li.Name
		al, exists := apiLangByID[apiID]
		if !exists || al.PrayerCount == 0 {
			continue
		}

		fmt.Printf("  %s (%s): %d prayers...\n", iso, name, al.PrayerCount)
		apiPrayers := fetchPrayers(apiID)
		if len(apiPrayers) == 0 {
			continue
		}

		// Build API map: sid → prayer
		apiByID := make(map[string]APIPrayer)
		for _, p := range apiPrayers {
			apiByID[strconv.Itoa(p.Id)] = p
		}
		dbEntries := dbByLang[iso]

		// Compute sets
		newIDs := make([]string, 0)
		for sid := range apiByID {
			if _, found := dbEntries[sid]; !found {
				newIDs = append(newIDs, sid)
			}
		}
		removedIDs := make([]string, 0)
		for sid := range dbEntries {
			if _, found := apiByID[sid]; !found {
				removedIDs = append(removedIDs, sid)
			}
		}
		overlap := len(apiPrayers) - len(newIDs)

		if len(newIDs) == 0 && len(removedIDs) == 0 {
			continue
		}
		langsChanged = append(langsChanged, iso)

		isFullReplacement := overlap == 0 && len(dbEntries) > 0 && len(newIDs) > 0

		section := []string{
			strings.Repeat("=", 60),
			fmt.Sprintf("%s (%s): %d new, %d removed", iso, name, len(newIDs), len(removedIDs)),
			fmt.Sprintf("  DB: %d  API: %d  Overlap: %d", len(dbEntries), len(apiPrayers), overlap),
		}
		if isFullReplacement {
			section = append(section, "  *** FULL REPLACEMENT DETECTED ***")
		}

		// Build fingerprint + word-set index from removed (old) entries
		type OldEntry struct {
			FP     string
			Tokens map[string]bool
			Phelps string
			Name   string
		}
		var oldEntries []OldEntry
		for sid := range removedIDs {
			_ = sid
		}
		for _, sid := range removedIDs {
			e := dbEntries[sid]
			fp := fingerprint(e.FullText, 150)
			tokens := textTokens(e.FullText)
			oldEntries = append(oldEntries, OldEntry{fp, tokens, e.Phelps, e.Name})
		}

		// Sort new IDs numerically for stable output
		sort.Slice(newIDs, func(i, j int) bool {
			a, _ := strconv.Atoi(newIDs[i])
			b, _ := strconv.Atoi(newIDs[j])
			return a < b
		})

		langInserts := []string{fmt.Sprintf("\n-- %s (%s) — %d new prayers", iso, name, len(newIDs))}
		langUnmatched := []string{}

		for _, sid := range newIDs {
			p := apiByID[sid]
			rawText := htmlToText(p.Text)
			apiName := p.FirstTagName

			// Try to carry phelps from old entry via content matching
			carriedPhelps := ""
			carriedName := ""
			matchMethod := ""

			if len(oldEntries) > 0 {
				fp := fingerprint(rawText, 150)

				// Level 1: exact fingerprint
				for _, old := range oldEntries {
					if old.FP == fp {
						carriedPhelps = old.Phelps
						carriedName = old.Name
						matchMethod = "exact"
						break
					}
				}

				// Level 2: prefix match (first 80 chars)
				if matchMethod == "" {
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
							carriedPhelps = old.Phelps
							carriedName = old.Name
							matchMethod = "prefix"
							break
						}
					}
				}

				// Level 3: Jaccard ≥ 0.55 on full text
				if matchMethod == "" {
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
						carriedPhelps = bestOld.Phelps
						carriedName = bestOld.Name
						matchMethod = fmt.Sprintf("fuzzy(%.2f)", bestScore)
					}
				}
			}

			prayerName := apiName
			if carriedName != "" {
				prayerName = carriedName
			}

			if carriedPhelps != "" {
				totalCarried++
				section = append(section,
					fmt.Sprintf("  + %s → phelps carried: %s  [%s]", sid, carriedPhelps, matchMethod))
			} else if matchMethod != "" {
				// Content matched but phelps was empty
				section = append(section,
					fmt.Sprintf("  ~ %s → content matched [%s] but old phelps was empty", sid, matchMethod))
				totalUnmatch++
				langUnmatched = append(langUnmatched,
					fmt.Sprintf("    %s/%s  %s", iso, sid, apiName))
			} else {
				totalUnmatch++
				langUnmatched = append(langUnmatched,
					fmt.Sprintf("    %s/%s  %s", iso, sid, apiName))
			}

			langInserts = append(langInserts, makeInsert(p, iso, apiID, prayerName, carriedPhelps))
		}
		totalNew += len(newIDs)

		// Removed entries → commented DELETEs
		sort.Slice(removedIDs, func(i, j int) bool {
			a, _ := strconv.Atoi(removedIDs[i])
			b, _ := strconv.Atoi(removedIDs[j])
			return a < b
		})
		langDeletes := []string{fmt.Sprintf("\n-- %s (%s) — %d removed prayers", iso, name, len(removedIDs))}
		for _, sid := range removedIDs {
			e := dbEntries[sid]
			phelps := e.Phelps
			if phelps == "" {
				phelps = "NULL"
			}
			langDeletes = append(langDeletes,
				fmt.Sprintf("-- DELETE FROM writings WHERE source='bahaiprayers.net' AND language='%s' AND source_id='%s'; -- phelps=%s",
					iso, sid, phelps))
		}
		totalRemoved += len(removedIDs)

		insertLines = append(insertLines, langInserts...)
		deleteLines = append(deleteLines, langDeletes...)
		reportLines = append(reportLines, section...)

		if len(langUnmatched) > 0 {
			diff := matchDifficulty(iso)
			cmd := matchCommand(iso)
			unmatched[iso] = &Unmatched{
				Name:    name,
				Prayers: langUnmatched,
				Diff:    diff,
			}
			_ = cmd
		}
	}

	// Build summary
	summary := []string{
		"bahaiprayers.net SYNC REPORT",
		fmt.Sprintf("Languages with changes:  %d: %s", len(langsChanged), strings.Join(langsChanged, ", ")),
		fmt.Sprintf("New prayers total:       %d", totalNew),
		fmt.Sprintf("  phelps carried over:   %d", totalCarried),
		fmt.Sprintf("  need new matching:     %d", totalUnmatch),
		fmt.Sprintf("Removed prayers total:   %d", totalRemoved),
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

	// Write report
	writeFile("/tmp/sync_report.txt", append(summary, reportLines...)...)

	// Write inserts
	writeFile("/tmp/sync_inserts.sql",
		append([]string{
			"-- New prayers from bahaiprayers.net API sync",
			"-- Apply with: dolt sql < /tmp/sync_inserts.sql",
			"",
		}, insertLines...)...)

	// Write deletes
	writeFile("/tmp/sync_deletes.sql",
		append([]string{
			"-- REMOVED prayers (commented out — review before uncommenting)",
			"-- These source_ids are in the DB but no longer returned by the API",
			"",
		}, deleteLines...)...)

	// Write unmatched grouped by difficulty
	var unmatchedLines []string
	unmatchedLines = append(unmatchedLines,
		"PRAYERS NEEDING NEW PHELPS MATCHING",
		strings.Repeat("=", 60), "")

	for _, label := range []string{"EASY", "MEDIUM", "HARD"} {
		first := true
		// Sort languages for stable output
		isos := make([]string, 0)
		for iso := range unmatched {
			if strings.HasPrefix(unmatched[iso].Diff, label) {
				isos = append(isos, iso)
			}
		}
		sort.Strings(isos)
		for _, iso := range isos {
			u := unmatched[iso]
			if first {
				unmatchedLines = append(unmatchedLines,
					fmt.Sprintf("\n%s LANGUAGES", label),
					strings.Repeat("-", 40))
				first = false
			}
			unmatchedLines = append(unmatchedLines,
				fmt.Sprintf("\n%s (%s) — %d prayers", iso, u.Name, len(u.Prayers)),
				fmt.Sprintf("  Strategy: %s", u.Diff),
				fmt.Sprintf("  Command:  %s", matchCommand(iso)),
			)
			unmatchedLines = append(unmatchedLines, u.Prayers...)
		}
	}
	writeFile("/tmp/sync_unmatched.txt", unmatchedLines...)

	fmt.Println("\nOutputs:")
	fmt.Println("  /tmp/sync_report.txt    — full language-by-language report")
	fmt.Println("  /tmp/sync_inserts.sql   — INSERT statements")
	fmt.Println("  /tmp/sync_deletes.sql   — removed entries (COMMENTED — review first!)")
	fmt.Println("  /tmp/sync_unmatched.txt — prayers needing matching, by difficulty")
	fmt.Println("\nNext steps:")
	fmt.Println("  dolt sql < /tmp/sync_inserts.sql")
	fmt.Println("  # review /tmp/sync_deletes.sql, then uncomment and apply")
}

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

// Silence unused import
var _ io.Reader
