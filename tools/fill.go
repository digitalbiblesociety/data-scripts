// fill.go — mechanical gap-filling modes (-fill-pdfs, -fill-fonts, -fill-abbr,
// -fill). Every mode preserves curated content: it only adds missing keys or
// replaces unspecified/unknown values, and prints exactly what it changed.
package main

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

func corpusPaths(dir string) ([]string, error) {
	dents, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	var paths []string
	for _, d := range dents {
		if strings.HasSuffix(d.Name(), ".md") {
			paths = append(paths, filepath.Join(dir, d.Name()))
		}
	}
	sort.Strings(paths)
	return paths, nil
}

func isUnspec(v string) bool {
	return v == "" || v == "unspecified" || v == "unknown"
}

// --- unicode_pdf ------------------------------------------------------------

// chartOverrides pins scripts whose chart cannot be derived by name/majority:
// composites get the chart of their most identifying component, and Hangul
// keys off the syllables chart rather than the jamo chart.
var chartOverrides = map[string]rune{
	"Hani": 0x4E00, // CJK Unified Ideographs (URO), not Extension B
	"Hans": 0x4E00,
	"Hant": 0x4E00,
	"Hang": 0xAC00, // Hangul Syllables (matches existing curation)
	"Jamo": 0x1100, // Hangul Jamo
	"Kore": 0xAC00,
	"Jpan": 0x3040, // Hiragana
	"Hanb": 0x3100, // Bopomofo, the distinguishing component
}

func chartURL(blockLo rune) string {
	return fmt.Sprintf("https://www.unicode.org/charts/PDF/U%04X.pdf", blockLo)
}

func overlap(a, b rng) int {
	lo, hi := a.lo, a.hi
	if b.lo > lo {
		lo = b.lo
	}
	if b.hi < hi {
		hi = b.hi
	}
	if hi < lo {
		return 0
	}
	return int(hi-lo) + 1
}

// chartURLMajority picks the block holding the most of the script's code
// points; ties go to the lower block.
func chartURLMajority(ranges []rng, blocks []ucdRange) string {
	best, bestN := -1, 0
	for i, b := range blocks {
		n := 0
		for _, r := range ranges {
			n += overlap(r, rng{b.lo, b.hi})
		}
		if n > bestN {
			best, bestN = i, n
		}
	}
	if best < 0 {
		return ""
	}
	return chartURL(blocks[best].lo)
}

// chartURLPreferred picks the chart for a script the way the corpus does:
// the block named after the script (exact match, then a name that contains
// the script name, lowest block first), falling back to the block holding
// the most of the script's code points.
func chartURLPreferred(code string, blocks []ucdRange) string {
	ranges := scriptRanges[code]
	if len(ranges) == 0 {
		return ""
	}
	long := strings.ToLower(strings.ReplaceAll(scriptLongNames[code], "_", " "))
	best, bestRank := -1, 3
	for i, b := range blocks {
		overlaps := false
		for _, r := range ranges {
			if overlap(r, rng{b.lo, b.hi}) > 0 {
				overlaps = true
				break
			}
		}
		if !overlaps {
			continue
		}
		name := strings.ToLower(b.val)
		rank := 3
		switch {
		case name == long:
			rank = 0
		case strings.Contains(name, long):
			rank = 1
		}
		if rank < bestRank {
			best, bestRank = i, rank
		}
	}
	if bestRank == 3 {
		return chartURLMajority(ranges, blocks)
	}
	return chartURL(blocks[best].lo)
}

func chartURLContaining(cp rune, blocks []ucdRange) string {
	for _, b := range blocks {
		if cp >= b.lo && cp <= b.hi {
			return chartURL(b.lo)
		}
	}
	return ""
}

func urlOK(c *http.Client, url string) bool {
	for _, method := range []string{http.MethodHead, http.MethodGet} {
		req, err := http.NewRequest(method, url, nil)
		if err != nil {
			return false
		}
		req.Header.Set("User-Agent", userAgent)
		resp, err := c.Do(req)
		if err != nil {
			return false
		}
		io.Copy(io.Discard, io.LimitReader(resp.Body, 1024))
		resp.Body.Close()
		switch {
		case resp.StatusCode == 200:
			return true
		case method == http.MethodHead && resp.StatusCode >= 400 && resp.StatusCode != 404:
			continue // some servers reject HEAD; retry with GET
		default:
			return false
		}
	}
	return false
}

func runFillPDFs(dir string, dry bool) int {
	client := newHTTPClient()
	blocks, err := fetchBlocks(client)
	if err != nil {
		fmt.Fprintf(os.Stderr, "fetch Blocks.txt: %v\n", err)
		return 1
	}
	paths, err := corpusPaths(dir)
	if err != nil {
		fmt.Fprintf(os.Stderr, "scan %s: %v\n", dir, err)
		return 1
	}

	added, mismatched, skipped := 0, 0, 0
	for _, p := range paths {
		doc, err := parseDoc(p)
		if err != nil {
			fmt.Fprintf(os.Stderr, "%v\n", err)
			continue
		}
		code := doc.scalar("script")
		if doc.scalar("unicode") != "true" {
			continue
		}

		var url string
		switch {
		case chartOverrides[code] != 0:
			url = chartURLContaining(chartOverrides[code], blocks)
		case len(compositeScripts[code]) > 0:
			if doc.scalar("unicode_pdf") == "" {
				skipped++
				fmt.Printf("  skip %s: composite script, set unicode_pdf by hand\n", code)
			}
			continue
		case len(scriptRanges[code]) > 0:
			url = chartURLPreferred(code, blocks)
		default:
			if doc.scalar("unicode_pdf") == "" {
				skipped++
				fmt.Printf("  skip %s: no UCD ranges under this code\n", code)
			}
			continue
		}

		if have := doc.scalar("unicode_pdf"); have != "" {
			if have != url {
				mismatched++
				fmt.Printf("  mismatch %s: has %s, derived %s (kept)\n", code, have, url)
			}
			continue
		}

		fmt.Printf("  %s → %s\n", code, url)
		if dry {
			added++
			continue
		}
		if !urlOK(client, url) {
			skipped++
			fmt.Printf("    ! not reachable, skipped\n")
			continue
		}
		doc.setScalarQuoted("unicode_pdf", url)
		if err := doc.save(); err != nil {
			fmt.Fprintf(os.Stderr, "write %s: %v\n", p, err)
			return 1
		}
		added++
	}

	verb := "added"
	if dry {
		verb = "would add"
	}
	fmt.Printf("\n%s %d unicode_pdf value(s); %d existing mismatch(es) kept; %d skipped\n",
		verb, added, mismatched, skipped)
	return 0
}

// --- abbr_short -------------------------------------------------------------

func abbrCandidates(code string) []string {
	c := strings.ToLower(code)
	var out []string
	for _, ij := range [][2]int{{0, 1}, {0, 2}, {0, 3}, {1, 2}, {1, 3}, {2, 3}} {
		if ij[1] < len(c) {
			out = append(out, string(c[ij[0]])+string(c[ij[1]]))
		}
	}
	for _, x := range "abcdefghijklmnopqrstuvwxyz0123456789" {
		out = append(out, string(c[0])+string(x))
	}
	return out
}

func runFillAbbr(dir string, dry bool) int {
	paths, err := corpusPaths(dir)
	if err != nil {
		fmt.Fprintf(os.Stderr, "scan %s: %v\n", dir, err)
		return 1
	}
	used := map[string]bool{}
	var todo []*fmDoc
	for _, p := range paths {
		doc, err := parseDoc(p)
		if err != nil {
			fmt.Fprintf(os.Stderr, "%v\n", err)
			continue
		}
		if a := doc.scalar("abbr_short"); a != "" {
			used[a] = true
		} else {
			todo = append(todo, doc)
		}
	}

	added := 0
	for _, doc := range todo {
		code := doc.scalar("script")
		pick := ""
		for _, cand := range abbrCandidates(code) {
			if !used[cand] {
				pick = cand
				break
			}
		}
		if pick == "" {
			fmt.Fprintf(os.Stderr, "  no free abbr_short for %s\n", code)
			continue
		}
		used[pick] = true
		fmt.Printf("  %s → %s\n", code, pick)
		if dry {
			added++
			continue
		}
		doc.setScalar("abbr_short", pick)
		if err := doc.save(); err != nil {
			fmt.Fprintf(os.Stderr, "write %s: %v\n", doc.path, err)
			return 1
		}
		added++
	}
	verb := "added"
	if dry {
		verb = "would add"
	}
	fmt.Printf("\n%s %d abbr_short value(s)\n", verb, added)
	return 0
}

// --- derivable fields from upstream ----------------------------------------

// fillableKeys are the derivable fields -fill may replace when the local value
// is unspecified/unknown. ligatures is absent: upstream has no source for it.
var fillableKeys = []string{
	"family", "type", "whitespace", "open_type_tag",
	"complex_positioning", "status", "baseline", "direction",
}

func runFillUpstream(dir string, dry bool, only map[string]bool) int {
	client := newHTTPClient()
	entries, err := listUpstream(client)
	if err != nil {
		fmt.Fprintf(os.Stderr, "list upstream: %v\n", err)
		return 1
	}
	byCode := map[string]ghEntry{}
	for _, e := range entries {
		if c := codeFromMDX(e.Name); c != "" {
			byCode[strings.ToLower(c)] = e
		}
	}

	paths, err := corpusPaths(dir)
	if err != nil {
		fmt.Fprintf(os.Stderr, "scan %s: %v\n", dir, err)
		return 1
	}

	filesChanged, fieldsFilled := 0, 0
	for _, p := range paths {
		doc, err := parseDoc(p)
		if err != nil {
			fmt.Fprintf(os.Stderr, "%v\n", err)
			continue
		}
		code := doc.scalar("script")
		if only != nil && !only[strings.ToLower(code)] {
			continue
		}
		changed := false

		// Fill derivable fields from upstream where the local value is a
		// placeholder. Only existing keys are replaced; keys intentionally
		// omitted (private-use codes) stay omitted.
		needs := false
		for _, k := range fillableKeys {
			if doc.get(k) != nil && isUnspec(doc.scalar(k)) {
				needs = true
				break
			}
		}
		if needs {
			if e, ok := byCode[strings.ToLower(code)]; ok {
				mdx, err := fetch(client, e.DownloadURL)
				if err != nil {
					fmt.Fprintf(os.Stderr, "  %s: fetch upstream: %v\n", code, err)
					continue
				}
				up, _, err := upstreamFields(mdx)
				if err != nil {
					fmt.Fprintf(os.Stderr, "  %s: parse upstream: %v\n", code, err)
					continue
				}
				for _, k := range fillableKeys {
					if doc.get(k) == nil || !isUnspec(doc.scalar(k)) {
						continue
					}
					uv := up[k]
					if isUnspec(uv) || (k == "open_type_tag" && uv == "none") {
						continue
					}
					// guard against upstream vocabulary drift
					if msg := validateValue(fmEntry{key: k, value: uv}); msg != "" {
						fmt.Fprintf(os.Stderr, "  %s: upstream %s: %s (skipped)\n", code, k, msg)
						continue
					}
					fmt.Printf("  %s: %s %s → %s\n", code, k, doc.scalar(k), uv)
					if k == "complex_positioning" {
						doc.setScalarQuoted(k, uv)
					} else {
						doc.setScalar(k, uv)
					}
					fieldsFilled++
					changed = true
				}
			}
		}

		// direction_notes is derivable locally from direction.
		dirv := doc.scalar("direction")
		switch dirv {
		case "ltr", "rtl", "ttb", "btt":
			if doc.get("direction_notes") == nil || doc.scalar("direction_notes") == "unspecified" {
				fmt.Printf("  %s: direction_notes → %q\n", code, directionNotes(dirv))
				doc.setScalarQuoted("direction_notes", directionNotes(dirv))
				fieldsFilled++
				changed = true
			}
		}

		if changed {
			filesChanged++
			if !dry {
				if err := doc.save(); err != nil {
					fmt.Fprintf(os.Stderr, "write %s: %v\n", p, err)
					return 1
				}
			}
		}
	}
	verb := "filled"
	if dry {
		verb = "would fill"
	}
	fmt.Printf("\n%s %d field(s) across %d file(s)\n", verb, fieldsFilled, filesChanged)
	return 0
}

// --- fonts via Google Fonts Noto -------------------------------------------

const gfMetadataURL = "https://fonts.google.com/metadata/fonts"

type gfFamily struct {
	Family  string   `json:"family"`
	Subsets []string `json:"subsets"`
}

func fetchGoogleFonts(c *http.Client) ([]gfFamily, error) {
	body, err := fetch(c, gfMetadataURL)
	if err != nil {
		return nil, err
	}
	// The endpoint historically prefixes the JSON with an XSSI guard.
	if i := strings.Index(body, "{"); i > 0 {
		body = body[i:]
	}
	var meta struct {
		FamilyMetadataList []gfFamily `json:"familyMetadataList"`
	}
	if err := json.Unmarshal([]byte(body), &meta); err != nil {
		return nil, fmt.Errorf("parse fonts metadata: %w", err)
	}
	return meta.FamilyMetadataList, nil
}

// gfSubsetFor maps an ISO 15924 code to the Google Fonts subset slug via the
// UCD long script name ("Canadian_Aboriginal" → "canadian-aboriginal").
func gfSubsetFor(code string) string {
	long, ok := scriptLongNames[code]
	if !ok {
		return ""
	}
	return strings.ReplaceAll(strings.ToLower(long), "_", "-")
}

func notoSpecimenURL(family string) string {
	return "https://fonts.google.com/noto/specimen/" + strings.ReplaceAll(family, " ", "+")
}

// pickNoto returns the best Noto Sans and Noto Serif families for a subset:
// prefer a family that names the script, then the shortest name.
func pickNoto(families []gfFamily, subset, longName string) []string {
	nameToken := strings.ToLower(strings.ReplaceAll(longName, "_", " "))
	var picks []string
	for _, kind := range []string{"Noto Sans", "Noto Serif"} {
		best := ""
		bestRank := 1 << 30
		for _, f := range families {
			if !strings.HasPrefix(f.Family, kind) {
				continue
			}
			hasSubset := false
			for _, s := range f.Subsets {
				if s == subset {
					hasSubset = true
					break
				}
			}
			if !hasSubset {
				continue
			}
			rank := len(f.Family)
			if strings.Contains(strings.ToLower(f.Family), nameToken) {
				rank -= 1000
			}
			if rank < bestRank {
				best, bestRank = f.Family, rank
			}
		}
		if best != "" {
			picks = append(picks, best)
		}
	}
	return picks
}

func fontBlockLines(key string, families []string) []string {
	lines := []string{key + ":"}
	for _, f := range families {
		lines = append(lines,
			fmt.Sprintf("  - name: %q", f),
			fmt.Sprintf("    url: %q", notoSpecimenURL(f)),
			`    provider: "Google Fonts"`,
		)
	}
	return lines
}

func runFillFonts(dir string, dry bool) int {
	client := newHTTPClient()
	families, err := fetchGoogleFonts(client)
	if err != nil {
		fmt.Fprintf(os.Stderr, "fetch Google Fonts metadata: %v\n", err)
		return 1
	}
	paths, err := corpusPaths(dir)
	if err != nil {
		fmt.Fprintf(os.Stderr, "scan %s: %v\n", dir, err)
		return 1
	}

	added, skipped := 0, 0
	for _, p := range paths {
		doc, err := parseDoc(p)
		if err != nil {
			fmt.Fprintf(os.Stderr, "%v\n", err)
			continue
		}
		if doc.get("fonts") != nil || doc.scalar("unicode") != "true" {
			continue
		}
		code := doc.scalar("script")
		subset := gfSubsetFor(code)
		if subset == "" {
			skipped++
			fmt.Printf("  skip %s: no UCD script name (composite or unencoded)\n", code)
			continue
		}
		picks := pickNoto(families, subset, scriptLongNames[code])
		if len(picks) == 0 {
			skipped++
			fmt.Printf("  skip %s: no Noto family on Google Fonts for subset %q\n", code, subset)
			continue
		}
		fmt.Printf("  %s → %s\n", code, strings.Join(picks, ", "))
		if dry {
			added++
			continue
		}
		doc.set("fonts", fontBlockLines("fonts", picks))
		if err := doc.save(); err != nil {
			fmt.Fprintf(os.Stderr, "write %s: %v\n", p, err)
			return 1
		}
		added++
	}
	verb := "added fonts to"
	if dry {
		verb = "would add fonts to"
	}
	fmt.Printf("\n%s %d file(s); %d skipped\n", verb, added, skipped)
	return 0
}
