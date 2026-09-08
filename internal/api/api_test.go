package api

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/oona-insurance/dev-portal/internal/auth"
)

func init() {
	// Initialize test secrets
	_ = auth.SetJWTSecret([]byte("12345678901234567890123456789012"))
	_ = auth.SetEncryptionKey([]byte("12345678901234567890123456789012"))
}

func TestHealthAndProbes(t *testing.T) {
	router := SetupRouter()
	ts := httptest.NewServer(router)
	defer ts.Close()

	// 1. Test /health
	resp, err := http.Get(ts.URL + "/health")
	if err != nil {
		t.Fatalf("Failed to GET /health: %v", err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Errorf("Expected status 200 for /health, got %d", resp.StatusCode)
	}

	// 2. Test /live
	respLive, err := http.Get(ts.URL + "/live")
	if err != nil {
		t.Fatalf("Failed to GET /live: %v", err)
	}
	if respLive.StatusCode != http.StatusOK {
		t.Errorf("Expected status 200 for /live, got %d", respLive.StatusCode)
	}

	// 3. Test Security Headers
	if resp.Header.Get("X-Frame-Options") != "DENY" {
		t.Errorf("Expected X-Frame-Options: DENY, got %s", resp.Header.Get("X-Frame-Options"))
	}
	if resp.Header.Get("X-Content-Type-Options") != "nosniff" {
		t.Errorf("Expected X-Content-Type-Options: nosniff, got %s", resp.Header.Get("X-Content-Type-Options"))
	}
}

func TestAuthAndCSRFProtection(t *testing.T) {
	router := SetupRouter()
	ts := httptest.NewServer(router)
	defer ts.Close()

	client := &http.Client{
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			return http.ErrUseLastResponse // Do not follow redirects
		},
	}

	// 1. Unauthorized access to /dashboard without cookie should redirect to /login when browser sends Accept: text/html
	req, _ := http.NewRequest("GET", ts.URL+"/dashboard", nil)
	req.Header.Set("Accept", "text/html,application/xhtml+xml")
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("Failed to GET /dashboard: %v", err)
	}
	if resp.StatusCode != http.StatusFound && resp.StatusCode != http.StatusSeeOther {
		t.Errorf("Expected redirect 302/303 for unauth /dashboard, got %d", resp.StatusCode)
	}

	// 2. Generate valid JWT auth token & CSRF token
	token, _ := auth.GenerateToken("test-user-id", "dev@oona-insurance.com", "developer")
	csrfToken := GenerateCSRFToken()

	authCookie := &http.Cookie{
		Name:  "oona_token",
		Value: token,
		Path:  "/",
	}
	csrfCookie := &http.Cookie{
		Name:  "oona_csrf",
		Value: csrfToken,
		Path:  "/",
	}

	// 3. Authenticated GET /dashboard with cookie should succeed (HTTP 200)
	reqAuth, _ := http.NewRequest("GET", ts.URL+"/dashboard", nil)
	reqAuth.AddCookie(authCookie)
	respAuth, err := client.Do(reqAuth)
	if err != nil {
		t.Fatalf("Failed to GET /dashboard with auth: %v", err)
	}
	if respAuth.StatusCode != http.StatusOK {
		t.Errorf("Expected status 200 for authenticated /dashboard, got %d", respAuth.StatusCode)
	}

	// 4. Mutation POST without CSRF header should be blocked (HTTP 403)
	ticketBody := strings.NewReader(`{"domain":"integration","country":"ph","service_name":"test-svc","repo_url":"https://github.com/test/repo"}`)
	reqMutNoCSRF, _ := http.NewRequest("POST", ts.URL+"/api/v1/tickets", ticketBody)
	reqMutNoCSRF.Header.Set("Content-Type", "application/json")
	reqMutNoCSRF.AddCookie(authCookie)
	reqMutNoCSRF.AddCookie(csrfCookie)
	respMutNoCSRF, err := client.Do(reqMutNoCSRF)
	if err != nil {
		t.Fatalf("Failed POST /api/v1/tickets: %v", err)
	}
	if respMutNoCSRF.StatusCode != http.StatusForbidden {
		t.Errorf("Expected HTTP 403 Forbidden for POST without CSRF header, got %d", respMutNoCSRF.StatusCode)
	}

	// 5. Mutation POST with valid CSRF header should succeed (HTTP 201)
	ticketBodyValid := strings.NewReader(`{"domain":"integration","country":"ph","service_name":"test-svc","repo_url":"https://github.com/test/repo"}`)
	reqMutValid, _ := http.NewRequest("POST", ts.URL+"/api/v1/tickets", ticketBodyValid)
	reqMutValid.Header.Set("Content-Type", "application/json")
	reqMutValid.Header.Set("X-CSRF-Token", csrfToken)
	reqMutValid.AddCookie(authCookie)
	reqMutValid.AddCookie(csrfCookie)
	respMutValid, err := client.Do(reqMutValid)
	if err != nil {
		t.Fatalf("Failed POST /api/v1/tickets with valid CSRF: %v", err)
	}
	if respMutValid.StatusCode != http.StatusCreated {
		t.Errorf("Expected HTTP 201 Created for POST with valid CSRF, got %d", respMutValid.StatusCode)
	}
}

func TestRBACAdminEnforcement(t *testing.T) {
	router := SetupRouter()
	ts := httptest.NewServer(router)
	defer ts.Close()

	client := &http.Client{}

	// Developer token (role: developer)
	devToken, _ := auth.GenerateToken("dev-user", "dev@oona-insurance.com", "developer")
	devCookie := &http.Cookie{Name: "oona_token", Value: devToken, Path: "/"}

	// Developer trying to access Admin Users page -> should return 403
	req, _ := http.NewRequest("GET", ts.URL+"/admin/users", nil)
	req.AddCookie(devCookie)
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("Failed GET /admin/users: %v", err)
	}
	if resp.StatusCode != http.StatusForbidden {
		t.Errorf("Expected HTTP 403 Forbidden for dev accessing /admin/users, got %d", resp.StatusCode)
	}

	// Admin token (role: admin)
	adminToken, _ := auth.GenerateToken("admin-user", "admin@oona-insurance.com", "admin")
	adminCookie := &http.Cookie{Name: "oona_token", Value: adminToken, Path: "/"}

	// Admin accessing Admin Users page -> should return 200
	reqAdmin, _ := http.NewRequest("GET", ts.URL+"/admin/users", nil)
	reqAdmin.AddCookie(adminCookie)
	respAdmin, err := client.Do(reqAdmin)
	if err != nil {
		t.Fatalf("Failed GET /admin/users: %v", err)
	}
	if respAdmin.StatusCode != http.StatusOK {
		t.Errorf("Expected HTTP 200 OK for admin accessing /admin/users, got %d", respAdmin.StatusCode)
	}
}
