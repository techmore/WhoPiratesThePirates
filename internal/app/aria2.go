package app

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

type aria2Status struct {
	Installed bool   `json:"installed"`
	Path      string `json:"path,omitempty"`
	Version   string `json:"version,omitempty"`
}

func detectAria2() aria2Status {
	return detectAria2At(resolveAria2Path())
}

func detectAria2At(name string) aria2Status {
	if strings.TrimSpace(name) == "" {
		return aria2Status{}
	}
	path, err := exec.LookPath(name)
	if err != nil {
		return aria2Status{}
	}
	status := aria2Status{Installed: true, Path: path}
	output, err := exec.Command(path, "--version").CombinedOutput()
	if err == nil {
		for _, line := range strings.Split(string(output), "\n") {
			line = strings.TrimSpace(line)
			if line != "" {
				status.Version = line
				break
			}
		}
	}
	return status
}

func resolveAria2Path() string {
	if configured := strings.TrimSpace(os.Getenv("APP_ARIA2_PATH")); configured != "" {
		return configured
	}
	if path, err := exec.LookPath("aria2c"); err == nil {
		return path
	}
	for _, candidate := range []string{
		"/opt/homebrew/bin/aria2c",
		"/usr/local/bin/aria2c",
		"/usr/bin/aria2c",
	} {
		if info, err := os.Stat(candidate); err == nil && !info.IsDir() {
			return filepath.Clean(candidate)
		}
	}
	return ""
}
