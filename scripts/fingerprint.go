// fingerprint.go — Language-agnostic fingerprint matching for Bahá'í prayers.
//
// Computes structural/statistical fingerprint vectors for prayers and uses
// Euclidean distance (after z-score normalization) to match LLM English
// translations against reference prayers. Supplements with word-overlap
// scoring when both texts are English.
//
// Usage:
//
//	go run scripts/fingerprint.go
//	go run scripts/fingerprint.go --apply > fix_llm.sql
//	go run scripts/fingerprint.go --threshold 0.60 --gap 0.10
//	go run scripts/fingerprint.go -v         # include OK entries
package main

import (
	"encoding/csv"
	"flag"
	"fmt"
	"math"
	"os"
	"os/exec"
	"regexp"
	"sort"
	"strings"
	"unicode"
)

const (
	numFeatures = 34
	dbPath      = "/home/joop/bahaiwritings"
)

// ── Data access ──────────────────────────────────────────────────────────

func doltCSV(sql string) [][]string {
	cmd := exec.Command("dolt", "sql", "-r", "csv", "-q", sql)
	cmd.Dir = dbPath
	out, err := cmd.Output()
	if err != nil {
		fmt.Fprintf(os.Stderr, "dolt error: %v\n%s\n", err, string(out))
		os.Exit(1)
	}
	r := csv.NewReader(strings.NewReader(string(out)))
	r.LazyQuotes = true
	rows, _ := r.ReadAll()
	if len(rows) < 2 {
		return nil
	}
	return rows[1:] // skip header
}

// ── Word overlap (for English-English matching) ──────────────────────────

var wordRe = regexp.MustCompile(`[a-zA-Z\x{00C0}-\x{024F}]+`)

func wordSet(text string) map[string]bool {
	words := wordRe.FindAllString(strings.ToLower(text), -1)
	s := make(map[string]bool, len(words))
	for _, w := range words {
		if len(w) > 3 {
			s[w] = true
		}
	}
	return s
}

// jaccard returns |intersection| / |union| — better for comparing texts of different lengths
func jaccard(a, b map[string]bool) float64 {
	if len(a) == 0 || len(b) == 0 {
		return 0
	}
	inter := 0
	for w := range a {
		if b[w] {
			inter++
		}
	}
	union := len(a) + len(b) - inter
	if union == 0 {
		return 0
	}
	return float64(inter) / float64(union)
}

// overlapCoeff returns |intersection| / min(|A|,|B|) — good when one text is a subset
func overlapCoeff(a, b map[string]bool) float64 {
	if len(a) == 0 || len(b) == 0 {
		return 0
	}
	inter := 0
	for w := range a {
		if b[w] {
			inter++
		}
	}
	min := len(a)
	if len(b) < min {
		min = len(b)
	}
	if min == 0 {
		return 0
	}
	return float64(inter) / float64(min)
}

// ── Fingerprint computation ─────────────────────────────────────────────

type fingerprint [numFeatures]float64

var vowels = map[rune]bool{
	'a': true, 'e': true, 'i': true, 'o': true, 'u': true,
	'A': true, 'E': true, 'I': true, 'O': true, 'U': true,
	'\u064E': true, '\u064F': true, '\u0650': true,
	'\u064B': true, '\u064C': true, '\u064D': true,
}

var sentenceEnders = map[rune]bool{
	'.': true, '!': true, '?': true,
	'\u3002': true, '\u06D4': true,
}

func computeFingerprint(text string) fingerprint {
	var fp fingerprint
	if len(text) == 0 {
		return fp
	}

	runes := []rune(text)
	totalChars := float64(len(runes))

	// Paragraphs
	paragraphs := strings.Split(text, "\n\n")
	var nonEmpty []string
	for _, p := range paragraphs {
		p = strings.TrimSpace(p)
		if p != "" {
			nonEmpty = append(nonEmpty, p)
		}
	}
	if len(nonEmpty) == 0 {
		nonEmpty = []string{text}
	}
	paragraphs = nonEmpty
	paraCount := float64(len(paragraphs))

	// Sentences
	sentCount := 0
	for _, r := range runes {
		if sentenceEnders[r] {
			sentCount++
		}
	}
	if sentCount == 0 {
		sentCount = 1
	}

	// Lines
	lines := strings.Split(text, "\n")
	lineCount := 0
	for _, l := range lines {
		if strings.TrimSpace(l) != "" {
			lineCount++
		}
	}
	if lineCount == 0 {
		lineCount = 1
	}

	// Paragraph lengths
	paraLens := make([]float64, len(paragraphs))
	for i, p := range paragraphs {
		paraLens[i] = float64(len([]rune(p)))
	}
	avgParaLen := totalChars / paraCount
	paraLenVar := 0.0
	for _, pl := range paraLens {
		d := pl - avgParaLen
		paraLenVar += d * d
	}
	if paraCount > 1 {
		paraLenVar /= (paraCount - 1)
	}

	// Words
	fields := strings.Fields(text)
	wordCount := float64(len(fields))
	if wordCount == 0 {
		wordCount = 1
	}

	wordLens := make([]float64, len(fields))
	wordFreq := map[string]int{}
	for i, w := range fields {
		wordLens[i] = float64(len([]rune(w)))
		wordFreq[strings.ToLower(w)]++
	}

	avgWordLen := 0.0
	for _, wl := range wordLens {
		avgWordLen += wl
	}
	avgWordLen /= wordCount

	wordLenVar := 0.0
	for _, wl := range wordLens {
		d := wl - avgWordLen
		wordLenVar += d * d
	}
	if wordCount > 1 {
		wordLenVar /= (wordCount - 1)
	}

	uniqueWords := float64(len(wordFreq))

	hapax := 0
	maxFreq := 0
	for _, c := range wordFreq {
		if c == 1 {
			hapax++
		}
		if c > maxFreq {
			maxFreq = c
		}
	}

	wordFreqEntropy := 0.0
	for _, c := range wordFreq {
		p := float64(c) / wordCount
		if p > 0 {
			wordFreqEntropy -= p * math.Log2(p)
		}
	}

	// Character stats
	charFreq := map[rune]int{}
	for _, r := range runes {
		charFreq[r]++
	}
	uniqueChars := float64(len(charFreq))

	charEntropy := 0.0
	for _, c := range charFreq {
		p := float64(c) / totalChars
		if p > 0 {
			charEntropy -= p * math.Log2(p)
		}
	}

	// Punctuation
	var excl, ques, comma, colon, semi, paren, quote, dash int
	punctTypes := map[rune]bool{}
	for _, r := range runes {
		switch r {
		case '!':
			excl++
			punctTypes[r] = true
		case '?', '\u061F':
			ques++
			punctTypes[r] = true
		case ',', '\u060C':
			comma++
			punctTypes[r] = true
		case ':':
			colon++
			punctTypes[r] = true
		case ';', '\u061B':
			semi++
			punctTypes[r] = true
		case '(', ')', '[', ']':
			paren++
			punctTypes[r] = true
		case '"', '\'', '\u201C', '\u201D', '\u2018', '\u2019', '\u00AB', '\u00BB':
			quote++
			punctTypes[r] = true
		case '-', '\u2013', '\u2014':
			dash++
			punctTypes[r] = true
		}
	}

	// Script stats
	var vowelCnt, upperCnt, digitCnt, diacriticCnt int
	for _, r := range runes {
		if vowels[r] {
			vowelCnt++
		}
		if unicode.IsUpper(r) {
			upperCnt++
		}
		if unicode.IsDigit(r) {
			digitCnt++
		}
		if unicode.In(r, unicode.Mn) {
			diacriticCnt++
		}
	}

	// First/last sentence
	firstSentLen := 0
	for _, r := range runes {
		firstSentLen++
		if sentenceEnders[r] {
			break
		}
	}
	lastSentLen := 0
	for i := len(runes) - 1; i >= 0; i-- {
		lastSentLen++
		if sentenceEnders[runes[i]] && lastSentLen > 1 {
			break
		}
	}

	firstWordLen := 0.0
	if len(fields) > 0 {
		firstWordLen = float64(len([]rune(fields[0])))
	}

	// Assemble vector
	fp[0] = math.Log1p(totalChars)
	fp[1] = paraCount
	fp[2] = float64(sentCount)
	fp[3] = float64(lineCount)
	fp[4] = avgParaLen / math.Max(totalChars, 1)
	fp[5] = math.Log1p(paraLenVar)
	fp[6] = paraLens[0] / totalChars
	fp[7] = paraLens[len(paraLens)-1] / totalChars

	fp[8] = uniqueChars
	fp[9] = charEntropy
	fp[10] = wordCount
	fp[11] = avgWordLen
	fp[12] = wordLenVar
	fp[13] = uniqueWords / wordCount

	fp[14] = float64(excl) / totalChars
	fp[15] = float64(ques) / totalChars
	fp[16] = float64(comma) / totalChars
	fp[17] = float64(colon) / totalChars
	fp[18] = float64(semi) / totalChars
	fp[19] = float64(paren) / totalChars
	fp[20] = float64(quote) / totalChars
	fp[21] = float64(dash) / totalChars
	fp[22] = float64(len(punctTypes)) / 15.0

	fp[23] = math.Sqrt(wordLenVar)
	fp[24] = wordFreqEntropy
	fp[25] = float64(hapax) / wordCount
	fp[26] = float64(maxFreq) / wordCount

	fp[27] = float64(firstSentLen) / totalChars
	fp[28] = float64(lastSentLen) / totalChars
	fp[29] = firstWordLen

	fp[30] = float64(vowelCnt) / totalChars
	fp[31] = float64(upperCnt) / totalChars
	fp[32] = float64(digitCnt) / totalChars
	fp[33] = float64(diacriticCnt) / totalChars

	return fp
}

// ── Z-score normalization ────────────────────────────────────────────────

type stats struct {
	mean, std float64
}

func computeStats(fps []fingerprint) [numFeatures]stats {
	var s [numFeatures]stats
	n := float64(len(fps))
	if n == 0 {
		return s
	}
	for d := 0; d < numFeatures; d++ {
		sum := 0.0
		for i := range fps {
			sum += fps[i][d]
		}
		mean := sum / n
		varSum := 0.0
		for i := range fps {
			diff := fps[i][d] - mean
			varSum += diff * diff
		}
		std := math.Sqrt(varSum / n)
		if std < 1e-12 {
			std = 1
		}
		s[d] = stats{mean, std}
	}
	return s
}

func normalize(fp fingerprint, s [numFeatures]stats) fingerprint {
	var out fingerprint
	for d := 0; d < numFeatures; d++ {
		out[d] = (fp[d] - s[d].mean) / s[d].std
	}
	return out
}

// ── Distance metrics ─────────────────────────────────────────────────────

// euclidean returns the Euclidean distance between two normalized fingerprints.
func euclidean(a, b fingerprint) float64 {
	sum := 0.0
	for d := 0; d < numFeatures; d++ {
		diff := a[d] - b[d]
		sum += diff * diff
	}
	return math.Sqrt(sum)
}

// distToSim converts a Euclidean distance to a 0-1 similarity score.
// Uses a Gaussian kernel: sim = exp(-dist^2 / (2*sigma^2))
// With sigma calibrated so that dist=sqrt(numFeatures) gives sim~0.1
func distToSim(dist float64) float64 {
	// sigma = sqrt(numFeatures) / sqrt(-2*ln(0.1)) ≈ sqrt(34)/2.146 ≈ 2.72
	sigma := math.Sqrt(float64(numFeatures)) / math.Sqrt(-2*math.Log(0.1))
	return math.Exp(-dist * dist / (2 * sigma * sigma))
}

// ── Main ─────────────────────────────────────────────────────────────────

type prayer struct {
	phelps  string
	version string
	text    string
	fp      fingerprint
	words   map[string]bool // word set for overlap matching
}

func main() {
	apply := flag.Bool("apply", false, "Output SQL UPDATE statements")
	threshold := flag.Float64("threshold", 0.55, "Min combined score to call WRONG")
	gap := flag.Float64("gap", 0.10, "Min gap between 1st and 2nd match")
	verbose := flag.Bool("v", false, "Verbose: show all LLM entries including OK")
	fpOnly := flag.Bool("fp-only", false, "Use fingerprint distance only (no word overlap)")
	flag.Parse()

	fmt.Fprintln(os.Stderr, "Loading reference prayers (English from bahaiprayers.net)...")
	refRows := doltCSV(`SELECT phelps, LEFT(text, 3000) as text FROM writings
		WHERE language='en' AND source='bahaiprayers.net'
		AND (type IS NULL OR type='prayer') AND phelps NOT LIKE 'TMP%'`)

	refs := make(map[string]*prayer, len(refRows))
	for _, r := range refRows {
		if len(r) < 2 || r[0] == "" || r[1] == "" {
			continue
		}
		refs[r[0]] = &prayer{phelps: r[0], text: r[1], words: wordSet(r[1])}
	}
	fmt.Fprintf(os.Stderr, "  %d reference prayers loaded\n", len(refs))

	// Load inventory first lines — only for word overlap, not fingerprints
	// (first lines are too short for structural fingerprinting)
	fmt.Fprintln(os.Stderr, "Loading inventory first lines (word overlap only)...")
	invRows := doltCSV("SELECT PIN, `First line (translated)` FROM inventory WHERE `First line (translated)` IS NOT NULL AND `First line (translated)` <> ''")
	invRefs := make(map[string]*prayer) // separate from fingerprint refs
	invAdded := 0
	for _, r := range invRows {
		if len(r) < 2 || r[0] == "" || r[1] == "" {
			continue
		}
		if _, exists := refs[r[0]]; !exists {
			ws := wordSet(r[1])
			if len(ws) < 5 {
				continue // too short for reliable word matching
			}
			invRefs[r[0]] = &prayer{phelps: r[0], text: r[1], words: ws}
			invAdded++
		}
	}
	fmt.Fprintf(os.Stderr, "  %d inventory-only references (word overlap only)\n", invAdded)

	fmt.Fprintln(os.Stderr, "Loading LLM translations...")
	llmRows := doltCSV(`SELECT phelps, version, LEFT(text, 3000) as text FROM writings
		WHERE source='llm-translation' AND language='en'`)

	llms := make([]prayer, 0, len(llmRows))
	for _, r := range llmRows {
		if len(r) < 3 || r[2] == "" {
			continue
		}
		llms = append(llms, prayer{phelps: r[0], version: r[1], text: r[2], words: wordSet(r[2])})
	}
	fmt.Fprintf(os.Stderr, "  %d LLM entries loaded\n", len(llms))

	// Load foreign-language entries for cross-language fingerprint matching
	fmt.Fprintln(os.Stderr, "Loading foreign-language entries for LLM codes...")
	llmCodes := map[string]bool{}
	for i := range llms {
		llmCodes[llms[i].phelps] = true
	}
	codeList := make([]string, 0, len(llmCodes))
	for c := range llmCodes {
		codeList = append(codeList, "'"+strings.ReplaceAll(c, "'", "''")+"'")
	}
	foreignRows := doltCSV(fmt.Sprintf(`SELECT phelps, LEFT(text, 3000) as text FROM writings
		WHERE phelps IN (%s) AND source <> 'llm-translation'
		AND language <> 'en'
		LIMIT 2000`, strings.Join(codeList, ",")))
	type foreignEntry struct {
		code string
		fp   fingerprint
	}
	var foreignRaw []struct {
		code string
		text string
	}
	for _, r := range foreignRows {
		if len(r) >= 2 && r[1] != "" {
			foreignRaw = append(foreignRaw, struct {
				code string
				text string
			}{r[0], r[1]})
		}
	}
	fmt.Fprintf(os.Stderr, "  %d foreign texts loaded\n", len(foreignRaw))

	// ── Compute fingerprints ──
	fmt.Fprintln(os.Stderr, "Computing fingerprints...")

	// Only full-text refs and LLM entries go into the normalization corpus
	var allFPs []fingerprint
	for _, ref := range refs {
		ref.fp = computeFingerprint(ref.text)
		allFPs = append(allFPs, ref.fp)
	}
	for i := range llms {
		llms[i].fp = computeFingerprint(llms[i].text)
		allFPs = append(allFPs, llms[i].fp)
	}
	foreignFPsRaw := make([]fingerprint, len(foreignRaw))
	for i, fr := range foreignRaw {
		foreignFPsRaw[i] = computeFingerprint(fr.text)
		allFPs = append(allFPs, foreignFPsRaw[i])
	}

	// Z-score normalize
	featureStats := computeStats(allFPs)
	for _, ref := range refs {
		ref.fp = normalize(ref.fp, featureStats)
	}
	for i := range llms {
		llms[i].fp = normalize(llms[i].fp, featureStats)
	}
	foreignFPs := make([]foreignEntry, len(foreignRaw))
	for i, fr := range foreignRaw {
		foreignFPs[i] = foreignEntry{fr.code, normalize(foreignFPsRaw[i], featureStats)}
	}

	// Build ref list for matching
	refList := make([]*prayer, 0, len(refs))
	for _, ref := range refs {
		refList = append(refList, ref)
	}

	// ── Combined scoring ──
	// For each LLM entry vs each reference, compute:
	//   fpSim  = Gaussian similarity from Euclidean distance of fingerprints
	//   wordSim = word overlap coefficient (English-English)
	//   combined = 0.3 * fpSim + 0.7 * wordSim  (word overlap is more precise for EN-EN)
	// With --fp-only: combined = fpSim

	fmt.Fprintln(os.Stderr, "Matching...")

	type result struct {
		llmCode   string
		llmVer    string
		bestMatch string
		bestScore float64
		bestFP    float64
		bestWord  float64
		secScore  float64
		secCode   string
		status    string
		opening   string
		curScore  float64
	}

	var results []result
	okCount, wrongCount, unclearCount := 0, 0, 0

	combinedScore := func(llm *prayer, ref *prayer) (combined, fpSim, wSim float64) {
		dist := euclidean(llm.fp, ref.fp)
		fpSim = distToSim(dist)
		if *fpOnly {
			return fpSim, fpSim, 0
		}
		wSim = jaccard(llm.words, ref.words)
		combined = 0.3*fpSim + 0.7*wSim
		return
	}

	for i := range llms {
		llm := &llms[i]

		// Find best and second-best among full-text refs
		bestCode := ""
		bestScore := -1.0
		bestFP := 0.0
		bestWord := 0.0
		secCode := ""
		secScore := -1.0

		for _, ref := range refList {
			score, _, _ := combinedScore(llm, ref)
			if score > bestScore {
				secScore = bestScore
				secCode = bestCode
				bestScore = score
				bestCode = ref.phelps
				bestFP, bestWord = 0, 0
			} else if score > secScore {
				secScore = score
				secCode = ref.phelps
			}
		}
		// Recompute components for best match
		if ref, ok := refs[bestCode]; ok {
			_, bestFP, bestWord = combinedScore(llm, ref)
		}

		// Also check inventory refs (Jaccard word overlap only) — these might be better matches
		for _, inv := range invRefs {
			wSim := jaccard(llm.words, inv.words)
			score := wSim // no fingerprint for inventory entries
			if !*fpOnly && score > bestScore {
				secScore = bestScore
				secCode = bestCode
				bestScore = score
				bestCode = inv.phelps
				bestFP = 0
				bestWord = wSim
			} else if !*fpOnly && score > secScore {
				secScore = score
				secCode = inv.phelps
			}
		}

		// Score against current assignment
		curScore := -1.0
		if ref, ok := refs[llm.phelps]; ok {
			curScore, _, _ = combinedScore(llm, ref)
		} else if inv, ok := invRefs[llm.phelps]; ok {
			curScore = jaccard(llm.words, inv.words)
		}
		// Cross-language fingerprint check against foreign texts under current code
		for _, ff := range foreignFPs {
			if ff.code == llm.phelps {
				dist := euclidean(llm.fp, ff.fp)
				fSim := distToSim(dist)
				if fSim > curScore {
					curScore = fSim
				}
			}
		}

		// Status
		status := "UNCLEAR"
		if bestCode == llm.phelps {
			status = "OK"
			okCount++
		} else if bestScore >= *threshold && (bestScore-secScore) >= *gap {
			status = "WRONG"
			wrongCount++
		} else {
			unclearCount++
		}

		opening := strings.ReplaceAll(llm.text, "\n", " ")
		if len(opening) > 60 {
			opening = opening[:60]
		}

		results = append(results, result{
			llmCode:   llm.phelps,
			llmVer:    llm.version,
			bestMatch: bestCode,
			bestScore: bestScore,
			bestFP:    bestFP,
			bestWord:  bestWord,
			secScore:  secScore,
			secCode:   secCode,
			status:    status,
			opening:   opening,
			curScore:  curScore,
		})
	}

	// Sort: WRONG first, then UNCLEAR, then OK; within each by score desc
	statusOrder := map[string]int{"WRONG": 0, "UNCLEAR": 1, "OK": 2}
	sort.Slice(results, func(i, j int) bool {
		if statusOrder[results[i].status] != statusOrder[results[j].status] {
			return statusOrder[results[i].status] < statusOrder[results[j].status]
		}
		return results[i].bestScore > results[j].bestScore
	})

	// ── Output ──
	if *apply {
		fmt.Println("SET FOREIGN_KEY_CHECKS=0;")
		for _, r := range results {
			if r.status == "WRONG" {
				safe := strings.ReplaceAll(r.bestMatch, "'", "''")
				safeVer := strings.ReplaceAll(r.llmVer, "'", "''")
				fmt.Printf("UPDATE writings SET phelps='%s' WHERE version='%s'; -- was %s, score=%.2f (fp=%.2f word=%.2f)\n",
					safe, safeVer, r.llmCode, r.bestScore, r.bestFP, r.bestWord)
			}
		}
		fmt.Println("SET FOREIGN_KEY_CHECKS=1;")
		fmt.Fprintf(os.Stderr, "\n%d WRONG (SQL emitted), %d UNCLEAR, %d OK\n", wrongCount, unclearCount, okCount)
	} else {
		fmt.Printf("%-14s %-14s %5s %5s %5s %5s %-7s  %s\n",
			"LLM_CODE", "BEST_MATCH", "SCORE", "FP", "WORD", "2ND", "STATUS", "OPENING")
		fmt.Println(strings.Repeat("-", 120))
		for _, r := range results {
			if r.status == "OK" && !*verbose {
				continue
			}
			fmt.Printf("%-14s %-14s %5.2f %5.2f %5.2f %5.2f %-7s  %s\n",
				r.llmCode, r.bestMatch, r.bestScore, r.bestFP, r.bestWord, r.secScore,
				r.status, r.opening)
		}
		fmt.Fprintf(os.Stderr, "\nSummary: %d WRONG, %d UNCLEAR, %d OK (total: %d)\n",
			wrongCount, unclearCount, okCount, len(results))
		fmt.Fprintf(os.Stderr, "Threshold: %.2f, Gap: %.2f\n", *threshold, *gap)
		if *fpOnly {
			fmt.Fprintln(os.Stderr, "Mode: fingerprint-only (no word overlap)")
		} else {
			fmt.Fprintln(os.Stderr, "Mode: combined (0.3*fingerprint + 0.7*word_overlap)")
		}
	}
}
