package app

import (
	"fmt"
	"math"
	"strings"

	"builder/internal/llm"

	bubbleprogress "github.com/charmbracelet/bubbles/progress"
	"github.com/charmbracelet/lipgloss"
)

func (l uiViewLayout) renderStatusLine(width int, style uiStyles) string {
	m := l.model
	spin := renderStatusDot(m.theme, m.activity, m.spinnerFrame)
	if m.reviewerRunning {
		spin = renderReviewerStatus()
	} else if m.compacting {
		spin = renderCompactionStatus()
	}
	segments := []string{
		spin,
		style.meta.Render(string(m.view.Mode())),
		style.meta.Render(l.statusModelLabel()),
	}
	if label := processCountLabel(m.backgroundManager); label != "" {
		segments = append(segments, style.meta.Render(label))
	}
	if cacheSection := l.renderCacheHitSection(style); cacheSection != "" {
		segments = append(segments, cacheSection)
	}
	separator := style.meta.Render(" | ")
	left := strings.Join(segments, separator)
	right := l.renderStatusLineRight(width, left, style)
	if right == "" {
		return padANSIRight(left, width)
	}
	gap := width - lipgloss.Width(left) - lipgloss.Width(right)
	if gap < 1 {
		gap = 1
	}
	return padANSIRight(left+strings.Repeat(" ", gap)+right, width)
}

func (l uiViewLayout) renderStatusLineRight(width int, left string, style uiStyles) string {
	separator := style.meta.Render(" | ")
	separatorWidth := lipgloss.Width(separator)
	available := width - lipgloss.Width(left) - 1
	if available <= 0 {
		return ""
	}
	segments := make([]string, 0, 3)
	used := 0
	prepend := func(segment string) {
		if segment == "" {
			return
		}
		segmentWidth := lipgloss.Width(segment)
		if segmentWidth == 0 {
			return
		}
		additional := segmentWidth
		if len(segments) > 0 {
			additional += separatorWidth
		}
		if used+additional > available {
			return
		}
		used += additional
		segments = append([]string{segment}, segments...)
	}

	prepend(l.renderContextUsage(style))

	headerAvailable := available - used
	if len(segments) > 0 {
		headerAvailable -= separatorWidth
	}
	prepend(l.renderActivityStatus(headerAvailable, style))

	noticeAvailable := available - used
	if len(segments) > 0 {
		noticeAvailable -= separatorWidth
	}
	prepend(l.renderStatusNotice(noticeAvailable))

	return strings.Join(segments, separator)
}

func (l uiViewLayout) renderStatusNotice(available int) string {
	m := l.model
	text := strings.TrimSpace(m.transientStatus)
	if text == "" || available <= 0 {
		return ""
	}
	text = truncateQueuedMessageLine(text, available)
	return statusNoticeStyle(m.theme, m.transientStatusKind).Render(text)
}

func (l uiViewLayout) renderActivityStatus(available int, style uiStyles) string {
	if available <= 0 {
		return ""
	}
	if text := strings.TrimSpace(l.model.reasoningStatusHeader); text != "" {
		text = truncateQueuedMessageLine(text, available)
		return statusNoticeStyle(l.model.theme, uiStatusNoticeNeutral).Render(text)
	}
	if strings.TrimSpace(l.model.transientStatus) != "" {
		return ""
	}
	if !l.shouldRenderHelpHint() {
		return ""
	}
	return style.meta.Render(truncateQueuedMessageLine(l.model.statusHelpHint(), available))
}

func (l uiViewLayout) shouldRenderHelpHint() bool {
	m := l.model
	if !m.canShowHelp() || m.helpVisible {
		return false
	}
	if m.busy || m.compacting || m.reviewerRunning {
		return false
	}
	return m.activity == uiActivityIdle
}

func statusNoticeStyle(theme string, kind uiStatusNoticeKind) lipgloss.Style {
	palette := uiPalette(theme)
	color := palette.primary
	switch kind {
	case uiStatusNoticeSuccess:
		color = palette.secondary
	case uiStatusNoticeError:
		color = statusRedColor()
	}
	return lipgloss.NewStyle().Foreground(color).Bold(true)
}

func (l uiViewLayout) statusModelLabel() string {
	m := l.model
	label := llm.ModelDisplayLabel(m.modelName, m.thinkingLevel)
	if m.fastModeAvailable && m.fastModeEnabled {
		label += " fast"
	}
	if !m.shouldShowModelLockedLabel() {
		return label
	}
	return label + " (model locked)"
}

func (m *uiModel) shouldShowModelLockedLabel() bool {
	if !m.modelContractLocked {
		return false
	}
	return strings.TrimSpace(m.modelName) != strings.TrimSpace(m.configuredModelName)
}

func (l uiViewLayout) renderCacheHitSection(style uiStyles) string {
	m := l.model
	if m.engine == nil {
		return ""
	}
	usage := m.engine.ContextUsage()
	if !usage.HasCacheHitPercentage {
		return style.meta.Render("cache --")
	}
	return style.meta.Render(fmt.Sprintf("cache %d%%", usage.CacheHitPercent))
}

func (l uiViewLayout) renderContextUsage(style uiStyles) string {
	m := l.model
	if m.engine == nil {
		return ""
	}
	usage := m.engine.ContextUsage()
	if usage.WindowTokens <= 0 {
		return ""
	}
	used := usage.UsedTokens
	if used < 0 {
		used = 0
	}
	rawPercent := int(math.Round((float64(used) * 100) / float64(usage.WindowTokens)))
	barPercent := rawPercent
	if barPercent < 0 {
		barPercent = 0
	}
	if barPercent > 100 {
		barPercent = 100
	}
	barProgress := bubbleprogress.New(
		bubbleprogress.WithWidth(statusContextBarWidth),
		bubbleprogress.WithoutPercentage(),
		bubbleprogress.WithSolidFill(statusContextZoneHex(m.theme, rawPercent)),
		bubbleprogress.WithFillCharacters('▮', '▯'),
	)
	barProgress.EmptyColor = statusContextEmptyHex(m.theme)
	bar := barProgress.ViewAs(float64(barPercent) / 100.0)
	label := style.meta.Render(fmt.Sprintf("%d%%", rawPercent))
	return label + " " + bar
}

func statusContextZoneHex(theme string, percent int) string {
	if strings.EqualFold(strings.TrimSpace(theme), "light") {
		if percent < 50 {
			return "#22863A"
		}
		if percent < 80 {
			return "#9A6700"
		}
		return "#CB2431"
	}
	if percent < 50 {
		return "#98C379"
	}
	if percent < 80 {
		return "#E5C07B"
	}
	return "#F97583"
}

func statusContextEmptyHex(theme string) string {
	if strings.EqualFold(strings.TrimSpace(theme), "light") {
		return "#A0A1A7"
	}
	return "#5C6370"
}

func statusContextZoneColor(percent int) lipgloss.TerminalColor {
	if percent < 50 {
		return statusGreenColor()
	}
	if percent < 80 {
		return statusAmberColor()
	}
	return statusRedColor()
}

func statusGreenColor() lipgloss.CompleteAdaptiveColor {
	return lipgloss.CompleteAdaptiveColor{
		Light: lipgloss.CompleteColor{ANSI: "2", ANSI256: "34", TrueColor: "#22863A"},
		Dark:  lipgloss.CompleteColor{ANSI: "2", ANSI256: "114", TrueColor: "#98C379"},
	}
}

func statusAmberColor() lipgloss.CompleteAdaptiveColor {
	return lipgloss.CompleteAdaptiveColor{
		Light: lipgloss.CompleteColor{ANSI: "3", ANSI256: "136", TrueColor: "#9A6700"},
		Dark:  lipgloss.CompleteColor{ANSI: "3", ANSI256: "180", TrueColor: "#E5C07B"},
	}
}

func statusRedColor() lipgloss.CompleteAdaptiveColor {
	return lipgloss.CompleteAdaptiveColor{
		Light: lipgloss.CompleteColor{ANSI: "1", ANSI256: "160", TrueColor: "#CB2431"},
		Dark:  lipgloss.CompleteColor{ANSI: "1", ANSI256: "203", TrueColor: "#F97583"},
	}
}

func renderStatusDot(theme string, activity uiActivity, frame int) string {
	palette := uiPalette(theme)
	switch activity {
	case uiActivityRunning:
		if (frame/3)%2 == 1 {
			return " "
		}
		return lipgloss.NewStyle().Foreground(palette.muted).Render("●")
	case uiActivityQueued:
		return lipgloss.NewStyle().Foreground(statusAmberColor()).Render("●")
	case uiActivityQuestion:
		return lipgloss.NewStyle().Foreground(palette.primary).Render("●")
	case uiActivityInterrupted:
		return lipgloss.NewStyle().Foreground(statusAmberColor()).Faint(true).Render("●")
	case uiActivityError:
		return lipgloss.NewStyle().Foreground(statusRedColor()).Render("●")
	default:
		return lipgloss.NewStyle().Foreground(statusGreenColor()).Render("●")
	}
}

func renderCompactionStatus() string {
	return lipgloss.NewStyle().Foreground(statusAmberColor()).Render("⚠ compacting")
}

func renderReviewerStatus() string {
	keyword := lipgloss.NewStyle().Foreground(statusGreenColor()).Bold(true).Render("reviewing")
	return "● " + keyword
}
