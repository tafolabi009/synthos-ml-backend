package handlers

import (
	"context"
	"encoding/json"
	"log"
	"strings"
	"time"

	"github.com/gofiber/fiber/v2"
	"github.com/google/uuid"
	"github.com/tafolabi009/backend/go_backend/internal/auth"
	"github.com/tafolabi009/backend/go_backend/internal/models"
	"github.com/tafolabi009/backend/go_backend/internal/repository"
	"github.com/tafolabi009/backend/go_backend/pkg/database"
)

const (
	maxFailedAttempts = 5
	lockoutDuration   = 15 * time.Minute
	accessTokenTTL    = 15 * time.Minute
	refreshTokenTTL   = 30 * 24 * time.Hour
	resetTokenTTL     = 1 * time.Hour
)

// Helper to safely dereference string pointer
func strVal(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

// Helper to create string pointer
func strPtr(s string) *string {
	return &s
}

// RegisterFiber handles user registration with full validation
// POST /api/v1/auth/register
func RegisterFiber(c *fiber.Ctx) error {
	var req models.RegisterRequest
	if err := c.BodyParser(&req); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
			"error": fiber.Map{
				"code":    "INVALID_REQUEST",
				"message": "Invalid request body",
			},
		})
	}

	// Validate required fields
	if req.Email == "" || req.Password == "" || req.FullName == "" || req.CompanyName == "" {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
			"error": fiber.Map{
				"code":    "VALIDATION_ERROR",
				"message": "Email, password, full_name, and company_name are required",
			},
		})
	}

	// Validate email format
	if !strings.Contains(req.Email, "@") || !strings.Contains(req.Email, ".") {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
			"error": fiber.Map{
				"code":    "VALIDATION_ERROR",
				"message": "Invalid email format",
			},
		})
	}

	// Validate password strength
	if err := auth.PasswordMeetsRequirements(req.Password); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
			"error": fiber.Map{
				"code":    "WEAK_PASSWORD",
				"message": err.Error(),
			},
		})
	}

	// Generate username from email if not provided
	username := req.Username
	if username == "" {
		username = strings.Split(req.Email, "@")[0]
	}

	// Hash password with bcrypt (cost 12)
	passwordHash, err := auth.HashPassword(req.Password)
	if err != nil {
		log.Printf("Failed to hash password: %v", err)
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{
			"error": fiber.Map{
				"code":    "INTERNAL_ERROR",
				"message": "Failed to process password",
			},
		})
	}

	// Create user
	now := time.Now().UTC()
	user := models.User{
		ID:               "usr_" + uuid.New().String()[:8],
		Email:            strings.ToLower(strings.TrimSpace(req.Email)),
		Username:         strPtr(strings.ToLower(strings.TrimSpace(username))),
		PasswordHash:     passwordHash,
		FullName:         strPtr(strings.TrimSpace(req.FullName)),
		CompanyID:        strPtr("cmp_" + uuid.New().String()[:8]),
		CompanyName:      strPtr(strings.TrimSpace(req.CompanyName)),
		Role:             strPtr("user"),
		TwoFactorEnabled: false,
		EmailVerified:    false,
		IsActive:         true,
		CreatedAt:        now,
		UpdatedAt:        now,
	}

	// Save user to database
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	userRepo := repository.NewUserRepository(database.GetDB())
	if err := userRepo.Create(ctx, &user); err != nil {
		if strings.Contains(err.Error(), "duplicate key") || strings.Contains(err.Error(), "unique constraint") {
			return c.Status(fiber.StatusConflict).JSON(fiber.Map{
				"error": fiber.Map{
					"code":    "EMAIL_EXISTS",
					"message": "An account with this email already exists",
				},
			})
		}

		log.Printf("Failed to create user: %v", err)
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{
			"error": fiber.Map{
				"code":    "DATABASE_ERROR",
				"message": "Failed to create user account",
			},
		})
	}

	// Log security event
	logSecurityEvent(ctx, user.ID, "user_registered", true, c.IP(), c.Get("User-Agent"), nil)

	return c.Status(fiber.StatusCreated).JSON(fiber.Map{
		"user_id":    user.ID,
		"email":      user.Email,
		"username":   user.Username,
		"company_id": user.CompanyID,
		"created_at": user.CreatedAt.Format(time.RFC3339),
	})
}

// LoginFiber handles user authentication with 2FA support
// POST /api/v1/auth/login
func LoginFiber(c *fiber.Ctx) error {
	var req models.LoginRequest
	if err := c.BodyParser(&req); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
			"error": fiber.Map{
				"code":    "INVALID_REQUEST",
				"message": "Invalid request body",
			},
		})
	}

	if req.Email == "" || req.Password == "" {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
			"error": fiber.Map{
				"code":    "VALIDATION_ERROR",
				"message": "Email and password are required",
			},
		})
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	userRepo := repository.NewUserRepository(database.GetDB())
	user, err := userRepo.GetByEmail(ctx, strings.ToLower(strings.TrimSpace(req.Email)))
	if err != nil {
		log.Printf("Login failed for %s: %v", req.Email, err) // Debug logging
		logSecurityEvent(ctx, "", "login_failed", false, c.IP(), c.Get("User-Agent"), map[string]string{"email": req.Email, "reason": "user_not_found"})
		return c.Status(fiber.StatusUnauthorized).JSON(fiber.Map{
			"error": fiber.Map{
				"code":    "INVALID_CREDENTIALS",
				"message": "Invalid email or password",
			},
		})
	}

	// Check if account is locked
	if user.LockedUntil != nil && time.Now().Before(*user.LockedUntil) {
		logSecurityEvent(ctx, user.ID, "login_failed", false, c.IP(), c.Get("User-Agent"), map[string]string{"reason": "account_locked"})
		return c.Status(fiber.StatusTooManyRequests).JSON(fiber.Map{
			"error": fiber.Map{
				"code":    "ACCOUNT_LOCKED",
				"message": "Account is temporarily locked due to too many failed login attempts",
			},
		})
	}

	// Check if account is active
	if !user.IsActive {
		return c.Status(fiber.StatusForbidden).JSON(fiber.Map{
			"error": fiber.Map{
				"code":    "ACCOUNT_DISABLED",
				"message": "This account has been disabled",
			},
		})
	}

	// Verify password
	if !auth.CheckPasswordHash(req.Password, user.PasswordHash) {
		// Increment failed attempts
		user.FailedLoginAttempts++
		if user.FailedLoginAttempts >= maxFailedAttempts {
			lockUntil := time.Now().Add(lockoutDuration)
			user.LockedUntil = &lockUntil
		}
		_ = userRepo.UpdateLoginAttempts(ctx, user.ID, user.FailedLoginAttempts, user.LockedUntil)

		logSecurityEvent(ctx, user.ID, "login_failed", false, c.IP(), c.Get("User-Agent"), map[string]string{"reason": "invalid_password", "attempts": string(rune(user.FailedLoginAttempts))})
		return c.Status(fiber.StatusUnauthorized).JSON(fiber.Map{
			"error": fiber.Map{
				"code":    "INVALID_CREDENTIALS",
				"message": "Invalid email or password",
			},
		})
	}

	// Check 2FA if enabled
	if user.TwoFactorEnabled {
		if req.TOTPCode == "" {
			// Return indication that 2FA is required
			return c.Status(fiber.StatusOK).JSON(fiber.Map{
				"requires_two_factor": true,
				"message":            "Two-factor authentication code required",
			})
		}

		// Validate TOTP code
		if !auth.ValidateTOTPCode(req.TOTPCode, strVal(user.TwoFactorSecret)) {
			// Check backup codes
			if idx, valid := auth.ValidateBackupCode(req.TOTPCode, user.TwoFactorBackupCodes); valid {
				// Remove used backup code
				user.TwoFactorBackupCodes = append(user.TwoFactorBackupCodes[:idx], user.TwoFactorBackupCodes[idx+1:]...)
				_ = userRepo.UpdateBackupCodes(ctx, user.ID, user.TwoFactorBackupCodes)
			} else {
				logSecurityEvent(ctx, user.ID, "2fa_failed", false, c.IP(), c.Get("User-Agent"), nil)
				return c.Status(fiber.StatusUnauthorized).JSON(fiber.Map{
					"error": fiber.Map{
						"code":    "INVALID_2FA_CODE",
						"message": "Invalid two-factor authentication code",
					},
				})
			}
		}
	}

	// Reset failed attempts on successful login
	if user.FailedLoginAttempts > 0 {
		_ = userRepo.UpdateLoginAttempts(ctx, user.ID, 0, nil)
	}

	// Generate session ID for tracking
	sessionID := "ses_" + uuid.New().String()[:8]

	// Generate tokens with full claims
	accessToken, err := auth.GenerateTokenWithClaims(
		user.ID, user.Email, strVal(user.Username), strVal(user.CompanyID), strVal(user.Role), sessionID, accessTokenTTL,
	)
	if err != nil {
		log.Printf("Failed to generate access token: %v", err)
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{
			"error": fiber.Map{
				"code":    "INTERNAL_ERROR",
				"message": "Failed to generate access token",
			},
		})
	}

	refreshToken, err := auth.GenerateTokenWithClaims(
		user.ID, user.Email, strVal(user.Username), strVal(user.CompanyID), strVal(user.Role), sessionID, refreshTokenTTL,
	)
	if err != nil {
		log.Printf("Failed to generate refresh token: %v", err)
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{
			"error": fiber.Map{
				"code":    "INTERNAL_ERROR",
				"message": "Failed to generate refresh token",
			},
		})
	}

	// Create session record
	session := models.Session{
		ID:               sessionID,
		UserID:           user.ID,
		RefreshTokenHash: auth.HashToken(refreshToken),
		UserAgent:        c.Get("User-Agent"),
		IPAddress:        c.IP(),
		IsValid:          true,
		CreatedAt:        time.Now().UTC(),
		ExpiresAt:        time.Now().Add(refreshTokenTTL),
		LastUsedAt:       time.Now().UTC(),
	}
	_ = userRepo.CreateSession(ctx, &session)

	// Update last login
	now := time.Now().UTC()
	user.LastLoginAt = &now
	_ = userRepo.UpdateLastLogin(ctx, user.ID, now)

	logSecurityEvent(ctx, user.ID, "login_success", true, c.IP(), c.Get("User-Agent"), nil)

	return c.JSON(models.LoginResponse{
		AccessToken:  accessToken,
		RefreshToken: refreshToken,
		TokenType:    "Bearer",
		ExpiresIn:    int(accessTokenTTL.Seconds()),
		User:         user.ToProfile(),
	})
}

// LogoutFiber handles user logout and session invalidation
// POST /api/v1/auth/logout
func LogoutFiber(c *fiber.Ctx) error {
	userID := c.Locals("user_id")
	if userID == nil {
		return c.Status(fiber.StatusUnauthorized).JSON(fiber.Map{
			"error": fiber.Map{
				"code":    "UNAUTHORIZED",
				"message": "Authentication required",
			},
		})
	}

	// Get the current token to blacklist it
	authHeader := c.Get("Authorization")
	if authHeader != "" {
		parts := strings.Split(authHeader, " ")
		if len(parts) == 2 {
			token := parts[1]
			claims, _ := auth.ValidateToken(token)
			if claims != nil {
				ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
				defer cancel()

				userRepo := repository.NewUserRepository(database.GetDB())

				// Invalidate session if session ID is in token
				if claims.SessionID != "" {
					_ = userRepo.InvalidateSession(ctx, claims.SessionID)
				}

				// Blacklist the token
				_ = userRepo.BlacklistToken(ctx, auth.HashToken(token), userID.(string), claims.ExpiresAt.Time)

				logSecurityEvent(ctx, userID.(string), "logout", true, c.IP(), c.Get("User-Agent"), nil)
			}
		}
	}

	return c.JSON(fiber.Map{
		"message": "Successfully logged out",
	})
}

// RefreshTokenFiber handles token refresh with rotation
// POST /api/v1/auth/refresh
func RefreshTokenFiber(c *fiber.Ctx) error {
	var req models.RefreshTokenRequest
	if err := c.BodyParser(&req); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
			"error": fiber.Map{
				"code":    "INVALID_REQUEST",
				"message": "Invalid request body",
			},
		})
	}

	if req.RefreshToken == "" {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
			"error": fiber.Map{
				"code":    "VALIDATION_ERROR",
				"message": "Refresh token is required",
			},
		})
	}

	// Validate refresh token
	claims, err := auth.ValidateToken(req.RefreshToken)
	if err != nil {
		return c.Status(fiber.StatusUnauthorized).JSON(fiber.Map{
			"error": fiber.Map{
				"code":    "INVALID_TOKEN",
				"message": "Invalid or expired refresh token",
			},
		})
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	userRepo := repository.NewUserRepository(database.GetDB())

	// Check if token is blacklisted
	if userRepo.IsTokenBlacklisted(ctx, auth.HashToken(req.RefreshToken)) {
		return c.Status(fiber.StatusUnauthorized).JSON(fiber.Map{
			"error": fiber.Map{
				"code":    "TOKEN_REVOKED",
				"message": "Token has been revoked",
			},
		})
	}

	// Verify session is still valid
	if claims.SessionID != "" {
		session, err := userRepo.GetSession(ctx, claims.SessionID)
		if err != nil || !session.IsValid {
			return c.Status(fiber.StatusUnauthorized).JSON(fiber.Map{
				"error": fiber.Map{
					"code":    "SESSION_INVALID",
					"message": "Session has been invalidated",
				},
			})
		}
	}

	// Get user to ensure account is still active
	user, err := userRepo.GetByID(ctx, claims.UserID)
	if err != nil || !user.IsActive {
		return c.Status(fiber.StatusUnauthorized).JSON(fiber.Map{
			"error": fiber.Map{
				"code":    "ACCOUNT_INVALID",
				"message": "Account not found or disabled",
			},
		})
	}

	// Generate new session ID for token rotation
	newSessionID := "ses_" + uuid.New().String()[:8]

	// Generate new access token
	accessToken, err := auth.GenerateTokenWithClaims(
		claims.UserID, claims.Email, strVal(user.Username), claims.CompanyID, strVal(user.Role), newSessionID, accessTokenTTL,
	)
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{
			"error": fiber.Map{
				"code":    "INTERNAL_ERROR",
				"message": "Failed to generate access token",
			},
		})
	}

	// Token rotation: generate new refresh token and blacklist the old one
	newRefreshToken, err := auth.GenerateTokenWithClaims(
		claims.UserID, claims.Email, strVal(user.Username), claims.CompanyID, strVal(user.Role), newSessionID, refreshTokenTTL,
	)
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{
			"error": fiber.Map{
				"code":    "INTERNAL_ERROR",
				"message": "Failed to generate refresh token",
			},
		})
	}

	// Blacklist old refresh token
	_ = userRepo.BlacklistToken(ctx, auth.HashToken(req.RefreshToken), claims.UserID, claims.ExpiresAt.Time)

	// Create new session
	session := models.Session{
		ID:               newSessionID,
		UserID:           claims.UserID,
		RefreshTokenHash: auth.HashToken(newRefreshToken),
		UserAgent:        c.Get("User-Agent"),
		IPAddress:        c.IP(),
		IsValid:          true,
		CreatedAt:        time.Now().UTC(),
		ExpiresAt:        time.Now().Add(refreshTokenTTL),
		LastUsedAt:       time.Now().UTC(),
	}
	_ = userRepo.CreateSession(ctx, &session)

	// Invalidate old session
	if claims.SessionID != "" {
		_ = userRepo.InvalidateSession(ctx, claims.SessionID)
	}

	return c.JSON(models.RefreshTokenResponse{
		AccessToken:  accessToken,
		RefreshToken: newRefreshToken,
		ExpiresIn:    int(accessTokenTTL.Seconds()),
	})
}

// GetMeFiber returns the current authenticated user's complete profile
// GET /api/v1/auth/me
func GetMeFiber(c *fiber.Ctx) error {
	userID := c.Locals("user_id")
	if userID == nil {
		return c.Status(fiber.StatusUnauthorized).JSON(fiber.Map{
			"error": fiber.Map{
				"code":    "UNAUTHORIZED",
				"message": "Authentication required",
			},
		})
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	userRepo := repository.NewUserRepository(database.GetDB())
	user, err := userRepo.GetByID(ctx, userID.(string))
	if err != nil {
		return c.Status(fiber.StatusNotFound).JSON(fiber.Map{
			"error": fiber.Map{
				"code":    "NOT_FOUND",
				"message": "User not found",
			},
		})
	}

	// Return safe user profile (no sensitive fields)
	return c.JSON(user.ToProfile())
}

// UpdateProfileFiber updates the current user's profile
// PUT /api/v1/auth/me
func UpdateProfileFiber(c *fiber.Ctx) error {
	userID := c.Locals("user_id")
	if userID == nil {
		return c.Status(fiber.StatusUnauthorized).JSON(fiber.Map{
			"error": fiber.Map{
				"code":    "UNAUTHORIZED",
				"message": "Authentication required",
			},
		})
	}

	var req models.UpdateProfileRequest
	if err := c.BodyParser(&req); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
			"error": fiber.Map{
				"code":    "INVALID_REQUEST",
				"message": "Invalid request body",
			},
		})
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	userRepo := repository.NewUserRepository(database.GetDB())
	
	// Get existing user
	user, err := userRepo.GetByID(ctx, userID.(string))
	if err != nil {
		return c.Status(fiber.StatusNotFound).JSON(fiber.Map{
			"error": fiber.Map{
				"code":    "NOT_FOUND",
				"message": "User not found",
			},
		})
	}

	// Update fields if provided
	if req.FullName != "" {
		user.FullName = &req.FullName
	}
	if req.Username != "" {
		user.Username = &req.Username
	}
	if req.CompanyName != "" {
		user.CompanyName = &req.CompanyName
	}
	if req.JobTitle != "" {
		user.JobTitle = &req.JobTitle
	}
	if req.Phone != "" {
		user.Phone = &req.Phone
	}

	// Save updates
	if err := userRepo.Update(ctx, user); err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{
			"error": fiber.Map{
				"code":    "UPDATE_FAILED",
				"message": "Failed to update profile",
			},
		})
	}

	// Return updated profile
	return c.JSON(fiber.Map{
		"message": "Profile updated successfully",
		"user":    user.ToProfile(),
	})
}

// ChangePasswordFiber handles password change for authenticated users
// POST /api/v1/auth/change-password
func ChangePasswordFiber(c *fiber.Ctx) error {
	userID := c.Locals("user_id")
	if userID == nil {
		return c.Status(fiber.StatusUnauthorized).JSON(fiber.Map{
			"error": fiber.Map{
				"code":    "UNAUTHORIZED",
				"message": "Authentication required",
			},
		})
	}

	var req models.ChangePasswordRequest
	if err := c.BodyParser(&req); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
			"error": fiber.Map{
				"code":    "INVALID_REQUEST",
				"message": "Invalid request body",
			},
		})
	}

	if req.CurrentPassword == "" || req.NewPassword == "" {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
			"error": fiber.Map{
				"code":    "VALIDATION_ERROR",
				"message": "Current password and new password are required",
			},
		})
	}

	// Validate new password strength
	if err := auth.PasswordMeetsRequirements(req.NewPassword); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
			"error": fiber.Map{
				"code":    "WEAK_PASSWORD",
				"message": err.Error(),
			},
		})
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	userRepo := repository.NewUserRepository(database.GetDB())
	user, err := userRepo.GetByID(ctx, userID.(string))
	if err != nil {
		return c.Status(fiber.StatusNotFound).JSON(fiber.Map{
			"error": fiber.Map{
				"code":    "NOT_FOUND",
				"message": "User not found",
			},
		})
	}

	// Verify current password
	if !auth.CheckPasswordHash(req.CurrentPassword, user.PasswordHash) {
		logSecurityEvent(ctx, userID.(string), "password_change_failed", false, c.IP(), c.Get("User-Agent"), map[string]string{"reason": "invalid_current_password"})
		return c.Status(fiber.StatusUnauthorized).JSON(fiber.Map{
			"error": fiber.Map{
				"code":    "INVALID_PASSWORD",
				"message": "Current password is incorrect",
			},
		})
	}

	// Hash new password
	newHash, err := auth.HashPassword(req.NewPassword)
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{
			"error": fiber.Map{
				"code":    "INTERNAL_ERROR",
				"message": "Failed to process new password",
			},
		})
	}

	// Update password
	now := time.Now().UTC()
	if err := userRepo.UpdatePassword(ctx, userID.(string), newHash, now); err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{
			"error": fiber.Map{
				"code":    "DATABASE_ERROR",
				"message": "Failed to update password",
			},
		})
	}

	// Invalidate all existing sessions except current
	_ = userRepo.InvalidateAllUserSessions(ctx, userID.(string))

	logSecurityEvent(ctx, userID.(string), "password_changed", true, c.IP(), c.Get("User-Agent"), nil)

	// Create notification
	_ = userRepo.CreateNotification(ctx, &models.Notification{
		ID:        "ntf_" + uuid.New().String()[:8],
		UserID:    userID.(string),
		Type:      "security",
		Title:     "Password Changed",
		Message:   "Your password was successfully changed. If you didn't make this change, please contact support immediately.",
		CreatedAt: now,
	})

	return c.JSON(fiber.Map{
		"message": "Password changed successfully. Please log in again with your new password.",
	})
}

// ForgotPasswordFiber initiates password reset flow
// POST /api/v1/auth/forgot-password
func ForgotPasswordFiber(c *fiber.Ctx) error {
	var req models.ForgotPasswordRequest
	if err := c.BodyParser(&req); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
			"error": fiber.Map{
				"code":    "INVALID_REQUEST",
				"message": "Invalid request body",
			},
		})
	}

	if req.Email == "" {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
			"error": fiber.Map{
				"code":    "VALIDATION_ERROR",
				"message": "Email is required",
			},
		})
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	userRepo := repository.NewUserRepository(database.GetDB())

	// Always return success to prevent email enumeration
	// But only actually create token if user exists
	user, err := userRepo.GetByEmail(ctx, strings.ToLower(strings.TrimSpace(req.Email)))
	if err == nil && user.IsActive {
		// Generate reset token
		token, err := auth.GenerateSecureToken(32)
		if err == nil {
			resetToken := models.PasswordResetToken{
				ID:        "rst_" + uuid.New().String()[:8],
				UserID:    user.ID,
				TokenHash: auth.HashToken(token),
				ExpiresAt: time.Now().Add(resetTokenTTL),
				CreatedAt: time.Now().UTC(),
			}

			if err := userRepo.CreatePasswordResetToken(ctx, &resetToken); err == nil {
				// In production, send email with reset link
				// For now, log the token (remove in production!)
				log.Printf("Password reset token for %s: %s", user.Email, token)

				logSecurityEvent(ctx, user.ID, "password_reset_requested", true, c.IP(), c.Get("User-Agent"), nil)
			}
		}
	}

	return c.JSON(fiber.Map{
		"message": "If an account with that email exists, a password reset link has been sent.",
	})
}

// ResetPasswordFiber completes password reset with token
// POST /api/v1/auth/reset-password
func ResetPasswordFiber(c *fiber.Ctx) error {
	var req models.ResetPasswordRequest
	if err := c.BodyParser(&req); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
			"error": fiber.Map{
				"code":    "INVALID_REQUEST",
				"message": "Invalid request body",
			},
		})
	}

	if req.Token == "" || req.NewPassword == "" {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
			"error": fiber.Map{
				"code":    "VALIDATION_ERROR",
				"message": "Token and new password are required",
			},
		})
	}

	// Validate password strength
	if err := auth.PasswordMeetsRequirements(req.NewPassword); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
			"error": fiber.Map{
				"code":    "WEAK_PASSWORD",
				"message": err.Error(),
			},
		})
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	userRepo := repository.NewUserRepository(database.GetDB())

	// Find valid reset token
	tokenHash := auth.HashToken(req.Token)
	resetToken, err := userRepo.GetPasswordResetToken(ctx, tokenHash)
	if err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
			"error": fiber.Map{
				"code":    "INVALID_TOKEN",
				"message": "Invalid or expired reset token",
			},
		})
	}

	// Check if token is expired
	if time.Now().After(resetToken.ExpiresAt) {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
			"error": fiber.Map{
				"code":    "TOKEN_EXPIRED",
				"message": "Reset token has expired. Please request a new one.",
			},
		})
	}

	// Check if token was already used
	if resetToken.UsedAt != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
			"error": fiber.Map{
				"code":    "TOKEN_USED",
				"message": "Reset token has already been used",
			},
		})
	}

	// Hash new password
	newHash, err := auth.HashPassword(req.NewPassword)
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{
			"error": fiber.Map{
				"code":    "INTERNAL_ERROR",
				"message": "Failed to process new password",
			},
		})
	}

	// Update password
	now := time.Now().UTC()
	if err := userRepo.UpdatePassword(ctx, resetToken.UserID, newHash, now); err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{
			"error": fiber.Map{
				"code":    "DATABASE_ERROR",
				"message": "Failed to update password",
			},
		})
	}

	// Mark token as used
	_ = userRepo.MarkResetTokenUsed(ctx, resetToken.ID, now)

	// Invalidate all sessions
	_ = userRepo.InvalidateAllUserSessions(ctx, resetToken.UserID)

	// Reset failed login attempts
	_ = userRepo.UpdateLoginAttempts(ctx, resetToken.UserID, 0, nil)

	logSecurityEvent(ctx, resetToken.UserID, "password_reset_completed", true, c.IP(), c.Get("User-Agent"), nil)

	return c.JSON(fiber.Map{
		"message": "Password has been reset successfully. Please log in with your new password.",
	})
}

// Helper function to log security events
func logSecurityEvent(ctx context.Context, userID, eventType string, success bool, ipAddress, userAgent string, details map[string]string) {
	userRepo := repository.NewUserRepository(database.GetDB())
	var detailsJSON *string
	if details != nil {
		if b, err := json.Marshal(details); err == nil {
			s := string(b)
			detailsJSON = &s
		}
	}
	_ = userRepo.LogSecurityEvent(ctx, &models.SecurityEvent{
		UserID:    userID,
		EventType: eventType,
		Success:   success,
		IPAddress: ipAddress,
		UserAgent: userAgent,
		Details:   detailsJSON,
		CreatedAt: time.Now().UTC(),
	})
}

// AdminResetPasswordFiber - TEMPORARY endpoint for admin password reset
// This should be removed or properly secured in production
// POST /api/v1/auth/admin-reset
func AdminResetPasswordFiber(c *fiber.Ctx) error {
	type ResetReq struct {
		AdminKey    string `json:"admin_key"`
		Email       string `json:"email"`
		NewPassword string `json:"new_password"`
	}

	var req ResetReq
	if err := c.BodyParser(&req); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "Invalid request"})
	}

	// Simple admin key check - replace with proper auth in production
	if req.AdminKey != "SynthOS2024AdminKey!" {
		return c.Status(fiber.StatusUnauthorized).JSON(fiber.Map{"error": "Invalid admin key"})
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	userRepo := repository.NewUserRepository(database.GetDB())
	user, err := userRepo.GetByEmail(ctx, strings.ToLower(strings.TrimSpace(req.Email)))
	if err != nil {
		return c.Status(fiber.StatusNotFound).JSON(fiber.Map{"error": "User not found"})
	}

	// Hash new password
	passwordHash, err := auth.HashPassword(req.NewPassword)
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "Failed to hash password"})
	}

	// Update password
	err = userRepo.UpdatePassword(ctx, user.ID, passwordHash, time.Now().UTC())
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "Failed to update password"})
	}

	// Reset failed login attempts
	_ = userRepo.UpdateLoginAttempts(ctx, user.ID, 0, nil)

	return c.JSON(fiber.Map{
		"success": true,
		"message": "Password reset successfully",
		"user_id": user.ID,
	})
}

// DebugDBFiber - TEMPORARY debug endpoint for checking database state
// GET /api/v1/auth/debug-db
func DebugDBFiber(c *fiber.Ctx) error {
	adminKey := c.Query("key")
	if adminKey != "SynthOS2024AdminKey!" {
		return c.Status(fiber.StatusUnauthorized).JSON(fiber.Map{"error": "Invalid key"})
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	db := database.GetDB()
	
	// Check table columns
	var columns []string
	rows, err := db.Query(ctx, `
		SELECT column_name 
		FROM information_schema.columns 
		WHERE table_name = 'users' 
		ORDER BY ordinal_position
	`)
	if err != nil {
		return c.JSON(fiber.Map{"error": err.Error()})
	}
	defer rows.Close()
	
	for rows.Next() {
		var col string
		rows.Scan(&col)
		columns = append(columns, col)
	}

	// Check user count
	var userCount int
	db.QueryRow(ctx, "SELECT COUNT(*) FROM users").Scan(&userCount)

	// Try to get a sample user (just email and id)
	var sampleEmail, sampleID string
	db.QueryRow(ctx, "SELECT id, email FROM users LIMIT 1").Scan(&sampleID, &sampleEmail)

	return c.JSON(fiber.Map{
		"columns":      columns,
		"user_count":   userCount,
		"sample_user":  fiber.Map{"id": sampleID, "email": sampleEmail},
	})
}
