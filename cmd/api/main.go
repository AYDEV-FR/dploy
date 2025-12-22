package main

import (
	"context"
	"log"

	"github.com/AYDEV-FR/dploy/internal/auth"
	"github.com/AYDEV-FR/dploy/internal/cleanup"
	"github.com/AYDEV-FR/dploy/internal/config"
	"github.com/AYDEV-FR/dploy/internal/handlers"
	"github.com/AYDEV-FR/dploy/internal/kube"
	"github.com/gofiber/fiber/v2"
	"github.com/gofiber/fiber/v2/middleware/cors"
	"github.com/gofiber/fiber/v2/middleware/logger"
	"github.com/gofiber/fiber/v2/middleware/recover"
)

func main() {
	// Load configuration
	cfg, err := config.Load("config/environments.yaml")
	if err != nil {
		log.Fatalf("Failed to load configuration: %v", err)
	}

	log.Printf("Configuration loaded - ArgoCD namespace: %s, Project: %s", cfg.ArgoCDNamespace, cfg.ArgoCDProject)

	// Initialize Kubernetes client
	kubeClient, err := kube.NewClient(cfg)
	if err != nil {
		log.Fatalf("Failed to create Kubernetes client: %v", err)
	}

	log.Println("Kubernetes client initialized")

	// Start TTL cleanup worker
	cleanupWorker := cleanup.NewWorker(kubeClient, cfg.CleanupInterval)
	go cleanupWorker.Start(context.Background())

	// Initialize JWT validator
	jwtValidator := auth.NewJWTValidator(cfg.JWKSUrl, cfg.JWTIssuer, cfg.JWTAudience, cfg.JWTUsernameClaim)
	log.Printf("JWT validator initialized with JWKS URL: %s", cfg.JWKSUrl)

	// Initialize OAuth handler
	oauthHandler, err := auth.NewOAuthHandler(cfg)
	if err != nil {
		log.Printf("Warning: OAuth handler initialization failed: %v", err)
		log.Println("OAuth login will not be available")
	} else {
		log.Printf("OAuth handler initialized with issuer: %s", cfg.OIDCIssuer)
	}

	// Initialize handlers
	healthHandler := handlers.NewHealthHandler(kubeClient)
	envHandler := handlers.NewEnvironmentsHandler(kubeClient)
	runHandler := handlers.NewRunHandler(kubeClient, cfg)

	// Create Fiber app
	app := fiber.New(fiber.Config{
		ErrorHandler: func(c *fiber.Ctx, err error) error {
			code := fiber.StatusInternalServerError
			if e, ok := err.(*fiber.Error); ok {
				code = e.Code
			}
			return c.Status(code).JSON(fiber.Map{
				"error": err.Error(),
			})
		},
	})

	// Middleware
	app.Use(recover.New())
	app.Use(logger.New())
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

	// OAuth2/OIDC endpoints (no auth)
	if oauthHandler != nil {
		app.Get("/auth/login", oauthHandler.Login)
		app.Get("/auth/callback", oauthHandler.Callback)
		app.Get("/auth/logout", oauthHandler.Logout)
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
	log.Printf("Starting server on %s", addr)

	if err := app.Listen(addr); err != nil {
		log.Fatalf("Failed to start server: %v", err)
	}
}
