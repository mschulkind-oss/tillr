// app6.js — Agent Heartbeat Dashboard Panel (Stats page integration)

// =====================================================
// AGENT HEARTBEAT PANEL — added to the Stats page
// =====================================================

App._renderAgentHeartbeatPanel = async function() {
    try {
        var data = await App.api('agents/status');
    } catch(e) {
        return '';
    }
    var agents = data.agents || [];
    if (agents.length === 0 && data.total_sessions === 0) {
        return '';
    }

    var html = '<div class="stats-section-header"><h3>Agent Activity</h3></div>';
    html += '<div class="stats-grid">';

    // Summary cards
    html += '<div class="stats-card stats-card-full"><div class="ahb-summary-row">';
    html += '<div class="stat-card stat-card--accent"><div class="stat-value">' + data.total_sessions + '</div><div class="stat-label">Total Sessions</div></div>';
    html += '<div class="stat-card stat-card--success"><div class="stat-value">' + data.active_count + '</div><div class="stat-label">Active</div></div>';
    html += '<div class="stat-card stat-card--warning"><div class="stat-value">' + data.stale_count + '</div><div class="stat-label">Stale</div></div>';
    html += '<div class="stat-card" style="--accent:var(--danger)"><div class="stat-value">' + data.failed_count + '</div><div class="stat-label">Failed</div></div>';
    html += '<div class="stat-card"><div class="stat-value">' + data.completed_count + '</div><div class="stat-label">Completed</div></div>';
    html += '<div class="stat-card stat-card--accent"><div class="stat-value">' + data.total_work_done + '</div><div class="stat-label">Work Items Done</div></div>';
    html += '</div></div>';

    // Agent list — only show if we have agents
    if (agents.length > 0) {
        // Sort: active first, then stale, then completed/failed
        var order = {active: 0, stale: 1, paused: 2, completed: 3, failed: 4};
        agents.sort(function(a, b) {
            return (order[a.heartbeat_status] || 9) - (order[b.heartbeat_status] || 9);
        });

        html += '<div class="stats-card stats-card-full">';
        html += '<div class="stats-card-title">Agent Sessions</div>';
        html += '<div class="ahb-agent-list">';
        for (var i = 0; i < agents.length; i++) {
            var ag = agents[i];
            var s = ag.session;
            var statusClass = 'ahb-status-' + ag.heartbeat_status;
            var statusIcon = ag.heartbeat_status === 'active' ? '🟢' :
                ag.heartbeat_status === 'stale' ? '🟡' :
                ag.heartbeat_status === 'failed' ? '🔴' :
                ag.heartbeat_status === 'completed' ? '✅' : '⚪';

            html += '<div class="ahb-agent-card ' + statusClass + '">';
            html += '<div class="ahb-agent-header">';
            html += '<span class="ahb-agent-icon">' + statusIcon + '</span>';
            html += '<span class="ahb-agent-name">' + App._esc(s.name) + '</span>';
            html += '<span class="badge badge-' + App._heartbeatBadgeClass(ag.heartbeat_status) + '">' + App._esc(ag.heartbeat_status) + '</span>';
            html += '</div>';

            html += '<div class="ahb-agent-details">';

            // Current task
            if (s.task_description) {
                html += '<div class="ahb-detail-row"><span class="ahb-detail-label">Task</span><span class="ahb-detail-value">' + App._esc(App._truncate(s.task_description, 80)) + '</span></div>';
            }

            // Feature
            if (ag.feature_name) {
                html += '<div class="ahb-detail-row"><span class="ahb-detail-label">Feature</span><span class="ahb-detail-value clickable-feature" data-feature-id="' + App._esc(s.feature_id) + '">' + App._esc(ag.feature_name) + '</span></div>';
            }

            // Current work item
            if (ag.current_work_item) {
                var wi = ag.current_work_item;
                html += '<div class="ahb-detail-row"><span class="ahb-detail-label">Working On</span><span class="ahb-detail-value"><span class="badge badge-implementing">' + App._esc(wi.work_type) + '</span> #' + wi.id + '</span></div>';
            }

            // Phase & Progress
            if (s.current_phase) {
                html += '<div class="ahb-detail-row"><span class="ahb-detail-label">Phase</span><span class="ahb-detail-value">' + App._esc(s.current_phase) + '</span></div>';
            }
            if (s.progress_pct > 0) {
                html += '<div class="ahb-detail-row"><span class="ahb-detail-label">Progress</span><span class="ahb-detail-value"><div class="ahb-progress"><div class="ahb-progress-bar" style="width:' + s.progress_pct + '%"></div><span class="ahb-progress-text">' + s.progress_pct + '%</span></div></span></div>';
            }

            // Last heartbeat
            html += '<div class="ahb-detail-row"><span class="ahb-detail-label">Last Heartbeat</span><span class="ahb-detail-value">' + App._relTime(s.updated_at) + '</span></div>';

            // Session duration
            html += '<div class="ahb-detail-row"><span class="ahb-detail-label">Duration</span><span class="ahb-detail-value">' + App._formatDuration(ag.session_duration_secs) + '</span></div>';

            // Work stats
            if (ag.completed_count > 0 || ag.failed_count > 0) {
                html += '<div class="ahb-detail-row"><span class="ahb-detail-label">Work Items</span><span class="ahb-detail-value">';
                html += '<span class="ahb-work-stat ahb-work-done">✓ ' + ag.completed_count + '</span>';
                if (ag.failed_count > 0) {
                    html += ' <span class="ahb-work-stat ahb-work-failed">✗ ' + ag.failed_count + '</span>';
                }
                html += '</span></div>';
            }

            html += '</div></div>';
        }
        html += '</div></div>';
    }

    html += '</div>';
    return html;
};

// Helper: badge class for heartbeat status
App._heartbeatBadgeClass = function(status) {
    switch(status) {
        case 'active': return 'done';
        case 'stale': return 'human-qa';
        case 'failed': return 'blocked';
        case 'completed': return 'done';
        default: return 'draft';
    }
};

// Helper: HTML escape
App._esc = function(str) {
    if (!str) return '';
    var d = document.createElement('div');
    d.textContent = str;
    return d.innerHTML;
};

// Helper: truncate string
App._truncate = function(str, max) {
    if (!str || str.length <= max) return str;
    return str.substring(0, max) + '…';
};

// Helper: relative time
App._relTime = function(dateStr) {
    if (!dateStr) return '';
    var d = new Date(dateStr.indexOf('T') < 0 ? dateStr.replace(' ', 'T') + 'Z' : dateStr);
    var now = new Date();
    var diff = Math.floor((now - d) / 1000);
    if (diff < 60) return 'just now';
    if (diff < 3600) return Math.floor(diff / 60) + 'm ago';
    if (diff < 86400) return Math.floor(diff / 3600) + 'h ago';
    return Math.floor(diff / 86400) + 'd ago';
};

// Helper: format seconds to human-readable duration
App._formatDuration = function(secs) {
    if (!secs || secs <= 0) return '—';
    if (secs < 60) return secs + 's';
    var mins = Math.floor(secs / 60);
    if (mins < 60) return mins + 'm';
    var hrs = Math.floor(mins / 60);
    var remMins = mins % 60;
    if (hrs < 24) return hrs + 'h ' + remMins + 'm';
    var days = Math.floor(hrs / 24);
    var remHrs = hrs % 24;
    return days + 'd ' + remHrs + 'h';
};

// Monkey-patch renderStats to append the agent heartbeat panel
(function() {
    var origRenderStats = App.renderStats;
    App.renderStats = async function() {
        var baseHtml = await origRenderStats.call(this);
        var agentPanel = await App._renderAgentHeartbeatPanel();
        return baseHtml + (agentPanel || '');
    };
})();

// =====================================================
// WORKFLOW VISUALIZATION PAGE
// =====================================================

// SVG arrow builder
App._wfArrow = function(label) {
    var svg = '<svg width="48" height="20" viewBox="0 0 48 20" fill="none">'
        + '<line x1="0" y1="10" x2="40" y2="10" stroke="currentColor" stroke-width="2"/>'
        + '<polygon points="40,5 48,10 40,15" fill="currentColor"/>'
        + '</svg>';
    var html = '<span class="wf-arrow wf-arrow--horiz">' + svg;
    if (label) html += '<span class="wf-arrow-label">' + App._esc(label) + '</span>';
    html += '</span>';
    return html;
};

// SVG down-arrow
App._wfArrowDown = function() {
    return '<svg width="20" height="28" viewBox="0 0 20 28" fill="none">'
        + '<line x1="10" y1="0" x2="10" y2="20" stroke="currentColor" stroke-width="2"/>'
        + '<polygon points="5,20 10,28 15,20" fill="currentColor"/>'
        + '</svg>';
};

// Build a flowchart node
App._wfNode = function(icon, label, status) {
    return '<span class="wf-node wf-node--' + status + '">'
        + '<span class="wf-node-icon">' + icon + '</span>'
        + App._esc(label)
        + '</span>';
};

// Determine node status from live agent data
App._wfNodeStatuses = function(agents) {
    var statuses = { queue: 'waiting', next: 'waiting', working: 'waiting',
        done: 'waiting', fail: 'waiting', review: 'waiting', approve: 'waiting', reject: 'waiting' };
    if (!agents || agents.length === 0) return statuses;
    var hasActive = false, hasDone = false, hasFailed = false;
    for (var i = 0; i < agents.length; i++) {
        var hs = agents[i].heartbeat_status;
        if (hs === 'active') hasActive = true;
        if (hs === 'completed') hasDone = true;
        if (hs === 'failed') hasFailed = true;
    }
    if (hasActive) {
        statuses.queue = 'done';
        statuses.next = 'done';
        statuses.working = 'active';
    }
    if (hasDone) {
        statuses.done = 'done';
    }
    if (hasFailed) {
        statuses.fail = 'failed';
    }
    if (hasDone || hasFailed) {
        statuses.review = 'active';
    }
    return statuses;
};

// Main render function
App.renderWorkflow = async function() {
    var agentData = null;
    try { agentData = await App.api('agents/status'); } catch(e) { /* ok */ }
    var agents = (agentData && agentData.agents) || [];
    var ns = App._wfNodeStatuses(agents);

    // History for recent completions
    var history = [];
    try { history = await App.api('history?limit=30'); } catch(e) { /* ok */ }
    if (!Array.isArray(history)) history = [];
    var completions = [];
    for (var i = 0; i < history.length && completions.length < 10; i++) {
        var ev = history[i];
        if (ev.event_type === 'work_completed' || ev.event_type === 'work_failed'
            || ev.event_type === 'feature_status_changed' || ev.event_type === 'cycle_step_completed') {
            completions.push(ev);
        }
    }

    var html = '<div class="page-header">'
        + '<h2 class="page-title">🔀 Agent Workflow</h2>'
        + '<div class="page-subtitle">Visual flowchart of how agents process work items through the lifecycle</div>'
        + '</div>';

    // --- Flowchart Diagram ---
    html += '<div class="wf-section">';
    html += '<div class="wf-section-title">📐 Workflow Diagram</div>';
    html += '<div class="wf-diagram">';

    // Main flow: Queue → next → Agent Working → done/fail → Queue
    html += '<div class="wf-main-flow">';
    html += App._wfNode('📥', 'Queue', ns.queue);
    html += App._wfArrow('next');
    html += App._wfNode('⚡', 'Agent Working', ns.working);
    html += App._wfArrow('done');
    html += App._wfNode('✅', 'Complete', ns.done);
    html += App._wfArrow('');
    html += App._wfNode('📥', 'Queue', ns.queue);
    html += '</div>';

    // Vertical connector from "Agent Working" down to branch
    html += '<div class="wf-vert-connector" style="color:var(--text-muted)">';
    html += App._wfArrowDown();
    html += '<span style="font-size:var(--font-xs);color:var(--text-muted);font-family:var(--font-mono)">fail</span>';
    html += '</div>';

    // Branch: Failed → Human Review → approve/reject
    html += '<div class="wf-branch">';
    html += App._wfNode('❌', 'Failed', ns.fail);
    html += App._wfArrow('');
    html += App._wfNode('👤', 'Human Review', ns.review);
    html += App._wfArrow('approve');
    html += App._wfNode('🔄', 'Retry', ns.approve === 'done' ? 'done' : 'waiting');
    html += '</div>';

    // Legend
    html += '<div class="wf-legend">';
    html += '<div class="wf-legend-item"><span class="wf-legend-dot wf-legend-dot--waiting"></span> Waiting</div>';
    html += '<div class="wf-legend-item"><span class="wf-legend-dot wf-legend-dot--active"></span> Active</div>';
    html += '<div class="wf-legend-item"><span class="wf-legend-dot wf-legend-dot--done"></span> Done</div>';
    html += '<div class="wf-legend-item"><span class="wf-legend-dot wf-legend-dot--failed"></span> Failed</div>';
    html += '</div>';

    html += '</div></div>';

    // --- Active Agents Panel ---
    html += '<div class="wf-section">';
    html += '<div class="wf-section-title">🤖 Active Agents';
    if (agentData) {
        html += ' <span class="badge badge-done">' + (agentData.active_count || 0) + ' active</span>';
    }
    html += '</div>';

    var activeAgents = agents.filter(function(a) { return a.heartbeat_status === 'active' || a.heartbeat_status === 'stale'; });

    if (activeAgents.length === 0) {
        html += '<div class="empty-state" style="padding:var(--space-8) 0">';
        html += '<div class="empty-state-icon">😴</div>';
        html += '<div class="empty-state-text">No active agents</div>';
        html += '<div class="empty-state-hint">Agents appear here when processing work items via <code>lifecycle next</code></div>';
        html += '</div>';
    } else {
        html += '<div class="wf-agents-grid">';
        for (var j = 0; j < activeAgents.length; j++) {
            var ag = activeAgents[j];
            var s = ag.session;
            var dotClass = 'wf-heartbeat-dot--' + ag.heartbeat_status;

            html += '<div class="wf-agent-card" style="animation-delay:' + (j * 0.05) + 's">';
            html += '<div class="wf-agent-header">';
            html += '<span class="wf-heartbeat-dot ' + dotClass + '" title="' + App._esc(ag.heartbeat_status) + '"></span>';
            html += '<span class="wf-agent-name">' + App._esc(s.name || 'Agent #' + s.id) + '</span>';
            html += '<span class="badge badge-' + App._heartbeatBadgeClass(ag.heartbeat_status) + '">' + App._esc(ag.heartbeat_status) + '</span>';
            html += '</div>';

            html += '<div class="wf-agent-meta">';

            if (ag.current_work_item) {
                html += '<div class="wf-agent-row"><span class="wf-agent-label">Work Item</span>';
                html += '<span class="wf-agent-value"><span class="badge badge-implementing">' + App._esc(ag.current_work_item.work_type) + '</span> #' + ag.current_work_item.id + '</span></div>';
            }
            if (ag.feature_name) {
                html += '<div class="wf-agent-row"><span class="wf-agent-label">Feature</span>';
                html += '<span class="wf-agent-value">' + App._esc(ag.feature_name) + '</span></div>';
            }
            html += '<div class="wf-agent-row"><span class="wf-agent-label">Active</span>';
            html += '<span class="wf-agent-value">' + App._formatDuration(ag.session_duration_secs) + '</span></div>';

            html += '<div class="wf-agent-row"><span class="wf-agent-label">Heartbeat</span>';
            html += '<span class="wf-agent-value">' + App._relTime(s.updated_at) + '</span></div>';

            if (s.current_phase) {
                html += '<div class="wf-agent-row"><span class="wf-agent-label">Phase</span>';
                html += '<span class="wf-agent-value">' + App._esc(s.current_phase) + '</span></div>';
            }
            if (ag.completed_count > 0 || ag.failed_count > 0) {
                html += '<div class="wf-agent-row"><span class="wf-agent-label">Items</span>';
                html += '<span class="wf-agent-value">';
                html += '<span style="color:var(--success)">✓ ' + ag.completed_count + '</span>';
                if (ag.failed_count > 0) html += ' <span style="color:var(--danger)">✗ ' + ag.failed_count + '</span>';
                html += '</span></div>';
            }

            html += '</div></div>';
        }
        html += '</div>';
    }
    html += '</div>';

    // --- Recent Completions ---
    html += '<div class="wf-section">';
    html += '<div class="wf-section-title">📋 Recent Completions</div>';

    if (completions.length === 0) {
        html += '<div class="empty-state" style="padding:var(--space-6) 0">';
        html += '<div class="empty-state-icon">📭</div>';
        html += '<div class="empty-state-text">No recent completions</div>';
        html += '<div class="empty-state-hint">Completed work items will appear here</div>';
        html += '</div>';
    } else {
        html += '<div class="wf-completions-list">';
        for (var k = 0; k < completions.length; k++) {
            var c = completions[k];
            var cIcon = c.event_type === 'work_completed' ? '✅' :
                c.event_type === 'work_failed' ? '❌' :
                c.event_type === 'cycle_step_completed' ? '🔄' : '📌';
            var evLabel = (c.event_type || '').replace(/_/g, ' ');
            var detail = '';
            if (c.details) {
                try {
                    var d = typeof c.details === 'string' ? JSON.parse(c.details) : c.details;
                    if (d.work_type) detail = d.work_type;
                    else if (d.new_status) detail = d.new_status;
                    else if (d.step) detail = d.step;
                } catch(e) { /* ok */ }
            }

            html += '<div class="wf-completion-row" style="animation-delay:' + (k * 0.03) + 's">';
            html += '<span class="wf-completion-icon">' + cIcon + '</span>';
            html += '<div class="wf-completion-info">';
            html += '<div class="wf-completion-title">' + App._esc(evLabel) + '</div>';
            var subParts = [];
            if (c.feature_id) subParts.push(c.feature_id);
            if (detail) subParts.push(detail);
            if (subParts.length > 0) {
                html += '<div class="wf-completion-sub">' + App._esc(subParts.join(' · ')) + '</div>';
            }
            html += '</div>';
            if (c.feature_id) {
                html += '<span class="badge badge-implementing" style="font-size:var(--font-xs)">' + App._esc(c.feature_id) + '</span>';
            }
            html += '<span class="wf-completion-time">' + App._relTime(c.created_at) + '</span>';
            html += '</div>';
        }
        html += '</div>';
    }
    html += '</div>';

    return html;
};

// Monkey-patch renderPage to handle the 'workflow' route
(function() {
    var origRenderPage = App.renderPage;
    App.renderPage = async function(page) {
        if (page === 'workflow') return App.renderWorkflow();
        return origRenderPage.call(this, page);
    };
})();
