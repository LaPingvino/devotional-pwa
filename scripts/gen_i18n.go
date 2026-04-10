// gen_i18n.go — Combine i18n/*.yaml files into static/data/i18n.json
//
// Parses simple key: "value" YAML (no nesting needed).
// Usage: go run scripts/gen_i18n.go

package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

func parseSimpleYAML(data string) map[string]string {
	m := make(map[string]string)
	for _, line := range strings.Split(data, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		idx := strings.Index(line, ":")
		if idx < 1 {
			continue
		}
		key := strings.TrimSpace(line[:idx])
		val := strings.TrimSpace(line[idx+1:])
		// Strip surrounding quotes
		if len(val) >= 2 && val[0] == '"' && val[len(val)-1] == '"' {
			val = val[1 : len(val)-1]
		}
		m[key] = val
	}
	return m
}

func main() {
	files, err := filepath.Glob("i18n/*.yaml")
	if err != nil || len(files) == 0 {
		fmt.Fprintln(os.Stderr, "No i18n/*.yaml files found")
		os.Exit(1)
	}

	bundle := map[string]map[string]string{}
	for _, f := range files {
		lang := strings.TrimSuffix(filepath.Base(f), ".yaml")
		data, err := os.ReadFile(f)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error reading %s: %v\n", f, err)
			continue
		}
		m := parseSimpleYAML(string(data))
		bundle[lang] = m
		fmt.Fprintf(os.Stderr, "  %s: %d strings\n", lang, len(m))
	}

	out, _ := json.Marshal(bundle)
	outPath := filepath.Join("static", "data", "i18n.json")
	os.WriteFile(outPath, out, 0644)
	fmt.Fprintf(os.Stderr, "Written %s (%d languages, %d bytes)\n", outPath, len(bundle), len(out))
}
