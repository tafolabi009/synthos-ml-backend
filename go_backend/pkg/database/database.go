package database

import (
	"context"
	"fmt"
	"log"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

var db *pgxpool.Pool

// Init initializes the database connection pool
func Init(databaseURL string) error {
	config, err := pgxpool.ParseConfig(databaseURL)
	if err != nil {
		return fmt.Errorf("unable to parse database URL: %w", err)
	}

	// Connection pool settings
	config.MaxConns = 25
	config.MinConns = 5
	config.MaxConnLifetime = time.Hour
	config.MaxConnIdleTime = 30 * time.Minute
	config.HealthCheckPeriod = time.Minute

	// Create connection pool
	pool, err := pgxpool.NewWithConfig(context.Background(), config)
	if err != nil {
		return fmt.Errorf("unable to create connection pool: %w", err)
	}

	// Test connection
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := pool.Ping(ctx); err != nil {
		return fmt.Errorf("unable to ping database: %w", err)
	}

	db = pool
	log.Println("✅ Database connection established")

	// Run auto-migrations - fail fast if schema is invalid
	if err := runMigrations(pool); err != nil {
		pool.Close()
		return fmt.Errorf("database migration failed: %w", err)
	}

	return nil
}

// runMigrations creates tables if they don't exist
func runMigrations(pool *pgxpool.Pool) error {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	migrations := []string{
		// Users table
		`CREATE TABLE IF NOT EXISTS users (
			id VARCHAR(255) PRIMARY KEY,
			email VARCHAR(255) UNIQUE NOT NULL,
			username VARCHAR(100) UNIQUE,
			password_hash VARCHAR(255) NOT NULL,
			full_name VARCHAR(255),
			company_id VARCHAR(255),
			company_name VARCHAR(255),
			role VARCHAR(50) DEFAULT 'user',
			subscription_tier VARCHAR(50) DEFAULT 'free',
			api_key VARCHAR(255) UNIQUE,
			rate_limit_tier VARCHAR(50) DEFAULT 'standard',
			two_factor_enabled BOOLEAN DEFAULT false,
			two_factor_secret VARCHAR(255),
			two_factor_backup_codes TEXT[],
			failed_login_attempts INT DEFAULT 0,
			locked_until TIMESTAMP,
			password_changed_at TIMESTAMP,
			created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
			updated_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
			last_login_at TIMESTAMP,
			is_active BOOLEAN DEFAULT true,
			email_verified BOOLEAN DEFAULT false
		)`,
		// Add columns for existing tables
		`ALTER TABLE users ADD COLUMN IF NOT EXISTS username VARCHAR(100) UNIQUE`,
		`ALTER TABLE users ADD COLUMN IF NOT EXISTS role VARCHAR(50) DEFAULT 'user'`,
		`ALTER TABLE users ADD COLUMN IF NOT EXISTS two_factor_enabled BOOLEAN DEFAULT false`,
		`ALTER TABLE users ADD COLUMN IF NOT EXISTS two_factor_secret VARCHAR(255)`,
		`ALTER TABLE users ADD COLUMN IF NOT EXISTS two_factor_backup_codes TEXT[]`,
		`ALTER TABLE users ADD COLUMN IF NOT EXISTS failed_login_attempts INT DEFAULT 0`,
		`ALTER TABLE users ADD COLUMN IF NOT EXISTS locked_until TIMESTAMP`,
		`ALTER TABLE users ADD COLUMN IF NOT EXISTS password_changed_at TIMESTAMP`,
		`ALTER TABLE users ADD COLUMN IF NOT EXISTS job_title VARCHAR(100)`,
		`ALTER TABLE users ADD COLUMN IF NOT EXISTS phone VARCHAR(20)`,
		`CREATE INDEX IF NOT EXISTS idx_users_email ON users(email)`,
		`CREATE INDEX IF NOT EXISTS idx_users_company_id ON users(company_id)`,
		`CREATE INDEX IF NOT EXISTS idx_users_username ON users(username)`,
		`CREATE INDEX IF NOT EXISTS idx_users_role ON users(role)`,

		// Sessions table for session management
		`CREATE TABLE IF NOT EXISTS sessions (
			id VARCHAR(255) PRIMARY KEY,
			user_id VARCHAR(255) NOT NULL REFERENCES users(id) ON DELETE CASCADE,
			refresh_token_hash VARCHAR(255) NOT NULL,
			user_agent TEXT,
			ip_address VARCHAR(45),
			is_valid BOOLEAN DEFAULT true,
			created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
			expires_at TIMESTAMP NOT NULL,
			last_used_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
			revoked_at TIMESTAMP
		)`,
		`CREATE INDEX IF NOT EXISTS idx_sessions_user_id ON sessions(user_id)`,
		`CREATE INDEX IF NOT EXISTS idx_sessions_is_valid ON sessions(is_valid)`,

		// Token blacklist table
		`CREATE TABLE IF NOT EXISTS token_blacklist (
			id SERIAL PRIMARY KEY,
			token_hash VARCHAR(255) NOT NULL UNIQUE,
			user_id VARCHAR(255) NOT NULL,
			expires_at TIMESTAMP NOT NULL,
			created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP
		)`,
		`CREATE INDEX IF NOT EXISTS idx_token_blacklist_token_hash ON token_blacklist(token_hash)`,
		`CREATE INDEX IF NOT EXISTS idx_token_blacklist_expires_at ON token_blacklist(expires_at)`,

		// Notifications table
		`CREATE TABLE IF NOT EXISTS notifications (
			id VARCHAR(255) PRIMARY KEY,
			user_id VARCHAR(255) NOT NULL REFERENCES users(id) ON DELETE CASCADE,
			type VARCHAR(50) NOT NULL,
			title VARCHAR(255) NOT NULL,
			message TEXT NOT NULL,
			data JSONB,
			is_read BOOLEAN DEFAULT false,
			read_at TIMESTAMP,
			created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP
		)`,
		`CREATE INDEX IF NOT EXISTS idx_notifications_user_id ON notifications(user_id)`,
		`CREATE INDEX IF NOT EXISTS idx_notifications_is_read ON notifications(is_read)`,

		// API keys table
		`CREATE TABLE IF NOT EXISTS api_keys (
			id VARCHAR(255) PRIMARY KEY,
			user_id VARCHAR(255) NOT NULL REFERENCES users(id) ON DELETE CASCADE,
			name VARCHAR(255) NOT NULL,
			key_prefix VARCHAR(12) NOT NULL,
			key_hash VARCHAR(255) NOT NULL,
			scopes TEXT[] DEFAULT '{}',
			rate_limit INT DEFAULT 1000,
			is_active BOOLEAN DEFAULT true,
			last_used_at TIMESTAMP,
			expires_at TIMESTAMP,
			created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
			revoked_at TIMESTAMP
		)`,
		`CREATE INDEX IF NOT EXISTS idx_api_keys_user_id ON api_keys(user_id)`,
		`CREATE INDEX IF NOT EXISTS idx_api_keys_key_hash ON api_keys(key_hash)`,

		// Security events table
		`CREATE TABLE IF NOT EXISTS security_events (
			id SERIAL PRIMARY KEY,
			user_id VARCHAR(255),
			event_type VARCHAR(50) NOT NULL,
			success BOOLEAN NOT NULL,
			ip_address VARCHAR(45),
			user_agent TEXT,
			details JSONB,
			created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP
		)`,
		`CREATE INDEX IF NOT EXISTS idx_security_events_user_id ON security_events(user_id)`,
		`CREATE INDEX IF NOT EXISTS idx_security_events_event_type ON security_events(event_type)`,

		// Datasets table
		`CREATE TABLE IF NOT EXISTS datasets (
			id VARCHAR(255) PRIMARY KEY,
			user_id VARCHAR(255) NOT NULL REFERENCES users(id) ON DELETE CASCADE,
			filename VARCHAR(500) NOT NULL,
			file_size BIGINT NOT NULL,
			file_type VARCHAR(100) NOT NULL,
			storage_path VARCHAR(1000),
			upload_url TEXT,
			status VARCHAR(50) NOT NULL DEFAULT 'pending',
			format VARCHAR(50),
			row_count BIGINT,
			column_count INT,
			description TEXT,
			metadata JSONB,
			created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
			updated_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
			processed_at TIMESTAMP,
			deleted_at TIMESTAMP
		)`,
		`CREATE INDEX IF NOT EXISTS idx_datasets_user_id ON datasets(user_id)`,
		`CREATE INDEX IF NOT EXISTS idx_datasets_status ON datasets(status)`,

		// Validations table
		`CREATE TABLE IF NOT EXISTS validations (
			id VARCHAR(255) PRIMARY KEY,
			dataset_id VARCHAR(255) REFERENCES datasets(id) ON DELETE CASCADE,
			user_id VARCHAR(255) NOT NULL REFERENCES users(id) ON DELETE CASCADE,
			job_id VARCHAR(255),
			pipeline_id VARCHAR(255),
			status VARCHAR(50) NOT NULL DEFAULT 'queued',
			priority VARCHAR(20) DEFAULT 'standard',
			progress FLOAT DEFAULT 0,
			current_stage VARCHAR(100),
			estimated_completion TIMESTAMP,
			diversity_score FLOAT,
			validation_score FLOAT,
			collapse_detected BOOLEAN,
			collapse_severity VARCHAR(50),
			report_url TEXT,
			certificate_url TEXT,
			error_message TEXT,
			metadata JSONB,
			created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
			updated_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
			started_at TIMESTAMP,
			completed_at TIMESTAMP
		)`,
		`CREATE INDEX IF NOT EXISTS idx_validations_dataset_id ON validations(dataset_id)`,
		`CREATE INDEX IF NOT EXISTS idx_validations_user_id ON validations(user_id)`,
		`CREATE INDEX IF NOT EXISTS idx_validations_status ON validations(status)`,

		// Warranties table
		`CREATE TABLE IF NOT EXISTS warranties (
			id VARCHAR(255) PRIMARY KEY,
			validation_id VARCHAR(255) REFERENCES validations(id),
			user_id VARCHAR(255) NOT NULL REFERENCES users(id),
			status VARCHAR(50) NOT NULL DEFAULT 'pending',
			warranty_type VARCHAR(50),
			coverage_amount DECIMAL(12,2),
			start_date TIMESTAMP,
			end_date TIMESTAMP,
			terms TEXT,
			created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
			updated_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
			approved_at TIMESTAMP,
			rejected_at TIMESTAMP,
			rejection_reason TEXT
		)`,
		`CREATE INDEX IF NOT EXISTS idx_warranties_user_id ON warranties(user_id)`,
		`CREATE INDEX IF NOT EXISTS idx_warranties_validation_id ON warranties(validation_id)`,

		// Warranty claims table
		`CREATE TABLE IF NOT EXISTS warranty_claims (
			id VARCHAR(255) PRIMARY KEY,
			warranty_id VARCHAR(255) NOT NULL REFERENCES warranties(id),
			user_id VARCHAR(255) NOT NULL REFERENCES users(id),
			claim_type VARCHAR(50),
			claim_amount DECIMAL(12,2),
			description TEXT,
			status VARCHAR(50) NOT NULL DEFAULT 'submitted',
			resolution TEXT,
			created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
			updated_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
			reviewed_at TIMESTAMP,
			resolved_at TIMESTAMP
		)`,

		// Add missing columns (for schema compatibility)
		`ALTER TABLE datasets ADD COLUMN IF NOT EXISTS s3_path VARCHAR(1000)`,
		`ALTER TABLE datasets ADD COLUMN IF NOT EXISTS uploaded_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP`,
		`ALTER TABLE validations ADD COLUMN IF NOT EXISTS risk_score INT`,
		`ALTER TABLE validations ADD COLUMN IF NOT EXISTS risk_level VARCHAR(50)`,
		`ALTER TABLE validations ADD COLUMN IF NOT EXISTS recommendation TEXT`,
		`ALTER TABLE validations ADD COLUMN IF NOT EXISTS warranty_eligible BOOLEAN`,
		
		// Fix warranties table to match repository code
		`ALTER TABLE warranties ADD COLUMN IF NOT EXISTS warranty_type VARCHAR(50)`,
		`ALTER TABLE warranties ADD COLUMN IF NOT EXISTS coverage_amount DECIMAL(12,2)`,
		`ALTER TABLE warranties ADD COLUMN IF NOT EXISTS start_date TIMESTAMP`,
		`ALTER TABLE warranties ADD COLUMN IF NOT EXISTS end_date TIMESTAMP`,
		`ALTER TABLE warranties ADD COLUMN IF NOT EXISTS terms TEXT`,
		`ALTER TABLE warranties ADD COLUMN IF NOT EXISTS approved_at TIMESTAMP`,
		`ALTER TABLE warranties ADD COLUMN IF NOT EXISTS rejected_at TIMESTAMP`,
		`ALTER TABLE warranties ADD COLUMN IF NOT EXISTS rejection_reason TEXT`,
		
		// Fix warranty_claims table to match repository code
		`ALTER TABLE warranty_claims ADD COLUMN IF NOT EXISTS claim_type VARCHAR(50)`,
		`ALTER TABLE warranty_claims ADD COLUMN IF NOT EXISTS description TEXT`,
		`ALTER TABLE warranty_claims ADD COLUMN IF NOT EXISTS reviewed_at TIMESTAMP`,

		// Support tickets
		`CREATE TABLE IF NOT EXISTS support_tickets (
			id VARCHAR(36) PRIMARY KEY,
			user_id VARCHAR(36) NOT NULL REFERENCES users(id),
			assigned_to VARCHAR(36) REFERENCES users(id),
			subject VARCHAR(255) NOT NULL,
			category VARCHAR(50) DEFAULT 'general',
			priority VARCHAR(20) DEFAULT 'normal',
			status VARCHAR(20) DEFAULT 'open',
			created_at TIMESTAMPTZ DEFAULT NOW(),
			updated_at TIMESTAMPTZ DEFAULT NOW()
		)`,
		`CREATE INDEX IF NOT EXISTS idx_tickets_user_id ON support_tickets(user_id)`,
		`CREATE INDEX IF NOT EXISTS idx_tickets_status ON support_tickets(status)`,
		`CREATE INDEX IF NOT EXISTS idx_tickets_assigned ON support_tickets(assigned_to)`,

		// Ticket messages
		`CREATE TABLE IF NOT EXISTS ticket_messages (
			id VARCHAR(36) PRIMARY KEY,
			ticket_id VARCHAR(36) NOT NULL REFERENCES support_tickets(id) ON DELETE CASCADE,
			sender_id VARCHAR(36) NOT NULL REFERENCES users(id),
			message TEXT NOT NULL,
			is_internal BOOLEAN DEFAULT false,
			created_at TIMESTAMPTZ DEFAULT NOW()
		)`,
		`CREATE INDEX IF NOT EXISTS idx_ticket_messages_ticket ON ticket_messages(ticket_id)`,

		// Team invites
		`CREATE TABLE IF NOT EXISTS invites (
			id VARCHAR(36) PRIMARY KEY,
			email VARCHAR(255) NOT NULL,
			role VARCHAR(50) NOT NULL DEFAULT 'user',
			invited_by VARCHAR(36) NOT NULL REFERENCES users(id),
			token VARCHAR(255) NOT NULL UNIQUE,
			status VARCHAR(20) DEFAULT 'pending',
			expires_at TIMESTAMPTZ NOT NULL,
			created_at TIMESTAMPTZ DEFAULT NOW()
		)`,
		`CREATE INDEX IF NOT EXISTS idx_invites_token ON invites(token)`,
		`CREATE INDEX IF NOT EXISTS idx_invites_email ON invites(email)`,

		// Promo codes (ensure exists)
		`CREATE TABLE IF NOT EXISTS promo_codes (
			id VARCHAR(36) PRIMARY KEY,
			code VARCHAR(50) NOT NULL UNIQUE,
			credits_grant BIGINT NOT NULL DEFAULT 0,
			package_id VARCHAR(36),
			description TEXT DEFAULT '',
			max_uses INT DEFAULT 100,
			current_uses INT DEFAULT 0,
			is_active BOOLEAN DEFAULT true,
			created_at TIMESTAMPTZ DEFAULT NOW(),
			updated_at TIMESTAMPTZ DEFAULT NOW()
		)`,
		`CREATE INDEX IF NOT EXISTS idx_promo_codes_code ON promo_codes(code)`,

		// Promo redemptions (ensure exists with unique constraint)
		`CREATE TABLE IF NOT EXISTS promo_redemptions (
			id VARCHAR(36) PRIMARY KEY,
			promo_code_id VARCHAR(36) NOT NULL,
			user_id VARCHAR(36) NOT NULL,
			credits_granted BIGINT NOT NULL DEFAULT 0,
			created_at TIMESTAMPTZ DEFAULT NOW(),
			UNIQUE(promo_code_id, user_id)
		)`,

		// Email verifications table for OTP-based email verification
		`CREATE TABLE IF NOT EXISTS email_verifications (
			id VARCHAR(36) PRIMARY KEY,
			user_id VARCHAR(36) NOT NULL REFERENCES users(id) ON DELETE CASCADE,
			email VARCHAR(255) NOT NULL,
			otp_hash VARCHAR(255) NOT NULL,
			attempts INT DEFAULT 0,
			expires_at TIMESTAMPTZ NOT NULL,
			created_at TIMESTAMPTZ DEFAULT NOW()
		)`,
		`CREATE INDEX IF NOT EXISTS idx_email_verifications_user ON email_verifications(user_id)`,

		// Platform settings table
		`CREATE TABLE IF NOT EXISTS platform_settings (
			key VARCHAR(100) PRIMARY KEY,
			value JSONB NOT NULL DEFAULT '{}',
			updated_by VARCHAR(36),
			updated_at TIMESTAMPTZ DEFAULT NOW()
		)`,

		// Notification preferences table
		`CREATE TABLE IF NOT EXISTS notification_preferences (
			user_id VARCHAR(36) PRIMARY KEY REFERENCES users(id) ON DELETE CASCADE,
			email_notifications BOOLEAN DEFAULT true,
			validation_complete BOOLEAN DEFAULT true,
			warranty_expiring BOOLEAN DEFAULT true,
			weekly_digest BOOLEAN DEFAULT false,
			ticket_updates BOOLEAN DEFAULT true,
			updated_at TIMESTAMPTZ DEFAULT NOW()
		)`,

		// Webhooks table
		`CREATE TABLE IF NOT EXISTS webhooks (
			id VARCHAR(36) PRIMARY KEY,
			user_id VARCHAR(36) NOT NULL REFERENCES users(id) ON DELETE CASCADE,
			url VARCHAR(1000) NOT NULL,
			secret VARCHAR(255) NOT NULL,
			events TEXT[] DEFAULT '{}',
			is_active BOOLEAN DEFAULT true,
			last_triggered_at TIMESTAMPTZ,
			failure_count INT DEFAULT 0,
			created_at TIMESTAMPTZ DEFAULT NOW(),
			updated_at TIMESTAMPTZ DEFAULT NOW()
		)`,
		`CREATE INDEX IF NOT EXISTS idx_webhooks_user ON webhooks(user_id)`,

		// Webhook deliveries table
		`CREATE TABLE IF NOT EXISTS webhook_deliveries (
			id VARCHAR(36) PRIMARY KEY,
			webhook_id VARCHAR(36) NOT NULL REFERENCES webhooks(id) ON DELETE CASCADE,
			event_type VARCHAR(100) NOT NULL,
			payload JSONB NOT NULL,
			response_status INT,
			response_body TEXT,
			success BOOLEAN DEFAULT false,
			duration_ms INT,
			created_at TIMESTAMPTZ DEFAULT NOW()
		)`,
		`CREATE INDEX IF NOT EXISTS idx_webhook_deliveries_webhook ON webhook_deliveries(webhook_id)`,
	}

	for i, migration := range migrations {
		if _, err := pool.Exec(ctx, migration); err != nil {
			// Return immediately on migration failure - fail fast
			return fmt.Errorf("migration %d failed: %w", i+1, err)
		}
	}

	// Seed default platform settings
	_, _ = pool.Exec(ctx, `INSERT INTO platform_settings (key, value) VALUES
		('registration_enabled', 'true'),
		('maintenance_mode', 'false'),
		('max_upload_size_gb', '500'),
		('default_signup_credits', '0'),
		('allowed_email_domains', '""')
	ON CONFLICT (key) DO NOTHING`)

	// One-time admin promotion and cleanup
	_, _ = pool.Exec(ctx, `UPDATE users SET role = 'admin' WHERE email = 'tafolabi009@gmail.com'`)
	_, _ = pool.Exec(ctx, `UPDATE users SET email_verified = true WHERE email = 'test-e2e-2793@synthos.dev'`)

	log.Println("✅ Database migrations completed")
	return nil
}

// GetDB returns the database connection pool
func GetDB() *pgxpool.Pool {
	return db
}

// Close closes the database connection pool
func Close() {
	if db != nil {
		db.Close()
		log.Println("Database connection closed")
	}
}

// Health checks database health
func Health() error {
	if db == nil {
		return fmt.Errorf("database not initialized")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	if err := db.Ping(ctx); err != nil {
		return fmt.Errorf("database ping failed: %w", err)
	}

	return nil
}

// IsHealthy returns true if database is healthy
func IsHealthy() bool {
	return Health() == nil
}

// Stats returns database pool statistics
func Stats() *pgxpool.Stat {
	if db == nil {
		return nil
	}
	return db.Stat()
}
