package kanban

import (
	"fmt"
	"strings"
)

// mcpPath is the path of the MCP endpoint of the pod (the dev agents reach it).
const mcpPath = "/mcp"

// TooLong refuses a text over its limit, with advice for the model that wrote it.
func TooLong(text string, limit int, advice string) error {
	if n := len([]rune(strings.TrimSpace(text))); n > limit {
		return fmt.Errorf("too long (%d characters, %d max). %s", n, limit, advice)
	}
	return nil
}

// NonEmpty is a pointer to a text, nil when it is empty (a field left as it is).
func NonEmpty(v string) *string {
	if v == "" {
		return nil
	}
	return &v
}
