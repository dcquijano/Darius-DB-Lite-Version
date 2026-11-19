package core

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/mail"
	"net/smtp"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
)

// ---------------------------------------------------------------- PHP Engine

type phpEngine struct{}

func (p *phpEngine) ID() string   { return "php" }
func (p *phpEngine) Name() string { return "PHP" }
func (p *phpEngine) IsSQL() bool  { return false }

func (p *phpEngine) Detect(dir string) bool {
	return fileExists(filepath.Join(dir, "php.exe")) || fileExists(filepath.Join(dir, "bin", "php.exe"))
}

func (p *phpEngine) ClientBinDir(inst Installation) string {
	if fileExists(filepath.Join(inst.Dir, "bin", "php.exe")) {
		return filepath.Join(inst.Dir, "bin")
	}
	return inst.Dir
}

func (p *phpEngine) DataDir(paths *Paths, _ Installation) string {
	return paths.Www
}

func (p *phpEngine) ConfigFile(paths *Paths, _ Installation) string {
	return filepath.Join(paths.EngineEtc("php"), "php.ini")
}

const defaultPhpIni = `; Darius DB (Lite Version 0.1.2) - PHP Local Development Configuration
; DocumentRoot: www/ | Pre-configured with Mailpit SMTP (127.0.0.1:1025)

[PHP]
engine = On
short_open_tag = Off
precision = 14
output_buffering = 4096
zlib.output_compression = Off
implicit_flush = Off
serialize_precision = -1
zend.enable_gc = On
expose_php = On

max_execution_time = 120
max_input_time = 120
memory_limit = 512M

error_reporting = E_ALL
display_errors = On
display_startup_errors = On
log_errors = On

post_max_size = 128M
file_uploads = On
upload_max_filesize = 128M
max_file_uploads = 50

allow_url_fopen = On
allow_url_include = Off
default_socket_timeout = 60

extension_dir = "ext"
extension=curl
extension=fileinfo
extension=gd
extension=mbstring
extension=mysqli
extension=openssl
extension=pdo_mysql
extension=pdo_pgsql
extension=pdo_sqlite
extension=pgsql
extension=sqlite3
extension=zip

[Date]
date.timezone = UTC

[mail function]
; Connects PHP mail() directly to Darius DB Mailpit on 127.0.0.1:1025
SMTP = 127.0.0.1
smtp_port = 1025
sendmail_from = dev@dariusdb.local
`

func (p *phpEngine) Prepare(ctx context.Context, paths *Paths, inst Installation, s EngineSettings, logf func(string)) error {
	if err := writeIfMissing(p.ConfigFile(paths, inst), defaultPhpIni); err != nil {
		return err
	}
	if err := os.MkdirAll(paths.Www, 0o755); err != nil {
		return err
	}
	ensureWwwIndex(paths.Www)
	return nil
}

func (p *phpEngine) Start(paths *Paths, inst Installation, s EngineSettings) (*StartSpec, error) {
	phpExe := filepath.Join(inst.Dir, "php.exe")
	if !fileExists(phpExe) {
		phpExe = filepath.Join(inst.Dir, "bin", "php.exe")
	}
	if !fileExists(phpExe) {
		return nil, errors.New("php.exe not found in " + inst.Dir)
	}
	extDir := filepath.Join(filepath.Dir(phpExe), "ext")
	args := []string{
		"-S", fmt.Sprintf("127.0.0.1:%d", s.Port),
		"-t", paths.Www,
		"-c", p.ConfigFile(paths, inst),
	}
	if dirExists(extDir) {
		args = append(args, "-d", fmt.Sprintf("extension_dir=%s", extDir))
	}
	cmd := exec.Command(phpExe, args...)
	cmd.Dir = paths.Www
	return &StartSpec{Cmd: cmd}, nil
}

func (p *phpEngine) Stop(paths *Paths, inst Installation, s EngineSettings) error {
	return fmt.Errorf("use process termination")
}

func (p *phpEngine) PidOf(paths *Paths, inst Installation) int { return 0 }

func detectSystemPHP() (Installation, bool) {
	candidates := []string{}
	if pathExe, err := exec.LookPath("php.exe"); err == nil && pathExe != "" {
		candidates = append(candidates, filepath.Dir(pathExe))
	}
	for _, pat := range []string{`C:\php*`, `C:\tools\php*`, `C:\Program Files\PHP*`} {
		if matches, err := filepath.Glob(pat); err == nil {
			candidates = append(candidates, matches...)
		}
	}
	for _, dir := range candidates {
		exe := filepath.Join(dir, "php.exe")
		if !fileExists(exe) {
			continue
		}
		ver := "8.4.0"
		cmd := exec.Command(exe, "-r", "echo PHP_VERSION;")
		hideWindow(cmd)
		if out, err := cmd.Output(); err == nil {
			if v := strings.TrimSpace(string(out)); v != "" {
				ver = parseVersion(v)
			}
		}
		return Installation{
			Engine:  "php",
			Name:    "php-" + ver,
			Version: ver,
			Dir:     dir,
		}, true
	}
	return Installation{}, false
}

// ---------------------------------------------------------------- Mailpit Engine

type mailpitEngine struct{}

func (m *mailpitEngine) ID() string   { return "mailpit" }
func (m *mailpitEngine) Name() string { return "Mailpit" }
func (m *mailpitEngine) IsSQL() bool  { return false }

func (m *mailpitEngine) Detect(dir string) bool {
	return fileExists(filepath.Join(dir, "mailpit.exe")) || fileExists(filepath.Join(dir, "bin", "mailpit.exe"))
}

func (m *mailpitEngine) ClientBinDir(inst Installation) string {
	if fileExists(filepath.Join(inst.Dir, "bin", "mailpit.exe")) {
		return filepath.Join(inst.Dir, "bin")
	}
	return inst.Dir
}

func (m *mailpitEngine) DataDir(p *Paths, _ Installation) string {
	return filepath.Join(p.Data, "mailpit")
}

func (m *mailpitEngine) ConfigFile(p *Paths, _ Installation) string {
	return filepath.Join(p.EngineEtc("mailpit"), "mailpit.conf")
}

const defaultMailpitConf = `# Darius DB (Lite Version 0.1.2) - Mailpit Email Testing Server Configuration
# Captures all local outgoing emails via SMTP and displays them in the Web Inbox UI.
http_port = 8025
smtp_port = 1025
bind_addr = 127.0.0.1
max_messages = 500
`

func (m *mailpitEngine) Prepare(ctx context.Context, p *Paths, inst Installation, s EngineSettings, logf func(string)) error {
	if err := writeIfMissing(m.ConfigFile(p, inst), defaultMailpitConf); err != nil {
		return err
	}
	return os.MkdirAll(m.DataDir(p, inst), 0o755)
}

func (m *mailpitEngine) Start(p *Paths, inst Installation, s EngineSettings) (*StartSpec, error) {
	mpExe := filepath.Join(inst.Dir, "mailpit.exe")
	if !fileExists(mpExe) {
		mpExe = filepath.Join(inst.Dir, "bin", "mailpit.exe")
	}
	if fileExists(mpExe) {
		dbFile := filepath.Join(m.DataDir(p, inst), "mailpit.db")
		cmd := exec.Command(mpExe,
			"--listen", fmt.Sprintf("127.0.0.1:%d", s.Port),
			"--smtp", "127.0.0.1:1025",
			"--db-file", dbFile,
		)
		cmd.Dir = inst.Dir
		return &StartSpec{Cmd: cmd}, nil
	}

	// Built-in portable Mailpit server mode (zero-download instant SMTP + Web Inbox)
	selfExe, err := os.Executable()
	if err != nil {
		return nil, err
	}
	cmd := exec.Command(selfExe, "--mailpit",
		"--port", strconv.Itoa(s.Port),
		"--smtp", "1025",
		"--data", m.DataDir(p, inst),
	)
	cmd.Dir = p.Root
	return &StartSpec{Cmd: cmd}, nil
}

func (m *mailpitEngine) Stop(p *Paths, inst Installation, s EngineSettings) error {
	return fmt.Errorf("use process termination")
}

func (m *mailpitEngine) PidOf(p *Paths, inst Installation) int { return 0 }

func detectBuiltinMailpit(p *Paths) Installation {
	return Installation{
		Engine:  "mailpit",
		Name:    "mailpit-lite-1.22",
		Version: "1.22.0",
		Dir:     p.Root,
	}
}

// ---------------------------------------------------------------- Built-in Mailpit Server (SMTP :1025 + Web UI :8025)

type CapturedMail struct {
	ID        string            `json:"id"`
	From      string            `json:"from"`
	To        []string          `json:"to"`
	Subject   string            `json:"subject"`
	Date      string            `json:"date"`
	Size      int               `json:"size"`
	Body      string            `json:"body"`
	Raw       string            `json:"raw"`
	Headers   map[string]string `json:"headers"`
	CreatedAt string            `json:"createdAt"`
}

type mailStore struct {
	mu       sync.Mutex
	filePath string
	msgs     []CapturedMail
}

func loadMailStore(dataDir string) *mailStore {
	_ = os.MkdirAll(dataDir, 0o755)
	fp := filepath.Join(dataDir, "messages.json")
	ms := &mailStore{filePath: fp, msgs: []CapturedMail{}}
	if b, err := os.ReadFile(fp); err == nil {
		_ = json.Unmarshal(b, &ms.msgs)
	}
	return ms
}

func (ms *mailStore) saveLocked() {
	if b, err := json.MarshalIndent(ms.msgs, "", "  "); err == nil {
		_ = os.WriteFile(ms.filePath, b, 0o644)
	}
}

func (ms *mailStore) add(from string, to []string, raw string) CapturedMail {
	ms.mu.Lock()
	defer ms.mu.Unlock()

	hdrs := map[string]string{}
	subject := "(No Subject)"
	body := raw
	if parsed, err := mail.ReadMessage(strings.NewReader(raw)); err == nil {
		for k, v := range parsed.Header {
			hdrs[k] = strings.Join(v, ", ")
		}
		if s := parsed.Header.Get("Subject"); s != "" {
			subject = s
		}
		if f := parsed.Header.Get("From"); f != "" {
			from = f
		}
		var b strings.Builder
		sc := bufio.NewScanner(parsed.Body)
		for sc.Scan() {
			b.WriteString(sc.Text() + "\n")
		}
		body = strings.TrimSpace(b.String())
	} else if idx := strings.Index(raw, "\r\n\r\n"); idx != -1 {
		body = strings.TrimSpace(raw[idx+4:])
	}

	now := time.Now()
	m := CapturedMail{
		ID:        fmt.Sprintf("msg-%d", now.UnixNano()),
		From:      from,
		To:        to,
		Subject:   subject,
		Date:      now.Format("2006-01-02 15:04:05"),
		Size:      len(raw),
		Body:      body,
		Raw:       raw,
		Headers:   hdrs,
		CreatedAt: now.Format(time.RFC3339),
	}
	ms.msgs = append([]CapturedMail{m}, ms.msgs...)
	if len(ms.msgs) > 500 {
		ms.msgs = ms.msgs[:500]
	}
	ms.saveLocked()
	return m
}

func (ms *mailStore) list() []CapturedMail {
	ms.mu.Lock()
	defer ms.mu.Unlock()
	out := make([]CapturedMail, len(ms.msgs))
	copy(out, ms.msgs)
	return out
}

func (ms *mailStore) clear() {
	ms.mu.Lock()
	defer ms.mu.Unlock()
	ms.msgs = []CapturedMail{}
	ms.saveLocked()
}

func (ms *mailStore) deleteID(id string) {
	ms.mu.Lock()
	defer ms.mu.Unlock()
	kept := make([]CapturedMail, 0, len(ms.msgs))
	for _, m := range ms.msgs {
		if m.ID != id {
			kept = append(kept, m)
		}
	}
	ms.msgs = kept
	ms.saveLocked()
}

// RunBuiltInMailpit runs the local SMTP capture server (:1025) and Web Inbox UI (:8025).
func RunBuiltInMailpit(args []string) int {
	httpPort := 8025
	smtpPort := 1025
	dataDir := filepath.Join(ResolveRoot(), "data", "mailpit")
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--port":
			if i+1 < len(args) {
				if p, err := strconv.Atoi(args[i+1]); err == nil && p > 0 {
					httpPort = p
				}
				i++
			}
		case "--smtp":
			if i+1 < len(args) {
				if p, err := strconv.Atoi(args[i+1]); err == nil && p > 0 {
					smtpPort = p
				}
				i++
			}
		case "--data":
			if i+1 < len(args) {
				dataDir = args[i+1]
				i++
			}
		}
	}

	store := loadMailStore(dataDir)

	// 1. Start SMTP listener on 127.0.0.1:smtpPort
	smtpAddr := fmt.Sprintf("127.0.0.1:%d", smtpPort)
	ln, err := net.Listen("tcp", smtpAddr)
	if err != nil {
		fmt.Fprintf(os.Stderr, "[mailpit] SMTP listen error on %s: %v\n", smtpAddr, err)
	} else {
		fmt.Fprintf(os.Stdout, "[mailpit] SMTP server listening on %s\n", smtpAddr)
		go func() {
			for {
				conn, err := ln.Accept()
				if err != nil {
					return
				}
				go handleSMTPConn(conn, store)
			}
		}()
	}

	// 2. Start Web Inbox UI on 127.0.0.1:httpPort
	mux := http.NewServeMux()
	mux.HandleFunc("/api/messages", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"smtpPort": smtpPort,
			"httpPort": httpPort,
			"messages": store.list(),
		})
	})
	mux.HandleFunc("/api/clear", func(w http.ResponseWriter, r *http.Request) {
		store.clear()
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"ok":true}`))
	})
	mux.HandleFunc("/api/delete", func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			ID string `json:"id"`
		}
		_ = json.NewDecoder(r.Body).Decode(&req)
		store.deleteID(req.ID)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"ok":true}`))
	})
	mux.HandleFunc("/api/send-test", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		msg := "From: Darius DB <no-reply@dariusdb.local>\r\n" +
			"To: developer@localhost\r\n" +
			"Subject: Welcome to Darius DB (Lite Version 0.1.2) Mailpit!\r\n" +
			"Date: " + time.Now().Format(time.RFC1123Z) + "\r\n" +
			"Content-Type: text/plain; charset=utf-8\r\n\r\n" +
			"Hello from Darius DB (Lite Version 0.1.2) Mailpit!\r\n\r\n" +
			"Your local SMTP email capture server is running on 127.0.0.1:" + strconv.Itoa(smtpPort) + ".\r\n" +
			"Any emails sent from PHP mail(), Laravel, ASP.NET / Visual Studio, Node.js, or Python to 127.0.0.1:" + strconv.Itoa(smtpPort) + " will appear right here in real time.\r\n\r\n" +
			"— Created by Darius Cumpio (MIT Open Source)"
		if err := smtp.SendMail(smtpAddr, nil, "no-reply@dariusdb.local", []string{"developer@localhost"}, []byte(msg)); err != nil {
			store.add("Darius DB <no-reply@dariusdb.local>", []string{"developer@localhost"}, msg)
		}
		_, _ = w.Write([]byte(`{"ok":true}`))
	})
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write([]byte(mailpitWebUIHTML))
	})

	httpAddr := fmt.Sprintf("127.0.0.1:%d", httpPort)
	fmt.Fprintf(os.Stdout, "[mailpit] Web Inbox UI listening at http://localhost:%d/\n", httpPort)
	if err := http.ListenAndServe(httpAddr, mux); err != nil {
		fmt.Fprintf(os.Stderr, "[mailpit] HTTP error: %v\n", err)
		return 1
	}
	return 0
}

func handleSMTPConn(conn net.Conn, store *mailStore) {
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(2 * time.Minute))
	r := bufio.NewReader(conn)
	writeLine := func(s string) {
		_, _ = conn.Write([]byte(s + "\r\n"))
	}

	writeLine("220 dariusdb.local ESMTP Darius DB Mailpit Ready")
	var from string
	var to []string
	for {
		line, err := r.ReadString('\n')
		if err != nil {
			return
		}
		trimmed := strings.TrimRight(line, "\r\n")
		upper := strings.ToUpper(trimmed)

		switch {
		case strings.HasPrefix(upper, "EHLO") || strings.HasPrefix(upper, "HELO"):
			writeLine("250-dariusdb.local Hello")
			writeLine("250-SIZE 20971520")
			writeLine("250 8BITMIME")
		case strings.HasPrefix(upper, "MAIL FROM:"):
			from = strings.Trim(strings.TrimSpace(trimmed[10:]), "<>")
			writeLine("250 2.1.0 OK")
		case strings.HasPrefix(upper, "RCPT TO:"):
			rcpt := strings.Trim(strings.TrimSpace(trimmed[8:]), "<>")
			to = append(to, rcpt)
			writeLine("250 2.1.5 OK")
		case upper == "DATA":
			writeLine("354 End data with <CR><LF>.<CR><LF>")
			var data strings.Builder
			for {
				dline, err := r.ReadString('\n')
				if err != nil {
					return
				}
				if strings.TrimRight(dline, "\r\n") == "." {
					break
				}
				if strings.HasPrefix(dline, "..") {
					dline = dline[1:]
				}
				data.WriteString(dline)
			}
			store.add(from, to, data.String())
			from = ""
			to = nil
			writeLine("250 2.0.0 OK: message queued in Darius DB Mailpit")
		case upper == "RSET":
			from = ""
			to = nil
			writeLine("250 2.0.0 OK")
		case upper == "NOOP":
			writeLine("250 2.0.0 OK")
		case upper == "QUIT":
			writeLine("221 2.0.0 Bye")
			return
		default:
			writeLine("250 OK")
		}
	}
}

const mailpitWebUIHTML = `<!DOCTYPE html>
<html lang="en">
<head>
  <meta charset="UTF-8" />
  <meta name="viewport" content="width=device-width, initial-scale=1.0" />
  <title>Mailpit — Darius DB (Lite Version 0.1.2)</title>
  <style>
    :root { --bg:#0c1220; --panel:#162033; --subtle:#0e1626; --border:rgba(148,163,184,0.18); --text:#e6edf7; --muted:#8ea0ba; --accent:#14b8a6; --green:#22c55e; --danger:#ef4444; }
    * { box-sizing:border-box; }
    body { margin:0; height:100vh; display:flex; flex-direction:column; font-family:"Segoe UI",system-ui,sans-serif; background:var(--bg); color:var(--text); }
    header { display:flex; align-items:center; justify-content:space-between; padding:12px 20px; background:var(--panel); border-bottom:1px solid var(--border); }
    .brand { display:flex; align-items:center; gap:10px; }
    .badge { background:rgba(34,197,94,0.14); color:var(--green); padding:4px 10px; border-radius:99px; font-size:12px; font-weight:600; }
    .actions { display:flex; align-items:center; gap:8px; }
    .btn { background:var(--accent); color:#042f2e; font-weight:600; border:none; border-radius:8px; padding:7px 13px; font-size:12.5px; cursor:pointer; }
    .btn.secondary { background:#1e293b; color:var(--text); border:1px solid var(--border); }
    .btn.danger { background:rgba(239,68,68,0.16); color:#fca5a5; border:1px solid rgba(239,68,68,0.3); }
    main { flex:1; display:grid; grid-template-columns:340px 1fr; min-height:0; }
    .list-pane { border-right:1px solid var(--border); overflow-y:auto; background:var(--subtle); }
    .msg-item { padding:12px 16px; border-bottom:1px solid var(--border); cursor:pointer; }
    .msg-item:hover, .msg-item.active { background:var(--panel); }
    .msg-subj { font-weight:600; font-size:13.5px; margin-bottom:4px; color:var(--text); }
    .msg-meta { font-size:12px; color:var(--muted); display:flex; justify-content:space-between; }
    .view-pane { padding:22px; overflow-y:auto; }
    .empty { color:var(--muted); text-align:center; padding:48px 20px; font-size:13.5px; }
    .hdr-box { background:var(--panel); border:1px solid var(--border); border-radius:10px; padding:14px 18px; margin-bottom:16px; font-size:13px; line-height:1.7; }
    .body-box { background:var(--panel); border:1px solid var(--border); border-radius:10px; padding:18px; white-space:pre-wrap; font-family:Consolas,monospace; font-size:13px; line-height:1.6; }
  </style>
</head>
<body>
  <header>
    <div class="brand">
      <strong style="font-size:16px">📬 Mailpit — Darius DB (Lite Version 0.1.2)</strong>
      <span class="badge" id="smtpBadge">SMTP 127.0.0.1:1025</span>
    </div>
    <div class="actions">
      <button class="btn" id="btnTest">✉ Send Test Email</button>
      <button class="btn secondary" id="btnRefresh">↻ Refresh</button>
      <button class="btn danger" id="btnClear">🗑 Clear Inbox</button>
    </div>
  </header>
  <main>
    <aside class="list-pane" id="msgList"></aside>
    <section class="view-pane" id="msgView">
      <div class="empty">Select an email on the left or click <strong>✉ Send Test Email</strong> to test local SMTP capture.</div>
    </section>
  </main>
  <script>
    (() => {
      let messages = [];
      let activeId = "";
      const esc = (s) => String(s || "").replace(/&/g,"&amp;").replace(/</g,"&lt;").replace(/>/g,"&gt;");

      async function load() {
        const r = await fetch("/api/messages");
        const data = await r.json();
        messages = data.messages || [];
        document.getElementById("smtpBadge").textContent = "SMTP 127.0.0.1:" + (data.smtpPort || 1025);
        renderList();
      }

      function renderList() {
        const el = document.getElementById("msgList");
        if (!messages.length) {
          el.innerHTML = '<div class="empty">Inbox is empty.<br><br>Send mail to <code>127.0.0.1:1025</code> or click <strong>Send Test Email</strong>.</div>';
          document.getElementById("msgView").innerHTML = '<div class="empty">No email selected.</div>';
          return;
        }
        if (!activeId || !messages.some((m) => m.id === activeId)) activeId = messages[0].id;
        el.innerHTML = messages.map((m) =>
          '<div class="msg-item ' + (m.id === activeId ? 'active' : '') + '" data-id="' + esc(m.id) + '">' +
            '<div class="msg-subj">' + esc(m.subject) + '</div>' +
            '<div class="msg-meta"><span>' + esc(m.from) + '</span><span>' + esc(m.date.split(" ")[1] || m.date) + '</span></div>' +
          '</div>'
        ).join("");
        renderView();
      }

      function renderView() {
        const m = messages.find((x) => x.id === activeId);
        const el = document.getElementById("msgView");
        if (!m) {
          el.innerHTML = '<div class="empty">No email selected.</div>';
          return;
        }
        el.innerHTML =
          '<div class="hdr-box">' +
            '<div><strong>Subject:</strong> ' + esc(m.subject) + '</div>' +
            '<div><strong>From:</strong> ' + esc(m.from) + '</div>' +
            '<div><strong>To:</strong> ' + esc((m.to || []).join(", ")) + '</div>' +
            '<div><strong>Date:</strong> ' + esc(m.date) + ' (' + m.size + ' bytes)</div>' +
          '</div>' +
          '<div class="body-box">' + esc(m.body) + '</div>';
      }

      document.getElementById("msgList").onclick = (e) => {
        const item = e.target.closest("[data-id]");
        if (item) { activeId = item.dataset.id; renderList(); }
      };
      document.getElementById("btnTest").onclick = async () => {
        await fetch("/api/send-test", { method: "POST" });
        await load();
      };
      document.getElementById("btnRefresh").onclick = load;
      document.getElementById("btnClear").onclick = async () => {
        await fetch("/api/clear", { method: "POST" });
        activeId = "";
        await load();
      };
      load();
      setInterval(load, 4000);
    })();
  </script>
</body>
</html>
`
