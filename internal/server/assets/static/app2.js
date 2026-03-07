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

