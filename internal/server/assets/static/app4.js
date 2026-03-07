// app4.js — Agent Dashboard, Idea Queue, Context Library, Spec Document pages

// --- Utility ---
function timeAgo(dateStr) {
    if (!dateStr) return '';
    const d = new Date(dateStr);
    const now = new Date();
    const diff = Math.floor((now - d) / 1000);
    if (diff < 60) return 'just now';
    if (diff < 3600) return Math.floor(diff / 60) + 'm ago';
    if (diff < 86400) return Math.floor(diff / 3600) + 'h ago';
    return Math.floor(diff / 86400) + 'd ago';
}

// =====================================================
// AGENT DASHBOARD PAGE
// =====================================================
App.renderAgents = async function() {
    const agents = await App.api('agents');
    const active = agents.filter(a => a.status === 'active');
    const completed = agents.filter(a => a.status === 'completed');
    const failed = agents.filter(a => a.status === 'failed');
    const successRate = agents.length > 0 ? Math.round((completed.length / agents.length) * 100) : 0;

    let html = `<div class="page-header">
        <h2 class="page-title">🤖 Agent Dashboard</h2>
        <div class="page-subtitle">${active.length} active agent${active.length !== 1 ? 's' : ''}</div>
    </div>`;

    // Stats row
    html += `<div class="stats-grid" style="margin-bottom:24px">
        <div class="stat-card"><div class="stat-value">${agents.length}</div><div class="stat-label">Total Sessions</div></div>
        <div class="stat-card"><div class="stat-value">${active.length}</div><div class="stat-label">Active</div></div>
        <div class="stat-card"><div class="stat-value">${completed.length}</div><div class="stat-label">Completed</div></div>
        <div class="stat-card"><div class="stat-value">${successRate}%</div><div class="stat-label">Success Rate</div></div>
    </div>`;

    // Active agents
    if (active.length > 0) {
        html += `<h3 style="margin-bottom:12px">Active Agents</h3>`;
        for (const a of active) {
            // Fetch updates for each active agent
            let updates = [];
            try {
                const detail = await App.api('agents/' + encodeURIComponent(a.id));
                updates = (detail.updates || []).slice(0, 5);
            } catch(e) { /* ignore */ }

            html += `<div class="card" style="margin-bottom:16px">
                <div style="display:flex;justify-content:space-between;align-items:center;margin-bottom:8px">
                    <div>
                        <strong>${esc(a.name)}</strong>
                        <span style="opacity:0.5;margin-left:8px;font-size:0.85em">${esc(a.id)}</span>
                    </div>
                    <div style="display:flex;gap:8px;align-items:center">
                        ${a.current_phase ? `<span class="status-badge status-implementing">${esc(a.current_phase)}</span>` : ''}
                        ${a.eta ? `<span style="font-size:0.85em;opacity:0.7">ETA: ${esc(a.eta)}</span>` : ''}
                    </div>
                </div>
                ${a.task_description ? `<div style="margin-bottom:8px;opacity:0.8">${esc(a.task_description)}</div>` : ''}
                ${a.feature_id ? `<div style="margin-bottom:8px">Feature: <span class="clickable-feature" data-feature-id="${esc(a.feature_id)}">${esc(a.feature_id)}</span></div>` : ''}
                <div style="margin-bottom:8px">
                    <div class="progress-bar"><div class="progress-fill" style="width:${a.progress_pct}%"></div></div>
                    <div style="font-size:0.85em;opacity:0.7;margin-top:4px">${a.progress_pct}% complete · Last active ${timeAgo(a.updated_at)}</div>
                </div>
                ${updates.length > 0 ? `<div style="border-top:1px solid var(--border);padding-top:8px;margin-top:8px">
                    <div style="font-size:0.85em;font-weight:600;margin-bottom:6px">Recent Updates</div>
                    ${updates.map(u => `<div style="padding:6px 0;border-bottom:1px solid var(--border-light, var(--border));font-size:0.9em">
                        <div style="display:flex;justify-content:space-between;margin-bottom:2px">
                            ${u.phase ? `<span class="status-badge status-planning">${esc(u.phase)}</span>` : '<span></span>'}
                            <span style="opacity:0.5">${timeAgo(u.created_at)}</span>
                        </div>
                        <div class="md-content">${renderMD(u.message_md)}</div>
                    </div>`).join('')}
                </div>` : ''}
            </div>`;
        }
    } else {
        html += `<div class="empty-state" style="margin-bottom:24px">
            <div class="empty-state-icon">🤖</div>
            <div class="empty-state-text">No active agents</div>
            <div class="empty-state-hint">Agents will appear here when they start working on tasks.</div>
        </div>`;
    }

    // Completed/Failed agents
    const past = [...completed, ...failed];
    if (past.length > 0) {
        html += `<details style="margin-top:16px"><summary style="cursor:pointer;font-weight:600;margin-bottom:8px">
            Completed & Failed Sessions (${past.length})
        </summary><div>`;
        for (const a of past) {
            const icon = a.status === 'completed' ? '✅' : '❌';
            html += `<div class="card" style="margin-bottom:8px;opacity:0.85">
                <div style="display:flex;justify-content:space-between;align-items:center">
                    <div>${icon} <strong>${esc(a.name)}</strong></div>
                    <div style="display:flex;gap:8px;align-items:center">
                        <span class="status-badge status-${a.status === 'completed' ? 'done' : 'blocked'}">${esc(a.status)}</span>
                        <span style="font-size:0.85em;opacity:0.5">${timeAgo(a.updated_at)}</span>
                    </div>
                </div>
                ${a.task_description ? `<div style="font-size:0.9em;opacity:0.7;margin-top:4px">${esc(a.task_description)}</div>` : ''}
            </div>`;
        }
        html += `</div></details>`;
    }

    return html;
};

// =====================================================
// IDEA QUEUE PAGE
// =====================================================
App.renderIdeas = async function() {
    const ideas = await App.api('ideas');
    const counts = { pending: 0, processing: 0, 'spec-ready': 0, approved: 0, rejected: 0 };
    ideas.forEach(i => { counts[i.status] = (counts[i.status] || 0) + 1; });

    let html = `<div class="page-header">
        <h2 class="page-title">💡 Idea Queue</h2>
        <div class="page-subtitle">${ideas.length} idea${ideas.length !== 1 ? 's' : ''} · ${counts.pending} pending · ${counts['spec-ready']} ready for review</div>
    </div>`;

    // Submit button
    html += `<div style="margin-bottom:20px">
        <button class="btn btn-primary" id="submitIdeaBtn">+ Submit Idea</button>
    </div>`;

    // Modal (hidden by default)
    html += `<div id="ideaModal" style="display:none;position:fixed;inset:0;background:rgba(0,0,0,0.6);z-index:1000;align-items:center;justify-content:center">
        <div class="card" style="width:90%;max-width:560px;max-height:90vh;overflow-y:auto;padding:24px">
            <h3 style="margin-bottom:16px">Submit New Idea</h3>
            <div style="margin-bottom:12px">
                <label style="display:block;font-weight:600;margin-bottom:4px">Title *</label>
                <input type="text" id="ideaTitle" style="width:100%;padding:8px;border:1px solid var(--border);border-radius:6px;background:var(--bg-secondary);color:var(--text)" placeholder="Idea title">
            </div>
            <div style="margin-bottom:12px">
                <label style="display:block;font-weight:600;margin-bottom:4px">Description</label>
                <textarea id="ideaDesc" rows="5" style="width:100%;padding:8px;border:1px solid var(--border);border-radius:6px;background:var(--bg-secondary);color:var(--text);resize:vertical" placeholder="Describe the idea (markdown supported)"></textarea>
            </div>
            <div style="display:flex;gap:12px;margin-bottom:12px">
                <div style="flex:1">
                    <label style="display:block;font-weight:600;margin-bottom:4px">Type</label>
                    <select id="ideaType" style="width:100%;padding:8px;border:1px solid var(--border);border-radius:6px;background:var(--bg-secondary);color:var(--text)">
                        <option value="feature">Feature</option>
                        <option value="bug">Bug</option>
                    </select>
                </div>
                <div style="flex:1;display:flex;align-items:end">
                    <label style="display:flex;align-items:center;gap:8px;cursor:pointer">
                        <input type="checkbox" id="ideaAuto"> Auto-implement
                    </label>
                </div>
            </div>
            <div style="display:flex;gap:8px;justify-content:flex-end">
                <button class="btn" id="ideaCancelBtn">Cancel</button>
                <button class="btn btn-primary" id="ideaSubmitBtn">Submit</button>
            </div>
        </div>
    </div>`;

    // Group ideas by status
    const statusOrder = ['pending', 'processing', 'spec-ready', 'approved', 'rejected'];
    const statusLabels = { pending: '⏳ Pending', processing: '⚙️ Processing', 'spec-ready': '📋 Spec Ready', approved: '✅ Approved', rejected: '❌ Rejected' };

    for (const st of statusOrder) {
        const group = ideas.filter(i => i.status === st);
        if (group.length === 0) continue;

        const collapsed = st === 'approved' || st === 'rejected';
        if (collapsed) {
            html += `<details style="margin-top:16px"><summary style="cursor:pointer;font-weight:600;margin-bottom:8px">${statusLabels[st]} (${group.length})</summary><div>`;
        } else {
            html += `<h3 style="margin:20px 0 12px">${statusLabels[st]} (${group.length})</h3>`;
        }

        for (const idea of group) {
            const typeBadge = idea.idea_type === 'bug' ? '🐛' : '✨';
            html += `<div class="card" style="margin-bottom:12px" data-idea-id="${idea.id}">
                <div style="display:flex;justify-content:space-between;align-items:center;margin-bottom:6px">
                    <div>
                        <span style="margin-right:6px">${typeBadge}</span>
                        <strong>${esc(idea.title)}</strong>
                        ${idea.auto_implement ? '<span style="font-size:0.8em;opacity:0.6;margin-left:6px">🤖 auto</span>' : ''}
                    </div>
                    <div style="display:flex;gap:6px;align-items:center">
                        <span class="status-badge status-${st === 'spec-ready' ? 'human-qa' : st === 'approved' ? 'done' : st === 'rejected' ? 'blocked' : 'planning'}">${esc(idea.status)}</span>
                        <span style="font-size:0.85em;opacity:0.5">${timeAgo(idea.created_at)}</span>
                    </div>
                </div>
                ${idea.raw_input ? `<div style="font-size:0.9em;opacity:0.8;margin-bottom:8px">${esc(idea.raw_input).substring(0, 200)}${idea.raw_input.length > 200 ? '...' : ''}</div>` : ''}
                <div style="font-size:0.85em;opacity:0.6">by ${esc(idea.submitted_by || 'human')}</div>
                ${idea.feature_id ? `<div style="margin-top:4px;font-size:0.85em">→ Feature: <span class="clickable-feature" data-feature-id="${esc(idea.feature_id)}">${esc(idea.feature_id)}</span></div>` : ''}
                ${idea.spec_md ? `<details style="margin-top:8px"><summary style="cursor:pointer;font-size:0.9em;font-weight:600">View Spec</summary>
                    <div class="md-content" style="margin-top:8px;padding:12px;background:var(--bg-secondary);border-radius:6px">${renderMD(idea.spec_md)}</div>
                </details>` : ''}
                ${st === 'spec-ready' ? `<div style="margin-top:10px;display:flex;gap:8px">
                    <button class="btn btn-primary idea-approve-btn" data-idea-id="${idea.id}" style="font-size:0.85em">✅ Approve</button>
                    <button class="btn idea-reject-btn" data-idea-id="${idea.id}" style="font-size:0.85em">❌ Reject</button>
                </div>` : ''}
            </div>`;
        }

        if (collapsed) html += `</div></details>`;
    }

    if (ideas.length === 0) {
        html += `<div class="empty-state">
            <div class="empty-state-icon">💡</div>
            <div class="empty-state-text">No ideas yet</div>
            <div class="empty-state-hint">Submit your first idea using the button above.</div>
        </div>`;
    }

    return html;
};

App._bindIdeasEvents = function() {
    const modal = document.getElementById('ideaModal');
    const openBtn = document.getElementById('submitIdeaBtn');
    const cancelBtn = document.getElementById('ideaCancelBtn');
    const submitBtn = document.getElementById('ideaSubmitBtn');

    if (openBtn) openBtn.addEventListener('click', () => { if (modal) modal.style.display = 'flex'; });
    if (cancelBtn) cancelBtn.addEventListener('click', () => { if (modal) modal.style.display = 'none'; });
    if (modal) modal.addEventListener('click', (e) => { if (e.target === modal) modal.style.display = 'none'; });

    if (submitBtn) submitBtn.addEventListener('click', async () => {
        const title = document.getElementById('ideaTitle')?.value?.trim();
        if (!title) return alert('Title is required');
        const desc = document.getElementById('ideaDesc')?.value?.trim() || '';
        const type = document.getElementById('ideaType')?.value || 'feature';
        const auto = document.getElementById('ideaAuto')?.checked || false;
        try {
            await App.apiPost('ideas', { title, raw_input: desc, idea_type: type, auto_implement: auto });
            if (modal) modal.style.display = 'none';
            App.navigate('ideas');
        } catch(e) { alert('Error: ' + e.message); }
    });

    document.querySelectorAll('.idea-approve-btn').forEach(btn => {
        btn.addEventListener('click', async (e) => {
            e.stopPropagation();
            const id = btn.dataset.ideaId;
            try {
                await App.apiPost('ideas/' + id + '/approve', {});
                App.navigate('ideas');
            } catch(e2) { alert('Error: ' + e2.message); }
        });
    });

    document.querySelectorAll('.idea-reject-btn').forEach(btn => {
        btn.addEventListener('click', async (e) => {
            e.stopPropagation();
            const id = btn.dataset.ideaId;
            try {
                await App.apiPost('ideas/' + id + '/reject', {});
                App.navigate('ideas');
            } catch(e2) { alert('Error: ' + e2.message); }
        });
    });
};

// =====================================================
// CONTEXT LIBRARY PAGE
// =====================================================
App.renderContext = async function() {
    let entries = await App.api('context');
    const q = App._contextSearch || '';
    if (q) {
        entries = await App.api('context/search?q=' + encodeURIComponent(q));
    }

    const typeFilter = App._contextTypeFilter || 'all';
    if (typeFilter !== 'all') {
        entries = entries.filter(e => e.context_type === typeFilter);
    }

    const types = ['all', 'source-analysis', 'doc', 'spec', 'research', 'note'];

    let html = `<div class="page-header">
        <h2 class="page-title">📚 Context Library</h2>
        <div class="page-subtitle">${entries.length} entr${entries.length !== 1 ? 'ies' : 'y'}</div>
    </div>`;

    // Search bar
    html += `<div style="margin-bottom:16px">
        <input type="text" id="contextSearchInput" value="${esc(q)}" placeholder="Search context entries..."
            style="width:100%;max-width:400px;padding:8px 12px;border:1px solid var(--border);border-radius:6px;background:var(--bg-secondary);color:var(--text)">
    </div>`;

    // Type filter pills
    html += `<div style="display:flex;gap:6px;flex-wrap:wrap;margin-bottom:20px">`;
    for (const t of types) {
        const active = t === typeFilter;
        html += `<button class="filter-btn ctx-type-filter ${active ? 'active' : ''}" data-type="${t}"
            style="padding:4px 12px;border-radius:16px;border:1px solid var(--border);cursor:pointer;font-size:0.85em;
            background:${active ? 'var(--accent)' : 'var(--bg-secondary)'};color:${active ? '#fff' : 'var(--text)'}">${t}</button>`;
    }
    html += `</div>`;

    // Context cards
    if (entries.length === 0) {
        html += `<div class="empty-state">
            <div class="empty-state-icon">📚</div>
            <div class="empty-state-text">No context entries${q ? ' matching "' + esc(q) + '"' : ''}</div>
            <div class="empty-state-hint">Context entries are added by agents during their work.</div>
        </div>`;
    } else {
        for (const e of entries) {
            const typeIcons = { 'source-analysis': '🔍', doc: '📄', spec: '📋', research: '🔬', note: '📝' };
            const icon = typeIcons[e.context_type] || '📎';
            const preview = (e.content_md || '').substring(0, 200).replace(/\n/g, ' ');
            html += `<div class="card ctx-card" style="margin-bottom:12px;cursor:pointer" data-ctx-id="${e.id}">
                <div style="display:flex;justify-content:space-between;align-items:center;margin-bottom:6px">
                    <div>
                        <span style="margin-right:6px">${icon}</span>
                        <strong>${esc(e.title)}</strong>
                    </div>
                    <div style="display:flex;gap:6px;align-items:center">
                        <span class="status-badge status-planning">${esc(e.context_type)}</span>
                        <span style="font-size:0.85em;opacity:0.5">${timeAgo(e.created_at)}</span>
                    </div>
                </div>
                <div style="font-size:0.9em;opacity:0.7;margin-bottom:4px">${esc(preview)}${(e.content_md || '').length > 200 ? '...' : ''}</div>
                <div style="display:flex;gap:8px;font-size:0.85em;opacity:0.6">
                    <span>by ${esc(e.author)}</span>
                    ${e.feature_id ? `<span>· Feature: <span class="clickable-feature" data-feature-id="${esc(e.feature_id)}">${esc(e.feature_id)}</span></span>` : ''}
                    ${e.tags ? `<span>· ${esc(e.tags)}</span>` : ''}
                </div>
                <div class="ctx-expanded" style="display:none;margin-top:12px;padding-top:12px;border-top:1px solid var(--border)">
                    <div class="md-content">${renderMD(e.content_md)}</div>
                </div>
            </div>`;
        }
    }

    return html;
};

App._bindContextEvents = function() {
    const searchInput = document.getElementById('contextSearchInput');
    let debounceTimer;
    if (searchInput) {
        searchInput.addEventListener('input', () => {
            clearTimeout(debounceTimer);
            debounceTimer = setTimeout(() => {
                App._contextSearch = searchInput.value.trim();
                App.navigate('context');
            }, 400);
        });
    }

    document.querySelectorAll('.ctx-type-filter').forEach(btn => {
        btn.addEventListener('click', () => {
            App._contextTypeFilter = btn.dataset.type;
            App.navigate('context');
        });
    });

    document.querySelectorAll('.ctx-card').forEach(card => {
        card.addEventListener('click', (e) => {
            if (e.target.closest('.clickable-feature')) return;
            const expanded = card.querySelector('.ctx-expanded');
            if (expanded) expanded.style.display = expanded.style.display === 'none' ? 'block' : 'none';
        });
    });
};

// =====================================================
// SPEC DOCUMENT PAGE
// =====================================================
App.renderSpec = async function() {
    const spec = await App.api('spec-document');

    let tocHtml = '';
    let contentHtml = '';

    for (const section of (spec.sections || [])) {
        const anchor = section.id;
        tocHtml += `<a href="#spec-${anchor}" class="spec-toc-item" style="padding:4px 0;display:block;font-size:0.9em;color:var(--text);text-decoration:none;opacity:0.8">${esc(section.title)}</a>`;

        contentHtml += `<div id="spec-${anchor}" class="spec-section" style="margin-bottom:32px">
            <h2 style="border-bottom:1px solid var(--border);padding-bottom:8px;margin-bottom:12px">${esc(section.title)}</h2>
            <div class="md-content">${renderMD(section.content_md)}</div>`;

        if (section.features && section.features.length > 0) {
            contentHtml += `<div style="margin-top:16px">`;
            for (const f of section.features) {
                const deps = (f.dependencies || []);
                contentHtml += `<div class="card" style="margin-bottom:10px;padding:12px">
                    <div style="display:flex;justify-content:space-between;align-items:center;margin-bottom:6px">
                        <strong class="clickable-feature" data-feature-id="${esc(f.id)}">${esc(f.name)}</strong>
                        <div style="display:flex;gap:6px">
                            <span class="status-badge status-${esc(f.status)}">${esc(f.status)}</span>
                            <span style="font-size:0.85em;opacity:0.6">P${f.priority}</span>
                        </div>
                    </div>
                    ${f.description ? `<div style="font-size:0.9em;opacity:0.8;margin-bottom:6px">${esc(f.description)}</div>` : ''}
                    ${deps.length > 0 ? `<div style="font-size:0.85em;margin-bottom:6px">Depends on: ${deps.map(d => `<span class="clickable-feature" data-feature-id="${esc(d)}" style="background:var(--bg-secondary);padding:2px 8px;border-radius:10px;margin-right:4px">${esc(d)}</span>`).join('')}</div>` : ''}
                    ${f.spec_md ? `<details><summary style="cursor:pointer;font-size:0.85em;font-weight:600;margin-top:4px">Specification</summary>
                        <div class="md-content" style="margin-top:8px;padding:12px;background:var(--bg-secondary);border-radius:6px">${renderMD(f.spec_md)}</div>
                    </details>` : ''}
                </div>`;
            }
            contentHtml += `</div>`;
        }

        contentHtml += `</div>`;
    }

    // Stats footer
    const s = spec.stats || {};
    const statsHtml = `<div class="stats-grid" style="margin-top:24px">
        <div class="stat-card"><div class="stat-value">${s.total_features || 0}</div><div class="stat-label">Features</div></div>
        <div class="stat-card"><div class="stat-value">${s.done || 0}</div><div class="stat-label">Done</div></div>
        <div class="stat-card"><div class="stat-value">${s.in_progress || 0}</div><div class="stat-label">In Progress</div></div>
        <div class="stat-card"><div class="stat-value">${s.total_milestones || 0}</div><div class="stat-label">Milestones</div></div>
    </div>`;

    let html = `<div class="spec-document">
        <div class="page-header" style="display:flex;justify-content:space-between;align-items:center">
            <div>
                <h2 class="page-title">📋 ${esc(spec.title || 'Software Specification')}</h2>
                <div class="page-subtitle">Generated ${spec.generated_at ? new Date(spec.generated_at).toLocaleString() : 'now'}</div>
            </div>
            <div>
                <button class="btn" onclick="window.print()" style="font-size:0.85em">🖨️ Print</button>
            </div>
        </div>
        ${statsHtml}
        <div style="display:flex;gap:24px;margin-top:24px">
            <div class="spec-toc" style="width:220px;flex-shrink:0;position:sticky;top:16px;align-self:flex-start;padding:12px;background:var(--bg-secondary);border-radius:8px;max-height:80vh;overflow-y:auto">
                <div style="font-weight:700;margin-bottom:8px;font-size:0.9em">Table of Contents</div>
                ${tocHtml}
            </div>
            <div style="flex:1;min-width:0">
                ${contentHtml}
            </div>
        </div>
    </div>`;

    return html;
};

App._bindSpecEvents = function() {
    // Smooth scroll for TOC links
    document.querySelectorAll('.spec-toc-item').forEach(link => {
        link.addEventListener('click', (e) => {
            e.preventDefault();
            const target = document.querySelector(link.getAttribute('href'));
            if (target) target.scrollIntoView({ behavior: 'smooth', block: 'start' });
        });
    });
};

// =====================================================
// PRINT STYLES
// =====================================================
(function() {
    const style = document.createElement('style');
    style.textContent = `
        @media print {
            .sidebar, .hamburger, .sidebar-overlay, .chord-indicator, .shortcut-modal-overlay,
            .spec-toc, .page-subtitle, .btn, .theme-toggle { display: none !important; }
            .content { margin: 0 !important; padding: 20px !important; }
            .spec-document { font-size: 11pt; }
            .card { break-inside: avoid; border: 1px solid #ddd !important; }
            .stats-grid { break-inside: avoid; }
        }
    `;
    document.head.appendChild(style);
})();
