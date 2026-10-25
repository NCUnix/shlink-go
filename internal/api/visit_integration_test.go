//go:build integration

package api_test

import (
	"context"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/NCUnix/shlink-go/internal/page"
	"github.com/NCUnix/shlink-go/internal/problem"
	"github.com/NCUnix/shlink-go/internal/testutil"
	"github.com/NCUnix/shlink-go/internal/types"
	"github.com/NCUnix/shlink-go/internal/visit"
)

type visitsPayload struct {
	Visits struct {
		Data       []visit.Visit   `json:"data"`
		Pagination page.Pagination `json:"pagination"`
	} `json:"visits"`
}

type orphanVisitsPayload struct {
	Visits struct {
		Data       []visit.OrphanVisit `json:"data"`
		Pagination page.Pagination     `json:"pagination"`
	} `json:"visits"`
}

type visitStatsPayload struct {
	Visits visit.Stats `json:"visits"`
}

type deletedVisitsPayload struct {
	DeletedVisits int64 `json:"deletedVisits"`
}

func TestGlobalVisitsAndStats(t *testing.T) {
	db := testutil.NewPostgres(t)
	fixtures := testutil.SeedBase(t, db)
	shortURLs := testutil.SeedShortURLs(t, db, fixtures)
	testutil.SeedVisits(t, db, fixtures, shortURLs)
	app := testutil.NewApp(t, db)

	for _, tc := range []struct {
		name   string
		apiKey string
		stats  visit.Stats
	}{
		{
			name: "admin", apiKey: "valid_api_key",
			stats: visit.Stats{
				NonOrphanVisits: visit.Summary{Total: 7, NonBots: 6, Bots: 1},
				OrphanVisits:    visit.Summary{Total: 3, NonBots: 2, Bots: 1},
			},
		},
		{
			name: "domain", apiKey: "domain_api_key",
			stats: visit.Stats{
				NonOrphanVisits: visit.Summary{},
				OrphanVisits:    visit.Summary{Total: 1, NonBots: 0, Bots: 1},
			},
		},
		{
			name: "author", apiKey: "author_api_key",
			stats: visit.Stats{
				NonOrphanVisits: visit.Summary{Total: 5, NonBots: 4, Bots: 1},
				OrphanVisits:    visit.Summary{},
			},
		},
		{
			name: "no orphans", apiKey: "no_orphans_api_key",
			stats: visit.Stats{
				NonOrphanVisits: visit.Summary{Total: 7, NonBots: 6, Bots: 1},
				OrphanVisits:    visit.Summary{},
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rec := performRequest(t, app.Handler(), http.MethodGet, "/rest/v3/visits", requestOptions{APIKey: tc.apiKey})
			require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
			require.Equal(t, tc.stats, decodeBody[visitStatsPayload](t, rec).Visits)
		})
	}
}

func TestShortURLVisits(t *testing.T) {
	db := testutil.NewPostgres(t)
	fixtures := testutil.SeedBase(t, db)
	shortURLs := testutil.SeedShortURLs(t, db, fixtures)
	testutil.SeedVisits(t, db, fixtures, shortURLs)
	app := testutil.NewApp(t, db)

	for _, tc := range []struct {
		name   string
		path   string
		apiKey string
		total  int
	}{
		{"domain example", "/rest/v3/short-urls/ghi789/visits?domain=example.com", "valid_api_key", 0},
		{"default domain", "/rest/v3/short-urls/ghi789/visits", "valid_api_key", 2},
		{"exclude bots", "/rest/v3/short-urls/def456/visits?excludeBots=true", "valid_api_key", 1},
		{"include bots", "/rest/v3/short-urls/def456/visits", "valid_api_key", 2},
		{"max page size", "/rest/v3/short-urls/abc123/visits?itemsPerPage=5000", "valid_api_key", 3},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rec := performRequest(t, app.Handler(), http.MethodGet, tc.path, requestOptions{APIKey: tc.apiKey})
			require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
			payload := decodeBody[visitsPayload](t, rec)
			require.Equal(t, tc.total, payload.Visits.Pagination.TotalItems)
			require.Len(t, payload.Visits.Data, tc.total)
		})
	}

	for _, tc := range []struct {
		path   string
		domain string
		apiKey string
	}{
		{"/rest/v3/short-urls/invalid/visits", "", "valid_api_key"},
		{"/rest/v3/short-urls/abc123/visits?domain=example.com", "example.com", "valid_api_key"},
		{"/rest/v3/short-urls/custom-with-domain/visits?domain=example.com", "example.com", "valid_api_key"},
		{"/rest/v3/short-urls/ghi789/visits", "", "author_api_key"},
		{"/rest/v3/short-urls/custom-with-domain/visits?domain=some-domain.com", "some-domain.com", "domain_api_key"},
	} {
		rec := performRequest(t, app.Handler(), http.MethodGet, tc.path, requestOptions{APIKey: tc.apiKey})
		payload := requireProblem(t, rec, http.StatusNotFound, problem.TypeShortURLNotFound)
		if tc.domain != "" {
			require.Equal(t, tc.domain, payload.Domain)
		}
	}
}

func TestTagVisits(t *testing.T) {
	db := testutil.NewPostgres(t)
	fixtures := testutil.SeedBase(t, db)
	shortURLs := testutil.SeedShortURLs(t, db, fixtures)
	testutil.SeedVisits(t, db, fixtures, shortURLs)
	app := testutil.NewApp(t, db)

	for _, tc := range []struct {
		name   string
		apiKey string
		tag    string
		query  string
		total  int
	}{
		{"foo admin", "valid_api_key", "foo", "", 5},
		{"foo admin no bots", "valid_api_key", "foo", "?excludeBots=true", 4},
		{"bar admin", "valid_api_key", "bar", "", 2},
		{"bar admin no bots", "valid_api_key", "bar", "?excludeBots=true", 1},
		{"baz admin", "valid_api_key", "baz", "", 0},
		{"foo author", "author_api_key", "foo", "", 5},
		{"bar author", "author_api_key", "bar", "", 2},
		{"foo domain", "domain_api_key", "foo", "", 0},
		{"foo example domain", "valid_api_key", "foo", "?domain=example.com", 0},
		{"foo default domain", "valid_api_key", "foo", "?domain=DEFAULT", 5},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rec := performRequest(t, app.Handler(), http.MethodGet, "/rest/v3/tags/"+tc.tag+"/visits"+tc.query, requestOptions{APIKey: tc.apiKey})
			require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
			payload := decodeBody[visitsPayload](t, rec)
			require.Equal(t, tc.total, payload.Visits.Pagination.TotalItems)
			require.Len(t, payload.Visits.Data, tc.total)
		})
	}

	for _, tc := range []struct {
		apiKey string
		tag    string
	}{
		{"valid_api_key", "invalid_tag"},
		{"domain_api_key", "bar"},
		{"author_api_key", "baz"},
	} {
		rec := performRequest(t, app.Handler(), http.MethodGet, "/rest/v3/tags/"+tc.tag+"/visits", requestOptions{APIKey: tc.apiKey})
		payload := requireProblem(t, rec, http.StatusNotFound, problem.TypeTagNotFound)
		require.Equal(t, tc.tag, payload.Tag)
	}
}

func TestDomainVisits(t *testing.T) {
	db := testutil.NewPostgres(t)
	fixtures := testutil.SeedBase(t, db)
	shortURLs := testutil.SeedShortURLs(t, db, fixtures)
	testutil.SeedVisits(t, db, fixtures, shortURLs)
	app := testutil.NewApp(t, db)

	for _, tc := range []struct {
		name   string
		apiKey string
		domain string
		query  string
		total  int
	}{
		{"example admin", "valid_api_key", "example.com", "", 0},
		{"DEFAULT admin", "valid_api_key", "DEFAULT", "", 7},
		{"DEFAULT admin no bots", "valid_api_key", "DEFAULT", "?excludeBots=true", 6},
		{"DEFAULT domain key", "domain_api_key", "DEFAULT", "", 0},
		{"DEFAULT author key", "author_api_key", "DEFAULT", "", 5},
		{"DEFAULT author key no bots", "author_api_key", "DEFAULT", "?excludeBots=true", 4},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rec := performRequest(t, app.Handler(), http.MethodGet, "/rest/v3/domains/"+tc.domain+"/visits"+tc.query, requestOptions{APIKey: tc.apiKey})
			require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
			payload := decodeBody[visitsPayload](t, rec)
			require.Equal(t, tc.total, payload.Visits.Pagination.TotalItems)
			require.Len(t, payload.Visits.Data, tc.total)
		})
	}

	for _, tc := range []struct {
		apiKey string
		domain string
	}{
		{"valid_api_key", "invalid_domain.com"},
		{"domain_api_key", "this_domain_is_detached.com"},
		{"author_api_key", "this_domain_is_detached.com"},
	} {
		rec := performRequest(t, app.Handler(), http.MethodGet, "/rest/v3/domains/"+tc.domain+"/visits", requestOptions{APIKey: tc.apiKey})
		payload := requireProblem(t, rec, http.StatusNotFound, problem.TypeDomainNotFound)
		require.Equal(t, tc.domain, payload.Authority)
	}
}

func TestOrphanVisits(t *testing.T) {
	db := testutil.NewPostgres(t)
	fixtures := testutil.SeedBase(t, db)
	shortURLs := testutil.SeedShortURLs(t, db, fixtures)
	testutil.SeedVisits(t, db, fixtures, shortURLs)
	app := testutil.NewApp(t, db)

	invalid := visit.OrphanVisit{
		Visit: visit.Visit{
			Referer:      stringPtr("https://s.test/foo"),
			Date:         mustAPITime(t, "2020-03-01T00:00:00+00:00"),
			UserAgent:    "cf-facebook",
			PotentialBot: true,
			VisitedURL:   stringPtr("https://example.com/short"),
		},
		Type: "invalid_short_url",
	}
	regular := visit.OrphanVisit{
		Visit: visit.Visit{
			Referer:      stringPtr("https://s.test/foo/bar"),
			Date:         mustAPITime(t, "2020-02-01T00:00:00+00:00"),
			UserAgent:    "shlink-tests-agent",
			PotentialBot: false,
			VisitedURL:   stringPtr("https://s.test/bar"),
		},
		Type: "regular_404",
	}
	baseURL := visit.OrphanVisit{
		Visit: visit.Visit{
			Referer:      stringPtr("https://s.test"),
			Date:         mustAPITime(t, "2020-01-01T00:00:00+00:00"),
			UserAgent:    "shlink-tests-agent",
			PotentialBot: false,
			VisitedURL:   stringPtr("https://s.test/foo"),
		},
		Type: "base_url",
	}

	for _, tc := range []struct {
		name     string
		query    string
		total    int
		expected []visit.OrphanVisit
	}{
		{"all", "", 3, []visit.OrphanVisit{invalid, regular, baseURL}},
		{"limit", "?itemsPerPage=2", 3, []visit.OrphanVisit{invalid, regular}},
		{"second page", "?itemsPerPage=2&page=2", 3, []visit.OrphanVisit{baseURL}},
		{"exclude bots", "?excludeBots=true", 2, []visit.OrphanVisit{regular, baseURL}},
		{"base url only", "?type=base_url", 1, []visit.OrphanVisit{baseURL}},
		{"regular only", "?type=regular_404", 1, []visit.OrphanVisit{regular}},
		{"invalid only", "?type=invalid_short_url", 1, []visit.OrphanVisit{invalid}},
		{"example domain", "?domain=example.com", 1, []visit.OrphanVisit{invalid}},
		{"default domain", "?domain=s.test", 2, []visit.OrphanVisit{regular, baseURL}},
		{"default keyword", "?domain=DEFAULT", 2, []visit.OrphanVisit{regular, baseURL}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rec := performRequest(t, app.Handler(), http.MethodGet, "/rest/v3/visits/orphan"+tc.query, requestOptions{APIKey: "valid_api_key"})
			require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
			payload := decodeBody[orphanVisitsPayload](t, rec)
			require.Equal(t, tc.total, payload.Visits.Pagination.TotalItems)
			require.Equal(t, tc.expected, payload.Visits.Data)
		})
	}

	rec := performRequest(t, app.Handler(), http.MethodGet, "/rest/v3/visits/orphan?type=invalid", requestOptions{APIKey: "valid_api_key"})
	requireProblem(t, rec, http.StatusBadRequest, problem.TypeInvalidData)

	rec = performRequest(t, app.Handler(), http.MethodGet, "/rest/v3/visits/orphan", requestOptions{APIKey: "no_orphans_api_key"})
	require.Equal(t, http.StatusOK, rec.Code)
	payload := decodeBody[orphanVisitsPayload](t, rec)
	require.Empty(t, payload.Visits.Data)
	require.Equal(t, 0, payload.Visits.Pagination.TotalItems)
}

func TestNonOrphanVisits(t *testing.T) {
	db := testutil.NewPostgres(t)
	fixtures := testutil.SeedBase(t, db)
	shortURLs := testutil.SeedShortURLs(t, db, fixtures)
	testutil.SeedVisits(t, db, fixtures, shortURLs)
	app := testutil.NewApp(t, db)

	for _, tc := range []struct {
		name  string
		query string
		total int
		count int
	}{
		{"all", "", 7, 7},
		{"middle page", "?page=2&itemsPerPage=3", 7, 3},
		{"last page", "?page=3&itemsPerPage=3", 7, 1},
		{"bots excluded", "?excludeBots=true", 6, 6},
		{"bots excluded paginated", "?excludeBots=true&page=1&itemsPerPage=4", 6, 4},
		{"future date", "?startDate=2021-06-02T00:00:00%2B00:00", 0, 0},
		{"example domain", "?domain=example.com", 0, 0},
		{"default domain", "?domain=DEFAULT", 7, 7},
		{"large page", "?itemsPerPage=5000", 7, 7},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rec := performRequest(t, app.Handler(), http.MethodGet, "/rest/v3/visits/non-orphan"+tc.query, requestOptions{APIKey: "valid_api_key"})
			require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
			payload := decodeBody[visitsPayload](t, rec)
			require.Equal(t, tc.total, payload.Visits.Pagination.TotalItems)
			require.Len(t, payload.Visits.Data, tc.count)
		})
	}
}

func TestDeleteShortURLVisits(t *testing.T) {
	db := testutil.NewPostgres(t)
	fixtures := testutil.SeedBase(t, db)
	shortURLs := testutil.SeedShortURLs(t, db, fixtures)
	testutil.SeedVisits(t, db, fixtures, shortURLs)
	app := testutil.NewApp(t, db)

	rec := performRequest(t, app.Handler(), http.MethodDelete, "/rest/v3/short-urls/abc123/visits", requestOptions{APIKey: "valid_api_key"})
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	require.Equal(t, int64(3), decodeBody[deletedVisitsPayload](t, rec).DeletedVisits)

	rec = performRequest(t, app.Handler(), http.MethodGet, "/rest/v3/visits/non-orphan", requestOptions{APIKey: "valid_api_key"})
	require.Equal(t, 4, decodeBody[visitsPayload](t, rec).Visits.Pagination.TotalItems)
	rec = performRequest(t, app.Handler(), http.MethodGet, "/rest/v3/visits/orphan", requestOptions{APIKey: "valid_api_key"})
	require.Equal(t, 3, decodeBody[orphanVisitsPayload](t, rec).Visits.Pagination.TotalItems)

	rec = performRequest(t, app.Handler(), http.MethodDelete, "/rest/v3/short-urls/abc123/visits", requestOptions{APIKey: "domain_api_key"})
	require.Equal(t, http.StatusNotFound, rec.Code)
}

func TestDeleteOrphanVisits(t *testing.T) {
	db := testutil.NewPostgres(t)
	fixtures := testutil.SeedBase(t, db)
	shortURLs := testutil.SeedShortURLs(t, db, fixtures)
	testutil.SeedVisits(t, db, fixtures, shortURLs)
	app := testutil.NewApp(t, db)

	rec := performRequest(t, app.Handler(), http.MethodDelete, "/rest/v3/visits/orphan", requestOptions{APIKey: "no_orphans_api_key"})
	require.Equal(t, http.StatusOK, rec.Code)
	require.Equal(t, int64(0), decodeBody[deletedVisitsPayload](t, rec).DeletedVisits)
	rec = performRequest(t, app.Handler(), http.MethodGet, "/rest/v3/visits/orphan", requestOptions{APIKey: "valid_api_key"})
	require.Equal(t, 3, decodeBody[orphanVisitsPayload](t, rec).Visits.Pagination.TotalItems)

	// A domain-scoped key may only delete orphan visits of its own domain.
	rec = performRequest(t, app.Handler(), http.MethodDelete, "/rest/v3/visits/orphan", requestOptions{APIKey: "domain_api_key"})
	require.Equal(t, http.StatusOK, rec.Code)
	require.Equal(t, int64(1), decodeBody[deletedVisitsPayload](t, rec).DeletedVisits)
	rec = performRequest(t, app.Handler(), http.MethodGet, "/rest/v3/visits/orphan", requestOptions{APIKey: "valid_api_key"})
	require.Equal(t, 2, decodeBody[orphanVisitsPayload](t, rec).Visits.Pagination.TotalItems)

	// An author-scoped key has no access to orphan visits at all.
	rec = performRequest(t, app.Handler(), http.MethodDelete, "/rest/v3/visits/orphan", requestOptions{APIKey: "author_api_key"})
	require.Equal(t, http.StatusOK, rec.Code)
	require.Equal(t, int64(0), decodeBody[deletedVisitsPayload](t, rec).DeletedVisits)

	rec = performRequest(t, app.Handler(), http.MethodDelete, "/rest/v3/visits/orphan", requestOptions{APIKey: "valid_api_key"})
	require.Equal(t, int64(2), decodeBody[deletedVisitsPayload](t, rec).DeletedVisits)
	rec = performRequest(t, app.Handler(), http.MethodGet, "/rest/v3/visits/orphan", requestOptions{APIKey: "valid_api_key"})
	require.Equal(t, 0, decodeBody[orphanVisitsPayload](t, rec).Visits.Pagination.TotalItems)
}

func TestLargeVisitPage(t *testing.T) {
	db := testutil.NewPostgres(t)
	fixtures := testutil.SeedBase(t, db)
	shortURLs := testutil.SeedShortURLs(t, db, fixtures)
	testutil.SeedVisits(t, db, fixtures, shortURLs)
	app := testutil.NewApp(t, db)

	custom := shortURLs["1/custom"]
	_, err := db.SQL.ExecContext(context.Background(),
		`INSERT INTO visits (short_url_id, domain_id, visited_at, referer, user_agent, visited_url, potential_bot)
		 SELECT $1, $2, now() - (n || ' seconds')::interval, '', 'shlink-tests-agent', 'http://s.test/custom', FALSE
		 FROM generate_series(1, 5000) AS n`,
		custom.ID, fixtures.DefaultDomainID,
	)
	require.NoError(t, err)

	rec := performRequest(t, app.Handler(), http.MethodGet, "/rest/v3/short-urls/custom/visits?itemsPerPage=5000", requestOptions{APIKey: "valid_api_key"})
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	payload := decodeBody[visitsPayload](t, rec)
	require.Equal(t, 5000, payload.Visits.Pagination.TotalItems)
	require.Equal(t, 5000, payload.Visits.Pagination.ItemsPerPage)
	require.Len(t, payload.Visits.Data, 5000)

	rows, err := db.SQL.QueryContext(context.Background(),
		`EXPLAIN (FORMAT TEXT)
		 SELECT v.id FROM visits v
		 WHERE v.orphan_type IS NULL AND v.short_url_id = $1
		 ORDER BY v.visited_at DESC, v.id DESC
		 LIMIT 5000`, custom.ID)
	require.NoError(t, err)
	defer rows.Close()
	plan := make([]string, 0)
	for rows.Next() {
		var line string
		require.NoError(t, rows.Scan(&line))
		plan = append(plan, line)
	}
	require.NoError(t, rows.Err())
	require.NoError(t, rows.Close())
	require.NotEmpty(t, strings.Join(plan, "\n"))

	var indexName string
	require.NoError(t, db.SQL.QueryRowContext(context.Background(),
		"SELECT indexname FROM pg_indexes WHERE indexname = 'visits_short_url_id_visited_at_idx'").Scan(&indexName))
	require.Equal(t, "visits_short_url_id_visited_at_idx", indexName)
}

func mustAPITime(t *testing.T, value string) types.Time {
	t.Helper()
	parsed, err := time.Parse(time.RFC3339, value)
	require.NoError(t, err)
	return types.NewTime(parsed)
}
