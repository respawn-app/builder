package app

import (
	"fmt"
	"strings"

	"builder/server/auth"
	"builder/shared/config"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

type authSuccessScreenData struct {
	Theme           string
	AlternateScreen config.TUIAlternateScreenPolicy
	Method          auth.Method
}

type authSuccessScreenModel struct {
	data   authSuccessScreenData
	width  int
	height int
	styles authSuccessScreenStyles
	ready  bool
}

type authSuccessScreenStyles struct {
	title lipgloss.Style
	hint  lipgloss.Style
}

func newAuthSuccessScreenModel(data authSuccessScreenData) *authSuccessScreenModel {
	return &authSuccessScreenModel{
		data:   data,
		width:  defaultPickerWidth,
		height: defaultPickerHeight,
		styles: newAuthSuccessScreenStyles(data.Theme),
	}
}

func newAuthSuccessScreenStyles(theme string) authSuccessScreenStyles {
	palette := uiPalette(theme)
	return authSuccessScreenStyles{
		title: lipgloss.NewStyle().Foreground(palette.primary).Bold(true),
		hint:  lipgloss.NewStyle().Foreground(palette.foreground),
	}
}

func (m *authSuccessScreenModel) Init() tea.Cmd {
	return nil
}

func (m *authSuccessScreenModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch key := msg.(type) {
	case tea.WindowSizeMsg:
		if key.Width > 0 {
			m.width = key.Width
		}
		if key.Height > 0 {
			m.height = key.Height
		}
		m.ready = true
		return m, nil
	case tea.KeyMsg:
		return m, tea.Quit
	}
	return m, nil
}

func (m *authSuccessScreenModel) View() string {
	body := strings.Join([]string{
		m.styles.title.Render(authSuccessScreenTitle(m.data.Method)),
		"",
		m.styles.hint.Render("Press any key to continue"),
	}, "\n")
	width := m.width
	height := m.height
	if width < 1 {
		width = defaultPickerWidth
	}
	if height < 1 {
		height = defaultPickerHeight
	}
	return lipgloss.Place(width, height, lipgloss.Center, lipgloss.Center, body)
}

func authSuccessScreenTitle(method auth.Method) string {
	if method.Type == auth.MethodOAuth && method.OAuth != nil {
		if email := strings.TrimSpace(method.OAuth.Email); email != "" {
			return fmt.Sprintf("Auth success for: %s", email)
		}
	}
	return "Auth success"
}

var runAuthSuccessScreen = func(data authSuccessScreenData) error {
	model := newAuthSuccessScreenModel(data)
	options := []tea.ProgramOption{}
	if shouldUseStartupPickerAltScreen(data.AlternateScreen) {
		options = append(options, tea.WithAltScreen())
	}
	program := tea.NewProgram(model, options...)
	_, err := program.Run()
	return err
}
