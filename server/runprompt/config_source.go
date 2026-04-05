package runprompt

import (
	"fmt"
	"sort"
	"strings"
)

func configSourceLines(src map[string]string) []string {
	keys := make([]string, 0, len(src))
	for k := range src {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	lines := make([]string, 0, len(keys))
	for _, k := range keys {
		lines = append(lines, fmt.Sprintf("%s=%s", k, strings.TrimSpace(src[k])))
	}
	return lines
}
