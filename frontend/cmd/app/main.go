package main

import (
	"bytes"
	"encoding/json"
	"log"
	"net/http"
	"os"
	"time"

	"dbplatform/frontend/authn"
	"dbplatform/frontend/views/pages"

	"github.com/a-h/templ"
)

func getInternalAPIURL() string {
	if url := os.Getenv("INTERNAL_API_URL"); url != "" {
		return url
	}
	return "http://backend:8080"
}

func getPort() string {
	if port := os.Getenv("PORT"); port != "" {
		return port
	}
	return "3000"
}

func NewFrontendMux(internalAPIURL string) *http.ServeMux {
	mux := http.NewServeMux()
	httpClient := &http.Client{Timeout: 5 * time.Second}

	fs := http.FileServer(http.Dir("./static"))
	mux.Handle("/static/", http.StripPrefix("/static/", fs))

	// Handler untuk /login (GET & POST)
	mux.HandleFunc("/login", func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet {
			csrfToken := ""
			req, err := http.NewRequestWithContext(r.Context(), http.MethodGet, internalAPIURL+"/api/csrf-token", nil)
			if err == nil {
				if cookie := r.Header.Get("Cookie"); cookie != "" {
					req.Header.Set("Cookie", cookie)
				}
				if resp, err := httpClient.Do(req); err == nil {
					defer resp.Body.Close()
					for _, c := range resp.Header.Values("Set-Cookie") {
						w.Header().Add("Set-Cookie", c)
					}
					var data struct {
						CSRFToken string `json:"csrf_token"`
					}
					if err := json.NewDecoder(resp.Body).Decode(&data); err == nil {
						csrfToken = data.CSRFToken
					}
				}
			}

			templ.Handler(pages.Login(csrfToken, "")).ServeHTTP(w, r)
			return
		}

		if r.Method == http.MethodPost {
			username := r.FormValue("username")
			password := r.FormValue("password")

			if username == "" || password == "" {
				w.WriteHeader(http.StatusOK)
				_ = pages.LoginError("Username dan password wajib diisi!").Render(r.Context(), w)
				return
			}

			payload, _ := json.Marshal(map[string]string{
				"username": username,
				"password": password,
			})

			req, err := http.NewRequestWithContext(r.Context(), http.MethodPost, internalAPIURL+"/api/auth/login", bytes.NewReader(payload))
			if err != nil {
				w.WriteHeader(http.StatusOK)
				_ = pages.LoginError("Gagal menyiapkan request ke Backend API").Render(r.Context(), w)
				return
			}

			req.Header.Set("Content-Type", "application/json")
			if cookie := r.Header.Get("Cookie"); cookie != "" {
				req.Header.Set("Cookie", cookie)
			}
			if csrf := r.Header.Get("X-CSRF-Token"); csrf != "" {
				req.Header.Set("X-CSRF-Token", csrf)
			}

			resp, err := httpClient.Do(req)
			if err != nil {
				w.WriteHeader(http.StatusOK)
				_ = pages.LoginError("Gagal menghubungi Backend API").Render(r.Context(), w)
				return
			}
			defer resp.Body.Close()

			if resp.StatusCode == http.StatusOK {
				for _, c := range resp.Header.Values("Set-Cookie") {
					w.Header().Add("Set-Cookie", c)
				}
				w.Header().Set("HX-Redirect", "/dashboard")
				w.WriteHeader(http.StatusOK)
				return
			}

			var errResp struct {
				Error string `json:"error"`
			}
			if err := json.NewDecoder(resp.Body).Decode(&errResp); err == nil && errResp.Error != "" {
				w.WriteHeader(http.StatusOK)
				_ = pages.LoginError(errResp.Error).Render(r.Context(), w)
				return
			}

			w.WriteHeader(http.StatusOK)
			_ = pages.LoginError("Username atau password salah!").Render(r.Context(), w)
			return
		}

		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
	})

	// Redirect root / ke /login
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/" {
			http.Redirect(w, r, "/login", http.StatusSeeOther)
			return
		}
		http.NotFound(w, r)
	})

	// Handler untuk /dashboard & /dashboard/select-db (Mengambil profil user dari Backend GET /api/auth/me)
	renderDashboard := func(w http.ResponseWriter, r *http.Request) {
		req, err := http.NewRequestWithContext(r.Context(), http.MethodGet, internalAPIURL+"/api/auth/me", nil)
		if err != nil {
			http.Redirect(w, r, "/login", http.StatusSeeOther)
			return
		}
		if cookie := r.Header.Get("Cookie"); cookie != "" {
			req.Header.Set("Cookie", cookie)
		}

		resp, err := httpClient.Do(req)
		if err != nil || resp.StatusCode != http.StatusOK {
			if resp != nil {
				resp.Body.Close()
			}
			http.Redirect(w, r, "/login", http.StatusSeeOther)
			return
		}
		defer resp.Body.Close()

		var user authn.AuthLayoutUser
		if err := json.NewDecoder(resp.Body).Decode(&user); err != nil {
			http.Redirect(w, r, "/login", http.StatusSeeOther)
			return
		}

		activeDBID := r.URL.Query().Get("db_id")
		if activeDBID == "" && len(user.Databases) > 0 {
			activeDBID = user.Databases[0].DatabaseID
		}

		templ.Handler(pages.Dashboard(&user, activeDBID)).ServeHTTP(w, r)
	}

	mux.HandleFunc("/dashboard", renderDashboard)
	mux.HandleFunc("/dashboard/select-db", renderDashboard)

	// Route /logout (Meneruskan logout ke Backend POST /api/auth/logout & menghapus cookie sesi)
	mux.HandleFunc("/logout", func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost {
			req, err := http.NewRequestWithContext(r.Context(), http.MethodPost, internalAPIURL+"/api/auth/logout", nil)
			if err == nil {
				if cookie := r.Header.Get("Cookie"); cookie != "" {
					req.Header.Set("Cookie", cookie)
				}
				if csrf := r.Header.Get("X-CSRF-Token"); csrf != "" {
					req.Header.Set("X-CSRF-Token", csrf)
				}
				if resp, err := httpClient.Do(req); err == nil {
					resp.Body.Close()
				}
			}

			http.SetCookie(w, &http.Cookie{
				Name:     "__Host-sid",
				Value:    "",
				Path:     "/",
				MaxAge:   -1,
				HttpOnly: true,
				Secure:   true,
				SameSite: http.SameSiteLaxMode,
			})
			w.Header().Set("HX-Redirect", "/login")
			w.WriteHeader(http.StatusOK)
			return
		}
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
	})

	return mux
}

func main() {
	port := getPort()
	internalAPIURL := getInternalAPIURL()
	mux := NewFrontendMux(internalAPIURL)

	log.Printf("SSR Frontend (Go + Templ) berjalan pada :%s (Backend API: %s)\n", port, internalAPIURL)
	if err := http.ListenAndServe(":"+port, mux); err != nil {
		log.Fatal(err)
	}
}
