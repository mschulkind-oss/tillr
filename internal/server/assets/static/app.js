/* Lifecycle Web Viewer — Application */

const App = {
    currentPage: 'dashboard',

    async init() {
        this.bindNavigation();
        this.bindThemeToggle();
        this.loadTheme();
        await this.navigate('dashboard');
    },

    bindNavigation() {
        document.querySelectorAll('.nav-link').forEach(link => {
            link.addEventListener('click', (e) => {
                e.preventDefault();
                this.navigate(link.dataset.page);
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
        });
    },

    loadTheme() {
        const saved = localStorage.getItem('lifecycle-theme') || 'dark';
        document.documentElement.setAttribute('data-theme', saved);
        this.updateThemeIcons(saved);
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
        document.querySelectorAll('.nav-link').forEach(l => l.classList.toggle('active', l.dataset.page === page));
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
            content.innerHTML = `<div class="empty-state"><div class="empty-state-icon">⚠️</div><div class="empty-state-text">Error loading page</div><div class="empty-state-hint">${esc(err.message)}</div></div>`;
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
            case 'qa': return this.renderQA();
            default: return '<div class="empty-state"><div class="empty-state-icon">🤷</div><div class="empty-state-text">Page not found</div></div>';
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
        const [status, features, milestones] = await Promise.all([
            this.api('status'), this.api('features'), this.api('milestones'),
        ]);
        const counts = status.feature_counts || {};
        const total = Object.values(counts).reduce((a, b) => a + b, 0);

        const statuses = ['draft', 'planning', 'implementing', 'agent-qa', 'human-qa', 'done', 'blocked'];
        const statusLabels = {draft:'Draft',planning:'Planning',implementing:'Implementing','agent-qa':'Agent QA','human-qa':'Human QA',done:'Done',blocked:'Blocked'};
        const kanbanCols = statuses.map(s => {
            const items = features.filter(f => f.status === s);
            return `<div class="kanban-column kanban-column-${s}">
                <div class="kanban-header"><span class="kanban-title">${statusLabels[s]||s}</span><span class="kanban-count">${items.length}</span></div>
                ${items.map(f => `<div class="kanban-card"><div class="kanban-card-title">${esc(f.name)}</div><div class="kanban-card-meta"><span class="kanban-card-priority p${f.priority}"></span>P${f.priority}${f.milestone_name ? ' · ' + esc(f.milestone_name) : ''}</div></div>`).join('') || '<div class="kanban-empty"><div class="kanban-empty-icon">○</div>No items</div>'}
            </div>`;
        }).join('');

        const milestoneCards = milestones.map(m => {
            const done = m.done_features || 0;
            const total = m.total_features || 0;
            const pct = total > 0 ? Math.round((done / total) * 100) : 0;
            return `<div class="card"><div class="card-header"><span class="card-title">${esc(m.name)}</span><span class="badge badge-${m.status}">${m.status}</span></div>
                <div class="progress-bar"><div class="progress-fill ${pct===100?'success':''}" style="width:${pct}%"></div></div>
                <div style="font-size:0.8rem;color:var(--text-muted)">${done}/${total} features · ${pct}%</div></div>`;
        }).join('');

        const events = (status.recent_events || []).slice(0, 8).map(e => `
            <div class="activity-item">
                <div class="activity-icon">${eventIcon(e.event_type)}</div>
                <div class="activity-content">
                    <div class="activity-text">${fmtEvent(e.event_type)}${e.feature_id ? ' <span style="color:var(--accent)">' + esc(e.feature_id) + '</span>' : ''}</div>
                    <div class="activity-time">${fmtTime(e.created_at)}</div>
                </div>
            </div>`).join('');

        return `<div class="page-header"><h2 class="page-title">${esc(status.project?.name || 'Project')} Dashboard</h2><p class="page-subtitle">Project overview and health at a glance</p></div>
            <div class="stats-grid">
                <div class="stat-card stat-card--accent"><div class="stat-card-info"><div class="stat-value">${total}</div><div class="stat-label">Total Features</div></div><div class="stat-icon">📦</div></div>
                <div class="stat-card stat-card--success"><div class="stat-card-info"><div class="stat-value">${counts.done||0}</div><div class="stat-label">Completed</div></div><div class="stat-icon">✅</div></div>
                <div class="stat-card stat-card--warning"><div class="stat-card-info"><div class="stat-value">${counts.implementing||0}</div><div class="stat-label">In Progress</div></div><div class="stat-icon">🔨</div></div>
                <div class="stat-card stat-card--purple"><div class="stat-card-info"><div class="stat-value">${status.active_cycles||0}</div><div class="stat-label">Active Cycles</div></div><div class="stat-icon">🔄</div></div>
            </div>
            <div class="card" style="margin-bottom:24px"><div class="card-title" style="margin-bottom:16px">Feature Board</div><div class="kanban">${kanbanCols}</div></div>
            <div class="two-col">
                <div><div class="card"><div class="card-title" style="margin-bottom:12px">Milestones</div>${milestoneCards || '<div style="color:var(--text-muted);font-size:0.85rem">No milestones yet</div>'}</div></div>
                <div><div class="card"><div class="card-title" style="margin-bottom:12px">Recent Activity</div>${events || '<div style="color:var(--text-muted);font-size:0.85rem">No activity yet</div>'}</div></div>
            </div>`;
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
        const labels = { 1: 'Critical', 2: 'High', 3: 'Medium', 4: 'Low', 5: 'Lowest' };
        return labels[p] || `P${p}`;
    },

    buildFeaturesTable(features) {
        if (!features.length) return '<div style="text-align:center;padding:40px;color:var(--text-muted);font-size:0.875rem">No features match the current filters</div>';
        const rows = features.map(f => {
            const prog = this.featureProgress(f.status);
            const pClass = f.priority <= 5 ? f.priority : 5;
            const desc = f.description ? esc(f.description).substring(0, 80) + (f.description.length > 80 ? '…' : '') : '';
            return `<tr class="ft-row status-${f.status}">
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
        </tr>`;
        }).join('');
        return `<table class="table"><thead><tr><th>Feature</th><th>Status</th><th>Priority</th><th>Milestone</th><th>Progress</th><th>Created</th></tr></thead><tbody>${rows}</tbody></table>`;
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

        if (!features.length) return `<div class="page-header"><h2 class="page-title">Features</h2></div><div class="empty-state"><div class="empty-state-icon">✨</div><div class="empty-state-text">No features yet</div><div class="empty-state-hint">Use <code>lifecycle feature add &lt;name&gt;</code> to create one</div></div>`;

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
                <div class="features-search-wrap"><input type="text" class="features-search" placeholder="Search features…" id="featuresSearch"></div>
            </div>
            <div class="card" id="featuresTableWrap">${this.buildFeaturesTable(features)}</div>`;
    },

    // ── Roadmap ──
    async renderRoadmap() {
        const items = await this.api('roadmap');
        if (!items.length) return `<div class="page-header"><h2 class="page-title">Roadmap</h2></div><div class="empty-state"><div class="empty-state-icon">🗺️</div><div class="empty-state-text">No roadmap items yet</div><div class="empty-state-hint">Use <code>lifecycle roadmap add &lt;title&gt;</code> to create one</div></div>`;

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

        function catCls(c) {
            if (!c) return 'roadmap-cat-0';
            let h = 0;
            for (let i = 0; i < c.length; i++) h = c.charCodeAt(i) + ((h << 5) - h);
            return 'roadmap-cat-' + (Math.abs(h) % 6);
        }

        let rank = 0;
        const sections = pris.filter(p => grouped[p]).map(pri => {
            const ritems = grouped[pri];
            return `<div class="roadmap-section pri-${pri}">
                <div class="roadmap-priority-header pri-${pri}"><span class="roadmap-priority-icon">${icons[pri]}</span><span class="roadmap-priority-label">${pri.replace('-',' ')}</span><span class="roadmap-priority-count">${ritems.length} item${ritems.length !== 1 ? 's' : ''}</span></div>
                <div class="roadmap-items">${ritems.map((r,i) => {
                    rank++;
                    return `<div class="roadmap-item st-${r.status}" style="animation-delay:${i*0.06}s">
                        <div class="roadmap-item-number">${rank}</div>
                        <div class="roadmap-item-content"><div class="roadmap-item-title">${esc(r.title)}</div>${r.description?`<div class="roadmap-item-desc">${esc(r.description)}</div>`:''}</div>
                        <div class="roadmap-item-meta">${r.category?`<span class="roadmap-category ${catCls(r.category)}">${esc(r.category)}</span>`:''}<span class="badge badge-${r.status}">${r.status}</span></div>
                    </div>`;
                }).join('')}</div>
            </div>`;
        }).join('');

        return `<div class="page-header"><h2 class="page-title">Roadmap</h2><p class="page-subtitle">Product vision and prioritized backlog</p></div>
            <div class="roadmap-summary">
                <div class="roadmap-summary-stat"><div class="roadmap-summary-value">${items.length}</div><div class="roadmap-summary-label">Total Items</div></div>
                <div class="roadmap-summary-stat"><div class="roadmap-summary-value" style="color:var(--warning)">${inProg}</div><div class="roadmap-summary-label">In Progress</div></div>
                <div class="roadmap-summary-stat"><div class="roadmap-summary-value" style="color:var(--success)">${done}</div><div class="roadmap-summary-label">Completed</div></div>
                <div class="roadmap-summary-stat"><div class="roadmap-summary-value" style="color:var(--accent)">${accepted}</div><div class="roadmap-summary-label">Accepted</div></div>
                <div class="roadmap-summary-stat"><div class="roadmap-summary-value" style="color:var(--purple)">${pct}%</div><div class="roadmap-summary-label">Progress</div></div>
            </div>${sections}`;
    },

    // ── Cycles ──
    async renderCycles() {
        const cycles = await this.api('cycles');
        if (!cycles.length) return `<div class="page-header"><h2 class="page-title">Iteration Cycles</h2></div><div class="empty-state"><div class="empty-state-icon">🔄</div><div class="empty-state-text">No active cycles</div><div class="empty-state-hint">Use <code>lifecycle cycle start &lt;type&gt; &lt;feature&gt;</code></div></div>`;

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

        return `<div class="page-header"><h2 class="page-title">Iteration Cycles</h2><p class="page-subtitle">${cycles.length} active</p></div>` +
            cycles.map(c => {
                const steps = ctSteps[c.cycle_type] || [];
                const pct = steps.length > 0 ? Math.round((c.current_step / steps.length) * 100) : 0;
                const scoreVal = c.score != null ? parseFloat(c.score) : null;
                const scoreCls = scoreVal != null ? (scoreVal >= 7 ? 'score-high' : scoreVal >= 4 ? 'score-mid' : 'score-low') : '';

                const pipeline = steps.map((s, i) => {
                    const state = i < c.current_step ? 'done' : i === c.current_step ? 'active' : '';
                    const indicator = state === 'done' ? '✓' : (i + 1);
                    return `<div class="cycle-node ${state}"><div class="cycle-node-indicator">${indicator}</div><div class="cycle-node-label">${s.replace(/-/g, ' ')}</div></div>`;
                }).join('');

                return `<div class="card cycle-card">
                    <div class="card-header"><span class="card-title">${esc(c.feature_id)}</span><span class="badge badge-${c.status}">${c.status}</span></div>
                    <div class="cycle-meta">
                        <span class="cycle-type-name">${c.cycle_type.replace(/-/g, ' ')}</span>
                        <span class="cycle-iteration-badge">⟳ Iteration ${c.iteration}</span>
                        ${scoreVal != null ? `<span class="cycle-score ${scoreCls}">★ ${scoreVal.toFixed(1)}</span>` : ''}
                    </div>
                    <div class="cycle-pipeline">${pipeline}</div>
                    <div class="cycle-progress"><div class="cycle-progress-fill" style="width:${pct}%"></div></div>
                </div>`;
            }).join('');
    },

    // ── History ──
    async renderHistory() {
        const events = await this.api('history');
        if (!events.length) return `<div class="page-header"><h2 class="page-title">History</h2></div><div class="empty-state"><div class="empty-state-icon">📜</div><div class="empty-state-text">No events yet</div></div>`;

        // Group events by date
        const grouped = {};
        events.forEach(e => {
            const day = e.created_at ? new Date(e.created_at).toLocaleDateString('en-US', { weekday: 'long', month: 'long', day: 'numeric', year: 'numeric' }) : 'Unknown';
            (grouped[day] = grouped[day] || []).push(e);
        });

        let idx = 0;
        const sections = Object.entries(grouped).map(([date, items]) => {
            const rows = items.map(e => {
                const delay = idx++ * 0.06;
                let detailHtml = '';
                if (e.data) {
                    try {
                        const d = typeof e.data === 'string' ? JSON.parse(e.data) : e.data;
                        detailHtml = Object.entries(d).map(([k,v]) =>
                            `<span class="timeline-detail-item"><span class="timeline-detail-key">${esc(k)}</span><span class="timeline-detail-val">${esc(String(v))}</span></span>`
                        ).join('');
                    } catch(_) { detailHtml = `<span class="timeline-detail-val">${esc(e.data)}</span>`; }
                }
                return `<div class="timeline-item ${eventClass(e.event_type)}" style="animation-delay:${delay}s">
                    <div class="timeline-dot">${eventIcon(e.event_type)}</div>
                    <div class="timeline-time">${fmtRelTime(e.created_at)}</div>
                    <div class="timeline-event"><span>${fmtEvent(e.event_type)}</span>${e.feature_id ? '<span class="feature-badge">' + esc(e.feature_id) + '</span>' : ''}</div>
                    ${detailHtml ? `<div class="timeline-detail">${detailHtml}</div>` : ''}
                </div>`;
            }).join('');
            return `<div class="timeline-date-group"><div class="timeline-date-sep"><span class="timeline-date-label">${esc(date)}</span></div>${rows}</div>`;
        }).join('');

        return `<div class="page-header"><h2 class="page-title">History</h2><p class="page-subtitle">${events.length} events</p></div>
            <div class="card"><div class="timeline">${sections}</div></div>`;
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
            ? `<div class="card" style="padding:0;overflow:hidden">${reviewed.map(e => {
                const isApproved = e.event_type === 'qa.approved';
                return `<div class="qa-reviewed-item">
                    <div class="qa-reviewed-icon ${isApproved ? 'approved' : 'rejected'}">${isApproved ? '✓' : '✗'}</div>
                    <div class="qa-reviewed-name">${esc(e.feature_id || 'Unknown')}</div>
                    <div class="qa-reviewed-time">${fmtTime(e.created_at)}</div>
                </div>`;
            }).join('')}</div>`
            : '<div style="color:var(--text-muted);font-size:0.85rem;text-align:center;padding:20px">No reviews yet</div>';

        if (!features.length) {
            return `<div class="page-header"><h2 class="page-title">Quality Assurance</h2></div>
                <div class="qa-layout">
                    <div>
                        <div class="empty-state" style="padding:40px">
                            <div class="empty-state-icon">🎉</div>
                            <div class="empty-state-text">All clear — nothing to review!</div>
                            <div class="empty-state-hint">Features will appear here when they reach the QA stage</div>
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
            <textarea class="qa-notes" data-feature="${esc(f.id)}" placeholder="Add review notes (optional)..."></textarea>
            <div class="qa-actions">
                <button class="btn-approve qa-approve" data-feature="${esc(f.id)}">✓ Approve</button>
                <button class="btn-reject qa-reject" data-feature="${esc(f.id)}">✗ Reject</button>
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

    bindPageEvents(page) {
        if (page === 'features') {
            const refresh = () => {
                const wrap = document.getElementById('featuresTableWrap');
                if (wrap) wrap.innerHTML = this.buildFeaturesTable(this.getFilteredFeatures());
            };
            document.querySelectorAll('.filter-pill').forEach(btn => btn.addEventListener('click', () => {
                document.querySelectorAll('.filter-pill').forEach(b => b.classList.remove('active'));
                btn.classList.add('active');
                this._featuresFilter = btn.dataset.status;
                refresh();
            }));
            const searchInput = document.getElementById('featuresSearch');
            if (searchInput) searchInput.addEventListener('input', (e) => { this._featuresSearch = e.target.value; refresh(); });
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
        if (!c) { c = document.createElement('div'); c.className = 'toast-container'; document.body.appendChild(c); }
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
function eventIcon(t) { if(t.includes('created'))return '⊕'; if(t.includes('completed')||t.includes('approved'))return '✔'; if(t.includes('failed')||t.includes('rejected'))return '✘'; if(t.includes('started'))return '▸'; if(t.includes('scored'))return '★'; if(t.includes('updated')||t.includes('edit'))return '✎'; if(t.includes('removed')||t.includes('deleted'))return '⊖'; return '●'; }
function eventClass(t) { if(t.includes('completed')||t.includes('approved'))return 'success'; if(t.includes('failed')||t.includes('rejected'))return 'danger'; if(t.includes('started')||t.includes('scored'))return 'warning'; if(t.includes('created'))return 'info'; if(t.includes('updated')||t.includes('edit'))return 'purple'; return ''; }
function fmtRelTime(iso) {
    if(!iso) return '';
    const d = new Date(iso), now = Date.now(), diff = now - d.getTime(), s = Math.floor(diff/1000);
    if(s < 60) return 'just now';
    if(s < 3600) { const m=Math.floor(s/60); return m + (m===1?' minute':' minutes') + ' ago'; }
    if(s < 86400) { const h=Math.floor(s/3600); return h + (h===1?' hour':' hours') + ' ago'; }
    if(s < 172800) return 'yesterday at ' + d.toLocaleTimeString('en-US',{hour:'2-digit',minute:'2-digit'});
    return d.toLocaleString('en-US',{month:'short',day:'numeric',hour:'2-digit',minute:'2-digit'});
}
function fmtEvent(t) { return t.split('.').map(s=>s.charAt(0).toUpperCase()+s.slice(1)).join(' '); }

document.addEventListener('DOMContentLoaded', () => App.init());
