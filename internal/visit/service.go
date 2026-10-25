package visit

import (
	"context"
	"fmt"
	"log/slog"
	"net"
	"strings"
	"time"

	"github.com/uptrace/bun"

	"github.com/NCUnix/shlink-go/internal/apikey"
	"github.com/NCUnix/shlink-go/internal/clock"
	"github.com/NCUnix/shlink-go/internal/config"
	"github.com/NCUnix/shlink-go/internal/geoip"
	"github.com/NCUnix/shlink-go/internal/page"
	"github.com/NCUnix/shlink-go/internal/types"
	"github.com/NCUnix/shlink-go/internal/ua"
)

type Service struct {
	db     *bun.DB
	geo    *geoip.Locator
	clock  clock.Clock
	cfg    config.Config
	logger *slog.Logger
}

func NewService(db *bun.DB, geo *geoip.Locator, cfg config.Config, clk clock.Clock, logger *slog.Logger) *Service {
	if logger == nil {
		logger = slog.Default()
	}
	return &Service{db: db, geo: geo, cfg: cfg, clock: clk, logger: logger}
}

func (s *Service) Track(ctx context.Context, record Record) error {
	if record.OrphanType != "" && !s.cfg.TrackOrphanVisits {
		return nil
	}
	if record.At.IsZero() {
		record.At = s.clock.Now()
	}

	remoteIP := normalizeIP(record.RemoteIP)
	lookupIP := remoteIP
	if lookupIP != nil && s.cfg.AnonymizeRemoteIP {
		remoteIP = anonymizeIP(lookupIP)
	}
	var location *geoip.Location
	if lookupIP != nil {
		location = s.geo.Lookup(lookupIP)
	}
	potentialBot := ua.IsBot(record.UserAgent)

	var remoteIPValue any
	if remoteIP != nil {
		remoteIPValue = remoteIP.String()
	}
	var latitude, longitude any
	var countryCode, countryName, regionName, cityName, timezone any
	if location != nil {
		latitude, longitude = location.Latitude, location.Longitude
		countryCode, countryName = location.CountryCode, location.CountryName
		regionName, cityName, timezone = location.RegionName, location.CityName, location.Timezone
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()

	_, err = tx.NewRaw(
		`INSERT INTO visits
			(short_url_id, domain_id, visited_at, referer, user_agent, remote_ip, visited_url, redirect_url,
			 potential_bot, orphan_type, country_code, country_name, region_name, city_name, latitude, longitude, timezone)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		record.ShortURLID, record.DomainID, record.At, crop(record.Referer, 1024), crop(record.UserAgent, 512),
		remoteIPValue, crop(record.VisitedURL, 2048), nullableCrop(record.RedirectURL, 2048), potentialBot,
		nullString(record.OrphanType), countryCode, countryName, regionName, cityName, latitude, longitude, timezone,
	).Exec(ctx)
	if err != nil {
		return fmt.Errorf("insert visit: %w", err)
	}
	if record.ShortURLID != nil {
		botIncrement := 0
		nonBotIncrement := 1
		if potentialBot {
			botIncrement, nonBotIncrement = 1, 0
		}
		if _, err := tx.NewRaw(
			`UPDATE short_urls
			 SET visits_total = visits_total + 1,
			     visits_non_bots = visits_non_bots + ?,
			     visits_bots = visits_bots + ?
			 WHERE id = ?`,
			nonBotIncrement, botIncrement, *record.ShortURLID,
		).Exec(ctx); err != nil {
			return fmt.Errorf("update short url visits: %w", err)
		}
	}
	return tx.Commit()
}

func (s *Service) Location(remoteIP string, anonymize bool) *geoip.Location {
	ip := normalizeIP(remoteIP)
	if ip == nil {
		return nil
	}
	if anonymize {
		ip = anonymizeIP(ip)
	}
	return s.geo.Lookup(ip)
}

func (s *Service) GlobalStats(ctx context.Context, scope apikey.Scope) (*Stats, error) {
	nonOrphan, err := s.summary(ctx, scope, false)
	if err != nil {
		return nil, err
	}
	orphan := Summary{}
	if !scope.DenyOrphan {
		orphan, err = s.summary(ctx, scope, true)
		if err != nil {
			return nil, err
		}
	}
	return &Stats{NonOrphanVisits: nonOrphan, OrphanVisits: orphan}, nil
}

func (s *Service) ListForShortURL(ctx context.Context, scope apikey.Scope, shortURLID int64, params ListParams) (*ListResult[Visit], error) {
	return s.list(ctx, scope, params, filter{shortURLID: &shortURLID})
}

func (s *Service) ListForTag(ctx context.Context, scope apikey.Scope, tag string, params ListParams) (*ListResult[Visit], error) {
	return s.list(ctx, scope, params, filter{tag: tag})
}

func (s *Service) ListForDomain(ctx context.Context, scope apikey.Scope, domain string, params ListParams) (*ListResult[Visit], error) {
	return s.list(ctx, scope, params, filter{domain: domain})
}

func (s *Service) ListNonOrphan(ctx context.Context, scope apikey.Scope, params ListParams) (*ListResult[Visit], error) {
	return s.list(ctx, scope, params, filter{})
}

func (s *Service) ListOrphan(ctx context.Context, scope apikey.Scope, params ListParams) (*ListResult[OrphanVisit], error) {
	if scope.DenyOrphan {
		return &ListResult[OrphanVisit]{Data: make([]OrphanVisit, 0), Pagination: page.New(1, 1, 0, 0)}, nil
	}
	result, err := s.listWithOrphan(ctx, scope, params)
	if err != nil {
		return nil, err
	}
	return result, nil
}

func (s *Service) DeleteShortURLVisits(ctx context.Context, shortURLID int64) (int64, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer func() { _ = tx.Rollback() }()
	result, err := tx.NewRaw("DELETE FROM visits WHERE short_url_id = ?", shortURLID).Exec(ctx)
	if err != nil {
		return 0, err
	}
	deleted, _ := result.RowsAffected()
	if _, err := tx.NewRaw(
		`UPDATE short_urls
		 SET visits_total = 0, visits_non_bots = 0, visits_bots = 0
		 WHERE id = ?`,
		shortURLID,
	).Exec(ctx); err != nil {
		return 0, err
	}
	if err := tx.Commit(); err != nil {
		return 0, err
	}
	return deleted, nil
}

func (s *Service) DeleteOrphanVisits(ctx context.Context, scope apikey.Scope) (int64, error) {
	if scope.DenyOrphan {
		return 0, nil
	}
	where := []string{"v.orphan_type IS NOT NULL"}
	args := make([]any, 0, 1)
	conditions, conditionArgs := scope.OrphanConditions("v")
	where = append(where, conditions...)
	args = append(args, conditionArgs...)
	result, err := s.db.NewRaw(
		"DELETE FROM visits v WHERE "+strings.Join(where, " AND "), args...,
	).Exec(ctx)
	if err != nil {
		return 0, err
	}
	return result.RowsAffected()
}

type filter struct {
	shortURLID *int64
	tag        string
	domain     string
	orphan     bool
}

func (s *Service) list(ctx context.Context, scope apikey.Scope, params ListParams, f filter) (*ListResult[Visit], error) {
	where, args, err := s.where(scope, params, f)
	if err != nil {
		return nil, err
	}
	total := 0
	if err := s.db.NewRaw(
		"SELECT count(*) FROM visits v WHERE "+strings.Join(where, " AND "), args...,
	).Scan(ctx, &total); err != nil {
		return nil, err
	}
	perPage := resolvedPageSize(params.ItemsPerPage, total)
	pageNo := resolvedPage(params.Page)
	offset := (pageNo - 1) * perPage
	order := visitOrder(params.OrderBy)
	queryArgs := append(append([]any{}, args...), perPage, offset)
	rows := make([]visitRow, 0)
	if err := s.db.NewRaw(
		`SELECT v.id, v.visited_at, v.referer, v.user_agent, v.visited_url, v.redirect_url, v.potential_bot,
			v.orphan_type, v.country_code, v.country_name, v.region_name, v.city_name,
			v.latitude, v.longitude, v.timezone
		 FROM visits v WHERE `+strings.Join(where, " AND ")+` ORDER BY `+order+` LIMIT ? OFFSET ?`,
		queryArgs...,
	).Scan(ctx, &rows); err != nil {
		return nil, err
	}
	data := make([]Visit, 0, len(rows))
	for _, row := range rows {
		data = append(data, row.visit())
	}
	return &ListResult[Visit]{Data: data, Pagination: page.New(pageNo, perPage, total, len(data))}, nil
}

func (s *Service) listWithOrphan(ctx context.Context, scope apikey.Scope, params ListParams) (*ListResult[OrphanVisit], error) {
	f := filter{orphan: true}
	where, args, err := s.where(scope, params, f)
	if err != nil {
		return nil, err
	}
	total := 0
	if err := s.db.NewRaw(
		"SELECT count(*) FROM visits v WHERE "+strings.Join(where, " AND "), args...,
	).Scan(ctx, &total); err != nil {
		return nil, err
	}
	perPage := resolvedPageSize(params.ItemsPerPage, total)
	pageNo := resolvedPage(params.Page)
	offset := (pageNo - 1) * perPage
	order := visitOrder(params.OrderBy)
	queryArgs := append(append([]any{}, args...), perPage, offset)
	rows := make([]visitRow, 0)
	if err := s.db.NewRaw(
		`SELECT v.id, v.visited_at, v.referer, v.user_agent, v.visited_url, v.redirect_url, v.potential_bot,
			v.orphan_type, v.country_code, v.country_name, v.region_name, v.city_name,
			v.latitude, v.longitude, v.timezone
		 FROM visits v WHERE `+strings.Join(where, " AND ")+` ORDER BY `+order+` LIMIT ? OFFSET ?`,
		queryArgs...,
	).Scan(ctx, &rows); err != nil {
		return nil, err
	}
	data := make([]OrphanVisit, 0, len(rows))
	for _, row := range rows {
		data = append(data, OrphanVisit{Visit: row.visit(), Type: valueOrEmpty(row.OrphanType)})
	}
	return &ListResult[OrphanVisit]{Data: data, Pagination: page.New(pageNo, perPage, total, len(data))}, nil
}

func (s *Service) where(scope apikey.Scope, params ListParams, f filter) ([]string, []any, error) {
	where := make([]string, 0, 8)
	args := make([]any, 0, 8)
	if f.orphan {
		where = append(where, "v.orphan_type IS NOT NULL")
		conditions, conditionArgs := scope.OrphanConditions("v")
		where = append(where, conditions...)
		args = append(args, conditionArgs...)
	} else {
		where = append(where, "v.orphan_type IS NULL")
		conditions, conditionArgs := scope.ShortURLConditions("s")
		if len(conditions) > 0 {
			where = append(where, "EXISTS (SELECT 1 FROM short_urls s WHERE s.id = v.short_url_id AND "+strings.Join(conditions, " AND ")+")")
			args = append(args, conditionArgs...)
		}
	}
	if f.shortURLID != nil {
		where = append(where, "v.short_url_id = ?")
		args = append(args, *f.shortURLID)
	}
	if f.tag != "" {
		where = append(where, `EXISTS (
			SELECT 1 FROM short_url_tags st JOIN tags t ON t.id = st.tag_id
			WHERE st.short_url_id = v.short_url_id AND t.name = ?
		)`)
		args = append(args, f.tag)
	}
	domainFilter := f.domain
	if domainFilter == "" {
		domainFilter = params.Domain
	}
	if domainFilter != "" {
		if strings.EqualFold(domainFilter, "DEFAULT") {
			where = append(where, "v.domain_id = (SELECT id FROM domains WHERE is_default = TRUE)")
		} else {
			where = append(where, "v.domain_id = (SELECT id FROM domains WHERE authority = ?)")
			args = append(args, domainFilter)
		}
	}
	if params.StartDate != nil {
		where = append(where, "v.visited_at >= ?")
		args = append(args, *params.StartDate)
	}
	if params.EndDate != nil {
		where = append(where, "v.visited_at <= ?")
		args = append(args, *params.EndDate)
	}
	if params.ExcludeBots {
		where = append(where, "v.potential_bot = FALSE")
	}
	if f.orphan && params.Type != "" {
		if !validOrphanType(params.Type) {
			return nil, nil, &ValidationError{Elements: []string{"type"}}
		}
		where = append(where, "v.orphan_type = ?")
		args = append(args, params.Type)
	}
	if len(where) == 0 {
		where = append(where, "TRUE")
	}
	return where, args, nil
}

func (s *Service) summary(ctx context.Context, scope apikey.Scope, orphan bool) (Summary, error) {
	where := make([]string, 0, 2)
	args := make([]any, 0, 2)
	if orphan {
		where = append(where, "v.orphan_type IS NOT NULL")
		conditions, conditionArgs := scope.OrphanConditions("v")
		where = append(where, conditions...)
		args = append(args, conditionArgs...)
	} else {
		where = append(where, "v.orphan_type IS NULL")
		conditions, conditionArgs := scope.ShortURLConditions("s")
		if len(conditions) > 0 {
			where = append(where, "EXISTS (SELECT 1 FROM short_urls s WHERE s.id = v.short_url_id AND "+strings.Join(conditions, " AND ")+")")
			args = append(args, conditionArgs...)
		}
	}
	var summary Summary
	if err := s.db.NewRaw(
		`SELECT count(*) AS total,
			count(*) FILTER (WHERE v.potential_bot = FALSE) AS non_bots,
			count(*) FILTER (WHERE v.potential_bot = TRUE) AS bots
		 FROM visits v WHERE `+strings.Join(where, " AND "),
		args...,
	).Scan(ctx, &summary); err != nil {
		return Summary{}, err
	}
	return summary, nil
}

type ValidationError struct {
	Elements []string
}

func (e *ValidationError) Error() string {
	return "invalid elements: " + strings.Join(e.Elements, ", ")
}

type visitRow struct {
	ID           int64     `bun:"id"`
	VisitedAt    time.Time `bun:"visited_at"`
	Referer      *string   `bun:"referer"`
	UserAgent    *string   `bun:"user_agent"`
	VisitedURL   *string   `bun:"visited_url"`
	RedirectURL  *string   `bun:"redirect_url"`
	PotentialBot bool      `bun:"potential_bot"`
	OrphanType   *string   `bun:"orphan_type"`
	CountryCode  *string   `bun:"country_code"`
	CountryName  *string   `bun:"country_name"`
	RegionName   *string   `bun:"region_name"`
	CityName     *string   `bun:"city_name"`
	Latitude     *float64  `bun:"latitude"`
	Longitude    *float64  `bun:"longitude"`
	Timezone     *string   `bun:"timezone"`
}

func (r visitRow) visit() Visit {
	v := Visit{
		Referer:      r.Referer,
		Date:         types.NewTime(r.VisitedAt),
		UserAgent:    valueOrEmpty(r.UserAgent),
		PotentialBot: r.PotentialBot,
		VisitedURL:   r.VisitedURL,
		RedirectURL:  r.RedirectURL,
	}
	if r.CountryCode != nil || r.CountryName != nil || r.CityName != nil {
		location := &geoip.Location{}
		if r.CountryCode != nil {
			location.CountryCode = *r.CountryCode
		}
		if r.CountryName != nil {
			location.CountryName = *r.CountryName
		}
		if r.RegionName != nil {
			location.RegionName = *r.RegionName
		}
		if r.CityName != nil {
			location.CityName = *r.CityName
		}
		if r.Latitude != nil {
			location.Latitude = *r.Latitude
		}
		if r.Longitude != nil {
			location.Longitude = *r.Longitude
		}
		if r.Timezone != nil {
			location.Timezone = *r.Timezone
		}
		v.VisitLocation = location
	}
	return v
}

func validOrphanType(value string) bool {
	return value == OrphanBaseURL || value == OrphanInvalidShortURL || value == OrphanRegular404
}

func visitOrder(orderBy string) string {
	switch strings.ToUpper(orderBy) {
	case "DATE-ASC", "VISITEDAT-ASC":
		return "v.visited_at ASC, v.id ASC"
	default:
		return "v.visited_at DESC, v.id DESC"
	}
}

func resolvedPage(value int) int {
	if value <= 0 {
		return 1
	}
	return value
}

func resolvedPageSize(value, total int) int {
	if value <= 0 {
		if total == 0 {
			return 1
		}
		return total
	}
	if value > 5000 {
		return 5000
	}
	return value
}

func normalizeIP(value string) net.IP {
	value = strings.TrimSpace(value)
	if value == "" {
		return nil
	}
	if host, _, err := net.SplitHostPort(value); err == nil {
		value = host
	}
	ip := net.ParseIP(strings.Trim(value, "[]"))
	return ip
}

func anonymizeIP(ip net.IP) net.IP {
	if ip4 := ip.To4(); ip4 != nil {
		result := append(net.IP(nil), ip4...)
		result[3] = 0
		return result
	}
	if ip6 := ip.To16(); ip6 != nil {
		result := append(net.IP(nil), ip6...)
		for i := 8; i < len(result); i++ {
			result[i] = 0
		}
		return result
	}
	return ip
}

func crop(value string, max int) string {
	if len(value) <= max {
		return value
	}
	return value[:max]
}

func nullableCrop(value *string, max int) any {
	if value == nil {
		return nil
	}
	return crop(*value, max)
}

func nullString(value string) any {
	if value == "" {
		return nil
	}
	return value
}

func valueOrEmpty(value *string) string {
	if value == nil {
		return ""
	}
	return *value
}
