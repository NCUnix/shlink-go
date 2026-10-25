package redirect

import (
	"encoding/base64"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/NCUnix/shlink-go/internal/clock"
	"github.com/NCUnix/shlink-go/internal/config"
	"github.com/NCUnix/shlink-go/internal/domain"
	"github.com/NCUnix/shlink-go/internal/problem"
	"github.com/NCUnix/shlink-go/internal/redirectrule"
	"github.com/NCUnix/shlink-go/internal/shorturl"
	"github.com/NCUnix/shlink-go/internal/visit"
)

const robotsHeader = `# For more information about the robots.txt standard, see:
# https://www.robotstxt.org/orig.html

User-agent: *
`

var trackingPixel = func() []byte {
	pixel, _ := base64.StdEncoding.DecodeString("R0lGODlhAQABAIAAAAAAAP///ywAAAAAAQABAAACAUwAOw==")
	return pixel
}()

type Service struct {
	shortURLs *shorturl.Service
	rules     *redirectrule.Service
	visits    *visit.Service
	domains   *domain.Repository
	cfg       config.Config
	clock     clock.Clock
	logger    *slog.Logger
}

func NewService(
	shortURLs *shorturl.Service,
	rules *redirectrule.Service,
	visits *visit.Service,
	domains *domain.Repository,
	cfg config.Config,
	clk clock.Clock,
	logger *slog.Logger,
) *Service {
	if logger == nil {
		logger = slog.Default()
	}
	return &Service{
		shortURLs: shortURLs,
		rules:     rules,
		visits:    visits,
		domains:   domains,
		cfg:       cfg,
		clock:     clk,
		logger:    logger,
	}
}

func (s *Service) Register(engine *gin.Engine) {
	engine.GET("/", s.handleBaseURL)
	engine.GET("/robots.txt", s.handleRobots)
	engine.GET("/:shortCode/track", s.handleTrack)
	engine.GET("/:shortCode", s.handleShortURL)
	engine.NoRoute(s.handleNoRoute)
}

func (s *Service) handleShortURL(c *gin.Context) {
	s.handleShortURLWithExtra(c, c.Param("shortCode"), "")
}

func (s *Service) handleShortURLWithExtra(c *gin.Context, shortCode, extraPath string) {
	if strings.HasPrefix(c.Request.URL.Path, "/rest/") {
		problem.Write(c, problem.NotFound())
		return
	}
	if extraPath == "/track" || strings.HasPrefix(extraPath, "/track/") {
		c.Status(http.StatusNotFound)
		return
	}
	if extraPath == "/" {
		if !s.cfg.ShortURLTrailingSlash {
			s.handleNotFound(c, visit.OrphanRegular404)
			return
		}
		extraPath = ""
	}
	if extraPath != "" && s.cfg.ExtraPathMode == "default" {
		s.handleNotFound(c, visit.OrphanRegular404)
		return
	}

	authority := requestAuthority(c.Request)
	resolved, err := s.shortURLs.ResolveForHost(c.Request.Context(), authority, shortCode)
	if err != nil {
		s.handleNotFound(c, visit.OrphanInvalidShortURL)
		return
	}
	if !isActive(resolved, s.clock.Now()) {
		s.handleNotFound(c, visit.OrphanInvalidShortURL, resolved.DomainID)
		return
	}

	target := resolved.LongURL
	if resolved.HasRedirectRules {
		set, err := s.rules.Get(c.Request.Context(), resolved.ID, resolved.LongURL)
		if err != nil {
			s.logger.Error("load_redirect_rules", "error", err, "short_code", resolved.ShortCode)
		} else {
			target = s.rules.Match(set, s.ruleRequest(c.Request))
		}
	}
	if target == "" {
		target = resolved.LongURL
	}

	if extraPath != "" && s.cfg.ExtraPathMode == "append" {
		target = appendPath(target, extraPath)
	}
	target = buildRedirectURL(target, c.Request, resolved.ForwardQuery, s.cfg.DisabledTrackParams())

	s.track(c, resolved.DomainID, resolved.ID, &target)
	c.Redirect(http.StatusFound, target)
}

func (s *Service) handleTrack(c *gin.Context) {
	shortCode := c.Param("shortCode")
	resolved, err := s.shortURLs.ResolveForHost(c.Request.Context(), requestAuthority(c.Request), shortCode)
	if err == nil && isActive(resolved, s.clock.Now()) {
		s.track(c, resolved.DomainID, resolved.ID, nil)
	}
	c.Data(http.StatusOK, "image/gif", trackingPixel)
}

func (s *Service) handleBaseURL(c *gin.Context) {
	s.handleNotFound(c, visit.OrphanBaseURL)
}

func (s *Service) handleNoRoute(c *gin.Context) {
	if strings.HasPrefix(c.Request.URL.Path, "/rest/") {
		problem.Write(c, problem.NotFound())
		return
	}
	path := c.Request.URL.Path
	parts := strings.Split(strings.Trim(path, "/"), "/")
	if len(parts) == 1 && parts[0] != "" && strings.HasSuffix(path, "/") {
		if s.cfg.ShortURLTrailingSlash {
			s.handleShortURLWithExtra(c, parts[0], "/")
			return
		}
	}
	if len(parts) > 1 && parts[0] != "" {
		s.handleShortURLWithExtra(c, parts[0], "/"+strings.Join(parts[1:], "/"))
		return
	}
	s.handleNotFound(c, visit.OrphanRegular404)
}

func (s *Service) handleNotFound(c *gin.Context, orphanType string, knownDomainID ...int64) {
	dom, _ := s.domainForRequest(c.Request)
	domainID := int64(0)
	if dom != nil {
		domainID = dom.ID
	}
	if len(knownDomainID) > 0 {
		domainID = knownDomainID[0]
	}

	redirectURL := ""
	if dom != nil {
		switch orphanType {
		case visit.OrphanBaseURL:
			redirectURL = value(dom.BaseURLRedirect)
		case visit.OrphanRegular404:
			redirectURL = value(dom.Regular404Redirect)
		case visit.OrphanInvalidShortURL:
			redirectURL = value(dom.InvalidShortURLRedirect)
		}
	}
	if redirectURL != "" {
		redirectURL = resolvePlaceholders(c.Request, redirectURL)
		s.trackOrphan(c, domainID, orphanType, &redirectURL)
		c.Redirect(http.StatusFound, redirectURL)
		return
	}
	s.trackOrphan(c, domainID, orphanType, nil)
	if orphanType == visit.OrphanInvalidShortURL {
		c.Data(http.StatusNotFound, "text/html; charset=utf-8", []byte(invalidShortURLHTML))
		return
	}
	c.Data(http.StatusNotFound, "text/html; charset=utf-8", []byte(notFoundHTML))
}

func (s *Service) handleRobots(c *gin.Context) {
	codes := make([]string, 0)
	if err := s.shortURLs.CrawlableCodes(c.Request.Context(), &codes); err != nil {
		s.logger.Error("list_crawlable_codes", "error", err)
		c.String(http.StatusInternalServerError, "Internal Server Error")
		return
	}
	var body strings.Builder
	body.WriteString(robotsHeader)
	for _, code := range codes {
		body.WriteString("Allow: /")
		body.WriteString(code)
		body.WriteByte('\n')
	}
	body.WriteString("Disallow: /\n")
	c.Data(http.StatusOK, "text/plain; charset=utf-8", []byte(body.String()))
}

func (s *Service) ruleRequest(request *http.Request) redirectrule.RequestData {
	ip := net.ParseIP(s.clientIP(request))
	location := s.visits.Location(s.clientIP(request), false)
	data := redirectrule.RequestData{
		UserAgent:      request.UserAgent(),
		AcceptLanguage: request.Header.Get("Accept-Language"),
		Query:          request.URL.Query(),
		IP:             ip,
		Now:            s.clock.Now(),
	}
	if location != nil {
		data.CountryCode = location.CountryCode
		data.CityName = location.CityName
	}
	return data
}

func (s *Service) track(c *gin.Context, domainID, shortURLID int64, redirectURL *string) {
	if s.trackingDisabled(c.Request) {
		return
	}
	shortID := shortURLID
	domID := domainID
	record := visit.Record{
		ShortURLID:  &shortID,
		DomainID:    &domID,
		Referer:     c.Request.Referer(),
		UserAgent:   c.Request.UserAgent(),
		RemoteIP:    s.clientIP(c.Request),
		VisitedURL:  visitedURL(c.Request),
		RedirectURL: redirectURL,
		At:          s.clock.Now(),
	}
	if err := s.visits.Track(c.Request.Context(), record); err != nil {
		s.logger.Error("track_visit", "error", err)
	}
}

func (s *Service) trackOrphan(c *gin.Context, domainID int64, orphanType string, redirectURL *string) {
	if s.trackingDisabled(c.Request) {
		return
	}
	record := visit.Record{
		DomainID:    &domainID,
		OrphanType:  orphanType,
		Referer:     c.Request.Referer(),
		UserAgent:   c.Request.UserAgent(),
		RemoteIP:    s.clientIP(c.Request),
		VisitedURL:  visitedURL(c.Request),
		RedirectURL: redirectURL,
		At:          s.clock.Now(),
	}
	if err := s.visits.Track(c.Request.Context(), record); err != nil {
		s.logger.Error("track_orphan_visit", "error", err)
	}
}

func (s *Service) trackingDisabled(request *http.Request) bool {
	for _, param := range s.cfg.DisabledTrackParams() {
		if _, ok := request.URL.Query()[param]; ok {
			return true
		}
	}
	return false
}

func (s *Service) domainForRequest(request *http.Request) (*domain.Domain, error) {
	authority := requestAuthority(request)
	dom, err := s.domains.GetByAuthority(request.Context(), authority)
	if err == nil {
		return dom, nil
	}
	return s.domains.GetDefault(request.Context())
}

func isActive(resolved *shorturl.Resolved, now time.Time) bool {
	if resolved.ValidSince != nil && now.Before(*resolved.ValidSince) {
		return false
	}
	if resolved.ValidUntil != nil && !now.Before(*resolved.ValidUntil) {
		return false
	}
	return resolved.MaxVisits == nil || resolved.VisitsTotal < int64(*resolved.MaxVisits)
}

func requestAuthority(request *http.Request) string {
	host := strings.ToLower(strings.TrimSpace(request.Host))
	if host == "" {
		return ""
	}
	if parsedHost, _, err := net.SplitHostPort(host); err == nil {
		return parsedHost
	}
	return strings.Trim(host, "[]")
}

func visitedURL(request *http.Request) string {
	scheme := "http"
	if request.TLS != nil {
		scheme = "https"
	}
	if forwarded := request.Header.Get("X-Forwarded-Proto"); forwarded != "" {
		scheme = strings.TrimSpace(strings.Split(forwarded, ",")[0])
	}
	return scheme + "://" + request.Host + request.URL.RequestURI()
}

func buildRedirectURL(raw string, request *http.Request, forwardQuery bool, disabled []string) string {
	target, err := url.Parse(raw)
	if err != nil {
		return raw
	}
	if !forwardQuery {
		return target.String()
	}
	baseQuery := target.Query()
	currentQuery := request.URL.Query()
	for _, param := range disabled {
		delete(currentQuery, param)
	}
	for key, values := range currentQuery {
		baseQuery[key] = append([]string(nil), values...)
	}
	if len(baseQuery) > 0 {
		target.RawQuery = encodeQuery(baseQuery)
	}
	return target.String()
}

func appendPath(raw, extraPath string) string {
	target, err := url.Parse(raw)
	if err != nil {
		return raw
	}
	target.Path = strings.TrimRight(target.Path, "/") + extraPath
	return target.String()
}

func encodeQuery(values url.Values) string {
	encoded := values.Encode()
	return strings.ReplaceAll(encoded, "+", "%20")
}

func resolvePlaceholders(request *http.Request, raw string) string {
	replaced := strings.ReplaceAll(raw, url.QueryEscape("{DOMAIN}"), requestAuthority(request))
	replaced = strings.ReplaceAll(replaced, url.QueryEscape("{ORIGINAL_PATH}"), url.QueryEscape(request.URL.Path))
	replaced = strings.ReplaceAll(replaced, "{DOMAIN}", requestAuthority(request))
	replaced = strings.ReplaceAll(replaced, "{ORIGINAL_PATH}", request.URL.Path)
	return replaced
}

func value(v *string) string {
	if v == nil {
		return ""
	}
	return *v
}

const notFoundHTML = `<!doctype html>
<html lang="en"><head><meta charset="utf-8"><title>404 Not Found</title></head>
<body><h1>404 Not Found</h1><p>The requested URL could not be found.</p></body></html>`

const invalidShortURLHTML = `<!doctype html>
<html lang="en"><head><meta charset="utf-8"><title>404 Not Found</title></head>
<body><h1>404 Not Found</h1><p>The short URL is invalid or has expired.</p></body></html>`
