package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/danielgtaylor/huma/v2"

	"github.com/NCUnix/shlink-go/internal/api/query"
	"github.com/NCUnix/shlink-go/internal/apikey"
	"github.com/NCUnix/shlink-go/internal/problem"
	"github.com/NCUnix/shlink-go/internal/shorturl"
	"github.com/NCUnix/shlink-go/internal/types"
)

type shortCodeInput struct {
	ShortCode string `path:"shortCode"`
	Domain    string `query:"domain"`
}

type listShortURLsInput struct {
	Page                    string `query:"page"`
	ItemsPerPage            string `query:"itemsPerPage"`
	SearchTerm              string `query:"searchTerm"`
	TagsMode                string `query:"tagsMode"`
	ExcludeTagsMode         string `query:"excludeTagsMode"`
	OrderBy                 string `query:"orderBy"`
	StartDate               string `query:"startDate"`
	EndDate                 string `query:"endDate"`
	ExcludeMaxVisitsReached string `query:"excludeMaxVisitsReached"`
	ExcludePastValidUntil   string `query:"excludePastValidUntil"`
	Domain                  string `query:"domain"`
	APIKeyName              string `query:"apiKeyName"`
}

type createShortURLInput struct {
	RawBody []byte `contentType:"application/json"`
}

type shortenURLInput struct {
	LongURL string `query:"longUrl"`
	Format  string `query:"format"`
	Accept  string `header:"Accept"`
}

type shortenURLOutput struct {
	ContentType string `header:"Content-Type"`
	Body        []byte
}

type updateShortURLInput struct {
	ShortCode string `path:"shortCode"`
	Domain    string `query:"domain"`
	RawBody   []byte `contentType:"application/json"`
}

type shortURLOutput struct {
	Body shorturl.ShortURL
}

type listShortURLsOutput struct {
	Body struct {
		ShortURLs shorturl.ShortURLList `json:"shortUrls"`
	}
}

type createBody struct {
	LongURL         string   `json:"longUrl"`
	CustomSlug      *string  `json:"customSlug"`
	PathPrefix      *string  `json:"pathPrefix"`
	FindIfExists    bool     `json:"findIfExists"`
	Domain          *string  `json:"domain"`
	ShortCodeLength *int     `json:"shortCodeLength"`
	ValidSince      *string  `json:"validSince"`
	ValidUntil      *string  `json:"validUntil"`
	MaxVisits       *int     `json:"maxVisits"`
	Tags            []string `json:"tags"`
	Title           *string  `json:"title"`
	Crawlable       *bool    `json:"crawlable"`
	ForwardQuery    *bool    `json:"forwardQuery"`
}

type updateBody struct {
	LongURL      *string                             `json:"longUrl"`
	ValidSince   types.OmittableNullable[types.Time] `json:"validSince"`
	ValidUntil   types.OmittableNullable[types.Time] `json:"validUntil"`
	MaxVisits    types.OmittableNullable[int]        `json:"maxVisits"`
	Tags         *[]string                           `json:"tags"`
	Title        types.OmittableNullable[string]     `json:"title"`
	Crawlable    *bool                               `json:"crawlable"`
	ForwardQuery *bool                               `json:"forwardQuery"`
}

func RegisterShortURLs(api huma.API, svc *shorturl.Service) {
	huma.Register(api, huma.Operation{
		OperationID:        "createShortUrl",
		Method:             http.MethodPost,
		Path:               "/rest/v3/short-urls",
		SkipValidateBody:   true,
		SkipValidateParams: true,
	}, func(ctx context.Context, input *createShortURLInput) (*shortURLOutput, error) {
		principal, ok := apikey.FromContext(ctx)
		if !ok {
			return nil, problem.MissingAuthentication()
		}
		var body createBody
		if p := DecodeJSONBytes(input.RawBody, &body); p != nil {
			return nil, p
		}
		validSince, p := parseOptionalTime(body.ValidSince, "validSince")
		if p != nil {
			return nil, p
		}
		validUntil, p := parseOptionalTime(body.ValidUntil, "validUntil")
		if p != nil {
			return nil, p
		}
		created, err := svc.Create(ctx, principal.Scope, &principal.APIKeyID, shorturl.CreateParams{
			LongURL:         body.LongURL,
			CustomSlug:      body.CustomSlug,
			PathPrefix:      body.PathPrefix,
			FindIfExists:    body.FindIfExists,
			Domain:          body.Domain,
			ShortCodeLength: body.ShortCodeLength,
			ValidSince:      validSince,
			ValidUntil:      validUntil,
			MaxVisits:       body.MaxVisits,
			Tags:            body.Tags,
			Title:           body.Title,
			Crawlable:       body.Crawlable,
			ForwardQuery:    body.ForwardQuery,
		})
		if err != nil {
			return nil, mapShortURLError(ctx, err, "")
		}
		return &shortURLOutput{Body: *created}, nil
	})

	huma.Register(api, huma.Operation{
		OperationID:        "shortenUrl",
		Method:             http.MethodGet,
		Path:               "/rest/v3/short-urls/shorten",
		SkipValidateParams: true,
		Responses: map[string]*huma.Response{
			"200": {
				Description: "The result of parsing the long URL",
				Content: map[string]*huma.MediaType{
					"application/json": {},
					"text/plain":       {},
				},
			},
		},
	}, func(ctx context.Context, input *shortenURLInput) (*shortenURLOutput, error) {
		principal, ok := apikey.FromContext(ctx)
		if !ok {
			return nil, problem.MissingAuthentication()
		}
		created, err := svc.Create(ctx, principal.Scope, &principal.APIKeyID, shorturl.CreateParams{
			LongURL: input.LongURL,
		})
		if err != nil {
			return nil, mapShortURLError(ctx, err, "")
		}

		format := strings.ToLower(strings.TrimSpace(input.Format))
		if format == "" {
			accept := strings.ToLower(strings.TrimSpace(strings.Split(input.Accept, ",")[0]))
			if strings.Contains(accept, "text/plain") {
				format = "txt"
			}
		}
		if format == "txt" {
			return &shortenURLOutput{ContentType: "text/plain", Body: []byte(created.ShortURL)}, nil
		}

		body, err := json.Marshal(created)
		if err != nil {
			return nil, internalProblem(ctx, err)
		}
		return &shortenURLOutput{ContentType: "application/json", Body: body}, nil
	})

	huma.Register(api, huma.Operation{
		OperationID:        "listShortUrls",
		Method:             http.MethodGet,
		Path:               "/rest/v3/short-urls",
		SkipValidateParams: true,
	}, func(ctx context.Context, input *listShortURLsInput) (*listShortURLsOutput, error) {
		principal, ok := apikey.FromContext(ctx)
		if !ok {
			return nil, problem.MissingAuthentication()
		}
		params, p := parseListParams(ctx, input)
		if p != nil {
			return nil, p
		}
		result, err := svc.List(ctx, principal.Scope, *params)
		if err != nil {
			return nil, mapShortURLError(ctx, err, "")
		}
		out := &listShortURLsOutput{}
		out.Body.ShortURLs = *result
		return out, nil
	})

	huma.Register(api, huma.Operation{
		OperationID:        "getShortUrl",
		Method:             http.MethodGet,
		Path:               "/rest/v3/short-urls/{shortCode}",
		SkipValidateParams: true,
	}, func(ctx context.Context, input *shortCodeInput) (*shortURLOutput, error) {
		principal, ok := apikey.FromContext(ctx)
		if !ok {
			return nil, problem.MissingAuthentication()
		}
		result, err := svc.Get(ctx, principal.Scope, input.ShortCode, stringPtr(input.Domain))
		if err != nil {
			return nil, mapShortURLError(ctx, err, input.ShortCode)
		}
		return &shortURLOutput{Body: *result}, nil
	})

	huma.Register(api, huma.Operation{
		OperationID:        "editShortUrl",
		Method:             http.MethodPatch,
		Path:               "/rest/v3/short-urls/{shortCode}",
		SkipValidateBody:   true,
		SkipValidateParams: true,
	}, func(ctx context.Context, input *updateShortURLInput) (*shortURLOutput, error) {
		principal, ok := apikey.FromContext(ctx)
		if !ok {
			return nil, problem.MissingAuthentication()
		}
		var body updateBody
		if p := DecodeJSONBytes(input.RawBody, &body); p != nil {
			return nil, p
		}
		result, err := svc.Update(ctx, principal.Scope, input.ShortCode, stringPtr(input.Domain), shorturl.UpdateParams{
			LongURL:      body.LongURL,
			ValidSince:   body.ValidSince,
			ValidUntil:   body.ValidUntil,
			MaxVisits:    body.MaxVisits,
			Tags:         body.Tags,
			Title:        body.Title,
			Crawlable:    body.Crawlable,
			ForwardQuery: body.ForwardQuery,
		})
		if err != nil {
			return nil, mapShortURLError(ctx, err, input.ShortCode)
		}
		return &shortURLOutput{Body: *result}, nil
	})

	huma.Register(api, huma.Operation{
		OperationID:        "deleteShortUrl",
		Method:             http.MethodDelete,
		Path:               "/rest/v3/short-urls/{shortCode}",
		SkipValidateParams: true,
		DefaultStatus:      http.StatusNoContent,
	}, func(ctx context.Context, input *shortCodeInput) (*struct{}, error) {
		principal, ok := apikey.FromContext(ctx)
		if !ok {
			return nil, problem.MissingAuthentication()
		}
		if err := svc.Delete(ctx, principal.Scope, input.ShortCode, stringPtr(input.Domain)); err != nil {
			return nil, mapShortURLError(ctx, err, input.ShortCode)
		}
		return &struct{}{}, nil
	})
}

func parseListParams(ctx context.Context, input *listShortURLsInput) (*shorturl.ListParams, *problem.Problem) {
	invalid := make([]string, 0)
	page := 1
	if input.Page != "" {
		parsed, err := strconv.Atoi(input.Page)
		if err != nil || parsed <= 0 {
			invalid = append(invalid, "page")
		} else {
			page = parsed
		}
	}
	itemsPerPage := 10
	if input.ItemsPerPage != "" {
		parsed, err := strconv.Atoi(input.ItemsPerPage)
		if err != nil || parsed <= 0 {
			invalid = append(invalid, "itemsPerPage")
		} else {
			itemsPerPage = parsed
		}
	}
	startDate, p := parseOptionalTime(stringPtr(input.StartDate), "startDate")
	if p != nil {
		invalid = append(invalid, "startDate")
	}
	endDate, p := parseOptionalTime(stringPtr(input.EndDate), "endDate")
	if p != nil {
		invalid = append(invalid, "endDate")
	}
	if len(invalid) > 0 {
		return nil, problem.InvalidData(invalid...)
	}
	excludeMax, _ := strconv.ParseBool(input.ExcludeMaxVisitsReached)
	excludePast, _ := strconv.ParseBool(input.ExcludePastValidUntil)
	return &shorturl.ListParams{
		Page:                    page,
		ItemsPerPage:            itemsPerPage,
		SearchTerm:              input.SearchTerm,
		Tags:                    query.FromContext(ctx, "tags"),
		TagsMode:                input.TagsMode,
		ExcludeTags:             query.FromContext(ctx, "excludeTags"),
		ExcludeTagsMode:         input.ExcludeTagsMode,
		OrderBy:                 input.OrderBy,
		StartDate:               startDate,
		EndDate:                 endDate,
		ExcludeMaxVisitsReached: excludeMax,
		ExcludePastValidUntil:   excludePast,
		Domain:                  stringPtr(input.Domain),
		APIKeyName:              input.APIKeyName,
	}, nil
}

func parseOptionalTime(value *string, element string) (*time.Time, *problem.Problem) {
	if value == nil || strings.TrimSpace(*value) == "" {
		return nil, nil
	}
	parsed, err := time.Parse(time.RFC3339Nano, *value)
	if err != nil {
		return nil, problem.InvalidData(element)
	}
	return &parsed, nil
}

func mapShortURLError(ctx context.Context, err error, shortCode string) *problem.Problem {
	var validation *shorturl.ValidationError
	if errors.As(err, &validation) {
		return problem.InvalidData(validation.Elements...)
	}
	var notFound *shorturl.NotFoundError
	if errors.As(err, &notFound) {
		return problem.ShortURLNotFound(notFound.ShortCode, notFound.Domain)
	}
	var conflict *shorturl.ConflictError
	if errors.As(err, &conflict) {
		detail := fmt.Sprintf("Provided slug %q is already in use.", conflict.Slug)
		if conflict.Domain != "" {
			detail = fmt.Sprintf("Provided slug %q is already in use for domain %q.", conflict.Slug, conflict.Domain)
		}
		p := problem.New(http.StatusBadRequest, problem.TypeNonUniqueSlug, "Invalid custom slug", detail)
		p.CustomSlug = conflict.Slug
		if conflict.Domain != "" {
			p.Domain = conflict.Domain
		}
		return p
	}
	return internalProblem(ctx, err)
}

func stringPtr(value string) *string {
	if value == "" {
		return nil
	}
	return &value
}
