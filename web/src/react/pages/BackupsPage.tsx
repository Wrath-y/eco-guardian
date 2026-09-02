import { useMemo, useState } from 'react'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { Alert, Button, Card, Descriptions, Form, Input, Modal, Space, Table, Tag, Typography, message } from 'antd'
import { CloudDownloadOutlined, CloudSyncOutlined, DatabaseOutlined, ReloadOutlined } from '@ant-design/icons'
import type { components } from '@/api/generated'
import PageHeader from '../components/PageHeader'
import JobProgress from '../components/JobProgress'
import { apiRequest, errorMessage, newIdempotencyKey, postJSON, shortID } from '../api'
import { useAppState } from '../context/AppContext'

type BackupPage = components['schemas']['BackupPage']
type BackupRecord = components['schemas']['BackupRecord']
type BackupAccepted = components['schemas']['BackupJobAccepted']
type RestorePreflight = components['schemas']['RestorePreflight']
type RestoreAccepted = components['schemas']['RestoreJobAccepted']

function formatBytes(bytes: number | null) {
  return bytes === null ? '未知' : new Intl.NumberFormat('zh-CN', { style: 'unit', unit: 'megabyte', maximumFractionDigits: 1 }).format(bytes / 1024 / 1024)
}

function sourceSummary(item: BackupRecord) {
  return item.source.revision_id ? `修订 ${shortID(item.source.revision_id)}`
    : item.source.release_id ? `发布 ${shortID(item.source.release_id)}`
      : item.source.migration_id ? `迁移 ${item.source.migration_id}`
        : item.source.caller_job_id ? `任务 ${shortID(item.source.caller_job_id)}` : '当前项目快照'
}

export default function BackupsPage() {
  const { project, refreshProject, refreshRuntime } = useAppState()
  const client = useQueryClient()
  const [messageApi, contextHolder] = message.useMessage()
  const [more, setMore] = useState<BackupRecord[]>([])
  const [cursor, setCursor] = useState('')
  const [backupJob, setBackupJob] = useState(sessionStorage.getItem(`backup-job:${project?.id}`) ?? '')
  const [restoreJob, setRestoreJob] = useState(sessionStorage.getItem(`restore-job:${project?.id}`) ?? '')
  const [preflight, setPreflight] = useState<RestorePreflight>()
  const [confirmation, setConfirmation] = useState('')
  const query = useQuery({ queryKey: ['backups', project?.id], queryFn: () => apiRequest<BackupPage>('/api/v1/backups?limit=50'), enabled: Boolean(project) })
  const items = useMemo(() => [...(query.data?.items ?? []), ...more], [more, query.data?.items])

  const manual = useMutation({
    mutationFn: (value: { reason: string }) => apiRequest<BackupAccepted>('/api/v1/backups', { method: 'POST', headers: { 'Idempotency-Key': newIdempotencyKey() }, body: JSON.stringify({ purpose: 'manual', reason: value.reason || 'manual backup' }) }),
    onSuccess: accepted => { setBackupJob(accepted.job.id); sessionStorage.setItem(`backup-job:${project?.id}`, accepted.job.id); messageApi.success('手动备份任务已提交') },
    onError: cause => messageApi.error(errorMessage(cause, '无法提交手动备份')),
  })
  const loadMore = useMutation({
    mutationFn: () => apiRequest<BackupPage>(`/api/v1/backups?limit=50&cursor=${encodeURIComponent(cursor || query.data?.next_cursor || '')}`),
    onSuccess: page => { setMore(current => [...current, ...page.items]); setCursor(page.next_cursor ?? '') },
    onError: cause => messageApi.error(errorMessage(cause, '无法读取更多备份')),
  })
  const inspect = useMutation({
    mutationFn: (item: BackupRecord) => postJSON<RestorePreflight>('/api/v1/restore-preflights', { backup_id: item.backup_id, target_mode: 'active' }),
    onSuccess: value => { setPreflight(value); setConfirmation('') },
    onError: cause => messageApi.error(errorMessage(cause, '恢复预检失败')),
  })
  const restore = useMutation({
    mutationFn: () => apiRequest<RestoreAccepted>('/api/v1/restores', { method: 'POST', headers: { 'Idempotency-Key': newIdempotencyKey() }, body: JSON.stringify({ backup_id: preflight!.backup.backup_id, target_mode: 'active', preflight_generation: preflight!.generation, confirmation: 'RESTORE' }) }),
    onSuccess: accepted => { setRestoreJob(accepted.job.id); sessionStorage.setItem(`restore-job:${project?.id}`, accepted.job.id); setPreflight(undefined); messageApi.success('恢复任务已进入维护队列') },
    onError: cause => messageApi.error(errorMessage(cause, '无法提交恢复任务')),
  })

  const restorable = (item: BackupRecord) => item.validation_state === 'valid' && ['current', 'older'].includes(item.compatibility_state) && item.project_uuid === project?.id

  return <div className="page-container backups-page">
    {contextHolder}
    <PageHeader title="备份与恢复" eyebrow={<><CloudSyncOutlined /> 恢复保护</>} description="查看项目恢复点、创建手动备份，并通过预检流程执行安全恢复。" extra={<Button icon={<ReloadOutlined />} onClick={() => void query.refetch()}>刷新清单</Button>} />
    <Card className="section-card" title="创建手动备份">
      <Form layout="inline" initialValues={{ reason: 'manual backup' }} onFinish={value => manual.mutate(value)}>
        <Form.Item name="reason" label="原因" style={{ flex: 1 }}><Input maxLength={256} placeholder="说明本次备份用途" /></Form.Item>
        <Form.Item><Button type="primary" htmlType="submit" icon={<CloudSyncOutlined />} loading={manual.isPending}>立即备份</Button></Form.Item>
      </Form>
    </Card>
    {backupJob && <JobProgress jobId={backupJob} title="备份任务" onDone={job => { if (job.status === 'succeeded') { sessionStorage.removeItem(`backup-job:${project?.id}`); void client.invalidateQueries({ queryKey: ['backups', project?.id] }) } }} />}
    {restoreJob && <JobProgress jobId={restoreJob} title="恢复任务" allowCancel={false} onDone={job => { if (job.status === 'succeeded') { sessionStorage.removeItem(`restore-job:${project?.id}`); void Promise.all([refreshProject(), refreshRuntime(), client.invalidateQueries({ queryKey: ['backups'] })]) } }} />}

    <Card className="section-card" title={<Space><DatabaseOutlined />恢复点清单</Space>}>
      {query.isError && <Alert className="block-alert" type="error" showIcon title={errorMessage(query.error, '无法读取备份清单')} action={<Button onClick={() => void query.refetch()}>重试</Button>} />}
      <Table<BackupRecord>
        rowKey="backup_id" loading={query.isLoading} dataSource={items} pagination={false} scroll={{ x: 1120 }}
        columns={[
          { title: '创建时间', dataIndex: 'created_at', width: 180, render: value => new Date(value).toLocaleString() },
          { title: '类型 / 来源', width: 170, render: (_, item) => <Space orientation="vertical" size={2}><Tag color="blue">{item.type}</Tag><span>{sourceSummary(item)}</span></Space> },
          { title: '版本', width: 160, render: (_, item) => <>App {item.app_version ?? '未知'}<br />Schema {item.schema_version ?? '未知'}</> },
          { title: '大小', dataIndex: 'db_bytes', width: 110, render: formatBytes },
          { title: '完整性', width: 210, render: (_, item) => <><Tag color={item.validation_state === 'valid' ? 'success' : 'error'}>{item.validation_state}</Tag><Tag>{item.compatibility_state}</Tag><br /><Typography.Text type="secondary">SHA-256 {shortID(item.db_sha256)}</Typography.Text></> },
          { title: '备份 ID', dataIndex: 'backup_id', ellipsis: true, render: value => <Typography.Text copyable={{ text: value }}>{shortID(value)}</Typography.Text> },
          { title: '操作', fixed: 'right', width: 140, render: (_, item) => <Button icon={<CloudDownloadOutlined />} disabled={!restorable(item)} loading={inspect.isPending} onClick={() => inspect.mutate(item)}>恢复预检</Button> },
        ]}
      />
      {(cursor || query.data?.next_cursor) && <Button className="load-more-button" loading={loadMore.isPending} onClick={() => loadMore.mutate()}>加载更多</Button>}
    </Card>

    <Modal open={Boolean(preflight)} title="恢复预检确认" okText="确认恢复" cancelText="取消" okButtonProps={{ danger: true, disabled: confirmation !== 'RESTORE', loading: restore.isPending }} onCancel={() => setPreflight(undefined)} onOk={() => restore.mutate()}>
      {preflight && <Space orientation="vertical" size="middle" style={{ width: '100%' }}>
        <Alert type={preflight.free_space_sufficient && preflight.writable && preflight.maintenance_available ? 'warning' : 'error'} showIcon title="恢复会进入全局维护状态" description="系统会先创建恢复前备份，随后替换当前项目数据并重建图谱。" />
        <Descriptions bordered size="small" column={1}>
          <Descriptions.Item label="备份">{shortID(preflight.backup.backup_id)}</Descriptions.Item>
          <Descriptions.Item label="空间充足">{preflight.free_space_sufficient ? '是' : '否'}</Descriptions.Item>
          <Descriptions.Item label="目录可写">{preflight.writable ? '是' : '否'}</Descriptions.Item>
          <Descriptions.Item label="需要迁移">{preflight.confirmation.migration_required ? '是' : '否'}</Descriptions.Item>
        </Descriptions>
        <Typography.Text>输入 <Typography.Text code>RESTORE</Typography.Text> 确认：</Typography.Text>
        <Input value={confirmation} onChange={event => setConfirmation(event.target.value)} aria-label="输入 RESTORE 确认恢复" />
      </Space>}
    </Modal>
  </div>
}
