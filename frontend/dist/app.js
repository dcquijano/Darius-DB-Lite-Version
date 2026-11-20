(() => {
  const api = () => window.go.main.App;
  const rt = () => window.runtime;

  const SYSTEM_DBS = new Set([
    "mysql", "information_schema", "performance_schema", "sys",
    "postgres", "template0", "template1",
    "master", "tempdb", "model", "msdb"
  ]);

  const state = {
    tab: "dashboard",
    info: null,
    settings: null,
    statuses: [],
    catalog: {},
    downloads: {}, // key "engine@version" -> Progress
    dbEngine: "mariadb",
    sqlDb: "",
    sqlDbs: [],
    sqlTables: [],
    sqlTable: "",
    sqlColumns: [],
    sqlLastResult: null,
    sqlShowingAll: false,
    redisDb: 0,
    redisCursor: "0",
    redisKeys: [],
    redisSelectedKey: "",
    verEngine: "mariadb",
    cfgEngine: "mariadb",
    cfgMode: "config",
    logTimer: null,
    termInitialized: false,
    termState: { mode: "shell", prompt: "C:\\>", busy: false },
    termHistory: [],
    termHistIdx: -1,
  };

  const $ = (id) => document.getElementById(id);
  const esc = (s) =>
    String(s ?? "")
      .replace(/&/g, "&amp;")
      .replace(/</g, "&lt;")
      .replace(/>/g, "&gt;")
      .replace(/"/g, "&quot;")
      .replace(/'/g, "&#39;");

  const errMsg = (e) => (typeof e === "string" ? e : e?.message || String(e));

  function toast(msg, kind = "info") {
    const el = document.createElement("div");
    el.className = `toast ${kind}`;
    el.textContent = msg;
    $("toasts").appendChild(el);
    setTimeout(() => el.remove(), kind === "err" ? 5500 : 3200);
  }

  async function copyText(text) {
    if (rt()?.ClipboardSetText) {
      await rt().ClipboardSetText(text);
    } else if (navigator.clipboard?.writeText) {
      await navigator.clipboard.writeText(text);
    }
  }

  function formatBytes(n) {
    if (!n || n < 0) return "0 B";
    const units = ["B", "KB", "MB", "GB"];
    let i = 0;
    let v = n;
    while (v >= 1024 && i < units.length - 1) {
      v /= 1024;
      i++;
    }
    return `${v.toFixed(i === 0 ? 0 : 1)} ${units[i]}`;
  }

  function applyTheme(theme) {
    const root = document.documentElement;
    if (theme === "dark") {
      root.setAttribute("data-theme", "dark");
      rt()?.WindowSetDarkTheme?.();
    } else if (theme === "light") {
      root.setAttribute("data-theme", "light");
      rt()?.WindowSetLightTheme?.();
    } else {
      root.removeAttribute("data-theme");
      rt()?.WindowSetSystemDefaultTheme?.();
    }
  }

  function onEvent(name, cb) {
    const r = rt();
    if (r?.EventsOnMultiple) return r.EventsOnMultiple(name, cb, -1);
    if (r?.EventsOn) return r.EventsOn(name, cb);
  }

  function getStatus(engine) {
    return state.statuses.find((s) => s.engine === engine);
  }

  // ---- Confirm modal ----
  let confirmResolve = null;
  function askConfirm(title, message, okLabel = "Confirm") {
    $("confirmTitle").textContent = title;
    $("confirmMsg").textContent = message;
    $("btnConfirmOk").textContent = okLabel;
    $("modalConfirm").hidden = false;
    return new Promise((resolve) => {
      confirmResolve = resolve;
    });
  }
  function closeConfirm(val) {
    $("modalConfirm").hidden = true;
    if (confirmResolve) {
      const r = confirmResolve;
      confirmResolve = null;
      r(val);
    }
  }

  // ---- Navigation ----
  function switchTab(tab) {
    state.tab = tab;
    document.querySelectorAll(".nav-item").forEach((b) =>
      b.classList.toggle("active", b.dataset.tab === tab)
    );
    document.querySelectorAll(".view").forEach((v) =>
      v.classList.toggle("active", v.id === `view-${tab}`)
    );

    stopLogPolling();

    if (tab === "database") {
      pickBestDbEngine();
      renderDbEngineTabs();
      loadDbView();
    } else if (tab === "versions") {
      renderVerEngineTabs();
      renderVersions();
    } else if (tab === "config") {
      renderCfgEngineTabs();
      loadCfgOrLog();
    } else if (tab === "terminal") {
      initTerminalView();
    } else if (tab === "settings") {
      loadSettingsForm();
    }
  }

  function pickBestDbEngine() {
    const cur = getStatus(state.dbEngine);
    if (cur && cur.engine !== "apache" && cur.engine !== "php" && cur.state === "running") return;
    const running = state.statuses.find(
      (s) => s.engine !== "apache" && s.engine !== "php" && s.engine !== "mailpit" && s.state === "running"
    );
    if (running) state.dbEngine = running.engine;
    else if (state.dbEngine === "apache" || state.dbEngine === "php") state.dbEngine = "mariadb";
  }

  // ---- Dashboard (5th-Picture Unified Row Layout: Apache + PHP Unified, Separate MariaDB & MySQL) ----
  function renderDashboard() {
    const phpSvc = state.statuses.find((s) => s.engine === "php");
    const apacheSvc = state.statuses.find((s) => s.engine === "apache");
    const mariaSvc = state.statuses.find((s) => s.engine === "mariadb");
    const mysqlSvc = state.statuses.find((s) => s.engine === "mysql");

    // Keep MariaDB and MySQL as separate rows (only PHP is embedded inside the Apache row)
    const visibleStatuses = state.statuses.filter((s) => s.engine !== "php");
    const runningCount = visibleStatuses.filter((s) => s.state === "running").length;
    const badge = $("runningBadge");
    badge.textContent = runningCount;
    badge.hidden = runningCount === 0;

    const anyInstalled = state.statuses.some((s) => s.installations && s.installations.length > 0);
    $("welcomeBanner").hidden = anyInstalled;

    renderActiveDownloads();

    const rowsHtml = visibleStatuses
      .map((s) => {
        const isApache = s.engine === "apache";
        const isMailpit = s.engine === "mailpit";
        const installed = s.installations && s.installations.length > 0;
        const busy = s.state === "starting" || s.state === "stopping";
        const running = s.state === "running";

        // Toggle switch on far left (exact match to 5th picture)
        const toggleAct = !installed ? "install" : running ? "stop" : "start";
        const toggleTitle = !installed
          ? `Click to install ${s.name}`
          : running
            ? `Stop ${s.name}`
            : `Start ${s.name}`;
        const toggleBtn = `<button type="button" class="svc-toggle ${s.state}" data-act="${toggleAct}" data-engine="${s.engine}" ${busy ? "disabled" : ""} title="${esc(toggleTitle)}"></button>`;

        // Version display / dropdown
        const cleanVer = (name, prefix) => {
          if (!name) return "";
          return String(name).replace(new RegExp("^" + prefix + "-?", "i"), "").replace(/^lite-/i, "");
        };

        let verSelect = "";
        if (installed) {
          if (s.installations.length > 1) {
            verSelect = `<select class="select sm svc-ver" data-engine="${s.engine}" ${busy ? "disabled" : ""} title="Select ${esc(s.name)} version">
              ${s.installations
                .map(
                  (inst) =>
                    `<option value="${esc(inst.name)}" ${inst.name === s.active ? "selected" : ""}>${esc(inst.name)}</option>`
                )
                .join("")}
            </select>`;
          } else {
            const verNum = cleanVer(s.active, s.engine) || s.installations[0].version || s.active;
            verSelect = `<span class="muted" style="font-size:13.5px;font-weight:500">${esc(verNum)}</span>`;
          }
        } else {
          verSelect = `<span class="muted" style="font-size:12px">(not installed)</span>`;
        }

        // Integrated PHP selector right inside the Apache row ("PHP 8.x with Apache 2.4")
        let phpIntegrationHtml = "";
        if (isApache && phpSvc) {
          const phpInstalled = phpSvc.installations && phpSvc.installations.length > 0;
          if (phpInstalled) {
            const phpOptions = phpSvc.installations
              .map(
                (inst) =>
                  `<option value="${esc(inst.name)}" ${inst.name === phpSvc.active ? "selected" : ""}>with PHP ${esc(inst.version || cleanVer(inst.name, "php"))}</option>`
              )
              .join("");
            phpIntegrationHtml = `<select class="select sm svc-ver" data-engine="php" style="border-color:rgba(20,184,166,0.45);color:var(--accent);font-weight:600" title="Active PHP version running inside Apache 2.4">${phpOptions}</select>`;
          } else {
            phpIntegrationHtml = `<button type="button" class="btn xs accent" data-act="install" data-engine="php" title="Install PHP 8.4 / 8.3 / 8.2 for Apache">+ Install PHP</button>`;
          }
        }

        // Quick icon next to service title (5th picture: open padlock next to Apache, blue envelope next to Mailpit)
        let titleIconBtn = "";
        if (isApache) {
          titleIconBtn = `<button type="button" class="svc-icon-link" data-act="opensite" data-engine="apache" data-port="${s.port}" title="Open Apache + PHP Web Root (http://localhost:${s.port}/)">
            <svg viewBox="0 0 24 24"><rect x="3" y="11" width="18" height="11" rx="2" ry="2"/><path d="M7 11V7a5 5 0 0 1 9.9-1"/></svg>
          </button>`;
        } else if (isMailpit) {
          titleIconBtn = `<button type="button" class="svc-icon-link mail-blue" data-act="opensite" data-engine="mailpit" data-port="${s.port}" title="Open Mailpit Web Inbox (http://localhost:${s.port}/)">
            <svg viewBox="0 0 24 24"><path d="M4 4h16c1.1 0 2 .9 2 2v12c0 1.1-.9 2-2 2H4c-1.1 0-2-.9-2-2V6c0-1.1.9-2 2-2z"/><polyline points="22,6 12,13 2,6"/></svg>
          </button>`;
        } else if (installed && (s.isSql || s.engine === "redis")) {
          titleIconBtn = `<button type="button" class="svc-icon-link" data-act="browse" data-engine="${s.engine}" title="Open ${esc(s.name)} Browser">
            <svg viewBox="0 0 24 24"><ellipse cx="12" cy="5" rx="8" ry="3"/><path d="M4 5v6c0 1.66 3.58 3 8 3s8-1.34 8-3V5"/><path d="M4 11v6c0 1.66 3.58 3 8 3s8-1.34 8-3v-6"/></svg>
          </button>`;
        }

        const displayName = isApache ? "Apache" : s.name;
        const portDisplay = isMailpit ? `1025/${s.port}` : `${s.port}`;

        // Right column: Reload (just like 5th picture) + subtle quick actions
        let rightActions = "";
        if (!installed) {
          rightActions = `<button class="btn xs primary" data-act="install" data-engine="${s.engine}">Install</button>`;
        } else {
          const reloadBtn = `<button type="button" class="svc-reload-link" data-act="${running ? "restart" : "start"}" data-engine="${s.engine}" ${busy ? "disabled" : ""} title="${running ? "Reload " + esc(s.name) : "Start " + esc(s.name)}">Reload</button>`;
          if (isApache) {
            rightActions = `
              ${reloadBtn}
              <button class="btn xs accent" data-act="opensite" data-engine="apache" data-port="${s.port}">Open Web ↗</button>
              <button class="btn xs" data-act="deploysite" data-engine="apache" title="Deploy website .zip or index.html to www/">Deploy .zip</button>
              <button class="btn xs ghost" data-act="data" data-engine="apache" title="Open www/ folder">www/</button>
              <button class="btn xs ghost" data-act="config" data-engine="apache" title="Edit Apache httpd.conf">httpd.conf</button>
              <button class="btn xs ghost" data-act="config" data-engine="php" title="Edit PHP php.ini (OPcache, PDO, mysqli, Mailpit)">php.ini</button>
              <button class="btn xs ghost" data-act="logs" data-engine="apache">Logs</button>
              <button class="btn xs ghost" data-act="repair" data-engine="apache">Repair</button>`;
          } else if (isMailpit) {
            rightActions = `
              ${reloadBtn}
              ${running ? `<button class="btn xs accent" data-act="opensite" data-engine="mailpit" data-port="${s.port}">Inbox ↗</button>` : ""}
              <button class="btn xs ghost" data-act="uri" data-engine="mailpit" title="Copy SMTP &amp; Web Inbox URL">SMTP</button>
              <button class="btn xs ghost" data-act="config" data-engine="mailpit">Config</button>
              <button class="btn xs ghost" data-act="logs" data-engine="mailpit">Logs</button>
              <button class="btn xs ghost" data-act="repair" data-engine="mailpit">Repair</button>`;
          } else {
            rightActions = `
              ${reloadBtn}
              ${running ? `<button class="btn xs accent" data-act="browse" data-engine="${s.engine}">Browse</button>` : ""}
              <button class="btn xs ghost" data-act="uri" data-engine="${s.engine}" title="Copy connection string">URI</button>
              <button class="btn xs ghost" data-act="config" data-engine="${s.engine}">Config</button>
              <button class="btn xs ghost" data-act="logs" data-engine="${s.engine}">Logs</button>
              <button class="btn xs ghost" data-act="data" data-engine="${s.engine}">Data</button>
              <button class="btn xs ghost" data-act="repair" data-engine="${s.engine}">Repair</button>`;
          }
        }

        return `
        <div class="svc-row ${s.state}">
          <div class="svc-row-left">
            ${toggleBtn}
            <div class="svc-name-wrap">
              <span class="svc-name-label">${esc(displayName)}</span>
              ${verSelect}
              ${phpIntegrationHtml}
              ${titleIconBtn}
              ${s.engine === "mssql" && s.serverName ? `<span class="meta-tag" title="MS SQL Server Name (Windows PC User)">Server: ${esc(s.serverName)}</span>` : ""}
              ${s.portBusy && !running ? `<span class="pill warn" title="Port ${s.port} is in use">Port busy</span>` : ""}
            </div>
          </div>
          <div class="svc-row-port">
            <span>${esc(portDisplay)}</span>
          </div>
          <div class="svc-row-right">
            ${rightActions}
          </div>
          ${s.message ? `<div class="svc-err" style="grid-column:1 / -1;margin-top:4px">${esc(s.message)}</div>` : ""}
        </div>`;
      })
      .join("");

    const activePhpVer = phpSvc?.active ? phpSvc.active.replace(/^php-?/i, "") : "8.4.27";
    const activeApacheVer = apacheSvc?.active ? apacheSvc.active.replace(/^apache-?(lite-)?/i, "") : "2.4";
    const activeDbPort =
      mariaSvc?.state === "running"
        ? mariaSvc.port
        : mysqlSvc?.state === "running"
          ? mysqlSvc.port
          : mariaSvc?.port || 3306;
    const activeDbLabel =
      mariaSvc?.state === "running"
        ? `MariaDB (${mariaSvc.active || ":3306"})`
        : mysqlSvc?.state === "running"
          ? `MySQL (${mysqlSvc.active || ":3307"})`
          : "MariaDB 11.4 (:3306) / MySQL 9.7 (:3307)";

    const stackGuideHtml = `
      <div class="stack-guide-card">
        <div class="stack-guide-head">
          <div>
            <strong style="font-size:14px">How They Work Together — Unified PHP + Apache + MariaDB / MySQL Stack</strong>
            <div class="muted" style="font-size:12px;margin-top:2px">
              Compatible with <strong>PHP 8.2 / 8.3 / 8.4 with Apache 2.4</strong> + <strong>MariaDB 10.11 / 11.4</strong> &amp; <strong>MySQL 8.4 / 9.7</strong> (PDO <code>utf8mb4</code> · OPcache · Prepared Statements)
            </div>
          </div>
          <div style="display:flex;gap:8px;flex-wrap:wrap">
            <button type="button" class="btn xs accent" data-act="copypdo" data-port="${activeDbPort}" title="Copy hardened PHP PDO (utf8mb4 + prepared statements) connection snippet">📋 Copy PHP PDO Snippet</button>
            <button type="button" class="btn xs" data-act="install" data-engine="php" title="Switch or install PHP 8.2, 8.3, or 8.4">+ PHP Versions (8.2 / 8.3 / 8.4)</button>
          </div>
        </div>
        <div class="stack-flow">
          <div style="font-size:13px;font-weight:600">🖥 Browser (<code>http://localhost:${apacheSvc?.port || 8080}/</code>)</div>
          <div class="stack-arrow">↓</div>
          <div class="stack-node apache">
            <strong>Apache ${esc(activeApacheVer)} (Port ${apacheSvc?.port || 8080})</strong>
            <span>Handles website requests · Drag &amp; Drop <code>www/</code> Hosting · Security Headers · URL Rewriting</span>
          </div>
          <div class="stack-arrow">↓</div>
          <div class="stack-node php">
            <strong>PHP ${esc(activePhpVer)} (Integrated inside Apache 2.4)</strong>
            <span>Processes application logic · Zend OPcache · PDO &amp; mysqli · Secure Sessions · Mailpit SMTP (:1025)</span>
          </div>
          <div class="stack-arrow">⇅ Reads and writes data (PDO Prepared Statements · utf8mb4)</div>
          <div class="stack-node db">
            <strong>${esc(activeDbLabel)}</strong>
            <span>Stores users, products, sales, and application data (InnoDB · 20,000+ rows optimized · Port ${activeDbPort})</span>
          </div>
        </div>
      </div>`;

    $("serviceGrid").innerHTML = `<div class="svc-list-panel">${rowsHtml}</div>${stackGuideHtml}`;
  }

  function renderActiveDownloads() {
    const active = Object.values(state.downloads).filter(
      (d) => d.phase === "downloading" || d.phase === "extracting"
    );
    const dlBadge = $("dlBadge");
    dlBadge.textContent = active.length;
    dlBadge.hidden = active.length === 0;

    const strip = $("dashDownloads");
    strip.hidden = active.length === 0;
    strip.innerHTML = active
      .map((d) => {
        const pct = d.total > 0 ? Math.min(100, Math.round((d.received / d.total) * 100)) : 0;
        const detail =
          d.phase === "downloading"
            ? d.total > 0
              ? `Downloading ${formatBytes(d.received)} / ${formatBytes(d.total)} (${pct}%)`
              : `Downloading ${formatBytes(d.received)}...`
            : `Extracting ${pct}%`;
        return `
        <div class="progress-card">
          <div class="progress-top">
            <span><strong>${esc(d.engine)} ${esc(d.version)}</strong> — ${detail}</span>
            <button class="btn xs danger-ghost" data-cancel-dl="${esc(d.engine)}|${esc(d.version)}">Cancel</button>
          </div>
          <div class="progress-bar"><div class="progress-fill" style="width:${pct}%"></div></div>
        </div>`;
      })
      .join("");
  }

  // ---- Engine segmented tab helper ----
  function renderSegTabs(containerId, activeEngine, onSelect) {
    const el = $(containerId);
    el.innerHTML = state.statuses
      .map(
        (s) =>
          `<button class="seg ${s.engine === activeEngine ? "active" : ""}" data-engine="${s.engine}">
            <span class="dot ${s.state}"></span>${esc(s.name)}
          </button>`
      )
      .join("");
    el.querySelectorAll(".seg").forEach((btn) =>
      btn.addEventListener("click", () => onSelect(btn.dataset.engine))
    );
  }

  // ---- Database Browser ----
  function renderDbEngineTabs() {
    const el = $("dbEngineTabs");
    el.innerHTML = state.statuses
      .filter((s) => s.isSql || s.engine === "redis")
      .map(
        (s) =>
          `<button class="seg ${s.engine === state.dbEngine ? "active" : ""}" data-engine="${s.engine}">
            <span class="dot ${s.state}"></span>${esc(s.name)}
          </button>`
      )
      .join("");
    el.querySelectorAll(".seg").forEach((btn) =>
      btn.addEventListener("click", () => {
        state.dbEngine = btn.dataset.engine;
        state.sqlDb = "";
        state.sqlDbs = [];
        state.sqlTables = [];
        state.sqlTable = "";
        state.sqlColumns = [];
        state.sqlLastResult = null;
        state.sqlShowingAll = false;
        renderDbEngineTabs();
        loadDbView();
      })
    );
  }

  async function loadDbView() {
    const s = getStatus(state.dbEngine);
    const stoppedEl = $("dbStoppedState");
    const sqlEl = $("sqlBrowser");
    const redisEl = $("redisBrowser");
    const isSql = s ? s.isSql : state.dbEngine !== "redis";

    $("btnOpenImport").hidden = !isSql;
    $("btnOpenRestore").hidden = state.dbEngine !== "mssql";
    $("btnBackupSql").hidden = !isSql;
    $("btnBackupBak").hidden = state.dbEngine !== "mssql";

    if (!s || s.state !== "running") {
      sqlEl.hidden = true;
      redisEl.hidden = true;
      stoppedEl.hidden = false;
      const installed = s && s.installations && s.installations.length > 0;
      $("dbStoppedMsg").textContent = installed
        ? `${s.name} is currently ${s.state}. Start it to browse databases.`
        : `${s ? s.name : state.dbEngine} is not installed yet.`;
      $("btnDbStartEngine").textContent = installed ? `Start ${s.name}` : `Install ${s ? s.name : ""}`;
      return;
    }

    stoppedEl.hidden = true;
    if (s.isSql) {
      redisEl.hidden = true;
      sqlEl.hidden = false;
      await loadSqlDatabases();
    } else {
      sqlEl.hidden = true;
      redisEl.hidden = false;
      await loadRedisDatabases();
    }
  }

  async function loadSqlDatabases() {
    try {
      const dbs = await api().ListDatabases(state.dbEngine);
      state.sqlDbs = dbs || [];
      if (!state.sqlDb || !state.sqlDbs.includes(state.sqlDb)) {
        state.sqlDb =
          state.sqlDbs.find((d) => !SYSTEM_DBS.has(d.toLowerCase())) || state.sqlDbs[0] || "";
      }
      renderSqlDbList();
      if (state.sqlDb) {
        await loadSqlTables(state.sqlDb);
      } else {
        state.sqlTables = [];
        state.sqlTable = "";
        state.sqlColumns = [];
        renderSqlTableList();
        updateSqlTableToolbar();
      }
    } catch (e) {
      toast(errMsg(e), "err");
    }
  }

  function renderSqlDbList() {
    $("sqlActiveDb").textContent = state.sqlDb ? `${state.dbEngine} / ${state.sqlDb}` : "No DB selected";
    const list = $("sqlDbList");
    if (!state.sqlDbs.length) {
      list.innerHTML = `<div class="empty-note">No databases</div>`;
      return;
    }
    list.innerHTML = state.sqlDbs
      .map((db) => {
        const isSys = SYSTEM_DBS.has(db.toLowerCase());
        return `<div class="tree-item ${db === state.sqlDb ? "active" : ""}" data-db="${esc(db)}">
          <span class="tree-label" title="${esc(db)}">${esc(db)}</span>
          ${isSys ? "" : `<button class="tree-del" data-drop-db="${esc(db)}" title="Drop database">✕</button>`}
        </div>`;
      })
      .join("");
  }

  function updateSqlTableToolbar() {
    const hasTable = !!state.sqlTable;
    const chip = $("sqlActiveTable");
    if (chip) {
      chip.hidden = !hasTable;
      chip.textContent = hasTable ? state.sqlTable : "";
    }
    if ($("btnRowCreate")) $("btnRowCreate").hidden = !hasTable;
    if ($("btnMetaExportTable")) $("btnMetaExportTable").hidden = !hasTable;
    if ($("btnMetaCreateRow")) $("btnMetaCreateRow").hidden = !hasTable;
    if ($("btnMetaShowAll")) $("btnMetaShowAll").hidden = !hasTable;
    if ($("sqlRowFilter")) $("sqlRowFilter").hidden = !(state.sqlLastResult && state.sqlLastResult.isQuery);
  }

  async function loadSqlTables(db) {
    $("sqlTablesTitle").textContent = `Tables (${db})`;
    try {
      const tables = await api().ListTables(state.dbEngine, db);
      state.sqlTables = tables || [];
      if (state.sqlTable && !state.sqlTables.some((t) => t.name === state.sqlTable)) {
        state.sqlTable = "";
        state.sqlColumns = [];
      }
      renderSqlTableList();
      updateSqlTableToolbar();
    } catch (e) {
      state.sqlTables = [];
      state.sqlTable = "";
      state.sqlColumns = [];
      renderSqlTableList();
      updateSqlTableToolbar();
      toast(errMsg(e), "err");
    }
  }

  function renderSqlTableList() {
    const list = $("sqlTableList");
    if (!state.sqlTables.length) {
      list.innerHTML = `<div class="empty-note">No tables</div>`;
      return;
    }
    list.innerHTML = state.sqlTables
      .map(
        (t, idx) =>
          `<div class="tree-item ${t.name === state.sqlTable ? "active" : ""}" data-table-idx="${idx}" title="${esc(t.name)} (${esc(t.kind)})">
            <span class="tree-label">${esc(t.name)}</span>
            ${t.kind && t.kind.toUpperCase().includes("VIEW") ? `<span class="type-tag">VIEW</span>` : `<button class="tree-del" data-drop-table="${esc(t.name)}" title="Drop table">✕</button>`}
          </div>`
      )
      .join("");
  }

  async function selectSqlTable(tableObj, showAll = false) {
    if (!tableObj) return;
    state.sqlTable = tableObj.name;
    state.sqlShowingAll = !!showAll;
    renderSqlTableList();
    updateSqlTableToolbar();
    $("sqlEditor").value = showAll ? (tableObj.selectAll || tableObj.select) : tableObj.select;
    try {
      state.sqlColumns = (await api().DescribeTable(state.dbEngine, state.sqlDb, tableObj.name)) || [];
    } catch {
      state.sqlColumns = [];
    }
    await runSqlQuery(showAll);
  }

  async function runSqlQuery(showAll = false) {
    const q = $("sqlEditor").value.trim();
    if (!q) return;
    const meta = $("sqlMeta");
    const bar = meta.parentElement;
    bar.className = "result-meta-bar";
    meta.textContent = showAll ? "Loading all rows..." : "Running query...";
    $("sqlResults").innerHTML = "";
    try {
      const res = showAll
        ? await api().RunQueryAll(state.dbEngine, state.sqlDb, q)
        : await api().RunQuery(state.dbEngine, state.sqlDb, q);
      state.sqlLastResult = res;
      if ($("sqlRowFilter")) $("sqlRowFilter").value = "";
      updateSqlTableToolbar();
      if (res.isQuery) {
        meta.textContent = `${res.rows.length} row(s)${res.truncated ? " (capped)" : showAll ? " (all)" : ""} · ${res.durationMs.toFixed(1)} ms`;
        renderSqlTable(res);
      } else {
        meta.textContent = `Query OK · ${res.affected} row(s) affected · ${res.durationMs.toFixed(1)} ms`;
        if (/\b(create|drop|alter|insert|update|delete|truncate)\b/i.test(q)) {
          loadSqlDatabases();
        }
      }
    } catch (e) {
      bar.className = "result-meta-bar error";
      meta.textContent = errMsg(e);
    }
  }

  function renderSqlTable(res) {
    const wrap = $("sqlResults");
    if (!res || !res.columns.length) {
      wrap.innerHTML = `<div class="empty-note">Empty result set</div>`;
      return;
    }
    const filter = ($("sqlRowFilter")?.value || "").trim().toLowerCase();
    const canRowCrud = !!state.sqlTable;
    const indexedRows = res.rows
      .map((row, idx) => ({ row, idx }))
      .filter(({ row }) => {
        if (!filter) return true;
        return row.some((c) => c !== null && String(c).toLowerCase().includes(filter));
      });

    const actHead = canRowCrud ? `<th class="row-acts-col">Actions</th>` : "";
    const thead = `<thead><tr>${actHead}${res.columns.map((c) => `<th>${esc(c)}</th>`).join("")}</tr></thead>`;
    const tbody = `<tbody>${indexedRows
      .map(({ row, idx }) => {
        const actCell = canRowCrud
          ? `<td class="row-acts-col"><div class="row-acts">
              <button type="button" class="row-btn show" data-row-act="show" data-row-idx="${idx}" title="Show full row details">Show</button>
              <button type="button" class="row-btn edit" data-row-act="edit" data-row-idx="${idx}" title="Edit this row">Edit</button>
              <button type="button" class="row-btn del" data-row-act="delete" data-row-idx="${idx}" title="Delete this row">Delete</button>
            </div></td>`
          : "";
        return `<tr data-row-idx="${idx}">${actCell}${row
          .map((cell) =>
            cell === null
              ? `<td class="null-val">NULL</td>`
              : `<td title="${esc(cell)}">${esc(cell)}</td>`
          )
          .join("")}</tr>`;
      })
      .join("")}</tbody>`;
    wrap.innerHTML = `<table class="data-table">${thead}${tbody}</table>`;
  }

  function buildRowKeyMap(rowIdx) {
    const res = state.sqlLastResult;
    if (!res || !res.rows[rowIdx]) return { keys: {}, hasPK: false };
    const row = res.rows[rowIdx];
    const colMap = new Map((state.sqlColumns || []).map((c) => [c.name.toLowerCase(), c]));
    const pkCols = res.columns.filter((c) => colMap.get(c.toLowerCase())?.isPrimaryKey);
    const hasPK = pkCols.length > 0;
    const keyNames = hasPK ? pkCols : res.columns;
    const keys = {};
    keyNames.forEach((colName) => {
      const ci = res.columns.indexOf(colName);
      if (ci >= 0) {
        const val = row[ci];
        keys[colName] = val === null ? { value: "", isNull: true } : { value: String(val), isNull: false };
      }
    });
    return { keys, hasPK };
  }

  function openShowRowModal(rowIdx) {
    const res = state.sqlLastResult;
    const row = res?.rows?.[rowIdx];
    if (!row) return;
    $("modalRowShowTitle").textContent = `Show Row — ${state.sqlTable} (#${rowIdx + 1})`;
    const jsonObj = {};
    $("rowShowFields").innerHTML = res.columns
      .map((col, ci) => {
        const val = row[ci];
        jsonObj[col] = val;
        let display = val === null ? "NULL" : String(val);
        if (val !== null && (display.startsWith("{") || display.startsWith("["))) {
          try {
            display = JSON.stringify(JSON.parse(display), null, 2);
          } catch { }
        }
        return `<div class="row-detail-item">
          <div class="row-detail-head">
            <strong>${esc(col)}</strong>
            <button type="button" class="btn xs ghost" data-copy-field="${ci}">Copy</button>
          </div>
          <pre class="row-detail-val ${val === null ? "null-val" : ""}">${esc(display)}</pre>
        </div>`;
      })
      .join("");
    $("rowShowFields").querySelectorAll("[data-copy-field]").forEach((b) => {
      b.onclick = async () => {
        const v = row[Number(b.dataset.copyField)];
        await copyText(v === null ? "" : String(v));
        toast("Copied field value", "ok");
      };
    });
    $("btnRowShowCopyJson").onclick = async () => {
      await copyText(JSON.stringify(jsonObj, null, 2));
      toast("Copied row JSON", "ok");
    };
    $("btnRowShowEdit").onclick = () => {
      $("modalRowShow").hidden = true;
      openRowFormModal("edit", rowIdx);
    };
    $("btnRowShowDelete").onclick = async () => {
      $("modalRowShow").hidden = true;
      await deleteSqlRow(rowIdx);
    };
    $("modalRowShow").hidden = false;
  }

  async function openRowFormModal(mode, rowIdx = -1) {
    if (!state.sqlTable) {
      toast("Select a table on the left first", "err");
      return;
    }
    if (!state.sqlColumns || !state.sqlColumns.length) {
      try {
        state.sqlColumns = (await api().DescribeTable(state.dbEngine, state.sqlDb, state.sqlTable)) || [];
      } catch (e) {
        toast(errMsg(e), "err");
        return;
      }
    }
    const cols = state.sqlColumns;
    if (!cols.length) {
      toast("Could not inspect columns for this table", "err");
      return;
    }
    const isEdit = mode === "edit";
    const res = state.sqlLastResult;
    const row = isEdit ? res?.rows?.[rowIdx] : null;
    if (isEdit && !row) return;

    $("modalRowFormTitle").textContent = isEdit
      ? `Edit Row — ${state.sqlTable}`
      : `Create New Row — ${state.sqlTable}`;
    $("btnSubmitRowForm").textContent = isEdit ? "Save Changes" : "Create Row";

    $("rowFormFields").innerHTML = cols
      .map((col, idx) => {
        let curVal = "";
        let isNull = false;
        if (isEdit && res) {
          const ci = res.columns.indexOf(col.name);
          const raw = ci >= 0 ? row[ci] : null;
          isNull = raw === null;
          curVal = raw === null ? "" : String(raw);
        } else if (!isEdit) {
          if (col.isAutoIncrement) {
            curVal = "";
          } else if (col.defaultVal !== undefined && col.defaultVal !== null) {
            curVal = col.defaultVal;
          } else if (col.nullable) {
            isNull = false;
          }
        }
        const isLong = /text|json|blob|clob|xml/i.test(col.dataType) || curVal.length > 90 || curVal.includes("\n");
        const placeholder = !isEdit && col.isAutoIncrement ? "Auto-generated if left blank" : "";
        const control = isLong
          ? `<textarea class="input row-field-val" data-col="${esc(col.name)}" rows="3" placeholder="${esc(placeholder)}" ${isNull ? "disabled" : ""}>${esc(curVal)}</textarea>`
          : `<input type="text" class="input row-field-val" data-col="${esc(col.name)}" value="${esc(curVal)}" placeholder="${esc(placeholder)}" ${isNull ? "disabled" : ""} />`;
        return `<div class="row-form-item" data-col-idx="${idx}">
          <div class="row-form-head">
            <strong>${esc(col.name)}</strong>
            <span class="meta-tag">${esc(col.dataType)}</span>
            ${col.isPrimaryKey ? `<span class="pill accent">PK</span>` : ""}
            ${col.isAutoIncrement ? `<span class="pill running">AUTO</span>` : ""}
            <div class="spacer"></div>
            ${col.nullable
            ? `<label class="check-inline" style="font-size:11.5px"><input type="checkbox" class="row-field-null" data-col="${esc(col.name)}" ${isNull ? "checked" : ""} /> NULL</label>`
            : ""}
          </div>
          ${control}
        </div>`;
      })
      .join("");

    $("rowFormFields").querySelectorAll(".row-field-null").forEach((chk) => {
      chk.onchange = () => {
        const inp = $("rowFormFields").querySelector(`.row-field-val[data-col="${CSS.escape(chk.dataset.col)}"]`);
        if (inp) inp.disabled = chk.checked;
      };
    });

    $("formRowCrud").onsubmit = async (e) => {
      e.preventDefault();
      const btn = $("btnSubmitRowForm");
      btn.disabled = true;
      try {
        const values = {};
        cols.forEach((col) => {
          const inp = $("rowFormFields").querySelector(`.row-field-val[data-col="${CSS.escape(col.name)}"]`);
          const nullChk = $("rowFormFields").querySelector(`.row-field-null[data-col="${CSS.escape(col.name)}"]`);
          const isNull = !!(nullChk && nullChk.checked);
          const val = inp ? inp.value : "";
          if (!isEdit && col.isAutoIncrement && !isNull && val === "") {
            return; // let DB auto-generate PK/identity
          }
          if (isEdit && res) {
            const ci = res.columns.indexOf(col.name);
            const origRaw = ci >= 0 ? row[ci] : null;
            const origIsNull = origRaw === null;
            const origStr = origRaw === null ? "" : String(origRaw);
            // Skip unchanged identity/PK columns (required for SQL Server IDENTITY columns)
            if (col.isAutoIncrement && isNull === origIsNull && val === origStr) {
              return;
            }
          }
          values[col.name] = { value: val, isNull };
        });
        if (isEdit) {
          const { keys, hasPK } = buildRowKeyMap(rowIdx);
          await api().UpdateRow(state.dbEngine, state.sqlDb, state.sqlTable, keys, values, hasPK);
          toast("Row updated!", "ok");
        } else {
          await api().InsertRow(state.dbEngine, state.sqlDb, state.sqlTable, values);
          toast("Row created!", "ok");
        }
        $("modalRowForm").hidden = true;
        await runSqlQuery(state.sqlShowingAll);
      } catch (err) {
        toast(errMsg(err), "err");
      } finally {
        btn.disabled = false;
      }
    };
    $("modalRowForm").hidden = false;
  }

  async function deleteSqlRow(rowIdx) {
    if (!state.sqlTable) return;
    if (!(await askConfirm("Delete Row?", `Permanently delete this row from "${state.sqlTable}"?`, "Delete"))) return;
    try {
      const { keys, hasPK } = buildRowKeyMap(rowIdx);
      await api().DeleteRow(state.dbEngine, state.sqlDb, state.sqlTable, keys, hasPK);
      toast("Row deleted", "ok");
      await runSqlQuery(state.sqlShowingAll);
    } catch (err) {
      toast(errMsg(err), "err");
    }
  }

  // ---- Redis Browser ----
  async function loadRedisDatabases() {
    try {
      const dbs = await api().RedisDatabases();
      const sel = $("redisDbSelect");
      sel.innerHTML = dbs
        .map(
          (d) =>
            `<option value="${d.index}" ${d.index === state.redisDb ? "selected" : ""}>db${d.index} (${d.keys})</option>`
        )
        .join("");
      await scanRedisKeys(false);
    } catch (e) {
      toast(errMsg(e), "err");
    }
  }

  async function scanRedisKeys(append) {
    const cursor = append ? state.redisCursor : "0";
    const pattern = $("redisPattern").value.trim() || "*";
    try {
      const res = await api().RedisScan(state.redisDb, pattern, cursor);
      state.redisCursor = res.cursor;
      state.redisKeys = append ? [...state.redisKeys, ...res.keys] : res.keys;
      renderRedisKeys();
    } catch (e) {
      toast(errMsg(e), "err");
    }
  }

  function renderRedisKeys() {
    const list = $("redisKeyList");
    $("redisMoreWrap").hidden = !state.redisCursor || state.redisCursor === "0";
    if (!state.redisKeys.length) {
      list.innerHTML = `<div class="empty-note">No matching keys</div>`;
      return;
    }
    list.innerHTML = state.redisKeys
      .map(
        (k) =>
          `<div class="tree-item ${k.name === state.redisSelectedKey ? "active" : ""}" data-rkey="${esc(k.name)}">
            <span class="tree-label" title="${esc(k.name)}">${esc(k.name)}</span>
            <span class="type-tag">${esc(k.type)}</span>
          </div>`
      )
      .join("");
  }

  async function inspectRedisKey(key) {
    state.redisSelectedKey = key;
    renderRedisKeys();
    const box = $("redisInspector");
    try {
      const val = await api().RedisGet(state.redisDb, key);
      let display = val.value;
      if (val.type === "string" && (display.startsWith("{") || display.startsWith("["))) {
        try {
          display = JSON.stringify(JSON.parse(display), null, 2);
        } catch { }
      }
      const ttlStr = val.ttl < 0 ? "no expiry" : `${val.ttl}s`;
      box.innerHTML = `
        <div class="editor-bar">
          <strong class="chip">${esc(val.key)}</strong>
          <span class="type-tag">${esc(val.type)}</span>
          <span class="muted">TTL: ${ttlStr} · Size: ${val.size}</span>
          <div class="spacer"></div>
          <button class="btn xs" id="btnRedisCopyVal">Copy</button>
          <button class="btn xs danger-ghost" id="btnRedisDelKey">Delete</button>
        </div>
        <pre class="redis-val-pre">${esc(display)}</pre>`;
      $("btnRedisCopyVal").onclick = async () => {
        await copyText(val.value);
        toast("Copied value", "ok");
      };
      $("btnRedisDelKey").onclick = async () => {
        if (!(await askConfirm("Delete Redis key?", `Delete "${key}" from db${state.redisDb}?`, "Delete"))) return;
        try {
          await api().RedisDelete(state.redisDb, key);
          state.redisSelectedKey = "";
          box.innerHTML = `<div class="empty-note">Key deleted.</div>`;
          await loadRedisDatabases();
        } catch (e) {
          toast(errMsg(e), "err");
        }
      };
    } catch (e) {
      box.innerHTML = `<div class="empty-note">${esc(errMsg(e))}</div>`;
    }
  }

  async function runRedisCli(line) {
    const out = $("redisCliOut");
    const entry = document.createElement("div");
    entry.className = "cli-entry";
    try {
      const reply = await api().RedisCommand(state.redisDb, line);
      entry.innerHTML = `<div class="cli-cmd">db${state.redisDb}&gt; ${esc(line)}</div><div class="cli-res">${esc(reply)}</div>`;
      if (/^(set|del|hset|lpush|rpush|sadd|zadd|flushdb|flushall|expire|rename|mset)/i.test(line)) {
        loadRedisDatabases();
      }
    } catch (e) {
      entry.innerHTML = `<div class="cli-cmd">db${state.redisDb}&gt; ${esc(line)}</div><div class="cli-err">${esc(errMsg(e))}</div>`;
    }
    out.appendChild(entry);
    out.scrollTop = out.scrollHeight;
  }

  // ---- Versions ----
  function renderVerEngineTabs() {
    renderSegTabs("verEngineTabs", state.verEngine, (eng) => {
      state.verEngine = eng;
      renderVerEngineTabs();
      renderVersions();
    });
  }

  const OFFICIAL_DOWNLOAD_PAGES = {
    apache: "https://www.apachelounge.com/download/",
    php: "https://windows.php.net/download/",
    mariadb: "https://mariadb.org/download/?t=mariadb&p=mariadb&o=windows&a=x86_64&k=zip",
    mysql: "https://dev.mysql.com/downloads/mysql/",
    postgresql: "https://www.enterprisedb.com/download-postgresql-binaries",
    mssql: "https://www.microsoft.com/en-us/sql-server/sql-server-downloads",
    redis: "https://github.com/redis-windows/redis-windows/releases",
    mailpit: "https://github.com/axllent/mailpit/releases",
  };

  function compareVer(a, b) {
    const as = String(a || "").split(".").map((n) => parseInt(n, 10) || 0);
    const bs = String(b || "").split(".").map((n) => parseInt(n, 10) || 0);
    const len = Math.max(as.length, bs.length);
    for (let i = 0; i < len; i++) {
      const x = as[i] || 0;
      const y = bs[i] || 0;
      if (x !== y) return x - y;
    }
    return 0;
  }

  function renderVersions() {
    const s = getStatus(state.verEngine);
    const insts = s ? s.installations || [] : [];
    const instSet = new Set(insts.map((i) => i.version));
    const entries = state.catalog[state.verEngine] || [];
    const newestEntry = entries[0] || null;

    const instList = $("installedList");
    if (!insts.length) {
      instList.innerHTML = `<div class="empty-note">No ${s ? esc(s.name) : ""} versions installed yet. Pick one below, check newest versions, or import a local .zip.</div>`;
    } else {
      instList.innerHTML = insts
        .map((inst) => {
          const isActive = s && inst.name === s.active;
          const newestDl = newestEntry ? state.downloads[`${state.verEngine}@${newestEntry.version}`] : null;
          const newestInProgress =
            newestDl && (newestDl.phase === "downloading" || newestDl.phase === "extracting");
          const canUpdate =
            newestEntry &&
            !instSet.has(newestEntry.version) &&
            !newestInProgress &&
            compareVer(newestEntry.version, inst.version) > 0;
          return `
          <div class="ver-row">
            <div class="ver-info">
              <div class="ver-title">
                <span>${esc(inst.name)}</span>
                ${isActive ? `<span class="pill running">Active</span>` : ""}
                ${canUpdate ? `<span class="pill warn">Newer: ${esc(newestEntry.version)}</span>` : ""}
              </div>
              <div class="ver-sub">${esc(inst.dir)}</div>
            </div>
            <div class="ver-actions">
              ${canUpdate
              ? `<button class="btn sm primary" data-dl-ver="${esc(newestEntry.version)}" data-dl-url="${esc(
                newestEntry.url
              )}" title="Download and install newest version ${esc(newestEntry.version)}">↑ Update to ${esc(
                newestEntry.version
              )}</button>`
              : ""
            }
              ${isActive
              ? ""
              : `<button class="btn sm accent" data-use-ver="${esc(inst.name)}">Use Version</button>`
            }
              <button class="btn sm danger-ghost" data-del-ver="${esc(inst.name)}">Remove</button>
            </div>
          </div>`;
        })
        .join("");
    }

    const catList = $("catalogList");
    if (!entries.length) {
      catList.innerHTML = `<div class="empty-note">No catalog entries for this engine.</div>`;
      return;
    }
    catList.innerHTML = entries
      .map((entry, idx) => {
        const dl = state.downloads[`${state.verEngine}@${entry.version}`];
        const inProgress = dl && (dl.phase === "downloading" || dl.phase === "extracting");
        const isInstalled = instSet.has(entry.version);
        let host = "";
        try {
          host = new URL(entry.url).host;
        } catch { }

        if (inProgress) {
          const pct = dl.total > 0 ? Math.min(100, Math.round((dl.received / dl.total) * 100)) : 0;
          const label =
            dl.phase === "downloading"
              ? dl.total > 0
                ? `${formatBytes(dl.received)} / ${formatBytes(dl.total)} (${pct}%)`
                : formatBytes(dl.received)
              : `Extracting (${pct}%)`;
          return `
          <div class="progress-card">
            <div class="progress-top">
              <span><strong>${esc(state.verEngine)}-${esc(entry.version)}</strong> — ${label}</span>
              <button class="btn xs danger-ghost" data-cancel-dl="${esc(state.verEngine)}|${esc(entry.version)}">Cancel</button>
            </div>
            <div class="progress-bar"><div class="progress-fill" style="width:${pct}%"></div></div>
          </div>`;
        }

        return `
        <div class="ver-row">
          <div class="ver-info">
            <div class="ver-title">
              <span>${esc(state.verEngine)}-${esc(entry.version)}</span>
              ${idx === 0 ? `<span class="pill running">Newest</span>` : ""}
              ${entry.note ? `<span class="pill accent">${esc(entry.note)}</span>` : ""}
              ${isInstalled ? `<span class="pill running">Installed</span>` : ""}
            </div>
            <div class="ver-sub" title="${esc(entry.url)}">${esc(host)}</div>
          </div>
          <div class="ver-actions">
            ${isInstalled
            ? `<button class="btn sm ghost" disabled title="Already installed — remove first if you want to reinstall">✓ Installed</button>`
            : `<button class="btn sm ghost" data-manual-url="${esc(entry.url)}" title="Download .zip manually in your web browser">Manual (.zip)</button>
                   <button class="btn sm primary" data-dl-ver="${esc(entry.version)}" data-dl-url="${esc(entry.url)}">Download</button>`
          }
          </div>
        </div>`;
      })
      .join("");
  }

  // ---- Config & Logs ----
  function renderCfgEngineTabs() {
    renderSegTabs("cfgEngineTabs", state.cfgEngine, (eng) => {
      state.cfgEngine = eng;
      renderCfgEngineTabs();
      loadCfgOrLog();
    });
  }

  function stopLogPolling() {
    if (state.logTimer) {
      clearInterval(state.logTimer);
      state.logTimer = null;
    }
  }

  async function loadCfgOrLog() {
    stopLogPolling();
    const isCfg = state.cfgMode === "config";
    $("cfgEditorPane").hidden = !isCfg;
    $("cfgLogPane").hidden = isCfg;
    document.querySelectorAll("#cfgModeTabs .seg").forEach((b) =>
      b.classList.toggle("active", b.dataset.mode === state.cfgMode)
    );

    if (isCfg) {
      await loadConfigFile();
    } else {
      await loadLogFile();
      if ($("chkLogAuto").checked) {
        state.logTimer = setInterval(loadLogFile, 2000);
      }
    }
  }

  async function loadConfigFile() {
    const s = getStatus(state.cfgEngine);
    $("btnCfgSaveRestart").hidden = !(s && s.state === "running");
    const hint = $("cfgHint");
    hint.hidden = true;
    try {
      const res = await api().ReadConfig(state.cfgEngine);
      $("cfgPath").textContent = res.path;
      $("cfgText").value = res.content || "";
      $("cfgText").disabled = !res.exists;
      $("btnCfgSave").disabled = !res.exists;
      $("btnCfgNotepad").disabled = !res.exists;
      if (!res.exists && res.hint) {
        hint.textContent = res.hint;
        hint.hidden = false;
      }
    } catch (e) {
      $("cfgPath").textContent = "—";
      $("cfgText").value = "";
      $("cfgText").disabled = true;
      $("btnCfgSave").disabled = true;
      $("btnCfgNotepad").disabled = true;
      hint.textContent = errMsg(e);
      hint.hidden = false;
    }
  }

  async function saveConfigFile(restart) {
    try {
      await api().SaveConfig(state.cfgEngine, $("cfgText").value);
      toast("Config saved", "ok");
      if (restart) {
        toast(`Restarting ${state.cfgEngine}...`, "info");
        await api().Restart(state.cfgEngine);
        toast(`${state.cfgEngine} restarted`, "ok");
      }
    } catch (e) {
      toast(errMsg(e), "err");
    }
  }

  async function loadLogFile() {
    if (state.info) {
      $("logPath").textContent = `${state.info.paths.logs}\\${state.cfgEngine}.log`;
    }
    try {
      const text = await api().ReadLog(state.cfgEngine);
      const area = $("logText");
      const atBottom = area.scrollHeight - area.scrollTop - area.clientHeight < 40;
      area.textContent = text || "(log is empty)";
      if (atBottom) area.scrollTop = area.scrollHeight;
    } catch { }
  }

  // ---- Built-in Terminal ----
  const TERM_QUICK_CMDS = {
    shell: ["status", "help", "path", "dir", "start all", "stop all"],
    mariadb: ["SHOW DATABASES;", "SHOW TABLES;", "SELECT VERSION();", "STATUS;", "exit"],
    mysql: ["SHOW DATABASES;", "SHOW TABLES;", "SELECT VERSION();", "STATUS;", "exit"],
    postgresql: ["\\l", "\\dt", "\\du", "\\conninfo", "SELECT version();", "\\q"],
    mssql: ["sp_databases", "sp_tables", "SELECT @@VERSION;", "STATUS;", "exit"],
    redis: ["PING", "INFO server", "DBSIZE", "KEYS *", "exit"],
  };

  async function initTerminalView() {
    if (!state.termInitialized) {
      state.termInitialized = true;
      try {
        const [st, banner] = await Promise.all([api().TerminalState(), api().TerminalBanner()]);
        state.termState = st;
        const out = $("termOutput");
        out.innerHTML = `<div class="term-banner-line">${esc(banner)}</div>`;
      } catch (e) {
        toast(errMsg(e), "err");
      }
    } else {
      try {
        state.termState = await api().TerminalState();
      } catch { }
    }
    updateTermUI();
    $("termInput").focus();
  }

  function updateTermUI() {
    const st = state.termState;
    $("termPrompt").textContent = st.prompt || ">";
    document.querySelectorAll("#termModeTabs .seg").forEach((b) =>
      b.classList.toggle("active", b.dataset.tmode === st.mode)
    );
    const cmds = TERM_QUICK_CMDS[st.mode] || TERM_QUICK_CMDS.shell;
    $("termQuickBar").innerHTML =
      `<span class="term-quick-label">Quick:</span>` +
      cmds.map((c) => `<button type="button" class="term-chip" data-tcmd="${esc(c)}">${esc(c)}</button>`).join("");
  }

  function appendTermBlock(promptText, cmdText, outputText, isErr = false) {
    const out = $("termOutput");
    const div = document.createElement("div");
    div.className = "term-block";
    let html = "";
    if (cmdText !== null) {
      html += `<div class="term-cmd-line"><span class="term-cmd-prompt">${esc(promptText)}</span>${esc(cmdText)}</div>`;
    }
    if (outputText) {
      html += `<div class="${isErr ? "term-err-line" : "term-res-line"}">${esc(outputText)}</div>`;
    }
    div.innerHTML = html;
    out.appendChild(div);
    out.scrollTop = out.scrollHeight;
  }

  async function switchTermMode(mode) {
    try {
      const res = await api().TerminalSetMode(mode);
      state.termState = res.state;
      updateTermUI();
      if (res.output) {
        appendTermBlock(null, null, res.output, false);
      }
      $("termInput").focus();
    } catch (e) {
      toast(errMsg(e), "err");
    }
  }

  async function runTermCommand(rawLine) {
    const line = rawLine.trim();
    const prevPrompt = state.termState.prompt || ">";
    if (!line) return;

    if (state.termHistory[state.termHistory.length - 1] !== line) {
      state.termHistory.push(line);
    }
    state.termHistIdx = state.termHistory.length;

    $("termInput").value = "";
    $("btnTermCancel").hidden = false;
    $("btnTermRun").disabled = true;

    try {
      const res = await api().TerminalExec(line);
      state.termState = res.state;
      updateTermUI();
      if (res.clear) {
        $("termOutput").innerHTML = "";
      } else {
        const isErr = /^(error|fatal|\(error\))/i.test(res.output || "");
        appendTermBlock(prevPrompt, line, res.output, isErr);
      }
    } catch (e) {
      appendTermBlock(prevPrompt, line, errMsg(e), true);
    } finally {
      $("btnTermCancel").hidden = true;
      $("btnTermRun").disabled = false;
      $("termInput").focus();
    }
  }

  // ---- Settings ----
  async function loadSettingsForm() {
    const s = await api().GetSettings();
    state.settings = s;
    const labels = {
      apache: "Apache Web Server (+ PHP)",
      php: "PHP (Standalone Server)",
      mariadb: "MariaDB",
      mysql: "MySQL",
      postgresql: "PostgreSQL",
      mssql: "MS SQL Server",
      redis: "Redis",
      mailpit: "Mailpit",
    };
    $("settingsEngineRows").innerHTML = ["apache", "php", "mariadb", "mysql", "postgresql", "mssql", "redis", "mailpit"]
      .map((id) => {
        const es = s.engines[id] || {};
        const isWebRoot = id === "apache" || id === "php";
        const isMailpit = id === "mailpit";
        const isMSSQL = id === "mssql";
        const noAuth = id === "redis" || isWebRoot || isMailpit || isMSSQL;
        const mssqlStatus = isMSSQL ? getStatus("mssql") : null;
        const mssqlServer = mssqlStatus?.serverName || "";
        return `<tr data-eng-row="${id}">
          <td>
            <strong>${labels[id]}</strong>
            ${isMSSQL && mssqlServer ? `<div class="muted" style="font-size:11px">Server Name: <code>${esc(mssqlServer)}</code></div>` : ""}
          </td>
          <td><input type="checkbox" class="eng-enabled" ${es.enabled ? "checked" : ""} /></td>
          <td><input type="number" class="input sm port-input eng-port" min="1" max="65535" value="${es.port || ""}" /></td>
          <td>${noAuth
            ? `<span class="muted">${isWebRoot ? "www/ (DocumentRoot)" : isMailpit ? "SMTP :1025 / Web UI" : isMSSQL ? `Windows Auth (${esc(mssqlServer || "LocalDB")})` : "—"}</span>`
            : `<input type="text" class="input sm eng-user" value="${esc(es.user || "")}" />`
          }</td>
          <td>${isWebRoot || isMailpit || isMSSQL
            ? `<span class="muted">—</span>`
            : `<input type="password" class="input sm eng-pass" value="${esc(es.password || "")}" placeholder="${id === "redis" ? "optional" : "blank = no password"}" />`
          }</td>
        </tr>`;
      })
      .join("");

    $("setRunOnBoot").checked = !!s.runOnBoot;
    $("setAutoStart").checked = !!s.autoStartServices;
    $("setStartMin").checked = !!s.startMinimized;
    $("setCloseToTray").checked = !!s.closeToTray;
    $("setStopOnExit").checked = !!s.stopOnExit;
    $("setTheme").value = s.theme || "system";
    $("setCatalogUrl").value = s.catalogUrl || "";

    if (state.info) {
      const p = state.info.paths;
      const rows = [
        ["Root", "root", p.root],
        ["Web Root (www/)", "www", p.www],
        ["Binaries (bin/)", "bin", p.bin],
        ["Data (data/)", "data", p.data],
        ["Config (etc/)", "etc", p.etc],
        ["Logs (logs/)", "logs", p.logs],
      ];
      $("folderList").innerHTML = rows
        .map(
          ([lbl, kind, dir]) =>
            `<div class="folder-row">
              <strong>${lbl}</strong>
              <code>${esc(dir)}</code>
              <button class="btn xs" data-open-folder="${kind}">Open</button>
            </div>`
        )
        .join("");

      $("aboutVersionBadge").textContent = "Lite Version " + state.info.version;
      const installerUrl =
        state.info.installerUrl ||
        "https://drive.google.com/drive/folders/1FVLCHqOfxmP9PbSx70jy_BfH2Yn1E_UG?usp=sharing";
      const repoUrl = state.info.repo || "https://github.com/dcquijano/Darius-DB-Lite-Version";
      const aboutRows = [
        ["Application", state.info.name || `Darius DB (Lite Version ${state.info.version})`],
        ["Creator / Author", state.info.author],
        ["Built By", state.info.builtBy],
        ["Created Date", state.info.created],
        ["Year", String(state.info.year)],
        ["Version", `Lite Version ${state.info.version}`],
        ["License", `${state.info.license} Open Source`],
        ["GitHub Repository", repoUrl, repoUrl],
        ["Official Installer (Google Drive)", installerUrl, installerUrl],
      ];
      $("aboutGrid").innerHTML = aboutRows
        .map(([k, v, link]) =>
          link
            ? `<div class="about-item"><span>${esc(k)}</span><strong><a href="#" data-ext-url="${esc(
              link
            )}" style="color:var(--accent);text-decoration:underline">${esc(v)}</a></strong></div>`
            : `<div class="about-item"><span>${esc(k)}</span><strong>${esc(v)}</strong></div>`
        )
        .join("");
    }
  }

  async function saveSettingsForm() {
    const next = structuredClone(state.settings);
    document.querySelectorAll("#settingsEngineRows tr").forEach((tr) => {
      const id = tr.dataset.engRow;
      const cur = next.engines[id] || {};
      cur.enabled = tr.querySelector(".eng-enabled").checked;
      cur.port = parseInt(tr.querySelector(".eng-port").value, 10) || cur.port;
      const u = tr.querySelector(".eng-user");
      if (u) cur.user = u.value.trim();
      const pw = tr.querySelector(".eng-pass");
      if (pw) cur.password = pw.value;
      next.engines[id] = cur;
    });
    next.runOnBoot = $("setRunOnBoot").checked;
    next.autoStartServices = $("setAutoStart").checked;
    next.startMinimized = $("setStartMin").checked;
    next.closeToTray = $("setCloseToTray").checked;
    next.stopOnExit = $("setStopOnExit").checked;
    next.theme = $("setTheme").value;
    next.catalogUrl = $("setCatalogUrl").value.trim();

    try {
      await api().SaveSettings(next);
      state.settings = next;
      applyTheme(next.theme);
      toast("Settings saved", "ok");
    } catch (e) {
      toast(errMsg(e), "err");
    }
  }

  // ---- Event Wiring ----
  function wireEvents() {
    // Nav
    $("nav").addEventListener("click", (e) => {
      const btn = e.target.closest(".nav-item");
      if (btn) switchTab(btn.dataset.tab);
    });

    // Sidebar actions
    $("btnSideTerminal").onclick = () => switchTab("terminal");
    $("btnSideRoot").onclick = () => api().OpenFolder("root").catch((e) => toast(errMsg(e), "err"));
    $("btnSideTray").onclick = () => api().HideToTray();
    $("btnSideQuit").onclick = () => api().QuitApp();

    // Dashboard actions
    $("btnStartAll").onclick = async () => {
      try {
        await api().StartAll();
      } catch (e) {
        toast(errMsg(e), "err");
      }
    };
    $("btnStopAll").onclick = async () => {
      try {
        await api().StopAll();
      } catch (e) {
        toast(errMsg(e), "err");
      }
    };
    $("btnGoVersions").onclick = () => switchTab("versions");

    // System Announcement & New Version Updater controls
    $("btnToggleAnnounce").onclick = () => {
      $("announcementBanner").hidden = !$("announcementBanner").hidden;
    };
    $("btnDismissAnnounce").onclick = () => {
      $("announcementBanner").hidden = true;
    };
    $("btnAnnounceGetSetup").onclick = () => {
      const url =
        state.info?.installerUrl ||
        "https://drive.google.com/drive/folders/1FVLCHqOfxmP9PbSx70jy_BfH2Yn1E_UG?usp=sharing";
      if (rt()?.BrowserOpenURL) rt().BrowserOpenURL(url);
      else window.open(url, "_blank");
      toast("Opened Official Darius DB Setup Installer folder (Google Drive)", "ok");
    };

    let latestUpdateInfo = null;
    const closeSysUpdater = () => ($("modalSysUpdater").hidden = true);
    const applyUpdateInfoToUI = (up) => {
      latestUpdateInfo = up;
      $("sysUpCurVer").textContent = `Darius DB (Lite Version ${up.currentVersion})`;
      $("sysUpLatVer").textContent = `Lite Version ${up.latestVersion}`;
      const badge = $("sysUpBadge");
      if (up.fetchOk === false) {
        badge.className = "pill warn";
        badge.textContent = "Fetch Failed";
      } else if (up.hasUpdate) {
        badge.className = "pill warn";
        badge.textContent = `Update Available (${up.latestVersion})`;
      } else {
        badge.className = "pill running";
        badge.textContent = up.latestCommitSha ? `Synced (${up.latestCommitSha})` : "Up to Date";
      }
      if ($("sysUpReadyBar")) {
        const showReady = !!(up.fetchOk !== false && up.hasUpdate && (up.directSetupUrl || up.fallbackSetupUrl || up.directZipUrl));
        $("sysUpReadyBar").hidden = !showReady;
        if (showReady) {
          $("sysUpReadyTitle").textContent = `🎉 Darius DB Lite Version ${up.latestVersion} is Available!`;
          $("sysUpReadySub").textContent = `Upgrade from v${up.currentVersion} → v${up.latestVersion} in-place (keeps data/ and www/ safe).`;
        }
      }
      if ($("sysUpTokenBox")) {
        $("sysUpTokenBox").hidden = !up.repoPrivateHint;
      }
      if ($("sysUpCommitBar")) {
        if (up.fetchOk === false && up.fetchError) {
          $("sysUpCommitBar").innerHTML = `<span style="color:var(--red)">⚠️ ${esc(up.fetchError)}</span>`;
        } else if (up.latestCommitSha) {
          $("sysUpCommitBar").innerHTML = `Latest GitHub Push: <code>${esc(up.latestCommitSha)}</code> — <strong>${esc(
            up.latestCommitMsg
          )}</strong> <span class="muted">(${esc(up.latestCommitDate)})</span>`;
        } else {
          $("sysUpCommitBar").textContent = `Checked at ${up.checkedAt || ""}`;
        }
      }
      $("sysUpAnnounceText").textContent = up.announcement || "";
      $("sysUpChangelogList").innerHTML = (up.changelog || [])
        .map((item) => `<li>${esc(item)}</li>`)
        .join("");
      if ($("announceUpdateInfo") && up.latestCommitSha) {
        $("announceUpdateInfo").textContent = `Latest GitHub Update [${up.latestCommitSha}]: ${up.latestCommitMsg} (${up.latestCommitDate})`;
      }
    };
    const setFetchProgress = (mode, pct, title, subHtml) => {
      const box = $("sysUpFetchBox");
      if (!box) return;
      box.hidden = false;
      box.className = "sysup-fetch-box" + (mode === "success" ? " success" : mode === "error" ? " error" : "");
      const sp = $("sysUpFetchSpinner");
      if (sp) sp.hidden = mode !== "loading";
      if ($("sysUpFetchTitle")) $("sysUpFetchTitle").textContent = title;
      if ($("sysUpFetchPct")) $("sysUpFetchPct").textContent = `${pct}%`;
      if ($("sysUpFetchBar")) $("sysUpFetchBar").style.width = `${pct}%`;
      if ($("sysUpFetchSub")) $("sysUpFetchSub").innerHTML = subHtml;
    };

    const refreshSystemUpdateModal = async (showLoadingBanner = false) => {
      const btn = $("btnSysUpCheckNow");
      btn.disabled = true;
      btn.textContent = "⏳ Checking...";
      if (showLoadingBanner) {
        setFetchProgress(
          "loading",
          35,
          "⏳ Loading & checking online GitHub release...",
          "Querying <code>dcquijano/Darius-DB-Lite-Version</code> version.json, commits &amp; releases..."
        );
      }
      try {
        const up = await api().CheckSystemUpdate();
        applyUpdateInfoToUI(up);
        if (up.fetchOk === false) {
          setFetchProgress(
            "error",
            100,
            "⚠️ Could Not Fetch Update from GitHub",
            esc(up.fetchError || "GitHub returned an error. Check internet connection or repository visibility.")
          );
          if (showLoadingBanner) {
            toast(up.fetchError || "Failed to fetch update from GitHub", "err");
          }
        } else if (showLoadingBanner) {
          setFetchProgress(
            "success",
            100,
            up.hasUpdate
              ? `🎉 Update Available: Lite Version ${up.latestVersion}!`
              : "✅ Successfully Checked Online GitHub Release!",
            `Latest Release: <strong>Lite Version ${esc(up.latestVersion)}</strong>` +
            (up.latestCommitSha
              ? ` · Commit <code>${esc(up.latestCommitSha)}</code>: "${esc(up.latestCommitMsg)}"`
              : "")
          );
          toast(
            up.hasUpdate
              ? `🎉 Update available: Darius DB Lite Version ${up.latestVersion}!`
              : `✅ Successfully checked online release (Lite Version ${up.latestVersion})`,
            "ok"
          );
        }
      } catch (e) {
        if (showLoadingBanner) {
          setFetchProgress("error", 100, "⚠️ Failed to check online updates", esc(errMsg(e)));
        }
        toast(errMsg(e), "err");
      } finally {
        btn.disabled = false;
        btn.textContent = "↻ Check Online";
      }
    };
    if ($("btnSysUpSyncGithub")) {
      $("btnSysUpSyncGithub").onclick = async () => {
        const btn = $("btnSysUpSyncGithub");
        btn.disabled = true;
        btn.textContent = "⏳ Loading & Fetching...";
        setFetchProgress(
          "loading",
          25,
          "⏳ Loading & fetching latest updates from GitHub...",
          "Step 1/3: Connecting to <code>github.com/dcquijano/Darius-DB-Lite-Version</code>..."
        );
        const stepTimer = setTimeout(() => {
          setFetchProgress(
            "loading",
            68,
            "⏳ Fetching version.json, recent commits & engine catalog...",
            "Step 2/3: Downloading latest commit history &amp; portable catalog.json..."
          );
        }, 280);
        try {
          const up = await api().SyncFromGitHub();
          clearTimeout(stepTimer);
          applyUpdateInfoToUI(up);
          state.catalog = await api().Catalog();
          if (state.tab === "versions") renderVersions();
          if (up.fetchOk === false) {
            setFetchProgress(
              "error",
              100,
              "⚠️ Could Not Fetch Updates from GitHub",
              esc(up.fetchError || "GitHub returned HTTP 404. If the repo is Private, make it Public or save a GitHub Token below.")
            );
            toast(up.fetchError || "Could not fetch updates from GitHub", "err");
            btn.disabled = false;
            btn.textContent = "🔄 Fetch Latest from GitHub";
            return;
          }
          setFetchProgress(
            "success",
            100,
            up.hasUpdate
              ? `🎉 Update Found: Lite Version ${up.latestVersion} Ready to Install!`
              : "✅ Successfully Fetched Latest Updates from GitHub!",
            `Synced <strong>Darius DB (Lite Version ${esc(up.latestVersion)})</strong>` +
            (up.latestCommitSha
              ? ` · Latest Commit <code>${esc(up.latestCommitSha)}</code>: <strong>${esc(
                up.latestCommitMsg
              )}</strong> (${esc(up.latestCommitDate)})`
              : " · Engine catalog &amp; version.json synced!")
          );
          btn.textContent = "✅ Successfully Fetched!";
          toast(
            up.hasUpdate
              ? `🎉 New update v${up.latestVersion} found! Click 'Download & Install Update Now' to upgrade.`
              : up.latestCommitSha
                ? `✅ Successfully fetched latest GitHub update [${up.latestCommitSha}]: ${up.latestCommitMsg}`
                : "✅ Successfully fetched latest version.json and catalog from GitHub!",
            "ok"
          );
          setTimeout(() => {
            btn.disabled = false;
            btn.textContent = "🔄 Fetch Latest from GitHub";
          }, 2200);
        } catch (e) {
          clearTimeout(stepTimer);
          setFetchProgress("error", 100, "⚠️ Failed to fetch from GitHub", esc(errMsg(e)));
          toast(errMsg(e), "err");
          btn.disabled = false;
          btn.textContent = "🔄 Fetch Latest from GitHub";
        }
      };
    }
    if ($("btnSysUpInstallNow")) {
      $("btnSysUpInstallNow").onclick = async () => {
        const up = latestUpdateInfo;
        const primary = up?.directSetupUrl || "";
        const fallback = up?.fallbackSetupUrl || up?.directZipUrl || "";
        const btn = $("btnSysUpInstallNow");
        btn.disabled = true;
        btn.textContent = "⏳ Downloading Update...";
        setFetchProgress(
          "loading",
          50,
          "⏳ Downloading Darius DB update package...",
          `Downloading installer from GitHub and preparing safe in-place upgrade...`
        );
        try {
          const msg = await api().ApplySystemUpdateAuto(primary, fallback);
          if (msg) {
            setFetchProgress("success", 100, "✅ Update Downloaded — Restarting Darius DB...", esc(msg));
            toast(msg, "ok");
          }
        } catch (e) {
          setFetchProgress("error", 100, "⚠️ Failed to Download/Install Update", esc(errMsg(e)));
          toast(errMsg(e), "err");
          btn.disabled = false;
          btn.textContent = "⬆️ Download & Install Update Now";
        }
      };
    }
    if ($("btnSysUpSaveToken")) {
      $("btnSysUpSaveToken").onclick = async () => {
        const tok = $("sysUpGithubToken").value.trim();
        try {
          await api().SaveGitHubToken(tok);
          toast(tok ? "Saved GitHub Token locally in settings.json — retrying fetch..." : "Cleared saved GitHub Token", "ok");
          await refreshSystemUpdateModal(true);
        } catch (e) {
          toast(errMsg(e), "err");
        }
      };
    }
    if ($("btnSysUpClearToken")) {
      $("btnSysUpClearToken").onclick = async () => {
        try {
          $("sysUpGithubToken").value = "";
          await api().SaveGitHubToken("");
          toast("Cleared saved token — retrying with Windows Git credentials...", "info");
          await refreshSystemUpdateModal(true);
        } catch (e) {
          toast(errMsg(e), "err");
        }
      };
    }
    const openSysUpdater = () => {
      if ($("sysUpFetchBox")) $("sysUpFetchBox").hidden = true;
      if ($("sysUpReadyBar")) $("sysUpReadyBar").hidden = true;
      $("modalSysUpdater").hidden = false;
      refreshSystemUpdateModal(false);
    };
    $("btnOpenSysUpdater").onclick = openSysUpdater;
    if ($("btnAnnounceSysUpdate")) $("btnAnnounceSysUpdate").onclick = openSysUpdater;
    $("btnCloseSysUpdater").onclick = closeSysUpdater;
    $("btnCancelSysUpdater").onclick = closeSysUpdater;
    $("btnSysUpCheckNow").onclick = () => refreshSystemUpdateModal(true);
    $("btnAnnounceCheckVer").onclick = async () => {
      const btn = $("btnAnnounceCheckVer");
      const infoEl = $("announceUpdateInfo");
      btn.disabled = true;
      btn.textContent = "Checking Newest Versions...";
      infoEl.textContent = "Checking official upstream servers (PHP, Mailpit, MariaDB, PostgreSQL, Redis, Apache, SQL Server)...";
      try {
        state.catalog = await api().RefreshCatalog();
        const updates = [];
        for (const s of state.statuses) {
          const entries = state.catalog[s.engine] || [];
          const newest = entries[0];
          if (!newest) continue;
          const insts = s.installations || [];
          if (!insts.length) {
            updates.push(`${s.name} ${newest.version} (ready to install)`);
          } else {
            const hasNewest = insts.some((i) => i.version === newest.version);
            if (!hasNewest && compareVer(newest.version, insts[0].version) > 0) {
              updates.push(`${s.name} ${newest.version} (update available)`);
            }
          }
        }
        infoEl.textContent = updates.length
          ? "Available to install / update: " + updates.join(" · ")
          : "All installed engines are on the latest available releases!";
        toast("Version catalog updated — choose an engine to install or update!", "ok");
        switchTab("versions");
      } catch (e) {
        infoEl.textContent = "Could not check online catalog: " + errMsg(e);
        toast(errMsg(e), "err");
      } finally {
        btn.disabled = false;
        btn.textContent = "↻ Check & Install New Versions";
      }
    };

    const closeRepair = () => ($("modalRepair").hidden = true);
    const runRepairAction = async () => {
      const target = $("repairTarget").value || "all";
      const resetCfg = !!$("repairResetCfg").checked;
      const btn = $("btnRunRepair");
      const box = $("repairReportBox");
      btn.disabled = true;
      btn.textContent = "Repairing...";
      box.hidden = false;
      box.innerHTML = `<div class="muted">Running diagnostics &amp; repairing ${esc(target)}...</div>`;
      try {
        const rep = await api().Repair(target, resetCfg);
        const rows = (rep.items || [])
          .map((it) => {
            const pillClass = it.status === "fixed" ? "running" : it.status === "warn" ? "warn" : "accent";
            const badgeText = it.status === "fixed" ? "FIXED" : it.status === "warn" ? "WARN" : "OK";
            return `<div style="display:flex;align-items:flex-start;gap:8px;padding:4px 0;border-bottom:1px solid var(--border)">
              <span class="pill ${pillClass}" style="min-width:48px;text-align:center">${badgeText}</span>
              <strong style="min-width:105px">${esc(it.category)}</strong>
              <span style="flex:1">${esc(it.action)}</span>
            </div>`;
          })
          .join("");
        box.innerHTML = `<div style="font-weight:600;margin-bottom:8px;color:var(--accent)">${esc(rep.summary)}</div>${rows}`;
        state.statuses = (await api().Status()) || [];
        renderDashboard();
        toast(rep.summary, rep.fixedCount > 0 ? "ok" : "info");
      } catch (e) {
        box.innerHTML = `<div style="color:var(--danger)">Repair error: ${esc(errMsg(e))}</div>`;
        toast(errMsg(e), "err");
      } finally {
        btn.disabled = false;
        btn.textContent = "🔧 Run Repair Now";
      }
    };
    const openRepairModal = (target = "all", autoRun = false) => {
      $("repairTarget").value = target || "all";
      $("repairResetCfg").checked = false;
      $("repairReportBox").hidden = !autoRun;
      if (!autoRun) $("repairReportBox").innerHTML = "";
      $("modalRepair").hidden = false;
      if (autoRun) runRepairAction();
    };
    $("btnOpenRepair").onclick = () => openRepairModal("all", false);
    $("btnCloseRepair").onclick = closeRepair;
    $("btnCancelRepair").onclick = closeRepair;
    $("btnRunRepair").onclick = runRepairAction;

    $("serviceGrid").addEventListener("click", async (e) => {
      const btn = e.target.closest("[data-act]");
      if (!btn) return;
      const eng = btn.dataset.engine;
      const act = btn.dataset.act;
      try {
        if (act === "start") await api().Start(eng);
        else if (act === "stop") await api().Stop(eng);
        else if (act === "restart") await api().Restart(eng);
        else if (act === "repair") openRepairModal(eng, true);
        else if (act === "install") {
          state.verEngine = eng;
          switchTab("versions");
        } else if (act === "opensite") {
          const url = `http://localhost:${btn.dataset.port || 8080}/`;
          if (rt()?.BrowserOpenURL) rt().BrowserOpenURL(url);
          else window.open(url, "_blank");
        } else if (act === "deploysite") {
          const url = await api().DeployWebsiteDialog();
          if (url) {
            toast(`Website deployed! Opening ${url}`, "ok");
            if (rt()?.BrowserOpenURL) rt().BrowserOpenURL(url);
            else window.open(url, "_blank");
          }
        } else if (act === "browse") {
          state.dbEngine = eng;
          switchTab("database");
        } else if (act === "config") {
          state.cfgEngine = eng;
          state.cfgMode = "config";
          switchTab("config");
        } else if (act === "logs") {
          state.cfgEngine = eng;
          state.cfgMode = "logs";
          switchTab("config");
        } else if (act === "data") {
          await api().OpenFolder("data:" + eng);
        } else if (act === "uri") {
          const uri = await api().ConnectionString(eng, "");
          await copyText(uri);
          toast(`Copied: ${uri}`, "ok");
        } else if (act === "copypdo") {
          const port = btn.dataset.port || "3306";
          const pdoSnippet = `<?php
$host = getenv('DB_HOST') ?: '127.0.0.1';
$port = getenv('DB_PORT') ?: '${port}';
$dbname = 'my_database';
$username = getenv('DB_USERNAME') ?: 'root';
$password = getenv('DB_PASSWORD') ?: '';

try {
    $pdo = new PDO(
        "mysql:host=$host;port=$port;dbname=$dbname;charset=utf8mb4",
        $username,
        $password,
        [
            PDO::ATTR_ERRMODE => PDO::ERRMODE_EXCEPTION,
            PDO::ATTR_DEFAULT_FETCH_MODE => PDO::FETCH_ASSOC,
            PDO::ATTR_EMULATE_PREPARES => false
        ]
    );
} catch (PDOException $e) {
    error_log($e->getMessage());
    http_response_code(500);
    exit('Database connection unavailable.');
}`;
          await copyText(pdoSnippet);
          toast(`Copied hardened PHP PDO connection snippet (utf8mb4 · port ${port})`, "ok");
        }
      } catch (err) {
        toast(errMsg(err), "err");
      }
    });

    $("serviceGrid").addEventListener("change", async (e) => {
      const sel = e.target.closest(".svc-ver");
      if (!sel) return;
      try {
        if (sel.dataset.mysqlUnified === "1" && sel.value.includes("|")) {
          const [targetEng, targetVer] = sel.value.split("|");
          const prevEng = sel.dataset.engine;
          const prevSvc = getStatus(prevEng);
          const wasRunning = prevSvc && prevSvc.state === "running";
          if (wasRunning && prevEng !== targetEng) {
            await api().Stop(prevEng);
          }
          await api().SetActive(targetEng, targetVer);
          if (wasRunning && prevEng !== targetEng) {
            await api().Start(targetEng);
          }
          toast(`Switched MySQL / MariaDB (Port 3306) to ${targetVer}`, "ok");
          return;
        }
        await api().SetActive(sel.dataset.engine, sel.value);
        if (sel.dataset.engine === "php") {
          toast(`Switched Apache PHP engine to ${sel.value}`, "ok");
        } else {
          toast(`Switched ${sel.dataset.engine} to ${sel.value}`, "ok");
        }
      } catch (err) {
        toast(errMsg(err), "err");
      }
    });

    // Global cancel download delegation
    document.body.addEventListener("click", (e) => {
      const btn = e.target.closest("[data-cancel-dl]");
      if (!btn) return;
      const [eng, ver] = btn.dataset.cancelDl.split("|");
      api().CancelInstall(eng, ver);
    });

    // Database tab events
    $("btnDbRefresh").onclick = () => loadDbView();

    const closeRestore = () => ($("modalRestore").hidden = true);
    const updateImportTableVisibility = () => {
      const fp = ($("restoreFilePath").value || "").trim().toLowerCase();
      const isCsv = fp.endsWith(".csv");
      $("restoreTableWrap").hidden = !isCsv;
      if (isCsv && !$("restoreTableName").value.trim()) {
        if (state.sqlTable) {
          $("restoreTableName").value = state.sqlTable;
        } else {
          const base = fp.split(/[\\/]/).pop().replace(/\.csv$/i, "").replace(/[^A-Za-z0-9_$\-]/g, "_");
          if (base) $("restoreTableName").value = base;
        }
      }
    };
    const openImportModal = (modeLabel) => {
      const s = getStatus(state.dbEngine);
      $("modalRestoreTitle").textContent =
        modeLabel === "restore" && state.dbEngine === "mssql"
          ? "Restore .bak Backup or Import .sql / .csv (MS SQL Server)"
          : `Import .sql / .csv into Database (${s ? s.name : state.dbEngine})`;
      $("formRestore").reset();
      $("restoreTableWrap").hidden = true;
      if (state.sqlDb && !SYSTEM_DBS.has(state.sqlDb.toLowerCase())) {
        $("restoreDbName").value = state.sqlDb;
      }
      $("modalRestore").hidden = false;
    };
    $("btnOpenImport").onclick = () => openImportModal("import");
    $("btnOpenRestore").onclick = () => openImportModal("restore");
    $("btnCloseRestore").onclick = closeRestore;
    $("btnCancelRestore").onclick = closeRestore;
    $("restoreFilePath").addEventListener("input", updateImportTableVisibility);
    $("btnBrowseRestore").onclick = async () => {
      try {
        const res = await api().PickRestoreFile(state.dbEngine);
        if (!res || !res.path) return;
        $("restoreFilePath").value = res.path;
        const curDb = $("restoreDbName").value.trim();
        if (!curDb || SYSTEM_DBS.has(curDb.toLowerCase())) {
          $("restoreDbName").value = res.suggestedDb || "";
        }
        updateImportTableVisibility();
      } catch (e) {
        toast(errMsg(e), "err");
      }
    };
    $("formRestore").onsubmit = async (e) => {
      e.preventDefault();
      const filePath = $("restoreFilePath").value.trim();
      const targetDb = $("restoreDbName").value.trim();
      const targetTable = $("restoreTableName").value.trim();
      if (!filePath) return;
      const btn = $("btnSubmitRestore");
      const orig = btn.textContent;
      btn.disabled = true;
      btn.textContent = "Importing...";
      try {
        const res = await api().ImportDatabaseFile(state.dbEngine, filePath, targetDb, targetTable);
        closeRestore();
        toast(res?.message || `Imported into "${res?.database || targetDb}"!`, "ok");
        if (res?.database) state.sqlDb = res.database;
        if (res?.table) state.sqlTable = res.table;
        await loadSqlDatabases();
      } catch (err) {
        toast(errMsg(err), "err");
      } finally {
        btn.disabled = false;
        btn.textContent = orig;
      }
    };

    let exportScope = "all";
    const closeExport = () => ($("modalExport").hidden = true);
    const updateExportCount = () => {
      const n = $("exportTableChecklist").querySelectorAll("input[type=checkbox]:checked").length;
      $("exportSelectedCount").textContent = `${n} table(s) selected`;
      const csvOpt = $("exportFormat").querySelector('option[value="csv"]');
      if (csvOpt) {
        const allowCsv = exportScope === "single" || (exportScope === "selected" && n === 1);
        csvOpt.disabled = !allowCsv;
        if (!allowCsv && $("exportFormat").value === "csv") {
          $("exportFormat").value = "sql";
        }
      }
      $("exportSqlOptions").hidden = $("exportFormat").value === "csv";
    };
    const renderExportChecklist = (preselectNames = []) => {
      const pre = new Set(preselectNames);
      const filter = ($("exportTableFilter").value || "").trim().toLowerCase();
      const tables = (state.sqlTables || []).filter((t) => !filter || t.name.toLowerCase().includes(filter));
      if (!tables.length) {
        $("exportTableChecklist").innerHTML = `<div class="empty-note">No matching tables</div>`;
      } else {
        $("exportTableChecklist").innerHTML = tables
          .map(
            (t) => `<label class="export-table-item" title="${esc(t.name)}">
              <input type="checkbox" value="${esc(t.name)}" ${pre.has(t.name) ? "checked" : ""} />
              <span>${esc(t.name)}</span>
            </label>`
          )
          .join("");
      }
      updateExportCount();
    };
    const setExportScope = (scope) => {
      exportScope = scope;
      document.querySelectorAll("#exportScopeTabs .seg").forEach((b) =>
        b.classList.toggle("active", b.dataset.expScope === scope)
      );
      $("exportTablePickerWrap").hidden = scope !== "selected";
      updateExportCount();
    };
    const openExportModal = (defaultScope = null) => {
      if (!state.sqlDb) {
        toast("Select a database on the left first", "err");
        return;
      }
      $("modalExportTitle").textContent = `📤 Export — ${state.sqlDb}`;
      const singleBtn = $("btnExpScopeSingle");
      if (state.sqlTable) {
        singleBtn.disabled = false;
        singleBtn.textContent = `Table: ${state.sqlTable}`;
      } else {
        singleBtn.disabled = true;
        singleBtn.textContent = "Current Table (none)";
      }
      $("exportFormat").value = "sql";
      $("exportIncSchema").checked = true;
      $("exportIncData").checked = true;
      $("exportTableFilter").value = "";
      renderExportChecklist(state.sqlTable ? [state.sqlTable] : []);
      const initialScope = defaultScope || (state.sqlTable ? "single" : "all");
      setExportScope(initialScope === "single" && !state.sqlTable ? "all" : initialScope);
      $("modalExport").hidden = false;
    };

    $("exportScopeTabs").addEventListener("click", (e) => {
      const b = e.target.closest("[data-exp-scope]");
      if (!b || b.disabled) return;
      setExportScope(b.dataset.expScope);
    });
    $("exportTableFilter").addEventListener("input", () => {
      const checked = Array.from($("exportTableChecklist").querySelectorAll("input:checked")).map((i) => i.value);
      renderExportChecklist(checked);
    });
    $("exportTableChecklist").addEventListener("change", updateExportCount);
    $("exportFormat").addEventListener("change", updateExportCount);
    $("btnExpSelectAll").onclick = () => {
      $("exportTableChecklist").querySelectorAll("input[type=checkbox]").forEach((c) => (c.checked = true));
      updateExportCount();
    };
    $("btnExpClearAll").onclick = () => {
      $("exportTableChecklist").querySelectorAll("input[type=checkbox]").forEach((c) => (c.checked = false));
      updateExportCount();
    };
    $("btnCloseExport").onclick = closeExport;
    $("btnCancelExport").onclick = closeExport;
    $("formExport").onsubmit = async (e) => {
      e.preventDefault();
      let tables = [];
      if (exportScope === "single") {
        if (!state.sqlTable) {
          toast("Select a table first", "err");
          return;
        }
        tables = [state.sqlTable];
      } else if (exportScope === "selected") {
        tables = Array.from($("exportTableChecklist").querySelectorAll("input[type=checkbox]:checked")).map((i) => i.value);
        if (!tables.length) {
          toast("Check at least one table to export", "err");
          return;
        }
      }
      const fmt = $("exportFormat").value || "sql";
      const incSchema = $("exportIncSchema").checked;
      const incData = $("exportIncData").checked;
      const btn = $("btnSubmitExport");
      btn.disabled = true;
      try {
        toast(`Exporting ${tables.length ? tables.join(", ") : state.sqlDb} (.${fmt})...`, "info");
        const outPath = await api().ExportDatabaseDialog(state.dbEngine, state.sqlDb, tables, fmt, incSchema, incData);
        if (outPath) {
          closeExport();
          toast(`Export saved: ${outPath}`, "ok");
        }
      } catch (err) {
        toast(errMsg(err), "err");
      } finally {
        btn.disabled = false;
      }
    };

    const runBackupDialog = async (fmt) => {
      if (!state.sqlDb) {
        toast("Select a database on the left first", "err");
        return;
      }
      try {
        toast(`Creating .${fmt} backup of "${state.sqlDb}"...`, "info");
        const outPath = await api().BackupDatabaseDialog(state.dbEngine, state.sqlDb, fmt);
        if (outPath) {
          toast(`Backup saved: ${outPath}`, "ok");
        }
      } catch (e) {
        toast(errMsg(e), "err");
      }
    };
    $("btnBackupBak").onclick = () => runBackupDialog("bak");
    $("btnBackupSql").onclick = () => openExportModal(state.sqlTable ? "single" : "all");
    if ($("btnMetaExportTable")) $("btnMetaExportTable").onclick = () => openExportModal("single");

    $("btnCopyIDE").onclick = async () => {
      try {
        const snippet = await api().IDESnippet(state.dbEngine, state.sqlDb || "my_app");
        await copyText(snippet);
        toast("Copied Visual Studio (appsettings.json) & VS Code (.env) snippet!", "ok");
      } catch (e) {
        toast(errMsg(e), "err");
      }
    };
    $("btnCopyConn").onclick = async () => {
      const uri = await api().ConnectionString(state.dbEngine, state.sqlDb);
      await copyText(uri);
      toast(`Copied: ${uri}`, "ok");
    };
    $("btnDbStartEngine").onclick = async () => {
      const s = getStatus(state.dbEngine);
      if (!s || !s.installations || !s.installations.length) {
        state.verEngine = state.dbEngine;
        switchTab("versions");
        return;
      }
      try {
        await api().Start(state.dbEngine);
      } catch (e) {
        toast(errMsg(e), "err");
      }
    };

    $("sqlDbList").addEventListener("click", async (e) => {
      const del = e.target.closest("[data-drop-db]");
      if (del) {
        e.stopPropagation();
        const name = del.dataset.dropDb;
        if (!(await askConfirm("Drop Database?", `Permanently drop database "${name}"? This cannot be undone.`, "Drop")))
          return;
        try {
          await api().DropDatabase(state.dbEngine, name);
          toast(`Dropped ${name}`, "ok");
          if (state.sqlDb === name) state.sqlDb = "";
          await loadSqlDatabases();
        } catch (err) {
          toast(errMsg(err), "err");
        }
        return;
      }
      const item = e.target.closest("[data-db]");
      if (!item) return;
      state.sqlDb = item.dataset.db;
      state.sqlTable = "";
      state.sqlColumns = [];
      state.sqlShowingAll = false;
      renderSqlDbList();
      await loadSqlTables(state.sqlDb);
    });

    $("sqlTableList").addEventListener("click", async (e) => {
      const dropBtn = e.target.closest("[data-drop-table]");
      if (dropBtn) {
        e.stopPropagation();
        const tbl = dropBtn.dataset.dropTable;
        if (!(await askConfirm("Drop Table?", `Permanently delete table "${tbl}" from "${state.sqlDb}"?`, "Drop")))
          return;
        try {
          await api().DropTable(state.dbEngine, state.sqlDb, tbl);
          toast(`Dropped table ${tbl}`, "ok");
          if (state.sqlTable === tbl) {
            state.sqlTable = "";
            state.sqlColumns = [];
            $("sqlResults").innerHTML = "";
          }
          await loadSqlTables(state.sqlDb);
        } catch (err) {
          toast(errMsg(err), "err");
        }
        return;
      }
      const item = e.target.closest("[data-table-idx]");
      if (!item) return;
      const t = state.sqlTables[Number(item.dataset.tableIdx)];
      await selectSqlTable(t, false);
    });

    const triggerShowAll = async () => {
      const t = state.sqlTables.find((x) => x.name === state.sqlTable);
      if (t) {
        await selectSqlTable(t, true);
      } else if ($("sqlEditor").value.trim()) {
        // Strip trailing LIMIT N if present so Show All returns every row
        $("sqlEditor").value = $("sqlEditor").value.replace(/\s+LIMIT\s+\d+\s*;?\s*$/i, ";");
        await runSqlQuery(true);
      } else {
        toast("Select a table or enter a SELECT query first", "info");
      }
    };
    $("btnRowCreate").onclick = () => openRowFormModal("create");
    $("btnMetaCreateRow").onclick = () => openRowFormModal("create");
    $("btnSqlShowAll").onclick = triggerShowAll;
    $("btnMetaShowAll").onclick = triggerShowAll;
    $("sqlRowFilter").addEventListener("input", () => {
      if (state.sqlLastResult) renderSqlTable(state.sqlLastResult);
    });

    $("sqlResults").addEventListener("click", async (e) => {
      const btn = e.target.closest("[data-row-act]");
      if (!btn) return;
      const idx = Number(btn.dataset.rowIdx);
      const act = btn.dataset.rowAct;
      if (act === "show") openShowRowModal(idx);
      else if (act === "edit") await openRowFormModal("edit", idx);
      else if (act === "delete") await deleteSqlRow(idx);
    });
    $("sqlResults").addEventListener("dblclick", (e) => {
      const tr = e.target.closest("tbody tr[data-row-idx]");
      if (!tr || !state.sqlTable) return;
      openShowRowModal(Number(tr.dataset.rowIdx));
    });

    $("btnCloseRowShow").onclick = () => ($("modalRowShow").hidden = true);
    $("btnDoneRowShow").onclick = () => ($("modalRowShow").hidden = true);
    $("btnCloseRowForm").onclick = () => ($("modalRowForm").hidden = true);
    $("btnCancelRowForm").onclick = () => ($("modalRowForm").hidden = true);

    // Create Table modal
    const appendNewTableColRow = (name = "", type = "VARCHAR(255)", nullable = true, pk = false, auto = false) => {
      const row = document.createElement("div");
      row.className = "new-col-row";
      row.innerHTML = `
        <input type="text" class="input xs tbl-col-name" placeholder="column_name" value="${esc(name)}" required />
        <select class="select sm tbl-col-type">
          ${["INT", "BIGINT", "VARCHAR(255)", "TEXT", "DECIMAL(10,2)", "BOOLEAN", "DATE", "DATETIME", "TIMESTAMP"]
          .map((tp) => `<option value="${tp}" ${tp === type ? "selected" : ""}>${tp}</option>`)
          .join("")}
        </select>
        <label><input type="checkbox" class="tbl-col-null" ${nullable ? "checked" : ""} /> Null</label>
        <label><input type="checkbox" class="tbl-col-pk" ${pk ? "checked" : ""} /> PK</label>
        <label><input type="checkbox" class="tbl-col-auto" ${auto ? "checked" : ""} /> Auto</label>
        <button type="button" class="btn xs danger-ghost tbl-col-rm" title="Remove column">✕</button>
      `;
      row.querySelector(".tbl-col-rm").onclick = () => row.remove();
      $("newTableCols").appendChild(row);
    };
    const closeNewTable = () => ($("modalNewTable").hidden = true);
    $("btnNewTable").onclick = () => {
      if (!state.sqlDb) {
        toast("Select a database first", "err");
        return;
      }
      $("modalNewTableTitle").textContent = `Create Table in "${state.sqlDb}"`;
      $("formNewTable").reset();
      $("newTableCols").innerHTML = "";
      appendNewTableColRow("id", "INT", false, true, true);
      appendNewTableColRow("name", "VARCHAR(255)", false, false, false);
      appendNewTableColRow("created_at", "TIMESTAMP", true, false, false);
      $("modalNewTable").hidden = false;
      $("newTableName").focus();
    };
    $("btnCloseNewTable").onclick = closeNewTable;
    $("btnCancelNewTable").onclick = closeNewTable;
    $("btnAddTableCol").onclick = () => appendNewTableColRow();
    $("formNewTable").onsubmit = async (e) => {
      e.preventDefault();
      const tblName = $("newTableName").value.trim();
      const cols = Array.from($("newTableCols").querySelectorAll(".new-col-row")).map((r) => ({
        name: r.querySelector(".tbl-col-name").value.trim(),
        type: r.querySelector(".tbl-col-type").value,
        nullable: r.querySelector(".tbl-col-null").checked,
        primaryKey: r.querySelector(".tbl-col-pk").checked,
        autoIncrement: r.querySelector(".tbl-col-auto").checked,
      }));
      try {
        await api().CreateTable(state.dbEngine, state.sqlDb, tblName, cols);
        closeNewTable();
        toast(`Created table "${tblName}"`, "ok");
        await loadSqlTables(state.sqlDb);
        const created = state.sqlTables.find((t) => t.name === tblName);
        if (created) await selectSqlTable(created, false);
      } catch (err) {
        toast(errMsg(err), "err");
      }
    };

    $("btnSqlRun").onclick = () => runSqlQuery(false);
    $("btnSqlClear").onclick = () => {
      $("sqlEditor").value = "";
      $("sqlEditor").focus();
    };
    $("sqlEditor").addEventListener("keydown", (e) => {
      if ((e.ctrlKey || e.metaKey) && e.key === "Enter") {
        e.preventDefault();
        runSqlQuery(false);
      }
    });

    // New DB modal
    const closeNewDb = () => ($("modalNewDb").hidden = true);
    $("btnNewDb").onclick = () => {
      const s = getStatus(state.dbEngine);
      $("modalNewDbTitle").textContent = `Create ${s ? s.name : ""} Database`;
      $("formNewDb").reset();
      $("modalNewDb").hidden = false;
      $("newDbName").focus();
    };
    $("btnCloseNewDb").onclick = closeNewDb;
    $("btnCancelNewDb").onclick = closeNewDb;
    $("formNewDb").onsubmit = async (e) => {
      e.preventDefault();
      const name = $("newDbName").value.trim();
      const user = $("newDbUser").value.trim();
      const pass = $("newDbPass").value;
      try {
        await api().CreateDatabase(state.dbEngine, name, user, pass);
        closeNewDb();
        toast(user ? `Created database "${name}" & user "${user}"` : `Created database "${name}"`, "ok");
        state.sqlDb = name;
        await loadSqlDatabases();
      } catch (err) {
        toast(errMsg(err), "err");
      }
    };

    // Redis events
    $("redisDbSelect").onchange = () => {
      state.redisDb = parseInt($("redisDbSelect").value, 10) || 0;
      state.redisSelectedKey = "";
      $("redisInspector").innerHTML = `<div class="empty-note">Select a key to inspect it.</div>`;
      scanRedisKeys(false);
    };
    $("btnRedisRefresh").onclick = () => loadRedisDatabases();
    $("btnRedisSearch").onclick = () => scanRedisKeys(false);
    $("redisPattern").addEventListener("keydown", (e) => {
      if (e.key === "Enter") scanRedisKeys(false);
    });
    $("btnRedisMore").onclick = () => scanRedisKeys(true);
    $("redisKeyList").addEventListener("click", (e) => {
      const item = e.target.closest("[data-rkey]");
      if (item) inspectRedisKey(item.dataset.rkey);
    });
    $("redisCliForm").onsubmit = (e) => {
      e.preventDefault();
      const input = $("redisCliInput");
      const line = input.value.trim();
      if (!line) return;
      input.value = "";
      runRedisCli(line);
    };

    // Versions events
    $("btnOpenBin").onclick = () => api().OpenFolder("bin");
    $("btnInstallZip").onclick = async () => {
      try {
        const ver = await api().InstallFromZip(state.verEngine);
        if (ver) toast(`Extracting ${state.verEngine}-${ver}...`, "info");
      } catch (e) {
        toast(errMsg(e), "err");
      }
    };
    $("btnRefreshCatalog").onclick = async () => {
      const btn = $("btnRefreshCatalog");
      const orig = btn.textContent;
      btn.disabled = true;
      btn.textContent = "Checking...";
      try {
        state.catalog = await api().RefreshCatalog();
        renderVersions();
        const top = (state.catalog[state.verEngine] || [])[0];
        toast(
          top
            ? `Catalog updated! Newest ${state.verEngine}: ${top.version}`
            : "Checked official releases — catalog is up to date",
          "ok"
        );
      } catch (e) {
        toast(errMsg(e), "err");
      } finally {
        btn.disabled = false;
        btn.textContent = orig;
      }
    };

    const closeManual = () => ($("modalManual").hidden = true);
    $("btnManualModal").onclick = () => {
      const s = getStatus(state.verEngine);
      $("modalManualTitle").textContent = `Manual Download & Custom Version (${s ? s.name : state.verEngine})`;
      $("formManual").reset();
      $("modalManual").hidden = false;
      $("manualVer").focus();
    };
    $("btnCloseManual").onclick = closeManual;
    $("btnCancelManual").onclick = closeManual;
    $("btnOpenOfficialSite").onclick = () => {
      const url = OFFICIAL_DOWNLOAD_PAGES[state.verEngine] || OFFICIAL_DOWNLOAD_PAGES.mariadb;
      if (rt()?.BrowserOpenURL) {
        rt().BrowserOpenURL(url);
      } else {
        window.open(url, "_blank");
      }
      toast("Opened official download page in your web browser", "info");
    };
    $("btnModalPickZip").onclick = async () => {
      closeManual();
      try {
        const ver = await api().InstallFromZip(state.verEngine);
        if (ver) toast(`Extracting ${state.verEngine}-${ver}...`, "info");
      } catch (e) {
        toast(errMsg(e), "err");
      }
    };
    $("formManual").onsubmit = async (e) => {
      e.preventDefault();
      const ver = $("manualVer").value.trim();
      const url = $("manualUrl").value.trim();
      const note = $("manualNote").value.trim() || "Custom";
      try {
        state.catalog = await api().AddCatalogEntry(state.verEngine, ver, url, note);
        closeManual();
        await api().Install(state.verEngine, ver, url);
        state.downloads[`${state.verEngine}@${ver}`] = {
          engine: state.verEngine,
          version: ver,
          phase: "downloading",
          received: 0,
          total: 0,
        };
        renderActiveDownloads();
        renderVersions();
        toast(`Added & started downloading ${state.verEngine}-${ver}`, "ok");
      } catch (err) {
        toast(errMsg(err), "err");
      }
    };

    $("installedList").addEventListener("click", async (e) => {
      const dlBtn = e.target.closest("[data-dl-ver]");
      if (dlBtn) {
        const targetEng = dlBtn.dataset.dlEng || state.verEngine;
        try {
          await api().Install(targetEng, dlBtn.dataset.dlVer, dlBtn.dataset.dlUrl);
          state.downloads[`${targetEng}@${dlBtn.dataset.dlVer}`] = {
            engine: targetEng,
            version: dlBtn.dataset.dlVer,
            phase: "downloading",
            received: 0,
            total: 0,
          };
          renderActiveDownloads();
          renderVersions();
          toast(`Updating ${targetEng} to ${dlBtn.dataset.dlVer}...`, "info");
        } catch (err) {
          toast(errMsg(err), "err");
        }
        return;
      }
      const use = e.target.closest("[data-use-ver]");
      if (use) {
        const targetEng = use.dataset.itemEng || state.verEngine;
        try {
          if (targetEng === "mysql" || targetEng === "mariadb") {
            const otherEng = targetEng === "mysql" ? "mariadb" : "mysql";
            const otherSvc = getStatus(otherEng);
            if (otherSvc && otherSvc.state === "running") {
              await api().Stop(otherEng);
            }
          }
          await api().SetActive(targetEng, use.dataset.useVer);
          state.statuses = (await api().Status()) || [];
          renderVerEngineTabs();
          renderVersions();
          renderDashboard();
          toast(`Active version set to ${use.dataset.useVer}`, "ok");
        } catch (err) {
          toast(errMsg(err), "err");
        }
        return;
      }
      const del = e.target.closest("[data-del-ver]");
      if (del) {
        const name = del.dataset.delVer;
        const targetEng = del.dataset.itemEng || state.verEngine;
        if (
          !(await askConfirm(
            "Remove Engine Version?",
            `Remove ${name} from Darius DB? Your databases in data/ will NOT be touched.`,
            "Remove"
          ))
        )
          return;
        try {
          await api().Uninstall(targetEng, name);
          state.statuses = (await api().Status()) || [];
          renderVerEngineTabs();
          renderVersions();
          renderDashboard();
          toast(`Removed ${name}`, "ok");
        } catch (err) {
          toast(errMsg(err), "err");
        }
      }
    });
    $("catalogList").addEventListener("click", async (e) => {
      const manualBtn = e.target.closest("[data-manual-url]");
      if (manualBtn) {
        const url = manualBtn.dataset.manualUrl;
        if (rt()?.BrowserOpenURL) {
          rt().BrowserOpenURL(url);
        } else {
          window.open(url, "_blank");
        }
        toast("Opened direct .zip download in your browser — click 'Import .zip' once downloaded.", "info");
        return;
      }
      const btn = e.target.closest("[data-dl-ver]");
      if (!btn || btn.disabled) return;
      const targetEng = btn.dataset.dlEng || state.verEngine;
      btn.disabled = true;
      try {
        await api().Install(targetEng, btn.dataset.dlVer, btn.dataset.dlUrl);
        state.downloads[`${targetEng}@${btn.dataset.dlVer}`] = {
          engine: targetEng,
          version: btn.dataset.dlVer,
          phase: "downloading",
          received: 0,
          total: 0,
        };
        renderActiveDownloads();
        renderVersions();
      } catch (err) {
        btn.disabled = false;
        toast(errMsg(err), "err");
      }
    });

    // Config & Logs events
    $("cfgModeTabs").addEventListener("click", (e) => {
      const b = e.target.closest("[data-mode]");
      if (!b) return;
      state.cfgMode = b.dataset.mode;
      loadCfgOrLog();
    });
    $("btnCfgReload").onclick = loadConfigFile;
    $("btnCfgSave").onclick = () => saveConfigFile(false);
    $("btnCfgSaveRestart").onclick = () => saveConfigFile(true);
    $("btnCfgNotepad").onclick = () =>
      api().OpenConfigExternal(state.cfgEngine).catch((e) => toast(errMsg(e), "err"));
    $("cfgText").addEventListener("keydown", (e) => {
      if ((e.ctrlKey || e.metaKey) && e.key.toLowerCase() === "s") {
        e.preventDefault();
        if (!$("btnCfgSave").disabled) saveConfigFile(false);
      }
    });
    $("btnLogRefresh").onclick = loadLogFile;
    $("chkLogAuto").onchange = () => loadCfgOrLog();
    $("btnLogFolder").onclick = () => api().OpenFolder("logs");
    $("btnLogClear").onclick = async () => {
      try {
        await api().ClearLog(state.cfgEngine);
        await loadLogFile();
        toast("Log cleared", "ok");
      } catch (e) {
        toast(errMsg(e), "err");
      }
    };

    // Settings events
    $("btnSaveSettings").onclick = saveSettingsForm;
    $("setTheme").onchange = () => applyTheme($("setTheme").value);
    $("btnRegisterPath").onclick = async () => {
      try {
        await api().RegisterCLIInPath();
        toast("Registered 'darius' CLI & DB tools in Windows PATH! Restart VS / VS Code terminal.", "ok");
      } catch (e) {
        toast(errMsg(e), "err");
      }
    };
    $("folderList").addEventListener("click", (e) => {
      const b = e.target.closest("[data-open-folder]");
      if (b) api().OpenFolder(b.dataset.openFolder);
    });
    $("aboutGrid").addEventListener("click", (e) => {
      const a = e.target.closest("[data-ext-url]");
      if (!a) return;
      e.preventDefault();
      const url = a.dataset.extUrl;
      if (rt()?.BrowserOpenURL) rt().BrowserOpenURL(url);
      else window.open(url, "_blank");
    });

    // Built-in Terminal events
    $("termModeTabs").addEventListener("click", (e) => {
      const b = e.target.closest("[data-tmode]");
      if (b) switchTermMode(b.dataset.tmode);
    });
    $("termQuickBar").addEventListener("click", (e) => {
      const b = e.target.closest("[data-tcmd]");
      if (b) runTermCommand(b.dataset.tcmd);
    });
    $("termForm").onsubmit = (e) => {
      e.preventDefault();
      runTermCommand($("termInput").value);
    };
    $("btnTermClear").onclick = () => {
      $("termOutput").innerHTML = "";
      $("termInput").focus();
    };
    $("btnTermCancel").onclick = () => api().TerminalCancel();
    $("btnTermExternal").onclick = () =>
      api().OpenTerminal().catch((e) => toast(errMsg(e), "err"));
    $("termScreen").addEventListener("click", (e) => {
      if (window.getSelection()?.toString()) return;
      if (!e.target.closest("button")) $("termInput").focus();
    });
    $("termInput").addEventListener("keydown", (e) => {
      if (e.key === "ArrowUp") {
        e.preventDefault();
        if (state.termHistory.length && state.termHistIdx > 0) {
          state.termHistIdx--;
          $("termInput").value = state.termHistory[state.termHistIdx];
        }
      } else if (e.key === "ArrowDown") {
        e.preventDefault();
        if (state.termHistIdx < state.termHistory.length - 1) {
          state.termHistIdx++;
          $("termInput").value = state.termHistory[state.termHistIdx];
        } else {
          state.termHistIdx = state.termHistory.length;
          $("termInput").value = "";
        }
      } else if ((e.ctrlKey || e.metaKey) && e.key.toLowerCase() === "l") {
        e.preventDefault();
        $("termOutput").innerHTML = "";
      } else if (e.ctrlKey && e.key.toLowerCase() === "c" && !window.getSelection()?.toString()) {
        if (!$("btnTermCancel").hidden) {
          e.preventDefault();
          api().TerminalCancel();
        }
      }
    });

    // Confirm modal
    $("btnCloseConfirm").onclick = () => closeConfirm(false);
    $("btnConfirmCancel").onclick = () => closeConfirm(false);
    $("btnConfirmOk").onclick = () => closeConfirm(true);

    window.addEventListener("keydown", (e) => {
      if (e.key === "Escape") {
        if (!$("modalConfirm").hidden) closeConfirm(false);
        if (!$("modalNewDb").hidden) closeNewDb();
        if (!$("modalManual").hidden) closeManual();
        if (!$("modalRestore").hidden) closeRestore();
        if (!$("modalRepair").hidden) closeRepair();
        if (!$("modalSysUpdater").hidden) closeSysUpdater();
      }
    });
  }

  // ---- Init ----
  async function init() {
    wireEvents();

    const [info, settings, statuses, catalog] = await Promise.all([
      api().Info(),
      api().GetSettings(),
      api().Status(),
      api().Catalog(),
    ]);

    state.info = info;
    state.settings = settings;
    state.statuses = statuses || [];
    state.catalog = catalog || {};

    $("appVersion").textContent = "Lite Version " + info.version;
    if ($("dashSystemBadge")) {
      $("dashSystemBadge").textContent = "Darius DB · Lite Version " + info.version;
    }
    $("rootPathLabel").textContent = info.root;
    applyTheme(settings.theme);

    renderDashboard();

    onEvent("status", (list) => {
      const prev = getStatus(state.dbEngine)?.state;
      state.statuses = list || [];
      renderDashboard();
      if (state.tab === "versions") {
        renderVerEngineTabs();
        renderVersions();
      } else if (state.tab === "database") {
        renderDbEngineTabs();
        const now = getStatus(state.dbEngine)?.state;
        if (prev !== now) loadDbView();
      } else if (state.tab === "config") {
        renderCfgEngineTabs();
        const s = getStatus(state.cfgEngine);
        $("btnCfgSaveRestart").hidden = !(s && s.state === "running");
      }
    });

    onEvent("download", (p) => {
      const k = `${p.engine}@${p.version}`;
      if (p.phase === "done") {
        delete state.downloads[k];
        toast(p.message || `${p.engine}-${p.version} installed`, "ok");
      } else if (p.phase === "error") {
        delete state.downloads[k];
        toast(`${p.engine} ${p.version}: ${p.message}`, "err");
      } else if (p.phase === "cancelled") {
        delete state.downloads[k];
        toast(`Cancelled ${p.engine} ${p.version}`, "info");
      } else {
        state.downloads[k] = p;
      }
      renderActiveDownloads();
      if (state.tab === "versions") renderVersions();
    });

    onEvent("navigate", (tab) => {
      if (tab) switchTab(tab);
    });
  }

  window.addEventListener("DOMContentLoaded", () => {
    // Wait briefly if Wails runtime injects asynchronously
    const boot = () => {
      if (window.go?.main?.App && window.runtime) {
        init().catch((e) => toast(errMsg(e), "err"));
      } else {
        setTimeout(boot, 50);
      }
    };
    boot();
  });
})();
