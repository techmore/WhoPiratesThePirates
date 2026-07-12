package importer

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"testing"
)

func TestValidateManifest(t *testing.T) {
	dir := t.TempDir()
	first := filepath.Join(dir, "first.txt")
	second := filepath.Join(dir, "second.txt")
	if err := os.WriteFile(first, []byte("alpha\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(second, []byte("beta\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	sum := sha256.Sum256([]byte("alpha\nbeta\n"))
	result, err := ValidateManifest(Manifest{
		Name:       "Authorized bundle",
		ApprovedBy: "owner",
		Checksum:   hex.EncodeToString(sum[:]),
		Files:      []string{"first.txt", "second.txt"},
	}, dir)
	if err != nil {
		t.Fatal(err)
	}
	if result.TotalBytes == 0 || len(result.Files) != 2 || result.PreviewChecksum != hex.EncodeToString(sum[:]) {
		t.Fatalf("unexpected validation result: %#v", result)
	}
}

func TestValidateManifestRejectsEscape(t *testing.T) {
	_, err := ValidateManifest(Manifest{
		Name:  "bad",
		Files: []string{"../outside"},
	}, t.TempDir())
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestValidateManifestRejectsSymlinkEscape(t *testing.T) {
	baseDir := t.TempDir()
	outside := filepath.Join(t.TempDir(), "outside.txt")
	if err := os.WriteFile(outside, []byte("outside"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(baseDir, "linked.txt")); err != nil {
		t.Fatal(err)
	}
	_, err := ValidateManifest(Manifest{Name: "bad", Files: []string{"linked.txt"}}, baseDir)
	if err == nil {
		t.Fatal("expected symlink escape to be rejected")
	}
}
