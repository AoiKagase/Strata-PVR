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
      } else if (name === "watch") {
        row.appendChild(actionButton("Watch", "Open m2ts stream", function () {
          window.location.href = "/api/recorded/" + encodeURIComponent(program.id) + "/watch.m2ts";
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
      (channel.programs || []).slice(0, 2).forEach(function (program) {
        programs.push(program);
      });
    });
    programs.sort(function (a, b) {
      return (a.start || 0) - (b.start || 0);
    });
    renderList("scheduleList", programs, "No schedule data", 10, ["reserve"]);
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

    renderList("recordingList", state.recording, "No active recordings", 8, ["stop"]);
    renderList("reserveList", state.reserves, "No reserves", 8, ["skip", "unskip", "unreserve"]);
    renderList("recordedList", state.recorded.slice().reverse(), "No recorded items", 8, ["watch", "download", "delete-recorded"]);
    renderSchedule();
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
      api("schedule.json")
    ]).then(function (result) {
      state.status = result[0] || {};
      state.reserves = result[1] || [];
      state.recording = result[2] || [];
      state.recorded = result[3] || [];
      state.schedule = result[4] || [];
      render();
    }).catch(showError);
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
