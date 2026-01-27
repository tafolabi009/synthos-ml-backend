package handlers

import (
	"context"
	"fmt"
	"log"
	"os"
	"strconv"
	"time"

	"github.com/gofiber/fiber/v2"
	"github.com/google/uuid"
	"github.com/tafolabi009/backend/go_backend/internal/models"
	"github.com/tafolabi009/backend/go_backend/internal/repository"
	"github.com/tafolabi009/backend/go_backend/pkg/grpcclient"
	"github.com/tafolabi009/backend/go_backend/pkg/storage"
	validationpb "github.com/tafolabi009/backend/proto/validation"
)

// DatasetHandler holds dependencies for dataset handlers
type DatasetHandler struct {
	S3Client         *storage.S3Client
	Repo             *repository.DatasetRepository
	ValidationClient *grpcclient.ValidationClient
}

// NewDatasetHandler creates a new DatasetHandler with injected dependencies
func NewDatasetHandler(s3Client *storage.S3Client, repo *repository.DatasetRepository, validationClient *grpcclient.ValidationClient) *DatasetHandler {
	return &DatasetHandler{
		S3Client:         s3Client,
		Repo:             repo,
		ValidationClient: validationClient,
	}
}

func getS3Bucket() string {
	if bucket := os.Getenv("S3_BUCKET"); bucket != "" {
		return bucket
	}
	return "synthos-datasets-570116615008"
}

// InitiateUploadFiber starts a dataset upload and returns signed URL - Fiber version
func (h *DatasetHandler) InitiateUploadFiber(c *fiber.Ctx) error {
	userID := c.Locals("user_id").(string)

	var req models.InitiateUploadRequest
	if err := c.BodyParser(&req); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
			"error": fiber.Map{
				"code":    "INVALID_REQUEST",
				"message": err.Error(),
			},
		})
	}

	// Generate dataset ID
	datasetID := "ds_" + uuid.New().String()[:8]

	// Generate S3 key path
	s3Key := fmt.Sprintf("%s/%s/%s", userID, datasetID, req.Filename)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	// Generate presigned URL for upload (valid for 1 hour)
	// Pass Content-Type to prevent AWS 403 signature mismatch errors
	uploadURL, err := h.S3Client.GeneratePresignedURL(ctx, s3Key, "PUT", time.Hour, req.FileType)
	if err != nil {
		log.Printf("Failed to generate presigned URL: %v", err)
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{
			"error": fiber.Map{
				"code":    "URL_GENERATION_ERROR",
				"message": "Failed to generate upload URL",
			},
		})
	}

	// Save dataset metadata to database
	dataset := models.Dataset{
		ID:          datasetID,
		UserID:      userID,
		Filename:    req.Filename,
		FileSize:    req.FileSize,
		FileType:    req.FileType,
		Status:      "uploading",
		S3Path:      s3Key,
		Description: req.Description,
		UploadedAt:  time.Now().UTC(),
	}

	if err := h.Repo.Create(ctx, &dataset); err != nil {
		log.Printf("Failed to create dataset record: %v", err)
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{
			"error": fiber.Map{
				"code":    "DATABASE_ERROR",
				"message": "Failed to save dataset metadata",
			},
		})
	}

	response := models.InitiateUploadResponse{
		DatasetID:    datasetID,
		UploadURL:    uploadURL,
		UploadMethod: "multipart",
		ChunkSize:    10485760,
		ExpiresIn:    3600,
	}

	return c.JSON(response)
}

// CompleteUploadFiber marks an upload as complete and triggers processing - Fiber version
func (h *DatasetHandler) CompleteUploadFiber(c *fiber.Ctx) error {
	datasetID := c.Params("id")
	userID := c.Locals("user_id").(string)

	var req models.CompleteUploadRequest
	if err := c.BodyParser(&req); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
			"error": fiber.Map{
				"code":    "INVALID_REQUEST",
				"message": err.Error(),
			},
		})
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	// Verify dataset belongs to user
	dataset, err := h.Repo.GetByID(ctx, datasetID)
	if err != nil {
		return c.Status(fiber.StatusNotFound).JSON(fiber.Map{
			"error": fiber.Map{
				"code":    "NOT_FOUND",
				"message": "Dataset not found",
			},
		})
	}

	if dataset.UserID != userID {
		return c.Status(fiber.StatusForbidden).JSON(fiber.Map{
			"error": fiber.Map{
				"code":    "FORBIDDEN",
				"message": "You do not have access to this dataset",
			},
		})
	}

	// Update dataset status to "processing"
	dataset.Status = "processing"
	dataset.ProcessedAt = &[]time.Time{time.Now().UTC()}[0]

	if err := h.Repo.Update(ctx, dataset); err != nil {
		log.Printf("Failed to update dataset status: %v", err)
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{
			"error": fiber.Map{
				"code":    "DATABASE_ERROR",
				"message": "Failed to update dataset status",
			},
		})
	}

	// Trigger ML backend via gRPC - this fixes the "Ghost Job" bug
	if h.ValidationClient != nil {
		go func() {
			// Use a separate context for the async gRPC call
			grpcCtx, grpcCancel := context.WithTimeout(context.Background(), 5*time.Minute)
			defer grpcCancel()

			// Create cascade config with defaults
			cascadeConfig := &validationpb.CascadeConfig{
				NumEpochs:               10,
				BatchSize:               32,
				LearningRate:            0.001,
				EarlyStoppingPatience:   3,
				ValidationSplit:         0.2,
				Tiers:                   []string{"light", "medium", "heavy"},
				EnableSpectralAnalysis:  true,
				EnableFrequencyAnalysis: true,
			}

			// Trigger cascade training on the ML backend
			_, err := h.ValidationClient.TrainCascade(grpcCtx, datasetID, dataset.S3Path, cascadeConfig)
			if err != nil {
				// Log the error but don't fail the request since data is already saved
				log.Printf("⚠️ Failed to trigger ML backend for dataset %s: %v", datasetID, err)
			} else {
				log.Printf("✅ Successfully triggered ML backend for dataset %s", datasetID)
			}
		}()
	} else {
		log.Printf("⚠️ ValidationClient not available, skipping ML backend trigger for dataset %s", datasetID)
	}

	response := models.CompleteUploadResponse{
		DatasetID:           datasetID,
		Status:              "processing",
		EstimatedCompletion: time.Now().Add(30 * time.Minute),
		ProcessingStages: []models.ProcessingStage{
			{Stage: "ingestion", Status: "in_progress", Progress: 0},
			{Stage: "profiling", Status: "pending", Progress: 0},
		},
	}

	return c.JSON(response)
}

// ListDatasetsFiber returns paginated list of datasets - Fiber version
func (h *DatasetHandler) ListDatasetsFiber(c *fiber.Ctx) error {
	userID := c.Locals("user_id").(string)

	page, _ := strconv.Atoi(c.Query("page", "1"))
	if page < 1 {
		page = 1
	}

	pageSize, _ := strconv.Atoi(c.Query("page_size", "20"))
	if pageSize < 1 || pageSize > 100 {
		pageSize = 20
	}

	status := c.Query("status")

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	datasets, totalCount, err := h.Repo.List(ctx, userID, page, pageSize)
	if err != nil {
		log.Printf("Failed to list datasets: %v", err)
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{
			"error": fiber.Map{
				"code":    "DATABASE_ERROR",
				"message": "Failed to retrieve datasets",
			},
		})
	}

	// Filter by status if provided
	if status != "" {
		filtered := []models.Dataset{}
		for _, ds := range datasets {
			if ds.Status == status {
				filtered = append(filtered, ds)
			}
		}
		datasets = filtered
	}

	totalPages := (totalCount + pageSize - 1) / pageSize

	response := models.DatasetListResponse{
		Datasets: datasets,
		Pagination: models.Pagination{
			Page:       page,
			PageSize:   pageSize,
			TotalCount: totalCount,
			TotalPages: totalPages,
		},
	}

	return c.JSON(response)
}

// GetDatasetFiber returns details for a specific dataset - Fiber version
func (h *DatasetHandler) GetDatasetFiber(c *fiber.Ctx) error {
	datasetID := c.Params("id")
	userID := c.Locals("user_id").(string)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	dataset, err := h.Repo.GetByID(ctx, datasetID)
	if err != nil {
		return c.Status(fiber.StatusNotFound).JSON(fiber.Map{
			"error": fiber.Map{
				"code":    "NOT_FOUND",
				"message": "Dataset not found",
			},
		})
	}

	if dataset.UserID != userID {
		return c.Status(fiber.StatusForbidden).JSON(fiber.Map{
			"error": fiber.Map{
				"code":    "FORBIDDEN",
				"message": "You do not have access to this dataset",
			},
		})
	}

	return c.JSON(dataset)
}

// DeleteDatasetFiber removes a dataset - Fiber version
func (h *DatasetHandler) DeleteDatasetFiber(c *fiber.Ctx) error {
	datasetID := c.Params("id")
	userID := c.Locals("user_id").(string)

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	dataset, err := h.Repo.GetByID(ctx, datasetID)
	if err != nil {
		return c.Status(fiber.StatusNotFound).JSON(fiber.Map{
			"error": fiber.Map{
				"code":    "NOT_FOUND",
				"message": "Dataset not found",
			},
		})
	}

	if dataset.UserID != userID {
		return c.Status(fiber.StatusForbidden).JSON(fiber.Map{
			"error": fiber.Map{
				"code":    "FORBIDDEN",
				"message": "You do not have access to this dataset",
			},
		})
	}

	// Delete from S3 if S3 path exists
	if dataset.S3Path != "" {
		if err := h.S3Client.Delete(ctx, dataset.S3Path); err != nil {
			log.Printf("Failed to delete file from S3: %v", err)
			// Continue with database deletion even if S3 delete fails
		}
	}

	if err := h.Repo.Delete(ctx, datasetID); err != nil {
		log.Printf("Failed to delete dataset from database: %v", err)
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{
			"error": fiber.Map{
				"code":    "DATABASE_ERROR",
				"message": "Failed to delete dataset",
			},
		})
	}

	return c.JSON(fiber.Map{
		"dataset_id": datasetID,
		"status":     "deleted",
		"deleted_at": time.Now().UTC().Format(time.RFC3339),
	})
}
