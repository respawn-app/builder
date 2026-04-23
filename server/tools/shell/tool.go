package shell

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"unicode"

	"builder/shared/config"

	xansi "github.com/charmbracelet/x/ansi"
)

const (
	defaultLimit                       = 16_000
	headTailSize                       = 1000
	truncationBannerTemplate           = "\n\n...[Output is very large, omitted %d bytes. Consider using more targeted commands to reduce output size]...\n\n"
	backgroundTruncationBannerTemplate = "\n\n...[Omitted %d bytes, read log file for details]...\n\n"
)

var shellEnvOverrides = []string{
	"TERM=dumb",
	"COLORTERM=",
	"CI=1",
	"NO_COLOR=1",
	"CLICOLOR=0",
	"CLICOLOR_FORCE=0",
	"FORCE_COLOR=0",
	"PAGER=cat",
	"GIT_PAGER=cat",
	"GH_PAGER=cat",
	"MANPAGER=cat",
	"SYSTEMD_PAGER=",
	"BAT_PAGER=cat",
	"GIT_EDITOR=:",
	"EDITOR=:",
	"VISUAL=:",
	"GIT_TERMINAL_PROMPT=0",
	"GCM_INTERACTIVE=Never",
	"DEBIAN_FRONTEND=noninteractive",
	"PY_COLORS=0",
	"CARGO_TERM_COLOR=never",
	"NPM_CONFIG_COLOR=false",
}

func marshalNoHTMLEscape(v any) (json.RawMessage, error) {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(v); err != nil {
		return nil, err
	}
	return bytes.TrimSuffix(buf.Bytes(), []byte("\n")), nil
}

func enrichEnv(base []string) []string {
	env := make(map[string]string, len(base)+len(shellEnvOverrides))
	order := make([]string, 0, len(base)+len(shellEnvOverrides))

	for _, entry := range base {
		key, value, ok := strings.Cut(entry, "=")
		if !ok || key == "" {
			continue
		}
		if _, exists := env[key]; !exists {
			order = append(order, key)
		}
		env[key] = value
	}

	for _, entry := range shellEnvOverrides {
		key, value, ok := strings.Cut(entry, "=")
		if !ok || key == "" {
			continue
		}
		if _, exists := env[key]; !exists {
			order = append(order, key)
		}
		env[key] = value
	}

	if _, exists := env["RIPGREP_CONFIG_PATH"]; !exists {
		if path, ok := managedRGConfigEnvValue(); ok {
			order = append(order, "RIPGREP_CONFIG_PATH")
			env["RIPGREP_CONFIG_PATH"] = path
		}
	}

	out := make([]string, 0, len(order))
	for _, key := range order {
		out = append(out, key+"="+env[key])
	}
	return out
}

func managedRGConfigEnvValue() (string, bool) {
	path, err := config.ResolveManagedRGConfigPath()
	if err != nil || strings.TrimSpace(path) == "" {
		return "", false
	}
	if _, err := os.Stat(path); err != nil {
		return "", false
	}
	return path, true
}

func sanitizeOutput(s string) string {
	if s == "" {
		return s
	}

	stripped := xansi.Strip(s)
	normalized := strings.ReplaceAll(stripped, "\r\n", "\n")
	normalized = strings.ReplaceAll(normalized, "\r", "\n")

	var b strings.Builder
	b.Grow(len(normalized))
	for _, r := range normalized {
		if r == '\n' || r == '\t' || !unicode.IsControl(r) {
			b.WriteRune(r)
		}
	}
	return b.String()
}

func truncate(s string, maxLen int) (string, bool, int) {
	return truncateWithTemplate(s, maxLen, truncationBannerTemplate)
}

func truncateBackgroundOutput(s string, maxLen int) (string, bool, int) {
	return truncateWithTemplate(s, maxLen, backgroundTruncationBannerTemplate)
}

func truncateWithTemplate(s string, maxLen int, bannerTemplate string) (string, bool, int) {
	if len(s) <= maxLen {
		return s, false, 0
	}
	headLen, tailLen := truncationSegmentLengths(len(s), maxLen)
	removed := len(s) - headLen - tailLen
	return formatTruncatedPreviewWithTemplate(s[:headLen], removed, s[len(s)-tailLen:], bannerTemplate), true, removed
}

func truncationSegmentLengths(total int, maxLen int) (int, int) {
	if total <= 1 {
		return total, 0
	}
	maxPreserve := min(total-1, headTailSize*2)
	preserve := maxPreserve
	if maxLen > 0 {
		preserve = min(maxPreserve, maxLen)
	}
	if preserve < 2 {
		preserve = min(total-1, 2)
	}
	head := preserve / 2
	tail := preserve - head
	if head <= 0 {
		head = 1
		tail = preserve - head
	}
	if tail <= 0 {
		tail = 1
		head = preserve - tail
	}
	if head > headTailSize {
		head = headTailSize
	}
	if tail > headTailSize {
		tail = headTailSize
	}
	return head, tail
}

func truncationBannerLen(removed int) int {
	return truncationBannerLenWithTemplate(truncationBannerTemplate, removed)
}

func backgroundTruncationBannerLen(removed int) int {
	return truncationBannerLenWithTemplate(backgroundTruncationBannerTemplate, removed)
}

func truncationBannerLenWithTemplate(bannerTemplate string, removed int) int {
	return len(fmt.Sprintf(bannerTemplate, removed))
}

func formatTruncatedPreview(head string, removed int, tail string) string {
	return formatTruncatedPreviewWithTemplate(head, removed, tail, truncationBannerTemplate)
}

func formatBackgroundTruncatedPreview(head string, removed int, tail string) string {
	return formatTruncatedPreviewWithTemplate(head, removed, tail, backgroundTruncationBannerTemplate)
}

func formatTruncatedPreviewWithTemplate(head string, removed int, tail string, bannerTemplate string) string {
	return fmt.Sprintf("%s%s%s", head, fmt.Sprintf(bannerTemplate, removed), tail)
}
