"use strict";
importScripts("shared.js");

const ALARM_NAME = "stringwork-poll";
const ALERT_COOLDOWN_MS = 5 * 60 * 1000;
const SEEN_ALERT_TTL_MS = 60 * 60 * 1000;
const ACTIVE_POLL_MS = 5000;
const PROGRESS_STALL_THRESHOLD_SEC = 5 * 60;

let activePollTimer = null;
let popupPort = null;
let cachedState = null;
let polling = false;

async function loadSettings() {
  const data = await chrome.storage.local.get("settings");
  return { ...DEFAULTS, ...(data.settings || {}) };
}

async function loadSeenAlerts() {
  const data = await chrome.storage.local.get("seenAlerts");
  const seen = data.seenAlerts || {};
  const now = Date.now();
  for (const key of Object.keys(seen)) {
    if (now - seen[key] > SEEN_ALERT_TTL_MS) delete seen[key];
  }
  return seen;
}

async function loadLastState() {
  const data = await chrome.storage.local.get("lastState");
  return data.lastState || null;
}

async function saveState(state) {
  await chrome.storage.local.set({ lastState: state, lastPollAt: Date.now() });
}

async function saveSeenAlerts(seen) {
  await chrome.storage.local.set({ seenAlerts: seen });
}

async function fetchState(settings) {
  const url = `${settings.serverUrl}/api/state`;
  const resp = await fetch(url, { signal: AbortSignal.timeout(8000) });
  if (!resp.ok) throw new Error(`HTTP ${resp.status}`);
  return resp.json();
}

function detectAlerts(newState, oldState) {
  const alerts = [];
  if (!newState) return alerts;

  const oldWorkerMap = {};
  if (oldState?.workers) {
    for (const w of oldState.workers) oldWorkerMap[w.instance_id] = w;
  }

  for (const w of newState.workers || []) {
    if (w.status === "offline") {
      const old = oldWorkerMap[w.instance_id];
      if (!old || old.status !== "offline") {
        alerts.push({
          key: `offline:${w.instance_id}`,
          title: `Worker ${w.instance_id} offline`,
          message: "Worker went offline.",
          severity: "red",
        });
      }
    }
  }

  const oldTaskMap = {};
  if (oldState?.tasks) {
    for (const t of oldState.tasks) oldTaskMap[t.id] = t;
  }

  for (const t of newState.tasks || []) {
    if (t.status === "blocked") {
      const old = oldTaskMap[t.id];
      if (!old || old.status !== "blocked") {
        alerts.push({
          key: `blocked:task:${t.id}`,
          title: `Task #${t.id} blocked`,
          message: t.title || "Task is blocked",
          severity: "red",
        });
      }
    }

    if (t.sla_status === "exceeded") {
      const old = oldTaskMap[t.id];
      if (!old || old.sla_status !== "exceeded") {
        alerts.push({
          key: `sla:task:${t.id}`,
          title: `Task #${t.id} SLA exceeded`,
          message: t.title || "SLA has been exceeded",
          severity: "orange",
        });
      }
    }

    if (
      t.status === "in_progress" &&
      t.last_progress_age &&
      parseAge(t.last_progress_age) > PROGRESS_STALL_THRESHOLD_SEC
    ) {
      const old = oldTaskMap[t.id];
      const wasStalled =
        old &&
        old.last_progress_age &&
        parseAge(old.last_progress_age) > PROGRESS_STALL_THRESHOLD_SEC;
      if (!wasStalled) {
        alerts.push({
          key: `stall:task:${t.id}`,
          title: `Task #${t.id} stalled`,
          message: `No progress for ${t.last_progress_age}: ${t.title || ""}`,
          severity: "red",
        });
      }
    }
  }

  return alerts;
}

function computeBadge(state) {
  if (!state) return { text: "", color: "#4F46E5" };

  let red = 0,
    orange = 0;

  for (const w of state.workers || []) {
    if (w.status === "offline") red++;
  }

  for (const t of state.tasks || []) {
    if (t.status === "blocked") red++;
    if (t.sla_status === "exceeded") orange++;
    if (
      t.status === "in_progress" &&
      t.last_progress_age &&
      parseAge(t.last_progress_age) > PROGRESS_STALL_THRESHOLD_SEC
    ) {
      red++;
    }
  }

  const driverName = getDriverName(state);
  const blue = (state.messages || []).filter(
    (m) => !m.read && m.to === driverName
  ).length;

  const total = red + orange + blue;
  let color = "#3B82F6";
  if (red > 0) color = "#EF4444";
  else if (orange > 0) color = "#F97316";

  return { text: total > 0 ? String(total) : "", color };
}

async function updateBadge(state) {
  const { text, color } = computeBadge(state);
  await chrome.action.setBadgeText({ text });
  await chrome.action.setBadgeBackgroundColor({ color });
}

async function fireNotifications(alerts, settings) {
  if (!settings.notificationsEnabled) return;

  const seen = await loadSeenAlerts();
  const now = Date.now();
  let updated = false;

  for (const alert of alerts) {
    if (seen[alert.key] && now - seen[alert.key] < ALERT_COOLDOWN_MS) continue;

    chrome.notifications.create(alert.key, {
      type: "basic",
      iconUrl: "icons/icon128.png",
      title: alert.title,
      message: alert.message,
    });

    seen[alert.key] = now;
    updated = true;
  }

  if (updated) await saveSeenAlerts(seen);
}

async function poll() {
  if (polling) return;
  polling = true;
  try {
    await pollInner();
  } finally {
    polling = false;
  }
}

async function pollInner() {
  const settings = await loadSettings();
  const { wasOffline: storedOffline } =
    await chrome.storage.local.get("wasOffline");
  let wasOffline = storedOffline || false;

  let newState;
  try {
    newState = await fetchState(settings);
  } catch {
    if (!wasOffline && settings.notificationsEnabled) {
      chrome.notifications.create("server-offline", {
        type: "basic",
        iconUrl: "icons/icon128.png",
        title: "Stringwork unreachable",
        message: `Cannot connect to ${settings.serverUrl}`,
      });
    }
    if (!wasOffline) {
      await chrome.storage.local.set({ wasOffline: true });
    }
    await chrome.action.setBadgeText({ text: "!" });
    await chrome.action.setBadgeBackgroundColor({ color: "#EF4444" });
    notifyPopup({ type: "stateUpdate", state: null, offline: true });
    return;
  }

  if (wasOffline && settings.notificationsEnabled) {
    chrome.notifications.create("server-online", {
      type: "basic",
      iconUrl: "icons/icon128.png",
      title: "Stringwork connected",
      message: "Server is reachable again.",
    });
  }
  if (wasOffline) {
    await chrome.storage.local.set({ wasOffline: false });
  }

  const oldState = await loadLastState();
  const alerts = detectAlerts(newState, oldState);

  await updateBadge(newState);
  await fireNotifications(alerts, settings);
  await saveState(newState);

  cachedState = newState;
  notifyPopup({ type: "stateUpdate", state: newState, offline: false });
}

function notifyPopup(msg) {
  if (popupPort) {
    try {
      popupPort.postMessage(msg);
    } catch {
      popupPort = null;
    }
  }
}

function startActivePoll() {
  if (activePollTimer) return;
  poll();
  activePollTimer = setInterval(poll, ACTIVE_POLL_MS);
}

function stopActivePoll() {
  if (activePollTimer) {
    clearInterval(activePollTimer);
    activePollTimer = null;
  }
}

chrome.runtime.onConnect.addListener((port) => {
  if (port.name !== "popup") return;
  popupPort = port;
  startActivePoll();

  port.onDisconnect.addListener(() => {
    popupPort = null;
    stopActivePoll();
  });
});

chrome.runtime.onMessage.addListener((msg, _sender, sendResponse) => {
  switch (msg.type) {
    case "getState":
      if (cachedState) {
        sendResponse({ state: cachedState, offline: false });
        return false;
      }
      (async () => {
        const [st, stored] = await Promise.all([
          loadLastState(),
          chrome.storage.local.get("wasOffline"),
        ]);
        sendResponse({ state: st, offline: stored.wasOffline || false });
      })();
      return true;

    case "restartWorkers":
      loadSettings()
        .then((s) =>
          fetch(`${s.serverUrl}/api/restart-workers`, {
            method: "POST",
            signal: AbortSignal.timeout(10000),
          })
        )
        .then((r) => {
          if (!r.ok) throw new Error(`HTTP ${r.status}`);
          return r.json();
        })
        .then((data) => sendResponse({ ok: true, data }))
        .catch((err) => sendResponse({ ok: false, error: err.message }));
      return true;

    case "openDashboard":
      loadSettings().then((s) => {
        chrome.tabs.create({ url: `${s.serverUrl}/dashboard` });
        sendResponse({ ok: true });
      });
      return true;

    case "settingsUpdated":
      setupAlarm();
      poll();
      sendResponse({ ok: true });
      return false;

    default:
      return false;
  }
});

async function setupAlarm() {
  const settings = await loadSettings();
  await chrome.alarms.clearAll();
  const periodMin = Math.max(1, settings.pollIntervalSec / 60);
  chrome.alarms.create(ALARM_NAME, { periodInMinutes: periodMin });
}

chrome.alarms.onAlarm.addListener((alarm) => {
  if (alarm.name === ALARM_NAME) poll();
});

chrome.runtime.onInstalled.addListener(() => {
  setupAlarm();
  poll();
});

chrome.runtime.onStartup.addListener(() => {
  setupAlarm();
  poll();
});
