package visit

import (
	"time"

	"github.com/NCUnix/shlink-go/internal/geoip"
	"github.com/NCUnix/shlink-go/internal/page"
	"github.com/NCUnix/shlink-go/internal/types"
)

const (
	OrphanBaseURL         = "base_url"
	OrphanInvalidShortURL = "invalid_short_url"
	OrphanRegular404      = "regular_404"
)

type Summary struct {
	Total   int64 `json:"total"`
	NonBots int64 `json:"nonBots"`
	Bots    int64 `json:"bots"`
}

type Stats struct {
	NonOrphanVisits Summary `json:"nonOrphanVisits"`
	OrphanVisits    Summary `json:"orphanVisits"`
}

type Visit struct {
	Referer       *string         `json:"referer"`
	Date          types.Time      `json:"date"`
	UserAgent     string          `json:"userAgent"`
	VisitLocation *geoip.Location `json:"visitLocation"`
	PotentialBot  bool            `json:"potentialBot"`
	VisitedURL    *string         `json:"visitedUrl"`
	RedirectURL   *string         `json:"redirectUrl"`
}

type OrphanVisit struct {
	Visit
	Type string `json:"type"`
}

type ListResult[T any] struct {
	Data       []T             `json:"data"`
	Pagination page.Pagination `json:"pagination"`
}

type ListParams struct {
	Page         int
	ItemsPerPage int
	StartDate    *time.Time
	EndDate      *time.Time
	ExcludeBots  bool
	OrderBy      string
	Domain       string
	Type         string
}

type Record struct {
	ShortURLID  *int64
	DomainID    *int64
	OrphanType  string
	Referer     string
	UserAgent   string
	RemoteIP    string
	VisitedURL  string
	RedirectURL *string
	At          time.Time
}
