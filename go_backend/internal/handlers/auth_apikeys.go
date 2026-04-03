package handlers

import (
	"context"
	"strconv"
	"time"

	"github.com/gofiber/fiber/v2"
	"github.com/google/uuid"
	"github.com/tafolabi009/backend/go_backend/internal/auth"
	"github.com/tafolabi009/backend/go_backend/internal/models"
	"github.com/tafolabi009/backend/go_backend/internal/repository"
	"github.com/tafolabi009/backend/go_backend/pkg/database"
)

// Valid API key scopes
var validScopes = map[string]bool{
	"read:datasets":     true,
	"write:datasets":    true,
	"read:validations":  true,
	"write:validations": true,
	"read:warranties":   true,
	"write:warranties":  true,
	"read:analytics":    true,
	"admin":             true,
}

// CreateAPIKeyFiber creates a new API key for the authenticated user
// POST /api/v1/api-keys
func CreateAPIKeyFiber(c *fiber.Ctx) error {
	userID := c.Locals("user_id")
	if userID == nil {
		return c.Status(fiber.StatusUnauthorized).JSON(fiber.Map{
			"error": fiber.Map{
				"code":    "UNAUTHORIZED",
				"message": "Authentication required",
			},
		})
	}

	var req models.APIKeyCreateRequest
	if err := c.BodyParser(&req); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
			"error": fiber.Map{
				"code":    "INVALID_REQUEST",
				"message": "Invalid request body",
			},
		})
	}

	if req.Name == "" {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
			"error": fiber.Map{
				"code":    "VALIDATION_ERROR",
				"message": "API key name is required",
			},
		})
	}

	if len(req.Scopes) == 0 {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
			"error": fiber.Map{
				"code":    "VALIDATION_ERROR",
				"message": "At least one scope is required",
			},
		})
	}

	// Validate scopes
	for _, scope := range req.Scopes {
		if !validScopes[scope] {
			return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
				"error": fiber.Map{
					"code":    "INVALID_SCOPE",
					"message": "Invalid scope: " + scope,
				},
			})
		}
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// Generate API key
	fullKey, prefix, keyHash, err := auth.GenerateAPIKey()
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{
			"error": fiber.Map{
				"code":    "INTERNAL_ERROR",
				"message": "Failed to generate API key",
			},
		})
	}

	now := time.Now().UTC()
	apiKey := models.APIKey{
		ID:        "key_" + uuid.New().String()[:8],
		UserID:    userID.(string),
		Name:      req.Name,
		KeyPrefix: prefix,
		KeyHash:   keyHash,
		Scopes:    req.Scopes,
		RateLimit: 1000, // Default rate limit
		IsActive:  true,
		CreatedAt: now,
	}

	// Set expiration if specified
	if req.ExpiresIn > 0 {
		expiresAt := now.Add(time.Duration(req.ExpiresIn) * 24 * time.Hour)
		apiKey.ExpiresAt = &expiresAt
	}

	userRepo := repository.NewUserRepository(database.GetDB())
	if err := userRepo.CreateAPIKey(ctx, &apiKey); err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{
			"error": fiber.Map{
				"code":    "DATABASE_ERROR",
				"message": "Failed to create API key",
			},
		})
	}

	logSecurityEvent(ctx, userID.(string), "api_key_created", true, c.IP(), c.Get("User-Agent"), map[string]string{"key_id": apiKey.ID})

	// Return the full key only once - it cannot be retrieved again
	return c.Status(fiber.StatusCreated).JSON(models.APIKeyCreateResponse{
		ID:        apiKey.ID,
		Name:      apiKey.Name,
		Key:       fullKey, // Only time the full key is shown
		KeyPrefix: apiKey.KeyPrefix,
		Scopes:    apiKey.Scopes,
		ExpiresAt: apiKey.ExpiresAt,
		CreatedAt: apiKey.CreatedAt,
	})
}

// ListAPIKeysFiber lists all API keys for the authenticated user
// GET /api/v1/api-keys
func ListAPIKeysFiber(c *fiber.Ctx) error {
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
	keys, err := userRepo.ListAPIKeys(ctx, userID.(string))
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{
			"error": fiber.Map{
				"code":    "DATABASE_ERROR",
				"message": "Failed to retrieve API keys",
			},
		})
	}

	// Keys are returned without the full key or hash - only prefix for identification
	return c.JSON(fiber.Map{
		"api_keys": keys,
		"total":    len(keys),
	})
}

// DeleteAPIKeyFiber revokes an API key
// DELETE /api/v1/api-keys/:id
func DeleteAPIKeyFiber(c *fiber.Ctx) error {
	userID := c.Locals("user_id")
	if userID == nil {
		return c.Status(fiber.StatusUnauthorized).JSON(fiber.Map{
			"error": fiber.Map{
				"code":    "UNAUTHORIZED",
				"message": "Authentication required",
			},
		})
	}

	keyID := c.Params("id")
	if keyID == "" {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
			"error": fiber.Map{
				"code":    "VALIDATION_ERROR",
				"message": "API key ID is required",
			},
		})
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	userRepo := repository.NewUserRepository(database.GetDB())

	// Verify key belongs to user
	key, err := userRepo.GetAPIKey(ctx, keyID)
	if err != nil {
		return c.Status(fiber.StatusNotFound).JSON(fiber.Map{
			"error": fiber.Map{
				"code":    "NOT_FOUND",
				"message": "API key not found",
			},
		})
	}

	if key.UserID != userID.(string) {
		return c.Status(fiber.StatusForbidden).JSON(fiber.Map{
			"error": fiber.Map{
				"code":    "FORBIDDEN",
				"message": "You do not have permission to delete this API key",
			},
		})
	}

	// Revoke the key
	if err := userRepo.RevokeAPIKey(ctx, keyID); err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{
			"error": fiber.Map{
				"code":    "DATABASE_ERROR",
				"message": "Failed to revoke API key",
			},
		})
	}

	logSecurityEvent(ctx, userID.(string), "api_key_revoked", true, c.IP(), c.Get("User-Agent"), map[string]string{"key_id": keyID})

	return c.JSON(fiber.Map{
		"message": "API key has been revoked",
	})
}

// GetNotificationsFiber retrieves notifications for the authenticated user
// GET /api/v1/notifications
func GetNotificationsFiber(c *fiber.Ctx) error {
	userID := c.Locals("user_id")
	if userID == nil {
		return c.Status(fiber.StatusUnauthorized).JSON(fiber.Map{
			"error": fiber.Map{
				"code":    "UNAUTHORIZED",
				"message": "Authentication required",
			},
		})
	}

	// Parse pagination parameters
	page, _ := strconv.Atoi(c.Query("page", "1"))
	perPage, _ := strconv.Atoi(c.Query("per_page", "20"))
	unreadOnly := c.Query("unread_only", "false") == "true"

	if page < 1 {
		page = 1
	}
	if perPage < 1 || perPage > 100 {
		perPage = 20
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	userRepo := repository.NewUserRepository(database.GetDB())
	notifications, total, err := userRepo.ListNotifications(ctx, userID.(string), page, perPage, unreadOnly)
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{
			"error": fiber.Map{
				"code":    "DATABASE_ERROR",
				"message": "Failed to retrieve notifications",
			},
		})
	}

	// Calculate unread count
	unreadCount, _ := userRepo.GetUnreadNotificationCount(ctx, userID.(string))

	totalPages := int(total) / perPage
	if int(total)%perPage > 0 {
		totalPages++
	}

	return c.JSON(fiber.Map{
		"notifications": notifications,
		"total":         total,
		"unread_count":  unreadCount,
		"page":          page,
		"per_page":      perPage,
		"total_pages":   totalPages,
	})
}

// MarkNotificationsReadFiber marks notifications as read
// POST /api/v1/notifications/read
func MarkNotificationsReadFiber(c *fiber.Ctx) error {
	userID := c.Locals("user_id")
	if userID == nil {
		return c.Status(fiber.StatusUnauthorized).JSON(fiber.Map{
			"error": fiber.Map{
				"code":    "UNAUTHORIZED",
				"message": "Authentication required",
			},
		})
	}

	var req models.NotificationReadRequest
	if err := c.BodyParser(&req); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
			"error": fiber.Map{
				"code":    "INVALID_REQUEST",
				"message": "Invalid request body",
			},
		})
	}

	if len(req.NotificationIDs) == 0 {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
			"error": fiber.Map{
				"code":    "VALIDATION_ERROR",
				"message": "At least one notification ID is required",
			},
		})
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	userRepo := repository.NewUserRepository(database.GetDB())
	readCount, err := userRepo.MarkNotificationsRead(ctx, userID.(string), req.NotificationIDs)
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{
			"error": fiber.Map{
				"code":    "DATABASE_ERROR",
				"message": "Failed to mark notifications as read",
			},
		})
	}

	return c.JSON(fiber.Map{
		"message":    "Notifications marked as read",
		"read_count": readCount,
	})
}
