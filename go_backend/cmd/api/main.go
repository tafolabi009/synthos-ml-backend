package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/gofiber/fiber/v2"
	"github.com/gofiber/fiber/v2/middleware/compress"
	"github.com/gofiber/fiber/v2/middleware/cors"
	"github.com/gofiber/fiber/v2/middleware/limiter"
	"github.com/gofiber/fiber/v2/middleware/logger"
	"github.com/gofiber/fiber/v2/middleware/recover"
	"github.com/gofiber/fiber/v2/middleware/requestid"

	"github.com/tafolabi009/backend/go_backend/internal/auth"
	"github.com/tafolabi009/backend/go_backend/internal/handlers"
	"github.com/tafolabi009/backend/go_backend/internal/middleware"
	configpkg "github.com/tafolabi009/backend/go_backend/pkg/config"
	"github.com/tafolabi009/backend/go_backend/pkg/database"
	"github.com/tafolabi009/backend/go_backend/pkg/grpcclient"
	"github.com/tafolabi009/backend/go_backend/pkg/monitoring"
	"github.com/tafolabi009/backend/go_backend/pkg/tracing"
)

func main() {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Load configuration
	cfg, err := configpkg.LoadProduction(ctx)
	if err != nil {
		log.Fatalf("Failed to load configuration: %v", err)
	}
	defer cfg.Close()

	log.Printf("Starting Synthos API Gateway in %s mode", cfg.Environment)

	// Initialize JWT with secret from config
	auth.InitJWT(cfg.JWTSecret)

	// Initialize database
	if err := database.Init(cfg.DatabaseURL); err != nil {
		log.Fatalf("Failed to initialize database: %v", err)
	}
	defer database.Close()

	// Initialize gRPC clients for ML services
	grpcCfg := grpcclient.DefaultProductionConfig()
	grpcCfg.ValidationAddr = cfg.ValidationServiceAddr
	grpcCfg.CollapseAddr = cfg.CollapseServiceAddr

	grpcClients, err := grpcclient.NewProductionClients(ctx, grpcCfg)
	if err != nil {
		log.Printf("⚠️ Failed to initialize gRPC clients (ML services may be unavailable): %v", err)
	} else {
		defer grpcClients.Close()
	}

	// Initialize Jaeger tracing (if enabled)
	if cfg.EnableTracing {
		tracer, closer, err := tracing.InitJaeger("synthos-api-gateway", cfg.JaegerEndpoint)
		if err != nil {
			log.Printf("Warning: Failed to initialize Jaeger tracer: %v", err)
		} else {
			defer closer.Close()
			log.Printf("✅ Jaeger tracer initialized: %s", cfg.JaegerEndpoint)
			_ = tracer
		}
	}

	// Create Fiber app with production settings
	app := fiber.New(fiber.Config{
		ReadTimeout:           30 * time.Second,
		WriteTimeout:          30 * time.Second,
		IdleTimeout:           120 * time.Second,
		BodyLimit:             100 * 1024 * 1024, // 100MB
		ErrorHandler:          productionErrorHandler,
		AppName:               "Synthos API Gateway v1.0.0",
		DisableStartupMessage: cfg.Environment == "production",
		EnablePrintRoutes:     cfg.Environment != "production",
		Prefork:               false, // Disable prefork for Kubernetes/ECS compatibility
	})

	// Request ID middleware
	app.Use(requestid.New())

	// Recover from panics
	app.Use(recover.New(recover.Config{
		EnableStackTrace: cfg.Environment != "production",
	}))

	// Compression
	app.Use(compress.New(compress.Config{
		Level: compress.LevelBestSpeed,
	}))

	// Logger
	app.Use(logger.New(logger.Config{
		Format:     "${time} ${status} - ${method} ${path} ${latency}\n",
		TimeFormat: "2006-01-02 15:04:05",
		TimeZone:   "UTC",
	}))

	// CORS configuration - allow all frontend headers
	app.Use(cors.New(cors.Config{
		AllowOrigins:     joinOrigins(cfg.AllowedOrigins),
		AllowMethods:     "GET,POST,PUT,DELETE,OPTIONS,PATCH",
		AllowHeaders:     "Content-Type,Authorization,X-Request-ID,X-Trace-ID,X-Requested-With,Accept,Origin,Cache-Control",
		AllowCredentials: true,
		ExposeHeaders:    "X-Request-ID,X-Trace-ID,Content-Length",
		MaxAge:           86400,
	}))

	// Rate limiting
	app.Use(limiter.New(limiter.Config{
		Max:               cfg.RateLimitRPS,
		Expiration:        time.Second,
		LimiterMiddleware: limiter.SlidingWindow{},
		KeyGenerator: func(c *fiber.Ctx) string {
			userID := c.Locals("user_id")
			if userID != nil {
				return fmt.Sprintf("%s:%s", c.IP(), userID)
			}
			return c.IP()
		},
		LimitReached: func(c *fiber.Ctx) error {
			return c.Status(fiber.StatusTooManyRequests).JSON(fiber.Map{
				"error": fiber.Map{
					"code":    "RATE_LIMIT_EXCEEDED",
					"message": "Too many requests, please try again later",
				},
			})
		},
	}))

	// Prometheus metrics middleware
	if cfg.EnableMetrics {
		app.Use(monitoring.PrometheusMiddleware())
		log.Println("✅ Prometheus metrics enabled at /metrics")
	}

	// Jaeger tracing middleware
	if cfg.EnableTracing {
		app.Use(tracing.TracingMiddleware())
		log.Println("✅ Jaeger distributed tracing enabled")
	}

	// Health check endpoints
	app.Get("/health", healthHandler(grpcClients))
	app.Get("/health/live", func(c *fiber.Ctx) error {
		return c.JSON(fiber.Map{"status": "alive"})
	})
	app.Get("/health/ready", readinessHandler(grpcClients))

	// Prometheus metrics endpoint
	if cfg.EnableMetrics {
		app.Get("/metrics", monitoring.MetricsHandler())
	}

	// API v1 routes
	v1 := app.Group("/api/v1")
	{
		// Auth routes (public)
		authRoutes := v1.Group("/auth")
		{
			authRoutes.Post("/register", handlers.RegisterFiber)
			authRoutes.Post("/login", handlers.LoginFiber)
			authRoutes.Post("/logout", middleware.AuthRequiredFiber(), handlers.LogoutFiber)
			authRoutes.Post("/refresh", handlers.RefreshTokenFiber)
			authRoutes.Post("/forgot-password", handlers.ForgotPasswordFiber)
			authRoutes.Post("/reset-password", handlers.ResetPasswordFiber)
		}

		// Protected auth routes
		authProtected := v1.Group("/auth", middleware.AuthRequiredFiber())
		{
			authProtected.Get("/me", handlers.GetMeFiber)
			authProtected.Post("/change-password", handlers.ChangePasswordFiber)

			// 2FA routes
			authProtected.Post("/2fa/setup", handlers.TwoFactorSetupFiber)
			authProtected.Post("/2fa/verify", handlers.TwoFactorVerifyFiber)
			authProtected.Post("/2fa/disable", handlers.TwoFactorDisableFiber)
		}

		// API Keys (protected)
		apiKeys := v1.Group("/api-keys", middleware.AuthRequiredFiber())
		{
			apiKeys.Post("", handlers.CreateAPIKeyFiber)
			apiKeys.Get("", handlers.ListAPIKeysFiber)
			apiKeys.Delete("/:id", handlers.DeleteAPIKeyFiber)
		}

		// Notifications (protected)
		notifications := v1.Group("/notifications", middleware.AuthRequiredFiber())
		{
			notifications.Get("", handlers.GetNotificationsFiber)
			notifications.Post("/read", handlers.MarkNotificationsReadFiber)
		}

		// Protected routes (require authentication)
		protected := v1.Group("", middleware.AuthRequiredFiber())
		{
			// Dataset management
			datasets := protected.Group("/datasets")
			{
				datasets.Post("/upload", middleware.RequireScopes("write:datasets"), handlers.InitiateUploadFiber)
				datasets.Post("/:id/complete", middleware.RequireScopes("write:datasets"), handlers.CompleteUploadFiber)
				datasets.Get("", middleware.RequireScopes("read:datasets"), handlers.ListDatasetsFiber)
				datasets.Get("/:id", middleware.RequireScopes("read:datasets"), handlers.GetDatasetFiber)
				datasets.Delete("/:id", middleware.RequireScopes("write:datasets"), handlers.DeleteDatasetFiber)
			}

			// Validation jobs
			validations := protected.Group("/validations")
			{
				validations.Post("/create", middleware.RequireScopes("write:validations"), handlers.CreateValidationFiber)
				validations.Get("", middleware.RequireScopes("read:validations"), handlers.ListValidationsFiber)
				validations.Get("/:id", middleware.RequireScopes("read:validations"), handlers.GetValidationFiber)
				validations.Get("/:id/report", middleware.RequireScopes("read:validations"), handlers.GetValidationReportFiber)
				validations.Get("/:id/certificate", middleware.RequireScopes("read:validations"), handlers.GetValidationCertificateFiber)
				validations.Get("/:id/collapse-details", middleware.RequireScopes("read:validations"), handlers.GetCollapseDetailsFiber)
				validations.Get("/:id/recommendations", middleware.RequireScopes("read:validations"), handlers.GetRecommendationsFiber)
			}

			// Warranty management
			warranties := protected.Group("/warranties")
			{
				warranties.Post("/:validation_id/request", middleware.RequireScopes("write:warranties"), handlers.RequestWarrantyFiber)
				warranties.Get("", middleware.RequireScopes("read:warranties"), handlers.ListWarrantiesFiber)
				warranties.Get("/:id", middleware.RequireScopes("read:warranties"), handlers.GetWarrantyFiber)
				warranties.Post("/:id/claim", middleware.RequireScopes("write:warranties"), handlers.FileWarrantyClaimFiber)
			}

			// Analytics
			analytics := protected.Group("/analytics")
			{
				analytics.Get("/usage", middleware.RequireScopes("read:analytics"), handlers.GetUsageAnalyticsFiber)
				analytics.Get("/validation-history", middleware.RequireScopes("read:analytics"), handlers.GetValidationHistoryFiber)
			}
		}
	}

	// Graceful shutdown
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)

	go func() {
		<-quit
		log.Println("Shutting down server...")

		shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer shutdownCancel()

		if err := app.ShutdownWithContext(shutdownCtx); err != nil {
			log.Fatal("Server forced to shutdown:", err)
		}

		log.Println("Server exited")
	}()

	// Start server
	addr := fmt.Sprintf(":%d", cfg.Port)
	log.Printf("🚀 Starting Synthos API Gateway on %s", addr)

	if cfg.TLSEnabled && cfg.TLSCertFile != "" && cfg.TLSKeyFile != "" {
		if err := app.ListenTLS(addr, cfg.TLSCertFile, cfg.TLSKeyFile); err != nil {
			log.Fatal("Server failed to start:", err)
		}
	} else {
		if err := app.Listen(addr); err != nil {
			log.Fatal("Server failed to start:", err)
		}
	}
}

// healthHandler returns comprehensive health status
func healthHandler(grpcClients *grpcclient.ProductionClients) fiber.Handler {
	return func(c *fiber.Ctx) error {
		health := fiber.Map{
			"status":  "healthy",
			"service": "synthos-api-gateway",
			"version": "1.0.0",
			"time":    time.Now().Unix(),
		}

		// Check gRPC services health
		if grpcClients != nil {
			health["services"] = fiber.Map{
				"validation": grpcClients.GetValidationHealth(),
				"collapse":   grpcClients.GetCollapseHealth(),
			}
		}

		// Check database health
		if database.IsHealthy() {
			health["database"] = "healthy"
		} else {
			health["database"] = "unhealthy"
			health["status"] = "degraded"
		}

		return c.JSON(health)
	}
}

// readinessHandler checks if the service is ready to accept traffic
func readinessHandler(grpcClients *grpcclient.ProductionClients) fiber.Handler {
	return func(c *fiber.Ctx) error {
		if !database.IsHealthy() {
			return c.Status(fiber.StatusServiceUnavailable).JSON(fiber.Map{
				"ready":  false,
				"reason": "database not ready",
			})
		}

		return c.JSON(fiber.Map{
			"ready": true,
		})
	}
}

// productionErrorHandler handles errors in production-safe manner
func productionErrorHandler(c *fiber.Ctx, err error) error {
	code := fiber.StatusInternalServerError
	errorCode := "INTERNAL_SERVER_ERROR"
	message := "An unexpected error occurred"

	if e, ok := err.(*fiber.Error); ok {
		code = e.Code
		message = e.Message

		switch code {
		case fiber.StatusBadRequest:
			errorCode = "BAD_REQUEST"
		case fiber.StatusUnauthorized:
			errorCode = "UNAUTHORIZED"
		case fiber.StatusForbidden:
			errorCode = "FORBIDDEN"
		case fiber.StatusNotFound:
			errorCode = "NOT_FOUND"
		case fiber.StatusConflict:
			errorCode = "CONFLICT"
		case fiber.StatusTooManyRequests:
			errorCode = "RATE_LIMIT_EXCEEDED"
		case fiber.StatusServiceUnavailable:
			errorCode = "SERVICE_UNAVAILABLE"
		}
	}

	// Log the error
	requestID := c.Locals("requestid")
	log.Printf("Error [%s] %d: %s (request_id: %v)", errorCode, code, err.Error(), requestID)

	return c.Status(code).JSON(fiber.Map{
		"error": fiber.Map{
			"code":       errorCode,
			"message":    message,
			"request_id": requestID,
		},
	})
}

// joinOrigins joins origins with comma for Fiber CORS
func joinOrigins(origins []string) string {
	// Production fallback - never return wildcard when AllowCredentials is true
	if len(origins) == 0 {
		return "https://www.synthos.dev,https://synthos.dev,https://app.synthos.dev"
	}
	result := ""
	for i, origin := range origins {
		if i > 0 {
			result += ","
		}
		result += origin
	}
	return result
}
