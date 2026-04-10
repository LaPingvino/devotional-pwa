// add_en_translations.go — Generate LLM English translations for prayers lacking English entries.
//
// Usage:
//   go run add_en_translations.go [--limit N] [--dry-run] [--min-langs N]
//
// For each phelps code that has no English entry but has ≥ min-langs other-language entries,
// pick the best available source text, translate with Gemini, and insert as language='en',
// source='llm-translation', is_verified=0.

package main

import (
	"bytes"
	"encoding/csv"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"strings"
)

const (
	llmGeminiModel = "gemini-2.5-flash-lite"
	llmSource      = "llm-translation"
	llmDoltDir     = "/home/joop/bahaiwritings"
)

// Preferred source languages for translation (most reliable to English)
var preferredSrcLangs = []string{"de", "fr", "pt", "es", "it", "nl", "sv", "no", "da", "id", "sw", "zh-Hant", "zh-Hans", "ja", "ko", "ru", "ar", "fa", "tr", "hi"}

func llmQuery(query string) []map[string]string {
	cmd := exec.Command("dolt", "sql", "-q", query, "--result-format", "csv")
	cmd.Dir = llmDoltDir
	out, err := cmd.Output()
	if err != nil {
		fmt.Fprintf(os.Stderr, "[dolt error] %v\n", err)
		return nil
	}
	r := csv.NewReader(bytes.NewReader(out))
	rows, _ := r.ReadAll()
	if len(rows) < 2 {
		return nil
	}
	headers := rows[0]
	var result []map[string]string
	for _, row := range rows[1:] {
		m := map[string]string{}
		for i, h := range headers {
			if i < len(row) {
				m[h] = row[i]
			}
		}
		result = append(result, m)
	}
	return result
}

func llmExec(sql string) {
	cmd := exec.Command("dolt", "sql", "-q", sql)
	cmd.Dir = llmDoltDir
	out, err := cmd.CombinedOutput()
	if err != nil {
		fmt.Fprintf(os.Stderr, "[dolt exec error] %v: %s\n", err, string(out))
	}
}

func llmSqlEsc(s string) string {
	return strings.NewReplacer(`\`, `\\`, `'`, `''`).Replace(s)
}

func geminiTranslate(srcLang, srcText, phelps string) string {
	prompt := fmt.Sprintf(`You are a Bahá'í prayer translator. Translate the following prayer from %s into English.

Output your response in this exact format:
<translation>
[the translated prayer text here, preserving paragraph breaks]
</translation>
<notes>
[optional: any notes about translation challenges, uncertain words, or comments]
</notes>

If you cannot translate, still use this format and explain in <notes>.

%s`, srcLang, srcText)

	cmd := exec.Command("gemini", "-m", llmGeminiModel)
	cmd.Stdin = strings.NewReader(prompt)
	out, err := cmd.Output()
	if err != nil {
		fmt.Fprintf(os.Stderr, "[gemini error] %v\n", err)
		return ""
	}
	raw := string(out)

	// Extract content between <translation> tags
	start := strings.Index(raw, "<translation>")
	end := strings.Index(raw, "</translation>")
	if start >= 0 && end > start {
		translation := strings.TrimSpace(raw[start+len("<translation>") : end])
		// Log any notes
		notesStart := strings.Index(raw, "<notes>")
		notesEnd := strings.Index(raw, "</notes>")
		if notesStart >= 0 && notesEnd > notesStart {
			notes := strings.TrimSpace(raw[notesStart+len("<notes>") : notesEnd])
			if notes != "" {
				fmt.Fprintf(os.Stderr, "  NOTE [%s]: %s\n", phelps, notes[:min(100, len(notes))])
			}
		}
		return translation
	}
	// Fallback: return trimmed raw output if no tags found
	return strings.TrimSpace(raw)
}

func main() {
	limit := flag.Int("limit", 100, "Max number of codes to process")
	dryRun := flag.Bool("dry-run", false, "Print SQL but don't apply")
	minLangs := flag.Int("min-langs", 5, "Min language count to process a code")
	codesFile := flag.String("codes-file", "", "File with one phelps code per line to retry specifically")
	flag.Parse()

	var rows []map[string]string

	if *codesFile != "" {
		// Read specific phelps codes from file
		data, err := os.ReadFile(*codesFile)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Cannot read codes file: %v\n", err)
			os.Exit(1)
		}
		for _, line := range strings.Split(strings.TrimSpace(string(data)), "\n") {
			code := strings.TrimSpace(line)
			if code != "" {
				rows = append(rows, map[string]string{"phelps": code, "lang_count": "?"})
			}
		}
		fmt.Fprintf(os.Stderr, "Loaded %d specific codes from %s\n", len(rows), *codesFile)
	} else {
		// Get top codes without English, ordered by coverage
		rows = llmQuery(fmt.Sprintf(`
SELECT phelps, COUNT(*) as lang_count
FROM writings w
WHERE phelps NOT LIKE 'TMP%%'
AND phelps NOT LIKE 'XA%%'
AND phelps NOT LIKE 'XB%%'
AND phelps IS NOT NULL AND phelps <> ''
AND NOT EXISTS (
  SELECT 1 FROM writings e WHERE e.phelps = w.phelps AND e.language = 'en'
)
GROUP BY phelps
HAVING lang_count >= %d
ORDER BY lang_count DESC
LIMIT %d`, *minLangs, *limit))
	}

	fmt.Fprintf(os.Stderr, "Found %d codes to translate\n", len(rows))

	for i, row := range rows {
		phelps := row["phelps"]
		count := row["lang_count"]
		fmt.Fprintf(os.Stderr, "[%d/%d] %s (%s langs)\n", i+1, len(rows), phelps, count)

		// Try to get the best source text
		var srcLang, srcText, srcName string
		for _, lang := range preferredSrcLangs {
			src := llmQuery(fmt.Sprintf(
				`SELECT language, LEFT(text, 2000) as text, COALESCE(name,'') as name
				 FROM writings WHERE phelps='%s' AND language='%s' LIMIT 1`,
				llmSqlEsc(phelps), lang))
			if len(src) > 0 && src[0]["text"] != "" {
				srcLang = src[0]["language"]
				srcText = src[0]["text"]
				srcName = src[0]["name"]
				break
			}
		}

		// Fall back to any available language, preferring longer texts (better content)
		if srcLang == "" {
			src := llmQuery(fmt.Sprintf(
				`SELECT language, LEFT(text, 2000) as text, COALESCE(name,'') as name
				 FROM writings WHERE phelps='%s' AND language <> 'en' AND LENGTH(text) > 50
				 ORDER BY LENGTH(text) DESC LIMIT 1`,
				llmSqlEsc(phelps)))
			if len(src) > 0 {
				srcLang = src[0]["language"]
				srcText = src[0]["text"]
				srcName = src[0]["name"]
			}
		}

		if srcLang == "" {
			fmt.Fprintf(os.Stderr, "  SKIP: no source text found\n")
			continue
		}

		enText := geminiTranslate(srcLang, srcText, phelps)
		if enText == "" {
			fmt.Fprintf(os.Stderr, "  SKIP: empty translation\n")
			continue
		}

		// Build display name with (LLM translated) suffix
		displayName := srcName
		if displayName != "" && !strings.Contains(displayName, "(LLM translated)") {
			displayName = displayName + " (LLM translated)"
		} else if displayName == "" {
			displayName = "(LLM translated)"
		}

		insertSQL := fmt.Sprintf(
			`SET FOREIGN_KEY_CHECKS=0; `+
				`INSERT INTO writings (phelps, language, version, name, text, source, source_id, is_verified) `+
				`VALUES ('%s', 'en', uuid(), '%s', '%s', '%s', '%s-llm', 0); `+
				`SET FOREIGN_KEY_CHECKS=1;`,
			llmSqlEsc(phelps),
			llmSqlEsc(displayName),
			llmSqlEsc(enText),
			llmSource,
			llmSqlEsc(phelps),
		)

		if *dryRun {
			fmt.Printf("-- %s (from %s)\n%s\n\n", phelps, srcLang, enText[:min(200, len(enText))])
		} else {
			llmExec(insertSQL)
			fmt.Printf("  OK: %s (from %s, %d chars)\n", phelps, srcLang, len(enText))
		}
	}
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
