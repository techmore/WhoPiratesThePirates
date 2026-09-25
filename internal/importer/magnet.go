package importer

import (
	"fmt"
	"net/url"
	"strings"
)

// ExternalReference is metadata parsed from an operator-supplied magnet or
// torrent URL. It never fetches the URL or contacts a tracker.
type ExternalReference struct {
	Kind      string   `json:"kind"`
	Reference string   `json:"reference"`
	InfoHash  string   `json:"infoHash,omitempty"`
	Name      string   `json:"name,omitempty"`
	Trackers  []string `json:"trackers,omitempty"`
}

func ParseExternalReference(raw string) (ExternalReference, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ExternalReference{}, fmt.Errorf("reference is required")
	}
	parsed, err := url.Parse(raw)
	if err != nil {
		return ExternalReference{}, fmt.Errorf("invalid reference: %w", err)
	}
	switch strings.ToLower(parsed.Scheme) {
	case "magnet":
		return parseMagnet(raw, parsed)
	case "http", "https":
		if !strings.HasSuffix(strings.ToLower(parsed.Path), ".torrent") {
			return ExternalReference{}, fmt.Errorf("torrent URL must end in .torrent")
		}
		return ExternalReference{Kind: "torrent-url", Reference: raw}, nil
	default:
		return ExternalReference{}, fmt.Errorf("reference must be a magnet: or https:// .torrent URL")
	}
}

func parseMagnet(raw string, parsed *url.URL) (ExternalReference, error) {
	values := parsed.Query()
	var infoHash string
	for _, xt := range values["xt"] {
		const prefix = "urn:btih:"
		if strings.HasPrefix(strings.ToLower(xt), prefix) {
			candidate := strings.TrimSpace(xt[len(prefix):])
			if validInfoHash(candidate) {
				infoHash = strings.ToUpper(candidate)
				break
			}
		}
	}
	if infoHash == "" {
		return ExternalReference{}, fmt.Errorf("magnet must contain a valid BitTorrent v1 info hash")
	}
	trackers := make([]string, 0)
	for _, tracker := range values["tr"] {
		tracker = strings.TrimSpace(tracker)
		if tracker != "" {
			trackers = append(trackers, tracker)
		}
	}
	return ExternalReference{
		Kind:      "magnet",
		Reference: raw,
		InfoHash:  infoHash,
		Name:      strings.TrimSpace(values.Get("dn")),
		Trackers:  trackers,
	}, nil
}

func validInfoHash(value string) bool {
	if len(value) == 40 {
		for _, r := range value {
			if !(r >= '0' && r <= '9') && !(r >= 'a' && r <= 'f') && !(r >= 'A' && r <= 'F') {
				return false
			}
		}
		return true
	}
	if len(value) == 32 {
		for _, r := range value {
			if !(r >= 'A' && r <= 'Z') && !(r >= '2' && r <= '7') {
				return false
			}
		}
		return true
	}
	return false
}
