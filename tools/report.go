// report.go — corpus completeness statistics (-report), so curation progress
// is measurable instead of anecdotal.
package main

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

var rePlaceholder = regexp.MustCompile(`^(Qa..|Z...|Sign)$`)

func runReport(dir string) int {
	dents, err := os.ReadDir(dir)
	if err != nil {
		fmt.Fprintf(os.Stderr, "read %s: %v\n", dir, err)
		return 2
	}

	curated := []string{"abbr_short", "unicode_pdf", "sample", "fonts", "screen_fonts"}
	curatedN := map[string]int{}
	unspecN := map[string]int{}
	statusN := map[string]int{}
	total, placeholders := 0, 0
	uniNoPDF, currentNoSample := 0, 0

	for _, d := range dents {
		if !strings.HasSuffix(d.Name(), ".md") {
			continue
		}
		entries, err := readFrontmatter(filepath.Join(dir, d.Name()))
		if err != nil {
			fmt.Fprintf(os.Stderr, "%s: %v\n", d.Name(), err)
			continue
		}
		total++
		vals := map[string]string{}
		for _, e := range entries {
			vals[e.key] = unquote(e.value)
			if _, seq := map[string]bool{"fonts": true, "screen_fonts": true}[e.key]; seq {
				vals[e.key] = "present"
			}
			if v := unquote(e.value); v == "unspecified" || v == "unknown" {
				unspecN[e.key]++
			}
		}
		if rePlaceholder.MatchString(vals["script"]) {
			placeholders++
		}
		for _, k := range curated {
			if vals[k] != "" {
				curatedN[k]++
			}
		}
		if s := vals["status"]; s != "" {
			statusN[s]++
		}
		if vals["unicode"] == "true" && vals["unicode_pdf"] == "" {
			uniNoPDF++
		}
		if vals["status"] == "Current" && vals["sample"] == "" {
			currentNoSample++
		}
	}

	fmt.Printf("corpus: %d scripts (%d placeholder codes)\n", total, placeholders)
	var statuses []string
	for s := range statusN {
		statuses = append(statuses, s)
	}
	sort.Strings(statuses)
	fmt.Printf("status:")
	for _, s := range statuses {
		fmt.Printf(" %s %d,", s, statusN[s])
	}
	fmt.Printf(" unset %d\n", total-sum(statusN))

	fmt.Println("\ncurated field coverage:")
	for _, k := range curated {
		fmt.Printf("  %-14s %3d/%d\n", k, curatedN[k], total)
	}

	fmt.Println("\nunspecified/unknown values:")
	var keys []string
	for k := range unspecN {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool { return unspecN[keys[i]] > unspecN[keys[j]] })
	for _, k := range keys {
		fmt.Printf("  %-20s %3d\n", k, unspecN[k])
	}

	fmt.Println("\ngaps:")
	fmt.Printf("  unicode-encoded scripts missing unicode_pdf: %d\n", uniNoPDF)
	fmt.Printf("  Current scripts missing sample:              %d\n", currentNoSample)
	return 0
}

func sum(m map[string]int) int {
	n := 0
	for _, v := range m {
		n += v
	}
	return n
}
