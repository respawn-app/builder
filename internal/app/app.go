package app

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"builder/internal/actions"
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
	WorkspaceRoot string
	SessionID     string
	Model         string
}

func Run(ctx context.Context, opts Options) error {
	cfg, err := config.Load(opts.WorkspaceRoot)
	if err != nil {
		return err
	}
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

	store, err := openOrCreateSession(containerDir, opts.SessionID, cfg.WorkspaceRoot)
	if err != nil {
		return err
	}

	toolRegistry, askBroker, err := buildToolRegistry(cfg.WorkspaceRoot)
	if err != nil {
		return err
	}
	askBridge := newAskBridge()
	askBroker.SetAskHandler(askBridge.Handle)

	transport := llm.NewHTTPTransport(mgr)
	client := llm.NewOpenAIClient(transport)

	runtimeEvents := make(chan runtime.Event, 2048)
	eng, err := runtime.New(store, client, toolRegistry, runtime.Config{
		Model:       opts.Model,
		Temperature: 1,
		MaxTokens:   0,
		OnEvent: func(evt runtime.Event) {
			select {
			case runtimeEvents <- evt:
			default:
			}
		},
	})
	if err != nil {
		return err
	}

	program := tea.NewProgram(NewUIModel(eng, runtimeEvents, askBridge.Events()), tea.WithAltScreen())
	_, err = program.Run()
	return err
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

		choice, err := prompt("AUTH REQUIRED. Choose method ([1] api_key, [2] oauth_device, [r] retry, [q] quit): ")
		if err != nil {
			return err
		}
		switch strings.TrimSpace(choice) {
		case "1", "api_key":
			key, err := prompt("Enter OpenAI API key: ")
			if err != nil {
				return err
			}
			if _, err := mgr.SwitchMethod(ctx, auth.Method{Type: auth.MethodAPIKey, APIKey: &auth.APIKeyMethod{Key: strings.TrimSpace(key)}}, true); err != nil {
				fmt.Fprintf(os.Stderr, "Failed to save API key: %v\n", err)
			}
		case "2", "oauth", "oauth_device":
			method, err := auth.RunOpenAIDeviceCodeFlow(ctx, oauthOpts, func(code auth.DeviceCode) {
				fmt.Fprintf(os.Stderr, "\nOpen %s and enter code: %s\nWaiting for authorization...\n\n", code.VerificationURL, code.UserCode)
			})
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
		case "r", "retry":
			continue
		case "q", "quit", "":
			return errors.New("startup blocked: auth not configured")
		default:
			fmt.Fprintln(os.Stderr, "Unknown choice")
		}
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

func buildToolRegistry(workspaceRoot string) (*tools.Registry, *askquestion.Broker, error) {
	patch, err := patchtool.New(workspaceRoot, true)
	if err != nil {
		return nil, nil, err
	}
	actionsReg := actions.NewRegistry()
	broker := askquestion.NewBroker(actionsReg)
	registry := tools.NewRegistry(
		bashtool.New(workspaceRoot, 10_000),
		patch,
		askquestion.NewTool(broker),
	)
	return registry, broker, nil
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
