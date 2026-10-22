package main

import (
	"log"
	"net/http"
	"if3110-web-application-development/authn"
	"if3110-web-application-development/views/pages"

	"github.com/a-h/templ"
)

func main() {
	fs := http.FileServer(http.Dir("./static"))
	http.Handle("/static/", http.StripPrefix("/static/", fs))

	// Handler Tunggal untuk /login (GET & POST)
	http.HandleFunc("/login", func(w http.ResponseWriter, r *http.Request) {
		// Jika request GET, render Halaman Login
		if r.Method == http.MethodGet {
			templ.Handler(pages.Login("", "")).ServeHTTP(w, r)
			return
		}

		// Jika request POST, proses data login
		if r.Method == http.MethodPost {
			username := r.FormValue("username")
			password := r.FormValue("password")

			// Simulasi Validasi Login
			if username == "admin" && password == "admin123" {
				w.Header().Set("HX-Redirect", "/dashboard")
				w.WriteHeader(http.StatusOK)
				return
			}

			// Jika gagal, tetap kirim StatusOK agar HTMX melakukan swap komponen error
			w.WriteHeader(http.StatusOK)
			pages.LoginError("Username atau password salah!").Render(r.Context(), w)
			return
		}

		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
	})

	// Redirect root / ke /login
	http.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/" {
			http.Redirect(w, r, "/login", http.StatusSeeOther)
			return
		}
		http.NotFound(w, r)
	})

	// Route /dashboard
	http.HandleFunc("/dashboard", func(w http.ResponseWriter, r *http.Request) {
		// Simulasi data respon dari Auth Context
		dummyUser := &authn.AuthLayoutUser{
			ID:              "usr-123",
			Username:        "Moreno",
			Role:            "PLATFORM_ADMIN",
			IsPlatformAdmin: true,
			Databases: []authn.DatabaseAccessSummary{
				{DatabaseID: "db-1", DatabaseName: "PostgreSQL Utama", DBMS: "POSTGRESQL", EffectiveAccess: "VIEW_EDIT"},
				{DatabaseID: "db-2", DatabaseName: "MySQL Production", DBMS: "MYSQL", EffectiveAccess: "VIEW_ONLY"},
			},
		}

		templ.Handler(pages.Dashboard(dummyUser, "db-1")).ServeHTTP(w, r)
	})

	// Route /logout
	http.HandleFunc("/logout", func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost {
			w.Header().Set("HX-Redirect", "/login")
			w.WriteHeader(http.StatusOK)
			return
		}
	})

	log.Println("Server berjalan di http://localhost:8080")
	if err := http.ListenAndServe(":8080", nil); err != nil {
		log.Fatal(err)
	}
}