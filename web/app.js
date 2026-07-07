(function () {
  var state = {
    status: null,
    reserves: [],
    recording: [],
    recorded: [],
    schedule: [],
    rules: [],
    config: {},
    scheduleChannel: "",
    scheduleWindowHours: 24,
    scheduleLimit: 20
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

  function programTitle(program) {
    return program.fullTitle || program.title || program.id || "Untitled";
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
    if (channel.channel && channel.channel.id) {
      return String(channel.channel.id);
    }
    return String(channel.id || "");
  }

  function scheduleChannelName(channel) {
    if (!channel) {
      return "";
    }
    if (channel.channel) {
      return channel.channel.name || channel.channel.channel || channel.channel.id || "";
    }
    return channel.name || channel.channel || channel.id || "";
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

  function renderActions(item, program, actions) {
    if (!actions || actions.length === 0) {
      return;
    }
    var row = document.createElement("div");
    row.className = "row-actions";
    actions.forEach(function (name) {
      if (name === "reserve") {
        row.appendChild(actionButton("Reserve", "Reserve this program", function () {
          runAction("program/" + encodeURIComponent(program.id) + ".json", "PUT");
        }));
      } else if (name === "unreserve" && program.isManualReserved) {
        row.appendChild(actionButton("Unreserve", "Remove this manual reserve", function () {
          runAction("reserves/" + encodeURIComponent(program.id) + ".json", "DELETE", "Remove this manual reserve?");
        }));
      } else if (name === "skip" && !program.isManualReserved && !program.isSkip) {
        row.appendChild(actionButton("Skip", "Skip this auto reserve", function () {
          runAction("reserves/" + encodeURIComponent(program.id) + "/skip.json", "PUT");
        }));
      } else if (name === "unskip" && !program.isManualReserved && program.isSkip) {
        row.appendChild(actionButton("Unskip", "Cancel skip", function () {
          runAction("reserves/" + encodeURIComponent(program.id) + "/unskip.json", "PUT");
        }));
      } else if (name === "stop") {
        row.appendChild(actionButton("Stop", "Stop this recording", function () {
          runAction("recording/" + encodeURIComponent(program.id) + ".json", "DELETE", "Stop this recording?");
        }));
      } else if (name === "watch-recording") {
        row.appendChild(actionButton("Watch M2TS", "Open live M2TS stream", function () {
          window.location.href = "/api/recording/" + encodeURIComponent(program.id) + "/watch.m2ts";
        }));
      } else if (name === "watch-m2ts") {
        row.appendChild(actionButton("Watch M2TS", "Open M2TS stream", function () {
          window.location.href = recordedWatchURL(program, "m2ts");
        }));
      } else if (name === "watch-mp4") {
        row.appendChild(actionButton("Watch MP4", "Open MP4 stream", function () {
          window.location.href = recordedWatchURL(program, "mp4");
        }));
      } else if (name === "watch-mp4-720p") {
        row.appendChild(actionButton("MP4 720p", "Open 720p MP4 transcode", function () {
          window.location.href = recordedWatchURL(program, "mp4", { "s": "1280x720", "b:v": "1800k", "b:a": "128k" });
        }));
      } else if (name === "watch-mp4-low") {
        row.appendChild(actionButton("MP4 Low", "Open low bitrate MP4 transcode", function () {
          window.location.href = recordedWatchURL(program, "mp4", { "s": "640x360", "b:v": "800k", "b:a": "96k" });
        }));
      } else if (name === "playlist") {
        row.appendChild(actionButton("XSPF", "Open XSPF playlist", function () {
          window.location.href = recordedWatchURL(program, "xspf");
        }));
      } else if (name === "download") {
        row.appendChild(actionButton("Download", "Download recorded file", function () {
          window.location.href = "/api/recorded/" + encodeURIComponent(program.id) + "/file.m2ts";
        }));
      } else if (name === "delete-recorded") {
        row.appendChild(actionButton("Delete", "Delete recorded entry and file", function () {
          runAction("recorded/" + encodeURIComponent(program.id) + ".json", "DELETE", "Delete this recorded item and file?");
        }));
      }
    });
    if (row.childNodes.length > 0) {
      item.appendChild(row);
    }
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
      var item = document.createElement("article");
      item.className = "program-row";

      var title = document.createElement("strong");
      title.textContent = programTitle(program);

      var meta = document.createElement("span");
      var parts = [formatTime(program.start), channelName(program), program.category].filter(Boolean);
      meta.textContent = parts.join(" / ");

      item.appendChild(title);
      item.appendChild(meta);
      renderActions(item, program, actions);
      root.appendChild(item);
    });
  }

  function renderSchedule() {
    var channels = state.schedule || [];
    var programs = [];
    channels.forEach(function (channel) {
      var channelID = scheduleChannelID(channel);
      if (state.scheduleChannel && channelID !== state.scheduleChannel) {
        return;
      }
      (channel.programs || []).forEach(function (program) {
        if (!program.channel && channel.channel) {
          program.channel = channel.channel;
        }
        programs.push(program);
      });
    });
    var now = Date.now();
    var until = state.scheduleWindowHours > 0 ? now + (state.scheduleWindowHours * 60 * 60 * 1000) : 0;
    programs = programs.filter(function (program) {
      if (!program || !program.start) {
        return false;
      }
      if (program.end && program.end < now) {
        return false;
      }
      return until === 0 || program.start <= until;
    });
    programs.sort(function (a, b) {
      return (a.start || 0) - (b.start || 0);
    });
    renderScheduleChannelOptions(channels);
    renderList("scheduleList", programs, "No schedule data", state.scheduleLimit, ["reserve"]);
  }

  function renderScheduleChannelOptions(channels) {
    var select = byId("scheduleChannel");
    if (!select) {
      return;
    }
    var current = state.scheduleChannel;
    select.innerHTML = "";
    var all = document.createElement("option");
    all.value = "";
    all.textContent = "All channels";
    select.appendChild(all);
    channels.forEach(function (channel) {
      var id = scheduleChannelID(channel);
      if (!id) {
        return;
      }
      var option = document.createElement("option");
      option.value = id;
      option.textContent = scheduleChannelName(channel) || id;
      select.appendChild(option);
    });
    select.value = current;
    if (select.value !== current) {
      state.scheduleChannel = "";
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
      root.textContent = "No rules";
      return;
    }
    root.className = "list";
    state.rules.forEach(function (rule, index) {
      var item = document.createElement("article");
      item.className = "program-row";

      var title = document.createElement("strong");
      title.textContent = "#" + index + (rule.isDisabled ? " Disabled" : " Enabled");

      var meta = document.createElement("span");
      meta.textContent = ruleSummary(rule);

      var row = document.createElement("div");
      row.className = "row-actions";
      row.appendChild(actionButton(rule.isDisabled ? "Enable" : "Disable", "Toggle rule", function () {
        runAction("rules/" + index + "/" + (rule.isDisabled ? "enable" : "disable") + ".json", "PUT");
      }));
      row.appendChild(actionButton("Edit JSON", "Copy this rule into the editor", function () {
        var editor = byId("ruleEditor");
        if (editor) {
          editor.value = JSON.stringify(rule, null, 2);
          editor.focus();
        }
      }));
      row.appendChild(actionButton("Delete", "Delete this rule", function () {
        runAction("rules/" + index + ".json", "DELETE", "Delete this rule?");
      }));

      item.appendChild(title);
      item.appendChild(meta);
      item.appendChild(row);
      root.appendChild(item);
    });
  }

  function settingValue(value) {
    if (value === undefined || value === null || value === "") {
      return "not set";
    }
    if (Array.isArray(value)) {
      return value.length ? value.join(", ") : "none";
    }
    if (typeof value === "boolean") {
      return value ? "enabled" : "disabled";
    }
    return String(value);
  }

  function renderSettings() {
    var root = byId("settingsList");
    if (!root) {
      return;
    }
    var cfg = state.config || {};
    var rows = [
      ["Mirakurun", cfg.mirakurunPath || cfg.schedulerMirakurunPath],
      ["Recorded directory", cfg.recordedDir],
      ["Recorded format", cfg.recordedFormat],
      ["WUI host", cfg.wuiHost],
      ["WUI port", cfg.wuiPort],
      ["Open host", cfg.wuiOpenHost],
      ["Open port", cfg.wuiOpenPort],
      ["TLS", Boolean(cfg.wuiTlsKeyPath || cfg.wuiTlsCertPath)],
      ["Exclude services", cfg.excludeServices],
      ["Storage low-space MB", cfg.storageLowSpaceThresholdMB],
      ["Storage low-space action", cfg.storageLowSpaceAction],
      ["Normalization", cfg.normalizationForm]
    ];
    root.innerHTML = "";
    rows.forEach(function (row) {
      var wrapper = document.createElement("div");
      var key = document.createElement("dt");
      var value = document.createElement("dd");
      key.textContent = row[0];
      value.textContent = settingValue(row[1]);
      wrapper.appendChild(key);
      wrapper.appendChild(value);
      root.appendChild(wrapper);
    });
  }

  function addRuleFromEditor() {
    var editor = byId("ruleEditor");
    if (!editor) {
      return;
    }
    var rule;
    try {
      rule = JSON.parse(editor.value);
    } catch (error) {
      showError(new Error("Rule JSON is invalid"));
      return;
    }
    setBusy("Working");
    sendJSON("rules.json", "POST", rule).then(refresh).catch(showError);
  }

  function addBasicRule() {
    var title = byId("ruleTitle");
    var ignoreTitle = byId("ruleIgnoreTitle");
    var type = byId("ruleType");
    var category = byId("ruleCategory");
    var durationMin = byId("ruleDurationMin");
    var durationMax = byId("ruleDurationMax");
    var hourStart = byId("ruleHourStart");
    var hourEnd = byId("ruleHourEnd");
    var rule = {};
    if (title && title.value.trim()) {
      rule.reserve_titles = [title.value.trim()];
    }
    if (ignoreTitle && ignoreTitle.value.trim()) {
      rule.ignore_titles = [ignoreTitle.value.trim()];
    }
    if (type && type.value) {
      rule.types = [type.value];
    }
    if (category && category.value.trim()) {
      rule.categories = [category.value.trim()];
    }
    var minText = durationMin ? durationMin.value.trim() : "";
    var maxText = durationMax ? durationMax.value.trim() : "";
    if (minText || maxText) {
      if (!minText || !maxText) {
        showError(new Error("Duration needs min and max"));
        return;
      }
      var min = Number(minText);
      var max = Number(maxText);
      if (!isFinite(min) || !isFinite(max) || min < 0 || max < 0 || min > max) {
        showError(new Error("Duration range is invalid"));
        return;
      }
      rule.duration = { min: Math.round(min * 60), max: Math.round(max * 60) };
    }
    var hourStartText = hourStart ? hourStart.value.trim() : "";
    var hourEndText = hourEnd ? hourEnd.value.trim() : "";
    if (hourStartText || hourEndText) {
      if (!hourStartText || !hourEndText) {
        showError(new Error("Hour needs start and end"));
        return;
      }
      var startHour = Number(hourStartText);
      var endHour = Number(hourEndText);
      if (!Number.isInteger(startHour) || !Number.isInteger(endHour) || startHour < 0 || startHour > 23 || endHour < 1 || endHour > 24) {
        showError(new Error("Hour range is invalid"));
        return;
      }
      rule.hour = { start: startHour, end: endHour };
    }
    if (!rule.reserve_titles && !rule.ignore_titles && !rule.types && !rule.categories && !rule.duration && !rule.hour) {
      showError(new Error("Rule is empty"));
      return;
    }
    setBusy("Working");
    sendJSON("rules.json", "POST", rule).then(function () {
      if (title) {
        title.value = "";
      }
      if (ignoreTitle) {
        ignoreTitle.value = "";
      }
      if (category) {
        category.value = "";
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
      refresh();
    }).catch(showError);
  }

  function tailText(value, maxLines) {
    var lines = (value || "").split(/\r?\n/);
    if (lines.length > maxLines) {
      lines = lines.slice(lines.length - maxLines);
    }
    return lines.join("\n").trim() || "No log data";
  }

  function setLog(id, value) {
    text(byId(id), tailText(value, 80));
  }

  function refreshLogs() {
    setBusy("Loading logs");
    Promise.all([
      apiText("log/scheduler.txt"),
      apiText("log/operator.txt"),
      apiText("log/wui.txt")
    ]).then(function (result) {
      setLog("schedulerLog", result[0]);
      setLog("operatorLog", result[1]);
      setLog("wuiLog", result[2]);
      render();
    }).catch(showError);
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
      badge.textContent = alive ? "Operator running" : "Operator idle";
      badge.className = alive ? "status-badge ok" : "status-badge";
    }

    renderList("recordingList", state.recording, "No active recordings", 8, ["watch-recording", "stop"]);
    renderList("reserveList", state.reserves, "No reserves", 8, ["skip", "unskip", "unreserve"]);
    renderList("recordedList", state.recorded.slice().reverse(), "No recorded items", 8, ["watch-m2ts", "watch-mp4", "watch-mp4-720p", "watch-mp4-low", "playlist", "download", "delete-recorded"]);
    renderSchedule();
    renderRules();
    renderSettings();
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
      api("config.json")
    ]).then(function (result) {
      state.status = result[0] || {};
      state.reserves = result[1] || [];
      state.recording = result[2] || [];
      state.recorded = result[3] || [];
      state.schedule = result[4] || [];
      state.rules = result[5] || [];
      state.config = result[6] || {};
      render();
      refreshLogs();
    }).catch(showError);
  }

  document.addEventListener("DOMContentLoaded", function () {
    var refreshButton = byId("refreshButton");
    if (refreshButton) {
      refreshButton.addEventListener("click", refresh);
    }
    var addRuleButton = byId("addRuleButton");
    if (addRuleButton) {
      addRuleButton.addEventListener("click", addRuleFromEditor);
    }
    var addBasicRuleButton = byId("addBasicRuleButton");
    if (addBasicRuleButton) {
      addBasicRuleButton.addEventListener("click", addBasicRule);
    }
    var refreshLogsButton = byId("refreshLogsButton");
    if (refreshLogsButton) {
      refreshLogsButton.addEventListener("click", refreshLogs);
    }
    var scheduleChannel = byId("scheduleChannel");
    if (scheduleChannel) {
      scheduleChannel.addEventListener("change", function () {
        state.scheduleChannel = scheduleChannel.value;
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
      scheduleLimit.addEventListener("change", function () {
        state.scheduleLimit = Number(scheduleLimit.value) || 20;
        renderSchedule();
      });
    }
    refresh();
    setInterval(refresh, 30000);
  });
}());
