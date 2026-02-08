package app

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"net/url"
	"os"
	"strings"

	"builder/internal/auth"
)

func ensureAuthReady(ctx context.Context, mgr *auth.Manager, oauthOpts auth.OpenAIOAuthOptions) error {
	for {
		if err := mgr.EnsureStartupReady(ctx); err == nil {
			return nil
		}

		state, loadErr := mgr.Load(ctx)
		if loadErr != nil {
			return loadErr
		}
		gate := auth.EvaluateStartupGate(state)
		fmt.Fprintf(os.Stderr, "Auth required (%s).\n", gate.Reason)

		if envKey := strings.TrimSpace(os.Getenv("OPENAI_API_KEY")); envKey != "" {
			_, err := mgr.SwitchMethod(ctx, auth.Method{Type: auth.MethodAPIKey, APIKey: &auth.APIKeyMethod{Key: envKey}}, true)
			if err == nil {
				continue
			}
			fmt.Fprintf(os.Stderr, "Failed to configure API key from environment: %v\n", err)
		}

		choice, err := prompt("AUTH REQUIRED. Choose method ([1] oauth_browser, [2] oauth_browser_paste, [3] oauth_device): ")
		if err != nil {
			return err
		}

		var method auth.Method
		switch strings.TrimSpace(choice) {
		case "1", "oauth_browser", "oauth":
			method, err = runOAuthBrowserAuto(ctx, oauthOpts)
		case "2", "oauth_browser_paste", "paste":
			method, err = runOAuthBrowserPaste(ctx, oauthOpts)
		case "3", "oauth_device", "device":
			method, err = auth.RunOpenAIDeviceCodeFlow(ctx, oauthOpts, func(code auth.DeviceCode) {
				fmt.Fprintf(os.Stderr, "\nOpen %s and enter code: %s\nWaiting for authorization...\n\n", code.VerificationURL, code.UserCode)
			})
		default:
			fmt.Fprintln(os.Stderr, "Unknown choice")
			continue
		}
		if err != nil {
			if errors.Is(err, auth.ErrDeviceCodeUnsupported) {
				fmt.Fprintln(os.Stderr, "OAuth device flow is not enabled for this issuer.")
			} else {
				fmt.Fprintf(os.Stderr, "OAuth login failed: %v\n", err)
			}
			continue
		}
		if _, err := mgr.SwitchMethod(ctx, method, true); err != nil {
			fmt.Fprintf(os.Stderr, "Failed to save OAuth credentials: %v\n", err)
		}
	}
}

func runOAuthBrowserAuto(ctx context.Context, opts auth.OpenAIOAuthOptions) (auth.Method, error) {
	listener, err := auth.StartOAuthCallbackListener()
	if err != nil {
		return auth.Method{}, err
	}
	session, err := auth.BeginOpenAIBrowserFlow(opts, listener.RedirectURI())
	if err != nil {
		_ = listener.Close()
		return auth.Method{}, err
	}
	fmt.Fprintf(os.Stderr, "\nOpen this URL to authorize:\n%s\n\n", session.AuthorizeURL)
	if err := auth.OpenBrowser(session.AuthorizeURL); err != nil {
		fmt.Fprintf(os.Stderr, "Could not open browser automatically (%v). Open the URL manually.\n", err)
	}
	fmt.Fprintln(os.Stderr, "Waiting for browser callback...")
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

func runOAuthBrowserPaste(ctx context.Context, opts auth.OpenAIOAuthOptions) (auth.Method, error) {
	session, err := auth.BeginOpenAIBrowserFlow(opts, "")
	if err != nil {
		return auth.Method{}, err
	}
	fmt.Fprintf(os.Stderr, "\nOpen this URL to authorize:\n%s\n\n", session.AuthorizeURL)
	if err := auth.OpenBrowser(session.AuthorizeURL); err != nil {
		fmt.Fprintf(os.Stderr, "Could not open browser automatically (%v). Open the URL manually.\n", err)
	}
	callbackInput, err := prompt("Paste callback URL (or code): ")
	if err != nil {
		return auth.Method{}, err
	}
	return auth.CompleteOpenAIBrowserFlow(ctx, opts, session, callbackInput)
}

func prompt(label string) (string, error) {
	fmt.Fprint(os.Stderr, label)
	r := bufio.NewReader(os.Stdin)
	line, err := r.ReadString('\n')
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
