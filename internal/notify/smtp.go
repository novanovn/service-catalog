package notify

import (
	"crypto/tls"
	"fmt"
	"net/smtp"
	"os"
)

// SendEmail sends a simple email using Oona's SMTP server
func SendEmail(toEmail, subject, body string) error {
	smtpHost := os.Getenv("SMTP_HOST") // e.g., email-smtp.ap-southeast-3.amazonaws.com
	smtpPort := os.Getenv("SMTP_PORT") // e.g., 587 or 465
	smtpUser := os.Getenv("SMTP_USER")
	smtpPass := os.Getenv("SMTP_PASS")
	fromEmail := os.Getenv("SMTP_FROM") // e.g., dev-portal@oona-insurance.com

	if smtpHost == "" || smtpUser == "" {
		fmt.Println("Warning: SMTP credentials not set. Skipping Email notification.")
		return nil
	}

	// Craft the email message
	header := make(map[string]string)
	header["From"] = fromEmail
	header["To"] = toEmail
	header["Subject"] = subject
	header["MIME-Version"] = "1.0"
	header["Content-Type"] = "text/html; charset=\"utf-8\""

	message := ""
	for k, v := range header {
		message += fmt.Sprintf("%s: %s\r\n", k, v)
	}
	message += "\r\n" + body

	auth := smtp.PlainAuth("", smtpUser, smtpPass, smtpHost)

	// In corporate environments, TLS config might need adjustments (e.g., InsecureSkipVerify if using internal CA)
	tlsconfig := &tls.Config{
		InsecureSkipVerify: false,
		ServerName:         smtpHost,
	}

	conn, err := tls.Dial("tcp", smtpHost+":"+smtpPort, tlsconfig)
	if err != nil {
		// Fallback to STARTTLS if standard TLS dial fails
		return sendStartTLS(smtpHost, smtpPort, auth, fromEmail, []string{toEmail}, []byte(message))
	}
	defer conn.Close()

	client, err := smtp.NewClient(conn, smtpHost)
	if err != nil {
		return err
	}

	if err = client.Auth(auth); err != nil {
		return err
	}

	if err = client.Mail(fromEmail); err != nil {
		return err
	}
	if err = client.Rcpt(toEmail); err != nil {
		return err
	}

	w, err := client.Data()
	if err != nil {
		return err
	}

	_, err = w.Write([]byte(message))
	if err != nil {
		return err
	}

	err = w.Close()
	if err != nil {
		return err
	}

	return client.Quit()
}

// sendStartTLS handles standard port 587 STARTTLS mechanism
func sendStartTLS(host, port string, auth smtp.Auth, from string, to []string, msg []byte) error {
	addr := host + ":" + port
	return smtp.SendMail(addr, auth, from, to, msg)
}
