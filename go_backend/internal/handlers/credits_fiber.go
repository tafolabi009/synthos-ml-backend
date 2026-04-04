package handlers

import (
	"context"
	"fmt"
	"log"
	"strconv"
	"time"

	"github.com/gofiber/fiber/v2"
	"github.com/tafolabi009/backend/go_backend/internal/models"
	"github.com/tafolabi009/backend/go_backend/internal/repository"
	"github.com/tafolabi009/backend/go_backend/pkg/database"
	"github.com/tafolabi009/backend/go_backend/pkg/webhook"
)

// GetCreditBalanceFiber returns the user's current credit balance and pricing info
func GetCreditBalanceFiber(c *fiber.Ctx) error {
	userID := c.Locals("user_id").(string)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	creditRepo := repository.NewCreditRepository(database.GetDB())

	balance, err := creditRepo.GetOrCreateBalance(ctx, userID)
	if err != nil {
		log.Printf("Failed to get credit balance: %v", err)
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{
			"error": fiber.Map{
				"code":    "DATABASE_ERROR",
				"message": "Failed to retrieve credit balance",
			},
		})
	}

	costs, err := creditRepo.GetCreditCosts(ctx)
	if err != nil {
		log.Printf("Failed to get credit costs: %v", err)
		costs = []models.CreditCost{}
	}

	response := models.CreditBalanceResponse{
		Balance:           balance.Balance,
		LifetimePurchased: balance.LifetimePurchased,
		LifetimeUsed:      balance.LifetimeUsed,
		CreditCosts:       costs,
	}

	return c.JSON(response)
}

// GetCreditPackagesFiber returns available credit packages for purchase
func GetCreditPackagesFiber(c *fiber.Ctx) error {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	creditRepo := repository.NewCreditRepository(database.GetDB())
	packages, err := creditRepo.GetPackages(ctx)
	if err != nil {
		log.Printf("Failed to get credit packages: %v", err)
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{
			"error": fiber.Map{
				"code":    "DATABASE_ERROR",
				"message": "Failed to retrieve credit packages",
			},
		})
	}

	return c.JSON(models.CreditPackagesResponse{Packages: packages})
}

// PurchaseCreditsFiber handles credit package purchases
func PurchaseCreditsFiber(c *fiber.Ctx) error {
	userID := c.Locals("user_id").(string)

	var req models.PurchaseCreditsRequest
	if err := c.BodyParser(&req); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
			"error": fiber.Map{
				"code":    "INVALID_REQUEST",
				"message": "Invalid request body",
			},
		})
	}

	if req.PackageID == "" {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
			"error": fiber.Map{
				"code":    "INVALID_REQUEST",
				"message": "package_id is required",
			},
		})
	}

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	creditRepo := repository.NewCreditRepository(database.GetDB())

	// Get the package
	pkg, err := creditRepo.GetPackageByID(ctx, req.PackageID)
	if err != nil {
		return c.Status(fiber.StatusNotFound).JSON(fiber.Map{
			"error": fiber.Map{
				"code":    "PACKAGE_NOT_FOUND",
				"message": "Credit package not found",
			},
		})
	}

	// TODO: Integrate with payment provider (Stripe) here
	// For now, we simulate a successful payment and add credits

	totalCredits := pkg.Credits + pkg.BonusCredits
	refType := "package"
	description := fmt.Sprintf("Purchased %s package (%d + %d bonus credits)", pkg.Name, pkg.Credits, pkg.BonusCredits)

	transaction, err := creditRepo.AddCredits(ctx, userID, totalCredits, "purchase", description, &refType, &pkg.ID)
	if err != nil {
		log.Printf("Failed to add credits: %v", err)
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{
			"error": fiber.Map{
				"code":    "CREDIT_ERROR",
				"message": "Failed to process credit purchase",
			},
		})
	}

	// Dispatch webhook event for credits purchased
	webhook.Dispatch("credits.purchased", userID, fiber.Map{"amount": totalCredits, "balance": transaction.BalanceAfter})

	// Get updated balance
	balance, err := creditRepo.GetOrCreateBalance(ctx, userID)
	if err != nil {
		log.Printf("Failed to get updated balance: %v", err)
	}

	response := models.PurchaseCreditsResponse{
		TransactionID: transaction.ID,
		PackageName:   pkg.Name,
		CreditsAdded:  pkg.Credits,
		BonusCredits:  pkg.BonusCredits,
		TotalAdded:    totalCredits,
		NewBalance:    transaction.BalanceAfter,
		AmountCharged: pkg.PriceCents,
		Currency:      pkg.Currency,
	}
	if balance != nil {
		response.Balance = *balance
	}

	return c.Status(fiber.StatusCreated).JSON(response)
}

// GetCreditHistoryFiber returns paginated credit transaction history
func GetCreditHistoryFiber(c *fiber.Ctx) error {
	userID := c.Locals("user_id").(string)

	page, _ := strconv.Atoi(c.Query("page", "1"))
	if page < 1 {
		page = 1
	}
	pageSize, _ := strconv.Atoi(c.Query("page_size", "20"))
	if pageSize < 1 || pageSize > 100 {
		pageSize = 20
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	creditRepo := repository.NewCreditRepository(database.GetDB())
	transactions, totalCount, err := creditRepo.ListTransactions(ctx, userID, page, pageSize)
	if err != nil {
		log.Printf("Failed to get credit history: %v", err)
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{
			"error": fiber.Map{
				"code":    "DATABASE_ERROR",
				"message": "Failed to retrieve credit history",
			},
		})
	}

	totalPages := (totalCount + pageSize - 1) / pageSize

	return c.JSON(models.CreditHistoryResponse{
		Transactions: transactions,
		Pagination: models.Pagination{
			Page:       page,
			PageSize:   pageSize,
			TotalCount: totalCount,
			TotalPages: totalPages,
		},
	})
}
