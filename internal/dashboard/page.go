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
  .badge.idle { background: #1f2d3d; color: var(--text-dim); }
  .badge.busy { background: #2a1f0d; color: var(--yellow); }
  .badge.offline { background: #21090d; color: var(--text-dim); }
  .badge.working { background: #0c2d6b; color: var(--accent); }
  .badge.task-bound { background: #2a1542; color: var(--purple); border: 1px solid #3d1f5e; }
  .badge.recovered { background: #2a2200; color: var(--orange); border: 1px solid #3d3000; }

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

  /* Recovery event timeline (reconciler / watchdog actions on a task).
     Rendered as a <details> block so the row stays compact by default
     and operators can expand to see the full sequence. */
  .recovery-events { margin-top: 4px; }
  .recovery-events summary {
    font-size: 11px;
    color: var(--text-dim);
    cursor: pointer;
    user-select: none;
    list-style: none;
  }
  .recovery-events summary::-webkit-details-marker { display: none; }
  .recovery-events summary::before {
    content: '▸';
    display: inline-block;
    margin-right: 4px;
    transition: transform 0.1s;
  }
  .recovery-events[open] summary::before { transform: rotate(90deg); }
  .recovery-events ol {
    margin: 4px 0 0 0;
    padding: 0 0 0 14px;
    font-size: 11px;
    color: var(--text-dim);
    list-style: none;
    border-left: 1px solid var(--border);
  }
  .recovery-events li {
    padding: 2px 0 2px 6px;
    line-height: 1.4;
  }
  .recovery-events .ev-source {
    display: inline-block;
    min-width: 70px;
    color: var(--text-dim);
    text-transform: uppercase;
    font-size: 10px;
    letter-spacing: 0.3px;
  }
  .recovery-events .ev-source.reconciler { color: var(--accent); }
  .recovery-events .ev-source.watchdog { color: var(--orange); }
  .recovery-events .ev-source.auto_cancel { color: var(--red); }
  .recovery-events .ev-age { color: var(--text-dim); margin-left: 4px; }

  /* Messages */
  .msg-list { max-height: 600px; overflow-y: auto; }
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
    max-height: 100px;
    overflow: hidden;
    line-height: 1.4;
    cursor: pointer;
    position: relative;
  }
  .msg-body.truncated::after {
    content: '';
    position: absolute;
    bottom: 0;
    left: 0;
    right: 0;
    height: 20px;
    background: linear-gradient(transparent, var(--surface));
    pointer-events: none;
  }
  .msg-body.expanded {
    max-height: none;
    overflow: visible;
  }
  .msg-body.expanded::after { display: none; }
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
  .agent-pill.stale, .worker-card.stale { opacity: 0.5; }
  .agent-pill.delivery-grace, .worker-card.delivery-grace {
    border-color: #3d3000;
    background: linear-gradient(180deg, rgba(210,153,34,0.06), var(--bg));
  }
  .agent-dot.delivery-grace { background: var(--yellow); box-shadow: 0 0 6px var(--yellow); }
  .msg.recovered {
    background: rgba(219,109,40,0.04);
    border-left: 3px solid var(--orange);
  }
  .msg.recovered .msg-from { color: var(--orange); }
  .agent-tasks {
    font-size: 11px;
    color: var(--text-dim);
    margin-top: 2px;
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
  .modal.wide { max-width: 560px; }
  .modal label { display: block; font-size: 12px; color: var(--text-dim); margin: 8px 0 4px; }
  .modal input[type="text"],
  .modal input[type="number"],
  .modal select,
  .modal textarea {
    width: 100%;
    background: var(--bg);
    color: var(--text);
    border: 1px solid var(--border);
    border-radius: 6px;
    padding: 6px 10px;
    font-size: 13px;
    font-family: inherit;
  }
  .modal textarea { font-family: monospace; resize: vertical; min-height: 80px; }
  .modal-result {
    margin-top: 12px;
    padding: 8px 12px;
    background: var(--bg);
    border: 1px solid var(--border);
    border-radius: 6px;
    font-size: 12px;
    font-family: monospace;
    color: var(--text-dim);
    max-height: 200px;
    overflow-y: auto;
    white-space: pre-wrap;
    word-break: break-word;
  }
  .modal-result.error { color: var(--red); border-color: #49282c; }

  /* Worker card menu */
  .worker-card { position: relative; }
  .worker-menu {
    position: absolute;
    top: 8px;
    right: 8px;
    width: 24px;
    height: 24px;
    border-radius: 4px;
    background: transparent;
    border: 0;
    color: var(--text-dim);
    cursor: pointer;
    font-size: 16px;
    line-height: 1;
    padding: 0;
  }
  .worker-menu:hover { background: var(--surface-hover); color: var(--text); }

  /* Toast */
  .toast {
    position: fixed;
    bottom: 24px;
    right: 24px;
    background: var(--surface);
    border: 1px solid var(--border);
    border-left: 3px solid var(--accent);
    border-radius: 6px;
    padding: 12px 16px;
    font-size: 13px;
    color: var(--text);
    box-shadow: 0 4px 16px rgba(0,0,0,0.4);
    z-index: 200;
    max-width: 420px;
    opacity: 0;
    transform: translateY(8px);
    transition: opacity 0.2s, transform 0.2s;
  }
  .toast.open { opacity: 1; transform: translateY(0); }
  .toast.error { border-left-color: var(--red); }
  .toast.success { border-left-color: var(--green); }
  .toast .toast-title { font-weight: 600; margin-bottom: 4px; }
  .toast .toast-detail { color: var(--text-dim); font-size: 12px; word-break: break-word; }

  /* GC stats strip */
  .gc-strip {
    padding: 6px 14px;
    font-size: 11px;
    color: var(--text-dim);
    background: var(--surface);
    border: 1px solid var(--border);
    border-radius: 6px;
    margin-bottom: 8px;
    display: flex;
    align-items: center;
    gap: 12px;
    flex-wrap: wrap;
  }
  .gc-strip .gc-label { font-weight: 600; color: var(--text); }
  .gc-strip .gc-sep { color: var(--border); }

  /* Pool status panel */
  .pool-panel { padding: 12px 14px; }
  .pool-grid {
    display: grid;
    grid-template-columns: repeat(auto-fit, minmax(180px, 1fr));
    gap: 12px;
    margin-bottom: 12px;
  }
  .pool-stat {
    background: var(--bg);
    border: 1px solid var(--border);
    border-radius: 6px;
    padding: 8px 10px;
  }
  .pool-stat .pool-label {
    font-size: 10px;
    color: var(--text-dim);
    text-transform: uppercase;
    letter-spacing: 0.3px;
    margin-bottom: 4px;
  }
  .pool-stat .pool-value { font-size: 18px; font-weight: 600; color: var(--text); }
  .pool-stat .pool-detail { font-size: 11px; color: var(--text-dim); margin-top: 2px; }
  .pool-detail .breakdown { font-family: monospace; }
  .pool-section-header {
    font-size: 11px;
    color: var(--text-dim);
    text-transform: uppercase;
    margin: 8px 0 4px;
    font-weight: 600;
  }
  .pool-card-toggle {
    margin-left: auto;
    background: transparent;
    border: 0;
    color: var(--text-dim);
    cursor: pointer;
    font-size: 11px;
    padding: 0;
  }
  .pool-card-toggle:hover { color: var(--text); }

  /* Send message inline form */
  .send-form {
    padding: 10px 14px;
    border-bottom: 1px solid var(--border);
    background: var(--surface-hover);
    display: none;
  }
  .send-form.open { display: block; }
  .send-form .send-row { display: flex; gap: 8px; margin-bottom: 8px; align-items: center; }
  .send-form .send-row label { margin: 0; min-width: 40px; }
  .send-form select { flex: 1; }
  .send-form textarea { width: 100%; min-height: 60px; }
  .send-form .send-actions { display: flex; gap: 6px; justify-content: flex-end; margin-top: 6px; }
  .send-form .send-from {
    font-size: 11px;
    color: var(--text-dim);
    font-family: monospace;
  }
  .header-action {
    margin-left: auto;
    background: transparent;
    border: 1px solid var(--border);
    border-radius: 4px;
    color: var(--text-dim);
    cursor: pointer;
    font-size: 11px;
    padding: 2px 8px;
  }
  .header-action:hover { color: var(--text); border-color: var(--accent); }
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
    <button class="btn btn-secondary" onclick="showPruneModal()">Prune…</button>
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

<!-- Cancel agent modal -->
<div class="modal-overlay" id="cancel-modal">
  <div class="modal">
    <h2>Cancel Agent</h2>
    <p>Cancel all in-progress tasks for <strong id="cancel-target"></strong>, kill the worker process, and (if applicable) recover any buffered output as a synthetic message.</p>
    <label>Reason (optional)</label>
    <input type="text" id="cancel-reason" placeholder="e.g. stuck, no longer needed">
    <div class="modal-actions">
      <button class="btn btn-secondary" onclick="hideCancelModal()">Close</button>
      <button class="btn btn-danger" id="cancel-confirm-btn" onclick="doCancelAgent()">Cancel agent</button>
    </div>
    <div class="modal-result" id="cancel-result" style="display:none"></div>
  </div>
</div>

<!-- Prune modal -->
<div class="modal-overlay" id="prune-modal">
  <div class="modal wide">
    <h2 style="color:var(--accent)">Prune Stale State</h2>
    <p>Garbage-collect old presence rows and offline worker instances. Dry-run is on by default — uncheck to commit the deletion.</p>
    <label class="modal-option"><input type="checkbox" id="prune-presence" checked> Presence (offline registrations)</label>
    <label class="modal-option"><input type="checkbox" id="prune-instances" checked> Agent instances (offline workers)</label>
    <div style="display:grid;grid-template-columns:1fr 1fr;gap:12px;margin-top:8px">
      <div>
        <label>Older than (days)</label>
        <input type="number" id="prune-older-days" min="0" value="7">
      </div>
      <div>
        <label>Task-bound older than (hours)</label>
        <input type="number" id="prune-task-bound-hours" min="0" value="24">
      </div>
    </div>
    <label class="modal-option" style="margin-top:8px">
      <input type="checkbox" id="prune-dry-run" checked> Dry run (preview only — recommended)
    </label>
    <div class="modal-actions">
      <button class="btn btn-secondary" onclick="hidePruneModal()">Close</button>
      <button class="btn btn-primary" id="prune-run-btn" onclick="doPrune()">Run</button>
    </div>
    <div class="modal-result" id="prune-result" style="display:none"></div>
  </div>
</div>

<!-- Toast -->
<div class="toast" id="toast" role="status" aria-live="polite"></div>

<!-- GC stats strip (populated when /api/state.gc is set) -->
<div class="gc-strip" id="gc-strip" style="display:none"></div>

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

  <!-- Row 2b: Pool status (mirrors mcp-stringwork admin pool-status) -->
  <div class="card full-width" id="pool-card" style="display:none">
    <div class="card-header">&#128202; Pool Status
      <button class="pool-card-toggle" id="pool-toggle" onclick="togglePoolPanel()">collapse</button>
    </div>
    <div class="card-body pool-panel" id="pool-body"></div>
  </div>

  <!-- Row 3: Tasks (full width) -->
  <div class="card full-width" id="tasks-card">
    <div class="card-header">&#9745; Tasks <span class="count" id="tasks-count">0</span></div>
    <div class="card-body" id="tasks"></div>
  </div>

  <!-- Row 4: Messages + Plans/Notes/Locks -->
  <div class="card" id="messages-card">
    <div class="card-header">&#128172; Messages <span class="count" id="messages-count">0</span>
      <button class="header-action" id="send-toggle" onclick="toggleSendForm()">Send Message</button>
    </div>
    <div class="send-form" id="send-form">
      <div class="send-row">
        <label>From:</label>
        <span class="send-from" id="send-from-display">(driver)</span>
      </div>
      <div class="send-row">
        <label>To:</label>
        <select id="send-to"></select>
      </div>
      <textarea id="send-content" placeholder="Message body…"></textarea>
      <div class="send-actions">
        <button class="btn btn-secondary" onclick="toggleSendForm(false)">Cancel</button>
        <button class="btn btn-primary" id="send-confirm-btn" onclick="doSendMessage()">Send</button>
      </div>
    </div>
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

function statusDotClass(status, reachable) {
  if (!reachable) return 'offline';
  if (status === 'working' || status === 'busy') return 'working';
  if (status === 'away') return 'away';
  return 'online';
}

// progressAgeSec parses the relative-time strings the API returns
// ("3m ago", "45s ago", "2h ago", "just now", "never", or absolute date)
// and returns the age in seconds. Used to suppress stale progress lines on
// offline rows once the watchdog has stopped updating them. Returns
// Infinity for "never" or absolute dates so the >120s suppression rule
// always trips for anything older than a day.
function progressAgeSec(s) {
  if (!s) return Infinity;
  s = String(s).trim();
  if (s === 'just now') return 0;
  if (s === 'never') return Infinity;
  const m = s.match(/^(\d+)\s*([smh])\s*ago$/);
  if (!m) return Infinity;
  const n = parseInt(m[1], 10);
  switch (m[2]) {
    case 's': return n;
    case 'm': return n * 60;
    case 'h': return n * 3600;
    default: return Infinity;
  }
}

function renderAgents(agents) {
  const el = document.getElementById('agents');
  document.getElementById('agents-count').textContent = agents ? agents.length : 0;
  if (!agents || agents.length === 0) {
    el.innerHTML = '<div class="empty">No agents registered</div>';
    return;
  }
  el.innerHTML = agents.map(a => {
    const dotCls = a.in_delivery_grace ? 'delivery-grace' : statusDotClass(a.status, a.reachable);
    const classes = [];
    if (!a.reachable) classes.push('stale');
    if (a.in_delivery_grace) classes.push('delivery-grace');
    const cardClass = classes.length ? ' ' + classes.join(' ') : '';
    const roleClass = a.role === 'driver' ? ' driver' : '';
    const roleBadge = a.role ? '<span class="agent-role' + roleClass + '">' + esc(a.role) + '</span>' : '';
    const meta = [a.last_seen || '', a.note || ''].filter(Boolean).join(' · ');
    const dotTitle = a.in_delivery_grace
      ? 'Delivery grace — last send ' + (a.last_send_age || 'just now')
      : (a.reachable ? '' : 'offline');
    const dotAttr = dotTitle ? ' title="' + escAttr(dotTitle) + '"' : '';
    const statusText = a.reachable ? esc(a.status || 'unknown') : 'offline';

    // Suppress stale progress lines on offline rows once they're >2min old —
    // matches the backend rule in worker_status.go so the dashboard doesn't
    // display ghost progress from days-old sessions.
    let progressHTML = '';
    const showProgress = !!a.progress && (a.reachable || progressAgeSec(a.progress_age) <= 120);
    if (showProgress) {
      let stepInfo = '';
      if (a.progress_total_steps > 0) {
        stepInfo = ' [' + a.progress_step + '/' + a.progress_total_steps + ']';
      }
      const age = a.progress_age ? ' (' + a.progress_age + ')' : '';
      progressHTML = '<div class="agent-progress">' + esc(a.progress) + esc(stepInfo) + esc(age) + '</div>';
    }

    let tasksHTML = '';
    if (a.current_tasks && a.current_tasks.length) {
      tasksHTML = '<div class="agent-tasks">Tasks: ' + a.current_tasks.map(id => '#' + id).join(', ') + '</div>';
    }

    return '<div class="agent-pill' + cardClass + '">' +
      '<div class="agent-dot ' + dotCls + '"' + dotAttr + '></div>' +
      '<div>' +
        '<div class="agent-name">' + esc(a.name) + ' ' + roleBadge + '</div>' +
        '<div class="agent-meta">' + statusText +
          (a.workspace ? ' · ' + esc(shortPath(a.workspace)) : '') +
          (a.last_heartbeat ? ' · HB: ' + esc(a.last_heartbeat) : '') +
          (meta ? ' · ' + esc(meta) : '') +
        '</div>' +
        progressHTML +
        tasksHTML +
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

  // Group: pool workers (sorted by id) followed by their task-bound children
  // so the new domain concept ("a claude-code pool plus its task-bound
  // siblings") is legible. Backend sorts strictly by instance_id; we
  // re-bucket here without losing within-bucket ordering.
  const sorted = [...workers].sort((a, b) => {
    if (a.agent_type !== b.agent_type) return a.agent_type.localeCompare(b.agent_type);
    if (a.is_task_bound !== b.is_task_bound) return a.is_task_bound ? 1 : -1;
    return a.instance_id.localeCompare(b.instance_id);
  });

  el.innerHTML = sorted.map(w => {
    const dotCls = w.in_delivery_grace ? 'delivery-grace' : statusDotClass(w.status, w.reachable);
    const classes = [];
    if (!w.reachable) classes.push('stale');
    if (w.in_delivery_grace) classes.push('delivery-grace');
    const cardClass = classes.length ? ' ' + classes.join(' ') : '';
    const dotTitle = w.in_delivery_grace
      ? 'Delivery grace — last send ' + (w.last_send_age || 'just now')
      : (w.reachable ? '' : 'offline');
    const dotAttr = dotTitle ? ' title="' + escAttr(dotTitle) + '"' : '';
    const statusBadge = w.reachable
      ? '<span class="badge ' + w.status + '">' + esc(w.status) + '</span>'
      : '<span class="badge offline">offline</span>';
    const taskBoundBadge = w.is_task_bound
      ? ' <span class="badge task-bound" title="Task-bound worker — lifetime tied to task #' + (w.bound_task_id || '?') + '">task-' + (w.bound_task_id || '?') + '</span>'
      : '';

    let progressHTML = '';
    const showProgress = !!w.progress && (w.reachable || progressAgeSec(w.progress_age) <= 120);
    if (showProgress) {
      let stepInfo = '';
      if (w.progress_total_steps > 0) {
        stepInfo = ' [step ' + w.progress_step + '/' + w.progress_total_steps + ']';
      }
      const age = w.progress_age ? ' (' + w.progress_age + ')' : '';
      progressHTML = '<div class="worker-progress">' + esc(w.progress) + esc(stepInfo) + esc(age) + '</div>';
    }
    const cancelBtn = '<button class="worker-menu" title="Cancel this agent" onclick="showCancelModal(\'' + escAttr(w.instance_id) + '\')">&times;</button>';
    return '<div class="worker-card' + cardClass + '">' +
      cancelBtn +
      '<div class="worker-id"><span class="agent-dot ' + dotCls + '"' + dotAttr + ' style="display:inline-block;width:8px;height:8px;margin-right:6px;vertical-align:middle"></span>' + esc(w.instance_id) + ' ' + statusBadge + taskBoundBadge + '</div>' +
      '<div class="worker-meta">Type: ' + esc(w.agent_type) + ' · HB: ' + esc(w.last_heartbeat) +
        (w.last_send_age ? ' · sent: ' + esc(w.last_send_age) : '') +
      '</div>' +
      (w.current_tasks && w.current_tasks.length ? '<div class="worker-meta">Tasks: ' + w.current_tasks.map(id => '#' + id).join(', ') + '</div>' : '') +
      progressHTML +
    '</div>';
  }).join('');
}

function renderTasks(tasks, total) {
  const el = document.getElementById('tasks');
  const shown = tasks ? tasks.length : 0;
  document.getElementById('tasks-count').textContent = total > shown ? shown + ' of ' + total : shown;
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
    } else if ((t.status === 'pending' || t.status === 'blocked') && t.result_summary) {
      progressCol = '<span style="font-size:11px;color:var(--orange)">' + esc(t.result_summary) + '</span>';
    }
    if (t.recovery_events && t.recovery_events.length > 0) {
      progressCol += renderRecoveryEvents(t.recovery_events);
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

function renderMessages(messages, total) {
  const el = document.getElementById('messages');
  const shown = messages ? messages.length : 0;
  document.getElementById('messages-count').textContent = total > shown ? shown + ' of ' + total : shown;
  if (!messages || messages.length === 0) {
    el.innerHTML = '<div class="empty">No messages</div>';
    return;
  }
  el.innerHTML = messages.map((m, i) => {
    const classes = ['msg'];
    if (!m.read) classes.push('unread');
    if (m.recovered) classes.push('recovered');
    const recoveredPill = m.recovered
      ? '<span class="badge recovered" title="Auto-recovered output emitted by cancel_agent — worker did not deliver before being cancelled">recovered</span> '
      : '';
    return '<div class="' + classes.join(' ') + '">' +
      '<div class="msg-header">' +
        recoveredPill +
        '<span class="msg-from">' + esc(m.from) + '</span>' +
        '<span class="msg-to">&#8594; ' + esc(m.to) + '</span>' +
        '<span class="msg-time">' + esc(m.timestamp) + ' (' + esc(m.age) + ')</span>' +
      '</div>' +
      '<div class="msg-body" id="msg-body-' + i + '" onclick="toggleMsg(this)">' + esc(m.content) + '</div>' +
    '</div>';
  }).join('');
  // Mark messages that overflow as truncated
  requestAnimationFrame(() => {
    document.querySelectorAll('.msg-body').forEach(el => {
      if (el.scrollHeight > 102 && !el.classList.contains('expanded')) {
        el.classList.add('truncated');
      }
    });
  });
}

function toggleMsg(el) {
  el.classList.toggle('expanded');
  el.classList.remove('truncated');
  if (!el.classList.contains('expanded') && el.scrollHeight > 102) {
    el.classList.add('truncated');
  }
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

// renderRecoveryEvents renders a Task.recovery_events array as a small
// expandable timeline. Default-collapsed so the task list stays scannable;
// the latest event summary is already shown via t.result_summary in the
// row's progress column. Expanded form gives operators the order events
// arrived (reconciler vs watchdog), the worker that triggered each one,
// and how long ago each happened — recreates the timeline that the old
// "first writer wins" ResultSummary destroyed.
function renderRecoveryEvents(events) {
  if (!events || events.length === 0) return '';
  const summaryLabel = events.length === 1
    ? '1 recovery event'
    : events.length + ' recovery events';
  let html = '<details class="recovery-events"><summary>' + esc(summaryLabel) + '</summary><ol>';
  events.forEach(ev => {
    const sourceClass = ev.source ? esc(ev.source) : '';
    const reasonTitle = ev.reason ? ' title="' + escAttr(ev.reason) + '"' : '';
    const instance = ev.instance_id ? ' <span class="ev-age">[' + esc(ev.instance_id) + ']</span>' : '';
    html += '<li' + reasonTitle + '>' +
      '<span class="ev-source ' + sourceClass + '">' + esc(ev.source || '?') + '</span> ' +
      esc(ev.summary || '') +
      '<span class="ev-age"> · ' + esc(ev.age || '') + '</span>' +
      instance +
      '</li>';
  });
  html += '</ol></details>';
  return html;
}

// latestState caches the most recent /api/state response so action handlers
// (cancel, send-message) can resolve the current driver / agent list without
// a refetch.
let latestState = null;

async function fetchState() {
  try {
    const resp = await fetch('/api/state');
    if (!resp.ok) return;
    const data = await resp.json();
    latestState = data;

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
    renderGCStrip(data.gc);
    renderTasks(data.tasks, data.total_tasks);
    renderMessages(data.messages, data.total_messages);
    renderSide(data);
    refreshSendForm(data);
  } catch (e) {
    document.getElementById('updated').textContent = 'error';
    document.getElementById('updated').style.color = 'var(--red)';
    setTimeout(() => { document.getElementById('updated').style.color = ''; }, 2000);
  }

  // Pool status is cheap; piggy-back on the same poll.
  fetchPoolStatus();
}

function renderGCStrip(gc) {
  const strip = document.getElementById('gc-strip');
  if (!gc) {
    strip.style.display = 'none';
    return;
  }
  strip.style.display = '';
  const lastRun = gc.last_run || 'never';
  const retention = (gc.presence_retention_days || 0) + 'd / ' +
                    (gc.instance_retention_days || 0) + 'd / ' +
                    (gc.task_bound_instance_retention_hours || 0) + 'h';
  strip.innerHTML =
    '<span class="gc-label">GC</span>' +
    '<span>' + esc(String(gc.presence_pruned_total || 0)) + ' presence + ' +
              esc(String(gc.instances_pruned_total || 0)) + ' instances pruned</span>' +
    '<span class="gc-sep">·</span>' +
    '<span>last run ' + esc(lastRun) + '</span>' +
    '<span class="gc-sep">·</span>' +
    '<span>retention ' + esc(retention) + ' (presence/instance/task-bound)</span>';
}

function driverName() {
  if (latestState && latestState.agents) {
    const d = latestState.agents.find(a => a.role === 'driver');
    if (d) return d.name;
  }
  return '';
}

function refreshSendForm(data) {
  const fromDisp = document.getElementById('send-from-display');
  const drv = driverName();
  fromDisp.textContent = drv ? drv + ' (driver)' : '(no driver)';
  fromDisp.dataset.from = drv;

  const sel = document.getElementById('send-to');
  const prev = sel.value;
  const recipients = (data.agents || [])
    .map(a => a.name)
    .filter(n => n && n !== drv)
    .sort();
  sel.innerHTML = recipients.map(n => '<option value="' + escAttr(n) + '">' + esc(n) + '</option>').join('');
  if (prev && recipients.includes(prev)) sel.value = prev;
}

// ── Pool status panel ────────────────────────────────────────────────────

let poolCollapsed = false;

function togglePoolPanel() {
  poolCollapsed = !poolCollapsed;
  const body = document.getElementById('pool-body');
  const toggle = document.getElementById('pool-toggle');
  body.style.display = poolCollapsed ? 'none' : '';
  toggle.textContent = poolCollapsed ? 'expand' : 'collapse';
}

async function fetchPoolStatus() {
  try {
    const resp = await fetch('/api/pool-status');
    if (!resp.ok) return;
    const data = await resp.json();
    renderPoolStatus(data);
  } catch (e) {
    // Pool status is optional — silently ignore.
  }
}

function renderBreakdownByType(map) {
  if (!map) return '';
  const keys = Object.keys(map).sort();
  if (!keys.length) return '<span class="breakdown">—</span>';
  return '<span class="breakdown">' + keys.map(k => esc(k) + ':' + map[k]).join(', ') + '</span>';
}

function renderPoolStatus(p) {
  const card = document.getElementById('pool-card');
  const body = document.getElementById('pool-body');
  if (!p) { card.style.display = 'none'; return; }
  card.style.display = '';

  const oldestActiveDetail = p.oldest_active
    ? esc(p.oldest_active.instance_id) + ' (' + esc(p.oldest_active.heartbeat_age) + ')'
    : '—';
  const oldestOfflineDetail = p.oldest_offline
    ? esc(p.oldest_offline.instance_id) + ' (' + esc(p.oldest_offline.heartbeat_age) + ')'
    : '—';
  const oldestPresenceDetail = p.oldest_presence
    ? esc(p.oldest_presence.agent) + ' (' + esc(p.oldest_presence.last_seen_age) + ')'
    : '—';

  let html = '<div class="pool-grid">' +
    '<div class="pool-stat"><div class="pool-label">Driver</div>' +
      '<div class="pool-value">' + esc(p.driver || '—') + '</div></div>' +
    '<div class="pool-stat"><div class="pool-label">Active</div>' +
      '<div class="pool-value">' + (p.active_instances || 0) + '</div>' +
      '<div class="pool-detail">' + renderBreakdownByType(p.worker_status_by_type) + '</div></div>' +
    '<div class="pool-stat"><div class="pool-label">Offline</div>' +
      '<div class="pool-value">' + (p.offline_instances || 0) + '</div>' +
      '<div class="pool-detail">' + renderBreakdownByType(p.worker_offline_by_type) + '</div></div>' +
    '<div class="pool-stat"><div class="pool-label">Task-bound idle</div>' +
      '<div class="pool-value">' + (p.task_bound_idle_rows || 0) + '</div>' +
      '<div class="pool-detail">' + renderBreakdownByType(p.worker_task_bound_by_type) + '</div></div>' +
    '<div class="pool-stat"><div class="pool-label">Stale presence</div>' +
      '<div class="pool-value">' + (p.stale_presence || 0) + '</div>' +
      '<div class="pool-detail">cutoff: ' + (p.stale_presence_cutoff_hours || 24) + 'h</div></div>' +
    '<div class="pool-stat"><div class="pool-label">In-flight tasks</div>' +
      '<div class="pool-value">' + (p.in_flight_task_count || 0) + '</div></div>' +
  '</div>';

  html += '<div class="pool-section-header">Oldest rows</div>' +
    '<div class="pool-detail">Active: ' + oldestActiveDetail + ' · Offline: ' + oldestOfflineDetail +
    ' · Presence: ' + oldestPresenceDetail + '</div>';

  if (p.in_flight_tasks && p.in_flight_tasks.length) {
    html += '<div class="pool-section-header">In-flight tasks</div>';
    html += '<div class="pool-detail">' + p.in_flight_tasks.map(t =>
      '#' + t.id + ' ' + esc(t.title) + ' &mdash; <em>' + esc(t.owner) + '</em> (' + esc(t.age) + ')'
    ).join('<br>') + '</div>';
  }

  body.innerHTML = html;
}

// ── Cancel agent modal ───────────────────────────────────────────────────

let cancelTargetID = '';

function showCancelModal(instanceID) {
  cancelTargetID = instanceID;
  document.getElementById('cancel-target').textContent = instanceID;
  document.getElementById('cancel-reason').value = '';
  document.getElementById('cancel-result').style.display = 'none';
  document.getElementById('cancel-modal').classList.add('open');
  setTimeout(() => document.getElementById('cancel-reason').focus(), 50);
}

function hideCancelModal() {
  document.getElementById('cancel-modal').classList.remove('open');
}

async function doCancelAgent() {
  if (!cancelTargetID) return;
  const drv = driverName();
  if (!drv) {
    showToast('error', 'No driver configured', 'Cannot cancel without a driver to attribute the action to.');
    return;
  }
  const reason = document.getElementById('cancel-reason').value.trim();
  const btn = document.getElementById('cancel-confirm-btn');
  const result = document.getElementById('cancel-result');
  btn.textContent = 'Cancelling…';
  btn.disabled = true;
  try {
    const resp = await fetch('/api/cancel-agent', {
      method: 'POST',
      headers: {'Content-Type': 'application/json'},
      body: JSON.stringify({agent: cancelTargetID, cancelled_by: drv, reason: reason}),
    });
    const data = await resp.json();
    if (!resp.ok) {
      result.classList.add('error');
      result.textContent = data.error || ('HTTP ' + resp.status);
      result.style.display = '';
      return;
    }
    result.classList.remove('error');
    const lines = ['Cancelled agent: ' + (data.agent || cancelTargetID)];
    if (data.cancelled_tasks && data.cancelled_tasks.length) {
      lines.push('Cancelled tasks: ' + data.cancelled_tasks.map(id => '#' + id).join(', '));
    } else {
      lines.push('No in-progress tasks to cancel.');
    }
    lines.push('Process killed: ' + (data.process_killed ? 'yes' : 'no (no orchestration or not running)'));
    if (data.recovered_from) {
      lines.push('Recovered output emitted as message from ' + data.recovered_from + '.');
    }
    result.textContent = lines.join('\n');
    result.style.display = '';
    showToast('success', 'Agent cancelled', cancelTargetID + (data.cancelled_tasks && data.cancelled_tasks.length ? ' (' + data.cancelled_tasks.length + ' task(s))' : ''));
    fetchState();
  } catch (e) {
    result.classList.add('error');
    result.textContent = 'Request failed: ' + e.message;
    result.style.display = '';
  } finally {
    btn.textContent = 'Cancel agent';
    btn.disabled = false;
  }
}

document.getElementById('cancel-modal').addEventListener('click', function(e) {
  if (e.target === this) hideCancelModal();
});

// ── Prune modal ──────────────────────────────────────────────────────────

function showPruneModal() {
  document.getElementById('prune-result').style.display = 'none';
  document.getElementById('prune-result').classList.remove('error');
  document.getElementById('prune-dry-run').checked = true;
  document.getElementById('prune-modal').classList.add('open');
}

function hidePruneModal() {
  document.getElementById('prune-modal').classList.remove('open');
}

async function doPrune() {
  const presence = document.getElementById('prune-presence').checked;
  const instances = document.getElementById('prune-instances').checked;
  const olderDays = parseInt(document.getElementById('prune-older-days').value, 10) || 0;
  const taskBoundHours = parseInt(document.getElementById('prune-task-bound-hours').value, 10) || 0;
  const dryRun = document.getElementById('prune-dry-run').checked;
  const btn = document.getElementById('prune-run-btn');
  const result = document.getElementById('prune-result');

  if (!presence && !instances) {
    result.classList.add('error');
    result.textContent = 'Select at least one of presence or instances.';
    result.style.display = '';
    return;
  }

  btn.textContent = dryRun ? 'Previewing…' : 'Pruning…';
  btn.disabled = true;
  try {
    const body = {
      presence: presence,
      instances: instances,
      older_than_days: olderDays,
      task_bound_older_hours: taskBoundHours,
      dry_run: dryRun,
    };
    const resp = await fetch('/api/prune', {
      method: 'POST',
      headers: {'Content-Type': 'application/json'},
      body: JSON.stringify(body),
    });
    const data = await resp.json();
    if (!resp.ok) {
      result.classList.add('error');
      result.textContent = data.error || ('HTTP ' + resp.status);
      result.style.display = '';
      return;
    }
    result.classList.remove('error');
    const verb = data.dry_run ? 'Would prune' : 'Pruned';
    const lines = [
      verb + ': ' + (data.presence_pruned || 0) + ' presence, ' + (data.instances_pruned || 0) + ' instances.',
      'Retention applied: presence=' + data.presence_retention_days + 'd, instances=' + data.instance_retention_days + 'd, task-bound=' + data.task_bound_instance_retention_hours + 'h.',
    ];
    if (data.dry_run) lines.push('Dry run — uncheck "Dry run" to commit.');
    result.textContent = lines.join('\n');
    result.style.display = '';
    if (!data.dry_run) {
      showToast('success', 'Prune complete', (data.presence_pruned || 0) + ' presence + ' + (data.instances_pruned || 0) + ' instances removed');
      fetchState();
    }
  } catch (e) {
    result.classList.add('error');
    result.textContent = 'Request failed: ' + e.message;
    result.style.display = '';
  } finally {
    btn.textContent = 'Run';
    btn.disabled = false;
  }
}

document.getElementById('prune-modal').addEventListener('click', function(e) {
  if (e.target === this) hidePruneModal();
});

// ── Send message inline form ─────────────────────────────────────────────

function toggleSendForm(force) {
  const form = document.getElementById('send-form');
  const open = (typeof force === 'boolean') ? force : !form.classList.contains('open');
  if (open) {
    form.classList.add('open');
    setTimeout(() => document.getElementById('send-content').focus(), 50);
  } else {
    form.classList.remove('open');
    document.getElementById('send-content').value = '';
  }
}

async function doSendMessage() {
  const drv = driverName();
  if (!drv) {
    showToast('error', 'No driver configured', 'Cannot send from the dashboard until a driver is registered.');
    return;
  }
  const to = document.getElementById('send-to').value;
  const content = document.getElementById('send-content').value.trim();
  if (!to || !content) {
    showToast('error', 'Missing fields', 'Recipient and message body are required.');
    return;
  }
  const btn = document.getElementById('send-confirm-btn');
  btn.textContent = 'Sending…';
  btn.disabled = true;
  try {
    const resp = await fetch('/api/send-message', {
      method: 'POST',
      headers: {'Content-Type': 'application/json'},
      body: JSON.stringify({from: drv, to: to, content: content}),
    });
    const data = await resp.json();
    if (!resp.ok) {
      showToast('error', 'Send failed', data.error || ('HTTP ' + resp.status));
      return;
    }
    showToast('success', 'Message sent', drv + ' → ' + to);
    toggleSendForm(false);
    fetchState();
  } catch (e) {
    showToast('error', 'Send failed', e.message);
  } finally {
    btn.textContent = 'Send';
    btn.disabled = false;
  }
}

// ── Toast ────────────────────────────────────────────────────────────────

let toastTimer = null;

function showToast(kind, title, detail) {
  const t = document.getElementById('toast');
  t.className = 'toast ' + (kind || 'success');
  t.innerHTML = '<div class="toast-title">' + esc(title || '') + '</div>' +
                (detail ? '<div class="toast-detail">' + esc(detail) + '</div>' : '');
  t.classList.add('open');
  if (toastTimer) clearTimeout(toastTimer);
  toastTimer = setTimeout(() => { t.classList.remove('open'); }, 4500);
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
