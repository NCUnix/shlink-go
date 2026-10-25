// Package logctx carries the application logger through a request context.
//
// HTTP handlers only receive a context, so packages such as internal/api cannot
// reach the *slog.Logger the application was built with. Without this, code that
// calls the package-level slog functions writes to the default logger instead of
// the configured one, ending up with a different format, output stream and level
// than the rest of the application.
package logctx

import (
	"context"
	"log/slog"
)

type contextKey struct{}

// With returns a context carrying logger. A nil logger leaves ctx unchanged.
func With(ctx context.Context, logger *slog.Logger) context.Context {
	if logger == nil {
		return ctx
	}
	return context.WithValue(ctx, contextKey{}, logger)
}

// From returns the logger stored in ctx, falling back to slog.Default.
func From(ctx context.Context) *slog.Logger {
	if ctx != nil {
		if logger, ok := ctx.Value(contextKey{}).(*slog.Logger); ok && logger != nil {
			return logger
		}
	}
	return slog.Default()
}
