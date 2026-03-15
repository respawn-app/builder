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
)

type authInteraction struct {
	Manager      *auth.Manager
	Gate         auth.StartupGate
	StartupErr   error
	OAuthOptions auth.OpenAIOAuthOptions
}

type authInteractor interface {
	WrapStore(base auth.Store) auth.Store
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
	return base
}

func (i *headlessAuthInteractor) WrapStore(base auth.Store) auth.Store {
	lookupEnv := i.lookupEnv
	if lookupEnv == nil {
		lookupEnv = os.Getenv
	}
	return auth.NewEnvAPIKeyOverrideStore(base, func(key string) (string, bool) {
		value := lookupEnv(key)
		return value, strings.TrimSpace(value) != ""
	})
}

func ensureAuthReady(ctx context.Context, mgr *auth.Manager, oauthOpts auth.OpenAIOAuthOptions, interactor authInteractor) error {
	if mgr == nil {
		return errors.New("auth manager is required")
	}
	if interactor == nil {
		return errors.New("auth interactor is required")
	}

	for {
		startupErr := mgr.EnsureStartupReady(ctx)
		if startupErr == nil {
			return nil
		}

		state, loadErr := mgr.Load(ctx)
		if loadErr != nil {
			return loadErr
		}
		gate := auth.EvaluateStartupGate(state)
		if gate.Ready {
			return nil
		}
		if err := interactor.Interact(ctx, authInteraction{
			Manager:      mgr,
			Gate:         gate,
			StartupErr:   startupErr,
			OAuthOptions: oauthOpts,
		}); err != nil {
			return err
		}
	}
}

func (i *headlessAuthInteractor) Interact(ctx context.Context, req authInteraction) error {
	if req.StartupErr != nil {
		return req.StartupErr
	}
	return auth.EnsureStartupReady(auth.EmptyState())
}

func (i *interactiveAuthInteractor) Interact(ctx context.Context, req authInteraction) error {
	fprintf(i.stderrOrDiscard(), "Auth required (%s).\n", req.Gate.Reason)

	handled, err := configureAuthFromEnvironment(ctx, req.Manager, i.lookupEnv)
	if err != nil {
		fprintf(i.stderrOrDiscard(), "Failed to configure API key from environment: %v\n", err)
	} else if handled {
		return nil
	}

	for {
		choice, err := i.prompt("AUTH REQUIRED. Choose method ([1] oauth_browser, [2] oauth_browser_paste, [3] oauth_device): ")
		if err != nil {
			return err
		}

		var method auth.Method
		switch strings.TrimSpace(choice) {
		case "1", "oauth_browser", "oauth":
			method, err = i.runOAuthBrowserAuto(ctx, req.OAuthOptions)
		case "2", "oauth_browser_paste", "paste":
			method, err = i.runOAuthBrowserPaste(ctx, req.OAuthOptions)
		case "3", "oauth_device", "device":
			method, err = i.runDeviceFlow(ctx, req.OAuthOptions, func(code auth.DeviceCode) {
				fprintf(i.stderrOrDiscard(), "\nOpen %s and enter code: %s\nWaiting for authorization...\n\n", code.VerificationURL, code.UserCode)
			})
		default:
			fprintf(i.stderrOrDiscard(), "Unknown choice\n")
			continue
		}
		if err != nil {
			if errors.Is(err, auth.ErrDeviceCodeUnsupported) {
				fprintf(i.stderrOrDiscard(), "OAuth device flow is not enabled for this issuer.\n")
			} else {
				fprintf(i.stderrOrDiscard(), "OAuth login failed: %v\n", err)
			}
			continue
		}
		if _, err := req.Manager.SwitchMethod(ctx, method, true); err != nil {
			return fmt.Errorf("save auth method: %w", err)
		}
		return nil
	}
}

func configureAuthFromEnvironment(ctx context.Context, mgr *auth.Manager, lookupEnv func(string) string) (bool, error) {
	if mgr == nil {
		return false, errors.New("auth manager is required")
	}
	if lookupEnv == nil {
		lookupEnv = os.Getenv
	}
	key := strings.TrimSpace(lookupEnv("OPENAI_API_KEY"))
	if key == "" {
		return false, nil
	}
	_, err := mgr.SwitchMethod(ctx, auth.Method{
		Type:   auth.MethodAPIKey,
		APIKey: &auth.APIKeyMethod{Key: key},
	}, true)
	if err != nil {
		return false, fmt.Errorf("configure API key from environment: %w", err)
	}
	return true, nil
}

func (i *interactiveAuthInteractor) runOAuthBrowserAuto(ctx context.Context, opts auth.OpenAIOAuthOptions) (auth.Method, error) {
	listener, err := i.startCallbackListener()
	if err != nil {
		return auth.Method{}, err
	}
	session, err := auth.BeginOpenAIBrowserFlow(opts, listener.RedirectURI())
	if err != nil {
		_ = listener.Close()
		return auth.Method{}, err
	}
	fprintf(i.stderrOrDiscard(), "\nOpen this URL to authorize:\n%s\n\n", session.AuthorizeURL)
	if err := i.openBrowser(session.AuthorizeURL); err != nil {
		fprintf(i.stderrOrDiscard(), "Could not open browser automatically (%v). Open the URL manually.\n", err)
	}
	fprintf(i.stderrOrDiscard(), "Waiting for browser callback...\n")
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

func (i *interactiveAuthInteractor) runOAuthBrowserPaste(ctx context.Context, opts auth.OpenAIOAuthOptions) (auth.Method, error) {
	session, err := auth.BeginOpenAIBrowserFlow(opts, "")
	if err != nil {
		return auth.Method{}, err
	}
	fprintf(i.stderrOrDiscard(), "\nOpen this URL to authorize:\n%s\n\n", session.AuthorizeURL)
	if err := i.openBrowser(session.AuthorizeURL); err != nil {
		fprintf(i.stderrOrDiscard(), "Could not open browser automatically (%v). Open the URL manually.\n", err)
	}
	callbackInput, err := i.prompt("Paste callback URL (or code): ")
	if err != nil {
		return auth.Method{}, err
	}
	return auth.CompleteOpenAIBrowserFlow(ctx, opts, session, callbackInput)
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
