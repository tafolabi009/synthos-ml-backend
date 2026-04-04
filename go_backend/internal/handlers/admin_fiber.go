package handlers

import (
	"context"
	"fmt"
	"log"
	"strconv"
	"time"

	"github.com/gofiber/fiber/v2"
	"github.com/google/uuid"
	"github.com/tafolabi009/backend/go_backend/pkg/database"
	"github.com/tafolabi009/backend/go_backend/pkg/email"
)

// GetSystemOverviewFiber returns system-wide statistics for admin dashboard
func GetSystemOverviewFiber(c *fiber.Ctx) error {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	db := database.GetDB()

	var totalUsers int64
	_ = db.QueryRow(ctx, `SELECT COUNT(*) FROM users`).Scan(&totalUsers)

	var totalValidations int64
	_ = db.QueryRow(ctx, `SELECT COUNT(*) FROM validations`).Scan(&totalValidations)

	var totalDatasets int64
	_ = db.QueryRow(ctx, `SELECT COUNT(*) FROM datasets`).Scan(&totalDatasets)

	var totalCreditsPurchased int64
	err := db.QueryRow(ctx, `SELECT COALESCE(SUM(amount), 0) FROM credit_transactions WHERE type = 'purchase'`).Scan(&totalCreditsPurchased)
	if err != nil {
		totalCreditsPurchased = 0
	}

	var activeJobs int64
	_ = db.QueryRow(ctx, `SELECT COUNT(*) FROM validations WHERE status IN ('pending', 'running', 'processing')`).Scan(&activeJobs)

	// Users grouped by role
	roleRows, err := db.Query(ctx, `SELECT COALESCE(role, 'user') as role, COUNT(*) as count FROM users GROUP BY role`)
	roleCounts := make(map[string]int64)
	if err == nil {
		defer roleRows.Close()
		for roleRows.Next() {
			var role string
			var count int64
			if err := roleRows.Scan(&role, &count); err == nil {
				roleCounts[role] = count
			}
		}
	}

	return c.JSON(fiber.Map{
		"total_users":            totalUsers,
		"total_validations":      totalValidations,
		"total_datasets":         totalDatasets,
		"total_credits_purchased": totalCreditsPurchased,
		"active_jobs":            activeJobs,
		"users_by_role":          roleCounts,
	})
}

// ListUsersFiber returns a paginated list of users with optional filters
func ListUsersFiber(c *fiber.Ctx) error {
	page, _ := strconv.Atoi(c.Query("page", "1"))
	if page < 1 {
		page = 1
	}
	pageSize, _ := strconv.Atoi(c.Query("page_size", "20"))
	if pageSize < 1 || pageSize > 100 {
		pageSize = 20
	}
	offset := (page - 1) * pageSize

	search := c.Query("search", "")
	roleFilter := c.Query("role", "")
	statusFilter := c.Query("status", "")

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	db := database.GetDB()

	// Build dynamic query
	query := `SELECT id, email, COALESCE(username, ''), COALESCE(full_name, ''), COALESCE(company_name, ''),
	           COALESCE(job_title, ''), COALESCE(phone, ''), COALESCE(role, 'user'),
	           two_factor_enabled, email_verified, is_active, last_login_at, created_at, updated_at
	           FROM users WHERE 1=1`
	countQuery := `SELECT COUNT(*) FROM users WHERE 1=1`
	args := []interface{}{}
	argIdx := 1

	if search != "" {
		clause := fmt.Sprintf(` AND (email ILIKE $%d OR COALESCE(full_name, '') ILIKE $%d OR COALESCE(username, '') ILIKE $%d)`, argIdx, argIdx, argIdx)
		query += clause
		countQuery += clause
		args = append(args, "%"+search+"%")
		argIdx++
	}
	if roleFilter != "" {
		clause := fmt.Sprintf(` AND COALESCE(role, 'user') = $%d`, argIdx)
		query += clause
		countQuery += clause
		args = append(args, roleFilter)
		argIdx++
	}
	if statusFilter != "" {
		isActive := statusFilter == "active"
		clause := fmt.Sprintf(` AND is_active = $%d`, argIdx)
		query += clause
		countQuery += clause
		args = append(args, isActive)
		argIdx++
	}

	var totalCount int64
	_ = db.QueryRow(ctx, countQuery, args...).Scan(&totalCount)

	query += fmt.Sprintf(` ORDER BY created_at DESC LIMIT $%d OFFSET $%d`, argIdx, argIdx+1)
	args = append(args, pageSize, offset)

	rows, err := db.Query(ctx, query, args...)
	if err != nil {
		log.Printf("Failed to list users: %v", err)
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{
			"error": fiber.Map{"code": "DATABASE_ERROR", "message": "Failed to list users"},
		})
	}
	defer rows.Close()

	type AdminUserProfile struct {
		ID               string     `json:"id"`
		Email            string     `json:"email"`
		Username         string     `json:"username"`
		FullName         string     `json:"full_name"`
		CompanyName      string     `json:"company_name"`
		JobTitle         string     `json:"job_title"`
		Phone            string     `json:"phone"`
		Role             string     `json:"role"`
		TwoFactorEnabled bool       `json:"two_factor_enabled"`
		EmailVerified    bool       `json:"email_verified"`
		IsActive         bool       `json:"is_active"`
		LastLoginAt      *time.Time `json:"last_login_at,omitempty"`
		CreatedAt        time.Time  `json:"created_at"`
		UpdatedAt        time.Time  `json:"updated_at"`
	}

	var users []AdminUserProfile
	for rows.Next() {
		var u AdminUserProfile
		if err := rows.Scan(&u.ID, &u.Email, &u.Username, &u.FullName, &u.CompanyName,
			&u.JobTitle, &u.Phone, &u.Role,
			&u.TwoFactorEnabled, &u.EmailVerified, &u.IsActive, &u.LastLoginAt, &u.CreatedAt, &u.UpdatedAt); err != nil {
			log.Printf("Failed to scan user row: %v", err)
			continue
		}
		users = append(users, u)
	}
	if users == nil {
		users = []AdminUserProfile{}
	}

	totalPages := int((totalCount + int64(pageSize) - 1) / int64(pageSize))

	return c.JSON(fiber.Map{
		"users": users,
		"pagination": fiber.Map{
			"page":        page,
			"page_size":   pageSize,
			"total_count": totalCount,
			"total_pages": totalPages,
		},
	})
}

// GetUserDetailFiber returns detailed user info including credit balance and counts
func GetUserDetailFiber(c *fiber.Ctx) error {
	userID := c.Params("id")

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	db := database.GetDB()

	var id, email, username, fullName, companyName, jobTitle, phone, role string
	var twoFA, emailVerified, isActive bool
	var lastLoginAt *time.Time
	var createdAt, updatedAt time.Time

	err := db.QueryRow(ctx,
		`SELECT id, email, COALESCE(username, ''), COALESCE(full_name, ''), COALESCE(company_name, ''),
		 COALESCE(job_title, ''), COALESCE(phone, ''), COALESCE(role, 'user'),
		 two_factor_enabled, email_verified, is_active, last_login_at, created_at, updated_at
		 FROM users WHERE id = $1`, userID,
	).Scan(&id, &email, &username, &fullName, &companyName, &jobTitle, &phone, &role,
		&twoFA, &emailVerified, &isActive, &lastLoginAt, &createdAt, &updatedAt)
	if err != nil {
		return c.Status(fiber.StatusNotFound).JSON(fiber.Map{
			"error": fiber.Map{"code": "NOT_FOUND", "message": "User not found"},
		})
	}

	var creditBalance int64
	_ = db.QueryRow(ctx, `SELECT COALESCE(balance, 0) FROM credit_balances WHERE user_id = $1`, userID).Scan(&creditBalance)

	var validationCount int64
	_ = db.QueryRow(ctx, `SELECT COUNT(*) FROM validations WHERE user_id = $1`, userID).Scan(&validationCount)

	var datasetCount int64
	_ = db.QueryRow(ctx, `SELECT COUNT(*) FROM datasets WHERE user_id = $1`, userID).Scan(&datasetCount)

	return c.JSON(fiber.Map{
		"user": fiber.Map{
			"id":                 id,
			"email":              email,
			"username":           username,
			"full_name":          fullName,
			"company_name":       companyName,
			"job_title":          jobTitle,
			"phone":              phone,
			"role":               role,
			"two_factor_enabled": twoFA,
			"email_verified":     emailVerified,
			"is_active":          isActive,
			"last_login_at":      lastLoginAt,
			"created_at":         createdAt,
			"updated_at":         updatedAt,
		},
		"credit_balance":   creditBalance,
		"validation_count": validationCount,
		"dataset_count":    datasetCount,
	})
}

// UpdateUserRoleFiber updates a user's role
func UpdateUserRoleFiber(c *fiber.Ctx) error {
	targetUserID := c.Params("id")
	currentUserID := c.Locals("user_id").(string)

	var req struct {
		Role string `json:"role"`
	}
	if err := c.BodyParser(&req); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
			"error": fiber.Map{"code": "INVALID_REQUEST", "message": "Invalid request body"},
		})
	}

	// Validate role
	validRoles := map[string]bool{"admin": true, "developer": true, "support": true, "user": true}
	if !validRoles[req.Role] {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
			"error": fiber.Map{"code": "INVALID_ROLE", "message": "Role must be one of: admin, developer, support, user"},
		})
	}

	// Prevent self-demotion
	if targetUserID == currentUserID {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
			"error": fiber.Map{"code": "SELF_DEMOTION", "message": "You cannot change your own role"},
		})
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	db := database.GetDB()

	tag, err := db.Exec(ctx, `UPDATE users SET role = $1, updated_at = NOW() WHERE id = $2`, req.Role, targetUserID)
	if err != nil {
		log.Printf("Failed to update user role: %v", err)
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{
			"error": fiber.Map{"code": "DATABASE_ERROR", "message": "Failed to update role"},
		})
	}
	if tag.RowsAffected() == 0 {
		return c.Status(fiber.StatusNotFound).JSON(fiber.Map{
			"error": fiber.Map{"code": "NOT_FOUND", "message": "User not found"},
		})
	}

	return c.JSON(fiber.Map{"success": true, "user_id": targetUserID, "role": req.Role})
}

// UpdateUserStatusFiber activates or deactivates a user
func UpdateUserStatusFiber(c *fiber.Ctx) error {
	targetUserID := c.Params("id")

	var req struct {
		IsActive bool `json:"is_active"`
	}
	if err := c.BodyParser(&req); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
			"error": fiber.Map{"code": "INVALID_REQUEST", "message": "Invalid request body"},
		})
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	db := database.GetDB()

	tag, err := db.Exec(ctx, `UPDATE users SET is_active = $1, updated_at = NOW() WHERE id = $2`, req.IsActive, targetUserID)
	if err != nil {
		log.Printf("Failed to update user status: %v", err)
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{
			"error": fiber.Map{"code": "DATABASE_ERROR", "message": "Failed to update status"},
		})
	}
	if tag.RowsAffected() == 0 {
		return c.Status(fiber.StatusNotFound).JSON(fiber.Map{
			"error": fiber.Map{"code": "NOT_FOUND", "message": "User not found"},
		})
	}

	return c.JSON(fiber.Map{"success": true, "user_id": targetUserID, "is_active": req.IsActive})
}

// CreatePromoCodeFiber creates a new promotional code
func CreatePromoCodeFiber(c *fiber.Ctx) error {
	var req struct {
		Code         string `json:"code"`
		CreditsGrant int64  `json:"credits_grant"`
		MaxUses      int    `json:"max_uses"`
		Description  string `json:"description"`
	}
	if err := c.BodyParser(&req); err != nil || req.Code == "" {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
			"error": fiber.Map{"code": "INVALID_REQUEST", "message": "code and credits_grant are required"},
		})
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	db := database.GetDB()

	promoID := "promo_" + uuid.New().String()[:8]
	_, err := db.Exec(ctx,
		`INSERT INTO promo_codes (id, code, credits_grant, description, max_uses, current_uses, is_active, created_at, updated_at)
		 VALUES ($1, $2, $3, $4, $5, 0, true, NOW(), NOW())`,
		promoID, req.Code, req.CreditsGrant, req.Description, req.MaxUses,
	)
	if err != nil {
		log.Printf("Failed to create promo code: %v", err)
		return c.Status(fiber.StatusConflict).JSON(fiber.Map{
			"error": fiber.Map{"code": "CONFLICT", "message": "Promo code already exists or database error"},
		})
	}

	return c.Status(fiber.StatusCreated).JSON(fiber.Map{
		"id":            promoID,
		"code":          req.Code,
		"credits_grant": req.CreditsGrant,
		"max_uses":      req.MaxUses,
		"description":   req.Description,
		"is_active":     true,
	})
}

// ListPromoCodesFiber lists all promotional codes
func ListPromoCodesFiber(c *fiber.Ctx) error {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	db := database.GetDB()

	rows, err := db.Query(ctx,
		`SELECT id, code, credits_grant, COALESCE(description, ''), max_uses, current_uses, is_active, created_at, updated_at
		 FROM promo_codes ORDER BY created_at DESC`)
	if err != nil {
		log.Printf("Failed to list promo codes: %v", err)
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{
			"error": fiber.Map{"code": "DATABASE_ERROR", "message": "Failed to list promo codes"},
		})
	}
	defer rows.Close()

	type PromoCode struct {
		ID           string    `json:"id"`
		Code         string    `json:"code"`
		CreditsGrant int64     `json:"credits_grant"`
		Description  string    `json:"description"`
		MaxUses      int       `json:"max_uses"`
		CurrentUses  int       `json:"current_uses"`
		IsActive     bool      `json:"is_active"`
		CreatedAt    time.Time `json:"created_at"`
		UpdatedAt    time.Time `json:"updated_at"`
	}

	var codes []PromoCode
	for rows.Next() {
		var p PromoCode
		if err := rows.Scan(&p.ID, &p.Code, &p.CreditsGrant, &p.Description, &p.MaxUses, &p.CurrentUses, &p.IsActive, &p.CreatedAt, &p.UpdatedAt); err != nil {
			log.Printf("Failed to scan promo code: %v", err)
			continue
		}
		codes = append(codes, p)
	}
	if codes == nil {
		codes = []PromoCode{}
	}

	return c.JSON(fiber.Map{"promo_codes": codes})
}

// UpdatePromoCodeFiber updates a promo code's active status
func UpdatePromoCodeFiber(c *fiber.Ctx) error {
	promoID := c.Params("id")

	var req struct {
		IsActive bool `json:"is_active"`
	}
	if err := c.BodyParser(&req); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
			"error": fiber.Map{"code": "INVALID_REQUEST", "message": "Invalid request body"},
		})
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	db := database.GetDB()

	tag, err := db.Exec(ctx, `UPDATE promo_codes SET is_active = $1, updated_at = NOW() WHERE id = $2`, req.IsActive, promoID)
	if err != nil {
		log.Printf("Failed to update promo code: %v", err)
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{
			"error": fiber.Map{"code": "DATABASE_ERROR", "message": "Failed to update promo code"},
		})
	}
	if tag.RowsAffected() == 0 {
		return c.Status(fiber.StatusNotFound).JSON(fiber.Map{
			"error": fiber.Map{"code": "NOT_FOUND", "message": "Promo code not found"},
		})
	}

	return c.JSON(fiber.Map{"success": true, "id": promoID, "is_active": req.IsActive})
}

// ListAllValidationsFiber lists all validations across all users (admin view)
func ListAllValidationsFiber(c *fiber.Ctx) error {
	page, _ := strconv.Atoi(c.Query("page", "1"))
	if page < 1 {
		page = 1
	}
	pageSize, _ := strconv.Atoi(c.Query("page_size", "20"))
	if pageSize < 1 || pageSize > 100 {
		pageSize = 20
	}
	offset := (page - 1) * pageSize

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	db := database.GetDB()

	var totalCount int64
	_ = db.QueryRow(ctx, `SELECT COUNT(*) FROM validations`).Scan(&totalCount)

	rows, err := db.Query(ctx,
		`SELECT v.id, v.user_id, v.dataset_id, v.status, COALESCE(v.validation_type, ''), v.created_at, v.updated_at,
		        COALESCE(u.email, ''), COALESCE(u.full_name, '')
		 FROM validations v
		 LEFT JOIN users u ON v.user_id = u.id
		 ORDER BY v.created_at DESC
		 LIMIT $1 OFFSET $2`, pageSize, offset)
	if err != nil {
		log.Printf("Failed to list all validations: %v", err)
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{
			"error": fiber.Map{"code": "DATABASE_ERROR", "message": "Failed to list validations"},
		})
	}
	defer rows.Close()

	type AdminValidation struct {
		ID             string    `json:"id"`
		UserID         string    `json:"user_id"`
		DatasetID      string    `json:"dataset_id"`
		Status         string    `json:"status"`
		ValidationType string    `json:"validation_type"`
		CreatedAt      time.Time `json:"created_at"`
		UpdatedAt      time.Time `json:"updated_at"`
		UserEmail      string    `json:"user_email"`
		UserFullName   string    `json:"user_full_name"`
	}

	var validations []AdminValidation
	for rows.Next() {
		var v AdminValidation
		if err := rows.Scan(&v.ID, &v.UserID, &v.DatasetID, &v.Status, &v.ValidationType,
			&v.CreatedAt, &v.UpdatedAt, &v.UserEmail, &v.UserFullName); err != nil {
			log.Printf("Failed to scan validation: %v", err)
			continue
		}
		validations = append(validations, v)
	}
	if validations == nil {
		validations = []AdminValidation{}
	}

	totalPages := int((totalCount + int64(pageSize) - 1) / int64(pageSize))

	return c.JSON(fiber.Map{
		"validations": validations,
		"pagination": fiber.Map{
			"page":        page,
			"page_size":   pageSize,
			"total_count": totalCount,
			"total_pages": totalPages,
		},
	})
}

// ListAllDatasetsFiber lists all datasets across all users (admin view)
func ListAllDatasetsFiber(c *fiber.Ctx) error {
	page, _ := strconv.Atoi(c.Query("page", "1"))
	if page < 1 {
		page = 1
	}
	pageSize, _ := strconv.Atoi(c.Query("page_size", "20"))
	if pageSize < 1 || pageSize > 100 {
		pageSize = 20
	}
	offset := (page - 1) * pageSize

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	db := database.GetDB()

	var totalCount int64
	_ = db.QueryRow(ctx, `SELECT COUNT(*) FROM datasets`).Scan(&totalCount)

	rows, err := db.Query(ctx,
		`SELECT d.id, d.user_id, d.name, d.status, COALESCE(d.file_type, ''), d.file_size, d.created_at, d.updated_at,
		        COALESCE(u.email, ''), COALESCE(u.full_name, '')
		 FROM datasets d
		 LEFT JOIN users u ON d.user_id = u.id
		 ORDER BY d.created_at DESC
		 LIMIT $1 OFFSET $2`, pageSize, offset)
	if err != nil {
		log.Printf("Failed to list all datasets: %v", err)
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{
			"error": fiber.Map{"code": "DATABASE_ERROR", "message": "Failed to list datasets"},
		})
	}
	defer rows.Close()

	type AdminDataset struct {
		ID           string    `json:"id"`
		UserID       string    `json:"user_id"`
		Name         string    `json:"name"`
		Status       string    `json:"status"`
		FileType     string    `json:"file_type"`
		FileSize     int64     `json:"file_size"`
		CreatedAt    time.Time `json:"created_at"`
		UpdatedAt    time.Time `json:"updated_at"`
		UserEmail    string    `json:"user_email"`
		UserFullName string    `json:"user_full_name"`
	}

	var datasets []AdminDataset
	for rows.Next() {
		var d AdminDataset
		if err := rows.Scan(&d.ID, &d.UserID, &d.Name, &d.Status, &d.FileType, &d.FileSize,
			&d.CreatedAt, &d.UpdatedAt, &d.UserEmail, &d.UserFullName); err != nil {
			log.Printf("Failed to scan dataset: %v", err)
			continue
		}
		datasets = append(datasets, d)
	}
	if datasets == nil {
		datasets = []AdminDataset{}
	}

	totalPages := int((totalCount + int64(pageSize) - 1) / int64(pageSize))

	return c.JSON(fiber.Map{
		"datasets": datasets,
		"pagination": fiber.Map{
			"page":        page,
			"page_size":   pageSize,
			"total_count": totalCount,
			"total_pages": totalPages,
		},
	})
}

// CreateInviteFiber creates a team invite
func CreateInviteFiber(c *fiber.Ctx) error {
	currentUserID := c.Locals("user_id").(string)

	var req struct {
		Email string `json:"email"`
		Role  string `json:"role"`
	}
	if err := c.BodyParser(&req); err != nil || req.Email == "" {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
			"error": fiber.Map{"code": "INVALID_REQUEST", "message": "email is required"},
		})
	}

	if req.Role == "" {
		req.Role = "user"
	}
	validRoles := map[string]bool{"admin": true, "developer": true, "support": true, "user": true}
	if !validRoles[req.Role] {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
			"error": fiber.Map{"code": "INVALID_ROLE", "message": "Role must be one of: admin, developer, support, user"},
		})
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	db := database.GetDB()

	inviteID := "inv_" + uuid.New().String()[:8]
	token := uuid.New().String()
	expiresAt := time.Now().Add(7 * 24 * time.Hour)

	_, err := db.Exec(ctx,
		`INSERT INTO invites (id, email, role, invited_by, token, status, expires_at, created_at)
		 VALUES ($1, $2, $3, $4, $5, 'pending', $6, NOW())`,
		inviteID, req.Email, req.Role, currentUserID, token, expiresAt,
	)
	if err != nil {
		log.Printf("Failed to create invite: %v", err)
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{
			"error": fiber.Map{"code": "DATABASE_ERROR", "message": "Failed to create invite"},
		})
	}

	// Send invite email (best-effort)
	go func() {
		emailClient := email.GetClient()
		if emailClient.IsConfigured() {
			// Get inviter's name
			var inviterName string
			database.GetDB().QueryRow(context.Background(),
				`SELECT COALESCE(full_name, email) FROM users WHERE id = $1`, currentUserID,
			).Scan(&inviterName)

			inviteLink := fmt.Sprintf("https://synthos.dev/register?invite=%s", token)
			subject, html := email.InviteEmail(inviterName, req.Role, inviteLink)
			if err := emailClient.Send(req.Email, subject, html); err != nil {
				log.Printf("Failed to send invite email to %s: %v", req.Email, err)
			}
		} else {
			log.Printf("Email client not configured, skipping invite email for %s", req.Email)
		}
	}()

	return c.Status(fiber.StatusCreated).JSON(fiber.Map{
		"id":         inviteID,
		"email":      req.Email,
		"role":       req.Role,
		"token":      token,
		"status":     "pending",
		"expires_at": expiresAt,
	})
}

// ListInvitesFiber lists all team invites
func ListInvitesFiber(c *fiber.Ctx) error {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	db := database.GetDB()

	rows, err := db.Query(ctx,
		`SELECT i.id, i.email, i.role, i.invited_by, i.token, i.status, i.expires_at, i.created_at,
		        COALESCE(u.email, '') as inviter_email
		 FROM invites i
		 LEFT JOIN users u ON i.invited_by = u.id
		 ORDER BY i.created_at DESC`)
	if err != nil {
		log.Printf("Failed to list invites: %v", err)
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{
			"error": fiber.Map{"code": "DATABASE_ERROR", "message": "Failed to list invites"},
		})
	}
	defer rows.Close()

	type Invite struct {
		ID           string    `json:"id"`
		Email        string    `json:"email"`
		Role         string    `json:"role"`
		InvitedBy    string    `json:"invited_by"`
		Token        string    `json:"token"`
		Status       string    `json:"status"`
		ExpiresAt    time.Time `json:"expires_at"`
		CreatedAt    time.Time `json:"created_at"`
		InviterEmail string    `json:"inviter_email"`
	}

	var invites []Invite
	for rows.Next() {
		var inv Invite
		if err := rows.Scan(&inv.ID, &inv.Email, &inv.Role, &inv.InvitedBy, &inv.Token, &inv.Status,
			&inv.ExpiresAt, &inv.CreatedAt, &inv.InviterEmail); err != nil {
			log.Printf("Failed to scan invite: %v", err)
			continue
		}
		invites = append(invites, inv)
	}
	if invites == nil {
		invites = []Invite{}
	}

	return c.JSON(fiber.Map{"invites": invites})
}
