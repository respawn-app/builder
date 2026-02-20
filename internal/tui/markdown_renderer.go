package tui

import (
	"fmt"
	"hash/fnv"
	"os"
	"strings"
	"sync"

	"github.com/charmbracelet/glamour"
	glamouransi "github.com/charmbracelet/glamour/ansi"
	"github.com/charmbracelet/glamour/styles"
	xansi "github.com/charmbracelet/x/ansi"
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
	if width < 1 {
		width = 1
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
	out = xansi.Wordwrap(out, width, " ,.;-+|")

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
		glamour.WithWordWrap(0),
		glamour.WithStyles(r.styleConfig()),
	)
	if err != nil {
		return nil, err
	}
	r.renderers[width] = termRenderer
	return termRenderer, nil
}

func (r *markdownRenderer) styleConfig() glamouransi.StyleConfig {
	var cfg glamouransi.StyleConfig
	if strings.EqualFold(strings.TrimSpace(r.theme), "light") {
		cfg = styles.LightStyleConfig
	} else {
		cfg = styles.DarkStyleConfig
	}
	zero := uint(0)
	cfg.Document.Margin = &zero
	cfg.Document.BlockPrefix = ""
	cfg.Document.BlockSuffix = ""
	cfg.Code.BackgroundColor = nil
	return cfg
}

func isMarkdownRole(role string) bool {
	switch strings.TrimSpace(role) {
	case "user", "assistant", "assistant_commentary", "reasoning":
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
