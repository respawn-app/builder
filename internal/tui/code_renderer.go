package tui

import (
	"bytes"
	"fmt"
	"strings"

	"builder/internal/transcript"
	"github.com/alecthomas/chroma/v2"
	"github.com/alecthomas/chroma/v2/formatters"
	"github.com/alecthomas/chroma/v2/lexers"
	chromastyles "github.com/alecthomas/chroma/v2/styles"
)

const codeCacheLimit = 512

type codeRenderer struct {
	theme     string
	cache     map[string]string
	formatter chroma.Formatter
}

func newCodeRenderer(theme string) *codeRenderer {
	return &codeRenderer{
		theme:     theme,
		cache:     make(map[string]string, 128),
		formatter: formatters.TTY256,
	}
}

func (r *codeRenderer) render(hint *transcript.ToolRenderHint, text string) (string, bool) {
	if hint == nil || !hint.Valid() || strings.TrimSpace(text) == "" {
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

func (r *codeRenderer) resolveLexer(hint *transcript.ToolRenderHint, text string) chroma.Lexer {
	switch hint.Kind {
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
