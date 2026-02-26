"use strict";

const DEFAULTS = {
  serverUrl: "http://localhost:8943",
  pollIntervalSec: 60,
  notificationsEnabled: true,
};

const VALID_POLL_INTERVALS = [60, 120];

const VALID_STATUSES = new Set([
  "pending",
  "in_progress",
  "blocked",
  "completed",
  "cancelled",
]);

function parseAge(age) {
  if (age == null || age === "") return Infinity;
  let total = 0;
  const h = age.match(/(\d+)h/);
  const m = age.match(/(\d+)m/);
  const s = age.match(/(\d+)s/);
  if (h) total += parseInt(h[1]) * 3600;
  if (m) total += parseInt(m[1]) * 60;
  if (s) total += parseInt(s[1]);
  if (total === 0 && /ago/.test(age)) return 0;
  return total;
}

function getDriverName(state) {
  if (!state?.agents) return "cursor";
  const driver = state.agents.find((a) => a.role === "driver");
  return driver?.name || "cursor";
}

function sanitizeStatus(status) {
  return VALID_STATUSES.has(status) ? status : "pending";
}

function clampPct(value) {
  const n = Number(value);
  if (!Number.isFinite(n)) return 0;
  return Math.max(0, Math.min(100, Math.round(n)));
}

function validatePollInterval(value) {
  const n = parseInt(value, 10);
  return VALID_POLL_INTERVALS.includes(n) ? n : DEFAULTS.pollIntervalSec;
}
