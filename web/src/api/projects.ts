// Multi-project daemon support.
// In single-project mode (tillr serve), API calls go to /api/...
// In daemon mode (tillr daemon), API calls go to /api/p/{slug}/...

export interface ProjectInfo {
  slug: string
  name: string
  path: string
}

let daemonMode = false
let projectList: ProjectInfo[] = []
let activeSlug: string | null = null

// Initialize: try to detect daemon mode by fetching /api/projects.
// In single-project mode this endpoint doesn't exist and returns 404.
export async function initProjects(): Promise<void> {
  try {
    const res = await fetch('/api/projects')
    if (res.ok) {
      const data: ProjectInfo[] = await res.json()
      if (Array.isArray(data) && data.length > 0) {
        daemonMode = true
        projectList = data

        // Restore last active project from localStorage
        const saved = localStorage.getItem('tillr-active-project')
        if (saved && data.some((p) => p.slug === saved)) {
          activeSlug = saved
        } else {
          activeSlug = data[0].slug
        }
      }
    }
  } catch {
    // Single-project mode — no daemon running
  }
}

export function isDaemonMode(): boolean {
  return daemonMode
}

export function getProjects(): ProjectInfo[] {
  return projectList
}

export function getActiveProject(): string | null {
  return activeSlug
}

export function setActiveProject(slug: string): void {
  activeSlug = slug
  localStorage.setItem('tillr-active-project', slug)
}

// Returns the API base path for the active project.
// Single-project: '' (URLs stay as /api/...)
// Daemon: '/api/p/{slug}' (URLs become /api/p/{slug}/...)
export function apiBase(): string {
  if (!daemonMode || !activeSlug) return ''
  return `/api/p/${activeSlug}`
}

// Rewrite an API URL for the current mode.
// Input: '/api/features' → Output: '/api/features' or '/api/p/myproject/features'
export function rewriteApiUrl(url: string): string {
  if (!daemonMode || !activeSlug) return url
  if (url.startsWith('/api/')) {
    return `/api/p/${activeSlug}${url.slice(4)}` // /api/features → /api/p/slug/features
  }
  return url
}
