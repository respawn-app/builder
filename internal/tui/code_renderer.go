package tui

import (
	"bytes"
	"fmt"
	"strconv"
	"strings"

	"builder/internal/transcript"
	"github.com/alecthomas/chroma/v2"
	"github.com/alecthomas/chroma/v2/formatters"
	"github.com/alecthomas/chroma/v2/lexers"
	chromastyles "github.com/alecthomas/chroma/v2/styles"
)

const codeCacheLimit = 512

const (
	diffBlockMaxLines = 400
	diffBlockMaxBytes = 64 * 1024
)

type codeRenderer struct {
	theme     string
	cache     map[string]string
	diffCache map[string][]diffRenderedLine
	formatter chroma.Formatter
}

type diffRenderKind string

const (
	diffRenderMeta    diffRenderKind = "meta"
	diffRenderAdd     diffRenderKind = "add"
	diffRenderRemove  diffRenderKind = "remove"
	diffRenderContext diffRenderKind = "context"
)

type diffRenderedLine struct {
	Kind diffRenderKind
	Text string
}

func newCodeRenderer(theme string) *codeRenderer {
	return &codeRenderer{
		theme:     theme,
		cache:     make(map[string]string, 128),
		diffCache: make(map[string][]diffRenderedLine, 64),
		formatter: formatters.TTY256,
	}
}

func (r *codeRenderer) render(hint *transcript.ToolRenderHint, text string) (string, bool) {
	if hint == nil || !hint.Valid() || strings.TrimSpace(text) == "" {
		return "", false
	}
	if hint.Kind == transcript.ToolRenderKindDiff {
		return "", false
	}
	key := fmt.Sprintf("%s|%s|%s|%t|%x", r.theme, hint.Kind, hint.Path, hint.ResultOnly, hashString(text))
	if cached, ok := r.cache[key]; ok {
		return cached, true
	}

	lexer := r.resolveLexer(hint, text)
	if lexer == nil {
		return "", false
	}
	iterator, err := chroma.Coalesce(lexer).Tokenise(nil, text)
	if err != nil {
		return "", false
	}

	var out bytes.Buffer
	if err := r.formatter.Format(&out, r.style(), iterator); err != nil {
		return "", false
	}
	rendered := strings.TrimRight(out.String(), "\n")
	if strings.TrimSpace(rendered) == "" {
		return "", false
	}

	if len(r.cache) >= codeCacheLimit {
		r.cache = make(map[string]string, 128)
	}
	r.cache[key] = rendered
	return rendered, true
}

func (r *codeRenderer) renderDiffLines(text string, width int) ([]diffRenderedLine, bool) {
	if strings.TrimSpace(text) == "" {
		return nil, false
	}
	if width < 1 {
		width = 1
	}
	key := fmt.Sprintf("%s|diff|w=%d|%x", r.theme, width, hashString(text))
	if cached, ok := r.diffCache[key]; ok {
		return append([]diffRenderedLine(nil), cached...), true
	}
	lines := strings.Split(strings.ReplaceAll(text, "\r\n", "\n"), "\n")
	out := make([]diffRenderedLine, 0, len(lines))
	var currentLexer chroma.Lexer
	var inferredLexer chroma.Lexer
	type pendingCodeLine struct {
		kind diffRenderKind
		text string
	}
	pending := make([]pendingCodeLine, 0, 16)
	pendingBytes := 0
	flushPending := func() {
		if len(pending) == 0 {
			return
		}
		plainLines := make([]string, 0, len(pending))
		for _, line := range pending {
			plainLines = append(plainLines, line.text)
		}
		source := strings.Join(plainLines, "\n")
		lexer := currentLexer
		if lexer == nil {
			if inferredLexer == nil {
				inferredLexer = lexers.Analyse(source)
			}
			lexer = inferredLexer
		}
		highlightedLines := r.highlightCodeBlock(lexer, source)
		if len(highlightedLines) != len(pending) {
			highlightedLines = plainLines
		}
		for idx, line := range pending {
			marker := " "
			switch line.kind {
			case diffRenderAdd:
				marker = "+"
			case diffRenderRemove:
				marker = "-"
			}
			content := highlightedLines[idx]
			wrapped := splitLines(wrapTextForViewport(content, max(1, width-1)))
			for chunkIdx, chunk := range wrapped {
				prefix := marker
				if chunkIdx > 0 {
					prefix = " "
				}
				out = append(out, diffRenderedLine{Kind: line.kind, Text: prefix + chunk})
			}
		}
		pending = pending[:0]
		pendingBytes = 0
	}
	appendPending := func(kind diffRenderKind, text string) {
		pending = append(pending, pendingCodeLine{kind: kind, text: text})
		pendingBytes += len(text) + 1
		if len(pending) >= diffBlockMaxLines || pendingBytes >= diffBlockMaxBytes {
			flushPending()
		}
	}
	for _, line := range lines {
		if path, ok := detectDiffPath(line); ok {
			flushPending()
			if lexer := lexers.Match(path); lexer != nil {
				currentLexer = lexer
				inferredLexer = nil
			}
			for _, chunk := range splitLines(wrapTextForViewport(line, width)) {
				out = append(out, diffRenderedLine{Kind: diffRenderMeta, Text: chunk})
			}
			continue
		}
		if strings.HasPrefix(line, "+") && !strings.HasPrefix(line, "+++") {
			appendPending(diffRenderAdd, line[1:])
			continue
		}
		if strings.HasPrefix(line, "-") && !strings.HasPrefix(line, "---") {
			appendPending(diffRenderRemove, line[1:])
			continue
		}
		if strings.HasPrefix(line, " ") {
			appendPending(diffRenderContext, line[1:])
			continue
		}
		flushPending()
		for _, chunk := range splitLines(wrapTextForViewport(line, width)) {
			out = append(out, diffRenderedLine{Kind: diffRenderMeta, Text: chunk})
		}
	}
	flushPending()
	serialized := make([]string, 0, len(out))
	for _, line := range out {
		serialized = append(serialized, line.Text)
	}
	rendered := strings.TrimRight(strings.Join(serialized, "\n"), "\n")
	if strings.TrimSpace(rendered) == "" {
		return nil, false
	}
	if len(r.diffCache) >= codeCacheLimit {
		r.diffCache = make(map[string][]diffRenderedLine, 64)
	}
	r.diffCache[key] = append([]diffRenderedLine(nil), out...)
	return append([]diffRenderedLine(nil), out...), true
}

func (r *codeRenderer) resolveLexer(hint *transcript.ToolRenderHint, text string) chroma.Lexer {
	switch hint.Kind {
	case transcript.ToolRenderKindShell:
		return lexers.Get("bash")
	case transcript.ToolRenderKindDiff:
		return lexers.Get("diff")
	case transcript.ToolRenderKindSource:
		if pathHint := strings.TrimSpace(hint.Path); pathHint != "" {
			if lexer := lexers.Match(pathHint); lexer != nil {
				return lexer
			}
		}
		return lexers.Analyse(text)
	default:
		return nil
	}
}

func (r *codeRenderer) highlightCodeBlock(lexer chroma.Lexer, source string) []string {
	sourceLines := splitLines(source)
	if lexer == nil || source == "" {
		return sourceLines
	}
	iterator, err := chroma.Coalesce(lexer).Tokenise(nil, source)
	if err != nil {
		return sourceLines
	}
	var out bytes.Buffer
	if err := r.formatter.Format(&out, r.style(), iterator); err != nil {
		return sourceLines
	}
	raw := strings.ReplaceAll(out.String(), "\r\n", "\n")
	highlighted := strings.Split(raw, "\n")
	if len(highlighted) == len(sourceLines)+1 && highlighted[len(highlighted)-1] == "" {
		highlighted = highlighted[:len(highlighted)-1]
	}
	if len(highlighted) < len(sourceLines) {
		padded := make([]string, len(sourceLines))
		copy(padded, highlighted)
		for idx := len(highlighted); idx < len(sourceLines); idx++ {
			padded[idx] = sourceLines[idx]
		}
		return padded
	}
	if len(highlighted) > len(sourceLines) {
		highlighted = highlighted[:len(sourceLines)]
	}
	return highlighted
}

func detectDiffPath(line string) (string, bool) {
	trimmed := strings.TrimSpace(line)
	if trimmed == "" {
		return "", false
	}
	if strings.HasPrefix(trimmed, "diff --git ") {
		parts := strings.Fields(trimmed)
		if len(parts) >= 4 {
			path := strings.TrimPrefix(parts[3], "b/")
			if path != "" && path != "/dev/null" {
				return path, true
			}
		}
		return "", false
	}
	if strings.HasPrefix(trimmed, "+++") || strings.HasPrefix(trimmed, "---") {
		path := strings.TrimSpace(trimmed[3:])
		path = strings.TrimPrefix(path, "a/")
		path = strings.TrimPrefix(path, "b/")
		if path == "" || path == "/dev/null" {
			return "", false
		}
		return path, true
	}
	if strings.HasPrefix(trimmed, "Edited:") {
		rest := strings.TrimSpace(trimmed[len("Edited:"):])
		if rest == "" {
			return "", false
		}
		if strings.HasPrefix(rest, "./") || strings.HasPrefix(rest, "../") || strings.HasPrefix(rest, "/") {
			normalized := normalizeDiffPathLine(rest)
			if normalized == "" {
				return "", false
			}
			return normalized, true
		}
		return "", false
	}
	if strings.HasPrefix(trimmed, "@@") {
		return "", false
	}
	if strings.HasPrefix(trimmed, "+") || strings.HasPrefix(trimmed, "-") {
		return "", false
	}
	if strings.HasPrefix(trimmed, "./") || strings.HasPrefix(trimmed, "../") || strings.HasPrefix(trimmed, "/") {
		normalized := normalizeDiffPathLine(trimmed)
		if normalized == "" {
			return "", false
		}
		return normalized, true
	}
	return "", false
}

func normalizeDiffPathLine(line string) string {
	trimmed := strings.TrimSpace(line)
	if trimmed == "" {
		return ""
	}
	parts := strings.Fields(trimmed)
	if len(parts) < 2 {
		return trimmed
	}
	end := len(parts)
	for end > 1 && isPatchCountToken(parts[end-1]) {
		end--
	}
	if end < len(parts) {
		candidate := strings.Join(parts[:end], " ")
		if candidate != "" {
			return candidate
		}
	}
	return trimmed
}

func isPatchCountToken(token string) bool {
	if len(token) < 2 {
		return false
	}
	if token[0] != '+' && token[0] != '-' {
		return false
	}
	for i := 1; i < len(token); i++ {
		if token[i] < '0' || token[i] > '9' {
			return false
		}
	}
	return true
}

func applyBackgroundTint(line string, bg string) string {
	if line == "" || bg == "" {
		return line
	}
	body := strings.ReplaceAll(line, "\x1b[0m", "\x1b[0m"+bg)
	return bg + body + "\x1b[0m"
}

func bgEscape(hex string) string {
	r, g, b, ok := parseHexColor(hex)
	if !ok {
		return ""
	}
	return fmt.Sprintf("\x1b[48;2;%d;%d;%dm", r, g, b)
}

func parseHexColor(hex string) (int, int, int, bool) {
	v := strings.TrimPrefix(strings.TrimSpace(hex), "#")
	if len(v) != 6 {
		return 0, 0, 0, false
	}
	raw, err := strconv.ParseUint(v, 16, 24)
	if err != nil {
		return 0, 0, 0, false
	}
	r := int((raw >> 16) & 0xFF)
	g := int((raw >> 8) & 0xFF)
	b := int(raw & 0xFF)
	return r, g, b, true
}

func (r *codeRenderer) style() *chroma.Style {
	if strings.EqualFold(strings.TrimSpace(r.theme), "light") {
		if style := chromastyles.Get("github"); style != nil {
			return style
		}
		if style := chromastyles.Get("friendly"); style != nil {
			return style
		}
	}
	if style := chromastyles.Get("github-dark"); style != nil {
		return style
	}
	if style := chromastyles.Get("monokai"); style != nil {
		return style
	}
	return chromastyles.Fallback
}
