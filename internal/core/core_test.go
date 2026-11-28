package core

import (
	"archive/zip"
	"context"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
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

	if !strings.Contains(term.Banner(), "Darius DB (Lite Version 0.1.6) Built-in Terminal") {
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
	res, err = term.Exec("sqlcmd")
	if err != nil || res.State.Mode != "mssql" {
		t.Fatalf("sqlcmd mode switch failed: %+v err=%v", res, err)
	}
	res, err = term.Exec("exit")
	if err != nil || res.State.Mode != "shell" {
		t.Fatalf("mssql exit failed: %+v err=%v", res, err)
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

func TestTableAndWhereHelpers(t *testing.T) {
	s1, t1 := splitSchemaTable("mysql", "govsystem", "web_configurations")
	if s1 != "govsystem" || t1 != "web_configurations" {
		t.Fatalf("mysql splitSchemaTable got %q.%q", s1, t1)
	}
	s2, t2 := splitSchemaTable("postgresql", "mydb", "analytics.events")
	if s2 != "analytics" || t2 != "events" {
		t.Fatalf("pg splitSchemaTable got %q.%q", s2, t2)
	}
	if got := qualifiedTable("mssql", "dbo", "users"); got != "[dbo].[users]" {
		t.Fatalf("mssql qualifiedTable got %q", got)
	}

	keys := map[string]RowValue{
		"id":   {Value: "42"},
		"note": {IsNull: true},
	}
	whereSQL, args := buildWhereClause("postgresql", keys, 3)
	if whereSQL != `"id" = $3 AND "note" IS NULL` || len(args) != 1 || args[0] != "42" {
		t.Fatalf("buildWhereClause got %q args=%v", whereSQL, args)
	}
}

func TestLoadSettingsNoDirtyRewriteAndHiddenVersions(t *testing.T) {
	dir := t.TempDir()
	st := LoadSettings(dir)
	if err := st.Update(func(s *Settings) {
		s.Engines["php"].HiddenVersions = []string{"php-8.4.0"}
	}); err != nil {
		t.Fatalf("Save initial settings: %v", err)
	}

	settingsPath := filepath.Join(dir, "settings.json")
	past := time.Now().Add(-1 * time.Hour)
	if err := os.Chtimes(settingsPath, past, past); err != nil {
		t.Fatalf("Chtimes: %v", err)
	}
	infoBefore, err := os.Stat(settingsPath)
	if err != nil {
		t.Fatalf("Stat before: %v", err)
	}

	_ = LoadSettings(dir)
	infoAfter, err := os.Stat(settingsPath)
	if err != nil {
		t.Fatalf("Stat after: %v", err)
	}
	if !infoAfter.ModTime().Equal(infoBefore.ModTime()) {
		t.Fatalf("LoadSettings rewrote unchanged settings.json (before=%v after=%v)", infoBefore.ModTime(), infoAfter.ModTime())
	}

	hidden := readHiddenVersions(dir, "php")
	if len(hidden) != 1 || hidden[0] != "php-8.4.0" {
		t.Fatalf("readHiddenVersions got %v", hidden)
	}
	if !isVersionHidden(&Paths{Root: dir}, "php", "PHP-8.4.0") {
		t.Fatal("expected php-8.4.0 to be hidden")
	}
}

func TestWaitForFastPoll(t *testing.T) {
	calls := 0
	start := time.Now()
	ok := waitFor(time.Second, func() bool {
		calls++
		return calls >= 2
	})
	elapsed := time.Since(start)
	if !ok {
		t.Fatal("expected waitFor to return true")
	}
	if elapsed > 200*time.Millisecond {
		t.Fatalf("expected fast polling under 200ms, took %v", elapsed)
	}
}

func TestSQLPoolCloseAndCacheInvalidate(t *testing.T) {
	InvalidateSystemDetectCache()
	CloseSQLPools("mysql", "")
}

func TestAcquireSingleInstanceReturnsTrueFirstTime(t *testing.T) {
	dir := t.TempDir()
	h, ok := AcquireSingleInstanceWithWait(dir, 50*time.Millisecond)
	if !ok {
		t.Fatal("expected AcquireSingleInstanceWithWait to succeed on fresh root")
	}
	ReleaseSingleInstance(h)
}

func TestIsPostgresArchiveDumpDetection(t *testing.T) {
	dir := t.TempDir()
	customSQL := filepath.Join(dir, "ewater.sql")
	if err := os.WriteFile(customSQL, []byte("PGDMP\x01\x0e\x00"), 0o644); err != nil {
		t.Fatalf("WriteFile customSQL: %v", err)
	}
	if !isPostgresArchiveDump(customSQL) {
		t.Fatal("expected PGDMP custom dump with .sql extension to be detected as archive dump")
	}

	plainSQL := filepath.Join(dir, "plain.backup")
	if err := os.WriteFile(plainSQL, []byte("-- PostgreSQL database dump\nSELECT 1;\n"), 0o644); err != nil {
		t.Fatalf("WriteFile plainSQL: %v", err)
	}
	if isPostgresArchiveDump(plainSQL) {
		t.Fatal("expected plain SQL text not to be detected as archive dump")
	}

	dirDump := filepath.Join(dir, "dir_dump")
	if err := os.MkdirAll(dirDump, 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dirDump, "toc.dat"), []byte("PGDMP"), 0o644); err != nil {
		t.Fatalf("WriteFile toc.dat: %v", err)
	}
	if !isPostgresArchiveDump(dirDump) {
		t.Fatal("expected directory dump with toc.dat to be detected as archive dump")
	}
}

func TestManagerSkipAdoptAndStopAllClearsErrors(t *testing.T) {
	dir := t.TempDir()
	p := NewPaths(dir)
	if err := p.Ensure(); err != nil {
		t.Fatalf("Paths.Ensure: %v", err)
	}
	st := LoadSettings(dir)
	var mgr *Manager
	// Ensure constructor-time ForceOffAll never fires onChange while caller's mgr pointer is still nil
	mgr = NewManagerWithOptions(p, st, func() {
		if mgr == nil {
			t.Fatal("onChange invoked before NewManagerWithOptions returned")
		}
	}, true)
	mgr.set("mssql", func(s *service) { s.state, s.message = StateStopped, "process exited" })
	if err := mgr.StopAll(); err != nil {
		t.Fatalf("StopAll: %v", err)
	}
	for _, s := range mgr.Status() {
		if s.State == StateRunning || s.State == StateStarting {
			t.Fatalf("expected %s to be stopped/offline, got %s", s.Engine, s.State)
		}
		if s.Message != "" {
			t.Fatalf("expected %s error message cleared on StopAll, got %q", s.Engine, s.Message)
		}
	}
}

func TestCloudURIParserAndSettings(t *testing.T) {
	c1, err := ParseCloudURI("mysql://cpanel_user:s3cr3t@server.example.com:3306/cpanel_appdb")
	if err != nil {
		t.Fatalf("ParseCloudURI mysql: %v", err)
	}
	if c1.Engine != "mysql" || c1.Host != "server.example.com" || c1.Port != 3306 || c1.Database != "cpanel_appdb" || c1.User != "cpanel_user" {
		t.Fatalf("unexpected mysql parsed: %+v", c1)
	}

	c2, err := ParseCloudURI("Server=db12345.public.databaseasp.net,1433;Database=db12345;User Id=db12345_user;Password=Pass#1;Encrypt=True;TrustServerCertificate=True;")
	if err != nil {
		t.Fatalf("ParseCloudURI mssql ADO.NET: %v", err)
	}
	if c2.Engine != "mssql" || c2.Provider != "monsterasp" || c2.Host != "db12345.public.databaseasp.net" || c2.Port != 1433 || c2.Database != "db12345" {
		t.Fatalf("unexpected mssql parsed: %+v", c2)
	}
	es := c2.ToEngineSettings()
	if mssqlServerTarget(es) != "db12345.public.databaseasp.net,1433" {
		t.Fatalf("unexpected remote mssql target: %q", mssqlServerTarget(es))
	}

	c3, err := ParseCloudURI("postgresql://postgres:pw@db.xyz.supabase.co:5432/postgres?sslmode=require")
	if err != nil {
		t.Fatalf("ParseCloudURI postgres: %v", err)
	}
	if c3.Engine != "postgresql" || c3.Provider != "supabase" || c3.SSLMode != "require" {
		t.Fatalf("unexpected postgres parsed: %+v", c3)
	}
}

func TestMSSQLSyncAndErrorHelpers(t *testing.T) {
	raw := "(1 rows affected)\r\n(1 rows affected)\r\nChanged database context to 'hris'.\r\nMsg 273, Level 16, State 1, Line 1 Cannot insert an explicit value into a timestamp column."
	got := formatSqlcmdError(raw, nil)
	if strings.Contains(got, "rows affected") || strings.Contains(got, "Changed database context") {
		t.Fatalf("expected noisy sqlcmd lines stripped, got %q", got)
	}
	if !strings.Contains(got, "Msg 273") {
		t.Fatalf("expected Msg 273 preserved, got %q", got)
	}
	if !isMSSQLAutoTimestampType("timestamp") || !isMSSQLAutoTimestampType("rowversion") || isMSSQLAutoTimestampType("datetime2") {
		t.Fatal("unexpected timestamp/rowversion detection")
	}
	wrapped := wrapMSSQLSyncRefresh("INSERT INTO [dbo].[Users] VALUES (1);", []TableInfo{{Schema: "dbo", Name: "Users", Kind: "BASE TABLE"}}, "hris")
	if !strings.Contains(wrapped, "NOCHECK CONSTRAINT ALL") || !strings.Contains(wrapped, "WITH CHECK CHECK CONSTRAINT ALL") || !strings.Contains(wrapped, "DELETE FROM [dbo].[Users]") {
		t.Fatalf("unexpected wrapped T-SQL: %s", wrapped)
	}
}

func TestSchedulerRetentionPurgeAndDueWindow(t *testing.T) {
	dir := t.TempDir()
	now := time.Date(2026, 11, 20, 2, 5, 0, 0, time.Local)

	// Snapshot 8 days old -> meets 1-week (7d) retention limit -> must be purged
	oldFile := filepath.Join(dir, "db70628_20261112_020000.zip")
	// Snapshot exactly 7 days old -> must be purged
	weekFile := filepath.Join(dir, "db70628_20261113_020500.zip")
	// Snapshot 3 days old -> must stay
	keepFile := filepath.Join(dir, "db70628_20261117_020000.zip")

	for _, f := range []string{oldFile, weekFile, keepFile} {
		if err := os.WriteFile(f, []byte("zipdata"), 0o644); err != nil {
			t.Fatalf("WriteFile %s: %v", f, err)
		}
	}

	n, purged, err := PurgeOldSnapshots(dir, 7, now)
	if err != nil {
		t.Fatalf("PurgeOldSnapshots: %v", err)
	}
	if n != 2 || len(purged) != 2 {
		t.Fatalf("expected 2 purged snapshots (>=7 days), got n=%d purged=%v", n, purged)
	}
	if _, err := os.Stat(keepFile); err != nil {
		t.Fatalf("expected 3-day snapshot %s to remain: %v", keepFile, err)
	}
	if _, err := os.Stat(oldFile); !os.IsNotExist(err) {
		t.Fatalf("expected 8-day snapshot %s to be deleted", oldFile)
	}
	if _, err := os.Stat(weekFile); !os.IsNotExist(err) {
		t.Fatalf("expected 7-day snapshot %s to be deleted", weekFile)
	}

	// Due-time checks: 02:00 due at 02:05, not due before 02:00, not due twice on same day,
	// and not due retroactively if created today at 18:05 for 02:00.
	if !isScheduleDue(now, "02:00", "", "2026-11-19 18:00") {
		t.Fatal("expected 02:00 schedule to be due at 02:05")
	}
	if isScheduleDue(now, "02:00", "2026-11-20", "2026-11-19 18:00") {
		t.Fatal("expected schedule already run today not to re-run")
	}
	if isScheduleDue(time.Date(2026, 11, 20, 1, 59, 0, 0, time.Local), "02:00", "", "2026-11-19 18:00") {
		t.Fatal("expected 02:00 schedule not to be due at 01:59")
	}
	if isScheduleDue(time.Date(2026, 11, 20, 18, 10, 0, 0, time.Local), "02:00", "", "2026-11-20 18:05") {
		t.Fatal("expected newly created 02:00 schedule at 18:05 not to run retroactively today")
	}
}

func TestReconcileMSSQLIdentityInserts(t *testing.T) {
	dstMap := map[string]bool{
		"[dbo].[announcements]": false, // existing target table WITHOUT identity -> Msg 8106 guard
		"[dbo].[users]":         true,  // existing target table WITH identity -> Msg 544 guard
	}
	in := strings.Join([]string{
		"SET IDENTITY_INSERT [dbo].[Announcements] ON;",
		"INSERT INTO [dbo].[Announcements] ([Id], [Title]) VALUES (N'1', N'Hi');",
		"SET IDENTITY_INSERT [dbo].[Announcements] OFF;",
		"GO",
		"INSERT INTO [dbo].[Users] ([Id], [Name]) VALUES (N'5', N'Alice');",
		"GO",
	}, "\r\n")
	got := reconcileMSSQLIdentityInserts(in, dstMap)
	if strings.Contains(got, "SET IDENTITY_INSERT [dbo].[Announcements]") {
		t.Fatalf("expected SET IDENTITY_INSERT stripped for non-identity target table, got:\n%s", got)
	}
	if !strings.Contains(got, "SET IDENTITY_INSERT [dbo].[Users] ON;") || !strings.Contains(got, "SET IDENTITY_INSERT [dbo].[Users] OFF;") {
		t.Fatalf("expected SET IDENTITY_INSERT injected for identity target table, got:\n%s", got)
	}
}

func TestMySQLBinaryAndNulEscaping(t *testing.T) {
	if !isMySQLBinaryCol("LONGBLOB") || !isMySQLBinaryCol("varbinary") || isMySQLBinaryCol("VARCHAR") {
		t.Fatal("unexpected binary column detection")
	}
	raw := "abc\x00def'g\\h\r\n\x1a"
	esc := sqlEscape(raw)
	if strings.ContainsRune(esc, '\x00') || strings.ContainsRune(esc, '\n') || strings.ContainsRune(esc, '\r') || strings.ContainsRune(esc, '\x1a') {
		t.Fatalf("expected NUL and control characters escaped in MySQL literal, got %q", esc)
	}
	if !strings.Contains(esc, `\0`) || !strings.Contains(esc, `\\`) || !strings.Contains(esc, `''`) {
		t.Fatalf("expected \\0, \\\\, '' escapes, got %q", esc)
	}
}

func TestMSSQLMultilineAndTabFieldEncoding(t *testing.T) {
	for _, dt := range []string{"varchar", "NVARCHAR", "char", "nchar", "text", "ntext", "xml", "sysname"} {
		if !isMSSQLTextType(dt) {
			t.Fatalf("expected %s to be recognized as MSSQL text type", dt)
		}
	}
	if isMSSQLTextType("int") || isMSSQLTextType("varbinary") {
		t.Fatal("int/varbinary must not be treated as MSSQL text type")
	}

	if got := decodeMSSQLFieldToTSQLExpr("plain text"); got != "N'plain text'" {
		t.Fatalf("unexpected plain expr: %s", got)
	}
	if got := decodeMSSQLFieldToTSQLExpr(`Header\r\n-Void and Discount.\t\\O'Brien`); got != `N'Header' + CHAR(13) + CHAR(10) + N'-Void and Discount.' + CHAR(9) + N'\O''Brien'` {
		t.Fatalf("unexpected decoded multiline/tab T-SQL expr: %s", got)
	}
	if got := decodeMSSQLFieldToTSQLExpr(`\n`); got != "CHAR(10)" {
		t.Fatalf("unexpected newline-only T-SQL expr: %s", got)
	}
}

func TestCloudSyncPreCancelledContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	var events []CloudSyncProgress
	_, err := CloudSyncDatabaseWithContext(ctx, &Paths{Root: t.TempDir()}, "mysql", EngineSettings{}, "src", "mysql", EngineSettings{}, "dst", func(p CloudSyncProgress) {
		events = append(events, p)
	})
	if !errors.Is(err, ErrSyncCancelled) {
		t.Fatalf("expected ErrSyncCancelled, got %v", err)
	}
	if len(events) == 0 || events[len(events)-1].State != "cancelled" {
		t.Fatalf("expected final progress event state='cancelled', got %+v", events)
	}
}

func TestSelectBestLocalDBPID(t *testing.T) {
	// Skip inaccessible SYSTEM SQL Server service (e.g. PID 1700) and pick user LocalDB sqlservr.exe
	got := selectBestLocalDBPID([]localDBProcCandidate{
		{pid: 1700, path: "", alive: false},
		{pid: 2400, path: `C:\Program Files\Microsoft SQL Server\MSSQL16.SQLEXPRESS\MSSQL\Binn\sqlservr.exe`, alive: true},
		{pid: 18420, path: `C:\Program Files\Microsoft SQL Server\170\LocalDB\Binn\sqlservr.exe`, alive: true},
	})
	if got != 18420 {
		t.Fatalf("expected LocalDB PID 18420, got %d", got)
	}

	gotFallback := selectBestLocalDBPID([]localDBProcCandidate{
		{pid: 1700, path: "", alive: false},
		{pid: 9100, path: "", alive: true},
	})
	if gotFallback != 9100 {
		t.Fatalf("expected alive fallback PID 9100, got %d", gotFallback)
	}
}

func TestFormatMSSQLErrorCannotOpenDB(t *testing.T) {
	raw := `Sqlcmd: Error: Microsoft ODBC Driver 17 for SQL Server : Login failed for user 'IKYOJIN\ADMIN'.. Sqlcmd: Error: Microsoft ODBC Driver 17 for SQL Server : Cannot open database "db70628" requested by the login. The login failed..`
	localMsg := formatMSSQLError(raw, nil, "db70628", EngineSettings{})
	if !strings.Contains(localMsg, `Cannot open local MS SQL database "db70628"`) {
		t.Fatalf("expected clear local MS SQL explanation, got: %s", localMsg)
	}
	remoteMsg := formatMSSQLError(raw, nil, "db70628", EngineSettings{Host: "db70628.public.databaseasp.net"})
	if !strings.Contains(remoteMsg, `Cannot open remote MS SQL database "db70628"`) {
		t.Fatalf("expected clear remote MS SQL explanation, got: %s", remoteMsg)
	}
}

func TestParseMSSQLOutputAndTargetCache(t *testing.T) {
	raw := "id\tname\tnote\r\n--\t----\t----\r\n1\tAlice\tNULL\r\n2\tBob\tActive\r\n3\tCara\tPending\r\n"
	res := parseMSSQLOutput([]byte(raw), 2, 12.5)
	if !res.IsQuery || !res.Truncated || len(res.Rows) != 2 || len(res.Columns) != 3 {
		t.Fatalf("unexpected parsed result: %+v", res)
	}
	if res.Rows[0][2] != nil || res.Rows[1][1] == nil || *res.Rows[1][1] != "Bob" {
		t.Fatalf("unexpected row values: %+v", res.Rows)
	}

	nonQuery := parseMSSQLOutput([]byte("(5 rows affected)\r\n"), 100, 3.2)
	if nonQuery.IsQuery || nonQuery.Affected != 5 {
		t.Fatalf("unexpected non-query parsed result: %+v", nonQuery)
	}

	mssqlTargetMu.Lock()
	mssqlTargetCached = `\\.\pipe\TEST`
	mssqlTargetChecked = time.Now()
	mssqlTargetMu.Unlock()
	if got := mssqlServerTarget(EngineSettings{}); got != `\\.\pipe\TEST` {
		t.Fatalf("expected cached target, got %q", got)
	}
	InvalidateMSSQLTargetCache()
	mssqlTargetMu.Lock()
	cleared := mssqlTargetCached == "" && mssqlTargetChecked.IsZero()
	mssqlTargetMu.Unlock()
	if !cleared {
		t.Fatal("expected InvalidateMSSQLTargetCache to clear cache")
	}
}

func TestRepairSystemFixesAllComponents(t *testing.T) {
	root := t.TempDir()
	p := NewPaths(root)
	if err := p.Ensure(); err != nil {
		t.Fatal(err)
	}

	// Corrupt settings.json, create leftover temp files, bad mailpit messages.json, and partial redis temp rdb
	if err := os.WriteFile(filepath.Join(root, "settings.json"), []byte("{not-json"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(p.Tmp, "dariusdb-mssql-123.sql"), []byte("SELECT 1;"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(p.Backups, "bad.zip.tmp"), []byte("partial"), 0o644); err != nil {
		t.Fatal(err)
	}
	_ = os.MkdirAll(filepath.Join(p.Data, "mailpit"), 0o755)
	if err := os.WriteFile(filepath.Join(p.Data, "mailpit", "messages.json"), []byte("{bad"), 0o644); err != nil {
		t.Fatal(err)
	}
	_ = os.MkdirAll(filepath.Join(p.Data, "redis"), 0o755)
	if err := os.WriteFile(filepath.Join(p.Data, "redis", "temp-99.rdb"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	// Fake installed MariaDB with broken config and incomplete data dir
	mariaDir := filepath.Join(p.EngineBin("mariadb"), "mariadb-11.4.0")
	_ = os.MkdirAll(filepath.Join(mariaDir, "bin"), 0o755)
	_ = os.WriteFile(filepath.Join(mariaDir, "bin", "mariadbd.exe"), []byte("stub"), 0o644)
	_ = os.MkdirAll(p.EngineEtc("mariadb"), 0o755)
	_ = os.WriteFile(filepath.Join(p.EngineEtc("mariadb"), "my.ini"), []byte("corrupt"), 0o644)
	partialData := filepath.Join(p.Data, "mariadb-11.4")
	_ = os.MkdirAll(partialData, 0o755)
	_ = os.WriteFile(filepath.Join(partialData, "partial.ibd"), []byte("partial"), 0o644)

	st := LoadSettings(root)
	mgr := NewManagerWithOptions(p, st, nil, true)
	// Force wedged state and port conflict on redis
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	busyPort := ln.Addr().(*net.TCPAddr).Port
	redisDir := filepath.Join(p.EngineBin("redis"), "redis-8.0.0")
	_ = os.MkdirAll(redisDir, 0o755)
	_ = os.WriteFile(filepath.Join(redisDir, "redis-server.exe"), []byte("stub"), 0o644)
	_ = st.Update(func(s *Settings) {
		s.Engines["redis"].Port = busyPort
		s.Engines["redis"].Version = "redis-8.0.0"
		s.Schedules = []BackupSyncSchedule{{Name: "Nightly", LocalEngine: "mssql", LocalDB: "hris", SyncTime: "bad", RetentionDays: 0}}
	})
	mgr.set("redis", func(s *service) { s.state = StateStarting; s.pid = 999999 })

	rep, err := mgr.RepairWithOptions(RepairOptions{
		Target:     "all",
		FixPorts:   true,
		CleanCache: true,
		DeepSystem: true,
	})
	if err != nil {
		t.Fatalf("RepairWithOptions failed: %v", err)
	}
	if rep.FixedCount < 6 {
		t.Fatalf("expected multiple system issues fixed, got %d items=%+v", rep.FixedCount, rep.Items)
	}
	if fileExists(filepath.Join(p.Tmp, "dariusdb-mssql-123.sql")) || fileExists(filepath.Join(p.Backups, "bad.zip.tmp")) {
		t.Fatal("expected temp files to be cleaned")
	}
	if b, err := os.ReadFile(filepath.Join(p.Data, "mailpit", "messages.json")); err != nil || strings.TrimSpace(string(b)) != "[]" {
		t.Fatalf("expected repaired mailpit messages.json, got %q err=%v", string(b), err)
	}
	if fileExists(filepath.Join(p.Data, "redis", "temp-99.rdb")) {
		t.Fatal("expected stale redis temp RDB removed")
	}
	if b, err := os.ReadFile(filepath.Join(p.EngineEtc("mariadb"), "my.ini")); err != nil || !strings.Contains(string(b), "[mysqld]") {
		t.Fatalf("expected repaired mariadb my.ini, got %q err=%v", string(b), err)
	}
	if st.Engine("redis").Port == busyPort {
		t.Fatalf("expected busy redis port %d to be reassigned", busyPort)
	}
	if gotSched := st.Get().Schedules; len(gotSched) != 1 || gotSched[0].SyncTime != "02:00" || gotSched[0].RetentionDays != 7 {
		t.Fatalf("expected normalized schedule, got %+v", gotSched)
	}
}

func TestCloudConnectionWithProgressEmitsStages(t *testing.T) {
	var events []CloudSyncProgress
	_, err := TestCloudConnectionWithProgress(CloudConnection{Engine: "mysql"}, func(p CloudSyncProgress) {
		events = append(events, p)
	})
	if err == nil {
		t.Fatal("expected error for missing host")
	}
	if len(events) < 2 {
		t.Fatalf("expected >= 2 progress events on validation failure, got %+v", events)
	}
	if events[0].Percent != 10 || events[len(events)-1].State != "error" || events[len(events)-1].Percent != 100 {
		t.Fatalf("unexpected progress sequence on missing host: %+v", events)
	}

	// Pick a guaranteed closed ephemeral port so TCP reachability check fails deterministically with progress stages.
	ln, lerr := net.Listen("tcp", "127.0.0.1:0")
	if lerr != nil {
		t.Fatal(lerr)
	}
	closedPort := ln.Addr().(*net.TCPAddr).Port
	ln.Close()

	events = nil
	res, cerr := TestCloudConnectionWithProgress(CloudConnection{
		Engine: "mysql",
		Host:   "127.0.0.1",
		Port:   closedPort,
	}, func(p CloudSyncProgress) {
		events = append(events, p)
	})
	if cerr == nil || res == nil || res.OK {
		t.Fatalf("expected connection failure on closed port %d, got res=%+v err=%v", closedPort, res, cerr)
	}
	if len(events) < 3 {
		t.Fatalf("expected validation, tcp probe, and error progress events, got %+v", events)
	}
	if events[1].Percent != 25 || events[len(events)-1].State != "error" || events[len(events)-1].Percent != 100 {
		t.Fatalf("unexpected progress sequence on closed port: %+v", events)
	}
}

func TestCloudConnectionCpanelPortCheck(t *testing.T) {
	res, err := TestCloudConnection(CloudConnection{
		Engine: "mysql",
		Host:   "64.251.21.13",
		Port:   2083,
	})
	if err == nil || res == nil || res.OK {
		t.Fatal("expected error when port 2083 is used for mysql")
	}
	if !strings.Contains(err.Error(), "cPanel web login") || !strings.Contains(err.Error(), "3306") {
		t.Fatalf("unexpected error message for port 2083: %v", err)
	}

	parsed, perr := ParseCloudURI("mysql://testuser:pass%7B123@64.251.21.13:2083/test_db")
	if perr != nil {
		t.Fatalf("ParseCloudURI failed: %v", perr)
	}
	if parsed.Port != 3306 || parsed.Password != "pass{123" || parsed.PanelURL != "https://64.251.21.13:2083" {
		t.Fatalf("unexpected ParseCloudURI cPanel normalization: %+v", parsed)
	}

	// Unescaped '@' and '#' and literal '+' inside password
	parsedAt, perr2 := ParseCloudURI("mysql://surelifenet12:i@NDU+ob#WU@fiNq8J@64.251.21.13:3306/surelifenet12_surelifetest")
	if perr2 != nil {
		t.Fatalf("ParseCloudURI with '@' and '#' failed: %v", perr2)
	}
	if parsedAt.User != "surelifenet12" || parsedAt.Password != "i@NDU+ob#WU@fiNq8J" || parsedAt.Host != "64.251.21.13" || parsedAt.Port != 3306 || parsedAt.Database != "surelifenet12_surelifetest" {
		t.Fatalf("unexpected ParseCloudURI '@' result: %+v", parsedAt)
	}
}

func TestPostgresPrepareAndRepairRestoresMissingConf(t *testing.T) {
	root := t.TempDir()
	p := NewPaths(root)
	if err := p.Ensure(); err != nil {
		t.Fatal(err)
	}

	pgBin := filepath.Join(p.EngineBin("postgresql"), "postgresql-18.7", "bin")
	pgShare := filepath.Join(p.EngineBin("postgresql"), "postgresql-18.7", "share")
	if err := os.MkdirAll(pgBin, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(pgShare, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(pgBin, "pg_ctl.exe"), []byte("stub"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(pgBin, "postgres.exe"), []byte("stub"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(pgShare, "postgresql.conf.sample"), []byte("# sample\nlisten_addresses = 'localhost'\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	pgData := filepath.Join(p.Data, "postgresql-18")
	if err := os.MkdirAll(pgData, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(pgData, "PG_VERSION"), []byte("18\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	// Simulate Repair having renamed postgresql.conf -> .bak or deleting postgresql.conf
	if err := os.WriteFile(filepath.Join(pgData, "postgresql.conf.bak"), []byte("# old\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	st := LoadSettings(root)
	_ = st.Update(func(s *Settings) {
		s.Engines["postgresql"].Version = "postgresql-18.7"
	})
	mgr := NewManagerWithOptions(p, st, nil, true)

	// Repair without ResetConfigs must restore missing postgresql.conf from sample
	if _, err := mgr.RepairWithOptions(RepairOptions{Target: "postgresql", ResetConfigs: false}); err != nil {
		t.Fatalf("RepairWithOptions failed: %v", err)
	}
	confPath := filepath.Join(pgData, "postgresql.conf")
	if b, err := os.ReadFile(confPath); err != nil || !strings.Contains(string(b), "listen_addresses") {
		t.Fatalf("expected restored postgresql.conf, got %q err=%v", string(b), err)
	}

	// Custom setting must NOT be wiped when ResetConfigs is false
	custom := "# custom\nmax_connections = 250\n"
	if err := os.WriteFile(confPath, []byte(custom), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := mgr.RepairWithOptions(RepairOptions{Target: "postgresql", ResetConfigs: false}); err != nil {
		t.Fatalf("second RepairWithOptions failed: %v", err)
	}
	if b, err := os.ReadFile(confPath); err != nil || string(b) != custom {
		t.Fatalf("expected custom postgresql.conf preserved when ResetConfigs=false, got %q err=%v", string(b), err)
	}

	// ResetConfigs=true must back up postgresql.conf AND regenerate a working one
	if _, err := mgr.RepairWithOptions(RepairOptions{Target: "postgresql", ResetConfigs: true}); err != nil {
		t.Fatalf("RepairWithOptions(ResetConfigs=true) failed: %v", err)
	}
	if b, err := os.ReadFile(confPath); err != nil || !strings.Contains(string(b), "listen_addresses") {
		t.Fatalf("expected regenerated postgresql.conf after ResetConfigs=true, got %q err=%v", string(b), err)
	}
}

func TestRunCmdCaptureCancelsPromptly(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 250*time.Millisecond)
	defer cancel()

	// Use cmd /c ping as a slow child that would otherwise block for 10+ seconds
	cmd := exec.Command("cmd", "/c", "echo starting && ping -n 20 127.0.0.1 >nul")
	var startedPID int
	start := time.Now()
	out, err := runCmdCapture(ctx, cmd, func(c *exec.Cmd) {
		if c.Process != nil {
			startedPID = c.Process.Pid
		}
	})
	elapsed := time.Since(start)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("expected context.DeadlineExceeded, got %v (out=%q)", err, string(out))
	}
	if startedPID == 0 {
		t.Fatal("expected onStarted callback to receive running cmd PID")
	}
	if elapsed > 3*time.Second {
		t.Fatalf("expected cancelled runCmdCapture to abort promptly, took %v", elapsed)
	}
	if !strings.Contains(string(out), "starting") {
		t.Fatalf("expected partial output captured before cancellation, got %q", string(out))
	}
}

func TestGetDatabaseDiagramGuards(t *testing.T) {
	if _, err := GetDatabaseDiagram("mysql", EngineSettings{}, ""); err == nil {
		t.Fatal("expected error when dbName is empty")
	}
	if _, err := GetDatabaseDiagram("redis", EngineSettings{}, "db0"); err == nil {
		t.Fatal("expected error for unsupported non-SQL engine redis")
	}
}

func TestFormatAutoPullLocalDBName(t *testing.T) {
	ts := time.Date(2026, 11, 22, 13, 21, 36, 0, time.Local)

	cases := []struct {
		base, fmt, want string
	}{
		{"alegria", "date_day_year_ts", "alegria_2026_11_22_Sun_132136"},
		{"alegria", "month_day_year_ts", "alegria_Nov_22_2026_Sun_132136"},
		{"alegria", "date_year_ts", "alegria_2026_11_22_132136"},
		{"alegria", "date_day_year", "alegria_2026_11_22_Sun"},
		{"alegria", "", "alegria_2026_11_22_Sun_132136"},
		{"  Alegria DB!  ", "date_day_year_ts", "Alegria_DB_2026_11_22_Sun_132136"},
		{"2026sales", "date_day_year", "db_2026sales_2026_11_22_Sun"},
		{"", "date_day_year", "pulled_db_2026_11_22_Sun"},
	}
	for _, tc := range cases {
		got := FormatAutoPullLocalDBName(tc.base, tc.fmt, ts)
		if got != tc.want {
			t.Errorf("FormatAutoPullLocalDBName(%q, %q) = %q, want %q", tc.base, tc.fmt, got, tc.want)
		}
		if !nameRe.MatchString(got) {
			t.Errorf("generated DB name %q does not satisfy nameRe", got)
		}
	}

	// Long cloud DB name must be safely truncated to <= 63 chars while preserving full suffix
	longBase := strings.Repeat("a", 60)
	gotLong := FormatAutoPullLocalDBName(longBase, "date_day_year_ts", ts)
	if len(gotLong) > 63 || !strings.HasSuffix(gotLong, "_2026_11_22_Sun_132136") || !nameRe.MatchString(gotLong) {
		t.Fatalf("unexpected truncated DB name: %q (len=%d)", gotLong, len(gotLong))
	}

	norm := (BackupSyncSchedule{AutoNameLocalDB: true, AutoNameFormat: "unknown"}).Normalized()
	if !norm.AutoNameLocalDB || norm.AutoNameFormat != "date_day_year_ts" {
		t.Fatalf("expected normalized AutoNameFormat=date_day_year_ts, got %+v", norm)
	}
}

func TestMapProgressRangeAndScheduleRunProgress(t *testing.T) {
	if got := mapProgressRange(0, 54, 95); got != 54 {
		t.Fatalf("expected 54, got %d", got)
	}
	if got := mapProgressRange(50, 54, 95); got != 74 {
		t.Fatalf("expected 74, got %d", got)
	}
	if got := mapProgressRange(100, 54, 95); got != 95 {
		t.Fatalf("expected 95, got %d", got)
	}
	if got := mapProgressRange(-10, 4, 52); got != 4 {
		t.Fatalf("expected clamp low 4, got %d", got)
	}
	if got := mapProgressRange(150, 4, 52); got != 52 {
		t.Fatalf("expected clamp high 52, got %d", got)
	}

	root := t.TempDir()
	p := NewPaths(root)
	_ = p.Ensure()
	st := LoadSettings(root)
	_ = st.Update(func(s *Settings) {
		s.Schedules = []BackupSyncSchedule{{
			ID:         "sched-prog",
			Name:       "NoOp Schedule",
			Enabled:    true,
			AutoSync:   false,
			AutoBackup: false,
		}}
	})
	sc := NewScheduler(p, st, nil, nil, nil)
	var events []ScheduleRunProgress
	sc.SetScheduleProgressCallback(func(ev ScheduleRunProgress) {
		events = append(events, ev)
	})
	if _, err := sc.RunScheduleNow("sched-prog", "both"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(events) < 2 || events[0].Percent != 2 || events[len(events)-1].Percent != 100 || events[len(events)-1].State != "done" {
		t.Fatalf("unexpected progress events: %+v", events)
	}
}

func TestEnsureWwwIndexUpgradesToV5WithLogoAndPreservesCustom(t *testing.T) {
	wwwDir := t.TempDir()
	indexPath := filepath.Join(wwwDir, "index.html")

	// 1. Fresh www/ gets v5 landing page with inline SVG favicon, Darius DB logo, and v0.1.6 title
	ensureWwwIndex(wwwDir)
	b, err := os.ReadFile(indexPath)
	if err != nil {
		t.Fatalf("read generated index.html: %v", err)
	}
	s := string(b)
	if !strings.Contains(s, "darius-www-v5") || !strings.Contains(s, "Lite Version 0.1.6") || !strings.Contains(s, `rel="icon"`) || !strings.Contains(s, "data:image/svg+xml") || !strings.Contains(s, "dariusLogoGrad") {
		t.Fatalf("expected v5 index.html with 0.1.6 title, favicon, and brand SVG logo")
	}

	// 2. Older Darius DB v3/v4 page automatically upgrades to v5
	v4Stub := "<!DOCTYPE html>\n<!-- darius-www-v4 -->\n<html><head><title>Darius DB (Lite Version 0.1.4)</title></head><body></body></html>"
	if err := os.WriteFile(indexPath, []byte(v4Stub), 0o644); err != nil {
		t.Fatal(err)
	}
	ensureWwwIndex(wwwDir)
	b, err = os.ReadFile(indexPath)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(b), "darius-www-v5") || !strings.Contains(string(b), "Lite Version 0.1.6") || !strings.Contains(string(b), `rel="icon"`) {
		t.Fatalf("expected older Darius DB landing page to upgrade to v5 with favicon")
	}

	// 3. Custom user index.html must never be overwritten
	custom := "<!DOCTYPE html><html><head><title>My Custom App</title></head><body><h1>Hello</h1></body></html>"
	if err := os.WriteFile(indexPath, []byte(custom), 0o644); err != nil {
		t.Fatal(err)
	}
	ensureWwwIndex(wwwDir)
	b, err = os.ReadFile(indexPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(b) != custom {
		t.Fatalf("ensureWwwIndex overwrote user custom index.html")
	}
}

func TestMergeUpdaterCommitChangelogDeduplicatesSHA(t *testing.T) {
	existing := []string{
		"[2d1b762] Add 0-100% progress loading modals (2026-11-23)",
		"[311a836] Rebuild installers (2026-11-23)",
	}
	commits := []GitHubCommitInfo{
		{SHA: "abc1234", Message: "feat: brand new feature", Date: "2026-11-23 10:30"},
		{SHA: "def5678", Message: "chore: auto-update version.json from commits [skip ci]", Date: "2026-11-23 10:29"},
		{SHA: "2d1b762", Message: "feat: add 0-100% progress loading modals", Date: "2026-11-23 09:52"},
	}
	got := mergeUpdaterCommitChangelog(existing, commits)
	if len(got) != 3 {
		t.Fatalf("expected 3 merged changelog entries (1 new + 2 existing, skipping dup SHA and [skip ci]), got %d: %#v", len(got), got)
	}
	if !strings.HasPrefix(got[0], "[abc1234]") {
		t.Fatalf("expected new commit prepended first, got %q", got[0])
	}
}

func TestUpdaterRepoEndpointsAndFallbackOrder(t *testing.T) {
	repos := updaterCandidateRepos()
	if len(repos) != 2 {
		t.Fatalf("expected 2 candidate repos, got %d", len(repos))
	}
	if repos[0].repo != "Darius-DB-Lite-Releases" || repos[1].repo != "Darius-DB-Lite-Version" {
		t.Fatalf("unexpected repo priority order: %+v", repos)
	}
	if !strings.Contains(repos[0].versionJSONURL, "/Darius-DB-Lite-Releases/main/version.json") ||
		!strings.Contains(repos[0].releasesURL, "/Darius-DB-Lite-Releases/releases/latest") ||
		!strings.Contains(repos[0].rawInstallerBase, "/Darius-DB-Lite-Releases/main/installer") {
		t.Fatalf("unexpected primary releases repo endpoints: %+v", repos[0])
	}
}

func TestManualRunDoesNotBlockScheduledSyncAndSyncLogsTrace(t *testing.T) {
	root := t.TempDir()
	p := NewPaths(root)
	if err := p.Ensure(); err != nil {
		t.Fatal(err)
	}
	st := LoadSettings(root)
	day := time.Date(2026, 11, 23, 9, 0, 0, 0, time.Local)
	_ = st.Update(func(s *Settings) {
		s.Schedules = []BackupSyncSchedule{{
			ID:          "sched-11am",
			Name:        "Daily 11 AM Pull",
			Enabled:     true,
			AutoSync:    true,
			SyncTime:    "11:00",
			AutoBackup:  false,
			LocalEngine: "mysql",
			LocalDB:     "app_db",
			CreatedAt:   day.Format("2006-01-02 15:04"),
		}}
	})
	sc := NewScheduler(p, st, nil, nil, nil)

	// Simulate manual Run Now at 10:10 AM (before 11:00 AM scheduled sync).
	// Even on error or success, manual run must NOT set LastSyncDate=today.
	run1010 := time.Date(2026, 11, 23, 10, 10, 0, 0, time.Local)
	_, _ = sc.executeLocked("sched-11am", true, false, run1010, "manual")
	cur := st.Get().Schedules[0]
	if cur.LastSyncDate != "" {
		t.Fatalf("manual Run Now must not set LastSyncDate, got %q", cur.LastSyncDate)
	}
	// At 11:00 AM, the scheduled sync must still be due!
	at1100 := time.Date(2026, 11, 23, 11, 0, 5, 0, time.Local)
	if !isScheduleDue(at1100, cur.SyncTime, cur.LastSyncDate, cur.CreatedAt) {
		t.Fatal("expected 11:00 AM scheduled sync to remain due after 10:10 AM manual Run Now")
	}

	// ResetScheduleDateForEditedTime: changing time at 10:45 to upcoming 11:00 clears LastSyncDate;
	// changing time at 11:05 to past 10:00 marks today so it does not fire retroactively.
	at1045 := time.Date(2026, 11, 23, 10, 45, 0, 0, time.Local)
	if got := ResetScheduleDateForEditedTime(at1045, "23:00", "11:00", "2026-11-23", true, true); got != "" {
		t.Fatalf("expected cleared LastSyncDate for upcoming 11:00 edit at 10:45, got %q", got)
	}
	at1105 := time.Date(2026, 11, 23, 11, 5, 0, 0, time.Local)
	if got := ResetScheduleDateForEditedTime(at1105, "23:00", "10:00", "", true, true); got != "2026-11-23" {
		t.Fatalf("expected today marker for past 10:00 edit at 11:05, got %q", got)
	}

	// Structured sync trace logs must be persisted newest-first and filterable by scheduleID
	AppendSyncLog(p, SyncLogEntry{
		ScheduleID: "sched-11am",
		Name:       "Daily 11 AM Pull",
		Trigger:    "scheduled",
		Action:     "sync",
		Direction:  "pull",
		Source:     "Cloud:prod/canyoneeringdb",
		Target:     "Local:MYSQL/canyoneeringdb",
		Status:     "ok",
		DurationMs: 1420,
		Detail:     "Synced 30 table(s)",
	})
	AppendSyncLog(p, SyncLogEntry{
		ScheduleID: "other-sched",
		Name:       "Other",
		Trigger:    "manual",
		Action:     "backup",
		Status:     "ok",
		Detail:     "backup.zip",
	})

	allLogs, err := ReadSyncLogs(p, "", 50)
	if err != nil || len(allLogs) < 3 {
		t.Fatalf("expected >= 3 persisted sync trace logs, got %d err=%v", len(allLogs), err)
	}
	if allLogs[0].ScheduleID != "other-sched" {
		t.Fatalf("expected newest entry first, got %+v", allLogs[0])
	}
	filtered, err := ReadSyncLogs(p, "sched-11am", 50)
	if err != nil || len(filtered) != 2 {
		t.Fatalf("expected 2 filtered logs for sched-11am, got %d err=%v", len(filtered), err)
	}
	if filtered[0].Trigger != "scheduled" || filtered[0].Status != "ok" {
		t.Fatalf("unexpected top filtered log: %+v", filtered[0])
	}
	if !fileExists(filepath.Join(p.Logs, "scheduler.log")) {
		t.Fatal("expected human-readable logs/scheduler.log to be written")
	}
	if err := ClearSyncLogs(p); err != nil {
		t.Fatalf("ClearSyncLogs: %v", err)
	}
	afterClear, _ := ReadSyncLogs(p, "", 50)
	if len(afterClear) != 0 {
		t.Fatalf("expected 0 logs after clear, got %d", len(afterClear))
	}
}

func TestFormatAutoBackupZipNameAndParseSnapshotTime(t *testing.T) {
	ts := time.Date(2026, 11, 23, 2, 0, 15, 0, time.Local)
	cases := []struct {
		base, suffix, fmt, want string
		wantDayOnly             bool
	}{
		{"alegria", "", "date_day_year_ts", "alegria_2026_11_23_Mon_020015.zip", false},
		{"alegria", "_cloud", "month_day_year_ts", "alegria_cloud_Nov_23_2026_Mon_020015.zip", false},
		{"alegria", "", "date_year_ts", "alegria_2026_11_23_020015.zip", false},
		{"alegria", "", "date_day_year", "alegria_2026_11_23_Mon.zip", true},
	}
	fallback := time.Date(2020, 1, 1, 0, 0, 0, 0, time.Local)
	for _, tc := range cases {
		got := FormatAutoBackupZipName(tc.base, tc.suffix, tc.fmt, ts)
		if got != tc.want {
			t.Errorf("FormatAutoBackupZipName(%q,%q,%q) = %q, want %q", tc.base, tc.suffix, tc.fmt, got, tc.want)
		}
		parsed := parseSnapshotTime(got, fallback)
		if tc.wantDayOnly {
			if parsed.Format("2006-01-02") != "2026-11-23" {
				t.Errorf("parseSnapshotTime(%q) = %v, want 2026-11-23", got, parsed)
			}
		} else if !parsed.Equal(ts) {
			t.Errorf("parseSnapshotTime(%q) = %v, want %v", got, parsed, ts)
		}
	}

	// Ensure 7-day PurgeOldSnapshots purges aged auto-named .zip snapshots accurately
	dir := t.TempDir()
	oldAuto := filepath.Join(dir, FormatAutoBackupZipName("alegria", "", "date_day_year_ts", ts.AddDate(0, 0, -8)))
	oldMon := filepath.Join(dir, FormatAutoBackupZipName("alegria", "", "month_day_year_ts", ts.AddDate(0, 0, -9)))
	freshAuto := filepath.Join(dir, FormatAutoBackupZipName("alegria", "", "date_day_year_ts", ts.AddDate(0, 0, -2)))
	for _, f := range []string{oldAuto, oldMon, freshAuto} {
		if err := os.WriteFile(f, []byte("zip"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	n, _, err := PurgeOldSnapshots(dir, 7, ts)
	if err != nil || n != 2 {
		t.Fatalf("expected 2 old auto-named snapshots purged, got n=%d err=%v", n, err)
	}
	if !fileExists(freshAuto) || fileExists(oldAuto) || fileExists(oldMon) {
		t.Fatal("unexpected snapshot files remaining after auto-named purge")
	}
}

func TestSchedulePullFolderNormalizationAndPaths(t *testing.T) {
	p := NewPaths(t.TempDir())
	s1 := (BackupSyncSchedule{
		Name:          "Alegria Daily",
		SyncDirection: "pull_folder",
		AutoSync:      true,
	}).Normalized()
	if s1.SyncDirection != "pull_folder" {
		t.Fatalf("expected SyncDirection=pull_folder preserved, got %q", s1.SyncDirection)
	}
	defDir := ScheduleSyncDir(p, s1)
	if !strings.HasSuffix(filepath.ToSlash(defDir), "/Alegria_Daily/syncs") {
		t.Fatalf("unexpected default ScheduleSyncDir: %q", defDir)
	}
	customDir := filepath.Join(p.Root, "my-exports")
	s2 := (BackupSyncSchedule{
		Name:          "Alegria Daily",
		SyncDirection: "pull_folder",
		SyncFolder:    "  " + customDir + "  ",
	}).Normalized()
	if got := ScheduleSyncDir(p, s2); got != customDir {
		t.Fatalf("expected custom ScheduleSyncDir %q, got %q", customDir, got)
	}
}

func TestListSnapshotsIncludesPurgeDateDayMonthTimeGuide(t *testing.T) {
	p := NewPaths(t.TempDir())
	sched := (BackupSyncSchedule{
		ID:            "sched_purge_guide",
		Name:          "canyoneeringdb Daily",
		AutoBackup:    true,
		RetentionDays: 7,
	}).Normalized()
	dir := ScheduleBackupDir(p, sched)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	snapFile := filepath.Join(dir, "tsujkcilc_canyoneeringdb_2026_11_23_Mon_133108.zip")
	if err := os.WriteFile(snapFile, []byte("zip-bytes"), 0o644); err != nil {
		t.Fatal(err)
	}

	// Active snapshot 1 hour after creation
	nowActive := time.Date(2026, 11, 23, 14, 31, 8, 0, time.Local)
	snaps, err := ListSnapshots(p, sched, nowActive)
	if err != nil || len(snaps) != 1 {
		t.Fatalf("expected 1 snapshot, got len=%d err=%v", len(snaps), err)
	}
	got := snaps[0]
	if got.CreatedDisplay != "Mon, Nov 23, 2026 · 01:31:08 PM" {
		t.Errorf("CreatedDisplay = %q, want %q", got.CreatedDisplay, "Mon, Nov 23, 2026 · 01:31:08 PM")
	}
	if got.PurgeAt != "Mon, Nov 30, 2026 · 01:31:08 PM" {
		t.Errorf("PurgeAt = %q, want %q", got.PurgeAt, "Mon, Nov 30, 2026 · 01:31:08 PM")
	}
	if got.PurgeStamp != "2026_11_30_Mon_133108" {
		t.Errorf("PurgeStamp = %q, want %q", got.PurgeStamp, "2026_11_30_Mon_133108")
	}
	if got.Expired {
		t.Errorf("expected snapshot to be active on %v, got Expired=true", nowActive)
	}

	// Expired snapshot at exact 7-day purge moment
	nowExpired := time.Date(2026, 11, 30, 13, 31, 8, 0, time.Local)
	snapsExp, err := ListSnapshots(p, sched, nowExpired)
	if err != nil || len(snapsExp) != 1 {
		t.Fatalf("expected 1 snapshot, got len=%d err=%v", len(snapsExp), err)
	}
	if !snapsExp[0].Expired || snapsExp[0].ExpiresIn != "Due for purge" {
		t.Errorf("expected expired snapshot on %v, got Expired=%v ExpiresIn=%q", nowExpired, snapsExp[0].Expired, snapsExp[0].ExpiresIn)
	}
}

func TestIsAutomatedCommitFiltering(t *testing.T) {
	if !isAutomatedCommit("github-actions[bot]", "chore: auto-update version.json from commits [skip ci]") {
		t.Fatal("expected github-actions[bot] commit to be filtered")
	}
	if !isAutomatedCommit("dcquijano", "chore: auto-update version.json from commits") {
		t.Fatal("expected auto-update version.json commit to be filtered")
	}
	if !isAutomatedCommit("dcquijano", "docs: tweak readme [skip ci]") {
		t.Fatal("expected [skip ci] commit to be filtered")
	}
	if isAutomatedCommit("dcquijano", "feat: add Purge Date/Day/Month/Time guide") {
		t.Fatal("expected normal feature commit NOT to be filtered")
	}
}

func TestPurgeAndListGeneratedSnapshotFoldersSafety(t *testing.T) {
	p := NewPaths(t.TempDir())
	customBkpDir := filepath.Join(p.Root, "custom_purge_folder")
	customSyncDir := filepath.Join(p.Root, "custom_sync_folder")
	sched := (BackupSyncSchedule{
		ID:            "sched_folder_purge",
		Name:          "canyoneeringdb Daily",
		AutoSync:      true,
		SyncDirection: "pull_folder",
		SyncFolder:    customSyncDir,
		AutoBackup:    true,
		BackupSource:  "cloud_folder",
		BackupDir:     customBkpDir,
		RetentionDays: 7,
	}).Normalized()

	if sched.BackupSource != "cloud_folder" {
		t.Fatalf("expected BackupSource=cloud_folder preserved, got %q", sched.BackupSource)
	}
	if dirs := SchedulePurgeDirs(p, sched); len(dirs) != 2 {
		t.Fatalf("expected 2 purge dirs (BackupDir + SyncFolder), got %v", dirs)
	}

	now := time.Date(2026, 11, 30, 12, 30, 0, 0, time.Local)
	// 1. Expired generated folder in customBkpDir (created 8 days ago) -> MUST purge
	expiredBkp := filepath.Join(customBkpDir, "tsujkcilc_canyoneeringdb_2026_11_22_Sun_123000")
	// 2. Fresh generated folder in customBkpDir (created 2 days ago) -> MUST keep & list
	freshBkp := filepath.Join(customBkpDir, "tsujkcilc_canyoneeringdb_2026_11_28_Sat_123000")
	// 3. Expired generated folder in customSyncDir (created 9 days ago) -> MUST purge via PurgeScheduleAllDirs
	expiredSync := filepath.Join(customSyncDir, "tsujkcilc_canyoneeringdb_2026_11_21_Sat_123000")
	// 4. Safety test: old dated folder containing an unrelated user file -> MUST NOT delete
	unsafeDated := filepath.Join(customBkpDir, "tsujkcilc_canyoneeringdb_2026_11_10_Tue_123000")
	// 5. Safety test: unrelated user folder -> MUST NOT delete
	userFolder := filepath.Join(customBkpDir, "my_important_docs")

	for _, d := range []string{expiredBkp, freshBkp, expiredSync, unsafeDated, userFolder} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(expiredBkp, "tsujkcilc_canyoneeringdb_2026_11_22_Sun_123000.zip"), []byte("zip1"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(freshBkp, "tsujkcilc_canyoneeringdb_2026_11_28_Sat_123000.zip"), []byte("zip2"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(expiredSync, "tsujkcilc_canyoneeringdb_2026_11_21_Sat_123000.zip"), []byte("zip3"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(unsafeDated, "tsujkcilc_canyoneeringdb_2026_11_10_Tue_123000.zip"), []byte("zip4"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(unsafeDated, "keep-me.txt"), []byte("user note"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(userFolder, "report.pdf"), []byte("pdf"), 0o644); err != nil {
		t.Fatal(err)
	}

	// ListSnapshots should list the 3 valid generated folders (and ignore unsafeDated/userFolder)
	snaps, err := ListSnapshots(p, sched, now)
	if err != nil {
		t.Fatalf("ListSnapshots: %v", err)
	}
	if len(snaps) != 3 {
		t.Fatalf("expected 3 generated folders listed, got %d (%+v)", len(snaps), snaps)
	}
	if snaps[0].Name != "tsujkcilc_canyoneeringdb_2026_11_28_Sat_123000/" || snaps[0].PurgeStamp != "2026_12_05_Sat_123000" {
		t.Errorf("unexpected newest folder snapshot: %+v", snaps[0])
	}

	// PurgeScheduleAllDirs should purge ONLY expiredBkp and expiredSync
	n, purged, err := PurgeScheduleAllDirs(p, sched, now)
	if err != nil || n != 2 {
		t.Fatalf("expected 2 expired generated folders purged, got n=%d purged=%v err=%v", n, purged, err)
	}
	if dirExists(expiredBkp) || dirExists(expiredSync) {
		t.Fatal("expired generated folders should have been purged")
	}
	if !dirExists(freshBkp) || !dirExists(unsafeDated) || !dirExists(userFolder) {
		t.Fatal("fresh generated folder or user-protected folders must never be deleted")
	}
}

func TestCancelScheduleRunAndCancelledBackupContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	p := NewPaths(t.TempDir())
	outZip := filepath.Join(p.Backups, "should_not_exist.zip")
	err := ExportDatabaseFullZipWithContext(ctx, p, "mysql", EngineSettings{}, "demo", nil, true, true, outZip, nil)
	if !errors.Is(err, ErrSyncCancelled) {
		t.Fatalf("expected ErrSyncCancelled from pre-cancelled ctx, got %v", err)
	}
	if fileExists(outZip) {
		t.Fatal("expected cancelled backup not to leave output zip behind")
	}

	sc := NewScheduler(p, LoadSettings(p.Root), nil, nil, nil)
	called := false
	sc.cancelMu.Lock()
	sc.runCancels["sched_sync_1:sync"] = func() { called = true }
	sc.cancelMu.Unlock()
	if !sc.CancelScheduleRun("sched_sync_1") || !called {
		t.Fatal("expected CancelScheduleRun to invoke matching schedule cancel hook")
	}
}

func TestIsTransientConnErrClassification(t *testing.T) {
	if !isTransientConnErr(errors.New("invalid connection")) {
		t.Fatal("expected 'invalid connection' to be classified as transient")
	}
	if !isTransientConnErr(errors.New("query users: invalid connection")) {
		t.Fatal("expected wrapped 'invalid connection' to be classified as transient")
	}
	if !isTransientConnErr(errors.New("read tcp 192.168.1.2: wsarecv: connection reset by peer")) {
		t.Fatal("expected wsarecv reset error to be classified as transient")
	}
	if isTransientConnErr(ErrSyncCancelled) {
		t.Fatal("explicit cancellation must never be classified as transient")
	}
	if isTransientConnErr(context.Canceled) {
		t.Fatal("context.Canceled must never be classified as transient")
	}
	if isTransientConnErr(errors.New("Error 1045: Access denied for user")) {
		t.Fatal("auth error must not be classified as transient")
	}
}

func TestClearScheduleStatusFields(t *testing.T) {
	s := BackupSyncSchedule{
		ID:               "sched_1",
		LastSyncAt:       "2026-11-24 16:45",
		LastSyncDate:     "2026-11-24",
		LastSyncStatus:   "err [Manual]: SQL dump failed: invalid connection",
		LastBackupAt:     "2026-11-24 16:50",
		LastBackupDate:   "2026-11-24",
		LastBackupStatus: "err [Manual]: SQL dump failed: invalid connection",
	}

	if err := ClearScheduleStatusFields(&s, "sync"); err != nil {
		t.Fatalf("unexpected error clearing sync status: %v", err)
	}
	if s.LastSyncAt != "" || s.LastSyncStatus != "" {
		t.Fatalf("expected sync status cleared, got at=%q status=%q", s.LastSyncAt, s.LastSyncStatus)
	}
	if s.LastSyncDate != "2026-11-24" {
		t.Fatalf("expected LastSyncDate preserved, got %q", s.LastSyncDate)
	}
	if s.LastBackupAt == "" || s.LastBackupStatus == "" {
		t.Fatal("clearing sync status must preserve backup status")
	}

	if err := ClearScheduleStatusFields(&s, "backup"); err != nil {
		t.Fatalf("unexpected error clearing backup status: %v", err)
	}
	if s.LastBackupAt != "" || s.LastBackupStatus != "" {
		t.Fatalf("expected backup status cleared, got at=%q status=%q", s.LastBackupAt, s.LastBackupStatus)
	}
	if s.LastBackupDate != "2026-11-24" {
		t.Fatalf("expected LastBackupDate preserved, got %q", s.LastBackupDate)
	}

	if err := ClearScheduleStatusFields(&s, "invalid"); err == nil {
		t.Fatal("expected error for invalid clear mode")
	}
}

func TestCancelScheduleRunModeAndPromptContextAbort(t *testing.T) {
	p := &Paths{Root: t.TempDir()}
	sc := NewScheduler(p, LoadSettings(p.Root), nil, nil, nil)
	syncCancelled := false
	bkpCancelled := false
	sc.cancelMu.Lock()
	sc.runCancels["sched_x:sync"] = func() { syncCancelled = true }
	sc.runCancels["sched_x:backup"] = func() { bkpCancelled = true }
	sc.cancelMu.Unlock()

	if !sc.CancelScheduleRunMode("sched_x", "sync") {
		t.Fatal("expected CancelScheduleRunMode(sync) to report true")
	}
	if !syncCancelled || bkpCancelled {
		t.Fatalf("expected only sync cancelled, got sync=%v backup=%v", syncCancelled, bkpCancelled)
	}
	if !sc.CancelScheduleRunMode("sched_x", "backup") || !bkpCancelled {
		t.Fatal("expected CancelScheduleRunMode(backup) to cancel backup hook")
	}

	// Pre-cancelled context must abort ListTablesCtx & ExportDatabaseFullZipWithContext immediately.
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	start := time.Now()
	if _, err := ListTablesCtx(ctx, "mysql", EngineSettings{Host: "192.0.2.1", Port: 3306}, "demo"); !errors.Is(err, context.Canceled) {
		t.Fatalf("expected context.Canceled from ListTablesCtx, got %v", err)
	}
	if elapsed := time.Since(start); elapsed > 500*time.Millisecond {
		t.Fatalf("expected ListTablesCtx to abort immediately on cancelled ctx, took %v", elapsed)
	}
}

func TestMSSQLPreloginAndBannerDetection(t *testing.T) {
	// Realistic MySQL / MariaDB Initial Handshake greeting packet
	mysqlPkt := []byte{0x4a, 0x00, 0x00, 0x00, 0x0a}
	mysqlPkt = append(mysqlPkt, []byte("10.6.18-MariaDB\x00extra-payload-mysql_native_password")...)
	eng, ver := detectNonTDSBanner(mysqlPkt)
	if eng != "MySQL/MariaDB" || ver != "10.6.18-MariaDB" {
		t.Fatalf("expected MySQL/MariaDB 10.6.18-MariaDB, got eng=%q ver=%q", eng, ver)
	}

	httpPkt := []byte("HTTP/1.1 400 Bad Request\r\nContent-Type: text/html\r\n")
	if e, _ := detectNonTDSBanner(httpPkt); e != "HTTP/Web" {
		t.Fatalf("expected HTTP/Web, got %q", e)
	}

	// SSLMode mappings for remote sqlcmd auth args
	reqArgs := mssqlAuthArgs("db.example.com,1433", EngineSettings{Host: "db.example.com", User: "u", Password: "p", SSLMode: "require"})
	joinedReq := strings.Join(reqArgs, " ")
	if !strings.Contains(joinedReq, "-N") || !strings.Contains(joinedReq, "-C") {
		t.Fatalf("expected -N and -C for SSLMode=require, got %v", reqArgs)
	}
	disArgs := mssqlAuthArgs("db.example.com,1433", EngineSettings{Host: "db.example.com", User: "u", Password: "p", SSLMode: "disable"})
	joinedDis := strings.Join(disArgs, " ")
	if strings.Contains(joinedDis, "-N") || strings.Contains(joinedDis, "-C") {
		t.Fatalf("expected no TLS flags for SSLMode=disable, got %v", disArgs)
	}

	msg := formatMSSQLError("Sqlcmd: Error: Microsoft ODBC Driver 17 for SQL Server : Protocol error in TDS stream. Client unable to establish connection due to prelogin failure.", nil, "appdb", EngineSettings{Host: "64.251.1.2", Port: 3306})
	if !strings.Contains(msg, "3306") || !strings.Contains(msg, "MySQL") {
		t.Fatalf("expected port 3306 MySQL hint in prelogin error, got: %s", msg)
	}
}

func TestSyncManagedPHPIni(t *testing.T) {
	root := t.TempDir()
	p := NewPaths(root)

	php1 := filepath.Join(p.EngineBin("php"), "php-8.4.4")
	php2 := filepath.Join(p.EngineBin("php"), "php-8.3.10", "bin")
	extPhp := filepath.Join(root, "external-php84")
	for _, d := range []string{php1, php2, extPhp} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(d, "php.exe"), []byte("stub"), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	linked, err := SyncManagedPHPIni(p)
	if err != nil {
		t.Fatalf("SyncManagedPHPIni failed: %v", err)
	}
	if linked != 2 {
		t.Fatalf("expected 2 managed PHP installations linked, got %d", linked)
	}

	etcIni := filepath.Join(p.EngineEtc("php"), "php.ini")
	etcInfo, err := os.Stat(etcIni)
	if err != nil {
		t.Fatalf("expected etc php.ini to exist: %v", err)
	}

	for _, dir := range []string{php1, php2} {
		binIni := filepath.Join(dir, "php.ini")
		binInfo, err := os.Stat(binIni)
		if err != nil {
			t.Fatalf("expected %s to exist: %v", binIni, err)
		}
		if !os.SameFile(etcInfo, binInfo) {
			t.Fatalf("expected %s to share hardlink inode with %s", binIni, etcIni)
		}
	}

	// External non-DariusDB PHP directory must remain completely untouched
	if fileExists(filepath.Join(extPhp, "php.ini")) {
		t.Fatal("expected external PHP directory not to be modified")
	}

	// Modifying etc/php/php.ini immediately reflects through the hardlinked bin php.ini
	updated := defaultPhpIni + "\n; hardlink-sync-check\n"
	if err := os.WriteFile(etcIni, []byte(updated), 0o644); err != nil {
		t.Fatal(err)
	}
	if b, err := os.ReadFile(filepath.Join(php1, "php.ini")); err != nil || !strings.Contains(string(b), "; hardlink-sync-check") {
		t.Fatalf("expected hardlinked php.ini to reflect etc edit immediately, got err=%v", err)
	}

	// Second call is idempotent
	if linked2, err := SyncManagedPHPIni(p); err != nil || linked2 != 0 {
		t.Fatalf("expected second SyncManagedPHPIni to be no-op, got linked=%d err=%v", linked2, err)
	}

	// Repair restores a deleted bin/php/*/php.ini link
	_ = os.Remove(filepath.Join(php1, "php.ini"))
	st := LoadSettings(root)
	mgr := NewManagerWithOptions(p, st, nil, true)
	rep, err := mgr.RepairWithOptions(RepairOptions{Target: "php"})
	if err != nil {
		t.Fatalf("RepairWithOptions(php) failed: %v", err)
	}
	if !fileExists(filepath.Join(php1, "php.ini")) {
		t.Fatalf("expected Repair to restore hardlinked php.ini, report=%+v", rep)
	}
}

func TestLoadCatalogHealsStaleURLsAndPreservesCustom(t *testing.T) {
	root := t.TempDir()
	p := NewPaths(root)
	if err := p.Ensure(); err != nil {
		t.Fatal(err)
	}
	stale := `{
  "apache": [
    { "version": "2.4.66", "url": "https://www.apachelounge.com/download/VS17/binaries/httpd-2.4.66-260223-win64-VS17.zip", "note": "Apache 2.4 Win64 VS17" }
  ],
  "mariadb": [
    { "version": "13.0.2", "url": "https://archive.mariadb.org/mariadb-13.0.2/winx64-packages/mariadb-13.0.2-winx64.zip", "note": "Latest Stable" },
    { "version": "11.4.14", "url": "https://archive.mariadb.org/mariadb-11.4.14/winx64-packages/mariadb-11.4.14-winx64.zip", "note": "LTS" }
  ],
  "mysql": [
    { "version": "9.7.2", "url": "https://cdn.mysql.com/Downloads/MySQL-9.7/mysql-9.7.2-winx64.zip", "note": "Latest" },
    { "version": "9.9.9", "url": "https://mirror.example.com/mysql-9.9.9-winx64.zip", "note": "Custom" }
  ]
}`
	if err := os.WriteFile(filepath.Join(root, "catalog.json"), []byte(stale), 0o644); err != nil {
		t.Fatal(err)
	}

	cat := LoadCatalog(p)

	// Dead Apache 2.4.66 entry should be pruned and replaced with 2.4.71
	if len(cat["apache"]) == 0 || cat["apache"][0].Version != "2.4.71" {
		t.Fatalf("expected healed apache catalog starting with 2.4.71, got %+v", cat["apache"])
	}
	for _, e := range cat["apache"] {
		if e.Version == "2.4.66" {
			t.Fatalf("expected dead apache 2.4.66 entry to be pruned, got %+v", cat["apache"])
		}
	}

	// Dead MariaDB 13.0.2 should be pruned, 11.4.14 normalized to downloads.mariadb.org, 13.1.2 merged
	for _, e := range cat["mariadb"] {
		if e.Version == "13.0.2" {
			t.Fatalf("expected dead mariadb 13.0.2 to be pruned, got %+v", cat["mariadb"])
		}
		if e.Version == "11.4.14" && !strings.HasPrefix(e.URL, "https://downloads.mariadb.org/") {
			t.Fatalf("expected mariadb 11.4.14 URL healed to downloads.mariadb.org, got %s", e.URL)
		}
	}

	// MySQL 9.7.2 should heal to archives URL, Custom 9.9.9 preserved, and 26.10.0 / 9.7.4 merged
	found972, foundCustom := false, false
	for _, e := range cat["mysql"] {
		if e.Version == "9.7.2" {
			found972 = true
			if e.URL != "https://cdn.mysql.com/archives/mysql-9.7/mysql-9.7.2-winx64.zip" {
				t.Fatalf("expected mysql 9.7.2 healed to archives URL, got %s", e.URL)
			}
		}
		if e.Version == "9.9.9" {
			foundCustom = true
			if e.URL != "https://mirror.example.com/mysql-9.9.9-winx64.zip" {
				t.Fatalf("expected Custom mysql URL preserved, got %s", e.URL)
			}
		}
	}
	if !found972 || !foundCustom {
		t.Fatalf("expected both healed 9.7.2 and Custom 9.9.9 in mysql catalog: %+v", cat["mysql"])
	}
}

func TestBuildDownloadCandidatesAndDownloadOnceRejectsHTML(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // ensure apache candidate builder doesn't hit network

	mysqlCands := buildDownloadCandidates(ctx, "mysql", "9.7.2", "https://cdn.mysql.com/Downloads/MySQL-9.7/mysql-9.7.2-winx64.zip")
	joined := strings.Join(mysqlCands, "\n")
	if !strings.Contains(joined, "https://cdn.mysql.com/archives/mysql-9.7/mysql-9.7.2-winx64.zip") {
		t.Fatalf("expected mysql archive fallback candidate, got %v", mysqlCands)
	}
	if !strings.Contains(joined, "https://downloads.mysql.com/archives/get/p/23/file/mysql-9.7.2-winx64.zip") {
		t.Fatalf("expected downloads.mysql.com archive fallback candidate, got %v", mysqlCands)
	}

	phpCands := buildDownloadCandidates(ctx, "php", "8.4.28", "https://windows.php.net/downloads/releases/php-8.4.28-Win32-vs17-x64.zip")
	if len(phpCands) < 2 || !strings.Contains(phpCands[1], "/downloads/releases/archives/") {
		t.Fatalf("expected php archives fallback candidate, got %v", phpCands)
	}

	// Verify downloadOnce rejects HTML 200 pages and 404s cleanly
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/html":
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			_, _ = w.Write([]byte("<html>Not Found</html>"))
		case "/404":
			http.NotFound(w, r)
		default:
			w.Header().Set("Content-Type", "application/zip")
			_, _ = w.Write([]byte("PK\x03\x04zipdata"))
		}
	}))
	defer srv.Close()

	p := NewPaths(t.TempDir())
	_ = p.Ensure()
	dl := NewDownloader(p, func(Progress) {}, nil)

	if err := dl.downloadOnce(context.Background(), "mysql", "9.7.2", srv.URL+"/html", filepath.Join(p.Tmp, "a.zip")); err == nil || !strings.Contains(err.Error(), "HTML") {
		t.Fatalf("expected HTML rejection error, got %v", err)
	}
	if err := dl.downloadOnce(context.Background(), "mysql", "9.7.2", srv.URL+"/404", filepath.Join(p.Tmp, "b.zip")); err == nil || !strings.Contains(err.Error(), "404") {
		t.Fatalf("expected 404 error, got %v", err)
	}
	okDest := filepath.Join(p.Tmp, "ok.zip")
	if err := dl.downloadOnce(context.Background(), "mysql", "9.7.2", srv.URL+"/ok.zip", okDest); err != nil {
		t.Fatalf("expected valid zip download to succeed, got %v", err)
	}
	if !fileExists(okDest) {
		t.Fatalf("expected downloaded file at %s", okDest)
	}
}

func TestMailpitStartUsesDatabaseFlag(t *testing.T) {
	root := t.TempDir()
	p := NewPaths(root)
	if err := p.Ensure(); err != nil {
		t.Fatal(err)
	}
	mpDir := filepath.Join(p.EngineBin("mailpit"), "mailpit-1.34.0")
	if err := os.MkdirAll(mpDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(mpDir, "mailpit.exe"), []byte("stub"), 0o644); err != nil {
		t.Fatal(err)
	}
	eng := &mailpitEngine{}
	inst := Installation{Engine: "mailpit", Name: "mailpit-1.34.0", Version: "1.34.0", Dir: mpDir}
	spec, err := eng.Start(p, inst, EngineSettings{Port: 8025})
	if err != nil {
		t.Fatalf("mailpit Start failed: %v", err)
	}
	argsJoined := strings.Join(spec.Cmd.Args, " ")
	if strings.Contains(argsJoined, "--db-file") {
		t.Fatalf("expected --db-file to be replaced by --database, got args: %v", spec.Cmd.Args)
	}
	if !strings.Contains(argsJoined, "--database") {
		t.Fatalf("expected --database flag in mailpit args, got: %v", spec.Cmd.Args)
	}
}

func TestListInstallationsDeduplicatesMSSQLOnPath(t *testing.T) {
	root := t.TempDir()
	p := NewPaths(root)
	if err := p.Ensure(); err != nil {
		t.Fatal(err)
	}
	mssqlDir := filepath.Join(p.EngineBin("mssql"), "mssql-17.0.1000.7")
	if err := os.MkdirAll(mssqlDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(mssqlDir, "sqlcmd.exe"), []byte("stub"), 0o644); err != nil {
		t.Fatal(err)
	}

	origPath := os.Getenv("PATH")
	origRoot := os.Getenv("DARIUSDB_HOME")
	t.Cleanup(func() {
		_ = os.Setenv("PATH", origPath)
		_ = os.Setenv("DARIUSDB_HOME", origRoot)
		InvalidateSystemDetectCache()
	})
	_ = os.Setenv("DARIUSDB_HOME", root)
	_ = os.Setenv("PATH", mssqlDir+string(os.PathListSeparator)+origPath)
	InvalidateSystemDetectCache()

	if got := lookPathOutsideDir("sqlcmd.exe", filepath.Join(root, "bin")); strings.EqualFold(filepath.Dir(got), mssqlDir) {
		t.Fatalf("lookPathOutsideDir should skip DariusDB bin dir, got %s", got)
	}

	insts := ListInstallations(p, "mssql")
	countInDir := 0
	for _, in := range insts {
		if strings.EqualFold(filepath.Clean(in.Dir), filepath.Clean(mssqlDir)) {
			countInDir++
		}
	}
	if countInDir != 1 {
		t.Fatalf("expected exactly 1 installation for %s (no duplicate mssql-2025 entry), got %d: %+v", mssqlDir, countInDir, insts)
	}
}

func TestVCRedistChecksAndErrorAnnotation(t *testing.T) {
	if !isDLLNotFoundErr(errors.New("exit status 0xc0000135")) {
		t.Fatal("expected 0xc0000135 to be classified as DLL not found")
	}
	ann := annotateWindowsExecError("MySQL", errors.New("exit status 0xc0000135"))
	if !strings.Contains(ann.Error(), "vc_redist.x64.exe") {
		t.Fatalf("expected vc_redist hint in annotated error, got: %v", ann)
	}

	tmpBin := t.TempDir()
	if dirHasVCRuntime(tmpBin) {
		t.Fatal("expected empty dir to report missing VC++ runtime DLLs")
	}
	for _, dll := range vcRedistRequiredDLLs {
		if err := os.WriteFile(filepath.Join(tmpBin, dll), []byte("stub"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if !dirHasVCRuntime(tmpBin) {
		t.Fatal("expected populated dir to report VC++ runtime DLLs present")
	}

	// Simulate missing System32 VC++ runtime and verify MySQL fails fast with helpful error
	vcRedistCheckMu.Lock()
	origCheck := hasVCRedistX64Fn
	hasVCRedistX64Fn = func() bool { return false }
	vcRedistCheckMu.Unlock()
	t.Cleanup(func() {
		vcRedistCheckMu.Lock()
		hasVCRedistX64Fn = origCheck
		vcRedistCheckMu.Unlock()
	})

	root := t.TempDir()
	p := NewPaths(root)
	_ = p.Ensure()
	myDir := filepath.Join(p.EngineBin("mysql"), "mysql-26.10.0")
	_ = os.MkdirAll(filepath.Join(myDir, "bin"), 0o755)
	_ = os.WriteFile(filepath.Join(myDir, "bin", "mysqld.exe"), []byte("stub"), 0o644)

	myEng := &mysqlFamily{id: "mysql", name: "MySQL"}
	_, startErr := myEng.Start(p, Installation{Engine: "mysql", Name: "mysql-26.10.0", Version: "26.10.0", Dir: myDir}, EngineSettings{Port: 3306})
	if startErr == nil || !strings.Contains(startErr.Error(), "0xc0000135") || !strings.Contains(startErr.Error(), "vc_redist.x64.exe") {
		t.Fatalf("expected actionable VC++ error on MySQL Start without runtime, got: %v", startErr)
	}
}

func TestCheckAllPrerequisitesAndOfflineDiscovery(t *testing.T) {
	engPrereqs := CheckPrerequisites()
	if len(engPrereqs) != 2 || engPrereqs[0].ID != PrereqIDVCRedistX64 || engPrereqs[1].ID != PrereqIDMSSQLLocalDB {
		t.Fatalf("CheckPrerequisites should preserve [vcredist_x64, mssql_localdb], got %+v", engPrereqs)
	}
	allPrereqs := CheckAllPrerequisites()
	if len(allPrereqs) != 3 || allPrereqs[0].ID != PrereqIDWebView2 || allPrereqs[1].ID != PrereqIDVCRedistX64 || allPrereqs[2].ID != PrereqIDMSSQLLocalDB {
		t.Fatalf("CheckAllPrerequisites should return [webview2, vcredist_x64, mssql_localdb], got %+v", allPrereqs)
	}

	bundle := t.TempDir()
	prereqDir := filepath.Join(bundle, "offline", "prereqs")
	if err := os.MkdirAll(prereqDir, 0o755); err != nil {
		t.Fatal(err)
	}
	wvFile := filepath.Join(prereqDir, "MicrosoftEdgeWebView2RuntimeInstallerX64.exe")
	vcFile := filepath.Join(prereqDir, "vc_redist.x64.exe")
	ldbFile := filepath.Join(prereqDir, "SqlLocalDB.msi")
	for _, f := range []string{wvFile, vcFile, ldbFile} {
		if err := os.WriteFile(f, []byte("stub-installer"), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	if got := FindOfflinePrereq(PrereqIDWebView2, []string{bundle}); got != wvFile {
		t.Fatalf("expected %s, got %q", wvFile, got)
	}
	if got := FindOfflinePrereq(PrereqIDVCRedistX64, []string{bundle}); got != vcFile {
		t.Fatalf("expected %s, got %q", vcFile, got)
	}
	if got := FindOfflinePrereq(PrereqIDMSSQLLocalDB, []string{bundle}); got != ldbFile {
		t.Fatalf("expected %s, got %q", ldbFile, got)
	}

	// Offline-only install with missing file fails fast without touching network.
	p := NewPaths(t.TempDir())
	_ = p.Ensure()
	err := InstallPrerequisiteWithOffline(context.Background(), p, PrereqIDVCRedistX64, []string{t.TempDir()}, true, nil)
	if err == nil || !strings.Contains(strings.ToLower(err.Error()), "offline bundle") {
		t.Fatalf("expected offline-only missing error, got %v", err)
	}
}

func TestOfflineEngineArchiveAndDirInstall(t *testing.T) {
	bundle := t.TempDir()
	enginesDir := filepath.Join(bundle, "offline", "engines")
	phpSrcDir := filepath.Join(bundle, "offline", "bin", "php", "php-8.6.0")
	if err := os.MkdirAll(enginesDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(phpSrcDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(phpSrcDir, "php.exe"), []byte("php-stub"), 0o644); err != nil {
		t.Fatal(err)
	}

	// Build a valid Redis zip with a shared top-level folder
	redisZip := filepath.Join(enginesDir, "redis-8.10.3.zip")
	zf, err := os.Create(redisZip)
	if err != nil {
		t.Fatal(err)
	}
	zw := zip.NewWriter(zf)
	w, err := zw.Create("Redis-8.10.3/redis-server.exe")
	if err != nil {
		t.Fatal(err)
	}
	_, _ = w.Write([]byte("redis-stub"))
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	_ = zf.Close()

	gotZip, gotVer, ok := FindOfflineEngineArchive("redis", "8.10.3", []string{bundle})
	if !ok || gotZip != redisZip || gotVer != "8.10.3" {
		t.Fatalf("FindOfflineEngineArchive(redis) got (%q, %q, %v)", gotZip, gotVer, ok)
	}

	gotDir, gotPhpVer, ok := FindOfflineEngineDir("php", "8.6.0", []string{bundle})
	if !ok || gotDir != phpSrcDir || gotPhpVer != "8.6.0" {
		t.Fatalf("FindOfflineEngineDir(php) got (%q, %q, %v)", gotDir, gotPhpVer, ok)
	}

	installRoot := t.TempDir()
	p := NewPaths(installRoot)
	if err := p.Ensure(); err != nil {
		t.Fatal(err)
	}

	var doneNames []string
	dl := NewDownloader(p, nil, func(engine, name string) {
		doneNames = append(doneNames, engine+":"+name)
	})
	actualVer, err := dl.InstallZipSync(context.Background(), "redis", gotVer, gotZip)
	if err != nil || actualVer != "8.10.3" {
		t.Fatalf("InstallZipSync failed: ver=%q err=%v", actualVer, err)
	}
	if !fileExists(filepath.Join(p.EngineBin("redis"), "redis-8.10.3", "redis-server.exe")) {
		t.Fatal("expected extracted redis-server.exe in bin/redis/redis-8.10.3/")
	}

	phpName, err := InstallOfflineEngineDir(p, "php", gotPhpVer, gotDir, func(engine, name string) {
		doneNames = append(doneNames, engine+":"+name)
	})
	if err != nil || phpName != "php-8.6.0" {
		t.Fatalf("InstallOfflineEngineDir(php) failed: name=%q err=%v", phpName, err)
	}
	if !fileExists(filepath.Join(p.EngineBin("php"), "php-8.6.0", "php.exe")) {
		t.Fatal("expected copied php.exe in bin/php/php-8.6.0/")
	}
	if !fileExists(filepath.Join(p.EngineBin("php"), "php-8.6.0", "php.ini")) {
		t.Fatal("expected SyncManagedPHPIni to link php.ini after offline PHP install")
	}
	if len(doneNames) != 2 {
		t.Fatalf("expected 2 onDone callbacks, got %v", doneNames)
	}
}





