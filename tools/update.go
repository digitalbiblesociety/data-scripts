// update fetches new writing-system entries from silnrsi/wstr and writes
// any not yet present in ./scripts as <Code>.md files using the local schema.
//
// Source: https://github.com/silnrsi/wstr (the repo behind
// https://writingsystems.info/scrlang/scripts-index/).
//
// Usage: go run ./tools          # from repo root
//        go run ./tools -dir ./scripts -dry-run
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"
)

const (
	contentsAPI = "https://api.github.com/repos/silnrsi/wstr/contents/src/content/docs/scrlang/scripts"
	userAgent   = "scripts-updater (+local)"
)

type ghEntry struct {
	Name        string `json:"name"`
	Path        string `json:"path"`
	DownloadURL string `json:"download_url"`
	Type        string `json:"type"`
}

func main() {
	var (
		dir      = flag.String("dir", "scripts", "directory holding local <Code>.md files")
		dryRun   = flag.Bool("dry-run", false, "list new scripts without writing")
		only     = flag.String("only", "", "comma-separated codes to restrict to (e.g. 'Vith,Toto')")
		validate = flag.Bool("validate", false, "validate frontmatter of all local files against schema (no network)")
	)
	flag.Parse()

	if *validate {
		os.Exit(runValidate(*dir))
	}

	client := &http.Client{Timeout: 30 * time.Second}

	entries, err := listUpstream(client)
	if err != nil {
		die("list upstream: %v", err)
	}

	existing, err := localCodes(*dir)
	if err != nil {
		die("scan local: %v", err)
	}

	filter := parseOnly(*only)

	var todo []ghEntry
	for _, e := range entries {
		code := codeFromMDX(e.Name)
		if code == "" {
			continue
		}
		if len(filter) > 0 && !filter[strings.ToLower(code)] {
			continue
		}
		if existing[strings.ToLower(code)] {
			continue
		}
		todo = append(todo, e)
	}

	if len(todo) == 0 {
		fmt.Println("nothing to do — local is up to date")
		return
	}

	sort.Slice(todo, func(i, j int) bool { return todo[i].Name < todo[j].Name })
	fmt.Printf("found %d new script(s)\n", len(todo))

	written := 0
	for _, e := range todo {
		code := codeFromMDX(e.Name)
		out := filepath.Join(*dir, capitalize(code)+".md")
		fmt.Printf("  %s  →  %s\n", e.Name, out)
		if *dryRun {
			continue
		}
		mdx, err := fetch(client, e.DownloadURL)
		if err != nil {
			fmt.Fprintf(os.Stderr, "    fetch failed: %v\n", err)
			continue
		}
		md, err := convert(mdx)
		if err != nil {
			fmt.Fprintf(os.Stderr, "    convert failed: %v\n", err)
			continue
		}
		if err := os.WriteFile(out, []byte(md), 0o644); err != nil {
			fmt.Fprintf(os.Stderr, "    write failed: %v\n", err)
			continue
		}
		written++
	}

	if *dryRun {
		fmt.Printf("\ndry-run: would write %d file(s)\n", len(todo))
	} else {
		fmt.Printf("\nwrote %d file(s)\n", written)
	}
}

func parseOnly(s string) map[string]bool {
	if s == "" {
		return nil
	}
	m := map[string]bool{}
	for _, p := range strings.Split(s, ",") {
		if p = strings.TrimSpace(p); p != "" {
			m[strings.ToLower(p)] = true
		}
	}
	return m
}

func listUpstream(c *http.Client) ([]ghEntry, error) {
	body, err := fetch(c, contentsAPI)
	if err != nil {
		return nil, err
	}
	var entries []ghEntry
	if err := json.Unmarshal([]byte(body), &entries); err != nil {
		return nil, fmt.Errorf("parse contents api: %w", err)
	}
	return entries, nil
}

func fetch(c *http.Client, url string) (string, error) {
	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("User-Agent", userAgent)
	if tok := os.Getenv("GITHUB_TOKEN"); tok != "" {
		req.Header.Set("Authorization", "Bearer "+tok)
	}
	resp, err := c.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return "", fmt.Errorf("GET %s → %d: %s", url, resp.StatusCode, strings.TrimSpace(string(b)))
	}
	b, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", err
	}
	return string(b), nil
}

func localCodes(dir string) (map[string]bool, error) {
	out := map[string]bool{}
	dents, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	for _, d := range dents {
		n := d.Name()
		if !strings.HasSuffix(n, ".md") {
			continue
		}
		out[strings.ToLower(strings.TrimSuffix(n, ".md"))] = true
	}
	return out, nil
}

func codeFromMDX(name string) string {
	if !strings.HasSuffix(name, ".mdx") {
		return ""
	}
	return strings.TrimSuffix(name, ".mdx")
}

func capitalize(code string) string {
	if code == "" {
		return code
	}
	return strings.ToUpper(code[:1]) + strings.ToLower(code[1:])
}

// --- MDX → local-format conversion ----------------------------------------

// upstream frontmatter keys we read
var (
	reFrontmatter = regexp.MustCompile(`(?s)\A---\n(.*?)\n---\n`)
	reBehavior    = regexp.MustCompile(`(?i)\s*,\s*`)
	reMDImage     = regexp.MustCompile(`(?m)^!\[[^\]]*\]\([^)]*\)\s*$`)
	reMDLink      = regexp.MustCompile(`\[([^\]]+)\]\([^)]+\)`)
	reMDXTag      = regexp.MustCompile(`<[A-Za-z][A-Za-z0-9]*(\s[^/>]*)?\s*/>`)
	reDetails     = regexp.MustCompile(`(?s)<details>\s*<summary>[^<]*</summary>(.*?)</details>`)
	reBlankLines  = regexp.MustCompile(`\n{3,}`)
)

func convert(mdx string) (string, error) {
	fm, body, err := splitFrontmatter(mdx)
	if err != nil {
		return "", err
	}
	props := parseSimpleYAML(fm)

	code := props["scrpropcode"]
	if code == "" {
		return "", fmt.Errorf("missing scrpropcode")
	}

	desc := extractDescription(body)

	var b strings.Builder
	b.WriteString("---\n")
	put := func(k, v string) {
		if v == "" {
			return
		}
		// Value already carries explicit YAML quoting — pass through verbatim.
		if len(v) >= 2 && v[0] == '"' && v[len(v)-1] == '"' {
			fmt.Fprintf(&b, "%s: %s\n", k, v)
			return
		}
		if needsQuote(v) {
			fmt.Fprintf(&b, "%s: %q\n", k, v)
		} else {
			fmt.Fprintf(&b, "%s: %s\n", k, v)
		}
	}

	// Behavior flags from scrpropbehavior comma-separated list.
	behavior := map[string]bool{}
	if raw := props["scrpropbehavior"]; raw != "" {
		for _, part := range reBehavior.Split(raw, -1) {
			behavior[strings.ToLower(strings.TrimSpace(part))] = true
		}
	}
	otCode := props["scrpropotcode"]
	if otCode == "" {
		otCode = "none"
	}
	dir := strings.ToLower(strings.TrimSpace(props["scrpropdirection"]))

	// Emit fields in the canonical schema order (see schema.json).
	put("script", code)
	// abbr_short, unicode_pdf: curated, no upstream source — omitted.
	put("name", props["scrpropname"])
	put("family", props["scrpropregion"])
	put("type", props["scrproptype"])
	put("whitespace", props["scrpropwspace"])
	put("open_type_tag", otCode)
	put("complex_positioning", yesNoUnknown(behavior, "complex positioning"))
	put("requires_font", "false")
	put("unicode", "true")
	put("diacritics", boolStr(behavior["diacritics"]))
	put("contextual_forms", boolStr(behavior["contextual forms"]))
	put("reordering", boolStr(behavior["reordering"]))
	put("split_graphs", boolStr(behavior["split graphs"]))
	put("status", props["scrpropstatus"])
	put("baseline", props["scrpropbaseline"])
	put("ligatures", "unspecified")
	if dir != "" {
		put("direction", dir)
		put("direction_notes", directionNotes(dir))
	}
	// sample, fonts, screen_fonts: curated, no upstream source — omitted.

	b.WriteString("---\n\n")
	if desc != "" {
		b.WriteString(desc)
		if !strings.HasSuffix(desc, "\n") {
			b.WriteString("\n")
		}
	}
	return b.String(), nil
}

func splitFrontmatter(mdx string) (string, string, error) {
	m := reFrontmatter.FindStringSubmatchIndex(mdx)
	if m == nil {
		return "", "", fmt.Errorf("no frontmatter")
	}
	return mdx[m[2]:m[3]], mdx[m[1]:], nil
}

// parseSimpleYAML handles the flat `key: value` lines used in upstream
// frontmatter. It is not a general YAML parser.
func parseSimpleYAML(s string) map[string]string {
	out := map[string]string{}
	for _, line := range strings.Split(s, "\n") {
		line = strings.TrimRight(line, "\r")
		if line == "" || strings.HasPrefix(strings.TrimSpace(line), "#") {
			continue
		}
		// indented children (e.g. "    hidden: true" under sidebar:) — ignore
		if strings.HasPrefix(line, " ") || strings.HasPrefix(line, "\t") {
			continue
		}
		i := strings.Index(line, ":")
		if i < 0 {
			continue
		}
		k := strings.TrimSpace(line[:i])
		v := strings.TrimSpace(line[i+1:])
		v = strings.Trim(v, `"'`)
		out[k] = v
	}
	return out
}

// extractDescription pulls the "## Script description" section out of the MDX
// body, merges the lead paragraph with the inner <details> block, and strips
// MDX tags / markdown images / link wrappers so the result is plain markdown.
func extractDescription(body string) string {
	const header = "## Script description"
	i := strings.Index(body, header)
	if i < 0 {
		return ""
	}
	section := body[i+len(header):]
	// Stop at the next top-level "## " heading.
	for {
		j := strings.Index(section, "\n## ")
		if j < 0 {
			break
		}
		section = section[:j]
		break
	}

	// Expand <details>…</details> to its inner content (drop the <summary>).
	section = reDetails.ReplaceAllStringFunc(section, func(s string) string {
		m := reDetails.FindStringSubmatch(s)
		if len(m) < 2 {
			return ""
		}
		return "\n" + m[1] + "\n"
	})

	// Drop sample images, self-closing MDX components, and link wrappers.
	section = reMDImage.ReplaceAllString(section, "")
	section = reMDXTag.ReplaceAllString(section, "")
	section = reMDLink.ReplaceAllString(section, "$1")

	// Drop stray <details>/<summary> tags if the regex didn't match.
	section = strings.ReplaceAll(section, "<details>", "")
	section = strings.ReplaceAll(section, "</details>", "")
	section = regexp.MustCompile(`<summary>[^<]*</summary>`).ReplaceAllString(section, "")

	section = reBlankLines.ReplaceAllString(strings.TrimSpace(section), "\n\n")
	return section
}

func directionNotes(dir string) string {
	switch dir {
	case "ltr":
		return "LTR (left-to-right)"
	case "rtl":
		return "RTL (right-to-left)"
	case "ttb":
		return "TTB (top-to-bottom)"
	case "btt":
		return "BTT (bottom-to-top)"
	default:
		return strings.ToUpper(dir)
	}
}

func boolStr(b bool) string {
	if b {
		return "true"
	}
	return "false"
}

func yesNoUnknown(behavior map[string]bool, key string) string {
	if behavior[key] {
		return `"yes"`
	}
	return "unknown"
}

func needsQuote(v string) bool {
	if v == "" {
		return false
	}
	// Already quoted, or contains chars that confuse a naive YAML reader.
	if strings.ContainsAny(v, `:#"'`+"\n") {
		return true
	}
	// Leave bare booleans / numbers / "none" alone.
	switch v {
	case "true", "false", "none", "yes", "no", "unknown":
		return false
	}
	// Quote anything with leading/trailing whitespace or starting with a
	// YAML-significant character.
	if v != strings.TrimSpace(v) {
		return true
	}
	switch v[0] {
	case '-', '?', '*', '&', '!', '|', '>', '%', '@', '`':
		return true
	}
	return false
}

func die(format string, args ...any) {
	fmt.Fprintf(os.Stderr, format+"\n", args...)
	os.Exit(1)
}

// --- schema validation (mirrors schema.json) ------------------------------

// canonical emission order — must match `properties` order in schema.json
var canonicalOrder = []string{
	"script",
	"abbr_short",
	"unicode_pdf",
	"name",
	"family",
	"type",
	"whitespace",
	"open_type_tag",
	"complex_positioning",
	"requires_font",
	"unicode",
	"diacritics",
	"contextual_forms",
	"reordering",
	"split_graphs",
	"status",
	"baseline",
	"ligatures",
	"direction",
	"direction_notes",
	"sample",
	"fonts",
	"screen_fonts",
}

var (
	requiredFields = []string{"script", "name", "unicode", "requires_font"}

	allowedKeys = func() map[string]int {
		m := map[string]int{}
		for i, k := range canonicalOrder {
			m[k] = i
		}
		return m
	}()

	enumValues = map[string][]string{
		"family": {
			"African", "American", "Artificial", "Central Asian", "East Asian",
			"European", "Handsigns", "Indic", "Insular Southeast Asian",
			"Mainland Southeast Asian", "Middle Eastern", "Pacific",
			"Signed Language", "South Asian", "Southeast Asian", "unspecified",
		},
		"type": {"abjad", "abugida", "alphabet", "featural", "logo-syllabary", "syllabary", "unspecified"},
		"whitespace": {"between phrases", "between words", "discretionary", "none", "unspecified"},
		"complex_positioning": {"yes", "no", "unknown"},
		"status":     {"Current", "Historical", "Fictional", "Unclear"},
		"baseline":   {"bottom", "centered", "hanging", "vertical", "unspecified"},
		"ligatures":  {"none", "optional", "required", "unspecified"},
		"direction": {
			"ltr", "rtl", "ttb", "btt",
			"vertical (rtl)", "vertical (rtl) and horizontal (ltr)", "unspecified",
		},
	}

	booleanFields = map[string]bool{
		"requires_font":    true,
		"unicode":          true,
		"diacritics":       true,
		"contextual_forms": true,
		"reordering":       true,
		"split_graphs":     true,
	}

	reScriptCode = regexp.MustCompile(`^([A-Z][a-z]{3}|Qa[a-z0-9]{2})$`)
	reAbbrShort  = regexp.MustCompile(`^[a-z0-9]{2}$`)
	reOTTag      = regexp.MustCompile(`^(none|unspecified|[a-z0-9]{2,4})$`)
)

// fmEntry is one line of the frontmatter as it appears in the file.
type fmEntry struct {
	line  int
	key   string
	value string // raw value text after the colon, trimmed; "" if collection
	isSeq bool   // true if this entry begins a YAML sequence ("key:" followed by "  - …")
}

// readFrontmatter scans top-level frontmatter keys (ignoring nested list items
// under a sequence key). Returns entries in the order they appear.
func readFrontmatter(path string) ([]fmEntry, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	text := string(data)
	if !strings.HasPrefix(text, "---\n") {
		return nil, fmt.Errorf("no opening frontmatter")
	}
	rest := text[4:]
	end := strings.Index(rest, "\n---")
	if end < 0 {
		return nil, fmt.Errorf("no closing frontmatter")
	}
	block := rest[:end]

	var entries []fmEntry
	var cur *fmEntry
	for i, raw := range strings.Split(block, "\n") {
		line := i + 2 // 1-based; first "---" is line 1
		if raw == "" {
			continue
		}
		// nested list item or indented child — belongs to current key
		if strings.HasPrefix(raw, " ") || strings.HasPrefix(raw, "\t") {
			if cur != nil {
				cur.isSeq = cur.isSeq || strings.HasPrefix(strings.TrimSpace(raw), "- ")
			}
			continue
		}
		colon := strings.Index(raw, ":")
		if colon < 0 {
			return nil, fmt.Errorf("line %d: missing ':'", line)
		}
		k := strings.TrimSpace(raw[:colon])
		v := strings.TrimSpace(raw[colon+1:])
		entries = append(entries, fmEntry{line: line, key: k, value: v})
		cur = &entries[len(entries)-1]
	}
	return entries, nil
}

func validateEntries(entries []fmEntry) []string {
	var errs []string
	seen := map[string]bool{}
	lastIdx := -1
	for _, e := range entries {
		if seen[e.key] {
			errs = append(errs, fmt.Sprintf("line %d: duplicate key %q", e.line, e.key))
			continue
		}
		seen[e.key] = true

		idx, ok := allowedKeys[e.key]
		if !ok {
			errs = append(errs, fmt.Sprintf("line %d: unknown key %q (not in schema)", e.line, e.key))
			continue
		}
		if idx < lastIdx {
			errs = append(errs, fmt.Sprintf("line %d: key %q out of canonical order", e.line, e.key))
		}
		lastIdx = idx

		if msg := validateValue(e); msg != "" {
			errs = append(errs, fmt.Sprintf("line %d: %s: %s", e.line, e.key, msg))
		}
	}

	for _, req := range requiredFields {
		if !seen[req] {
			errs = append(errs, fmt.Sprintf("missing required key %q", req))
		}
	}
	return errs
}

func validateValue(e fmEntry) string {
	v := unquote(e.value)

	// Sequences (fonts, screen_fonts) — we accept them if the key is allowed
	// to be a sequence in the schema and the lines beneath looked list-ish.
	if e.key == "fonts" || e.key == "screen_fonts" {
		if e.value != "" {
			return "must be a YAML sequence (no inline scalar)"
		}
		if !e.isSeq {
			return "sequence is empty"
		}
		return ""
	}

	if e.value == "" {
		return "missing value"
	}

	if booleanFields[e.key] {
		if v != "true" && v != "false" {
			return fmt.Sprintf("expected boolean, got %q", v)
		}
		return ""
	}

	if allowed, ok := enumValues[e.key]; ok {
		for _, a := range allowed {
			if v == a {
				return ""
			}
		}
		return fmt.Sprintf("value %q not in enum %v", v, allowed)
	}

	switch e.key {
	case "script":
		if !reScriptCode.MatchString(v) {
			return fmt.Sprintf("value %q must match ISO 15924 four-letter (Aaaa) or private-use (Qaxx) form", v)
		}
	case "abbr_short":
		if !reAbbrShort.MatchString(v) {
			return fmt.Sprintf("value %q must be two lowercase alphanumerics", v)
		}
	case "open_type_tag":
		if !reOTTag.MatchString(v) {
			return fmt.Sprintf("value %q must be 'none' or 2-4 lowercase alphanumerics", v)
		}
	case "unicode_pdf":
		if !strings.HasPrefix(v, "http://") && !strings.HasPrefix(v, "https://") {
			return "must be an http(s) URL"
		}
	}
	return ""
}

func unquote(v string) string {
	if len(v) >= 2 {
		if (v[0] == '"' && v[len(v)-1] == '"') || (v[0] == '\'' && v[len(v)-1] == '\'') {
			return v[1 : len(v)-1]
		}
	}
	return v
}

// dupReport returns one group of lines per value that appears in more than one
// file, sorted for stable output, plus the count of colliding values.
func dupReport(label string, byValue map[string][]string) (lines []string, count int) {
	var dups []string
	for v, files := range byValue {
		if len(files) > 1 {
			dups = append(dups, v)
		}
	}
	sort.Strings(dups)
	for _, v := range dups {
		count++
		lines = append(lines, fmt.Sprintf("duplicate %s %q in:", label, v))
		files := append([]string(nil), byValue[v]...)
		sort.Strings(files)
		for _, f := range files {
			lines = append(lines, "  - "+f)
		}
	}
	return lines, count
}

func runValidate(dir string) int {
	dents, err := os.ReadDir(dir)
	if err != nil {
		fmt.Fprintf(os.Stderr, "read %s: %v\n", dir, err)
		return 2
	}
	var paths []string
	for _, d := range dents {
		if strings.HasSuffix(d.Name(), ".md") {
			paths = append(paths, filepath.Join(dir, d.Name()))
		}
	}
	sort.Strings(paths)

	// Accumulate script codes and abbr_short values across the whole corpus;
	// per-file validation can't see collisions between files.
	codeFiles := map[string][]string{}
	abbrFiles := map[string][]string{}

	ok, bad := 0, 0
	for _, p := range paths {
		entries, err := readFrontmatter(p)
		if err != nil {
			bad++
			fmt.Printf("%s\n  - frontmatter: %v\n", p, err)
			continue
		}
		for _, e := range entries {
			switch e.key {
			case "script":
				if v := unquote(e.value); v != "" {
					codeFiles[v] = append(codeFiles[v], p)
				}
			case "abbr_short":
				if v := unquote(e.value); v != "" {
					abbrFiles[v] = append(abbrFiles[v], p)
				}
			}
		}

		errs := validateEntries(entries)
		if len(errs) == 0 {
			ok++
			continue
		}
		bad++
		fmt.Printf("%s\n", p)
		for _, e := range errs {
			fmt.Printf("  - %s\n", e)
		}
	}

	codeLines, codeDups := dupReport("script code", codeFiles)
	abbrLines, abbrDups := dupReport("abbr_short", abbrFiles)
	dupLines := append(codeLines, abbrLines...)
	dups := codeDups + abbrDups
	if len(dupLines) > 0 {
		fmt.Println()
		for _, l := range dupLines {
			fmt.Println(l)
		}
	}

	fmt.Printf("\n%d ok, %d with errors (of %d total)\n", ok, bad, len(paths))
	if dups > 0 {
		fmt.Printf("%d duplicate value(s) across files\n", dups)
	}
	if bad > 0 || dups > 0 {
		return 1
	}
	return 0
}
