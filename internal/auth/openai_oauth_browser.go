package auth

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"os/exec"
	"runtime"
	"strings"
	"time"
)

const defaultManualBrowserRedirectURI = "http://127.0.0.1:43110/callback"

type BrowserAuthSession struct {
	AuthorizeURL string
	RedirectURI  string
	State        string
	CodeVerifier string
}

type BrowserCallback struct {
	Code  string
	State string
}

type OAuthCallbackListener struct {
	redirectURI string
	resultCh    chan BrowserCallback
	errCh       chan error
	server      *http.Server
	listener    net.Listener
}

func BeginOpenAIBrowserFlow(opts OpenAIOAuthOptions, redirectURI string) (BrowserAuthSession, error) {
	opts = normalizeOpenAIOAuthOptions(opts)
	if strings.TrimSpace(redirectURI) == "" {
		redirectURI = defaultManualBrowserRedirectURI
	}

	state, err := randomBase64URL(24)
	if err != nil {
		return BrowserAuthSession{}, fmt.Errorf("generate oauth state: %w", err)
	}
	codeVerifier, err := randomBase64URL(48)
	if err != nil {
		return BrowserAuthSession{}, fmt.Errorf("generate oauth code verifier: %w", err)
	}

	h := sha256.Sum256([]byte(codeVerifier))
	challenge := base64.RawURLEncoding.EncodeToString(h[:])

	issuer := strings.TrimSuffix(opts.Issuer, "/")
	endpoint := issuer + "/authorize"
	values := url.Values{}
	values.Set("response_type", "code")
	values.Set("client_id", opts.ClientID)
	values.Set("redirect_uri", redirectURI)
	values.Set("scope", "openid profile email offline_access")
	values.Set("code_challenge", challenge)
	values.Set("code_challenge_method", "S256")
	values.Set("state", state)
	values.Set("prompt", "consent")

	return BrowserAuthSession{
		AuthorizeURL: endpoint + "?" + values.Encode(),
		RedirectURI:  redirectURI,
		State:        state,
		CodeVerifier: codeVerifier,
	}, nil
}

func CompleteOpenAIBrowserFlow(ctx context.Context, opts OpenAIOAuthOptions, session BrowserAuthSession, callbackInput string) (Method, error) {
	parsed, err := ParseOAuthCallbackInput(callbackInput)
	if err != nil {
		return Method{}, err
	}
	if strings.TrimSpace(session.State) != "" && strings.TrimSpace(parsed.State) != "" && parsed.State != session.State {
		return Method{}, errors.New("oauth state mismatch")
	}
	if strings.TrimSpace(parsed.Code) == "" {
		return Method{}, errors.New("oauth callback is missing code")
	}
	return exchangeOpenAIAuthorizationCode(ctx, opts, parsed.Code, session.CodeVerifier, session.RedirectURI)
}

func ParseOAuthCallbackInput(input string) (BrowserCallback, error) {
	input = strings.TrimSpace(input)
	if input == "" {
		return BrowserCallback{}, errors.New("oauth callback input is empty")
	}

	if strings.Contains(input, "://") {
		u, err := url.Parse(input)
		if err != nil {
			return BrowserCallback{}, fmt.Errorf("parse callback url: %w", err)
		}
		q := u.Query()
		return BrowserCallback{Code: q.Get("code"), State: q.Get("state")}, nil
	}

	if strings.Contains(input, "code=") {
		q, err := url.ParseQuery(strings.TrimPrefix(input, "?"))
		if err != nil {
			return BrowserCallback{}, fmt.Errorf("parse callback query: %w", err)
		}
		return BrowserCallback{Code: q.Get("code"), State: q.Get("state")}, nil
	}

	return BrowserCallback{Code: input}, nil
}

func StartOAuthCallbackListener() (*OAuthCallbackListener, error) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return nil, fmt.Errorf("listen oauth callback: %w", err)
	}
	resultCh := make(chan BrowserCallback, 1)
	errCh := make(chan error, 1)
	srv := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		result := BrowserCallback{Code: q.Get("code"), State: q.Get("state")}
		if strings.TrimSpace(result.Code) == "" {
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte("Missing code in callback"))
			return
		}
		_, _ = w.Write([]byte("Authorization received. Return to terminal."))
		select {
		case resultCh <- result:
		default:
		}
	})}
	go func() {
		if serveErr := srv.Serve(ln); serveErr != nil && !errors.Is(serveErr, http.ErrServerClosed) {
			errCh <- serveErr
		}
	}()
	return &OAuthCallbackListener{
		redirectURI: "http://" + ln.Addr().String() + "/callback",
		resultCh:    resultCh,
		errCh:       errCh,
		server:      srv,
		listener:    ln,
	}, nil
}

func (l *OAuthCallbackListener) RedirectURI() string {
	if l == nil {
		return ""
	}
	return l.redirectURI
}

func (l *OAuthCallbackListener) Wait(ctx context.Context, timeout time.Duration) (BrowserCallback, error) {
	if l == nil {
		return BrowserCallback{}, errors.New("oauth callback listener is nil")
	}
	if timeout <= 0 {
		timeout = defaultPollTimeout
	}
	waitCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	defer l.Close()

	select {
	case <-waitCtx.Done():
		if errors.Is(waitCtx.Err(), context.DeadlineExceeded) {
			return BrowserCallback{}, errors.New("oauth browser callback timed out")
		}
		return BrowserCallback{}, waitCtx.Err()
	case serveErr := <-l.errCh:
		return BrowserCallback{}, fmt.Errorf("oauth callback server failed: %w", serveErr)
	case result := <-l.resultCh:
		return result, nil
	}
}

func (l *OAuthCallbackListener) Close() error {
	if l == nil {
		return nil
	}
	_ = l.server.Shutdown(context.Background())
	if l.listener != nil {
		return l.listener.Close()
	}
	return nil
}

func OpenBrowser(rawURL string) error {
	if strings.TrimSpace(rawURL) == "" {
		return errors.New("empty url")
	}
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "darwin":
		cmd = exec.Command("open", rawURL)
	case "windows":
		cmd = exec.Command("rundll32", "url.dll,FileProtocolHandler", rawURL)
	default:
		cmd = exec.Command("xdg-open", rawURL)
	}
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("open browser: %w", err)
	}
	return nil
}

func randomBase64URL(size int) (string, error) {
	if size <= 0 {
		size = 32
	}
	b := make([]byte, size)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}
