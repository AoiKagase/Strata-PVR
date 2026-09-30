"use strict";

const assert = require("node:assert/strict");
const fs = require("node:fs");
const path = require("node:path");
const test = require("node:test");
const vm = require("node:vm");

const appSource = fs.readFileSync(path.join(__dirname, "app.js"), "utf8");

function extractFunction(name) {
  const start = appSource.indexOf(`function ${name}(`);
  assert.notEqual(start, -1, `Could not find ${name} in app.js`);

  const openingBrace = appSource.indexOf("{", start);
  let depth = 0;
  for (let i = openingBrace; i < appSource.length; i += 1) {
    if (appSource[i] === "{") {
      depth += 1;
    } else if (appSource[i] === "}") {
      depth -= 1;
      if (depth === 0) {
        return appSource.slice(start, i + 1);
      }
    }
  }

  throw new Error(`Could not find the end of ${name} in app.js`);
}

function channelOptionEntries(schedule, extraValues, sourceChannels) {
  const context = vm.createContext({ state: { schedule } });
  const functions = ["scheduleChannelID", "scheduleChannelName", "channelOptionEntries"]
    .map(extractFunction)
    .join("\n");
  vm.runInContext(`${functions}\nglobalThis.getEntries = channelOptionEntries;`, context);
  return JSON.parse(JSON.stringify(context.getEntries(extraValues, sourceChannels)));
}

test("search result channel is treated as present when the search view has not loaded the schedule", () => {
  const entries = channelOptionEntries([], ["8kr1"], [{ id: "8kr1", name: "テスト放送局" }]);

  assert.deepEqual(entries, [{ id: "8kr1", name: "テスト放送局" }]);
});

test("schedule channel label wins and search channel with the same ID is not duplicated", () => {
  const entries = channelOptionEntries(
    [{ channel: { id: "8kr1", name: "番組表の局名" } }],
    ["8kr1"],
    [{ id: "8kr1", name: "検索結果の局名" }]
  );

  assert.deepEqual(entries, [{ id: "8kr1", name: "番組表の局名" }]);
});

test("channel absent from both sources keeps the existing unavailable label", () => {
  const entries = channelOptionEntries([], ["8kr1"], []);

  assert.deepEqual(entries, [{ id: "8kr1", name: "8kr1（現在の番組表にありません）" }]);
});
