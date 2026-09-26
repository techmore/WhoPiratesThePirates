package app

import (
	"strings"
	"testing"

	"who-pirates-the-pirates/internal/importer"
)

func TestDefaultCatalogPresets(t *testing.T) {
	presets := defaultCatalogPresets()
	if len(presets) < 10 {
		t.Fatalf("expected a useful default profile list, got %d presets", len(presets))
	}
	seen := make(map[string]bool, len(presets))
	for _, preset := range presets {
		if preset.ID == "" || preset.Name == "" || preset.Description == "" {
			t.Fatalf("preset is missing required metadata: %#v", preset)
		}
		if seen[preset.ID] {
			t.Fatalf("duplicate preset id %q", preset.ID)
		}
		seen[preset.ID] = true
	}

	primary, ok := catalogPresetByID("pirate-bay-yts-2024-06")
	if !ok || !strings.Contains(primary.Name, "The Pirate Bay & YTS") || primary.Magnet == "" {
		t.Fatalf("expected configured primary recovery preset, got %#v", primary)
	}
	parsed, err := importer.ParseExternalReference(primary.Magnet)
	if err != nil || parsed.InfoHash != "0D4CD209E72F28023692DFCA65345AA508F9BF7A" {
		t.Fatalf("primary preset magnet did not parse as expected: %#v err=%v", parsed, err)
	}

	profileOnly, ok := catalogPresetByID("open-source-software")
	if !ok || profileOnly.Magnet != "" {
		t.Fatalf("profile-only presets must not ship an unapproved source: %#v", profileOnly)
	}
}
