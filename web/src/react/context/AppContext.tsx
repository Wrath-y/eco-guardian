import { createContext, useContext, type ReactNode } from 'react'
import { useQuery, useQueryClient } from '@tanstack/react-query'
import { ApiError, apiRequest, type ActiveProject, type RuntimeCapabilities, type RuntimeStatus } from '../api'

interface AppState {
  project: ActiveProject | null
  projectLoading: boolean
  runtime?: RuntimeStatus
  runtimeCapabilities?: RuntimeCapabilities
  runtimeError: string
  setProject: (project: ActiveProject | null) => void
  refreshProject: () => Promise<unknown>
  refreshRuntime: () => Promise<unknown>
}

const AppStateContext = createContext<AppState | null>(null)

async function currentProject() {
  try {
    return await apiRequest<ActiveProject>('/api/v1/projects/current')
  } catch (cause) {
    if (cause instanceof ApiError && cause.status === 404) return null
    throw cause
  }
}

export function AppStateProvider({ children }: { children: ReactNode }) {
  const client = useQueryClient()
  const projectQuery = useQuery({ queryKey: ['project', 'current'], queryFn: currentProject })
  const runtimeQuery = useQuery({
    queryKey: ['runtime', 'status'],
    queryFn: () => apiRequest<RuntimeStatus>('/api/v1/runtime/status'),
    refetchInterval: query => {
      const phase = query.state.data?.phase
      return phase === 'ready' || phase === 'degraded' ? 5_000 : phase === 'stopped' ? 15_000 : 2_000
    },
  })
  const capabilityQuery = useQuery({
    queryKey: ['runtime', 'capabilities'],
    queryFn: () => apiRequest<RuntimeCapabilities>('/api/v1/runtime/capabilities'),
    refetchInterval: 5_000,
  })

  const value: AppState = {
    project: projectQuery.data ?? null,
    projectLoading: projectQuery.isPending,
    runtime: runtimeQuery.data,
    runtimeCapabilities: capabilityQuery.data,
    runtimeError: runtimeQuery.error instanceof Error ? runtimeQuery.error.message : '',
    setProject: project => client.setQueryData(['project', 'current'], project),
    refreshProject: projectQuery.refetch,
    refreshRuntime: async () => Promise.all([runtimeQuery.refetch(), capabilityQuery.refetch()]),
  }

  return <AppStateContext.Provider value={value}>{children}</AppStateContext.Provider>
}

export function useAppState() {
  const value = useContext(AppStateContext)
  if (!value) throw new Error('useAppState must be used within AppStateProvider')
  return value
}
