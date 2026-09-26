package app

// CatalogPreset is a curated recovery profile shown in the operator's
// first-run workflow. A preset without a Magnet is intentionally a profile
// only: an administrator must supply an authorized source before approving it.
type CatalogPreset struct {
	ID          string
	Name        string
	Description string
	Magnet      string
}

const pirateBayYTSRecoveryMagnet = "magnet:?xt=urn:btih:0D4CD209E72F28023692DFCA65345AA508F9BF7A&dn=The%20Pirate%20Bay%20%26amp%3B%20YTS%20-%20Full%20Database%20Backup%20-%202024-06&tr=udp%3A%2F%2Ftracker.opentrackr.org%3A1337&tr=udp%3A%2F%2Fopen.stealth.si%3A80%2Fannounce&tr=udp%3A%2F%2Ftracker.torrent.eu.org%3A451%2Fannounce&tr=udp%3A%2F%2Ftracker.bittor.pw%3A1337%2Fannounce&tr=udp%3A%2F%2Fpublic.popcorn-tracker.org%3A6969%2Fannounce&tr=udp%3A%2F%2Ftracker.dler.org%3A6969%2Fannounce&tr=udp%3A%2F%2Fexodus.desync.com%3A6969&tr=udp%3A%2F%2Fopen.demonii.com%3A1337%2Fannounce&tr=udp%3A%2F%2Fglotorrents.pw%3A6969%2Fannounce&tr=udp%3A%2F%2Ftracker.coppersurfer.tk%3A6969&tr=udp%3A%2F%2Ftorrent.gresille.org%3A80%2Fannounce&tr=udp%3A%2F%2Fp4p.arenabg.com%3A1337&tr=udp%3A%2F%2Ftracker.internetwarriors.net%3A1337"

func defaultCatalogPresets() []CatalogPreset {
	return []CatalogPreset{
		{
			ID:          "pirate-bay-yts-2024-06",
			Name:        "The Pirate Bay & YTS database backup (2024-06)",
			Description: "The existing recovered backup profile. Its authorized magnet is configured and can run the full download, extraction, validation, indexing, and load workflow.",
			Magnet:      pirateBayYTSRecoveryMagnet,
		},
		{
			ID:          "open-source-software",
			Name:        "Open-source software and Linux distributions",
			Description: "Profile for operating-system images, package mirrors, developer tools, and other openly licensed software catalogs.",
		},
		{
			ID:          "public-domain-video",
			Name:        "Public-domain movies and television",
			Description: "Profile for catalogs containing public-domain film and television material with documented provenance.",
		},
		{
			ID:          "creative-commons-video",
			Name:        "Creative Commons video",
			Description: "Profile for openly licensed video collections where the source license is recorded with the backup.",
		},
		{
			ID:          "public-domain-books",
			Name:        "Public-domain books and ebooks",
			Description: "Profile for public-domain text, ebook, and scanned-book catalogs.",
		},
		{
			ID:          "open-audiobooks-podcasts",
			Name:        "Open audiobooks and podcasts",
			Description: "Profile for public-domain audiobooks and openly licensed podcast archives.",
		},
		{
			ID:          "open-music-audio",
			Name:        "Open music and audio archives",
			Description: "Profile for Creative Commons, public-domain, and permissioned audio catalogs.",
		},
		{
			ID:          "research-datasets",
			Name:        "Academic and research datasets",
			Description: "Profile for reproducible research data and public datasets with documented access terms.",
		},
		{
			ID:          "open-educational-resources",
			Name:        "Open educational resources",
			Description: "Profile for openly licensed textbooks, course materials, and classroom media.",
		},
		{
			ID:          "open-source-games-mods",
			Name:        "Open-source games and game mods",
			Description: "Profile for open-source games, mods, maps, and development assets.",
		},
		{
			ID:          "web-archive-snapshots",
			Name:        "Web and archive snapshots",
			Description: "Profile for authorized website, documentation, and archive snapshots.",
		},
		{
			ID:          "organization-backup-catalog",
			Name:        "Personal or organizational backup catalog",
			Description: "Profile for a private, permissioned backup maintained by the operator or their organization.",
		},
		{
			ID:          "custom-catalog",
			Name:        "Custom catalog",
			Description: "Blank profile for an administrator-managed catalog with its own documented source and license.",
		},
	}
}

func catalogPresetByID(id string) (CatalogPreset, bool) {
	for _, preset := range defaultCatalogPresets() {
		if preset.ID == id {
			return preset, true
		}
	}
	return CatalogPreset{}, false
}
