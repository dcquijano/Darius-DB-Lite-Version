package core

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"html"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
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
<!-- darius-www-v3 -->
<html lang="en">
<head>
  <meta charset="UTF-8" />
  <meta name="viewport" content="width=device-width, initial-scale=1.0" />
  <title>Darius DB (Lite Version 0.1.2) — Local Web Server & Drag-and-Drop Hosting</title>
  <style>
    :root { --bg:#0c1220; --card:#162033; --subtle:#0e1626; --border:rgba(148,163,184,0.18); --text:#e6edf7; --muted:#8ea0ba; --accent:#14b8a6; --accent-hover:#0d9488; --green:#22c55e; --danger:#ef4444; }
    * { box-sizing:border-box; }
    body { margin:0; min-height:100vh; font-family:"Segoe UI",system-ui,sans-serif; background:var(--bg); color:var(--text); display:flex; align-items:center; justify-content:center; padding:24px; }
    .box { max-width:760px; width:100%; background:var(--card); border:1px solid var(--border); border-radius:14px; padding:30px; box-shadow:0 16px 40px rgba(0,0,0,0.35); }
    .top-bar { display:flex; align-items:center; justify-content:space-between; flex-wrap:wrap; gap:10px; }
    .badge { display:inline-flex; align-items:center; gap:8px; background:rgba(34,197,94,0.14); color:var(--green); padding:5px 12px; border-radius:99px; font-size:12.5px; font-weight:600; }
    .dot { width:8px; height:8px; border-radius:50%; background:var(--green); box-shadow:0 0 8px var(--green); }
    h1 { margin:14px 0 6px; font-size:24px; }
    p { margin:0 0 16px; color:var(--muted); line-height:1.55; font-size:14px; }
    code { background:var(--subtle); border:1px solid var(--border); padding:2px 7px; border-radius:6px; font-family:Consolas,monospace; color:#38bdf8; font-size:12.5px; }
    .drop-card { background:var(--subtle); border:2px dashed rgba(20,184,166,0.45); border-radius:12px; padding:22px 20px; text-align:center; transition:all 0.18s ease; margin-top:12px; position:relative; }
    .drop-card.dragover { border-color:var(--green); background:rgba(20,184,166,0.12); transform:scale(1.01); box-shadow:0 0 24px rgba(20,184,166,0.22); }
    .drop-icon { font-size:32px; margin-bottom:6px; display:block; }
    .drop-title { font-size:16px; font-weight:600; color:var(--text); margin-bottom:4px; }
    .drop-sub { font-size:12.5px; color:var(--muted); margin-bottom:14px; }
    .drop-controls { display:flex; align-items:center; justify-content:center; gap:8px; flex-wrap:wrap; max-width:560px; margin:0 auto; }
    .slug-input { background:#162033; border:1px solid var(--border); color:var(--text); border-radius:8px; padding:8px 12px; font-size:13px; width:220px; outline:none; }
    .slug-input:focus { border-color:var(--accent); }
    .btn { display:inline-flex; align-items:center; gap:6px; background:var(--accent); color:#042f2e; font-weight:600; border:none; border-radius:8px; padding:8px 14px; font-size:13px; cursor:pointer; text-decoration:none; transition:background 0.15s; }
    .btn:hover { background:var(--accent-hover); color:#fff; }
    .btn.secondary { background:#1e293b; color:var(--text); border:1px solid var(--border); }
    .btn.secondary:hover { border-color:var(--accent); color:var(--accent); }
    .btn.danger { background:rgba(239,68,68,0.14); color:#fca5a5; border:1px solid rgba(239,68,68,0.3); padding:5px 10px; font-size:12px; }
    .btn.danger:hover { background:var(--danger); color:#fff; }
    .status-msg { margin-top:12px; padding:10px 14px; border-radius:8px; font-size:13px; display:none; align-items:center; justify-content:space-between; gap:10px; text-align:left; }
    .status-msg.ok { display:flex; background:rgba(34,197,94,0.14); border:1px solid rgba(34,197,94,0.35); color:#86efac; }
    .status-msg.err { display:flex; background:rgba(239,68,68,0.14); border:1px solid rgba(239,68,68,0.35); color:#fca5a5; }
    .trust-badge { display:inline-flex; align-items:center; gap:6px; background:rgba(20,184,166,0.15); border:1px solid rgba(20,184,166,0.35); color:#5eead4; padding:4px 10px; border-radius:99px; font-size:12px; font-weight:600; cursor:pointer; }
    .trust-modal-bg { position:fixed; inset:0; background:rgba(4,9,20,0.78); display:none; align-items:center; justify-content:center; z-index:999; padding:20px; }
    .trust-modal-bg.open { display:flex; }
    .trust-modal { background:var(--card); border:1px solid rgba(20,184,166,0.45); border-radius:14px; max-width:520px; width:100%; padding:24px; box-shadow:0 20px 50px rgba(0,0,0,0.6); text-align:left; }
    .trust-modal h3 { margin:0 0 8px; font-size:18px; color:var(--text); display:flex; align-items:center; gap:8px; }
    .trust-modal p { margin:0 0 12px; font-size:13px; color:var(--muted); }
    .trust-box { background:var(--subtle); border:1px solid var(--border); border-radius:10px; padding:12px 14px; margin-bottom:14px; font-size:12.5px; line-height:1.7; }
    .trust-chk { display:flex; align-items:flex-start; gap:8px; margin:8px 0; font-size:12.5px; color:var(--text); cursor:pointer; }
    .trust-actions { display:flex; justify-content:flex-end; gap:10px; margin-top:16px; }
    .prog-wrap { width:100%; background:#0b1322; height:7px; border-radius:99px; overflow:hidden; margin-top:6px; }
    .prog-bar { height:100%; background:var(--accent); width:0%; transition:width 0.15s; }
    .sites-panel { background:var(--subtle); border:1px solid var(--border); border-radius:10px; padding:16px 18px; margin-top:16px; }
    .sites-head { display:flex; align-items:center; justify-content:space-between; margin-bottom:10px; }
    .sites-head h3 { margin:0; font-size:13.5px; color:var(--accent); text-transform:uppercase; letter-spacing:0.04em; }
    .site-list { display:flex; flex-direction:column; gap:8px; }
    .site-row { display:flex; align-items:center; justify-content:space-between; gap:10px; background:#162033; border:1px solid var(--border); border-radius:8px; padding:9px 13px; }
    .site-info { display:flex; align-items:center; gap:10px; min-width:0; }
    .site-link { color:#38bdf8; font-weight:600; text-decoration:none; font-size:14px; overflow:hidden; text-overflow:ellipsis; white-space:nowrap; }
    .site-link:hover { text-decoration:underline; color:var(--accent); }
    .site-meta { color:var(--muted); font-size:12px; }
    .site-actions { display:flex; align-items:center; gap:6px; }
    .steps { background:var(--subtle); border:1px solid var(--border); border-radius:10px; padding:14px 18px; margin-top:14px; }
    .steps h3 { margin:0 0 8px; font-size:13px; color:var(--accent); text-transform:uppercase; letter-spacing:0.04em; }
    .steps ol { margin:0; padding-left:20px; color:var(--text); line-height:1.75; font-size:13.5px; }
    .foot { margin-top:20px; padding-top:14px; border-top:1px solid var(--border); display:flex; justify-content:space-between; font-size:12px; color:var(--muted); }
  </style>
</head>
<body>
  <div class="box">
    <div class="top-bar">
      <div style="display:flex;align-items:center;gap:8px;flex-wrap:wrap">
        <span class="badge"><span class="dot"></span> <span id="serverStatusText">Apache &amp; PHP Web Server is Running</span></span>
        <span class="trust-badge" id="trustStatusBadge" title="Click to toggle Auto-Trust Author &amp; Full Folder Permissions">🛡 Trust Author: Enabled (Full Permissions)</span>
      </div>
      <code id="wwwPathBadge">www/</code>
    </div>
    <h1>Darius DB (Lite Version 0.1.2)</h1>
    <p>Your local web server is active and serving websites directly from your portable <code>www/</code> directory.</p>

    <!-- Trust Author & Folder Permissions Modal -->
    <div class="trust-modal-bg" id="trustModal">
      <div class="trust-modal">
        <h3>🛡 Do you trust the authors of the files in this folder?</h3>
        <p>Darius DB Apache Web Server is about to grant local hosting permissions and deploy these files into <code>C:\DariusDB\www\</code>.</p>
        <div class="trust-box" id="trustSummaryBox"></div>
        <label class="trust-chk">
          <input type="checkbox" id="chkGrantPerms" checked />
          <span><strong>Trust Author &amp; Grant Full Permissions</strong> (Read, Write &amp; Script Execution in <code>www/</code>)</span>
        </label>
        <label class="trust-chk">
          <input type="checkbox" id="chkSkipHeavy" checked />
          <span>Skip heavy non-web cache folders (<code>.git/</code>, <code>node_modules/</code>, <code>.vs/</code>) for instant upload</span>
        </label>
        <label class="trust-chk">
          <input type="checkbox" id="chkAlwaysTrust" checked />
          <span>Remember my choice and automatically trust local authors &amp; permissions</span>
        </label>
        <div class="trust-actions">
          <button type="button" class="btn secondary" id="btnTrustCancel">Cancel</button>
          <button type="button" class="btn" id="btnTrustConfirm">🛡 Trust Author &amp; Deploy</button>
        </div>
      </div>
    </div>

    <!-- Drag & Drop Website Deployer -->
    <div class="drop-card" id="dropZone">
      <span class="drop-icon">🚀</span>
      <div class="drop-title">Drag &amp; Drop Your Website Folder, .zip Archive, or HTML Files Here</div>
      <div class="drop-sub">
        Drop any project folder (e.g. <code>my-site/</code>), a <code>.zip</code> website bundle, or <code>.html</code>/<code>.css</code>/<code>.js</code> files to publish immediately into <code>www/</code>
      </div>
      <div class="drop-controls">
        <input type="text" id="siteSlug" class="slug-input" placeholder="Site folder (auto from folder/zip)" title="Optional target subfolder name in www/" />
        <button type="button" class="btn" id="btnPickFolder">📁 Select Folder</button>
        <button type="button" class="btn secondary" id="btnPickFiles">📦 Select .zip / Files</button>
        <input type="file" id="inputFolder" webkitdirectory directory multiple hidden />
        <input type="file" id="inputFiles" multiple hidden />
      </div>
      <div class="status-msg" id="deployStatus"></div>
    </div>

    <!-- Hosted Websites in www/ -->
    <div class="sites-panel">
      <div class="sites-head">
        <h3>Hosted Websites &amp; Projects in www/</h3>
        <button type="button" class="btn secondary" style="padding:4px 10px;font-size:12px" id="btnRefreshSites">↻ Refresh</button>
      </div>
      <div class="site-list" id="siteList">
        <div class="site-meta">Loading hosted websites...</div>
      </div>
    </div>

    <div class="steps">
      <h3>How to Host Your Local Website</h3>
      <ol>
        <li><strong>Drag &amp; Drop</strong> your website project folder, <code>.zip</code> file, or <code>index.html</code> into the box above (or copy into <code>C:\DariusDB\www\</code>).</li>
        <li>Your project is automatically deployed into <code>www\&lt;site-name&gt;\</code>.</li>
        <li>Click <strong>Open Site ↗</strong> in the Hosted Websites list above to view <code>http://localhost:PORT/&lt;site-name&gt;/</code> live!</li>
      </ol>
    </div>

    <div class="foot">
      <span>Created by Darius Cumpio · MIT Open Source</span>
      <span>Darius DB (Lite Version 0.1.2)</span>
    </div>
  </div>

  <script>
    (() => {
      const dropZone = document.getElementById("dropZone");
      const siteSlug = document.getElementById("siteSlug");
      const inputFolder = document.getElementById("inputFolder");
      const inputFiles = document.getElementById("inputFiles");
      const deployStatus = document.getElementById("deployStatus");
      const siteList = document.getElementById("siteList");
      const wwwPathBadge = document.getElementById("wwwPathBadge");

      const esc = (s) => String(s || "").replace(/&/g,"&amp;").replace(/</g,"&lt;").replace(/>/g,"&gt;").replace(/"/g,"&quot;");
      const fmtSize = (b) => b < 1024 ? b + " B" : b < 1048576 ? (b/1024).toFixed(1) + " KB" : (b/1048576).toFixed(1) + " MB";

      function showStatus(html, ok = true) {
        deployStatus.className = "status-msg " + (ok ? "ok" : "err");
        deployStatus.innerHTML = html;
      }

      const srvLabel = document.getElementById("serverStatusText");
      if (srvLabel) {
        srvLabel.textContent = location.port === "9000"
          ? "PHP Web Server (:9000) is Running"
          : "Apache & PHP Web Server (:" + (location.port || "8080") + ") is Running";
      }

      // Smart JSON API caller that supports both Apache (:8080) and PHP (:9000) and automatically falls back if one port returns HTML
      async function apiFetch(path, opts) {
        const candidates = [path];
        if (location.port !== "8080") candidates.push("http://localhost:8080" + path);
        if (location.port !== "9000") candidates.push("http://localhost:9000" + path);
        let lastErr = null;
        for (const url of candidates) {
          try {
            const r = await fetch(url, opts);
            const txt = (await r.text()).trim();
            if (txt.startsWith("{")) {
              const data = JSON.parse(txt);
              if (!r.ok || data.error) throw new Error(data.error || ("HTTP " + r.status));
              return data;
            }
            lastErr = new Error("Deploy API on " + url + " returned non-JSON response");
          } catch (e) {
            lastErr = e;
          }
        }
        throw lastErr || new Error("Local web server deploy API is unreachable");
      }

      async function loadSites() {
        try {
          const data = await apiFetch("/__darius_www__/sites");
          if (data.wwwDir) wwwPathBadge.textContent = data.wwwDir;
          const sites = data.sites || [];
          if (!sites.length) {
            siteList.innerHTML = '<div class="site-meta">No project subfolders in <code>www/</code> yet — drag &amp; drop a folder, .zip, .php, or .html file above!</div>';
            return;
          }
          siteList.innerHTML = sites.map((s) =>
            '<div class="site-row">' +
              '<div class="site-info">' +
                '<span>' + (s.isDir ? "📁" : "📄") + '</span>' +
                '<div>' +
                  '<a class="site-link" href="' + esc(s.url) + '" target="_blank">' + esc(s.name) + '</a>' +
                  '<div class="site-meta">' + esc(s.url) + ' · ' + s.files + ' file' + (s.files === 1 ? "" : "s") + ' (' + fmtSize(s.bytes) + ')</div>' +
                '</div>' +
              '</div>' +
              '<div class="site-actions">' +
                '<a class="btn" style="padding:5px 11px;font-size:12px" href="' + esc(s.url) + '" target="_blank">Open Site ↗</a>' +
                '<button type="button" class="btn danger" data-del="' + esc(s.name) + '">✕</button>' +
              '</div>' +
            '</div>'
          ).join("");
        } catch {
          siteList.innerHTML = '<div class="site-meta">Place project folders inside <code>www/</code> and open <code>http://localhost:' + (location.port || 8080) + '/&lt;project&gt;/</code>.</div>';
        }
      }

      siteList.addEventListener("click", async (e) => {
        const btn = e.target.closest("[data-del]");
        if (!btn) return;
        const name = btn.dataset.del;
        if (!confirm('Remove "' + name + '" from www/?')) return;
        try {
          await apiFetch("/__darius_www__/delete", {
            method: "POST",
            headers: { "Content-Type": "application/json" },
            body: JSON.stringify({ name })
          });
          loadSites();
        } catch (err) {
          showStatus("<span>Error: " + esc(err.message) + "</span>", false);
        }
      });

      document.getElementById("btnRefreshSites").onclick = loadSites;
      document.getElementById("btnPickFolder").onclick = () => inputFolder.click();
      document.getElementById("btnPickFiles").onclick = () => inputFiles.click();

      inputFolder.onchange = () => {
        if (inputFolder.files.length) promptTrustAndDeploy(Array.from(inputFolder.files).map((f) => ({ file: f, relPath: f.webkitRelativePath || f.name })));
        inputFolder.value = "";
      };
      inputFiles.onchange = () => {
        if (inputFiles.files.length) promptTrustAndDeploy(Array.from(inputFiles.files).map((f) => ({ file: f, relPath: f.name })));
        inputFiles.value = "";
      };

      const SKIP_DIRS = new Set([".git", "node_modules", ".vs", ".idea", "__pycache__", ".next", "dist-cache"]);

      // Recursively read dropped folders and files via FileSystemEntry API
      async function readEntry(entry, prefix = "") {
        if (entry.isFile) {
          return new Promise((resolve) => {
            entry.file((f) => resolve([{ file: f, relPath: prefix + f.name }]), () => resolve([]));
          });
        }
        if (entry.isDirectory) {
          const reader = entry.createReader();
          const allEntries = [];
          const readBatch = () => new Promise((resolve) => {
            reader.readEntries((batch) => {
              if (!batch.length) return resolve();
              allEntries.push(...batch);
              readBatch().then(resolve);
            }, () => resolve());
          });
          await readBatch();
          const out = [];
          for (const child of allEntries) {
            const sub = await readEntry(child, prefix + entry.name + "/");
            out.push(...sub);
          }
          return out;
        }
        return [];
      }

      ["dragenter", "dragover"].forEach((ev) => {
        window.addEventListener(ev, (e) => {
          e.preventDefault();
          dropZone.classList.add("dragover");
        });
      });
      ["dragleave", "drop"].forEach((ev) => {
        window.addEventListener(ev, (e) => {
          e.preventDefault();
          if (ev === "drop" || e.relatedTarget === null) {
            dropZone.classList.remove("dragover");
          }
        });
      });

      window.addEventListener("drop", async (e) => {
        e.preventDefault();
        dropZone.classList.remove("dragover");
        const dt = e.dataTransfer;
        if (!dt) return;

        showStatus("<span>Scanning dropped folder &amp; verifying author permissions...</span>", true);
        const items = dt.items ? Array.from(dt.items) : [];
        const entries = items.map((it) => it.webkitGetAsEntry ? it.webkitGetAsEntry() : null).filter(Boolean);
        if (entries.length) {
          const collected = [];
          for (const ent of entries) {
            const sub = await readEntry(ent, "");
            collected.push(...sub);
          }
          if (collected.length) {
            await promptTrustAndDeploy(collected);
            return;
          }
        }
        if (dt.files && dt.files.length) {
          await promptTrustAndDeploy(Array.from(dt.files).map((f) => ({ file: f, relPath: f.name })));
        }
      });

      const trustModal = document.getElementById("trustModal");
      const trustSummaryBox = document.getElementById("trustSummaryBox");
      const chkGrantPerms = document.getElementById("chkGrantPerms");
      const chkSkipHeavy = document.getElementById("chkSkipHeavy");
      const chkAlwaysTrust = document.getElementById("chkAlwaysTrust");
      const trustStatusBadge = document.getElementById("trustStatusBadge");
      let pendingUploadItems = null;

      function isHeavyCachePath(relPath) {
        const parts = String(relPath || "").replace(/\\/g, "/").split("/");
        return parts.some((p) => SKIP_DIRS.has(p.toLowerCase()));
      }

      function detectTargetSite(items) {
        const manual = siteSlug.value.trim();
        if (manual) return manual;
        if (items.length === 1 && items[0].file.name.toLowerCase().endsWith(".zip")) {
          return items[0].file.name.replace(/\.zip$/i, "");
        }
        const firstRel = String(items[0].relPath || items[0].file.name).replace(/\\/g, "/").replace(/^\/+/, "");
        const parts = firstRel.split("/");
        if (parts.length > 1 && parts[0]) return parts[0];
        return items[0].file.name.replace(/\.[^.]+$/, "") || "my-site";
      }

      trustStatusBadge.onclick = () => {
        const cur = localStorage.getItem("darius_always_trust") === "1";
        const next = !cur;
        localStorage.setItem("darius_always_trust", next ? "1" : "0");
        updateTrustBadge();
      };

      function updateTrustBadge() {
        const always = localStorage.getItem("darius_always_trust") === "1";
        trustStatusBadge.textContent = always
          ? "🛡 Author Trusted: Auto-Allow (Full Permissions)"
          : "🛡 Trust Author: Ask Before Deploy";
      }
      updateTrustBadge();

      async function promptTrustAndDeploy(rawItems) {
        if (!rawItems || !rawItems.length) return;
        const folderName = detectTargetSite(rawItems);
        const filteredCount = rawItems.filter((it) => !isHeavyCachePath(it.relPath)).length;
        const heavyCount = rawItems.length - filteredCount;

        // Always open the Trust Author & Permissions dialog if > 200 files or if auto-trust is off
        if (localStorage.getItem("darius_always_trust") === "1" && rawItems.length <= 200) {
          await uploadFileList(rawItems, true, true);
          return;
        }

        pendingUploadItems = rawItems;
        trustSummaryBox.innerHTML =
          '<div><strong>Project / Folder:</strong> <code>' + esc(folderName) + '</code></div>' +
          '<div><strong>Destination:</strong> <code>C:\\DariusDB\\www\\' + esc(folderName) + '\\</code></div>' +
          '<div><strong>Total Files Detected:</strong> ' + rawItems.length.toLocaleString() + ' file(s)' +
          (heavyCount > 0 ? ' (' + filteredCount.toLocaleString() + ' web files + ' + heavyCount.toLocaleString() + ' in <code>.git</code>/<code>node_modules</code>)' : '') +
          '</div>' +
          '<div><strong>Permissions:</strong> Full Local Read / Write / Execute &amp; Author Trust</div>';
        trustModal.classList.add("open");
      }

      document.getElementById("btnTrustCancel").onclick = () => {
        trustModal.classList.remove("open");
        pendingUploadItems = null;
        showStatus("<span>Deployment cancelled.</span>", false);
      };

      document.getElementById("btnTrustConfirm").onclick = async () => {
        if (!chkGrantPerms.checked) {
          alert("Please check 'Trust Author & Grant Full Permissions' to deploy into www/.");
          return;
        }
        if (chkAlwaysTrust.checked) {
          localStorage.setItem("darius_always_trust", "1");
        } else {
          localStorage.setItem("darius_always_trust", "0");
        }
        updateTrustBadge();
        const items = pendingUploadItems;
        const skipHeavy = chkSkipHeavy.checked;
        trustModal.classList.remove("open");
        pendingUploadItems = null;
        if (items) {
          await uploadFileList(items, skipHeavy, true);
        }
      };

      async function uploadFileList(rawItems, skipHeavy = true, trusted = true) {
        let items = skipHeavy ? rawItems.filter((it) => !isHeavyCachePath(it.relPath)) : rawItems;
        if (!items.length) items = rawItems;
        if (!items.length) return;

        // Determine siteName & strip common outer directory once so all chunked batches write to the exact same site folder
        let targetSite = siteSlug.value.trim();
        let commonTop = "";
        let allHaveTop = true;
        for (const it of items) {
          const rel = String(it.relPath || it.file.name).replace(/\\/g, "/").replace(/^\/+/, "");
          const parts = rel.split("/");
          if (parts.length < 2) { allHaveTop = false; break; }
          if (!commonTop) commonTop = parts[0];
          else if (commonTop.toLowerCase() !== parts[0].toLowerCase()) { allHaveTop = false; break; }
        }
        if (!targetSite) {
          if (allHaveTop && commonTop) targetSite = commonTop;
          else if (items.length === 1) targetSite = items[0].file.name.replace(/\.[^.]+$/, "");
          else targetSite = "site-" + Date.now();
        }

        // Upload in safe chunks of 40 files per HTTP request so 13,000+ file folders never fail with 'Failed to fetch'
        const BATCH_SIZE = 40;
        const total = items.length;
        let uploaded = 0;
        let finalRes = null;

        try {
          for (let start = 0; start < total; start += BATCH_SIZE) {
            const chunk = items.slice(start, start + BATCH_SIZE);
            const pct = Math.round((uploaded / total) * 100);
            showStatus(
              '<div style="width:100%">' +
                '<div style="display:flex;justify-content:space-between"><span>🛡 Author Trusted · Deploying <strong>' + esc(targetSite) + '</strong> to <code>www/</code>...</span><span>' + uploaded.toLocaleString() + ' / ' + total.toLocaleString() + ' (' + pct + '%)</span></div>' +
                '<div class="prog-wrap"><div class="prog-bar" style="width:' + pct + '%"></div></div>' +
              '</div>',
              true
            );

            const fd = new FormData();
            fd.append("siteName", targetSite);
            fd.append("trusted", trusted ? "true" : "false");
            for (const it of chunk) {
              let rel = String(it.relPath || it.file.name).replace(/\\/g, "/").replace(/^\/+/, "");
              if (allHaveTop && commonTop && rel.toLowerCase().startsWith(commonTop.toLowerCase() + "/")) {
                rel = rel.slice(commonTop.length + 1);
              }
              fd.append("files[]", it.file, it.file.name);
              fd.append("paths[]", rel || it.file.name);
            }

            const res = await apiFetch("/__darius_www__/upload", { method: "POST", body: fd });
            finalRes = res;
            uploaded += chunk.length;
          }

          siteSlug.value = "";
          const siteUrl = (finalRes && finalRes.url) || ("/" + targetSite + "/");
          const siteDir = (finalRes && finalRes.site) || targetSite;
          showStatus(
            '<span>✓ 🛡 Trusted &amp; Deployed <strong>' + esc(siteDir) + '</strong> (' + total.toLocaleString() + ' file' + (total === 1 ? '' : 's') + ') with full permissions in <code>www/' + esc(siteDir) + '/</code></span>' +
            '<a class="btn" style="padding:5px 12px;font-size:12px" href="' + esc(siteUrl) + '" target="_blank">Open Site ↗</a>',
            true
          );
          await loadSites();
        } catch (err) {
          showStatus("<span>Deploy Error: " + esc(err.message) + "</span>", false);
        }
      }

      loadSites();
    })();
  </script>
</body>
</html>
`

func ensureWwwIndex(wwwDir string) {
	indexPath := filepath.Join(wwwDir, "index.html")
	if !fileExists(indexPath) {
		_ = writeIfMissing(indexPath, defaultWwwIndexHTML)
		return
	}
	// Automatically upgrade older Darius DB welcome page to the v3 Apache + PHP Drag & Drop dashboard
	if b, err := os.ReadFile(indexPath); err == nil {
		s := string(b)
		if strings.Contains(s, "Darius DB") && !strings.Contains(s, "darius-www-v3") {
			_ = os.WriteFile(indexPath, []byte(strings.ReplaceAll(defaultWwwIndexHTML, "\n", "\r\n")), 0o644)
		}
	}
}

func (a *apacheEngine) Prepare(ctx context.Context, p *Paths, inst Installation, s EngineSettings, logf func(string)) error {
	if err := writeIfMissing(a.ConfigFile(p, inst), apacheHttpdConf); err != nil {
		return err
	}
	if err := os.MkdirAll(p.Www, 0o755); err != nil {
		return err
	}
	ensureWwwIndex(p.Www)
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
	ensureWwwIndex(wwwDir)

	fs := http.FileServer(http.Dir(wwwDir))
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		w.Header().Set("Server", "Apache/2.4 (DariusDB-Lite/0.1.2)")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("X-Frame-Options", "SAMEORIGIN")
		w.Header().Set("Referrer-Policy", "strict-origin-when-cross-origin")
		w.Header().Set("X-XSS-Protection", "1; mode=block")

		// Built-in Drag & Drop Website Deployer API
		if strings.HasPrefix(r.URL.Path, "/__darius_www__/") {
			handleDariusWwwAPI(w, r, wwwDir, port)
			return
		}

		// Protect sensitive dotfiles and configuration files (.env, .git, .ht*, composer.lock)
		lowerPath := strings.ToLower(r.URL.Path)
		for _, seg := range strings.Split(lowerPath, "/") {
			if seg == ".env" || seg == ".git" || seg == "composer.lock" || strings.HasPrefix(seg, ".ht") {
				http.Error(w, "403 Forbidden — Access to sensitive configuration files is blocked by Darius DB Apache security policy.", http.StatusForbidden)
				return
			}
		}

		// Clean directory listing or index.php execution if requesting a directory
		relPath := filepath.FromSlash(strings.TrimPrefix(filepath.Clean("/"+r.URL.Path), "/"))
		fullPath := filepath.Join(wwwDir, relPath)
		if st, err := os.Stat(fullPath); err == nil && st.IsDir() {
			hasHTML := fileExists(filepath.Join(fullPath, "index.html")) || fileExists(filepath.Join(fullPath, "index.htm"))
			hasPHP := fileExists(filepath.Join(fullPath, "index.php"))
			if hasPHP && (!hasHTML || r.URL.Path != "/") {
				if !strings.HasSuffix(r.URL.Path, "/") {
					http.Redirect(w, r, r.URL.Path+"/", http.StatusMovedPermanently)
					return
				}
				servePHPFile(w, r, wwwDir, filepath.Join(fullPath, "index.php"), port)
				fmt.Fprintf(os.Stdout, "%s - \"%s %s\" [PHP] (%s)\n",
					time.Now().Format("2006-01-02 15:04:05"), r.Method, r.URL.Path, time.Since(start).Round(time.Microsecond))
				return
			}
			if !hasHTML {
				renderDirListing(w, r.URL.Path, fullPath)
				fmt.Fprintf(os.Stdout, "%s - \"%s %s\" 200 (%s)\n",
					time.Now().Format("2006-01-02 15:04:05"), r.Method, r.URL.Path, time.Since(start).Round(time.Microsecond))
				return
			}
		} else if err == nil && !st.IsDir() && strings.HasSuffix(strings.ToLower(fullPath), ".php") {
			servePHPFile(w, r, wwwDir, fullPath, port)
			fmt.Fprintf(os.Stdout, "%s - \"%s %s\" [PHP] (%s)\n",
				time.Now().Format("2006-01-02 15:04:05"), r.Method, r.URL.Path, time.Since(start).Round(time.Microsecond))
			return
		} else if err != nil {
			// URL Rewrite / Front-Controller fallback: route clean URLs in www/<site>/... to www/<site>/index.php
			trimmed := strings.TrimPrefix(r.URL.Path, "/")
			parts := strings.SplitN(trimmed, "/", 2)
			if len(parts) >= 1 && parts[0] != "" {
				siteFrontController := filepath.Join(wwwDir, parts[0], "index.php")
				if fileExists(siteFrontController) {
					servePHPFile(w, r, wwwDir, siteFrontController, port)
					fmt.Fprintf(os.Stdout, "%s - \"%s %s\" [PHP-Rewrite] (%s)\n",
						time.Now().Format("2006-01-02 15:04:05"), r.Method, r.URL.Path, time.Since(start).Round(time.Microsecond))
					return
				}
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

type WwwSiteInfo struct {
	Name  string `json:"name"`
	URL   string `json:"url"`
	IsDir bool   `json:"isDir"`
	Files int    `json:"files"`
	Bytes int64  `json:"bytes"`
}

var slugSanitizeRe = regexp.MustCompile(`[^a-zA-Z0-9_\-\.]+`)

func sanitizeSiteSlug(s string) string {
	s = strings.TrimSpace(s)
	s = slugSanitizeRe.ReplaceAllString(s, "-")
	s = strings.Trim(s, "-.")
	return s
}

func ListWwwSites(wwwDir string) []WwwSiteInfo {
	entries, err := os.ReadDir(wwwDir)
	if err != nil {
		return []WwwSiteInfo{}
	}
	sites := []WwwSiteInfo{}
	for _, e := range entries {
		name := e.Name()
		if strings.EqualFold(name, "index.html") || strings.HasPrefix(name, ".") {
			continue
		}
		full := filepath.Join(wwwDir, name)
		if e.IsDir() {
			count := 0
			var total int64
			_ = filepath.Walk(full, func(_ string, info os.FileInfo, err error) error {
				if err == nil && info != nil && !info.IsDir() {
					count++
					total += info.Size()
				}
				return nil
			})
			sites = append(sites, WwwSiteInfo{
				Name:  name,
				URL:   "/" + name + "/",
				IsDir: true,
				Files: count,
				Bytes: total,
			})
		} else if info, err := e.Info(); err == nil {
			sites = append(sites, WwwSiteInfo{
				Name:  name,
				URL:   "/" + name,
				IsDir: false,
				Files: 1,
				Bytes: info.Size(),
			})
		}
	}
	sort.Slice(sites, func(i, j int) bool {
		if sites[i].IsDir != sites[j].IsDir {
			return sites[i].IsDir
		}
		return strings.ToLower(sites[i].Name) < strings.ToLower(sites[j].Name)
	})
	return sites
}

func handleDariusWwwAPI(w http.ResponseWriter, r *http.Request, wwwDir string, port int) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	w.Header().Set("Access-Control-Allow-Methods", "GET, POST, OPTIONS")
	w.Header().Set("Access-Control-Allow-Headers", "*")
	w.Header().Set("Access-Control-Allow-Private-Network", "true")
	if r.Method == http.MethodOptions {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	writeJSON := func(code int, v interface{}) {
		w.WriteHeader(code)
		_ = json.NewEncoder(w).Encode(v)
	}

	switch r.URL.Path {
	case "/__darius_www__/sites":
		writeJSON(http.StatusOK, map[string]interface{}{
			"wwwDir": wwwDir,
			"port":   port,
			"sites":  ListWwwSites(wwwDir),
		})

	case "/__darius_www__/delete":
		if r.Method != http.MethodPost {
			writeJSON(http.StatusMethodNotAllowed, map[string]string{"error": "POST required"})
			return
		}
		var req struct {
			Name string `json:"name"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeJSON(http.StatusBadRequest, map[string]string{"error": err.Error()})
			return
		}
		name := filepath.Base(strings.TrimSpace(req.Name))
		if name == "" || name == "." || name == ".." || strings.EqualFold(name, "index.html") {
			writeJSON(http.StatusBadRequest, map[string]string{"error": "invalid site name"})
			return
		}
		target := filepath.Join(wwwDir, name)
		if err := os.RemoveAll(target); err != nil {
			writeJSON(http.StatusInternalServerError, map[string]string{"error": err.Error()})
			return
		}
		writeJSON(http.StatusOK, map[string]interface{}{"ok": true})

	case "/__darius_www__/upload":
		if r.Method != http.MethodPost {
			writeJSON(http.StatusMethodNotAllowed, map[string]string{"error": "POST required"})
			return
		}
		if err := r.ParseMultipartForm(128 << 20); err != nil {
			writeJSON(http.StatusBadRequest, map[string]string{"error": "cannot parse upload: " + err.Error()})
			return
		}
		siteName := sanitizeSiteSlug(r.FormValue("siteName"))
		files := r.MultipartForm.File["files"]
		if len(files) == 0 {
			files = r.MultipartForm.File["files[]"]
		}
		paths := r.MultipartForm.Value["paths"]
		if len(paths) == 0 {
			paths = r.MultipartForm.Value["paths[]"]
		}
		if len(files) == 0 {
			writeJSON(http.StatusBadRequest, map[string]string{"error": "no files uploaded"})
			return
		}

		// Case 1: Single .zip archive dropped
		if len(files) == 1 && strings.HasSuffix(strings.ToLower(files[0].Filename), ".zip") {
			if siteName == "" {
				base := strings.TrimSuffix(files[0].Filename, filepath.Ext(files[0].Filename))
				siteName = sanitizeSiteSlug(base)
			}
			if siteName == "" {
				siteName = "site-" + time.Now().Format("150405")
			}
			f, err := files[0].Open()
			if err != nil {
				writeJSON(http.StatusBadRequest, map[string]string{"error": err.Error()})
				return
			}
			defer f.Close()
			raw, err := io.ReadAll(f)
			if err != nil {
				writeJSON(http.StatusBadRequest, map[string]string{"error": err.Error()})
				return
			}
			count, err := extractSiteZipBytes(raw, filepath.Join(wwwDir, siteName))
			if err != nil {
				writeJSON(http.StatusBadRequest, map[string]string{"error": err.Error()})
				return
			}
			writeJSON(http.StatusOK, map[string]interface{}{
				"ok":    true,
				"site":  siteName,
				"url":   "/" + siteName + "/",
				"count": count,
			})
			return
		}

		// Case 2: Dropped folder or multiple files
		// Detect common root folder from relative paths if siteName was not explicitly specified
		commonTop := ""
		allHaveTop := true
		for i := range files {
			rel := files[i].Filename
			if i < len(paths) && paths[i] != "" {
				rel = paths[i]
			}
			rel = strings.TrimLeft(filepath.ToSlash(rel), "/")
			parts := strings.SplitN(rel, "/", 2)
			if len(parts) < 2 {
				allHaveTop = false
				break
			}
			if commonTop == "" {
				commonTop = parts[0]
			} else if !strings.EqualFold(commonTop, parts[0]) {
				allHaveTop = false
				break
			}
		}

		if siteName == "" {
			if allHaveTop && commonTop != "" {
				siteName = sanitizeSiteSlug(commonTop)
			} else if len(files) == 1 {
				ext := strings.ToLower(filepath.Ext(files[0].Filename))
				base := strings.TrimSuffix(files[0].Filename, filepath.Ext(files[0].Filename))
				if (ext == ".html" || ext == ".htm") && !strings.EqualFold(base, "index") {
					siteName = sanitizeSiteSlug(base)
				} else {
					siteName = "site-" + time.Now().Format("150405")
				}
			} else {
				siteName = "site-" + time.Now().Format("150405")
			}
		}

		destSiteDir := filepath.Join(wwwDir, siteName)
		if err := os.MkdirAll(destSiteDir, 0o755); err != nil {
			writeJSON(http.StatusInternalServerError, map[string]string{"error": err.Error()})
			return
		}

		count := 0
		for i, fh := range files {
			rel := fh.Filename
			if i < len(paths) && paths[i] != "" {
				rel = paths[i]
			}
			rel = strings.TrimLeft(filepath.ToSlash(rel), "/")
			if allHaveTop && commonTop != "" && strings.HasPrefix(rel, commonTop+"/") {
				rel = strings.TrimPrefix(rel, commonTop+"/")
			}
			// If a single non-index HTML file was dropped, save it as index.html inside www/<siteName>/
			if len(files) == 1 && (strings.HasSuffix(strings.ToLower(rel), ".html") || strings.HasSuffix(strings.ToLower(rel), ".htm")) {
				rel = "index.html"
			}
			cleanRel := filepath.Clean(filepath.FromSlash(rel))
			if cleanRel == "." || strings.HasPrefix(cleanRel, "..") {
				continue
			}
			targetPath := filepath.Join(destSiteDir, cleanRel)
			if !strings.HasPrefix(strings.ToLower(filepath.Clean(targetPath)), strings.ToLower(filepath.Clean(destSiteDir))) {
				continue
			}
			if err := os.MkdirAll(filepath.Dir(targetPath), 0o755); err != nil {
				continue
			}
			src, err := fh.Open()
			if err != nil {
				continue
			}
			out, err := os.Create(targetPath)
			if err != nil {
				src.Close()
				continue
			}
			_, _ = io.Copy(out, src)
			out.Close()
			src.Close()
			_ = os.Chmod(targetPath, 0o777)
			count++
		}
		_ = os.Chmod(destSiteDir, 0o777)

		writeJSON(http.StatusOK, map[string]interface{}{
			"ok":    true,
			"site":  siteName,
			"url":   "/" + siteName + "/",
			"count": count,
		})

	default:
		writeJSON(http.StatusNotFound, map[string]string{"error": "unknown endpoint"})
	}
}

func extractSiteZipBytes(raw []byte, destDir string) (int, error) {
	zr, err := zip.NewReader(bytes.NewReader(raw), int64(len(raw)))
	if err != nil {
		return 0, fmt.Errorf("invalid .zip archive: %w", err)
	}
	return extractZipReaderToSite(zr, destDir)
}

func extractZipReaderToSite(zr *zip.Reader, destDir string) (int, error) {
	// Detect if all files share a single top-level directory inside the zip
	commonTop := ""
	allHaveTop := true
	for _, f := range zr.File {
		name := strings.Trim(filepath.ToSlash(f.Name), "/")
		if name == "" {
			continue
		}
		parts := strings.SplitN(name, "/", 2)
		if len(parts) < 2 {
			if !f.FileInfo().IsDir() {
				allHaveTop = false
				break
			}
			continue
		}
		if commonTop == "" {
			commonTop = parts[0]
		} else if !strings.EqualFold(commonTop, parts[0]) {
			allHaveTop = false
			break
		}
	}

	if err := os.MkdirAll(destDir, 0o755); err != nil {
		return 0, err
	}
	cleanDest := strings.ToLower(filepath.Clean(destDir))
	count := 0
	for _, f := range zr.File {
		if f.FileInfo().IsDir() {
			continue
		}
		rel := strings.TrimLeft(filepath.ToSlash(f.Name), "/")
		if allHaveTop && commonTop != "" && strings.HasPrefix(rel, commonTop+"/") {
			rel = strings.TrimPrefix(rel, commonTop+"/")
		}
		cleanRel := filepath.Clean(filepath.FromSlash(rel))
		if cleanRel == "." || strings.HasPrefix(cleanRel, "..") {
			continue
		}
		target := filepath.Join(destDir, cleanRel)
		if !strings.HasPrefix(strings.ToLower(filepath.Clean(target)), cleanDest) {
			continue
		}
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			return count, err
		}
		rc, err := f.Open()
		if err != nil {
			return count, err
		}
		out, err := os.Create(target)
		if err != nil {
			rc.Close()
			return count, err
		}
		_, cerr := io.Copy(out, rc)
		out.Close()
		rc.Close()
		if cerr != nil {
			return count, cerr
		}
		count++
	}
	return count, nil
}

// DeployWebsitePath deploys a local .zip archive, project directory, or .html file into www/<siteName>/.
func DeployWebsitePath(wwwDir, srcPath, siteName string) (string, int, error) {
	srcPath = strings.TrimSpace(srcPath)
	st, err := os.Stat(srcPath)
	if err != nil {
		return "", 0, err
	}
	siteName = sanitizeSiteSlug(siteName)
	if siteName == "" {
		base := filepath.Base(srcPath)
		if !st.IsDir() {
			base = strings.TrimSuffix(base, filepath.Ext(base))
		}
		siteName = sanitizeSiteSlug(base)
		if siteName == "" || strings.EqualFold(siteName, "index") {
			siteName = "site-" + time.Now().Format("150405")
		}
	}
	destDir := filepath.Join(wwwDir, siteName)

	if st.IsDir() {
		count := 0
		err := filepath.Walk(srcPath, func(path string, info os.FileInfo, err error) error {
			if err != nil || info == nil || info.IsDir() {
				return err
			}
			rel, err := filepath.Rel(srcPath, path)
			if err != nil {
				return err
			}
			target := filepath.Join(destDir, rel)
			if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
				return err
			}
			in, err := os.Open(path)
			if err != nil {
				return err
			}
			defer in.Close()
			out, err := os.Create(target)
			if err != nil {
				return err
			}
			_, cerr := io.Copy(out, in)
			out.Close()
			if cerr == nil {
				count++
			}
			return cerr
		})
		return siteName, count, err
	}

	ext := strings.ToLower(filepath.Ext(srcPath))
	if ext == ".zip" {
		zr, err := zip.OpenReader(srcPath)
		if err != nil {
			return "", 0, err
		}
		defer zr.Close()
		count, err := extractZipReaderToSite(&zr.Reader, destDir)
		return siteName, count, err
	}
	if ext == ".html" || ext == ".htm" {
		if err := os.MkdirAll(destDir, 0o755); err != nil {
			return "", 0, err
		}
		b, err := os.ReadFile(srcPath)
		if err != nil {
			return "", 0, err
		}
		if err := os.WriteFile(filepath.Join(destDir, "index.html"), b, 0o644); err != nil {
			return "", 0, err
		}
		return siteName, 1, nil
	}
	return "", 0, errors.New("unsupported file type — choose a .zip website archive, project folder, or .html file")
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

func findPHPExecutables() (phpCgi string, phpCli string, phpIni string) {
	root := ResolveRoot()
	paths := NewPaths(root)
	phpIni = filepath.Join(paths.EngineEtc("php"), "php.ini")
	_ = writeIfMissing(phpIni, defaultPhpIni)
	dirs := []string{}
	// Prioritize the active PHP version selected in Darius DB settings
	activePhp := LoadSettings(root).Engine("php").Version
	if activePhp != "" {
		if inst, ok := FindInstallation(paths, "php", activePhp); ok {
			dirs = append(dirs, inst.Dir, filepath.Join(inst.Dir, "bin"))
		}
	}
	for _, inst := range ListInstallations(paths, "php") {
		dirs = append(dirs, inst.Dir, filepath.Join(inst.Dir, "bin"))
	}
	if sysInst, ok := detectSystemPHP(); ok {
		dirs = append(dirs, sysInst.Dir, filepath.Join(sysInst.Dir, "bin"))
	}
	for _, d := range dirs {
		if phpCgi == "" && fileExists(filepath.Join(d, "php-cgi.exe")) {
			phpCgi = filepath.Join(d, "php-cgi.exe")
		}
		if phpCli == "" && fileExists(filepath.Join(d, "php.exe")) {
			phpCli = filepath.Join(d, "php.exe")
		}
		if phpCgi != "" && phpCli != "" {
			break
		}
	}
	return phpCgi, phpCli, phpIni
}

// resolveActiveMySQLFamilySettings detects the active/running MariaDB or MySQL port & credentials for PHP PDO / mysqli.
func resolveActiveMySQLFamilySettings() (dbPort int, dbUser, dbPass string, mariaPort, mysqlPort int) {
	st := LoadSettings(ResolveRoot())
	maria := st.Engine("mariadb")
	mysql := st.Engine("mysql")
	mariaPort = maria.Port
	if mariaPort <= 0 {
		mariaPort = 3306
	}
	mysqlPort = mysql.Port
	if mysqlPort <= 0 {
		mysqlPort = 3307
	}
	// Prefer whichever of MariaDB or MySQL is actively listening right now
	if portInUse(mariaPort) {
		u := maria.User
		if u == "" {
			u = "root"
		}
		return mariaPort, u, maria.Password, mariaPort, mysqlPort
	}
	if portInUse(mysqlPort) {
		u := mysql.User
		if u == "" {
			u = "root"
		}
		return mysqlPort, u, mysql.Password, mariaPort, mysqlPort
	}
	u := maria.User
	if u == "" {
		u = "root"
	}
	return mariaPort, u, maria.Password, mariaPort, mysqlPort
}

func servePHPFile(w http.ResponseWriter, r *http.Request, wwwDir, scriptPath string, port int) {
	phpCgi, phpCli, phpIni := findPHPExecutables()
	if phpCgi == "" && phpCli == "" {
		http.Error(w, "PHP is not installed yet. Install PHP from the Versions tab in Darius DB to run .php websites.", http.StatusServiceUnavailable)
		return
	}
	dbPort, dbUser, dbPass, mariaPort, mysqlPort := resolveActiveMySQLFamilySettings()

	if phpCgi != "" {
		args := []string{
			"-d", "cgi.force_redirect=0",
			"-d", fmt.Sprintf("mysqli.default_port=%d", dbPort),
			"-d", "mysqli.default_host=127.0.0.1",
			"-d", "mysqli.default_user=" + dbUser,
		}
		if fileExists(phpIni) {
			args = append(args, "-c", phpIni)
		}
		extDir := filepath.Join(filepath.Dir(phpCgi), "ext")
		if dirExists(extDir) {
			args = append(args, "-d", "extension_dir="+extDir)
		}
		cmd := exec.Command(phpCgi, args...)
		hideWindow(cmd)
		cmd.Dir = filepath.Dir(scriptPath)

		scriptName := r.URL.Path
		if strings.HasSuffix(scriptName, "/") {
			scriptName += "index.php"
		}
		env := append(os.Environ(),
			"REDIRECT_STATUS=200",
			"GATEWAY_INTERFACE=CGI/1.1",
			"SERVER_PROTOCOL=HTTP/1.1",
			"SERVER_SOFTWARE=Apache/2.4 (DariusDB-Lite/0.1.2)",
			"SERVER_NAME=localhost",
			"SERVER_ADDR=127.0.0.1",
			"SERVER_PORT="+strconv.Itoa(port),
			"REMOTE_ADDR=127.0.0.1",
			"DOCUMENT_ROOT="+wwwDir,
			"SCRIPT_FILENAME="+scriptPath,
			"SCRIPT_NAME="+scriptName,
			"REQUEST_URI="+r.URL.RequestURI(),
			"QUERY_STRING="+r.URL.RawQuery,
			"REQUEST_METHOD="+r.Method,
			"CONTENT_TYPE="+r.Header.Get("Content-Type"),
			"CONTENT_LENGTH="+strconv.FormatInt(maxInt64(r.ContentLength, 0), 10),
			"DB_CONNECTION=mysql",
			"DB_HOST=127.0.0.1",
			"DB_PORT="+strconv.Itoa(dbPort),
			"DB_USERNAME="+dbUser,
			"DB_PASSWORD="+dbPass,
			"MARIADB_HOST=127.0.0.1",
			"MARIADB_PORT="+strconv.Itoa(mariaPort),
			"MYSQL_HOST=127.0.0.1",
			"MYSQL_PORT="+strconv.Itoa(mysqlPort),
			"MAIL_HOST=127.0.0.1",
			"MAIL_PORT=1025",
		)
		for k, vals := range r.Header {
			hk := "HTTP_" + strings.ToUpper(strings.ReplaceAll(k, "-", "_"))
			env = append(env, hk+"="+strings.Join(vals, ", "))
		}
		cmd.Env = env
		if r.Body != nil {
			cmd.Stdin = r.Body
		}

		out, err := cmd.Output()
		if err != nil && len(out) == 0 {
			var ee *exec.ExitError
			if errors.As(err, &ee) && len(ee.Stderr) > 0 {
				http.Error(w, "PHP Error:\n"+string(ee.Stderr), http.StatusInternalServerError)
				return
			}
			http.Error(w, "PHP CGI Error: "+err.Error(), http.StatusInternalServerError)
			return
		}

		// Split CGI headers and body
		sep := []byte("\r\n\r\n")
		idx := bytes.Index(out, sep)
		if idx == -1 {
			sep = []byte("\n\n")
			idx = bytes.Index(out, sep)
		}
		statusCode := http.StatusOK
		body := out
		if idx != -1 {
			hdrBlock := string(out[:idx])
			body = out[idx+len(sep):]
			for _, line := range strings.Split(strings.ReplaceAll(hdrBlock, "\r\n", "\n"), "\n") {
				line = strings.TrimSpace(line)
				if line == "" {
					continue
				}
				parts := strings.SplitN(line, ":", 2)
				if len(parts) != 2 {
					continue
				}
				hk := strings.TrimSpace(parts[0])
				hv := strings.TrimSpace(parts[1])
				if strings.EqualFold(hk, "Status") {
					fields := strings.Fields(hv)
					if len(fields) > 0 {
						if code, err := strconv.Atoi(fields[0]); err == nil && code >= 100 {
							statusCode = code
						}
					}
					continue
				}
				w.Header().Add(hk, hv)
			}
		}
		if w.Header().Get("Content-Type") == "" {
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
		}
		w.WriteHeader(statusCode)
		_, _ = w.Write(body)
		return
	}

	// Fallback to php.exe CLI if php-cgi.exe is not present
	args := []string{}
	if fileExists(phpIni) {
		args = append(args, "-c", phpIni)
	}
	extDir := filepath.Join(filepath.Dir(phpCli), "ext")
	if dirExists(extDir) {
		args = append(args, "-d", "extension_dir="+extDir)
	}
	args = append(args, scriptPath)
	cmd := exec.Command(phpCli, args...)
	hideWindow(cmd)
	cmd.Dir = filepath.Dir(scriptPath)
	out, _ := cmd.CombinedOutput()
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = w.Write(out)
}

func maxInt64(a, b int64) int64 {
	if a > b {
		return a
	}
	return b
}
