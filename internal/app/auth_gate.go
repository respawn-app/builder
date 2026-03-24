package app

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"strings"
	"time"

	"builder/internal/auth"
	"builder/internal/config"
	"github.com/charmbracelet/lipgloss"
)

type authInteraction struct {
	Manager         *auth.Manager
	State           auth.State
	Gate            auth.StartupGate
	StartupErr      error
	FlowErr         error
	OAuthOptions    auth.OpenAIOAuthOptions
	Theme           string
	AlternateScreen config.TUIAlternateScreenPolicy
	HasEnvAPIKey    bool
}

type authInteractor interface {
	WrapStore(base auth.Store) auth.Store
	NeedsInteraction(req authInteraction) bool
	Interact(ctx context.Context, req authInteraction) error
}

type headlessAuthInteractor struct {
	lookupEnv func(string) string
}

type oauthCallbackListener interface {
	RedirectURI() string
	Wait(ctx context.Context, timeoutSeconds time.Duration) (auth.BrowserCallback, error)
	Close() error
}

type interactiveAuthInteractor struct {
	stdin                 io.Reader
	stderr                io.Writer
	lookupEnv             func(string) string
	openBrowser           func(string) error
	startCallbackListener func() (oauthCallbackListener, error)
	runDeviceFlow         func(context.Context, auth.OpenAIOAuthOptions, func(auth.DeviceCode)) (auth.Method, error)
	pickMethod            func(authInteraction) (authMethodPickerResult, error)
	pickConflict          func(authInteraction) (authConflictPickerResult, error)
	showSuccess           func(authSuccessScreenData) error
	promptReader          *bufio.Reader
}

func newInteractiveAuthInteractor() authInteractor {
	return &interactiveAuthInteractor{
		stdin:       os.Stdin,
		stderr:      os.Stderr,
		lookupEnv:   os.Getenv,
		openBrowser: auth.OpenBrowser,
		startCallbackListener: func() (oauthCallbackListener, error) {
			return auth.StartOAuthCallbackListener()
		},
		runDeviceFlow: auth.RunOpenAIDeviceCodeFlow,
	}
}

func newHeadlessAuthInteractor() authInteractor {
	return &headlessAuthInteractor{lookupEnv: os.Getenv}
}

func (i *interactiveAuthInteractor) WrapStore(base auth.Store) auth.Store {
	lookupEnv := i.lookupEnv
	if lookupEnv == nil {
		lookupEnv = os.Getenv
	}
	return auth.NewEnvAPIKeyOverrideStore(base, func(key string) (string, bool) {
		value := lookupEnv(key)
		return value, strings.TrimSpace(value) != ""
	}, auth.EnvAPIKeyOverrideRespectStoredPreference)
}

func (i *headlessAuthInteractor) WrapStore(base auth.Store) auth.Store {
	lookupEnv := i.lookupEnv
	if lookupEnv == nil {
		lookupEnv = os.Getenv
	}
	return auth.NewEnvAPIKeyOverrideStore(base, func(key string) (string, bool) {
		value := lookupEnv(key)
		return value, strings.TrimSpace(value) != ""
	}, auth.EnvAPIKeyOverrideAlways)
}

func ensureAuthReady(ctx context.Context, mgr *auth.Manager, oauthOpts auth.OpenAIOAuthOptions, theme string, alternateScreen config.TUIAlternateScreenPolicy, interactor authInteractor) error {
	if mgr == nil {
		return errors.New("auth manager is required")
	}
	if interactor == nil {
		return errors.New("auth interactor is required")
	}

	for {
		state, err := mgr.Load(ctx)
		if err != nil {
			return err
		}
		gate := auth.EvaluateStartupGate(state)
		var startupErr error
		if !gate.Ready {
			startupErr = auth.EnsureStartupReady(state)
		}
		req := authInteraction{
			Manager:         mgr,
			State:           state,
			Gate:            gate,
			StartupErr:      startupErr,
			OAuthOptions:    oauthOpts,
			Theme:           theme,
			AlternateScreen: alternateScreen,
			HasEnvAPIKey:    hasNonEmptyEnvValue(resolveEnvLookup(interactor), "OPENAI_API_KEY"),
		}
		if !interactor.NeedsInteraction(req) {
			if startupErr != nil {
				return startupErr
			}
			return nil
		}
		if err := interactor.Interact(ctx, req); err != nil {
			return err
		}
	}
}

func resolveEnvLookup(interactor authInteractor) func(string) string {
	switch v := interactor.(type) {
	case *interactiveAuthInteractor:
		if v.lookupEnv != nil {
			return v.lookupEnv
		}
	case *headlessAuthInteractor:
		if v.lookupEnv != nil {
			return v.lookupEnv
		}
	}
	return os.Getenv
}

func hasNonEmptyEnvValue(lookupEnv func(string) string, key string) bool {
	if lookupEnv == nil {
		lookupEnv = os.Getenv
	}
	return strings.TrimSpace(lookupEnv(key)) != ""
}

func (i *headlessAuthInteractor) NeedsInteraction(req authInteraction) bool {
	return !req.Gate.Ready
}

func (i *interactiveAuthInteractor) NeedsInteraction(req authInteraction) bool {
	if !req.Gate.Ready {
		return true
	}
	return req.HasEnvAPIKey && req.State.EnvAPIKeyPreference == auth.EnvAPIKeyPreferenceUnspecified && req.State.Method.Type == auth.MethodOAuth
}

func (i *headlessAuthInteractor) Interact(ctx context.Context, req authInteraction) error {
	if req.StartupErr != nil {
		return req.StartupErr
	}
	return auth.EnsureStartupReady(auth.EmptyState())
}

func (i *interactiveAuthInteractor) Interact(ctx context.Context, req authInteraction) error {
	if i.NeedsInteraction(req) && req.Gate.Ready {
		return i.resolveEnvAPIKeyConflict(ctx, req)
	}

	for {
		choice, err := i.chooseMethod(req)
		if err != nil {
			return err
		}
		req.FlowErr = nil

		var method auth.Method
		switch choice {
		case authMethodChoiceEnvAPIKey:
			_, err = req.Manager.SetEnvAPIKeyPreference(ctx, auth.EnvAPIKeyPreferencePreferEnv, true)
			if err != nil {
				return fmt.Errorf("save env api key preference: %w", err)
			}
			if err := i.showAuthSuccess(ctx, req); err != nil {
				return err
			}
			return nil
		case authMethodChoiceBrowserAuto:
			method, err = i.runOAuthBrowserAuto(ctx, req.OAuthOptions, req.Theme)
		case authMethodChoiceBrowserPaste:
			method, err = i.runOAuthBrowserPaste(ctx, req.OAuthOptions, req.Theme)
		case authMethodChoiceDevice:
			method, err = i.runDeviceFlow(ctx, req.OAuthOptions, func(code auth.DeviceCode) {
				i.printAuthSection(req.Theme, authMethodDisplayTitle(authMethodChoiceDevice), []string{
					authURLStyle(req.Theme).Render(code.VerificationURL),
					authBodyStyle(req.Theme).Render("Code: ") + authCodeStyle(req.Theme).Render(code.UserCode),
					authMetaStyle(req.Theme).Render("Waiting for authorization..."),
				})
			})
		default:
			req.FlowErr = fmt.Errorf("unknown auth method %q", choice)
			continue
		}
		if err != nil {
			req.FlowErr = err
			continue
		}
		preference := req.State.EnvAPIKeyPreference
		setPreference := false
		if req.HasEnvAPIKey && preference == auth.EnvAPIKeyPreferenceUnspecified {
			preference = auth.EnvAPIKeyPreferencePreferSaved
			setPreference = true
		}
		if _, err := req.Manager.SwitchMethodAndSetEnvAPIKeyPreference(ctx, method, preference, setPreference, true); err != nil {
			return fmt.Errorf("save auth method: %w", err)
		}
		if err := i.showAuthSuccess(ctx, req); err != nil {
			return err
		}
		return nil
	}
}

func (i *interactiveAuthInteractor) resolveEnvAPIKeyConflict(ctx context.Context, req authInteraction) error {
	run := i.pickConflict
	if run == nil {
		run = runAuthConflictPicker
	}
	picked, err := run(req)
	if err != nil {
		return err
	}
	if picked.Canceled {
		return errors.New("auth canceled by user")
	}
	preference := auth.EnvAPIKeyPreferencePreferSaved
	if picked.Choice == authConflictChoiceEnvAPIKey {
		preference = auth.EnvAPIKeyPreferencePreferEnv
	}
	if _, err := req.Manager.SetEnvAPIKeyPreference(ctx, preference, true); err != nil {
		return fmt.Errorf("save env api key preference: %w", err)
	}
	return nil
}

func (i *interactiveAuthInteractor) showAuthSuccess(ctx context.Context, req authInteraction) error {
	run := i.showSuccess
	if run == nil {
		run = runAuthSuccessScreen
	}
	state, err := req.Manager.Load(ctx)
	if err != nil {
		return fmt.Errorf("load auth state for success screen: %w", err)
	}
	return run(authSuccessScreenData{
		Theme:           req.Theme,
		AlternateScreen: req.AlternateScreen,
		Method:          state.Method,
	})
}

func (i *interactiveAuthInteractor) chooseMethod(req authInteraction) (authMethodChoice, error) {
	run := i.pickMethod
	if run == nil {
		run = runAuthMethodPicker
	}
	picked, err := run(req)
	if err != nil {
		return "", err
	}
	if picked.Canceled {
		return "", errors.New("auth canceled by user")
	}
	return picked.Choice, nil
}

func (i *interactiveAuthInteractor) runOAuthBrowserAuto(ctx context.Context, opts auth.OpenAIOAuthOptions, theme string) (auth.Method, error) {
	listener, err := i.startCallbackListener()
	if err != nil {
		return auth.Method{}, err
	}
	session, err := auth.BeginOpenAIBrowserFlow(opts, listener.RedirectURI())
	if err != nil {
		_ = listener.Close()
		return auth.Method{}, err
	}
	lines := []string{authURLStyle(theme).Render(session.AuthorizeURL)}
	if err := i.openBrowser(session.AuthorizeURL); err != nil {
		lines = append(lines, authMetaStyle(theme).Render(fmt.Sprintf("Builder could not open your browser automatically (%v). Open the URL manually.", err)))
	} else {
		lines = append(lines, authMetaStyle(theme).Render("Builder opened your default browser. If nothing appeared, open the URL manually."))
	}
	lines = append(lines, authMetaStyle(theme).Render("Waiting for browser callback..."))
	i.printAuthSection(theme, authMethodDisplayTitle(authMethodChoiceBrowserAuto), lines)
	callback, err := listener.Wait(ctx, opts.PollTimeout)
	if err != nil {
		return auth.Method{}, err
	}
	query := url.Values{
		"code":  []string{callback.Code},
		"state": []string{callback.State},
	}
	return auth.CompleteOpenAIBrowserFlow(ctx, opts, session, query.Encode())
}

func (i *interactiveAuthInteractor) runOAuthBrowserPaste(ctx context.Context, opts auth.OpenAIOAuthOptions, theme string) (auth.Method, error) {
	session, err := auth.BeginOpenAIBrowserFlow(opts, "")
	if err != nil {
		return auth.Method{}, err
	}
	lines := []string{authURLStyle(theme).Render(session.AuthorizeURL)}
	if err := i.openBrowser(session.AuthorizeURL); err != nil {
		lines = append(lines, authMetaStyle(theme).Render(fmt.Sprintf("Builder could not open your browser automatically (%v). Open the URL manually.", err)))
	} else {
		lines = append(lines, authMetaStyle(theme).Render("Builder opened your default browser. If nothing appeared, open the URL manually."))
	}
	lines = append(lines, authMetaStyle(theme).Render("After sign-in, paste the full callback URL or just the code below."))
	i.printAuthSection(theme, authMethodDisplayTitle(authMethodChoiceBrowserPaste), lines)
	callbackInput, err := i.prompt(authPromptStyle(theme).Render("Paste callback URL or code: "))
	if err != nil {
		return auth.Method{}, err
	}
	return auth.CompleteOpenAIBrowserFlow(ctx, opts, session, callbackInput)
}

func (i *interactiveAuthInteractor) printAuthSection(theme, title string, lines []string) {
	if len(lines) == 0 {
		return
	}
	var out strings.Builder
	out.WriteByte('\n')
	out.WriteString(authTitleStyle(theme).Render(title))
	out.WriteByte('\n')
	for idx, line := range lines {
		if idx > 0 {
			out.WriteByte('\n')
		}
		out.WriteString(line)
	}
	out.WriteString("\n\n")
	fprintf(i.stderrOrDiscard(), "%s", out.String())
}

func authTitleStyle(theme string) lipgloss.Style {
	return lipgloss.NewStyle().Foreground(uiPalette(theme).primary).Bold(true)
}

func authBodyStyle(theme string) lipgloss.Style {
	return lipgloss.NewStyle().Foreground(uiPalette(theme).foreground)
}

func authMetaStyle(theme string) lipgloss.Style {
	return lipgloss.NewStyle().Foreground(uiPalette(theme).muted).Faint(true)
}

func authPromptStyle(theme string) lipgloss.Style {
	return lipgloss.NewStyle().Foreground(uiPalette(theme).primary).Bold(true)
}

func authURLStyle(theme string) lipgloss.Style {
	return lipgloss.NewStyle().Foreground(uiPalette(theme).primary).Underline(true)
}

func authCodeStyle(theme string) lipgloss.Style {
	return lipgloss.NewStyle().Foreground(uiPalette(theme).secondary).Bold(true)
}

func (i *interactiveAuthInteractor) prompt(label string) (string, error) {
	if i.stdin == nil {
		return "", errors.New("auth prompt input is required")
	}
	fprintf(i.stderrOrDiscard(), "%s", label)
	if i.promptReader == nil {
		i.promptReader = bufio.NewReader(i.stdin)
	}
	line, err := i.promptReader.ReadString('\n')
	if err != nil {
		if errors.Is(err, os.ErrClosed) {
			return "", err
		}
		if len(line) == 0 {
			return "", err
		}
	}
	return strings.TrimRight(line, "\r\n"), nil
}

func (i *interactiveAuthInteractor) stderrOrDiscard() io.Writer {
	if i == nil || i.stderr == nil {
		return io.Discard
	}
	return i.stderr
}

func fprintf(w io.Writer, format string, args ...any) {
	_, _ = fmt.Fprintf(w, format, args...)
}
