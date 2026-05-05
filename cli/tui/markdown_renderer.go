package tui

import (
	"fmt"
	"hash/fnv"
	"strings"

	"github.com/charmbracelet/glamour"
	glamouransi "github.com/charmbracelet/glamour/ansi"
)

const markdownCacheLimit = 1024

type markdownRendererErrorReporter func(RenderDiagnostic)

type markdownRenderer struct {
	theme               string
	styles              rendererStyleAdapter
	renderers           map[int]*glamour.TermRenderer
	cache               map[string]string
	reportErr           markdownRendererErrorReporter
	newTermRenderer     func(...glamour.TermRendererOption) (*glamour.TermRenderer, error)
	reportedInitFailure bool
}

func newMarkdownRenderer(theme string, reportErr markdownRendererErrorReporter) *markdownRenderer {
	return &markdownRenderer{
		theme:           theme,
		styles:          newRendererStyleAdapter(theme),
		renderers:       make(map[int]*glamour.TermRenderer, 8),
		cache:           make(map[string]string, 128),
		reportErr:       reportErr,
		newTermRenderer: glamour.NewTermRenderer,
	}
}

func (r *markdownRenderer) render(role RenderIntent, text string, width int) (string, error) {
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
		if !r.reportedInitFailure && r.reportErr != nil {
			r.reportedInitFailure = true
			r.reportErr(RenderDiagnostic{
				Component: "markdown_renderer",
				Message:   fmt.Sprintf("markdown renderer disabled, falling back to plain text: %v", err),
				Err:       err,
				Severity:  RenderDiagnosticSeverityWarn,
			})
		}
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
	termRenderer, err := r.newTermRenderer(
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
	return r.styles.markdownConfig()
}

func isMarkdownRole(role RenderIntent) bool {
	switch role {
	case RenderIntentUser, RenderIntentAssistant, RenderIntentAssistantCommentary, RenderIntentToolQuestion, RenderIntentToolQuestionError:
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
