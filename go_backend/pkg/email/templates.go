package email

import "fmt"

// emailWrapper wraps email content in a consistent branded template
func emailWrapper(content string) string {
	return `<!DOCTYPE html>
<html lang="en">
<head><meta charset="UTF-8"><meta name="viewport" content="width=device-width, initial-scale=1.0"></head>
<body style="margin:0;padding:0;background-color:#f4f4f7;font-family:-apple-system,BlinkMacSystemFont,'Segoe UI',Roboto,Helvetica,Arial,sans-serif;">
<table role="presentation" width="100%" cellpadding="0" cellspacing="0" style="background-color:#f4f4f7;">
<tr><td align="center" style="padding:40px 20px;">
<table role="presentation" width="100%" cellpadding="0" cellspacing="0" style="max-width:560px;background-color:#ffffff;border-radius:8px;overflow:hidden;box-shadow:0 2px 8px rgba(0,0,0,0.08);">
<!-- Header -->
<tr><td style="background:linear-gradient(135deg,#1a1a2e 0%,#16213e 100%);padding:28px 40px;text-align:center;">
<span style="font-size:24px;font-weight:700;color:#ffffff;letter-spacing:1px;">Synthos</span>
</td></tr>
<!-- Content -->
<tr><td style="padding:36px 40px;">` + content + `</td></tr>
<!-- Footer -->
<tr><td style="padding:24px 40px;background-color:#f9fafb;border-top:1px solid #e5e7eb;text-align:center;">
<p style="margin:0 0 8px;font-size:12px;color:#9ca3af;">Synthos &mdash; A Genovo Technologies Company</p>
<p style="margin:0;font-size:11px;color:#d1d5db;">You received this email because you have an account at synthos.dev. If you believe this was sent in error, please contact support.</p>
</td></tr>
</table>
</td></tr>
</table>
</body>
</html>`
}

// buttonHTML returns a styled CTA button
func buttonHTML(text, href string) string {
	return fmt.Sprintf(`<table role="presentation" cellpadding="0" cellspacing="0" style="margin:28px auto;">
<tr><td align="center" style="background-color:#2563eb;border-radius:6px;">
<a href="%s" target="_blank" style="display:inline-block;padding:14px 32px;font-size:15px;font-weight:600;color:#ffffff;text-decoration:none;letter-spacing:0.3px;">%s</a>
</td></tr>
</table>`, href, text)
}

// VerificationOTPEmail returns the subject and HTML for an email verification OTP
func VerificationOTPEmail(name, otp string) (subject, html string) {
	greeting := "Hi"
	if name != "" {
		greeting = fmt.Sprintf("Hi %s", name)
	}
	content := fmt.Sprintf(`<p style="margin:0 0 16px;font-size:15px;color:#374151;">%s,</p>
<p style="margin:0 0 24px;font-size:15px;color:#374151;">Please use the following code to verify your email address:</p>
<table role="presentation" cellpadding="0" cellspacing="0" style="margin:0 auto 24px;">
<tr><td style="background-color:#f0f4ff;border:2px solid #2563eb;border-radius:8px;padding:16px 40px;text-align:center;">
<span style="font-size:32px;font-weight:700;letter-spacing:8px;color:#1e40af;">%s</span>
</td></tr>
</table>
<p style="margin:0 0 8px;font-size:14px;color:#6b7280;">This code expires in <strong>10 minutes</strong>.</p>
<p style="margin:0;font-size:14px;color:#6b7280;">If you did not create an account, you can safely ignore this email.</p>`, greeting, otp)

	return "Verify your email - Synthos", emailWrapper(content)
}

// PasswordResetEmail returns the subject and HTML for a password reset email
func PasswordResetEmail(name, resetLink string) (subject, html string) {
	greeting := "Hi"
	if name != "" {
		greeting = fmt.Sprintf("Hi %s", name)
	}
	content := fmt.Sprintf(`<p style="margin:0 0 16px;font-size:15px;color:#374151;">%s,</p>
<p style="margin:0 0 8px;font-size:15px;color:#374151;">We received a request to reset your password. Click the button below to choose a new password:</p>
%s
<p style="margin:0 0 8px;font-size:14px;color:#6b7280;">This link expires in <strong>1 hour</strong>.</p>
<p style="margin:0;font-size:14px;color:#6b7280;">If you did not request a password reset, please ignore this email. Your password will remain unchanged.</p>`, greeting, buttonHTML("Reset Password", resetLink))

	return "Reset your password - Synthos", emailWrapper(content)
}

// InviteEmail returns the subject and HTML for a team invite email
func InviteEmail(inviterName, role, inviteLink string) (subject, html string) {
	roleDisplay := role
	if roleDisplay == "" {
		roleDisplay = "member"
	}
	inviterLine := "You have been invited"
	if inviterName != "" {
		inviterLine = fmt.Sprintf("%s has invited you", inviterName)
	}
	content := fmt.Sprintf(`<p style="margin:0 0 16px;font-size:15px;color:#374151;">%s to join <strong>Synthos</strong> as a <strong>%s</strong>.</p>
<p style="margin:0 0 8px;font-size:15px;color:#374151;">Synthos is an AI-powered data validation platform that helps teams ensure data quality and integrity.</p>
%s
<p style="margin:0;font-size:14px;color:#6b7280;">This invitation expires in <strong>7 days</strong>. If you believe this was sent in error, you can safely ignore it.</p>`, inviterLine, roleDisplay, buttonHTML("Accept Invitation", inviteLink))

	return "You've been invited to join Synthos", emailWrapper(content)
}

// WelcomeEmail returns the subject and HTML for a welcome email after verification
func WelcomeEmail(name string) (subject, html string) {
	greeting := "Welcome!"
	if name != "" {
		greeting = fmt.Sprintf("Welcome, %s!", name)
	}
	content := fmt.Sprintf(`<p style="margin:0 0 16px;font-size:18px;font-weight:600;color:#1e40af;">%s</p>
<p style="margin:0 0 16px;font-size:15px;color:#374151;">Your email has been verified and your Synthos account is now active.</p>
<p style="margin:0 0 24px;font-size:15px;color:#374151;">Here is what you can do next:</p>
<table role="presentation" cellpadding="0" cellspacing="0" style="margin:0 0 24px;width:100%%;">
<tr><td style="padding:8px 0;font-size:14px;color:#374151;">1. Upload a dataset for validation</td></tr>
<tr><td style="padding:8px 0;font-size:14px;color:#374151;">2. Run AI-powered validation checks</td></tr>
<tr><td style="padding:8px 0;font-size:14px;color:#374151;">3. Get detailed quality reports and recommendations</td></tr>
</table>
%s
<p style="margin:0;font-size:14px;color:#6b7280;">If you have any questions, our support team is here to help.</p>`, greeting, buttonHTML("Go to Dashboard", "https://synthos.dev/dashboard"))

	return "Welcome to Synthos", emailWrapper(content)
}

// TicketReplyEmail returns the subject and HTML for a support ticket reply notification
func TicketReplyEmail(ticketSubject, replierName, message string) (subject, html string) {
	content := fmt.Sprintf(`<p style="margin:0 0 16px;font-size:15px;color:#374151;"><strong>%s</strong> replied to your support ticket:</p>
<p style="margin:0 0 8px;font-size:13px;color:#6b7280;text-transform:uppercase;letter-spacing:0.5px;">Ticket: %s</p>
<table role="presentation" cellpadding="0" cellspacing="0" style="width:100%%;margin:0 0 24px;">
<tr><td style="background-color:#f9fafb;border-left:3px solid #2563eb;padding:16px 20px;border-radius:0 4px 4px 0;">
<p style="margin:0;font-size:14px;color:#374151;line-height:1.6;white-space:pre-wrap;">%s</p>
</td></tr>
</table>
%s`, replierName, ticketSubject, message, buttonHTML("View Ticket", "https://synthos.dev/support"))

	return fmt.Sprintf("New reply on: %s", ticketSubject), emailWrapper(content)
}

// ValidationCompleteEmail returns the subject and HTML for a validation completion notification
func ValidationCompleteEmail(name, datasetName string, riskScore int) (subject, html string) {
	greeting := "Hi"
	if name != "" {
		greeting = fmt.Sprintf("Hi %s", name)
	}

	// Determine risk level color
	riskColor := "#16a34a" // green
	riskLabel := "Low Risk"
	if riskScore >= 70 {
		riskColor = "#dc2626" // red
		riskLabel = "High Risk"
	} else if riskScore >= 40 {
		riskColor = "#f59e0b" // amber
		riskLabel = "Medium Risk"
	}

	content := fmt.Sprintf(`<p style="margin:0 0 16px;font-size:15px;color:#374151;">%s,</p>
<p style="margin:0 0 24px;font-size:15px;color:#374151;">Your validation for <strong>%s</strong> has completed.</p>
<table role="presentation" cellpadding="0" cellspacing="0" style="margin:0 auto 24px;text-align:center;">
<tr><td style="padding:20px 40px;background-color:#f9fafb;border-radius:8px;border:1px solid #e5e7eb;">
<p style="margin:0 0 4px;font-size:13px;color:#6b7280;text-transform:uppercase;letter-spacing:0.5px;">Risk Score</p>
<p style="margin:0 0 4px;font-size:40px;font-weight:700;color:%s;">%d</p>
<p style="margin:0;font-size:14px;font-weight:600;color:%s;">%s</p>
</td></tr>
</table>
%s`, greeting, datasetName, riskColor, riskScore, riskColor, riskLabel, buttonHTML("View Full Report", "https://synthos.dev/validations"))

	return fmt.Sprintf("Validation complete: %s", datasetName), emailWrapper(content)
}
