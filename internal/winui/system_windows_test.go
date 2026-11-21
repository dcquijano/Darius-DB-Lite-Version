//go:build windows

package winui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestMergeUserPathAddAndRemove(t *testing.T) {
	initial := `C:\Windows\System32;C:\Tools`
	added, changed := mergeUserPath(initial, []string{`C:\DariusDB`, `C:\DariusDB\bin\mysql\bin`}, true)
	if !changed {
		t.Fatalf("expected changed=true on initial add")
	}
	if !strings.Contains(added, `C:\DariusDB`) || !strings.Contains(added, `C:\DariusDB\bin\mysql\bin`) {
		t.Fatalf("expected added dirs in PATH, got %q", added)
	}

	// Idempotent add should report changed=false.
	_, changedAgain := mergeUserPath(added, []string{`C:\DariusDB\`}, true)
	if changedAgain {
		t.Fatalf("expected idempotent add to report changed=false")
	}

	// Removing root strips both root and nested tool folders while preserving unrelated entries.
	removed, remChanged := mergeUserPath(added, []string{`C:\DariusDB`}, false)
	if !remChanged {
		t.Fatalf("expected changed=true on remove")
	}
	if strings.Contains(strings.ToLower(removed), "dariusdb") {
		t.Fatalf("expected all DariusDB entries stripped, got %q", removed)
	}
	if !strings.Contains(removed, `C:\Windows\System32`) || !strings.Contains(removed, `C:\Tools`) {
		t.Fatalf("expected existing PATH entries preserved, got %q", removed)
	}
}

func TestRestoreCoreBinariesMissingOnly(t *testing.T) {
	base := t.TempDir()
	sourceDir := filepath.Join(base, "installer")
	targetDir := filepath.Join(base, "install")
	if err := os.MkdirAll(sourceDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(targetDir, 0o755); err != nil {
		t.Fatal(err)
	}

	selfExe := filepath.Join(sourceDir, "DariusDB-Repair.exe")
	if err := os.WriteFile(selfExe, []byte("repair-exe"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(sourceDir, "darius.exe"), []byte("new-cli"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(sourceDir, "DariusDB.exe"), []byte("new-app"), 0o644); err != nil {
		t.Fatal(err)
	}
	// Existing file in target should be kept when onlyMissing=true.
	if err := os.WriteFile(filepath.Join(targetDir, "DariusDB.exe"), []byte("existing-app"), 0o644); err != nil {
		t.Fatal(err)
	}

	restored, err := RestoreCoreBinaries(targetDir, selfExe, true)
	if err != nil {
		t.Fatalf("RestoreCoreBinaries failed: %v", err)
	}
	if len(restored) != 2 {
		t.Fatalf("expected DariusDB-Repair.exe and darius.exe restored, got %v", restored)
	}
	gotExisting, _ := os.ReadFile(filepath.Join(targetDir, "DariusDB.exe"))
	if string(gotExisting) != "existing-app" {
		t.Fatalf("expected existing DariusDB.exe untouched in onlyMissing mode, got %q", string(gotExisting))
	}
	gotCli, _ := os.ReadFile(filepath.Join(targetDir, "darius.exe"))
	if string(gotCli) != "new-cli" {
		t.Fatalf("expected missing darius.exe copied, got %q", string(gotCli))
	}
}

func TestDetectInstallDirExplicitClean(t *testing.T) {
	got := DetectInstallDir(`C:\Custom\DariusDB\`)
	if got != `C:\Custom\DariusDB` {
		t.Fatalf("expected cleaned explicit dir, got %q", got)
	}
}
