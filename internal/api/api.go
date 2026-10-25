package api

import (
	"github.com/danielgtaylor/huma/v2"
	"github.com/danielgtaylor/huma/v2/adapters/humagin"
	"github.com/gin-gonic/gin"
)

// ShlinkVersion is the upstream Shlink API version this server emulates. The web
// client reads it to decide which features to show, so it is the contract
// version, not the version of this Go binary.
const ShlinkVersion = "5.1.7"

func NewHuma(engine *gin.Engine) huma.API {
	cfg := huma.DefaultConfig("Shlink Go API", ShlinkVersion)
	cfg.OpenAPIPath = ""
	cfg.DocsPath = ""
	cfg.SchemasPath = ""
	// Shlink v3 responses do not include Huma's $schema field or Link header.
	cfg.CreateHooks = nil
	return humagin.New(engine, cfg)
}
