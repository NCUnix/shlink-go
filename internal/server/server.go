package server

import (
	"log/slog"
	"net/http"

	"github.com/danielgtaylor/huma/v2"
	"github.com/gin-gonic/gin"

	"github.com/NCUnix/shlink-go/internal/api"
	"github.com/NCUnix/shlink-go/internal/api/query"
	"github.com/NCUnix/shlink-go/internal/config"
	"github.com/NCUnix/shlink-go/internal/store"
)

type App struct {
	Config config.Config
	DB     *store.DB
	Logger *slog.Logger
	Engine *gin.Engine
	API    huma.API
}

func New(cfg config.Config, db *store.DB, logger *slog.Logger, auth gin.HandlerFunc) *App {
	engine := gin.New()
	engine.RedirectTrailingSlash = false
	engine.HandleMethodNotAllowed = true
	if len(cfg.TrustedProxies) == 0 {
		_ = engine.SetTrustedProxies(nil)
	} else {
		_ = engine.SetTrustedProxies(cfg.TrustedProxies)
	}

	engine.Use(requestIDMiddleware())
	engine.Use(loggerMiddleware(logger))
	engine.Use(accessLogMiddleware(logger))
	engine.Use(recoveryMiddleware(logger))
	engine.Use(corsMiddleware(cfg))
	engine.Use(implicitOptionsMiddleware())
	engine.Use(bodyLimitMiddleware(1 << 20))
	engine.Use(query.ArraysMiddleware())
	if auth != nil {
		engine.Use(auth)
	}

	app := &App{
		Config: cfg,
		DB:     db,
		Logger: logger,
		Engine: engine,
	}
	app.registerHealth()
	app.API = api.NewHuma(engine)
	return app
}

func (a *App) Handler() http.Handler {
	return a.Engine
}
