package email

import (
	"bytes"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"time"
)

var defaultClient *EmailClient

// EmailClient handles sending emails via the Resend API
type EmailClient struct {
	apiKey     string
	fromEmail  string
	fromName   string
	httpClient *http.Client
}

// SendEmailRequest is the payload sent to the Resend API
type SendEmailRequest struct {
	From    string   `json:"from"`
	To      []string `json:"to"`
	Subject string   `json:"subject"`
	Html    string   `json:"html"`
}

// SendEmailResponse is the response from the Resend API
type SendEmailResponse struct {
	ID string `json:"id"`
}

// Init initializes the default email client at startup
func Init() {
	defaultClient = NewEmailClient()
	if defaultClient.IsConfigured() {
		log.Println("Email client configured (Resend)")
	} else {
		log.Println("Warning: RESEND_API_KEY not set, email sending is disabled")
	}
}

// GetClient returns the default email client
func GetClient() *EmailClient {
	if defaultClient == nil {
		defaultClient = NewEmailClient()
	}
	return defaultClient
}

// NewEmailClient creates a new EmailClient from environment variables
func NewEmailClient() *EmailClient {
	apiKey := os.Getenv("RESEND_API_KEY")
	fromEmail := os.Getenv("EMAIL_FROM_ADDRESS")
	if fromEmail == "" {
		fromEmail = "noreply@synthos.dev"
	}
	fromName := os.Getenv("EMAIL_FROM_NAME")
	if fromName == "" {
		fromName = "Synthos"
	}
	return &EmailClient{
		apiKey:    apiKey,
		fromEmail: fromEmail,
		fromName:  fromName,
		httpClient: &http.Client{Timeout: 10 * time.Second},
	}
}

// IsConfigured returns true if the email client has an API key set
func (c *EmailClient) IsConfigured() bool {
	return c.apiKey != ""
}

// Send sends an email via the Resend API
func (c *EmailClient) Send(to, subject, htmlBody string) error {
	if !c.IsConfigured() {
		return fmt.Errorf("email client not configured: RESEND_API_KEY is empty")
	}

	req := SendEmailRequest{
		From:    fmt.Sprintf("%s <%s>", c.fromName, c.fromEmail),
		To:      []string{to},
		Subject: subject,
		Html:    htmlBody,
	}

	body, err := json.Marshal(req)
	if err != nil {
		return fmt.Errorf("failed to marshal email request: %w", err)
	}

	httpReq, err := http.NewRequest("POST", "https://api.resend.com/emails", bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("failed to create request: %w", err)
	}

	httpReq.Header.Set("Authorization", "Bearer "+c.apiKey)
	httpReq.Header.Set("Content-Type", "application/json")

	resp, err := c.httpClient.Do(httpReq)
	if err != nil {
		return fmt.Errorf("failed to send email: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		var errBody map[string]interface{}
		json.NewDecoder(resp.Body).Decode(&errBody)
		return fmt.Errorf("resend API error (status %d): %v", resp.StatusCode, errBody)
	}

	return nil
}
