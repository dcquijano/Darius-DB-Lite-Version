package core

import (
	"context"
	"fmt"
	"html"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

type apacheEngine struct{}

func (a *apacheEngine) ID() string   { return "apache" }
func (a *apacheEngine) Name() string { return "Apache Web Server" }
func (a *apacheEngine) IsSQL() bool  { return false }

func (a *apacheEngine) Detect(dir string) bool {
	return fileExists(filepath.Join(dir, "bin", "httpd.exe")) || fileExists(filepath.Join(dir, "httpd.exe"))
}

func (a *apacheEngine) ClientBinDir(inst Installation) string {
	if fileExists(filepath.Join(inst.Dir, "bin", "httpd.exe")) {
		return filepath.Join(inst.Dir, "bin")
	}
	return inst.Dir
}

func (a *apacheEngine) DataDir(p *Paths, _ Installation) string {
	return p.Www
}

func (a *apacheEngine) ConfigFile(p *Paths, _ Installation) string {
	return filepath.Join(p.EngineEtc("apache"), "httpd.conf")
}

const apacheHttpdConf = `# Darius DB (Lite Version 0.1.2) - Apache HTTP Server configuration
# DocumentRoot (www/) and Listen port are passed automatically by Darius DB.
ServerName localhost

LoadModule access_compat_module modules/mod_access_compat.so
LoadModule actions_module modules/mod_actions.so
LoadModule alias_module modules/mod_alias.so
LoadModule allowmethods_module modules/mod_allowmethods.so
LoadModule asis_module modules/mod_asis.so
LoadModule auth_basic_module modules/mod_auth_basic.so
LoadModule authn_core_module modules/mod_authn_core.so
LoadModule authn_file_module modules/mod_authn_file.so
LoadModule authz_core_module modules/mod_authz_core.so
LoadModule authz_groupfile_module modules/mod_authz_groupfile.so
LoadModule authz_host_module modules/mod_authz_host.so
LoadModule authz_user_module modules/mod_authz_user.so
LoadModule autoindex_module modules/mod_autoindex.so
LoadModule cgi_module modules/mod_cgi.so
LoadModule dir_module modules/mod_dir.so
LoadModule env_module modules/mod_env.so
LoadModule headers_module modules/mod_headers.so
LoadModule include_module modules/mod_include.so
LoadModule isapi_module modules/mod_isapi.so
LoadModule log_config_module modules/mod_log_config.so
LoadModule mime_module modules/mod_mime.so
LoadModule negotiation_module modules/mod_negotiation.so
LoadModule rewrite_module modules/mod_rewrite.so
LoadModule setenvif_module modules/mod_setenvif.so

<IfModule unixd_module>
User daemon
Group daemon
</IfModule>

ServerAdmin admin@localhost
DocumentRoot "${WWWROOT}"

<Directory />
    AllowOverride none
    Require all denied
</Directory>

<Directory "${WWWROOT}">
    Options Indexes FollowSymLinks
    AllowOverride All
    Require all granted
</Directory>

<IfModule dir_module>
    DirectoryIndex index.html index.htm index.php
</IfModule>

<Files ".ht*">
    Require all denied
</Files>

ErrorLog "${LOGROOT}/apache-error.log"
LogLevel warn

<IfModule log_config_module>
    LogFormat "%h %l %u %t \"%r\" %>s %b" common
    CustomLog "${LOGROOT}/apache.log" common
</IfModule>

<IfModule mime_module>
    TypesConfig "${SRVROOT}/conf/mime.types"
    AddType application/x-compress .Z
    AddType application/x-gzip .gz .tgz
    AddType text/html .shtml
    AddOutputFilter INCLUDES .shtml
</IfModule>
`

const defaultWwwIndexHTML = `<!DOCTYPE html>
<html lang="en">
<head>
  <meta charset="UTF-8" />
  <meta name="viewport" content="width=device-width, initial-scale=1.0" />
  <title>Darius DB (Lite Version 0.1.2) — Local Web Server</title>
  <style>
    :root { --bg:#0c1220; --card:#162033; --border:rgba(148,163,184,0.18); --text:#e6edf7; --muted:#8ea0ba; --accent:#14b8a6; --green:#22c55e; }
    * { box-sizing:border-box; }
    body { margin:0; min-height:100vh; font-family:"Segoe UI",system-ui,sans-serif; background:var(--bg); color:var(--text); display:flex; align-items:center; justify-content:center; padding:24px; }
    .box { max-width:680px; width:100%; background:var(--card); border:1px solid var(--border); border-radius:14px; padding:32px; box-shadow:0 16px 40px rgba(0,0,0,0.35); }
    .badge { display:inline-flex; align-items:center; gap:8px; background:rgba(34,197,94,0.14); color:var(--green); padding:5px 12px; border-radius:99px; font-size:12.5px; font-weight:600; }
    .dot { width:8px; height:8px; border-radius:50%; background:var(--green); box-shadow:0 0 8px var(--green); }
    h1 { margin:16px 0 8px; font-size:24px; }
    p { margin:0 0 18px; color:var(--muted); line-height:1.55; }
    code { background:#0e1626; border:1px solid var(--border); padding:3px 7px; border-radius:6px; font-family:Consolas,monospace; color:#38bdf8; font-size:13px; }
    .steps { background:#0e1626; border:1px solid var(--border); border-radius:10px; padding:16px 20px; margin-top:16px; }
    .steps h3 { margin:0 0 10px; font-size:14px; color:var(--accent); text-transform:uppercase; letter-spacing:0.04em; }
    .steps ol { margin:0; padding-left:20px; color:var(--text); line-height:1.8; font-size:14px; }
    .foot { margin-top:22px; padding-top:14px; border-top:1px solid var(--border); display:flex; justify-content:space-between; font-size:12px; color:var(--muted); }
  </style>
</head>
<body>
  <div class="box">
    <span class="badge"><span class="dot"></span> Apache Web Server is Running</span>
    <h1>Darius DB (Lite Version 0.1.2)</h1>
    <p>Your local web server is active and serving websites directly from your portable <code>www/</code> directory.</p>
    <div class="steps">
      <h3>How to Host Your Local Website</h3>
      <ol>
        <li>Click <strong>www/</strong> on the Apache card in Darius DB (or open <code>C:\DariusDB\www\</code>).</li>
        <li>Place your website files in <code>www\</code> or create a project subfolder like <code>www\my-site\index.html</code>.</li>
        <li>Open <code>http://localhost:PORT/my-site/</code> in your browser to preview it live!</li>
      </ol>
    </div>
    <div class="foot">
      <span>Created by Darius Cumpio · MIT Open Source</span>
      <span>Darius DB (Lite Version 0.1.2)</span>
    </div>
  </div>
</body>
</html>
`

func (a *apacheEngine) Prepare(ctx context.Context, p *Paths, inst Installation, s EngineSettings, logf func(string)) error {
	if err := writeIfMissing(a.ConfigFile(p, inst), apacheHttpdConf); err != nil {
		return err
	}
	if err := os.MkdirAll(p.Www, 0o755); err != nil {
		return err
	}
	if dirEmpty(p.Www) {
		_ = writeIfMissing(filepath.Join(p.Www, "index.html"), defaultWwwIndexHTML)
	}
	return nil
}

func (a *apacheEngine) Start(p *Paths, inst Installation, s EngineSettings) (*StartSpec, error) {
	httpd := filepath.Join(inst.Dir, "bin", "httpd.exe")
	if !fileExists(httpd) {
		httpd = filepath.Join(inst.Dir, "httpd.exe")
	}
	if fileExists(httpd) {
		cmd := exec.Command(httpd,
			"-f", a.ConfigFile(p, inst),
			"-c", fmt.Sprintf(`Define SRVROOT "%s"`, filepath.ToSlash(inst.Dir)),
			"-c", fmt.Sprintf(`Define WWWROOT "%s"`, filepath.ToSlash(p.Www)),
			"-c", fmt.Sprintf(`Define LOGROOT "%s"`, filepath.ToSlash(p.Logs)),
			"-c", fmt.Sprintf("Listen 127.0.0.1:%d", s.Port),
			"-DFOREGROUND",
		)
		cmd.Dir = inst.Dir
		return &StartSpec{Cmd: cmd}, nil
	}

	// Built-in portable web server mode (runs without needing external httpd.exe download)
	selfExe, err := os.Executable()
	if err != nil {
		return nil, err
	}
	cmd := exec.Command(selfExe, "--httpd",
		"--port", strconv.Itoa(s.Port),
		"--www", p.Www,
	)
	cmd.Dir = p.Root
	return &StartSpec{Cmd: cmd}, nil
}

func (a *apacheEngine) Stop(p *Paths, inst Installation, s EngineSettings) error {
	// Foreground httpd.exe and --httpd stop cleanly when terminated by Manager.Stop
	return fmt.Errorf("use process termination")
}

func (a *apacheEngine) PidOf(p *Paths, inst Installation) int { return 0 }

func detectBuiltinApache(p *Paths) Installation {
	return Installation{
		Engine:  "apache",
		Name:    "apache-lite-2.4",
		Version: "2.4.0",
		Dir:     p.Root,
	}
}

// RunBuiltInHTTPD runs the built-in lightweight web server serving wwwDir on 127.0.0.1:port.
func RunBuiltInHTTPD(args []string) int {
	port := 8080
	wwwDir := filepath.Join(ResolveRoot(), "www")
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--port":
			if i+1 < len(args) {
				if p, err := strconv.Atoi(args[i+1]); err == nil && p > 0 {
					port = p
				}
				i++
			}
		case "--www":
			if i+1 < len(args) {
				wwwDir = args[i+1]
				i++
			}
		}
	}

	_ = os.MkdirAll(wwwDir, 0o755)
	if dirEmpty(wwwDir) {
		_ = writeIfMissing(filepath.Join(wwwDir, "index.html"), defaultWwwIndexHTML)
	}

	fs := http.FileServer(http.Dir(wwwDir))
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		w.Header().Set("Server", "Apache/2.4 (DariusDB-Lite/0.1.2)")
		// Clean directory listing if requesting a directory without index.html
		relPath := filepath.FromSlash(strings.TrimPrefix(filepath.Clean("/"+r.URL.Path), "/"))
		fullPath := filepath.Join(wwwDir, relPath)
		if st, err := os.Stat(fullPath); err == nil && st.IsDir() {
			if !fileExists(filepath.Join(fullPath, "index.html")) && !fileExists(filepath.Join(fullPath, "index.htm")) {
				renderDirListing(w, r.URL.Path, fullPath)
				fmt.Fprintf(os.Stdout, "%s - \"%s %s\" 200 (%s)\n",
					time.Now().Format("2006-01-02 15:04:05"), r.Method, r.URL.Path, time.Since(start).Round(time.Microsecond))
				return
			}
		}
		fs.ServeHTTP(w, r)
		fmt.Fprintf(os.Stdout, "%s - \"%s %s\" (%s)\n",
			time.Now().Format("2006-01-02 15:04:05"), r.Method, r.URL.Path, time.Since(start).Round(time.Microsecond))
	})

	addr := fmt.Sprintf("127.0.0.1:%d", port)
	fmt.Fprintf(os.Stdout, "[apache] Serving %s at http://localhost:%d/\n", wwwDir, port)
	if err := http.ListenAndServe(addr, handler); err != nil {
		fmt.Fprintf(os.Stderr, "[apache] error: %v\n", err)
		return 1
	}
	return 0
}

func renderDirListing(w http.ResponseWriter, urlPath, dirPath string) {
	entries, err := os.ReadDir(dirPath)
	if err != nil {
		http.Error(w, "Cannot read directory", http.StatusInternalServerError)
		return
	}
	if !strings.HasSuffix(urlPath, "/") {
		urlPath += "/"
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	var b strings.Builder
	fmt.Fprintf(&b, `<!DOCTYPE html><html><head><meta charset="utf-8"><title>Index of %s</title>`, html.EscapeString(urlPath))
	b.WriteString(`<style>body{font-family:"Segoe UI",system-ui,sans-serif;background:#0c1220;color:#e6edf7;padding:28px;max-width:760px;margin:0 auto}h2{margin:0 0 16px;color:#14b8a6}ul{list-style:none;padding:0;margin:0;background:#162033;border:1px solid rgba(148,163,184,0.18);border-radius:10px;overflow:hidden}li{border-bottom:1px solid rgba(148,163,184,0.12)}li:last-child{border-bottom:none}a{display:flex;justify-content:space-between;padding:10px 16px;color:#e6edf7;text-decoration:none}a:hover{background:rgba(20,184,166,0.12);color:#14b8a6}.tag{color:#8ea0ba;font-size:12px}</style></head><body>`)
	fmt.Fprintf(&b, `<h2>Index of %s</h2><ul>`, html.EscapeString(urlPath))
	if urlPath != "/" {
		b.WriteString(`<li><a href="../"><span>📁 .. (Parent Directory)</span><span class="tag">DIR</span></a></li>`)
	}
	for _, e := range entries {
		name := e.Name()
		href := urlPath + name
		tag := "FILE"
		icon := "📄 "
		if e.IsDir() {
			href += "/"
			tag = "DIR"
			icon = "📁 "
		}
		fmt.Fprintf(&b, `<li><a href="%s"><span>%s%s</span><span class="tag">%s</span></a></li>`,
			html.EscapeString(href), icon, html.EscapeString(name), tag)
	}
	b.WriteString(`</ul></body></html>`)
	_, _ = w.Write([]byte(b.String()))
}
