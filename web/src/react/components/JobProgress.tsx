import { useEffect, useRef } from 'react'
import { useQuery, useQueryClient } from '@tanstack/react-query'
import { Alert, Button, Card, Descriptions, Progress, Space, Tag, Typography } from 'antd'
import { CloseCircleOutlined, LoadingOutlined, ReloadOutlined } from '@ant-design/icons'
import type { components } from '@/api/generated'
import { apiRequest, errorMessage } from '../api'

type Job = components['schemas']['Job']

interface Props {
  jobId: string
  title?: string
  onDone?: (job: Job) => void
  onRetry?: () => void
  allowCancel?: boolean
}

const terminal = new Set<Job['status']>(['succeeded', 'failed', 'canceled', 'interrupted'])
const statusColor: Record<Job['status'], string> = { queued: 'default', running: 'processing', succeeded: 'success', failed: 'error', canceled: 'default', interrupted: 'warning' }
const statusLabel: Record<Job['status'], string> = { queued: '排队中', running: '执行中', succeeded: '已完成', failed: '失败', canceled: '已取消', interrupted: '已中断' }

export default function JobProgress({ jobId, title = '任务进度', onDone, onRetry, allowCancel = true }: Props) {
  const client = useQueryClient()
  const announced = useRef('')
  const query = useQuery({
    queryKey: ['job', jobId],
    queryFn: () => apiRequest<Job>(`/api/v1/jobs/${encodeURIComponent(jobId)}`),
    enabled: Boolean(jobId),
    refetchInterval: value => {
      const job = value.state.data
      return job && terminal.has(job.status) ? false : Math.max(job?.poll_after_ms ?? 1_000, 500)
    },
  })
  const job = query.data

  useEffect(() => {
    if (job && terminal.has(job.status) && announced.current !== `${job.id}:${job.status}`) {
      announced.current = `${job.id}:${job.status}`
      onDone?.(job)
    }
  }, [job, onDone])

  async function cancel() {
    await apiRequest<Job>(`/api/v1/jobs/${encodeURIComponent(jobId)}/cancel`, { method: 'POST' })
    await client.invalidateQueries({ queryKey: ['job', jobId] })
  }

  return <Card className="job-card" title={<Space><LoadingOutlined spin={job?.status === 'running'} />{title}</Space>}
    extra={job && <Tag color={statusColor[job.status]}>{statusLabel[job.status]}</Tag>}>
    {query.isPending && <Typography.Text type="secondary">正在读取任务状态…</Typography.Text>}
    {query.error && <Alert type="error" showIcon title={errorMessage(query.error, '无法读取任务状态')} action={<Button onClick={() => void query.refetch()}>重试</Button>} />}
    {job && <Space orientation="vertical" size="middle" style={{ width: '100%' }}>
      <Progress percent={Math.round((job.progress ?? (job.status === 'succeeded' ? 1 : 0)) * 100)} status={job.status === 'failed' ? 'exception' : job.status === 'succeeded' ? 'success' : 'active'} />
      <Descriptions size="small" column={{ xs: 1, md: 3 }}>
        <Descriptions.Item label="任务 ID"><Typography.Text copyable code>{job.id}</Typography.Text></Descriptions.Item>
        <Descriptions.Item label="阶段">{job.phase || job.kind}</Descriptions.Item>
        <Descriptions.Item label="更新时间">{job.updated_at ? new Date(job.updated_at).toLocaleString() : new Date(job.created_at).toLocaleString()}</Descriptions.Item>
        {job.result_id && <Descriptions.Item label="结果 ID"><Typography.Text copyable>{job.result_id}</Typography.Text></Descriptions.Item>}
      </Descriptions>
      {job.warning && <Alert type="warning" showIcon title={job.warning} />}
      {job.error && <Alert type="error" showIcon title={job.error} />}
      <Space>
        {allowCancel && !terminal.has(job.status) && <Button danger icon={<CloseCircleOutlined />} onClick={() => void cancel()}>取消任务</Button>}
        {(job.status === 'failed' || job.status === 'interrupted') && onRetry && <Button type="primary" icon={<ReloadOutlined />} onClick={onRetry}>重试</Button>}
      </Space>
    </Space>}
  </Card>
}
