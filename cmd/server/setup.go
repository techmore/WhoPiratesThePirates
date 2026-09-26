package main

import (
	"bufio"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	_ "modernc.org/sqlite"
	"who-pirates-the-pirates/internal/catalog"
	"who-pirates-the-pirates/internal/state"
)

func runSetup() error {
	home, err := defaultConfigHome()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(home, 0o700); err != nil {
		return err
	}

	catalogPath := filepath.Join(home, "catalog.sqlite")
	statePath := filepath.Join(home, "app_state.sqlite")
	if _, err := os.Stat(catalogPath); errors.Is(err, os.ErrNotExist) {
		if err := createEmptyCatalog(catalogPath); err != nil {
			return err
		}
		fmt.Printf("created empty catalog %s\n", catalogPath)
	} else if err != nil {
		return err
	} else {
		fmt.Printf("keeping existing catalog %s\n", catalogPath)
	}

	st, err := state.Open(statePath)
	if err != nil {
		return err
	}
	if err := st.Close(); err != nil {
		return err
	}

	envPath := filepath.Join(home, "service.env")
	if _, err := os.Stat(envPath); errors.Is(err, os.ErrNotExist) {
		secret, err := randomSecret()
		if err != nil {
			return err
		}
		contents := strings.Join([]string{
			"APP_DB_PATH=" + catalogPath,
			"APP_STATE_PATH=" + statePath,
			"APP_BIND_ADDR=127.0.0.1",
			"PORT=8080",
			"ADMIN_PASSWORD=",
			"ADMIN_SESSION_SECRET=" + secret,
			"",
		}, "\n")
		if err := os.WriteFile(envPath, []byte(contents), 0o600); err != nil {
			return err
		}
		fmt.Printf("created %s; ADMIN_PASSWORD is optional for local loopback/container mode\n", envPath)
	} else if err != nil {
		return err
	} else {
		fmt.Printf("keeping existing configuration %s\n", envPath)
	}

	fmt.Println("setup complete")
	if runtime.GOOS == "darwin" {
		fmt.Println("next: optionally set ADMIN_PASSWORD, then run: brew services start whop2p")
	} else {
		fmt.Println("next: optionally set ADMIN_PASSWORD, then run: whop2p")
	}
	return nil
}

func runLoadCatalog(source string) error {
	if err := validateCatalog(source); err != nil {
		return fmt.Errorf("catalog validation failed: %w", err)
	}
	home, err := defaultConfigHome()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(home, 0o700); err != nil {
		return err
	}
	sourceAbs, err := filepath.Abs(source)
	if err != nil {
		return err
	}
	destination := filepath.Join(home, "catalog.sqlite")
	destinationAbs, err := filepath.Abs(destination)
	if err != nil {
		return err
	}
	if sourceAbs == destinationAbs {
		return fmt.Errorf("source and destination are the same file: %s", destination)
	}

	if _, err := os.Stat(destination); err == nil {
		backup := fmt.Sprintf("catalog.sqlite.backup-%s", time.Now().UTC().Format("20060102T150405Z"))
		if err := os.Rename(destination, filepath.Join(home, backup)); err != nil {
			return fmt.Errorf("back up existing catalog: %w", err)
		}
		fmt.Printf("backed up existing catalog to %s\n", filepath.Join(home, backup))
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}

	temporary, err := os.CreateTemp(home, ".catalog-*.tmp")
	if err != nil {
		return err
	}
	temporaryName := temporary.Name()
	defer os.Remove(temporaryName)
	input, err := os.Open(sourceAbs)
	if err != nil {
		_ = temporary.Close()
		return err
	}
	if _, err := io.Copy(temporary, input); err != nil {
		_ = input.Close()
		_ = temporary.Close()
		return err
	}
	if err := input.Close(); err != nil {
		_ = temporary.Close()
		return err
	}
	if err := temporary.Chmod(0o600); err != nil {
		_ = temporary.Close()
		return err
	}
	if err := temporary.Sync(); err != nil {
		_ = temporary.Close()
		return err
	}
	if err := temporary.Close(); err != nil {
		return err
	}
	if err := os.Rename(temporaryName, destination); err != nil {
		return err
	}
	fmt.Printf("installed catalog %s\n", destination)
	fmt.Println("restart the service to read the new catalog")
	return nil
}

func validateCatalog(path string) error {
	return catalog.Validate(path)
}

func runOpen() error {
	if err := loadServiceEnv(); err != nil {
		return err
	}
	target := strings.TrimSpace(os.Getenv("WHOP2P_URL"))
	if target == "" {
		bind := strings.TrimSpace(os.Getenv("APP_BIND_ADDR"))
		if bind == "" || bind == "0.0.0.0" || bind == "::" {
			bind = "127.0.0.1"
		}
		port := strings.TrimSpace(os.Getenv("PORT"))
		if port == "" {
			port = "8080"
		}
		target = "http://" + joinHostPort(bind, port)
	}

	client := &http.Client{Timeout: 750 * time.Millisecond}
	response, err := client.Get(target + "/healthz")
	if err != nil {
		return fmt.Errorf("server is not ready at %s: %w", target, err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return fmt.Errorf("server health check returned %s", response.Status)
	}

	command := "xdg-open"
	if runtime.GOOS == "darwin" {
		command = "open"
	}
	if _, err := exec.LookPath(command); err != nil {
		return fmt.Errorf("cannot open browser: %w", err)
	}
	return exec.Command(command, target).Start()
}

func loadServiceEnv() error {
	home, err := defaultConfigHome()
	if err != nil {
		return err
	}
	file, err := os.Open(filepath.Join(home, "service.env"))
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	defer file.Close()

	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		key, value, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		key = strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(key), "export "))
		value = strings.TrimSpace(value)
		if key != "" && os.Getenv(key) == "" {
			_ = os.Setenv(key, value)
		}
	}
	return scanner.Err()
}

func defaultConfigHome() (string, error) {
	if value := strings.TrimSpace(os.Getenv("WHOP2P_HOME")); value != "" {
		return value, nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".config", "whop2p"), nil
}

func createEmptyCatalog(path string) error {
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return err
	}
	defer db.Close()
	statements := []string{
		`create table categories (id integer primary key, name text)`,
		`create table torrents (id integer primary key, category integer, status text, name text, numFiles integer, size real, seeders integer, leechers integer, username text, added integer, description text, imdb text, language text, textLanguage text, infoHash text)`,
		`create table files (id integer primary key, parentTorrentId integer, name text, size real)`,
		`create table yts_movies (id integer primary key)`,
		`create table yts_torrent_data (id integer primary key)`,
	}
	for _, statement := range statements {
		if _, err := db.Exec(statement); err != nil {
			return err
		}
	}
	return nil
}

func randomSecret() (string, error) {
	buffer := make([]byte, 32)
	if _, err := rand.Read(buffer); err != nil {
		return "", err
	}
	return hex.EncodeToString(buffer), nil
}

func joinHostPort(host, port string) string {
	if strings.Contains(host, ":") && !strings.HasPrefix(host, "[") {
		return "[" + host + "]:" + port
	}
	return host + ":" + port
}
