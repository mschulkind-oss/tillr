import { NavLink } from 'react-router-dom'
import { useStore } from '../store'
import { isDaemonMode, getProjects, getActiveProject, setActiveProject } from '../api/projects'

interface NavItem {
  path: string
  label: string
  icon: string
}

const sections: { title: string; items: NavItem[] }[] = [
  {
    title: 'Workspace',
    items: [
      { path: '/dashboard', label: 'Dashboard', icon: '📊' },
      { path: '/features', label: 'Features', icon: '✨' },
      { path: '/roadmap', label: 'Roadmap', icon: '🗺️' },
      { path: '/cycles', label: 'Cycles', icon: '🔄' },
      { path: '/agents', label: 'Agents', icon: '🤖' },
      { path: '/workstreams', label: 'Workstreams', icon: '🧵' },
      { path: '/workflow', label: 'Workflow', icon: '⚡' },
      { path: '/timeline', label: 'Timeline', icon: '📅' },
    ],
  },
  {
    title: 'Intake',
    items: [
      { path: '/ideas', label: 'Ideas', icon: '💡' },
      { path: '/context', label: 'Context', icon: '📎' },
    ],
  },
  {
    title: 'Review',
    items: [
      { path: '/discussions', label: 'Discussions', icon: '💬' },
      { path: '/decisions', label: 'Decisions', icon: '⚖️' },
      { path: '/history', label: 'History', icon: '📜' },
      { path: '/qa', label: 'QA', icon: '✅' },
    ],
  },
  {
    title: 'Insights',
    items: [
      { path: '/stats', label: 'Stats', icon: '📈' },
      { path: '/spec', label: 'Spec Doc', icon: '📄' },
    ],
  },
]

export function Sidebar() {
  const sidebarOpen = useStore((s) => s.sidebarOpen)
  const setSidebarOpen = useStore((s) => s.setSidebarOpen)
  const theme = useStore((s) => s.theme)
  const toggleTheme = useStore((s) => s.toggleTheme)
  const setHelpModalOpen = useStore((s) => s.setHelpModalOpen)

  return (
    <>
      {/* Mobile overlay */}
      {sidebarOpen && (
        <div
          className="fixed inset-0 bg-black/50 z-40 lg:hidden"
          onClick={() => setSidebarOpen(false)}
        />
      )}

      <aside
        className={`
          fixed top-0 left-0 h-full z-50 bg-sidebar-bg border-r border-border
          transition-transform duration-200 w-[220px]
          lg:translate-x-0 lg:relative lg:z-auto
          ${sidebarOpen ? 'translate-x-0' : '-translate-x-full'}
        `}
      >
        {/* Logo */}
        <div className="flex items-center gap-2 px-4 h-14 border-b border-border">
          <span className="text-lg">🌱</span>
          <span className="font-semibold text-text-primary text-sm">Tillr</span>
        </div>

        {/* Project switcher (daemon mode only) */}
        {isDaemonMode() && (
          <div className="px-3 py-2 border-b border-border">
            <select
              value={getActiveProject() || ''}
              onChange={(e) => {
                setActiveProject(e.target.value)
                window.location.reload()
              }}
              className="w-full px-2 py-1.5 rounded-md bg-bg-secondary border border-border text-text-primary text-sm cursor-pointer"
            >
              {getProjects().map((p) => (
                <option key={p.slug} value={p.slug}>
                  {p.name}
                </option>
              ))}
            </select>
          </div>
        )}

        {/* Navigation */}
        <nav className="flex-1 overflow-y-auto py-2">
          {sections.map((section) => (
            <div key={section.title} className="mb-2">
              <div className="px-4 py-1.5 text-[10px] font-semibold uppercase tracking-wider text-text-muted">
                {section.title}
              </div>
              {section.items.map((item) => (
                <NavLink
                  key={item.path}
                  to={item.path}
                  onClick={() => {
                    if (window.innerWidth < 1024) setSidebarOpen(false)
                  }}
                  className={({ isActive }) =>
                    `flex items-center gap-2.5 px-4 py-1.5 mx-2 rounded-md text-sm transition-colors ${
                      isActive
                        ? 'bg-sidebar-active/15 text-accent font-medium'
                        : 'text-text-secondary hover:bg-sidebar-hover hover:text-text-primary'
                    }`
                  }
                >
                  <span className="text-sm">{item.icon}</span>
                  <span>{item.label}</span>
                </NavLink>
              ))}
            </div>
          ))}
        </nav>

        {/* Footer */}
        <div className="border-t border-border p-3 space-y-1">
          <button
            onClick={() => setHelpModalOpen(true)}
            className="flex items-center gap-2 w-full px-2 py-1.5 rounded text-sm text-text-secondary hover:bg-sidebar-hover hover:text-text-primary transition-colors"
            title="Keyboard shortcuts (?)"
          >
            <span className="text-sm">&#x2328;</span>
            <span>Shortcuts</span>
            <kbd className="ml-auto text-[10px] px-1.5 py-0.5 rounded bg-bg-secondary border border-border text-text-muted font-mono">?</kbd>
          </button>
          <button
            onClick={toggleTheme}
            className="flex items-center gap-2 w-full px-2 py-1.5 rounded text-sm text-text-secondary hover:bg-sidebar-hover hover:text-text-primary transition-colors"
          >
            <span>{theme === 'dark' ? '☀️' : '🌙'}</span>
            <span>{theme === 'dark' ? 'Light mode' : 'Dark mode'}</span>
          </button>
        </div>
      </aside>
    </>
  )
}
