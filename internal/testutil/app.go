//go:build integration

package testutil

import (
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	application "github.com/NCUnix/shlink-go/internal/app"
	"github.com/NCUnix/shlink-go/internal/clock"
	"github.com/NCUnix/shlink-go/internal/config"
	"github.com/NCUnix/shlink-go/internal/store"
)

func NewApp(t *testing.T, db *store.DB) *application.App {
	t.Helper()
	return NewAppWithConfig(t, db, TestConfig(), TestClock())
}

func NewAppWithConfig(t *testing.T, db *store.DB, cfg config.Config, clk clock.Clock) *application.App {
	t.Helper()
	return NewAppWithLogger(t, db, cfg, clk, slog.New(slog.NewTextHandler(io.Discard, nil)))
}

// NewAppWithLogger builds the application with a caller-supplied logger, so a
// test can assert on what the server actually emits.
func NewAppWithLogger(t *testing.T, db *store.DB, cfg config.Config, clk clock.Clock, logger *slog.Logger) *application.App {
	t.Helper()
	result, err := application.NewWithClock(cfg, db, logger, clk)
	require.NoError(t, err)
	t.Cleanup(func() { _ = result.Close() })
	return result
}

func TestClock() clock.Clock {
	return clock.Fixed{T: time.Date(2021, 6, 1, 0, 0, 0, 0, time.UTC)}
}

func TestConfig() config.Config {
	return config.Config{
		DefaultDomain:           "s.test",
		ISHTTPSEnabled:          false,
		CORSAllowedOrigins:      []string{"*"},
		CORSMaxAge:              3600,
		ShortURLTrailingSlash:   false,
		ExtraPathMode:           "default",
		DefaultShortCodesLength: 5,
		DefaultShortCodesChars:  "0123456789abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ",
		LogLevel:                "error",
		TrackOrphanVisits:       true,
		TrustedProxies:          []string{"192.0.2.1"},
	}
}
