package app

import (
	"io"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
)

type aria2Status struct {
	Installed bool   `json:"installed"`
	Path      string `json:"path,omitempty"`
	Version   string `json:"version,omitempty"`
}

type aria2Progress struct {
	CompletedBytes int64
	TotalBytes     int64
	SpeedBytes     int64
	ETASeconds     int64
	Progress       float64
}

var (
	aria2TransferPattern = regexp.MustCompile(`([0-9]+([.][0-9]+)?)\s*([A-Za-z]+)\s*/\s*([0-9]+([.][0-9]+)?)\s*([A-Za-z]+)\s*\(([0-9]+([.][0-9]+)?)%\)`)
	aria2SpeedPattern    = regexp.MustCompile(`\bDL:\s*([0-9]+([.][0-9]+)?)\s*([A-Za-z]+)`)
	aria2ETAPattern      = regexp.MustCompile(`\bETA:\s*([0-9dhms:]+)`)
)

func parseAria2ProgressLine(line string) (aria2Progress, bool) {
	transfer := aria2TransferPattern.FindStringSubmatch(line)
	if len(transfer) == 0 {
		return aria2Progress{}, false
	}
	completed, ok := parseAria2Size(transfer[1], transfer[3])
	if !ok {
		return aria2Progress{}, false
	}
	total, ok := parseAria2Size(transfer[4], transfer[6])
	if !ok {
		return aria2Progress{}, false
	}
	percent, err := strconv.ParseFloat(transfer[7], 64)
	if err != nil {
		return aria2Progress{}, false
	}
	progress := aria2Progress{
		CompletedBytes: completed,
		TotalBytes:     total,
		Progress:       percent,
	}
	if speed := aria2SpeedPattern.FindStringSubmatch(line); len(speed) > 0 {
		progress.SpeedBytes, _ = parseAria2Size(speed[1], speed[3])
	}
	if eta := aria2ETAPattern.FindStringSubmatch(line); len(eta) > 1 {
		progress.ETASeconds = parseAria2ETA(eta[1])
	}
	return progress, true
}

func parseAria2Size(value, unit string) (int64, bool) {
	number, err := strconv.ParseFloat(strings.TrimSpace(value), 64)
	if err != nil || number < 0 {
		return 0, false
	}
	multiplier := float64(1)
	switch strings.ToUpper(strings.TrimSpace(unit)) {
	case "B":
	case "K", "KB", "KIB":
		multiplier = 1 << 10
	case "M", "MB", "MIB":
		multiplier = 1 << 20
	case "G", "GB", "GIB":
		multiplier = 1 << 30
	case "T", "TB", "TIB":
		multiplier = 1 << 40
	default:
		return 0, false
	}
	bytes := number * multiplier
	if bytes > float64(math.MaxInt64) {
		return 0, false
	}
	return int64(bytes + 0.5), true
}

func parseAria2ETA(value string) int64 {
	value = strings.TrimSpace(value)
	if value == "" || value == "--" {
		return 0
	}
	if strings.Contains(value, ":") {
		parts := strings.Split(value, ":")
		if len(parts) > 3 {
			return 0
		}
		var total int64
		for i, part := range parts {
			seconds, err := strconv.ParseInt(part, 10, 64)
			if err != nil || seconds < 0 {
				return 0
			}
			power := len(parts) - i - 1
			for j := 0; j < power; j++ {
				seconds *= 60
			}
			total += seconds
		}
		return total
	}

	var total int64
	var number int64
	seen := false
	for _, char := range value {
		switch {
		case char >= '0' && char <= '9':
			number = number*10 + int64(char-'0')
			seen = true
		case char == 'd' || char == 'h' || char == 'm' || char == 's':
			if !seen {
				return 0
			}
			multiplier := int64(1)
			switch char {
			case 'd':
				multiplier = 24 * 60 * 60
			case 'h':
				multiplier = 60 * 60
			case 'm':
				multiplier = 60
			}
			total += number * multiplier
			number = 0
			seen = false
		default:
			return 0
		}
	}
	if seen {
		return 0
	}
	return total
}

type aria2OutputWriter struct {
	mu         sync.Mutex
	log        io.Writer
	pending    string
	onProgress func(aria2Progress)
}

func newAria2OutputWriter(log io.Writer, onProgress func(aria2Progress)) *aria2OutputWriter {
	return &aria2OutputWriter{log: log, onProgress: onProgress}
}

func (w *aria2OutputWriter) Write(data []byte) (int, error) {
	w.mu.Lock()
	n, err := w.log.Write(data)
	if n > 0 {
		w.pending += string(data[:n])
	}
	updates := w.consumeLocked(false)
	w.mu.Unlock()
	w.emit(updates)
	return n, err
}

func (w *aria2OutputWriter) Flush() {
	w.mu.Lock()
	updates := w.consumeLocked(true)
	w.mu.Unlock()
	w.emit(updates)
}

func (w *aria2OutputWriter) consumeLocked(flush bool) []aria2Progress {
	updates := make([]aria2Progress, 0, 1)
	for {
		index := strings.IndexAny(w.pending, "\r\n")
		if index < 0 {
			break
		}
		line := w.pending[:index]
		w.pending = w.pending[index+1:]
		if progress, ok := parseAria2ProgressLine(line); ok {
			updates = append(updates, progress)
		}
	}
	if flush && strings.TrimSpace(w.pending) != "" {
		if progress, ok := parseAria2ProgressLine(w.pending); ok {
			updates = append(updates, progress)
		}
		w.pending = ""
	}
	return updates
}

func (w *aria2OutputWriter) emit(updates []aria2Progress) {
	if w.onProgress == nil {
		return
	}
	for _, progress := range updates {
		w.onProgress(progress)
	}
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

func resolveArchiveExtractor() string {
	if configured := strings.TrimSpace(os.Getenv("APP_ARCHIVE_EXTRACTOR")); configured != "" {
		return configured
	}
	for _, name := range []string{"7z", "7zz"} {
		if path, err := exec.LookPath(name); err == nil {
			return path
		}
	}
	for _, candidate := range []string{
		"/opt/homebrew/bin/7z",
		"/opt/homebrew/bin/7zz",
		"/usr/local/bin/7z",
		"/usr/local/bin/7zz",
		"/usr/bin/7z",
		"/usr/bin/7zz",
	} {
		if info, err := os.Stat(candidate); err == nil && !info.IsDir() {
			return filepath.Clean(candidate)
		}
	}
	return ""
}
