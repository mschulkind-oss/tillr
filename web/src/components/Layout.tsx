import { Outlet } from 'react-router-dom'
import { Sidebar } from './Sidebar'
import { NotificationBell } from './NotificationBell'
import { Toasts } from './Toast'
import { useStore } from '../store'
import { useWebSocket } from '../hooks/useWebSocket'

export function Layout() {
  useWebSocket()
  const setSidebarOpen = useStore((s) => s.setSidebarOpen)

  return (
    <div className="flex h-screen overflow-hidden">
      <Sidebar />

      <div className="flex-1 flex flex-col min-w-0 overflow-hidden">
        {/* Header bar */}
        <header className="flex items-center justify-between px-4 h-14 border-b border-border bg-bg-secondary shrink-0">
          <button
            onClick={() => setSidebarOpen(true)}
            className="lg:hidden p-2 text-text-secondary hover:text-text-primary"
          >
            ☰
          </button>
          <div className="flex-1" />
          <NotificationBell />
        </header>

        {/* Page content */}
        <main className="flex-1 overflow-y-auto p-6">
          <Outlet />
        </main>
      </div>

      <Toasts />
    </div>
  )
}
