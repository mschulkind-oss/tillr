/* Lifecycle Web Viewer — Application */

const App = {
    currentPage: 'dashboard',

    async init() {
        this.bindNavigation();
        this.bindThemeToggle();
        this.bindHamburger();
        this.loadTheme();
        this.connectWebSocket();
        this._navContext = {};
        this._breadcrumbDetail = null;
        this._breadcrumbSub = null;
        window.addEventListener('popstate', () => {
            const parsed = this.parsePath();
            if (parsed.page) {
                this._navContext = parsed.context || {};
                this._breadcrumbDetail = null;
                this._breadcrumbSub = null;
                this.navigate(parsed.page, null, true);
            }
        });
        const initial = this.parsePath();
        if (initial.context) this._navContext = initial.context;
        App.initKeyboardShortcuts();
        await this.navigate(initial.page || 'dashboard', null, true);
        this.updateQABadge();
    },

    parsePath() {
        // Support both /page/id paths and legacy #page/id hashes
        const hash = window.location.hash.replace(/^#/, '');
        if (hash) {
            // Legacy hash URL — redirect to real URL
            const parts = hash.split('/');
            const page = parts[0];
            const id = parts.slice(1).join('/');
            const newPath = '/' + page + (id ? '/' + id : '');
            history.replaceState(null, '', newPath);
            return { page, context: id ? { id: decodeURIComponent(id) } : {} };
        }
        const path = window.location.pathname.replace(/^\//, '');
        if (!path) return { page: null, context: {} };
        const parts = path.split('/');
        const page = parts[0];
        const context = parts.slice(1).length ? { id: decodeURIComponent(parts.slice(1).join('/')) } : {};
        return { page, context };
    },

    connectWebSocket() {
        const proto = location.protocol === 'https:' ? 'wss:' : 'ws:';
        const url = `${proto}//${location.host}/ws`;
        this._ws = new WebSocket(url);
        this._ws.onmessage = (event) => {
            try {
                const msg = JSON.parse(event.data);
                if (msg.type === 'refresh') {
                    // Preserve expanded state across re-render
                    const expandedFeature = document.querySelector('.ft-row.expanded');
                    if (expandedFeature) this._expandedFeatureId = expandedFeature.dataset.featureId;
                    const expandedRoadmap = document.querySelector('.roadmap-item.expanded');
                    if (expandedRoadmap) this._expandedRoadmapId = expandedRoadmap.dataset.roadmapId;
                    // Preserve scroll position across WebSocket-triggered re-renders
                    const scrollY = window.scrollY;
                    this._isRefresh = true;
                    this.navigate(this.currentPage).then(() => {
                        window.scrollTo(0, scrollY);
                        this._isRefresh = false;
                    });
                    this.updateQABadge();
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

    async updateQABadge() {
        try {
            const pending = await this.api('qa/pending');
            const badge = document.getElementById('qaBadge');
            if (!badge) return;
            const count = Array.isArray(pending) ? pending.length : 0;
            if (count > 0) {
                badge.textContent = count;
                badge.style.display = '';
            } else {
                badge.style.display = 'none';
            }
        } catch { /* ignore errors */ }
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

    async navigate(page, context, skipPush) {
        if (context) this._navContext = context;
        this.currentPage = page;
        const ctxId = this._navContext?.id ? '/' + encodeURIComponent(this._navContext.id) : '';
        const newPath = '/' + page + ctxId;
        if (!skipPush && window.location.pathname !== newPath) history.pushState(null, '', newPath);
        document.querySelectorAll('.nav-link').forEach(l => {
            const isActive = l.dataset.page === page;
            l.classList.toggle('active', isActive);
            if (isActive) l.setAttribute('aria-current', 'page');
            else l.removeAttribute('aria-current');
        });
        document.title = page.charAt(0).toUpperCase() + page.slice(1) + ' — Lifecycle';
        const content = document.getElementById('content');
        const isRefresh = this._isRefresh;
        if (content.children.length && !isRefresh) {
            content.style.transition = 'opacity 0.15s ease';
            content.style.opacity = '0';
            await new Promise(r => setTimeout(r, 150));
        }
        // Preserve container height during re-render to prevent CLS
        const prevHeight = content.offsetHeight;
        if (prevHeight > 0) content.style.minHeight = prevHeight + 'px';
        content.style.transition = 'none';
        content.style.opacity = '1';
        if (!isRefresh) content.innerHTML = this.renderSkeleton();
        try {
            const html = await this.renderPage(page);
            content.innerHTML = html;
            // Release min-height lock after content is rendered
            requestAnimationFrame(() => { content.style.minHeight = ''; });
            App.updateBreadcrumbs();
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

    updateBreadcrumbs() {
        const content = document.getElementById('content');
        if (!content) return;
        let el = document.getElementById('breadcrumb');
        if (!el) {
            el = document.createElement('div');
            el.id = 'breadcrumb';
            el.className = 'breadcrumb-bar';
            el.setAttribute('aria-label', 'Breadcrumb');
            content.prepend(el);
        } else if (el.parentNode !== content) {
            content.prepend(el);
        }
        const labels = {
            dashboard: 'Dashboard', features: 'Features', roadmap: 'Roadmap',
            cycles: 'Cycles', stats: 'Stats', history: 'History',
            discussions: 'Discussions', qa: 'QA', agents: 'Agents',
            ideas: 'Ideas', context: 'Context', spec: 'Spec Doc', adrs: 'Decisions'
        };
        const page = this.currentPage;
        const label = labels[page] || page.charAt(0).toUpperCase() + page.slice(1);
        let html = '<span class="breadcrumb-item' + (this._breadcrumbDetail ? '' : ' active') + '"'
            + (this._breadcrumbDetail ? ' onclick="App._breadcrumbDetail=null;App._breadcrumbSub=null;App.navigate(\'' + page + '\');return false" style="cursor:pointer"' : '')
            + '>' + label + '</span>';
        if (this._breadcrumbDetail) {
            html += '<span class="breadcrumb-separator" aria-hidden="true">›</span>'
                + '<span class="breadcrumb-item' + (this._breadcrumbSub ? '' : ' active') + '">'
                + esc(this._breadcrumbDetail) + '</span>';
        }
        if (this._breadcrumbSub) {
            html += '<span class="breadcrumb-separator" aria-hidden="true">›</span>'
                + '<span class="breadcrumb-item active">' + esc(this._breadcrumbSub) + '</span>';
        }
        el.innerHTML = html;
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

    navigateTo(page, id) {
        this._navContext = id ? { id } : {};
        this.navigate(page);
    },

    scoreColorClass(score) {
        if (score >= 8) return 'score-green';
        if (score >= 6) return 'score-yellow';
        return 'score-red';
    },

    renderSpecContent(spec) {
        if (!spec) return '';
        const lines = spec.split('\n');
        let inList = false, html = '';
        for (const line of lines) {
            const m = line.match(/^\s*(\d+)[.)]\s+(.+)/);
            if (m) {
                if (!inList) { html += '<ol class="spec-criteria-list">'; inList = true; }
                html += `<li>${esc(m[2])}</li>`;
            } else {
                if (inList) { html += '</ol>'; inList = false; }
                if (line.trim()) html += `<p class="spec-text">${esc(line)}</p>`;
            }
        }
        if (inList) html += '</ol>';
        return html || `<pre class="feature-spec-block"><code>${esc(spec)}</code></pre>`;
    },

    async renderPage(page) {
        switch (page) {
            case 'dashboard': return this.renderDashboard();
            case 'features': return this.renderFeatures();
            case 'roadmap': return this.renderRoadmap();
            case 'cycles': return this.renderCycles();
            case 'stats': return App.renderStats();
            case 'history': return this.renderHistory();
            case 'discussions': return this.renderDiscussions();
            case 'qa': return this.renderQA();
            case 'stats': return this.renderStats();
            case 'agents': return App.renderAgents();
            case 'ideas': return App.renderIdeas();
            case 'context': return App.renderContext();
            case 'spec': return App.renderSpec();
            case 'adrs': return App.renderDecisions();
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
        const [status, features, milestones, roadmap, cycles, discussions, gitData] = await Promise.all([
            this.api('status'), this.api('features'), this.api('milestones'),
            this.api('roadmap'), this.api('cycles'),
            this.api('discussions').catch(() => []),
            this.api('git/log').catch(() => ({ vcs: '', commits: [] })),
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
            const emptyClass = items.length === 0 ? ' kanban-column--empty' : '';
            return `<div class="kanban-column kanban-column-${s}${emptyClass}">
                <div class="kanban-header"><span class="kanban-title">${statusLabels[s]||s}</span><span class="kanban-count">${items.length}</span></div>
                ${items.map(f => {
                    const blockingCount = features.filter(o => o.depends_on && o.depends_on.includes(f.id) && o.status !== 'done').length;
                    const blockedByNames = (f.status === 'blocked' && f.depends_on) ? f.depends_on.filter(d => { const dep = features.find(o => o.id === d); return dep && dep.status === 'blocked'; }) : [];
                    const blockingHtml = blockingCount ? '<div class="kanban-card-blocking">⚠️ Blocking: ' + blockingCount + '</div>' : '';
                    const blockedHtml = blockedByNames.length ? '<div class="kanban-card-blocked">🚫 Blocked by: ' + blockedByNames.map(d => esc(d)).join(', ') + '</div>' : '';
                    return '<div class="kanban-card" data-status="' + s + '" data-feature-id="' + esc(f.id) + '" data-feature-name="' + esc(f.name) + '" title="' + esc(f.name) + '"><div class="kanban-card-title">' + esc(f.name) + '</div><div class="kanban-card-meta"><span class="kanban-card-priority p' + f.priority + '"></span>P' + f.priority + (f.milestone_name ? ' · ' + esc(f.milestone_name) : '') + '</div>' + blockedHtml + blockingHtml + '</div>';
                }).join('') || '<div class="kanban-empty">—</div>'}
            </div>`;
        }).join('');

        const milestoneCards = milestones.length ? milestones.map(m => {
            const done = m.done_features || 0;
            const mtotal = m.total_features || 0;
            const pct = mtotal > 0 ? Math.round((done / mtotal) * 100) : 0;
            const pctClass = pct === 100 ? 'milestone-complete' : pct > 0 ? 'milestone-active' : '';
            return `<div class="card milestone-card ${pctClass}" style="cursor:pointer" data-milestone="${esc(m.name)}" data-milestone-id="${esc(m.id)}"><div class="card-header"><span class="card-title inline-editable" onclick="event.stopPropagation();App.inlineEdit(this,{value:${JSON.stringify(m.name)},type:'text',onSave:async function(v){await App.apiPatch('milestones/'+${JSON.stringify(m.id)},{name:v})}})">${esc(m.name)}</span><span class="badge badge-${m.status}">${m.status}</span></div>
                <div class="progress-bar" role="progressbar" aria-valuenow="${pct}" aria-valuemin="0" aria-valuemax="100" aria-label="${esc(m.name)} progress"><div class="progress-fill ${pct===100?'success':''}" style="width:${pct}%"></div></div>
                <div class="milestone-meta"><span class="milestone-fraction">${done}/${mtotal} features</span><span class="milestone-pct">${pct}%</span></div></div>`;
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
            return `<div class="dash-roadmap-item" data-roadmap-id="${esc(r.id)}" style="display:flex;align-items:center;gap:10px;padding:8px 4px;border-bottom:1px solid var(--border);cursor:pointer;border-radius:4px;transition:background 0.15s ease" onmouseover="this.style.background='var(--bg-hover)'" onmouseout="this.style.background='transparent'">
                <span style="color:${priColors[r.priority]||'var(--text-secondary)'};font-size:0.75rem;font-weight:700;min-width:20px;text-align:center">${i+1}</span>
                <span style="font-size:0.82rem;flex:1;min-width:0;overflow:hidden;text-overflow:ellipsis;white-space:nowrap;color:var(--text-primary)">${esc(r.title)}</span>
                <span class="dash-roadmap-status" style="font-size:0.72rem;color:var(--text-secondary)">${stIcons[r.status]||'○'} ${r.status}</span>
                ${r.effort ? `<span class="effort-badge effort-${r.effort}">${r.effort.toUpperCase()}</span>` : ''}
            </div>`;
        }).join('') : '<div style="color:var(--text-secondary);font-size:0.82rem;padding:8px 0">No roadmap items yet</div>';

        // Active cycles
        const activeCycles = (cycles || []).filter(c => c.status === 'active');
        const cycleCards = activeCycles.length ? activeCycles.map(c => {
            const steps = c.config?.steps || [];
            const currentIdx = steps.indexOf(c.current_step);
            const progress = steps.length > 0 ? Math.round(((currentIdx + 1) / steps.length) * 100) : 0;
            return `<div style="padding:8px 4px;border-bottom:1px solid var(--border)">
                <div style="display:flex;align-items:center;gap:8px;margin-bottom:6px">
                    <span style="font-size:0.85rem;font-weight:600;flex:1;color:var(--text-primary)">${esc(c.feature_id)}</span>
                    <span class="cycle-type-name">${esc(c.cycle_type)}</span>
                </div>
                <div style="display:flex;align-items:center;gap:8px">
                    <div class="progress-bar" style="flex:1;height:6px"><div class="progress-fill" style="width:${progress}%"></div></div>
                    <span style="font-size:0.72rem;color:var(--text-secondary)">${c.current_step} (${currentIdx+1}/${steps.length})</span>
                </div>
            </div>`;
        }).join('') : '<div style="color:var(--text-secondary);font-size:0.82rem;padding:8px 0">No active cycles</div>';

        // Priority distribution mini-chart
        const priCounts = {};
        features.forEach(f => { priCounts[f.priority] = (priCounts[f.priority]||0) + 1; });
        const priLabels = {1:'Critical',2:'High',3:'Medium',4:'Low',5:'Nice to have'};
        const priColors = {1:'var(--danger)',2:'var(--warning)',3:'var(--accent)',4:'var(--success)',5:'var(--purple)'};
        const priChart = Object.keys(priLabels).map(p => {
            const count = priCounts[p] || 0;
            const pct = total > 0 ? Math.round((count/total)*100) : 0;
            return `<div style="display:flex;align-items:center;gap:8px;padding:4px 0">
                <span class="dash-pri-label" style="font-size:0.75rem;color:var(--text-secondary);min-width:74px">${priLabels[p]}</span>
                <div style="flex:1;height:10px;background:var(--bg-tertiary);border-radius:5px;overflow:hidden"><div style="height:100%;width:${pct}%;background:${priColors[p]};border-radius:5px;transition:width 0.8s cubic-bezier(0.4,0,0.2,1)"></div></div>
                <span class="dash-pri-count" style="font-size:0.75rem;color:var(--text-primary);min-width:22px;text-align:right;font-weight:600">${count}</span>
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

        // Cycle scores dots for dashboard
        const recentScores = [];
        (cycles || []).forEach(c => { if (c.scores) c.scores.forEach(s => recentScores.push({ score: s.score, feature: c.feature_id, step: s.step, created: s.created_at, notes: s.notes })); });
        recentScores.sort((a, b) => (b.created || '').localeCompare(a.created || ''));
        const scoreDots = recentScores.slice(0, 24).map(s => {
            const cls = this.scoreColorClass(s.score);
            return `<span class="score-dot ${cls}" title="${s.score.toFixed(1)} — ${esc(s.feature)}${s.notes ? '\n' + s.notes : ''}">${s.score.toFixed(1)}</span>`;
        }).join('');
        const scoresCard = recentScores.length ? `<div class="card"><div class="card-title" style="margin-bottom:8px">🎯 Cycle Scores</div><div class="score-dots-wrap">${scoreDots}</div></div>` : '';

        // Git/VCS recent commits
        const gitCommits = (gitData && gitData.commits) || [];
        const vcsLabel = gitData && gitData.vcs === 'jj' ? 'jj' : 'git';
        const gitCard = gitCommits.length ? `<div class="card"><div class="card-title" style="margin-bottom:8px">📝 Recent Commits <span class="text-secondary" style="font-size:0.72rem;font-weight:400">(${esc(vcsLabel)})</span></div><div class="commit-list">${
            gitCommits.slice(0, 8).map(c => {
                const hash = (c.hash || '').substring(0, 8);
                let date = c.date || '';
                if (date.length > 16) date = date.substring(0, 16);
                return `<div class="commit-row"><code class="commit-hash">${esc(hash)}</code><span class="commit-msg">${esc(c.message || '')}</span><span class="commit-author text-secondary">${esc(c.author || '')}</span><span class="commit-date text-secondary">${esc(date)}</span></div>`;
            }).join('')
        }</div></div>` : '';

        const qaCount = (counts['human-qa']||0) + (counts['agent-qa']||0);
        return `<div class="page-header"><h2 class="page-title">${esc(status.project?.name || 'Project')} Dashboard</h2><p class="page-subtitle">Project overview and health at a glance</p></div>
            <div class="stats-grid">
                <div class="stat-card stat-card--accent" style="cursor:pointer" onclick="App.navigate('features')"><div class="stat-card-info"><div class="stat-value">${total}</div><div class="stat-label">Total Features</div></div><div class="stat-icon" aria-hidden="true">📦</div></div>
                <div class="stat-card stat-card--success" style="cursor:pointer" onclick="App.navigate('features')"><div class="stat-card-info"><div class="stat-value">${counts.done||0}</div><div class="stat-label">Completed</div></div><div class="stat-icon" aria-hidden="true">✅</div></div>
                <div class="stat-card stat-card--warning" style="cursor:pointer" onclick="App.navigate('features')"><div class="stat-card-info"><div class="stat-value">${counts.implementing||0}</div><div class="stat-label">In Progress</div></div><div class="stat-icon" aria-hidden="true">🔨</div></div>
                <div class="stat-card stat-card--danger" style="cursor:pointer" onclick="App.navigate('qa')"><div class="stat-card-info"><div class="stat-value">${qaCount}</div><div class="stat-label">Awaiting QA</div></div><div class="stat-icon" aria-hidden="true">🔍</div></div>
                <div class="stat-card stat-card--purple" style="cursor:pointer" onclick="App.navigate('cycles')"><div class="stat-card-info"><div class="stat-value">${status.active_cycles||0}</div><div class="stat-label">Active Cycles</div></div><div class="stat-icon" aria-hidden="true">🔄</div></div>
            </div>
            ${total > 0 ? this.renderStatusBar(counts, total) : ''}
            <div class="card" style="margin-bottom:12px;overflow:visible"><div class="card-title" style="margin-bottom:10px;font-size:0.95rem">Feature Board</div><div class="kanban">${kanbanCols}</div></div>
            <div class="dashboard-grid">
                <div class="card"><div class="card-title" style="margin-bottom:8px">Milestones</div>${milestoneCards}</div>
                <div class="card"><div class="card-title" style="margin-bottom:8px">Recent Activity</div>${events}</div>
                <div class="card" style="cursor:pointer" onclick="App.navigate('roadmap')"><div class="card-title" style="margin-bottom:8px">📋 Roadmap Highlights</div>${roadmapPreview}</div>
                <div class="card"><div class="card-title" style="margin-bottom:8px">Priority Distribution</div>${priChart}${activeCycles.length ? '<div style="margin-top:12px;border-top:1px solid var(--border);padding-top:8px"><div class="card-title" style="margin-bottom:8px">Active Cycles</div>' + cycleCards + '</div>' : ''}</div>
                ${scoresCard}
                ${statsCard}
                ${gitCard}
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
            return `<div class="roadmap-bar-segment" style="width:${pct}%;background:${s.color}" title="${s.label}: ${counts[s.key]}"><span class="roadmap-bar-label">${s.label}</span></div>`;
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
            const rmItem = f.roadmap_item_id && this._roadmapData ? this._roadmapData.find(r => r.id === f.roadmap_item_id) : null;
            const rmDisplay = rmItem ? rmItem.title : f.roadmap_item_id;
            const specHtml = f.spec ? `<div class="feature-spec-section"><div class="feature-spec-header">Spec</div><div class="feature-spec-card">${this.renderSpecContent(f.spec)}</div></div>` : '';
            const blockingCount = features.filter(o => o.depends_on && o.depends_on.includes(f.id) && o.status !== 'done').length;
            const blockedByNames = (f.status === 'blocked' && f.depends_on) ? f.depends_on.filter(d => { const dep = features.find(o => o.id === d); return dep && dep.status === 'blocked'; }) : [];
            const blockingTag = blockingCount ? `<span class="ft-blocking-tag" title="Blocking ${blockingCount} feature(s)">⚠️ Blocking: ${blockingCount}</span>` : '';
            const blockedTag = blockedByNames.length ? `<span class="ft-blocked-tag" title="Blocked by ${blockedByNames.join(', ')}">🚫 ${blockedByNames.map(d => esc(d)).join(', ')}</span>` : '';
            return `<tr class="ft-row status-${f.status}" data-feature-id="${esc(f.id)}" style="cursor:pointer">
            <td>
                <span class="ft-name inline-editable" onclick="event.stopPropagation();App.inlineEdit(this,{value:${JSON.stringify(f.name)},type:'text',onSave:async function(v){await App.apiPatch('features/'+${JSON.stringify(f.id)},{name:v})}})">${esc(f.name)}</span>
                <div class="ft-id">${esc(f.id)}</div>
                ${desc ? `<div class="ft-desc inline-editable" title="${esc(f.description)}" onclick="event.stopPropagation();App.inlineEdit(this,{value:${JSON.stringify(f.description||'')},type:'textarea',onSave:async function(v){await App.apiPatch('features/'+${JSON.stringify(f.id)},{description:v})}})">${desc}</div>` : ''}
            </td>
            <td><span class="badge badge-${f.status}">${f.status}</span>${(f.status==='implementing'||f.status==='agent-qa')?`<button class="btn-send-qa" onclick="event.stopPropagation();App.sendToQA('${esc(f.id)}')" title="Send to QA for review">Send to QA →</button>`:''}${blockedTag}${blockingTag}</td>
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
              <div class="roadmap-detail-row"><span class="roadmap-detail-label">Status</span><span class="roadmap-detail-value"><span class="feature-status-select-wrap"><select class="feature-status-select" data-feature-id="${esc(f.id)}" onchange="App.changeFeatureStatus('${esc(f.id)}', this.value)">${['draft','planning','implementing','agent-qa','human-qa','done','blocked'].map(s => '<option value="' + s + '"' + (f.status === s ? ' selected' : '') + '>' + s + '</option>').join('')}</select><span class="feature-status-select-arrow">▾</span></span></span></div>
              <div class="roadmap-detail-row"><span class="roadmap-detail-label">Priority</span><span class="roadmap-detail-value">${this.priorityLabel(f.priority)}</span></div>
              ${f.milestone_name ? `<div class="roadmap-detail-row"><span class="roadmap-detail-label">Milestone</span><span class="roadmap-detail-value">${esc(f.milestone_name)}</span></div>` : ''}
              ${f.description ? `<div class="roadmap-detail-row"><span class="roadmap-detail-label">Description</span><span class="roadmap-detail-value inline-editable" onclick="event.stopPropagation();App.inlineEdit(this,{value:${JSON.stringify(f.description||'')},type:'textarea',onSave:async function(v){await App.apiPatch('features/'+${JSON.stringify(f.id)},{description:v})}})">${esc(f.description)}</span></div>` : ''}
              ${f.roadmap_item_id ? `<div class="roadmap-detail-row"><span class="roadmap-detail-label">Roadmap Item</span><span class="roadmap-detail-value"><a href="#" class="feature-roadmap-link" data-roadmap-id="${esc(f.roadmap_item_id)}">${esc(rmDisplay)}</a></span></div>` : ''}
              ${f.depends_on && f.depends_on.length ? `<div class="roadmap-detail-row"><span class="roadmap-detail-label">Depends On</span><span class="roadmap-detail-value">${f.depends_on.map(d => `<a href="#" class="clickable-feature" data-feature-id="${esc(d)}">${esc(d)}</a>`).join(', ')}</span></div>` : ''}
              <div class="roadmap-detail-row"><span class="roadmap-detail-label">Created</span><span class="roadmap-detail-value">${fmtTime(f.created_at)}</span></div>
              ${specHtml}
              <div class="feature-deps-section" data-deps-for="${esc(f.id)}"></div>
              <div class="feature-enriched-section" data-enriched-for="${esc(f.id)}"></div>
              <div class="feature-history-section" data-history-for="${esc(f.id)}"></div>
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
            list = list.filter(f => (f.name && f.name.toLowerCase().includes(q)) || (f.id && f.id.toLowerCase().includes(q)) || (f.description && f.description.toLowerCase().includes(q)) || (f.status && f.status.includes(q)) || (f.milestone_name && f.milestone_name.toLowerCase().includes(q)));
        }
        return list;
    },

    async renderFeatures() {
        const [features, roadmapItems] = await Promise.all([
            this.api('features'),
            this.api('roadmap').catch(() => []),
        ]);
        this._featuresData = features;
        this._roadmapData = roadmapItems;
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
                <div class="features-toolbar-right">
                    <div class="features-view-toggle" id="featuresViewToggle">
                        <button class="view-toggle-btn active" data-view="list" title="List View">☰</button>
                        <button class="view-toggle-btn" data-view="graph" title="Graph View">◈</button>
                    </div>
                    ${App.renderSearchBox('featuresSearch', 'Search features…')}
                </div>
            </div>
            <div class="card" id="featuresTableWrap">${this.buildFeaturesTable(features)}</div>
            <div id="featuresGraphWrap" class="features-graph-wrap" style="display:none"></div>`;
    },

    // ── Roadmap ──
    async renderRoadmap() {
        const [items, features] = await Promise.all([this.api('roadmap'), this.api('features')]);
        if (!items.length) return `<div class="page-header"><h2 class="page-title">Roadmap</h2><p class="page-subtitle">Product vision and prioritized backlog</p></div>
            <div class="empty-state">
                <div class="empty-state-icon">🗺️</div>
                <div class="empty-state-text">Your roadmap is wide open</div>
                <div class="empty-state-hint">Chart the course for your project by adding your first roadmap item.</div>
                <div class="empty-state-cta"><span class="cta-icon">$</span> lifecycle roadmap add &lt;title&gt;</div>
            </div>`;

        this._roadmapData = items;
        this._roadmapFeatures = features;
        if (!this.roadmapFilters) this.roadmapFilters = { category: 'all', status: 'all', priority: 'all' };
        if (!this._roadmapView) this._roadmapView = 'priority';

        const pris = ['critical','high','medium','low','nice-to-have'];
        const priIcons = {critical:'🔴',high:'🟠',medium:'🟡',low:'🟢','nice-to-have':'🔵'};
        const priColors = {critical:'var(--danger)',high:'var(--warning)',medium:'var(--accent)',low:'var(--success)','nice-to-have':'var(--purple)'};
        const grouped = {};
        items.forEach(r => { (grouped[r.priority] = grouped[r.priority] || []).push(r); });

        // Build feature lookup by roadmap_item_id
        const featuresByRoadmap = {};
        features.forEach(f => {
            if (f.roadmap_item_id) {
                (featuresByRoadmap[f.roadmap_item_id] = featuresByRoadmap[f.roadmap_item_id] || []).push(f);
            }
        });

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

        const priFilterCounts = { all: items.length };
        pris.forEach(p => { priFilterCounts[p] = (grouped[p] || []).length; });
        const priLabels = { all:'All', critical:'Critical', high:'High', medium:'Medium', low:'Low', 'nice-to-have':'Nice to Have' };
        const priPills = ['all', ...pris].filter(p => p === 'all' || priFilterCounts[p]).map(p => {
            const active = this.roadmapFilters.priority === p ? ' active' : '';
            return `<button class="roadmap-filter-pill${active}" data-filter-type="priority" data-filter-value="${p}">${priLabels[p] || p}<span class="pill-count">${priFilterCounts[p]}</span></button>`;
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
            <div class="roadmap-filter-group"><span class="roadmap-filter-label">Priority</span><div class="roadmap-filter-pills">${priPills}</div></div>
            <div class="roadmap-filter-group"><span class="roadmap-filter-label">Category</span><div class="roadmap-filter-pills">${catPills}</div></div>
            <div class="roadmap-filter-group"><span class="roadmap-filter-label">Status</span><div class="roadmap-filter-pills">${stPills}</div></div>
        </div>`;

        // Helper: render linked features inline
        const renderLinkedFeatures = (roadmapId) => {
            const linked = featuresByRoadmap[roadmapId] || [];
            if (!linked.length) return '';
            return `<div class="roadmap-inline-features">${linked.map(f =>
                `<span class="roadmap-inline-feature clickable-feature" data-feature-id="${esc(f.id)}"><span class="badge badge-${f.status}" style="font-size:0.6rem;padding:1px 6px">${f.status}</span> ${esc(f.name)}${(f.depends_on && f.depends_on.length) ? `<span class="dep-indicator" title="Depends on: ${f.depends_on.map(d => esc(d)).join(', ')}">⛓️</span>` : ''}</span>`
            ).join('')}</div>`;
        };

        // Status action buttons for expanded details
        const roadmapStatusOrder = ['proposed','accepted','in-progress','done','deferred'];
        const roadmapStatusLabels = {proposed:'Proposed',accepted:'Accepted','in-progress':'In Progress',done:'Completed',deferred:'Deferred'};
        const roadmapStatusIcons = {proposed:'○',accepted:'◉','in-progress':'◔',done:'✓',deferred:'⏸'};

        // Helper: render a single roadmap item card
        const renderItem = (r, rank, i) => {
            const itemCat = r.category || 'uncategorized';
            const linkedHtml = renderLinkedFeatures(r.id);
            const statusBtns = roadmapStatusOrder.map(s =>
                `<button class="roadmap-status-btn rs-${s}${r.status === s ? ' rs-active' : ''}" data-roadmap-id="${esc(r.id)}" data-new-status="${s}" title="Set status to ${roadmapStatusLabels[s]}">${roadmapStatusIcons[s]} ${roadmapStatusLabels[s]}</button>`
            ).join('');
            const effortLabels = {xs:'XS',s:'S',m:'M',l:'L',xl:'XL'};
            const effortHtml = r.effort ? `<span class="effort-badge effort-badge-prominent effort-${r.effort} inline-editable" onclick="event.stopPropagation();App.inlineEdit(this,{value:${JSON.stringify(r.effort||'')},type:'select',options:['xs','s','m','l','xl'],onSave:async function(v){await App.apiPatch('roadmap/'+${JSON.stringify(r.id)},{effort:v})}})">${effortLabels[r.effort]||r.effort}</span>` : '';
            // Feature progress for this roadmap item
            const prog = App.getRoadmapItemProgress(r.id, featuresByRoadmap);
            const progressHtml = prog ? `<div class="rm-card-progress"><div class="rm-card-progress-bar"><div class="rm-card-progress-fill" style="width:${prog.pct}%"></div></div><span class="rm-card-progress-label">${prog.done}/${prog.total}</span></div>` : '';
            // Description with show more
            const descLen = 120;
            const shortDesc = r.description && r.description.length > descLen;
            const descHtml = r.description ? `<div class="roadmap-item-desc inline-editable${shortDesc ? ' rm-desc-truncated' : ''}" onclick="event.stopPropagation();App.inlineEdit(this,{value:${JSON.stringify(r.description||'')},type:'textarea',onSave:async function(v){await App.apiPatch('roadmap/'+${JSON.stringify(r.id)},{description:v})}})">${esc(shortDesc ? r.description.substring(0, descLen) + '…' : r.description)}${shortDesc ? `<button class="rm-show-more" type="button">show more</button>` : ''}</div>` : '';
            const fullDescAttr = shortDesc ? ` data-full-desc="${esc(r.description)}"` : '';
            return `<div class="roadmap-item rm-card-enhanced st-${r.status}" role="listitem" tabindex="0" data-category="${esc(itemCat)}" data-status="${r.status}" data-roadmap-id="${esc(r.id)}" data-priority="${r.priority}"${fullDescAttr} style="animation-delay:${i*0.06}s">
                <div class="rm-card-heat rm-heat-${r.priority}"></div>
                <div class="roadmap-item-number">${rank}</div>
                <div class="roadmap-item-content">
                    <div class="rm-card-top-row">
                        <div class="roadmap-item-title inline-editable" onclick="event.stopPropagation();App.inlineEdit(this,{value:${JSON.stringify(r.title)},type:'text',onSave:async function(v){await App.apiPatch('roadmap/'+${JSON.stringify(r.id)},{title:v})}})">${esc(r.title)}</div>
                        ${r.category?`<span class="roadmap-category rm-card-cat ${catCls(r.category)}">${esc(r.category)}</span>`:''}
                    </div>
                    ${descHtml}
                    ${progressHtml}
                    ${linkedHtml}
                </div>
                <div class="roadmap-item-meta">
                    <span class="priority-badge pri-${r.priority} inline-editable" onclick="event.stopPropagation();App.inlineEdit(this,{value:${JSON.stringify(r.priority||'')},type:'select',options:['critical','high','medium','low','nice-to-have'],onSave:async function(v){await App.apiPatch('roadmap/'+${JSON.stringify(r.id)},{priority:v})}})">${priIcons[r.priority] || '⚪'} ${String(r.priority || '').replace('-',' ')}</span>
                    ${effortHtml}
                    <span class="badge badge-${r.status}">${r.status}</span>
                </div>
                <div class="roadmap-item-details">
                    ${r.description ? `<div class="roadmap-description-block">${esc(r.description)}</div>` : ''}
                    <div class="roadmap-detail-row"><span class="roadmap-detail-label">ID</span><span class="roadmap-detail-value roadmap-detail-id">${esc(r.id)}</span></div>
                    ${r.category ? `<div class="roadmap-detail-row"><span class="roadmap-detail-label">Category</span><span class="roadmap-detail-value">${esc(r.category)}</span></div>` : ''}
                    ${r.effort ? `<div class="roadmap-detail-row"><span class="roadmap-detail-label">Effort</span><span class="roadmap-detail-value"><span class="effort-badge effort-${r.effort}">${{xs:'🟢 XS',s:'🔵 S',m:'🟡 M',l:'🟠 L',xl:'🔴 XL'}[r.effort]||r.effort}</span></span></div>` : ''}
                    <div class="roadmap-detail-row"><span class="roadmap-detail-label">Created</span><span class="roadmap-detail-value">${fmtTime(r.created_at)}</span></div>
                    <div class="roadmap-status-actions"><span class="roadmap-detail-label">Status</span><div class="roadmap-status-btns">${statusBtns}</div></div>
                    ${App.renderRoadmapLinkedFeatures(featuresByRoadmap[r.id] || [])}
                </div>
            </div>`;
        };

        // Priority-grouped sections
        let rank = 0;
        const prioritySections = pris.filter(p => grouped[p]).map(pri => {
            const ritems = grouped[pri];
            return `<div class="roadmap-section pri-${pri}">
                <div class="roadmap-priority-header pri-${pri}"><span class="roadmap-priority-icon" aria-hidden="true">${priIcons[pri]}</span><span class="roadmap-priority-label">${pri.replace('-',' ')}</span><span class="roadmap-priority-count">${ritems.length} item${ritems.length !== 1 ? 's' : ''}</span></div>
                <div class="roadmap-items" role="list">${ritems.map((r,i) => { rank++; return renderItem(r, rank, i); }).join('')}</div>
            </div>`;
        }).join('');

        // Category-grouped sections
        const catGrouped = {};
        items.forEach(r => { const c = r.category || 'uncategorized'; (catGrouped[c] = catGrouped[c] || []).push(r); });
        const catSortedKeys = Object.keys(catGrouped).sort();
        let catRank = 0;
        const categorySections = catSortedKeys.map(cat => {
            const catItems = catGrouped[cat];
            const cls = catCls(cat);
            return `<div class="roadmap-section roadmap-cat-section" data-cat-group="${esc(cat)}">
                <div class="roadmap-category-header ${cls}" data-collapsible="true">
                    <span class="roadmap-cat-header-icon">📁</span>
                    <span class="roadmap-cat-header-label">${esc(cat)}</span>
                    <span class="roadmap-priority-count">${catItems.length} item${catItems.length !== 1 ? 's' : ''}</span>
                    <span class="roadmap-cat-chevron">▾</span>
                </div>
                <div class="roadmap-items roadmap-cat-items" role="list">${catItems.map((r,i) => { catRank++; return renderItem(r, catRank, i); }).join('')}</div>
            </div>`;
        }).join('');

        // Category distribution chart
        const catEntries = Object.entries(catCounts).sort((a,b) => b[1] - a[1]);
        const catBarSegments = catEntries.map(([cat, count]) => {
            const idx = Math.abs(catCls(cat).replace('roadmap-cat-','')) % 6;
            const color = catBarColors[idx];
            const widthPct = ((count / items.length) * 100).toFixed(1);
            return `<div class="roadmap-bar-segment" style="width:${widthPct}%;background:${color}" title="${esc(cat)}: ${count} item${count !== 1 ? 's' : ''} (${widthPct}%)"><span class="roadmap-bar-label">${esc(cat)}</span></div>`;
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

        // Priority distribution chart (horizontal stacked bar)
        const priCounts = {};
        items.forEach(r => { priCounts[r.priority] = (priCounts[r.priority] || 0) + 1; });
        const priBarSegments = pris.filter(p => priCounts[p]).map(p => {
            const count = priCounts[p];
            const widthPct = ((count / items.length) * 100).toFixed(1);
            return `<div class="roadmap-bar-segment" style="width:${widthPct}%;background:${priColors[p]}" title="${p.replace('-',' ')}: ${count} item${count !== 1 ? 's' : ''} (${widthPct}%)"><span class="roadmap-bar-label">${p.replace('-',' ')}</span></div>`;
        }).join('');
        const priLegendItems = pris.filter(p => priCounts[p]).map(p => {
            return `<div class="roadmap-legend-item"><span class="roadmap-legend-dot" style="background:${priColors[p]}"></span><span class="roadmap-legend-label">${p.replace('-',' ')}</span><span class="roadmap-legend-count">${priCounts[p]}</span></div>`;
        }).join('');
        const priorityChart = `<div class="roadmap-priority-chart">
            <div class="roadmap-chart-label">Priority Distribution</div>
            <div class="roadmap-bar">${priBarSegments}</div>
            <div class="roadmap-legend">${priLegendItems}</div>
        </div>`;

        // Dependency flow visualization
        const depFeatures = features.filter(f => (f.depends_on && f.depends_on.length) || features.some(o => o.depends_on && o.depends_on.includes(f.id)));
        let depGraphHtml = '';
        if (depFeatures.length) {
            const featureMap = {};
            features.forEach(f => { featureMap[f.id] = f; });
            // Topological layer assignment
            const layers = {};
            const assigned = new Set();
            const getLayer = (fid, visited) => {
                if (layers[fid] !== undefined) return layers[fid];
                if (visited.has(fid)) return 0;
                visited.add(fid);
                const f = featureMap[fid];
                if (!f || !f.depends_on || !f.depends_on.length) { layers[fid] = 0; return 0; }
                let maxDep = 0;
                f.depends_on.forEach(d => { if (featureMap[d]) maxDep = Math.max(maxDep, getLayer(d, visited) + 1); });
                layers[fid] = maxDep;
                return maxDep;
            };
            depFeatures.forEach(f => getLayer(f.id, new Set()));
            const maxLayer = Math.max(0, ...Object.values(layers));
            const statusColor = {done:'dep-done',implementing:'dep-implementing',draft:'dep-draft',blocked:'dep-blocked',planning:'dep-planning','agent-qa':'dep-implementing','human-qa':'dep-implementing'};

            // Build columns
            const columns = [];
            for (let l = 0; l <= maxLayer; l++) {
                const colFeatures = depFeatures.filter(f => (layers[f.id] || 0) === l);
                columns.push(colFeatures);
            }

            const depNodes = columns.map((col, ci) =>
                `<div class="dep-column">${col.map(f =>
                    `<div class="dep-node ${statusColor[f.status] || 'dep-draft'} clickable-feature" data-feature-id="${esc(f.id)}" title="${esc(f.name)} (${f.status})${f.depends_on && f.depends_on.length ? '\\nDepends on: ' + f.depends_on.join(', ') : ''}">
                        <div class="dep-node-name">${esc(f.name)}</div>
                        <div class="dep-node-status">${f.status}</div>
                    </div>`
                ).join('')}</div>${ci < columns.length - 1 ? '<div class="dep-arrow-col"><div class="dep-arrow">→</div></div>' : ''}`
            ).join('');

            // Build dependency edges list
            const depEdges = [];
            depFeatures.forEach(f => {
                if (f.depends_on) f.depends_on.forEach(d => {
                    if (featureMap[d]) depEdges.push(`<div class="dep-edge"><span class="dep-edge-from">${esc(featureMap[d].name)}</span><span class="dep-edge-arrow">→</span><span class="dep-edge-to">${esc(f.name)}</span></div>`);
                });
            });

            depGraphHtml = `<div class="dep-graph-container" id="depGraphContainer" style="display:none">
                <div class="dep-graph-legend">
                    <span class="dep-legend-item"><span class="dep-legend-dot dep-done"></span>Done</span>
                    <span class="dep-legend-item"><span class="dep-legend-dot dep-implementing"></span>In Progress</span>
                    <span class="dep-legend-item"><span class="dep-legend-dot dep-draft"></span>Draft</span>
                    <span class="dep-legend-item"><span class="dep-legend-dot dep-blocked"></span>Blocked</span>
                </div>
                <div class="dep-graph">${depNodes}</div>
                ${depEdges.length ? `<div class="dep-edges-list"><div class="dep-edges-title">Dependency Edges</div>${depEdges.join('')}</div>` : ''}
            </div>`;
        }

        // View toggle buttons
        const viewToggle = `<div class="roadmap-view-toggle">
            <button class="roadmap-view-btn${this._roadmapView === 'priority' ? ' active' : ''}" data-view="priority" title="Group by priority">📊 Priority</button>
            <button class="roadmap-view-btn${this._roadmapView === 'category' ? ' active' : ''}" data-view="category" title="Group by category">📁 Category</button>
            <button class="roadmap-view-btn${this._roadmapView === 'timeline' ? ' active' : ''}" data-view="timeline" title="Timeline view">🗓️ Timeline</button>
            ${depFeatures.length ? `<button class="roadmap-view-btn${this._roadmapView === 'dependencies' ? ' active' : ''}" data-view="dependencies" title="Dependency flow">🔗 Dependencies</button>` : ''}
        </div>`;

        // Compact priority summary
        const priSummaryParts = pris.filter(p => (grouped[p] || []).length > 0).map(p => `<span class="roadmap-count-${p}">${(grouped[p] || []).length} ${p.replace('-',' ')}</span>`);
        const compactSummary = `<span class="roadmap-compact-summary">${items.length} items · ${priSummaryParts.join(' · ')}</span>`;

        // Hero banner with progress ring + dashboard
        const heroBanner = App.renderRoadmapHeroBanner(items, features, featuresByRoadmap, pris, priColors, priIcons, catCounts, catCls);

        // Timeline view HTML
        const timelineHtml = App.renderRoadmapTimelineView(items, featuresByRoadmap, pris, priColors, catCls);

        return `<div class="page-header" data-date="${new Date().toLocaleDateString()}"><div class="page-header-row"><h2 class="page-title">Roadmap</h2>${viewToggle}<button class="btn-print" onclick="window.print()" title="Print or save as PDF"><span aria-hidden="true">🖨️</span> Print / Export</button></div><p class="page-subtitle">Strategic priorities and planned work — ranked by impact</p><div class="page-subtitle-counts">${compactSummary}</div></div>
            ${heroBanner}
            ${priorityChart}${categoryChart}${App.renderSearchBox('roadmapSearch', 'Search roadmap…')}${filterBar}
            <div id="roadmapSections" class="roadmap-view-priority">${prioritySections}</div>
            <div id="roadmapCategorySections" class="roadmap-view-category" style="display:none">${categorySections}</div>
            <div id="roadmapTimelineContainer" class="roadmap-view-timeline" style="display:none">${timelineHtml}</div>
            ${depGraphHtml}
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
            const latestScore = scores.length ? scores[scores.length - 1].score : null;
            const latestCls = latestScore != null ? App.scoreColorClass(latestScore) : '';
            const curStep = steps[c.current_step] || '';

            return `<div class="card cycle-card cycle-card-compact" data-cycle-id="${c.id}" style="cursor:pointer">
                <div class="cc-row">
                    <span class="cc-feature clickable-feature" data-feature-id="${esc(c.feature_id)}">${esc(c.feature_id)}</span>
                    <span class="badge badge-${c.status}">${c.status}</span>
                </div>
                <div class="cc-row cc-info">
                    <span class="cycle-type-name">${c.cycle_type.replace(/-/g, ' ')}</span>
                    <span class="cc-step-pill">${c.current_step}/${totalSteps}${curStep ? ' · ' + curStep.replace(/-/g, ' ') : ''}</span>
                    ${avgScore != null ? `<span class="cycle-score ${scoreCls}">★ ${avgScore.toFixed(1)}</span>` : ''}
                    ${latestScore != null ? `<span class="score-badge ${latestCls}" style="font-size:0.7rem">latest ${latestScore.toFixed(1)}</span>` : ''}
                </div>
                <div class="cycle-progress"><div class="cycle-progress-fill" style="width:${pct}%"></div></div>
            </div>`;
        };

        let html = `<div class="page-header"><h2 class="page-title">Iteration Cycles</h2><p class="page-subtitle">${activeCycles.length} active · ${completedCycles.length} completed</p></div>`;
        html += App.renderSearchBox('cyclesSearch', 'Search cycles…');

        if (activeCycles.length) {
            html += `<h3 class="section-title">Active Cycles</h3><div class="cycle-card-grid">${activeCycles.map(renderCycleCard).join('')}</div>`;
        }
        if (completedCycles.length) {
            html += `<details class="cycle-completed-toggle" id="cyclesCompletedToggle"><summary class="section-title" style="margin-top:20px;cursor:pointer">Completed Cycles <span class="cc-toggle-count">(${completedCycles.length})</span></summary><div class="cycle-card-grid">${completedCycles.map(renderCycleCard).join('')}</div></details>`;
        }

        // Cycle type reference
        html += `<h3 class="section-title" style="margin-top:20px">Available Cycle Types</h3><div class="cycle-types-grid">`;
        const ctIcons = {'ui-refinement':'🎨','feature-implementation':'⚙️','roadmap-planning':'📋','bug-triage':'🐛','documentation':'📖','architecture-review':'🏗️','release':'🚀','onboarding-dx':'👋'};
        for (const [type, steps] of Object.entries(ctSteps)) {
            html += `<div class="card cycle-type-ref"><div class="card-title"><span class="cycle-type-icon">${ctIcons[type]||'🔄'}</span>${type.replace(/-/g, ' ')}</div><div class="cycle-type-steps">${steps.map(s => `<span class="cycle-type-step">${s.replace(/-/g,' ')}</span>`).join('<span class="cycle-step-arrow">→</span>')}</div><div class="cycle-type-count">${steps.length} steps</div></div>`;
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
            ${App.renderSearchBox('historySearch', 'Search events…')}
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
                let dataJson = '';
                if (e.data) {
                    try {
                        const d = typeof e.data === 'string' ? JSON.parse(e.data) : e.data;
                        detailHtml = Object.entries(d).map(([k,v]) =>
                            `<span class="detail-badge"><span class="detail-badge-key">${esc(k)}</span><span class="detail-badge-val">${esc(String(v))}</span></span>`
                        ).join('');
                        dataJson = JSON.stringify(d, null, 2);
                    } catch(_) { detailHtml = `<span class="detail-badge"><span class="detail-badge-val">${esc(e.data)}</span></span>`; dataJson = String(e.data); }
                }
                return `<div class="timeline-item ${eventClass(e.event_type)}" style="animation-delay:${delay}s;cursor:pointer" data-event-idx="${idx-1}">
                    <div class="timeline-dot">${eventIcon(e.event_type)}</div>
                    <div class="timeline-time">${fmtRelTime(e.created_at)}</div>
                    <div class="timeline-event"><span>${fmtEvent(e.event_type)}</span>${e.feature_id ? `<span class="badge badge-implementing clickable-feature" data-feature-id="${esc(e.feature_id)}" style="cursor:pointer">${esc(e.feature_id)}</span>` : ''}</div>
                    ${detailHtml ? `<div class="timeline-detail">${detailHtml}</div>` : ''}
                    ${dataJson ? `<div class="event-expand" style="display:none"><pre class="event-json">${esc(dataJson)}</pre></div>` : ''}
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
                            <div class="empty-state-text">No features awaiting review — great job!</div>
                            <div class="empty-state-hint">Features will appear here when agents complete their work, or you can send features to QA from the <a href="#" onclick="App.navigate('features');return false">Features</a> page.</div>
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
            ${f.spec ? `<div class="qa-spec-section"><details class="qa-spec-details"><summary class="qa-spec-toggle"><span class="qa-spec-toggle-icon">▶</span> Feature Spec</summary><div class="qa-spec-text qa-spec-md">${renderMD(f.spec)}</div></details></div>` : ''}
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
            ${App.renderSearchBox('discSearch', 'Search discussions…')}
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
                    return `<div class="feature-disc-item clickable-discussion" data-disc-id="${d.id}" style="cursor:pointer">
                        <span class="badge ${statusCls}">${esc(d.status || 'open')}</span>
                        <span class="feature-disc-title">${esc(d.title)}</span>
                        <span class="disc-comment-count">${d.comment_count || 0} 💬</span>
                    </div>`;
                }).join('')}</div>`;
            container.querySelectorAll('.clickable-discussion').forEach(el => {
                el.addEventListener('click', (e) => { e.stopPropagation(); App.navigateTo('discussions', el.dataset.discId); });
            });
        } catch {
            container.innerHTML = '';
        }
    },

    async loadFeatureHistory(featureId, container) {
        try {
            const events = await this.api('history');
            const featureEvents = (events || []).filter(e => e.feature_id === featureId).slice(0, 15);
            if (!featureEvents.length) { container.innerHTML = ''; return; }
            const rows = featureEvents.map(e => {
                let detail = '';
                if (e.data) {
                    try {
                        const d = typeof e.data === 'string' ? JSON.parse(e.data) : e.data;
                        if (d.score !== undefined) detail = `<span class="score-badge ${App.scoreColorClass(d.score)}">${Number(d.score).toFixed(1)}</span>`;
                        else if (d.result) detail = `<span class="feature-history-result">${esc(String(d.result).substring(0, 80))}</span>`;
                        else if (d.new_status) detail = `<span class="badge badge-${d.new_status}">${esc(d.new_status)}</span>`;
                    } catch(e) { /* ignore */ }
                }
                return `<div class="feature-history-item">
                    <span class="feature-history-icon">${eventIcon(e.event_type)}</span>
                    <span class="feature-history-event">${fmtEvent(e.event_type)}</span>
                    ${detail}
                    <span class="feature-history-time">${fmtRelTime(e.created_at)}</span>
                </div>`;
            }).join('');
            container.innerHTML = `<div class="feature-discussions-header">History</div><div class="feature-history-list">${rows}</div>`;
        } catch {
            container.innerHTML = '';
        }
    },

    // loadFeatureEnrichedData assigned after object literal (see below)

    bindPageEvents(page) {
        // Global: bind clickable features anywhere
        const bindClickableFeatures = (root) => {
            (root || document).querySelectorAll('.clickable-feature').forEach(el => {
                if (el.dataset.bound) return;
                el.dataset.bound = '1';
                el.addEventListener('click', (e) => { e.preventDefault(); e.stopPropagation(); App.navigateTo('features', el.dataset.featureId); });
            });
        };

        if (page === 'dashboard') {
            document.querySelectorAll('.kanban-card').forEach(card => {
                card.addEventListener('click', () => {
                    const fid = card.dataset.featureId;
                    if (fid) App.navigateTo('features', fid);
                    else { App._featuresFilter = card.dataset.status; App.navigate('features'); }
                });
            });
            document.querySelectorAll('.activity-item[data-feature-id]').forEach(item => {
                item.addEventListener('click', () => { App.navigateTo('features', item.dataset.featureId); });
            });
            document.querySelectorAll('[data-milestone]').forEach(card => {
                card.addEventListener('click', () => { App.navigate('features'); });
            });
            document.querySelectorAll('.dash-roadmap-item[data-roadmap-id]').forEach(item => {
                item.addEventListener('click', () => { App.navigateTo('roadmap', item.dataset.roadmapId); });
            });
            bindClickableFeatures();
        }
        if (page === 'features') {
            App._setupFeaturePage.call(this);
        }
        if (page === 'stats') {
            App._bindStatsEvents();
        }
        if (page === 'history') {
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
                    bindClickableFeatures(timeline);
                    this.bindHistoryExpand(timeline);
                }
                const remaining = filtered.length - this._historyShown;
                if (remaining <= 0) {
                    btn.parentElement.remove();
                } else {
                    btn.textContent = `Load more (${remaining} remaining)`;
                }
            });
            // Expandable event items + clickable features
            bindClickableFeatures();
            this.bindHistoryExpand(document);
        }
        if (page === 'roadmap') {
            const toggleItem = async (item) => {
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
                    // Bind clickable features inside the expanded details
                    bindClickableFeatures(item);
                    var titleEl = item.querySelector('.roadmap-item-title');
                    App._breadcrumbDetail = titleEl ? titleEl.textContent : (item.dataset.roadmapId || null);
                } else {
                    App._breadcrumbDetail = null;
                    App._breadcrumbSub = null;
                }
                App.updateBreadcrumbs();
            };
            document.querySelectorAll('.roadmap-item').forEach(item => {
                item.addEventListener('click', (e) => {
                    if (e.target.closest('.roadmap-status-btn')) return;
                    toggleItem(item);
                });
            });
            // Status change buttons
            document.querySelectorAll('.roadmap-status-btn').forEach(btn => {
                btn.addEventListener('click', (e) => {
                    e.stopPropagation();
                    App.changeRoadmapStatus(btn.dataset.roadmapId, btn.dataset.newStatus);
                });
            });
            // Make inline features clickable
            bindClickableFeatures(document.getElementById('content'));
            // Spec toggle
            document.querySelectorAll('.roadmap-spec-toggle').forEach(btn => {
                btn.addEventListener('click', (e) => {
                    e.stopPropagation();
                    const wrap = btn.closest('.roadmap-spec-wrap');
                    if (!wrap) return;
                    const content = wrap.querySelector('.roadmap-spec-content');
                    if (!content) return;
                    const open = content.style.maxHeight && content.style.maxHeight !== '0px';
                    content.style.maxHeight = open ? '0px' : content.scrollHeight + 'px';
                    content.style.opacity = open ? '0' : '1';
                    btn.querySelector('.roadmap-spec-chevron').textContent = open ? '▸' : '▾';
                });
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
                const cf = this.roadmapFilters.category, sf = this.roadmapFilters.status, pf = this.roadmapFilters.priority, q = (this._roadmapSearch || '').toLowerCase();
                let vis = 0, total = 0;
                document.querySelectorAll('.roadmap-item').forEach(item => {
                    total++;
                    const show = (cf === 'all' || item.dataset.category === cf) && (sf === 'all' || item.dataset.status === sf) && (pf === 'all' || item.dataset.priority === pf) && (!q || item.textContent.toLowerCase().includes(q));
                    if (show) { item.classList.remove('search-hidden'); vis++; }
                    else { item.classList.add('search-hidden'); }
                });
                document.querySelectorAll('.roadmap-section').forEach(section => {
                    const hasVisible = section.querySelectorAll('.roadmap-item:not(.search-hidden)').length > 0;
                    if (hasVisible) section.classList.remove('search-hidden');
                    else section.classList.add('search-hidden');
                });
                App.updateSearchCount('roadmapSearch', vis, total);
            };
            this._applyRoadmapFilters = applyRoadmapFilters;
            document.querySelectorAll('.roadmap-filter-pill').forEach(btn => btn.addEventListener('click', () => {
                const type = btn.dataset.filterType;
                const value = btn.dataset.filterValue;
                this.roadmapFilters[type] = value;
                document.querySelectorAll(`.roadmap-filter-pill[data-filter-type="${type}"]`).forEach(b => b.classList.remove('active'));
                btn.classList.add('active');
                applyRoadmapFilters();
            }));
            applyRoadmapFilters();

            // View toggle (Priority / Category / Timeline / Dependencies)
            const switchView = (view) => {
                this._roadmapView = view;
                document.querySelectorAll('.roadmap-view-btn').forEach(b => b.classList.toggle('active', b.dataset.view === view));
                const priSections = document.getElementById('roadmapSections');
                const catSections = document.getElementById('roadmapCategorySections');
                const timelineContainer = document.getElementById('roadmapTimelineContainer');
                const depGraph = document.getElementById('depGraphContainer');
                const filters = document.querySelector('.roadmap-filters');
                if (priSections) priSections.style.display = view === 'priority' ? '' : 'none';
                if (catSections) catSections.style.display = view === 'category' ? '' : 'none';
                if (timelineContainer) timelineContainer.style.display = view === 'timeline' ? '' : 'none';
                if (depGraph) depGraph.style.display = view === 'dependencies' ? '' : 'none';
                if (filters) filters.style.display = (view === 'dependencies' || view === 'timeline') ? 'none' : '';
            };
            document.querySelectorAll('.roadmap-view-btn').forEach(btn => btn.addEventListener('click', () => {
                switchView(btn.dataset.view);
            }));
            switchView(this._roadmapView);

            // Category collapsible headers
            document.querySelectorAll('.roadmap-category-header[data-collapsible]').forEach(header => {
                header.addEventListener('click', () => {
                    const section = header.closest('.roadmap-cat-section');
                    const catItems = section?.querySelector('.roadmap-cat-items');
                    const chevron = header.querySelector('.roadmap-cat-chevron');
                    if (catItems) {
                        const collapsed = catItems.style.display === 'none';
                        catItems.style.display = collapsed ? '' : 'none';
                        if (chevron) chevron.textContent = collapsed ? '▾' : '▸';
                    }
                });
            });

            // "Show more" description toggle
            document.querySelectorAll('.rm-show-more').forEach(btn => {
                btn.addEventListener('click', (e) => {
                    e.stopPropagation();
                    const item = btn.closest('.roadmap-item');
                    const descEl = btn.closest('.roadmap-item-desc');
                    if (item && descEl && item.dataset.fullDesc) {
                        descEl.textContent = item.dataset.fullDesc;
                        descEl.classList.remove('rm-desc-truncated');
                    }
                });
            });

            // Timeline block click navigates to item
            document.querySelectorAll('.rm-tl-block').forEach(block => {
                block.addEventListener('click', () => {
                    const rid = block.dataset.roadmapId;
                    if (rid) {
                        switchView('priority');
                        setTimeout(() => {
                            const item = document.querySelector('.roadmap-item[data-roadmap-id="' + rid + '"]');
                            if (item) {
                                item.scrollIntoView({ behavior: 'smooth', block: 'center' });
                                item.classList.add('rm-highlight');
                                setTimeout(() => item.classList.remove('rm-highlight'), 2000);
                            }
                        }, 100);
                    }
                });
            });

            // Auto-expand roadmap item from navigation context or WS refresh
            const roadmapAutoId = this._navContext?.id || this._expandedRoadmapId;
            if (roadmapAutoId) {
                const item = document.querySelector(`.roadmap-item[data-roadmap-id="${roadmapAutoId}"]`);
                if (item) { toggleItem(item); setTimeout(() => item.scrollIntoView({ behavior: 'smooth', block: 'center' }), 100); }
                this._navContext = {};
                this._expandedRoadmapId = null;
            }

            // Initialize drag-and-drop for roadmap cards
            var priContainer = document.getElementById('roadmapSections');
            if (priContainer) App._initRoadmapDragDrop(priContainer);
            var catContainer = document.getElementById('roadmapCategorySections');
            if (catContainer) App._initRoadmapDragDrop(catContainer);
        }
        if (page === 'discussions') {
            App._bindDiscussionEvents();
        }
        if (page === 'cycles') {
            if (typeof App.bindCycleCardClicks === 'function') {
                App.bindCycleCardClicks();
            }
            bindClickableFeatures();
        }
        if (page === 'qa') {
            document.querySelectorAll('.qa-approve').forEach(btn => {
                if (btn.dataset.bound) return;
                btn.dataset.bound = '1';
                btn.addEventListener('click', async () => {
                    if (btn.disabled) return;
                    btn.disabled = true;
                    const notes = document.querySelector(`.qa-notes[data-feature="${btn.dataset.feature}"]`)?.value || 'Approved via web';
                    try { await this.apiPost('qa/' + btn.dataset.feature + '/approve', { notes }); App.toast('✓ Feature approved', 'success'); } catch(e) { App.toast('Error approving', 'error'); }
                    this.navigate('qa');
                });
            });
            document.querySelectorAll('.qa-reject').forEach(btn => {
                if (btn.dataset.bound) return;
                btn.dataset.bound = '1';
                btn.addEventListener('click', async () => {
                    if (btn.disabled) return;
                    btn.disabled = true;
                    const notes = document.querySelector(`.qa-notes[data-feature="${btn.dataset.feature}"]`)?.value || 'Rejected via web';
                    try { await this.apiPost('qa/' + btn.dataset.feature + '/reject', { notes }); App.toast('✗ Feature rejected', 'error'); } catch(e) { App.toast('Error rejecting', 'error'); }
                    this.navigate('qa');
                });
            });
        }
        if (page === 'stats') {
            App.drawStatsCharts();
        }
        if (page === 'ideas') {
            App._bindIdeasEvents();
        }
        if (page === 'context') {
            App._bindContextEvents();
        }
        if (page === 'spec') {
            App._bindSpecEvents();
        }
        if (page === 'agents' || page === 'ideas' || page === 'context' || page === 'spec') {
            bindClickableFeatures();
        }
        if (page === 'agents') {
            App._bindAgentsEvents();
        }
        App._bindGlobalSearch.call(this, page);
    },

    bindHistoryExpand(root) {
        root.querySelectorAll('.timeline-item[data-event-idx]').forEach(item => {
            if (item.dataset.boundExpand) return;
            item.dataset.boundExpand = '1';
            item.addEventListener('click', (e) => {
                if (e.target.closest('.clickable-feature')) return;
                const expand = item.querySelector('.event-expand');
                if (expand) {
                    const isVisible = expand.style.display !== 'none';
                    expand.style.display = isVisible ? 'none' : 'block';
                }
            });
        });
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

// PATCH API helper — assigned outside object literal to avoid V8 parsing quirk
App.apiPatch = async function(endpoint, body) {
    const resp = await fetch('/api/' + endpoint, {
        method: 'PATCH',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify(body),
    });
    return resp.json();
};

// Change roadmap item status via PATCH endpoint
App.changeRoadmapStatus = async function(id, newStatus) {
    try {
        const result = await App.apiPatch('roadmap/' + id + '/status', { status: newStatus });
        if (result.error) {
            App.toast('Error: ' + result.error, 'error');
            return;
        }
        App.toast('Status changed to ' + newStatus, 'success');
        // Remember which item was expanded so we can re-expand after re-render
        App._expandedRoadmapId = id;
        App.render();
    } catch (e) {
        App.toast('Failed to change status', 'error');
    }
};

// Render linked features for roadmap item drill-down
App.renderRoadmapLinkedFeatures = function(linked) {
    if (!linked || !linked.length) return '';
    const esc = window.esc || (function(s) { if(!s) return ''; const d = document.createElement('div'); d.textContent = s; return d.innerHTML; });
    const priIcons = {critical:'🔴',high:'🟠',medium:'🟡',low:'🟢','nice-to-have':'🔵'};

    const featureCards = linked.map(function(f) {
        const priLabel = String(f.priority || '').replace('-', ' ');
        const priIcon = priIcons[f.priority] || '⚪';
        const deps = (f.depends_on && f.depends_on.length)
            ? '<span class="dep-indicator" title="Depends on: ' + f.depends_on.map(function(d) { return esc(d); }).join(', ') + '">⛓️</span>'
            : '';
        let specHtml = '';
        if (f.spec) {
            specHtml = '<div class="roadmap-spec-wrap">' +
                '<button class="roadmap-spec-toggle" type="button">' +
                    '<span class="roadmap-spec-chevron">▸</span> Spec' +
                '</button>' +
                '<div class="roadmap-spec-content" style="max-height:0;opacity:0">' +
                    '<pre class="roadmap-spec-text">' + esc(f.spec) + '</pre>' +
                '</div>' +
            '</div>';
        }

        return '<div class="roadmap-linked-feature-card clickable-feature" data-feature-id="' + esc(f.id) + '">' +
            '<div class="roadmap-linked-feature-header">' +
                '<span class="badge badge-' + f.status + '">' + f.status + '</span>' +
                '<span class="roadmap-linked-feature-name">' + esc(f.name) + '</span>' +
                '<span class="priority-badge pri-' + f.priority + '" style="font-size:0.7rem">' + priIcon + ' ' + priLabel + '</span>' +
                deps +
            '</div>' +
            specHtml +
        '</div>';
    }).join('');

    return '<div class="enriched-section roadmap-linked-features-section">' +
        '<div class="enriched-section-header">🔗 Linked Features (' + linked.length + ')</div>' +
        featureCards +
    '</div>';
};

// Feature page setup — assigned outside object literal to avoid V8 parsing quirk
App._setupFeaturePage = function() {
    const self = this;
    const expandFeature = function(fid) {
        const row = document.querySelector('.ft-row[data-feature-id="' + fid + '"]');
        const detail = document.querySelector('.ft-detail-row[data-detail-for="' + fid + '"]');
        if (!row || !detail) return;
        document.querySelectorAll('.ft-detail-row').forEach(function(d) { d.style.display = 'none'; });
        document.querySelectorAll('.ft-row').forEach(function(r) { r.classList.remove('expanded'); });
        detail.style.display = 'table-row';
        row.classList.add('expanded');
        // Ensure enriched section exists
        var enrichedSection = detail.querySelector('[data-enriched-for="' + fid + '"]');
        if (!enrichedSection) {
            var container = detail.querySelector('.roadmap-item-details');
            if (container) {
                var histSection = detail.querySelector('[data-history-for="' + fid + '"]');
                enrichedSection = document.createElement('div');
                enrichedSection.className = 'feature-enriched-section';
                enrichedSection.setAttribute('data-enriched-for', fid);
                if (histSection) container.insertBefore(enrichedSection, histSection);
                else container.appendChild(enrichedSection);
            }
        }
        if (enrichedSection && !enrichedSection.getAttribute('data-loaded')) {
            App.loadFeatureEnrichedData(fid, enrichedSection);
            enrichedSection.setAttribute('data-loaded', '1');
        }
        var depsSection = detail.querySelector('[data-deps-for="' + fid + '"]');
        if (depsSection && !depsSection.getAttribute('data-loaded')) {
            App.loadFeatureDeps(fid, depsSection);
            depsSection.setAttribute('data-loaded', '1');
        }
        var discSection = detail.querySelector('[data-discussions-for="' + fid + '"]');
        if (discSection) App.loadFeatureDiscussions(fid, discSection);
        var hSection = detail.querySelector('[data-history-for="' + fid + '"]');
        if (hSection && !hSection.getAttribute('data-loaded')) {
            App.loadFeatureHistory(fid, hSection);
            hSection.setAttribute('data-loaded', '1');
        }
    };
    var bindClickableFeatures = function(root) {
        (root || document).querySelectorAll('.clickable-feature').forEach(function(el) {
            el.addEventListener('click', function(e) {
                e.preventDefault();
                e.stopPropagation();
                App.navigateTo('features', el.dataset.featureId);
            });
        });
    };
    var bindFeatureRows = function() {
        document.querySelectorAll('.ft-row').forEach(function(row) {
            row.addEventListener('click', function() {
                var fid = row.dataset.featureId;
                var detail = document.querySelector('.ft-detail-row[data-detail-for="' + fid + '"]');
                if (detail) {
                    var isVisible = detail.style.display !== 'none';
                    document.querySelectorAll('.ft-detail-row').forEach(function(d) { d.style.display = 'none'; });
                    document.querySelectorAll('.ft-row').forEach(function(r) { r.classList.remove('expanded'); });
                    if (!isVisible) {
                        expandFeature(fid);
                        var fname = row.querySelector('.ft-name');
                        App._breadcrumbDetail = fname ? fname.textContent : fid;
                    } else {
                        App._breadcrumbDetail = null;
                        App._breadcrumbSub = null;
                    }
                    App.updateBreadcrumbs();
                }
            });
        });
        document.querySelectorAll('.feature-roadmap-link').forEach(function(link) {
            link.addEventListener('click', function(e) { e.preventDefault(); e.stopPropagation(); App.navigateTo('roadmap', link.dataset.roadmapId); });
        });
        bindClickableFeatures();
        // Prevent status dropdowns from triggering row expand/collapse
        document.querySelectorAll('.feature-status-select').forEach(function(sel) {
            sel.addEventListener('click', function(e) { e.stopPropagation(); });
        });
    };
    var refresh = function() {
        var expandedRow = document.querySelector('.ft-row.expanded');
        var savedId = expandedRow ? expandedRow.dataset.featureId : null;
        var wrap = document.getElementById('featuresTableWrap');
        if (wrap) wrap.innerHTML = self.buildFeaturesTable(self.getFilteredFeatures());
        bindFeatureRows();
        if (savedId) {
            var r = document.querySelector('.ft-row[data-feature-id="' + savedId + '"]');
            if (r) expandFeature(savedId);
        }
    };
    document.querySelectorAll('.filter-pill').forEach(function(btn) {
        btn.addEventListener('click', function() {
            document.querySelectorAll('.filter-pill').forEach(function(b) { b.classList.remove('active'); });
            btn.classList.add('active');
            self._featuresFilter = btn.dataset.status;
            refresh();
        });
    });
    App.bindSearchBox('featuresSearch', function(term) { self._featuresSearch = term; refresh(); App.updateSearchCount('featuresSearch', self.getFilteredFeatures().length, (self._featuresData || []).length); });
    // View toggle (list ↔ graph)
    document.querySelectorAll('.view-toggle-btn').forEach(function(btn) {
        btn.addEventListener('click', function() {
            document.querySelectorAll('.view-toggle-btn').forEach(function(b) { b.classList.remove('active'); });
            btn.classList.add('active');
            var view = btn.dataset.view;
            var tableWrap = document.getElementById('featuresTableWrap');
            var graphWrap = document.getElementById('featuresGraphWrap');
            if (view === 'graph') {
                if (tableWrap) tableWrap.style.display = 'none';
                if (graphWrap) { graphWrap.style.display = ''; App.renderFeaturesDepGraph(graphWrap, self._featuresData); }
            } else {
                if (tableWrap) tableWrap.style.display = '';
                if (graphWrap) graphWrap.style.display = 'none';
            }
        });
    });
    bindFeatureRows();
    var autoExpandId = (self._navContext && self._navContext.id) || self._expandedFeatureId;
    if (autoExpandId) {
        var r = document.querySelector('.ft-row[data-feature-id="' + autoExpandId + '"]');
        if (r) {
            expandFeature(autoExpandId);
            setTimeout(function() { r.scrollIntoView({ behavior: 'smooth', block: 'center' }); }, 100);
        }
        self._navContext = {};
        self._expandedFeatureId = null;
    }

    // Initialize drag-and-drop for feature rows
    var featuresTableWrap = document.getElementById('featuresTableWrap');
    if (featuresTableWrap) App._initFeaturesDragDrop(featuresTableWrap);
};

// Assigned outside object literal to avoid Chrome parsing quirk
App.loadFeatureEnrichedData = async function(featureId, container) {
    try {
        const data = await App.api('features/' + encodeURIComponent(featureId));
        let html = '';

        if (data.work_items && data.work_items.length) {
            const wiRows = data.work_items.map(wi => {
                const statusIcon = wi.status === 'done' ? '✅' : wi.status === 'active' ? '🔄' : wi.status === 'failed' ? '❌' : '⏳';
                return `<div class="enriched-work-item">
                    <span class="enriched-wi-icon">${statusIcon}</span>
                    <span class="badge badge-${wi.status}">${esc(wi.status)}</span>
                    <span class="enriched-wi-type">${esc(wi.work_type)}</span>
                    ${wi.result ? `<span class="enriched-wi-result">${esc(String(wi.result).substring(0, 100))}</span>` : ''}
                    <span class="enriched-wi-time">${fmtRelTime(wi.created_at)}</span>
                </div>`;
            }).join('');
            html += `<div class="enriched-section"><div class="enriched-section-header">Work Items</div>${wiRows}</div>`;
        }

        if (data.cycles && data.cycles.length) {
            const cycleRows = data.cycles.map(c => {
                const statusIcon = c.status === 'completed' ? '✅' : c.status === 'active' ? '🔄' : '❌';
                return `<div class="enriched-cycle-item">
                    <span class="enriched-cycle-icon">${statusIcon}</span>
                    <span class="badge badge-${c.status}">${esc(c.status)}</span>
                    <span class="enriched-cycle-type">${esc(c.cycle_type)}</span>
                    ${c.step_name ? `<span class="enriched-cycle-step">Step: ${esc(c.step_name)}</span>` : ''}
                    <span class="enriched-cycle-iter">Iter ${c.iteration}</span>
                    <span class="enriched-wi-time">${fmtRelTime(c.created_at)}</span>
                </div>`;
            }).join('');
            html += `<div class="enriched-section"><div class="enriched-section-header">Cycle History</div>${cycleRows}</div>`;
        }

        if (data.scores && data.scores.length) {
            const scoreRows = data.scores.map(s => {
                const cls = App.scoreColorClass(s.score);
                return `<div class="enriched-score-item">
                    <span class="score-badge ${cls}">${Number(s.score).toFixed(1)}</span>
                    <span class="enriched-score-step">Step ${s.step}</span>
                    <span class="enriched-cycle-iter">Iter ${s.iteration}</span>
                    ${s.notes ? `<span class="enriched-score-notes">${esc(s.notes)}</span>` : ''}
                    <span class="enriched-wi-time">${fmtRelTime(s.created_at)}</span>
                </div>`;
            }).join('');
            html += `<div class="enriched-section"><div class="enriched-section-header">Scores</div>${scoreRows}</div>`;
        }

        const parent = container.closest('.feature-detail-row') || container.closest('.roadmap-item-details');
        if (data.feature && data.feature.spec && parent && !parent.querySelector('.feature-spec-section')) {
            html += `<div class="feature-spec-section"><div class="feature-spec-header">Spec</div><div class="feature-spec-card">${App.renderSpecContent(data.feature.spec)}</div></div>`;
        }

        container.innerHTML = html;
    } catch(e) {
        container.innerHTML = '';
    }
};

// Fetch and render dependency info for a feature in its detail view
App.loadFeatureDeps = async function(featureId, container) {
    try {
        const data = await App.api('features/' + encodeURIComponent(featureId) + '/deps');
        if (!data || (!data.depends_on.length && !data.depended_by.length)) {
            container.innerHTML = '';
            return;
        }

        const statusBadge = function(status) {
            return '<span class="badge badge-' + esc(status) + '">' + esc(status) + '</span>';
        };

        let html = '<div class="enriched-section"><div class="enriched-section-header">Dependencies</div>';

        if (data.depends_on.length) {
            html += '<div class="dep-detail-group"><div class="dep-detail-label">Depends On:</div>';
            data.depends_on.forEach(function(d) {
                html += '<div class="dep-detail-item"><a href="#" class="clickable-feature dep-link" data-feature-id="' + esc(d.id) + '">' + esc(d.name) + '</a> ' + statusBadge(d.status) + '</div>';
            });
            html += '</div>';
        }

        if (data.depended_by.length) {
            html += '<div class="dep-detail-group"><div class="dep-detail-label">Required By:</div>';
            data.depended_by.forEach(function(d) {
                html += '<div class="dep-detail-item"><a href="#" class="clickable-feature dep-link" data-feature-id="' + esc(d.id) + '">' + esc(d.name) + '</a> ' + statusBadge(d.status) + '</div>';
            });
            html += '</div>';
        }

        if (data.blocking_chain.length) {
            html += '<div class="dep-detail-group dep-blocking"><div class="dep-detail-label">\u26A0\uFE0F Blocking Chain:</div>';
            data.blocking_chain.forEach(function(b) {
                html += '<div class="dep-detail-item dep-blocking-item">' + esc(b) + '</div>';
            });
            html += '</div>';
        }

        // Mini dependency graph canvas
        if (data.depends_on.length || data.depended_by.length) {
            html += '<div class="dep-mini-graph">';
            html += '<canvas id="depMiniCanvas-' + esc(featureId) + '" class="dep-mini-canvas"></canvas>';
            html += '</div>';
        }

        html += '</div>';
        container.innerHTML = html;

        // Draw the mini graph on canvas
        if (data.depends_on.length || data.depended_by.length) {
            var canvas = document.getElementById('depMiniCanvas-' + featureId);
            if (canvas) App.drawMiniDepGraph(canvas, data);
        }

        // Bind clickable features
        container.querySelectorAll('.dep-link').forEach(function(a) {
            a.addEventListener('click', function(e) {
                e.preventDefault();
                App._expandedFeatureId = a.dataset.featureId;
                App.navigate('features', { id: a.dataset.featureId });
            });
        });
    } catch(e) {
        container.innerHTML = '';
    }
};

// Draw a mini dependency graph on a canvas element
App.drawMiniDepGraph = function(canvas, data) {
    var dpr = window.devicePixelRatio || 1;
    var width = canvas.parentElement.offsetWidth || 400;
    var height = 150;
    canvas.width = width * dpr;
    canvas.height = height * dpr;
    canvas.style.width = width + 'px';
    canvas.style.height = height + 'px';
    var ctx = canvas.getContext('2d');
    ctx.scale(dpr, dpr);

    var statusColors = {
        done: '#3fb950', implementing: '#58a6ff', 'agent-qa': '#58a6ff',
        'human-qa': '#58a6ff', draft: '#8b949e', planning: '#d29922',
        blocked: '#f85149', unknown: '#8b949e'
    };

    // Collect columns: depends_on (left), feature (center), depended_by (right)
    var columns = [];
    if (data.depends_on.length) columns.push(data.depends_on.map(function(d) { return { id: d.id, name: d.name, status: d.status }; }));
    columns.push([{ id: data.feature.id, name: data.feature.name, status: data.feature.status, isCurrent: true }]);
    if (data.depended_by.length) columns.push(data.depended_by.map(function(d) { return { id: d.id, name: d.name, status: d.status }; }));

    var colWidth = width / columns.length;
    var nodeW = Math.min(100, colWidth - 20);
    var nodeH = 32;
    var positions = {};

    columns.forEach(function(col, ci) {
        var cx = colWidth * ci + colWidth / 2;
        var totalH = col.length * (nodeH + 12) - 12;
        var startY = (height - totalH) / 2;
        col.forEach(function(node, ni) {
            var x = cx - nodeW / 2;
            var y = startY + ni * (nodeH + 12);
            positions[node.id] = { x: x, y: y, cx: cx, cy: y + nodeH / 2, node: node };
        });
    });

    // Draw edges
    ctx.lineWidth = 2;
    data.depends_on.forEach(function(d) {
        var from = positions[d.id];
        var to = positions[data.feature.id];
        if (from && to) {
            ctx.beginPath();
            ctx.strokeStyle = statusColors[d.status] || statusColors.unknown;
            ctx.globalAlpha = 0.5;
            ctx.moveTo(from.cx + nodeW / 2, from.cy);
            ctx.lineTo(to.cx - nodeW / 2, to.cy);
            ctx.stroke();
            var ax = to.cx - nodeW / 2;
            var ay = to.cy;
            ctx.beginPath();
            ctx.globalAlpha = 0.7;
            ctx.fillStyle = statusColors[d.status] || statusColors.unknown;
            ctx.moveTo(ax, ay);
            ctx.lineTo(ax - 6, ay - 4);
            ctx.lineTo(ax - 6, ay + 4);
            ctx.closePath();
            ctx.fill();
        }
    });
    data.depended_by.forEach(function(d) {
        var from = positions[data.feature.id];
        var to = positions[d.id];
        if (from && to) {
            ctx.beginPath();
            ctx.strokeStyle = statusColors[d.status] || statusColors.unknown;
            ctx.globalAlpha = 0.5;
            ctx.moveTo(from.cx + nodeW / 2, from.cy);
            ctx.lineTo(to.cx - nodeW / 2, to.cy);
            ctx.stroke();
            var ax = to.cx - nodeW / 2;
            var ay = to.cy;
            ctx.beginPath();
            ctx.globalAlpha = 0.7;
            ctx.fillStyle = statusColors[d.status] || statusColors.unknown;
            ctx.moveTo(ax, ay);
            ctx.lineTo(ax - 6, ay - 4);
            ctx.lineTo(ax - 6, ay + 4);
            ctx.closePath();
            ctx.fill();
        }
    });

    // Draw nodes
    ctx.globalAlpha = 1;
    Object.values(positions).forEach(function(p) {
        var color = statusColors[p.node.status] || statusColors.unknown;
        ctx.fillStyle = color;
        ctx.globalAlpha = p.node.isCurrent ? 1 : 0.8;
        ctx.beginPath();
        var r = 6;
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
        if (p.node.isCurrent) {
            ctx.strokeStyle = '#ffffff';
            ctx.lineWidth = 2;
            ctx.stroke();
        }
        ctx.globalAlpha = 1;
        ctx.fillStyle = '#ffffff';
        ctx.font = (p.node.isCurrent ? 'bold ' : '') + '11px -apple-system, sans-serif';
        ctx.textAlign = 'center';
        ctx.textBaseline = 'middle';
        var label = p.node.name.length > 12 ? p.node.name.substring(0, 11) + '\u2026' : p.node.name;
        ctx.fillText(label, p.cx, p.cy);
    });
};

// Render full dependency graph (canvas-based) for the roadmap Dependencies tab
App.renderDependencyGraph = function(container) {
    App.api('dependencies').then(function(data) {
        if (!data || !data.nodes || !data.nodes.length) {
            container.innerHTML = '<div class="empty-state"><div class="empty-state-icon">\uD83D\uDD17</div><div class="empty-state-text">No dependencies found</div></div>';
            return;
        }
        var html = '<div class="dep-canvas-container">';
        html += '<div class="dep-graph-legend">';
        html += '<span class="dep-legend-item"><span class="dep-legend-dot dep-done"></span>Done</span>';
        html += '<span class="dep-legend-item"><span class="dep-legend-dot dep-implementing"></span>In Progress</span>';
        html += '<span class="dep-legend-item"><span class="dep-legend-dot dep-draft"></span>Draft</span>';
        html += '<span class="dep-legend-item"><span class="dep-legend-dot dep-blocked"></span>Blocked</span>';
        html += '</div>';
        html += '<canvas id="depFullCanvas" class="dep-full-canvas"></canvas>';
        html += '</div>';
        container.innerHTML = html;
        var canvas = document.getElementById('depFullCanvas');
        if (canvas) App.drawFullDepGraph(canvas, data);
    });
};

// Draw full project dependency graph on canvas
App.drawFullDepGraph = function(canvas, data) {
    var nodes = data.nodes;
    var edges = data.edges;
    if (!nodes.length) return;

    var nodeMap = {};
    nodes.forEach(function(n) { nodeMap[n.id] = n; });
    var deps = {};
    edges.forEach(function(e) {
        if (!deps[e.from]) deps[e.from] = [];
        deps[e.from].push(e.to);
    });

    // Compute layers (topological sort)
    var layers = {};
    var getLayer = function(id, visited) {
        if (layers[id] !== undefined) return layers[id];
        if (visited[id]) return 0;
        visited[id] = true;
        if (!deps[id] || !deps[id].length) { layers[id] = 0; return 0; }
        var mx = 0;
        deps[id].forEach(function(d) {
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

    var dpr = window.devicePixelRatio || 1;
    var colWidth = 160;
    var nodeW = 120;
    var nodeH = 40;
    var rowGap = 16;
    var width = Math.max(colWidth * columns.length, 400);
    var maxColLen = Math.max.apply(null, columns.map(function(c) { return c.length; }).concat([1]));
    var height = Math.max(maxColLen * (nodeH + rowGap) + 40, 200);

    canvas.width = width * dpr;
    canvas.height = height * dpr;
    canvas.style.width = width + 'px';
    canvas.style.height = height + 'px';
    var ctx = canvas.getContext('2d');
    ctx.scale(dpr, dpr);

    var statusColors = {
        done: '#3fb950', implementing: '#58a6ff', 'agent-qa': '#58a6ff',
        'human-qa': '#58a6ff', draft: '#8b949e', planning: '#d29922',
        blocked: '#f85149'
    };

    var positions = {};
    columns.forEach(function(col, ci) {
        var cx = colWidth * ci + colWidth / 2;
        var totalH = col.length * (nodeH + rowGap) - rowGap;
        var startY = (height - totalH) / 2;
        col.forEach(function(node, ni) {
            positions[node.id] = {
                x: cx - nodeW / 2, y: startY + ni * (nodeH + rowGap),
                cx: cx, cy: startY + ni * (nodeH + rowGap) + nodeH / 2
            };
        });
    });

    // Draw edges with bezier curves
    ctx.lineWidth = 2;
    edges.forEach(function(e) {
        var from = positions[e.from];
        var to = positions[e.to];
        if (!from || !to) return;
        var color = statusColors[(nodeMap[e.to] || {}).status] || '#8b949e';
        ctx.beginPath();
        ctx.strokeStyle = color;
        ctx.globalAlpha = 0.4;
        var startX = from.x;
        var endX = to.x + nodeW;
        ctx.moveTo(startX, from.cy);
        var cpx = (startX + endX) / 2;
        ctx.bezierCurveTo(cpx, from.cy, cpx, to.cy, endX, to.cy);
        ctx.stroke();
        ctx.globalAlpha = 0.7;
        ctx.fillStyle = color;
        ctx.beginPath();
        ctx.moveTo(startX, from.cy);
        ctx.lineTo(startX + 6, from.cy - 4);
        ctx.lineTo(startX + 6, from.cy + 4);
        ctx.closePath();
        ctx.fill();
    });

    // Draw nodes
    ctx.globalAlpha = 1;
    nodes.forEach(function(n) {
        var p = positions[n.id];
        if (!p) return;
        var color = statusColors[n.status] || '#8b949e';
        ctx.fillStyle = color;
        ctx.globalAlpha = 0.9;
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
        ctx.globalAlpha = 1;
        ctx.fillStyle = '#ffffff';
        ctx.font = 'bold 11px -apple-system, BlinkMacSystemFont, sans-serif';
        ctx.textAlign = 'center';
        ctx.textBaseline = 'middle';
        var label = n.name.length > 14 ? n.name.substring(0, 13) + '\u2026' : n.name;
        ctx.fillText(label, p.cx, p.cy - 6);
        ctx.font = '10px -apple-system, BlinkMacSystemFont, sans-serif';
        ctx.globalAlpha = 0.8;
        ctx.fillText(n.status, p.cx, p.cy + 8);
    });

    // Click handler for canvas nodes
    canvas.onclick = function(evt) {
        var rect = canvas.getBoundingClientRect();
        var mx = (evt.clientX - rect.left) * (canvas.width / dpr / rect.width);
        var my = (evt.clientY - rect.top) * (canvas.height / dpr / rect.height);
        for (var id in positions) {
            var p = positions[id];
            if (mx >= p.x && mx <= p.x + nodeW && my >= p.y && my <= p.y + nodeH) {
                App._expandedFeatureId = id;
                App.navigate('features', { id: id });
                return;
            }
        }
    };
    canvas.style.cursor = 'pointer';
};

function esc(s) { if(!s) return ''; const d=document.createElement('div'); d.textContent=s; return d.innerHTML; }

// Markdown rendering via marked.js
function renderMD(md) {
    if (!md) return '';
    if (typeof marked !== 'undefined' && marked.parse) {
        return marked.parse(md, { breaks: true, gfm: true });
    }
    // Fallback: escape HTML and do minimal formatting
    var h = esc(md);
    h = h.replace(/^### (.+)$/gm, '<h3>$1</h3>');
    h = h.replace(/^## (.+)$/gm, '<h2>$1</h2>');
    h = h.replace(/^# (.+)$/gm, '<h1>$1</h1>');
    h = h.replace(/\*\*(.+?)\*\*/g, '<strong>$1</strong>');
    h = h.replace(/\*(.+?)\*/g, '<em>$1</em>');
    h = h.replace(/`(.+?)`/g, '<code>$1</code>');
    h = h.replace(/\n/g, '<br>');
    return h;
}

// Delta flash: highlight changed elements in a container
function deltaFlash(container) {
    if (!container) return;
    var children = Array.from(container.children);
    children.forEach(function(el) {
        el.classList.add('animate-flash-update');
        el.addEventListener('animationend', function() {
            el.classList.remove('animate-flash-update');
        }, { once: true });
    });
}

// Flash a single element (for new items, status changes)
function flashElement(el, type) {
    if (!el) return;
    var cls = type === 'new' ? 'animate-flash-new' : type === 'status' ? 'animate-flash-status' : 'animate-flash-update';
    el.classList.add(cls);
    el.addEventListener('animationend', function() { el.classList.remove(cls); }, { once: true });
}
function fmtDate(iso) { if(!iso) return '—'; return new Date(iso).toLocaleDateString('en-US',{month:'short',day:'numeric',year:'numeric'}); }
function parseUTC(iso) { if(!iso) return null; return new Date(iso.includes('T') || iso.includes('Z') ? iso : iso.replace(' ','T') + 'Z'); }
function fmtTime(iso) { const d = parseUTC(iso); if(!d) return ''; return d.toLocaleString('en-US',{month:'short',day:'numeric',hour:'2-digit',minute:'2-digit'}); }
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
    const d = parseUTC(iso);
    if(!d || isNaN(d.getTime())) return '';
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

// ── Stats Page ──────────────────────────────────────────────────────────────
App.renderStats = async function() {
    var results = await Promise.all([
        App.api('stats'),
        App.api('features').catch(function() { return []; }),
        App.api('cycles').catch(function() { return []; }),
        App.api('history?type=feature.status_changed').catch(function() { return []; })
    ]);
    var stats = results[0];
    var allFeatures = results[1] || [];
    var allCycles = results[2] || [];
    var historyEvents = results[3] || [];

    var fs = stats.feature_stats || {};
    var cs = stats.cycle_stats || {};
    var rs = stats.roadmap_stats || {};
    var ms = stats.milestone_stats || [];
    var act = stats.activity || {};
    var byStatus = fs.by_status || {};
    var byPri = rs.by_priority || {};
    var byCat = rs.by_category || {};
    var scores = cs.scores_over_time || [];

    // Store data for post-render chart drawing
    App._statsData = stats;
    App._statsFeatures = allFeatures;
    App._statsHistory = historyEvents;

    var statusColors = {draft:'#8b949e',planning:'#8b5cf6',implementing:'#3b82f6','agent-qa':'#f59e0b','human-qa':'#ec4899',done:'#10b981',blocked:'#ef4444'};
    var priColors = {critical:'#ef4444',high:'#f59e0b',medium:'#3b82f6',low:'#8b949e','nice-to-have':'#8b5cf6'};

    var statusEntries = Object.entries(byStatus).filter(function(e){return e[1]>0;});
    var total = fs.total || 0;

    // Compute extra stats
    var qaCount = (byStatus['human-qa'] || 0) + (byStatus['agent-qa'] || 0);
    var blockedCount = byStatus.blocked || 0;
    var activeCycles = allCycles.filter(function(c) { return c.status === 'active'; }).length;
    var doneCount = byStatus.done || 0;
    var failedCount = byStatus.failed || 0;
    var finishedCount = doneCount + failedCount;
    var successRateStr = finishedCount > 0 ? (doneCount / finishedCount * 100).toFixed(0) + '%' : 'N/A';

    // Compute average cycle time from features (implementing → done via history events)
    var cycleTimes = App._computeCycleTimes(historyEvents, allFeatures);
    var avgCycleTime = cycleTimes.length > 0
        ? (cycleTimes.reduce(function(a, b) { return a + b; }, 0) / cycleTimes.length).toFixed(1)
        : null;
    App._statsCycleTimes = cycleTimes;

    // Status donut using CSS conic-gradient
    var donutSegments = [];
    var angle = 0;
    for (var di = 0; di < statusEntries.length; di++) {
        var dk = statusEntries[di][0], dv = statusEntries[di][1];
        var startAngle = angle;
        angle += (dv / total) * 360;
        donutSegments.push((statusColors[dk]||'#8b949e') + ' ' + startAngle.toFixed(1) + 'deg ' + angle.toFixed(1) + 'deg');
    }
    var donutGradient = donutSegments.length > 0 ? 'conic-gradient(' + donutSegments.join(', ') + ')' : 'conic-gradient(#484f58 0deg 360deg)';

    var donutLegend = '';
    for (var li = 0; li < statusEntries.length; li++) {
        var lk = statusEntries[li][0], lv = statusEntries[li][1];
        donutLegend += '<div class="stats-legend-item">'
            + '<span class="stats-legend-dot" style="background:' + (statusColors[lk]||'#8b949e') + '"></span>'
            + '<span>' + lk + '</span><span class="stats-legend-val">' + lv + '</span></div>';
    }

    // Priority bars
    var priOrder = ['critical','high','medium','low','nice-to-have'];
    var maxPri = Math.max.apply(null, priOrder.map(function(p){return byPri[p]||0;})) || 1;
    var priHtml = '';
    for (var pi = 0; pi < priOrder.length; pi++) {
        var pk = priOrder[pi];
        var pv = byPri[pk] || 0;
        if (pv === 0) continue;
        var pct = (pv / maxPri * 100).toFixed(0);
        priHtml += '<div class="stats-bar-row">'
            + '<span class="stats-bar-label">' + pk + '</span>'
            + '<div class="stats-bar-track"><div class="stats-bar-fill" style="width:' + pct + '%;background:' + (priColors[pk]||'#8b949e') + '"></div></div>'
            + '<span class="stats-bar-val">' + pv + '</span>'
            + '</div>';
    }

    // Category bars
    var catEntries = Object.entries(byCat).sort(function(a,b){return b[1]-a[1];});
    var maxCat = catEntries.length > 0 ? catEntries[0][1] : 1;
    var catHtml = '';
    var catColors = {core:'#3b82f6',ux:'#8b5cf6',infrastructure:'#f59e0b',dx:'#10b981',documentation:'#ec4899'};
    for (var ci = 0; ci < catEntries.length; ci++) {
        var ck = catEntries[ci][0], cv = catEntries[ci][1];
        var cpct = (cv / maxCat * 100).toFixed(0);
        catHtml += '<div class="stats-bar-row">'
            + '<span class="stats-bar-label">' + ck + '</span>'
            + '<div class="stats-bar-track"><div class="stats-bar-fill" style="width:' + cpct + '%;background:' + (catColors[ck]||'#8b949e') + '"></div></div>'
            + '<span class="stats-bar-val">' + cv + '</span>'
            + '</div>';
    }

    // Milestone progress
    var msHtml = '';
    if (ms.length === 0) {
        msHtml = '<div class="empty-state-hint">No milestones yet</div>';
    }
    for (var mi = 0; mi < ms.length; mi++) {
        var m = ms[mi];
        var mpct = (m.progress||0).toFixed(0);
        msHtml += '<div class="stats-milestone">'
            + '<div class="stats-milestone-header"><span>' + esc(m.name) + '</span><span>' + m.done + '/' + m.total + ' (' + mpct + '%)</span></div>'
            + '<div class="progress-bar"><div class="progress-fill" style="width:' + mpct + '%" data-width="' + mpct + '"></div></div>'
            + '</div>';
    }

    // Cycle type distribution (computed from scores)
    var cycleTypeCounts = {};
    scores.forEach(function(s) {
        cycleTypeCounts[s.cycle] = (cycleTypeCounts[s.cycle] || 0) + 1;
    });
    var cycleTypeColors = {
        'feature-implementation':'#3b82f6','ui-refinement':'#8b5cf6','bug-triage':'#ef4444',
        'documentation':'#10b981','architecture-review':'#f59e0b','release':'#ec4899',
        'roadmap-planning':'#14b8a6','onboarding-dx':'#6366f1'
    };
    var cycleTypeEntries = Object.entries(cycleTypeCounts).sort(function(a,b){return b[1]-a[1];});
    var cycleTypeLegend = '';
    for (var cti = 0; cti < cycleTypeEntries.length; cti++) {
        var ctk = cycleTypeEntries[cti][0], ctv = cycleTypeEntries[cti][1];
        cycleTypeLegend += '<div class="stats-legend-item">'
            + '<span class="stats-legend-dot" style="background:' + (cycleTypeColors[ctk]||'#8b949e') + '"></span>'
            + '<span>' + ctk.replace(/-/g, ' ') + '</span><span class="stats-legend-val">' + ctv + '</span></div>';
    }

    // Feature velocity
    var weeklyRate = act.events_last_7_days || 0;
    var monthlyRate = act.events_last_30_days || 0;
    var avgIterPerCycle = cs.total_cycles > 0 ? (cs.total_iterations / cs.total_cycles).toFixed(1) : '0';

    return '<div class="page-header"><h2>Project Statistics</h2>'
        + '<div class="page-subtitle">' + total + ' features \u00b7 ' + (rs.total||0) + ' roadmap items \u00b7 ' + (cs.total_cycles||0) + ' cycles \u00b7 ' + (act.total_events||0) + ' events</div></div>'
        + '<div class="stats-grid">'
        // Row 1: Overview cards (8 cards) — clickable for drill-down
        + '<div class="stats-card stats-card-sm stats-card-link" data-nav="features?status=done"><div class="stats-card-title">Completion</div>'
        + '<div class="stats-big-number">' + (fs.completion_rate||0).toFixed(1) + '%</div>'
        + '<div class="stats-card-sub">' + doneCount + ' of ' + total + ' features done</div></div>'
        + '<div class="stats-card stats-card-sm stats-card-link" data-nav="' + (failedCount > 0 ? 'features?status=failed' : 'features') + '"><div class="stats-card-title">Success Rate</div>'
        + '<div class="stats-big-number">' + successRateStr + '</div>'
        + '<div class="stats-card-sub">' + doneCount + ' done' + (failedCount > 0 ? ' · ' + failedCount + ' failed' : '') + '</div></div>'
        + '<div class="stats-card stats-card-sm"><div class="stats-card-title">Avg Cycle Time</div>'
        + '<div class="stats-big-number">' + (avgCycleTime !== null ? avgCycleTime + 'd' : 'N/A') + '</div>'
        + '<div class="stats-card-sub">' + cycleTimes.length + ' features measured</div></div>'
        + '<div class="stats-card stats-card-sm stats-card-link" data-nav="history"><div class="stats-card-title">Activity</div>'
        + '<div class="stats-big-number">' + (act.total_events||0) + '</div>'
        + '<div class="stats-card-sub">' + weeklyRate + ' last 7d · ' + monthlyRate + ' last 30d</div></div>'
        + '<div class="stats-card stats-card-sm stats-card-link" data-nav="cycles"><div class="stats-card-title">Avg Score</div>'
        + '<div class="stats-big-number">' + (cs.avg_score||0).toFixed(1) + '</div>'
        + '<div class="stats-card-sub">' + scores.length + ' scores across ' + (cs.total_cycles||0) + ' cycles</div></div>'
        + '<div class="stats-card stats-card-sm stats-card-link" data-nav="qa"><div class="stats-card-title">In QA</div>'
        + '<div class="stats-big-number">' + qaCount + '</div>'
        + '<div class="stats-card-sub">' + (byStatus['agent-qa']||0) + ' agent · ' + (byStatus['human-qa']||0) + ' human</div></div>'
        + '<div class="stats-card stats-card-sm stats-card-link" data-nav="features?status=blocked"><div class="stats-card-title">Blocked</div>'
        + '<div class="stats-big-number' + (blockedCount > 0 ? ' stats-big-number--danger' : '') + '">' + blockedCount + '</div>'
        + '<div class="stats-card-sub">features blocked</div></div>'
        + '<div class="stats-card stats-card-sm stats-card-link" data-nav="cycles"><div class="stats-card-title">Active Cycles</div>'
        + '<div class="stats-big-number">' + activeCycles + '</div>'
        + '<div class="stats-card-sub">' + (cs.total_iterations||0) + ' total iterations</div></div>'
        // Row 2: Feature Status donut + Score Trend canvas
        + '<div class="stats-card stats-card-md"><div class="stats-card-title">Feature Status</div>'
        + '<div class="stats-donut-wrap"><div class="stats-donut" style="background:' + donutGradient + '"><div class="stats-donut-hole">' + total + '</div></div>'
        + '<div class="stats-legend">' + donutLegend + '</div></div></div>'
        + '<div class="stats-card stats-card-md"><div class="stats-card-title">Score Trend</div>'
        + '<div class="score-chart-container"><canvas id="scoreTrendCanvas" class="score-chart-canvas"></canvas>'
        + '<div id="scoreTrendCanvasTooltip" class="score-chart-tooltip"></div></div></div>'
        // Row 3: Priority + Category
        + '<div class="stats-card stats-card-md"><div class="stats-card-title">Roadmap by Priority</div>'
        + (priHtml || '<div class="empty-state-hint">No roadmap items</div>') + '</div>'
        + '<div class="stats-card stats-card-md"><div class="stats-card-title">Roadmap by Category</div>'
        + (catHtml || '<div class="empty-state-hint">No roadmap items</div>') + '</div>'
        // Row 4: Cycle Type Distribution + Feature Velocity
        + '<div class="stats-card stats-card-md"><div class="stats-card-title">Cycle Type Distribution</div>'
        + (cycleTypeEntries.length > 0
            ? '<div class="stats-donut-wrap"><canvas id="cycleTypeCanvas" class="score-chart-canvas" style="max-width:200px;max-height:200px"></canvas>'
              + '<div class="stats-legend">' + cycleTypeLegend + '</div></div>'
            : '<div class="empty-state-hint">No cycle data yet</div>') + '</div>'
        + '<div class="stats-card stats-card-md"><div class="stats-card-title">Feature Velocity</div>'
        + '<div class="stats-velocity">'
        + '<div class="stats-velocity-row"><span class="stats-velocity-label">Features Completed</span><span class="stats-velocity-value">' + doneCount + '</span></div>'
        + '<div class="stats-velocity-row"><span class="stats-velocity-label">Events (7 days)</span><span class="stats-velocity-value">' + weeklyRate + '</span></div>'
        + '<div class="stats-velocity-row"><span class="stats-velocity-label">Events (30 days)</span><span class="stats-velocity-value">' + monthlyRate + '</span></div>'
        + '<div class="stats-velocity-row"><span class="stats-velocity-label">Avg Iterations/Cycle</span><span class="stats-velocity-value">' + avgIterPerCycle + '</span></div>'
        + '<div class="stats-velocity-row"><span class="stats-velocity-label">Total Cycles</span><span class="stats-velocity-value">' + (cs.total_cycles||0) + '</span></div>'
        + '</div></div>'
        // Row 5: Milestone Progress (full width)
        + '<div class="stats-card stats-card-full"><div class="stats-card-title">Milestone Progress</div>' + msHtml + '</div>'
        + '</div>'
        + '<div class="stats-section-header"><h3>Progress Over Time</h3></div>'
        + '<div class="stats-grid">'
        + '<div class="stats-card stats-card-full"><div class="stats-card-title">Feature Burndown</div>'
        + '<div class="burndown-chart-container"><canvas id="burndownCanvas" class="score-chart-canvas"></canvas>'
        + '<div id="burndownCanvasTooltip" class="score-chart-tooltip"></div></div></div>'
        + '<div class="stats-card stats-card-full"><div class="stats-card-title">Weekly Velocity</div>'
        + '<div class="velocity-chart-container"><canvas id="velocityCanvas" class="score-chart-canvas"></canvas>'
        + '<div id="velocityCanvasTooltip" class="score-chart-tooltip"></div></div></div>'
        + '<div class="stats-card stats-card-full"><div class="stats-card-title">Cycle Time Distribution</div>'
        + '<div class="cycletime-chart-container"><canvas id="cycleTimeCanvas" class="score-chart-canvas"></canvas>'
        + '<div id="cycleTimeCanvasTooltip" class="score-chart-tooltip"></div></div></div>'
        + '</div>';
};

// Compute cycle times (days from first implementing to done) for each feature


document.addEventListener('DOMContentLoaded', () => App.init());
