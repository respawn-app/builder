package app

import (
	"strings"
)

type startupFullScreenPromptSpec struct {
	Width           int
	Height          int
	Title           string
	Theme           string
	Lines           []askPromptLine
	Footer          string
	MinContentLines int
}

func renderStartupFullScreenPrompt(spec startupFullScreenPromptSpec) string {
	contentWidth := max(1, spec.Width)
	contentHeight := max(1, spec.Height)
	styles := newOnboardingStyles(spec.Theme)
	headerLines := wrapANSIText(spec.Title, contentWidth)
	footerLines := wrapStyledParagraphs(spec.Footer, contentWidth, styles.footer)
	rendered := make([]string, 0, len(spec.Lines)*2)
	for _, line := range spec.Lines {
		parts := wrapANSIText(line.Text, contentWidth)
		if len(parts) == 0 {
			parts = []string{""}
		}
		for _, part := range parts {
			switch {
			case line.Kind == askPromptLineKindHint:
				rendered = append(rendered, styles.helper.Render(padANSIRight(part, contentWidth)))
			case line.Selected:
				rendered = append(rendered, styles.optionSelected.Render(padANSIRight(part, contentWidth)))
			case line.Kind == askPromptLineKindOption:
				rendered = append(rendered, styles.option.Render(padANSIRight(part, contentWidth)))
			default:
				rendered = append(rendered, styles.body.Render(padANSIRight(part, contentWidth)))
			}
		}
	}
	separatorLines := startupPromptSeparatorCount(contentHeight, len(headerLines), len(footerLines), max(1, spec.MinContentLines))
	availableContentHeight := contentHeight - len(headerLines) - len(footerLines) - separatorLines
	if availableContentHeight < 1 {
		availableContentHeight = 1
	}
	visible := rendered
	if len(visible) > availableContentHeight {
		if startupPromptHasOptions(spec.Lines) {
			visible = visible[len(visible)-availableContentHeight:]
		} else {
			visible = visible[:availableContentHeight]
		}
	}
	var b strings.Builder
	b.WriteString(strings.Join(headerLines, "\n"))
	if separatorLines >= 1 {
		b.WriteString("\n\n")
	} else if len(visible) > 0 || len(footerLines) > 0 {
		b.WriteString("\n")
	}
	if len(visible) > 0 {
		b.WriteString(strings.Join(visible, "\n"))
	}
	if filler := availableContentHeight - len(visible); filler > 0 {
		b.WriteString(strings.Repeat("\n", filler))
	}
	if len(footerLines) > 0 {
		if separatorLines >= 2 {
			b.WriteString("\n\n")
		} else {
			b.WriteString("\n")
		}
		b.WriteString(strings.Join(footerLines, "\n"))
	}
	return b.String()
}

func startupPromptSeparatorCount(height int, headerLines int, footerLines int, minContentLines int) int {
	if height-headerLines-footerLines-2 >= minContentLines {
		return 2
	}
	if height-headerLines-footerLines-1 >= minContentLines {
		return 1
	}
	return 0
}

func startupPromptHasOptions(lines []askPromptLine) bool {
	for _, line := range lines {
		if line.Kind == askPromptLineKindOption {
			return true
		}
	}
	return false
}
