package gen

// Test-only accessors so the black-box gen_test package can exercise the pure
// tree builders directly without widening the production API surface.

func RenderTree(paths []string) string { return renderTree(paths) }

func RenderCollapsibleTree(paths []string) string { return renderCollapsibleTree(paths) }

func LessFilesFirst(a, b string) bool { return lessFilesFirst(a, b) }
