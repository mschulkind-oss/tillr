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

