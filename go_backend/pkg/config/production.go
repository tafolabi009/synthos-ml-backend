package config

import (
	"context"
	"fmt"
	"log"
	"os"
	"strconv"
	"strings"
	"time"

	secretmanager "cloud.google.com/go/secretmanager/apiv1"
	secretmanagerpb "cloud.google.com/go/secretmanager/apiv1/secretmanagerpb"
	"github.com/tafolabi009/backend/go_backend/pkg/secrets"
)

// ProductionConfig holds all production configuration
type ProductionConfig struct {
	Environment string
	Port        int
	
	// Database
	DatabaseURL     string
	DatabaseMaxConn int
	DatabaseMinConn int
	
	// Redis
	RedisURL      string
	RedisPassword string
	RedisTLS      bool
	
	// JWT
	JWTSecret     string
	JWTExpiry     time.Duration
	RefreshExpiry time.Duration
	
	// Cloud Provider
	CloudProvider string // "gcp" or "aws"

	// GCP
	GCPProjectID    string
	GCSBucket       string
	GCPRegion       string

	// AWS (legacy)
	AWSRegion          string
	S3Bucket           string
	S3Endpoint         string
	AWSAccessKeyID     string
	AWSSecretAccessKey string
	SecretsManagerID   string
	
	// gRPC Services
	OrchestratorAddr      string
	ValidationServiceAddr string
	CollapseServiceAddr   string
	DataServiceAddr       string
	
	// CORS
	AllowedOrigins []string
	
	// Monitoring
	EnableMetrics  bool
	EnableTracing  bool
	JaegerEndpoint string
	
	// Rate Limiting
	RateLimitRPS   int
	RateLimitBurst int
	
	// TLS
	TLSEnabled  bool
	TLSCertFile string
	TLSKeyFile  string
	
	// Secrets Manager
	secretsManager *secrets.SecretsManager
}

// LoadProduction loads production configuration with Secrets Manager
func LoadProduction(ctx context.Context) (*ProductionConfig, error) {
	env := getEnvWithDefault("ENVIRONMENT", "production")
	
	cfg := &ProductionConfig{
		Environment: env,
		Port:        getEnvInt("PORT", 8000),
		
		// Database
		DatabaseMaxConn: getEnvInt("DATABASE_MAX_CONN", 25),
		DatabaseMinConn: getEnvInt("DATABASE_MIN_CONN", 5),
		
		// Redis
		RedisURL: getEnvWithDefault("REDIS_URL", "localhost:6379"),
		RedisTLS: getEnvBool("REDIS_TLS", false),
		
		// JWT
		JWTExpiry:     time.Duration(getEnvInt("JWT_EXPIRY_HOURS", 24)) * time.Hour,
		RefreshExpiry: time.Duration(getEnvInt("REFRESH_EXPIRY_HOURS", 168)) * time.Hour,
		
		// Cloud Provider
		CloudProvider: getEnvWithDefault("CLOUD_PROVIDER", "gcp"),

		// GCP
		GCPProjectID: getEnvWithDefault("GCP_PROJECT_ID", ""),
		GCSBucket:    getEnvWithDefault("GCS_BUCKET", ""),
		GCPRegion:    getEnvWithDefault("GCP_REGION", "us-central1"),

		// AWS (legacy)
		AWSRegion:        getEnvWithDefault("AWS_REGION", "us-east-1"),
		S3Bucket:         getEnvWithDefault("S3_BUCKET", "synthos-datasets"),
		S3Endpoint:       getEnvWithDefault("S3_ENDPOINT", ""),
		SecretsManagerID: getEnvWithDefault("SECRETS_MANAGER_ID", "synthos/production/secrets"),
		
		// gRPC Services
		OrchestratorAddr:      getEnvWithDefault("ORCHESTRATOR_ADDR", "localhost:8080"),
		ValidationServiceAddr: getEnvWithDefault("VALIDATION_SERVICE_ADDR", "localhost:50051"),
		CollapseServiceAddr:   getEnvWithDefault("COLLAPSE_SERVICE_ADDR", "localhost:50052"),
		DataServiceAddr:       getEnvWithDefault("DATA_SERVICE_ADDR", "localhost:50054"),
		
		// CORS - production safe origins
		AllowedOrigins: getEnvList("ALLOWED_ORIGINS", []string{
			"https://www.synthos.dev",
			"https://synthos.dev",
			"https://app.synthos.dev",
		}),
		
		// Monitoring
		EnableMetrics:  getEnvBool("ENABLE_METRICS", true),
		EnableTracing:  getEnvBool("ENABLE_TRACING", true),
		JaegerEndpoint: getEnvWithDefault("JAEGER_ENDPOINT", "localhost:6831"),
		
		// Rate Limiting
		RateLimitRPS:   getEnvInt("RATE_LIMIT_RPS", 100),
		RateLimitBurst: getEnvInt("RATE_LIMIT_BURST", 200),
		
		// TLS
		TLSEnabled:  getEnvBool("TLS_ENABLED", false),
		TLSCertFile: getEnvWithDefault("TLS_CERT_FILE", ""),
		TLSKeyFile:  getEnvWithDefault("TLS_KEY_FILE", ""),
	}
	
	// Load secrets from GCP Secret Manager if using GCP
	if cfg.CloudProvider == "gcp" && cfg.GCPProjectID != "" {
		log.Println("Loading secrets from GCP Secret Manager...")
		cfg.JWTSecret = loadGCPSecret(ctx, cfg.GCPProjectID, "synthos-jwt-secret", cfg.JWTSecret)
		dbPass := loadGCPSecret(ctx, cfg.GCPProjectID, "synthos-db-password", "")
		if dbPass != "" && cfg.DatabaseURL == "" {
			cfg.DatabaseURL = fmt.Sprintf("postgres://synthos:%s@/synthos?host=/cloudsql/%s", dbPass, getEnvWithDefault("CLOUD_SQL_INSTANCE", ""))
		}
		cfg.RedisPassword = loadGCPSecret(ctx, cfg.GCPProjectID, "synthos-redis-password", cfg.RedisPassword)
		log.Println("✅ Loaded secrets from GCP Secret Manager")
	}

	// Load secrets from AWS Secrets Manager in production (legacy)
	if cfg.CloudProvider == "aws" && env == "production" && cfg.SecretsManagerID != "" {
		secretsCfg := secrets.SecretsConfig{
			Region:        cfg.AWSRegion,
			SecretID:      cfg.SecretsManagerID,
			CacheDuration: 5 * time.Minute,
		}
		
		sm, err := secrets.NewSecretsManager(ctx, secretsCfg)
		if err != nil {
			log.Printf("⚠️ Failed to initialize Secrets Manager, falling back to env vars: %v", err)
		} else {
			cfg.secretsManager = sm
			
			// Load secrets
			allSecrets, err := sm.GetAll()
			if err == nil {
				if allSecrets.JWTSecret != "" {
					cfg.JWTSecret = allSecrets.JWTSecret
				}
				if allSecrets.DatabaseURL != "" {
					cfg.DatabaseURL = allSecrets.DatabaseURL
				}
				if allSecrets.RedisPassword != "" {
					cfg.RedisPassword = allSecrets.RedisPassword
				}
				if allSecrets.AWSAccessKeyID != "" {
					cfg.AWSAccessKeyID = allSecrets.AWSAccessKeyID
				}
				if allSecrets.AWSSecretKey != "" {
					cfg.AWSSecretAccessKey = allSecrets.AWSSecretKey
				}
				log.Println("✅ Loaded secrets from AWS Secrets Manager")
			}
		}
	}
	
	// Fallback to environment variables if secrets not loaded
	if cfg.JWTSecret == "" {
		cfg.JWTSecret = getEnvWithDefault("JWT_SECRET", "")
	}
	if cfg.DatabaseURL == "" {
		cfg.DatabaseURL = getEnvWithDefault("DATABASE_URL", "")
	}
	if cfg.RedisPassword == "" {
		cfg.RedisPassword = getEnvWithDefault("REDIS_PASSWORD", "")
	}
	if cfg.AWSAccessKeyID == "" {
		cfg.AWSAccessKeyID = getEnvWithDefault("AWS_ACCESS_KEY_ID", "")
	}
	if cfg.AWSSecretAccessKey == "" {
		cfg.AWSSecretAccessKey = getEnvWithDefault("AWS_SECRET_ACCESS_KEY", "")
	}
	
	// Validate required configuration
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	
	return cfg, nil
}

// Validate validates the configuration
func (c *ProductionConfig) Validate() error {
	if c.Environment == "production" {
		if c.JWTSecret == "" {
			return fmt.Errorf("JWT_SECRET is required in production")
		}
		if len(c.JWTSecret) < 32 {
			return fmt.Errorf("JWT_SECRET must be at least 32 characters")
		}
		if c.DatabaseURL == "" {
			return fmt.Errorf("DATABASE_URL is required in production")
		}
		if len(c.AllowedOrigins) == 0 {
			return fmt.Errorf("ALLOWED_ORIGINS must be set in production")
		}
		for _, origin := range c.AllowedOrigins {
			if origin == "*" {
				return fmt.Errorf("wildcard CORS origin not allowed in production")
			}
		}
	}
	return nil
}

// GetSecretsManager returns the secrets manager instance
func (c *ProductionConfig) GetSecretsManager() *secrets.SecretsManager {
	return c.secretsManager
}

// RefreshSecrets forces a refresh of secrets
func (c *ProductionConfig) RefreshSecrets(ctx context.Context) error {
	if c.secretsManager == nil {
		return fmt.Errorf("secrets manager not initialized")
	}
	return c.secretsManager.ForceRefresh(ctx)
}

// Close cleans up resources
func (c *ProductionConfig) Close() {
	if c.secretsManager != nil {
		c.secretsManager.Stop()
	}
}

// Helper functions
func getEnvWithDefault(key, defaultValue string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return defaultValue
}

func getEnvInt(key string, defaultValue int) int {
	if value := os.Getenv(key); value != "" {
		if intVal, err := strconv.Atoi(value); err == nil {
			return intVal
		}
	}
	return defaultValue
}

func getEnvBool(key string, defaultValue bool) bool {
	if value := os.Getenv(key); value != "" {
		return strings.ToLower(value) == "true" || value == "1"
	}
	return defaultValue
}

func getEnvList(key string, defaultValue []string) []string {
	if value := os.Getenv(key); value != "" {
		return strings.Split(value, ",")
	}
	return defaultValue
}

// loadGCPSecret loads a secret from GCP Secret Manager, returns fallback on error
func loadGCPSecret(ctx context.Context, projectID, secretID, fallback string) string {
	client, err := secretmanager.NewClient(ctx)
	if err != nil {
		log.Printf("⚠️ Failed to create GCP Secret Manager client: %v", err)
		return fallback
	}
	defer client.Close()

	name := fmt.Sprintf("projects/%s/secrets/%s/versions/latest", projectID, secretID)
	result, err := client.AccessSecretVersion(ctx, &secretmanagerpb.AccessSecretVersionRequest{
		Name: name,
	})
	if err != nil {
		log.Printf("⚠️ Failed to access secret %s: %v", secretID, err)
		return fallback
	}

	return string(result.Payload.Data)
}
