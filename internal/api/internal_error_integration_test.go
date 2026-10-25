//go:build integration

package api_test

import (
	"bytes"
	"log/slog"
	"net/http"
	"sync"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/NCUnix/shlink-go/internal/testutil"
)

// syncBuffer collects log output written from handler goroutines.
type syncBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *syncBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *syncBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

// TestUnexpectedDatabaseFailureIsLogged covers the 500 fallback shared by every
// mapXError helper: the client must get the generic internal-error problem, and
// the cause must reach the application logger configured for the server (not
// slog.Default, which would use a different format and stream).
func TestUnexpectedDatabaseFailureIsLogged(t *testing.T) {
	db := testutil.NewPostgres(t)
	testutil.SeedBase(t, db)

	var logs syncBuffer
	logger := slog.New(slog.NewJSONHandler(&logs, nil))
	app := testutil.NewAppWithLogger(t, db, testutil.TestConfig(), testutil.TestClock(), logger)

	cases := []struct {
		name  string
		table string
		path  string
	}{
		{"tag list", "tags", "/rest/v3/tags"},
		{"domain list", "domains", "/rest/v3/domains"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := db.ExecContext(t.Context(), "DROP TABLE "+tc.table+" CASCADE")
			require.NoError(t, err)

			before := logs.String()

			rec := performRequest(t, app.Handler(), http.MethodGet, tc.path, requestOptions{APIKey: "valid_api_key"})
			payload := requireProblem(t, rec, http.StatusInternalServerError, "https://shlink.io/api/error/internal")
			require.Equal(t, "Internal error", payload.Title)
			require.Equal(t, "An unexpected error occurred.", payload.Detail)

			emitted := logs.String()[len(before):]
			require.Contains(t, emitted, `"level":"ERROR"`, "500 must be logged at error level")
			require.Contains(t, emitted, `"msg":"request_failed"`)
			require.Contains(t, emitted, "does not exist", "the underlying cause must be logged, not swallowed")
		})
	}
}
