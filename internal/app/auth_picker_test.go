package app

import (
	"context"
	"errors"
	"io"
	"strings"
	"testing"
	"time"

	"builder/internal/auth"
	"builder/internal/config"
	"builder/internal/tui"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"
)

func TestAuthMethodPickerViewUsesFriendlyTitlesAndDescriptions(t *testing.T) {
	m := newAuthMethodPickerModel("dark", startupPickerNotice{
		Text: "Choose how Builder should complete OpenAI sign-in.",
		Kind: startupPickerNoticeNeutral,
	}, false)
	out := ansi.Strip(m.View())
	if !strings.Contains(out, "Sign in to Builder") {
		t.Fatalf("expected auth picker title, got %q", out)
	}
	if !strings.Contains(out, "Open browser and finish automatically") {
		t.Fatalf("expected friendly browser title, got %q", out)
	}
	if !strings.Contains(out, "Open browser and paste the callback manually") {
		t.Fatalf("expected friendly paste title, got %q", out)
	}
	if !strings.Contains(out, "Use a device code in any browser") {
		t.Fatalf("expected friendly device title, got %q", out)
	}
	if !strings.Contains(out, "Choose how Builder should complete OpenAI sign-in.") {
		t.Fatalf("expected body subtitle, got %q", out)
	}
	if strings.Contains(out, "Best on this machine. Builder opens your browser and waits for the callback.") {
		t.Fatalf("did not expect per-option descriptions, got %q", out)
	}
	if strings.Contains(out, "Recommended when your terminal can open a local browser.") {
		t.Fatalf("did not expect per-option selected note, got %q", out)
	}
	if strings.Contains(out, "oauth_browser") || strings.Contains(out, "oauth_browser_paste") || strings.Contains(out, "oauth_device") {
		t.Fatalf("did not expect raw auth method ids in picker, got %q", out)
	}
}

func TestAuthMethodPickerIncludesEnvAPIKeyOptionWhenAvailable(t *testing.T) {
	m := newAuthMethodPickerModel("dark", startupPickerNotice{}, true)
	out := ansi.Strip(m.View())
	if !strings.Contains(out, "Use existing OPENAI_API_KEY from now on") {
		t.Fatalf("expected env api key option, got %q", out)
	}
}

func TestAuthMethodPickerHeaderUsesAppForeground(t *testing.T) {
	m := newAuthMethodPickerModel("dark", startupPickerNotice{}, false)
	header := m.renderHeader()
	expectedPrefix := strings.TrimSuffix(tui.ApplyThemeDefaultForeground("x", "dark"), "x\x1b[0m")
	if !strings.HasPrefix(header, expectedPrefix) {
		t.Fatalf("expected auth picker header to start with app foreground, got %q", header)
	}
	if stripped := ansi.Strip(header); !strings.Contains(stripped, "Sign in to Builder") {
		t.Fatalf("expected auth picker header text preserved, got %q", stripped)
	}
}

func TestAuthMethodPickerSelectsSecondOption(t *testing.T) {
	m := newAuthMethodPickerModel("dark", startupPickerNotice{}, false)
	next, _ := m.Update(tea.KeyMsg{Type: tea.KeyDown})
	m = next.(*startupPickerModel)
	next, _ = m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = next.(*startupPickerModel)
	if m.result.ChoiceID != string(authMethodChoiceBrowserPaste) {
		t.Fatalf("choice=%q want %q", m.result.ChoiceID, authMethodChoiceBrowserPaste)
	}
}

func TestAuthMethodPickerCancel(t *testing.T) {
	m := newAuthMethodPickerModel("dark", startupPickerNotice{}, false)
	next, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'q'}})
	m = next.(*startupPickerModel)
	if !m.result.Canceled {
		t.Fatal("expected canceled result")
	}
}

func TestAuthMethodPickerScrollsToKeepSelectedRowVisible(t *testing.T) {
	m := newAuthMethodPickerModel("dark", startupPickerNotice{}, false)
	next, _ := m.Update(tea.WindowSizeMsg{Width: 80, Height: 6})
	m = next.(*startupPickerModel)
	next, _ = m.Update(tea.KeyMsg{Type: tea.KeyDown})
	m = next.(*startupPickerModel)
	next, _ = m.Update(tea.KeyMsg{Type: tea.KeyDown})
	m = next.(*startupPickerModel)
	out := ansi.Strip(m.View())
	if !strings.Contains(out, "Use a device code in any browser") {
		t.Fatalf("expected selected row visible on short terminal, got %q", out)
	}
	if strings.Contains(out, "Open browser and finish automatically") {
		t.Fatalf("expected viewport to scroll past first row, got %q", out)
	}
}

func TestAuthMethodPickerDropsSelectedNoteWhenHeightIsTight(t *testing.T) {
	m := newAuthMethodPickerModel("dark", startupPickerNotice{}, false)
	next, _ := m.Update(tea.WindowSizeMsg{Width: 80, Height: 5})
	m = next.(*startupPickerModel)
	out := ansi.Strip(m.View())
	if !strings.Contains(out, "Open browser and finish automatically") {
		t.Fatalf("expected selected row visible, got %q", out)
	}
	if strings.Contains(out, "Best on this machine. Builder opens your browser and waits for the callback.") {
		t.Fatalf("did not expect per-option descriptions on tight height, got %q", out)
	}
}

func TestAuthMethodPickerSubtitleSeparatedFromHeaderByBlankLine(t *testing.T) {
	m := newAuthMethodPickerModel("dark", startupPickerNotice{
		Text: "Choose how Builder should complete OpenAI sign-in.",
		Kind: startupPickerNoticeNeutral,
	}, false)
	lines := strings.Split(ansi.Strip(m.View()), "\n")
	titleLine := -1
	for i, line := range lines {
		if strings.Contains(line, "Sign in to Builder") {
			titleLine = i
			break
		}
	}
	if titleLine < 0 || titleLine+2 >= len(lines) {
		t.Fatalf("expected subtitle after blank line, got %q", strings.Join(lines, "\n"))
	}
	if strings.TrimSpace(lines[titleLine+1]) != "" {
		t.Fatalf("expected blank line between title and subtitle, got %q", lines[titleLine+1])
	}
	if !strings.Contains(lines[titleLine+2], "Choose how Builder should complete OpenAI sign-in.") {
		t.Fatalf("expected subtitle after blank line, got %q", strings.Join(lines, "\n"))
	}
	if !strings.HasPrefix(lines[titleLine], "  ") {
		t.Fatalf("expected indented title line, got %q", lines[titleLine])
	}
	if !strings.HasPrefix(lines[titleLine+2], "  ") {
		t.Fatalf("expected subtitle aligned with title, got %q", lines[titleLine+2])
	}
}

func TestAuthConflictPickerUsesBodySubtitleAndSingleLineRows(t *testing.T) {
	m := newStartupPickerModel(
		authConflictPickerHeaderMarkdown,
		"Choose auth source",
		"dark",
		startupPickerNotice{
			Text: "Builder found both saved subscription auth and OPENAI_API_KEY. Choose which source should win from now on.",
			Kind: startupPickerNoticeNeutral,
		},
		authConflictOptions(),
	)
	out := ansi.Strip(m.View())
	if !strings.Contains(out, "Choose Auth Source") {
		t.Fatalf("expected conflict picker title, got %q", out)
	}
	if !strings.Contains(out, "Builder found both saved subscription auth and OPENAI_API_KEY") {
		t.Fatalf("expected body subtitle, got %q", out)
	}
	if strings.Contains(out, "Prefer the API key already exported in your environment whenever it is present.") {
		t.Fatalf("did not expect per-option descriptions, got %q", out)
	}
	if strings.Contains(out, "Builder will keep using the environment API key until you change auth with /logout.") {
		t.Fatalf("did not expect per-option notes, got %q", out)
	}
	lines := strings.Split(out, "\n")
	titleLine := -1
	for i, line := range lines {
		if strings.Contains(line, "Choose Auth Source") {
			titleLine = i
			break
		}
	}
	if titleLine < 0 || titleLine+2 >= len(lines) {
		t.Fatalf("expected subtitle after blank line, got %q", out)
	}
	if strings.TrimSpace(lines[titleLine+1]) != "" {
		t.Fatalf("expected blank line between title and subtitle, got %q", lines[titleLine+1])
	}
	if !strings.Contains(lines[titleLine+2], "Builder found both saved subscription auth") {
		t.Fatalf("expected subtitle after blank line, got %q", out)
	}
	if !containsInOrder(out,
		"Use existing OPENAI_API_KEY from now on",
		"Keep using saved subscription sign-in",
	) {
		t.Fatalf("expected single-line conflict rows, got %q", out)
	}
	if !strings.HasPrefix(lines[titleLine], "  ") {
		t.Fatalf("expected indented title line, got %q", lines[titleLine])
	}
	if !strings.HasPrefix(lines[titleLine+2], "  ") {
		t.Fatalf("expected subtitle aligned with title, got %q", lines[titleLine+2])
	}
}

func TestAuthMethodPickerNoticeForDeviceUnsupported(t *testing.T) {
	notice := authMethodPickerNoticeForRequest(authInteraction{FlowErr: auth.ErrDeviceCodeUnsupported})
	if notice.Kind != startupPickerNoticeError {
		t.Fatalf("expected error notice kind, got %q", notice.Kind)
	}
	if !strings.Contains(notice.Text, "Device-code sign-in is not enabled") {
		t.Fatalf("unexpected notice text %q", notice.Text)
	}
}

func TestAuthMethodPickerNoticeUsesStartupError(t *testing.T) {
	notice := authMethodPickerNoticeForRequest(authInteraction{StartupErr: errors.New("refresh failed")})
	if notice.Kind != startupPickerNoticeError {
		t.Fatalf("expected error notice kind, got %q", notice.Kind)
	}
	if !strings.Contains(notice.Text, "refresh failed") {
		t.Fatalf("unexpected notice text %q", notice.Text)
	}
}

func TestAuthSuccessScreenTitleUsesEmailWhenAvailable(t *testing.T) {
	got := authSuccessScreenTitle(auth.Method{
		Type: auth.MethodOAuth,
		OAuth: &auth.OAuthMethod{
			Email: "user@example.com",
		},
	})
	if got != "Auth success for: user@example.com" {
		t.Fatalf("unexpected title %q", got)
	}
}

func TestAuthSuccessScreenTitleFallsBackWithoutEmail(t *testing.T) {
	if got := authSuccessScreenTitle(auth.Method{Type: auth.MethodAPIKey, APIKey: &auth.APIKeyMethod{Key: "sk"}}); got != "Auth success" {
		t.Fatalf("unexpected title %q", got)
	}
}

func TestInteractiveAuthInteractorNeedsInteractionForEnvConflict(t *testing.T) {
	interactor := &interactiveAuthInteractor{}
	if !interactor.NeedsInteraction(authInteraction{
		Gate:         auth.StartupGate{Ready: true},
		State:        auth.State{Scope: auth.ScopeGlobal, Method: auth.Method{Type: auth.MethodOAuth, OAuth: &auth.OAuthMethod{AccessToken: "x"}}},
		HasEnvAPIKey: true,
	}) {
		t.Fatal("expected unresolved env-vs-oauth conflict to require interaction")
	}
	if interactor.NeedsInteraction(authInteraction{
		Gate:         auth.StartupGate{Ready: true},
		State:        auth.State{Scope: auth.ScopeGlobal, Method: auth.Method{Type: auth.MethodOAuth, OAuth: &auth.OAuthMethod{AccessToken: "x"}}, EnvAPIKeyPreference: auth.EnvAPIKeyPreferencePreferSaved},
		HasEnvAPIKey: true,
	}) {
		t.Fatal("did not expect saved preference to reopen conflict picker")
	}
}

func TestInteractiveAuthInteractorOffersEnvAPIKeyChoiceWhenAvailable(t *testing.T) {
	ctx := context.Background()
	mgr := auth.NewManager(auth.NewMemoryStore(auth.EmptyState()), nil, time.Now)
	pickerCalled := false
	successCalls := 0
	interactor := &interactiveAuthInteractor{
		stderr: io.Discard,
		pickMethod: func(req authInteraction) (authMethodPickerResult, error) {
			pickerCalled = true
			if !req.HasEnvAPIKey {
				t.Fatal("expected env api key to be offered in auth flow")
			}
			return authMethodPickerResult{Choice: authMethodChoiceEnvAPIKey}, nil
		},
		showSuccess: func(authSuccessScreenData) error {
			successCalls++
			return nil
		},
	}

	err := interactor.Interact(ctx, authInteraction{
		Manager:         mgr,
		State:           auth.EmptyState(),
		Gate:            auth.StartupGate{Reason: auth.ErrAuthNotConfigured.Error()},
		Theme:           "dark",
		AlternateScreen: config.TUIAlternateScreenAuto,
		HasEnvAPIKey:    true,
	})
	if err != nil {
		t.Fatalf("interact: %v", err)
	}
	if !pickerCalled {
		t.Fatal("expected auth picker to run")
	}
	if successCalls != 1 {
		t.Fatalf("expected success screen once, got %d", successCalls)
	}
	state, err := mgr.Load(ctx)
	if err != nil {
		t.Fatalf("load state: %v", err)
	}
	if state.EnvAPIKeyPreference != auth.EnvAPIKeyPreferencePreferEnv {
		t.Fatalf("expected env preference saved, got %q", state.EnvAPIKeyPreference)
	}
}

func TestInteractiveAuthInteractorResolvesEnvConflictAndRemembersPreference(t *testing.T) {
	ctx := context.Background()
	mgr := auth.NewManager(auth.NewMemoryStore(auth.State{
		Scope: auth.ScopeGlobal,
		Method: auth.Method{
			Type: auth.MethodOAuth,
			OAuth: &auth.OAuthMethod{
				AccessToken: "oauth-token",
			},
		},
	}), nil, time.Now)
	successCalls := 0
	interactor := &interactiveAuthInteractor{
		pickConflict: func(authInteraction) (authConflictPickerResult, error) {
			return authConflictPickerResult{Choice: authConflictChoiceEnvAPIKey}, nil
		},
		showSuccess: func(authSuccessScreenData) error {
			successCalls++
			return nil
		},
	}
	called := false
	interactor.pickConflict = func(req authInteraction) (authConflictPickerResult, error) {
		called = true
		return authConflictPickerResult{Choice: authConflictChoiceEnvAPIKey}, nil
	}

	err := interactor.Interact(ctx, authInteraction{
		Manager:         mgr,
		State:           auth.State{Scope: auth.ScopeGlobal, Method: auth.Method{Type: auth.MethodOAuth, OAuth: &auth.OAuthMethod{AccessToken: "oauth-token"}}},
		Gate:            auth.StartupGate{Ready: true},
		Theme:           "dark",
		AlternateScreen: config.TUIAlternateScreenAuto,
		HasEnvAPIKey:    true,
	})
	if err != nil {
		t.Fatalf("interact: %v", err)
	}
	if !called {
		t.Fatal("expected conflict picker to run")
	}
	if successCalls != 0 {
		t.Fatalf("expected no success screen for conflict-only resolution, got %d", successCalls)
	}
	state, err := mgr.Load(ctx)
	if err != nil {
		t.Fatalf("load state: %v", err)
	}
	if state.EnvAPIKeyPreference != auth.EnvAPIKeyPreferencePreferEnv {
		t.Fatalf("expected env preference saved, got %q", state.EnvAPIKeyPreference)
	}
}

func TestInteractiveAuthInteractorChoosingOAuthWithEnvRemembersSavedPreference(t *testing.T) {
	ctx := context.Background()
	mgr := auth.NewManager(auth.NewMemoryStore(auth.EmptyState()), nil, time.Now)
	successCalls := 0
	interactor := &interactiveAuthInteractor{
		stderr: io.Discard,
		pickMethod: func(authInteraction) (authMethodPickerResult, error) {
			return authMethodPickerResult{Choice: authMethodChoiceDevice}, nil
		},
		runDeviceFlow: func(context.Context, auth.OpenAIOAuthOptions, func(auth.DeviceCode)) (auth.Method, error) {
			return auth.Method{
				Type: auth.MethodOAuth,
				OAuth: &auth.OAuthMethod{
					AccessToken:  "access-token",
					RefreshToken: "refresh-token",
					TokenType:    "Bearer",
					Expiry:       time.Now().Add(time.Hour),
				},
			}, nil
		},
		showSuccess: func(authSuccessScreenData) error {
			successCalls++
			return nil
		},
	}

	err := interactor.Interact(ctx, authInteraction{
		Manager:         mgr,
		State:           auth.EmptyState(),
		Gate:            auth.StartupGate{Reason: auth.ErrAuthNotConfigured.Error()},
		Theme:           "dark",
		AlternateScreen: config.TUIAlternateScreenAuto,
		HasEnvAPIKey:    true,
	})
	if err != nil {
		t.Fatalf("interact: %v", err)
	}
	if successCalls != 1 {
		t.Fatalf("expected success screen once, got %d", successCalls)
	}
	state, err := mgr.Load(ctx)
	if err != nil {
		t.Fatalf("load state: %v", err)
	}
	if state.Method.Type != auth.MethodOAuth {
		t.Fatalf("expected oauth auth, got %q", state.Method.Type)
	}
	if state.EnvAPIKeyPreference != auth.EnvAPIKeyPreferencePreferSaved {
		t.Fatalf("expected saved-auth preference after choosing oauth, got %q", state.EnvAPIKeyPreference)
	}
}

func TestInteractiveAuthInteractorRetriesWithFlowErrorAndClearsOnSuccess(t *testing.T) {
	ctx := context.Background()
	mgr := auth.NewManager(auth.NewMemoryStore(auth.EmptyState()), nil, time.Now)
	pickCalls := 0
	deviceCalls := 0
	successCalls := 0
	interactor := &interactiveAuthInteractor{
		stderr: io.Discard,
		pickMethod: func(req authInteraction) (authMethodPickerResult, error) {
			pickCalls++
			switch pickCalls {
			case 1:
				if req.FlowErr != nil {
					t.Fatalf("did not expect initial flow error, got %v", req.FlowErr)
				}
				return authMethodPickerResult{Choice: authMethodChoiceDevice}, nil
			case 2:
				if !errors.Is(req.FlowErr, auth.ErrDeviceCodeUnsupported) {
					t.Fatalf("expected device unsupported error on retry, got %v", req.FlowErr)
				}
				return authMethodPickerResult{Choice: authMethodChoiceDevice}, nil
			default:
				t.Fatalf("did not expect additional picker call %d", pickCalls)
				return authMethodPickerResult{}, nil
			}
		},
		runDeviceFlow: func(context.Context, auth.OpenAIOAuthOptions, func(auth.DeviceCode)) (auth.Method, error) {
			deviceCalls++
			if deviceCalls == 1 {
				return auth.Method{}, auth.ErrDeviceCodeUnsupported
			}
			return auth.Method{
				Type: auth.MethodOAuth,
				OAuth: &auth.OAuthMethod{
					AccessToken:  "access-token",
					RefreshToken: "refresh-token",
					TokenType:    "Bearer",
					Expiry:       time.Now().Add(time.Hour),
				},
			}, nil
		},
		showSuccess: func(authSuccessScreenData) error {
			successCalls++
			return nil
		},
	}

	err := interactor.Interact(ctx, authInteraction{
		Manager:         mgr,
		State:           auth.EmptyState(),
		Gate:            auth.StartupGate{Reason: auth.ErrAuthNotConfigured.Error()},
		Theme:           "dark",
		AlternateScreen: config.TUIAlternateScreenAuto,
	})
	if err != nil {
		t.Fatalf("interact: %v", err)
	}
	if pickCalls != 2 {
		t.Fatalf("expected two picker calls, got %d", pickCalls)
	}
	if deviceCalls != 2 {
		t.Fatalf("expected two device flow attempts, got %d", deviceCalls)
	}
	if successCalls != 1 {
		t.Fatalf("expected success screen only after successful retry, got %d", successCalls)
	}
	state, err := mgr.Load(ctx)
	if err != nil {
		t.Fatalf("load state: %v", err)
	}
	if state.Method.Type != auth.MethodOAuth {
		t.Fatalf("expected oauth auth, got %q", state.Method.Type)
	}
}
