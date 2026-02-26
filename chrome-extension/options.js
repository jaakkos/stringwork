"use strict";

function load() {
  chrome.storage.local.get("settings", (data) => {
    const s = { ...DEFAULTS, ...(data.settings || {}) };
    document.getElementById("server-url").value = s.serverUrl;
    document.getElementById("poll-interval").value = String(
      validatePollInterval(s.pollIntervalSec)
    );
    document.getElementById("notifications").checked = s.notificationsEnabled;
  });
}

function save() {
  const rawUrl = document.getElementById("server-url").value.replace(/\/+$/, "");
  const settings = {
    serverUrl: rawUrl || DEFAULTS.serverUrl,
    pollIntervalSec: validatePollInterval(
      document.getElementById("poll-interval").value
    ),
    notificationsEnabled: document.getElementById("notifications").checked,
  };

  chrome.storage.local.set({ settings }, () => {
    const msg = document.getElementById("saved-msg");
    msg.classList.add("show");
    setTimeout(() => msg.classList.remove("show"), 2000);

    chrome.runtime.sendMessage({ type: "settingsUpdated" });
  });
}

document.addEventListener("DOMContentLoaded", load);
document.getElementById("save-btn").addEventListener("click", save);
