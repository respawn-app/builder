package app

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
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

	mgr := auth.NewManager(
		auth.NewFileStore(config.GlobalAuthConfigPath(cfg)),
		auth.NewOAuthRefresher(nil, time.Now, 5*time.Minute),
		time.Now,
	)
	if err := ensureAuthReady(ctx, mgr); err != nil {
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
	askBroker.SetAskHandler(func(req askquestion.Request) (string, error) {
		return askFromStdin(req), nil
	})

	transport := llm.NewHTTPTransport(mgr)
	client := llm.NewOpenAIClient(transport)

	eng, err := runtime.New(store, client, toolRegistry, runtime.Config{
		Model:       opts.Model,
		Temperature: 1,
		MaxTokens:   0,
	})
	if err != nil {
		return err
	}

	program := tea.NewProgram(NewUIModel(eng), tea.WithAltScreen())
	_, err = program.Run()
	return err
}

func ensureAuthReady(ctx context.Context, mgr *auth.Manager) error {
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

		choice, err := prompt("Choose auth method ([1] api_key, [2] oauth, [q] quit): ")
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
		case "2", "oauth":
			access, err := prompt("Enter OAuth access token: ")
			if err != nil {
				return err
			}
			refresh, err := prompt("Enter OAuth refresh token (optional): ")
			if err != nil {
				return err
			}
			expiresRaw, err := prompt("Enter OAuth expiry RFC3339 (optional): ")
			if err != nil {
				return err
			}
			exp := time.Now().Add(30 * time.Minute).UTC()
			if strings.TrimSpace(expiresRaw) != "" {
				parsed, parseErr := time.Parse(time.RFC3339, strings.TrimSpace(expiresRaw))
				if parseErr != nil {
					fmt.Fprintf(os.Stderr, "Invalid expiry format, using default 30m: %v\n", parseErr)
				} else {
					exp = parsed.UTC()
				}
			}
			_, err = mgr.SwitchMethod(ctx, auth.Method{Type: auth.MethodOAuth, OAuth: &auth.OAuthMethod{
				AccessToken:  strings.TrimSpace(access),
				RefreshToken: strings.TrimSpace(refresh),
				TokenType:    "Bearer",
				Expiry:       exp,
			}}, true)
			if err != nil {
				fmt.Fprintf(os.Stderr, "Failed to save OAuth credentials: %v\n", err)
			}
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

	sort.Slice(summaries, func(i, j int) bool {
		return summaries[i].UpdatedAt.After(summaries[j].UpdatedAt)
	})
	return session.Open(summaries[0].Path)
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

func askFromStdin(req askquestion.Request) string {
	fmt.Printf("\n? %s\n", req.Question)
	for i, s := range req.Suggestions {
		fmt.Printf("  %d. %s\n", i+1, s)
	}
	ans, _ := prompt("answer> ")
	ans = strings.TrimSpace(ans)
	if ans == "" {
		return ans
	}
	if len(req.Suggestions) > 0 {
		idx := -1
		if _, err := fmt.Sscanf(ans, "%d", &idx); err == nil {
			if idx >= 1 && idx <= len(req.Suggestions) {
				return req.Suggestions[idx-1]
			}
		}
	}
	return ans
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

func dumpJSON(v any) string {
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return "{}"
	}
	return string(b)
}
