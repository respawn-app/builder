package idempotency

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"builder/server/metadata"
	"builder/server/primaryrun"
	"builder/shared/serverapi"
)

const DefaultRetention = 10 * time.Minute

type Store interface {
	GetMutationDedupRecord(ctx context.Context, method string, resourceID string, clientRequestID string) (metadata.MutationDedupRecord, bool, error)
	UpsertMutationDedupRecord(ctx context.Context, record metadata.MutationDedupRecord) error
	DeleteExpiredMutationDedupRecords(ctx context.Context, expiresAt time.Time) (int64, error)
}

type Request struct {
	Method             string
	ResourceID         string
	ClientRequestID    string
	PayloadFingerprint string
}

type CachePolicy struct {
	ShouldCacheError func(error) bool
}

type JSONCodec[T any] struct{}

func (JSONCodec[T]) Marshal(value T) (string, error) {
	body, err := json.Marshal(value)
	if err != nil {
		return "", err
	}
	return string(body), nil
}

func (JSONCodec[T]) Unmarshal(raw string) (T, error) {
	var out T
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		trimmed = "null"
	}
	if err := json.Unmarshal([]byte(trimmed), &out); err != nil {
		return out, err
	}
	return out, nil
}

type entry struct {
	request     Request
	record      metadata.MutationDedupRecord
	done        bool
	cacheable   bool
	completedAt time.Time
	ready       chan struct{}
}

type Coordinator struct {
	store     Store
	retention time.Duration
	now       func() time.Time

	mu      sync.Mutex
	entries map[string]*entry
}

func NewCoordinator(store Store, retention time.Duration) *Coordinator {
	if retention <= 0 {
		retention = DefaultRetention
	}
	return &Coordinator{
		store:     store,
		retention: retention,
		now:       time.Now,
		entries:   map[string]*entry{},
	}
}

func FingerprintPayload(payload any) (string, error) {
	body, err := json.Marshal(payload)
	if err != nil {
		return "", fmt.Errorf("marshal dedupe payload: %w", err)
	}
	sum := sha256.Sum256(body)
	return hex.EncodeToString(sum[:]), nil
}

func Execute[T any](ctx context.Context, c *Coordinator, req Request, codec JSONCodec[T], fn func(context.Context) (T, error)) (T, error) {
	return ExecuteWithPolicy(ctx, c, req, codec, CachePolicy{}, fn)
}

func ExecuteWithPolicy[T any](ctx context.Context, c *Coordinator, req Request, codec JSONCodec[T], policy CachePolicy, fn func(context.Context) (T, error)) (T, error) {
	var zero T
	if c == nil {
		return zero, errors.New("idempotency coordinator is required")
	}
	if err := req.Validate(); err != nil {
		return zero, err
	}
	key := req.key()
	for {
		c.mu.Lock()
		c.sweepExpiredEntriesLocked(c.now())
		if existing, ok := c.entries[key]; ok {
			if existing.request.PayloadFingerprint != req.PayloadFingerprint {
				c.mu.Unlock()
				return zero, payloadMismatchError(req.ClientRequestID)
			}
			if existing.done {
				if existing.cacheable {
					record := existing.record
					c.mu.Unlock()
					return decodeRecord(record, codec)
				}
				delete(c.entries, key)
				c.mu.Unlock()
				continue
			}
			ready := existing.ready
			c.mu.Unlock()
			select {
			case <-ready:
				continue
			case <-ctx.Done():
				return zero, ctx.Err()
			}
		}
		if c.store != nil {
			record, found, err := c.store.GetMutationDedupRecord(ctx, req.Method, req.ResourceID, req.ClientRequestID)
			if err != nil {
				c.mu.Unlock()
				return zero, err
			}
			if found {
				c.mu.Unlock()
				if record.PayloadFingerprint != req.PayloadFingerprint {
					return zero, payloadMismatchError(req.ClientRequestID)
				}
				return decodeRecord(record, codec)
			}
		}
		pending := &entry{request: req, ready: make(chan struct{})}
		c.entries[key] = pending
		c.mu.Unlock()

		response, err := fn(ctx)
		cacheable := shouldCache(err, policy)
		completedAt := c.now().UTC()
		if !cacheable {
			c.mu.Lock()
			delete(c.entries, key)
			close(pending.ready)
			c.mu.Unlock()
			return response, err
		}

		encodedResponse, encodeErr := codec.Marshal(response)
		if encodeErr != nil {
			c.mu.Lock()
			delete(c.entries, key)
			close(pending.ready)
			c.mu.Unlock()
			return zero, fmt.Errorf("encode idempotent response: %w", encodeErr)
		}
		errorCode, errorMessage := encodeReplayableError(err)
		record := metadata.MutationDedupRecord{
			Method:             req.Method,
			ResourceID:         req.ResourceID,
			ClientRequestID:    req.ClientRequestID,
			PayloadFingerprint: req.PayloadFingerprint,
			ResponseJSON:       encodedResponse,
			ErrorCode:          errorCode,
			ErrorMessage:       errorMessage,
			CompletedAt:        completedAt,
			ExpiresAt:          completedAt.Add(c.retention),
			MetadataJSON:       "{}",
		}
		if c.store != nil {
			_, _ = c.store.DeleteExpiredMutationDedupRecords(ctx, completedAt)
			_ = c.store.UpsertMutationDedupRecord(ctx, record)
		}

		c.mu.Lock()
		pending.record = record
		pending.done = true
		pending.cacheable = true
		pending.completedAt = completedAt
		close(pending.ready)
		c.mu.Unlock()
		return response, err
	}
}

func shouldCache(err error, policy CachePolicy) bool {
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return false
	}
	if policy.ShouldCacheError != nil {
		return policy.ShouldCacheError(err)
	}
	return true
}

func (c *Coordinator) sweepExpiredEntriesLocked(now time.Time) {
	for key, existing := range c.entries {
		if existing == nil || !existing.done || existing.completedAt.IsZero() {
			continue
		}
		if now.Sub(existing.completedAt) >= c.retention {
			delete(c.entries, key)
		}
	}
}

func (r Request) Validate() error {
	if strings.TrimSpace(r.Method) == "" {
		return errors.New("method is required")
	}
	if strings.TrimSpace(r.ResourceID) == "" {
		return errors.New("resource_id is required")
	}
	if strings.TrimSpace(r.ClientRequestID) == "" {
		return errors.New("client_request_id is required")
	}
	if strings.TrimSpace(r.PayloadFingerprint) == "" {
		return errors.New("payload_fingerprint is required")
	}
	return nil
}

func (r Request) key() string {
	return strings.Join([]string{strings.TrimSpace(r.Method), strings.TrimSpace(r.ResourceID), strings.TrimSpace(r.ClientRequestID)}, "|")
}

func decodeRecord[T any](record metadata.MutationDedupRecord, codec JSONCodec[T]) (T, error) {
	response, err := codec.Unmarshal(record.ResponseJSON)
	if err != nil {
		var zero T
		return zero, fmt.Errorf("decode idempotent response: %w", err)
	}
	if strings.TrimSpace(record.ErrorCode) != "" || strings.TrimSpace(record.ErrorMessage) != "" {
		return response, decodeReplayableError(record.ErrorCode, record.ErrorMessage)
	}
	return response, nil
}

func payloadMismatchError(clientRequestID string) error {
	return fmt.Errorf("client_request_id %q reused with different payload", clientRequestID)
}

func encodeReplayableError(err error) (string, string) {
	if err == nil {
		return "", ""
	}
	message := strings.TrimSpace(err.Error())
	switch {
	case errors.Is(err, serverapi.ErrWorkspaceNotRegistered):
		return "workspace_not_registered", message
	case errors.Is(err, serverapi.ErrProjectNotFound):
		return "project_not_found", message
	case errors.Is(err, serverapi.ErrProjectUnavailable):
		return "project_unavailable", message
	case errors.Is(err, serverapi.ErrSessionAlreadyControlled):
		return "session_already_controlled", message
	case errors.Is(err, serverapi.ErrInvalidControllerLease):
		return "invalid_controller_lease", message
	case errors.Is(err, serverapi.ErrPromptNotFound):
		return "prompt_not_found", message
	case errors.Is(err, serverapi.ErrPromptAlreadyResolved):
		return "prompt_already_resolved", message
	case errors.Is(err, serverapi.ErrPromptUnsupported):
		return "prompt_unsupported", message
	case errors.Is(err, serverapi.ErrStreamGap):
		return "stream_gap", message
	case errors.Is(err, serverapi.ErrStreamUnavailable):
		return "stream_unavailable", message
	case errors.Is(err, serverapi.ErrStreamFailed):
		return "stream_failed", message
	case errors.Is(err, primaryrun.ErrActivePrimaryRun):
		return "active_primary_run", message
	default:
		return "generic", message
	}
}

func decodeReplayableError(code string, message string) error {
	trimmedCode := strings.TrimSpace(code)
	trimmedMessage := strings.TrimSpace(message)
	if trimmedCode == "" && trimmedMessage == "" {
		return nil
	}
	if trimmedMessage == "" {
		trimmedMessage = "request failed"
	}
	wrapped := errors.New(trimmedMessage)
	switch trimmedCode {
	case "workspace_not_registered":
		return errors.Join(serverapi.ErrWorkspaceNotRegistered, wrapped)
	case "project_not_found":
		return errors.Join(serverapi.ErrProjectNotFound, wrapped)
	case "project_unavailable":
		return errors.Join(serverapi.ErrProjectUnavailable, wrapped)
	case "session_already_controlled":
		return errors.Join(serverapi.ErrSessionAlreadyControlled, wrapped)
	case "invalid_controller_lease":
		return errors.Join(serverapi.ErrInvalidControllerLease, wrapped)
	case "prompt_not_found":
		return errors.Join(serverapi.ErrPromptNotFound, wrapped)
	case "prompt_already_resolved":
		return errors.Join(serverapi.ErrPromptAlreadyResolved, wrapped)
	case "prompt_unsupported":
		return errors.Join(serverapi.ErrPromptUnsupported, wrapped)
	case "stream_gap":
		return errors.Join(serverapi.ErrStreamGap, wrapped)
	case "stream_unavailable":
		return errors.Join(serverapi.ErrStreamUnavailable, wrapped)
	case "stream_failed":
		return errors.Join(serverapi.ErrStreamFailed, wrapped)
	case "active_primary_run":
		return errors.Join(primaryrun.ErrActivePrimaryRun, wrapped)
	default:
		return wrapped
	}
}
