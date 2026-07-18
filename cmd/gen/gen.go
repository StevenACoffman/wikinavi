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

	"github.com/StevenACoffman/wikinavi/cmd/root"
	"github.com/peterbourgon/ff/v4"
)

const (
	startMarker = "<!--starttoc-->"
	endMarker   = "<!--endtoc-->"
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
	Flags          *ff.FlagSet
	Command        *ff.Command
}

// New creates and registers the gen command with the given parent config.
func New(parent *root.Config) *Config {
	var cfg Config
	cfg.Config = parent
	cfg.Flags = ff.NewFlagSet("gen").SetParent(parent.Flags)
	cfg.Flags.BoolVar(&cfg.DisableHome, 0, "disable-home", "disable Home.md TOC injection")
	cfg.Flags.BoolVar(&cfg.DisableSidebar, 0, "disable-sidebar", "disable _Sidebar.md TOC injection")
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

func (cfg *Config) exec(_ context.Context, args []string) error {
	l := cfg.logger()

	dir := "."
	if len(args) > 0 {
		dir = args[0]
	}

	var targetFiles []string
	if !cfg.DisableHome {
		targetFiles = append(targetFiles, "Home.md")
	}
	if !cfg.DisableSidebar {
		targetFiles = append(targetFiles, "_Sidebar.md")
	}
	if len(targetFiles) == 0 {
		return errors.New("gen: --disable-home and --disable-sidebar both set; nothing to generate")
	}

	if err := initializeFiles(targetFiles); err != nil {
		return fmt.Errorf("gen: %w", err)
	}

	files, err := listFiles(l, dir, ".md")
	if err != nil {
		return fmt.Errorf("gen: %w", err)
	}
	l.Debug("collected markdown files", "count", len(files), "files", files)

	toc := renderTree(files)
	l.Debug("rendered table of contents", "toc", toc)

	l.Info("injecting table of contents", "targets", targetFiles)
	if err := updateFiles(l, targetFiles, toc); err != nil {
		return fmt.Errorf("gen: %w", err)
	}

	l.Info("injection complete; run 'wikinavi push' to publish your changes")
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
func listFiles(l *slog.Logger, dir, extension string) ([]string, error) {
	exclusions := []string{"Home.md", "_Sidebar.md", "README.md"}

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
		if slices.Contains(exclusions, base) {
			l.Debug("ignoring excluded file", "path", p)
			return nil
		}
		if filepath.Ext(p) != extension {
			l.Debug("ignoring non-matching file", "path", p, "ext", filepath.Ext(p))
			return nil
		}
		l.Debug("appending file", "path", p)
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

func updateFiles(l *slog.Logger, files []string, toc string) error {
	for _, f := range files {
		if err := updateFile(l, f, toc); err != nil {
			return err
		}
	}
	return nil
}

// updateFile replaces the existing marker block in file with toc, or prepends a
// fresh block when no markers are present.
func updateFile(l *slog.Logger, file, toc string) error {
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

	l.Debug("writing file", "path", file, "bytes", len(output))
	if err := os.WriteFile(file, []byte(output), 0o644); err != nil {
		return fmt.Errorf("write %s: %w", file, err)
	}
	return nil
}

// lessFilesFirst orders paths so that, at every directory level, files sort
// ahead of subdirectories; ties within the same kind stay lexicographic. It is
// equivalent to comparing each path's segments where a file segment ranks
// before a directory segment at the same position — a consistent total order.
func lessFilesFirst(a, b string) bool {
	as := strings.Split(strings.TrimPrefix(a, "./"), "/")
	bs := strings.Split(strings.TrimPrefix(b, "./"), "/")
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

	// Reorder to "files before subdirectories at each level" without mutating
	// the caller's slice; the walk below relies only on each subtree being
	// contiguous, which this ordering preserves.
	paths = slices.Clone(paths)
	slices.SortStableFunc(paths, func(a, b string) int {
		switch {
		case a == b:
			return 0
		case lessFilesFirst(a, b):
			return -1
		default:
			return 1
		}
	})

	deslug := strings.NewReplacer("-", " ", "_", " ")
	// Indentation is cosmetic: a <li> at directory depth d (0 at the root) is
	// indented 4+4d spaces; the <ul> it opens sits two spaces deeper.
	liPad := func(depth int) string { return strings.Repeat(" ", 4+4*depth) }
	ulPad := func(depth int) string { return strings.Repeat(" ", 6+4*depth) }

	var b strings.Builder
	var stack []string // open directory segments, root -> deepest

	b.WriteString("<ul>\n")
	for _, p := range paths {
		segs := strings.Split(strings.TrimPrefix(p, "./"), "/")
		dirs, file := segs[:len(segs)-1], segs[len(segs)-1]

		// How many leading directory segments still match the open stack?
		common := 0
		for common < len(stack) && common < len(dirs) && stack[common] == dirs[common] {
			common++
		}

		// Close every directory we have left, deepest first.
		for i := len(stack) - 1; i >= common; i-- {
			b.WriteString(ulPad(i) + "</ul>\n")
			b.WriteString(liPad(i) + "</li>\n")
		}
		stack = stack[:common]

		// Open each newly entered directory.
		for _, dir := range dirs[common:] {
			b.WriteString(liPad(len(stack)) + "<li>" + html.EscapeString(dir) + "\n")
			b.WriteString(ulPad(len(stack)) + "<ul>\n")
			stack = append(stack, dir)
		}

		// Emit the file link. The ".md" (any case) extension is dropped, and
		// '-'/'_' become spaces for the visible label.
		name := file[:len(file)-len(path.Ext(file))]
		b.WriteString(liPad(len(stack)) +
			`<li><a href="./` + html.EscapeString(name) + `">` +
			html.EscapeString(deslug.Replace(name)) + "</a></li>\n")
	}

	// Close whatever remains open.
	for i := len(stack) - 1; i >= 0; i-- {
		b.WriteString(ulPad(i) + "</ul>\n")
		b.WriteString(liPad(i) + "</li>\n")
	}
	b.WriteString("</ul>\n")
	return b.String()
}
