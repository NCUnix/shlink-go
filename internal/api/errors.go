package api

import (
	"context"
	"net/http"

	"github.com/NCUnix/shlink-go/internal/logctx"
	"github.com/NCUnix/shlink-go/internal/problem"
)

func internalProblem(ctx context.Context, err error) *problem.Problem {
	logctx.From(ctx).ErrorContext(ctx, "request_failed", "error", err)
	return problem.New(
		http.StatusInternalServerError,
		"https://shlink.io/api/error/internal",
		"Internal error",
		"An unexpected error occurred.",
	)
}
