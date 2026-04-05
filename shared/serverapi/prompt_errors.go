package serverapi

import "errors"

var ErrPromptNotFound = errors.New("prompt not found")
var ErrPromptAlreadyResolved = errors.New("prompt already resolved")
var ErrPromptUnsupported = errors.New("prompt cannot be answered through this boundary")
