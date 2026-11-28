//go:build windows

package main

import (
	"archive/zip"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"dariusdb/internal/core"
)

func TestParseSelectionArgs(t *testing.T) {
	allPrereqs := parsePrereqSelectionArg("all")
	if len(allPrereqs) != 3 {
		t.Fatalf("expected 3 prereqs for 'all', got %v", allPrereqs)
	}
	customPrereqs := parsePrereqSelectionArg("webview2, vc++, localdb, wv2")
	if len(customPrereqs) != 3 {
		t.Fatalf("expected deduplicated 3 prereqs, got %v", customPrereqs)
	}
	if len(parsePrereqSelectionArg("none")) != 0 {
		t.Fatalf("expected empty prereqs for 'none'")
	}

	rec := parseEngineSelectionArg("recommended")
	if len(rec) != 3 {
		t.Fatalf("expected 3 recommended engines (mariadb, php, apache), got %v", rec)
	}
	allEng := parseEngineSelectionArg("all")
	if len(allEng) != len(setupEngineOrder) {
		t.Fatalf("expected %d engines for 'all', got %v", len(setupEngineOrder), allEng)
	}
	customEng := parseEngineSelectionArg("maria, postgres, sqlcmd, httpd, pg")
	if strings.Join(customEng, ",") != "mariadb,postgresql,mssql,apache" {
		t.Fatalf("unexpected custom engine parse: %v", customEng)
	}
	if len(parseEngineSelectionArg("lite")) != 0 {
		t.Fatalf("expected empty engines for 'lite'")
	}
}

func TestDiscoverOfflineDirsAndProbeEnvironment(t *testing.T) {
	base := t.TempDir()
	installDir := filepath.Join(base, "DariusDB")
	offlinePack := filepath.Join(base, "usb-offline")
	if err := os.MkdirAll(filepath.Join(installDir, "bin", "mariadb", "mariadb-13.1.2", "bin"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(offlinePack, "prereqs"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(offlinePack, "engines"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(offlinePack, "prereqs", "vc_redist.x64.exe"), []byte("vc"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(offlinePack, "engines", "mariadb-13.1.2.zip"), []byte("PK\x03\x04"), 0o644); err != nil {
		t.Fatal(err)
	}

	// Self exe placed directly inside installDir must not treat installDir\bin as an offline bundle.
	dirs := discoverOfflineDirs(offlinePack, filepath.Join(installDir, "DariusDB-Setup.exe"), installDir)
	for _, d := range dirs {
		if strings.EqualFold(filepath.Clean(d), filepath.Clean(installDir)) {
			t.Fatalf("discoverOfflineDirs must never return installDir root %q directly", d)
		}
	}

	snap := probeSetupEnvironment(offlinePack, filepath.Join(base, "DariusDB-Setup.exe"), installDir)
	if !snap.HasOfflinePack {
		t.Fatal("expected HasOfflinePack=true when offline prereq/engine files exist")
	}
	if snap.Prereqs[1].OfflinePath == "" {
		t.Fatal("expected VC++ offline installer path to be detected")
	}
	foundMariaOffline := false
	for _, e := range snap.Engines {
		if e.ID == "mariadb" && e.OfflinePath != "" && strings.Contains(formatEngineCheckboxLabel(e), "[Offline]") {
			foundMariaOffline = true
		}
	}
	if !foundMariaOffline {
		t.Fatalf("expected mariadb to be detected as [Offline] in snapshot: %+v", snap.Engines)
	}
}

func TestInstallSelectedComponentsOffline(t *testing.T) {
	base := t.TempDir()
	installDir := filepath.Join(base, "install")
	offlineDir := filepath.Join(base, "offline")
	if err := os.MkdirAll(filepath.Join(offlineDir, "engines"), 0o755); err != nil {
		t.Fatal(err)
	}
	mariaBin := filepath.Join(offlineDir, "bin", "mariadb", "mariadb-13.1.2", "bin")
	if err := os.MkdirAll(mariaBin, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(mariaBin, "mysqld.exe"), []byte("mysqld-stub"), 0o644); err != nil {
		t.Fatal(err)
	}

	// Create valid offline Redis zip
	redisZip := filepath.Join(offlineDir, "engines", "redis-8.10.3.zip")
	f, err := os.Create(redisZip)
	if err != nil {
		t.Fatal(err)
	}
	zw := zip.NewWriter(f)
	w, err := zw.Create("redis-8.10.3/redis-server.exe")
	if err != nil {
		t.Fatal(err)
	}
	_, _ = w.Write([]byte("redis-stub"))
	_ = zw.Close()
	_ = f.Close()

	opts := installOptions{
		dir:         installDir,
		engines:     []string{"mariadb", "redis", "postgresql"}, // postgresql missing -> warning only
		offlineDir:  offlineDir,
		offlineOnly: true,
	}

	var logs []string
	installed, warnings := installSelectedComponents(context.Background(), opts, 35, 80, func(_ int, msg string) {
		logs = append(logs, msg)
	}, nil)

	if len(installed) != 2 {
		t.Fatalf("expected 2 offline engines installed (mariadb, redis), got installed=%v warnings=%v logs=%v", installed, warnings, logs)
	}
	if len(warnings) != 1 || !strings.Contains(warnings[0], "postgresql") {
		t.Fatalf("expected 1 warning for missing offline postgresql, got %v", warnings)
	}
	if !fileExists(filepath.Join(installDir, "bin", "mariadb", "mariadb-13.1.2", "bin", "mysqld.exe")) {
		t.Fatal("expected mariadb mysqld.exe installed from offline dir")
	}
	if !fileExists(filepath.Join(installDir, "bin", "redis", "redis-8.10.3", "redis-server.exe")) {
		t.Fatal("expected redis-server.exe extracted from offline zip")
	}

	st := core.LoadSettings(installDir)
	if st.Engine("mariadb").Version != "mariadb-13.1.2" {
		t.Fatalf("expected settings.json mariadb version set to mariadb-13.1.2, got %q", st.Engine("mariadb").Version)
	}
	if st.Engine("redis").Version != "redis-8.10.3" {
		t.Fatalf("expected settings.json redis version set to redis-8.10.3, got %q", st.Engine("redis").Version)
	}
}
