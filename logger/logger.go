// Package logger is how the engine logs. Log is an interface in the shape
// every Go structured logger already has — slog's — so a host program hands
// the engine its own logger and nothing adapts: Memdoor's pkg/ declares the
// same four methods and its gateway logger satisfies both.
package logger

import "log/slog"

// Logger is the four methods the engine uses. *slog.Logger satisfies it.
type Logger interface {
	Info(msg string, args ...any)
	Warn(msg string, args ...any)
	Error(msg string, args ...any)
	Debug(msg string, args ...any)
}

// Log is the engine's logger: slog's default until a host sets its own.
var Log Logger = slog.Default()
