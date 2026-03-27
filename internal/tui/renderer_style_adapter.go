package tui

import (
	"strings"

	"github.com/alecthomas/chroma/v2"
	chromastyles "github.com/alecthomas/chroma/v2/styles"
	glamouransi "github.com/charmbracelet/glamour/ansi"
	"github.com/charmbracelet/glamour/styles"
)

type rendererStyleAdapter struct {
	theme string
}

func newRendererStyleAdapter(theme string) rendererStyleAdapter {
	return rendererStyleAdapter{theme: normalizeTheme(theme)}
}

func (a rendererStyleAdapter) markdownConfig() glamouransi.StyleConfig {
	var cfg glamouransi.StyleConfig
	if a.theme == "light" {
		cfg = styles.LightStyleConfig
	} else {
		cfg = styles.DarkStyleConfig
	}
	foreground := themeForegroundColor(a.theme).hexString()
	zero := uint(0)
	cfg.Document.Margin = &zero
	cfg.Document.BlockPrefix = ""
	cfg.Document.BlockSuffix = ""
	cfg.Document.Color = &foreground
	cfg.Text.Color = &foreground
	cfg.CodeBlock.StylePrimitive.Color = &foreground
	if cfg.CodeBlock.Chroma != nil {
		cfg.CodeBlock.Chroma.Text.Color = &foreground
		cfg.CodeBlock.Chroma.Name.Color = &foreground
	}
	clearMarkdownBackgrounds(&cfg)
	return cfg
}

func (a rendererStyleAdapter) chromaStyle() *chroma.Style {
	return withTransparentChromaBackgrounds(a.baseChromaStyle(), chroma.MustParseColour(themeForegroundColor(a.theme).hexString()))
}

func (a rendererStyleAdapter) baseChromaStyle() *chroma.Style {
	if strings.EqualFold(strings.TrimSpace(a.theme), "light") {
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
