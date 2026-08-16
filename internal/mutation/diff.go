package mutation

import (
	"bytes"
	"fmt"
	"strconv"
	"strings"
)

// UnifiedDiff renders the change between two versions of a file as a minimal
// unified diff. Formatted Terraform sources yield a single-line hunk, which is
// what keeps survivor reports readable.
func UnifiedDiff(path string, original, mutated []byte) string {
	originalLines := splitLines(original)
	mutatedLines := splitLines(mutated)

	prefix := 0
	for prefix < len(originalLines) && prefix < len(mutatedLines) &&
		originalLines[prefix] == mutatedLines[prefix] {
		prefix++
	}

	suffix := 0
	for suffix < len(originalLines)-prefix && suffix < len(mutatedLines)-prefix &&
		originalLines[len(originalLines)-1-suffix] == mutatedLines[len(mutatedLines)-1-suffix] {
		suffix++
	}

	removed := originalLines[prefix : len(originalLines)-suffix]
	added := mutatedLines[prefix : len(mutatedLines)-suffix]

	if len(removed) == 0 && len(added) == 0 {
		return ""
	}

	builder := strings.Builder{}
	fmt.Fprintf(&builder, "--- a/%s\n+++ b/%s\n", path, path)
	fmt.Fprintf(&builder, "@@ -%s +%s @@\n", hunk(prefix, len(removed)), hunk(prefix, len(added)))

	for _, line := range removed {
		builder.WriteString("-" + line + "\n")
	}

	for _, line := range added {
		builder.WriteString("+" + line + "\n")
	}

	return builder.String()
}

func hunk(start, length int) string {
	if length == 0 {
		return fmt.Sprintf("%d,0", start)
	}

	if length == 1 {
		return strconv.Itoa(start + 1)
	}

	return fmt.Sprintf("%d,%d", start+1, length)
}

func splitLines(content []byte) []string {
	trimmed := bytes.TrimSuffix(content, []byte("\n"))
	if len(trimmed) == 0 {
		return []string{}
	}

	return strings.Split(string(trimmed), "\n")
}
