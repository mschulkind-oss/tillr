import { useStore } from '../store'

export function Toasts() {
  const toasts = useStore((s) => s.toasts)
  const removeToast = useStore((s) => s.removeToast)

  if (toasts.length === 0) return null

  return (
    <div className="fixed bottom-6 right-6 z-50 flex flex-col gap-2">
      {toasts.map((toast) => (
        <div
          key={toast.id}
          className={`
            flex items-center gap-3 px-4 py-3 rounded-lg shadow-card-hover
            animate-in slide-in-from-right duration-300
            ${toast.type === 'success' ? 'bg-success/10 border border-success/30 text-success' : ''}
            ${toast.type === 'error' ? 'bg-danger/10 border border-danger/30 text-danger' : ''}
            ${toast.type === 'info' ? 'bg-accent/10 border border-accent/30 text-accent' : ''}
          `}
        >
          <span className="text-sm">{toast.message}</span>
          <button
            onClick={() => removeToast(toast.id)}
            className="ml-2 text-text-muted hover:text-text-primary"
          >
            ×
          </button>
        </div>
      ))}
    </div>
  )
}
