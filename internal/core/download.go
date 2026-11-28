package core

import (
	"archive/zip"
	"context"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"
)

const (
	defaultReleasesCatalogURL    = "https://raw.githubusercontent.com/dcquijano/Darius-DB-Lite-Releases/main/catalog.json"
	defaultReleasesCatalogAPIURL = "https://api.github.com/repos/dcquijano/Darius-DB-Lite-Releases/contents/catalog.json"
	defaultRemoteCatalogURL      = "https://raw.githubusercontent.com/dcquijano/Darius-DB-Lite-Version/main/internal/core/catalog.json"
	defaultRemoteCatalogAPIURL   = "https://api.github.com/repos/dcquijano/Darius-DB-Lite-Version/contents/internal/core/catalog.json"
)

//go:embed catalog.json
var embeddedCatalog []byte

type CatalogEntry struct {
	Version string `json:"version"`
	URL     string `json:"url"`
	Note    string `json:"note,omitempty"`
}

type Catalog map[string][]CatalogEntry

// LoadCatalog returns catalog.json next to the exe if present (user editable /
// refreshed from the internet), merged with the catalog embedded at build time.
func LoadCatalog(p *Paths) Catalog {
	var base Catalog
	_ = json.Unmarshal(embeddedCatalog, &base)
	if base == nil {
		base = Catalog{}
	}
	embeddedByKey := map[string]CatalogEntry{}
	for eng, entries := range base {
		for i := range entries {
			entries[i].URL = normalizeCatalogURL(eng, entries[i].Version, entries[i].URL)
			embeddedByKey[eng+"@"+entries[i].Version] = entries[i]
		}
		base[eng] = entries
	}
	if p == nil {
		return base
	}
	if b, err := os.ReadFile(filepath.Join(p.Root, "catalog.json")); err == nil {
		var disk Catalog
		if json.Unmarshal(b, &disk) == nil && len(disk) > 0 {
			for eng, entries := range disk {
				if len(entries) == 0 {
					continue
				}
				merged := make([]CatalogEntry, 0, len(entries)+len(base[eng]))
				seen := map[string]bool{}
				for _, e := range entries {
					if e.Version == "" || e.URL == "" {
						continue
					}
					isCustom := strings.EqualFold(strings.TrimSpace(e.Note), "Custom")
					if !isCustom && isKnownDeadCatalogURL(eng, e.URL) {
						// Replace known-dead URLs with embedded equivalent if available, otherwise skip.
						if emb, ok := embeddedByKey[eng+"@"+e.Version]; ok {
							e = emb
						} else {
							continue
						}
					}
					e.URL = normalizeCatalogURL(eng, e.Version, e.URL)
					if !isCustom {
						if emb, ok := embeddedByKey[eng+"@"+e.Version]; ok && emb.URL != "" {
							e.URL = emb.URL
							if e.Note == "" {
								e.Note = emb.Note
							}
						}
					}
					if !seen[e.Version] {
						seen[e.Version] = true
						merged = append(merged, e)
					}
				}
				for _, emb := range base[eng] {
					if !seen[emb.Version] {
						seen[emb.Version] = true
						merged = append(merged, emb)
					}
				}
				if len(merged) > 0 {
					sortCatalogEntries(merged)
					base[eng] = merged
				}
			}
		}
	}
	return base
}

func sortCatalogEntries(list []CatalogEntry) {
	sort.Slice(list, func(i, j int) bool {
		return compareVersions(list[i].Version, list[j].Version) > 0
	})
}

func isKnownDeadCatalogURL(engine, rawURL string) bool {
	low := strings.ToLower(strings.TrimSpace(rawURL))
	switch engine {
	case "apache":
		return strings.Contains(low, "apachelounge.com/download/") && strings.Contains(low, "httpd-2.4.66-260223")
	case "mariadb":
		return strings.Contains(low, "archive.mariadb.org/mariadb-13.0.") || strings.Contains(low, "archive.mariadb.org/mariadb-10.11.")
	default:
		return false
	}
}

func normalizeCatalogURL(engine, version, rawURL string) string {
	u := strings.TrimSpace(rawURL)
	if u == "" {
		return u
	}
	low := strings.ToLower(u)
	switch engine {
	case "mariadb":
		if strings.HasPrefix(low, "http://downloads.mariadb.org/") {
			u = "https://" + u[len("http://"):]
		} else if strings.Contains(low, "archive.mariadb.org/mariadb-") && strings.HasSuffix(low, "-winx64.zip") && version != "" {
			u = fmt.Sprintf("https://downloads.mariadb.org/rest-api/mariadb/%s/mariadb-%s-winx64.zip", version, version)
		}
	case "mysql":
		if strings.EqualFold(u, "https://cdn.mysql.com/Downloads/MySQL-9.7/mysql-9.7.2-winx64.zip") {
			u = "https://cdn.mysql.com/archives/mysql-9.7/mysql-9.7.2-winx64.zip"
		}
	}
	return u
}

// RefreshCatalog discovers the newest engine versions from official upstream
// APIs (Apache Lounge, PHP, MariaDB, MySQL, PostgreSQL, MS SQL, Redis, Mailpit,
// plus GitHub repo catalog and optional custom URL), merges them into catalog.json,
// and returns the updated catalog.
func RefreshCatalog(p *Paths, customURL string) (Catalog, error) {
	return RefreshCatalogWithToken(p, customURL, "")
}

// RefreshCatalogWithToken behaves like RefreshCatalog while attaching an optional GitHub token
// so private repository catalogs can also be refreshed.
func RefreshCatalogWithToken(p *Paths, customURL, githubToken string) (Catalog, error) {
	base := LoadCatalog(p)
	client := &http.Client{Timeout: 12 * time.Second}
	tokens := buildCandidateTokens(githubToken)
	if len(tokens) == 0 {
		tokens = []string{""}
	}

	merge := func(engine string, entry CatalogEntry, authoritative bool) {
		if entry.Version == "" || entry.URL == "" {
			return
		}
		entry.URL = normalizeCatalogURL(engine, entry.Version, entry.URL)
		list := base[engine]
		for i, existing := range list {
			if existing.Version == entry.Version {
				isCustom := strings.EqualFold(strings.TrimSpace(existing.Note), "Custom")
				if authoritative && !isCustom {
					list[i].URL = entry.URL
				}
				if entry.Note != "" && (existing.Note == "" || (authoritative && !isCustom)) {
					list[i].Note = entry.Note
				}
				return
			}
		}
		base[engine] = append(list, entry)
	}

	// 1. Fetch remote catalog JSON (custom URL if set, plus official GitHub repo catalog)
	type catSource struct {
		url    string
		accept string
		custom bool
	}
	sources := []catSource{
		{defaultReleasesCatalogURL, "", false},
		{defaultReleasesCatalogAPIURL, "application/vnd.github.raw+json", false},
		{defaultRemoteCatalogURL, "", false},
		{defaultRemoteCatalogAPIURL, "application/vnd.github.raw+json", false},
	}
	if tokens[0] != "" {
		sources = []catSource{
			{defaultReleasesCatalogAPIURL, "application/vnd.github.raw+json", false},
			{defaultReleasesCatalogURL, "", false},
			{defaultRemoteCatalogAPIURL, "application/vnd.github.raw+json", false},
			{defaultRemoteCatalogURL, "", false},
		}
	}
	if strings.TrimSpace(customURL) != "" {
		sources = append([]catSource{{strings.TrimSpace(customURL), "", true}}, sources...)
	}
	fetchedCat := false
	for _, tok := range tokens {
		if fetchedCat {
			break
		}
		for _, s := range sources {
			var rem Catalog
			if _, err := getJSONNoCache(client, s.url, tok, s.accept, &rem); err == nil && len(rem) > 0 {
				for eng, entries := range rem {
					for _, e := range entries {
						if !s.custom && isKnownDeadCatalogURL(eng, e.URL) {
							continue
						}
						merge(eng, e, s.custom)
					}
				}
				fetchedCat = true
				break
			}
		}
	}

	// 2. Live upstream discovery in parallel across all engines
	var wg sync.WaitGroup
	var mu sync.Mutex
	safeMerge := func(engine string, entry CatalogEntry) {
		mu.Lock()
		merge(engine, entry, true)
		mu.Unlock()
	}

	// Apache Lounge latest Win64 build
	wg.Add(1)
	go func() {
		defer wg.Done()
		if entries := fetchApacheLoungeEntries(client); len(entries) > 0 {
			mu.Lock()
			kept := make([]CatalogEntry, 0, len(base["apache"]))
			for _, e := range base["apache"] {
				if strings.EqualFold(strings.TrimSpace(e.Note), "Custom") {
					kept = append(kept, e)
				}
			}
			base["apache"] = kept
			for _, e := range entries {
				merge("apache", e, true)
			}
			mu.Unlock()
		}
	}()

	// PHP Windows latest releases.json
	wg.Add(1)
	go func() {
		defer wg.Done()
		for _, e := range fetchPHPEntries(client) {
			safeMerge("php", e)
		}
	}()

	// MariaDB latest per active stable series
	wg.Add(1)
	go func() {
		defer wg.Done()
		var majorResp struct {
			MajorReleases []struct {
				ReleaseID     string `json:"release_id"`
				ReleaseStatus string `json:"release_status"`
				SupportType   string `json:"release_support_type"`
			} `json:"major_releases"`
		}
		seriesList := []struct{ id, note string }{
			{"13.1", "Latest Stable"},
			{"12.3", "LTS"},
			{"11.8", "LTS"},
			{"11.4", "LTS"},
		}
		if getJSON(client, "https://downloads.mariadb.org/rest-api/mariadb/", &majorResp) == nil && len(majorResp.MajorReleases) > 0 {
			discovered := make([]struct{ id, note string }, 0, len(majorResp.MajorReleases))
			for _, mr := range majorResp.MajorReleases {
				if strings.EqualFold(mr.ReleaseStatus, "RC") || strings.EqualFold(mr.ReleaseStatus, "Alpha") || strings.EqualFold(mr.ReleaseStatus, "Beta") {
					continue
				}
				if mr.ReleaseID == "" || strings.HasPrefix(mr.ReleaseID, "10.11") {
					continue
				}
				note := "Stable"
				if len(discovered) == 0 {
					note = "Latest Stable"
				} else if strings.Contains(strings.ToLower(mr.SupportType), "long term") {
					note = "LTS"
				}
				discovered = append(discovered, struct{ id, note string }{mr.ReleaseID, note})
			}
			if len(discovered) > 0 {
				seriesList = discovered
			}
		}
		var mwg sync.WaitGroup
		for _, series := range seriesList {
			mwg.Add(1)
			go func(ser, note string) {
				defer mwg.Done()
				var resp struct {
					Releases map[string]struct {
						Files []struct {
							FileName        string `json:"file_name"`
							FileDownloadURL string `json:"file_download_url"`
						} `json:"files"`
					} `json:"releases"`
				}
				if getJSON(client, "https://downloads.mariadb.org/rest-api/mariadb/"+ser+"/latest/", &resp) == nil {
					for ver, rel := range resp.Releases {
						dlURL := fmt.Sprintf("https://downloads.mariadb.org/rest-api/mariadb/%s/mariadb-%s-winx64.zip", ver, ver)
						for _, f := range rel.Files {
							if strings.EqualFold(f.FileName, fmt.Sprintf("mariadb-%s-winx64.zip", ver)) && f.FileDownloadURL != "" {
								dlURL = normalizeCatalogURL("mariadb", ver, f.FileDownloadURL)
								break
							}
						}
						safeMerge("mariadb", CatalogEntry{Version: ver, URL: dlURL, Note: note})
					}
				}
			}(series.id, series.note)
		}
		mwg.Wait()
	}()

	// MySQL active cycles via endoflife.date + verified CDN/archive URLs
	wg.Add(1)
	go func() {
		defer wg.Done()
		for _, e := range fetchMySQLEntries(client) {
			safeMerge("mysql", e)
		}
	}()

	// PostgreSQL supported versions
	wg.Add(1)
	go func() {
		defer wg.Done()
		var pgs []struct {
			Major       int  `json:"major"`
			LatestMinor int  `json:"latestMinor"`
			Supported   bool `json:"supported"`
		}
		if getJSON(client, "https://www.postgresql.org/versions.json", &pgs) == nil {
			for _, pg := range pgs {
				if !pg.Supported || pg.Major < 14 {
					continue
				}
				ver := fmt.Sprintf("%d.%d", pg.Major, pg.LatestMinor)
				u := fmt.Sprintf("https://get.enterprisedb.com/postgresql/postgresql-%s-1-windows-x64-binaries.zip", ver)
				note := "Stable"
				if pg.Major >= 19 {
					note = "Latest"
				}
				safeMerge("postgresql", CatalogEntry{Version: ver, URL: u, Note: note})
			}
		}
	}()

	// MS SQL go-sqlcmd latest GitHub release
	wg.Add(1)
	go func() {
		defer wg.Done()
		var rels []struct {
			Assets []struct {
				Name               string `json:"name"`
				BrowserDownloadURL string `json:"browser_download_url"`
			} `json:"assets"`
		}
		if getJSON(client, "https://api.github.com/repos/microsoft/go-sqlcmd/releases?per_page=2", &rels) == nil {
			for _, r := range rels {
				for _, a := range r.Assets {
					if a.Name == "sqlcmd-windows-amd64.zip" && a.BrowserDownloadURL != "" {
						mu.Lock()
						for i, existing := range base["mssql"] {
							if !strings.EqualFold(strings.TrimSpace(existing.Note), "Custom") {
								base["mssql"][i].URL = a.BrowserDownloadURL
							}
						}
						mu.Unlock()
						return
					}
				}
			}
		}
	}()

	// Redis Windows latest GitHub releases
	wg.Add(1)
	go func() {
		defer wg.Done()
		var rels []struct {
			TagName string `json:"tag_name"`
			Assets  []struct {
				Name               string `json:"name"`
				BrowserDownloadURL string `json:"browser_download_url"`
			} `json:"assets"`
		}
		if getJSON(client, "https://api.github.com/repos/redis-windows/redis-windows/releases?per_page=6", &rels) == nil {
			for i, r := range rels {
				ver := strings.TrimPrefix(r.TagName, "v")
				for _, a := range r.Assets {
					if strings.HasSuffix(a.Name, "-Windows-x64-msys2.zip") {
						note := "Stable"
						if i == 0 {
							note = "Latest"
						}
						safeMerge("redis", CatalogEntry{Version: ver, URL: a.BrowserDownloadURL, Note: note})
						break
					}
				}
			}
		}
	}()

	// Mailpit latest GitHub releases
	wg.Add(1)
	go func() {
		defer wg.Done()
		var rels []struct {
			TagName string `json:"tag_name"`
			Assets  []struct {
				Name               string `json:"name"`
				BrowserDownloadURL string `json:"browser_download_url"`
			} `json:"assets"`
		}
		if getJSON(client, "https://api.github.com/repos/axllent/mailpit/releases?per_page=4", &rels) == nil {
			for i, r := range rels {
				ver := strings.TrimPrefix(r.TagName, "v")
				for _, a := range r.Assets {
					if a.Name == "mailpit-windows-amd64.zip" {
						note := "Stable"
						if i == 0 {
							note = "Latest Official"
						}
						safeMerge("mailpit", CatalogEntry{Version: ver, URL: a.BrowserDownloadURL, Note: note})
						break
					}
				}
			}
		}
	}()

	wg.Wait()

	// Sort each engine's versions newest-first
	for eng, list := range base {
		sortCatalogEntries(list)
		base[eng] = list
	}

	_ = SaveCatalog(p, base)
	return base, nil
}

var reApacheLoungeWin64 = regexp.MustCompile(`href="(/download/(VS\d+)/binaries/(httpd-(\d+\.\d+\.\d+)-[^"]*?-Win64-[^"]*?\.zip))"`)

func fetchApacheLoungeEntries(client *http.Client) []CatalogEntry {
	req, err := http.NewRequest("GET", "https://www.apachelounge.com/download/", nil)
	if err != nil {
		return nil
	}
	req.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) DariusDB-Lite/0.1.6")
	resp, err := client.Do(req)
	if err != nil || resp.StatusCode != 200 {
		if resp != nil {
			resp.Body.Close()
		}
		return nil
	}
	defer resp.Body.Close()
	b, err := io.ReadAll(io.LimitReader(resp.Body, 512<<10))
	if err != nil {
		return nil
	}
	matches := reApacheLoungeWin64.FindAllStringSubmatch(string(b), -1)
	seen := map[string]bool{}
	var out []CatalogEntry
	for _, m := range matches {
		path, vs, ver := m[1], m[2], m[4]
		u := "https://www.apachelounge.com" + path
		if seen[u] {
			continue
		}
		seen[u] = true
		out = append(out, CatalogEntry{
			Version: ver,
			URL:     u,
			Note:    fmt.Sprintf("Apache 2.4 Win64 %s", vs),
		})
	}
	return out
}

func fetchPHPEntries(client *http.Client) []CatalogEntry {
	var raw map[string]json.RawMessage
	if getJSON(client, "https://windows.php.net/downloads/releases/releases.json", &raw) != nil {
		return nil
	}
	var out []CatalogEntry
	for branch, payload := range raw {
		if compareVersions(branch, "8.1") < 0 {
			continue
		}
		var fields map[string]json.RawMessage
		if json.Unmarshal(payload, &fields) != nil {
			continue
		}
		var ver string
		if b, ok := fields["version"]; ok {
			_ = json.Unmarshal(b, &ver)
		}
		if ver == "" {
			continue
		}
		var zipPath, vsLabel string
		for k, v := range fields {
			if strings.HasPrefix(k, "ts-vs") && strings.HasSuffix(k, "-x64") {
				var build struct {
					Zip struct {
						Path string `json:"path"`
					} `json:"zip"`
				}
				if json.Unmarshal(v, &build) == nil && build.Zip.Path != "" {
					zipPath = build.Zip.Path
					parts := strings.Split(k, "-")
					if len(parts) >= 2 {
						vsLabel = strings.ToUpper(parts[1])
					}
					break
				}
			}
		}
		if zipPath == "" {
			continue
		}
		note := fmt.Sprintf("PHP %s (%s x64)", branch, vsLabel)
		out = append(out, CatalogEntry{
			Version: ver,
			URL:     "https://windows.php.net/downloads/releases/" + zipPath,
			Note:    note,
		})
	}
	return out
}

func fetchMySQLEntries(client *http.Client) []CatalogEntry {
	var cycles []struct {
		Cycle  string      `json:"cycle"`
		Latest string      `json:"latest"`
		LTS    interface{} `json:"lts"`
	}
	if getJSON(client, "https://endoflife.date/api/mysql.json", &cycles) != nil {
		return nil
	}
	var out []CatalogEntry
	var mu sync.Mutex
	var wg sync.WaitGroup
	first := true
	for _, c := range cycles {
		ver := strings.TrimSpace(c.Latest)
		cycle := strings.TrimSpace(c.Cycle)
		if ver == "" || cycle == "" || compareVersions(cycle, "8.0") < 0 {
			continue
		}
		// Keep Innovation/LTS series (26.x, 9.7+, 8.4, 8.0); skip expired 8.1-8.3 / 9.0-9.6 series
		if compareVersions(cycle, "8.0") > 0 && compareVersions(cycle, "8.4") < 0 {
			continue
		}
		if compareVersions(cycle, "9.0") >= 0 && compareVersions(cycle, "9.7") < 0 {
			continue
		}
		isLTS := false
		switch v := c.LTS.(type) {
		case bool:
			isLTS = v
		case string:
			isLTS = v != ""
		}
		note := "Stable"
		if first {
			note = "Latest"
			first = false
		} else if isLTS {
			note = "LTS"
		}
		wg.Add(1)
		go func(cycle, ver, note string) {
			defer wg.Done()
			primary := fmt.Sprintf("https://cdn.mysql.com/Downloads/MySQL-%s/mysql-%s-winx64.zip", cycle, ver)
			archive := fmt.Sprintf("https://cdn.mysql.com/archives/mysql-%s/mysql-%s-winx64.zip", cycle, ver)
			chosen := ""
			if headOK(client, primary) {
				chosen = primary
			} else if headOK(client, archive) {
				chosen = archive
			}
			if chosen != "" {
				mu.Lock()
				out = append(out, CatalogEntry{Version: ver, URL: chosen, Note: note})
				mu.Unlock()
			}
		}(cycle, ver, note)
	}
	wg.Wait()
	return out
}

func headOK(client *http.Client, rawURL string) bool {
	req, err := http.NewRequest("HEAD", rawURL, nil)
	if err != nil {
		return false
	}
	req.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) DariusDB-Lite/0.1.6")
	resp, err := client.Do(req)
	if err != nil {
		return false
	}
	resp.Body.Close()
	return resp.StatusCode == 200
}

// AddCustomCatalogEntry saves a user-provided version and download URL into catalog.json.
func AddCustomCatalogEntry(p *Paths, engine, version, url, note string) (Catalog, error) {
	if _, err := GetEngine(engine); err != nil {
		return nil, err
	}
	version = strings.TrimSpace(version)
	url = strings.TrimSpace(url)
	if version == "" || url == "" {
		return nil, errors.New("version and download URL are required")
	}
	c := LoadCatalog(p)
	list := c[engine]
	updated := false
	for i, e := range list {
		if e.Version == version {
			list[i] = CatalogEntry{Version: version, URL: url, Note: note}
			updated = true
			break
		}
	}
	if !updated {
		list = append([]CatalogEntry{{Version: version, URL: url, Note: note}}, list...)
	}
	sort.Slice(list, func(i, j int) bool {
		return compareVersions(list[i].Version, list[j].Version) > 0
	})
	c[engine] = list
	if err := SaveCatalog(p, c); err != nil {
		return nil, err
	}
	return c, nil
}

func SaveCatalog(p *Paths, c Catalog) error {
	b, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(p.Root, "catalog.json"), b, 0o644)
}

func fetchCatalogJSON(client *http.Client, url string) (Catalog, error) {
	var c Catalog
	if err := getJSON(client, url, &c); err != nil {
		return nil, err
	}
	return c, nil
}

func getJSON(client *http.Client, url string, dest interface{}) error {
	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return err
	}
	req.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) DariusDB-Lite/0.1.6")
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	b, err := io.ReadAll(io.LimitReader(resp.Body, 2<<20))
	if err != nil {
		return err
	}
	return json.Unmarshal(b, dest)
}

// ---------------------------------------------------------------- downloads

type Progress struct {
	Engine   string `json:"engine"`
	Version  string `json:"version"`
	Phase    string `json:"phase"` // downloading | extracting | done | error | cancelled
	Received int64  `json:"received"`
	Total    int64  `json:"total"`
	Message  string `json:"message"`
}

type Downloader struct {
	p      *Paths
	emit   func(Progress)
	onDone func(engine, name string)
	mu     sync.Mutex
	active map[string]context.CancelFunc
}

func NewDownloader(p *Paths, emit func(Progress), onDone func(engine, name string)) *Downloader {
	if emit == nil {
		emit = func(Progress) {}
	}
	return &Downloader{p: p, emit: emit, onDone: onDone, active: map[string]context.CancelFunc{}}
}

func key(engine, version string) string { return engine + "@" + version }

func (d *Downloader) Active() []string {
	d.mu.Lock()
	defer d.mu.Unlock()
	out := []string{}
	for k := range d.active {
		out = append(out, k)
	}
	return out
}

func (d *Downloader) Cancel(engine, version string) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if c := d.active[key(engine, version)]; c != nil {
		c()
	}
}

func (d *Downloader) begin(engine, version string) (context.Context, error) {
	if _, err := GetEngine(engine); err != nil {
		return nil, err
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	name := engine + "-" + version
	if dirExists(filepath.Join(d.p.EngineBin(engine), name)) {
		return nil, fmt.Errorf("%s is already installed", name)
	}
	for _, inst := range ListInstallations(d.p, engine) {
		if strings.EqualFold(inst.Name, name) || strings.EqualFold(inst.Version, version) {
			return nil, fmt.Errorf("%s %s is already installed", engine, version)
		}
	}
	k := key(engine, version)
	if d.active[k] != nil {
		return nil, fmt.Errorf("%s %s is already being installed", engine, version)
	}
	ctx, cancel := context.WithCancel(context.Background())
	d.active[k] = cancel
	return ctx, nil
}

func (d *Downloader) end(engine, version string) {
	d.mu.Lock()
	defer d.mu.Unlock()
	delete(d.active, key(engine, version))
}

// Install downloads url and installs it as bin/<engine>/<engine>-<version>.
// It runs asynchronously and reports through emit.
func (d *Downloader) Install(engine, version, url string) error {
	ctx, err := d.begin(engine, version)
	if err != nil {
		return err
	}
	go func() {
		defer d.end(engine, version)
		_ = d.runInstall(ctx, engine, version, url)
	}()
	return nil
}

// InstallSync downloads url and installs it as bin/<engine>/<engine>-<version> synchronously.
func (d *Downloader) InstallSync(ctx context.Context, engine, version, url string) error {
	baseCtx, err := d.begin(engine, version)
	if err != nil {
		return err
	}
	defer d.end(engine, version)
	runCtx := baseCtx
	if ctx != nil {
		var cancel context.CancelFunc
		runCtx, cancel = context.WithCancel(ctx)
		defer cancel()
		go func() {
			select {
			case <-baseCtx.Done():
				cancel()
			case <-runCtx.Done():
			}
		}()
	}
	return d.runInstall(runCtx, engine, version, url)
}

func (d *Downloader) runInstall(ctx context.Context, engine, version, url string) error {
	if err := os.MkdirAll(d.p.Tmp, 0o755); err != nil {
		d.fail(ctx, engine, version, err)
		return err
	}
	zipPath := filepath.Join(d.p.Tmp, fmt.Sprintf("%s-%s.zip", engine, version))
	dlErr := d.download(ctx, engine, version, url, zipPath)
	if dlErr != nil {
		if engine == "apache" && ctx.Err() == nil {
			d.emit(Progress{Engine: engine, Version: version, Phase: "extracting", Received: 1, Total: 1})
			finalDir := filepath.Join(d.p.EngineBin("apache"), "apache-"+version)
			if err := provisionPortableApache(finalDir, version); err == nil {
				d.finish(engine, version)
				return nil
			}
		}
		d.fail(ctx, engine, version, dlErr)
		return dlErr
	}
	err := d.extract(ctx, engine, version, zipPath)
	os.Remove(zipPath)
	if err != nil {
		if engine == "apache" && ctx.Err() == nil {
			finalDir := filepath.Join(d.p.EngineBin("apache"), "apache-"+version)
			if perr := provisionPortableApache(finalDir, version); perr == nil {
				d.finish(engine, version)
				return nil
			}
		}
		d.fail(ctx, engine, version, err)
		return err
	}
	d.finish(engine, version)
	return nil
}

// InstallZip installs a local zip file (e.g. downloaded manually).
func (d *Downloader) InstallZip(engine, zipPath string) (string, error) {
	version := parseVersion(strings.TrimSuffix(filepath.Base(zipPath), filepath.Ext(zipPath)))
	if version == "" {
		version = time.Now().Format("20060102-150405")
	}
	ctx, err := d.begin(engine, version)
	if err != nil {
		return "", err
	}
	go func() {
		defer d.end(engine, version)
		if err := d.extract(ctx, engine, version, zipPath); err != nil {
			d.fail(ctx, engine, version, err)
			return
		}
		d.finish(engine, version)
	}()
	return version, nil
}

// InstallZipSync installs a local zip archive synchronously into bin/<engine>/<engine>-<version>.
func (d *Downloader) InstallZipSync(ctx context.Context, engine, version, zipPath string) (string, error) {
	if strings.TrimSpace(version) == "" {
		version = parseVersion(strings.TrimSuffix(filepath.Base(zipPath), filepath.Ext(zipPath)))
	}
	if strings.TrimSpace(version) == "" {
		version = time.Now().Format("20060102-150405")
	}
	baseCtx, err := d.begin(engine, version)
	if err != nil {
		return "", err
	}
	defer d.end(engine, version)
	runCtx := baseCtx
	if ctx != nil {
		var cancel context.CancelFunc
		runCtx, cancel = context.WithCancel(ctx)
		defer cancel()
		go func() {
			select {
			case <-baseCtx.Done():
				cancel()
			case <-runCtx.Done():
			}
		}()
	}
	if err := d.extract(runCtx, engine, version, zipPath); err != nil {
		d.fail(runCtx, engine, version, err)
		return "", err
	}
	d.finish(engine, version)
	return version, nil
}

// FindOfflineEngineArchive searches searchDirs (and their engines/ and offline/engines/ subfolders)
// for a locally bundled zip archive for engine (preferring preferredVersion when matched).
func FindOfflineEngineArchive(engine, preferredVersion string, searchDirs []string) (zipPath, version string, ok bool) {
	eng := strings.ToLower(strings.TrimSpace(engine))
	if eng == "" {
		return "", "", false
	}
	pref := strings.TrimSpace(preferredVersion)

	type candidate struct {
		path    string
		version string
	}
	var matches []candidate
	seenDir := map[string]bool{}
	scanDir := func(dir string) {
		dir = strings.TrimSpace(dir)
		if dir == "" {
			return
		}
		clean := strings.ToLower(filepath.Clean(dir))
		if seenDir[clean] {
			return
		}
		seenDir[clean] = true
		entries, err := os.ReadDir(dir)
		if err != nil {
			return
		}
		for _, e := range entries {
			if e.IsDir() {
				continue
			}
			name := e.Name()
			low := strings.ToLower(name)
			if !strings.HasSuffix(low, ".zip") {
				continue
			}
			if !matchesOfflineEngineFileName(eng, low) {
				continue
			}
			base := strings.TrimSuffix(name, filepath.Ext(name))
			ver := parseVersion(base)
			if ver == "" {
				ver = pref
			}
			matches = append(matches, candidate{
				path:    filepath.Join(dir, name),
				version: ver,
			})
		}
	}

	for _, root := range searchDirs {
		if strings.TrimSpace(root) == "" {
			continue
		}
		scanDir(filepath.Join(root, "offline", "engines"))
		scanDir(filepath.Join(root, "engines"))
		scanDir(filepath.Join(root, "offline"))
		scanDir(root)
	}
	if len(matches) == 0 {
		return "", "", false
	}
	if pref != "" {
		for _, m := range matches {
			if strings.EqualFold(m.version, pref) {
				return m.path, m.version, true
			}
		}
	}
	sort.Slice(matches, func(i, j int) bool {
		return compareVersions(matches[i].version, matches[j].version) > 0
	})
	return matches[0].path, matches[0].version, true
}

func matchesOfflineEngineFileName(engine, lowerFileName string) bool {
	switch engine {
	case "apache":
		return strings.HasPrefix(lowerFileName, "apache-") || strings.HasPrefix(lowerFileName, "httpd-")
	case "mssql":
		return strings.HasPrefix(lowerFileName, "mssql-") || strings.HasPrefix(lowerFileName, "sqlcmd-")
	case "postgresql":
		return strings.HasPrefix(lowerFileName, "postgresql-") || strings.HasPrefix(lowerFileName, "postgres-")
	default:
		return strings.HasPrefix(lowerFileName, engine+"-") || strings.HasPrefix(lowerFileName, engine+"_")
	}
}

// FindOfflineEngineDir searches searchDirs for a pre-extracted offline engine directory
// under offline/bin/<engine>/ or bin/<engine>/.
func FindOfflineEngineDir(engine, preferredVersion string, searchDirs []string) (dirPath, version string, ok bool) {
	eng, err := GetEngine(engine)
	if err != nil {
		return "", "", false
	}
	pref := strings.TrimSpace(preferredVersion)
	type candidate struct {
		path    string
		version string
	}
	var matches []candidate
	seenDir := map[string]bool{}
	scanBase := func(base string) {
		if strings.TrimSpace(base) == "" {
			return
		}
		clean := strings.ToLower(filepath.Clean(base))
		if seenDir[clean] {
			return
		}
		seenDir[clean] = true
		entries, err := os.ReadDir(base)
		if err != nil {
			return
		}
		for _, e := range entries {
			if !e.IsDir() {
				continue
			}
			sub := filepath.Join(base, e.Name())
			if !eng.Detect(sub) {
				continue
			}
			matches = append(matches, candidate{
				path:    sub,
				version: parseVersion(e.Name()),
			})
		}
	}

	for _, root := range searchDirs {
		if strings.TrimSpace(root) == "" {
			continue
		}
		scanBase(filepath.Join(root, "offline", "bin", engine))
		scanBase(filepath.Join(root, "bin", engine))
	}
	if len(matches) == 0 {
		return "", "", false
	}
	if pref != "" {
		for _, m := range matches {
			if strings.EqualFold(m.version, pref) {
				return m.path, m.version, true
			}
		}
	}
	sort.Slice(matches, func(i, j int) bool {
		return compareVersions(matches[i].version, matches[j].version) > 0
	})
	return matches[0].path, matches[0].version, true
}

// InstallOfflineEngineDir copies a pre-extracted engine directory into p.EngineBin(engine)
// as <engine>-<version>, validates it with eng.Detect, and runs post-install hooks (like PHP ini sync).
func InstallOfflineEngineDir(p *Paths, engine, version, srcDir string, onDone func(engine, name string)) (string, error) {
	if p == nil {
		return "", errors.New("paths not initialized")
	}
	eng, err := GetEngine(engine)
	if err != nil {
		return "", err
	}
	if !eng.Detect(srcDir) {
		return "", fmt.Errorf("folder %s does not look like a %s build", srcDir, eng.Name())
	}
	if strings.TrimSpace(version) == "" {
		version = parseVersion(filepath.Base(srcDir))
	}
	if strings.TrimSpace(version) == "" {
		version = "offline"
	}
	name := engine + "-" + version
	final := filepath.Join(p.EngineBin(engine), name)
	if dirExists(final) {
		return name, fmt.Errorf("%s is already installed", name)
	}
	tmp := filepath.Join(p.EngineBin(engine), "."+name+".partial")
	_ = os.RemoveAll(tmp)
	if err := copyDirRecursive(srcDir, tmp); err != nil {
		_ = os.RemoveAll(tmp)
		return "", err
	}
	if err := os.Rename(tmp, final); err != nil {
		_ = os.RemoveAll(tmp)
		return "", err
	}
	if engine == "php" {
		_, _ = SyncManagedPHPIni(p)
	}
	if onDone != nil {
		onDone(engine, name)
	}
	return name, nil
}

func copyDirRecursive(src, dst string) error {
	return filepath.Walk(src, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(src, path)
		if err != nil {
			return err
		}
		target := filepath.Join(dst, rel)
		if info.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			return err
		}
		in, err := os.Open(path)
		if err != nil {
			return err
		}
		defer in.Close()
		out, err := os.Create(target)
		if err != nil {
			return err
		}
		if _, err := io.Copy(out, in); err != nil {
			out.Close()
			return err
		}
		return out.Close()
	})
}

func (d *Downloader) fail(ctx context.Context, engine, version string, err error) {
	phase := "error"
	if ctx.Err() != nil {
		phase, err = "cancelled", errors.New("cancelled")
	}
	d.emit(Progress{Engine: engine, Version: version, Phase: phase, Message: err.Error()})
}

func (d *Downloader) finish(engine, version string) {
	name := engine + "-" + version
	d.emit(Progress{Engine: engine, Version: version, Phase: "done", Message: name + " installed"})
	if d.onDone != nil {
		d.onDone(engine, name)
	}
}

var downloadHTTPClient = &http.Client{
	Transport: &http.Transport{
		Proxy: http.ProxyFromEnvironment,
		DialContext: (&net.Dialer{
			Timeout:   15 * time.Second,
			KeepAlive: 30 * time.Second,
		}).DialContext,
		TLSHandshakeTimeout:   15 * time.Second,
		ResponseHeaderTimeout: 20 * time.Second,
		IdleConnTimeout:       60 * time.Second,
	},
}

func (d *Downloader) download(ctx context.Context, engine, version, rawURL, dest string) error {
	candidates := buildDownloadCandidates(ctx, engine, version, rawURL)
	var lastErr error
	for _, u := range candidates {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		err := d.downloadOnce(ctx, engine, version, u, dest)
		if err == nil {
			return nil
		}
		lastErr = err
		if ctx.Err() != nil {
			return ctx.Err()
		}
	}
	return lastErr
}

func buildDownloadCandidates(ctx context.Context, engine, version, rawURL string) []string {
	seen := map[string]bool{}
	var out []string
	add := func(u string) {
		u = strings.TrimSpace(u)
		if u == "" || seen[u] {
			return
		}
		seen[u] = true
		out = append(out, u)
	}

	norm := normalizeCatalogURL(engine, version, rawURL)
	add(rawURL)
	add(norm)

	ver := strings.TrimSpace(version)
	switch engine {
	case "mysql":
		majMin := versionPrefix(ver, 2)
		if ver != "" && majMin != "" {
			add(fmt.Sprintf("https://cdn.mysql.com/archives/mysql-%s/mysql-%s-winx64.zip", majMin, ver))
			add(fmt.Sprintf("https://downloads.mysql.com/archives/get/p/23/file/mysql-%s-winx64.zip", ver))
			add(fmt.Sprintf("https://cdn.mysql.com/Downloads/MySQL-%s/mysql-%s-winx64.zip", majMin, ver))
		}
	case "php":
		if strings.Contains(norm, "windows.php.net/downloads/releases/") {
			if strings.Contains(norm, "/downloads/releases/archives/") {
				add(strings.Replace(norm, "/downloads/releases/archives/", "/downloads/releases/", 1))
			} else {
				add(strings.Replace(norm, "/downloads/releases/", "/downloads/releases/archives/", 1))
			}
		}
	case "mariadb":
		if ver != "" {
			add(fmt.Sprintf("https://downloads.mariadb.org/rest-api/mariadb/%s/mariadb-%s-winx64.zip", ver, ver))
			add(fmt.Sprintf("https://dlm.mariadb.com/rest-api/mariadb/%s/mariadb-%s-winx64.zip", ver, ver))
			add(fmt.Sprintf("https://archive.mariadb.org/mariadb-%s/winx64-packages/mariadb-%s-winx64.zip", ver, ver))
		}
	case "apache":
		if ctx.Err() == nil {
			probeClient := &http.Client{Timeout: 8 * time.Second}
			for _, e := range fetchApacheLoungeEntries(probeClient) {
				add(e.URL)
			}
		}
	}
	return out
}

func (d *Downloader) downloadOnce(ctx context.Context, engine, version, url, dest string) error {
	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return err
	}
	req.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) DariusDB-Lite/0.1.6")
	resp, err := downloadHTTPClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return fmt.Errorf("download failed: %s", resp.Status)
	}
	if ct := strings.ToLower(resp.Header.Get("Content-Type")); strings.Contains(ct, "text/html") {
		return fmt.Errorf("remote host returned an HTML page instead of a .zip archive")
	}
	part := dest + ".part"
	f, err := os.Create(part)
	if err != nil {
		return err
	}
	total := resp.ContentLength
	var received int64
	last := time.Time{}
	buf := make([]byte, 256<<10)
	for {
		n, rerr := resp.Body.Read(buf)
		if n > 0 {
			if _, werr := f.Write(buf[:n]); werr != nil {
				f.Close()
				return werr
			}
			received += int64(n)
			if time.Since(last) > 200*time.Millisecond {
				last = time.Now()
				d.emit(Progress{Engine: engine, Version: version, Phase: "downloading", Received: received, Total: total})
			}
		}
		if rerr == io.EOF {
			break
		}
		if rerr != nil {
			f.Close()
			os.Remove(part)
			return rerr
		}
	}
	if err := f.Close(); err != nil {
		return err
	}
	if total > 0 && received != total {
		os.Remove(part)
		return fmt.Errorf("incomplete download (%d of %d bytes)", received, total)
	}
	return os.Rename(part, dest)
}

func (d *Downloader) extract(ctx context.Context, engine, version, zipPath string) error {
	zr, err := zip.OpenReader(zipPath)
	if err != nil {
		return fmt.Errorf("cannot open zip: %v", err)
	}
	defer zr.Close()

	// strip a single shared top-level folder (e.g. "mariadb-11.4.14-winx64/", "pgsql/")
	prefix := ""
	for i, f := range zr.File {
		name := strings.ReplaceAll(f.Name, `\`, "/")
		top := strings.SplitN(name, "/", 2)[0] + "/"
		if !strings.Contains(name, "/") {
			prefix = ""
			break
		}
		if i == 0 {
			prefix = top
		} else if top != prefix {
			prefix = ""
			break
		}
	}

	var total int64
	for _, f := range zr.File {
		total += int64(f.UncompressedSize64)
	}

	name := engine + "-" + version
	final := filepath.Join(d.p.EngineBin(engine), name)
	tmp := filepath.Join(d.p.EngineBin(engine), "."+name+".partial")
	os.RemoveAll(tmp)
	if err := os.MkdirAll(tmp, 0o755); err != nil {
		return err
	}
	cleanTmp := filepath.Clean(tmp) + string(os.PathSeparator)

	var done int64
	last := time.Time{}
	for _, f := range zr.File {
		if ctx.Err() != nil {
			os.RemoveAll(tmp)
			return ctx.Err()
		}
		rel := strings.TrimPrefix(strings.ReplaceAll(f.Name, `\`, "/"), prefix)
		if rel == "" {
			continue
		}
		target := filepath.Join(tmp, filepath.FromSlash(rel))
		if !strings.HasPrefix(filepath.Clean(target)+string(os.PathSeparator), cleanTmp) {
			os.RemoveAll(tmp)
			return fmt.Errorf("unsafe path in zip: %s", f.Name)
		}
		if f.FileInfo().IsDir() {
			if err := os.MkdirAll(target, 0o755); err != nil {
				return err
			}
			continue
		}
		if err := extractFile(f, target); err != nil {
			os.RemoveAll(tmp)
			return err
		}
		done += int64(f.UncompressedSize64)
		if time.Since(last) > 200*time.Millisecond {
			last = time.Now()
			d.emit(Progress{Engine: engine, Version: version, Phase: "extracting", Received: done, Total: total})
		}
	}

	eng, _ := GetEngine(engine)
	src := tmp
	if !eng.Detect(src) {
		// Check one level down in case the zip also had a stray README next to the engine folder.
		if entries, err := os.ReadDir(tmp); err == nil {
			for _, e := range entries {
				if e.IsDir() {
					sub := filepath.Join(tmp, e.Name())
					if eng.Detect(sub) {
						src = sub
						break
					}
				}
			}
		}
	}
	if !eng.Detect(src) {
		os.RemoveAll(tmp)
		return fmt.Errorf("this zip does not look like a %s build", eng.Name())
	}
	if err := os.Rename(src, final); err != nil {
		os.RemoveAll(tmp)
		return err
	}
	if src != tmp {
		os.RemoveAll(tmp)
	}
	if engine == "php" {
		_, _ = SyncManagedPHPIni(d.p)
	}
	return nil
}

func extractFile(f *zip.File, target string) error {
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		return err
	}
	rc, err := f.Open()
	if err != nil {
		return err
	}
	defer rc.Close()
	out, err := os.Create(target)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, rc); err != nil {
		out.Close()
		return err
	}
	return out.Close()
}
