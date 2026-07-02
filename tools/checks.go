// checks.go — consistency checks that involve more than one key or more than
// one file, beyond the per-value schema rules in update.go.
package main

import (
	"fmt"
	"regexp"
)

// Every existing unicode_pdf in the corpus is a Unicode chart PDF; keep it so.
var reChartPDF = regexp.MustCompile(`^https://www\.unicode\.org/charts/PDF/U[0-9A-F]{4,5}\.pdf$`)

// compositeScripts maps ISO 15924 codes that cover several Unicode Script
// property values onto their constituent codes.
var compositeScripts = map[string][]string{
	"Hans": {"Hani"},
	"Hant": {"Hani"},
	"Hanb": {"Hani", "Bopo"},
	"Jpan": {"Hani", "Hira", "Kana"},
	"Kore": {"Hani", "Hang"},
	"Hrkt": {"Hira", "Kana"},
	"Jamo": {"Hang"},
}

// rangesFor returns the Unicode code point ranges for an ISO 15924 code, or
// nil when the script is not in the generated table (unencoded or private-use).
func rangesFor(code string) []rng {
	if parts, ok := compositeScripts[code]; ok {
		var out []rng
		for _, p := range parts {
			out = append(out, scriptRanges[p]...)
		}
		return out
	}
	return scriptRanges[code]
}

func inRanges(r rune, ranges []rng) bool {
	for _, x := range ranges {
		if r >= x.lo && r <= x.hi {
			return true
		}
	}
	return false
}

// checkSample verifies every rune of a sample belongs to the script itself or
// to Common/Inherited (spaces, punctuation, shared combining marks). Returns
// "" when fine, or when no range data exists for the code.
func checkSample(code, sample string) string {
	ranges := rangesFor(code)
	if len(ranges) == 0 {
		return ""
	}
	neutral := append(append([]rng{}, scriptRanges["Zyyy"]...), scriptRanges["Zinh"]...)
	for _, r := range sample {
		if inRanges(r, ranges) || inRanges(r, neutral) {
			continue
		}
		return fmt.Sprintf("rune %q (U+%04X) is not part of script %s (or Common/Inherited)", r, r, code)
	}
	return ""
}

// checkCrossField runs per-file checks spanning multiple keys.
func checkCrossField(entries []fmEntry) []string {
	vals := map[string]string{}
	for _, e := range entries {
		vals[e.key] = unquote(e.value)
	}
	var errs []string
	if vals["unicode"] == "false" && vals["unicode_pdf"] != "" {
		errs = append(errs, "unicode_pdf present but unicode: false")
	}
	if s := vals["sample"]; s != "" {
		if msg := checkSample(vals["script"], s); msg != "" {
			errs = append(errs, "sample: "+msg)
		}
	}
	return errs
}
