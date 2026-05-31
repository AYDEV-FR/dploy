package main

import (
	"errors"

	"github.com/AYDEV-FR/dploy/internal/auth"
	"github.com/AYDEV-FR/dploy/internal/config"
	"github.com/AYDEV-FR/dploy/internal/handlers"
	"github.com/AYDEV-FR/dploy/internal/kube"
	"github.com/AYDEV-FR/dploy/internal/logger"
	"github.com/AYDEV-FR/dploy/internal/models"
	"github.com/gofiber/fiber/v2"
	"github.com/gofiber/fiber/v2/middleware/cors"
	fiberlogger "github.com/gofiber/fiber/v2/middleware/logger"
	"github.com/gofiber/fiber/v2/middleware/recover"
)

func main() {
	// Load configuration
	cfg, err := config.Load()
	if err != nil {
		panic("Failed to load configuration: " + err.Error())
	}

	// Initialize logger with debug mode
	logger.Init(cfg.Debug)
	defer logger.Sync()

	logger.Info("Configuration loaded", "namespace", cfg.Namespace)

	// Initialize Kubernetes client
	kubeClient, err := kube.NewClient(cfg)
	if err != nil {
		logger.Fatal("Failed to create Kubernetes client", "error", err)
	}

	logger.Info("Kubernetes client initialized")

	// Initialize JWT validator
	jwtValidator := auth.NewJWTValidator(cfg.JWKSUrl, cfg.JWTIssuer, cfg.JWTAudience, cfg.JWTUsernameClaim)
	logger.Info("JWT validator initialized", "jwksURL", cfg.JWKSUrl)
	logger.Debug("JWT settings",
		"issuer", cfg.JWTIssuer,
		"audience", cfg.JWTAudience,
		"usernameClaim", cfg.JWTUsernameClaim,
	)

	// Initialize OIDC handler. discoverOIDCWithRetry already rides out the
	// post-startup network-identity window; if it still fails, fall over so
	// Kubernetes restarts the pod instead of running with /auth/login broken.
	oidcHandler, err := auth.NewOIDCHandler(cfg)
	if err != nil {
		logger.Fatal("OIDC handler initialization failed after retries; exiting for restart", "error", err)
	}
	logger.Info("OIDC handler initialized", "issuer", cfg.OIDCIssuer)
	logger.Debug("OIDC settings",
		"clientID", cfg.OIDCClientID,
		"redirectURL", cfg.OIDCRedirectURL,
	)

	// Initialize handlers
	healthHandler := handlers.NewHealthHandler(kubeClient)
	envHandler := handlers.NewEnvironmentsHandler(kubeClient)
	runHandler := handlers.NewRunHandler(kubeClient, cfg)
	meHandler := handlers.NewMeHandler(cfg)
	adminHandler := handlers.NewAdminHandler(kubeClient)

	// Create Fiber app
	app := fiber.New(fiber.Config{
		ErrorHandler: func(c *fiber.Ctx, err error) error {
			code := fiber.StatusInternalServerError
			var e *fiber.Error
			if errors.As(err, &e) {
				code = e.Code
			}
			return c.Status(code).JSON(fiber.Map{
				"error": err.Error(),
			})
		},
	})

	// Middleware
	app.Use(recover.New())
	app.Use(fiberlogger.New())
	app.Use(cors.New(cors.Config{
		AllowOrigins: "*",
		AllowMethods: "GET,POST,DELETE,OPTIONS",
		AllowHeaders: "Origin, Content-Type, Accept, Authorization",
	}))

	// Static files (React app)
	app.Static("/static", "./web/dist/static")

	// Health endpoints (no auth)
	app.Get("/health", healthHandler.Health)
	app.Get("/ready", healthHandler.Ready)

	// OIDC endpoints (no auth)
	if oidcHandler != nil {
		app.Get("/auth/login", oidcHandler.Login)
		app.Get("/auth/callback", oidcHandler.Callback)
		app.Get("/auth/logout", oidcHandler.Logout)
	}

	// Public UI feature flags — the web UI fetches this at bootstrap to hide
	// nav links and skip disabled routes. No auth (the UI hasn't logged in yet).
	app.Get("/api/ui-config", func(c *fiber.Ctx) error {
		return c.JSON(models.UIConfigResponse{
			CatalogEnabled:   cfg.CatalogEnabled,
			InstancesEnabled: cfg.InstancesListEnabled,
			ManagerEnabled:   cfg.ManagerEnabled,
		})
	})

	// Public API endpoints — catalog listing is gated by CATALOG_ENABLED so a
	// "run-only" deployment doesn't expose the full template list. The route is
	// always registered to return a clean JSON 404 when off (otherwise an
	// unmatched /api/... path falls through to either the auth middleware or
	// the SPA catch-all, neither of which makes sense for an API client).
	app.Get("/api/environments/available", func(c *fiber.Ctx) error {
		if !cfg.CatalogEnabled {
			return c.Status(fiber.StatusNotFound).JSON(models.ErrorResponse{Error: "catalog disabled"})
		}
		return envHandler.ListAvailable(c)
	})

	// Public route - /run/{env} serves React SPA (handles auth in browser)
	app.Get("/run/:env", func(c *fiber.Ctx) error {
		return c.SendFile("./web/dist/index.html")
	})

	// Protected API endpoints
	api := app.Group("/api", auth.Middleware(jwtValidator))
	// User's instance list is gated by INSTANCES_LIST_ENABLED for the same reason.
	api.Get("/environments", func(c *fiber.Ctx) error {
		if !cfg.InstancesListEnabled {
			return c.Status(fiber.StatusNotFound).JSON(models.ErrorResponse{Error: "instances list disabled"})
		}
		return envHandler.ListUserEnvironments(c)
	})
	api.Get("/run/:env", runHandler.CreateEnvironment)
	api.Get("/run/:env/status", runHandler.GetStatus)
	api.Post("/run/:env/extend", runHandler.ExtendTTL)
	api.Delete("/run/:env", runHandler.DeleteEnvironment)
	// Self-discovery: lets the UI know who it's logged in as and whether to
	// surface admin affordances. Always available behind auth.
	api.Get("/me", meHandler.Get)

	// Admin endpoints — gated by MANAGER_ENABLED + the admin claim/value pair.
	// 404 when disabled, 403 to non-admin requesters.
	api.Get("/admin/instances", func(c *fiber.Ctx) error {
		if !cfg.ManagerEnabled {
			return c.Status(fiber.StatusNotFound).JSON(models.ErrorResponse{Error: "manager disabled"})
		}
		return c.Next()
	}, auth.AdminMiddleware(cfg), adminHandler.ListAllInstances)

	// SPA fallback - serve React app for all unmatched routes
	app.Get("/*", func(c *fiber.Ctx) error {
		return c.SendFile("./web/dist/index.html")
	})

	// Start server
	addr := cfg.ServerHost + ":" + cfg.ServerPort
	logger.Info("Starting server", "addr", addr)
	logger.Debug("Server configuration",
		"host", cfg.ServerHost,
		"port", cfg.ServerPort,
		"namespace", cfg.Namespace,
	)

	if err := app.Listen(addr); err != nil {
		logger.Fatal("Failed to start server", "error", err)
	}
}
