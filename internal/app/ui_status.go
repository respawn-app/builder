package app

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"builder/internal/auth"
	"builder/internal/config"
	"builder/internal/llm"
	"builder/internal/runtime"
	"builder/internal/session"
	"builder/internal/tokenutil"
	"builder/internal/tui"

	tea "github.com/charmbracelet/bubbletea"
)

const (
	statusRefreshTimeout = 10 * time.Second
	statusGitTimeout     = 4 * time.Second
	statusUsageBaseURL   = "https://chatgpt.com/backend-api"
)

var statusUsagePayloadFetcher = fetchStatusUsagePayload

type uiStatusConfig struct {
	WorkspaceRoot   string
	PersistenceRoot string
	Settings        config.Settings
	Source          config.SourceReport
	AuthManager     *auth.Manager
	AuthStatePath   string
}

type uiStatusCollector interface {
	Collect(ctx context.Context, req uiStatusRequest) (uiStatusSnapshot, error)
}

type uiStatusProgressiveCollector interface {
	CollectBase(req uiStatusRequest) uiStatusSnapshot
	CollectAuth(ctx context.Context, req uiStatusRequest, base uiStatusSnapshot) uiStatusAuthStageResult
	CollectGit(ctx context.Context, req uiStatusRequest, base uiStatusSnapshot) uiStatusGitStageResult
	CollectEnvironment(ctx context.Context, req uiStatusRequest, base uiStatusSnapshot) uiStatusEnvironmentStageResult
}

type uiStatusSection string

const (
	uiStatusSectionBase        uiStatusSection = "base"
	uiStatusSectionAuth        uiStatusSection = "account"
	uiStatusSectionGit         uiStatusSection = "git"
	uiStatusSectionEnvironment uiStatusSection = "environment"
)

type uiStatusRequest struct {
	Engine                *runtime.Engine
	WorkspaceRoot         string
	PersistenceRoot       string
	Settings              config.Settings
	Source                config.SourceReport
	AuthManager           *auth.Manager
	AuthStatePath         string
	SessionName           string
	SessionID             string
	ConfiguredModelName   string
	ModelName             string
	ThinkingLevel         string
	FastModeAvailable     bool
	FastModeEnabled       bool
	ReviewerEnabled       bool
	ReviewerMode          string
	AutoCompactionEnabled bool
	CurrentTime           time.Time
}

type uiStatusSnapshot struct {
	CollectedAt       time.Time
	Workdir           string
	SessionName       string
	SessionID         string
	ParentSessionID   string
	ParentSessionName string
	Git               uiStatusGitInfo
	Auth              uiStatusAuthInfo
	Context           uiStatusContextInfo
	Model             uiStatusModelInfo
	Config            uiStatusConfigInfo
	Subscription      uiStatusSubscriptionInfo
	Skills            []runtime.SkillInspection
	SkillTokenCounts  map[string]int
	AgentsPaths       []string
	AgentTokenCounts  map[string]int
	CompactionCount   int
	CollectorWarning  string
}

type uiStatusAuthInfo struct {
	Summary string
	Details []string
}

type uiStatusGitInfo struct {
	Visible bool
	Branch  string
	Dirty   bool
	Ahead   int
	Behind  int
	Error   string
}

type uiStatusContextInfo struct {
	UsedTokens      int
	AvailableTokens int
	WindowTokens    int
	ThresholdTokens int
}

type uiStatusModelInfo struct {
	Summary string
}

type uiStatusConfigInfo struct {
	SettingsPath    string
	OverrideSources []string
	Supervisor      string
	AutoCompaction  bool
}

type uiStatusSubscriptionInfo struct {
	Applicable bool
	Summary    string
	Windows    []uiStatusSubscriptionWindow
}

type uiStatusSubscriptionWindow struct {
	Label       string
	Qualifier   string
	UsedPercent float64
	ResetAt     time.Time
}

type statusUsagePayload struct {
	PlanType             string                   `json:"plan_type"`
	RateLimit            *statusUsageRateLimit    `json:"rate_limit"`
	AdditionalRateLimits []statusUsageExtraBucket `json:"additional_rate_limits"`
}

type statusUsageExtraBucket struct {
	MeteredFeature string                `json:"metered_feature"`
	LimitName      string                `json:"limit_name"`
	RateLimit      *statusUsageRateLimit `json:"rate_limit"`
}

type statusUsageRateLimit struct {
	PrimaryWindow   *statusUsageWindow `json:"primary_window"`
	SecondaryWindow *statusUsageWindow `json:"secondary_window"`
}

type statusUsageWindow struct {
	UsedPercent        float64 `json:"used_percent"`
	LimitWindowSeconds int     `json:"limit_window_seconds"`
	ResetAt            int64   `json:"reset_at"`
}

type statusRefreshDoneMsg struct {
	token    uint64
	snapshot uiStatusSnapshot
	err      error
}

type statusBaseRefreshDoneMsg struct {
	token    uint64
	snapshot uiStatusSnapshot
}

type statusAuthRefreshDoneMsg struct {
	token    uint64
	cacheKey string
	result   uiStatusAuthStageResult
}

type statusGitRefreshDoneMsg struct {
	token    uint64
	cacheKey string
	result   uiStatusGitStageResult
}

type statusEnvironmentRefreshDoneMsg struct {
	token    uint64
	cacheKey string
	result   uiStatusEnvironmentStageResult
}

type uiStatusAuthStageResult struct {
	Auth         uiStatusAuthInfo
	Subscription uiStatusSubscriptionInfo
	Warning      string
}

type uiStatusGitStageResult struct {
	Git uiStatusGitInfo
}

type uiStatusEnvironmentStageResult struct {
	Skills           []runtime.SkillInspection
	SkillTokenCounts map[string]int
	AgentsPaths      []string
	AgentTokenCounts map[string]int
	CollectorWarning string
}

type defaultUIStatusCollector struct{}

func WithUIStatusConfig(statusConfig uiStatusConfig) UIOption {
	return func(m *uiModel) {
		m.statusConfig = statusConfig
		if m.statusCollector == nil {
			m.statusCollector = defaultUIStatusCollector{}
		}
	}
}

func WithUIStatusCollector(collector uiStatusCollector) UIOption {
	return func(m *uiModel) {
		if collector != nil {
			m.statusCollector = collector
		}
	}
}

func WithUIStatusRepository(repository uiStatusRepository) UIOption {
	return func(m *uiModel) {
		if repository != nil {
			m.statusRepository = repository
		}
	}
}

func (m *uiModel) newStatusRequest(now time.Time) uiStatusRequest {
	return uiStatusRequest{
		Engine:                m.engine,
		WorkspaceRoot:         strings.TrimSpace(m.statusConfig.WorkspaceRoot),
		PersistenceRoot:       strings.TrimSpace(m.statusConfig.PersistenceRoot),
		Settings:              m.statusConfig.Settings,
		Source:                m.statusConfig.Source,
		AuthManager:           m.statusConfig.AuthManager,
		AuthStatePath:         strings.TrimSpace(m.statusConfig.AuthStatePath),
		SessionName:           strings.TrimSpace(m.sessionName),
		SessionID:             strings.TrimSpace(m.sessionID),
		ConfiguredModelName:   strings.TrimSpace(m.configuredModelName),
		ModelName:             strings.TrimSpace(m.modelName),
		ThinkingLevel:         strings.TrimSpace(m.thinkingLevel),
		FastModeAvailable:     m.fastModeAvailable,
		FastModeEnabled:       m.fastModeEnabled,
		ReviewerEnabled:       m.reviewerEnabled,
		ReviewerMode:          strings.TrimSpace(m.reviewerMode),
		AutoCompactionEnabled: m.autoCompactionEnabled,
		CurrentTime:           now,
	}
}

func (c defaultUIStatusCollector) Collect(ctx context.Context, req uiStatusRequest) (uiStatusSnapshot, error) {
	snapshot := c.CollectBase(req)
	authResult := c.CollectAuth(ctx, req, snapshot)
	gitResult := c.CollectGit(ctx, req, snapshot)
	envResult := c.CollectEnvironment(ctx, req, snapshot)
	snapshot.Auth = authResult.Auth
	snapshot.Subscription = authResult.Subscription
	snapshot.Git = gitResult.Git
	snapshot.Skills = envResult.Skills
	snapshot.SkillTokenCounts = envResult.SkillTokenCounts
	snapshot.AgentsPaths = envResult.AgentsPaths
	snapshot.AgentTokenCounts = envResult.AgentTokenCounts
	warnings := make([]string, 0, 3)
	if strings.TrimSpace(snapshot.CollectorWarning) != "" {
		warnings = append(warnings, strings.TrimSpace(snapshot.CollectorWarning))
	}
	if strings.TrimSpace(authResult.Warning) != "" {
		warnings = append(warnings, strings.TrimSpace(authResult.Warning))
	}
	if strings.TrimSpace(envResult.CollectorWarning) != "" {
		warnings = append(warnings, strings.TrimSpace(envResult.CollectorWarning))
	}
	snapshot.CollectorWarning = strings.Join(warnings, " | ")
	return snapshot, nil
}

func (defaultUIStatusCollector) CollectBase(req uiStatusRequest) uiStatusSnapshot {
	collectedAt := req.CurrentTime
	if collectedAt.IsZero() {
		collectedAt = time.Now()
	}
	workdir := statusWorkdir(req.WorkspaceRoot)
	contextInfo := uiStatusContextInfo{ThresholdTokens: req.Settings.ContextCompactionThresholdTokens}
	parentSessionID := ""
	parentSessionName := ""
	compactionCount := 0
	if req.Engine != nil {
		usage := req.Engine.ContextUsage()
		contextInfo.UsedTokens = usage.UsedTokens
		contextInfo.WindowTokens = usage.WindowTokens
		contextInfo.AvailableTokens = usage.WindowTokens - usage.UsedTokens
		if contextInfo.AvailableTokens < 0 {
			contextInfo.AvailableTokens = 0
		}
		parentSessionID = strings.TrimSpace(req.Engine.ParentSessionID())
		parentSessionName = statusParentSessionName(req.PersistenceRoot, parentSessionID)
		compactionCount = req.Engine.CompactionCount()
	}
	return uiStatusSnapshot{
		CollectedAt:       collectedAt,
		Workdir:           filepath.ToSlash(strings.TrimSpace(workdir)),
		SessionName:       strings.TrimSpace(req.SessionName),
		SessionID:         strings.TrimSpace(req.SessionID),
		ParentSessionID:   parentSessionID,
		ParentSessionName: parentSessionName,
		Context:           contextInfo,
		Model:             uiStatusModelInfo{Summary: statusModelSummary(req)},
		Config: uiStatusConfigInfo{
			SettingsPath:    filepath.ToSlash(strings.TrimSpace(req.Source.SettingsPath)),
			OverrideSources: statusConfigOverrideSources(req.Source),
			Supervisor:      statusSupervisorLabel(req.ReviewerEnabled, strings.TrimSpace(req.ReviewerMode)),
			AutoCompaction:  req.AutoCompactionEnabled,
		},
		CompactionCount: compactionCount,
	}
}

func statusParentSessionName(persistenceRoot, parentSessionID string) string {
	root := strings.TrimSpace(persistenceRoot)
	parentID := strings.TrimSpace(parentSessionID)
	if root == "" || parentID == "" {
		return ""
	}
	store, err := session.OpenByID(root, parentID)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(store.Meta().Name)
}

func (defaultUIStatusCollector) CollectAuth(ctx context.Context, req uiStatusRequest, _ uiStatusSnapshot) uiStatusAuthStageResult {
	state := auth.EmptyState()
	authStateErr := error(nil)
	if req.AuthManager != nil {
		loaded, loadErr := req.AuthManager.Load(ctx)
		if loadErr != nil {
			authStateErr = loadErr
		} else {
			state = loaded
			resolved, resolveErr := req.AuthManager.CurrentState(ctx)
			if resolveErr == nil {
				state = resolved
			} else {
				authStateErr = resolveErr
			}
		}
	}
	result := uiStatusAuthStageResult{
		Auth:         statusAuthInfo(state, req.Settings, authStateErr),
		Subscription: collectSubscriptionStatus(ctx, req, state, authStateErr),
	}
	if authStateErr != nil {
		result.Warning = "auth: " + authStateErr.Error()
	}
	return result
}

func (defaultUIStatusCollector) CollectGit(ctx context.Context, _ uiStatusRequest, base uiStatusSnapshot) uiStatusGitStageResult {
	return uiStatusGitStageResult{Git: collectGitStatus(ctx, base.Workdir)}
}

func (defaultUIStatusCollector) CollectEnvironment(_ context.Context, req uiStatusRequest, _ uiStatusSnapshot) uiStatusEnvironmentStageResult {
	result := uiStatusEnvironmentStageResult{}
	warnings := make([]string, 0, 2)
	skills, skillsErr := runtime.InspectSkills(req.WorkspaceRoot)
	if skillsErr != nil {
		warnings = append(warnings, "skills: "+skillsErr.Error())
	} else {
		result.Skills = skills
		result.SkillTokenCounts = statusEstimateSkillTokens(skills)
	}
	agentsPaths, agentsErr := runtime.InstalledAgentsPaths(req.WorkspaceRoot)
	if agentsErr != nil {
		warnings = append(warnings, "agents: "+agentsErr.Error())
	} else {
		result.AgentsPaths = agentsPaths
		result.AgentTokenCounts = statusEstimatePathTokens(agentsPaths)
	}
	result.CollectorWarning = strings.Join(warnings, " | ")
	return result
}

func statusWorkdir(workspaceRoot string) string {
	workdir := strings.TrimSpace(workspaceRoot)
	if workdir != "" {
		return workdir
	}
	if cwd, err := os.Getwd(); err == nil {
		return strings.TrimSpace(cwd)
	}
	return ""
}

func collectGitStatus(ctx context.Context, workdir string) uiStatusGitInfo {
	trimmedWorkdir := strings.TrimSpace(workdir)
	if trimmedWorkdir == "" {
		return uiStatusGitInfo{}
	}
	if _, err := exec.LookPath("git"); err != nil {
		return uiStatusGitInfo{}
	}
	gitCtx, cancel := context.WithTimeout(ctx, statusGitTimeout)
	defer cancel()
	cmd := exec.CommandContext(gitCtx, "git", "-C", trimmedWorkdir, "status", "--porcelain=v2", "--branch")
	out, err := cmd.CombinedOutput()
	if gitCtx.Err() == context.DeadlineExceeded || err != nil {
		if statusGitShouldHide(err, string(out)) {
			return uiStatusGitInfo{}
		}
		return uiStatusGitInfo{Visible: true, Error: statusGitError(err, string(out))}
	}
	gitInfo := uiStatusGitInfo{Visible: true}
	for _, line := range splitPlainLines(strings.TrimSpace(string(out))) {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			continue
		}
		if strings.HasPrefix(trimmed, "# branch.head ") {
			gitInfo.Branch = strings.TrimSpace(strings.TrimPrefix(trimmed, "# branch.head "))
			if gitInfo.Branch == "(detached)" {
				gitInfo.Branch = "detached"
			}
			continue
		}
		if strings.HasPrefix(trimmed, "# branch.ab ") {
			fields := strings.Fields(strings.TrimPrefix(trimmed, "# branch.ab "))
			for _, field := range fields {
				if strings.HasPrefix(field, "+") {
					fmt.Sscanf(strings.TrimPrefix(field, "+"), "%d", &gitInfo.Ahead)
				}
				if strings.HasPrefix(field, "-") {
					fmt.Sscanf(strings.TrimPrefix(field, "-"), "%d", &gitInfo.Behind)
				}
			}
			continue
		}
		if !strings.HasPrefix(trimmed, "#") {
			gitInfo.Dirty = true
		}
	}
	if gitInfo.Branch == "" {
		gitInfo.Branch = "unknown"
	}
	return gitInfo
}

func statusGitShouldHide(err error, output string) bool {
	if err == nil {
		return false
	}
	text := strings.ToLower(strings.TrimSpace(output))
	if text == "" {
		text = strings.ToLower(err.Error())
	}
	return strings.Contains(text, "not a git repository") || strings.Contains(text, "cannot change to") || strings.Contains(text, "no such file")
}

func statusGitError(err error, output string) string {
	message := strings.TrimSpace(output)
	if message == "" && err != nil {
		message = strings.TrimSpace(err.Error())
	}
	if message == "" {
		return "git status failed"
	}
	return "git status failed: " + message
}

func collectSubscriptionStatus(ctx context.Context, req uiStatusRequest, state auth.State, authStateErr error) uiStatusSubscriptionInfo {
	if !statusShouldFetchSubscriptionUsage(req.Settings, state) {
		return uiStatusSubscriptionInfo{}
	}
	if authStateErr != nil {
		return uiStatusSubscriptionInfo{Applicable: true, Summary: "Subscription unavailable: " + authStateErr.Error()}
	}
	payload, err := statusUsagePayloadFetcher(ctx, statusUsageBaseURL, state)
	if err != nil {
		return uiStatusSubscriptionInfo{Applicable: true, Summary: "Subscription unavailable: " + err.Error()}
	}
	windows := statusUsageWindowsByLabel(payload)
	summary := statusSubscriptionPlanSummary(payload.PlanType)
	return uiStatusSubscriptionInfo{Applicable: true, Summary: summary, Windows: windows}
}

func statusShouldFetchSubscriptionUsage(settings config.Settings, state auth.State) bool {
	if state.Method.Type != auth.MethodOAuth || state.Method.OAuth == nil {
		return false
	}
	if strings.TrimSpace(settings.ProviderOverride) != "" {
		return false
	}
	if strings.TrimSpace(settings.OpenAIBaseURL) != "" {
		return false
	}
	return true
}

func statusSubscriptionPlanSummary(plan string) string {
	trimmed := strings.TrimSpace(plan)
	if trimmed == "" {
		return "Subscription"
	}
	normalized := strings.ToLower(trimmed)
	return strings.ToUpper(normalized[:1]) + normalized[1:] + " subscription"
}

func fetchStatusUsagePayload(ctx context.Context, baseURL string, state auth.State) (statusUsagePayload, error) {
	authorization, err := state.Method.AuthHeaderValue()
	if err != nil {
		return statusUsagePayload{}, err
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, baseURL+"/wham/usage", nil)
	if err != nil {
		return statusUsagePayload{}, err
	}
	request.Header.Set("Authorization", authorization)
	request.Header.Set("User-Agent", "builder/dev")
	if accountID := strings.TrimSpace(state.Method.OAuth.AccountID); accountID != "" {
		request.Header.Set("ChatGPT-Account-Id", accountID)
	}
	response, err := (&http.Client{Timeout: statusRefreshTimeout}).Do(request)
	if err != nil {
		return statusUsagePayload{}, err
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return statusUsagePayload{}, fmt.Errorf("usage request failed: %s", response.Status)
	}
	var payload statusUsagePayload
	if err := json.NewDecoder(response.Body).Decode(&payload); err != nil {
		return statusUsagePayload{}, fmt.Errorf("decode usage response: %w", err)
	}
	return payload, nil
}

func statusUsageWindowsByLabel(payload statusUsagePayload) []uiStatusSubscriptionWindow {
	type orderedWindow struct {
		window        uiStatusSubscriptionWindow
		durationSecs  int
		discoveryRank int
	}
	qualifierCounts := map[string]int{}
	ordered := make([]orderedWindow, 0, 2+len(payload.AdditionalRateLimits)*2)
	discoveryRank := 0
	addWindow := func(window *statusUsageWindow, qualifier string) {
		if window == nil {
			return
		}
		label := statusLimitDuration(window.LimitWindowSeconds / 60)
		if label == "" {
			return
		}
		snapshot := uiStatusSubscriptionWindow{
			Label:       label,
			Qualifier:   qualifier,
			UsedPercent: window.UsedPercent,
		}
		if window.ResetAt > 0 {
			snapshot.ResetAt = time.Unix(window.ResetAt, 0).UTC()
		}
		ordered = append(ordered, orderedWindow{
			window:        snapshot,
			durationSecs:  window.LimitWindowSeconds,
			discoveryRank: discoveryRank,
		})
		discoveryRank++
	}
	if payload.RateLimit != nil {
		addWindow(payload.RateLimit.PrimaryWindow, "")
		addWindow(payload.RateLimit.SecondaryWindow, "")
	}
	for _, extra := range payload.AdditionalRateLimits {
		if extra.RateLimit == nil {
			continue
		}
		qualifier := statusUsageWindowQualifier(extra, qualifierCounts)
		addWindow(extra.RateLimit.PrimaryWindow, qualifier)
		addWindow(extra.RateLimit.SecondaryWindow, qualifier)
	}
	sort.SliceStable(ordered, func(i, j int) bool {
		if ordered[i].durationSecs != ordered[j].durationSecs {
			return ordered[i].durationSecs < ordered[j].durationSecs
		}
		return ordered[i].discoveryRank < ordered[j].discoveryRank
	})
	windows := make([]uiStatusSubscriptionWindow, 0, len(ordered))
	for _, window := range ordered {
		windows = append(windows, window.window)
	}
	return windows
}

func statusUsageWindowQualifier(bucket statusUsageExtraBucket, counts map[string]int) string {
	limitName := strings.TrimSpace(bucket.LimitName)
	feature := strings.TrimSpace(bucket.MeteredFeature)
	base := ""
	switch {
	case limitName == "" && feature == "":
		base = "extra"
	case limitName == "":
		base = feature
	case feature == "" || strings.EqualFold(limitName, feature):
		base = limitName
	default:
		base = limitName + " / " + feature
	}
	counts[base]++
	if counts[base] == 1 {
		return base
	}
	return fmt.Sprintf("%s #%d", base, counts[base])
}

func statusLimitDuration(windowMinutes int) string {
	const minutesPerHour = 60
	const minutesPerDay = 24 * minutesPerHour
	const minutesPerWeek = 7 * minutesPerDay
	const minutesPerMonth = 30 * minutesPerDay
	const roundingBiasMinutes = 3

	if windowMinutes < 0 {
		windowMinutes = 0
	}
	if windowMinutes <= minutesPerDay+roundingBiasMinutes {
		hours := (windowMinutes + roundingBiasMinutes) / minutesPerHour
		if hours < 1 {
			hours = 1
		}
		return fmt.Sprintf("%dh", hours)
	}
	if windowMinutes <= minutesPerWeek+roundingBiasMinutes {
		return "weekly"
	}
	if windowMinutes <= minutesPerMonth+roundingBiasMinutes {
		return "monthly"
	}
	return "annual"
}

func statusAuthInfo(state auth.State, settings config.Settings, statusErr error) uiStatusAuthInfo {
	if statusErr != nil && !state.IsConfigured() {
		return uiStatusAuthInfo{Summary: "Auth unavailable", Details: []string{statusErr.Error()}}
	}
	details := make([]string, 0, 2)
	baseURL := strings.TrimSpace(settings.OpenAIBaseURL)
	if baseURL != "" && !statusIsDefaultOpenAIBaseURL(baseURL) {
		details = append(details, filepath.ToSlash(baseURL))
	}
	switch state.Method.Type {
	case auth.MethodOAuth:
		summary := "Subscription"
		if state.Method.OAuth != nil && strings.TrimSpace(state.Method.OAuth.Email) != "" {
			summary = strings.TrimSpace(state.Method.OAuth.Email)
		}
		if statusErr != nil {
			details = append(details, statusErr.Error())
		}
		return uiStatusAuthInfo{Summary: summary, Details: details}
	case auth.MethodAPIKey:
		summary := "API key"
		if provider := statusProviderLabel(state, settings); provider != "" {
			details = append(details, provider)
		}
		if pref := statusEnvPreferenceLabel(state.EnvAPIKeyPreference); pref != "" {
			details = append(details, pref)
		}
		if statusErr != nil {
			details = append(details, statusErr.Error())
		}
		return uiStatusAuthInfo{Summary: summary, Details: details}
	default:
		if statusErr != nil {
			return uiStatusAuthInfo{Summary: "Not configured", Details: []string{statusErr.Error()}}
		}
		return uiStatusAuthInfo{Summary: "Not configured"}
	}
}

func statusEstimateSkillTokens(skills []runtime.SkillInspection) map[string]int {
	paths := make([]string, 0, len(skills))
	for _, skill := range skills {
		if !skill.Loaded {
			continue
		}
		path := strings.TrimSpace(skill.Path)
		if path == "" {
			continue
		}
		paths = append(paths, path)
	}
	return statusEstimatePathTokens(paths)
}

func statusEstimatePathTokens(paths []string) map[string]int {
	counts := map[string]int{}
	for _, rawPath := range paths {
		path := strings.TrimSpace(rawPath)
		if path == "" {
			continue
		}
		contents, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		counts[path] = tokenutil.ApproxTextTokenCount(string(contents))
	}
	return counts
}

func statusProviderLabel(state auth.State, settings config.Settings) string {
	providerOverride := strings.ToLower(strings.TrimSpace(settings.ProviderOverride))
	if providerOverride != "" {
		return providerOverride
	}
	if state.Method.Type == auth.MethodOAuth {
		return "chatgpt-codex"
	}
	if strings.TrimSpace(settings.OpenAIBaseURL) != "" {
		return "openai-compatible"
	}
	return "openai"
}

func statusEnvPreferenceLabel(preference auth.EnvAPIKeyPreference) string {
	switch preference {
	case auth.EnvAPIKeyPreferencePreferEnv:
		return "prefer env"
	case auth.EnvAPIKeyPreferencePreferSaved:
		return "prefer saved"
	default:
		return ""
	}
}

func statusConfigOverrideSources(src config.SourceReport) []string {
	present := map[string]bool{}
	for _, source := range src.Sources {
		switch strings.TrimSpace(source) {
		case "env":
			present["ENV"] = true
		case "cli":
			present["CLI ARGS"] = true
		}
	}
	ordered := make([]string, 0, len(present))
	for _, label := range []string{"ENV", "CLI ARGS"} {
		if present[label] {
			ordered = append(ordered, label)
		}
	}
	return ordered
}

func statusIsDefaultOpenAIBaseURL(baseURL string) bool {
	trimmed := strings.TrimRight(strings.TrimSpace(baseURL), "/")
	return trimmed == "" || trimmed == "https://chatgpt.com" || trimmed == "https://chatgpt.com/backend-api" || trimmed == "https://chat.openai.com" || trimmed == "https://chat.openai.com/backend-api"
}

func statusModelSummary(req uiStatusRequest) string {
	resolved := strings.TrimSpace(req.ModelName)
	configured := strings.TrimSpace(req.ConfiguredModelName)
	modelName := resolved
	if modelName == "" {
		modelName = configured
	}
	if modelName == "" {
		modelName = "<unset>"
	}
	parts := []string{llm.ModelDisplayLabel(modelName, strings.TrimSpace(req.ThinkingLevel))}
	if req.FastModeAvailable && req.FastModeEnabled {
		parts = append(parts, "fast")
	}
	return strings.Join(parts, " ")
}

func statusSupervisorLabel(enabled bool, mode string) string {
	if !enabled {
		return "off"
	}
	trimmed := strings.TrimSpace(mode)
	if trimmed == "" || trimmed == "off" {
		return "on"
	}
	return trimmed
}

func statusOnOff(value bool) string {
	if value {
		return "on"
	}
	return "off"
}

func statusYesNo(value bool) string {
	if value {
		return "yes"
	}
	return "no"
}

func (m *uiModel) openStatusOverlay() {
	m.statusVisible = true
	m.statusScroll = 0
	m.statusError = ""
	m.statusLoading = false
	m.statusPendingSections = nil
	m.statusSectionWarnings = nil
	if m.statusCollector == nil {
		m.statusCollector = defaultUIStatusCollector{}
	}
}

func (m *uiModel) closeStatusOverlay() {
	m.statusVisible = false
	m.statusOverlayPushed = false
	m.statusScroll = 0
	m.statusLoading = false
	m.statusPendingSections = nil
	m.statusSectionWarnings = nil
}

func (m *uiModel) startStatusSectionRefresh(sections ...uiStatusSection) {
	if len(sections) == 0 {
		m.statusLoading = false
		return
	}
	if m.statusPendingSections == nil {
		m.statusPendingSections = map[uiStatusSection]bool{}
	}
	if m.statusSectionWarnings == nil {
		m.statusSectionWarnings = map[uiStatusSection]string{}
	}
	for _, section := range sections {
		m.statusPendingSections[section] = true
		delete(m.statusSectionWarnings, section)
	}
	m.statusLoading = len(m.statusPendingSections) > 0
}

func (m *uiModel) finishStatusSectionRefresh(section uiStatusSection, warning string) {
	if m.statusPendingSections != nil {
		delete(m.statusPendingSections, section)
	}
	if m.statusSectionWarnings == nil {
		m.statusSectionWarnings = map[uiStatusSection]string{}
	}
	if strings.TrimSpace(warning) == "" {
		delete(m.statusSectionWarnings, section)
	} else {
		m.statusSectionWarnings[section] = strings.TrimSpace(warning)
	}
	m.statusLoading = len(m.statusPendingSections) > 0
	m.statusSnapshot.CollectorWarning = m.statusCombinedWarnings()
}

func (m *uiModel) statusCombinedWarnings() string {
	if len(m.statusSectionWarnings) == 0 {
		return ""
	}
	parts := make([]string, 0, len(m.statusSectionWarnings))
	for _, section := range []uiStatusSection{uiStatusSectionBase, uiStatusSectionEnvironment, uiStatusSectionGit, uiStatusSectionAuth} {
		if warning := strings.TrimSpace(m.statusSectionWarnings[section]); warning != "" {
			parts = append(parts, warning)
		}
	}
	return strings.Join(parts, " | ")
}

func (m *uiModel) pushStatusOverlayIfNeeded() tea.Cmd {
	if m.statusOverlayPushed {
		return nil
	}
	if m.view.Mode() != tui.ModeOngoing {
		return nil
	}
	m.statusOverlayPushed = true
	if transitionCmd := m.toggleTranscriptModeWithOptions(true, true); transitionCmd != nil {
		return transitionCmd
	}
	return tea.ClearScreen
}

func (m *uiModel) popStatusOverlayIfNeeded() tea.Cmd {
	if !m.statusOverlayPushed {
		return nil
	}
	m.statusOverlayPushed = false
	if m.view.Mode() != tui.ModeDetail {
		return nil
	}
	if transitionCmd := m.toggleTranscriptMode(); transitionCmd != nil {
		return transitionCmd
	}
	return tea.ClearScreen
}

func (m *uiModel) moveStatusScroll(delta int) {
	m.statusScroll += delta
	if m.statusScroll < 0 {
		m.statusScroll = 0
	}
}

func (m *uiModel) moveStatusScrollPage(deltaPages int) {
	rowsPerPage := m.statusRowsPerPage()
	m.moveStatusScroll(deltaPages * rowsPerPage)
}

func (m *uiModel) statusRowsPerPage() int {
	available := m.termHeight - 1
	if available < 1 {
		return 1
	}
	return available
}

func (m *uiModel) statusRefreshCmd() tea.Cmd {
	m.statusRefreshToken++
	token := m.statusRefreshToken
	request := m.newStatusRequest(time.Now())
	collector := m.statusCollector
	if collector == nil {
		collector = defaultUIStatusCollector{}
	}
	if progressive, ok := collector.(uiStatusProgressiveCollector); ok {
		base := progressive.CollectBase(request)
		seed := uiStatusSeedResult{Snapshot: base}
		if m.statusRepository != nil {
			seed = m.statusRepository.SeedSnapshot(request, base, request.CurrentTime)
		}
		m.statusSnapshot = seed.Snapshot
		m.statusError = ""
		m.statusPendingSections = nil
		m.statusSectionWarnings = seed.Warnings
		m.startStatusSectionRefresh(seed.PendingSections...)
		cmds := make([]tea.Cmd, 0, len(seed.PendingSections))
		for _, section := range seed.PendingSections {
			switch section {
			case uiStatusSectionAuth:
				cmds = append(cmds, m.statusAuthRefreshCmd(token, statusAuthCacheKey(request), request, progressive, base))
			case uiStatusSectionGit:
				cmds = append(cmds, m.statusGitRefreshCmd(token, statusGitCacheKey(base.Workdir), request, progressive, base))
			case uiStatusSectionEnvironment:
				cmds = append(cmds, m.statusEnvironmentRefreshCmd(token, statusEnvironmentCacheKey(request), request, progressive, base))
			}
		}
		if len(cmds) == 0 {
			m.statusLoading = false
			m.statusSnapshot.CollectorWarning = m.statusCombinedWarnings()
			return nil
		}
		return tea.Batch(cmds...)
	}
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), statusRefreshTimeout)
		defer cancel()
		snapshot, err := collector.Collect(ctx, request)
		return statusRefreshDoneMsg{token: token, snapshot: snapshot, err: err}
	}
}

func (m *uiModel) statusBaseRefreshCmd(token uint64, request uiStatusRequest, collector uiStatusProgressiveCollector) tea.Cmd {
	return func() tea.Msg {
		return statusBaseRefreshDoneMsg{token: token, snapshot: collector.CollectBase(request)}
	}
}

func (m *uiModel) statusAuthRefreshCmd(token uint64, cacheKey string, request uiStatusRequest, collector uiStatusProgressiveCollector, base uiStatusSnapshot) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), statusRefreshTimeout)
		defer cancel()
		return statusAuthRefreshDoneMsg{token: token, cacheKey: cacheKey, result: collector.CollectAuth(ctx, request, base)}
	}
}

func (m *uiModel) statusGitRefreshCmd(token uint64, cacheKey string, request uiStatusRequest, collector uiStatusProgressiveCollector, base uiStatusSnapshot) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), statusRefreshTimeout)
		defer cancel()
		return statusGitRefreshDoneMsg{token: token, cacheKey: cacheKey, result: collector.CollectGit(ctx, request, base)}
	}
}

func (m *uiModel) statusEnvironmentRefreshCmd(token uint64, cacheKey string, request uiStatusRequest, collector uiStatusProgressiveCollector, base uiStatusSnapshot) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), statusRefreshTimeout)
		defer cancel()
		return statusEnvironmentRefreshDoneMsg{token: token, cacheKey: cacheKey, result: collector.CollectEnvironment(ctx, request, base)}
	}
}

func (c uiInputController) startStatusFlowCmd() tea.Cmd {
	m := c.model
	m.openStatusOverlay()
	refreshCmd := m.statusRefreshCmd()
	if overlayCmd := m.pushStatusOverlayIfNeeded(); overlayCmd != nil {
		return tea.Batch(overlayCmd, refreshCmd)
	}
	return refreshCmd
}

func (c uiInputController) stopStatusFlowCmd() tea.Cmd {
	m := c.model
	overlayCmd := m.popStatusOverlayIfNeeded()
	m.closeStatusOverlay()
	if overlayCmd != nil {
		return overlayCmd
	}
	return nil
}

func (c uiInputController) handleStatusOverlayKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	m := c.model
	switch strings.ToLower(msg.String()) {
	case "ctrl+c":
		if m.busy {
			if m.engine != nil {
				_ = m.engine.Interrupt()
			}
			m.preSubmitCheckToken++
			c.releaseLockedInjectedInput(true)
			c.restorePendingInjectedIntoInput()
			c.restoreQueuedMessagesIntoInput()
			m.pendingPreSubmitText = ""
			m.busy = false
			m.activity = uiActivityInterrupted
			m.clearReviewerState()
			return m, nil
		}
		m.exitAction = UIActionExit
		if overlayCmd := m.popStatusOverlayIfNeeded(); overlayCmd != nil {
			m.closeStatusOverlay()
			return m, tea.Sequence(overlayCmd, tea.Quit)
		}
		return m, tea.Quit
	case "esc", "q":
		return m, c.stopStatusFlowCmd()
	case "up":
		m.moveStatusScroll(-1)
		return m, nil
	case "down":
		m.moveStatusScroll(1)
		return m, nil
	case "pgup":
		m.moveStatusScrollPage(-1)
		return m, nil
	case "pgdown":
		m.moveStatusScrollPage(1)
		return m, nil
	case "home":
		m.statusScroll = 0
		return m, nil
	case "end":
		m.statusScroll = 1 << 30
		return m, nil
	default:
		return m, nil
	}
}
