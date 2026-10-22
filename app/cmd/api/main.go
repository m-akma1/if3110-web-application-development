package main

import (
	"log"
	"net/http"
	"os"

	apphttp "dbplatform/app/internal/http"
)

func main() {
	srv := apphttp.NewServer(os.Stdout)

	log.Println("Backend API berjalan pada :8080")
	if err := http.ListenAndServe(":8080", srv); err != nil {
		log.Fatalf("server error: %v", err)
	}
}
