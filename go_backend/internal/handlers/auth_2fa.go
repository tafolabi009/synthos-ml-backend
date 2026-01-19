package handlers

import (
	"context"
	"time"

	"github.com/gofiber/fiber/v2"
	"github.com/google/uuid"
	"github.com/tafolabi009/backend/go_backend/internal/auth"
	"github.com/tafolabi009/backend/go_backend/internal/models"
	"github.com/tafolabi009/backend/go_backend/internal/repository"
	"github.com/tafolabi009/backend/go_backend/pkg/database"
)

// TwoFactorSetupFiber initiates 2FA setup for the authenticated user
// POST /api/v1/auth/2fa/setup
func TwoFactorSetupFiber(c *fiber.Ctx) error {
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

	if user.TwoFactorEnabled {
		return c.Status(fiber.StatusConflict).JSON(fiber.Map{
			"error": fiber.Map{
				"code":    "2FA_ALREADY_ENABLED",
				"message": "Two-factor authentication is already enabled",
			},
		})
	}

	// Generate TOTP secret
	secret, qrURL, err := auth.GenerateTOTPSecret(user.Email)
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{
			"error": fiber.Map{
				"code":    "INTERNAL_ERROR",
				"message": "Failed to generate 2FA secret",
			},
		})
	}

	// Generate backup codes
	backupCodes, err := auth.GenerateBackupCodes(10)
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{
			"error": fiber.Map{
				"code":    "INTERNAL_ERROR",
				"message": "Failed to generate backup codes",
			},
		})
	}

	// Store secret temporarily (will be confirmed on verify)
	// In production, store encrypted and with expiration
	if err := userRepo.StorePending2FASecret(ctx, userID.(string), secret, auth.HashBackupCodes(backupCodes)); err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{
			"error": fiber.Map{
				"code":    "DATABASE_ERROR",
				"message": "Failed to store 2FA setup data",
			},
		})
	}

	logSecurityEvent(ctx, userID.(string), "2fa_setup_initiated", true, c.IP(), c.Get("User-Agent"), nil)

	// Return setup data - secret and backup codes shown only once
	return c.JSON(models.TwoFactorSetupResponse{
		Secret:      secret,
		QRCodeURL:   qrURL,
		BackupCodes: backupCodes,
	})
}

// TwoFactorVerifyFiber verifies and completes 2FA setup
// POST /api/v1/auth/2fa/verify
func TwoFactorVerifyFiber(c *fiber.Ctx) error {
	userID := c.Locals("user_id")
	if userID == nil {
		return c.Status(fiber.StatusUnauthorized).JSON(fiber.Map{
			"error": fiber.Map{
				"code":    "UNAUTHORIZED",
				"message": "Authentication required",
			},
		})
	}

	var req models.TwoFactorVerifyRequest
	if err := c.BodyParser(&req); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
			"error": fiber.Map{
				"code":    "INVALID_REQUEST",
				"message": "Invalid request body",
			},
		})
	}

	if req.Code == "" {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
			"error": fiber.Map{
				"code":    "VALIDATION_ERROR",
				"message": "Verification code is required",
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

	if user.TwoFactorEnabled {
		return c.Status(fiber.StatusConflict).JSON(fiber.Map{
			"error": fiber.Map{
				"code":    "2FA_ALREADY_ENABLED",
				"message": "Two-factor authentication is already enabled",
			},
		})
	}

	// Get pending 2FA secret
	pendingSecret, backupCodes, err := userRepo.GetPending2FASecret(ctx, userID.(string))
	if err != nil || pendingSecret == "" {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
			"error": fiber.Map{
				"code":    "NO_PENDING_2FA",
				"message": "No pending 2FA setup found. Please initiate setup first.",
			},
		})
	}

	// Validate the provided code
	if !auth.ValidateTOTPCode(req.Code, pendingSecret) {
		logSecurityEvent(ctx, userID.(string), "2fa_verify_failed", false, c.IP(), c.Get("User-Agent"), nil)
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
			"error": fiber.Map{
				"code":    "INVALID_CODE",
				"message": "Invalid verification code",
			},
		})
	}

	// Enable 2FA
	if err := userRepo.Enable2FA(ctx, userID.(string), pendingSecret, backupCodes); err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{
			"error": fiber.Map{
				"code":    "DATABASE_ERROR",
				"message": "Failed to enable 2FA",
			},
		})
	}

	logSecurityEvent(ctx, userID.(string), "2fa_enabled", true, c.IP(), c.Get("User-Agent"), nil)

	// Create notification
	_ = userRepo.CreateNotification(ctx, &models.Notification{
		ID:        "ntf_" + uuid.New().String()[:8],
		UserID:    userID.(string),
		Type:      "security",
		Title:     "Two-Factor Authentication Enabled",
		Message:   "Two-factor authentication has been enabled for your account. You will now need to enter a code from your authenticator app when signing in.",
		CreatedAt: time.Now().UTC(),
	})

	return c.JSON(fiber.Map{
		"message":            "Two-factor authentication has been enabled successfully",
		"two_factor_enabled": true,
	})
}

// TwoFactorDisableFiber disables 2FA for the authenticated user
// POST /api/v1/auth/2fa/disable
func TwoFactorDisableFiber(c *fiber.Ctx) error {
	userID := c.Locals("user_id")
	if userID == nil {
		return c.Status(fiber.StatusUnauthorized).JSON(fiber.Map{
			"error": fiber.Map{
				"code":    "UNAUTHORIZED",
				"message": "Authentication required",
			},
		})
	}

	var req models.TwoFactorDisableRequest
	if err := c.BodyParser(&req); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
			"error": fiber.Map{
				"code":    "INVALID_REQUEST",
				"message": "Invalid request body",
			},
		})
	}

	if req.Password == "" || req.Code == "" {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
			"error": fiber.Map{
				"code":    "VALIDATION_ERROR",
				"message": "Password and verification code are required",
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

	if !user.TwoFactorEnabled {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
			"error": fiber.Map{
				"code":    "2FA_NOT_ENABLED",
				"message": "Two-factor authentication is not enabled",
			},
		})
	}

	// Verify password
	if !auth.CheckPasswordHash(req.Password, user.PasswordHash) {
		logSecurityEvent(ctx, userID.(string), "2fa_disable_failed", false, c.IP(), c.Get("User-Agent"), map[string]string{"reason": "invalid_password"})
		return c.Status(fiber.StatusUnauthorized).JSON(fiber.Map{
			"error": fiber.Map{
				"code":    "INVALID_PASSWORD",
				"message": "Invalid password",
			},
		})
	}

	// Verify TOTP code or backup code
	secret := ""
	if user.TwoFactorSecret != nil {
		secret = *user.TwoFactorSecret
	}
	valid := auth.ValidateTOTPCode(req.Code, secret)
	if !valid {
		if _, valid = auth.ValidateBackupCode(req.Code, user.TwoFactorBackupCodes); !valid {
			logSecurityEvent(ctx, userID.(string), "2fa_disable_failed", false, c.IP(), c.Get("User-Agent"), map[string]string{"reason": "invalid_code"})
			return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
				"error": fiber.Map{
					"code":    "INVALID_CODE",
					"message": "Invalid verification code",
				},
			})
		}
	}

	// Disable 2FA
	if err := userRepo.Disable2FA(ctx, userID.(string)); err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{
			"error": fiber.Map{
				"code":    "DATABASE_ERROR",
				"message": "Failed to disable 2FA",
			},
		})
	}

	logSecurityEvent(ctx, userID.(string), "2fa_disabled", true, c.IP(), c.Get("User-Agent"), nil)

	// Create notification
	_ = userRepo.CreateNotification(ctx, &models.Notification{
		ID:        "ntf_" + uuid.New().String()[:8],
		UserID:    userID.(string),
		Type:      "security",
		Title:     "Two-Factor Authentication Disabled",
		Message:   "Two-factor authentication has been disabled for your account. Your account is now less secure.",
		CreatedAt: time.Now().UTC(),
	})

	return c.JSON(fiber.Map{
		"message":            "Two-factor authentication has been disabled",
		"two_factor_enabled": false,
	})
}
