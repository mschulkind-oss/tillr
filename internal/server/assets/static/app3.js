// ── Enhanced Cycles: Detail Drill-Down ──

App._cycleTypeIcons = {
    'ui-refinement': '🎨', 'feature-implementation': '⚙️',
    'roadmap-planning': '📋', 'bug-triage': '🐛',
    'documentation': '📖', 'architecture-review': '🏗️',
    'release': '🚀', 'onboarding-dx': '👋', 'spec-iteration': '📝'
};

App._cycleTypeSteps = {
    'ui-refinement': ['design','ux-review','develop','manual-qa','judge'],
    'feature-implementation': ['research','develop','agent-qa','judge','human-qa'],
    'roadmap-planning': ['research','plan','create-roadmap','prioritize','human-review'],
    'bug-triage': ['report','reproduce','root-cause','fix','verify'],
    'documentation': ['research','draft','review','edit','publish'],
    'architecture-review': ['analyze','propose','discuss','decide','implement'],
    'release': ['freeze','qa','fix','staging','verify','ship'],
    'onboarding-dx': ['try','friction-log','improve','verify','document'],
    'spec-iteration': ['research','draft-spec','review','judge','human-review']
};

App.showCycleDetail = function(cycleId) {
    App._activeCycleDetail = cycleId;
    App._breadcrumbDetail = 'Cycle #' + cycleId;
    App.updateBreadcrumbs();
    App.loadCycleDetail(cycleId);
};

App.loadCycleDetail = function(cycleId) {
    var content = document.getElementById('content');
    if (!content) return;

    // Keep breadcrumb if present
    var bc = content.querySelector('.breadcrumb-bar');
    var tempFrag = document.createDocumentFragment();
    if (bc) tempFrag.appendChild(bc);

    content.innerHTML = '<div class="loading-spinner" style="text-align:center;padding:60px 0"><div class="spinner"></div></div>';
    if (tempFrag.children.length) content.insertBefore(tempFrag.children[0], content.firstChild);

    App.api('cycles/' + cycleId).then(function(data) {
        App.renderCycleDetailView(data);
    }).catch(function() {
        // Fallback: try separate requests
        Promise.all([
            App.api('cycles'),
            App.api('cycles/' + cycleId + '/scores')
        ]).then(function(results) {
            var cycles = results[0];
            var scores = results[1];
            var cycle = null;
            for (var i = 0; i < cycles.length; i++) {
                if (cycles[i].id === cycleId) { cycle = cycles[i]; break; }
            }
            if (!cycle) {
                content.innerHTML = '<div class="empty-state"><div class="empty-state-icon">❌</div><div class="empty-state-text">Cycle not found</div></div>';
                return;
            }
            var steps = App._cycleTypeSteps[cycle.cycle_type] || [];
            App.renderCycleDetailView({ cycle: cycle, scores: scores || [], steps: steps });
        });
    });
};

App.renderCycleDetailView = function(data) {
    var content = document.getElementById('content');
    if (!content) return;

    var cycle = data.cycle;
    var scores = data.scores || [];
    var steps = data.steps || App._cycleTypeSteps[cycle.cycle_type] || [];
    var icon = App._cycleTypeIcons[cycle.cycle_type] || '🔄';
    var totalSteps = steps.length;
    var pct = totalSteps > 0 ? Math.round((cycle.current_step / totalSteps) * 100) : 0;
    var avgScore = scores.length ? (scores.reduce(function(a, s) { return a + s.score; }, 0) / scores.length) : null;

    // Group scores by iteration
    var iterations = {};
    var maxIter = cycle.iteration || 1;
    scores.forEach(function(s) {
        var iter = s.iteration || 1;
        if (!iterations[iter]) iterations[iter] = [];
        iterations[iter].push(s);
        if (iter > maxIter) maxIter = iter;
    });

    var selectedIter = App._cycleDetailIter || 0; // 0 = all

    var bc = content.querySelector('.breadcrumb-bar');
    var tempFrag = document.createDocumentFragment();
    if (bc) tempFrag.appendChild(bc);

    var html = '';

    // Header
    html += '<div class="cd-header">';
    html += '<button class="cd-back" id="cdBack">← Back to Cycles</button>';
    html += '<div class="cd-title-row">';
    html += '<span class="cd-icon">' + icon + '</span>';
    html += '<div class="cd-title-info">';
    html += '<h2 class="cd-title">' + esc(cycle.cycle_type.replace(/-/g, ' ')) + '</h2>';
    html += '<div class="cd-subtitle">';
    html += '<span class="clickable-feature cd-feature-link" data-feature-id="' + esc(cycle.feature_id) + '">' + esc(cycle.feature_id) + '</span>';
    html += ' · <span class="badge badge-' + cycle.status + '">' + cycle.status + '</span>';
    html += ' · Iteration ' + cycle.iteration;
    if (avgScore !== null) {
        var avgCls = App.scoreColorClass(avgScore);
        html += ' · <span class="score-badge ' + avgCls + '">★ ' + avgScore.toFixed(1) + ' avg</span>';
    }
    html += '</div></div></div>';

    // Progress bar
    html += '<div class="cd-progress-wrap">';
    html += '<div class="cd-progress-label">' + pct + '% complete (' + cycle.current_step + '/' + totalSteps + ' steps)</div>';
    html += '<div class="cycle-progress" style="height:6px"><div class="cycle-progress-fill" style="width:' + pct + '%"></div></div>';
    html += '</div>';
    html += '</div>';

    // Step timeline (horizontal stepper, enhanced)
    html += '<div class="cd-section"><h3 class="cd-section-title">Step Progression</h3>';
    html += '<div class="cd-stepper">';
    for (var si = 0; si < steps.length; si++) {
        var state = si < cycle.current_step ? 'done' : si === cycle.current_step ? 'active' : 'pending';
        var stepScores = scores.filter(function(sc) { return sc.step === si; });
        var bestScore = stepScores.length ? stepScores[stepScores.length - 1].score : null;
        var indicator = state === 'done' ? '✓' : (si + 1);

        html += '<div class="cd-step ' + state + '">';
        html += '<div class="cd-step-indicator">' + indicator + '</div>';
        html += '<div class="cd-step-body">';
        html += '<div class="cd-step-name">' + esc(steps[si].replace(/-/g, ' ')) + '</div>';
        html += '<div class="cd-step-status">';
        if (state === 'done') html += '<span class="cd-status-badge cd-status-done">Done</span>';
        else if (state === 'active') html += '<span class="cd-status-badge cd-status-active">Active</span>';
        else html += '<span class="cd-status-badge cd-status-pending">Pending</span>';
        html += '</div>';
        if (bestScore !== null) {
            var bCls = App.scoreColorClass(bestScore);
            html += '<span class="score-badge ' + bCls + '" style="font-size:0.72rem">' + bestScore.toFixed(1) + '</span>';
        }
        // Show all scores for this step
        if (stepScores.length) {
            html += '<div class="cd-step-scores">';
            stepScores.forEach(function(ss) {
                var sCls = App.scoreColorClass(ss.score);
                html += '<div class="cd-step-score-item">';
                html += '<span class="score-badge ' + sCls + '">' + ss.score.toFixed(1) + '</span>';
                html += '<span class="cd-step-score-iter">Iter ' + (ss.iteration || 1) + '</span>';
                if (ss.notes) html += '<span class="cd-step-score-notes">' + esc(ss.notes) + '</span>';
                html += '<span class="cd-step-score-time">' + fmtTime(ss.created_at) + '</span>';
                html += '</div>';
            });
            html += '</div>';
        }
        html += '</div></div>';
        if (si < steps.length - 1) html += '<div class="cd-step-connector ' + (si < cycle.current_step ? 'done' : '') + '"></div>';
    }
    html += '</div></div>';

    // Score History section
    if (scores.length) {
        html += '<div class="cd-section"><h3 class="cd-section-title">Score History</h3>';

        // Iteration tabs
        if (maxIter > 1) {
            html += '<div class="cd-iter-tabs" id="cdIterTabs">';
            html += '<button class="cd-iter-tab' + (selectedIter === 0 ? ' active' : '') + '" data-iter="0">All</button>';
            for (var it = 1; it <= maxIter; it++) {
                var itScores = iterations[it] || [];
                html += '<button class="cd-iter-tab' + (selectedIter === it ? ' active' : '') + '" data-iter="' + it + '">Iteration ' + it + ' <span class="cd-iter-count">' + itScores.length + '</span></button>';
            }
            html += '</div>';
        }

        // Score sparkline
        var filteredScores = selectedIter === 0 ? scores : (iterations[selectedIter] || []);
        if (filteredScores.length >= 2) {
            html += '<div class="cd-sparkline-wrap">';
            html += '<canvas id="cdSparklineCanvas" width="600" height="80"></canvas>';
            html += '</div>';
        }

        // Score cards
        html += '<div class="cd-score-grid">';
        filteredScores.forEach(function(s) {
            var stepName = (steps[s.step] || 'Step ' + s.step).replace(/-/g, ' ');
            var cls = App.scoreColorClass(s.score);
            html += '<div class="cd-score-card">';
            html += '<div class="cd-score-card-header">';
            html += '<span class="score-badge ' + cls + ' cd-score-big">' + s.score.toFixed(1) + '</span>';
            html += '<div class="cd-score-card-meta">';
            html += '<div class="cd-score-step">' + esc(stepName) + '</div>';
            html += '<div class="cd-score-time">' + fmtTime(s.created_at) + '</div>';
            if (maxIter > 1) html += '<div class="cd-score-iter">Iteration ' + (s.iteration || 1) + '</div>';
            html += '</div></div>';
            if (s.notes) html += '<div class="cd-score-notes">' + esc(s.notes) + '</div>';
            html += '</div>';
        });
        html += '</div></div>';
    }

    // Timestamps
    html += '<div class="cd-section cd-timestamps">';
    html += '<span>Created: ' + fmtTime(cycle.created_at) + '</span>';
    html += '<span>Updated: ' + fmtTime(cycle.updated_at) + '</span>';
    html += '</div>';

    content.innerHTML = html;
    if (tempFrag.children.length) content.insertBefore(tempFrag.children[0], content.firstChild);

    // Bind events
    App.bindCycleDetailEvents(data);
};

App.bindCycleDetailEvents = function(data) {
    var backBtn = document.getElementById('cdBack');
    if (backBtn) {
        backBtn.addEventListener('click', function() {
            App._activeCycleDetail = null;
            App._breadcrumbDetail = null;
            App._cycleDetailIter = 0;
            App.navigate('cycles');
        });
    }

    // Feature link
    document.querySelectorAll('.cd-feature-link').forEach(function(el) {
        el.addEventListener('click', function(e) {
            e.preventDefault();
            var fid = el.getAttribute('data-feature-id');
            if (fid) {
                App._navContext = { featureId: fid };
                App._breadcrumbDetail = fid;
                App.navigate('features');
            }
        });
    });

    // Iteration tabs
    var tabs = document.getElementById('cdIterTabs');
    if (tabs) {
        tabs.querySelectorAll('.cd-iter-tab').forEach(function(tab) {
            tab.addEventListener('click', function() {
                App._cycleDetailIter = parseInt(tab.getAttribute('data-iter'), 10);
                App.renderCycleDetailView(data);
            });
        });
    }

    // Draw sparkline
    App.drawCycleDetailSparkline(data);
};

App.drawCycleDetailSparkline = function(data) {
    var canvas = document.getElementById('cdSparklineCanvas');
    if (!canvas) return;

    var scores = data.scores || [];
    var selectedIter = App._cycleDetailIter || 0;
    if (selectedIter > 0) {
        scores = scores.filter(function(s) { return (s.iteration || 1) === selectedIter; });
    }
    if (scores.length < 2) return;

    var ctx = canvas.getContext('2d');
    var dpr = window.devicePixelRatio || 1;
    var container = canvas.parentElement;
    var w = container.clientWidth || 600;
    var h = 80;

    canvas.width = w * dpr;
    canvas.height = h * dpr;
    canvas.style.width = w + 'px';
    canvas.style.height = h + 'px';
    ctx.scale(dpr, dpr);

    var cs = getComputedStyle(document.documentElement);
    var textSec = cs.getPropertyValue('--text-secondary').trim() || '#8b949e';
    var font = cs.getPropertyValue('--font-sans').trim() || 'system-ui, sans-serif';

    var pad = { top: 8, right: 16, bottom: 16, left: 32 };
    var plotW = w - pad.left - pad.right;
    var plotH = h - pad.top - pad.bottom;

    // Y-axis: 0-10
    ctx.fillStyle = textSec;
    ctx.font = '9px ' + font;
    ctx.textAlign = 'right';
    ctx.textBaseline = 'middle';
    for (var v = 0; v <= 10; v += 5) {
        var gy = pad.top + plotH - (v / 10 * plotH);
        ctx.fillText(v.toString(), pad.left - 6, gy);
        ctx.strokeStyle = cs.getPropertyValue('--border').trim() || '#30363d';
        ctx.lineWidth = 0.5;
        ctx.setLineDash([2, 3]);
        ctx.beginPath();
        ctx.moveTo(pad.left, gy);
        ctx.lineTo(pad.left + plotW, gy);
        ctx.stroke();
    }
    ctx.setLineDash([]);

    var points = scores.map(function(s, i) {
        return {
            x: pad.left + (scores.length === 1 ? plotW / 2 : (i / (scores.length - 1)) * plotW),
            y: pad.top + plotH - (Math.min(s.score, 10) / 10 * plotH),
            score: s.score
        };
    });

    // Gradient fill
    ctx.beginPath();
    points.forEach(function(p, i) { if (i === 0) ctx.moveTo(p.x, p.y); else ctx.lineTo(p.x, p.y); });
    ctx.lineTo(points[points.length - 1].x, pad.top + plotH);
    ctx.lineTo(points[0].x, pad.top + plotH);
    ctx.closePath();
    var grad = ctx.createLinearGradient(0, pad.top, 0, pad.top + plotH);
    grad.addColorStop(0, 'rgba(88, 166, 255, 0.15)');
    grad.addColorStop(1, 'rgba(88, 166, 255, 0.01)');
    ctx.fillStyle = grad;
    ctx.fill();

    // Line
    ctx.beginPath();
    points.forEach(function(p, i) { if (i === 0) ctx.moveTo(p.x, p.y); else ctx.lineTo(p.x, p.y); });
    ctx.strokeStyle = cs.getPropertyValue('--accent').trim() || '#58a6ff';
    ctx.lineWidth = 2;
    ctx.stroke();

    // Dots with color coding
    points.forEach(function(p) {
        ctx.beginPath();
        ctx.arc(p.x, p.y, 4, 0, 2 * Math.PI);
        ctx.fillStyle = p.score >= 8 ? '#3fb950' : p.score >= 6 ? '#d29922' : '#f85149';
        ctx.fill();
        ctx.strokeStyle = cs.getPropertyValue('--bg-card').trim() || '#1c2128';
        ctx.lineWidth = 2;
        ctx.stroke();
    });
};

// Override cycle card clicks to open detail view
App.bindCycleCardClicks = function() {
    document.querySelectorAll('.cycle-card[data-cycle-id]').forEach(function(card) {
        card.addEventListener('click', function(e) {
            if (e.target.closest('.clickable-feature')) return;
            e.stopPropagation();
            var cid = parseInt(card.getAttribute('data-cycle-id'), 10);
            if (cid) App.showCycleDetail(cid);
        });
    });
};

// ── Threaded Discussions ──

App._discAvatarColors = [
    '#e06c75', '#61afef', '#c678dd', '#98c379', '#e5c07b',
    '#56b6c2', '#d19a66', '#be5046', '#7ec8e3', '#c9a0dc'
];

App._discAvatarColor = function(name) {
    var hash = 0;
    var s = (name || 'U').toLowerCase();
    for (var i = 0; i < s.length; i++) {
        hash = ((hash << 5) - hash) + s.charCodeAt(i);
        hash = hash & hash;
    }
    return App._discAvatarColors[Math.abs(hash) % App._discAvatarColors.length];
};

App._discInitials = function(name) {
    if (!name) return '?';
    var parts = name.trim().split(/\s+/);
    if (parts.length >= 2) return (parts[0][0] + parts[1][0]).toUpperCase();
    return name.substring(0, 2).toUpperCase();
};

App._discAvatar = function(name, size) {
    var sz = size || 36;
    var color = App._discAvatarColor(name);
    var initials = App._discInitials(name);
    return '<div class="disc-avatar" style="width:' + sz + 'px;height:' + sz + 'px;background:' + color + ';font-size:' + (sz * 0.38) + 'px">' + initials + '</div>';
};

App._discTimeAgo = function(iso) {
    if (!iso) return '';
    var now = Date.now();
    var then = new Date(iso).getTime();
    var diff = Math.max(0, now - then);
    var secs = Math.floor(diff / 1000);
    if (secs < 60) return 'just now';
    var mins = Math.floor(secs / 60);
    if (mins < 60) return mins + 'm ago';
    var hours = Math.floor(mins / 60);
    if (hours < 24) return hours + 'h ago';
    var days = Math.floor(hours / 24);
    if (days < 30) return days + 'd ago';
    var months = Math.floor(days / 30);
    if (months < 12) return months + 'mo ago';
    return Math.floor(months / 12) + 'y ago';
};

App._discRenderMarkdown = function(text) {
    if (!text) return '';
    var e = function(s) { if(!s) return ''; var d=document.createElement('div'); d.textContent=s; return d.innerHTML; };
    var lines = text.split('\n');
    var html = '';
    var inCode = false;
    for (var i = 0; i < lines.length; i++) {
        var line = lines[i];
        if (line.match(/^```/)) {
            if (inCode) { html += '</code></pre>'; inCode = false; }
            else { html += '<pre><code>'; inCode = true; }
            continue;
        }
        if (inCode) { html += e(line) + '\n'; continue; }
        var escaped = e(line);
        // Bold
        escaped = escaped.replace(/\*\*(.+?)\*\*/g, '<strong>$1</strong>');
        // Inline code
        escaped = escaped.replace(/`([^`]+)`/g, '<code>$1</code>');
        // Links
        escaped = escaped.replace(/\[([^\]]+)\]\((https?:\/\/[^)]+)\)/g, '<a href="$2" target="_blank" rel="noopener">$1</a>');
        if (escaped.trim() === '') html += '<p>&nbsp;</p>';
        else html += '<p>' + escaped + '</p>';
    }
    if (inCode) html += '</code></pre>';
    return html;
};

App.renderDiscussions = async function() {
    var discussions = await App.api('discussions');
    var features = [];
    try { features = await App.api('features'); } catch(ex) { /* ignore */ }

    App._discussionsData = discussions;
    App._discussionsFeatures = features;
    App._discViewMode = 'list';
    App._discDetailId = null;

    // Check if navigating to a specific discussion
    if (App._navContext && App._navContext.id) {
        App._discDetailId = parseInt(App._navContext.id, 10);
        App._discViewMode = 'detail';
        App._navContext = {};
    }

    if (App._discViewMode === 'detail' && App._discDetailId) {
        return App._renderDiscussionDetail(App._discDetailId);
    }
    return App._renderDiscussionList(discussions, features);
};

App._renderDiscussionList = function(discussions, features) {
    var esc = window.esc || function(s) { if(!s) return ''; var d=document.createElement('div'); d.textContent=s; return d.innerHTML; };

    var statusCounts = {};
    (discussions || []).forEach(function(d) { statusCounts[d.status || 'open'] = (statusCounts[d.status || 'open'] || 0) + 1; });

    var headerHtml = '<div class="page-header">' +
        '<div style="display:flex;align-items:center;justify-content:space-between;flex-wrap:wrap;gap:8px">' +
        '<div><h2 class="page-title">Discussions</h2>' +
        '<p class="page-subtitle">' + (discussions.length || 0) + ' discussion' + (discussions.length !== 1 ? 's' : '') + '</p></div>' +
        '<button class="disc-new-btn" id="discNewBtn">＋ New Discussion</button>' +
        '</div></div>';

    var statsHtml = '<div class="stats-grid" style="margin-bottom:16px">' +
        '<div class="stat-card stat-card--success"><div class="stat-card-info"><div class="stat-value">' + (statusCounts.open || 0) + '</div><div class="stat-label">Open</div></div><div class="stat-icon" aria-hidden="true">🟢</div></div>' +
        '<div class="stat-card stat-card--accent"><div class="stat-card-info"><div class="stat-value">' + (statusCounts.resolved || 0) + '</div><div class="stat-label">Resolved</div></div><div class="stat-icon" aria-hidden="true">🔵</div></div>' +
        '<div class="stat-card stat-card--purple"><div class="stat-card-info"><div class="stat-value">' + (statusCounts.merged || 0) + '</div><div class="stat-label">Merged</div></div><div class="stat-icon" aria-hidden="true">🟣</div></div>' +
        '<div class="stat-card"><div class="stat-card-info"><div class="stat-value">' + (statusCounts.closed || 0) + '</div><div class="stat-label">Closed</div></div><div class="stat-icon" aria-hidden="true">⚪</div></div>' +
        '</div>';

    // New discussion form (hidden by default)
    var featureOptions = '<option value="">None</option>';
    (features || []).forEach(function(f) {
        featureOptions += '<option value="' + esc(f.id) + '">' + esc(f.id) + ' — ' + esc(f.name) + '</option>';
    });

    var formHtml = '<div id="discNewForm" style="display:none;margin-bottom:16px">' +
        '<div class="disc-form">' +
        '<div class="disc-form-title">New Discussion</div>' +
        '<div class="disc-form-group"><label for="discNewTitle">Title</label><input type="text" id="discNewTitle" placeholder="Discussion title…"></div>' +
        '<div class="disc-form-group"><label for="discNewBody">Body</label><textarea id="discNewBody" rows="4" placeholder="Describe what you want to discuss…"></textarea>' +
        '<div class="disc-form-hint">Supports **bold**, `code`, and [links](url)</div></div>' +
        '<div class="disc-form-group"><label for="discNewFeature">Link to Feature</label><select id="discNewFeature">' + featureOptions + '</select></div>' +
        '<div class="disc-form-group"><label for="discNewAuthor">Author</label><input type="text" id="discNewAuthor" value="human"></div>' +
        '<div class="disc-form-actions"><button class="disc-form-submit" id="discNewSubmit">Create Discussion</button>' +
        '<button class="disc-form-cancel" id="discNewCancel">Cancel</button></div>' +
        '</div></div>';

    if (!discussions || !discussions.length) {
        return headerHtml + formHtml +
            '<div class="empty-state">' +
            '<div class="empty-state-icon">💬</div>' +
            '<div class="empty-state-text">No discussions yet</div>' +
            '<div class="empty-state-hint">Start a discussion to track proposals, decisions, and conversations.</div>' +
            '</div>';
    }

    var listHtml = '<div class="disc-list">';
    discussions.forEach(function(d) {
        var statusCls = 'disc-status-' + (d.status || 'open');
        var preview = (d.body || '').substring(0, 100);
        if ((d.body || '').length > 100) preview += '…';
        if (!preview) preview = 'No description';
        var featureTag = d.feature_id ? '<a class="disc-feature-tag disc-feature-nav" data-feature-id="' + esc(d.feature_id) + '">' + esc(d.feature_id) + '</a>' : '';

        listHtml += '<div class="disc-list-item" data-disc-id="' + d.id + '">' +
            App._discAvatar(d.author, 36) +
            '<div class="disc-list-content">' +
            '<div class="disc-list-title">' +
            '<span>' + esc(d.title) + '</span>' +
            '<span class="badge ' + statusCls + '">' + esc(d.status || 'open') + '</span>' +
            '</div>' +
            '<div class="disc-list-preview">' + esc(preview) + '</div>' +
            '<div class="disc-list-meta">' +
            '<span>' + esc(d.author || 'Unknown') + '</span>' +
            '<span class="disc-reply-badge">💬 ' + (d.comment_count || 0) + '</span>' +
            (featureTag ? featureTag : '') +
            '<span>' + App._discTimeAgo(d.created_at) + '</span>' +
            '</div>' +
            '</div></div>';
    });
    listHtml += '</div>';

    return headerHtml + statsHtml + formHtml + listHtml;
};

App._renderDiscussionDetail = async function(discId) {
    var esc = window.esc || function(s) { if(!s) return ''; var d=document.createElement('div'); d.textContent=s; return d.innerHTML; };
    var disc;
    try {
        disc = await App.api('discussions/' + discId);
    } catch(ex) {
        return '<div class="empty-state"><div class="empty-state-icon">❌</div><div class="empty-state-text">Discussion not found</div></div>';
    }

    App._discCurrentDiscussion = disc;
    App._breadcrumbDetail = disc.title;
    App.updateBreadcrumbs();

    var statusCls = 'disc-status-' + (disc.status || 'open');
    var featureTag = disc.feature_id ? '<a class="disc-feature-tag disc-feature-nav" data-feature-id="' + esc(disc.feature_id) + '">' + esc(disc.feature_id) + '</a>' : '';

    var headerHtml = '<button class="disc-back-btn" id="discBackBtn">← Back to Discussions</button>' +
        '<div class="disc-detail-header">' +
        '<div class="disc-detail-title">' +
        '<span>#' + disc.id + '</span> ' + esc(disc.title) +
        ' <span class="badge ' + statusCls + '">' + esc(disc.status || 'open') + '</span>' +
        '</div>' +
        '<div class="disc-detail-meta">' +
        App._discAvatar(disc.author, 24) +
        '<span>' + esc(disc.author) + '</span>' +
        '<span>·</span>' +
        '<span>' + App._discTimeAgo(disc.created_at) + '</span>' +
        (featureTag ? '<span>·</span>' + featureTag : '') +
        '<span>·</span>' +
        '<span class="disc-reply-badge">💬 ' + (disc.comment_count || 0) + ' replies</span>' +
        '</div></div>';

    // Body
    var bodyHtml = '';
    if (disc.body) {
        bodyHtml = '<div class="disc-detail-body">' + App._discRenderMarkdown(disc.body) + '</div>';
    }

    // Replies thread
    var threadHtml = '<div class="disc-thread">';
    threadHtml += '<div class="disc-thread-title">Replies (' + ((disc.comments || []).length) + ')</div>';
    if (!disc.comments || !disc.comments.length) {
        threadHtml += '<div style="color:var(--text-muted);font-size:0.85rem;padding:8px 0">No replies yet. Be the first to respond.</div>';
    } else {
        disc.comments.forEach(function(c) {
            var typeCls = '';
            var ctype = c.comment_type || c.type || 'comment';
            if (ctype !== 'comment') typeCls = '<span class="badge disc-type-' + ctype + '">' + esc(ctype) + '</span>';
            threadHtml += '<div class="disc-reply">' +
                '<div>' + App._discAvatar(c.author, 32).replace('disc-avatar', 'disc-reply-avatar') + '</div>' +
                '<div class="disc-reply-content">' +
                '<div class="disc-reply-header">' +
                '<span class="disc-reply-author">' + esc(c.author || 'Unknown') + '</span>' +
                typeCls +
                '<span class="disc-reply-time">' + App._discTimeAgo(c.created_at) + '</span>' +
                '</div>' +
                '<div class="disc-reply-body">' + App._discRenderMarkdown(c.content) + '</div>' +
                '</div></div>';
        });
    }
    threadHtml += '</div>';

    // Reply form
    var replyFormHtml = '<div class="disc-form" id="discReplyFormWrap">' +
        '<div class="disc-form-title">Reply</div>' +
        '<div class="disc-form-group"><textarea id="discReplyBody" rows="3" placeholder="Write a reply…"></textarea>' +
        '<div class="disc-form-hint">Supports **bold**, `code`, and [links](url)</div></div>' +
        '<div class="disc-form-group"><label for="discReplyAuthor">Author</label><input type="text" id="discReplyAuthor" value="human"></div>' +
        '<div class="disc-form-actions"><button class="disc-form-submit" id="discReplySubmit">Post Reply</button></div>' +
        '</div>';

    return headerHtml + bodyHtml + threadHtml + replyFormHtml;
};

App._bindDiscussionEvents = function() {
    // List view events
    var newBtn = document.getElementById('discNewBtn');
    var newForm = document.getElementById('discNewForm');
    var newCancel = document.getElementById('discNewCancel');
    var newSubmit = document.getElementById('discNewSubmit');

    if (newBtn && newForm) {
        newBtn.addEventListener('click', function() {
            newForm.style.display = newForm.style.display === 'none' ? 'block' : 'none';
            if (newForm.style.display === 'block') {
                var titleInput = document.getElementById('discNewTitle');
                if (titleInput) titleInput.focus();
            }
        });
    }
    if (newCancel && newForm) {
        newCancel.addEventListener('click', function() { newForm.style.display = 'none'; });
    }
    if (newSubmit) {
        newSubmit.addEventListener('click', async function() {
            var title = (document.getElementById('discNewTitle') || {}).value || '';
            var body = (document.getElementById('discNewBody') || {}).value || '';
            var featureId = (document.getElementById('discNewFeature') || {}).value || '';
            var author = (document.getElementById('discNewAuthor') || {}).value || 'human';
            if (!title.trim()) { App.toast('Title is required', 'error'); return; }
            newSubmit.disabled = true;
            try {
                await App.apiPost('discussions', { title: title.trim(), body: body, feature_id: featureId, author: author });
                App.toast('Discussion created', 'success');
                App._discViewMode = 'list';
                App._breadcrumbDetail = null;
                App.navigate('discussions');
            } catch(ex) {
                App.toast('Failed to create discussion', 'error');
            }
            newSubmit.disabled = false;
        });
    }

    // Click discussion to open detail
    document.querySelectorAll('.disc-list-item').forEach(function(item) {
        item.addEventListener('click', function(e) {
            if (e.target.closest('.disc-feature-nav')) return;
            var id = parseInt(item.getAttribute('data-disc-id'), 10);
            if (id) App.navigateTo('discussions', id);
        });
    });

    // Feature link navigation
    document.querySelectorAll('.disc-feature-nav').forEach(function(link) {
        link.addEventListener('click', function(e) {
            e.preventDefault();
            e.stopPropagation();
            App.navigateTo('features', link.getAttribute('data-feature-id'));
        });
    });

    // Back button
    var backBtn = document.getElementById('discBackBtn');
    if (backBtn) {
        backBtn.addEventListener('click', function() {
            App._breadcrumbDetail = null;
            App._discViewMode = 'list';
            App.navigate('discussions');
        });
    }

    // Reply submit
    var replySubmit = document.getElementById('discReplySubmit');
    if (replySubmit && App._discCurrentDiscussion) {
        replySubmit.addEventListener('click', async function() {
            var body = (document.getElementById('discReplyBody') || {}).value || '';
            var author = (document.getElementById('discReplyAuthor') || {}).value || 'human';
            if (!body.trim()) { App.toast('Reply body is required', 'error'); return; }
            var discId = App._discCurrentDiscussion.id;
            replySubmit.disabled = true;
            try {
                await App.apiPost('discussions/' + discId + '/replies', { body: body.trim(), author: author });
                App.toast('Reply posted', 'success');
                // Re-render detail to show new reply
                var content = document.getElementById('content');
                if (content) {
                    var html = await App._renderDiscussionDetail(discId);
                    content.innerHTML = html;
                    App.updateBreadcrumbs();
                    App._bindDiscussionEvents();
                    // Auto-scroll to reply form
                    var formWrap = document.getElementById('discReplyFormWrap');
                    if (formWrap) formWrap.scrollIntoView({ behavior: 'smooth', block: 'center' });
                }
            } catch(ex) {
                App.toast('Failed to post reply', 'error');
            }
            replySubmit.disabled = false;
        });
    }
};

// ── Roadmap Hero Banner with Progress Ring + Summary Dashboard ──
App.renderRoadmapHeroBanner = function(items, features, featuresByRoadmap, pris, priColors, priIcons, catCounts, catCls) {
    var esc = window.esc || function(s) { if(!s) return ''; var d = document.createElement('div'); d.textContent = s; return d.innerHTML; };
    var total = items.length;
    var sCounts = {};
    items.forEach(function(r) { sCounts[r.status] = (sCounts[r.status] || 0) + 1; });
    var done = sCounts['done'] || 0;
    var inProg = sCounts['in-progress'] || 0;
    var accepted = sCounts['accepted'] || 0;
    var proposed = sCounts['proposed'] || 0;
    var deferred = sCounts['deferred'] || 0;
    var pct = total > 0 ? Math.round((done / total) * 100) : 0;

    // Feature-level completion for linked features
    var totalLinked = 0;
    var doneLinked = 0;
    items.forEach(function(r) {
        var linked = featuresByRoadmap[r.id] || [];
        totalLinked += linked.length;
        linked.forEach(function(f) { if (f.status === 'done') doneLinked++; });
    });
    var featurePct = totalLinked > 0 ? Math.round((doneLinked / totalLinked) * 100) : 0;

    // SVG progress ring
    var radius = 42;
    var circumference = 2 * Math.PI * radius;
    var offset = circumference - (pct / 100) * circumference;
    var featureOffset = circumference - (featurePct / 100) * circumference;
    var progressRing = '<svg class="rm-progress-ring" width="120" height="120" viewBox="0 0 120 120">' +
        '<circle class="rm-progress-ring-bg" cx="60" cy="60" r="' + radius + '" />' +
        '<circle class="rm-progress-ring-feature" cx="60" cy="60" r="' + (radius - 8) + '" stroke-dasharray="' + (2 * Math.PI * (radius - 8)) + '" stroke-dashoffset="' + (2 * Math.PI * (radius - 8) - (featurePct / 100) * 2 * Math.PI * (radius - 8)) + '" />' +
        '<circle class="rm-progress-ring-fill" cx="60" cy="60" r="' + radius + '" stroke-dasharray="' + circumference + '" stroke-dashoffset="' + offset + '" />' +
        '<text class="rm-progress-ring-text" x="60" y="55" text-anchor="middle">' + pct + '%</text>' +
        '<text class="rm-progress-ring-subtext" x="60" y="72" text-anchor="middle">complete</text>' +
    '</svg>';

    // Status breakdown pills
    var statusItems = [
        { key: 'proposed', label: 'Proposed', count: proposed, cls: 'rm-st-proposed' },
        { key: 'accepted', label: 'Accepted', count: accepted, cls: 'rm-st-accepted' },
        { key: 'in-progress', label: 'In Progress', count: inProg, cls: 'rm-st-inprogress' },
        { key: 'done', label: 'Completed', count: done, cls: 'rm-st-done' },
        { key: 'deferred', label: 'Deferred', count: deferred, cls: 'rm-st-deferred' }
    ];
    var statusPills = statusItems.filter(function(s) { return s.count > 0; }).map(function(s) {
        return '<div class="rm-status-pill ' + s.cls + '">' +
            '<span class="rm-status-pill-count">' + s.count + '</span>' +
            '<span class="rm-status-pill-label">' + s.label + '</span>' +
        '</div>';
    }).join('');

    // Priority mini-bars
    var priBarItems = pris.filter(function(p) { return items.some(function(r) { return r.priority === p; }); }).map(function(p) {
        var count = items.filter(function(r) { return r.priority === p; }).length;
        var barPct = total > 0 ? ((count / total) * 100).toFixed(1) : 0;
        return '<div class="rm-pri-bar-row">' +
            '<span class="rm-pri-bar-label">' + (priIcons[p] || '') + ' ' + p.replace('-', ' ') + '</span>' +
            '<div class="rm-pri-bar-track"><div class="rm-pri-bar-fill rm-pri-' + p + '" style="width:' + barPct + '%"></div></div>' +
            '<span class="rm-pri-bar-count">' + count + '</span>' +
        '</div>';
    }).join('');

    // Category chips
    var catEntries = Object.entries(catCounts).sort(function(a, b) { return b[1] - a[1]; });
    var catChips = catEntries.map(function(entry) {
        var cat = entry[0];
        var count = entry[1];
        return '<span class="rm-cat-chip ' + catCls(cat) + '">' + esc(cat) + ' <span class="rm-cat-chip-count">' + count + '</span></span>';
    }).join('');

    return '<div class="rm-hero-banner">' +
        '<div class="rm-hero-left">' +
            progressRing +
            '<div class="rm-hero-meta">' +
                '<div class="rm-hero-total"><span class="rm-hero-total-num">' + total + '</span> roadmap items</div>' +
                (totalLinked > 0 ? '<div class="rm-hero-features">' + doneLinked + '/' + totalLinked + ' linked features done (' + featurePct + '%)</div>' : '') +
            '</div>' +
        '</div>' +
        '<div class="rm-hero-center">' +
            '<div class="rm-hero-section-title">Status</div>' +
            '<div class="rm-status-pills">' + statusPills + '</div>' +
            '<div class="rm-hero-section-title" style="margin-top:12px">Categories</div>' +
            '<div class="rm-cat-chips">' + catChips + '</div>' +
        '</div>' +
        '<div class="rm-hero-right">' +
            '<div class="rm-hero-section-title">Priority Breakdown</div>' +
            '<div class="rm-pri-bars">' + priBarItems + '</div>' +
        '</div>' +
    '</div>';
};

// ── Roadmap Timeline View ──
App.renderRoadmapTimelineView = function(items, featuresByRoadmap, pris, priColors, catCls) {
    var esc = window.esc || function(s) { if(!s) return ''; var d = document.createElement('div'); d.textContent = s; return d.innerHTML; };
    if (!items || !items.length) return '<div class="rm-timeline-empty">No items to display</div>';

    var priColorMap = { critical: '#f85149', high: '#d29922', medium: '#58a6ff', low: '#3fb950', 'nice-to-have': '#bc8cff' };
    var statusFill = { proposed: 0.1, accepted: 0.25, 'in-progress': 0.6, done: 1.0, deferred: 0.05 };
    var statusLabels = { proposed: 'Proposed', accepted: 'Accepted', 'in-progress': 'In Progress', done: 'Completed', deferred: 'Deferred' };

    // Group by category
    var catGrouped = {};
    items.forEach(function(r) {
        var c = r.category || 'uncategorized';
        if (!catGrouped[c]) catGrouped[c] = [];
        catGrouped[c].push(r);
    });
    var catKeys = Object.keys(catGrouped).sort();

    // Build timeline lanes
    var lanes = catKeys.map(function(cat) {
        var catItems = catGrouped[cat];
        var blocks = catItems.map(function(r, idx) {
            var color = priColorMap[r.priority] || '#58a6ff';
            var fill = statusFill[r.status] || 0.1;
            var linked = featuresByRoadmap[r.id] || [];
            var linkedDone = linked.filter(function(f) { return f.status === 'done'; }).length;
            var linkedTotal = linked.length;
            var linkedPct = linkedTotal > 0 ? Math.round((linkedDone / linkedTotal) * 100) : 0;
            var effortWidths = { xs: 1, s: 1.5, m: 2, l: 3, xl: 4 };
            var w = effortWidths[r.effort] || 2;

            return '<div class="rm-tl-block" data-roadmap-id="' + esc(r.id) + '" style="--block-color:' + color + ';--block-width:' + w + ';--fill-pct:' + (fill * 100) + '%" title="' + esc(r.title) + ' (' + (statusLabels[r.status] || r.status) + ')&#10;Priority: ' + r.priority + (r.effort ? '&#10;Effort: ' + r.effort.toUpperCase() : '') + (linkedTotal > 0 ? '&#10;Features: ' + linkedDone + '/' + linkedTotal + ' done' : '') + '">' +
                '<div class="rm-tl-block-fill"></div>' +
                '<div class="rm-tl-block-label">' + esc(r.title) + '</div>' +
                (linkedTotal > 0 ? '<div class="rm-tl-block-features">' + linkedDone + '/' + linkedTotal + '</div>' : '') +
            '</div>';
        }).join('');

        return '<div class="rm-tl-lane">' +
            '<div class="rm-tl-lane-label ' + catCls(cat) + '">' + esc(cat) + '</div>' +
            '<div class="rm-tl-lane-track">' + blocks + '</div>' +
        '</div>';
    }).join('');

    // Legend
    var legendItems = pris.map(function(p) {
        var color = priColorMap[p] || '#58a6ff';
        return '<span class="rm-tl-legend-item"><span class="rm-tl-legend-dot" style="background:' + color + '"></span>' + p.replace('-', ' ') + '</span>';
    }).join('');

    var statusLegend = Object.keys(statusFill).map(function(s) {
        var fill = statusFill[s];
        return '<span class="rm-tl-legend-item"><span class="rm-tl-legend-bar"><span class="rm-tl-legend-bar-fill" style="width:' + (fill * 100) + '%"></span></span>' + (statusLabels[s] || s) + '</span>';
    }).join('');

    return '<div class="rm-timeline">' +
        '<div class="rm-tl-header">' +
            '<div class="rm-tl-title">Timeline View</div>' +
            '<div class="rm-tl-subtitle">Items sized by effort, colored by priority, filled by status</div>' +
        '</div>' +
        '<div class="rm-tl-legends">' +
            '<div class="rm-tl-legend-group"><span class="rm-tl-legend-label">Priority:</span>' + legendItems + '</div>' +
            '<div class="rm-tl-legend-group"><span class="rm-tl-legend-label">Status:</span>' + statusLegend + '</div>' +
        '</div>' +
        '<div class="rm-tl-lanes">' + lanes + '</div>' +
    '</div>';
};

// ── Roadmap item card progress calculation ──
App.getRoadmapItemProgress = function(roadmapId, featuresByRoadmap) {
    var linked = featuresByRoadmap[roadmapId] || [];
    if (!linked.length) return null;
    var done = linked.filter(function(f) { return f.status === 'done'; }).length;
    return { done: done, total: linked.length, pct: Math.round((done / linked.length) * 100) };
};

// ── Features Page Override (moved here to avoid V8 truncation in app.js object literal) ──

App.renderFeatures = async function() {
    var results = await Promise.all([
        App.api('features'),
        App.api('roadmap').catch(function() { return []; }),
    ]);
    var features = results[0];
    var roadmapItems = results[1];
    App._featuresData = features;
    App._roadmapData = roadmapItems;
    App._featuresFilter = 'all';
    App._featuresSearch = '';

    if (!features.length) return '<div class="page-header"><h2 class="page-title">Features</h2><p class="page-subtitle">Track your project\'s features through their lifecycle</p></div>'
        + '<div class="empty-state"><div class="empty-state-icon">✨</div><div class="empty-state-text">No features yet</div>'
        + '<div class="empty-state-hint">Features are the building blocks of your project. Add one to get started!</div>'
        + '<div class="empty-state-cta"><span class="cta-icon">$</span> lifecycle feature add &lt;name&gt;</div></div>';

    var statuses = ['all','draft','planning','implementing','agent-qa','human-qa','done','blocked'];
    var counts = {};
    counts.all = features.length;
    features.forEach(function(f) { counts[f.status] = (counts[f.status] || 0) + 1; });
    var pills = statuses.filter(function(s) { return s === 'all' || counts[s]; }).map(function(s) {
        return '<button class="filter-pill' + (s === 'all' ? ' active' : '') + '" data-status="' + s + '">' + (s === 'all' ? 'All' : s) + '<span class="pill-count">' + (counts[s] || 0) + '</span></button>';
    }).join('');

    return '<div class="page-header"><h2 class="page-title">Features</h2><p class="page-subtitle">' + features.length + ' features tracked</p></div>'
        + '<div class="features-toolbar">'
        + '  <div class="filter-pills">' + pills + '</div>'
        + '  <div class="features-toolbar-right">'
        + '    <div class="features-view-toggle" id="featuresViewToggle">'
        + '      <button class="view-toggle-btn active" data-view="list" title="List View">☰</button>'
        + '      <button class="view-toggle-btn" data-view="graph" title="Dependency Graph">◈</button>'
        + '    </div>'
        + '    <div class="features-search-wrap"><input type="text" class="features-search" placeholder="Search features…" id="featuresSearch" aria-label="Search features"></div>'
        + '  </div>'
        + '</div>'
        + '<div class="card" id="featuresTableWrap">' + App.buildFeaturesTable(features) + '</div>'
        + '<div id="featuresGraphWrap" class="features-graph-wrap" style="display:none"></div>';
};

// ── Features Dependency Graph ──

App.renderFeaturesDepGraph = function(container, features) {
    App.api('dependencies').then(function(data) {
        if (!data || !data.nodes || !data.nodes.length) {
            container.innerHTML = '<div class="empty-state"><div class="empty-state-icon">🔗</div><div class="empty-state-text">No features to graph</div></div>';
            return;
        }
        var nodes = data.nodes;
        var edges = data.edges || [];
        var edgeCount = edges.length;
        var rootCount = 0;
        var leafCount = 0;
        var hasIncoming = {};
        var hasOutgoing = {};
        edges.forEach(function(e) { hasIncoming[e.to] = true; hasOutgoing[e.from] = true; });
        nodes.forEach(function(n) {
            if (!hasOutgoing[n.id]) rootCount++;
            if (!hasIncoming[n.id]) leafCount++;
        });

        var html = '<div class="depgraph-stats">';
        html += '<span class="depgraph-stat"><strong>' + nodes.length + '</strong> features</span>';
        html += '<span class="depgraph-stat"><strong>' + edgeCount + '</strong> dependencies</span>';
        html += '<span class="depgraph-stat"><strong>' + rootCount + '</strong> roots</span>';
        html += '<span class="depgraph-stat"><strong>' + leafCount + '</strong> leaves</span>';
        html += '</div>';
        html += '<div class="depgraph-legend">';
        html += '<span class="depgraph-legend-item"><span class="depgraph-legend-dot" style="background:#8b949e"></span>Draft</span>';
        html += '<span class="depgraph-legend-item"><span class="depgraph-legend-dot" style="background:#d29922"></span>Planning</span>';
        html += '<span class="depgraph-legend-item"><span class="depgraph-legend-dot" style="background:#58a6ff"></span>Implementing</span>';
        html += '<span class="depgraph-legend-item"><span class="depgraph-legend-dot" style="background:#3fb950"></span>Done</span>';
        html += '<span class="depgraph-legend-item"><span class="depgraph-legend-dot" style="background:#f85149"></span>Blocked</span>';
        html += '</div>';
        html += '<div class="depgraph-canvas-wrap"><canvas id="featuresDepCanvas"></canvas></div>';
        container.innerHTML = html;

        var canvas = document.getElementById('featuresDepCanvas');
        if (canvas) App._drawFeaturesDepGraph(canvas, data);
    }).catch(function() {
        container.innerHTML = '<div class="empty-state"><div class="empty-state-icon">⚠️</div><div class="empty-state-text">Could not load dependency data</div></div>';
    });
};

App._drawFeaturesDepGraph = function(canvas, data) {
    var nodes = data.nodes;
    var edges = data.edges || [];
    if (!nodes.length) return;

    var STATUS_COLORS = {
        done: '#3fb950', implementing: '#58a6ff', 'agent-qa': '#58a6ff',
        'human-qa': '#f0883e', draft: '#8b949e', planning: '#d29922',
        blocked: '#f85149'
    };

    var nodeMap = {};
    nodes.forEach(function(n) { nodeMap[n.id] = n; });

    // Build adjacency for topological layering (from → to means from depends on to)
    var depsOf = {};
    edges.forEach(function(e) {
        if (!depsOf[e.from]) depsOf[e.from] = [];
        depsOf[e.from].push(e.to);
    });

    // Compute layers via longest-path from roots (left-to-right)
    var layers = {};
    var getLayer = function(id, visited) {
        if (layers[id] !== undefined) return layers[id];
        if (visited[id]) return 0;
        visited[id] = true;
        var deps = depsOf[id];
        if (!deps || !deps.length) { layers[id] = 0; return 0; }
        var mx = 0;
        deps.forEach(function(d) {
            if (nodeMap[d]) mx = Math.max(mx, getLayer(d, visited) + 1);
        });
        layers[id] = mx;
        return mx;
    };
    nodes.forEach(function(n) { getLayer(n.id, {}); });

    var maxLayer = Math.max(0, Math.max.apply(null, Object.values(layers).concat([0])));
    var columns = [];
    for (var l = 0; l <= maxLayer; l++) {
        columns.push(nodes.filter(function(n) { return (layers[n.id] || 0) === l; }));
    }

    // Layout
    var nodeW = 140;
    var nodeH = 48;
    var colGap = 60;
    var rowGap = 20;
    var colWidth = nodeW + colGap;
    var maxColLen = Math.max.apply(null, columns.map(function(c) { return c.length; }).concat([1]));
    var graphW = colWidth * columns.length + colGap;
    var graphH = maxColLen * (nodeH + rowGap) + 80;

    var positions = {};
    columns.forEach(function(col, ci) {
        var cx = colGap / 2 + colWidth * ci + nodeW / 2;
        var totalH = col.length * (nodeH + rowGap) - rowGap;
        var startY = (graphH - totalH) / 2;
        col.forEach(function(node, ni) {
            positions[node.id] = {
                x: cx - nodeW / 2, y: startY + ni * (nodeH + rowGap),
                cx: cx, cy: startY + ni * (nodeH + rowGap) + nodeH / 2,
                node: node
            };
        });
    });

    // Canvas setup
    var dpr = window.devicePixelRatio || 1;
    var wrap = canvas.parentElement;
    var canvasDisplayW = Math.max(wrap.clientWidth, graphW);
    var canvasDisplayH = Math.max(400, graphH);

    // State for pan/zoom
    var state = {
        scale: 1,
        offsetX: 0,
        offsetY: 0,
        dragging: false,
        dragStartX: 0,
        dragStartY: 0,
        hoveredId: null
    };

    // Fit graph to view if it's wider than container
    if (graphW > wrap.clientWidth) {
        state.scale = Math.max(0.4, wrap.clientWidth / graphW);
    }

    var toWorld = function(cx, cy) {
        var rect = canvas.getBoundingClientRect();
        var sx = (cx - rect.left) / state.scale - state.offsetX / state.scale;
        var sy = (cy - rect.top) / state.scale - state.offsetY / state.scale;
        return { x: sx, y: sy };
    };

    var hitTest = function(wx, wy) {
        for (var id in positions) {
            var p = positions[id];
            if (wx >= p.x && wx <= p.x + nodeW && wy >= p.y && wy <= p.y + nodeH) return id;
        }
        return null;
    };

    var connectedTo = function(id) {
        var set = {};
        set[id] = true;
        edges.forEach(function(e) {
            if (e.from === id) set[e.to] = true;
            if (e.to === id) set[e.from] = true;
        });
        return set;
    };

    var draw = function() {
        canvas.width = canvasDisplayW * dpr;
        canvas.height = canvasDisplayH * dpr;
        canvas.style.width = canvasDisplayW + 'px';
        canvas.style.height = canvasDisplayH + 'px';
        var ctx = canvas.getContext('2d');
        ctx.setTransform(dpr, 0, 0, dpr, 0, 0);
        ctx.clearRect(0, 0, canvasDisplayW, canvasDisplayH);
        ctx.save();
        ctx.translate(state.offsetX, state.offsetY);
        ctx.scale(state.scale, state.scale);

        var cs = getComputedStyle(document.documentElement);
        var font = cs.getPropertyValue('--font-sans').trim() || 'system-ui, sans-serif';
        var bgCard = cs.getPropertyValue('--bg-card').trim() || '#1c2128';
        var borderColor = cs.getPropertyValue('--border').trim() || '#30363d';

        var highlighted = state.hoveredId ? connectedTo(state.hoveredId) : null;

        // Draw edges (bezier curves with arrows)
        edges.forEach(function(e) {
            var from = positions[e.to];   // depends_on target is drawn on left
            var to = positions[e.from];   // the feature that depends is drawn on right
            if (!from || !to) return;
            var color = STATUS_COLORS[(nodeMap[e.to] || {}).status] || '#8b949e';
            var dimmed = highlighted && !highlighted[e.from] && !highlighted[e.to];

            ctx.beginPath();
            ctx.strokeStyle = color;
            ctx.lineWidth = 2;
            ctx.globalAlpha = dimmed ? 0.1 : 0.5;
            var sx = from.x + nodeW;
            var sy = from.cy;
            var ex = to.x;
            var ey = to.cy;
            var cpOffset = Math.min(Math.abs(ex - sx) * 0.4, 60);
            ctx.moveTo(sx, sy);
            ctx.bezierCurveTo(sx + cpOffset, sy, ex - cpOffset, ey, ex, ey);
            ctx.stroke();

            // Arrowhead
            ctx.globalAlpha = dimmed ? 0.1 : 0.7;
            ctx.fillStyle = color;
            ctx.beginPath();
            ctx.moveTo(ex, ey);
            ctx.lineTo(ex - 7, ey - 4);
            ctx.lineTo(ex - 7, ey + 4);
            ctx.closePath();
            ctx.fill();
        });

        // Draw nodes
        ctx.globalAlpha = 1;
        Object.keys(positions).forEach(function(id) {
            var p = positions[id];
            var n = p.node;
            var color = STATUS_COLORS[n.status] || '#8b949e';
            var isHovered = state.hoveredId === id;
            var dimmed = highlighted && !highlighted[id];

            ctx.globalAlpha = dimmed ? 0.15 : 1;

            // Node shadow for hovered
            if (isHovered) {
                ctx.shadowColor = color;
                ctx.shadowBlur = 12;
            }

            // Rounded rectangle background
            ctx.fillStyle = bgCard;
            ctx.strokeStyle = isHovered ? color : borderColor;
            ctx.lineWidth = isHovered ? 2.5 : 1;
            var r = 8;
            ctx.beginPath();
            ctx.moveTo(p.x + r, p.y);
            ctx.lineTo(p.x + nodeW - r, p.y);
            ctx.quadraticCurveTo(p.x + nodeW, p.y, p.x + nodeW, p.y + r);
            ctx.lineTo(p.x + nodeW, p.y + nodeH - r);
            ctx.quadraticCurveTo(p.x + nodeW, p.y + nodeH, p.x + nodeW - r, p.y + nodeH);
            ctx.lineTo(p.x + r, p.y + nodeH);
            ctx.quadraticCurveTo(p.x, p.y + nodeH, p.x, p.y + nodeH - r);
            ctx.lineTo(p.x, p.y + r);
            ctx.quadraticCurveTo(p.x, p.y, p.x + r, p.y);
            ctx.fill();
            ctx.stroke();
            ctx.shadowColor = 'transparent';
            ctx.shadowBlur = 0;

            // Status bar on left edge
            ctx.fillStyle = color;
            ctx.beginPath();
            ctx.moveTo(p.x + r, p.y);
            ctx.lineTo(p.x + 4, p.y);
            ctx.quadraticCurveTo(p.x, p.y, p.x, p.y + r);
            ctx.lineTo(p.x, p.y + nodeH - r);
            ctx.quadraticCurveTo(p.x, p.y + nodeH, p.x + 4, p.y + nodeH);
            ctx.lineTo(p.x + r, p.y + nodeH);
            ctx.closePath();
            ctx.fill();

            // Feature name
            ctx.fillStyle = cs.getPropertyValue('--text-primary').trim() || '#e6edf3';
            ctx.font = 'bold 12px ' + font;
            ctx.textAlign = 'left';
            ctx.textBaseline = 'middle';
            var label = n.name.length > 16 ? n.name.substring(0, 15) + '\u2026' : n.name;
            ctx.fillText(label, p.x + 12, p.cy - 8);

            // Status badge
            ctx.font = '10px ' + font;
            ctx.fillStyle = color;
            ctx.fillText(n.status, p.x + 12, p.cy + 8);
        });

        ctx.restore();
    };

    draw();

    // Mouse move → hover
    canvas.addEventListener('mousemove', function(evt) {
        if (state.dragging) {
            var dx = evt.clientX - state.dragStartX;
            var dy = evt.clientY - state.dragStartY;
            state.offsetX += dx;
            state.offsetY += dy;
            state.dragStartX = evt.clientX;
            state.dragStartY = evt.clientY;
            draw();
            return;
        }
        var w = toWorld(evt.clientX, evt.clientY);
        var hit = hitTest(w.x, w.y);
        if (hit !== state.hoveredId) {
            state.hoveredId = hit;
            canvas.style.cursor = hit ? 'pointer' : 'grab';
            draw();
        }
    });

    // Click → navigate
    canvas.addEventListener('click', function(evt) {
        var w = toWorld(evt.clientX, evt.clientY);
        var hit = hitTest(w.x, w.y);
        if (hit) {
            App._expandedFeatureId = hit;
            App.navigate('features', { id: hit });
        }
    });

    // Wheel → zoom
    canvas.addEventListener('wheel', function(evt) {
        evt.preventDefault();
        var rect = canvas.getBoundingClientRect();
        var mx = evt.clientX - rect.left;
        var my = evt.clientY - rect.top;
        var delta = evt.deltaY > 0 ? 0.9 : 1.1;
        var newScale = Math.min(3, Math.max(0.2, state.scale * delta));
        var ratio = newScale / state.scale;
        state.offsetX = mx - (mx - state.offsetX) * ratio;
        state.offsetY = my - (my - state.offsetY) * ratio;
        state.scale = newScale;
        draw();
    }, { passive: false });

    // Drag to pan
    canvas.addEventListener('mousedown', function(evt) {
        var w = toWorld(evt.clientX, evt.clientY);
        if (!hitTest(w.x, w.y)) {
            state.dragging = true;
            state.dragStartX = evt.clientX;
            state.dragStartY = evt.clientY;
            canvas.style.cursor = 'grabbing';
        }
    });
    window.addEventListener('mouseup', function() {
        if (state.dragging) {
            state.dragging = false;
            canvas.style.cursor = state.hoveredId ? 'pointer' : 'grab';
        }
    });

    canvas.style.cursor = 'grab';
};

