// Package gen implements the "gen" CLI command.
package gen

import (
	"context"
	"errors"
	"fmt"
	"html"
	"io/fs"
	"log/slog"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"slices"
	"strings"

	"github.com/peterbourgon/ff/v4"

	"github.com/StevenACoffman/wikinavi/cmd/root"
)

const (
	startMarker = "<!--starttoc-->"
	endMarker   = "<!--endtoc-->"
)

// Icons for collapsible output. Numeric character references survive GitHub's
// Markdown sanitizer (which strips <style>/class), unlike any CSS approach.
const (
	folderIcon = "&#128193;" // 📁
	fileIcon   = "&#128196;" // 📄
)

// tocBlockRE matches an existing injected block so it can be replaced in place.
// (?s) makes '.' span newlines; the match runs from the opening to the closing
// marker's inner text.
var tocBlockRE = regexp.MustCompile(`(?s)starttoc-->.*<!--endtoc`)

// Config holds the configuration for the gen command.
type Config struct {
	*root.Config
	DisableHome    bool
	DisableSidebar bool
	Collapsible    bool
	Flags          *ff.FlagSet
	Command        *ff.Command
}

// treeVisitor receives structural events as sorted paths are walked into a
// directory tree. depth is the directory's index from the root (0 = top level).
// enterDir/leaveDir bracket each directory (name is the deslugged display
// label); file is called once per leaf with the flat wiki page name for its
// href and the deslugged leaf label. The visitor owns all HTML and formatting
// (escaping, indentation); walkTree owns only the structure.
type treeVisitor struct {
	enterDir func(depth int, name string)
	leaveDir func(depth int)
	file     func(depth int, href, label string)
}

// New creates and registers the gen command with the given parent config.
func New(parent *root.Config) *Config {
	var cfg Config
	cfg.Config = parent
	cfg.Flags = ff.NewFlagSet("gen").SetParent(parent.Flags)
	cfg.Flags.BoolVar(&cfg.DisableHome, 0, "disable-home", "disable Home.md TOC injection")
	cfg.Flags.BoolVar(
		&cfg.DisableSidebar,
		0,
		"disable-sidebar",
		"disable _Sidebar.md TOC injection",
	)
	cfg.Flags.BoolVar(
		&cfg.Collapsible,
		0,
		"collapsible",
		"render directories as collapsible <details> sections",
	)
	cfg.Command = &ff.Command{
		Name:      "gen",
		Usage:     "wikinavi gen [FLAGS] [DIR]",
		ShortHelp: "generate a table of contents and inject it into Home.md and _Sidebar.md",
		LongHelp: `Generate a table of contents from the markdown files in DIR (default ".")
and inject it into the wiki's homepage (Home.md) and sidebar (_Sidebar.md).

The TOC is written between "` + startMarker + `" and "` + endMarker + `" markers.
On first run the marker block is prepended; subsequent runs replace it in place.`,
		Flags: cfg.Flags,
		Exec:  cfg.exec,
	}
	parent.Command.Subcommands = append(parent.Command.Subcommands, cfg.Command)
	return &cfg
}

func (cfg *Config) exec(ctx context.Context, args []string) error {
	l := cfg.logger()

	dir := "."
	if len(args) > 1 {
		return errors.New("gen: too many arguments; expected at most one DIR")
	}
	if len(args) == 1 {
		dir = args[0]
	}

	// Targets live inside DIR so scanning and writing share one root.
	var targetFiles []string
	if !cfg.DisableHome {
		targetFiles = append(targetFiles, filepath.Join(dir, "Home.md"))
	}
	if !cfg.DisableSidebar {
		targetFiles = append(targetFiles, filepath.Join(dir, "_Sidebar.md"))
	}
	if len(targetFiles) == 0 {
		return errors.New("gen: --disable-home and --disable-sidebar both set; nothing to generate")
	}

	if err := initializeFiles(targetFiles); err != nil {
		return fmt.Errorf("gen: %w", err)
	}

	files, err := listFiles(ctx, l, dir, ".md")
	if err != nil {
		return fmt.Errorf("gen: %w", err)
	}
	l.DebugContext(ctx, "collected markdown files", "count", len(files), "files", files)

	render := renderTree
	if cfg.Collapsible {
		render = renderCollapsibleTree
	}
	toc := render(files)
	l.DebugContext(ctx, "rendered table of contents", "toc", toc)

	l.InfoContext(ctx, "injecting table of contents", "targets", targetFiles)
	if err := updateFiles(ctx, l, targetFiles, toc); err != nil {
		return fmt.Errorf("gen: %w", err)
	}

	l.InfoContext(ctx, "injection complete; commit and push your changes to publish")
	return nil
}

// logger returns the shared logger, or a no-op logger when the command is
// constructed directly (e.g. in unit tests) without the dispatcher.
func (cfg *Config) logger() *slog.Logger {
	if cfg.Logger != nil {
		return cfg.Logger
	}
	return slog.New(slog.DiscardHandler)
}

// listFiles walks dir and returns every file with the given extension, skipping
// dotfiles/dot-directories (.git, .idea, ...) and the generated wiki pages.
func listFiles(ctx context.Context, l *slog.Logger, dir, extension string) ([]string, error) {
	// Never listed: the two generated wiki pages plus the repo README.
	excluded := []string{"Home.md", "_Sidebar.md", "README.md"}
	var files []string
	err := filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		base := filepath.Base(p)
		if base != "." && strings.HasPrefix(base, ".") {
			if d.IsDir() {
				return filepath.SkipDir // don't descend into .git, .idea, ...
			}
			return nil
		}
		if d.IsDir() {
			return nil
		}
		if slices.Contains(excluded, base) {
			l.DebugContext(ctx, "ignoring excluded file", "path", p)
			return nil
		}
		if !strings.EqualFold(filepath.Ext(p), extension) {
			l.DebugContext(ctx, "ignoring non-matching file", "path", p, "ext", filepath.Ext(p))
			return nil
		}
		l.DebugContext(ctx, "appending file", "path", p)
		files = append(files, p)
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("walk %s: %w", dir, err)
	}
	return files, nil
}

// initializeFiles creates any target file that does not yet exist, seeded with
// an empty marker block so updateFile can inject into it.
func initializeFiles(files []string) error {
	const tocTags = startMarker + "\n" + endMarker + "\n"
	for _, f := range files {
		if _, err := os.Stat(f); errors.Is(err, os.ErrNotExist) {
			if err := os.WriteFile(f, []byte(tocTags), 0o644); err != nil {
				return fmt.Errorf("initialize %s: %w", f, err)
			}
		}
	}
	return nil
}

func updateFiles(ctx context.Context, l *slog.Logger, files []string, toc string) error {
	for _, f := range files {
		if err := updateFile(ctx, l, f, toc); err != nil {
			return err
		}
	}
	return nil
}

// updateFile replaces the existing marker block in file with toc, or prepends a
// fresh block when no markers are present.
func updateFile(ctx context.Context, l *slog.Logger, file, toc string) error {
	b, err := os.ReadFile(file)
	if err != nil {
		return fmt.Errorf("read %s: %w", file, err)
	}
	input := string(b)

	var output string
	if strings.Contains(input, "starttoc-->") && strings.Contains(input, "<!--endtoc") {
		// ReplaceAllLiteralString so '$' in a filename is not treated as a submatch reference.
		output = tocBlockRE.ReplaceAllLiteralString(input, "starttoc-->\n"+toc+"<!--endtoc")
	} else {
		output = startMarker + "\n" + toc + "\n" + endMarker + "\n" + input
	}

	l.DebugContext(ctx, "writing file", "path", file, "bytes", len(output))
	if err := os.WriteFile(file, []byte(output), 0o644); err != nil {
		return fmt.Errorf("write %s: %w", file, err)
	}
	return nil
}

// virtualSegments splits a path into its display/tree segments. Both "/" and
// ":" act as separators: because GitHub wikis are flat (the web editor cannot
// create subdirectories), a page encodes its folder path as colons in the
// filename — e.g. "Tips:SLOs:intro.md" lives at Tips/SLOs/intro. The extension
// is dropped and empty segments (from "::" or a leading/trailing ":") are
// ignored.
func virtualSegments(p string) []string {
	parts := strings.Split(strings.TrimPrefix(p, "./"), "/")
	base := parts[len(parts)-1]
	base = base[:len(base)-len(path.Ext(base))]
	parts = parts[:len(parts)-1] // drop the filename; re-add its colon pieces
	for _, s := range strings.Split(base, ":") {
		if s != "" {
			parts = append(parts, s)
		}
	}
	return parts
}

// hrefName returns the real, flat wiki page name a path links to: its base
// filename without extension, colons intact (GitHub serves and links these
// without percent-encoding, e.g. /wiki/Home:Is:Where:This:Is). Any real
// directory prefix is dropped, matching the flat-wiki link convention.
func hrefName(p string) string {
	base := path.Base(strings.TrimPrefix(p, "./"))
	return base[:len(base)-len(path.Ext(base))]
}

// lessFilesFirst orders paths so that, at every level, files sort ahead of
// subdirectories; ties within the same kind stay lexicographic. Levels are the
// virtual segments (see virtualSegments), so colon-encoded folders order just
// like real ones — a consistent total order.
func lessFilesFirst(a, b string) bool {
	as, bs := virtualSegments(a), virtualSegments(b)
	for i := 0; i < len(as) && i < len(bs); i++ {
		if as[i] == bs[i] {
			continue
		}
		aFile, bFile := i == len(as)-1, i == len(bs)-1
		if aFile != bFile {
			return aFile // a file at this level renders before a subdirectory
		}
		return as[i] < bs[i] // same kind: lexicographic
	}
	return len(as) < len(bs)
}

// sortForTree returns a copy of paths ordered files-before-subdirectories at
// every level (see lessFilesFirst), without mutating the caller's slice. The
// ordering keeps each subtree contiguous, which walkTree relies on.
func sortForTree(paths []string) []string {
	out := slices.Clone(paths)
	slices.SortStableFunc(out, func(a, b string) int {
		switch {
		case a == b:
			return 0
		case lessFilesFirst(a, b):
			return -1
		default:
			return 1
		}
	})
	return out
}

// walkTree sorts paths files-first and drives v through the implied directory
// tree. This is the traversal shared by every renderer; the renderers differ
// only in the HTML their visitors emit.
func walkTree(paths []string, v treeVisitor) {
	deslug := strings.NewReplacer("-", " ", "_", " ")
	var stack []string // open directory segments (raw names, for prefix matching)
	for _, p := range sortForTree(paths) {
		segs := virtualSegments(p)
		if len(segs) == 0 {
			continue // degenerate name (e.g. just ":.md"); nothing to render
		}
		dirs, leaf := segs[:len(segs)-1], segs[len(segs)-1]

		// How many leading directory segments still match the open stack?
		common := 0
		for common < len(stack) && common < len(dirs) && stack[common] == dirs[common] {
			common++
		}
		// Close every directory we have left, deepest first.
		for i := len(stack) - 1; i >= common; i-- {
			v.leaveDir(i)
		}
		stack = stack[:common]
		// Open each newly entered directory. Names are deslugged for display but
		// kept raw on the stack so prefix matching stays exact.
		for _, dir := range dirs[common:] {
			v.enterDir(len(stack), deslug.Replace(dir))
			stack = append(stack, dir)
		}
		// Emit the leaf: real flat page name for the href, deslugged leaf label.
		v.file(len(stack), hrefName(p), deslug.Replace(leaf))
	}
	// Close whatever remains open.
	for i := len(stack) - 1; i >= 0; i-- {
		v.leaveDir(i)
	}
}

// renderTree converts a slice of markdown file paths into a nested <ul> HTML
// fragment. Directories become <li> group headers wrapping a nested <ul>;
// files become linked <li> entries, and at every level files are listed before
// subdirectories.
//
// Requires: each path is non-empty and ends with a filename whose extension is
// ".md" case-insensitively (e.g. "./a/b/name.md" or "a/b/name.md").
// Ensures:  the output contains exactly one <a> per input path; hrefs are flat
// ("./" + filename without extension); list nesting is balanced. An empty slice
// returns "".
func renderTree(paths []string) string {
	if len(paths) == 0 {
		return ""
	}

	// Indentation is cosmetic: a <li> at directory depth d (0 at the root) is
	// indented 4+4d spaces; the <ul> it opens sits two spaces deeper.
	liPad := func(depth int) string { return strings.Repeat(" ", 4+4*depth) }
	ulPad := func(depth int) string { return strings.Repeat(" ", 6+4*depth) }

	var b strings.Builder
	b.WriteString("<ul>\n")
	walkTree(paths, treeVisitor{
		enterDir: func(depth int, name string) {
			b.WriteString(liPad(depth) + "<li>" + html.EscapeString(name) + "\n")
			b.WriteString(ulPad(depth) + "<ul>\n")
		},
		leaveDir: func(depth int) {
			b.WriteString(ulPad(depth) + "</ul>\n")
			b.WriteString(liPad(depth) + "</li>\n")
		},
		file: func(depth int, href, label string) {
			b.WriteString(liPad(depth) +
				`<li><a href="./` + html.EscapeString(href) + `">` +
				html.EscapeString(label) + "</a></li>\n")
		},
	})
	b.WriteString("</ul>\n")
	return b.String()
}

// renderCollapsibleTree renders the same file set as renderTree, but wraps each
// directory in a <details>/<summary> disclosure so the navigation collapses on
// GitHub. Top-level directories start expanded (<details open>); nested ones
// start collapsed. Summaries carry a folder icon and files a page icon (numeric
// character references, which survive GitHub's sanitizer). The native <details>
// triangle — not custom CSS, which GitHub strips — is the open/closed indicator.
//
// Requires: as renderTree.
// Ensures:  exactly one <a> per input path; directories render as
// <li><details><summary>…</summary><ul>…</ul></details></li>; output begins with
// "<ul>" at column 0 and contains no blank line (both required for GitHub to
// treat the whole fragment as one raw-HTML block). An empty slice returns "".
func renderCollapsibleTree(paths []string) string {
	if len(paths) == 0 {
		return ""
	}

	// Indentation is cosmetic (GitHub ignores interior whitespace in a raw-HTML
	// block). A directory at depth d nests its <li> at tag depth 3d+1, its
	// <details> at 3d+2, and its <summary>/<ul> at 3d+3.
	pad := func(depth int) string { return strings.Repeat("  ", depth) }

	var b strings.Builder
	b.WriteString("<ul>\n")
	walkTree(paths, treeVisitor{
		enterDir: func(depth int, name string) {
			openAttr := ""
			if depth == 0 {
				openAttr = " open" // top-level starts expanded
			}
			b.WriteString(pad(3*depth+1) + "<li>\n")
			b.WriteString(pad(3*depth+2) + "<details" + openAttr + ">\n")
			b.WriteString(
				pad(
					3*depth+3,
				) + "<summary>" + folderIcon + " " + html.EscapeString(
					name,
				) + "</summary>\n",
			)
			b.WriteString(pad(3*depth+3) + "<ul>\n")
		},
		leaveDir: func(depth int) {
			b.WriteString(pad(3*depth+3) + "</ul>\n")
			b.WriteString(pad(3*depth+2) + "</details>\n")
			b.WriteString(pad(3*depth+1) + "</li>\n")
		},
		file: func(depth int, href, label string) {
			b.WriteString(pad(3*depth+1) +
				"<li>" + fileIcon + ` <a href="./` + html.EscapeString(href) + `">` +
				html.EscapeString(label) + "</a></li>\n")
		},
	})
	b.WriteString("</ul>\n")
	return b.String()
}
