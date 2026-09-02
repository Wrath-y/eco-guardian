import { lazy, Suspense, useEffect, useMemo, useState } from 'react'
import { Avatar, Breadcrumb, Button, Layout, Menu, Space, Spin, Tag, Typography, type MenuProps } from 'antd'
import {
  ApartmentOutlined, AppstoreOutlined, BarsOutlined, BranchesOutlined, CloudSyncOutlined,
  BulbOutlined, CloudServerOutlined, CodeOutlined, DatabaseOutlined, ExperimentOutlined, FileSearchOutlined,
  FolderOpenOutlined, MenuFoldOutlined, MenuUnfoldOutlined, ProjectOutlined, RobotOutlined, SafetyCertificateOutlined,
  SettingOutlined, TagsOutlined, ThunderboltOutlined,
} from '@ant-design/icons'
import { Navigate, Route, Routes, useLocation, useNavigate } from 'react-router-dom'
import { AppStateProvider, useAppState } from './context/AppContext'
import ProjectGuard from './components/ProjectGuard'
import RuntimeStatus from './components/RuntimeStatus'

const ProjectsPage = lazy(() => import('./pages/ProjectsPage'))
const EntityListPage = lazy(() => import('./pages/EntityListPage'))
const EntityEditorPage = lazy(() => import('./pages/EntityEditorPage'))
const VersionsPage = lazy(() => import('./pages/VersionsPage'))
const RevisionDiffPage = lazy(() => import('./pages/RevisionDiffPage'))
const SimulationsPage = lazy(() => import('./pages/SimulationsPage'))
const ImpactPage = lazy(() => import('./pages/ImpactPage'))
const RiskReviewsPage = lazy(() => import('./pages/RiskReviewsPage'))
const AIDesignPage = lazy(() => import('./pages/AIDesignPage'))
const BackupsPage = lazy(() => import('./pages/BackupsPage'))
const SettingsPage = lazy(() => import('./pages/SettingsPage'))

const { Header, Sider, Content } = Layout

const routeLabels: Array<[RegExp, string, string]> = [
  [/^\/projects/, '项目空间', '项目'],
  [/^\/config\/([^/]+)\/[^/]+/, '配置中心', '编辑配置'],
  [/^\/config\/([^/]+)/, '配置中心', '配置列表'],
  [/^\/versions\/[^/]+\/diff/, '版本与发布', '版本差异'],
  [/^\/versions/, '版本与发布', '版本历史'],
  [/^\/simulations/, '分析工具', '模拟实验'],
  [/^\/impact/, '分析工具', '影响分析'],
  [/^\/risk-reviews/, '分析工具', '风险复核'],
  [/^\/ai-design/, '智能辅助', 'AI 平衡设计'],
  [/^\/backups/, '系统维护', '备份与恢复'],
  [/^\/settings/, '系统维护', '运行设置'],
]

function selectedKey(path: string) {
  if (path.startsWith('/config/')) return `config-${path.split('/')[2]}`
  if (path.startsWith('/versions')) return 'versions'
  return path.split('/')[1] || 'projects'
}

function Shell() {
  const [collapsed, setCollapsed] = useState(false)
  const [mobile, setMobile] = useState(false)
  const location = useLocation()
  const navigate = useNavigate()
  const { project } = useAppState()
  const labels = routeLabels.find(([pattern]) => pattern.test(location.pathname)) ?? routeLabels[0]

  useEffect(() => { if (mobile) setCollapsed(true) }, [location.pathname, mobile])

  const menuItems = useMemo<MenuProps['items']>(() => [
    { key: 'projects', icon: <ProjectOutlined />, label: '项目空间' },
    { type: 'group', label: '配置中心', children: [
      { key: 'config-attribute', icon: <BarsOutlined />, label: '属性', disabled: !project },
      { key: 'config-tag', icon: <TagsOutlined />, label: '标签', disabled: !project },
      { key: 'config-character', icon: <AppstoreOutlined />, label: '角色', disabled: !project },
      { key: 'config-skill', icon: <ThunderboltOutlined />, label: '技能', disabled: !project },
      { key: 'config-item', icon: <DatabaseOutlined />, label: '物品', disabled: !project },
      { key: 'config-effect', icon: <BulbOutlined />, label: '效果', disabled: !project },
    ] },
    { type: 'group', label: '版本与分析', children: [
      { key: 'versions', icon: <BranchesOutlined />, label: '版本历史', disabled: !project },
      { key: 'simulations', icon: <ExperimentOutlined />, label: '模拟实验', disabled: !project },
      { key: 'impact', icon: <ApartmentOutlined />, label: '影响分析', disabled: !project },
      { key: 'risk-reviews', icon: <FileSearchOutlined />, label: '风险复核', disabled: !project },
      { key: 'ai-design', icon: <RobotOutlined />, label: 'AI 平衡设计', disabled: !project },
    ] },
    { type: 'group', label: '系统', children: [
      { key: 'backups', icon: <CloudSyncOutlined />, label: '备份与恢复', disabled: !project },
      { key: 'settings', icon: <SettingOutlined />, label: '运行设置' },
    ] },
  ], [project])

  function menuNavigate(key: string) {
    const path = key.startsWith('config-') ? `/config/${key.slice('config-'.length)}` : `/${key}`
    navigate(path)
  }

  return (
    <Layout className="app-layout">
      <Sider
        className="app-sider"
        width={256}
        collapsible
        collapsed={collapsed}
        collapsedWidth={mobile ? 0 : 76}
        trigger={null}
        breakpoint="lg"
        onBreakpoint={setMobile}
      >
        <button className="brand" aria-label="前往项目空间" onClick={() => navigate('/projects')}>
          <span className="brand-mark"><SafetyCertificateOutlined /></span>
          {!collapsed && <span className="brand-copy"><strong>Eco Guardian</strong><small>数值治理工作台</small></span>}
        </button>
        <Menu theme="dark" mode="inline" selectedKeys={[selectedKey(location.pathname)]} items={menuItems} onClick={({ key }) => menuNavigate(key)} />
        {!collapsed && <div className="sider-foot"><CodeOutlined /> React · Ant Design</div>}
      </Sider>

      <Layout className="site-layout">
        <Header className="app-header">
          <Space size="middle" className="header-left">
            <Button type="text" aria-label={collapsed ? '展开导航' : '收起导航'} icon={collapsed ? <MenuUnfoldOutlined /> : <MenuFoldOutlined />} onClick={() => setCollapsed(value => !value)} />
            <Breadcrumb items={[{ title: labels[1] }, { title: labels[2] }]} />
          </Space>
          <Space size="middle" className="header-right">
            {project ? <Tag className="project-tag" icon={<FolderOpenOutlined />} color="blue">{project.name}</Tag> : <Typography.Text type="secondary">未打开项目</Typography.Text>}
            <RuntimeStatus />
            <Avatar className="product-avatar" icon={<CloudServerOutlined />} />
          </Space>
        </Header>

        <Content className="app-content">
          <Suspense fallback={<div className="route-loading"><Spin size="large" /></div>}><Routes>
            <Route path="/" element={<Navigate to="/projects" replace />} />
            <Route path="/projects" element={<ProjectsPage />} />
            <Route path="/settings" element={<SettingsPage />} />
            <Route element={<ProjectGuard />}>
              <Route path="/config/:kind" element={<EntityListPage />} />
              <Route path="/config/:kind/:id" element={<EntityEditorPage />} />
              <Route path="/versions" element={<VersionsPage />} />
              <Route path="/versions/:id/diff" element={<RevisionDiffPage />} />
              <Route path="/simulations" element={<SimulationsPage />} />
              <Route path="/impact" element={<ImpactPage />} />
              <Route path="/risk-reviews" element={<RiskReviewsPage />} />
              <Route path="/ai-design" element={<AIDesignPage />} />
              <Route path="/backups" element={<BackupsPage />} />
            </Route>
            <Route path="*" element={<Navigate to="/projects" replace />} />
          </Routes></Suspense>
        </Content>
      </Layout>
    </Layout>
  )
}

export default function App() {
  return <AppStateProvider><Shell /></AppStateProvider>
}
