//go:build integration

package domain_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/NCUnix/shlink-go/internal/domain"
	"github.com/NCUnix/shlink-go/internal/testutil"
)

type domainRow struct {
	Authority       string  `bun:"authority"`
	IsDefault       bool    `bun:"is_default"`
	BaseURLRedirect *string `bun:"base_url_redirect"`
}

func TestEnsureDefault(t *testing.T) {
	db := testutil.NewPostgres(t)

	list := func(t *testing.T) []domainRow {
		t.Helper()
		var rows []domainRow
		require.NoError(t, db.NewSelect().Table("domains").
			Column("authority", "is_default", "base_url_redirect").
			Order("authority").
			Scan(t.Context(), &rows))
		return rows
	}

	t.Run("creates a normalized default domain", func(t *testing.T) {
		testutil.Reset(t, db)

		require.NoError(t, domain.EnsureDefault(t.Context(), db.DB, "  Example.COM "))

		rows := list(t)
		require.Len(t, rows, 1)
		require.Equal(t, "example.com", rows[0].Authority)
		require.True(t, rows[0].IsDefault)
	})

	t.Run("moves the default flag off the previous domain", func(t *testing.T) {
		testutil.Reset(t, db)
		_, err := db.ExecContext(t.Context(),
			"INSERT INTO domains (authority, is_default) VALUES ('old.example', TRUE)")
		require.NoError(t, err)

		require.NoError(t, domain.EnsureDefault(t.Context(), db.DB, "new.example"))

		rows := list(t)
		require.Len(t, rows, 2)
		require.Equal(t, "new.example", rows[0].Authority)
		require.True(t, rows[0].IsDefault)
		require.Equal(t, "old.example", rows[1].Authority)
		require.False(t, rows[1].IsDefault, "only one domain may be the default")
	})

	t.Run("is idempotent", func(t *testing.T) {
		testutil.Reset(t, db)

		require.NoError(t, domain.EnsureDefault(t.Context(), db.DB, "s.test"))
		require.NoError(t, domain.EnsureDefault(t.Context(), db.DB, "s.test"))

		rows := list(t)
		require.Len(t, rows, 1)
		require.True(t, rows[0].IsDefault)
	})

	t.Run("keeps existing redirect settings", func(t *testing.T) {
		testutil.Reset(t, db)
		_, err := db.ExecContext(t.Context(),
			"INSERT INTO domains (authority, is_default, base_url_redirect) VALUES ('s.test', FALSE, 'https://fallback.example')")
		require.NoError(t, err)

		require.NoError(t, domain.EnsureDefault(t.Context(), db.DB, "s.test"))

		rows := list(t)
		require.Len(t, rows, 1)
		require.True(t, rows[0].IsDefault)
		require.NotNil(t, rows[0].BaseURLRedirect)
		require.Equal(t, "https://fallback.example", *rows[0].BaseURLRedirect)
	})
}
