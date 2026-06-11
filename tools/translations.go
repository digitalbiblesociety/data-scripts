// translations populates the `translations[]` array on each script file from
// Wikidata's `rdfs:label` in target languages. It mirrors the flow used for
// language names in data-languages (tools/sources/wikidata_names.go), keyed
// here on wdt:P506 (ISO 15924 alpha-4 code) instead of wdt:P220.
//
// Each translation item shape: {translation_iso, name, auto?}. Curated
// (Wikidata) entries omit `auto`. Future LLM/MT-sourced entries set
// `auto: true`.
//
// Target languages, with priority order for each Wikidata xml:lang tag:
//
//	zho  ← zh-hans > zh-cn > zh-sg > zh
//	jpn  ← ja
//	hin  ← hi
//	kor  ← ko
//	ara  ← ar
//	spa  ← es
//	fra  ← fr
//	deu  ← de
//	por  ← pt
//	ben  ← bn
//	rus  ← ru
//	ind  ← id
//
// Adding more languages: append to `translationTargets`. The SPARQL filter
// and per-row binding logic pick the rest up automatically.
//
// Pipeline:
//
//	1. One chunked Wikidata SPARQL query gathers all candidate rdfs:label
//	   bindings whose xml:lang is in any target's priority list. Cached
//	   monthly as .cache/wikidata-translations-<hash>-<YYYY-MM>.json.
//	2. For each script code with at least one match, the highest-priority
//	   label per target wins. Each becomes one translation item.
//	3. Existing translations in a file are preserved verbatim (dedup key:
//	   translation_iso); only missing targets are appended. The merged array
//	   is sorted by translation_iso and emitted as the last frontmatter key,
//	   per the canonical order in schema.json.
//
// Honors -only Code[,Code,...] to scope a test run and -dry-run / -force.
package main

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
)

const (
	wikidataSPARQL  = "https://query.wikidata.org/sparql"
	sparqlUA        = "scripts-tools/1.0 (https://github.com/dbs/scripts; aidev@dbs.org)"
	sparqlChunkSize = 500
)

// translationTarget maps a target translation_iso (ISO 639-3) to the
// Wikidata xml:lang tags (in priority order, earliest wins) that should
// populate it.
type translationTarget struct {
	Iso  string   // ISO 639-3 of the target language, used as translation_iso
	Tags []string // xml:lang values, highest priority first
}

var translationTargets = []translationTarget{
	{Iso: "zho", Tags: []string{"zh-hans", "zh-cn", "zh-sg", "zh"}},
	{Iso: "jpn", Tags: []string{"ja"}},
	{Iso: "hin", Tags: []string{"hi"}},
	{Iso: "kor", Tags: []string{"ko"}},
	{Iso: "ara", Tags: []string{"ar"}},
	{Iso: "spa", Tags: []string{"es"}},
	{Iso: "fra", Tags: []string{"fr"}},
	{Iso: "deu", Tags: []string{"de"}},
	{Iso: "por", Tags: []string{"pt"}},
	{Iso: "ben", Tags: []string{"bn"}},
	{Iso: "rus", Tags: []string{"ru"}},
	{Iso: "ind", Tags: []string{"id"}},
}

func allWikidataLangTags() []string {
	seen := map[string]bool{}
	var out []string
	for _, t := range translationTargets {
		for _, l := range t.Tags {
			if !seen[l] {
				seen[l] = true
				out = append(out, l)
			}
		}
	}
	return out
}

type tagBinding struct {
	Iso      string // translation_iso (target)
	Priority int    // lower wins
}

func wikidataTagBindings() map[string]tagBinding {
	m := map[string]tagBinding{}
	for _, t := range translationTargets {
		for i, l := range t.Tags {
			if _, dup := m[l]; dup {
				continue
			}
			m[l] = tagBinding{Iso: t.Iso, Priority: i}
		}
	}
	return m
}

func runTranslations(dir, only string, dryRun, force bool) int {
	codes, err := scriptCodes(dir)
	if err != nil {
		fmt.Fprintf(os.Stderr, "scan local: %v\n", err)
		return 2
	}

	if filter := parseOnly(only); len(filter) > 0 {
		filtered := codes[:0]
		for _, c := range codes {
			if filter[strings.ToLower(c)] {
				filtered = append(filtered, c)
			}
		}
		codes = filtered
		fmt.Printf("scope: -only restricted to %d code(s)\n", len(codes))
	} else {
		fmt.Printf("scope: full corpus, %d codes\n", len(codes))
	}
	if len(codes) == 0 {
		return 0
	}

	mapping, err := fetchTranslationMapping(codes, force)
	if err != nil {
		fmt.Fprintf(os.Stderr, "wikidata: %v\n", err)
		return 2
	}
	fmt.Printf("wikidata: resolved labels for %d code(s)\n", len(mapping))

	updated, unchanged, skipped := 0, 0, 0
	perTarget := map[string]int{}
	for _, code := range codes {
		byTarget, ok := mapping[code]
		if !ok || len(byTarget) == 0 {
			skipped++
			continue
		}
		for t := range byTarget {
			perTarget[t]++
		}
		path := filepath.Join(dir, code+".md")
		changed, err := mergeTranslationsFile(path, byTarget, false, dryRun)
		if err != nil {
			fmt.Fprintf(os.Stderr, "%s: %v\n", path, err)
			return 2
		}
		if changed {
			updated++
		} else {
			unchanged++
		}
	}

	fmt.Printf("updated: %d  unchanged: %d  no-label: %d\n", updated, unchanged, skipped)
	for _, t := range translationTargets {
		fmt.Printf("  %s: %d\n", t.Iso, perTarget[t.Iso])
	}
	if dryRun {
		fmt.Println("(dry run — no files written)")
	}
	return 0
}

// scriptCodes lists <Code> basenames under dir, preserving case, sorted.
func scriptCodes(dir string) ([]string, error) {
	dents, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	var out []string
	for _, d := range dents {
		n := d.Name()
		if strings.HasSuffix(n, ".md") {
			out = append(out, strings.TrimSuffix(n, ".md"))
		}
	}
	sort.Strings(out)
	return out, nil
}

// --- Wikidata fetch --------------------------------------------------------

type sparqlResp struct {
	Results struct {
		Bindings []map[string]struct {
			Value   string `json:"value"`
			XMLLang string `json:"xml:lang,omitempty"`
		} `json:"bindings"`
	} `json:"results"`
}

// fetchTranslationMapping returns, per script code, a map of target
// translation_iso → best-priority label.
func fetchTranslationMapping(codes []string, force bool) (map[string]map[string]string, error) {
	hash := codesHash(codes)
	cacheFile := filepath.Join(".cache", fmt.Sprintf("wikidata-translations-%s-%s.json", hash, yearMonth(time.Now().UTC())))
	if !force {
		if data, err := os.ReadFile(cacheFile); err == nil {
			var out map[string]map[string]string
			if json.Unmarshal(data, &out) == nil {
				fmt.Printf("  mapping: %s (cached)\n", cacheFile)
				return out, nil
			}
		}
	}
	out, err := runTranslationSPARQLChunks(codes)
	if err != nil {
		return nil, err
	}
	if data, err := json.Marshal(out); err == nil {
		_ = os.MkdirAll(".cache", 0o755)
		if err := os.WriteFile(cacheFile, data, 0o644); err == nil {
			fmt.Printf("  mapping: %s (wrote %d)\n", cacheFile, len(out))
		}
	}
	return out, nil
}

func runTranslationSPARQLChunks(codes []string) (map[string]map[string]string, error) {
	bindings := wikidataTagBindings()
	langs := allWikidataLangTags()
	best := map[string]map[string]string{}
	bestPri := map[string]map[string]int{}

	for i := 0; i < len(codes); i += sparqlChunkSize {
		end := i + sparqlChunkSize
		if end > len(codes) {
			end = len(codes)
		}
		chunk := codes[i:end]
		fmt.Printf("  SPARQL chunk %d-%d (%d codes)...\n", i, end, len(chunk))

		codeValues := make([]string, len(chunk))
		for j, c := range chunk {
			codeValues[j] = `"` + c + `"`
		}
		langValues := make([]string, len(langs))
		for j, l := range langs {
			langValues[j] = `"` + l + `"`
		}
		query := fmt.Sprintf(`SELECT ?code ?label WHERE {
  VALUES ?code { %s }
  ?script wdt:P506 ?code .
  ?script rdfs:label ?label .
  FILTER(LANG(?label) IN (%s))
}`, strings.Join(codeValues, " "), strings.Join(langValues, ", "))

		q := url.Values{}
		q.Set("query", query)
		q.Set("format", "json")
		body, err := sparqlGET(wikidataSPARQL + "?" + q.Encode())
		if err != nil {
			return nil, fmt.Errorf("chunk %d-%d: %w", i, end, err)
		}
		var resp sparqlResp
		if err := json.Unmarshal(body, &resp); err != nil {
			return nil, fmt.Errorf("parse chunk %d-%d: %w", i, end, err)
		}
		for _, b := range resp.Results.Bindings {
			code := b["code"].Value
			label := b["label"].Value
			lang := strings.ToLower(b["label"].XMLLang)
			if code == "" || label == "" || lang == "" {
				continue
			}
			bind, ok := bindings[lang]
			if !ok {
				continue
			}
			fb, fok := best[code]
			if !fok {
				fb = map[string]string{}
				best[code] = fb
				bestPri[code] = map[string]int{}
			}
			pri := bestPri[code]
			if cur, exists := pri[bind.Iso]; exists && bind.Priority >= cur {
				continue
			}
			fb[bind.Iso] = label
			pri[bind.Iso] = bind.Priority
		}
		time.Sleep(250 * time.Millisecond)
	}
	return best, nil
}

var sparqlClient = &http.Client{Timeout: 90 * time.Second}

// sparqlGET fetches a SPARQL endpoint. The per-chunk responses are not
// cached at the HTTP layer — caching happens in fetchTranslationMapping
// after the chunks have been merged.
func sparqlGET(u string) ([]byte, error) {
	req, err := http.NewRequest(http.MethodGet, u, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", sparqlUA)
	req.Header.Set("Accept", "application/sparql-results+json")
	resp, err := sparqlClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		preview, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return nil, fmt.Errorf("GET sparql → %d: %s", resp.StatusCode, strings.TrimSpace(string(preview)))
	}
	return io.ReadAll(resp.Body)
}

// codesHash returns a short stable fingerprint of the input codes so we can
// detect when the input set changes and avoid stale caches.
func codesHash(codes []string) string {
	h := uint64(1469598103934665603)
	for _, c := range codes {
		for i := 0; i < len(c); i++ {
			h ^= uint64(c[i])
			h *= 1099511628211
		}
		h ^= '|'
		h *= 1099511628211
	}
	return strconv.FormatUint(h, 16)
}

func yearMonth(t time.Time) string {
	return fmt.Sprintf("%04d-%02d", t.Year(), int(t.Month()))
}

// --- frontmatter merge -------------------------------------------------------

// rawTranslation is one existing translations[] item, kept verbatim so
// curated edits survive a re-run untouched.
type rawTranslation struct {
	iso   string
	lines []string
}

var reTranslationIso = regexp.MustCompile(`^\s*(?:- )?translation_iso:\s*(\S+)\s*$`)

// mergeTranslationsFile appends missing targets to the file's translations[]
// array. Existing items win on collision and are preserved byte-for-byte.
// The merged array is sorted by translation_iso and placed as the last
// frontmatter key. Items merged with auto set carry `auto: true`, marking
// them LLM/MT-sourced rather than curated. Returns whether the file changed
// (or would change).
func mergeTranslationsFile(path string, byTarget map[string]string, auto, dryRun bool) (bool, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return false, err
	}
	text := string(data)
	if !strings.HasPrefix(text, "---\n") {
		return false, fmt.Errorf("no opening frontmatter")
	}
	rest := text[4:]
	end := strings.Index(rest, "\n---")
	if end < 0 {
		return false, fmt.Errorf("no closing frontmatter")
	}
	block, tail := rest[:end], rest[end:]

	others, items := splitTranslationsBlock(block)
	have := map[string]bool{}
	for _, it := range items {
		have[it.iso] = true
	}

	added := false
	for iso, name := range byTarget {
		if have[iso] {
			continue
		}
		lines := []string{
			"  - translation_iso: " + iso,
			"    name: " + yamlScalar(name),
		}
		if auto {
			lines = append(lines, "    auto: true")
		}
		items = append(items, rawTranslation{iso: iso, lines: lines})
		added = true
	}
	if len(items) == 0 {
		return false, nil
	}
	sort.Slice(items, func(i, j int) bool { return items[i].iso < items[j].iso })

	var b strings.Builder
	b.WriteString("---\n")
	for _, l := range others {
		b.WriteString(l)
		b.WriteString("\n")
	}
	b.WriteString("translations:\n")
	for _, it := range items {
		for _, l := range it.lines {
			b.WriteString(l)
			b.WriteString("\n")
		}
	}
	// b ends with "\n" and tail starts with "\n---" — drop the duplicate.
	newText := strings.TrimSuffix(b.String(), "\n") + tail

	if newText == text {
		return false, nil
	}
	if added && !dryRun {
		if err := os.WriteFile(path, []byte(newText), 0o644); err != nil {
			return false, err
		}
	}
	return added, nil
}

// splitTranslationsBlock separates the translations: sequence from the rest
// of the frontmatter block. Items are returned with their raw lines.
func splitTranslationsBlock(block string) (others []string, items []rawTranslation) {
	lines := strings.Split(block, "\n")
	inBlock := false
	var cur *rawTranslation
	flush := func() {
		if cur != nil {
			items = append(items, *cur)
			cur = nil
		}
	}
	for _, l := range lines {
		indented := strings.HasPrefix(l, " ") || strings.HasPrefix(l, "\t")
		switch {
		case strings.TrimRight(l, " ") == "translations:":
			inBlock = true
		case inBlock && indented:
			if strings.HasPrefix(strings.TrimSpace(l), "- ") {
				flush()
				cur = &rawTranslation{lines: []string{l}}
			} else if cur != nil {
				cur.lines = append(cur.lines, l)
			}
			if cur != nil && cur.iso == "" {
				if m := reTranslationIso.FindStringSubmatch(l); m != nil {
					cur.iso = unquote(m[1])
				}
			}
		default:
			if inBlock && !indented {
				inBlock = false
				flush()
			}
			others = append(others, l)
		}
	}
	flush()
	return others, items
}

// yamlScalar renders a label, quoting only when the plain form would be
// ambiguous YAML.
func yamlScalar(v string) string {
	if needsQuote(v) {
		return strconv.Quote(v)
	}
	return v
}

// --- missing report ----------------------------------------------------------

// missingEntry is one script's translation gaps, with enough context for a
// human or LLM translator to act on.
type missingEntry struct {
	Name     string            `json:"name"`
	Family   string            `json:"family,omitempty"`
	Type     string            `json:"type,omitempty"`
	Status   string            `json:"status,omitempty"`
	Existing map[string]string `json:"existing,omitempty"` // translation_iso → name
	Missing  []string          `json:"missing"`            // translation_isos still absent
}

var reTranslationName = regexp.MustCompile(`^\s*name:\s*(.+?)\s*$`)

// fileTranslationState returns the script's English name, select context
// fields, and its current translations.
func fileTranslationState(path string) (entry missingEntry, err error) {
	fm, err := readFrontmatter(path)
	if err != nil {
		return entry, err
	}
	for _, e := range fm {
		switch e.key {
		case "name":
			entry.Name = unquote(e.value)
		case "family":
			entry.Family = unquote(e.value)
		case "type":
			entry.Type = unquote(e.value)
		case "status":
			entry.Status = unquote(e.value)
		}
	}

	data, err := os.ReadFile(path)
	if err != nil {
		return entry, err
	}
	text := string(data)
	if !strings.HasPrefix(text, "---\n") {
		return entry, fmt.Errorf("no opening frontmatter")
	}
	rest := text[4:]
	end := strings.Index(rest, "\n---")
	if end < 0 {
		return entry, fmt.Errorf("no closing frontmatter")
	}
	_, items := splitTranslationsBlock(rest[:end])

	entry.Existing = map[string]string{}
	for _, it := range items {
		for _, l := range it.lines {
			if m := reTranslationName.FindStringSubmatch(l); m != nil {
				entry.Existing[it.iso] = unquote(m[1])
				break
			}
		}
	}
	for _, t := range translationTargets {
		if _, ok := entry.Existing[t.Iso]; !ok {
			entry.Missing = append(entry.Missing, t.Iso)
		}
	}
	sort.Strings(entry.Missing)
	return entry, nil
}

// runMissing prints a JSON object mapping each script code with at least one
// absent target to its gaps and context. No network.
func runMissing(dir string) int {
	codes, err := scriptCodes(dir)
	if err != nil {
		fmt.Fprintf(os.Stderr, "scan local: %v\n", err)
		return 2
	}
	out := map[string]missingEntry{}
	for _, code := range codes {
		entry, err := fileTranslationState(filepath.Join(dir, code+".md"))
		if err != nil {
			fmt.Fprintf(os.Stderr, "%s: %v\n", code, err)
			return 2
		}
		if len(entry.Missing) > 0 {
			out[code] = entry
		}
	}
	data, err := json.MarshalIndent(out, "", "  ")
	if err != nil {
		fmt.Fprintf(os.Stderr, "marshal: %v\n", err)
		return 2
	}
	fmt.Println(string(data))
	return 0
}

// --- LLM/MT fill -------------------------------------------------------------

var reTranslationIsoCode = regexp.MustCompile(`^[a-z]{3}$`)

// runFill merges proposals from a JSON file ({code: {translation_iso:
// name}}) into the corpus as `auto: true` entries. Existing items — curated
// or auto — are never replaced.
func runFill(dir, file, only string, dryRun bool) int {
	data, err := os.ReadFile(file)
	if err != nil {
		fmt.Fprintf(os.Stderr, "read %s: %v\n", file, err)
		return 2
	}
	var proposals map[string]map[string]string
	if err := json.Unmarshal(data, &proposals); err != nil {
		fmt.Fprintf(os.Stderr, "parse %s: %v\n", file, err)
		return 2
	}

	codes, err := scriptCodes(dir)
	if err != nil {
		fmt.Fprintf(os.Stderr, "scan local: %v\n", err)
		return 2
	}
	known := map[string]bool{}
	for _, c := range codes {
		known[c] = true
	}
	filter := parseOnly(only)

	targets := map[string]bool{}
	for _, t := range translationTargets {
		targets[t.Iso] = true
	}

	props := make([]string, 0, len(proposals))
	for code := range proposals {
		props = append(props, code)
	}
	sort.Strings(props)

	updated, unchanged, bad := 0, 0, 0
	perTarget := map[string]int{}
	for _, code := range props {
		if len(filter) > 0 && !filter[strings.ToLower(code)] {
			continue
		}
		if !known[code] {
			fmt.Fprintf(os.Stderr, "  skip %s: no such script file\n", code)
			bad++
			continue
		}
		byTarget := map[string]string{}
		for iso, name := range proposals[code] {
			iso = strings.TrimSpace(iso)
			name = strings.TrimSpace(name)
			if !reTranslationIsoCode.MatchString(iso) || !targets[iso] {
				fmt.Fprintf(os.Stderr, "  skip %s/%s: not a supported target\n", code, iso)
				bad++
				continue
			}
			if name == "" {
				fmt.Fprintf(os.Stderr, "  skip %s/%s: empty name\n", code, iso)
				bad++
				continue
			}
			byTarget[iso] = name
		}
		if len(byTarget) == 0 {
			continue
		}
		path := filepath.Join(dir, code+".md")
		changed, err := mergeTranslationsFile(path, byTarget, true, dryRun)
		if err != nil {
			fmt.Fprintf(os.Stderr, "%s: %v\n", path, err)
			return 2
		}
		if changed {
			updated++
			for iso := range byTarget {
				perTarget[iso]++
			}
		} else {
			unchanged++
		}
	}

	fmt.Printf("updated: %d  unchanged: %d  rejected: %d\n", updated, unchanged, bad)
	for _, t := range translationTargets {
		if perTarget[t.Iso] > 0 {
			fmt.Printf("  %s: %d\n", t.Iso, perTarget[t.Iso])
		}
	}
	if dryRun {
		fmt.Println("(dry run — no files written)")
	}
	return 0
}
