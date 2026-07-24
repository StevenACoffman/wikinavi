package gen

// Test-only accessors so the black-box gen_test package can exercise the pure
// tree builders directly without widening the production API surface.

func RenderTree(initialisms string, paths []string) string {
	return renderTree(newCaser(initialisms), paths)
}

func RenderCollapsibleTree(initialisms string, paths []string) string {
	return renderCollapsibleTree(newCaser(initialisms), paths)
}

func LessFilesFirst(a, b string) bool { return lessFilesFirst(a, b) }
