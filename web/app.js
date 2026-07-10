(function () {
  var hiddenChannelsStorageKey = "strata-pvr.scheduleHiddenChannels";
  var listFiltersStorageKey = "strata-pvr.listFilters";
  var recordedPageSizeStorageKey = "strata-pvr.recordedPageSize";
  var scheduleZoomStorageKey = "strata-pvr.scheduleZoomLevel";
  var scheduleWindowHoursByMode = {
    "day": 24,
    "three-days": 72,
    "all": 0
  };
  var scheduleZoomLevels = [
    { id: "wide", label: "広域", minutePixels: 0.75 },
    { id: "standard", label: "標準", minutePixels: 1.15 },
    { id: "detail", label: "詳細", minutePixels: 2.4 },
    { id: "precise", label: "精密", minutePixels: 5 }
  ];
  var scheduleDefaultZoomLevel = "standard";
  var scheduleMinimumProgramMinutes = 30;
  var recordedPageSizeOptions = [50, 100, 200];
  var recordedDefaultPageSize = 50;
  var programDialogReturnFocus = null;
  var playerDialogReturnFocus = null;
  var confirmDialogReturnFocus = null;
  var playerSourceBuilder = null;
  var playerBaseQuery = null;
  var playerSeekable = false;
  var playerSeeking = false;
  var playerTimelineStart = 0;
  var playerTimelineDuration = 0;
  var playerFallbackDuration = 0;
  var playerKnownDuration = 0;
  var pendingConfirmResolve = null;
  var scheduleMenuTouchStart = null;
  var metricsRefreshTimer = null;
  var focusableControlSelector = "button, [href], input, select, textarea, [tabindex]:not([tabindex='-1'])";
  var state = {
    status: null,
    reserves: [],
    recording: [],
    recorded: [],
    schedule: [],
    rules: [],
    config: {},
    storage: null,
    metrics: null,
    scheduleChannel: "",
    scheduleType: "",
    scheduleDay: "",
    scheduleGenre: "",
    scheduleHiddenChannels: loadHiddenChannels(),
    scheduleWindowMode: "day",
    scheduleZoomLevel: loadScheduleZoomLevel(),
    scheduleChannelTouched: false,
    editingRuleFormIndex: null,
    selectedProgram: null,
    activeProgramID: "",
    currentView: "",
    viewScrollPositions: {},
    scheduleGuideScroll: { left: 0, top: 0 },
    listFilters: loadListFilters(),
    recordedPage: 1,
    recordedPageSize: loadRecordedPageSize(),
    programStateIndex: { reserves: {}, recording: {} },
    realtimeChannel: null,
    hasLoaded: false,
    isLoading: false,
    lastError: null
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

  function scheduleZoomIndex(level) {
    for (var i = 0; i < scheduleZoomLevels.length; i += 1) {
      if (scheduleZoomLevels[i].id === level) {
        return i;
      }
    }
    return scheduleZoomIndex(scheduleDefaultZoomLevel);
  }

  function scheduleZoomConfig() {
    return scheduleZoomLevels[scheduleZoomIndex(state.scheduleZoomLevel)] || scheduleZoomLevels[scheduleZoomIndex(scheduleDefaultZoomLevel)];
  }

  function scheduleMinutePixels() {
    return scheduleZoomConfig().minutePixels;
  }

  function loadScheduleZoomLevel() {
    try {
      var raw = window.localStorage ? window.localStorage.getItem(scheduleZoomStorageKey) : "";
      return scheduleZoomLevels.some(function (level) { return level.id === raw; }) ? raw : scheduleDefaultZoomLevel;
    } catch (error) {
      return scheduleDefaultZoomLevel;
    }
  }

  function saveScheduleZoomLevel() {
    try {
      if (window.localStorage) {
        window.localStorage.setItem(scheduleZoomStorageKey, state.scheduleZoomLevel);
      }
    } catch (error) {
      // localStorage can be unavailable in private or embedded contexts.
    }
  }

  function normalizeRecordedPageSize(value) {
    var size = Number(value);
    return recordedPageSizeOptions.indexOf(size) >= 0 ? size : recordedDefaultPageSize;
  }

  function loadRecordedPageSize() {
    try {
      var raw = window.localStorage ? window.localStorage.getItem(recordedPageSizeStorageKey) : "";
      return normalizeRecordedPageSize(raw);
    } catch (error) {
      return recordedDefaultPageSize;
    }
  }

  function saveRecordedPageSize() {
    state.recordedPageSize = normalizeRecordedPageSize(state.recordedPageSize);
    try {
      if (window.localStorage) {
        window.localStorage.setItem(recordedPageSizeStorageKey, String(state.recordedPageSize));
      }
    } catch (error) {
      // localStorage can be unavailable in private or embedded contexts.
    }
  }

  function defaultListFilters() {
    return {
      rules: { query: "", state: "", sort: "indexAsc" },
      recorded: { query: "", category: "", sort: "startDesc" },
      reserves: { query: "", category: "", sort: "startAsc" }
    };
  }

  function defaultListFilter(name) {
    var defaults = defaultListFilters();
    var source = defaults[name] || {};
    var result = {};
    Object.keys(source).forEach(function (key) {
      result[key] = source[key];
    });
    return result;
  }

  function normalizeListFilters(value) {
    var defaults = defaultListFilters();
    Object.keys(defaults).forEach(function (name) {
      var source = value && typeof value[name] === "object" ? value[name] : {};
      Object.keys(defaults[name]).forEach(function (key) {
        defaults[name][key] = typeof source[key] === "string" ? source[key] : "";
      });
    });
    return defaults;
  }

  function loadListFilters() {
    try {
      var raw = window.localStorage ? window.localStorage.getItem(listFiltersStorageKey) : "";
      return normalizeListFilters(raw ? JSON.parse(raw) : {});
    } catch (error) {
      return defaultListFilters();
    }
  }

  function saveListFilters() {
    try {
      if (window.localStorage) {
        state.listFilters = normalizeListFilters(state.listFilters);
        window.localStorage.setItem(listFiltersStorageKey, JSON.stringify(state.listFilters));
      }
    } catch (error) {
      // localStorage can be unavailable in private or embedded contexts.
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

  function toggleClass(node, className, enabled) {
    if (node) {
      node.classList.toggle(className, enabled);
    }
  }

  function iconUse(iconID) {
    var svg = document.createElementNS("http://www.w3.org/2000/svg", "svg");
    var use = document.createElementNS("http://www.w3.org/2000/svg", "use");
    svg.setAttribute("class", "icon");
    svg.setAttribute("aria-hidden", "true");
    use.setAttribute("href", "#icon-" + iconID);
    svg.appendChild(use);
    return svg;
  }

  function setIconOnlyControl(control, iconID, label) {
    if (!control) {
      return;
    }
    control.innerHTML = "";
    control.appendChild(iconUse(iconID));
    var hidden = document.createElement("span");
    hidden.className = "screen-reader-only";
    hidden.textContent = label;
    control.appendChild(hidden);
    control.setAttribute("aria-label", label);
    control.title = label;
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
      var control = dialog.querySelector(focusableControlSelector);
      if (control && typeof control.focus === "function") {
        control.focus();
      }
    }, 0);
  }

  function focusDialogControl(dialog, selector) {
    if (!dialog) {
      return;
    }
    window.setTimeout(function () {
      var control = dialog.querySelector(selector);
      if (control && typeof control.focus === "function") {
        control.focus();
      } else {
        focusFirstDialogControl(dialog);
      }
    }, 0);
  }

  function dialogFocusableControls(dialog) {
    return Array.prototype.slice.call(dialog.querySelectorAll(focusableControlSelector)).filter(function (control) {
      return !control.disabled && control.getAttribute("aria-hidden") !== "true" && typeof control.focus === "function";
    });
  }

  function trapDialogFocus(dialog, event) {
    if (event.key !== "Tab" || !dialog.open) {
      return;
    }
    var controls = dialogFocusableControls(dialog);
    if (!controls.length) {
      return;
    }
    var first = controls[0];
    var last = controls[controls.length - 1];
    if (event.shiftKey && document.activeElement === first) {
      event.preventDefault();
      last.focus();
    } else if (!event.shiftKey && document.activeElement === last) {
      event.preventDefault();
      first.focus();
    }
  }

  function bindDialogFocusTrap(dialog) {
    if (dialog) {
      dialog.addEventListener("keydown", function (event) {
        trapDialogFocus(dialog, event);
      });
    }
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
		return fetch("/api/config", {
			body: raw,
			credentials: "same-origin",
			headers: { "Content-Type": "application/json" },
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

  function renderProgramCategoryChip(program) {
    var category = programCategory(program);
    if (!category) {
      return null;
    }
    var chip = document.createElement("span");
    chip.className = "program-category-chip" + categoryClass(category);
    chip.textContent = category;
    chip.title = "ジャンル: " + category;
    return chip;
  }

  function renderProgramMeta(parts, program, className) {
    var meta = document.createElement("div");
    meta.className = className || "program-row-meta";
    var chip = renderProgramCategoryChip(program);
    if (chip) {
      meta.appendChild(chip);
    }
    var body = document.createElement("span");
    body.textContent = (parts || []).filter(Boolean).join(" / ");
    meta.appendChild(body);
    return meta;
  }

  function setProgramMeta(root, parts, program) {
    if (!root) {
      return;
    }
    root.innerHTML = "";
    var chip = renderProgramCategoryChip(program);
    if (chip) {
      root.appendChild(chip);
    }
    var body = document.createElement("span");
    body.className = "program-dialog-meta-text";
    body.textContent = (parts || []).filter(Boolean).join(" / ");
    root.appendChild(body);
    renderProgramStateBadges(root, program);
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

  function operatorAlive() {
    var operator = state.status && state.status.operator;
    return Boolean(operator && (operator.alive || operator.isRunning));
  }

  function programOnAir(program) {
    if (!program || !program.start) {
      return false;
    }
    var now = Date.now();
    return now >= program.start && now <= programEnd(program);
  }

  function reserveConflictCount() {
    return (state.reserves || []).filter(function (program) {
      return program && program.isConflict;
    }).length;
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
      decorated.isConflict = Boolean(reserve.isConflict);
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

  function isRecordedProgram(program) {
    if (!program || !program.id) {
      return false;
    }
    return (state.recorded || []).some(function (recorded) {
      return recorded && recorded.id === program.id;
    });
  }

  function recordedProgramFor(program) {
    if (!program || !program.id) {
      return null;
    }
    for (var i = 0; i < state.recorded.length; i += 1) {
      if (state.recorded[i] && state.recorded[i].id === program.id) {
        return state.recorded[i];
      }
    }
    return null;
  }

  function programDialogActions(program) {
    if (isRecordedProgram(program)) {
      return ["watch-mp4", "download", "xspf", "delete-recorded", "create-rule-from-program"];
    }
    return ["reserve", "unreserve", "skip", "unskip", "watch-recording-mp4", "preview-recording", "stop", "watch-channel-mp4", "create-rule-from-program"];
  }

  function programStateLabels(program, options) {
    options = options || {};
    var labels = [];
    if (program && program.isRecording) {
      labels.push({ text: "録画中", type: "recording" });
    } else if (program && program.isReserved && !options.hideReservedBadge) {
      labels.push({ text: program.isManualReserved ? "手動予約" : "予約済み", type: "reserved" });
    }
    if (program && program.isConflict) {
      labels.push({ text: "競合", type: "conflict" });
    }
    if (program && program.isSkip) {
      labels.push({ text: "スキップ", type: "skip" });
    }
    return labels;
  }

  function programStateLabelText(program) {
    return programStateLabels(program).map(function (label) {
      return label.text;
    }).join(" / ");
  }

  function renderProgramStateBadges(item, program, options) {
    var labels = programStateLabels(program, options);
    if (!labels.length) {
      return;
    }
    var badges = document.createElement("div");
    badges.className = "program-state-badges";
    labels.forEach(function (label) {
      var badge = document.createElement("span");
      badge.className = "program-state-badge " + label.type;
      badge.textContent = label.text;
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
      return !query || normalizeSearchText(programSearchText(program)).indexOf(query) >= 0;
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

  function sortedPrograms(items, filterName) {
    var filter = state.listFilters[filterName] || {};
    var sort = filter.sort || (filterName === "recorded" ? "startDesc" : "startAsc");
    return (items || []).slice().sort(function (a, b) {
      var startOrder = (a.start || 0) - (b.start || 0);
      if (sort === "startDesc") {
        return -startOrder;
      }
      if (sort === "category") {
        return programCategory(a).localeCompare(programCategory(b), "ja") || startOrder;
      }
      if (sort === "title") {
        return programTitle(a).localeCompare(programTitle(b), "ja") || startOrder;
      }
      if (sort === "duration") {
        return (programEnd(a) - (a.start || 0)) - (programEnd(b) - (b.start || 0)) || startOrder;
      }
      return startOrder;
    });
  }

  function sortedRuleEntries(items) {
    var sort = (state.listFilters.rules && state.listFilters.rules.sort) || "indexAsc";
    return (items || []).slice().sort(function (a, b) {
      if (sort === "indexDesc") {
        return b.index - a.index;
      }
      if (sort === "state") {
        return Number(Boolean(a.rule && a.rule.isDisabled)) - Number(Boolean(b.rule && b.rule.isDisabled)) || a.index - b.index;
      }
      if (sort === "title") {
        return ruleSummary(a.rule).localeCompare(ruleSummary(b.rule), "ja") || a.index - b.index;
      }
      return a.index - b.index;
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

  function scheduleChannelTypeRank(type) {
    var normalized = String(type || "").toUpperCase();
    if (normalized === "GR") {
      return 0;
    }
    if (normalized === "BS") {
      return 1;
    }
    if (normalized === "CS") {
      return 2;
    }
    return 3;
  }

  function compareScheduleChannelGroups(a, b) {
    return scheduleChannelTypeRank(a && a.type) - scheduleChannelTypeRank(b && b.type) || (a && a.order || 0) - (b && b.order || 0);
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

  function dateKeyStart(value) {
    var match = String(value || "").match(/^(\d{4})-(\d{2})-(\d{2})$/);
    if (!match) {
      return 0;
    }
    return new Date(Number(match[1]), Number(match[2]) - 1, Number(match[3]), 0, 0, 0, 0).getTime();
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

  function actionButton(label, title, fn, className) {
    var button = document.createElement("button");
    button.type = "button";
    button.className = className || "small-button";
    button.title = title || label;
    button.textContent = label;
    button.addEventListener("click", fn);
    return button;
  }

  function currentView() {
    var hash = (window.location.hash || "#dashboard").replace("#", "");
    return hash || "dashboard";
  }

  function syncStickyOffsets() {
    var topbar = document.querySelector(".topbar");
    if (!topbar) {
      return;
    }
    var height = Math.ceil(topbar.getBoundingClientRect().height);
    document.documentElement.style.setProperty("--topbar-height", height + "px");
  }

  function isMobileScheduleLayout() {
    return window.matchMedia && window.matchMedia("(max-width: 520px)").matches;
  }

  function setScheduleMenuOpen(open) {
    var expanded = Boolean(open);
    document.body.classList.toggle("schedule-menu-open", expanded);
    var button = byId("scheduleMenuButton");
    var backdrop = byId("scheduleMenuBackdrop");
    if (button) {
      button.setAttribute("aria-expanded", expanded ? "true" : "false");
    }
    if (backdrop) {
      backdrop.hidden = !expanded;
    }
  }

  function closeScheduleMenu() {
    setScheduleMenuOpen(false);
  }

  function setScheduleFilterOpen(open) {
    var expanded = Boolean(open);
    document.body.classList.toggle("schedule-filter-open", expanded);
    var button = byId("scheduleFilterButton");
    if (button) {
      button.setAttribute("aria-expanded", expanded ? "true" : "false");
    }
  }

  function closeScheduleFilter() {
    setScheduleFilterOpen(false);
  }

  function firstScheduleChannelID(channels) {
    for (var i = 0; i < channels.length; i += 1) {
      var id = scheduleChannelID(channels[i]);
      var groupID = id || scheduleChannelName(channels[i]) || "unknown";
      if (id && state.scheduleHiddenChannels.indexOf(groupID) < 0 && state.scheduleHiddenChannels.indexOf(id) < 0) {
        return id;
      }
    }
    return "";
  }

  function ensureMobileScheduleChannelDefault(channels) {
    if (!isMobileScheduleLayout() || state.scheduleChannel || state.scheduleChannelTouched) {
      return;
    }
    var id = firstScheduleChannelID(channels || []);
    if (id) {
      state.scheduleChannel = id;
    }
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
    if (state.currentView !== "schedule") {
      closeScheduleMenu();
      closeScheduleFilter();
    }
    document.querySelectorAll(".management-menu").forEach(function (menu) {
      menu.open = name === "status" || name === "settings" || name === "logs";
    });
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

  function updateOperationalStatus() {
    var alive = operatorAlive();
    var statusText = state.lastError ? state.lastError.message : (alive ? "オペレータ稼働中" : "オペレータ停止中");
    var conflicts = reserveConflictCount();
    var badge = byId("statusBadge");
    if (badge) {
      badge.textContent = statusText;
      badge.className = state.lastError ? "status-badge error" : (alive ? "status-badge ok" : "status-badge");
    }
    var scheduleOperator = byId("scheduleOperatorStatus");
    if (scheduleOperator) {
      scheduleOperator.textContent = statusText;
      scheduleOperator.className = "schedule-status-pill" + (state.lastError ? " error" : (alive ? " ok" : ""));
    }
    text(byId("scheduleRecordingSummary"), "録画中 " + state.recording.length);
    text(byId("scheduleReserveSummary"), "予約 " + state.reserves.length);
    text(byId("scheduleConflictSummary"), "競合 " + conflicts);
    toggleClass(byId("scheduleConflictSummary"), "has-conflicts", conflicts > 0);
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
      "reserves": "reserveListQuery",
      "recorded": "recordedListQuery",
      "rules": "ruleListQuery",
		"settings": "strataMirakurunURL"
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

  function focusRowAt(index) {
    var rows = currentFocusableRows();
    if (!rows.length) {
      return false;
    }
    index = Math.max(0, Math.min(rows.length - 1, index));
    rows[index].focus();
    return true;
  }

  function focusPagedRow(delta) {
    var rows = currentFocusableRows();
    if (!rows.length) {
      return false;
    }
    var index = rows.indexOf(document.activeElement);
    if (index < 0) {
      index = delta > 0 ? -1 : rows.length;
    }
    index += delta * 10;
    index = Math.max(0, Math.min(rows.length - 1, index));
    rows[index].focus();
    return true;
  }

  function closeTopDialog() {
    var dialogs = ["playerDialog", "programDialog", "confirmDialog"];
    for (var i = 0; i < dialogs.length; i++) {
      var dialog = byId(dialogs[i]);
      if (dialog && dialog.open) {
        if (dialogs[i] === "playerDialog") {
          closePlayerDialog();
        } else if (dialogs[i] === "programDialog") {
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
      } else if (event.key === "Home") {
        if (focusRowAt(0)) {
          event.preventDefault();
        }
      } else if (event.key === "End") {
        if (focusRowAt(currentFocusableRows().length - 1)) {
          event.preventDefault();
        }
      } else if (event.key === "PageDown") {
        if (focusPagedRow(1)) {
          event.preventDefault();
        }
      } else if (event.key === "PageUp") {
        if (focusPagedRow(-1)) {
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
      focusDialogControl(dialog, "#confirmDialogOK");
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

  function setRecordedCleanupStatus(message, kind) {
    var status = byId("recordedCleanupStatus");
    if (!status) {
      return;
    }
    status.hidden = !message;
    status.className = "cleanup-status" + (kind ? " " + kind : "");
    status.textContent = message || "";
  }

  function summarizeRecordedCleanup(result, applied) {
    var removed = result && Number(result.removed || 0);
    var kept = result && Number(result.kept || 0);
    var total = result && Number(result.total || 0);
    if (!removed) {
      return "削除対象はありません。録画済み " + total + "件を確認しました。";
    }
    var removedItems = ((result && result.items) || []).filter(function (item) {
      return item && item.action === "remove";
    }).slice(0, 3).map(function (item) {
      return item.id || item.recorded || "IDなし";
    });
    var suffix = removedItems.length ? " 対象: " + removedItems.join(", ") + (removed > removedItems.length ? " ほか" : "") : "";
    return (applied ? "クリーンアップしました。" : "クリーンアップ対象があります。") +
      " 削除対象 " + removed + "件 / 残す項目 " + kept + "件。" + suffix;
  }

  function cleanupRecorded() {
    var button = byId("recordedCleanupButton");
    if (button) {
      button.disabled = true;
      button.setAttribute("aria-busy", "true");
    }
    setBusy("録画済みクリーンアップ確認中");
    setRecordedCleanupStatus("録画ファイルの存在を確認しています。", "");
    return request("recorded/cleanup", "GET").then(function (result) {
      if (!result.removed) {
        setBusy("録画済みクリーンアップ対象なし");
        setRecordedCleanupStatus(summarizeRecordedCleanup(result, false), "success");
        return;
      }
      setRecordedCleanupStatus(summarizeRecordedCleanup(result, false), "warning");
      return confirmAction(
        result.removed + "件の録画済み項目を削除しますか？",
        {
          danger: true,
          meta: "実ファイルが見つからない録画済み項目だけを削除します。録画ファイル自体は削除しません。",
          okLabel: "クリーンアップ",
          title: "録画済みクリーンアップの確認"
        }
      ).then(function (confirmed) {
        if (!confirmed) {
          setBusy("録画済みクリーンアップをキャンセル");
          return;
        }
        setBusy("録画済みクリーンアップ中");
        return request("recorded/cleanup", "PUT").then(function (applied) {
          setRecordedCleanupStatus(summarizeRecordedCleanup(applied, true), "success");
          return refresh();
        });
      });
    }).catch(function (error) {
      setRecordedCleanupStatus(error.message, "error");
      showError(error);
    }).finally(function () {
      if (button) {
        button.disabled = false;
        button.setAttribute("aria-busy", "false");
      }
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

  function recordedHLSURL(program, query) {
    query = cloneQuery(query || {});
    var quality = qualityFromQuery(query) || "540p";
    if (quality === "custom") {
      quality = "540p";
    }
    var params = new URLSearchParams();
    params.set("quality", quality);
    ["ss", "t", "audio"].forEach(function (key) {
      if (query[key]) {
        params.set(key, query[key]);
      }
    });
    return "/api/recorded/" + encodeURIComponent(program.id) + "/hls/index.m3u8?" + params.toString();
  }

  function recordedPlaybackURL(program, query) {
    var video = document.createElement("video");
    if (video.canPlayType("application/vnd.apple.mpegurl")) {
      return recordedHLSURL(program, query);
    }
    return recordedWatchURL(program, "mp4", query);
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

  function playerWindowURL(url, meta) {
    var params = new URLSearchParams();
    params.set("src", url);
    if (meta) {
      params.set("title", meta);
    }
    return "/player.html?" + params.toString();
  }

  function cloneQuery(query) {
    var clone = {};
    Object.keys(query || {}).forEach(function (key) {
      if (query[key] !== undefined && query[key] !== null && query[key] !== "") {
        clone[key] = String(query[key]);
      }
    });
    return clone;
  }

  function presetQuery(name) {
    var preset = mp4Presets[name];
    if (!preset) {
      return {};
    }
    return {
      "s": preset.s,
      "b:v": preset.video,
      "b:a": preset.audio
    };
  }

  function applyQualityToQuery(query, quality) {
    var next = cloneQuery(query);
    delete next.s;
    delete next["b:v"];
    delete next["b:a"];
    var preset = presetQuery(quality);
    Object.keys(preset).forEach(function (key) {
      next[key] = preset[key];
    });
    return Object.keys(next).length ? next : null;
  }

  function applyAudioToQuery(query, audio) {
    var next = cloneQuery(query);
    delete next.audio;
    if (audio === "secondary") {
      next.audio = "secondary";
    }
    return Object.keys(next).length ? next : null;
  }

  function audioFromQuery(query) {
    return query && query.audio === "secondary" ? "secondary" : "";
  }

  function qualityFromQuery(query) {
    query = query || {};
    var matched = "";
    Object.keys(mp4Presets).some(function (name) {
      var preset = mp4Presets[name];
      if (query.s === preset.s && query["b:v"] === preset.video && query["b:a"] === preset.audio) {
        matched = name;
        return true;
      }
      return false;
    });
    if (matched) {
      return matched;
    }
    return query.s || query["b:v"] || query["b:a"] ? "custom" : "";
  }

  function formatPlayerTime(seconds) {
    if (!isFinite(seconds) || seconds < 0) {
      return "--:--";
    }
    seconds = Math.floor(seconds);
    var hours = Math.floor(seconds / 3600);
    var minutes = Math.floor((seconds % 3600) / 60);
    var rest = seconds % 60;
    var prefix = hours > 0 ? hours + ":" + String(minutes).padStart(2, "0") : String(minutes);
    return prefix + ":" + String(rest).padStart(2, "0");
  }

  function finitePositiveSeconds(value) {
    var number = Number(value);
    return isFinite(number) && number > 0 ? number : 0;
  }

  function programDurationSeconds(program) {
    if (!program) {
      return 0;
    }
    var seconds = finitePositiveSeconds(program.seconds);
    if (seconds > 0) {
      return seconds;
    }
    if (program.start && program.end && program.end > program.start) {
      return Math.round((program.end - program.start) / 1000);
    }
    return 0;
  }

  function playerNativeDuration(video) {
    return video && isFinite(video.duration) && video.duration > 0 ? video.duration : 0;
  }

  function playerQueryStart() {
    return finitePositiveSeconds(playerBaseQuery && playerBaseQuery.ss);
  }

  function playerQueryLimit() {
    return finitePositiveSeconds(playerBaseQuery && playerBaseQuery.t);
  }

  function playerConfiguredDuration() {
    if (playerSeekable && playerTimelineDuration > 0) {
      return playerTimelineDuration;
    }
    return playerQueryLimit() || playerFallbackDuration;
  }

  function playerPrefersConfiguredDuration() {
    return playerSeekable && playerConfiguredDuration() > 0;
  }

  function playerFiniteDuration(video) {
    var configuredDuration = playerConfiguredDuration();
    if (playerPrefersConfiguredDuration()) {
      playerKnownDuration = configuredDuration;
      return configuredDuration;
    }
    var nativeDuration = playerNativeDuration(video);
    if (nativeDuration > 0) {
      playerKnownDuration = nativeDuration;
      return nativeDuration;
    }
    if (configuredDuration > 0) {
      playerKnownDuration = configuredDuration;
      return configuredDuration;
    }
    return playerKnownDuration;
  }

  function playerCurrentTime(video, duration) {
    var offset = Math.max(0, playerQueryStart() - playerTimelineStart);
    if (!video || !isFinite(video.currentTime) || video.currentTime < 0) {
      return Math.min(offset, duration || 0);
    }
    if (!playerPrefersConfiguredDuration()) {
      return video.currentTime;
    }
    return Math.min(duration || 0, offset + video.currentTime);
  }

  function updatePlayerControls() {
    var video = byId("playerVideo");
    var playButton = byId("playerPlayButton");
    var seek = byId("playerSeek");
    var time = byId("playerTime");
    var muteButton = byId("playerMuteButton");
    var volume = byId("playerVolume");
    var quality = byId("playerQuality");
    var audio = byId("playerAudio");
    if (!video) {
      return;
    }
    var duration = playerFiniteDuration(video);
    var currentTime = playerCurrentTime(video, duration);
    if (playButton) {
      setIconOnlyControl(playButton, video.paused ? "play" : "pause", video.paused ? "再生" : "一時停止");
    }
    if (seek) {
      seek.disabled = duration <= 0;
      if (!playerSeeking) {
        seek.value = duration > 0 ? String(Math.min(1000, Math.max(0, Math.round((currentTime / duration) * 1000)))) : "0";
      }
    }
    if (time) {
      time.textContent = duration > 0 ? formatPlayerTime(currentTime) + " / " + formatPlayerTime(duration) : "LIVE";
    }
    if (muteButton) {
      setIconOnlyControl(muteButton, video.muted || video.volume === 0 ? "volume-x" : "volume-2", video.muted || video.volume === 0 ? "ミュート解除" : "ミュート");
    }
    if (volume && document.activeElement !== volume) {
      volume.value = String(video.muted ? 0 : video.volume);
    }
    if (quality) {
      quality.disabled = !playerSourceBuilder;
    }
    if (audio) {
      audio.disabled = !playerSourceBuilder;
    }
  }

  function setPlayerSource(url, query) {
    var video = byId("playerVideo");
    var openLink = byId("playerOpenLink");
    if (openLink) {
      var metaNode = byId("playerDialogMeta");
      openLink.href = playerWindowURL(url, metaNode ? metaNode.textContent : "");
    }
    if (!video) {
      return;
    }
    video.pause();
    video.removeAttribute("src");
    video.load();
    video.src = url;
    playerBaseQuery = cloneQuery(query || {});
    playerKnownDuration = playerConfiguredDuration();
    updatePlayerControls();
    video.play().catch(function () {
      // Browsers may block autoplay; controls remain available for manual start.
    }).finally(updatePlayerControls);
  }

  function togglePlayerPlayback() {
    var video = byId("playerVideo");
    if (!video || !video.src) {
      return;
    }
    if (video.paused) {
      video.play().catch(function () {
        // Manual controls remain available when playback is blocked.
      }).finally(updatePlayerControls);
    } else {
      video.pause();
      updatePlayerControls();
    }
  }

  function seekPlayerToRange() {
    var video = byId("playerVideo");
    var seek = byId("playerSeek");
    var duration = playerFiniteDuration(video);
    if (!video || !seek || duration <= 0) {
      return;
    }
    var nextTime = (Number(seek.value) / 1000) * duration;
    if (playerSeekable && playerSourceBuilder && playerPrefersConfiguredDuration()) {
      var nextQuery = cloneQuery(playerBaseQuery);
      nextQuery.ss = String(Math.max(0, Math.floor(playerTimelineStart + nextTime)));
      if (duration > 0) {
        nextQuery.t = String(Math.max(1, Math.floor(duration - nextTime)));
      }
      setPlayerSource(playerSourceBuilder(nextQuery), nextQuery);
      updatePlayerQualityControl(nextQuery, true);
      return;
    }
    video.currentTime = nextTime;
    updatePlayerControls();
  }

  function togglePlayerMute() {
    var video = byId("playerVideo");
    if (!video) {
      return;
    }
    video.muted = !video.muted;
    updatePlayerControls();
  }

  function changePlayerVolume() {
    var video = byId("playerVideo");
    var volume = byId("playerVolume");
    if (!video || !volume) {
      return;
    }
    var value = Number(volume.value);
    if (!isFinite(value)) {
      value = 1;
    }
    video.volume = Math.min(1, Math.max(0, value));
    video.muted = video.volume === 0;
    updatePlayerControls();
  }

  function togglePlayerFullscreen() {
    var shell = document.querySelector(".player-shell");
    if (!shell || !shell.requestFullscreen) {
      return;
    }
    if (document.fullscreenElement) {
      document.exitFullscreen().catch(function () {});
    } else {
      shell.requestFullscreen().catch(function () {});
    }
  }

  function bindPlayerVideoEvents() {
    var video = byId("playerVideo");
    if (!video) {
      return;
    }
    ["loadedmetadata", "durationchange", "timeupdate", "play", "pause", "volumechange", "ended", "waiting", "playing"].forEach(function (name) {
      video.addEventListener(name, updatePlayerControls);
    });
  }

  function updatePlayerQualityControl(query, enabled) {
    var select = byId("playerQuality");
    if (!select) {
      return;
    }
    select.disabled = !enabled;
    select.value = qualityFromQuery(query);
    if (select.value !== "custom") {
      var custom = Array.prototype.filter.call(select.options, function (option) {
        return option.value === "custom";
      })[0];
      if (custom) {
        custom.hidden = true;
      }
    } else {
      Array.prototype.forEach.call(select.options, function (option) {
        if (option.value === "custom") {
          option.hidden = false;
        }
      });
    }
  }

  function updatePlayerAudioControl(query, enabled) {
    var select = byId("playerAudio");
    if (!select) {
      return;
    }
    select.disabled = !enabled;
    select.value = audioFromQuery(query);
  }

  function changePlayerQuality(quality) {
    if (!playerSourceBuilder) {
      return;
    }
    var video = byId("playerVideo");
    var duration = playerFiniteDuration(video);
    var elapsed = Math.floor(playerCurrentTime(video, duration));
    var nextQuery = applyQualityToQuery(playerBaseQuery, quality);
    if (playerSeekable && elapsed >= 0) {
      nextQuery = nextQuery || {};
      nextQuery.ss = String(playerTimelineStart + elapsed);
      if (duration > 0) {
        nextQuery.t = String(Math.max(1, duration - elapsed));
      }
    }
    setPlayerSource(playerSourceBuilder(nextQuery), nextQuery);
    updatePlayerQualityControl(nextQuery, true);
    updatePlayerAudioControl(nextQuery, true);
  }

  function changePlayerAudio(audio) {
    if (!playerSourceBuilder) {
      return;
    }
    var video = byId("playerVideo");
    var duration = playerFiniteDuration(video);
    var elapsed = Math.floor(playerCurrentTime(video, duration));
    var nextQuery = applyAudioToQuery(playerBaseQuery, audio);
    if (playerSeekable && elapsed >= 0) {
      nextQuery = nextQuery || {};
      nextQuery.ss = String(playerTimelineStart + elapsed);
      if (duration > 0) {
        nextQuery.t = String(Math.max(1, duration - elapsed));
      }
    }
    setPlayerSource(playerSourceBuilder(nextQuery), nextQuery);
    updatePlayerQualityControl(nextQuery, true);
    updatePlayerAudioControl(nextQuery, true);
  }

  function openPlayerDialog(meta, url, options) {
    var dialog = byId("playerDialog");
    var video = byId("playerVideo");
    options = options || {};
    if (!dialog || !video || !dialog.showModal) {
      openURL(url);
      return;
    }
    playerDialogReturnFocus = rememberFocus();
    text(byId("playerDialogMeta"), meta || "");
    playerSourceBuilder = typeof options.sourceBuilder === "function" ? options.sourceBuilder : null;
    playerSeekable = Boolean(options.seekable);
    playerTimelineStart = playerSeekable ? finitePositiveSeconds(options.query && options.query.ss) : 0;
    playerTimelineDuration = playerSeekable ? finitePositiveSeconds(options.query && options.query.t) : 0;
    playerFallbackDuration = finitePositiveSeconds(options.duration);
    if (playerSeekable && playerTimelineDuration <= 0) {
      playerTimelineDuration = playerFallbackDuration;
    }
    playerKnownDuration = playerFallbackDuration;
    updatePlayerQualityControl(options.query || null, Boolean(playerSourceBuilder));
    updatePlayerAudioControl(options.query || null, Boolean(playerSourceBuilder));
    dialog.showModal();
    setPlayerSource(url, options.query || null);
    video.focus();
  }

  function openAdjustablePlayer(meta, buildURL, query, seekable, duration) {
    openPlayerDialog(meta, buildURL(query), {
      query: query,
      seekable: seekable,
      duration: duration,
      sourceBuilder: buildURL
    });
  }

  function closePlayerDialog() {
    var dialog = byId("playerDialog");
    if (dialog && dialog.close) {
      dialog.close();
    } else if (dialog) {
      dialog.removeAttribute("open");
      stopPlayerVideo();
      restoreFocus(playerDialogReturnFocus);
      playerDialogReturnFocus = null;
    }
  }

  function stopPlayerVideo() {
    var video = byId("playerVideo");
    if (!video) {
      return;
    }
    video.pause();
    video.removeAttribute("src");
    video.load();
    playerSourceBuilder = null;
    playerBaseQuery = null;
    playerSeekable = false;
    playerSeeking = false;
    playerTimelineStart = 0;
    playerTimelineDuration = 0;
    playerFallbackDuration = 0;
    playerKnownDuration = 0;
    updatePlayerQualityControl(null, false);
    updatePlayerAudioControl(null, false);
    updatePlayerControls();
  }

  var mp4Presets = {
    "1080p": { s: "1920x1080", video: "2600k", audio: "96k" },
    "720p": { s: "1280x720", video: "1400k", audio: "96k" },
    "540p": { s: "960x540", video: "900k", audio: "64k" },
    "360p": { s: "640x360", video: "550k", audio: "64k" }
  };

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

  function formatPercent(value) {
    if (typeof value !== "number" || !isFinite(value)) {
      return "取得不可";
    }
    return value.toFixed(value >= 10 ? 1 : 2) + "%";
  }

  function formatUptime(seconds) {
    if (typeof seconds !== "number" || !isFinite(seconds) || seconds < 0) {
      return "取得不可";
    }
    var total = Math.floor(seconds);
    var days = Math.floor(total / 86400);
    var hours = Math.floor((total % 86400) / 3600);
    var minutes = Math.floor((total % 3600) / 60);
    if (days > 0) {
      return days + "日 " + hours + "時間";
    }
    if (hours > 0) {
      return hours + "時間 " + minutes + "分";
    }
    return minutes + "分";
  }

  function renderActions(item, program, actions) {
    if (!actions || actions.length === 0) {
      return;
    }
    if (actions.indexOf("watch-mp4") >= 0 && actions.indexOf("delete-recorded") >= 0) {
      renderRecordedActions(item, program, actions);
      return;
    }
    var row = document.createElement("div");
    row.className = "row-actions";
    actions.forEach(function (name) {
      if (name === "reserve") {
        if (!program.isReserved && !program.isRecording) {
          var onAir = programOnAir(program);
          var label = onAir ? "録画開始" : "予約";
          var title = onAir ? "この放送中の番組を手動予約して録画開始を待つ" : "この番組を予約";
          row.appendChild(actionButton(label, title, function () {
            setBusy("Working");
            request("program/" + encodeURIComponent(program.id) + ".json", "PUT").then(refresh).then(function () {
              if (onAir && !operatorAlive()) {
                showError(new Error("オペレータが停止中です。録画を開始するには service operator execute を起動してください"));
              }
            }).catch(showError);
          }));
        }
      } else if (name === "unreserve" && program.isManualReserved && !program.isRecording) {
        row.appendChild(actionButton("予約削除", "手動予約を削除", function () {
          runAction("reserves/" + encodeURIComponent(program.id) + ".json", "DELETE", "この手動予約を削除しますか？", actionConfirmOptions("DELETE", "この手動予約を削除しますか？", program, "予約削除の確認"));
        }));
      } else if (name === "skip" && program.isReserved && !program.isManualReserved && !program.isSkip && !program.isRecording) {
        row.appendChild(actionButton("スキップ", "自動予約をスキップ", function () {
          runAction("reserves/" + encodeURIComponent(program.id) + "/skip", "PUT");
        }));
      } else if (name === "unskip" && program.isReserved && !program.isManualReserved && program.isSkip && !program.isRecording) {
        row.appendChild(actionButton("解除", "スキップを解除", function () {
          runAction("reserves/" + encodeURIComponent(program.id) + "/unskip", "PUT");
        }));
      } else if (name === "stop" && program.isRecording) {
        row.appendChild(actionButton("停止", "録画を停止", function () {
          runAction("recording/" + encodeURIComponent(program.id) + ".json", "DELETE", "この録画を停止しますか？", actionConfirmOptions("DELETE", "この録画を停止しますか？", program, "録画停止の確認"));
        }));
      } else if (name === "watch-recording-mp4" && program.isRecording) {
        row.appendChild(actionButton("視聴", "録画中の番組を視聴", function () {
          openAdjustablePlayer(program.title || program.id || "録画中", function (query) {
            return recordingWatchURL(program, "mp4", query);
          }, null, false);
        }));
      } else if (name === "preview-recording" && program.isRecording) {
        row.appendChild(actionButton("静止画", "録画中の静止画を開く", function () {
          openURL("/api/recording/" + encodeURIComponent(program.id) + "/preview");
        }));
      } else if (name === "watch-mp4") {
        row.appendChild(actionButton("視聴", "録画済み番組を視聴", function () {
          var initialQuery = presetQuery("540p");
          openAdjustablePlayer(program.title || program.id || "録画済み", function (query) {
            return recordedPlaybackURL(program, query);
          }, initialQuery, true, programDurationSeconds(program));
        }));
      } else if (name === "download") {
        row.appendChild(actionButton("ダウンロード", "録画ファイルを実体ファイル名で保存", function () {
          openURL("/api/recorded/" + encodeURIComponent(program.id) + "/file.m2ts");
        }));
      } else if (name === "xspf") {
        row.appendChild(actionButton("XSPF", "録画済み番組のプレイリストを開く", function () {
          openURL(recordedWatchURL(program, "xspf"));
        }));
      } else if (name === "delete-recorded") {
        row.appendChild(actionButton("削除", "録画済み項目とファイルを削除", function () {
          runAction("recorded/" + encodeURIComponent(program.id) + ".json", "DELETE", "この録画済み項目とファイルを削除しますか？", actionConfirmOptions("DELETE", "この録画済み項目とファイルを削除しますか？", program, "録画済み削除の確認"));
        }));
      } else if (name === "watch-channel-mp4") {
        var channelID = programChannelID(program);
        if (channelID) {
          row.appendChild(actionButton("視聴", "この番組のチャンネルを視聴", function () {
            openAdjustablePlayer(program.title || channelID || "チャンネル", function (query) {
              return channelURL(channelID, "watch", "mp4", query);
            }, null, false);
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

  function recordedActionButton(name, program, className) {
    if (name === "watch-mp4") {
      return actionButton("視聴", "録画済み番組を視聴", function () {
        var initialQuery = presetQuery("540p");
        openAdjustablePlayer(program.title || program.id || "録画済み", function (query) {
          return recordedPlaybackURL(program, query);
        }, initialQuery, true, programDurationSeconds(program));
      }, className);
    }
    if (name === "download") {
      return actionButton("ダウンロード", "録画ファイルを実体ファイル名で保存", function () {
        openURL("/api/recorded/" + encodeURIComponent(program.id) + "/file.m2ts");
      }, className);
    }
    if (name === "xspf") {
      return actionButton("XSPF", "録画済み番組のプレイリストを開く", function () {
        openURL(recordedWatchURL(program, "xspf"));
      }, className);
    }
    if (name === "delete-recorded") {
      return actionButton("削除", "録画済み項目とファイルを削除", function () {
        runAction("recorded/" + encodeURIComponent(program.id) + ".json", "DELETE", "この録画済み項目とファイルを削除しますか？", actionConfirmOptions("DELETE", "この録画済み項目とファイルを削除しますか？", program, "録画済み削除の確認"));
      }, className || "small-button danger-button");
    }
    if (name === "create-rule-from-program") {
      return actionButton("ルール作成", "この番組を元にルールフォームを開く", function () {
        closeProgramDialog();
        fillRuleFormFromProgram(program);
      }, className);
    }
    return null;
  }

  function renderRecordedActions(item, program, actions) {
    var row = document.createElement("div");
    row.className = "row-actions recorded-actions";

    var primary = actions.indexOf("watch-mp4") >= 0 ? recordedActionButton("watch-mp4", program, "small-button primary-action") : null;
    if (primary) {
      row.appendChild(primary);
    }

    var secondaryNames = actions.filter(function (name) {
      return name !== "watch-mp4" && name !== "delete-recorded";
    });
    var secondaryButtons = secondaryNames.map(function (name) {
      return recordedActionButton(name, program, "small-button");
    }).filter(Boolean);
    secondaryButtons.forEach(function (button) {
      row.appendChild(button);
    });

    var deleteButton = actions.indexOf("delete-recorded") >= 0 ? recordedActionButton("delete-recorded", program) : null;
    if (deleteButton) {
      var danger = document.createElement("div");
      danger.className = "row-danger-actions";
      danger.appendChild(deleteButton);
      row.appendChild(danger);
    }

    if (row.childNodes.length > 0) {
      item.appendChild(row);
    }
  }

  function channelLink(channelID, label) {
    var button = document.createElement("button");
    button.type = "button";
    button.className = "channel-link";
    button.textContent = label || channelID || "不明なチャンネル";
    button.title = "番組表をこのチャンネルで絞り込む";
    button.addEventListener("click", function () {
      state.scheduleChannel = channelID || "";
      window.location.hash = "schedule";
      renderSchedule();
    });
    return button;
  }

  function isActiveProgram(program) {
    return Boolean(program && program.id && state.activeProgramID && program.id === state.activeProgramID);
  }

  function programPreviewURL(program, resource, size) {
    return "/api/" + resource + "/" + encodeURIComponent(program.id) + "/preview?size=" + encodeURIComponent(size || "160x90");
  }

  function programPreviewUnavailable(program, resource) {
    return resource === "recorded" && Boolean(program && program.isRemoved);
  }

  function renderProgramPreview(program, resource) {
    if (!program || !program.recorded || (resource !== "recording" && resource !== "recorded")) {
      return null;
    }
    if (programPreviewUnavailable(program, resource)) {
      return null;
    }
    var image = document.createElement("img");
    image.className = "program-preview-image";
    image.alt = "";
    image.loading = "lazy";
    image.src = programPreviewURL(program, resource, "160x90");
    image.addEventListener("error", function () {
      var row = image.closest(".program-row");
      if (row) {
        row.classList.remove("with-preview");
        if (resource === "recorded") {
          renderRecordedPreviewWarning(row);
        }
      }
      image.remove();
    });
    return image;
  }

  function renderRecordedPreviewWarning(row) {
    if (!row || row.querySelector(".recorded-preview-warning")) {
      return;
    }
    row.classList.add("preview-unavailable");
    var body = row.querySelector(".program-row-body");
    if (!body) {
      return;
    }
    var warning = document.createElement("span");
    warning.className = "recorded-preview-warning";
    warning.title = "録画ファイルが移動または削除されているか、プレビュー生成に失敗しました";
    warning.textContent = "ファイル確認";
    body.appendChild(warning);
  }

  function clearProgramDialogPreview(root) {
    if (!root) {
      return;
    }
    var objectURL = root.getAttribute("data-object-url");
    if (objectURL) {
      URL.revokeObjectURL(objectURL);
      root.removeAttribute("data-object-url");
    }
    root.removeAttribute("data-preview-id");
    root.innerHTML = "";
    root.hidden = true;
  }

  function renderProgramDialogPreviewAlert(root, message) {
    clearProgramDialogPreview(root);
    var alert = document.createElement("div");
    alert.className = "program-dialog-alert";
    alert.setAttribute("role", "status");
    alert.textContent = message;
    root.appendChild(alert);
    root.hidden = false;
  }

  function appendProgramDialogPreviewImage(root, resource, src) {
    var figure = document.createElement("figure");
    figure.className = "program-dialog-preview-figure";
    var image = document.createElement("img");
    image.className = "program-dialog-preview-image";
    image.src = src;
    image.alt = "";
    image.loading = "lazy";
    image.addEventListener("error", function () {
      clearProgramDialogPreview(root);
    });
    var caption = document.createElement("figcaption");
    caption.textContent = resource === "recording" ? "録画中プレビュー" : "録画済みプレビュー";
    figure.appendChild(image);
    figure.appendChild(caption);
    root.appendChild(figure);
    root.hidden = false;
  }

  function renderProgramDialogPreview(root, program) {
    if (!root) {
      return;
    }
    clearProgramDialogPreview(root);
    if (!program || !program.id) {
      return;
    }
    root.setAttribute("data-preview-id", program.id);

    var resource = "";
    var previewProgram = program;
    if (program.isRecording) {
      resource = "recording";
    } else {
      var recorded = recordedProgramFor(program);
      if (recorded) {
        resource = "recorded";
        previewProgram = recorded;
      }
    }
    if (!resource || !previewProgram.recorded) {
      return;
    }

    var previewURL = programPreviewURL(previewProgram, resource, "480x270") + (resource === "recording" ? "&_=" + Date.now() : "");
    if (resource === "recording") {
      appendProgramDialogPreviewImage(root, resource, previewURL);
      return;
    }

    fetch(previewURL).then(function (response) {
      if (root.getAttribute("data-preview-id") !== program.id) {
        return null;
      }
      if (response.status === 410) {
        renderProgramDialogPreviewAlert(root, "録画ファイルが見つかりません。移動または削除されている可能性があります。");
        return null;
      }
      if (!response.ok) {
        clearProgramDialogPreview(root);
        return null;
      }
      return response.blob();
    }).then(function (blob) {
      if (!blob || root.getAttribute("data-preview-id") !== program.id) {
        return;
      }
      clearProgramDialogPreview(root);
      var objectURL = URL.createObjectURL(blob);
      root.setAttribute("data-object-url", objectURL);
      appendProgramDialogPreviewImage(root, resource, objectURL);
    }).catch(function () {
      if (root.getAttribute("data-preview-id") === program.id) {
        clearProgramDialogPreview(root);
      }
    });
  }

  function renderProgramRow(program, actions, showChannel, options) {
    program = decorateProgramState(program);
    options = options || {};
    var item = document.createElement("article");
    item.className = "program-row";
    if (options.compactActions) {
      item.className += " compact-actions";
    }
    item.classList.toggle("with-preview", Boolean(options.preview));
    item.classList.toggle("selected", isActiveProgram(program));
    item.classList.toggle("skip", Boolean(program.isSkip));
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

    var parts = [formatTime(program.start)];
    if (showChannel) {
      parts.push(channelName(program));
    }
    var meta = renderProgramMeta(parts, program);

    var body = document.createElement("div");
    body.className = "program-row-body";
    body.appendChild(title);
    body.appendChild(meta);
    renderProgramStateBadges(body, program, options);
    renderActions(body, program, actions);

    if (options.preview) {
      var previewResource = options.previewResource || "recording";
      var previewUnavailable = programPreviewUnavailable(program, previewResource);
      var preview = renderProgramPreview(program, previewResource);
      if (preview) {
        item.appendChild(preview);
      } else {
        item.classList.remove("with-preview");
      }
    }
    item.appendChild(body);
    if (previewUnavailable) {
      renderRecordedPreviewWarning(item);
    }
    return item;
  }

  function currentProgramForGroup(group, now) {
    var current = null;
    (group.programs || []).some(function (program) {
      if (program.start <= now && programEnd(program) > now) {
        current = program;
        return true;
      }
      return false;
    });
    return current;
  }

  function channelGroupHidden(group) {
    var firstProgramChannelID = programChannelID((group.programs || [])[0]);
    return state.scheduleHiddenChannels.indexOf(group.id) >= 0 || state.scheduleHiddenChannels.indexOf(firstProgramChannelID) >= 0;
  }

  function visibleChannelProgramGroups() {
    return channelProgramGroups().filter(function (group) {
      return !channelGroupHidden(group);
    });
  }

  function renderOnAirLiveRow(group, current) {
    var item = document.createElement("article");
    item.className = "live-channel-row";

    var channel = document.createElement("div");
    channel.className = "inline-channel-row live-channel-name";
    channel.appendChild(channelLink(group.id, group.name || group.id));

    var nowPlaying = document.createElement("button");
    nowPlaying.type = "button";
    nowPlaying.className = "program-title-button live-channel-program";
    if (current) {
      nowPlaying.textContent = programTitle(current);
      nowPlaying.title = "番組詳細を開く";
      nowPlaying.addEventListener("click", function () {
        openProgramDialog(current);
      });
    } else {
      nowPlaying.textContent = "番組情報なし";
      nowPlaying.disabled = true;
    }

    var time = document.createElement("span");
    time.className = "live-channel-time";
    time.textContent = current ? [formatClock(current.start), formatClock(programEnd(current))].filter(Boolean).join("-") : "";

    var actions = document.createElement("div");
    actions.className = "row-actions live-channel-actions";
    if (group.id) {
      actions.appendChild(actionButton("視聴", "このチャンネルをライブ視聴", function () {
        openAdjustablePlayer(group.name || group.id || "チャンネル", function (query) {
          return channelURL(group.id, "watch", "mp4", query);
        }, null, false);
      }, "small-button"));
    }

    item.appendChild(channel);
    item.appendChild(nowPlaying);
    item.appendChild(time);
    item.appendChild(actions);
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
    return scheduleWindowHoursByMode.all;
  }

  function scheduleCardMinimumHeight(durationHeight) {
    if (state.scheduleZoomLevel === "precise") {
      return Math.max(24, durationHeight);
    }
    if (state.scheduleZoomLevel === "detail") {
      return Math.max(12, durationHeight);
    }
    return durationHeight;
  }

  function syncScheduleZoomControls() {
    var config = scheduleZoomConfig();
    var index = scheduleZoomIndex(config.id);
    var label = byId("scheduleZoomLabel");
    var out = byId("scheduleZoomOutButton");
    var input = byId("scheduleZoomInButton");
    if (label) {
      label.textContent = config.label;
    }
    if (out) {
      out.disabled = index <= 0;
    }
    if (input) {
      input.disabled = index >= scheduleZoomLevels.length - 1;
    }
  }

  function setScheduleZoomLevel(nextLevel) {
    var currentIndex = scheduleZoomIndex(state.scheduleZoomLevel);
    var nextIndex = scheduleZoomIndex(nextLevel);
    if (nextIndex === currentIndex) {
      syncScheduleZoomControls();
      return;
    }
    var scroll = document.querySelector(".schedule-guide-scroll");
    var minuteOffset = scroll ? scroll.scrollTop / scheduleMinutePixels() : 0;
    state.scheduleZoomLevel = scheduleZoomLevels[nextIndex].id;
    saveScheduleZoomLevel();
    state.scheduleGuideScroll.top = Math.round(minuteOffset * scheduleMinutePixels());
    renderSchedule();
    syncScheduleZoomControls();
  }

  function changeScheduleZoom(delta) {
    var index = scheduleZoomIndex(state.scheduleZoomLevel);
    var nextIndex = Math.max(0, Math.min(scheduleZoomLevels.length - 1, index + delta));
    setScheduleZoomLevel(scheduleZoomLevels[nextIndex].id);
  }

  function channelProgramGroups() {
    var groups = [];
    (state.schedule || []).forEach(function (channel, index) {
      var channelID = scheduleChannelID(channel);
      var groupID = channelID || scheduleChannelName(channel) || "unknown";
      var displayName = scheduleChannelName(channel) || channelID || "不明なチャンネル";
      var group = {
        id: groupID,
        name: displayName,
        order: index,
        type: scheduleChannelType(channel),
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

  function renderList(id, items, emptyText, limit, actions, options) {
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
      root.appendChild(renderProgramRow(program, actions, true, options));
    });
  }

  function recordedPageCount(total) {
    return Math.max(1, Math.ceil(total / state.recordedPageSize));
  }

  function clampRecordedPage(total) {
    var maxPage = recordedPageCount(total);
    state.recordedPage = Math.max(1, Math.min(maxPage, Number(state.recordedPage) || 1));
    return state.recordedPage;
  }

  function recordedPageItems(items) {
    var page = clampRecordedPage((items || []).length);
    var start = (page - 1) * state.recordedPageSize;
    return (items || []).slice(start, start + state.recordedPageSize);
  }

  function updateRecordedPaginationControls(total) {
    var pageSize = normalizeRecordedPageSize(state.recordedPageSize);
    state.recordedPageSize = pageSize;
    var page = clampRecordedPage(total);
    var maxPage = recordedPageCount(total);
    var start = total > 0 ? ((page - 1) * pageSize) + 1 : 0;
    var end = total > 0 ? Math.min(total, page * pageSize) : 0;
    var pageSizeSelect = byId("recordedListPageSize");
    if (pageSizeSelect) {
      pageSizeSelect.value = String(pageSize);
    }
    text(byId("recordedListPageSummary"), total > 0 ? start + "-" + end + " / " + total + "件" : "0件");
    [
      { id: "recordedListFirstPage", disabled: page <= 1 || total === 0 },
      { id: "recordedListPrevPage", disabled: page <= 1 || total === 0 },
      { id: "recordedListNextPage", disabled: page >= maxPage || total === 0 },
      { id: "recordedListLastPage", disabled: page >= maxPage || total === 0 }
    ].forEach(function (item) {
      var button = byId(item.id);
      if (button) {
        button.disabled = item.disabled;
      }
    });
  }

  function renderOnAirList() {
    var root = byId("onAirList");
    if (!root) {
      return;
    }
    var groups = visibleChannelProgramGroups().sort(function (a, b) {
      return compareScheduleChannelGroups(a, b);
    });
    root.innerHTML = "";
    if (!groups.length) {
      root.className = "list empty";
      root.textContent = "現在放送中の番組はありません";
      return;
    }
    var now = Date.now();
    root.className = "list live-channel-list";
    groups.forEach(function (group) {
      root.appendChild(renderOnAirLiveRow(group, currentProgramForGroup(group, now)));
    });
  }

  function renderSchedule() {
    var root = byId("scheduleList");
    if (!root) {
      return;
    }
    var channels = state.schedule || [];
    ensureMobileScheduleChannelDefault(channels);
    var channelOrder = [];
    var channelMeta = {};
    var channelGroups = [];
    var now = Date.now();
    var hours = scheduleWindowHours();
    var windowStart = state.scheduleDay ? dateKeyStart(state.scheduleDay) : now;
    var until = hours > 0 ? windowStart + (hours * 60 * 60 * 1000) : 0;

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
          order: channelOrder.length,
          type: scheduleChannelType(channel),
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
        } else if (!item.channel) {
          item.channel = { id: channelID || groupID, name: displayName, type: scheduleChannelType(channel) };
        }
        if (item.channel) {
          item.channel.name = scheduleProgramChannelName(item, displayName);
        }
        channelMeta[groupID].programs.push(item);
      });
    });
    channelOrder.sort(function (a, b) {
      return compareScheduleChannelGroups(channelMeta[a] || {}, channelMeta[b] || {});
    }).forEach(function (id) {
      if (channelMeta[id] && channelMeta[id].programs.length) {
        channelMeta[id].programs.sort(function (a, b) {
          return (a.start || 0) - (b.start || 0);
        });
        channelGroups.push(channelMeta[id]);
      }
    });
    renderScheduleChannelOptions(channels);
    renderScheduleFilterOptions(channels);
    syncScheduleFilterControls();
    syncScheduleZoomControls();
    root.innerHTML = "";
    if (!channelGroups.length) {
      renderScheduleEmpty(root, channels);
      return;
    }
    renderScheduleGuide(root, channelGroups);
  }

  function selectedOptionLabel(id, fallback) {
    var select = byId(id);
    if (select && select.selectedIndex >= 0 && select.options[select.selectedIndex]) {
      return select.options[select.selectedIndex].textContent;
    }
    return fallback || "";
  }

  function scheduleActiveFilterLabels() {
    var labels = [];
    if (state.scheduleChannel) {
      labels.push("チャンネル: " + selectedOptionLabel("scheduleChannel", state.scheduleChannel));
    }
    if (state.scheduleType) {
      labels.push("種別: " + selectedOptionLabel("scheduleType", state.scheduleType));
    }
    if (state.scheduleDay) {
      labels.push("日付: " + selectedOptionLabel("scheduleDay", state.scheduleDay));
    }
    if (state.scheduleGenre) {
      labels.push("ジャンル: " + selectedOptionLabel("scheduleGenre", state.scheduleGenre));
    }
    if (state.scheduleWindowMode && state.scheduleWindowMode !== "all") {
      labels.push("範囲: " + selectedOptionLabel("scheduleWindow", state.scheduleWindowMode));
    }
    if (state.scheduleHiddenChannels.length) {
      labels.push("非表示: " + state.scheduleHiddenChannels.length + "ch");
    }
    return labels;
  }

  function resetScheduleFilters() {
    state.scheduleChannel = "";
    state.scheduleChannelTouched = false;
    state.scheduleType = "";
    state.scheduleDay = "";
    state.scheduleGenre = "";
    state.scheduleWindowMode = "all";
    state.scheduleHiddenChannels = [];
    state.listFilters.schedule = defaultListFilter("schedule");
    saveListFilters();
    saveHiddenChannels();
    state.scheduleGuideScroll = { left: 0, top: 0 };
    renderSchedule();
  }

  function syncScheduleFilterControls() {
    var type = byId("scheduleType");
    if (type) {
      type.value = state.scheduleType;
      if (type.value !== state.scheduleType) {
        state.scheduleType = "";
        type.value = "";
      }
    }
    var windowMode = byId("scheduleWindow");
    if (windowMode) {
      windowMode.value = state.scheduleWindowMode || "all";
      if (windowMode.value !== (state.scheduleWindowMode || "all")) {
        state.scheduleWindowMode = "all";
        windowMode.value = "all";
      }
    }
  }

  function renderScheduleEmpty(root, channels) {
    var filters = scheduleActiveFilterLabels();
    root.className = "list empty contextual-empty";
    root.innerHTML = "";

    var heading = document.createElement("strong");
    var copy = document.createElement("p");
    root.appendChild(heading);
    root.appendChild(copy);

    if (!channels.length) {
      heading.textContent = "番組表データがありません";
      copy.textContent = "Mirakurun または取得処理の状態を確認してから、番組表を再読み込みしてください。";
      var recovery = document.createElement("div");
      recovery.className = "empty-actions";
      recovery.appendChild(actionButton("再読込", "番組表データを再読み込み", refresh));
      var logs = document.createElement("a");
      logs.className = "small-button empty-link-button";
      logs.href = "#logs";
      logs.textContent = "ログを確認";
      recovery.appendChild(logs);
      root.appendChild(recovery);
      return;
    }

    heading.textContent = "条件に一致する番組はありません";
    copy.textContent = filters.length ? "適用中: " + filters.join(" / ") : "現在時刻以降に表示できる番組がありません。";
    if (filters.length) {
      var actions = document.createElement("div");
      actions.className = "empty-actions";
      actions.appendChild(actionButton("条件を解除", "番組表の絞り込みをすべて解除", resetScheduleFilters));
      root.appendChild(actions);
    }
  }

  function renderScheduleGuide(root, channelGroups) {
    var firstStart = null;
    var lastEnd = null;
    var minuteHeight = scheduleMinutePixels();

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
    scroll.tabIndex = 0;
    scroll.addEventListener("scroll", function () {
      state.scheduleGuideScroll = {
        left: scroll.scrollLeft,
        top: scroll.scrollTop
      };
    });
    scroll.addEventListener("wheel", function (event) {
      if (!event.ctrlKey) {
        return;
      }
      event.preventDefault();
      changeScheduleZoom(event.deltaY > 0 ? -1 : 1);
    }, { passive: false });

    var grid = document.createElement("div");
    grid.className = "schedule-guide-grid";
    grid.setAttribute("data-schedule-zoom", state.scheduleZoomLevel);
    grid.style.gridTemplateColumns = "48px repeat(" + channelGroups.length + ", minmax(132px, 1fr))";
    grid.style.minWidth = (48 + channelGroups.length * 132) + "px";

    var corner = document.createElement("div");
    corner.className = "schedule-corner";
    corner.textContent = "時刻";
    grid.appendChild(corner);

    channelGroups.forEach(function (group) {
      var heading = document.createElement("div");
      heading.className = "schedule-channel-head";
      var nameRow = document.createElement("div");
      nameRow.className = "schedule-channel-name-row";
      nameRow.appendChild(channelLink(group.id, group.name));
      heading.appendChild(nameRow);
      var mediaRow = document.createElement("div");
      mediaRow.className = "schedule-channel-media-row";
      if (group.logo) {
        var logo = document.createElement("img");
        logo.className = "schedule-channel-logo";
        logo.src = channelURL(group.id, "logo", "png");
        logo.alt = "";
        logo.loading = "lazy";
        mediaRow.appendChild(logo);
      }
      mediaRow.appendChild(renderChannelActions(group.id, group.name));
      heading.appendChild(mediaRow);
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

  function renderChannelActions(channelID, label) {
    var row = document.createElement("div");
    row.className = "channel-actions";
    if (!channelID || channelID === "unknown") {
      return row;
    }
    row.appendChild(actionButton("視聴", "チャンネルを視聴", function () {
      openAdjustablePlayer(label || channelID || "チャンネル", function (query) {
        return channelURL(channelID, "watch", "mp4", query);
      }, null, false);
    }));
    return row;
  }

  function categoryClass(category) {
    var value = String(category || "").toLowerCase();
    if (value.indexOf("anime") >= 0 || value.indexOf("アニメ") >= 0) {
      return " category-anime";
    }
    if (value.indexOf("movie") >= 0 || value.indexOf("cinema") >= 0 || value.indexOf("映画") >= 0) {
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
    if (value.indexOf("information") >= 0 || value.indexOf("情報") >= 0) {
      return " category-information";
    }
    if (value.indexOf("variety") >= 0 || value.indexOf("バラエティ") >= 0) {
      return " category-variety";
    }
    if (value.indexOf("documentary") >= 0 || value.indexOf("ドキュメンタリー") >= 0) {
      return " category-documentary";
    }
    if (value.indexOf("theater") >= 0 || value.indexOf("劇場") >= 0) {
      return " category-theater";
    }
    if (value.indexOf("hobby") >= 0 || value.indexOf("趣味") >= 0) {
      return " category-hobby";
    }
    if (value.indexOf("welfare") >= 0 || value.indexOf("福祉") >= 0) {
      return " category-welfare";
    }
    if (value.indexOf("etc") >= 0 || value.indexOf("その他") >= 0) {
      return " category-etc";
    }
    return "";
  }

  function renderScheduleCard(program, timelineStart, minuteHeight) {
    program = decorateProgramState(program);
    var card = document.createElement("article");
    var stateLabel = programStateLabelText(program);
    card.className = "schedule-card" + categoryClass(program.category);
    card.classList.toggle("recording", Boolean(program.isRecording));
    card.classList.toggle("reserved", Boolean(program.isReserved && !program.isRecording));
    card.classList.toggle("conflict", Boolean(program.isConflict));
    card.classList.toggle("skip", Boolean(program.isSkip));
    card.classList.toggle("has-state", Boolean(stateLabel));
    var end = programEnd(program);
    var durationMinutes = Math.max(1, (end - program.start) / 60000);
    var top = Math.max(0, Math.round(((program.start - timelineStart) / 60000) * minuteHeight));
    var durationHeight = Math.max(1, Math.round(durationMinutes * minuteHeight));
    var height = Math.round(scheduleCardMinimumHeight(durationHeight));
    card.style.top = top + "px";
    card.style.height = height + "px";
    card.classList.toggle("short", durationMinutes < 15);
    card.classList.toggle("very-short", durationMinutes < 8);
    card.classList.toggle("selected", isActiveProgram(program));
    card.title = [programTitle(program), program.detail || program.description || ""].filter(Boolean).join("\n");
    card.tabIndex = 0;
    card.setAttribute("role", "button");
    card.setAttribute("aria-label", [programTitle(program), stateLabel, "の詳細を開く"].filter(Boolean).join(" "));

    var time = document.createElement("span");
    time.className = "schedule-card-time";
    time.textContent = formatClock(program.start) + "-" + formatClock(end);

    if (stateLabel) {
      var stateBadge = document.createElement("span");
      stateBadge.className = "schedule-card-state";
      stateBadge.textContent = stateLabel;
      card.appendChild(stateBadge);
    }

    var title = document.createElement("strong");
    title.textContent = programTitle(program);

    var meta = document.createElement("span");
    meta.className = "schedule-card-meta";
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
    var preview = byId("programDialogPreview");
    var description = byId("programDialogDescription");
    var actions = byId("programDialogActions");
    var end = programEnd(program);
    state.selectedProgram = program;
    state.activeProgramID = program && program.id ? program.id : "";
    text(title, programTitle(program));
    setProgramMeta(meta, [formatTime(program.start) + " - " + formatTime(end), channelName(program), formatDuration(program.start, end)], program);
    renderProgramDialogPreview(preview, program);
    text(description, program.detail || program.description || "番組説明はありません。");
    if (actions) {
      actions.innerHTML = "";
      renderActions(actions, program, programDialogActions(program));
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
    var days = {};
    select.innerHTML = "";
    var all = document.createElement("option");
    all.value = "";
    all.textContent = "すべて";
    select.appendChild(all);
    channelProgramGroups().forEach(function (group) {
      (group.programs || []).forEach(function (program) {
        if (program && program.start && (!program.end || program.end >= Date.now())) {
          days[dateKey(program.start)] = true;
        }
      });
    });
    Object.keys(days).sort().forEach(function (key) {
      var day = dateKeyStart(key);
      var option = document.createElement("option");
      option.value = key;
      option.textContent = dayLabel(day);
      select.appendChild(option);
    });
    select.value = current;
    if (select.value !== current) {
      state.scheduleDay = "";
      select.value = "";
    }
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
    var filtered = sortedRuleEntries(filteredRules(state.rules));
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
      item.className = "program-row rule-row";
      item.tabIndex = 0;

      var body = document.createElement("div");
      body.className = "rule-row-body";

      var title = document.createElement("strong");
      title.textContent = "#" + index + (rule.isDisabled ? " 無効" : " 有効");

      var meta = document.createElement("span");
      meta.textContent = ruleSummary(rule);

      var row = document.createElement("div");
      row.className = "row-actions";
      row.appendChild(actionButton(rule.isDisabled ? "有効化" : "無効化", "ルールの有効状態を切り替え", function () {
        runAction("rules/" + index + "/" + (rule.isDisabled ? "enable" : "disable") + ".json", "PUT");
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

      body.appendChild(title);
      body.appendChild(meta);
      item.appendChild(body);
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
    var cfg = state.config || {};
    var strataPanel = byId("strataConfigPanel");
    if (strataPanel) {
      strataPanel.hidden = cfg.schema !== "strata/config";
    }
    if (cfg.schema === "strata/config") {
      renderStrataConfigForm(cfg);
    }
  }

  function renderStrataConfigForm(cfg) {
    var web = cfg.web || {};
    var authentication = web.authentication || {};
    var recording = cfg.recording || {};
    var lowSpace = recording.lowSpace || {};
    var previewCache = cfg.previewCache || {};
    var services = cfg.services || {};
    var advanced = cfg.advanced || {};
    setControlValue("strataMirakurunURL", (cfg.mirakurun || {}).url);
    setControlValue("strataListenAddress", web.listenAddress);
    setControlValue("strataWebPort", web.port);
    setControlValue("strataAuthEnabled", authentication.enabled);
    setControlValue("strataRecordingDirectory", recording.directory);
    setControlValue("strataFilenameFormat", recording.filenameFormat);
    setControlValue("strataRecordingPriority", (cfg.mirakurun || {}).recordingPriority);
    setControlValue("strataConflictedPriority", (cfg.mirakurun || {}).conflictedPriority);
    setControlValue("strataLowSpaceThreshold", lowSpace.thresholdMB);
    setControlValue("strataLowSpaceAction", lowSpace.action);
	setControlValue("strataPreviewCacheMaxAge", previewCache.maxAgeDays);
	setControlValue("strataPreviewCacheMaxSize", previewCache.maxSizeMB);
    setControlValue("strataExcludedServices", services.excluded);
    setControlValue("strataServiceOrder", services.order);
    setControlValue("strataNormalizationForm", advanced.normalizationForm);
    var root = byId("strataAuthUsers");
    root.innerHTML = "";
    (authentication.users || []).forEach(function (user) {
      appendStrataUser(user);
    });
    updateStrataAuthUsersState();
  }

  function appendStrataUser(user) {
    var root = byId("strataAuthUsers");
    if (!root) {
      return;
    }
    var row = document.createElement("div");
    row.className = "config-user-row";
    row.innerHTML = '<label><span>ユーザー名</span><input class="config-form-control strata-user-name" type="text" autocomplete="username" required></label>' +
      '<label><span>パスワード</span><input class="config-form-control strata-user-password" type="password" autocomplete="new-password" spellcheck="false"></label>' +
      '<button class="icon-button danger-button strata-user-remove" type="button" title="ユーザーを削除" aria-label="ユーザーを削除">&times;</button>';
    row.querySelector(".strata-user-name").value = user && user.username || "";
    var password = row.querySelector(".strata-user-password");
    if (user && user.passwordConfigured) {
      password.placeholder = "変更しない";
      row.dataset.passwordConfigured = "true";
    }
    row.querySelector(".strata-user-remove").addEventListener("click", function () {
      row.remove();
    });
    root.appendChild(row);
  }

  function updateStrataAuthUsersState() {
    var enabled = byId("strataAuthEnabled").checked;
    byId("strataAuthUsers").hidden = !enabled;
    byId("addStrataUserButton").hidden = !enabled;
  }

  function requiredString(id, label) {
    var value = controlString(id);
    if (!value) {
      showError(new Error(label + "を入力してください"));
      return null;
    }
    return value;
  }

  function requiredInteger(id, label, minimum, maximum) {
    var value = Number(controlString(id));
    if (!Number.isInteger(value) || value < minimum || value > maximum) {
      showError(new Error(label + "は" + minimum + "から" + maximum + "の整数にしてください"));
      return null;
    }
    return value;
  }

  function strataServiceList(id, label) {
    var values = splitList(controlString(id)).map(Number);
    if (values.some(function (value) { return !Number.isInteger(value) || value <= 0; })) {
      showError(new Error(label + "はカンマ区切りの正の整数にしてください"));
      return null;
    }
    return values;
  }

  function readStrataConfigForm() {
    var mirakurunURL = requiredString("strataMirakurunURL", "Mirakurun URL");
    var listenAddress = requiredString("strataListenAddress", "待受アドレス");
    var port = requiredInteger("strataWebPort", "ポート", 1, 65535);
    var directory = requiredString("strataRecordingDirectory", "録画保存先");
    var filenameFormat = requiredString("strataFilenameFormat", "ファイル名形式");
    var threshold = requiredInteger("strataLowSpaceThreshold", "空き容量しきい値", 0, Number.MAX_SAFE_INTEGER);
	var previewMaxAge = requiredInteger("strataPreviewCacheMaxAge", "プレビュー保持日数", 0, Number.MAX_SAFE_INTEGER);
	var previewMaxSize = requiredInteger("strataPreviewCacheMaxSize", "プレビュー上限", 0, Number.MAX_SAFE_INTEGER);
    var excluded = strataServiceList("strataExcludedServices", "除外サービス");
    var order = strataServiceList("strataServiceOrder", "サービス順");
    if (mirakurunURL === null || listenAddress === null || port === null || directory === null || filenameFormat === null || threshold === null || previewMaxAge === null || previewMaxSize === null || excluded === null || order === null) {
      return null;
    }
    var enabled = byId("strataAuthEnabled").checked;
    var users = [];
    var names = {};
    Array.prototype.forEach.call(document.querySelectorAll("#strataAuthUsers .config-user-row"), function (row) {
      var username = row.querySelector(".strata-user-name").value.trim();
      var password = row.querySelector(".strata-user-password").value;
      if (!username || names[username] || (!password && row.dataset.passwordConfigured !== "true")) {
        users = null;
        return;
      }
      names[username] = true;
      var user = { username: username, passwordConfigured: row.dataset.passwordConfigured === "true" };
      if (password) {
        user.password = password;
      }
      users.push(user);
    });
    if (users === null || (enabled && users.length === 0)) {
      showError(new Error("認証ユーザーの名前とパスワードを確認してください"));
      return null;
    }
    var cfg = state.config || {};
    return {
      schema: cfg.schema,
      version: cfg.version,
      mirakurun: { url: mirakurunURL, recordingPriority: Number(controlString("strataRecordingPriority")), conflictedPriority: Number(controlString("strataConflictedPriority")) },
      recording: { directory: directory, filenameFormat: filenameFormat, lowSpace: { thresholdMB: threshold, action: controlString("strataLowSpaceAction") } },
	  previewCache: { maxAgeDays: previewMaxAge, maxSizeMB: previewMaxSize },
      web: { listenAddress: listenAddress, port: port, authentication: { enabled: enabled, users: users } },
      services: { excluded: excluded, order: order },
      advanced: { normalizationForm: controlString("strataNormalizationForm") }
    };
  }

  function saveStrataConfigFromForm() {
    var config = readStrataConfigForm();
    if (!config) {
      return;
    }
    confirmAction("Strata設定を保存しますか？", { danger: false, okLabel: "保存", title: "設定保存の確認" }).then(function (confirmed) {
      if (!confirmed) {
        return;
      }
      setBusy("設定保存中");
      sendConfigJSON(JSON.stringify(config)).then(function (savedConfig) {
        state.config = savedConfig || {};
        render();
        setBusy("設定を保存しました");
      }).catch(showError);
    });
  }

  function renderSettings() {
    var root = byId("settingsList");
    if (!root) {
      return;
    }
    var cfg = state.config || {};
	var rows = [
      ["形式", "Strata config v" + cfg.version],
      ["Mirakurun", (cfg.mirakurun || {}).url],
      ["録画保存先", (cfg.recording || {}).directory],
      ["待受", ((cfg.web || {}).listenAddress || "") + ":" + ((cfg.web || {}).port || "")],
      ["認証", ((cfg.web || {}).authentication || {}).enabled],
		["ユーザー", (((cfg.web || {}).authentication || {}).users || []).map(function (user) { return user.username; })]
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
  }

  function renderStatus() {
    var root = byId("resourceList");
    if (!root) {
      return;
    }
    var status = state.status || {};
    var system = status.system || {};
    var memory = system.memory || {};
    var operator = status.operator || {};
    var rows = [
      ["WUI", "稼働中" + (system.pid ? " / PID " + system.pid : "")],
      ["オペレータ", (operator.alive ? "稼働中" : "停止中") + (operator.pid ? " / PID " + operator.pid : "")],
      ["CPUコア", system.core],
      ["OS", [system.os, system.arch].filter(Boolean).join(" / ")],
      ["Go", system.goVersion],
      ["Goroutine", system.goroutines],
      ["起動時刻", system.startedAt ? formatTime(system.startedAt) : ""],
      ["稼働時間", formatUptime(system.uptimeSeconds)],
      ["メモリ使用量", formatBytes(memory.alloc)],
      ["ヒープ使用量", formatBytes(memory.heapAlloc)],
      ["システム確保", formatBytes(memory.sys)],
      ["GC回数", memory.numGC]
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
    renderStorage();
    renderMetrics();
  }

  function renderStorage() {
    var root = byId("storageList");
    if (!root) {
      return;
    }
    var storage = state.storage || {};
    var rows = [
      ["対象", storage.path || "録画保存先"],
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

  function renderMetrics() {
    renderStoragePieChart();
    renderResourceLineChart();
  }

  function renderStoragePieChart() {
    var root = byId("storagePieChart");
    if (!root) {
      return;
    }
    var storage = state.metrics && state.metrics.current || state.storage || {};
    var storagePath = state.storage && state.storage.path ? state.storage.path : "";
    var used = numberValue(storage.storageUsed, storage.used);
    var total = numberValue(storage.storageTotal, storage.size);
    var avail = numberValue(storage.storageAvail, storage.avail);
    var recorded = Math.max(0, Math.min(used, numberValue(storage.storageRecorded, storage.recorded)));
    if (!total || total <= 0) {
      root.innerHTML = '<div class="chart-empty">取得不可</div>';
      text(byId("storageChartSummary"), "取得不可");
      return;
    }
    var usedPercent = Math.max(0, Math.min(100, used / total * 100));
    var recordedPercent = Math.max(0, Math.min(100, recorded / total * 100));
    var otherUsedPercent = Math.max(0, usedPercent - recordedPercent);
    var freePercent = Math.max(0, 100 - usedPercent);
    var recordedEnd = recordedPercent;
    var otherEnd = recordedPercent + otherUsedPercent;
    root.innerHTML = [
      '<div class="pie-chart-ring" style="--recorded-end:', recordedEnd.toFixed(2), '; --other-end:', otherEnd.toFixed(2), '"></div>',
      '<div class="pie-chart-center"><strong>', formatPercent(usedPercent), '</strong><span>使用中</span></div>',
      '<div class="chart-legend">',
      '<span><i class="legend-recorded"></i>録画ファイル ', formatBytes(recorded), '</span>',
      '<span><i class="legend-used"></i>その他 ', formatBytes(Math.max(0, used - recorded)), '</span>',
      '<span><i class="legend-free"></i>空き ', formatBytes(avail || Math.max(0, total - used)), '</span>',
      '<span><i class="legend-total"></i>総量 ', formatBytes(total), '</span>',
      '</div>'
    ].join("");
    text(byId("storageChartSummary"), (storagePath ? "対象 " + storagePath + " / " : "録画保存先 / ") + "総量 " + formatBytes(total) + " / 空き " + formatPercent(freePercent));
  }

  function renderResourceLineChart() {
    var root = byId("resourceLineChart");
    if (!root) {
      return;
    }
    var samples = state.metrics && Array.isArray(state.metrics.samples) ? state.metrics.samples : [];
    if (samples.length < 2) {
      root.innerHTML = '<div class="chart-empty">履歴を収集中</div>';
      text(byId("resourceChartSummary"), "直近6時間");
      return;
    }
    var now = Date.now();
    var windowMS = ((state.metrics && state.metrics.windowSeconds) || 21600) * 1000;
    var points = samples.map(function (sample) {
      return {
        time: Date.parse(sample.time),
        cpu: optionalNumber(sample.cpuPercent),
        memory: optionalNumber(sample.memoryPercent)
      };
    }).filter(function (point) {
      return isFinite(point.time) && point.time >= now - windowMS;
    });
    if (points.length < 2) {
      root.innerHTML = '<div class="chart-empty">履歴を収集中</div>';
      return;
    }
    var width = 640;
    var height = 220;
    var padLeft = 38;
    var padRight = 14;
    var padTop = 18;
    var padBottom = 28;
    var minTime = Math.min.apply(null, points.map(function (point) { return point.time; }));
    var maxTime = Math.max(now, Math.max.apply(null, points.map(function (point) { return point.time; })));
    if (maxTime <= minTime) {
      maxTime = minTime + 1;
    }
    var cpuPath = linePath(points, "cpu", minTime, maxTime, width, height, padLeft, padRight, padTop, padBottom);
    var memoryPath = linePath(points, "memory", minTime, maxTime, width, height, padLeft, padRight, padTop, padBottom);
    var latest = points[points.length - 1];
    root.innerHTML = [
      '<svg viewBox="0 0 ', width, ' ', height, '" class="metric-svg" aria-hidden="true">',
      '<line x1="', padLeft, '" y1="', padTop, '" x2="', padLeft, '" y2="', height - padBottom, '" class="chart-axis"></line>',
      '<line x1="', padLeft, '" y1="', height - padBottom, '" x2="', width - padRight, '" y2="', height - padBottom, '" class="chart-axis"></line>',
      chartGridLine(25, width, height, padLeft, padRight, padTop, padBottom),
      chartGridLine(50, width, height, padLeft, padRight, padTop, padBottom),
      chartGridLine(75, width, height, padLeft, padRight, padTop, padBottom),
      cpuPath ? '<path d="' + cpuPath + '" class="chart-line chart-line-cpu"></path>' : "",
      memoryPath ? '<path d="' + memoryPath + '" class="chart-line chart-line-memory"></path>' : "",
      '<text x="', padLeft, '" y="', height - 8, '" class="chart-label">-6h</text>',
      '<text x="', width - padRight, '" y="', height - 8, '" text-anchor="end" class="chart-label">now</text>',
      '<text x="6" y="', padTop + 4, '" class="chart-label">100%</text>',
      '</svg>',
      '<div class="chart-legend">',
      '<span><i class="legend-cpu"></i>CPU ', formatPercent(latest.cpu), '</span>',
      '<span><i class="legend-memory"></i>メモリ ', formatPercent(latest.memory), '</span>',
      '</div>'
    ].join("");
    text(byId("resourceChartSummary"), "直近6時間 / " + samples.length + "点");
  }

  function numberValue() {
    for (var i = 0; i < arguments.length; i += 1) {
      var value = arguments[i];
      if (typeof value === "number" && isFinite(value)) {
        return value;
      }
    }
    return 0;
  }

  function optionalNumber(value) {
    return typeof value === "number" && isFinite(value) ? value : NaN;
  }

  function linePath(points, key, minTime, maxTime, width, height, padLeft, padRight, padTop, padBottom) {
    var plotWidth = width - padLeft - padRight;
    var plotHeight = height - padTop - padBottom;
    var path = "";
    points.forEach(function (point) {
      var value = point[key];
      if (typeof value !== "number" || !isFinite(value)) {
        return;
      }
      var x = padLeft + ((point.time - minTime) / (maxTime - minTime)) * plotWidth;
      var y = padTop + (1 - Math.max(0, Math.min(100, value)) / 100) * plotHeight;
      path += (path ? " L " : "M ") + x.toFixed(1) + " " + y.toFixed(1);
    });
    return path;
  }

  function chartGridLine(percent, width, height, padLeft, padRight, padTop, padBottom) {
    var y = padTop + (1 - percent / 100) * (height - padTop - padBottom);
    return '<line x1="' + padLeft + '" y1="' + y.toFixed(1) + '" x2="' + (width - padRight) + '" y2="' + y.toFixed(1) + '" class="chart-grid"></line>';
  }

  function forceScheduler() {
    runAction("scheduler/force", "PUT", "スケジューラを実行しますか？");
  }

  function controlString(id) {
    var control = byId(id);
    return control ? control.value.trim() : "";
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
      sendJSON("rules", "POST", rule).then(function () {
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
    updateOperationalStatus();

    var filteredReserves = sortedPrograms(filteredPrograms(state.reserves, "reserves"), "reserves");
    var recordedNewestFirst = sortedPrograms(state.recorded, "recorded");
    var filteredRecorded = sortedPrograms(filteredPrograms(state.recorded, "recorded"), "recorded");
    var pagedRecorded = recordedPageItems(filteredRecorded);

    updateListCategoryOptions("reserveListCategory", state.reserves, "reserves");
    updateListCategoryOptions("recordedListCategory", state.recorded, "recorded");
    updateListFilterSummary("reserveListFilterSummary", filteredReserves.length, state.reserves.length);
    updateListFilterSummary("recordedListFilterSummary", filteredRecorded.length, state.recorded.length);
    updateRecordedPaginationControls(filteredRecorded.length);

    renderList("recordingList", state.recording, "録画中の番組はありません", 8, ["watch-recording-mp4", "preview-recording", "stop"], { preview: true, previewResource: "recording" });
    renderList("reserveList", state.reserves, "予約はありません", 8, ["skip", "unskip", "unreserve"], { hideReservedBadge: true, compactActions: true });
    renderList("reserveListPage", filteredReserves, "条件に一致する予約はありません", 100, ["skip", "unskip", "unreserve"], { hideReservedBadge: true, compactActions: true });
    renderList("recordedList", recordedNewestFirst, "録画済み番組はありません", 8, ["watch-mp4", "download", "xspf", "delete-recorded"], { preview: true, previewResource: "recorded" });
    renderList("recordedListPage", pagedRecorded, "条件に一致する録画済み番組はありません", state.recordedPageSize, ["watch-mp4", "download", "xspf", "delete-recorded"], { preview: true, previewResource: "recorded" });
    renderOnAirList();
    renderSchedule();
    renderRules();
    renderSettings();
    renderStatus();
    renderRuleFormState();
  }

  function setBusy(message) {
    var badge = byId("statusBadge");
    if (badge) {
      badge.textContent = message;
      badge.className = "status-badge";
    }
    var scheduleOperator = byId("scheduleOperatorStatus");
    if (scheduleOperator) {
      scheduleOperator.textContent = message;
      scheduleOperator.className = "schedule-status-pill";
    }
  }

  function setListPlaceholder(id, message, className) {
    var root = byId(id);
    if (!root) {
      return;
    }
    root.innerHTML = "";
    root.className = className || "list empty";
    root.textContent = message;
  }

  function setRecoverableListPlaceholder(id, message) {
    var root = byId(id);
    if (!root) {
      return;
    }
    root.innerHTML = "";
    root.className = "list empty error recoverable-empty";
    var copy = document.createElement("p");
    copy.textContent = message;
    var actions = document.createElement("div");
    actions.className = "empty-actions";
    var retry = actionButton("再試行", "データを再読み込み", refresh);
    var logs = document.createElement("a");
    logs.className = "small-button empty-link-button";
    logs.href = "#logs";
    logs.textContent = "ログを確認";
    actions.appendChild(retry);
    actions.appendChild(logs);
    root.appendChild(copy);
    root.appendChild(actions);
  }

  function setRefreshLoading(loading) {
    ["refreshButton", "scheduleRefreshButton"].forEach(function (id) {
      var button = byId(id);
      if (!button) {
        return;
      }
      button.disabled = Boolean(loading);
      button.setAttribute("aria-busy", loading ? "true" : "false");
      if (id === "scheduleRefreshButton") {
        setIconOnlyControl(button, "refresh-cw", loading ? "読込中" : "再読込");
      } else {
        button.textContent = loading ? "更新中" : "更新";
      }
    });
    document.body.classList.toggle("is-loading", Boolean(loading));
  }

  function renderInitialLoadingState() {
    setRefreshLoading(true);
    if (state.hasLoaded) {
      return;
    }
    [
      "recordingList",
      "onAirList",
      "reserveList",
      "reserveListPage",
      "recordedList",
      "recordedListPage",
      "ruleList"
    ].forEach(function (id) {
      setListPlaceholder(id, "読み込み中");
    });
  }

  function renderInitialLoadError(error) {
    setRefreshLoading(false);
    if (state.hasLoaded) {
      return;
    }
    [
      "recordingList",
      "onAirList",
      "reserveList",
      "reserveListPage",
      "recordedList",
      "recordedListPage",
      "ruleList"
    ].forEach(function (id) {
      setRecoverableListPlaceholder(id, "読み込みに失敗しました");
    });
    text(byId("ruleListFilterSummary"), "0件");
    text(byId("reserveListFilterSummary"), "0件");
    text(byId("recordedListFilterSummary"), "0件");
    if (error) {
      setBusy(error.message);
      showError(error);
    }
  }

  function showError(error) {
    state.lastError = error;
    var badge = byId("statusBadge");
    if (badge) {
      badge.textContent = error.message;
      badge.className = "status-badge error";
    }
    updateOperationalStatus();
  }

  function refresh() {
    state.isLoading = true;
    state.lastError = null;
    setBusy("読み込み中");
    renderInitialLoadingState();
    Promise.all([
      api("status"),
      api("reserves"),
      api("recording"),
      api("recorded"),
      api("schedule"),
      api("rules"),
      api("config"),
      api("storage").catch(function () {
        return null;
      }),
      api("metrics").catch(function () {
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
      state.metrics = result[8] || null;
      state.hasLoaded = true;
      state.isLoading = false;
      state.lastError = null;
      setRefreshLoading(false);
      render();
    }).catch(function (error) {
      state.isLoading = false;
      renderInitialLoadError(error);
      showError(error);
    });
  }

  function refreshOperationalData() {
    Promise.all([
      api("status"),
      api("reserves"),
      api("recording"),
      api("storage").catch(function () {
        return state.storage;
      }),
      api("metrics").catch(function () {
        return state.metrics;
      })
    ]).then(function (result) {
      state.status = result[0] || {};
      state.reserves = result[1] || [];
      state.recording = result[2] || [];
      state.storage = result[3] || null;
      state.metrics = result[4] || null;
      state.lastError = null;
      render();
    }).catch(showError);
  }

  function refreshMetrics() {
    api("metrics").then(function (result) {
      state.metrics = result || null;
      renderMetrics();
      updateOperationalStatus();
    }).catch(function () {
      renderMetrics();
    });
  }

  function refreshLogs() {
    setBusy("ログ読み込み中");
    Promise.all([
      apiText("log/scheduler"),
      apiText("log/operator"),
      apiText("log/wui")
    ]).then(function (result) {
      setLog("schedulerLog", result[0]);
      setLog("operatorLog", result[1]);
      setLog("wuiLog", result[2]);
      updateOperationalStatus();
    }).catch(showError);
  }

  function bindListFilter(filterName, queryID, categoryID, sortID) {
    var query = byId(queryID);
    var category = byId(categoryID);
    var sort = byId(sortID);
    var current = state.listFilters[filterName] || {};
    if (query) {
      query.value = current.query || "";
      query.addEventListener("input", function () {
        state.listFilters[filterName].query = query.value;
        if (filterName === "recorded") {
          state.recordedPage = 1;
        }
        saveListFilters();
        render();
      });
    }
    if (category) {
      category.addEventListener("change", function () {
        state.listFilters[filterName].category = category.value;
        if (filterName === "recorded") {
          state.recordedPage = 1;
        }
        saveListFilters();
        render();
      });
    }
    if (sort) {
      sort.value = current.sort || sort.value;
      if (current.sort && sort.value !== current.sort) {
        sort.value = sort.options.length ? sort.options[0].value : "";
      }
      state.listFilters[filterName].sort = sort.value;
      sort.addEventListener("change", function () {
        state.listFilters[filterName].sort = sort.value;
        if (filterName === "recorded") {
          state.recordedPage = 1;
        }
        saveListFilters();
        render();
      });
    }
  }

  function resetListFilterControls(filterName, ids, renderFn) {
    var defaults = defaultListFilter(filterName);
    state.listFilters[filterName] = defaults;
    if (filterName === "recorded") {
      state.recordedPage = 1;
    }
    [
      { id: ids.query, value: defaults.query || "" },
      { id: ids.category, value: defaults.category || defaults.state || "" },
      { id: ids.sort, value: defaults.sort || "" }
    ].forEach(function (item) {
      var control = byId(item.id);
      if (control) {
        control.value = item.value;
      }
    });
    saveListFilters();
    (renderFn || render)();
  }

  function resetListFilter(filterName, ids, renderFn) {
    var button = byId(ids.button);
    if (button) {
      button.addEventListener("click", function () {
        resetListFilterControls(filterName, ids, renderFn);
      });
    }
    var query = byId(ids.query);
    if (query) {
      query.addEventListener("keydown", function (event) {
        if (event.key === "Escape") {
          event.preventDefault();
          resetListFilterControls(filterName, ids, renderFn);
          query.focus();
        }
      });
    }
  }

  function bindRecordedPagination() {
    var pageSize = byId("recordedListPageSize");
    if (pageSize) {
      pageSize.value = String(state.recordedPageSize);
      pageSize.addEventListener("change", function () {
        state.recordedPageSize = normalizeRecordedPageSize(pageSize.value);
        state.recordedPage = 1;
        saveRecordedPageSize();
        render();
      });
    }
    [
      { id: "recordedListFirstPage", icon: "chevrons-left", label: "最初のページ" },
      { id: "recordedListPrevPage", icon: "chevron-left", label: "前のページ" },
      { id: "recordedListNextPage", icon: "chevron-right", label: "次のページ" },
      { id: "recordedListLastPage", icon: "chevrons-right", label: "最後のページ" }
    ].forEach(function (control) {
      setIconOnlyControl(byId(control.id), control.icon, control.label);
    });
    [
      { id: "recordedListFirstPage", page: function () { return 1; } },
      { id: "recordedListPrevPage", page: function () { return state.recordedPage - 1; } },
      { id: "recordedListNextPage", page: function () { return state.recordedPage + 1; } },
      {
        id: "recordedListLastPage",
        page: function () {
          var filteredRecorded = sortedPrograms(filteredPrograms(state.recorded, "recorded"), "recorded");
          return recordedPageCount(filteredRecorded.length);
        }
      }
    ].forEach(function (control) {
      var button = byId(control.id);
      if (!button) {
        return;
      }
      button.addEventListener("click", function () {
        var filteredRecorded = sortedPrograms(filteredPrograms(state.recorded, "recorded"), "recorded");
        state.recordedPage = Math.max(1, Math.min(recordedPageCount(filteredRecorded.length), control.page()));
        render();
        var firstRow = byId("recordedListPage") && byId("recordedListPage").querySelector(".program-row[tabindex='0']");
        if (firstRow && typeof firstRow.focus === "function") {
          firstRow.focus({ preventScroll: true });
        }
      });
    });
  }

  document.addEventListener("DOMContentLoaded", function () {
    syncStickyOffsets();
    window.addEventListener("resize", syncStickyOffsets);
    window.addEventListener("orientationchange", syncStickyOffsets);
    window.addEventListener("resize", function () {
      if (!isMobileScheduleLayout()) {
        closeScheduleMenu();
        closeScheduleFilter();
      }
    });
    initNavigation();
    document.querySelectorAll("[data-view-link]").forEach(function (link) {
      link.addEventListener("click", function () {
        closeScheduleMenu();
        closeScheduleFilter();
      });
    });
    initKeyboardShortcuts();
    var refreshButton = byId("refreshButton");
    if (refreshButton) {
      refreshButton.addEventListener("click", refresh);
    }
    var recordedCleanupButton = byId("recordedCleanupButton");
    if (recordedCleanupButton) {
      recordedCleanupButton.addEventListener("click", cleanupRecorded);
    }
    if (!metricsRefreshTimer) {
      metricsRefreshTimer = window.setInterval(refreshMetrics, 30000);
    }
    bindListFilter("reserves", "reserveListQuery", "reserveListCategory", "reserveListSort");
    bindListFilter("recorded", "recordedListQuery", "recordedListCategory", "recordedListSort");
    bindRecordedPagination();
    resetListFilter("reserves", {
      button: "reserveListFilterReset",
      category: "reserveListCategory",
      query: "reserveListQuery",
      sort: "reserveListSort"
    });
    resetListFilter("recorded", {
      button: "recordedListFilterReset",
      category: "recordedListCategory",
      query: "recordedListQuery",
      sort: "recordedListSort"
    });
    var ruleListQuery = byId("ruleListQuery");
    if (ruleListQuery) {
      ruleListQuery.value = state.listFilters.rules.query || "";
      ruleListQuery.addEventListener("input", function () {
        state.listFilters.rules.query = ruleListQuery.value;
        saveListFilters();
        renderRules();
      });
    }
    var ruleListState = byId("ruleListState");
    if (ruleListState) {
      ruleListState.value = state.listFilters.rules.state || "";
      ruleListState.addEventListener("change", function () {
        state.listFilters.rules.state = ruleListState.value;
        saveListFilters();
        renderRules();
      });
    }
    var ruleListSort = byId("ruleListSort");
    if (ruleListSort) {
      ruleListSort.value = state.listFilters.rules.sort || "indexAsc";
      if (state.listFilters.rules.sort && ruleListSort.value !== state.listFilters.rules.sort) {
        ruleListSort.value = ruleListSort.options.length ? ruleListSort.options[0].value : "indexAsc";
      }
      state.listFilters.rules.sort = ruleListSort.value;
      ruleListSort.addEventListener("change", function () {
        state.listFilters.rules.sort = ruleListSort.value;
        saveListFilters();
        renderRules();
      });
    }
    resetListFilter("rules", {
      button: "ruleListFilterReset",
      category: "ruleListState",
      query: "ruleListQuery",
      sort: "ruleListSort"
    }, renderRules);
    var playerDialogClose = byId("playerDialogClose");
    if (playerDialogClose) {
      playerDialogClose.addEventListener("click", closePlayerDialog);
    }
    bindPlayerVideoEvents();
    var playerVideo = byId("playerVideo");
    if (playerVideo) {
      playerVideo.addEventListener("click", togglePlayerPlayback);
      playerVideo.addEventListener("keydown", function (event) {
        if (event.key === " " || event.key === "Enter") {
          event.preventDefault();
          togglePlayerPlayback();
        }
      });
    }
    var playerPlayButton = byId("playerPlayButton");
    if (playerPlayButton) {
      playerPlayButton.addEventListener("click", togglePlayerPlayback);
    }
    var playerSeek = byId("playerSeek");
    if (playerSeek) {
      playerSeek.addEventListener("input", function () {
        playerSeeking = true;
      });
      playerSeek.addEventListener("change", function () {
        seekPlayerToRange();
        playerSeeking = false;
      });
    }
    var playerMuteButton = byId("playerMuteButton");
    if (playerMuteButton) {
      playerMuteButton.addEventListener("click", togglePlayerMute);
    }
    var playerVolume = byId("playerVolume");
    if (playerVolume) {
      playerVolume.addEventListener("input", changePlayerVolume);
      playerVolume.addEventListener("change", changePlayerVolume);
    }
    var playerQuality = byId("playerQuality");
    if (playerQuality) {
      playerQuality.addEventListener("change", function () {
        changePlayerQuality(playerQuality.value);
      });
    }
    var playerAudio = byId("playerAudio");
    if (playerAudio) {
      playerAudio.addEventListener("change", function () {
        changePlayerAudio(playerAudio.value);
      });
    }
    var playerFullscreenButton = byId("playerFullscreenButton");
    if (playerFullscreenButton) {
      playerFullscreenButton.addEventListener("click", togglePlayerFullscreen);
    }
    var playerDialog = byId("playerDialog");
    if (playerDialog) {
      bindDialogFocusTrap(playerDialog);
      playerDialog.addEventListener("click", function (event) {
        if (event.target === playerDialog) {
          closePlayerDialog();
        }
      });
      playerDialog.addEventListener("close", function () {
        stopPlayerVideo();
        restoreFocus(playerDialogReturnFocus);
        playerDialogReturnFocus = null;
      });
    }
    var forceSchedulerButton = byId("forceSchedulerButton");
    if (forceSchedulerButton) {
      forceSchedulerButton.addEventListener("click", forceScheduler);
    }
    var strataConfigForm = byId("strataConfigForm");
    if (strataConfigForm) {
      strataConfigForm.addEventListener("submit", function (event) {
        event.preventDefault();
      });
    }
    var saveStrataConfigButton = byId("saveStrataConfigButton");
    if (saveStrataConfigButton) {
      saveStrataConfigButton.addEventListener("click", saveStrataConfigFromForm);
    }
    var strataAuthEnabled = byId("strataAuthEnabled");
    if (strataAuthEnabled) {
      strataAuthEnabled.addEventListener("change", updateStrataAuthUsersState);
    }
    var addStrataUserButton = byId("addStrataUserButton");
    if (addStrataUserButton) {
      addStrataUserButton.addEventListener("click", function () {
        appendStrataUser({});
        var names = byId("strataAuthUsers").querySelectorAll(".strata-user-name");
        names[names.length - 1].focus();
      });
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
    var scheduleRefreshButton = byId("scheduleRefreshButton");
    if (scheduleRefreshButton) {
      scheduleRefreshButton.addEventListener("click", refresh);
    }
    var programDialogClose = byId("programDialogClose");
    if (programDialogClose) {
      programDialogClose.addEventListener("click", closeProgramDialog);
    }
    var programDialog = byId("programDialog");
    if (programDialog) {
      bindDialogFocusTrap(programDialog);
      programDialog.addEventListener("click", function (event) {
        if (event.target === programDialog) {
          closeProgramDialog();
        }
      });
      programDialog.addEventListener("close", function () {
        clearProgramDialogPreview(byId("programDialogPreview"));
        state.selectedProgram = null;
        restoreFocus(programDialogReturnFocus);
        programDialogReturnFocus = null;
      });
    }
    var confirmDialog = byId("confirmDialog");
    if (confirmDialog) {
      bindDialogFocusTrap(confirmDialog);
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
        state.scheduleChannelTouched = true;
        state.scheduleChannel = scheduleChannel.value;
        renderSchedule();
        if (isMobileScheduleLayout()) {
          closeScheduleFilter();
        }
      });
    }
    var scheduleMenuButton = byId("scheduleMenuButton");
    if (scheduleMenuButton) {
      scheduleMenuButton.addEventListener("click", function () {
        setScheduleMenuOpen(!document.body.classList.contains("schedule-menu-open"));
      });
    }
    var scheduleFilterButton = byId("scheduleFilterButton");
    if (scheduleFilterButton) {
      scheduleFilterButton.addEventListener("click", function () {
        setScheduleFilterOpen(!document.body.classList.contains("schedule-filter-open"));
      });
    }
    var scheduleMenuBackdrop = byId("scheduleMenuBackdrop");
    if (scheduleMenuBackdrop) {
      scheduleMenuBackdrop.addEventListener("click", closeScheduleMenu);
    }
    document.addEventListener("touchstart", function (event) {
      if (!isMobileScheduleLayout() || state.currentView !== "schedule" || event.touches.length !== 1) {
        scheduleMenuTouchStart = null;
        return;
      }
      var touch = event.touches[0];
      scheduleMenuTouchStart = {
        x: touch.clientX,
        y: touch.clientY,
        open: document.body.classList.contains("schedule-menu-open")
      };
    }, { passive: true });
    document.addEventListener("touchend", function (event) {
      if (!scheduleMenuTouchStart || !event.changedTouches.length) {
        return;
      }
      var touch = event.changedTouches[0];
      var dx = touch.clientX - scheduleMenuTouchStart.x;
      var dy = touch.clientY - scheduleMenuTouchStart.y;
      var horizontal = Math.abs(dx) > 60 && Math.abs(dx) > Math.abs(dy) * 1.4;
      if (horizontal && !scheduleMenuTouchStart.open && scheduleMenuTouchStart.x <= 28 && dx > 0) {
        setScheduleMenuOpen(true);
      } else if (horizontal && scheduleMenuTouchStart.open && dx < 0) {
        closeScheduleMenu();
      }
      scheduleMenuTouchStart = null;
    }, { passive: true });
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
          renderOnAirList();
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
        renderOnAirList();
      });
    }
    var scheduleClearHiddenButton = byId("scheduleClearHiddenButton");
    if (scheduleClearHiddenButton) {
      scheduleClearHiddenButton.addEventListener("click", function () {
        state.scheduleHiddenChannels = [];
        saveHiddenChannels();
        renderSchedule();
        renderOnAirList();
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
    var scheduleZoomOutButton = byId("scheduleZoomOutButton");
    if (scheduleZoomOutButton) {
      scheduleZoomOutButton.addEventListener("click", function () {
        changeScheduleZoom(-1);
      });
    }
    var scheduleZoomInButton = byId("scheduleZoomInButton");
    if (scheduleZoomInButton) {
      scheduleZoomInButton.addEventListener("click", function () {
        changeScheduleZoom(1);
      });
    }
    document.addEventListener("keydown", function (event) {
      if (!event.ctrlKey || !document.activeElement || !document.activeElement.closest(".schedule-guide-scroll")) {
        return;
      }
      if (event.key === "+" || event.key === "=") {
        event.preventDefault();
        changeScheduleZoom(1);
      } else if (event.key === "-" || event.key === "_") {
        event.preventDefault();
        changeScheduleZoom(-1);
      }
    });
    subscribeRealtimeRefresh();
    refresh();
    setInterval(refreshOperationalData, 30000);
    setInterval(refreshLogs, 60000);
    refreshLogs();
  });
}());
