import { useStore } from '../store'

interface ShortcutGroup {
  title: string
  shortcuts: { keys: string[]; description: string }[]
}

const groups: ShortcutGroup[] = [
  {
    title: 'Navigation',
    shortcuts: [
      { keys: ['g', 'd'], description: 'Go to Dashboard' },
      { keys: ['g', 'f'], description: 'Go to Features' },
      { keys: ['g', 'r'], description: 'Go to Roadmap' },
      { keys: ['g', 'c'], description: 'Go to Cycles' },
      { keys: ['g', 'a'], description: 'Go to Agents' },
      { keys: ['g', 'w'], description: 'Go to Workflow' },
      { keys: ['g', 'i'], description: 'Go to Ideas' },
      { keys: ['g', 'q'], description: 'Go to QA' },
      { keys: ['g', 's'], description: 'Go to Stats' },
      { keys: ['g', 'h'], description: 'Go to History' },
    ],
  },
  {
    title: 'Actions',
    shortcuts: [
      { keys: ['?'], description: 'Toggle this help' },
      { keys: ['/'], description: 'Focus search' },
      { keys: ['Esc'], description: 'Close modal / dialog' },
      { keys: ['j'], description: 'Next item in list' },
      { keys: ['k'], description: 'Previous item in list' },
      { keys: ['Enter'], description: 'Open highlighted item' },
      { keys: ['t'], description: 'Toggle theme' },
    ],
  },
]

function Kbd({ children }: { children: string }) {
  return (
    <kbd className="inline-flex items-center justify-center min-w-[24px] h-6 px-1.5 rounded bg-bg-secondary border border-border text-text-primary text-xs font-mono font-medium shadow-sm">
      {children}
    </kbd>
  )
}

export function HelpModal() {
  const open = useStore((s) => s.helpModalOpen)
  const setOpen = useStore((s) => s.setHelpModalOpen)

  if (!open) return null

  return (
    <div
      className="fixed inset-0 z-[100] flex items-center justify-center bg-black/60 backdrop-blur-sm"
      onClick={() => setOpen(false)}
    >
      <div
        className="bg-bg-primary border border-border rounded-xl shadow-2xl max-w-2xl w-full mx-4 max-h-[80vh] overflow-y-auto"
        onClick={(e) => e.stopPropagation()}
      >
        {/* Header */}
        <div className="flex items-center justify-between px-6 py-4 border-b border-border">
          <h2 className="text-lg font-semibold text-text-primary">Keyboard Shortcuts</h2>
          <button
            onClick={() => setOpen(false)}
            className="text-text-muted hover:text-text-primary transition-colors text-xl leading-none"
          >
            &times;
          </button>
        </div>

        {/* Body */}
        <div className="grid grid-cols-1 sm:grid-cols-2 gap-6 p-6">
          {groups.map((group) => (
            <div key={group.title}>
              <h3 className="text-sm font-semibold text-text-muted uppercase tracking-wider mb-3">
                {group.title}
              </h3>
              <div className="space-y-2">
                {group.shortcuts.map((sc) => (
                  <div key={sc.description} className="flex items-center justify-between gap-3">
                    <span className="text-sm text-text-secondary">{sc.description}</span>
                    <span className="flex items-center gap-1 shrink-0">
                      {sc.keys.map((k, i) => (
                        <span key={i} className="flex items-center gap-1">
                          {i > 0 && <span className="text-text-muted text-xs">then</span>}
                          <Kbd>{k}</Kbd>
                        </span>
                      ))}
                    </span>
                  </div>
                ))}
              </div>
            </div>
          ))}
        </div>

        {/* Footer hint */}
        <div className="px-6 py-3 border-t border-border text-center">
          <span className="text-xs text-text-muted">
            Press <Kbd>?</Kbd> or <Kbd>Esc</Kbd> to close
          </span>
        </div>
      </div>
    </div>
  )
}
