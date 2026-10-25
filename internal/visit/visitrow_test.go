package visit

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func stringPtr(value string) *string { return &value }

func floatPtr(value float64) *float64 { return &value }

func TestVisitRowVisit(t *testing.T) {
	t.Run("maps every column", func(t *testing.T) {
		row := visitRow{
			ID:           7,
			VisitedAt:    time.Date(2024, 5, 6, 7, 8, 9, 0, time.FixedZone("UTC+2", 2*60*60)),
			Referer:      stringPtr("https://referer.example/path"),
			UserAgent:    stringPtr("Mozilla/5.0 (X11; Linux x86_64)"),
			VisitedURL:   stringPtr("https://s.test/abc"),
			RedirectURL:  stringPtr("https://target.example/landing"),
			PotentialBot: true,
			CountryCode:  stringPtr("ES"),
			CountryName:  stringPtr("Spain"),
			RegionName:   stringPtr("Madrid"),
			CityName:     stringPtr("Madrid"),
			Latitude:     floatPtr(40.4168),
			Longitude:    floatPtr(-3.7038),
			Timezone:     stringPtr("Europe/Madrid"),
		}

		got := row.visit()

		require.Equal(t, "https://referer.example/path", *got.Referer)
		require.Equal(t, "Mozilla/5.0 (X11; Linux x86_64)", got.UserAgent)
		require.Equal(t, "https://s.test/abc", *got.VisitedURL)
		require.Equal(t, "https://target.example/landing", *got.RedirectURL)
		require.True(t, got.PotentialBot)
		require.Equal(t, time.Date(2024, 5, 6, 5, 8, 9, 0, time.UTC), got.Date.Time, "dates are stored in UTC")

		require.NotNil(t, got.VisitLocation)
		require.Equal(t, "ES", got.VisitLocation.CountryCode)
		require.Equal(t, "Spain", got.VisitLocation.CountryName)
		require.Equal(t, "Madrid", got.VisitLocation.RegionName)
		require.Equal(t, "Madrid", got.VisitLocation.CityName)
		require.InDelta(t, 40.4168, got.VisitLocation.Latitude, 1e-9)
		require.InDelta(t, -3.7038, got.VisitLocation.Longitude, 1e-9)
		require.Equal(t, "Europe/Madrid", got.VisitLocation.Timezone)
	})

	t.Run("leaves absent optional columns empty", func(t *testing.T) {
		got := visitRow{VisitedAt: time.Now()}.visit()

		require.Nil(t, got.Referer)
		require.Empty(t, got.UserAgent)
		require.Nil(t, got.VisitedURL)
		require.Nil(t, got.RedirectURL)
		require.False(t, got.PotentialBot)
		require.Nil(t, got.VisitLocation, "no geo column means no visitLocation object at all")
	})

	t.Run("builds a location from any single geo column", func(t *testing.T) {
		got := visitRow{VisitedAt: time.Now(), Latitude: floatPtr(1.5)}.visit()

		require.NotNil(t, got.VisitLocation)
		require.InDelta(t, 1.5, got.VisitLocation.Latitude, 1e-9)
		require.Empty(t, got.VisitLocation.CountryCode)
	})

	t.Run("keeps explicitly empty strings distinct from null", func(t *testing.T) {
		got := visitRow{VisitedAt: time.Now(), Referer: stringPtr(""), UserAgent: stringPtr("")}.visit()

		require.NotNil(t, got.Referer)
		require.Empty(t, *got.Referer)
		require.Empty(t, got.UserAgent)
	})
}

func TestVisitOrder(t *testing.T) {
	require.Equal(t, "v.visited_at ASC, v.id ASC", visitOrder("date-asc"))
	require.Equal(t, "v.visited_at ASC, v.id ASC", visitOrder("visitedAt-ASC"))
	require.Equal(t, "v.visited_at DESC, v.id DESC", visitOrder("date-desc"))
	require.Equal(t, "v.visited_at DESC, v.id DESC", visitOrder(""))
}

func TestResolvedPageSize(t *testing.T) {
	require.Equal(t, 1, resolvedPage(0))
	require.Equal(t, 3, resolvedPage(3))
	require.Equal(t, 1, resolvedPageSize(0, 0))
	require.Equal(t, 42, resolvedPageSize(0, 42), "unset page size falls back to the total")
	require.Equal(t, 5000, resolvedPageSize(100000, 42), "page size is capped")
	require.Equal(t, 15, resolvedPageSize(15, 42))
}
