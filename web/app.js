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
  var playerSubtitleSourceBuilder = null;
  var playerBaseQuery = null;
  var playerSeekable = false;
  var playerSeeking = false;
  var playerTimelineStart = 0;
  var playerTimelineDuration = 0;
  var playerFallbackDuration = 0;
  var playerKnownDuration = 0;
  var playerCurrentURL = "";
  var pendingConfirmResolve = null;
  var scheduleMenuTouchStart = null;
  var metricsRefreshTimer = null;
  var refreshQueued = false;
  var refreshVersion = 0;
  var operationalRefreshInFlight = false;
  var metricsRefreshInFlight = false;
  var realtimeSourceID = "wui-" + Date.now() + "-" + Math.random().toString(36).slice(2);
  var announcementTimer = null;
  var apiRequestTimeoutMs = 15000;
  var strataConfigFormDirty = false;
  var focusableControlSelector = "button, [href], input, select, textarea, [tabindex]:not([tabindex='-1'])";
  var state = {
    status: null,
    reserves: [],
    recording: [],
    recorded: [],
    schedule: [],
    broadcasting: null,
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
    scheduleGuideScrollToCurrentTime: false,
    listFilters: loadListFilters(),
    searchPage: 1,
    recordedPage: 1,
    recordedPageSize: loadRecordedPageSize(),
    programStateIndex: { reserves: {}, recording: {} },
    realtimeChannel: null,
    rulesLoaded: false,
    configLoaded: false,
    storageLoaded: false,
    viewDataRequests: {},
    viewDataErrors: {},
    hasLoaded: false,
    isLoading: false,
    lastError: null,
    lastOperationalAnnouncement: ""
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
      reserves: { query: "", category: "", sort: "startAsc" },
      search: { query: "", category: "", type: "", title: "", description: "", programID: "", channelID: "", startHour: "", endHour: "" }
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

  function announce(message) {
    var liveRegion = byId("liveAnnouncement");
    if (liveRegion) {
      if (announcementTimer !== null) {
        window.clearTimeout(announcementTimer);
      }
      liveRegion.textContent = "";
      announcementTimer = window.setTimeout(function () {
        liveRegion.textContent = message || "";
        announcementTimer = null;
      }, 0);
    }
  }

  function debounce(fn, delay) {
    var timer = null;
    return function () {
      var args = arguments;
      if (timer !== null) {
        window.clearTimeout(timer);
      }
      timer = window.setTimeout(function () {
        timer = null;
        fn.apply(null, args);
      }, delay);
    };
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

  function isHiddenDialogControl(control) {
    if (!control || control.hidden || control.getAttribute("aria-hidden") === "true") {
      return true;
    }
    if (control.closest("[hidden]")) {
      return true;
    }
    var closedDetails = control.closest("details:not([open])");
    if (closedDetails && control.parentElement !== closedDetails) {
      return true;
    }
    if (typeof window.getComputedStyle === "function") {
      var style = window.getComputedStyle(control);
      if (style.display === "none" || style.visibility === "hidden") {
        return true;
      }
    }
    return false;
  }

  function dialogFocusableControls(dialog) {
    return Array.prototype.slice.call(dialog.querySelectorAll(focusableControlSelector)).filter(function (control) {
      return !control.disabled && !isHiddenDialogControl(control) && typeof control.focus === "function";
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

  function requestError(path, response) {
    if (response.status === 401) {
      return new Error("認証が必要です。ログイン状態を確認してください");
    }
    if (response.status === 403) {
      return new Error("この操作を実行する権限がありません");
    }
    if (response.status === 404) {
      return new Error("対象が見つかりません。録画状態が変わった可能性があります");
    }
    if (response.status === 409) {
      return new Error("対象の番組はスクランブルなどの理由で利用できません");
    }
    if (response.status === 410) {
      return new Error("録画ファイルが利用できません。録画が終了してから再試行してください");
    }
    if (response.status === 429) {
      return new Error("リクエストが多すぎます。少し待ってから再試行してください");
    }
    if (response.status >= 500) {
      return new Error("サーバーで処理に失敗しました。WUIログを確認してください");
    }
    return new Error(path + " の処理に失敗しました（HTTP " + response.status + "）");
  }

  function networkError(error) {
    if (error && error.name === "AbortError") {
      return new Error("サーバーからの応答がありません。接続を確認して再試行してください");
    }
    if (error && error.name === "TypeError") {
      return new Error("サーバーに接続できません。ネットワーク接続を確認して再試行してください");
    }
    return error;
  }

  function fetchWithTimeout(url, options) {
    options = options || {};
    if (typeof window.AbortController !== "function") {
      return fetch(url, options);
    }
    var controller = new window.AbortController();
    var timer = window.setTimeout(function () {
      controller.abort();
    }, apiRequestTimeoutMs);
    options.signal = controller.signal;
    return fetch(url, options).then(function (response) {
      window.clearTimeout(timer);
      return response;
    }, function (error) {
      window.clearTimeout(timer);
      throw error;
    });
  }

  function parseJSONResponse(response, path) {
    return response.text().then(function (body) {
      if (!body) {
        return {};
      }
      try {
        return JSON.parse(body);
      } catch (error) {
        throw new Error(path + " の応答を読み取れませんでした。WUIログを確認してください");
      }
    });
  }

  function request(path, method) {
    return fetchWithTimeout("/api/" + path, {
      credentials: "same-origin",
      method: method || "GET"
    }).then(function (response) {
      if (!response.ok) {
        throw requestError(path, response);
      }
      return parseJSONResponse(response, path).then(function (result) {
        publishMutation(path, method || "GET");
        return result;
      });
    }).catch(function (error) {
      throw networkError(error);
    });
  }

  function sendJSON(path, method, value) {
    return fetchWithTimeout("/api/" + path, {
      body: JSON.stringify(value),
      credentials: "same-origin",
      headers: { "Content-Type": "application/json" },
      method: method
    }).then(function (response) {
      if (!response.ok) {
        throw requestError(path, response);
      }
      return parseJSONResponse(response, path).then(function (result) {
        publishMutation(path, method);
        return result;
      });
    }).catch(function (error) {
      throw networkError(error);
    });
  }

	function sendConfigJSON(raw) {
		return fetchWithTimeout("/api/config", {
			body: raw,
			credentials: "same-origin",
			headers: { "Content-Type": "application/json" },
      method: "PUT"
    }).then(function (response) {
      if (!response.ok) {
        throw requestError("config.json", response);
      }
      return parseJSONResponse(response, "config.json").then(function (result) {
        publishRealtime("notify-config");
        return result;
      });
    }).catch(function (error) {
      throw networkError(error);
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
    var message = { event: eventName, at: Date.now(), source: realtimeSourceID };
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
      if (!message || message.source === realtimeSourceID || !refreshEvents[message.event]) {
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
    return fetchWithTimeout("/api/" + path, {
      credentials: "same-origin"
    }).then(function (response) {
      if (response.status === 204) {
        return "";
      }
      if (!response.ok) {
        throw requestError(path, response);
      }
      return response.text();
    }).catch(function (error) {
      throw networkError(error);
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
    var raw = program && (program.title || program.fullTitle) || "";
    return normalizeAribExternalFlags(stripProgramFlags(raw)) || program.id || "無題";
  }

  function programFullTitle(program) {
    return normalizeAribExternalFlags(program.fullTitle || program.title || program.id || "無題");
  }

  function programFlagNames(program) {
    var known = ["新", "終", "再", "字", "デ", "解", "無", "二", "S", "SS", "初", "生", "Ｎ", "映", "多", "双"];
    var aliases = { "無料": "無", "生放送": "生" };
    var found = {};
    var flags = [];
    function add(flag) {
      if (!found[flag]) {
        found[flag] = true;
        flags.push(flag);
      }
    }
    (Array.isArray(program && program.flags) ? program.flags : []).forEach(function (flag) {
      var value = String(flag || "");
      if (value) {
        add(aribExternalFlagName(value) || value);
      }
    });
    var raw = String(program && (program.fullTitle || program.title) || "");
    known.forEach(function (flag) {
      var escaped = flag.replace(/[.*+?^${}()|[\]\\]/g, "\\$&");
      if (new RegExp("(?:\\[|【|\\()" + escaped + "(?:\\]|】|\\))").test(raw)) {
        add(flag);
      }
    });
    Object.keys(aliases).forEach(function (source) {
      var escaped = source.replace(/[.*+?^${}()|[\]\\]/g, "\\$&");
      if (new RegExp("(?:\\[|【|\\()" + escaped + "(?:\\]|】|\\))").test(raw)) {
        add(aliases[source]);
      }
    });
    Array.from(raw).forEach(function (symbol) {
      if (isAribExternalFlag(symbol)) {
        add(aribExternalFlagName(symbol));
      }
    });
    return flags;
  }

  function isAribExternalFlag(value) {
    if (!value || value.length === 0) {
      return false;
    }
    return Boolean(aribExternalFlagName(value));
  }

  function aribExternalFlagName(value) {
    var names = {
      "\uE0F8": "HV", "\uE0F9": "SD", "\uE0FA": "P", "\uE0FB": "W", "\uE0FC": "MV",
      "\uE0FD": "手", "\uE0FE": "字", "\uE0FF": "双",
      "\uE180": "デ", "\uE181": "S", "\uE182": "二", "\uE183": "多", "\uE184": "解", "\uE185": "SS", "\uE186": "B", "\uE187": "N",
      "\uE18A": "天", "\uE18B": "交", "\uE18C": "映", "\uE18D": "無", "\uE18E": "料",
      "⚿": "鍵マーク", "\uE190": "前", "\uE191": "後", "\uE192": "再", "\uE193": "新", "\uE194": "初", "\uE195": "終", "\uE196": "生", "\uE197": "販", "\uE198": "声", "\uE199": "吹", "\uE19A": "PPV", "㊙": "秘", "\uE19C": "ほか"
    };
    if (Object.prototype.hasOwnProperty.call(names, value)) {
      return names[value];
    }
    var legacyNames = {
      "🈟": "新", "🈡": "終", "🈞": "再", "🈑": "字", "🈓": "デ", "🈖": "解", "🈚": "無", "🈔": "二",
      "🅂": "S", "🅍": "SS", "🈠": "初", "🈢": "生", "🄽": "N", "🈙": "映", "🈕": "多", "🈒": "双"
    };
    if (Object.prototype.hasOwnProperty.call(legacyNames, value)) {
      return legacyNames[value];
    }
    return "";
  }

  function normalizeAribExternalFlags(value) {
    return Array.from(String(value || "")).map(function (character) {
      return aribExternalFlagName(character) || character;
    }).join("");
  }

  function stripProgramFlags(title) {
    var known = ["無料", "生放送", "新", "終", "再", "字", "デ", "解", "無", "二", "S", "SS", "初", "生", "Ｎ", "映", "多", "双"];
    var pattern = known.map(function (flag) {
      return flag.replace(/[.*+?^${}()|[\]\\]/g, "\\$&");
    }).join("|");
    return Array.from(String(title || "").replace(new RegExp("(?:\\[|【|\\()(?:" + pattern + ")(?:\\]|】|\\))", "g"), "")).filter(function (character) {
      return !isAribExternalFlag(character);
    }).join("").replace(/^\s+|\s+$/g, "");
  }

  function programFlagClass(flag) {
    return {
      "新": "new",
      "終": "end",
      "再": "repeat",
      "字": "caption",
      "無": "free",
      "生": "live"
    }[flag] || "default";
  }

  function setProgramTitleContent(root, program) {
    if (!root) {
      return;
    }
    var flags = programFlagNames(program);
    var displayProgram = cloneProgram(program || {});
    var rawTitle = displayProgram.fullTitle || displayProgram.title || displayProgram.id || "無題";
    displayProgram.title = normalizeAribExternalFlags(stripProgramFlags(rawTitle));
    displayProgram.fullTitle = normalizeAribExternalFlags(rawTitle);
    root.innerHTML = "";
    flags.forEach(function (flag) {
      var badge = document.createElement("span");
      badge.className = "program-flag-badge program-flag-" + programFlagClass(flag);
      badge.textContent = flag;
      badge.title = "番組フラグ: " + flag;
      badge.setAttribute("aria-label", flag);
      root.appendChild(badge);
    });
    var label = document.createElement("span");
    label.className = "program-title-label";
    label.textContent = programTitle(displayProgram);
    root.appendChild(label);
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
    var isRecorded = isRecordedProgram(program);
    if (!reserve && !active) {
      return program;
    }
    var decorated = {};
    Object.keys(program).forEach(function (key) {
      decorated[key] = program[key];
    });
    if (reserve && !isRecorded) {
      decorated.isReserved = true;
      decorated.isManualReserved = Boolean(reserve.isManualReserved);
      decorated.isSkip = Boolean(reserve.isSkip);
      decorated.isConflict = Boolean(reserve.isConflict);
      decorated._reserveState = reserve;
    }
    if (active) {
      decorated.abort = Boolean(active.abort);
    }
    if (active && !active.abort) {
      decorated.isRecording = true;
      decorated.isManualReserved = Boolean(active.isManualReserved || decorated.isManualReserved);
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

  function activeRecordingPrograms() {
    return (state.recording || []).filter(function (program) {
      return program && !program.abort;
    });
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
    if (rule.createdAt) {
      var createdAt = formatTime(rule.createdAt);
      if (createdAt) {
        parts.push("追加 " + createdAt);
      }
    }
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

  function ruleValidationIssues(rule) {
    if (!state.schedule || !state.schedule.length) {
      return [];
    }
    var issues = [];
    var channels = state.schedule;
    var matchesChannel = function (value) {
      return channels.some(function (channel) {
        var type = scheduleChannelType(channel);
        var sid = channel && channel.sid !== undefined ? String(channel.sid) : "";
        var physical = channel && typeof channel.channel === "string" ? channel.channel : "";
        return value === scheduleChannelID(channel) || value === physical || (type && sid && value === type + "_" + sid);
      });
    };
    [
      { key: "channels", label: "channels", values: rule.channels },
      { key: "ignore_channels", label: "ignore_channels", values: rule.ignore_channels }
    ].forEach(function (field) {
      var invalid = (Array.isArray(field.values) ? field.values : []).filter(function (value) {
        return !matchesChannel(String(value));
      });
      if (invalid.length) {
        issues.push("⚠ " + field.label + "未登録: " + invalid.join(", "));
      }
    });
    if (rule.sid && !channels.some(function (channel) {
      return channel && channel.sid !== undefined && String(channel.sid) === String(rule.sid);
    })) {
      issues.push("⚠ sid未登録: " + rule.sid);
    }
    return issues;
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
    if (/^!\/search\/top\//.test(hash)) {
      applyLegacySearchHash(hash);
      return "search";
    }
    return (hash.split("?", 1)[0] || "dashboard");
  }

  function decodeHashValue(value) {
    try {
      return decodeURIComponent(String(value || "").replace(/\+/g, " "));
    } catch (error) {
      return String(value || "");
    }
  }

  function parseSearchHashQuery(value) {
    var result = {};
    String(value || "").replace(/^\?|\/$/g, "").split("&").forEach(function (part) {
      if (!part) {
        return;
      }
      var separator = part.indexOf("=");
      var key = separator >= 0 ? part.slice(0, separator) : part;
      var item = separator >= 0 ? part.slice(separator + 1) : "";
      result[decodeHashValue(key)] = decodeHashValue(item);
    });
    return result;
  }

  function applyLegacySearchHash(hash) {
    var match = hash.match(/^!\/search\/top\/([^/]*)\/?$/);
    if (!match) {
      return;
    }
    var params = parseSearchHashQuery(match[1]);
    var filter = defaultListFilter("search");
    filter.title = params.title || "";
    filter.description = params.desc || params.description || "";
    filter.category = params.cat || params.category || "";
    filter.type = params.type || "";
    filter.programID = params.pgid || params.programID || "";
    filter.channelID = params.chid || params.channelID || "";
    filter.startHour = params.start || params.startHour || "";
    filter.endHour = params.end || params.endHour || "";
    state.listFilters.search = filter;
    state.searchPage = Math.max(1, (parseInt(params.page, 10) || 0) + 1);
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
    return window.matchMedia && window.matchMedia("(max-width: 760px)").matches;
  }

  function setScheduleMenuOpen(open) {
    var expanded = Boolean(open);
    document.body.classList.toggle("schedule-menu-open", expanded);
    var button = byId("scheduleMenuButton");
    var drawer = byId("mainNavDrawer");
    var backdrop = byId("scheduleMenuBackdrop");
    var mobileSchedule = isMobileScheduleLayout() && state.currentView === "schedule";
    if (button) {
      button.setAttribute("aria-expanded", expanded ? "true" : "false");
    }
    if (drawer) {
      drawer.inert = mobileSchedule && !expanded;
      if (mobileSchedule) {
        drawer.setAttribute("aria-hidden", expanded ? "false" : "true");
      } else {
        drawer.removeAttribute("aria-hidden");
      }
      if (!expanded && mobileSchedule && drawer.contains(document.activeElement) && button && typeof button.focus === "function") {
        button.focus();
      }
    }
    if (backdrop) {
      backdrop.hidden = !expanded;
    }
    if (expanded && drawer && isMobileScheduleLayout() && state.currentView === "schedule") {
      window.setTimeout(function () {
        if (!document.body.classList.contains("schedule-menu-open")) {
          return;
        }
        var firstLink = drawer.querySelector("a");
        if (firstLink && typeof firstLink.focus === "function") {
          firstLink.focus();
        }
      }, 0);
    }
  }

  function closeScheduleMenu() {
    setScheduleMenuOpen(false);
  }

  function setScheduleFilterOpen(open) {
    var expanded = Boolean(open);
    document.body.classList.toggle("schedule-filter-open", expanded);
    var button = byId("scheduleFilterButton");
    var controls = byId("scheduleNavControls");
    if (button) {
      button.setAttribute("aria-expanded", expanded ? "true" : "false");
    }
    if (!expanded && controls && controls.contains(document.activeElement) && button && typeof button.focus === "function") {
      button.focus();
    }
  }

  function closeScheduleFilter() {
    setScheduleFilterOpen(false);
  }

  function loadViewData(view, force) {
    var path = "";
    var readyKey = "";
    if (view === "rules") {
      path = "rules";
      readyKey = "rulesLoaded";
    } else if (view === "settings") {
      path = "config";
      readyKey = "configLoaded";
    } else if (view === "status") {
      path = "storage";
      readyKey = "storageLoaded";
    }
    if (!path) {
      return Promise.resolve();
    }
    if (!force && state[readyKey]) {
      return Promise.resolve();
    }
    if (state.viewDataRequests[view]) {
      return state.viewDataRequests[view];
    }
    state[readyKey] = false;
    state.viewDataErrors[view] = null;
    if (view === "rules") {
      renderRules();
    } else if (view === "settings") {
      renderSettings();
    } else if (view === "status") {
      renderStatus(false);
    }
    var requestPromise = api(path).then(function (result) {
      if (path === "rules") {
        state.rules = result || [];
      } else if (path === "config") {
        state.config = result || {};
      } else if (path === "storage") {
        state.storage = result || null;
      }
      state[readyKey] = true;
      if (state.currentView === view) {
        if (view === "rules") {
          renderRules();
        } else if (view === "settings") {
          renderSettings();
        } else if (view === "status") {
          renderStatus(false);
        }
      }
    }).catch(function (error) {
      state.viewDataErrors[view] = error;
      if (state.currentView === view) {
        if (view === "rules") {
          renderRules();
        } else if (view === "settings") {
          renderSettings();
        } else if (view === "status") {
          renderStatus(false);
        }
      }
      showError(error);
    }).finally(function () {
      state.viewDataRequests[view] = null;
    });
    state.viewDataRequests[view] = requestPromise;
    return requestPromise;
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
    var previousView = state.currentView;
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
    if (state.currentView === "schedule" && previousView !== "schedule") {
      state.scheduleGuideScrollToCurrentTime = true;
    }
    if (state.hasLoaded) {
      if (state.currentView === "status") {
        renderStatus();
        loadViewData("status");
        startMetricsRefresh();
      } else if (state.currentView === "dashboard" || state.currentView === "schedule" || state.currentView === "reserves") {
        renderOperationalData();
      } else if (state.currentView === "search") {
        renderSearch();
      } else if (state.currentView === "rules") {
        renderRules();
        renderRuleFormState();
        loadViewData("rules");
      } else if (state.currentView === "settings") {
        renderSettings();
        loadViewData("settings");
      } else if (state.currentView === "logs") {
        refreshLogs();
      }
    }
    if (state.currentView !== "status") {
      stopMetricsRefresh();
    }
    if (state.currentView !== "schedule") {
      closeScheduleMenu();
      closeScheduleFilter();
    } else {
      setScheduleMenuOpen(document.body.classList.contains("schedule-menu-open"));
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
    var recordingCount = activeRecordingPrograms().length;
    var reserveCount = state.reserves.length;
    var conflicts = reserveConflictCount();
    var operationalAnnouncement = [
      statusText,
      "録画中 " + recordingCount,
      "予約 " + reserveCount,
      "競合 " + conflicts
    ].join(" / ");
    if (state.lastOperationalAnnouncement !== operationalAnnouncement) {
      state.lastOperationalAnnouncement = operationalAnnouncement;
      announce(operationalAnnouncement);
    }
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
    text(byId("scheduleRecordingSummary"), "録画中 " + recordingCount);
    text(byId("scheduleReserveSummary"), "予約 " + reserveCount);
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
      "search": "searchQuery",
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
    return Array.prototype.slice.call(view.querySelectorAll(".program-row .program-title-button"));
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
      if (event.key === "Escape" && (document.body.classList.contains("schedule-menu-open") || document.body.classList.contains("schedule-filter-open"))) {
        closeScheduleFilter();
        closeScheduleMenu();
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
      setBusy("処理中");
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

  function programSubtitlesURL(collection, program, query) {
    var url = "/api/" + collection + "/" + encodeURIComponent(program.id) + "/subtitles.vtt";
    query = query || {};
    var params = new URLSearchParams();
    ["ss", "t"].forEach(function (key) {
      if (query[key]) {
        params.set(key, query[key]);
      }
    });
    var encoded = params.toString();
    return encoded ? url + "?" + encoded : url;
  }

  function recordedSubtitlesURL(program, query) {
    return programSubtitlesURL("recorded", program, query);
  }

  function recordedXSPFURL(program) {
    var prefix = window.location.origin + "/api/recorded/" + encodeURIComponent(program.id) + "/";
    return recordedWatchURL(program, "xspf", { prefix: prefix, ext: "m2ts" });
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
    if (recordedNativeHLSSupported(video, window.navigator && window.navigator.userAgent)) {
      return recordedHLSURL(program, query);
    }
    return recordedWatchURL(program, "mp4", query);
  }

  function recordedNativeHLSSupported(video, userAgent) {
    if (!video || !video.canPlayType("application/vnd.apple.mpegurl")) {
      return false;
    }
    userAgent = userAgent || "";
    if (/iPhone|iPad|iPod/i.test(userAgent)) {
      return true;
    }
    return /Macintosh/i.test(userAgent) && /Safari/i.test(userAgent) && !/Chrome|Chromium|Edg/i.test(userAgent);
  }

  function recordingWatchURL(program, ext, query) {
    var url = "/api/recording/" + encodeURIComponent(program.id) + "/watch." + ext;
    if (!query) {
      return url;
    }
    return url + "?" + new URLSearchParams(query).toString();
  }

  function recordingSubtitlesURL(program, query) {
    return programSubtitlesURL("recording", program, query);
  }

  function channelSubtitlesURL(channelID) {
    return channelURL(channelID, "subtitles", "vtt");
  }

  function channelURL(channelID, resource, ext, query) {
    var url = "/api/channel/" + encodeURIComponent(channelID) + "/" + resource;
    if (ext) {
      url += "." + ext;
    }
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

  function setPlayerStatus(message, kind) {
    var status = byId("playerStatus");
    var messageNode = byId("playerStatusMessage");
    var retry = byId("playerRetryButton");
    if (!status || !messageNode) {
      return;
    }
    messageNode.textContent = message || "";
    status.hidden = !message;
    status.className = "player-status" + (message ? " " + (kind || "error") : "");
    if (retry) {
      retry.hidden = !message || kind === "info";
    }
  }

  function playerMediaErrorMessage(video) {
    var code = video && video.error ? video.error.code : 0;
    if (code === 3) {
      return "映像をデコードできませんでした。FFmpegまたは録画データを確認してください。";
    }
    if (code === 4) {
      return "この映像を再生できませんでした。録画が開始直後の場合は、数秒後に再試行してください。";
    }
    return "再生に失敗しました。録画状態とWUIログを確認して、再試行してください。";
  }

  function retryPlayerSource() {
    if (!playerSourceBuilder) {
      return;
    }
    var query = cloneQuery(playerBaseQuery || {});
    try {
      setPlayerSource(playerSourceBuilder(Object.keys(query).length ? query : null), query);
    } catch (error) {
      setPlayerStatus("再生URLを作成できませんでした。録画状態を確認して再試行してください");
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
    if (!url) {
      setPlayerStatus("再生URLを作成できませんでした。録画状態を確認して再試行してください");
      return;
    }
    setPlayerStatus("");
    stopHLSPlayback(playerCurrentURL);
    video.pause();
    video.removeAttribute("src");
    video.load();
    video.src = url;
    playerCurrentURL = url;
    playerBaseQuery = cloneQuery(query || {});
    playerKnownDuration = playerConfiguredDuration();
    updatePlayerSubtitleTrack(playerBaseQuery);
    updatePlayerControls();
    video.play().catch(function (error) {
      if (!error || error.name !== "NotAllowedError") {
        setPlayerStatus("再生を開始できませんでした。録画が開始直後の場合は、数秒後に再試行してください");
      }
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
    video.addEventListener("error", function () {
      setPlayerStatus(playerMediaErrorMessage(video));
      updatePlayerControls();
    });
  }

  function bindPlayerSubtitleEvents(track) {
    track = track || byId("playerSubtitleTrack");
    if (!track) {
      return;
    }
    track.addEventListener("error", function () {
      if (track.getAttribute("src")) {
        setPlayerStatus("字幕を読み込めませんでした。FFmpegにlibaribcaptionまたはlibaribb24を含むビルドが必要です。");
      }
    });
  }

  function updatePlayerSubtitleControl(enabled) {
    var select = byId("playerSubtitle");
    var track = byId("playerSubtitleTrack");
    if (select) {
      select.disabled = !enabled;
      if (!enabled) {
        select.value = "";
      }
    }
    if (track && !enabled) {
      track.track.mode = "disabled";
      track.removeAttribute("src");
    }
  }

  function updatePlayerSubtitleTrack(query) {
    var select = byId("playerSubtitle");
    var track = byId("playerSubtitleTrack");
    if (!select || !track || !playerSubtitleSourceBuilder || select.value !== "ja") {
      if (track) {
        track.track.mode = "disabled";
        track.removeAttribute("src");
      }
      return;
    }
    var url = playerSubtitleSourceBuilder(cloneQuery(query || {}));
    if (!url) {
      track.track.mode = "disabled";
      track.removeAttribute("src");
      return;
    }
    if (track.getAttribute("src") !== url) {
      track.track.mode = "disabled";
      track.removeAttribute("src");
      var replacement = track.cloneNode(false);
      track.parentNode.replaceChild(replacement, track);
      track = replacement;
      bindPlayerSubtitleEvents(track);
      track.setAttribute("src", url);
    }
    track.track.mode = "showing";
  }

  function changePlayerSubtitle() {
    updatePlayerSubtitleTrack(playerBaseQuery || {});
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
    playerSubtitleSourceBuilder = typeof options.subtitleBuilder === "function" ? options.subtitleBuilder : null;
    playerSeekable = Boolean(options.seekable);
    playerTimelineStart = playerSeekable ? finitePositiveSeconds(options.query && options.query.ss) : 0;
    playerTimelineDuration = playerSeekable ? finitePositiveSeconds(options.query && options.query.t) : 0;
    playerFallbackDuration = finitePositiveSeconds(options.duration);
    if (playerSeekable && playerTimelineDuration <= 0) {
      playerTimelineDuration = playerFallbackDuration;
    }
    playerKnownDuration = playerFallbackDuration;
    updatePlayerSubtitleControl(Boolean(playerSubtitleSourceBuilder));
    updatePlayerQualityControl(options.query || null, Boolean(playerSourceBuilder));
    updatePlayerAudioControl(options.query || null, Boolean(playerSourceBuilder));
    dialog.showModal();
    setPlayerSource(url, options.query || null);
    if (options.status) {
      setPlayerStatus(options.status, "info");
    }
    video.focus();
  }

  function openAdjustablePlayer(meta, buildURL, query, seekable, duration, status, subtitleBuilder) {
    openPlayerDialog(meta, buildURL(query), {
      query: query,
      seekable: seekable,
      duration: duration,
      sourceBuilder: buildURL,
      status: status,
      subtitleBuilder: subtitleBuilder
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
    stopHLSPlayback(playerCurrentURL);
    playerCurrentURL = "";
    video.pause();
    video.removeAttribute("src");
    video.load();
    playerSourceBuilder = null;
    playerSubtitleSourceBuilder = null;
    playerBaseQuery = null;
    playerSeekable = false;
    playerSeeking = false;
    playerTimelineStart = 0;
    playerTimelineDuration = 0;
    playerFallbackDuration = 0;
    playerKnownDuration = 0;
    updatePlayerSubtitleControl(false);
    updatePlayerQualityControl(null, false);
    updatePlayerAudioControl(null, false);
    updatePlayerControls();
  }

  function stopHLSPlayback(url) {
    if (!url || !/\/api\/recorded\/[^/]+\/hls\/index\.m3u8(?:\?|$)/.test(url)) {
      return;
    }
    fetch(url, { method: "DELETE", keepalive: true }).catch(function () {});
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
            setBusy("処理中");
            request("program/" + encodeURIComponent(program.id), "PUT").then(refresh).then(function () {
              if (onAir && !operatorAlive()) {
                showError(new Error("オペレータが停止中です。録画を開始するには strata-pvr run operator を起動してください"));
              }
            }).catch(showError);
          }));
        }
      } else if (name === "unreserve" && program.isManualReserved && !program.isRecording) {
        row.appendChild(actionButton("予約削除", "手動予約を削除", function () {
          runAction("reserves/" + encodeURIComponent(program.id), "DELETE", "この手動予約を削除しますか？", actionConfirmOptions("DELETE", "この手動予約を削除しますか？", program, "予約削除の確認"));
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
          runAction("recording/" + encodeURIComponent(program.id), "DELETE", "この録画を停止しますか？", actionConfirmOptions("DELETE", "この録画を停止しますか？", program, "録画停止の確認"));
        }));
      } else if (name === "watch-recording-mp4" && program.isRecording) {
        row.appendChild(actionButton("視聴", "録画中の番組を視聴", function () {
          openAdjustablePlayer(program.title || program.id || "録画中", function (query) {
            return recordingWatchURL(program, "mp4", query);
          }, null, false, 0, "録画中の保存データを再生しています。Mirakurunのチューナーは使用しません。録画の進行で一時停止した場合は、数秒後に再試行してください。", function (query) {
            return recordingSubtitlesURL(program, query);
          });
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
          }, initialQuery, true, programDurationSeconds(program), "", function (query) {
            return recordedSubtitlesURL(program, query);
          });
        }));
      } else if (name === "download") {
        row.appendChild(actionButton("ダウンロード", "録画ファイルを実体ファイル名で保存", function () {
          openURL("/api/recorded/" + encodeURIComponent(program.id) + "/file.m2ts");
        }));
      } else if (name === "xspf") {
        row.appendChild(actionButton("XSPF", "録画済み番組のプレイリストを開く", function () {
          openURL(recordedXSPFURL(program));
        }));
      } else if (name === "delete-recorded") {
        row.appendChild(actionButton("削除", "録画済み項目とファイルを削除", function () {
          runAction("recorded/" + encodeURIComponent(program.id), "DELETE", "この録画済み項目とファイルを削除しますか？", actionConfirmOptions("DELETE", "この録画済み項目とファイルを削除しますか？", program, "録画済み削除の確認"));
        }));
      } else if (name === "watch-channel-mp4") {
        var channelID = programChannelID(program);
        if (channelID) {
          row.appendChild(actionButton("視聴", "この番組のチャンネルを視聴", function () {
            openAdjustablePlayer(program.title || channelID || "チャンネル", function (query) {
              return channelURL(channelID, "watch", "mp4", query);
            }, null, false, 0, "", function () {
              return channelSubtitlesURL(channelID);
            });
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
        }, initialQuery, true, programDurationSeconds(program), "", function (query) {
          return recordedSubtitlesURL(program, query);
        });
      }, className);
    }
    if (name === "download") {
      return actionButton("ダウンロード", "録画ファイルを実体ファイル名で保存", function () {
        openURL("/api/recorded/" + encodeURIComponent(program.id) + "/file.m2ts");
      }, ((className || "") + " recorded-action-download").trim());
    }
    if (name === "xspf") {
      return actionButton("XSPF", "録画済み番組のプレイリストを開く", function () {
        openURL(recordedXSPFURL(program));
      }, ((className || "") + " recorded-action-xspf").trim());
    }
    if (name === "delete-recorded") {
      return actionButton("削除", "録画済み項目とファイルを削除", function () {
        runAction("recorded/" + encodeURIComponent(program.id), "DELETE", "この録画済み項目とファイルを削除しますか？", actionConfirmOptions("DELETE", "この録画済み項目とファイルを削除しますか？", program, "録画済み削除の確認"));
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
    button.title = "番組をこのチャンネルで検索";
    button.addEventListener("click", function () {
      state.listFilters.search.channelID = channelID || "";
      state.searchPage = 1;
      saveListFilters();
      window.location.hash = "search";
      renderSearch();
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
    image.src = programPreviewURL(program, resource, "160x90") + (resource === "recording" ? "&_=" + Date.now() : "");
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

    fetchWithTimeout(previewURL, { credentials: "same-origin" }).then(function (response) {
      if (root.getAttribute("data-preview-id") !== program.id) {
        return null;
      }
      if (response.status === 410) {
        renderProgramDialogPreviewAlert(root, "録画ファイルが見つかりません。移動または削除されている可能性があります。");
        return null;
      }
      if (!response.ok) {
        renderProgramDialogPreviewAlert(root, "プレビューを取得できませんでした。時間をおいて再試行してください。");
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
        renderProgramDialogPreviewAlert(root, "プレビューを取得できませんでした。時間をおいて再試行してください。");
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
    item.addEventListener("dblclick", function (event) {
      if (!isEditableTarget(event.target)) {
        openProgramDialog(program);
      }
    });

    var title = document.createElement("button");
    title.type = "button";
    title.className = "program-title-button";
    setProgramTitleContent(title, program);
    title.title = programFullTitle(program) + "\n番組詳細を開く";
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
      setProgramTitleContent(nowPlaying, current);
      nowPlaying.title = programFullTitle(current) + "\n番組詳細を開く";
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
        }, null, false, 0, "", function () {
          return channelSubtitlesURL(group.id);
        });
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

  function scheduleCurrentTimeScrollTop(scroll, firstStart, lastEnd, minuteHeight, now) {
    if (!scroll || now < firstStart || now > lastEnd) {
      return null;
    }
    var lineTop = Math.round(((now - firstStart) / 60000) * minuteHeight);
    var headerHeight = 76;
    var visibleTimelineHeight = Math.max(0, scroll.clientHeight - headerHeight);
    var leadingContext = Math.round(visibleTimelineHeight * 0.35);
    return Math.max(0, lineTop - leadingContext);
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

  function broadcastingProgramGroups() {
    if (!Array.isArray(state.broadcasting)) {
      return visibleChannelProgramGroups();
    }
    var groups = channelProgramGroups();
    var byID = {};
    groups.forEach(function (group) {
      byID[group.id] = group;
      group.programs = [];
    });
    (state.broadcasting || []).forEach(function (program, index) {
      if (!program || !program.start) {
        return;
      }
      var id = programChannelID(program);
      if (!id) {
        return;
      }
      var group = byID[id];
      if (!group) {
        group = {
          id: id,
          name: channelName(program) || id,
          order: groups.length + index,
          type: programChannelType(program),
          logo: false,
          programs: []
        };
        byID[id] = group;
        groups.push(group);
      }
      group.programs.push(cloneProgram(program));
    });
    return groups.filter(function (group) {
      return group.programs.length > 0;
    });
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

  var searchPageSize = 50;

  function searchResults() {
    var filter = state.listFilters.search || defaultListFilter("search");
    var query = normalizeSearchText(filter.query);
    var title = normalizeSearchText(filter.title);
    var description = normalizeSearchText(filter.description);
    var now = Date.now();
    var results = [];
    (state.schedule || []).forEach(function (channel) {
      (channel.programs || []).forEach(function (source) {
        if (!source || (source.end || 0) < now) {
          return;
        }
        var program = cloneProgram(source);
        if (!program.channel) {
          program.channel = channel;
        } else if (typeof program.channel === "object") {
          program.channel = cloneProgram(program.channel);
          if (!program.channel.id && channel.id) {
            program.channel.id = channel.id;
          }
          if (!program.channel.name && channel.name) {
            program.channel.name = channel.name;
          }
          if (!program.channel.type && channel.type) {
            program.channel.type = channel.type;
          }
        }
        var haystack = normalizeSearchText([
          program.id, programTitle(program), program.title, program.fullTitle,
          program.detail, program.description, programCategory(program), channelName(program)
        ].join(" "));
        if (query && haystack.indexOf(query) < 0) {
          return;
        }
        if (title && normalizeSearchText(programTitle(program)).indexOf(title) < 0) {
          return;
        }
        if (description && normalizeSearchText(program.detail || program.description || "").indexOf(description) < 0) {
          return;
        }
        if (filter.category && programCategory(program) !== filter.category) {
          return;
        }
        if (filter.type && programChannelType(program) !== filter.type) {
          return;
        }
        if (filter.programID && String(program.id || "") !== String(filter.programID)) {
          return;
        }
        if (filter.channelID && programChannelID(program) !== String(filter.channelID)) {
          return;
        }
        if (!matchesSearchHours(program, filter.startHour, filter.endHour)) {
          return;
        }
        results.push(program);
      });
    });
    return results.sort(function (a, b) {
      return (a.start || 0) - (b.start || 0);
    });
  }

  function matchesSearchHours(program, startHour, endHour) {
    if (!startHour && !endHour) {
      return true;
    }
    var startRule = startHour === "" ? 0 : Number(startHour);
    var endRule = endHour === "" ? 24 : Number(endHour);
    if (!isFinite(startRule) || !isFinite(endRule)) {
      return true;
    }
    var start = new Date(program.start || 0).getHours();
    var end = new Date(programEnd(program)).getHours();
    if (start > end) {
      end += 24;
    }
    if (startRule > endRule) {
      return !((startRule > start) && (endRule < end));
    }
    return !(startRule > start || endRule < end);
  }

  function renderSearchFilterOptions(results) {
    var categories = {};
    (state.schedule || []).forEach(function (channel) {
      (channel.programs || []).forEach(function (program) {
        if (program && program.category) {
          categories[String(program.category)] = true;
        }
      });
    });
    var category = byId("searchCategory");
    if (category) {
      var currentCategory = (state.listFilters.search || {}).category || "";
      category.innerHTML = "<option value=\"\">全ジャンル</option>";
      Object.keys(categories).sort().forEach(function (value) {
        var option = document.createElement("option");
        option.value = value;
        option.textContent = value;
        category.appendChild(option);
      });
      category.value = currentCategory;
      if (category.value !== currentCategory) {
        state.listFilters.search.category = "";
      }
    }
    var type = byId("searchType");
    if (type) {
      type.value = (state.listFilters.search || {}).type || "";
    }
    renderChannelSelectOptions(byId("searchChannelID"), [
      (state.listFilters.search || {}).channelID || ""
    ], "全チャンネル", false);
    var count = results.length;
    var pageCount = Math.max(1, Math.ceil(count / searchPageSize));
    state.searchPage = Math.max(1, Math.min(pageCount, Number(state.searchPage) || 1));
    var start = count ? (state.searchPage - 1) * searchPageSize + 1 : 0;
    var end = count ? Math.min(count, state.searchPage * searchPageSize) : 0;
    text(byId("searchListFilterSummary"), count ? start + "-" + end + " / " + count + "件" : "0件");
    document.querySelectorAll("[data-search-page-summary]").forEach(function (summary) {
      text(summary, count ? state.searchPage + " / " + pageCount + "ページ" : "0ページ");
    });
    [
      { action: "first", disabled: state.searchPage <= 1 || !count },
      { action: "prev", disabled: state.searchPage <= 1 || !count },
      { action: "next", disabled: state.searchPage >= pageCount || !count },
      { action: "last", disabled: state.searchPage >= pageCount || !count }
    ].forEach(function (item) {
      document.querySelectorAll("[data-search-page-control='" + item.action + "']").forEach(function (button) {
        button.disabled = item.disabled;
      });
    });
  }

  function renderSearch() {
    var results = searchResults();
    renderSearchFilterOptions(results);
    var start = (state.searchPage - 1) * searchPageSize;
    renderList("searchList", results.slice(start, start + searchPageSize), "条件に一致する番組はありません", searchPageSize, ["reserve", "skip", "unskip", "create-rule-from-program"]);
    var filter = state.listFilters.search || defaultListFilter("search");
    [
      ["searchQuery", filter.query],
      ["searchTitle", filter.title],
      ["searchDescription", filter.description],
      ["searchProgramID", filter.programID],
      ["searchChannelID", filter.channelID],
      ["searchStartHour", filter.startHour],
      ["searchEndHour", filter.endHour]
    ].forEach(function (item) {
      var control = byId(item[0]);
      if (control && control.value !== item[1]) {
        control.value = item[1] || "";
      }
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
    document.querySelectorAll("[data-recorded-page-size]").forEach(function (pageSizeSelect) {
      pageSizeSelect.value = String(pageSize);
    });
    document.querySelectorAll("[data-recorded-page-summary]").forEach(function (summary) {
      text(summary, total > 0 ? start + "-" + end + " / " + total + "件" : "0件");
    });
    [
      { action: "first", disabled: page <= 1 || total === 0 },
      { action: "prev", disabled: page <= 1 || total === 0 },
      { action: "next", disabled: page >= maxPage || total === 0 },
      { action: "last", disabled: page >= maxPage || total === 0 }
    ].forEach(function (item) {
      document.querySelectorAll("[data-recorded-page-control='" + item.action + "']").forEach(function (button) {
        button.disabled = item.disabled;
      });
    });
  }

  function renderOnAirList() {
    var root = byId("onAirList");
    if (!root) {
      return;
    }
    var groups = broadcastingProgramGroups().filter(function (group) {
      return !channelGroupHidden(group);
    }).sort(function (a, b) {
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
    var timelineNow = Date.now();
    if ((!state.scheduleDay || dateKey(timelineNow) === state.scheduleDay) && timelineNow < firstStart) {
      firstStart = timelineNow;
    }
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
    var virtualLanes = [];
    var virtualRenderFrame = 0;
    var virtualWindowStart = -1;
    var virtualWindowEnd = -1;

    function renderVisibleScheduleLanes() {
      var headerHeight = 76;
      var overscan = Math.max(720, Math.round(scroll.clientHeight * 0.75));
      var visibleTop = Math.max(0, scroll.scrollTop - headerHeight);
      var visibleBottom = visibleTop + Math.max(scroll.clientHeight, 360);
      var nextWindowStart = Math.max(0, visibleTop - overscan);
      var nextWindowEnd = visibleBottom + overscan;
      if (nextWindowStart === virtualWindowStart && nextWindowEnd === virtualWindowEnd) {
        return;
      }
      virtualWindowStart = nextWindowStart;
      virtualWindowEnd = nextWindowEnd;
      virtualLanes.forEach(function (record) {
        var lane = record.lane;
        var programs = record.group.programs;
        var low = 0;
        var high = programs.length;
        while (low < high) {
          var middle = Math.floor((low + high) / 2);
          var middleTop = Math.max(0, Math.round(((programs[middle].start - firstStart) / 60000) * minuteHeight));
          if (middleTop < nextWindowStart) {
            low = middle + 1;
          } else {
            high = middle;
          }
        }
        lane.innerHTML = "";
        for (var index = Math.max(0, low - 1); index < programs.length; index += 1) {
          var program = programs[index];
          var top = Math.max(0, Math.round(((program.start - firstStart) / 60000) * minuteHeight));
          var bottom = top + Math.max(1, Math.round(((programEnd(program) - program.start) / 60000) * minuteHeight));
          if (top > nextWindowEnd) {
            break;
          }
          if (bottom >= nextWindowStart) {
            lane.appendChild(renderScheduleCard(program, firstStart, minuteHeight));
          }
        }
        var now = Date.now();
        if (now >= firstStart && now <= lastEnd && now >= firstStart + (nextWindowStart / minuteHeight) * 60000 && now <= firstStart + (nextWindowEnd / minuteHeight) * 60000) {
          var line = document.createElement("div");
          line.className = "schedule-now-line";
          line.style.top = Math.round(((now - firstStart) / 60000) * minuteHeight) + "px";
          lane.appendChild(line);
        }
      });
    }

    function queueVisibleScheduleLanes() {
      if (virtualRenderFrame) {
        return;
      }
      var render = function () {
        virtualRenderFrame = 0;
        renderVisibleScheduleLanes();
      };
      virtualRenderFrame = window.requestAnimationFrame ? window.requestAnimationFrame(render) : window.setTimeout(render, 0);
    }

    scroll.addEventListener("scroll", function () {
      state.scheduleGuideScroll = {
        left: scroll.scrollLeft,
        top: scroll.scrollTop
      };
      queueVisibleScheduleLanes();
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
        logo.src = channelURL(group.id, "logo", "");
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
      virtualLanes.push({ lane: lane, group: group });
      grid.appendChild(lane);
    });
    scroll.appendChild(grid);
    root.appendChild(scroll);
    window.setTimeout(function () {
      var scrollTop = state.scheduleGuideScroll.top || 0;
      if (state.scheduleGuideScrollToCurrentTime) {
        var currentTimeScrollTop = scheduleCurrentTimeScrollTop(scroll, firstStart, lastEnd, minuteHeight, Date.now());
        if (currentTimeScrollTop !== null) {
          scrollTop = currentTimeScrollTop;
          state.scheduleGuideScroll.top = scrollTop;
          state.scheduleGuideScrollToCurrentTime = false;
        }
      }
      state.scheduleGuideScroll.top = scrollTop;
      scroll.scrollLeft = state.scheduleGuideScroll.left || 0;
      scroll.scrollTop = state.scheduleGuideScroll.top || 0;
      renderVisibleScheduleLanes();
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
      }, null, false, 0, "", function () {
        return channelSubtitlesURL(channelID);
      });
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
    card.title = [programFullTitle(program), program.detail || program.description || ""].filter(Boolean).join("\n");
    card.tabIndex = 0;
    card.setAttribute("role", "button");
    card.setAttribute("aria-label", [programFullTitle(program), programFlagNames(program).join(" "), stateLabel, "の詳細を開く"].filter(Boolean).join(" "));

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
    setProgramTitleContent(title, program);

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
    setProgramTitleContent(title, program);
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
    renderRuleFormOptions();
    var root = byId("ruleList");
    if (!root) {
      return;
    }
    if (!state.rulesLoaded) {
      root.className = "list empty";
      if (state.viewDataErrors.rules) {
        setRecoverableListPlaceholder("ruleList", "ルールを読み込めませんでした");
      } else {
        root.textContent = "ルールを読み込み中";
      }
      updateListFilterSummary("ruleListFilterSummary", 0, 0);
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
      var validationIssues = ruleValidationIssues(rule);
      meta.textContent = [ruleSummary(rule)].concat(validationIssues).join(" / ");
      if (validationIssues.length) {
        meta.className = "rule-validation-error";
        meta.title = "現在の番組表に存在しないチャンネルまたはSIDがあります";
      }

      var row = document.createElement("div");
      row.className = "row-actions";
      row.appendChild(actionButton(rule.isDisabled ? "有効化" : "無効化", "ルールの有効状態を切り替え", function () {
        runAction("rules/" + index + "/" + (rule.isDisabled ? "enable" : "disable"), "PUT");
      }));
      row.appendChild(actionButton("フォーム編集", "このルールをフォームに読み込む", function () {
        fillRuleFormFromRule(rule, index);
      }));
      row.appendChild(actionButton("削除", "このルールを削除", function () {
        runAction("rules/" + index, "DELETE", "このルールを削除しますか？", {
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
    if (strataConfigFormDirty) {
      return;
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
      strataConfigFormDirty = true;
      row.remove();
    });
    root.appendChild(row);
  }

  function updateStrataAuthUsersState() {
    var enabled = byId("strataAuthEnabled").checked;
    byId("strataAuthUsers").hidden = !enabled;
    byId("addStrataUserButton").hidden = !enabled;
  }

  function clearConfigFieldErrors() {
    var form = byId("strataConfigForm");
    if (!form) {
      return;
    }
    form.querySelectorAll("[aria-invalid='true']").forEach(function (control) {
      control.removeAttribute("aria-invalid");
      control.removeAttribute("aria-describedby");
    });
    form.querySelectorAll(".field-error").forEach(function (error) {
      error.remove();
    });
  }

  function clearConfigFieldError(id) {
    var control = byId(id);
    var error = byId(id + "Error");
    if (control) {
      control.removeAttribute("aria-invalid");
      control.removeAttribute("aria-describedby");
    }
    if (error) {
      error.remove();
    }
  }

  function setConfigFieldError(id, message) {
    var control = byId(id);
    if (!control) {
      return;
    }
    var errorID = id + "Error";
    var error = byId(errorID);
    if (!error) {
      error = document.createElement("span");
      error.id = errorID;
      error.className = "field-error";
      control.parentNode.appendChild(error);
    }
    error.textContent = message;
    control.setAttribute("aria-invalid", "true");
    control.setAttribute("aria-describedby", errorID);
  }

  function requiredString(id, label) {
    var value = controlString(id);
    if (!value) {
      var message = label + "を入力してください";
      setConfigFieldError(id, message);
      showError(new Error(message));
      return null;
    }
    clearConfigFieldError(id);
    return value;
  }

  function requiredInteger(id, label, minimum, maximum) {
    var value = Number(controlString(id));
    if (!Number.isInteger(value) || value < minimum || value > maximum) {
      var message = label + "は" + minimum + "から" + maximum + "の整数にしてください";
      setConfigFieldError(id, message);
      showError(new Error(message));
      return null;
    }
    clearConfigFieldError(id);
    return value;
  }

  function strataServiceList(id, label) {
    var values = splitList(controlString(id)).map(Number);
    if (values.some(function (value) { return !Number.isInteger(value) || value <= 0; })) {
      var message = label + "はカンマ区切りの正の整数にしてください";
      setConfigFieldError(id, message);
      showError(new Error(message));
      return null;
    }
    clearConfigFieldError(id);
    return values;
  }

  function readStrataConfigForm() {
    clearConfigFieldErrors();
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
        strataConfigFormDirty = false;
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
    if (!state.configLoaded) {
      root.innerHTML = "";
      var loadingKey = document.createElement("dt");
      var loadingValue = document.createElement("dd");
      loadingKey.textContent = "設定";
      loadingValue.textContent = state.viewDataErrors.settings ? "設定を読み込めませんでした" : "読み込み中";
      root.appendChild(loadingKey);
      root.appendChild(loadingValue);
      if (state.viewDataErrors.settings) {
        var retryValue = document.createElement("dd");
        retryValue.appendChild(actionButton("再試行", "設定を再読み込み", function () {
          loadViewData("settings", true);
        }));
        root.appendChild(retryValue);
      }
      var strataPanel = byId("strataConfigPanel");
      if (strataPanel) {
        strataPanel.hidden = true;
      }
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

  function renderStatus(includeMetrics) {
    var root = byId("resourceList");
    if (!root) {
      return;
    }
    var status = state.status || {};
    var application = status.application || {};
    var system = status.system || {};
    var memory = system.memory || {};
    var operator = status.operator || {};
    var rows = [
      ["バージョン", application.version || "不明"],
      ["コミット", application.commit || "不明"],
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
    if (includeMetrics !== false) {
      renderMetrics();
    }
  }

  function renderStorage() {
    var root = byId("storageList");
    if (!root) {
      return;
    }
    if (state.viewDataErrors.status) {
      root.innerHTML = "";
      root.className = "list empty error recoverable-empty";
      var copy = document.createElement("p");
      copy.textContent = "ストレージ情報を取得できませんでした";
      var actions = document.createElement("div");
      actions.className = "empty-actions";
      actions.appendChild(actionButton("再試行", "ストレージ情報を再読み込み", function () {
        loadViewData("status", true);
      }));
      root.appendChild(copy);
      root.appendChild(actions);
      return;
    }
    if (!state.storageLoaded) {
      setListPlaceholder("storageList", "読み込み中", "settings-list empty");
      return;
    }
    var storage = state.storage || {};
    root.className = "settings-list";
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
    text(byId("storageChartSummary"), [
      storagePath ? "対象 " + storagePath : "録画保存先",
      "使用中 " + formatPercent(usedPercent),
      "録画 " + formatBytes(recorded),
      "空き " + formatPercent(freePercent)
    ].join(" / "));
  }

  function renderResourceLineChart() {
    var root = byId("resourceLineChart");
    if (!root) {
      return;
    }
    var samples = state.metrics && Array.isArray(state.metrics.samples) ? state.metrics.samples : [];
    if (samples.length < 2) {
      root.innerHTML = '<div class="chart-empty">履歴を収集中</div>';
      text(byId("resourceChartSummary"), "直近6時間 / 履歴を収集中");
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
      text(byId("resourceChartSummary"), "直近6時間 / 履歴を収集中");
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
    var resourceSummary = [
      "直近6時間",
      "CPU " + formatPercent(latest.cpu),
      "メモリ " + formatPercent(latest.memory),
      points.length + "点"
    ].join(" / ");
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
    text(byId("resourceChartSummary"), resourceSummary);
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

  function listFormValues(control) {
    if (!control) {
      return [];
    }
    if (control.multiple) {
      return Array.prototype.map.call(control.selectedOptions, function (option) {
        return option.value;
      }).filter(Boolean);
    }
    return splitList(control.value);
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
    if (control.multiple) {
      var values = Array.isArray(value) ? value.map(String) : splitList(value);
      Array.prototype.forEach.call(control.options, function (option) {
        option.selected = values.indexOf(option.value) >= 0;
      });
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

  function channelOptionEntries(extraValues) {
    var entries = [];
    var seen = {};
    (state.schedule || []).forEach(function (channel) {
      var id = scheduleChannelID(channel);
      if (!id || seen[id]) {
        return;
      }
      seen[id] = true;
      entries.push({ id: id, name: scheduleChannelName(channel) || id });
    });
    (extraValues || []).forEach(function (id) {
      id = String(id || "");
      if (id && !seen[id]) {
        seen[id] = true;
        entries.push({ id: id, name: id + "（現在の番組表にありません）" });
      }
    });
    return entries;
  }

  function renderChannelSelectOptions(select, extraValues, emptyLabel, multiple) {
    if (!select) {
      return;
    }
    var currentValues = multiple ? listFormValues(select) : [];
    var values = (extraValues || []).concat(currentValues);
    select.innerHTML = "";
    if (!multiple) {
      var empty = document.createElement("option");
      empty.value = "";
      empty.textContent = emptyLabel || "選択";
      select.appendChild(empty);
    }
    channelOptionEntries(values).forEach(function (entry) {
      var option = document.createElement("option");
      option.value = entry.id;
      option.textContent = entry.name;
      option.title = entry.id;
      option.selected = values.indexOf(entry.id) >= 0;
      select.appendChild(option);
    });
  }

  function categoryOptionEntries(extraValues) {
    var entries = [];
    var seen = {};
    (state.schedule || []).forEach(function (channel) {
      (channel.programs || []).forEach(function (program) {
        var category = program && program.category ? String(program.category) : "";
        if (category && !seen[category]) {
          seen[category] = true;
          entries.push(category);
        }
      });
    });
    (extraValues || []).forEach(function (category) {
      category = String(category || "");
      if (category && !seen[category]) {
        seen[category] = true;
        entries.push(category);
      }
    });
    return entries.sort(function (a, b) { return a.localeCompare(b); });
  }

  function renderRuleFormOptions(extraCategories, extraChannels, extraIgnoreChannels) {
    var categories = byId("ruleCategories");
    var categoryValues = (extraCategories || []).concat(listFormValues(categories));
    if (categories) {
      categories.innerHTML = "";
      categoryOptionEntries(categoryValues).forEach(function (category) {
        var option = document.createElement("option");
        option.value = category;
        option.textContent = category;
        option.selected = categoryValues.indexOf(category) >= 0;
        categories.appendChild(option);
      });
    }
    ["ruleChannels", "ruleIgnoreChannels"].forEach(function (id) {
      var select = byId(id);
      var extraValues = id === "ruleChannels" ? extraChannels : extraIgnoreChannels;
      renderChannelSelectOptions(select, (extraValues || []).concat(listFormValues(select)), "", true);
    });
  }

  function clearRuleForm() {
    [
      "ruleTitle",
      "ruleIgnoreTitle",
      "ruleDescription",
      "ruleIgnoreDescription",
      "ruleSid",
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
      recorded_format: true,
      createdAt: true
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
    var categories = rule.categories && rule.categories.length ? rule.categories : (rule.category ? [rule.category] : []);
    renderRuleFormOptions(categories, rule.channels, rule.ignore_channels);
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
    setListFormValue("ruleCategories", categories);
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
    setFormValue("ruleCategories", "");
    renderRuleFormOptions(program.category ? [program.category] : [], channelID && !/^\d+$/.test(channelID) ? [channelID] : [], []);
    setFormValue("ruleCategories", program.category ? [program.category] : []);
    setFormValue("ruleChannels", channelID && !/^\d+$/.test(channelID) ? [channelID] : []);
    setFormValue("ruleIgnoreChannels", []);
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
    var categoryValues = listFormValues(categories);
    if (categoryValues.length) {
      rule.categories = categoryValues;
      delete rule.category;
    }
    var channelValues = listFormValues(channels);
    if (channelValues.length) {
      rule.channels = channelValues;
    }
    var ignoreChannelValues = listFormValues(ignoreChannels);
    if (ignoreChannelValues.length) {
      rule.ignore_channels = ignoreChannelValues;
    }
    var flagValues = listFormValues(flags);
    if (flagValues.length) {
      rule.reserve_flags = flagValues;
    }
    var ignoreFlagValues = listFormValues(ignoreFlags);
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
    if (!Object.keys(rule).length || (!rule.reserve_titles && !rule.ignore_titles && !rule.reserve_descriptions && !rule.ignore_descriptions && !rule.types && !rule.sid && !rule.categories && !rule.channels && !rule.ignore_channels && !rule.reserve_flags && !rule.ignore_flags && !rule.duration && !rule.hour)) {
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
      sendJSON("rules/" + state.editingRuleFormIndex, "PUT", rule).then(function () {
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

    renderList("recordingList", activeRecordingPrograms(), "録画中の番組はありません", 8, ["watch-recording-mp4", "stop"], { preview: true, previewResource: "recording" });
    renderList("reserveList", state.reserves, "予約はありません", 8, ["skip", "unskip", "unreserve"], { hideReservedBadge: true, compactActions: true });
    renderList("reserveListPage", filteredReserves, "条件に一致する予約はありません", 100, ["skip", "unskip", "unreserve"], { hideReservedBadge: true, compactActions: true });
    renderList("recordedList", recordedNewestFirst, "録画済み番組はありません", 8, ["watch-mp4", "download", "xspf", "delete-recorded"], { preview: true, previewResource: "recorded" });
    renderList("recordedListPage", pagedRecorded, "条件に一致する録画済み番組はありません", state.recordedPageSize, ["watch-mp4", "download", "xspf", "delete-recorded"], { preview: true, previewResource: "recorded" });
    renderOnAirList();
    renderSchedule();
    renderSearch();
    renderRules();
    renderSettings();
    renderStatus(state.currentView === "status");
    renderRuleFormState();
  }

  function renderOperationalData() {
    state.programStateIndex = {
      reserves: programByID(state.reserves),
      recording: programByID(state.recording)
    };
    updateOperationalStatus();

    if (state.currentView === "dashboard") {
      renderList("recordingList", activeRecordingPrograms(), "録画中の番組はありません", 8, ["watch-recording-mp4", "stop"], { preview: true, previewResource: "recording" });
      renderList("reserveList", state.reserves, "予約はありません", 8, ["skip", "unskip", "unreserve"], { hideReservedBadge: true, compactActions: true });
      renderOnAirList();
      return;
    }
    if (state.currentView === "schedule") {
      renderSchedule();
      return;
    }
    if (state.currentView === "search") {
      renderSearch();
      return;
    }
    if (state.currentView === "reserves") {
      var filteredReserves = sortedPrograms(filteredPrograms(state.reserves, "reserves"), "reserves");
      updateListCategoryOptions("reserveListCategory", state.reserves, "reserves");
      updateListFilterSummary("reserveListFilterSummary", filteredReserves.length, state.reserves.length);
      renderList("reserveListPage", filteredReserves, "条件に一致する予約はありません", 100, ["skip", "unskip", "unreserve"], { hideReservedBadge: true, compactActions: true });
      return;
    }
    if (state.currentView === "status") {
      renderStatus(false);
    }
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
    var mainContent = byId("mainContent");
    if (mainContent) {
      mainContent.setAttribute("aria-busy", loading ? "true" : "false");
    }
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
    if (state.isLoading) {
      refreshQueued = true;
      return Promise.resolve();
    }
    var version = ++refreshVersion;
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
      api("schedule/broadcasting").catch(function () {
        return null;
      })
    ]).then(function (result) {
      if (version !== refreshVersion) {
        return;
      }
      state.status = result[0] || {};
      state.reserves = result[1] || [];
      state.recording = result[2] || [];
      state.recorded = result[3] || [];
      state.schedule = result[4] || [];
      state.broadcasting = Array.isArray(result[5]) ? result[5] : null;
      state.hasLoaded = true;
      state.lastError = null;
      render();
      if (state.currentView === "status") {
        loadViewData("status", true);
        startMetricsRefresh();
      } else if (state.currentView === "rules") {
        loadViewData("rules", true);
      } else if (state.currentView === "settings") {
        loadViewData("settings", true);
      }
    }).catch(function (error) {
      renderInitialLoadError(error);
      showError(error);
    }).finally(function () {
      state.isLoading = false;
      setRefreshLoading(false);
      if (state.currentView === "status") {
        refreshMetrics();
      }
      if (refreshQueued) {
        refreshQueued = false;
        refresh();
      }
    });
  }

  function refreshOperationalData() {
    if (document.visibilityState === "hidden" || state.isLoading || operationalRefreshInFlight) {
      return;
    }
    var version = refreshVersion;
    operationalRefreshInFlight = true;
    Promise.all([
      api("status"),
      api("reserves"),
      api("recording"),
      api("schedule/broadcasting").catch(function () {
        return state.broadcasting;
      }),
      api("storage").then(function (storage) {
        return { value: storage, error: null };
      }, function (error) {
        return { value: state.storage, error: error };
      })
    ]).then(function (result) {
      if (version !== refreshVersion) {
        return;
      }
      state.status = result[0] || {};
      state.reserves = result[1] || [];
      state.recording = result[2] || [];
      state.broadcasting = Array.isArray(result[3]) ? result[3] : state.broadcasting;
      var storageResult = result[4] || {};
      state.storage = storageResult.value || null;
      state.storageLoaded = !storageResult.error;
      state.viewDataErrors.status = storageResult.error || null;
      state.lastError = null;
      if (document.visibilityState !== "hidden") {
        renderOperationalData();
      }
    }).catch(showError).finally(function () {
      operationalRefreshInFlight = false;
    });
  }

  function refreshMetrics() {
    if (document.visibilityState === "hidden" || state.currentView !== "status" || state.isLoading || metricsRefreshInFlight) {
      return;
    }
    var version = refreshVersion;
    metricsRefreshInFlight = true;
    api("metrics").then(function (result) {
      if (version !== refreshVersion) {
        return;
      }
      state.metrics = result || null;
      if (state.currentView === "status") {
        renderMetrics();
      }
      updateOperationalStatus();
    }).catch(function () {
      if (document.visibilityState !== "hidden" && state.currentView === "status") {
        renderMetrics();
      }
    }).finally(function () {
      metricsRefreshInFlight = false;
    });
  }

  function startMetricsRefresh(refreshNow) {
    if (metricsRefreshTimer || state.currentView !== "status") {
      return;
    }
    if (refreshNow !== false) {
      refreshMetrics();
    }
    metricsRefreshTimer = window.setInterval(refreshMetrics, 30000);
  }

  function stopMetricsRefresh() {
    if (!metricsRefreshTimer) {
      return;
    }
    window.clearInterval(metricsRefreshTimer);
    metricsRefreshTimer = null;
  }

  function refreshLogs() {
    if (document.visibilityState === "hidden" || state.currentView !== "logs") {
      return;
    }
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
    var renderFiltered = debounce(render, 120);
    if (query) {
      query.value = current.query || "";
      query.addEventListener("input", function () {
        state.listFilters[filterName].query = query.value;
        if (filterName === "recorded") {
          state.recordedPage = 1;
        }
        saveListFilters();
        renderFiltered();
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

  function bindSearchControls() {
    var fields = {
      query: "searchQuery",
      category: "searchCategory",
      type: "searchType",
      title: "searchTitle",
      description: "searchDescription",
      programID: "searchProgramID",
      channelID: "searchChannelID",
      startHour: "searchStartHour",
      endHour: "searchEndHour"
    };
    Object.keys(fields).forEach(function (key) {
      var control = byId(fields[key]);
      if (!control) {
        return;
      }
      control.addEventListener(control.tagName === "SELECT" ? "change" : "input", function () {
        state.listFilters.search[key] = control.value.trim ? control.value.trim() : control.value;
        state.searchPage = 1;
        saveListFilters();
        renderSearch();
      });
    });
    var reset = byId("searchFilterReset");
    if (reset) {
      reset.addEventListener("click", function () {
        state.listFilters.search = defaultListFilter("search");
        state.searchPage = 1;
        saveListFilters();
        renderSearch();
      });
    }
    [
      { action: "first", page: function () { return 1; } },
      { action: "prev", page: function () { return state.searchPage - 1; } },
      { action: "next", page: function () { return state.searchPage + 1; } },
      { action: "last", page: function () { return Math.max(1, Math.ceil(searchResults().length / searchPageSize)); } }
    ].forEach(function (item) {
      document.querySelectorAll("[data-search-page-control='" + item.action + "']").forEach(function (button) {
        button.addEventListener("click", function () {
          state.searchPage = item.page();
          renderSearch();
        });
      });
    });
  }

  function bindRecordedPagination() {
    document.querySelectorAll("[data-recorded-page-size]").forEach(function (pageSize) {
      pageSize.value = String(state.recordedPageSize);
      pageSize.addEventListener("change", function () {
        state.recordedPageSize = normalizeRecordedPageSize(pageSize.value);
        state.recordedPage = 1;
        saveRecordedPageSize();
        render();
      });
    });
    [
      { action: "first", icon: "chevrons-left", label: "最初のページ" },
      { action: "prev", icon: "chevron-left", label: "前のページ" },
      { action: "next", icon: "chevron-right", label: "次のページ" },
      { action: "last", icon: "chevrons-right", label: "最後のページ" }
    ].forEach(function (control) {
      document.querySelectorAll("[data-recorded-page-control='" + control.action + "']").forEach(function (button) {
        setIconOnlyControl(button, control.icon, control.label);
      });
    });
    [
      { action: "first", page: function () { return 1; } },
      { action: "prev", page: function () { return state.recordedPage - 1; } },
      { action: "next", page: function () { return state.recordedPage + 1; } },
      {
        action: "last",
        page: function () {
          var filteredRecorded = sortedPrograms(filteredPrograms(state.recorded, "recorded"), "recorded");
          return recordedPageCount(filteredRecorded.length);
        }
      }
    ].forEach(function (control) {
      document.querySelectorAll("[data-recorded-page-control='" + control.action + "']").forEach(function (button) {
        button.addEventListener("click", function () {
          var filteredRecorded = sortedPrograms(filteredPrograms(state.recorded, "recorded"), "recorded");
          state.recordedPage = Math.max(1, Math.min(recordedPageCount(filteredRecorded.length), control.page()));
          render();
          var firstRow = byId("recordedListPage") && byId("recordedListPage").querySelector(".program-title-button");
          if (firstRow && typeof firstRow.focus === "function") {
            firstRow.focus({ preventScroll: true });
          }
        });
      });
    });
  }

  document.addEventListener("DOMContentLoaded", function () {
    syncStickyOffsets();
    window.addEventListener("resize", syncStickyOffsets);
    window.addEventListener("orientationchange", syncStickyOffsets);
    var previousMobileScheduleLayout = isMobileScheduleLayout();
    window.addEventListener("resize", function () {
      var mobileScheduleLayout = isMobileScheduleLayout();
      if (mobileScheduleLayout !== previousMobileScheduleLayout) {
        closeScheduleMenu();
        closeScheduleFilter();
      } else if (!mobileScheduleLayout) {
        closeScheduleMenu();
        closeScheduleFilter();
      }
      previousMobileScheduleLayout = mobileScheduleLayout;
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
      var renderRulesFiltered = debounce(renderRules, 120);
      ruleListQuery.addEventListener("input", function () {
        state.listFilters.rules.query = ruleListQuery.value;
        saveListFilters();
        renderRulesFiltered();
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
    var playerRetryButton = byId("playerRetryButton");
    if (playerRetryButton) {
      playerRetryButton.addEventListener("click", retryPlayerSource);
    }
    bindPlayerVideoEvents();
    bindPlayerSubtitleEvents();
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
    var playerSubtitle = byId("playerSubtitle");
    if (playerSubtitle) {
      playerSubtitle.addEventListener("change", changePlayerSubtitle);
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
      strataConfigForm.addEventListener("input", function () {
        strataConfigFormDirty = true;
      });
      strataConfigForm.addEventListener("change", function () {
        strataConfigFormDirty = true;
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
        strataConfigFormDirty = true;
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
    bindSearchControls();
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
    document.addEventListener("visibilitychange", function () {
      if (document.visibilityState !== "visible") {
        stopMetricsRefresh();
        return;
      }
      refreshOperationalData();
      if (state.currentView === "status") {
        startMetricsRefresh();
      }
      if (state.currentView === "logs") {
        refreshLogs();
      }
    });
    refreshLogs();
  });
}());
