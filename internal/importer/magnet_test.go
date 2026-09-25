package importer

import "testing"

func TestParseExternalReferenceMagnet(t *testing.T) {
	ref, err := ParseExternalReference("magnet:?xt=urn:btih:0123456789abcdef0123456789abcdef01234567&dn=Ubuntu%2024.04&tr=udp%3A%2F%2Ftracker.example%3A80")
	if err != nil {
		t.Fatal(err)
	}
	if ref.Kind != "magnet" || ref.InfoHash != "0123456789ABCDEF0123456789ABCDEF01234567" || ref.Name != "Ubuntu 24.04" || len(ref.Trackers) != 1 {
		t.Fatalf("unexpected parsed reference: %#v", ref)
	}
}

func TestParseExternalReferenceTorrentURL(t *testing.T) {
	ref, err := ParseExternalReference("https://authorized.example/files/ubuntu.torrent")
	if err != nil {
		t.Fatal(err)
	}
	if ref.Kind != "torrent-url" || ref.Reference == "" {
		t.Fatalf("unexpected parsed reference: %#v", ref)
	}
}

func TestParseExternalReferenceRejectsUnsupportedInput(t *testing.T) {
	for _, value := range []string{
		"https://example.com/file.bin",
		"magnet:?dn=missing-hash",
		"ftp://example.com/file.torrent",
		"not a reference",
	} {
		if _, err := ParseExternalReference(value); err == nil {
			t.Fatalf("expected %q to be rejected", value)
		}
	}
}
