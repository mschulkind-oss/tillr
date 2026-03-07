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

App.drawBurndownChart = function(canvasId, tooltipId, points) {
    var canvas = document.getElementById(canvasId);
    if (!canvas) return;
    var ctx = canvas.getContext('2d');
    var container = canvas.parentElement;
    var dpr = window.devicePixelRatio || 1;
    var w = container.clientWidth || 400;
    var h = container.clientHeight || 320;

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

    if (!points || points.length === 0) {
        ctx.fillStyle = textSec;
        ctx.font = '14px ' + font;
        ctx.textAlign = 'center';
        ctx.textBaseline = 'middle';
        ctx.fillText('No feature data yet', w / 2, h / 2);
        return;
    }

    var pad = { top: 28, right: 24, bottom: 48, left: 52 };
    var plotW = w - pad.left - pad.right;
    var plotH = h - pad.top - pad.bottom;
    var maxY = Math.max.apply(null, points.map(function(p) { return p.total; })) || 1;
    // Round maxY up to nearest nice number
    maxY = Math.ceil(maxY * 1.1);
    if (maxY < 2) maxY = 2;

    // Grid lines
    var gridCount = Math.min(maxY, 6);
    var gridStep = Math.ceil(maxY / gridCount);
    ctx.strokeStyle = border;
    ctx.lineWidth = 0.5;
    ctx.setLineDash([4, 4]);
    for (var gl = gridStep; gl <= maxY; gl += gridStep) {
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

    // Zero line label
    ctx.fillStyle = textSec;
    ctx.font = '10px ' + font;
    ctx.textAlign = 'right';
    ctx.textBaseline = 'middle';
    ctx.fillText('0', pad.left - 8, pad.top + plotH);

    // Calculate point positions
    var chartPoints = points.map(function(p, i) {
        var x = pad.left + (points.length === 1 ? plotW / 2 : (i / (points.length - 1)) * plotW);
        var yRemaining = pad.top + plotH - (Math.min(p.remaining, maxY) / maxY * plotH);
        return { x: x, yRemaining: yRemaining, date: p.date, remaining: p.remaining, done: p.done, total: p.total };
    });

    // X-axis date labels
    var step = Math.max(1, Math.floor(points.length / 8));
    ctx.textAlign = 'center';
    ctx.textBaseline = 'top';
    chartPoints.forEach(function(p, i) {
        if (i % step === 0 || i === points.length - 1) {
            ctx.fillStyle = textSec;
            ctx.font = '9px ' + font;
            ctx.save();
            ctx.translate(p.x, pad.top + plotH + 8);
            ctx.rotate(-0.4);
            ctx.fillText(p.date.length >= 5 ? p.date.substring(5) : p.date, 0, 0);
            ctx.restore();
        }
    });

    // Ideal burndown line (dashed, from first point's total to 0)
    var idealStart = points[0].total;
    if (idealStart > 0) {
        ctx.beginPath();
        var idealY0 = pad.top + plotH - (idealStart / maxY * plotH);
        var idealY1 = pad.top + plotH;
        ctx.moveTo(pad.left, idealY0);
        ctx.lineTo(pad.left + plotW, idealY1);
        ctx.strokeStyle = '#6b7280';
        ctx.lineWidth = 1.5;
        ctx.setLineDash([6, 4]);
        ctx.stroke();
        ctx.setLineDash([]);
    }

    // Gradient fill under remaining line
    ctx.beginPath();
    chartPoints.forEach(function(p, i) {
        if (i === 0) ctx.moveTo(p.x, p.yRemaining);
        else ctx.lineTo(p.x, p.yRemaining);
    });
    ctx.lineTo(chartPoints[chartPoints.length - 1].x, pad.top + plotH);
    ctx.lineTo(chartPoints[0].x, pad.top + plotH);
    ctx.closePath();
    var grad = ctx.createLinearGradient(0, pad.top, 0, pad.top + plotH);
    grad.addColorStop(0, 'rgba(239, 68, 68, 0.12)');
    grad.addColorStop(1, 'rgba(239, 68, 68, 0.01)');
    ctx.fillStyle = grad;
    ctx.fill();

    // Actual burndown line (remaining features)
    ctx.beginPath();
    chartPoints.forEach(function(p, i) {
        if (i === 0) ctx.moveTo(p.x, p.yRemaining);
        else ctx.lineTo(p.x, p.yRemaining);
    });
    ctx.strokeStyle = '#ef4444';
    ctx.lineWidth = 2.5;
    ctx.stroke();

    // Data point dots (sampled to avoid overcrowding)
    var dotStep = Math.max(1, Math.floor(chartPoints.length / 30));
    chartPoints.forEach(function(p, i) {
        if (i % dotStep === 0 || i === chartPoints.length - 1) {
            ctx.beginPath();
            ctx.arc(p.x, p.yRemaining, 3, 0, 2 * Math.PI);
            ctx.fillStyle = '#ef4444';
            ctx.fill();
            ctx.strokeStyle = bgCard;
            ctx.lineWidth = 1.5;
            ctx.stroke();
        }
    });

    // Legend
    ctx.font = '10px ' + font;
    ctx.textAlign = 'left';
    ctx.textBaseline = 'middle';
    var legX = pad.left + 8;
    var legY = pad.top + 4;
    // Actual line legend
    ctx.beginPath();
    ctx.moveTo(legX, legY);
    ctx.lineTo(legX + 20, legY);
    ctx.strokeStyle = '#ef4444';
    ctx.lineWidth = 2;
    ctx.setLineDash([]);
    ctx.stroke();
    ctx.fillStyle = textSec;
    ctx.fillText('Remaining', legX + 24, legY);
    // Ideal line legend
    ctx.beginPath();
    ctx.moveTo(legX + 100, legY);
    ctx.lineTo(legX + 120, legY);
    ctx.strokeStyle = '#6b7280';
    ctx.lineWidth = 1.5;
    ctx.setLineDash([6, 4]);
    ctx.stroke();
    ctx.setLineDash([]);
    ctx.fillStyle = textSec;
    ctx.fillText('Ideal', legX + 124, legY);

    // Store for tooltip
    canvas._chartPoints = chartPoints;
    if (!canvas._tooltipBound) {
        canvas._tooltipBound = true;
        var tooltip = document.getElementById(tooltipId);
        canvas.addEventListener('mousemove', function(e) {
            if (!tooltip || !canvas._chartPoints) return;
            var rect = canvas.getBoundingClientRect();
            var mx = e.clientX - rect.left;
            var closest = null, minDist = 20;
            canvas._chartPoints.forEach(function(p) {
                var d = Math.abs(p.x - mx);
                if (d < minDist) { minDist = d; closest = p; }
            });
            if (closest) {
                tooltip.style.display = 'block';
                var tLeft = closest.x + 12;
                var containerW = canvas.clientWidth;
                if (tLeft + 160 > containerW) tLeft = closest.x - 170;
                tooltip.style.left = tLeft + 'px';
                tooltip.style.top = (closest.yRemaining - 10) + 'px';
                tooltip.innerHTML = '<strong>' + closest.date + '</strong>'
                    + '<br><span style="color:#ef4444">Remaining: ' + closest.remaining + '</span>'
                    + '<br><span style="color:#10b981">Done: ' + closest.done + '</span>'
                    + '<br><span style="opacity:0.7">Total: ' + closest.total + '</span>';
            } else {
                tooltip.style.display = 'none';
            }
        });
        canvas.addEventListener('mouseleave', function() {
            if (tooltip) tooltip.style.display = 'none';
        });
    }
};

App.drawVelocityChart = function(canvasId, tooltipId, velocity) {
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

    if (!velocity || velocity.length === 0) {
        ctx.fillStyle = textSec;
        ctx.font = '14px ' + font;
        ctx.textAlign = 'center';
        ctx.textBaseline = 'middle';
        ctx.fillText('No velocity data yet', w / 2, h / 2);
        return;
    }

    var pad = { top: 28, right: 24, bottom: 48, left: 44 };
    var plotW = w - pad.left - pad.right;
    var plotH = h - pad.top - pad.bottom;
    var maxY = Math.max.apply(null, velocity.map(function(v) { return v.completed; })) || 1;
    maxY = Math.ceil(maxY * 1.2);
    if (maxY < 1) maxY = 1;

    // Grid lines
    var gridCount = Math.min(maxY, 5);
    var gridStep = Math.max(1, Math.ceil(maxY / gridCount));
    ctx.strokeStyle = border;
    ctx.lineWidth = 0.5;
    ctx.setLineDash([4, 4]);
    for (var gl = gridStep; gl <= maxY; gl += gridStep) {
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

    // Zero label
    ctx.fillStyle = textSec;
    ctx.font = '10px ' + font;
    ctx.textAlign = 'right';
    ctx.textBaseline = 'middle';
    ctx.fillText('0', pad.left - 8, pad.top + plotH);

    // Average velocity line
    var totalCompleted = velocity.reduce(function(a, v) { return a + v.completed; }, 0);
    var avgVelocity = totalCompleted / velocity.length;

    // Draw bars
    var barGap = Math.max(2, Math.floor(plotW * 0.02));
    var barW = Math.max(4, (plotW - barGap * (velocity.length + 1)) / velocity.length);
    if (barW > 60) barW = 60;
    var totalBarsWidth = velocity.length * barW + (velocity.length - 1) * barGap;
    var barStartX = pad.left + (plotW - totalBarsWidth) / 2;

    var barPoints = velocity.map(function(v, i) {
        var x = barStartX + i * (barW + barGap);
        var barH = (v.completed / maxY) * plotH;
        var y = pad.top + plotH - barH;
        return { x: x, y: y, barH: barH, week: v.week, completed: v.completed };
    });

    barPoints.forEach(function(bp) {
        var barGrad = ctx.createLinearGradient(bp.x, bp.y, bp.x, pad.top + plotH);
        barGrad.addColorStop(0, '#3b82f6');
        barGrad.addColorStop(1, 'rgba(59, 130, 246, 0.4)');
        ctx.fillStyle = barGrad;
        // Rounded top corners
        var radius = Math.min(4, barW / 2);
        ctx.beginPath();
        ctx.moveTo(bp.x + radius, bp.y);
        ctx.lineTo(bp.x + barW - radius, bp.y);
        ctx.quadraticCurveTo(bp.x + barW, bp.y, bp.x + barW, bp.y + radius);
        ctx.lineTo(bp.x + barW, pad.top + plotH);
        ctx.lineTo(bp.x, pad.top + plotH);
        ctx.lineTo(bp.x, bp.y + radius);
        ctx.quadraticCurveTo(bp.x, bp.y, bp.x + radius, bp.y);
        ctx.closePath();
        ctx.fill();
    });

    // Average velocity line
    if (avgVelocity > 0) {
        var avgY = pad.top + plotH - (avgVelocity / maxY * plotH);
        ctx.beginPath();
        ctx.moveTo(pad.left, avgY);
        ctx.lineTo(pad.left + plotW, avgY);
        ctx.strokeStyle = '#f59e0b';
        ctx.lineWidth = 1.5;
        ctx.setLineDash([6, 4]);
        ctx.stroke();
        ctx.setLineDash([]);
        // Label
        ctx.fillStyle = '#f59e0b';
        ctx.font = '9px ' + font;
        ctx.textAlign = 'right';
        ctx.fillText('avg: ' + avgVelocity.toFixed(1), pad.left + plotW, avgY - 4);
    }

    // X-axis labels
    var labelStep = Math.max(1, Math.floor(velocity.length / 10));
    ctx.textAlign = 'center';
    ctx.textBaseline = 'top';
    barPoints.forEach(function(bp, i) {
        if (i % labelStep === 0 || i === velocity.length - 1) {
            ctx.fillStyle = textSec;
            ctx.font = '9px ' + font;
            var label = bp.week;
            // Shorten "2025-W03" to "W03"
            var wIdx = label.indexOf('W');
            if (wIdx > 0) label = label.substring(wIdx);
            ctx.save();
            ctx.translate(bp.x + barW / 2, pad.top + plotH + 8);
            ctx.rotate(-0.4);
            ctx.fillText(label, 0, 0);
            ctx.restore();
        }
    });

    // Legend
    ctx.font = '10px ' + font;
    ctx.textAlign = 'left';
    ctx.textBaseline = 'middle';
    var legX = pad.left + 8;
    var legY = pad.top + 4;
    ctx.fillStyle = '#3b82f6';
    ctx.fillRect(legX, legY - 4, 12, 8);
    ctx.fillStyle = textSec;
    ctx.fillText('Completed', legX + 16, legY);
    ctx.beginPath();
    ctx.moveTo(legX + 90, legY);
    ctx.lineTo(legX + 110, legY);
    ctx.strokeStyle = '#f59e0b';
    ctx.lineWidth = 1.5;
    ctx.setLineDash([6, 4]);
    ctx.stroke();
    ctx.setLineDash([]);
    ctx.fillStyle = textSec;
    ctx.fillText('Avg', legX + 114, legY);

    // Store for tooltip
    canvas._barPoints = barPoints;
    canvas._barW = barW;
    if (!canvas._tooltipBound) {
        canvas._tooltipBound = true;
        var tooltip = document.getElementById(tooltipId);
        canvas.addEventListener('mousemove', function(e) {
            if (!tooltip || !canvas._barPoints) return;
            var rect = canvas.getBoundingClientRect();
            var mx = e.clientX - rect.left;
            var my = e.clientY - rect.top;
            var closest = null;
            canvas._barPoints.forEach(function(bp) {
                if (mx >= bp.x && mx <= bp.x + canvas._barW && my >= bp.y && my <= pad.top + plotH) {
                    closest = bp;
                }
            });
            if (closest) {
                tooltip.style.display = 'block';
                var tLeft = closest.x + canvas._barW + 8;
                var containerW = canvas.clientWidth;
                if (tLeft + 140 > containerW) tLeft = closest.x - 150;
                tooltip.style.left = tLeft + 'px';
                tooltip.style.top = (closest.y - 10) + 'px';
                tooltip.innerHTML = '<strong>' + closest.week + '</strong>'
                    + '<br><span style="color:#3b82f6">Completed: ' + closest.completed + '</span>';
            } else {
                tooltip.style.display = 'none';
            }
        });
        canvas.addEventListener('mouseleave', function() {
            if (tooltip) tooltip.style.display = 'none';
        });
    }
};

App.drawStatsCharts = function() {
    var data = App._statsData;
    if (!data) return;
    var cs = data.cycle_stats || {};
    var scores = cs.scores_over_time || [];
    App.drawScoreTrendChart('scoreTrendCanvas', scores);
    App.drawCycleTypeChart('cycleTypeCanvas', scores);

    // Fetch burndown data and draw progress charts
    App.api('stats/burndown').then(function(burndown) {
        if (burndown) {
            App.drawBurndownChart('burndownCanvas', 'burndownCanvasTooltip', burndown.points || []);
            App.drawVelocityChart('velocityCanvas', 'velocityCanvasTooltip', burndown.velocity || []);
        }
    }).catch(function() {
        // Silently handle errors — charts will show "No data" state
    });
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

    // Build QA gate status indicator
    var gateSteps = ['draft', 'planning', 'implementing', 'agent-qa', 'human-qa', 'done'];
    var currentIdx = gateSteps.indexOf(f.status || 'human-qa');
    if (currentIdx < 0) currentIdx = 4;
    var gateHtml = '<div class="qa-gate-status">'
        + '<div class="qa-gate-label">QA Gate Progress</div>'
        + '<div class="qa-gate-steps">';
    for (var gi = 0; gi < gateSteps.length; gi++) {
        var stepCls = gi < currentIdx ? 'completed' : gi === currentIdx ? 'current' : 'upcoming';
        var stepLabel = gateSteps[gi].replace('-', ' ');
        gateHtml += '<div class="qa-gate-step ' + stepCls + '" title="' + stepLabel + '">'
            + '<div class="qa-gate-dot"></div>'
            + '<div class="qa-gate-step-label">' + stepLabel + '</div>'
            + '</div>';
        if (gi < gateSteps.length - 1) {
            gateHtml += '<div class="qa-gate-connector ' + (gi < currentIdx ? 'completed' : '') + '"></div>';
        }
    }
    gateHtml += '</div></div>';

    // Build expandable spec section
    var specHtml = '';
    if (f.spec) {
        var specId = 'qaSpec_' + id.replace(/[^a-zA-Z0-9_-]/g, '_');
        specHtml = '<div class="qa-spec-section">'
            + '<button class="qa-spec-toggle" aria-expanded="false" aria-controls="' + specId + '" type="button">'
            +   '<span class="qa-spec-toggle-icon">▶</span>'
            +   '<span>Feature Spec</span>'
            + '</button>'
            + '<div class="qa-spec-content" id="' + specId + '">'
            +   '<pre class="qa-spec-text">' + App._esc(f.spec) + '</pre>'
            + '</div>'
            + '</div>';
    }

    var html = '<div class="qa-review-card" data-qa-feature="' + id + '">'
        + '<div class="qa-card-header">'
        +   '<span class="qa-card-title">' + name + '</span>'
        +   '<div class="qa-card-badges">'
        +     '<span class="badge badge-human-qa">awaiting QA</span>'
        +     '<span class="qa-priority-badge ' + priorityClass + '">P' + f.priority + ' ' + priorityLabel + '</span>'
        +   '</div>'
        + '</div>'
        + gateHtml
        + '<div class="qa-card-description">' + desc + '</div>'
        + specHtml
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
        + '<div class="qa-notes-section">'
        +   '<label class="qa-notes-label" for="qaNotes_' + id + '">Review Notes</label>'
        +   '<textarea class="qa-notes" id="qaNotes_' + id + '" placeholder="Add review notes or feedback…" aria-label="Review notes for ' + name + '"></textarea>'
        + '</div>'
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
            + '<span class="qa-empty-stat qa-empty-stat--divider"></span>'
            + '<span class="qa-empty-stat"><span class="qa-reviewed-icon rejected" style="width:18px;height:18px;font-size:0.6rem;display:inline-flex">✗</span> ' + rejectedCount + ' rejected</span>'
            + '</div>';
    }

    return header
        + '<div class="qa-layout qa-layout--empty">'
        + '<div>'
        +   '<div class="qa-empty-hero">'
        +     '<div class="qa-empty-icon">✅</div>'
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
    var then = parseUTC(iso).getTime();
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
    return parseUTC(iso).toLocaleDateString('en-US', { month: 'short', day: 'numeric' });
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

    // Expandable spec toggles
    document.querySelectorAll('.qa-spec-toggle').forEach(function(btn) {
        btn.addEventListener('click', function() {
            var content = btn.nextElementSibling;
            var isExpanded = btn.getAttribute('aria-expanded') === 'true';
            btn.setAttribute('aria-expanded', String(!isExpanded));
            content.classList.toggle('qa-spec-content--open');
            btn.querySelector('.qa-spec-toggle-icon').textContent = isExpanded ? '▶' : '▼';
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

