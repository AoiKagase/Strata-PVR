(function () {
  var state = {
    status: null,
    reserves: [],
    recording: [],
    recorded: [],
    schedule: []
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
    return fetch("/api/" + path, { credentials: "same-origin" }).then(function (response) {
      if (!response.ok) {
        throw new Error(path + " returned " + response.status);
      }
      return response.json();
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

  function renderList(id, items, emptyText, limit) {
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
      root.appendChild(item);
    });
  }

  function renderSchedule() {
    var channels = state.schedule || [];
    var programs = [];
    channels.forEach(function (channel) {
      (channel.programs || []).slice(0, 2).forEach(function (program) {
        programs.push(program);
      });
    });
    programs.sort(function (a, b) {
      return (a.start || 0) - (b.start || 0);
    });
    renderList("scheduleList", programs, "No schedule data", 10);
  }

  function render() {
    text(byId("reserveCount"), String(state.reserves.length));
    text(byId("recordingCount"), String(state.recording.length));
    text(byId("recordedCount"), String(state.recorded.length));
    text(byId("channelCount"), String(state.schedule.length));

    var badge = byId("statusBadge");
    if (badge) {
      var operator = state.status && state.status.operator;
      var alive = operator && (operator.alive || operator.isRunning);
      badge.textContent = alive ? "Operator running" : "Operator idle";
      badge.className = alive ? "status-badge ok" : "status-badge";
    }

    renderList("recordingList", state.recording, "No active recordings", 8);
    renderList("reserveList", state.reserves, "No reserves", 8);
    renderList("recordedList", state.recorded.slice().reverse(), "No recorded items", 8);
    renderSchedule();
  }

  function refresh() {
    var badge = byId("statusBadge");
    if (badge) {
      badge.textContent = "Loading";
      badge.className = "status-badge";
    }
    Promise.all([
      api("status.json"),
      api("reserves.json"),
      api("recording.json"),
      api("recorded.json"),
      api("schedule.json")
    ]).then(function (result) {
      state.status = result[0] || {};
      state.reserves = result[1] || [];
      state.recording = result[2] || [];
      state.recorded = result[3] || [];
      state.schedule = result[4] || [];
      render();
    }).catch(function (error) {
      if (badge) {
        badge.textContent = error.message;
        badge.className = "status-badge error";
      }
    });
  }

  document.addEventListener("DOMContentLoaded", function () {
    var refreshButton = byId("refreshButton");
    if (refreshButton) {
      refreshButton.addEventListener("click", refresh);
    }
    refresh();
    setInterval(refresh, 30000);
  });
}());
