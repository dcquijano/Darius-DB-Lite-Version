package core

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

const (
	defaultVersionJSONURL   = "https://raw.githubusercontent.com/dcquijano/Darius-DB-Lite-Version/main/version.json"
	defaultGitHubRelURL     = "https://api.github.com/repos/dcquijano/Darius-DB-Lite-Version/releases/latest"
	defaultGitHubCommitsURL = "https://api.github.com/repos/dcquijano/Darius-DB-Lite-Version/commits?per_page=8"
	defaultDriveFolderURL   = "https://drive.google.com/drive/folders/1FVLCHqOfxmP9PbSx70jy_BfH2Yn1E_UG?usp=sharing"
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
	HasUpdate        bool               `json:"hasUpdate"`
	Channel          string             `json:"channel"`
	ReleasedAt       string             `json:"releasedAt"`
	Author           string             `json:"author"`
	InstallerURL     string             `json:"installerUrl"`
	DirectSetupURL   string             `json:"directSetupUrl"`
	RepoURL          string             `json:"repoUrl"`
	Announcement     string             `json:"announcement"`
	Changelog        []string           `json:"changelog"`
	LatestCommitSHA  string             `json:"latestCommitSha"`
	LatestCommitMsg  string             `json:"latestCommitMsg"`
	LatestCommitDate string             `json:"latestCommitDate"`
	RecentCommits    []GitHubCommitInfo `json:"recentCommits"`
	CheckedAt        string             `json:"checkedAt"`
}

func getJSONNoCache(client *http.Client, rawURL string, dest interface{}) error {
	sep := "?"
	if strings.Contains(rawURL, "?") {
		sep = "&"
	}
	bustURL := fmt.Sprintf("%s%st=%d", rawURL, sep, time.Now().UnixMilli())
	req, err := http.NewRequest("GET", bustURL, nil)
	if err != nil {
		return err
	}
	req.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) DariusDB-Updater/0.1.2")
	req.Header.Set("Cache-Control", "no-cache, no-store, must-revalidate")
	req.Header.Set("Pragma", "no-cache")
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

// CheckSystemUpdate checks the official GitHub version.json, GitHub Commits API, and GitHub Releases API
// (bypassing CDN cache) for newly pushed updates, announcements, and release binaries.
func CheckSystemUpdate(currentVersion string) (*SystemUpdateInfo, error) {
	if currentVersion == "" {
		currentVersion = CLIAppVersion
	}
	info := &SystemUpdateInfo{
		Name:           "Darius DB (Lite Version " + currentVersion + ")",
		CurrentVersion: currentVersion,
		LatestVersion:  currentVersion,
		HasUpdate:      false,
		Channel:        "Lite Version",
		ReleasedAt:     "2026-11-19",
		Author:         CLIAppAuthor,
		InstallerURL:   defaultDriveFolderURL,
		RepoURL:        CLIAppRepo,
		Announcement:   "Darius DB (Lite Version 0.1.2) — Live GitHub Sync enabled! Includes MS SQL Server default PC User Server Name & dbdarius/admin credentials, Apache Drag & Drop Hosting with Trust Author & Folder Permissions, PHP 8.4/8.3/8.2, Mailpit (:8025 / SMTP :1025), .bak/.sql Restore & Backup, and 1-Click Self-Repair.",
		Changelog: []string{
			"Set default MS SQL Server Name to Windows PC User Name and default credentials to dbdarius / admin",
			"Added Trust Author & Folder Permissions dialog and 40-file chunked batch uploader for Apache Drag & Drop Hosting",
			"Added built-in System Version Updater with live GitHub commit & version.json fetching",
			"Added PHP (8.4.4, 8.3.17 LTS, 8.2.27 LTS, 8.1.31) and Mailpit (:8025 Web Inbox + :1025 SMTP Capture)",
			"Added MS SQL Server 2025 (17.x) .bak & .sql Database Restore and Backup",
			"Added 1-Click Self-Repair & Diagnostics and 3-Step Win32 Uninstall & Maintenance Wizard",
		},
		RecentCommits: []GitHubCommitInfo{},
		CheckedAt:     time.Now().Format("2006-01-02 15:04:05"),
	}

	client := &http.Client{Timeout: 10 * time.Second}

	// 1. Check remote version.json in repository (cache-busted)
	var rem struct {
		Name           string   `json:"name"`
		Version        string   `json:"version"`
		Channel        string   `json:"channel"`
		ReleasedAt     string   `json:"releasedAt"`
		Author         string   `json:"author"`
		InstallerURL   string   `json:"installerUrl"`
		DirectSetupURL string   `json:"directSetupUrl"`
		RepoURL        string   `json:"repoUrl"`
		Announcement   string   `json:"announcement"`
		Changelog      []string `json:"changelog"`
	}
	if getJSONNoCache(client, defaultVersionJSONURL, &rem) == nil && rem.Version != "" {
		info.LatestVersion = strings.TrimPrefix(strings.TrimSpace(rem.Version), "v")
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
		if rem.RepoURL != "" {
			info.RepoURL = rem.RepoURL
		}
		if rem.Announcement != "" {
			info.Announcement = rem.Announcement
		}
		if len(rem.Changelog) > 0 {
			info.Changelog = rem.Changelog
		}
	}

	// 2. Fetch live recent commits pushed to GitHub (api.github.com/repos/dcquijano/Darius-DB-Lite-Version/commits)
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
	if getJSONNoCache(client, defaultGitHubCommitsURL, &ghCommits) == nil && len(ghCommits) > 0 {
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
	}

	// 3. Also check GitHub Releases API in case a tagged release has a direct Setup .exe asset
	var ghRel struct {
		TagName     string `json:"tag_name"`
		PublishedAt string `json:"published_at"`
		Body        string `json:"body"`
		Assets      []struct {
			Name               string `json:"name"`
			BrowserDownloadURL string `json:"browser_download_url"`
		} `json:"assets"`
	}
	if getJSONNoCache(client, defaultGitHubRelURL, &ghRel) == nil && ghRel.TagName != "" {
		relVer := parseVersion(ghRel.TagName)
		if relVer != "" && compareVersions(relVer, info.LatestVersion) >= 0 {
			info.LatestVersion = relVer
			for _, a := range ghRel.Assets {
				low := strings.ToLower(a.Name)
				if strings.HasSuffix(low, ".exe") && strings.Contains(low, "setup") {
					info.DirectSetupURL = a.BrowserDownloadURL
					break
				}
			}
		}
	}

	info.HasUpdate = compareVersions(info.LatestVersion, info.CurrentVersion) > 0
	return info, nil
}

// SyncFromGitHub fetches the newest version.json, recent commits, and catalog.json
// directly from GitHub and saves them into the portable root folder.
func SyncFromGitHub(p *Paths, currentVersion string) (*SystemUpdateInfo, error) {
	info, err := CheckSystemUpdate(currentVersion)
	if err != nil {
		return nil, err
	}
	// Save synced version.json locally in p.Root
	if b, err := json.MarshalIndent(info, "", "  "); err == nil {
		_ = os.WriteFile(filepath.Join(p.Root, "version.json"), b, 0o644)
	}
	// Also refresh catalog.json from GitHub + upstream APIs
	_, _ = RefreshCatalog(p, "")
	return info, nil
}

// ApplySystemUpdateFromURL downloads a Darius DB Setup .exe or Portable .zip from downloadURL
// into tmp/ and launches the in-place system update.
func ApplySystemUpdateFromURL(p *Paths, downloadURL string) (string, error) {
	downloadURL = strings.TrimSpace(downloadURL)
	if downloadURL == "" {
		return "", errors.New("no direct download URL provided — download DariusDB-Setup.exe from Google Drive or pick a local file")
	}
	if err := os.MkdirAll(p.Tmp, 0o755); err != nil {
		return "", err
	}
	ext := ".exe"
	if strings.Contains(strings.ToLower(downloadURL), ".zip") {
		ext = ".zip"
	}
	dest := filepath.Join(p.Tmp, "DariusDB-System-Update"+ext)

	client := &http.Client{Timeout: 5 * time.Minute}
	req, err := http.NewRequest("GET", downloadURL, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) DariusDB-Updater/0.1.2")
	resp, err := client.Do(req)
	if err != nil {
		return "", fmt.Errorf("download failed: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return "", fmt.Errorf("download returned HTTP %d", resp.StatusCode)
	}
	ct := strings.ToLower(resp.Header.Get("Content-Type"))
	if strings.Contains(ct, "text/html") {
		return "", errors.New("the URL points to a web page (e.g. Google Drive folder view) rather than a direct .exe/.zip binary — click 'Open Official Google Drive Installer Folder' to download the file, then click 'Select Setup .exe / Portable .zip & Update Now'")
	}

	out, err := os.Create(dest)
	if err != nil {
		return "", err
	}
	_, cerr := io.Copy(out, resp.Body)
	out.Close()
	if cerr != nil {
		return "", cerr
	}
	return ApplySystemUpdateFromFile(p, dest)
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

	_ = os.MkdirAll(p.Tmp, 0o755)
	stagedPath := filepath.Join(p.Tmp, "staged-update"+ext)
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
		_, _ = io.Copy(out, in)
		out.Close()
		in.Close()
	}

	scriptPath := filepath.Join(p.Tmp, "apply-system-update.ps1")
	escPS := func(s string) string { return strings.ReplaceAll(s, "'", "''") }

	var actionBlock string
	if ext == ".exe" {
		actionBlock = fmt.Sprintf(
			`Start-Process -FilePath '%s' -ArgumentList '--silent','--force','--dir=%s' -Wait`,
			escPS(stagedPath), escPS(p.Root),
		)
	} else {
		actionBlock = fmt.Sprintf(
			`Expand-Archive -LiteralPath '%s' -DestinationPath '%s' -Force`,
			escPS(stagedPath), escPS(p.Root),
		)
	}

	launcher := filepath.Join(p.Root, "DariusDB-Launcher.exe")
	psContent := fmt.Sprintf(`Start-Sleep -Seconds 2
Get-Process -Name "DariusDB*" -ErrorAction SilentlyContinue | Stop-Process -Force -ErrorAction SilentlyContinue
Start-Sleep -Milliseconds 600
%s
Remove-Item -Force '%s' -ErrorAction SilentlyContinue
if (Test-Path -LiteralPath '%s') {
    Start-Process -FilePath '%s'
} elseif (Test-Path -LiteralPath '%s') {
    Start-Process -FilePath '%s'
}
`,
		actionBlock,
		escPS(stagedPath),
		escPS(launcher), escPS(launcher),
		escPS(filepath.Join(p.Root, "DariusDB.exe")), escPS(filepath.Join(p.Root, "DariusDB.exe")),
	)

	if err := os.WriteFile(scriptPath, []byte(strings.ReplaceAll(psContent, "\n", "\r\n")), 0o644); err != nil {
		return "", err
	}

	cmd := exec.Command("powershell.exe", "-NoProfile", "-NonInteractive", "-WindowStyle", "Hidden", "-ExecutionPolicy", "Bypass", "-File", scriptPath)
	cmd.Dir = os.TempDir()
	hideWindow(cmd)
	if err := cmd.Start(); err != nil {
		return "", fmt.Errorf("failed to start updater helper: %w", err)
	}
	return fmt.Sprintf("Updating Darius DB in %s from %s and restarting...", p.Root, filepath.Base(filePath)), nil
}
