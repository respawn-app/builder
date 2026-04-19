package requestmemo

import (
	"context"
	"testing"
	"time"
)

func TestMemoDoesNotReplayCanceledOrDeadlineExceededOutcome(t *testing.T) {
	tests := []struct {
		name    string
		wantErr error
	}{
		{
			name:    "canceled",
			wantErr: context.Canceled,
		},
		{
			name:    "deadline exceeded",
			wantErr: context.DeadlineExceeded,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			memo := New[string, string]()
			calls := 0

			first, err := memo.Do(context.Background(), "req-1", "same", func(a string, b string) bool {
				return a == b
			}, func(context.Context) (string, error) {
				calls++
				return "", tt.wantErr
			})
			if err != tt.wantErr {
				t.Fatalf("first error = %v, want %v", err, tt.wantErr)
			}
			if first != "" {
				t.Fatalf("first response = %q, want empty", first)
			}

			second, err := memo.Do(context.Background(), "req-1", "same", func(a string, b string) bool {
				return a == b
			}, func(context.Context) (string, error) {
				calls++
				return "ok", nil
			})
			if err != nil {
				t.Fatalf("second error = %v", err)
			}
			if second != "ok" {
				t.Fatalf("second response = %q, want ok", second)
			}
			if calls != 2 {
				t.Fatalf("run calls = %d, want 2", calls)
			}
		})
	}
}

func TestMemoPrunesExpiredEntriesBelowCapacity(t *testing.T) {
	memo := New[string, string]()
	base := time.Date(2026, time.January, 1, 12, 0, 0, 0, time.UTC)
	now := base
	memo.now = func() time.Time { return now }
	memo.ttl = time.Minute
	memo.maxEntries = 16

	firstCalls := 0
	first, err := memo.Do(context.Background(), "req-1", "same", func(a string, b string) bool {
		return a == b
	}, func(context.Context) (string, error) {
		firstCalls++
		return "first", nil
	})
	if err != nil {
		t.Fatalf("first call error = %v", err)
	}
	if first != "first" {
		t.Fatalf("first response = %q, want first", first)
	}

	now = now.Add(2 * time.Minute)
	if _, err := memo.Do(context.Background(), "req-2", "other", func(a string, b string) bool {
		return a == b
	}, func(context.Context) (string, error) {
		return "second", nil
	}); err != nil {
		t.Fatalf("second request error = %v", err)
	}

	replayed, err := memo.Do(context.Background(), "req-1", "same", func(a string, b string) bool {
		return a == b
	}, func(context.Context) (string, error) {
		firstCalls++
		return "fresh", nil
	})
	if err != nil {
		t.Fatalf("replay error = %v", err)
	}
	if replayed != "fresh" {
		t.Fatalf("replayed response = %q, want fresh", replayed)
	}
	if firstCalls != 2 {
		t.Fatalf("req-1 run calls = %d, want 2", firstCalls)
	}
}
