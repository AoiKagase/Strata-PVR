(function () {
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
    scheduleHiddenChannels: [],
    scheduleWindowHours: 24,
    scheduleLimit: 50,
    editingRuleIndex: null,
    selectedProgram: null,
    configEditorDirty: false
  };

  function byId(id) {
    return document.getElementById(id);
  }

  function text(node, value) {
    if (node) {
      node.textContent = value;
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
        return body ? JSON.parse(body) : {};
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
        return body ? JSON.parse(body) : {};
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
        return body ? JSON.parse(body) : {};
      });
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
    document.querySelectorAll("[data-view-link]").forEach(function (link) {
      var active = link.getAttribute("data-view-link") === name;
      link.classList.toggle("active", active);
      if (active) {
        link.setAttribute("aria-current", "page");
      } else {
        link.removeAttribute("aria-current");
      }
    });
  }

  function initNavigation() {
    setView(currentView());
    window.addEventListener("hashchange", function () {
      setView(currentView());
    });
  }

  function confirmAction(message) {
    return window.confirm(message);
  }

  function runAction(path, method, message) {
    if (message && !confirmAction(message)) {
      return;
    }
    setBusy("Working");
    request(path, method).then(refresh).catch(showError);
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
        row.appendChild(actionButton("予約", "この番組を予約", function () {
          runAction("program/" + encodeURIComponent(program.id) + ".json", "PUT");
        }));
      } else if (name === "unreserve" && program.isManualReserved) {
        row.appendChild(actionButton("予約削除", "手動予約を削除", function () {
          runAction("reserves/" + encodeURIComponent(program.id) + ".json", "DELETE", "この手動予約を削除しますか？");
        }));
      } else if (name === "skip" && !program.isManualReserved && !program.isSkip) {
        row.appendChild(actionButton("スキップ", "自動予約をスキップ", function () {
          runAction("reserves/" + encodeURIComponent(program.id) + "/skip.json", "PUT");
        }));
      } else if (name === "unskip" && !program.isManualReserved && program.isSkip) {
        row.appendChild(actionButton("解除", "スキップを解除", function () {
          runAction("reserves/" + encodeURIComponent(program.id) + "/unskip.json", "PUT");
        }));
      } else if (name === "stop") {
        row.appendChild(actionButton("停止", "録画を停止", function () {
          runAction("recording/" + encodeURIComponent(program.id) + ".json", "DELETE", "この録画を停止しますか？");
        }));
      } else if (name === "watch-recording") {
        row.appendChild(actionButton("M2TS", "録画中のM2TSを開く", function () {
          openURL(recordingWatchURL(program, "m2ts"));
        }));
      } else if (name === "watch-recording-mp4") {
        row.appendChild(actionButton("MP4", "録画中のMP4変換視聴を開く", function () {
          openURL(recordingWatchURL(program, "mp4"));
        }));
      } else if (name === "playlist-recording") {
        row.appendChild(actionButton("XSPF", "録画中のプレイリストを開く", function () {
          openURL(recordingWatchURL(program, "xspf"));
        }));
      } else if (name === "preview-recording") {
        row.appendChild(actionButton("プレビュー", "録画中のプレビュー画像を開く", function () {
          openURL("/api/recording/" + encodeURIComponent(program.id) + "/preview.png");
        }));
      } else if (name === "watch-m2ts") {
        row.appendChild(actionButton("M2TS", "M2TSを開く", function () {
          openURL(recordedWatchURL(program, "m2ts"));
        }));
      } else if (name === "watch-mp4") {
        row.appendChild(actionButton("MP4", "MP4変換視聴を開く", function () {
          openURL(recordedWatchURL(program, "mp4"));
        }));
      } else if (name === "watch-mp4-720p") {
        row.appendChild(actionButton("MP4 720p", "720p MP4変換視聴を開く", function () {
          openURL(recordedWatchURL(program, "mp4", { "s": "1280x720", "b:v": "1800k", "b:a": "128k" }));
        }));
      } else if (name === "watch-mp4-low") {
        row.appendChild(actionButton("MP4低画質", "低ビットレートMP4変換視聴を開く", function () {
          openURL(recordedWatchURL(program, "mp4", { "s": "640x360", "b:v": "800k", "b:a": "96k" }));
        }));
      } else if (name === "watch-mp4-custom") {
        row.appendChild(actionButton("MP4指定", "再生条件付きMP4変換視聴を開く", function () {
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
          runAction("recorded/" + encodeURIComponent(program.id) + ".json", "DELETE", "この録画済み項目とファイルを削除しますか？");
        }));
      }
    });
    if (row.childNodes.length > 0) {
      item.appendChild(row);
    }
  }

  function renderProgramRow(program, actions, showChannel) {
    var item = document.createElement("article");
    item.className = "program-row";

    var title = document.createElement("strong");
    title.textContent = programTitle(program);

    var meta = document.createElement("span");
    var parts = [formatTime(program.start)];
    if (showChannel) {
      parts.push(channelName(program));
    }
    parts.push(program.category);
    meta.textContent = parts.filter(Boolean).join(" / ");

    item.appendChild(title);
    item.appendChild(meta);
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

  function renderSchedule() {
    var root = byId("scheduleList");
    if (!root) {
      return;
    }
    var channels = state.schedule || [];
    var candidates = [];
    var channelOrder = [];
    var channelMeta = {};
    var channelGroups = [];
    var groupsByID = {};
    var limit = state.scheduleLimit || 20;
    var now = Date.now();
    var until = state.scheduleWindowHours > 0 ? now + (state.scheduleWindowHours * 60 * 60 * 1000) : 0;

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
        candidates.push({
          channelID: groupID,
          program: item
        });
      });
    });
    candidates.sort(function (a, b) {
      return (a.program.start || 0) - (b.program.start || 0);
    });
    candidates.slice(0, limit).forEach(function (item) {
      if (!groupsByID[item.channelID]) {
        groupsByID[item.channelID] = channelMeta[item.channelID];
      }
      groupsByID[item.channelID].programs.push(item.program);
    });
    channelOrder.forEach(function (id) {
      if (groupsByID[id] && groupsByID[id].programs.length) {
        channelGroups.push(groupsByID[id]);
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
    var minuteHeight = 0.72;

    channelGroups.forEach(function (group) {
      group.programs.forEach(function (program) {
        if (firstStart === null || program.start < firstStart) {
          firstStart = program.start;
        }
        var end = program.end || (program.start + 30 * 60 * 1000);
        if (lastEnd === null || end > lastEnd) {
          lastEnd = end;
        }
      });
    });
    firstStart = Math.floor(firstStart / 3600000) * 3600000;
    lastEnd = Math.ceil(lastEnd / 3600000) * 3600000;
    var totalMinutes = Math.max(60, Math.round((lastEnd - firstStart) / 60000));
    var guideHeight = Math.max(180, Math.round(totalMinutes * minuteHeight));

    root.className = "schedule-guide";
    var scroll = document.createElement("div");
    scroll.className = "schedule-guide-scroll";
    scroll.setAttribute("role", "region");
    scroll.setAttribute("aria-label", "番組表");

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
      var name = document.createElement("strong");
      name.textContent = group.name;
      heading.appendChild(name);
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
  }

  function renderChannelActions(channelID) {
    var row = document.createElement("div");
    row.className = "channel-actions";
    if (!channelID || channelID === "unknown") {
      return row;
    }
    row.appendChild(actionButton("M2TS", "チャンネルのM2TSを開く", function () {
      openURL(channelURL(channelID, "watch", "m2ts"));
    }));
    row.appendChild(actionButton("MP4", "チャンネルのMP4変換視聴を開く", function () {
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
    var card = document.createElement("article");
    card.className = "schedule-card" + categoryClass(program.category);
    var end = program.end || (program.start + 30 * 60 * 1000);
    var top = Math.max(0, Math.round(((program.start - timelineStart) / 60000) * minuteHeight));
    var height = Math.max(24, Math.round(((end - program.start) / 60000) * minuteHeight));
    card.style.top = top + "px";
    card.style.height = height + "px";
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
    meta.textContent = program.category || "未分類";

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
    var dialog = byId("programDialog");
    var title = byId("programDialogTitle");
    var meta = byId("programDialogMeta");
    var description = byId("programDialogDescription");
    var actions = byId("programDialogActions");
    var end = program.end || (program.start + 30 * 60 * 1000);
    state.selectedProgram = program;
    text(title, programTitle(program));
    text(meta, [formatTime(program.start) + " - " + formatTime(end), channelName(program), program.category, formatDuration(program.start, end)].filter(Boolean).join(" / "));
    text(description, program.detail || program.description || "番組説明はありません。");
    if (actions) {
      actions.innerHTML = "";
      renderActions(actions, program, ["reserve", "unreserve", "skip", "unskip"]);
    }
    if (dialog && dialog.showModal) {
      dialog.showModal();
    } else if (dialog) {
      dialog.setAttribute("open", "open");
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
      if (!id || state.scheduleHiddenChannels.indexOf(id) >= 0) {
        return;
      }
      var option = document.createElement("option");
      option.value = id;
      option.textContent = scheduleChannelName(channel) || id;
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
      return;
    }
    root.className = "list";
    state.rules.forEach(function (rule, index) {
      var item = document.createElement("article");
      item.className = "program-row";

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
      row.appendChild(actionButton("削除", "このルールを削除", function () {
        runAction("rules/" + index + ".json", "DELETE", "このルールを削除しますか？");
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
    setControlValue("configMirakurunPath", cfg.mirakurunPath || cfg.schedulerMirakurunPath);
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
      ["録画ディレクトリ", cfg.recordedDir],
      ["録画ファイル名", cfg.recordedFormat],
      ["WUIホスト", cfg.wuiHost],
      ["WUIポート", cfg.wuiPort],
      ["公開ホスト", cfg.wuiOpenHost],
      ["公開ポート", cfg.wuiOpenPort],
      ["TLS", Boolean(cfg.wuiTlsKeyPath || cfg.wuiTlsCertPath)],
      ["WUIユーザー", cfg.wuiUsers],
      ["許可国コード", cfg.wuiAllowCountries],
      ["サービス順", cfg.serviceOrder],
      ["VAAPI", cfg.vaapiEnabled],
      ["除外サービス", cfg.excludeServices],
      ["空き容量しきい値MB", cfg.storageLowSpaceThresholdMB],
      ["空き容量不足時の動作", cfg.storageLowSpaceAction],
      ["空き容量通知先", cfg.storageLowSpaceNotifyTo],
      ["正規化", cfg.normalizationForm]
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
    delete config.schedulerMirakurunPath;
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
    if (!confirmAction("フォームの内容で config.json を保存しますか？")) {
      return;
    }
    setBusy("設定保存中");
    sendConfigJSON(JSON.stringify(config, null, 2)).then(function (savedConfig) {
      state.config = savedConfig || {};
      state.configEditorDirty = false;
      render();
      setBusy("設定を保存しました");
    }).catch(showError);
  }

  function saveConfigFromEditor() {
    var raw = readConfigEditor();
    if (!raw) {
      return;
    }
    if (!confirmAction("config.json を保存しますか？")) {
      return;
    }
    setBusy("設定保存中");
    sendConfigJSON(raw).then(function (config) {
      state.config = config || {};
      state.configEditorDirty = false;
      render();
      setBusy("設定を保存しました");
    }).catch(showError);
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
    setBusy("処理中");
    sendJSON("rules.json", "POST", rule).then(function () {
      state.editingRuleIndex = null;
      renderRuleEditorState();
      refresh();
    }).catch(showError);
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
    setBusy("処理中");
    sendJSON("rules/" + state.editingRuleIndex + ".json", "PUT", rule).then(function () {
      state.editingRuleIndex = null;
      renderRuleEditorState();
      refresh();
    }).catch(showError);
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

  function addBasicRule() {
    var title = byId("ruleTitle");
    var ignoreTitle = byId("ruleIgnoreTitle");
    var description = byId("ruleDescription");
    var ignoreDescription = byId("ruleIgnoreDescription");
    var type = byId("ruleType");
    var sid = byId("ruleSid");
    var category = byId("ruleCategory");
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
    if (title && title.value.trim()) {
      rule.reserve_titles = [title.value.trim()];
    }
    if (ignoreTitle && ignoreTitle.value.trim()) {
      rule.ignore_titles = [ignoreTitle.value.trim()];
    }
    if (description && description.value.trim()) {
      rule.reserve_descriptions = [description.value.trim()];
    }
    if (ignoreDescription && ignoreDescription.value.trim()) {
      rule.ignore_descriptions = [ignoreDescription.value.trim()];
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
      rule.categories = [category.value.trim()];
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
    setBusy("処理中");
    sendJSON("rules.json", "POST", rule).then(function () {
      if (title) {
        title.value = "";
      }
      if (ignoreTitle) {
        ignoreTitle.value = "";
      }
      if (description) {
        description.value = "";
      }
      if (ignoreDescription) {
        ignoreDescription.value = "";
      }
      if (category) {
        category.value = "";
      }
      if (sid) {
        sid.value = "";
      }
      if (channels) {
        channels.value = "";
      }
      if (ignoreChannels) {
        ignoreChannels.value = "";
      }
      if (flags) {
        flags.value = "";
      }
      if (ignoreFlags) {
        ignoreFlags.value = "";
      }
      if (durationMin) {
        durationMin.value = "";
      }
      if (durationMax) {
        durationMax.value = "";
      }
      if (hourStart) {
        hourStart.value = "";
      }
      if (hourEnd) {
        hourEnd.value = "";
      }
      if (recordedFormat) {
        recordedFormat.value = "";
      }
      if (disabled) {
        disabled.checked = false;
      }
      if (extraJSON) {
        extraJSON.value = "";
      }
      refresh();
    }).catch(showError);
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

    renderList("recordingList", state.recording, "録画中の番組はありません", 8, ["watch-recording", "watch-recording-mp4", "playlist-recording", "preview-recording", "stop"]);
    renderList("reserveList", state.reserves, "予約はありません", 8, ["skip", "unskip", "unreserve"]);
    renderList("reserveListPage", state.reserves, "予約はありません", 100, ["skip", "unskip", "unreserve"]);
    renderList("recordedList", state.recorded.slice().reverse(), "録画済み番組はありません", 8, ["watch-m2ts", "watch-mp4", "watch-mp4-720p", "watch-mp4-low", "watch-mp4-custom", "watch-m2ts-offset", "playlist", "download", "preview-recorded", "delete-recorded"]);
    renderList("recordedListPage", state.recorded.slice().reverse(), "録画済み番組はありません", 100, ["watch-m2ts", "watch-mp4", "watch-mp4-720p", "watch-mp4-low", "watch-mp4-custom", "watch-m2ts-offset", "playlist", "download", "preview-recorded", "delete-recorded"]);
    renderSchedule();
    renderRules();
    renderSettings();
    renderRuleEditorState();
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

  document.addEventListener("DOMContentLoaded", function () {
    initNavigation();
    var refreshButton = byId("refreshButton");
    if (refreshButton) {
      refreshButton.addEventListener("click", refresh);
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
    var refreshLogsButton = byId("refreshLogsButton");
    if (refreshLogsButton) {
      refreshLogsButton.addEventListener("click", refreshLogs);
    }
    var programDialogClose = byId("programDialogClose");
    if (programDialogClose) {
      programDialogClose.addEventListener("click", closeProgramDialog);
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
          renderSchedule();
        }
      });
    }
    var scheduleClearHiddenButton = byId("scheduleClearHiddenButton");
    if (scheduleClearHiddenButton) {
      scheduleClearHiddenButton.addEventListener("click", function () {
        state.scheduleHiddenChannels = [];
        renderSchedule();
      });
    }
    var scheduleWindow = byId("scheduleWindow");
    if (scheduleWindow) {
      scheduleWindow.addEventListener("change", function () {
        state.scheduleWindowHours = Number(scheduleWindow.value) || 0;
        renderSchedule();
      });
    }
    var scheduleLimit = byId("scheduleLimit");
    if (scheduleLimit) {
      scheduleLimit.value = String(state.scheduleLimit);
      scheduleLimit.addEventListener("change", function () {
        state.scheduleLimit = Number(scheduleLimit.value) || 50;
        renderSchedule();
      });
    }
    refresh();
    setInterval(refresh, 30000);
    setInterval(refreshLogs, 60000);
    refreshLogs();
  });
}());
