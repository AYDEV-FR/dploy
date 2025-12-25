package main

import (
	"context"
	"errors"

	"github.com/AYDEV-FR/dploy/internal/auth"
	"github.com/AYDEV-FR/dploy/internal/cleanup"
	"github.com/AYDEV-FR/dploy/internal/config"
	"github.com/AYDEV-FR/dploy/internal/handlers"
	"github.com/AYDEV-FR/dploy/internal/kube"
	"github.com/AYDEV-FR/dploy/internal/logger"
	"github.com/gofiber/fiber/v2"
	"github.com/gofiber/fiber/v2/middleware/cors"
	fiberlogger "github.com/gofiber/fiber/v2/middleware/logger"
	"github.com/gofiber/fiber/v2/middleware/recover"
)

func main() {
	// Load configuration
	cfg, err := config.Load("config/environments.yaml")
	if err != nil {
		panic("Failed to load configuration: " + err.Error())
	}

	// Initialize logger with debug mode
	logger.Init(cfg.Debug)
	defer logger.Sync()

	logger.Info("Configuration loaded",
		"argocdNamespace", cfg.ArgoCDNamespace,
		"argocdProject", cfg.ArgoCDProject,
	)

	// Initialize Kubernetes client
	kubeClient, err := kube.NewClient(cfg)
	if err != nil {
		logger.Fatal("Failed to create Kubernetes client", "error", err)
	}

	logger.Info("Kubernetes client initialized")

	// Start TTL cleanup worker
	cleanupWorker := cleanup.NewWorker(kubeClient, cfg.CleanupInterval)
	go cleanupWorker.Start(context.Background())

	// Initialize JWT validator
	jwtValidator := auth.NewJWTValidator(cfg.JWKSUrl, cfg.JWTIssuer, cfg.JWTAudience, cfg.JWTUsernameClaim)
	logger.Info("JWT validator initialized", "jwksURL", cfg.JWKSUrl)
	logger.Debug("JWT settings",
		"issuer", cfg.JWTIssuer,
		"audience", cfg.JWTAudience,
		"usernameClaim", cfg.JWTUsernameClaim,
	)

	// Initialize OIDC handler
	oidcHandler, err := auth.NewOIDCHandler(cfg)
	if err != nil {
		logger.Warn("OIDC handler initialization failed", "error", err)
		logger.Info("OIDC login will not be available")
	} else {
		logger.Info("OIDC handler initialized", "issuer", cfg.OIDCIssuer)
		logger.Debug("OIDC settings",
			"clientID", cfg.OIDCClientID,
			"redirectURL", cfg.OIDCRedirectURL,
		)
	}

	// Initialize handlers
	healthHandler := handlers.NewHealthHandler(kubeClient)
	envHandler := handlers.NewEnvironmentsHandler(kubeClient)
	runHandler := handlers.NewRunHandler(kubeClient, cfg)

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

	// Static files (web UI)
	app.Static("/static", "./web")
	app.Get("/", func(c *fiber.Ctx) error {
		return c.SendFile("./web/index.html")
	})

	// Health endpoints (no auth)
	app.Get("/health", healthHandler.Health)
	app.Get("/ready", healthHandler.Ready)

	// OIDC endpoints (no auth)
	if oidcHandler != nil {
		app.Get("/auth/login", oidcHandler.Login)
		app.Get("/auth/callback", oidcHandler.Callback)
		app.Get("/auth/logout", oidcHandler.Logout)
	}

	// Public API endpoints
	app.Get("/api/environments/available", envHandler.ListAvailable)

	// Public route - /run/{env} serves HTML page (handles auth in browser)
	app.Get("/run/:env", func(c *fiber.Ctx) error {
		return c.SendFile("./web/run.html")
	})

	// Protected API endpoints
	api := app.Group("/api", auth.Middleware(jwtValidator))
	api.Get("/environments", envHandler.ListUserEnvironments)
	api.Get("/run/:env", runHandler.CreateEnvironment)
	api.Get("/run/:env/status", runHandler.GetStatus)
	api.Post("/run/:env/extend", runHandler.ExtendTTL)
	api.Delete("/run/:env", runHandler.DeleteEnvironment)

	// Start server
	addr := cfg.ServerHost + ":" + cfg.ServerPort
	logger.Info("Starting server", "addr", addr)
	logger.Debug("Server configuration",
		"host", cfg.ServerHost,
		"port", cfg.ServerPort,
		"baseDomain", cfg.BaseDomain,
	)

	if err := app.Listen(addr); err != nil {
		logger.Fatal("Failed to start server", "error", err)
	}
}
