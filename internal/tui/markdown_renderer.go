package tui

import (
	"fmt"
	"hash/fnv"
	"os"
	"strings"
	"sync"

	"github.com/charmbracelet/glamour"
)

const markdownCacheLimit = 1024

var markdownInitErrOnce sync.Once

type markdownRenderer struct {
	theme     string
	renderers map[int]*glamour.TermRenderer
	cache     map[string]string
}

func newMarkdownRenderer(theme string) *markdownRenderer {
	return &markdownRenderer{
		theme:     theme,
		renderers: make(map[int]*glamour.TermRenderer, 8),
		cache:     make(map[string]string, 128),
	}
}

func (r *markdownRenderer) render(role, text string, width int) (string, error) {
	if strings.TrimSpace(text) == "" {
		return "", nil
	}
	if !isMarkdownRole(role) {
		return text, nil
	}
	if width < 8 {
		width = 8
	}

	key := fmt.Sprintf("%s|%s|%d|%x", r.theme, role, width, hashString(text))
	if cached, ok := r.cache[key]; ok {
		return cached, nil
	}

	renderer, err := r.getRenderer(width)
	if err != nil {
		markdownInitErrOnce.Do(func() {
			_, _ = fmt.Fprintf(os.Stderr, "markdown renderer disabled, falling back to plain text: %v\n", err)
		})
		return "", err
	}
	out, err := renderer.Render(text)
	if err != nil {
		return "", err
	}
	out = strings.TrimRight(out, "\n")

	if len(r.cache) >= markdownCacheLimit {
		r.cache = make(map[string]string, 128)
	}
	r.cache[key] = out
	return out, nil
}

func (r *markdownRenderer) getRenderer(width int) (*glamour.TermRenderer, error) {
	if existing, ok := r.renderers[width]; ok {
		return existing, nil
	}
	termRenderer, err := glamour.NewTermRenderer(
		glamour.WithWordWrap(width),
	)
	if err != nil {
		return nil, err
	}
	r.renderers[width] = termRenderer
	return termRenderer, nil
}

func isMarkdownRole(role string) bool {
	switch strings.TrimSpace(role) {
	case "user", "assistant":
		return true
	default:
		return false
	}
}

func hashString(v string) uint64 {
	h := fnv.New64a()
	_, _ = h.Write([]byte(v))
	return h.Sum64()
}
