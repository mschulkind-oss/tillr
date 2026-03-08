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

// Screen reader announcement utility
App.announce = function(message, priority) {
    var el = document.getElementById('sr-announcements');
    if (!el) {
        el = document.createElement('div');
        el.id = 'sr-announcements';
        el.setAttribute('role', 'status');
        el.setAttribute('aria-live', priority || 'polite');
        el.setAttribute('aria-atomic', 'true');
        el.style.cssText = 'position:absolute;width:1px;height:1px;overflow:hidden;clip:rect(0,0,0,0);';
        document.body.appendChild(el);
    }
    el.textContent = '';
    setTimeout(function() { el.textContent = message; }, 100);
};

// =====================================================
// AGENT DASHBOARD PAGE
// =====================================================
App.renderAgents = async function() {
    // If an agent ID is in context, render detail view
    if (App._navContext?.id) {
        return App.renderAgentDetail(App._navContext.id);
    }

    const [agents, worktrees] = await Promise.all([
        App.api('agents'),
        App.api('worktrees').catch(() => []),
    ]);
    const active = agents.filter(a => a.status === 'active');
    const completed = agents.filter(a => a.status === 'completed');
    const failed = agents.filter(a => a.status === 'failed');
    const finished = completed.length + failed.length;
    const successRate = finished > 0 ? Math.round((completed.length / finished) * 100) : -1;

    let html = `<div class="page-header">
        <h2 class="page-title">🤖 Agent Dashboard</h2>
        <div class="page-subtitle">${active.length} active agent${active.length !== 1 ? 's' : ''}</div>
    </div>`;

    // Stats row
    html += `<div class="stats-grid app4-stats-row">
        <div class="stat-card stat-card--accent"><div class="stat-value">${agents.length}</div><div class="stat-label">Total Sessions</div></div>
        <div class="stat-card stat-card--success"><div class="stat-value">${active.length}</div><div class="stat-label">Active</div></div>
        <div class="stat-card"><div class="stat-value">${completed.length}</div><div class="stat-label">Completed</div></div>
        <div class="stat-card stat-card--warning"><div class="stat-value">${successRate >= 0 ? successRate + '%' : 'N/A'}</div><div class="stat-label">Success Rate</div></div>
    </div>`;

    // Coordination status panel
    try {
        const coord = await App.api('agents/coordination');
        const hasIssues = coord.stale_agents.length > 0 || coord.conflicts.length > 0;
        html += `<div class="card" style="margin-bottom:16px;${hasIssues ? 'border-left:3px solid var(--warning)' : ''}">
            <h3 style="margin:0 0 12px">🔗 Coordination</h3>
            <div class="stats-grid app4-stats-row" style="margin-bottom:8px">
                <div class="stat-card"><div class="stat-value">${coord.queue_depth}</div><div class="stat-label">Queue Depth</div></div>
                <div class="stat-card"><div class="stat-value">${coord.claimed_items}</div><div class="stat-label">Claimed</div></div>
                <div class="stat-card${coord.stale_agents.length ? ' stat-card--warning' : ''}"><div class="stat-value">${coord.stale_agents.length}</div><div class="stat-label">Stale</div></div>
                <div class="stat-card${coord.conflicts.length ? ' stat-card--warning' : ''}"><div class="stat-value">${coord.conflicts.length}</div><div class="stat-label">Conflicts</div></div>
            </div>
            ${coord.stale_agents.length ? `<div style="margin-top:8px"><strong>⚠ Stale Agents</strong> (no heartbeat in 5 min):<ul style="margin:4px 0">${coord.stale_agents.map(a => `<li>${esc(a.name)} (${esc(a.id)}) — last seen ${timeAgo(a.updated_at)}</li>`).join('')}</ul></div>` : ''}
            ${coord.conflicts.length ? `<div style="margin-top:8px"><strong>⚠ Conflicts</strong> (multiple agents on same feature):<ul style="margin:4px 0">${coord.conflicts.map(c => `<li>Feature <strong>${esc(c.feature_id)}</strong>: ${c.agents.map(a => esc(a)).join(', ')}</li>`).join('')}</ul></div>` : ''}
        </div>`;
    } catch(e) { /* coordination endpoint not available */ }

    // Queue panel
    try {
        const queueData = await App.api('queue');
        var q = queueData.queue || [];
        var qs = queueData.stats || {};
        html += `<div class="card" style="margin-bottom:16px">
            <h3 style="margin:0 0 12px">📋 Work Queue</h3>
            <div class="stats-grid app4-stats-row" style="margin-bottom:8px">
                <div class="stat-card"><div class="stat-value">${qs.total_pending || 0}</div><div class="stat-label">Pending</div></div>
                <div class="stat-card stat-card--accent"><div class="stat-value">${qs.total_claimed || 0}</div><div class="stat-label">Claimed</div></div>
                <div class="stat-card stat-card--success"><div class="stat-value">${qs.total_completed_today || 0}</div><div class="stat-label">Done Today</div></div>
            </div>`;
        if (q.length > 0) {
            html += `<table class="queue-table" style="width:100%;border-collapse:collapse;font-size:0.85rem">
                <thead><tr style="text-align:left;border-bottom:1px solid var(--border)">
                    <th style="padding:6px 8px">Prio</th><th style="padding:6px 8px">Feature</th>
                    <th style="padding:6px 8px">Type</th><th style="padding:6px 8px">Agent</th>
                    <th style="padding:6px 8px">Wait</th>
                </tr></thead><tbody>`;
            q.forEach(function(item) {
                var prioColor = item.priority >= 8 ? 'var(--danger)' : item.priority >= 5 ? 'var(--warning)' : 'var(--success)';
                var agentCell = item.assigned_agent ? esc(item.assigned_agent) : '<span class="text-secondary">unassigned</span>';
                var statusBadge = item.status === 'active' ? '<span class="badge badge-implementing">active</span> ' : '';
                html += `<tr style="border-bottom:1px solid var(--border-light)">
                    <td style="padding:6px 8px"><span style="display:inline-block;width:8px;height:8px;border-radius:50%;background:${prioColor};margin-right:4px"></span>${item.priority}</td>
                    <td style="padding:6px 8px">${statusBadge}${esc(item.feature_name || item.feature_id)}</td>
                    <td style="padding:6px 8px"><span class="badge">${esc(item.work_type)}</span></td>
                    <td style="padding:6px 8px">${agentCell}</td>
                    <td style="padding:6px 8px">${timeAgo(item.created_at)}</td>
                </tr>`;
            });
            html += '</tbody></table>';
        } else {
            html += '<div class="text-secondary" style="padding:8px 0">No items in queue.</div>';
        }
        html += '</div>';
    } catch(e) { /* queue endpoint not available */ }

    html += App.renderSearchBox('agentsSearch', 'Search agents…');

    // Active agents
    if (active.length > 0) {
        html += `<h3 class="app4-section-heading">Active Agents</h3>`;
        for (const a of active) {
            let updates = [];
            try {
                const detail = await App.api('agents/' + encodeURIComponent(a.id));
                updates = (detail.updates || []).slice(0, 5);
            } catch(e) { /* ignore */ }

            const progressClass = a.progress_pct >= 80 ? 'progress-fill--high' : a.progress_pct >= 40 ? 'progress-fill--mid' : '';

            html += `<div class="card app4-agent-card app4-clickable-agent" data-agent-id="${esc(a.id)}">
                <div class="app4-agent-header">
                    <div class="app4-agent-identity">
                        <strong class="app4-agent-name">${esc(a.name)}</strong>
                        <span class="app4-agent-id">${esc(a.id)}</span>
                    </div>
                    <div class="app4-agent-meta">
                        ${a.current_phase ? `<span class="badge badge-implementing">${esc(a.current_phase)}</span>` : ''}
                        ${a.eta ? `<span class="app4-meta-text">ETA: ${esc(a.eta)}</span>` : ''}
                    </div>
                </div>
                ${a.task_description ? `<div class="app4-agent-task">${esc(a.task_description)}</div>` : ''}
                ${a.feature_id ? `<div class="app4-agent-feature">Feature: <span class="clickable-feature" data-feature-id="${esc(a.feature_id)}">${esc(a.feature_id)}</span></div>` : ''}
                <div class="app4-progress-section">
                    <div class="progress-bar app4-progress-bar"><div class="progress-fill app4-progress-fill ${progressClass}" style="width:${a.progress_pct}%"></div></div>
                    <div class="app4-progress-label">${a.progress_pct}% complete · Last update ${timeAgo(a.updated_at)} · Running for ${timeAgo(a.created_at)}</div>
                </div>
                ${updates.length > 0 ? `<div class="app4-updates-section">
                    <div class="app4-updates-heading">Recent Updates</div>
                    ${updates.map(u => `<div class="app4-update-item">
                        <div class="app4-update-header">
                            ${u.phase ? `<span class="badge badge-planning">${esc(u.phase)}</span>` : '<span></span>'}
                            <span class="app4-time-text">${timeAgo(u.created_at)}</span>
                        </div>
                        <div class="md-content">${renderMD(u.message_md)}</div>
                    </div>`).join('')}
                </div>` : ''}
            </div>`;
        }
    } else {
        html += `<div class="empty-state app4-empty-state">
            <div class="empty-state-icon">🤖</div>
            <div class="empty-state-text">No active agents</div>
            <div class="empty-state-hint">Agents will appear here when they start working on tasks.</div>
            <div class="app4-empty-pulse"></div>
        </div>`;
    }

    // Completed/Failed agents
    const past = [...completed, ...failed];
    if (past.length > 0) {
        html += `<details class="app4-past-section"><summary class="app4-past-summary">
            Completed & Failed Sessions (${past.length})
        </summary><div class="app4-past-list">`;
        for (const a of past) {
            const icon = a.status === 'completed' ? '✅' : '❌';
            html += `<div class="card app4-past-card app4-clickable-agent" data-agent-id="${esc(a.id)}">
                <div class="app4-past-card-header">
                    <div class="app4-past-card-name">${icon} <strong>${esc(a.name)}</strong></div>
                    <div class="app4-past-card-meta">
                        <span class="badge badge-${a.status === 'completed' ? 'done' : 'failed'}">${esc(a.status)}</span>
                        <span class="app4-time-text">${timeAgo(a.updated_at)}</span>
                    </div>
                </div>
                ${a.task_description ? `<div class="app4-past-card-desc">${esc(a.task_description)}</div>` : ''}
            </div>`;
        }
        html += `</div></details>`;
    }

    // Workspaces section
    html += `<h3 class="app4-section-heading" style="margin-top:32px">📂 Workspaces</h3>`;
    if (worktrees.length > 0) {
        html += `<div class="app4-worktree-grid">`;
        for (const wt of worktrees) {
            const agentName = wt.agent_session_id ? agents.find(a => a.id === wt.agent_session_id)?.name : null;
            html += `<div class="card app4-worktree-card">
                <div class="app4-wt-header">
                    <strong class="app4-wt-name">📁 ${esc(wt.name)}</strong>
                    ${wt.branch ? `<span class="badge badge-planning">${esc(wt.branch)}</span>` : ''}
                </div>
                <div class="app4-wt-path">${esc(wt.path)}</div>
                ${agentName ? `<div class="app4-wt-agent">🤖 <span class="app4-clickable-agent-link" data-agent-id="${esc(wt.agent_session_id)}">${esc(agentName)}</span></div>` : '<div class="app4-wt-agent app4-wt-unlinked">No agent linked</div>'}
            </div>`;
        }
        html += `</div>`;
    } else {
        html += `<div class="empty-state app4-empty-state" style="padding:24px">
            <div class="empty-state-text" style="font-size:14px">No workspaces configured</div>
            <div class="empty-state-hint">Use <code>lifecycle worktree add &lt;name&gt;</code> to create one.</div>
        </div>`;
    }

    return html;
};

// Agent Detail View
App.renderAgentDetail = async function(agentId) {
    let detail;
    try {
        detail = await App.api('agents/' + encodeURIComponent(agentId));
    } catch(e) {
        return `<div class="empty-state"><div class="empty-state-icon">❌</div>
            <div class="empty-state-text">Agent not found</div>
            <div class="empty-state-hint">${esc(e.message)}</div>
            <button class="app4-btn app4-btn-ghost" onclick="App._navContext={};App.navigate('agents')">← Back to Agents</button>
        </div>`;
    }
    const s = detail.session;
    const updates = detail.updates || [];
    const wt = detail.worktree;

    const statusColors = { active: 'implementing', completed: 'done', failed: 'blocked', paused: 'planning', abandoned: 'blocked' };
    const progressClass = s.progress_pct >= 80 ? 'progress-fill--high' : s.progress_pct >= 40 ? 'progress-fill--mid' : '';

    let html = `<div class="page-header">
        <div>
            <button class="app4-btn app4-btn-ghost app4-back-btn" id="agentBackBtn" style="margin-bottom:8px">← Back to Agents</button>
            <h2 class="page-title">🤖 ${esc(s.name)}</h2>
            <div class="page-subtitle">${esc(s.id)}</div>
        </div>
    </div>`;

    // Info cards row
    html += `<div class="stats-grid app4-stats-row">
        <div class="stat-card"><div class="stat-value"><span class="badge badge-${statusColors[s.status] || 'planning'}" style="font-size:16px">${esc(s.status)}</span></div><div class="stat-label">Status</div></div>
        <div class="stat-card"><div class="stat-value">${s.progress_pct}%</div><div class="stat-label">Progress</div></div>
        <div class="stat-card"><div class="stat-value">${s.current_phase ? esc(s.current_phase) : '—'}</div><div class="stat-label">Phase</div></div>
        <div class="stat-card"><div class="stat-value">${s.eta ? esc(s.eta) : '—'}</div><div class="stat-label">ETA</div></div>
    </div>`;

    // Progress bar
    html += `<div class="card" style="margin-bottom:16px">
        <div class="app4-progress-section">
            <div class="progress-bar app4-progress-bar"><div class="progress-fill app4-progress-fill ${progressClass}" style="width:${s.progress_pct}%"></div></div>
            <div class="app4-progress-label">${s.progress_pct}% complete</div>
        </div>
    </div>`;

    // Task description
    if (s.task_description) {
        html += `<div class="card" style="margin-bottom:16px">
            <div class="app4-updates-heading">Task Description</div>
            <div class="app4-agent-task">${esc(s.task_description)}</div>
        </div>`;
    }

    // Feature link
    if (s.feature_id) {
        html += `<div class="card" style="margin-bottom:16px;padding:12px 16px">
            Feature: <span class="clickable-feature" data-feature-id="${esc(s.feature_id)}">${esc(s.feature_id)}</span>
        </div>`;
    }

    // Linked Worktree
    if (wt) {
        html += `<div class="card" style="margin-bottom:16px">
            <div class="app4-updates-heading">📂 Linked Workspace</div>
            <div class="app4-wt-detail">
                <div><strong>${esc(wt.name)}</strong></div>
                <div class="app4-wt-path">${esc(wt.path)}</div>
                ${wt.branch ? `<div>Branch: <span class="badge badge-planning">${esc(wt.branch)}</span></div>` : ''}
            </div>
        </div>`;
    }

    // Status Updates Timeline
    html += `<div class="card">
        <div class="app4-updates-heading">Status Updates Timeline (${updates.length})</div>`;
    if (updates.length > 0) {
        html += `<div class="app4-timeline">`;
        for (const u of updates) {
            html += `<div class="app4-timeline-item">
                <div class="app4-timeline-dot"></div>
                <div class="app4-timeline-content">
                    <div class="app4-update-header">
                        ${u.phase ? `<span class="badge badge-planning">${esc(u.phase)}</span>` : '<span></span>'}
                        ${u.progress_pct != null ? `<span class="app4-meta-text">${u.progress_pct}%</span>` : ''}
                        <span class="app4-time-text">${timeAgo(u.created_at)}${u.created_at ? ' · ' + new Date(u.created_at + 'Z').toLocaleString() : ''}</span>
                    </div>
                    <div class="md-content">${renderMD(u.message_md)}</div>
                </div>
            </div>`;
        }
        html += `</div>`;
    } else {
        html += `<div class="app4-wt-unlinked" style="padding:12px">No status updates yet.</div>`;
    }
    html += `</div>`;

    // Timestamps
    html += `<div style="margin-top:12px;color:var(--text-secondary);font-size:12px">
        Created: ${s.created_at ? new Date(s.created_at + 'Z').toLocaleString() : '—'} · 
        Updated: ${s.updated_at ? new Date(s.updated_at + 'Z').toLocaleString() : '—'}
    </div>`;

    return html;
};

App._bindAgentsEvents = function() {
    // Agent card clicks → navigate to detail
    document.querySelectorAll('.app4-clickable-agent').forEach(card => {
        card.addEventListener('click', (e) => {
            if (e.target.closest('.clickable-feature')) return;
            App.navigateTo('agents', card.dataset.agentId);
        });
        card.style.cursor = 'pointer';
    });
    // Agent links in worktree section
    document.querySelectorAll('.app4-clickable-agent-link').forEach(link => {
        link.addEventListener('click', (e) => {
            e.stopPropagation();
            App.navigateTo('agents', link.dataset.agentId);
        });
        link.style.cursor = 'pointer';
    });
    // Back button on detail view
    const backBtn = document.getElementById('agentBackBtn');
    if (backBtn) {
        backBtn.addEventListener('click', () => { App._navContext = {}; App.navigate('agents'); });
    }
};

// =====================================================
// IDEA QUEUE PAGE
// =====================================================
App.renderIdeas = async function() {
    App._ideasViewMode = App._ideasViewMode || 'queue';
    const viewMode = App._ideasViewMode;

    if (viewMode === 'history') {
        return App.renderIdeasHistory();
    }

    const ideas = await App.api('ideas');
    const counts = { pending: 0, processing: 0, 'spec-ready': 0, approved: 0, rejected: 0 };
    ideas.forEach(i => { counts[i.status] = (counts[i.status] || 0) + 1; });

    let html = `<div class="page-header">
        <h2 class="page-title">💡 Idea Queue</h2>
        <div class="page-subtitle">${ideas.length} idea${ideas.length !== 1 ? 's' : ''} · ${counts.pending} pending · ${counts['spec-ready']} ready for review</div>
    </div>`;

    html += `<div class="app4-ideas-toolbar">
        <div class="app4-view-toggle">
            <button class="app4-toggle-btn active" data-view="queue">📋 Queue</button>
            <button class="app4-toggle-btn" data-view="history">📜 History</button>
        </div>
        <button class="app4-btn app4-btn-primary" id="submitIdeaBtn"><span class="app4-btn-icon">+</span> Submit Idea</button>
    </div>`;

    html += App.renderSearchBox('ideasSearch', 'Search ideas…');

    // Modal — simplified to match feedback modal design
    html += `<div id="ideaModal" class="feedback-modal-overlay">
        <div class="feedback-modal" role="dialog" aria-modal="true" aria-label="Submit Idea">
            <div class="feedback-modal-header">
                <span class="feedback-modal-title">Submit Idea</span>
                <button type="button" class="feedback-modal-close" id="ideaCancelBtn" aria-label="Close">&times;</button>
            </div>
            <form id="ideaForm" autocomplete="off">
                <textarea id="ideaText" class="feedback-textarea" placeholder="First line becomes the title.\nDescribe your idea below (markdown supported)" rows="10"></textarea>
                <div class="feedback-actions">
                    <button type="submit" class="feedback-submit-btn" id="ideaSubmitBtn">Submit Idea</button>
                </div>
            </form>
        </div>
    </div>`;

    // Group ideas by status
    const statusOrder = ['pending', 'processing', 'spec-ready', 'approved', 'rejected'];
    const statusLabels = { pending: '⏳ Pending', processing: '⚙️ Processing', 'spec-ready': '📋 Spec Ready', approved: '✅ Approved', rejected: '❌ Rejected' };
    const badgeMap = { pending: 'planning', processing: 'implementing', 'spec-ready': 'human-qa', approved: 'done', rejected: 'blocked' };

    for (const st of statusOrder) {
        const group = ideas.filter(i => i.status === st);
        if (group.length === 0) continue;

        const collapsed = st === 'approved' || st === 'rejected';
        if (collapsed) {
            html += `<details class="app4-past-section"><summary class="app4-past-summary">${statusLabels[st]} (${group.length})</summary><div class="app4-past-list">`;
        } else {
            html += `<h3 class="app4-section-heading">${statusLabels[st]} (${group.length})</h3>`;
        }

        for (const idea of group) {
            const typeBadge = idea.idea_type === 'bug' ? '🐛' : '✨';
            html += `<div class="card app4-idea-card" data-idea-id="${idea.id}">
                <div class="app4-idea-header">
                    <div class="app4-idea-title-row">
                        <span class="app4-idea-type-icon">${typeBadge}</span>
                        <strong>${esc(idea.title)}</strong>
                        ${idea.auto_implement ? '<span class="app4-auto-badge">🤖 auto</span>' : ''}
                    </div>
                    <div class="app4-idea-meta">
                        <span class="badge badge-${badgeMap[st] || 'planning'}">${esc(idea.status)}</span>
                        <span class="app4-time-text">${timeAgo(idea.created_at)}</span>
                    </div>
                </div>
                ${idea.raw_input ? `<div class="app4-idea-desc">${esc(idea.raw_input).substring(0, 200)}${idea.raw_input.length > 200 ? '...' : ''}</div>` : ''}
                <div class="app4-idea-author">by ${esc(idea.submitted_by || 'human')}</div>
                ${idea.feature_id ? `<div class="app4-idea-feature">→ Feature: <span class="clickable-feature" data-feature-id="${esc(idea.feature_id)}">${esc(idea.feature_id)}</span></div>` : ''}
                ${idea.spec_md ? `<details class="app4-idea-spec-details"><summary class="app4-idea-spec-summary">View Spec</summary>
                    <div class="md-content app4-idea-spec-content">${renderMD(idea.spec_md)}</div>
                </details>` : ''}
                ${st === 'spec-ready' ? `<div class="app4-idea-actions">
                    <button class="app4-btn app4-btn-approve idea-approve-btn" data-idea-id="${idea.id}">✅ Approve</button>
                    <button class="app4-btn app4-btn-reject idea-reject-btn" data-idea-id="${idea.id}">❌ Reject</button>
                </div>` : ''}
            </div>`;
        }

        if (collapsed) html += `</div></details>`;
    }

    if (ideas.length === 0) {
        html += `<div class="empty-state app4-empty-state">
            <div class="empty-state-icon">💡</div>
            <div class="empty-state-text">No ideas yet</div>
            <div class="empty-state-hint">Submit your first idea to get started. Ideas are processed by agents and turned into actionable specs.</div>
            <div class="app4-empty-pulse"></div>
        </div>`;
    }

    return html;
};

App._bindIdeasEvents = function() {
    // View toggle buttons
    document.querySelectorAll('.app4-toggle-btn').forEach(btn => {
        btn.addEventListener('click', () => {
            App._ideasViewMode = btn.dataset.view;
            App.navigate('ideas');
        });
    });

    const modal = document.getElementById('ideaModal');
    const openBtn = document.getElementById('submitIdeaBtn');
    const cancelBtn = document.getElementById('ideaCancelBtn');
    const ideaForm = document.getElementById('ideaForm');

    function showIdeaModal() {
        if (!modal) return;
        modal.classList.add('visible');
        modal.setAttribute('aria-hidden', 'false');
        var ta = document.getElementById('ideaText');
        var draft = localStorage.getItem('lifecycle_idea_draft');
        if (ta && draft) ta.value = draft;
        if (ta) setTimeout(function() { ta.focus(); }, 50);
    }
    function hideIdeaModal() {
        if (!modal) return;
        modal.classList.remove('visible');
        modal.setAttribute('aria-hidden', 'true');
    }

    if (openBtn) openBtn.addEventListener('click', showIdeaModal);
    if (cancelBtn) cancelBtn.addEventListener('click', hideIdeaModal);
    if (modal) modal.addEventListener('click', (e) => { if (e.target === modal) hideIdeaModal(); });

    // Save idea draft to localStorage on keystroke
    var ideaTextarea = document.getElementById('ideaText');
    if (ideaTextarea) {
        ideaTextarea.addEventListener('input', function() {
            localStorage.setItem('lifecycle_idea_draft', ideaTextarea.value);
        });
    }

    if (ideaForm) ideaForm.addEventListener('submit', async (e) => {
        e.preventDefault();
        var ta = document.getElementById('ideaText');
        var text = (ta && ta.value || '').trim();
        if (!text) return;
        var btn = document.getElementById('ideaSubmitBtn');
        if (btn) { btn.disabled = true; btn.textContent = 'Sending…'; }
        var lines = text.split('\n');
        var title = lines[0].substring(0, 100);
        var description = lines.slice(1).join('\n').trim();
        try {
            await App.apiPost('ideas', { title: title, raw_input: description, idea_type: 'feature', submitted_by: 'human' });
            localStorage.removeItem('lifecycle_idea_draft');
            if (ta) ta.value = '';
            hideIdeaModal();
            App.navigate('ideas');
        } catch(err) {
            App.toast('Error: ' + (err.message || 'Submit failed'), 'error');
        } finally {
            if (btn) { btn.disabled = false; btn.textContent = 'Submit Idea'; }
        }
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

    // Idea card click → detail panel
    document.querySelectorAll('.app4-idea-card').forEach(card => {
        card.style.cursor = 'pointer';
        card.addEventListener('click', (e) => {
            if (e.target.closest('.idea-approve-btn, .idea-reject-btn, .clickable-feature, .app4-idea-spec-details')) return;
            const id = card.dataset.ideaId;
            if (id) App._showIdeaDetail(id);
        });
    });
};

// Idea detail panel — overlay modal
App._showIdeaDetail = async function(ideaId) {
    let idea;
    try { idea = await App.api('ideas/' + ideaId); }
    catch(e) { App.toast('Failed to load idea: ' + e.message, 'error'); return; }

    const typeBadge = idea.idea_type === 'bug' ? '🐛 Bug' : '✨ Feature';
    const badgeMap = { pending: 'planning', processing: 'implementing', 'spec-ready': 'human-qa', approved: 'done', rejected: 'blocked' };
    const badgeCls = badgeMap[idea.status] || 'planning';
    const canAct = idea.status === 'pending' || idea.status === 'spec-ready';

    let html = `<div class="idea-detail-overlay" id="ideaDetailOverlay">
    <div class="idea-detail-panel" role="dialog" aria-modal="true" aria-label="Idea Detail">
        <div class="idea-detail-header">
            <div class="idea-detail-title-row">
                <span class="idea-detail-type">${typeBadge}</span>
                <h2 class="idea-detail-title">${esc(idea.title)}</h2>
                <span class="badge badge-${badgeCls}">${esc(idea.status)}</span>
            </div>
            <button type="button" class="feedback-modal-close" id="ideaDetailClose" aria-label="Close">&times;</button>
        </div>
        <div class="idea-detail-body">
            <div class="idea-detail-meta">
                <span>👤 ${esc(idea.submitted_by || 'human')}</span>
                <span>🕐 ${timeAgo(idea.created_at)}</span>
                ${idea.auto_implement ? '<span>🤖 Auto-implement</span>' : ''}
                ${idea.assigned_agent ? '<span>🔧 Agent: ' + esc(idea.assigned_agent) + '</span>' : ''}
            </div>`;

    if (idea.raw_input) {
        html += `<div class="idea-detail-section">
            <h3 class="idea-detail-section-title">Description</h3>
            <div class="idea-detail-desc md-content">${renderMD(idea.raw_input)}</div>
        </div>`;
    }

    if (idea.spec_md) {
        html += `<div class="idea-detail-section">
            <h3 class="idea-detail-section-title">Generated Spec</h3>
            <div class="idea-detail-spec md-content">${renderMD(idea.spec_md)}</div>
        </div>`;
    }

    if (idea.feature_id) {
        html += `<div class="idea-detail-section">
            <h3 class="idea-detail-section-title">Linked Feature</h3>
            <div class="idea-detail-feature-link clickable-feature" data-feature-id="${esc(idea.feature_id)}">→ ${esc(idea.feature_id)}</div>
        </div>`;
    }

    if (idea.source_page) {
        var pgName = idea.source_page.replace('#', '');
        pgName = pgName.charAt(0).toUpperCase() + pgName.slice(1);
        html += `<div class="idea-detail-section"><h3 class="idea-detail-section-title">Source</h3><div style="color:var(--text-dim);font-size:0.9em">\u{1F4CD} ${esc(pgName)} page</div></div>`;
    }

    if (canAct) {
        html += `<div class="idea-detail-actions">
            <button class="app4-btn app4-btn-approve" id="ideaDetailApprove">✅ Approve</button>
            <button class="app4-btn app4-btn-reject" id="ideaDetailReject">❌ Reject</button>
        </div>`;
    }

    html += `</div></div></div>`;

    // Insert overlay
    const existing = document.getElementById('ideaDetailOverlay');
    if (existing) existing.remove();
    document.body.insertAdjacentHTML('beforeend', html);

    const overlay = document.getElementById('ideaDetailOverlay');
    requestAnimationFrame(() => overlay.classList.add('visible'));

    function closeDetail() {
        overlay.classList.remove('visible');
        setTimeout(() => overlay.remove(), 200);
    }

    document.getElementById('ideaDetailClose').addEventListener('click', closeDetail);
    overlay.addEventListener('click', (e) => { if (e.target === overlay) closeDetail(); });

    // Escape key
    function onKey(e) { if (e.key === 'Escape') { closeDetail(); document.removeEventListener('keydown', onKey); } }
    document.addEventListener('keydown', onKey);

    // Clickable feature link
    overlay.querySelectorAll('.clickable-feature').forEach(el => {
        el.addEventListener('click', (e) => {
            e.preventDefault(); e.stopPropagation();
            closeDetail();
            App.navigateTo('features', el.dataset.featureId);
        });
    });

    // Approve / reject buttons
    const approveBtn = document.getElementById('ideaDetailApprove');
    const rejectBtn = document.getElementById('ideaDetailReject');
    if (approveBtn) approveBtn.addEventListener('click', async () => {
        try { await App.apiPost('ideas/' + ideaId + '/approve', {}); closeDetail(); App.navigate('ideas'); }
        catch(e2) { App.toast('Error: ' + e2.message, 'error'); }
    });
    if (rejectBtn) rejectBtn.addEventListener('click', async () => {
        try { await App.apiPost('ideas/' + ideaId + '/reject', {}); closeDetail(); App.navigate('ideas'); }
        catch(e2) { App.toast('Error: ' + e2.message, 'error'); }
    });
};
App.renderContext = async function() {
    let entries = await App.api('context');
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
    html += App.renderSearchBox('contextSearch', 'Search context entries…', App._contextSearch);

    // Type filter pills
    html += `<div class="app4-ctx-filter-row">`;
    for (const t of types) {
        const active = t === typeFilter;
        html += `<button class="app4-filter-pill ctx-type-filter ${active ? 'active' : ''}" data-type="${t}">${t}</button>`;
    }
    html += `</div>`;

    // Context cards
    if (entries.length === 0) {
        html += `<div class="empty-state app4-empty-state">
            <div class="empty-state-icon">📚</div>
            <div class="empty-state-text">No context entries</div>
            <div class="empty-state-hint">Context entries are added by agents during their work. They capture research, analysis, and notes for future reference.</div>
            <div class="app4-empty-pulse"></div>
        </div>`;
    } else {
        for (const e of entries) {
            const typeIcons = { 'source-analysis': '🔍', doc: '📄', spec: '📋', research: '🔬', note: '📝' };
            const icon = typeIcons[e.context_type] || '📎';
            const preview = (e.content_md || '').substring(0, 200).replace(/\n/g, ' ');
            html += `<div class="card app4-ctx-card ctx-card" data-ctx-id="${e.id}">
                <div class="app4-ctx-card-header">
                    <div class="app4-ctx-card-title">
                        <span class="app4-ctx-icon">${icon}</span>
                        <strong>${esc(e.title)}</strong>
                    </div>
                    <div class="app4-ctx-card-meta">
                        <span class="badge badge-planning">${esc(e.context_type)}</span>
                        <span class="app4-time-text">${timeAgo(e.created_at)}</span>
                    </div>
                </div>
                <div class="app4-ctx-preview">${esc(preview)}${(e.content_md || '').length > 200 ? '...' : ''}</div>
                <div class="app4-ctx-footer">
                    <span>by ${esc(e.author)}</span>
                    ${e.feature_id ? `<span>· Feature: <span class="clickable-feature" data-feature-id="${esc(e.feature_id)}">${esc(e.feature_id)}</span></span>` : ''}
                    ${e.tags ? `<span>· ${esc(e.tags)}</span>` : ''}
                </div>
                <div class="ctx-expanded app4-ctx-expanded">
                    <div class="md-content">${renderMD(e.content_md)}</div>
                </div>
            </div>`;
        }
    }

    return html;
};

App._bindContextEvents = function() {
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
        tocHtml += `<a href="#spec-${anchor}" class="app4-toc-link spec-toc-item">${esc(section.title)}</a>`;

        contentHtml += `<div id="spec-${anchor}" class="spec-section app4-spec-section">
            <h2 class="app4-spec-section-title">${esc(section.title)}</h2>
            <div class="md-content">${renderMD(section.content_md)}</div>`;

        if (section.features && section.features.length > 0) {
            contentHtml += `<div class="app4-spec-features">`;
            for (const f of section.features) {
                const deps = (f.dependencies || []);
                contentHtml += `<div class="card app4-spec-feature-card">
                    <div class="app4-spec-feature-header">
                        <strong class="clickable-feature" data-feature-id="${esc(f.id)}">${esc(f.name)}</strong>
                        <div class="app4-spec-feature-meta">
                            <span class="badge badge-${esc(f.status)}">${esc(f.status)}</span>
                            <span class="app4-priority-badge">P${f.priority}</span>
                        </div>
                    </div>
                    ${f.description ? `<div class="app4-spec-feature-desc">${esc(f.description)}</div>` : ''}
                    ${deps.length > 0 ? `<div class="app4-spec-deps">Depends on: ${deps.map(d => `<span class="clickable-feature app4-dep-chip" data-feature-id="${esc(d)}">${esc(d)}</span>`).join('')}</div>` : ''}
                    ${f.spec_md ? `<details class="app4-idea-spec-details"><summary class="app4-idea-spec-summary">Specification</summary>
                        <div class="md-content app4-idea-spec-content">${renderMD(f.spec_md)}</div>
                    </details>` : ''}
                </div>`;
            }
            contentHtml += `</div>`;
        }

        contentHtml += `</div>`;
    }

    // Stats footer
    const s = spec.stats || {};
    const statsHtml = `<div class="stats-grid app4-stats-row">
        <div class="stat-card stat-card--accent"><div class="stat-value">${s.total_features || 0}</div><div class="stat-label">Features</div></div>
        <div class="stat-card stat-card--success"><div class="stat-value">${s.done || 0}</div><div class="stat-label">Done</div></div>
        <div class="stat-card stat-card--warning"><div class="stat-value">${s.in_progress || 0}</div><div class="stat-label">In Progress</div></div>
        <div class="stat-card stat-card--purple"><div class="stat-value">${s.total_milestones || 0}</div><div class="stat-label">Milestones</div></div>
    </div>`;

    let html = `<div class="spec-document">
        <div class="page-header app4-spec-header">
            <div>
                <h2 class="page-title">📋 ${esc(spec.title || 'Software Specification')}</h2>
                <div class="page-subtitle">Generated ${spec.generated_at ? new Date(spec.generated_at).toLocaleString() : 'now'}</div>
            </div>
            <div>
                <button class="app4-btn app4-btn-ghost" onclick="window.print()">🖨️ Print</button>
            </div>
        </div>
        ${statsHtml}
        <div class="app4-spec-layout">
            <nav class="app4-toc-sidebar">
                <div class="app4-toc-heading">Table of Contents</div>
                ${tocHtml}
            </nav>
            <div class="app4-spec-content">
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
// GLOBAL SEARCH FILTER — reusable component
// =====================================================
App.renderSearchBox = function(id, placeholder, value) {
    const v = value || '';
    return `<div class="gsf-wrap" id="${id}Wrap"><span class="gsf-icon">🔍</span><input type="text" class="gsf-input" id="${id}" value="${esc(v)}" placeholder="${esc(placeholder || 'Search…')}" aria-label="${esc(placeholder || 'Search')}"><span class="gsf-clear" id="${id}Clear"${v ? '' : ' style="display:none"'} aria-label="Clear search">×</span><span class="gsf-count" id="${id}Count"></span></div>`;
};

App.bindSearchBox = function(id, filterFn) {
    var input = document.getElementById(id);
    var clear = document.getElementById(id + 'Clear');
    if (!input) return;
    var timer;
    var run = function() {
        clearTimeout(timer);
        timer = setTimeout(function() {
            var term = input.value.trim().toLowerCase();
            if (clear) clear.style.display = term ? '' : 'none';
            filterFn(term);
        }, 150);
    };
    input.addEventListener('input', run);
    if (clear) clear.addEventListener('click', function() {
        input.value = ''; clear.style.display = 'none';
        filterFn(''); input.focus();
    });
    if (input.value.trim()) { setTimeout(run, 0); }
};

App.updateSearchCount = function(id, shown, total) {
    var el = document.getElementById(id + 'Count');
    if (el) el.textContent = (shown < total) ? shown + ' / ' + total : '';
};

App._filterItems = function(searchId, selector, term) {
    var items = document.querySelectorAll(selector);
    var shown = 0;
    items.forEach(function(item) {
        var match = !term || item.textContent.toLowerCase().includes(term);
        item.style.display = match ? '' : 'none';
        if (match) shown++;
    });
    App.updateSearchCount(searchId, shown, items.length);
};

App._bindGlobalSearch = function(page) {
    if (page === 'cycles') {
        App.bindSearchBox('cyclesSearch', function(term) {
            App._filterItems('cyclesSearch', '.cycle-card', term);
        });
    }
    if (page === 'history') {
        App.bindSearchBox('historySearch', function(term) {
            var items = document.querySelectorAll('.timeline-item');
            var shown = 0;
            items.forEach(function(it) {
                var match = !term || it.textContent.toLowerCase().includes(term);
                it.style.display = match ? '' : 'none';
                if (match) shown++;
            });
            document.querySelectorAll('.timeline-date-group').forEach(function(g) {
                g.style.display = g.querySelectorAll('.timeline-item:not([style*="display: none"])').length ? '' : 'none';
            });
            App.updateSearchCount('historySearch', shown, items.length);
        });
    }
    if (page === 'discussions') {
        App.bindSearchBox('discSearch', function(term) {
            var rows = document.querySelectorAll('.disc-row');
            var shown = 0;
            rows.forEach(function(row) {
                var match = !term || row.textContent.toLowerCase().includes(term);
                row.style.display = match ? '' : 'none';
                var detail = document.querySelector('.disc-detail-row[data-disc-detail="' + row.dataset.discId + '"]');
                if (detail && !match) detail.style.display = 'none';
                if (match) shown++;
            });
            App.updateSearchCount('discSearch', shown, rows.length);
        });
    }
    if (page === 'roadmap') {
        App.bindSearchBox('roadmapSearch', function(term) {
            App._roadmapSearch = term;
            if (App._applyRoadmapFilters) App._applyRoadmapFilters();
        });
    }
    if (page === 'agents') {
        App.bindSearchBox('agentsSearch', function(term) {
            var cards = document.querySelectorAll('.app4-agent-card, .app4-past-card');
            var shown = 0;
            cards.forEach(function(c) {
                var match = !term || c.textContent.toLowerCase().includes(term);
                c.style.display = match ? '' : 'none';
                if (match) shown++;
            });
            App.updateSearchCount('agentsSearch', shown, cards.length);
        });
    }
    if (page === 'ideas') {
        App.bindSearchBox('ideasSearch', function(term) {
            App._filterItems('ideasSearch', '.app4-idea-card', term);
        });
    }
    if (page === 'context') {
        App.bindSearchBox('contextSearch', function(term) {
            App._contextSearch = term;
            App._filterItems('contextSearch', '.ctx-card', term);
        });
    }
};

// =====================================================
// PRINT STYLES
// =====================================================
(function() {
    const style = document.createElement('style');
    style.textContent = `
        @media print {
            .sidebar, .hamburger, .sidebar-overlay, .chord-indicator, .shortcut-modal-overlay,
            .feedback-fab, .feedback-modal-overlay,
            .spec-toc, .page-subtitle, .btn, .theme-toggle { display: none !important; }
            .content { margin: 0 !important; padding: 20px !important; }
            .spec-document { font-size: 11pt; }
            .card { break-inside: avoid; border: 1px solid #ddd !important; }
            .stats-grid { break-inside: avoid; }
        }
    `;
    document.head.appendChild(style);
})();

// =====================================================
// QUICK FEEDBACK MODAL (simplified with localStorage)
// =====================================================
var FEEDBACK_LS_KEY = 'lifecycle_feedback_draft';
var _feedbackSaveTimer = null;

App.showFeedbackModal = function() {
    var overlay = document.getElementById('feedbackModal');
    if (!overlay) return;
    overlay.classList.add('visible');
    overlay.setAttribute('aria-hidden', 'false');
    // Show captured context
    var ctxEl = document.getElementById('feedbackContextInfo');
    if (ctxEl) {
        var pg = window.location.pathname.replace(/^\//, '') || 'dashboard';
        ctxEl.textContent = '\u{1F4CD} Captured from: ' + pg.charAt(0).toUpperCase() + pg.slice(1) + ' page';
    }
    // Restore draft from localStorage
    var textarea = document.getElementById('feedbackText');
    if (textarea) {
        var draft = localStorage.getItem(FEEDBACK_LS_KEY);
        if (draft) textarea.value = draft;
        setTimeout(function() { textarea.focus(); }, 50);
    }
    // Close on backdrop click (preserve content)
    overlay.onclick = function(e) {
        if (e.target === overlay) App.hideFeedbackModal();
    };
};

App.hideFeedbackModal = function() {
    var overlay = document.getElementById('feedbackModal');
    if (!overlay) return;
    overlay.classList.remove('visible');
    overlay.setAttribute('aria-hidden', 'true');
    // Content stays in localStorage — NOT cleared on close
};

// Save to localStorage on every keystroke (debounced 300ms)
document.addEventListener('input', function(e) {
    if (e.target.id !== 'feedbackText') return;
    if (_feedbackSaveTimer) clearTimeout(_feedbackSaveTimer);
    _feedbackSaveTimer = setTimeout(function() {
        localStorage.setItem(FEEDBACK_LS_KEY, e.target.value);
    }, 300);
});

// Submit handler
document.addEventListener('submit', function(e) {
    if (!e.target.matches('#feedbackForm')) return;
    e.preventDefault();
    var textarea = document.getElementById('feedbackText');
    var text = (textarea && textarea.value || '').trim();
    if (!text) return;
    var btn = document.getElementById('feedbackSubmitBtn');
    if (btn) { btn.disabled = true; btn.textContent = 'Sending…'; }
    // Extract first line as title, rest as description
    var lines = text.split('\n');
    var title = lines[0].substring(0, 100);
    var description = lines.slice(1).join('\n').trim();
    // Capture page context
    var sourcePage = window.location.pathname || '/dashboard';
    var context = { page: sourcePage };
    try {
        var featureEls = document.querySelectorAll('[data-feature-id]');
        if (featureEls.length > 0) {
            context.visible_feature_ids = Array.from(featureEls).map(function(el) { return el.dataset.featureId; }).filter(Boolean).slice(0, 20);
        }
        var cycleEls = document.querySelectorAll('[data-cycle-id]');
        if (cycleEls.length > 0) {
            context.visible_cycle_ids = Array.from(cycleEls).map(function(el) { return el.dataset.cycleId; }).filter(Boolean).slice(0, 20);
        }
    } catch(_e) {}
    App.apiPost('ideas', {
        title: title,
        raw_input: description,
        idea_type: 'feedback',
        submitted_by: 'human',
        source_page: sourcePage,
        context: JSON.stringify(context)
    }).then(function() {
        // Clear localStorage on successful submit
        localStorage.removeItem(FEEDBACK_LS_KEY);
        if (textarea) textarea.value = '';
        App.hideFeedbackModal();
        App.toast('Feedback submitted!', 'success');
        if (typeof App.announce === 'function') App.announce('Feedback submitted');
    }).catch(function(err) {
        App.toast('Error: ' + (err.message || 'Submit failed'), 'error');
    }).finally(function() {
        if (btn) { btn.disabled = false; btn.textContent = 'Submit'; }
    });
});

App.showToast = function(message) {
    var existing = document.querySelector('.toast-notification');
    if (existing) existing.remove();
    var toast = document.createElement('div');
    toast.className = 'toast-notification';
    toast.setAttribute('role', 'status');
    toast.setAttribute('aria-live', 'polite');
    toast.textContent = message;
    document.body.appendChild(toast);
    // Trigger reflow then add visible class
    toast.offsetHeight;
    toast.classList.add('visible');
    setTimeout(function() {
        toast.classList.remove('visible');
        setTimeout(function() { toast.remove(); }, 300);
    }, 2000);
};

// Reusable inline edit function — used by features, roadmap items, milestones
App.inlineEdit = function(el, opts) {
    if (el.querySelector('.inline-edit-input')) return;
    var original = opts.value;
    var input;
    if (opts.type === 'select') {
        input = document.createElement('select');
        input.className = 'inline-edit-input inline-edit-select';
        (opts.options || []).forEach(function(o) {
            var opt = document.createElement('option');
            opt.value = typeof o === 'object' ? o.value : o;
            opt.textContent = typeof o === 'object' ? o.label : o;
            if ((typeof o === 'object' ? o.value : o) === original) opt.selected = true;
            input.appendChild(opt);
        });
    } else if (opts.type === 'textarea') {
        input = document.createElement('textarea');
        input.className = 'inline-edit-input inline-edit-textarea';
        input.value = original;
        input.rows = 3;
    } else {
        input = document.createElement('input');
        input.className = 'inline-edit-input';
        input.type = 'text';
        input.value = original;
    }
    el.dataset.originalHtml = el.innerHTML;
    el.innerHTML = '';
    el.appendChild(input);
    input.focus();
    if (input.select && opts.type !== 'select') input.select();

    var done = false;
    var save = async function() {
        if (done) return; done = true;
        var newVal = input.value.trim();
        if (newVal && newVal !== original) {
            try { await opts.onSave(newVal); App.showToast('\u2713 Saved'); }
            catch(e) { App.showToast('\u2717 ' + (e.message || 'Save failed')); el.innerHTML = el.dataset.originalHtml; return; }
        }
        el.innerHTML = newVal ? App.esc(newVal) : el.dataset.originalHtml;
    };
    var cancel = function() { if (done) return; done = true; el.innerHTML = el.dataset.originalHtml; };
    input.addEventListener('keydown', function(e) {
        if (e.key === 'Enter' && !(opts.type === 'textarea' && e.shiftKey)) { e.preventDefault(); save(); }
        if (e.key === 'Escape') { e.preventDefault(); cancel(); }
    });
    input.addEventListener('blur', function() { setTimeout(save, 150); });
    if (opts.type === 'select') input.addEventListener('change', save);
};

// =====================================================
// GIT/VCS ACTIVITY (used by Dashboard)
// =====================================================
App.renderGitActivity = function() {
    return App.api('git/log').then(function(data) {
        var commits = data && data.commits;
        if (!commits || !commits.length) return '';
        var vcsLabel = (data.vcs === 'jj') ? 'jj' : 'git';
        var html = '<div class="card"><div class="card-title" style="margin-bottom:8px">📝 Recent Commits <span class="text-secondary" style="font-size:0.72rem;font-weight:400">(' + App.esc(vcsLabel) + ')</span></div><div class="commit-list">';
        commits.slice(0, 8).forEach(function(c) {
            var hash = c.hash ? c.hash.substring(0, 8) : '';
            var date = c.date || '';
            if (date.length > 16) date = date.substring(0, 16);
            html += '<div class="commit-row">'
                + '<code class="commit-hash">' + App.esc(hash) + '</code>'
                + '<span class="commit-msg">' + App.esc(c.message || '') + '</span>'
                + '<span class="commit-author text-secondary">' + App.esc(c.author || '') + '</span>'
                + '<span class="commit-date text-secondary">' + App.esc(date) + '</span>'
                + '</div>';
        });
        html += '</div></div>';
        return html;
    }).catch(function() { return ''; });
};

// =====================================================
// DECISIONS (ADR) PAGE
// =====================================================
App.renderDecisions = async function() {
    var decisions = [];
    try { decisions = await App.api('decisions'); } catch(e) { decisions = []; }

    var statusIcons = {
        proposed: '📝', accepted: '✅', rejected: '❌',
        superseded: '🔄', deprecated: '⚠️'
    };
    var statusColors = {
        proposed: 'var(--color-warning)', accepted: 'var(--color-success)',
        rejected: 'var(--color-danger, #e74c3c)', superseded: 'var(--color-info, #3498db)',
        deprecated: 'var(--color-muted, #95a5a6)'
    };

    var html = '<div class="page-header">'
        + '<h2 class="page-title">📐 Architecture Decisions</h2>'
        + '<div class="page-subtitle">' + decisions.length + ' decision' + (decisions.length !== 1 ? 's' : '') + ' recorded</div>'
        + '</div>';

    // Stats row
    var counts = {proposed:0, accepted:0, rejected:0, superseded:0, deprecated:0};
    decisions.forEach(function(d) { if (counts[d.status] !== undefined) counts[d.status]++; });
    html += '<div class="stats-grid app4-stats-row">'
        + '<div class="stat-card"><div class="stat-value">' + decisions.length + '</div><div class="stat-label">Total</div></div>'
        + '<div class="stat-card stat-card--accent"><div class="stat-value">' + counts.proposed + '</div><div class="stat-label">Proposed</div></div>'
        + '<div class="stat-card stat-card--success"><div class="stat-value">' + counts.accepted + '</div><div class="stat-label">Accepted</div></div>'
        + '<div class="stat-card stat-card--warning"><div class="stat-value">' + (counts.rejected + counts.superseded + counts.deprecated) + '</div><div class="stat-label">Closed</div></div>'
        + '</div>';

    if (decisions.length === 0) {
        html += '<div class="empty-state">'
            + '<div class="empty-state-icon">📐</div>'
            + '<div class="empty-state-text">No decisions recorded yet</div>'
            + '<div class="empty-state-hint">Use <code>lifecycle decision add "Title"</code> to record an architecture decision.</div>'
            + '</div>';
        return html;
    }

    html += '<div class="card-grid" style="display:flex;flex-direction:column;gap:12px;">';
    decisions.forEach(function(d) {
        var icon = statusIcons[d.status] || '📝';
        var color = statusColors[d.status] || 'var(--color-muted)';
        html += '<div class="card" style="padding:16px;border-left:4px solid ' + color + ';">'
            + '<div style="display:flex;justify-content:space-between;align-items:center;margin-bottom:8px;">'
            + '<h3 style="margin:0;font-size:1.1em;">' + icon + ' ' + App.esc(d.title) + '</h3>'
            + '<span class="badge" style="background:' + color + ';color:white;padding:2px 8px;border-radius:4px;font-size:0.8em;">' + d.status + '</span>'
            + '</div>'
            + '<div style="font-size:0.85em;color:var(--color-text-secondary);margin-bottom:8px;">'
            + '<code>' + App.esc(d.id) + '</code>';
        if (d.feature_id) {
            html += ' · Feature: <a href="#" onclick="App.navigate(\'features\',{id:\'' + App.esc(d.feature_id) + '\'});return false;">' + App.esc(d.feature_id) + '</a>';
        }
        html += ' · ' + timeAgo(d.created_at) + '</div>';

        if (d.context) {
            html += '<details style="margin-bottom:6px;"><summary style="cursor:pointer;font-weight:600;font-size:0.9em;">Context</summary>'
                + '<div style="padding:8px 0 0 12px;font-size:0.9em;white-space:pre-wrap;">' + App.esc(d.context) + '</div></details>';
        }
        if (d.decision) {
            html += '<details style="margin-bottom:6px;"><summary style="cursor:pointer;font-weight:600;font-size:0.9em;">Decision</summary>'
                + '<div style="padding:8px 0 0 12px;font-size:0.9em;white-space:pre-wrap;">' + App.esc(d.decision) + '</div></details>';
        }
        if (d.consequences) {
            html += '<details style="margin-bottom:6px;"><summary style="cursor:pointer;font-weight:600;font-size:0.9em;">Consequences</summary>'
                + '<div style="padding:8px 0 0 12px;font-size:0.9em;white-space:pre-wrap;">' + App.esc(d.consequences) + '</div></details>';
        }
        html += '</div>';
    });
    html += '</div>';
    return html;
};

// =====================================================
// ACCESSIBILITY: Announce page navigations to screen readers
// =====================================================
(function() {
    var _origNavigate = App.navigate.bind(App);
    App.navigate = async function(page, context) {
        var result = await _origNavigate(page, context);
        var label = page.charAt(0).toUpperCase() + page.slice(1);
        App.announce(label + ' page loaded');
        return result;
    };
})();
