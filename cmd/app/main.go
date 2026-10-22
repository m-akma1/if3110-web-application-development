package main

import (
	"log"
	"net/http"
	"if3110-web-application-development/views/pages"

	"github.com/a-h/templ"
)

func main() {
	fs := http.FileServer(http.Dir("./static"))
	http.Handle("/static/", http.StripPrefix("/static/", fs))

	// Handler Tunggal untuk /login (GET & POST)
	http.HandleFunc("/login", func(w http.ResponseWriter, r *http.Request) {
		// 1. Jika request GET, render Halaman Login
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

	log.Println("Server berjalan di http://localhost:8080")
	if err := http.ListenAndServe(":8080", nil); err != nil {
		log.Fatal(err)
	}
}