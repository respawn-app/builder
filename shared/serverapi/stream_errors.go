package serverapi

import (
	"context"
	"errors"
	"io"
)

var ErrStreamGap = errors.New("stream cursor is outside the retained range and client must rehydrate")
var ErrStreamUnavailable = errors.New("stream is unavailable")
var ErrStreamFailed = errors.New("stream failed")

var ErrSessionActivityGap = ErrStreamGap
var ErrProcessOutputGap = ErrStreamGap
var ErrSessionActivityUnavailable = ErrStreamUnavailable
var ErrProcessOutputUnavailable = ErrStreamUnavailable

func NormalizeStreamError(err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, io.EOF) || errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return err
	}
	if errors.Is(err, ErrStreamGap) || errors.Is(err, ErrStreamUnavailable) || errors.Is(err, ErrStreamFailed) {
		return err
	}
	return errors.Join(ErrStreamFailed, err)
}
