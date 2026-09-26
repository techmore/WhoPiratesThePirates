package app

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
)

func TestParseAria2ProgressLine(t *testing.T) {
	progress, ok := parseAria2ProgressLine("[#43c689 16384B/13107360B(0%) CN:1 DL:16384B ETA:13m19s]")
	if !ok {
		t.Fatal("expected aria2 progress line to parse")
	}
	if progress.CompletedBytes != 16384 || progress.TotalBytes != 13107360 || progress.SpeedBytes != 16384 || progress.ETASeconds != 799 || progress.Progress != 0 {
		t.Fatalf("unexpected parsed progress: %#v", progress)
	}

	progress, ok = parseAria2ProgressLine("[#43c689 1.5MiB/2GiB(12.5%) CN:1 DL:512KiB ETA:01:02]")
	if !ok {
		t.Fatal("expected human-readable aria2 progress line to parse")
	}
	if progress.CompletedBytes != 1572864 || progress.TotalBytes != 2147483648 || progress.SpeedBytes != 524288 || progress.ETASeconds != 62 || progress.Progress != 12.5 {
		t.Fatalf("unexpected human-readable progress: %#v", progress)
	}
}

func TestAria2OutputWriterReportsProgress(t *testing.T) {
	var log bytes.Buffer
	var updates []aria2Progress
	writer := newAria2OutputWriter(&log, func(progress aria2Progress) {
		updates = append(updates, progress)
	})
	if _, err := writer.Write([]byte("[#gid 512B/1024B(50%) CN:1 DL:128B ETA:4s]")); err != nil {
		t.Fatal(err)
	}
	if len(updates) != 0 {
		t.Fatalf("expected partial output to wait for a line boundary, got %#v", updates)
	}
	if _, err := writer.Write([]byte("\n")); err != nil {
		t.Fatal(err)
	}
	writer.Flush()
	if len(updates) != 1 || updates[0].CompletedBytes != 512 || updates[0].TotalBytes != 1024 || updates[0].Progress != 50 {
		t.Fatalf("unexpected progress updates: %#v", updates)
	}
	if log.String() != "[#gid 512B/1024B(50%) CN:1 DL:128B ETA:4s]\n" {
		t.Fatalf("expected complete aria2 output in log, got %q", log.String())
	}
}

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
