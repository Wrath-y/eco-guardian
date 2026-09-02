import { useEffect, useState } from 'react'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { Alert, Button, Card, Descriptions, Form, Input, Modal, Space, Table, Tag, Timeline, Typography, message } from 'antd'
import { BranchesOutlined, DiffOutlined, HistoryOutlined, PlusOutlined, RollbackOutlined } from '@ant-design/icons'
import { useNavigate, useSearchParams } from 'react-router-dom'
import type { components } from '@/api/generated'
import PageHeader from '../components/PageHeader'
import GraphStatusCard from '../components/GraphStatusCard'
import { apiRequest, errorMessage, postJSON, shortID } from '../api'
import { useAppState } from '../context/AppContext'

type RevisionPage = components['schemas']['RevisionPage']
type Revision = components['schemas']['RevisionHistoryItem']
type RevisionDetail = components['schemas']['RevisionDetail']
type ReleasePage = components['schemas']['ReleasePage']
type Release = components['schemas']['Release']

const statusColor: Record<string, string> = { working: 'processing', candidate: 'warning', active_release: 'success', history: 'default' }
const statusLabel: Record<string, string> = { working: '当前工作', candidate: '候选', active_release: '当前正式', history: '历史' }

export default function VersionsPage() {
  const { project } = useAppState()
  const [params, setParams] = useSearchParams()
  const navigate = useNavigate()
  const client = useQueryClient()
  const [revisionCursor, setRevisionCursor] = useState('')
  const [releaseCursor, setReleaseCursor] = useState('')
  const [messageApi, contextHolder] = message.useMessage()
  const selectedID = params.get('revision') ?? ''
  const candidateID = params.get('candidate') ?? ''
  const revisions = useQuery({ queryKey: ['revisions', project?.id, revisionCursor], queryFn: () => apiRequest<RevisionPage>(`/api/v1/revisions${revisionCursor ? `?cursor=${encodeURIComponent(revisionCursor)}` : ''}`), enabled: Boolean(project) })
  const releases = useQuery({ queryKey: ['releases', project?.id, releaseCursor], queryFn: () => apiRequest<ReleasePage>(`/api/v1/releases${releaseCursor ? `?cursor=${encodeURIComponent(releaseCursor)}` : ''}`), enabled: Boolean(project) })
  const selected = useQuery({ queryKey: ['revision', selectedID], queryFn: () => apiRequest<RevisionDetail>(`/api/v1/revisions/${selectedID}`), enabled: Boolean(selectedID) })
  const workingID = revisions.data?.items.find(item => item.status.includes('working'))?.id ?? ''

  useEffect(() => {
    if (!selectedID && revisions.data?.items[0]) setParams(current => { const next = new URLSearchParams(current); next.set('revision', revisions.data!.items[0].id); return next }, { replace: true })
  }, [revisions.data, selectedID, setParams])

  const checkpoint = useMutation({
    mutationFn: (value: { name?: string }) => postJSON<RevisionDetail>('/api/v1/revisions', { kind: 'checkpoint', current_working: { revision_id: selectedID }, name: value.name ?? '' }),
    onSuccess: value => { messageApi.success('检查点已创建'); void client.invalidateQueries({ queryKey: ['revisions'] }); setParams(current => { const next = new URLSearchParams(current); next.set('revision', value.id); next.set('candidate', value.id); return next }) },
    onError: cause => messageApi.error(errorMessage(cause, '无法创建检查点')),
  })
  const restore = useMutation({
    mutationFn: (release: Release) => postJSON<RevisionDetail>('/api/v1/revisions', { kind: 'restore_release', current_working: { revision_id: workingID }, source_release_id: release.id }),
    onSuccess: value => { messageApi.success('回滚候选已创建，不会自动发布'); void client.invalidateQueries({ queryKey: ['revisions'] }); setParams({ revision: value.id, candidate: value.id }) },
    onError: cause => messageApi.error(errorMessage(cause, '无法创建回滚候选')),
  })

  function selectRevision(id: string) { setParams(current => { const next = new URLSearchParams(current); next.set('revision', id); return next }) }
  function selectCandidate(id: string) { setParams(current => { const next = new URLSearchParams(current); next.set('candidate', id); return next }) }

  return <div className="page-container versions-page">
    {contextHolder}
    <PageHeader title="版本历史" eyebrow={<><BranchesOutlined /> 不可变配置版本</>} description="查看修订时间线、选择候选、创建检查点，并从正式版本生成安全的回滚候选。" />
    {revisions.isError && <Alert className="block-alert" type="error" showIcon title={errorMessage(revisions.error, '无法读取版本历史')} action={<Button onClick={() => void revisions.refetch()}>重试</Button>} />}
    <Card className="section-card" title={<Space><HistoryOutlined />配置版本</Space>}>
      <Table<Revision>
        rowKey="id" loading={revisions.isLoading} dataSource={revisions.data?.items ?? []} pagination={false}
        rowClassName={row => row.id === selectedID ? 'selected-table-row' : ''}
        columns={[
          { title: '版本', dataIndex: 'display_revision', width: 100, render: (value, row) => <Button type="link" onClick={() => selectRevision(row.id)}>#{value}</Button> },
          { title: '名称 / Hash', render: (_, row) => <Space orientation="vertical" size={0}><Typography.Text strong>{row.metadata.name || '未命名修订'}</Typography.Text><Typography.Text code>{shortID(row.config_hash)}</Typography.Text></Space> },
          { title: '状态', width: 230, render: (_, row) => <Space wrap>{row.status.map(status => <Tag key={status} color={statusColor[status]}>{statusLabel[status]}</Tag>)}{candidateID === row.id && <Tag color="gold">已选候选</Tag>}</Space> },
          { title: '创建时间', dataIndex: ['metadata', 'created_at'], width: 180, render: value => new Date(value).toLocaleString() },
          { title: '图谱', width: 150, render: (_, row) => <GraphStatusCard revisionId={row.id} compact /> },
          { title: '操作', width: 250, render: (_, row) => <Space><Button onClick={() => selectCandidate(row.id)}>设为候选</Button><Button icon={<DiffOutlined />} onClick={() => navigate(`/versions/${row.id}/diff${candidateID ? `?base=${candidateID}` : ''}`)}>查看差异</Button></Space> },
        ]}
      />
      {revisions.data?.next_cursor && <Button className="load-more-button" onClick={() => setRevisionCursor(revisions.data!.next_cursor!)}>加载更多版本</Button>}
    </Card>

    {selectedID && <Card className="section-card" title={`版本 #${selected.data?.display_revision ?? '…'} 状态时间线`} loading={selected.isLoading}>
      {selected.isError && <Alert type="error" showIcon title={errorMessage(selected.error, '无法读取版本详情')} />}
      {selected.data && <>
        <Descriptions size="small" column={{ xs: 1, md: 3 }}>
          <Descriptions.Item label="修订 ID"><Typography.Text copyable code>{selected.data.id}</Typography.Text></Descriptions.Item>
          <Descriptions.Item label="配置 Hash"><Typography.Text copyable code>{selected.data.config_hash}</Typography.Text></Descriptions.Item>
          <Descriptions.Item label="Manifest"><Typography.Text copyable code>{selected.data.metadata.version_manifest.hash}</Typography.Text></Descriptions.Item>
        </Descriptions>
        <Timeline className="revision-timeline" items={selected.data.timeline.map(event => ({ children: <><Typography.Text strong>{event.type}</Typography.Text><br /><Typography.Text type="secondary">{new Date(event.occurred_at).toLocaleString()} {event.status && `· ${event.status}`}</Typography.Text></> }))} />
        <Form layout="inline" onFinish={value => checkpoint.mutate(value)}><Form.Item name="name" label="检查点名称"><Input placeholder="可选" /></Form.Item><Form.Item><Button htmlType="submit" icon={<PlusOutlined />} loading={checkpoint.isPending}>创建同内容检查点</Button></Form.Item></Form>
        <GraphStatusCard revisionId={selectedID} />
      </>}
    </Card>}

    <Card className="section-card" title="正式版本历史">
      <Table<Release>
        rowKey="id" loading={releases.isLoading} dataSource={releases.data?.items ?? []} pagination={false}
        columns={[
          { title: '正式版本', dataIndex: 'id', render: value => <Typography.Text copyable>{shortID(value)}</Typography.Text> },
          { title: '修订', dataIndex: 'revision_id', render: value => shortID(value) },
          { title: '说明', dataIndex: 'notes', render: value => value || <Typography.Text type="secondary">无发布说明</Typography.Text> },
          { title: '创建时间', dataIndex: 'created_at', render: value => new Date(value).toLocaleString() },
          { title: '操作', width: 210, render: (_, row) => <Button icon={<RollbackOutlined />} disabled={!workingID} loading={restore.isPending} onClick={() => Modal.confirm({ title: '创建回滚候选？', content: '此操作只创建新的候选修订，不会自动发布。', onOk: () => restore.mutateAsync(row) })}>创建回滚候选</Button> },
        ]}
      />
      {releases.data?.next_cursor && <Button className="load-more-button" onClick={() => setReleaseCursor(releases.data!.next_cursor!)}>加载更多正式版本</Button>}
    </Card>
  </div>
}
