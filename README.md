# Writing Systems of the World

A curated dataset of the world's writing systems — one markdown file per script, with YAML frontmatter describing structural properties (direction, type, status, ligatures, fonts, …) and a body of prose description.

The repository currently holds **313 scripts**: the public ISO 15924 set plus locally-curated private-use codes (`Qa*`, `Z***`, `Sign`).

## Layout

```
schema.json     JSON Schema describing the frontmatter (WSW-YAML)
scripts/        One <Code>.md per script; <Code> is ISO 15924 (e.g. Arab.md)
scripts/index.json  Flattened JSON dump of the corpus
tools/          Go utilities (Go ≥ 1.22, stdlib only)
  update.go     Fetches new scripts from silnrsi/wstr and validates the corpus
  go.mod
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

The updater fills in the left column for new scripts. The right column is left blank for human curation.

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

- required keys present
- no unknown keys
- canonical key order
- enum vocabularies (`family`, `type`, `status`, `direction`, …)
- regex patterns (`script`, `abbr_short`, `open_type_tag`)
- boolean fields are `true`/`false`
- `script` codes and `abbr_short` values are unique across the corpus

Exits non-zero if any file has issues. Current status: **313 ok, 0 with errors**.

## Adding or editing scripts by hand

1. Create `scripts/<Code>.md` (first letter uppercase, e.g. `Vith.md`).
2. Copy the frontmatter shape from `schema.json` or an existing nearby script.
3. Run `go run ./tools -validate` before committing.

## Provenance & licensing

Description text and structural properties are derived from [silnrsi/wstr](https://github.com/silnrsi/wstr) (SIL Global), licensed CC BY-SA 4.0. Curated additions (`abbr_short`, `unicode_pdf`, `sample`, `fonts`, `screen_fonts`, private-use codes) are local contributions.
