// match.go — Unified prayer matching tool
// Consolidates: gemini_batch_match.py, rosetta_match.py, match_embedded_headers.py
//
// Usage:
//   go run match.go --lang XX [options]
//
// Strategies (--strategy, default=auto):
//   auto     : smart iterative matching — builds vocab if needed, loops until
//              convergence, falls back rosetta→standard→translate automatically
//   standard : send batches to Gemini with full inventory context
//   rosetta  : category-narrowed matching with cross-language examples
//
// Flags:
//   --translate     In-prompt translation (Gemini translates first, then identifies)
//   --pretranslate  Use trans CLI to pre-translate prayer text
//   --build-vocab   (Re)build Rosetta vocabulary from matched prayers then exit
//   --batch-size N    Prayers per Gemini call (default 6 standard, 3 rosetta)
//   --loops N         Max rounds in auto mode (default auto=12, explicit=1)
//   --dry-run         Print SQL but don't apply to DB
//   --recheck-tmps    Include TMP-coded prayers; uses translate+pretranslate fallbacks
//   --source S        DB source column value to target (default: bahaiprayers.net)
//   --propose         Write JSON proposals instead of applying directly
//   --out FILE        Output path for proposals (default: /tmp/<lang>_proposals.json)
//   --detect-duplicates  Find prayers where multiple texts share one code

package main

import (
	"bufio"
	"bytes"
	"crypto/md5"
	"encoding/csv"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync/atomic"
	"time"
	"unicode"
)

// ── Configuration ───────────────────────────────────────────────────────────

const geminiModel = "gemini-2.5-flash-lite"

var defaultDoltDir = os.Expand("$HOME/bahaiwritings", os.Getenv)
var defaultInvCSV = os.Expand("$HOME/prayermatching/data/inventory_export.csv", os.Getenv)

// prayerSource is the DB source column value to read/write. Set from --source flag.
var prayerSource = "bahaiprayers.net"

// Phelps codes known to produce false positives
var falsePositiveCodes = map[string]bool{
	"ABU0030": true, "ABU0196": true, "ABU0394": true, "AB00049": true,
}

// English Bahá'í prayer category names (for Rosetta decoding)
var knownEnCategories = []string{
	"Aid and Assistance", "Children", "Departed", "Detachment", "The Fast",
	"Forgiveness", "Gatherings", "Healing", "Marriage", "Morning", "Evening",
	"Protection", "Praise and Gratitude", "Ridván", "Spiritual Growth",
	"Steadfastness", "Teaching", "Tests and Difficulties", "Women", "Youth",
	"Short Obligatory Prayer", "Medium Obligatory Prayer", "Long Obligatory Prayer",
	"Additional Prayers Revealed by Bahá'u'lláh", "Additional Prayers Revealed by 'Abdu'l‑Bahá",
	"Huqúqu'lláh", "Tablets", "Other",
}

// Reference languages for cross-language examples in Rosetta matching
var referenceLangs = []string{"en", "de", "fr", "sw", "sm", "id", "pt"}

// ── Dolt helpers ─────────────────────────────────────────────────────────────

func doltQuery(doltDir, query string) []map[string]string {
	cmd := exec.Command("dolt", "sql", "-q", query, "--result-format", "csv")
	cmd.Dir = doltDir
	out, err := cmd.Output()
	if err != nil {
		fmt.Fprintf(os.Stderr, "  [dolt error] %v\n", err)
		return nil
	}
	r := csv.NewReader(bytes.NewReader(out))
	rows, _ := r.ReadAll()
	if len(rows) < 2 {
		return nil
	}
	header := rows[0]
	result := make([]map[string]string, 0, len(rows)-1)
	for _, row := range rows[1:] {
		m := make(map[string]string, len(header))
		for i, h := range header {
			if i < len(row) {
				m[h] = row[i]
			}
		}
		result = append(result, m)
	}
	return result
}

func doltExec(doltDir, sql string) error {
	cmd := exec.Command("dolt", "sql", "-q", sql)
	cmd.Dir = doltDir
	out, err := cmd.CombinedOutput()
	if err != nil {
		fmt.Fprintf(os.Stderr, "  [dolt exec error] %v: %s\n", err, string(out))
	}
	return err
}

func sqlEsc(s string) string {
	return strings.NewReplacer(`\`, `\\`, `'`, `''`).Replace(s)
}

// ── Dolt write server ─────────────────────────────────────────────────────────
// Serializes all Dolt write (EXEC) calls so parallel match processes don't
// contend on the DB write lock. The first process to run auto-starts the server;
// subsequent processes connect to the running socket.

const serverSock = "/tmp/match-dolt.sock"

var serverDoltDir string      // set when running as server
var activeConns int64         // atomic counter of open client connections
var serverShutdown chan struct{} // closed by main() to stop the server

type writeReq struct {
	sql   string
	reply chan error
}

// runServer starts the write-serializing server. A single goroutine drains
// the writeCh channel so all dolt SQL executions are strictly sequential —
// preventing the Dolt file-lock contention that occurs with parallel processes.
func runServer(doltDir string) {
	os.Remove(serverSock)
	l, err := net.Listen("unix", serverSock)
	if err != nil {
		fmt.Fprintf(os.Stderr, "[server] listen error: %v\n", err)
		return
	}
	defer os.Remove(serverSock)
	serverDoltDir = doltDir

	// Single serialized writer goroutine — the only place doltExec is called
	writeCh := make(chan writeReq, 64)
	go func() {
		for req := range writeCh {
			req.reply <- doltExec(doltDir, req.sql)
		}
	}()

	fmt.Fprintf(os.Stderr, "[server] Dolt write server started (%s)\n", serverSock)

	// Shutdown when main() signals done (closes serverShutdown channel)
	go func() {
		<-serverShutdown
		l.Close()
	}()

	for {
		conn, err := l.Accept()
		if err != nil {
			break
		}
		atomic.AddInt64(&activeConns, 1)
		go func(c net.Conn) {
			defer func() {
				c.Close()
				atomic.AddInt64(&activeConns, -1)
			}()
			scanner := bufio.NewScanner(c)
			scanner.Buffer(make([]byte, 1<<20), 1<<20)
			for scanner.Scan() {
				req := writeReq{sql: scanner.Text(), reply: make(chan error, 1)}
				writeCh <- req
				if err := <-req.reply; err != nil {
					fmt.Fprintf(c, "ERROR:%v\n", err)
				} else {
					fmt.Fprintln(c, "OK")
				}
			}
		}(conn)
	}
	close(writeCh)
	fmt.Fprintln(os.Stderr, "[server] Shutting down.")
}

// ensureServer starts the write server in background if it isn't running yet.
// Returns true if we are the server (caller should not proceed with matching).
func ensureServer(doltDir string) {
	if conn, err := net.Dial("unix", serverSock); err == nil {
		conn.Close()
		return // Already running
	}
	// Start server as a background goroutine in this process
	go runServer(doltDir)
	// Wait for it to be ready (up to 2s)
	for i := 0; i < 20; i++ {
		time.Sleep(100 * time.Millisecond)
		if conn, err := net.Dial("unix", serverSock); err == nil {
			conn.Close()
			return
		}
	}
	fmt.Fprintln(os.Stderr, "[warn] Server did not start in time — using direct writes")
}

// serverExec sends a write SQL to the server (with retry, or falls back to direct doltExec).
func serverExec(doltDir, sql string) error {
	var conn net.Conn
	var err error
	for i := 0; i < 10; i++ {
		conn, err = net.Dial("unix", serverSock)
		if err == nil {
			break
		}
		time.Sleep(time.Duration(200+i*100) * time.Millisecond)
	}
	if err != nil {
		// No server after retries — fall back to direct write
		return doltExec(doltDir, sql)
	}
	defer conn.Close()
	fmt.Fprintln(conn, sql)
	scanner := bufio.NewScanner(conn)
	scanner.Buffer(make([]byte, 1<<20), 1<<20)
	if scanner.Scan() {
		resp := scanner.Text()
		if strings.HasPrefix(resp, "ERROR:") {
			return fmt.Errorf("%s", resp[6:])
		}
	}
	return nil
}

// sanitizeCode strips prompt-echo garbage from a Gemini-returned phelps code.
// Gemini sometimes echoes the placeholder text (e.g. "phelps_code:BH00074BLE",
// "code:BH00074BLE", "PIN:BH00074BLE"). Strip any word-colon prefix that isn't VERIFY.
var sanitizeCodeRe = regexp.MustCompile(`(?i)^(?:phelps[_\-]?code|pin(?:_or_verify)?|code|verify):`)
var validPhelpsRe = regexp.MustCompile(`^(?:AB|BH|BB|ABU|TMP|XAB|XBH|XBB)\d`)

func sanitizeCode(s string) string {
	s = strings.TrimSpace(s)
	s = sanitizeCodeRe.ReplaceAllString(s, "")
	return strings.TrimSpace(s)
}

// ── Inventory ───────────────────────────────────────────────────────────────

type InvEntry struct {
	PIN      string
	FirstEN  string
	OrigLine string
}

func loadInventory(csvPath string) map[string]InvEntry {
	f, err := os.Open(csvPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "  [error] Cannot open inventory: %v\n", err)
		return map[string]InvEntry{}
	}
	defer f.Close()
	inv := make(map[string]InvEntry)
	r := csv.NewReader(f)
	r.LazyQuotes = true
	rows, _ := r.ReadAll()
	for i, row := range rows {
		if i == 0 || len(row) < 3 {
			continue
		}
		pin := strings.TrimSpace(row[0])
		orig := strings.TrimSpace(row[2])
		en := ""
		if len(row) >= 4 {
			en = strings.TrimSpace(row[3])
		}
		if en == "" {
			en = orig
		}
		inv[pin] = InvEntry{PIN: pin, FirstEN: en, OrigLine: orig}
	}
	return inv
}

// ── Unresolved prayers ───────────────────────────────────────────────────────

type Prayer struct {
	SourceID    string
	Language    string
	Text        string
	Header      string // Extracted ## header if present
	EnglishHint string // English version fetched from bahaiprayers.app (when source=bahaiprayers.app)
}

// loadAllPrayers loads every prayer for a language (for --recheck-all mode).
func loadAllPrayers(doltDir, lang string) []Prayer {
	rows := doltQuery(doltDir,
		fmt.Sprintf(`SELECT source_id, language, phelps, LEFT(text,400) as text FROM writings `+
			`WHERE source='%s' AND language='%s' ORDER BY CAST(source_id AS UNSIGNED)`,
			sqlEsc(prayerSource), lang))
	prayers := make([]Prayer, 0, len(rows))
	for _, row := range rows {
		text := row["text"]
		header := ""
		if strings.HasPrefix(text, "## ") {
			lines := strings.SplitN(text, "\n", 2)
			header = strings.TrimPrefix(lines[0], "## ")
		}
		prayers = append(prayers, Prayer{
			SourceID: row["source_id"],
			Language: lang,
			Text:     text,
			Header:   header,
		})
	}
	return prayers
}

func loadUnresolved(doltDir, lang string, recheckTMPs bool) []Prayer {
	cond := `(phelps IS NULL OR phelps='')`
	if recheckTMPs {
		cond = `(phelps IS NULL OR phelps='' OR phelps LIKE 'TMP%')`
	}
	rows := doltQuery(doltDir,
		fmt.Sprintf(`SELECT source_id, language, LEFT(text,400) as text FROM writings `+
			`WHERE %s AND source='%s' `+
			`AND language='%s' ORDER BY CAST(source_id AS UNSIGNED)`, cond, sqlEsc(prayerSource), lang))
	prayers := make([]Prayer, 0, len(rows))
	for _, row := range rows {
		text := row["text"]
		header := ""
		if strings.HasPrefix(text, "## ") {
			lines := strings.SplitN(text, "\n", 2)
			header = strings.TrimPrefix(lines[0], "## ")
		}
		prayers = append(prayers, Prayer{
			SourceID: row["source_id"],
			Language: lang,
			Text:     text,
			Header:   header,
		})
	}
	return prayers
}

// ── Gemini ───────────────────────────────────────────────────────────────────

func geminiCall(prompt string, retries int) string {
	for attempt := 0; attempt < retries; attempt++ {
		if attempt > 0 {
			fmt.Fprintf(os.Stderr, "  [retry %d]\n", attempt)
			time.Sleep(time.Duration(3*(attempt)) * time.Second)
		}
		cmd := exec.Command("gemini", "-m", geminiModel)
		cmd.Stdin = strings.NewReader(prompt)
		var stdout, stderr bytes.Buffer
		cmd.Stdout = &stdout
		cmd.Stderr = &stderr

		done := make(chan error, 1)
		if err := cmd.Start(); err != nil {
			continue
		}
		go func() { done <- cmd.Wait() }()
		select {
		case err := <-done:
			if err == nil || stdout.Len() > 0 {
				return strings.TrimSpace(stdout.String())
			}
		case <-time.After(90 * time.Second):
			cmd.Process.Kill()
			fmt.Fprintf(os.Stderr, "  [timeout] Gemini timed out (attempt %d)\n", attempt+1)
		}
	}
	return ""
}

// extractJSON finds the first valid JSON object in a string, handling code blocks
func extractJSON(s string) map[string]string {
	// Strip markdown code blocks
	s = regexp.MustCompile("```(?:json)?\\s*").ReplaceAllString(s, "")
	s = strings.ReplaceAll(s, "```", "")

	// Try simple non-nested objects first (handles "Extra data" cases)
	for _, m := range regexp.MustCompile(`\{[^{}]+\}`).FindAllString(s, -1) {
		var result map[string]string
		if json.Unmarshal([]byte(m), &result) == nil {
			return result
		}
	}
	// Fallback: greedy match
	if m := regexp.MustCompile(`\{[\s\S]+\}`).FindString(s); m != "" {
		var result map[string]string
		json.Unmarshal([]byte(m), &result)
		return result
	}
	return nil
}

func extractJSONAny(s string) map[string]interface{} {
	s = regexp.MustCompile("```(?:json)?\\s*").ReplaceAllString(s, "")
	s = strings.ReplaceAll(s, "```", "")
	for _, m := range regexp.MustCompile(`\{[^{}]+\}`).FindAllString(s, -1) {
		var result map[string]interface{}
		if json.Unmarshal([]byte(m), &result) == nil {
			return result
		}
	}
	if m := regexp.MustCompile(`\{[\s\S]+\}`).FindString(s); m != "" {
		var result map[string]interface{}
		json.Unmarshal([]byte(m), &result)
		return result
	}
	return nil
}

// ── Match results ─────────────────────────────────────────────────────────────

type MatchResult struct {
	SourceID string
	Phelps   string
	EnFirst  string
	Category string
	Verify   bool
}

func outputSQL(lang string, results []MatchResult, uncertain []Prayer) {
	fmt.Printf("\n-- SQL UPDATE statements for %s (match.go) --\n", lang)
	fmt.Printf("-- Total matches: %d\n", len(results))
	fmt.Printf("-- Uncertain: %d\n", len(uncertain))
	fmt.Println()
	for _, r := range results {
		tag := ""
		if r.Verify {
			tag = "-- VERIFY-WARN -- "
		}
		fmt.Printf("%s-- %s -- id=%s (%s) match=%q\n", tag, r.Category, r.SourceID, lang, r.EnFirst)
		fmt.Printf("UPDATE writings SET phelps='%s' WHERE source_id='%s' AND language='%s' AND source='%s';\n\n",
			sqlEsc(r.Phelps), sqlEsc(r.SourceID), sqlEsc(lang), sqlEsc(prayerSource))
	}
	if len(uncertain) > 0 {
		fmt.Println("\n-- RETRY LIST --")
		for _, p := range uncertain {
			txt := strings.ReplaceAll(strings.TrimSpace(p.Text), "\n", " ")
			if len(txt) > 70 {
				txt = txt[:70]
			}
			fmt.Printf("-- %s/%s: %q\n", lang, p.SourceID, txt)
		}
	}
}

// ── Rosetta Vocabulary ────────────────────────────────────────────────────────

type VocabEntry struct {
	LocalTerm  string
	EnMeaning  string
	TermType   string // category, deity, address, keyword, phrase
	SrcPhelps  string
}

func loadRosettaVocab(doltDir, lang string) []VocabEntry {
	rows := doltQuery(doltDir,
		fmt.Sprintf(`SELECT local_term, en_meaning, term_type, COALESCE(source_phelps,'') as sp FROM rosetta_vocab WHERE language='%s'`, lang))
	entries := make([]VocabEntry, 0, len(rows))
	for _, row := range rows {
		entries = append(entries, VocabEntry{
			LocalTerm: row["local_term"],
			EnMeaning: row["en_meaning"],
			TermType:  row["term_type"],
			SrcPhelps: row["sp"],
		})
	}
	return entries
}

func saveVocabEntry(doltDir string, e VocabEntry) {
	sp := "NULL"
	if e.SrcPhelps != "" {
		sp = fmt.Sprintf("'%s'", sqlEsc(e.SrcPhelps))
	}
	serverExec(doltDir, fmt.Sprintf(
		`INSERT INTO rosetta_vocab (language, local_term, en_meaning, term_type, source_phelps) `+
			`VALUES ('%s', '%s', '%s', '%s', %s) `+
			`ON DUPLICATE KEY UPDATE en_meaning=VALUES(en_meaning), term_type=VALUES(term_type)`,
		sqlEsc(e.TermType[:0]+""), // language placeholder — fill below
		sqlEsc(e.LocalTerm), sqlEsc(e.EnMeaning), e.TermType, sp))
}

func saveVocabEntries(doltDir, lang string, entries []VocabEntry) {
	for _, e := range entries {
		sp := "NULL"
		if e.SrcPhelps != "" {
			sp = fmt.Sprintf("'%s'", sqlEsc(e.SrcPhelps))
		}
		serverExec(doltDir, fmt.Sprintf(
			`INSERT INTO rosetta_vocab (language, local_term, en_meaning, term_type, source_phelps) `+
				`VALUES ('%s', '%s', '%s', '%s', %s) `+
				`ON DUPLICATE KEY UPDATE en_meaning=VALUES(en_meaning), term_type=VALUES(term_type)`,
			sqlEsc(lang), sqlEsc(e.LocalTerm), sqlEsc(e.EnMeaning), sqlEsc(e.TermType), sp))
	}
}

// ── Build vocabulary (Rosetta Phase 1) ───────────────────────────────────────

func buildVocab(doltDir, lang string, inv map[string]InvEntry) {
	fmt.Fprintf(os.Stderr, "\n=== Building Rosetta vocabulary for %s ===\n", lang)

	// Step A: decode category headers
	existingRows := doltQuery(doltDir, fmt.Sprintf(
		`SELECT local_term FROM rosetta_vocab WHERE language='%s' AND term_type='category'`, lang))
	existingCats := map[string]bool{}
	for _, r := range existingRows {
		existingCats[r["local_term"]] = true
	}

	headerRows := doltQuery(doltDir, fmt.Sprintf(
		`SELECT DISTINCT SUBSTRING_INDEX(text, '\n', 1) as hdr FROM writings WHERE language='%s' AND source='%s'`, lang, sqlEsc(prayerSource)))
	var newHeaders []string
	for _, r := range headerRows {
		h := strings.TrimSpace(r["hdr"])
		if strings.HasPrefix(h, "## ") {
			h = strings.TrimPrefix(h, "## ")
			if !existingCats[h] {
				newHeaders = append(newHeaders, h)
			}
		}
	}

	if len(newHeaders) > 0 {
		fmt.Fprintf(os.Stderr, "  Decoding %d new category headers...\n", len(newHeaders))
		var hLines []string
		for _, h := range newHeaders {
			hLines = append(hLines, "- "+h)
		}
		prompt := fmt.Sprintf(
			"These are prayer book chapter headings in language '%s'.\n"+
				"Match each to the closest English Bahá'í prayer category from:\n%s\n\n"+
				"Headings:\n%s\n\n"+
				"Reply ONLY with valid JSON: {\"heading\": \"English category\", ...}",
			lang, strings.Join(knownEnCategories, "\n"), strings.Join(hLines, "\n"))

		resp := geminiCall(prompt, 3)
		if m := extractJSONAny(resp); m != nil {
			var entries []VocabEntry
			for local, en := range m {
				enStr, _ := en.(string)
				if enStr == "" {
					continue
				}
				entries = append(entries, VocabEntry{LocalTerm: local, EnMeaning: enStr, TermType: "category"})
				fmt.Fprintf(os.Stderr, "    %q -> %q\n", local, enStr)
			}
			saveVocabEntries(doltDir, lang, entries)
		}
		time.Sleep(2 * time.Second)
	}

	// Step B: extract vocabulary from matched prayers
	matched := doltQuery(doltDir, fmt.Sprintf(
		`SELECT source_id, phelps, LEFT(text,400) as text FROM writings `+
			`WHERE language='%s' AND source='%s' AND phelps IS NOT NULL AND phelps <> '' LIMIT 40`, lang, sqlEsc(prayerSource)))
	if len(matched) == 0 {
		fmt.Fprintf(os.Stderr, "  No matched prayers for vocab extraction\n")
		return
	}
	fmt.Fprintf(os.Stderr, "  Extracting vocabulary from %d matched prayers...\n", len(matched))

	batchSize := 5
	for i := 0; i < len(matched); i += batchSize {
		batch := matched[i:]
		if len(batch) > batchSize {
			batch = batch[:batchSize]
		}
		var pairs []string
		for _, row := range batch {
			enFirst := ""
			if e, ok := inv[row["phelps"]]; ok {
				enFirst = e.FirstEN
			}
			txt := stripHeader(row["text"])
			if len(txt) > 200 {
				txt = txt[:200]
			}
			pairs = append(pairs, fmt.Sprintf("[%s] English: %q\n%s text: %s", row["phelps"], enFirst, lang, txt))
		}
		prompt := fmt.Sprintf(
			"Below are Bahá'í prayers in language '%s', each with its English first line.\n"+
				"Extract a mini-dictionary: words for God/Lord/Thou/Thy, address forms (O God!, O Lord!),\n"+
				"and key terms (servant, praise, glory, mercy, forgiveness, etc.).\n\n%s\n\n"+
				"Reply ONLY with JSON: {\"local_term\": \"English meaning\", ...}\n"+
				"Focus on %s terms → English meanings.",
			lang, strings.Join(pairs, "\n\n"), lang)

		resp := geminiCall(prompt, 3)
		if m := extractJSONAny(resp); m != nil {
			var entries []VocabEntry
			for local, en := range m {
				enStr, _ := en.(string)
				if len(local) < 2 || len(enStr) < 2 {
					continue
				}
				ttype := classifyVocabType(enStr)
				entries = append(entries, VocabEntry{LocalTerm: local, EnMeaning: enStr, TermType: ttype})
				fmt.Fprintf(os.Stderr, "    [%s] %q = %q\n", ttype, local, enStr)
			}
			saveVocabEntries(doltDir, lang, entries)
		}
		time.Sleep(3 * time.Second)
	}
	fmt.Fprintf(os.Stderr, "  Vocabulary build complete for %s\n", lang)
}

func classifyVocabType(en string) string {
	lo := strings.ToLower(en)
	if strings.Contains(lo, "god") || strings.Contains(lo, "lord") ||
		strings.Contains(lo, "thou") || strings.Contains(lo, "thy") || strings.Contains(lo, "thee") {
		return "deity"
	}
	if strings.HasPrefix(lo, "o ") {
		return "address"
	}
	return "keyword"
}

// ── Rosetta matching (Phase 2) ────────────────────────────────────────────────

func getCategoryPhelps(doltDir, enCat string) []string {
	rows := doltQuery(doltDir, fmt.Sprintf(
		`SELECT phelps_code FROM prayer_book_structure WHERE source_language='en:bp' `+
			`AND category_name='%s' ORDER BY order_in_category`, sqlEsc(enCat)))
	codes := make([]string, 0, len(rows))
	for _, r := range rows {
		codes = append(codes, r["phelps_code"])
	}
	return codes
}

func getReferenceOpenings(doltDir string, phelps []string, langs []string) map[string]map[string]string {
	if len(phelps) == 0 {
		return nil
	}
	// Cap at 10 codes to keep query small
	if len(phelps) > 10 {
		phelps = phelps[:10]
	}
	var quotedCodes, quotedLangs []string
	for _, c := range phelps {
		quotedCodes = append(quotedCodes, "'"+sqlEsc(c)+"'")
	}
	for _, l := range langs {
		quotedLangs = append(quotedLangs, "'"+sqlEsc(l)+"'")
	}
	rows := doltQuery(doltDir, fmt.Sprintf(
		`SELECT phelps, language, LEFT(text,120) as opening FROM writings `+
			`WHERE phelps IN (%s) AND language IN (%s) AND source='bahaiprayers.net' `+
			`ORDER BY phelps, language`,
		strings.Join(quotedCodes, ","), strings.Join(quotedLangs, ",")))
	result := map[string]map[string]string{}
	for _, r := range rows {
		p := r["phelps"]
		if result[p] == nil {
			result[p] = map[string]string{}
		}
		result[p][r["language"]] = stripHeader(r["opening"])
	}
	return result
}

func runRosettaMatch(prayers []Prayer, lang, doltDir string, vocab []VocabEntry,
	inv map[string]InvEntry, batchSize int) ([]MatchResult, []Prayer) {

	// Build lookup maps from vocab
	catMap := map[string]string{}
	deityMap := map[string]string{}
	keyMap := map[string]string{}
	for _, e := range vocab {
		switch e.TermType {
		case "category":
			catMap[e.LocalTerm] = e.EnMeaning
		case "deity", "address":
			deityMap[e.LocalTerm] = e.EnMeaning
		default:
			keyMap[e.LocalTerm] = e.EnMeaning
		}
	}

	// Group prayers by category header
	byHeader := map[string][]Prayer{}
	headerOrder := []string{}
	seen := map[string]bool{}
	for _, p := range prayers {
		h := p.Header
		if h == "" {
			h = "_none_"
		}
		if !seen[h] {
			headerOrder = append(headerOrder, h)
			seen[h] = true
		}
		byHeader[h] = append(byHeader[h], p)
	}

	var results []MatchResult
	var uncertain []Prayer

	for _, header := range headerOrder {
		batch := byHeader[header]
		enCat := catMap[header]
		if enCat == "" {
			// Try partial match
			for k, v := range catMap {
				if strings.Contains(strings.ToLower(header), strings.ToLower(k)) ||
					strings.Contains(strings.ToLower(k), strings.ToLower(header)) {
					enCat = v
					break
				}
			}
		}
		var candidates []string
		if enCat != "" && enCat != "Other" {
			candidates = getCategoryPhelps(doltDir, enCat)
			if len(candidates) > 12 {
				candidates = candidates[:12]
			}
		}
		if len(candidates) == 0 {
			fmt.Fprintf(os.Stderr, "    [skip] no candidates for %q\n", header)
			uncertain = append(uncertain, batch...)
			continue
		}

		refOpenings := getReferenceOpenings(doltDir, candidates, referenceLangs)
		fmt.Fprintf(os.Stderr, "  Category: %q -> %q (%d candidates, %d prayers)\n",
			header, enCat, len(candidates), len(batch))

		// Build candidate list text
		var candLines []string
		for _, code := range candidates {
			en := ""
			if e, ok := inv[code]; ok {
				en = e.FirstEN
			}
			if len(en) > 70 {
				en = en[:70]
			}
			refs := ""
			if rm := refOpenings[code]; rm != nil {
				var rParts []string
				for _, l := range []string{"sm", "id", "sw", "de", "fr"} {
					if v, ok := rm[l]; ok && v != "" {
						if len(v) > 45 {
							v = v[:45]
						}
						rParts = append(rParts, l+": "+v)
						if len(rParts) >= 2 {
							break
						}
					}
				}
				if len(rParts) > 0 {
					refs = " [" + strings.Join(rParts, " | ") + "]"
				}
			}
			candLines = append(candLines, fmt.Sprintf("  %s: %q%s", code, en, refs))
		}
		candText := strings.Join(candLines, "\n")

		// Process in sub-batches
		for i := 0; i < len(batch); i += batchSize {
			subBatch := batch[i:]
			if len(subBatch) > batchSize {
				subBatch = subBatch[:batchSize]
			}

			var prayerBlocks []string
			for _, p := range subBatch {
				txt := stripHeader(p.Text)
				if len(txt) > 250 {
					txt = txt[:250]
				}
				// Annotate known terms
				var annots []string
				for local, en := range deityMap {
					if strings.Contains(txt, local) {
						annots = append(annots, fmt.Sprintf("%q=%q", local, en))
						if len(annots) >= 4 {
							break
						}
					}
				}
				annot := ""
				if len(annots) > 0 {
					annot = "\nKnown terms: " + strings.Join(annots, "; ")
				}
				prayerBlocks = append(prayerBlocks, fmt.Sprintf("[id=%s]%s\n%s", p.SourceID, annot, txt))
			}
			prayersText := strings.Join(prayerBlocks, "\n\n---\n\n")

			prompt := fmt.Sprintf(
				"Matching Bahá'í prayers in language '%s' to Phelps codes.\n\n"+
					"Section: %q (= %q)\n\n"+
					"Candidate prayers (code: English opening [cross-lang examples]):\n%s\n\n"+
					"For each prayer below, pick the BEST matching candidate using:\n"+
					"- Opening address to God, main theme/request, vocabulary annotations\n\n"+
					"Prayers:\n\n%s\n\n"+
					"Reply with JSON: {\"SOURCE_ID\": \"code_or_VERIFY:code\", ...}\n"+
					"Use VERIFY:code if uncertain. Use NULL only if prayer clearly doesn't fit any candidate.",
				lang, header, enCat, candText, prayersText)

			resp := geminiCall(prompt, 3)
			idMap := extractJSON(resp)
			if idMap == nil {
				fmt.Fprintf(os.Stderr, "    [warn] No valid JSON: %q\n", resp[:min(80, len(resp))])
				uncertain = append(uncertain, subBatch...)
				continue
			}

			for _, p := range subBatch {
				raw := strings.TrimSpace(idMap[p.SourceID])
				verify := strings.HasPrefix(strings.ToUpper(raw), "VERIFY:")
				code := raw
				if verify {
					code = raw[7:]
				}
				code = sanitizeCode(code)
				upper := strings.ToUpper(code)
				if code == "" || upper == "NULL" || upper == "NONE" || falsePositiveCodes[code] {
					fmt.Fprintf(os.Stderr, "    no match id=%s\n", p.SourceID)
					uncertain = append(uncertain, p)
					continue
				}
				en := ""
				if e, ok := inv[code]; ok {
					en = e.FirstEN
				}
				tag := "MATCH"
				if verify {
					tag = "VERIFY"
				}
				fmt.Fprintf(os.Stderr, "    %s id=%s: %s %q\n", tag, p.SourceID, code, en[:min(50, len(en))])
				results = append(results, MatchResult{
					SourceID: p.SourceID, Phelps: code, EnFirst: en,
					Category: enCat, Verify: verify,
				})
			}
			time.Sleep(2 * time.Second)
		}
	}
	return results, uncertain
}

// ── Standard matching ─────────────────────────────────────────────────────────

func buildInventoryContext(inv map[string]InvEntry) string {
	var lines []string
	for pin, e := range inv {
		en := e.FirstEN
		if len(en) > 80 {
			en = en[:80]
		}
		lines = append(lines, pin+": "+en)
	}
	// Keep to ~500 entries to avoid huge prompts
	if len(lines) > 500 {
		lines = lines[:500]
	}
	return strings.Join(lines, "\n")
}

func runStandardMatch(prayers []Prayer, lang string, inv map[string]InvEntry,
	translate, pretranslate bool, batchSize int) ([]MatchResult, []Prayer) {

	var results []MatchResult
	var uncertain []Prayer

	// Pre-translate if requested
	if pretranslate {
		prayers = pretranslatePrayers(prayers, lang)
	}

	invContext := buildInventoryContext(inv)

	for i := 0; i < len(prayers); i += batchSize {
		batch := prayers[i:]
		if len(batch) > batchSize {
			batch = batch[:batchSize]
		}

		var blocks []string
		for _, p := range batch {
			txt := stripHeader(p.Text)
			if len(txt) > 300 {
				txt = txt[:300]
			}
			blocks = append(blocks, fmt.Sprintf("[id=%s]\n%s", p.SourceID, txt))
		}
		prayersText := strings.Join(blocks, "\n\n---\n\n")

		translateHint := ""
		if translate {
			translateHint = "First translate each prayer to English, then identify.\n"
		}

		prompt := fmt.Sprintf(
			"Match Bahá'í prayers in language '%s' to Phelps inventory codes.\n"+
				"%s\n"+
				"Inventory (PIN: English first line):\n%s\n\n"+
				"Prayers to identify:\n\n%s\n\n"+
				"Reply with JSON: {\"SOURCE_ID\": \"PIN_or_VERIFY:PIN\", ...}\n"+
				"Use VERIFY:PIN if uncertain. Use NULL if no match found.\n"+
				"Only use codes from the inventory above.",
			lang, translateHint, invContext, prayersText)

		resp := geminiCall(prompt, 3)
		idMap := extractJSON(resp)
		if idMap == nil {
			uncertain = append(uncertain, batch...)
			continue
		}

		for _, p := range batch {
			raw := strings.TrimSpace(idMap[p.SourceID])
			verify := strings.HasPrefix(strings.ToUpper(raw), "VERIFY:")
			code := raw
			if verify {
				code = raw[7:]
			}
			code = sanitizeCode(code)
			upper := strings.ToUpper(code)
			if code == "" || upper == "NULL" || upper == "NONE" || falsePositiveCodes[code] || !validPhelpsRe.MatchString(code) {
				uncertain = append(uncertain, p)
				continue
			}
			en := ""
			if e, ok := inv[code]; ok {
				en = e.FirstEN
			}
			tag := "MATCH"
			if verify {
				tag = "VERIFY"
			}
			fmt.Fprintf(os.Stderr, "  %s id=%s: %s %q\n", tag, p.SourceID, code, en[:min(50, len(en))])
			results = append(results, MatchResult{
				SourceID: p.SourceID, Phelps: code, EnFirst: en, Verify: verify,
			})
		}
		time.Sleep(2 * time.Second)
	}
	return results, uncertain
}

// ── Pre-translation ───────────────────────────────────────────────────────────

func pretranslatePrayers(prayers []Prayer, lang string) []Prayer {
	result := make([]Prayer, len(prayers))
	for i, p := range prayers {
		txt := stripHeader(p.Text)
		if len(txt) > 300 {
			txt = txt[:300]
		}
		cmd := exec.Command("trans", "-brief", "-no-auto", lang+":en", txt)
		out, err := cmd.Output()
		if err == nil && len(out) > 0 {
			result[i] = Prayer{
				SourceID: p.SourceID, Language: p.Language,
				Text:   "## " + p.Header + "\n\n[EN:" + strings.TrimSpace(string(out)) + "]\n" + p.Text,
				Header: p.Header,
			}
		} else {
			result[i] = p
		}
		time.Sleep(500 * time.Millisecond)
	}
	return result
}

// ── Dolt commit/push helper ───────────────────────────────────────────────────

func doltCommitAndPush(doltDir, msg string, push bool) {
	add := exec.Command("dolt", "add", "writings")
	add.Dir = doltDir
	if out, err := add.CombinedOutput(); err != nil {
		fmt.Fprintf(os.Stderr, "dolt add failed: %v: %s\n", err, out)
		return
	}
	commit := exec.Command("dolt", "commit", "-m", msg)
	commit.Dir = doltDir
	if out, err := commit.CombinedOutput(); err != nil {
		fmt.Fprintf(os.Stderr, "dolt commit failed: %v: %s\n", err, out)
		return
	}
	fmt.Fprintf(os.Stderr, "Committed: %s\n", msg)
	if push {
		p := exec.Command("dolt", "push", "origin", "main")
		p.Dir = doltDir
		if out, err := p.CombinedOutput(); err != nil {
			fmt.Fprintf(os.Stderr, "dolt push failed: %v: %s\n", err, out)
		} else {
			fmt.Fprintf(os.Stderr, "Pushed to origin/main\n%s", out)
		}
	}
}

// ── Apply matches to DB ───────────────────────────────────────────────────────

func applyMatches(results []MatchResult, lang, doltDir string) int {
	if len(results) == 0 {
		return 0
	}
	applied := 0
	for _, r := range results {
		sql := fmt.Sprintf("SET FOREIGN_KEY_CHECKS=0; UPDATE writings SET phelps='%s' WHERE source_id='%s' AND language='%s' AND source='%s'; SET FOREIGN_KEY_CHECKS=1;",
			sqlEsc(r.Phelps), sqlEsc(r.SourceID), sqlEsc(lang), sqlEsc(prayerSource))
		if err := serverExec(doltDir, sql); err == nil {
			applied++
		}
	}
	return applied
}

// ── Strategy auto-detection ───────────────────────────────────────────────────

func detectStrategy(doltDir, lang string, prayers []Prayer) string {
	// Check if rosetta vocab exists
	vocab := doltQuery(doltDir, fmt.Sprintf(
		`SELECT COUNT(*) as cnt FROM rosetta_vocab WHERE language='%s' AND term_type='category'`, lang))
	if len(vocab) > 0 && vocab[0]["cnt"] != "0" {
		return "rosetta"
	}
	// Check if prayers have ## headers
	headerCount := 0
	for _, p := range prayers {
		if p.Header != "" {
			headerCount++
		}
	}
	if headerCount > len(prayers)/2 {
		return "rosetta" // Will need --build-vocab first
	}
	return "standard"
}

// ── Helpers ───────────────────────────────────────────────────────────────────

func stripHeader(text string) string {
	text = strings.TrimSpace(text)
	if strings.HasPrefix(text, "## ") {
		if idx := strings.Index(text, "\n"); idx >= 0 {
			text = strings.TrimSpace(text[idx:])
		}
	}
	return text
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

// ── Hant→Hans cross-script matching ──────────────────────────────────────────
// Converts zh-Hant prayer texts to Simplified Chinese via opencc, then matches
// against already-resolved zh-Hans prayers by character Jaccard similarity.

// convertToSimplified batch-converts Traditional Chinese texts to Simplified
// using opencc via Python subprocess.
func convertToSimplified(texts []string) []string {
	input := strings.Join(texts, "\n")
	cmd := exec.Command("python3", "-c",
		`import sys, opencc; cc=opencc.OpenCC('t2s');`+
			`[print(cc.convert(l)) for l in sys.stdin.read().split('\n')]`)
	cmd.Stdin = strings.NewReader(input)
	out, err := cmd.Output()
	if err != nil {
		return texts // fallback: return originals unchanged
	}
	result := strings.Split(strings.TrimRight(string(out), "\n"), "\n")
	// Pad/trim to same length as input
	for len(result) < len(texts) {
		result = append(result, texts[len(result)])
	}
	return result[:len(texts)]
}

// jaccardRunes computes Jaccard similarity on unique rune sets.
func jaccardRunes(a, b string) float64 {
	setA := make(map[rune]bool)
	for _, r := range a {
		setA[r] = true
	}
	setB := make(map[rune]bool)
	for _, r := range b {
		setB[r] = true
	}
	inter := 0
	for r := range setA {
		if setB[r] {
			inter++
		}
	}
	union := len(setA) + len(setB) - inter
	if union == 0 {
		return 0
	}
	return float64(inter) / float64(union)
}

// hantToHansPrayers converts prayer texts from Traditional to Simplified Chinese
// using opencc, returning new Prayer slice with converted Text fields.
// Gemini identifies Simplified Chinese much more reliably than Traditional.
func hantToHansPrayers(prayers []Prayer) []Prayer {
	var texts []string
	for _, p := range prayers {
		texts = append(texts, p.Text)
	}
	fmt.Fprintf(os.Stderr, "[hant2hans] Converting %d prayers Traditional→Simplified...\n", len(prayers))
	converted := convertToSimplified(texts)
	result := make([]Prayer, len(prayers))
	for i, p := range prayers {
		result[i] = p
		if i < len(converted) {
			result[i].Text = converted[i]
		}
	}
	return result
}

func isCJK(r rune) bool {
	return (r >= 0x4E00 && r <= 0x9FFF) || (r >= 0x3040 && r <= 0x30FF) ||
		(r >= 0xAC00 && r <= 0xD7AF) || (r >= 0x3400 && r <= 0x4DBF)
}

func hasCJK(s string) bool {
	for _, r := range s {
		if isCJK(r) {
			return true
		}
	}
	return false
}

func isRTL(lang string) bool {
	rtl := map[string]bool{"ar": true, "fa": true, "ur": true, "he": true, "ug": true}
	return rtl[lang]
}

// ── bahaiprayers.app English fetch ───────────────────────────────────────────

const appBaseURL = "https://www.bahaiprayers.app"
const appCacheDir = "/tmp/bpapp_cache"

var appHTTPClient = &http.Client{Timeout: 15 * time.Second}

var reAppPrayerDiv = regexp.MustCompile(`(?is)<div[^>]*id="prayer"[^>]*>(.*?)</div>`)
var reAppPAuthor = regexp.MustCompile(`(?is)<p[^>]*class="author"[^>]*>(.*?)</p>`)
var reAppParas = regexp.MustCompile(`(?is)<p(?:\s[^>]*)?>(.+?)</p>`)
var reAppBlockTag = regexp.MustCompile(`(?i)<(/?(p|br|h[1-6]|li)[^>]*)>`)
var reAppHTMLTag = regexp.MustCompile(`<[^>]+>`)
var reAppMultiNL = regexp.MustCompile(`\n{3,}`)

func stripAppHTML(h string) string {
	h = reAppBlockTag.ReplaceAllStringFunc(h, func(m string) string { return "\n" + m })
	h = reAppHTMLTag.ReplaceAllString(h, "")
	h = strings.NewReplacer(
		"&amp;", "&", "&lt;", "<", "&gt;", ">",
		"&quot;", `"`, "&#39;", "'", "&nbsp;", " ",
		"&#x27;", "'", "&#x2019;", "\u2019",
	).Replace(h)
	return strings.TrimSpace(reAppMultiNL.ReplaceAllString(h, "\n\n"))
}

// fetchAppEnglish fetches the English version of a bahaiprayers.app prayer by its
// source_id, using the shared disk cache. Returns the prayer body text, or "".
func fetchAppEnglish(sourceID string) string {
	url := fmt.Sprintf("%s/prayer?id=%s&to=en", appBaseURL, sourceID)
	hash := fmt.Sprintf("%x", md5.Sum([]byte(url)))
	cachePath := filepath.Join(appCacheDir, hash+".html")

	var htmlContent string
	if data, err := os.ReadFile(cachePath); err == nil {
		htmlContent = string(data)
	} else {
		resp, err := appHTTPClient.Get(url)
		if err != nil || resp.StatusCode != 200 {
			return ""
		}
		body, err := io.ReadAll(resp.Body)
		resp.Body.Close()
		if err != nil {
			return ""
		}
		htmlContent = string(body)
		_ = os.MkdirAll(appCacheDir, 0755)
		_ = os.WriteFile(cachePath, body, 0644)
	}

	content := htmlContent
	if m := reAppPrayerDiv.FindStringSubmatch(htmlContent); m != nil {
		content = m[1]
	}
	bodyHTML := reAppPAuthor.ReplaceAllString(content, "")
	paraMatches := reAppParas.FindAllStringSubmatch(bodyHTML, -1)
	var paras []string
	for _, pm := range paraMatches {
		text := stripAppHTML(pm[1])
		if strings.TrimSpace(text) != "" {
			paras = append(paras, text)
		}
	}
	return strings.Join(paras, "\n\n")
}

// enrichWithAppEnglish fetches the English version for each bahaiprayers.app prayer
// and stores it in Prayer.EnglishHint. Prayers whose source_id is not numeric are skipped.
// Returns count of prayers enriched.
func enrichWithAppEnglish(prayers []Prayer) int {
	if prayerSource != "bahaiprayers.app" {
		return 0
	}
	enriched := 0
	for i := range prayers {
		if prayers[i].EnglishHint != "" {
			continue
		}
		en := fetchAppEnglish(prayers[i].SourceID)
		if en != "" {
			prayers[i].EnglishHint = en
			enriched++
		}
	}
	return enriched
}

// appEnglishMatchPrayers matches prayers using their fetched English text against the
// English bahaiprayers.net entries. Returns high-confidence matches (jaccard ≥ 0.55)
// and the remaining unresolved prayers.
func appEnglishMatchPrayers(doltDir string, prayers []Prayer) ([]MatchResult, []Prayer) {
	if prayerSource != "bahaiprayers.app" {
		return nil, prayers
	}

	// Enrich prayers with English text
	enriched := enrichWithAppEnglish(prayers)
	if enriched == 0 {
		return nil, prayers
	}

	// Load English net entries for fingerprint matching
	enRows := doltQuery(doltDir,
		`SELECT phelps, LEFT(text, 600) as text FROM writings `+
			`WHERE language='en' AND source='bahaiprayers.net' AND phelps IS NOT NULL AND phelps <> ''`)
	if len(enRows) == 0 {
		return nil, prayers
	}

	normText := func(s string) string {
		var sb strings.Builder
		for _, line := range strings.Split(s, "\n") {
			t := strings.TrimSpace(line)
			if strings.HasPrefix(t, "#") || strings.HasPrefix(t, "*") {
				continue
			}
			if sb.Len() > 0 {
				sb.WriteByte(' ')
			}
			sb.WriteString(t)
		}
		return strings.ToLower(strings.TrimSpace(sb.String()))
	}
	wordToks := func(s string) map[string]bool {
		toks := make(map[string]bool)
		for _, w := range strings.Fields(s) {
			w = strings.TrimFunc(w, func(r rune) bool { return !unicode.IsLetter(r) && !unicode.IsDigit(r) })
			if len(w) > 2 {
				toks[w] = true
			}
		}
		return toks
	}
	jaccardBool := func(a, b map[string]bool) float64 {
		inter := 0
		for k := range a {
			if b[k] {
				inter++
			}
		}
		union := len(a) + len(b) - inter
		if union == 0 {
			return 0
		}
		return float64(inter) / float64(union)
	}

	type enEntry struct {
		phelps string
		fp     string
		tokens map[string]bool
	}
	enIndex := make([]enEntry, 0, len(enRows))
	for _, r := range enRows {
		norm := normText(r["text"])
		fp := norm
		if len(fp) > 120 {
			fp = fp[:120]
		}
		enIndex = append(enIndex, enEntry{r["phelps"], fp, wordToks(norm)})
	}

	var matched []MatchResult
	var unresolved []Prayer
	for _, p := range prayers {
		if p.EnglishHint == "" {
			unresolved = append(unresolved, p)
			continue
		}
		norm := normText(p.EnglishHint)
		fp := norm
		if len(fp) > 120 {
			fp = fp[:120]
		}
		toks := wordToks(norm)

		best, bestScore := "", 0.0
		for _, e := range enIndex {
			if e.fp == fp {
				best = e.phelps
				bestScore = 1.0
				break
			}
			if sc := jaccardBool(toks, e.tokens); sc > bestScore {
				bestScore = sc
				best = e.phelps
			}
		}

		hint := p.EnglishHint
		if len(hint) > 80 {
			hint = hint[:80]
		}
		if bestScore >= 0.55 && best != "" {
			fmt.Fprintf(os.Stderr, "  [app-en] %s/%s → %s (%.2f): %s\n",
				p.Language, p.SourceID, best, bestScore, hint)
			matched = append(matched, MatchResult{
				SourceID: p.SourceID,
				Phelps:   best,
				EnFirst:  "[app-en] " + hint,
			})
		} else {
			fmt.Fprintf(os.Stderr, "  [app-en] %s/%s no match (%.2f): %s\n",
				p.Language, p.SourceID, bestScore, hint)
			unresolved = append(unresolved, p)
		}
	}
	if len(matched) > 0 {
		fmt.Fprintf(os.Stderr, "[app-en] %d/%d matched via English app version\n",
			len(matched), len(matched)+len(unresolved))
	}
	return matched, unresolved
}

// crossSourceMatch checks if any OTHER source in the DB has the same prayer
// already matched (same language, non-null phelps). Uses normalized text
// fingerprinting (exact prefix + word Jaccard ≥ 0.55). Returns matched results
// and the remaining unresolved prayers.
func crossSourceMatch(doltDir, lang string, prayers []Prayer) ([]MatchResult, []Prayer) {
	// Load all phelps-coded prayers for this language from sources other than prayerSource
	rows := doltQuery(doltDir, fmt.Sprintf(
		`SELECT phelps, LEFT(text, 600) as text FROM writings `+
			`WHERE language='%s' AND source<>'%s' AND phelps IS NOT NULL AND phelps<>''`,
		sqlEsc(lang), sqlEsc(prayerSource)))
	if len(rows) == 0 {
		return nil, prayers
	}

	type candidate struct {
		phelps string
		fp     string
		tokens map[string]int
	}
	normalizeText := func(s string) string {
		// Strip ## headers, collapse whitespace, lower
		var sb strings.Builder
		for _, line := range strings.Split(s, "\n") {
			t := strings.TrimSpace(line)
			if strings.HasPrefix(t, "##") {
				continue
			}
			if sb.Len() > 0 {
				sb.WriteByte(' ')
			}
			sb.WriteString(t)
		}
		return strings.ToLower(sb.String())
	}
	wordTokens := func(s string) map[string]int {
		toks := make(map[string]int)
		for _, w := range strings.Fields(s) {
			// Strip punctuation
			w = strings.TrimFunc(w, func(r rune) bool { return !unicode.IsLetter(r) && !unicode.IsDigit(r) })
			if len(w) > 1 {
				toks[w]++
			}
		}
		return toks
	}
	jaccardWords := func(a, b map[string]int) float64 {
		inter, union := 0, 0
		for w, ca := range a {
			if cb := b[w]; cb > 0 {
				if ca < cb {
					inter += ca
				} else {
					inter += cb
				}
			}
			union += ca
		}
		for w, cb := range b {
			if a[w] == 0 {
				union += cb
			}
		}
		if union == 0 {
			return 0
		}
		return float64(inter) / float64(union)
	}

	candidates := make([]candidate, 0, len(rows))
	for _, r := range rows {
		norm := normalizeText(r["text"])
		fp := norm
		if len(fp) > 120 {
			fp = fp[:120]
		}
		candidates = append(candidates, candidate{
			phelps: r["phelps"],
			fp:     fp,
			tokens: wordTokens(norm),
		})
	}

	var matched []MatchResult
	var unresolved []Prayer
	for _, p := range prayers {
		norm := normalizeText(p.Text)
		fp := norm
		if len(fp) > 120 {
			fp = fp[:120]
		}
		toks := wordTokens(norm)

		best := ""
		bestScore := 0.0
		for _, c := range candidates {
			if c.fp == fp {
				best = c.phelps
				bestScore = 1.0
				break
			}
			score := jaccardWords(toks, c.tokens)
			if score > bestScore {
				bestScore = score
				best = c.phelps
			}
		}
		if bestScore >= 0.55 && best != "" {
			matched = append(matched, MatchResult{
				SourceID: p.SourceID,
				Phelps:   best,
				EnFirst:  fmt.Sprintf("[cross-source %.2f]", bestScore),
			})
		} else {
			unresolved = append(unresolved, p)
		}
	}
	if len(matched) > 0 {
		fmt.Fprintf(os.Stderr, "[cross-source] %d/%d matched from other DB sources\n", len(matched), len(matched)+len(unresolved))
	}
	return matched, unresolved
}

// isNonLatinScript returns true for languages where --translate helps significantly
func isNonLatinScript(lang string) bool {
	nonLatin := map[string]bool{
		"ar": true, "fa": true, "ur": true, "he": true, "ug": true,
		"hy": true, "ka": true, "ml": true, "kn": true, "ta": true,
		"te": true, "bn": true, "gu": true, "pa": true, "si": true,
		"th": true, "lo": true, "my": true, "km": true,
		"ko": true, "ja": true, "zh": true, "zh-Hans": true, "zh-Hant": true,
		"am": true, "ti": true,
	}
	return nonLatin[lang]
}

// Suppress unused warning
var _ = unicode.IsSpace
var _ = io.EOF
var _ = bufio.NewReader
var _ = hasCJK
var _ = isRTL

// ── Proposal pipeline ────────────────────────────────────────────────────────
//
// --propose writes structured JSON proposals instead of applying matches.
// Each proposal includes evidence from multiple signals so a reviewer can
// accept/reject without re-running the matcher.

type Proposal struct {
	Version      string            `json:"version,omitempty"` // UUID from writings table (if available)
	Language     string            `json:"language"`
	SourceID     string            `json:"source_id"`
	CurrentCode  string            `json:"current_phelps"`
	ProposedCode string            `json:"proposed_phelps"`
	Confidence   float64           `json:"confidence"`
	Evidence     ProposalEvidence  `json:"evidence"`
	Reason       string            `json:"reason"`
	Strategy     string            `json:"strategy"` // position, crosslang, structural
}

type ProposalEvidence struct {
	CategoryMatch     string   `json:"category_match,omitempty"`
	AuthorMatch       string   `json:"author_match,omitempty"`
	LengthRatio       float64  `json:"length_ratio,omitempty"`
	PositionInCat     int      `json:"position_in_category,omitempty"`
	EnPositionInCat   int      `json:"en_position_in_category,omitempty"`
	OpeningPhrase     string   `json:"opening_phrase,omitempty"`
	EnOpeningPhrase   string   `json:"en_opening_phrase,omitempty"`
	AlreadyInLang     bool     `json:"already_in_lang"`
	CrossLangVotes    []string `json:"cross_lang_votes,omitempty"`
	ParaCount         int      `json:"para_count,omitempty"`
	EnParaCount       int      `json:"en_para_count,omitempty"`
}

func writeProposals(proposals []Proposal, outPath string) error {
	data, err := json.MarshalIndent(proposals, "", "  ")
	if err != nil {
		return err
	}
	if err := os.WriteFile(outPath, data, 0644); err != nil {
		return err
	}
	fmt.Fprintf(os.Stderr, "Wrote %d proposals to %s\n", len(proposals), outPath)
	return nil
}

// ── Position matching ────────────────────────────────────────────────────────
// Matches prayers by their position within a category. Prayer #3 in "Healing"
// in Tuvaluan should match prayer #3 in "Healing" in English PBS.
//
// Requires: prayer_book_structure entries for the target language (or
// category headers in the text that can be mapped to English categories).

type pbsEntry struct {
	CategoryName string
	PhelpsCode   string
	Order        int
}

// loadPBS loads prayer_book_structure for a given language's :bp prayerbook.
// Bare lang codes like "en", "fr" are suffixed to "en:bp", "fr:bp" since
// PBS source_language values now carry the :bp suffix.
func loadPBS(doltDir, lang string) []pbsEntry {
	rows := doltQuery(doltDir, fmt.Sprintf(
		`SELECT category_name, phelps_code, order_in_category FROM prayer_book_structure `+
			`WHERE source_language='%s:bp' ORDER BY category_name, order_in_category`, sqlEsc(lang)))
	entries := make([]pbsEntry, 0, len(rows))
	for _, r := range rows {
		ord, _ := strconv.Atoi(r["order_in_category"])
		entries = append(entries, pbsEntry{
			CategoryName: r["category_name"],
			PhelpsCode:   r["phelps_code"],
			Order:        ord,
		})
	}
	return entries
}

// loadExistingCodes returns the set of phelps codes already assigned in a language.
func loadExistingCodes(doltDir, lang string) map[string]bool {
	rows := doltQuery(doltDir, fmt.Sprintf(
		`SELECT DISTINCT phelps FROM writings WHERE language='%s' AND source='%s' `+
			`AND phelps IS NOT NULL AND phelps <> '' AND phelps NOT LIKE 'TMP%%'`,
		sqlEsc(lang), sqlEsc(prayerSource)))
	codes := make(map[string]bool, len(rows))
	for _, r := range rows {
		codes[r["phelps"]] = true
	}
	return codes
}

// loadFullText loads full text for a prayer by source_id + language.
func loadFullText(doltDir, lang, sourceID string) string {
	rows := doltQuery(doltDir, fmt.Sprintf(
		`SELECT text FROM writings WHERE language='%s' AND source_id='%s' AND source='%s' LIMIT 1`,
		sqlEsc(lang), sqlEsc(sourceID), sqlEsc(prayerSource)))
	if len(rows) > 0 {
		return rows[0]["text"]
	}
	return ""
}

// loadEnglishProps loads English prayer properties for candidate matching.
type enPrayerProps struct {
	Phelps    string
	Length    int
	Paras     int
	FirstLine string
	Author    string // AB, BH, BB, ABU prefix
}

func loadEnglishPropsMap(doltDir string) map[string]enPrayerProps {
	rows := doltQuery(doltDir,
		`SELECT phelps, CHAR_LENGTH(text) as tlen, text FROM writings `+
			`WHERE language='en' AND source='bahaiprayers.net' AND phelps IS NOT NULL AND phelps <> '' AND phelps NOT LIKE 'TMP%'`)
	props := make(map[string]enPrayerProps, len(rows))
	for _, r := range rows {
		text := r["text"]
		tlen, _ := strconv.Atoi(r["tlen"])
		paras := 0
		for _, p := range strings.Split(text, "\n\n") {
			if strings.TrimSpace(p) != "" {
				paras++
			}
		}
		firstLine := ""
		for _, line := range strings.Split(strings.TrimSpace(text), "\n") {
			line = strings.TrimSpace(line)
			if line != "" && !strings.HasPrefix(line, "#") && !strings.HasPrefix(line, "*") {
				if len(line) > 80 {
					line = line[:80]
				}
				firstLine = line
				break
			}
		}
		author := ""
		code := r["phelps"]
		for _, pfx := range []string{"ABU", "AB", "BH", "BB", "XAB", "XBH", "XBB"} {
			if strings.HasPrefix(code, pfx) {
				author = pfx
				break
			}
		}
		props[code] = enPrayerProps{
			Phelps: code, Length: tlen, Paras: paras,
			FirstLine: firstLine, Author: author,
		}
	}
	return props
}

// Category translation map from build_tmp_reference.py (local headers → English keywords)
var catTranslations = map[string]string{
	// Tuvaluan
	"MOTU KEATEA": "Morning", "AFIAFI": "Evening", "VALUAPO": "Midnight",
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
	"TALO FAKA-PITOA TOETOE": "Obligatory",
	"FAKATASITASIIGA": "Steadfastness",
	"LUKUUGA FAKA-TE-AGAAGA": "Assembly",
	"MATUA FAKATALI O FANAU": "Children",
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
	// Gilbertese
	"TE TATARO N TE INGABONG": "Morning", "TE TATARO N TE TAIRIKI": "Evening",
	"TE TATARO N TE NUKANIBONG": "Midnight",
	"TE MAIU NI KATEI": "Spiritual", "TE KATITEUANAAKI": "Unity",
	"TE REIREI": "Teaching", "TE BUOBUOKI": "Aid",
	"TE KARAOIROAKI": "Healing", "TE KATEIMATOAAKI": "Steadfastness",
	"TE KABWARABURE": "Forgiveness", "TE KAMANOAKI": "Protection",
	"TE ATAATAINGKAMI": "Children", "TE ROROBUAKA": "Youth",
	"AINE": "Women", "TE MARE": "Marriage",
	"TE AKI-MAMATAM": "Fasting", "TE KARINEAKI": "Praise",
	// Tamil
	"காலை": "Morning", "மாலை": "Evening", "நள்ளிரவு": "Midnight",
	"ஆன்மீக வளர்ச்சி": "Spiritual", "ஒற்றுமை": "Unity",
	"போதனை": "Teaching", "உதவி": "Aid",
	"குணமாக்கல்": "Healing", "உறுதி": "Steadfastness",
	"மன்னிப்பு": "Forgiveness", "பாதுகாப்பு": "Protection",
	"குழந்தைகள்": "Children", "இளைஞர்கள்": "Youth",
	"பெண்கள்": "Women", "திருமணம்": "Marriage",
	"நோன்பு": "Fasting", "புகழ்": "Praise",
	// Generic Spanish/German/other
	"Niños": "Children", "Protección": "Protection",
	"Reuniones": "Gatherings", "Jóvenes": "Youth",
	"Mujeres": "Women", "Ayuno": "Fasting",
	"Enseñanza": "Teaching", "Constancia": "Steadfastness",
	"Perdón": "Forgiveness", "Iluminación": "Enlightenment",
	"Asamblea Espiritual": "Assembly",
	"América": "America", "Tabla de Aḥmad": "Tablet of Ahmad",
	"Schutz": "Protection", "Heilung": "Healing",
	"Standhaftigkeit": "Steadfastness",
	"Für die Verstorbenen": "Departed",
	"Cercanía a Dios": "Nearness", "Difuntos": "Departed",
	"Lob und Dank": "Praise", "Beistand": "Aid",
}

// resolveEnCategory maps a local category header to an English category name,
// using catTranslations first, then rosetta vocab, then partial match.
func resolveEnCategory(header, lang, doltDir string) string {
	if en, ok := catTranslations[header]; ok {
		return en
	}
	// Try rosetta vocab
	rows := doltQuery(doltDir, fmt.Sprintf(
		`SELECT en_meaning FROM rosetta_vocab WHERE language='%s' AND local_term='%s' AND term_type='category' LIMIT 1`,
		sqlEsc(lang), sqlEsc(header)))
	if len(rows) > 0 && rows[0]["en_meaning"] != "" {
		return rows[0]["en_meaning"]
	}
	// Partial match in catTranslations
	for k, v := range catTranslations {
		if strings.Contains(strings.ToLower(header), strings.ToLower(k)) ||
			strings.Contains(strings.ToLower(k), strings.ToLower(header)) {
			return v
		}
	}
	return ""
}

// enCategoryToPBSCats maps an English keyword (e.g. "Healing") to matching PBS
// category_name values (e.g. "Healing", "Health and Healing").
func enCategoryToPBSCats(doltDir, keyword string) []string {
	rows := doltQuery(doltDir, fmt.Sprintf(
		`SELECT DISTINCT category_name FROM prayer_book_structure `+
			`WHERE source_language='en:bp' AND category_name LIKE '%%%s%%'`, sqlEsc(keyword)))
	cats := make([]string, 0, len(rows))
	for _, r := range rows {
		cats = append(cats, r["category_name"])
	}
	return cats
}

// runPositionMatch matches prayers by position within their category.
// For each unresolved prayer with a category header, it finds the English PBS
// entries for that category, ordered by position. Then it checks if position N
// in the target language maps to the same position in English PBS.
func runPositionMatch(doltDir, lang string, prayers []Prayer,
	enProps map[string]enPrayerProps, inv map[string]InvEntry, recheckAll bool) []Proposal {

	existingCodes := loadExistingCodes(doltDir, lang)
	langPBS := loadPBS(doltDir, lang)
	enPBS := loadPBS(doltDir, "en")

	// Build per-category ordered code lists from PBS
	type catCodes struct {
		codes []string
	}
	langCatCodes := map[string][]string{} // category_name → ordered codes
	for _, e := range langPBS {
		langCatCodes[e.CategoryName] = append(langCatCodes[e.CategoryName], e.PhelpsCode)
	}
	enCatCodes := map[string][]string{}
	for _, e := range enPBS {
		enCatCodes[e.CategoryName] = append(enCatCodes[e.CategoryName], e.PhelpsCode)
	}

	// Group prayers by header, preserving order within each header
	type prayerInCat struct {
		prayer Prayer
		posInCat int // 0-based position among prayers with this header
	}
	headerGroups := map[string][]prayerInCat{}
	headerOrder := []string{}
	headerSeen := map[string]bool{}

	// Also load ALL prayers for this lang+header (not just unresolved) to get true position
	allPrayersByHeader := map[string][]string{} // header → ordered source_ids
	allRows := doltQuery(doltDir, fmt.Sprintf(
		`SELECT source_id, LEFT(text,200) as text FROM writings `+
			`WHERE language='%s' AND source='%s' ORDER BY CAST(source_id AS UNSIGNED)`,
		sqlEsc(lang), sqlEsc(prayerSource)))
	for _, r := range allRows {
		text := r["text"]
		header := ""
		if strings.HasPrefix(text, "## ") {
			lines := strings.SplitN(text, "\n", 2)
			header = strings.TrimPrefix(lines[0], "## ")
		}
		if header != "" {
			allPrayersByHeader[header] = append(allPrayersByHeader[header], r["source_id"])
		}
	}

	// Map each prayer to its position in its header group.
	// First try text headers (## prefix), then fall back to PBS categories.
	// Build PBS-based grouping for prayers without text headers.
	pbsByCat := map[string][]string{} // PBS category_name → ordered source_ids
	if len(langPBS) > 0 {
		// Build phelps→source_id map for this language
		phelpsToSIDs := map[string][]string{}
		for _, r := range allRows {
			sid := r["source_id"]
			// Look up phelps for this source_id
			pRows := doltQuery(doltDir, fmt.Sprintf(
				`SELECT phelps FROM writings WHERE source_id='%s' AND language='%s' AND source='%s' LIMIT 1`,
				sqlEsc(sid), sqlEsc(lang), sqlEsc(prayerSource)))
			if len(pRows) > 0 && pRows[0]["phelps"] != "" {
				ph := pRows[0]["phelps"]
				phelpsToSIDs[ph] = append(phelpsToSIDs[ph], sid)
			}
		}
		// Group by PBS category, preserving PBS order
		for _, e := range langPBS {
			cat := e.CategoryName
			sids := phelpsToSIDs[e.PhelpsCode]
			for _, sid := range sids {
				pbsByCat[cat] = append(pbsByCat[cat], sid)
			}
		}
	}

	for _, p := range prayers {
		// Try text header first
		if p.Header != "" {
			allInHeader := allPrayersByHeader[p.Header]
			pos := -1
			for i, sid := range allInHeader {
				if sid == p.SourceID {
					pos = i
					break
				}
			}
			if pos >= 0 {
				if !headerSeen[p.Header] {
					headerOrder = append(headerOrder, p.Header)
					headerSeen[p.Header] = true
				}
				headerGroups[p.Header] = append(headerGroups[p.Header], prayerInCat{prayer: p, posInCat: pos})
				continue
			}
		}

		// Fall back to PBS category
		for cat, sids := range pbsByCat {
			for i, sid := range sids {
				if sid == p.SourceID {
					// Use PBS category as a synthetic header (prefixed to avoid collision)
					syntheticHeader := "PBS:" + cat
					if !headerSeen[syntheticHeader] {
						headerOrder = append(headerOrder, syntheticHeader)
						headerSeen[syntheticHeader] = true
					}
					headerGroups[syntheticHeader] = append(headerGroups[syntheticHeader],
						prayerInCat{prayer: p, posInCat: i})
					goto nextPrayer
				}
			}
		}
	nextPrayer:
	}

	var proposals []Proposal

	for _, header := range headerOrder {
		var enKeyword string
		var enCats []string

		if strings.HasPrefix(header, "PBS:") {
			// Synthetic header from PBS — the category name is already English
			pbsCat := header[4:]
			enKeyword = pbsCat
			enCats = []string{pbsCat}
		} else {
			enKeyword = resolveEnCategory(header, lang, doltDir)
			if enKeyword == "" {
				fmt.Fprintf(os.Stderr, "  [position] skip %q — no English category mapping\n", header)
				continue
			}
			enCats = enCategoryToPBSCats(doltDir, enKeyword)
		}
		if len(enCats) == 0 {
			fmt.Fprintf(os.Stderr, "  [position] skip %q → %q — no PBS category\n", header, enKeyword)
			continue
		}

		// Collect all English codes in order for the matching categories
		var enCodesOrdered []string
		for _, cat := range enCats {
			enCodesOrdered = append(enCodesOrdered, enCatCodes[cat]...)
		}
		if len(enCodesOrdered) == 0 {
			continue
		}

		group := headerGroups[header]
		totalInHeader := len(allPrayersByHeader[header])
		fmt.Fprintf(os.Stderr, "  [position] %q → %q: %d unresolved / %d total, %d en candidates\n",
			header, enKeyword, len(group), totalInHeader, len(enCodesOrdered))

		for _, pic := range group {
			if pic.posInCat >= len(enCodesOrdered) {
				continue
			}
			candidateCode := enCodesOrdered[pic.posInCat]

			// "Already in language" guard (skip in recheck-all mode)
			if !recheckAll && existingCodes[candidateCode] {
				continue
			}
			// False positive guard
			if falsePositiveCodes[candidateCode] {
				continue
			}

			// Get prayer text properties
			fullText := loadFullText(doltDir, lang, pic.prayer.SourceID)
			prayerLen := len(fullText)
			prayerParas := 0
			for _, para := range strings.Split(fullText, "\n\n") {
				if strings.TrimSpace(para) != "" {
					prayerParas++
				}
			}
			firstLine := ""
			for _, line := range strings.Split(strings.TrimSpace(fullText), "\n") {
				line = strings.TrimSpace(line)
				if line != "" && !strings.HasPrefix(line, "#") && !strings.HasPrefix(line, "*") {
					if len(line) > 80 {
						line = line[:80]
					}
					firstLine = line
					break
				}
			}

			// Get English properties
			enP, enOK := enProps[candidateCode]
			lengthRatio := 0.0
			if enOK && enP.Length > 0 {
				lengthRatio = float64(prayerLen) / float64(enP.Length)
			}

			// Author prefix check
			authorMatch := ""
			currentCode := pic.prayer.SourceID // We'll get the actual current phelps
			currentPhelps := ""
			cpRows := doltQuery(doltDir, fmt.Sprintf(
				`SELECT phelps FROM writings WHERE source_id='%s' AND language='%s' AND source='%s' LIMIT 1`,
				sqlEsc(pic.prayer.SourceID), sqlEsc(lang), sqlEsc(prayerSource)))
			if len(cpRows) > 0 {
				currentPhelps = cpRows[0]["phelps"]
			}
			_ = currentCode

			if enOK {
				authorMatch = enP.Author
			}
			// Check if current TMP had an original code with author prefix
			if strings.HasPrefix(currentPhelps, "TMP") {
				// Try to get original code from diff history
				origRows := doltQuery(doltDir, fmt.Sprintf(
					`SELECT from_phelps FROM dolt_diff_writings `+
						`WHERE to_phelps='%s' AND to_language='%s' AND to_source='%s' LIMIT 1`,
					sqlEsc(currentPhelps), sqlEsc(lang), sqlEsc(prayerSource)))
				if len(origRows) > 0 {
					orig := origRows[0]["from_phelps"]
					for _, pfx := range []string{"ABU", "AB", "BH", "BB"} {
						if strings.HasPrefix(orig, pfx) && authorMatch != "" && !strings.HasPrefix(authorMatch, pfx[:2]) {
							authorMatch = "" // Author mismatch with original
						}
					}
				}
			}

			// Compute confidence
			confidence := 0.5 // base: position match
			if lengthRatio > 0.3 && lengthRatio < 3.0 {
				confidence += 0.15 // reasonable length
			}
			if lengthRatio > 0.5 && lengthRatio < 2.0 {
				confidence += 0.10 // good length
			}
			if enOK && prayerParas == enP.Paras {
				confidence += 0.10 // paragraph count matches
			}
			if totalInHeader == len(enCodesOrdered) {
				confidence += 0.10 // same number of prayers in category
			}
			if confidence > 0.99 {
				confidence = 0.99
			}

			reason := fmt.Sprintf("Position #%d in %q, en=%q",
				pic.posInCat+1, header, enKeyword)
			if lengthRatio > 0 {
				reason += fmt.Sprintf(", ratio=%.2f", lengthRatio)
			}
			if enOK && prayerParas == enP.Paras {
				reason += fmt.Sprintf(", paras=%d match", prayerParas)
			}

			proposals = append(proposals, Proposal{
				Language:     lang,
				SourceID:     pic.prayer.SourceID,
				CurrentCode:  currentPhelps,
				ProposedCode: candidateCode,
				Confidence:   confidence,
				Strategy:     "position",
				Reason:       reason,
				Evidence: ProposalEvidence{
					CategoryMatch:   enKeyword,
					AuthorMatch:     authorMatch,
					LengthRatio:     lengthRatio,
					PositionInCat:   pic.posInCat + 1,
					EnPositionInCat: pic.posInCat + 1,
					OpeningPhrase:   firstLine,
					EnOpeningPhrase: enP.FirstLine,
					AlreadyInLang:   false,
					ParaCount:       prayerParas,
					EnParaCount:     enP.Paras,
				},
			})

			enFirstShort := enP.FirstLine
			if len(enFirstShort) > 50 {
				enFirstShort = enFirstShort[:50]
			}
			fmt.Fprintf(os.Stderr, "    → %s (pos %d, ratio=%.2f, conf=%.2f) %s\n",
				candidateCode, pic.posInCat+1, lengthRatio, confidence, enFirstShort)
		}
	}

	return proposals
}

// ── Cross-language voting ────────────────────────────────────────────────────
// For each unresolved prayer, check what code other languages assigned to
// prayers at similar source_ids or with similar structural properties.
// If 3+ languages agree on a code, that's strong evidence.

func runCrossLangVote(doltDir, lang string, prayers []Prayer,
	enProps map[string]enPrayerProps, inv map[string]InvEntry, recheckAll bool) []Proposal {

	existingCodes := loadExistingCodes(doltDir, lang)
	var proposals []Proposal

	for _, p := range prayers {
		// Find what other languages have for similar source_ids on the same source
		rows := doltQuery(doltDir, fmt.Sprintf(
			`SELECT language, phelps FROM writings `+
				`WHERE source_id='%s' AND source='%s' AND language<>'%s' `+
				`AND phelps IS NOT NULL AND phelps<>'' AND phelps NOT LIKE 'TMP%%'`,
			sqlEsc(p.SourceID), sqlEsc(prayerSource), sqlEsc(lang)))

		if len(rows) == 0 {
			continue
		}

		// Count votes per code
		votes := map[string][]string{} // code → list of languages
		for _, r := range rows {
			code := r["phelps"]
			votes[code] = append(votes[code], r["language"])
		}

		// Find the top-voted code
		bestCode := ""
		bestVoters := []string{}
		for code, voters := range votes {
			if len(voters) > len(bestVoters) {
				bestCode = code
				bestVoters = voters
			}
		}

		if bestCode == "" || len(bestVoters) < 2 {
			continue
		}
		if !recheckAll && existingCodes[bestCode] {
			continue
		}
		if falsePositiveCodes[bestCode] {
			continue
		}

		// Get current phelps
		currentPhelps := ""
		cpRows := doltQuery(doltDir, fmt.Sprintf(
			`SELECT phelps FROM writings WHERE source_id='%s' AND language='%s' AND source='%s' LIMIT 1`,
			sqlEsc(p.SourceID), sqlEsc(lang), sqlEsc(prayerSource)))
		if len(cpRows) > 0 {
			currentPhelps = cpRows[0]["phelps"]
		}

		// Get length ratio
		fullText := loadFullText(doltDir, lang, p.SourceID)
		lengthRatio := 0.0
		enP, enOK := enProps[bestCode]
		if enOK && enP.Length > 0 {
			lengthRatio = float64(len(fullText)) / float64(enP.Length)
		}

		// Compute confidence based on vote count
		confidence := 0.3 + float64(len(bestVoters))*0.15
		if confidence > 0.95 {
			confidence = 0.95
		}
		// Boost if length ratio is reasonable
		if lengthRatio > 0.3 && lengthRatio < 3.0 {
			confidence += 0.05
		}
		if confidence > 0.99 {
			confidence = 0.99
		}

		voteStrs := make([]string, len(bestVoters))
		for i, v := range bestVoters {
			voteStrs[i] = v + ":" + bestCode
		}

		reason := fmt.Sprintf("%d languages agree on %s: %s",
			len(bestVoters), bestCode, strings.Join(bestVoters, ","))

		enFirst := ""
		if enOK {
			enFirst = enP.FirstLine
		}

		proposals = append(proposals, Proposal{
			Language:     lang,
			SourceID:     p.SourceID,
			CurrentCode:  currentPhelps,
			ProposedCode: bestCode,
			Confidence:   confidence,
			Strategy:     "crosslang",
			Reason:       reason,
			Evidence: ProposalEvidence{
				CrossLangVotes: voteStrs,
				LengthRatio:    lengthRatio,
				AlreadyInLang:  false,
				EnOpeningPhrase: enFirst,
			},
		})

		enFirstShort := enFirst
		if len(enFirstShort) > 50 {
			enFirstShort = enFirstShort[:50]
		}
		fmt.Fprintf(os.Stderr, "  [crosslang] %s/%s → %s (%d votes, conf=%.2f) %s\n",
			lang, p.SourceID, bestCode, len(bestVoters), confidence, enFirstShort)
	}

	return proposals
}

// ── Structural fingerprint matching ──────────────────────────────────────────
// Combines: category + author prefix + length bucket + paragraph count
// to find matching English prayers. Lower confidence than position matching,
// but works for languages without PBS.

func runStructuralMatch(doltDir, lang string, prayers []Prayer,
	enProps map[string]enPrayerProps, inv map[string]InvEntry, recheckAll bool) []Proposal {

	existingCodes := loadExistingCodes(doltDir, lang)
	enPBS := loadPBS(doltDir, "en")

	// Build category → codes map from English PBS
	enCatCodes := map[string][]string{}
	for _, e := range enPBS {
		enCatCodes[e.CategoryName] = append(enCatCodes[e.CategoryName], e.PhelpsCode)
	}

	var proposals []Proposal

	for _, p := range prayers {
		if p.Header == "" {
			continue
		}
		enKeyword := resolveEnCategory(p.Header, lang, doltDir)
		if enKeyword == "" {
			continue
		}

		// Get candidate codes from English PBS categories matching keyword
		enCats := enCategoryToPBSCats(doltDir, enKeyword)
		var candidateCodes []string
		for _, cat := range enCats {
			candidateCodes = append(candidateCodes, enCatCodes[cat]...)
		}
		if len(candidateCodes) == 0 {
			continue
		}

		// Get prayer properties
		fullText := loadFullText(doltDir, lang, p.SourceID)
		prayerLen := len(fullText)
		prayerParas := 0
		for _, para := range strings.Split(fullText, "\n\n") {
			if strings.TrimSpace(para) != "" {
				prayerParas++
			}
		}
		firstLine := ""
		for _, line := range strings.Split(strings.TrimSpace(fullText), "\n") {
			line = strings.TrimSpace(line)
			if line != "" && !strings.HasPrefix(line, "#") && !strings.HasPrefix(line, "*") {
				if len(line) > 80 {
					line = line[:80]
				}
				firstLine = line
				break
			}
		}

		// Get current phelps for author prefix check
		currentPhelps := ""
		cpRows := doltQuery(doltDir, fmt.Sprintf(
			`SELECT phelps FROM writings WHERE source_id='%s' AND language='%s' AND source='%s' LIMIT 1`,
			sqlEsc(p.SourceID), sqlEsc(lang), sqlEsc(prayerSource)))
		if len(cpRows) > 0 {
			currentPhelps = cpRows[0]["phelps"]
		}

		// Score each candidate
		type scored struct {
			code       string
			score      float64
			ratio      float64
			enP        enPrayerProps
		}
		var candidates []scored
		for _, code := range candidateCodes {
			if (!recheckAll && existingCodes[code]) || falsePositiveCodes[code] {
				continue
			}
			enP, ok := enProps[code]
			if !ok {
				continue
			}
			ratio := 0.0
			if enP.Length > 0 {
				ratio = float64(prayerLen) / float64(enP.Length)
			}
			// Skip wildly different lengths
			if ratio < 0.2 || ratio > 5.0 {
				continue
			}

			score := 0.0
			// Length ratio closeness (0 to 0.3)
			if ratio > 0.5 && ratio < 2.0 {
				score += 0.3 * (1.0 - abs64(ratio-1.0))
			}
			// Paragraph count match (0 to 0.2)
			if prayerParas == enP.Paras {
				score += 0.2
			} else if abs(prayerParas-enP.Paras) <= 1 {
				score += 0.1
			}
			candidates = append(candidates, scored{code: code, score: score, ratio: ratio, enP: enP})
		}

		if len(candidates) == 0 {
			continue
		}

		// Sort by score descending
		for i := 0; i < len(candidates)-1; i++ {
			for j := i + 1; j < len(candidates); j++ {
				if candidates[j].score > candidates[i].score {
					candidates[i], candidates[j] = candidates[j], candidates[i]
				}
			}
		}

		best := candidates[0]
		// Only propose if there's a clear winner (score gap to #2)
		if len(candidates) > 1 && best.score-candidates[1].score < 0.05 {
			continue // Too ambiguous
		}

		confidence := 0.3 + best.score
		if confidence > 0.75 {
			confidence = 0.75 // Cap structural — it's the weakest signal
		}

		reason := fmt.Sprintf("Structural: %q→%q, ratio=%.2f, paras=%d/%d",
			p.Header, enKeyword, best.ratio, prayerParas, best.enP.Paras)

		proposals = append(proposals, Proposal{
			Language:     lang,
			SourceID:     p.SourceID,
			CurrentCode:  currentPhelps,
			ProposedCode: best.code,
			Confidence:   confidence,
			Strategy:     "structural",
			Reason:       reason,
			Evidence: ProposalEvidence{
				CategoryMatch:   enKeyword,
				AuthorMatch:     best.enP.Author,
				LengthRatio:     best.ratio,
				OpeningPhrase:   firstLine,
				EnOpeningPhrase: best.enP.FirstLine,
				AlreadyInLang:   false,
				ParaCount:       prayerParas,
				EnParaCount:     best.enP.Paras,
			},
		})
	}

	return proposals
}

func abs(a int) int {
	if a < 0 {
		return -a
	}
	return a
}

func abs64(a float64) float64 {
	if a < 0 {
		return -a
	}
	return a
}

// ── Duplicate detection ──────────────────────────────────────────────────────
// Finds prayers where multiple different texts share one phelps code in a
// language. These need sub-codes or one of them is wrong.

func runDetectDuplicates(doltDir, lang string) []Proposal {
	rows := doltQuery(doltDir, fmt.Sprintf(
		`SELECT phelps, COUNT(*) as cnt FROM writings `+
			`WHERE language='%s' AND source='%s' AND phelps IS NOT NULL AND phelps<>'' `+
			`AND phelps NOT LIKE 'TMP%%' `+
			`GROUP BY phelps HAVING cnt > 1 ORDER BY cnt DESC`,
		sqlEsc(lang), sqlEsc(prayerSource)))

	var proposals []Proposal
	for _, r := range rows {
		code := r["phelps"]
		cnt, _ := strconv.Atoi(r["cnt"])
		// Get the actual prayer entries
		entries := doltQuery(doltDir, fmt.Sprintf(
			`SELECT source_id, LEFT(text, 120) as text, CHAR_LENGTH(text) as tlen FROM writings `+
				`WHERE language='%s' AND source='%s' AND phelps='%s' ORDER BY CAST(source_id AS UNSIGNED)`,
			sqlEsc(lang), sqlEsc(prayerSource), sqlEsc(code)))

		for i, e := range entries {
			if i == 0 {
				continue // Keep the first one, flag the rest
			}
			firstLine := ""
			for _, line := range strings.Split(strings.TrimSpace(e["text"]), "\n") {
				line = strings.TrimSpace(line)
				if line != "" && !strings.HasPrefix(line, "#") && !strings.HasPrefix(line, "*") {
					if len(line) > 80 {
						line = line[:80]
					}
					firstLine = line
					break
				}
			}
			proposals = append(proposals, Proposal{
				Language:     lang,
				SourceID:     e["source_id"],
				CurrentCode:  code,
				ProposedCode: "TMP?????", // needs manual assignment
				Confidence:   0.0,
				Strategy:     "duplicate",
				Reason:       fmt.Sprintf("Code %s shared by %d prayers in %s — needs sub-code or correction", code, cnt, lang),
				Evidence: ProposalEvidence{
					OpeningPhrase: firstLine,
					AlreadyInLang: true,
				},
			})
		}
	}

	if len(proposals) > 0 {
		fmt.Fprintf(os.Stderr, "[duplicates] Found %d duplicate-code entries in %s\n", len(proposals), lang)
	}
	return proposals
}

// ── Verification ─────────────────────────────────────────────────────────────
//
// --verify checks data quality for already-matched prayers:
//   1. Invalid codes  — phelps not in inventory (Gemini hallucination)
//   2. False positives — known FP codes (ABU0030 etc.)
//   3. Duplicate codes — same code on multiple prayers in same language
//   4. Category mismatch — code's English category ≠ prayer's section
//   5. Statistical outliers — code covers an implausible fraction of language
//   6. Gemini re-verify — ask Gemini to confirm suspicious matches (--reverify)

type VerifyIssue struct {
	SourceID string
	Phelps   string
	Reason   string
	Severity string // "clear", "warn", "info"
	EnFirst  string
	TextSnip string
}

func runVerify(doltDir, lang string, inv map[string]InvEntry, reverify, dryRun bool) {
	fmt.Fprintf(os.Stderr, "\n=== Verify: %s ===\n", lang)

	// Load all matched prayers for this language
	rows := doltQuery(doltDir, fmt.Sprintf(
		`SELECT source_id, phelps, LEFT(text,120) as text, CHAR_LENGTH(text) as text_len FROM writings `+
			`WHERE language='%s' AND source='%s' AND phelps IS NOT NULL AND phelps <> '' `+
			`ORDER BY CAST(source_id AS UNSIGNED)`, sqlEsc(lang), sqlEsc(prayerSource)))

	if len(rows) == 0 {
		fmt.Fprintln(os.Stderr, "No matched prayers found.")
		return
	}
	fmt.Fprintf(os.Stderr, "Checking %d matched prayers...\n\n", len(rows))

	// Load prayer_book_structure section→category mapping for this language
	secRows := doltQuery(doltDir, fmt.Sprintf(
		`SELECT DISTINCT w.source_id, pbs.category_name `+
			`FROM writings w JOIN prayer_book_structure pbs `+
			`ON pbs.phelps_code = w.phelps AND pbs.source_language = '%s:bp' `+
			`WHERE w.language = '%s' AND w.source='%s'`,
		sqlEsc(lang), sqlEsc(lang), sqlEsc(prayerSource)))
	sectionCat := map[string]string{}
	for _, r := range secRows {
		sectionCat[r["source_id"]] = r["category_name"]
	}

	// Load English category for each phelps code
	enCatRows := doltQuery(doltDir,
		`SELECT DISTINCT phelps_code, category_name FROM prayer_book_structure WHERE source_language='en:bp'`)
	enCodeCat := map[string]string{}
	for _, r := range enCatRows {
		enCodeCat[r["phelps_code"]] = r["category_name"]
	}

	// Load set of phelps codes that have at least one English entry in the DB.
	// Used by Check 8: prayers assigned a code with no English counterpart are suspicious.
	enCodeRows := doltQuery(doltDir,
		`SELECT DISTINCT phelps FROM writings WHERE language='en' AND source='bahaiprayers.net' AND phelps IS NOT NULL`)
	enCodes := map[string]bool{}
	for _, r := range enCodeRows {
		enCodes[r["phelps"]] = true
	}

	// Load rosetta category mappings for this language (local section → English category)
	vocabRows := doltQuery(doltDir, fmt.Sprintf(
		`SELECT local_term, en_meaning FROM rosetta_vocab WHERE language='%s' AND term_type='category'`,
		sqlEsc(lang)))
	localToEnCat := map[string]string{}
	for _, r := range vocabRows {
		localToEnCat[r["local_term"]] = r["en_meaning"]
	}

	// Count code frequency in this language
	codeCount := map[string]int{}
	for _, r := range rows {
		codeCount[r["phelps"]]++
	}
	total := len(rows)

	var issues []VerifyIssue
	var clearCount, warnCount int

	for _, r := range rows {
		sid := r["source_id"]
		code := r["phelps"]
		text := r["text"]
		snip := text
		if len(snip) > 60 {
			snip = snip[:60]
		}
		snip = strings.ReplaceAll(snip, "\n", " ")
		enFirst := ""
		if e, ok := inv[code]; ok {
			enFirst = e.FirstEN
		}

		issue := VerifyIssue{SourceID: sid, Phelps: code, EnFirst: enFirst, TextSnip: snip}

		// Check 1: Code not in inventory
		// Sub-passage codes (e.g. AB00431PIT) have 3-letter suffix — check base code too
		// TMP codes (TMP00001) are legitimate temporary codes — never flag as hallucination
		subPassageRe := regexp.MustCompile(`^([A-Z]{2,3}\d{4,5})[A-Z]{3}$`)
		_, inInv := inv[code]
		baseCode := code
		isSubPassage := false
		isTMP := strings.HasPrefix(code, "TMP")
		if !inInv && !isTMP {
			if m := subPassageRe.FindStringSubmatch(code); m != nil {
				baseCode = m[1]
				_, inInv = inv[baseCode]
				isSubPassage = inInv
			}
		}
		if !inInv && !isTMP {
			issue.Reason = fmt.Sprintf("code %s not in inventory (likely hallucination)", code)
			issue.Severity = "clear"
			issues = append(issues, issue)
			clearCount++
			continue
		}
		_ = isSubPassage // used below

		// Check 2: Known false positive codes (also catches sub-passages of FP base codes)
		if falsePositiveCodes[code] || falsePositiveCodes[baseCode] {
			issue.Reason = fmt.Sprintf("known false positive code %s", code)
			issue.Severity = "clear"
			issues = append(issues, issue)
			clearCount++
			continue
		}

		// Check 3: Base code used where sub-passage codes exist for same base
		// (e.g. prayer has BH01234 but BH01234ABC, BH01234DEF are in DB for this lang)
		if !isSubPassage && subPassageRe.FindString(code) == "" {
			subRows := doltQuery(doltDir, fmt.Sprintf(
				`SELECT COUNT(*) as cnt FROM writings `+
					`WHERE language='%s' AND source='bahaiprayers.net' `+
					`AND phelps REGEXP '^%s[A-Z]{3}$'`, sqlEsc(lang), sqlEsc(code)))
			if len(subRows) > 0 && subRows[0]["cnt"] != "0" {
				issue.Reason = fmt.Sprintf("base code %s used but sub-passage codes exist in %s (may need sub-code)", code, lang)
				issue.Severity = "warn"
				issues = append(issues, issue)
				warnCount++
			}
		}

		// Check 4: Statistical outlier (code covers >25% of all matched prayers in lang)
		if count := codeCount[code]; count > 1 && float64(count)/float64(total) > 0.25 && total > 8 {
			issue.Reason = fmt.Sprintf("code used %d/%d times (%.0f%%) — likely over-matched",
				count, total, 100*float64(count)/float64(total))
			issue.Severity = "warn"
			issues = append(issues, issue)
			warnCount++
			continue
		}

		// Check 5: Duplicate codes (same code on 2+ prayers, same language)
		// Only flag once per code (first occurrence already adds it)
		if codeCount[code] > 1 {
			// Count how many times we've already added this code to issues
			alreadyFlagged := 0
			for _, existing := range issues {
				if existing.Phelps == code && strings.Contains(existing.Reason, "possible duplicate") {
					alreadyFlagged++
				}
			}
			if alreadyFlagged == 0 { // flag only the first duplicate pair, not every instance
				issue.Reason = fmt.Sprintf("code assigned to %d prayers in %s (possible duplicate)", codeCount[code], lang)
				issue.Severity = "warn"
				issues = append(issues, issue)
				warnCount++
			}
		}

		// Check 6: Category mismatch (LLM-free: rosetta vocab maps local section → English category)
		if localSec, ok := sectionCat[sid]; ok && localSec != "" {
			enSec := localToEnCat[localSec]
			codeCat := enCodeCat[code]
			if enSec != "" && codeCat != "" && enSec != codeCat {
				// Exclude "Other" mismatches — too many false alarms
				if enSec != "Other" && codeCat != "Other" {
					issue.Reason = fmt.Sprintf("category mismatch: prayer in %q but code is %q", enSec, codeCat)
					issue.Severity = "warn"
					issues = append(issues, issue)
					warnCount++
				}
			}
		}

		// Check 7: Length anomaly — prayer text length vs median for that code across all languages
		// (LLM-free: if this prayer is <20% or >500% of median length for the code, suspicious)
		prayerLen, _ := strconv.Atoi(r["text_len"])
		lenRows := doltQuery(doltDir, fmt.Sprintf(
			`SELECT AVG(CHAR_LENGTH(text)) as avg_len FROM writings `+
				`WHERE phelps='%s' AND source='bahaiprayers.net'`, sqlEsc(code)))
		if len(lenRows) > 0 && lenRows[0]["avg_len"] != "" {
			var avgLen float64
			fmt.Sscanf(lenRows[0]["avg_len"], "%f", &avgLen)
			if avgLen > 0 {
				ratio := float64(prayerLen) / avgLen
				if ratio < 0.15 || ratio > 6.0 {
					issue.Reason = fmt.Sprintf("length anomaly: prayer is %d chars but avg for %s is %.0f (ratio %.1fx)",
						prayerLen, code, avgLen, ratio)
					issue.Severity = "warn"
					issues = append(issues, issue)
					warnCount++
				}
			}
		}

		// Check 8: No English counterpart in the DB.
		// If a phelps code (or its base code for sub-passages) has no English prayer in the
		// writings table, the assignment is suspicious — most Bahá'í prayers in the corpus
		// have an English version.  TMP codes are excluded (they are expected to lack English).
		// This check runs last so all other context is already available.
		if !isTMP {
			hasEn := enCodes[code]
			if !hasEn && isSubPassage {
				hasEn = enCodes[baseCode]
			}
			if !hasEn {
				issue.Reason = fmt.Sprintf("no English prayer with code %s exists in DB (possibly wrong match)", code)
				issue.Severity = "no-en" // separate bucket — see output section
				issues = append(issues, issue)
				warnCount++
			}
		}
	}

	// Print report
	if len(issues) == 0 {
		fmt.Fprintf(os.Stderr, "No issues found. All %d matches look clean.\n", total)
		return
	}

	noEnCount := 0
	for _, iss := range issues {
		if iss.Severity == "no-en" {
			noEnCount++
		}
	}
	fmt.Fprintf(os.Stderr, "Found %d issues (%d clear, %d no-en, %d warn):\n\n",
		len(issues), clearCount, noEnCount, warnCount-noEnCount)

	var clearIssues, noEnIssues, warnIssues []VerifyIssue
	for _, iss := range issues {
		switch iss.Severity {
		case "clear":
			clearIssues = append(clearIssues, iss)
		case "no-en":
			noEnIssues = append(noEnIssues, iss)
		default:
			warnIssues = append(warnIssues, iss)
		}
	}

	// NO-EN issues first: prayers with no English counterpart in the DB.
	// These are the highest-priority review candidates — a wrong match is most
	// easily spotted when there is no English reference to compare against.
	if len(noEnIssues) > 0 {
		fmt.Fprintln(os.Stderr, "--- NO-EN (no English prayer with this code — check match) ---")
		suspicious := noEnIssues
		if reverify && len(suspicious) > 0 {
			suspicious = geminiReverify(suspicious, lang, inv, doltDir)
		}
		for _, iss := range suspicious {
			fmt.Fprintf(os.Stderr, "  [NO-EN] id=%s phelps=%s\n    reason: %s\n    text: %q\n\n",
				iss.SourceID, iss.Phelps, iss.Reason, iss.TextSnip)
		}
	}

	// WARN issues: optionally reverify with Gemini
	if len(warnIssues) > 0 {
		fmt.Fprintln(os.Stderr, "--- WARN (suspicious, needs review) ---")
		suspicious := warnIssues
		if reverify && len(suspicious) > 0 {
			suspicious = geminiReverify(suspicious, lang, inv, doltDir)
		}
		for _, iss := range suspicious {
			fmt.Fprintf(os.Stderr, "  [%s] id=%s phelps=%s\n    reason: %s\n    text: %q\n    en:   %q\n\n",
				iss.Severity, iss.SourceID, iss.Phelps, iss.Reason, iss.TextSnip, iss.EnFirst)
		}
	}

	// Output SQL for clear issues
	if len(clearIssues) > 0 {
		fmt.Printf("\n-- Verify: clear false positives for %s --\n", lang)
		fmt.Printf("-- %d prayers with invalid/FP codes\n\n", len(clearIssues))
		fmt.Println("SET FOREIGN_KEY_CHECKS=0;")
		for _, iss := range clearIssues {
			fmt.Fprintf(os.Stderr, "  [CLEAR] id=%s phelps=%s — %s\n", iss.SourceID, iss.Phelps, iss.Reason)
			fmt.Printf("-- %s\nUPDATE writings SET phelps=NULL WHERE source_id='%s' AND language='%s' AND source='%s';\n\n",
				iss.Reason, sqlEsc(iss.SourceID), sqlEsc(lang), sqlEsc(prayerSource))
		}
		fmt.Println("SET FOREIGN_KEY_CHECKS=1;")

		if !dryRun {
			fmt.Fprintln(os.Stderr, "\nApplying clears to DB...")
			var sb strings.Builder
			sb.WriteString("SET FOREIGN_KEY_CHECKS=0; ")
			for _, iss := range clearIssues {
				fmt.Fprintf(&sb, "UPDATE writings SET phelps=NULL WHERE source_id='%s' AND language='%s' AND source='%s'; ",
					sqlEsc(iss.SourceID), sqlEsc(lang), sqlEsc(prayerSource))
			}
			sb.WriteString("SET FOREIGN_KEY_CHECKS=1;")
			if err := serverExec(doltDir, sb.String()); err == nil {
				fmt.Fprintf(os.Stderr, "Cleared %d false positives.\n", len(clearIssues))
			}
		}
	}
}

// geminiReverify asks Gemini to confirm each suspicious match.
// Returns only the issues where Gemini confirms the match is wrong (CLEAR) or still suspicious.
func geminiReverify(issues []VerifyIssue, lang string, inv map[string]InvEntry, doltDir string) []VerifyIssue {
	fmt.Fprintf(os.Stderr, "[reverify] Asking Gemini to re-check %d suspicious matches...\n", len(issues))

	batchSize := 8
	var confirmed []VerifyIssue

	for i := 0; i < len(issues); i += batchSize {
		batch := issues[i:]
		if len(batch) > batchSize {
			batch = batch[:batchSize]
		}

		var blocks []string
		for _, iss := range batch {
			enFirst := iss.EnFirst
			if enFirst == "" {
				if e, ok := inv[iss.Phelps]; ok {
					enFirst = e.FirstEN
				}
			}
			blocks = append(blocks, fmt.Sprintf(
				"[id=%s]\nPrayer text (%s): %q\nAssigned code %s: %q\nReason flagged: %s",
				iss.SourceID, lang, iss.TextSnip, iss.Phelps, enFirst, iss.Reason))
		}

		prompt := fmt.Sprintf(
			"For each prayer below, decide if the assigned Phelps code is correct.\n"+
				"Reply with JSON: {\"SOURCE_ID\": \"CORRECT\", \"SOURCE_ID\": \"WRONG\", \"SOURCE_ID\": \"UNCERTAIN\"}\n\n"+
				"%s", strings.Join(blocks, "\n\n---\n\n"))

		resp := geminiCall(prompt, 2)
		idMap := extractJSON(resp)
		if idMap == nil {
			confirmed = append(confirmed, batch...) // can't verify, keep all as warnings
			continue
		}

		for _, iss := range batch {
			verdict := strings.ToUpper(strings.TrimSpace(idMap[iss.SourceID]))
			switch verdict {
			case "WRONG":
				iss.Severity = "clear"
				iss.Reason = iss.Reason + " [Gemini: WRONG]"
				confirmed = append(confirmed, iss)
			case "CORRECT":
				// Gemini says it's fine — drop from issues
				fmt.Fprintf(os.Stderr, "  [reverify] id=%s: Gemini confirmed CORRECT\n", iss.SourceID)
			default:
				iss.Reason = iss.Reason + " [Gemini: UNCERTAIN]"
				confirmed = append(confirmed, iss)
			}
		}
		time.Sleep(1 * time.Second)
	}
	return confirmed
}

// ── Main ───────────────────────────────────────────────────────────────────────

func main() {
	lang := flag.String("lang", "", "Language code (required)")
	strategy := flag.String("strategy", "auto", "Matching strategy: auto, standard, rosetta, headers")
	translate := flag.Bool("translate", false, "In-prompt translation (Gemini translates then identifies)")
	pretranslate := flag.Bool("pretranslate", false, "Pre-translate via trans CLI before identification")
	buildVocabFlag := flag.Bool("build-vocab", false, "Build/update Rosetta vocabulary from matched prayers")
	verifyFlag := flag.Bool("verify", false, "Check data quality of existing matches (invalid codes, duplicates, category mismatches)")
	reverifyFlag := flag.Bool("reverify", false, "With --verify: ask Gemini to re-confirm suspicious matches")
	batchSize := flag.Int("batch-size", 0, "Prayers per Gemini call (0 = auto)")
	loops := flag.Int("loops", 1, "Iterations of match→apply→rebuild (for rosetta strategy)")
	dryRun := flag.Bool("dry-run", false, "Print SQL but don't apply to DB")
	doltDir := flag.String("dolt-dir", defaultDoltDir, "Path to Dolt repo directory")
	invCSV := flag.String("inv-csv", defaultInvCSV, "Path to inventory CSV")
	serverMode := flag.Bool("server", false, "Run as Dolt write server (auto-started; you don't need this manually)")
	applySQL := flag.String("apply-sql", "", "Apply UPDATE/INSERT statements from a SQL file (grep ^UPDATE|^INSERT|^SET)")
	commitMsg := flag.String("commit", "", "After applying, run: dolt add writings && dolt commit -m MSG")
	pushFlag := flag.Bool("push", false, "After committing, run: dolt push origin main")
	recheckTMPs := flag.Bool("recheck-tmps", false, "Include TMP-coded prayers in unresolved set (for re-identification)")
	sourceFlag := flag.String("source", "bahaiprayers.net", "DB source value to read/write (e.g. bahaiprayers.app)")
	proposeFlag := flag.Bool("propose", false, "Write JSON proposals instead of applying matches directly")
	recheckAll := flag.Bool("recheck-all", false, "With --propose: re-examine ALL prayers, not just unresolved/TMP")
	outFlag := flag.String("out", "", "Output file for --propose JSON (default: /tmp/<lang>_proposals.json)")
	detectDups := flag.Bool("detect-duplicates", false, "Find prayers where multiple texts share one code")
	flag.Parse()
	prayerSource = *sourceFlag

	// Server mode: just run the write-serializing server and exit
	if *serverMode {
		runServer(*doltDir)
		return
	}

	// Ensure a write server is running (starts one in this process if needed).
	// All subsequent serverExec calls will go through this single serialized path.
	if !*dryRun {
		serverShutdown = make(chan struct{})
		defer close(serverShutdown)
		ensureServer(*doltDir)
	}

	// --apply-sql: read file, apply UPDATE/INSERT/SET lines, optionally commit+push
	if *applySQL != "" {
		data, err := os.ReadFile(*applySQL)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error reading %s: %v\n", *applySQL, err)
			os.Exit(1)
		}
		var stmts []string
		for _, line := range strings.Split(string(data), "\n") {
			t := strings.TrimSpace(line)
			if strings.HasPrefix(t, "UPDATE ") || strings.HasPrefix(t, "INSERT ") || strings.HasPrefix(t, "SET ") {
				stmts = append(stmts, t)
			}
		}
		if len(stmts) == 0 {
			fmt.Fprintln(os.Stderr, "No UPDATE/INSERT/SET statements found in file.")
			os.Exit(1)
		}
		sql := strings.Join(stmts, " ")
		fmt.Fprintf(os.Stderr, "Applying %d statements from %s...\n", len(stmts), *applySQL)
		if *dryRun {
			fmt.Println(sql)
		} else {
			if err := serverExec(*doltDir, sql); err != nil {
				fmt.Fprintf(os.Stderr, "Apply failed: %v\n", err)
				os.Exit(1)
			}
			fmt.Fprintln(os.Stderr, "Done.")
			if *commitMsg != "" {
				doltCommitAndPush(*doltDir, *commitMsg, *pushFlag)
			}
		}
		return
	}

	if *lang == "" {
		fmt.Fprintln(os.Stderr, "Error: --lang is required (or use --apply-sql FILE)")
		flag.Usage()
		os.Exit(1)
	}

	// Load inventory
	inv := loadInventory(*invCSV)
	fmt.Fprintf(os.Stderr, "Loaded %d inventory entries\n", len(inv))

	// Build vocab if requested
	if *buildVocabFlag {
		buildVocab(*doltDir, *lang, inv)
		fmt.Fprintln(os.Stderr, "\nVocabulary built. Run without --build-vocab to match prayers.")
		return
	}

	// Verify data quality if requested
	if *verifyFlag {
		runVerify(*doltDir, *lang, inv, *reverifyFlag, *dryRun)
		return
	}

	// Detect duplicates mode
	if *detectDups {
		proposals := runDetectDuplicates(*doltDir, *lang)
		if len(proposals) == 0 {
			fmt.Fprintln(os.Stderr, "No duplicate codes found.")
			return
		}
		outPath := *outFlag
		if outPath == "" {
			outPath = fmt.Sprintf("/tmp/%s_duplicates.json", *lang)
		}
		writeProposals(proposals, outPath)
		return
	}

	// Propose mode: run structural strategies and write JSON
	if *proposeFlag {
		fmt.Fprintf(os.Stderr, "\n=== Propose mode: %s ===\n", *lang)

		var prayers []Prayer
		if *recheckAll {
			prayers = loadAllPrayers(*doltDir, *lang)
			fmt.Fprintf(os.Stderr, "%s: %d total prayers (recheck-all mode)\n", *lang, len(prayers))
		} else {
			prayers = loadUnresolved(*doltDir, *lang, true) // always include TMPs in propose mode
			fmt.Fprintf(os.Stderr, "%s: %d unresolved prayers (including TMPs)\n", *lang, len(prayers))
		}
		if len(prayers) == 0 {
			fmt.Fprintln(os.Stderr, "Nothing to propose.")
			return
		}

		enProps := loadEnglishPropsMap(*doltDir)
		fmt.Fprintf(os.Stderr, "Loaded %d English prayer properties\n", len(enProps))

		var allProposals []Proposal

		// Strategy 1: Position matching (best for languages with PBS or headers)
		fmt.Fprintln(os.Stderr, "\n--- Position matching ---")
		posProposals := runPositionMatch(*doltDir, *lang, prayers, enProps, inv, *recheckAll)
		allProposals = append(allProposals, posProposals...)

		// Collect already-proposed source_ids to avoid double-proposing
		proposed := map[string]bool{}
		for _, p := range allProposals {
			proposed[p.SourceID] = true
		}

		// Strategy 2: Cross-language voting
		fmt.Fprintln(os.Stderr, "\n--- Cross-language voting ---")
		var remaining []Prayer
		for _, p := range prayers {
			if !proposed[p.SourceID] {
				remaining = append(remaining, p)
			}
		}
		crossProposals := runCrossLangVote(*doltDir, *lang, remaining, enProps, inv, *recheckAll)
		allProposals = append(allProposals, crossProposals...)
		for _, p := range crossProposals {
			proposed[p.SourceID] = true
		}

		// Strategy 3: Structural fingerprint (for anything still unmatched)
		fmt.Fprintln(os.Stderr, "\n--- Structural fingerprint ---")
		remaining = nil
		for _, p := range prayers {
			if !proposed[p.SourceID] {
				remaining = append(remaining, p)
			}
		}
		structProposals := runStructuralMatch(*doltDir, *lang, remaining, enProps, inv, *recheckAll)
		allProposals = append(allProposals, structProposals...)

		// In recheck-all mode, filter to only corrections (proposed != current)
		if *recheckAll {
			var corrections []Proposal
			var confirmations int
			for _, p := range allProposals {
				if p.ProposedCode != p.CurrentCode {
					corrections = append(corrections, p)
				} else {
					confirmations++
				}
			}
			fmt.Fprintf(os.Stderr, "\n=== Recheck summary for %s ===\n", *lang)
			fmt.Fprintf(os.Stderr, "  Confirmed correct: %d\n", confirmations)
			fmt.Fprintf(os.Stderr, "  Proposed corrections: %d\n", len(corrections))
			allProposals = corrections
		}

		// Summary
		fmt.Fprintf(os.Stderr, "\n=== Proposal summary for %s ===\n", *lang)
		fmt.Fprintf(os.Stderr, "  Position: %d proposals\n", len(posProposals))
		fmt.Fprintf(os.Stderr, "  Cross-lang: %d proposals\n", len(crossProposals))
		fmt.Fprintf(os.Stderr, "  Structural: %d proposals\n", len(structProposals))
		fmt.Fprintf(os.Stderr, "  Total: %d proposals (of %d prayers)\n", len(allProposals), len(prayers))

		// Write output
		outPath := *outFlag
		if outPath == "" {
			outPath = fmt.Sprintf("/tmp/%s_proposals.json", *lang)
		}
		if err := writeProposals(allProposals, outPath); err != nil {
			fmt.Fprintf(os.Stderr, "Error writing proposals: %v\n", err)
			os.Exit(1)
		}
		return
	}

	// Load unresolved prayers
	prayers := loadUnresolved(*doltDir, *lang, *recheckTMPs)
	fmt.Fprintf(os.Stderr, "\n%s: %d unresolved prayers\n", *lang, len(prayers))
	if len(prayers) == 0 {
		fmt.Fprintln(os.Stderr, "Nothing to do.")
		return
	}

	// Pre-pass: cross-source matching (texts already matched in another source)
	if xMatches, remaining := crossSourceMatch(*doltDir, *lang, prayers); len(xMatches) > 0 {
		if !*dryRun {
			applyMatches(xMatches, *lang, *doltDir)
		} else {
			outputSQL(*lang, xMatches, nil)
		}
		prayers = remaining
		if len(prayers) == 0 {
			fmt.Fprintln(os.Stderr, "All prayers matched via cross-source. Done.")
			return
		}
	}

	// Pre-pass: bahaiprayers.app English fetch + fingerprint match (zero Gemini tokens)
	if prayerSource == "bahaiprayers.app" {
		fmt.Fprintln(os.Stderr, "Fetching English versions from bahaiprayers.app...")
		if enMatches, remaining := appEnglishMatchPrayers(*doltDir, prayers); len(enMatches) > 0 {
			if !*dryRun {
				applyMatches(enMatches, *lang, *doltDir)
			} else {
				outputSQL(*lang, enMatches, nil)
			}
			prayers = remaining
			if len(prayers) == 0 {
				fmt.Fprintln(os.Stderr, "All prayers matched via app-English. Done.")
				return
			}
		}
		// For remaining prayers, substitute English text so Gemini works in English
		substituted := 0
		for i := range prayers {
			if prayers[i].EnglishHint != "" {
				prayers[i].Text = "## " + prayers[i].Header + "\n\n" + prayers[i].EnglishHint
				substituted++
			}
		}
		if substituted > 0 {
			fmt.Fprintf(os.Stderr, "[app-en] Substituted English text for %d prayers (Gemini will see English)\n", substituted)
		}
	}

	// Determine initial strategy
	strat := *strategy
	isAuto := strat == "auto"
	if isAuto {
		// Special case: zh-Hant benefits from Hans cross-matching first
		if *lang == "zh-Hant" {
			strat = "hant2hans"
		} else {
			strat = detectStrategy(*doltDir, *lang, prayers)
			fmt.Fprintf(os.Stderr, "Auto-detected strategy: %s\n", strat)
		}
	}

	// Default batch sizes
	bs := *batchSize
	if bs == 0 {
		if strat == "rosetta" {
			bs = 3
		} else {
			bs = 6
		}
	}

	// Auto-mode tracks phases: rosetta → standard → translate → done
	// autoPhase values: "rosetta", "standard", "translate"
	autoPhase := strat
	autoTranslate := *translate
	prevRemaining := len(prayers)
	maxRounds := *loops
	if isAuto && *loops == 1 {
		maxRounds = 12 // auto mode runs until convergence (capped at 12 rounds)
	}

	for loop := 1; loop <= maxRounds; loop++ {
		if maxRounds > 1 {
			fmt.Fprintf(os.Stderr, "\n=== Round %d (strategy=%s", loop, autoPhase)
			if autoTranslate {
				fmt.Fprint(os.Stderr, "+translate")
			}
			fmt.Fprintf(os.Stderr, ", %d unresolved) ===\n", len(prayers))
		}

		// Reload prayers after first loop
		if loop > 1 {
			prayers = loadUnresolved(*doltDir, *lang, *recheckTMPs)
			fmt.Fprintf(os.Stderr, "%s: %d unresolved prayers remaining\n", *lang, len(prayers))
			if len(prayers) == 0 {
				break
			}
		}

		var results []MatchResult
		var uncertain []Prayer

		switch autoPhase {
		case "hant2hans":
			// Convert Traditional→Simplified so Gemini identifies correctly
			converted := hantToHansPrayers(prayers)
			// Use rosetta if vocab exists, otherwise standard
			vocab := loadRosettaVocab(*doltDir, *lang)
			if len(vocab) > 0 {
				results, uncertain = runRosettaMatch(converted, *lang, *doltDir, vocab, inv, bs)
			} else {
				results, uncertain = runStandardMatch(converted, *lang, inv, false, false, bs)
			}
			// After hant2hans, fall back to standard for any remaining
			if isAuto {
				autoPhase = "standard"
			}
		case "rosetta":
			// Auto-build vocab if not yet available
			vocab := loadRosettaVocab(*doltDir, *lang)
			if len(vocab) == 0 {
				if isAuto {
					fmt.Fprintln(os.Stderr, "[auto] No rosetta vocab — building vocabulary now...")
					buildVocab(*doltDir, *lang, inv)
					vocab = loadRosettaVocab(*doltDir, *lang)
				}
				if len(vocab) == 0 {
					fmt.Fprintln(os.Stderr, "[auto] Vocab build produced nothing — falling back to standard")
					autoPhase = "standard"
					results, uncertain = runStandardMatch(prayers, *lang, inv, autoTranslate, *pretranslate, bs)
					break
				}
			} else if loop > 1 {
				// Rebuild vocab enriched with newly matched prayers
				buildVocab(*doltDir, *lang, inv)
				vocab = loadRosettaVocab(*doltDir, *lang)
			}
			results, uncertain = runRosettaMatch(prayers, *lang, *doltDir, vocab, inv, bs)
		case "standard":
			results, uncertain = runStandardMatch(prayers, *lang, inv, autoTranslate, *pretranslate, bs)
		default:
			fmt.Fprintf(os.Stderr, "Unknown strategy: %s\n", autoPhase)
			os.Exit(1)
		}

		fmt.Fprintf(os.Stderr, "\nRound %d: %d matches, %d uncertain\n", loop, len(results), len(uncertain))

		lastLoop := !isAuto && loop == maxRounds
		if *dryRun || lastLoop {
			outputSQL(*lang, results, uncertain)
		}

		if !*dryRun && len(results) > 0 {
			applied := applyMatches(results, *lang, *doltDir)
			fmt.Fprintf(os.Stderr, "Applied %d matches to DB\n", applied)
		}

		// Auto-mode convergence: advance phase when stuck
		if isAuto {
			prayers = loadUnresolved(*doltDir, *lang, *recheckTMPs)
			if len(prayers) == 0 {
				break
			}
			if len(prayers) >= prevRemaining {
				// No progress this round — try next phase
				switch autoPhase {
				case "rosetta":
					fmt.Fprintln(os.Stderr, "[auto] Rosetta stalled — switching to standard pass")
					autoPhase = "standard"
					bs = 6
				case "standard":
					if !autoTranslate && (isNonLatinScript(*lang) || *recheckTMPs) {
						fmt.Fprintln(os.Stderr, "[auto] Standard stalled — retrying with translate")
						autoTranslate = true
					} else if *recheckTMPs && !*pretranslate {
						fmt.Fprintln(os.Stderr, "[auto] translate stalled — retrying with pretranslate")
						*pretranslate = true
					} else {
						fmt.Fprintln(os.Stderr, "[auto] No more progress. Stopping.")
						outputSQL(*lang, nil, prayers)
						return
					}
				default:
					fmt.Fprintln(os.Stderr, "[auto] No more progress. Stopping.")
					outputSQL(*lang, nil, prayers)
					return
				}
			}
			prevRemaining = len(prayers)
		}
	}

	// Final SQL output if not dry-run (auto mode outputs at end)
	if isAuto && !*dryRun {
		// Already applied inline — just print remaining unresolved count
		remaining := loadUnresolved(*doltDir, *lang, *recheckTMPs)
		fmt.Fprintf(os.Stderr, "\nDone. %d prayers still unresolved.\n", len(remaining))
	}

	// Auto-commit/push if requested
	if !*dryRun && *commitMsg != "" {
		doltCommitAndPush(*doltDir, *commitMsg, *pushFlag)
	}
}
