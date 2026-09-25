package importer

import (
	"crypto/sha256"
	"encoding/hex"
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

const (
	DefaultMaxManifestFiles       = 256
	DefaultMaxManifestBytes int64 = 512 * 1024 * 1024
)

type ManifestLimits struct {
	MaxFiles int
	MaxBytes int64
}

var DefaultManifestLimits = ManifestLimits{
	MaxFiles: DefaultMaxManifestFiles,
	MaxBytes: DefaultMaxManifestBytes,
}

// ValidateManifest ensures the import request points to local files,
// normalizes file paths, and validates an optional sha256 checksum.
func ValidateManifest(manifest Manifest, baseDir string) (ValidationResult, error) {
	return ValidateManifestWithLimits(manifest, baseDir, DefaultManifestLimits)
}

func ValidateManifestWithLimits(manifest Manifest, baseDir string, limits ManifestLimits) (ValidationResult, error) {
	if strings.TrimSpace(manifest.Name) == "" {
		return ValidationResult{}, fmt.Errorf("manifest name is required")
	}
	if len(manifest.Files) == 0 {
		return ValidationResult{}, fmt.Errorf("at least one file is required")
	}
	if limits.MaxFiles <= 0 || limits.MaxBytes <= 0 {
		return ValidationResult{}, fmt.Errorf("manifest limits must be positive")
	}
	if len(manifest.Files) > limits.MaxFiles {
		return ValidationResult{}, fmt.Errorf("manifest contains too many files: maximum is %d", limits.MaxFiles)
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
	seen := make(map[string]struct{}, len(manifest.Files))
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
		if _, ok := seen[resolved]; ok {
			return ValidationResult{}, fmt.Errorf("manifest contains duplicate file: %s", rel)
		}
		seen[resolved] = struct{}{}
		info, err := os.Stat(resolved)
		if err != nil {
			return ValidationResult{}, err
		}
		if !info.Mode().IsRegular() {
			return ValidationResult{}, fmt.Errorf("manifest file must be regular: %s", rel)
		}
		remaining := limits.MaxBytes - result.TotalBytes
		if remaining <= 0 {
			return ValidationResult{}, fmt.Errorf("manifest exceeds maximum total size of %d bytes", limits.MaxBytes)
		}

		f, err := os.Open(resolved)
		if err != nil {
			return ValidationResult{}, err
		}
		n, err := io.Copy(hash, io.LimitReader(f, remaining+1))
		_ = f.Close()
		if err != nil {
			return ValidationResult{}, err
		}
		if n > remaining {
			return ValidationResult{}, fmt.Errorf("manifest exceeds maximum total size of %d bytes", limits.MaxBytes)
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
