package core

import (
	"archive/zip"
	"context"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

const defaultRemoteCatalogURL = "https://raw.githubusercontent.com/dcquijano/Darius-DB-Lite-Version/main/internal/core/catalog.json"

//go:embed catalog.json
var embeddedCatalog []byte

type CatalogEntry struct {
	Version string `json:"version"`
	URL     string `json:"url"`
	Note    string `json:"note,omitempty"`
}

type Catalog map[string][]CatalogEntry

// LoadCatalog returns catalog.json next to the exe if present (user editable /
// refreshed from the internet), otherwise the catalog embedded at build time.
func LoadCatalog(p *Paths) Catalog {
	var c Catalog
	if b, err := os.ReadFile(filepath.Join(p.Root, "catalog.json")); err == nil {
		if json.Unmarshal(b, &c) == nil && len(c) > 0 {
			return c
		}
	}
	_ = json.Unmarshal(embeddedCatalog, &c)
	return c
}

// RefreshCatalog discovers the newest engine versions from official upstream
// APIs (MariaDB, PostgreSQL, Redis, GitHub repo catalog, and optional custom URL),
// merges them into catalog.json, and returns the updated catalog.
func RefreshCatalog(p *Paths, customURL string) (Catalog, error) {
	base := LoadCatalog(p)
	client := &http.Client{Timeout: 12 * time.Second}

	merge := func(engine string, entry CatalogEntry) {
		if entry.Version == "" || entry.URL == "" {
			return
		}
		list := base[engine]
		for i, existing := range list {
			if existing.Version == entry.Version {
				if entry.Note != "" && existing.Note == "" {
					list[i].Note = entry.Note
				}
				return
			}
		}
		base[engine] = append(list, entry)
	}

	// 1. Fetch remote catalog JSON (custom URL if set, plus official GitHub repo catalog)
	urls := []string{defaultRemoteCatalogURL}
	if strings.TrimSpace(customURL) != "" {
		urls = append([]string{strings.TrimSpace(customURL)}, urls...)
	}
	for _, u := range urls {
		if rem, err := fetchCatalogJSON(client, u); err == nil {
			for eng, entries := range rem {
				for _, e := range entries {
					merge(eng, e)
				}
			}
		}
	}

	// 2. Live upstream discovery in parallel (MariaDB, PostgreSQL, Redis)
	var wg sync.WaitGroup
	var mu sync.Mutex
	safeMerge := func(engine string, entry CatalogEntry) {
		mu.Lock()
		merge(engine, entry)
		mu.Unlock()
	}

	// MariaDB latest per active series
	for _, series := range []struct{ id, note string }{
		{"13.0", "Latest Stable"},
		{"12.3", "LTS"},
		{"11.8", "LTS"},
		{"11.4", "LTS"},
		{"10.11", "LTS"},
	} {
		wg.Add(1)
		go func(ser, note string) {
			defer wg.Done()
			var resp struct {
				Releases map[string]struct{} `json:"releases"`
			}
			if getJSON(client, "https://downloads.mariadb.org/rest-api/mariadb/"+ser+"/latest/", &resp) == nil {
				for ver := range resp.Releases {
					u := fmt.Sprintf("https://archive.mariadb.org/mariadb-%s/winx64-packages/mariadb-%s-winx64.zip", ver, ver)
					safeMerge("mariadb", CatalogEntry{Version: ver, URL: u, Note: note})
				}
			}
		}(series.id, series.note)
	}

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
				if pg.Major >= 18 {
					note = "Latest"
				}
				safeMerge("postgresql", CatalogEntry{Version: ver, URL: u, Note: note})
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

	wg.Wait()

	// Sort each engine's versions newest-first
	for eng, list := range base {
		sort.Slice(list, func(i, j int) bool {
			return compareVersions(list[i].Version, list[j].Version) > 0
		})
		base[eng] = list
	}

	_ = SaveCatalog(p, base)
	return base, nil
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
	req.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) DariusDB-Lite/0.1.2")
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
	name := engine + "-" + version
	if dirExists(filepath.Join(d.p.EngineBin(engine), name)) {
		return nil, fmt.Errorf("%s is already installed", name)
	}
	d.mu.Lock()
	defer d.mu.Unlock()
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
		zipPath := filepath.Join(d.p.Tmp, fmt.Sprintf("%s-%s.zip", engine, version))
		if err := d.download(ctx, engine, version, url, zipPath); err != nil {
			d.fail(ctx, engine, version, err)
			return
		}
		err := d.extract(ctx, engine, version, zipPath)
		os.Remove(zipPath)
		if err != nil {
			d.fail(ctx, engine, version, err)
			return
		}
		d.finish(engine, version)
	}()
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

func (d *Downloader) download(ctx context.Context, engine, version, url, dest string) error {
	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return err
	}
	req.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) DariusDB-Lite/0.1.2")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return fmt.Errorf("download failed: %s", resp.Status)
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
