"use strict";

const $ = (sel) => document.querySelector(sel);

const PROGRESS_STALL_SEC = 5 * 60;

let port = null;

function truncate(str, len) {
  if (!str) return "";
  return str.length > len ? str.slice(0, len) + "..." : str;
}

function escHtml(s) {
  const d = document.createElement("div");
  d.textContent = s || "";
  return d.innerHTML;
}

function escAttr(s) {
  return (s || "")
    .replace(/&/g, "&amp;")
    .replace(/"/g, "&quot;")
    .replace(/'/g, "&#39;")
    .replace(/</g, "&lt;")
    .replace(/>/g, "&gt;");
}

// --- Rendering ---

function renderOffline(serverUrl) {
  $("#main").style.display = "none";
  $("#offline").style.display = "flex";
  $("#offline-url").textContent = serverUrl || "Server URL not configured";
  $("#conn-dot").classList.remove("online");
  $("#conn-dot").classList.add("offline");
}

function renderOnline(state) {
  $("#offline").style.display = "none";
  $("#main").style.display = "";
  $("#conn-dot").classList.remove("offline");
  $("#conn-dot").classList.add("online");

  const workspace = state.workspace || state.agents?.[0]?.workspace || "";
  const short =
    workspace.length > 40
      ? "..." + workspace.slice(workspace.length - 37)
      : workspace;
  $("#workspace").textContent = short;
  $("#workspace").title = workspace;

  renderWorkers(state);
  renderTasks(state);
  renderMessages(state);
}

function renderWorkers(state) {
  const workers = state.workers || [];
  const agents = state.agents || [];
  const agentMap = {};
  for (const a of agents) agentMap[a.name] = a;

  $("#worker-count").textContent = workers.length;

  if (!workers.length) {
    $("#workers").innerHTML = '<div class="empty">No workers registered</div>';
    return;
  }

  const taskMap = {};
  for (const t of state.tasks || []) {
    if (t.assigned_to && t.status === "in_progress") {
      taskMap[t.assigned_to] = t;
    }
  }

  let html = "";
  for (const w of workers) {
    // Task-bound workers (e.g. "claude-code-task-7") have no agentMap entry
    // keyed by instance_id — fall back to the agent_type so role still
    // resolves. Mirrors the dashboard's grouping logic in page.go.
    const agent =
      agentMap[w.instance_id] || agentMap[w.agent_type] || {};

    // Offline / unreachable rules: backend now writes Status="offline" on
    // stale rows + flips Reachable=false; treat both signals as authoritative
    // so a stale "busy" row never sneaks "working" UI through.
    const offline = w.status === "offline" || w.reachable === false;
    const isActive = !offline && w.status !== "idle";

    const statusClass = offline
      ? "offline"
      : w.status === "busy"
        ? "busy"
        : w.status === "idle"
          ? "idle"
          : "working";

    const task = isActive ? taskMap[w.instance_id] : null;
    const taskTitle = task ? truncate(task.title, 45) : "";

    const pct =
      isActive && w.progress_step && w.progress_total_steps
        ? clampPct((w.progress_step / w.progress_total_steps) * 100)
        : null;

    const statusLabel = offline
      ? `<div class="worker-task" style="color:var(--text-dim)">offline${w.last_heartbeat ? " \u00b7 last seen " + escHtml(w.last_heartbeat) : ""}</div>`
      : w.status === "idle"
        ? `<div class="worker-task" style="color:var(--text-dim)">idle</div>`
        : "";

    const taskBoundHint = w.is_task_bound
      ? ` <span class="task-bound-hint" title="Task-bound worker — lifetime tied to task #${w.bound_task_id || "?"}">task-${w.bound_task_id || "?"}</span>`
      : "";

    html += `<div class="worker-row">
      <span class="worker-dot ${statusClass}"></span>
      <div class="worker-info">
        <div class="worker-name">${escHtml(w.instance_id)}${taskBoundHint}<span class="role">${escHtml(agent.role || w.agent_type || "")}</span></div>
        ${!isActive ? statusLabel : ""}
        ${taskTitle ? `<div class="worker-task">${escHtml(taskTitle)}</div>` : ""}
        ${isActive && w.progress ? `<div class="worker-task" title="${escAttr(w.progress)}">${escHtml(truncate(w.progress, 60))}</div>` : ""}
        ${pct !== null ? `<div class="worker-progress-wrap"><div class="progress-bar"><div class="progress-fill" style="width:${pct}%"></div></div><span class="progress-pct">${pct}%</span></div>` : ""}
      </div>
    </div>`;
  }
  $("#workers").innerHTML = html;
}

function renderTasks(state) {
  const tasks = (state.tasks || []).filter(
    (t) => t.status !== "completed" && t.status !== "cancelled"
  );

  const countEl = $("#task-count");
  countEl.textContent = tasks.length;

  const hasAlert = tasks.some(
    (t) => t.status === "blocked" || t.sla_status === "exceeded"
  );
  countEl.classList.toggle("alert", hasAlert);

  if (!tasks.length) {
    $("#tasks").innerHTML = '<div class="empty">No active tasks</div>';
    return;
  }

  let html = "";
  for (const t of tasks) {
    const stalled =
      t.status === "in_progress" &&
      t.last_progress_age &&
      parseAge(t.last_progress_age) > PROGRESS_STALL_SEC;

    const safeStatus = sanitizeStatus(t.status);
    const safePct = clampPct(t.progress_percent);

    let rowClass = "task-row";
    if (safeStatus === "blocked") rowClass += " blocked-glow";
    if (t.sla_status === "exceeded") rowClass += " sla-warn";

    html += `<div class="${rowClass}">
      <span class="task-badge ${safeStatus}">${safeStatus.replace("_", " ")}</span>
      <div class="task-info">
        <div class="task-title">#${parseInt(t.id) || 0} ${escHtml(truncate(t.title, 40))}</div>
        <div class="task-meta">
          <span>${escHtml(t.assigned_to || "unassigned")}</span>
          <span>${escHtml(t.age || "")}</span>
          ${stalled ? '<span style="color:var(--red)">stalled</span>' : ""}
          ${t.sla_status === "exceeded" ? '<span style="color:var(--orange)">SLA exceeded</span>' : ""}
        </div>
        ${t.progress_percent ? `<div class="worker-progress-wrap"><div class="progress-bar"><div class="progress-fill" style="width:${safePct}%"></div></div><span class="progress-pct">${safePct}%</span></div>` : ""}
      </div>
    </div>`;
  }
  $("#tasks").innerHTML = html;
}

function renderMessages(state) {
  const driverName = getDriverName(state);
  const msgs = (state.messages || [])
    .filter((m) => m.to === driverName)
    .slice(-10)
    .reverse();

  const unread = msgs.filter((m) => !m.read).length;
  const countEl = $("#msg-count");
  countEl.textContent = unread;
  countEl.classList.toggle("alert", unread > 0);

  if (!msgs.length) {
    $("#messages").innerHTML = '<div class="empty">No messages</div>';
    return;
  }

  let html = "";
  for (const m of msgs) {
    const fromClass = m.from === "system" ? "system" : "";
    html += `<div class="msg-row">
      <span class="msg-from ${fromClass}">${escHtml(m.from)}</span>
      <span class="msg-time">${escHtml(m.timestamp || m.age || "")}</span>
      <div class="msg-content">${escHtml(truncate(m.content, 120))}</div>
    </div>`;
  }
  $("#messages").innerHTML = html;
}

// --- Actions ---

function initActions() {
  const restartBtn = $("#restart-btn");
  let confirmTimeout = null;

  restartBtn.addEventListener("click", () => {
    if (restartBtn.classList.contains("confirm")) {
      restartBtn.disabled = true;
      restartBtn.textContent = "Restarting...";
      chrome.runtime.sendMessage({ type: "restartWorkers" }, (resp) => {
        if (resp?.ok) {
          restartBtn.textContent = "Restarted";
        } else {
          restartBtn.textContent = resp?.error || "Failed";
        }
        setTimeout(() => {
          restartBtn.textContent = "Restart Workers";
          restartBtn.disabled = false;
          restartBtn.classList.remove("confirm");
        }, 2000);
      });
      clearTimeout(confirmTimeout);
      return;
    }

    restartBtn.classList.add("confirm");
    restartBtn.textContent = "Confirm restart?";
    confirmTimeout = setTimeout(() => {
      restartBtn.classList.remove("confirm");
      restartBtn.textContent = "Restart Workers";
    }, 3000);
  });

  $("#open-dashboard").addEventListener("click", (e) => {
    e.preventDefault();
    chrome.runtime.sendMessage({ type: "openDashboard" });
  });

  $("#gear-btn").addEventListener("click", () => {
    chrome.runtime.openOptionsPage();
  });

  $("#open-options").addEventListener("click", (e) => {
    e.preventDefault();
    chrome.runtime.openOptionsPage();
  });

  const msgToggle = $("#msg-toggle");
  const msgBody = $("#messages");
  msgToggle.addEventListener("click", () => {
    const expanded = msgToggle.getAttribute("aria-expanded") === "true";
    msgToggle.setAttribute("aria-expanded", String(!expanded));
    msgBody.classList.toggle("collapsed");
  });
}

// --- Lifecycle ---

function connect() {
  port = chrome.runtime.connect({ name: "popup" });
  port.onMessage.addListener((msg) => {
    if (msg.type === "stateUpdate") {
      if (msg.offline) {
        chrome.storage.local.get("settings", (data) => {
          const url = data.settings?.serverUrl || DEFAULTS.serverUrl;
          renderOffline(url);
        });
      } else if (msg.state) {
        renderOnline(msg.state);
      }
    }
  });

  port.onDisconnect.addListener(() => {
    port = null;
  });
}

document.addEventListener("DOMContentLoaded", () => {
  initActions();
  connect();

  chrome.runtime.sendMessage({ type: "getState" }, (resp) => {
    if (resp?.offline || !resp?.state) {
      chrome.storage.local.get("settings", (data) => {
        const url = data.settings?.serverUrl || DEFAULTS.serverUrl;
        renderOffline(url);
      });
    } else {
      renderOnline(resp.state);
    }
  });
});
