package rsync

import "log/slog"

// Logger is an interface that allows specifying your own logger.
// By default, the Go log package is used, which prints to stderr.
type Logger = slog.Logger
