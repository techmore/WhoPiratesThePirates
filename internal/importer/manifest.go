package importer

import (
	"bufio"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

// Manifest describes a dataset the operator is authorized to ingest.
// The validator keeps the trust boundary explicit: the importer only
// accepts local files or explicit manifest references.
type Manifest struct {
	Name       string   `json:"name"`
	ApprovedBy string   `json:"approvedBy"`
	Checksum   string   `json:"checksum"`
	Files      []string `json:"files"`
}

type ValidationResult struct {
	Name            string
	Files           []string
	Checksum        string
	TotalBytes      int64
	PreviewChecksum string
}

var ErrMissingManifest = errors.New("manifest is required")

// ValidateManifest ensures the import request points to local files,
// normalizes file paths, and validates an optional sha256 checksum.
func ValidateManifest(manifest Manifest, baseDir string) (ValidationResult, error) {
	if strings.TrimSpace(manifest.Name) == "" {
		return ValidationResult{}, fmt.Errorf("manifest name is required")
	}
	if len(manifest.Files) == 0 {
		return ValidationResult{}, fmt.Errorf("at least one file is required")
	}
	if strings.TrimSpace(baseDir) == "" {
		baseDir = "."
	}

	result := ValidationResult{
		Name:     strings.TrimSpace(manifest.Name),
		Checksum: strings.ToLower(strings.TrimSpace(manifest.Checksum)),
		Files:    make([]string, 0, len(manifest.Files)),
	}

	hash := sha256.New()
	for _, raw := range manifest.Files {
		rel := strings.TrimSpace(raw)
		if rel == "" {
			return ValidationResult{}, fmt.Errorf("empty file entry in manifest")
		}
		if filepath.IsAbs(rel) {
			return ValidationResult{}, fmt.Errorf("manifest file must be relative: %s", rel)
		}
		full := filepath.Clean(filepath.Join(baseDir, rel))
		if !isWithinBase(baseDir, full) {
			return ValidationResult{}, fmt.Errorf("manifest file escapes base directory: %s", rel)
		}
		resolved, err := filepath.EvalSymlinks(full)
		if err != nil {
			return ValidationResult{}, err
		}
		if !isWithinBase(baseDir, resolved) {
			return ValidationResult{}, fmt.Errorf("manifest file resolves outside base directory: %s", rel)
		}

		f, err := os.Open(resolved)
		if err != nil {
			return ValidationResult{}, err
		}
		n, err := io.Copy(hash, f)
		_ = f.Close()
		if err != nil {
			return ValidationResult{}, err
		}
		result.TotalBytes += n
		result.Files = append(result.Files, resolved)
	}

	result.PreviewChecksum = hex.EncodeToString(hash.Sum(nil))
	if result.Checksum != "" && result.PreviewChecksum != result.Checksum {
		return ValidationResult{}, fmt.Errorf("checksum mismatch: expected %s got %s", result.Checksum, result.PreviewChecksum)
	}
	return result, nil
}

func isWithinBase(baseDir, path string) bool {
	baseAbs, err := filepath.Abs(baseDir)
	if err != nil {
		return false
	}
	if resolvedBase, err := filepath.EvalSymlinks(baseAbs); err == nil {
		baseAbs = resolvedBase
	}
	pathAbs, err := filepath.Abs(path)
	if err != nil {
		return false
	}
	if resolvedPath, err := filepath.EvalSymlinks(pathAbs); err == nil {
		pathAbs = resolvedPath
	}
	rel, err := filepath.Rel(baseAbs, pathAbs)
	if err != nil {
		return false
	}
	return rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

// PreviewFileChecksum produces a stable sha256 for the first n lines of a file.
// It is useful for confirming a manifest before doing a full ingest.
func PreviewFileChecksum(path string, lines int) (string, error) {
	if lines <= 0 {
		return "", fmt.Errorf("lines must be positive")
	}
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()

	hash := sha256.New()
	scanner := bufio.NewScanner(f)
	for i := 0; i < lines && scanner.Scan(); i++ {
		_, _ = hash.Write([]byte(scanner.Text()))
		_, _ = hash.Write([]byte{'\n'})
	}
	if err := scanner.Err(); err != nil {
		return "", err
	}
	return hex.EncodeToString(hash.Sum(nil)), nil
}
