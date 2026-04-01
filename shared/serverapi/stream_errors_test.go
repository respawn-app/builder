package serverapi

import (
	"context"
	"errors"
	"io"
	"testing"
)

func TestNormalizeStreamErrorPreservesKnownCases(t *testing.T) {
	for _, err := range []error{io.EOF, context.Canceled, context.DeadlineExceeded, ErrStreamGap, ErrStreamUnavailable, ErrStreamFailed} {
		got := NormalizeStreamError(err)
		if !errors.Is(got, err) {
			t.Fatalf("expected %v to be preserved, got %v", err, got)
		}
	}
}

func TestNormalizeStreamErrorWrapsUnexpectedFailure(t *testing.T) {
	err := NormalizeStreamError(errors.New("boom"))
	if !errors.Is(err, ErrStreamFailed) {
		t.Fatalf("expected ErrStreamFailed, got %v", err)
	}
}
