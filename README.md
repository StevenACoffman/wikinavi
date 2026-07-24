# wikinavi

Generate a table of contents / navigation sidebar for a GitHub wiki from your
Markdown files, and inject it into the wiki's `Home.md` and `_Sidebar.md`.

GitHub wikis are flat and their editor can't create folders, so `wikinavi`
reconstructs a nested tree from your page names and renders it as sanitizer-safe
HTML that GitHub actually displays.

## Install

```sh
go install github.com/StevenACoffman/wikinavi@latest
```

## Usage

Run inside a checked-out wiki repository:

```sh
wikinavi gen            # scan ".", inject the TOC into Home.md and _Sidebar.md
wikinavi gen path/to/wiki
wikinavi gen --collapsible   # render directories as collapsible <details> sections
wikinavi version
```

Then commit and push the changed pages to publish.

### Flags

| Flag                 | Effect                                                                |
| -------------------- | --------------------------------------------------------------------- |
| `--collapsible`      | Render directories as collapsible `<details>` sections                |
| `--disable-home`     | Skip injecting into `Home.md`                                          |
| `--disable-sidebar`  | Skip injecting into `_Sidebar.md`                                      |
| `--initialisms=LIST` | Comma-separated acronyms to upper-case in labels (e.g. `SLI,SLO,GCP`)  |
| `-v`, `--verbose`    | Debug logging to stderr                                               |

Every flag can also be set via a `WIKINAVI_`-prefixed environment variable
(e.g. `WIKINAVI_COLLAPSIBLE=true`).

### Labels

A page's link label is derived from its filename: `-` and `_` become spaces, and
each word is title-cased — except a word that already contains a capital is left
untouched, so intentional casing survives (`AuditLogs-Designs.md` → "AuditLogs
Designs", `SLOs-and-SLIs.md` → "SLOs And SLIs"). Recognized initialisms are
upper-cased: Go's standard set (`API`, `HTTP`, `URL`, `ID`, ...) always, plus any
you pass via `--initialisms` (`sli-basics.md` → "SLI Basics" with
`--initialisms=SLI`).

## Folders in a flat wiki: the colon convention

Because a GitHub wiki has no subdirectories, encode folders in the page name
with colons — `:` is treated as a path separator. For example, a page file:

```text
Tips:SLOs:error-budgets.md
```

renders as the tree **Tips → SLOs → error budgets**, while the link still points
at the real flat page (`./Tips:SLOs:error-budgets`, which GitHub serves without
escaping). Hyphens and underscores in a segment become spaces for the display
label; the original casing is preserved (so acronyms like `SLO` stay intact).

At every level, files are listed before subdirectories.

## How injection works

`wikinavi` writes the generated list between two HTML-comment markers:

```html
<!--starttoc-->
... generated navigation ...
<!--endtoc-->
```

On the first run the marker block is prepended to the page (creating the page if
it does not exist). On later runs the block is replaced in place, so the rest of
the page — and your marker positions — are preserved.

## Why HTML (not styled)?

GitHub sanitizes wiki Markdown, stripping `<style>`, `class`, and inline styles,
so the navigation cannot be CSS-styled. `wikinavi` emits only allowlisted tags
(`<ul>`, `<li>`, `<a>`, and, with `--collapsible`, `<details>`/`<summary>`), and
uses the native `<details>` disclosure triangle as the expand/collapse indicator.
Folder and file entries are prefixed with 📁 / 📄 via numeric character
references, which survive sanitization.
