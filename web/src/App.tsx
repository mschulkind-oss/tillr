import { BrowserRouter, Routes, Route, Navigate } from 'react-router-dom'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { Layout } from './components/Layout'
import { Dashboard } from './pages/Dashboard'
import { Features } from './pages/Features'
import { FeatureDetail } from './pages/FeatureDetail'
import { QA } from './pages/QA'
import { Roadmap } from './pages/Roadmap'
import { MilestoneDetail } from './pages/MilestoneDetail'
import { RoadmapDetail } from './pages/RoadmapDetail'
import { Agents } from './pages/Agents'
import { AgentDetail } from './pages/AgentDetail'
import { Cycles } from './pages/Cycles'
import { CycleDetail } from './pages/CycleDetail'
import { Ideas } from './pages/Ideas'
import { IdeaDetail } from './pages/IdeaDetail'
import { Discussions } from './pages/Discussions'
import { DiscussionDetail } from './pages/DiscussionDetail'
import { Decisions } from './pages/Decisions'
import { DecisionDetail } from './pages/DecisionDetail'
import { Context } from './pages/Context'
import { History } from './pages/History'
import { Workflow } from './pages/Workflow'
import { Stats } from './pages/Stats'
import { Spec } from './pages/Spec'
import { Timeline } from './pages/Timeline'
import Workstreams from './pages/Workstreams'
import WorkstreamDetail from './pages/WorkstreamDetail'
import { useEffect, useState } from 'react'
import { useStore } from './store'
import { KeyboardShortcuts } from './components/KeyboardShortcuts'
import { HelpModal } from './components/HelpModal'
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
        <KeyboardShortcuts />
        <HelpModal />
        <Routes>
          <Route element={<Layout />}>
            <Route path="/" element={<Navigate to="/dashboard" replace />} />
            <Route path="/dashboard" element={<Dashboard />} />
            <Route path="/features" element={<Features />} />
            <Route path="/features/:id" element={<FeatureDetail />} />
            <Route path="/qa" element={<QA />} />
            <Route path="/qa/:workstreamId" element={<QA />} />
            <Route path="/roadmap" element={<Roadmap />} />
            <Route path="/roadmap/:id" element={<RoadmapDetail />} />
            <Route path="/milestones/:id" element={<MilestoneDetail />} />
            {/* Agents & Cycles */}
            <Route path="/agents" element={<Agents />} />
            <Route path="/agents/:id" element={<AgentDetail />} />
            <Route path="/cycles" element={<Cycles />} />
            <Route path="/cycles/:id" element={<CycleDetail />} />
            <Route path="/ideas" element={<Ideas />} />
            <Route path="/ideas/:id" element={<IdeaDetail />} />
            <Route path="/discussions" element={<Discussions />} />
            <Route path="/discussions/:id" element={<DiscussionDetail />} />
            <Route path="/decisions" element={<Decisions />} />
            <Route path="/decisions/:id" element={<DecisionDetail />} />
            <Route path="/history" element={<History />} />
            <Route path="/stats" element={<Stats />} />
            <Route path="/timeline" element={<Timeline />} />
            <Route path="/spec" element={<Spec />} />
            <Route path="/context" element={<Context />} />
            <Route path="/workstreams" element={<Workstreams />} />
            <Route path="/workstreams/:id" element={<WorkstreamDetail />} />
            <Route path="/workflow" element={<Workflow />} />
            <Route path="*" element={<Navigate to="/dashboard" replace />} />
          </Route>
        </Routes>
      </BrowserRouter>
    </QueryClientProvider>
  )
}
