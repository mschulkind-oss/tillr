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
            document.getElementById('themeToggle').textContent = next === 'dark' ? '🌙' : '☀️';
        });
    },

    loadTheme() {
        const saved = localStorage.getItem('lifecycle-theme') || 'dark';
        document.documentElement.setAttribute('data-theme', saved);
        document.getElementById('themeToggle').textContent = saved === 'dark' ? '🌙' : '☀️';
    },

    async navigate(page) {
        this.currentPage = page;
        document.querySelectorAll('.nav-link').forEach(l => l.classList.toggle('active', l.dataset.page === page));
        const content = document.getElementById('content');
        content.innerHTML = '<div style="text-align:center;padding:60px;color:var(--text-muted)">Loading...</div>';
        try {
            content.innerHTML = await this.renderPage(page);
            this.bindPageEvents(page);
        } catch (err) {
            content.innerHTML = `<div class="empty-state"><div class="empty-state-icon">⚠️</div><div class="empty-state-text">Error loading page</div><div class="empty-state-hint">${esc(err.message)}</div></div>`;
        }
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
        const kanbanCols = statuses.map(s => {
            const items = features.filter(f => f.status === s);
            return `<div class="kanban-column">
                <div class="kanban-header"><span class="kanban-title">${s}</span><span class="kanban-count">${items.length}</span></div>
                ${items.map(f => `<div class="kanban-card"><div class="kanban-card-title">${esc(f.name)}</div><div class="kanban-card-meta">P${f.priority}${f.milestone_name ? ' · ' + esc(f.milestone_name) : ''}</div></div>`).join('') || '<div style="text-align:center;padding:20px;color:var(--text-muted);font-size:0.8rem">No items</div>'}
            </div>`;
        }).join('');

        const milestoneCards = milestones.map(m => {
            const pct = m.total_features > 0 ? Math.round((m.done_features / m.total_features) * 100) : 0;
            return `<div class="card"><div class="card-header"><span class="card-title">${esc(m.name)}</span><span class="badge badge-${m.status}">${m.status}</span></div>
                <div class="progress-bar"><div class="progress-fill ${pct===100?'success':''}" style="width:${pct}%"></div></div>
                <div style="font-size:0.8rem;color:var(--text-muted)">${m.done_features}/${m.total_features} features · ${pct}%</div></div>`;
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
                <div class="stat-card"><div class="stat-value">${total}</div><div class="stat-label">Total Features</div></div>
                <div class="stat-card"><div class="stat-value" style="color:var(--success)">${counts.done||0}</div><div class="stat-label">Completed</div></div>
                <div class="stat-card"><div class="stat-value" style="color:var(--warning)">${counts.implementing||0}</div><div class="stat-label">In Progress</div></div>
                <div class="stat-card"><div class="stat-value" style="color:var(--purple)">${status.active_cycles||0}</div><div class="stat-label">Active Cycles</div></div>
            </div>
            <div class="card" style="margin-bottom:24px"><div class="card-title" style="margin-bottom:16px">Feature Board</div><div class="kanban">${kanbanCols}</div></div>
            <div class="two-col">
                <div><div class="card"><div class="card-title" style="margin-bottom:12px">Milestones</div>${milestoneCards || '<div style="color:var(--text-muted);font-size:0.85rem">No milestones yet</div>'}</div></div>
                <div><div class="card"><div class="card-title" style="margin-bottom:12px">Recent Activity</div>${events || '<div style="color:var(--text-muted);font-size:0.85rem">No activity yet</div>'}</div></div>
            </div>`;
    },

    // ── Features ──
    async renderFeatures() {
        const features = await this.api('features');
        if (!features.length) return `<div class="page-header"><h2 class="page-title">Features</h2></div><div class="empty-state"><div class="empty-state-icon">✨</div><div class="empty-state-text">No features yet</div><div class="empty-state-hint">Use <code>lifecycle feature add &lt;name&gt;</code> to create one</div></div>`;

        const rows = features.map(f => `<tr>
            <td><strong>${esc(f.name)}</strong><br><span style="color:var(--text-muted);font-size:0.75rem">${esc(f.id)}</span></td>
            <td><span class="badge badge-${f.status}">${f.status}</span></td>
            <td>${f.priority}</td>
            <td>${esc(f.milestone_name||'—')}</td>
            <td style="color:var(--text-muted)">${fmtDate(f.created_at)}</td>
        </tr>`).join('');

        return `<div class="page-header"><h2 class="page-title">Features</h2><p class="page-subtitle">${features.length} features tracked</p></div>
            <div class="card"><table class="table"><thead><tr><th>Feature</th><th>Status</th><th>Priority</th><th>Milestone</th><th>Created</th></tr></thead><tbody>${rows}</tbody></table></div>`;
    },

    // ── Roadmap ──
    async renderRoadmap() {
        const items = await this.api('roadmap');
        if (!items.length) return `<div class="page-header"><h2 class="page-title">Roadmap</h2></div><div class="empty-state"><div class="empty-state-icon">🗺️</div><div class="empty-state-text">No roadmap items yet</div><div class="empty-state-hint">Use <code>lifecycle roadmap add &lt;title&gt;</code> to create one</div></div>`;

        const pris = ['critical','high','medium','low','nice-to-have'];
        const icons = {critical:'🔴',high:'🟠',medium:'🟡',low:'🟢','nice-to-have':'🔵'};
        const grouped = {};
        items.forEach(r => { (grouped[r.priority] = grouped[r.priority] || []).push(r); });

        return `<div class="page-header"><h2 class="page-title">Roadmap</h2><p class="page-subtitle">${items.length} items across ${Object.keys(grouped).length} priority levels</p></div>` +
            pris.filter(p => grouped[p]).map(pri => {
                const ritems = grouped[pri];
                return `<div class="roadmap-section">
                    <div class="roadmap-priority-header"><span class="roadmap-priority-icon">${icons[pri]}</span><span class="roadmap-priority-label">${pri}</span><span style="color:var(--text-muted);font-size:0.85rem">${ritems.length} items</span></div>
                    ${ritems.map((r,i) => `<div class="roadmap-item">
                        <div class="roadmap-item-number">${i+1}</div>
                        <div class="roadmap-item-content"><div class="roadmap-item-title">${esc(r.title)}</div>${r.description?`<div class="roadmap-item-desc">${esc(r.description)}</div>`:''}</div>
                        <div class="roadmap-item-meta">${r.category?`<span class="roadmap-category">${esc(r.category)}</span>`:''}<span class="badge badge-${r.status}">${r.status}</span></div>
                    </div>`).join('')}
                </div>`;
            }).join('');
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
                return `<div class="card"><div class="card-header"><span class="card-title">${esc(c.feature_id)}</span><span class="badge badge-${c.status}">${c.status}</span></div>
                    <div style="font-size:0.85rem;color:var(--text-secondary);margin-bottom:8px">${c.cycle_type} · Iteration ${c.iteration}</div>
                    <div class="cycle-steps">${steps.map((s,i) => `<div class="cycle-step ${i<c.current_step?'done':i===c.current_step?'active':''}">${s}</div>`).join('')}</div></div>`;
            }).join('');
    },

    // ── History ──
    async renderHistory() {
        const events = await this.api('history');
        if (!events.length) return `<div class="page-header"><h2 class="page-title">History</h2></div><div class="empty-state"><div class="empty-state-icon">📜</div><div class="empty-state-text">No events yet</div></div>`;

        return `<div class="page-header"><h2 class="page-title">History</h2><p class="page-subtitle">${events.length} events</p></div>
            <div class="card"><div class="timeline">${events.map(e => {
                let detail = '';
                if (e.data) { try { const d = JSON.parse(e.data); detail = Object.entries(d).map(([k,v])=>k+': '+v).join(' · '); } catch(_){detail=e.data;} }
                return `<div class="timeline-item ${eventClass(e.event_type)}">
                    <div class="timeline-time">${fmtTime(e.created_at)}</div>
                    <div class="timeline-event">${eventIcon(e.event_type)} ${fmtEvent(e.event_type)}${e.feature_id?' <span style="color:var(--accent)">'+esc(e.feature_id)+'</span>':''}</div>
                    ${detail?`<div class="timeline-detail">${esc(detail)}</div>`:''}
                </div>`;
            }).join('')}</div></div>`;
    },

    // ── QA ──
    async renderQA() {
        const features = await this.api('features?status=human-qa');
        if (!features.length) return `<div class="page-header"><h2 class="page-title">Quality Assurance</h2></div><div class="empty-state"><div class="empty-state-icon">✅</div><div class="empty-state-text">All clear!</div><div class="empty-state-hint">No features pending QA review</div></div>`;

        return `<div class="page-header"><h2 class="page-title">Quality Assurance</h2><p class="page-subtitle">${features.length} features awaiting review</p></div>` +
            features.map(f => `<div class="card"><div class="card-header"><span class="card-title">${esc(f.name)}</span><span class="badge badge-human-qa">awaiting QA</span></div>
                <div style="font-size:0.85rem;color:var(--text-secondary);margin-bottom:12px">${esc(f.description||'No description')}</div>
                <div style="display:flex;gap:8px">
                    <button class="qa-approve" data-feature="${esc(f.id)}" style="padding:6px 16px;border-radius:6px;border:1px solid var(--success);background:transparent;color:var(--success);cursor:pointer;font-weight:600">✓ Approve</button>
                    <button class="qa-reject" data-feature="${esc(f.id)}" style="padding:6px 16px;border-radius:6px;border:1px solid var(--danger);background:transparent;color:var(--danger);cursor:pointer;font-weight:600">✗ Reject</button>
                </div></div>`).join('');
    },

    bindPageEvents(page) {
        if (page === 'qa') {
            document.querySelectorAll('.qa-approve').forEach(btn => btn.addEventListener('click', async () => { await this.apiPost('qa/'+btn.dataset.feature+'/approve',{notes:'Approved via web'}); this.navigate('qa'); }));
            document.querySelectorAll('.qa-reject').forEach(btn => btn.addEventListener('click', async () => { await this.apiPost('qa/'+btn.dataset.feature+'/reject',{notes:'Rejected via web'}); this.navigate('qa'); }));
        }
    },
};

function esc(s) { if(!s) return ''; const d=document.createElement('div'); d.textContent=s; return d.innerHTML; }
function fmtDate(iso) { if(!iso) return '—'; return new Date(iso).toLocaleDateString('en-US',{month:'short',day:'numeric',year:'numeric'}); }
function fmtTime(iso) { if(!iso) return ''; return new Date(iso).toLocaleString('en-US',{month:'short',day:'numeric',hour:'2-digit',minute:'2-digit'}); }
function eventIcon(t) { if(t.includes('created'))return '+'; if(t.includes('completed')||t.includes('approved'))return '✓'; if(t.includes('failed')||t.includes('rejected'))return '✗'; if(t.includes('started'))return '▶'; if(t.includes('scored'))return '★'; return '·'; }
function eventClass(t) { if(t.includes('completed')||t.includes('approved'))return 'success'; if(t.includes('failed')||t.includes('rejected'))return 'danger'; if(t.includes('started')||t.includes('scored'))return 'warning'; return ''; }
function fmtEvent(t) { return t.split('.').map(s=>s.charAt(0).toUpperCase()+s.slice(1)).join(' '); }

document.addEventListener('DOMContentLoaded', () => App.init());
