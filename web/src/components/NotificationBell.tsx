import { useStore } from '../store'
import { useState, useRef, useEffect } from 'react'

export function NotificationBell() {
  const notifications = useStore((s) => s.notifications)
  const markAllRead = useStore((s) => s.markAllRead)
  const clearNotifications = useStore((s) => s.clearNotifications)
  const unreadCount = useStore((s) => s.unreadCount)
  const [open, setOpen] = useState(false)
  const ref = useRef<HTMLDivElement>(null)

  const count = unreadCount()

  useEffect(() => {
    function handleClick(e: MouseEvent) {
      if (ref.current && !ref.current.contains(e.target as Node)) {
        setOpen(false)
      }
    }
    document.addEventListener('mousedown', handleClick)
    return () => document.removeEventListener('mousedown', handleClick)
  }, [])

  return (
    <div ref={ref} className="relative">
      <button
        onClick={() => {
          setOpen(!open)
          if (!open) markAllRead()
        }}
        className="relative p-2 rounded-md text-text-secondary hover:text-text-primary hover:bg-bg-hover transition-colors"
      >
        🔔
        {count > 0 && (
          <span className="absolute -top-0.5 -right-0.5 flex items-center justify-center w-4 h-4 text-[10px] font-bold bg-danger text-white rounded-full">
            {count > 9 ? '9+' : count}
          </span>
        )}
      </button>

      {open && (
        <div className="absolute right-0 top-full mt-2 w-80 bg-bg-card border border-border rounded-lg shadow-modal z-50 max-h-96 overflow-hidden flex flex-col">
          <div className="flex items-center justify-between px-4 py-3 border-b border-border">
            <span className="text-sm font-medium text-text-primary">Notifications</span>
            {notifications.length > 0 && (
              <button
                onClick={clearNotifications}
                className="text-xs text-text-muted hover:text-text-primary"
              >
                Clear all
              </button>
            )}
          </div>
          <div className="overflow-y-auto flex-1">
            {notifications.length === 0 ? (
              <div className="px-4 py-8 text-center text-sm text-text-muted">
                No notifications
              </div>
            ) : (
              notifications.slice(0, 20).map((n) => (
                <div
                  key={n.id}
                  className={`px-4 py-2.5 border-b border-border-light text-sm ${
                    n.read ? 'text-text-muted' : 'text-text-primary bg-bg-hover/30'
                  }`}
                >
                  <p className="text-xs">{n.message}</p>
                  <p className="text-[10px] text-text-muted mt-1">
                    {formatTime(n.timestamp)}
                  </p>
                </div>
              ))
            )}
          </div>
        </div>
      )}
    </div>
  )
}

function formatTime(iso: string): string {
  const d = new Date(iso)
  const now = new Date()
  const diff = now.getTime() - d.getTime()
  const mins = Math.floor(diff / 60000)
  if (mins < 1) return 'Just now'
  if (mins < 60) return `${mins}m ago`
  const hrs = Math.floor(mins / 60)
  if (hrs < 24) return `${hrs}h ago`
  return d.toLocaleDateString()
}
