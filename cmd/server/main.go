package main

import (
	"context"
	"errors"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

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
	server := &http.Server{
		Addr:              addr,
		Handler:           svc.Router(),
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       30 * time.Second,
		WriteTimeout:      30 * time.Second,
		IdleTimeout:       60 * time.Second,
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	errCh := make(chan error, 1)
	go func() { errCh <- server.ListenAndServe() }()
	log.Printf("listening on %s", addr)

	select {
	case err := <-errCh:
		if !errors.Is(err, http.ErrServerClosed) {
			log.Fatal(err)
		}
	case <-ctx.Done():
		log.Printf("shutting down")
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := server.Shutdown(shutdownCtx); err != nil {
			log.Printf("server shutdown: %v", err)
		}
		if err := svc.Close(); err != nil {
			log.Printf("database close: %v", err)
		}
	}
}
