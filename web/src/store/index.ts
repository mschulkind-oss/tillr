// Post-reset minimal client state. Theme, sidebar, toasts.
// Notifications / help-modal / hotkeys removed; reintroduce per the
// consulting-firm roadmap.
import { create } from 'zustand'
import { persist } from 'zustand/middleware'

interface Toast {
  id: string
  message: string
  type: 'success' | 'error' | 'info'
}

interface AppState {
  theme: 'dark' | 'light'
  toggleTheme: () => void

  sidebarOpen: boolean
  setSidebarOpen: (open: boolean) => void

  toasts: Toast[]
  addToast: (message: string, type?: Toast['type']) => void
  removeToast: (id: string) => void
}

export const useStore = create<AppState>()(
  persist(
    (set) => ({
      theme: 'dark',
      toggleTheme: () =>
        set((s) => {
          const next = s.theme === 'dark' ? 'light' : 'dark'
          document.documentElement.setAttribute('data-theme', next)
          return { theme: next }
        }),

      sidebarOpen: true,
      setSidebarOpen: (open) => set({ sidebarOpen: open }),

      toasts: [],
      addToast: (message, type = 'info') => {
        const id = Date.now().toString()
        set((s) => ({ toasts: [...s.toasts, { id, message, type }] }))
        setTimeout(() => {
          set((s) => ({ toasts: s.toasts.filter((t) => t.id !== id) }))
        }, 4000)
      },
      removeToast: (id) =>
        set((s) => ({ toasts: s.toasts.filter((t) => t.id !== id) })),
    }),
    {
      name: 'tillr-store',
      partialize: (state) => ({ theme: state.theme }),
    },
  ),
)
