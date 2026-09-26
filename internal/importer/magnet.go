package importer

import (
	"fmt"
	"html"
	"net/url"
	"strings"
)

// ExternalReference is metadata parsed from an operator-supplied magnet or
// torrent URL. Callers decide whether an authorized reference is downloaded.
type ExternalReference struct {
	Kind      string   `json:"kind"`
	Reference string   `json:"reference"`
	InfoHash  string   `json:"infoHash,omitempty"`
	Name      string   `json:"name,omitempty"`
	Trackers  []string `json:"trackers,omitempty"`
}

func ParseExternalReference(raw string) (ExternalReference, error) {
	raw = normalizeExternalReference(raw)
	if raw == "" {
		return ExternalReference{}, fmt.Errorf("magnet or .torrent URL is required")
	}
	parsed, err := url.Parse(raw)
	if err != nil {
		return ExternalReference{}, fmt.Errorf("invalid magnet or torrent URL: %w", err)
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
		return ExternalReference{}, fmt.Errorf("input must be a magnet link or an https:// .torrent URL")
	}
}

func normalizeExternalReference(raw string) string {
	raw = html.UnescapeString(strings.TrimSpace(raw))
	for range 2 {
		lower := strings.ToLower(raw)
		if strings.HasPrefix(lower, "magnet:") || strings.HasPrefix(lower, "http://") || strings.HasPrefix(lower, "https://") {
			break
		}
		decoded, err := url.PathUnescape(raw)
		if err != nil || decoded == raw {
			break
		}
		raw = html.UnescapeString(strings.TrimSpace(decoded))
	}
	raw = strings.Trim(raw, "\"'`<>")
	if index := strings.Index(strings.ToLower(raw), "magnet:"); index > 0 {
		raw = raw[index:]
	}
	if strings.HasPrefix(strings.ToLower(raw), "?xt=urn:btih:") {
		raw = "magnet:" + raw
	}
	if strings.HasPrefix(strings.ToLower(raw), "xt=urn:btih:") {
		raw = "magnet:?" + raw
	}
	return strings.Trim(raw, "\"'`<>")
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
