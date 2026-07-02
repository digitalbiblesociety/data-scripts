// frontmatter.go — parse, edit and re-serialize scripts/<Code>.md files while
// preserving the original formatting byte-for-byte. Used by the -fill* modes,
// which must never disturb curated content.
package main

import (
	"fmt"
	"os"
	"strings"
)

// fmBlock is one top-level frontmatter key together with its raw lines.
// lines[0] is the "key: value" (or "key:") line; any following indented or
// blank lines belong to the same block (YAML sequences under fonts/…).
type fmBlock struct {
	key   string
	lines []string
}

// fmDoc is a parsed script file: ordered frontmatter blocks plus the raw
// remainder of the file after the closing "---" delimiter.
type fmDoc struct {
	path   string
	blocks []fmBlock
	rest   string // raw text after the closing "---", leading newline included
}

func parseDoc(path string) (*fmDoc, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	text := string(data)
	if !strings.HasPrefix(text, "---\n") {
		return nil, fmt.Errorf("%s: no opening frontmatter", path)
	}
	body := text[4:]
	end := strings.Index(body, "\n---")
	if end < 0 {
		return nil, fmt.Errorf("%s: no closing frontmatter", path)
	}
	doc := &fmDoc{path: path, rest: body[end+4:]}
	for _, raw := range strings.Split(body[:end], "\n") {
		if raw == "" || strings.HasPrefix(raw, " ") || strings.HasPrefix(raw, "\t") {
			if len(doc.blocks) == 0 {
				return nil, fmt.Errorf("%s: frontmatter starts with indented line", path)
			}
			b := &doc.blocks[len(doc.blocks)-1]
			b.lines = append(b.lines, raw)
			continue
		}
		colon := strings.Index(raw, ":")
		if colon < 0 {
			return nil, fmt.Errorf("%s: frontmatter line without ':': %q", path, raw)
		}
		doc.blocks = append(doc.blocks, fmBlock{
			key:   strings.TrimSpace(raw[:colon]),
			lines: []string{raw},
		})
	}
	return doc, nil
}

func (d *fmDoc) get(key string) *fmBlock {
	for i := range d.blocks {
		if d.blocks[i].key == key {
			return &d.blocks[i]
		}
	}
	return nil
}

// scalar returns the unquoted scalar value of key, or "" if the key is absent
// or introduces a sequence.
func (d *fmDoc) scalar(key string) string {
	b := d.get(key)
	if b == nil {
		return ""
	}
	colon := strings.Index(b.lines[0], ":")
	return unquote(strings.TrimSpace(b.lines[0][colon+1:]))
}

// set replaces key's block, or inserts a new block at the key's canonical
// schema position.
func (d *fmDoc) set(key string, lines []string) {
	if b := d.get(key); b != nil {
		b.lines = lines
		return
	}
	idx, ok := allowedKeys[key]
	if !ok {
		panic("fmDoc.set: key not in schema: " + key)
	}
	at := len(d.blocks)
	for i := range d.blocks {
		if j, ok := allowedKeys[d.blocks[i].key]; ok && j > idx {
			at = i
			break
		}
	}
	d.blocks = append(d.blocks, fmBlock{})
	copy(d.blocks[at+1:], d.blocks[at:])
	d.blocks[at] = fmBlock{key: key, lines: lines}
}

// setScalar renders "key: value" using the same quoting rules as the updater.
func (d *fmDoc) setScalar(key, value string) {
	if needsQuote(value) {
		d.set(key, []string{fmt.Sprintf("%s: %q", key, value)})
	} else {
		d.set(key, []string{fmt.Sprintf("%s: %s", key, value)})
	}
}

// setScalarQuoted always renders the value double-quoted (corpus convention
// for sample, unicode_pdf and direction_notes).
func (d *fmDoc) setScalarQuoted(key, value string) {
	d.set(key, []string{fmt.Sprintf("%s: %q", key, value)})
}

func (d *fmDoc) marshal() []byte {
	var b strings.Builder
	b.WriteString("---\n")
	for _, blk := range d.blocks {
		for _, l := range blk.lines {
			b.WriteString(l)
			b.WriteString("\n")
		}
	}
	b.WriteString("---")
	b.WriteString(d.rest)
	return []byte(b.String())
}

func (d *fmDoc) save() error {
	return os.WriteFile(d.path, d.marshal(), 0o644)
}

// seqItems parses a sequence block (fonts, screen_fonts) into ordered
// key→value maps, one per "- " item.
func (b *fmBlock) seqItems() []map[string]string {
	var items []map[string]string
	var cur map[string]string
	for _, raw := range b.lines[1:] {
		line := strings.TrimSpace(raw)
		if line == "" {
			continue
		}
		if strings.HasPrefix(line, "- ") {
			cur = map[string]string{}
			items = append(items, cur)
			line = strings.TrimSpace(line[2:])
		}
		if cur == nil {
			continue
		}
		colon := strings.Index(line, ":")
		if colon < 0 {
			continue
		}
		cur[strings.TrimSpace(line[:colon])] = unquote(strings.TrimSpace(line[colon+1:]))
	}
	return items
}
