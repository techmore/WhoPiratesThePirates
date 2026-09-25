package app

import (
	"os"
	"path/filepath"
	"testing"
)

func TestDetectAria2AtMissingBinary(t *testing.T) {
	status := detectAria2At(filepath.Join(t.TempDir(), "missing-aria2c"))
	if status.Installed || status.Path != "" || status.Version != "" {
		t.Fatalf("unexpected missing aria2 status: %#v", status)
	}
}

func TestDetectAria2AtReportsVersion(t *testing.T) {
	path := filepath.Join(t.TempDir(), "aria2c")
	if err := os.WriteFile(path, []byte("#!/bin/sh\necho 'aria2 version 1.99.0'\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	status := detectAria2At(path)
	if !status.Installed || status.Path != path || status.Version != "aria2 version 1.99.0" {
		t.Fatalf("unexpected aria2 status: %#v", status)
	}
}
