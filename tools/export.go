// export.go — machine-readable build artifacts (-export): scripts.json with
// typed frontmatter plus description, and INDEX.md with per-family tables.
// With -check, verifies the artifacts on disk are in sync with the corpus.
package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"strings"
)

type exportFont struct {
	Name     string `json:"name"`
	URL      string `json:"url"`
	Provider string `json:"provider"`
	Notes    string `json:"notes,omitempty"`
}

type exportTranslation struct {
	TranslationISO string `json:"translation_iso"`
	Name           string `json:"name"`
	Auto           bool   `json:"auto,omitempty"`
}

// exportEntry mirrors the canonical schema key order.
type exportEntry struct {
	Script             string       `json:"script"`
	AbbrShort          string       `json:"abbr_short,omitempty"`
	UnicodePDF         string       `json:"unicode_pdf,omitempty"`
	Name               string       `json:"name"`
	Family             string       `json:"family,omitempty"`
	Type               string       `json:"type,omitempty"`
	Whitespace         string       `json:"whitespace,omitempty"`
	OpenTypeTag        string       `json:"open_type_tag,omitempty"`
	ComplexPositioning string       `json:"complex_positioning,omitempty"`
	RequiresFont       bool         `json:"requires_font"`
	Unicode            bool         `json:"unicode"`
	Diacritics         *bool        `json:"diacritics,omitempty"`
	ContextualForms    *bool        `json:"contextual_forms,omitempty"`
	Reordering         *bool        `json:"reordering,omitempty"`
	SplitGraphs        *bool        `json:"split_graphs,omitempty"`
	Status             string       `json:"status,omitempty"`
	Baseline           string       `json:"baseline,omitempty"`
	Ligatures          string       `json:"ligatures,omitempty"`
	Direction          string       `json:"direction,omitempty"`
	DirectionNotes     string       `json:"direction_notes,omitempty"`
	Sample             string       `json:"sample,omitempty"`
	Fonts              []exportFont        `json:"fonts,omitempty"`
	ScreenFonts        []exportFont        `json:"screen_fonts,omitempty"`
	Languages          []string            `json:"languages,omitempty"`
	Translations       []exportTranslation `json:"translations,omitempty"`
	Description        string              `json:"description,omitempty"`
}

func boolPtr(v string) *bool {
	b := v == "true"
	if v != "true" && v != "false" {
		return nil
	}
	return &b
}

func languagesOf(doc *fmDoc) []string {
	blk := doc.get("languages")
	if blk == nil {
		return nil
	}
	return blk.seqScalars()
}

func translationsOf(doc *fmDoc) []exportTranslation {
	blk := doc.get("translations")
	if blk == nil {
		return nil
	}
	var out []exportTranslation
	for _, item := range blk.seqItems() {
		out = append(out, exportTranslation{
			TranslationISO: item["translation_iso"],
			Name:           item["name"],
			Auto:           item["auto"] == "true",
		})
	}
	return out
}

func fontsOf(doc *fmDoc, key string) []exportFont {
	blk := doc.get(key)
	if blk == nil {
		return nil
	}
	var out []exportFont
	for _, item := range blk.seqItems() {
		out = append(out, exportFont{
			Name:     item["name"],
			URL:      item["url"],
			Provider: item["provider"],
			Notes:    item["notes"],
		})
	}
	return out
}

func buildExport(dir string) ([]exportEntry, error) {
	paths, err := corpusPaths(dir)
	if err != nil {
		return nil, err
	}
	var entries []exportEntry
	for _, p := range paths {
		doc, err := parseDoc(p)
		if err != nil {
			return nil, err
		}
		entries = append(entries, exportEntry{
			Script:             doc.scalar("script"),
			AbbrShort:          doc.scalar("abbr_short"),
			UnicodePDF:         doc.scalar("unicode_pdf"),
			Name:               doc.scalar("name"),
			Family:             doc.scalar("family"),
			Type:               doc.scalar("type"),
			Whitespace:         doc.scalar("whitespace"),
			OpenTypeTag:        doc.scalar("open_type_tag"),
			ComplexPositioning: doc.scalar("complex_positioning"),
			RequiresFont:       doc.scalar("requires_font") == "true",
			Unicode:            doc.scalar("unicode") == "true",
			Diacritics:         boolPtr(doc.scalar("diacritics")),
			ContextualForms:    boolPtr(doc.scalar("contextual_forms")),
			Reordering:         boolPtr(doc.scalar("reordering")),
			SplitGraphs:        boolPtr(doc.scalar("split_graphs")),
			Status:             doc.scalar("status"),
			Baseline:           doc.scalar("baseline"),
			Ligatures:          doc.scalar("ligatures"),
			Direction:          doc.scalar("direction"),
			DirectionNotes:     doc.scalar("direction_notes"),
			Sample:             doc.scalar("sample"),
			Fonts:              fontsOf(doc, "fonts"),
			ScreenFonts:        fontsOf(doc, "screen_fonts"),
			Languages:          languagesOf(doc),
			Translations:       translationsOf(doc),
			Description:        strings.TrimSpace(doc.rest),
		})
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].Script < entries[j].Script })
	return entries, nil
}

func renderJSON(entries []exportEntry) ([]byte, error) {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", "  ")
	if err := enc.Encode(entries); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

func mdCell(s string) string {
	return strings.ReplaceAll(s, "|", "\\|")
}

func renderIndex(entries []exportEntry) []byte {
	byFamily := map[string][]exportEntry{}
	for _, e := range entries {
		f := e.Family
		if f == "" || f == "unspecified" {
			f = "Unclassified"
		}
		byFamily[f] = append(byFamily[f], e)
	}
	var families []string
	for f := range byFamily {
		if f != "Unclassified" {
			families = append(families, f)
		}
	}
	sort.Strings(families)
	if len(byFamily["Unclassified"]) > 0 {
		families = append(families, "Unclassified")
	}

	var b strings.Builder
	b.WriteString("# Script index\n\n")
	fmt.Fprintf(&b, "%d scripts. Generated by `go run ./tools -export`; do not edit by hand.\n", len(entries))
	for _, f := range families {
		fmt.Fprintf(&b, "\n## %s (%d)\n\n", f, len(byFamily[f]))
		b.WriteString("| Code | Name | Type | Status | Direction | Sample |\n")
		b.WriteString("|------|------|------|--------|-----------|--------|\n")
		for _, e := range byFamily[f] {
			fmt.Fprintf(&b, "| [%s](scripts/%s.md) | %s | %s | %s | %s | %s |\n",
				e.Script, e.Script, mdCell(e.Name), mdCell(e.Type), mdCell(e.Status),
				mdCell(e.Direction), mdCell(e.Sample))
		}
	}
	return []byte(b.String())
}

func runExport(dir string, check bool) int {
	entries, err := buildExport(dir)
	if err != nil {
		fmt.Fprintf(os.Stderr, "export: %v\n", err)
		return 2
	}
	jsonOut, err := renderJSON(entries)
	if err != nil {
		fmt.Fprintf(os.Stderr, "export: %v\n", err)
		return 2
	}
	indexOut := renderIndex(entries)

	artifacts := []struct {
		path string
		data []byte
	}{
		{"scripts.json", jsonOut},
		{"INDEX.md", indexOut},
	}

	if check {
		stale := 0
		for _, a := range artifacts {
			have, err := os.ReadFile(a.path)
			if err != nil || !bytes.Equal(have, a.data) {
				stale++
				fmt.Fprintf(os.Stderr, "%s is out of sync with scripts/ — run: go run ./tools -export\n", a.path)
			}
		}
		if stale > 0 {
			return 1
		}
		fmt.Println("export artifacts in sync")
		return 0
	}

	for _, a := range artifacts {
		if err := os.WriteFile(a.path, a.data, 0o644); err != nil {
			fmt.Fprintf(os.Stderr, "write %s: %v\n", a.path, err)
			return 2
		}
		fmt.Printf("wrote %s\n", a.path)
	}
	return 0
}
