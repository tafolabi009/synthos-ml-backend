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

	// Run auto-migrations
	if err := runMigrations(pool); err != nil {
		log.Printf("⚠️ Migration warning: %v", err)
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
			password_hash VARCHAR(255) NOT NULL,
			full_name VARCHAR(255),
			company_id VARCHAR(255),
			company_name VARCHAR(255),
			subscription_tier VARCHAR(50) DEFAULT 'free',
			api_key VARCHAR(255) UNIQUE,
			rate_limit_tier VARCHAR(50) DEFAULT 'standard',
			created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
			updated_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
			last_login_at TIMESTAMP,
			is_active BOOLEAN DEFAULT true,
			email_verified BOOLEAN DEFAULT false
		)`,
		`CREATE INDEX IF NOT EXISTS idx_users_email ON users(email)`,
		`CREATE INDEX IF NOT EXISTS idx_users_company_id ON users(company_id)`,

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
			coverage_type VARCHAR(50) NOT NULL,
			coverage_period_days INT NOT NULL,
			max_claim_amount DECIMAL(10,2),
			premium DECIMAL(10,2),
			status VARCHAR(50) NOT NULL DEFAULT 'pending',
			terms_version VARCHAR(20),
			created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
			updated_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
			expires_at TIMESTAMP,
			activated_at TIMESTAMP
		)`,
		`CREATE INDEX IF NOT EXISTS idx_warranties_user_id ON warranties(user_id)`,
		`CREATE INDEX IF NOT EXISTS idx_warranties_validation_id ON warranties(validation_id)`,

		// Warranty claims table
		`CREATE TABLE IF NOT EXISTS warranty_claims (
			id VARCHAR(255) PRIMARY KEY,
			warranty_id VARCHAR(255) NOT NULL REFERENCES warranties(id),
			user_id VARCHAR(255) NOT NULL REFERENCES users(id),
			reason TEXT NOT NULL,
			evidence_urls TEXT[],
			claim_amount DECIMAL(10,2),
			status VARCHAR(50) NOT NULL DEFAULT 'submitted',
			resolution TEXT,
			created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
			updated_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
			resolved_at TIMESTAMP
		)`,
	}

	for _, migration := range migrations {
		if _, err := pool.Exec(ctx, migration); err != nil {
			log.Printf("Migration error: %v", err)
			// Continue with other migrations
		}
	}

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
