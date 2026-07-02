# Writing Systems of the World

A curated dataset of the world's writing systems — one markdown file per script, with YAML frontmatter describing structural properties (direction, type, status, ligatures, fonts, …) and a body of prose description.

The repository currently holds **313 scripts**: the public ISO 15924 set plus locally-curated private-use codes (`Qa*`, `Z***`, `Sign`). Sample validation and chart derivation are aligned with **Unicode 17.0.0** (see `ucdVersion` in `tools/uniranges_gen.go`).

## Layout

```
schema.json     JSON Schema describing the frontmatter (WSW-YAML)
scripts/        One <Code>.md per script; <Code> is ISO 15924 (e.g. Arab.md)
scripts.json    Generated: full corpus as typed JSON (frontmatter + description)
INDEX.md        Generated: per-family index tables
tools/          Go utilities (Go ≥ 1.22, stdlib only)
  update.go         Fetches new scripts from silnrsi/wstr; schema validation
  frontmatter.go    Format-preserving parser/editor for the corpus files
  checks.go         Cross-field and corpus-wide consistency checks
  fill.go           Mechanical gap-filling modes (see below)
  export.go         scripts.json / INDEX.md generation
  report.go         Completeness statistics
  unicode.go        UCD plumbing; regenerates uniranges_gen.go
  uniranges_gen.go  Generated: per-script Unicode code point ranges
.github/workflows/  CI validation + monthly upstream check
```

## File format

Each `scripts/<Code>.md` is a markdown file with YAML frontmatter at the top:

```yaml
---
script: Adlm
abbr_short: ad
unicode_pdf: "https://www.unicode.org/charts/PDF/U1E900.pdf"
name: Adlam
family: African
type: alphabet
whitespace: between words
open_type_tag: none
complex_positioning: unknown
requires_font: false
unicode: true
diacritics: true
contextual_forms: true
reordering: false
split_graphs: false
status: Current
baseline: bottom
ligatures: none
direction: rtl
direction_notes: "RTL (right-to-left)"
sample: "𞤢𞤤𞤵𞤤𞤢𞤤𞤵"
fonts:
  - name: "Noto Sans Adlam"
    url: "https://fonts.google.com/noto/specimen/Noto+Sans+Adlam"
    provider: "Google Fonts"
---

The Adlam script is used for writing the Fulani language in Guinea…
```

The order of keys is canonical — see the `properties` block of `schema.json`. Tools that write these files must preserve it.

### Required keys

`script`, `name`, `unicode`, `requires_font` — present in every file.

Placeholder codes (`Qa*`, `Z***`, `Sign`) intentionally omit `family`/`type`/`status`/`direction`; these are validated when present but not mandatory.

### Curated vs. derivable fields

| Derivable from upstream (silnrsi/wstr)                                                                                                                                                                  | Manually curated                                              |
| ------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- | ------------------------------------------------------------- |
| `script`, `name`, `family`, `type`, `whitespace`, `open_type_tag`, `complex_positioning`, `diacritics`, `contextual_forms`, `reordering`, `split_graphs`, `status`, `baseline`, `direction`, description | `abbr_short`, `unicode_pdf`, `sample`, `fonts`, `screen_fonts` |

The updater fills in the left column for new scripts. The right column is left blank for human curation, though several `-fill*` modes below can bootstrap it mechanically.

## Tools

All commands run from the repo root.

### Add any new upstream scripts

```sh
go run ./tools                # detect codes not yet present, fetch & write
go run ./tools -dry-run       # preview without writing
go run ./tools -only Vith,Toto   # restrict to specific codes
```

Source: [`silnrsi/wstr`](https://github.com/silnrsi/wstr) `src/content/docs/scrlang/scripts/*.mdx` (the repo behind <https://writingsystems.info/scrlang/scripts-index/>).

If you hit the 60-req/hr unauthenticated GitHub rate limit, export `GITHUB_TOKEN`.

The updater never touches existing files — curated values are preserved.

### Validate the corpus against the schema

```sh
go run ./tools -validate
```

Checks every file under `scripts/`:

- required keys present, no unknown keys, canonical key order
- enum vocabularies (`family`, `type`, `status`, `direction`, …)
- regex patterns (`script`, `abbr_short`, `open_type_tag`)
- boolean fields are `true`/`false`
- `unicode_pdf` is a real Unicode chart URL, and absent when `unicode: false`
- every rune of `sample` belongs to the script (per UCD Scripts.txt) or to Common/Inherited
- `abbr_short` values are unique across the corpus

Exits non-zero if any file has issues.

### Completeness report

```sh
go run ./tools -report
```

Prints curated-field coverage, `unspecified`/`unknown` counts per field, and the open curation gaps (Current scripts without a `sample`, encoded scripts without a `unicode_pdf`). Use it to measure curation progress.

### Mechanical gap-filling

All fill modes preserve curated content: they only add missing keys or replace `unspecified`/`unknown` values, print every change they make, and accept `-dry-run`.

```sh
go run ./tools -fill-pdfs   # derive unicode_pdf from UCD block data (verifies each URL)
go run ./tools -fill-fonts  # fill fonts from Noto families on Google Fonts
go run ./tools -fill-abbr   # assign unique abbr_short values where missing
go run ./tools -fill        # re-harvest unspecified/unknown derivable fields from upstream
```

`-fill` honors `-only` for restricting to specific codes.

### Regenerate the Unicode ranges table

```sh
go run ./tools -gen-ranges
```

Refetches UCD `Scripts.txt` / `PropertyValueAliases.txt` and rewrites `tools/uniranges_gen.go` (used by sample validation and `-fill-pdfs`). Run after each annual Unicode release, then re-run `-validate`.

### Machine-readable artifacts

```sh
go run ./tools -export          # regenerate scripts.json and INDEX.md
go run ./tools -export -check   # verify they are in sync (used by CI)
```

`scripts.json` is the whole corpus as a JSON array (typed frontmatter plus the prose description) for downstream consumers; `INDEX.md` is a human-browsable per-family index. Regenerate and commit both whenever `scripts/` changes.

## Adding or editing scripts by hand

1. Create `scripts/<Code>.md` (first letter uppercase, e.g. `Vith.md`).
2. Copy the frontmatter shape from `schema.json` or an existing nearby script.
3. Run `go run ./tools -validate`, then `go run ./tools -export`, before committing.

## Continuous integration

- `validate.yml` — on every push/PR: `go vet`, `go test`, schema validation, and an export-in-sync check.
- `upstream-sync.yml` — monthly (or manually via *Run workflow*): refreshes the UCD ranges table, imports any new scripts from silnrsi/wstr, runs all mechanical fills, regenerates the export artifacts, and opens a pull request with the changes for review. Requires the repo setting **Allow GitHub Actions to create and approve pull requests** (Settings → Actions → General). New files arrive with derivable fields populated; `sample` and `screen_fonts` still need hand-curation.

## Provenance & licensing

Description text and structural properties are derived from [silnrsi/wstr](https://github.com/silnrsi/wstr) (SIL Global), licensed [CC BY-SA 4.0](LICENSE). Curated additions (`abbr_short`, `unicode_pdf`, `sample`, `fonts`, `screen_fonts`, private-use codes) are local contributions under the same license. Unicode data files are © Unicode, Inc., used under the [Unicode License](https://www.unicode.org/license.txt).
