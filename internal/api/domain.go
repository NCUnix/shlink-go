package api

import (
	"context"
	"net/http"
	"strings"

	"github.com/danielgtaylor/huma/v2"

	"github.com/NCUnix/shlink-go/internal/apikey"
	"github.com/NCUnix/shlink-go/internal/domain"
	"github.com/NCUnix/shlink-go/internal/problem"
	"github.com/NCUnix/shlink-go/internal/shorturl"
	"github.com/NCUnix/shlink-go/internal/types"
)

type domainRedirects struct {
	BaseURLRedirect         *string `json:"baseUrlRedirect"`
	Regular404Redirect      *string `json:"regular404Redirect"`
	InvalidShortURLRedirect *string `json:"invalidShortUrlRedirect"`
}

type domainInfo struct {
	Domain    string          `json:"domain"`
	IsDefault bool            `json:"isDefault"`
	Redirects domainRedirects `json:"redirects"`
}

type listDomainsOutput struct {
	Body struct {
		Domains struct {
			Data             []domainInfo    `json:"data"`
			DefaultRedirects domainRedirects `json:"defaultRedirects"`
		} `json:"domains"`
	}
}

type updateDomainRedirectsInput struct {
	RawBody []byte `contentType:"application/json"`
}

type updateDomainRedirectsBody struct {
	Domain                  string                          `json:"domain"`
	BaseURLRedirect         types.OmittableNullable[string] `json:"baseUrlRedirect"`
	Regular404Redirect      types.OmittableNullable[string] `json:"regular404Redirect"`
	InvalidShortURLRedirect types.OmittableNullable[string] `json:"invalidShortURLRedirect"`
}

type domainRedirectsOutput struct {
	Body domainRedirects
}

func RegisterDomains(api huma.API, repo *domain.Repository) {
	huma.Register(api, huma.Operation{
		OperationID:        "listDomains",
		Method:             http.MethodGet,
		Path:               "/rest/v3/domains",
		SkipValidateParams: true,
	}, func(ctx context.Context, _ *struct{}) (*listDomainsOutput, error) {
		principal, ok := apikey.FromContext(ctx)
		if !ok {
			return nil, problem.MissingAuthentication()
		}
		domains, err := repo.ListVisible(ctx, principal.Scope)
		if err != nil {
			return nil, internalProblem(ctx, err)
		}
		out := &listDomainsOutput{}
		out.Body.Domains.Data = make([]domainInfo, 0, len(domains))
		for _, item := range domains {
			out.Body.Domains.Data = append(out.Body.Domains.Data, domainDTO(item))
			if item.IsDefault {
				out.Body.Domains.DefaultRedirects = redirectsDTO(item)
			}
		}
		return out, nil
	})

	huma.Register(api, huma.Operation{
		OperationID:      "updateDomainRedirects",
		Method:           http.MethodPatch,
		Path:             "/rest/v3/domains/redirects",
		SkipValidateBody: true,
	}, func(ctx context.Context, input *updateDomainRedirectsInput) (*domainRedirectsOutput, error) {
		principal, ok := apikey.FromContext(ctx)
		if !ok {
			return nil, problem.MissingAuthentication()
		}
		var body updateDomainRedirectsBody
		if p := DecodeJSONBytes(input.RawBody, &body); p != nil {
			return nil, p
		}
		if !shorturl.ValidAuthority(body.Domain) {
			return nil, problem.InvalidData("domain")
		}
		if !principal.Scope.IsAdmin {
			// Domain-scoped keys may only manage the domain they are bound to.
			if principal.Scope.DomainID == nil {
				return nil, problem.ForbiddenOperation("update domain redirects")
			}
			scoped, err := repo.Get(ctx, *principal.Scope.DomainID)
			if err != nil {
				return nil, internalProblem(ctx, err)
			}
			if !strings.EqualFold(scoped.Authority, domain.NormalizeAuthority(body.Domain)) {
				return nil, problem.ForbiddenOperation("update domain redirects")
			}
		}
		updated, err := repo.UpdateRedirects(ctx, body.Domain, domain.RedirectsPatch{
			BaseURLRedirect:            body.BaseURLRedirect.ValuePtr(),
			BaseURLRedirectSet:         body.BaseURLRedirect.Set,
			Regular404Redirect:         body.Regular404Redirect.ValuePtr(),
			Regular404RedirectSet:      body.Regular404Redirect.Set,
			InvalidShortURLRedirect:    body.InvalidShortURLRedirect.ValuePtr(),
			InvalidShortURLRedirectSet: body.InvalidShortURLRedirect.Set,
		})
		if err != nil {
			return nil, internalProblem(ctx, err)
		}
		return &domainRedirectsOutput{Body: redirectsDTO(*updated)}, nil
	})
}

func domainDTO(item domain.Domain) domainInfo {
	return domainInfo{
		Domain:    item.Authority,
		IsDefault: item.IsDefault,
		Redirects: redirectsDTO(item),
	}
}

func redirectsDTO(item domain.Domain) domainRedirects {
	return domainRedirects{
		BaseURLRedirect:         item.BaseURLRedirect,
		Regular404Redirect:      item.Regular404Redirect,
		InvalidShortURLRedirect: item.InvalidShortURLRedirect,
	}
}
