package api

import (
	"context"
	"errors"
	"net/http"
	"strconv"

	"github.com/danielgtaylor/huma/v2"

	"github.com/NCUnix/shlink-go/internal/api/query"
	"github.com/NCUnix/shlink-go/internal/apikey"
	"github.com/NCUnix/shlink-go/internal/problem"
	"github.com/NCUnix/shlink-go/internal/tag"
)

type listTagsInput struct {
	Page         string `query:"page"`
	ItemsPerPage string `query:"itemsPerPage"`
	SearchTerm   string `query:"searchTerm"`
	OrderBy      string `query:"orderBy"`
}

type listTagsOutput struct {
	Body struct {
		Tags tag.TagList `json:"tags"`
	}
}

type tagStatsOutput struct {
	Body struct {
		Tags tag.StatsResult `json:"tags"`
	}
}

type renameTagInput struct {
	RawBody []byte `contentType:"application/json"`
}

type renameTagBody struct {
	OldName string `json:"oldName"`
	NewName string `json:"newName"`
}

type deleteTagsInput struct{}

func RegisterTags(api huma.API, svc *tag.Service) {
	huma.Register(api, huma.Operation{
		OperationID:        "listTags",
		Method:             http.MethodGet,
		Path:               "/rest/v3/tags",
		SkipValidateParams: true,
	}, func(ctx context.Context, input *listTagsInput) (*listTagsOutput, error) {
		principal, ok := apikey.FromContext(ctx)
		if !ok {
			return nil, problem.MissingAuthentication()
		}
		params, p := parseTagListParams(input)
		if p != nil {
			return nil, p
		}
		result, err := svc.List(ctx, principal.Scope, *params)
		if err != nil {
			return nil, mapTagError(ctx, err)
		}
		out := &listTagsOutput{}
		out.Body.Tags = *result
		return out, nil
	})

	huma.Register(api, huma.Operation{
		OperationID:        "tagsStats",
		Method:             http.MethodGet,
		Path:               "/rest/v3/tags/stats",
		SkipValidateParams: true,
	}, func(ctx context.Context, input *listTagsInput) (*tagStatsOutput, error) {
		principal, ok := apikey.FromContext(ctx)
		if !ok {
			return nil, problem.MissingAuthentication()
		}
		params, p := parseTagListParams(input)
		if p != nil {
			return nil, p
		}
		result, err := svc.Stats(ctx, principal.Scope, *params)
		if err != nil {
			return nil, mapTagError(ctx, err)
		}
		out := &tagStatsOutput{}
		out.Body.Tags = *result
		return out, nil
	})

	huma.Register(api, huma.Operation{
		OperationID:      "renameTag",
		Method:           http.MethodPut,
		Path:             "/rest/v3/tags",
		SkipValidateBody: true,
		DefaultStatus:    http.StatusNoContent,
	}, func(ctx context.Context, input *renameTagInput) (*struct{}, error) {
		principal, ok := apikey.FromContext(ctx)
		if !ok {
			return nil, problem.MissingAuthentication()
		}
		var body renameTagBody
		if p := DecodeJSONBytes(input.RawBody, &body); p != nil {
			return nil, p
		}
		if body.OldName == "" || body.NewName == "" {
			return nil, problem.InvalidData("oldName", "newName")
		}
		if err := svc.Rename(ctx, principal.Scope, body.OldName, body.NewName); err != nil {
			return nil, mapTagError(ctx, err)
		}
		return &struct{}{}, nil
	})

	huma.Register(api, huma.Operation{
		OperationID:        "deleteTags",
		Method:             http.MethodDelete,
		Path:               "/rest/v3/tags",
		SkipValidateParams: true,
		DefaultStatus:      http.StatusNoContent,
	}, func(ctx context.Context, _ *deleteTagsInput) (*struct{}, error) {
		principal, ok := apikey.FromContext(ctx)
		if !ok {
			return nil, problem.MissingAuthentication()
		}
		if err := svc.Delete(ctx, principal.Scope, query.FromContext(ctx, "tags")); err != nil {
			return nil, mapTagError(ctx, err)
		}
		return &struct{}{}, nil
	})
}

func parseTagListParams(input *listTagsInput) (*tag.ListParams, *problem.Problem) {
	params := &tag.ListParams{Page: 1, SearchTerm: input.SearchTerm, OrderBy: input.OrderBy}
	invalid := make([]string, 0)
	if input.Page != "" {
		value, err := strconv.Atoi(input.Page)
		if err != nil || value <= 0 {
			invalid = append(invalid, "page")
		} else {
			params.Page = value
		}
	}
	if input.ItemsPerPage != "" {
		value, err := strconv.Atoi(input.ItemsPerPage)
		if err != nil || value <= 0 {
			invalid = append(invalid, "itemsPerPage")
		} else {
			params.ItemsPerPage = value
		}
	}
	if len(invalid) > 0 {
		return nil, problem.InvalidData(invalid...)
	}
	return params, nil
}

func mapTagError(ctx context.Context, err error) *problem.Problem {
	var validation *tag.ValidationError
	if errors.As(err, &validation) {
		return problem.InvalidData(validation.Elements...)
	}
	var forbidden *tag.ForbiddenError
	if errors.As(err, &forbidden) {
		return problem.ForbiddenTagOperation(forbidden.Operation)
	}
	var notFound *tag.NotFoundError
	if errors.As(err, &notFound) {
		return problem.TagNotFound(notFound.Name)
	}
	var conflict *tag.ConflictError
	if errors.As(err, &conflict) {
		return problem.TagConflict(conflict.OldName, conflict.NewName)
	}
	return internalProblem(ctx, err)
}
