package main

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"who-pirates-the-pirates/internal/app"
)

var version = "dev"

func main() {
	if len(os.Args) == 2 {
		switch os.Args[1] {
		case "--version", "-version":
			fmt.Println(version)
			return
		case "setup":
			if err := runSetup(); err != nil {
				log.Fatal(err)
			}
			return
		case "open":
			if err := runOpen(); err != nil {
				log.Fatal(err)
			}
			return
		}
	}
	if err := loadServiceEnv(); err != nil {
		log.Printf("warning: could not load service environment: %v", err)
	}

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

	bindAddr := os.Getenv("APP_BIND_ADDR")
	if bindAddr == "" {
		bindAddr = "127.0.0.1"
	}
	tlsCertFile := os.Getenv("APP_TLS_CERT_FILE")
	tlsKeyFile := os.Getenv("APP_TLS_KEY_FILE")
	tlsEnabled := tlsCertFile != "" || tlsKeyFile != ""
	if tlsEnabled && (tlsCertFile == "" || tlsKeyFile == "") {
		log.Fatal("APP_TLS_CERT_FILE and APP_TLS_KEY_FILE must be configured together")
	}
	allowInsecureHTTP := envEnabled(os.Getenv("APP_ALLOW_INSECURE_HTTP"))
	if !tlsEnabled && !isLoopbackAddress(bindAddr) && !allowInsecureHTTP {
		log.Fatalf("refusing insecure HTTP on non-loopback address %q; configure TLS or set APP_ALLOW_INSECURE_HTTP=true explicitly", bindAddr)
	}
	if !tlsEnabled && !isLoopbackAddress(bindAddr) {
		log.Printf("warning: serving admin credentials over insecure HTTP on %s", bindAddr)
	}

	svc, err := app.NewWithOptions(dbPath, statePath, app.Options{
		SecureCookies: tlsEnabled || envEnabled(os.Getenv("APP_COOKIE_SECURE")),
	})
	if err != nil {
		log.Fatal(err)
	}

	addr := net.JoinHostPort(bindAddr, port)
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
	go func() {
		if tlsEnabled {
			errCh <- server.ListenAndServeTLS(tlsCertFile, tlsKeyFile)
			return
		}
		errCh <- server.ListenAndServe()
	}()
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

func isLoopbackAddress(value string) bool {
	host := strings.TrimSpace(value)
	if strings.EqualFold(host, "localhost") {
		return true
	}
	host = strings.TrimPrefix(strings.TrimSuffix(host, "]"), "[")
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

func envEnabled(value string) bool {
	return value == "1" || strings.EqualFold(strings.TrimSpace(value), "true")
}
