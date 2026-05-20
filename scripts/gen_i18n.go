// gen_i18n.go — Generate i18n artifacts from the Dolt i18n table.
//
// The Dolt holywritings/bahaiwritings@main i18n table is the source of
// truth for UI strings. Rows with key='ui/<key>' hold per-language UI
// translations as JSON-encoded scalars (e.g. value='"Writings"').
//
// This script:
//   1. Queries Dolt for all ui/* rows
//   2. Writes static/data/i18n.json — the bundle the runtime fetches
//   3. Regenerates i18n/<lang>.yaml — Hugo reads these at build time
//
// Run before `hugo` so the YAML files reflect the latest Dolt state.
//
// Usage: go run scripts/gen_i18n.go [--dolt-dir <path>]

package main

import (
	"encoding/csv"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
)

func main() {
	doltDir := flag.String("dolt-dir", filepath.Join(os.Getenv("HOME"), "bahaiwritings"), "Dolt repo path")
	flag.Parse()

	cmd := exec.Command("dolt", "sql", "-q",
		"SELECT `key`, language, value FROM i18n WHERE `key` LIKE 'ui/%' ORDER BY `key`, language",
		"--result-format", "csv")
	cmd.Dir = *doltDir
	out, err := cmd.Output()
	if err != nil {
		fmt.Fprintf(os.Stderr, "dolt query failed: %v\n", err)
		os.Exit(1)
	}

	r := csv.NewReader(strings.NewReader(string(out)))
	rows, err := r.ReadAll()
	if err != nil {
		fmt.Fprintf(os.Stderr, "csv parse failed: %v\n", err)
		os.Exit(1)
	}

	// bundle[lang][key] = decoded string
	bundle := map[string]map[string]string{}
	for i, row := range rows {
		if i == 0 || len(row) < 3 {
			continue
		}
		fullKey, lang, jsonVal := row[0], row[1], row[2]
		key := strings.TrimPrefix(fullKey, "ui/")
		var val string
		if err := json.Unmarshal([]byte(jsonVal), &val); err != nil {
			// Some values may be raw strings due to historic shape; tolerate
			val = jsonVal
		}
		if bundle[lang] == nil {
			bundle[lang] = map[string]string{}
		}
		bundle[lang][key] = val
	}

	// 1. static/data/i18n.json (runtime bundle)
	jsonOut, err := json.Marshal(bundle)
	if err != nil {
		fmt.Fprintf(os.Stderr, "json marshal: %v\n", err)
		os.Exit(1)
	}
	jsonPath := filepath.Join("static", "data", "i18n.json")
	if err := os.MkdirAll(filepath.Dir(jsonPath), 0755); err != nil {
		fmt.Fprintf(os.Stderr, "mkdir: %v\n", err)
		os.Exit(1)
	}
	if err := os.WriteFile(jsonPath, jsonOut, 0644); err != nil {
		fmt.Fprintf(os.Stderr, "write %s: %v\n", jsonPath, err)
		os.Exit(1)
	}
	fmt.Fprintf(os.Stderr, "Wrote %s (%d languages, %d bytes)\n", jsonPath, len(bundle), len(jsonOut))

	// 2. i18n/<lang>.yaml (build-time files for Hugo)
	if err := os.MkdirAll("i18n", 0755); err != nil {
		fmt.Fprintf(os.Stderr, "mkdir i18n: %v\n", err)
		os.Exit(1)
	}
	langs := make([]string, 0, len(bundle))
	for lang := range bundle {
		langs = append(langs, lang)
	}
	sort.Strings(langs)
	for _, lang := range langs {
		kvs := bundle[lang]
		keys := make([]string, 0, len(kvs))
		for k := range kvs {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		var b strings.Builder
		b.WriteString(fmt.Sprintf("# Auto-generated from Dolt i18n table by gen_i18n.go — do not hand-edit\n"))
		b.WriteString(fmt.Sprintf("# Source: holywritings/bahaiwritings@main, rows WHERE `key` LIKE 'ui/%%' AND language='%s'\n\n", lang))
		for _, k := range keys {
			v := kvs[k]
			// Escape for YAML double-quoted string
			esc := strings.ReplaceAll(v, "\\", "\\\\")
			esc = strings.ReplaceAll(esc, "\"", "\\\"")
			esc = strings.ReplaceAll(esc, "\n", "\\n")
			b.WriteString(fmt.Sprintf("%s: \"%s\"\n", k, esc))
		}
		path := filepath.Join("i18n", lang+".yaml")
		if err := os.WriteFile(path, []byte(b.String()), 0644); err != nil {
			fmt.Fprintf(os.Stderr, "write %s: %v\n", path, err)
			os.Exit(1)
		}
		fmt.Fprintf(os.Stderr, "  %s: %d strings\n", lang, len(kvs))
	}
	fmt.Fprintf(os.Stderr, "Wrote i18n/*.yaml (%d languages)\n", len(bundle))
}
