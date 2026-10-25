package apikey

// Scope-to-SQL conditions live here, next to the Scope they derive from, so
// every service filters visibility the same way instead of re-deriving rules.

// ShortURLConditions returns the where fragments (and args) restricting a
// query to the short URLs the scope may see. alias is the short_urls table
// alias used by the caller.
func (s Scope) ShortURLConditions(alias string) ([]string, []any) {
	where := make([]string, 0, 2)
	args := make([]any, 0, 2)
	if s.AuthorAPIKeyID != nil {
		where = append(where, alias+".author_api_key_id = ?")
		args = append(args, *s.AuthorAPIKeyID)
	}
	if s.DomainID != nil {
		where = append(where, alias+".domain_id = ?")
		args = append(args, *s.DomainID)
	}
	return where, args
}

// OrphanConditions restricts orphan visits to the domains the scope owns.
// Orphan visits have no short URL, so a key limited to authored short URLs
// cannot see any of them.
func (s Scope) OrphanConditions(alias string) ([]string, []any) {
	if s.DomainID != nil {
		return []string{alias + ".domain_id = ?"}, []any{*s.DomainID}
	}
	if s.AuthorAPIKeyID != nil {
		return []string{"FALSE"}, nil
	}
	return nil, nil
}
