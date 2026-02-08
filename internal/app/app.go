package app

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"builder/internal/auth"
	"builder/internal/config"
	"builder/internal/llm"
	"builder/internal/runtime"
	"builder/internal/session"
	"builder/internal/tools"
	askquestion "builder/internal/tools/askquestion"
	bashtool "builder/internal/tools/bash"
	patchtool "builder/internal/tools/patch"

	tea "github.com/charmbracelet/bubbletea"
)

type Options struct {
	WorkspaceRoot       string
	SessionID           string
	Model               string
	ThinkingLevel       string
	Theme               string
	ModelTimeoutSeconds int
	BashTimeoutSeconds  int
	Tools               string
}

func Run(ctx context.Context, opts Options) error {
	cfg, err := config.Load(opts.WorkspaceRoot, config.LoadOptions{
		Model:               opts.Model,
		ThinkingLevel:       opts.ThinkingLevel,
		Theme:               opts.Theme,
		ModelTimeoutSeconds: opts.ModelTimeoutSeconds,
		BashTimeoutSeconds:  opts.BashTimeoutSeconds,
		Tools:               opts.Tools,
	})
	if err != nil {
		return err
	}
	printConfigReport(cfg)

	_, containerDir, err := config.ResolveWorkspaceContainer(cfg)
	if err != nil {
		return err
	}

	oauthOpts := auth.OpenAIOAuthOptions{
		Issuer:   firstNonEmpty(strings.TrimSpace(os.Getenv("BUILDER_OAUTH_ISSUER")), auth.DefaultOpenAIIssuer),
		ClientID: firstNonEmpty(strings.TrimSpace(os.Getenv("BUILDER_OAUTH_CLIENT_ID")), auth.DefaultOpenAIClientID),
	}

	mgr := auth.NewManager(
		auth.NewFileStore(config.GlobalAuthConfigPath(cfg)),
		auth.NewOpenAIOAuthRefresher(oauthOpts, time.Now, 5*time.Minute),
		time.Now,
	)
	if err := ensureAuthReady(ctx, mgr, oauthOpts); err != nil {
		return err
	}

	currentSessionID := strings.TrimSpace(opts.SessionID)
	for {
		store, err := openOrCreateSession(containerDir, currentSessionID, cfg.WorkspaceRoot)
		if err != nil {
			return err
		}

		active := effectiveSettings(cfg.Settings, store.Meta().Locked)
		enabledTools := activeToolIDs(active, store.Meta().Locked)

		logger, err := newRunLogger(store.Dir())
		if err != nil {
			return err
		}
		logger.Logf("app.start session_id=%s workspace=%s model=%s", store.Meta().SessionID, cfg.WorkspaceRoot, active.Model)
		logger.Logf("config.settings path=%s created=%t", cfg.Source.SettingsPath, cfg.Source.CreatedDefaultConfig)
		for _, line := range configSourceLines(cfg.Source) {
			logger.Logf("config.source %s", line)
		}

		toolRegistry, askBroker, err := buildToolRegistry(cfg.WorkspaceRoot, enabledTools, time.Duration(active.Timeouts.BashDefaultSeconds)*time.Second)
		if err != nil {
			_ = logger.Close()
			return err
		}
		askBridge := newAskBridge()
		askBroker.SetAskHandler(askBridge.Handle)

		modelHTTPClient := &http.Client{Timeout: time.Duration(active.Timeouts.ModelRequestSeconds) * time.Second}
		client, err := llm.NewProviderClient(llm.ProviderClientOptions{
			Model:      active.Model,
			Auth:       mgr,
			HTTPClient: modelHTTPClient,
		})
		if err != nil {
			_ = logger.Close()
			return err
		}

		runtimeEvents := make(chan runtime.Event, 2048)
		eng, err := runtime.New(store, client, toolRegistry, runtime.Config{
			Model:         active.Model,
			Temperature:   1,
			MaxTokens:     0,
			ThinkingLevel: active.ThinkingLevel,
			EnabledTools:  enabledTools,
			OnEvent: func(evt runtime.Event) {
				logger.Logf(formatRuntimeEvent(evt))
				runtimeEvents <- evt
			},
		})
		if err != nil {
			_ = logger.Close()
			return err
		}

		program := tea.NewProgram(NewUIModel(
			eng,
			runtimeEvents,
			askBridge.Events(),
			WithUILogger(logger),
			WithUIModelName(active.Model),
			WithUITheme(active.Theme),
		), tea.WithAltScreen())
		finalModel, runErr := program.Run()
		if runErr != nil {
			logger.Logf("app.exit err=%q", runErr.Error())
			_ = logger.Close()
			return runErr
		}
		logger.Logf("app.exit ok")
		_ = logger.Close()

		action := extractUIAction(finalModel)
		switch action {
		case UIActionNewSession:
			newStore, err := session.Create(containerDir, filepath.Base(containerDir), cfg.WorkspaceRoot)
			if err != nil {
				return err
			}
			currentSessionID = newStore.Meta().SessionID
			continue
		case UIActionLogout:
			if _, err := mgr.ClearMethod(ctx, true); err != nil {
				return err
			}
			if err := ensureAuthReady(ctx, mgr, oauthOpts); err != nil {
				return err
			}
			currentSessionID = store.Meta().SessionID
			continue
		default:
			return nil
		}
	}
}

func effectiveSettings(base config.Settings, locked *session.LockedContract) config.Settings {
	out := base
	if locked == nil {
		return out
	}
	if strings.TrimSpace(locked.Model) != "" {
		out.Model = locked.Model
	}
	if strings.TrimSpace(locked.ThinkingLevel) != "" {
		out.ThinkingLevel = locked.ThinkingLevel
	}
	return out
}

func activeToolIDs(settings config.Settings, locked *session.LockedContract) []tools.ID {
	if locked != nil {
		ids := make([]tools.ID, 0, len(locked.EnabledTools))
		for _, raw := range locked.EnabledTools {
			if id, ok := tools.ParseID(raw); ok {
				ids = append(ids, id)
			}
		}
		return dedupeSortToolIDs(ids)
	}
	return dedupeSortToolIDs(config.EnabledToolIDs(settings))
}

func dedupeSortToolIDs(ids []tools.ID) []tools.ID {
	seen := map[tools.ID]bool{}
	out := make([]tools.ID, 0, len(ids))
	for _, id := range ids {
		if seen[id] {
			continue
		}
		seen[id] = true
		out = append(out, id)
	}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out
}

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
	return auth.CompleteOpenAIBrowserFlow(ctx, opts, session, "code="+urlEncode(callback.Code)+"&state="+urlEncode(callback.State))
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

func urlEncode(v string) string {
	repl := strings.NewReplacer("%", "%25", "&", "%26", "=", "%3D", "+", "%2B", " ", "%20")
	return repl.Replace(v)
}

func extractUIAction(model tea.Model) UIAction {
	if model == nil {
		return UIActionNone
	}
	typed, ok := model.(*uiModel)
	if !ok {
		return UIActionNone
	}
	return typed.Action()
}

func configSourceLines(src config.SourceReport) []string {
	keys := make([]string, 0, len(src.Sources))
	for k := range src.Sources {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	lines := make([]string, 0, len(keys))
	for _, k := range keys {
		lines = append(lines, fmt.Sprintf("%s=%s", k, src.Sources[k]))
	}
	return lines
}

func printConfigReport(cfg config.App) {
	if cfg.Source.CreatedDefaultConfig {
		fmt.Fprintln(os.Stderr, "Created default settings file on first run.")
	}
	for _, line := range configSourceLines(cfg.Source) {
		fmt.Fprintf(os.Stderr, "  %s\n", line)
	}
}

func openOrCreateSession(containerDir, selectedID, workspaceRoot string) (*session.Store, error) {
	if strings.TrimSpace(selectedID) != "" {
		return session.Open(filepath.Join(containerDir, selectedID))
	}

	summaries, err := session.ListSessions(containerDir)
	if err != nil {
		return nil, err
	}
	if len(summaries) == 0 {
		containerName := filepath.Base(containerDir)
		return session.Create(containerDir, containerName, workspaceRoot)
	}

	picked, err := runSessionPicker(summaries)
	if err != nil {
		return nil, err
	}
	if picked.Canceled {
		return nil, errors.New("startup canceled by user")
	}
	if picked.CreateNew {
		containerName := filepath.Base(containerDir)
		return session.Create(containerDir, containerName, workspaceRoot)
	}
	if picked.Session == nil {
		return nil, errors.New("no session selected")
	}
	return session.Open(picked.Session.Path)
}

func buildToolRegistry(workspaceRoot string, enabled []tools.ID, bashDefaultTimeout time.Duration) (*tools.Registry, *askquestion.Broker, error) {
	patch, err := patchtool.New(workspaceRoot, true)
	if err != nil {
		return nil, nil, err
	}
	broker := askquestion.NewBroker()

	handlers := make([]tools.Handler, 0, len(enabled))
	for _, id := range enabled {
		switch id {
		case tools.ToolBash:
			handlers = append(handlers, bashtool.New(workspaceRoot, 10_000, bashtool.WithDefaultTimeout(bashDefaultTimeout)))
		case tools.ToolPatch:
			handlers = append(handlers, patch)
		case tools.ToolAskQuestion:
			handlers = append(handlers, askquestion.NewTool(broker))
		}
	}
	return tools.NewRegistry(handlers...), broker, nil
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

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if strings.TrimSpace(v) != "" {
			return v
		}
	}
	return ""
}
