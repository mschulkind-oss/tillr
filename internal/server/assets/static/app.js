/* Lifecycle Web Viewer — Application */

const App = {
    currentPage: 'dashboard',

    async init() {
        this.bindNavigation();
        this.bindThemeToggle();
        this.bindHamburger();
        this.loadTheme();
        this.connectWebSocket();
        await this.navigate('dashboard');
    },

    connectWebSocket() {
        const proto = location.protocol === 'https:' ? 'wss:' : 'ws:';
        const url = `${proto}//${location.host}/ws`;
        this._ws = new WebSocket(url);
        this._ws.onmessage = (event) => {
            try {
                const msg = JSON.parse(event.data);
                if (msg.type === 'refresh') {
                    this.navigate(this.currentPage);
                }
            } catch { /* ignore non-JSON */ }
        };
        this._ws.onclose = () => {
            setTimeout(() => this.connectWebSocket(), 3000);
        };
        this._ws.onerror = () => {
            this._ws.close();
        };
    },

    bindNavigation() {
        document.querySelectorAll('.nav-link').forEach(link => {
            link.addEventListener('click', (e) => {
                e.preventDefault();
                this.navigate(link.dataset.page);
                this.closeSidebar();
            });
        });
    },

    bindThemeToggle() {
        document.getElementById('themeToggle').addEventListener('click', () => {
            const html = document.documentElement;
            const next = html.getAttribute('data-theme') === 'dark' ? 'light' : 'dark';
            html.setAttribute('data-theme', next);
            localStorage.setItem('lifecycle-theme', next);
            this.updateThemeIcons(next);
            this.updateThemeAriaLabel(next);
        });
    },

    bindHamburger() {
        const hamburger = document.getElementById('hamburger');
        const overlay = document.getElementById('sidebarOverlay');
        if (hamburger) {
            hamburger.addEventListener('click', () => this.toggleSidebar());
        }
        if (overlay) {
            overlay.addEventListener('click', () => this.closeSidebar());
        }
    },

    toggleSidebar() {
        const sidebar = document.getElementById('sidebar');
        const hamburger = document.getElementById('hamburger');
        const overlay = document.getElementById('sidebarOverlay');
        const isOpen = sidebar.classList.toggle('open');
        hamburger.classList.toggle('active', isOpen);
        hamburger.setAttribute('aria-expanded', isOpen);
        overlay.classList.toggle('visible', isOpen);
    },

    closeSidebar() {
        const sidebar = document.getElementById('sidebar');
        const hamburger = document.getElementById('hamburger');
        const overlay = document.getElementById('sidebarOverlay');
        sidebar.classList.remove('open');
        hamburger.classList.remove('active');
        hamburger.setAttribute('aria-expanded', 'false');
        overlay.classList.remove('visible');
    },

    loadTheme() {
        const saved = localStorage.getItem('lifecycle-theme') || 'dark';
        document.documentElement.setAttribute('data-theme', saved);
        this.updateThemeIcons(saved);
        this.updateThemeAriaLabel(saved);
    },

    updateThemeAriaLabel(theme) {
        const btn = document.getElementById('themeToggle');
        if (btn) btn.setAttribute('aria-label', theme === 'dark' ? 'Switch to light theme' : 'Switch to dark theme');
    },

    updateThemeIcons(theme) {
        const icons = document.querySelectorAll('.theme-toggle-icon');
        if (icons.length === 2) {
            icons[0].classList.toggle('dim', theme === 'light');
            icons[1].classList.toggle('dim', theme === 'dark');
        }
    },

    async navigate(page) {
        this.currentPage = page;
        document.querySelectorAll('.nav-link').forEach(l => {
            const isActive = l.dataset.page === page;
            l.classList.toggle('active', isActive);
            if (isActive) l.setAttribute('aria-current', 'page');
            else l.removeAttribute('aria-current');
        });
        document.title = page.charAt(0).toUpperCase() + page.slice(1) + ' — Lifecycle';
        const content = document.getElementById('content');
        if (content.children.length) {
            content.style.transition = 'opacity 0.15s ease';
            content.style.opacity = '0';
            await new Promise(r => setTimeout(r, 150));
        }
        content.style.transition = 'none';
        content.style.opacity = '1';
        content.innerHTML = this.renderSkeleton();
        try {
            const html = await this.renderPage(page);
            content.innerHTML = html;
            this.applyStaggerAnimation(content);
            this.animateProgressBars(content);
            this.animateStatValues(content);
            this.bindPageEvents(page);
            content.style.transition = '';
            content.style.opacity = '';
        } catch (err) {
            content.innerHTML = `<div class="empty-state empty-state--error">
                <div class="empty-state-icon">🔌</div>
                <div class="empty-state-text">Couldn't load this page</div>
                <div class="empty-state-hint">${esc(err.message)}</div>
                <button class="empty-state-retry" onclick="App.navigate('${page}')">↻ Try again</button>
            </div>`;
            content.style.transition = '';
            content.style.opacity = '';
        }
    },

    renderSkeleton() {
        return `<div style="padding:20px 0">
            <div class="skeleton skeleton-text" style="width:200px;height:24px;margin-bottom:24px"></div>
            <div class="stats-grid">
                <div class="skeleton skeleton-stat"></div><div class="skeleton skeleton-stat"></div>
                <div class="skeleton skeleton-stat"></div><div class="skeleton skeleton-stat"></div>
            </div>
            <div class="skeleton skeleton-card"></div>
            <div class="skeleton skeleton-card"></div>
        </div>`;
    },

    applyStaggerAnimation(container) {
        const items = container.querySelectorAll('.card, .stat-card, .roadmap-item, .kanban-card, .roadmap-section');
        items.forEach((el, i) => { el.style.animationDelay = `${Math.min(i * 0.06, 0.42)}s`; });
    },

    animateProgressBars(container) {
        container.querySelectorAll('.progress-fill').forEach(bar => {
            const w = bar.style.width;
            bar.style.transition = 'none';
            bar.style.width = '0';
            void bar.offsetHeight;
            bar.style.transition = '';
            bar.style.width = w;
        });
    },

    animateStatValues(container) {
        container.querySelectorAll('.stat-value').forEach(el => {
            const target = parseInt(el.textContent, 10);
            if (isNaN(target) || target <= 0) return;
            const dur = 600, start = performance.now();
            el.textContent = '0';
            const tick = (now) => {
                const t = Math.min((now - start) / dur, 1);
                el.textContent = Math.round(target * (1 - Math.pow(1 - t, 3)));
                if (t < 1) requestAnimationFrame(tick);
                else el.classList.add('counted');
            };
            requestAnimationFrame(tick);
        });
    },

    async renderPage(page) {
        switch (page) {
            case 'dashboard': return this.renderDashboard();
            case 'features': return this.renderFeatures();
            case 'roadmap': return this.renderRoadmap();
            case 'cycles': return this.renderCycles();
            case 'history': return this.renderHistory();
            case 'discussions': return this.renderDiscussions();
            case 'qa': return this.renderQA();
            default: return `<div class="empty-state">
                <div class="empty-state-icon">🧭</div>
                <div class="empty-state-text">Page not found</div>
                <div class="empty-state-hint">This page doesn't exist. Try one of the links in the sidebar.</div>
            </div>`;
        }
    },

    async api(endpoint) {
        const resp = await fetch('/api/' + endpoint);
        if (!resp.ok) throw new Error('API error: ' + resp.status);
        return resp.json();
    },

    async apiPost(endpoint, body) {
        const resp = await fetch('/api/' + endpoint, {
            method: 'POST',
            headers: { 'Content-Type': 'application/json' },
            body: JSON.stringify(body),
        });
        return resp.json();
    },

    // ── Dashboard ──
    async renderDashboard() {
        const [status, features, milestones, roadmap, cycles, discussions] = await Promise.all([
            this.api('status'), this.api('features'), this.api('milestones'),
            this.api('roadmap'), this.api('cycles'),
            this.api('discussions').catch(() => []),
        ]);
        const counts = status.feature_counts || {};
        const total = Object.values(counts).reduce((a, b) => a + b, 0);

        if (total === 0 && milestones.length === 0) {
            return `<div class="page-header"><h2 class="page-title">${esc(status.project?.name || 'Project')} Dashboard</h2><p class="page-subtitle">Project overview and health at a glance</p></div>
                <div class="empty-state">
                    <div class="empty-state-icon">🚀</div>
                    <div class="empty-state-text">Welcome to your project!</div>
                    <div class="empty-state-hint">Start building by adding your first feature and milestone.</div>
                    <div class="empty-state-cta"><span class="cta-icon">$</span> lifecycle feature add &lt;name&gt;</div>
                </div>`;
        }

        const statuses = ['draft', 'planning', 'implementing', 'agent-qa', 'human-qa', 'done', 'blocked'];
        const statusLabels = {draft:'Draft',planning:'Planning',implementing:'Implementing','agent-qa':'Agent QA','human-qa':'Human QA',done:'Done',blocked:'Blocked'};
        const kanbanCols = statuses.map(s => {
            const items = features.filter(f => f.status === s);
            return `<div class="kanban-column kanban-column-${s}">
                <div class="kanban-header"><span class="kanban-title">${statusLabels[s]||s}</span><span class="kanban-count">${items.length}</span></div>
                ${items.map(f => `<div class="kanban-card" data-status="${s}" data-feature-name="${esc(f.name)}" title="${esc(f.name)}"><div class="kanban-card-title">${esc(f.name)}</div><div class="kanban-card-meta"><span class="kanban-card-priority p${f.priority}"></span>P${f.priority}${f.milestone_name ? ' · ' + esc(f.milestone_name) : ''}</div></div>`).join('') || '<div class="kanban-empty"><div class="kanban-empty-icon">○</div>No items</div>'}
            </div>`;
        }).join('');

        const milestoneCards = milestones.length ? milestones.map(m => {
            const done = m.done_features || 0;
            const mtotal = m.total_features || 0;
            const pct = mtotal > 0 ? Math.round((done / mtotal) * 100) : 0;
            return `<div class="card" style="cursor:pointer" data-milestone="${esc(m.name)}"><div class="card-header"><span class="card-title">${esc(m.name)}</span><span class="badge badge-${m.status}">${m.status}</span></div>
                <div class="progress-bar" role="progressbar" aria-valuenow="${pct}" aria-valuemin="0" aria-valuemax="100" aria-label="${esc(m.name)} progress"><div class="progress-fill ${pct===100?'success':''}" style="width:${pct}%"></div></div>
                <div style="font-size:0.8rem;color:var(--text-muted)">${done}/${mtotal} features · ${pct}%</div></div>`;
        }).join('') : `<div class="empty-state empty-state--compact">
            <div class="empty-state-icon">🏔️</div>
            <div class="empty-state-text">No milestones yet</div>
            <div class="empty-state-cta"><span class="cta-icon">$</span> lifecycle milestone add &lt;name&gt;</div>
        </div>`;

        const recentEvents = (status.recent_events || []).slice(0, 8);
        const events = recentEvents.length ? recentEvents.map(e => `
            <div class="activity-item" ${e.feature_id ? `data-feature-id="${esc(e.feature_id)}" style="cursor:pointer"` : ''}>
                <div class="activity-icon">${eventIcon(e.event_type)}</div>
                <div class="activity-content">
                    <div class="activity-text">${fmtEvent(e.event_type)}${e.feature_id ? ' <span class="feature-badge">' + esc(e.feature_id) + '</span>' : ''}</div>
                    <div class="activity-time">${fmtTime(e.created_at)}</div>
                </div>
            </div>`).join('') : `<div class="empty-state empty-state--compact">
                <div class="empty-state-icon">⏳</div>
                <div class="empty-state-text">No activity yet</div>
                <div class="empty-state-hint">Events will appear here as you work on features.</div>
            </div>`;

        // Roadmap highlights — top items by priority
        const topRoadmap = (roadmap || []).slice(0, 6);
        const roadmapPreview = topRoadmap.length ? topRoadmap.map((r, i) => {
            const priColors = {critical:'var(--danger)',high:'var(--warning)',medium:'var(--accent)',low:'var(--success)','nice-to-have':'var(--purple)'};
            const stIcons = {proposed:'○','accepted':'◐','in-progress':'◑',completed:'●',deferred:'◌'};
            return `<div style="display:flex;align-items:center;gap:10px;padding:6px 0;border-bottom:1px solid var(--border)">
                <span style="color:${priColors[r.priority]||'var(--text-muted)'};font-size:0.7rem;font-weight:700;min-width:18px;text-align:center">${i+1}</span>
                <span style="font-size:0.85rem;flex:1;min-width:0;overflow:hidden;text-overflow:ellipsis;white-space:nowrap">${esc(r.title)}</span>
                <span style="font-size:0.7rem;color:var(--text-muted)">${stIcons[r.status]||'○'} ${r.status}</span>
                ${r.effort ? `<span class="effort-badge effort-${r.effort}">${r.effort.toUpperCase()}</span>` : ''}
            </div>`;
        }).join('') : '<div style="color:var(--text-muted);font-size:0.8rem;padding:8px 0">No roadmap items yet</div>';

        // Active cycles
        const activeCycles = (cycles || []).filter(c => c.status === 'active');
        const cycleCards = activeCycles.length ? activeCycles.map(c => {
            const steps = c.config?.steps || [];
            const currentIdx = steps.indexOf(c.current_step);
            const progress = steps.length > 0 ? Math.round(((currentIdx + 1) / steps.length) * 100) : 0;
            return `<div style="padding:6px 0;border-bottom:1px solid var(--border)">
                <div style="display:flex;align-items:center;gap:8px;margin-bottom:4px">
                    <span style="font-size:0.85rem;font-weight:600;flex:1">${esc(c.feature_id)}</span>
                    <span class="cycle-type-name">${esc(c.cycle_type)}</span>
                </div>
                <div style="display:flex;align-items:center;gap:8px">
                    <div class="progress-bar" style="flex:1"><div class="progress-fill" style="width:${progress}%"></div></div>
                    <span style="font-size:0.7rem;color:var(--text-muted)">${c.current_step} (${currentIdx+1}/${steps.length})</span>
                </div>
            </div>`;
        }).join('') : '<div style="color:var(--text-muted);font-size:0.8rem;padding:8px 0">No active cycles</div>';

        // Priority distribution mini-chart
        const priCounts = {};
        features.forEach(f => { priCounts[f.priority] = (priCounts[f.priority]||0) + 1; });
        const priLabels = {1:'Critical',2:'High',3:'Medium',4:'Low',5:'Nice to have'};
        const priColors = {1:'var(--danger)',2:'var(--warning)',3:'var(--accent)',4:'var(--success)',5:'var(--purple)'};
        const priChart = Object.keys(priLabels).map(p => {
            const count = priCounts[p] || 0;
            const pct = total > 0 ? Math.round((count/total)*100) : 0;
            return `<div style="display:flex;align-items:center;gap:8px;padding:3px 0">
                <span style="font-size:0.7rem;color:var(--text-muted);min-width:70px">${priLabels[p]}</span>
                <div style="flex:1;height:8px;background:var(--bg-tertiary);border-radius:4px;overflow:hidden"><div style="height:100%;width:${pct}%;background:${priColors[p]};border-radius:4px;transition:width 0.6s ease"></div></div>
                <span style="font-size:0.7rem;color:var(--text-muted);min-width:20px;text-align:right">${count}</span>
            </div>`;
        }).join('');

        // Project stats
        const totalEvents = (status.recent_events || []).length;
        const totalDiscussions = (discussions || []).length;
        const allScores = [];
        (cycles || []).forEach(c => { if (c.scores) c.scores.forEach(s => allScores.push(s.score)); });
        const avgCycleScore = allScores.length ? (allScores.reduce((a,b) => a+b, 0) / allScores.length).toFixed(1) : null;
        const withSpec = features.filter(f => f.spec && f.spec.trim()).length;
        const withoutSpec = total - withSpec;
        const statsCard = `<div class="card"><div class="card-title" style="margin-bottom:8px">📊 Project Stats</div>
            <div class="project-stats-grid">
                <div class="project-stat-item"><span class="project-stat-value">${totalEvents}</span><span class="project-stat-label">Total Events</span></div>
                <div class="project-stat-item"><span class="project-stat-value">${totalDiscussions}</span><span class="project-stat-label">Discussions</span></div>
                <div class="project-stat-item"><span class="project-stat-value">${avgCycleScore ?? '—'}</span><span class="project-stat-label">Avg Cycle Score</span></div>
                <div class="project-stat-item"><span class="project-stat-value">${withSpec}/${total}</span><span class="project-stat-label">With Specs</span></div>
            </div>
            ${total > 0 ? `<div style="margin-top:8px"><div style="display:flex;align-items:center;gap:8px;font-size:0.75rem;color:var(--text-muted);margin-bottom:4px"><span>Spec coverage</span><span>${total > 0 ? Math.round((withSpec/total)*100) : 0}%</span></div><div class="progress-bar"><div class="progress-fill${withSpec===total?' success':''}" style="width:${total > 0 ? Math.round((withSpec/total)*100) : 0}%"></div></div></div>` : ''}
        </div>`;

        return `<div class="page-header"><h2 class="page-title">${esc(status.project?.name || 'Project')} Dashboard</h2><p class="page-subtitle">Project overview and health at a glance</p></div>
            <div class="stats-grid">
                <div class="stat-card stat-card--accent"><div class="stat-card-info"><div class="stat-value">${total}</div><div class="stat-label">Total Features</div></div><div class="stat-icon" aria-hidden="true">📦</div></div>
                <div class="stat-card stat-card--success"><div class="stat-card-info"><div class="stat-value">${counts.done||0}</div><div class="stat-label">Completed</div></div><div class="stat-icon" aria-hidden="true">✅</div></div>
                <div class="stat-card stat-card--warning"><div class="stat-card-info"><div class="stat-value">${counts.implementing||0}</div><div class="stat-label">In Progress</div></div><div class="stat-icon" aria-hidden="true">🔨</div></div>
                <div class="stat-card stat-card--purple"><div class="stat-card-info"><div class="stat-value">${status.active_cycles||0}</div><div class="stat-label">Active Cycles</div></div><div class="stat-icon" aria-hidden="true">🔄</div></div>
            </div>
            ${total > 0 ? this.renderStatusBar(counts, total) : ''}
            <div class="card" style="margin-bottom:12px;overflow:visible"><div class="card-title" style="margin-bottom:10px;font-size:0.95rem">Feature Board</div><div class="kanban">${kanbanCols}</div></div>
            <div class="dashboard-grid">
                <div class="card"><div class="card-title" style="margin-bottom:8px">Milestones</div>${milestoneCards}</div>
                <div class="card"><div class="card-title" style="margin-bottom:8px">Recent Activity</div>${events}</div>
                <div class="card" style="cursor:pointer" onclick="App.navigate('roadmap')"><div class="card-title" style="margin-bottom:8px">📋 Roadmap Highlights</div>${roadmapPreview}</div>
                <div class="card"><div class="card-title" style="margin-bottom:8px">Priority Distribution</div>${priChart}${activeCycles.length ? '<div style="margin-top:12px;border-top:1px solid var(--border);padding-top:8px"><div class="card-title" style="margin-bottom:8px">Active Cycles</div>' + cycleCards + '</div>' : ''}</div>
                ${statsCard}
            </div>`;
    },

    renderStatusBar(counts, total) {
        const segments = [
            { key: 'done', label: 'Done', color: 'var(--success)' },
            { key: 'human-qa', label: 'Human QA', color: 'var(--warning)' },
            { key: 'agent-qa', label: 'Agent QA', color: '#39d2c0' },
            { key: 'implementing', label: 'Building', color: 'var(--accent)' },
            { key: 'planning', label: 'Planning', color: 'var(--purple)' },
            { key: 'draft', label: 'Draft', color: 'var(--text-muted)' },
            { key: 'blocked', label: 'Blocked', color: 'var(--danger)' },
        ].filter(s => counts[s.key] > 0);
        const bar = segments.map(s => {
            const pct = ((counts[s.key] / total) * 100).toFixed(1);
            return `<div class="roadmap-bar-segment" style="width:${pct}%;background:${s.color}" title="${s.label}: ${counts[s.key]}"></div>`;
        }).join('');
        const legend = segments.map(s =>
            `<span class="roadmap-legend-item"><span class="roadmap-legend-dot" style="background:${s.color}"></span><span class="roadmap-legend-label">${s.label}</span><span class="roadmap-legend-count">${counts[s.key]}</span></span>`
        ).join('');
        return `<div class="roadmap-category-chart" style="margin-bottom:12px"><div class="roadmap-bar">${bar}</div><div class="roadmap-legend">${legend}</div></div>`;
    },

    // ── Features ──
    _featuresData: [],
    _featuresFilter: 'all',
    _featuresSearch: '',

    featureProgress(status) {
        const order = { draft: 0, planning: 1, implementing: 2, 'agent-qa': 3, 'human-qa': 4, done: 5, blocked: -1 };
        const step = order[status];
        if (step < 0) return { pct: 0, color: 'var(--danger)' };
        const pct = Math.round((step / 5) * 100);
        const color = pct === 100 ? 'var(--success)' : 'var(--accent)';
        return { pct, color };
    },

    priorityLabel(p) {
        const labels = { 1: 'Critical', 2: 'High', 3: 'Medium', 4: 'Low', 5: 'Nice to have' };
        return labels[p] || `P${p}`;
    },

    buildFeaturesTable(features) {
        if (!features.length) return `<div class="empty-state empty-state--compact">
            <div class="empty-state-icon">🔍</div>
            <div class="empty-state-text">No features match</div>
            <div class="empty-state-hint">Try adjusting your search or filters.</div>
        </div>`;

        const sCounts = {};
        features.forEach(f => { sCounts[f.status] = (sCounts[f.status] || 0) + 1; });
        const parts = [`${features.length} feature${features.length !== 1 ? 's' : ''}`];
        if (sCounts['done']) parts.push(`<span class="sum-done">${sCounts['done']} done</span>`);
        if (sCounts['implementing']) parts.push(`<span class="sum-inprog">${sCounts['implementing']} implementing</span>`);
        if (sCounts['in-progress']) parts.push(`<span class="sum-inprog">${sCounts['in-progress']} in progress</span>`);
        if (sCounts['blocked']) parts.push(`<span class="sum-blocked">${sCounts['blocked']} blocked</span>`);
        const summary = `<div class="features-summary">${parts.join('<span class="sep"> · </span>')}</div>`;

        const rows = features.map(f => {
            const prog = this.featureProgress(f.status);
            const pClass = f.priority <= 5 ? f.priority : 5;
            const desc = f.description ? esc(f.description).substring(0, 80) + (f.description.length > 80 ? '…' : '') : '';
            return `<tr class="ft-row status-${f.status}" data-feature-id="${esc(f.id)}" style="cursor:pointer">
            <td>
                <span class="ft-name">${esc(f.name)}</span>
                <div class="ft-id">${esc(f.id)}</div>
                ${desc ? `<div class="ft-desc" title="${esc(f.description)}">${desc}</div>` : ''}
            </td>
            <td><span class="badge badge-${f.status}">${f.status}</span></td>
            <td><span class="priority-dot p-${pClass}">${this.priorityLabel(f.priority)}</span></td>
            <td>${esc(f.milestone_name||'—')}</td>
            <td>
                <div class="ft-progress-wrap">
                    <div class="ft-progress"><div class="ft-progress-fill" style="width:${prog.pct}%;background:${prog.color}"></div></div>
                    <span>${prog.pct}%</span>
                </div>
            </td>
            <td style="color:var(--text-muted)">${fmtDate(f.created_at)}</td>
        </tr>
        <tr class="ft-detail-row" data-detail-for="${esc(f.id)}" style="display:none">
          <td colspan="6">
            <div class="roadmap-item-details" style="max-height:none;opacity:1;padding:8px 16px">
              <div class="roadmap-detail-row"><span class="roadmap-detail-label">ID</span><span class="roadmap-detail-value roadmap-detail-id">${esc(f.id)}</span></div>
              <div class="roadmap-detail-row"><span class="roadmap-detail-label">Status</span><span class="roadmap-detail-value">${esc(f.status)}</span></div>
              <div class="roadmap-detail-row"><span class="roadmap-detail-label">Priority</span><span class="roadmap-detail-value">${this.priorityLabel(f.priority)}</span></div>
              ${f.milestone_name ? `<div class="roadmap-detail-row"><span class="roadmap-detail-label">Milestone</span><span class="roadmap-detail-value">${esc(f.milestone_name)}</span></div>` : ''}
              ${f.description ? `<div class="roadmap-detail-row"><span class="roadmap-detail-label">Description</span><span class="roadmap-detail-value">${esc(f.description)}</span></div>` : ''}
              ${f.roadmap_item_id ? `<div class="roadmap-detail-row"><span class="roadmap-detail-label">Roadmap Item</span><span class="roadmap-detail-value"><a href="#" class="feature-roadmap-link" data-roadmap-id="${esc(f.roadmap_item_id)}">${esc(f.roadmap_item_id)}</a></span></div>` : ''}
              <div class="roadmap-detail-row"><span class="roadmap-detail-label">Created</span><span class="roadmap-detail-value">${fmtTime(f.created_at)}</span></div>
              ${f.spec ? `<div class="feature-spec-section"><div class="feature-spec-header">Spec</div><pre class="feature-spec-block"><code>${esc(f.spec)}</code></pre></div>` : ''}
              <div class="feature-discussions-section" data-discussions-for="${esc(f.id)}"><div class="feature-discussions-loading" style="font-size:0.8rem;color:var(--text-muted);padding:4px 0">Loading discussions…</div></div>
            </div>
          </td>
        </tr>`;
        }).join('');
        return `${summary}<table class="table"><thead><tr><th>Feature</th><th>Status</th><th>Priority</th><th>Milestone</th><th>Progress</th><th>Created</th></tr></thead><tbody>${rows}</tbody></table>`;
    },

    getFilteredFeatures() {
        let list = this._featuresData;
        if (this._featuresFilter !== 'all') list = list.filter(f => f.status === this._featuresFilter);
        if (this._featuresSearch) {
            const q = this._featuresSearch.toLowerCase();
            list = list.filter(f => (f.name && f.name.toLowerCase().includes(q)) || (f.id && f.id.toLowerCase().includes(q)) || (f.description && f.description.toLowerCase().includes(q)));
        }
        return list;
    },

    async renderFeatures() {
        const features = await this.api('features');
        this._featuresData = features;
        this._featuresFilter = 'all';
        this._featuresSearch = '';

        if (!features.length) return `<div class="page-header"><h2 class="page-title">Features</h2><p class="page-subtitle">Track your project's features through their lifecycle</p></div>
            <div class="empty-state">
                <div class="empty-state-icon">✨</div>
                <div class="empty-state-text">No features yet</div>
                <div class="empty-state-hint">Features are the building blocks of your project. Add one to get started!</div>
                <div class="empty-state-cta"><span class="cta-icon">$</span> lifecycle feature add &lt;name&gt;</div>
            </div>`;

        const statuses = ['all','draft','planning','implementing','agent-qa','human-qa','done','blocked'];
        const counts = {};
        counts.all = features.length;
        features.forEach(f => { counts[f.status] = (counts[f.status] || 0) + 1; });
        const pills = statuses.filter(s => s === 'all' || counts[s]).map(s =>
            `<button class="filter-pill${s==='all'?' active':''}" data-status="${s}">${s === 'all' ? 'All' : s}<span class="pill-count">${counts[s]||0}</span></button>`
        ).join('');

        return `<div class="page-header"><h2 class="page-title">Features</h2><p class="page-subtitle">${features.length} features tracked</p></div>
            <div class="features-toolbar">
                <div class="filter-pills">${pills}</div>
                <div class="features-search-wrap"><input type="text" class="features-search" placeholder="Search features…" id="featuresSearch" aria-label="Search features"></div>
            </div>
            <div class="card" id="featuresTableWrap">${this.buildFeaturesTable(features)}</div>`;
    },

    // ── Roadmap ──
    async renderRoadmap() {
        const items = await this.api('roadmap');
        if (!items.length) return `<div class="page-header"><h2 class="page-title">Roadmap</h2><p class="page-subtitle">Product vision and prioritized backlog</p></div>
            <div class="empty-state">
                <div class="empty-state-icon">🗺️</div>
                <div class="empty-state-text">Your roadmap is wide open</div>
                <div class="empty-state-hint">Chart the course for your project by adding your first roadmap item.</div>
                <div class="empty-state-cta"><span class="cta-icon">$</span> lifecycle roadmap add &lt;title&gt;</div>
            </div>`;

        this._roadmapData = items;
        if (!this.roadmapFilters) this.roadmapFilters = { category: 'all', status: 'all' };

        const pris = ['critical','high','medium','low','nice-to-have'];
        const icons = {critical:'🔴',high:'🟠',medium:'🟡',low:'🟢','nice-to-have':'🔵'};
        const grouped = {};
        items.forEach(r => { (grouped[r.priority] = grouped[r.priority] || []).push(r); });

        const sCounts = {};
        items.forEach(r => { sCounts[r.status] = (sCounts[r.status] || 0) + 1; });
        const done = sCounts['done'] || 0;
        const inProg = sCounts['in-progress'] || 0;
        const accepted = sCounts['accepted'] || 0;
        const pct = items.length > 0 ? Math.round((done / items.length) * 100) : 0;

        const catCounts = {};
        items.forEach(r => { const c = r.category || 'uncategorized'; catCounts[c] = (catCounts[c] || 0) + 1; });
        const catBarColors = {0:'var(--accent)',1:'var(--purple)',2:'var(--warning)',3:'var(--success)',4:'var(--danger)',5:'#38d4c7'};

        function catCls(c) {
            if (!c) return 'roadmap-cat-0';
            let h = 0;
            for (let i = 0; i < c.length; i++) h = c.charCodeAt(i) + ((h << 5) - h);
            return 'roadmap-cat-' + (Math.abs(h) % 6);
        }

        // Build filter bar
        const categories = [...new Set(items.map(r => r.category || 'uncategorized'))].sort();
        const catFilterCounts = { all: items.length };
        categories.forEach(c => { catFilterCounts[c] = catCounts[c] || 0; });
        const catPills = ['all', ...categories].map(c => {
            const active = this.roadmapFilters.category === c ? ' active' : '';
            const label = c === 'all' ? 'All' : esc(c);
            return `<button class="roadmap-filter-pill${active}" data-filter-type="category" data-filter-value="${esc(c)}">${label}<span class="pill-count">${catFilterCounts[c]}</span></button>`;
        }).join('');

        const statusOrder = ['proposed','accepted','in-progress','done','deferred'];
        const statusFilterCounts = { all: items.length };
        items.forEach(r => { statusFilterCounts[r.status] = (statusFilterCounts[r.status] || 0) + 1; });
        const statusLabels = { all:'All', proposed:'Proposed', accepted:'Accepted', 'in-progress':'In Progress', done:'Completed', deferred:'Deferred' };
        const stPills = ['all', ...statusOrder].filter(s => s === 'all' || statusFilterCounts[s]).map(s => {
            const active = this.roadmapFilters.status === s ? ' active' : '';
            return `<button class="roadmap-filter-pill${active}" data-filter-type="status" data-filter-value="${s}">${statusLabels[s] || s}<span class="pill-count">${statusFilterCounts[s]}</span></button>`;
        }).join('');

        const filterBar = `<div class="roadmap-filters">
            <div class="roadmap-filter-group"><span class="roadmap-filter-label">Category</span><div class="roadmap-filter-pills">${catPills}</div></div>
            <div class="roadmap-filter-group"><span class="roadmap-filter-label">Status</span><div class="roadmap-filter-pills">${stPills}</div></div>
        </div>`;

        let rank = 0;
        const sections = pris.filter(p => grouped[p]).map(pri => {
            const ritems = grouped[pri];
            return `<div class="roadmap-section pri-${pri}">
                <div class="roadmap-priority-header pri-${pri}"><span class="roadmap-priority-icon" aria-hidden="true">${icons[pri]}</span><span class="roadmap-priority-label">${pri.replace('-',' ')}</span><span class="roadmap-priority-count">${ritems.length} item${ritems.length !== 1 ? 's' : ''}</span></div>
                <div class="roadmap-items" role="list">${ritems.map((r,i) => {
                    rank++;
                    const itemCat = r.category || 'uncategorized';
                    return `<div class="roadmap-item st-${r.status}" role="listitem" tabindex="0" data-category="${esc(itemCat)}" data-status="${r.status}" style="animation-delay:${i*0.06}s">
                        <div class="roadmap-item-number">${rank}</div>
                        <div class="roadmap-item-content"><div class="roadmap-item-title">${esc(r.title)}</div>${r.description?`<div class="roadmap-item-desc">${esc(r.description)}</div>`:''}</div>
                        <div class="roadmap-item-meta">${r.category?`<span class="roadmap-category ${catCls(r.category)}">${esc(r.category)}</span>`:''}${r.effort?`<span class="effort-badge effort-${r.effort}">${{xs:'🟢 XS',s:'🔵 S',m:'🟡 M',l:'🟠 L',xl:'🔴 XL'}[r.effort]||r.effort}</span>`:''}<span class="badge badge-${r.status}">${r.status}</span></div>
                    </div>`;
                }).join('')}</div>
            </div>`;
        }).join('');

        const catEntries = Object.entries(catCounts).sort((a,b) => b[1] - a[1]);
        const catBarSegments = catEntries.map(([cat, count]) => {
            const idx = Math.abs(catCls(cat).replace('roadmap-cat-','')) % 6;
            const color = catBarColors[idx];
            const widthPct = ((count / items.length) * 100).toFixed(1);
            return `<div class="roadmap-bar-segment" style="width:${widthPct}%;background:${color}" title="${esc(cat)}: ${count} item${count !== 1 ? 's' : ''} (${widthPct}%)"></div>`;
        }).join('');
        const catLegendItems = catEntries.map(([cat, count]) => {
            const idx = Math.abs(catCls(cat).replace('roadmap-cat-','')) % 6;
            const color = catBarColors[idx];
            return `<div class="roadmap-legend-item"><span class="roadmap-legend-dot" style="background:${color}"></span><span class="roadmap-legend-label">${esc(cat)}</span><span class="roadmap-legend-count">${count}</span></div>`;
        }).join('');
        const categoryChart = `<div class="roadmap-category-chart">
            <div class="roadmap-bar">${catBarSegments}</div>
            <div class="roadmap-legend">${catLegendItems}</div>
        </div>`;

        return `<div class="page-header"><div class="page-header-row"><h2 class="page-title">Roadmap</h2><button class="btn-print" onclick="window.print()" title="Print or save as PDF"><span aria-hidden="true">🖨️</span> Print / Export</button></div><p class="page-subtitle">Strategic priorities and planned work — ranked by impact</p></div>
            <div class="roadmap-summary">
                <div class="roadmap-summary-stat"><div class="roadmap-summary-value">${items.length}</div><div class="roadmap-summary-label">Total Items</div></div>
                <div class="roadmap-summary-stat"><div class="roadmap-summary-value text-warning">${inProg}</div><div class="roadmap-summary-label">In Progress</div></div>
                <div class="roadmap-summary-stat"><div class="roadmap-summary-value text-success">${done}</div><div class="roadmap-summary-label">Completed</div></div>
                <div class="roadmap-summary-stat"><div class="roadmap-summary-value text-accent">${accepted}</div><div class="roadmap-summary-label">Accepted</div></div>
                <div class="roadmap-summary-stat"><div class="roadmap-summary-value text-purple">${pct}%</div><div class="roadmap-summary-label">Progress</div></div>
            </div>${categoryChart}${filterBar}
            <div id="roadmapSections">${sections}</div>
            <div class="roadmap-keyboard-hint" aria-hidden="true">Tip: Use ↑↓ to navigate, Enter to expand</div>`;
    },

    // ── Cycles ──
    async renderCycles() {
        const cycles = await this.api('cycles');

        const ctSteps = {
            'ui-refinement':['design','ux-review','develop','manual-qa','judge'],
            'feature-implementation':['research','develop','agent-qa','judge','human-qa'],
            'roadmap-planning':['research','plan','create-roadmap','prioritize','human-review'],
            'bug-triage':['report','reproduce','root-cause','fix','verify'],
            'documentation':['research','draft','review','edit','publish'],
            'architecture-review':['analyze','propose','discuss','decide','implement'],
            'release':['freeze','qa','fix','staging','verify','ship'],
            'onboarding-dx':['try','friction-log','improve','verify','document'],
        };

        if (!cycles.length) return `<div class="page-header"><h2 class="page-title">Iteration Cycles</h2><p class="page-subtitle">Structured iteration workflows for features</p></div>
            <div class="empty-state">
                <div class="empty-state-icon">🔄</div>
                <div class="empty-state-text">No cycles</div>
                <div class="empty-state-hint">Cycles guide features through structured steps like research, develop, QA, and review.</div>
                <div class="empty-state-cta"><span class="cta-icon">$</span> lifecycle cycle start &lt;type&gt; &lt;feature&gt;</div>
            </div>`;

        // Fetch scores for each cycle
        const scoresMap = {};
        await Promise.all(cycles.map(async c => {
            try {
                scoresMap[c.id] = await this.api(`cycles/${c.id}/scores`);
            } catch { scoresMap[c.id] = []; }
        }));

        const activeCycles = cycles.filter(c => c.status === 'active');
        const completedCycles = cycles.filter(c => c.status !== 'active');

        const renderCycleCard = (c) => {
            const steps = ctSteps[c.cycle_type] || [];
            const totalSteps = steps.length;
            const pct = totalSteps > 0 ? Math.round((c.current_step / totalSteps) * 100) : 0;
            const scores = scoresMap[c.id] || [];
            const avgScore = scores.length ? (scores.reduce((s, x) => s + x.score, 0) / scores.length) : null;
            const scoreCls = avgScore != null ? (avgScore >= 7 ? 'score-high' : avgScore >= 4 ? 'score-mid' : 'score-low') : '';

            const pipeline = steps.map((s, i) => {
                const state = i < c.current_step ? 'done' : i === c.current_step ? 'active' : '';
                const stepScore = scores.find(sc => sc.step === i);
                const indicator = state === 'done' ? '✓' : (i + 1);
                const scoreTag = stepScore ? `<div class="cycle-node-score">${stepScore.score.toFixed(1)}</div>` : '';
                return `<div class="cycle-node ${state}"><div class="cycle-node-indicator">${indicator}</div><div class="cycle-node-label">${s.replace(/-/g, ' ')}</div>${scoreTag}</div>`;
            }).join('');

            // Score sparkline
            let sparkline = '';
            if (scores.length >= 2) {
                const w = 120, h = 32, pad = 2;
                const maxS = 10, minS = 0;
                const points = scores.map((s, i) => {
                    const x = pad + (i / (scores.length - 1)) * (w - 2 * pad);
                    const y = h - pad - ((s.score - minS) / (maxS - minS)) * (h - 2 * pad);
                    return `${x},${y}`;
                }).join(' ');
                sparkline = `<svg class="score-sparkline" width="${w}" height="${h}" viewBox="0 0 ${w} ${h}"><polyline points="${points}" fill="none" stroke="var(--accent)" stroke-width="2"/></svg>`;
            }

            return `<div class="card cycle-card" data-cycle-id="${c.id}">
                <div class="card-header"><span class="card-title">${esc(c.feature_id)}</span><span class="badge badge-${c.status}">${c.status}</span></div>
                <div class="cycle-meta">
                    <span class="cycle-type-name">${c.cycle_type.replace(/-/g, ' ')}</span>
                    <span class="cycle-iteration-badge">⟳ Iteration ${c.iteration}</span>
                    ${avgScore != null ? `<span class="cycle-score ${scoreCls}">★ ${avgScore.toFixed(1)} avg</span>` : ''}
                    <span class="cycle-step-count">${c.current_step}/${totalSteps} steps</span>
                </div>
                <div class="cycle-pipeline">${pipeline}</div>
                <div class="cycle-progress"><div class="cycle-progress-fill" style="width:${pct}%"></div></div>
                ${sparkline ? `<div class="cycle-sparkline-row">${sparkline}<span class="sparkline-label">${scores.length} scores</span></div>` : ''}
            </div>`;
        };

        let html = `<div class="page-header"><h2 class="page-title">Iteration Cycles</h2><p class="page-subtitle">${activeCycles.length} active · ${completedCycles.length} completed</p></div>`;

        if (activeCycles.length) {
            html += `<h3 class="section-title">Active Cycles</h3>` + activeCycles.map(renderCycleCard).join('');
        }
        if (completedCycles.length) {
            html += `<h3 class="section-title" style="margin-top:20px">Completed Cycles</h3>` + completedCycles.map(renderCycleCard).join('');
        }

        // Cycle type reference
        html += `<h3 class="section-title" style="margin-top:20px">Available Cycle Types</h3><div class="cycle-types-grid">`;
        for (const [type, steps] of Object.entries(ctSteps)) {
            html += `<div class="card cycle-type-ref"><div class="card-title">${type.replace(/-/g, ' ')}</div><div class="cycle-type-steps">${steps.map(s => `<span class="cycle-type-step">${s.replace(/-/g,' ')}</span>`).join(' → ')}</div></div>`;
        }
        html += `</div>`;

        return html;
    },

    // ── History ──
    _historyPageSize: 50,

    async renderHistory() {
        const events = await this.api('history');
        if (!events.length) return `<div class="page-header"><h2 class="page-title">History</h2><p class="page-subtitle">Complete event timeline</p></div>
            <div class="empty-state">
                <div class="empty-state-icon">📜</div>
                <div class="empty-state-text">No events recorded yet</div>
                <div class="empty-state-hint">Every action — feature changes, cycle steps, QA decisions — is captured here automatically.</div>
                <div class="empty-state-cta"><span class="cta-icon">$</span> lifecycle feature add &lt;name&gt;</div>
            </div>`;

        this._historyEvents = events;
        this._historyFilter = this._historyFilter || 'all';
        this._historyShown = Math.min(this._historyPageSize, events.length);

        // Categorize events
        const categories = {};
        events.forEach(e => {
            const cat = e.event_type.split('.')[0] || 'other';
            categories[cat] = (categories[cat] || 0) + 1;
        });

        // Feature filter
        const features = [...new Set(events.map(e => e.feature_id).filter(Boolean))];

        const filtered = this._historyFilter === 'all' ? events
            : events.filter(e => e.event_type.startsWith(this._historyFilter) || e.feature_id === this._historyFilter);
        const shown = filtered.slice(0, this._historyShown);
        const hasMore = filtered.length > this._historyShown;

        const filterBtns = [['all', 'All', events.length]].concat(
            Object.entries(categories).sort((a,b) => b[1]-a[1]).map(([k,v]) => [k, k.charAt(0).toUpperCase() + k.slice(1), v])
        );

        return `<div class="page-header"><h2 class="page-title">History</h2><p class="page-subtitle">${events.length} events</p></div>
            <div class="history-filters" id="historyFilters">
                ${filterBtns.map(([id, label, count]) =>
                    `<button class="filter-btn ${this._historyFilter === id ? 'active' : ''}" data-filter="${id}">${label} <span class="filter-count">${count}</span></button>`
                ).join('')}
                ${features.length > 1 ? `<select class="filter-select" id="historyFeatureFilter"><option value="all">All features</option>${features.map(f => `<option value="${f}" ${this._historyFilter === f ? 'selected' : ''}>${f}</option>`).join('')}</select>` : ''}
            </div>
            <div class="card"><div class="timeline" id="historyTimeline">${this.buildHistoryItems(shown, 0)}</div>
            ${hasMore ? `<div class="timeline-load-more-wrap"><button class="timeline-load-more" id="historyLoadMore">Load more (${filtered.length - this._historyShown} remaining)</button></div>` : ''}</div>`;
    },

    buildHistoryItems(events, startIdx) {
        const grouped = {};
        events.forEach(e => {
            const day = e.created_at ? new Date(e.created_at).toLocaleDateString('en-US', { weekday: 'long', month: 'long', day: 'numeric', year: 'numeric' }) : 'Unknown';
            (grouped[day] = grouped[day] || []).push(e);
        });

        let idx = startIdx;
        return Object.entries(grouped).map(([date, items]) => {
            const rows = items.map(e => {
                const delay = Math.min(idx++ * 0.04, 1.2);
                let detailHtml = '';
                if (e.data) {
                    try {
                        const d = typeof e.data === 'string' ? JSON.parse(e.data) : e.data;
                        detailHtml = Object.entries(d).map(([k,v]) =>
                            `<span class="detail-badge"><span class="detail-badge-key">${esc(k)}</span><span class="detail-badge-val">${esc(String(v))}</span></span>`
                        ).join('');
                    } catch(_) { detailHtml = `<span class="detail-badge"><span class="detail-badge-val">${esc(e.data)}</span></span>`; }
                }
                return `<div class="timeline-item ${eventClass(e.event_type)}" style="animation-delay:${delay}s">
                    <div class="timeline-dot">${eventIcon(e.event_type)}</div>
                    <div class="timeline-time">${fmtRelTime(e.created_at)}</div>
                    <div class="timeline-event"><span>${fmtEvent(e.event_type)}</span>${e.feature_id ? '<span class="badge badge-implementing">' + esc(e.feature_id) + '</span>' : ''}</div>
                    ${detailHtml ? `<div class="timeline-detail">${detailHtml}</div>` : ''}
                </div>`;
            }).join('');
            return `<div class="timeline-date-group"><div class="timeline-date-sep"><hr class="timeline-date-line"/><span class="timeline-date-label">${esc(date)}</span><hr class="timeline-date-line"/></div>${rows}</div>`;
        }).join('');
    },

    // ── QA ──
    async renderQA() {
        const [features, history] = await Promise.all([
            this.api('features?status=human-qa'),
            this.api('history'),
        ]);

        const reviewed = (history || []).filter(e =>
            e.event_type === 'qa.approved' || e.event_type === 'qa.rejected'
        ).slice(0, 8);

        const reviewedHtml = reviewed.length
            ? `<div class="card card--flush">${reviewed.map(e => {
                const isApproved = e.event_type === 'qa.approved';
                return `<div class="qa-reviewed-item">
                    <div class="qa-reviewed-icon ${isApproved ? 'approved' : 'rejected'}">${isApproved ? '✓' : '✗'}</div>
                    <div class="qa-reviewed-name">${esc(e.feature_id || 'Unknown')}</div>
                    <div class="qa-reviewed-time">${fmtTime(e.created_at)}</div>
                </div>`;
            }).join('')}</div>`
            : '<div class="empty-state empty-state--compact"><div class="empty-state-icon">📋</div><div class="empty-state-text">No reviews yet</div></div>';

        if (!features.length) {
            return `<div class="page-header"><h2 class="page-title">Quality Assurance</h2><p class="page-subtitle">Review and approve features</p></div>
                <div class="qa-layout">
                    <div>
                        <div class="empty-state" style="padding:40px">
                            <div class="empty-state-icon">🎉</div>
                            <div class="empty-state-text">All clear — nothing to review!</div>
                            <div class="empty-state-hint">Features will appear here when they reach the <code>human-qa</code> stage.</div>
                        </div>
                    </div>
                    <div>
                        <div class="qa-column-title"><span class="qa-column-dot reviewed"></span>Recently Reviewed</div>
                        ${reviewedHtml}
                    </div>
                </div>`;
        }

        const pendingCards = features.map(f => `<div class="qa-review-card">
            <div class="qa-card-header">
                <span class="qa-card-title">${esc(f.name)}</span>
                <span class="badge badge-human-qa">awaiting QA</span>
            </div>
            <div class="qa-card-description">${esc(f.description || 'No description provided')}</div>
            <div class="qa-card-meta">
                <span>🏷️ ${esc(f.id)}</span>
                ${f.milestone_name ? `<span>📌 ${esc(f.milestone_name)}</span>` : ''}
                <span>⚡ P${f.priority}</span>
            </div>
            <textarea class="qa-notes" data-feature="${esc(f.id)}" placeholder="Add review notes (optional)..." aria-label="Review notes for ${esc(f.name)}"></textarea>
            <div class="qa-actions">
                <button class="btn-approve qa-approve" data-feature="${esc(f.id)}" aria-label="Approve ${esc(f.name)}">✓ Approve</button>
                <button class="btn-reject qa-reject" data-feature="${esc(f.id)}" aria-label="Reject ${esc(f.name)}">✗ Reject</button>
            </div>
        </div>`).join('');

        return `<div class="page-header"><h2 class="page-title">Quality Assurance</h2><p class="page-subtitle">Review and approve features</p></div>
            <div class="qa-summary">
                <div class="qa-summary-count">${features.length}</div>
                <div class="qa-summary-label"><strong>${features.length === 1 ? '1 feature' : features.length + ' features'}</strong> pending review</div>
            </div>
            <div class="qa-layout">
                <div>
                    <div class="qa-column-title"><span class="qa-column-dot pending"></span>Pending Review</div>
                    ${pendingCards}
                </div>
                <div>
                    <div class="qa-column-title"><span class="qa-column-dot reviewed"></span>Recently Reviewed</div>
                    ${reviewedHtml}
                </div>
            </div>`;
    },

    // ── Discussions ──
    async renderDiscussions() {
        const discussions = await this.api('discussions');
        if (!discussions.length) return `<div class="page-header"><h2 class="page-title">Discussions</h2><p class="page-subtitle">Project discussions and decisions</p></div>
            <div class="empty-state">
                <div class="empty-state-icon">💬</div>
                <div class="empty-state-text">No discussions yet</div>
                <div class="empty-state-hint">Discussions help track proposals, decisions, and team conversations.</div>
            </div>`;

        this._discussionsData = discussions;

        const statusColors = { open: 'var(--success)', resolved: 'var(--accent)', merged: 'var(--purple)', closed: 'var(--text-muted)' };
        const rows = discussions.map(d => {
            const statusCls = 'disc-status-' + (d.status || 'open');
            return `<tr class="disc-row" data-disc-id="${d.id}" style="cursor:pointer">
                <td><span class="disc-id">#${d.id}</span></td>
                <td><span class="badge ${statusCls}">${esc(d.status || 'open')}</span></td>
                <td><span class="disc-title">${esc(d.title)}</span></td>
                <td>${esc(d.author || '—')}</td>
                <td><span class="disc-comment-count">${d.comment_count || 0}</span></td>
                <td>${d.feature_id ? `<a href="#" class="disc-feature-link" data-feature-id="${esc(d.feature_id)}">${esc(d.feature_id)}</a>` : '—'}</td>
                <td style="color:var(--text-muted)">${fmtDate(d.created_at)}</td>
            </tr>
            <tr class="disc-detail-row" data-disc-detail="${d.id}" style="display:none">
              <td colspan="7">
                <div class="disc-comments-wrap" id="discComments${d.id}">
                  <div style="font-size:0.8rem;color:var(--text-muted);padding:8px">Loading comments…</div>
                </div>
              </td>
            </tr>`;
        }).join('');

        const statusCounts = {};
        discussions.forEach(d => { statusCounts[d.status || 'open'] = (statusCounts[d.status || 'open'] || 0) + 1; });

        return `<div class="page-header"><h2 class="page-title">Discussions</h2><p class="page-subtitle">${discussions.length} discussion${discussions.length !== 1 ? 's' : ''}</p></div>
            <div class="stats-grid" style="margin-bottom:16px">
                <div class="stat-card stat-card--success"><div class="stat-card-info"><div class="stat-value">${statusCounts.open || 0}</div><div class="stat-label">Open</div></div><div class="stat-icon" aria-hidden="true">🟢</div></div>
                <div class="stat-card stat-card--accent"><div class="stat-card-info"><div class="stat-value">${statusCounts.resolved || 0}</div><div class="stat-label">Resolved</div></div><div class="stat-icon" aria-hidden="true">🔵</div></div>
                <div class="stat-card stat-card--purple"><div class="stat-card-info"><div class="stat-value">${statusCounts.merged || 0}</div><div class="stat-label">Merged</div></div><div class="stat-icon" aria-hidden="true">🟣</div></div>
                <div class="stat-card"><div class="stat-card-info"><div class="stat-value">${statusCounts.closed || 0}</div><div class="stat-label">Closed</div></div><div class="stat-icon" aria-hidden="true">⚪</div></div>
            </div>
            <div class="card"><table class="table"><thead><tr><th>ID</th><th>Status</th><th>Title</th><th>Author</th><th>Comments</th><th>Feature</th><th>Created</th></tr></thead><tbody>${rows}</tbody></table></div>`;
    },

    renderDiscussionComments(comments) {
        if (!comments || !comments.length) return '<div style="font-size:0.8rem;color:var(--text-muted);padding:8px">No comments</div>';
        const typeColors = { proposal: 'disc-type-proposal', approval: 'disc-type-approval', objection: 'disc-type-objection', revision: 'disc-type-revision', decision: 'disc-type-decision', comment: 'disc-type-comment' };
        return comments.map(c => {
            const indent = (c.parent_id && c.parent_id > 0) ? ' disc-comment-reply' : '';
            const ctype = c.comment_type || c.type || 'comment';
            const typeCls = typeColors[ctype] || 'disc-type-comment';
            return `<div class="disc-comment${indent}">
                <div class="disc-comment-header">
                    <span class="disc-comment-author">${esc(c.author || 'Unknown')}</span>
                    <span class="badge ${typeCls}">${esc(ctype)}</span>
                    <span class="disc-comment-time">${fmtTime(c.created_at)}</span>
                </div>
                <div class="disc-comment-body">${esc(c.content || '')}</div>
            </div>`;
        }).join('');
    },

    async loadFeatureDiscussions(featureId, container) {
        try {
            const discussions = await this.api('discussions?feature=' + encodeURIComponent(featureId));
            if (!discussions || !discussions.length) {
                container.innerHTML = '';
                return;
            }
            container.innerHTML = `<div class="feature-discussions-header">Linked Discussions</div>
                <div class="feature-discussions-list">${discussions.map(d => {
                    const statusCls = 'disc-status-' + (d.status || 'open');
                    return `<div class="feature-disc-item" data-disc-id="${d.id}" style="cursor:pointer">
                        <span class="badge ${statusCls}">${esc(d.status || 'open')}</span>
                        <span class="feature-disc-title">${esc(d.title)}</span>
                        <span class="disc-comment-count">${d.comment_count || 0} 💬</span>
                    </div>`;
                }).join('')}</div>`;
        } catch {
            container.innerHTML = '';
        }
    },

    bindPageEvents(page) {
        if (page === 'dashboard') {
            document.querySelectorAll('.kanban-card').forEach(card => {
                card.addEventListener('click', () => {
                    const status = card.dataset.status;
                    App._featuresFilter = status;
                    App.navigate('features');
                });
            });
            document.querySelectorAll('.activity-item[data-feature-id]').forEach(item => {
                item.addEventListener('click', () => { App.navigate('features'); });
            });
            document.querySelectorAll('[data-milestone]').forEach(card => {
                card.addEventListener('click', () => { App.navigate('features'); });
            });
        }
        if (page === 'features') {
            const bindFeatureRows = () => {
                document.querySelectorAll('.ft-row').forEach(row => {
                    row.addEventListener('click', () => {
                        const fid = row.dataset.featureId;
                        const detail = document.querySelector(`.ft-detail-row[data-detail-for="${fid}"]`);
                        if (detail) {
                            const isVisible = detail.style.display !== 'none';
                            document.querySelectorAll('.ft-detail-row').forEach(d => d.style.display = 'none');
                            document.querySelectorAll('.ft-row').forEach(r => r.classList.remove('expanded'));
                            if (!isVisible) {
                                detail.style.display = 'table-row';
                                row.classList.add('expanded');
                                const discSection = detail.querySelector(`[data-discussions-for="${fid}"]`);
                                if (discSection) this.loadFeatureDiscussions(fid, discSection);
                            }
                        }
                    });
                });
                document.querySelectorAll('.feature-roadmap-link').forEach(link => {
                    link.addEventListener('click', (e) => { e.preventDefault(); e.stopPropagation(); App.navigate('roadmap'); });
                });
            };
            const refresh = () => {
                const wrap = document.getElementById('featuresTableWrap');
                if (wrap) wrap.innerHTML = this.buildFeaturesTable(this.getFilteredFeatures());
                bindFeatureRows();
            };
            document.querySelectorAll('.filter-pill').forEach(btn => btn.addEventListener('click', () => {
                document.querySelectorAll('.filter-pill').forEach(b => b.classList.remove('active'));
                btn.classList.add('active');
                this._featuresFilter = btn.dataset.status;
                refresh();
            }));
            const searchInput = document.getElementById('featuresSearch');
            if (searchInput) searchInput.addEventListener('input', (e) => { this._featuresSearch = e.target.value; refresh(); });
            bindFeatureRows();
        }
        if (page === 'history') {
            // Filter buttons
            document.querySelectorAll('.filter-btn').forEach(btn => {
                btn.addEventListener('click', () => {
                    this._historyFilter = btn.dataset.filter;
                    this.navigate('history');
                });
            });
            const featureFilter = document.getElementById('historyFeatureFilter');
            if (featureFilter) featureFilter.addEventListener('change', () => {
                this._historyFilter = featureFilter.value;
                this.navigate('history');
            });
            const btn = document.getElementById('historyLoadMore');
            if (btn) btn.addEventListener('click', () => {
                const events = this._historyEvents;
                const filter = this._historyFilter || 'all';
                const filtered = filter === 'all' ? events
                    : events.filter(e => e.event_type.startsWith(filter) || e.feature_id === filter);
                const prev = this._historyShown;
                this._historyShown = Math.min(prev + this._historyPageSize, filtered.length);
                const timeline = document.getElementById('historyTimeline');
                if (timeline) {
                    const tmp = document.createElement('div');
                    tmp.innerHTML = this.buildHistoryItems(filtered.slice(prev, this._historyShown), prev);
                    while (tmp.firstChild) timeline.appendChild(tmp.firstChild);
                }
                const remaining = filtered.length - this._historyShown;
                if (remaining <= 0) {
                    btn.parentElement.remove();
                } else {
                    btn.textContent = `Load more (${remaining} remaining)`;
                }
            });
        }
        if (page === 'roadmap') {
            const toggleItem = (item) => {
                const wasExpanded = item.classList.contains('expanded');
                document.querySelectorAll('.roadmap-item.expanded').forEach(el => {
                    el.classList.remove('expanded');
                    const ch = el.querySelector('.roadmap-item-chevron');
                    if (ch) ch.textContent = '▸';
                });
                if (!wasExpanded) {
                    item.classList.add('expanded');
                    const ch = item.querySelector('.roadmap-item-chevron');
                    if (ch) ch.textContent = '▾';
                }
            };
            document.querySelectorAll('.roadmap-item').forEach(item => {
                item.addEventListener('click', () => toggleItem(item));
            });
            const content = document.getElementById('content');
            if (content) content.addEventListener('keydown', (e) => {
                const items = Array.from(content.querySelectorAll('.roadmap-item'));
                if (!items.length) return;
                const idx = items.indexOf(document.activeElement);
                if (e.key === 'ArrowDown') {
                    e.preventDefault();
                    const next = idx < items.length - 1 ? idx + 1 : 0;
                    items[next].focus();
                } else if (e.key === 'ArrowUp') {
                    e.preventDefault();
                    const prev = idx > 0 ? idx - 1 : items.length - 1;
                    items[prev].focus();
                } else if (e.key === 'Enter' || e.key === ' ') {
                    if (idx < 0) return;
                    e.preventDefault();
                    toggleItem(items[idx]);
                } else if (e.key === 'Escape') {
                    items.forEach(it => {
                        it.classList.remove('expanded');
                        const ch = it.querySelector('.roadmap-item-chevron');
                        if (ch) ch.textContent = '▸';
                    });
                    if (idx >= 0) items[idx].blur();
                }
            });
            // Roadmap filter pills
            const applyRoadmapFilters = () => {
                const cf = this.roadmapFilters.category;
                const sf = this.roadmapFilters.status;
                document.querySelectorAll('.roadmap-item').forEach(item => {
                    const matchCat = cf === 'all' || item.dataset.category === cf;
                    const matchSt = sf === 'all' || item.dataset.status === sf;
                    item.style.display = (matchCat && matchSt) ? '' : 'none';
                });
                document.querySelectorAll('.roadmap-section').forEach(section => {
                    const visible = section.querySelectorAll('.roadmap-item:not([style*="display: none"])');
                    section.style.display = visible.length ? '' : 'none';
                });
            };
            document.querySelectorAll('.roadmap-filter-pill').forEach(btn => btn.addEventListener('click', () => {
                const type = btn.dataset.filterType;
                const value = btn.dataset.filterValue;
                this.roadmapFilters[type] = value;
                document.querySelectorAll(`.roadmap-filter-pill[data-filter-type="${type}"]`).forEach(b => b.classList.remove('active'));
                btn.classList.add('active');
                applyRoadmapFilters();
            }));
            applyRoadmapFilters();
        }
        if (page === 'discussions') {
            document.querySelectorAll('.disc-row').forEach(row => {
                row.addEventListener('click', async () => {
                    const did = row.dataset.discId;
                    const detail = document.querySelector(`.disc-detail-row[data-disc-detail="${did}"]`);
                    if (detail) {
                        const isVisible = detail.style.display !== 'none';
                        document.querySelectorAll('.disc-detail-row').forEach(d => d.style.display = 'none');
                        document.querySelectorAll('.disc-row').forEach(r => r.classList.remove('expanded'));
                        if (!isVisible) {
                            detail.style.display = 'table-row';
                            row.classList.add('expanded');
                            const wrap = document.getElementById('discComments' + did);
                            if (wrap) {
                                try {
                                    const disc = await this.api('discussions/' + did);
                                    wrap.innerHTML = this.renderDiscussionComments(disc.comments || []);
                                } catch {
                                    wrap.innerHTML = '<div style="font-size:0.8rem;color:var(--danger);padding:8px">Failed to load comments</div>';
                                }
                            }
                        }
                    }
                });
            });
            document.querySelectorAll('.disc-feature-link').forEach(link => {
                link.addEventListener('click', (e) => { e.preventDefault(); e.stopPropagation(); App.navigate('features'); });
            });
        }
        if (page === 'qa') {
            document.querySelectorAll('.qa-approve').forEach(btn => btn.addEventListener('click', async () => {
                btn.style.transform = 'scale(0.95)';
                const notes = document.querySelector(`.qa-notes[data-feature="${btn.dataset.feature}"]`)?.value || 'Approved via web';
                try { await this.apiPost('qa/' + btn.dataset.feature + '/approve', { notes }); App.toast('✓ Feature approved', 'success'); } catch(e) { App.toast('Error approving', 'error'); }
                this.navigate('qa');
            }));
            document.querySelectorAll('.qa-reject').forEach(btn => btn.addEventListener('click', async () => {
                btn.style.transform = 'scale(0.95)';
                const notes = document.querySelector(`.qa-notes[data-feature="${btn.dataset.feature}"]`)?.value || 'Rejected via web';
                try { await this.apiPost('qa/' + btn.dataset.feature + '/reject', { notes }); App.toast('✗ Feature rejected', 'error'); } catch(e) { App.toast('Error rejecting', 'error'); }
                this.navigate('qa');
            }));
        }
    },

    toast(msg, type) {
        let c = document.querySelector('.toast-container');
        if (!c) { c = document.createElement('div'); c.className = 'toast-container'; c.setAttribute('role', 'status'); c.setAttribute('aria-live', 'polite'); document.body.appendChild(c); }
        const t = document.createElement('div');
        t.className = `toast toast-${type || 'info'}`;
        t.textContent = msg;
        c.appendChild(t);
        setTimeout(() => { t.classList.add('toast-exit'); t.addEventListener('animationend', () => t.remove()); }, 3000);
    },
};

function esc(s) { if(!s) return ''; const d=document.createElement('div'); d.textContent=s; return d.innerHTML; }
function fmtDate(iso) { if(!iso) return '—'; return new Date(iso).toLocaleDateString('en-US',{month:'short',day:'numeric',year:'numeric'}); }
function fmtTime(iso) { if(!iso) return ''; return new Date(iso).toLocaleString('en-US',{month:'short',day:'numeric',hour:'2-digit',minute:'2-digit'}); }
function eventIcon(t) {
    if(t.includes('approved')) return '✔';
    if(t.includes('completed')) return '✔';
    if(t.includes('rejected')) return '✘';
    if(t.includes('failed')) return '✘';
    if(t.includes('created')) return '⊕';
    if(t.includes('started')) return '▸';
    if(t.includes('scored')) return '★';
    if(t.includes('updated')||t.includes('edit')) return '✎';
    if(t.includes('removed')||t.includes('deleted')) return '⊖';
    if(t.includes('cycle')) return '⟳';
    if(t.includes('milestone')) return '⚑';
    if(t.includes('heartbeat')) return '♥';
    if(t.includes('qa')||t.includes('review')) return '⊘';
    if(t.includes('moved')||t.includes('transition')) return '→';
    if(t.includes('assigned')) return '⊙';
    if(t.includes('comment')||t.includes('note')) return '✦';
    return '●';
}
function eventClass(t) {
    if(t.includes('completed')||t.includes('approved')) return 'success';
    if(t.includes('failed')||t.includes('rejected')) return 'danger';
    if(t.includes('started')||t.includes('scored')) return 'warning';
    if(t.includes('created')) return 'info';
    if(t.includes('updated')||t.includes('edit')) return 'purple';
    if(t.includes('cycle')||t.includes('milestone')) return 'info';
    if(t.includes('heartbeat')) return 'success';
    if(t.includes('qa')||t.includes('review')) return 'warning';
    return '';
}
function fmtRelTime(iso) {
    if(!iso) return '';
    const d = new Date(iso);
    if(isNaN(d.getTime())) return '';
    const now = Date.now(), diff = now - d.getTime();
    if(diff < 0) return 'just now';
    const s = Math.floor(diff/1000);
    if(s < 60) return 'just now';
    if(s < 3600) { const m=Math.floor(s/60); return m + (m===1?' minute':' minutes') + ' ago'; }
    if(s < 86400) { const h=Math.floor(s/3600); return h + (h===1?' hour':' hours') + ' ago'; }
    if(s < 172800) return 'yesterday at ' + d.toLocaleTimeString('en-US',{hour:'2-digit',minute:'2-digit'});
    const days = Math.floor(s/86400);
    if(days < 30) return days + (days===1?' day':' days') + ' ago';
    if(days < 365) { const mo=Math.floor(days/30); return mo + (mo===1?' month':' months') + ' ago'; }
    const yr=Math.floor(days/365); return yr + (yr===1?' year':' years') + ' ago';
}
function fmtEvent(t) { return t.split('.').map(s=>s.charAt(0).toUpperCase()+s.slice(1)).join(' '); }

document.addEventListener('DOMContentLoaded', () => App.init());
