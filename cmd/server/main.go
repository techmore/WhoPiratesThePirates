package main

import (
	"log"
	"net/http"
	"os"

	"who-pirates-the-pirates/internal/app"
)

func main() {
	dbPath := os.Getenv("APP_DB_PATH")
	if dbPath == "" {
		dbPath = "tpb.sqlite"
	}

	statePath := os.Getenv("APP_STATE_PATH")
	if statePath == "" {
		statePath = "app_state.sqlite"
	}

	port := os.Getenv("PORT")
	if port == "" {
		port = "8080"
	}

	svc, err := app.New(dbPath, statePath)
	if err != nil {
		log.Fatal(err)
	}

	addr := ":" + port
	log.Printf("listening on %s", addr)
	log.Fatal(http.ListenAndServe(addr, svc.Router()))
}
