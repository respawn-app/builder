package app

import (
	"context"
	"crypto/sha256"
	"fmt"
	"path"
	"strings"
	"sync"
	"time"

	"builder/server/auth"
	"builder/server/runtime"
)

const (
	statusAuthCacheFreshness        = time.Minute
	statusGitCacheFreshness         = 5 * time.Second
	statusEnvironmentCacheFreshness = 5 * time.Minute
)

type uiStatusRepository interface {
	SeedSnapshot(req uiStatusRequest, base uiStatusSnapshot, now time.Time) uiStatusSeedResult
	StoreAuth(cacheKey string, result uiStatusAuthStageResult, now time.Time)
	StoreGit(cacheKey string, result uiStatusGitStageResult, now time.Time)
	StoreEnvironment(cacheKey string, result uiStatusEnvironmentStageResult, now time.Time)
}

type uiStatusSeedResult struct {
	Snapshot        uiStatusSnapshot
	PendingSections []uiStatusSection
	Warnings        map[uiStatusSection]string
}

type memoryUIStatusRepository struct {
	mu        sync.Mutex
	authByKey map[string]uiStatusAuthCacheEntry
	gitByKey  map[string]uiStatusGitCacheEntry
	envByKey  map[string]uiStatusEnvironmentCacheEntry
}

type uiStatusAuthCacheEntry struct {
	fetchedAt time.Time
	result    uiStatusAuthStageResult
}

type uiStatusGitCacheEntry struct {
	fetchedAt time.Time
	result    uiStatusGitStageResult
}

type uiStatusEnvironmentCacheEntry struct {
	fetchedAt time.Time
	result    uiStatusEnvironmentStageResult
}

func newMemoryUIStatusRepository() uiStatusRepository {
	return &memoryUIStatusRepository{
		authByKey: map[string]uiStatusAuthCacheEntry{},
		gitByKey:  map[string]uiStatusGitCacheEntry{},
		envByKey:  map[string]uiStatusEnvironmentCacheEntry{},
	}
}

func (r *memoryUIStatusRepository) SeedSnapshot(req uiStatusRequest, base uiStatusSnapshot, now time.Time) uiStatusSeedResult {
	r.mu.Lock()
	defer r.mu.Unlock()

	seed := uiStatusSeedResult{Snapshot: base, Warnings: map[uiStatusSection]string{}}

	authEntry, authCached := r.authByKey[statusAuthCacheKey(req)]
	if authCached {
		seed.Snapshot.Auth = authEntry.result.Auth
		seed.Snapshot.Subscription = authEntry.result.Subscription
		if warning := strings.TrimSpace(authEntry.result.Warning); warning != "" {
			seed.Warnings[uiStatusSectionAuth] = warning
		}
	}
	if !authCached || now.Sub(authEntry.fetchedAt) > statusAuthCacheFreshness {
		seed.PendingSections = append(seed.PendingSections, uiStatusSectionAuth)
	}

	gitEntry, gitCached := r.gitByKey[statusGitCacheKey(statusGitRoot(req))]
	if gitCached {
		seed.Snapshot.Git = gitEntry.result.Git
	}
	if !gitCached || !gitEntry.result.Git.Visible || now.Sub(gitEntry.fetchedAt) > statusGitCacheFreshness {
		seed.PendingSections = append(seed.PendingSections, uiStatusSectionGit)
	}

	envEntry, envCached := r.envByKey[statusEnvironmentCacheKey(req)]
	if envCached {
		seed.Snapshot.Skills = append([]runtime.SkillInspection(nil), envEntry.result.Skills...)
		seed.Snapshot.SkillTokenCounts = cloneStatusTokenMap(envEntry.result.SkillTokenCounts)
		seed.Snapshot.AgentsPaths = append([]string(nil), envEntry.result.AgentsPaths...)
		seed.Snapshot.AgentTokenCounts = cloneStatusTokenMap(envEntry.result.AgentTokenCounts)
		if warning := strings.TrimSpace(envEntry.result.CollectorWarning); warning != "" {
			seed.Warnings[uiStatusSectionEnvironment] = warning
		}
	}
	if !envCached || now.Sub(envEntry.fetchedAt) > statusEnvironmentCacheFreshness {
		seed.PendingSections = append(seed.PendingSections, uiStatusSectionEnvironment)
	}

	if len(seed.Warnings) == 0 {
		seed.Warnings = nil
	}
	return seed
}

func (r *memoryUIStatusRepository) StoreAuth(cacheKey string, result uiStatusAuthStageResult, now time.Time) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if strings.TrimSpace(cacheKey) == "" {
		return
	}
	r.authByKey[cacheKey] = uiStatusAuthCacheEntry{fetchedAt: statusRepositoryTime(now), result: result}
}

func (r *memoryUIStatusRepository) StoreGit(cacheKey string, result uiStatusGitStageResult, now time.Time) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if strings.TrimSpace(cacheKey) == "" {
		return
	}
	r.gitByKey[cacheKey] = uiStatusGitCacheEntry{fetchedAt: statusRepositoryTime(now), result: result}
}

func (r *memoryUIStatusRepository) StoreEnvironment(cacheKey string, result uiStatusEnvironmentStageResult, now time.Time) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if strings.TrimSpace(cacheKey) == "" {
		return
	}
	r.envByKey[cacheKey] = uiStatusEnvironmentCacheEntry{fetchedAt: statusRepositoryTime(now), result: result}
}

func statusRepositoryTime(now time.Time) time.Time {
	if now.IsZero() {
		return time.Now()
	}
	return now
}

func statusAuthCacheKey(req uiStatusRequest) string {
	identity := statusAuthCacheIdentity(req)
	return strings.Join([]string{
		strings.TrimSpace(req.Settings.OpenAIBaseURL),
		strings.TrimSpace(req.Settings.ProviderOverride),
		identity,
	}, "|")
}

func statusGitCacheKey(workdir string) string {
	trimmed := strings.TrimSpace(workdir)
	if trimmed == "" {
		return ""
	}
	normalized := strings.ReplaceAll(trimmed, "\\", "/")
	return path.Clean(normalized)
}

func statusEnvironmentCacheKey(req uiStatusRequest) string {
	return strings.TrimSpace(req.WorkspaceRoot)
}

func cloneStatusTokenMap(input map[string]int) map[string]int {
	if len(input) == 0 {
		return nil
	}
	cloned := make(map[string]int, len(input))
	for key, value := range input {
		cloned[key] = value
	}
	return cloned
}

func statusAuthCacheIdentity(req uiStatusRequest) string {
	if req.AuthManager == nil {
		return "auth:none"
	}
	state, err := req.AuthManager.Load(context.Background())
	if err != nil {
		return "auth:error"
	}
	return statusAuthIdentity(state)
}

func statusAuthIdentity(state auth.State) string {
	switch state.Method.Type {
	case auth.MethodOAuth:
		oauth := state.Method.OAuth
		if oauth == nil {
			return "oauth"
		}
		parts := []string{
			"oauth",
			strings.TrimSpace(oauth.AccountID),
			strings.TrimSpace(oauth.Email),
		}
		if parts[1] == "" && parts[2] == "" {
			parts = append(parts, statusOpaqueOAuthIdentity(*oauth))
		}
		return strings.Join(parts, "|")
	case auth.MethodAPIKey:
		return strings.Join([]string{
			"apikey",
			string(state.EnvAPIKeyPreference),
		}, "|")
	default:
		return "auth:none"
	}
}

func statusOpaqueOAuthIdentity(oauth auth.OAuthMethod) string {
	token := strings.TrimSpace(oauth.RefreshToken)
	if token == "" {
		token = strings.TrimSpace(oauth.AccessToken)
	}
	if token == "" {
		return "opaque"
	}
	sum := sha256.Sum256([]byte(token))
	return fmt.Sprintf("opaque:%x", sum[:8])
}
