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
  let confirmReturnFocus = null;
  function askConfirm(title, message, okLabel = "Confirm") {
    confirmReturnFocus = document.activeElement instanceof HTMLElement ? document.activeElement : null;
    $("confirmTitle").textContent = title;
    $("confirmMsg").textContent = message;
    $("btnConfirmOk").textContent = okLabel;
    $("modalConfirm").hidden = false;
    setTimeout(() => $("btnConfirmCancel")?.focus?.(), 0);
    return new Promise((resolve) => {
      confirmResolve = resolve;
    });
  }
  function closeConfirm(val) {
    $("modalConfirm").hidden = true;
    const ret = confirmReturnFocus;
    confirmReturnFocus = null;
    if (ret && ret.isConnected) {
      try { ret.focus(); } catch {}
    }
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
    } else if (tab === "guide") {
      window.DariusShortcutsGuide?.onTabActivated?.("guide");
    }
  }

  const activeSqlTabByEngine = new Map();
  let sqlTabIdSeq = 0;

  function engineTabs(engine = state.dbEngine) {
    return state.sqlTabs.filter((t) => t.engine === engine);
  }

  function dockedEngineTabs(engine = state.dbEngine) {
    return state.sqlTabs.filter((t) => t.engine === engine && !t.detached);
  }

  function getSqlTab(tabId) {
    return state.sqlTabs.find((t) => t.id === tabId) || null;
  }

  function getActiveSqlTab() {
    return getSqlTab(state.activeSqlTabId);
  }

  function isBlankSqlTab(tab) {
    return !!tab && tab.kind !== "diagram" && !tab.diagram && !tab.table && !tab.result && !tab.loading && !(tab.sql || "").trim();
  }

  function nextQueryTabTitle(engine) {
    const used = new Set(engineTabs(engine).map((t) => t.title));
    let n = 1;
    while (used.has(`Query ${n}`)) n++;
    return `Query ${n}`;
  }

  function createSqlTabObj({ engine = state.dbEngine, db = state.sqlDb, table = "", title = "", sql = "", kind = "query", diagram = null, pageSize = state.sqlPageSize || 50 } = {}) {
    const id = `sqltab-${++sqlTabIdSeq}`;
    return {
      id,
      kind: kind || "query",
      title: title || table || nextQueryTabTitle(engine),
      engine,
      db: db || "",
      table,
      columns: [],
      sql: sql || "",
      result: null,
      diagram: diagram || null,
      showingAll: false,
      page: 1,
      pageSize: pageSize ?? 50,
      filter: "",
      colWidths: null,
      metaText: "Ready",
      metaError: false,
      loading: false,
      reqSeq: 0,
      detached: false,
      _autoBlank: false,
      floatPos: null,
    };
  }

  function syncActiveTabFromDom() {
    const tab = getActiveSqlTab();
    if (!tab || tab.detached || tab.kind === "diagram") return;
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
    const tabs = dockedEngineTabs(state.dbEngine);
    const multiDb = new Set(tabs.map((t) => t.db).filter(Boolean)).size > 1;
    el.innerHTML = tabs
      .map((t) => {
        const active = t.id === state.activeSqlTabId;
        const label = t.title || t.table || "Query";
        const dbHint = multiDb && t.db ? ` (${t.db})` : "";
        const tip = `${label}${t.db ? ` — ${t.db}` : ""} (Drag outside to pop out · Drag sideways to reorder)`;
        return `<div class="sql-tab ${active ? "active" : ""}" data-sqltab-id="${esc(t.id)}" role="tab" aria-selected="${active ? "true" : "false"}" title="${esc(tip)}">
          ${t.loading ? `<span class="sql-tab-dot" title="Running query..."></span>` : ""}
          <span class="sql-tab-title">${esc(label)}${dbHint ? `<span class="sql-tab-db">${esc(dbHint)}</span>` : ""}</span>
          <button type="button" class="sql-tab-pop" data-pop-sqltab="${esc(t.id)}" title="Pop out query tab">↗</button>
          <button type="button" class="sql-tab-close" data-close-sqltab="${esc(t.id)}" title="Close tab">✕</button>
        </div>`;
      })
      .join("");
    const activeEl = el.querySelector(".sql-tab.active");
    if (activeEl && activeEl.scrollIntoView) {
      activeEl.scrollIntoView({ block: "nearest", inline: "nearest" });
    }
    const clearAllBtn = $("btnCloseAllSqlTabs");
    if (clearAllBtn) {
      const all = engineTabs(state.dbEngine);
      const hasContentOrMulti = all.length > 1 || all.some((t) => t.detached || !isBlankSqlTab(t));
      clearAllBtn.disabled = !hasContentOrMulti;
    }
  }

  function applyTabToDom(tab, { syncSidebar = false } = {}) {
    if (!tab) return;
    if (tab.detached) {
      updateFloatingSqlWin(tab);
      return;
    }
    state.activeSqlTabId = tab.id;
    activeSqlTabByEngine.set(tab.engine, tab.id);
    const isDiagram = tab.kind === "diagram";
    if ($("sqlDiagramStage")) $("sqlDiagramStage").hidden = !isDiagram;
    if ($("sqlEditorBox")) $("sqlEditorBox").hidden = isDiagram;
    if ($("sqlMainSplitH")) $("sqlMainSplitH").hidden = isDiagram;
    if ($("sqlResultBox")) $("sqlResultBox").hidden = isDiagram;

    if (isDiagram) {
      state.sqlTable = "";
      state.sqlColumns = [];
      state.sqlLastResult = null;
      updateSqlTableToolbar();
      renderSqlTabs();
      renderDatabaseDiagramInto($("sqlDiagramStage"), tab);
    } else {
      if ($("sqlDiagramStage")) $("sqlDiagramStage").innerHTML = "";
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
    }

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
    if (tab && tab.engine === state.dbEngine && !tab.detached) return tab;
    const savedId = activeSqlTabByEngine.get(state.dbEngine);
    const savedTab = savedId ? getSqlTab(savedId) : null;
    tab = (savedTab && !savedTab.detached ? savedTab : null) || dockedEngineTabs(state.dbEngine)[0] || null;
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
    const siblingsBefore = dockedEngineTabs(closing.engine);
    const sibIdx = siblingsBefore.findIndex((t) => t.id === tabId);
    state.sqlTabs.splice(idx, 1);
    if (closing.detached) {
      removeFloatingSqlWin(tabId);
      return;
    }

    if (closing.engine !== state.dbEngine) {
      if (activeSqlTabByEngine.get(closing.engine) === tabId) {
        const rem = dockedEngineTabs(closing.engine);
        if (rem.length) activeSqlTabByEngine.set(closing.engine, rem[Math.min(Math.max(0, sibIdx), rem.length - 1)].id);
        else activeSqlTabByEngine.delete(closing.engine);
      }
      return;
    }

    if (state.activeSqlTabId === tabId) {
      const remaining = dockedEngineTabs(state.dbEngine);
      if (remaining.length > 0) {
        applyTabToDom(remaining[Math.min(Math.max(0, sibIdx), remaining.length - 1)], { syncSidebar: true });
      } else {
        const fresh = createSqlTabObj({ engine: state.dbEngine, db: state.sqlDb });
        state.sqlTabs.push(fresh);
        applyTabToDom(fresh);
      }
    } else {
      renderSqlTabs();
    }
  }

  function closeAllSqlTabs() {
    const eng = state.dbEngine;
    const all = engineTabs(eng);
    if (!all.length) return;
    syncActiveTabFromDom();
    const hadAction = all.length > 1 || all.some((t) => t.detached || !isBlankSqlTab(t));
    all.forEach((t) => {
      if (t.detached) removeFloatingSqlWin(t.id);
    });
    state.sqlTabs = state.sqlTabs.filter((t) => t.engine !== eng);
    activeSqlTabByEngine.delete(eng);
    const fresh = createSqlTabObj({ engine: eng, db: state.sqlDb });
    state.sqlTabs.push(fresh);
    applyTabToDom(fresh);
    if ($("sqlEditor")) $("sqlEditor").focus();
    if (hadAction) toast(`Closed ${all.length} query tab(s)`, "ok");
  }

  function resetSqlBrowserState(nextDb = "") {
    const prevTab = getActiveSqlTab();
    if (prevTab && !prevTab.detached) {
      syncActiveTabFromDom();
      activeSqlTabByEngine.set(prevTab.engine, prevTab.id);
    }
    state.sqlDb = nextDb;
    state.sqlDbs = [];
    state.sqlTables = [];
    state.activeSqlTabId = "";
    const restoredId = activeSqlTabByEngine.get(state.dbEngine);
    const restoredTab = restoredId ? getSqlTab(restoredId) : null;
    let nextTab = (restoredTab && !restoredTab.detached ? restoredTab : null) || dockedEngineTabs(state.dbEngine)[0] || null;
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

  // ---- Chrome-Style Tear-Off / Floating & Re-Dockable Query Windows ----
  const floatingSqlWins = new Map();
  let floatSqlZSeq = 1;
  let suppressNextSqlTabClick = false;

  function focusFloatingSqlWin(winEl) {
    if (!winEl) return;
    document.querySelectorAll(".sql-float-win.is-focused").forEach((el) => el.classList.remove("is-focused"));
    winEl.classList.add("is-focused");
    winEl.style.zIndex = String(++floatSqlZSeq);
  }

  function removeFloatingSqlWin(tabId) {
    const winEl = floatingSqlWins.get(tabId);
    if (winEl) {
      winEl.remove();
      floatingSqlWins.delete(tabId);
    }
  }

  function clearTabBarDropPreview() {
    const bar = $("sqlTabBar");
    if (bar) bar.classList.remove("is-drop-target");
    if ($("sqlTabDropHint")) $("sqlTabDropHint").hidden = true;
    $("sqlTabList")?.querySelectorAll(".sql-tab.is-drop-before").forEach((el) => el.classList.remove("is-drop-before"));
  }

  function findTabBarInsertBeforeId(clientX) {
    const list = $("sqlTabList");
    if (!list) return null;
    const els = Array.from(list.querySelectorAll(".sql-tab"));
    for (const el of els) {
      const r = el.getBoundingClientRect();
      if (clientX < r.left + r.width / 2) {
        return el.dataset.sqltabId || null;
      }
    }
    return null;
  }

  function isPointerOverSqlTabBar(clientX, clientY) {
    const bar = $("sqlTabBar");
    if (!bar || $("tab-database")?.hidden || !bar.offsetParent) return false;
    const r = bar.getBoundingClientRect();
    return clientX >= r.left - 12 && clientX <= r.right + 12 && clientY >= r.top - 20 && clientY <= r.bottom + 24;
  }

  function syncFloatingWinToTab(tab, winEl) {
    if (!tab || !winEl) return;
    const ed = winEl.querySelector(".sql-float-editor");
    const filt = winEl.querySelector(".sql-float-filter");
    if (ed) tab.sql = ed.value;
    if (filt) tab.filter = filt.value;
    if (!winEl.classList.contains("is-maximized")) {
      const r = winEl.getBoundingClientRect();
      if (r.width > 0 && r.height > 0) {
        tab.floatPos = { left: Math.round(r.left), top: Math.round(r.top), width: Math.round(r.width), height: Math.round(r.height) };
      }
    }
  }

  function updateFloatingSqlWin(tab) {
    const winEl = floatingSqlWins.get(tab?.id);
    if (!winEl) return;
    const dot = winEl.querySelector(".sql-float-dot");
    if (dot) dot.hidden = !tab.loading;
    const labelEl = winEl.querySelector(".sql-float-title");
    if (labelEl) labelEl.textContent = tab.title || tab.table || "Query";
    const dbEl = winEl.querySelector(".sql-float-db");
    if (dbEl) dbEl.textContent = tab.db ? `${tab.engine} / ${tab.db}` : tab.engine;

    if (tab.kind === "diagram") {
      const diagWrap = winEl.querySelector(".sql-float-diagram");
      if (diagWrap) renderDatabaseDiagramInto(diagWrap, tab);
      return;
    }

    const ed = winEl.querySelector(".sql-float-editor");
    if (ed && document.activeElement !== ed) ed.value = tab.sql || "";
    const filt = winEl.querySelector(".sql-float-filter");
    if (filt) {
      filt.hidden = !(tab.result && tab.result.isQuery);
      if (document.activeElement !== filt) filt.value = tab.filter || "";
    }

    const meta = winEl.querySelector(".sql-float-meta");
    if (meta) {
      meta.textContent = tab.metaText || "Ready";
      meta.parentElement.className = tab.metaError ? "result-meta-bar error" : "result-meta-bar";
    }

    const resWrap = winEl.querySelector(".sql-float-results");
    const pagerBar = winEl.querySelector(".sql-float-pager");
    if (tab.result && tab.result.isQuery && resWrap) {
      const stats = renderSqlTableInto(resWrap, tab.result, {
        filterText: tab.filter || "",
        page: tab.page || 1,
        pageSize: tab.pageSize !== undefined ? tab.pageSize : 50,
        dbName: tab.db || "",
        tableName: "",
        colWidths: null,
      });
      tab.page = stats.page;
      if (pagerBar) {
        if (stats.total > 0 || tab.result.rows.length > 0) {
          pagerBar.hidden = false;
          pagerBar.querySelector(".sql-float-pinfo").textContent =
            stats.total === 0 ? "0 match" : `${stats.startIdx + 1}–${stats.endIdx} of ${stats.total} · Page ${stats.page}/${stats.totalPages}`;
          pagerBar.querySelector("[data-f-prev]").disabled = stats.page <= 1;
          pagerBar.querySelector("[data-f-next]").disabled = stats.page >= stats.totalPages;
        } else {
          pagerBar.hidden = true;
        }
      }
    } else {
      if (resWrap) resWrap.innerHTML = "";
      if (pagerBar) pagerBar.hidden = true;
    }
  }

  function startFloatingWinDrag(winEl, tab, startClientX, startClientY) {
    focusFloatingSqlWin(winEl);
    if (winEl.classList.contains("is-maximized")) return;
    const rect = winEl.getBoundingClientRect();
    const offsetX = Math.max(16, Math.min(rect.width - 60, startClientX - rect.left));
    const offsetY = Math.max(8, Math.min(36, startClientY - rect.top));
    let pendingInsertBeforeId = null;
    let overTabBar = false;

    const onMove = (ev) => {
      const maxL = Math.max(8, window.innerWidth - rect.width - 8);
      const maxT = Math.max(8, window.innerHeight - rect.height - 8);
      const left = Math.max(8, Math.min(maxL, ev.clientX - offsetX));
      const top = Math.max(8, Math.min(maxT, ev.clientY - offsetY));
      winEl.style.left = `${left}px`;
      winEl.style.top = `${top}px`;

      overTabBar = isPointerOverSqlTabBar(ev.clientX, ev.clientY);
      clearTabBarDropPreview();
      if (overTabBar) {
        winEl.classList.add("is-over-tabbar");
        $("sqlTabBar")?.classList.add("is-drop-target");
        if ($("sqlTabDropHint")) $("sqlTabDropHint").hidden = false;
        pendingInsertBeforeId = findTabBarInsertBeforeId(ev.clientX);
        if (pendingInsertBeforeId) {
          const targetEl = $("sqlTabList")?.querySelector(`[data-sqltab-id="${pendingInsertBeforeId}"]`);
          targetEl?.classList.add("is-drop-before");
        }
      } else {
        winEl.classList.remove("is-over-tabbar");
        pendingInsertBeforeId = null;
      }
    };

    const onUp = () => {
      window.removeEventListener("pointermove", onMove);
      window.removeEventListener("pointerup", onUp);
      window.removeEventListener("pointercancel", onUp);
      winEl.classList.remove("is-over-tabbar");
      clearTabBarDropPreview();
      if (overTabBar) {
        dockSqlTab(tab.id, pendingInsertBeforeId);
      } else {
        syncFloatingWinToTab(tab, winEl);
      }
    };

    window.addEventListener("pointermove", onMove);
    window.addEventListener("pointerup", onUp);
    window.addEventListener("pointercancel", onUp);
  }

  function getCaretCoordsInTextarea(ta, pos) {
    const div = document.createElement("div");
    const cs = getComputedStyle(ta);
    const copyProps = [
      "boxSizing", "width", "paddingTop", "paddingRight", "paddingBottom", "paddingLeft",
      "borderTopWidth", "borderLeftWidth", "fontStyle", "fontVariant", "fontWeight",
      "fontStretch", "fontSize", "lineHeight", "fontFamily", "letterSpacing", "tabSize",
    ];
    div.style.position = "absolute";
    div.style.visibility = "hidden";
    div.style.whiteSpace = "pre-wrap";
    div.style.wordWrap = "break-word";
    copyProps.forEach((p) => { div.style[p] = cs[p]; });
    div.textContent = ta.value.slice(0, pos);
    const marker = document.createElement("span");
    marker.textContent = ta.value.slice(pos) || ".";
    div.appendChild(marker);
    document.body.appendChild(div);
    const top = marker.offsetTop - ta.scrollTop;
    const left = marker.offsetLeft - ta.scrollLeft;
    document.body.removeChild(div);
    return { top, left };
  }

  function getSqlSuggestContext(text, cursorPos) {
    const before = text.slice(0, cursorPos);
    const tokMatch = before.match(/([A-Za-z0-9_`".*]*)$/);
    const rawTok = tokMatch ? tokMatch[1] : "";
    const replaceStart = cursorPos - rawTok.length;
    const beforeTok = before.slice(0, replaceStart);
    const prefix = rawTok.replace(/^[`"]+/, "");
    const dotIdx = prefix.lastIndexOf(".");
    const tableQualifier = dotIdx >= 0 ? prefix.slice(0, dotIdx).replace(/[`"]/g, "") : "";
    const colPrefix = dotIdx >= 0 ? prefix.slice(dotIdx + 1) : prefix;
    const lastSemi = before.lastIndexOf(";");
    const stmtRaw = before.slice(lastSemi + 1);
    const stmtTrimLeft = stmtRaw.replace(/^\s+/, "");
    const stmtStartPos = (lastSemi + 1) + (stmtRaw.length - stmtTrimLeft.length);
    const isLeadCommandPhrase = /^(create|show|alter|drop|insert|update|delete|describe|explain|truncate)\s+[A-Za-z0-9_]*$/i.test(stmtTrimLeft);

    const prevStmtChunk = beforeTok.split(";").pop() || "";
    const prevTrim = prevStmtChunk.trimEnd();
    const prevUpper = prevTrim.toUpperCase();
    const atStatementStart = prevTrim === "" || /;\s*$/.test(prevStmtChunk);
    const expectTable = /\b(FROM|JOIN|INTO|UPDATE|TABLE|DESCRIBE|DESC|TRUNCATE|SHOW\s+CREATE\s+TABLE|SHOW\s+COLUMNS\s+FROM|SHOW\s+INDEX\s+FROM)\s*$/i.test(prevTrim);
    const expectDb = /\b(USE|DATABASE|SCHEMA)\s*$/i.test(prevTrim);
    const expectColumn = /\b(SELECT|WHERE|AND|OR|SET|ORDER\s+BY|GROUP\s+BY|HAVING|ON)\s*$/i.test(prevTrim) || /,\s*$/.test(prevTrim) || dotIdx >= 0;
    const effectivePrefix = isLeadCommandPhrase ? stmtTrimLeft : colPrefix;
    const effectiveStart = isLeadCommandPhrase ? stmtStartPos : (dotIdx >= 0 ? replaceStart + dotIdx + 1 : replaceStart);
    return {
      rawTok,
      prefix,
      colPrefix: effectivePrefix,
      tableQualifier,
      replaceStart: effectiveStart,
      replaceEnd: cursorPos,
      atStatementStart: atStatementStart || isLeadCommandPhrase,
      isLeadCommandPhrase,
      expectTable: isLeadCommandPhrase ? false : expectTable,
      expectDb: isLeadCommandPhrase ? false : expectDb,
      expectColumn: isLeadCommandPhrase ? false : expectColumn,
      prevUpper,
    };
  }

  function buildSqlSuggestions(ctx, tab = null) {
    const rawEng = tab?.engine || state.dbEngine || "mysql";
    const engine = rawEng.startsWith("cloud:")
      ? ((state.cloudProfiles || []).find((p) => "cloud:" + p.id === rawEng)?.engine || "mysql")
      : rawEng;
    const tabDb = tab?.db || state.sqlDb || "";
    const cachedTbls = (tabDb && sqlTablesCache.get(`${rawEng}|${tabDb}`)?.tables) || (tabDb === state.sqlDb ? state.sqlTables : []) || [];
    const tabCols = (tab?.columns?.length ? tab.columns : state.sqlColumns) || [];
    const sampleTbl = tab?.table || state.sqlTable || cachedTbls[0]?.name || "table_name";
    const sampleDb = tabDb || "my_database";
    const qTbl = engine === "mysql" || engine === "mariadb" ? "`" + sampleTbl + "`" : sampleTbl;
    const autoIncCol = engine === "postgresql"
      ? "id SERIAL PRIMARY KEY"
      : engine === "mssql"
        ? "id INT IDENTITY(1,1) PRIMARY KEY"
        : "id INT AUTO_INCREMENT PRIMARY KEY";

    const templates = [
      {
        kind: "tmpl",
        label: "CREATE TABLE ...",
        meta: "Template",
        insert: `CREATE TABLE new_table (\n  ${autoIncCol},\n  name VARCHAR(120) NOT NULL,\n  created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP\n);`,
        cursorToken: "new_table",
      },
      { kind: "tmpl", label: "CREATE DATABASE ...", meta: "Template", insert: `CREATE DATABASE ${sampleDb}_new;`, cursorToken: `${sampleDb}_new` },
      { kind: "tmpl", label: "CREATE INDEX ...", meta: "Template", insert: `CREATE INDEX idx_${sampleTbl}_col ON ${qTbl} (column_name);`, cursorToken: "column_name" },
      { kind: "tmpl", label: "CREATE VIEW ...", meta: "Template", insert: `CREATE VIEW v_${sampleTbl} AS\nSELECT * FROM ${qTbl};`, cursorToken: `v_${sampleTbl}` },
      {
        kind: "tmpl",
        label: "SHOW TABLES;",
        meta: "Template",
        insert: engine === "postgresql"
          ? "SELECT table_schema, table_name, table_type FROM information_schema.tables WHERE table_schema NOT IN ('pg_catalog','information_schema') ORDER BY table_name;"
          : engine === "mssql"
            ? "SELECT s.name AS schema_name, o.name AS table_name FROM sys.objects o JOIN sys.schemas s ON o.schema_id = s.schema_id WHERE o.type = 'U' ORDER BY s.name, o.name;"
            : "SHOW TABLES;",
      },
      {
        kind: "tmpl",
        label: "SHOW DATABASES;",
        meta: "Template",
        insert: engine === "postgresql"
          ? "SELECT datname FROM pg_database WHERE NOT datistemplate ORDER BY datname;"
          : engine === "mssql"
            ? "SELECT name FROM sys.databases ORDER BY name;"
            : "SHOW DATABASES;",
      },
      {
        kind: "tmpl",
        label: `SHOW CREATE TABLE ${sampleTbl};`,
        meta: "Template",
        insert: engine === "mysql" || engine === "mariadb"
          ? `SHOW CREATE TABLE ${qTbl};`
          : `SELECT column_name, data_type, is_nullable FROM information_schema.columns WHERE table_name = '${sampleTbl}';`,
      },
      {
        kind: "tmpl",
        label: `SHOW COLUMNS FROM ${sampleTbl};`,
        meta: "Template",
        insert: engine === "mysql" || engine === "mariadb"
          ? `SHOW COLUMNS FROM ${qTbl};`
          : `SELECT column_name, data_type, is_nullable FROM information_schema.columns WHERE table_name = '${sampleTbl}';`,
      },
      { kind: "tmpl", label: "SHOW STATUS / VARIABLES;", meta: "Template", insert: "SHOW VARIABLES LIKE '%timeout%';", cursorToken: "%timeout%" },
      {
        kind: "tmpl",
        label: `SELECT * FROM ${sampleTbl}`,
        meta: "Template",
        insert: engine === "mssql" ? `SELECT TOP 100 * FROM ${qTbl};` : `SELECT * FROM ${qTbl} LIMIT 100;`,
      },
      { kind: "tmpl", label: `SELECT COUNT(*) FROM ${sampleTbl};`, meta: "Template", insert: `SELECT COUNT(*) AS total FROM ${qTbl};` },
      { kind: "tmpl", label: `INSERT INTO ${sampleTbl} ...`, meta: "Template", insert: `INSERT INTO ${qTbl} (col1, col2) VALUES ('val1', 'val2');`, cursorToken: "col1" },
      { kind: "tmpl", label: `UPDATE ${sampleTbl} SET ...`, meta: "Template", insert: `UPDATE ${qTbl} SET col1 = 'value' WHERE id = 1;`, cursorToken: "col1" },
      { kind: "tmpl", label: `DELETE FROM ${sampleTbl} WHERE ...`, meta: "Template", insert: `DELETE FROM ${qTbl} WHERE id = 1;`, cursorToken: "id = 1" },
      { kind: "tmpl", label: `ALTER TABLE ${sampleTbl} ADD ...`, meta: "Template", insert: `ALTER TABLE ${qTbl} ADD COLUMN new_col VARCHAR(120);`, cursorToken: "new_col" },
      { kind: "tmpl", label: `DESCRIBE ${sampleTbl};`, meta: "Template", insert: `DESCRIBE ${qTbl};` },
      { kind: "tmpl", label: `EXPLAIN SELECT ...`, meta: "Template", insert: `EXPLAIN SELECT * FROM ${qTbl} LIMIT 100;` },
      { kind: "tmpl", label: `DROP TABLE IF EXISTS ${sampleTbl};`, meta: "Template", insert: `DROP TABLE IF EXISTS ${qTbl};` },
      { kind: "tmpl", label: `TRUNCATE TABLE ${sampleTbl};`, meta: "Template", insert: `TRUNCATE TABLE ${qTbl};` },
    ];

    const tables = cachedTbls.map((t) => ({
      kind: "tbl",
      label: t.name,
      meta: /view/i.test(t.kind || "") ? "View" : "Table",
      insert: t.name,
    }));

    const cols = tabCols.map((c) => ({
      kind: "col",
      label: c.name,
      meta: c.dataType || "Column",
      insert: c.name,
    }));

    const dbs = ((sqlDbsCache.get(rawEng)?.dbs) || state.sqlDbs || []).map((d) => ({
      kind: "tbl",
      label: d,
      meta: "Database",
      insert: d,
    }));

    const keywords = [
      "SELECT", "CREATE", "SHOW", "INSERT", "UPDATE", "DELETE", "ALTER", "DROP", "DESCRIBE", "EXPLAIN",
      "FROM", "WHERE", "AND", "OR", "NOT", "IN", "LIKE", "BETWEEN", "IS NULL", "IS NOT NULL",
      "ORDER BY", "GROUP BY", "HAVING", "LIMIT", "OFFSET", "JOIN", "INNER JOIN", "LEFT JOIN", "RIGHT JOIN",
      "ON", "AS", "DISTINCT", "VALUES", "SET", "INTO", "TABLE", "DATABASE", "INDEX", "VIEW", "COLUMN",
      "PRIMARY KEY", "FOREIGN KEY", "REFERENCES", "AUTO_INCREMENT", "NOT NULL", "DEFAULT",
      "COUNT(*)", "SUM()", "AVG()", "MIN()", "MAX()", "COALESCE()", "NOW()", "CURRENT_TIMESTAMP",
      "VARCHAR(255)", "INT", "BIGINT", "TEXT", "BOOLEAN", "DECIMAL(10,2)", "DATETIME", "TIMESTAMP",
    ].map((kw) => ({ kind: "kw", label: kw, meta: "SQL", insert: kw }));

    const q = (ctx.colPrefix || "").toLowerCase();
    let pool = [];
    if (ctx.tableQualifier && cols.length) {
      pool = [...cols];
    } else if (ctx.expectTable) {
      pool = [...tables, ...dbs];
    } else if (ctx.expectDb) {
      pool = [...dbs];
    } else if (ctx.expectColumn) {
      pool = [...cols, ...tables, ...keywords];
    } else if (ctx.atStatementStart) {
      pool = [...templates, ...keywords, ...tables];
    } else {
      pool = [...keywords, ...tables, ...cols, ...templates];
    }

    const scored = [];
    const seen = new Set();
    for (const item of pool) {
      const key = item.kind + ":" + item.label.toLowerCase();
      if (seen.has(key)) continue;
      const lblLower = item.label.toLowerCase();
      let score = 0;
      if (!q) {
        score = item.kind === "tmpl" ? 90 : item.kind === "tbl" ? 85 : item.kind === "col" ? 80 : 60;
      } else if (lblLower === q) {
        score = 200;
      } else if (lblLower.startsWith(q)) {
        score = 150 + (item.kind === "tmpl" && ctx.atStatementStart ? 20 : item.kind === "tbl" && ctx.expectTable ? 25 : 0);
      } else if (lblLower.includes(q)) {
        score = 80;
      } else {
        continue;
      }
      seen.add(key);
      scored.push({ ...item, score });
    }
    scored.sort((a, b) => b.score - a.score || a.label.localeCompare(b.label));
    return scored.slice(0, 12);
  }

  // Wires IDE-style Tab autocomplete & snippet suggestions to any SQL editor textarea/menu pair.
  function attachSqlAutocomplete({ textarea, menu, stage, boundsEl = null, getTab = () => null, onRun = null }) {
    const st = { open: false, items: [], activeIdx: 0, replaceStart: 0, replaceEnd: 0, prefix: "" };

    const close = () => {
      st.open = false;
      st.items = [];
      st.activeIdx = 0;
      if (menu) menu.hidden = true;
    };

    const renderMenu = () => {
      if (!menu || !textarea) return;
      if (!st.open || !st.items.length) {
        menu.hidden = true;
        return;
      }
      const coords = getCaretCoordsInTextarea(textarea, st.replaceEnd);
      const stageW = stage?.clientWidth || textarea.clientWidth || 500;
      const boxH = boundsEl?.clientHeight || stage?.clientHeight || textarea.clientHeight || 200;
      const maxLeft = Math.max(8, stageW - 320);
      const leftPx = Math.max(8, Math.min(maxLeft, coords.left + 8));
      let topPx = coords.top + 22;
      if (topPx + 190 > boxH && coords.top > 140) {
        topPx = Math.max(6, coords.top - 185);
      }
      menu.style.left = `${leftPx}px`;
      menu.style.top = `${Math.max(6, topPx)}px`;

      const q = (st.prefix || "").toLowerCase();
      const highlightMatch = (lbl) => {
        if (!q) return esc(lbl);
        const idx = lbl.toLowerCase().indexOf(q);
        if (idx < 0) return esc(lbl);
        return esc(lbl.slice(0, idx)) + `<mark>${esc(lbl.slice(idx, idx + q.length))}</mark>` + esc(lbl.slice(idx + q.length));
      };

      menu.innerHTML =
        `<div class="sql-suggest-list">` +
        st.items
          .map((item, idx) => {
            const active = idx === st.activeIdx;
            const kindLabel = item.kind === "tmpl" ? "Snippet" : item.kind === "tbl" ? "Table" : item.kind === "col" ? "Col" : "SQL";
            return `<button type="button" class="sql-suggest-item${active ? " active" : ""}" role="option" aria-selected="${active}" data-sg-idx="${idx}">
              <span class="sql-suggest-left">
                <span class="sql-suggest-kind ${esc(item.kind)}">${kindLabel}</span>
                <span class="sql-suggest-label">${highlightMatch(item.label)}</span>
              </span>
              <span class="sql-suggest-meta">${active ? `<span class="sql-suggest-tab-key">Tab ⇥</span>` : esc(item.meta || "")}</span>
            </button>`;
          })
          .join("") +
        `</div>` +
        `<div class="sql-suggest-foot"><span>↑↓ choose · <strong>Tab</strong> or click to insert</span><span>Esc close</span></div>`;
      menu.hidden = false;
      const activeEl = menu.querySelector(".sql-suggest-item.active");
      if (activeEl) activeEl.scrollIntoView({ block: "nearest" });
    };

    const open = (forceShow = false) => {
      if (!textarea) return;
      const pos = textarea.selectionStart ?? 0;
      const ctx = getSqlSuggestContext(textarea.value, pos);
      if (!forceShow && !ctx.colPrefix && !ctx.expectTable && !ctx.expectDb && !ctx.tableQualifier) {
        close();
        return;
      }
      const items = buildSqlSuggestions(ctx, getTab());
      if (!items.length) {
        close();
        return;
      }
      st.open = true;
      st.items = items;
      st.activeIdx = 0;
      st.replaceStart = ctx.replaceStart;
      st.replaceEnd = ctx.replaceEnd;
      st.prefix = ctx.colPrefix;
      renderMenu();
    };

    const apply = (idx = st.activeIdx) => {
      const item = st.items[idx];
      if (!textarea || !item) return false;
      const val = textarea.value;
      const start = st.replaceStart;
      const end = st.replaceEnd;
      let insertText = item.insert || item.label;
      const needTrailingSpace = item.kind === "kw" && !insertText.endsWith(")") && !insertText.endsWith(";");
      if (needTrailingSpace && (val[end] === undefined || !/\s/.test(val[end]))) {
        insertText += " ";
      }
      textarea.value = val.slice(0, start) + insertText + val.slice(end);
      let nextPos = start + insertText.length;
      if (item.cursorToken) {
        const tokIdx = insertText.indexOf(item.cursorToken);
        if (tokIdx >= 0) {
          textarea.focus();
          textarea.setSelectionRange(start + tokIdx, start + tokIdx + item.cursorToken.length);
          const tab = getTab();
          if (tab) tab.sql = textarea.value;
          close();
          return true;
        }
      }
      if (insertText.endsWith("()")) nextPos -= 1;
      textarea.focus();
      textarea.setSelectionRange(nextPos, nextPos);
      const tab = getTab();
      if (tab) tab.sql = textarea.value;
      close();
      return true;
    };

    if (menu) {
      menu.addEventListener("mousedown", (e) => {
        e.preventDefault();
        const btn = e.target.closest("[data-sg-idx]");
        if (btn) apply(Number(btn.dataset.sgIdx));
      });
    }
    if (textarea) {
      textarea.addEventListener("input", () => open(false));
      textarea.addEventListener("blur", () => setTimeout(() => close(), 120));
      textarea.addEventListener("keydown", (e) => {
        if ((e.ctrlKey || e.metaKey) && e.key === "Enter") {
          e.preventDefault();
          close();
          if (onRun) onRun();
          return;
        }
        if ((e.ctrlKey || e.metaKey) && e.code === "Space") {
          e.preventDefault();
          open(true);
          return;
        }
        if (e.key === "Escape" && st.open) {
          e.preventDefault();
          close();
          return;
        }
        if (st.open && (e.key === "ArrowDown" || e.key === "ArrowUp")) {
          e.preventDefault();
          const len = st.items.length;
          if (len > 0) {
            st.activeIdx = e.key === "ArrowDown" ? (st.activeIdx + 1) % len : (st.activeIdx - 1 + len) % len;
            renderMenu();
          }
          return;
        }
        if (e.key === "Tab" && !e.ctrlKey && !e.metaKey && !e.altKey) {
          if (st.open && st.items.length) {
            e.preventDefault();
            apply(st.activeIdx);
            return;
          }
          if (!e.shiftKey && textarea.selectionStart === textarea.selectionEnd) {
            const ctx = getSqlSuggestContext(textarea.value, textarea.selectionStart);
            if (ctx.colPrefix) {
              const items = buildSqlSuggestions(ctx, getTab());
              if (items.length === 1) {
                e.preventDefault();
                st.items = items;
                st.replaceStart = ctx.replaceStart;
                st.replaceEnd = ctx.replaceEnd;
                st.prefix = ctx.colPrefix;
                apply(0);
                return;
              }
              if (items.length > 1) {
                e.preventDefault();
                open(true);
                return;
              }
            }
            e.preventDefault();
            const pos = textarea.selectionStart;
            textarea.value = textarea.value.slice(0, pos) + "  " + textarea.value.slice(pos);
            textarea.setSelectionRange(pos + 2, pos + 2);
            const tab = getTab();
            if (tab) tab.sql = textarea.value;
          }
        }
      });
    }
    return { close, open };
  }

  function mountFloatingSqlWin(tab) {
    const layer = $("sqlFloatingWindows");
    if (!layer || !tab) return null;
    removeFloatingSqlWin(tab.id);

    const isDiagram = tab.kind === "diagram";
    const pos = tab.floatPos || {};
    const w = pos.width || Math.min(isDiagram ? 860 : 640, Math.max(400, Math.round(window.innerWidth * (isDiagram ? 0.68 : 0.54))));
    const h = pos.height || Math.min(isDiagram ? 620 : 460, Math.max(320, Math.round(window.innerHeight * (isDiagram ? 0.74 : 0.6))));
    const cascade = floatingSqlWins.size * 24;
    const left = Math.max(8, Math.min(window.innerWidth - w - 8, pos.left ?? Math.round((window.innerWidth - w) * 0.65 + cascade)));
    const top = Math.max(8, Math.min(window.innerHeight - h - 8, pos.top ?? Math.round(84 + cascade)));

    const winEl = document.createElement("div");
    winEl.className = "sql-float-win";
    winEl.dataset.floatTabId = tab.id;
    winEl.style.left = `${left}px`;
    winEl.style.top = `${top}px`;
    winEl.style.width = `${w}px`;
    winEl.style.height = `${h}px`;
    winEl.innerHTML = `
      <div class="sql-float-head" title="Drag back onto the SQL tab bar above to put this tab back">
        <span class="sql-float-tabpill">
          <span class="sql-tab-dot sql-float-dot" ${tab.loading ? "" : "hidden"}></span>
          <span class="sql-float-title">${esc(tab.title || tab.table || "Query")}</span>
        </span>
        <span class="chip sql-float-db">${esc(tab.db ? `${tab.engine} / ${tab.db}` : tab.engine)}</span>
        <div class="spacer"></div>
        <div class="sql-float-head-acts">
          <button type="button" class="btn xs accent" data-f-dock title="Put this tab back into the main tab bar">↩ Put Back</button>
          <button type="button" class="icon-btn" data-f-max title="Maximize / Restore">⬜</button>
          <button type="button" class="icon-btn" data-f-close title="Close tab">✕</button>
        </div>
      </div>
      ${
        isDiagram
          ? `<div class="sql-float-body"><div class="sql-diagram-stage sql-float-diagram" style="flex:1;min-height:0"></div></div>`
          : `
      <div class="sql-float-bar">
        <span class="hint">Tab to suggest · Ctrl+Enter to run · Drag titlebar back to dock</span>
        <div class="spacer"></div>
        <button type="button" class="btn xs" data-f-all title="Run without SQL LIMIT">Show All</button>
        <button type="button" class="btn xs" data-f-clear>Clear</button>
        <button type="button" class="btn xs primary" data-f-run>Run Query</button>
      </div>
      <div class="sql-float-body">
        <div class="sql-float-editor-wrap">
          <textarea class="sql-float-editor" spellcheck="false" placeholder="SELECT 1; -- Write SQL here (press Tab for CREATE, SHOW, SELECT suggestions)..."></textarea>
          <div class="sql-suggest-menu" role="listbox" aria-label="SQL autocomplete suggestions" hidden></div>
        </div>
        <div class="sql-float-split" title="Drag to resize editor / results"></div>
        <div class="sql-float-result-wrap">
          <div class="result-meta-bar">
            <div class="result-meta-text sql-float-meta">Ready</div>
            <div class="spacer"></div>
            <input type="search" class="input xs sql-float-filter" placeholder="Filter rows..." style="width:150px" hidden />
          </div>
          <div class="table-wrap sql-float-results"></div>
          <div class="result-pager-bar sql-float-pager" hidden>
            <span class="pager-info sql-float-pinfo">0</span>
            <div class="spacer"></div>
            <button type="button" class="btn xs pager-btn" data-f-prev>‹ Prev</button>
            <button type="button" class="btn xs pager-btn" data-f-next>Next ›</button>
          </div>
        </div>
      </div>`
      }
      <div class="sql-float-resize-se" title="Drag to resize window"></div>
    `;

    let floatSuggest = null;
    if (!isDiagram) {
      const ed = winEl.querySelector(".sql-float-editor");
      const edWrap = winEl.querySelector(".sql-float-editor-wrap");
      ed.value = tab.sql || "";
      ed.addEventListener("input", () => {
        tab.sql = ed.value;
      });
      floatSuggest = attachSqlAutocomplete({
        textarea: ed,
        menu: winEl.querySelector(".sql-suggest-menu"),
        stage: edWrap,
        boundsEl: winEl.querySelector(".sql-float-body"),
        getTab: () => tab,
        onRun: () => {
          tab.sql = ed.value;
          runSqlQuery(false, false, tab.id);
        },
      });

      const filt = winEl.querySelector(".sql-float-filter");
      let filtTimer = null;
      filt.addEventListener("input", () => {
        clearTimeout(filtTimer);
        filtTimer = setTimeout(() => {
          tab.filter = filt.value;
          tab.page = 1;
          updateFloatingSqlWin(tab);
        }, 120);
      });

      winEl.querySelector("[data-f-run]").onclick = () => {
        floatSuggest.close();
        tab.sql = ed.value;
        runSqlQuery(false, false, tab.id);
      };
      winEl.querySelector("[data-f-all]").onclick = () => {
        floatSuggest.close();
        tab.sql = applySqlQueryLimit(ed.value, 0);
        ed.value = tab.sql;
        runSqlQuery(true, false, tab.id);
      };
      winEl.querySelector("[data-f-clear]").onclick = () => {
        floatSuggest.close();
        ed.value = "";
        tab.sql = "";
        ed.focus();
      };
      winEl.querySelector("[data-f-prev]").onclick = () => {
        tab.page = Math.max(1, (tab.page || 1) - 1);
        updateFloatingSqlWin(tab);
      };
      winEl.querySelector("[data-f-next]").onclick = () => {
        tab.page = (tab.page || 1) + 1;
        updateFloatingSqlWin(tab);
      };

      // Horizontal splitter inside floating window
      const splitEl = winEl.querySelector(".sql-float-split");
      splitEl.addEventListener("pointerdown", (e) => {
        if (e.button !== 0) return;
        e.preventDefault();
        const startY = e.clientY;
        const startH = edWrap.getBoundingClientRect().height;
        const maxH = Math.max(90, winEl.getBoundingClientRect().height - 165);
        const onMv = (ev) => {
          const nextH = Math.max(64, Math.min(maxH, startH + (ev.clientY - startY)));
          edWrap.style.height = `${nextH}px`;
        };
        const onUp = () => {
          window.removeEventListener("pointermove", onMv);
          window.removeEventListener("pointerup", onUp);
        };
        window.addEventListener("pointermove", onMv);
        window.addEventListener("pointerup", onUp);
      });
    }

    winEl.addEventListener("pointerdown", () => focusFloatingSqlWin(winEl));

    const head = winEl.querySelector(".sql-float-head");
    head.addEventListener("pointerdown", (e) => {
      if (e.button !== 0 || e.target.closest("button")) return;
      e.preventDefault();
      floatSuggest?.close();
      startFloatingWinDrag(winEl, tab, e.clientX, e.clientY);
    });
    head.addEventListener("dblclick", (e) => {
      if (e.target.closest("button")) return;
      winEl.classList.toggle("is-maximized");
    });

    winEl.querySelector("[data-f-dock]").onclick = () => {
      floatSuggest?.close();
      dockSqlTab(tab.id, null);
    };
    winEl.querySelector("[data-f-max]").onclick = () => winEl.classList.toggle("is-maximized");
    winEl.querySelector("[data-f-close]").onclick = () => closeSqlTab(tab.id);

    // Corner resize handle
    const resizer = winEl.querySelector(".sql-float-resize-se");
    resizer.addEventListener("pointerdown", (e) => {
      if (e.button !== 0 || winEl.classList.contains("is-maximized")) return;
      e.preventDefault();
      e.stopPropagation();
      const r = winEl.getBoundingClientRect();
      const sx = e.clientX;
      const sy = e.clientY;
      const onMv = (ev) => {
        const nw = Math.max(340, Math.min(window.innerWidth - r.left - 8, r.width + (ev.clientX - sx)));
        const nh = Math.max(260, Math.min(window.innerHeight - r.top - 8, r.height + (ev.clientY - sy)));
        winEl.style.width = `${nw}px`;
        winEl.style.height = `${nh}px`;
      };
      const onUp = () => {
        window.removeEventListener("pointermove", onMv);
        window.removeEventListener("pointerup", onUp);
        syncFloatingWinToTab(tab, winEl);
      };
      window.addEventListener("pointermove", onMv);
      window.addEventListener("pointerup", onUp);
    });

    layer.appendChild(winEl);
    floatingSqlWins.set(tab.id, winEl);
    focusFloatingSqlWin(winEl);
    updateFloatingSqlWin(tab);
    return winEl;
  }

  function detachSqlTab(tabId, opts = {}) {
    const tab = getSqlTab(tabId);
    if (!tab) return null;
    if (tab.id === state.activeSqlTabId) {
      syncActiveTabFromDom();
    }
    if (tab.detached && floatingSqlWins.has(tab.id)) {
      const existingWin = floatingSqlWins.get(tab.id);
      focusFloatingSqlWin(existingWin);
      return existingWin;
    }

    const w = Math.min(640, Math.max(400, Math.round(window.innerWidth * 0.54)));
    const h = Math.min(460, Math.max(320, Math.round(window.innerHeight * 0.6)));
    if (opts.clientX !== undefined && opts.clientY !== undefined) {
      tab.floatPos = {
        left: Math.max(8, Math.min(window.innerWidth - w - 8, Math.round(opts.clientX - Math.min(130, w * 0.25)))),
        top: Math.max(8, Math.min(window.innerHeight - h - 8, Math.round(opts.clientY - 18))),
        width: w,
        height: h,
      };
    }

    tab.detached = true;
    tab._autoBlank = false;

    const remaining = dockedEngineTabs(tab.engine);
    if (tab.engine === state.dbEngine && state.activeSqlTabId === tab.id) {
      if (remaining.length > 0) {
        applyTabToDom(remaining[0], { syncSidebar: true });
      } else {
        const fresh = createSqlTabObj({ engine: tab.engine, db: state.sqlDb });
        fresh._autoBlank = true;
        state.sqlTabs.push(fresh);
        applyTabToDom(fresh);
      }
    } else {
      if (remaining.length === 0) {
        const fresh = createSqlTabObj({ engine: tab.engine, db: tab.db });
        fresh._autoBlank = true;
        state.sqlTabs.push(fresh);
      }
      renderSqlTabs();
    }

    return mountFloatingSqlWin(tab);
  }

  function dockSqlTab(tabId, insertBeforeId = null) {
    const tab = getSqlTab(tabId);
    if (!tab) return;
    const winEl = floatingSqlWins.get(tab.id);
    if (winEl) syncFloatingWinToTab(tab, winEl);

    // Drop any untouched auto-created placeholder tab if the user simply puts the detached tab back
    const autoBlanks = state.sqlTabs.filter(
      (t) => t.id !== tab.id && t.engine === tab.engine && !t.detached && t._autoBlank && isBlankSqlTab(t)
    );
    if (autoBlanks.length > 0) {
      const rmIds = new Set(autoBlanks.map((t) => t.id));
      state.sqlTabs = state.sqlTabs.filter((t) => !rmIds.has(t.id));
      if (rmIds.has(state.activeSqlTabId)) state.activeSqlTabId = "";
    }

    tab.detached = false;
    tab._autoBlank = false;

    if (insertBeforeId && insertBeforeId !== tab.id) {
      const fromIdx = state.sqlTabs.findIndex((t) => t.id === tab.id);
      if (fromIdx >= 0) {
        state.sqlTabs.splice(fromIdx, 1);
        const toIdx = state.sqlTabs.findIndex((t) => t.id === insertBeforeId);
        if (toIdx >= 0) state.sqlTabs.splice(toIdx, 0, tab);
        else state.sqlTabs.push(tab);
      }
    }

    removeFloatingSqlWin(tab.id);
    if (state.tab !== "database") switchTab("database");

    if (tab.engine !== state.dbEngine) {
      activeSqlTabByEngine.set(tab.engine, tab.id);
      state.dbEngine = tab.engine;
      resetSqlBrowserState(tab.db || "");
      renderDbEngineTabs();
      loadDbView();
    } else {
      applyTabToDom(tab, { syncSidebar: true });
    }
  }

  // ---- SSMS-Style Right-Click Context Menu & Interactive ER Database Diagram ----
  function hideDbContextMenu() {
    const menu = $("dbContextMenu");
    if (!menu || menu.hidden) return;
    menu.hidden = true;
    menu.innerHTML = "";
  }

  function showDbContextMenu(clientX, clientY, headerText, items) {
    const menu = $("dbContextMenu");
    if (!menu) return;
    hideDbContextMenu();
    if (headerText) {
      const head = document.createElement("div");
      head.className = "db-ctx-head";
      head.textContent = headerText;
      head.title = headerText;
      menu.appendChild(head);
    }
    (items || []).forEach((item) => {
      if (!item || item.type === "sep") {
        const sep = document.createElement("div");
        sep.className = "db-ctx-sep";
        menu.appendChild(sep);
        return;
      }
      const btn = document.createElement("button");
      btn.type = "button";
      btn.className = `db-ctx-item${item.danger ? " danger" : ""}`;
      btn.disabled = !!item.disabled;
      btn.innerHTML = `<span style="width:16px;text-align:center;flex-shrink:0">${esc(item.icon || "")}</span><span>${esc(item.label || "")}</span>`;
      btn.onclick = async (ev) => {
        ev.stopPropagation();
        hideDbContextMenu();
        if (item.onClick) await item.onClick();
      };
      menu.appendChild(btn);
    });
    menu.hidden = false;
    menu.style.left = "0px";
    menu.style.top = "0px";
    const r = menu.getBoundingClientRect();
    const left = Math.max(8, Math.min(window.innerWidth - r.width - 8, clientX));
    const top = Math.max(8, Math.min(window.innerHeight - r.height - 8, clientY));
    menu.style.left = `${left}px`;
    menu.style.top = `${top}px`;
  }

  function diagramTableKey(name) {
    const s = String(name || "").trim().toLowerCase();
    const dot = s.lastIndexOf(".");
    return dot >= 0 ? s.slice(dot + 1) : s;
  }

  function buildDiagramEdges(data, includeInferred = true) {
    const tables = data?.tables || [];
    const fks = data?.foreignKeys || [];
    const byLower = new Map();
    const byShort = new Map();
    tables.forEach((t) => {
      const low = String(t.name || "").toLowerCase();
      byLower.set(low, t);
      const short = diagramTableKey(t.name);
      if (!byShort.has(short)) byShort.set(short, t);
    });

    const resolveTable = (name) => {
      const low = String(name || "").toLowerCase();
      return byLower.get(low) || byShort.get(diagramTableKey(low)) || null;
    };

    const edges = [];
    const seen = new Set();
    fks.forEach((fk) => {
      const fromTbl = resolveTable(fk.fromTable);
      const toTbl = resolveTable(fk.toTable);
      if (!fromTbl || !toTbl) return;
      const key = `${fromTbl.name}|${String(fk.fromColumn || "").toLowerCase()}|${toTbl.name}`;
      if (seen.has(key)) return;
      seen.add(key);
      edges.push({
        constraintName: fk.constraintName || "",
        fromTable: fromTbl.name,
        fromColumn: fk.fromColumn,
        toTable: toTbl.name,
        toColumn: fk.toColumn || "id",
        updateRule: fk.updateRule || "",
        deleteRule: fk.deleteRule || "",
        inferred: false,
      });
    });

    if (!includeInferred) return edges;

    const findTargetTableForFkCol = (colName) => {
      const low = String(colName || "").toLowerCase();
      if (!low.endsWith("_id") || low.length <= 3) return null;
      const base = low.slice(0, -3).replace(/^tbl_/, "");
      const candidates = [base, `${base}s`, `${base}es`];
      if (base.endsWith("y") && base.length > 1) candidates.push(`${base.slice(0, -1)}ies`);
      if (base.endsWith("s")) candidates.push(base.slice(0, -1));
      if (base.endsWith("ies")) candidates.push(`${base.slice(0, -3)}y`);
      for (const c of candidates) {
        const hit = byShort.get(c);
        if (hit) return hit;
      }
      return null;
    };

    tables.forEach((t) => {
      (t.columns || []).forEach((col) => {
        const target = findTargetTableForFkCol(col.name);
        if (!target) return;
        const key = `${t.name}|${String(col.name || "").toLowerCase()}|${target.name}`;
        if (seen.has(key)) return;
        const pkCol = (target.columns || []).find((c) => c.isPrimaryKey)?.name || (target.columns || []).find((c) => c.name.toLowerCase() === "id")?.name || "";
        if (!pkCol) return;
        seen.add(key);
        edges.push({
          constraintName: `inferred:${t.name}.${col.name}`,
          fromTable: t.name,
          fromColumn: col.name,
          toTable: target.name,
          toColumn: pkCol,
          updateRule: "",
          deleteRule: "",
          inferred: true,
        });
      });
    });
    return edges;
  }

  function computeDiagramAutoLayout(tables, edges) {
    const layout = {};
    if (!tables || !tables.length) return layout;

    const degree = new Map();
    tables.forEach((t) => degree.set(t.name, 0));
    (edges || []).forEach((e) => {
      degree.set(e.fromTable, (degree.get(e.fromTable) || 0) + 1);
      degree.set(e.toTable, (degree.get(e.toTable) || 0) + 1);
    });

    const sorted = [...tables].sort((a, b) => {
      const da = degree.get(a.name) || 0;
      const db = degree.get(b.name) || 0;
      if (da !== db) return db - da;
      return a.name.localeCompare(b.name);
    });

    const colsPerRow = Math.max(2, Math.min(5, Math.ceil(Math.sqrt(sorted.length * 1.15))));
    const gapX = 310;
    const gapY = 44;
    const startX = 48;
    const startY = 42;
    let curY = startY;

    for (let i = 0; i < sorted.length; i += colsPerRow) {
      const slice = sorted.slice(i, i + colsPerRow);
      let maxRowH = 120;
      slice.forEach((t, colIdx) => {
        const colRows = Math.min(11, Math.max(1, (t.columns || []).length));
        const estH = 42 + colRows * 25 + 10;
        if (estH > maxRowH) maxRowH = estH;
        layout[t.name] = {
          x: startX + colIdx * gapX,
          y: curY,
        };
      });
      curY += maxRowH + gapY;
    }
    return layout;
  }

  function diagramToMermaid(data, edges, visibleSet) {
    const tables = (data?.tables || []).filter((t) => !visibleSet || visibleSet.has(t.name));
    const visNames = new Set(tables.map((t) => t.name));
    const cleanId = (s) => String(s || "tbl").replace(/[^A-Za-z0-9_]/g, "_");
    const lines = ["erDiagram"];
    tables.forEach((t) => {
      lines.push(`  ${cleanId(t.name)} {`);
      (t.columns || []).forEach((c) => {
        const typ = String(c.dataType || "string").replace(/\s+/g, "_").replace(/[^A-Za-z0-9_()]/g, "");
        const col = cleanId(c.name);
        const flag = c.isPrimaryKey ? " PK" : "";
        lines.push(`    ${typ || "any"} ${col}${flag}`);
      });
      lines.push("  }");
    });
    (edges || []).forEach((e) => {
      if (!visNames.has(e.fromTable) || !visNames.has(e.toTable)) return;
      const rel = e.inferred ? "}o..||" : "}o--||";
      lines.push(`  ${cleanId(e.fromTable)} ${rel} ${cleanId(e.toTable)} : "${e.fromColumn} -> ${e.toColumn}"`);
    });
    return lines.join("\n");
  }

  function diagramToSvgDocument(diagState, edges, visibleTables) {
    const layout = diagState.layout || {};
    const nodeW = 248;
    const nodePos = [];
    let minX = Infinity, minY = Infinity, maxX = -Infinity, maxY = -Infinity;
    visibleTables.forEach((t) => {
      const p = layout[t.name] || { x: 40, y: 40 };
      const cols = t.columns || [];
      const h = 36 + Math.max(1, cols.length) * 23 + 8;
      nodePos.push({ t, x: p.x, y: p.y, w: nodeW, h });
      minX = Math.min(minX, p.x);
      minY = Math.min(minY, p.y);
      maxX = Math.max(maxX, p.x + nodeW);
      maxY = Math.max(maxY, p.y + h);
    });
    if (!nodePos.length) {
      minX = 0; minY = 0; maxX = 600; maxY = 300;
    }
    const pad = 36;
    const vbX = Math.floor(minX - pad);
    const vbY = Math.floor(minY - pad);
    const vbW = Math.ceil(maxX - minX + pad * 2);
    const vbH = Math.ceil(maxY - minY + pad * 2);
    const posMap = new Map(nodePos.map((n) => [n.t.name, n]));
    const escXml = (s) => String(s || "").replace(/&/g, "&amp;").replace(/</g, "&lt;").replace(/>/g, "&gt;").replace(/"/g, "&quot;");

    const edgePaths = (edges || []).map((e) => {
      const a = posMap.get(e.fromTable);
      const b = posMap.get(e.toTable);
      if (!a || !b) return "";
      const axIdx = Math.max(0, (a.t.columns || []).findIndex((c) => c.name === e.fromColumn));
      const bxIdx = Math.max(0, (b.t.columns || []).findIndex((c) => c.name === e.toColumn));
      const y1 = a.y + 44 + axIdx * 23;
      const y2 = b.y + 44 + bxIdx * 23;
      let d = "";
      if (a === b) {
        const rx = a.x + a.w;
        d = `M ${rx} ${y1} C ${rx + 56} ${y1}, ${rx + 56} ${y2}, ${rx} ${y2}`;
      } else if (a.x + a.w < b.x) {
        const x1 = a.x + a.w;
        const x2 = b.x;
        const dx = Math.max(36, (x2 - x1) * 0.45);
        d = `M ${x1} ${y1} C ${x1 + dx} ${y1}, ${x2 - dx} ${y2}, ${x2} ${y2}`;
      } else if (b.x + b.w < a.x) {
        const x1 = a.x;
        const x2 = b.x + b.w;
        const dx = Math.max(36, (x1 - x2) * 0.45);
        d = `M ${x1} ${y1} C ${x1 - dx} ${y1}, ${x2 + dx} ${y2}, ${x2} ${y2}`;
      } else {
        const x1 = a.x + a.w;
        const x2 = b.x + b.w;
        const bend = Math.max(x1, x2) + 46;
        d = `M ${x1} ${y1} C ${bend} ${y1}, ${bend} ${y2}, ${x2} ${y2}`;
      }
      const stroke = e.inferred ? "#38bdf8" : "#14b8a6";
      const dash = e.inferred ? ` stroke-dasharray="5 4"` : "";
      const marker = e.inferred ? "url(#fk-arrow-inf)" : "url(#fk-arrow)";
      return `<path d="${d}" fill="none" stroke="${stroke}" stroke-width="2"${dash} marker-end="${marker}"/>`;
    }).join("\n");

    const truncTxt = (s, maxLen) => {
      const str = String(s || "");
      return str.length > maxLen ? `${str.slice(0, maxLen - 1)}…` : str;
    };

    const cards = nodePos.map((n) => {
      const colRows = (n.t.columns || []).map((c, idx) => {
        const ry = n.y + 48 + idx * 23;
        const badge = c.isPrimaryKey ? "PK" : "";
        return `<text x="${n.x + 10}" y="${ry}" fill="${c.isPrimaryKey ? "#fbbf24" : "#94a3b8"}" font-size="10" font-family="Consolas, 'Courier New', monospace">${escXml(badge)}</text>
          <text x="${n.x + 30}" y="${ry}" fill="#e2e8f0" font-size="11.5" font-family="Consolas, 'Courier New', monospace">${escXml(truncTxt(c.name, 20))}</text>
          <text x="${n.x + n.w - 10}" y="${ry}" text-anchor="end" fill="#94a3b8" font-size="10.5" font-family="Consolas, 'Courier New', monospace">${escXml(truncTxt(c.dataType || "", 12))}</text>`;
      }).join("\n");
      return `<g>
        <rect x="${n.x}" y="${n.y}" width="${n.w}" height="${n.h}" rx="8" fill="#131c2e" stroke="#26334d"/>
        <rect x="${n.x}" y="${n.y}" width="${n.w}" height="32" rx="8" fill="#1a263e"/>
        <text x="${n.x + 10}" y="${n.y + 20}" fill="#f8fafc" font-size="12" font-weight="700" font-family="Segoe UI, system-ui, sans-serif">${escXml(truncTxt(n.t.name, 28))}</text>
        ${colRows}
      </g>`;
    }).join("\n");

    return `<?xml version="1.0" encoding="UTF-8"?>
<svg xmlns="http://www.w3.org/2000/svg" viewBox="${vbX} ${vbY} ${vbW} ${vbH}" width="${vbW}" height="${vbH}">
  <rect x="${vbX}" y="${vbY}" width="${vbW}" height="${vbH}" fill="#0b1120"/>
  <defs>
    <marker id="fk-arrow" viewBox="0 0 10 10" refX="8" refY="5" markerWidth="6" markerHeight="6" orient="auto-start-reverse">
      <path d="M 0 1 L 10 5 L 0 9 z" fill="#14b8a6"/>
    </marker>
    <marker id="fk-arrow-inf" viewBox="0 0 10 10" refX="8" refY="5" markerWidth="6" markerHeight="6" orient="auto-start-reverse">
      <path d="M 0 1 L 10 5 L 0 9 z" fill="#38bdf8"/>
    </marker>
  </defs>
  ${edgePaths}
  ${cards}
</svg>`;
  }

  function utf8ToBase64(str) {
    const bytes = new TextEncoder().encode(String(str || ""));
    let bin = "";
    for (let i = 0; i < bytes.length; i++) {
      bin += String.fromCharCode(bytes[i]);
    }
    return btoa(bin);
  }

  function rasterizeSvgToCanvas(svgDoc) {
    return new Promise((resolve, reject) => {
      const wMatch = svgDoc.match(/\bwidth="(\d+(?:\.\d+)?)"/);
      const hMatch = svgDoc.match(/\bheight="(\d+(?:\.\d+)?)"/);
      const w = Math.max(100, Math.ceil(Number(wMatch?.[1] || 800)));
      const h = Math.max(100, Math.ceil(Number(hMatch?.[1] || 600)));
      const maxDim = 7600;
      const scale = Math.max(1, Math.min(2, Math.floor(Math.min(maxDim / w, maxDim / h) * 10) / 10));

      const img = new Image();
      img.onload = () => {
        try {
          const canvas = document.createElement("canvas");
          canvas.width = Math.round(w * scale);
          canvas.height = Math.round(h * scale);
          const ctx = canvas.getContext("2d");
          ctx.fillStyle = "#0b1120";
          ctx.fillRect(0, 0, canvas.width, canvas.height);
          ctx.scale(scale, scale);
          ctx.drawImage(img, 0, 0, w, h);
          resolve(canvas);
        } catch (err) {
          reject(err);
        }
      };
      img.onerror = () => reject(new Error("Failed to rasterize SVG diagram"));
      img.src = `data:image/svg+xml;base64,${utf8ToBase64(svgDoc)}`;
    });
  }

  function downloadDataUrlFallback(filename, urlOrBlob, isBlob = false) {
    const url = isBlob ? URL.createObjectURL(urlOrBlob) : urlOrBlob;
    const a = document.createElement("a");
    a.href = url;
    a.download = filename;
    document.body.appendChild(a);
    a.click();
    a.remove();
    if (isBlob) setTimeout(() => URL.revokeObjectURL(url), 1000);
  }

  async function exportDiagramFormat(tab, diagState, visibleEdges, visibleTables, visSet, format) {
    const dbSlug = String(tab.db || "database").replace(/[^A-Za-z0-9_.-]+/g, "_");
    const svgDoc = diagramToSvgDocument(diagState, visibleEdges, visibleTables);
    const filename = `${dbSlug}-diagram.${format}`;

    try {
      if (format === "copy-png") {
        const canvas = await rasterizeSvgToCanvas(svgDoc);
        const pngBlob = await new Promise((res, rej) =>
          canvas.toBlob((b) => (b ? res(b) : rej(new Error("PNG encoding failed"))), "image/png")
        );
        if (navigator.clipboard && typeof ClipboardItem !== "undefined") {
          await navigator.clipboard.write([new ClipboardItem({ "image/png": pngBlob })]);
          toast("Copied diagram PNG image to clipboard!", "ok");
        } else {
          const dataUrl = canvas.toDataURL("image/png");
          if (api()?.SaveDiagramFileDialog) {
            const p = await api().SaveDiagramFileDialog(`${dbSlug}-diagram.png`, "png", dataUrl);
            if (p) toast(`Saved diagram PNG: ${p}`, "ok");
          } else {
            downloadDataUrlFallback(`${dbSlug}-diagram.png`, pngBlob, true);
          }
        }
        return;
      }

      let payloadB64 = "";
      let fallbackBlob = null;
      if (format === "png" || format === "jpg" || format === "webp") {
        const mime = format === "jpg" ? "image/jpeg" : `image/${format}`;
        const canvas = await rasterizeSvgToCanvas(svgDoc);
        const dataUrl = canvas.toDataURL(mime, format === "png" ? undefined : 0.94);
        payloadB64 = dataUrl;
        fallbackBlob = await (await fetch(dataUrl)).blob();
      } else if (format === "svg") {
        payloadB64 = utf8ToBase64(svgDoc);
        fallbackBlob = new Blob([svgDoc], { type: "image/svg+xml;charset=utf-8" });
      } else if (format === "html") {
        const htmlDoc = `<!DOCTYPE html><html lang="en"><head><meta charset="utf-8"><title>${esc(tab.db)} — ER Diagram</title><style>body{margin:0;background:#0b1120;color:#e2e8f0;font-family:system-ui,sans-serif;display:flex;flex-direction:column;min-height:100vh}header{padding:10px 16px;background:#131c2e;border-bottom:1px solid #26334d;font-size:13px;font-weight:600}main{flex:1;overflow:auto;padding:20px;display:flex;justify-content:center;align-items:flex-start}svg{max-width:100%;height:auto;border-radius:10px;box-shadow:0 12px 32px rgba(0,0,0,0.45)}</style></head><body><header>${esc(tab.engine)} / ${esc(tab.db)} — Database ER Diagram</header><main>${svgDoc.replace(/^<\?xml[^>]*\?>\s*/i, "")}</main></body></html>`;
        payloadB64 = utf8ToBase64(htmlDoc);
        fallbackBlob = new Blob([htmlDoc], { type: "text/html;charset=utf-8" });
      } else if (format === "mmd") {
        const mmd = diagramToMermaid(diagState.data, visibleEdges, visSet);
        payloadB64 = utf8ToBase64(mmd);
        fallbackBlob = new Blob([mmd], { type: "text/plain;charset=utf-8" });
      } else {
        return;
      }

      if (api()?.SaveDiagramFileDialog) {
        const savedPath = await api().SaveDiagramFileDialog(filename, format, payloadB64);
        if (savedPath) toast(`Saved diagram: ${savedPath}`, "ok");
      } else if (fallbackBlob) {
        downloadDataUrlFallback(filename, fallbackBlob, true);
        toast(`Exported ${filename}`, "ok");
      }
    } catch (err) {
      toast(errMsg(err), "err");
    }
  }

  function refreshDiagramViewForTab(tab) {
    if (!tab) return;
    if (tab.detached) {
      updateFloatingSqlWin(tab);
      return;
    }
    if (tab.engine === state.dbEngine && tab.id === state.activeSqlTabId) {
      renderDatabaseDiagramInto($("sqlDiagramStage"), tab);
    }
  }

  async function loadDatabaseDiagramData(tab, { preserveLayout = false } = {}) {
    if (!tab || !tab.diagram) return;
    const d = tab.diagram;
    d.loading = true;
    d.error = "";
    tab.loading = true;
    renderSqlTabs();
    refreshDiagramViewForTab(tab);
    try {
      const data = await api().GetDatabaseDiagram(tab.engine, tab.db);
      d.data = data || { engine: tab.engine, database: tab.db, tables: [], foreignKeys: [] };
      const edges = buildDiagramEdges(d.data, d.showInferred);
      const freshLayout = computeDiagramAutoLayout(d.data.tables || [], edges);
      if (preserveLayout && d.layout) {
        const merged = {};
        (d.data.tables || []).forEach((t) => {
          merged[t.name] = d.layout[t.name] || freshLayout[t.name] || { x: 40, y: 40 };
        });
        d.layout = merged;
      } else {
        d.layout = freshLayout;
        d._fitPending = true;
      }
      d.loading = false;
      tab.loading = false;
      renderSqlTabs();
      refreshDiagramViewForTab(tab);
    } catch (err) {
      d.loading = false;
      d.error = errMsg(err);
      tab.loading = false;
      renderSqlTabs();
      refreshDiagramViewForTab(tab);
    }
  }

  async function openDatabaseDiagramTab({ engine = state.dbEngine, db = state.sqlDb, focusTable = "" } = {}) {
    if (!db) {
      toast("Select a database first to open a diagram", "info");
      return null;
    }
    syncActiveTabFromDom();
    if (engine === state.dbEngine && db !== state.sqlDb) {
      state.sqlDb = db;
      renderSqlDbList();
      loadSqlTables(db, false);
    }
    const existing = engineTabs(engine).find((t) => t.kind === "diagram" && t.db === db);
    if (existing) {
      if (existing.diagram && focusTable) existing.diagram.selectedTable = focusTable;
      if (existing.detached) {
        updateFloatingSqlWin(existing);
        focusFloatingSqlWin(floatingSqlWins.get(existing.id));
      } else {
        applyTabToDom(existing, { syncSidebar: true });
      }
      if (existing.diagram && !existing.diagram.data && !existing.diagram.loading) {
        loadDatabaseDiagramData(existing);
      }
      return existing;
    }
    const tab = createSqlTabObj({
      engine,
      db,
      kind: "diagram",
      title: `🗺️ ${db}`,
      diagram: {
        database: db,
        loading: true,
        error: "",
        data: null,
        layout: {},
        panX: 24,
        panY: 20,
        zoom: 1,
        showInferred: true,
        visibleTables: null,
        selectedTable: focusTable || "",
        filterText: "",
        pickerOpen: false,
        _fitPending: true,
      },
    });
    state.sqlTabs.push(tab);
    applyTabToDom(tab, { syncSidebar: true });
    await loadDatabaseDiagramData(tab);
    return tab;
  }

  function renderDatabaseDiagramInto(container, tab) {
    if (!container || !tab || !tab.diagram) return;
    const d = tab.diagram;
    const allTables = d.data?.tables || [];
    const visSet = d.visibleTables ? new Set(d.visibleTables) : null;
    const visibleTables = allTables.filter((t) => !visSet || visSet.has(t.name));
    const allEdges = buildDiagramEdges(d.data, d.showInferred);
    const visNames = new Set(visibleTables.map((t) => t.name));
    const visibleEdges = allEdges.filter((e) => visNames.has(e.fromTable) && visNames.has(e.toTable));
    const fkColMap = new Map();
    visibleEdges.forEach((e) => {
      const k = `${e.fromTable}|${String(e.fromColumn || "").toLowerCase()}`;
      fkColMap.set(k, e);
    });

    const zoomPct = Math.round((d.zoom || 1) * 100);
    container.innerHTML = `
      <div class="diagram-root">
        <div class="diagram-toolbar">
          <span class="chip accent" title="Database Diagram">${esc(`${tab.engine} / ${tab.db}`)}</span>
          <span class="chip">${visibleTables.length}/${allTables.length} tables · ${visibleEdges.length} links</span>
          <button type="button" class="btn xs" data-diag-act="picker">📋 Tables</button>
          <label class="inline-check" style="margin:0 4px;font-size:11.5px;gap:5px" title="Draw dashed inferred links for *_id columns even without explicit SQL FOREIGN KEY constraints">
            <input type="checkbox" data-diag-act="inferred" ${d.showInferred ? "checked" : ""} />
            <span>Inferred *_id</span>
          </label>
          <div class="spacer"></div>
          <button type="button" class="btn xs" data-diag-act="arrange" title="Auto-arrange table cards cleanly">✨ Auto Arrange</button>
          <button type="button" class="btn xs" data-diag-act="fit" title="Fit diagram to viewport">⊡ Fit</button>
          <button type="button" class="btn xs" data-diag-act="zoom-out" title="Zoom out">−</button>
          <span class="chip" data-diag-zoom-label>${zoomPct}%</span>
          <button type="button" class="btn xs" data-diag-act="zoom-in" title="Zoom in">+</button>
          <button type="button" class="btn xs" data-diag-act="mermaid" title="Copy Mermaid erDiagram snippet">Mermaid</button>
          <button type="button" class="btn xs" data-diag-act="export" title="Export diagram as PNG, JPEG, WebP, SVG, HTML, or Mermaid">📤 Export ▾</button>
          <button type="button" class="btn xs primary" data-diag-act="refresh" title="Reload schema & foreign keys">↻ Refresh</button>
        </div>
        <div class="diagram-picker-pop" ${d.pickerOpen ? "" : "hidden"}>
          <input type="search" class="input xs" data-diag-picker-filter placeholder="Filter tables..." value="${esc(d.filterText || "")}" />
          <div style="display:flex;gap:4px;flex-wrap:wrap">
            <button type="button" class="btn xs" data-diag-pick="all">All</button>
            <button type="button" class="btn xs" data-diag-pick="none">None</button>
            <button type="button" class="btn xs" data-diag-pick="linked">Linked</button>
          </div>
          <div class="diagram-picker-list">
            ${allTables
              .filter((t) => !d.filterText || t.name.toLowerCase().includes(d.filterText.toLowerCase()))
              .map((t) => {
                const checked = !visSet || visSet.has(t.name);
                return `<label class="inline-check" style="gap:6px;padding:2px 4px;border-radius:4px">
                  <input type="checkbox" data-diag-pick-tbl="${esc(t.name)}" ${checked ? "checked" : ""} />
                  <span style="overflow:hidden;text-overflow:ellipsis;white-space:nowrap">${esc(t.name)}</span>
                </label>`;
              })
              .join("")}
          </div>
        </div>
        <div class="diagram-viewport">
          <div class="diagram-world" style="transform:translate(${d.panX || 0}px,${d.panY || 0}px) scale(${d.zoom || 1})">
            <svg class="diagram-svg" width="4000" height="3000"></svg>
            ${visibleTables
              .map((t) => {
                const pos = d.layout?.[t.name] || { x: 40, y: 40 };
                const isView = String(t.kind || "").toUpperCase().includes("VIEW");
                const isSel = d.selectedTable === t.name;
                return `<div class="diagram-node${isSel ? " is-hot" : ""}" data-diag-node="${esc(t.name)}" style="left:${pos.x}px;top:${pos.y}px">
                  <div class="diagram-node-head${isView ? " is-view" : ""}" title="Drag to reposition · Double-click header to browse rows">
                    <span>${isView ? "👁️" : "📋"}</span>
                    <span class="diagram-node-title">${esc(t.name)}</span>
                    <button type="button" class="diagram-node-open" data-diag-open-tbl="${esc(t.name)}" title="Open table rows in query tab">↗</button>
                  </div>
                  <div class="diagram-node-cols">
                    ${(t.columns || [])
                      .map((c) => {
                        const fkEdge = fkColMap.get(`${t.name}|${String(c.name || "").toLowerCase()}`);
                        const badge = c.isPrimaryKey ? "🔑" : fkEdge ? "🔗" : "";
                        const badgeTip = c.isPrimaryKey
                          ? "Primary Key"
                          : fkEdge
                            ? `${fkEdge.inferred ? "Inferred link" : "Foreign Key"} -> ${fkEdge.toTable}.${fkEdge.toColumn}`
                            : "";
                        return `<div class="diagram-col-row" data-diag-col="${esc(c.name)}" title="${esc(badgeTip || c.dataType || "")}">
                          <span class="diagram-col-badges">${badge}</span>
                          <span class="diagram-col-name">${esc(c.name)}</span>
                          <span class="diagram-col-type">${esc(c.dataType || "")}</span>
                          <span class="diagram-col-null">${c.nullable ? "?" : ""}</span>
                        </div>`;
                      })
                      .join("")}
                  </div>
                </div>`;
              })
              .join("")}
          </div>
          ${
            d.loading
              ? `<div class="diagram-empty-overlay"><span class="muted">Loading tables, columns, and relationships for <b>${esc(tab.db)}</b>...</span></div>`
              : d.error
                ? `<div class="diagram-empty-overlay"><span style="color:var(--red)">${esc(d.error)}</span><button type="button" class="btn xs primary" data-diag-act="refresh">Retry</button></div>`
                : !allTables.length
                  ? `<div class="diagram-empty-overlay"><span class="muted">No tables found in <b>${esc(tab.db)}</b>.</span></div>`
                  : ""
          }
        </div>
      </div>
    `;

    const viewport = container.querySelector(".diagram-viewport");
    const world = container.querySelector(".diagram-world");
    const svg = container.querySelector(".diagram-svg");
    const zoomLbl = container.querySelector("[data-diag-zoom-label]");

    const applyTransform = () => {
      if (world) world.style.transform = `translate(${d.panX}px,${d.panY}px) scale(${d.zoom})`;
      if (zoomLbl) zoomLbl.textContent = `${Math.round(d.zoom * 100)}%`;
    };

    const nodeEls = new Map();
    container.querySelectorAll(".diagram-node").forEach((el) => {
      nodeEls.set(el.dataset.diagNode, el);
    });

    const getColMidY = (nodeEl, colName) => {
      const top = nodeEl.offsetTop;
      const h = nodeEl.offsetHeight || 90;
      const colsWrap = nodeEl.querySelector(".diagram-node-cols");
      const rows = colsWrap ? Array.from(colsWrap.querySelectorAll(".diagram-col-row")) : [];
      const row = rows.find((r) => String(r.dataset.diagCol || "").toLowerCase() === String(colName || "").toLowerCase());
      if (!row || !colsWrap) return top + Math.min(48, h / 2);
      const relY = colsWrap.offsetTop + row.offsetTop - colsWrap.scrollTop + row.offsetHeight / 2;
      return Math.max(top + 18, Math.min(top + h - 8, top + relY));
    };

    const redrawSvg = () => {
      if (!svg) return;
      const defs = `<defs>
        <marker id="diag-fk-arrow" viewBox="0 0 10 10" refX="8" refY="5" markerWidth="6" markerHeight="6" orient="auto-start-reverse">
          <path d="M 0 1 L 10 5 L 0 9 z" fill="var(--accent)"/>
        </marker>
        <marker id="diag-fk-arrow-inf" viewBox="0 0 10 10" refX="8" refY="5" markerWidth="6" markerHeight="6" orient="auto-start-reverse">
          <path d="M 0 1 L 10 5 L 0 9 z" fill="#38bdf8"/>
        </marker>
      </defs>`;
      const parts = [defs];
      visibleEdges.forEach((e, idx) => {
        const aEl = nodeEls.get(e.fromTable);
        const bEl = nodeEls.get(e.toTable);
        if (!aEl || !bEl) return;
        const ax = aEl.offsetLeft;
        const aw = aEl.offsetWidth || 248;
        const bx = bEl.offsetLeft;
        const bw = bEl.offsetWidth || 248;
        const y1 = getColMidY(aEl, e.fromColumn);
        const y2 = getColMidY(bEl, e.toColumn);
        let pathD = "";
        if (aEl === bEl) {
          const rx = ax + aw;
          pathD = `M ${rx} ${y1} C ${rx + 56} ${y1}, ${rx + 56} ${y2}, ${rx} ${y2}`;
        } else if (ax + aw + 18 < bx) {
          const x1 = ax + aw;
          const x2 = bx;
          const dx = Math.max(38, (x2 - x1) * 0.46);
          pathD = `M ${x1} ${y1} C ${x1 + dx} ${y1}, ${x2 - dx} ${y2}, ${x2} ${y2}`;
        } else if (bx + bw + 18 < ax) {
          const x1 = ax;
          const x2 = bx + bw;
          const dx = Math.max(38, (x1 - x2) * 0.46);
          pathD = `M ${x1} ${y1} C ${x1 - dx} ${y1}, ${x2 + dx} ${y2}, ${x2} ${y2}`;
        } else {
          const x1 = ax + aw;
          const x2 = bx + bw;
          const bend = Math.max(x1, x2) + 46;
          pathD = `M ${x1} ${y1} C ${bend} ${y1}, ${bend} ${y2}, ${x2} ${y2}`;
        }
        const tip = `${e.fromTable}.${e.fromColumn} -> ${e.toTable}.${e.toColumn}${e.constraintName ? ` (${e.constraintName})` : ""}`;
        const marker = e.inferred ? "url(#diag-fk-arrow-inf)" : "url(#diag-fk-arrow)";
        parts.push(
          `<path class="diagram-fk-path${e.inferred ? " inferred" : ""}" data-edge-idx="${idx}" data-from="${esc(e.fromTable)}" data-to="${esc(e.toTable)}" d="${pathD}" marker-end="${marker}"><title>${esc(tip)}</title></path>`
        );
      });
      svg.innerHTML = parts.join("");

      svg.querySelectorAll(".diagram-fk-path").forEach((pEl) => {
        pEl.addEventListener("mouseenter", () => {
          pEl.classList.add("is-hot");
          nodeEls.get(pEl.dataset.from)?.classList.add("is-hot");
          nodeEls.get(pEl.dataset.to)?.classList.add("is-hot");
        });
        pEl.addEventListener("mouseleave", () => {
          pEl.classList.remove("is-hot");
          if (d.selectedTable !== pEl.dataset.from) nodeEls.get(pEl.dataset.from)?.classList.remove("is-hot");
          if (d.selectedTable !== pEl.dataset.to) nodeEls.get(pEl.dataset.to)?.classList.remove("is-hot");
        });
      });
    };

    const fitView = () => {
      if (!viewport || !nodeEls.size) return;
      const vr = viewport.getBoundingClientRect();
      if (vr.width < 40 || vr.height < 40) return;
      let minX = Infinity, minY = Infinity, maxX = -Infinity, maxY = -Infinity;
      nodeEls.forEach((el) => {
        minX = Math.min(minX, el.offsetLeft);
        minY = Math.min(minY, el.offsetTop);
        maxX = Math.max(maxX, el.offsetLeft + (el.offsetWidth || 248));
        maxY = Math.max(maxY, el.offsetTop + (el.offsetHeight || 160));
      });
      const contentW = Math.max(100, maxX - minX);
      const contentH = Math.max(100, maxY - minY);
      const pad = 28;
      const scale = Math.max(0.32, Math.min(1.1, Math.min((vr.width - pad * 2) / contentW, (vr.height - pad * 2) / contentH)));
      d.zoom = Number(scale.toFixed(3));
      d.panX = Math.round((vr.width - contentW * d.zoom) / 2 - minX * d.zoom);
      d.panY = Math.round((vr.height - contentH * d.zoom) / 2 - minY * d.zoom);
      applyTransform();
    };

    // Node dragging, hover highlighting, and open-table shortcut
    nodeEls.forEach((el, tblName) => {
      el.addEventListener("mouseenter", () => {
        svg?.querySelectorAll(".diagram-fk-path").forEach((p) => {
          if (p.dataset.from === tblName || p.dataset.to === tblName) p.classList.add("is-hot");
        });
      });
      el.addEventListener("mouseleave", () => {
        svg?.querySelectorAll(".diagram-fk-path.is-hot").forEach((p) => p.classList.remove("is-hot"));
      });
      el.querySelector(".diagram-node-cols")?.addEventListener("scroll", () => redrawSvg(), { passive: true });
      const head = el.querySelector(".diagram-node-head");
      head?.addEventListener("dblclick", (ev) => {
        if (ev.target.closest("button")) return;
        const tblObj = state.sqlTables.find((x) => x.name === tblName) || { name: tblName, kind: "BASE TABLE", select: `SELECT * FROM ${tblName} LIMIT 50;`, selectAll: `SELECT * FROM ${tblName};` };
        if (tab.db === state.sqlDb) selectSqlTable(tblObj, false);
      });
      head?.addEventListener("pointerdown", (ev) => {
        if (ev.button !== 0 || ev.target.closest("button")) return;
        ev.preventDefault();
        ev.stopPropagation();
        d.selectedTable = tblName;
        nodeEls.forEach((n, k) => n.classList.toggle("is-hot", k === tblName));
        const startX = ev.clientX;
        const startY = ev.clientY;
        const origX = el.offsetLeft;
        const origY = el.offsetTop;
        const onMv = (m) => {
          const z = d.zoom || 1;
          const nx = Math.max(0, Math.round(origX + (m.clientX - startX) / z));
          const ny = Math.max(0, Math.round(origY + (m.clientY - startY) / z));
          el.style.left = `${nx}px`;
          el.style.top = `${ny}px`;
          d.layout[tblName] = { x: nx, y: ny };
          redrawSvg();
        };
        const onUp = () => {
          window.removeEventListener("pointermove", onMv);
          window.removeEventListener("pointerup", onUp);
        };
        window.addEventListener("pointermove", onMv);
        window.addEventListener("pointerup", onUp);
      });
    });

    container.querySelectorAll("[data-diag-open-tbl]").forEach((btn) => {
      btn.onclick = async (ev) => {
        ev.stopPropagation();
        const tblName = btn.dataset.diagOpenTbl;
        if (state.dbEngine !== tab.engine) return;
        if (state.sqlDb !== tab.db) {
          state.sqlDb = tab.db;
          renderSqlDbList();
          await loadSqlTables(tab.db, false);
        }
        const tblObj = state.sqlTables.find((x) => x.name === tblName);
        if (tblObj) await selectSqlTable(tblObj, false);
      };
    });

    // Pan & Zoom on background
    if (viewport) {
      viewport.addEventListener("pointerdown", (ev) => {
        if (ev.button !== 0 || ev.target.closest(".diagram-node")) return;
        ev.preventDefault();
        viewport.classList.add("is-panning");
        const sx = ev.clientX;
        const sy = ev.clientY;
        const ox = d.panX || 0;
        const oy = d.panY || 0;
        const onMv = (m) => {
          d.panX = Math.round(ox + (m.clientX - sx));
          d.panY = Math.round(oy + (m.clientY - sy));
          applyTransform();
        };
        const onUp = () => {
          viewport.classList.remove("is-panning");
          window.removeEventListener("pointermove", onMv);
          window.removeEventListener("pointerup", onUp);
        };
        window.addEventListener("pointermove", onMv);
        window.addEventListener("pointerup", onUp);
      });
      viewport.addEventListener("wheel", (ev) => {
        ev.preventDefault();
        const vr = viewport.getBoundingClientRect();
        const mx = ev.clientX - vr.left;
        const my = ev.clientY - vr.top;
        const oldZ = d.zoom || 1;
        const factor = ev.deltaY < 0 ? 1.12 : 0.89;
        const newZ = Math.max(0.25, Math.min(2.2, Number((oldZ * factor).toFixed(3))));
        if (newZ === oldZ) return;
        d.panX = Math.round(mx - ((mx - (d.panX || 0)) / oldZ) * newZ);
        d.panY = Math.round(my - ((my - (d.panY || 0)) / oldZ) * newZ);
        d.zoom = newZ;
        applyTransform();
      }, { passive: false });
    }

    // Toolbar actions
    const setZoomCentered = (nextZ) => {
      const clamped = Math.max(0.25, Math.min(2.2, Number(nextZ.toFixed(3))));
      if (viewport) {
        const vr = viewport.getBoundingClientRect();
        const cx = vr.width / 2;
        const cy = vr.height / 2;
        const oldZ = d.zoom || 1;
        d.panX = Math.round(cx - ((cx - (d.panX || 0)) / oldZ) * clamped);
        d.panY = Math.round(cy - ((cy - (d.panY || 0)) / oldZ) * clamped);
      }
      d.zoom = clamped;
      applyTransform();
    };

    container.querySelectorAll("[data-diag-act]").forEach((el) => {
      if (el.tagName === "INPUT") {
        el.onchange = () => {
          d.showInferred = !!el.checked;
          renderDatabaseDiagramInto(container, tab);
        };
        return;
      }
      el.onclick = async () => {
        const act = el.dataset.diagAct;
        if (act === "picker") {
          d.pickerOpen = !d.pickerOpen;
          renderDatabaseDiagramInto(container, tab);
        } else if (act === "arrange") {
          d.layout = computeDiagramAutoLayout(visibleTables, visibleEdges);
          d._fitPending = true;
          renderDatabaseDiagramInto(container, tab);
        } else if (act === "fit") {
          fitView();
        } else if (act === "zoom-in") {
          setZoomCentered((d.zoom || 1) * 1.18);
        } else if (act === "zoom-out") {
          setZoomCentered((d.zoom || 1) * 0.85);
        } else if (act === "refresh") {
          await loadDatabaseDiagramData(tab, { preserveLayout: true });
        } else if (act === "mermaid") {
          await copyText(diagramToMermaid(d.data, visibleEdges, visSet));
          toast("Copied Mermaid erDiagram to clipboard!", "ok");
        } else if (act === "export") {
          const rect = el.getBoundingClientRect();
          showDbContextMenu(rect.left, rect.bottom + 4, `Export ${tab.db} Diagram`, [
            {
              icon: "🖼️",
              label: "PNG Image (.png)",
              onClick: () => exportDiagramFormat(tab, d, visibleEdges, visibleTables, visSet, "png"),
            },
            {
              icon: "📷",
              label: "JPEG Image (.jpg)",
              onClick: () => exportDiagramFormat(tab, d, visibleEdges, visibleTables, visSet, "jpg"),
            },
            {
              icon: "🌐",
              label: "WebP Image (.webp)",
              onClick: () => exportDiagramFormat(tab, d, visibleEdges, visibleTables, visSet, "webp"),
            },
            {
              icon: "📐",
              label: "SVG Vector (.svg)",
              onClick: () => exportDiagramFormat(tab, d, visibleEdges, visibleTables, visSet, "svg"),
            },
            {
              icon: "📄",
              label: "Standalone HTML (.html)",
              onClick: () => exportDiagramFormat(tab, d, visibleEdges, visibleTables, visSet, "html"),
            },
            {
              icon: "🧩",
              label: "Mermaid Diagram (.mmd)",
              onClick: () => exportDiagramFormat(tab, d, visibleEdges, visibleTables, visSet, "mmd"),
            },
            { type: "sep" },
            {
              icon: "📋",
              label: "Copy PNG Image to Clipboard",
              onClick: () => exportDiagramFormat(tab, d, visibleEdges, visibleTables, visSet, "copy-png"),
            },
            {
              icon: "📝",
              label: "Copy Mermaid Text",
              onClick: async () => {
                await copyText(diagramToMermaid(d.data, visibleEdges, visSet));
                toast("Copied Mermaid erDiagram to clipboard!", "ok");
              },
            },
          ]);
        }
      };
    });

    // Table picker handlers
    const pFilter = container.querySelector("[data-diag-picker-filter]");
    if (pFilter) {
      pFilter.oninput = () => {
        d.filterText = pFilter.value;
        const pos = pFilter.selectionStart;
        renderDatabaseDiagramInto(container, tab);
        const nf = container.querySelector("[data-diag-picker-filter]");
        if (nf) {
          nf.focus();
          nf.setSelectionRange(pos, pos);
        }
      };
    }
    container.querySelectorAll("[data-diag-pick]").forEach((btn) => {
      btn.onclick = () => {
        const mode = btn.dataset.diagPick;
        if (mode === "all") {
          d.visibleTables = null;
        } else if (mode === "none") {
          d.visibleTables = [];
        } else if (mode === "linked") {
          const linked = new Set();
          allEdges.forEach((e) => {
            linked.add(e.fromTable);
            linked.add(e.toTable);
          });
          d.visibleTables = Array.from(linked);
        }
        d._fitPending = true;
        renderDatabaseDiagramInto(container, tab);
      };
    });
    container.querySelectorAll("[data-diag-pick-tbl]").forEach((cb) => {
      cb.onchange = () => {
        const cur = new Set(d.visibleTables ? d.visibleTables : allTables.map((t) => t.name));
        if (cb.checked) cur.add(cb.dataset.diagPickTbl);
        else cur.delete(cb.dataset.diagPickTbl);
        d.visibleTables = cur.size === allTables.length ? null : Array.from(cur);
        renderDatabaseDiagramInto(container, tab);
      };
    });

    requestAnimationFrame(() => {
      redrawSvg();
      if (d._fitPending && !d.loading && nodeEls.size > 0) {
        d._fitPending = false;
        fitView();
        redrawSvg();
      }
    });
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
        if (existing.detached) focusFloatingSqlWin(floatingSqlWins.get(existing.id));
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

    const isStillActive = () => !tab.detached && state.activeSqlTabId === tab.id && state.dbEngine === tab.engine;
    if (tab.detached) {
      updateFloatingSqlWin(tab);
    } else if (isStillActive()) {
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
      if (tab.detached) {
        updateFloatingSqlWin(tab);
      } else if (isStillActive()) {
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
      if (tab.detached) {
        updateFloatingSqlWin(tab);
      } else if (isStillActive()) {
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

  function renderSqlTableInto(wrap, res, { filterText = "", page = 1, pageSize = 50, dbName = "", tableName = "", colWidths = null } = {}) {
    if (!res || !res.columns.length) {
      wrap.innerHTML = `<div class="empty-note">Empty result set</div>`;
      return { total: 0, page: 1, totalPages: 1, pageSize: pageSize || 50, startIdx: 0, endIdx: 0 };
    }
    const filter = String(filterText || "").trim().toLowerCase();
    const canRowCrud = !!tableName;
    const indexedRows = [];
    for (let idx = 0; idx < res.rows.length; idx++) {
      const row = res.rows[idx];
      if (filter && !row.some((c) => c !== null && String(c).toLowerCase().includes(filter))) {
        continue;
      }
      indexedRows.push({ row, idx });
    }

    const total = indexedRows.length;
    const ps = pageSize > 0 ? pageSize : Math.max(total, 1);
    const totalPages = Math.max(1, Math.ceil(total / ps));
    const safePage = Math.min(Math.max(1, page || 1), totalPages);

    const startIdx = (safePage - 1) * ps;
    const endIdx = Math.min(startIdx + ps, total);
    const visible = indexedRows.slice(startIdx, endIdx);

    const colsKey = `${dbName || ""}|${tableName || ""}|${canRowCrud ? "1:" : "0:"}${res.columns.join("\x00")}`;
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
    if (tableEl && colWidths && colWidths.colsKey === colsKey) {
      applyTableColWidths(tableEl, colWidths.widths);
    }
    wrap.scrollTop = 0;
    return { total, page: safePage, totalPages, pageSize: ps, startIdx, endIdx };
  }

  function renderSqlTable(res) {
    const wrap = $("sqlResults");
    if (!res || !res.columns.length) {
      wrap.innerHTML = `<div class="empty-note">Empty result set</div>`;
      hideSqlPager();
      return;
    }
    const stats = renderSqlTableInto(wrap, res, {
      filterText: $("sqlRowFilter")?.value || "",
      page: state.sqlPage,
      pageSize: state.sqlPageSize,
      dbName: state.sqlDb,
      tableName: state.sqlTable,
      colWidths: state.sqlColWidths,
    });
    state.sqlPage = stats.page;
    renderSqlPager(res, stats.total, res.rows.length, stats.pageSize, stats.totalPages, stats.total === 0 ? 0 : stats.startIdx + 1, stats.endIdx);
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
            : `<div class="pass-wrap"><input type="password" class="input sm eng-pass" value="${esc(es.password || "")}" placeholder="${id === "redis" ? "optional" : "blank = no password"}" /><button type="button" class="pass-toggle-btn" title="Show password" aria-label="Show password" onclick="const i=this.previousElementSibling;const s=i.type==='password';i.type=s?'text':'password';this.textContent=s?'🙈':'👁️';this.title=s?'Hide password':'Show password';">👁️</button></div>`
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
      if (btn.disabled) return;
      const prevHtml = btn.innerHTML;
      btn.disabled = true;
      btn.textContent = "⏳ Starting...";
      toast("Starting enabled services...", "info");
      try {
        await api().StartAll();
        state.statuses = (await api().Status()) || [];
        renderDashboard();
      } catch (e) {
        toast(errMsg(e), "err");
      } finally {
        btn.disabled = false;
        btn.innerHTML = prevHtml;
      }
    };
    $("btnStopAll").onclick = async () => {
      const btn = $("btnStopAll");
      if (btn.disabled) return;
      const prevHtml = btn.innerHTML;
      btn.disabled = true;
      btn.textContent = "⏳ Stopping All...";
      toast("Stopping all running services...", "info");
      try {
        await api().StopAll();
        state.statuses = (await api().Status()) || [];
        renderDashboard();
        toast("All dashboard services turned off", "ok");
      } catch (e) {
        toast(errMsg(e), "err");
      } finally {
        btn.disabled = false;
        btn.innerHTML = prevHtml;
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

    const confirmAndDropDatabase = async (name) => {
      if (!name || SYSTEM_DBS.has(name.toLowerCase())) return;
      if (!(await askConfirm("Drop Database?", `Permanently drop database "${name}"? This cannot be undone.`, "Drop")))
        return;
      try {
        await api().DropDatabase(state.dbEngine, name);
        toast(`Dropped ${name}`, "ok");
        state.sqlTabs
          .filter((t) => t.engine === state.dbEngine && t.db === name)
          .forEach((t) => removeFloatingSqlWin(t.id));
        state.sqlTabs = state.sqlTabs.filter((t) => !(t.engine === state.dbEngine && t.db === name));
        if (state.sqlDb === name) state.sqlDb = "";
        invalidateSqlCache(state.dbEngine);
        ensureActiveSqlTab();
        await loadSqlDatabases(true);
      } catch (err) {
        toast(errMsg(err), "err");
      }
    };

    const confirmAndDropTable = async (tbl) => {
      if (!tbl || !state.sqlDb) return;
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
    };

    const selectDatabaseItem = async (dbName) => {
      if (!dbName) return;
      syncActiveTabFromDom();
      state.sqlDb = dbName;
      const curTab = getActiveSqlTab();
      if (curTab && isBlankSqlTab(curTab)) {
        curTab.db = state.sqlDb;
        renderSqlTabs();
      }
      $("sqlActiveDb").textContent = `${state.dbEngine} / ${state.sqlDb}`;
      renderSqlDbList();
      await loadSqlTables(state.sqlDb);
    };

    document.addEventListener("pointerdown", (e) => {
      const menu = $("dbContextMenu");
      if (menu && !menu.hidden && !e.target.closest("#dbContextMenu")) {
        hideDbContextMenu();
      }
    });
    document.addEventListener("keydown", (e) => {
      if (e.key === "Escape") hideDbContextMenu();
    });
    window.addEventListener("blur", hideDbContextMenu);

    if ($("btnNewDiagram")) {
      $("btnNewDiagram").onclick = () => openDatabaseDiagramTab({ engine: state.dbEngine, db: state.sqlDb });
    }

    $("sqlDbList").addEventListener("click", async (e) => {
      const del = e.target.closest("[data-drop-db]");
      if (del) {
        e.stopPropagation();
        await confirmAndDropDatabase(del.dataset.dropDb);
        return;
      }
      const item = e.target.closest("[data-db]");
      if (!item) return;
      await selectDatabaseItem(item.dataset.db);
    });

    $("sqlDbList").addEventListener("contextmenu", async (e) => {
      const item = e.target.closest("[data-db]");
      if (!item) return;
      e.preventDefault();
      e.stopPropagation();
      const dbName = item.dataset.db;
      const isSys = SYSTEM_DBS.has(dbName.toLowerCase());
      if (state.sqlDb !== dbName) {
        selectDatabaseItem(dbName);
      }
      showDbContextMenu(e.clientX, e.clientY, `Database: ${dbName}`, [
        {
          icon: "🗺️",
          label: "New Database Diagram",
          onClick: () => openDatabaseDiagramTab({ engine: state.dbEngine, db: dbName }),
        },
        { type: "sep" },
        {
          icon: "📝",
          label: "New Query Tab",
          onClick: () => {
            state.sqlDb = dbName;
            const t = openNewSqlTab({ focus: true });
            if (t) {
              t.db = dbName;
              renderSqlTabs();
            }
          },
        },
        {
          icon: "➕",
          label: "Create Table...",
          onClick: () => {
            state.sqlDb = dbName;
            $("btnNewTable")?.click();
          },
        },
        {
          icon: "↻",
          label: "Refresh Tables",
          onClick: () => loadSqlTables(dbName, true),
        },
        {
          icon: "🔄",
          label: "Refresh Databases",
          onClick: () => loadSqlDatabases(true),
        },
        { type: "sep" },
        {
          icon: "🗑️",
          label: "Drop Database...",
          danger: true,
          disabled: isSys,
          onClick: () => confirmAndDropDatabase(dbName),
        },
      ]);
    });

    $("sqlTableList").addEventListener("click", async (e) => {
      const dropBtn = e.target.closest("[data-drop-table]");
      if (dropBtn) {
        e.stopPropagation();
        await confirmAndDropTable(dropBtn.dataset.dropTable);
        return;
      }
      const item = e.target.closest("[data-table-idx]");
      if (!item) return;
      const t = state.sqlTables[Number(item.dataset.tableIdx)];
      await selectSqlTable(t, false);
    });

    $("sqlTableList").addEventListener("contextmenu", (e) => {
      const item = e.target.closest("[data-table-idx]");
      if (!item) return;
      e.preventDefault();
      e.stopPropagation();
      const tblObj = state.sqlTables[Number(item.dataset.tableIdx)];
      if (!tblObj) return;
      const isView = String(tblObj.kind || "").toUpperCase().includes("VIEW");
      const items = [
        {
          icon: "📄",
          label: "Select Top Rows",
          onClick: () => selectSqlTable(tblObj, false),
        },
        {
          icon: "📋",
          label: "Show All Rows",
          onClick: () => selectSqlTable(tblObj, true),
        },
        { type: "sep" },
        {
          icon: "🗺️",
          label: "New Database Diagram",
          onClick: () => openDatabaseDiagramTab({ engine: state.dbEngine, db: state.sqlDb, focusTable: tblObj.name }),
        },
        {
          icon: "📤",
          label: "Export Table...",
          onClick: () => {
            state.sqlTable = tblObj.name;
            openExportModal("single");
          },
        },
      ];
      if (!isView) {
        items.push({ type: "sep" });
        items.push({
          icon: "🗑️",
          label: "Drop Table...",
          danger: true,
          onClick: () => confirmAndDropTable(tblObj.name),
        });
      }
      showDbContextMenu(e.clientX, e.clientY, `${isView ? "View" : "Table"}: ${tblObj.name}`, items);
    });

    if ($("btnNewSqlTab")) {
      $("btnNewSqlTab").onclick = () => openNewSqlTab({ focus: true });
    }
    if ($("btnCloseAllSqlTabs")) {
      $("btnCloseAllSqlTabs").onclick = () => closeAllSqlTabs();
    }
    if ($("sqlTabList")) {
      $("sqlTabList").addEventListener("click", (e) => {
        if (suppressNextSqlTabClick) {
          suppressNextSqlTabClick = false;
          return;
        }
        const popBtn = e.target.closest("[data-pop-sqltab]");
        if (popBtn) {
          e.stopPropagation();
          detachSqlTab(popBtn.dataset.popSqltab);
          return;
        }
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
      $("sqlTabList").addEventListener("pointerdown", (e) => {
        if (e.button !== 0 || e.target.closest("[data-close-sqltab], [data-pop-sqltab]")) return;
        const tabEl = e.target.closest("[data-sqltab-id]");
        if (!tabEl) return;
        const tabId = tabEl.dataset.sqltabId;
        const startX = e.clientX;
        const startY = e.clientY;
        let dragging = false;
        let tornOut = false;

        const onMove = (ev) => {
          if (tornOut) return;
          const dx = ev.clientX - startX;
          const dy = ev.clientY - startY;
          if (!dragging && Math.hypot(dx, dy) < 6) return;
          dragging = true;
          suppressNextSqlTabClick = true;
          const liveEl = $("sqlTabList")?.querySelector(`[data-sqltab-id="${tabId}"]`);
          liveEl?.classList.add("is-dragging");

          const barRect = $("sqlTabBar")?.getBoundingClientRect();
          if (barRect && (ev.clientY > barRect.bottom + 28 || ev.clientY < barRect.top - 24)) {
            tornOut = true;
            cleanup();
            const winEl = detachSqlTab(tabId, { clientX: ev.clientX, clientY: ev.clientY });
            const detachedTab = getSqlTab(tabId);
            if (winEl && detachedTab) {
              startFloatingWinDrag(winEl, detachedTab, ev.clientX, ev.clientY);
            }
            return;
          }

          // Reorder horizontally inside the tab strip
          const sibs = Array.from($("sqlTabList")?.querySelectorAll(".sql-tab") || []);
          for (const sib of sibs) {
            const sibId = sib.dataset.sqltabId;
            if (!sibId || sibId === tabId) continue;
            const r = sib.getBoundingClientRect();
            const mid = r.left + r.width / 2;
            const curIdx = state.sqlTabs.findIndex((t) => t.id === tabId);
            const sibIdx = state.sqlTabs.findIndex((t) => t.id === sibId);
            if (curIdx < 0 || sibIdx < 0) continue;
            if ((curIdx < sibIdx && ev.clientX > mid) || (curIdx > sibIdx && ev.clientX < mid)) {
              const [moved] = state.sqlTabs.splice(curIdx, 1);
              const nextSibIdx = state.sqlTabs.findIndex((t) => t.id === sibId);
              state.sqlTabs.splice(curIdx < sibIdx ? nextSibIdx + 1 : nextSibIdx, 0, moved);
              renderSqlTabs();
              $("sqlTabList")?.querySelector(`[data-sqltab-id="${tabId}"]`)?.classList.add("is-dragging");
              break;
            }
          }
        };

        const cleanup = () => {
          window.removeEventListener("pointermove", onMove);
          window.removeEventListener("pointerup", onUp);
          window.removeEventListener("pointercancel", onUp);
          $("sqlTabList")?.querySelectorAll(".sql-tab.is-dragging").forEach((el) => el.classList.remove("is-dragging"));
        };

        const onUp = () => {
          cleanup();
          if (dragging) setTimeout(() => { suppressNextSqlTabClick = false; }, 30);
        };

        window.addEventListener("pointermove", onMove);
        window.addEventListener("pointerup", onUp);
        window.addEventListener("pointercancel", onUp);
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

    $("btnSqlRun").onclick = () => {
      closeSqlSuggest();
      runSqlQuery(false);
    };
    $("btnSqlClear").onclick = () => {
      closeSqlSuggest();
      $("sqlEditor").value = "";
      const tab = getActiveSqlTab();
      if (tab) tab.sql = "";
      $("sqlEditor").focus();
    };

    // SQL Editor IDE-style Tab Autocomplete & Snippet Suggestions (CREATE, SHOW, SELECT, Tables, Columns)
    const mainSqlSuggest = attachSqlAutocomplete({
      textarea: $("sqlEditor"),
      menu: $("sqlSuggestMenu"),
      stage: $("sqlEditorStage"),
      getTab: getActiveSqlTab,
      onRun: () => runSqlQuery(false),
    });
    const closeSqlSuggest = () => mainSqlSuggest.close();

    // New DB modal
    const closeNewDb = () => ($("modalNewDb").hidden = true);
    $("btnNewDb").onclick = () => {
      const s = getStatus(state.dbEngine);
      $("modalNewDbTitle").textContent = `Create ${s ? s.name : ""} Database`;
      $("formNewDb").reset();
      $("newDbPass").type = "password";
      if ($("btnToggleNewDbPass")) {
        $("btnToggleNewDbPass").textContent = "👁️";
        $("btnToggleNewDbPass").title = "Show password";
      }
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
      if (e.defaultPrevented || e.shiftKey) return;
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

    const closeTopModal = () => {
      if ($("modalConfirm") && !$("modalConfirm").hidden) { closeConfirm(false); return true; }
      if ($("modalSyncLogs") && !$("modalSyncLogs").hidden) { $("modalSyncLogs").hidden = true; return true; }
      if ($("modalAutoSyncRun") && !$("modalAutoSyncRun").hidden) { $("btnCloseAutoSyncRun")?.click(); return true; }
      if ($("modalAutoBackupRun") && !$("modalAutoBackupRun").hidden) { $("btnCloseAutoBackupRun")?.click(); return true; }
      if ($("modalScheduleSnapshots") && !$("modalScheduleSnapshots").hidden) { $("modalScheduleSnapshots").hidden = true; return true; }
      if ($("modalAutoSyncForm") && !$("modalAutoSyncForm").hidden) { $("modalAutoSyncForm").hidden = true; return true; }
      if ($("modalAutoBackupForm") && !$("modalAutoBackupForm").hidden) { $("modalAutoBackupForm").hidden = true; return true; }
      if ($("modalBackupProgress") && !$("modalBackupProgress").hidden) { $("modalBackupProgress").hidden = true; return true; }
      if ($("modalCloudSync") && !$("modalCloudSync").hidden) { $("btnCloseCloudSync")?.click(); return true; }
      if ($("modalCloudForm") && !$("modalCloudForm").hidden) { $("modalCloudForm").hidden = true; return true; }
      if ($("modalSysUpdater") && !$("modalSysUpdater").hidden) { closeSysUpdater(); return true; }
      if ($("modalRepair") && !$("modalRepair").hidden) { closeRepair(); return true; }
      if ($("modalNewTable") && !$("modalNewTable").hidden) { closeNewTable(); return true; }
      if ($("modalExport") && !$("modalExport").hidden) { closeExport(); return true; }
      if ($("modalRowForm") && !$("modalRowForm").hidden) { $("modalRowForm").hidden = true; return true; }
      if ($("modalRowShow") && !$("modalRowShow").hidden) { $("modalRowShow").hidden = true; return true; }
      if ($("modalRestore") && !$("modalRestore").hidden) { closeRestore(); return true; }
      if ($("modalManual") && !$("modalManual").hidden) { closeManual(); return true; }
      if ($("modalNewDb") && !$("modalNewDb").hidden) { closeNewDb(); return true; }
      return false;
    };

    const TERM_MODES = ["shell", "mariadb", "mysql", "postgresql", "mssql", "redis"];

    if (window.DariusShortcutsGuide?.init) {
      window.DariusShortcutsGuide.init({
        getTab: () => state.tab,
        switchTab,
        openFolder: (kind) => api().OpenFolder(kind).catch((e) => toast(errMsg(e), "err")),
        copyText,
        toast,
        clickBtn: (id) => {
          const el = $(id);
          if (!el || el.hidden || el.disabled) return false;
          if ((id === "btnPagePrev" || id === "btnPageNext") && $("sqlPagerBar")?.hidden) return false;
          el.click();
          return true;
        },
        isAnyOtherModalOpen: (excludeId) =>
          Array.from(document.querySelectorAll(".modal-backdrop")).some((m) => m.id !== excludeId && !m.hidden),
        closeTopModal,
        focusTerminal: () => {
          switchTab("terminal");
          setTimeout(() => $("termInput")?.focus(), 20);
        },
        clearTerminal: () => {
          $("btnTermClear")?.click();
        },
        cycleTermMode: (delta) => {
          const idx = Math.max(0, TERM_MODES.indexOf(state.termState?.mode || "shell"));
          const next = TERM_MODES[(idx + delta + TERM_MODES.length) % TERM_MODES.length];
          switchTermMode(next);
        },
        runSqlQuery: () => runSqlQuery(false),
        openNewSqlTab: () => openNewSqlTab({ focus: true }),
        closeActiveSqlTab: () => {
          if (state.activeSqlTabId) closeSqlTab(state.activeSqlTabId);
        },
        cycleSqlTab: (delta) => {
          const tabs = engineTabs(state.dbEngine);
          if (tabs.length < 2) return;
          const idx = Math.max(0, tabs.findIndex((t) => t.id === state.activeSqlTabId));
          const next = tabs[(idx + delta + tabs.length) % tabs.length];
          if (!next) return;
          syncActiveTabFromDom();
          applyTabToDom(next, { syncSidebar: true });
        },
        focusDbSearch: () => {
          if ($("sqlBrowser") && !$("sqlBrowser").hidden) {
            const flt = $("sqlRowFilter");
            if (flt && !flt.hidden) {
              flt.focus();
              flt.select?.();
              return true;
            }
            const ed = $("sqlEditor");
            if (ed) {
              ed.focus();
              return true;
            }
          } else if ($("redisBrowser") && !$("redisBrowser").hidden) {
            const pat = $("redisPattern");
            if (pat) {
              pat.focus();
              pat.select?.();
              return true;
            }
          }
          return false;
        },
        focusGuideSearch: () => {
          const inp = $("guideSearch");
          if (inp) {
            inp.focus();
            inp.select?.();
          }
        },
        toggleConfigLogMode: () => {
          state.cfgMode = state.cfgMode === "config" ? "logs" : "config";
          loadCfgOrLog();
        },
        saveConfig: (restart) => {
          if (state.cfgMode !== "config") return;
          if (restart) {
            const btn = $("btnCfgSaveRestart");
            if (btn && !btn.hidden && !btn.disabled) {
              saveConfigFile(true);
              return;
            }
          }
          const saveBtn = $("btnCfgSave");
          if (saveBtn && !saveBtn.disabled) saveConfigFile(false);
        },
        saveSettings: () => saveSettingsForm(),
        refreshCurrentView: async () => {
          try {
            if (state.tab === "dashboard") {
              state.statuses = (await api().Status()) || [];
              renderDashboard();
              toast("Refreshed service statuses", "info");
            } else if (state.tab === "database") {
              await loadDbView(true);
            } else if (state.tab === "cloud") {
              const [cloudProfiles, schedules, winTask] = await Promise.all([
                api().CloudProfiles().catch(() => state.cloudProfiles),
                api().Schedules().catch(() => state.schedules),
                api().WindowsTaskStatus().catch(() => state.winTask),
              ]);
              state.cloudProfiles = cloudProfiles || [];
              state.schedules = schedules || [];
              state.winTask = winTask || state.winTask;
              updateCloudBadge();
              renderCloudView();
              toast("Refreshed cloud profiles & schedules", "info");
            } else if (state.tab === "versions") {
              const btn = $("btnRefreshCatalog");
              if (btn && !btn.disabled) btn.click();
            } else if (state.tab === "config") {
              if (state.cfgMode === "config") await loadConfigFile();
              else await loadLogFile();
            } else if (state.tab === "settings") {
              await loadSettingsForm();
              toast("Reloaded settings", "info");
            } else if (state.tab === "guide") {
              window.DariusShortcutsGuide?.onTabActivated?.("guide");
            }
          } catch (e) {
            toast(errMsg(e), "err");
          }
        },
      });
    } else {
      window.addEventListener("keydown", (e) => {
        if (e.key === "Escape") closeTopModal();
      });
    }
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
    if ($("guideVersionBadge")) {
      $("guideVersionBadge").textContent = "Darius DB · Lite Version " + info.version;
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
        const syncScheds = (state.schedules || []).filter((s) => s.cloudId === c.id && s.enabled && s.autoSync);
        const bkpScheds = (state.schedules || []).filter((s) => s.cloudId === c.id && s.enabled && s.autoBackup);
        const autoSyncPill = syncScheds.length
          ? `<span class="pill running" title="Daily Auto-Sync @ ${esc(syncScheds[0].syncTime || "02:00")}">🔄 Auto-Sync @ ${esc(syncScheds[0].syncTime || "02:00")}</span>`
          : "";
        const autoBkpPill = bkpScheds.length
          ? `<span class="pill accent" title="Daily Auto-Backup @ ${esc(bkpScheds[0].backupTime || "02:00")} · Purge >= ${bkpScheds[0].retentionDays || 7}d">📦 Auto-Backup ${bkpScheds[0].retentionDays || 7}d</span>`
          : "";
        return `<div class="cloud-card" data-cid="${esc(c.id)}">
          <div class="cloud-card-head">
            <div>
              <strong style="font-size:14px">${esc(c.name)}</strong>
              <div style="display:flex;gap:5px;margin-top:4px;flex-wrap:wrap">
                <span class="pill accent">${esc(providerLabel)}</span>
                <span class="pill">${esc(engineLabel)}</span>
                ${c.sslMode && c.sslMode !== "disable" ? `<span class="pill">TLS: ${esc(c.sslMode)}</span>` : ""}
                ${autoSyncPill}
                ${autoBkpPill}
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
            <button type="button" class="btn xs" data-cact="sync" title="Open one-time Manual Sync modal">🔄 Sync</button>
            ${c.panelUrl ? `<button type="button" class="btn xs" data-cact="panel">🌐 Panel ↗</button>` : ""}
            <button type="button" class="btn xs ghost" data-cact="uri">Copy URI</button>
            <button type="button" class="btn xs ghost" data-cact="edit">Edit</button>
            <button type="button" class="btn xs danger-ghost" data-cact="del">✕</button>
          </div>
        </div>`;
      })
      .join("");
  }

  const liveScheduledRuns = {};

  const GUIDE_DAYS = ["Sun", "Mon", "Tue", "Wed", "Thu", "Fri", "Sat"];
  const GUIDE_MONTHS = ["Jan", "Feb", "Mar", "Apr", "May", "Jun", "Jul", "Aug", "Sep", "Oct", "Nov", "Dec"];

  function formatPurgeDateGuide(dt, includeSeconds = true) {
    const d = dt instanceof Date ? dt : new Date(dt);
    if (isNaN(d.getTime())) return "—";
    const pad2 = (n) => String(n).padStart(2, "0");
    const day = GUIDE_DAYS[d.getDay()];
    const mon = GUIDE_MONTHS[d.getMonth()];
    const dd = pad2(d.getDate());
    const yyyy = d.getFullYear();
    const h24 = d.getHours();
    const h12 = pad2(h24 % 12 || 12);
    const mm = pad2(d.getMinutes());
    const ss = pad2(d.getSeconds());
    const ampm = h24 >= 12 ? "PM" : "AM";
    return `${day}, ${mon} ${dd}, ${yyyy} · ${h12}:${mm}${includeSeconds ? ":" + ss : ""} ${ampm}`;
  }

  function formatPurgeStamp(dt) {
    const d = dt instanceof Date ? dt : new Date(dt);
    if (isNaN(d.getTime())) return "—";
    const pad2 = (n) => String(n).padStart(2, "0");
    return `${d.getFullYear()}_${pad2(d.getMonth() + 1)}_${pad2(d.getDate())}_${GUIDE_DAYS[d.getDay()]}_${pad2(d.getHours())}${pad2(d.getMinutes())}${pad2(d.getSeconds())}`;
  }

  function computeSchedulePurgeGuide(sched, now = new Date()) {
    const retDays = Math.max(1, Number(sched?.retentionDays) || 7);
    const cutoff = new Date(now.getTime() - retDays * 86400000);
    let baseDt = null;
    let sourceLabel = "Next backup";
    if (sched?.lastBackupAt) {
      const parsed = new Date(String(sched.lastBackupAt).replace(" ", "T"));
      if (!isNaN(parsed.getTime())) {
        baseDt = parsed;
        sourceLabel = "Last backup";
      }
    }
    if (!baseDt) {
      const m = /^(\d{1,2}):(\d{2})$/.exec(String(sched?.backupTime || "02:00").trim());
      baseDt = new Date(now.getFullYear(), now.getMonth(), now.getDate(), m ? Number(m[1]) : 2, m ? Number(m[2]) : 0, 0, 0);
      if (baseDt <= now) baseDt = new Date(baseDt.getTime() + 86400000);
    }
    const purgeAt = new Date(baseDt.getTime() + retDays * 86400000);
    return {
      retDays,
      sourceLabel,
      baseDt,
      purgeAt,
      cutoff,
      purgeAtText: formatPurgeDateGuide(purgeAt, false),
      purgeStamp: formatPurgeStamp(purgeAt),
      cutoffText: formatPurgeDateGuide(cutoff, false),
    };
  }

  function computeNextScheduleText(hhmm, lastDate, createdAt) {
    const raw = String(hhmm || "02:00").trim();
    const m = /^(\d{1,2}):(\d{2})$/.exec(raw);
    if (!m) return raw;
    const hh = Number(m[1]);
    const mm = Number(m[2]);
    const now = new Date();
    const pad = (n) => String(n).padStart(2, "0");
    const today = `${now.getFullYear()}-${pad(now.getMonth() + 1)}-${pad(now.getDate())}`;
    const target = new Date(now.getFullYear(), now.getMonth(), now.getDate(), hh, mm, 0, 0);
    const label12 = `${hh % 12 || 12}:${pad(mm)} ${hh >= 12 ? "PM" : "AM"}`;
    if (lastDate === today) {
      return `Tomorrow ${label12} (today's scheduled run done)`;
    }
    if (now < target) {
      return `Today ${label12}`;
    }
    if (!lastDate && createdAt && String(createdAt).startsWith(today)) {
      const ct = new Date(String(createdAt).replace(" ", "T"));
      if (!isNaN(ct.getTime()) && ct > target) {
        return `Tomorrow ${label12}`;
      }
    }
    return `Due now (${label12})`;
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
    const syncGrid = $("cloudAutoSyncGrid");
    const syncList = (state.schedules || []).filter((s) => s.autoSync);
    if (syncGrid) {
      if (!syncList.length) {
        syncGrid.innerHTML = `<div class="empty-note" style="grid-column:1 / -1;padding:18px">
          No Daily Auto-Sync schedules yet. Click <strong>+ New Auto-Sync</strong> to automatically pull <strong>Cloud → Local DB</strong>, export to a <strong>Generated Folder (No Local DB)</strong>, or push <strong>Local → Cloud</strong> every day.
        </div>`;
      } else {
        syncGrid.innerHTML = syncList
          .map((s) => {
            const cloud = (state.cloudProfiles || []).find((p) => p.id === s.cloudId);
            const dirLabel =
              s.syncDirection === "push"
                ? "Push Local → Cloud"
                : s.syncDirection === "pull_folder"
                  ? "Pull Cloud → Folder"
                  : "Pull Cloud → Local DB";
            const syncBadge = `<span class="pill accent">${s.syncDirection === "pull_folder" ? "📁" : "🔄"} ${dirLabel} @ ${esc(s.syncTime || "02:00")}</span>`;
            const autoNameBadge = s.syncDirection !== "push" && s.autoNameLocalDb
              ? `<span class="pill">✨ ${s.syncDirection === "pull_folder" ? "Auto-folder" : "Auto-named"}</span>`
              : "";
            const live = liveScheduledRuns[s.id];
            const isLiveRunning = !!(live && live.state === "running" && live.mode !== "backup");
            const statusPill = isLiveRunning
              ? `<span class="pill accent">⏳ Running ${Math.round(live.percent || 0)}%</span>`
              : s.enabled
                ? `<span class="pill running">Active</span>`
                : `<span class="pill">Paused</span>`;
            const syncIsErr = /^err\b/i.test(String(s.lastSyncStatus || "").trim());
            const syncIsOk = !!s.lastSyncAt && !syncIsErr;
            const syncMsgText = s.lastSyncAt ? String(s.lastSyncStatus || "ok") : "Pending next scheduled time";
            const cloudLabel = cloud ? `${cloud.name} (${s.cloudDb || cloud.database || ""})` : (s.cloudDb || "Cloud not set");
            const localLabel = `${(s.localEngine || "mssql").toUpperCase()}/${s.localDb || (s.autoNameLocalDb ? (s.cloudDb || "auto") : "-")}${s.autoNameLocalDb && s.syncDirection !== "push" ? "_<date_time>" : ""}`;
            const fromText = s.syncDirection === "push" ? localLabel : cloudLabel;
            const targetText =
              s.syncDirection === "pull_folder"
                ? `📁 ${s.syncFolder || "backups/" + s.name + "/syncs"}`
                : s.syncDirection === "push"
                  ? cloudLabel
                  : localLabel;
            const liveHtml = isLiveRunning
              ? `<div class="sched-live-box">
                  <div class="sched-live-top">
                    <span>⏰ ${esc(live.stage || "Auto-Sync running...")}</span>
                    <span style="font-family:var(--font-mono)">${Math.round(live.percent || 0)}%</span>
                  </div>
                  <div class="progress-bar" style="height:5px;margin:5px 0 3px">
                    <div class="progress-fill" style="width:${Math.max(2, Math.min(100, Math.round(live.percent || 0)))}%"></div>
                  </div>
                  <div class="muted" style="font-size:11px;overflow-wrap:anywhere;word-break:break-word">${esc(live.detail || "")}</div>
                </div>`
              : "";
            return `<div class="cloud-card sched-card" data-sid="${esc(s.id)}">
              <div class="cloud-card-head">
                <div>
                  <strong style="font-size:14px">${esc(s.name)}</strong>
                  <div style="display:flex;gap:5px;margin-top:5px;flex-wrap:wrap">
                    ${syncBadge}
                    ${autoNameBadge}
                  </div>
                </div>
                ${statusPill}
              </div>
              <div class="sched-route-box">
                <div class="sched-route-row">
                  <span class="sched-route-k">From</span>
                  <span class="sched-route-v" title="${esc(fromText)}">${esc(fromText)}</span>
                </div>
                <div class="sched-route-row">
                  <span class="sched-route-k">Target</span>
                  <span class="sched-route-v" title="${esc(targetText)}">${esc(targetText)}</span>
                </div>
              </div>
              ${
                s.enabled
                  ? `<div class="sched-timing-panel"><div class="sched-timing-row"><span class="sched-timing-label">⏰ Next Auto-Sync</span><span class="sched-timing-val">${esc(computeNextScheduleText(s.syncTime, s.lastSyncDate, s.createdAt))}</span></div></div>`
                  : ""
              }
              ${liveHtml}
              <div class="sched-status-box">
                <div class="sched-status-item">
                  <div class="sched-status-head">
                    <span class="sched-status-tag"><span class="sched-status-dot${syncIsOk ? " ok" : syncIsErr ? " err" : ""}"></span>Last Sync</span>
                    <div class="sched-status-right">
                      <span class="sched-status-ts">${esc(s.lastSyncAt || "—")}</span>
                      ${s.lastSyncAt || s.lastSyncStatus ? `<button type="button" class="sched-status-clear${syncIsErr ? " is-err" : ""}" data-sact="clear-sync-status" aria-label="Clear last sync status" title="Clear Last Sync status">✕</button>` : ""}
                    </div>
                  </div>
                  <div class="sched-status-msg${syncIsErr ? " err" : ""}" title="${esc(syncMsgText)}">${esc(syncMsgText)}</div>
                </div>
              </div>
              <div class="sched-card-footer">
                <div class="sched-actions-main">
                  <button type="button" class="btn xs primary" data-sact="run-sync">▶ Run Sync</button>
                  <button type="button" class="btn xs accent" data-sact="logs" title="View Sync trace log for this schedule">📋 Logs</button>
                  ${s.syncDirection === "pull_folder" ? `<button type="button" class="btn xs" data-sact="folder-sync">📁 Folder</button>` : ""}
                </div>
                <div class="sched-actions-util">
                  <button type="button" class="btn xs ghost" data-sact="toggle-sync">${s.enabled ? "Pause" : "Enable"}</button>
                  <button type="button" class="btn xs ghost" data-sact="edit-sync">Edit</button>
                  <button type="button" class="btn xs danger-ghost" data-sact="del-sync" aria-label="Delete auto-sync schedule" title="Delete Auto-Sync schedule">✕</button>
                </div>
              </div>
            </div>`;
          })
          .join("");
      }
    }

    const bkpGrid = $("cloudAutoBackupGrid");
    const bkpList = (state.schedules || []).filter((s) => s.autoBackup);
    if (bkpGrid) {
      if (!bkpList.length) {
        bkpGrid.innerHTML = `<div class="empty-note" style="grid-column:1 / -1;padding:18px">
          No Daily Auto-Backup schedules yet. Click <strong>+ New Auto-Backup</strong> to save daily <strong>.zip</strong> snapshots or <strong>dated folders</strong> at 2:00 AM with automatic <strong>7-day (1 week) purge</strong>.
        </div>`;
      } else {
        bkpGrid.innerHTML = bkpList
          .map((s) => {
            const cloud = (state.cloudProfiles || []).find((p) => p.id === s.cloudId);
            const backupBadge = `<span class="pill">${s.backupSource === "cloud_folder" ? "📁 Folder" : "📦 .zip"} @ ${esc(s.backupTime || "02:00")} · Purge ≥ ${s.retentionDays || 7}d${s.autoNameBackup ? (s.backupSource === "cloud_folder" ? " · ✨ Auto-folder" : " · ✨ Auto-named") : ""}</span>`;
            const live = liveScheduledRuns[s.id];
            const isLiveRunning = !!(live && live.state === "running" && live.mode !== "sync");
            const statusPill = isLiveRunning
              ? `<span class="pill accent">⏳ Running ${Math.round(live.percent || 0)}%</span>`
              : s.enabled
                ? `<span class="pill running">Active</span>`
                : `<span class="pill">Paused</span>`;
            const purgeGuide = computeSchedulePurgeGuide(s);
            const bkpIsErr = /^err\b/i.test(String(s.lastBackupStatus || "").trim());
            const bkpIsOk = !!s.lastBackupAt && !bkpIsErr;
            const bkpMsgText = s.lastBackupAt ? String(s.lastBackupStatus || "ok") : "Pending next scheduled run";

            const cloudDesc = cloud ? `☁️ ${cloud.name} (${s.cloudDb || cloud.database || ""})` : (s.cloudDb ? `☁️ ${s.cloudDb}` : "☁️ Cloud");
            const localDesc = `💻 ${(s.localEngine || "mssql").toUpperCase()}/${s.localDb || "-"}`;
            const sourceDesc =
              s.backupSource === "local"
                ? localDesc
                : s.backupSource === "both"
                  ? `${cloudDesc} + ${localDesc}`
                  : cloudDesc;
            const folderDesc = `📁 ${s.backupDir || "backups/" + s.name}`;

            const liveHtml = isLiveRunning
              ? `<div class="sched-live-box">
                  <div class="sched-live-top">
                    <span>⏰ ${esc(live.stage || "Auto-Backup running...")}</span>
                    <span style="font-family:var(--font-mono)">${Math.round(live.percent || 0)}%</span>
                  </div>
                  <div class="progress-bar" style="height:5px;margin:5px 0 3px">
                    <div class="progress-fill" style="width:${Math.max(2, Math.min(100, Math.round(live.percent || 0)))}%"></div>
                  </div>
                  <div class="muted" style="font-size:11px;overflow-wrap:anywhere;word-break:break-word">${esc(live.detail || "")}</div>
                </div>`
              : "";

            return `<div class="cloud-card sched-card" data-sid="${esc(s.id)}">
              <div class="cloud-card-head">
                <div>
                  <strong style="font-size:14px">${esc(s.name)}</strong>
                  <div style="display:flex;gap:5px;margin-top:5px;flex-wrap:wrap">
                    ${backupBadge}
                  </div>
                </div>
                ${statusPill}
              </div>
              <div class="sched-route-box">
                <div class="sched-route-row">
                  <span class="sched-route-k">Source</span>
                  <span class="sched-route-v" title="${esc(sourceDesc)}">${esc(sourceDesc)}</span>
                </div>
                <div class="sched-route-row">
                  <span class="sched-route-k">Folder</span>
                  <span class="sched-route-v" title="${esc(folderDesc)}">${esc(folderDesc)}</span>
                </div>
              </div>
              <div class="sched-timing-panel">
                ${
                  s.enabled
                    ? `<div class="sched-timing-row"><span class="sched-timing-label">📦 Next Auto-Backup</span><span class="sched-timing-val">${esc(computeNextScheduleText(s.backupTime, s.lastBackupDate, s.createdAt))}</span></div>`
                    : ""
                }
                <div class="sched-purge-line" title="Purge stamp: ${esc(purgeGuide.purgeStamp)} · Cutoff now: files on/before ${esc(purgeGuide.cutoffText)}">
                  <span>🗑 Purge guide (≥${purgeGuide.retDays}d · ${esc(purgeGuide.sourceLabel)})</span>
                  <span class="sched-purge-val-group"><strong>${esc(purgeGuide.purgeAtText)}</strong><code class="purge-stamp-tag">${esc(purgeGuide.purgeStamp)}</code></span>
                </div>
              </div>
              ${liveHtml}
              <div class="sched-status-box">
                <div class="sched-status-item">
                  <div class="sched-status-head">
                    <span class="sched-status-tag"><span class="sched-status-dot${bkpIsOk ? " ok" : bkpIsErr ? " err" : ""}"></span>Last Backup</span>
                    <div class="sched-status-right">
                      <span class="sched-status-ts">${esc(s.lastBackupAt || "—")}</span>
                      ${s.lastBackupAt || s.lastBackupStatus ? `<button type="button" class="sched-status-clear${bkpIsErr ? " is-err" : ""}" data-sact="clear-backup-status" aria-label="Clear last backup status" title="Clear Last Backup status">✕</button>` : ""}
                    </div>
                  </div>
                  <div class="sched-status-msg${bkpIsErr ? " err" : ""}" title="${esc(bkpMsgText)}">${esc(bkpMsgText)}</div>
                </div>
              </div>
              <div class="sched-card-footer">
                <div class="sched-actions-main">
                  <button type="button" class="btn xs primary" data-sact="run-backup">▶ Backup Now</button>
                  <button type="button" class="btn xs accent" data-sact="snaps">📦 Snapshots</button>
                  <button type="button" class="btn xs" data-sact="purge" title="Purge snapshots >= ${s.retentionDays || 7} days old">🗑 Purge</button>
                  <button type="button" class="btn xs" data-sact="folder-backup">📁 Folder</button>
                  <button type="button" class="btn xs accent" data-sact="logs" title="View Backup &amp; Purge trace log">📋 Logs</button>
                </div>
                <div class="sched-actions-util">
                  <button type="button" class="btn xs ghost" data-sact="toggle-backup">${s.enabled ? "Pause" : "Enable"}</button>
                  <button type="button" class="btn xs ghost" data-sact="edit-backup">Edit</button>
                  <button type="button" class="btn xs danger-ghost" data-sact="del-backup" aria-label="Delete auto-backup schedule" title="Delete Auto-Backup schedule">✕</button>
                </div>
              </div>
            </div>`;
          })
          .join("");
      }
    }
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
    let autoSyncRun = null;
    let autoBkpRun = null;

    const updateFloatingStackBodyClass = () => {
      const stack = $("floatingProgressStack");
      const syncVis = !$("syncFloatingBar")?.hidden;
      const autoSyncVis = !$("autoSyncFloatingBar")?.hidden;
      const autoBkpVis = !$("autoBkpFloatingBar")?.hidden;
      const anyVisible = syncVis || autoSyncVis || autoBkpVis;
      if (stack) stack.hidden = !anyVisible;
      document.body.classList.toggle("has-sync-floating", !!anyVisible);

      const runningCount =
        (syncVis && syncJob.running ? 1 : 0) +
        (autoSyncVis && autoSyncRun?.st?.running ? 1 : 0) +
        (autoBkpVis && autoBkpRun?.st?.running ? 1 : 0);
      const visibleCount = (syncVis ? 1 : 0) + (autoSyncVis ? 1 : 0) + (autoBkpVis ? 1 : 0);
      const finishedCount = Math.max(0, visibleCount - runningCount);

      const sumEl = $("floatStackSummary");
      if (sumEl) {
        if (runningCount > 0 && finishedCount > 0) {
          sumEl.textContent = `${runningCount} running · ${finishedCount} done`;
          sumEl.className = "pill accent";
        } else if (runningCount > 0) {
          sumEl.textContent = `${runningCount} running`;
          sumEl.className = "pill accent";
        } else if (finishedCount > 0) {
          sumEl.textContent = `${finishedCount} finished`;
          sumEl.className = "pill";
        } else {
          sumEl.textContent = "0 active";
          sumEl.className = "pill";
        }
      }
      if ($("btnClearFinishedFloats")) {
        $("btnClearFinishedFloats").hidden = finishedCount === 0;
      }
    };

    const updateFloatingSync = () => {
      const bar = $("syncFloatingBar");
      if (!bar) return;
      const show = !!syncJob.minimized;
      bar.hidden = !show;
      if (show) {
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
        const fclose = $("btnFloatCloseSync");
        if (fclose) {
          fclose.hidden = !!syncJob.running;
        }
      }
      updateFloatingStackBodyClass();
    };

    const setSyncFormLocked = (locked) => {
      [
        "syncLocalEngine",
        "syncLocalDb",
        "btnQuickGenLocalDb",
        "syncCloudProfile",
        "syncCloudDb",
        "syncAutoNameChk",
        "syncAutoNameBase",
        "syncAutoNameFmt",
        "btnRefreshSyncAutoName",
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
      $("cloudPass").type = "password";
      if ($("btnToggleCloudPass")) {
        $("btnToggleCloudPass").textContent = "👁️";
        $("btnToggleCloudPass").title = "Show password";
      }
      $("cloudEditId").value = existing?.id || "";
      $("cloudUriInput").value = "";
      $("cloudTestStatus").hidden = true;
      $("cloudTestStatus").classList.remove("success", "error");
      if ($("cloudTestBar")) $("cloudTestBar").style.width = "0%";
      if ($("cloudTestPct")) $("cloudTestPct").textContent = "0%";
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
        if ($("cloudPanelUrl") && parsed.panelUrl) $("cloudPanelUrl").value = parsed.panelUrl;
        const extra = parsed.panelUrl ? " (cPanel web port converted to MySQL 3306)" : "";
        toast(`Parsed ${parsed.engine.toUpperCase()} connection (${parsed.host}:${parsed.port})${extra}`, "ok");
      } catch (err) {
        toast(errMsg(err), "err");
      }
    };

    const setCloudTestProgress = (percent, mode = "running", title = "", detail = "") => {
      const pct = Math.max(0, Math.min(100, Math.round(Number(percent) || 0)));
      const box = $("cloudTestStatus");
      if (box) {
        box.hidden = false;
        box.classList.remove("success", "error");
        if (mode === "done" || mode === "success") box.classList.add("success");
        else if (mode === "error") box.classList.add("error");
      }
      if ($("cloudTestSpinner")) $("cloudTestSpinner").hidden = mode !== "running";
      if ($("cloudTestTitle") && title) $("cloudTestTitle").textContent = title;
      if ($("cloudTestPct")) $("cloudTestPct").textContent = `${pct}%`;
      if ($("cloudTestBar")) $("cloudTestBar").style.width = `${pct}%`;
      if ($("cloudTestSub")) {
        const sub = $("cloudTestSub");
        const raw = detail || "";
        const m1045 = raw.match(/Access denied for user\s+['"]?([^'"@]+)['"]?@['"]?([^'"\s)]+)['"]?/i);
        const portVal = Number($("cloudPort")?.value) || 0;
        const isCpanelPort = portVal === 2083 || portVal === 2082 || portVal === 2086 || portVal === 2087 || raw.includes("2083") || raw.includes("2082");
        const isTimeout = /i\/o timeout|timed out/i.test(raw);

        if (mode === "error" && isCpanelPort) {
          sub.innerHTML = `<div style="color:var(--red);font-weight:600;margin-bottom:6px">${esc(raw)}</div>
            <div style="padding:8px 10px;background:var(--bg-card);border:1px solid var(--border);border-radius:6px;line-height:1.45;color:var(--text)">
              <strong style="color:var(--warn)">⚠️ Port ${portVal || 2083} is the cPanel Web Login, NOT MySQL!</strong><br>
              cPanel web login uses HTTPS port 2083. Remote MySQL databases always listen on port <strong>3306</strong>.<br>
              <button type="button" class="btn xs accent" style="margin-top:6px" onclick="window.__switchPort && window.__switchPort(3306)">Switch Port to 3306 &amp; Re-Test</button>
            </div>`;
        } else if (mode === "error" && m1045) {
          const user = m1045[1];
          const clientIp = m1045[2];
          sub.innerHTML = `<div style="color:var(--red);font-weight:600;margin-bottom:6px">${esc(raw)}</div>
            <div style="padding:8px 10px;background:var(--bg-card);border:1px solid var(--border);border-radius:6px;line-height:1.45;color:var(--text)">
              <strong style="color:var(--accent)">💡 How to Fix cPanel Error 1045 (Access Denied):</strong>
              <ol style="margin:4px 0 0;padding-left:18px;display:flex;flex-direction:column;gap:4px">
                <li>Log in to your cPanel dashboard and open <strong>Remote MySQL®</strong>.</li>
                <li>Add <code style="font-weight:700;color:var(--accent)">%</code> (wildcard — <strong>recommended</strong> if your router/ISP switches public IPs) <button type="button" class="btn xs accent" style="padding:1px 6px;font-size:11px" onclick="window.__copyText && window.__copyText('%', 'Copied % wildcard to clipboard')">Copy %</button> or your current IP <code style="font-weight:700;color:var(--accent);user-select:all">${esc(clientIp)}</code> <button type="button" class="btn xs ghost" style="padding:1px 6px;font-size:11px" onclick="window.__copyText && window.__copyText('${esc(clientIp)}', 'Copied ${esc(clientIp)} to clipboard')">Copy IP</button> to allowed hosts.</li>
                <li>Under cPanel → <strong>MySQL® Databases</strong>, ensure user <strong>${esc(user)}</strong> exists, click <strong>👁️ Show password</strong> above to verify password, and verify it is added to the database with <strong>ALL PRIVILEGES</strong>.</li>
              </ol>
            </div>`;
        } else if (mode === "error" && isTimeout) {
          sub.innerHTML = `<div style="color:var(--red);font-weight:600;margin-bottom:6px">${esc(raw)}</div>
            <div style="padding:8px 10px;background:var(--bg-card);border:1px solid var(--border);border-radius:6px;line-height:1.45;color:var(--text)">
              <strong style="color:var(--warn)">⏱️ Connection Timed Out (Firewall / Network Block):</strong>
              <ol style="margin:4px 0 0;padding-left:18px;display:flex;flex-direction:column;gap:4px">
                <li><strong>cPanel / Hosting Firewall (CSF / cPHulk):</strong> Repeated failed logins (Error 1045) trigger a temporary IP block (usually 5–15 minutes). Wait a few minutes or check from cPanel.</li>
                <li><strong>Outbound Port Block:</strong> Confirm your ISP, router, or VPN allows outbound connections on port <strong>${esc(String(portVal || 3306))}</strong>.</li>
                <li>Verify <strong>Host</strong> and <strong>Port</strong> match your provider's remote database endpoint.</li>
              </ol>
            </div>`;
        } else {
          sub.textContent = raw;
        }
      }

      const formBtn = $("btnTestCloudForm");
      if (formBtn && formBtn.disabled && mode === "running") {
        formBtn.textContent = `⏳ Testing ${pct}%`;
      }
      const cardBtn = state.activeCloudTestBtn;
      if (cardBtn && cardBtn.isConnected && mode === "running") {
        cardBtn.textContent = `⏳ ${pct}%`;
      }
    };

    rt()?.EventsOn?.("cloud-test-progress", (p) => {
      if (!p) return;
      state.lastCloudTestPct = Math.max(state.lastCloudTestPct || 0, Number(p.percent) || 0);
      setCloudTestProgress(
        p.percent,
        p.state || "running",
        p.stage || "Testing connection...",
        p.detail || ""
      );
    });

    $("btnTestCloudForm").onclick = async () => {
      const btn = $("btnTestCloudForm");
      btn.disabled = true;
      btn.textContent = "⏳ Testing 5%";
      state.lastCloudTestPct = 5;
      const startedAt = Date.now();
      setCloudTestProgress(5, "running", "Starting connection test...", "Elapsed 0.0s — preparing network probe");
      const elapsedTimer = setInterval(() => {
        if (!btn.disabled) return;
        const secs = ((Date.now() - startedAt) / 1000).toFixed(1);
        const curPct = Number($("cloudTestPct")?.textContent?.replace("%", "")) || state.lastCloudTestPct || 10;
        const nudged = curPct < 88 ? Math.min(88, curPct + 2) : curPct;
        state.lastCloudTestPct = nudged;
        setCloudTestProgress(nudged, "running", $("cloudTestTitle")?.textContent || "Connecting...", `Elapsed ${secs}s — waiting for remote database response`);
      }, 450);
      try {
        const res = await api().TestCloudProfile(buildCloudObjFromForm());
        setCloudTestProgress(100, "done", `✅ Connected in ${res.latencyMs ?? 0} ms`, res.message || "Connection verified.");
        toast(res.message, "ok");
      } catch (err) {
        const secs = ((Date.now() - startedAt) / 1000).toFixed(1);
        setCloudTestProgress(100, "error", `⚠️ Connection test failed (${secs}s)`, errMsg(err));
        toast(errMsg(err), "err");
      } finally {
        clearInterval(elapsedTimer);
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

    window.__copyText = (text, msg) => {
      if (navigator.clipboard?.writeText) {
        navigator.clipboard.writeText(text).catch(() => {});
      }
      toast(msg || `Copied ${text}`, "ok");
    };
    window.__switchPort = (p) => {
      if ($("cloudPort")) $("cloudPort").value = p;
      if ($("btnTestCloudForm")) $("btnTestCloudForm").click();
    };

    const wirePasswordToggle = (btnId, inputId) => {
      const btn = $(btnId);
      const inp = $(inputId);
      if (!btn || !inp) return;
      btn.onclick = (e) => {
        e.preventDefault();
        e.stopPropagation();
        const isPass = inp.type === "password";
        inp.type = isPass ? "text" : "password";
        btn.textContent = isPass ? "🙈" : "👁️";
        btn.title = isPass ? "Hide password" : "Show password";
        btn.setAttribute("aria-label", btn.title);
        inp.focus();
      };
    };
    wirePasswordToggle("btnToggleCloudPass", "cloudPass");
    wirePasswordToggle("btnToggleNewDbPass", "newDbPass");

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

    const AUTO_PULL_DAYS = ["Sun", "Mon", "Tue", "Wed", "Thu", "Fri", "Sat"];
    const AUTO_PULL_MONTHS = ["Jan", "Feb", "Mar", "Apr", "May", "Jun", "Jul", "Aug", "Sep", "Oct", "Nov", "Dec"];
    const buildAutoPullLocalDbName = (baseName, format = "date_day_year_ts", now = new Date()) => {
      let clean = String(baseName || "")
        .trim()
        .replace(/[^A-Za-z0-9_-]+/g, "_")
        .replace(/^[_-]+|[_-]+$/g, "");
      if (!clean) clean = "pulled_db";
      if (/^[0-9]/.test(clean)) clean = "db_" + clean;

      const pad2 = (n) => String(n).padStart(2, "0");
      const yyyy = String(now.getFullYear());
      const mm = pad2(now.getMonth() + 1);
      const dd = pad2(now.getDate());
      const day = AUTO_PULL_DAYS[now.getDay()];
      const mon = AUTO_PULL_MONTHS[now.getMonth()];
      const hh = pad2(now.getHours());
      const mi = pad2(now.getMinutes());
      const ss = pad2(now.getSeconds());

      let suffix;
      if (format === "month_day_year_ts") suffix = `_${mon}_${dd}_${yyyy}_${day}_${hh}${mi}${ss}`;
      else if (format === "date_year_ts") suffix = `_${yyyy}_${mm}_${dd}_${hh}${mi}${ss}`;
      else if (format === "date_day_year") suffix = `_${yyyy}_${mm}_${dd}_${day}`;
      else suffix = `_${yyyy}_${mm}_${dd}_${day}_${hh}${mi}${ss}`;

      const maxBase = Math.max(1, 63 - suffix.length);
      if (clean.length > maxBase) {
        clean = clean.slice(0, maxBase).replace(/[_-]+$/g, "") || "db";
      }
      return clean + suffix;
    };

    const getSyncDirection = () =>
      $("syncDirTabs")?.querySelector(".seg.active")?.dataset.dir || "push";
    const getSyncAutoBaseName = () =>
      ($("syncAutoNameBase")?.value || "").trim() || ($("syncCloudDb")?.value || "").trim();

    const updateSyncAutoNameUI = ({ applyInput = false, now = new Date() } = {}) => {
      const isPull = getSyncDirection() === "pull";
      if ($("syncAutoNameBox")) $("syncAutoNameBox").hidden = !isPull;
      if ($("btnQuickGenLocalDb")) $("btnQuickGenLocalDb").hidden = !isPull;
      const autoOn = isPull && !!$("syncAutoNameChk")?.checked;
      if ($("syncAutoNameOpts")) $("syncAutoNameOpts").hidden = !autoOn;
      const cloudDb = ($("syncCloudDb")?.value || "").trim();
      if ($("syncAutoNameBase")) {
        $("syncAutoNameBase").placeholder = cloudDb ? `e.g. ${cloudDb}` : "e.g. alegria";
      }
      const fmt = $("syncAutoNameFmt")?.value || "date_day_year_ts";
      const generated = buildAutoPullLocalDbName(getSyncAutoBaseName() || "alegria", fmt, now);
      if ($("syncAutoNamePreview")) {
        $("syncAutoNamePreview").textContent = `${generated} (${generated.length}/63)`;
      }
      if (autoOn && applyInput && $("syncLocalDb")) {
        $("syncLocalDb").value = buildAutoPullLocalDbName(getSyncAutoBaseName(), fmt, now);
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
      if ($("syncAutoNameChk")) $("syncAutoNameChk").checked = false;
      if ($("syncAutoNameBase")) $("syncAutoNameBase").value = "";
      if ($("syncAutoNameFmt")) $("syncAutoNameFmt").value = "date_day_year_ts";
      updateSyncAutoNameUI({ applyInput: false });
      if ($("cloudSyncBox")) $("cloudSyncBox").hidden = true;
      $("modalCloudSync").hidden = false;
    };

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
        const stopped = await api().CancelCloudSync();
        if (!stopped && syncJob.running) {
          syncJob.running = false;
          syncJob.cancelling = false;
          setSyncFormLocked(false);
          setSyncProgress(syncJob.percent, "cancelled", "🛑 Sync Cancelled", "No active sync process found.");
        }
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
      updateSyncAutoNameUI({ applyInput: !!$("syncAutoNameChk")?.checked });
    });
    $("syncCloudDb")?.addEventListener("input", () => {
      updateSyncAutoNameUI({ applyInput: !!$("syncAutoNameChk")?.checked });
    });
    $("syncAutoNameChk")?.addEventListener("change", () => {
      updateSyncAutoNameUI({ applyInput: !!$("syncAutoNameChk")?.checked });
    });
    $("syncAutoNameBase")?.addEventListener("input", () => {
      updateSyncAutoNameUI({ applyInput: true });
    });
    $("syncAutoNameFmt")?.addEventListener("change", () => {
      updateSyncAutoNameUI({ applyInput: true });
    });
    $("btnRefreshSyncAutoName")?.addEventListener("click", () => {
      updateSyncAutoNameUI({ applyInput: true, now: new Date() });
    });
    $("btnQuickGenLocalDb")?.addEventListener("click", () => {
      const base =
        ($("syncAutoNameBase")?.value || "").trim() ||
        ($("syncCloudDb")?.value || "").trim() ||
        ($("syncLocalDb")?.value || "").trim();
      const fmt = $("syncAutoNameFmt")?.value || "date_day_year_ts";
      const generated = buildAutoPullLocalDbName(base, fmt, new Date());
      $("syncLocalDb").value = generated;
      if ($("syncAutoNamePreview")) {
        $("syncAutoNamePreview").textContent = `${generated} (${generated.length}/63)`;
      }
      $("syncLocalDb").focus();
      $("syncLocalDb").select?.();
    });
    $("syncLocalDb")?.addEventListener("input", () => {
      // If user manually types into Local Database while Auto-generate is on, switch to manual so their edit sticks
      if ($("syncAutoNameChk")?.checked) {
        $("syncAutoNameChk").checked = false;
        updateSyncAutoNameUI({ applyInput: false });
      }
    });
    $("syncLocalEngine").addEventListener("change", loadSyncLocalDbs);
    $("syncDirTabs").addEventListener("click", (e) => {
      const btn = e.target.closest("[data-dir]");
      if (!btn || syncJob.running) return;
      $("syncDirTabs").querySelectorAll(".seg").forEach((b) => b.classList.toggle("active", b === btn));
      updateSyncAutoNameUI({ applyInput: !!$("syncAutoNameChk")?.checked });
    });
    $("btnOpenCloudSync").onclick = () => openCloudSyncModal("");
    if ($("btnMinimizeCloudSync")) $("btnMinimizeCloudSync").onclick = minimizeCloudSync;
    if ($("btnToggleFloatStack")) {
      $("btnToggleFloatStack").onclick = () => {
        const stack = $("floatingProgressStack");
        if (!stack) return;
        const collapsed = stack.classList.toggle("collapsed");
        $("btnToggleFloatStack").textContent = collapsed ? "▸" : "▾";
        $("btnToggleFloatStack").setAttribute("aria-expanded", collapsed ? "false" : "true");
        $("btnToggleFloatStack").title = collapsed ? "Expand background jobs panel" : "Collapse background jobs panel";
      };
    }
    if ($("btnClearFinishedFloats")) {
      $("btnClearFinishedFloats").onclick = () => {
        if (!syncJob.running && syncJob.minimized) {
          clearTimeout(syncHideTimer);
          syncJob.minimized = false;
          updateFloatingSync();
        }
        if (autoSyncRun && !autoSyncRun.st.running && autoSyncRun.st.minimized) autoSyncRun.close();
        if (autoBkpRun && !autoBkpRun.st.running && autoBkpRun.st.minimized) autoBkpRun.close();
      };
    }
    if ($("btnRestoreCloudSync")) $("btnRestoreCloudSync").onclick = restoreCloudSync;
    if ($("btnFloatCancelSync")) {
      $("btnFloatCancelSync").onclick = (e) => {
        e.stopPropagation();
        cancelRunningSync();
      };
    }
    if ($("btnFloatCloseSync")) {
      $("btnFloatCloseSync").onclick = (e) => {
        e.stopPropagation();
        clearTimeout(syncHideTimer);
        syncJob.minimized = false;
        updateFloatingSync();
      };
    }
    if ($("syncFloatingBar")) {
      $("syncFloatingBar").onclick = (e) => {
        if (e.target.closest("#btnFloatCancelSync, #btnFloatCloseSync")) return;
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
      const dir = getSyncDirection();
      const localEng = $("syncLocalEngine").value;
      const cloudId = $("syncCloudProfile").value;
      const cloudDb = $("syncCloudDb").value.trim();
      if (dir === "pull" && $("syncAutoNameChk")?.checked) {
        updateSyncAutoNameUI({ applyInput: true, now: new Date() });
      }
      const localDb = $("syncLocalDb").value.trim();
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

    // ---- Separated Modals: Daily Auto-Sync (#modalAutoSyncForm) & Daily Auto-Backup (#modalAutoBackupForm) ----
    const populateCloudProfileSelect = (sel, allowLocalOnly) => {
      if (!sel) return [];
      const sqlProfiles = (state.cloudProfiles || []).filter((p) => p.engine !== "redis");
      const head = allowLocalOnly ? `<option value="">-- None (Local DB Only) --</option>` : "";
      sel.innerHTML =
        head +
        sqlProfiles
          .map((p) => `<option value="${esc(p.id)}">${esc(p.name)} (${esc(p.engine)} · ${esc(p.host)})</option>`)
          .join("");
      return sqlProfiles;
    };

    const updateAutoSyncModalUI = () => {
      const dir = $("syncSchedDir")?.value || "pull";
      const isPull = dir === "pull" || dir === "pull_folder";
      const isFolder = dir === "pull_folder";
      if ($("syncSchedLocalRow")) $("syncSchedLocalRow").hidden = isFolder;
      if ($("syncSchedFolderRow")) $("syncSchedFolderRow").hidden = !isFolder;
      if ($("syncSchedAutoNameWrap")) $("syncSchedAutoNameWrap").hidden = !isPull;
      if ($("syncSchedAutoNameLabel")) {
        $("syncSchedAutoNameLabel").textContent = isFolder
          ? "✨ Auto-generate dated Folder & .zip name on each daily Pull (Date, Day, Year & Timestamp)"
          : "✨ Auto-generate new Local DB name on each daily Pull (Date, Day, Year & Timestamp)";
      }
      const autoOn = isPull && !!$("syncSchedAutoNameChk")?.checked;
      if ($("syncSchedAutoNameOpts")) $("syncSchedAutoNameOpts").hidden = !autoOn;
      if ($("syncSchedLocalDb")) {
        $("syncSchedLocalDb").placeholder = autoOn
          ? "Base name (e.g. alegria · blank = Cloud DB)"
          : "e.g. hris";
      }
      if ($("syncSchedAutoNamePreview")) {
        const base = ($("syncSchedLocalDb")?.value || "").trim() || ($("syncSchedCloudDb")?.value || "").trim() || "alegria";
        const fmt = $("syncSchedAutoNameFmt")?.value || "date_day_year_ts";
        const previewDate = new Date();
        const [hh, mm] = String($("syncSchedTime")?.value || "02:00").split(":").map(Number);
        if (Number.isFinite(hh) && Number.isFinite(mm)) previewDate.setHours(hh, mm, 0, 0);
        const gen = buildAutoPullLocalDbName(base, fmt, previewDate);
        if (isFolder) {
          const folderPrefix = ($("syncSchedFolder")?.value || "").trim();
          const rootHint = folderPrefix ? folderPrefix.replace(/[\\/]+$/, "") : "backups/<schedule>/syncs";
          $("syncSchedAutoNamePreview").textContent = `${rootHint}/${gen}/${gen}.zip`;
        } else {
          $("syncSchedAutoNamePreview").textContent = gen;
        }
      }
    };

    const closeAutoSyncModal = () => {
      if ($("modalAutoSyncForm")) $("modalAutoSyncForm").hidden = true;
    };
    const openAutoSyncModal = (existing = null) => {
      const sqlProfiles = (state.cloudProfiles || []).filter((p) => p.engine !== "redis");
      if (!sqlProfiles.length) {
        toast("Add a Cloud MySQL, MariaDB, PostgreSQL, or MS SQL profile first", "info");
        openCloudFormModal(null, "cpanel");
        return;
      }
      $("formAutoSync")?.reset();
      $("syncSchedEditId").value = existing?.id || "";
      populateCloudProfileSelect($("syncSchedCloudId"), false);

      if (existing) {
        $("modalAutoSyncTitle").textContent = `🔄 Edit Daily Auto-Sync — ${existing.name}`;
        $("syncSchedName").value = existing.name || "";
        $("syncSchedEnabled").checked = existing.enabled !== false;
        $("syncSchedCloudId").value = existing.cloudId || sqlProfiles[0].id;
        $("syncSchedCloudDb").value = existing.cloudDb || "";
        $("syncSchedDir").value = existing.syncDirection || "pull";
        $("syncSchedTime").value = existing.syncTime || "02:00";
        $("syncSchedLocalEngine").value = existing.localEngine || "mssql";
        $("syncSchedLocalDb").value = existing.localDb || "";
        if ($("syncSchedFolder")) $("syncSchedFolder").value = existing.syncFolder || "";
        if ($("syncSchedAutoNameChk")) $("syncSchedAutoNameChk").checked = !!existing.autoNameLocalDb;
        if ($("syncSchedAutoNameFmt")) $("syncSchedAutoNameFmt").value = existing.autoNameFormat || "date_day_year_ts";
      } else {
        $("modalAutoSyncTitle").textContent = "🔄 New Daily Auto-Sync Schedule";
        const first = sqlProfiles[0];
        $("syncSchedCloudId").value = first.id;
        $("syncSchedCloudDb").value = first.database || "";
        $("syncSchedLocalEngine").value = first.engine || "mssql";
        $("syncSchedName").value = `${first.name} Auto-Sync`;
        $("syncSchedEnabled").checked = true;
        $("syncSchedDir").value = "pull";
        $("syncSchedTime").value = "02:00";
        $("syncSchedLocalDb").value = "";
        if ($("syncSchedFolder")) $("syncSchedFolder").value = "";
        if ($("syncSchedAutoNameChk")) $("syncSchedAutoNameChk").checked = false;
        if ($("syncSchedAutoNameFmt")) $("syncSchedAutoNameFmt").value = "date_day_year_ts";
      }
      updateAutoSyncModalUI();
      $("modalAutoSyncForm").hidden = false;
    };

    const updateAutoBackupModalUI = () => {
      const src = $("bkpSchedSource")?.value || "cloud";
      const isFolder = src === "cloud_folder";
      const needsCloud = src === "cloud" || src === "cloud_folder" || src === "both";
      const needsLocal = src === "local" || src === "both";
      if ($("bkpSchedCloudRow")) $("bkpSchedCloudRow").hidden = !needsCloud;
      if ($("bkpSchedLocalRow")) $("bkpSchedLocalRow").hidden = !needsLocal;
      if ($("bkpSchedAutoNameLabel")) {
        $("bkpSchedAutoNameLabel").textContent = isFolder
          ? "✨ Auto-generate dated Folder & .zip name on each daily Backup (Date, Day, Year & Timestamp)"
          : "✨ Auto-generate new Backup .zip filename on each daily Backup (Date, Day, Year & Timestamp)";
      }
      const autoOn = !!$("bkpSchedAutoNameChk")?.checked;
      if ($("bkpSchedAutoNameOpts")) $("bkpSchedAutoNameOpts").hidden = !autoOn;
      if ($("bkpSchedAutoNamePreview")) {
        const base =
          src === "local"
            ? ($("bkpSchedLocalDb")?.value || "").trim() || ($("bkpSchedCloudDb")?.value || "").trim() || "alegria"
            : isFolder
              ? ($("bkpSchedLocalDb")?.value || "").trim() || ($("bkpSchedCloudDb")?.value || "").trim() || "alegria"
              : ($("bkpSchedCloudDb")?.value || "").trim() || ($("bkpSchedLocalDb")?.value || "").trim() || "alegria";
        const fmt = $("bkpSchedAutoNameFmt")?.value || "date_day_year_ts";
        const previewDate = new Date();
        const [bhh, bmm] = String($("bkpSchedTime")?.value || "02:00").split(":").map(Number);
        if (Number.isFinite(bhh) && Number.isFinite(bmm)) previewDate.setHours(bhh, bmm, 0, 0);
        const suffix = src === "both" ? "_cloud" : "";
        const gen = buildAutoPullLocalDbName(base + suffix, fmt, previewDate);
        if (isFolder) {
          const folderPrefix = ($("bkpSchedDir")?.value || "").trim();
          const rootHint = folderPrefix ? folderPrefix.replace(/[\\/]+$/, "") : "backups/<schedule>";
          $("bkpSchedAutoNamePreview").textContent = `${rootHint}/${gen}/${gen}.zip`;
        } else {
          $("bkpSchedAutoNamePreview").textContent = gen + ".zip";
        }
      }

      const retDays = Math.max(1, Number($("bkpSchedRetentionDays")?.value) || 7);
      const now = new Date();
      const bkpDate = new Date(now.getTime());
      const [phh, pmm] = String($("bkpSchedTime")?.value || "02:00").split(":").map(Number);
      if (Number.isFinite(phh) && Number.isFinite(pmm)) bkpDate.setHours(phh, pmm, 0, 0);
      const purgeDate = new Date(bkpDate.getTime() + retDays * 86400000);
      const cutoffNow = new Date(now.getTime() - retDays * 86400000);
      const daysLabel = retDays === 7 ? "7 days · 1 week" : `${retDays} ${retDays === 1 ? "day" : "days"}`;
      const bkpText = formatPurgeDateGuide(bkpDate, true);
      const purgeText = formatPurgeDateGuide(purgeDate, true);
      if ($("bkpSchedPurgeDaysLabel")) $("bkpSchedPurgeDaysLabel").textContent = daysLabel;
      if ($("bkpSchedPurgeTargetDate")) $("bkpSchedPurgeTargetDate").textContent = purgeText;
      if ($("bkpSchedPurgeBackupDate")) $("bkpSchedPurgeBackupDate").textContent = bkpText;
      if ($("bkpSchedPurgeTargetDateSub")) $("bkpSchedPurgeTargetDateSub").textContent = purgeText;
      if ($("bkpSchedPurgeStampPreview")) $("bkpSchedPurgeStampPreview").textContent = formatPurgeStamp(purgeDate);
      if ($("bkpSchedPurgeCutoffNow")) $("bkpSchedPurgeCutoffNow").textContent = formatPurgeDateGuide(cutoffNow, true);
    };

    const closeAutoBackupModal = () => {
      if ($("modalAutoBackupForm")) $("modalAutoBackupForm").hidden = true;
    };
    const openAutoBackupModal = (existing = null) => {
      $("formAutoBackup")?.reset();
      $("bkpSchedEditId").value = existing?.id || "";
      const sqlProfiles = populateCloudProfileSelect($("bkpSchedCloudId"), true);

      if (existing) {
        $("modalAutoBackupTitle").textContent = `📦 Edit Daily Auto-Backup — ${existing.name}`;
        $("bkpSchedName").value = existing.name || "";
        $("bkpSchedEnabled").checked = existing.enabled !== false;
        $("bkpSchedSource").value = existing.backupSource || (existing.cloudId ? "cloud" : "local");
        $("bkpSchedTime").value = existing.backupTime || "02:00";
        $("bkpSchedRetentionDays").value = existing.retentionDays || 7;
        $("bkpSchedCloudId").value = existing.cloudId || "";
        $("bkpSchedCloudDb").value = existing.cloudDb || "";
        $("bkpSchedLocalEngine").value = existing.localEngine || "mssql";
        $("bkpSchedLocalDb").value = existing.localDb || "";
        if ($("bkpSchedDir")) $("bkpSchedDir").value = existing.backupDir || "";
        if ($("bkpSchedAutoNameChk")) $("bkpSchedAutoNameChk").checked = !!existing.autoNameBackup;
        if ($("bkpSchedAutoNameFmt")) $("bkpSchedAutoNameFmt").value = existing.autoNameBackupFormat || "date_day_year_ts";
      } else {
        $("modalAutoBackupTitle").textContent = "📦 New Daily Auto-Backup & Purge Schedule";
        const first = sqlProfiles[0];
        if (first) {
          $("bkpSchedCloudId").value = first.id;
          $("bkpSchedCloudDb").value = first.database || "";
          $("bkpSchedLocalEngine").value = first.engine || "mssql";
          $("bkpSchedName").value = `${first.name} Auto-Backup (2 AM)`;
          $("bkpSchedSource").value = "cloud";
        } else {
          $("bkpSchedName").value = "Local Auto-Backup (2 AM)";
          $("bkpSchedSource").value = "local";
        }
        $("bkpSchedEnabled").checked = true;
        $("bkpSchedTime").value = "02:00";
        $("bkpSchedRetentionDays").value = 7;
        $("bkpSchedLocalDb").value = "";
        if ($("bkpSchedDir")) $("bkpSchedDir").value = "";
        if ($("bkpSchedAutoNameChk")) $("bkpSchedAutoNameChk").checked = false;
        if ($("bkpSchedAutoNameFmt")) $("bkpSchedAutoNameFmt").value = "date_day_year_ts";
      }
      updateAutoBackupModalUI();
      $("modalAutoBackupForm").hidden = false;
    };

    // Wire Auto-Sync Modal events
    $("syncSchedCloudId")?.addEventListener("change", () => {
      const c = (state.cloudProfiles || []).find((p) => p.id === $("syncSchedCloudId").value);
      if (c) {
        if (c.database) $("syncSchedCloudDb").value = c.database;
        if ($("syncSchedLocalEngine").querySelector(`option[value="${c.engine}"]`)) {
          $("syncSchedLocalEngine").value = c.engine;
        }
      }
      updateAutoSyncModalUI();
    });
    $("syncSchedDir")?.addEventListener("change", () => {
      if ($("syncSchedDir").value === "pull_folder" && $("syncSchedAutoNameChk") && !$("syncSchedAutoNameChk").checked) {
        $("syncSchedAutoNameChk").checked = true;
      }
      updateAutoSyncModalUI();
    });
    $("syncSchedFolder")?.addEventListener("input", updateAutoSyncModalUI);
    if ($("btnBrowseSyncSchedFolder")) {
      $("btnBrowseSyncSchedFolder").onclick = async () => {
        try {
          const picked = await api().BrowseFolderDialog(
            "Select Destination Folder for Daily Auto-Sync Exports",
            ($("syncSchedFolder")?.value || "").trim()
          );
          if (picked) {
            $("syncSchedFolder").value = picked;
            updateAutoSyncModalUI();
          }
        } catch (err) {
          toast(errMsg(err), "err");
        }
      };
    }
    $("syncSchedAutoNameChk")?.addEventListener("change", updateAutoSyncModalUI);
    $("syncSchedAutoNameFmt")?.addEventListener("change", updateAutoSyncModalUI);
    $("syncSchedLocalDb")?.addEventListener("input", updateAutoSyncModalUI);
    $("syncSchedCloudDb")?.addEventListener("input", updateAutoSyncModalUI);
    $("syncSchedTime")?.addEventListener("change", updateAutoSyncModalUI);
    if ($("btnNewSchedule")) $("btnNewSchedule").onclick = () => openAutoSyncModal(null);
    if ($("btnAddAutoSync")) $("btnAddAutoSync").onclick = () => openAutoSyncModal(null);
    if ($("btnCloseAutoSyncForm")) $("btnCloseAutoSyncForm").onclick = closeAutoSyncModal;
    if ($("btnCancelAutoSyncForm")) $("btnCancelAutoSyncForm").onclick = closeAutoSyncModal;

    if ($("formAutoSync")) {
      $("formAutoSync").onsubmit = async (e) => {
        e.preventDefault();
        const editId = $("syncSchedEditId").value.trim();
        const existing = (state.schedules || []).find((x) => x.id === editId);
        const dir = $("syncSchedDir").value || "pull";
        const autoName = (dir === "pull" || dir === "pull_folder") && !!$("syncSchedAutoNameChk")?.checked;
        const autoFmt = $("syncSchedAutoNameFmt")?.value || "date_day_year_ts";
        const cloudId = $("syncSchedCloudId").value;
        const cloudDb = $("syncSchedCloudDb").value.trim();
        const localDb = dir === "pull_folder" ? "" : $("syncSchedLocalDb").value.trim();
        if (!cloudId || !cloudDb) {
          toast("Select a Cloud Profile and enter Cloud Database", "err");
          return;
        }
        if (dir !== "pull_folder" && !autoName && !localDb) {
          toast("Enter a Local Database name or turn on Auto-generate", "err");
          return;
        }
        try {
          if (existing && existing.autoBackup) {
            // Preserve the legacy schedule's Auto-Backup on its original ID/folder and save Auto-Sync independently
            await api().SaveSchedule({ ...existing, autoSync: false });
          }
          const payload = {
            id: existing && !existing.autoBackup ? existing.id : "",
            name: $("syncSchedName").value.trim(),
            enabled: $("syncSchedEnabled").checked,
            cloudId,
            cloudDb,
            localEngine: $("syncSchedLocalEngine").value,
            localDb,
            autoNameLocalDb: autoName,
            autoNameFormat: autoName ? autoFmt : "",
            autoSync: true,
            syncDirection: dir,
            syncFolder: dir === "pull_folder" ? ($("syncSchedFolder")?.value || "").trim() : "",
            syncTime: $("syncSchedTime").value || "02:00",
            autoBackup: false,
            retentionDays: existing?.retentionDays || 7,
            createdAt: existing && !existing.autoBackup ? (existing.createdAt || "") : "",
            lastSyncAt: existing?.lastSyncAt || "",
            lastSyncDate: existing?.lastSyncDate || "",
            lastSyncStatus: existing?.lastSyncStatus || "",
          };
          state.schedules = (await api().SaveSchedule(payload)) || [];
          closeAutoSyncModal();
          renderCloudView();
          toast("Saved Daily Auto-Sync schedule!", "ok");
        } catch (err) {
          toast(errMsg(err), "err");
        }
      };
    }

    // Wire Auto-Backup Modal events
    $("bkpSchedCloudId")?.addEventListener("change", () => {
      const c = (state.cloudProfiles || []).find((p) => p.id === $("bkpSchedCloudId").value);
      if (c) {
        if (c.database) $("bkpSchedCloudDb").value = c.database;
        if ($("bkpSchedLocalEngine").querySelector(`option[value="${c.engine}"]`)) {
          $("bkpSchedLocalEngine").value = c.engine;
        }
      }
      updateAutoBackupModalUI();
    });
    $("bkpSchedSource")?.addEventListener("change", () => {
      if ($("bkpSchedSource").value === "cloud_folder" && $("bkpSchedAutoNameChk") && !$("bkpSchedAutoNameChk").checked) {
        $("bkpSchedAutoNameChk").checked = true;
      }
      updateAutoBackupModalUI();
    });
    $("bkpSchedDir")?.addEventListener("input", updateAutoBackupModalUI);
    if ($("btnBrowseBkpSchedDir")) {
      $("btnBrowseBkpSchedDir").onclick = async () => {
        try {
          const picked = await api().BrowseFolderDialog(
            "Select Destination & Purge Folder for Daily Auto-Backup",
            ($("bkpSchedDir")?.value || "").trim()
          );
          if (picked) {
            $("bkpSchedDir").value = picked;
            updateAutoBackupModalUI();
          }
        } catch (err) {
          toast(errMsg(err), "err");
        }
      };
    }
    $("bkpSchedAutoNameChk")?.addEventListener("change", updateAutoBackupModalUI);
    $("bkpSchedAutoNameFmt")?.addEventListener("change", updateAutoBackupModalUI);
    $("bkpSchedTime")?.addEventListener("change", updateAutoBackupModalUI);
    $("bkpSchedRetentionDays")?.addEventListener("input", updateAutoBackupModalUI);
    $("bkpSchedRetentionDays")?.addEventListener("change", updateAutoBackupModalUI);
    $("bkpSchedCloudDb")?.addEventListener("input", updateAutoBackupModalUI);
    $("bkpSchedLocalDb")?.addEventListener("input", updateAutoBackupModalUI);
    if ($("btnNewAutoBackup")) $("btnNewAutoBackup").onclick = () => openAutoBackupModal(null);
    if ($("btnAddAutoBackup")) $("btnAddAutoBackup").onclick = () => openAutoBackupModal(null);
    if ($("btnCloseAutoBackupForm")) $("btnCloseAutoBackupForm").onclick = closeAutoBackupModal;
    if ($("btnCancelAutoBackupForm")) $("btnCancelAutoBackupForm").onclick = closeAutoBackupModal;

    if ($("formAutoBackup")) {
      $("formAutoBackup").onsubmit = async (e) => {
        e.preventDefault();
        const editId = $("bkpSchedEditId").value.trim();
        const existing = (state.schedules || []).find((x) => x.id === editId);
        const src = $("bkpSchedSource").value || "cloud";
        const needsCloud = src === "cloud" || src === "cloud_folder" || src === "both";
        const needsLocal = src === "local" || src === "both";
        const cloudId = needsCloud ? $("bkpSchedCloudId").value : "";
        const cloudDb = needsCloud ? $("bkpSchedCloudDb").value.trim() : "";
        const localDb = needsLocal ? $("bkpSchedLocalDb").value.trim() : "";
        if (needsCloud && (!cloudId || !cloudDb)) {
          toast("Select a Cloud Profile and enter Cloud Database for Auto-Backup", "err");
          return;
        }
        if (needsLocal && !localDb) {
          toast("Enter the Local Database to back up", "err");
          return;
        }
        const autoBkpName = !!$("bkpSchedAutoNameChk")?.checked;
        const autoBkpFmt = $("bkpSchedAutoNameFmt")?.value || "date_day_year_ts";
        try {
          if (existing && existing.autoSync) {
            // Detach Sync side onto its own standalone schedule before keeping Backup on existing ID
            await api().SaveSchedule({
              ...existing,
              id: "",
              name: `${existing.name} (Sync)`,
              autoBackup: false,
              lastBackupAt: "",
              lastBackupDate: "",
              lastBackupStatus: "",
              lastPurgedCount: 0,
            });
          }
          const payload = {
            id: existing ? existing.id : "",
            name: $("bkpSchedName").value.trim(),
            enabled: $("bkpSchedEnabled").checked,
            cloudId,
            cloudDb,
            localEngine: $("bkpSchedLocalEngine").value,
            localDb,
            autoSync: false,
            autoBackup: true,
            autoNameBackup: autoBkpName,
            autoNameBackupFormat: autoBkpName ? autoBkpFmt : "",
            backupSource: src,
            backupDir: ($("bkpSchedDir")?.value || "").trim(),
            backupTime: $("bkpSchedTime").value || "02:00",
            retentionDays: Math.max(1, Number($("bkpSchedRetentionDays").value) || 7),
            createdAt: existing?.createdAt || "",
          };
          state.schedules = (await api().SaveSchedule(payload)) || [];
          closeAutoBackupModal();
          renderCloudView();
          toast("Saved Daily Auto-Backup & Purge schedule!", "ok");
        } catch (err) {
          toast(errMsg(err), "err");
        }
      };
    }

    let activeSnapSchedId = "";

    const createSchedRunTracker = (cfg) => {
      const st = {
        running: false,
        cancelling: false,
        minimized: false,
        schedId: "",
        name: "",
        startedAt: 0,
        pct: 0,
        state: "idle",
        stage: "",
        detail: "",
        timer: null,
        hideTimer: null,
        btn: null,
        idleBtnLabel: cfg.idleBtnLabel,
      };

      const renderFloat = () => {
        const bar = $(cfg.floatBarId);
        if (!bar) return;
        bar.hidden = !st.minimized;
        if (st.minimized) {
          if ($(cfg.floatSpinnerId)) $(cfg.floatSpinnerId).hidden = st.state !== "running";
          if ($(cfg.floatStageId)) $(cfg.floatStageId).textContent = st.stage || cfg.defaultStage;
          if ($(cfg.floatPctId)) $(cfg.floatPctId).textContent = `${st.pct}%`;
          if ($(cfg.floatBarFillId)) $(cfg.floatBarFillId).style.width = `${st.pct}%`;
          if ($(cfg.floatDetailId)) $(cfg.floatDetailId).textContent = st.detail || "";
          bar.className = "sync-floating" + (st.state === "done" ? " success" : (st.state === "error" || st.state === "cancelled") ? " error" : "");
          const fc = $(cfg.floatCancelBtnId);
          if (fc) {
            fc.hidden = !st.running;
            fc.disabled = !!st.cancelling;
            fc.textContent = st.cancelling ? "⏳ Cancelling..." : "🛑 Cancel";
          }
          const fclose = cfg.floatCloseBtnId ? $(cfg.floatCloseBtnId) : null;
          if (fclose) {
            fclose.hidden = !!st.running;
          }
        }
        updateFloatingStackBodyClass();
      };

      const renderModal = (modeState, stage, detail) => {
        const elapsedSec = st.startedAt ? ((Date.now() - st.startedAt) / 1000).toFixed(1) + "s" : "0.0s";
        const box = $(cfg.boxId);
        if (box) {
          box.className = "sysup-fetch-box" + (modeState === "done" ? " success" : (modeState === "error" || modeState === "cancelled") ? " error" : "");
          if ($(cfg.spinnerId)) $(cfg.spinnerId).hidden = modeState !== "running";
          if ($(cfg.stageId)) $(cfg.stageId).textContent = stage || cfg.defaultStage;
          if ($(cfg.pctId)) $(cfg.pctId).textContent = `${st.pct}%`;
          if ($(cfg.elapsedId)) $(cfg.elapsedId).textContent = elapsedSec;
          if ($(cfg.barId)) $(cfg.barId).style.width = `${st.pct}%`;
          if ($(cfg.detailId)) $(cfg.detailId).textContent = detail || "";
        }
        const isRun = modeState === "running";
        if ($(cfg.minHeadBtnId)) $(cfg.minHeadBtnId).hidden = !isRun;
        if ($(cfg.minFootBtnId)) $(cfg.minFootBtnId).hidden = !isRun;
        if ($(cfg.cancelBtnId)) {
          $(cfg.cancelBtnId).hidden = !isRun;
          $(cfg.cancelBtnId).disabled = !!st.cancelling;
          $(cfg.cancelBtnId).textContent = st.cancelling ? "⏳ Cancelling..." : cfg.cancelLabel;
        }
        if ($(cfg.doneBtnId)) {
          $(cfg.doneBtnId).textContent = isRun ? "Hide" : "Close";
        }
        if (cfg.snapsBtnId && $(cfg.snapsBtnId)) {
          $(cfg.snapsBtnId).hidden = isRun || !st.schedId;
        }
        if (st.btn && st.btn.isConnected && isRun) {
          st.btn.textContent = `⏳ ${st.pct}%`;
        }
      };

      const stopTimer = () => {
        if (st.timer) {
          clearInterval(st.timer);
          st.timer = null;
        }
      };

      const minimize = () => {
        if (!st.running) {
          if ($(cfg.modalId)) $(cfg.modalId).hidden = true;
          return;
        }
        st.minimized = true;
        if ($(cfg.modalId)) $(cfg.modalId).hidden = true;
        renderFloat();
      };

      const restore = () => {
        clearTimeout(st.hideTimer);
        st.minimized = false;
        renderFloat();
        if ($(cfg.modalTitleId)) {
          const icon = cfg.mode === "sync" ? "🔄" : "📦";
          $(cfg.modalTitleId).textContent = st.running
            ? `${icon} ${cfg.runTitle} — ${st.name || "Schedule"}`
            : `${st.state === "done" ? "✅" : st.state === "cancelled" ? "🛑" : "⚠️"} ${st.name || cfg.runTitle}`;
        }
        renderModal(st.state === "idle" ? "running" : st.state, st.stage, st.detail);
        if ($(cfg.modalId)) $(cfg.modalId).hidden = false;
      };

      const close = () => {
        if (st.running) {
          minimize();
          toast(`${cfg.runTitle} is still running in the background — click 🛑 Cancel on the floating bar to abort.`, "info");
          return;
        }
        clearTimeout(st.hideTimer);
        st.minimized = false;
        renderFloat();
        if ($(cfg.modalId)) $(cfg.modalId).hidden = true;
      };

      const cancel = async () => {
        if (!st.running || st.cancelling) return;
        st.cancelling = true;
        update(st.pct, "running", cfg.cancellingStage, "Stopping active database export/import...");
        try {
          const stopped = api().CancelScheduleRunMode
            ? await api().CancelScheduleRunMode(st.schedId || "", cfg.mode || "")
            : await api().CancelScheduleRun(st.schedId || "");
          if (!stopped && st.running) {
            update(st.pct, "cancelled", cfg.cancellingStage.replace(/\.\.\.$/, ""), "No active background run found.");
          }
        } catch {
          // Run promise settles cleanly once backend aborts
        }
      };

      const update = (pct, modeState, stage, detail) => {
        const clamped = Math.max(0, Math.min(100, Math.round(Number(pct) || 0)));
        st.pct = clamped;
        st.state = modeState || "running";
        st.stage = stage || cfg.defaultStage;
        st.detail = detail || "";
        if (st.state !== "running") {
          st.running = false;
          st.cancelling = false;
          stopTimer();
          if ($(cfg.modalTitleId)) {
            const prefix = st.state === "done" ? "✅ Completed" : st.state === "cancelled" ? "🛑 Cancelled" : "⚠️ Failed";
            $(cfg.modalTitleId).textContent = `${prefix} — ${st.name || cfg.runTitle}`;
          }
          if (st.btn && st.btn.isConnected) {
            st.btn.disabled = false;
            st.btn.textContent = st.idleBtnLabel;
          }
          if (st.minimized) {
            clearTimeout(st.hideTimer);
            st.hideTimer = setTimeout(() => {
              if (!st.running && st.minimized) {
                st.minimized = false;
                renderFloat();
              }
            }, 5000);
          }
        }
        renderModal(st.state, st.stage, st.detail);
        renderFloat();
      };

      const start = (sched, btn, openModal = true) => {
        stopTimer();
        clearTimeout(st.hideTimer);
        st.running = true;
        st.cancelling = false;
        st.minimized = !openModal;
        st.schedId = sched?.id || "";
        st.name = sched?.name || cfg.runTitle;
        st.startedAt = Date.now();
        st.pct = 2;
        st.state = "running";
        st.btn = btn || null;
        if ($(cfg.modalTitleId)) {
          const icon = cfg.mode === "sync" ? "🔄" : "📦";
          $(cfg.modalTitleId).textContent = `${icon} ${cfg.runTitle} — ${st.name}`;
        }
        update(2, "running", cfg.startStage, `Preparing ${st.name}...`);
        if (openModal && $(cfg.modalId)) {
          $(cfg.modalId).hidden = false;
        }
        st.timer = setInterval(() => {
          if (!st.running) return;
          if ($(cfg.elapsedId)) {
            $(cfg.elapsedId).textContent = ((Date.now() - st.startedAt) / 1000).toFixed(1) + "s";
          }
        }, 350);
      };

      if ($(cfg.minHeadBtnId)) $(cfg.minHeadBtnId).onclick = minimize;
      if ($(cfg.minFootBtnId)) $(cfg.minFootBtnId).onclick = minimize;
      if ($(cfg.closeBtnId)) $(cfg.closeBtnId).onclick = close;
      if ($(cfg.doneBtnId)) $(cfg.doneBtnId).onclick = close;
      if ($(cfg.cancelBtnId)) $(cfg.cancelBtnId).onclick = cancel;
      if ($(cfg.floatRestoreBtnId)) $(cfg.floatRestoreBtnId).onclick = restore;
      if ($(cfg.floatCancelBtnId)) {
        $(cfg.floatCancelBtnId).onclick = (e) => {
          e.stopPropagation();
          cancel();
        };
      }
      if (cfg.floatCloseBtnId && $(cfg.floatCloseBtnId)) {
        $(cfg.floatCloseBtnId).onclick = (e) => {
          e.stopPropagation();
          clearTimeout(st.hideTimer);
          st.minimized = false;
          renderFloat();
        };
      }
      if ($(cfg.floatBarId)) {
        $(cfg.floatBarId).onclick = (e) => {
          if (e.target.closest("#" + cfg.floatCancelBtnId)) return;
          if (cfg.floatCloseBtnId && e.target.closest("#" + cfg.floatCloseBtnId)) return;
          restore();
        };
      }

      return { st, start, update, minimize, restore, close, stopTimer, renderFloat };
    };

    autoSyncRun = createSchedRunTracker({
      mode: "sync",
      runTitle: "Running Daily Auto-Sync",
      defaultStage: "Syncing database...",
      startStage: "⏳ Starting Auto-Sync...",
      cancellingStage: "🛑 Cancelling Auto-Sync...",
      cancelLabel: "🛑 Cancel Sync",
      idleBtnLabel: "▶ Run Sync",
      modalId: "modalAutoSyncRun",
      modalTitleId: "autoSyncRunModalTitle",
      boxId: "autoSyncRunProgBox",
      spinnerId: "autoSyncRunSpinner",
      stageId: "autoSyncRunStage",
      pctId: "autoSyncRunPct",
      elapsedId: "autoSyncRunElapsed",
      barId: "autoSyncRunBar",
      detailId: "autoSyncRunDetail",
      minHeadBtnId: "btnMinimizeAutoSyncRun",
      minFootBtnId: "btnMinFootAutoSyncRun",
      closeBtnId: "btnCloseAutoSyncRun",
      doneBtnId: "btnDoneAutoSyncRun",
      cancelBtnId: "btnCancelAutoSyncRun",
      floatBarId: "autoSyncFloatingBar",
      floatSpinnerId: "autoSyncFloatSpinner",
      floatStageId: "autoSyncFloatStage",
      floatPctId: "autoSyncFloatPct",
      floatBarFillId: "autoSyncFloatBar",
      floatDetailId: "autoSyncFloatDetail",
      floatRestoreBtnId: "btnRestoreAutoSyncRun",
      floatCancelBtnId: "btnFloatCancelAutoSync",
      floatCloseBtnId: "btnFloatCloseAutoSync",
    });

    autoBkpRun = createSchedRunTracker({
      mode: "backup",
      runTitle: "Creating Backup",
      defaultStage: "Creating backup...",
      startStage: "⏳ Creating backup...",
      cancellingStage: "🛑 Cancelling Backup...",
      cancelLabel: "🛑 Cancel Backup",
      idleBtnLabel: "▶ Backup Now",
      modalId: "modalAutoBackupRun",
      modalTitleId: "autoBkpRunModalTitle",
      boxId: "autoBkpRunProgBox",
      spinnerId: "autoBkpRunSpinner",
      stageId: "autoBkpRunStage",
      pctId: "autoBkpRunPct",
      elapsedId: "autoBkpRunElapsed",
      barId: "autoBkpRunBar",
      detailId: "autoBkpRunDetail",
      minHeadBtnId: "btnMinimizeAutoBackupRun",
      minFootBtnId: "btnMinFootAutoBackupRun",
      closeBtnId: "btnCloseAutoBackupRun",
      doneBtnId: "btnDoneAutoBackupRun",
      cancelBtnId: "btnCancelAutoBackupRun",
      snapsBtnId: "btnOpenSnapsAfterRun",
      floatBarId: "autoBkpFloatingBar",
      floatSpinnerId: "autoBkpFloatSpinner",
      floatStageId: "autoBkpFloatStage",
      floatPctId: "autoBkpFloatPct",
      floatBarFillId: "autoBkpFloatBar",
      floatDetailId: "autoBkpFloatDetail",
      floatRestoreBtnId: "btnRestoreAutoBkpRun",
      floatCancelBtnId: "btnFloatCancelAutoBkp",
      floatCloseBtnId: "btnFloatCloseAutoBkp",
    });

    if ($("btnOpenSnapsAfterRun")) {
      $("btnOpenSnapsAfterRun").onclick = async () => {
        const sid = autoBkpRun.st.schedId;
        autoBkpRun.close();
        if (sid) await loadSnapshotsModal(sid);
      };
    }

    rt()?.EventsOn?.("schedule-run-progress", (p) => {
      if (!p) return;
      const st = p.state || "running";
      const isAuto = p.trigger === "scheduled";
      const mode = p.mode || "both";
      const targetTracker = mode === "backup" ? autoBkpRun : autoSyncRun;

      if (isAuto && p.scheduleId) {
        const prev = liveScheduledRuns[p.scheduleId];
        liveScheduledRuns[p.scheduleId] = p;
        const label = mode === "backup" ? "Auto-Backup" : "Auto-Sync";
        if (!prev && st === "running") {
          toast(`⏰ ${label} started: ${p.name || "Schedule"}`, "info");
          targetTracker.start({ id: p.scheduleId, name: p.name }, null, false);
        } else if (st === "done") {
          toast(`✅ ${label} finished: ${p.name || "Schedule"} — ${p.detail || "OK"}`, "ok");
          invalidateSqlCache();
          if (state.tab === "database") loadSqlDatabases(true).catch(() => {});
          setTimeout(() => {
            if (liveScheduledRuns[p.scheduleId]?.state !== "running") {
              delete liveScheduledRuns[p.scheduleId];
              if (state.tab === "cloud") renderSchedules();
            }
          }, 2500);
        } else if (st === "error" || st === "cancelled") {
          toast(
            st === "cancelled"
              ? `🛑 ${label} cancelled: ${p.name || "Schedule"}`
              : `⚠️ ${label} failed (${p.name || "Schedule"}): ${p.detail || "See Sync Logs"}`,
            st === "cancelled" ? "info" : "err"
          );
          setTimeout(() => {
            if (liveScheduledRuns[p.scheduleId]?.state !== "running") {
              delete liveScheduledRuns[p.scheduleId];
              if (state.tab === "cloud") renderSchedules();
            }
          }, 4000);
        }
        if (state.tab === "cloud") renderSchedules();
      }

      if (targetTracker.st.schedId && p.scheduleId && p.scheduleId !== targetTracker.st.schedId) return;
      if (!targetTracker.st.running && st === "running" && !isAuto) return;
      if (targetTracker.st.cancelling && st === "running") return;
      const stagePrefix = isAuto && p.name ? `${p.name}: ` : "";
      targetTracker.update(p.percent, st, stagePrefix + (p.stage || "Running..."), p.detail || "");
    });

    // ---- Sync & Auto-Schedule Trace Logs Modal ----
    let syncLogEntries = [];
    const closeSyncLogsModal = () => {
      if ($("modalSyncLogs")) $("modalSyncLogs").hidden = true;
    };
    const renderSyncLogsList = () => {
      const box = $("syncLogList");
      if (!box) return;
      const schedF = ($("syncLogSchedFilter")?.value || "").trim();
      const statF = ($("syncLogStatusFilter")?.value || "").trim().toLowerCase();
      const q = ($("syncLogSearch")?.value || "").trim().toLowerCase();

      const filtered = (syncLogEntries || []).filter((e) => {
        if (schedF && e.scheduleId !== schedF) return false;
        if (statF && String(e.status || "").toLowerCase() !== statF) return false;
        if (q) {
          const hay = `${e.timestamp} ${e.name} ${e.trigger} ${e.action} ${e.source} ${e.target} ${e.status} ${e.detail}`.toLowerCase();
          if (!hay.includes(q)) return false;
        }
        return true;
      });
      if ($("syncLogCountPill")) {
        $("syncLogCountPill").textContent = `${filtered.length} ${filtered.length === 1 ? "entry" : "entries"}`;
      }
      if (!filtered.length) {
        box.innerHTML = `<div class="empty-note" style="padding:20px">No sync or schedule trace logs yet. Every automatic scheduled sync (e.g. 11:00 AM), <strong>▶ Run Now</strong>, <strong>▶ Backup Now</strong>, and <strong>🔄 Sync</strong> run will be logged here with timestamp, duration, and result.</div>`;
        return;
      }
      box.innerHTML = filtered
        .map((e) => {
          const st = String(e.status || "ok").toLowerCase();
          const stPillCls = st === "ok" ? "running" : st === "error" ? "stopped" : "accent";
          const stLabel = st === "ok" ? "✓ OK" : st === "error" ? "✕ ERROR" : st === "cancelled" ? "🛑 CANCELLED" : "⏳ STARTED";
          const trigLabel = e.trigger === "scheduled" ? "⏰ Scheduled" : e.trigger === "sync-modal" ? "🔄 Sync Modal" : "▶ Manual";
          const actLabel = e.action === "backup" ? "📦 Backup" : e.action === "purge" ? "🗑 Purge" : `🔄 Sync${e.direction ? " (" + e.direction + ")" : ""}`;
          const durText = e.durationMs > 0 ? `${(e.durationMs / 1000).toFixed(1)}s` : "";
          const route = e.source || e.target ? `${e.source || ""} → ${e.target || ""}` : "";
          return `<div class="sync-log-row status-${esc(st)}">
            <div class="sync-log-head">
              <div class="sync-log-meta">
                <strong style="font-size:12.5px">${esc(e.name || "Sync")}</strong>
                <span class="pill accent">${esc(trigLabel)}</span>
                <span class="pill">${esc(actLabel)}</span>
                <span class="pill ${stPillCls}">${esc(stLabel)}</span>
                ${durText ? `<span class="muted" style="font-family:var(--font-mono);font-size:11px">${esc(durText)}</span>` : ""}
              </div>
              <span class="sync-log-ts">${esc(e.timestamp || "")}</span>
            </div>
            ${route ? `<div class="sync-log-route">${esc(route)}</div>` : ""}
            <div class="sync-log-detail">${esc(e.detail || "")}</div>
          </div>`;
        })
        .join("");
    };
    const openSyncLogsModal = async (scheduleId = "") => {
      const sel = $("syncLogSchedFilter");
      if (sel) {
        const opts = [`<option value="">All Schedules &amp; Syncs</option>`];
        for (const sc of state.schedules || []) {
          opts.push(`<option value="${esc(sc.id)}">${esc(sc.name)}</option>`);
        }
        sel.innerHTML = opts.join("");
        sel.value = scheduleId || "";
      }
      if ($("syncLogStatusFilter")) $("syncLogStatusFilter").value = "";
      if ($("syncLogSearch")) $("syncLogSearch").value = "";
      try {
        syncLogEntries = (await api().SyncLogs("", 300)) || [];
      } catch (err) {
        syncLogEntries = [];
        toast(errMsg(err), "err");
      }
      renderSyncLogsList();
      if ($("modalSyncLogs")) $("modalSyncLogs").hidden = false;
    };
    if ($("btnOpenSyncLogs")) $("btnOpenSyncLogs").onclick = () => openSyncLogsModal("");
    if ($("btnCloseSyncLogs")) $("btnCloseSyncLogs").onclick = closeSyncLogsModal;
    if ($("btnDoneSyncLogs")) $("btnDoneSyncLogs").onclick = closeSyncLogsModal;
    if ($("syncLogSchedFilter")) $("syncLogSchedFilter").onchange = renderSyncLogsList;
    if ($("syncLogStatusFilter")) $("syncLogStatusFilter").onchange = renderSyncLogsList;
    if ($("syncLogSearch")) $("syncLogSearch").oninput = renderSyncLogsList;
    if ($("btnRefreshSyncLogs")) {
      $("btnRefreshSyncLogs").onclick = async () => {
        try {
          syncLogEntries = (await api().SyncLogs("", 300)) || [];
          renderSyncLogsList();
          toast("Sync trace logs refreshed", "info");
        } catch (err) {
          toast(errMsg(err), "err");
        }
      };
    }
    if ($("btnOpenSyncLogFile")) {
      $("btnOpenSyncLogFile").onclick = () => api().OpenSyncLogFile().catch((err) => toast(errMsg(err), "err"));
    }
    if ($("btnClearSyncLogs")) {
      $("btnClearSyncLogs").onclick = async () => {
        if (!(await askConfirm("Clear Sync Trace Logs?", "Clear all saved sync and schedule history entries from logs/sync-history.jsonl and logs/scheduler.log?", "Clear Logs"))) return;
        try {
          await api().ClearSyncLogs();
          syncLogEntries = [];
          renderSyncLogsList();
          toast("Sync trace logs cleared", "ok");
        } catch (err) {
          toast(errMsg(err), "err");
        }
      };
    }
    rt()?.EventsOn?.("sync-log-updated", (entry) => {
      if (!entry) return;
      syncLogEntries = [entry, ...(syncLogEntries || []).filter((x) => x.id !== entry.id)].slice(0, 300);
      if ($("modalSyncLogs") && !$("modalSyncLogs").hidden) {
        renderSyncLogsList();
      }
    });

    const closeSnapModal = () => ($("modalScheduleSnapshots").hidden = true);
    const loadSnapshotsModal = async (schedId, keepProg = false) => {
      activeSnapSchedId = schedId;
      const sched = (state.schedules || []).find((s) => s.id === schedId);
      const retDays = Math.max(1, Number(sched?.retentionDays) || 7);
      const cutoffNow = new Date(Date.now() - retDays * 86400000);
      $("modalSnapTitle").textContent = `📦 Snapshots — ${sched ? sched.name : ""}`;
      $("modalSnapMeta").textContent = `Purge after ${retDays} day(s) · Cutoff now: ${formatPurgeDateGuide(cutoffNow, false)} (older files purge)`;
      if ($("btnSnapPurgeNow")) {
        $("btnSnapPurgeNow").textContent = `🗑 Purge Expired (≥${retDays}d)`;
        $("btnSnapPurgeNow").title = `Remove snapshots on or before ${formatPurgeDateGuide(cutoffNow, true)}`;
      }
      if (!keepProg && $("snapBackupProgBox")) {
        $("snapBackupProgBox").hidden = true;
      }
      const list = (await api().ScheduleSnapshots(schedId).catch(() => [])) || [];
      const box = $("snapListContainer");
      if (!list.length) {
        box.innerHTML = `<div class="empty-note" style="padding:18px">No snapshots saved yet. Click <strong>▶ Backup Now</strong> or wait for the 2:00 AM daily run.</div>`;
      } else {
        box.innerHTML = list
          .map(
            (sn) => `<div class="folder-row" style="padding:9px 11px;align-items:flex-start">
              <div style="min-width:0;flex:1">
                <strong style="font-size:12.5px;display:block;overflow:hidden;text-overflow:ellipsis;white-space:nowrap" title="${esc(sn.path)}">${esc(sn.name)}</strong>
                <div class="muted" style="font-size:11px;margin-top:2px">📅 Created: ${esc(sn.createdDisplay || sn.createdAt)} · ${esc(sn.sizeText)}</div>
                ${
                  sn.purgeAt
                    ? `<div class="snap-purge-line${sn.expired ? " expired" : ""}">
                        <span>🗑 ${sn.expired ? "Purge due since:" : "Auto-Purge:"} <strong>${esc(sn.purgeAt)}</strong></span>
                        ${sn.purgeStamp ? `<code class="purge-stamp-tag" title="Purge date/day/month/time guide">${esc(sn.purgeStamp)}</code>` : ""}
                      </div>`
                    : ""
                }
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
    if ($("btnSnapOpenFolder")) $("btnSnapOpenFolder").onclick = () => api().OpenScheduleTargetFolder(activeSnapSchedId, "backup").catch((e) => toast(errMsg(e), "err"));
    if ($("btnSnapPurgeNow")) {
      $("btnSnapPurgeNow").onclick = async () => {
        try {
          const sched = (state.schedules || []).find((s) => s.id === activeSnapSchedId);
          const retDays = Math.max(1, Number(sched?.retentionDays) || 7);
          const cutoffStr = formatPurgeDateGuide(new Date(Date.now() - retDays * 86400000), false);
          const n = await api().PurgeScheduleSnapshots(activeSnapSchedId);
          toast(
            n > 0
              ? `Purged ${n} expired snapshot(s) on/before ${cutoffStr}.`
              : `No snapshots on/before ${cutoffStr} (≥${retDays}d) to purge.`,
            n > 0 ? "ok" : "info"
          );
          await loadSnapshotsModal(activeSnapSchedId);
        } catch (err) {
          toast(errMsg(err), "err");
        }
      };
    }
    if ($("btnSnapRunBackupNow")) {
      $("btnSnapRunBackupNow").onclick = async () => {
        const sched = (state.schedules || []).find((s) => s.id === activeSnapSchedId);
        if (!sched) return;
        closeSnapModal();
        autoBkpRun.start(sched, null, true);
        try {
          const msg = await api().RunScheduleNow(activeSnapSchedId, "backup");
          autoBkpRun.update(100, "done", "✅ Snapshot backup complete!", msg || "Snapshot created!");
          toast(msg || "Snapshot created!", "ok");
          state.schedules = (await api().Schedules()) || state.schedules;
          renderCloudView();
        } catch (err) {
          const em = errMsg(err);
          const cancelled = autoBkpRun.st.cancelling || /cancel/i.test(em);
          autoBkpRun.update(
            cancelled ? autoBkpRun.st.pct : 100,
            cancelled ? "cancelled" : "error",
            cancelled ? "🛑 Backup Cancelled" : "⚠️ Snapshot backup failed",
            cancelled ? "Auto-Backup was cancelled by user." : em
          );
          toast(cancelled ? "Auto-Backup cancelled." : em, cancelled ? "info" : "err");
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

    const handleScheduleCardClick = async (e) => {
      const btn = e.target.closest("[data-sact]");
      if (!btn) return;
      const card = btn.closest("[data-sid]");
      const id = card?.dataset.sid;
      const s = (state.schedules || []).find((x) => x.id === id);
      if (!s) return;
      const act = btn.dataset.sact;

      if (act === "run-sync" || act === "run-backup" || act === "run") {
        const mode = act === "run-sync" ? "sync" : act === "run-backup" ? "backup" : "both";
        const tracker = mode === "backup" ? autoBkpRun : autoSyncRun;
        if (tracker.st.running) {
          tracker.restore();
          return;
        }
        const prev = btn.textContent;
        btn.disabled = true;
        btn.textContent = "⏳ 2%";
        tracker.start(s, btn, true);
        try {
          const msg = await api().RunScheduleNow(s.id, mode);
          tracker.update(
            100,
            "done",
            mode === "backup" ? "✅ Backup complete!" : "✅ Sync complete!",
            msg || (mode === "backup" ? "Backup snapshot created!" : "Sync complete!")
          );
          toast(msg || "Complete!", "ok");
          state.schedules = (await api().Schedules()) || state.schedules;
          renderCloudView();
          invalidateSqlCache();
          if (state.tab === "database") await loadSqlDatabases(true);
        } catch (err) {
          const em = errMsg(err);
          const cancelled = tracker.st.cancelling || /cancel/i.test(em);
          tracker.update(
            cancelled ? tracker.st.pct : 100,
            cancelled ? "cancelled" : "error",
            cancelled ? (mode === "backup" ? "🛑 Backup Cancelled" : "🛑 Sync Cancelled") : (mode === "backup" ? "⚠️ Backup failed" : "⚠️ Sync failed"),
            cancelled ? "Run was cancelled by user." : em
          );
          toast(cancelled ? "Run cancelled." : em, cancelled ? "info" : "err");
        } finally {
          tracker.st.running = false;
          tracker.stopTimer();
          btn.disabled = false;
          btn.textContent = prev;
        }
      } else if (act === "logs") {
        await openSyncLogsModal(s.id);
      } else if (act === "snaps") {
        await loadSnapshotsModal(s.id);
      } else if (act === "purge") {
        try {
          const retDays = Math.max(1, Number(s.retentionDays) || 7);
          const cutoffStr = formatPurgeDateGuide(new Date(Date.now() - retDays * 86400000), false);
          const n = await api().PurgeScheduleSnapshots(s.id);
          toast(
            n > 0
              ? `Purged ${n} snapshot(s) on/before ${cutoffStr} (≥${retDays}d).`
              : `No snapshots on/before ${cutoffStr} (≥${retDays}d) to purge.`,
            n > 0 ? "ok" : "info"
          );
        } catch (err) {
          toast(errMsg(err), "err");
        }
      } else if (act === "folder-sync") {
        await api().OpenScheduleTargetFolder(s.id, "sync");
      } else if (act === "folder-backup" || act === "folder") {
        await api().OpenScheduleTargetFolder(s.id, "backup");
      } else if (act === "toggle-sync" || act === "toggle-backup" || act === "toggle") {
        try {
          if (act === "toggle-sync" && s.autoBackup) {
            await api().SaveSchedule({ ...s, autoSync: false });
            await api().SaveSchedule({ ...s, id: "", name: `${s.name} (Sync)`, enabled: !s.enabled, autoBackup: false, lastBackupAt: "", lastBackupDate: "", lastBackupStatus: "", lastPurgedCount: 0 });
            state.schedules = (await api().Schedules()) || [];
          } else if (act === "toggle-backup" && s.autoSync) {
            await api().SaveSchedule({ ...s, id: "", name: `${s.name} (Sync)`, autoBackup: false, lastBackupAt: "", lastBackupDate: "", lastBackupStatus: "", lastPurgedCount: 0 });
            state.schedules = (await api().SaveSchedule({ ...s, autoSync: false, enabled: !s.enabled })) || [];
          } else {
            state.schedules = (await api().SaveSchedule({ ...s, enabled: !s.enabled })) || [];
          }
          renderCloudView();
        } catch (err) {
          toast(errMsg(err), "err");
        }
      } else if (act === "edit-sync") {
        openAutoSyncModal(s);
      } else if (act === "edit-backup" || act === "edit") {
        openAutoBackupModal(s);
      } else if (act === "del-sync") {
        if (s.autoBackup) {
          if (!(await askConfirm("Remove Daily Auto-Sync?", `Remove Daily Auto-Sync for "${s.name}"? (Your Daily Auto-Backup & snapshots on disk will be kept.)`, "Remove Sync"))) return;
          state.schedules = (await api().SaveSchedule({ ...s, autoSync: false })) || [];
        } else {
          if (!(await askConfirm("Delete Auto-Sync Schedule?", `Delete Daily Auto-Sync "${s.name}"?`, "Delete"))) return;
          state.schedules = (await api().DeleteSchedule(s.id)) || [];
        }
        renderCloudView();
        toast(`Removed Auto-Sync "${s.name}"`, "ok");
      } else if (act === "clear-sync-status" || act === "clear-backup-status") {
        const mode = act === "clear-sync-status" ? "sync" : "backup";
        try {
          state.schedules = (await api().ClearScheduleStatus(s.id, mode)) || state.schedules;
          renderCloudView();
          toast(mode === "sync" ? "Cleared Last Sync status." : "Cleared Last Backup status.", "info");
        } catch (err) {
          toast(errMsg(err), "err");
        }
      } else if (act === "del-backup" || act === "del") {
        if (s.autoSync && act === "del-backup") {
          if (!(await askConfirm("Remove Daily Auto-Backup?", `Remove Daily Auto-Backup for "${s.name}"? (Your Daily Auto-Sync and existing files on disk will be kept.)`, "Remove Backup"))) return;
          state.schedules = (await api().SaveSchedule({ ...s, autoBackup: false })) || [];
        } else {
          if (!(await askConfirm("Delete Auto-Backup Schedule?", `Remove Daily Auto-Backup "${s.name}"? (Existing backup snapshots on disk will be kept.)`, "Delete"))) return;
          state.schedules = (await api().DeleteSchedule(s.id)) || [];
        }
        renderCloudView();
        toast(`Removed Auto-Backup "${s.name}"`, "ok");
      }
    };
    $("cloudAutoSyncGrid")?.addEventListener("click", handleScheduleCardClick);
    $("cloudAutoBackupGrid")?.addEventListener("click", handleScheduleCardClick);

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
        btn.textContent = "⏳ 5%";
        state.activeCloudTestBtn = btn;
        try {
          const res = await api().TestCloudProfile(c);
          state.cloudProfiles = (await api().CloudProfiles()) || state.cloudProfiles;
          renderCloudView();
          toast(res.message, "ok");
        } catch (err) {
          toast(errMsg(err), "err");
        } finally {
          state.activeCloudTestBtn = null;
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
