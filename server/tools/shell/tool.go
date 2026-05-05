package shell

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"
	"unicode"

	"builder/server/tools/shell/shellenv"

	xansi "github.com/charmbracelet/x/ansi"
)

const (
	defaultLimit                       = 16_000
	headTailSize                       = 1000
	truncationBannerTemplate           = "\n\n...[Output is very large, omitted %d bytes. Consider using more targeted commands to reduce output size]...\n\n"
	backgroundTruncationBannerTemplate = "\n\n...[Omitted %d bytes, read log file for details]...\n\n"
)

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
	return enrichEnvForSession(base, "")
}

func enrichEnvForSession(base []string, sessionID string) []string {
	return shellenv.EnrichForSession(base, sessionID)
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
