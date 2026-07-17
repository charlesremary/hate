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

// Escape user text for safe insertion into innerHTML.
function escapeHtml(s) {
  return String(s == null ? '' : s).replace(/[&<>"']/g, c =>
    ({ '&': '&amp;', '<': '&lt;', '>': '&gt;', '"': '&quot;', "'": '&#39;' }[c]));
}

// Convenience: escape then linkify TKT refs (for short inline strings).
function escapeAndLinkify(text) {
  return linkifyTicketRefs(escapeHtml(text));
}

// Render markdown to sanitized HTML, then re-apply TKT-ref chips to the
// resulting *text nodes* only (so we never rewrite href/attribute contents).
function renderMarkdown(text) {
  if (!text) return '';
  if (typeof marked === 'undefined' || typeof DOMPurify === 'undefined') {
    // Libraries failed to load — degrade to escaped, whitespace-preserving text.
    return `<div style="white-space:pre-wrap">${escapeAndLinkify(text)}</div>`;
  }
  const html = DOMPurify.sanitize(marked.parse(String(text), { gfm: true, breaks: true }));
  if (!currentProject || !currentProject.prefix) return html;

  const tpl = document.createElement('template');
  tpl.innerHTML = html;
  const re = new RegExp(`\\b${escapeRegex(currentProject.prefix)}-[A-Za-z0-9]{3,}\\b`);
  const walker = document.createTreeWalker(tpl.content, NodeFilter.SHOW_TEXT);
  const targets = [];
  while (walker.nextNode()) {
    const n = walker.currentNode;
    if (n.parentNode && n.parentNode.closest && n.parentNode.closest('a')) continue; // skip existing links
    if (re.test(n.nodeValue)) targets.push(n);
  }
  targets.forEach(n => {
    const span = document.createElement('span');
    span.innerHTML = escapeAndLinkify(n.nodeValue);
    n.replaceWith(...span.childNodes);
  });
  return tpl.innerHTML;
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
    ? `<div style="margin-top:6px;color:#555;font-size:12px;max-height:60px;overflow:hidden;white-space:pre-wrap">${escapeHtml(t.description.length > 240 ? t.description.slice(0, 240) + '…' : t.description)}</div>`
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
let effortToDaysMap = {}; // effort size → configured days, for the current project
let ticketView = localStorage.getItem('hate:ticketview') || 'list'; // 'list' | 'plan'
let projectResources = [];
let currentUser = null;
let showBilling = false; // Billing tab is hidden unless enabled in Settings.
let showCosmic = false;  // COSMIC tab (experimental) is hidden unless enabled in Settings.

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
  document.getElementById('tab-overview').classList.toggle('hidden', tab !== 'overview');
  document.getElementById('tab-billing').classList.toggle('hidden', tab !== 'billing');
  document.getElementById('tab-cosmic').classList.toggle('hidden', tab !== 'cosmic');
  document.getElementById('tab-testcases').classList.toggle('hidden', tab !== 'testcases');

  if (tab === 'tickets' && currentProject) loadTickets();
  if (tab === 'dashboard' && currentProject) loadDashboard();
  if (tab === 'overview' && currentProject) loadOverview();
  if (tab === 'billing' && currentProject) loadBilling();
  if (tab === 'cosmic' && currentProject) loadCosmic();
  if (tab === 'testcases' && currentProject) loadTestCases();
}

// Show or hide the Billing tab per the app setting. Hidden by default.
function applyBillingVisibility() {
  const btn = document.querySelector('.tab[data-tab="billing"]');
  if (btn) btn.classList.toggle('hidden', !showBilling);
  // If billing was the active tab and just got hidden, fall back to tickets.
  if (!showBilling && currentTab === 'billing') switchTab('tickets');
}

// Show or hide the experimental COSMIC tab per the app setting. Hidden by default.
function applyCosmicVisibility() {
  const btn = document.querySelector('.tab[data-tab="cosmic"]');
  if (btn) btn.classList.toggle('hidden', !showCosmic);
  if (!showCosmic && currentTab === 'cosmic') switchTab('tickets');
}

// ── Project Overview ──────────────────────────────────
// Per-project reference material: contacts, links, and general instructions.
// The whole overview is replaced on every add/edit/delete (small hand lists).
let overviewData = { contacts: [], links: [], instructions: [] };
let overviewEditing = null; // { section, id } while a form is open

function ovNewId() {
  return (crypto.randomUUID ? crypto.randomUUID() : 'ov' + Math.random().toString(36).slice(2));
}

async function loadOverview() {
  const el = document.getElementById('overview-content');
  if (!currentProject) { el.innerHTML = '<p style="color:#999;padding:16px">Open a project to see its overview.</p>'; return; }
  el.innerHTML = '<p style="color:#999;padding:16px">Loading…</p>';
  overviewEditing = null;
  try {
    const d = await API.get(`/api/projects/${currentProject.id}/overview`);
    overviewData = { contacts: d.contacts || [], links: d.links || [], instructions: d.instructions || [] };
    renderOverview();
  } catch (e) { el.innerHTML = `<p style="color:#c62828;padding:16px">${escapeHtml(e.message)}</p>`; }
}

async function ovPersist() {
  const d = await API.put(`/api/projects/${currentProject.id}/overview`, overviewData);
  overviewData = { contacts: d.contacts || [], links: d.links || [], instructions: d.instructions || [] };
  overviewEditing = null;
  renderOverview();
  showToast('Overview saved');
}

function ovAdd(section) { overviewEditing = { section, id: null }; renderOverview(); }
function ovEdit(section, id) { overviewEditing = { section, id }; renderOverview(); }
function ovCancel() { overviewEditing = null; renderOverview(); }
async function ovDelete(section, id) {
  overviewData[section] = overviewData[section].filter(x => x.id !== id);
  try { await ovPersist(); } catch (e) { showToast(e.message, 'error'); }
}

const OV_CARD = 'background:var(--surface);border:1px solid var(--border);border-radius:8px;padding:12px 14px;margin-bottom:10px';
const OV_SEC = 'margin-bottom:28px;max-width:760px';

function ovSectionHeader(title, section) {
  return `<div style="display:flex;align-items:center;justify-content:space-between;margin-bottom:12px">
    <h3 style="font-size:15px;margin:0">${title}</h3>
    <button class="btn-secondary" onclick="ovAdd('${section}')">+ Add</button>
  </div>`;
}

function ovRowButtons(section, id) {
  return `<div style="display:flex;gap:6px;flex-shrink:0">
    <button class="btn-secondary" style="padding:2px 8px;font-size:12px" onclick="ovEdit('${section}','${id}')">Edit</button>
    <button class="btn-secondary" style="padding:2px 8px;font-size:12px" onclick="ovDelete('${section}','${id}')">Delete</button>
  </div>`;
}

function renderOverview() {
  const el = document.getElementById('overview-content');
  el.innerHTML = ovContactsSection() + ovLinksSection() + ovInstructionsSection();
}

// ── Contacts ──
function ovContactsSection() {
  const editing = overviewEditing && overviewEditing.section === 'contacts';
  let items = overviewData.contacts.map(c => {
    if (editing && overviewEditing.id === c.id) return ovContactForm(c);
    const badge = c.type === 'client'
      ? '<span class="badge s-blocked" style="font-size:10px">CLIENT</span>'
      : '<span class="badge s-in_progress" style="font-size:10px">INTERNAL</span>';
    const sub = [c.role, c.company].filter(Boolean).map(escapeHtml).join(', ');
    const chat = c.chat_handle ? `${c.chat_platform ? c.chat_platform[0].toUpperCase() + c.chat_platform.slice(1) : 'Chat'} ${escapeHtml(c.chat_handle)}` : '';
    const contactLine = [
      c.email ? `<a href="mailto:${escapeHtml(c.email)}">${escapeHtml(c.email)}</a>` : '',
      c.phone ? escapeHtml(c.phone) : '',
      chat,
    ].filter(Boolean).join(' &middot; ');
    return `<div style="${OV_CARD};display:flex;justify-content:space-between;gap:12px">
      <div>
        <div style="font-weight:600">${escapeHtml(c.name)} ${badge}</div>
        ${sub ? `<div style="color:var(--text-muted);font-size:12px;margin-top:2px">${sub}</div>` : ''}
        ${contactLine ? `<div style="font-size:13px;margin-top:4px">${contactLine}</div>` : ''}
      </div>
      ${ovRowButtons('contacts', c.id)}
    </div>`;
  }).join('');
  if (editing && !overviewEditing.id) items += ovContactForm(null);
  if (!overviewData.contacts.length && !editing) items = '<p style="color:#999;font-size:13px">No contacts yet.</p>';
  return `<section style="${OV_SEC}">${ovSectionHeader('Contacts', 'contacts')}${items}</section>`;
}

function ovContactForm(c) {
  c = c || {};
  const sel = (v, opts) => opts.map(o => `<option value="${o}"${v === o ? ' selected' : ''}>${o[0].toUpperCase() + o.slice(1)}</option>`).join('');
  return `<form onsubmit="return false" style="${OV_CARD};border-color:var(--accent)">
    <div style="display:flex;gap:8px">
      <label style="flex:1">Type<select id="ov-c-type">${sel(c.type || 'internal', ['internal','client'])}</select></label>
      <label style="flex:2">Name<input type="text" id="ov-c-name" value="${escapeHtml(c.name || '')}"></label>
    </div>
    <div style="display:flex;gap:8px">
      <label style="flex:1">Role / title<input type="text" id="ov-c-role" value="${escapeHtml(c.role || '')}"></label>
      <label style="flex:1">Company / org<input type="text" id="ov-c-company" value="${escapeHtml(c.company || '')}"></label>
    </div>
    <div style="display:flex;gap:8px">
      <label style="flex:1">Email<input type="text" id="ov-c-email" value="${escapeHtml(c.email || '')}"></label>
      <label style="flex:1">Phone<input type="text" id="ov-c-phone" value="${escapeHtml(c.phone || '')}"></label>
    </div>
    <div style="display:flex;gap:8px">
      <label style="width:120px">Chat<select id="ov-c-platform">${sel(c.chat_platform || 'slack', ['slack','teams','other'])}</select></label>
      <label style="flex:1">Handle<input type="text" id="ov-c-handle" value="${escapeHtml(c.chat_handle || '')}" placeholder="@handle"></label>
    </div>
    <div class="form-actions">
      <button class="btn-primary" onclick="ovSaveContact('${c.id || ''}')">Save</button>
      <button class="btn-secondary" onclick="ovCancel()">Cancel</button>
    </div>
  </form>`;
}

async function ovSaveContact(id) {
  const name = document.getElementById('ov-c-name').value.trim();
  if (!name) { showToast('Name is required', 'error'); return; }
  const item = {
    id: id || ovNewId(),
    type: document.getElementById('ov-c-type').value,
    name,
    role: document.getElementById('ov-c-role').value.trim(),
    company: document.getElementById('ov-c-company').value.trim(),
    email: document.getElementById('ov-c-email').value.trim(),
    phone: document.getElementById('ov-c-phone').value.trim(),
    chat_platform: document.getElementById('ov-c-platform').value,
    chat_handle: document.getElementById('ov-c-handle').value.trim(),
  };
  ovUpsert('contacts', item);
  try { await ovPersist(); } catch (e) { showToast(e.message, 'error'); }
}

// ── Links ──
function ovLinksSection() {
  const editing = overviewEditing && overviewEditing.section === 'links';
  let items = overviewData.links.map(l => {
    if (editing && overviewEditing.id === l.id) return ovLinkForm(l);
    return `<div style="${OV_CARD};display:flex;justify-content:space-between;gap:12px">
      <div style="min-width:0">
        <div style="font-weight:600">${escapeHtml(l.description || l.url)}</div>
        <a href="${escapeHtml(l.url)}" target="_blank" rel="noopener" style="font-size:13px;word-break:break-all">${escapeHtml(l.url)}</a>
      </div>
      ${ovRowButtons('links', l.id)}
    </div>`;
  }).join('');
  if (editing && !overviewEditing.id) items += ovLinkForm(null);
  if (!overviewData.links.length && !editing) items = '<p style="color:#999;font-size:13px">No links yet.</p>';
  return `<section style="${OV_SEC}">${ovSectionHeader('Links', 'links')}${items}</section>`;
}

function ovLinkForm(l) {
  l = l || {};
  return `<form onsubmit="return false" style="${OV_CARD};border-color:var(--accent)">
    <label>Description<input type="text" id="ov-l-desc" value="${escapeHtml(l.description || '')}" placeholder="e.g. GitHub repo"></label>
    <label>URL<input type="text" id="ov-l-url" value="${escapeHtml(l.url || '')}" placeholder="https://…"></label>
    <div class="form-actions">
      <button class="btn-primary" onclick="ovSaveLink('${l.id || ''}')">Save</button>
      <button class="btn-secondary" onclick="ovCancel()">Cancel</button>
    </div>
  </form>`;
}

async function ovSaveLink(id) {
  const url = document.getElementById('ov-l-url').value.trim();
  if (!url) { showToast('URL is required', 'error'); return; }
  ovUpsert('links', { id: id || ovNewId(), description: document.getElementById('ov-l-desc').value.trim(), url });
  try { await ovPersist(); } catch (e) { showToast(e.message, 'error'); }
}

// ── Instructions ──
function ovInstructionsSection() {
  const editing = overviewEditing && overviewEditing.section === 'instructions';
  let items = overviewData.instructions.map(i => {
    if (editing && overviewEditing.id === i.id) return ovInstructionForm(i);
    return `<div style="${OV_CARD}">
      <div style="display:flex;justify-content:space-between;gap:12px;align-items:flex-start">
        <div style="font-weight:600">${escapeHtml(i.title || 'Untitled')}</div>
        ${ovRowButtons('instructions', i.id)}
      </div>
      ${i.body ? `<div class="md" style="margin-top:8px;font-size:13px">${renderMarkdown(i.body)}</div>` : ''}
    </div>`;
  }).join('');
  if (editing && !overviewEditing.id) items += ovInstructionForm(null);
  if (!overviewData.instructions.length && !editing) items = '<p style="color:#999;font-size:13px">No instructions yet.</p>';
  return `<section style="${OV_SEC}">${ovSectionHeader('General instructions', 'instructions')}${items}</section>`;
}

function ovInstructionForm(i) {
  i = i || {};
  return `<form onsubmit="return false" style="${OV_CARD};border-color:var(--accent)">
    <label>Title<input type="text" id="ov-i-title" value="${escapeHtml(i.title || '')}" placeholder="e.g. How to log time"></label>
    <label>Body (Markdown supported)<textarea id="ov-i-body" rows="5" style="width:100%;padding:7px 10px;border:1px solid var(--border);border-radius:6px;font-size:14px;font-family:inherit">${escapeHtml(i.body || '')}</textarea></label>
    <div class="form-actions">
      <button class="btn-primary" onclick="ovSaveInstruction('${i.id || ''}')">Save</button>
      <button class="btn-secondary" onclick="ovCancel()">Cancel</button>
    </div>
  </form>`;
}

async function ovSaveInstruction(id) {
  const title = document.getElementById('ov-i-title').value.trim();
  const body = document.getElementById('ov-i-body').value.trim();
  if (!title && !body) { showToast('Add a title or body', 'error'); return; }
  ovUpsert('instructions', { id: id || ovNewId(), title, body });
  try { await ovPersist(); } catch (e) { showToast(e.message, 'error'); }
}

// Replace an item by id in a section, or append when new.
function ovUpsert(section, item) {
  const arr = overviewData[section];
  const idx = arr.findIndex(x => x.id === item.id);
  if (idx >= 0) arr[idx] = item; else arr.push(item);
}

// ── Tickets ───────────────────────────────────────────
async function loadTickets() {
  if (!currentProject) return;
  const tbody = document.getElementById('ticket-tbody');
  tbody.innerHTML = '<tr><td colspan="8" style="padding:16px;color:#999">Loading…</td></tr>';

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
    // Cache the project's effort→days map so the detail panel can show what each
    // t-shirt size resolves to (non-fatal if it fails).
    try {
      effortToDaysMap = (await API.get(`/api/projects/${currentProject.id}/effort-to-days`)).effort_to_days || {};
    } catch { /* leave the previous map in place */ }
    populatePhaseFilter(allTickets);
    populateTagFilter(allTickets);
    populateAssigneeFilter(allTickets);
    renderTickets();
  } catch (e) {
    tbody.innerHTML = `<tr><td colspan="8" style="padding:16px;color:red">${e.message}</td></tr>`;
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

// ── Predecessor readiness & work-order ─────────────────
// A predecessor counts as "done" once it is complete or closed (matches the
// backend's terminal statuses). Readiness is computed against the full loaded
// ticket set (allTickets) so closed/filtered predecessors are still counted.
const DONE_STATUSES = new Set(['complete', 'closed']);
function isDone(t) { return !!t && DONE_STATUSES.has(t.status); }

// Returns { state: 'none'|'ready'|'blocked', blocking: N } for one ticket.
// Orphaned predecessor refs (not in the set) are treated as satisfied, mirroring
// the backend balance engine.
function predecessorState(t, byId) {
  const preds = t.predecessors || [];
  if (preds.length === 0) return { state: 'none', blocking: 0 };
  let blocking = 0;
  for (const pid of preds) {
    const p = byId.get(pid);
    if (p && !isDone(p)) blocking++;
  }
  return { state: blocking > 0 ? 'blocked' : 'ready', blocking };
}

// Inline badge shown next to the title. Nothing for done tickets or tickets
// without predecessors.
function depBadge(t, byId) {
  if (isDone(t)) return '';
  const { state, blocking } = predecessorState(t, byId);
  if (state === 'ready') return ' <span class="badge dep-ready">Ready</span>';
  if (state === 'blocked') return ` <span class="badge dep-blocked">Blocked · ${blocking}</span>`;
  return '';
}

// Topological depth = longest chain of *unfinished* predecessors. Ready tickets
// (no unfinished preds) are depth 0; a ticket always ranks after its blockers.
// Cycle-safe via the recursion stack guard.
function computeWorkOrder(tickets, byId) {
  const depth = new Map();
  function computeDepth(id, stack) {
    if (depth.has(id)) return depth.get(id);
    if (stack.has(id)) return 0; // cycle: stop climbing
    stack.add(id);
    const t = byId.get(id);
    let d = 0;
    if (t) {
      for (const pid of (t.predecessors || [])) {
        const p = byId.get(pid);
        if (p && !isDone(p)) d = Math.max(d, 1 + computeDepth(pid, stack));
      }
    }
    stack.delete(id);
    depth.set(id, d);
    return d;
  }
  tickets.forEach(t => computeDepth(t.id, new Set()));
  return depth;
}

// ── Parent/child via reserved `parent:<ID>` tags ───────
// A child carries one tag `parent:<PARENT_TICKET_ID>`. No schema change — the
// parent is an ordinary ticket and the link is just a conventionally-named tag.
const PARENT_TAG_PREFIX = 'parent:';
function isReservedTag(tag) { return tag.startsWith(PARENT_TAG_PREFIX); }
const BACKLOG_TAG = 'backlog';
function isBacklogTicket(t) { return (t.tags || []).includes(BACKLOG_TAG); }
function visibleTags(t) { return (t.tags || []).filter(x => !isReservedTag(x)); }
function parentIdOf(t) {
  const tag = (t.tags || []).find(isReservedTag);
  return tag ? tag.slice(PARENT_TAG_PREFIX.length).trim() : null;
}
function childrenOf(parentId) {
  if (!parentId) return [];
  return (allTickets || []).filter(t => (t.tags || []).includes(PARENT_TAG_PREFIX + parentId));
}
function childCountMap() {
  const m = new Map();
  (allTickets || []).forEach(t => { const p = parentIdOf(t); if (p) m.set(p, (m.get(p) || 0) + 1); });
  return m;
}

// The ticket currently shown in the detail panel — lets the tag editors below
// preserve the *other* category of tags when one category is edited.
let panelTicket = null;

// Edit visible (non-parent) tags, preserving the parent tag.
function setVisibleTags(id, raw) {
  const tags = (panelTicket && panelTicket.id === id ? panelTicket.tags : []) || [];
  const entered = raw.split(',').map(s => s.trim()).filter(Boolean).filter(x => !isReservedTag(x));
  editField(id, 'tags', [...entered, ...tags.filter(isReservedTag)]);
}
// Set or clear the parent, preserving all non-parent tags. Empty input clears it.
function setParent(id, raw) {
  const tags = (panelTicket && panelTicket.id === id ? panelTicket.tags : []) || [];
  const pid = (raw || '').trim();
  if (pid === id) { showToast("A ticket can't be its own parent", 'error'); renderTicketPanel(panelTicket); return; }
  const kept = tags.filter(x => !isReservedTag(x));
  editField(id, 'tags', pid ? [...kept, PARENT_TAG_PREFIX + pid] : kept);
}

function renderTicketTable(tickets) {
  const tbody = document.getElementById('ticket-tbody');
  const mode = document.getElementById('filter-sort').value || 'start';
  const byId = new Map((allTickets || []).map(t => [t.id, t]));
  const kidCount = childCountMap();

  if (mode === 'work') {
    const depth = computeWorkOrder(tickets, byId);
    // Committed-active first, then backlog, then done — so uncommitted/finished
    // work never leads the order.
    const rank = t => isDone(t) ? 2 : (isBacklogTicket(t) ? 1 : 0);
    tickets.sort((a, b) =>
         (rank(a) - rank(b))
      || ((depth.get(a.id) || 0) - (depth.get(b.id) || 0))     // dependency order: ready before blocked
      || (a.phase || '~').localeCompare(b.phase || '~')        // within a stage: phase order (Discovery→Build→…); unphased last
      || cmpDate(a.planned_start_date, b.planned_start_date)
      || cmpDate(a.due_date, b.due_date)
      || ((PRIORITY_ORDER[a.priority] ?? 99) - (PRIORITY_ORDER[b.priority] ?? 99))
      || a.id.localeCompare(b.id));
  } else {
    tickets.sort((a, b) => compareTickets(a, b, mode));
  }

  if (tickets.length === 0) {
    tbody.innerHTML = '<tr><td colspan="8" style="padding:16px;color:#999">No tickets. Create one with "+ New Ticket".</td></tr>';
    return;
  }
  tbody.innerHTML = tickets.map(t => `
    <tr data-id="${t.id}">
      <td style="text-align:center;padding:0;white-space:nowrap">${gutterMarks(t)}</td>
      <td><strong>${t.id}</strong></td>
      <td>${t.title}${isBacklogTicket(t) ? ' <span class="badge backlog-badge">Backlog</span>' : ''}${depBadge(t, byId)}${kidCount.get(t.id) ? ` <span class="badge child-badge" title="${kidCount.get(t.id)} child ticket(s)">↳ ${kidCount.get(t.id)}</span>` : ''}</td>
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

// Tag filter: parent/child "Children of <id>" entries first, then plain tags.
// Built with DOM APIs (textContent/value) so user-defined tags can't inject HTML.
function populateTagFilter(tickets) {
  const sel = document.getElementById('filter-tag');
  const current = sel.value;
  const all = new Set();
  tickets.forEach(t => (t.tags || []).forEach(tag => all.add(tag)));
  const parents = [...all].filter(isReservedTag).sort();
  const plain = [...all].filter(x => !isReservedTag(x)).sort();

  sel.innerHTML = '';
  const allOpt = document.createElement('option');
  allOpt.value = ''; allOpt.textContent = 'All tags';
  sel.appendChild(allOpt);

  const addGroup = (label, items, labelFor) => {
    if (!items.length) return;
    const g = document.createElement('optgroup');
    g.label = label;
    items.forEach(tag => {
      const o = document.createElement('option');
      o.value = tag; o.textContent = labelFor(tag);
      g.appendChild(o);
    });
    sel.appendChild(g);
  };
  addGroup('Parent / child', parents, tag => `Children of ${tag.slice(PARENT_TAG_PREFIX.length)}`);
  addGroup('Tags', plain, tag => tag);

  sel.value = current;
}

// Assignee filter: people who actually have tickets, by name (value = email),
// plus an Unassigned option when any ticket has no assignee.
function populateAssigneeFilter(tickets) {
  const sel = document.getElementById('filter-assignee');
  const current = sel.value;
  const emails = [...new Set(tickets.map(t => t.assignee).filter(Boolean))].sort();
  sel.innerHTML = '<option value="">All assignees</option>';
  if (tickets.some(t => !t.assignee)) {
    const o = document.createElement('option');
    o.value = '__unassigned__'; o.textContent = 'Unassigned';
    sel.appendChild(o);
  }
  emails.forEach(email => {
    const o = document.createElement('option');
    o.value = email; o.textContent = resolveResourceName(email);
    sel.appendChild(o);
  });
  sel.value = [...sel.options].some(o => o.value === current) ? current : '';
}

// The client-side filters applied to allTickets: hide-closed, tag, assignee.
function visibleTickets() {
  const hideClosed = document.getElementById('filter-hide-closed').checked;
  const tagFilter = document.getElementById('filter-tag').value;
  const assigneeFilter = document.getElementById('filter-assignee').value;
  let visible = hideClosed ? allTickets.filter(t => t.status !== 'closed' && t.status !== 'complete') : allTickets;
  if (tagFilter) visible = visible.filter(t => (t.tags || []).includes(tagFilter));
  if (assigneeFilter === '__unassigned__') visible = visible.filter(t => !t.assignee);
  else if (assigneeFilter) visible = visible.filter(t => t.assignee === assigneeFilter);
  return visible;
}

// Render the ticket set in whichever view is active (flat list vs dependency plan).
function renderTickets() {
  const planActive = ticketView === 'plan';
  const table = document.getElementById('ticket-table');
  const plan = document.getElementById('ticket-plan');
  if (table) table.classList.toggle('hidden', planActive);
  if (plan) plan.classList.toggle('hidden', !planActive);
  const visible = visibleTickets();
  if (planActive) renderTicketPlan(visible);
  else renderTicketTable(visible);
}

// Compute dependency stages + critical path from the loaded tickets (mirrors the
// server's execution-plan logic). Backlog excluded; parents tracked separately.
function computeExecPlan(all) {
  const byId = {};
  all.forEach(t => { if (!isBacklogTicket(t)) byId[t.id] = t; });
  const ids = Object.keys(byId);
  const preds = {};
  ids.forEach(id => { preds[id] = (byId[id].predecessors || []).filter(p => byId[p]); });
  const wave = {};
  const waveOf = (id, stk) => {
    if (id in wave) return wave[id];
    if (stk.has(id)) return 0;
    stk.add(id);
    let best = 0;
    for (const p of preds[id]) best = Math.max(best, waveOf(p, stk) + 1);
    stk.delete(id);
    wave[id] = best; return best;
  };
  ids.forEach(id => waveOf(id, new Set()));
  const dur = id => {
    const e = byId[id] && byId[id].effort;
    const d = e ? effortToDaysMap[e] : 0;
    return d == null ? 0 : d;
  };
  const ef = {};
  const efOf = (id, stk) => {
    if (id in ef) return ef[id];
    if (stk.has(id)) return 0;
    stk.add(id);
    let best = 0;
    for (const p of preds[id]) best = Math.max(best, efOf(p, stk));
    stk.delete(id);
    ef[id] = best + dur(id); return ef[id];
  };
  ids.forEach(id => efOf(id, new Set()));
  let endNode = null, maxEF = -1;
  ids.slice().sort().forEach(id => { if (ef[id] > maxEF) { maxEF = ef[id]; endNode = id; } });
  const critical = new Set();
  let cur = endNode;
  while (cur) {
    critical.add(cur);
    const want = ef[cur] - dur(cur);
    let next = null;
    for (const p of preds[cur]) { if (Math.abs(ef[p] - want) < 1e-9) { next = p; break; } }
    cur = next;
  }
  const isParent = new Set();
  all.forEach(t => (t.tags || []).forEach(tag => { if (tag.startsWith('parent:')) isParent.add(tag.slice(7)); }));
  return { byId, preds, wave, dur, critical, isParent };
}

// Interactive dependency-stage view: filtered tickets grouped by stage, each row
// clickable to open/start it.
function renderTicketPlan(visible) {
  const el = document.getElementById('ticket-plan');
  if (!allTickets.length) { el.innerHTML = '<p style="color:#999;padding:16px">No tickets.</p>'; return; }
  const P = computeExecPlan(allTickets);
  const HPD = 8;
  const stages = {};
  visible.forEach(t => {
    if (P.isParent.has(t.id) || !P.byId[t.id]) return; // skip parents/backlog
    const w = P.wave[t.id] ?? 0;
    (stages[w] = stages[w] || []).push(t.id);
  });
  const stageNums = Object.keys(stages).map(Number).sort((a, b) => a - b);
  if (!stageNums.length) { el.innerHTML = '<p style="color:#999;padding:16px">No tickets match the current filters.</p>'; return; }
  const stageDays = {}; let maxStageDays = 0;
  stageNums.forEach(w => { stageDays[w] = stages[w].reduce((s, id) => s + P.dur(id), 0); if (stageDays[w] > maxStageDays) maxStageDays = stageDays[w]; });
  const blocks = stageNums.map(w => {
    const list = stages[w].slice().sort((a, b) => {
      const ca = P.critical.has(a), cb = P.critical.has(b);
      if (ca !== cb) return ca ? -1 : 1;
      return a < b ? -1 : 1;
    });
    const bar = maxStageDays > 0 ? stageDays[w] / maxStageDays * 100 : 0;
    const crit = list.filter(id => P.critical.has(id)).length;
    const count = list.length === 1 ? '<strong>1</strong> ticket' : `<strong>${list.length}</strong> tickets, independent (can run at once)`;
    const note = w === 0 ? ' · <span style="color:#16a34a">can start now</span>' : '';
    const critNote = crit > 0 ? ` · <span style="color:#dc2626">${crit} on critical path ★</span>` : '';
    const rows = list.map(id => {
      const t = P.byId[id];
      const star = P.critical.has(id) ? '<span style="color:#dc2626">★</span> ' : '';
      const needs = (P.preds[id] || []).filter(p => !P.isParent.has(p)).map(p => `${p} (stage ${(P.wave[p] ?? 0) + 1})`);
      const nstr = needs.length ? ` <span style="color:#b45309;font-size:11px">needs ${escapeHtml(needs.join(', '))}</span>` : '';
      const eff = P.dur(id) ? ` <span style="color:#aaa;font-size:11px">${(P.dur(id) * HPD).toFixed(0)}h</span>` : '';
      return `<div onclick="openTicketPanel('${id}')" style="cursor:pointer;padding:5px 8px 5px 22px;display:flex;gap:8px;align-items:center;border-radius:4px" onmouseover="this.style.background='#f8fafc'" onmouseout="this.style.background=''">
        ${statusBadge(t.status)}
        <span style="font-weight:600;color:#1565c0">${id}</span>
        <span style="flex:1;min-width:0">${star}${escapeHtml(t.title)}</span>${eff}${nstr}
      </div>`;
    }).join('');
    const open = w === 0 ? ' open' : '';
    return `<details${open} style="border:1px solid #e5e7eb;border-radius:8px;margin-bottom:10px">
      <summary style="cursor:pointer;padding:10px 12px">
        <span style="display:inline-flex;align-items:center;gap:10px;flex-wrap:wrap">
          <strong style="color:#334155">Stage ${w + 1}</strong>
          <span style="display:inline-block;width:120px;height:12px;background:#f1f5f9;border-radius:3px"><span style="display:block;height:100%;width:${bar.toFixed(1)}%;min-width:3px;background:#3b82f6;border-radius:3px"></span></span>
          <span style="font-size:12.5px;color:#334155">${count} · <span style="color:#0d9488;font-weight:600">Σ ${(stageDays[w] * HPD).toFixed(0)}h ≈ ${stageDays[w].toFixed(0)}d</span>${note}${critNote}</span>
        </span>
      </summary>
      <div style="padding:2px 6px 8px">${rows}</div>
    </details>`;
  }).join('');
  el.innerHTML = `<div style="max-width:1000px;margin:0 auto">
    <p style="font-size:12px;color:#777;margin:4px 0 12px">A <strong>stage</strong> groups tickets that don't depend on each other, so they can run at once. <strong>Click a ticket to open it and start work.</strong> <span style="color:#b45309">needs</span> points to earlier stages that must finish first. Respects the filters above.</p>
    ${blocks}</div>`;
}

function setTicketView(v) {
  ticketView = v;
  localStorage.setItem('hate:ticketview', v);
  const l = document.getElementById('view-list'), p = document.getElementById('view-plan');
  l.style.background = v === 'list' ? '#2563eb' : '#fff'; l.style.color = v === 'list' ? '#fff' : '#334155';
  p.style.background = v === 'plan' ? '#2563eb' : '#fff'; p.style.color = v === 'plan' ? '#fff' : '#334155';
  renderTickets();
}
document.getElementById('view-list').addEventListener('click', () => setTicketView('list'));
document.getElementById('view-plan').addEventListener('click', () => setTicketView('plan'));
(function initTicketView() {
  const v = ticketView, l = document.getElementById('view-list'), p = document.getElementById('view-plan');
  l.style.background = v === 'list' ? '#2563eb' : '#fff'; l.style.color = v === 'list' ? '#fff' : '#334155';
  p.style.background = v === 'plan' ? '#2563eb' : '#fff'; p.style.color = v === 'plan' ? '#fff' : '#334155';
})();

document.getElementById('filter-status').addEventListener('change', loadTickets);
document.getElementById('filter-type').addEventListener('change', loadTickets);
document.getElementById('filter-phase').addEventListener('change', loadTickets);
document.getElementById('filter-tag').addEventListener('change', loadTickets);
document.getElementById('filter-assignee').addEventListener('change', loadTickets);
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
  renderTickets();
});

// ── Ticket detail panel ──────────────────────────────
async function openTicketPanel(ticketId) {
  pendingPromoteAfterLog = null; // scope any gated-promote intent to one panel session
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

// Resolve an effort t-shirt size to its configured days/hours, e.g. "m (3d · 24h)".
// Hours use the same 8h/day basis as the schedule and the hours-at-risk rule.
function effortLabel(e) {
  if (!e) return '—';
  const d = effortToDaysMap[e];
  if (d == null) return e;
  const days = +(+d).toFixed(2);
  const hours = +(d * 8).toFixed(2);
  return `${e} <span style="color:#888;font-weight:400">(${days}d · ${hours}h)</span>`;
}

// Label the create-ticket effort options with their configured hours, e.g. "M (24h)".
function refreshEffortOptions() {
  const sel = document.getElementById('nt-effort');
  if (!sel) return;
  [...sel.options].forEach(o => {
    if (!o.value) return; // leave the blank "—" option alone
    const d = effortToDaysMap[o.value];
    o.textContent = d == null ? o.value.toUpperCase() : `${o.value.toUpperCase()} (${+(d * 8).toFixed(2)}h)`;
  });
}

// Auto-size a test-case textarea to its content so long steps/expected/comments
// wrap and stay fully readable instead of clipping.
function autoGrowTC(el) {
  el.style.height = 'auto';
  el.style.height = el.scrollHeight + 'px';
}

// Gutter markers for the ticket list: red ✕ = blocked, blue ! = hours at risk (≥90%).
function gutterMarks(t) {
  const badge = (bg, glyph, title) =>
    `<span title="${title}" style="display:inline-flex;align-items:center;justify-content:center;width:16px;height:16px;border-radius:50%;background:${bg};color:#fff;font-weight:800;font-size:11px;line-height:1">${glyph}</span>`;
  const marks = [];
  if (t.status === 'blocked') marks.push(badge('#dc2626', '✕', 'Blocked'));
  if (t.at_risk) marks.push(badge('#2563eb', '!', "Logged hours ≥ 90% of this ticket's allotment"));
  return marks.join('&nbsp;');
}

function renderTicketPanel(t) {
  panelTicket = t;
  const content = document.getElementById('panel-content');
  const typeOptions = TYPE_VALUES.map(v =>
    `<option value="${v}" ${t.type===v?'selected':''}>${TYPE_LABELS[v]}</option>`).join('');
  const fields = [
    ['Type', `<select class="inline-edit" onchange="editField('${t.id}','type',this.value)">${typeOptions}</select>`],
    ['Status', statusBadge(t.status)],
    ['Priority', priorityCell(t.priority)], ['Effort', effortLabel(t.effort)],
    ['Assignee', resolveResourceName(t.assignee)], ['Creator', resolveResourceName(t.creator)],
    ['Due date', t.due_date || '—'], ['Planned start', t.planned_start_date || '—'],
    ['Actual start', t.actual_start_date || '—'],
    ['Predecessors', t.predecessors.length ? linkifyTicketRefs(t.predecessors.join(', ')) : '—'],
    ['Parent', `${parentIdOf(t) ? linkifyTicketRefs(parentIdOf(t)) + ' ' : ''}<input class="inline-edit" value="${parentIdOf(t) || ''}" onblur="setParent('${t.id}', this.value)" placeholder="parent ticket id" style="max-width:150px">`],
    ['Tags', `<input class="inline-edit" value="${visibleTags(t).join(', ')}" onblur="setVisibleTags('${t.id}', this.value)" placeholder="comma separated">`],
    ['Phase', `<input class="inline-edit" value="${t.phase||''}" onblur="editField('${t.id}','phase',this.value.trim()||null)" placeholder="e.g. Discovery">`],
  ];

  const activity = [...(t.activity || [])].reverse().slice(0, 20).map(a => {
    // Comments can be full markdown; system events are short single-line strings.
    const detail = !a.detail ? ''
      : a.action === 'comment'
        ? `<div class="md act-md">${renderMarkdown(a.detail)}</div>`
        : ': ' + escapeAndLinkify(a.detail);
    return `
    <li class="activity-item">
      <div class="act-time">${a.timestamp.replace('T',' ').replace('Z','')} ${a.author ? '· ' + a.author.split('@')[0] : ''}</div>
      <div class="act-detail"><strong>${a.action}</strong>${detail}</div>
    </li>`;
  }).join('');

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
    ? `<div style="background:#fff3e0;border-left:4px solid #e65100;padding:8px 12px;margin:8px 0;font-size:13px"><strong>⏩ Force closed</strong>${t.closed_at ? ' on ' + t.closed_at.slice(0,10) : ''}: ${escapeAndLinkify(t.cancellation_reason)}</div>`
    : '';

  const kids = childrenOf(t.id);
  const kidsDone = kids.filter(isDone).length;
  const childrenSection = kids.length ? `
    <div class="panel-section" style="margin-top:16px">
      <h4>Children <span style="font-size:12px;color:#999;font-weight:normal">(${kids.length}${kidsDone ? ` · ${kidsDone} done` : ''})</span></h4>
      <ul class="child-list">
        ${kids.map(c => `<li>${linkifyTicketRefs(c.id)} <span class="child-title">${escapeHtml(c.title)}</span> ${statusBadge(c.status)}</li>`).join('')}
      </ul>
    </div>` : '';

  const tcs = t.test_cases || [];
  const tcPass = tcs.filter(c => c.status === 'pass').length;
  const tcFail = tcs.filter(c => c.status === 'fail').length;
  const tcSummary = tcs.length ? ` · ${tcPass} pass${tcFail ? `, ${tcFail} fail` : ''}` : '';
  const tcStatusBtn = (active, color, label, title, onclick) =>
    `<button title="${title}" onclick="${onclick}" style="cursor:pointer;width:26px;height:24px;border-radius:4px;border:1px solid ${active ? color : '#ddd'};background:${active ? color : '#fff'};color:${active ? '#fff' : '#bbb'};font-weight:800;font-size:13px;line-height:1">${label}</button>`;
  const tcRows = tcs.map((c, i) => `
      <div style="border:1px solid #e5e7eb;border-radius:6px;padding:8px 10px;margin-bottom:8px;display:flex;gap:8px;align-items:flex-start">
        <span style="font-weight:700;color:#bbb;font-size:12px;padding-top:5px">${i + 1}</span>
        <div style="flex:1;min-width:0">
          <textarea class="tc-ta" rows="1" placeholder="Step / action" oninput="autoGrowTC(this)" onblur="updateTestCase('${t.id}','${c.id}','step',this.value)" style="width:100%;box-sizing:border-box;border:none;border-bottom:1px solid #eee;font-size:13px;padding:3px 0;resize:none;overflow:hidden;font-family:inherit;line-height:1.45;background:transparent">${escapeHtml(c.step)}</textarea>
          <textarea class="tc-ta" rows="1" placeholder="Expected result" oninput="autoGrowTC(this)" onblur="updateTestCase('${t.id}','${c.id}','expected',this.value)" style="width:100%;box-sizing:border-box;border:none;border-bottom:1px solid #eee;font-size:13px;padding:3px 0;resize:none;overflow:hidden;font-family:inherit;line-height:1.45;color:#555;background:transparent">${escapeHtml(c.expected)}</textarea>
          <textarea class="tc-ta" rows="1" placeholder="QA comment — what happened / how to reproduce" oninput="autoGrowTC(this)" onblur="updateTestCase('${t.id}','${c.id}','comment',this.value)" style="width:100%;box-sizing:border-box;border:none;font-size:12px;padding:4px 0 0;resize:none;overflow:hidden;font-family:inherit;line-height:1.45;color:#777;background:transparent">${escapeHtml(c.comment || '')}</textarea>
        </div>
        <div style="display:flex;gap:4px;align-items:center;padding-top:2px">
          ${tcStatusBtn(c.status === 'pass', '#16a34a', '✓', 'Pass', `setTestCaseStatus('${t.id}','${c.id}','${c.status === 'pass' ? '' : 'pass'}')`)}
          ${tcStatusBtn(c.status === 'fail', '#dc2626', '✕', 'Fail', `setTestCaseStatus('${t.id}','${c.id}','${c.status === 'fail' ? '' : 'fail'}')`)}
          <button class="btn-delete-time" title="Delete case" onclick="deleteTestCase('${t.id}','${c.id}')">🗑</button>
        </div>
      </div>`).join('');
  const testCasesSection = `
    <div class="panel-section" style="margin-top:16px">
      <h4>Test Cases <span style="font-size:12px;color:#999;font-weight:normal">(${tcs.length}${tcSummary})</span></h4>
      ${tcs.length ? tcRows : '<p style="color:#999;font-size:13px;margin:4px 0 8px">No test cases yet — add how to test this so QA (or whoever inherits it) can reproduce it.</p>'}
      <div style="display:flex;flex-direction:column;gap:6px;margin-top:8px">
        <input id="tc-step-${t.id}" placeholder="Step / action" onkeydown="tcAddKey(event,'${t.id}')" style="width:100%;box-sizing:border-box;padding:6px;border:1px solid #ddd;border-radius:4px;font-size:13px">
        <input id="tc-expected-${t.id}" placeholder="Expected result (Enter to add)" onkeydown="tcAddKey(event,'${t.id}')" style="width:100%;box-sizing:border-box;padding:6px;border:1px solid #ddd;border-radius:4px;font-size:13px">
        <button class="btn-secondary" style="align-self:flex-start" onclick="addTestCase('${t.id}')">+ Add case</button>
      </div>
      <details style="margin-top:8px">
        <summary style="cursor:pointer;font-size:12px;color:#555">Paste multiple &mdash; one per line: <code>action | expected</code></summary>
        <textarea id="tc-bulk-${t.id}" rows="4" placeholder="Select radio = Business | Dropdown shows Sales / Support / Billing&#10;Submit with empty comment | Validation error, nothing written" style="width:100%;box-sizing:border-box;padding:6px;border:1px solid #ddd;border-radius:4px;font-size:12px;margin-top:6px"></textarea>
        <button class="btn-secondary" onclick="addTestCasesBulk('${t.id}')">Add cases</button>
      </details>
    </div>`;

  content.innerHTML = `
    <div class="panel-section">
      <h4>${t.title} <span class="time-badge">${totalHours.toFixed(2)}h</span></h4>
      ${cancelBanner}
      ${t.description ? `<div class="md" style="margin-top:8px">${renderMarkdown(t.description)}</div>` : ''}
    </div>
    <div class="panel-section">
      ${fields.map(([l, v]) => `<div class="field-row"><span class="field-label">${l}</span><span class="field-value">${v}</span></div>`).join('')}
    </div>
    ${childrenSection}
    ${testCasesSection}
    <div class="panel-actions">
      ${WORKFLOW_TYPES.includes(t.type) ? `
      <button class="btn-primary" onclick="promoteTicket('${t.id}')">▲ Promote</button>
      <button class="btn-secondary" onclick="demoteTicket('${t.id}')">▼ Demote</button>
      ${t.status !== 'blocked' ? `<button class="btn-secondary" onclick="blockTicket('${t.id}','${(t.title||'').replace(/'/g, "\\'")}')">⛔ Block</button>` : ''}` : ''}
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
  // Size every test-case textarea to fit its text.
  document.querySelectorAll('#panel-content .tc-ta').forEach(autoGrowTC);
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

// When a promote is gated for missing time, remember the intent so the next
// successful time log on that ticket auto-retries the promote.
let pendingPromoteAfterLog = null;

async function promoteTicket(id) {
  try {
    const r = await fetch(`/api/projects/${currentProject.id}/tickets/${id}/promote?author=${encodeURIComponent(currentUser?.email || '')}`, { method: 'POST' });
    const data = await r.json();
    if (!r.ok) {
      if (data.needs_time_log) { promptTimeToPromote(id, data.detail); return; }
      throw new Error(data.detail || data.error || r.statusText);
    }
    showToast(`${id} → ${data.status}`);
    renderTicketPanel(data);
    loadTickets();
  } catch (e) { showToast(e.message, 'error'); }
}

// Gated promote: pop the log-time box focused on the description, and arm an
// auto-promote for once the time is logged.
function promptTimeToPromote(id, detail) {
  pendingPromoteAfterLog = id;
  const box = document.getElementById(`time-box-${id}`);
  if (box) box.classList.remove('hidden');
  const desc = document.getElementById(`time-desc-${id}`);
  if (desc) desc.focus();
  showToast(detail || 'Log time (with a description) to promote', 'error');
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

// Open the block modal to capture a reason. The submit happens in the block-form
// handler; the reason lands on the ticket and the PM dashboard's blocked list.
function blockTicket(id, title) {
  document.getElementById('bl-reason').value = '';
  document.getElementById('bl-ticket-label').textContent = `${id}${title ? ' — ' + title : ''}`;
  const overlay = document.getElementById('block-modal-overlay');
  overlay.dataset.ticketId = id;
  overlay.classList.remove('hidden');
  setTimeout(() => document.getElementById('bl-reason').focus(), 0);
}

async function editField(id, field, value) {
  try {
    const t = await API.patch(`/api/projects/${currentProject.id}/tickets/${id}`, { field, value, author: currentUser?.email || '' });
    showToast(`${field} updated`);
    renderTicketPanel(t);
    loadTickets();
  } catch (e) { showToast(e.message, 'error'); }
}

async function addTestCase(id) {
  const step = document.getElementById(`tc-step-${id}`).value.trim();
  const expected = document.getElementById(`tc-expected-${id}`).value.trim();
  if (!step && !expected) { showToast('Enter a step or expected result', 'error'); return; }
  try {
    const t = await API.post(`/api/projects/${currentProject.id}/tickets/${id}/test-cases`, { step, expected, author: currentUser?.email || '' });
    renderTicketPanel(t);
    const el = document.getElementById(`tc-step-${id}`); if (el) el.focus(); // keep authoring without the mouse
  } catch (e) { showToast(e.message, 'error'); }
}
// Enter in either add field commits the case (keyboard-first authoring).
function tcAddKey(e, id) {
  if (e.key === 'Enter') { e.preventDefault(); addTestCase(id); }
}
// Paste-to-author: one case per line, "action | expected" (expected optional).
async function addTestCasesBulk(id) {
  const raw = document.getElementById(`tc-bulk-${id}`).value;
  const cases = raw.split('\n').map(l => l.trim()).filter(Boolean).map(l => {
    const i = l.indexOf('|');
    return i >= 0 ? { step: l.slice(0, i).trim(), expected: l.slice(i + 1).trim() } : { step: l, expected: '' };
  });
  if (!cases.length) { showToast('Nothing to add — one case per line', 'error'); return; }
  try {
    const t = await API.post(`/api/projects/${currentProject.id}/tickets/${id}/test-cases/bulk`, { cases, author: currentUser?.email || '' });
    renderTicketPanel(t);
  } catch (e) { showToast(e.message, 'error'); }
}
// Field edits (step/expected/comment) fire on blur — refresh the cache but don't
// re-render, so we don't yank focus mid-edit. Status/add/delete re-render.
async function updateTestCase(id, caseId, field, value) {
  try {
    const body = { author: currentUser?.email || '' };
    body[field] = value;
    panelTicket = await API.patch(`/api/projects/${currentProject.id}/tickets/${id}/test-cases/${caseId}`, body);
  } catch (e) { showToast(e.message, 'error'); }
}
async function setTestCaseStatus(id, caseId, status) {
  try {
    const t = await API.patch(`/api/projects/${currentProject.id}/tickets/${id}/test-cases/${caseId}`, { status, author: currentUser?.email || '' });
    renderTicketPanel(t);
  } catch (e) { showToast(e.message, 'error'); }
}
async function deleteTestCase(id, caseId) {
  try {
    const t = await API.delete(`/api/projects/${currentProject.id}/tickets/${id}/test-cases/${caseId}?author=${encodeURIComponent(currentUser?.email || '')}`);
    renderTicketPanel(t);
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

// Holds a time log that was blocked by strict enforcement, pending the
// extension authorization from the modal.
let pendingTimeExtend = null;

async function submitTimeEntry(id) {
  const date = document.getElementById(`time-date-${id}`)?.value;
  const hours = parseFloat(document.getElementById(`time-hours-${id}`)?.value);
  const desc = document.getElementById(`time-desc-${id}`)?.value?.trim();
  if (!date || !hours || !desc) { showToast('Fill in date, hours, and description', 'error'); return; }
  await postTimeEntry(id, { date, hours, description: desc });
}

// postTimeEntry sends the log via a raw fetch so it can detect the strict-mode
// 409 (needs_time_extension) and open the authorization modal. When `extend`
// is provided, it re-sends with the authorization attached.
async function postTimeEntry(id, { date, hours, description, extend }) {
  const body = { date, hours, description, author: currentUser?.email || '' };
  if (extend) { body.extend_authorized = true; body.extend_reason = extend; }
  try {
    const r = await fetch(`/api/projects/${currentProject.id}/tickets/${id}/time`, {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify(body),
    });
    const data = await r.json();
    if (!r.ok) {
      if (data.needs_time_extension) {
        openTimeExtendModal(id, { date, hours, description }, data);
        return;
      }
      throw new Error(data.detail || r.statusText);
    }
    showToast(`Logged ${hours}h`);
    renderTicketPanel(data);
    if (pendingPromoteAfterLog === id) {
      pendingPromoteAfterLog = null;
      promoteTicket(id);
    }
  } catch (e) { showToast(e.message, 'error'); }
}

function openTimeExtendModal(id, entry, info) {
  pendingTimeExtend = { id, entry };
  const over = (info.would_be_hours - info.allotted_hours);
  document.getElementById('te-context').innerHTML =
    `Logging <strong>${entry.hours}h</strong> brings <strong>${id}</strong> to ` +
    `<strong>${info.would_be_hours}h</strong> — <strong>${(+over.toFixed(2))}h over</strong> its ` +
    `${info.allotted_hours}h allotment. Confirm you're authorized to extend and record why.`;
  document.getElementById('te-authorized').checked = false;
  document.getElementById('te-reason').value = '';
  document.getElementById('time-extend-overlay').classList.remove('hidden');
  document.getElementById('te-reason').focus();
}

function closeTimeExtendModal() {
  document.getElementById('time-extend-overlay').classList.add('hidden');
  pendingTimeExtend = null;
}

document.getElementById('btn-cancel-time-extend').addEventListener('click', closeTimeExtendModal);
document.getElementById('time-extend-overlay').addEventListener('click', (e) => {
  if (e.target === document.getElementById('time-extend-overlay')) closeTimeExtendModal();
});
document.getElementById('time-extend-form').addEventListener('submit', (e) => {
  e.preventDefault();
  if (!pendingTimeExtend) return;
  const reason = document.getElementById('te-reason').value.trim();
  if (!document.getElementById('te-authorized').checked || !reason) {
    showToast('Check the authorization box and enter a reason', 'error');
    return;
  }
  const { id, entry } = pendingTimeExtend;
  document.getElementById('time-extend-overlay').classList.add('hidden');
  pendingTimeExtend = null;
  postTimeEntry(id, { ...entry, extend: reason });
});

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

// ── Phase rollup ─────────────────────────────────────
let lastPhaseRollup = null;

document.getElementById('btn-phase-rollup').addEventListener('click', async () => {
  if (!currentProject) return;
  const btn = document.getElementById('btn-phase-rollup');
  btn.disabled = true; btn.textContent = 'Σ …';
  try {
    const report = await API.get(`/api/projects/${currentProject.id}/phase-rollup`);
    lastPhaseRollup = report;
    renderPhaseRollup(report);
    document.getElementById('phase-rollup-modal-overlay').classList.remove('hidden');
  } catch (e) { showToast(e.message, 'error'); }
  finally { btn.disabled = false; btn.textContent = 'Σ Phases'; }
});

document.getElementById('btn-close-phase-rollup').addEventListener('click', () => {
  document.getElementById('phase-rollup-modal-overlay').classList.add('hidden');
});
document.getElementById('phase-rollup-modal-overlay').addEventListener('click', (e) => {
  if (e.target === document.getElementById('phase-rollup-modal-overlay'))
    document.getElementById('phase-rollup-modal-overlay').classList.add('hidden');
});
document.getElementById('btn-phase-rollup-csv').addEventListener('click', () => {
  if (lastPhaseRollup) downloadPhaseRollupCSV(lastPhaseRollup);
  else showToast('Open a phase rollup first', 'error');
});

function pctText(v) { return (v == null ? 0 : v).toFixed(1) + '%'; }

function renderPhaseRollup(report) {
  const c = document.getElementById('phase-rollup-content');
  if (!report.phases || !report.phases.length) {
    c.innerHTML = `<div style="color:#666">No tickets to roll up yet.</div>`;
    return;
  }
  const bar = (v) => {
    const w = Math.max(0, Math.min(100, v || 0));
    return `<div style="background:#eee;border-radius:6px;height:8px;width:90px;display:inline-block;vertical-align:middle;overflow:hidden">
      <div style="background:#2e7d32;height:8px;width:${w}%"></div></div>`;
  };
  const rows = report.phases.map(p => {
    const flags = [];
    if (p.blocked_count) flags.push(`<span class="badge s-blocked">${p.blocked_count} blocked</span>`);
    if (!p.effort_based) flags.push(`<span class="badge s-not_started" title="No effort sizes in this phase — % is by ticket count">count-based</span>`);
    if (p.no_effort_count) flags.push(`<span style="color:#999;font-size:11px" title="Tickets with no effort size (invisible to the effort math)">${p.no_effort_count} unsized</span>`);
    if (p.cancelled_count) flags.push(`<span style="color:#999;font-size:11px" title="Force-closed / descoped — excluded from %">${p.cancelled_count} descoped</span>`);
    return `<tr style="border-bottom:1px solid #eee">
      <td style="padding:6px 8px"><strong>${escapeHtml(p.label)}</strong></td>
      <td style="padding:6px 8px;white-space:nowrap">${bar(p.percent_complete)} ${pctText(p.percent_complete)}</td>
      <td style="padding:6px 8px">${p.complete_count}/${p.ticket_count}</td>
      <td style="padding:6px 8px;white-space:nowrap">${p.done_effort_days}/${p.total_effort_days}d</td>
      <td style="padding:6px 8px;white-space:nowrap">${p.planned_start || '—'} → ${p.due_date || '—'}</td>
      <td style="padding:6px 8px">${flags.join(' ')}</td>
    </tr>`;
  }).join('');
  c.innerHTML = `
    <div style="margin-bottom:10px">Project overall: <strong>${pctText(report.percent_complete)}</strong>
      <span style="color:#999">— ${report.total_tickets} tickets, ${escapeHtml(report.basis)}</span></div>
    <table style="width:100%;border-collapse:collapse">
      <thead><tr style="text-align:left;border-bottom:2px solid #ddd;color:#555">
        <th style="padding:6px 8px">Phase</th><th style="padding:6px 8px">Complete</th>
        <th style="padding:6px 8px">Tickets</th><th style="padding:6px 8px">Effort (done/total)</th>
        <th style="padding:6px 8px">Dates</th><th style="padding:6px 8px"></th>
      </tr></thead>
      <tbody>${rows}</tbody>
    </table>
    <p style="color:#999;font-size:11px;margin-top:10px">Effort-weighted: % = done effort-days ÷ total effort-days.
      Descoped (force-closed) tickets are excluded; phases with no effort sizes fall back to ticket count.</p>`;
}

function downloadPhaseRollupCSV(report) {
  const head = ['phase', 'percent_complete', 'basis', 'tickets_total', 'tickets_complete',
    'in_progress', 'not_started', 'blocked', 'descoped', 'effort_days_total',
    'effort_days_done', 'unsized_tickets', 'planned_start', 'due_date'];
  const esc = (s) => `"${String(s == null ? '' : s).replace(/"/g, '""')}"`;
  const lines = [head.join(',')];
  (report.phases || []).forEach(p => {
    lines.push([
      esc(p.label), p.percent_complete, p.effort_based ? 'effort' : 'count',
      p.ticket_count, p.complete_count, p.in_progress_count, p.not_started_count,
      p.blocked_count, p.cancelled_count, p.total_effort_days, p.done_effort_days,
      p.no_effort_count, esc(p.planned_start || ''), esc(p.due_date || '')
    ].join(','));
  });
  lines.push([esc('TOTAL'), report.percent_complete, report.basis, report.total_tickets,
    '', '', '', '', '', '', '', '', '', ''].join(','));
  const blob = new Blob([lines.join('\n')], { type: 'text/csv' });
  const a = document.createElement('a');
  a.href = URL.createObjectURL(blob);
  a.download = `phase-rollup-${currentProject.id}-${new Date().toISOString().slice(0, 10)}.csv`;
  a.click();
  URL.revokeObjectURL(a.href);
  showToast('Phase rollup CSV downloaded');
}

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
  // Populate the phase picker with phases already used in this project (type-or-pick).
  const phases = [...new Set((allTickets || []).map(t => t.phase).filter(Boolean))].sort();
  const dl = document.getElementById('nt-phase-list');
  dl.innerHTML = phases.map(p => `<option value="${escapeHtml(p)}"></option>`).join('');
  refreshEffortOptions();
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

document.getElementById('block-form').addEventListener('submit', async (e) => {
  e.preventDefault();
  const overlay = document.getElementById('block-modal-overlay');
  const id = overlay.dataset.ticketId;
  const reason = document.getElementById('bl-reason').value.trim();
  try {
    const t = await API.post(`/api/projects/${currentProject.id}/tickets/${id}/block`, {
      reason,
      author: currentUser?.email || '',
    });
    overlay.classList.add('hidden');
    showToast(`${id} blocked`);
    renderTicketPanel(t);
    loadTickets();
  } catch (e) { showToast(e.message, 'error'); }
});
document.getElementById('btn-cancel-block').addEventListener('click', () => {
  document.getElementById('block-modal-overlay').classList.add('hidden');
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
  // COSMIC sizing (optional, standard types only) → tags. A self-contained ticket
  // can carry both a class and a CFP size; either is optional.
  if (!isAuto) {
    const tags = [];
    const cfp = parseInt(document.getElementById('nt-cfp').value, 10);
    if (Number.isFinite(cfp) && cfp > 0) tags.push(`cfp:${cfp}`);
    const cls = document.getElementById('nt-class').value;
    if (cls) tags.push(cls);
    if (tags.length) body.tags = tags;
  }
  try {
    const ticket = await API.post(`/api/projects/${currentProject.id}/tickets`, body);
    const msg = isAuto && body.hours ? `Created ${ticket.id} — ${body.hours}h logged` : `Created ${ticket.id}`;
    showToast(msg);
    document.getElementById('modal-overlay').classList.add('hidden');
    document.getElementById('new-ticket-form').reset();
    document.getElementById('nt-sizing').open = false;
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
    // Every project in one list: shown (tracked) and hidden together, each with
    // a Show / Hide toggle reflecting its current visibility.
    const [active, hidden] = await Promise.all([
      API.get('/api/projects'),
      API.get('/api/projects/hidden'),
    ]);
    const all = [
      ...active.map(p => ({ ...p, hidden: false })),
      ...hidden.map(p => ({ ...p, hidden: true })),
    ].sort((a, b) => a.name.localeCompare(b.name));

    const row = (p) => {
      const closeAction = p.closed_at
        ? `<button class="btn-secondary" onclick="reopenProjectFromManage('${p.id}')">Reopen</button>`
        : `<button class="btn-secondary" onclick="closeProjectFromManage('${p.id}', '${p.name.replace(/'/g, "\\'")}')">Close</button>`;
      const closedTag = p.closed_at ? `<span style="color:#e65100;font-size:12px;margin-left:6px">🔒 closed ${p.closed_at}</span>` : '';
      const ep = encodeURIComponent(p.path);
      const vis = `<span class="vis-toggle">
            <label><input type="radio" name="vis-${p.id}" ${!p.hidden ? 'checked' : ''} onchange="setProjectVisibility('${ep}', false)"> Show</label>
            <label><input type="radio" name="vis-${p.id}" ${p.hidden ? 'checked' : ''} onchange="setProjectVisibility('${ep}', true)"> Hide</label>
          </span>`;
      return `
        <tr>
          <td><strong>${p.name}</strong>${closedTag}<div class="mp-path">${p.path}</div></td>
          <td style="white-space:nowrap">${closeAction}</td>
          <td style="white-space:nowrap">${vis}</td>
        </tr>`;
    };

    container.innerHTML = all.length
      ? `<table class="manage-projects-table"><tbody>${all.map(row).join('')}</tbody></table>`
      : '<p style="color:#999;font-size:13px">No projects.</p>';
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

// Toggle a project's sidebar visibility from the Project List. hidden=true hides
// it from the sidebar (folder untouched); hidden=false shows it again.
async function setProjectVisibility(encodedPath, hidden) {
  const path = decodeURIComponent(encodedPath);
  try {
    await API.post(hidden ? '/api/projects/hide' : '/api/projects/unhide', { path });
    showToast(hidden ? 'Project hidden' : 'Project shown');
    // If we just hid the currently open project, drop to the welcome view.
    if (hidden && currentProject && currentProject.path === path) {
      currentProject = null;
      document.getElementById('project-view').classList.add('hidden');
      document.getElementById('welcome').classList.remove('hidden');
    }
    renderManageProjects();
    loadProjects();
  } catch (e) {
    showToast(e.message, 'error');
    renderManageProjects(); // revert the radio to the true state on failure
  }
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
    document.getElementById('show-cosmic').checked = cfg.show_cosmic || false;
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
  const mhInputs = document.getElementById('max-hours-inputs');
  const mhEmpty = document.getElementById('max-hours-empty');
  const mhProj = document.getElementById('max-hours-project');
  const stInputs = document.getElementById('strict-time-inputs');
  const stEmpty = document.getElementById('strict-time-empty');
  const stProj = document.getElementById('strict-time-project');
  if (!currentProject) {
    inputs.classList.add('hidden');
    note.classList.add('hidden');
    empty.classList.remove('hidden');
    projLabel.textContent = '';
    mhInputs.classList.add('hidden');
    mhEmpty.classList.remove('hidden');
    mhProj.textContent = '';
    stInputs.classList.add('hidden');
    stEmpty.classList.remove('hidden');
    stProj.textContent = '';
    return;
  }
  projLabel.textContent = `— ${currentProject.name || currentProject.id}`;
  mhProj.textContent = `— ${currentProject.name || currentProject.id}`;
  stProj.textContent = `— ${currentProject.name || currentProject.id}`;
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
  try {
    const hb = await API.get(`/api/projects/${currentProject.id}/hour-budget`);
    document.getElementById('work-hours').value = hb.work_hours ?? '';
    document.getElementById('admin-hours').value = hb.admin_hours ?? '';
    document.getElementById('qa-hours').value = hb.qa_hours ?? '';
    mhEmpty.classList.add('hidden');
    mhInputs.classList.remove('hidden');
  } catch (e) { showToast(e.message, 'error'); }
  try {
    const st = await API.get(`/api/projects/${currentProject.id}/strict-time`);
    document.getElementById('strict-time').checked = !!st.strict_time_enforcement;
    stEmpty.classList.add('hidden');
    stInputs.classList.remove('hidden');
  } catch (e) { showToast(e.message, 'error'); }
  try {
    const eq = await API.get(`/api/projects/${currentProject.id}/enforce-qa`);
    document.getElementById('enforce-qa').checked = !!eq.enforce_qa;
    document.getElementById('enforce-qa-project').textContent = '— ' + currentProject.name.toUpperCase();
    document.getElementById('enforce-qa-empty').classList.add('hidden');
    document.getElementById('enforce-qa-inputs').classList.remove('hidden');
  } catch (e) { showToast(e.message, 'error'); }
}

document.getElementById('settings-form').addEventListener('submit', async (e) => {
  e.preventDefault();
  const body = {
    projects_root: document.getElementById('settings-projects-root').value || undefined,
    show_billing: document.getElementById('show-billing').checked,
    show_cosmic: document.getElementById('show-cosmic').checked,
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
        // Effort sizing supports quarter-day granularity — snap to the nearest 0.25.
        const raw = parseFloat(document.getElementById(`effort-${k}`).value);
        if (Number.isFinite(raw) && raw >= 0.25) effortToDays[k] = Math.round(raw * 4) / 4;
      }
      if (Object.keys(effortToDays).length === 5) {
        await API.put(`/api/projects/${currentProject.id}/effort-to-days`, { effort_to_days: effortToDays });
      } else {
        showToast('Effort sizing not saved — all five sizes need a value ≥ 0.25', 'error');
        return;
      }
    }
    if (currentProject && !document.getElementById('max-hours-inputs').classList.contains('hidden')) {
      // Blank clears a pool; a value must be > 0.
      const parsePool = (id, label) => {
        const raw = document.getElementById(id).value.trim();
        if (raw === '') return null;
        const v = parseFloat(raw);
        if (!Number.isFinite(v) || v <= 0) throw new Error(`${label} must be a number greater than 0 (or blank to clear)`);
        return v;
      };
      let workHours, adminHours, qaHours;
      try {
        workHours = parsePool('work-hours', 'Work hours');
        adminHours = parsePool('admin-hours', 'Admin / meeting hours');
        qaHours = parsePool('qa-hours', 'QA hours');
      } catch (err) { showToast(err.message, 'error'); return; }
      await API.put(`/api/projects/${currentProject.id}/hour-budget`, { work_hours: workHours, admin_hours: adminHours, qa_hours: qaHours });
    }
    if (currentProject && !document.getElementById('strict-time-inputs').classList.contains('hidden')) {
      await API.put(`/api/projects/${currentProject.id}/strict-time`, {
        strict_time_enforcement: document.getElementById('strict-time').checked,
      });
    }
    if (currentProject && !document.getElementById('enforce-qa-inputs').classList.contains('hidden')) {
      await API.put(`/api/projects/${currentProject.id}/enforce-qa`, {
        enforce_qa: document.getElementById('enforce-qa').checked,
      });
    }
    showToast('Settings saved');
    showBilling = body.show_billing;
    applyBillingVisibility();
    showCosmic = body.show_cosmic;
    applyCosmicVisibility();
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
  const totNormal = entries.reduce((s, e) => s + (e.hours || 0), 0);
  const totOverride = entries.reduce((s, e) => s + (e.override_hours || 0), 0);
  const totAll = totNormal + totOverride;
  const rows = entries.map(e => {
    const normal = e.hours || 0;
    const over = e.override_hours || 0;
    const reasonCell = over > 0
      ? (e.extend_reason || '<span style="color:#ef4444;font-weight:600">⚠ no authorization</span>')
      : '';
    return `
    <tr>
      <td>${e.ticket_id}</td>
      <td>${e.ticket_title}</td>
      <td>${e.ticket_phase || '—'}</td>
      <td>${e.date}</td>
      <td style="text-align:right">${normal.toFixed(2)}</td>
      <td style="text-align:right${over > 0 ? ';color:#b45309;font-weight:600' : ';color:#bbb'}">${over.toFixed(2)}</td>
      <td style="text-align:right;font-weight:600">${(normal + over).toFixed(2)}</td>
      <td>${e.description}</td>
      <td>${e.author || '—'}</td>
      <td>${reasonCell}</td>
    </tr>`;
  }).join('');
  container.innerHTML = `
    <table class="billing-table">
      <thead><tr><th>Ticket</th><th>Title</th><th>Phase</th><th>Date</th><th style="text-align:right">Hours</th><th style="text-align:right">Override</th><th style="text-align:right">Total</th><th>Description</th><th>Author</th><th>Override reason</th></tr></thead>
      <tbody>${rows}</tbody>
      <tfoot><tr>
        <td colspan="4" style="text-align:right;font-weight:700">Total</td>
        <td style="text-align:right;font-weight:700">${totNormal.toFixed(2)}</td>
        <td style="text-align:right;font-weight:700;color:#b45309">${totOverride.toFixed(2)}</td>
        <td style="text-align:right;font-weight:700">${totAll.toFixed(2)}</td>
        <td colspan="3"></td>
      </tr></tfoot>
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

// ── Test cases tab ────────────────────────────────────
async function loadTestCases() {
  if (!currentProject) return;
  const el = document.getElementById('testcases-content');
  el.innerHTML = '<p style="color:#999;padding:16px">Loading…</p>';
  try {
    const rows = await API.get(`/api/projects/${currentProject.id}/test-summary`);
    renderTestCases(rows);
  } catch (e) {
    el.innerHTML = `<p style="color:red;padding:16px">${e.message}</p>`;
  }
}

function tcResultBadge(status) {
  if (status === 'pass') return '<span style="background:#16a34a;color:#fff;border-radius:4px;padding:1px 6px;font-size:11px;font-weight:700">PASS</span>';
  if (status === 'fail') return '<span style="background:#dc2626;color:#fff;border-radius:4px;padding:1px 6px;font-size:11px;font-weight:700">FAIL</span>';
  return '<span style="color:#bbb;font-size:12px">untested</span>';
}

function renderTestCases(rows) {
  const el = document.getElementById('testcases-content');
  if (!rows.length) {
    el.innerHTML = '<p style="color:#999;padding:16px">No active tickets have test cases yet. Add them on a ticket, or let the agent draft them.</p>';
    return;
  }
  const agg = rows.reduce((a, r) => ({ total: a.total + r.total, pass: a.pass + r.pass, fail: a.fail + r.fail, untested: a.untested + r.untested }), { total: 0, pass: 0, fail: 0, untested: 0 });
  const who = e => e ? e.split('@')[0] : '—';
  const blocks = rows.map(r => {
    const caseRows = (r.cases || []).map((c, i) => `
      <tr>
        <td style="padding:5px 8px;color:#bbb;vertical-align:top">${i + 1}</td>
        <td style="padding:5px 8px;vertical-align:top">${escapeHtml(c.step)}</td>
        <td style="padding:5px 8px;color:#555;vertical-align:top">${escapeHtml(c.expected)}</td>
        <td style="padding:5px 8px;white-space:nowrap;vertical-align:top">${tcResultBadge(c.status)}</td>
        <td style="padding:5px 8px;color:#777;vertical-align:top">${escapeHtml(c.comment || '')}</td>
      </tr>`).join('');
    return `
      <details style="border:1px solid #e5e7eb;border-radius:8px;margin-bottom:10px;padding:0 12px">
        <summary style="cursor:pointer;padding:12px 0;font-size:14px;display:flex;gap:10px;align-items:baseline;flex-wrap:wrap">
          <a onclick="event.preventDefault();event.stopPropagation();openTicketPanel('${r.ticket_id}')" href="#" style="color:#1565c0;font-weight:600;text-decoration:none">${r.ticket_id}</a>
          <span style="flex:1;min-width:120px">${escapeHtml(r.title)}</span>
          <span style="color:#888;font-size:12px">${escapeHtml(who(r.assignee))}</span>
          <span style="font-size:12px;white-space:nowrap">
            <span style="color:#16a34a;font-weight:600">${r.pass}✓</span>
            <span style="color:#dc2626;font-weight:600">${r.fail}✗</span>
            <span style="color:#b45309">${r.untested} untested</span>
          </span>
        </summary>
        <table style="width:100%;border-collapse:collapse;font-size:13px;margin:0 0 12px">
          <thead><tr style="text-align:left;color:#666;border-bottom:1px solid #eee">
            <th style="padding:5px 8px">#</th><th style="padding:5px 8px">Step / action</th><th style="padding:5px 8px">Expected result</th><th style="padding:5px 8px">Result</th><th style="padding:5px 8px">Comment</th>
          </tr></thead>
          <tbody>${caseRows}</tbody>
        </table>
      </details>`;
  }).join('');
  el.innerHTML = `
    <div style="max-width:1000px;margin:0 auto">
      <h2 style="font-size:18px;margin:0 0 4px">Test cases</h2>
      <p style="color:#666;font-size:13px;margin:0 0 16px">${agg.total} cases across ${rows.length} ticket${rows.length !== 1 ? 's' : ''} — <span style="color:#16a34a;font-weight:600">${agg.pass} pass</span> · <span style="color:#dc2626;font-weight:600">${agg.fail} fail</span> · <span style="color:#b45309;font-weight:600">${agg.untested} untested</span></p>
      ${blocks}
    </div>`;
}

// ── COSMIC calibration (experimental) ─────────────────
const fmtCosmicH = n => (n || 0).toFixed(1);
const fmtCosmicRate = n => (n == null ? '—' : n.toFixed(2));
const fmtCosmicPct = n => (n == null ? '—' : Math.round(n) + '%');

async function loadCosmic() {
  if (!currentProject) return;
  const el = document.getElementById('cosmic-content');
  el.innerHTML = '<p style="color:#999;padding:16px">Loading…</p>';
  try {
    const rep = await API.get(`/api/projects/${currentProject.id}/cosmic`);
    renderCosmic(rep);
  } catch (e) {
    el.innerHTML = `<p style="color:red;padding:16px">${e.message}</p>`;
  }
}

function renderCosmic(rep) {
  const el = document.getElementById('cosmic-content');
  const a = rep.aggregate, as = rep.assumed;

  if (!rep.features.length) {
    el.innerHTML = `
      <div style="max-width:680px">
        <h2 style="font-size:18px;margin-bottom:8px">COSMIC calibration <span class="badge backlog-badge">experimental</span></h2>
        <p style="color:#666;font-size:13px;line-height:1.6">No sized features yet. To calibrate your delivery pace against COSMIC function points:</p>
        <ol style="color:#666;font-size:13px;line-height:1.8;margin:8px 0 0 18px">
          <li>Tag a parent ticket <code>cfp:&lt;N&gt;</code> with its COSMIC size — the size lives only on the parent.</li>
          <li>Make the work items its children (tag them <code>parent:&lt;parent-id&gt;</code>).</li>
          <li>Tag each child <code>functional</code>, <code>config</code>, or <code>nonfunc</code>, and log hours on it.</li>
        </ol>
        <p style="color:#999;font-size:12px;margin-top:12px">Observed functional pace = Σ(functional child hours) ÷ feature CFP.</p>
      </div>`;
    return;
  }

  // Sanity flag when the aggregate rate is wildly off the assumed band.
  let flag = '';
  if (a.h_per_cfp != null && (a.h_per_cfp < as.band_low / 4 || a.h_per_cfp > as.band_high * 4)) {
    flag = `<div style="color:#c62828;font-size:12px;margin-top:8px">⚠ ${a.h_per_cfp.toFixed(2)} h/CFP is far outside the assumed band — verify the CFP counts and that all hours are logged before trusting it.</div>`;
  }
  let dq = '';
  if (a.unclassed_hours > 0) dq += `<div style="color:#e65100;font-size:12px;margin-top:6px">⚠ ${fmtCosmicH(a.unclassed_hours)} h on unclassed children — classify them or the rate is unreliable.</div>`;
  if (a.parent_hours > 0) dq += `<div style="color:#e65100;font-size:12px;margin-top:6px">⚑ ${fmtCosmicH(a.parent_hours)} h logged on parent tickets — hours belong on children.</div>`;

  const compare = a.h_per_cfp != null && a.h_per_cfp > 0 ? `${(as.band_mid / a.h_per_cfp).toFixed(1)}× under the ${as.band_mid} rate` : '';

  const card = `
    <div style="background:#fff;border-radius:8px;box-shadow:0 1px 4px rgba(0,0,0,.08);padding:20px 24px;max-width:760px;margin-bottom:20px">
      <div style="font-size:12px;text-transform:uppercase;letter-spacing:.5px;color:#666;margin-bottom:14px">Calibration · ${a.feature_count} sized feature${a.feature_count === 1 ? '' : 's'}</div>
      <div style="display:flex;gap:40px;flex-wrap:wrap">
        <div>
          <div style="font-size:28px;font-weight:700">${fmtCosmicRate(a.h_per_cfp)}<span style="font-size:13px;color:#999;font-weight:400"> h/CFP functional</span></div>
          <div style="font-size:12px;color:#666;margin-top:3px">median ${fmtCosmicRate(a.h_per_cfp_median)} · range ${fmtCosmicRate(a.h_per_cfp_min)}–${fmtCosmicRate(a.h_per_cfp_max)} · n=${a.n}</div>
          <div style="font-size:12px;color:#888;margin-top:2px">assumed band ${as.band_low} · ${as.band_mid} · ${as.band_high}${compare ? ` — ${compare}` : ''}</div>
        </div>
        <div>
          <div style="font-size:28px;font-weight:700">${fmtCosmicPct(a.wrap_pct)}<span style="font-size:13px;color:#999;font-weight:400"> wrap</span></div>
          <div style="font-size:12px;color:#888;margin-top:3px">assumed ${as.wrap_pct}%</div>
        </div>
        <div>
          <div style="font-size:28px;font-weight:700">${a.total_cfp}<span style="font-size:13px;color:#999;font-weight:400"> CFP</span></div>
          <div style="font-size:12px;color:#888;margin-top:3px">${fmtCosmicH(a.functional_hours)} functional h</div>
        </div>
      </div>
      ${flag}${dq}
    </div>`;

  const rows = rep.features.map(f => {
    let warn = '';
    if (f.unclassed_hours > 0) warn += `<span title="${fmtCosmicH(f.unclassed_hours)} h unclassed" style="color:#e65100">⚠</span> `;
    if (f.parent_hours > 0) warn += `<span title="${fmtCosmicH(f.parent_hours)} h on parent" style="color:#e65100">⚑</span>`;
    return `
    <tr>
      <td><a class="ticket-ref" href="javascript:void(0)" onclick="openTicketPanel('${f.id}')">${f.id}</a></td>
      <td>${escapeHtml(f.title)}</td>
      <td style="text-align:right">${f.cfp}</td>
      <td style="text-align:right">${fmtCosmicH(f.functional_hours)}</td>
      <td style="text-align:right">${fmtCosmicH(f.config_hours + f.nonfunc_hours)}</td>
      <td style="text-align:right;font-weight:600">${fmtCosmicRate(f.h_per_cfp)}</td>
      <td style="text-align:right">${fmtCosmicPct(f.wrap_pct)}</td>
      <td>${warn}</td>
    </tr>`;
  }).join('');

  el.innerHTML = card + renderCosmicEstimate(rep) + `
    <table class="billing-table">
      <thead><tr>
        <th>Feature</th><th>Title</th><th style="text-align:right">CFP</th>
        <th style="text-align:right">Func h</th><th style="text-align:right">Wrap h</th>
        <th style="text-align:right">h/CFP</th><th style="text-align:right">Wrap</th><th></th>
      </tr></thead>
      <tbody>${rows}</tbody>
    </table>`;
}

// Manual initial-estimate block: borrowed h/CFP + wrap % × total CFP → projected
// project hours, shown against actual-so-far. Inputs persist per project.
function renderCosmicEstimate(rep) {
  const e = rep.estimate || {}, a = rep.aggregate;
  const hpc = e.h_per_cfp != null ? e.h_per_cfp : '';
  const wp = e.wrap_pct != null ? e.wrap_pct : '';
  const actual = a.functional_hours + a.config_hours + a.nonfunc_hours;
  const hasEst = e.h_per_cfp != null && e.total_hours > 0;
  const pctOfEst = (hasEst && actual > 0) ? `${(100 * actual / e.total_hours).toFixed(0)}% of estimate` : 'no hours logged yet';
  const projected = hasEst ? `
      <div style="display:flex;gap:40px;flex-wrap:wrap;margin-top:14px">
        <div><div style="font-size:28px;font-weight:700">${fmtCosmicH(e.total_hours)}<span style="font-size:13px;color:#999;font-weight:400"> h estimated</span></div>
          <div style="font-size:12px;color:#666;margin-top:3px">code ${fmtCosmicH(e.code_hours)} + wrap ${fmtCosmicH(e.wrap_hours)}</div></div>
        <div><div style="font-size:28px;font-weight:700">${fmtCosmicH(actual)}<span style="font-size:13px;color:#999;font-weight:400"> h actual so far</span></div>
          <div style="font-size:12px;color:#666;margin-top:3px">${pctOfEst}</div></div>
      </div>`
    : `<div style="font-size:12px;color:#999;margin-top:10px">Enter a code rate (and wrap %) from a comparable delivered project to project this one's total hours from its ${e.total_cfp} CFP.</div>`;
  return `
    <div style="background:#fff;border-radius:8px;box-shadow:0 1px 4px rgba(0,0,0,.08);padding:20px 24px;max-width:760px;margin-bottom:20px">
      <div style="font-size:12px;text-transform:uppercase;letter-spacing:.5px;color:#666;margin-bottom:12px">Initial estimate · ${e.total_cfp} CFP</div>
      <div style="display:flex;gap:16px;align-items:flex-end;flex-wrap:wrap">
        <label style="font-size:13px">h/CFP<br><input id="est-hpercfp" type="number" step="0.001" min="0" value="${hpc}" placeholder="e.g. 0.17" style="width:110px"></label>
        <label style="font-size:13px">wrap %<br><input id="est-wrappct" type="number" step="1" min="0" value="${wp}" placeholder="e.g. 35" style="width:90px"></label>
        <button class="btn-primary" onclick="saveCosmicEstimate()">Save estimate</button>
      </div>
      ${projected}
    </div>`;
}

async function saveCosmicEstimate() {
  const hpcRaw = document.getElementById('est-hpercfp').value.trim();
  const wpRaw = document.getElementById('est-wrappct').value.trim();
  const body = {
    h_per_cfp: hpcRaw === '' ? null : parseFloat(hpcRaw),
    wrap_pct: wpRaw === '' ? null : parseFloat(wpRaw),
  };
  try {
    await API.put(`/api/projects/${currentProject.id}/cosmic-estimate`, body);
    showToast('Estimate saved');
    loadCosmic();
  } catch (e) { showToast(e.message, 'error'); }
}

// ── Init ──────────────────────────────────────────────
async function initTabVisibility() {
  try {
    const cfg = await API.get('/api/projects/settings');
    showBilling = cfg.show_billing || false;
    showCosmic = cfg.show_cosmic || false;
  } catch (e) { showBilling = false; showCosmic = false; }
  applyBillingVisibility();
  applyCosmicVisibility();
}

loadProjects();
initTabVisibility();
