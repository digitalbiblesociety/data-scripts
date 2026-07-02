package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestRoundTrip proves parseDoc→marshal is byte-identical for every file in
// the corpus, so the -fill* modes cannot silently reformat curated content.
func TestRoundTrip(t *testing.T) {
	paths, err := filepath.Glob(filepath.Join("..", "scripts", "*.md"))
	if err != nil || len(paths) == 0 {
		t.Fatalf("glob corpus: %v (%d files)", err, len(paths))
	}
	for _, p := range paths {
		orig, err := os.ReadFile(p)
		if err != nil {
			t.Fatal(err)
		}
		doc, err := parseDoc(p)
		if err != nil {
			t.Errorf("%s: %v", p, err)
			continue
		}
		if got := doc.marshal(); string(got) != string(orig) {
			t.Errorf("%s: round-trip differs", p)
		}
	}
}

func TestSetInsertsCanonically(t *testing.T) {
	doc := &fmDoc{blocks: []fmBlock{
		{key: "script", lines: []string{"script: Test"}},
		{key: "name", lines: []string{"name: Test"}},
		{key: "sample", lines: []string{`sample: "x"`}},
	}}
	doc.setScalarQuoted("unicode_pdf", "https://www.unicode.org/charts/PDF/U0000.pdf")
	var keys []string
	for _, b := range doc.blocks {
		keys = append(keys, b.key)
	}
	if got := strings.Join(keys, ","); got != "script,unicode_pdf,name,sample" {
		t.Fatalf("insertion order wrong: %s", got)
	}
}
