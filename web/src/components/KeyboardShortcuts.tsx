import { useCallback, useRef } from 'react'
import { useNavigate } from 'react-router-dom'
import { useRawKeydown } from '../hooks/useHotkeys'
import { useStore } from '../store'

const NAV_MAP: Record<string, string> = {
  d: '/dashboard',
  f: '/features',
  r: '/roadmap',
  c: '/cycles',
  a: '/agents',
  w: '/workflow',
  i: '/ideas',
  q: '/qa',
  s: '/stats',
  h: '/history',
}

/**
 * Provides global keyboard shortcuts for the app.
 * Uses a simple state machine for two-key sequences (g + <key>).
 * Renders nothing visible.
 */
export function KeyboardShortcuts() {
  const navigate = useNavigate()
  const toggleTheme = useStore((s) => s.toggleTheme)
  const helpModalOpen = useStore((s) => s.helpModalOpen)
  const setHelpModalOpen = useStore((s) => s.setHelpModalOpen)

  // State machine for "g" prefix sequences
  const prefixRef = useRef<string | null>(null)
  const prefixTimerRef = useRef<ReturnType<typeof setTimeout> | null>(null)

  // List navigation state: managed via DOM data attributes
  const highlightedRef = useRef<number>(-1)

  const clearPrefix = useCallback(() => {
    prefixRef.current = null
    if (prefixTimerRef.current) {
      clearTimeout(prefixTimerRef.current)
      prefixTimerRef.current = null
    }
  }, [])

  const setPrefix = useCallback((key: string) => {
    prefixRef.current = key
    if (prefixTimerRef.current) clearTimeout(prefixTimerRef.current)
    prefixTimerRef.current = setTimeout(() => {
      prefixRef.current = null
    }, 1000)
  }, [])

  useRawKeydown(
    (e: KeyboardEvent) => {
      const key = e.key

      // Escape: close help modal or any open dialog
      if (key === 'Escape') {
        if (helpModalOpen) {
          setHelpModalOpen(false)
          e.preventDefault()
        }
        clearPrefix()
        return
      }

      // Don't process other shortcuts when help modal is open (except Escape above)
      // But allow ? to toggle it off
      if (key === '?' || (key === '/' && e.shiftKey)) {
        setHelpModalOpen(!helpModalOpen)
        e.preventDefault()
        clearPrefix()
        return
      }

      if (helpModalOpen) return

      // Two-key sequences: if prefix is active, try to resolve
      if (prefixRef.current === 'g') {
        const path = NAV_MAP[key]
        if (path) {
          navigate(path)
          e.preventDefault()
        }
        clearPrefix()
        return
      }

      // Start "g" prefix
      if (key === 'g' && !e.ctrlKey && !e.metaKey && !e.altKey) {
        setPrefix('g')
        return
      }

      // Single-key shortcuts
      switch (key) {
        case '/': {
          // Focus search input if it exists
          const searchInput = document.querySelector<HTMLInputElement>(
            'input[type="search"], input[placeholder*="earch"]'
          )
          if (searchInput) {
            searchInput.focus()
            e.preventDefault()
          }
          break
        }

        case 't':
          if (!e.ctrlKey && !e.metaKey && !e.altKey) {
            toggleTheme()
            e.preventDefault()
          }
          break

        case 'j':
        case 'k': {
          // Navigate list items: look for [data-list-item] elements
          const items = document.querySelectorAll<HTMLElement>('[data-list-item]')
          if (items.length === 0) break

          if (key === 'j') {
            highlightedRef.current = Math.min(highlightedRef.current + 1, items.length - 1)
          } else {
            highlightedRef.current = Math.max(highlightedRef.current - 1, 0)
          }

          // Update visual highlight
          items.forEach((el, i) => {
            if (i === highlightedRef.current) {
              el.setAttribute('data-kb-highlighted', 'true')
              el.scrollIntoView({ block: 'nearest' })
            } else {
              el.removeAttribute('data-kb-highlighted')
            }
          })
          e.preventDefault()
          break
        }

        case 'Enter': {
          // Open the highlighted list item
          const highlighted = document.querySelector<HTMLElement>(
            '[data-list-item][data-kb-highlighted="true"]'
          )
          if (highlighted) {
            // Try to find a link inside the item, or click the item itself
            const link = highlighted.querySelector<HTMLAnchorElement>('a')
            if (link) {
              link.click()
            } else {
              highlighted.click()
            }
            e.preventDefault()
          }
          break
        }
      }
    },
    [navigate, toggleTheme, helpModalOpen, setHelpModalOpen]
  )

  return null
}
