// verify_llm.go — Verify LLM English translations against inventory/reference prayers.
//
// Uses a low-dimension "theme vector" based on distinctive prayer keywords
// to find the closest matching reference prayer for each LLM entry.
// Outputs SQL to recode mismatched foreign-language prayers and delete wrong LLM entries.
//
// Usage:
//   go run scripts/verify_llm.go --dolt-dir ~/bahaiwritings [--apply] [--threshold 0.3]
package main

import (
	"database/sql"
	"flag"
	"fmt"
	"log"
	"math"
	"os"
	"os/exec"
	"regexp"
	"sort"
	"strings"
	"time"

	_ "github.com/go-sql-driver/mysql"
)

// Stop words — extremely common words in Bahá'í prayer English that don't help distinguish prayers
var stopWords = map[string]bool{
	"the": true, "and": true, "that": true, "thou": true, "thy": true, "thee": true,
	"this": true, "with": true, "from": true, "hast": true, "art": true, "for": true,
	"all": true, "who": true, "his": true, "are": true, "unto": true, "have": true,
	"god": true, "lord": true, "mine": true, "their": true, "which": true, "him": true,
	"upon": true, "not": true, "may": true, "hath": true, "them": true,
	"our": true, "its": true, "but": true, "been": true, "those": true, "they": true,
	"shall": true, "will": true, "doth": true, "dost": true, "was": true, "were": true,
	"has": true, "had": true, "let": true, "nor": true, "yet": true, "every": true,
	"can": true, "one": true, "verily": true, "truly": true, "most": true, "own": true,
	"her": true, "she": true, "what": true, "whom": true, "whose": true,
}

// Vocabulary built from all reference texts (populated at runtime)
var vocab map[string]int    // word -> dimension index
var idf map[string]float64  // word -> inverse document frequency

var wordRe = regexp.MustCompile(`[a-zA-Z]{3,}`)

func tokenize(text string) map[string]int {
	words := wordRe.FindAllString(strings.ToLower(text), -1)
	freq := make(map[string]int)
	for _, w := range words {
		if !stopWords[w] && len(w) >= 3 {
			freq[w]++
		}
	}
	return freq
}

// buildVocab creates the word→dimension mapping from all reference texts.
// Only includes words that appear in 2+ but <50% of documents (informative words).
func buildVocab(docs []map[string]int) {
	docFreq := make(map[string]int) // how many docs contain this word
	for _, d := range docs {
		for w := range d {
			docFreq[w]++
		}
	}
	n := float64(len(docs))
	vocab = make(map[string]int)
	idf = make(map[string]float64)
	idx := 0
	// Collect candidates with IDF scores
	type wordIDF struct {
		word string
		idf  float64
	}
	var candidates []wordIDF
	for w, df := range docFreq {
		if df >= 2 && float64(df)/n < 0.3 && len(w) >= 4 {
			candidates = append(candidates, wordIDF{w, math.Log(n / float64(df))})
		}
	}
	// Keep top 2000 by IDF (most distinctive words)
	sort.Slice(candidates, func(i, j int) bool { return candidates[i].idf > candidates[j].idf })
	if len(candidates) > 2000 {
		candidates = candidates[:2000]
	}
	for _, c := range candidates {
		vocab[c.word] = idx
		idf[c.word] = c.idf
		idx++
	}
	log.Printf("  Vocabulary: %d words (from %d total)", len(vocab), len(docFreq))
}

func vectorize(wordFreq map[string]int) sparseVec {
	vec := make(sparseVec)
	total := 0.0
	for _, c := range wordFreq {
		total += float64(c)
	}
	if total == 0 {
		return vec
	}
	for w, c := range wordFreq {
		if idx, ok := vocab[w]; ok {
			tf := float64(c) / total
			vec[idx] = tf * idf[w]
		}
	}
	return vec
}

func cosine(a, b sparseVec) float64 {
	// Iterate over the smaller vector for efficiency
	if len(a) > len(b) {
		a, b = b, a
	}
	var dot, magA, magB float64
	for idx, va := range a {
		if vb, ok := b[idx]; ok {
			dot += va * vb
		}
		magA += va * va
	}
	for _, vb := range b {
		magB += vb * vb
	}
	if magA == 0 || magB == 0 {
		return 0
	}
	return dot / (math.Sqrt(magA) * math.Sqrt(magB))
}

// Sparse vector: map[dimension_index]value — much more memory-efficient for TF-IDF
type sparseVec map[int]float64

type refEntry struct {
	phelps string
	vec    sparseVec
	text   string // first 100 chars for display
}

type llmEntry struct {
	phelps  string
	version string
	text    string
	vec     sparseVec
}

type match struct {
	llm       llmEntry
	bestCode  string
	bestScore float64
	nextScore float64
	status    string // OK, WRONG, UNCLEAR
}

func main() {
	doltDir := flag.String("dolt-dir", os.Getenv("HOME")+"/bahaiwritings", "Dolt database directory")
	threshold := flag.Float64("threshold", 0.3, "Minimum cosine similarity to consider a match")
	apply := flag.Bool("apply", false, "Output SQL to apply corrections")
	dsn := flag.String("dsn", "", "MySQL DSN (e.g. root@tcp(127.0.0.1:3306)/bahaiwritings). If empty, starts a temporary dolt sql-server.")
	flag.Parse()

	log.Printf("Loading data from %s...", *doltDir)

	var db *sql.DB
	var err error

	if *dsn != "" {
		db, err = sql.Open("mysql", *dsn)
	} else {
		// Start a temporary dolt sql-server
		dbName := "bahaiwritings"
		port := "13337"
		log.Printf("Starting temporary dolt sql-server on port %s...", port)
		cmd := exec.Command("dolt", "sql-server", "-H", "127.0.0.1", "-P", port, "--no-auto-commit")
		cmd.Dir = *doltDir
		cmd.Stdout = os.Stderr
		cmd.Stderr = os.Stderr
		if err := cmd.Start(); err != nil {
			log.Fatalf("Cannot start dolt sql-server: %v", err)
		}
		defer func() {
			cmd.Process.Kill()
			cmd.Wait()
		}()
		// Wait for server to be ready
		for i := 0; i < 30; i++ {
			db, err = sql.Open("mysql", fmt.Sprintf("root@tcp(127.0.0.1:%s)/%s", port, dbName))
			if err == nil {
				if err = db.Ping(); err == nil {
					break
				}
			}
			time.Sleep(500 * time.Millisecond)
		}
		if err != nil {
			log.Fatalf("Cannot connect after starting server: %v", err)
		}
		_ = dbName
	}
	if err != nil {
		log.Fatalf("Cannot open database: %v", err)
	}
	defer db.Close()

	// Phase 1: Load all text and tokenize
	log.Println("Loading reference prayers...")
	type rawRef struct {
		phelps string
		text   string
		words  map[string]int
	}
	refRaw := make(map[string]*rawRef)

	// Inventory
	rows, err := db.Query(`SELECT PIN, ` + "`First line (translated)`" + ` FROM inventory
		WHERE ` + "`First line (translated)`" + ` IS NOT NULL AND ` + "`First line (translated)`" + ` <> ''`)
	if err != nil {
		log.Fatal(err)
	}
	for rows.Next() {
		var pin, text string
		rows.Scan(&pin, &text)
		refRaw[pin] = &rawRef{phelps: pin, text: text, words: tokenize(text)}
	}
	rows.Close()

	// English prayers from bahaiprayers.net (override inventory with longer text)
	rows, err = db.Query(`SELECT phelps, LEFT(text, 2000) FROM writings
		WHERE language='en' AND source='bahaiprayers.net' AND (type IS NULL OR type='prayer')
		AND phelps NOT LIKE 'TMP%'`)
	if err != nil {
		log.Fatal(err)
	}
	for rows.Next() {
		var phelps, text string
		rows.Scan(&phelps, &text)
		w := tokenize(text)
		if existing, ok := refRaw[phelps]; !ok || len(w) > len(existing.words) {
			refRaw[phelps] = &rawRef{phelps: phelps, text: text, words: w}
		}
	}
	rows.Close()
	log.Printf("  %d reference entries", len(refRaw))

	// Load LLM entries (tokenize only, vectorize after vocab is built)
	log.Println("Loading LLM entries...")
	type rawLLM struct {
		phelps  string
		version string
		text    string
		words   map[string]int
	}
	var llmRaw []rawLLM
	rows, err = db.Query(`SELECT phelps, version, LEFT(text, 2000) FROM writings
		WHERE source='llm-translation' AND language='en'`)
	if err != nil {
		log.Fatal(err)
	}
	for rows.Next() {
		var e rawLLM
		rows.Scan(&e.phelps, &e.version, &e.text)
		e.words = tokenize(e.text)
		llmRaw = append(llmRaw, e)
	}
	rows.Close()
	log.Printf("  %d LLM entries", len(llmRaw))

	// Phase 2: Build vocabulary from all reference docs
	log.Println("Building vocabulary...")
	allDocs := make([]map[string]int, 0, len(refRaw))
	for _, r := range refRaw {
		allDocs = append(allDocs, r.words)
	}
	buildVocab(allDocs)

	// Phase 3: Vectorize everything
	log.Println("Vectorizing...")
	refs := make(map[string]*refEntry)
	for code, r := range refRaw {
		refs[code] = &refEntry{phelps: code, vec: vectorize(r.words), text: r.text[:min(len(r.text), 80)]}
	}

	var llmEntries []llmEntry
	for _, r := range llmRaw {
		llmEntries = append(llmEntries, llmEntry{
			phelps: r.phelps, version: r.version, text: r.text, vec: vectorize(r.words),
		})
	}

	// Build inverted index: word dimension -> list of ref codes that have it
	log.Println("Building inverted index...")
	invertedIdx := make(map[int][]string) // dim -> phelps codes
	for code, r := range refs {
		for dim := range r.vec {
			invertedIdx[dim] = append(invertedIdx[dim], code)
		}
	}

	// Match using inverted index to prefilter candidates
	log.Println("Matching...")
	var matches []match

	for _, llm := range llmEntries {
		// Find candidate refs that share at least one vocab word
		candidateScores := make(map[string]int) // code -> shared word count
		for dim := range llm.vec {
			for _, code := range invertedIdx[dim] {
				candidateScores[code]++
			}
		}
		// Only compute cosine for top 500 candidates by shared word count
		type cand struct {
			code  string
			count int
		}
		var cands []cand
		for code, count := range candidateScores {
			cands = append(cands, cand{code, count})
		}
		sort.Slice(cands, func(i, j int) bool { return cands[i].count > cands[j].count })
		if len(cands) > 500 {
			cands = cands[:500]
		}

		type scored struct {
			code  string
			score float64
		}
		var scores []scored
		for _, c := range cands {
			r := refs[c.code]
			s := cosine(llm.vec, r.vec)
			scores = append(scores, scored{r.phelps, s})
		}
		sort.Slice(scores, func(i, j int) bool { return scores[i].score > scores[j].score })

		bestCode := ""
		bestScore := 0.0
		nextScore := 0.0
		if len(scores) > 0 {
			bestCode = scores[0].code
			bestScore = scores[0].score
		}
		if len(scores) > 1 {
			nextScore = scores[1].score
		}

		status := "UNCLEAR"
		if bestCode == llm.phelps {
			status = "OK"
		} else if bestScore >= *threshold && bestScore > nextScore*1.2 {
			status = "WRONG"
		}

		matches = append(matches, match{llm: llm, bestCode: bestCode, bestScore: bestScore, nextScore: nextScore, status: status})
	}

	// Sort by status then score
	sort.Slice(matches, func(i, j int) bool {
		if matches[i].status != matches[j].status {
			order := map[string]int{"WRONG": 0, "UNCLEAR": 1, "OK": 2}
			return order[matches[i].status] < order[matches[j].status]
		}
		return matches[i].bestScore > matches[j].bestScore
	})

	// Output
	ok, wrong, unclear := 0, 0, 0
	fmt.Printf("%-14s %-14s %5s %5s %-7s %s\n", "LLM_CODE", "BEST_MATCH", "SCORE", "2ND", "STATUS", "OPENING")
	fmt.Println(strings.Repeat("-", 120))

	for _, m := range matches {
		opening := strings.ReplaceAll(m.llm.text[:min(len(m.llm.text), 55)], "\n", " ")
		fmt.Printf("%-14s %-14s %5.2f %5.2f %-7s %s\n",
			m.llm.phelps, m.bestCode, m.bestScore, m.nextScore, m.status, opening)

		switch m.status {
		case "OK":
			ok++
		case "WRONG":
			wrong++
		default:
			unclear++
		}
	}

	fmt.Printf("\n--- Summary: %d OK, %d WRONG, %d unclear (threshold=%.2f) ---\n",
		ok, wrong, unclear, *threshold)

	// Output SQL
	if *apply {
		fmt.Println("\n-- SQL to apply corrections:")
		fmt.Println("-- WARNING: Review before applying! The theme-vector matching is approximate.")
		fmt.Println("-- High-score matches (>0.5) are very likely correct.")
		fmt.Println("-- Lower scores need manual verification.")
		fmt.Println("SET FOREIGN_KEY_CHECKS=0;")
		for _, m := range matches {
			if m.status == "WRONG" {
				// The LLM entry under code X tells us the foreign-language prayer is actually code Y.
				// But we must be careful: code X might have CORRECT entries in other languages.
				// The LLM was generated from a specific source language entry.
				// Strategy: find which non-English entries under code X DON'T have code Y already,
				// and only recode those where the LLM was the reference.
				// For safety, we recode ALL non-English non-inventory entries from X to Y,
				// but only if X doesn't have many languages (suggesting it's a single-language code).
				opening := strings.ReplaceAll(m.llm.text[:min(len(m.llm.text), 80)], "\n", " ")
				fmt.Printf("\n-- %s → %s (score=%.2f, gap=%.2f)\n", m.llm.phelps, m.bestCode, m.bestScore, m.bestScore-m.nextScore)
				fmt.Printf("-- LLM: %s\n", opening)

				if m.bestScore >= 0.5 {
					fmt.Printf("-- HIGH CONFIDENCE\n")
				}

				// Delete the LLM English entry
				fmt.Printf("DELETE FROM writings WHERE version='%s';\n", m.llm.version)

				// Recode foreign entries — but only if this code is likely single-language
				// (codes with many languages are more complex, skip auto-recode)
				fmt.Printf("-- To recode foreign entries, verify first then run:\n")
				fmt.Printf("-- UPDATE writings SET phelps='%s' WHERE phelps='%s' AND language<>'en' AND source<>'inventory' AND (type IS NULL OR type='prayer');\n",
					m.bestCode, m.llm.phelps)
			}
		}

		// Delete OK entries that have a real English counterpart
		fmt.Println("\n-- Delete OK LLM entries where real English exists:")
		for _, m := range matches {
			if m.status == "OK" {
				fmt.Printf("-- DELETE FROM writings WHERE version='%s'; -- %s\n", m.llm.version, m.llm.phelps)
			}
		}

		fmt.Println("\nSET FOREIGN_KEY_CHECKS=1;")
	}
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
