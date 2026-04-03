package tests

import (
"bytes"
"encoding/json"
"net/http/httptest"
"testing"
"time"

"github.com/gofiber/fiber/v2"
"github.com/stretchr/testify/assert"
"github.com/stretchr/testify/require"
"github.com/tafolabi009/backend/go_backend/internal/auth"
"github.com/tafolabi009/backend/go_backend/internal/middleware"
)

func setupTestApp() *fiber.App {
auth.InitJWT("test-secret")

app := fiber.New(fiber.Config{
ErrorHandler: func(c *fiber.Ctx, err error) error {
return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{
"error": fiber.Map{
"code":    "INTERNAL_ERROR",
"message": err.Error(),
},
})
},
})

// Health check endpoint
app.Get("/health", func(c *fiber.Ctx) error {
return c.Status(fiber.StatusOK).JSON(fiber.Map{
"status": "healthy",
"time":   time.Now().UTC().Format(time.RFC3339),
})
})

// Mock auth endpoints for testing
app.Post("/api/v1/auth/register", mockRegisterHandler)
app.Post("/api/v1/auth/login", mockLoginHandler)
app.Post("/api/v1/auth/refresh", mockRefreshHandler)

// Protected routes
protected := app.Group("/api/v1", middleware.AuthRequiredFiber())
protected.Post("/datasets/upload", mockUploadHandler)
protected.Post("/validations/create", mockCreateValidationHandler)
protected.Get("/validations/:id", mockGetValidationHandler)

return app
}

func mockRegisterHandler(c *fiber.Ctx) error {
var req map[string]interface{}
if err := c.BodyParser(&req); err != nil {
return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
"error": fiber.Map{"code": "INVALID_REQUEST", "message": err.Error()},
})
}
if req["email"] == nil || req["password"] == nil {
return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
"error": fiber.Map{"code": "VALIDATION_ERROR", "message": "Email and password required"},
})
}
return c.Status(fiber.StatusCreated).JSON(fiber.Map{
"user_id": "usr_test123", "email": req["email"],
"company_id": "cmp_test123", "created_at": time.Now().UTC().Format(time.RFC3339),
})
}

func mockLoginHandler(c *fiber.Ctx) error {
var req map[string]interface{}
if err := c.BodyParser(&req); err != nil {
return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
"error": fiber.Map{"code": "INVALID_REQUEST", "message": err.Error()},
})
}
accessToken, _ := auth.GenerateToken("usr_test123", req["email"].(string), "cmp_test123", 15*time.Minute)
refreshToken, _ := auth.GenerateToken("usr_test123", req["email"].(string), "cmp_test123", 30*24*time.Hour)
return c.Status(fiber.StatusOK).JSON(fiber.Map{
"access_token": accessToken, "refresh_token": refreshToken, "token_type": "Bearer", "expires_in": 900,
})
}

func mockRefreshHandler(c *fiber.Ctx) error {
accessToken, _ := auth.GenerateToken("usr_test123", "test@example.com", "cmp_test123", 15*time.Minute)
refreshToken, _ := auth.GenerateToken("usr_test123", "test@example.com", "cmp_test123", 30*24*time.Hour)
return c.Status(fiber.StatusOK).JSON(fiber.Map{
"access_token": accessToken, "refresh_token": refreshToken, "token_type": "Bearer", "expires_in": 900,
})
}

func mockUploadHandler(c *fiber.Ctx) error {
return c.Status(fiber.StatusOK).JSON(fiber.Map{"upload_id": "upl_test123", "status": "pending"})
}

func mockCreateValidationHandler(c *fiber.Ctx) error {
return c.Status(fiber.StatusAccepted).JSON(fiber.Map{"validation_id": "val_test123", "status": "pending"})
}

func mockGetValidationHandler(c *fiber.Ctx) error {
return c.Status(fiber.StatusOK).JSON(fiber.Map{"validation_id": c.Params("id"), "status": "completed", "score": 0.85})
}

func TestHealthCheck(t *testing.T) {
app := setupTestApp()
req := httptest.NewRequest("GET", "/health", nil)
resp, err := app.Test(req)
require.NoError(t, err)
assert.Equal(t, fiber.StatusOK, resp.StatusCode)
}

func TestFullAuthFlow(t *testing.T) {
app := setupTestApp()

// Register
body, _ := json.Marshal(map[string]interface{}{"email": "test@test.com", "password": "Pass123!", "name": "Test"})
req := httptest.NewRequest("POST", "/api/v1/auth/register", bytes.NewReader(body))
req.Header.Set("Content-Type", "application/json")
resp, err := app.Test(req)
require.NoError(t, err)
assert.Equal(t, fiber.StatusCreated, resp.StatusCode)

// Login
body, _ = json.Marshal(map[string]interface{}{"email": "test@test.com", "password": "Pass123!"})
req = httptest.NewRequest("POST", "/api/v1/auth/login", bytes.NewReader(body))
req.Header.Set("Content-Type", "application/json")
resp, err = app.Test(req)
require.NoError(t, err)
assert.Equal(t, fiber.StatusOK, resp.StatusCode)

var loginResp map[string]interface{}
json.NewDecoder(resp.Body).Decode(&loginResp)
assert.Contains(t, loginResp, "access_token")
accessToken := loginResp["access_token"].(string)

// Protected route
req = httptest.NewRequest("POST", "/api/v1/datasets/upload", bytes.NewReader([]byte(`{"filename":"test.parquet"}`)))
req.Header.Set("Content-Type", "application/json")
req.Header.Set("Authorization", "Bearer "+accessToken)
resp, err = app.Test(req)
require.NoError(t, err)
assert.Equal(t, fiber.StatusOK, resp.StatusCode)
}

func TestUnauthorizedAccess(t *testing.T) {
app := setupTestApp()
routes := []struct{ method, path string }{
{"POST", "/api/v1/datasets/upload"},
{"POST", "/api/v1/validations/create"},
{"GET", "/api/v1/validations/val_123"},
}
for _, r := range routes {
t.Run(r.path, func(t *testing.T) {
req := httptest.NewRequest(r.method, r.path, nil)
resp, err := app.Test(req)
require.NoError(t, err)
assert.Equal(t, fiber.StatusUnauthorized, resp.StatusCode)
})
}
}

func TestInvalidToken(t *testing.T) {
app := setupTestApp()
req := httptest.NewRequest("GET", "/api/v1/validations/val_123", nil)
req.Header.Set("Authorization", "Bearer invalid.token")
resp, err := app.Test(req)
require.NoError(t, err)
assert.Equal(t, fiber.StatusUnauthorized, resp.StatusCode)
}
