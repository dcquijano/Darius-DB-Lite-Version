(() => {
  const CHECKLIST_KEY = "dariusdb.guide.checklist.v1";

  const $ = (id) => document.getElementById(id);
  const esc = (s) =>
    String(s ?? "")
      .replace(/&/g, "&amp;")
      .replace(/</g, "&lt;")
      .replace(/>/g, "&gt;")
      .replace(/"/g, "&quot;")
      .replace(/'/g, "&#39;");

  function renderKeys(keys) {
    return keys
      .map((combo) =>
        combo
          .map((k) => `<kbd class="kbd">${esc(k)}</kbd>`)
          .join('<span class="kbd-plus">+</span>')
      )
      .join('<span class="kbd-or">or</span>');
  }

  function isEditableTarget(target) {
    if (!target || target === document.body || target === document.documentElement) return false;
    const tag = (target.tagName || "").toUpperCase();
    if (tag === "INPUT" || tag === "TEXTAREA" || tag === "SELECT") return true;
    return !!target.isContentEditable;
  }

  function isTextInputTarget(target) {
    if (!target) return false;
    const tag = (target.tagName || "").toUpperCase();
    if (tag === "TEXTAREA") return true;
    if (tag === "INPUT") {
      const t = (target.type || "text").toLowerCase();
      return !["checkbox", "radio", "button", "submit", "reset", "range", "color"].includes(t);
    }
    return !!target.isContentEditable;
  }

  // Single source of truth for keyboard shortcuts (used by both runtime dispatcher and UI reference)
  const SHORTCUT_GROUPS = [
    {
      id: "navigation",
      title: "Navigation",
      scope: "global",
      scopeLabel: "Anywhere",
      items: [
        { id: "nav-dashboard", keys: [["Alt", "1"], ["Ctrl", "1"]], label: "Open Dashboard (Services)", action: (h) => h.switchTab("dashboard") },
        { id: "nav-database", keys: [["Alt", "2"], ["Ctrl", "2"]], label: "Open Database Browser", action: (h) => h.switchTab("database") },
        { id: "nav-cloud", keys: [["Alt", "3"], ["Ctrl", "3"]], label: "Open Cloud Databases", action: (h) => h.switchTab("cloud") },
        { id: "nav-versions", keys: [["Alt", "4"], ["Ctrl", "4"]], label: "Open Versions Manager", action: (h) => h.switchTab("versions") },
        { id: "nav-config", keys: [["Alt", "5"], ["Ctrl", "5"]], label: "Open Config & Logs", action: (h) => h.switchTab("config") },
        { id: "nav-terminal", keys: [["Alt", "6"], ["Ctrl", "6"]], label: "Open Built-in Terminal", action: (h) => h.switchTab("terminal") },
        { id: "nav-settings", keys: [["Alt", "7"], ["Ctrl", "7"]], label: "Open Settings", action: (h) => h.switchTab("settings") },
        { id: "nav-guide", keys: [["Alt", "8"], ["Ctrl", "8"]], label: "Open Step-by-Step Guide & Shortcuts", action: (h) => h.switchTab("guide") },
        { id: "help-modal", keys: [["F1"], ["Ctrl", "/"]], altHint: "? outside inputs", label: "Toggle Keyboard Shortcuts Popup", action: null },
        { id: "modal-esc", keys: [["Esc"]], label: "Close active popup / dialog", action: null },
      ],
    },
    {
      id: "global-actions",
      title: "Global Service & System Controls",
      scope: "global",
      scopeLabel: "Anywhere",
      items: [
        { id: "start-all", keys: [["F8"], ["Ctrl", "Shift", "S"]], altHint: "or Alt+Shift+S", label: "Start All enabled services", action: (h) => h.clickBtn("btnStartAll") },
        { id: "stop-all", keys: [["F9"], ["Ctrl", "Shift", "X"]], altHint: "or Alt+Shift+X", label: "Stop All running services", action: (h) => h.clickBtn("btnStopAll") },
        { id: "repair-one", keys: [["Ctrl", "Shift", "R"]], label: "Open Repair Service dialog", action: (h) => h.clickBtn("btnOpenRepair") },
        { id: "repair-all", keys: [["Ctrl", "Shift", "A"]], label: "Open Repair All (Full System) dialog", action: (h) => h.clickBtn("btnRepairAllSystem") },
        { id: "sys-updater", keys: [["Ctrl", "Shift", "U"]], label: "Open System Version Updater", action: (h) => h.clickBtn("btnOpenSysUpdater") },
        { id: "open-root", keys: [["Ctrl", "Shift", "O"]], label: "Open Portable Root Folder in Explorer", action: (h) => h.clickBtn("btnSideRoot") },
        { id: "hide-tray", keys: [["Ctrl", "Shift", "H"]], label: "Hide window to Windows System Tray", action: (h) => h.clickBtn("btnSideTray") },
        { id: "jump-term", keys: [["Ctrl", "`"]], label: "Jump to Built-in Terminal & focus prompt", action: (h) => h.focusTerminal() },
        { id: "refresh-view", keys: [["F5"]], label: "Refresh current view (no full reload)", action: (h) => h.refreshCurrentView() },
      ],
    },
    {
      id: "database",
      title: "Database Browser (SQL & Redis)",
      scope: "database",
      scopeLabel: "Database Tab",
      items: [
        { id: "sql-run", keys: [["Ctrl", "Enter"]], label: "Run SQL query in active tab", action: (h) => h.runSqlQuery() },
        { id: "sql-show-all", keys: [["Ctrl", "Shift", "Enter"]], label: "Show All rows (removes LIMIT / TOP)", action: (h) => h.clickBtn("btnSqlShowAll") },
        { id: "sql-new-tab", keys: [["Alt", "N"]], label: "Open new SQL query tab", action: (h) => h.openNewSqlTab() },
        { id: "sql-close-tab", keys: [["Alt", "W"]], label: "Close current SQL query tab", action: (h) => h.closeActiveSqlTab() },
        { id: "sql-prev-tab", keys: [["Alt", "["]], label: "Switch to previous SQL query tab", action: (h) => h.cycleSqlTab(-1) },
        { id: "sql-next-tab", keys: [["Alt", "]"]], label: "Switch to next SQL query tab", action: (h) => h.cycleSqlTab(1) },
        { id: "sql-clear", keys: [["Ctrl", "Shift", "K"]], label: "Clear SQL editor", action: (h) => h.clickBtn("btnSqlClear") },
        { id: "db-filter", keys: [["Ctrl", "F"]], label: "Focus Row Filter (SQL) or Key Pattern (Redis)", action: (h) => h.focusDbSearch() },
        { id: "db-new", keys: [["Alt", "D"]], label: "Create new Database (+ user)", action: (h) => h.clickBtn("btnNewDb") },
        { id: "db-new-table", keys: [["Alt", "T"]], label: "Create new Table in active database", action: (h) => h.clickBtn("btnNewTable") },
        { id: "db-new-row", keys: [["Alt", "="]], label: "Create / Insert new Row in active table", action: (h) => h.clickBtn("btnRowCreate") },
        { id: "db-import", keys: [["Alt", "I"]], label: "Import .sql / .csv / .bak file", action: (h) => h.clickBtn("btnOpenImport") },
        { id: "db-export", keys: [["Alt", "E"]], label: "Export database or selected tables", action: (h) => h.clickBtn("btnBackupSql") },
        { id: "db-backup-zip", keys: [["Alt", "B"]], label: "Create Full .zip Backup (SQL + CSVs)", action: (h) => h.clickBtn("btnBackupZip") },
        { id: "db-copy-uri", keys: [["Alt", "C"]], label: "Copy Connection URI to clipboard", action: (h) => h.clickBtn("btnCopyConn") },
        { id: "db-refresh", keys: [["Alt", "R"]], label: "Refresh databases, tables, or Redis keys", action: (h) => h.clickBtn("btnDbRefresh") },
        { id: "db-prev-page", keys: [["Alt", "←"]], label: "Previous results page", action: (h) => h.clickBtn("btnPagePrev") },
        { id: "db-next-page", keys: [["Alt", "→"]], label: "Next results page", action: (h) => h.clickBtn("btnPageNext") },
      ],
    },
    {
      id: "cloud",
      title: "Cloud Databases & Auto-Sync",
      scope: "cloud",
      scopeLabel: "Cloud Tab",
      items: [
        { id: "cloud-add", keys: [["Alt", "N"]], label: "Add Cloud Database profile", action: (h) => h.clickBtn("btnAddCloud") },
        { id: "cloud-sync", keys: [["Alt", "S"]], label: "Open Manual Local ↔ Cloud Sync dialog", action: (h) => h.clickBtn("btnOpenCloudSync") },
        { id: "cloud-sched", keys: [["Alt", "B"]], label: "New Daily Auto-Sync Schedule", action: (h) => h.clickBtn("btnNewSchedule") },
      ],
    },
    {
      id: "versions",
      title: "Versions Manager",
      scope: "versions",
      scopeLabel: "Versions Tab",
      items: [
        { id: "ver-check", keys: [["Alt", "R"]], label: "Check newest upstream engine versions", action: (h) => h.clickBtn("btnRefreshCatalog") },
        { id: "ver-import", keys: [["Alt", "I"]], label: "Import local portable .zip archive", action: (h) => h.clickBtn("btnInstallZip") },
        { id: "ver-manual", keys: [["Alt", "M"]], label: "Open Manual / Custom Version URL modal", action: (h) => h.clickBtn("btnManualModal") },
      ],
    },
    {
      id: "config",
      title: "Config & Logs",
      scope: "config",
      scopeLabel: "Config & Logs Tab",
      items: [
        { id: "cfg-save", keys: [["Ctrl", "S"]], label: "Save active configuration file", action: (h) => h.saveConfig(false) },
        { id: "cfg-save-restart", keys: [["Ctrl", "Shift", "Enter"]], label: "Save config & restart running service", action: (h) => h.saveConfig(true) },
        { id: "cfg-toggle", keys: [["Alt", "L"]], label: "Toggle between Config File and Service Log", action: (h) => h.toggleConfigLogMode() },
        { id: "cfg-reload", keys: [["Alt", "R"]], label: "Reload config file or refresh log", action: (h) => h.refreshCurrentView() },
      ],
    },
    {
      id: "terminal",
      title: "Built-in Multi-Mode Terminal",
      scope: "terminal",
      scopeLabel: "Terminal Tab",
      items: [
        { id: "term-run", keys: [["Enter"]], label: "Execute typed command", action: null },
        { id: "term-hist", keys: [["↑"], ["↓"]], label: "Browse command history (previous / next)", action: null },
        { id: "term-clear", keys: [["Ctrl", "L"]], label: "Clear terminal screen output", action: (h) => h.clearTerminal() },
        { id: "term-cancel", keys: [["Ctrl", "C"]], label: "Stop running command (when no text selected)", action: null },
        { id: "term-prev-mode", keys: [["Alt", "["]], label: "Previous terminal mode (Shell / DB CLIs)", action: (h) => h.cycleTermMode(-1) },
        { id: "term-next-mode", keys: [["Alt", "]"]], label: "Next terminal mode (Shell / DB CLIs)", action: (h) => h.cycleTermMode(1) },
      ],
    },
    {
      id: "settings-guide",
      title: "Settings & Guide",
      scope: "settings",
      scopeLabel: "Settings / Guide Tab",
      items: [
        { id: "settings-save", keys: [["Ctrl", "S"]], label: "Save Settings (on Settings tab)", action: (h) => h.saveSettings() },
        { id: "guide-search", keys: [["Ctrl", "F"]], label: "Search steps, topics & shortcuts (on Guide tab)", action: (h) => h.focusGuideSearch() },
      ],
    },
  ];

  // Tooltips applied to existing buttons so shortcuts are discoverable inline
  const BUTTON_SHORTCUT_HINTS = {
    btnStartAll: "Start all enabled services (F8, Ctrl+Shift+S, or Alt+Shift+S)",
    btnStopAll: "Stop all running services (F9, Ctrl+Shift+X, or Alt+Shift+X)",
    btnDashGuides: "Open Step-by-Step Visual Guide with Screenshots (Alt+8)",
    btnDashShortcuts: "Open Keyboard Shortcuts quick reference (F1 or Ctrl+/)",
    btnOpenRepair: "Repair a single selected service/engine only (Ctrl+Shift+R)",
    btnRepairAllSystem: "Repair All system components, blocked ports, caches, locks & PATH (Ctrl+Shift+A)",
    btnOpenSysUpdater: "Check for Darius DB System Updates from GitHub (Ctrl+Shift+U)",
    btnSideTerminal: "Open Built-in Terminal (Alt+6 or Ctrl+`)",
    btnSideRoot: "Open portable folder in Explorer (Ctrl+Shift+O)",
    btnSideTray: "Minimize to system tray (Ctrl+Shift+H)",
    btnSideShortcuts: "Open Keyboard Shortcuts quick reference (F1 or Ctrl+/)",
    btnNewSqlTab: "Open new SQL query tab (Alt+N)",
    btnSqlRun: "Run SQL query (Ctrl+Enter)",
    btnSqlShowAll: "Show all rows without LIMIT (Ctrl+Shift+Enter)",
    btnSqlClear: "Clear SQL editor (Ctrl+Shift+K)",
    btnNewDb: "Create new database (Alt+D)",
    btnNewTable: "Create new table (Alt+T)",
    btnRowCreate: "Insert new row in active table (Alt+=)",
    btnOpenImport: "Import .sql, .csv, or .bak file (Alt+I)",
    btnBackupSql: "Export database or selected tables (Alt+E)",
    btnBackupZip: "Full .zip backup: SQL + CSVs + manifest (Alt+B)",
    btnCopyConn: "Copy connection URI (Alt+C)",
    btnDbRefresh: "Refresh databases & tables (Alt+R or F5)",
    btnPagePrev: "Previous page (Alt+Left)",
    btnPageNext: "Next page (Alt+Right)",
    btnAddCloud: "Add Cloud Database profile (Alt+N)",
    btnOpenCloudSync: "One-time Manual Local ↔ Cloud Sync (Alt+S)",
    btnNewSchedule: "New Daily Auto-Sync schedule (Alt+B)",
    btnRefreshCatalog: "Check newest official versions online (Alt+R or F5)",
    btnInstallZip: "Import portable .zip archive (Alt+I)",
    btnManualModal: "Manual download or custom version URL (Alt+M)",
    btnCfgSave: "Save config file (Ctrl+S)",
    btnCfgSaveRestart: "Save config & restart service (Ctrl+Shift+Enter)",
    btnCfgReload: "Reload config from disk (Alt+R or F5)",
    btnTermClear: "Clear terminal output (Ctrl+L)",
    btnSaveSettings: "Save settings to settings.json (Ctrl+S)",
  };

  const QUICKSTART_CHECKLIST = [
    { id: "chk-install", label: "Install at least one engine in Versions (MariaDB, MySQL, PostgreSQL, Redis, PHP, or Mailpit)", goTab: "versions", goLabel: "Open Versions" },
    { id: "chk-start", label: "Start your services on Dashboard (press F8 / Ctrl+Shift+S for Start All; F9 / Alt+Shift+X for Stop All)", goTab: "dashboard", goLabel: "Open Dashboard" },
    { id: "chk-db", label: "Open Database Browser, create a database (+ New), and run your first query with Ctrl+Enter", goTab: "database", goLabel: "Open Database" },
    { id: "chk-backup", label: "Test Import (.sql/.csv/.bak) or Full .zip Backup to keep snapshots safe", goTab: "database", goLabel: "Database Tools" },
    { id: "chk-cloud", label: "(Optional) Add a Cloud DB profile or schedule a Daily 2:00 AM Auto-Sync & Backup", goTab: "cloud", goLabel: "Open Cloud" },
    { id: "chk-cli", label: "Add 'darius' CLI & DB tools to Windows PATH in Settings for Visual Studio / VS Code", goTab: "settings", goLabel: "Open Settings" },
  ];

  const GUIDE_STEPS = [
    {
      num: 1,
      id: "step-install",
      badge: "Setup",
      title: "Install Portable Database & Web Engines",
      summary: "Download official portable builds into bin/ with zero installer or registry bloat.",
      shortcut: "Alt+4",
      images: [
        { src: "guide-images/setup-wizard-step1-license.jpg", caption: "Setup Wizard Step 1 — Welcome & MIT License Agreement" },
        { src: "guide-images/setup-wizard-step2-location.jpg", caption: "Setup Wizard Step 2 — Destination Folder, Shortcuts & Windows PATH Integration" },
        { src: "guide-images/setup-wizard-step3-requirements.jpg", caption: "Setup Wizard Step 3 — Live Requirements Check (WebView2, VC++ x64, LocalDB) & Selectable Database/Web Engines" },
        { src: "guide-images/setup-wizard-repair-mode.jpg", caption: "Setup Wizard Maintenance / Repair Mode — repairs binaries, configs, ports & PATH while keeping data/ safe" },
        { src: "guide-images/versions-manager.jpg", caption: "Versions Manager — check newest builds, download portable engines, or import .zip anytime after setup" },
      ],
      steps: [
        "During Windows installation with <strong>DariusDB-Setup-0.1.6.exe</strong>, Step 3 automatically checks <strong>WebView2</strong>, <strong>Visual C++ 2015–2022 (x64)</strong>, and <strong>SQL Server LocalDB</strong>, letting you pre-install any database/web engines right away (or pick <strong>Lite Only (None)</strong> and install later).",
        "Open the <strong>Versions</strong> tab in the left sidebar (or press <kbd class=\"kbd\">Alt</kbd>+<kbd class=\"kbd\">4</kbd>).",
        "Pick the engine you want from the top tabs: <strong>Apache</strong>, <strong>PHP</strong>, <strong>MariaDB</strong>, <strong>MySQL</strong>, <strong>PostgreSQL</strong>, <strong>MS SQL Server</strong>, <strong>Redis</strong>, or <strong>Mailpit</strong>.",
        "Click <strong>↻ Check Newest Versions</strong> (<kbd class=\"kbd\">Alt</kbd>+<kbd class=\"kbd\">R</kbd>) to fetch the latest upstream releases.",
        "Click <strong>Download</strong> next to your preferred version, or click <strong>Import .zip</strong> (<kbd class=\"kbd\">Alt</kbd>+<kbd class=\"kbd\">I</kbd>) for an offline portable archive.",
        "To run multiple versions side by side (for example MariaDB 13.0 and 11.4, or PHP 8.2 / 8.3 / 8.4), install both and switch anytime — each major version uses its own <code>data/</code> folder so switching never corrupts data.",
      ],
      tip: "Removing an old engine version in Versions only deletes its binaries in bin/; your databases in data/ stay completely untouched.",
      actions: [
        { label: "Open Versions →", type: "tab", target: "versions", primary: true },
        { label: "Open bin/ Folder", type: "folder", target: "bin" },
      ],
    },
    {
      num: 2,
      id: "step-dashboard",
      badge: "Daily Use",
      title: "Start, Stop & Configure Auto-Run on Dashboard",
      summary: "Control all engines with one click, live ports/PIDs, and Auto-Run presets.",
      shortcut: "Alt+1",
      images: [
        { src: "guide-images/dashboard-overview.jpg", caption: "Services Dashboard — Start/Stop engines, switch versions, and monitor ports & PIDs" },
        { src: "guide-images/dashboard-auto-run.jpg", caption: "Auto-Run Bar — toggle auto-start on launch and pick per-service pills or presets" },
      ],
      steps: [
        "Go to <strong>Dashboard</strong> (<kbd class=\"kbd\">Alt</kbd>+<kbd class=\"kbd\">1</kbd>) to see every engine's status dot, active version dropdown, port, and PID.",
        "Click <strong>Start All</strong> (<kbd class=\"kbd\">F8</kbd>, <kbd class=\"kbd\">Ctrl</kbd>+<kbd class=\"kbd\">Shift</kbd>+<kbd class=\"kbd\">S</kbd>, or <kbd class=\"kbd\">Alt</kbd>+<kbd class=\"kbd\">Shift</kbd>+<kbd class=\"kbd\">S</kbd>) to launch all enabled engines, or <strong>Stop All</strong> (<kbd class=\"kbd\">F9</kbd>, <kbd class=\"kbd\">Ctrl</kbd>+<kbd class=\"kbd\">Shift</kbd>+<kbd class=\"kbd\">X</kbd>, or <kbd class=\"kbd\">Alt</kbd>+<kbd class=\"kbd\">Shift</kbd>+<kbd class=\"kbd\">X</kbd>) to turn everything off — use <kbd class=\"kbd\">F8</kbd>/<kbd class=\"kbd\">F9</kbd> or <kbd class=\"kbd\">Alt</kbd>+<kbd class=\"kbd\">Shift</kbd>+<kbd class=\"kbd\">X</kbd> if Windows/Edge intercepts <kbd class=\"kbd\">Ctrl</kbd>+<kbd class=\"kbd\">Shift</kbd>+<kbd class=\"kbd\">X</kbd>.",
        "For PHP/Laravel/WordPress apps, use the unified <strong>MySQL / MariaDB (Port 3306)</strong> selector — Darius DB automatically stops the conflicting engine before switching so port <code>3306</code> stays clean.",
        "Use the <strong>Auto-Run Bar</strong> at the top of Dashboard to toggle which services launch automatically whenever Darius DB opens (or pick quick presets like <em>MySQL + Apache</em> or <em>Match Running</em>).",
        "Click <strong>Hide</strong> (<kbd class=\"kbd\">Ctrl</kbd>+<kbd class=\"kbd\">Shift</kbd>+<kbd class=\"kbd\">H</kbd>) to minimize Darius DB to the Windows System Tray while services keep running.",
      ],
      tip: "On first start, Darius DB automatically generates clean default config files in etc/ and initializes each engine's data/ directory.",
      actions: [
        { label: "Open Dashboard →", type: "tab", target: "dashboard", primary: true },
      ],
    },
    {
      num: 3,
      id: "step-web",
      badge: "Web Stack",
      title: "Host Local Websites, PHP Apps & Mailpit Email Testing",
      summary: "Serve projects from www/ on localhost:8080 and catch local emails in Mailpit (:8025).",
      shortcut: "Alt+1",
      images: [
        { src: "guide-images/dashboard-services-stack.jpg", caption: "Unified Apache 2.4 + PHP 8.x + MariaDB/MySQL (:3306) Full-Stack Architecture" },
      ],
      steps: [
        "Start <strong>Apache Web Server</strong> (port <code>8080</code>) on the Dashboard — PHP 8.x runs integrated inside Apache 2.4.",
        "Place HTML or PHP project folders inside <code>www/</code>, or click <strong>Deploy .zip</strong> on the Apache row to deploy a zipped site directly into <code>www/</code>.",
        "Click <strong>Open Site</strong> on the Apache card to visit <code>http://localhost:8080/</code> in your browser.",
        "Scroll to the <strong>Full-Stack Architecture</strong> card on Dashboard and click <strong>📋 Copy PHP PDO Snippet</strong> for a ready-to-use, hardened <code>utf8mb4</code> prepared-statement PHP connection script.",
        "Start <strong>Mailpit</strong> to capture outgoing local emails via SMTP on port <code>1025</code> and view them in the Mailpit web inbox at <code>http://localhost:8025/</code>.",
      ],
      tip: "Switch between PHP 8.2, 8.3, and 8.4 directly from the PHP version dropdown on Dashboard — Apache reloads with the chosen PHP version.",
      actions: [
        { label: "Open Dashboard →", type: "tab", target: "dashboard", primary: true },
        { label: "Open www/ Folder", type: "folder", target: "www" },
      ],
    },
    {
      num: 4,
      id: "step-database",
      badge: "Core",
      title: "Browse Databases, Multi-Tab SQL, Row CRUD & Redis",
      summary: "Inspect tables, edit rows visually, run multi-tab SQL queries, and manage Redis keys.",
      shortcut: "Alt+2",
      images: [
        { src: "guide-images/database-manager.jpg", caption: "Database Browser — databases, tables, multi-tab SQL editor (Ctrl+Enter) & results" },
        { src: "guide-images/database-table-tree.jpg", caption: "Expandable Sidebar Table Tree — Columns, Keys & Relations (PK/FK), Indexes & Properties / DDL shortcut" },
        { src: "guide-images/database-structure-keys.jpg", caption: "Structure & Keys Inspector — Columns, Primary/Foreign Keys, Related Entities & Table Properties" },
        { src: "guide-images/database-structure-ddl.jpg", caption: "Foreign Keys (Outgoing & Incoming), Indexes & Table Definition (CREATE TABLE DDL)" },
        { src: "guide-images/database-context-menu.jpg", caption: "Right-Click Database Context Menu — Structure & Foreign Keys, New Database Diagram, Copy DDL, Create Table & Refresh" },
        { src: "guide-images/database-diagram-export.jpg", caption: "Interactive ER Database Diagram & Export Dropdown (PNG, JPEG, WebP, SVG, HTML, Mermaid)" },
        { src: "guide-images/database-floating-tabs.jpg", caption: "Chrome-Style Tear-Off Floating Query Windows — pop out, drag back (↩ Put Back) & Close All" },
        { src: "guide-images/database-sql-autocomplete.jpg", caption: "IDE-Style SQL Tab Autocomplete — CREATE, SHOW, SELECT, functions, tables & columns" },
        { src: "guide-images/database-show-row.jpg", caption: "Show Row Details — inspect all columns and copy JSON in one click" },
        { src: "guide-images/database-edit-row.jpg", caption: "Edit Row — update table records visually with NULL toggles" },
        { src: "guide-images/database-create-row.jpg", caption: "Create Row — insert new rows directly into any selected table (Alt+=)" },
      ],
      steps: [
        "Open <strong>Database</strong> (<kbd class=\"kbd\">Alt</kbd>+<kbd class=\"kbd\">2</kbd>) or click <strong>Browse</strong> on any running engine card.",
        "<strong>Create DB &amp; User:</strong> Click <strong>+ New</strong> (<kbd class=\"kbd\">Alt</kbd>+<kbd class=\"kbd\">D</kbd>) in the Databases pane to create a database and optionally create a dedicated user with full privileges in one step.",
        "<strong>Create &amp; Browse Tables:</strong> Click <strong>+ New</strong> (<kbd class=\"kbd\">Alt</kbd>+<kbd class=\"kbd\">T</kbd>) in the Tables pane to build a table visually, or click any existing table to preview its rows.",
        "<strong>Expandable Table Tree, Foreign Keys &amp; Structure Inspector:</strong> Click the <strong>▸</strong> arrow next to any table to expand <em>Columns</em>, <em>Keys &amp; Relations</em> (clickable FK &amp; incoming entity links), and <em>Indexes</em>; switch the results bar between <strong>📊 Rows</strong> and <strong>🔑 Structure &amp; Keys</strong> (or right-click a table → <em>Structure, Foreign Keys &amp; Properties</em> / <em>Copy CREATE TABLE DDL</em>).",
        "<strong>Interactive ER Database Diagram:</strong> Right-click any database or table → <strong>🗺️ New Database Diagram</strong> (or click <strong>🗺️ Diagram</strong>) for draggable table cards, live FK + dashed inferred <code>*_id</code> links, pan/zoom/Auto-Arrange/Fit, table picker, and <strong>📤 Export ▾</strong> (PNG, JPEG, WebP, SVG, HTML, Mermaid, or Clipboard PNG).",
        "<strong>Multi-Tab SQL, Autocomplete &amp; Floating Windows:</strong> Press <kbd class=\"kbd\">Tab</kbd> in the SQL editor for smart suggestions; press <kbd class=\"kbd\">Alt</kbd>+<kbd class=\"kbd\">N</kbd> for extra tabs, <kbd class=\"kbd\">Alt</kbd>+<kbd class=\"kbd\">[</kbd>/<kbd class=\"kbd\">]</kbd> to switch, or drag any tab out (or click <strong>↗</strong>) into a floating window and click <strong>↩ Put Back</strong> (or <strong>✕ Close All</strong>).",
        "<strong>Visual Row CRUD &amp; Pagination:</strong> Double-click any row to view details, or use <strong>+ Create</strong> (<kbd class=\"kbd\">Alt</kbd>+<kbd class=\"kbd\">=</kbd>), <strong>Show</strong>, <strong>Edit</strong>, and <strong>Delete</strong>. Filter rows with <kbd class=\"kbd\">Ctrl</kbd>+<kbd class=\"kbd\">F</kbd> and change pages with <kbd class=\"kbd\">Alt</kbd>+<kbd class=\"kbd\">←</kbd> / <kbd class=\"kbd\">Alt</kbd>+<kbd class=\"kbd\">→</kbd>.",
        "<strong>Redis Browser:</strong> Select Redis from the top engine tabs, choose <code>db0</code>–<code>db15</code>, filter keys (e.g. <code>session:*</code>), inspect JSON-formatted values &amp; TTL, and run commands in the bottom <code>redis&gt;</code> bar.",
      ],
      tip: "Drag the split handles between Databases/Tables, Sidebar/Main, and SQL Editor/Results to resize panes — double-click any split handle to reset.",
      actions: [
        { label: "Open Database Browser →", type: "tab", target: "database", primary: true },
      ],
    },
    {
      num: 5,
      id: "step-backup",
      badge: "Data Safety",
      title: "Import, Export, Full .zip Backup & .bak Restore",
      summary: "Move data cleanly using .sql, .csv, full .zip archives, and MS SQL .bak files.",
      shortcut: "Alt+I / Alt+E",
      images: [
        { src: "guide-images/database-import.jpg", caption: "Import .sql, .csv, or .bak directly into an existing or auto-created DB (Alt+I)" },
        { src: "guide-images/database-export.jpg", caption: "Export Entire DB, Current Table, or Selected Tables as .sql, .zip, or .csv (Alt+E)" },
        { src: "guide-images/database-backup-zip.jpg", caption: "Full .zip Backup — complete SQL dump + per-table CSVs + manifest.json (Alt+B)" },
        { src: "guide-images/database-restore-bak.jpg", caption: "Restore MS SQL .bak Backup with automatic logical file relocation into data/mssql/" },
      ],
      steps: [
        "<strong>Import (<kbd class=\"kbd\">Alt</kbd>+<kbd class=\"kbd\">I</kbd>):</strong> In the Database tab, click <strong>📥 Import</strong>, browse for a <code>.sql</code> dump, <code>.csv</code> spreadsheet (auto-creates columns from CSV headers), MS SQL <code>.bak</code>, or PostgreSQL <code>.backup</code> file, verify the target database name, and click <strong>Import Now</strong>.",
        "<strong>MS SQL .bak Restore:</strong> On MS SQL Server, click <strong>Restore .bak</strong> — Darius DB automatically runs <code>RESTORE FILELISTONLY</code> and relocates logical data/log files cleanly into <code>data/mssql/</code>.",
        "<strong>Export (<kbd class=\"kbd\">Alt</kbd>+<kbd class=\"kbd\">E</kbd>):</strong> Click <strong>📤 Export</strong> and choose <em>Entire Database</em>, <em>Current Table</em>, or <em>Selected Tables</em>. Pick <code>.sql</code> (toggle Structure and/or Data), <code>.zip</code>, or single-table <code>.csv</code>.",
        "<strong>Full .zip Backup (<kbd class=\"kbd\">Alt</kbd>+<kbd class=\"kbd\">B</kbd>):</strong> Click <strong>📦 Backup .zip</strong> to export a complete portable archive containing the full SQL dump, per-table CSV files, and a <code>manifest.json</code> with live progress tracking.",
      ],
      tip: "If the target database name does not exist yet during Import, Darius DB creates it automatically.",
      actions: [
        { label: "Open Database Browser →", type: "tab", target: "database", primary: true },
        { label: "Open backups/ Folder", type: "folder", target: "backups" },
      ],
    },
    {
      num: 6,
      id: "step-cloud",
      badge: "Cloud & Sync",
      title: "Cloud Databases & Daily 2:00 AM Auto-Sync / Backup",
      summary: "Connect remote hosts, sync Local ↔ Cloud, and automate daily 2:00 AM .zip backups with 7-day purge.",
      shortcut: "Alt+3",
      images: [
        { src: "guide-images/cloud-databases.jpg", caption: "Cloud Databases — provider presets, saved connections, Daily Auto-Sync & 2:00 AM Auto-Backup" },
        { src: "guide-images/cloud-add-database.jpg", caption: "Add Cloud DB — paste URI or ADO.NET string to auto-fill & Test Connection (Alt+N)" },
        { src: "guide-images/cloud-auto-sync-schedule.jpg", caption: "Daily Auto-Sync Schedule configuration (Alt+B)" },
        { src: "guide-images/cloud-daily-auto-backup.jpg", caption: "Daily Auto-Backup & Purge Schedule with Purge Date Guide and 7-day retention" },
        { src: "guide-images/cloud-sync-trace-logs.jpg", caption: "Sync & Auto-Schedule Trace Logs — filterable scheduler.log history" },
        { src: "guide-images/cloud-win-2am-task.jpg", caption: "Windows 2:00 AM Scheduled Task integration & 7-day automatic snapshot purge" },
        { src: "guide-images/cloud-backups-folder.jpg", caption: "backups/ Snapshot Folder — dated .zip archives organized per schedule" },
      ],
      steps: [
        "Open <strong>Cloud</strong> (<kbd class=\"kbd\">Alt</kbd>+<kbd class=\"kbd\">3</kbd>) and click a <strong>Quick Provider Preset</strong> (cPanel/Plesk/Hostinger, MonsterASP.NET/SmarterASP, Supabase/Neon, Aiven/TiDB/PlanetScale, AWS RDS/Azure, or Redis Cloud) or click <strong>+ Add Cloud DB</strong> (<kbd class=\"kbd\">Alt</kbd>+<kbd class=\"kbd\">N</kbd>).",
        "Paste a full connection URI (<code>mysql://...</code>, <code>postgresql://...</code>) or ADO.NET string (<code>Server=...;Database=...;</code>) into the Quick Auto-Fill box and click <strong>Parse</strong>.",
        "Click <strong>🔌 Test Connection</strong> to verify connectivity with live progress stages, then click <strong>Save Profile</strong>.",
        "Click <strong>Browse</strong> on any saved Cloud card to inspect live cloud tables, run SQL, edit rows, and export backups inside the Database tab.",
        "<strong>Manual Local ↔ Cloud Sync (<kbd class=\"kbd\">Alt</kbd>+<kbd class=\"kbd\">S</kbd>):</strong> One-time Push or Pull between Local and Cloud. When pulling <strong>Cloud → Local</strong>, optionally turn on <strong>✨ Auto-generate Local DB name</strong> (or click <strong>✨ Auto</strong>) to name the pulled DB with date, day, year &amp; timestamp (e.g. <code>alegria_2026_11_22_Sun_132136</code>).",
        "<strong>Separate Daily Auto-Sync (<kbd class=\"kbd\">Alt</kbd>+<kbd class=\"kbd\">B</kbd>) &amp; Daily Auto-Backup Modals:</strong> Manage <strong>Daily Auto-Sync</strong> (Local DB or Generated Folder) independently from <strong>Daily 2:00 AM Auto-Backup</strong> (.zip snapshots, Generated Folder &amp; automatic 7-day purge) in their own dedicated sections &amp; modals. Toggle <strong>🪟 Win 2 AM Task</strong> to register a Windows Task Scheduler wake-up job.",
      ],
      tip: "For cPanel Remote MySQL, whitelist your current public IP under cPanel → Remote MySQL® before testing the connection.",
      actions: [
        { label: "Open Cloud Databases →", type: "tab", target: "cloud", primary: true },
        { label: "Open backups/ Folder", type: "folder", target: "backups" },
      ],
    },
    {
      num: 7,
      id: "step-config-term",
      badge: "Power Tools",
      title: "Edit Config Files, View Live Logs & Use Built-in Terminal",
      summary: "Tune engine settings safely and run SQL / Shell / Redis commands without leaving the app.",
      shortcut: "Alt+5 / Alt+6",
      images: [
        { src: "guide-images/config-logs.jpg", caption: "Config & Logs — edit engine configs with Ctrl+S / Save & Restart and monitor logs (Alt+L)" },
        { src: "guide-images/terminal.jpg", caption: "Built-in Multi-Mode Terminal — switch between Shell, MariaDB, MySQL, psql, sqlcmd & Redis CLI" },
      ],
      steps: [
        "<strong>Config &amp; Logs (<kbd class=\"kbd\">Alt</kbd>+<kbd class=\"kbd\">5</kbd>):</strong> Pick an engine tab to edit <code>httpd.conf</code>, <code>my.ini</code>, <code>postgresql.conf</code>, <code>mssql.conf</code>, or <code>redis.conf</code>.",
        "Press <kbd class=\"kbd\">Ctrl</kbd>+<kbd class=\"kbd\">S</kbd> to save changes, or <kbd class=\"kbd\">Ctrl</kbd>+<kbd class=\"kbd\">Shift</kbd>+<kbd class=\"kbd\">Enter</kbd> to <strong>Save &amp; Restart</strong> immediately. Click <strong>Notepad</strong> if you prefer an external editor.",
        "Press <kbd class=\"kbd\">Alt</kbd>+<kbd class=\"kbd\">L</kbd> to toggle between <strong>Config File</strong> and <strong>Service Log</strong> (with 2-second auto-refresh enabled by default).",
        "<strong>Built-in Terminal (<kbd class=\"kbd\">Alt</kbd>+<kbd class=\"kbd\">6</kbd> or <kbd class=\"kbd\">Ctrl</kbd>+<kbd class=\"kbd\">`</kbd>):</strong> Switch between <em>Shell (CMD)</em>, <em>MariaDB</em>, <em>MySQL</em>, <em>PostgreSQL (psql)</em>, <em>MS SQL (sqlcmd)</em>, and <em>Redis CLI</em> using the top tabs or <kbd class=\"kbd\">Alt</kbd>+<kbd class=\"kbd\">[</kbd> / <kbd class=\"kbd\">Alt</kbd>+<kbd class=\"kbd\">]</kbd>.",
        "Use quick-command chips above the terminal, <kbd class=\"kbd\">↑</kbd> / <kbd class=\"kbd\">↓</kbd> for history, <kbd class=\"kbd\">Ctrl</kbd>+<kbd class=\"kbd\">L</kbd> to clear, and <kbd class=\"kbd\">Ctrl</kbd>+<kbd class=\"kbd\">C</kbd> to stop a running command.",
      ],
      tip: "PostgreSQL creates postgresql.conf inside data/postgresql-<major>/ during its first start (initdb) — start PostgreSQL once before editing it.",
      actions: [
        { label: "Open Config & Logs →", type: "tab", target: "config", primary: true },
        { label: "Open Terminal →", type: "tab", target: "terminal" },
      ],
    },
    {
      num: 8,
      id: "step-repair-update",
      badge: "Maintenance",
      title: "Self-Healing Repair, System Updater & Settings",
      summary: "Recover stuck ports/locks safely, update Darius DB in 1 click, and customize ports & themes.",
      shortcut: "Ctrl+Shift+R / U",
      images: [
        { src: "guide-images/repair-diagnostics.jpg", caption: "Self-Healing Repair & Diagnostics — fix configs, stale locks & ports safely (Ctrl+Shift+R / A)" },
        { src: "guide-images/system-version-updater.jpg", caption: "GitHub System Version Updater — check releases, view commits & update in-place (Ctrl+Shift+U)" },
        { src: "guide-images/settings.jpg", caption: "Settings — configure engine ports, credentials, theme, Windows boot & PATH (Alt+7)" },
      ],
      steps: [
        "<strong>🔧 Repair Single Service (<kbd class=\"kbd\">Ctrl</kbd>+<kbd class=\"kbd\">Shift</kbd>+<kbd class=\"kbd\">R</kbd>):</strong> Checks and fixes only the chosen service's binaries, configs, data directory, and stale PID locks without touching other engines.",
        "<strong>🛠️ Repair All (<kbd class=\"kbd\">Ctrl</kbd>+<kbd class=\"kbd\">Shift</kbd>+<kbd class=\"kbd\">A</kbd>):</strong> Full-system diagnostics across all portable directories, <code>settings.json</code>, stale PIDs/orphaned processes, blocked ports (optional auto-reassign), SQL/UI caches, MS SQL LocalDB, and Windows <code>PATH</code> — keeping <code>data/</code> and <code>www/</code> safe.",
        "<strong>⬆️ System Updater (<kbd class=\"kbd\">Ctrl</kbd>+<kbd class=\"kbd\">Shift</kbd>+<kbd class=\"kbd\">U</kbd>):</strong> Click <strong>↻ Check Online</strong> or <strong>🔄 Fetch Latest from GitHub</strong> to view recent commits and upgrade Darius DB binaries in-place with automatic restart.",
        "<strong>Settings (<kbd class=\"kbd\">Alt</kbd>+<kbd class=\"kbd\">7</kbd>):</strong> Change engine ports, admin usernames/passwords, theme (System / Dark / Light), Windows startup, and close-to-tray behavior, then press <kbd class=\"kbd\">Ctrl</kbd>+<kbd class=\"kbd\">S</kbd> to save.",
      ],
      tip: "If a port is already occupied by another program, open Settings to assign a free port (or enable Auto-reassign ports in Repair All).",
      actions: [
        { label: "Open Repair →", type: "btn", target: "btnOpenRepair" },
        { label: "Open System Updater →", type: "btn", target: "btnOpenSysUpdater" },
        { label: "Open Settings →", type: "tab", target: "settings", primary: true },
      ],
    },
    {
      num: 9,
      id: "step-ide-cli",
      badge: "Developers",
      title: "Connect Visual Studio, VS Code & Developer CLI (darius)",
      summary: "Copy ready-made connection snippets and control Darius DB from any IDE terminal.",
      shortcut: "Alt+7 / Alt+C",
      images: [
        { src: "guide-images/guide-cli-ports.jpg", caption: "Default Engine Ports, Mailpit (:8025/:1025) & One-Click Copy darius CLI Commands" },
        { src: "guide-images/guide-keyboard-shortcuts.jpg", caption: "Full Searchable Keyboard Shortcuts Reference (F1 / Ctrl+/ / Alt+8)" },
        { src: "guide-images/terminal.jpg", caption: "Use darius CLI & DB tools in Built-in Terminal, Visual Studio, VS Code, or PowerShell" },
      ],
      steps: [
        "In <strong>Database</strong> (<kbd class=\"kbd\">Alt</kbd>+<kbd class=\"kbd\">2</kbd>), click <strong>VS / .env</strong> to copy both Visual Studio (<code>appsettings.json</code>) and VS Code (<code>.env</code>) connection blocks, or press <kbd class=\"kbd\">Alt</kbd>+<kbd class=\"kbd\">C</kbd> to copy the raw Connection URI.",
        "Open <strong>Settings</strong> (<kbd class=\"kbd\">Alt</kbd>+<kbd class=\"kbd\">7</kbd>) and click <strong>Add CLI &amp; DB Tools to Windows PATH</strong> (one-time step, then restart your IDE terminal).",
        "Run <code>darius status</code>, <code>darius start all</code>, <code>darius env mssql MyApp</code>, <code>darius sql mariadb \"SHOW DATABASES;\"</code>, <code>darius restore</code>, <code>darius backup</code>, or <code>darius repair</code> directly from Visual Studio Package Manager Console, VS Code, PowerShell, or CMD.",
      ],
      tip: "Switch to the 'CLI & Quick Reference' tab above to copy any darius CLI or Launcher command in one click.",
      actions: [
        { label: "Open Settings →", type: "tab", target: "settings", primary: true },
        { label: "View CLI Commands →", type: "gmode", target: "cli" },
      ],
    },
  ];

  const DEFAULT_PORTS = [
    { engine: "Apache Web Server (+ PHP)", port: "8080", user: "www/ (DocumentRoot)", notes: "Browse at http://localhost:8080/ · Supports Drag & Drop / Deploy .zip" },
    { engine: "PHP (Standalone / Integrated)", port: "9000", user: "Integrated in Apache", notes: "Switch PHP 8.2 / 8.3 / 8.4 from Dashboard dropdown" },
    { engine: "MariaDB", port: "3306", user: "root (blank password)", notes: "Shares standard port 3306 cleanly with MySQL toggle" },
    { engine: "MySQL", port: "3306", user: "root (blank password)", notes: "Standard port 3306 for Laravel / PHP / Node / Prisma" },
    { engine: "PostgreSQL", port: "5432", user: "postgres (blank)", notes: "Initializes data/postgresql-<major>/ & postgresql.conf on first start" },
    { engine: "MS SQL Server / LocalDB", port: "1433", user: "Windows Auth", notes: "SQL Server 2025 / 2022 · Supports .bak restore & auto-sqlcmd" },
    { engine: "Redis", port: "6379", user: "No auth by default", notes: "Logical DBs db0–db15 · Built-in key scanner & redis> CLI" },
    { engine: "Mailpit (Email Catcher)", port: "8025 / 1025", user: "Web UI :8025 · SMTP :1025", notes: "Inspect local PHP / app emails at http://localhost:8025/" },
  ];

  const CLI_GROUPS = [
    {
      title: "Services & Status",
      items: [
        { cmd: "darius status", desc: "Show all engines, states, versions, ports & PIDs" },
        { cmd: "darius start all", desc: "Start all enabled services headlessly" },
        { cmd: "darius stop all", desc: "Gracefully stop all running services" },
        { cmd: "darius restart mariadb", desc: "Restart a specific engine (mariadb, mysql, postgresql, mssql, redis)" },
        { cmd: "darius gui", desc: "Open or focus the Darius DB desktop window" },
      ],
    },
    {
      title: "IDE Connection Snippets & Queries",
      items: [
        { cmd: "darius env mssql MyApp", desc: "Print Visual Studio (appsettings.json) & VS Code (.env) config" },
        { cmd: "darius uri mariadb my_app", desc: "Print raw connection URI (mysql://, postgresql://, etc.)" },
        { cmd: "darius dbs mariadb", desc: "List all databases on a running engine" },
        { cmd: "darius createdb mariadb shop_db shop_user secret123", desc: "Create a database and optional user with privileges" },
        { cmd: 'darius sql mariadb "SHOW TABLES;" -d shop_db', desc: "Run SQL / T-SQL / Redis command and print ASCII table" },
      ],
    },
    {
      title: "Backups, Versions, Repair & Launcher",
      items: [
        { cmd: "darius backup mariadb shop_db shop_backup.sql", desc: "Back up a database to .sql or .bak" },
        { cmd: "darius restore mssql backup.bak MyAppDB", desc: "Restore MS SQL .bak or import .sql script" },
        { cmd: "darius versions", desc: "List installed & downloadable portable engine versions" },
        { cmd: "darius repair all --fix-ports", desc: "Run self-healing diagnostics & auto-fix blocked ports" },
        { cmd: "darius path --add", desc: "Add darius CLI & active DB tools to Windows User PATH" },
        { cmd: "DariusDB-Launcher.exe --minimized", desc: "Launch Darius DB hidden in Windows System Tray" },
      ],
    },
  ];

  const PORTABLE_FOLDERS = [
    { kind: "root", name: "Root Folder", desc: "Portable app root containing executables & settings.json" },
    { kind: "www", name: "www/", desc: "Apache & PHP local website DocumentRoot (localhost:8080)" },
    { kind: "bin", name: "bin/", desc: "Downloaded portable engine binaries per version" },
    { kind: "data", name: "data/", desc: "Your database storage folders (preserved on upgrades & repairs)" },
    { kind: "etc", name: "etc/", desc: "Engine configuration files (httpd.conf, my.ini, redis.conf, ...)" },
    { kind: "logs", name: "logs/", desc: "Per-service runtime log files" },
    { kind: "backups", name: "backups/", desc: "Daily 2:00 AM Auto-Backup .zip snapshots (auto-purged after 7 days)" },
  ];

  const TROUBLESHOOTING = [
    {
      title: "Port already used by another program",
      fix: "Another database server (XAMPP, WAMP, Docker, or Windows service) is listening on that port. Open Settings (Alt+7) to change the port, or run Repair All (Ctrl+Shift+A) with 'Auto-reassign blocked ports' checked.",
    },
    {
      title: "cPanel Remote MySQL Error 1045 / Timeout",
      fix: "Log in to cPanel → Remote MySQL® and add your current public IP (or %). Make sure Host uses the server hostname/IP (port 3306, not cPanel's 2083) and Username/DB include your cPanel prefix.",
    },
    {
      title: "PostgreSQL postgresql.conf not found yet",
      fix: "PostgreSQL generates postgresql.conf inside data/postgresql-<major>/ during its first start (initdb). Start PostgreSQL once on Dashboard, then edit it in Config & Logs (Alt+5).",
    },
    {
      title: "Service refuses to start after sudden PC shutdown",
      fix: "A stale PID lock file (e.g. postmaster.pid) may be left behind. Press Ctrl+Shift+R (Repair Service) to clear stale crash locks safely while keeping data/ untouched.",
    },
    {
      title: "System Updater says repository not found / rate-limited",
      fix: "If updating from a private fork or hitting GitHub API limits, open System Updater (Ctrl+Shift+U) and paste a read-only GitHub Personal Access Token (stored locally in settings.json).",
    },
  ];

  // State local to shortcuts & guide module
  let host = null;
  let guideMode = "steps";
  let guideSearchQuery = "";
  let shortcutModalQuery = "";
  let lastFocusBeforeShortcuts = null;
  let checklistState = {};

  function loadChecklist() {
    try {
      const raw = localStorage.getItem(CHECKLIST_KEY);
      const parsed = raw ? JSON.parse(raw) : {};
      checklistState = parsed && typeof parsed === "object" ? parsed : {};
    } catch {
      checklistState = {};
    }
  }

  function saveChecklist() {
    try {
      localStorage.setItem(CHECKLIST_KEY, JSON.stringify(checklistState));
    } catch {
      // Ignore storage quota / disabled storage
    }
  }

  function allShortcutsFlat() {
    const out = [];
    for (const g of SHORTCUT_GROUPS) {
      for (const item of g.items) {
        out.push({ ...item, groupId: g.id, groupTitle: g.title, scope: g.scope, scopeLabel: g.scopeLabel });
      }
    }
    return out;
  }

  function formatKeySearchText(item) {
    const combos = (item.keys || []).map((c) => c.join("+")).join(" ");
    return `${combos} ${item.altHint || ""} ${item.label} ${item.groupTitle} ${item.scopeLabel}`.toLowerCase();
  }

  // ---- Shortcut Overlay Modal ----
  function renderShortcutsModal() {
    const grid = $("shortcutsModalGrid");
    if (!grid) return;
    const q = shortcutModalQuery.trim().toLowerCase();
    const activeTab = host?.getTab?.() || "dashboard";

    // Order current view's group right after Navigation & Global so relevant keys appear immediately
    const ordered = [...SHORTCUT_GROUPS].sort((a, b) => {
      const rank = (g) => {
        if (g.scope === activeTab) return 0;
        if (g.id === "navigation") return 1;
        if (g.id === "global-actions") return 2;
        return 3;
      };
      return rank(a) - rank(b);
    });

    const cards = [];
    for (const g of ordered) {
      const matched = g.items.filter((it) => {
        if (!q) return true;
        return formatKeySearchText({ ...it, groupTitle: g.title, scopeLabel: g.scopeLabel }).includes(q);
      });
      if (!matched.length) continue;
      const isCurrent = g.scope === activeTab;
      cards.push(`
        <section class="sc-card ${isCurrent ? "is-current" : ""}">
          <div class="sc-card-head">
            <h4>${esc(g.title)}</h4>
            <span class="pill ${isCurrent ? "running" : "accent"}">${esc(isCurrent ? "Current View" : g.scopeLabel)}</span>
          </div>
          <div class="sc-list">
            ${matched
          .map(
            (it) => `<div class="sc-row">
                  <span class="sc-label">${esc(it.label)}${it.altHint ? ` <span class="muted">(${esc(it.altHint)})</span>` : ""}</span>
                  <span class="sc-keys">${renderKeys(it.keys)}</span>
                </div>`
          )
          .join("")}
          </div>
        </section>
      `);
    }

    grid.innerHTML = cards.length
      ? cards.join("")
      : `<div class="empty-note" style="grid-column:1/-1">No keyboard shortcuts match "<strong>${esc(shortcutModalQuery)}</strong>".</div>`;
  }

  function openShortcutsModal() {
    const modal = $("modalShortcuts");
    if (!modal) return;
    lastFocusBeforeShortcuts = document.activeElement instanceof HTMLElement ? document.activeElement : null;
    shortcutModalQuery = "";
    const search = $("shortcutsModalSearch");
    if (search) search.value = "";
    renderShortcutsModal();
    modal.hidden = false;
    if (search) search.focus();
  }

  function closeShortcutsModal() {
    const modal = $("modalShortcuts");
    if (!modal || modal.hidden) return;
    modal.hidden = true;
    if (lastFocusBeforeShortcuts && typeof lastFocusBeforeShortcuts.focus === "function") {
      try {
        lastFocusBeforeShortcuts.focus();
      } catch {
        // Element may no longer be focusable
      }
    }
    lastFocusBeforeShortcuts = null;
  }

  function toggleShortcutsModal() {
    const modal = $("modalShortcuts");
    if (!modal) return;
    if (!modal.hidden) {
      closeShortcutsModal();
      return;
    }
    // Never stack on top of another open modal
    if (host?.isAnyOtherModalOpen?.("modalShortcuts")) return;
    openShortcutsModal();
  }

  function trapModalFocus(modalEl, e) {
    if (e.key !== "Tab") return;
    const focusables = Array.from(
      modalEl.querySelectorAll('button:not([disabled]):not([hidden]), input:not([disabled]):not([hidden]), select:not([disabled]), textarea:not([disabled]), a[href], [tabindex]:not([tabindex="-1"])')
    ).filter((el) => el.offsetParent !== null);
    if (!focusables.length) return;
    const first = focusables[0];
    const last = focusables[focusables.length - 1];
    if (e.shiftKey) {
      if (document.activeElement === first || !modalEl.contains(document.activeElement)) {
        e.preventDefault();
        last.focus();
      }
    } else if (document.activeElement === last || !modalEl.contains(document.activeElement)) {
      e.preventDefault();
      first.focus();
    }
  }

  // ---- Screenshot Lightbox Modal ----
  let lightboxState = { title: "", images: [], idx: 0, lastFocus: null };

  function renderLightbox() {
    const imgs = lightboxState.images || [];
    if (!imgs.length) return;
    const i = Math.max(0, Math.min(imgs.length - 1, lightboxState.idx));
    lightboxState.idx = i;
    const cur = imgs[i];
    if ($("lightboxTitle")) $("lightboxTitle").textContent = lightboxState.title || "Screenshot Preview";
    if ($("lightboxCounter")) $("lightboxCounter").textContent = `${i + 1} / ${imgs.length}`;
    if ($("lightboxImg")) {
      $("lightboxImg").src = cur.src;
      $("lightboxImg").alt = cur.caption || lightboxState.title || "Guide screenshot";
    }
    if ($("lightboxCaption")) $("lightboxCaption").textContent = cur.caption || "";
    const single = imgs.length <= 1;
    if ($("btnLightboxPrev")) $("btnLightboxPrev").disabled = single;
    if ($("btnLightboxNext")) $("btnLightboxNext").disabled = single;
  }

  function openLightbox(stepId, imgIdx = 0) {
    const step = GUIDE_STEPS.find((s) => s.id === stepId);
    const modal = $("modalGuideLightbox");
    if (!step || !step.images?.length || !modal) return;
    lightboxState = {
      title: `Step ${step.num}: ${step.title}`,
      images: step.images,
      idx: Number(imgIdx) || 0,
      lastFocus: document.activeElement instanceof HTMLElement ? document.activeElement : null,
    };
    renderLightbox();
    modal.hidden = false;
    $("btnCloseLightbox")?.focus();
  }

  function closeLightbox() {
    const modal = $("modalGuideLightbox");
    if (!modal || modal.hidden) return;
    modal.hidden = true;
    const prev = lightboxState.lastFocus;
    lightboxState.lastFocus = null;
    if (prev && typeof prev.focus === "function") {
      try {
        prev.focus();
      } catch {
        // Element may no longer be focusable
      }
    }
  }

  function stepLightbox(delta) {
    const n = (lightboxState.images || []).length;
    if (n < 2) return;
    lightboxState.idx = (lightboxState.idx + delta + n) % n;
    renderLightbox();
  }

  // ---- In-App Guide & Shortcuts View ----
  function renderQuickShortcutsStrip() {
    const chips = [
      { keys: [["F1"]], label: "Shortcuts Popup" },
      { keys: [["Alt", "1–8"]], label: "Switch Tabs" },
      { keys: [["F8"], ["Ctrl", "Shift", "S"]], label: "Start All" },
      { keys: [["F9"], ["Alt", "Shift", "X"]], label: "Stop All" },
      { keys: [["Ctrl", "Enter"]], label: "Run SQL" },
      { keys: [["Ctrl", "S"]], label: "Save Config/Settings" },
    ];
    return `
      <div class="card guide-shortcuts-strip">
        <div class="guide-strip-head">
          <div>
            <strong>⌨️ Essential Shortcut Keys</strong>
            <span class="muted"> · Works anywhere inside Darius DB</span>
          </div>
          <div style="display:flex;gap:6px;flex-wrap:wrap">
            <button type="button" class="btn xs accent" data-guide-act="gmode" data-guide-target="shortcuts">View All Shortcuts →</button>
            <button type="button" class="btn xs" data-guide-act="shortcuts-modal">Popup (F1)</button>
          </div>
        </div>
        <div class="guide-strip-chips">
          ${chips
        .map(
          (c) => `<div class="guide-key-chip">${renderKeys(c.keys)}<span>${esc(c.label)}</span></div>`
        )
        .join("")}
        </div>
      </div>
    `;
  }

  function renderChecklistCard() {
    const total = QUICKSTART_CHECKLIST.length;
    const doneCount = QUICKSTART_CHECKLIST.filter((c) => !!checklistState[c.id]).length;
    const pct = Math.round((doneCount / total) * 100);
    return `
      <div class="card guide-checklist-card">
        <div class="card-head">
          <div>
            <h2>🚀 Interactive Quick-Start Checklist</h2>
            <span class="muted">Follow these 6 milestones to get productive in under 2 minutes — progress saves automatically.</span>
          </div>
          <div class="guide-check-meta">
            <span class="pill ${doneCount === total ? "running" : "accent"}">${doneCount} / ${total} completed (${pct}%)</span>
            ${doneCount > 0 ? `<button type="button" class="btn xs ghost" data-guide-reset-check="1">Reset</button>` : ""}
          </div>
        </div>
        <div class="progress-bar" style="margin-bottom:12px" role="progressbar" aria-valuenow="${pct}" aria-valuemin="0" aria-valuemax="100" aria-label="Quick-start completion">
          <div class="progress-fill" style="width:${pct}%"></div>
        </div>
        <div class="guide-check-list">
          ${QUICKSTART_CHECKLIST.map((item) => {
      const checked = !!checklistState[item.id];
      return `<div class="guide-check-row ${checked ? "is-done" : ""}">
              <label class="guide-check-label">
                <input type="checkbox" data-guide-chk="${esc(item.id)}" ${checked ? "checked" : ""} />
                <span>${esc(item.label)}</span>
              </label>
              <button type="button" class="btn xs" data-guide-act="tab" data-guide-target="${esc(item.goTab)}">${esc(item.goLabel)} →</button>
            </div>`;
    }).join("")}
        </div>
      </div>
    `;
  }

  function renderStepImages(step) {
    const imgs = step.images || [];
    if (!imgs.length) return "";
    return `
      <div class="guide-media-grid ${imgs.length === 1 ? "single" : ""}">
        ${imgs
        .map(
          (img, idx) => `<figure class="guide-figure">
              <button type="button" class="guide-img-btn" data-guide-lb-step="${esc(step.id)}" data-guide-lb-idx="${idx}" title="Click to zoom screenshot">
                <img src="${esc(img.src)}" alt="${esc(img.caption)}" loading="lazy" decoding="async" />
                <span class="guide-zoom-pill" aria-hidden="true">🔍 Zoom</span>
              </button>
              <figcaption class="guide-figcaption">${esc(img.caption)}</figcaption>
            </figure>`
        )
        .join("")}
      </div>
    `;
  }

  function renderStepsTab(q) {
    const filtered = GUIDE_STEPS.filter((s) => {
      if (!q) return true;
      const imgCaps = (s.images || []).map((i) => i.caption).join(" ");
      const hay = `${s.title} ${s.summary} ${s.badge} ${s.steps.join(" ")} ${s.tip} ${imgCaps}`.toLowerCase();
      return hay.includes(q);
    });

    const topCards = q ? "" : `${renderQuickShortcutsStrip()}${renderChecklistCard()}`;
    const stepsHtml = filtered.length
      ? filtered
        .map(
          (s, idx) => `
          <details class="guide-step card" ${q || idx < 2 ? "open" : ""}>
            <summary class="guide-step-summary">
              <span class="guide-step-num">Step ${s.num}</span>
              <div class="guide-step-head-text">
                <div class="guide-step-title-row">
                  <h3>${esc(s.title)}</h3>
                  <span class="pill accent">${esc(s.badge)}</span>
                  ${s.images?.length ? `<span class="pill">🖼️ ${s.images.length} ${s.images.length === 1 ? "pic" : "pics"}</span>` : ""}
                </div>
                <p class="muted">${esc(s.summary)}</p>
              </div>
              <span class="guide-step-key"><kbd class="kbd">${esc(s.shortcut)}</kbd></span>
              <span class="guide-chevron" aria-hidden="true">▾</span>
            </summary>
            <div class="guide-step-body">
              ${renderStepImages(s)}
              <ol class="guide-ol">
                ${s.steps.map((line) => `<li>${line}</li>`).join("")}
              </ol>
              ${s.tip ? `<div class="guide-tip"><strong>💡 Tip:</strong> ${esc(s.tip)}</div>` : ""}
              <div class="guide-step-actions">
                ${s.actions
              .map(
                (a) =>
                  `<button type="button" class="btn sm ${a.primary ? "primary" : ""}" data-guide-act="${esc(a.type)}" data-guide-target="${esc(a.target)}">${esc(a.label)}</button>`
              )
              .join("")}
              </div>
            </div>
          </details>
        `
        )
        .join("")
      : `<div class="card empty-note">No guide steps match "<strong>${esc(guideSearchQuery)}</strong>". Try searching for <em>SQL</em>, <em>Cloud</em>, <em>Backup</em>, <em>Repair</em>, or <em>PHP</em>.</div>`;

    const controlsBar = filtered.length
      ? `<div class="guide-section-bar">
          <div>
            <strong>Visual Step-by-Step Guide (Darius DB Lite Version)</strong>
            <span class="muted"> · ${filtered.length} topic${filtered.length === 1 ? "" : "s"} · Click any picture to zoom</span>
          </div>
          <div style="display:flex;gap:6px">
            <button type="button" class="btn xs" data-guide-expand="all">Expand All</button>
            <button type="button" class="btn xs ghost" data-guide-expand="none">Collapse All</button>
          </div>
        </div>`
      : "";

    return `${topCards}${controlsBar}<div class="guide-steps-list">${stepsHtml}</div>`;
  }

  function renderShortcutsTab(q) {
    const groupsHtml = SHORTCUT_GROUPS.map((g) => {
      const items = g.items.filter((it) => {
        if (!q) return true;
        return formatKeySearchText({ ...it, groupTitle: g.title, scopeLabel: g.scopeLabel }).includes(q);
      });
      if (!items.length) return "";
      return `
        <section class="card sc-card">
          <div class="card-head">
            <h2>${esc(g.title)}</h2>
            <span class="pill accent">${esc(g.scopeLabel)}</span>
          </div>
          <div class="sc-list">
            ${items
          .map(
            (it) => `<div class="sc-row">
                  <span class="sc-label">${esc(it.label)}${it.altHint ? ` <span class="muted">(${esc(it.altHint)})</span>` : ""}</span>
                  <span class="sc-keys">${renderKeys(it.keys)}</span>
                </div>`
          )
          .join("")}
          </div>
        </section>
      `;
    })
      .filter(Boolean)
      .join("");

    const intro = `
      <div class="guide-tip-banner">
        <div>
          <strong>⌨️ Pro Tip:</strong> Press <kbd class="kbd">F1</kbd>, <kbd class="kbd">Ctrl</kbd>+<kbd class="kbd">/</kbd>, or <kbd class="kbd">?</kbd> from anywhere in Darius DB to open the quick Keyboard Shortcuts popup. Shortcuts never interfere while typing in SQL, Config, or Terminal fields, and <kbd class="kbd">Esc</kbd> closes any open modal.
        </div>
        <button type="button" class="btn sm accent" data-guide-act="shortcuts-modal">Open Popup (F1)</button>
      </div>
    `;

    return `${intro}<div class="guide-sc-grid">${groupsHtml ||
      `<div class="card empty-note" style="grid-column:1/-1">No shortcuts match "<strong>${esc(guideSearchQuery)}</strong>".</div>`
      }</div>`;
  }

  function renderCliTab(q) {
    const matchText = (t) => !q || String(t).toLowerCase().includes(q);

    const cliCards = CLI_GROUPS.map((g) => {
      const rows = g.items.filter((it) => matchText(`${g.title} ${it.cmd} ${it.desc}`));
      if (!rows.length) return "";
      return `
        <div class="card">
          <div class="card-head">
            <h2>${esc(g.title)}</h2>
            <span class="muted">Click Copy to paste into VS Code, Visual Studio, PowerShell, or CMD</span>
          </div>
          <div class="guide-cli-list">
            ${rows
          .map(
            (it) => `<div class="guide-cli-row">
                  <div class="guide-cli-info">
                    <code>${esc(it.cmd)}</code>
                    <span class="muted">${esc(it.desc)}</span>
                  </div>
                  <button type="button" class="btn xs" data-guide-copy="${esc(it.cmd)}" title="Copy command">Copy</button>
                </div>`
          )
          .join("")}
          </div>
        </div>
      `;
    })
      .filter(Boolean)
      .join("");

    const ports = DEFAULT_PORTS.filter((p) => matchText(`${p.engine} ${p.port} ${p.user} ${p.notes}`));
    const portsHtml = ports.length
      ? `<div class="card">
          <div class="card-head">
            <h2>🔌 Default Ports &amp; Credentials</h2>
            <button type="button" class="btn xs" data-guide-act="tab" data-guide-target="settings">Edit in Settings →</button>
          </div>
          <div class="eng-table-wrap">
            <table class="eng-table guide-port-table">
              <thead>
                <tr>
                  <th>Engine</th>
                  <th>Default Port</th>
                  <th>Default Login / Target</th>
                  <th>Notes</th>
                </tr>
              </thead>
              <tbody>
                ${ports
        .map(
          (p) => `<tr>
                      <td><strong>${esc(p.engine)}</strong></td>
                      <td><code>${esc(p.port)}</code></td>
                      <td>${esc(p.user)}</td>
                      <td class="muted">${esc(p.notes)}</td>
                    </tr>`
        )
        .join("")}
              </tbody>
            </table>
          </div>
        </div>`
      : "";

    const folders = PORTABLE_FOLDERS.filter((f) => matchText(`${f.name} ${f.desc}`));
    const foldersHtml = folders.length
      ? `<div class="card">
          <div class="card-head">
            <h2>📁 Portable Folder Structure</h2>
            <span class="muted">Everything lives inside one portable folder — move it anywhere.</span>
          </div>
          <div class="folder-list">
            ${folders
        .map(
          (f) => `<div class="folder-row">
                  <strong>${esc(f.name)}</strong>
                  <span class="muted" style="flex:1;min-width:0">${esc(f.desc)}</span>
                  <button type="button" class="btn xs" data-guide-act="folder" data-guide-target="${esc(f.kind)}">Open</button>
                </div>`
        )
        .join("")}
          </div>
        </div>`
      : "";

    const troubles = TROUBLESHOOTING.filter((t) => matchText(`${t.title} ${t.fix}`));
    const troubleHtml = troubles.length
      ? `<div class="card">
          <div class="card-head">
            <h2>🩺 Common Troubleshooting</h2>
            <button type="button" class="btn xs accent" data-guide-act="btn" data-guide-target="btnOpenRepair">🔧 Open Repair</button>
          </div>
          <div class="guide-trouble-grid">
            ${troubles
        .map(
          (t) => `<div class="guide-trouble-item">
                  <strong>${esc(t.title)}</strong>
                  <p class="muted">${esc(t.fix)}</p>
                </div>`
        )
        .join("")}
          </div>
        </div>`
      : "";

    const combined = `${portsHtml}${cliCards}${foldersHtml}${troubleHtml}`;
    return combined || `<div class="card empty-note">No reference items match "<strong>${esc(guideSearchQuery)}</strong>".</div>`;
  }

  function renderGuideView() {
    const body = $("guideContent");
    if (!body) return;
    const tabs = $("guideModeTabs");
    if (tabs) {
      tabs.querySelectorAll("[data-gmode]").forEach((b) =>
        b.classList.toggle("active", b.dataset.gmode === guideMode)
      );
    }
    const q = guideSearchQuery.trim().toLowerCase();
    if (guideMode === "shortcuts") {
      body.innerHTML = renderShortcutsTab(q);
    } else if (guideMode === "cli") {
      body.innerHTML = renderCliTab(q);
    } else {
      body.innerHTML = renderStepsTab(q);
    }
  }

  function setGuideMode(mode) {
    guideMode = mode || "steps";
    renderGuideView();
  }

  // ---- Global Keyboard Dispatcher ----
  const TAB_BY_DIGIT = {
    "1": "dashboard",
    "2": "database",
    "3": "cloud",
    "4": "versions",
    "5": "config",
    "6": "terminal",
    "7": "settings",
    "8": "guide",
  };

  function handleKeyDown(e) {
    if (!host) return;
    if (e.defaultPrevented) return;

    const modalShortcuts = $("modalShortcuts");
    const shortcutsOpen = !!(modalShortcuts && !modalShortcuts.hidden);
    const modalLightbox = $("modalGuideLightbox");
    const lightboxOpen = !!(modalLightbox && !modalLightbox.hidden);
    const key = e.key || "";
    const lower = key.toLowerCase();
    const code = e.code || "";
    const isKey = (ch) => lower === ch || code === "Key" + ch.toUpperCase();
    const consume = () => {
      e.preventDefault();
      e.stopPropagation();
    };
    const ctrlOrMeta = (e.ctrlKey || e.metaKey) && !e.altKey;
    const altOnly = e.altKey && !e.ctrlKey && !e.metaKey;
    const isAltGr = e.ctrlKey && e.altKey; // Windows European keyboards use Ctrl+Alt for AltGr

    if (isAltGr) return;

    // 0. Screenshot Lightbox modal takes priority when open
    if (lightboxOpen) {
      if (key === "Tab") {
        trapModalFocus(modalLightbox, e);
        return;
      }
      if (key === "Escape") {
        consume();
        closeLightbox();
        return;
      }
      if (key === "ArrowLeft") {
        consume();
        stepLightbox(-1);
        return;
      }
      if (key === "ArrowRight") {
        consume();
        stepLightbox(1);
        return;
      }
      if (key === "F5") consume();
      return;
    }

    // 1. Focus trap when Shortcuts modal is open
    if (shortcutsOpen && key === "Tab") {
      trapModalFocus(modalShortcuts, e);
      return;
    }

    // 2. Escape closes active topmost modal
    if (key === "Escape") {
      if (shortcutsOpen) {
        consume();
        closeShortcutsModal();
        return;
      }
      if (host.closeTopModal()) {
        consume();
      }
      return;
    }

    // 3. F1 or Ctrl+/ toggles Keyboard Shortcuts modal
    if (key === "F1" || (ctrlOrMeta && !e.shiftKey && (key === "/" || code === "Slash"))) {
      consume();
      toggleShortcutsModal();
      return;
    }

    // 4. Inside Shortcuts modal, allow Ctrl+F to focus its filter box; block all background shortcuts
    if (shortcutsOpen) {
      if (ctrlOrMeta && !e.shiftKey && isKey("f")) {
        consume();
        $("shortcutsModalSearch")?.focus();
      }
      return;
    }

    // 5. If any other modal is open, prevent F5 reload and don't trigger background shortcuts
    if (host.isAnyOtherModalOpen("modalShortcuts")) {
      if (key === "F5") consume();
      return;
    }

    const activeTab = host.getTab();
    const inText = isTextInputTarget(e.target);
    const inEditable = isEditableTarget(e.target);

    // 6. '?' outside text inputs opens Keyboard Shortcuts popup
    if (key === "?" && !e.ctrlKey && !e.metaKey && !e.altKey && !inEditable) {
      consume();
      openShortcutsModal();
      return;
    }

    // 7. F5 smart view refresh; F8 Start All; F9 Stop All (zero Windows/Edge accelerator collisions)
    if (!e.ctrlKey && !e.metaKey && !e.altKey && !e.shiftKey) {
      if (key === "F5") {
        consume();
        host.refreshCurrentView();
        return;
      }
      if (key === "F8") {
        consume();
        host.clickBtn("btnStartAll");
        return;
      }
      if (key === "F9") {
        consume();
        host.clickBtn("btnStopAll");
        return;
      }
    }

    // 8. Tab navigation: Alt+1..8 or Ctrl+1..8
    const digitTab = TAB_BY_DIGIT[key] || (code.startsWith("Digit") ? TAB_BY_DIGIT[code.slice(5)] : null);
    if ((altOnly || ctrlOrMeta) && !e.shiftKey && digitTab) {
      consume();
      host.switchTab(digitTab);
      return;
    }

    // 9. Ctrl+` (Backtick) jumps to Built-in Terminal
    if (ctrlOrMeta && !e.shiftKey && (key === "`" || code === "Backquote")) {
      consume();
      host.focusTerminal();
      return;
    }

    // 9b. Alt+Shift+S (Start All) & Alt+Shift+X (Stop All) — reliable fallback when Windows/Edge intercepts Ctrl+Shift+X
    if (altOnly && e.shiftKey) {
      if (isKey("s")) {
        consume();
        host.clickBtn("btnStartAll");
        return;
      }
      if (isKey("x")) {
        consume();
        host.clickBtn("btnStopAll");
        return;
      }
    }

    // 10. Global Ctrl+Shift+<Key> system controls (match both e.key and physical e.code for IME/layout reliability)
    if (ctrlOrMeta && e.shiftKey) {
      if (isKey("s")) {
        consume();
        host.clickBtn("btnStartAll");
        return;
      }
      if (isKey("x")) {
        consume();
        host.clickBtn("btnStopAll");
        return;
      }
      if (isKey("r")) {
        consume();
        host.clickBtn("btnOpenRepair");
        return;
      }
      if (isKey("a")) {
        consume();
        host.clickBtn("btnRepairAllSystem");
        return;
      }
      if (isKey("u")) {
        consume();
        host.clickBtn("btnOpenSysUpdater");
        return;
      }
      if (isKey("o")) {
        consume();
        host.clickBtn("btnSideRoot");
        return;
      }
      if (isKey("h")) {
        consume();
        host.clickBtn("btnSideTray");
        return;
      }
      if (isKey("k") && activeTab === "database") {
        consume();
        host.clickBtn("btnSqlClear");
        return;
      }
      if (key === "Enter") {
        if (activeTab === "database") {
          consume();
          host.clickBtn("btnSqlShowAll");
          return;
        }
        if (activeTab === "config") {
          consume();
          host.saveConfig(true);
          return;
        }
      }
    }

    // 11. Ctrl+Enter (Database: Run SQL Query anywhere in SQL view)
    if (ctrlOrMeta && !e.shiftKey && key === "Enter" && activeTab === "database") {
      const sqlVisible = $("sqlBrowser") && !$("sqlBrowser").hidden;
      if (sqlVisible) {
        consume();
        host.runSqlQuery();
        return;
      }
    }

    // 12. Ctrl+S (Config: Save file; Settings: Save settings)
    if (ctrlOrMeta && !e.shiftKey && isKey("s")) {
      if (activeTab === "config") {
        consume();
        host.saveConfig(false);
        return;
      }
      if (activeTab === "settings") {
        consume();
        host.saveSettings();
        return;
      }
    }

    // 13. Ctrl+L (Terminal: Clear output from anywhere on Terminal tab)
    if (ctrlOrMeta && !e.shiftKey && isKey("l") && activeTab === "terminal") {
      consume();
      host.clearTerminal();
      return;
    }

    // 14. Ctrl+F (Database filter/pattern; Guide search)
    if (ctrlOrMeta && !e.shiftKey && isKey("f")) {
      if (activeTab === "guide") {
        consume();
        host.focusGuideSearch();
        return;
      }
      if (activeTab === "database" && host.focusDbSearch()) {
        consume();
        return;
      }
    }

    // 15. Context-scoped Alt+<Key> shortcuts
    if (altOnly && !e.shiftKey) {
      if (activeTab === "database") {
        const sqlVisible = $("sqlBrowser") && !$("sqlBrowser").hidden;
        if (isKey("n") && sqlVisible) {
          consume();
          host.openNewSqlTab();
          return;
        }
        if (isKey("w") && sqlVisible) {
          consume();
          host.closeActiveSqlTab();
          return;
        }
        if ((key === "[" || code === "BracketLeft") && sqlVisible) {
          consume();
          host.cycleSqlTab(-1);
          return;
        }
        if ((key === "]" || code === "BracketRight") && sqlVisible) {
          consume();
          host.cycleSqlTab(1);
          return;
        }
        if (isKey("d") && sqlVisible) {
          consume();
          host.clickBtn("btnNewDb");
          return;
        }
        if (isKey("t") && sqlVisible) {
          consume();
          host.clickBtn("btnNewTable");
          return;
        }
        if ((key === "=" || key === "+" || code === "Equal") && sqlVisible) {
          consume();
          host.clickBtn("btnRowCreate");
          return;
        }
        if (isKey("i") && sqlVisible) {
          consume();
          host.clickBtn("btnOpenImport");
          return;
        }
        if (isKey("e") && sqlVisible) {
          consume();
          host.clickBtn("btnBackupSql");
          return;
        }
        if (isKey("b") && sqlVisible) {
          consume();
          host.clickBtn("btnBackupZip");
          return;
        }
        if (isKey("c")) {
          consume();
          host.clickBtn("btnCopyConn");
          return;
        }
        if (isKey("r")) {
          consume();
          host.clickBtn("btnDbRefresh");
          return;
        }
        if (key === "ArrowLeft" && !inText && sqlVisible) {
          consume();
          host.clickBtn("btnPagePrev");
          return;
        }
        if (key === "ArrowRight" && !inText && sqlVisible) {
          consume();
          host.clickBtn("btnPageNext");
          return;
        }
      } else if (activeTab === "cloud") {
        if (isKey("n")) {
          consume();
          host.clickBtn("btnAddCloud");
          return;
        }
        if (isKey("s")) {
          consume();
          host.clickBtn("btnOpenCloudSync");
          return;
        }
        if (isKey("b")) {
          consume();
          host.clickBtn("btnNewSchedule");
          return;
        }
      } else if (activeTab === "versions") {
        if (isKey("r")) {
          consume();
          host.clickBtn("btnRefreshCatalog");
          return;
        }
        if (isKey("i")) {
          consume();
          host.clickBtn("btnInstallZip");
          return;
        }
        if (isKey("m")) {
          consume();
          host.clickBtn("btnManualModal");
          return;
        }
      } else if (activeTab === "config") {
        if (isKey("l")) {
          consume();
          host.toggleConfigLogMode();
          return;
        }
        if (isKey("r")) {
          consume();
          host.refreshCurrentView();
          return;
        }
      } else if (activeTab === "terminal") {
        if (key === "[" || code === "BracketLeft") {
          consume();
          host.cycleTermMode(-1);
          return;
        }
        if (key === "]" || code === "BracketRight") {
          consume();
          host.cycleTermMode(1);
          return;
        }
      }
    }
  }

  function applyButtonHints() {
    for (const [id, tip] of Object.entries(BUTTON_SHORTCUT_HINTS)) {
      const el = $(id);
      if (el) el.title = tip;
    }
  }

  function wireUI() {
    // Dashboard top header Guides & Shortcuts buttons + Sidebar footer Shortcuts button
    if ($("btnDashGuides")) {
      $("btnDashGuides").onclick = () => {
        guideMode = "steps";
        host.switchTab("guide");
      };
    }
    if ($("btnDashShortcuts")) {
      $("btnDashShortcuts").onclick = openShortcutsModal;
    }
    if ($("btnGuideBackDash")) {
      $("btnGuideBackDash").onclick = () => host.switchTab("dashboard");
    }
    if ($("btnSideShortcuts")) {
      $("btnSideShortcuts").onclick = openShortcutsModal;
    }

    // Screenshot Lightbox controls
    if ($("btnCloseLightbox")) $("btnCloseLightbox").onclick = closeLightbox;
    if ($("btnDoneLightbox")) $("btnDoneLightbox").onclick = closeLightbox;
    if ($("btnLightboxPrev")) $("btnLightboxPrev").onclick = () => stepLightbox(-1);
    if ($("btnLightboxNext")) $("btnLightboxNext").onclick = () => stepLightbox(1);
    if ($("modalGuideLightbox")) {
      $("modalGuideLightbox").addEventListener("click", (e) => {
        if (e.target === $("modalGuideLightbox")) closeLightbox();
      });
    }

    // Shortcuts modal controls
    if ($("btnCloseShortcuts")) $("btnCloseShortcuts").onclick = closeShortcutsModal;
    if ($("btnDoneShortcuts")) $("btnDoneShortcuts").onclick = closeShortcutsModal;
    if ($("btnShortcutsOpenGuide")) {
      $("btnShortcutsOpenGuide").onclick = () => {
        closeShortcutsModal();
        guideMode = "steps";
        host.switchTab("guide");
      };
    }
    if ($("modalShortcuts")) {
      $("modalShortcuts").addEventListener("click", (e) => {
        if (e.target === $("modalShortcuts")) closeShortcutsModal();
      });
    }
    if ($("shortcutsModalSearch")) {
      $("shortcutsModalSearch").addEventListener("input", (e) => {
        shortcutModalQuery = e.target.value || "";
        renderShortcutsModal();
      });
    }

    // Guide view controls
    if ($("guideModeTabs")) {
      $("guideModeTabs").addEventListener("click", (e) => {
        const btn = e.target.closest("[data-gmode]");
        if (!btn) return;
        setGuideMode(btn.dataset.gmode);
      });
    }
    if ($("btnGuideOpenShortcuts")) {
      $("btnGuideOpenShortcuts").onclick = openShortcutsModal;
    }
    if ($("guideSearch")) {
      $("guideSearch").addEventListener("input", (e) => {
        guideSearchQuery = e.target.value || "";
        renderGuideView();
      });
    }
    if ($("guideContent")) {
      $("guideContent").addEventListener("change", (e) => {
        const chk = e.target.closest("[data-guide-chk]");
        if (!chk) return;
        checklistState[chk.dataset.guideChk] = !!chk.checked;
        saveChecklist();
        renderGuideView();
      });
      $("guideContent").addEventListener("click", async (e) => {
        const resetBtn = e.target.closest("[data-guide-reset-check]");
        if (resetBtn) {
          checklistState = {};
          saveChecklist();
          renderGuideView();
          host.toast("Quick-start checklist reset", "info");
          return;
        }
        const expBtn = e.target.closest("[data-guide-expand]");
        if (expBtn) {
          const openAll = expBtn.dataset.guideExpand === "all";
          $("guideContent")
            .querySelectorAll("details.guide-step")
            .forEach((d) => {
              d.open = openAll;
            });
          return;
        }
        const copyBtn = e.target.closest("[data-guide-copy]");
        if (copyBtn) {
          const cmd = copyBtn.dataset.guideCopy || "";
          try {
            await host.copyText(cmd);
            host.toast(`Copied: ${cmd}`, "ok");
          } catch (err) {
            host.toast(String(err), "err");
          }
          return;
        }
        const lbBtn = e.target.closest("[data-guide-lb-step]");
        if (lbBtn) {
          openLightbox(lbBtn.dataset.guideLbStep, Number(lbBtn.dataset.guideLbIdx) || 0);
          return;
        }
        const actBtn = e.target.closest("[data-guide-act]");
        if (!actBtn) return;
        const type = actBtn.dataset.guideAct;
        const target = actBtn.dataset.guideTarget;
        if (type === "tab" && target) {
          host.switchTab(target);
        } else if (type === "folder" && target) {
          host.openFolder(target);
        } else if (type === "btn" && target) {
          host.clickBtn(target);
        } else if (type === "gmode" && target) {
          setGuideMode(target);
        } else if (type === "shortcuts-modal") {
          openShortcutsModal();
        }
      });
    }
  }

  function init(hostAdapter) {
    host = hostAdapter;
    loadChecklist();
    applyButtonHints();
    wireUI();
    renderGuideView();
    document.addEventListener("keydown", handleKeyDown, true);
  }

  window.DariusShortcutsGuide = {
    SHORTCUT_GROUPS,
    GUIDE_STEPS,
    QUICKSTART_CHECKLIST,
    DEFAULT_PORTS,
    CLI_GROUPS,
    init,
    openShortcutsModal,
    closeShortcutsModal,
    toggleShortcutsModal,
    setGuideMode,
    onTabActivated(tab) {
      if (tab === "guide") renderGuideView();
    },
    _handleKeyDown: handleKeyDown,
    _allShortcutsFlat: allShortcutsFlat,
  };
})();
