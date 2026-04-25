package app

import (
	"builder/server/auth"
	"builder/server/sessionview"
	"builder/shared/client"
	"builder/shared/config"
	"context"
	tea "github.com/charmbracelet/bubbletea"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"
)

func TestStatusLineGitStartupRefreshCachesBranch(t *testing.T) {
	repoRoot := initStatusLineGitRepo(t, "statusline-branch")
	search := newStubUIPathReferenceSearch()
	close(search.events)
	m := newProjectedTestUIModel(
		nil,
		closedProjectedRuntimeEvents(),
		closedAskEvents(),
		WithUIPathReferenceSearch(search),
		WithUIStatusConfig(uiStatusConfig{WorkspaceRoot: repoRoot}),
	)

	updated := drainStatusLineStartupCommands(t, m, m.Init())
	status := stripANSIAndTrimRight(updated.renderStatusLine(120, uiThemeStyles("dark")))
	if !strings.Contains(status, "statusline-branch") {
		t.Fatalf("expected startup git branch in status line, got %q", status)
	}
}

func initStatusLineGitRepo(t *testing.T, branch string) string {
	t.Helper()
	repoRoot := t.TempDir()
	cmd := exec.Command("git", "-C", repoRoot, "init", "-b", branch)
	cmd.Env = sanitizedGitEnv(os.Environ())
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git init -b %s: %v (%s)", branch, err, out)
	}
	return repoRoot
}

func drainStatusLineStartupCommands(t *testing.T, m *uiModel, cmd tea.Cmd) *uiModel {
	t.Helper()
	if cmd == nil {
		return m
	}
	msg := cmd()
	switch typed := msg.(type) {
	case nil:
		return m
	case tea.BatchMsg:
		for _, child := range typed {
			m = drainStatusLineStartupCommands(t, m, child)
		}
		return m
	default:
		next, nextCmd := m.Update(msg)
		updated, ok := next.(*uiModel)
		if !ok {
			t.Fatalf("unexpected model type %T", next)
		}
		return drainStatusLineStartupCommands(t, updated, nextCmd)
	}
}

func TestStatusUsageWindowsByLabelDisambiguatesDuplicateExtraBucketsWithoutUniqueQualifier(t *testing.T) {
	resetAt := time.Date(2026, time.March, 25, 2, 0, 0, 0, time.UTC).Unix()
	windows := statusUsageWindowsByLabel(statusUsagePayload{
		AdditionalRateLimits: []statusUsageExtraBucket{
			{
				RateLimit: &statusUsageRateLimit{
					PrimaryWindow: &statusUsageWindow{UsedPercent: 10, LimitWindowSeconds: 5 * 3600, ResetAt: resetAt},
				},
			},
			{
				RateLimit: &statusUsageRateLimit{
					PrimaryWindow: &statusUsageWindow{UsedPercent: 20, LimitWindowSeconds: 5 * 3600, ResetAt: resetAt},
				},
			},
			{
				MeteredFeature: "images",
				LimitName:      "images",
				RateLimit: &statusUsageRateLimit{
					PrimaryWindow: &statusUsageWindow{UsedPercent: 30, LimitWindowSeconds: 5 * 3600, ResetAt: resetAt},
				},
			},
			{
				MeteredFeature: "images",
				LimitName:      "images",
				RateLimit: &statusUsageRateLimit{
					PrimaryWindow: &statusUsageWindow{UsedPercent: 40, LimitWindowSeconds: 5 * 3600, ResetAt: resetAt},
				},
			},
		},
	})
	got := make([]string, 0, len(windows))
	for _, window := range windows {
		got = append(got, window.Qualifier)
	}
	want := []string{"extra", "extra #2", "images", "images #2"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("qualifiers = %v, want %v", got, want)
	}
}

func TestStatusParentSessionNameResolvesFromSessionViews(t *testing.T) {
	persistenceRoot := t.TempDir()
	parentStore := createAuthoritativeAppSession(t, persistenceRoot, "/tmp/work-a")
	if err := parentStore.SetName("incident-root"); err != nil {
		t.Fatalf("set parent name: %v", err)
	}
	sessionViews := client.NewLoopbackSessionViewClient(sessionview.NewService(sessionview.NewStaticSessionResolver(parentStore), nil, nil))
	got, warning := statusParentSessionName(context.Background(), sessionViews, parentStore.Meta().SessionID)
	if warning != "" {
		t.Fatalf("unexpected warning: %q", warning)
	}
	if got != "incident-root" {
		t.Fatalf("parent session name = %q", got)
	}
}

func TestStatusRefreshCmdSchedulesBaseEnrichmentForProgressiveCollector(t *testing.T) {
	persistenceRoot := t.TempDir()
	parentStore := createAuthoritativeAppSession(t, persistenceRoot, "/tmp/work-a")
	if err := parentStore.SetName("incident-root"); err != nil {
		t.Fatalf("set parent name: %v", err)
	}
	sessionViews := client.NewLoopbackSessionViewClient(sessionview.NewService(sessionview.NewStaticSessionResolver(parentStore), nil, nil))
	collector := &stubProgressiveStatusCollector{base: uiStatusSnapshot{ParentSessionID: parentStore.Meta().SessionID}}
	m := newProjectedStaticUIModel(
		WithUIStatusConfig(uiStatusConfig{SessionViews: sessionViews}),
		WithUIStatusCollector(collector),
	)
	cmd := m.statusRefreshCmd()
	if cmd == nil {
		t.Fatal("expected progressive status refresh to schedule base enrichment")
	}
	batch, ok := cmd().(tea.BatchMsg)
	if !ok {
		t.Fatalf("message type = %T, want tea.BatchMsg", cmd())
	}
	if len(batch) == 0 {
		t.Fatal("expected at least one batched status command")
	}
	baseMsg, ok := batch[0]().(statusBaseRefreshDoneMsg)
	if !ok {
		t.Fatalf("batched message type = %T, want statusBaseRefreshDoneMsg", batch[0]())
	}
	if baseMsg.snapshot.ParentSessionName != "incident-root" {
		t.Fatalf("parent session name = %q, want incident-root", baseMsg.snapshot.ParentSessionName)
	}
}

func TestStatusVisibleAuthSummarySuppressesGenericSubscriptionWhenPlanPresent(t *testing.T) {
	if got := statusVisibleAuthSummary(uiStatusAuthInfo{Summary: "Subscription", Visible: true}, uiStatusSubscriptionInfo{Summary: "Pro subscription"}); got != "" {
		t.Fatalf("visible auth summary = %q", got)
	}
	if got := statusVisibleAuthSummary(uiStatusAuthInfo{Summary: "user@example.com", Visible: true}, uiStatusSubscriptionInfo{Summary: "Pro subscription"}); got != "user@example.com" {
		t.Fatalf("visible auth summary = %q", got)
	}
	if got := statusVisibleAuthSummary(uiStatusAuthInfo{Summary: "Not configured"}, uiStatusSubscriptionInfo{Summary: "Pro subscription"}); got != "" {
		t.Fatalf("visible auth summary = %q", got)
	}
}

func TestStatusSubscriptionResetMetaIncludesRelativeDuration(t *testing.T) {
	now := time.Date(2026, time.March, 24, 20, 0, 0, 0, time.UTC)
	resetAt := now.Add(49*time.Hour + 3*time.Minute)
	got := statusSubscriptionResetMeta(resetAt, now)
	if !strings.Contains(got, "in 2d1h3m") {
		t.Fatalf("reset meta = %q", got)
	}
	if !strings.Contains(got, "at ") {
		t.Fatalf("expected local timestamp in reset meta, got %q", got)
	}
}

func TestStatusCollectorPrefersWorkspaceRootForWorkdir(t *testing.T) {
	workspaceRoot := t.TempDir()
	collector := defaultUIStatusCollector{}
	snapshot, err := collector.Collect(context.Background(), uiStatusRequest{WorkspaceRoot: workspaceRoot})
	if err != nil {
		t.Fatalf("collect status: %v", err)
	}
	if snapshot.Workdir != workspaceRoot {
		t.Fatalf("workdir = %q, want %q", snapshot.Workdir, workspaceRoot)
	}
	if snapshot.Git.Visible {
		t.Fatal("expected non-git temp directory to hide git section")
	}
}

func TestStatusShouldFetchSubscriptionUsageOnlyForDefaultOpenAIOAuth(t *testing.T) {
	oauthState := auth.State{Method: auth.Method{Type: auth.MethodOAuth, OAuth: &auth.OAuthMethod{AccessToken: "access-token", AccountID: "acct-123"}}}
	if !statusShouldFetchSubscriptionUsage(config.Settings{}, oauthState) {
		t.Fatal("expected default OAuth configuration to allow subscription usage fetch")
	}
	for _, baseURL := range []string{
		"https://chatgpt.com",
		"https://chatgpt.com/backend-api",
		"https://chat.openai.com",
		"https://chat.openai.com/backend-api",
	} {
		if !statusShouldFetchSubscriptionUsage(config.Settings{OpenAIBaseURL: baseURL}, oauthState) {
			t.Fatalf("expected official ChatGPT base URL %q to allow subscription usage fetch", baseURL)
		}
	}
	if statusShouldFetchSubscriptionUsage(config.Settings{OpenAIBaseURL: "https://example.com/backend-api"}, oauthState) {
		t.Fatal("expected custom base URL override to disable subscription usage fetch")
	}
	if statusShouldFetchSubscriptionUsage(config.Settings{ProviderOverride: "anthropic"}, oauthState) {
		t.Fatal("expected provider override to disable subscription usage fetch")
	}
	if statusShouldFetchSubscriptionUsage(config.Settings{}, auth.State{}) {
		t.Fatal("expected non-OAuth auth state to disable subscription usage fetch")
	}
}

func TestCollectSubscriptionStatusDoesNotFetchForOverrides(t *testing.T) {
	originalFetcher := statusUsagePayloadFetcher
	defer func() { statusUsagePayloadFetcher = originalFetcher }()
	called := false
	statusUsagePayloadFetcher = func(_ context.Context, baseURL string, _ auth.State) (statusUsagePayload, error) {
		called = true
		if baseURL != statusUsageBaseURL {
			t.Fatalf("usage fetch base URL = %q, want %q", baseURL, statusUsageBaseURL)
		}
		return statusUsagePayload{PlanType: "pro"}, nil
	}
	state := auth.State{Method: auth.Method{Type: auth.MethodOAuth, OAuth: &auth.OAuthMethod{AccessToken: "access-token", AccountID: "acct-123"}}}

	status := collectSubscriptionStatus(context.Background(), uiStatusRequest{Settings: config.Settings{OpenAIBaseURL: "https://example.com/backend-api"}}, state, nil)
	if status.Applicable {
		t.Fatalf("expected overridden base URL to disable subscription status, got %+v", status)
	}
	status = collectSubscriptionStatus(context.Background(), uiStatusRequest{Settings: config.Settings{ProviderOverride: "openai-compatible"}}, state, nil)
	if status.Applicable {
		t.Fatalf("expected provider override to disable subscription status, got %+v", status)
	}
	if called {
		t.Fatal("did not expect subscription usage fetcher to be called for overrides")
	}

	status = collectSubscriptionStatus(context.Background(), uiStatusRequest{Settings: config.Settings{OpenAIBaseURL: "https://chatgpt.com/backend-api"}}, state, nil)
	if !status.Applicable || status.Summary != "Pro subscription" {
		t.Fatalf("expected official ChatGPT base URL to preserve subscription status, got %+v", status)
	}
	if !called {
		t.Fatal("expected subscription usage fetcher to be called for official ChatGPT base URL")
	}
}

func TestCollectSubscriptionStatusUsesFixedUsageEndpointForOfficialChatGPTHost(t *testing.T) {
	originalFetcher := statusUsagePayloadFetcher
	defer func() { statusUsagePayloadFetcher = originalFetcher }()
	called := false
	statusUsagePayloadFetcher = func(_ context.Context, baseURL string, _ auth.State) (statusUsagePayload, error) {
		called = true
		if baseURL != statusUsageBaseURL {
			t.Fatalf("usage fetch base URL = %q, want %q", baseURL, statusUsageBaseURL)
		}
		return statusUsagePayload{
			PlanType: "pro",
			RateLimit: &statusUsageRateLimit{
				PrimaryWindow: &statusUsageWindow{UsedPercent: 12.5, LimitWindowSeconds: 5 * 3600, ResetAt: 1704069000},
			},
		}, nil
	}
	state := auth.State{Method: auth.Method{Type: auth.MethodOAuth, OAuth: &auth.OAuthMethod{AccessToken: "access-token", AccountID: "acct-123"}}}
	status := collectSubscriptionStatus(context.Background(), uiStatusRequest{Settings: config.Settings{OpenAIBaseURL: "https://chatgpt.com"}}, state, nil)
	if !called {
		t.Fatal("expected subscription usage fetcher to be called")
	}
	if !status.Applicable || status.Summary != "Pro subscription" {
		t.Fatalf("expected official ChatGPT host to preserve subscription status, got %+v", status)
	}
	if len(status.Windows) != 1 || status.Windows[0].Label != "5h" {
		t.Fatalf("expected quota window preserved, got %+v", status.Windows)
	}
}

func TestFetchStatusUsagePayloadFetchesWhamUsageWithOAuthHeaders(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/backend-api/wham/usage" {
			t.Fatalf("path = %q", r.URL.Path)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer access-token" {
			t.Fatalf("authorization header = %q", got)
		}
		if got := r.Header.Get("ChatGPT-Account-Id"); got != "acct-123" {
			t.Fatalf("ChatGPT-Account-Id header = %q", got)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"plan_type":"pro",
			"rate_limit":{
				"primary_window":{"used_percent":12.5,"limit_window_seconds":18000,"reset_at":1704069000},
				"secondary_window":{"used_percent":40.0,"limit_window_seconds":604800,"reset_at":1704074400}
			}
		}`))
	}))
	defer server.Close()

	status, err := fetchStatusUsagePayload(context.Background(), server.URL+"/backend-api", auth.State{Method: auth.Method{Type: auth.MethodOAuth, OAuth: &auth.OAuthMethod{AccessToken: "access-token", AccountID: "acct-123"}}})
	if err != nil {
		t.Fatalf("fetch status usage payload: %v", err)
	}

	if status.PlanType != "pro" {
		t.Fatalf("plan type = %q", status.PlanType)
	}
	windows := statusUsageWindowsByLabel(status)
	if len(windows) != 2 {
		t.Fatalf("windows len = %d", len(windows))
	}
	if windows[0].Label != "5h" || windows[1].Label != "weekly" {
		t.Fatalf("windows = %#v", windows)
	}
}

func TestFetchStatusUsagePayloadHandlesUsageErrors(t *testing.T) {
	t.Run("non-2xx", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			http.Error(w, "boom", http.StatusBadGateway)
		}))
		defer server.Close()

		_, err := fetchStatusUsagePayload(context.Background(), server.URL+"/backend-api", auth.State{Method: auth.Method{Type: auth.MethodOAuth, OAuth: &auth.OAuthMethod{AccessToken: "access-token"}}})
		if err == nil || !strings.Contains(err.Error(), "usage request failed") {
			t.Fatalf("err = %v", err)
		}
	})

	t.Run("decode error", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			_, _ = w.Write([]byte(`{"plan_type":`))
		}))
		defer server.Close()

		_, err := fetchStatusUsagePayload(context.Background(), server.URL+"/backend-api", auth.State{Method: auth.Method{Type: auth.MethodOAuth, OAuth: &auth.OAuthMethod{AccessToken: "access-token"}}})
		if err == nil || !strings.Contains(err.Error(), "decode usage response") {
			t.Fatalf("err = %v", err)
		}
	})
}

func TestStatusCollectorUsesRefreshedOAuthStateForUsageFetch(t *testing.T) {
	now := time.Date(2026, time.January, 1, 10, 0, 0, 0, time.UTC)
	store := auth.NewMemoryStore(auth.State{
		Scope: auth.ScopeGlobal,
		Method: auth.Method{
			Type: auth.MethodOAuth,
			OAuth: &auth.OAuthMethod{
				AccessToken:  "stale-token",
				RefreshToken: "refresh-token",
				TokenType:    "Bearer",
				Expiry:       now.Add(-time.Minute),
				AccountID:    "acct-456",
			},
		},
	})
	refresher := auth.NewOAuthRefresher(nil, func() time.Time { return now }, 30*time.Second)
	refresher.Refresh = func(context.Context, auth.Method) (auth.Method, error) {
		return auth.Method{
			Type: auth.MethodOAuth,
			OAuth: &auth.OAuthMethod{
				AccessToken:  "fresh-token",
				RefreshToken: "refresh-token",
				TokenType:    "Bearer",
				Expiry:       now.Add(time.Hour),
				AccountID:    "acct-456",
			},
		}, nil
	}
	manager := auth.NewManager(store, refresher, func() time.Time { return now.Add(time.Minute) })
	originalFetcher := statusUsagePayloadFetcher
	defer func() { statusUsagePayloadFetcher = originalFetcher }()
	statusUsagePayloadFetcher = func(_ context.Context, baseURL string, state auth.State) (statusUsagePayload, error) {
		if baseURL != statusUsageBaseURL {
			t.Fatalf("base URL = %q", baseURL)
		}
		authorization, err := state.Method.AuthHeaderValue()
		if err != nil {
			t.Fatalf("auth header value: %v", err)
		}
		if got := authorization; got != "Bearer fresh-token" {
			t.Fatalf("authorization header value = %q", got)
		}
		if got := strings.TrimSpace(state.Method.OAuth.AccountID); got != "acct-456" {
			t.Fatalf("ChatGPT-Account-Id value = %q", got)
		}
		return statusUsagePayload{PlanType: "pro", RateLimit: &statusUsageRateLimit{PrimaryWindow: &statusUsageWindow{UsedPercent: 12.5, LimitWindowSeconds: 18000, ResetAt: 1704069000}}}, nil
	}

	collector := defaultUIStatusCollector{}
	snapshot, err := collector.Collect(context.Background(), uiStatusRequest{
		WorkspaceRoot: t.TempDir(),
		Settings:      config.Settings{},
		AuthManager:   manager,
	})
	if err != nil {
		t.Fatalf("collect status: %v", err)
	}
	if !strings.Contains(snapshot.Auth.Summary, "Subscription") {
		t.Fatalf("auth summary = %q", snapshot.Auth.Summary)
	}
	if snapshot.Subscription.Summary != "Pro subscription" {
		t.Fatalf("subscription summary = %q", snapshot.Subscription.Summary)
	}
	if len(snapshot.Subscription.Windows) != 1 || snapshot.Subscription.Windows[0].Label != "5h" {
		t.Fatalf("windows = %#v", snapshot.Subscription.Windows)
	}
}

func TestStatusCollectorPreservesStoredAuthStateWhenRefreshFails(t *testing.T) {
	now := time.Date(2026, time.January, 1, 10, 0, 0, 0, time.UTC)
	store := auth.NewMemoryStore(auth.State{
		Scope: auth.ScopeGlobal,
		Method: auth.Method{
			Type: auth.MethodOAuth,
			OAuth: &auth.OAuthMethod{
				AccessToken:  "stale-token",
				RefreshToken: "refresh-token",
				TokenType:    "Bearer",
				Expiry:       now.Add(-time.Minute),
				AccountID:    "acct-789",
				Email:        "user@example.com",
			},
		},
		EnvAPIKeyPreference: auth.EnvAPIKeyPreferencePreferSaved,
	})
	refresher := auth.NewOAuthRefresher(nil, func() time.Time { return now }, 30*time.Second)
	refresher.Refresh = func(context.Context, auth.Method) (auth.Method, error) {
		return auth.Method{}, auth.ErrOAuthRefreshFailed
	}
	manager := auth.NewManager(store, refresher, func() time.Time { return now.Add(time.Minute) })

	collector := defaultUIStatusCollector{}
	snapshot, err := collector.Collect(context.Background(), uiStatusRequest{
		WorkspaceRoot: t.TempDir(),
		Settings:      config.Settings{},
		AuthManager:   manager,
	})
	if err != nil {
		t.Fatalf("collect status: %v", err)
	}
	if !strings.Contains(snapshot.Auth.Summary, "user@example.com") {
		t.Fatalf("auth summary = %q", snapshot.Auth.Summary)
	}
	if !snapshot.Subscription.Applicable {
		t.Fatal("expected subscription section to stay applicable")
	}
	if !strings.Contains(snapshot.Subscription.Summary, auth.ErrOAuthRefreshFailed.Error()) {
		t.Fatalf("subscription summary = %q", snapshot.Subscription.Summary)
	}
	if !strings.Contains(snapshot.CollectorWarning, auth.ErrOAuthRefreshFailed.Error()) {
		t.Fatalf("collector warning = %q", snapshot.CollectorWarning)
	}
}
