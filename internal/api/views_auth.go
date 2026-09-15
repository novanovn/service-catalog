package api

import (
	"fmt"
	"html/template"
	"log"
	"net/http"
	"os"
	"path/filepath"

	"service-catalog/internal/auth"
	"golang.org/x/crypto/bcrypt"
)

func RenderLoginPage(w http.ResponseWriter, r *http.Request) {
	tmpl, err := template.ParseFiles(filepath.Join("internal", "templates", "login.html"))
	if err != nil {
		http.Error(w, "Failed to load login template", http.StatusInternalServerError)
		return
	}
	tmpl.Execute(w, nil)
}

// HandleLogin processes the form login and sets the cookie
func HandleLogin(w http.ResponseWriter, r *http.Request) {
	email := r.FormValue("email")
	password := r.FormValue("password")

	var userID string
	var userEmail string
	var userRole string
	authenticated := false

	// Attempt PostgreSQL database user lookup & bcrypt password verification
	if DB != nil {
		user, err := DB.GetUserByEmail(r.Context(), email)
		if err == nil && user.IsActive {
			if bcrypt.CompareHashAndPassword([]byte(user.PasswordHash), []byte(password)) == nil {
				bytes, errVal := user.ID.Value()
				if errVal == nil && bytes != nil {
					if b, ok := bytes.([]byte); ok && len(b) == 16 {
						userID = fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:16])
					}
				}
				if userID == "" {
					userID = fmt.Sprintf("%x-%x-%x-%x-%x", user.ID.Bytes[0:4], user.ID.Bytes[4:6], user.ID.Bytes[6:8], user.ID.Bytes[8:10], user.ID.Bytes[10:16])
				}
				userEmail = user.Email
				userRole = string(user.Role)
				authenticated = true
			}
		} else if err != nil {
			log.Printf("DB user lookup error for %s: %v", email, err)
		}
	} else {
		log.Println("WARNING: DB is nil. Falling back to mock login verification.")
	}

	// Fallback for UI-Only / Mock Testing Mode strictly when DB is not connected and NOT in production
	if !authenticated && DB == nil && os.Getenv("APP_ENV") != "production" {
		if email == "admin@oona-insurance.com" && password == "admin123" {
			userID = "mock-uuid-1234"
			userEmail = email
			userRole = "admin"
			authenticated = true
		}
	}

	if authenticated {
		// Generate JWT Token
		tokenString, err := auth.GenerateToken(userID, userEmail, userRole)
		if err != nil {
			http.Error(w, "Failed to generate session token", http.StatusInternalServerError)
			return
		}

		isProduction := os.Getenv("APP_ENV") == "production"

		// Set Auth Cookie (HttpOnly — JS cannot read)
		http.SetCookie(w, &http.Cookie{
			Name:     "oona_token",
			Value:    tokenString,
			Path:     "/",
			HttpOnly: true,                 // Prevents XSS accessing the token
			Secure:   isProduction,         // Dynamic based on APP_ENV
			MaxAge:   86400,                // 24 hours
			SameSite: http.SameSiteLaxMode, // Prevents CSRF
		})

		// Set CSRF Cookie (NOT HttpOnly — JS must read to send as header)
		csrfToken := GenerateCSRFToken()
		http.SetCookie(w, &http.Cookie{
			Name:     "oona_csrf",
			Value:    csrfToken,
			Path:     "/",
			HttpOnly: false, // JS needs to read this to set X-CSRF-Token header
			Secure:   isProduction,
			MaxAge:   86400,                   // Same lifetime as auth token
			SameSite: http.SameSiteStrictMode, // Strict: never sent cross-site
		})

		// Redirect to dashboard (HTMX will handle this gracefully)
		w.Header().Set("HX-Redirect", "/dashboard")
		w.WriteHeader(http.StatusOK)
		return
	}

	// Login Failed
	w.Header().Set("Content-Type", "text/html")
	w.Write([]byte(`
		<div class="rounded-md bg-red-50 p-4">
			<div class="flex">
				<div class="flex-shrink-0">
					<svg class="h-5 w-5 text-red-400" viewBox="0 0 20 20" fill="currentColor"><path fill-rule="evenodd" d="M10 18a8 8 0 100-16 8 8 0 000 16zM8.28 7.22a.75.75 0 00-1.06 1.06L8.94 10l-1.72 1.72a.75.75 0 101.06 1.06L10 11.06l1.72 1.72a.75.75 0 101.06-1.06L11.06 10l1.72-1.72a.75.75 0 00-1.06-1.06L10 8.94 8.28 7.22z" clip-rule="evenodd" /></svg>
				</div>
				<div class="ml-3">
					<h3 class="text-sm font-medium text-red-800">Login failed</h3>
					<div class="mt-2 text-sm text-red-700"><p>Invalid email or password.</p></div>
				</div>
			</div>
		</div>
	`))
}

// HandleLogout clears auth and CSRF cookies
func HandleLogout(w http.ResponseWriter, r *http.Request) {
	isProduction := os.Getenv("APP_ENV") == "production"
	// Clear auth token
	http.SetCookie(w, &http.Cookie{
		Name:     "oona_token",
		Value:    "",
		Path:     "/",
		HttpOnly: true,
		Secure:   isProduction,
		MaxAge:   -1,
		SameSite: http.SameSiteLaxMode,
	})
	// Clear CSRF token
	http.SetCookie(w, &http.Cookie{
		Name:     "oona_csrf",
		Value:    "",
		Path:     "/",
		HttpOnly: false,
		Secure:   isProduction,
		MaxAge:   -1,
		SameSite: http.SameSiteStrictMode,
	})
	if r.Header.Get("HX-Request") == "true" {
		w.Header().Set("HX-Redirect", "/login")
		w.WriteHeader(http.StatusOK)
		return
	}
	http.Redirect(w, r, "/login", http.StatusSeeOther)
}

func RenderProfile(w http.ResponseWriter, r *http.Request) {
	tmpl, err := parsePage("profile.html")
	if err != nil {
		http.Error(w, "Failed to load template: "+err.Error(), http.StatusInternalServerError)
		return
	}

	claims, _ := r.Context().Value(userCtxKey).(*auth.Claims)

	data := struct {
		Title string
		User  *auth.Claims
	}{
		Title: "User Profile",
		User:  claims,
	}

	tmpl.ExecuteTemplate(w, "base", data)
}

// RenderSettings renders the user settings page
func RenderSettings(w http.ResponseWriter, r *http.Request) {
	tmpl, err := parsePage("settings.html")
	if err != nil {
		http.Error(w, "Failed to load template: "+err.Error(), http.StatusInternalServerError)
		return
	}

	claims, _ := r.Context().Value(userCtxKey).(*auth.Claims)

	data := struct {
		Title string
		User  *auth.Claims
	}{
		Title: "Settings",
		User:  claims,
	}

	tmpl.ExecuteTemplate(w, "base", data)
}
