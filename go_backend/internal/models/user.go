package models

import (
	"time"
)

// User represents a system user with full profile data
type User struct {
	ID                  string     `json:"user_id" db:"id"`
	Email               string     `json:"email" db:"email"`
	Username            *string    `json:"username" db:"username"`
	PasswordHash        string     `json:"-" db:"password_hash"`
	FullName            *string    `json:"full_name" db:"full_name"`
	CompanyID           *string    `json:"company_id" db:"company_id"`
	CompanyName         *string    `json:"company_name" db:"company_name"`
	Role                *string    `json:"role" db:"role"`
	SubscriptionTier    *string    `json:"subscription_tier" db:"subscription_tier"`
	TwoFactorEnabled    bool       `json:"two_factor_enabled" db:"two_factor_enabled"`
	TwoFactorSecret     *string    `json:"-" db:"two_factor_secret"`       // Never exposed
	TwoFactorBackupCodes []string  `json:"-" db:"two_factor_backup_codes"` // Never exposed
	FailedLoginAttempts int        `json:"-" db:"failed_login_attempts"`
	LockedUntil         *time.Time `json:"-" db:"locked_until"`
	PasswordChangedAt   *time.Time `json:"password_changed_at,omitempty" db:"password_changed_at"`
	EmailVerified       bool       `json:"email_verified" db:"email_verified"`
	IsActive            bool       `json:"is_active" db:"is_active"`
	LastLoginAt         *time.Time `json:"last_login_at,omitempty" db:"last_login_at"`
	CreatedAt           time.Time  `json:"created_at" db:"created_at"`
	UpdatedAt           time.Time  `json:"updated_at" db:"updated_at"`
}

// UserProfile is a safe representation of user data for API responses
// This excludes all sensitive fields and is what /me returns
type UserProfile struct {
	ID               string     `json:"id"`
	Email            string     `json:"email"`
	Username         string     `json:"username"`
	FullName         string     `json:"full_name"`
	CompanyID        string     `json:"company_id"`
	CompanyName      string     `json:"company_name"`
	Role             string     `json:"role"`
	Roles            []string   `json:"roles"` // Expanded roles for frontend compatibility
	SubscriptionTier string     `json:"subscription_tier"`
	TwoFactorEnabled bool       `json:"two_factor_enabled"`
	EmailVerified    bool       `json:"email_verified"`
	IsActive         bool       `json:"is_active"`
	LastLoginAt      *time.Time `json:"last_login_at,omitempty"`
	CreatedAt        time.Time  `json:"created_at"`
}

// ToProfile converts a User to a safe UserProfile
func (u *User) ToProfile() *UserProfile {
	role := "user"
	if u.Role != nil {
		role = *u.Role
	}
	roles := []string{role}
	if role == "admin" {
		roles = append(roles, "user")
	}
	
	username := ""
	if u.Username != nil {
		username = *u.Username
	}
	fullName := ""
	if u.FullName != nil {
		fullName = *u.FullName
	}
	companyID := ""
	if u.CompanyID != nil {
		companyID = *u.CompanyID
	}
	companyName := ""
	if u.CompanyName != nil {
		companyName = *u.CompanyName
	}
	subscriptionTier := "free"
	if u.SubscriptionTier != nil {
		subscriptionTier = *u.SubscriptionTier
	}
	
	return &UserProfile{
		ID:               u.ID,
		Email:            u.Email,
		Username:         username,
		FullName:         fullName,
		CompanyID:        companyID,
		CompanyName:      companyName,
		Role:             role,
		Roles:            roles,
		SubscriptionTier: subscriptionTier,
		TwoFactorEnabled: u.TwoFactorEnabled,
		EmailVerified:    u.EmailVerified,
		IsActive:         u.IsActive,
		LastLoginAt:      u.LastLoginAt,
		CreatedAt:        u.CreatedAt,
	}
}

// RegisterRequest is the request body for user registration
type RegisterRequest struct {
	Email       string `json:"email" validate:"required,email"`
	Password    string `json:"password" validate:"required,min=8"`
	Username    string `json:"username" validate:"omitempty,min=3,max=50,alphanum"`
	FullName    string `json:"full_name" validate:"required,min=1,max=100"`
	CompanyName string `json:"company_name" validate:"required,min=1,max=200"`
}

// LoginRequest is the request body for user login
type LoginRequest struct {
	Email    string `json:"email" validate:"required,email"`
	Password string `json:"password" validate:"required"`
	TOTPCode string `json:"totp_code,omitempty"` // Required if 2FA is enabled
}

// LoginResponse is the response for successful login
type LoginResponse struct {
	AccessToken      string       `json:"access_token"`
	RefreshToken     string       `json:"refresh_token"`
	TokenType        string       `json:"token_type"`
	ExpiresIn        int          `json:"expires_in"`
	User             *UserProfile `json:"user"`
	RequiresTwoFactor bool        `json:"requires_two_factor,omitempty"`
}

// RefreshTokenRequest is the request body for token refresh
type RefreshTokenRequest struct {
	RefreshToken string `json:"refresh_token" validate:"required"`
}

// RefreshTokenResponse is the response for token refresh
type RefreshTokenResponse struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token,omitempty"` // New refresh token for rotation
	ExpiresIn    int    `json:"expires_in"`
}

// ChangePasswordRequest for password change
type ChangePasswordRequest struct {
	CurrentPassword string `json:"current_password" validate:"required"`
	NewPassword     string `json:"new_password" validate:"required,min=8"`
}

// ForgotPasswordRequest for initiating password reset
type ForgotPasswordRequest struct {
	Email string `json:"email" validate:"required,email"`
}

// ResetPasswordRequest for completing password reset
type ResetPasswordRequest struct {
	Token       string `json:"token" validate:"required"`
	NewPassword string `json:"new_password" validate:"required,min=8"`
}

// TwoFactorSetupResponse contains 2FA setup data
type TwoFactorSetupResponse struct {
	Secret      string   `json:"secret"`       // Only shown once during setup
	QRCodeURL   string   `json:"qr_code_url"`  // otpauth:// URL for QR code generation
	BackupCodes []string `json:"backup_codes"` // Recovery codes, shown only once
}

// TwoFactorVerifyRequest for verifying 2FA setup or login
type TwoFactorVerifyRequest struct {
	Code string `json:"code" validate:"required,len=6"`
}

// TwoFactorDisableRequest for disabling 2FA
type TwoFactorDisableRequest struct {
	Password string `json:"password" validate:"required"`
	Code     string `json:"code" validate:"required"` // TOTP code or backup code
}

// Session represents an active user session
type Session struct {
	ID               string    `json:"id" db:"id"`
	UserID           string    `json:"user_id" db:"user_id"`
	RefreshTokenHash string    `json:"-" db:"refresh_token_hash"`
	UserAgent        string    `json:"user_agent" db:"user_agent"`
	IPAddress        string    `json:"ip_address" db:"ip_address"`
	IsValid          bool      `json:"is_valid" db:"is_valid"`
	CreatedAt        time.Time `json:"created_at" db:"created_at"`
	ExpiresAt        time.Time `json:"expires_at" db:"expires_at"`
	LastUsedAt       time.Time `json:"last_used_at" db:"last_used_at"`
	RevokedAt        *time.Time `json:"revoked_at,omitempty" db:"revoked_at"`
}

// APIKey represents a user's API key
type APIKey struct {
	ID         string     `json:"id" db:"id"`
	UserID     string     `json:"user_id" db:"user_id"`
	Name       string     `json:"name" db:"name"`
	KeyPrefix  string     `json:"key_prefix" db:"key_prefix"`   // First 8 chars for display
	KeyHash    string     `json:"-" db:"key_hash"`              // Never exposed
	Scopes     []string   `json:"scopes" db:"scopes"`
	RateLimit  int        `json:"rate_limit" db:"rate_limit"`
	IsActive   bool       `json:"is_active" db:"is_active"`
	LastUsedAt *time.Time `json:"last_used_at,omitempty" db:"last_used_at"`
	ExpiresAt  *time.Time `json:"expires_at,omitempty" db:"expires_at"`
	CreatedAt  time.Time  `json:"created_at" db:"created_at"`
	RevokedAt  *time.Time `json:"revoked_at,omitempty" db:"revoked_at"`
}

// APIKeyCreateRequest for creating a new API key
type APIKeyCreateRequest struct {
	Name      string   `json:"name" validate:"required,min=1,max=100"`
	Scopes    []string `json:"scopes" validate:"required,min=1"`
	ExpiresIn int      `json:"expires_in,omitempty"` // Days until expiration, 0 = never
}

// APIKeyCreateResponse contains the full key (shown only once)
type APIKeyCreateResponse struct {
	ID        string    `json:"id"`
	Name      string    `json:"name"`
	Key       string    `json:"key"`       // Full key, shown only once
	KeyPrefix string    `json:"key_prefix"`
	Scopes    []string  `json:"scopes"`
	ExpiresAt *time.Time `json:"expires_at,omitempty"`
	CreatedAt time.Time `json:"created_at"`
}

// Notification represents a user notification
type Notification struct {
	ID        string     `json:"id" db:"id"`
	UserID    string     `json:"user_id" db:"user_id"`
	Type      string     `json:"type" db:"type"`
	Title     string     `json:"title" db:"title"`
	Message   string     `json:"message" db:"message"`
	Data      *string    `json:"data,omitempty" db:"data"` // JSON string
	IsRead    bool       `json:"is_read" db:"is_read"`
	ReadAt    *time.Time `json:"read_at,omitempty" db:"read_at"`
	CreatedAt time.Time  `json:"created_at" db:"created_at"`
}

// NotificationReadRequest for marking notifications as read
type NotificationReadRequest struct {
	NotificationIDs []string `json:"notification_ids" validate:"required,min=1"`
}

// PasswordResetToken for password reset flow
type PasswordResetToken struct {
	ID        string    `json:"id" db:"id"`
	UserID    string    `json:"user_id" db:"user_id"`
	TokenHash string    `json:"-" db:"token_hash"`
	ExpiresAt time.Time `json:"expires_at" db:"expires_at"`
	UsedAt    *time.Time `json:"used_at,omitempty" db:"used_at"`
	CreatedAt time.Time `json:"created_at" db:"created_at"`
}

// SecurityEvent for audit logging
type SecurityEvent struct {
	ID        int64     `json:"id" db:"id"`
	UserID    string    `json:"user_id" db:"user_id"`
	EventType string    `json:"event_type" db:"event_type"`
	Success   bool      `json:"success" db:"success"`
	IPAddress string    `json:"ip_address" db:"ip_address"`
	UserAgent string    `json:"user_agent" db:"user_agent"`
	Location  string    `json:"location,omitempty" db:"location"`
	Details   *string   `json:"details,omitempty" db:"details"` // JSON string
	CreatedAt time.Time `json:"created_at" db:"created_at"`
}

// PaginatedResponse is a generic wrapper for paginated results
type PaginatedResponse struct {
	Items      interface{} `json:"items"`
	Total      int64       `json:"total"`
	Page       int         `json:"page"`
	PerPage    int         `json:"per_page"`
	TotalPages int         `json:"total_pages"`
}
