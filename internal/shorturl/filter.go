package shorturl

import (
	"fmt"
	"strings"

	"github.com/NCUnix/shlink-go/internal/apikey"
)

func (s *Service) scopeWhere(scope apikey.Scope) ([]string, []any) {
	where, args := scope.ShortURLConditions("s")
	if len(where) == 0 {
		where = append(where, "TRUE")
	}
	return where, args
}

func (s *Service) listWhere(scope apikey.Scope, params ListParams) ([]string, []any) {
	where, args := s.scopeWhere(scope)
	if params.SearchTerm != "" {
		term := "%" + params.SearchTerm + "%"
		where = append(where, "(s.long_url ILIKE ? OR s.short_code ILIKE ? OR s.title ILIKE ? OR d.authority ILIKE ?)")
		args = append(args, term, term, term, term)
	}
	if params.Domain != nil && *params.Domain != "" {
		if strings.EqualFold(*params.Domain, "DEFAULT") {
			where = append(where, "d.is_default = TRUE")
		} else {
			where = append(where, "d.authority = ?")
			args = append(args, *params.Domain)
		}
	}
	if params.StartDate != nil {
		where = append(where, "s.date_created >= ?")
		args = append(args, *params.StartDate)
	}
	if params.EndDate != nil {
		where = append(where, "s.date_created <= ?")
		args = append(args, *params.EndDate)
	}
	if params.ExcludeMaxVisitsReached {
		where = append(where, "(s.max_visits IS NULL OR s.visits_total < s.max_visits)")
	}
	if params.ExcludePastValidUntil {
		where = append(where, "(s.valid_until IS NULL OR s.valid_until > now())")
	}
	if len(params.Tags) > 0 {
		tags := NormalizeTags(params.Tags)
		if len(tags) > 0 {
			where = append(where, tagCondition("s.id", tags, params.TagsMode))
			args = append(args, stringsToAny(tags)...)
		}
	}
	if len(params.ExcludeTags) > 0 {
		tags := NormalizeTags(params.ExcludeTags)
		if len(tags) > 0 {
			condition := tagCondition("s.id", tags, params.ExcludeTagsMode)
			where = append(where, "NOT ("+condition+")")
			args = append(args, stringsToAny(tags)...)
		}
	}
	if params.APIKeyName != "" && scope.IsAdmin {
		where = append(where, "s.author_api_key_id = (SELECT id FROM api_keys WHERE name = ?)")
		args = append(args, params.APIKeyName)
	}
	return where, args
}

func tagCondition(shortURLColumn string, tags []string, mode string) string {
	placeholders := makePlaceholders(len(tags))
	base := fmt.Sprintf(
		"EXISTS (SELECT 1 FROM short_url_tags st JOIN tags t ON t.id = st.tag_id WHERE st.short_url_id = %s AND t.name IN (%s))",
		shortURLColumn, placeholders,
	)
	if mode == "all" {
		return fmt.Sprintf(
			"(SELECT count(DISTINCT t.name) FROM short_url_tags st JOIN tags t ON t.id = st.tag_id WHERE st.short_url_id = %s AND t.name IN (%s)) = %d",
			shortURLColumn, placeholders, len(tags),
		)
	}
	return base
}

func orderByClause(orderBy string) (string, bool) {
	parts := strings.SplitN(orderBy, "-", 2)
	field := parts[0]
	direction := "ASC"
	if len(parts) == 2 {
		direction = strings.ToUpper(parts[1])
		if direction != "ASC" && direction != "DESC" {
			return "", false
		}
	}
	column := ""
	switch field {
	case "dateCreated":
		column = "s.date_created"
	case "shortCode":
		column = "s.short_code"
	case "longUrl":
		column = "s.long_url"
	case "title":
		column = "s.title"
	case "visits":
		column = "s.visits_total"
	case "nonBotVisits":
		column = "s.visits_non_bots"
	default:
		return "", false
	}
	result := column + " " + direction
	if field == "title" {
		result += " NULLS LAST"
	}
	result += ", s.id ASC"
	return result, true
}

func makePlaceholders(n int) string {
	if n <= 0 {
		return ""
	}
	parts := make([]string, n)
	for i := range parts {
		parts[i] = "?"
	}
	return strings.Join(parts, ",")
}

func stringsToAny(values []string) []any {
	out := make([]any, len(values))
	for i, value := range values {
		out[i] = value
	}
	return out
}
