import { BrowserRouter, Routes, Route, Navigate } from 'react-router-dom'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { Layout } from './components/Layout'
import { Dashboard } from './pages/Dashboard'
import { Features } from './pages/Features'
import { FeatureDetail } from './pages/FeatureDetail'
import { QA } from './pages/QA'
import { Roadmap } from './pages/Roadmap'
import { PlaceholderPage } from './pages/Placeholder'
import { useEffect } from 'react'
import { useStore } from './store'

const queryClient = new QueryClient({
  defaultOptions: {
    queries: {
      staleTime: 10_000,
      refetchOnWindowFocus: true,
      retry: 1,
    },
  },
})

function ThemeInit() {
  const theme = useStore((s) => s.theme)
  useEffect(() => {
    document.documentElement.setAttribute('data-theme', theme)
  }, [theme])
  return null
}

export default function App() {
  return (
    <QueryClientProvider client={queryClient}>
      <ThemeInit />
      <BrowserRouter>
        <Routes>
          <Route element={<Layout />}>
            <Route path="/" element={<Navigate to="/dashboard" replace />} />
            <Route path="/dashboard" element={<Dashboard />} />
            <Route path="/features" element={<Features />} />
            <Route path="/features/:id" element={<FeatureDetail />} />
            <Route path="/qa" element={<QA />} />
            <Route path="/roadmap" element={<Roadmap />} />
            {/* Phase 2 pages - placeholders for now */}
            <Route path="/cycles" element={<PlaceholderPage title="Cycles" icon="🔄" />} />
            <Route path="/cycles/:id" element={<PlaceholderPage title="Cycle Detail" icon="🔄" />} />
            <Route path="/agents" element={<PlaceholderPage title="Agents" icon="🤖" />} />
            <Route path="/ideas" element={<PlaceholderPage title="Ideas" icon="💡" />} />
            <Route path="/discussions" element={<PlaceholderPage title="Discussions" icon="💬" />} />
            <Route path="/discussions/:id" element={<PlaceholderPage title="Discussion Detail" icon="💬" />} />
            <Route path="/decisions" element={<PlaceholderPage title="Decisions" icon="⚖️" />} />
            <Route path="/history" element={<PlaceholderPage title="History" icon="📜" />} />
            <Route path="/stats" element={<PlaceholderPage title="Stats" icon="📈" />} />
            <Route path="/timeline" element={<PlaceholderPage title="Timeline" icon="📅" />} />
            <Route path="/spec" element={<PlaceholderPage title="Spec Doc" icon="📄" />} />
            <Route path="/context" element={<PlaceholderPage title="Context" icon="📚" />} />
            <Route path="/workflow" element={<PlaceholderPage title="Workflow" icon="⚡" />} />
            <Route path="*" element={<Navigate to="/dashboard" replace />} />
          </Route>
        </Routes>
      </BrowserRouter>
    </QueryClientProvider>
  )
}
