package gen_test

import (
	"context"
	"flag"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/StevenACoffman/wikinavi/cmd/gen"
	"github.com/StevenACoffman/wikinavi/cmd/root"
)

var update = flag.Bool("update", false, "update golden files")

// sampleInput is deliberately unsorted so the tests also exercise the
// files-before-subdirectories ordering, and includes hyphen/underscore names to
// exercise deslugging plus colon-encoded folders.
func sampleInput() []string {
	return []string{
		"guides/advanced_topics.md",
		"getting-started.md",
		"guides/setup/install.md",
		"guides/intro.md",
		"reference/api.md",
		"zebra.md",
		// Colon-encoded folders (flat GitHub-wiki filenames) mixed with real
		// subdirectories, to exercise both structure sources together.
		"tips:slos:intro.md",
		"tips:overview.md",
	}
}

func TestRender(t *testing.T) {
	cases := map[string]struct {
		paths       []string
		collapsible bool
	}{
		"flat":              {sampleInput(), false},
		"collapsible":       {sampleInput(), true},
		"flat_empty":        {nil, false},
		"collapsible_empty": {nil, true},
	}

	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			got := gen.RenderTree(tc.paths)
			if tc.collapsible {
				got = gen.RenderCollapsibleTree(tc.paths)
			}
			checkGolden(t, name, got)
			if len(tc.paths) > 0 {
				checkBlockInvariants(t, got)
			}
		})
	}
}

func TestLessFilesFirst(t *testing.T) {
	cases := map[string]struct {
		a, b string
		want bool
	}{
		"file before dir at root":   {"zebra.md", "guides/x.md", true},
		"dir after file at root":    {"guides/x.md", "zebra.md", false},
		"two files lexicographic":   {"a.md", "b.md", true},
		"two dirs lexicographic":    {"a/x.md", "b/x.md", true},
		"nested file before subdir": {"guides/intro.md", "guides/setup/i.md", true},
		// Colon-encoded folders order identically to real ones.
		"colon file before colon subdir": {"tips:overview.md", "tips:slos:intro.md", true},
		"colon equivalent to slash":      {"a:b.md", "a/b.md", false},
	}

	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			if got := gen.LessFilesFirst(tc.a, tc.b); got != tc.want {
				t.Errorf("LessFilesFirst(%q, %q) = %v, want %v", tc.a, tc.b, got, tc.want)
			}
		})
	}
}

// The TestGen* functions drive the command through the real dispatcher path,
// covering exec's own logic end-to-end.

func TestGenBothDisabledIsError(t *testing.T) {
	err := runGen(t, t.TempDir(), "--disable-home", "--disable-sidebar")
	if err == nil || !strings.Contains(err.Error(), "nothing to generate") {
		t.Fatalf("expected 'nothing to generate' error, got %v", err)
	}
}

func TestGenGeneratesAndIsIdempotent(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "intro.md")
	writeFile(t, dir, "guides/setup.md")
	writeFile(t, dir, "README.md")      // excluded
	writeFile(t, dir, "notes.txt")      // wrong extension
	writeFile(t, dir, ".git/config.md") // dot-directory must be skipped

	if err := runGen(t, dir); err != nil {
		t.Fatalf("gen: %s", err)
	}
	sidebar := filepath.Join(dir, "_Sidebar.md")
	first := readFile(t, sidebar)
	for _, want := range []string{`href="./intro"`, `href="./setup"`} {
		if !strings.Contains(first, want) {
			t.Errorf("missing %s in output:\n%s", want, first)
		}
	}
	for _, leaked := range []string{"config", "notes", "README"} {
		if strings.Contains(first, leaked) {
			t.Errorf("excluded/dotdir/wrong-ext %q leaked into output:\n%s", leaked, first)
		}
	}

	// Re-run must replace the block in place, not stack a second one.
	if err := runGen(t, dir); err != nil {
		t.Fatalf("gen re-run: %s", err)
	}
	if n := strings.Count(readFile(t, sidebar), "<!--starttoc-->"); n != 1 {
		t.Errorf("expected exactly one marker block after re-run, got %d", n)
	}
}

func TestGenCollapsible(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "guides/setup.md")
	if err := runGen(t, dir, "--collapsible"); err != nil {
		t.Fatalf("gen: %s", err)
	}
	got := readFile(t, filepath.Join(dir, "_Sidebar.md"))
	if !strings.Contains(got, "<details") {
		t.Errorf("expected <details> in collapsible output, got:\n%s", got)
	}
}

// runGen builds and runs the gen command through the real dispatcher path.
func runGen(t *testing.T, dir string, flags ...string) error {
	t.Helper()
	r := root.New(strings.NewReader(""), io.Discard, io.Discard)
	gen.New(r)
	argv := append([]string{"gen"}, flags...)
	argv = append(argv, dir)
	if err := r.Command.Parse(argv); err != nil {
		t.Fatalf("parse: %s", err)
	}
	return r.Command.Run(context.Background())
}

func checkGolden(t *testing.T, name, got string) {
	t.Helper()
	golden := filepath.Join("testdata", name+".golden")
	if *update {
		if err := os.WriteFile(golden, []byte(got), 0o644); err != nil {
			t.Fatalf("write golden: %s", err)
		}
	}
	want, err := os.ReadFile(golden)
	if err != nil {
		t.Fatalf("read golden (run `go test -update` to create it): %s", err)
	}
	if got != string(want) {
		t.Errorf("output mismatch\n--- got ---\n%s\n--- want ---\n%s", got, want)
	}
}

// checkBlockInvariants enforces the two rules that make the fragment render as a
// single raw-HTML block on GitHub: it must open at column 0 and contain no blank
// line (a blank line terminates the block, spilling the rest as literal text).
func checkBlockInvariants(t *testing.T, got string) {
	t.Helper()
	if !strings.HasPrefix(got, "<ul>\n") {
		t.Errorf("output must start with `<ul>` at column 0, got:\n%s", got)
	}
	if strings.Contains(got, "\n\n") {
		t.Errorf("output must not contain a blank line, got:\n%s", got)
	}
}

func writeFile(t *testing.T, dir, rel string) {
	t.Helper()
	p := filepath.Join(dir, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatalf("mkdir %s: %s", rel, err)
	}
	if err := os.WriteFile(p, nil, 0o644); err != nil {
		t.Fatalf("write %s: %s", rel, err)
	}
}

func readFile(t *testing.T, p string) string {
	t.Helper()
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatalf("read %s: %s", p, err)
	}
	return string(b)
}
