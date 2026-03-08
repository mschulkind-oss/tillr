import { create } from 'zustand'
import { persist } from 'zustand/middleware'

export interface Notification {
  id: string
  type: 'info' | 'success' | 'warning' | 'error'
  message: string
  timestamp: string
  read: boolean
}

interface Toast {
  id: string
  message: string
  type: 'success' | 'error' | 'info'
}

interface AppState {
  // Theme
  theme: 'dark' | 'light'
  toggleTheme: () => void

  // Sidebar
  sidebarOpen: boolean
  setSidebarOpen: (open: boolean) => void

  // Notifications
  notifications: Notification[]
  addNotification: (n: Notification) => void
  markAllRead: () => void
  clearNotifications: () => void
  unreadCount: () => number

  // Toasts
  toasts: Toast[]
  addToast: (message: string, type?: Toast['type']) => void
  removeToast: (id: string) => void
}

export const useStore = create<AppState>()(
  persist(
    (set, get) => ({
      // Theme
      theme: 'dark',
      toggleTheme: () =>
        set((s) => {
          const next = s.theme === 'dark' ? 'light' : 'dark'
          document.documentElement.setAttribute('data-theme', next)
          return { theme: next }
        }),

      // Sidebar
      sidebarOpen: true,
      setSidebarOpen: (open) => set({ sidebarOpen: open }),

      // Notifications
      notifications: [],
      addNotification: (n) =>
        set((s) => ({
          notifications: [n, ...s.notifications].slice(0, 50),
        })),
      markAllRead: () =>
        set((s) => ({
          notifications: s.notifications.map((n) => ({ ...n, read: true })),
        })),
      clearNotifications: () => set({ notifications: [] }),
      unreadCount: () => get().notifications.filter((n) => !n.read).length,

      // Toasts
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
      name: 'lifecycle-store',
      partialize: (state) => ({ theme: state.theme }),
    }
  )
)
