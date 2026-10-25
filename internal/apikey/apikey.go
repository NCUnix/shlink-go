package apikey

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/uptrace/bun"
)

type Scope struct {
	IsAdmin        bool
	AuthorAPIKeyID *int64
	DomainID       *int64
	DenyOrphan     bool
}

func (s Scope) HasShortURLRestrictions() bool {
	return s.AuthorAPIKeyID != nil || s.DomainID != nil
}

type Principal struct {
	APIKeyID int64
	Name     string
	Scope    Scope
}

type Authenticator struct {
	db *bun.DB
}

func NewAuthenticator(db *bun.DB) *Authenticator {
	return &Authenticator{db: db}
}

func Hash(raw string) string {
	sum := sha256.Sum256([]byte(raw))
	return hex.EncodeToString(sum[:])
}

var ErrInvalidKey = errors.New("invalid api key")

func (a *Authenticator) Authenticate(ctx context.Context, raw string) (*Principal, error) {
	if raw == "" {
		return nil, ErrInvalidKey
	}

	var key struct {
		ID        int64      `bun:"id"`
		Name      string     `bun:"name"`
		ExpiresAt *time.Time `bun:"expires_at"`
	}
	err := a.db.NewSelect().
		Table("api_keys").
		Column("id", "name", "expires_at").
		Where("key_hash = ?", Hash(raw)).
		Where("enabled = TRUE").
		Where("(expires_at IS NULL OR expires_at > now())").
		Scan(ctx, &key)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrInvalidKey
		}
		return nil, fmt.Errorf("lookup api key: %w", err)
	}

	principal := &Principal{APIKeyID: key.ID, Name: key.Name}
	var roles []struct {
		Role string          `bun:"role"`
		Meta json.RawMessage `bun:"meta"`
	}
	if err := a.db.NewSelect().
		Table("api_key_roles").
		Column("role", "meta").
		Where("api_key_id = ?", key.ID).
		Scan(ctx, &roles); err != nil {
		return nil, fmt.Errorf("lookup api key roles: %w", err)
	}

	if len(roles) == 0 {
		principal.Scope.IsAdmin = true
		return principal, nil
	}

	for _, role := range roles {
		switch role.Role {
		case "AUTHORED_SHORT_URLS":
			id := key.ID
			principal.Scope.AuthorAPIKeyID = &id
		case "DOMAIN_SPECIFIC":
			var meta struct {
				DomainID int64 `json:"domain_id"`
			}
			if err := json.Unmarshal(role.Meta, &meta); err != nil || meta.DomainID == 0 {
				return nil, fmt.Errorf("invalid DOMAIN_SPECIFIC role metadata")
			}
			id := meta.DomainID
			principal.Scope.DomainID = &id
		case "NO_ORPHAN_VISITS":
			principal.Scope.DenyOrphan = true
		}
	}
	return principal, nil
}

func EnsureInitial(ctx context.Context, db *bun.DB, rawKey string) error {
	if rawKey == "" {
		return nil
	}
	_, err := db.ExecContext(
		ctx,
		`INSERT INTO api_keys (name, key_hash)
		 SELECT ?, ?
		 WHERE NOT EXISTS (SELECT 1 FROM api_keys)`,
		"Initial API key",
		Hash(rawKey),
	)
	if err != nil {
		return fmt.Errorf("create initial api key: %w", err)
	}
	return nil
}
