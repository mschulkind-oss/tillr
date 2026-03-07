// app5.js — Timeline View, Batch Feature Operations & Activity Heatmap

// =====================================================
// ACTIVITY HEATMAP
// =====================================================

App.renderHeatmap = function(containerEl, heatmapData) {
    var days = heatmapData.days || [];
    var monthNames = ['Jan','Feb','Mar','Apr','May','Jun','Jul','Aug','Sep','Oct','Nov','Dec'];
    var dayLabels = ['Sun','Mon','Tue','Wed','Thu','Fri','Sat'];

    // Build a map of date -> day data
    var dayMap = {};
    for (var i = 0; i < days.length; i++) {
        dayMap[days[i].date] = days[i];
    }

    // Build 52 weeks of data ending today
    var today = new Date();
    today.setHours(0, 0, 0, 0);
    // Find the last Saturday (end of last complete week column) or use today
    var endDate = new Date(today);
    // Start from ~52 weeks ago, aligned to Sunday
    var startDate = new Date(today);
    startDate.setDate(startDate.getDate() - 364);
    // Align to Sunday
    startDate.setDate(startDate.getDate() - startDate.getDay());

    // Build weeks array: each week is an array of 7 day objects
    var weeks = [];
    var currentDate = new Date(startDate);
    while (currentDate <= today) {
        var week = [];
        for (var d = 0; d < 7; d++) {
            var dateStr = currentDate.getFullYear() + '-'
                + String(currentDate.getMonth() + 1).padStart(2, '0') + '-'
                + String(currentDate.getDate()).padStart(2, '0');
            var dayData = dayMap[dateStr];
            var count = dayData ? dayData.count : 0;
            var level = count === 0 ? 0 : count <= 3 ? 1 : count <= 6 ? 2 : count <= 9 ? 3 : 4;
            var isFuture = currentDate > today;
            week.push({
                date: dateStr,
                count: count,
                level: level,
                events: dayData ? dayData.events : {},
                isFuture: isFuture,
                dayOfWeek: currentDate.getDay(),
                month: currentDate.getMonth(),
                year: currentDate.getFullYear()
            });
            currentDate.setDate(currentDate.getDate() + 1);
        }
        weeks.push(week);
    }

    // Build month labels with column positions
    var monthLabels = [];
    var lastMonth = -1;
    for (var w = 0; w < weeks.length; w++) {
        var firstDayMonth = weeks[w][0].month;
        if (firstDayMonth !== lastMonth) {
            monthLabels.push({ month: monthNames[firstDayMonth], col: w });
            lastMonth = firstDayMonth;
        }
    }

    // Build HTML
    var html = '<div class="heatmap-wrap">';

    // Month labels row
    html += '<div class="heatmap-top"><div class="hm-labels"></div><div class="heatmap-months">';
    var prevCol = 0;
    for (var m = 0; m < monthLabels.length; m++) {
        var ml = monthLabels[m];
        var colSpan = (m + 1 < monthLabels.length ? monthLabels[m + 1].col : weeks.length) - ml.col;
        var widthPx = colSpan * 14; // 12px cell + 2px gap
        html += '<span class="hm-month" style="width:' + widthPx + 'px">' + ml.month + '</span>';
    }
    html += '</div></div>';

    // Grid body: day labels + cells
    html += '<div class="heatmap-body">';

    // Day labels column
    html += '<div class="hm-labels">';
    for (var dl = 0; dl < 7; dl++) {
        // Only show Mon, Wed, Fri labels
        if (dl === 1 || dl === 3 || dl === 5) {
            html += '<div class="hm-label">' + dayLabels[dl].substring(0, 3) + '</div>';
        } else {
            html += '<div class="hm-label"></div>';
        }
    }
    html += '</div>';

    // Week columns
    html += '<div class="heatmap">';
    for (var wi = 0; wi < weeks.length; wi++) {
        html += '<div class="hm-week">';
        for (var di = 0; di < weeks[wi].length; di++) {
            var cell = weeks[wi][di];
            if (cell.isFuture) {
                html += '<div class="hm-cell" data-level="0" style="opacity:0.3"></div>';
            } else {
                html += '<div class="hm-cell" data-level="' + cell.level + '"'
                    + ' data-date="' + cell.date + '"'
                    + ' data-count="' + cell.count + '"></div>';
            }
        }
        html += '</div>';
    }
    html += '</div></div>';

    // Legend
    html += '<div class="hm-legend"><span>Less</span>';
    for (var l = 0; l <= 4; l++) {
        html += '<span class="hm-legend-cell" data-level="' + l + '"></span>';
    }
    html += '<span>More</span></div>';

    html += '</div>';

    // Tooltip element
    html += '<div class="hm-tooltip" id="hmTooltip"></div>';

    containerEl.innerHTML = html;

    // Bind hover tooltip
    var tooltip = containerEl.querySelector('#hmTooltip');
    var cells = containerEl.querySelectorAll('.hm-cell[data-date]');
    for (var ci = 0; ci < cells.length; ci++) {
        cells[ci].addEventListener('mouseenter', function(e) {
            var c = e.target;
            var date = c.getAttribute('data-date');
            var count = c.getAttribute('data-count');
            var parts = date.split('-');
            var dateObj = new Date(parseInt(parts[0]), parseInt(parts[1]) - 1, parseInt(parts[2]));
            var formatted = monthNames[dateObj.getMonth()] + ' ' + dateObj.getDate() + ', ' + dateObj.getFullYear();
            var text = '<span class="hm-tooltip-count">' + count + ' event' + (count !== '1' ? 's' : '') + '</span> on ' + formatted;
            tooltip.innerHTML = text;
            tooltip.style.display = 'block';
            var rect = c.getBoundingClientRect();
            tooltip.style.left = (rect.left + rect.width / 2 - tooltip.offsetWidth / 2) + 'px';
            tooltip.style.top = (rect.top - tooltip.offsetHeight - 6) + 'px';
        });
        cells[ci].addEventListener('mouseleave', function() {
            tooltip.style.display = 'none';
        });
    }
};

// =====================================================
// TIMELINE PAGE
// =====================================================

App.renderTimeline = async function() {
    var features, milestones;
    try {
        var data = await Promise.all([
            App.api('features'),
            App.api('milestones').catch(function() { return []; }),
        ]);
        features = data[0];
        milestones = data[1];
    } catch (e) {
        return '<div class="empty-state"><div class="empty-state-icon">⚠️</div><div class="empty-state-text">Could not load timeline data</div></div>';
    }

    if (!features || !features.length) {
        return '<div class="page-header"><h2 class="page-title">📅 Timeline</h2><p class="page-subtitle">Feature progress over time</p></div>' +
            '<div class="empty-state"><div class="empty-state-icon">📅</div><div class="empty-state-text">No features to display</div>' +
            '<div class="empty-state-hint">Add features to see them on the timeline.</div>' +
            '<div class="empty-state-cta"><span class="cta-icon">$</span> lifecycle feature add &lt;name&gt;</div></div>';
    }

    App._timelineScale = App._timelineScale || 'month';
    App._timelineFeatures = features;
    App._timelineMilestones = milestones;

    var scaleButtons = ['week', 'month', 'quarter'].map(function(s) {
        return '<button class="filter-pill tl-scale-btn' + (s === App._timelineScale ? ' active' : '') + '" data-scale="' + s + '">' +
            s.charAt(0).toUpperCase() + s.slice(1) + '</button>';
    }).join('');

    return '<div class="page-header"><h2 class="page-title">📅 Timeline</h2>' +
        '<p class="page-subtitle">Gantt-style view of feature progress</p></div>' +
        '<div class="features-toolbar"><div class="filter-pills">' + scaleButtons + '</div></div>' +
        '<div class="card"><div class="tl-container" id="timelineContainer">' +
        App._buildTimeline(features, milestones, App._timelineScale) +
        '</div></div>';
};

App._buildTimeline = function(features, milestones, scale) {
    var now = new Date();
    var dates = [];
    features.forEach(function(f) {
        if (f.created_at) dates.push(new Date(f.created_at));
        if (f.updated_at) dates.push(new Date(f.updated_at));
    });
    milestones.forEach(function(m) {
        if (m.created_at) dates.push(new Date(m.created_at));
    });
    dates.push(now);

    if (!dates.length) return '<div class="empty-state-hint">No date data available.</div>';

    var minDate = new Date(Math.min.apply(null, dates));
    var maxDate = new Date(Math.max.apply(null, dates));

    // Add padding
    minDate.setDate(minDate.getDate() - 7);
    maxDate.setDate(maxDate.getDate() + 14);

    var dayMs = 86400000;
    var totalDays = Math.max(Math.ceil((maxDate - minDate) / dayMs), 7);

    // Pixels per day based on scale
    var pxPerDay;
    switch (scale) {
        case 'week': pxPerDay = 20; break;
        case 'quarter': pxPerDay = 3; break;
        default: pxPerDay = 8;
    }
    var totalWidth = totalDays * pxPerDay;

    var statusColors = {
        'done': 'var(--success)',
        'implementing': 'var(--accent)',
        'agent-qa': 'var(--accent)',
        'planning': 'var(--warning)',
        'draft': 'var(--warning)',
        'blocked': 'var(--danger)',
        'human-qa': 'var(--purple)',
    };

    // Build header with time divisions
    var headerCells = '';
    var cursor = new Date(minDate);
    while (cursor < maxDate) {
        var label, cellDays;
        if (scale === 'week') {
            label = cursor.toLocaleDateString(undefined, { month: 'short', day: 'numeric' });
            cellDays = 7;
        } else if (scale === 'quarter') {
            label = cursor.toLocaleDateString(undefined, { month: 'short', year: '2-digit' });
            cellDays = 30;
        } else {
            label = cursor.toLocaleDateString(undefined, { month: 'short', day: 'numeric' });
            cellDays = 14;
        }
        var cellWidth = cellDays * pxPerDay;
        headerCells += '<div class="tl-header-cell" style="min-width:' + cellWidth + 'px;width:' + cellWidth + 'px">' + label + '</div>';
        cursor.setDate(cursor.getDate() + cellDays);
    }

    // Today marker position
    var todayOffset = Math.round(((now - minDate) / dayMs) * pxPerDay);

    // Group features by milestone
    var milestoneMap = {};
    milestones.forEach(function(m) { milestoneMap[m.id] = m; });
    var groups = {};
    var noMilestone = [];
    features.forEach(function(f) {
        if (f.milestone_id && milestoneMap[f.milestone_id]) {
            if (!groups[f.milestone_id]) groups[f.milestone_id] = [];
            groups[f.milestone_id].push(f);
        } else {
            noMilestone.push(f);
        }
    });

    var rowsHtml = '';

    // Render milestone groups
    var milestoneIds = milestones.map(function(m) { return m.id; });
    milestoneIds.forEach(function(msId) {
        var ms = milestoneMap[msId];
        var groupFeatures = groups[msId];
        if (!groupFeatures || !groupFeatures.length) return;

        // Milestone header row
        rowsHtml += '<div class="tl-group-header">' +
            '<div class="tl-label tl-milestone-label">🏁 ' + App._tlEsc(ms.name) + '</div>' +
            '<div class="tl-track" style="width:' + totalWidth + 'px">';
        // Milestone marker line
        if (ms.created_at) {
            var msOffset = Math.round(((new Date(ms.created_at) - minDate) / dayMs) * pxPerDay);
            rowsHtml += '<div class="tl-milestone-marker" style="left:' + msOffset + 'px" title="' + App._tlEsc(ms.name) + '"></div>';
        }
        rowsHtml += '</div></div>';

        groupFeatures.forEach(function(f) {
            rowsHtml += App._buildTimelineRow(f, minDate, dayMs, pxPerDay, totalWidth, statusColors);
        });
    });

    // Ungrouped features
    if (noMilestone.length) {
        rowsHtml += '<div class="tl-group-header">' +
            '<div class="tl-label tl-milestone-label">📦 No Milestone</div>' +
            '<div class="tl-track" style="width:' + totalWidth + 'px"></div></div>';
        noMilestone.forEach(function(f) {
            rowsHtml += App._buildTimelineRow(f, minDate, dayMs, pxPerDay, totalWidth, statusColors);
        });
    }

    return '<div class="tl-scroll">' +
        '<div class="tl-chart" style="min-width:' + (totalWidth + 200) + 'px">' +
        '<div class="tl-header-row"><div class="tl-label tl-label-header">Feature</div>' +
        '<div class="tl-track tl-header-track" style="width:' + totalWidth + 'px">' + headerCells + '</div></div>' +
        '<div class="tl-body" style="position:relative">' +
        '<div class="tl-today" style="left:' + (200 + todayOffset) + 'px" title="Today"></div>' +
        rowsHtml +
        '</div></div></div>';
};

App._buildTimelineRow = function(f, minDate, dayMs, pxPerDay, totalWidth, statusColors) {
    var start = f.created_at ? new Date(f.created_at) : new Date();
    var end = f.status === 'done' ? new Date(f.updated_at || f.created_at) : new Date();
    var startOffset = Math.max(0, Math.round(((start - minDate) / dayMs) * pxPerDay));
    var barWidth = Math.max(6, Math.round(((end - start) / dayMs) * pxPerDay));
    var color = statusColors[f.status] || 'var(--text-muted)';
    var name = App._tlEsc(f.name);
    var badge = '<span class="badge badge-' + f.status + '" style="font-size:0.65rem;margin-left:4px">' + f.status + '</span>';

    return '<div class="tl-row">' +
        '<div class="tl-label" title="' + name + '">' + name + '</div>' +
        '<div class="tl-track" style="width:' + totalWidth + 'px">' +
        '<div class="tl-bar" data-feature-id="' + App._tlEsc(f.id) + '" style="left:' + startOffset + 'px;width:' + barWidth + 'px;background:' + color + '" title="' + name + ' (' + f.status + ')">' +
        '<span class="tl-bar-label">' + name + '</span>' +
        '</div></div></div>';
};

App._tlEsc = function(s) {
    if (!s) return '';
    var d = document.createElement('div');
    d.textContent = s;
    return d.innerHTML;
};

// =====================================================
// TIMELINE PAGE EVENTS
// =====================================================

App._bindTimelineEvents = function() {
    // Scale buttons
    document.querySelectorAll('.tl-scale-btn').forEach(function(btn) {
        btn.addEventListener('click', function() {
            App._timelineScale = btn.dataset.scale;
            App.navigate('timeline');
        });
    });

    // Click bar to navigate to features
    document.querySelectorAll('.tl-bar[data-feature-id]').forEach(function(bar) {
        bar.style.cursor = 'pointer';
        bar.addEventListener('click', function() {
            App.navigateTo('features', bar.dataset.featureId);
        });
    });
};

// =====================================================
// BATCH FEATURE OPERATIONS (Web UI)
// =====================================================

App._batchSelectedIds = new Set();

App._buildBatchCheckbox = function(featureId) {
    return '<td class="tl-batch-td" style="width:32px;text-align:center;padding:4px">' +
        '<input type="checkbox" class="batch-checkbox" data-feature-id="' + featureId + '" ' +
        (App._batchSelectedIds.has(featureId) ? 'checked' : '') +
        ' onclick="event.stopPropagation();App._toggleBatchSelect(this)" aria-label="Select feature"></td>';
};

App._toggleBatchSelect = function(checkbox) {
    var fid = checkbox.dataset.featureId;
    if (checkbox.checked) {
        App._batchSelectedIds.add(fid);
    } else {
        App._batchSelectedIds.delete(fid);
    }
    App._updateBatchBar();

    // Update select-all checkbox
    var selectAll = document.getElementById('batchSelectAll');
    if (selectAll) {
        var allBoxes = document.querySelectorAll('.batch-checkbox');
        var checkedBoxes = document.querySelectorAll('.batch-checkbox:checked');
        selectAll.checked = allBoxes.length > 0 && allBoxes.length === checkedBoxes.length;
        selectAll.indeterminate = checkedBoxes.length > 0 && checkedBoxes.length < allBoxes.length;
    }
};

App._toggleBatchSelectAll = function(checkbox) {
    var allBoxes = document.querySelectorAll('.batch-checkbox');
    allBoxes.forEach(function(cb) {
        cb.checked = checkbox.checked;
        var fid = cb.dataset.featureId;
        if (checkbox.checked) {
            App._batchSelectedIds.add(fid);
        } else {
            App._batchSelectedIds.delete(fid);
        }
    });
    App._updateBatchBar();
};

App._updateBatchBar = function() {
    var bar = document.getElementById('batchActionBar');
    if (!bar) return;
    var count = App._batchSelectedIds.size;
    if (count === 0) {
        bar.style.display = 'none';
        return;
    }
    bar.style.display = 'flex';
    var label = bar.querySelector('.batch-count');
    if (label) label.textContent = count + ' selected';
};

App._batchAction = async function(action, value) {
    var ids = Array.from(App._batchSelectedIds);
    if (!ids.length) return;
    try {
        await App.apiPost('features/batch', {
            feature_ids: ids,
            action: action,
            value: value,
        });
        App._batchSelectedIds.clear();
        App.navigate('features');
    } catch (e) {
        alert('Batch update failed: ' + (e.message || e));
    }
};

App._showBatchDropdown = function(button, type) {
    // Remove any existing dropdown
    var existing = document.querySelector('.batch-dropdown');
    if (existing) existing.remove();

    var options;
    if (type === 'set_status') {
        options = ['draft', 'planning', 'implementing', 'agent-qa', 'human-qa', 'done', 'blocked'];
    } else if (type === 'set_milestone') {
        options = (App._batchMilestones || []).map(function(m) { return m.id; });
        if (!options.length) { alert('No milestones available'); return; }
    } else if (type === 'set_priority') {
        options = ['1', '2', '3', '4', '5', '6', '7', '8', '9', '10'];
    }

    var dd = document.createElement('div');
    dd.className = 'batch-dropdown';
    dd.innerHTML = options.map(function(o) {
        var label = o;
        if (type === 'set_milestone') {
            var ms = (App._batchMilestones || []).find(function(m) { return m.id === o; });
            label = ms ? ms.name : o;
        }
        return '<div class="batch-dropdown-item" data-value="' + o + '">' + label + '</div>';
    }).join('');

    var rect = button.getBoundingClientRect();
    dd.style.position = 'fixed';
    dd.style.bottom = (window.innerHeight - rect.top + 4) + 'px';
    dd.style.left = rect.left + 'px';
    document.body.appendChild(dd);

    dd.querySelectorAll('.batch-dropdown-item').forEach(function(item) {
        item.addEventListener('click', function() {
            App._batchAction(type, item.dataset.value);
            dd.remove();
        });
    });

    // Close on outside click
    setTimeout(function() {
        var handler = function(e) {
            if (!dd.contains(e.target)) {
                dd.remove();
                document.removeEventListener('click', handler);
            }
        };
        document.addEventListener('click', handler);
    }, 0);
};

// =====================================================
// INJECT BATCH UI INTO FEATURES PAGE
// =====================================================

(function() {
    // Wrap buildFeaturesTable to add checkboxes
    var _origBuildFeaturesTable = App.buildFeaturesTable.bind(App);
    App.buildFeaturesTable = function(features) {
        var html = _origBuildFeaturesTable(features);
        if (!features || !features.length) return html;

        // Inject checkbox header and cells via DOM manipulation after render
        // We use a marker class so we know to inject checkboxes after DOM mount
        return '<div data-batch-inject="1">' + html + '</div>';
    };

    // Wrap _setupFeaturePage to add batch bindings
    var _origSetupFeaturePage = App._setupFeaturePage;
    App._setupFeaturePage = function() {
        _origSetupFeaturePage.call(this);
        App._injectBatchUI();
    };
})();

App._injectBatchUI = function() {
    var wrap = document.querySelector('[data-batch-inject]');
    if (!wrap) return;

    // Load milestones for batch dropdown
    App.api('milestones').then(function(ms) {
        App._batchMilestones = ms || [];
    }).catch(function() {
        App._batchMilestones = [];
    });

    // Add checkbox to header
    var thead = wrap.querySelector('thead tr');
    if (thead && !thead.querySelector('.batch-th')) {
        var th = document.createElement('th');
        th.className = 'batch-th';
        th.style.cssText = 'width:32px;text-align:center;padding:4px';
        th.innerHTML = '<input type="checkbox" id="batchSelectAll" onclick="App._toggleBatchSelectAll(this)" aria-label="Select all" title="Select all">';
        thead.insertBefore(th, thead.firstChild);
    }

    // Add checkbox to each feature row
    wrap.querySelectorAll('.ft-row[data-feature-id]').forEach(function(row) {
        if (row.querySelector('.batch-checkbox')) return;
        var fid = row.dataset.featureId;
        var td = document.createElement('td');
        td.style.cssText = 'width:32px;text-align:center;padding:4px';
        td.innerHTML = '<input type="checkbox" class="batch-checkbox" data-feature-id="' + fid + '" ' +
            (App._batchSelectedIds.has(fid) ? 'checked' : '') +
            ' onclick="event.stopPropagation();App._toggleBatchSelect(this)" aria-label="Select feature">';
        row.insertBefore(td, row.firstChild);
    });

    // Add extra colspan to detail rows
    wrap.querySelectorAll('.ft-detail-row td[colspan]').forEach(function(td) {
        var current = parseInt(td.getAttribute('colspan'), 10);
        td.setAttribute('colspan', current + 1);
    });

    // Add batch action bar if not present
    if (!document.getElementById('batchActionBar')) {
        var bar = document.createElement('div');
        bar.id = 'batchActionBar';
        bar.className = 'batch-action-bar';
        bar.style.display = 'none';
        bar.innerHTML = '<span class="batch-count">0 selected</span>' +
            '<button class="batch-btn" onclick="App._showBatchDropdown(this,\'set_status\')">Set Status ▾</button>' +
            '<button class="batch-btn" onclick="App._showBatchDropdown(this,\'set_milestone\')">Set Milestone ▾</button>' +
            '<button class="batch-btn" onclick="App._showBatchDropdown(this,\'set_priority\')">Set Priority ▾</button>' +
            '<button class="batch-btn batch-btn-clear" onclick="App._batchSelectedIds.clear();document.querySelectorAll(\'.batch-checkbox\').forEach(function(c){c.checked=false});App._updateBatchBar();var sa=document.getElementById(\'batchSelectAll\');if(sa){sa.checked=false;sa.indeterminate=false}">✕ Clear</button>';
        document.body.appendChild(bar);
    }
    App._updateBatchBar();
};

// =====================================================
// MONKEY-PATCH renderPage TO ADD TIMELINE ROUTE
// =====================================================

(function() {
    var _origRenderPage = App.renderPage.bind(App);
    App.renderPage = async function(page) {
        if (page === 'timeline') return App.renderTimeline();
        return _origRenderPage(page);
    };
})();

// =====================================================
// MONKEY-PATCH bindPageEvents TO ADD TIMELINE BINDINGS
// =====================================================

(function() {
    var _origBindPageEvents = App.bindPageEvents.bind(App);
    App.bindPageEvents = function(page) {
        _origBindPageEvents(page);
        if (page === 'timeline') {
            App._bindTimelineEvents();
        }
    };
})();

// Image Lightbox
(function() {
    function closeLightbox() {
        var lb = document.getElementById('lightbox');
        if (lb) { lb.style.display = 'none'; document.body.style.overflow = ''; }
    }

    document.addEventListener('click', function(e) {
        var img = e.target.closest('img');
        if (!img || img.closest('.lightbox')) return;
        var lb = document.getElementById('lightbox');
        if (!lb) return;
        lb.querySelector('.lightbox-img').src = img.src;
        lb.style.display = 'flex';
        document.body.style.overflow = 'hidden';
    });

    document.addEventListener('click', function(e) {
        if (e.target.closest('.lightbox-backdrop')) closeLightbox();
    });

    document.addEventListener('keydown', function(e) {
        if (e.key === 'Escape') closeLightbox();
    });
})();

// =====================================================
// CLICKABLE STAT CARDS — navigational drill-down
// =====================================================

document.addEventListener('click', function(e) {
    var card = e.target.closest('.stats-card-link[data-nav]');
    if (!card) return;
    var nav = card.getAttribute('data-nav');
    if (!nav) return;
    var parts = nav.split('?');
    var page = parts[0];
    var params = parts[1] || '';
    if (params) {
        window._statsNavFilter = {};
        params.split('&').forEach(function(p) {
            var kv = p.split('=');
            window._statsNavFilter[kv[0]] = kv[1];
        });
    }
    window.location.hash = '#' + page;
});

// Apply stats navigation filter on Features page
(function() {
    var origBind = App.bindPageEvents;
    App.bindPageEvents = function(page) {
        origBind.call(this, page);
        if (page === 'features' && window._statsNavFilter && window._statsNavFilter.status) {
            var status = window._statsNavFilter.status;
            window._statsNavFilter = null;
            App._featuresFilter = status;
            // Click the matching pill
            var pill = document.querySelector('.filter-pill[data-status="' + status + '"]');
            if (pill) pill.click();
        }
    };
})();
