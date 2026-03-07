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
