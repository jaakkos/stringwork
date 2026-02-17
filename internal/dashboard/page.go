package dashboard

import "net/http"

func (h *Handler) handleDashboard(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Write([]byte(dashboardHTML))
}

const dashboardHTML = `<!DOCTYPE html>
<html lang="en">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>Stringwork Dashboard</title>
<link rel="icon" type="image/svg+xml" href="data:image/svg+xml,%3Csvg xmlns='http://www.w3.org/2000/svg' viewBox='0 0 128 128' fill='none'%3E%3Cpath d='M64 64C53 55 38 48 24 38' stroke='%234F46E5' stroke-width='4' stroke-linecap='round'/%3E%3Cpath d='M64 64C74 50 90 39 105 31' stroke='%234F46E5' stroke-width='4' stroke-linecap='round'/%3E%3Cpath d='M64 64C78 70 96 86 102 103' stroke='%234F46E5' stroke-width='4' stroke-linecap='round'/%3E%3Cpath d='M64 64C55 79 42 93 29 104' stroke='%234F46E5' stroke-width='4' stroke-linecap='round'/%3E%3Ccircle cx='64' cy='64' r='8' fill='%234F46E5' stroke='%23F8FAFC' stroke-width='2'/%3E%3Ccircle cx='64' cy='64' r='2' fill='%23F8FAFC'/%3E%3Ccircle cx='24' cy='38' r='5' fill='%2314B8A6' stroke='%23F8FAFC' stroke-width='2'/%3E%3Ccircle cx='105' cy='31' r='5' fill='%2314B8A6' stroke='%23F8FAFC' stroke-width='2'/%3E%3Ccircle cx='102' cy='103' r='5' fill='%2314B8A6' stroke='%23F8FAFC' stroke-width='2'/%3E%3Ccircle cx='29' cy='104' r='5' fill='%2314B8A6' stroke='%23F8FAFC' stroke-width='2'/%3E%3C/svg%3E">
<style>
  :root {
    --bg: #0d1117;
    --surface: #161b22;
    --surface-hover: #1c2129;
    --border: #30363d;
    --text: #e6edf3;
    --text-dim: #8b949e;
    --accent: #58a6ff;
    --green: #3fb950;
    --yellow: #d29922;
    --red: #f85149;
    --orange: #db6d28;
    --purple: #bc8cff;
  }
  * { box-sizing: border-box; margin: 0; padding: 0; }
  body {
    font-family: -apple-system, BlinkMacSystemFont, 'Segoe UI', Helvetica, Arial, sans-serif;
    background: var(--bg);
    color: var(--text);
    font-size: 14px;
    line-height: 1.5;
    padding: 16px;
  }
  header {
    display: flex;
    align-items: center;
    justify-content: space-between;
    margin-bottom: 16px;
    padding-bottom: 12px;
    border-bottom: 1px solid var(--border);
  }
  header h1 {
    font-size: 20px;
    font-weight: 600;
    color: var(--text);
  }
  header h1 span { color: var(--accent); }
  .meta {
    font-size: 12px;
    color: var(--text-dim);
  }
  .meta .live { color: var(--green); }

  /* Layout */
  .grid {
    display: grid;
    grid-template-columns: 1fr 1fr;
    gap: 16px;
  }
  @media (max-width: 900px) { .grid { grid-template-columns: 1fr; } }
  .card {
    background: var(--surface);
    border: 1px solid var(--border);
    border-radius: 8px;
    overflow: hidden;
  }
  .card-header {
    padding: 10px 14px;
    border-bottom: 1px solid var(--border);
    font-weight: 600;
    font-size: 13px;
    text-transform: uppercase;
    letter-spacing: 0.5px;
    color: var(--text-dim);
    display: flex;
    align-items: center;
    gap: 6px;
  }
  .card-header .count {
    font-size: 11px;
    background: var(--border);
    color: var(--text-dim);
    padding: 1px 6px;
    border-radius: 10px;
    margin-left: auto;
  }
  .card-body { padding: 0; }
  .full-width { grid-column: 1 / -1; }

  /* Agent pills */
  .agent-list { display: flex; flex-wrap: wrap; gap: 8px; padding: 12px 14px; }
  .agent-pill {
    display: flex;
    align-items: flex-start;
    gap: 8px;
    padding: 8px 14px;
    background: var(--bg);
    border: 1px solid var(--border);
    border-radius: 8px;
    min-width: 220px;
    flex: 1;
    max-width: 360px;
  }
  .agent-dot {
    width: 10px;
    height: 10px;
    border-radius: 50%;
    flex-shrink: 0;
    margin-top: 4px;
  }
  .agent-dot.online { background: var(--green); box-shadow: 0 0 6px var(--green); }
  .agent-dot.offline { background: var(--text-dim); }
  .agent-dot.working { background: var(--accent); box-shadow: 0 0 6px var(--accent); }
  .agent-dot.away { background: var(--yellow); }
  .agent-name { font-weight: 600; font-size: 13px; }
  .agent-meta { font-size: 11px; color: var(--text-dim); }
  .agent-progress {
    font-size: 11px;
    color: var(--yellow);
    margin-top: 2px;
    font-style: italic;
  }
  .agent-role {
    font-size: 10px;
    padding: 1px 6px;
    border-radius: 4px;
    background: var(--border);
    color: var(--text-dim);
    text-transform: uppercase;
    letter-spacing: 0.3px;
  }
  .agent-role.driver { background: #1f3a5f; color: var(--accent); }

  /* Tables */
  table { width: 100%; border-collapse: collapse; }
  th {
    text-align: left;
    padding: 8px 14px;
    font-size: 11px;
    font-weight: 600;
    color: var(--text-dim);
    text-transform: uppercase;
    letter-spacing: 0.5px;
    border-bottom: 1px solid var(--border);
  }
  td {
    padding: 8px 14px;
    border-bottom: 1px solid var(--border);
    font-size: 13px;
    vertical-align: top;
  }
  tr:last-child td { border-bottom: none; }
  tr:hover { background: var(--surface-hover); }

  /* Status badges */
  .badge {
    display: inline-block;
    padding: 2px 8px;
    border-radius: 12px;
    font-size: 11px;
    font-weight: 600;
    text-transform: uppercase;
    letter-spacing: 0.3px;
  }
  .badge.pending { background: #1f2d3d; color: var(--accent); }
  .badge.in_progress { background: #2a1f0d; color: var(--yellow); }
  .badge.completed { background: #0d2818; color: var(--green); }
  .badge.blocked { background: #2d1a1a; color: var(--red); }
  .badge.cancelled { background: #2d1a1a; color: var(--red); }
  .badge.active { background: #0d2818; color: var(--green); }
  .badge.decision { background: #1f3a5f; color: var(--accent); }
  .badge.note { background: #1f2d3d; color: var(--text-dim); }
  .badge.question { background: #2a1f0d; color: var(--yellow); }
  .badge.blocker { background: #2d1a1a; color: var(--red); }

  /* Priority indicators */
  .priority {
    display: inline-block;
    width: 8px;
    height: 8px;
    border-radius: 2px;
    margin-right: 4px;
  }
  .priority.p1 { background: var(--red); }
  .priority.p2 { background: var(--orange); }
  .priority.p3 { background: var(--text-dim); }
  .priority.p4 { background: var(--border); }

  /* Progress bar */
  .progress-bar {
    width: 100%;
    height: 4px;
    background: var(--border);
    border-radius: 2px;
    margin-top: 4px;
    overflow: hidden;
  }
  .progress-bar .fill {
    height: 100%;
    border-radius: 2px;
    background: var(--accent);
    transition: width 0.3s ease;
  }
  .progress-bar .fill.over { background: var(--red); }
  .progress-text {
    font-size: 11px;
    color: var(--text-dim);
    margin-top: 2px;
  }
  .sla-over { color: var(--red); font-weight: 600; }
  .sla-ok { color: var(--green); }

  /* Messages */
  .msg-list { max-height: 400px; overflow-y: auto; }
  .msg {
    padding: 8px 14px;
    border-bottom: 1px solid var(--border);
    font-size: 13px;
  }
  .msg:last-child { border-bottom: none; }
  .msg:hover { background: var(--surface-hover); }
  .msg-header {
    display: flex;
    align-items: center;
    gap: 6px;
    margin-bottom: 4px;
  }
  .msg-from { font-weight: 600; color: var(--accent); }
  .msg-to { color: var(--text-dim); }
  .msg-time { font-size: 11px; color: var(--text-dim); margin-left: auto; }
  .msg-body {
    color: var(--text);
    white-space: pre-wrap;
    word-break: break-word;
    font-size: 12px;
    max-height: 60px;
    overflow: hidden;
    line-height: 1.4;
  }
  .msg.unread { border-left: 3px solid var(--accent); }

  /* Plan items */
  .plan-items { padding: 8px 14px; }
  .plan-item {
    display: flex;
    align-items: center;
    gap: 8px;
    padding: 4px 0;
    font-size: 13px;
  }
  .plan-item .badge { font-size: 10px; }
  .plan-owner { color: var(--text-dim); font-size: 11px; }

  /* Workers */
  .worker-list { padding: 12px 14px; display: flex; flex-wrap: wrap; gap: 8px; }
  .worker-card {
    background: var(--bg);
    border: 1px solid var(--border);
    border-radius: 8px;
    padding: 10px 14px;
    min-width: 220px;
    flex: 1;
    max-width: 360px;
  }
  .worker-id { font-weight: 600; font-size: 13px; }
  .worker-meta { font-size: 11px; color: var(--text-dim); margin-top: 2px; }
  .worker-progress {
    font-size: 11px;
    color: var(--yellow);
    margin-top: 4px;
    font-style: italic;
  }

  /* Notes list */
  .note-list { max-height: 300px; overflow-y: auto; }
  .note-item {
    padding: 8px 14px;
    border-bottom: 1px solid var(--border);
    font-size: 13px;
  }
  .note-item:last-child { border-bottom: none; }
  .note-item:hover { background: var(--surface-hover); }
  .note-header {
    display: flex;
    align-items: center;
    gap: 6px;
    margin-bottom: 2px;
  }
  .note-author { font-weight: 600; color: var(--accent); font-size: 12px; }
  .note-age { font-size: 11px; color: var(--text-dim); margin-left: auto; }
  .note-body { font-size: 12px; color: var(--text); }

  /* File locks */
  .lock-list { padding: 8px 14px; }
  .lock-item {
    display: flex;
    align-items: center;
    gap: 8px;
    padding: 4px 0;
    font-size: 12px;
  }
  .lock-path { font-family: monospace; color: var(--accent); }
  .lock-owner { color: var(--text-dim); }

  .empty {
    padding: 24px 14px;
    text-align: center;
    color: var(--text-dim);
    font-size: 13px;
  }

  /* Settings bar */
  .settings {
    display: flex;
    align-items: center;
    gap: 12px;
  }
  .settings label {
    font-size: 12px;
    color: var(--text-dim);
  }
  .settings select {
    background: var(--bg);
    color: var(--text);
    border: 1px solid var(--border);
    border-radius: 4px;
    padding: 2px 6px;
    font-size: 12px;
  }

  /* Workspace bar */
  .workspace-bar {
    font-size: 12px;
    color: var(--text-dim);
    font-family: monospace;
    margin-top: 2px;
  }
  .workspace-bar .path { color: var(--accent); }

  /* Buttons */
  .btn {
    font-size: 12px;
    font-weight: 600;
    padding: 4px 12px;
    border-radius: 6px;
    border: 1px solid var(--border);
    cursor: pointer;
    transition: background 0.15s, border-color 0.15s;
  }
  .btn-primary {
    background: #0c2d6b;
    color: var(--accent);
    border-color: #1a3f7a;
  }
  .btn-primary:hover { background: #163d8c; border-color: var(--accent); }
  .btn-primary:disabled { opacity: 0.5; cursor: not-allowed; }
  .btn-warning {
    background: #1a1500;
    color: var(--yellow);
    border-color: #3d3000;
  }
  .btn-warning:hover { background: #2a2200; border-color: var(--yellow); }
  .btn-warning:disabled { opacity: 0.5; cursor: not-allowed; }
  .btn-danger {
    background: #21090d;
    color: var(--red);
    border-color: #49282c;
  }
  .btn-danger:hover { background: #31111a; border-color: var(--red); }
  .btn-secondary {
    background: var(--surface);
    color: var(--text-dim);
    border-color: var(--border);
  }
  .btn-secondary:hover { background: var(--surface-hover); color: var(--text); }

  /* Modal */
  .modal-overlay {
    display: none;
    position: fixed;
    inset: 0;
    background: rgba(0,0,0,0.6);
    z-index: 100;
    align-items: center;
    justify-content: center;
  }
  .modal-overlay.open { display: flex; }
  .modal {
    background: var(--surface);
    border: 1px solid var(--border);
    border-radius: 12px;
    padding: 24px;
    max-width: 420px;
    width: 90%;
    box-shadow: 0 8px 32px rgba(0,0,0,0.4);
  }
  .modal h2 {
    font-size: 16px;
    margin-bottom: 8px;
    color: var(--red);
  }
  .modal p {
    font-size: 13px;
    color: var(--text-dim);
    margin-bottom: 16px;
    line-height: 1.5;
  }
  .modal-option {
    display: flex;
    align-items: center;
    gap: 8px;
    margin-bottom: 8px;
    font-size: 13px;
    color: var(--text);
  }
  .modal-option input[type="checkbox"] {
    accent-color: var(--accent);
  }
  .modal-actions {
    display: flex;
    gap: 8px;
    justify-content: flex-end;
    margin-top: 20px;
  }
</style>
</head>
<body>
<header>
  <div>
    <h1><svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 128 128" fill="none" width="28" height="28" style="vertical-align: middle; margin-right: 6px"><path d="M64 64C53 55 38 48 24 38" stroke="#4F46E5" stroke-width="4" stroke-linecap="round"/><path d="M64 64C74 50 90 39 105 31" stroke="#4F46E5" stroke-width="4" stroke-linecap="round"/><path d="M64 64C78 70 96 86 102 103" stroke="#4F46E5" stroke-width="4" stroke-linecap="round"/><path d="M64 64C55 79 42 93 29 104" stroke="#4F46E5" stroke-width="4" stroke-linecap="round"/><circle cx="64" cy="64" r="8" fill="#4F46E5" stroke="#F8FAFC" stroke-width="2"/><circle cx="64" cy="64" r="2" fill="#F8FAFC"/><circle cx="24" cy="38" r="5" fill="#14B8A6" stroke="#F8FAFC" stroke-width="2"/><circle cx="105" cy="31" r="5" fill="#14B8A6" stroke="#F8FAFC" stroke-width="2"/><circle cx="102" cy="103" r="5" fill="#14B8A6" stroke="#F8FAFC" stroke-width="2"/><circle cx="29" cy="104" r="5" fill="#14B8A6" stroke="#F8FAFC" stroke-width="2"/></svg> Stringwork</h1>
    <div class="workspace-bar" id="workspace-bar"></div>
  </div>
  <div class="settings">
    <label>Refresh:
      <select id="interval" onchange="setInterval_()">
        <option value="2000">2s</option>
        <option value="5000" selected>5s</option>
        <option value="10000">10s</option>
        <option value="0">Off</option>
      </select>
    </label>
    <button class="btn btn-primary" onclick="showSwitchModal()">Switch Project</button>
    <button class="btn btn-warning" id="restart-btn" onclick="restartWorkers()">Restart Workers</button>
    <button class="btn btn-danger" onclick="showResetModal()">Reset State</button>
    <span class="meta">Updated: <span id="updated" class="live">-</span></span>
  </div>
</header>

<!-- Reset confirmation modal -->
<div class="modal-overlay" id="reset-modal">
  <div class="modal">
    <h2>Reset State</h2>
    <p>This will clear all tasks, messages, plans, notes, and file locks. This cannot be undone.</p>
    <label class="modal-option">
      <input type="checkbox" id="reset-keep-agents" checked>
      Keep agent presence (recommended)
    </label>
    <div class="modal-actions">
      <button class="btn btn-secondary" onclick="hideResetModal()">Cancel</button>
      <button class="btn btn-danger" id="reset-confirm-btn" onclick="doReset()">Reset Everything</button>
    </div>
  </div>
</div>
<!-- Switch Project modal -->
<div class="modal-overlay" id="switch-modal">
  <div class="modal">
    <h2 style="color:var(--accent)">Switch Project</h2>
    <p>This will cancel all running workers, clear all tasks, messages, and plans, then set the new workspace. Agents stay registered.</p>
    <label class="modal-option" style="flex-direction:column;align-items:stretch;gap:4px">
      <span>New workspace path:</span>
      <input type="text" id="switch-workspace" placeholder="/path/to/project" style="width:100%;padding:6px 10px;background:var(--bg);color:var(--text);border:1px solid var(--border);border-radius:6px;font-size:13px;font-family:monospace">
    </label>
    <div class="modal-actions">
      <button class="btn btn-secondary" onclick="hideSwitchModal()">Cancel</button>
      <button class="btn btn-primary" id="switch-confirm-btn" onclick="doSwitchProject()">Switch Project</button>
    </div>
  </div>
</div>

<div class="grid">
  <!-- Row 1: Agents (full width) -->
  <div class="card full-width" id="agents-card">
    <div class="card-header">&#128101; Agents <span class="count" id="agents-count">0</span></div>
    <div class="card-body"><div class="agent-list" id="agents"></div></div>
  </div>

  <!-- Row 2: Workers (full width, only shown when workers exist) -->
  <div class="card full-width" id="workers-card" style="display:none">
    <div class="card-header">&#9881; Workers <span class="count" id="workers-count">0</span></div>
    <div class="card-body"><div class="worker-list" id="workers"></div></div>
  </div>

  <!-- Row 3: Tasks (full width) -->
  <div class="card full-width" id="tasks-card">
    <div class="card-header">&#9745; Tasks <span class="count" id="tasks-count">0</span></div>
    <div class="card-body" id="tasks"></div>
  </div>

  <!-- Row 4: Messages + Plans/Notes/Locks -->
  <div class="card" id="messages-card">
    <div class="card-header">&#128172; Messages <span class="count" id="messages-count">0</span></div>
    <div class="card-body msg-list" id="messages"></div>
  </div>

  <div class="card" id="side-card">
    <div class="card-header" id="side-header">&#128203; Plans</div>
    <div class="card-body" id="side-body"></div>
  </div>
</div>

<script>
let timer = null;
let refreshMs = 5000;

function setInterval_() {
  refreshMs = parseInt(document.getElementById('interval').value);
  if (timer) clearInterval(timer);
  if (refreshMs > 0) timer = setInterval(fetchState, refreshMs);
}

function statusDotClass(status, connected) {
  if (!connected) return 'offline';
  if (status === 'working') return 'working';
  if (status === 'away') return 'away';
  return 'online';
}

function renderAgents(agents) {
  const el = document.getElementById('agents');
  document.getElementById('agents-count').textContent = agents ? agents.length : 0;
  if (!agents || agents.length === 0) {
    el.innerHTML = '<div class="empty">No agents registered</div>';
    return;
  }
  el.innerHTML = agents.map(a => {
    const dotCls = statusDotClass(a.status, a.connected);
    const roleClass = a.role === 'driver' ? ' driver' : '';
    const roleBadge = a.role ? '<span class="agent-role' + roleClass + '">' + esc(a.role) + '</span>' : '';
    const meta = [a.last_seen || '', a.note || ''].filter(Boolean).join(' · ');

    let progressHTML = '';
    if (a.progress) {
      let stepInfo = '';
      if (a.progress_total_steps > 0) {
        stepInfo = ' [' + a.progress_step + '/' + a.progress_total_steps + ']';
      }
      const age = a.progress_age ? ' (' + a.progress_age + ')' : '';
      progressHTML = '<div class="agent-progress">' + esc(a.progress) + esc(stepInfo) + esc(age) + '</div>';
    }

    return '<div class="agent-pill">' +
      '<div class="agent-dot ' + dotCls + '"></div>' +
      '<div>' +
        '<div class="agent-name">' + esc(a.name) + ' ' + roleBadge + '</div>' +
        '<div class="agent-meta">' + esc(a.status || 'unknown') +
          (a.workspace ? ' · ' + esc(shortPath(a.workspace)) : '') +
          (a.last_heartbeat ? ' · HB: ' + esc(a.last_heartbeat) : '') +
          (meta ? ' · ' + esc(meta) : '') +
        '</div>' +
        progressHTML +
      '</div>' +
    '</div>';
  }).join('');
}

function renderWorkers(workers) {
  const card = document.getElementById('workers-card');
  const el = document.getElementById('workers');
  document.getElementById('workers-count').textContent = workers ? workers.length : 0;
  if (!workers || workers.length === 0) {
    card.style.display = 'none';
    return;
  }
  card.style.display = '';
  el.innerHTML = workers.map(w => {
    let progressHTML = '';
    if (w.progress) {
      let stepInfo = '';
      if (w.progress_total_steps > 0) {
        stepInfo = ' [step ' + w.progress_step + '/' + w.progress_total_steps + ']';
      }
      const age = w.progress_age ? ' (' + w.progress_age + ')' : '';
      progressHTML = '<div class="worker-progress">' + esc(w.progress) + esc(stepInfo) + esc(age) + '</div>';
    }
    return '<div class="worker-card">' +
      '<div class="worker-id">' + esc(w.instance_id) + ' <span class="badge ' + w.status + '">' + esc(w.status) + '</span></div>' +
      '<div class="worker-meta">Type: ' + esc(w.agent_type) + ' · HB: ' + esc(w.last_heartbeat) + '</div>' +
      (w.current_tasks && w.current_tasks.length ? '<div class="worker-meta">Tasks: ' + w.current_tasks.map(id => '#' + id).join(', ') + '</div>' : '') +
      progressHTML +
    '</div>';
  }).join('');
}

function renderTasks(tasks) {
  const el = document.getElementById('tasks');
  document.getElementById('tasks-count').textContent = tasks ? tasks.length : 0;
  if (!tasks || tasks.length === 0) {
    el.innerHTML = '<div class="empty">No tasks</div>';
    return;
  }
  let html = '<table><thead><tr><th>ID</th><th></th><th>Title</th><th>Status</th><th>Progress</th><th>Assignee</th><th>Creator</th><th>Age</th></tr></thead><tbody>';
  tasks.forEach(t => {
    let progressCol = '';
    if (t.status === 'in_progress') {
      if (t.progress_percent > 0) {
        const barClass = t.sla_status === 'over' ? ' over' : '';
        progressCol += '<div class="progress-bar"><div class="fill' + barClass + '" style="width:' + t.progress_percent + '%"></div></div>';
        progressCol += '<div class="progress-text">' + t.progress_percent + '%';
        if (t.last_progress_age) progressCol += ' · ' + esc(t.last_progress_age);
        progressCol += '</div>';
      } else if (t.last_progress_age) {
        progressCol += '<div class="progress-text">Last: ' + esc(t.last_progress_age) + '</div>';
      }
      if (t.sla_status === 'over') {
        progressCol += '<div class="sla-over">SLA OVER</div>';
      } else if (t.sla_status === 'ok') {
        progressCol += '<div class="sla-ok" style="font-size:11px">SLA OK</div>';
      }
      if (t.progress_description) {
        progressCol += '<div class="progress-text" style="max-width:200px;overflow:hidden;text-overflow:ellipsis;white-space:nowrap" title="' + escAttr(t.progress_description) + '">' + esc(t.progress_description) + '</div>';
      }
    } else if (t.status === 'completed' && t.result_summary) {
      progressCol = '<span style="font-size:11px;color:var(--text-dim)">' + esc(t.result_summary) + '</span>';
    }

    html += '<tr>' +
      '<td>#' + t.id + '</td>' +
      '<td><span class="priority p' + t.priority + '"></span></td>' +
      '<td>' + esc(t.title) + '</td>' +
      '<td><span class="badge ' + t.status + '">' + esc(t.status) + '</span></td>' +
      '<td>' + progressCol + '</td>' +
      '<td>' + esc(t.assigned_to || '-') + '</td>' +
      '<td>' + esc(t.created_by || '-') + '</td>' +
      '<td style="white-space:nowrap;color:var(--text-dim)">' + esc(t.age) + '</td>' +
    '</tr>';
  });
  html += '</tbody></table>';
  el.innerHTML = html;
}

function renderMessages(messages) {
  const el = document.getElementById('messages');
  document.getElementById('messages-count').textContent = messages ? messages.length : 0;
  if (!messages || messages.length === 0) {
    el.innerHTML = '<div class="empty">No messages</div>';
    return;
  }
  el.innerHTML = messages.map(m => {
    const unread = m.read ? '' : ' unread';
    return '<div class="msg' + unread + '">' +
      '<div class="msg-header">' +
        '<span class="msg-from">' + esc(m.from) + '</span>' +
        '<span class="msg-to">&#8594; ' + esc(m.to) + '</span>' +
        '<span class="msg-time">' + esc(m.timestamp) + ' (' + esc(m.age) + ')</span>' +
      '</div>' +
      '<div class="msg-body">' + esc(m.content) + '</div>' +
    '</div>';
  }).join('');
}

function renderSide(data) {
  const header = document.getElementById('side-header');
  const body = document.getElementById('side-body');

  let html = '';
  const sections = [];

  // Plans
  if (data.plans && data.plans.length > 0) {
    sections.push('Plans');
    data.plans.forEach(p => {
      html += '<div class="plan-items"><div style="font-weight:600;margin-bottom:4px">' + esc(p.title) +
        ' <span class="badge ' + p.status + '">' + esc(p.status) + '</span></div>';
      if (p.items) {
        p.items.forEach(item => {
          html += '<div class="plan-item">' +
            '<span class="badge ' + item.status + '">' + esc(item.status) + '</span> ' +
            esc(item.title) +
            (item.owner ? ' <span class="plan-owner">(' + esc(item.owner) + ')</span>' : '') +
          '</div>';
        });
      }
      html += '</div>';
    });
  }

  // Session Notes
  if (data.session_notes && data.session_notes.length > 0) {
    sections.push('Notes');
    html += '<div style="border-top:1px solid var(--border)">';
    html += '<div style="padding:10px 14px 4px 14px;font-size:11px;font-weight:600;color:var(--text-dim);text-transform:uppercase">Session Notes</div>';
    html += '<div class="note-list">';
    data.session_notes.forEach(n => {
      html += '<div class="note-item">' +
        '<div class="note-header">' +
          '<span class="note-author">' + esc(n.author) + '</span>' +
          '<span class="badge ' + n.category + '">' + esc(n.category) + '</span>' +
          '<span class="note-age">' + esc(n.age) + '</span>' +
        '</div>' +
        '<div class="note-body">' + esc(n.content) + '</div>' +
      '</div>';
    });
    html += '</div></div>';
  }

  // File Locks
  if (data.file_locks && data.file_locks.length > 0) {
    sections.push('Locks');
    html += '<div style="border-top:1px solid var(--border)">';
    html += '<div style="padding:10px 14px 4px 14px;font-size:11px;font-weight:600;color:var(--text-dim);text-transform:uppercase">File Locks</div>';
    html += '<div class="lock-list">';
    data.file_locks.forEach(l => {
      html += '<div class="lock-item">' +
        '<span class="lock-path">' + esc(l.path) + '</span>' +
        '<span class="lock-owner">by ' + esc(l.locked_by) + '</span>' +
        '<span style="color:var(--text-dim);font-size:11px">' + esc(l.age) + ' · ' + esc(l.expires) + '</span>' +
      '</div>';
    });
    html += '</div></div>';
  }

  header.innerHTML = '&#128203; ' + (sections.length > 0 ? sections.join(' / ') : 'Plans');
  body.innerHTML = html || '<div class="empty">No plans, notes, or locks</div>';
}

function shortPath(p) {
  const parts = p.split('/');
  return parts.length > 2 ? '.../' + parts.slice(-2).join('/') : p;
}

function esc(s) {
  if (!s) return '';
  const d = document.createElement('div');
  d.textContent = s;
  return d.innerHTML;
}

function escAttr(s) {
  if (!s) return '';
  return s.replace(/&/g,'&amp;').replace(/"/g,'&quot;').replace(/</g,'&lt;').replace(/>/g,'&gt;');
}

async function fetchState() {
  try {
    const resp = await fetch('/api/state');
    if (!resp.ok) return;
    const data = await resp.json();

    document.getElementById('updated').textContent = new Date().toLocaleTimeString();

    const wsBar = document.getElementById('workspace-bar');
    if (data.workspace) {
      wsBar.innerHTML = '<span class="path">' + esc(data.workspace) + '</span>';
      wsBar.dataset.workspace = data.workspace;
    } else {
      wsBar.innerHTML = '<span style="color:var(--text-dim)">no workspace set</span>';
      wsBar.dataset.workspace = '';
    }

    renderAgents(data.agents);
    renderWorkers(data.workers);
    renderTasks(data.tasks);
    renderMessages(data.messages);
    renderSide(data);
  } catch (e) {
    document.getElementById('updated').textContent = 'error';
    document.getElementById('updated').style.color = 'var(--red)';
    setTimeout(() => { document.getElementById('updated').style.color = ''; }, 2000);
  }
}

function showSwitchModal() {
  document.getElementById('switch-modal').classList.add('open');
  const input = document.getElementById('switch-workspace');
  const bar = document.getElementById('workspace-bar');
  const current = bar.dataset.workspace || '';
  if (current) input.value = current;
  input.focus();
  input.select();
}
function hideSwitchModal() {
  document.getElementById('switch-modal').classList.remove('open');
}
async function doSwitchProject() {
  const btn = document.getElementById('switch-confirm-btn');
  const workspace = document.getElementById('switch-workspace').value.trim();
  if (!workspace) { alert('Please enter a workspace path'); return; }
  btn.textContent = 'Switching...';
  btn.disabled = true;
  try {
    const resp = await fetch('/api/switch-project?workspace=' + encodeURIComponent(workspace), { method: 'POST' });
    const data = await resp.json();
    if (!resp.ok) {
      alert('Switch failed: ' + (data.error || resp.statusText));
      return;
    }
    hideSwitchModal();
    fetchState();
  } catch (e) {
    alert('Switch failed: ' + e.message);
  } finally {
    btn.textContent = 'Switch Project';
    btn.disabled = false;
  }
}
document.getElementById('switch-modal').addEventListener('click', function(e) {
  if (e.target === this) hideSwitchModal();
});
document.getElementById('switch-workspace').addEventListener('keydown', function(e) {
  if (e.key === 'Enter') doSwitchProject();
  if (e.key === 'Escape') hideSwitchModal();
});

async function restartWorkers() {
  const btn = document.getElementById('restart-btn');
  const origText = btn.textContent;
  btn.textContent = 'Restarting...';
  btn.disabled = true;
  try {
    const resp = await fetch('/api/restart-workers', { method: 'POST' });
    const data = await resp.json();
    if (!resp.ok) {
      alert('Restart failed: ' + (data.error || resp.statusText));
      return;
    }
    const killed = (data.killed && data.killed.length) ? data.killed.join(', ') : 'none running';
    btn.textContent = 'Restarted!';
    setTimeout(() => { btn.textContent = origText; btn.disabled = false; }, 2000);
    fetchState();
  } catch (e) {
    alert('Restart failed: ' + e.message);
    btn.textContent = origText;
    btn.disabled = false;
  }
}

function showResetModal() {
  document.getElementById('reset-modal').classList.add('open');
}
function hideResetModal() {
  document.getElementById('reset-modal').classList.remove('open');
}
async function doReset() {
  const btn = document.getElementById('reset-confirm-btn');
  const keepAgents = document.getElementById('reset-keep-agents').checked;
  btn.textContent = 'Resetting...';
  btn.disabled = true;
  try {
    const url = '/api/reset' + (keepAgents ? '?keep_agents=true' : '');
    const resp = await fetch(url, { method: 'POST' });
    if (!resp.ok) {
      const data = await resp.json();
      alert('Reset failed: ' + (data.error || resp.statusText));
      return;
    }
    hideResetModal();
    fetchState();
  } catch (e) {
    alert('Reset failed: ' + e.message);
  } finally {
    btn.textContent = 'Reset Everything';
    btn.disabled = false;
  }
}
document.getElementById('reset-modal').addEventListener('click', function(e) {
  if (e.target === this) hideResetModal();
});

fetchState();
timer = setInterval(fetchState, refreshMs);
</script>
</body>
</html>`
