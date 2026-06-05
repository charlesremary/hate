// Copyright 2026 Charles Emary
// SPDX-License-Identifier: FSL-1.1-Apache-2.0

/* hate — vanilla JS frontend */

const API = {
  async get(url) {
    const r = await fetch(url);
    if (!r.ok) throw new Error((await r.json()).detail || r.statusText);
    return r.json();
  },
  async post(url, body) {
    const r = await fetch(url, {
      method: 'POST',
      headers: body !== undefined ? {'Content-Type': 'application/json'} : {},
      body: body !== undefined ? JSON.stringify(body) : undefined,
    });
    if (!r.ok) throw new Error((await r.json()).detail || r.statusText);
    return r.json();
  },
  async patch(url, body) {
    const r = await fetch(url, {
      method: 'PATCH',
      headers: {'Content-Type': 'application/json'},
      body: JSON.stringify(body),
    });
    if (!r.ok) throw new Error((await r.json()).detail || r.statusText);
    return r.json();
  },
  async put(url, body) {
    const r = await fetch(url, {
      method: 'PUT',
      headers: {'Content-Type': 'application/json'},
      body: JSON.stringify(body),
    });
    if (!r.ok) throw new Error((await r.json()).detail || r.statusText);
    return r.json();
  },
  async delete(url) {
    const r = await fetch(url, { method: 'DELETE' });
    if (!r.ok) throw new Error((await r.json()).detail || r.statusText);
    return r.json();
  },
};

// ── Toast notifications ──────────────────────────────
const toast = document.getElementById('toast');
let toastTimer;
function showToast(msg, type = 'success') {
  toast.textContent = msg;
  toast.className = `toast ${type}`;
  clearTimeout(toastTimer);
  toastTimer = setTimeout(() => toast.classList.add('hidden'), 3000);
}

// ── State ────────────────────────────────────────────
let currentProject = null;
let currentTab = 'tickets';

// ── Ticket cross-references ──────────────────────────
// Cache of resolved ticket lookups for hovercards. Keyed by ticket ID;
// value is the full ticket JSON, or null for "not found". Cleared when
// the user switches projects.
const ticketRefCache = new Map();

function escapeRegex(s) { return s.replace(/[.*+?^${}()|[\]\\]/g, '\\$&'); }

// Wrap any `<prefix>-<3+ alphanumerics>` substring in a clickable chip.
// Pass through untouched if no project / no prefix available.
function linkifyTicketRefs(text) {
  if (!text || !currentProject || !currentProject.prefix) return text || '';
  const re = new RegExp(`\\b(${escapeRegex(currentProject.prefix)}-[A-Za-z0-9]{3,})\\b`, 'g');
  return String(text).replace(re, '<a class="ticket-ref" data-ticket="$1" href="javascript:void(0)" onclick="event.preventDefault();event.stopPropagation();openTicketPanel(\'$1\');hideTicketHovercard(true);">$1</a>');
}

// Hovercard for ticket references. A single shared DOM node is created lazily
// on first hover and reused for every chip.
const HOVERCARD_SHOW_DELAY = 220;
const HOVERCARD_HIDE_DELAY = 200;
let hovercardEl = null;
let hovercardShowTimer = null;
let hovercardHideTimer = null;
let hovercardCurrentId = null;

function ensureTicketHovercard() {
  if (hovercardEl) return hovercardEl;
  const el = document.createElement('div');
  el.id = 'ticket-hovercard';
  el.style.cssText = 'position:fixed;z-index:9000;background:#fff;border:1px solid #cfd8dc;border-radius:8px;box-shadow:0 6px 24px rgba(0,0,0,.18);padding:12px;width:340px;font-size:13px;display:none;line-height:1.4';
  el.addEventListener('mouseenter', () => {
    if (hovercardHideTimer) { clearTimeout(hovercardHideTimer); hovercardHideTimer = null; }
  });
  el.addEventListener('mouseleave', () => scheduleHideTicketHovercard());
  document.body.appendChild(el);
  hovercardEl = el;
  return el;
}

function positionTicketHovercard(target) {
  const card = ensureTicketHovercard();
  const rect = target.getBoundingClientRect();
  // Default: place to the right of the chip; if it would overflow, place below.
  let left = rect.right + 8;
  let top = rect.top;
  const cardWidth = 360;
  const cardHeight = 240; // estimate; will adjust with viewport bounds
  if (left + cardWidth > window.innerWidth - 8) {
    left = Math.max(8, rect.left - cardWidth - 8);
  }
  if (left < 8) {
    left = 8;
    top = rect.bottom + 6;
  }
  top = Math.max(8, Math.min(window.innerHeight - cardHeight - 8, top));
  card.style.left = `${left}px`;
  card.style.top = `${top}px`;
}

async function showTicketHovercardFor(target, ticketId) {
  if (!currentProject) return;
  const card = ensureTicketHovercard();
  positionTicketHovercard(target);
  hovercardCurrentId = ticketId;
  card.innerHTML = `<div style="color:#999">Loading <strong>${ticketId}</strong>…</div>`;
  card.style.display = 'block';

  let t = ticketRefCache.get(ticketId);
  if (t === undefined) {
    try {
      t = await API.get(`/api/projects/${currentProject.id}/tickets/${ticketId}`);
    } catch {
      t = null;
    }
    ticketRefCache.set(ticketId, t);
  }
  // If the user already moved on to a different chip, abandon this render.
  if (hovercardCurrentId !== ticketId) return;

  if (!t) {
    target.classList.add('ticket-ref-broken');
    card.innerHTML = `<div><strong>${ticketId}</strong></div><div style="color:#888;margin-top:4px">Ticket not found in this project.</div>`;
    return;
  }
  target.classList.remove('ticket-ref-broken');
  card.innerHTML = renderTicketHovercardBody(t);
  positionTicketHovercard(target);
}

function renderTicketHovercardBody(t) {
  const lastAct = (t.activity && t.activity.length) ? t.activity[t.activity.length - 1] : null;
  const lastStr = lastAct
    ? `<div style="color:#888;font-size:11px;margin-top:6px">Last: ${lastAct.action.replace(/_/g, ' ')} · ${lastAct.timestamp.replace('T', ' ').replace('Z', '').slice(0, 16)}</div>`
    : '';
  const desc = t.description
    ? `<div style="margin-top:6px;color:#555;font-size:12px;max-height:60px;overflow:hidden">${t.description.length > 240 ? t.description.slice(0, 240) + '…' : t.description}</div>`
    : '';
  const atts = (t.attachments || []).length
    ? `<div style="margin-top:8px;padding-top:8px;border-top:1px solid #eee">
         <div style="font-size:11px;color:#888;text-transform:uppercase;letter-spacing:.5px;margin-bottom:4px">Attachments</div>
         <ul style="list-style:none;padding:0;margin:0">
           ${t.attachments.map(a => `
             <li style="margin:2px 0">
               <a href="/api/projects/${currentProject.id}/tickets/${t.id}/attachments/${a.id}"
                  target="_blank" rel="noopener"
                  style="color:#1565c0;text-decoration:none;font-size:12px">📎 ${a.filename}</a>
               <span style="color:#aaa;font-size:11px"> · ${formatBytes(a.size)}</span>
             </li>`).join('')}
         </ul>
       </div>`
    : '';
  return `
    <div style="display:flex;align-items:center;gap:8px;margin-bottom:4px">
      <strong style="font-size:13px">${t.id}</strong>
      ${statusBadge(t.status)}
    </div>
    <div style="font-weight:600">${t.title}</div>
    ${desc}
    <div style="margin-top:6px;font-size:12px;color:#666">Assignee: ${resolveResourceName(t.assignee) || '—'}</div>
    ${lastStr}
    ${atts}
    <div style="margin-top:8px;padding-top:6px;border-top:1px solid #eee;text-align:right">
      <a href="javascript:void(0)"
         onclick="openTicketPanel('${t.id}');hideTicketHovercard(true);"
         style="font-size:12px;color:#1565c0">Open ticket →</a>
    </div>`;
}

function scheduleShowTicketHovercard(target, ticketId) {
  if (hovercardShowTimer) clearTimeout(hovercardShowTimer);
  if (hovercardHideTimer) { clearTimeout(hovercardHideTimer); hovercardHideTimer = null; }
  hovercardShowTimer = setTimeout(() => showTicketHovercardFor(target, ticketId), HOVERCARD_SHOW_DELAY);
}

function scheduleHideTicketHovercard() {
  if (hovercardShowTimer) { clearTimeout(hovercardShowTimer); hovercardShowTimer = null; }
  if (hovercardHideTimer) clearTimeout(hovercardHideTimer);
  hovercardHideTimer = setTimeout(() => hideTicketHovercard(false), HOVERCARD_HIDE_DELAY);
}

function hideTicketHovercard(immediate) {
  if (hovercardShowTimer) { clearTimeout(hovercardShowTimer); hovercardShowTimer = null; }
  if (hovercardHideTimer) { clearTimeout(hovercardHideTimer); hovercardHideTimer = null; }
  if (hovercardEl) hovercardEl.style.display = 'none';
  hovercardCurrentId = null;
}

// Event delegation — fires for any chip rendered anywhere in the document.
document.addEventListener('mouseover', (e) => {
  const ref = e.target.closest && e.target.closest('.ticket-ref');
  if (!ref) return;
  scheduleShowTicketHovercard(ref, ref.dataset.ticket);
});
document.addEventListener('mouseout', (e) => {
  const ref = e.target.closest && e.target.closest('.ticket-ref');
  if (!ref) return;
  scheduleHideTicketHovercard();
});
let allTickets = [];
let projectResources = [];
let currentUser = null;
let showBilling = false; // Billing tab is hidden unless enabled in Settings.

// ── Status / priority helpers ────────────────────────
const STATUS_LABELS = {
  not_started: 'Not Started', in_progress: 'In Progress',
  dev_complete: 'Dev Complete', qa_testing: 'QA Testing',
  submitted_for_review: 'Submitted for Review', approved: 'Approved',
  complete: 'Complete', closed: 'Closed', rework: 'Rework', blocked: 'Blocked',
};
const STATUS_VALUES = Object.keys(STATUS_LABELS);
const TYPE_LABELS = {
  task: 'Task', dev_task: 'Dev Task', design_task: 'Design Task',
  meeting: 'Meeting', administration: 'Administration',
};
const TYPE_VALUES = Object.keys(TYPE_LABELS);
// Ticket types with a promote/demote workflow — meeting/administration auto-complete
// on creation and have no workflow, so their workflow buttons are hidden.
const WORKFLOW_TYPES = ['task', 'dev_task', 'design_task'];

function healthBadge(health) {
  if (!health) return '';
  const cls = `health-badge health-${health}`;
  const labels = { green: '● Green', yellow: '● Yellow', red: '● Red' };
  return `<span class="${cls}">${labels[health] || health}</span>`;
}

function statusBadge(s) {
  return `<span class="badge s-${s}">${STATUS_LABELS[s] || s}</span>`;
}

function priorityCell(p) {
  return `<span class="p-${p}">${p || '—'}</span>`;
}

function resolveResourceName(emailOrName) {
  if (!emailOrName) return '—';
  const r = projectResources.find(r => r.email === emailOrName);
  return r ? r.name : (emailOrName.includes('@') ? emailOrName.split('@')[0] : emailOrName);
}

// ── Project list ─────────────────────────────────────
async function loadProjects() {
  const nav = document.getElementById('project-list');
  try {
    const all = await API.get('/api/projects');
    const showClosed = document.getElementById('filter-show-closed-projects')?.checked;
    const projects = showClosed ? all : all.filter(p => !p.closed_at);
    if (projects.length === 0) {
      const hint = !showClosed && all.some(p => p.closed_at)
        ? 'All projects are closed. Toggle "Show closed projects" above to see them.'
        : 'No projects found.<br>Create one or check Settings.';
      nav.innerHTML = `<div class="loading">${hint}</div>`;
      return;
    }
    // Group projects by client
    const grouped = {};
    projects.forEach(p => {
      const client = p.client || 'Ungrouped';
      if (!grouped[client]) grouped[client] = [];
      grouped[client].push(p);
    });
    const clients = Object.keys(grouped).sort((a, b) => a === 'Ungrouped' ? 1 : b === 'Ungrouped' ? -1 : a.localeCompare(b));
    nav.innerHTML = clients.map(client => `
      <div class="client-group">
        <div class="client-header">${client}</div>
        ${grouped[client].map(p => `
          <div class="project-item${p.closed_at ? ' closed' : ''}" data-id="${p.id}" data-path="${p.path}" ${p.closed_at ? `style="opacity:.55"` : ''}>
            <div>
              <div class="proj-name">${p.closed_at ? '🔒 ' : ''}${p.name}</div>
              <div class="proj-meta">${p.ticket_count} ticket${p.ticket_count !== 1 ? 's' : ''}
                ${p.health ? ' · ' + p.health : ''}
                ${p.closed_at ? ' · closed ' + p.closed_at : ''}</div>
            </div>
          </div>`).join('')}
      </div>`).join('');

    nav.querySelectorAll('.project-item').forEach(el => {
      el.addEventListener('click', () => selectProject(el.dataset.id, el.dataset.path, projects.find(p => p.id === el.dataset.id)));
    });
  } catch (e) {
    nav.innerHTML = `<div class="loading">Error: ${e.message}</div>`;
  }
}

// Refresh the sidebar when the "show closed" toggle flips.
document.getElementById('filter-show-closed-projects').addEventListener('change', loadProjects);

// Show/hide the closed banner and lock the New Ticket button. The server still
// enforces the read-only contract — this is UX so users see the state up front.
function applyProjectClosedUI(closedAt) {
  const banner = document.getElementById('project-closed-banner');
  const dateEl = document.getElementById('project-closed-date');
  const newBtn = document.getElementById('btn-new-ticket');
  if (closedAt) {
    banner.classList.remove('hidden');
    dateEl.textContent = `(on ${closedAt})`;
    newBtn.disabled = true;
    newBtn.title = 'Project is closed — reopen it to add tickets';
  } else {
    banner.classList.add('hidden');
    dateEl.textContent = '';
    newBtn.disabled = false;
    newBtn.title = '';
  }
}

async function selectProject(id, path, projectData) {
  currentProject = { id, path, ...projectData };
  ticketRefCache.clear();

  // Highlight sidebar item
  document.querySelectorAll('.project-item').forEach(el => el.classList.remove('active'));
  document.querySelector(`.project-item[data-id="${id}"]`)?.classList.add('active');

  // Show project view
  document.getElementById('welcome').classList.add('hidden');
  document.getElementById('settings-view').classList.add('hidden');
  document.getElementById('help-view').classList.add('hidden');
  document.getElementById('project-view').classList.remove('hidden');

  document.getElementById('project-title').textContent = currentProject.name || id;
  const hb = document.getElementById('project-health-badge');
  hb.outerHTML = `<span id="project-health-badge">${healthBadge(currentProject.health)}</span>`;
  applyProjectClosedUI(currentProject.closed_at);

  // Fetch resources and current user identity
  try {
    projectResources = await API.get(`/api/projects/${id}/resources`);
    const whoami = await API.get(`/api/projects/${id}/whoami`);
    currentUser = whoami.resource || { name: whoami.git_identity.name, email: whoami.git_identity.email };
  } catch (e) { projectResources = []; currentUser = null; }

  populateAssigneeSelect();
  renderTeamList();
  refreshSyncStatus();
  refreshCommitAs();
  switchTab(currentTab);
}

function populateAssigneeSelect() {
  const sel = document.getElementById('nt-assignee');
  sel.innerHTML = '<option value="">Unassigned</option>';
  projectResources.forEach(r => {
    const opt = document.createElement('option');
    opt.value = r.email;
    opt.textContent = `${r.name}${r.role ? ' (' + r.role + ')' : ''}`;
    sel.appendChild(opt);
  });
  // Auto-select if only one resource
  if (projectResources.length === 1) sel.value = projectResources[0].email;
}

// ── Tabs ─────────────────────────────────────────────
document.querySelectorAll('.tab').forEach(btn => {
  btn.addEventListener('click', () => switchTab(btn.dataset.tab));
});

function switchTab(tab) {
  currentTab = tab;
  document.querySelectorAll('.tab').forEach(b => b.classList.toggle('active', b.dataset.tab === tab));
  document.getElementById('tab-tickets').classList.toggle('hidden', tab !== 'tickets');
  document.getElementById('tab-dashboard').classList.toggle('hidden', tab !== 'dashboard');
  document.getElementById('tab-billing').classList.toggle('hidden', tab !== 'billing');

  if (tab === 'tickets' && currentProject) loadTickets();
  if (tab === 'dashboard' && currentProject) loadDashboard();
  if (tab === 'billing' && currentProject) loadBilling();
}

// Show or hide the Billing tab per the app setting. Hidden by default.
function applyBillingVisibility() {
  const btn = document.querySelector('.tab[data-tab="billing"]');
  if (btn) btn.classList.toggle('hidden', !showBilling);
  // If billing was the active tab and just got hidden, fall back to tickets.
  if (!showBilling && currentTab === 'billing') switchTab('tickets');
}

// ── Tickets ───────────────────────────────────────────
async function loadTickets() {
  if (!currentProject) return;
  const tbody = document.getElementById('ticket-tbody');
  tbody.innerHTML = '<tr><td colspan="7" style="padding:16px;color:#999">Loading…</td></tr>';

  try {
    const status = document.getElementById('filter-status').value;
    const type = document.getElementById('filter-type').value;
    const phase = document.getElementById('filter-phase').value;
    let url = `/api/projects/${currentProject.id}/tickets`;
    const params = [];
    if (status) params.push(`status=${status}`);
    if (type) params.push(`type=${type}`);
    if (phase) params.push(`phase=${encodeURIComponent(phase)}`);
    if (params.length) url += '?' + params.join('&');

    allTickets = await API.get(url);
    populatePhaseFilter(allTickets);
    const hideClosed = document.getElementById('filter-hide-closed').checked;
    const visible = hideClosed ? allTickets.filter(t => t.status !== 'closed' && t.status !== 'complete') : allTickets;
    renderTicketTable(visible);
  } catch (e) {
    tbody.innerHTML = `<tr><td colspan="7" style="padding:16px;color:red">${e.message}</td></tr>`;
  }
}

// Ticket sorting — mode comes from the toolbar dropdown.
// All comparators send missing dates to the bottom.
const PRIORITY_ORDER = { critical: 0, high: 1, medium: 2, low: 3 };
function cmpDate(a, b) {
  const da = a || '￿';
  const db = b || '￿';
  return da.localeCompare(db);
}
function compareTickets(a, b, mode) {
  switch (mode) {
    case 'due':
      return cmpDate(a.due_date, b.due_date)
          || cmpDate(a.planned_start_date, b.planned_start_date)
          || a.id.localeCompare(b.id);
    case 'priority':
      return ((PRIORITY_ORDER[a.priority] ?? 99) - (PRIORITY_ORDER[b.priority] ?? 99))
          || cmpDate(a.due_date, b.due_date)
          || a.id.localeCompare(b.id);
    case 'created':
      return (a.created_at || '').localeCompare(b.created_at || '')
          || a.id.localeCompare(b.id);
    case 'start':
    default:
      return cmpDate(a.planned_start_date, b.planned_start_date)
          || cmpDate(a.due_date, b.due_date)
          || a.id.localeCompare(b.id);
  }
}

function renderTicketTable(tickets) {
  const tbody = document.getElementById('ticket-tbody');
  const mode = document.getElementById('filter-sort').value || 'start';
  tickets.sort((a, b) => compareTickets(a, b, mode));
  if (tickets.length === 0) {
    tbody.innerHTML = '<tr><td colspan="7" style="padding:16px;color:#999">No tickets. Create one with "+ New Ticket".</td></tr>';
    return;
  }
  tbody.innerHTML = tickets.map(t => `
    <tr data-id="${t.id}">
      <td><strong>${t.id}</strong></td>
      <td>${t.title}</td>
      <td>${t.phase || '—'}</td>
      <td>${statusBadge(t.status)}</td>
      <td>${t.assignee ? t.assignee.split('@')[0] : '—'}</td>
      <td>${t.planned_start_date || '—'}</td>
      <td>${t.due_date || '—'}</td>
    </tr>`).join('');

  tbody.querySelectorAll('tr').forEach(row => {
    row.addEventListener('click', () => openTicketPanel(row.dataset.id));
  });
}

// Populate filter dropdowns once
(function populateFilters() {
  const statusSel = document.getElementById('filter-status');
  STATUS_VALUES.forEach(s => {
    const opt = document.createElement('option');
    opt.value = s; opt.textContent = STATUS_LABELS[s];
    statusSel.appendChild(opt);
  });
  const typeSel = document.getElementById('filter-type');
  TYPE_VALUES.forEach(t => {
    const opt = document.createElement('option');
    opt.value = t; opt.textContent = TYPE_LABELS[t];
    typeSel.appendChild(opt);
  });
})();

function populatePhaseFilter(tickets) {
  const sel = document.getElementById('filter-phase');
  const current = sel.value;
  const phases = [...new Set(tickets.map(t => t.phase).filter(Boolean))].sort();
  sel.innerHTML = '<option value="">All phases</option>';
  phases.forEach(p => {
    const opt = document.createElement('option');
    opt.value = p; opt.textContent = p;
    sel.appendChild(opt);
  });
  sel.value = current;
}

document.getElementById('filter-status').addEventListener('change', loadTickets);
document.getElementById('filter-type').addEventListener('change', loadTickets);
document.getElementById('filter-phase').addEventListener('change', loadTickets);
document.getElementById('filter-hide-closed').addEventListener('change', loadTickets);

// Sort preference is per-browser, not per-project — a PM picking "due date"
// usually wants it for all their projects.
const SORT_STORAGE_KEY = 'hate:sort';
(function restoreSortPreference() {
  const saved = localStorage.getItem(SORT_STORAGE_KEY);
  if (!saved) return;
  const sel = document.getElementById('filter-sort');
  if ([...sel.options].some(o => o.value === saved)) sel.value = saved;
})();
document.getElementById('filter-sort').addEventListener('change', (e) => {
  localStorage.setItem(SORT_STORAGE_KEY, e.target.value);
  // No need to refetch — re-render the visible set with the new sort.
  if (!allTickets.length) return;
  const hideClosed = document.getElementById('filter-hide-closed').checked;
  const visible = hideClosed ? allTickets.filter(t => t.status !== 'closed' && t.status !== 'complete') : allTickets;
  renderTicketTable(visible);
});

// ── Ticket detail panel ──────────────────────────────
async function openTicketPanel(ticketId) {
  const panel = document.getElementById('ticket-panel');
  panel.classList.remove('hidden');
  document.getElementById('panel-ticket-id').textContent = ticketId;
  document.getElementById('panel-content').innerHTML = '<p style="color:#999;padding:16px">Loading…</p>';

  try {
    const t = await API.get(`/api/projects/${currentProject.id}/tickets/${ticketId}`);
    renderTicketPanel(t);
  } catch (e) {
    document.getElementById('panel-content').innerHTML = `<p style="color:red">${e.message}</p>`;
  }
}

function renderTicketPanel(t) {
  const content = document.getElementById('panel-content');
  const typeOptions = TYPE_VALUES.map(v =>
    `<option value="${v}" ${t.type===v?'selected':''}>${TYPE_LABELS[v]}</option>`).join('');
  const fields = [
    ['Type', `<select class="inline-edit" onchange="editField('${t.id}','type',this.value)">${typeOptions}</select>`],
    ['Status', statusBadge(t.status)],
    ['Priority', priorityCell(t.priority)], ['Effort', t.effort || '—'],
    ['Assignee', resolveResourceName(t.assignee)], ['Creator', resolveResourceName(t.creator)],
    ['Due date', t.due_date || '—'], ['Planned start', t.planned_start_date || '—'],
    ['Actual start', t.actual_start_date || '—'],
    ['Predecessors', t.predecessors.length ? linkifyTicketRefs(t.predecessors.join(', ')) : '—'],
    ['Tags', `<input class="inline-edit" value="${(t.tags||[]).join(', ')}" onblur="editField('${t.id}','tags',this.value.split(',').map(s=>s.trim()).filter(Boolean))" placeholder="comma separated">`],
    ['Phase', `<input class="inline-edit" value="${t.phase||''}" onblur="editField('${t.id}','phase',this.value.trim()||null)" placeholder="e.g. Discovery">`],
  ];

  const activity = [...(t.activity || [])].reverse().slice(0, 20).map(a => `
    <li class="activity-item">
      <div class="act-time">${a.timestamp.replace('T',' ').replace('Z','')} ${a.author ? '· ' + a.author.split('@')[0] : ''}</div>
      <div class="act-detail"><strong>${a.action}</strong>${a.detail ? ': ' + linkifyTicketRefs(a.detail) : ''}</div>
    </li>`).join('');

  const timeEntries = t.time_entries || [];
  const totalHours = timeEntries.reduce((s, e) => s + e.hours, 0);
  const timeRows = timeEntries.length
    ? timeEntries.map(e => `
        <tr>
          <td>${e.date}</td>
          <td>${e.hours.toFixed(2)}</td>
          <td>${e.description}</td>
          <td><button class="btn-delete-time" onclick="deleteTimeEntry('${t.id}','${e.id}')" title="Delete">✕</button></td>
        </tr>`).join('')
    : '<tr><td colspan="4" style="color:#999">No time logged</td></tr>';

  const today = new Date().toISOString().slice(0, 10);

  const cancelBanner = t.cancellation_reason
    ? `<div style="background:#fff3e0;border-left:4px solid #e65100;padding:8px 12px;margin:8px 0;font-size:13px"><strong>⏩ Force closed</strong>${t.closed_at ? ' on ' + t.closed_at.slice(0,10) : ''}: ${linkifyTicketRefs(t.cancellation_reason)}</div>`
    : '';

  content.innerHTML = `
    <div class="panel-section">
      <h4>${t.title} <span class="time-badge">${totalHours.toFixed(2)}h</span></h4>
      ${cancelBanner}
      ${t.description ? `<p style="margin-top:8px;font-size:13px;color:#555">${linkifyTicketRefs(t.description)}</p>` : ''}
    </div>
    <div class="panel-section">
      ${fields.map(([l, v]) => `<div class="field-row"><span class="field-label">${l}</span><span class="field-value">${v}</span></div>`).join('')}
    </div>
    <div class="panel-actions">
      ${WORKFLOW_TYPES.includes(t.type) ? `
      <button class="btn-primary" onclick="promoteTicket('${t.id}')">▲ Promote</button>
      <button class="btn-secondary" onclick="demoteTicket('${t.id}')">▼ Demote</button>
      ${t.status !== 'blocked' ? `<button class="btn-secondary" onclick="blockTicket('${t.id}')">⛔ Block</button>` : ''}` : ''}
      ${t.status !== 'closed' ? `<button class="btn-secondary" onclick="forceCloseTicket('${t.id}','${(t.title||'').replace(/'/g, "\\'")}')" title="Skip the workflow and close this ticket with a reason">⏩ Force close</button>` : ''}
      <button class="btn-secondary" onclick="showCommentBox('${t.id}')">💬 Comment</button>
      <button class="btn-secondary" onclick="showTimeBox('${t.id}')">⏱ Log Time</button>
      ${projectResources.length ? `<select class="assign-select" onchange="assignTicket('${t.id}', this.value)">
        <option value="">Assign…</option>
        ${projectResources.map(r => `<option value="${r.email}" ${t.assignee === r.email ? 'selected' : ''}>${r.name}</option>`).join('')}
      </select>` : ''}
    </div>
    <div id="comment-box-${t.id}" class="hidden" style="margin-top:12px">
      <textarea id="comment-text-${t.id}" rows="3" style="width:100%;padding:6px;border:1px solid #ddd;border-radius:4px;font-size:13px" placeholder="Add a comment…"></textarea>
      <button class="btn-primary" style="margin-top:6px" onclick="submitComment('${t.id}')">Post</button>
    </div>
    <div id="time-box-${t.id}" class="hidden" style="margin-top:12px">
      <div class="time-form">
        <div style="display:flex;gap:8px">
          <label style="flex:1">Date<input type="date" id="time-date-${t.id}" value="${today}"></label>
          <label style="width:80px">Hours<input type="number" id="time-hours-${t.id}" step="0.25" min="0.25" placeholder="1.5"></label>
        </div>
        <label style="margin-top:6px">Description<input type="text" id="time-desc-${t.id}" placeholder="What did you work on?"></label>
        <button class="btn-primary" style="margin-top:8px" onclick="submitTimeEntry('${t.id}')">Log Time</button>
      </div>
    </div>
    <div class="panel-section" style="margin-top:20px">
      <h4>Time Entries</h4>
      <table class="time-table">
        <thead><tr><th>Date</th><th>Hours</th><th>Description</th><th></th></tr></thead>
        <tbody>${timeRows}</tbody>
      </table>
    </div>
    <div class="panel-section" style="margin-top:20px">
      <h4>Attachments <span style="font-size:12px;color:#999;font-weight:normal">(max 25 MB each)</span></h4>
      ${renderAttachments(t)}
    </div>
    <div class="panel-section" style="margin-top:20px">
      <h4>Activity</h4>
      <ul class="activity-list">${activity || '<li class="activity-item">No activity yet.</li>'}</ul>
    </div>`;
  // After innerHTML is replaced, wire the drop-zone events for this ticket.
  wireAttachmentDropZone(t.id);
}

function renderAttachments(t) {
  const closed = !!(currentProject && currentProject.closed_at);
  const list = (t.attachments || []).map(a => {
    const isImg = (a.content_type || '').startsWith('image/');
    const href = `/api/projects/${currentProject.id}/tickets/${t.id}/attachments/${a.id}`;
    const thumb = isImg
      ? `<img src="${href}" alt="" style="height:32px;width:32px;object-fit:cover;border-radius:4px;border:1px solid #eee;vertical-align:middle;margin-right:8px">`
      : `<span style="display:inline-block;width:32px;text-align:center;margin-right:8px;color:#888">📎</span>`;
    const sizeStr = formatBytes(a.size);
    const uploaded = `${(a.uploaded_at || '').replace('T',' ').replace('Z','').slice(0,16)}${a.uploaded_by ? ' · ' + a.uploaded_by.split('@')[0] : ''}`;
    const del = closed ? '' : `<button class="btn-delete-time" title="Delete attachment" onclick="deleteAttachment('${t.id}','${a.id}','${a.filename.replace(/'/g, "\\'")}')">✕</button>`;
    return `<li class="att-row" style="display:flex;align-items:center;padding:6px 0;border-bottom:1px solid #eee">
      ${thumb}
      <div style="flex:1;min-width:0">
        <a href="${href}" target="_blank" rel="noopener" style="font-size:13px;color:#1565c0;text-decoration:none">${a.filename}</a>
        <div style="font-size:11px;color:#999">${sizeStr} · ${uploaded}</div>
      </div>
      ${del}
    </li>`;
  }).join('') || '<li style="color:#999;font-size:13px;padding:6px 0">No attachments yet.</li>';
  const dropZone = closed
    ? `<p style="font-size:12px;color:#999;margin:8px 0 0">Project is closed — uploads disabled.</p>`
    : `<div id="att-drop-${t.id}" class="att-drop" style="margin-top:10px;padding:14px;border:2px dashed #cfd8dc;border-radius:6px;text-align:center;color:#666;font-size:13px;cursor:pointer">
        Drop a file here, or <span style="color:#1565c0;text-decoration:underline">click to choose</span>
        <input type="file" id="att-file-${t.id}" style="display:none">
      </div>`;
  return `<ul class="att-list" style="list-style:none;padding:0;margin:0">${list}</ul>${dropZone}`;
}

function wireAttachmentDropZone(ticketId) {
  const zone = document.getElementById(`att-drop-${ticketId}`);
  if (!zone) return; // closed project — no zone rendered
  const input = document.getElementById(`att-file-${ticketId}`);
  zone.addEventListener('click', () => input.click());
  input.addEventListener('change', () => {
    if (input.files && input.files[0]) uploadAttachment(ticketId, input.files[0]);
    input.value = '';
  });
  zone.addEventListener('dragover', (e) => {
    e.preventDefault();
    zone.style.background = '#e3f2fd';
    zone.style.borderColor = '#1565c0';
  });
  zone.addEventListener('dragleave', () => {
    zone.style.background = '';
    zone.style.borderColor = '#cfd8dc';
  });
  zone.addEventListener('drop', (e) => {
    e.preventDefault();
    zone.style.background = '';
    zone.style.borderColor = '#cfd8dc';
    const f = e.dataTransfer.files && e.dataTransfer.files[0];
    if (f) uploadAttachment(ticketId, f);
  });
}

const MAX_ATTACHMENT_BYTES = 25 * 1024 * 1024;

async function uploadAttachment(ticketId, file) {
  if (file.size > MAX_ATTACHMENT_BYTES) {
    showToast(`File too large — max ${formatBytes(MAX_ATTACHMENT_BYTES)}`, 'error');
    return;
  }
  const form = new FormData();
  form.append('file', file);
  form.append('author', currentUser?.email || '');
  showToast(`Uploading ${file.name}…`);
  try {
    const resp = await fetch(`/api/projects/${currentProject.id}/tickets/${ticketId}/attachments`, {
      method: 'POST',
      body: form,
    });
    if (!resp.ok) {
      const err = await resp.json().catch(() => ({ detail: resp.statusText }));
      throw new Error(err.detail || 'Upload failed');
    }
    const t = await resp.json();
    showToast(`Attached ${file.name}`);
    renderTicketPanel(t);
  } catch (e) { showToast(e.message, 'error'); }
}

async function deleteAttachment(ticketId, attachmentId, filename) {
  if (!confirm(`Delete attachment "${filename}"?`)) return;
  try {
    const t = await API.delete(`/api/projects/${currentProject.id}/tickets/${ticketId}/attachments/${attachmentId}?author=${encodeURIComponent(currentUser?.email || '')}`);
    showToast('Attachment removed');
    renderTicketPanel(t);
  } catch (e) { showToast(e.message, 'error'); }
}

function formatBytes(n) {
  if (n < 1024) return `${n} B`;
  if (n < 1024 * 1024) return `${(n / 1024).toFixed(1)} KB`;
  return `${(n / 1024 / 1024).toFixed(1)} MB`;
}

async function promoteTicket(id) {
  try {
    const t = await API.post(`/api/projects/${currentProject.id}/tickets/${id}/promote?author=${encodeURIComponent(currentUser?.email || '')}`);
    showToast(`${id} → ${t.status}`);
    renderTicketPanel(t);
    loadTickets();
  } catch (e) { showToast(e.message, 'error'); }
}

async function demoteTicket(id) {
  try {
    const t = await API.post(`/api/projects/${currentProject.id}/tickets/${id}/demote?author=${encodeURIComponent(currentUser?.email || '')}`);
    showToast(`${id} → ${t.status}`);
    renderTicketPanel(t);
    loadTickets();
  } catch (e) { showToast(e.message, 'error'); }
}

// Open the force-close modal. The actual submit happens in the form handler at
// the bottom of this file.
function forceCloseTicket(id, title) {
  document.getElementById('fc-ticket-label').textContent = `${id} — ${title}`;
  document.getElementById('fc-reason').value = '';
  document.getElementById('force-close-modal-overlay').dataset.ticketId = id;
  document.getElementById('force-close-modal-overlay').classList.remove('hidden');
  setTimeout(() => document.getElementById('fc-reason').focus(), 0);
}

async function blockTicket(id) {
  try {
    const t = await API.post(`/api/projects/${currentProject.id}/tickets/${id}/block?author=${encodeURIComponent(currentUser?.email || '')}`);
    showToast(`${id} → ${t.status}`);
    renderTicketPanel(t);
    loadTickets();
  } catch (e) { showToast(e.message, 'error'); }
}

async function editField(id, field, value) {
  try {
    const t = await API.patch(`/api/projects/${currentProject.id}/tickets/${id}`, { field, value, author: currentUser?.email || '' });
    showToast(`${field} updated`);
    renderTicketPanel(t);
    loadTickets();
  } catch (e) { showToast(e.message, 'error'); }
}

async function assignTicket(id, email) {
  if (!email) return;
  try {
    const t = await API.patch(`/api/projects/${currentProject.id}/tickets/${id}`, { field: 'assignee', value: email, author: currentUser?.email || '' });
    showToast(`Assigned to ${resolveResourceName(email)}`);
    renderTicketPanel(t);
    loadTickets();
  } catch (e) { showToast(e.message, 'error'); }
}

function showCommentBox(id) {
  const box = document.getElementById(`comment-box-${id}`);
  if (box) box.classList.toggle('hidden');
}

async function submitComment(id) {
  const text = document.getElementById(`comment-text-${id}`)?.value?.trim();
  if (!text) return;
  try {
    const t = await API.post(`/api/projects/${currentProject.id}/tickets/${id}/comment`, { message: text, author: currentUser?.email || '' });
    showToast('Comment added');
    renderTicketPanel(t);
  } catch (e) { showToast(e.message, 'error'); }
}

function showTimeBox(id) {
  const box = document.getElementById(`time-box-${id}`);
  if (box) box.classList.toggle('hidden');
}

async function submitTimeEntry(id) {
  const date = document.getElementById(`time-date-${id}`)?.value;
  const hours = parseFloat(document.getElementById(`time-hours-${id}`)?.value);
  const desc = document.getElementById(`time-desc-${id}`)?.value?.trim();
  if (!date || !hours || !desc) { showToast('Fill in date, hours, and description', 'error'); return; }
  try {
    const t = await API.post(`/api/projects/${currentProject.id}/tickets/${id}/time`, { date, hours, description: desc, author: currentUser?.email || '' });
    showToast(`Logged ${hours}h`);
    renderTicketPanel(t);
  } catch (e) { showToast(e.message, 'error'); }
}

async function deleteTimeEntry(ticketId, entryId) {
  try {
    const t = await API.delete(`/api/projects/${currentProject.id}/tickets/${ticketId}/time/${entryId}`);
    showToast('Time entry deleted');
    renderTicketPanel(t);
  } catch (e) { showToast(e.message, 'error'); }
}

document.getElementById('btn-close-panel').addEventListener('click', () => {
  document.getElementById('ticket-panel').classList.add('hidden');
});

// ── Dashboard ─────────────────────────────────────────
function loadDashboard() {
  if (!currentProject) return;
  const frame = document.getElementById('dashboard-frame');
  frame.src = `/api/projects/${currentProject.id}/dashboard`;
}

// ── Snapshot button ──────────────────────────────────
document.getElementById('btn-run-snapshot').addEventListener('click', async () => {
  if (!currentProject) return;
  const btn = document.getElementById('btn-run-snapshot');
  btn.disabled = true; btn.textContent = '⟳ Running…';
  try {
    const snap = await API.post(`/api/projects/${currentProject.id}/snapshot`);
    showToast(`Snapshot complete — health: ${snap.computed_health}`);
    // Refresh health badge
    currentProject.health = snap.computed_health;
    document.getElementById('project-health-badge').outerHTML =
      `<span id="project-health-badge">${healthBadge(snap.computed_health)}</span>`;
    if (currentTab === 'dashboard') loadDashboard();
  } catch (e) {
    const msg = e.message.includes('baseline') ? 'No baseline yet — use PM Dashboard to baseline first' : e.message;
    showToast(msg, 'error');
  }
  finally { btn.disabled = false; btn.textContent = '⟳ Snapshot'; }
});

// ── Check schedule (capacity conflicts) ──────────────
document.getElementById('btn-check-schedule').addEventListener('click', async () => {
  if (!currentProject) return;
  const btn = document.getElementById('btn-check-schedule');
  btn.disabled = true; btn.textContent = '✓ Checking…';
  try {
    const report = await API.post(`/api/projects/${currentProject.id}/check-conflicts`);
    showConflictsModal(report);
  } catch (e) { showToast(e.message, 'error'); }
  finally { btn.disabled = false; btn.textContent = '✓ Check schedule'; }
});

document.getElementById('btn-close-conflicts').addEventListener('click', () => {
  document.getElementById('conflicts-modal-overlay').classList.add('hidden');
});

// ── Balance project ──────────────────────────────────
let lastBalanceReport = null;

document.getElementById('btn-balance').addEventListener('click', async () => {
  if (!currentProject) return;
  const btn = document.getElementById('btn-balance');
  btn.disabled = true; btn.textContent = '⚖ Computing…';
  try {
    const report = await API.post(`/api/projects/${currentProject.id}/balance`, { apply: false, author: currentUser?.email || '' });
    lastBalanceReport = report;
    renderBalancePreview(report);
    document.getElementById('balance-modal-overlay').classList.remove('hidden');
  } catch (e) { showToast(e.message, 'error'); }
  finally { btn.disabled = false; btn.textContent = '⚖ Balance'; }
});

document.getElementById('btn-close-balance').addEventListener('click', () => {
  document.getElementById('balance-modal-overlay').classList.add('hidden');
});
document.getElementById('balance-modal-overlay').addEventListener('click', (e) => {
  if (e.target === document.getElementById('balance-modal-overlay'))
    document.getElementById('balance-modal-overlay').classList.add('hidden');
});

function renderBalancePreview(report) {
  const content = document.getElementById('balance-content');
  if (report.cycle_detected) {
    content.innerHTML = `<div style="background:#ffebee;border-left:4px solid #c62828;padding:10px 14px;border-radius:4px;color:#b71c1c">
      <strong>⚠ Predecessor cycle detected.</strong> Cannot balance until you break the cycle.
      Tickets involved: ${(report.cycle_ticket_ids || []).map(linkifyTicketRefs).join(', ')}
    </div>`;
    return;
  }
  if (!report.tickets_affected) {
    content.innerHTML = `<div style="color:#666">Nothing to balance — no schedulable tickets found. Make sure tickets have an effort size and assignee.</div>`;
    return;
  }
  const oldEnd = report.original_end_date || '—';
  const newEnd = report.proposed_end_date || '—';
  const shiftDays = (() => {
    if (!report.original_end_date || !report.proposed_end_date) return null;
    const a = new Date(report.original_end_date), b = new Date(report.proposed_end_date);
    return Math.round((b - a) / 86400000);
  })();
  const shiftBadge = shiftDays === null ? '' :
    `<span style="color:${shiftDays > 0 ? '#c62828' : '#1b5e20'};font-weight:600">${shiftDays > 0 ? '+' : ''}${shiftDays} days</span>`;
  const banner = `
    <div style="background:#fff3e0;border-left:4px solid #e65100;padding:10px 14px;border-radius:4px;margin-bottom:12px">
      <div><strong>Original project end:</strong> ${oldEnd}</div>
      <div><strong>Proposed end at real capacity:</strong> ${newEnd} ${shiftBadge}</div>
      <div style="font-size:12px;color:#666;margin-top:4px">
        ${report.tickets_affected} ticket${report.tickets_affected === 1 ? '' : 's'} will have new planned-start / due dates.
        Algorithm: ${report.algorithm}.
      </div>
    </div>`;

  const rows = report.changes.map(c => {
    const shift = c.old_due ? Math.round((new Date(c.new_due) - new Date(c.old_due)) / 86400000) : null;
    const shiftCell = shift === null ? '<span style="color:#999">new</span>'
      : `<span style="color:${shift > 0 ? '#c62828' : '#1b5e20'};font-weight:500">${shift > 0 ? '+' : ''}${shift}d</span>`;
    return `<tr>
      <td>${linkifyTicketRefs(c.ticket_id)}</td>
      <td style="max-width:280px;overflow:hidden;text-overflow:ellipsis;white-space:nowrap" title="${c.title.replace(/"/g, '&quot;')}">${c.title}</td>
      <td>${c.assignee.split('@')[0]}</td>
      <td>${c.hours_needed}h</td>
      <td>${c.old_start || '—'} → ${c.old_due || '—'}</td>
      <td>${c.new_start} → ${c.new_due}</td>
      <td>${shiftCell}</td>
    </tr>`;
  }).join('');

  const table = `
    <table style="width:100%;border-collapse:collapse;font-size:12px">
      <thead>
        <tr style="background:#fafafa;text-align:left">
          <th style="padding:6px 8px;border-bottom:1px solid #ddd">ID</th>
          <th style="padding:6px 8px;border-bottom:1px solid #ddd">Title</th>
          <th style="padding:6px 8px;border-bottom:1px solid #ddd">Assignee</th>
          <th style="padding:6px 8px;border-bottom:1px solid #ddd">Effort</th>
          <th style="padding:6px 8px;border-bottom:1px solid #ddd">Current</th>
          <th style="padding:6px 8px;border-bottom:1px solid #ddd">Proposed</th>
          <th style="padding:6px 8px;border-bottom:1px solid #ddd">Shift</th>
        </tr>
      </thead>
      <tbody>${rows}</tbody>
    </table>`;

  const skipped = (report.skipped || []).length
    ? `<details style="margin-top:12px"><summary style="cursor:pointer;color:#666;font-size:12px">${report.skipped.length} ticket${report.skipped.length === 1 ? '' : 's'} skipped</summary>
        <ul style="list-style:none;padding:8px 12px 0;margin:0;font-size:12px;color:#666">
          ${report.skipped.map(s => `<li>${linkifyTicketRefs(s.ticket_id)} — ${s.title} <span style="color:#999">(${s.reason})</span></li>`).join('')}
        </ul></details>`
    : '';

  const actions = `
    <div style="margin-top:16px;padding-top:12px;border-top:1px solid #eee;display:flex;gap:8px;align-items:center">
      <button class="btn-primary" id="btn-apply-balance">Apply ${report.tickets_affected} change${report.tickets_affected === 1 ? '' : 's'}</button>
      <button class="btn-secondary" id="btn-cancel-balance">Cancel</button>
      <span style="margin-left:auto;color:#888;font-size:11px">
        Heads-up: if this project has a baseline, the next snapshot will report all these as slip.
      </span>
    </div>`;

  content.innerHTML = banner + table + skipped + actions;

  document.getElementById('btn-apply-balance').addEventListener('click', applyBalance);
  document.getElementById('btn-cancel-balance').addEventListener('click', () => {
    document.getElementById('balance-modal-overlay').classList.add('hidden');
  });
}

async function applyBalance() {
  if (!lastBalanceReport) return;
  const apply = document.getElementById('btn-apply-balance');
  apply.disabled = true; apply.textContent = 'Applying…';
  try {
    await API.post(`/api/projects/${currentProject.id}/balance`, { apply: true, author: currentUser?.email || '' });
    showToast(`Balanced ${lastBalanceReport.tickets_affected} tickets`);
    document.getElementById('balance-modal-overlay').classList.add('hidden');
    if (currentTab === 'tickets') loadTickets();
  } catch (e) { showToast(e.message, 'error'); }
  finally { apply.disabled = false; apply.textContent = 'Apply'; }
}
document.getElementById('conflicts-modal-overlay').addEventListener('click', (e) => {
  if (e.target === document.getElementById('conflicts-modal-overlay'))
    document.getElementById('conflicts-modal-overlay').classList.add('hidden');
});

// Cache of the most recent conflict report so the phase dropdown can swap
// views without re-fetching.
let lastConflictReport = null;

function showConflictsModal(report) {
  lastConflictReport = report;
  renderConflictsModal('__all__');
  document.getElementById('conflicts-modal-overlay').classList.remove('hidden');
}

// scope: '__all__' = top-level summary; otherwise a phase value (incl. '' for no-phase).
function renderConflictsModal(scope) {
  const report = lastConflictReport;
  if (!report) return;
  const content = document.getElementById('conflicts-content');

  // Resolve the active view's data.
  let view;
  if (scope === '__all__') {
    view = {
      conflicts: report.conflicts || [],
      warnings: report.warnings || [],
      tickets_analyzed: report.tickets_analyzed,
      label: 'All phases',
    };
  } else {
    const ph = (report.phase_summaries || []).find(p => p.phase === scope);
    view = ph
      ? { conflicts: ph.conflicts || [], warnings: ph.warnings || [], tickets_analyzed: ph.tickets_analyzed, label: ph.label }
      : { conflicts: [], warnings: [], tickets_analyzed: 0, label: scope };
  }

  // Phase dropdown — always present, even when only the "(no phase)" bucket exists.
  const totalDaysOver = (report.conflicts || []).reduce((s, rc) => s + rc.days.length, 0);
  const phaseOptions = [
    `<option value="__all__" ${scope === '__all__' ? 'selected' : ''}>All phases (${totalDaysOver} day${totalDaysOver === 1 ? '' : 's'} over)</option>`,
    ...(report.phase_summaries || []).map(p =>
      `<option value="${(p.phase || '').replace(/"/g, '&quot;')}" ${scope === p.phase ? 'selected' : ''}>${p.label} (${p.days_over} day${p.days_over === 1 ? '' : 's'} over)</option>`
    ),
  ].join('');

  const phaseRow = `
    <div style="display:flex;gap:10px;align-items:center;margin-bottom:12px">
      <label style="font-size:12px;color:#666">Phase</label>
      <select id="conflict-phase-select" style="padding:4px 8px;font-size:13px">${phaseOptions}</select>
    </div>`;

  const summary = `<div style="color:#666;margin-bottom:12px;font-size:12px">
    ${view.label} — analyzed ${view.tickets_analyzed} ticket${view.tickets_analyzed === 1 ? '' : 's'} across ${report.resources_checked} resource${report.resources_checked === 1 ? '' : 's'}.
  </div>`;

  let body = '';
  if (view.conflicts.length === 0) {
    body = `<div style="background:#e8f5e9;border-left:4px solid #43a047;padding:10px 14px;border-radius:4px;color:#1b5e20;font-weight:500">
      ✓ No capacity conflicts in this view. Everyone's daily load fits within their availability.
    </div>`;
  } else {
    body = view.conflicts.map(rc => `
      <div style="margin-bottom:16px;border:1px solid #eee;border-radius:6px;overflow:hidden">
        <div style="background:#fff3e0;padding:8px 12px;border-bottom:1px solid #ffe0b2">
          <strong>${rc.name}</strong>
          <span style="color:#666;font-size:12px">· ${rc.email} · ${rc.capacity_hours}h/day capacity</span>
        </div>
        <div style="padding:8px 12px">
          ${rc.days.map(d => `
            <div style="margin:6px 0;padding:6px 8px;background:#fafafa;border-radius:4px">
              <div style="font-size:12px;color:#e65100;margin-bottom:4px">
                <strong>${d.date}</strong> — ${d.assigned_hours.toFixed(1)}h assigned / ${d.capacity_hours}h capacity
                <span style="color:#888"> (over by ${d.over_by_hours.toFixed(1)}h)</span>
              </div>
              <ul style="list-style:none;padding:0;margin:0">
                ${d.tickets.map(t => `
                  <li style="padding:2px 0;font-size:12px">
                    ${linkifyTicketRefs(t.ticket_id)}
                    <span style="color:#666">— ${t.title}</span>
                    <span style="color:#888;font-size:11px"> · ${t.hours.toFixed(1)}h/day · ${t.start_date}→${t.due_date}</span>
                  </li>
                `).join('')}
              </ul>
            </div>
          `).join('')}
        </div>
      </div>
    `).join('');
  }
  let warnings = '';
  if (view.warnings && view.warnings.length) {
    warnings = `<details style="margin-top:14px"><summary style="cursor:pointer;color:#666;font-size:12px">${view.warnings.length} ticket${view.warnings.length === 1 ? '' : 's'} skipped (no dates / no assignee / no effort)</summary>
      <ul style="list-style:none;padding:8px 12px 0;margin:0;font-size:12px;color:#666">
        ${view.warnings.map(w => `<li>${linkifyTicketRefs(w.ticket_id)} — ${w.title} <span style="color:#999">(${w.reason})</span></li>`).join('')}
      </ul></details>`;
  }
  content.innerHTML = phaseRow + summary + body + warnings;

  // Re-attach the dropdown handler each render.
  document.getElementById('conflict-phase-select').addEventListener('change', (e) => {
    renderConflictsModal(e.target.value);
  });
}

// ── Git identity ─────────────────────────────────────
async function refreshCommitAs() {
  if (!currentProject) return;
  const el = document.getElementById('commit-as');
  try {
    const data = await API.get(`/api/projects/${currentProject.id}/git-identity`);
    const cfg = data.configured;
    const cur = data.current;
    const gitUser = (cfg && cfg.name) ? cfg.name : (cur && cur.name) ? cur.name : null;
    const gitEmail = (cfg && cfg.email) ? cfg.email : (cur && cur.email) ? cur.email : null;
    if (gitUser) {
      el.className = 'commit-as active';
      el.textContent = `Working as: ${gitUser}${gitEmail ? ' <' + gitEmail + '>' : ''}`;
      // Pre-fill team modal fields
      if (cfg && cfg.name) {
        document.getElementById('gi-name').value = cfg.name;
        document.getElementById('gi-email').value = cfg.email;
      }
    } else {
      el.className = 'commit-as active warning';
      el.textContent = 'No Git identity set';
      document.getElementById('gi-name').value = '';
      document.getElementById('gi-email').value = '';
    }
  } catch (e) { el.className = 'commit-as'; }
}

document.getElementById('btn-set-identity').addEventListener('click', async () => {
  if (!currentProject) return;
  const name = document.getElementById('gi-name').value.trim();
  const email = document.getElementById('gi-email').value.trim();
  if (!name || !email) { showToast('Name and email are required', 'error'); return; }
  try {
    await API.post(`/api/projects/${currentProject.id}/git-identity`, { name, email });
    showToast(`Identity saved: ${name}`);
    refreshCommitAs();
    refreshTeamModal();
  } catch (e) { showToast(e.message, 'error'); }
});

// ── Sync button ──────────────────────────────────────
async function refreshSyncStatus() {
  if (!currentProject) return;
  const el = document.getElementById('sync-status');
  const btn = document.getElementById('btn-sync');
  try {
    const s = await API.get(`/api/projects/${currentProject.id}/sync-status`);
    el.className = 'sync-status';
    if (!s.has_remote) { el.style.display = 'none'; btn.style.display = 'none'; return; }
    btn.style.display = '';
    if (s.status === 'up_to_date') { el.className = 'sync-status up-to-date'; el.textContent = 'Up to date'; }
    else if (s.status === 'ahead') { el.className = 'sync-status ahead'; el.textContent = `${s.ahead} unpushed`; }
    else if (s.status === 'behind') { el.className = 'sync-status behind'; el.textContent = `${s.behind} to pull`; }
    else if (s.status === 'diverged') { el.className = 'sync-status diverged'; el.textContent = `${s.ahead}↑ ${s.behind}↓`; }
    else { el.style.display = 'none'; }
  } catch (e) { el.style.display = 'none'; btn.style.display = 'none'; }
}

document.getElementById('btn-sync').addEventListener('click', async () => {
  if (!currentProject) return;
  const btn = document.getElementById('btn-sync');
  btn.disabled = true; btn.textContent = '⇅ Syncing…';
  try {
    const result = await API.post(`/api/projects/${currentProject.id}/sync`);
    showToast(result.message);
    refreshSyncStatus();
  } catch (e) { showToast(e.message, 'error'); }
  finally { btn.disabled = false; btn.textContent = '⇅ Sync'; }
});

// ── New ticket modal ─────────────────────────────────
document.getElementById('btn-new-ticket').addEventListener('click', () => {
  document.getElementById('modal-overlay').classList.remove('hidden');
  document.getElementById('nt-title').focus();
});
document.getElementById('btn-cancel-ticket').addEventListener('click', () => {
  document.getElementById('modal-overlay').classList.add('hidden');
});
document.getElementById('modal-overlay').addEventListener('click', (e) => {
  if (e.target === document.getElementById('modal-overlay'))
    document.getElementById('modal-overlay').classList.add('hidden');
});

// Force-close modal wiring.
document.getElementById('btn-cancel-force-close').addEventListener('click', () => {
  document.getElementById('force-close-modal-overlay').classList.add('hidden');
});
document.getElementById('force-close-modal-overlay').addEventListener('click', (e) => {
  if (e.target === document.getElementById('force-close-modal-overlay'))
    document.getElementById('force-close-modal-overlay').classList.add('hidden');
});
document.getElementById('force-close-form').addEventListener('submit', async (e) => {
  e.preventDefault();
  const overlay = document.getElementById('force-close-modal-overlay');
  const id = overlay.dataset.ticketId;
  const reason = document.getElementById('fc-reason').value.trim();
  if (reason.length < 5) {
    showToast('Reason must be at least 5 characters', 'error');
    return;
  }
  try {
    const t = await API.post(`/api/projects/${currentProject.id}/tickets/${id}/force-close`, {
      reason,
      author: currentUser?.email || '',
    });
    overlay.classList.add('hidden');
    showToast(`${id} force-closed`);
    renderTicketPanel(t);
    loadTickets();
  } catch (e) { showToast(e.message, 'error'); }
});

const AUTO_COMPLETE_TYPES = ['meeting', 'administration'];

document.getElementById('nt-type').addEventListener('change', () => {
  const type = document.getElementById('nt-type').value;
  const isAuto = AUTO_COMPLETE_TYPES.includes(type);
  document.getElementById('nt-fields-standard').classList.toggle('hidden', isAuto);
  document.getElementById('nt-fields-meeting').classList.toggle('hidden', type !== 'meeting');
  document.getElementById('nt-fields-time').classList.toggle('hidden', !isAuto);
});

document.getElementById('new-ticket-form').addEventListener('submit', async (e) => {
  e.preventDefault();
  const type = document.getElementById('nt-type').value;
  const isAuto = AUTO_COMPLETE_TYPES.includes(type);
  const body = {
    type,
    title: document.getElementById('nt-title').value,
    description: document.getElementById('nt-description').value || undefined,
    creator: currentUser?.email || undefined,
    assignee: document.getElementById('nt-assignee').value || undefined,
  };
  if (!isAuto) {
    body.priority = document.getElementById('nt-priority').value || undefined;
    body.effort = document.getElementById('nt-effort').value || undefined;
    body.due_date = document.getElementById('nt-due-date').value || undefined;
  } else {
    const hours = parseFloat(document.getElementById('nt-hours').value);
    if (hours > 0) body.hours = hours;
  }
  if (type === 'meeting') {
    body.attendees = document.getElementById('nt-attendees').value || undefined;
  }
  const phaseVal = document.getElementById('nt-phase').value.trim();
  if (phaseVal) body.phase = phaseVal;
  try {
    const ticket = await API.post(`/api/projects/${currentProject.id}/tickets`, body);
    const msg = isAuto && body.hours ? `Created ${ticket.id} — ${body.hours}h logged` : `Created ${ticket.id}`;
    showToast(msg);
    document.getElementById('modal-overlay').classList.add('hidden');
    document.getElementById('new-ticket-form').reset();
    document.getElementById('nt-type').dispatchEvent(new Event('change'));
    loadTickets();
  } catch (e) { showToast(e.message, 'error'); }
});

// ── New project modal ────────────────────────────────
document.getElementById('btn-new-project').addEventListener('click', () => {
  document.getElementById('new-project-modal-overlay').classList.remove('hidden');
  document.getElementById('np-folder').focus();
});
document.getElementById('btn-cancel-project').addEventListener('click', () => {
  document.getElementById('new-project-modal-overlay').classList.add('hidden');
});

document.getElementById('new-project-form').addEventListener('submit', async (e) => {
  e.preventDefault();
  const client = document.getElementById('np-client').value.trim();
  const folder = document.getElementById('np-folder').value;
  const name = document.getElementById('np-name').value;
  const id = document.getElementById('np-id').value.toUpperCase();
  try {
    await API.post('/api/projects', { client, folder_name: folder, project_name: name, project_id: id, prefix: id });
    showToast(`Project ${name} created`);
    document.getElementById('new-project-modal-overlay').classList.add('hidden');
    document.getElementById('new-project-form').reset();
    loadProjects();
  } catch (e) { showToast(e.message, 'error'); }
});

// ── Open existing project modal ──────────────────────
document.getElementById('btn-open-project').addEventListener('click', () => {
  document.getElementById('open-project-modal-overlay').classList.remove('hidden');
  document.getElementById('op-path').focus();
});
document.getElementById('btn-cancel-open-project').addEventListener('click', () => {
  document.getElementById('open-project-modal-overlay').classList.add('hidden');
});
document.getElementById('open-project-modal-overlay').addEventListener('click', (e) => {
  if (e.target === document.getElementById('open-project-modal-overlay'))
    document.getElementById('open-project-modal-overlay').classList.add('hidden');
});

document.getElementById('open-project-form').addEventListener('submit', async (e) => {
  e.preventDefault();
  const path = document.getElementById('op-path').value.trim();
  if (!path) return;
  try {
    const project = await API.post('/api/projects/open', { path });
    showToast(`Opened ${project.name}`);
    document.getElementById('open-project-modal-overlay').classList.add('hidden');
    document.getElementById('open-project-form').reset();
    await loadProjects();
    selectProject(project.id, project.path, project);
  } catch (e) { showToast(e.message, 'error'); }
});

// ── Manage tracked projects modal ────────────────────
document.getElementById('btn-manage-projects').addEventListener('click', () => {
  document.getElementById('manage-projects-modal-overlay').classList.remove('hidden');
  renderManageProjects();
});
document.getElementById('btn-close-manage-projects').addEventListener('click', () => {
  document.getElementById('manage-projects-modal-overlay').classList.add('hidden');
});
document.getElementById('manage-projects-modal-overlay').addEventListener('click', (e) => {
  if (e.target === document.getElementById('manage-projects-modal-overlay'))
    document.getElementById('manage-projects-modal-overlay').classList.add('hidden');
});

async function renderManageProjects() {
  const container = document.getElementById('manage-projects-list');
  container.innerHTML = '<p style="color:#999;font-size:13px">Loading…</p>';
  try {
    // Always pull closed projects too so they can be reopened from this modal.
    const [active, hidden] = await Promise.all([
      API.get('/api/projects'),
      API.get('/api/projects/hidden'),
    ]);
    const row = (p, trackAction, trackLabel) => {
      const closeAction = p.closed_at
        ? `<button class="btn-secondary" onclick="reopenProjectFromManage('${p.id}')">Reopen</button>`
        : `<button class="btn-secondary" onclick="closeProjectFromManage('${p.id}', '${p.name.replace(/'/g, "\\'")}')">Close</button>`;
      const closedTag = p.closed_at ? `<span style="color:#e65100;font-size:12px;margin-left:6px">🔒 closed ${p.closed_at}</span>` : '';
      return `
        <tr>
          <td><strong>${p.name}</strong>${closedTag}<div class="mp-path">${p.path}</div></td>
          <td style="white-space:nowrap">
            ${closeAction}
            <button class="btn-secondary" onclick="${trackAction}('${encodeURIComponent(p.path)}')">${trackLabel}</button>
          </td>
        </tr>`;
    };
    let html = '<h4 class="mp-heading">Tracked</h4>';
    html += active.length
      ? `<table class="manage-projects-table"><tbody>${active.map(p => row(p, 'hideProjectFromTracking', 'Remove')).join('')}</tbody></table>`
      : '<p style="color:#999;font-size:13px">No tracked projects.</p>';
    if (hidden.length) {
      html += '<h4 class="mp-heading">Removed from tracking</h4>';
      html += `<table class="manage-projects-table"><tbody>${hidden.map(p => row(p, 'restoreProjectToTracking', 'Restore')).join('')}</tbody></table>`;
    }
    container.innerHTML = html;
  } catch (e) {
    container.innerHTML = `<p style="color:red">${e.message}</p>`;
  }
}

async function closeProjectFromManage(projectId, projectName) {
  if (!confirm(`Close "${projectName}"?\n\nTicket changes will be blocked until reopened. The project will disappear from the sidebar unless "Show closed projects" is on.`)) return;
  try {
    await API.post(`/api/projects/${projectId}/close`);
    showToast('Project closed');
    if (currentProject && currentProject.id === projectId) {
      currentProject.closed_at = new Date().toISOString().slice(0, 10);
      applyProjectClosedUI(currentProject.closed_at);
    }
    renderManageProjects();
    loadProjects();
  } catch (e) { showToast(e.message, 'error'); }
}

async function reopenProjectFromManage(projectId) {
  try {
    await API.post(`/api/projects/${projectId}/reopen`);
    showToast('Project reopened');
    if (currentProject && currentProject.id === projectId) {
      currentProject.closed_at = '';
      applyProjectClosedUI('');
    }
    renderManageProjects();
    loadProjects();
  } catch (e) { showToast(e.message, 'error'); }
}

async function hideProjectFromTracking(encodedPath) {
  const path = decodeURIComponent(encodedPath);
  try {
    await API.post('/api/projects/hide', { path });
    showToast('Removed from tracking');
    // If the project being removed is the one currently open, drop to the welcome view.
    if (currentProject && currentProject.path === path) {
      currentProject = null;
      document.getElementById('project-view').classList.add('hidden');
      document.getElementById('welcome').classList.remove('hidden');
    }
    renderManageProjects();
    loadProjects();
  } catch (e) { showToast(e.message, 'error'); }
}

async function restoreProjectToTracking(encodedPath) {
  const path = decodeURIComponent(encodedPath);
  try {
    await API.post('/api/projects/unhide', { path });
    showToast('Restored to tracking');
    renderManageProjects();
    loadProjects();
  } catch (e) { showToast(e.message, 'error'); }
}

// ── Help ──────────────────────────────────────────────
document.getElementById('btn-help').addEventListener('click', () => {
  document.getElementById('welcome').classList.add('hidden');
  document.getElementById('project-view').classList.add('hidden');
  document.getElementById('settings-view').classList.add('hidden');
  document.getElementById('help-view').classList.remove('hidden');
});

// ── Settings ──────────────────────────────────────────
document.getElementById('btn-settings').addEventListener('click', async () => {
  document.getElementById('welcome').classList.add('hidden');
  document.getElementById('project-view').classList.add('hidden');
  document.getElementById('help-view').classList.add('hidden');
  document.getElementById('settings-view').classList.remove('hidden');

  try {
    const cfg = await API.get('/api/projects/settings');
    document.getElementById('settings-projects-root').value = cfg.projects_root || '';
    document.getElementById('show-billing').checked = cfg.show_billing || false;
    document.getElementById('scheduler-enabled').checked = cfg.scheduler?.enabled || false;
    document.getElementById('scheduler-interval').value = cfg.scheduler?.interval_hours || 24;
  } catch (e) { showToast(e.message, 'error'); }
  loadProjectInfoSection();
  await loadEffortSizingSection();
});

// Project info — currently just the display name. Per-project, requires an open project.
function loadProjectInfoSection() {
  const fields = document.getElementById('project-info-fields');
  const empty = document.getElementById('project-info-empty');
  const projLabel = document.getElementById('project-info-project');
  if (!currentProject) {
    fields.classList.add('hidden');
    empty.classList.remove('hidden');
    projLabel.textContent = '';
    return;
  }
  projLabel.textContent = `— ${currentProject.id}`;
  document.getElementById('pi-project-name').value = currentProject.name || '';
  empty.classList.add('hidden');
  fields.classList.remove('hidden');
}

// Effort sizing only edits when a project is active — values are per-project.
async function loadEffortSizingSection() {
  const inputs = document.getElementById('effort-sizing-inputs');
  const empty = document.getElementById('effort-sizing-empty');
  const note = document.getElementById('effort-sizing-note');
  const projLabel = document.getElementById('effort-sizing-project');
  if (!currentProject) {
    inputs.classList.add('hidden');
    note.classList.add('hidden');
    empty.classList.remove('hidden');
    projLabel.textContent = '';
    return;
  }
  projLabel.textContent = `— ${currentProject.name || currentProject.id}`;
  try {
    const data = await API.get(`/api/projects/${currentProject.id}/effort-to-days`);
    const m = data.effort_to_days || {};
    for (const k of ['xs','s','m','l','xl']) {
      document.getElementById(`effort-${k}`).value = m[k] ?? '';
    }
    empty.classList.add('hidden');
    inputs.classList.remove('hidden');
    note.classList.remove('hidden');
  } catch (e) { showToast(e.message, 'error'); }
}

document.getElementById('settings-form').addEventListener('submit', async (e) => {
  e.preventDefault();
  const body = {
    projects_root: document.getElementById('settings-projects-root').value || undefined,
    show_billing: document.getElementById('show-billing').checked,
    scheduler: {
      enabled: document.getElementById('scheduler-enabled').checked,
      interval_hours: parseFloat(document.getElementById('scheduler-interval').value),
      projects: 'all',
    },
  };
  try {
    await API.put('/api/projects/settings', body);
    if (currentProject && !document.getElementById('project-info-fields').classList.contains('hidden')) {
      const newName = document.getElementById('pi-project-name').value.trim();
      if (newName && newName !== currentProject.name) {
        const updated = await API.patch(`/api/projects/${currentProject.id}/info`, { project_name: newName });
        currentProject.name = updated.project_name;
        document.getElementById('project-title').textContent = currentProject.name;
      }
    }
    if (currentProject && !document.getElementById('effort-sizing-inputs').classList.contains('hidden')) {
      const effortToDays = {};
      for (const k of ['xs','s','m','l','xl']) {
        const v = parseInt(document.getElementById(`effort-${k}`).value, 10);
        if (Number.isFinite(v) && v >= 1) effortToDays[k] = v;
      }
      if (Object.keys(effortToDays).length === 5) {
        await API.put(`/api/projects/${currentProject.id}/effort-to-days`, { effort_to_days: effortToDays });
      } else {
        showToast('Effort sizing not saved — all five sizes need a value ≥ 1', 'error');
        return;
      }
    }
    showToast('Settings saved');
    showBilling = body.show_billing;
    applyBillingVisibility();
    loadProjects();
  } catch (e) { showToast(e.message, 'error'); }
});

document.getElementById('btn-run-now').addEventListener('click', async () => {
  const result_el = document.getElementById('scheduler-result');
  result_el.textContent = 'Running snapshots…';
  try {
    const result = await API.post('/api/scheduler/run');
    result_el.textContent = JSON.stringify(result.results, null, 2);
    showToast('Snapshot run complete');
  } catch (e) {
    result_el.textContent = e.message;
    showToast(e.message, 'error');
  }
});

// ── Team & identity ───────────────────────────────────
document.getElementById('btn-team').addEventListener('click', () => {
  document.getElementById('team-modal-overlay').classList.remove('hidden');
  document.getElementById('add-resource-form').classList.add('hidden');
  refreshTeamModal();
});
document.getElementById('btn-close-team').addEventListener('click', () => {
  document.getElementById('team-modal-overlay').classList.add('hidden');
});
document.getElementById('team-modal-overlay').addEventListener('click', (e) => {
  if (e.target === document.getElementById('team-modal-overlay'))
    document.getElementById('team-modal-overlay').classList.add('hidden');
});

// Fetches the saved identity + roster and re-renders both sections of the modal.
async function refreshTeamModal() {
  if (!currentProject) return;
  try {
    const [whoami, resources] = await Promise.all([
      API.get(`/api/projects/${currentProject.id}/whoami`),
      API.get(`/api/projects/${currentProject.id}/resources`),
    ]);
    projectResources = resources;
    const gi = whoami.git_identity || { name: '', email: '' };
    document.getElementById('gi-name').value = gi.name || '';
    document.getElementById('gi-email').value = gi.email || '';
    renderIdentityStatus(gi, whoami.resource);
    renderTeamList(whoami.resource);
    populateAssigneeSelect();
  } catch (e) { showToast(e.message, 'error'); }
}

// Shows whether the saved identity is set, and whether it links to a team member.
function renderIdentityStatus(gi, matched) {
  const el = document.getElementById('identity-status');
  if (!gi.name && !gi.email) {
    el.className = 'identity-status warn';
    el.innerHTML = "⚠ No identity set — your commits and time entries won't be attributed to anyone.";
  } else if (matched) {
    el.className = 'identity-status ok';
    el.innerHTML = `✓ Linked to team member <strong>${matched.name}</strong>.`;
  } else {
    el.className = 'identity-status warn';
    el.innerHTML = "⚠ This identity isn't on the team roster, so you can't be assigned tickets. " +
      '<button class="btn-link" onclick="addMeToTeam()">Add me to the team</button>';
  }
}

// Renders the roster. The member matching the saved identity is tagged "you".
function renderTeamList(matched) {
  const container = document.getElementById('team-list');
  if (!projectResources.length) {
    container.innerHTML = '<p class="team-hint">No team members yet — add one below.</p>';
    return;
  }
  const matchedEmail = matched ? matched.email : null;
  container.innerHTML = `<table class="team-table">
    <thead><tr><th>Name</th><th>Email</th><th>Git username</th><th>Role</th><th>Daily hours</th><th></th></tr></thead>
    <tbody>${projectResources.map(r => teamRow(r, r.email === matchedEmail)).join('')}</tbody>
  </table>`;
}

function teamRow(r, isYou) {
  const e = encodeURIComponent(r.email);
  const dh = (r.daily_hours_available == null) ? '8 (default)' : `${r.daily_hours_available}h`;
  return `<tr data-email="${e}">
    <td>${r.name}${isYou ? ' <span class="you-tag">you</span>' : ''}</td>
    <td>${r.email}</td>
    <td>${r.git_user || '—'}</td>
    <td>${r.role || '—'}</td>
    <td>${dh}</td>
    <td class="team-row-actions">
      <button class="icon-btn-sm" title="Edit" onclick="editTeamRow('${e}')">✎</button>
      <button class="icon-btn-sm" title="Remove" onclick="removeResource('${e}')">✕</button>
    </td>
  </tr>`;
}

// Swaps one roster row into inline-edit mode. Inputs are scoped to the row.
function editTeamRow(encodedEmail) {
  const r = projectResources.find(x => x.email === decodeURIComponent(encodedEmail));
  const row = document.querySelector(`#team-list tr[data-email="${encodedEmail}"]`);
  if (!r || !row) return;
  row.innerHTML = `
    <td><input class="te-edit te-name" value="${r.name}"></td>
    <td><input class="te-edit te-email" value="${r.email}"></td>
    <td><input class="te-edit te-git" value="${r.git_user || ''}"></td>
    <td><input class="te-edit te-role" value="${r.role || ''}"></td>
    <td><input class="te-edit te-daily" type="number" min="0.5" max="24" step="0.5" placeholder="8" value="${r.daily_hours_available == null ? '' : r.daily_hours_available}" style="width:70px"></td>
    <td class="team-row-actions">
      <button class="icon-btn-sm" title="Save" onclick="saveTeamRow('${encodedEmail}')">✓</button>
      <button class="icon-btn-sm" title="Cancel" onclick="refreshTeamModal()">✕</button>
    </td>`;
  row.querySelector('.te-name').focus();
}

async function saveTeamRow(encodedEmail) {
  const row = document.querySelector(`#team-list tr[data-email="${encodedEmail}"]`);
  if (!row) return;
  const dailyRaw = row.querySelector('.te-daily').value.trim();
  const body = {
    name: row.querySelector('.te-name').value.trim(),
    email: row.querySelector('.te-email').value.trim(),
    git_user: row.querySelector('.te-git').value.trim() || undefined,
    role: row.querySelector('.te-role').value.trim() || 'developer',
    daily_hours_available: dailyRaw === '' ? null : parseFloat(dailyRaw),
  };
  if (!body.name || !body.email) { showToast('Name and email are required', 'error'); return; }
  try {
    projectResources = await API.patch(`/api/projects/${currentProject.id}/resources/${encodedEmail}`, body);
    showToast('Member updated');
    refreshTeamModal();
  } catch (e) { showToast(e.message, 'error'); }
}

async function removeResource(encodedEmail) {
  try {
    projectResources = await API.delete(`/api/projects/${currentProject.id}/resources/${encodedEmail}`);
    showToast('Member removed');
    refreshTeamModal();
  } catch (e) { showToast(e.message, 'error'); }
}

// Reveal / hide the add-member form.
document.getElementById('btn-show-add-resource').addEventListener('click', () => {
  document.getElementById('add-resource-form').classList.remove('hidden');
  document.getElementById('res-name').focus();
});
document.getElementById('btn-cancel-add-resource').addEventListener('click', () => {
  const form = document.getElementById('add-resource-form');
  form.reset();
  document.getElementById('res-role').value = 'developer';
  form.classList.add('hidden');
});

document.getElementById('add-resource-form').addEventListener('submit', async (e) => {
  e.preventDefault();
  const dailyRaw = document.getElementById('res-daily-hours').value.trim();
  const body = {
    name: document.getElementById('res-name').value.trim(),
    email: document.getElementById('res-email').value.trim(),
    git_user: document.getElementById('res-git-user').value.trim() || undefined,
    role: document.getElementById('res-role').value.trim() || 'developer',
    daily_hours_available: dailyRaw === '' ? null : parseFloat(dailyRaw),
  };
  try {
    projectResources = await API.post(`/api/projects/${currentProject.id}/resources`, body);
    showToast(`Added ${body.name}`);
    const form = document.getElementById('add-resource-form');
    form.reset();
    document.getElementById('res-role').value = 'developer';
    form.classList.add('hidden');
    refreshTeamModal();
  } catch (e) { showToast(e.message, 'error'); }
});

// One click: add the current saved identity to the roster.
async function addMeToTeam() {
  if (!currentProject) return;
  try {
    const whoami = await API.get(`/api/projects/${currentProject.id}/whoami`);
    const gi = whoami.git_identity || {};
    if (!gi.name || !gi.email) {
      showToast('Save your identity first (Name + Email above)', 'error');
      return;
    }
    projectResources = await API.post(`/api/projects/${currentProject.id}/resources`, {
      name: gi.name, email: gi.email, git_user: gi.name, role: 'developer',
    });
    showToast(`Added you to the team — ${gi.name}`);
    refreshTeamModal();
  } catch (e) { showToast(e.message, 'error'); }
}

// ── Billing ───────────────────────────────────────────
let billingEntries = [];

async function loadBilling() {
  if (!currentProject) return;
  const container = document.getElementById('billing-content');
  container.innerHTML = '<p style="color:#999;padding:16px">Loading…</p>';

  const start = document.getElementById('billing-start').value;
  const end = document.getElementById('billing-end').value;
  let url = `/api/projects/${currentProject.id}/tickets/billing`;
  const params = [];
  if (start) params.push(`start=${start}`);
  if (end) params.push(`end=${end}`);
  if (params.length) url += '?' + params.join('&');

  try {
    const data = await API.get(url);
    billingEntries = data.entries;
    populateBillingAuthorFilter(billingEntries);
    renderBilling();
  } catch (e) {
    billingEntries = [];
    container.innerHTML = `<p style="color:red">${e.message}</p>`;
  }
}

// Populate the author dropdown from the authors present in the loaded entries.
function populateBillingAuthorFilter(entries) {
  const sel = document.getElementById('billing-author');
  const current = sel.value;
  const authors = [...new Set(entries.map(e => e.author).filter(Boolean))].sort();
  sel.innerHTML = '<option value="">All authors</option>';
  authors.forEach(a => {
    const opt = document.createElement('option');
    opt.value = a; opt.textContent = a;
    sel.appendChild(opt);
  });
  // Keep the prior selection if that author still has entries in range.
  sel.value = authors.includes(current) ? current : '';
}

// Render the billing table from billingEntries, filtered by the selected author.
function renderBilling() {
  const container = document.getElementById('billing-content');
  const author = document.getElementById('billing-author').value;
  const entries = author ? billingEntries.filter(e => e.author === author) : billingEntries;

  if (entries.length === 0) {
    container.innerHTML = '<p style="color:#999;padding:16px">No time entries found for this period.</p>';
    return;
  }
  const totalHours = entries.reduce((s, e) => s + e.hours, 0);
  const rows = entries.map(e => `
    <tr>
      <td>${e.ticket_id}</td>
      <td>${e.ticket_title}</td>
      <td>${e.ticket_phase || '—'}</td>
      <td>${e.date}</td>
      <td>${e.hours.toFixed(2)}</td>
      <td>${e.description}</td>
      <td>${e.author || '—'}</td>
    </tr>`).join('');
  container.innerHTML = `
    <table class="billing-table">
      <thead><tr><th>Ticket</th><th>Title</th><th>Phase</th><th>Date</th><th>Hours</th><th>Description</th><th>Author</th></tr></thead>
      <tbody>${rows}</tbody>
      <tfoot><tr><td colspan="4" style="text-align:right;font-weight:700">Total</td><td style="font-weight:700">${totalHours.toFixed(2)}</td><td colspan="2"></td></tr></tfoot>
    </table>`;
}

function generateBillingCSV() {
  const table = document.querySelector('.billing-table');
  if (!table) { showToast('Load billing data first', 'error'); return; }
  const rows = [];
  table.querySelectorAll('thead tr, tbody tr, tfoot tr').forEach(tr => {
    const cells = [];
    tr.querySelectorAll('th, td').forEach(td => cells.push(`"${td.textContent.replace(/"/g, '""')}"`));
    rows.push(cells.join(','));
  });
  const blob = new Blob([rows.join('\n')], { type: 'text/csv' });
  const a = document.createElement('a');
  a.href = URL.createObjectURL(blob);
  const author = document.getElementById('billing-author').value;
  const authorSuffix = author ? '-' + author.split('@')[0] : '';
  a.download = `billing-${currentProject.id}${authorSuffix}-${new Date().toISOString().slice(0,10)}.csv`;
  a.click();
  URL.revokeObjectURL(a.href);
  showToast('CSV downloaded');
}

document.getElementById('billing-start').addEventListener('change', loadBilling);
document.getElementById('billing-end').addEventListener('change', loadBilling);
document.getElementById('billing-author').addEventListener('change', renderBilling);

// ── Init ──────────────────────────────────────────────
async function initBillingVisibility() {
  try {
    const cfg = await API.get('/api/projects/settings');
    showBilling = cfg.show_billing || false;
  } catch (e) { showBilling = false; }
  applyBillingVisibility();
}

loadProjects();
initBillingVisibility();
