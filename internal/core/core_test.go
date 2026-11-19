package core

import (
	"archive/zip"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestVersionHelpers(t *testing.T) {
	if got := parseVersion("mariadb-11.4.14-winx64"); got != "11.4.14" {
		t.Fatalf("parseVersion got %q", got)
	}
	if got := versionPrefix("11.4.14", 2); got != "11.4" {
		t.Fatalf("versionPrefix(2) got %q", got)
	}
	if got := versionPrefix("18.7", 1); got != "18" {
		t.Fatalf("versionPrefix(1) got %q", got)
	}
	if compareVersions("11.10.0", "11.4.14") <= 0 {
		t.Fatal("expected 11.10.0 > 11.4.14")
	}
	if compareVersions("8.4.11", "8.4.11") != 0 {
		t.Fatal("expected equal versions")
	}
}

func TestSplitArgs(t *testing.T) {
	got, err := splitArgs(`SET "hello world" 'a b c' 123`)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"SET", "hello world", "a b c", "123"}
	if len(got) != len(want) {
		t.Fatalf("len %d != %d (%v)", len(got), len(want), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("arg %d: got %q want %q", i, got[i], want[i])
		}
	}
	if _, err := splitArgs(`SET "unclosed`); err == nil {
		t.Fatal("expected error for unbalanced quotes")
	}
}

func TestReturnsRows(t *testing.T) {
	cases := []struct {
		q    string
		want bool
	}{
		{"SELECT 1", true},
		{"-- comment\nSELECT * FROM users", true},
		{"/* multi\nline */ SHOW TABLES;", true},
		{"WITH cte AS (SELECT 1) SELECT * FROM cte", true},
		{"INSERT INTO t VALUES (1) RETURNING id", true},
		{"CREATE TABLE t (id INT)", false},
		{"UPDATE t SET a = 1", false},
		{"DELETE FROM t", false},
		{"-- comment only", false},
	}
	for _, tc := range cases {
		if got := returnsRows(tc.q); got != tc.want {
			t.Errorf("returnsRows(%q) = %v, want %v", tc.q, got, tc.want)
		}
	}
}

func TestQuoting(t *testing.T) {
	if got := quoteIdent("mariadb", "my`db"); got != "`my``db`" {
		t.Fatalf("mariadb quoteIdent got %s", got)
	}
	if got := quoteIdent("postgresql", `my"db`); got != `"my""db"` {
		t.Fatalf("pg quoteIdent got %s", got)
	}
	if got := quoteStr("mysql", `a'b\c`); got != `'a''b\\c'` {
		t.Fatalf("mysql quoteStr got %s", got)
	}
}

func TestSettingsRoundTrip(t *testing.T) {
	dir := t.TempDir()
	st := LoadSettings(dir)
	if err := st.Update(func(s *Settings) {
		s.Engines["mariadb"].Port = 3308
		s.Theme = "dark"
	}); err != nil {
		t.Fatal(err)
	}
	reloaded := LoadSettings(dir)
	got := reloaded.Get()
	if got.Engines["mariadb"].Port != 3308 || got.Theme != "dark" {
		t.Fatalf("unexpected reloaded settings: %+v", got)
	}
	if got.Engines["redis"].Port != 6379 {
		t.Fatalf("default redis port lost: %+v", got.Engines["redis"])
	}
}

func TestEmbeddedCatalog(t *testing.T) {
	p := NewPaths(t.TempDir())
	c := LoadCatalog(p)
	for _, id := range EngineIDs {
		if len(c[id]) == 0 {
			t.Errorf("catalog missing entries for %s", id)
		}
		for _, e := range c[id] {
			if e.Version == "" || !strings.HasPrefix(e.URL, "https://") {
				t.Errorf("invalid catalog entry for %s: %+v", id, e)
			}
		}
	}
}

func makeZip(t *testing.T, path string, files map[string]string) {
	t.Helper()
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	zw := zip.NewWriter(f)
	for name, body := range files {
		w, err := zw.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := w.Write([]byte(body)); err != nil {
			t.Fatal(err)
		}
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestExtractStripsRootAndRejectsZipSlip(t *testing.T) {
	root := t.TempDir()
	p := NewPaths(root)
	if err := p.Ensure(); err != nil {
		t.Fatal(err)
	}
	dl := NewDownloader(p, func(Progress) {}, nil)

	// Valid MariaDB zip with single top folder
	okZip := filepath.Join(root, "ok.zip")
	makeZip(t, okZip, map[string]string{
		"mariadb-11.4.14-winx64/bin/mariadbd.exe": "stub",
	})
	if err := dl.extract(context.Background(), "mariadb", "11.4.14", okZip); err != nil {
		t.Fatalf("extract valid zip failed: %v", err)
	}
	insts := ListInstallations(p, "mariadb")
	if len(insts) != 1 || insts[0].Name != "mariadb-11.4.14" {
		t.Fatalf("unexpected installations: %+v", insts)
	}

	// Malicious zip-slip archive
	badZip := filepath.Join(root, "evil.zip")
	makeZip(t, badZip, map[string]string{
		"../outside.txt": "pwn",
	})
	if err := dl.extract(context.Background(), "redis", "7.4.11", badZip); err == nil {
		t.Fatal("expected zip-slip error")
	}
}

func TestEnsureConfigDoesNotOverwrite(t *testing.T) {
	p := NewPaths(t.TempDir())
	if err := p.Ensure(); err != nil {
		t.Fatal(err)
	}
	if err := EnsureConfig(p, "mariadb"); err != nil {
		t.Fatal(err)
	}
	cfg := filepath.Join(p.EngineEtc("mariadb"), "my.ini")
	custom := "[mysqld]\nmax_connections = 500\n"
	if err := os.WriteFile(cfg, []byte(custom), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := EnsureConfig(p, "mariadb"); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(cfg)
	if string(b) != custom {
		t.Fatalf("EnsureConfig overwrote user config: %q", string(b))
	}
}

func TestBuiltInTerminal(t *testing.T) {
	root := t.TempDir()
	p := NewPaths(root)
	if err := p.Ensure(); err != nil {
		t.Fatal(err)
	}
	st := LoadSettings(root)
	mgr := NewManager(p, st, nil)
	term := NewTerminal(p, st, mgr, nil)

	if !strings.Contains(term.Banner(), "Darius DB (Lite Version 0.1.0) Built-in Terminal") {
		t.Fatalf("unexpected banner: %s", term.Banner())
	}

	// Built-in status table
	res, err := term.Exec("status")
	if err != nil || !strings.Contains(res.Output, "MariaDB") {
		t.Fatalf("status command failed: %+v err=%v", res, err)
	}

	// Built-in cd
	res, err = term.Exec("cd bin")
	if err != nil || !strings.HasSuffix(res.State.Cwd, "bin") {
		t.Fatalf("cd command failed: %+v err=%v", res, err)
	}

	// Interactive DB CLI auto-mode switch
	res, err = term.Exec("psql")
	if err != nil || res.State.Mode != "postgresql" {
		t.Fatalf("psql mode switch failed: %+v err=%v", res, err)
	}
	res, err = term.Exec(`\q`)
	if err != nil || res.State.Mode != "shell" {
		t.Fatalf("\\q exit failed: %+v err=%v", res, err)
	}

	// External shell command execution
	res, err = term.Exec("echo hello-darius-terminal")
	if err != nil || !strings.Contains(res.Output, "hello-darius-terminal") {
		t.Fatalf("shell echo failed: %+v err=%v", res, err)
	}
}

func TestE2ERedis(t *testing.T) {
	if os.Getenv("DARIUSDB_E2E") == "" {
		t.Skip("set DARIUSDB_E2E=1 to run live download & server lifecycle test")
	}
	root := t.TempDir()
	p := NewPaths(root)
	if err := p.Ensure(); err != nil {
		t.Fatal(err)
	}
	st := LoadSettings(root)
	if err := st.Update(func(s *Settings) { s.Engines["redis"].Port = 16379 }); err != nil {
		t.Fatal(err)
	}
	mgr := NewManager(p, st, nil)

	cat := LoadCatalog(p)
	entry := cat["redis"][0]
	done := make(chan Progress, 1)
	dl := NewDownloader(p, func(pr Progress) {
		if pr.Phase == "done" || pr.Phase == "error" || pr.Phase == "cancelled" {
			done <- pr
		}
	}, func(engine, name string) {
		mgr.OnInstalled(engine, name)
	})
	if err := dl.Install("redis", entry.Version, entry.URL); err != nil {
		t.Fatal(err)
	}
	pr := <-done
	if pr.Phase != "done" {
		t.Fatalf("download failed: %+v", pr)
	}
	time.Sleep(100 * time.Millisecond)

	if err := mgr.Start("redis"); err != nil {
		t.Fatalf("start redis: %v", err)
	}
	defer mgr.Stop("redis")

	es := st.Engine("redis")
	if out, err := RedisCommand(es, 0, `SET greeting "hello from darius db"`); err != nil {
		t.Fatalf("SET: %v", err)
	} else if !strings.Contains(out, "OK") {
		t.Fatalf("unexpected SET reply: %s", out)
	}
	dbs, err := RedisDatabases(es)
	if err != nil || len(dbs) == 0 || dbs[0].Keys != 1 {
		t.Fatalf("RedisDatabases: %+v err=%v", dbs, err)
	}
	scan, err := RedisScan(es, 0, "*", "0")
	if err != nil || len(scan.Keys) != 1 || scan.Keys[0].Name != "greeting" {
		t.Fatalf("RedisScan: %+v err=%v", scan, err)
	}
	val, err := RedisGet(es, 0, "greeting")
	if err != nil || val.Value != "hello from darius db" {
		t.Fatalf("RedisGet: %+v err=%v", val, err)
	}
	if err := RedisDelete(es, 0, "greeting"); err != nil {
		t.Fatalf("RedisDelete: %v", err)
	}
	if err := mgr.Stop("redis"); err != nil {
		t.Fatalf("Stop: %v", err)
	}
	if mgr.IsRunning("redis") {
		t.Fatal("redis still running after Stop")
	}
}

