package tracker

import "errors"

// ErrNotSupported is returned when a tracker backend does not support an operation.
var ErrNotSupported = errors.New("operation not supported")
