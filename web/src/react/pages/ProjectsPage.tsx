import { useState } from 'react'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { Alert, Button, Card, Col, Empty, Flex, Modal, Row, Skeleton, Space, Statistic, Tag, Typography, message } from 'antd'
import { CloseOutlined, FolderAddOutlined, FolderOpenOutlined, HistoryOutlined, SafetyCertificateOutlined } from '@ant-design/icons'
import PageHeader from '../components/PageHeader'
import { ApiError, apiRequest, errorMessage, postJSON, type ActiveProject } from '../api'
import { useAppState } from '../context/AppContext'

export default function ProjectsPage() {
  const { project, setProject } = useAppState()
  const [messageApi, contextHolder] = message.useMessage()
  const client = useQueryClient()
  const [operation, setOperation] = useState('')
  const [lockedProject, setLockedProject] = useState<ActiveProject | null>(null)
  const recent = useQuery({ queryKey: ['projects', 'recent'], queryFn: () => apiRequest<ActiveProject[]>('/api/v1/projects/recent') })

  async function closeCurrent() {
    if (!project) return
    await apiRequest<void>('/api/v1/projects/close', { method: 'POST' })
    setProject(null)
  }

  const chooseMutation = useMutation({
    mutationFn: async (mode: 'create' | 'open') => {
      setOperation(mode)
      if (project) await closeCurrent()
      const selection = await postJSON<{ token: string }>('/api/v1/project-selections', {})
      return postJSON<ActiveProject>('/api/v1/projects', { selection_token: selection.token, mode })
    },
    onSuccess: value => {
      setProject(value)
      void client.invalidateQueries({ queryKey: ['projects', 'recent'] })
      messageApi.success(`已打开项目「${value.name}」`)
    },
    onError: cause => messageApi.error(errorMessage(cause, '无法打开项目')),
    onSettled: () => setOperation(''),
  })

  const recentMutation = useMutation({
    mutationFn: async (value: ActiveProject) => {
      setOperation(value.id)
      if (project) await closeCurrent()
      return apiRequest<ActiveProject>(`/api/v1/projects/recent/${value.id}`, { method: 'POST' })
    },
    onSuccess: value => { setLockedProject(null); setProject(value); messageApi.success(`已打开项目「${value.name}」`) },
    onError: (cause, value) => {
      if (cause instanceof ApiError && cause.code === 'PROJECT_LOCKED') setLockedProject(value)
      messageApi.error(errorMessage(cause, '无法打开最近项目'))
    },
    onSettled: () => setOperation(''),
  })

  const closeOtherMutation = useMutation({
    mutationFn: (value: ActiveProject) => apiRequest<void>(`/api/v1/projects/recent/${value.id}/close-other-instance`, { method: 'POST' }),
    onSuccess: (_, value) => {
      setLockedProject(null)
      messageApi.success('其他实例已安全关闭，正在重新打开项目')
      recentMutation.mutate(value)
    },
    onError: cause => messageApi.error(cause instanceof ApiError && cause.code === 'OTHER_INSTANCE_UNAVAILABLE'
      ? '无法安全识别该实例；如果它由旧版本启动，请先手动关闭一次。'
      : errorMessage(cause, '无法安全关闭其他实例')),
  })

  function confirmCloseOther(value: ActiveProject) {
    Modal.confirm({
      title: '确认关闭其他实例？',
      content: `将关闭当前占用「${value.name}」的另一个 Eco Guardian 进程。该实例浏览器中尚未保存的表单输入可能丢失。`,
      okText: '关闭其他实例',
      cancelText: '取消',
      okButtonProps: { danger: true },
      onOk: () => closeOtherMutation.mutateAsync(value),
    })
  }

  const closeMutation = useMutation({
    mutationFn: closeCurrent,
    onSuccess: () => messageApi.success('项目已安全关闭'),
    onError: cause => messageApi.error(errorMessage(cause, '无法关闭项目')),
  })

  return <div className="page-container project-page">
    {contextHolder}
    <PageHeader
      eyebrow={<><SafetyCertificateOutlined /> 本地优先的数值治理</>}
      title="项目空间"
      description="创建或打开一个项目，所有配置、版本、验证和恢复记录都会围绕当前项目组织。"
      extra={<Space wrap>
        <Button icon={<FolderOpenOutlined />} loading={operation === 'open'} onClick={() => chooseMutation.mutate('open')}>打开项目</Button>
        <Button type="primary" icon={<FolderAddOutlined />} loading={operation === 'create'} onClick={() => chooseMutation.mutate('create')}>创建项目</Button>
      </Space>}
    />

    {project ? <Card className="hero-project-card">
      <Row gutter={[24, 24]} align="middle">
        <Col flex="auto">
          <Space orientation="vertical" size={6}>
            <Tag color="success">当前项目</Tag>
            <Typography.Title level={3} style={{ margin: 0 }}>{project.name}</Typography.Title>
            <Typography.Text type="secondary">项目标识 {project.id}</Typography.Text>
          </Space>
        </Col>
        <Col><Statistic title="数据库 Schema" value={project.db_schema_version} prefix="v" /></Col>
        <Col><Button danger icon={<CloseOutlined />} loading={closeMutation.isPending} onClick={() => closeMutation.mutate()}>关闭项目</Button></Col>
      </Row>
    </Card> : <>
      <Alert className="block-alert" type="info" showIcon title="尚未打开项目" description="请创建新项目，或从本机选择已有的 Eco Guardian 项目。" />
      {lockedProject && <Alert
        className="block-alert"
        type="warning"
        showIcon
        title={`「${lockedProject.name}」正在其他实例中使用`}
        description="系统只会通过项目锁中的加密凭据联系持锁实例，不会删除锁文件或强制抢占数据库。"
        action={<Button danger loading={closeOtherMutation.isPending} onClick={() => confirmCloseOther(lockedProject)}>关闭其他实例</Button>}
      />}
    </>}

    <Card title={<Space><HistoryOutlined />最近项目</Space>} className="section-card">
      {recent.isError && <Alert type="error" showIcon title="最近项目加载失败" action={<Button onClick={() => void recent.refetch()}>重试</Button>} />}
      {recent.isLoading ? <Skeleton active paragraph={{ rows: 3 }} /> : recent.data?.length ? <Flex vertical className="project-recent-list">
        {recent.data.map(item => <div className="project-recent-row" key={item.id}>
          <div className="project-list-icon"><FolderOpenOutlined /></div>
          <div className="project-recent-copy"><strong>{item.name}</strong><Flex gap="small" wrap><Typography.Text type="secondary">{item.id}</Typography.Text>{item.db_schema_version && <Tag>Schema v{item.db_schema_version}</Tag>}</Flex></div>
          <Button type="link" loading={operation === item.id} onClick={() => recentMutation.mutate(item)}>打开 {item.name}</Button>
        </div>)}
      </Flex> : <Empty image={Empty.PRESENTED_IMAGE_SIMPLE} description="暂无最近项目" />}
    </Card>
  </div>
}
