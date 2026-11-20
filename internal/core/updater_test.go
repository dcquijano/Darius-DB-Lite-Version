package core

import (
	"os"
	"path/filepath"
	"testing"
)

func TestValidateUpdateBinary(t *testing.T) {
	dir := t.TempDir()

	goodExe := filepath.Join(dir, "good.exe")
	if err := os.WriteFile(goodExe, []byte("MZ\x90\x00testpayload"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := validateUpdateBinary(goodExe, ".exe"); err != nil {
		t.Fatalf("expected valid exe, got %v", err)
	}

	badExe := filepath.Join(dir, "bad.exe")
	if err := os.WriteFile(badExe, []byte("<html>404</html>"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := validateUpdateBinary(badExe, ".exe"); err == nil {
		t.Fatal("expected error for HTML disguised as .exe")
	}

	goodZip := filepath.Join(dir, "good.zip")
	if err := os.WriteFile(goodZip, []byte("PK\x03\x04testzip"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := validateUpdateBinary(goodZip, ".zip"); err != nil {
		t.Fatalf("expected valid zip, got %v", err)
	}

	badZip := filepath.Join(dir, "bad.zip")
	if err := os.WriteFile(badZip, []byte("not-a-zip"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := validateUpdateBinary(badZip, ".zip"); err == nil {
		t.Fatal("expected error for invalid zip")
	}
}

func TestSettingsGitHubTokenRoundTrip(t *testing.T) {
	dir := t.TempDir()
	st := LoadSettings(dir)
	if err := st.Update(func(s *Settings) {
		s.GitHubToken = "github_pat_test_12345"
	}); err != nil {
		t.Fatal(err)
	}
	reloaded := LoadSettings(dir)
	if got := reloaded.Get().GitHubToken; got != "github_pat_test_12345" {
		t.Fatalf("expected saved token, got %q", got)
	}
}

func TestBuildCandidateTokensDeduplicates(t *testing.T) {
	toks := buildCandidateTokens("tok_a", "tok_b", "tok_a", "  ")
	if len(toks) < 2 || toks[0] != "tok_a" || toks[1] != "tok_b" {
		t.Fatalf("unexpected candidate tokens order: %v", toks)
	}
}

func TestHasUpdateByBuildID(t *testing.T) {
	if !(compareVersions("0.1.3", "0.1.3") == 0 && "202611201155" > "202611201100") {
		t.Fatal("expected newer build ID on same version to compare greater")
	}
}
