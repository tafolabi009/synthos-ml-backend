package handlers

import (
	"context"
	"log"
	"strconv"
	"time"

	"github.com/gofiber/fiber/v2"
	"github.com/google/uuid"
	"github.com/tafolabi009/backend/go_backend/pkg/database"
)

// CreateTicketFiber creates a new support ticket with an initial message
func CreateTicketFiber(c *fiber.Ctx) error {
	userID := c.Locals("user_id").(string)

	var req struct {
		Subject  string `json:"subject"`
		Message  string `json:"message"`
		Category string `json:"category"`
	}
	if err := c.BodyParser(&req); err != nil || req.Subject == "" || req.Message == "" {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
			"error": fiber.Map{"code": "INVALID_REQUEST", "message": "subject and message are required"},
		})
	}

	if req.Category == "" {
		req.Category = "general"
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	db := database.GetDB()

	ticketID := "tkt_" + uuid.New().String()[:8]
	msgID := "msg_" + uuid.New().String()[:8]

	tx, err := db.Begin(ctx)
	if err != nil {
		log.Printf("Failed to begin ticket transaction: %v", err)
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{
			"error": fiber.Map{"code": "SERVER_ERROR", "message": "Failed to create ticket"},
		})
	}
	defer tx.Rollback(ctx)

	// Create ticket
	_, err = tx.Exec(ctx,
		`INSERT INTO support_tickets (id, user_id, subject, category, priority, status, created_at, updated_at)
		 VALUES ($1, $2, $3, $4, 'normal', 'open', NOW(), NOW())`,
		ticketID, userID, req.Subject, req.Category,
	)
	if err != nil {
		log.Printf("Failed to create ticket: %v", err)
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{
			"error": fiber.Map{"code": "DATABASE_ERROR", "message": "Failed to create ticket"},
		})
	}

	// Create initial message
	_, err = tx.Exec(ctx,
		`INSERT INTO ticket_messages (id, ticket_id, sender_id, message, is_internal, created_at)
		 VALUES ($1, $2, $3, $4, false, NOW())`,
		msgID, ticketID, userID, req.Message,
	)
	if err != nil {
		log.Printf("Failed to create ticket message: %v", err)
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{
			"error": fiber.Map{"code": "DATABASE_ERROR", "message": "Failed to create ticket message"},
		})
	}

	if err := tx.Commit(ctx); err != nil {
		log.Printf("Failed to commit ticket: %v", err)
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{
			"error": fiber.Map{"code": "SERVER_ERROR", "message": "Failed to finalize ticket"},
		})
	}

	return c.Status(fiber.StatusCreated).JSON(fiber.Map{
		"id":       ticketID,
		"subject":  req.Subject,
		"category": req.Category,
		"status":   "open",
		"priority": "normal",
	})
}

// ListMyTicketsFiber lists the authenticated user's tickets
func ListMyTicketsFiber(c *fiber.Ctx) error {
	userID := c.Locals("user_id").(string)

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
	_ = db.QueryRow(ctx, `SELECT COUNT(*) FROM support_tickets WHERE user_id = $1`, userID).Scan(&totalCount)

	rows, err := db.Query(ctx,
		`SELECT id, subject, category, priority, status, created_at, updated_at
		 FROM support_tickets
		 WHERE user_id = $1
		 ORDER BY updated_at DESC
		 LIMIT $2 OFFSET $3`, userID, pageSize, offset)
	if err != nil {
		log.Printf("Failed to list tickets: %v", err)
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{
			"error": fiber.Map{"code": "DATABASE_ERROR", "message": "Failed to list tickets"},
		})
	}
	defer rows.Close()

	type MyTicket struct {
		ID        string    `json:"id"`
		Subject   string    `json:"subject"`
		Category  string    `json:"category"`
		Priority  string    `json:"priority"`
		Status    string    `json:"status"`
		CreatedAt time.Time `json:"created_at"`
		UpdatedAt time.Time `json:"updated_at"`
	}

	var tickets []MyTicket
	for rows.Next() {
		var t MyTicket
		if err := rows.Scan(&t.ID, &t.Subject, &t.Category, &t.Priority, &t.Status, &t.CreatedAt, &t.UpdatedAt); err != nil {
			log.Printf("Failed to scan ticket: %v", err)
			continue
		}
		tickets = append(tickets, t)
	}
	if tickets == nil {
		tickets = []MyTicket{}
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

// GetMyTicketFiber returns a single ticket belonging to the authenticated user (excludes internal messages)
func GetMyTicketFiber(c *fiber.Ctx) error {
	ticketID := c.Params("id")
	userID := c.Locals("user_id").(string)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	db := database.GetDB()

	var id, subject, category, priority, status string
	var createdAt, updatedAt time.Time

	err := db.QueryRow(ctx,
		`SELECT id, subject, category, priority, status, created_at, updated_at
		 FROM support_tickets
		 WHERE id = $1 AND user_id = $2`, ticketID, userID,
	).Scan(&id, &subject, &category, &priority, &status, &createdAt, &updatedAt)
	if err != nil {
		return c.Status(fiber.StatusNotFound).JSON(fiber.Map{
			"error": fiber.Map{"code": "NOT_FOUND", "message": "Ticket not found"},
		})
	}

	// Get messages (exclude internal notes)
	msgRows, err := db.Query(ctx,
		`SELECT m.id, m.sender_id, m.message, m.created_at, COALESCE(u.full_name, u.email) as sender_name
		 FROM ticket_messages m
		 LEFT JOIN users u ON m.sender_id = u.id
		 WHERE m.ticket_id = $1 AND m.is_internal = false
		 ORDER BY m.created_at ASC`, ticketID)

	type PublicMessage struct {
		ID         string    `json:"id"`
		SenderID   string    `json:"sender_id"`
		Message    string    `json:"message"`
		CreatedAt  time.Time `json:"created_at"`
		SenderName string    `json:"sender_name"`
	}

	var messages []PublicMessage
	if err == nil {
		defer msgRows.Close()
		for msgRows.Next() {
			var m PublicMessage
			if err := msgRows.Scan(&m.ID, &m.SenderID, &m.Message, &m.CreatedAt, &m.SenderName); err == nil {
				messages = append(messages, m)
			}
		}
	}
	if messages == nil {
		messages = []PublicMessage{}
	}

	return c.JSON(fiber.Map{
		"ticket": fiber.Map{
			"id":         id,
			"subject":    subject,
			"category":   category,
			"priority":   priority,
			"status":     status,
			"created_at": createdAt,
			"updated_at": updatedAt,
		},
		"messages": messages,
	})
}

// ReplyToMyTicketFiber adds a customer reply to their own ticket
func ReplyToMyTicketFiber(c *fiber.Ctx) error {
	ticketID := c.Params("id")
	userID := c.Locals("user_id").(string)

	var req struct {
		Message string `json:"message"`
	}
	if err := c.BodyParser(&req); err != nil || req.Message == "" {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
			"error": fiber.Map{"code": "INVALID_REQUEST", "message": "message is required"},
		})
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	db := database.GetDB()

	// Verify ticket belongs to user
	var exists bool
	err := db.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM support_tickets WHERE id = $1 AND user_id = $2)`, ticketID, userID).Scan(&exists)
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

	// Always is_internal=false for customer replies
	_, err = tx.Exec(ctx,
		`INSERT INTO ticket_messages (id, ticket_id, sender_id, message, is_internal, created_at)
		 VALUES ($1, $2, $3, $4, false, NOW())`,
		msgID, ticketID, userID, req.Message,
	)
	if err != nil {
		log.Printf("Failed to insert ticket reply: %v", err)
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
		"id":        msgID,
		"ticket_id": ticketID,
		"message":   req.Message,
	})
}
