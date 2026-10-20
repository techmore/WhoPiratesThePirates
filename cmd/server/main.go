package main

import (
	"context"
	"errors"
	"fmt"
	"io"
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

const (
	exitOK      = 0
	exitFailure = 1
	exitUsage   = 2
)

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

// run dispatches the CLI. Usage errors exit 2, runtime failures exit 1, and
// the server itself only returns once it has shut down.
func run(args []string, stdout, stderr io.Writer) int {
	command := "serve"
	if len(args) > 0 {
		command = args[0]
	}
	rest := args[min(1, len(args)):]

	switch command {
	case "serve":
		if len(rest) != 0 {
			return usageError(stderr, "serve does not take arguments")
		}
		if err := runServe(); err != nil {
			fmt.Fprintf(stderr, "whop2p: %v\n", err)
			return exitFailure
		}
		return exitOK
	case "setup":
		if len(rest) != 0 {
			return usageError(stderr, "setup does not take arguments")
		}
		if err := runSetup(); err != nil {
			fmt.Fprintf(stderr, "whop2p setup: %v\n", err)
			return exitFailure
		}
		return exitOK
	case "load-catalog":
		if len(rest) != 1 {
			return usageError(stderr, "load-catalog requires exactly one argument: the catalog SQLite file")
		}
		if err := runLoadCatalog(rest[0]); err != nil {
			fmt.Fprintf(stderr, "whop2p load-catalog: %v\n", err)
			return exitFailure
		}
		return exitOK
	case "open":
		if len(rest) != 0 {
			return usageError(stderr, "open does not take arguments")
		}
		if err := runOpen(); err != nil {
			fmt.Fprintf(stderr, "whop2p open: %v\n", err)
			return exitFailure
		}
		return exitOK
	case "version", "--version", "-version", "-v":
		fmt.Fprintln(stdout, version)
		return exitOK
	case "help", "--help", "-help", "-h":
		printUsage(stdout)
		return exitOK
	default:
		return usageError(stderr, fmt.Sprintf("unknown command %q", command))
	}
}

func usageError(stderr io.Writer, message string) int {
	fmt.Fprintf(stderr, "whop2p: %s\n\n", message)
	printUsage(stderr)
	return exitUsage
}

func printUsage(w io.Writer) {
	fmt.Fprint(w, `whop2p - private, read-only SQLite torrent catalog browser

Usage:
  whop2p [command]

Commands:
  serve               Start the catalog server (default when no command is given)
  setup               Create the local configuration directory, an empty catalog,
                      an app-state database, and a service.env file
  load-catalog FILE   Validate FILE and install it as the local catalog backup
  open                Check the running server's health and open it in a browser
  version             Print the build version
  help                Show this help

Configuration:
  Settings are read from the environment. Values missing from the environment
  are loaded from $WHOP2P_HOME/service.env (default ~/.config/whop2p/service.env).

  APP_DB_PATH              Read-only catalog database        (default: tpb.sqlite)
  APP_STATE_PATH           Writable application state        (default: app_state.sqlite)
  APP_BIND_ADDR            Listen address                    (default: 127.0.0.1)
  PORT                     Listen port                       (default: 8080)
  ADMIN_PASSWORD           Admin password; unset = local no-password mode
  ADMIN_SESSION_SECRET     Stable HMAC secret for admin sessions
  APP_ALLOWED_HOSTS        Extra Host names allowed to reach the admin surface
                           in no-password mode (comma separated)
  APP_ARIA2_PATH           aria2c executable used for authorized downloads
  APP_ARCHIVE_EXTRACTOR    7z executable used to unpack downloaded archives
  APP_DOWNLOAD_DIR         Directory for started downloads
  APP_TLS_CERT_FILE        TLS certificate (serve HTTPS directly)
  APP_TLS_KEY_FILE         TLS private key
  APP_COOKIE_SECURE        Mark admin cookies Secure behind a TLS proxy (1/true)
  APP_ALLOW_INSECURE_HTTP  Permit plaintext HTTP on a non-loopback address (1/true)
  WHOP2P_HOME              Local configuration directory
  WHOP2P_URL               URL used by "whop2p open"

Exit codes:
  0  success
  1  runtime error (startup failure, validation failure, server error)
  2  usage error (unknown command or bad arguments)
`)
}

func runServe() error {
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

	port := strings.TrimSpace(os.Getenv("PORT"))
	if port == "" {
		port = "8080"
	}
	if err := validatePort(port); err != nil {
		return err
	}

	bindAddr := os.Getenv("APP_BIND_ADDR")
	if bindAddr == "" {
		bindAddr = "127.0.0.1"
	}
	tlsCertFile := os.Getenv("APP_TLS_CERT_FILE")
	tlsKeyFile := os.Getenv("APP_TLS_KEY_FILE")
	tlsEnabled := tlsCertFile != "" || tlsKeyFile != ""
	if tlsEnabled && (tlsCertFile == "" || tlsKeyFile == "") {
		return errors.New("APP_TLS_CERT_FILE and APP_TLS_KEY_FILE must be configured together")
	}
	allowInsecureHTTP := envEnabled(os.Getenv("APP_ALLOW_INSECURE_HTTP"))
	if !tlsEnabled && !isLoopbackAddress(bindAddr) && !allowInsecureHTTP {
		return fmt.Errorf("refusing insecure HTTP on non-loopback address %q; configure TLS or set APP_ALLOW_INSECURE_HTTP=true explicitly", bindAddr)
	}
	if !tlsEnabled && !isLoopbackAddress(bindAddr) {
		log.Printf("warning: serving admin credentials over insecure HTTP on %s", bindAddr)
	}

	svc, err := app.NewWithOptions(dbPath, statePath, app.Options{
		SecureCookies: tlsEnabled || envEnabled(os.Getenv("APP_COOKIE_SECURE")),
		Version:       version,
	})
	if err != nil {
		return err
	}
	defer func() {
		if err := svc.Close(); err != nil {
			log.Printf("database close: %v", err)
		}
	}()

	addr := net.JoinHostPort(bindAddr, port)
	server := &http.Server{
		Addr:    addr,
		Handler: svc.Router(),
		// Per-request deadlines; handlers that legitimately run long (catalog
		// uploads and loads) extend them with http.ResponseController.
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
			return err
		}
		return nil
	case <-ctx.Done():
		log.Printf("shutting down")
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := server.Shutdown(shutdownCtx); err != nil {
			return fmt.Errorf("server shutdown: %w", err)
		}
		return nil
	}
}

func validatePort(value string) error {
	port, err := net.LookupPort("tcp", value)
	if err != nil {
		return fmt.Errorf("invalid PORT %q: %w", value, err)
	}
	if port == 0 {
		return fmt.Errorf("invalid PORT %q: must be between 1 and 65535", value)
	}
	return nil
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
