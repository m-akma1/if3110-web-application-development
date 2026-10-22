package main

import (
	"log"
	"net/http"

	apphttp "dbplatform/backend/internal/http"
)

func main() {
	srv := apphttp.NewServer()

	log.Println("Backend API berjalan pada :8080")
	if err := http.ListenAndServe(":8080", srv); err != nil {
		log.Fatalf("server error: %v", err)
	}
}
