package shorturl

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/uptrace/bun"

	"github.com/NCUnix/shlink-go/internal/api/types"
	"github.com/NCUnix/shlink-go/internal/apikey"
	"github.com/NCUnix/shlink-go/internal/clock"
	"github.com/NCUnix/shlink-go/internal/config"
	"github.com/NCUnix/shlink-go/internal/domain"
)

type Service struct {
	db            *bun.DB
	domains       *domain.Repository
	clock         clock.Clock
	defaultDomain string
	scheme        string
	codeLength    int
	codeChars     string
}

func NewService(db *bun.DB, domains *domain.Repository, cfg config.Config, clk clock.Clock) *Service {
	scheme := "http"
	if cfg.ISHTTPSEnabled {
		scheme = "https"
	}
	return &Service{
		db:            db,
		domains:       domains,
		clock:         clk,
		defaultDomain: cfg.DefaultDomain,
		scheme:        scheme,
		codeLength:    cfg.DefaultShortCodesLength,
		codeChars:     cfg.DefaultShortCodesChars,
	}
}

func (s *Service) Create(ctx context.Context, scope apikey.Scope, authorAPIKeyID *int64, params CreateParams) (*ShortURL, error) {
	if !ValidLongURL(params.LongURL) {
		return nil, &ValidationError{Elements: []string{"longUrl"}}
	}
	if params.ValidSince != nil && params.ValidUntil != nil && params.ValidSince.After(*params.ValidUntil) {
		return nil, &ValidationError{Elements: []string{"validSince", "validUntil"}}
	}
	if params.MaxVisits != nil && *params.MaxVisits <= 0 {
		return nil, &ValidationError{Elements: []string{"maxVisits"}}
	}

	dom, err := s.resolveCreationDomain(ctx, scope, params.Domain)
	if err != nil {
		return nil, err
	}
	tags := NormalizeTags(params.Tags)

	if params.FindIfExists {
		if id, err := s.findMatching(ctx, scope, dom.ID, params, tags); err != nil {
			return nil, err
		} else if id != 0 {
			return s.getByID(ctx, scope, id)
		}
	}

	base, err := s.buildShortCode(params)
	if err != nil {
		return nil, err
	}
	title := CropTitle(params.Title)
	crawlable := false
	if params.Crawlable != nil {
		crawlable = *params.Crawlable
	}
	forwardQuery := true
	if params.ForwardQuery != nil {
		forwardQuery = *params.ForwardQuery
	}

	var createdID int64
	for attempt := 0; attempt < 6; attempt++ {
		code := base
		if params.CustomSlug == nil && attempt > 0 {
			generated, genErr := s.generateCode(params)
			if genErr != nil {
				return nil, genErr
			}
			code, err = s.applyPathPrefix(params, generated)
			if err != nil {
				return nil, err
			}
		}
		createdID, err = s.insertShortURL(ctx, authorAPIKeyID, dom, code, params, title, crawlable, forwardQuery, tags)
		if err == nil {
			dto, err := s.getByID(ctx, scope, createdID)
			if err != nil {
				return nil, err
			}
			dto.Tags = tags
			return dto, nil
		}
		if !isUniqueViolation(err) {
			return nil, err
		}
		if params.CustomSlug != nil {
			return nil, &ConflictError{Slug: code, Domain: domainName(dom)}
		}
	}
	return nil, fmt.Errorf("could not generate a unique short code")
}

func (s *Service) insertShortURL(
	ctx context.Context,
	authorAPIKeyID *int64,
	dom *domain.Domain,
	code string,
	params CreateParams,
	title *string,
	crawlable, forwardQuery bool,
	tags []string,
) (int64, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer func() { _ = tx.Rollback() }()

	var id int64
	err = tx.QueryRowContext(
		ctx,
		`INSERT INTO short_urls
			(domain_id, short_code, long_url, title, date_created, valid_since, valid_until, max_visits,
			 crawlable, forward_query, author_api_key_id)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		 RETURNING id`,
		dom.ID, code, params.LongURL, title, s.clock.Now(),
		params.ValidSince, params.ValidUntil, params.MaxVisits,
		crawlable, forwardQuery, authorAPIKeyID,
	).Scan(&id)
	if err != nil {
		return 0, fmt.Errorf("insert short url: %w", err)
	}
	if err := s.replaceTags(ctx, tx, id, tags); err != nil {
		return 0, fmt.Errorf("replace tags: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return 0, err
	}
	return id, nil
}

func (s *Service) Get(ctx context.Context, scope apikey.Scope, shortCode string, domainParam *string) (*ShortURL, error) {
	dom, err := s.resolveLookupDomain(ctx, shortCode, domainParam)
	if err != nil {
		return nil, err
	}
	row, err := s.findByCode(ctx, scope, shortCode, dom)
	if err != nil {
		return nil, err
	}
	return s.buildDTO(ctx, row)
}

func (s *Service) ResolveForScope(ctx context.Context, scope apikey.Scope, shortCode string, domainParam *string) (*Resolved, error) {
	dom, err := s.resolveLookupDomain(ctx, shortCode, domainParam)
	if err != nil {
		return nil, err
	}
	row, err := s.findByCode(ctx, scope, shortCode, dom)
	if err != nil {
		return nil, err
	}
	return resolvedFromStored(row), nil
}

func (s *Service) ResolveForHost(ctx context.Context, authority, shortCode string) (*Resolved, error) {
	authority = strings.ToLower(strings.TrimSpace(authority))
	dom, err := s.domains.GetByAuthority(ctx, authority)
	if errors.Is(err, sql.ErrNoRows) {
		dom, err = s.domains.GetDefault(ctx)
	}
	if err != nil {
		return nil, err
	}
	row := new(storedShortURL)
	query := shortURLSelect + `
		WHERE s.short_code = ? AND s.domain_id = ?`
	if err := s.db.NewRaw(query, shortCode, dom.ID).Scan(ctx, row); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, &NotFoundError{ShortCode: shortCode, Domain: domainName(dom)}
		}
		return nil, err
	}
	return resolvedFromStored(row), nil
}

func (s *Service) List(ctx context.Context, scope apikey.Scope, params ListParams) (*ShortURLList, error) {
	if params.Page <= 0 {
		params.Page = 1
	}
	if params.ItemsPerPage <= 0 {
		params.ItemsPerPage = 10
	}
	if params.ItemsPerPage > 5000 {
		params.ItemsPerPage = 5000
	}
	if params.TagsMode == "" {
		params.TagsMode = "any"
	}
	if params.ExcludeTagsMode == "" {
		params.ExcludeTagsMode = "any"
	}
	if params.OrderBy == "" {
		params.OrderBy = "dateCreated-DESC"
	}
	invalid := make([]string, 0)
	if params.TagsMode != "any" && params.TagsMode != "all" {
		invalid = append(invalid, "tagsMode")
	}
	if params.ExcludeTagsMode != "any" && params.ExcludeTagsMode != "all" {
		invalid = append(invalid, "excludeTagsMode")
	}
	orderClause, ok := orderByClause(params.OrderBy)
	if !ok {
		invalid = append([]string{"orderBy"}, invalid...)
	}
	if len(invalid) > 0 {
		return nil, &ValidationError{Elements: invalid}
	}

	where, args := s.listWhere(scope, params)
	countQuery := "SELECT count(*) FROM short_urls s JOIN domains d ON d.id = s.domain_id WHERE " + strings.Join(where, " AND ")
	var total int
	if err := s.db.NewRaw(countQuery, args...).Scan(ctx, &total); err != nil {
		return nil, err
	}

	offset := (params.Page - 1) * params.ItemsPerPage
	query := shortURLSelect + `
		WHERE ` + strings.Join(where, " AND ") + ` ORDER BY ` + orderClause + ` LIMIT ? OFFSET ?`
	rows := make([]storedShortURL, 0)
	queryArgs := append(append([]any{}, args...), params.ItemsPerPage, offset)
	if err := s.db.NewRaw(query, queryArgs...).Scan(ctx, &rows); err != nil {
		return nil, err
	}

	ids := make([]int64, 0, len(rows))
	for i := range rows {
		ids = append(ids, rows[i].ID)
	}
	tagsByID, err := s.tagsForMany(ctx, ids)
	if err != nil {
		return nil, err
	}

	data := make([]ShortURL, 0, len(rows))
	for i := range rows {
		dto, err := s.buildDTOWithTags(&rows[i], tagsByID[rows[i].ID])
		if err != nil {
			return nil, err
		}
		data = append(data, *dto)
	}
	pages := 0
	if total > 0 {
		pages = (total + params.ItemsPerPage - 1) / params.ItemsPerPage
	}
	return &ShortURLList{
		Data: data,
		Pagination: Pagination{
			CurrentPage:        params.Page,
			PagesCount:         pages,
			ItemsPerPage:       params.ItemsPerPage,
			ItemsInCurrentPage: len(data),
			TotalItems:         total,
		},
	}, nil
}

func (s *Service) Update(ctx context.Context, scope apikey.Scope, shortCode string, domainParam *string, params UpdateParams) (*ShortURL, error) {
	dom, err := s.resolveLookupDomain(ctx, shortCode, domainParam)
	if err != nil {
		return nil, err
	}
	row, err := s.findByCode(ctx, scope, shortCode, dom)
	if err != nil {
		return nil, err
	}

	sets := make([]string, 0, 8)
	args := make([]any, 0, 8)
	invalid := make([]string, 0)
	add := func(column string, value any) {
		args = append(args, value)
		sets = append(sets, fmt.Sprintf("%s = ?", column))
	}

	if params.LongURL != nil {
		if !ValidLongURL(*params.LongURL) {
			invalid = append(invalid, "longUrl")
		} else {
			add("long_url", *params.LongURL)
		}
	}
	if params.ValidSince.Set {
		if params.ValidSince.Valid {
			add("valid_since", params.ValidSince.Value.Time)
		} else {
			add("valid_since", nil)
		}
	}
	if params.ValidUntil.Set {
		if params.ValidUntil.Valid {
			add("valid_until", params.ValidUntil.Value.Time)
		} else {
			add("valid_until", nil)
		}
	}
	if params.MaxVisits.Set {
		if params.MaxVisits.Valid && params.MaxVisits.Value <= 0 {
			invalid = append(invalid, "maxVisits")
		} else if params.MaxVisits.Valid {
			add("max_visits", params.MaxVisits.Value)
		} else {
			add("max_visits", nil)
		}
	}
	if params.Title.Set {
		if params.Title.Valid {
			add("title", CropTitle(&params.Title.Value))
		} else {
			add("title", nil)
		}
	}
	if params.Crawlable != nil {
		add("crawlable", *params.Crawlable)
	}
	if params.ForwardQuery != nil {
		add("forward_query", *params.ForwardQuery)
	}
	if len(invalid) > 0 {
		return nil, &ValidationError{Elements: invalid}
	}

	if len(sets) > 0 {
		args = append(args, row.ID)
		query := "UPDATE short_urls SET " + strings.Join(sets, ", ") + " WHERE id = ?"
		if _, err := s.db.ExecContext(ctx, query, args...); err != nil {
			return nil, err
		}
	}
	if params.Tags != nil {
		tx, err := s.db.BeginTx(ctx, nil)
		if err != nil {
			return nil, err
		}
		if err := s.replaceTags(ctx, tx, row.ID, NormalizeTags(*params.Tags)); err != nil {
			_ = tx.Rollback()
			return nil, err
		}
		if err := tx.Commit(); err != nil {
			return nil, err
		}
	}
	return s.getByID(ctx, scope, row.ID)
}

func (s *Service) Delete(ctx context.Context, scope apikey.Scope, shortCode string, domainParam *string) error {
	dom, err := s.resolveLookupDomain(ctx, shortCode, domainParam)
	if err != nil {
		return err
	}
	row, err := s.findByCode(ctx, scope, shortCode, dom)
	if err != nil {
		return err
	}
	result, err := s.db.ExecContext(ctx, "DELETE FROM short_urls WHERE id = ?", row.ID)
	if err != nil {
		return err
	}
	n, _ := result.RowsAffected()
	if n == 0 {
		return &NotFoundError{ShortCode: shortCode, Domain: domainName(dom)}
	}
	return nil
}

func (s *Service) CrawlableCodes(ctx context.Context, out *[]string) error {
	return s.db.NewRaw(
		"SELECT DISTINCT short_code FROM short_urls WHERE crawlable = TRUE ORDER BY short_code ASC",
	).Scan(ctx, out)
}

func (s *Service) resolveCreationDomain(ctx context.Context, scope apikey.Scope, requested *string) (*domain.Domain, error) {
	if scope.DomainID != nil {
		d, err := s.domains.Get(ctx, *scope.DomainID)
		if err != nil {
			return nil, err
		}
		return d, nil
	}
	if requested == nil || strings.TrimSpace(*requested) == "" || strings.EqualFold(*requested, "DEFAULT") {
		return s.domains.GetDefault(ctx)
	}
	if !ValidAuthority(*requested) {
		return nil, &ValidationError{Elements: []string{"domain"}}
	}
	d, err := s.domains.GetByAuthority(ctx, *requested)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, &ValidationError{Elements: []string{"domain"}}
	}
	return d, err
}

func (s *Service) resolveLookupDomain(ctx context.Context, shortCode string, requested *string) (*domain.Domain, error) {
	if requested == nil || strings.TrimSpace(*requested) == "" || strings.EqualFold(*requested, "DEFAULT") {
		return s.domains.GetDefault(ctx)
	}
	d, err := s.domains.GetByAuthority(ctx, *requested)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, &NotFoundError{ShortCode: shortCode, Domain: *requested}
	}
	return d, err
}

func (s *Service) findByCode(ctx context.Context, scope apikey.Scope, shortCode string, dom *domain.Domain) (*storedShortURL, error) {
	where, args := s.scopeWhere(scope)
	where = append(where, "s.short_code = ?", "s.domain_id = ?")
	args = append(args, shortCode, dom.ID)
	row := new(storedShortURL)
	query := shortURLSelect + `
		WHERE ` + strings.Join(where, " AND ")
	if err := s.db.NewRaw(query, args...).Scan(ctx, row); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, &NotFoundError{ShortCode: shortCode, Domain: domainName(dom)}
		}
		return nil, err
	}
	return row, nil
}

func (s *Service) getByID(ctx context.Context, scope apikey.Scope, id int64) (*ShortURL, error) {
	row := new(storedShortURL)
	where, args := s.scopeWhere(scope)
	where = append(where, "s.id = ?")
	args = append(args, id)
	query := shortURLSelect + `
		WHERE ` + strings.Join(where, " AND ")
	if err := s.db.NewRaw(query, args...).Scan(ctx, row); err != nil {
		return nil, err
	}
	return s.buildDTO(ctx, row)
}

func (s *Service) buildDTO(ctx context.Context, row *storedShortURL) (*ShortURL, error) {
	tags, err := s.tagsFor(ctx, row.ID)
	if err != nil {
		return nil, err
	}
	return s.buildDTOWithTags(row, tags)
}

func (s *Service) buildDTOWithTags(row *storedShortURL, tags []string) (*ShortURL, error) {
	if tags == nil {
		tags = make([]string, 0)
	}
	var title *string
	if row.Title != nil {
		title = row.Title
	}
	var domainName *string
	if !row.IsDefault {
		authority := row.Authority
		domainName = &authority
	}
	result := &ShortURL{
		ShortCode:   row.ShortCode,
		ShortURL:    s.scheme + "://" + row.Authority + "/" + row.ShortCode,
		LongURL:     row.LongURL,
		DateCreated: types.NewTime(row.DateCreated),
		VisitsSummary: VisitsSummary{
			Total:   row.VisitsTotal,
			NonBots: row.VisitsNonBots,
			Bots:    row.VisitsBots,
		},
		Tags:             tags,
		Domain:           domainName,
		Title:            title,
		Crawlable:        row.Crawlable,
		ForwardQuery:     row.ForwardQuery,
		HasRedirectRules: row.HasRedirectRules,
	}
	if row.MaxVisits != nil {
		v := *row.MaxVisits
		result.Meta.MaxVisits = &v
	}
	if row.ValidSince != nil {
		v := types.NewTime(*row.ValidSince)
		result.Meta.ValidSince = &v
	}
	if row.ValidUntil != nil {
		v := types.NewTime(*row.ValidUntil)
		result.Meta.ValidUntil = &v
	}
	return result, nil
}

func resolvedFromStored(row *storedShortURL) *Resolved {
	return &Resolved{
		ID:               row.ID,
		DomainID:         row.DomainID,
		ShortCode:        row.ShortCode,
		LongURL:          row.LongURL,
		ForwardQuery:     row.ForwardQuery,
		Crawlable:        row.Crawlable,
		DomainAuthority:  row.Authority,
		IsDefaultDomain:  row.IsDefault,
		ValidSince:       row.ValidSince,
		ValidUntil:       row.ValidUntil,
		MaxVisits:        row.MaxVisits,
		VisitsTotal:      row.VisitsTotal,
		HasRedirectRules: row.HasRedirectRules,
	}
}

func (s *Service) tagsFor(ctx context.Context, shortURLID int64) ([]string, error) {
	tags := make([]string, 0)
	if err := s.db.NewRaw(
		`SELECT t.name FROM short_url_tags st JOIN tags t ON t.id = st.tag_id
		 WHERE st.short_url_id = ? ORDER BY t.name`,
		shortURLID,
	).Scan(ctx, &tags); err != nil {
		return nil, err
	}
	return tags, nil
}

func (s *Service) tagsForMany(ctx context.Context, ids []int64) (map[int64][]string, error) {
	result := make(map[int64][]string, len(ids))
	for _, id := range ids {
		result[id] = make([]string, 0)
	}
	if len(ids) == 0 {
		return result, nil
	}
	args := make([]any, len(ids))
	for i, id := range ids {
		args[i] = id
	}
	rows := make([]struct {
		ShortURLID int64  `bun:"short_url_id"`
		Name       string `bun:"name"`
	}, 0)
	if err := s.db.NewRaw(
		`SELECT st.short_url_id, t.name
		 FROM short_url_tags st JOIN tags t ON t.id = st.tag_id
		 WHERE st.short_url_id IN (`+makePlaceholders(len(ids))+`)
		 ORDER BY st.short_url_id, t.name`,
		args...,
	).Scan(ctx, &rows); err != nil {
		return nil, err
	}
	for _, row := range rows {
		result[row.ShortURLID] = append(result[row.ShortURLID], row.Name)
	}
	return result, nil
}

func (s *Service) replaceTags(ctx context.Context, tx bun.Tx, shortURLID int64, tags []string) error {
	if _, err := tx.ExecContext(ctx, "DELETE FROM short_url_tags WHERE short_url_id = ?", shortURLID); err != nil {
		return err
	}
	for _, name := range tags {
		var tagID int64
		err := tx.QueryRowContext(
			ctx,
			`INSERT INTO tags (name) VALUES (?)
			 ON CONFLICT (name) DO UPDATE SET name = EXCLUDED.name
			 RETURNING id`,
			name,
		).Scan(&tagID)
		if err != nil {
			return err
		}
		if _, err := tx.ExecContext(
			ctx,
			"INSERT INTO short_url_tags (short_url_id, tag_id) VALUES (?, ?) ON CONFLICT DO NOTHING",
			shortURLID, tagID,
		); err != nil {
			return err
		}
	}
	return nil
}

func (s *Service) findMatching(ctx context.Context, scope apikey.Scope, domainID int64, params CreateParams, tags []string) (int64, error) {
	query := `SELECT s.id FROM short_urls s
		WHERE s.domain_id = ? AND s.long_url = ?
		AND s.valid_since IS NOT DISTINCT FROM ?
		AND s.valid_until IS NOT DISTINCT FROM ?
		AND s.max_visits IS NOT DISTINCT FROM ?`
	args := []any{domainID, params.LongURL, params.ValidSince, params.ValidUntil, params.MaxVisits}
	// Apply the same restrictions as the read endpoints, so an existing short URL
	// outside the key's scope is never returned (and never leaks its details).
	conditions, conditionArgs := scope.ShortURLConditions("s")
	for _, condition := range conditions {
		query += " AND " + condition
	}
	args = append(args, conditionArgs...)
	if params.CustomSlug != nil {
		slug, err := NormalizeSlug(*params.CustomSlug)
		if err != nil {
			return 0, &ValidationError{Elements: []string{"customSlug"}}
		}
		query += " AND s.short_code = ?"
		args = append(args, slug)
	}
	query += " ORDER BY s.id DESC"
	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return 0, err
	}
	defer rows.Close()
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return 0, err
		}
		existingTags, err := s.tagsFor(ctx, id)
		if err != nil {
			return 0, err
		}
		if equalStrings(existingTags, sortedCopy(tags)) {
			return id, nil
		}
	}
	return 0, rows.Err()
}

func (s *Service) buildShortCode(params CreateParams) (string, error) {
	var base string
	if params.CustomSlug != nil {
		slug, err := NormalizeSlug(*params.CustomSlug)
		if err != nil {
			return "", &ValidationError{Elements: []string{"customSlug"}}
		}
		base = slug
	} else {
		code, err := s.generateCode(params)
		if err != nil {
			return "", err
		}
		base = code
	}
	return s.applyPathPrefix(params, base)
}

func (s *Service) generateCode(params CreateParams) (string, error) {
	length := s.codeLength
	if params.ShortCodeLength != nil {
		length = *params.ShortCodeLength
		if length < 4 {
			return "", &ValidationError{Elements: []string{"shortCodeLength"}}
		}
	}
	return GenerateCode(length, s.codeChars)
}

func (s *Service) applyPathPrefix(params CreateParams, base string) (string, error) {
	if params.PathPrefix != nil && strings.TrimSpace(*params.PathPrefix) != "" {
		prefix, err := NormalizeSlug(*params.PathPrefix)
		if err != nil {
			return "", &ValidationError{Elements: []string{"pathPrefix"}}
		}
		base = prefix + "-" + base
	}
	return base, nil
}

func sortedCopy(values []string) []string {
	out := append([]string(nil), values...)
	sort.Strings(out)
	return out
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func domainName(d *domain.Domain) string {
	if d.IsDefault {
		return ""
	}
	return d.Authority
}

func isUniqueViolation(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == "23505"
}
