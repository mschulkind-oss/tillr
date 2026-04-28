import { NavLink } from 'react-router-dom'
import { useStore } from '../store'
import { isDaemonMode, getProjects, getActiveProject, setActiveProject } from '../api/projects'

interface NavItem {
  path: string
  label: string
  icon: string
}

// Post-reset MVP: Features, Personas, Retros. More items get added
// per the consulting-firm roadmap (see docs/consulting-firm/roadmap.md).
const items: NavItem[] = [
  { path: '/features', label: 'Features', icon: '✨' },
  { path: '/personas', label: 'Personas', icon: '🎭' },
  { path: '/retros', label: 'Retros', icon: '🔁' },
]

export function Sidebar() {
  const sidebarOpen = useStore((s) => s.sidebarOpen)
  const setSidebarOpen = useStore((s) => s.setSidebarOpen)
  const theme = useStore((s) => s.theme)
  const toggleTheme = useStore((s) => s.toggleTheme)

  return (
    <>
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
        <div className="flex items-center gap-2 px-4 h-14 border-b border-border">
          <span className="text-lg">🌱</span>
          <span className="font-semibold text-text-primary text-sm">Tillr</span>
        </div>

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

        <nav className="flex-1 overflow-y-auto py-2">
          {items.map((item) => (
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
        </nav>

        <div className="border-t border-border p-3">
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
