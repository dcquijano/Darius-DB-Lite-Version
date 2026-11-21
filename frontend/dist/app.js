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
    cloudProfiles: [],
    schedules: [],
    winTask: { registered: false, detail: "" },
    downloads: {}, // key "engine@version" -> Progress
    dbEngine: "mariadb",
    sqlDb: "",
    sqlDbs: [],
    sqlTables: [],
    sqlTable: "",
    sqlColumns: [],
    sqlLastResult: null,
    sqlShowingAll: false,
    sqlPage: 1,
    sqlPageSize: 50,
    sqlColWidths: null,
    sqlTabs: [],
    activeSqlTabId: "",
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
    } else if (tab === "cloud") {
      renderCloudView();
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

  const activeSqlTabByEngine = new Map();
  let sqlTabIdSeq = 0;

  function engineTabs(engine = state.dbEngine) {
    return state.sqlTabs.filter((t) => t.engine === engine);
  }

  function getSqlTab(tabId) {
    return state.sqlTabs.find((t) => t.id === tabId) || null;
  }

  function getActiveSqlTab() {
    return getSqlTab(state.activeSqlTabId);
  }

  function isBlankSqlTab(tab) {
    return !!tab && !tab.table && !tab.result && !tab.loading && !(tab.sql || "").trim();
  }

  function nextQueryTabTitle(engine) {
    const used = new Set(engineTabs(engine).map((t) => t.title));
    let n = 1;
    while (used.has(`Query ${n}`)) n++;
    return `Query ${n}`;
  }

  function createSqlTabObj({ engine = state.dbEngine, db = state.sqlDb, table = "", title = "", sql = "", pageSize = state.sqlPageSize || 50 } = {}) {
    const id = `sqltab-${++sqlTabIdSeq}`;
    return {
      id,
      title: title || table || nextQueryTabTitle(engine),
      engine,
      db: db || "",
      table,
      columns: [],
      sql: sql || "",
      result: null,
      showingAll: false,
      page: 1,
      pageSize: pageSize ?? 50,
      filter: "",
      colWidths: null,
      metaText: "Ready",
      metaError: false,
      loading: false,
      reqSeq: 0,
    };
  }

  function syncActiveTabFromDom() {
    const tab = getActiveSqlTab();
    if (!tab) return;
    tab.table = state.sqlTable;
    tab.columns = state.sqlColumns;
    tab.result = state.sqlLastResult;
    tab.showingAll = state.sqlShowingAll;
    tab.page = state.sqlPage;
    tab.pageSize = state.sqlPageSize;
    tab.colWidths = state.sqlColWidths;
    if ($("sqlEditor")) tab.sql = $("sqlEditor").value;
    if ($("sqlRowFilter")) tab.filter = $("sqlRowFilter").value;
    if ($("sqlMeta")) {
      tab.metaText = $("sqlMeta").textContent;
      tab.metaError = $("sqlMeta").parentElement?.classList.contains("error") || false;
    }
  }

  function renderSqlTabs() {
    const el = $("sqlTabList");
    if (!el) return;
    const tabs = engineTabs(state.dbEngine);
    const multiDb = new Set(tabs.map((t) => t.db).filter(Boolean)).size > 1;
    el.innerHTML = tabs
      .map((t) => {
        const active = t.id === state.activeSqlTabId;
        const label = t.title || t.table || "Query";
        const dbHint = multiDb && t.db ? ` (${t.db})` : "";
        const tip = `${label}${t.db ? ` — ${t.db}` : ""}`;
        return `<div class="sql-tab ${active ? "active" : ""}" data-sqltab-id="${esc(t.id)}" role="tab" aria-selected="${active ? "true" : "false"}" title="${esc(tip)}">
          ${t.loading ? `<span class="sql-tab-dot" title="Running query..."></span>` : ""}
          <span class="sql-tab-title">${esc(label)}${dbHint ? `<span class="sql-tab-db">${esc(dbHint)}</span>` : ""}</span>
          <button type="button" class="sql-tab-close" data-close-sqltab="${esc(t.id)}" title="Close tab">✕</button>
        </div>`;
      })
      .join("");
    const activeEl = el.querySelector(".sql-tab.active");
    if (activeEl && activeEl.scrollIntoView) {
      activeEl.scrollIntoView({ block: "nearest", inline: "nearest" });
    }
  }

  function applyTabToDom(tab, { syncSidebar = false } = {}) {
    if (!tab) return;
    state.activeSqlTabId = tab.id;
    activeSqlTabByEngine.set(tab.engine, tab.id);
    state.sqlTable = tab.table || "";
    state.sqlColumns = tab.columns || [];
    state.sqlLastResult = tab.result || null;
    state.sqlShowingAll = !!tab.showingAll;
    state.sqlPage = tab.page || 1;
    state.sqlPageSize = tab.pageSize !== undefined ? tab.pageSize : 50;
    state.sqlColWidths = tab.colWidths || null;

    if ($("sqlEditor")) $("sqlEditor").value = tab.sql || "";
    if ($("sqlRowFilter")) $("sqlRowFilter").value = tab.filter || "";
    if ($("sqlPageSize")) $("sqlPageSize").value = String(state.sqlPageSize);
    const meta = $("sqlMeta");
    if (meta) {
      meta.textContent = tab.metaText || "Ready";
      meta.parentElement.className = tab.metaError ? "result-meta-bar error" : "result-meta-bar";
    }
    if (tab.result && tab.result.isQuery) {
      renderSqlTable(tab.result);
    } else {
      if ($("sqlResults")) $("sqlResults").innerHTML = "";
      hideSqlPager();
    }
    updateSqlTableToolbar();
    renderSqlTabs();

    if (syncSidebar && tab.db && tab.db !== state.sqlDb && tab.engine === state.dbEngine) {
      state.sqlDb = tab.db;
      renderSqlDbList();
      loadSqlTables(tab.db, false);
    } else {
      if ($("sqlActiveDb")) {
        const displayDb = tab.db || state.sqlDb;
        $("sqlActiveDb").textContent = displayDb ? `${state.dbEngine} / ${displayDb}` : "No DB selected";
      }
      renderSqlTableList();
    }
  }

  function ensureActiveSqlTab() {
    let tab = getActiveSqlTab();
    if (tab && tab.engine === state.dbEngine) return tab;
    const savedId = activeSqlTabByEngine.get(state.dbEngine);
    tab = (savedId && getSqlTab(savedId)) || engineTabs(state.dbEngine)[0] || null;
    if (!tab) {
      tab = createSqlTabObj({ engine: state.dbEngine, db: state.sqlDb });
      state.sqlTabs.push(tab);
    }
    applyTabToDom(tab);
    return tab;
  }

  function openNewSqlTab({ focus = true } = {}) {
    syncActiveTabFromDom();
    const tab = createSqlTabObj({ engine: state.dbEngine, db: state.sqlDb });
    state.sqlTabs.push(tab);
    applyTabToDom(tab);
    if (focus && $("sqlEditor")) $("sqlEditor").focus();
    return tab;
  }

  function closeSqlTab(tabId) {
    const idx = state.sqlTabs.findIndex((t) => t.id === tabId);
    if (idx < 0) return;
    const closing = state.sqlTabs[idx];
    const siblingsBefore = engineTabs(closing.engine);
    const sibIdx = siblingsBefore.findIndex((t) => t.id === tabId);
    state.sqlTabs.splice(idx, 1);

    if (closing.engine !== state.dbEngine) {
      if (activeSqlTabByEngine.get(closing.engine) === tabId) {
        const rem = engineTabs(closing.engine);
        if (rem.length) activeSqlTabByEngine.set(closing.engine, rem[Math.min(sibIdx, rem.length - 1)].id);
        else activeSqlTabByEngine.delete(closing.engine);
      }
      return;
    }

    if (state.activeSqlTabId === tabId) {
      const remaining = engineTabs(state.dbEngine);
      if (remaining.length > 0) {
        applyTabToDom(remaining[Math.min(sibIdx, remaining.length - 1)], { syncSidebar: true });
      } else {
        const fresh = createSqlTabObj({ engine: state.dbEngine, db: state.sqlDb });
        state.sqlTabs.push(fresh);
        applyTabToDom(fresh);
      }
    } else {
      renderSqlTabs();
    }
  }

  function resetSqlBrowserState(nextDb = "") {
    const prevTab = getActiveSqlTab();
    if (prevTab) {
      syncActiveTabFromDom();
      activeSqlTabByEngine.set(prevTab.engine, prevTab.id);
    }
    state.sqlDb = nextDb;
    state.sqlDbs = [];
    state.sqlTables = [];
    state.activeSqlTabId = "";
    const restoredId = activeSqlTabByEngine.get(state.dbEngine);
    let nextTab = (restoredId && getSqlTab(restoredId)) || engineTabs(state.dbEngine)[0] || null;
    if (!nextTab) {
      nextTab = createSqlTabObj({ engine: state.dbEngine, db: nextDb });
      state.sqlTabs.push(nextTab);
    } else if (nextDb && isBlankSqlTab(nextTab)) {
      nextTab.db = nextDb;
    }
    if (nextTab.db && !nextDb) state.sqlDb = nextTab.db;
    applyTabToDom(nextTab);
    if ($("sqlDbsCount")) $("sqlDbsCount").hidden = true;
    if ($("sqlTablesTitle")) {
      $("sqlTablesTitle").textContent = state.sqlDb ? `Tables (${state.sqlDb})` : "Tables";
      $("sqlTablesTitle").title = state.sqlDb ? `Tables in ${state.sqlDb}` : "Tables";
    }
    if ($("sqlTablesCount")) $("sqlTablesCount").hidden = true;
  }

  function pickBestDbEngine() {
    if (String(state.dbEngine || "").startsWith("cloud:")) {
      const id = state.dbEngine.slice(6);
      if (state.cloudProfiles.some((p) => p.id === id)) return;
    }
    const prevEng = state.dbEngine;
    const cur = getStatus(state.dbEngine);
    if (cur && cur.engine !== "apache" && cur.engine !== "php" && cur.state === "running") return;
    const running = state.statuses.find(
      (s) => s.engine !== "apache" && s.engine !== "php" && s.engine !== "mailpit" && s.state === "running"
    );
    if (running) state.dbEngine = running.engine;
    else if (state.dbEngine === "apache" || state.dbEngine === "php" || String(state.dbEngine || "").startsWith("cloud:")) state.dbEngine = "mariadb";
    if (state.dbEngine !== prevEng) {
      resetSqlBrowserState("");
    }
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

    const engSettings = state.settings?.engines || {};
    const autoRunOn = !!state.settings?.autoStartServices;
    const isAutoEng = (id) => !!engSettings[id]?.enabled;
    const autoNames = visibleStatuses
      .filter((s) => (s.installations && s.installations.length > 0) && isAutoEng(s.engine))
      .map((s) => (s.engine === "apache" ? "Apache" : s.name));

    const autoRunPillsHtml = visibleStatuses
      .map((s) => {
        const inst = s.installations && s.installations.length > 0;
        const on = isAutoEng(s.engine);
        const lbl = s.engine === "apache" ? "Apache+PHP" : s.name;
        return `<button type="button" class="auto-pill ${on ? "on" : ""}" data-act="toggleauto" data-engine="${s.engine}" ${!inst ? "disabled" : ""} title="${!inst ? esc(s.name) + " is not installed" : (on ? "Remove " : "Add ") + esc(s.name) + " to Auto Run & Start All"}">⚡ ${esc(lbl)}</button>`;
      })
      .join("");

    const autoRunBarHtml = `
      <div class="auto-run-bar">
        <div class="auto-run-left">
          <button type="button" class="auto-pill master ${autoRunOn ? "on" : ""}" data-act="toggleautorun" title="Automatically start checked services when Darius DB opens">
            ${autoRunOn ? "⚡ Auto Run: ON" : "⭘ Auto Run: OFF"}
          </button>
          <span class="muted" style="font-size:11.5px">
            ${autoNames.length ? "Selected: <strong>" + esc(autoNames.join(" + ")) + "</strong>" : "No services selected"}
          </span>
        </div>
        <div class="auto-run-pills">${autoRunPillsHtml}</div>
        <div class="auto-run-presets">
          <button type="button" class="btn xs ghost" data-preset-auto="mysql" title="Auto run MySQL only">MySQL</button>
          <button type="button" class="btn xs ghost" data-preset-auto="mysql+apache" title="Auto run MySQL + Apache (PHP)">MySQL + Apache</button>
          <button type="button" class="btn xs ghost" data-preset-auto="running" title="Auto run whichever services are currently ON">Use Running</button>
          <button type="button" class="btn xs ghost" data-preset-auto="none" title="Turn off all auto-run selections">Clear</button>
        </div>
      </div>`;

    const rowsHtml = visibleStatuses
      .map((s) => {
        const isApache = s.engine === "apache";
        const isMailpit = s.engine === "mailpit";
        const installed = s.installations && s.installations.length > 0;
        const busy = s.state === "starting" || s.state === "stopping";
        const running = s.state === "running";

        // Allow clicking an active or starting toggle to turn it OFF immediately
        const toggleAct = !installed ? "install" : (running || s.state === "starting") ? "stop" : "start";
        const toggleTitle = !installed
          ? `Click to install ${s.name}`
          : s.state === "starting"
            ? `Cancel & turn off ${s.name}`
            : running
              ? `Stop ${s.name}`
              : `Start ${s.name}`;
        const toggleDisabled = s.state === "stopping" ? "disabled" : "";
        const toggleBtn = `<button type="button" class="svc-toggle ${s.state}" data-act="${toggleAct}" data-engine="${s.engine}" ${toggleDisabled} title="${esc(toggleTitle)}"></button>`;

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

        // Right column: Reload + quick Auto-Run toggle + actions
        const autoOn = isAutoEng(s.engine);
        const autoRowBtn = installed
          ? `<button type="button" class="auto-pill ${autoOn ? "on" : ""}" data-act="toggleauto" data-engine="${s.engine}" title="${autoOn ? "Auto Run ON (click to disable)" : "Auto Run OFF (click to include in Auto Run & Start All)"}">⚡ Auto${autoOn ? ": ON" : ""}</button>`
          : "";
        let rightActions = "";
        if (!installed) {
          rightActions = `<button class="btn xs primary" data-act="install" data-engine="${s.engine}">Install</button>`;
        } else {
          const reloadBtn = `<button type="button" class="svc-reload-link" data-act="${running ? "restart" : "start"}" data-engine="${s.engine}" ${busy ? "disabled" : ""} title="${running ? "Reload " + esc(s.name) : "Start " + esc(s.name)}">Reload</button>`;
          if (isApache) {
            rightActions = `
              ${autoRowBtn}
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
              ${autoRowBtn}
              ${reloadBtn}
              ${running ? `<button class="btn xs accent" data-act="opensite" data-engine="mailpit" data-port="${s.port}">Inbox ↗</button>` : ""}
              <button class="btn xs ghost" data-act="uri" data-engine="mailpit" title="Copy SMTP &amp; Web Inbox URL">SMTP</button>
              <button class="btn xs ghost" data-act="config" data-engine="mailpit">Config</button>
              <button class="btn xs ghost" data-act="logs" data-engine="mailpit">Logs</button>
              <button class="btn xs ghost" data-act="repair" data-engine="mailpit">Repair</button>`;
          } else {
            rightActions = `
              ${autoRowBtn}
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

    $("serviceGrid").innerHTML = `${autoRunBarHtml}<div class="svc-list-panel">${rowsHtml}</div>${stackGuideHtml}`;
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

  function getActiveCloudProfile() {
    if (!String(state.dbEngine || "").startsWith("cloud:")) return null;
    const id = state.dbEngine.slice(6);
    return state.cloudProfiles.find((p) => p.id === id) || null;
  }

  // ---- Database Browser ----
  function renderDbEngineTabs() {
    const el = $("dbEngineTabs");
    const localTabs = state.statuses
      .filter((s) => s.isSql || s.engine === "redis")
      .map(
        (s) =>
          `<button class="seg ${s.engine === state.dbEngine ? "active" : ""}" data-engine="${s.engine}">
            <span class="dot ${s.state}"></span>${esc(s.name)}
          </button>`
      );
    const activeCloud = getActiveCloudProfile();
    if (activeCloud) {
      localTabs.push(
        `<button class="seg active" data-engine="cloud:${esc(activeCloud.id)}">☁️ ${esc(activeCloud.name)}</button>`
      );
    }
    el.innerHTML = localTabs.join("");
    el.querySelectorAll(".seg").forEach((btn) =>
      btn.addEventListener("click", () => {
        state.dbEngine = btn.dataset.engine;
        resetSqlBrowserState("");
        renderDbEngineTabs();
        loadDbView();
      })
    );
  }

  const SQL_CACHE_TTL_MS = 15000;
  const sqlDbsCache = new Map();
  const sqlTablesCache = new Map();
  const sqlSchemaCache = new Map();
  const sqlSchemaInFlight = new Map();
  let sqlDbsReqSeq = 0;
  let sqlTablesReqSeq = 0;
  let sqlQueryReqSeq = 0;

  function invalidateSqlCache(engine = "", db = "", table = "") {
    if (!engine) {
      sqlDbsCache.clear();
      sqlTablesCache.clear();
      sqlSchemaCache.clear();
      return;
    }
    const engPrefix = `${engine}|`;
    if (!db) {
      sqlDbsCache.delete(engine);
      for (const k of sqlTablesCache.keys()) if (k.startsWith(engPrefix)) sqlTablesCache.delete(k);
      for (const k of sqlSchemaCache.keys()) if (k.startsWith(engPrefix)) sqlSchemaCache.delete(k);
      return;
    }
    const dbKey = `${engine}|${db}`;
    const dbPrefix = `${dbKey}|`;
    if (!table) {
      sqlTablesCache.delete(dbKey);
      for (const k of sqlSchemaCache.keys()) if (k.startsWith(dbPrefix)) sqlSchemaCache.delete(k);
      return;
    }
    sqlSchemaCache.delete(`${dbKey}|${table}`);
  }

  async function ensureSqlColumns(engine, db, table) {
    if (!engine || !db || !table) return [];
    const key = `${engine}|${db}|${table}`;
    if (sqlSchemaCache.has(key)) return sqlSchemaCache.get(key);
    if (sqlSchemaInFlight.has(key)) return sqlSchemaInFlight.get(key);
    const p = api()
      .DescribeTable(engine, db, table)
      .then((cols) => {
        const list = cols || [];
        sqlSchemaCache.set(key, list);
        state.sqlTabs.forEach((t) => {
          if (t.engine === engine && t.db === db && t.table === table) t.columns = list;
        });
        if (state.dbEngine === engine && (getActiveSqlTab()?.db || state.sqlDb) === db && state.sqlTable === table) {
          state.sqlColumns = list;
        }
        return list;
      })
      .finally(() => {
        sqlSchemaInFlight.delete(key);
      });
    sqlSchemaInFlight.set(key, p);
    return p;
  }

  async function loadDbView(force = false) {
    const cloud = getActiveCloudProfile();
    const s = cloud ? null : getStatus(state.dbEngine);
    const stoppedEl = $("dbStoppedState");
    const sqlEl = $("sqlBrowser");
    const redisEl = $("redisBrowser");
    const cloudBar = $("cloudActiveBar");
    const isSql = cloud ? cloud.engine !== "redis" : (s ? s.isSql : state.dbEngine !== "redis");

    if (cloudBar) {
      cloudBar.hidden = !cloud;
      if (cloud) {
        $("cloudActiveTitle").textContent = cloud.name;
        $("cloudActiveMeta").textContent = `${(cloud.provider || "cloud").toUpperCase()} · ${cloud.engine.toUpperCase()} · ${cloud.host}:${cloud.port}${cloud.sslMode ? " (" + cloud.sslMode + ")" : ""}`;
      }
    }

    $("btnOpenImport").hidden = !isSql;
    $("btnOpenRestore").hidden = !!cloud || state.dbEngine !== "mssql";
    $("btnBackupSql").hidden = !isSql;
    if ($("btnBackupZip")) $("btnBackupZip").hidden = !isSql;
    $("btnBackupBak").hidden = !!cloud || state.dbEngine !== "mssql";

    if (!cloud && (!s || s.state !== "running")) {
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
    if (isSql) {
      redisEl.hidden = true;
      sqlEl.hidden = false;
      reapplySplitters();
      ensureActiveSqlTab();
      if (force) invalidateSqlCache(state.dbEngine);
      if (cloud && cloud.database && !state.sqlDb) state.sqlDb = cloud.database;
      await loadSqlDatabases(force);
    } else {
      sqlEl.hidden = true;
      redisEl.hidden = false;
      reapplySplitters();
      await loadRedisDatabases();
    }
  }

  function applySqlDbList(dbs) {
    state.sqlDbs = dbs || [];
    if (!state.sqlDb || !state.sqlDbs.includes(state.sqlDb)) {
      state.sqlDb =
        state.sqlDbs.find((d) => !SYSTEM_DBS.has(d.toLowerCase())) || state.sqlDbs[0] || "";
    }
    const curTab = getActiveSqlTab();
    if (curTab && isBlankSqlTab(curTab) && state.sqlDb) {
      curTab.db = state.sqlDb;
      renderSqlTabs();
    }
    renderSqlDbList();
  }

  async function refreshSqlDbsBackground(reqEngine) {
    try {
      const dbs = await api().ListDatabases(reqEngine);
      sqlDbsCache.set(reqEngine, { data: dbs || [], ts: Date.now() });
      if (state.dbEngine !== reqEngine) return;
      const prevDb = state.sqlDb;
      applySqlDbList(dbs);
      if (state.sqlDb && state.sqlDb !== prevDb) {
        await loadSqlTables(state.sqlDb, true);
      }
    } catch { }
  }

  async function loadSqlDatabases(force = false) {
    const reqEngine = state.dbEngine;
    const reqSeq = ++sqlDbsReqSeq;
    try {
      if (!force && sqlDbsCache.has(reqEngine)) {
        const cached = sqlDbsCache.get(reqEngine);
        applySqlDbList(cached.data);
        if (state.sqlDb) {
          await loadSqlTables(state.sqlDb, false);
        } else {
          state.sqlTables = [];
          state.sqlTable = "";
          state.sqlColumns = [];
          renderSqlTableList();
          updateSqlTableToolbar();
        }
        if (Date.now() - cached.ts > SQL_CACHE_TTL_MS) {
          refreshSqlDbsBackground(reqEngine);
        }
        return;
      }

      const knownDb = state.sqlDb || "";
      const canParallelTables = !!knownDb && (force || !sqlTablesCache.has(`${reqEngine}|${knownDb}`));
      const tablesPromise = canParallelTables ? loadSqlTables(knownDb, force) : null;

      const dbs = await api().ListDatabases(reqEngine);
      if (state.dbEngine !== reqEngine || reqSeq !== sqlDbsReqSeq) return;
      sqlDbsCache.set(reqEngine, { data: dbs || [], ts: Date.now() });
      applySqlDbList(dbs);

      if (state.sqlDb) {
        if (tablesPromise && state.sqlDb === knownDb) {
          await tablesPromise;
        } else {
          await loadSqlTables(state.sqlDb, force);
        }
      } else {
        state.sqlTables = [];
        state.sqlTable = "";
        state.sqlColumns = [];
        renderSqlTableList();
        updateSqlTableToolbar();
      }
    } catch (e) {
      if (state.dbEngine !== reqEngine || reqSeq !== sqlDbsReqSeq) return;
      toast(errMsg(e), "err");
    }
  }

  function renderSqlDbList() {
    $("sqlActiveDb").textContent = state.sqlDb ? `${state.dbEngine} / ${state.sqlDb}` : "No DB selected";
    const dbsCountEl = $("sqlDbsCount");
    if (dbsCountEl) {
      const dbCount = state.sqlDbs.length;
      dbsCountEl.textContent = String(dbCount);
      dbsCountEl.title = `${dbCount} database${dbCount === 1 ? "" : "s"}`;
      dbsCountEl.hidden = false;
    }
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

  async function loadSqlTables(db, force = false, background = false) {
    if (!db) return;
    const reqEngine = state.dbEngine;
    const cacheKey = `${reqEngine}|${db}`;
    if (!force && sqlTablesCache.has(cacheKey)) {
      const cached = sqlTablesCache.get(cacheKey);
      state.sqlTables = cached.data;
      const curTab = getActiveSqlTab();
      if (curTab && curTab.engine === reqEngine && curTab.db === db && state.sqlTable && !state.sqlTables.some((t) => t.name === state.sqlTable)) {
        state.sqlTable = "";
        state.sqlColumns = [];
        curTab.table = "";
        curTab.columns = [];
      }
      renderSqlTableList();
      updateSqlTableToolbar();
      if (Date.now() - cached.ts > SQL_CACHE_TTL_MS && !background) {
        loadSqlTables(db, true, true);
      }
      return;
    }

    const reqSeq = ++sqlTablesReqSeq;
    if (!background) {
      const titleEl = $("sqlTablesTitle");
      if (titleEl) {
        titleEl.textContent = `Tables (${db})`;
        titleEl.title = `Tables in ${db}`;
      }
      const countEl = $("sqlTablesCount");
      if (countEl) {
        countEl.textContent = "…";
        countEl.title = `Loading tables in ${db}...`;
        countEl.hidden = false;
      }
    }
    try {
      const tables = await api().ListTables(reqEngine, db);
      const list = tables || [];
      sqlTablesCache.set(cacheKey, { data: list, ts: Date.now() });
      if (state.dbEngine !== reqEngine || state.sqlDb !== db || (!background && reqSeq !== sqlTablesReqSeq)) return;
      state.sqlTables = list;
      const curTab = getActiveSqlTab();
      if (curTab && curTab.engine === reqEngine && curTab.db === db && state.sqlTable && !state.sqlTables.some((t) => t.name === state.sqlTable)) {
        state.sqlTable = "";
        state.sqlColumns = [];
        curTab.table = "";
        curTab.columns = [];
      }
      renderSqlTableList();
      updateSqlTableToolbar();
    } catch (e) {
      if (background || state.dbEngine !== reqEngine || state.sqlDb !== db || reqSeq !== sqlTablesReqSeq) return;
      state.sqlTables = [];
      const curTab = getActiveSqlTab();
      if (curTab && curTab.engine === reqEngine && curTab.db === db) {
        state.sqlTable = "";
        state.sqlColumns = [];
      }
      renderSqlTableList();
      updateSqlTableToolbar();
      toast(errMsg(e), "err");
    }
  }

  function renderSqlTableList() {
    const titleEl = $("sqlTablesTitle");
    if (titleEl) {
      titleEl.textContent = state.sqlDb ? `Tables (${state.sqlDb})` : "Tables";
      titleEl.title = state.sqlDb ? `Tables in ${state.sqlDb}` : "Tables";
    }
    const countEl = $("sqlTablesCount");
    if (countEl) {
      if (!state.sqlDb) {
        countEl.hidden = true;
      } else {
        const total = state.sqlTables.length;
        const viewCount = state.sqlTables.filter((t) => t.kind && t.kind.toUpperCase().includes("VIEW")).length;
        const tableCount = total - viewCount;
        countEl.textContent = String(total);
        countEl.hidden = false;
        countEl.title = viewCount > 0
          ? `${tableCount} table${tableCount === 1 ? "" : "s"}, ${viewCount} view${viewCount === 1 ? "" : "s"} (${total} total) in ${state.sqlDb}`
          : `${total} table${total === 1 ? "" : "s"} in ${state.sqlDb}`;
      }
    }
    const list = $("sqlTableList");
    if (!state.sqlTables.length) {
      list.innerHTML = `<div class="empty-note">No tables</div>`;
      return;
    }
    const curTab = getActiveSqlTab();
    const activeMatchDb = !curTab || !curTab.db || curTab.db === state.sqlDb;
    list.innerHTML = state.sqlTables
      .map((t, idx) => {
        const safeName = esc(t.name);
        const safeKind = esc(t.kind);
        const isView = t.kind && t.kind.toUpperCase().includes("VIEW");
        const isActive = activeMatchDb && t.name === state.sqlTable;
        return `<div class="tree-item ${isActive ? "active" : ""}" data-table-idx="${idx}" title="${safeName} (${safeKind})">
            <span class="tree-label">${safeName}</span>
            ${isView ? `<span class="type-tag">VIEW</span>` : `<button class="tree-del" data-drop-table="${safeName}" title="Drop table">✕</button>`}
          </div>`;
      })
      .join("");
  }

  function hideSqlPager() {
    if ($("sqlPagerBar")) $("sqlPagerBar").hidden = true;
    if ($("btnPagerLoadMax")) $("btnPagerLoadMax").hidden = true;
  }

  function detectSqlLimit(q) {
    const s = String(q || "").trim();
    const mLimit = s.match(/\bLIMIT\s+(\d+)\s*;?\s*$/i);
    if (mLimit) return Number(mLimit[1]) || 0;
    const mTop = s.match(/^\s*SELECT\s+TOP\s+(\d+)\s+/i);
    if (mTop) return Number(mTop[1]) || 0;
    return 0;
  }

  function applySqlQueryLimit(q, limit) {
    const s = String(q || "").trim();
    if (!s) return s;
    const hasSemi = /;\s*$/.test(s);
    const body = s.replace(/;\s*$/, "").trim();
    if (/\bLIMIT\s+\d+\s*$/i.test(body)) {
      const next = limit > 0
        ? body.replace(/\bLIMIT\s+\d+\s*$/i, `LIMIT ${limit}`)
        : body.replace(/\s+\bLIMIT\s+\d+\s*$/i, "");
      return hasSemi ? `${next};` : next;
    }
    if (/^\s*SELECT\s+TOP\s+\d+\s+/i.test(body)) {
      const next = limit > 0
        ? body.replace(/^(\s*SELECT\s+)TOP\s+\d+\s+/i, `$1TOP ${limit} `)
        : body.replace(/^(\s*SELECT\s+)TOP\s+\d+\s+/i, "$1");
      return hasSemi ? `${next};` : next;
    }
    return s;
  }

  async function selectSqlTable(tableObj, showAll = false, opts = {}) {
    if (!tableObj) return;
    syncActiveTabFromDom();
    const reqEngine = state.dbEngine;
    const reqDb = state.sqlDb;
    const existing = engineTabs(reqEngine).find((t) => t.db === reqDb && t.table === tableObj.name);
    let targetTab = null;

    if (existing && !opts.forceNew) {
      targetTab = existing;
      const wasActive = existing.id === state.activeSqlTabId;
      if (!wasActive) {
        applyTabToDom(existing);
        if (!showAll && (existing.result || existing.loading)) {
          return;
        }
      } else if (!showAll && existing.loading) {
        return;
      }
    } else {
      const cur = ensureActiveSqlTab();
      if (isBlankSqlTab(cur) && !opts.forceNew) {
        targetTab = cur;
        targetTab.engine = reqEngine;
        targetTab.db = reqDb;
        targetTab.table = tableObj.name;
        targetTab.title = tableObj.name;
      } else {
        targetTab = createSqlTabObj({
          engine: reqEngine,
          db: reqDb,
          table: tableObj.name,
          title: tableObj.name,
        });
        state.sqlTabs.push(targetTab);
      }
    }

    const fetchAll = !!showAll || targetTab.pageSize === 0;
    targetTab.table = tableObj.name;
    targetTab.title = tableObj.name;
    targetTab.db = reqDb;
    targetTab.showingAll = fetchAll;
    targetTab.page = 1;
    targetTab.columns = sqlSchemaCache.get(`${reqEngine}|${reqDb}|${tableObj.name}`) || [];
    let sql = tableObj.select;
    if (fetchAll) {
      sql = tableObj.selectAll || applySqlQueryLimit(tableObj.select, 0);
    } else if (targetTab.pageSize > 100) {
      sql = applySqlQueryLimit(tableObj.select, targetTab.pageSize);
    }
    targetTab.sql = sql;
    applyTabToDom(targetTab);

    await runSqlQuery(fetchAll || targetTab.pageSize > 1000, false, targetTab.id);
    if (
      state.dbEngine === reqEngine &&
      state.sqlDb === reqDb &&
      targetTab.table === tableObj.name &&
      !sqlSchemaCache.has(`${reqEngine}|${reqDb}|${tableObj.name}`)
    ) {
      ensureSqlColumns(reqEngine, reqDb, tableObj.name).catch(() => { });
    }
  }

  async function runSqlQuery(showAll = false, preservePage = false, explicitTabId = "") {
    syncActiveTabFromDom();
    const tab = (explicitTabId && getSqlTab(explicitTabId)) || ensureActiveSqlTab();
    const q = (tab.sql || "").trim();
    if (!q) return;
    const reqEngine = tab.engine;
    const reqDb = tab.db || state.sqlDb;
    tab.db = reqDb;
    const reqSeq = ++tab.reqSeq;
    tab.loading = true;
    if (!preservePage) {
      tab.page = 1;
      tab.filter = "";
    }
    tab.showingAll = !!showAll;
    tab.metaText = showAll ? "Loading all rows..." : "Running query...";
    tab.metaError = false;

    const isStillActive = () => state.activeSqlTabId === tab.id && state.dbEngine === tab.engine;
    if (isStillActive()) {
      state.sqlPage = tab.page;
      state.sqlShowingAll = tab.showingAll;
      if (!preservePage && $("sqlRowFilter")) $("sqlRowFilter").value = "";
      const meta = $("sqlMeta");
      if (meta) {
        meta.parentElement.className = "result-meta-bar";
        meta.textContent = tab.metaText;
      }
      if ($("sqlResults")) $("sqlResults").innerHTML = "";
      hideSqlPager();
    }
    renderSqlTabs();

    try {
      const res = showAll
        ? await api().RunQueryAll(reqEngine, reqDb, q)
        : await api().RunQuery(reqEngine, reqDb, q);
      if (!getSqlTab(tab.id) || tab.reqSeq !== reqSeq) return;
      tab.loading = false;
      tab.result = res;
      if (res.isQuery) {
        tab.metaText = `${res.rows.length} row(s)${res.truncated ? " (capped)" : showAll ? " (all)" : ""} · ${res.durationMs.toFixed(1)} ms`;
        tab.metaError = false;
      } else {
        tab.metaText = `Query OK · ${res.affected} row(s) affected · ${res.durationMs.toFixed(1)} ms`;
        tab.metaError = false;
        if (/\b(create|drop|alter|rename)\b/i.test(q)) {
          invalidateSqlCache(reqEngine);
          loadSqlDatabases(true);
        }
      }
      if (isStillActive()) {
        applyTabToDom(tab);
      } else {
        renderSqlTabs();
      }
    } catch (e) {
      if (!getSqlTab(tab.id) || tab.reqSeq !== reqSeq) return;
      tab.loading = false;
      tab.result = null;
      tab.metaText = errMsg(e);
      tab.metaError = true;
      if (isStillActive()) {
        applyTabToDom(tab);
      } else {
        renderSqlTabs();
      }
    }
  }

  function buildPageRange(current, total) {
    if (total <= 7) {
      return Array.from({ length: total }, (_, i) => i + 1);
    }
    const items = [1];
    let start = Math.max(2, current - 1);
    let end = Math.min(total - 1, current + 1);
    if (current <= 3) end = Math.min(total - 1, 4);
    if (current >= total - 2) start = Math.max(2, total - 3);
    if (start > 2) items.push("...");
    for (let p = start; p <= end; p++) items.push(p);
    if (end < total - 1) items.push("...");
    items.push(total);
    return items;
  }

  function renderSqlPager(res, total, totalUnfiltered, pageSize, totalPages, startRow, endRow) {
    const pager = $("sqlPagerBar");
    if (!pager) return;
    pager.hidden = false;

    const info = $("sqlPagerInfo");
    if (total === 0) {
      info.textContent = totalUnfiltered > 0 ? `Showing 0 of 0 matches (filtered from ${totalUnfiltered})` : "0 row(s)";
    } else {
      const base = `Showing ${startRow}–${endRow} of ${total} row(s)`;
      const filtNote = totalUnfiltered !== total ? ` (filtered from ${totalUnfiltered})` : "";
      const pageNote = ` · Page ${state.sqlPage}/${totalPages}`;
      info.textContent = base + filtNote + pageNote;
    }

    const sel = $("sqlPageSize");
    if (sel) sel.value = String(state.sqlPageSize);

    const loadMaxBtn = $("btnPagerLoadMax");
    if (loadMaxBtn) {
      const q = $("sqlEditor")?.value || "";
      const sqlLim = detectSqlLimit(q);
      const isLimited = !!res && ((sqlLim > 0 && res.rows.length >= sqlLim) || !!res.truncated);
      loadMaxBtn.hidden = !isLimited;
    }

    $("btnPageFirst").disabled = state.sqlPage <= 1 || total === 0;
    $("btnPagePrev").disabled = state.sqlPage <= 1 || total === 0;
    $("btnPageNext").disabled = state.sqlPage >= totalPages || total === 0;
    $("btnPageLast").disabled = state.sqlPage >= totalPages || total === 0;

    const nums = $("sqlPagerNumbers");
    if (nums) {
      const items = buildPageRange(state.sqlPage, totalPages);
      nums.innerHTML = items
        .map((it) =>
          it === "..."
            ? `<span class="pager-ellipsis">…</span>`
            : `<button type="button" class="btn xs pager-btn ${it === state.sqlPage ? "active" : ""}" data-page="${it}">${it}</button>`
        )
        .join("");
    }

    const jumpWrap = $("sqlPageJumpWrap");
    const jumpInp = $("sqlPageJump");
    if (jumpWrap && jumpInp) {
      jumpWrap.hidden = totalPages <= 5;
      jumpInp.max = String(totalPages);
      jumpInp.value = String(state.sqlPage);
    }
  }

  function renderSqlTable(res) {
    const wrap = $("sqlResults");
    if (!res || !res.columns.length) {
      wrap.innerHTML = `<div class="empty-note">Empty result set</div>`;
      hideSqlPager();
      return;
    }
    const filter = ($("sqlRowFilter")?.value || "").trim().toLowerCase();
    const canRowCrud = !!state.sqlTable;
    const indexedRows = [];
    for (let idx = 0; idx < res.rows.length; idx++) {
      const row = res.rows[idx];
      if (filter && !row.some((c) => c !== null && String(c).toLowerCase().includes(filter))) {
        continue;
      }
      indexedRows.push({ row, idx });
    }

    const total = indexedRows.length;
    const pageSize = state.sqlPageSize > 0 ? state.sqlPageSize : Math.max(total, 1);
    const totalPages = Math.max(1, Math.ceil(total / pageSize));
    state.sqlPage = Math.min(Math.max(1, state.sqlPage || 1), totalPages);

    const startIdx = (state.sqlPage - 1) * pageSize;
    const endIdx = Math.min(startIdx + pageSize, total);
    const visible = indexedRows.slice(startIdx, endIdx);

    const colsKey = `${state.sqlDb || ""}|${state.sqlTable || ""}|${canRowCrud ? "1:" : "0:"}${res.columns.join("\x00")}`;
    const colOffset = canRowCrud ? 1 : 0;
    const actHead = canRowCrud ? `<th class="row-acts-col">Actions</th>` : "";
    const thead = `<thead><tr>${actHead}${res.columns
      .map(
        (c, ci) =>
          `<th class="resizable-col" data-col-idx="${ci + colOffset}" title="${esc(c)}"><span class="th-label">${esc(c)}</span><span class="col-resizer" title="Drag to stretch column · Double-click to fit/reset"></span></th>`
      )
      .join("")}</tr></thead>`;
    const rowHtml = new Array(visible.length);
    for (let i = 0; i < visible.length; i++) {
      const { row, idx } = visible[i];
      const actCell = canRowCrud
        ? `<td class="row-acts-col"><div class="row-acts">
              <button type="button" class="row-btn show" data-row-act="show" data-row-idx="${idx}" title="Show full row details">Show</button>
              <button type="button" class="row-btn edit" data-row-act="edit" data-row-idx="${idx}" title="Edit this row">Edit</button>
              <button type="button" class="row-btn del" data-row-act="delete" data-row-idx="${idx}" title="Delete this row">Delete</button>
            </div></td>`
        : "";
      let cells = "";
      for (let c = 0; c < row.length; c++) {
        const cell = row[c];
        if (cell === null) {
          cells += `<td class="null-val">NULL</td>`;
        } else {
          const safeCell = esc(cell);
          cells += `<td title="${safeCell}">${safeCell}</td>`;
        }
      }
      rowHtml[i] = `<tr data-row-idx="${idx}">${actCell}${cells}</tr>`;
    }
    wrap.innerHTML = `<table class="data-table" data-cols-key="${esc(colsKey)}">${thead}<tbody>${rowHtml.join("")}</tbody></table>`;
    const tableEl = wrap.querySelector("table.data-table");
    if (tableEl && state.sqlColWidths && state.sqlColWidths.colsKey === colsKey) {
      applyTableColWidths(tableEl, state.sqlColWidths.widths);
    }
    wrap.scrollTop = 0;
    renderSqlPager(res, total, res.rows.length, pageSize, totalPages, total === 0 ? 0 : startIdx + 1, endIdx);
  }

  function applyTableColWidths(table, widths) {
    const ths = Array.from(table.querySelectorAll("thead th"));
    if (!ths.length || ths.length !== widths.length) return;
    let total = 0;
    ths.forEach((th, idx) => {
      const w = Math.max(48, Math.round(widths[idx]));
      th.style.width = `${w}px`;
      th.style.minWidth = `${w}px`;
      th.style.maxWidth = `${w}px`;
      total += w;
    });
    table.classList.add("is-col-resized");
    table.style.width = `${total}px`;
  }

  function captureTableColWidths(table) {
    return Array.from(table.querySelectorAll("thead th")).map((th) =>
      Math.max(48, Math.round(th.getBoundingClientRect().width))
    );
  }

  let textMeasureCtx = null;
  function measureTextPx(text, font) {
    if (!textMeasureCtx) {
      const canvas = document.createElement("canvas");
      textMeasureCtx = canvas.getContext("2d");
    }
    if (!textMeasureCtx) return String(text || "").length * 7.5;
    textMeasureCtx.font = font;
    return textMeasureCtx.measureText(String(text || "")).width;
  }

  function wireSqlColResizer() {
    const wrap = $("sqlResults");
    if (!wrap || wrap.dataset.colResizeWired) return;
    wrap.dataset.colResizeWired = "1";

    wrap.addEventListener("pointerdown", (e) => {
      const handle = e.target.closest(".col-resizer");
      if (!handle || e.button !== 0) return;
      const th = handle.closest("th");
      const table = th?.closest("table.data-table");
      if (!th || !table) return;
      e.preventDefault();
      e.stopPropagation();

      const ths = Array.from(table.querySelectorAll("thead th"));
      const colIdx = ths.indexOf(th);
      if (colIdx < 0) return;

      const widths = captureTableColWidths(table);
      applyTableColWidths(table, widths);
      const startX = e.clientX;
      const startW = widths[colIdx];
      handle.classList.add("active");
      document.body.classList.add("is-resizing-col");

      const onMove = (ev) => {
        widths[colIdx] = Math.max(56, Math.round(startW + (ev.clientX - startX)));
        applyTableColWidths(table, widths);
      };
      const onUp = () => {
        window.removeEventListener("pointermove", onMove);
        window.removeEventListener("pointerup", onUp);
        window.removeEventListener("pointercancel", onUp);
        handle.classList.remove("active");
        document.body.classList.remove("is-resizing-col");
        state.sqlColWidths = { colsKey: table.dataset.colsKey || "", widths: widths.slice() };
      };
      window.addEventListener("pointermove", onMove);
      window.addEventListener("pointerup", onUp);
      window.addEventListener("pointercancel", onUp);
    });

    wrap.addEventListener("dblclick", (e) => {
      const handle = e.target.closest(".col-resizer");
      if (!handle) return;
      const th = handle.closest("th");
      const table = th?.closest("table.data-table");
      if (!th || !table) return;
      e.preventDefault();
      e.stopPropagation();

      const ths = Array.from(table.querySelectorAll("thead th"));
      const colIdx = ths.indexOf(th);
      if (colIdx < 0) return;

      const headerTxt = th.querySelector(".th-label")?.textContent || th.textContent || "";
      let maxW = measureTextPx(headerTxt, '600 12px "Segoe UI", system-ui, sans-serif') + 10;
      const rows = table.querySelectorAll("tbody tr");
      for (let i = 0; i < rows.length; i++) {
        const cell = rows[i].children[colIdx];
        if (!cell) continue;
        const w = measureTextPx(cell.textContent, '12px "Cascadia Code", Consolas, monospace');
        if (w > maxW) maxW = w;
      }
      const fitW = Math.min(Math.max(Math.ceil(maxW) + 26, 68), 760);
      const widths = captureTableColWidths(table);
      widths[colIdx] = Math.abs(widths[colIdx] - fitW) <= 3 && widths[colIdx] > 180 ? 180 : fitW;
      applyTableColWidths(table, widths);
      state.sqlColWidths = { colsKey: table.dataset.colsKey || "", widths: widths.slice() };
    });
  }

  const SPLIT_PREFS_KEY = "dariusdb.ui.splits";
  const splitRefreshers = [];
  function reapplySplitters() {
    splitRefreshers.forEach((fn) => fn());
  }

  function readSplitPrefs() {
    try {
      const raw = localStorage.getItem(SPLIT_PREFS_KEY);
      return raw ? JSON.parse(raw) || {} : {};
    } catch {
      return {};
    }
  }

  function writeSplitPrefs(prefs) {
    try {
      localStorage.setItem(SPLIT_PREFS_KEY, JSON.stringify(prefs));
    } catch { }
  }

  function bindVerticalSplit(cfg) {
    const splitEl = $(cfg.splitId);
    const paneEl = $(cfg.paneId);
    const handleEl = $(cfg.handleId);
    if (!splitEl || !paneEl || !handleEl) return;

    const allPrefs = readSplitPrefs();
    const saved = allPrefs[cfg.storageKey] || {};
    let width = Number(saved.width) || cfg.defaultW;
    let collapsed = !!saved.collapsed;

    const save = () => {
      const next = readSplitPrefs();
      next[cfg.storageKey] = { width: Math.round(width), collapsed };
      writeSplitPrefs(next);
    };

    const getMaxW = () => {
      const cw = splitEl.clientWidth;
      return cw > 0 ? Math.max(cfg.minW, Math.min(cfg.maxW, cw - 260)) : cfg.maxW;
    };

    const apply = () => {
      const clampedW = Math.min(Math.max(width, cfg.minW), getMaxW());
      paneEl.classList.toggle("is-collapsed", collapsed);
      handleEl.classList.toggle("is-collapsed-a", collapsed);
      paneEl.style.width = collapsed ? "" : `${clampedW}px`;

      const btnCollapse = handleEl.querySelector('[data-split-act="collapse-a"]');
      const btnExpand = handleEl.querySelector('[data-split-act="expand-a"]');
      if (btnCollapse) btnCollapse.hidden = collapsed;
      if (btnExpand) {
        btnExpand.hidden = false;
        btnExpand.title = collapsed
          ? "Expand sidebar"
          : clampedW >= cfg.wideW - 8
            ? "Reset sidebar width"
            : "Widen sidebar";
      }
    };

    handleEl.addEventListener("click", (e) => {
      const btn = e.target.closest("[data-split-act]");
      if (!btn) return;
      e.stopPropagation();
      const act = btn.dataset.splitAct;
      if (act === "collapse-a") {
        collapsed = true;
      } else if (act === "expand-a") {
        if (collapsed) {
          collapsed = false;
        } else {
          width = width >= cfg.wideW - 8 ? cfg.defaultW : cfg.wideW;
        }
      }
      apply();
      save();
    });

    handleEl.addEventListener("dblclick", (e) => {
      if (e.target.closest(".split-btn")) return;
      if (collapsed || Math.abs(width - cfg.defaultW) > 6) {
        collapsed = false;
        width = cfg.defaultW;
      } else {
        width = cfg.wideW;
      }
      apply();
      save();
    });

    handleEl.addEventListener("pointerdown", (e) => {
      if (e.target.closest(".split-btn") || e.button !== 0) return;
      e.preventDefault();
      const startX = e.clientX;
      const startW = collapsed ? 0 : paneEl.getBoundingClientRect().width;
      handleEl.classList.add("is-dragging");
      document.body.classList.add("is-resizing-col");

      const onMove = (ev) => {
        const nextW = startW + (ev.clientX - startX);
        if (nextW < 60) {
          collapsed = true;
        } else {
          collapsed = false;
          width = Math.min(Math.max(nextW, cfg.minW), getMaxW());
        }
        apply();
      };
      const onUp = () => {
        window.removeEventListener("pointermove", onMove);
        window.removeEventListener("pointerup", onUp);
        window.removeEventListener("pointercancel", onUp);
        handleEl.classList.remove("is-dragging");
        document.body.classList.remove("is-resizing-col");
        save();
      };
      window.addEventListener("pointermove", onMove);
      window.addEventListener("pointerup", onUp);
      window.addEventListener("pointercancel", onUp);
    });

    splitRefreshers.push(apply);
    apply();
  }

  function bindHorizontalSplit(cfg) {
    const topEl = $(cfg.topId);
    const botEl = $(cfg.botId);
    const handleEl = $(cfg.handleId);
    if (!topEl || !botEl || !handleEl) return;

    const allPrefs = readSplitPrefs();
    const saved = allPrefs[cfg.storageKey] || {};
    let pct = Number(saved.pct) || cfg.defaultPct;
    let collapsed = saved.collapsed === "a" || saved.collapsed === "b" ? saved.collapsed : "";

    const save = () => {
      const next = readSplitPrefs();
      next[cfg.storageKey] = { pct: Math.round(pct * 10) / 10, collapsed };
      writeSplitPrefs(next);
    };

    const apply = () => {
      const clampedPct = Math.min(Math.max(pct, 12), 88);
      topEl.classList.toggle("is-collapsed", collapsed === "a");
      botEl.classList.toggle("is-collapsed", collapsed === "b");
      handleEl.classList.toggle("is-collapsed-a", collapsed === "a");
      handleEl.classList.toggle("is-collapsed-b", collapsed === "b");

      if (collapsed === "a") {
        topEl.style.flex = "0 0 auto";
        botEl.style.flex = "1 1 0px";
      } else if (collapsed === "b") {
        topEl.style.flex = "1 1 0px";
        botEl.style.flex = "0 0 auto";
      } else {
        topEl.style.flex = `${clampedPct} 1 0px`;
        botEl.style.flex = `${100 - clampedPct} 1 0px`;
      }

      const btnUp = handleEl.querySelector('[data-split-act="collapse-a"]');
      const btnDown = handleEl.querySelector('[data-split-act="collapse-b"]');
      if (btnUp) {
        btnUp.hidden = collapsed === "a";
        btnUp.title = collapsed === "b"
          ? `Expand ${cfg.bottomLabel}`
          : `Collapse ${cfg.topLabel} (expand ${cfg.bottomLabel})`;
      }
      if (btnDown) {
        btnDown.hidden = collapsed === "b";
        btnDown.title = collapsed === "a"
          ? `Expand ${cfg.topLabel}`
          : `Collapse ${cfg.bottomLabel} (expand ${cfg.topLabel})`;
      }
    };

    handleEl.addEventListener("click", (e) => {
      const btn = e.target.closest("[data-split-act]");
      if (!btn) return;
      e.stopPropagation();
      const act = btn.dataset.splitAct;
      if (act === "collapse-a") {
        collapsed = collapsed === "b" ? "" : "a";
      } else if (act === "collapse-b") {
        collapsed = collapsed === "a" ? "" : "b";
      }
      apply();
      save();
    });

    handleEl.addEventListener("dblclick", (e) => {
      if (e.target.closest(".split-btn")) return;
      collapsed = "";
      pct = cfg.defaultPct;
      apply();
      save();
    });

    handleEl.addEventListener("pointerdown", (e) => {
      if (e.target.closest(".split-btn") || e.button !== 0) return;
      e.preventDefault();
      const startY = e.clientY;
      const startMode = collapsed;
      const startTopH = topEl.getBoundingClientRect().height;
      const startBotH = botEl.getBoundingClientRect().height;
      const totalH = startTopH + startBotH;
      if (totalH <= 0) return;

      handleEl.classList.add("is-dragging");
      document.body.classList.add("is-resizing-row");

      const onMove = (ev) => {
        const dy = ev.clientY - startY;
        if (startMode === "a") {
          if (dy <= 10) return;
          collapsed = "";
          const nextTop = Math.min(Math.max(cfg.minTopPx + dy, cfg.minTopPx), totalH - cfg.minBotPx);
          pct = (nextTop / totalH) * 100;
          apply();
          return;
        }
        if (startMode === "b") {
          if (dy >= -10) return;
          collapsed = "";
          const nextTop = Math.min(Math.max(totalH - cfg.minBotPx + dy, cfg.minTopPx), totalH - cfg.minBotPx);
          pct = (nextTop / totalH) * 100;
          apply();
          return;
        }
        const nextTop = Math.min(Math.max(startTopH + dy, cfg.minTopPx), totalH - cfg.minBotPx);
        pct = (nextTop / totalH) * 100;
        apply();
      };
      const onUp = () => {
        window.removeEventListener("pointermove", onMove);
        window.removeEventListener("pointerup", onUp);
        window.removeEventListener("pointercancel", onUp);
        handleEl.classList.remove("is-dragging");
        document.body.classList.remove("is-resizing-row");
        save();
      };
      window.addEventListener("pointermove", onMove);
      window.addEventListener("pointerup", onUp);
      window.addEventListener("pointercancel", onUp);
    });

    splitRefreshers.push(apply);
    apply();
  }

  function setupBrowserSplitters() {
    bindVerticalSplit({
      splitId: "sqlBrowser",
      paneId: "sqlBrowserPane",
      handleId: "sqlBrowserSplitV",
      storageKey: "sqlSidebar",
      defaultW: 240,
      wideW: 380,
      minW: 165,
      maxW: 560,
    });
    bindHorizontalSplit({
      topId: "sqlDbList",
      botId: "sqlTableList",
      handleId: "sqlPaneSplitH",
      storageKey: "sqlDbTables",
      defaultPct: 35,
      minTopPx: 54,
      minBotPx: 72,
      topLabel: "Databases",
      bottomLabel: "Tables",
    });
    bindHorizontalSplit({
      topId: "sqlEditorBox",
      botId: "sqlResultBox",
      handleId: "sqlMainSplitH",
      storageKey: "sqlEditorResult",
      defaultPct: 38,
      minTopPx: 90,
      minBotPx: 96,
      topLabel: "SQL editor",
      bottomLabel: "results",
    });
    bindVerticalSplit({
      splitId: "redisBrowser",
      paneId: "redisBrowserPane",
      handleId: "redisBrowserSplitV",
      storageKey: "redisSidebar",
      defaultW: 240,
      wideW: 380,
      minW: 165,
      maxW: 560,
    });
    bindHorizontalSplit({
      topId: "redisInspector",
      botId: "redisCliBox",
      handleId: "redisMainSplitH",
      storageKey: "redisInspectorCli",
      defaultPct: 60,
      minTopPx: 90,
      minBotPx: 96,
      topLabel: "inspector",
      bottomLabel: "CLI",
    });
    window.addEventListener("resize", reapplySplitters);
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
    const activeTab = getActiveSqlTab();
    const rowDb = activeTab?.db || state.sqlDb;
    const rowEngine = activeTab?.engine || state.dbEngine;
    const rowTable = state.sqlTable;
    if (!state.sqlColumns || !state.sqlColumns.length) {
      try {
        state.sqlColumns = await ensureSqlColumns(rowEngine, rowDb, rowTable);
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
          await api().UpdateRow(rowEngine, rowDb, rowTable, keys, values, hasPK);
          toast("Row updated!", "ok");
        } else {
          await api().InsertRow(rowEngine, rowDb, rowTable, values);
          toast("Row created!", "ok");
        }
        $("modalRowForm").hidden = true;
        await runSqlQuery(state.sqlShowingAll, true, activeTab?.id || "");
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
    const activeTab = getActiveSqlTab();
    const rowDb = activeTab?.db || state.sqlDb;
    const rowEngine = activeTab?.engine || state.dbEngine;
    const rowTable = state.sqlTable;
    try {
      if (!state.sqlColumns || !state.sqlColumns.length) {
        try {
          state.sqlColumns = await ensureSqlColumns(rowEngine, rowDb, rowTable);
        } catch { }
      }
      const { keys, hasPK } = buildRowKeyMap(rowIdx);
      await api().DeleteRow(rowEngine, rowDb, rowTable, keys, hasPK);
      toast("Row deleted", "ok");
      await runSqlQuery(state.sqlShowingAll, true, activeTab?.id || "");
    } catch (err) {
      toast(errMsg(err), "err");
    }
  }

  // ---- Redis Browser (Local & Cloud) ----
  const isCloudTarget = () => String(state.dbEngine || "").startsWith("cloud:");
  async function loadRedisDatabases() {
    try {
      const dbs = isCloudTarget()
        ? await api().CloudRedisDatabases(state.dbEngine)
        : await api().RedisDatabases();
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
      const res = isCloudTarget()
        ? await api().CloudRedisScan(state.dbEngine, state.redisDb, pattern, cursor)
        : await api().RedisScan(state.redisDb, pattern, cursor);
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
      const val = isCloudTarget()
        ? await api().CloudRedisGet(state.dbEngine, state.redisDb, key)
        : await api().RedisGet(state.redisDb, key);
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
          if (isCloudTarget()) await api().CloudRedisDelete(state.dbEngine, state.redisDb, key);
          else await api().RedisDelete(state.redisDb, key);
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
      const reply = isCloudTarget()
        ? await api().CloudRedisCommand(state.dbEngine, state.redisDb, line)
        : await api().RedisCommand(state.redisDb, line);
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
      const btn = $("btnStartAll");
      const prev = btn.textContent;
      btn.disabled = true;
      btn.textContent = "⏳ Starting...";
      try {
        await api().StartAll();
        state.statuses = (await api().Status()) || [];
        renderDashboard();
      } catch (e) {
        toast(errMsg(e), "err");
      } finally {
        btn.disabled = false;
        btn.textContent = prev;
      }
    };
    $("btnStopAll").onclick = async () => {
      const btn = $("btnStopAll");
      const prev = btn.textContent;
      btn.disabled = true;
      btn.textContent = "⏳ Stopping All...";
      try {
        await api().StopAll();
        state.statuses = (await api().Status()) || [];
        renderDashboard();
        toast("All dashboard services turned off", "ok");
      } catch (e) {
        toast(errMsg(e), "err");
      } finally {
        btn.disabled = false;
        btn.textContent = prev;
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
      $("sysUpCurVer").textContent = `Darius DB (Lite Version ${up.currentVersion})${up.currentBuild ? " · Build " + up.currentBuild : ""}`;
      $("sysUpLatVer").textContent = `Lite Version ${up.latestVersion}${up.latestBuild ? " · Build " + up.latestBuild : ""}`;
      const badge = $("sysUpBadge");
      if (up.fetchOk === false) {
        badge.className = "pill warn";
        badge.textContent = "Fetch Failed";
      } else if (up.hasUpdate) {
        badge.className = "pill warn";
        badge.textContent = up.latestBuild ? `Update Ready (${up.latestBuild})` : `Update Available (${up.latestVersion})`;
      } else {
        badge.className = "pill running";
        badge.textContent = up.latestCommitSha ? `Synced (${up.latestCommitSha})` : "Up to Date";
      }
      if ($("btnAnnounceSysUpdate")) {
        $("btnAnnounceSysUpdate").textContent = up.fetchOk && up.hasUpdate
          ? `⬆️ Update to ${up.latestVersion}${up.latestBuild ? " (" + up.latestBuild + ")" : ""}`
          : "⬆️ System Updater";
      }
      if ($("sysUpReadyBar")) {
        const hasPkg = !!(up.directSetupUrl || up.fallbackSetupUrl || up.directZipUrl);
        const showReady = up.fetchOk !== false && hasPkg && up.hasUpdate;
        $("sysUpReadyBar").hidden = !showReady;
        if (showReady) {
          $("sysUpReadyTitle").textContent = `🎉 Update Ready: Lite Version ${up.latestVersion}${up.latestBuild ? " (Build " + up.latestBuild + ")" : ""}!`;
          $("sysUpReadySub").textContent = `Upgrade build ${up.currentBuild || up.currentVersion} → ${up.latestBuild || up.latestVersion} in-place and restart visibly (keeps data/ and www/ safe).`;
          if ($("btnSysUpInstallNow")) $("btnSysUpInstallNow").textContent = "⬆️ Update & Reboot Now";
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
      const recent10 = (up.recentCommits || []).slice(0, 10);
      const pushLines = recent10.map((c) => `[${c.sha}] ${c.message}${c.date ? ` (${c.date})` : ""}`);
      const pushSet = new Set(pushLines.map((x) => x.toLowerCase()));
      const extraNotes = (up.changelog || []).filter((item) => !pushSet.has(String(item).toLowerCase()));
      const displayItems = pushLines.length ? [...pushLines, ...extraNotes] : (up.changelog || []);
      $("sysUpChangelogList").innerHTML = displayItems
        .map((item) => `<li>${esc(item)}</li>`)
        .join("");
      if ($("announceBodyText") && up.announcement) {
        $("announceBodyText").textContent = up.announcement;
      }
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
          const buildSummary = up.latestBuild
            ? `Build <code>${esc(up.currentBuild || "-")}</code> → GitHub Build <code>${esc(up.latestBuild)}</code>`
            : `Latest Release: <strong>Lite Version ${esc(up.latestVersion)}</strong>`;
          setFetchProgress(
            "success",
            100,
            up.hasUpdate
              ? `🎉 New Build Found (${esc(up.latestBuild || up.latestVersion)}) — Click "Fetch Latest from GitHub" to Update & Reboot!`
              : "✅ Online Check Complete — Running Latest Build!",
            buildSummary +
            (up.latestCommitSha
              ? ` · Commit <code>${esc(up.latestCommitSha)}</code>: "${esc(up.latestCommitMsg)}"`
              : "")
          );
          if (!up.hasUpdate && $("sysUpReadyBar") && (up.directSetupUrl || up.fallbackSetupUrl || up.directZipUrl)) {
            $("sysUpReadyBar").hidden = false;
            $("sysUpReadyTitle").textContent = `✅ Up to Date (Build ${up.currentBuild || up.currentVersion})`;
            $("sysUpReadySub").textContent = "You already have the latest GitHub build. Use Reinstall & Reboot only if you want to refresh binaries.";
            if ($("btnSysUpInstallNow")) $("btnSysUpInstallNow").textContent = "🔄 Reinstall & Reboot";
          }
          toast(
            up.hasUpdate
              ? `🎉 New update ready (${up.latestBuild || up.latestVersion})! Click Fetch Latest from GitHub to auto-update & reboot.`
              : `✅ Check Online complete — up to date (Build ${up.currentBuild || up.latestVersion})`,
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
          if (up.hasUpdate && (up.directSetupUrl || up.fallbackSetupUrl || up.directZipUrl)) {
            const primary = up.directSetupUrl || "";
            const fallback = up.fallbackSetupUrl || up.directZipUrl || "";
            setFetchProgress(
              "loading",
              82,
              `🎉 Update Found (Build ${esc(up.latestBuild || up.latestVersion)}) — Downloading & Rebooting...`,
              "Step 3/3: Downloading latest Darius DB installer and rebooting with window visible..."
            );
            btn.textContent = "⏳ Updating & Rebooting...";
            const msg = await api().ApplySystemUpdateAuto(primary, fallback);
            setFetchProgress("success", 100, "✅ Update Staged — Rebooting Darius DB...", esc(msg || "Rebooting..."));
            if (msg) toast(msg, "ok");
            return;
          }
          setFetchProgress(
            "success",
            100,
            `✅ Already on Latest GitHub Build (${esc(up.currentBuild || up.latestVersion)})!`,
            `Installed Build <code>${esc(up.currentBuild || "-")}</code> matches GitHub Build <code>${esc(up.latestBuild || "-")}</code>` +
            (up.latestCommitSha
              ? ` · Commit <code>${esc(up.latestCommitSha)}</code>: <strong>${esc(
                up.latestCommitMsg
              )}</strong> (${esc(up.latestCommitDate)})`
              : " · Engine catalog &amp; version.json synced!")
          );
          if ($("sysUpReadyBar") && (up.directSetupUrl || up.fallbackSetupUrl || up.directZipUrl)) {
            $("sysUpReadyBar").hidden = false;
            $("sysUpReadyTitle").textContent = `✅ Already on Latest Build (${up.currentBuild || up.currentVersion})`;
            $("sysUpReadySub").textContent = "No reboot needed — click Reinstall & Reboot only if you want to refresh executables.";
            if ($("btnSysUpInstallNow")) $("btnSysUpInstallNow").textContent = "🔄 Reinstall & Reboot";
          }
          btn.textContent = "✅ Already Latest!";
          toast(
            up.latestCommitSha
              ? `✅ Already on latest GitHub build [${up.latestCommitSha}]: ${up.latestCommitMsg}`
              : "✅ Already running the latest Darius DB version from GitHub!",
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

    const healFrontendCaches = (cleanCache) => {
      const uiFixes = [];
      if (!cleanCache) return uiFixes;
      invalidateSqlCache();
      state.activeBackupBtn = null;
      try {
        const rawSplits = localStorage.getItem(SPLIT_PREFS_KEY);
        if (rawSplits) {
          const parsed = JSON.parse(rawSplits);
          if (!parsed || typeof parsed !== "object") {
            localStorage.removeItem(SPLIT_PREFS_KEY);
            uiFixes.push("Cleared corrupted browser pane split layout");
          }
        }
      } catch {
        try { localStorage.removeItem(SPLIT_PREFS_KEY); } catch { }
        uiFixes.push("Reset unreadable UI split layout storage");
      }
      reapplySplitters();
      uiFixes.push("Cleared frontend SQL database/table/schema caches & verified UI layout");
      return uiFixes;
    };

    let repairModalMode = "single";
    const closeRepair = () => ($("modalRepair").hidden = true);
    const getRepairButtonLabel = () => (repairModalMode === "all" ? "🛠️ Run Repair All" : "🔧 Run Repair");
    const runRepairAction = async () => {
      const target = repairModalMode === "all" ? "all" : ($("repairTarget").value || "apache");
      const resetCfg = !!$("repairResetCfg").checked;
      const fixPorts = !!$("repairFixPorts")?.checked;
      const cleanCache = !!$("repairCleanCache")?.checked;
      const deepSystem = repairModalMode === "all";
      const btn = $("btnRunRepair");
      const box = $("repairReportBox");
      btn.disabled = true;
      btn.textContent = repairModalMode === "all" ? "Repairing All..." : "Repairing...";
      box.hidden = false;
      box.innerHTML = `<div class="muted">${
        repairModalMode === "all"
          ? "Running full system diagnostics &amp; repairing all components..."
          : `Running diagnostics &amp; repairing ${esc(target)}...`
      }</div>`;
      try {
        const rep = api().RepairSystem
          ? await api().RepairSystem({ target, resetConfigs: resetCfg, fixPorts, cleanCache, deepSystem })
          : await api().Repair(target, resetCfg);
        const uiFixes = healFrontendCaches(cleanCache);
        const allItems = [...(rep.items || [])];
        uiFixes.forEach((msg) => allItems.push({ category: "UI & Cache", action: msg, status: "fixed" }));
        const rows = allItems
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
        const [statuses, settings, cloudProfiles, schedules] = await Promise.all([
          api().Status().catch(() => state.statuses),
          api().GetSettings().catch(() => state.settings),
          api().CloudProfiles().catch(() => state.cloudProfiles),
          api().Schedules().catch(() => state.schedules),
        ]);
        state.statuses = statuses || [];
        state.settings = settings || state.settings;
        state.cloudProfiles = cloudProfiles || [];
        state.schedules = schedules || [];
        updateCloudBadge();
        renderDashboard();
        if (state.tab === "settings") loadSettingsForm();
        if (state.tab === "cloud") renderCloudView();
        if (state.tab === "database") await loadDbView(true);
        toast(rep.summary, rep.warnCount > 0 ? "info" : rep.fixedCount > 0 ? "ok" : "info");
      } catch (e) {
        box.innerHTML = `<div style="color:var(--danger)">Repair error: ${esc(errMsg(e))}</div>`;
        toast(errMsg(e), "err");
      } finally {
        btn.disabled = false;
        btn.textContent = getRepairButtonLabel();
      }
    };
    const defaultSingleRepairTarget = () => {
      const running = (state.statuses || []).find((s) => s.running && s.engine);
      if (running) return running.engine;
      const installed = (state.statuses || []).find((s) => s.installed && s.engine);
      return installed ? installed.engine : "apache";
    };
    const openRepairModal = (target = "all", autoRun = false, opts = {}) => {
      const isAll = opts.mode === "all" || (opts.mode !== "single" && target === "all");
      repairModalMode = isAll ? "all" : "single";

      const sel = $("repairTarget");
      const allOpt = sel?.querySelector('option[value="all"]');
      if (isAll) {
        if (allOpt) allOpt.hidden = false;
        sel.value = "all";
        sel.disabled = true;
        if ($("repairModalTitle")) $("repairModalTitle").textContent = "🛠️ Repair All (Full System & Engines)";
        if ($("repairModalDesc")) {
          $("repairModalDesc").innerHTML =
            "Full-system recovery across all folders (<code>www/</code>, <code>bin/</code>, <code>data/</code>, <code>etc/</code>, <code>logs/</code>, <code>backups/</code>, <code>tmp/</code>), <code>settings.json</code>, Cloud profiles, Schedules, Mailpit inbox, oversized logs, stale PIDs &amp; orphaned processes, Windows <code>PATH</code>, SQL/UI caches, blocked ports &amp; MS SQL LocalDB — while keeping databases in <code>data/</code> untouched.";
        }
        if ($("repairTargetLabel")) $("repairTargetLabel").textContent = "Scope";
      } else {
        const picked = target && target !== "all" ? target : defaultSingleRepairTarget();
        if (allOpt) allOpt.hidden = true;
        sel.disabled = false;
        sel.value = picked;
        if (sel.value === "all") sel.value = "apache";
        const engLabel = sel.options[sel.selectedIndex]?.textContent || picked;
        if ($("repairModalTitle")) {
          $("repairModalTitle").textContent = target && target !== "all"
            ? `🔧 Repair ${engLabel} Only`
            : "🔧 Repair Service Only";
        }
        if ($("repairModalDesc")) {
          $("repairModalDesc").innerHTML =
            "Repairs only the selected component — verifies its binaries, configuration, data folder, and clears stale PID locks while keeping databases in <code>data/</code> untouched.";
        }
        if ($("repairTargetLabel")) $("repairTargetLabel").textContent = "Service to Repair (Single Component Only)";
      }

      $("repairResetCfg").checked = !!opts.resetConfigs;
      if ($("repairFixPorts")) $("repairFixPorts").checked = opts.fixPorts !== undefined ? !!opts.fixPorts : isAll;
      if ($("repairCleanCache")) $("repairCleanCache").checked = opts.cleanCache !== undefined ? !!opts.cleanCache : isAll;
      $("btnRunRepair").textContent = getRepairButtonLabel();
      $("repairReportBox").hidden = !autoRun;
      if (!autoRun) $("repairReportBox").innerHTML = "";
      $("modalRepair").hidden = false;
      if (autoRun) runRepairAction();
    };
    $("btnOpenRepair").onclick = () => openRepairModal(defaultSingleRepairTarget(), false, { mode: "single", fixPorts: false, cleanCache: false, resetConfigs: false });
    if ($("btnRepairAllSystem")) {
      $("btnRepairAllSystem").onclick = () => openRepairModal("all", false, { mode: "all", fixPorts: true, cleanCache: true, resetConfigs: false });
    }
    $("btnCloseRepair").onclick = closeRepair;
    $("btnCancelRepair").onclick = closeRepair;
    $("btnRunRepair").onclick = runRepairAction;

    $("serviceGrid").addEventListener("click", async (e) => {
      const presetBtn = e.target.closest("[data-preset-auto]");
      if (presetBtn) {
        const mode = presetBtn.dataset.presetAuto;
        const map = { apache: false, php: false, mariadb: false, mysql: false, postgresql: false, mssql: false, redis: false, mailpit: false };
        let autoOn = true;
        if (mode === "mysql") {
          map.mysql = true;
        } else if (mode === "mysql+apache") {
          map.mysql = true;
          map.apache = true;
          map.php = true;
        } else if (mode === "running") {
          let any = false;
          for (const st of state.statuses || []) {
            if (st.state === "running" && st.engine !== "php") {
              map[st.engine] = true;
              any = true;
            }
          }
          if (!any) {
            toast("No services are currently running on Dashboard", "info");
            return;
          }
        } else if (mode === "none") {
          autoOn = false;
        }
        try {
          state.settings = await api().SetAutoRunConfig(autoOn, map);
          renderDashboard();
          if (state.tab === "settings") renderSettings();
          const chosen = Object.keys(map).filter((k) => k !== "php" && map[k]);
          toast(autoOn ? `Auto Run set: ${chosen.join(" + ").toUpperCase()} (ON)` : "Auto Run turned OFF", "ok");
        } catch (err) {
          toast(errMsg(err), "err");
        }
        return;
      }

      const btn = e.target.closest("[data-act]");
      if (!btn) return;
      const eng = btn.dataset.engine;
      const act = btn.dataset.act;
      state.recentStartClick = state.recentStartClick || {};
      try {
        if (act === "toggleautorun") {
          const nextOn = !state.settings?.autoStartServices;
          state.settings = await api().SetAutoRunConfig(nextOn, {});
          renderDashboard();
          if (state.tab === "settings") renderSettings();
          toast(nextOn ? "⚡ Auto Run on Open turned ON" : "Auto Run on Open turned OFF", "ok");
        } else if (act === "toggleauto") {
          const curOn = !!state.settings?.engines?.[eng]?.enabled;
          const nextOn = !curOn;
          const patch = { [eng]: nextOn };
          const shouldAutoStart = nextOn ? true : !!state.settings?.autoStartServices;
          state.settings = await api().SetAutoRunConfig(shouldAutoStart, patch);
          renderDashboard();
          if (state.tab === "settings") renderSettings();
          toast(`${eng.toUpperCase()} ${nextOn ? "added to" : "removed from"} Auto Run`, "ok");
        } else if (act === "start") {
          state.recentStartClick[eng] = Date.now();
          await api().Start(eng);
        } else if (act === "stop") {
          const st = getStatus(eng);
          if (st?.state === "starting" && Date.now() - (state.recentStartClick[eng] || 0) < 900) {
            return;
          }
          await api().Stop(eng);
        }
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
          resetSqlBrowserState("");
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
    $("btnDbRefresh").onclick = () => loadDbView(true);

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
        invalidateSqlCache(state.dbEngine);
        await loadSqlDatabases(true);
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
        closeExport();
        const outPath = await api().ExportDatabaseDialog(state.dbEngine, state.sqlDb, tables, fmt, incSchema, incData);
        if (outPath) {
          toast(`Export saved: ${outPath}`, "ok");
        }
      } catch (err) {
        toast(errMsg(err), "err");
      } finally {
        btn.disabled = false;
      }
    };

    const setBackupProgressUI = (pct, mode, stage, detail) => {
      const modal = $("modalBackupProgress");
      const box = $("backupProgBox");
      if (!modal || !box) return;
      const clamped = Math.max(0, Math.min(100, Number(pct) || 0));
      modal.hidden = false;
      box.className = "sysup-fetch-box" + (mode === "done" ? " success" : mode === "error" ? " error" : "");
      const sp = $("backupProgSpinner");
      if (sp) sp.hidden = mode !== "running";
      if ($("backupProgStage")) $("backupProgStage").textContent = stage || "Backing up...";
      if ($("backupProgPct")) $("backupProgPct").textContent = `${clamped}%`;
      if ($("backupProgBar")) $("backupProgBar").style.width = `${clamped}%`;
      if ($("backupProgDetail")) $("backupProgDetail").textContent = detail || "";
      if (state.activeBackupBtn) {
        if (mode === "running") state.activeBackupBtn.textContent = `⏳ ${clamped}%`;
      }
      if (mode === "done") {
        setTimeout(() => {
          if ($("backupProgBox")?.classList.contains("success")) {
            modal.hidden = true;
          }
        }, 1200);
      }
    };
    const closeBackupProgress = () => {
      if ($("modalBackupProgress")) $("modalBackupProgress").hidden = true;
    };
    if ($("btnCloseBackupProg")) $("btnCloseBackupProg").onclick = closeBackupProgress;
    if ($("btnDoneBackupProg")) $("btnDoneBackupProg").onclick = closeBackupProgress;
    rt()?.EventsOn?.("backup-progress", (p) => {
      if (!p) return;
      setBackupProgressUI(p.percent, p.state || "running", p.stage || "Backing up...", p.detail || "");
    });

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
    const runFullZipBackup = async (targetEngine, dbName) => {
      if (!dbName) {
        toast("Select a database first", "err");
        return;
      }
      try {
        toast(`Creating full .zip backup of ${dbName} (SQL dump + table CSVs)...`, "info");
        const outPath = await api().ExportDatabaseDialog(targetEngine, dbName, [], "zip", true, true);
        if (outPath) toast(`Full .zip backup saved: ${outPath}`, "ok");
      } catch (e) {
        toast(errMsg(e), "err");
      }
    };
    $("btnBackupBak").onclick = () => runBackupDialog("bak");
    $("btnBackupSql").onclick = () => openExportModal(state.sqlTable ? "single" : "all");
    if ($("btnBackupZip")) $("btnBackupZip").onclick = () => runFullZipBackup(state.dbEngine, state.sqlDb);
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
          state.sqlTabs = state.sqlTabs.filter((t) => !(t.engine === state.dbEngine && t.db === name));
          if (state.sqlDb === name) state.sqlDb = "";
          invalidateSqlCache(state.dbEngine);
          ensureActiveSqlTab();
          await loadSqlDatabases(true);
        } catch (err) {
          toast(errMsg(err), "err");
        }
        return;
      }
      const item = e.target.closest("[data-db]");
      if (!item) return;
      syncActiveTabFromDom();
      state.sqlDb = item.dataset.db;
      const curTab = getActiveSqlTab();
      if (curTab && isBlankSqlTab(curTab)) {
        curTab.db = state.sqlDb;
        renderSqlTabs();
      }
      $("sqlActiveDb").textContent = `${state.dbEngine} / ${state.sqlDb}`;
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
          state.sqlTabs
            .filter((t) => t.engine === state.dbEngine && t.db === state.sqlDb && t.table === tbl)
            .forEach((t) => closeSqlTab(t.id));
          invalidateSqlCache(state.dbEngine, state.sqlDb);
          await loadSqlTables(state.sqlDb, true);
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

    if ($("btnNewSqlTab")) {
      $("btnNewSqlTab").onclick = () => openNewSqlTab({ focus: true });
    }
    if ($("sqlTabList")) {
      $("sqlTabList").addEventListener("click", (e) => {
        const closeBtn = e.target.closest("[data-close-sqltab]");
        if (closeBtn) {
          e.stopPropagation();
          closeSqlTab(closeBtn.dataset.closeSqltab);
          return;
        }
        const tabEl = e.target.closest("[data-sqltab-id]");
        if (!tabEl) return;
        if (tabEl.dataset.sqltabId === state.activeSqlTabId) return;
        syncActiveTabFromDom();
        const tab = getSqlTab(tabEl.dataset.sqltabId);
        if (tab) applyTabToDom(tab, { syncSidebar: true });
      });
      $("sqlTabList").addEventListener("auxclick", (e) => {
        if (e.button !== 1) return;
        const tabEl = e.target.closest("[data-sqltab-id]");
        if (!tabEl) return;
        e.preventDefault();
        closeSqlTab(tabEl.dataset.sqltabId);
      });
      $("sqlTabList").addEventListener("dblclick", (e) => {
        if (e.target === $("sqlTabList")) openNewSqlTab({ focus: true });
      });
    }

    const triggerShowAll = async () => {
      const curTab = getActiveSqlTab();
      const targetDb = curTab?.db || state.sqlDb;
      const t = targetDb === state.sqlDb ? state.sqlTables.find((x) => x.name === state.sqlTable) : null;
      if (t) {
        await selectSqlTable(t, true);
      } else if ($("sqlEditor").value.trim()) {
        // Strip LIMIT N / TOP N so Show All returns every row
        $("sqlEditor").value = applySqlQueryLimit($("sqlEditor").value, 0);
        state.sqlShowingAll = true;
        await runSqlQuery(true);
      } else {
        toast("Select a table or enter a SELECT query first", "info");
      }
    };
    $("btnRowCreate").onclick = () => openRowFormModal("create");
    $("btnMetaCreateRow").onclick = () => openRowFormModal("create");
    $("btnSqlShowAll").onclick = triggerShowAll;
    $("btnMetaShowAll").onclick = triggerShowAll;
    if ($("btnPagerLoadMax")) $("btnPagerLoadMax").onclick = triggerShowAll;

    const goSqlPage = (nextPage) => {
      if (!state.sqlLastResult) return;
      state.sqlPage = nextPage;
      renderSqlTable(state.sqlLastResult);
    };
    if ($("btnPageFirst")) $("btnPageFirst").onclick = () => goSqlPage(1);
    if ($("btnPagePrev")) $("btnPagePrev").onclick = () => goSqlPage(Math.max(1, state.sqlPage - 1));
    if ($("btnPageNext")) $("btnPageNext").onclick = () => goSqlPage(state.sqlPage + 1);
    if ($("btnPageLast")) {
      $("btnPageLast").onclick = () => {
        if (!state.sqlLastResult) return;
        const filter = ($("sqlRowFilter")?.value || "").trim().toLowerCase();
        const total = filter
          ? state.sqlLastResult.rows.filter((r) => r.some((c) => c !== null && String(c).toLowerCase().includes(filter))).length
          : state.sqlLastResult.rows.length;
        const ps = state.sqlPageSize > 0 ? state.sqlPageSize : Math.max(total, 1);
        goSqlPage(Math.max(1, Math.ceil(total / ps)));
      };
    }
    if ($("sqlPagerNumbers")) {
      $("sqlPagerNumbers").addEventListener("click", (e) => {
        const btn = e.target.closest("[data-page]");
        if (!btn) return;
        goSqlPage(Number(btn.dataset.page) || 1);
      });
    }
    if ($("sqlPageJump")) {
      const jump = () => goSqlPage(Math.max(1, Number($("sqlPageJump").value) || 1));
      $("sqlPageJump").addEventListener("change", jump);
      $("sqlPageJump").addEventListener("keydown", (e) => {
        if (e.key === "Enter") {
          e.preventDefault();
          jump();
        }
      });
    }
    if ($("sqlPageSize")) {
      $("sqlPageSize").addEventListener("change", async () => {
        const raw = $("sqlPageSize").value;
        const newSize = raw === "0" ? 0 : Number(raw) || 50;
        state.sqlPageSize = newSize;
        state.sqlPage = 1;
        const res = state.sqlLastResult;
        const q = $("sqlEditor")?.value.trim() || "";
        const sqlLim = detectSqlLimit(q);
        const needsFetchAll =
          !!res &&
          newSize === 0 &&
          ((sqlLim > 0 && res.rows.length >= sqlLim) || !!res.truncated);
        const needsFetchLarger =
          !!res &&
          newSize > 0 &&
          ((sqlLim > 0 && newSize > sqlLim && res.rows.length >= sqlLim) ||
            (!!res.truncated && newSize > res.rows.length));
        if (needsFetchAll || needsFetchLarger) {
          if (sqlLim > 0) {
            $("sqlEditor").value = applySqlQueryLimit(q, newSize);
          }
          state.sqlShowingAll = newSize === 0;
          await runSqlQuery(newSize === 0 || newSize > 1000);
          return;
        }
        if (res) renderSqlTable(res);
      });
    }

    let sqlFilterTimer = null;
    $("sqlRowFilter").addEventListener("input", () => {
      clearTimeout(sqlFilterTimer);
      sqlFilterTimer = setTimeout(() => {
        state.sqlPage = 1;
        if (state.sqlLastResult) renderSqlTable(state.sqlLastResult);
      }, 120);
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
        invalidateSqlCache(state.dbEngine, state.sqlDb);
        await loadSqlTables(state.sqlDb, true);
        const created = state.sqlTables.find((t) => t.name === tblName);
        if (created) await selectSqlTable(created, false);
      } catch (err) {
        toast(errMsg(err), "err");
      }
    };

    $("btnSqlRun").onclick = () => runSqlQuery(false);
    $("btnSqlClear").onclick = () => {
      $("sqlEditor").value = "";
      const tab = getActiveSqlTab();
      if (tab) tab.sql = "";
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
        invalidateSqlCache(state.dbEngine);
        await loadSqlDatabases(true);
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
        if ($("modalCloudForm") && !$("modalCloudForm").hidden) $("modalCloudForm").hidden = true;
        if ($("modalCloudSync") && !$("modalCloudSync").hidden) $("btnCloseCloudSync")?.click();
      }
    });
  }

  // ---- Init ----
  async function init() {
    wireEvents();
    wireCloudEvents();
    setupBrowserSplitters();
    wireSqlColResizer();

    const [info, settings, statuses, catalog, cloudProfiles, schedules, winTask] = await Promise.all([
      api().Info(),
      api().GetSettings(),
      api().Status(),
      api().Catalog(),
      api().CloudProfiles().catch(() => []),
      api().Schedules().catch(() => []),
      api().WindowsTaskStatus().catch(() => ({ registered: false, detail: "" })),
    ]);

    state.info = info;
    state.settings = settings;
    state.statuses = statuses || [];
    state.catalog = catalog || {};
    state.cloudProfiles = cloudProfiles || [];
    state.schedules = schedules || [];
    state.winTask = winTask || { registered: false, detail: "" };
    updateCloudBadge();

    $("appVersion").textContent = "Lite Version " + info.version;
    if ($("dashSystemBadge")) {
      $("dashSystemBadge").textContent = "Darius DB · Lite Version " + info.version;
    }
    $("rootPathLabel").textContent = info.root;
    applyTheme(settings.theme);

    renderDashboard();

    // Non-blocking live announcement & GitHub commit check on startup (deferred so Dashboard paints immediately)
    setTimeout(() => {
      api().CheckSystemUpdate().then((up) => {
        if (!up) return;
        if ($("announceBodyText") && up.announcement) {
          $("announceBodyText").textContent = up.announcement;
        }
        if ($("announceUpdateInfo") && up.latestCommitSha) {
          $("announceUpdateInfo").textContent = `Latest GitHub Update [${up.latestCommitSha}]: ${up.latestCommitMsg} (${up.latestCommitDate})`;
        }
        if (up.fetchOk && up.hasUpdate && $("btnAnnounceSysUpdate")) {
          $("btnAnnounceSysUpdate").textContent = `⬆️ Update to ${up.latestVersion}`;
        }
      }).catch(() => { });
    }, 400);

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
        if (prev !== now) loadDbView(true);
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

    onEvent("app-updated", (build) => {
      toast(`✅ Darius DB updated and restarted cleanly (Build ${build || state.info?.version || ""})!`, "ok");
    });
  }

  // ---- Cloud Databases View & Actions ----
  const CLOUD_PRESETS = {
    cpanel: {
      provider: "cpanel",
      engine: "mysql",
      port: 3306,
      sslMode: "preferred",
      hostPlaceholder: "server.yourdomain.com or IP",
      dbPlaceholder: "cpaneluser_myapp",
      hint: "cPanel / Plesk / Hostinger: Add your public IP (or %) under cPanel → Remote MySQL®, then enter your cPanel server hostname/IP and prefixed DB/User.",
    },
    monsterasp: {
      provider: "monsterasp",
      engine: "mssql",
      port: 1433,
      sslMode: "skip-verify",
      hostPlaceholder: "db12345.public.databaseasp.net",
      dbPlaceholder: "db12345",
      hint: "MonsterASP.NET / SmarterASP / Somee: Paste your ADO.NET connection string above (or enter dbXXXX.public.databaseasp.net, port 1433, Database, Username, Password). Uses TrustServerCertificate automatically.",
    },
    supabase: {
      provider: "supabase",
      engine: "postgresql",
      port: 5432,
      sslMode: "require",
      hostPlaceholder: "db.xxxx.supabase.co or ep-xxx.neon.tech",
      dbPlaceholder: "postgres",
      hint: "Supabase / Neon / Render / Railway: Paste your postgresql:// URI above for instant auto-fill (port 5432 or 6543 pooler, SSL Require).",
    },
    tidb: {
      provider: "tidb",
      engine: "mysql",
      port: 4000,
      sslMode: "require",
      hostPlaceholder: "gateway01.us-east-1.prod.aws.tidbcloud.com",
      dbPlaceholder: "test",
      hint: "Aiven / TiDB Cloud / PlanetScale: Requires TLS enabled (Require SSL/TLS). Paste your mysql:// URI above.",
    },
    rds: {
      provider: "rds",
      engine: "mysql",
      port: 3306,
      sslMode: "require",
      hostPlaceholder: "mydb.xxxxxx.region.rds.amazonaws.com",
      dbPlaceholder: "production",
      hint: "AWS RDS / Azure SQL / Google Cloud SQL: Ensure your Security Group or Firewall rule allows your public IP.",
    },
    rediscloud: {
      provider: "rediscloud",
      engine: "redis",
      port: 6379,
      sslMode: "require",
      hostPlaceholder: "redis-12345.c1.us-east-1-2.ec2.cloud.redislabs.com",
      dbPlaceholder: "",
      hint: "Redis Cloud / Upstash: Paste your rediss:// URI above or set host, port, password, and TLS Require.",
    },
  };

  function updateCloudBadge() {
    const badge = $("cloudBadge");
    if (!badge) return;
    const n = (state.cloudProfiles || []).length;
    badge.textContent = n;
    badge.hidden = n === 0;
  }

  function renderCloudView() {
    updateCloudBadge();
    renderSchedules();
    const grid = $("cloudProfileGrid");
    if (!grid) return;
    const list = state.cloudProfiles || [];
    if (!list.length) {
      grid.innerHTML = `<div class="empty-note" style="grid-column:1 / -1;padding:24px">
        No cloud databases saved yet. Click a provider preset above or <strong>+ Add Cloud DB</strong> to connect cPanel, MonsterASP.NET, Supabase, Neon, Aiven, TiDB, AWS RDS, Azure, or Redis Cloud.
      </div>`;
      return;
    }
    grid.innerHTML = list
      .map((c) => {
        const providerLabel = (c.provider || "cloud").toUpperCase();
        const engineLabel = (c.engine || "mysql").toUpperCase();
        const latPill = c.lastLatencyMs
          ? `<span class="pill running" title="Last connected ${esc(c.lastConnected || "")}">${c.lastLatencyMs} ms</span>`
          : `<span class="pill">Ready</span>`;
        const sched = (state.schedules || []).find((s) => s.cloudId === c.id && s.enabled);
        const autoPill = sched
          ? `<span class="pill running" title="Scheduled daily @ ${esc(sched.backupTime || sched.syncTime)}">⏰ Auto ${esc(sched.syncDirection === "pull" ? "Pull" : "Push")} / ${sched.retentionDays || 7}d</span>`
          : "";
        return `<div class="cloud-card" data-cid="${esc(c.id)}">
          <div class="cloud-card-head">
            <div>
              <strong style="font-size:14px">${esc(c.name)}</strong>
              <div style="display:flex;gap:5px;margin-top:4px;flex-wrap:wrap">
                <span class="pill accent">${esc(providerLabel)}</span>
                <span class="pill">${esc(engineLabel)}</span>
                ${c.sslMode && c.sslMode !== "disable" ? `<span class="pill">TLS: ${esc(c.sslMode)}</span>` : ""}
                ${autoPill}
              </div>
            </div>
            ${latPill}
          </div>
          <div class="cloud-card-meta">
            ${esc(c.host)}:${c.port}${c.database ? " / " + esc(c.database) : ""}${c.user ? " · user: " + esc(c.user) : ""}
          </div>
          <div class="cloud-card-actions">
            <button type="button" class="btn xs primary" data-cact="browse">🔌 Connect &amp; Browse</button>
            <button type="button" class="btn xs accent" data-cact="test">↻ Test</button>
            ${c.engine !== "redis" ? `<button type="button" class="btn xs" data-cact="backupzip" title="Download full .zip backup (SQL + CSVs)">📦 Backup .zip</button>` : ""}
            <button type="button" class="btn xs" data-cact="sync">🔄 Sync</button>
            ${c.panelUrl ? `<button type="button" class="btn xs" data-cact="panel">🌐 Panel ↗</button>` : ""}
            <button type="button" class="btn xs ghost" data-cact="uri">Copy URI</button>
            <button type="button" class="btn xs ghost" data-cact="edit">Edit</button>
            <button type="button" class="btn xs danger-ghost" data-cact="del">✕</button>
          </div>
        </div>`;
      })
      .join("");
  }

  function renderSchedules() {
    const winBtn = $("btnToggleWinTask");
    if (winBtn) {
      const reg = !!state.winTask?.registered;
      winBtn.textContent = reg ? "🪟 Win 2 AM Task: ON" : "🪟 Win 2 AM Task: OFF";
      winBtn.className = reg ? "btn xs accent" : "btn xs";
      winBtn.title = reg
        ? `Registered in Windows Task Scheduler (${state.winTask.detail || "02:00 AM daily"}). Click to remove.`
        : "Click to register daily 2:00 AM Windows Task Scheduler trigger even when app is closed";
    }
    const grid = $("cloudScheduleGrid");
    if (!grid) return;
    const list = state.schedules || [];
    if (!list.length) {
      grid.innerHTML = `<div class="empty-note" style="grid-column:1 / -1;padding:18px">
        No daily schedules configured yet. Click <strong>+ New Schedule</strong> or open <strong>🔄 Sync</strong> and check <strong>Enable Daily Auto-Sync &amp; 2:00 AM Auto-Backup</strong> to automatically pull <strong>Cloud → Local</strong> every day and purge backup snapshots older than <strong>7 days (1 week)</strong>.
      </div>`;
      return;
    }
    grid.innerHTML = list
      .map((s) => {
        const cloud = (state.cloudProfiles || []).find((p) => p.id === s.cloudId);
        const dirLabel = s.syncDirection === "push" ? "Push Local → Cloud" : "Pull Cloud → Local";
        const syncBadge = s.autoSync
          ? `<span class="pill accent">🔄 ${dirLabel} @ ${esc(s.syncTime || "02:00")}</span>`
          : "";
        const backupBadge = s.autoBackup
          ? `<span class="pill">📦 .zip @ ${esc(s.backupTime || "02:00")} · Purge ≥ ${s.retentionDays || 7}d</span>`
          : "";
        const statusPill = s.enabled
          ? `<span class="pill running">Active</span>`
          : `<span class="pill">Paused</span>`;
        const syncStat = s.lastSyncAt ? `Last sync (${s.lastSyncAt}): ${s.lastSyncStatus || "ok"}` : "Sync: pending next scheduled time";
        const bkpStat = s.lastBackupAt ? `Last backup (${s.lastBackupAt}): ${s.lastBackupStatus || "ok"}` : "Backup: pending next 2:00 AM run";
        return `<div class="cloud-card" data-sid="${esc(s.id)}">
          <div class="cloud-card-head">
            <div>
              <strong style="font-size:14px">${esc(s.name)}</strong>
              <div style="display:flex;gap:5px;margin-top:4px;flex-wrap:wrap">
                ${syncBadge}
                ${backupBadge}
              </div>
            </div>
            ${statusPill}
          </div>
          <div class="cloud-card-meta">
            ${cloud ? esc(cloud.name) + " (" + esc(s.cloudDb || cloud.database || "") + ")" : esc(s.cloudDb || "Local only")} ↔ ${esc((s.localEngine || "mssql").toUpperCase())}/${esc(s.localDb || "-")}
          </div>
          <div class="muted" style="font-size:11.5px;line-height:1.45">
            <div>${esc(syncStat)}</div>
            <div>${esc(bkpStat)}</div>
          </div>
          <div class="cloud-card-actions">
            <button type="button" class="btn xs primary" data-sact="run">▶ Run Now</button>
            <button type="button" class="btn xs accent" data-sact="snaps">📦 Snapshots</button>
            <button type="button" class="btn xs" data-sact="purge" title="Purge snapshots >= ${s.retentionDays || 7} days old">🗑 Purge</button>
            <button type="button" class="btn xs" data-sact="folder">📁 Folder</button>
            <button type="button" class="btn xs ghost" data-sact="toggle">${s.enabled ? "Pause" : "Enable"}</button>
            <button type="button" class="btn xs ghost" data-sact="edit">Edit</button>
            <button type="button" class="btn xs danger-ghost" data-sact="del">✕</button>
          </div>
        </div>`;
      })
      .join("");
  }

  function wireCloudEvents() {
    const closeCloudForm = () => ($("modalCloudForm").hidden = true);
    const syncJob = {
      running: false,
      cancelling: false,
      minimized: false,
      percent: 0,
      state: "idle",
      stage: "",
      detail: "",
      summary: "",
    };
    let syncHideTimer = null;

    const updateFloatingSync = () => {
      const bar = $("syncFloatingBar");
      if (!bar) return;
      const show = !!syncJob.minimized;
      bar.hidden = !show;
      document.body.classList.toggle("has-sync-floating", show);
      if (!show) return;
      if ($("syncFloatSpinner")) $("syncFloatSpinner").hidden = syncJob.state !== "running";
      if ($("syncFloatStage")) $("syncFloatStage").textContent = syncJob.stage || syncJob.summary || "Syncing database...";
      if ($("syncFloatPct")) $("syncFloatPct").textContent = `${syncJob.percent}%`;
      if ($("syncFloatBar")) $("syncFloatBar").style.width = `${syncJob.percent}%`;
      if ($("syncFloatDetail")) $("syncFloatDetail").textContent = syncJob.detail || syncJob.summary || "";
      bar.className = "sync-floating" + (syncJob.state === "done" ? " success" : (syncJob.state === "error" || syncJob.state === "cancelled") ? " error" : "");
      const fc = $("btnFloatCancelSync");
      if (fc) {
        fc.hidden = !syncJob.running;
        fc.disabled = !!syncJob.cancelling;
        fc.textContent = syncJob.cancelling ? "⏳ Cancelling..." : "🛑 Cancel";
      }
    };

    const setSyncFormLocked = (locked) => {
      [
        "syncLocalEngine",
        "syncLocalDb",
        "syncCloudProfile",
        "syncCloudDb",
        "syncAlsoSaveSchedule",
        "syncAutoTime",
        "syncAutoBackupChk",
        "syncAutoBackupTime",
        "syncAutoKeepDays",
        "btnSaveSyncScheduleOnly",
      ].forEach((id) => {
        if ($(id)) $(id).disabled = !!locked;
      });
      $("syncDirTabs")?.querySelectorAll(".seg").forEach((b) => {
        b.disabled = !!locked;
      });
      if ($("btnMinimizeCloudSync")) {
        $("btnMinimizeCloudSync").hidden = !locked;
      }
      const cbtn = $("btnCancelCloudSync");
      if (cbtn) {
        if (locked) {
          cbtn.className = "btn danger-ghost";
          cbtn.disabled = !!syncJob.cancelling;
          cbtn.textContent = syncJob.cancelling ? "⏳ Cancelling..." : "🛑 Cancel Sync";
        } else {
          cbtn.className = "btn";
          cbtn.disabled = false;
          cbtn.textContent = "Cancel";
        }
      }
    };

    const minimizeCloudSync = () => {
      if (!syncJob.running) {
        $("modalCloudSync").hidden = true;
        return;
      }
      syncJob.minimized = true;
      $("modalCloudSync").hidden = true;
      updateFloatingSync();
    };

    const restoreCloudSync = () => {
      clearTimeout(syncHideTimer);
      syncJob.minimized = false;
      updateFloatingSync();
      $("modalCloudSync").hidden = false;
    };

    const closeCloudSync = () => {
      if (syncJob.running) {
        minimizeCloudSync();
        toast("Sync is still running — minimized to background. Click 🛑 Cancel to abort.", "info");
        return;
      }
      clearTimeout(syncHideTimer);
      syncJob.minimized = false;
      updateFloatingSync();
      $("modalCloudSync").hidden = true;
    };

    const applyPresetToForm = (key) => {
      const p = CLOUD_PRESETS[key];
      if (!p) return;
      $("cloudProvider").value = p.provider;
      $("cloudEngine").value = p.engine;
      $("cloudPort").value = p.port;
      $("cloudSsl").value = p.sslMode;
      $("cloudHost").placeholder = p.hostPlaceholder;
      $("cloudDb").placeholder = p.dbPlaceholder;
      const hintEl = $("cloudProviderHint");
      if (hintEl) {
        hintEl.textContent = p.hint;
        hintEl.hidden = false;
      }
    };

    const openCloudFormModal = (existing = null, presetKey = "") => {
      $("formCloudProfile").reset();
      $("cloudEditId").value = existing?.id || "";
      $("cloudUriInput").value = "";
      $("cloudTestStatus").hidden = true;
      $("modalCloudTitle").textContent = existing ? `Edit Cloud Profile — ${existing.name}` : "☁️ Add Cloud Database";
      if ($("cloudProviderHint")) $("cloudProviderHint").hidden = true;
      if (presetKey) applyPresetToForm(presetKey);
      if (existing) {
        $("cloudName").value = existing.name || "";
        $("cloudProvider").value = existing.provider || "cloud";
        $("cloudEngine").value = existing.engine || "mysql";
        $("cloudHost").value = existing.host || "";
        $("cloudPort").value = existing.port || 3306;
        $("cloudDb").value = existing.database || "";
        $("cloudUser").value = existing.user || "";
        $("cloudPass").value = existing.password || "";
        $("cloudSsl").value = existing.sslMode || "preferred";
        $("cloudPanelUrl").value = existing.panelUrl || "";
      }
      $("modalCloudForm").hidden = false;
      $("cloudName").focus();
    };

    const buildCloudObjFromForm = () => ({
      id: $("cloudEditId").value.trim(),
      name: $("cloudName").value.trim(),
      provider: $("cloudProvider").value,
      engine: $("cloudEngine").value,
      host: $("cloudHost").value.trim(),
      port: Number($("cloudPort").value) || 3306,
      database: $("cloudDb").value.trim(),
      user: $("cloudUser").value.trim(),
      password: $("cloudPass").value,
      sslMode: $("cloudSsl").value,
      panelUrl: $("cloudPanelUrl").value.trim(),
    });

    $("cloudEngine").addEventListener("change", () => {
      const defaults = { mysql: 3306, mariadb: 3306, postgresql: 5432, mssql: 1433, redis: 6379 };
      const p = Number($("cloudPort").value);
      if (!p || [3306, 5432, 1433, 6379, 4000].includes(p)) {
        $("cloudPort").value = defaults[$("cloudEngine").value] || 3306;
      }
    });

    $("btnAddCloud").onclick = () => openCloudFormModal(null, "cpanel");
    $("btnCloseCloudForm").onclick = closeCloudForm;
    $("btnCancelCloudForm").onclick = closeCloudForm;

    $("cloudPresetStrip").addEventListener("click", (e) => {
      const btn = e.target.closest("[data-preset]");
      if (btn) openCloudFormModal(null, btn.dataset.preset);
    });

    $("btnParseCloudUri").onclick = async () => {
      const raw = $("cloudUriInput").value.trim();
      if (!raw) {
        toast("Paste a connection URI or ADO.NET string first", "err");
        return;
      }
      try {
        const parsed = await api().ParseCloudURI(raw);
        if (!$("cloudName").value.trim()) $("cloudName").value = parsed.name || parsed.host;
        $("cloudProvider").value = parsed.provider || "cloud";
        $("cloudEngine").value = parsed.engine || "mysql";
        $("cloudHost").value = parsed.host || "";
        $("cloudPort").value = parsed.port || 3306;
        $("cloudDb").value = parsed.database || "";
        $("cloudUser").value = parsed.user || "";
        $("cloudPass").value = parsed.password || "";
        $("cloudSsl").value = parsed.sslMode || "preferred";
        toast(`Parsed ${parsed.engine.toUpperCase()} connection (${parsed.host}:${parsed.port})`, "ok");
      } catch (err) {
        toast(errMsg(err), "err");
      }
    };

    $("btnTestCloudForm").onclick = async () => {
      const box = $("cloudTestStatus");
      const btn = $("btnTestCloudForm");
      btn.disabled = true;
      btn.textContent = "⏳ Testing...";
      box.hidden = false;
      box.style.color = "var(--text-muted)";
      box.textContent = "Connecting to remote database server...";
      try {
        const res = await api().TestCloudProfile(buildCloudObjFromForm());
        box.style.color = "var(--green)";
        box.textContent = `✅ ${res.message}`;
        toast(res.message, "ok");
      } catch (err) {
        box.style.color = "var(--red)";
        box.textContent = `⚠️ ${errMsg(err)}`;
        toast(errMsg(err), "err");
      } finally {
        btn.disabled = false;
        btn.textContent = "🔌 Test Connection";
      }
    };

    $("formCloudProfile").onsubmit = async (e) => {
      e.preventDefault();
      try {
        const profileObj = buildCloudObjFromForm();
        state.cloudProfiles = (await api().SaveCloudProfile(profileObj)) || [];
        if (profileObj?.id) invalidateSqlCache("cloud:" + profileObj.id);
        closeCloudForm();
        renderCloudView();
        toast("Cloud database profile saved", "ok");
      } catch (err) {
        toast(errMsg(err), "err");
      }
    };

    const loadSyncLocalDbs = async () => {
      const eng = $("syncLocalEngine").value;
      const dl = $("syncLocalDbOptions");
      if (!dl) return;
      try {
        const dbs = await api().ListDatabases(eng);
        dl.innerHTML = (dbs || [])
          .filter((d) => !SYSTEM_DBS.has(d.toLowerCase()))
          .map((d) => `<option value="${esc(d)}"></option>`)
          .join("");
      } catch {
        dl.innerHTML = "";
      }
    };

    const openCloudSyncModal = async (preselectCloudId = "") => {
      if (syncJob.running) {
        restoreCloudSync();
        return;
      }
      const sqlProfiles = (state.cloudProfiles || []).filter((p) => p.engine !== "redis");
      if (!sqlProfiles.length) {
        toast("Add a Cloud MySQL, MariaDB, PostgreSQL, or MS SQL profile first", "info");
        openCloudFormModal(null, "cpanel");
        return;
      }
      clearTimeout(syncHideTimer);
      syncJob.minimized = false;
      syncJob.state = "idle";
      updateFloatingSync();
      setSyncFormLocked(false);
      const sel = $("syncCloudProfile");
      sel.innerHTML = sqlProfiles
        .map((p) => `<option value="${esc(p.id)}">${esc(p.name)} (${esc(p.engine)} · ${esc(p.host)})</option>`)
        .join("");
      if (preselectCloudId) sel.value = preselectCloudId;
      const chosen = sqlProfiles.find((p) => p.id === sel.value) || sqlProfiles[0];
      if (chosen) {
        $("syncCloudDb").value = chosen.database || "";
        if ($("syncLocalEngine").querySelector(`option[value="${chosen.engine}"]`)) {
          $("syncLocalEngine").value = chosen.engine;
        }
      }
      await loadSyncLocalDbs();
      if ($("syncAlsoSaveSchedule")) $("syncAlsoSaveSchedule").checked = false;
      if ($("syncSchedMiniOpts")) $("syncSchedMiniOpts").hidden = true;
      if ($("btnSaveSyncScheduleOnly")) $("btnSaveSyncScheduleOnly").hidden = true;
      if ($("cloudSyncBox")) $("cloudSyncBox").hidden = true;
      $("modalCloudSync").hidden = false;
    };

    const saveScheduleFromSyncModal = async () => {
      const dir = $("syncDirTabs").querySelector(".seg.active")?.dataset.dir || "pull";
      const localEng = $("syncLocalEngine").value;
      const localDb = $("syncLocalDb").value.trim();
      const cloudId = $("syncCloudProfile").value;
      const cloudDb = $("syncCloudDb").value.trim();
      const c = (state.cloudProfiles || []).find((p) => p.id === cloudId);
      const syncTime = $("syncAutoTime")?.value || "02:00";
      const doBackup = $("syncAutoBackupChk") ? $("syncAutoBackupChk").checked : true;
      const bkpTime = $("syncAutoBackupTime")?.value || "02:00";
      const retDays = Math.max(1, Number($("syncAutoKeepDays")?.value) || 7);
      const existing = (state.schedules || []).find((s) => s.cloudId === cloudId && s.localDb === localDb);
      const payload = {
        id: existing?.id || "",
        name: existing?.name || `${c?.name || cloudDb} Daily`,
        enabled: true,
        cloudId,
        cloudDb,
        localEngine: localEng,
        localDb,
        autoSync: true,
        syncDirection: dir,
        syncTime,
        autoBackup: doBackup,
        backupTime: bkpTime,
        backupSource: "cloud",
        retentionDays: retDays,
      };
      state.schedules = (await api().SaveSchedule(payload)) || [];
      renderCloudView();
    };

    if ($("syncAlsoSaveSchedule")) {
      $("syncAlsoSaveSchedule").addEventListener("change", () => {
        const on = $("syncAlsoSaveSchedule").checked;
        if ($("syncSchedMiniOpts")) $("syncSchedMiniOpts").hidden = !on;
        if ($("btnSaveSyncScheduleOnly")) $("btnSaveSyncScheduleOnly").hidden = !on;
      });
    }
    if ($("btnSaveSyncScheduleOnly")) {
      $("btnSaveSyncScheduleOnly").onclick = async () => {
        if (!$("syncLocalDb").value.trim() || !$("syncCloudDb").value.trim()) {
          toast("Enter both Local Database and Cloud Database first", "err");
          return;
        }
        try {
          await saveScheduleFromSyncModal();
          closeCloudSync();
          toast("Saved daily Auto-Sync & 2:00 AM Auto-Backup (7-day purge) schedule!", "ok");
        } catch (err) {
          toast(errMsg(err), "err");
        }
      };
    }

    const setSyncProgress = (pct, mode, title, detail) => {
      const clamped = Math.max(0, Math.min(100, Number(pct) || 0));
      syncJob.percent = clamped;
      syncJob.state = mode || "running";
      syncJob.stage = title || "Syncing...";
      syncJob.detail = detail || "";

      const box = $("cloudSyncBox");
      if (box) {
        box.hidden = false;
        box.className = "sysup-fetch-box" + (mode === "done" ? " success" : (mode === "error" || mode === "cancelled") ? " error" : "");
        const sp = $("cloudSyncSpinner");
        if (sp) sp.hidden = mode !== "running";
        if ($("cloudSyncTitle")) $("cloudSyncTitle").textContent = syncJob.stage;
        if ($("cloudSyncPct")) $("cloudSyncPct").textContent = `${clamped}%`;
        if ($("cloudSyncBar")) $("cloudSyncBar").style.width = `${clamped}%`;
        if ($("cloudSyncSub")) $("cloudSyncSub").textContent = syncJob.detail;
      }
      const btn = $("btnRunCloudSync");
      if (btn && mode === "running") {
        btn.textContent = `⏳ Syncing ${clamped}%`;
      }
      updateFloatingSync();
    };

    const cancelRunningSync = async () => {
      if (!syncJob.running) {
        closeCloudSync();
        return;
      }
      if (syncJob.cancelling) return;
      syncJob.cancelling = true;
      setSyncFormLocked(true);
      setSyncProgress(syncJob.percent, "running", "🛑 Cancelling sync...", "Stopping active export/import processes...");
      try {
        await api().CancelCloudSync();
      } catch {
        // Ignore IPC cancel error; the main CloudSyncDatabase promise settles the UI state
      }
    };

    rt()?.EventsOn?.("cloud-sync-progress", (p) => {
      if (!p) return;
      if ($("modalCloudSync")?.hidden && !syncJob.running && !syncJob.minimized) return;
      if (syncJob.cancelling && p.state === "running") return;
      setSyncProgress(p.percent, p.state || "running", p.stage || "Syncing...", p.detail || "");
    });
    rt()?.EventsOn?.("schedules-updated", (list) => {
      state.schedules = list || [];
      if (state.tab === "cloud") renderCloudView();
    });

    $("syncCloudProfile").addEventListener("change", () => {
      const c = (state.cloudProfiles || []).find((p) => p.id === $("syncCloudProfile").value);
      if (c && c.database) $("syncCloudDb").value = c.database;
    });
    $("syncLocalEngine").addEventListener("change", loadSyncLocalDbs);
    $("syncDirTabs").addEventListener("click", (e) => {
      const btn = e.target.closest("[data-dir]");
      if (!btn || syncJob.running) return;
      $("syncDirTabs").querySelectorAll(".seg").forEach((b) => b.classList.toggle("active", b === btn));
    });
    $("btnOpenCloudSync").onclick = () => openCloudSyncModal("");
    if ($("btnMinimizeCloudSync")) $("btnMinimizeCloudSync").onclick = minimizeCloudSync;
    if ($("btnRestoreCloudSync")) $("btnRestoreCloudSync").onclick = restoreCloudSync;
    if ($("btnFloatCancelSync")) {
      $("btnFloatCancelSync").onclick = (e) => {
        e.stopPropagation();
        cancelRunningSync();
      };
    }
    if ($("syncFloatingBar")) {
      $("syncFloatingBar").onclick = (e) => {
        if (e.target.closest("#btnFloatCancelSync")) return;
        restoreCloudSync();
      };
    }
    $("btnCloseCloudSync").onclick = closeCloudSync;
    $("btnCancelCloudSync").onclick = () => (syncJob.running ? cancelRunningSync() : closeCloudSync());

    $("formCloudSync").onsubmit = async (e) => {
      e.preventDefault();
      if (syncJob.running) {
        restoreCloudSync();
        return;
      }
      clearTimeout(syncHideTimer);
      const dir = $("syncDirTabs").querySelector(".seg.active")?.dataset.dir || "push";
      const localEng = $("syncLocalEngine").value;
      const localDb = $("syncLocalDb").value.trim();
      const cloudId = $("syncCloudProfile").value;
      const cloudDb = $("syncCloudDb").value.trim();
      const btn = $("btnRunCloudSync");
      btn.disabled = true;
      btn.textContent = "⏳ Syncing 0%";
      syncJob.running = true;
      syncJob.cancelling = false;
      syncJob.minimized = false;
      syncJob.summary = dir === "push" ? `${localDb} → ${cloudDb}` : `${cloudDb} → ${localDb}`;
      setSyncFormLocked(true);
      setSyncProgress(
        2,
        "running",
        "⏳ Starting database sync...",
        dir === "push"
          ? `Preparing Push: Local (${localEng}/${localDb}) → Cloud (${cloudDb})`
          : `Preparing Pull: Cloud (${cloudDb}) → Local (${localEng}/${localDb})`
      );
      try {
        if ($("syncAlsoSaveSchedule")?.checked) {
          await saveScheduleFromSyncModal();
        }
        const msg = dir === "push"
          ? await api().CloudSyncDatabase(localEng, localDb, "cloud:" + cloudId, cloudDb)
          : await api().CloudSyncDatabase("cloud:" + cloudId, cloudDb, localEng, localDb);
        syncJob.running = false;
        syncJob.cancelling = false;
        setSyncFormLocked(false);
        setSyncProgress(100, "done", "✅ Sync Complete!", msg || "Database synced successfully.");
        toast(msg || "Sync complete!", "ok");
        invalidateSqlCache();
        if (state.tab === "database") await loadSqlDatabases(true);
        if (syncJob.minimized) {
          syncHideTimer = setTimeout(() => {
            if (!syncJob.running && syncJob.minimized) {
              syncJob.minimized = false;
              updateFloatingSync();
            }
          }, 2200);
        } else {
          syncHideTimer = setTimeout(() => {
            if (!syncJob.running && !$("modalCloudSync").hidden) closeCloudSync();
          }, 900);
        }
      } catch (err) {
        const em = errMsg(err);
        const wasCancelled = syncJob.cancelling || /cancel/i.test(em);
        syncJob.running = false;
        syncJob.cancelling = false;
        setSyncFormLocked(false);
        if (wasCancelled) {
          setSyncProgress(syncJob.percent, "cancelled", "🛑 Sync Cancelled", "Database sync was cancelled by user.");
          syncJob.minimized = false;
          updateFloatingSync();
          toast("Database sync cancelled.", "info");
        } else {
          setSyncProgress(syncJob.percent, "error", "⚠️ Sync Failed", em);
          toast(em, "err");
          if (syncJob.minimized) {
            restoreCloudSync();
          }
        }
      } finally {
        syncJob.running = false;
        syncJob.cancelling = false;
        setSyncFormLocked(false);
        btn.disabled = false;
        btn.textContent = "🔄 Sync Now";
      }
    };

    // ---- Daily Auto-Sync & 2:00 AM Auto-Backup Schedule Modal & Snapshot Viewer ----
    const closeScheduleModal = () => ($("modalScheduleForm").hidden = true);
    const openScheduleModal = (existing = null) => {
      $("formSchedule").reset();
      $("schedEditId").value = existing?.id || "";
      const sel = $("schedCloudId");
      const sqlProfiles = (state.cloudProfiles || []).filter((p) => p.engine !== "redis");
      sel.innerHTML = `<option value="">-- Local Only (No Cloud Sync) --</option>` + sqlProfiles
        .map((p) => `<option value="${esc(p.id)}">${esc(p.name)} (${esc(p.engine)} · ${esc(p.host)})</option>`)
        .join("");

      if (existing) {
        $("modalScheduleTitle").textContent = `Edit Schedule — ${existing.name}`;
        $("schedName").value = existing.name || "";
        $("schedEnabled").checked = existing.enabled !== false;
        sel.value = existing.cloudId || "";
        $("schedCloudDb").value = existing.cloudDb || "";
        $("schedLocalEngine").value = existing.localEngine || "mssql";
        $("schedLocalDb").value = existing.localDb || "";
        $("schedAutoSync").checked = !!existing.autoSync;
        $("schedSyncDir").value = existing.syncDirection || "pull";
        $("schedSyncTime").value = existing.syncTime || "02:00";
        $("schedAutoBackup").checked = existing.autoBackup !== false;
        $("schedBackupSource").value = existing.backupSource || "cloud";
        $("schedBackupTime").value = existing.backupTime || "02:00";
        $("schedRetentionDays").value = existing.retentionDays || 7;
      } else {
        $("modalScheduleTitle").textContent = "⏰ New Daily Auto-Sync & 2:00 AM Auto-Backup";
        const first = sqlProfiles[0];
        if (first) {
          sel.value = first.id;
          $("schedCloudDb").value = first.database || "";
          $("schedLocalEngine").value = first.engine || "mssql";
          $("schedName").value = `${first.name} Daily (2 AM)`;
        }
        $("schedEnabled").checked = true;
        $("schedAutoSync").checked = true;
        $("schedSyncDir").value = "pull";
        $("schedSyncTime").value = "02:00";
        $("schedAutoBackup").checked = true;
        $("schedBackupSource").value = first ? "cloud" : "local";
        $("schedBackupTime").value = "02:00";
        $("schedRetentionDays").value = 7;
      }
      $("modalScheduleForm").hidden = false;
    };

    $("schedCloudId")?.addEventListener("change", () => {
      const c = (state.cloudProfiles || []).find((p) => p.id === $("schedCloudId").value);
      if (c) {
        if (c.database) $("schedCloudDb").value = c.database;
        if ($("schedLocalEngine").querySelector(`option[value="${c.engine}"]`)) {
          $("schedLocalEngine").value = c.engine;
        }
      }
    });
    if ($("btnNewSchedule")) $("btnNewSchedule").onclick = () => openScheduleModal(null);
    if ($("btnAddSchedule")) $("btnAddSchedule").onclick = () => openScheduleModal(null);
    if ($("btnCloseScheduleForm")) $("btnCloseScheduleForm").onclick = closeScheduleModal;
    if ($("btnCancelScheduleForm")) $("btnCancelScheduleForm").onclick = closeScheduleModal;

    if ($("formSchedule")) {
      $("formSchedule").onsubmit = async (e) => {
        e.preventDefault();
        const existing = (state.schedules || []).find((x) => x.id === $("schedEditId").value);
        const payload = {
          id: $("schedEditId").value.trim(),
          name: $("schedName").value.trim(),
          enabled: $("schedEnabled").checked,
          cloudId: $("schedCloudId").value,
          cloudDb: $("schedCloudDb").value.trim(),
          localEngine: $("schedLocalEngine").value,
          localDb: $("schedLocalDb").value.trim(),
          autoSync: $("schedAutoSync").checked,
          syncDirection: $("schedSyncDir").value || "pull",
          syncTime: $("schedSyncTime").value || "02:00",
          autoBackup: $("schedAutoBackup").checked,
          backupSource: $("schedBackupSource").value || "cloud",
          backupTime: $("schedBackupTime").value || "02:00",
          retentionDays: Math.max(1, Number($("schedRetentionDays").value) || 7),
          createdAt: existing?.createdAt || "",
        };
        try {
          state.schedules = (await api().SaveSchedule(payload)) || [];
          closeScheduleModal();
          renderCloudView();
          toast("Saved daily Auto-Sync / Auto-Backup schedule!", "ok");
        } catch (err) {
          toast(errMsg(err), "err");
        }
      };
    }

    let activeSnapSchedId = "";
    const closeSnapModal = () => ($("modalScheduleSnapshots").hidden = true);
    const loadSnapshotsModal = async (schedId) => {
      activeSnapSchedId = schedId;
      const sched = (state.schedules || []).find((s) => s.id === schedId);
      $("modalSnapTitle").textContent = `📦 Snapshots — ${sched ? sched.name : ""}`;
      $("modalSnapMeta").textContent = `Retention policy: Purge snapshots ≥ ${sched?.retentionDays || 7} day(s) (1 week)`;
      const list = (await api().ScheduleSnapshots(schedId).catch(() => [])) || [];
      const box = $("snapListContainer");
      if (!list.length) {
        box.innerHTML = `<div class="empty-note" style="padding:18px">No snapshots saved yet. Click <strong>▶ Backup Now</strong> or wait for the 2:00 AM daily run.</div>`;
      } else {
        box.innerHTML = list
          .map(
            (sn) => `<div class="folder-row" style="padding:8px 10px">
              <div style="min-width:0;flex:1">
                <strong style="font-size:12.5px;display:block;overflow:hidden;text-overflow:ellipsis;white-space:nowrap" title="${esc(sn.path)}">${esc(sn.name)}</strong>
                <span class="muted" style="font-size:11px">${esc(sn.createdAt)} · ${esc(sn.sizeText)}</span>
              </div>
              <span class="pill ${sn.expired ? "stopped" : "running"}" style="flex-shrink:0">${esc(sn.expiresIn)}</span>
            </div>`
          )
          .join("");
      }
      $("modalScheduleSnapshots").hidden = false;
    };
    if ($("btnCloseSnapModal")) $("btnCloseSnapModal").onclick = closeSnapModal;
    if ($("btnDoneSnapModal")) $("btnDoneSnapModal").onclick = closeSnapModal;
    if ($("btnSnapOpenFolder")) $("btnSnapOpenFolder").onclick = () => api().OpenScheduleFolder(activeSnapSchedId).catch((e) => toast(errMsg(e), "err"));
    if ($("btnSnapPurgeNow")) {
      $("btnSnapPurgeNow").onclick = async () => {
        try {
          const n = await api().PurgeScheduleSnapshots(activeSnapSchedId);
          toast(`Purged ${n} expired snapshot(s).`, n > 0 ? "ok" : "info");
          await loadSnapshotsModal(activeSnapSchedId);
        } catch (err) {
          toast(errMsg(err), "err");
        }
      };
    }
    if ($("btnSnapRunBackupNow")) {
      $("btnSnapRunBackupNow").onclick = async () => {
        const btn = $("btnSnapRunBackupNow");
        btn.disabled = true;
        btn.textContent = "⏳ Backing up...";
        try {
          const msg = await api().RunScheduleNow(activeSnapSchedId, "backup");
          toast(msg || "Snapshot created!", "ok");
          state.schedules = (await api().Schedules()) || state.schedules;
          renderSchedules();
          await loadSnapshotsModal(activeSnapSchedId);
        } catch (err) {
          toast(errMsg(err), "err");
        } finally {
          btn.disabled = false;
          btn.textContent = "▶ Backup Now";
        }
      };
    }

    if ($("btnOpenBackupsFolder")) $("btnOpenBackupsFolder").onclick = () => api().OpenFolder("backups").catch((e) => toast(errMsg(e), "err"));
    if ($("btnPurgeAllSnapshots")) {
      $("btnPurgeAllSnapshots").onclick = async () => {
        try {
          const n = await api().PurgeScheduleSnapshots("");
          toast(n > 0 ? `Purged ${n} snapshot(s) aged >= retention limit.` : "No snapshots met the 7-day purge age yet.", n > 0 ? "ok" : "info");
        } catch (err) {
          toast(errMsg(err), "err");
        }
      };
    }
    if ($("btnToggleWinTask")) {
      $("btnToggleWinTask").onclick = async () => {
        const wantEnable = !state.winTask?.registered;
        try {
          await api().SetWindowsTask("02:00", wantEnable);
          state.winTask = (await api().WindowsTaskStatus()) || { registered: wantEnable, detail: "" };
          renderSchedules();
          toast(wantEnable ? "Registered daily 2:00 AM Windows Task Scheduler job!" : "Removed Windows 2:00 AM scheduled task.", "ok");
        } catch (err) {
          toast(errMsg(err), "err");
        }
      };
    }

    if ($("cloudScheduleGrid")) {
      $("cloudScheduleGrid").addEventListener("click", async (e) => {
        const btn = e.target.closest("[data-sact]");
        if (!btn) return;
        const card = btn.closest("[data-sid]");
        const id = card?.dataset.sid;
        const s = (state.schedules || []).find((x) => x.id === id);
        if (!s) return;
        const act = btn.dataset.sact;
        if (act === "run") {
          const prev = btn.textContent;
          btn.disabled = true;
          btn.textContent = "⏳ Running...";
          try {
            const msg = await api().RunScheduleNow(s.id, "both");
            toast(msg || "Schedule run complete!", "ok");
            state.schedules = (await api().Schedules()) || state.schedules;
            renderCloudView();
          } catch (err) {
            toast(errMsg(err), "err");
          } finally {
            btn.disabled = false;
            btn.textContent = prev;
          }
        } else if (act === "snaps") {
          await loadSnapshotsModal(s.id);
        } else if (act === "purge") {
          try {
            const n = await api().PurgeScheduleSnapshots(s.id);
            toast(n > 0 ? `Purged ${n} snapshot(s) >= ${s.retentionDays || 7} days.` : `No snapshots >= ${s.retentionDays || 7} days old.`, n > 0 ? "ok" : "info");
          } catch (err) {
            toast(errMsg(err), "err");
          }
        } else if (act === "folder") {
          await api().OpenScheduleFolder(s.id);
        } else if (act === "toggle") {
          try {
            state.schedules = (await api().SaveSchedule({ ...s, enabled: !s.enabled })) || [];
            renderCloudView();
          } catch (err) {
            toast(errMsg(err), "err");
          }
        } else if (act === "edit") {
          openScheduleModal(s);
        } else if (act === "del") {
          if (!(await askConfirm("Delete Schedule?", `Remove daily schedule "${s.name}"? (Existing backup snapshots on disk will be kept.)`, "Delete"))) return;
          state.schedules = (await api().DeleteSchedule(s.id)) || [];
          renderCloudView();
          toast(`Deleted schedule "${s.name}"`, "ok");
        }
      });
    }

    $("cloudProfileGrid").addEventListener("click", async (e) => {
      const btn = e.target.closest("[data-cact]");
      if (!btn) return;
      const card = btn.closest("[data-cid]");
      const id = card?.dataset.cid;
      const c = (state.cloudProfiles || []).find((p) => p.id === id);
      if (!c) return;
      const act = btn.dataset.cact;

      if (act === "browse") {
        state.dbEngine = "cloud:" + c.id;
        resetSqlBrowserState(c.database || "");
        switchTab("database");
      } else if (act === "test") {
        const prev = btn.textContent;
        btn.disabled = true;
        btn.textContent = "⏳...";
        try {
          const res = await api().TestCloudProfile(c);
          state.cloudProfiles = (await api().CloudProfiles()) || state.cloudProfiles;
          renderCloudView();
          toast(res.message, "ok");
        } catch (err) {
          toast(errMsg(err), "err");
        } finally {
          btn.disabled = false;
          btn.textContent = prev;
        }
      } else if (act === "backupzip") {
        let db = c.database || "";
        if (!db) {
          try {
            const dbs = await api().ListDatabases("cloud:" + c.id);
            db = (dbs || []).find((d) => !SYSTEM_DBS.has(d.toLowerCase())) || (dbs || [])[0] || "";
          } catch (err) {
            toast(errMsg(err), "err");
            return;
          }
        }
        if (!db) {
          toast("Set a Default Database on this Cloud profile first", "err");
          return;
        }
        const prev = btn.textContent;
        btn.disabled = true;
        btn.textContent = "⏳ 0%";
        state.activeBackupBtn = btn;
        try {
          toast(`Creating full .zip backup of ${db}...`, "info");
          const outPath = await api().ExportDatabaseDialog("cloud:" + c.id, db, [], "zip", true, true);
          if (outPath) toast(`Full .zip backup saved: ${outPath}`, "ok");
        } catch (err) {
          toast(errMsg(err), "err");
        } finally {
          if (state.activeBackupBtn === btn) state.activeBackupBtn = null;
          btn.disabled = false;
          btn.textContent = prev;
        }
      } else if (act === "sync") {
        await openCloudSyncModal(c.id);
      } else if (act === "panel" && c.panelUrl) {
        if (rt()?.BrowserOpenURL) rt().BrowserOpenURL(c.panelUrl);
        else window.open(c.panelUrl, "_blank");
      } else if (act === "uri") {
        const uri = await api().ConnectionString("cloud:" + c.id, c.database || "");
        await copyText(uri);
        toast("Copied cloud connection URI", "ok");
      } else if (act === "edit") {
        openCloudFormModal(c);
      } else if (act === "del") {
        if (!(await askConfirm("Delete Cloud Profile?", `Remove "${c.name}" from saved cloud profiles?`, "Delete"))) return;
        const cloudEng = "cloud:" + c.id;
        invalidateSqlCache(cloudEng);
        state.sqlTabs = state.sqlTabs.filter((t) => t.engine !== cloudEng);
        activeSqlTabByEngine.delete(cloudEng);
        state.cloudProfiles = (await api().DeleteCloudProfile(c.id)) || [];
        if (state.dbEngine === cloudEng) {
          state.dbEngine = "mariadb";
          resetSqlBrowserState("");
        }
        renderCloudView();
        toast(`Removed ${c.name}`, "ok");
      }
    });

    if ($("btnCloudBarBackupZip")) {
      $("btnCloudBarBackupZip").onclick = () => {
        const c = getActiveCloudProfile();
        const db = state.sqlDb || c?.database || "";
        if (!db) {
          toast("Select a database first", "err");
          return;
        }
        api().ExportDatabaseDialog(state.dbEngine, db, [], "zip", true, true).then((outPath) => {
          if (outPath) toast(`Full .zip backup saved: ${outPath}`, "ok");
        }).catch((err) => toast(errMsg(err), "err"));
      };
    }
    if ($("btnCloudBarSync")) {
      $("btnCloudBarSync").onclick = () => {
        const c = getActiveCloudProfile();
        openCloudSyncModal(c ? c.id : "");
      };
    }
    if ($("btnCloudBarAll")) $("btnCloudBarAll").onclick = () => switchTab("cloud");
    if ($("btnCloudBarLocal")) {
      $("btnCloudBarLocal").onclick = () => {
        state.dbEngine = "mariadb";
        resetSqlBrowserState("");
        pickBestDbEngine();
        renderDbEngineTabs();
        loadDbView();
      };
    }
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
