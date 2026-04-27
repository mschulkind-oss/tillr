import { BrowserRouter, Routes, Route, Navigate } from 'react-router-dom'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { useEffect, useState } from 'react'
import { Layout } from './components/Layout'
import { Features } from './pages/Features'
import { FeatureDetail } from './pages/FeatureDetail'
import { useStore } from './store'
import { initProjects } from './api/projects'

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
  const [ready, setReady] = useState(false)

  useEffect(() => {
    initProjects().then(() => setReady(true))
  }, [])

  if (!ready) {
    return (
      <div className="flex items-center justify-center h-screen bg-bg-primary text-text-muted text-sm">
        Loading...
      </div>
    )
  }

  return (
    <QueryClientProvider client={queryClient}>
      <ThemeInit />
      <BrowserRouter>
        <Routes>
          <Route element={<Layout />}>
            <Route path="/" element={<Navigate to="/features" replace />} />
            <Route path="/features" element={<Features />} />
            <Route path="/features/:id" element={<FeatureDetail />} />
            <Route path="*" element={<Navigate to="/features" replace />} />
          </Route>
        </Routes>
      </BrowserRouter>
    </QueryClientProvider>
  )
}
