package core

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

const (
	defaultRepoOwner        = "dcquijano"
	defaultRepoName         = "Darius-DB-Lite-Version"
	defaultVersionJSONURL   = "https://raw.githubusercontent.com/dcquijano/Darius-DB-Lite-Version/main/version.json"
	defaultVersionAPIURL    = "https://api.github.com/repos/dcquijano/Darius-DB-Lite-Version/contents/version.json"
	defaultGitHubRelURL     = "https://api.github.com/repos/dcquijano/Darius-DB-Lite-Version/releases/latest"
	defaultGitHubCommitsURL = "https://api.github.com/repos/dcquijano/Darius-DB-Lite-Version/commits?per_page=8"
	defaultDriveFolderURL   = "https://drive.google.com/drive/folders/1FVLCHqOfxmP9PbSx70jy_BfH2Yn1E_UG?usp=sharing"
	defaultRawInstallerBase = "https://raw.githubusercontent.com/dcquijano/Darius-DB-Lite-Version/main/installer"
)

type GitHubCommitInfo struct {
	SHA     string `json:"sha"`
	Message string `json:"message"`
	Author  string `json:"author"`
	Date    string `json:"date"`
}

type SystemUpdateInfo struct {
	Name             string             `json:"name"`
	CurrentVersion   string             `json:"currentVersion"`
	LatestVersion    string             `json:"latestVersion"`
	CurrentBuild     string             `json:"currentBuild,omitempty"`
	LatestBuild      string             `json:"latestBuild,omitempty"`
	HasUpdate        bool               `json:"hasUpdate"`
	Channel          string             `json:"channel"`
	ReleasedAt       string             `json:"releasedAt"`
	Author           string             `json:"author"`
	InstallerURL     string             `json:"installerUrl"`
	DirectSetupURL   string             `json:"directSetupUrl"`
	DirectZipURL     string             `json:"directZipUrl"`
	FallbackSetupURL string             `json:"fallbackSetupUrl"`
	RepoURL          string             `json:"repoUrl"`
	Announcement     string             `json:"announcement"`
	Changelog        []string           `json:"changelog"`
	LatestCommitSHA  string             `json:"latestCommitSha"`
	LatestCommitMsg  string             `json:"latestCommitMsg"`
	LatestCommitDate string             `json:"latestCommitDate"`
	RecentCommits    []GitHubCommitInfo `json:"recentCommits"`
	CheckedAt        string             `json:"checkedAt"`
	FetchOK          bool               `json:"fetchOk"`
	FetchError       string             `json:"fetchError,omitempty"`
	FetchSource      string             `json:"fetchSource,omitempty"`
	RepoPrivateHint  bool               `json:"repoPrivateHint,omitempty"`
}

var (
	gcmOnce  sync.Once
	gcmToken string
)

// resolveGitCredentialToken queries Windows Git Credential Manager non-interactively
// (in memory only) so machines already signed into GitHub via Git can fetch private repos.
func resolveGitCredentialToken() string {
	gcmOnce.Do(func() {
		gitExe, err := exec.LookPath("git")
		if err != nil {
			return
		}
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		cmd := exec.CommandContext(ctx, gitExe, "credential", "fill")
		hideWindow(cmd)
		cmd.Env = append(os.Environ(), "GCM_INTERACTIVE=never", "GIT_TERMINAL_PROMPT=0")
		cmd.Stdin = strings.NewReader("protocol=https\nhost=github.com\n\n")
		out, err := cmd.Output()
		if err != nil {
			return
		}
		for _, line := range strings.Split(strings.ReplaceAll(string(out), "\r\n", "\n"), "\n") {
			if strings.HasPrefix(line, "password=") {
				tok := strings.TrimSpace(strings.TrimPrefix(line, "password="))
				if tok != "" {
					gcmToken = tok
				}
				return
			}
		}
	})
	return gcmToken
}

func resolveGitHubToken(tokens ...string) string {
	for _, t := range tokens {
		if s := strings.TrimSpace(t); s != "" {
			return s
		}
	}
	for _, envKey := range []string{"DARIUSDB_GITHUB_TOKEN", "GITHUB_TOKEN", "GH_TOKEN"} {
		if s := strings.TrimSpace(os.Getenv(envKey)); s != "" {
			return s
		}
	}
	return resolveGitCredentialToken()
}

func isGitHubHost(rawURL string) bool {
	low := strings.ToLower(rawURL)
	return strings.Contains(low, "github.com") || strings.Contains(low, "githubusercontent.com")
}

// buildCandidateTokens returns distinct non-empty GitHub tokens in priority order:
// explicit/env tokens first, followed by Windows Git Credential Manager fallback.
func buildCandidateTokens(tokens ...string) []string {
	var out []string
	seen := map[string]bool{}
	add := func(t string) {
		t = strings.TrimSpace(t)
		if t != "" && !seen[t] {
			seen[t] = true
			out = append(out, t)
		}
	}
	for _, t := range tokens {
		add(t)
	}
	for _, envKey := range []string{"DARIUSDB_GITHUB_TOKEN", "GITHUB_TOKEN", "GH_TOKEN"} {
		add(os.Getenv(envKey))
	}
	add(resolveGitCredentialToken())
	return out
}

func getJSONNoCache(client *http.Client, rawURL, token, accept string, dest interface{}) (int, error) {
	reqURL := rawURL
	// Bust CDN caches on raw.githubusercontent.com; keep GitHub REST API URLs clean
	if strings.Contains(strings.ToLower(rawURL), "raw.githubusercontent.com") {
		sep := "?"
		if strings.Contains(rawURL, "?") {
			sep = "&"
		}
		reqURL = fmt.Sprintf("%s%st=%d", rawURL, sep, time.Now().UnixMilli())
	}
	req, err := http.NewRequest("GET", reqURL, nil)
	if err != nil {
		return 0, err
	}
	req.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) DariusDB-Updater/0.1.3")
	req.Header.Set("Cache-Control", "no-cache, no-store, must-revalidate")
	req.Header.Set("Pragma", "no-cache")
	if accept != "" {
		req.Header.Set("Accept", accept)
	}
	if token != "" && isGitHubHost(rawURL) {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := client.Do(req)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return resp.StatusCode, fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	b, err := io.ReadAll(io.LimitReader(resp.Body, 2<<20))
	if err != nil {
		return resp.StatusCode, err
	}
	// Strip UTF-8 BOM if present
	b = bytes.TrimPrefix(b, []byte{0xEF, 0xBB, 0xBF})
	return resp.StatusCode, json.Unmarshal(b, dest)
}

// CheckSystemUpdate checks the official GitHub version.json, GitHub Commits API, and GitHub Releases API
// (bypassing CDN cache) for newly pushed updates, announcements, and release binaries.
// Pass an optional GitHub Personal Access Token for private repositories.
func CheckSystemUpdate(currentVersion string, tokens ...string) (*SystemUpdateInfo, error) {
	if currentVersion == "" {
		currentVersion = CLIAppVersion
	}
	candidateTokens := buildCandidateTokens(tokens...)
	// Try candidate tokens (plus anonymous "" last if none worked)
	tokenAttempts := append([]string(nil), candidateTokens...)
	if len(tokenAttempts) == 0 {
		tokenAttempts = []string{""}
	}

	var bestInfo *SystemUpdateInfo
	for i, tok := range tokenAttempts {
		info := checkSystemUpdateWithToken(currentVersion, tok)
		if info.FetchOK {
			return info, nil
		}
		bestInfo = info
		// Only try next candidate token on auth/visibility errors (401/403/404)
		if !info.RepoPrivateHint || i == len(tokenAttempts)-1 {
			break
		}
	}
	return bestInfo, nil
}

func checkSystemUpdateWithToken(currentVersion, tok string) *SystemUpdateInfo {

	info := &SystemUpdateInfo{
		Name:           "Darius DB (Lite Version " + currentVersion + ")",
		CurrentVersion: currentVersion,
		LatestVersion:  currentVersion,
		CurrentBuild:   CLIAppBuild,
		LatestBuild:    CLIAppBuild,
		HasUpdate:      false,
		Channel:        "Lite Version",
		ReleasedAt:     "2026-11-20",
		Author:         CLIAppAuthor,
		InstallerURL:   defaultDriveFolderURL,
		RepoURL:        CLIAppRepo,
		Announcement:   "Darius DB (Lite Version 0.1.3) — Full-Stack Web & Database Manager! Now includes Database Import (.sql/.csv/.bak), Full Row/Table CRUD (Create, Show, Edit, Delete, Show All), Single & Selected-Table Export (.sql/.csv), 1-Click GitHub Auto-Updater & Restart, standalone DariusDB-Updater.exe, and Toggle-Switch Apache 2.4 + PHP 8.x + MySQL/MariaDB (:3306).",
		Changelog: []string{
			"Added Database Import (.sql, .csv, .bak) and Full CRUD (Create Row, Create Table, Show Details, Edit Row, Delete, Show All)",
			"Added Single-Table, Selected-Tables, and Full-Database Export (.sql with Structure/Data toggles & .csv)",
			"Added 1-Click GitHub System Updater with BuildID tracking, automatic update download, and automatic app restart",
			"Added standalone DariusDB-Updater.exe and bundled Installer, Launcher, Uninstaller & Updater executables on GitHub",
			"Fixed MySQL Error 1130 on 127.0.0.1, removed username/password from mssql.conf, and auto-provisioned sqlcmd.exe",
			"Separated MariaDB and MySQL rows/tabs and redesigned Dashboard with Toggle-Switch rows & Full-Stack guide",
		},
		RecentCommits: []GitHubCommitInfo{},
		CheckedAt:     time.Now().Format("2006-01-02 15:04:05"),
	}

	client := &http.Client{Timeout: 12 * time.Second}

	var (
		gotVersion bool
		gotCommits bool
		gotRelease bool
		lastStatus int
		lastErr    error
		sources    []string
	)

	recordErr := func(code int, err error) {
		if code != 0 && (lastStatus == 0 || code == 404 || code == 401 || code == 403) {
			lastStatus = code
		}
		if err != nil && lastErr == nil {
			lastErr = err
		}
	}

	// 1. Check remote version.json in repository (prefer GitHub Contents API with raw accept when token is set)
	var rem struct {
		Name             string   `json:"name"`
		Version          string   `json:"version"`
		Build            string   `json:"build"`
		Channel          string   `json:"channel"`
		ReleasedAt       string   `json:"releasedAt"`
		Author           string   `json:"author"`
		InstallerURL     string   `json:"installerUrl"`
		DirectSetupURL   string   `json:"directSetupUrl"`
		DirectZipURL     string   `json:"directZipUrl"`
		FallbackSetupURL string   `json:"fallbackSetupUrl"`
		RepoURL          string   `json:"repoUrl"`
		Announcement     string   `json:"announcement"`
		Changelog        []string `json:"changelog"`
	}

	type verEndpoint struct {
		url    string
		accept string
	}
	verEndpoints := []verEndpoint{
		{defaultVersionJSONURL, ""},
		{defaultVersionAPIURL, "application/vnd.github.raw+json"},
	}
	if tok != "" {
		verEndpoints = []verEndpoint{
			{defaultVersionAPIURL, "application/vnd.github.raw+json"},
			{defaultVersionJSONURL, ""},
		}
	}
	for _, ep := range verEndpoints {
		code, err := getJSONNoCache(client, ep.url, tok, ep.accept, &rem)
		if err == nil && rem.Version != "" {
			gotVersion = true
			sources = append(sources, "version.json")
			info.LatestVersion = strings.TrimPrefix(strings.TrimSpace(rem.Version), "v")
			if strings.TrimSpace(rem.Build) != "" {
				info.LatestBuild = strings.TrimSpace(rem.Build)
			}
			if rem.Name != "" {
				info.Name = rem.Name
			}
			if rem.Channel != "" {
				info.Channel = rem.Channel
			}
			if rem.ReleasedAt != "" {
				info.ReleasedAt = rem.ReleasedAt
			}
			if rem.InstallerURL != "" {
				info.InstallerURL = rem.InstallerURL
			}
			if rem.DirectSetupURL != "" {
				info.DirectSetupURL = rem.DirectSetupURL
			}
			if rem.DirectZipURL != "" {
				info.DirectZipURL = rem.DirectZipURL
			}
			if rem.FallbackSetupURL != "" {
				info.FallbackSetupURL = rem.FallbackSetupURL
			}
			if rem.RepoURL != "" {
				info.RepoURL = rem.RepoURL
			}
			if rem.Announcement != "" {
				info.Announcement = rem.Announcement
			}
			if len(rem.Changelog) > 0 {
				info.Changelog = rem.Changelog
			}
			break
		}
		recordErr(code, err)
	}

	// 2. Fetch live recent commits pushed to GitHub
	var ghCommits []struct {
		SHA    string `json:"sha"`
		Commit struct {
			Message string `json:"message"`
			Author  struct {
				Name string `json:"name"`
				Date string `json:"date"`
			} `json:"author"`
		} `json:"commit"`
	}
	if code, err := getJSONNoCache(client, defaultGitHubCommitsURL, tok, "application/vnd.github+json", &ghCommits); err == nil && len(ghCommits) > 0 {
		gotCommits = true
		sources = append(sources, "commits")
		seenMsg := map[string]bool{}
		for _, c := range info.Changelog {
			seenMsg[strings.ToLower(strings.TrimSpace(c))] = true
		}
		var newCommitItems []string
		for i, c := range ghCommits {
			shortSHA := c.SHA
			if len(shortSHA) > 7 {
				shortSHA = shortSHA[:7]
			}
			firstLine := strings.TrimSpace(strings.Split(c.Commit.Message, "\n")[0])
			dateStr := c.Commit.Author.Date
			if t, err := time.Parse(time.RFC3339, dateStr); err == nil {
				dateStr = t.Local().Format("2006-01-02 15:04")
			}
			if i == 0 {
				info.LatestCommitSHA = shortSHA
				info.LatestCommitMsg = firstLine
				info.LatestCommitDate = dateStr
			}
			info.RecentCommits = append(info.RecentCommits, GitHubCommitInfo{
				SHA:     shortSHA,
				Message: firstLine,
				Author:  c.Commit.Author.Name,
				Date:    dateStr,
			})
			if firstLine != "" && !seenMsg[strings.ToLower(firstLine)] {
				newCommitItems = append(newCommitItems, fmt.Sprintf("[%s] %s (%s)", shortSHA, firstLine, dateStr))
				seenMsg[strings.ToLower(firstLine)] = true
			}
		}
		if len(newCommitItems) > 0 {
			info.Changelog = append(newCommitItems, info.Changelog...)
		}
	} else {
		recordErr(code, err)
	}

	// 3. Check GitHub Releases API in case a tagged release has a direct Setup .exe / Portable .zip asset
	var ghRel struct {
		TagName     string `json:"tag_name"`
		PublishedAt string `json:"published_at"`
		Body        string `json:"body"`
		Assets      []struct {
			Name               string `json:"name"`
			URL                string `json:"url"`
			BrowserDownloadURL string `json:"browser_download_url"`
		} `json:"assets"`
	}
	if code, err := getJSONNoCache(client, defaultGitHubRelURL, tok, "application/vnd.github+json", &ghRel); err == nil && ghRel.TagName != "" {
		gotRelease = true
		sources = append(sources, "releases")
		relVer := parseVersion(ghRel.TagName)
		if relVer != "" && compareVersions(relVer, info.LatestVersion) >= 0 {
			info.LatestVersion = relVer
			for _, a := range ghRel.Assets {
				low := strings.ToLower(a.Name)
				dlURL := a.BrowserDownloadURL
				if tok != "" && a.URL != "" {
					dlURL = a.URL
				}
				if strings.HasSuffix(low, ".exe") && strings.Contains(low, "setup") {
					info.DirectSetupURL = dlURL
				} else if strings.HasSuffix(low, ".zip") && strings.Contains(low, "portable") {
					info.DirectZipURL = dlURL
				}
			}
		}
	} else if code != 404 {
		// 404 on /releases/latest just means no GitHub Release tag published yet
		recordErr(code, err)
	}

	// Ensure a fallback raw setup URL is available whenever we know the latest version
	if info.FallbackSetupURL == "" && info.LatestVersion != "" {
		if tok != "" {
			info.FallbackSetupURL = fmt.Sprintf(
				"https://api.github.com/repos/%s/%s/contents/installer/DariusDB-Setup-%s.exe",
				defaultRepoOwner, defaultRepoName, info.LatestVersion,
			)
		} else {
			info.FallbackSetupURL = fmt.Sprintf("%s/DariusDB-Setup-%s.exe", defaultRawInstallerBase, info.LatestVersion)
		}
	}
	if info.DirectSetupURL == "" && info.FallbackSetupURL != "" {
		info.DirectSetupURL = info.FallbackSetupURL
	}

	info.FetchOK = gotVersion || gotCommits || gotRelease
	if len(sources) > 0 {
		info.FetchSource = "GitHub (" + strings.Join(sources, " + ") + ")"
	}
	if !info.FetchOK {
		switch lastStatus {
		case 404:
			info.RepoPrivateHint = true
			if tok != "" {
				info.FetchError = fmt.Sprintf(
					"GitHub returned HTTP 404 for %s/%s even with your token. Ensure the token has 'Contents: Read-only' (or 'repo') permission and the repository exists.",
					defaultRepoOwner, defaultRepoName,
				)
			} else {
				info.FetchError = fmt.Sprintf(
					"GitHub returned HTTP 404 for %s/%s. This happens when the GitHub repository is set to Private. Fix: Make the repo Public in GitHub Settings → Danger Zone, OR save a GitHub Personal Access Token below.",
					defaultRepoOwner, defaultRepoName,
				)
			}
		case 401, 403:
			info.RepoPrivateHint = true
			info.FetchError = fmt.Sprintf(
				"GitHub returned HTTP %d (authentication required or API rate limit reached). Add a valid GitHub Personal Access Token below or try again shortly.",
				lastStatus,
			)
		default:
			if lastErr != nil {
				info.FetchError = fmt.Sprintf("Unable to reach GitHub: %v", lastErr)
			} else {
				info.FetchError = "Unable to fetch release metadata from GitHub."
			}
		}
	}

	verCmp := compareVersions(info.LatestVersion, info.CurrentVersion)
	info.HasUpdate = verCmp > 0 || (verCmp == 0 && info.LatestBuild != "" && info.LatestBuild > info.CurrentBuild)
	return info
}

// SyncFromGitHub fetches the newest version.json, recent commits, and catalog.json
// directly from GitHub and saves them into the portable root folder.
func SyncFromGitHub(p *Paths, currentVersion string, tokens ...string) (*SystemUpdateInfo, error) {
	tok := resolveGitHubToken(tokens...)
	if tok == "" && p != nil {
		tok = resolveGitHubToken(LoadSettings(p.Root).Get().GitHubToken)
	}
	info, err := CheckSystemUpdate(currentVersion, tok)
	if err != nil {
		return nil, err
	}
	// Only persist synced version.json when GitHub fetch actually succeeded
	if info.FetchOK && p != nil {
		if b, err := json.MarshalIndent(info, "", "  "); err == nil {
			_ = os.WriteFile(filepath.Join(p.Root, "version.json"), b, 0o644)
		}
	}
	// Also refresh catalog.json from GitHub + upstream APIs
	if p != nil {
		_, _ = RefreshCatalogWithToken(p, "", tok)
	}
	return info, nil
}

// ApplySystemUpdateWithFallback tries primaryURL first, then fallbackURL if primaryURL fails.
func ApplySystemUpdateWithFallback(p *Paths, primaryURL, fallbackURL string, tokens ...string) (string, error) {
	primaryURL = strings.TrimSpace(primaryURL)
	fallbackURL = strings.TrimSpace(fallbackURL)
	if primaryURL == "" {
		primaryURL = fallbackURL
	}
	if primaryURL == "" {
		return "", errors.New("no direct download URL available — publish a GitHub Release or pick a local DariusDB-Setup-*.exe / .zip file")
	}
	msg, err := ApplySystemUpdateFromURL(p, primaryURL, tokens...)
	if err == nil {
		return msg, nil
	}
	if fallbackURL != "" && !strings.EqualFold(fallbackURL, primaryURL) {
		if msg2, err2 := ApplySystemUpdateFromURL(p, fallbackURL, tokens...); err2 == nil {
			return msg2, nil
		}
	}
	return "", err
}

// resolveGitHubReleaseDownloadURL converts a GitHub web release download URL
// (github.com/<owner>/<repo>/releases/download/<tag>/<file>) into its GitHub API
// asset URL (api.github.com/repos/<owner>/<repo>/releases/assets/<id>) so private repo
// downloads succeed reliably with Bearer auth.
func resolveGitHubReleaseDownloadURL(client *http.Client, rawURL, tok string) string {
	const marker = "github.com/"
	low := strings.ToLower(rawURL)
	idx := strings.Index(low, marker)
	if idx < 0 || !strings.Contains(low, "/releases/download/") {
		return rawURL
	}
	rest := rawURL[idx+len(marker):]
	parts := strings.Split(rest, "/")
	// Expected: owner / repo / releases / download / tag / filename
	if len(parts) < 6 || !strings.EqualFold(parts[2], "releases") || !strings.EqualFold(parts[3], "download") {
		return rawURL
	}
	owner, repo, tag, assetName := parts[0], parts[1], parts[4], parts[5]
	apiURL := fmt.Sprintf("https://api.github.com/repos/%s/%s/releases/tags/%s", owner, repo, tag)
	var rel struct {
		Assets []struct {
			Name string `json:"name"`
			URL  string `json:"url"`
		} `json:"assets"`
	}
	if _, err := getJSONNoCache(client, apiURL, tok, "application/vnd.github+json", &rel); err == nil {
		for _, a := range rel.Assets {
			if strings.EqualFold(a.Name, assetName) && a.URL != "" {
				return a.URL
			}
		}
	}
	return rawURL
}

// ApplySystemUpdateFromURL downloads a Darius DB Setup .exe or Portable .zip from downloadURL
// into %TEMP%\DariusDB-Update\ and launches the in-place system update.
func ApplySystemUpdateFromURL(p *Paths, downloadURL string, tokens ...string) (string, error) {
	downloadURL = strings.TrimSpace(downloadURL)
	if downloadURL == "" {
		return "", errors.New("no direct download URL provided — download DariusDB-Setup.exe from GitHub/Google Drive or pick a local file")
	}
	var savedTok string
	if p != nil {
		savedTok = LoadSettings(p.Root).Get().GitHubToken
	}
	allToks := append(append([]string(nil), tokens...), savedTok)
	candidateTokens := buildCandidateTokens(allToks...)
	if len(candidateTokens) == 0 {
		candidateTokens = []string{""}
	}

	stageDir := filepath.Join(os.TempDir(), "DariusDB-Update")
	if err := os.MkdirAll(stageDir, 0o755); err != nil {
		return "", err
	}
	ext := ".exe"
	if strings.Contains(strings.ToLower(downloadURL), ".zip") {
		ext = ".zip"
	}
	dest := filepath.Join(stageDir, "DariusDB-System-Update"+ext)

	client := &http.Client{Timeout: 10 * time.Minute}
	var lastErr error
	for _, tok := range candidateTokens {
		targetURL := downloadURL
		if tok != "" {
			targetURL = resolveGitHubReleaseDownloadURL(client, downloadURL, tok)
		}
		req, err := http.NewRequest("GET", targetURL, nil)
		if err != nil {
			return "", err
		}
		req.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) DariusDB-Updater/0.1.3")
		if strings.Contains(strings.ToLower(targetURL), "api.github.com/repos/") {
			if strings.Contains(strings.ToLower(targetURL), "/contents/") {
				req.Header.Set("Accept", "application/vnd.github.raw+json")
			} else {
				req.Header.Set("Accept", "application/octet-stream")
			}
		}
		if tok != "" && isGitHubHost(targetURL) {
			req.Header.Set("Authorization", "Bearer "+tok)
		}

		resp, err := client.Do(req)
		if err != nil {
			lastErr = fmt.Errorf("download failed: %w", err)
			continue
		}
		if resp.StatusCode == 404 || resp.StatusCode == 401 || resp.StatusCode == 403 {
			code := resp.StatusCode
			resp.Body.Close()
			if code == 404 && isGitHubHost(targetURL) && tok == "" {
				lastErr = errors.New("GitHub returned HTTP 404 for the update binary — either the repository is Private (make it Public or save a GitHub Token) or the release asset is not uploaded yet")
			} else {
				lastErr = fmt.Errorf("download returned HTTP %d", code)
			}
			continue
		}
		if resp.StatusCode != 200 {
			code := resp.StatusCode
			resp.Body.Close()
			return "", fmt.Errorf("download returned HTTP %d", code)
		}
		ct := strings.ToLower(resp.Header.Get("Content-Type"))
		if strings.Contains(ct, "text/html") {
			resp.Body.Close()
			return "", errors.New("the URL points to a web page (e.g. Google Drive folder view) rather than a direct .exe/.zip binary — download the file first, then click 'Select Setup .exe / Portable .zip & Update Now'")
		}

		out, err := os.Create(dest)
		if err != nil {
			resp.Body.Close()
			return "", err
		}
		n, cerr := io.Copy(out, resp.Body)
		out.Close()
		resp.Body.Close()
		if cerr != nil {
			_ = os.Remove(dest)
			return "", cerr
		}
		if n < 64*1024 {
			_ = os.Remove(dest)
			return "", fmt.Errorf("downloaded file is too small (%d bytes) — verify the direct download URL", n)
		}
		return ApplySystemUpdateFromFile(p, dest)
	}
	if lastErr != nil {
		return "", lastErr
	}
	return "", errors.New("download failed")
}

func validateUpdateBinary(filePath, ext string) error {
	f, err := os.Open(filePath)
	if err != nil {
		return err
	}
	defer f.Close()
	var hdr [4]byte
	if _, err := io.ReadFull(f, hdr[:]); err != nil {
		return fmt.Errorf("invalid or empty update file: %w", err)
	}
	if ext == ".exe" && (hdr[0] != 'M' || hdr[1] != 'Z') {
		return errors.New("the selected .exe file is not a valid Windows executable (missing MZ header)")
	}
	if ext == ".zip" && (hdr[0] != 'P' || hdr[1] != 'K') {
		return errors.New("the selected .zip file is not a valid ZIP archive (missing PK header)")
	}
	return nil
}

// ApplySystemUpdateFromFile launches an in-place update of Darius DB from a local
// DariusDB-Setup-*.exe or DariusDB-*-portable-*.zip file and restarts Darius DB.
func ApplySystemUpdateFromFile(p *Paths, filePath string) (string, error) {
	filePath = strings.TrimSpace(filePath)
	st, err := os.Stat(filePath)
	if err != nil || st.IsDir() {
		return "", fmt.Errorf("update file not found: %s", filePath)
	}
	ext := strings.ToLower(filepath.Ext(filePath))
	if ext != ".exe" && ext != ".zip" {
		return "", errors.New("select a DariusDB-Setup-*.exe installer or DariusDB-*-portable-*.zip archive")
	}
	if err := validateUpdateBinary(filePath, ext); err != nil {
		return "", err
	}

	// Stage outside p.Root (%TEMP%\DariusDB-Update) so killing processes inside p.Root never locks or deletes the updater
	stageDir := filepath.Join(os.TempDir(), "DariusDB-Update")
	if err := os.MkdirAll(stageDir, 0o755); err != nil {
		return "", err
	}
	stagedPath := filepath.Join(stageDir, "staged-update"+ext)
	if !strings.EqualFold(filepath.Clean(filePath), filepath.Clean(stagedPath)) {
		in, err := os.Open(filePath)
		if err != nil {
			return "", err
		}
		out, err := os.Create(stagedPath)
		if err != nil {
			in.Close()
			return "", err
		}
		_, copyErr := io.Copy(out, in)
		out.Close()
		in.Close()
		if copyErr != nil {
			return "", copyErr
		}
	}

	scriptPath := filepath.Join(stageDir, "apply-system-update.ps1")
	escPS := func(s string) string { return strings.ReplaceAll(s, "'", "''") }

	var actionBlock string
	if ext == ".exe" {
		actionBlock = fmt.Sprintf(
			`Start-Process -FilePath '%s' -ArgumentList '--silent','--force','--dir=%s' -Wait`,
			escPS(stagedPath), escPS(p.Root),
		)
	} else {
		extractDir := filepath.Join(stageDir, "extract")
		actionBlock = fmt.Sprintf(`Remove-Item -Recurse -Force '%s' -ErrorAction SilentlyContinue
Add-Type -AssemblyName System.IO.Compression.FileSystem
[System.IO.Compression.ZipFile]::ExtractToDirectory('%s', '%s')
$allowed = @('DariusDB.exe','DariusDB-Launcher.exe','DariusDB-Uninstall.exe','DariusDB-Updater.exe','darius.exe','LICENSE','README.md','GUIDE.md','version.json')
Get-ChildItem -LiteralPath '%s' -Recurse -File | Where-Object { $allowed -contains $_.Name } | ForEach-Object {
    Copy-Item -LiteralPath $_.FullName -Destination (Join-Path '%s' $_.Name) -Force
}
Remove-Item -Recurse -Force '%s' -ErrorAction SilentlyContinue`,
			escPS(extractDir),
			escPS(stagedPath), escPS(extractDir),
			escPS(extractDir), escPS(p.Root),
			escPS(extractDir),
		)
	}

	launcher := filepath.Join(p.Root, "DariusDB-Launcher.exe")
	mainExe := filepath.Join(p.Root, "DariusDB.exe")
	psContent := fmt.Sprintf(`$ProgressPreference = 'SilentlyContinue'
Start-Sleep -Milliseconds 1200
$rootClean = '%s'.TrimEnd('\').ToLower()
for ($i = 0; $i -lt 15; $i++) {
    $procs = Get-Process -Name "DariusDB","DariusDB-Launcher","DariusDB-Updater" -ErrorAction SilentlyContinue | Where-Object {
        -not $_.Path -or $_.Path.ToLower().StartsWith($rootClean)
    }
    if (-not $procs) { break }
    $procs | ForEach-Object { Stop-Process -Id $_.Id -Force -ErrorAction SilentlyContinue }
    Start-Sleep -Milliseconds 400
}
%s
Remove-Item -Force '%s' -ErrorAction SilentlyContinue
Start-Sleep -Milliseconds 400
if (Test-Path -LiteralPath '%s') {
    Start-Process -FilePath '%s' -WorkingDirectory '%s'
} elseif (Test-Path -LiteralPath '%s') {
    Start-Process -FilePath '%s' -WorkingDirectory '%s'
}
`,
		escPS(p.Root),
		actionBlock,
		escPS(stagedPath),
		escPS(launcher), escPS(launcher), escPS(p.Root),
		escPS(mainExe), escPS(mainExe), escPS(p.Root),
	)

	if err := os.WriteFile(scriptPath, []byte(strings.ReplaceAll(psContent, "\n", "\r\n")), 0o644); err != nil {
		return "", err
	}

	cmd := exec.Command("powershell.exe", "-NoProfile", "-NonInteractive", "-WindowStyle", "Hidden", "-ExecutionPolicy", "Bypass", "-File", scriptPath)
	cmd.Dir = stageDir
	hideWindow(cmd)
	if err := cmd.Start(); err != nil {
		return "", fmt.Errorf("failed to start updater helper: %w", err)
	}
	return fmt.Sprintf("Updating Darius DB in %s from %s and restarting...", p.Root, filepath.Base(filePath)), nil
}

// LaunchStandaloneUpdater starts DariusDB-Updater.exe if present in the install directory or installer folder.
func LaunchStandaloneUpdater(p *Paths) error {
	candidates := []string{
		filepath.Join(p.Root, "DariusDB-Updater.exe"),
		filepath.Join(p.Root, "installer", "DariusDB-Updater.exe"),
		filepath.Join(p.Root, "build", "bin", "DariusDB-Updater.exe"),
	}
	for _, c := range candidates {
		if fileExists(c) {
			cmd := exec.Command(c, "--dir="+p.Root)
			cmd.Dir = p.Root
			return cmd.Start()
		}
	}
	return fmt.Errorf("DariusDB-Updater.exe was not found in %s", p.Root)
}
