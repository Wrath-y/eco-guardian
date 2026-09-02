import { Navigate, Outlet, useLocation } from 'react-router-dom'
import { Spin } from 'antd'
import { useAppState } from '../context/AppContext'

export default function ProjectGuard() {
  const { project, projectLoading } = useAppState()
  const location = useLocation()
  if (projectLoading) return <div className="route-loading"><Spin size="large" description="正在确认当前项目…"><span /></Spin></div>
  if (!project) return <Navigate to="/projects" replace state={{ from: location.pathname }} />
  return <Outlet />
}
