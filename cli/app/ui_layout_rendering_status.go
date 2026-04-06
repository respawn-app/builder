package app

import (
	"fmt"
	"math"
	"strings"

	"builder/server/llm"
	"builder/shared/theme"

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
		style.meta.Render(l.statusModeLabel()),
		style.meta.Render(l.statusModelLabel()),
	}
	if label := processCountLabel(m.listProcesses()); label != "" {
		segments = append(segments, style.meta.Render(label))
	}
	if cacheSection := l.renderCacheHitSection(style); cacheSection != "" {
		segments = append(segments, cacheSection)
	}
	if serverOwnershipSection := l.renderServerOwnershipSection(style); serverOwnershipSection != "" {
		segments = append(segments, serverOwnershipSection)
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

func (l uiViewLayout) statusModeLabel() string {
	if l.model.rollback.isActive() {
		return "editing"
	}
	return string(l.model.view.Mode())
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
	if available <= 0 {
		return ""
	}
	text := strings.TrimSpace(m.runtimeDisconnectStatusText())
	kind := uiStatusNoticeError
	if text == "" {
		text = strings.TrimSpace(m.transientStatus)
		kind = m.transientStatusKind
	}
	if text == "" {
		return ""
	}
	text = truncateQueuedMessageLine(text, available)
	return statusNoticeStyle(m.theme, kind).Render(text)
}

func (l uiViewLayout) renderActivityStatus(available int, style uiStyles) string {
	if available <= 0 {
		return ""
	}
	if text := strings.TrimSpace(l.model.reasoningStatusHeader); text != "" {
		text = truncateQueuedMessageLine(text, available)
		return statusNoticeStyle(l.model.theme, uiStatusNoticeNeutral).Render(text)
	}
	if l.model.runtimeDisconnectStatusVisible() {
		return ""
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
	usage := l.model.runtimeStatus().ContextUsage
	if usage.WindowTokens <= 0 && !usage.HasCacheHitPercentage {
		return ""
	}
	if !usage.HasCacheHitPercentage {
		return style.meta.Render("cache --")
	}
	return style.meta.Render(fmt.Sprintf("cache %d%%", usage.CacheHitPercent))
}

func (l uiViewLayout) renderServerOwnershipSection(style uiStyles) string {
	if !l.model.statusConfig.OwnsServer {
		return ""
	}
	return style.meta.Render("server owned")
}

func (l uiViewLayout) renderContextUsage(style uiStyles) string {
	usage := l.model.runtimeStatus().ContextUsage
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
		bubbleprogress.WithSolidFill(statusContextZoneHex(l.model.theme, rawPercent)),
		bubbleprogress.WithFillCharacters('▮', '▯'),
	)
	barProgress.EmptyColor = statusContextEmptyHex(l.model.theme)
	bar := barProgress.ViewAs(float64(barPercent) / 100.0)
	label := style.meta.Render(fmt.Sprintf("%d%%", rawPercent))
	return label + " " + bar
}

func statusContextZoneHex(themeName string, percent int) string {
	return statusContextZone(themeName, percent).TrueColor
}

func statusContextEmptyHex(themeName string) string {
	return theme.ResolvePalette(themeName).Status.ContextEmpty.TrueColor
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
	return theme.DefaultPalette().Status.Success.Adaptive()
}

func statusAmberColor() lipgloss.CompleteAdaptiveColor {
	return theme.DefaultPalette().Status.Warning.Adaptive()
}

func statusRedColor() lipgloss.CompleteAdaptiveColor {
	return theme.DefaultPalette().Status.Error.Adaptive()
}

func statusContextZone(themeName string, percent int) theme.Color {
	palette := theme.ResolvePalette(themeName).Status
	if percent < 50 {
		return palette.Success
	}
	if percent < 80 {
		return palette.Warning
	}
	return palette.Error
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
