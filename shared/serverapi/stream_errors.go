package serverapi

import "errors"

var ErrStreamGap = errors.New("stream cursor is outside the retained range and client must rehydrate")
var ErrStreamUnavailable = errors.New("stream is unavailable")

var ErrSessionActivityGap = ErrStreamGap
var ErrProcessOutputGap = ErrStreamGap
var ErrSessionActivityUnavailable = ErrStreamUnavailable
var ErrProcessOutputUnavailable = ErrStreamUnavailable
