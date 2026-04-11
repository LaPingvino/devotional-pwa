// fp_test.go — Benchmark fingerprint vectors for cross-language prayer matching.
//
// Measures how well the N-dimensional language-agnostic fingerprint vectors
// identify the same prayer across different languages, using the Dolt database
// ground truth (same phelps code = same prayer).
//
// Usage:
//
//	go run fp_test.go --quick     # fast mode (~50 codes, ~200 queries)
//	go run fp_test.go             # thorough mode (~200 codes, ~500 queries)
//	go run fp_test.go --ablation  # include feature ablation (slow)
package main

import (
	"bufio"
	"encoding/csv"
	"flag"
	"fmt"
	"math"
	"math/rand"
	"os"
	"os/exec"
	"sort"
	"strings"
	"unicode"
)

const (
	numFeatures = 31
	dbPath      = "/home/joop/bahaiwritings"
	dictPath    = "/tmp/claude/term_dict_clean.csv"
)

// ── Semantic term dictionary ───────────────────────────────────────────

// semCategory maps a category name to its constituent term_en values.
var semCategories = []struct {
	name  string
	terms []string
}{
	{"sem_healing", []string{"healing", "healer", "name"}},
	{"sem_children", []string{"children"}},
	{"sem_unity", []string{"unity"}},
	{"sem_morning", []string{"morning", "risen", "grace"}},
	{"sem_burial", []string{"burial", "deceased", "departed"}},
	{"sem_marriage", []string{"marriage"}},
	{"sem_worship", []string{"worship", "witness", "face"}},
	{"sem_teaching", []string{"teaching", "soldiers", "youth"}},
	{"sem_fasting", []string{"fasting", "fast"}},
	{"sem_blessed", []string{"blessed", "spot"}},
	{"sem_tests", []string{"tests", "trials"}},
	{"sem_steadfast", []string{"steadfast", "firm", "mercy", "refresh"}},
}

// termDict: language -> category index -> []lowercase term_local
var termDict map[string][12][]string

func loadTermDict() {
	termDict = make(map[string][12][]string)

	// Build term_en -> category index lookup
	termToCat := map[string]int{}
	for i, cat := range semCategories {
		for _, t := range cat.terms {
			termToCat[t] = i
		}
	}

	f, err := os.Open(dictPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Warning: cannot open term dict %s: %v\n", dictPath, err)
		return
	}
	defer f.Close()

	r := csv.NewReader(f)
	rows, err := r.ReadAll()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Warning: cannot parse term dict: %v\n", err)
		return
	}

	for i, row := range rows {
		if i == 0 || len(row) < 3 {
			continue
		}
		lang := row[0]
		termEN := row[1]
		termLocal := strings.ToLower(row[2])
		if termLocal == "" {
			continue
		}

		catIdx, ok := termToCat[termEN]
		if !ok {
			continue
		}

		arr := termDict[lang]
		arr[catIdx] = append(arr[catIdx], termLocal)
		termDict[lang] = arr
	}
	fmt.Fprintf(os.Stderr, "  Loaded term dictionary: %d languages\n", len(termDict))
}

// ── Feature names (for ablation reporting) ──────────────────────────────

var featureNames = [numFeatures]string{
	// Kept from ablation (16 features)
	"excl_density", "opening_frac", "closing_frac", "quote_density",
	"dash_density", "upper_ratio", "paragraph_count", "sentence_count",
	"line_count", "comma_density", "colon_density", "semi_density",
	"uniqueness_ratio", "first_sent_frac", "last_sent_frac", "vowel_density",
	// Structural semantic features (3 features)
	"excl_arc_end", "sent_len_cv", "line_opening_diversity",
	// Term dictionary semantic features (12 features)
	"sem_healing", "sem_children", "sem_unity", "sem_morning",
	"sem_burial", "sem_marriage", "sem_worship", "sem_teaching",
	"sem_fasting", "sem_blessed", "sem_tests", "sem_steadfast",
}

// ── Feature weights (emphasize best discriminators) ─────────────────────

var featureWeights = [numFeatures]float64{
	// excl_density, opening_frac, closing_frac, quote_density, dash_density, upper_ratio
	1.0, 1.0, 1.0, 1.0, 1.0, 1.0,
	// paragraph_count, sentence_count, line_count, comma_density, colon_density, semi_density
	1.0, 1.0, 1.0, 1.0, 1.0, 1.0,
	// uniqueness_ratio, first_sent_frac, last_sent_frac, vowel_density
	1.0, 1.0, 1.0, 1.0,
	// structural semantic: excl_arc_end, sent_len_cv, line_opening_diversity
	1.0, 1.0, 0.0, // line_opening_diversity disabled per ablation
	// term dictionary semantic (12)
	0.2, 0.2, 0.2, 0.2, 0.2, 0.2, 0.2, 0.2, 0.2, 0.2, 0.2, 0.2,
}

// ── Data access ─────────────────────────────────────────────────────────

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
	return rows[1:]
}

// ── Fingerprint computation (copied from fingerprint.go) ────────────────

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

func computeFingerprint(text, lang string) fingerprint {
	var fp fingerprint
	if len(text) == 0 {
		return fp
	}

	runes := []rune(text)
	totalChars := float64(len(runes))

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

	sentCount := 0
	for _, r := range runes {
		if sentenceEnders[r] {
			sentCount++
		}
	}
	if sentCount == 0 {
		sentCount = 1
	}

	lines := strings.Split(text, "\n")
	var nonEmptyLines []string
	for _, l := range lines {
		if strings.TrimSpace(l) != "" {
			nonEmptyLines = append(nonEmptyLines, strings.TrimSpace(l))
		}
	}
	lineCount := len(nonEmptyLines)
	if lineCount == 0 {
		lineCount = 1
		nonEmptyLines = []string{text}
	}

	paraLens := make([]float64, len(paragraphs))
	for i, p := range paragraphs {
		paraLens[i] = float64(len([]rune(p)))
	}

	fields := strings.Fields(text)
	wordCount := float64(len(fields))
	if wordCount == 0 {
		wordCount = 1
	}

	wordFreq := map[string]int{}
	for _, w := range fields {
		wordFreq[strings.ToLower(w)]++
	}

	uniqueWords := float64(len(wordFreq))

	var excl, comma, colon, semi, quote, dash int
	var exclPositions []int // track positions for emotional arc
	for i, r := range runes {
		switch r {
		case '!':
			excl++
			exclPositions = append(exclPositions, i)
		case ',', '\u060C':
			comma++
		case ':':
			colon++
		case ';', '\u061B':
			semi++
		case '"', '\'', '\u201C', '\u201D', '\u2018', '\u2019', '\u00AB', '\u00BB':
			quote++
		case '-', '\u2013', '\u2014':
			dash++
		}
	}

	var vowelCnt, upperCnt int
	for _, r := range runes {
		if vowels[r] {
			vowelCnt++
		}
		if unicode.IsUpper(r) {
			upperCnt++
		}
	}

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

	// ── New: Emotional arc (exclamation distribution) ──
	exclArcEnd := 0.0
	if excl > 0 {
		thirdPoint := int(totalChars * 2.0 / 3.0)
		lastThirdExcl := 0
		for _, pos := range exclPositions {
			if pos >= thirdPoint {
				lastThirdExcl++
			}
		}
		exclArcEnd = float64(lastThirdExcl) / float64(excl)
	}

	// ── New: Sentence length variance (coefficient of variation) ──
	sentLenCV := 0.0
	{
		// Split by sentence enders
		var sentLens []float64
		curLen := 0
		for _, r := range runes {
			curLen++
			if sentenceEnders[r] {
				sentLens = append(sentLens, float64(curLen))
				curLen = 0
			}
		}
		if curLen > 0 {
			sentLens = append(sentLens, float64(curLen))
		}
		if len(sentLens) > 1 {
			mean := 0.0
			for _, sl := range sentLens {
				mean += sl
			}
			mean /= float64(len(sentLens))
			if mean > 0 {
				varSum := 0.0
				for _, sl := range sentLens {
					d := sl - mean
					varSum += d * d
				}
				sentLenCV = math.Sqrt(varSum/float64(len(sentLens))) / mean
			}
		}
	}

	// ── New: Line opening diversity ──
	uniqueLineStarts := map[rune]bool{}
	for _, line := range nonEmptyLines {
		rr := []rune(line)
		if len(rr) > 0 {
			uniqueLineStarts[rr[0]] = true
		}
	}
	lineOpenDiv := float64(len(uniqueLineStarts)) / float64(lineCount)

	// ── Assign features ──
	// Kept features (16)
	fp[0] = float64(excl) / totalChars                 // excl_density
	fp[1] = paraLens[0] / totalChars                   // opening_frac
	fp[2] = paraLens[len(paraLens)-1] / totalChars     // closing_frac
	fp[3] = float64(quote) / totalChars                // quote_density
	fp[4] = float64(dash) / totalChars                 // dash_density
	fp[5] = float64(upperCnt) / totalChars             // upper_ratio
	fp[6] = paraCount                                  // paragraph_count
	fp[7] = float64(sentCount)                         // sentence_count
	fp[8] = float64(lineCount)                         // line_count
	fp[9] = float64(comma) / totalChars                // comma_density
	fp[10] = float64(colon) / totalChars               // colon_density
	fp[11] = float64(semi) / totalChars                // semi_density
	fp[12] = uniqueWords / wordCount                   // uniqueness_ratio
	fp[13] = float64(firstSentLen) / totalChars        // first_sent_frac
	fp[14] = float64(lastSentLen) / totalChars         // last_sent_frac
	fp[15] = float64(vowelCnt) / totalChars            // vowel_density
	// Structural semantic features (3) — ablation-validated
	fp[16] = exclArcEnd                                // excl_arc_end
	fp[17] = sentLenCV                                 // sent_len_cv
	fp[18] = lineOpenDiv                               // line_opening_diversity

	// Term dictionary semantic features (12)
	if termDict != nil {
		langTerms, ok := termDict[lang]
		if ok {
			lowerText := strings.ToLower(text)
			for catIdx := 0; catIdx < 12; catIdx++ {
				count := 0
				for _, term := range langTerms[catIdx] {
					// Count all occurrences of term as substring
					idx := 0
					for {
						pos := strings.Index(lowerText[idx:], term)
						if pos < 0 {
							break
						}
						count++
						idx += pos + len(term)
					}
				}
				// Use log-scaled density to reduce impact of repeated terms
				if count > 0 {
					fp[19+catIdx] = math.Log1p(float64(count)) / wordCount
				}
			}
		}
	}

	return fp
}

// ── Z-score normalization ───────────────────────────────────────────────

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

// ── Similarity ──────────────────────────────────────────────────────────

func cosineSim(a, b fingerprint) float64 {
	dot, normA, normB := 0.0, 0.0, 0.0
	for d := 0; d < numFeatures; d++ {
		wa := a[d] * featureWeights[d]
		wb := b[d] * featureWeights[d]
		dot += wa * wb
		normA += wa * wa
		normB += wb * wb
	}
	if normA < 1e-12 || normB < 1e-12 {
		return 0
	}
	return dot / (math.Sqrt(normA) * math.Sqrt(normB))
}

// cosineSimMask is like cosineSim but zeroes out the feature at index skip (-1 = none).
func cosineSimMask(a, b fingerprint, skip int) float64 {
	dot, normA, normB := 0.0, 0.0, 0.0
	for d := 0; d < numFeatures; d++ {
		if d == skip {
			continue
		}
		wa := a[d] * featureWeights[d]
		wb := b[d] * featureWeights[d]
		dot += wa * wb
		normA += wa * wa
		normB += wb * wb
	}
	if normA < 1e-12 || normB < 1e-12 {
		return 0
	}
	return dot / (math.Sqrt(normA) * math.Sqrt(normB))
}

// ── Data types ──────────────────────────────────────────────────────────

type entry struct {
	phelps   string
	language string
	text     string
	fullLen  int
	fp       fingerprint
}

// ── Main ────────────────────────────────────────────────────────────────

func main() {
	quick := flag.Bool("quick", false, "Quick mode: fewer samples for faster results")
	ablation := flag.Bool("ablation", false, "Run feature ablation (slow)")
	seed := flag.Int64("seed", 42, "Random seed for reproducibility")
	flag.Parse()

	rng := rand.New(rand.NewSource(*seed))

	maxCodes := 200
	maxQueries := 500
	if *quick {
		maxCodes = 50
		maxQueries = 200
	}

	// ── Load term dictionary ──
	loadTermDict()

	// ── Load data ──
	fmt.Fprintln(os.Stderr, "Loading prayer data from Dolt...")
	rows := doltCSV(fmt.Sprintf(`
		SELECT w.phelps, w.language, LEFT(w.text, 3000) as text, LENGTH(w.text) as full_len
		FROM writings w
		INNER JOIN (
			SELECT phelps FROM writings
			WHERE (type IS NULL OR type='prayer')
			AND phelps NOT LIKE 'TMP%%'
			AND source <> 'inventory'
			AND source <> 'llm-translation'
			GROUP BY phelps
			HAVING COUNT(DISTINCT language) >= 5
		) codes ON w.phelps = codes.phelps
		WHERE (w.type IS NULL OR w.type='prayer')
		AND w.source <> 'inventory'
		AND w.source <> 'llm-translation'
		ORDER BY w.phelps, w.language
		LIMIT %d`, 100000))

	if rows == nil {
		fmt.Fprintln(os.Stderr, "No data returned from Dolt")
		os.Exit(1)
	}

	// Parse into entries, grouped by phelps code
	codeEntries := map[string][]entry{}
	for _, r := range rows {
		if len(r) < 4 || r[2] == "" {
			continue
		}
		fl := 0
		fmt.Sscanf(r[3], "%d", &fl)
		e := entry{phelps: r[0], language: r[1], text: r[2], fullLen: fl}
		codeEntries[e.phelps] = append(codeEntries[e.phelps], e)
	}
	fmt.Fprintf(os.Stderr, "  Loaded %d entries across %d phelps codes\n", len(rows), len(codeEntries))

	// Filter to codes with 5+ language entries
	var codes []string
	for code, entries := range codeEntries {
		langs := map[string]bool{}
		for _, e := range entries {
			langs[e.language] = true
		}
		if len(langs) >= 5 {
			codes = append(codes, code)
		}
	}
	sort.Strings(codes)
	fmt.Fprintf(os.Stderr, "  %d codes with 5+ languages\n", len(codes))

	// Sample codes
	if len(codes) > maxCodes {
		rng.Shuffle(len(codes), func(i, j int) { codes[i], codes[j] = codes[j], codes[i] })
		codes = codes[:maxCodes]
		sort.Strings(codes)
	}

	// Collect all entries for sampled codes
	var allEntries []entry
	sampledEntries := map[string][]int{} // code -> indices into allEntries
	for _, code := range codes {
		for _, e := range codeEntries[code] {
			idx := len(allEntries)
			allEntries = append(allEntries, e)
			sampledEntries[code] = append(sampledEntries[code], idx)
		}
	}
	fmt.Fprintf(os.Stderr, "  %d entries from %d sampled codes\n", len(allEntries), len(codes))

	// Compute fingerprints
	fmt.Fprintln(os.Stderr, "Computing fingerprints...")
	rawFPs := make([]fingerprint, len(allEntries))
	for i := range allEntries {
		rawFPs[i] = computeFingerprint(allEntries[i].text, allEntries[i].language)
	}

	// Z-score normalize
	featureStats := computeStats(rawFPs)
	for i := range allEntries {
		allEntries[i].fp = normalize(rawFPs[i], featureStats)
	}

	// ── Test 1: Cross-language Recall@K ──
	fmt.Fprintln(os.Stderr, "Running Test 1: Cross-language Recall@K...")

	type query struct {
		idx    int    // index into allEntries
		code   string // phelps code
		bucket int    // 0=short, 1=medium, 2=long
	}

	var queries []query
	for _, code := range codes {
		indices := sampledEntries[code]
		if len(indices) < 2 {
			continue
		}
		// Pick one entry as query (first language alphabetically for reproducibility)
		qi := indices[0]
		fl := allEntries[qi].fullLen
		bucket := 1
		if fl < 500 {
			bucket = 0
		} else if fl > 2000 {
			bucket = 2
		}
		queries = append(queries, query{idx: qi, code: code, bucket: bucket})
	}

	// Limit queries
	if len(queries) > maxQueries {
		rng.Shuffle(len(queries), func(i, j int) { queries[i], queries[j] = queries[j], queries[i] })
		queries = queries[:maxQueries]
	}

	fmt.Fprintf(os.Stderr, "  %d queries prepared\n", len(queries))

	// For each query, rank ALL other entries by cosine similarity
	recall := map[int]int{1: 0, 5: 0, 10: 0, 50: 0}
	bucketRecall := [3]map[int]int{
		{1: 0, 5: 0, 10: 0, 50: 0},
		{1: 0, 5: 0, 10: 0, 50: 0},
		{1: 0, 5: 0, 10: 0, 50: 0},
	}
	bucketTotal := [3]int{}

	type scored struct {
		idx  int
		sim  float64
		code string
	}

	progress := 0
	for _, q := range queries {
		progress++
		if progress%50 == 0 {
			fmt.Fprintf(os.Stderr, "  query %d/%d\n", progress, len(queries))
		}

		// Rank all OTHER entries (different language from query, or different code)
		var candidates []scored
		for i := range allEntries {
			if i == q.idx {
				continue
			}
			// Skip same language (we want cross-language matching)
			if allEntries[i].language == allEntries[q.idx].language {
				continue
			}
			sim := cosineSim(allEntries[q.idx].fp, allEntries[i].fp)
			candidates = append(candidates, scored{i, sim, allEntries[i].phelps})
		}
		sort.Slice(candidates, func(i, j int) bool {
			return candidates[i].sim > candidates[j].sim
		})

		// Check if any correct match (same code) is in top K
		for _, k := range []int{1, 5, 10, 50} {
			found := false
			limit := k
			if limit > len(candidates) {
				limit = len(candidates)
			}
			for _, c := range candidates[:limit] {
				if c.code == q.code {
					found = true
					break
				}
			}
			if found {
				recall[k]++
				bucketRecall[q.bucket][k]++
			}
		}
		bucketTotal[q.bucket]++
	}

	// ── Test 2: Intra-code vs Inter-code similarity ──
	fmt.Fprintln(os.Stderr, "Running Test 2: Intra-code vs Inter-code similarity...")

	intraSims := []float64{}
	interSims := []float64{}

	for _, code := range codes {
		indices := sampledEntries[code]
		if len(indices) < 3 {
			continue
		}
		// Intra-code: all pairs within code
		for i := 0; i < len(indices); i++ {
			for j := i + 1; j < len(indices); j++ {
				sim := cosineSim(allEntries[indices[i]].fp, allEntries[indices[j]].fp)
				intraSims = append(intraSims, sim)
			}
		}
	}

	// Inter-code: sample random pairs from different codes
	interSampleN := len(intraSims)
	if interSampleN > 10000 {
		interSampleN = 10000
	}
	for n := 0; n < interSampleN; n++ {
		c1 := codes[rng.Intn(len(codes))]
		c2 := codes[rng.Intn(len(codes))]
		if c1 == c2 {
			continue
		}
		i1 := sampledEntries[c1][rng.Intn(len(sampledEntries[c1]))]
		i2 := sampledEntries[c2][rng.Intn(len(sampledEntries[c2]))]
		sim := cosineSim(allEntries[i1].fp, allEntries[i2].fp)
		interSims = append(interSims, sim)
	}

	avgIntra := avg(intraSims)
	avgInter := avg(interSims)

	// ── Test 4: Known mismatch detection ──
	fmt.Fprintln(os.Stderr, "Running Test 4: Known mismatch detection...")
	test4Total, test4Correct := runTest4(allEntries, sampledEntries, featureStats)

	// ── Output results ──
	fmt.Println("=== Fingerprint Vector Test Results ===")
	fmt.Println()
	fmt.Printf("Test 1: Cross-language Recall (N=%d queries across %d codes)\n", len(queries), len(codes))
	for _, k := range []int{1, 5, 10, 50} {
		fmt.Printf("  Recall@%-3d  %5.1f%%\n", k, 100*float64(recall[k])/float64(len(queries)))
	}

	fmt.Println()
	fmt.Println("Test 2: Intra-code vs Inter-code Similarity")
	fmt.Printf("  Avg intra-code similarity: %.3f (N=%d pairs)\n", avgIntra, len(intraSims))
	fmt.Printf("  Avg inter-code similarity: %.3f (N=%d pairs)\n", avgInter, len(interSims))
	if avgInter > 0.001 {
		fmt.Printf("  Separation ratio: %.1fx\n", avgIntra/avgInter)
	} else {
		fmt.Printf("  Separation ratio: inf (inter ~ 0)\n")
	}

	fmt.Println()
	bucketNames := [3]string{"Short (<500)", "Medium (500-2k)", "Long (>2k)"}
	fmt.Println("Test 3: By Length Bucket")
	for b := 0; b < 3; b++ {
		if bucketTotal[b] == 0 {
			fmt.Printf("  %-18s N=0\n", bucketNames[b]+":")
			continue
		}
		r1 := 100 * float64(bucketRecall[b][1]) / float64(bucketTotal[b])
		r10 := 100 * float64(bucketRecall[b][10]) / float64(bucketTotal[b])
		r50 := 100 * float64(bucketRecall[b][50]) / float64(bucketTotal[b])
		fmt.Printf("  %-18s Recall@1=%5.1f%%, @10=%5.1f%%, @50=%5.1f%% (N=%d)\n",
			bucketNames[b]+":", r1, r10, r50, bucketTotal[b])
	}

	fmt.Println()
	if test4Total > 0 {
		fmt.Printf("Test 4: Known Mismatch Detection (N=%d)\n", test4Total)
		fmt.Printf("  Correct identification: %.1f%%\n", 100*float64(test4Correct)/float64(test4Total))
	} else {
		fmt.Println("Test 4: Known Mismatch Detection — skipped (no matching data)")
	}

	// ── Test 5: Feature ablation ──
	if *ablation {
		fmt.Fprintln(os.Stderr, "Running Test 5: Feature ablation...")
		fmt.Println()
		fmt.Println("=== Feature Importance (ablation) ===")

		baseRecall1 := float64(recall[1]) / float64(len(queries))

		type ablResult struct {
			feature string
			recall1 float64
			drop    float64
		}
		var ablResults []ablResult

		for skip := 0; skip < numFeatures; skip++ {
			fmt.Fprintf(os.Stderr, "  ablation feature %d/%d: %s\n", skip+1, numFeatures, featureNames[skip])
			hits := 0
			for _, q := range queries {
				var candidates []scored
				for i := range allEntries {
					if i == q.idx || allEntries[i].language == allEntries[q.idx].language {
						continue
					}
					sim := cosineSimMask(allEntries[q.idx].fp, allEntries[i].fp, skip)
					candidates = append(candidates, scored{i, sim, allEntries[i].phelps})
				}
				sort.Slice(candidates, func(i, j int) bool {
					return candidates[i].sim > candidates[j].sim
				})
				if len(candidates) > 0 && candidates[0].code == q.code {
					hits++
				}
			}
			r := float64(hits) / float64(len(queries))
			ablResults = append(ablResults, ablResult{
				feature: featureNames[skip],
				recall1: r,
				drop:    baseRecall1 - r,
			})
		}

		// Sort by drop (most impactful first)
		sort.Slice(ablResults, func(i, j int) bool {
			return ablResults[i].drop > ablResults[j].drop
		})

		for _, ar := range ablResults {
			sign := ""
			if ar.drop >= 0 {
				sign = "-"
			} else {
				sign = "+"
			}
			fmt.Printf("  Removing %-22s Recall@1 %.1f%% → %.1f%% (%s%.1f%%)\n",
				"'"+ar.feature+"':", 100*baseRecall1, 100*ar.recall1,
				sign, 100*math.Abs(ar.drop))
		}
	}
}

// ── Test 4 implementation ───────────────────────────────────────────────

func runTest4(allEntries []entry, sampledEntries map[string][]int, featureStats [numFeatures]stats) (total, correct int) {
	// Read known-wrong file
	f, err := os.Open("/tmp/claude/llm_wrong.txt")
	if err != nil {
		fmt.Fprintf(os.Stderr, "  Cannot open /tmp/claude/llm_wrong.txt: %v\n", err)
		return 0, 0
	}
	defer f.Close()

	// Collect all unique codes from the wrong file
	var wrongEntries []struct {
		llmCode, bestMatch string
	}
	allCodes := map[string]bool{}

	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) < 5 || fields[4] != "WRONG" {
			continue
		}
		wrongEntries = append(wrongEntries, struct{ llmCode, bestMatch string }{fields[0], fields[1]})
		allCodes[fields[0]] = true
		allCodes[fields[1]] = true
	}

	if len(wrongEntries) == 0 {
		return 0, 0
	}

	// Load ALL texts (any language) for these codes
	codeList := make([]string, 0, len(allCodes))
	for c := range allCodes {
		codeList = append(codeList, "'"+strings.ReplaceAll(c, "'", "''")+"'")
	}
	refRows := doltCSV(fmt.Sprintf(`SELECT phelps, LEFT(text, 3000) as text FROM writings
		WHERE phelps IN (%s)
		AND source <> 'llm-translation'
		AND (type IS NULL OR type='prayer')`, strings.Join(codeList, ",")))

	// Group texts by code, compute fingerprints
	type refFP struct {
		code string
		fp   fingerprint
	}
	codeTexts := map[string][]string{}
	for _, r := range refRows {
		if len(r) >= 2 && r[1] != "" {
			codeTexts[r[0]] = append(codeTexts[r[0]], r[1])
		}
	}

	// Build all fingerprints for normalization
	var rawFPs []fingerprint
	codeFPs := map[string][]fingerprint{}
	for code, texts := range codeTexts {
		for _, t := range texts {
			fp := computeFingerprint(t, "")
			rawFPs = append(rawFPs, fp)
			codeFPs[code] = append(codeFPs[code], fp)
		}
	}

	// Also load LLM texts
	llmRows := doltCSV(`SELECT phelps, LEFT(text, 3000) as text FROM writings
		WHERE source='llm-translation' AND language='en'`)
	llmByCode := map[string][]string{}
	for _, r := range llmRows {
		if len(r) >= 2 && r[1] != "" {
			llmByCode[r[0]] = append(llmByCode[r[0]], r[1])
			rawFPs = append(rawFPs, computeFingerprint(r[1], "en"))
		}
	}

	localStats := computeStats(rawFPs)

	// Normalize code fingerprints
	normCodeFPs := map[string][]fingerprint{}
	for code, fps := range codeFPs {
		for _, fp := range fps {
			normCodeFPs[code] = append(normCodeFPs[code], normalize(fp, localStats))
		}
	}

	// Average similarity of a query FP to all FPs under a code
	avgSimToCode := func(query fingerprint, code string) (float64, bool) {
		fps, ok := normCodeFPs[code]
		if !ok || len(fps) == 0 {
			return 0, false
		}
		sum := 0.0
		for _, fp := range fps {
			sum += cosineSim(query, fp)
		}
		return sum / float64(len(fps)), true
	}

	// For each wrong entry, find the LLM text and compare
	for _, we := range wrongEntries {
		// Find LLM text — may be under bestMatch (reassigned) or llmCode (original)
		var llmText string
		for _, code := range []string{we.bestMatch, we.llmCode} {
			if texts, ok := llmByCode[code]; ok && len(texts) > 0 {
				llmText = texts[0]
				break
			}
		}
		if llmText == "" {
			continue
		}

		llmFP := normalize(computeFingerprint(llmText, "en"), localStats)

		bestSim, foundBest := avgSimToCode(llmFP, we.bestMatch)
		llmSim, foundLLM := avgSimToCode(llmFP, we.llmCode)

		if !foundBest || !foundLLM {
			continue
		}

		total++
		if bestSim > llmSim {
			correct++
		}
	}

	return total, correct
}

// ── Helpers ─────────────────────────────────────────────────────────────

func avg(vals []float64) float64 {
	if len(vals) == 0 {
		return 0
	}
	sum := 0.0
	for _, v := range vals {
		sum += v
	}
	return sum / float64(len(vals))
}
