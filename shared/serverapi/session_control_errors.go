package serverapi

import "errors"

var ErrSessionAlreadyControlled = errors.New("session is already controlled by another client")
var ErrInvalidControllerLease = errors.New("controller lease is invalid or expired")
var ErrRuntimeUnavailable = errors.New("session runtime is unavailable")
