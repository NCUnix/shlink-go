//go:build integration

package apikey_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/NCUnix/shlink-go/internal/apikey"
	"github.com/NCUnix/shlink-go/internal/testutil"
)

type apiKeyRow struct {
	Name    string `bun:"name"`
	KeyHash string `bun:"key_hash"`
}

func TestEnsureInitial(t *testing.T) {
	db := testutil.NewPostgres(t)

	list := func(t *testing.T) []apiKeyRow {
		t.Helper()
		var rows []apiKeyRow
		require.NoError(t, db.NewSelect().Table("api_keys").Column("name", "key_hash").Order("id").Scan(t.Context(), &rows))
		return rows
	}

	t.Run("empty key is a no-op", func(t *testing.T) {
		testutil.Reset(t, db)

		require.NoError(t, apikey.EnsureInitial(t.Context(), db.DB, ""))
		require.Empty(t, list(t))
	})

	t.Run("creates the key on an empty database", func(t *testing.T) {
		testutil.Reset(t, db)

		require.NoError(t, apikey.EnsureInitial(t.Context(), db.DB, "bootstrap-secret"))

		rows := list(t)
		require.Len(t, rows, 1)
		require.Equal(t, "Initial API key", rows[0].Name)
		require.Equal(t, apikey.Hash("bootstrap-secret"), rows[0].KeyHash)
		require.NotEqual(t, "bootstrap-secret", rows[0].KeyHash, "the raw key must never be stored")
	})

	t.Run("does not create a second key", func(t *testing.T) {
		testutil.Reset(t, db)

		require.NoError(t, apikey.EnsureInitial(t.Context(), db.DB, "first"))
		require.NoError(t, apikey.EnsureInitial(t.Context(), db.DB, "second"))

		rows := list(t)
		require.Len(t, rows, 1)
		require.Equal(t, apikey.Hash("first"), rows[0].KeyHash, "the existing key must win")
	})

	t.Run("leaves an existing database alone", func(t *testing.T) {
		testutil.Reset(t, db)
		_, err := db.ExecContext(t.Context(),
			"INSERT INTO api_keys (name, key_hash) VALUES (?, ?)", "provisioned", apikey.Hash("provisioned-key"))
		require.NoError(t, err)

		require.NoError(t, apikey.EnsureInitial(t.Context(), db.DB, "bootstrap-secret"))

		rows := list(t)
		require.Len(t, rows, 1)
		require.Equal(t, "provisioned", rows[0].Name)
	})
}
