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
		dir       = flag.String("dir", "scripts", "directory holding local <Code>.md files")
		dryRun    = flag.Bool("dry-run", false, "list new scripts without writing")
		only      = flag.String("only", "", "comma-separated codes to restrict to (e.g. 'Vith,Toto')")
		validate  = flag.Bool("validate", false, "validate frontmatter of all local files against schema (no network)")
		report    = flag.Bool("report", false, "print corpus completeness statistics (no network)")
		genRanges = flag.Bool("gen-ranges", false, "regenerate tools/uniranges_gen.go from the latest UCD")
		fillPDFs  = flag.Bool("fill-pdfs", false, "derive missing unicode_pdf values from UCD block data")
		fillFonts = flag.Bool("fill-fonts", false, "fill missing fonts from Noto families on Google Fonts")
		fillAbbr  = flag.Bool("fill-abbr", false, "assign unique abbr_short values where missing")
		fill      = flag.Bool("fill", false, "replace unspecified/unknown derivable fields from upstream (honors -only)")
		export    = flag.Bool("export", false, "regenerate scripts.json and INDEX.md from the corpus")
		check     = flag.Bool("check", false, "with -export: verify artifacts are in sync instead of writing")
	)
	flag.Parse()

	switch {
	case *validate:
		os.Exit(runValidate(*dir))
	case *report:
		os.Exit(runReport(*dir))
	case *genRanges:
		os.Exit(runGenRanges("tools/uniranges_gen.go"))
	case *fillPDFs:
		os.Exit(runFillPDFs(*dir, *dryRun))
	case *fillFonts:
		os.Exit(runFillFonts(*dir, *dryRun))
	case *fillAbbr:
		os.Exit(runFillAbbr(*dir, *dryRun))
	case *fill:
		os.Exit(runFillUpstream(*dir, *dryRun, parseOnly(*only)))
	case *export:
		os.Exit(runExport(*dir, *check))
	}

	client := newHTTPClient()

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

func newHTTPClient() *http.Client {
	return &http.Client{Timeout: 30 * time.Second}
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

// upstreamFields converts upstream MDX frontmatter into canonical-key values
// (plain, unquoted) plus the extracted plain-markdown description.
func upstreamFields(mdx string) (map[string]string, string, error) {
	fm, body, err := splitFrontmatter(mdx)
	if err != nil {
		return nil, "", err
	}
	props := parseSimpleYAML(fm)

	code := props["scrpropcode"]
	if code == "" {
		return nil, "", fmt.Errorf("missing scrpropcode")
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

	f := map[string]string{
		"script":              code,
		"name":                props["scrpropname"],
		"family":              props["scrpropregion"],
		"type":                props["scrproptype"],
		"whitespace":          props["scrpropwspace"],
		"open_type_tag":       otCode,
		"complex_positioning": yesNoUnknown(behavior, "complex positioning"),
		"diacritics":          boolStr(behavior["diacritics"]),
		"contextual_forms":    boolStr(behavior["contextual forms"]),
		"reordering":          boolStr(behavior["reordering"]),
		"split_graphs":        boolStr(behavior["split graphs"]),
		"status":              props["scrpropstatus"],
		"baseline":            props["scrpropbaseline"],
	}
	if dir != "" {
		f["direction"] = dir
		f["direction_notes"] = directionNotes(dir)
	}
	return f, extractDescription(body), nil
}

func convert(mdx string) (string, error) {
	f, desc, err := upstreamFields(mdx)
	if err != nil {
		return "", err
	}

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

	// Emit fields in the canonical schema order (see schema.json).
	put("script", f["script"])
	// abbr_short, unicode_pdf: curated, no upstream source — omitted.
	put("name", f["name"])
	put("family", f["family"])
	put("type", f["type"])
	put("whitespace", f["whitespace"])
	put("open_type_tag", f["open_type_tag"])
	put("complex_positioning", quoteYesNo(f["complex_positioning"]))
	put("requires_font", "false")
	put("unicode", "true")
	put("diacritics", f["diacritics"])
	put("contextual_forms", f["contextual_forms"])
	put("reordering", f["reordering"])
	put("split_graphs", f["split_graphs"])
	put("status", f["status"])
	put("baseline", f["baseline"])
	put("ligatures", "unspecified")
	put("direction", f["direction"])
	if f["direction"] != "" {
		// corpus convention: direction_notes is always double-quoted
		put("direction_notes", fmt.Sprintf("%q", f["direction_notes"]))
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

// quoteYesNo renders yes/no double-quoted (corpus convention, and YAML 1.1
// would otherwise read them as booleans); other values pass through bare.
func quoteYesNo(v string) string {
	if v == "yes" || v == "no" {
		return fmt.Sprintf("%q", v)
	}
	return v
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
		return "yes"
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
	"languages",
	"translations",
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

	sequenceFields = map[string]bool{
		"fonts":        true,
		"screen_fonts": true,
		"languages":    true,
		"translations": true,
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

func validateFile(path string) []string {
	entries, err := readFrontmatter(path)
	if err != nil {
		return []string{fmt.Sprintf("frontmatter: %v", err)}
	}

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
	errs = append(errs, checkCrossField(entries)...)
	return errs
}

func validateValue(e fmEntry) string {
	v := unquote(e.value)

	// Sequences (fonts, screen_fonts, languages, translations) — we accept
	// them if the key is allowed to be a sequence in the schema and the lines
	// beneath looked list-ish.
	if sequenceFields[e.key] {
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
		if !reChartPDF.MatchString(v) {
			return fmt.Sprintf("value %q must be a Unicode chart URL (https://www.unicode.org/charts/PDF/U<hex>.pdf)", v)
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

	fileErrs := make([][]string, len(paths))
	abbrs := map[string][]int{}
	for i, p := range paths {
		fileErrs[i] = validateFile(p)
		if entries, err := readFrontmatter(p); err == nil {
			for _, e := range entries {
				if e.key == "abbr_short" {
					a := unquote(e.value)
					abbrs[a] = append(abbrs[a], i)
				}
			}
		}
	}

	// corpus-wide: abbr_short values are cross-reference keys and must be unique
	for a, idxs := range abbrs {
		if len(idxs) < 2 {
			continue
		}
		var names []string
		for _, i := range idxs {
			names = append(names, filepath.Base(paths[i]))
		}
		sort.Strings(names)
		for _, i := range idxs {
			fileErrs[i] = append(fileErrs[i],
				fmt.Sprintf("abbr_short %q duplicated across %s", a, strings.Join(names, ", ")))
		}
	}

	ok, bad := 0, 0
	for i, p := range paths {
		if len(fileErrs[i]) == 0 {
			ok++
			continue
		}
		bad++
		fmt.Printf("%s\n", p)
		for _, e := range fileErrs[i] {
			fmt.Printf("  - %s\n", e)
		}
	}
	fmt.Printf("\n%d ok, %d with errors (of %d total)\n", ok, bad, len(paths))
	if bad > 0 {
		return 1
	}
	return 0
}
