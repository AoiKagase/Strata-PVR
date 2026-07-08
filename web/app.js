(function () {
  var hiddenChannelsStorageKey = "strata-pvr.scheduleHiddenChannels";
  var scheduleWindowHoursByMode = {
    "day": 24,
    "three-days": 72,
    "all": 0
  };
  var scheduleMinutePixels = 1.15;
  var scheduleMinimumProgramMinutes = 30;
  var programDialogReturnFocus = null;
  var mp4DialogReturnFocus = null;
  var confirmDialogReturnFocus = null;
  var pendingConfirmResolve = null;

  var state = {
    status: null,
    reserves: [],
    recording: [],
    recorded: [],
    schedule: [],
    rules: [],
    config: {},
    storage: null,
    scheduleChannel: "",
    scheduleStreamChannel: "",
    scheduleType: "",
    scheduleDay: "",
    scheduleGenre: "",
    scheduleHiddenChannels: loadHiddenChannels(),
    scheduleWindowMode: "day",
    channelProgramsChannel: "",
    channelProgramsGenre: "",
    channelProgramsSort: "start",
    editingRuleIndex: null,
    editingRuleFormIndex: null,
    selectedProgram: null,
    activeProgramID: "",
    currentView: "",
    viewScrollPositions: {},
    scheduleGuideScroll: { left: 0, top: 0 },
    listFilters: {
      channelPrograms: { query: "", category: "" },
      rules: { query: "", state: "" },
      recorded: { query: "", category: "" },
      reserves: { query: "", category: "" }
    },
    programStateIndex: { reserves: {}, recording: {} },
    realtimeChannel: null,
    configEditorDirty: false
  };

  function byId(id) {
    return document.getElementById(id);
  }

  function normalizeHiddenChannels(values) {
    var seen = {};
    return (Array.isArray(values) ? values : []).filter(function (value) {
      if (typeof value !== "string" || !value || seen[value]) {
        return false;
      }
      seen[value] = true;
      return true;
    });
  }

  function loadHiddenChannels() {
    try {
      var raw = window.localStorage ? window.localStorage.getItem(hiddenChannelsStorageKey) : "";
      var values = raw ? JSON.parse(raw) : [];
      return normalizeHiddenChannels(values);
    } catch (error) {
      return [];
    }
  }

  function saveHiddenChannels() {
    try {
      if (window.localStorage) {
        state.scheduleHiddenChannels = normalizeHiddenChannels(state.scheduleHiddenChannels);
        window.localStorage.setItem(hiddenChannelsStorageKey, JSON.stringify(state.scheduleHiddenChannels));
      }
    } catch (error) {
      // localStorage can be unavailable in private or embedded contexts.
    }
  }

  function text(node, value) {
    if (node) {
      node.textContent = value;
    }
  }

  function rememberFocus() {
    return document.activeElement && document.activeElement !== document.body ? document.activeElement : null;
  }

  function restoreFocus(node) {
    if (node && document.contains(node) && typeof node.focus === "function") {
      node.focus();
    }
  }

  function focusFirstDialogControl(dialog) {
    if (!dialog) {
      return;
    }
    window.setTimeout(function () {
      var control = dialog.querySelector("button, [href], input, select, textarea, [tabindex]:not([tabindex='-1'])");
      if (control && typeof control.focus === "function") {
        control.focus();
      }
    }, 0);
  }

  function api(path) {
    return request(path, "GET");
  }

  function request(path, method) {
    return fetch("/api/" + path, {
      credentials: "same-origin",
      method: method || "GET"
    }).then(function (response) {
      if (!response.ok) {
        throw new Error(path + " returned " + response.status);
      }
      return response.text().then(function (body) {
        var result = body ? JSON.parse(body) : {};
        publishMutation(path, method || "GET");
        return result;
      });
    });
  }

  function sendJSON(path, method, value) {
    return fetch("/api/" + path, {
      body: JSON.stringify(value),
      credentials: "same-origin",
      headers: { "Content-Type": "application/json" },
      method: method
    }).then(function (response) {
      if (!response.ok) {
        throw new Error(path + " returned " + response.status);
      }
      return response.text().then(function (body) {
        var result = body ? JSON.parse(body) : {};
        publishMutation(path, method);
        return result;
      });
    });
  }

  function sendConfigJSON(raw) {
    return fetch("/api/config.json?json=" + encodeURIComponent(raw), {
      credentials: "same-origin",
      method: "PUT"
    }).then(function (response) {
      if (!response.ok) {
        throw new Error("config.json returned " + response.status);
      }
      return response.text().then(function (body) {
        var result = body ? JSON.parse(body) : {};
        publishRealtime("notify-config");
        return result;
      });
    });
  }

  function notifyEventForPath(path, method) {
    if (!method || method === "GET") {
      return "";
    }
    if (/^program\//.test(path) || /^reserves?(\b|\/|\.json)/.test(path)) {
      return "notify-reserves";
    }
    if (/^recording\//.test(path)) {
      return "notify-recording";
    }
    if (/^recorded\//.test(path)) {
      return "notify-recorded";
    }
    if (/^rules(\b|\/|\.json)/.test(path)) {
      return "notify-rules";
    }
    if (/^scheduler\//.test(path)) {
      return "notify-schedule";
    }
    return "";
  }

  function publishMutation(path, method) {
    publishRealtime(notifyEventForPath(path, method));
  }

  function publishRealtime(eventName) {
    if (!eventName) {
      return;
    }
    if (typeof window.StrataPVRNotify === "function") {
      window.StrataPVRNotify(eventName);
      return;
    }
    var message = { event: eventName, at: Date.now() };
    if (typeof window.BroadcastChannel === "function") {
      try {
        var channel = new window.BroadcastChannel("strata-pvr");
        channel.postMessage(message);
        channel.close();
      } catch (error) {
        // Continue with the localStorage fallback.
      }
    }
    try {
      if (window.localStorage) {
        window.localStorage.setItem("strata-pvr:notify", JSON.stringify(message));
      }
    } catch (error) {
      // localStorage can be unavailable in private or embedded contexts.
    }
  }

  function subscribeRealtimeRefresh() {
    var storageKey = "strata-pvr:notify";
    var refreshEvents = {
      "notify-config": true,
      "notify-recorded": true,
      "notify-recording": true,
      "notify-reserves": true,
      "notify-rules": true,
      "notify-schedule": true,
      "notify-storage": true
    };
    function handle(message) {
      if (!message || !refreshEvents[message.event]) {
        return;
      }
      refresh();
    }
    if (typeof window.BroadcastChannel === "function") {
      try {
        var channel = new window.BroadcastChannel("strata-pvr");
        channel.onmessage = function (event) {
          handle(event.data);
        };
        state.realtimeChannel = channel;
      } catch (error) {
        // The storage event fallback below still covers other tabs.
      }
    }
    window.addEventListener("storage", function (event) {
      if (event.key !== storageKey || !event.newValue) {
        return;
      }
      try {
        handle(JSON.parse(event.newValue));
      } catch (error) {
        // Ignore malformed values written by other code.
      }
    });
  }

  function apiText(path) {
    return fetch("/api/" + path, {
      credentials: "same-origin"
    }).then(function (response) {
      if (response.status === 204) {
        return "";
      }
      if (!response.ok) {
        throw new Error(path + " returned " + response.status);
      }
      return response.text();
    });
  }

  function formatTime(value) {
    if (!value) {
      return "";
    }
    var date = new Date(value);
    if (isNaN(date.getTime())) {
      return "";
    }
    return date.toLocaleString([], {
      month: "2-digit",
      day: "2-digit",
      hour: "2-digit",
      minute: "2-digit"
    });
  }

  function formatClock(value) {
    if (!value) {
      return "";
    }
    var date = new Date(value);
    if (isNaN(date.getTime())) {
      return "";
    }
    return date.toLocaleTimeString([], {
      hour: "2-digit",
      minute: "2-digit"
    });
  }

  function formatDuration(start, end) {
    if (!start || !end || end <= start) {
      return "";
    }
    return Math.round((end - start) / 60000) + "分";
  }

  function programTitle(program) {
    return program.fullTitle || program.title || program.id || "無題";
  }

  function channelName(program) {
    if (!program.channel) {
      return "";
    }
    return program.channel.name || program.channel.channel || program.channel.id || "";
  }

  function programChannelID(program) {
    if (!program || !program.channel) {
      return "";
    }
    if (typeof program.channel === "object") {
      return String(program.channel.id || program.channel.serviceId || program.channel.sid || program.channel.channel || "");
    }
    return String(program.channel || "");
  }

  function programChannelType(program) {
    if (!program || !program.channel || typeof program.channel !== "object") {
      return String(program && program.type || "");
    }
    return String(program.channel.type || program.channel.channelType || program.type || "");
  }

  function programCategory(program) {
    return String(program && (program.category || program.genre || "") || "");
  }

  function normalizeSearchText(value) {
    return String(value || "").toLocaleLowerCase("ja").trim();
  }

  function programSearchText(program) {
    return [
      program && program.id,
      programTitle(program || {}),
      program && program.title,
      program && program.fullTitle,
      program && program.detail,
      program && program.description,
      programCategory(program),
      channelName(program || {}),
      programChannelID(program || {}),
      programChannelType(program || {})
    ].filter(Boolean).join(" ");
  }

  function programByID(items) {
    var byID = {};
    (items || []).forEach(function (program) {
      if (program && program.id) {
        byID[program.id] = program;
      }
    });
    return byID;
  }

  function decorateProgramState(program) {
    if (!program || !program.id) {
      return program;
    }
    var reserves = state.programStateIndex.reserves || {};
    var recording = state.programStateIndex.recording || {};
    var reserve = reserves[program.id] || null;
    var active = recording[program.id] || null;
    if (!reserve && !active) {
      return program;
    }
    var decorated = {};
    Object.keys(program).forEach(function (key) {
      decorated[key] = program[key];
    });
    if (reserve) {
      decorated.isReserved = true;
      decorated.isManualReserved = Boolean(reserve.isManualReserved);
      decorated.isSkip = Boolean(reserve.isSkip);
      decorated._reserveState = reserve;
    }
    if (active) {
      decorated.isRecording = true;
      decorated.isManualReserved = Boolean(active.isManualReserved || decorated.isManualReserved);
      decorated.abort = Boolean(active.abort);
      decorated.pid = active.pid;
      decorated.recorded = active.recorded || decorated.recorded;
      decorated._recordingState = active;
    }
    return decorated;
  }

  function renderProgramStateBadges(item, program) {
    var labels = [];
    if (program && program.isRecording) {
      labels.push("録画中");
    } else if (program && program.isReserved) {
      labels.push(program.isManualReserved ? "手動予約" : "予約済み");
    }
    if (program && program.isSkip) {
      labels.push("スキップ");
    }
    if (!labels.length) {
      return;
    }
    var badges = document.createElement("div");
    badges.className = "program-state-badges";
    labels.forEach(function (label) {
      var badge = document.createElement("span");
      badge.className = "program-state-badge";
      badge.textContent = label;
      badges.appendChild(badge);
    });
    item.appendChild(badges);
  }

  function filteredPrograms(items, filterName) {
    var filter = state.listFilters[filterName] || {};
    var query = normalizeSearchText(filter.query);
    var category = filter.category || "";
    return (items || []).filter(function (program) {
      if (category && programCategory(program) !== category) {
        return false;
      }
      if (!query) {
        return true;
      }
      return normalizeSearchText(programSearchText(program)).indexOf(query) >= 0;
    });
  }

  function filteredRules(items) {
    var filter = state.listFilters.rules || {};
    var query = normalizeSearchText(filter.query);
    var wantedState = filter.state || "";
    return (items || []).map(function (rule, index) {
      return { index: index, rule: rule };
    }).filter(function (entry) {
      var rule = entry.rule;
      if (wantedState === "enabled" && rule.isDisabled) {
        return false;
      }
      if (wantedState === "disabled" && !rule.isDisabled) {
        return false;
      }
      if (!query) {
        return true;
      }
      return normalizeSearchText("#" + entry.index + " " + ruleSummary(rule) + " " + JSON.stringify(rule)).indexOf(query) >= 0;
    });
  }

  function listCategories(items) {
    var seen = {};
    var values = [];
    (items || []).forEach(function (program) {
      var category = programCategory(program);
      if (category && !seen[category]) {
        seen[category] = true;
        values.push(category);
      }
    });
    return values.sort(function (a, b) {
      return a.localeCompare(b, "ja");
    });
  }

  function updateListCategoryOptions(selectID, items, filterName) {
    var select = byId(selectID);
    if (!select) {
      return;
    }
    var filter = state.listFilters[filterName] || {};
    var current = filter.category || "";
    select.innerHTML = "";
    var all = document.createElement("option");
    all.value = "";
    all.textContent = "全ジャンル";
    select.appendChild(all);
    listCategories(items).forEach(function (category) {
      var option = document.createElement("option");
      option.value = category;
      option.textContent = category;
      select.appendChild(option);
    });
    if (current && !Array.prototype.some.call(select.options, function (option) {
      return option.value === current;
    })) {
      filter.category = "";
      current = "";
    }
    select.value = current;
  }

  function updateListFilterSummary(id, shown, total) {
    var root = byId(id);
    if (!root) {
      return;
    }
    root.textContent = shown === total ? total + "件" : shown + " / " + total + "件";
  }

  function scheduleChannelID(channel) {
    if (!channel) {
      return "";
    }
    if (channel.channel && typeof channel.channel === "object" && channel.channel.id) {
      return String(channel.channel.id);
    }
    return String(channel.id || "");
  }

  function scheduleChannelName(channel) {
    if (!channel) {
      return "";
    }
    if (channel.channel && typeof channel.channel === "object") {
      return channel.channel.name || channel.channel.serviceName || channel.channel.displayName || channel.channel.channel || channel.channel.id || "";
    }
    return channel.name || channel.serviceName || channel.displayName || channel.channelName || channel.channel || channel.id || "";
  }

  function scheduleChannelHasLogo(channel) {
    if (!channel) {
      return false;
    }
    if (channel.channel && typeof channel.channel === "object" && channel.channel.hasLogoData !== undefined) {
      return Boolean(channel.channel.hasLogoData);
    }
    return Boolean(channel.hasLogoData);
  }

  function scheduleChannelType(channel) {
    if (!channel) {
      return "";
    }
    if (channel.channel && typeof channel.channel === "object") {
      return String(channel.channel.type || channel.channel.channelType || "");
    }
    return String(channel.type || channel.channelType || "");
  }

  function scheduleProgramChannelName(program, fallback) {
    if (program && program.channel) {
      return program.channel.name || program.channel.serviceName || program.channel.displayName || program.channel.channel || fallback || "";
    }
    return fallback || "";
  }

  function dateKey(value) {
    var date = new Date(value);
    if (isNaN(date.getTime())) {
      return "";
    }
    return date.getFullYear() + "-" + String(date.getMonth() + 1).padStart(2, "0") + "-" + String(date.getDate()).padStart(2, "0");
  }

  function startOfDay(value) {
    var date = new Date(value);
    date.setHours(0, 0, 0, 0);
    return date.getTime();
  }

  function dayLabel(value) {
    var date = new Date(value);
    return date.toLocaleDateString([], {
      month: "2-digit",
      day: "2-digit",
      weekday: "short"
    });
  }

  function ruleSummary(rule) {
    var parts = [];
    if (rule.types && rule.types.length) {
      parts.push("types " + rule.types.join(","));
    }
    if (rule.categories && rule.categories.length) {
      parts.push("categories " + rule.categories.join(","));
    }
    if (rule.channels && rule.channels.length) {
      parts.push("channels " + rule.channels.join(","));
    }
    if (rule.reserve_titles && rule.reserve_titles.length) {
      parts.push("titles " + rule.reserve_titles.join(","));
    }
    if (rule.ignore_titles && rule.ignore_titles.length) {
      parts.push("ignores " + rule.ignore_titles.join(","));
    }
    return parts.join(" / ") || JSON.stringify(rule);
  }

  function actionButton(label, title, fn) {
    var button = document.createElement("button");
    button.type = "button";
    button.className = "small-button";
    button.title = title || label;
    button.textContent = label;
    button.addEventListener("click", fn);
    return button;
  }

  function currentView() {
    var hash = (window.location.hash || "#dashboard").replace("#", "");
    return hash || "dashboard";
  }

  function setView(name) {
    var found = false;
    if (state.currentView) {
      state.viewScrollPositions[state.currentView] = window.pageYOffset || document.documentElement.scrollTop || 0;
    }
    document.body.className = document.body.className.replace(/\bview-[^\s]+/g, "").trim();
    document.body.classList.add("view-" + name);
    document.querySelectorAll("[data-view]").forEach(function (view) {
      var active = view.getAttribute("data-view") === name;
      view.hidden = !active;
      view.classList.toggle("active", active);
      if (active) {
        found = true;
      }
    });
    if (!found && name !== "dashboard") {
      window.location.hash = "dashboard";
      return;
    }
    state.currentView = found ? name : "dashboard";
    document.querySelectorAll("[data-view-link]").forEach(function (link) {
      var active = link.getAttribute("data-view-link") === name;
      link.classList.toggle("active", active);
      if (active) {
        link.setAttribute("aria-current", "page");
      } else {
        link.removeAttribute("aria-current");
      }
    });
    window.setTimeout(function () {
      window.scrollTo(0, state.viewScrollPositions[state.currentView] || 0);
    }, 0);
  }

  function initNavigation() {
    setView(currentView());
    window.addEventListener("hashchange", function () {
      setView(currentView());
    });
  }

  function isEditableTarget(target) {
    if (!target) {
      return false;
    }
    var name = (target.tagName || "").toLowerCase();
    return name === "input" || name === "select" || name === "textarea" || target.isContentEditable;
  }

  function focusCurrentSearch() {
    var view = currentView();
    var focusByView = {
      "channel-programs": "channelProgramsQuery",
      "reserves": "reserveListQuery",
      "recorded": "recordedListQuery",
      "rules": "ruleListQuery",
      "settings": "configEditor"
    };
    var control = byId(focusByView[view]);
    if (!control && view === "schedule") {
      control = byId("scheduleChannel");
    }
    if (control && typeof control.focus === "function") {
      control.focus();
      if (typeof control.select === "function") {
        control.select();
      }
    }
  }

  function currentFocusableRows() {
    var view = document.querySelector("[data-view='" + state.currentView + "']");
    if (!view) {
      return [];
    }
    return Array.prototype.slice.call(view.querySelectorAll(".program-row[tabindex='0']"));
  }

  function focusAdjacentRow(delta) {
    var rows = currentFocusableRows();
    if (!rows.length) {
      return false;
    }
    var index = rows.indexOf(document.activeElement);
    if (index < 0) {
      rows[delta > 0 ? 0 : rows.length - 1].focus();
      return true;
    }
    index = Math.max(0, Math.min(rows.length - 1, index + delta));
    rows[index].focus();
    return true;
  }

  function closeTopDialog() {
    var dialogs = ["mp4Dialog", "programDialog", "confirmDialog"];
    for (var i = 0; i < dialogs.length; i++) {
      var dialog = byId(dialogs[i]);
      if (dialog && dialog.open) {
        if (dialogs[i] === "programDialog") {
          closeProgramDialog();
        } else if (dialogs[i] === "confirmDialog") {
          closeConfirmDialog(false);
        } else {
          dialog.close();
        }
        return true;
      }
    }
    return false;
  }

  function initKeyboardShortcuts() {
    var viewShortcuts = {
      "1": "dashboard",
      "2": "schedule",
      "3": "reserves",
      "4": "recorded",
      "5": "rules",
      "6": "logs",
      "7": "settings"
    };
    window.addEventListener("keydown", function (event) {
      if (event.key === "Escape" && closeTopDialog()) {
        event.preventDefault();
        return;
      }
      if (event.altKey || event.ctrlKey || event.metaKey || isEditableTarget(event.target)) {
        return;
      }
      if (viewShortcuts[event.key]) {
        event.preventDefault();
        window.location.hash = viewShortcuts[event.key];
      } else if (event.key === "r" || event.key === "R") {
        event.preventDefault();
        refresh();
      } else if (event.key === "/") {
        event.preventDefault();
        focusCurrentSearch();
      } else if (event.key === "j" || event.key === "ArrowDown") {
        if (focusAdjacentRow(1)) {
          event.preventDefault();
        }
      } else if (event.key === "k" || event.key === "ArrowUp") {
        if (focusAdjacentRow(-1)) {
          event.preventDefault();
        }
      }
    });
  }

  function closeConfirmDialog(result) {
    var dialog = byId("confirmDialog");
    if (pendingConfirmResolve) {
      pendingConfirmResolve(Boolean(result));
      pendingConfirmResolve = null;
    }
    if (dialog && dialog.close) {
      dialog.close();
    } else if (dialog) {
      dialog.removeAttribute("open");
      restoreFocus(confirmDialogReturnFocus);
      confirmDialogReturnFocus = null;
    }
  }

  function confirmAction(message, options) {
    var dialog = byId("confirmDialog");
    if (!dialog || !dialog.showModal) {
      return Promise.resolve(window.confirm(message));
    }
    if (pendingConfirmResolve) {
      closeConfirmDialog(false);
    }
    options = options || {};
    text(byId("confirmDialogTitle"), options.title || "確認");
    text(byId("confirmDialogMessage"), message || "実行しますか？");
    text(byId("confirmDialogMeta"), options.meta || "");
    text(byId("confirmDialogCancel"), options.cancelLabel || "キャンセル");
    text(byId("confirmDialogOK"), options.okLabel || "実行");
    var ok = byId("confirmDialogOK");
    if (ok) {
      ok.classList.toggle("danger-button", options.danger !== false);
    }
    confirmDialogReturnFocus = rememberFocus();
    return new Promise(function (resolve) {
      pendingConfirmResolve = resolve;
      dialog.showModal();
      focusFirstDialogControl(dialog);
    });
  }

  function runAction(path, method, message, options) {
    return (message ? confirmAction(message, options || actionConfirmOptions(method, message)) : Promise.resolve(true)).then(function (confirmed) {
      if (!confirmed) {
        return;
      }
      setBusy("Working");
      return request(path, method).then(refresh).catch(showError);
    });
  }

  function programConfirmMeta(program) {
    return [programTitle(program), channelName(program), program && program.id].filter(Boolean).join(" / ");
  }

  function actionConfirmOptions(method, message, program, title) {
    return {
      danger: method === "DELETE" || /削除|停止/.test(message || ""),
      meta: program ? programConfirmMeta(program) : "",
      okLabel: /停止/.test(message || "") ? "停止" : (method === "DELETE" || /削除/.test(message || "") ? "削除" : "実行"),
      title: title || "操作の確認"
    };
  }

  function recordedWatchURL(program, ext, query) {
    var url = "/api/recorded/" + encodeURIComponent(program.id) + "/watch." + ext;
    if (!query) {
      return url;
    }
    return url + "?" + new URLSearchParams(query).toString();
  }

  function recordingWatchURL(program, ext, query) {
    var url = "/api/recording/" + encodeURIComponent(program.id) + "/watch." + ext;
    if (!query) {
      return url;
    }
    return url + "?" + new URLSearchParams(query).toString();
  }

  function channelURL(channelID, resource, ext, query) {
    var url = "/api/channel/" + encodeURIComponent(channelID) + "/" + resource + "." + ext;
    if (!query) {
      return url;
    }
    return url + "?" + new URLSearchParams(query).toString();
  }

  function openURL(url) {
    window.location.href = url;
  }

  function playbackNumber(id, label) {
    var input = byId(id);
    var value = input ? input.value.trim() : "";
    if (!value) {
      return null;
    }
    var number = Number(value);
    if (!Number.isInteger(number) || number < 0) {
      showError(new Error(label + " is invalid"));
      return false;
    }
    return number;
  }

  function playbackQuery(includeQuality) {
    var start = playbackNumber("playbackStart", "Playback start");
    if (start === false) {
      return false;
    }
    var duration = playbackNumber("playbackDuration", "Playback duration");
    if (duration === false) {
      return false;
    }
    var query = {};
    if (start !== null) {
      query.ss = String(start * 60);
    }
    if (duration !== null && duration > 0) {
      query.t = String(duration * 60);
    }
    var quality = byId("playbackQuality");
    if (includeQuality && quality && quality.value === "720p") {
      query.s = "1280x720";
      query["b:v"] = "1800k";
      query["b:a"] = "128k";
    } else if (includeQuality && quality && quality.value === "low") {
      query.s = "640x360";
      query["b:v"] = "800k";
      query["b:a"] = "96k";
    }
    return Object.keys(query).length ? query : null;
  }

  var mp4Presets = {
    "1080p": { s: "1920x1080", video: "4000k", audio: "192k" },
    "720p": { s: "1280x720", video: "1800k", audio: "128k" },
    "540p": { s: "960x540", video: "1200k", audio: "128k" },
    "360p": { s: "640x360", video: "800k", audio: "96k" }
  };

  var pendingMP4Open = null;

  function applyMP4Preset(name) {
    var preset = mp4Presets[name];
    var resolution = byId("mp4Resolution");
    var videoBitrate = byId("mp4VideoBitrate");
    var audioBitrate = byId("mp4AudioBitrate");
    var readonly = Boolean(preset) || name === "";
    if (preset) {
      resolution.value = preset.s;
      videoBitrate.value = preset.video;
      audioBitrate.value = preset.audio;
    } else if (name === "") {
      resolution.value = "";
      videoBitrate.value = "";
      audioBitrate.value = "";
    }
    [resolution, videoBitrate, audioBitrate].forEach(function (input) {
      if (input) {
        input.readOnly = readonly;
      }
    });
  }

  function bitrateValue(id, label) {
    var input = byId(id);
    var value = input ? input.value.trim() : "";
    if (!value) {
      return "";
    }
    if (!/^[1-9][0-9]*(k|K|m|M)$/.test(value)) {
      showError(new Error(label + "は 1800k または 2m の形式で指定してください"));
      return false;
    }
    return value;
  }

  function resolutionValue(id) {
    var input = byId(id);
    var value = input ? input.value.trim() : "";
    if (!value) {
      return "";
    }
    if (!/^[1-9][0-9]{1,4}x[1-9][0-9]{1,4}$/.test(value)) {
      showError(new Error("解像度は 1280x720 の形式で指定してください"));
      return false;
    }
    return value;
  }

  function mp4QueryFromDialog() {
    var resolution = resolutionValue("mp4Resolution");
    if (resolution === false) {
      return false;
    }
    var videoBitrate = bitrateValue("mp4VideoBitrate", "映像ビットレート");
    if (videoBitrate === false) {
      return false;
    }
    var audioBitrate = bitrateValue("mp4AudioBitrate", "音声ビットレート");
    if (audioBitrate === false) {
      return false;
    }
    var query = {};
    if (resolution) {
      query.s = resolution;
    }
    if (videoBitrate) {
      query["b:v"] = videoBitrate;
    }
    if (audioBitrate) {
      query["b:a"] = audioBitrate;
    }
    return Object.keys(query).length ? query : null;
  }

  function openMP4Dialog(meta, openWithQuery, initialPreset) {
    var dialog = byId("mp4Dialog");
    if (!dialog || !dialog.showModal) {
      openWithQuery(initialPreset && mp4Presets[initialPreset] ? {
        "s": mp4Presets[initialPreset].s,
        "b:v": mp4Presets[initialPreset].video,
        "b:a": mp4Presets[initialPreset].audio
      } : null);
      return;
    }
    mp4DialogReturnFocus = rememberFocus();
    pendingMP4Open = openWithQuery;
    text(byId("mp4DialogMeta"), meta || "");
    var preset = byId("mp4Preset");
    if (preset) {
      preset.value = initialPreset || "";
      applyMP4Preset(preset.value);
    }
    dialog.showModal();
    focusFirstDialogControl(dialog);
  }

  function submitMP4Dialog() {
    var query = mp4QueryFromDialog();
    if (query === false || !pendingMP4Open) {
      return;
    }
    var dialog = byId("mp4Dialog");
    if (dialog) {
      dialog.close();
    }
    var open = pendingMP4Open;
    pendingMP4Open = null;
    open(query);
  }

  function formatBytes(value) {
    if (typeof value !== "number" || !isFinite(value) || value < 0) {
      return "取得不可";
    }
    var units = ["B", "KB", "MB", "GB", "TB"];
    var size = value;
    var unit = 0;
    while (size >= 1024 && unit < units.length - 1) {
      size /= 1024;
      unit += 1;
    }
    return (unit === 0 ? String(size) : size.toFixed(size >= 10 ? 1 : 2)) + " " + units[unit];
  }

  function renderActions(item, program, actions) {
    if (!actions || actions.length === 0) {
      return;
    }
    var row = document.createElement("div");
    row.className = "row-actions";
    actions.forEach(function (name) {
      if (name === "reserve") {
        if (!program.isReserved && !program.isRecording) {
          row.appendChild(actionButton("予約", "この番組を予約", function () {
            runAction("program/" + encodeURIComponent(program.id) + ".json", "PUT");
          }));
        }
      } else if (name === "unreserve" && program.isManualReserved && !program.isRecording) {
        row.appendChild(actionButton("予約削除", "手動予約を削除", function () {
          runAction("reserves/" + encodeURIComponent(program.id) + ".json", "DELETE", "この手動予約を削除しますか？", actionConfirmOptions("DELETE", "この手動予約を削除しますか？", program, "予約削除の確認"));
        }));
      } else if (name === "skip" && program.isReserved && !program.isManualReserved && !program.isSkip && !program.isRecording) {
        row.appendChild(actionButton("スキップ", "自動予約をスキップ", function () {
          runAction("reserves/" + encodeURIComponent(program.id) + "/skip.json", "PUT");
        }));
      } else if (name === "unskip" && program.isReserved && !program.isManualReserved && program.isSkip && !program.isRecording) {
        row.appendChild(actionButton("解除", "スキップを解除", function () {
          runAction("reserves/" + encodeURIComponent(program.id) + "/unskip.json", "PUT");
        }));
      } else if (name === "stop" && program.isRecording) {
        row.appendChild(actionButton("停止", "録画を停止", function () {
          runAction("recording/" + encodeURIComponent(program.id) + ".json", "DELETE", "この録画を停止しますか？", actionConfirmOptions("DELETE", "この録画を停止しますか？", program, "録画停止の確認"));
        }));
      } else if (name === "watch-recording-mp4" && program.isRecording) {
        row.appendChild(actionButton("視聴", "録画中の番組を変換視聴で開く", function () {
          openMP4Dialog(program.title || program.id || "録画中", function (query) {
            openURL(recordingWatchURL(program, "mp4", query));
          });
        }));
      } else if (name === "playlist-recording" && program.isRecording) {
        row.appendChild(actionButton("XSPF", "録画中のプレイリストを開く", function () {
          openURL(recordingWatchURL(program, "xspf"));
        }));
      } else if (name === "preview-recording" && program.isRecording) {
        row.appendChild(actionButton("プレビュー", "録画中のプレビュー画像を開く", function () {
          openURL("/api/recording/" + encodeURIComponent(program.id) + "/preview.png");
        }));
      } else if (name === "watch-m2ts") {
        row.appendChild(actionButton("M2TS", "M2TSを開く", function () {
          openURL(recordedWatchURL(program, "m2ts"));
        }));
      } else if (name === "watch-mp4") {
        row.appendChild(actionButton("視聴", "録画済み番組を変換視聴で開く", function () {
          openMP4Dialog(program.title || program.id || "録画済み", function (query) {
            openURL(recordedWatchURL(program, "mp4", query));
          });
        }));
      } else if (name === "watch-mp4-720p") {
        row.appendChild(actionButton("720p視聴", "720p変換視聴を開く", function () {
          openMP4Dialog(program.title || program.id || "録画済み", function (query) {
            openURL(recordedWatchURL(program, "mp4", query));
          }, "720p");
        }));
      } else if (name === "watch-mp4-low") {
        row.appendChild(actionButton("低画質視聴", "低ビットレート変換視聴を開く", function () {
          openMP4Dialog(program.title || program.id || "録画済み", function (query) {
            openURL(recordedWatchURL(program, "mp4", query));
          }, "360p");
        }));
      } else if (name === "watch-mp4-custom") {
        row.appendChild(actionButton("詳細視聴", "再生条件付き変換視聴を開く", function () {
          var query = playbackQuery(true);
          if (query === false) {
            return;
          }
          openURL(recordedWatchURL(program, "mp4", query));
        }));
      } else if (name === "watch-m2ts-offset") {
        row.appendChild(actionButton("M2TS指定", "開始位置・長さ指定付きM2TSを開く", function () {
          var query = playbackQuery(false);
          if (query === false) {
            return;
          }
          openURL(recordedWatchURL(program, "m2ts", query));
        }));
      } else if (name === "playlist") {
        row.appendChild(actionButton("XSPF", "プレイリストを開く", function () {
          openURL(recordedWatchURL(program, "xspf"));
        }));
      } else if (name === "download") {
        row.appendChild(actionButton("保存", "録画ファイルを保存", function () {
          openURL("/api/recorded/" + encodeURIComponent(program.id) + "/file.m2ts");
        }));
      } else if (name === "preview-recorded") {
        row.appendChild(actionButton("プレビュー", "録画済みプレビュー画像を開く", function () {
          openURL("/api/recorded/" + encodeURIComponent(program.id) + "/preview.png");
        }));
      } else if (name === "delete-recorded") {
        row.appendChild(actionButton("削除", "録画済み項目とファイルを削除", function () {
          runAction("recorded/" + encodeURIComponent(program.id) + ".json", "DELETE", "この録画済み項目とファイルを削除しますか？", actionConfirmOptions("DELETE", "この録画済み項目とファイルを削除しますか？", program, "録画済み削除の確認"));
        }));
      } else if (name === "watch-channel-mp4") {
        var channelID = programChannelID(program);
        if (channelID) {
          row.appendChild(actionButton("視聴", "この番組のチャンネルを変換視聴で開く", function () {
            openMP4Dialog(program.title || channelID || "チャンネル", function (query) {
              openURL(channelURL(channelID, "watch", "mp4", query));
            });
          }));
        }
      } else if (name === "open-channel-programs") {
        var programsChannelID = programChannelID(program);
        if (programsChannelID) {
          row.appendChild(actionButton("番組一覧", "このチャンネルの番組一覧を開く", function () {
            closeProgramDialog();
            openChannelPrograms(programsChannelID);
          }));
        }
      } else if (name === "create-rule-from-program") {
        row.appendChild(actionButton("ルール作成", "この番組を元にルールフォームを開く", function () {
          closeProgramDialog();
          fillRuleFormFromProgram(program);
        }));
      }
    });
    if (row.childNodes.length > 0) {
      item.appendChild(row);
    }
  }

  function channelLink(channelID, label) {
    var button = document.createElement("button");
    button.type = "button";
    button.className = "channel-link";
    button.textContent = label || channelID || "不明なチャンネル";
    button.title = "このチャンネルの番組一覧を開く";
    button.addEventListener("click", function () {
      openChannelPrograms(channelID);
    });
    return button;
  }

  function isActiveProgram(program) {
    return Boolean(program && program.id && state.activeProgramID && program.id === state.activeProgramID);
  }

  function renderProgramRow(program, actions, showChannel) {
    program = decorateProgramState(program);
    var item = document.createElement("article");
    item.className = "program-row";
    item.classList.toggle("selected", isActiveProgram(program));
    item.tabIndex = 0;
    item.setAttribute("role", "group");
    item.setAttribute("aria-label", programTitle(program) + " の詳細を開く");
    item.addEventListener("dblclick", function (event) {
      if (!isEditableTarget(event.target)) {
        openProgramDialog(program);
      }
    });
    item.addEventListener("keydown", function (event) {
      if (event.target !== item) {
        return;
      }
      if (event.key === "Enter" || event.key === " ") {
        event.preventDefault();
        openProgramDialog(program);
      }
    });

    var title = document.createElement("button");
    title.type = "button";
    title.className = "program-title-button";
    title.textContent = programTitle(program);
    title.title = "番組詳細を開く";
    title.addEventListener("click", function () {
      openProgramDialog(program);
    });

    var meta = document.createElement("span");
    var parts = [formatTime(program.start)];
    if (showChannel) {
      parts.push(channelName(program));
    }
    parts.push(program.category);
    meta.textContent = parts.filter(Boolean).join(" / ");

    item.appendChild(title);
    item.appendChild(meta);
    renderProgramStateBadges(item, program);
    renderActions(item, program, actions);
    return item;
  }

  function renderProgramRowWithChannelLink(program, actions) {
    program = decorateProgramState(program);
    var item = document.createElement("article");
    item.className = "program-row";
    item.classList.toggle("selected", isActiveProgram(program));
    item.tabIndex = 0;
    item.setAttribute("role", "group");
    item.setAttribute("aria-label", programTitle(program) + " の詳細を開く");
    item.addEventListener("dblclick", function (event) {
      if (!isEditableTarget(event.target)) {
        openProgramDialog(program);
      }
    });
    item.addEventListener("keydown", function (event) {
      if (event.target !== item) {
        return;
      }
      if (event.key === "Enter" || event.key === " ") {
        event.preventDefault();
        openProgramDialog(program);
      }
    });

    var title = document.createElement("button");
    title.type = "button";
    title.className = "program-title-button";
    title.textContent = programTitle(program);
    title.title = "番組詳細を開く";
    title.addEventListener("click", function () {
      openProgramDialog(program);
    });

    var meta = document.createElement("span");
    var channelID = programChannelID(program);
    var parts = [formatTime(program.start), program.category];
    meta.textContent = parts.filter(Boolean).join(" / ");

    item.appendChild(title);
    item.appendChild(meta);
    if (channelID || channelName(program)) {
      var channelRow = document.createElement("div");
      channelRow.className = "inline-channel-row";
      channelRow.appendChild(channelLink(channelID, channelName(program) || channelID));
      item.appendChild(channelRow);
    }
    renderProgramStateBadges(item, program);
    renderActions(item, program, actions);
    return item;
  }

  function cloneProgram(program) {
    var item = {};
    Object.keys(program).forEach(function (key) {
      item[key] = program[key];
    });
    return item;
  }

  function programEnd(program) {
    return program.end || (program.start + scheduleMinimumProgramMinutes * 60 * 1000);
  }

  function scheduleWindowHours() {
    if (Object.prototype.hasOwnProperty.call(scheduleWindowHoursByMode, state.scheduleWindowMode)) {
      return scheduleWindowHoursByMode[state.scheduleWindowMode];
    }
    return scheduleWindowHoursByMode.day;
  }

  function channelProgramGroups() {
    var groups = [];
    (state.schedule || []).forEach(function (channel) {
      var channelID = scheduleChannelID(channel);
      var groupID = channelID || scheduleChannelName(channel) || "unknown";
      var displayName = scheduleChannelName(channel) || channelID || "不明なチャンネル";
      var group = {
        id: groupID,
        name: displayName,
        logo: Boolean(channelID && scheduleChannelHasLogo(channel)),
        programs: []
      };
      (channel.programs || []).forEach(function (program) {
        if (!program || !program.start) {
          return;
        }
        var item = cloneProgram(program);
        if (!item.channel && channel.channel) {
          item.channel = channel.channel;
        }
        if (!item.channel || typeof item.channel !== "object") {
          item.channel = { id: String(item.channel || groupID), name: displayName, type: scheduleChannelType(channel) };
        }
        item.channel.id = item.channel.id || groupID;
        item.channel.name = scheduleProgramChannelName(item, displayName);
        group.programs.push(item);
      });
      groups.push(group);
    });
    return groups;
  }

  function findChannelProgramGroup(channelID) {
    var groups = channelProgramGroups();
    var found = null;
    groups.some(function (group) {
      if (group.id === channelID) {
        found = group;
        return true;
      }
      return false;
    });
    return found;
  }

  function openChannelPrograms(channelID) {
    if (!channelID) {
      return;
    }
    state.channelProgramsChannel = channelID;
    state.channelProgramsGenre = "";
    window.location.hash = "channel-programs";
    renderChannelPrograms();
  }

  function renderList(id, items, emptyText, limit, actions) {
    var root = byId(id);
    if (!root) {
      return;
    }
    root.innerHTML = "";
    if (!items || items.length === 0) {
      root.className = "list empty";
      root.textContent = emptyText;
      return;
    }
    root.className = "list";
    items.slice(0, limit || 8).forEach(function (program) {
      root.appendChild(renderProgramRow(program, actions, true));
    });
  }

  function renderOnAirList() {
    var root = byId("onAirList");
    if (!root) {
      return;
    }
    var now = Date.now();
    var items = [];
    channelProgramGroups().forEach(function (group) {
      var current = null;
      group.programs.some(function (program) {
        if (program.start <= now && programEnd(program) > now) {
          current = program;
          return true;
        }
        return false;
      });
      if (current) {
        items.push(current);
      }
    });
    root.innerHTML = "";
    if (!items.length) {
      root.className = "list empty";
      root.textContent = "現在放送中の番組はありません";
      return;
    }
    root.className = "list";
    items.sort(function (a, b) {
      return channelName(a).localeCompare(channelName(b), "ja");
    }).forEach(function (program) {
      root.appendChild(renderProgramRowWithChannelLink(program, ["watch-recording-mp4", "playlist-recording", "preview-recording", "stop", "watch-channel-mp4"]));
    });
  }

  function renderSchedule() {
    var root = byId("scheduleList");
    if (!root) {
      return;
    }
    var channels = state.schedule || [];
    var channelOrder = [];
    var channelMeta = {};
    var channelGroups = [];
    var now = Date.now();
    var hours = scheduleWindowHours();
    var until = hours > 0 ? now + (hours * 60 * 60 * 1000) : 0;

    channels.forEach(function (channel) {
      var channelID = scheduleChannelID(channel);
      var groupID = channelID || scheduleChannelName(channel) || "unknown";
      var displayName = scheduleChannelName(channel) || channelID || "不明なチャンネル";
      if (state.scheduleChannel && channelID !== state.scheduleChannel) {
        return;
      }
      if (state.scheduleHiddenChannels.indexOf(groupID) >= 0 || state.scheduleHiddenChannels.indexOf(channelID) >= 0) {
        return;
      }
      if (state.scheduleType && scheduleChannelType(channel) !== state.scheduleType) {
        return;
      }
      if (!channelMeta[groupID]) {
        channelMeta[groupID] = {
          id: groupID,
          name: displayName,
          logo: Boolean(channelID && scheduleChannelHasLogo(channel)),
          programs: []
        };
        channelOrder.push(groupID);
      }
      (channel.programs || []).filter(function (program) {
        if (!program || !program.start) {
          return false;
        }
        if (program.end && program.end < now) {
          return false;
        }
        if (state.scheduleDay && dateKey(program.start) !== state.scheduleDay) {
          return false;
        }
        if (state.scheduleGenre && String(program.category || "") !== state.scheduleGenre) {
          return false;
        }
        return until === 0 || program.start <= until;
      }).forEach(function (program) {
        var item = cloneProgram(program);
        if (!item.channel && channel.channel) {
          item.channel = channel.channel;
        }
        if (item.channel) {
          item.channel.name = scheduleProgramChannelName(item, displayName);
        }
        channelMeta[groupID].programs.push(item);
      });
    });
    channelOrder.forEach(function (id) {
      if (channelMeta[id] && channelMeta[id].programs.length) {
        channelMeta[id].programs.sort(function (a, b) {
          return (a.start || 0) - (b.start || 0);
        });
        channelGroups.push(channelMeta[id]);
      }
    });
    renderScheduleChannelOptions(channels);
    renderScheduleFilterOptions(channels);
    renderScheduleChannelTools(channels);
    root.innerHTML = "";
    if (!channelGroups.length) {
      root.className = "list empty";
      root.textContent = "番組表データがありません";
      return;
    }
    renderScheduleGuide(root, channelGroups);
  }

  function renderScheduleGuide(root, channelGroups) {
    var firstStart = null;
    var lastEnd = null;
    var minuteHeight = scheduleMinutePixels;

    channelGroups.forEach(function (group) {
      group.programs.forEach(function (program) {
        if (firstStart === null || program.start < firstStart) {
          firstStart = program.start;
        }
        var end = programEnd(program);
        if (lastEnd === null || end > lastEnd) {
          lastEnd = end;
        }
      });
    });
    firstStart = Math.floor(firstStart / 3600000) * 3600000;
    lastEnd = Math.ceil(lastEnd / 3600000) * 3600000;
    var totalMinutes = Math.max(60, Math.round((lastEnd - firstStart) / 60000));
    var guideHeight = Math.max(360, Math.round(totalMinutes * minuteHeight));

    root.className = "schedule-guide";
    var scroll = document.createElement("div");
    scroll.className = "schedule-guide-scroll";
    scroll.setAttribute("role", "region");
    scroll.setAttribute("aria-label", "番組表");
    scroll.addEventListener("scroll", function () {
      state.scheduleGuideScroll = {
        left: scroll.scrollLeft,
        top: scroll.scrollTop
      };
    });

    var grid = document.createElement("div");
    grid.className = "schedule-guide-grid";
    grid.style.gridTemplateColumns = "48px repeat(" + channelGroups.length + ", minmax(132px, 1fr))";
    grid.style.minWidth = (48 + channelGroups.length * 132) + "px";

    var corner = document.createElement("div");
    corner.className = "schedule-corner";
    corner.textContent = "時刻";
    grid.appendChild(corner);

    channelGroups.forEach(function (group) {
      var heading = document.createElement("div");
      heading.className = "schedule-channel-head";
      if (group.logo) {
        var logo = document.createElement("img");
        logo.className = "schedule-channel-logo";
        logo.src = channelURL(group.id, "logo", "png");
        logo.alt = "";
        logo.loading = "lazy";
        heading.appendChild(logo);
      }
      heading.appendChild(channelLink(group.id, group.name));
      grid.appendChild(heading);
    });

    var timeRail = document.createElement("div");
    timeRail.className = "schedule-time-rail";
    timeRail.style.height = guideHeight + "px";
    for (var mark = firstStart; mark <= lastEnd; mark += 3600000) {
      var label = document.createElement("span");
      label.style.top = Math.round(((mark - firstStart) / 60000) * minuteHeight) + "px";
      label.textContent = formatTime(mark);
      timeRail.appendChild(label);
    }
    grid.appendChild(timeRail);

    channelGroups.forEach(function (group) {
      var lane = document.createElement("div");
      lane.className = "schedule-channel-lane";
      lane.style.height = guideHeight + "px";
      group.programs.forEach(function (program) {
        lane.appendChild(renderScheduleCard(program, firstStart, minuteHeight));
      });
      var now = Date.now();
      if (now >= firstStart && now <= lastEnd) {
        var line = document.createElement("div");
        line.className = "schedule-now-line";
        line.style.top = Math.round(((now - firstStart) / 60000) * minuteHeight) + "px";
        lane.appendChild(line);
      }
      grid.appendChild(lane);
    });
    scroll.appendChild(grid);
    root.appendChild(scroll);
    window.setTimeout(function () {
      scroll.scrollLeft = state.scheduleGuideScroll.left || 0;
      scroll.scrollTop = state.scheduleGuideScroll.top || 0;
    }, 0);
  }

  function renderChannelActions(channelID) {
    var row = document.createElement("div");
    row.className = "channel-actions";
    if (!channelID || channelID === "unknown") {
      return row;
    }
    row.appendChild(actionButton("視聴", "チャンネルを変換視聴で開く", function () {
      openURL(channelURL(channelID, "watch", "mp4"));
    }));
    row.appendChild(actionButton("XSPF", "チャンネルのプレイリストを開く", function () {
      openURL(channelURL(channelID, "watch", "xspf"));
    }));
    return row;
  }

  function renderScheduleChannelTools(channels) {
    var root = byId("scheduleChannelTools");
    if (!root) {
      return;
    }
    root.innerHTML = "";
    root.hidden = true;
    if (!state.scheduleStreamChannel) {
      return;
    }
    var selected = null;
    channels.some(function (channel) {
      if (scheduleChannelID(channel) === state.scheduleStreamChannel) {
        selected = channel;
        return true;
      }
      return false;
    });
    if (!selected) {
      return;
    }
    var label = document.createElement("span");
    label.className = "channel-tool-label";
    label.textContent = scheduleChannelName(selected) || state.scheduleStreamChannel;
    root.appendChild(label);
    root.appendChild(renderChannelActions(state.scheduleStreamChannel));
    root.hidden = false;
  }

  function categoryClass(category) {
    var value = String(category || "").toLowerCase();
    if (value.indexOf("anime") >= 0 || value.indexOf("アニメ") >= 0) {
      return " category-anime";
    }
    if (value.indexOf("movie") >= 0 || value.indexOf("映画") >= 0) {
      return " category-movie";
    }
    if (value.indexOf("news") >= 0 || value.indexOf("ニュース") >= 0 || value.indexOf("報道") >= 0) {
      return " category-news";
    }
    if (value.indexOf("sports") >= 0 || value.indexOf("スポーツ") >= 0) {
      return " category-sports";
    }
    if (value.indexOf("drama") >= 0 || value.indexOf("ドラマ") >= 0) {
      return " category-drama";
    }
    if (value.indexOf("music") >= 0 || value.indexOf("音楽") >= 0) {
      return " category-music";
    }
    return "";
  }

  function renderScheduleCard(program, timelineStart, minuteHeight) {
    program = decorateProgramState(program);
    var card = document.createElement("article");
    card.className = "schedule-card" + categoryClass(program.category);
    card.classList.toggle("recording", Boolean(program.isRecording));
    card.classList.toggle("reserved", Boolean(program.isReserved && !program.isRecording));
    var end = programEnd(program);
    var top = Math.max(0, Math.round(((program.start - timelineStart) / 60000) * minuteHeight));
    var height = Math.max(24, Math.round(((end - program.start) / 60000) * minuteHeight));
    card.style.top = top + "px";
    card.style.height = height + "px";
    card.classList.toggle("selected", isActiveProgram(program));
    card.title = [programTitle(program), program.detail || program.description || ""].filter(Boolean).join("\n");
    card.tabIndex = 0;
    card.setAttribute("role", "button");
    card.setAttribute("aria-label", programTitle(program) + " の詳細を開く");

    var time = document.createElement("span");
    time.className = "schedule-card-time";
    time.textContent = formatClock(program.start) + "-" + formatClock(end);

    var title = document.createElement("strong");
    title.textContent = programTitle(program);

    var meta = document.createElement("span");
    meta.className = "schedule-card-meta";
    var stateLabel = program.isRecording ? "録画中" : (program.isReserved ? (program.isManualReserved ? "手動予約" : "予約済み") : "");
    meta.textContent = [program.category || "未分類", stateLabel].filter(Boolean).join(" / ");

    card.appendChild(time);
    card.appendChild(title);
    card.appendChild(meta);
    card.addEventListener("click", function () {
      openProgramDialog(program);
    });
    card.addEventListener("keydown", function (event) {
      if (event.key === "Enter" || event.key === " ") {
        event.preventDefault();
        openProgramDialog(program);
      }
    });
    return card;
  }

  function openProgramDialog(program) {
    program = decorateProgramState(program);
    var dialog = byId("programDialog");
    var title = byId("programDialogTitle");
    var meta = byId("programDialogMeta");
    var description = byId("programDialogDescription");
    var actions = byId("programDialogActions");
    var end = programEnd(program);
    state.selectedProgram = program;
    state.activeProgramID = program && program.id ? program.id : "";
    text(title, programTitle(program));
    text(meta, [formatTime(program.start) + " - " + formatTime(end), channelName(program), program.category, formatDuration(program.start, end)].filter(Boolean).join(" / "));
    text(description, program.detail || program.description || "番組説明はありません。");
    if (actions) {
      actions.innerHTML = "";
      renderActions(actions, program, ["reserve", "unreserve", "skip", "unskip", "watch-recording-mp4", "playlist-recording", "preview-recording", "stop", "watch-channel-mp4", "open-channel-programs", "create-rule-from-program"]);
    }
    programDialogReturnFocus = rememberFocus();
    if (dialog && dialog.showModal) {
      dialog.showModal();
      focusFirstDialogControl(dialog);
    } else if (dialog) {
      dialog.setAttribute("open", "open");
      focusFirstDialogControl(dialog);
    }
  }

  function closeProgramDialog() {
    var dialog = byId("programDialog");
    if (dialog && dialog.close) {
      dialog.close();
    } else if (dialog) {
      dialog.removeAttribute("open");
    }
  }

  function renderScheduleChannelOptions(channels) {
    var options = [];
    channels.forEach(function (channel) {
      var id = scheduleChannelID(channel);
      if (!id) {
        return;
      }
      options.push({
        id: id,
        name: scheduleChannelName(channel) || id
      });
    });

    var filterSelect = byId("scheduleChannel");
    if (filterSelect) {
      var currentFilter = state.scheduleChannel;
      filterSelect.innerHTML = "";
      var all = document.createElement("option");
      all.value = "";
      all.textContent = "すべて";
      filterSelect.appendChild(all);
      options.forEach(function (entry) {
        var option = document.createElement("option");
        option.value = entry.id;
        option.textContent = entry.name;
        filterSelect.appendChild(option);
      });
      filterSelect.value = currentFilter;
      if (filterSelect.value !== currentFilter) {
        state.scheduleChannel = "";
        filterSelect.value = "";
      }
    }

    if (!state.scheduleStreamChannel && options.length) {
      state.scheduleStreamChannel = options[0].id;
    }
    var streamSelect = byId("scheduleStreamChannel");
    if (streamSelect) {
      var currentStream = state.scheduleStreamChannel;
      streamSelect.innerHTML = "";
      options.forEach(function (entry) {
        var option = document.createElement("option");
        option.value = entry.id;
        option.textContent = entry.name;
        streamSelect.appendChild(option);
      });
      streamSelect.value = currentStream;
      if (streamSelect.value !== currentStream) {
        state.scheduleStreamChannel = options.length ? options[0].id : "";
        streamSelect.value = state.scheduleStreamChannel;
      }
    }
  }

  function renderScheduleFilterOptions(channels) {
    renderScheduleDayOptions();
    renderScheduleGenreOptions(channels);
    renderScheduleHiddenChannelOptions(channels);
  }

  function renderScheduleDayOptions() {
    var select = byId("scheduleDay");
    if (!select) {
      return;
    }
    var current = state.scheduleDay;
    select.innerHTML = "";
    var all = document.createElement("option");
    all.value = "";
    all.textContent = "すべて";
    select.appendChild(all);
    var today = startOfDay(Date.now());
    for (var i = 0; i < 7; i += 1) {
      var day = today + i * 86400000;
      var option = document.createElement("option");
      option.value = dateKey(day);
      option.textContent = dayLabel(day);
      select.appendChild(option);
    }
    select.value = current;
  }

  function renderScheduleGenreOptions(channels) {
    var select = byId("scheduleGenre");
    if (!select) {
      return;
    }
    var current = state.scheduleGenre;
    var genres = {};
    channels.forEach(function (channel) {
      (channel.programs || []).forEach(function (program) {
        if (program && program.category) {
          genres[String(program.category)] = true;
        }
      });
    });
    select.innerHTML = "";
    var all = document.createElement("option");
    all.value = "";
    all.textContent = "全ジャンル";
    select.appendChild(all);
    Object.keys(genres).sort().forEach(function (genre) {
      var option = document.createElement("option");
      option.value = genre;
      option.textContent = genre;
      select.appendChild(option);
    });
    select.value = current;
    if (select.value !== current) {
      state.scheduleGenre = "";
      select.value = "";
    }
  }

  function renderScheduleHiddenChannelOptions(channels) {
    var select = byId("scheduleHiddenChannel");
    if (!select) {
      return;
    }
    var current = select.value;
    select.innerHTML = "";
    var empty = document.createElement("option");
    empty.value = "";
    empty.textContent = state.scheduleHiddenChannels.length ? state.scheduleHiddenChannels.length + "ch非表示中" : "選択";
    select.appendChild(empty);
    channels.forEach(function (channel) {
      var id = scheduleChannelID(channel) || scheduleChannelName(channel);
      if (!id) {
        return;
      }
      var option = document.createElement("option");
      option.value = id;
      option.textContent = (state.scheduleHiddenChannels.indexOf(id) >= 0 ? "非表示: " : "表示中: ") + (scheduleChannelName(channel) || id);
      select.appendChild(option);
    });
    select.value = current;
    if (select.value !== current) {
      select.value = "";
    }
  }

  function renderChannelPrograms() {
    var root = byId("channelProgramsList");
    if (!root) {
      return;
    }
    var group = findChannelProgramGroup(state.channelProgramsChannel);
    var title = byId("channelProgramsTitle");
    var tools = byId("channelProgramsTools");
    root.innerHTML = "";
    if (!group) {
      if (title) {
        title.textContent = "チャンネル番組一覧";
      }
      if (tools) {
        tools.hidden = true;
        tools.innerHTML = "";
      }
      renderChannelProgramsGenreOptions([]);
      updateListFilterSummary("channelProgramsFilterSummary", 0, 0);
      root.className = "list empty";
      root.textContent = "チャンネルを選択してください";
      return;
    }

    if (title) {
      title.textContent = group.name + " の番組一覧";
    }
    if (tools) {
      tools.hidden = false;
      tools.innerHTML = "";
      var label = document.createElement("span");
      label.className = "channel-tool-label";
      label.textContent = group.name;
      tools.appendChild(label);
      tools.appendChild(renderChannelActions(group.id));
    }

    renderChannelProgramsGenreOptions(group.programs);
    var programs = group.programs.filter(function (program) {
      if (programEnd(program) < Date.now()) {
        return false;
      }
      return !state.channelProgramsGenre || String(program.category || "") === state.channelProgramsGenre;
    });
    state.listFilters.channelPrograms.category = state.channelProgramsGenre;
    programs = filteredPrograms(programs, "channelPrograms");
    updateListFilterSummary("channelProgramsFilterSummary", programs.length, group.programs.length);
    programs.sort(function (a, b) {
      if (state.channelProgramsSort === "category") {
        return String(a.category || "").localeCompare(String(b.category || ""), "ja") || (a.start || 0) - (b.start || 0);
      }
      if (state.channelProgramsSort === "title") {
        return programTitle(a).localeCompare(programTitle(b), "ja") || (a.start || 0) - (b.start || 0);
      }
      if (state.channelProgramsSort === "duration") {
        return (programEnd(a) - a.start) - (programEnd(b) - b.start) || (a.start || 0) - (b.start || 0);
      }
      return (a.start || 0) - (b.start || 0);
    });

    if (!programs.length) {
      root.className = "list empty";
      root.textContent = "条件に一致する番組はありません";
      return;
    }
    root.className = "list";
    programs.forEach(function (program) {
      root.appendChild(renderProgramRow(program, ["reserve", "unreserve", "skip", "unskip", "watch-recording-mp4", "playlist-recording", "preview-recording", "stop", "watch-channel-mp4", "create-rule-from-program"], false));
    });
  }

  function renderChannelProgramsGenreOptions(programs) {
    var select = byId("channelProgramsGenre");
    if (!select) {
      return;
    }
    var current = state.channelProgramsGenre;
    var genres = {};
    (programs || []).forEach(function (program) {
      if (program && program.category) {
        genres[String(program.category)] = true;
      }
    });
    select.innerHTML = "";
    var all = document.createElement("option");
    all.value = "";
    all.textContent = "全ジャンル";
    select.appendChild(all);
    Object.keys(genres).sort().forEach(function (genre) {
      var option = document.createElement("option");
      option.value = genre;
      option.textContent = genre;
      select.appendChild(option);
    });
    select.value = current;
    if (select.value !== current) {
      state.channelProgramsGenre = "";
      select.value = "";
    }
  }

  function renderRules() {
    var root = byId("ruleList");
    if (!root) {
      return;
    }
    root.innerHTML = "";
    if (!state.rules || state.rules.length === 0) {
      root.className = "list empty";
      root.textContent = "ルールはありません";
      updateListFilterSummary("ruleListFilterSummary", 0, 0);
      return;
    }
    var filtered = filteredRules(state.rules);
    updateListFilterSummary("ruleListFilterSummary", filtered.length, state.rules.length);
    if (!filtered.length) {
      root.className = "list empty";
      root.textContent = "条件に一致するルールはありません";
      return;
    }
    root.className = "list";
    filtered.forEach(function (entry) {
      var rule = entry.rule;
      var index = entry.index;
      var item = document.createElement("article");
      item.className = "program-row";
      item.tabIndex = 0;

      var title = document.createElement("strong");
      title.textContent = "#" + index + (rule.isDisabled ? " 無効" : " 有効");

      var meta = document.createElement("span");
      meta.textContent = ruleSummary(rule);

      var row = document.createElement("div");
      row.className = "row-actions";
      row.appendChild(actionButton(rule.isDisabled ? "有効化" : "無効化", "ルールの有効状態を切り替え", function () {
        runAction("rules/" + index + "/" + (rule.isDisabled ? "enable" : "disable") + ".json", "PUT");
      }));
      row.appendChild(actionButton("JSON編集", "このルールをエディタに読み込む", function () {
        var editor = byId("ruleEditor");
        if (editor) {
          editor.value = JSON.stringify(rule, null, 2);
          state.editingRuleIndex = index;
          renderRuleEditorState();
          editor.focus();
        }
      }));
      row.appendChild(actionButton("フォーム編集", "このルールをフォームに読み込む", function () {
        fillRuleFormFromRule(rule, index);
      }));
      row.appendChild(actionButton("削除", "このルールを削除", function () {
        runAction("rules/" + index + ".json", "DELETE", "このルールを削除しますか？", {
          danger: true,
          meta: ruleSummary(rule),
          okLabel: "削除",
          title: "ルール削除の確認"
        });
      }));

      item.appendChild(title);
      item.appendChild(meta);
      item.appendChild(row);
      root.appendChild(item);
    });
  }

  function settingValue(value) {
    if (value === undefined || value === null || value === "") {
      return "未設定";
    }
    if (Array.isArray(value)) {
      return value.length ? value.join(", ") : "なし";
    }
    if (typeof value === "boolean") {
      return value ? "有効" : "無効";
    }
    return String(value);
  }

  function setControlValue(id, value) {
    var control = byId(id);
    if (!control) {
      return;
    }
    if (control.type === "checkbox") {
      control.checked = Boolean(value);
      return;
    }
    if (Array.isArray(value)) {
      control.value = value.join(", ");
      return;
    }
    control.value = value === undefined || value === null ? "" : String(value);
  }

  function setBooleanSelectValue(id, value) {
    var control = byId(id);
    if (!control) {
      return;
    }
    if (value === undefined || value === null) {
      control.value = "";
      return;
    }
    control.value = value ? "true" : "false";
  }

  function renderConfigForm() {
    if (state.configEditorDirty) {
      return;
    }
    var cfg = state.config || {};
    setControlValue("configMirakurunPath", cfg.mirakurunPath);
    setControlValue("configSchedulerMirakurunPath", cfg.schedulerMirakurunPath);
    setControlValue("configRecordedDir", cfg.recordedDir);
    setControlValue("configRecordedFormat", cfg.recordedFormat);
    setControlValue("configWuiHost", cfg.wuiHost);
    setControlValue("configWuiPort", cfg.wuiPort);
    setControlValue("configWuiOpenServer", cfg.wuiOpenServer);
    setControlValue("configWuiOpenHost", cfg.wuiOpenHost);
    setControlValue("configWuiOpenPort", cfg.wuiOpenPort);
    setControlValue("configNormalizationForm", cfg.normalizationForm);
    setControlValue("configStorageLowSpaceThresholdMB", cfg.storageLowSpaceThresholdMB);
    setControlValue("configStorageLowSpaceAction", cfg.storageLowSpaceAction);
    setControlValue("configExcludeServices", cfg.excludeServices);
    setControlValue("configUid", cfg.uid);
    setControlValue("configGid", cfg.gid);
    setControlValue("configWuiUsers", cfg.wuiUsers);
    setControlValue("configWuiAllowCountries", cfg.wuiAllowCountries);
    setControlValue("configServiceOrder", cfg.serviceOrder);
    setBooleanSelectValue("configWuiXFF", cfg.wuiXFF);
    setBooleanSelectValue("configWuiMdnsAdvertisement", cfg.wuiMdnsAdvertisement);
    setBooleanSelectValue("configVaapiEnabled", cfg.vaapiEnabled);
    setControlValue("configVaapiDevice", cfg.vaapiDevice);
    setControlValue("configRecordingPriority", cfg.recordingPriority);
    setControlValue("configConflictedPriority", cfg.conflictedPriority);
    setControlValue("configWuiTlsKeyPath", cfg.wuiTlsKeyPath);
    setControlValue("configWuiTlsCertPath", cfg.wuiTlsCertPath);
    setControlValue("configWuiTlsCaPath", cfg.wuiTlsCaPath);
    setControlValue("configWuiTlsPassphrase", cfg.wuiTlsPassphrase);
    setBooleanSelectValue("configWuiTlsRequestCert", cfg.wuiTlsRequestCert);
    setBooleanSelectValue("configWuiTlsRejectUnauthorized", cfg.wuiTlsRejectUnauthorized);
    setControlValue("configStorageLowSpaceNotifyTo", cfg.storageLowSpaceNotifyTo);
    setControlValue("configStorageLowSpaceCommand", cfg.storageLowSpaceCommand);
    setControlValue("configSchedulerStartCommand", cfg.schedulerStartCommand);
    setControlValue("configSchedulerEndCommand", cfg.schedulerEndCommand);
    setControlValue("configEpgStartCommand", cfg.epgStartCommand);
    setControlValue("configEpgEndCommand", cfg.epgEndCommand);
    setControlValue("configConflictCommand", cfg.conflictCommand);
    setControlValue("configRecordedCommand", cfg.recordedCommand);
  }

  function renderSettings() {
    var root = byId("settingsList");
    if (!root) {
      return;
    }
    var cfg = state.config || {};
    var rows = [
      ["Mirakurun", cfg.mirakurunPath || cfg.schedulerMirakurunPath],
      ["旧Mirakurun", cfg.schedulerMirakurunPath],
      ["実行ユーザーID", cfg.uid],
      ["実行グループID", cfg.gid],
      ["録画ディレクトリ", cfg.recordedDir],
      ["録画ファイル名", cfg.recordedFormat],
      ["WUIホスト", cfg.wuiHost],
      ["WUIポート", cfg.wuiPort],
      ["公開WUI", cfg.wuiOpenServer],
      ["公開ホスト", cfg.wuiOpenHost],
      ["公開ポート", cfg.wuiOpenPort],
      ["WUI X-Forwarded-For", cfg.wuiXFF],
      ["mDNS広告", cfg.wuiMdnsAdvertisement],
      ["TLS", Boolean(cfg.wuiTlsKeyPath || cfg.wuiTlsCertPath)],
      ["TLS CA", cfg.wuiTlsCaPath],
      ["TLSクライアント証明書要求", cfg.wuiTlsRequestCert],
      ["TLS未認証拒否", cfg.wuiTlsRejectUnauthorized],
      ["WUIユーザー", cfg.wuiUsers],
      ["許可国コード", cfg.wuiAllowCountries],
      ["サービス順", cfg.serviceOrder],
      ["VAAPI", cfg.vaapiEnabled],
      ["VAAPIデバイス", cfg.vaapiDevice],
      ["除外サービス", cfg.excludeServices],
      ["録画優先度", cfg.recordingPriority],
      ["競合優先度", cfg.conflictedPriority],
      ["空き容量しきい値MB", cfg.storageLowSpaceThresholdMB],
      ["空き容量不足時の動作", cfg.storageLowSpaceAction],
      ["空き容量通知先", cfg.storageLowSpaceNotifyTo],
      ["空き容量コマンド", cfg.storageLowSpaceCommand],
      ["正規化", cfg.normalizationForm],
      ["スケジューラ開始コマンド", cfg.schedulerStartCommand],
      ["スケジューラ終了コマンド", cfg.schedulerEndCommand],
      ["EPG開始コマンド", cfg.epgStartCommand],
      ["EPG終了コマンド", cfg.epgEndCommand],
      ["競合コマンド", cfg.conflictCommand],
      ["録画完了コマンド", cfg.recordedCommand]
    ];
    root.innerHTML = "";
    rows.forEach(function (row) {
      var key = document.createElement("dt");
      var value = document.createElement("dd");
      key.textContent = row[0];
      value.textContent = settingValue(row[1]);
      root.appendChild(key);
      root.appendChild(value);
    });
    renderConfigForm();
    renderConfigEditor();
    renderStorage();
  }

  function renderConfigEditor(force) {
    var editor = byId("configEditor");
    if (!editor) {
      return;
    }
    if (!force && state.configEditorDirty) {
      return;
    }
    editor.value = JSON.stringify(state.config || {}, null, 2);
    state.configEditorDirty = false;
  }

  function renderStorage() {
    var root = byId("storageList");
    if (!root) {
      return;
    }
    var storage = state.storage || {};
    var rows = [
      ["録画ファイル", formatBytes(storage.recorded)],
      ["ディスク容量", formatBytes(storage.size)],
      ["使用量", formatBytes(storage.used)],
      ["空き容量", formatBytes(storage.avail)]
    ];
    root.innerHTML = "";
    rows.forEach(function (row) {
      var key = document.createElement("dt");
      var value = document.createElement("dd");
      key.textContent = row[0];
      value.textContent = row[1];
      root.appendChild(key);
      root.appendChild(value);
    });
  }

  function forceScheduler() {
    runAction("scheduler/force.json", "PUT", "スケジューラを実行しますか？");
  }

  function readConfigEditorObject() {
    var editor = byId("configEditor");
    if (!editor) {
      return null;
    }
    try {
      var config = JSON.parse(editor.value);
      if (!config || Array.isArray(config) || typeof config !== "object") {
        showError(new Error("設定JSONはオブジェクトにしてください"));
        return null;
      }
      return config;
    } catch (error) {
      showError(new Error("設定JSONが正しくありません"));
      return null;
    }
  }

  function readConfigEditor() {
    var config = readConfigEditorObject();
    return config ? JSON.stringify(config, null, 2) : null;
  }

  function controlString(id) {
    var control = byId(id);
    return control ? control.value.trim() : "";
  }

  function setOptionalString(config, field, id) {
    var value = controlString(id);
    if (value) {
      config[field] = value;
    } else {
      delete config[field];
    }
  }

  function setOptionalStringOrInteger(config, field, id, label) {
    var value = controlString(id);
    if (!value) {
      delete config[field];
      return true;
    }
    if (/^-?\d+$/.test(value)) {
      config[field] = Number(value);
      return true;
    }
    if (value.toLowerCase() === "null") {
      config[field] = null;
      return true;
    }
    if (value.indexOf(",") !== -1) {
      showError(new Error(label + " はユーザー名/グループ名、整数ID、または null を入力してください"));
      return false;
    }
    config[field] = value;
    return true;
  }

  function setOptionalPort(config, field, id) {
    var value = controlString(id);
    if (!value) {
      delete config[field];
      return true;
    }
    var number = Number(value);
    if (!Number.isInteger(number) || number < 1 || number > 65535) {
      showError(new Error(field + " は 1-65535 の整数にしてください"));
      return false;
    }
    config[field] = number;
    return true;
  }

  function setOptionalNonNegativeInteger(config, field, id) {
    var value = controlString(id);
    if (!value) {
      delete config[field];
      return true;
    }
    var number = Number(value);
    if (!Number.isInteger(number) || number < 0) {
      showError(new Error(field + " は 0 以上の整数にしてください"));
      return false;
    }
    config[field] = number;
    return true;
  }

  function setOptionalInteger(config, field, id) {
    var value = controlString(id);
    if (!value) {
      delete config[field];
      return true;
    }
    var number = Number(value);
    if (!Number.isInteger(number)) {
      showError(new Error(field + " は整数にしてください"));
      return false;
    }
    config[field] = number;
    return true;
  }

  function setOptionalBooleanSelect(config, field, id) {
    var value = controlString(id);
    if (!value) {
      delete config[field];
      return true;
    }
    if (value !== "true" && value !== "false") {
      showError(new Error(field + " は有効または無効を選択してください"));
      return false;
    }
    config[field] = value === "true";
    return true;
  }

  function setOptionalStringList(config, field, id) {
    var values = splitList(controlString(id));
    if (values.length) {
      config[field] = values;
    } else {
      delete config[field];
    }
  }

  function setOptionalPositiveIntegerList(config, field, id, label) {
    var values = splitList(controlString(id)).map(function (value) {
      return Number(value);
    });
    if (values.some(function (value) {
      return !Number.isInteger(value) || value <= 0;
    })) {
      showError(new Error(label + "はカンマ区切りの正の整数にしてください"));
      return false;
    }
    if (values.length) {
      config[field] = values;
    } else {
      delete config[field];
    }
    return true;
  }

  function applyConfigFormToEditor(silent) {
    var config = readConfigEditorObject();
    var openServer = byId("configWuiOpenServer");
    if (!config) {
      return null;
    }
    setOptionalString(config, "mirakurunPath", "configMirakurunPath");
    setOptionalString(config, "schedulerMirakurunPath", "configSchedulerMirakurunPath");
    setOptionalString(config, "recordedDir", "configRecordedDir");
    setOptionalString(config, "recordedFormat", "configRecordedFormat");
    setOptionalString(config, "wuiHost", "configWuiHost");
    if (!setOptionalPort(config, "wuiPort", "configWuiPort")) {
      return null;
    }
    if (openServer) {
      config.wuiOpenServer = openServer.checked;
    }
    setOptionalString(config, "wuiOpenHost", "configWuiOpenHost");
    if (!setOptionalPort(config, "wuiOpenPort", "configWuiOpenPort")) {
      return null;
    }
    setOptionalString(config, "normalizationForm", "configNormalizationForm");
    if (!setOptionalNonNegativeInteger(config, "storageLowSpaceThresholdMB", "configStorageLowSpaceThresholdMB")) {
      return null;
    }
    setOptionalString(config, "storageLowSpaceAction", "configStorageLowSpaceAction");
    var excludeServices = splitList(controlString("configExcludeServices")).map(function (value) {
      return Number(value);
    });
    if (excludeServices.some(function (value) {
      return !Number.isInteger(value) || value <= 0;
    })) {
      showError(new Error("除外サービスはカンマ区切りの正の整数にしてください"));
      return null;
    }
    if (excludeServices.length) {
      config.excludeServices = excludeServices;
    } else {
      delete config.excludeServices;
    }
    if (!setOptionalStringOrInteger(config, "uid", "configUid", "実行ユーザーID")) {
      return null;
    }
    if (!setOptionalStringOrInteger(config, "gid", "configGid", "実行グループID")) {
      return null;
    }
    setOptionalStringList(config, "wuiUsers", "configWuiUsers");
    setOptionalStringList(config, "wuiAllowCountries", "configWuiAllowCountries");
    if (!setOptionalPositiveIntegerList(config, "serviceOrder", "configServiceOrder", "サービス順")) {
      return null;
    }
    if (!setOptionalBooleanSelect(config, "wuiXFF", "configWuiXFF")) {
      return null;
    }
    if (!setOptionalBooleanSelect(config, "wuiMdnsAdvertisement", "configWuiMdnsAdvertisement")) {
      return null;
    }
    if (!setOptionalBooleanSelect(config, "vaapiEnabled", "configVaapiEnabled")) {
      return null;
    }
    setOptionalString(config, "vaapiDevice", "configVaapiDevice");
    if (!setOptionalInteger(config, "recordingPriority", "configRecordingPriority")) {
      return null;
    }
    if (!setOptionalInteger(config, "conflictedPriority", "configConflictedPriority")) {
      return null;
    }
    setOptionalString(config, "wuiTlsKeyPath", "configWuiTlsKeyPath");
    setOptionalString(config, "wuiTlsCertPath", "configWuiTlsCertPath");
    setOptionalString(config, "wuiTlsCaPath", "configWuiTlsCaPath");
    setOptionalString(config, "wuiTlsPassphrase", "configWuiTlsPassphrase");
    if (!setOptionalBooleanSelect(config, "wuiTlsRequestCert", "configWuiTlsRequestCert")) {
      return null;
    }
    if (!setOptionalBooleanSelect(config, "wuiTlsRejectUnauthorized", "configWuiTlsRejectUnauthorized")) {
      return null;
    }
    setOptionalString(config, "storageLowSpaceNotifyTo", "configStorageLowSpaceNotifyTo");
    setOptionalString(config, "storageLowSpaceCommand", "configStorageLowSpaceCommand");
    setOptionalString(config, "schedulerStartCommand", "configSchedulerStartCommand");
    setOptionalString(config, "schedulerEndCommand", "configSchedulerEndCommand");
    setOptionalString(config, "epgStartCommand", "configEpgStartCommand");
    setOptionalString(config, "epgEndCommand", "configEpgEndCommand");
    setOptionalString(config, "conflictCommand", "configConflictCommand");
    setOptionalString(config, "recordedCommand", "configRecordedCommand");
    var editor = byId("configEditor");
    if (editor) {
      editor.value = JSON.stringify(config, null, 2);
      state.configEditorDirty = true;
    }
    if (!silent) {
      setBusy("フォームの内容を設定JSONに反映しました");
    }
    return config;
  }

  function saveConfigFromForm() {
    var config = applyConfigFormToEditor(true);
    if (!config) {
      return;
    }
    confirmAction("フォームの内容で config.json を保存しますか？", {
      danger: false,
      okLabel: "保存",
      title: "設定保存の確認"
    }).then(function (confirmed) {
      if (!confirmed) {
        return;
      }
      setBusy("設定保存中");
      sendConfigJSON(JSON.stringify(config, null, 2)).then(function (savedConfig) {
        state.config = savedConfig || {};
        state.configEditorDirty = false;
        render();
        setBusy("設定を保存しました");
      }).catch(showError);
    });
  }

  function saveConfigFromEditor() {
    var raw = readConfigEditor();
    if (!raw) {
      return;
    }
    confirmAction("config.json を保存しますか？", {
      danger: false,
      okLabel: "保存",
      title: "設定保存の確認"
    }).then(function (confirmed) {
      if (!confirmed) {
        return;
      }
      setBusy("設定保存中");
      sendConfigJSON(raw).then(function (config) {
        state.config = config || {};
        state.configEditorDirty = false;
        render();
        setBusy("設定を保存しました");
      }).catch(showError);
    });
  }

  function resetConfigEditor() {
    state.configEditorDirty = false;
    renderConfigForm();
    renderConfigEditor(true);
    setBusy("設定JSONを再読み込みしました");
  }

  function addRuleFromEditor() {
    var rule = readRuleEditor();
    if (!rule) {
      return;
    }
    confirmAction("JSONエディタの内容でルールを追加しますか？", {
      danger: false,
      okLabel: "追加",
      title: "ルール追加の確認"
    }).then(function (confirmed) {
      if (!confirmed) {
        return;
      }
      setBusy("処理中");
      sendJSON("rules.json", "POST", rule).then(function () {
        state.editingRuleIndex = null;
        renderRuleEditorState();
        refresh();
      }).catch(showError);
    });
  }

  function saveRuleFromEditor() {
    if (state.editingRuleIndex === null || state.editingRuleIndex === undefined) {
      showError(new Error("ルールが選択されていません"));
      return;
    }
    var rule = readRuleEditor();
    if (!rule) {
      return;
    }
    confirmAction("JSONエディタの内容でルール #" + state.editingRuleIndex + " を保存しますか？", {
      danger: false,
      okLabel: "保存",
      title: "ルール保存の確認"
    }).then(function (confirmed) {
      if (!confirmed) {
        return;
      }
      setBusy("処理中");
      sendJSON("rules/" + state.editingRuleIndex + ".json", "PUT", rule).then(function () {
        state.editingRuleIndex = null;
        renderRuleEditorState();
        refresh();
      }).catch(showError);
    });
  }

  function readRuleEditor() {
    var editor = byId("ruleEditor");
    if (!editor) {
      return null;
    }
    try {
      return JSON.parse(editor.value);
    } catch (error) {
      showError(new Error("ルールJSONが正しくありません"));
      return null;
    }
  }

  function renderRuleEditorState() {
    var saveButton = byId("saveRuleButton");
    if (saveButton) {
      saveButton.disabled = state.editingRuleIndex === null || state.editingRuleIndex === undefined;
      saveButton.title = saveButton.disabled ? "先にルールのJSON編集を選択してください" : "ルール #" + state.editingRuleIndex + " を保存";
    }
  }

  function splitList(value) {
    return (value || "").split(",").map(function (part) {
      return part.trim();
    }).filter(Boolean);
  }

  function readExtraRuleJSON(input) {
    if (!input || !input.value.trim()) {
      return {};
    }
    try {
      var extra = JSON.parse(input.value);
      if (!extra || Array.isArray(extra) || typeof extra !== "object") {
        showError(new Error("追加JSONはオブジェクトにしてください"));
        return null;
      }
      return extra;
    } catch (error) {
      showError(new Error("追加JSONが正しくありません"));
      return null;
    }
  }

  function setFormValue(id, value) {
    var control = byId(id);
    if (!control) {
      return;
    }
    if (control.type === "checkbox") {
      control.checked = Boolean(value);
      return;
    }
    control.value = value === undefined || value === null ? "" : String(value);
  }

  function setListFormValue(id, value) {
    if (Array.isArray(value)) {
      setFormValue(id, value.join(", "));
      return;
    }
    setFormValue(id, value);
  }

  function clearRuleForm() {
    [
      "ruleTitle",
      "ruleIgnoreTitle",
      "ruleDescription",
      "ruleIgnoreDescription",
      "ruleSid",
      "ruleCategory",
      "ruleCategories",
      "ruleChannels",
      "ruleIgnoreChannels",
      "ruleFlags",
      "ruleIgnoreFlags",
      "ruleDurationMin",
      "ruleDurationMax",
      "ruleHourStart",
      "ruleHourEnd",
      "ruleRecordedFormat",
      "ruleExtraJson"
    ].forEach(function (id) {
      setFormValue(id, "");
    });
    setFormValue("ruleType", "");
    setFormValue("ruleDisabled", false);
  }

  function renderRuleFormState() {
    var button = byId("saveBasicRuleButton");
    var reset = byId("resetRuleFormButton");
    var status = byId("ruleFormStatus");
    var editing = state.editingRuleFormIndex !== null && state.editingRuleFormIndex !== undefined;
    if (button) {
      button.disabled = !editing;
      button.title = editing ? "フォームの内容でルール #" + state.editingRuleFormIndex + " を保存" : "先にフォーム編集を選択してください";
    }
    if (reset) {
      reset.disabled = !editing;
    }
    if (status) {
      status.textContent = editing ? "編集中: ルール #" + state.editingRuleFormIndex : "新規作成";
    }
  }

  function legacyRuleExtra(rule) {
    var known = {
      isDisabled: true,
      sid: true,
      types: true,
      channels: true,
      ignore_channels: true,
      category: true,
      categories: true,
      hour: true,
      duration: true,
      reserve_titles: true,
      ignore_titles: true,
      reserve_descriptions: true,
      ignore_descriptions: true,
      reserve_flags: true,
      ignore_flags: true,
      recorded_format: true
    };
    var extra = {};
    Object.keys(rule || {}).forEach(function (key) {
      if (!known[key]) {
        extra[key] = rule[key];
      }
    });
    return extra;
  }

  function ruleExtraText(extra) {
    return Object.keys(extra || {}).length ? JSON.stringify(extra, null, 2) : "";
  }

  function fillRuleFormFromRule(rule, index) {
    rule = rule || {};
    setListFormValue("ruleTitle", rule.reserve_titles);
    setListFormValue("ruleIgnoreTitle", rule.ignore_titles);
    setListFormValue("ruleDescription", rule.reserve_descriptions);
    setListFormValue("ruleIgnoreDescription", rule.ignore_descriptions);
    setFormValue("ruleType", Array.isArray(rule.types) && rule.types.length === 1 ? rule.types[0] : "");
    if (byId("ruleType") && byId("ruleType").value === "" && rule.types && rule.types.length) {
      var extra = legacyRuleExtra(rule);
      extra.types = rule.types;
      setFormValue("ruleExtraJson", ruleExtraText(extra));
    } else {
      setFormValue("ruleExtraJson", ruleExtraText(legacyRuleExtra(rule)));
    }
    setFormValue("ruleSid", rule.sid || "");
    setFormValue("ruleCategory", rule.category || "");
    setListFormValue("ruleCategories", rule.categories);
    setListFormValue("ruleChannels", rule.channels);
    setListFormValue("ruleIgnoreChannels", rule.ignore_channels);
    setListFormValue("ruleFlags", rule.reserve_flags);
    setListFormValue("ruleIgnoreFlags", rule.ignore_flags);
    setFormValue("ruleDurationMin", rule.duration && rule.duration.min !== undefined ? Math.round(Number(rule.duration.min) / 60) : "");
    setFormValue("ruleDurationMax", rule.duration && rule.duration.max !== undefined ? Math.round(Number(rule.duration.max) / 60) : "");
    setFormValue("ruleHourStart", rule.hour && rule.hour.start !== undefined ? rule.hour.start : "");
    setFormValue("ruleHourEnd", rule.hour && rule.hour.end !== undefined ? rule.hour.end : "");
    setFormValue("ruleRecordedFormat", rule.recorded_format || "");
    setFormValue("ruleDisabled", Boolean(rule.isDisabled));
    state.editingRuleFormIndex = index;
    renderRuleFormState();
    window.location.hash = "rules";
    var title = byId("ruleTitle");
    if (title) {
      title.focus();
    }
  }

  function fillRuleFormFromProgram(program) {
    if (!program) {
      return;
    }
    var end = program.end || (program.start + 30 * 60 * 1000);
    var durationMinutes = program.start !== undefined && end > program.start ? Math.round((end - program.start) / 60000) : "";
    var startDate = program.start !== undefined ? new Date(program.start) : null;
    var endDate = end ? new Date(end) : null;
    var endHour = "";
    if (startDate && endDate && !isNaN(startDate.getTime()) && !isNaN(endDate.getTime())) {
      endHour = dateKey(startDate.getTime()) !== dateKey(endDate.getTime()) ? 24 : Math.min(24, Math.max(1, Math.ceil((endDate.getHours() * 60 + endDate.getMinutes()) / 60)));
    }
    var channelID = programChannelID(program);
    var type = programChannelType(program);
    setFormValue("ruleTitle", program.title || program.fullTitle || programTitle(program));
    setFormValue("ruleIgnoreTitle", "");
    setFormValue("ruleDescription", "");
    setFormValue("ruleIgnoreDescription", "");
    setFormValue("ruleType", type);
    if (byId("ruleType") && byId("ruleType").value !== type) {
      setFormValue("ruleType", "");
    }
    setFormValue("ruleSid", channelID && /^\d+$/.test(channelID) ? channelID : "");
    setFormValue("ruleCategory", program.category || "");
    setFormValue("ruleCategories", "");
    setFormValue("ruleChannels", channelID && !/^\d+$/.test(channelID) ? channelID : "");
    setFormValue("ruleIgnoreChannels", "");
    setFormValue("ruleFlags", "");
    setFormValue("ruleIgnoreFlags", "");
    setFormValue("ruleDurationMin", durationMinutes);
    setFormValue("ruleDurationMax", durationMinutes);
    setFormValue("ruleHourStart", startDate && !isNaN(startDate.getTime()) ? startDate.getHours() : "");
    setFormValue("ruleHourEnd", endHour);
    setFormValue("ruleRecordedFormat", "");
    setFormValue("ruleDisabled", false);
    setFormValue("ruleExtraJson", "");
    state.editingRuleFormIndex = null;
    renderRuleFormState();
    closeProgramDialog();
    window.location.hash = "rules";
    setBusy("番組情報をルールフォームに反映しました");
    var title = byId("ruleTitle");
    if (title) {
      title.focus();
    }
  }

  function readRuleForm() {
    var title = byId("ruleTitle");
    var ignoreTitle = byId("ruleIgnoreTitle");
    var description = byId("ruleDescription");
    var ignoreDescription = byId("ruleIgnoreDescription");
    var type = byId("ruleType");
    var sid = byId("ruleSid");
    var category = byId("ruleCategory");
    var categories = byId("ruleCategories");
    var channels = byId("ruleChannels");
    var ignoreChannels = byId("ruleIgnoreChannels");
    var flags = byId("ruleFlags");
    var ignoreFlags = byId("ruleIgnoreFlags");
    var durationMin = byId("ruleDurationMin");
    var durationMax = byId("ruleDurationMax");
    var hourStart = byId("ruleHourStart");
    var hourEnd = byId("ruleHourEnd");
    var recordedFormat = byId("ruleRecordedFormat");
    var disabled = byId("ruleDisabled");
    var extraJSON = byId("ruleExtraJson");
    var rule = {};
    var extraRule = readExtraRuleJSON(extraJSON);
    if (extraRule === null) {
      return;
    }
    Object.keys(extraRule).forEach(function (key) {
      rule[key] = extraRule[key];
    });
    var titleValues = title ? splitList(title.value) : [];
    if (titleValues.length) {
      rule.reserve_titles = titleValues;
    }
    var ignoreTitleValues = ignoreTitle ? splitList(ignoreTitle.value) : [];
    if (ignoreTitleValues.length) {
      rule.ignore_titles = ignoreTitleValues;
    }
    var descriptionValues = description ? splitList(description.value) : [];
    if (descriptionValues.length) {
      rule.reserve_descriptions = descriptionValues;
    }
    var ignoreDescriptionValues = ignoreDescription ? splitList(ignoreDescription.value) : [];
    if (ignoreDescriptionValues.length) {
      rule.ignore_descriptions = ignoreDescriptionValues;
    }
    if (type && type.value) {
      rule.types = [type.value];
    }
    var sidText = sid ? sid.value.trim() : "";
    if (sidText) {
      var sidValue = Number(sidText);
      if (!Number.isInteger(sidValue) || sidValue <= 0) {
        showError(new Error("SIDが正しくありません"));
        return;
      }
      rule.sid = sidValue;
    }
    if (category && category.value.trim()) {
      rule.category = category.value.trim();
    }
    var categoryValues = categories ? splitList(categories.value) : [];
    if (categoryValues.length) {
      rule.categories = categoryValues;
      delete rule.category;
    }
    var channelValues = channels ? splitList(channels.value) : [];
    if (channelValues.length) {
      rule.channels = channelValues;
    }
    var ignoreChannelValues = ignoreChannels ? splitList(ignoreChannels.value) : [];
    if (ignoreChannelValues.length) {
      rule.ignore_channels = ignoreChannelValues;
    }
    var flagValues = flags ? splitList(flags.value) : [];
    if (flagValues.length) {
      rule.reserve_flags = flagValues;
    }
    var ignoreFlagValues = ignoreFlags ? splitList(ignoreFlags.value) : [];
    if (ignoreFlagValues.length) {
      rule.ignore_flags = ignoreFlagValues;
    }
    var minText = durationMin ? durationMin.value.trim() : "";
    var maxText = durationMax ? durationMax.value.trim() : "";
    if (minText || maxText) {
      if (!minText || !maxText) {
        showError(new Error("最短分と最長分を両方入力してください"));
        return;
      }
      var min = Number(minText);
      var max = Number(maxText);
      if (!isFinite(min) || !isFinite(max) || min < 0 || max < 0 || min > max) {
        showError(new Error("時間範囲が正しくありません"));
        return;
      }
      rule.duration = { min: Math.round(min * 60), max: Math.round(max * 60) };
    }
    var hourStartText = hourStart ? hourStart.value.trim() : "";
    var hourEndText = hourEnd ? hourEnd.value.trim() : "";
    if (hourStartText || hourEndText) {
      if (!hourStartText || !hourEndText) {
        showError(new Error("開始時と終了時を両方入力してください"));
        return;
      }
      var startHour = Number(hourStartText);
      var endHour = Number(hourEndText);
      if (!Number.isInteger(startHour) || !Number.isInteger(endHour) || startHour < 0 || startHour > 23 || endHour < 1 || endHour > 24) {
        showError(new Error("開始時・終了時が正しくありません"));
        return;
      }
      rule.hour = { start: startHour, end: endHour };
    }
    if (recordedFormat && recordedFormat.value.trim()) {
      rule.recorded_format = recordedFormat.value.trim();
    }
    if (disabled && disabled.checked) {
      rule.isDisabled = true;
    }
    if (!Object.keys(rule).length || (!rule.reserve_titles && !rule.ignore_titles && !rule.reserve_descriptions && !rule.ignore_descriptions && !rule.types && !rule.sid && !rule.category && !rule.categories && !rule.channels && !rule.ignore_channels && !rule.reserve_flags && !rule.ignore_flags && !rule.duration && !rule.hour)) {
      showError(new Error("ルール条件が空です"));
      return;
    }
    return rule;
  }

  function addBasicRule() {
    var rule = readRuleForm();
    if (!rule) {
      return;
    }
    confirmAction("フォームの内容でルールを追加しますか？", {
      danger: false,
      okLabel: "追加",
      title: "ルール追加の確認"
    }).then(function (confirmed) {
      if (!confirmed) {
        return;
      }
      setBusy("処理中");
      sendJSON("rules.json", "POST", rule).then(function () {
        clearRuleForm();
        state.editingRuleFormIndex = null;
        renderRuleFormState();
        refresh();
      }).catch(showError);
    });
  }

  function saveBasicRule() {
    if (state.editingRuleFormIndex === null || state.editingRuleFormIndex === undefined) {
      showError(new Error("フォーム編集するルールが選択されていません"));
      return;
    }
    var rule = readRuleForm();
    if (!rule) {
      return;
    }
    confirmAction("フォームの内容でルール #" + state.editingRuleFormIndex + " を保存しますか？", {
      danger: false,
      okLabel: "保存",
      title: "ルール保存の確認"
    }).then(function (confirmed) {
      if (!confirmed) {
        return;
      }
      setBusy("処理中");
      sendJSON("rules/" + state.editingRuleFormIndex + ".json", "PUT", rule).then(function () {
        clearRuleForm();
        state.editingRuleFormIndex = null;
        renderRuleFormState();
        refresh();
      }).catch(showError);
    });
  }

  function resetRuleForm() {
    clearRuleForm();
    state.editingRuleFormIndex = null;
    renderRuleFormState();
    setBusy("ルールフォームを新規作成に戻しました");
  }

  function tailText(value, maxLines) {
    var lines = (value || "").split(/\r?\n/);
    if (lines.length > maxLines) {
      lines = lines.slice(lines.length - maxLines);
    }
    return lines.join("\n").trim() || "ログはありません";
  }

  function setLog(id, value) {
    text(byId(id), tailText(value, 80));
  }

  function render() {
    state.programStateIndex = {
      reserves: programByID(state.reserves),
      recording: programByID(state.recording)
    };
    text(byId("reserveCount"), String(state.reserves.length));
    text(byId("recordingCount"), String(state.recording.length));
    text(byId("recordedCount"), String(state.recorded.length));
    text(byId("channelCount"), String(state.schedule.length));
    text(byId("ruleCount"), String(state.rules.length));

    var badge = byId("statusBadge");
    if (badge) {
      var operator = state.status && state.status.operator;
      var alive = operator && (operator.alive || operator.isRunning);
      badge.textContent = alive ? "オペレータ稼働中" : "オペレータ待機中";
      badge.className = alive ? "status-badge ok" : "status-badge";
    }

    var filteredReserves = filteredPrograms(state.reserves, "reserves");
    var recordedNewestFirst = state.recorded.slice().reverse();
    var filteredRecorded = filteredPrograms(recordedNewestFirst, "recorded");

    updateListCategoryOptions("reserveListCategory", state.reserves, "reserves");
    updateListCategoryOptions("recordedListCategory", state.recorded, "recorded");
    updateListFilterSummary("reserveListFilterSummary", filteredReserves.length, state.reserves.length);
    updateListFilterSummary("recordedListFilterSummary", filteredRecorded.length, state.recorded.length);

    renderList("recordingList", state.recording, "録画中の番組はありません", 8, ["watch-recording-mp4", "playlist-recording", "preview-recording", "stop"]);
    renderList("reserveList", state.reserves, "予約はありません", 8, ["skip", "unskip", "unreserve"]);
    renderList("reserveListPage", filteredReserves, "条件に一致する予約はありません", 100, ["skip", "unskip", "unreserve"]);
    renderList("recordedList", recordedNewestFirst, "録画済み番組はありません", 8, ["watch-m2ts", "watch-mp4", "watch-mp4-720p", "watch-mp4-low", "watch-mp4-custom", "watch-m2ts-offset", "playlist", "download", "preview-recorded", "delete-recorded"]);
    renderList("recordedListPage", filteredRecorded, "条件に一致する録画済み番組はありません", 100, ["watch-m2ts", "watch-mp4", "watch-mp4-720p", "watch-mp4-low", "watch-mp4-custom", "watch-m2ts-offset", "playlist", "download", "preview-recorded", "delete-recorded"]);
    renderOnAirList();
    renderSchedule();
    renderChannelPrograms();
    renderRules();
    renderSettings();
    renderRuleEditorState();
    renderRuleFormState();
  }

  function setBusy(message) {
    var badge = byId("statusBadge");
    if (badge) {
      badge.textContent = message;
      badge.className = "status-badge";
    }
  }

  function showError(error) {
    var badge = byId("statusBadge");
    if (badge) {
      badge.textContent = error.message;
      badge.className = "status-badge error";
    }
  }

  function refresh() {
    setBusy("Loading");
    Promise.all([
      api("status.json"),
      api("reserves.json"),
      api("recording.json"),
      api("recorded.json"),
      api("schedule.json"),
      api("rules.json"),
      api("config.json"),
      api("storage.json").catch(function () {
        return null;
      })
    ]).then(function (result) {
      state.status = result[0] || {};
      state.reserves = result[1] || [];
      state.recording = result[2] || [];
      state.recorded = result[3] || [];
      state.schedule = result[4] || [];
      state.rules = result[5] || [];
      state.config = result[6] || {};
      state.storage = result[7] || null;
      render();
    }).catch(showError);
  }

  function refreshLogs() {
    setBusy("ログ読み込み中");
    Promise.all([
      apiText("log/scheduler.txt"),
      apiText("log/operator.txt"),
      apiText("log/wui.txt")
    ]).then(function (result) {
      setLog("schedulerLog", result[0]);
      setLog("operatorLog", result[1]);
      setLog("wuiLog", result[2]);
    }).catch(showError);
  }

  function bindListFilter(filterName, queryID, categoryID) {
    var query = byId(queryID);
    var category = byId(categoryID);
    if (query) {
      query.addEventListener("input", function () {
        state.listFilters[filterName].query = query.value;
        render();
      });
    }
    if (category) {
      category.addEventListener("change", function () {
        state.listFilters[filterName].category = category.value;
        render();
      });
    }
  }

  document.addEventListener("DOMContentLoaded", function () {
    initNavigation();
    initKeyboardShortcuts();
    var refreshButton = byId("refreshButton");
    if (refreshButton) {
      refreshButton.addEventListener("click", refresh);
    }
    bindListFilter("reserves", "reserveListQuery", "reserveListCategory");
    bindListFilter("recorded", "recordedListQuery", "recordedListCategory");
    var channelProgramsQuery = byId("channelProgramsQuery");
    if (channelProgramsQuery) {
      channelProgramsQuery.addEventListener("input", function () {
        state.listFilters.channelPrograms.query = channelProgramsQuery.value;
        renderChannelPrograms();
      });
    }
    var ruleListQuery = byId("ruleListQuery");
    if (ruleListQuery) {
      ruleListQuery.addEventListener("input", function () {
        state.listFilters.rules.query = ruleListQuery.value;
        renderRules();
      });
    }
    var ruleListState = byId("ruleListState");
    if (ruleListState) {
      ruleListState.addEventListener("change", function () {
        state.listFilters.rules.state = ruleListState.value;
        renderRules();
      });
    }
    var mp4Preset = byId("mp4Preset");
    if (mp4Preset) {
      mp4Preset.addEventListener("change", function () {
        applyMP4Preset(mp4Preset.value);
      });
    }
    var mp4OpenButton = byId("mp4OpenButton");
    if (mp4OpenButton) {
      mp4OpenButton.addEventListener("click", submitMP4Dialog);
    }
    var mp4DialogClose = byId("mp4DialogClose");
    if (mp4DialogClose) {
      mp4DialogClose.addEventListener("click", function () {
        var dialog = byId("mp4Dialog");
        if (dialog) {
          dialog.close();
        }
      });
    }
    var mp4Dialog = byId("mp4Dialog");
    if (mp4Dialog) {
      mp4Dialog.addEventListener("click", function (event) {
        if (event.target === mp4Dialog) {
          mp4Dialog.close();
        }
      });
      mp4Dialog.addEventListener("close", function () {
        pendingMP4Open = null;
        restoreFocus(mp4DialogReturnFocus);
        mp4DialogReturnFocus = null;
      });
    }
    var forceSchedulerButton = byId("forceSchedulerButton");
    if (forceSchedulerButton) {
      forceSchedulerButton.addEventListener("click", forceScheduler);
    }
    var configEditor = byId("configEditor");
    if (configEditor) {
      configEditor.addEventListener("input", function () {
        state.configEditorDirty = true;
      });
    }
    var saveConfigButton = byId("saveConfigButton");
    if (saveConfigButton) {
      saveConfigButton.addEventListener("click", saveConfigFromEditor);
    }
    var resetConfigButton = byId("resetConfigButton");
    if (resetConfigButton) {
      resetConfigButton.addEventListener("click", resetConfigEditor);
    }
    var applyConfigFormButton = byId("applyConfigFormButton");
    if (applyConfigFormButton) {
      applyConfigFormButton.addEventListener("click", function () {
        applyConfigFormToEditor(false);
      });
    }
    var saveConfigFormButton = byId("saveConfigFormButton");
    if (saveConfigFormButton) {
      saveConfigFormButton.addEventListener("click", saveConfigFromForm);
    }
    Array.prototype.forEach.call(document.querySelectorAll(".config-form-control"), function (control) {
      control.addEventListener("change", function () {
        applyConfigFormToEditor(true);
      });
    });
    var addRuleButton = byId("addRuleButton");
    if (addRuleButton) {
      addRuleButton.addEventListener("click", addRuleFromEditor);
    }
    var saveRuleButton = byId("saveRuleButton");
    if (saveRuleButton) {
      saveRuleButton.addEventListener("click", saveRuleFromEditor);
    }
    var addBasicRuleButton = byId("addBasicRuleButton");
    if (addBasicRuleButton) {
      addBasicRuleButton.addEventListener("click", addBasicRule);
    }
    var saveBasicRuleButton = byId("saveBasicRuleButton");
    if (saveBasicRuleButton) {
      saveBasicRuleButton.addEventListener("click", saveBasicRule);
    }
    var resetRuleFormButton = byId("resetRuleFormButton");
    if (resetRuleFormButton) {
      resetRuleFormButton.addEventListener("click", resetRuleForm);
    }
    var refreshLogsButton = byId("refreshLogsButton");
    if (refreshLogsButton) {
      refreshLogsButton.addEventListener("click", refreshLogs);
    }
    var programDialogClose = byId("programDialogClose");
    if (programDialogClose) {
      programDialogClose.addEventListener("click", closeProgramDialog);
    }
    var programDialog = byId("programDialog");
    if (programDialog) {
      programDialog.addEventListener("click", function (event) {
        if (event.target === programDialog) {
          closeProgramDialog();
        }
      });
      programDialog.addEventListener("close", function () {
        state.selectedProgram = null;
        restoreFocus(programDialogReturnFocus);
        programDialogReturnFocus = null;
      });
    }
    var confirmDialog = byId("confirmDialog");
    if (confirmDialog) {
      confirmDialog.addEventListener("cancel", function () {
        closeConfirmDialog(false);
      });
      confirmDialog.addEventListener("close", function () {
        if (pendingConfirmResolve) {
          pendingConfirmResolve(false);
          pendingConfirmResolve = null;
        }
        restoreFocus(confirmDialogReturnFocus);
        confirmDialogReturnFocus = null;
      });
      confirmDialog.addEventListener("click", function (event) {
        if (event.target === confirmDialog) {
          closeConfirmDialog(false);
        }
      });
    }
    var confirmDialogCancel = byId("confirmDialogCancel");
    if (confirmDialogCancel) {
      confirmDialogCancel.addEventListener("click", function () {
        closeConfirmDialog(false);
      });
    }
    var confirmDialogOK = byId("confirmDialogOK");
    if (confirmDialogOK) {
      confirmDialogOK.addEventListener("click", function () {
        closeConfirmDialog(true);
      });
    }
    var scheduleChannel = byId("scheduleChannel");
    if (scheduleChannel) {
      scheduleChannel.addEventListener("change", function () {
        state.scheduleChannel = scheduleChannel.value;
        renderSchedule();
      });
    }
    var scheduleStreamChannel = byId("scheduleStreamChannel");
    if (scheduleStreamChannel) {
      scheduleStreamChannel.addEventListener("change", function () {
        state.scheduleStreamChannel = scheduleStreamChannel.value;
        renderScheduleChannelTools(state.schedule || []);
      });
    }
    var scheduleType = byId("scheduleType");
    if (scheduleType) {
      scheduleType.addEventListener("change", function () {
        state.scheduleType = scheduleType.value;
        renderSchedule();
      });
    }
    var scheduleDay = byId("scheduleDay");
    if (scheduleDay) {
      scheduleDay.addEventListener("change", function () {
        state.scheduleDay = scheduleDay.value;
        renderSchedule();
      });
    }
    var scheduleGenre = byId("scheduleGenre");
    if (scheduleGenre) {
      scheduleGenre.addEventListener("change", function () {
        state.scheduleGenre = scheduleGenre.value;
        renderSchedule();
      });
    }
    var scheduleHideChannelButton = byId("scheduleHideChannelButton");
    if (scheduleHideChannelButton) {
      scheduleHideChannelButton.addEventListener("click", function () {
        var select = byId("scheduleHiddenChannel");
        if (select && select.value && state.scheduleHiddenChannels.indexOf(select.value) < 0) {
          state.scheduleHiddenChannels.push(select.value);
          saveHiddenChannels();
          renderSchedule();
        }
      });
    }
    var scheduleShowChannelButton = byId("scheduleShowChannelButton");
    if (scheduleShowChannelButton) {
      scheduleShowChannelButton.addEventListener("click", function () {
        var select = byId("scheduleHiddenChannel");
        if (!select || !select.value) {
          return;
        }
        state.scheduleHiddenChannels = state.scheduleHiddenChannels.filter(function (id) {
          return id !== select.value;
        });
        saveHiddenChannels();
        renderSchedule();
      });
    }
    var scheduleClearHiddenButton = byId("scheduleClearHiddenButton");
    if (scheduleClearHiddenButton) {
      scheduleClearHiddenButton.addEventListener("click", function () {
        state.scheduleHiddenChannels = [];
        saveHiddenChannels();
        renderSchedule();
      });
    }
    var scheduleWindow = byId("scheduleWindow");
    if (scheduleWindow) {
      scheduleWindow.value = state.scheduleWindowMode;
      scheduleWindow.addEventListener("change", function () {
        state.scheduleWindowMode = scheduleWindow.value || "day";
        renderSchedule();
      });
    }
    var channelProgramsGenre = byId("channelProgramsGenre");
    if (channelProgramsGenre) {
      channelProgramsGenre.addEventListener("change", function () {
        state.channelProgramsGenre = channelProgramsGenre.value;
        renderChannelPrograms();
      });
    }
    var channelProgramsSort = byId("channelProgramsSort");
    if (channelProgramsSort) {
      channelProgramsSort.value = state.channelProgramsSort;
      channelProgramsSort.addEventListener("change", function () {
        state.channelProgramsSort = channelProgramsSort.value || "start";
        renderChannelPrograms();
      });
    }
    subscribeRealtimeRefresh();
    refresh();
    setInterval(refresh, 30000);
    setInterval(refreshLogs, 60000);
    refreshLogs();
  });
}());
