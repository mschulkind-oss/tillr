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
                    <div class="app4-progress-label">${a.progress_pct}% complete · Last active ${timeAgo(a.updated_at)}</div>
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
    const ideas = await App.api('ideas');
    const counts = { pending: 0, processing: 0, 'spec-ready': 0, approved: 0, rejected: 0 };
    ideas.forEach(i => { counts[i.status] = (counts[i.status] || 0) + 1; });

    let html = `<div class="page-header">
        <h2 class="page-title">💡 Idea Queue</h2>
        <div class="page-subtitle">${ideas.length} idea${ideas.length !== 1 ? 's' : ''} · ${counts.pending} pending · ${counts['spec-ready']} ready for review</div>
    </div>`;

    // Submit button
    html += `<div class="app4-ideas-toolbar">
        <button class="app4-btn app4-btn-primary" id="submitIdeaBtn"><span class="app4-btn-icon">+</span> Submit Idea</button>
    </div>`;

    html += App.renderSearchBox('ideasSearch', 'Search ideas…');

    // Modal (hidden by default)
    html += `<div id="ideaModal" class="app4-modal-overlay">
        <div class="card app4-modal-card">
            <div class="app4-modal-header">
                <h3 class="app4-modal-title">Submit New Idea</h3>
                <button class="app4-modal-close" id="ideaCancelBtn" aria-label="Close">✕</button>
            </div>
            <div class="app4-modal-body">
                <div class="app4-form-group">
                    <label class="app4-form-label" for="ideaTitle">Title <span class="app4-required">*</span></label>
                    <input type="text" id="ideaTitle" class="app4-form-input" placeholder="What's the idea?">
                </div>
                <div class="app4-form-group">
                    <label class="app4-form-label" for="ideaDesc">Description</label>
                    <textarea id="ideaDesc" rows="5" class="app4-form-input app4-form-textarea" placeholder="Describe the idea (markdown supported)"></textarea>
                </div>
                <div class="app4-form-row">
                    <div class="app4-form-group" style="flex:1">
                        <label class="app4-form-label" for="ideaType">Type</label>
                        <select id="ideaType" class="app4-form-input">
                            <option value="feature">✨ Feature</option>
                            <option value="bug">🐛 Bug</option>
                        </select>
                    </div>
                    <div class="app4-form-group app4-form-checkbox-group">
                        <label class="app4-checkbox-label">
                            <input type="checkbox" id="ideaAuto" class="app4-checkbox"> Auto-implement
                        </label>
                    </div>
                </div>
            </div>
            <div class="app4-modal-footer">
                <button class="app4-btn app4-btn-ghost" id="ideaCancelBtn2">Cancel</button>
                <button class="app4-btn app4-btn-primary" id="ideaSubmitBtn">Submit Idea</button>
            </div>
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
    const modal = document.getElementById('ideaModal');
    const openBtn = document.getElementById('submitIdeaBtn');
    const cancelBtn = document.getElementById('ideaCancelBtn');
    const cancelBtn2 = document.getElementById('ideaCancelBtn2');
    const submitBtn = document.getElementById('ideaSubmitBtn');

    if (openBtn) openBtn.addEventListener('click', () => { if (modal) modal.style.display = 'flex'; });
    if (cancelBtn) cancelBtn.addEventListener('click', () => { if (modal) modal.style.display = 'none'; });
    if (cancelBtn2) cancelBtn2.addEventListener('click', () => { if (modal) modal.style.display = 'none'; });
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
// QUICK FEEDBACK MODAL
// =====================================================
App.showFeedbackModal = function() {
    var overlay = document.getElementById('feedbackModal');
    if (!overlay) return;
    overlay.classList.add('visible');
    overlay.setAttribute('aria-hidden', 'false');
    // Reset form
    var form = document.getElementById('feedbackForm');
    if (form) form.reset();
    // Auto-focus title
    var title = document.getElementById('feedbackTitle');
    if (title) setTimeout(function() { title.focus(); }, 50);
    // Close on backdrop click
    overlay.onclick = function(e) {
        if (e.target === overlay) App.hideFeedbackModal();
    };
};

App.hideFeedbackModal = function() {
    var overlay = document.getElementById('feedbackModal');
    if (!overlay) return;
    overlay.classList.remove('visible');
    overlay.setAttribute('aria-hidden', 'true');
};

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

// Bind feedback form submission
(function() {
    document.addEventListener('DOMContentLoaded', function() {
        var form = document.getElementById('feedbackForm');
        if (!form) return;
        form.addEventListener('submit', async function(e) {
            e.preventDefault();
            var title = document.getElementById('feedbackTitle').value.trim();
            if (!title) return;
            var desc = document.getElementById('feedbackDesc').value.trim();
            var type = document.getElementById('feedbackType').value;
            var submitBtn = document.getElementById('feedbackSubmitBtn');
            if (submitBtn) { submitBtn.disabled = true; submitBtn.textContent = 'Submitting…'; }
            try {
                await App.apiPost('ideas', {
                    title: title,
                    raw_input: desc,
                    idea_type: type === 'idea' ? 'feature' : type,
                });
                App.hideFeedbackModal();
                App.showToast('✓ Feedback submitted');
            } catch(err) {
                App.showToast('✗ Error: ' + err.message);
            } finally {
                if (submitBtn) { submitBtn.disabled = false; submitBtn.textContent = 'Submit'; }
            }
        });
    });
})();

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
