import { useEffect, useRef } from 'react'

export interface HotkeyConfig {
  key: string
  ctrl?: boolean
  shift?: boolean
  alt?: boolean
  meta?: boolean
  handler: () => void
}

function isEditableElement(el: EventTarget | null): boolean {
  if (!el || !(el instanceof HTMLElement)) return false
  const tag = el.tagName
  if (tag === 'INPUT' || tag === 'TEXTAREA' || tag === 'SELECT') return true
  if (el.isContentEditable) return true
  return false
}

/**
 * Simple hotkey hook that registers global keydown listeners.
 * Automatically disabled when user is typing in inputs/textareas.
 * Supports modifier keys and a raw key handler for sequence-based shortcuts.
 */
export function useHotkeys(
  hotkeys: HotkeyConfig[],
  deps: unknown[] = []
) {
  const hotkeysRef = useRef(hotkeys)
  hotkeysRef.current = hotkeys

  useEffect(() => {
    function handler(e: KeyboardEvent) {
      if (isEditableElement(e.target)) return

      for (const hk of hotkeysRef.current) {
        const ctrlMatch = (hk.ctrl ?? false) === (e.ctrlKey || e.metaKey)
        const shiftMatch = (hk.shift ?? false) === e.shiftKey
        const altMatch = (hk.alt ?? false) === e.altKey

        if (e.key === hk.key && ctrlMatch && shiftMatch && altMatch) {
          e.preventDefault()
          hk.handler()
          return
        }
      }
    }

    window.addEventListener('keydown', handler)
    return () => window.removeEventListener('keydown', handler)
  }, deps)
}

/**
 * Low-level hook: calls onKey for every non-input keydown event.
 * Used by the KeyboardShortcuts component for sequence detection.
 */
export function useRawKeydown(
  onKey: (e: KeyboardEvent) => void,
  deps: unknown[] = []
) {
  const onKeyRef = useRef(onKey)
  onKeyRef.current = onKey

  useEffect(() => {
    function handler(e: KeyboardEvent) {
      if (isEditableElement(e.target)) return
      onKeyRef.current(e)
    }

    window.addEventListener('keydown', handler)
    return () => window.removeEventListener('keydown', handler)
  }, deps)
}
