package server

import (
	"log/slog"
	"net/http"
	"runtime/debug"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"

	"github.com/NCUnix/shlink-go/internal/logctx"
	"github.com/NCUnix/shlink-go/internal/problem"
)

const requestIDKey = "request_id"

func requestIDMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		id := c.GetHeader("X-Request-Id")
		if id == "" {
			id = uuid.NewString()
		}
		c.Set(requestIDKey, id)
		c.Header("X-Request-Id", id)
		c.Next()
	}
}

// loggerMiddleware exposes the application logger to handlers through the
// request context, so code that only receives a context still logs with the
// configured handler instead of slog.Default.
func loggerMiddleware(logger *slog.Logger) gin.HandlerFunc {
	return func(c *gin.Context) {
		c.Request = c.Request.WithContext(logctx.With(c.Request.Context(), logger))
		c.Next()
	}
}

func accessLogMiddleware(logger *slog.Logger) gin.HandlerFunc {
	return func(c *gin.Context) {
		start := time.Now()
		c.Next()
		attrs := []any{
			"request_id", c.GetString(requestIDKey),
			"method", c.Request.Method,
			"path", c.Request.URL.Path,
			"status", c.Writer.Status(),
			"duration_ms", time.Since(start).Milliseconds(),
		}
		if name := c.GetString("api_key_name"); name != "" {
			attrs = append(attrs, "api_key_name", name)
		}
		logger.Info("http_request", attrs...)
	}
}

func recoveryMiddleware(logger *slog.Logger) gin.HandlerFunc {
	return func(c *gin.Context) {
		defer func() {
			if recovered := recover(); recovered != nil {
				logger.Error(
					"panic_recovered",
					"request_id", c.GetString(requestIDKey),
					"panic", recovered,
					"stack", string(debug.Stack()),
				)
				if !c.Writer.Written() {
					problem.Abort(c, problem.New(
						http.StatusInternalServerError,
						"https://shlink.io/api/error/internal",
						"Internal error",
						"An unexpected error occurred.",
					))
				}
			}
		}()
		c.Next()
	}
}

func bodyLimitMiddleware(limit int64) gin.HandlerFunc {
	return func(c *gin.Context) {
		if c.Request.Body != nil && c.Request.Method != http.MethodGet && c.Request.Method != http.MethodHead {
			c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, limit)
		}
		c.Next()
	}
}

func implicitOptionsMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		if c.Request.Method != http.MethodOptions || c.GetHeader("Origin") != "" {
			c.Next()
			return
		}
		c.Header("Allow", allowedMethods(c.Request.URL.Path))
		c.Status(http.StatusNoContent)
		c.Abort()
	}
}

func isRESTPath(path string) bool {
	return strings.HasPrefix(path, "/rest/")
}
