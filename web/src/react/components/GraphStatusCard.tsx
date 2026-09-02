import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { Alert, Button, Card, Descriptions, Progress, Space, Tag, Typography, message } from 'antd'
import { ApartmentOutlined, ReloadOutlined, SyncOutlined } from '@ant-design/icons'
import type { components } from '@/api/generated'
import { apiRequest, errorMessage, newIdempotencyKey, shortID } from '../api'

type GraphStatus = components['schemas']['GraphStatus']
type GraphAccepted = components['schemas']['GraphSyncJobAccepted']

const stateColor: Record<string, string> = { graph_ready: 'success', graph_building: 'processing', graph_queued: 'processing', graph_failed: 'error', blocked_validation: 'error', saved: 'default', validating: 'warning' }

export default function GraphStatusCard({ revisionId, compact = false }: { revisionId: string; compact?: boolean }) {
  const client = useQueryClient()
  const [messageApi, contextHolder] = message.useMessage()
  const query = useQuery({ queryKey: ['graph-status', revisionId], queryFn: () => apiRequest<GraphStatus>(`/api/v1/revisions/${encodeURIComponent(revisionId)}/graph-status`), enabled: Boolean(revisionId), refetchInterval: value => ['graph_queued', 'graph_building', 'validating'].includes(value.state.data?.pipeline_state ?? '') ? 1_000 : false })
  const sync = useMutation({
    mutationFn: () => apiRequest<GraphAccepted>(`/api/v1/revisions/${encodeURIComponent(revisionId)}/graph-sync`, { method: 'POST', headers: { 'Idempotency-Key': newIdempotencyKey() }, body: JSON.stringify(query.data?.actions.includes('retry') && query.data.job ? { intent: 'retry', retry_of_job_id: query.data.job.id } : { intent: 'ensure' }) }),
    onSuccess: () => { messageApi.success('图谱同步任务已提交'); void client.invalidateQueries({ queryKey: ['graph-status', revisionId] }) },
    onError: cause => messageApi.error(errorMessage(cause, '图谱同步失败')),
  })
  const status = query.data
  if (compact) return <Space size={4}>{contextHolder}{query.isLoading ? <Tag>读取中</Tag> : query.isError ? <Tag color="error">不可读取</Tag> : <Tag color={stateColor[status!.pipeline_state]}>{status!.pipeline_state}</Tag>}{status?.actions.some(action => action === 'retry' || action === 'wait') && <Button size="small" type="text" icon={<ReloadOutlined />} loading={sync.isPending} onClick={() => sync.mutate()} />}</Space>

  return <Card className="section-card graph-card" title={<Space><ApartmentOutlined />图谱投影</Space>} extra={<Button icon={<SyncOutlined />} loading={sync.isPending} onClick={() => sync.mutate()}>确保同步</Button>}>
    {contextHolder}
    {query.isError && <Alert type="error" showIcon title={errorMessage(query.error, '图谱状态不可读取')} action={<Button onClick={() => void query.refetch()}>重试</Button>} />}
    {status && <>
      <Descriptions size="small" column={{ xs: 1, md: 3 }}>
        <Descriptions.Item label="流水线"><Tag color={stateColor[status.pipeline_state]}>{status.pipeline_state}</Tag></Descriptions.Item>
        <Descriptions.Item label="新鲜度"><Tag>{status.freshness}</Tag></Descriptions.Item>
        <Descriptions.Item label="校验">{status.validation_result ?? '尚未校验'}</Descriptions.Item>
        {status.projection && <Descriptions.Item label="规模">{status.projection.node_count} 节点 · {status.projection.edge_count} 边</Descriptions.Item>}
        {status.provider && <Descriptions.Item label="Provider">{status.provider.status} · {status.provider.query_ready ? '可查询' : '未就绪'}</Descriptions.Item>}
        {status.projection && <Descriptions.Item label="Manifest"><Typography.Text code>{shortID(status.projection.graph_manifest_hash)}</Typography.Text></Descriptions.Item>}
      </Descriptions>
      {typeof status.job_progress === 'number' && <Progress percent={Math.round(status.job_progress * 100)} status={status.pipeline_state === 'graph_failed' ? 'exception' : 'active'} />}
      {status.freshness_reasons.length > 0 && <Alert type="warning" showIcon title="图谱投影不是最新状态" description={status.freshness_reasons.join('；')} />}
      {status.error && <Alert type="error" showIcon title={status.error.code} description={status.error.message} />}
    </>}
  </Card>
}
