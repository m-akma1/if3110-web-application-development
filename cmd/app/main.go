package main

import (
	"log"
	"net/http"
	"if3110-web-application-development/views"

	"github.com/a-h/templ"
)

func main() {
	// Serve file statis (CSS/JS)
	fs := http.FileServer(http.Dir("./static"))
	http.Handle("/static/", http.StripPrefix("/static/", fs))

	// Render Halaman Utama
	http.Handle("/", templ.Handler(views.Hello()))

	// Endpoint Test HTMX Partial Response
	http.HandleFunc("/api/ping", func(w http.ResponseWriter, r *http.Request) {
		views.PingResult().Render(r.Context(), w)
	})

	log.Println("Server berjalan di http://localhost:8080")
	if err := http.ListenAndServe(":8080", nil); err != nil {
		log.Fatal(err)
	}
}