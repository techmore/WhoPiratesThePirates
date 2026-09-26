package app

import (
	"fmt"
	"html"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"

	"who-pirates-the-pirates/internal/catalog"
)

// resumeReferenceRecovery picks up artifacts that were downloaded before the
// process restarted. Older reference rows do not have a persisted status, so
// the artifact scan is intentionally part of startup recovery as well.
func (a *App) resumeReferenceRecovery() {
	items, err := a.state.ImportReferences(100)
	if err != nil {
		return
	}
	for _, item := range items {
		if item.RecoveryStatus == "loaded" && item.RecoveredPath != "" {
			if _, err := os.Stat(item.RecoveredPath); err == nil {
				continue
			}
		}
		artifact, err := a.findReferenceArtifact(item.Name, item.DownloadDir)
		if err != nil || artifact == "" {
			continue
		}
		a.recoverReference(item.ID, item.Reference, item.Name, item.DownloadDir)
	}
}

func (a *App) recoverReference(id int64, reference, name, preferredDir string) {
	a.recoveryMu.Lock()
	defer a.recoveryMu.Unlock()

	item, err := a.state.ImportReference(id)
	if err != nil {
		return
	}
	if item.RecoveryStatus == "loaded" && item.RecoveredPath != "" {
		if _, err := os.Stat(item.RecoveredPath); err == nil {
			return
		}
	}
	a.setRecoveryStatus(id, "recovering", "Looking for the completed catalog download.", "", "")
	artifact, err := a.findReferenceArtifact(name, preferredDir)
	if err != nil {
		a.setRecoveryStatus(id, "recovering", "Waiting for a readable downloaded artifact.", "", err.Error())
		return
	}
	if artifact == "" {
		a.setRecoveryStatus(id, "recovering", "Waiting for a complete SQLite database or supported archive.", "", "")
		return
	}
	if item.DownloadStatus != "completed" {
		downloadDir := item.DownloadDir
		if strings.TrimSpace(downloadDir) == "" {
			downloadDir = preferredDir
		}
		completed := downloadStatus{
			Status:         "completed",
			Directory:      downloadDir,
			RecoveryStatus: "recovering",
			RecoveryDetail: "Downloaded artifact found; continuing with catalog recovery.",
			CompletedBytes: item.DownloadCompletedBytes,
			TotalBytes:     item.DownloadTotalBytes,
			SpeedBytes:     0,
			ETASeconds:     0,
			Progress:       item.DownloadProgress,
		}
		if info, statErr := os.Stat(artifact); statErr == nil && info.Size() > 0 && completed.TotalBytes == 0 {
			completed.CompletedBytes = info.Size()
			completed.TotalBytes = info.Size()
			completed.Progress = 100
		}
		a.setDownloadStatus(id, completed)
	}

	extractionDetail := fmt.Sprintf("Using downloaded SQLite database %s.", filepath.Base(artifact))
	if !isCatalogArtifact(artifact) {
		extractionDetail = fmt.Sprintf("Extracting %s with 7z.", filepath.Base(artifact))
	}
	a.setRecoveryStatus(id, "extracting", extractionDetail, artifact, "")
	candidates, err := a.recoveredCatalogCandidates(id, artifact)
	if err != nil {
		a.failRecovery(id, artifact, err)
		return
	}

	a.setRecoveryStatus(id, "validating", fmt.Sprintf("Checking SQLite integrity and schema across %d candidate file(s).", len(candidates)), "", "")
	var catalogPath string
	var validationErr error
	for index, candidate := range candidates {
		a.setRecoveryStatus(id, "validating", fmt.Sprintf("Validating candidate %d of %d: %s.", index+1, len(candidates), filepath.Base(candidate)), candidate, "")
		if err := catalog.Validate(candidate); err == nil {
			catalogPath = candidate
			break
		} else {
			validationErr = err
		}
	}
	if catalogPath == "" {
		if validationErr == nil {
			validationErr = fmt.Errorf("no SQLite catalog found in downloaded artifact")
		}
		a.failRecovery(id, artifact, validationErr)
		return
	}
	a.setRecoveryStatus(id, "validated", fmt.Sprintf("SQLite catalog validated: %s. Preparing the live load.", filepath.Base(catalogPath)), catalogPath, "")

	recoveryName := recoveryDisplayName(name, artifact)
	magnet := ""
	if item.Kind == "magnet" {
		magnet = reference
	}
	if err := a.state.EnsureCatalogSource(recoveryName, magnet, catalogPath, true); err != nil {
		a.failRecovery(id, catalogPath, fmt.Errorf("register recovery source: %w", err))
		return
	}

	a.setRecoveryStatus(id, "loading", "Building the search index and switching the active catalog. This may take a while for large databases.", catalogPath, "")
	if err := a.replaceCatalogValidated(catalogPath, recoveryName); err != nil {
		a.failRecovery(id, catalogPath, err)
		return
	}

	loadedDetail := "Database loaded and searchable."
	if stats, statsErr := a.catalogStats(); statsErr == nil {
		loadedDetail = fmt.Sprintf("Database loaded: %d torrents, %d files, %d categories. Search is ready.", stats.Torrents, stats.Files, stats.Categories)
	}
	a.setRecoveryStatus(id, "loaded", loadedDetail, catalogPath, "")
	_ = a.state.Audit("import_reference_recovery_loaded", fmt.Sprintf("id=%d name=%s path=%s", id, recoveryName, catalogPath))
}

func (a *App) failRecovery(id int64, artifact string, err error) {
	a.setRecoveryStatus(id, "failed", "Recovery stopped. Fix the error and retry the catalog load.", artifact, err.Error())
	_ = a.state.Audit("import_reference_recovery_failed", fmt.Sprintf("id=%d artifact=%s error=%v", id, artifact, err))
}

func (a *App) setRecoveryStatus(id int64, status, detail, path, recoveryError string) {
	_ = a.state.UpdateImportReferenceRecovery(id, status, detail, path, recoveryError)
	a.downloadMu.Lock()
	if current, ok := a.downloads[id]; ok {
		current.RecoveryStatus = status
		current.RecoveryDetail = detail
		current.RecoveredPath = path
		current.RecoveryError = recoveryError
		a.downloads[id] = current
	}
	a.downloadMu.Unlock()
}

func (a *App) findReferenceArtifact(name, preferredDir string) (string, error) {
	roots := make([]string, 0, 2)
	if strings.TrimSpace(preferredDir) != "" {
		roots = append(roots, preferredDir)
	}
	if a.downloadDir != "" && filepath.Clean(preferredDir) != filepath.Clean(a.downloadDir) {
		roots = append(roots, a.downloadDir)
	}
	nameKey := recoveryNameKey(name)
	var all []string
	for _, root := range roots {
		candidates, err := artifactFiles(root)
		if err != nil {
			return "", err
		}
		if len(candidates) == 0 {
			continue
		}
		all = append(all, candidates...)
		if nameKey != "" {
			matched := make([]string, 0, len(candidates))
			for _, candidate := range candidates {
				candidateKey := recoveryNameKey(filepath.Base(candidate))
				if strings.Contains(candidateKey, nameKey) || strings.Contains(nameKey, candidateKey) {
					matched = append(matched, candidate)
				}
			}
			if len(matched) > 0 {
				return newestArtifact(matched), nil
			}
		}
		if len(candidates) == 1 {
			return candidates[0], nil
		}
	}
	if len(all) == 1 {
		return all[0], nil
	}
	return "", nil
}

func artifactFiles(root string) ([]string, error) {
	if strings.TrimSpace(root) == "" {
		return nil, nil
	}
	if _, err := os.Stat(root); os.IsNotExist(err) {
		return nil, nil
	} else if err != nil {
		return nil, err
	}
	var candidates []string
	err := filepath.WalkDir(root, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() {
			if entry.Name() != filepath.Base(root) && strings.HasPrefix(entry.Name(), ".") {
				return filepath.SkipDir
			}
			return nil
		}
		if !entry.Type().IsRegular() {
			return nil
		}
		lower := strings.ToLower(entry.Name())
		if strings.HasSuffix(lower, ".aria2") || strings.HasSuffix(lower, ".log") || strings.HasPrefix(entry.Name(), ".") {
			return nil
		}
		candidates = append(candidates, path)
		return nil
	})
	if err != nil {
		return nil, err
	}
	return candidates, nil
}

func newestArtifact(paths []string) string {
	if len(paths) == 0 {
		return ""
	}
	sort.SliceStable(paths, func(i, j int) bool {
		left, leftErr := os.Stat(paths[i])
		right, rightErr := os.Stat(paths[j])
		if leftErr != nil || rightErr != nil {
			return paths[i] > paths[j]
		}
		return left.ModTime().After(right.ModTime())
	})
	return paths[0]
}

func (a *App) recoveredCatalogCandidates(id int64, artifact string) ([]string, error) {
	if isCatalogArtifact(artifact) {
		return []string{artifact}, nil
	}
	if !isArchiveArtifact(artifact) {
		return nil, fmt.Errorf("downloaded file is not a supported SQLite catalog or archive: %s", filepath.Base(artifact))
	}
	if strings.TrimSpace(a.archiveExtractor) == "" {
		return nil, fmt.Errorf("archive extractor is unavailable; install 7z or set APP_ARCHIVE_EXTRACTOR")
	}
	if err := os.MkdirAll(a.recoveryDir, 0o700); err != nil {
		return nil, fmt.Errorf("create recovery directory: %w", err)
	}
	destination := filepath.Join(a.recoveryDir, fmt.Sprintf("reference-%d", id))
	if err := os.MkdirAll(destination, 0o700); err != nil {
		return nil, fmt.Errorf("create extraction directory: %w", err)
	}
	if existing, err := catalogFiles(destination); err != nil {
		return nil, err
	} else if len(existing) > 0 {
		return existing, nil
	}
	command := exec.Command(a.archiveExtractor, "x", "-y", "-o"+destination, artifact)
	output, err := command.CombinedOutput()
	if err != nil {
		return nil, fmt.Errorf("extract archive: %w: %s", err, strings.TrimSpace(string(output)))
	}
	candidates, err := catalogFiles(destination)
	if err != nil {
		return nil, err
	}
	if len(candidates) == 0 {
		return nil, fmt.Errorf("archive extracted successfully but no SQLite catalog file was found")
	}
	return candidates, nil
}

func catalogFiles(root string) ([]string, error) {
	var candidates []string
	if _, err := os.Stat(root); os.IsNotExist(err) {
		return candidates, nil
	} else if err != nil {
		return nil, err
	}
	err := filepath.WalkDir(root, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() {
			return nil
		}
		if !entry.Type().IsRegular() || !isCatalogArtifact(path) {
			return nil
		}
		candidates = append(candidates, path)
		return nil
	})
	return candidates, err
}

func isCatalogArtifact(path string) bool {
	lower := strings.ToLower(path)
	return strings.HasSuffix(lower, ".sqlite") || strings.HasSuffix(lower, ".sqlite3") || strings.HasSuffix(lower, ".db")
}

func isArchiveArtifact(path string) bool {
	lower := strings.ToLower(path)
	for _, suffix := range []string{".7z", ".zip", ".tar", ".tar.gz", ".tgz", ".gz", ".bz2", ".xz"} {
		if strings.HasSuffix(lower, suffix) {
			return true
		}
	}
	return false
}

func recoveryNameKey(value string) string {
	value = strings.ToLower(html.UnescapeString(value))
	var builder strings.Builder
	for _, char := range value {
		if (char >= 'a' && char <= 'z') || (char >= '0' && char <= '9') {
			builder.WriteRune(char)
		}
	}
	return builder.String()
}

func recoveryDisplayName(name, artifact string) string {
	name = strings.TrimSpace(html.UnescapeString(name))
	if name != "" {
		return name
	}
	base := filepath.Base(artifact)
	for _, suffix := range []string{".tar.gz", ".tgz", ".7z", ".zip", ".tar", ".gz", ".bz2", ".xz", ".sqlite", ".sqlite3", ".db"} {
		if strings.HasSuffix(strings.ToLower(base), suffix) {
			base = base[:len(base)-len(suffix)]
			break
		}
	}
	return strings.TrimSpace(base)
}
