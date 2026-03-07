App.drawScoreTrendChart = function(canvasId, scores) {
    var canvas = document.getElementById(canvasId);
    if (!canvas) return;
    var ctx = canvas.getContext('2d');
    var container = canvas.parentElement;
    var dpr = window.devicePixelRatio || 1;
    var w = container.clientWidth || 400;
    var h = container.clientHeight || 260;

    canvas.width = w * dpr;
    canvas.height = h * dpr;
    canvas.style.width = w + 'px';
    canvas.style.height = h + 'px';
    ctx.scale(dpr, dpr);

    var cs = getComputedStyle(document.documentElement);
    var font = cs.getPropertyValue('--font-sans').trim() || 'system-ui, sans-serif';
    var textSec = cs.getPropertyValue('--text-secondary').trim() || '#8b949e';
    var border = cs.getPropertyValue('--border').trim() || '#30363d';
    var bgCard = cs.getPropertyValue('--bg-card').trim() || '#1c2128';

    if (!scores || scores.length === 0) {
        ctx.fillStyle = textSec;
        ctx.font = '14px ' + font;
        ctx.textAlign = 'center';
        ctx.textBaseline = 'middle';
        ctx.fillText('No scores yet', w / 2, h / 2);
        return;
    }

    var cycleColors = {
        'feature-implementation':'#3b82f6','ui-refinement':'#8b5cf6','bug-triage':'#ef4444',
        'documentation':'#10b981','architecture-review':'#f59e0b','release':'#ec4899',
        'roadmap-planning':'#14b8a6','onboarding-dx':'#6366f1'
    };

    var pad = { top: 24, right: 24, bottom: 44, left: 44 };
    var plotW = w - pad.left - pad.right;
    var plotH = h - pad.top - pad.bottom;
    var maxY = 10;

    // Grid lines at 2, 4, 6, 8, 10
    ctx.strokeStyle = border;
    ctx.lineWidth = 0.5;
    ctx.setLineDash([4, 4]);
    for (var gl = 2; gl <= 10; gl += 2) {
        var gy = pad.top + plotH - (gl / maxY * plotH);
        ctx.beginPath();
        ctx.moveTo(pad.left, gy);
        ctx.lineTo(pad.left + plotW, gy);
        ctx.stroke();
        ctx.fillStyle = textSec;
        ctx.font = '10px ' + font;
        ctx.textAlign = 'right';
        ctx.textBaseline = 'middle';
        ctx.fillText(gl.toString(), pad.left - 8, gy);
    }
    ctx.setLineDash([]);

    // Calculate point positions
    var points = scores.map(function(s, i) {
        var x = pad.left + (scores.length === 1 ? plotW / 2 : (i / (scores.length - 1)) * plotW);
        var y = pad.top + plotH - (Math.min(s.score, maxY) / maxY * plotH);
        return { x: x, y: y, score: s.score, cycle: s.cycle || '', date: s.date || '' };
    });

    // X-axis date labels
    var step = Math.max(1, Math.floor(scores.length / 6));
    ctx.textAlign = 'center';
    ctx.textBaseline = 'top';
    points.forEach(function(p, i) {
        if (i % step === 0 || i === scores.length - 1) {
            ctx.fillStyle = textSec;
            ctx.font = '9px ' + font;
            ctx.fillText(p.date.length >= 5 ? p.date.substring(5) : p.date, p.x, pad.top + plotH + 8);
        }
    });

    // Gradient fill under line
    ctx.beginPath();
    points.forEach(function(p, i) {
        if (i === 0) ctx.moveTo(p.x, p.y);
        else ctx.lineTo(p.x, p.y);
    });
    ctx.lineTo(points[points.length - 1].x, pad.top + plotH);
    ctx.lineTo(points[0].x, pad.top + plotH);
    ctx.closePath();
    var grad = ctx.createLinearGradient(0, pad.top, 0, pad.top + plotH);
    grad.addColorStop(0, 'rgba(59, 130, 246, 0.15)');
    grad.addColorStop(1, 'rgba(59, 130, 246, 0.01)');
    ctx.fillStyle = grad;
    ctx.fill();

    // Line segments color-coded by cycle type
    for (var li = 1; li < points.length; li++) {
        ctx.beginPath();
        ctx.moveTo(points[li - 1].x, points[li - 1].y);
        ctx.lineTo(points[li].x, points[li].y);
        ctx.strokeStyle = cycleColors[points[li].cycle] || '#3b82f6';
        ctx.lineWidth = 2.5;
        ctx.stroke();
    }

    // Data point dots
    points.forEach(function(p) {
        ctx.beginPath();
        ctx.arc(p.x, p.y, 4, 0, 2 * Math.PI);
        ctx.fillStyle = cycleColors[p.cycle] || '#3b82f6';
        ctx.fill();
        ctx.strokeStyle = bgCard;
        ctx.lineWidth = 2;
        ctx.stroke();
    });

    // Store points for tooltip
    canvas._chartPoints = points;
    canvas._chartCycleColors = cycleColors;

    // Bind tooltip events (once)
    if (!canvas._tooltipBound) {
        canvas._tooltipBound = true;
        var tooltip = document.getElementById(canvasId + 'Tooltip');
        canvas.addEventListener('mousemove', function(e) {
            if (!tooltip || !canvas._chartPoints) return;
            var rect = canvas.getBoundingClientRect();
            var mx = e.clientX - rect.left;
            var my = e.clientY - rect.top;
            var closest = null, minDist = 30;
            canvas._chartPoints.forEach(function(p) {
                var d = Math.sqrt((p.x - mx) * (p.x - mx) + (p.y - my) * (p.y - my));
                if (d < minDist) { minDist = d; closest = p; }
            });
            if (closest) {
                var color = canvas._chartCycleColors[closest.cycle] || '#3b82f6';
                tooltip.style.display = 'block';
                // Position tooltip, keeping it within container
                var tLeft = closest.x + 12;
                var containerW = canvas.clientWidth;
                if (tLeft + 140 > containerW) tLeft = closest.x - 150;
                tooltip.style.left = tLeft + 'px';
                tooltip.style.top = (closest.y - 10) + 'px';
                tooltip.innerHTML = '<strong style="color:' + color + '">' + closest.score.toFixed(1) + '</strong>'
                    + '<br><span>' + closest.cycle.replace(/-/g, ' ') + '</span>'
                    + '<br><span style="opacity:0.7">' + closest.date + '</span>';
            } else {
                tooltip.style.display = 'none';
            }
        });
        canvas.addEventListener('mouseleave', function() {
            if (tooltip) tooltip.style.display = 'none';
        });
    }
};

App.drawCycleTypeChart = function(canvasId, scores) {
    var canvas = document.getElementById(canvasId);
    if (!canvas) return;
    var ctx = canvas.getContext('2d');
    var dpr = window.devicePixelRatio || 1;
    var size = Math.min(canvas.parentElement.clientWidth, 200);
    canvas.width = size * dpr;
    canvas.height = size * dpr;
    canvas.style.width = size + 'px';
    canvas.style.height = size + 'px';
    ctx.scale(dpr, dpr);

    var cs = getComputedStyle(document.documentElement);
    var font = cs.getPropertyValue('--font-sans').trim() || 'system-ui, sans-serif';
    var textSec = cs.getPropertyValue('--text-secondary').trim() || '#8b949e';
    var textPri = cs.getPropertyValue('--text-primary').trim() || '#e6edf3';

    var counts = {};
    (scores || []).forEach(function(s) { counts[s.cycle] = (counts[s.cycle] || 0) + 1; });
    var entries = Object.entries(counts).sort(function(a, b) { return b[1] - a[1]; });
    var total = entries.reduce(function(a, e) { return a + e[1]; }, 0);

    if (total === 0) {
        ctx.fillStyle = textSec;
        ctx.font = '14px ' + font;
        ctx.textAlign = 'center';
        ctx.textBaseline = 'middle';
        ctx.fillText('No data', size / 2, size / 2);
        return;
    }

    var cycleColors = {
        'feature-implementation':'#3b82f6','ui-refinement':'#8b5cf6','bug-triage':'#ef4444',
        'documentation':'#10b981','architecture-review':'#f59e0b','release':'#ec4899',
        'roadmap-planning':'#14b8a6','onboarding-dx':'#6366f1'
    };

    var cx = size / 2, cy = size / 2;
    var r = Math.min(cx, cy) - 8;
    var inner = r * 0.55;
    var angle = -Math.PI / 2;

    entries.forEach(function(e) {
        var sweep = (e[1] / total) * 2 * Math.PI;
        ctx.beginPath();
        ctx.moveTo(cx + inner * Math.cos(angle), cy + inner * Math.sin(angle));
        ctx.arc(cx, cy, r, angle, angle + sweep);
        ctx.arc(cx, cy, inner, angle + sweep, angle, true);
        ctx.closePath();
        ctx.fillStyle = cycleColors[e[0]] || '#484f58';
        ctx.fill();
        angle += sweep;
    });

    // Center text
    ctx.fillStyle = textPri;
    ctx.font = 'bold 20px ' + font;
    ctx.textAlign = 'center';
    ctx.textBaseline = 'middle';
    ctx.fillText(total, cx, cy - 5);
    ctx.fillStyle = textSec;
    ctx.font = '9px ' + font;
    ctx.fillText('SCORES', cx, cy + 11);
};

App.drawStatsCharts = function() {
    var data = App._statsData;
    if (!data) return;
    var cs = data.cycle_stats || {};
    var scores = cs.scores_over_time || [];
    App.drawScoreTrendChart('scoreTrendCanvas', scores);
    App.drawCycleTypeChart('cycleTypeCanvas', scores);
};

App._bindStatsEvents = function() {
    App.animateProgressBars(document.getElementById('content'));
};


// ── Keyboard Shortcuts ──
App.initKeyboardShortcuts = function() {
    var chordActive = false;
    var chordTimer = null;
    var indicator = document.getElementById('chordIndicator');

    var navMap = {
        d: 'dashboard',
        f: 'features',
        r: 'roadmap',
        c: 'cycles',
        s: 'stats',
        h: 'history',
        q: 'qa',
    };

    function showChord() {
        chordActive = true;
        if (indicator) indicator.classList.add('visible');
        clearTimeout(chordTimer);
        chordTimer = setTimeout(function() { hideChord(); }, 1500);
    }

    function hideChord() {
        chordActive = false;
        if (indicator) indicator.classList.remove('visible');
        clearTimeout(chordTimer);
    }

    function isInputFocused() {
        var el = document.activeElement;
        if (!el) return false;
        var tag = el.tagName;
        return tag === 'INPUT' || tag === 'TEXTAREA' || tag === 'SELECT' || el.isContentEditable;
    }

    document.addEventListener('keydown', function(e) {
        // Close shortcut modal on Escape
        if (e.key === 'Escape') {
            App.hideShortcutHelp();
            hideChord();
            return;
        }

        // Skip if inside input elements (unless it's Escape)
        if (isInputFocused()) return;

        // If shortcut modal is open, only respond to Escape (handled above)
        var modal = document.getElementById('shortcutModal');
        if (modal && modal.classList.contains('visible')) return;

        if (chordActive) {
            e.preventDefault();
            hideChord();
            var page = navMap[e.key];
            if (page) {
                App._breadcrumbDetail = null;
                App.navigate(page);
            }
            return;
        }

        if (e.key === 'g' && !e.ctrlKey && !e.metaKey && !e.altKey) {
            e.preventDefault();
            showChord();
            return;
        }

        if (e.key === '/' && !e.ctrlKey && !e.metaKey) {
            var search = document.getElementById('featuresSearch')
                || document.querySelector('input[type="text"][placeholder*="earch"]');
            if (search) {
                e.preventDefault();
                search.focus();
                search.select();
            }
            return;
        }

        if (e.key === '?' && !e.ctrlKey && !e.metaKey) {
            e.preventDefault();
            App.showShortcutHelp();
            return;
        }
    });
};

App.showShortcutHelp = function() {
    var modal = document.getElementById('shortcutModal');
    if (modal) {
        modal.classList.add('visible');
        modal.setAttribute('aria-hidden', 'false');
        // Close on overlay click (not on inner modal click)
        modal.onclick = function(e) {
            if (e.target === modal) App.hideShortcutHelp();
        };
    }
};

App.hideShortcutHelp = function() {
    var modal = document.getElementById('shortcutModal');
    if (modal) {
        modal.classList.remove('visible');
        modal.setAttribute('aria-hidden', 'true');
    }
};

// ── Breadcrumb Navigation ──
App.updateBreadcrumbs = function() {
    var content = document.getElementById('content');
    if (!content) return;
    // Remove existing breadcrumb bar
    var existing = content.querySelector('.breadcrumb-bar');
    if (existing) existing.remove();

    var page = App.currentPage;
    if (page === 'dashboard' && !App._breadcrumbDetail) return;

    var pageLabels = {
        dashboard: 'Dashboard', features: 'Features', roadmap: 'Roadmap',
        cycles: 'Cycles', stats: 'Stats', history: 'History',
        qa: 'QA', discussions: 'Discussions',
    };

    var bar = document.createElement('div');
    bar.className = 'breadcrumb-bar';
    bar.setAttribute('aria-label', 'Breadcrumb');

    // Root: Dashboard
    var root = document.createElement('a');
    root.className = 'breadcrumb-item';
    root.textContent = 'Dashboard';
    root.addEventListener('click', function(e) { e.preventDefault(); App._breadcrumbDetail = null; App.navigate('dashboard'); });
    bar.appendChild(root);

    if (page !== 'dashboard') {
        bar.appendChild(App._makeBreadcrumbSep());
        if (App._breadcrumbDetail) {
            var pageLink = document.createElement('a');
            pageLink.className = 'breadcrumb-item';
            pageLink.textContent = pageLabels[page] || page;
            pageLink.addEventListener('click', function(e) { e.preventDefault(); App._breadcrumbDetail = null; App.navigate(page); });
            bar.appendChild(pageLink);
            bar.appendChild(App._makeBreadcrumbSep());
            var detail = document.createElement('span');
            detail.className = 'breadcrumb-item active';
            detail.textContent = App._breadcrumbDetail;
            bar.appendChild(detail);
        } else {
            var current = document.createElement('span');
            current.className = 'breadcrumb-item active';
            current.textContent = pageLabels[page] || page;
            bar.appendChild(current);
        }
    }

    content.insertBefore(bar, content.firstChild);
};

App._makeBreadcrumbSep = function() {
    var sep = document.createElement('span');
    sep.className = 'breadcrumb-separator';
    sep.textContent = '›';
    sep.setAttribute('aria-hidden', 'true');
    return sep;
};

// ── Inline Feature Status Editing ──
// ── Enhanced QA Page ──

App.renderQA = async function() {
    var features, history;
    try {
        var results = await Promise.all([
            App.api('features?status=human-qa'),
            App.api('history'),
        ]);
        features = results[0] || [];
        history = results[1] || [];
    } catch(e) {
        features = [];
        history = [];
    }

    var qaEvents = (history).filter(function(e) {
        return e.event_type === 'qa.approved' || e.event_type === 'qa.rejected';
    });

    // Count review rounds per feature from history
    var reviewCounts = {};
    qaEvents.forEach(function(e) {
        if (e.feature_id) {
            reviewCounts[e.feature_id] = (reviewCounts[e.feature_id] || 0) + 1;
        }
    });

    var approvedCount = qaEvents.filter(function(e) { return e.event_type === 'qa.approved'; }).length;
    var rejectedCount = qaEvents.filter(function(e) { return e.event_type === 'qa.rejected'; }).length;

    var reviewed = qaEvents.slice(0, 10);
    var reviewedHtml = App._renderQAReviewedList(reviewed);

    if (!features.length) {
        return App._renderQAEmptyState(reviewedHtml, approvedCount, rejectedCount);
    }

    var pendingCards = features.map(function(f) {
        return App._renderQACard(f, reviewCounts[f.id] || 0);
    }).join('');

    var header = '<div class="page-header"><h2 class="page-title">Quality Assurance</h2>'
        + '<p class="page-subtitle">Review and approve features</p></div>';

    var summary = '<div class="qa-summary">'
        + '<div class="qa-summary-stat">'
        +   '<div class="qa-summary-count qa-count-pending">' + features.length + '</div>'
        +   '<div class="qa-summary-label">Pending</div>'
        + '</div>'
        + '<div class="qa-summary-divider"></div>'
        + '<div class="qa-summary-stat">'
        +   '<div class="qa-summary-count qa-count-approved">' + approvedCount + '</div>'
        +   '<div class="qa-summary-label">Approved</div>'
        + '</div>'
        + '<div class="qa-summary-divider"></div>'
        + '<div class="qa-summary-stat">'
        +   '<div class="qa-summary-count qa-count-rejected">' + rejectedCount + '</div>'
        +   '<div class="qa-summary-label">Rejected</div>'
        + '</div>'
        + '</div>';

    var layout = '<div class="qa-layout">'
        + '<div>'
        +   '<div class="qa-column-title"><span class="qa-column-dot pending"></span>Pending Review</div>'
        +   pendingCards
        + '</div>'
        + '<div>'
        +   '<div class="qa-column-title"><span class="qa-column-dot reviewed"></span>Recently Reviewed</div>'
        +   reviewedHtml
        + '</div>'
        + '</div>';

    return header + summary + layout;
};

App._renderQACard = function(f, reviewRounds) {
    var id = App._esc(f.id);
    var name = App._esc(f.name);
    var desc = App._esc(f.description || 'No description provided');
    var milestone = f.milestone_name ? App._esc(f.milestone_name) : '';
    var priorityClass = f.priority <= 1 ? 'qa-priority-high'
        : f.priority <= 3 ? 'qa-priority-medium' : 'qa-priority-low';
    var priorityLabel = f.priority <= 1 ? 'High'
        : f.priority <= 3 ? 'Medium' : 'Low';
    var enteredQA = f.updated_at || f.created_at;

    var html = '<div class="qa-review-card" data-qa-feature="' + id + '">'
        + '<div class="qa-card-header">'
        +   '<span class="qa-card-title">' + name + '</span>'
        +   '<div class="qa-card-badges">'
        +     '<span class="badge badge-human-qa">awaiting QA</span>'
        +     '<span class="qa-priority-badge ' + priorityClass + '">P' + f.priority + ' ' + priorityLabel + '</span>'
        +   '</div>'
        + '</div>'
        + '<div class="qa-card-description">' + desc + '</div>'
        + '<div class="qa-card-meta">'
        +   '<span title="Feature ID">🏷️ ' + id + '</span>';

    if (milestone) {
        html += '<span title="Milestone">📌 ' + milestone + '</span>';
    }

    html += '<span title="Entered QA">🕐 ' + App._fmtTimeAgo(enteredQA) + '</span>';

    if (reviewRounds > 0) {
        html += '<span title="Previous review rounds">🔄 ' + reviewRounds + ' prior ' + (reviewRounds === 1 ? 'review' : 'reviews') + '</span>';
    }

    html += '</div>'
        + '<textarea class="qa-notes" id="qaNotes_' + id + '" placeholder="Add review notes or feedback…" aria-label="Review notes for ' + name + '"></textarea>'
        + '<div class="qa-actions">'
        +   '<button class="btn-approve qa-action-approve" data-qa-id="' + id + '" aria-label="Approve ' + name + '">✓ Approve</button>'
        +   '<button class="btn-reject qa-action-reject" data-qa-id="' + id + '" aria-label="Reject ' + name + '">✗ Reject</button>'
        + '</div>'
        + '</div>';

    return html;
};

App._renderQAReviewedList = function(reviewed) {
    if (!reviewed.length) {
        return '<div class="empty-state empty-state--compact">'
            + '<div class="empty-state-icon">📋</div>'
            + '<div class="empty-state-text">No reviews yet</div>'
            + '</div>';
    }

    var items = reviewed.map(function(e) {
        var isApproved = e.event_type === 'qa.approved';
        var icon = isApproved ? '✓' : '✗';
        var cls = isApproved ? 'approved' : 'rejected';
        var label = isApproved ? 'Approved' : 'Rejected';
        var notes = '';
        if (e.data) {
            try {
                var d = JSON.parse(e.data);
                notes = d.notes || d.reason || '';
            } catch(ex) { /* ignore */ }
        }

        var html = '<div class="qa-reviewed-item qa-reviewed-' + cls + '">'
            + '<div class="qa-reviewed-icon ' + cls + '">' + icon + '</div>'
            + '<div class="qa-reviewed-info">'
            +   '<div class="qa-reviewed-name">' + App._esc(e.feature_id || 'Unknown') + '</div>'
            +   '<div class="qa-reviewed-verdict">' + label + '</div>'
            + '</div>'
            + '<div class="qa-reviewed-time">' + App._fmtTimeAgo(e.created_at) + '</div>';

        if (notes) {
            html += '<div class="qa-reviewed-notes">' + App._esc(notes) + '</div>';
        }

        html += '</div>';
        return html;
    }).join('');

    return '<div class="card card--flush">' + items + '</div>';
};

App._renderQAEmptyState = function(reviewedHtml, approvedCount, rejectedCount) {
    var header = '<div class="page-header"><h2 class="page-title">Quality Assurance</h2>'
        + '<p class="page-subtitle">Review and approve features</p></div>';

    var totalReviewed = approvedCount + rejectedCount;
    var statsHtml = '';
    if (totalReviewed > 0) {
        statsHtml = '<div class="qa-empty-stats">'
            + '<span class="qa-empty-stat"><span class="qa-reviewed-icon approved" style="width:18px;height:18px;font-size:0.6rem;display:inline-flex">✓</span> ' + approvedCount + ' approved</span>'
            + '<span class="qa-empty-stat"><span class="qa-reviewed-icon rejected" style="width:18px;height:18px;font-size:0.6rem;display:inline-flex">✗</span> ' + rejectedCount + ' rejected</span>'
            + '</div>';
    }

    return header
        + '<div class="qa-layout">'
        + '<div>'
        +   '<div class="qa-empty-hero">'
        +     '<div class="qa-empty-icon">🎉</div>'
        +     '<div class="qa-empty-title">All caught up!</div>'
        +     '<div class="qa-empty-subtitle">No features are waiting for review.</div>'
        +     statsHtml
        +     '<div class="qa-empty-hint">Features will appear here when they reach the <code>human-qa</code> stage.</div>'
        +   '</div>'
        + '</div>'
        + '<div>'
        +   '<div class="qa-column-title"><span class="qa-column-dot reviewed"></span>Recently Reviewed</div>'
        +   reviewedHtml
        + '</div>'
        + '</div>';
};

App._fmtTimeAgo = function(iso) {
    if (!iso) return '—';
    var now = Date.now();
    var then = new Date(iso).getTime();
    var diff = now - then;
    if (isNaN(diff)) return '—';
    var mins = Math.floor(diff / 60000);
    if (mins < 1) return 'just now';
    if (mins < 60) return mins + 'm ago';
    var hrs = Math.floor(mins / 60);
    if (hrs < 24) return hrs + 'h ago';
    var days = Math.floor(hrs / 24);
    if (days < 7) return days + 'd ago';
    // Fall back to formatted date
    return new Date(iso).toLocaleDateString('en-US', { month: 'short', day: 'numeric' });
};

App._esc = function(s) {
    if (!s) return '';
    var d = document.createElement('div');
    d.textContent = s;
    return d.innerHTML;
};

App._qaConfirmAndAct = async function(featureId, action) {
    var notesEl = document.getElementById('qaNotes_' + featureId);
    var notes = notesEl ? notesEl.value : '';

    var isApprove = action === 'approve';
    var verb = isApprove ? 'approve' : 'reject';
    var defaultNote = isApprove ? 'Approved via web' : 'Rejected via web';

    if (!isApprove && !notes.trim()) {
        // For rejections, prompt for notes if empty
        if (notesEl) {
            notesEl.focus();
            notesEl.placeholder = 'Please provide a reason for rejection…';
            notesEl.classList.add('qa-notes-required');
            App.toast('Please add rejection notes', 'error');
            return;
        }
    }

    // Show confirmation modal
    App._showQAConfirmModal(featureId, verb, notes || defaultNote, function() {
        App._executeQAAction(featureId, verb, notes || defaultNote);
    });
};

App._showQAConfirmModal = function(featureId, verb, notes, onConfirm) {
    // Remove existing modal if any
    var existing = document.getElementById('qaConfirmModal');
    if (existing) existing.remove();

    var isApprove = verb === 'approve';
    var icon = isApprove ? '✓' : '✗';
    var color = isApprove ? 'var(--success)' : 'var(--danger)';
    var title = isApprove ? 'Approve Feature' : 'Reject Feature';
    var desc = isApprove
        ? 'This will mark the feature as done and complete the QA cycle.'
        : 'This will send the feature back to development for further work.';

    var modal = document.createElement('div');
    modal.id = 'qaConfirmModal';
    modal.className = 'qa-confirm-overlay';
    modal.innerHTML = '<div class="qa-confirm-dialog">'
        + '<div class="qa-confirm-header">'
        +   '<span class="qa-confirm-icon" style="color:' + color + '">' + icon + '</span>'
        +   '<span class="qa-confirm-title">' + title + '</span>'
        + '</div>'
        + '<div class="qa-confirm-body">'
        +   '<div class="qa-confirm-feature">Feature: <strong>' + App._esc(featureId) + '</strong></div>'
        +   '<div class="qa-confirm-desc">' + desc + '</div>'
        +   (notes ? '<div class="qa-confirm-notes-label">Notes:</div><div class="qa-confirm-notes-preview">' + App._esc(notes) + '</div>' : '')
        + '</div>'
        + '<div class="qa-confirm-actions">'
        +   '<button class="qa-confirm-cancel">Cancel</button>'
        +   '<button class="qa-confirm-submit" style="background:' + color + ';border-color:' + color + '">' + icon + ' ' + (isApprove ? 'Approve' : 'Reject') + '</button>'
        + '</div>'
        + '</div>';

    document.body.appendChild(modal);

    // Animate in
    requestAnimationFrame(function() { modal.classList.add('visible'); });

    modal.querySelector('.qa-confirm-cancel').addEventListener('click', function() {
        modal.classList.remove('visible');
        setTimeout(function() { modal.remove(); }, 200);
    });

    modal.querySelector('.qa-confirm-submit').addEventListener('click', function() {
        modal.classList.remove('visible');
        setTimeout(function() { modal.remove(); }, 200);
        onConfirm();
    });

    // Close on overlay click
    modal.addEventListener('click', function(e) {
        if (e.target === modal) {
            modal.classList.remove('visible');
            setTimeout(function() { modal.remove(); }, 200);
        }
    });

    // Close on Escape
    var escHandler = function(e) {
        if (e.key === 'Escape') {
            modal.classList.remove('visible');
            setTimeout(function() { modal.remove(); }, 200);
            document.removeEventListener('keydown', escHandler);
        }
    };
    document.addEventListener('keydown', escHandler);
};

App._executeQAAction = async function(featureId, verb, notes) {
    try {
        await App.apiPost('qa/' + featureId + '/' + verb, { notes: notes });
        var isApprove = verb === 'approve';
        App.toast(isApprove ? '✓ Feature approved' : '✗ Feature rejected', isApprove ? 'success' : 'error');
    } catch(e) {
        App.toast('Error: could not ' + verb + ' feature', 'error');
    }
    App.navigate('qa');
};

// Wrap bindPageEvents to add enhanced QA bindings
(function() {
    var _origBind = App.bindPageEvents;
    App.bindPageEvents = function(page) {
        _origBind.call(this, page);
        if (page === 'qa') {
            App._bindQAPageEvents();
        }
    };
})();

App._bindQAPageEvents = function() {
    // Approve buttons (new class qa-action-approve)
    document.querySelectorAll('.qa-action-approve').forEach(function(btn) {
        btn.addEventListener('click', function() {
            App._qaConfirmAndAct(btn.dataset.qaId, 'approve');
        });
    });

    // Reject buttons (new class qa-action-reject)
    document.querySelectorAll('.qa-action-reject').forEach(function(btn) {
        btn.addEventListener('click', function() {
            App._qaConfirmAndAct(btn.dataset.qaId, 'reject');
        });
    });

    // Remove required highlight on notes input
    document.querySelectorAll('.qa-notes').forEach(function(ta) {
        ta.addEventListener('input', function() {
            ta.classList.remove('qa-notes-required');
        });
    });
};

App.changeFeatureStatus = async function(id, newStatus) {
    try {
        var result = await App.apiPatch('features/' + encodeURIComponent(id), { status: newStatus });
        if (result.error) {
            App.toast('Error: ' + result.error, 'error');
            return;
        }
        App.toast('Status changed to ' + newStatus, 'success');
        // Update the local data so refresh uses correct state
        if (App._featuresData) {
            for (var i = 0; i < App._featuresData.length; i++) {
                if (App._featuresData[i].id === id) {
                    App._featuresData[i].status = newStatus;
                    break;
                }
            }
        }
        // Re-render features page
        App._breadcrumbDetail = null;
        App.navigate('features');
    } catch (e) {
        App.toast('Failed to change status', 'error');
    }
};

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

