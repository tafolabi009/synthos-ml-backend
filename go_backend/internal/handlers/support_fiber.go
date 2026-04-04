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
	"github.com/tafolabi009/backend/go_backend/pkg/sanitize"
)

// GetSupportOverviewFiber returns support dashboard overview
func GetSupportOverviewFiber(c *fiber.Ctx) error {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	db := database.GetDB()

	// Count tickets by status
	statusRows, err := db.Query(ctx, `SELECT status, COUNT(*) FROM support_tickets GROUP BY status`)
	ticketsByStatus := make(map[string]int64)
	if err == nil {
		defer statusRows.Close()
		for statusRows.Next() {
			var status string
			var count int64
			if err := statusRows.Scan(&status, &count); err == nil {
				ticketsByStatus[status] = count
			}
		}
	}

	// Recent tickets
	recentRows, err := db.Query(ctx,
		`SELECT t.id, t.subject, t.status, t.priority, t.created_at, COALESCE(u.email, '')
		 FROM support_tickets t
		 LEFT JOIN users u ON t.user_id = u.id
		 ORDER BY t.created_at DESC LIMIT 10`)

	type RecentTicket struct {
		ID        string    `json:"id"`
		Subject   string    `json:"subject"`
		Status    string    `json:"status"`
		Priority  string    `json:"priority"`
		CreatedAt time.Time `json:"created_at"`
		UserEmail string    `json:"user_email"`
	}

	var recentTickets []RecentTicket
	if err == nil {
		defer recentRows.Close()
		for recentRows.Next() {
			var t RecentTicket
			if err := recentRows.Scan(&t.ID, &t.Subject, &t.Status, &t.Priority, &t.CreatedAt, &t.UserEmail); err == nil {
				recentTickets = append(recentTickets, t)
			}
		}
	}
	if recentTickets == nil {
		recentTickets = []RecentTicket{}
	}

	return c.JSON(fiber.Map{
		"tickets_by_status": ticketsByStatus,
		"recent_tickets":    recentTickets,
	})
}

// ListTicketsFiber returns a paginated list of support tickets with filters
func ListTicketsFiber(c *fiber.Ctx) error {
	page, _ := strconv.Atoi(c.Query("page", "1"))
	if page < 1 {
		page = 1
	}
	pageSize, _ := strconv.Atoi(c.Query("page_size", "20"))
	if pageSize < 1 || pageSize > 100 {
		pageSize = 20
	}
	offset := (page - 1) * pageSize

	statusFilter := c.Query("status", "")
	priorityFilter := c.Query("priority", "")
	assignedToFilter := c.Query("assigned_to", "")

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	db := database.GetDB()

	query := `SELECT t.id, t.user_id, COALESCE(t.assigned_to, ''), t.subject, t.category, t.priority, t.status,
	           t.created_at, t.updated_at, COALESCE(u.email, ''), COALESCE(u.full_name, '')
	           FROM support_tickets t
	           LEFT JOIN users u ON t.user_id = u.id
	           WHERE 1=1`
	countQuery := `SELECT COUNT(*) FROM support_tickets t WHERE 1=1`
	args := []interface{}{}
	argIdx := 1

	if statusFilter != "" {
		clause := fmt.Sprintf(` AND t.status = $%d`, argIdx)
		query += clause
		countQuery += clause
		args = append(args, statusFilter)
		argIdx++
	}
	if priorityFilter != "" {
		clause := fmt.Sprintf(` AND t.priority = $%d`, argIdx)
		query += clause
		countQuery += clause
		args = append(args, priorityFilter)
		argIdx++
	}
	if assignedToFilter != "" {
		clause := fmt.Sprintf(` AND t.assigned_to = $%d`, argIdx)
		query += clause
		countQuery += clause
		args = append(args, assignedToFilter)
		argIdx++
	}

	var totalCount int64
	_ = db.QueryRow(ctx, countQuery, args...).Scan(&totalCount)

	query += fmt.Sprintf(` ORDER BY t.created_at DESC LIMIT $%d OFFSET $%d`, argIdx, argIdx+1)
	args = append(args, pageSize, offset)

	rows, err := db.Query(ctx, query, args...)
	if err != nil {
		log.Printf("Failed to list tickets: %v", err)
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{
			"error": fiber.Map{"code": "DATABASE_ERROR", "message": "Failed to list tickets"},
		})
	}
	defer rows.Close()

	type TicketRow struct {
		ID           string    `json:"id"`
		UserID       string    `json:"user_id"`
		AssignedTo   string    `json:"assigned_to"`
		Subject      string    `json:"subject"`
		Category     string    `json:"category"`
		Priority     string    `json:"priority"`
		Status       string    `json:"status"`
		CreatedAt    time.Time `json:"created_at"`
		UpdatedAt    time.Time `json:"updated_at"`
		UserEmail    string    `json:"user_email"`
		UserFullName string    `json:"user_full_name"`
	}

	var tickets []TicketRow
	for rows.Next() {
		var t TicketRow
		if err := rows.Scan(&t.ID, &t.UserID, &t.AssignedTo, &t.Subject, &t.Category, &t.Priority,
			&t.Status, &t.CreatedAt, &t.UpdatedAt, &t.UserEmail, &t.UserFullName); err != nil {
			log.Printf("Failed to scan ticket: %v", err)
			continue
		}
		tickets = append(tickets, t)
	}
	if tickets == nil {
		tickets = []TicketRow{}
	}

	totalPages := int((totalCount + int64(pageSize) - 1) / int64(pageSize))

	return c.JSON(fiber.Map{
		"tickets": tickets,
		"pagination": fiber.Map{
			"page":        page,
			"page_size":   pageSize,
			"total_count": totalCount,
			"total_pages": totalPages,
		},
	})
}

// GetTicketFiber returns a single ticket with its message thread
func GetTicketFiber(c *fiber.Ctx) error {
	ticketID := c.Params("id")

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	db := database.GetDB()

	var id, userID, subject, category, priority, status string
	var assignedTo string
	var createdAt, updatedAt time.Time
	var userEmail, userFullName string

	err := db.QueryRow(ctx,
		`SELECT t.id, t.user_id, COALESCE(t.assigned_to, ''), t.subject, t.category, t.priority, t.status,
		        t.created_at, t.updated_at, COALESCE(u.email, ''), COALESCE(u.full_name, '')
		 FROM support_tickets t
		 LEFT JOIN users u ON t.user_id = u.id
		 WHERE t.id = $1`, ticketID,
	).Scan(&id, &userID, &assignedTo, &subject, &category, &priority, &status,
		&createdAt, &updatedAt, &userEmail, &userFullName)
	if err != nil {
		return c.Status(fiber.StatusNotFound).JSON(fiber.Map{
			"error": fiber.Map{"code": "NOT_FOUND", "message": "Ticket not found"},
		})
	}

	// Get messages
	msgRows, err := db.Query(ctx,
		`SELECT m.id, m.sender_id, m.message, m.is_internal, m.created_at, COALESCE(u.email, ''), COALESCE(u.full_name, '')
		 FROM ticket_messages m
		 LEFT JOIN users u ON m.sender_id = u.id
		 WHERE m.ticket_id = $1
		 ORDER BY m.created_at ASC`, ticketID)

	type Message struct {
		ID             string    `json:"id"`
		SenderID       string    `json:"sender_id"`
		Message        string    `json:"message"`
		IsInternal     bool      `json:"is_internal"`
		CreatedAt      time.Time `json:"created_at"`
		SenderEmail    string    `json:"sender_email"`
		SenderFullName string    `json:"sender_full_name"`
	}

	var messages []Message
	if err == nil {
		defer msgRows.Close()
		for msgRows.Next() {
			var m Message
			if err := msgRows.Scan(&m.ID, &m.SenderID, &m.Message, &m.IsInternal, &m.CreatedAt,
				&m.SenderEmail, &m.SenderFullName); err == nil {
				messages = append(messages, m)
			}
		}
	}
	if messages == nil {
		messages = []Message{}
	}

	return c.JSON(fiber.Map{
		"ticket": fiber.Map{
			"id":             id,
			"user_id":        userID,
			"assigned_to":    assignedTo,
			"subject":        subject,
			"category":       category,
			"priority":       priority,
			"status":         status,
			"created_at":     createdAt,
			"updated_at":     updatedAt,
			"user_email":     userEmail,
			"user_full_name": userFullName,
		},
		"messages": messages,
	})
}

// ReplyToTicketFiber adds a reply to a support ticket
func ReplyToTicketFiber(c *fiber.Ctx) error {
	ticketID := c.Params("id")
	senderID := c.Locals("user_id").(string)

	var req struct {
		Message    string `json:"message"`
		IsInternal bool   `json:"is_internal"`
	}
	if err := c.BodyParser(&req); err != nil || req.Message == "" {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
			"error": fiber.Map{"code": "INVALID_REQUEST", "message": "message is required"},
		})
	}

	// Sanitize user input
	req.Message = sanitize.String(req.Message)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	db := database.GetDB()

	// Verify ticket exists
	var exists bool
	err := db.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM support_tickets WHERE id = $1)`, ticketID).Scan(&exists)
	if err != nil || !exists {
		return c.Status(fiber.StatusNotFound).JSON(fiber.Map{
			"error": fiber.Map{"code": "NOT_FOUND", "message": "Ticket not found"},
		})
	}

	msgID := "msg_" + uuid.New().String()[:8]

	tx, err := db.Begin(ctx)
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{
			"error": fiber.Map{"code": "SERVER_ERROR", "message": "Failed to begin transaction"},
		})
	}
	defer tx.Rollback(ctx)

	_, err = tx.Exec(ctx,
		`INSERT INTO ticket_messages (id, ticket_id, sender_id, message, is_internal, created_at)
		 VALUES ($1, $2, $3, $4, $5, NOW())`,
		msgID, ticketID, senderID, req.Message, req.IsInternal,
	)
	if err != nil {
		log.Printf("Failed to insert ticket message: %v", err)
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{
			"error": fiber.Map{"code": "DATABASE_ERROR", "message": "Failed to add reply"},
		})
	}

	_, err = tx.Exec(ctx, `UPDATE support_tickets SET updated_at = NOW() WHERE id = $1`, ticketID)
	if err != nil {
		log.Printf("Failed to update ticket timestamp: %v", err)
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{
			"error": fiber.Map{"code": "DATABASE_ERROR", "message": "Failed to update ticket"},
		})
	}

	if err := tx.Commit(ctx); err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{
			"error": fiber.Map{"code": "SERVER_ERROR", "message": "Failed to commit reply"},
		})
	}

	return c.Status(fiber.StatusCreated).JSON(fiber.Map{
		"id":          msgID,
		"ticket_id":   ticketID,
		"sender_id":   senderID,
		"message":     req.Message,
		"is_internal": req.IsInternal,
	})
}

// UpdateTicketStatusFiber updates a ticket's status
func UpdateTicketStatusFiber(c *fiber.Ctx) error {
	ticketID := c.Params("id")

	var req struct {
		Status string `json:"status"`
	}
	if err := c.BodyParser(&req); err != nil || req.Status == "" {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
			"error": fiber.Map{"code": "INVALID_REQUEST", "message": "status is required"},
		})
	}

	validStatuses := map[string]bool{"open": true, "in_progress": true, "waiting": true, "resolved": true, "closed": true}
	if !validStatuses[req.Status] {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
			"error": fiber.Map{"code": "INVALID_STATUS", "message": "Status must be one of: open, in_progress, waiting, resolved, closed"},
		})
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	db := database.GetDB()

	tag, err := db.Exec(ctx, `UPDATE support_tickets SET status = $1, updated_at = NOW() WHERE id = $2`, req.Status, ticketID)
	if err != nil {
		log.Printf("Failed to update ticket status: %v", err)
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{
			"error": fiber.Map{"code": "DATABASE_ERROR", "message": "Failed to update ticket status"},
		})
	}
	if tag.RowsAffected() == 0 {
		return c.Status(fiber.StatusNotFound).JSON(fiber.Map{
			"error": fiber.Map{"code": "NOT_FOUND", "message": "Ticket not found"},
		})
	}

	return c.JSON(fiber.Map{"success": true, "ticket_id": ticketID, "status": req.Status})
}

// AssignTicketFiber assigns a ticket to a support agent
func AssignTicketFiber(c *fiber.Ctx) error {
	ticketID := c.Params("id")

	var req struct {
		AssignedTo string `json:"assigned_to"`
	}
	if err := c.BodyParser(&req); err != nil || req.AssignedTo == "" {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
			"error": fiber.Map{"code": "INVALID_REQUEST", "message": "assigned_to is required"},
		})
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	db := database.GetDB()

	// Verify the assigned user exists
	var assigneeExists bool
	_ = db.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM users WHERE id = $1)`, req.AssignedTo).Scan(&assigneeExists)
	if !assigneeExists {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
			"error": fiber.Map{"code": "INVALID_USER", "message": "Assigned user does not exist"},
		})
	}

	tag, err := db.Exec(ctx, `UPDATE support_tickets SET assigned_to = $1, updated_at = NOW() WHERE id = $2`, req.AssignedTo, ticketID)
	if err != nil {
		log.Printf("Failed to assign ticket: %v", err)
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{
			"error": fiber.Map{"code": "DATABASE_ERROR", "message": "Failed to assign ticket"},
		})
	}
	if tag.RowsAffected() == 0 {
		return c.Status(fiber.StatusNotFound).JSON(fiber.Map{
			"error": fiber.Map{"code": "NOT_FOUND", "message": "Ticket not found"},
		})
	}

	return c.JSON(fiber.Map{"success": true, "ticket_id": ticketID, "assigned_to": req.AssignedTo})
}

// UpdateTicketPriorityFiber updates a ticket's priority
func UpdateTicketPriorityFiber(c *fiber.Ctx) error {
	ticketID := c.Params("id")

	var req struct {
		Priority string `json:"priority"`
	}
	if err := c.BodyParser(&req); err != nil || req.Priority == "" {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
			"error": fiber.Map{"code": "INVALID_REQUEST", "message": "priority is required"},
		})
	}

	validPriorities := map[string]bool{"low": true, "normal": true, "high": true, "urgent": true}
	if !validPriorities[req.Priority] {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
			"error": fiber.Map{"code": "INVALID_PRIORITY", "message": "Priority must be one of: low, normal, high, urgent"},
		})
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	db := database.GetDB()

	tag, err := db.Exec(ctx, `UPDATE support_tickets SET priority = $1, updated_at = NOW() WHERE id = $2`, req.Priority, ticketID)
	if err != nil {
		log.Printf("Failed to update ticket priority: %v", err)
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{
			"error": fiber.Map{"code": "DATABASE_ERROR", "message": "Failed to update ticket priority"},
		})
	}
	if tag.RowsAffected() == 0 {
		return c.Status(fiber.StatusNotFound).JSON(fiber.Map{
			"error": fiber.Map{"code": "NOT_FOUND", "message": "Ticket not found"},
		})
	}

	return c.JSON(fiber.Map{"success": true, "ticket_id": ticketID, "priority": req.Priority})
}

// GetUserForSupportFiber returns read-only user details for support agents
func GetUserForSupportFiber(c *fiber.Ctx) error {
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

	var ticketCount int64
	_ = db.QueryRow(ctx, `SELECT COUNT(*) FROM support_tickets WHERE user_id = $1`, userID).Scan(&ticketCount)

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
		"ticket_count":     ticketCount,
	})
}

// GetUserValidationsFiber returns a user's validations (for support view)
func GetUserValidationsFiber(c *fiber.Ctx) error {
	userID := c.Params("id")

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
	_ = db.QueryRow(ctx, `SELECT COUNT(*) FROM validations WHERE user_id = $1`, userID).Scan(&totalCount)

	rows, err := db.Query(ctx,
		`SELECT id, dataset_id, status, COALESCE(validation_type, ''), created_at, updated_at
		 FROM validations
		 WHERE user_id = $1
		 ORDER BY created_at DESC
		 LIMIT $2 OFFSET $3`, userID, pageSize, offset)
	if err != nil {
		log.Printf("Failed to list user validations: %v", err)
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{
			"error": fiber.Map{"code": "DATABASE_ERROR", "message": "Failed to list validations"},
		})
	}
	defer rows.Close()

	type UserValidation struct {
		ID             string    `json:"id"`
		DatasetID      string    `json:"dataset_id"`
		Status         string    `json:"status"`
		ValidationType string    `json:"validation_type"`
		CreatedAt      time.Time `json:"created_at"`
		UpdatedAt      time.Time `json:"updated_at"`
	}

	var validations []UserValidation
	for rows.Next() {
		var v UserValidation
		if err := rows.Scan(&v.ID, &v.DatasetID, &v.Status, &v.ValidationType, &v.CreatedAt, &v.UpdatedAt); err != nil {
			log.Printf("Failed to scan validation: %v", err)
			continue
		}
		validations = append(validations, v)
	}
	if validations == nil {
		validations = []UserValidation{}
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
