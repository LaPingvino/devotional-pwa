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
	"regexp"
	"sort"
	"strings"

	_ "github.com/go-sql-driver/mysql"
)

// Theme dimensions — each is a list of distinctive keywords for that theme.
// A prayer's vector is its score per dimension (count of matching keywords / total keywords).
var themes = map[string][]string{
	"healing": {"heal", "healer", "healing", "sick", "sickness", "disease", "afflict",
		"remedy", "cure", "physician", "recovery", "ailing", "infirm", "sufficer"},
	"children": {"child", "children", "infant", "babe", "little", "tender", "seedling",
		"sapling", "nursed", "cradle", "maidservant", "youth", "young", "educate"},
	"unity": {"unity", "unite", "united", "oneness", "harmony", "concord", "fellowship",
		"gathering", "assemblage", "assembly", "meeting", "gathered", "companions"},
	"teaching": {"teach", "teaching", "teacher", "spread", "diffuse", "promulgate",
		"proclaim", "herald", "journey", "travel", "arise", "army", "hosts", "pioneer"},
	"departed": {"departed", "deceased", "dead", "death", "die", "dying", "grave",
		"tomb", "burial", "paradise", "reunion", "forgive", "forgiveness", "mercy"},
	"protection": {"protect", "protection", "shelter", "refuge", "shield", "guard",
		"safeguard", "preserve", "defend", "fortress", "stronghold", "sufficient"},
	"marriage": {"marriage", "married", "marry", "bride", "bridegroom", "wedding",
		"spouse", "husband", "wife", "couple", "two", "nest", "birds", "united"},
	"morning": {"morning", "dawn", "arise", "arisen", "awaken", "awakened", "sleep",
		"slumber", "daybreak", "sunrise", "morn", "risen", "wakefulness"},
	"evening": {"evening", "night", "nightfall", "midnight", "retire", "rest",
		"repose", "slumber", "darkness", "setting"},
	"fasting": {"fast", "fasting", "hunger", "thirst", "abstain", "feast",
		"nourish", "sustain", "intercalary"},
	"covenant": {"covenant", "steadfast", "firm", "firmness", "faithful", "faithfulness",
		"testament", "violation", "violate", "wayward", "allegiance"},
	"tests": {"test", "tests", "trial", "trials", "tribulation", "difficulty",
		"difficulties", "adversity", "affliction", "calamity", "suffering", "patience"},
	"praise": {"praise", "praised", "glorified", "glorify", "glory", "exalted",
		"adoration", "magnify", "lauded", "thanksgiving", "thankful"},
	"forgiveness": {"forgive", "forgiveness", "pardon", "repent", "repentance",
		"sin", "sins", "transgression", "err", "erred", "shortcoming"},
	"detachment": {"detach", "detachment", "renounce", "worldly", "material",
		"desire", "passion", "vain", "idle", "fancy", "wealth", "poverty"},
	"obligatory": {"obligatory", "noon", "recited", "recite", "witness", "testify",
		"worship", "powerlessness", "might", "created", "know"},
	"nearness": {"near", "nearness", "close", "intimate", "presence", "face",
		"countenance", "behold", "attain", "court", "threshold", "door"},
	"spiritual": {"spirit", "spiritual", "soul", "heart", "heavenly", "celestial",
		"divine", "holy", "sacred", "pure", "illumine", "radiance", "light"},
	"service": {"serve", "service", "servant", "handmaid", "maidservant",
		"minister", "assist", "aid", "help", "succor", "support"},
	"knowledge": {"knowledge", "wisdom", "understanding", "insight", "certitude",
		"truth", "recognition", "discover", "reveal", "mystery", "secret"},
	"family": {"family", "mother", "father", "parent", "parents", "son", "daughter",
		"household", "home", "offspring", "generation"},
	"prosperity": {"prosper", "prosperity", "progress", "advance", "flourish",
		"success", "victory", "triumph", "conquer", "prevail"},
}

var dimNames []string

func init() {
	dimNames = make([]string, 0, len(themes))
	for k := range themes {
		dimNames = append(dimNames, k)
	}
	sort.Strings(dimNames)
}

var wordRe = regexp.MustCompile(`[a-zA-Z]{3,}`)

func tokenize(text string) []string {
	matches := wordRe.FindAllString(strings.ToLower(text), -1)
	return matches
}

func vectorize(text string) []float64 {
	words := tokenize(text)
	if len(words) == 0 {
		return make([]float64, len(dimNames))
	}
	// Build word set for fast lookup
	wordSet := make(map[string]int)
	for _, w := range words {
		wordSet[w]++
	}
	vec := make([]float64, len(dimNames))
	for i, dim := range dimNames {
		keywords := themes[dim]
		score := 0.0
		for _, kw := range keywords {
			if c, ok := wordSet[kw]; ok {
				score += float64(c)
			}
			// Also check partial matches (e.g., "healed" matches "heal")
			for w, c := range wordSet {
				if len(w) > len(kw) && strings.HasPrefix(w, kw) {
					score += float64(c) * 0.5
				}
			}
		}
		vec[i] = score / float64(len(words)) // normalize by text length
	}
	return vec
}

func cosine(a, b []float64) float64 {
	var dot, magA, magB float64
	for i := range a {
		dot += a[i] * b[i]
		magA += a[i] * a[i]
		magB += b[i] * b[i]
	}
	if magA == 0 || magB == 0 {
		return 0
	}
	return dot / (math.Sqrt(magA) * math.Sqrt(magB))
}

type refEntry struct {
	phelps string
	vec    []float64
	text   string // first 100 chars for display
}

type llmEntry struct {
	phelps  string
	version string
	text    string
	vec     []float64
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
	flag.Parse()

	// Connect via dolt sql-server or use CLI
	// For simplicity, use dolt sql CLI via exec
	// Actually, let's just use the mysql driver if a server is running,
	// otherwise fall back to reading from CLI

	log.Printf("Loading data from %s...", *doltDir)

	db, err := sql.Open("mysql", "root@tcp(127.0.0.1:3306)/bahaiwritings")
	if err != nil {
		log.Fatalf("Cannot connect to dolt: %v", err)
	}
	defer db.Close()

	// Test connection - if it fails, we need dolt sql-server
	if err := db.Ping(); err != nil {
		log.Fatalf("Cannot ping dolt server. Start one with: cd %s && dolt sql-server &\nError: %v", *doltDir, err)
	}

	// Load reference: inventory first lines + English prayers
	log.Println("Loading reference prayers...")
	refs := make(map[string]*refEntry)

	// Inventory
	rows, err := db.Query(`SELECT PIN, ` + "`First line (translated)`" + ` FROM inventory
		WHERE ` + "`First line (translated)`" + ` IS NOT NULL AND ` + "`First line (translated)`" + ` <> ''`)
	if err != nil {
		log.Fatal(err)
	}
	for rows.Next() {
		var pin, text string
		rows.Scan(&pin, &text)
		refs[pin] = &refEntry{phelps: pin, vec: vectorize(text), text: text[:min(len(text), 80)]}
	}
	rows.Close()

	// English prayers from bahaiprayers.net
	rows, err = db.Query(`SELECT phelps, LEFT(text, 2000) FROM writings
		WHERE language='en' AND source='bahaiprayers.net' AND (type IS NULL OR type='prayer')
		AND phelps NOT LIKE 'TMP%'`)
	if err != nil {
		log.Fatal(err)
	}
	for rows.Next() {
		var phelps, text string
		rows.Scan(&phelps, &text)
		vec := vectorize(text)
		if existing, ok := refs[phelps]; !ok || magnitude(vec) > magnitude(existing.vec) {
			refs[phelps] = &refEntry{phelps: phelps, vec: vec, text: text[:min(len(text), 80)]}
		}
	}
	rows.Close()

	log.Printf("  %d reference entries", len(refs))

	// Load LLM entries
	log.Println("Loading LLM entries...")
	rows, err = db.Query(`SELECT phelps, version, LEFT(text, 2000) FROM writings
		WHERE source='llm-translation' AND language='en'`)
	if err != nil {
		log.Fatal(err)
	}
	var llmEntries []llmEntry
	for rows.Next() {
		var e llmEntry
		rows.Scan(&e.phelps, &e.version, &e.text)
		e.vec = vectorize(e.text)
		llmEntries = append(llmEntries, e)
	}
	rows.Close()
	log.Printf("  %d LLM entries", len(llmEntries))

	// Match
	var matches []match
	refList := make([]refEntry, 0, len(refs))
	for _, r := range refs {
		refList = append(refList, *r)
	}

	for _, llm := range llmEntries {
		type scored struct {
			code  string
			score float64
		}
		var scores []scored
		for _, r := range refList {
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
		fmt.Println("SET FOREIGN_KEY_CHECKS=0;")
		for _, m := range matches {
			if m.status == "WRONG" {
				// Find which languages have this wrong code (non-English, non-LLM)
				fmt.Printf("-- %s → %s (score=%.2f)\n", m.llm.phelps, m.bestCode, m.bestScore)
				fmt.Printf("-- LLM: %s\n", strings.ReplaceAll(m.llm.text[:min(len(m.llm.text), 80)], "\n", " "))
				fmt.Printf("UPDATE writings SET phelps='%s' WHERE phelps='%s' AND language<>'en' AND source<>'inventory';\n",
					m.bestCode, m.llm.phelps)
				fmt.Printf("DELETE FROM writings WHERE version='%s';\n\n", m.llm.version)
			} else if m.status == "OK" {
				// Check if real English exists — if so, delete LLM
				fmt.Printf("-- %s OK, delete LLM if real English exists\n", m.llm.phelps)
				fmt.Printf("-- DELETE FROM writings WHERE version='%s'; -- only if net English exists\n\n", m.llm.version)
			}
		}
		fmt.Println("SET FOREIGN_KEY_CHECKS=1;")
	}
}

func magnitude(v []float64) float64 {
	sum := 0.0
	for _, x := range v {
		sum += x * x
	}
	return math.Sqrt(sum)
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
