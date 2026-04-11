// build_dict.go — Bootstrap a per-language theological term dictionary
// from known prayers that contain distinctive terms.
//
// Strategy: Use "anchor prayers" where we know which English theological
// term appears. For each language translation, find the word that's most
// distinctive (appears in that prayer but rarely in others for that language).
//
// Output: CSV dictionary for loading into doltlite.
//
// Usage:
//   go run scripts/build_dict.go > /tmp/claude/term_dict.csv
package main

import (
	"encoding/csv"
	"fmt"
	"math"
	"os"
	"os/exec"
	"regexp"
	"sort"
	"strings"
	"unicode"
)

const dbPath = "/home/joop/bahaiwritings"

// Anchor prayers: phelps code → English term they're known for
// These are prayers with very distinctive themes
var anchors = map[string][]string{
	// Healing
	"BB00623":       {"healing", "healer"},    // Remover of Difficulties (short)
	"BH01313NAM":    {"healing", "name"},       // Thy Name is my healing
	"BH00870":       {"healing"},               // Long Healing Prayer

	// Children
	"AB10714":       {"children"},              // O God, guide...children
	"AB04004":       {"children"},              // O Lord...children

	// Unity
	"BH10505":       {"unity"},                 // O my God, O my God, unite
	"AB02000DIS":    {"unity"},                 // Discourse on unity

	// Marriage
	"AB03461MAR":    {"marriage"},              // Marriage prayer

	// Deceased / departed
	"AB11094":       {"deceased", "departed"},  // O my God, departed
	"BH09085":       {"burial"},                // Burial prayer

	// Morning
	"BH00009SHE":    {"morning", "risen"},      // I have risen this morning
	"BH00009GRA":    {"morning", "grace"},      // Morning prayer (grace)

	// Forgiveness / mercy
	"BH00071":       {"forgiveness"},           // I beg forgiveness
	"BH00513":       {"mercy", "refresh"},      // O God, refresh and gladden

	// Fasting
	"BH00154FIR":    {"fasting", "fast"},       // First day of fast

	// Teaching / steadfastness
	"AB00218SOU":    {"teaching", "soldiers"},  // Army of light
	"AB10703RAD":    {"youth"},                 // Youth prayer

	// Protection
	"BH00074BLE":    {"blessed", "spot"},       // Blessed is the spot

	// Praise / glorification
	"BH11209":       {"witness", "worship"},    // Short obligatory (I bear witness)
	"BH03447":       {"worship", "face"},       // Medium obligatory

	// Firmness / tests
	"AB12789":       {"steadfast", "firm"},     // Steadfastness prayer
	"BH08600TES":    {"tests", "trials"},       // Tests prayer
}

func doltCSV(query string) [][]string {
	cmd := exec.Command("dolt", "sql", "-r", "csv", "-q", query)
	cmd.Dir = dbPath
	out, err := cmd.Output()
	if err != nil {
		fmt.Fprintf(os.Stderr, "dolt error: %v\n", err)
		return nil
	}
	r := csv.NewReader(strings.NewReader(string(out)))
	rows, _ := r.ReadAll()
	if len(rows) > 1 {
		return rows[1:] // skip header
	}
	return nil
}

var wordRe = regexp.MustCompile(`[\p{L}\p{M}]{2,}`)

func tokenize(text string) []string {
	return wordRe.FindAllString(strings.ToLower(text), -1)
}

func main() {
	fmt.Fprintln(os.Stderr, "Building per-language term dictionary...")

	// Step 1: For each language, build corpus word frequencies
	fmt.Fprintln(os.Stderr, "Loading corpus word frequencies per language...")

	// Get top languages (those with 20+ prayers for good frequency stats)
	langRows := doltCSV(`SELECT language, COUNT(*) as c FROM writings
		WHERE (type IS NULL OR type='prayer') AND source <> 'inventory' AND source <> 'llm-translation'
		GROUP BY language HAVING COUNT(*) >= 20 ORDER BY c DESC`)

	var topLangs []string
	for _, r := range langRows {
		topLangs = append(topLangs, r[0])
	}
	fmt.Fprintf(os.Stderr, "  %d languages with 20+ prayers\n", len(topLangs))

	// For each language, load all prayer texts and build word frequency
	type langCorpus struct {
		wordFreq map[string]int // word → document frequency (how many prayers contain it)
		docCount int
	}
	corpora := map[string]*langCorpus{}

	for i, lang := range topLangs {
		if i%50 == 0 {
			fmt.Fprintf(os.Stderr, "  Loading %s (%d/%d)...\n", lang, i, len(topLangs))
		}
		rows := doltCSV(fmt.Sprintf(`SELECT LEFT(text, 2000) as text FROM writings
			WHERE language='%s' AND (type IS NULL OR type='prayer')
			AND source <> 'inventory' AND source <> 'llm-translation'`, lang))

		corpus := &langCorpus{wordFreq: map[string]int{}, docCount: len(rows)}
		for _, r := range rows {
			seen := map[string]bool{}
			for _, w := range tokenize(r[0]) {
				if !seen[w] {
					corpus.wordFreq[w]++
					seen[w] = true
				}
			}
		}
		corpora[lang] = corpus
	}

	// Step 2: For each anchor prayer × language, find the most distinctive word
	fmt.Fprintln(os.Stderr, "Extracting terms from anchor prayers...")

	type dictEntry struct {
		Language string
		TermEN   string
		TermLocal string
		Score    float64
		Source   string // phelps code it came from
	}
	var entries []dictEntry

	for phelps, enTerms := range anchors {
		// Get translations of this prayer
		rows := doltCSV(fmt.Sprintf(`SELECT language, LEFT(text, 2000) as text FROM writings
			WHERE phelps='%s' AND (type IS NULL OR type='prayer')
			AND source <> 'inventory' AND source <> 'llm-translation'`, phelps))

		for _, r := range rows {
			lang := r[0]
			text := r[1]
			corpus, ok := corpora[lang]
			if !ok {
				continue
			}

			words := tokenize(text)
			if len(words) == 0 {
				continue
			}

			// Count word frequency in THIS prayer
			localFreq := map[string]int{}
			for _, w := range words {
				localFreq[w]++
			}

			// Score each word: high local frequency + low corpus document frequency = distinctive
			type scored struct {
				word  string
				score float64
			}
			var candidates []scored
			for w, lf := range localFreq {
				if len([]rune(w)) < 3 {
					continue
				}
				df := corpus.wordFreq[w]
				if df == 0 {
					df = 1
				}
				// TF-IDF-like score: local freq × inverse document freq
				idf := math.Log(float64(corpus.docCount) / float64(df))
				tf := float64(lf) / float64(len(words))
				score := tf * idf

				// Boost if word starts with uppercase (theological terms often capitalized)
				for _, c := range w {
					if unicode.IsUpper(c) {
						score *= 1.5
					}
					break
				}

				candidates = append(candidates, scored{w, score})
			}
			sort.Slice(candidates, func(i, j int) bool {
				return candidates[i].score > candidates[j].score
			})

			// Take top 3 most distinctive words as candidate terms
			for k := 0; k < 3 && k < len(candidates); k++ {
				c := candidates[k]
				if c.score < 0.01 {
					continue
				}
				for _, enTerm := range enTerms {
					entries = append(entries, dictEntry{
						Language:  lang,
						TermEN:    enTerm,
						TermLocal: c.word,
						Score:     c.score,
						Source:    phelps,
					})
				}
			}
		}
	}

	// Step 3: Deduplicate — keep highest-scoring term per language+enTerm
	fmt.Fprintln(os.Stderr, "Deduplicating...")
	type key struct {
		lang, term string
	}
	best := map[key]dictEntry{}
	for _, e := range entries {
		k := key{e.Language, e.TermEN}
		if existing, ok := best[k]; !ok || e.Score > existing.Score {
			best[k] = e
		}
	}

	// Sort and output
	var final []dictEntry
	for _, e := range best {
		final = append(final, e)
	}
	sort.Slice(final, func(i, j int) bool {
		if final[i].Language != final[j].Language {
			return final[i].Language < final[j].Language
		}
		return final[i].TermEN < final[j].TermEN
	})

	// Output CSV
	w := csv.NewWriter(os.Stdout)
	w.Write([]string{"language", "term_en", "term_local", "score", "source"})
	for _, e := range final {
		w.Write([]string{e.Language, e.TermEN, e.TermLocal,
			fmt.Sprintf("%.4f", e.Score), e.Source})
	}
	w.Flush()

	fmt.Fprintf(os.Stderr, "Done! %d dictionary entries across %d languages\n",
		len(final), len(topLangs))

	// Print sample
	fmt.Fprintln(os.Stderr, "\nSample entries:")
	shown := map[string]int{}
	for _, e := range final {
		if shown[e.Language] < 2 {
			fmt.Fprintf(os.Stderr, "  %s: %s = %s (from %s, score=%.3f)\n",
				e.Language, e.TermEN, e.TermLocal, e.Source, e.Score)
			shown[e.Language]++
		}
	}
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
