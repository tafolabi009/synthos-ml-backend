package secrets

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"sync"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/secretsmanager"
)

// SecretsConfig holds configuration for AWS Secrets Manager
type SecretsConfig struct {
	Region        string
	SecretID      string
	CacheDuration time.Duration
}

// SecretsCache caches secrets with automatic refresh
type SecretsCache struct {
	mu           sync.RWMutex
	client       *secretsmanager.Client
	config       SecretsConfig
	cachedSecret map[string]string
	lastFetched  time.Time
	stopRefresh  chan struct{}
}

// SecretsManager manages secrets from AWS Secrets Manager
type SecretsManager struct {
	cache *SecretsCache
}

// SynthosSecrets represents the structure of secrets stored in AWS Secrets Manager
type SynthosSecrets struct {
	JWTSecret         string `json:"jwt_secret"`
	DatabaseURL       string `json:"database_url"`
	RedisPassword     string `json:"redis_password"`
	AWSAccessKeyID    string `json:"aws_access_key_id"`
	AWSSecretKey      string `json:"aws_secret_access_key"`
	EncryptionKey     string `json:"encryption_key"`
	AdminAPIKey       string `json:"admin_api_key"`
	StripeSecretKey   string `json:"stripe_secret_key"`
	SendGridAPIKey    string `json:"sendgrid_api_key"`
}

// NewSecretsManager creates a new secrets manager with automatic rotation
func NewSecretsManager(ctx context.Context, cfg SecretsConfig) (*SecretsManager, error) {
	if cfg.CacheDuration == 0 {
		cfg.CacheDuration = 5 * time.Minute
	}

	awsCfg, err := config.LoadDefaultConfig(ctx, config.WithRegion(cfg.Region))
	if err != nil {
		return nil, fmt.Errorf("failed to load AWS config: %w", err)
	}

	client := secretsmanager.NewFromConfig(awsCfg)

	cache := &SecretsCache{
		client:      client,
		config:      cfg,
		stopRefresh: make(chan struct{}),
	}

	// Initial fetch
	if err := cache.refresh(ctx); err != nil {
		return nil, fmt.Errorf("failed to fetch initial secrets: %w", err)
	}

	// Start background refresh
	go cache.startBackgroundRefresh(ctx)

	log.Printf("✅ Secrets Manager initialized (region: %s, secret: %s)", cfg.Region, cfg.SecretID)
	return &SecretsManager{cache: cache}, nil
}

// refresh fetches secrets from AWS Secrets Manager
func (c *SecretsCache) refresh(ctx context.Context) error {
	input := &secretsmanager.GetSecretValueInput{
		SecretId: aws.String(c.config.SecretID),
	}

	result, err := c.client.GetSecretValue(ctx, input)
	if err != nil {
		return fmt.Errorf("failed to get secret value: %w", err)
	}

	var secretData map[string]string
	if result.SecretString != nil {
		if err := json.Unmarshal([]byte(*result.SecretString), &secretData); err != nil {
			return fmt.Errorf("failed to unmarshal secret: %w", err)
		}
	}

	c.mu.Lock()
	c.cachedSecret = secretData
	c.lastFetched = time.Now()
	c.mu.Unlock()

	log.Printf("🔄 Secrets refreshed at %s", c.lastFetched.Format(time.RFC3339))
	return nil
}

// startBackgroundRefresh refreshes secrets periodically
func (c *SecretsCache) startBackgroundRefresh(ctx context.Context) {
	ticker := time.NewTicker(c.config.CacheDuration)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			if err := c.refresh(ctx); err != nil {
				log.Printf("⚠️ Failed to refresh secrets: %v", err)
			}
		case <-c.stopRefresh:
			return
		case <-ctx.Done():
			return
		}
	}
}

// Get retrieves a secret value by key
func (m *SecretsManager) Get(key string) (string, error) {
	m.cache.mu.RLock()
	defer m.cache.mu.RUnlock()

	value, ok := m.cache.cachedSecret[key]
	if !ok {
		return "", fmt.Errorf("secret key %s not found", key)
	}
	return value, nil
}

// GetAll retrieves all secrets as a structured object
func (m *SecretsManager) GetAll() (*SynthosSecrets, error) {
	m.cache.mu.RLock()
	defer m.cache.mu.RUnlock()

	secrets := &SynthosSecrets{
		JWTSecret:       m.cache.cachedSecret["jwt_secret"],
		DatabaseURL:     m.cache.cachedSecret["database_url"],
		RedisPassword:   m.cache.cachedSecret["redis_password"],
		AWSAccessKeyID:  m.cache.cachedSecret["aws_access_key_id"],
		AWSSecretKey:    m.cache.cachedSecret["aws_secret_access_key"],
		EncryptionKey:   m.cache.cachedSecret["encryption_key"],
		AdminAPIKey:     m.cache.cachedSecret["admin_api_key"],
		StripeSecretKey: m.cache.cachedSecret["stripe_secret_key"],
		SendGridAPIKey:  m.cache.cachedSecret["sendgrid_api_key"],
	}

	return secrets, nil
}

// GetJWTSecret retrieves the JWT secret
func (m *SecretsManager) GetJWTSecret() string {
	value, err := m.Get("jwt_secret")
	if err != nil {
		log.Printf("⚠️ Failed to get JWT secret: %v", err)
		return ""
	}
	return value
}

// GetDatabaseURL retrieves the database URL
func (m *SecretsManager) GetDatabaseURL() string {
	value, err := m.Get("database_url")
	if err != nil {
		log.Printf("⚠️ Failed to get database URL: %v", err)
		return ""
	}
	return value
}

// Stop stops the background refresh
func (m *SecretsManager) Stop() {
	close(m.cache.stopRefresh)
}

// ForceRefresh forces an immediate refresh of secrets
func (m *SecretsManager) ForceRefresh(ctx context.Context) error {
	return m.cache.refresh(ctx)
}

// IsHealthy checks if secrets are loaded and recent
func (m *SecretsManager) IsHealthy() bool {
	m.cache.mu.RLock()
	defer m.cache.mu.RUnlock()
	
	// Consider healthy if secrets were fetched within 2x cache duration
	return time.Since(m.cache.lastFetched) < 2*m.cache.config.CacheDuration
}
