package core

import (
	"archive/zip"
	"context"
	"errors"
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

	if !strings.Contains(term.Banner(), "Darius DB (Lite Version 0.1.4) Built-in Terminal") {
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

