import { useEffect, useState } from 'react'
import { useMutation, useQuery } from '@tanstack/react-query'
import { Alert, Button, Card, Col, Descriptions, Form, Input, InputNumber, Radio, Row, Select, Space, Table, Tag, Typography, message } from 'antd'
import { ExperimentOutlined, HistoryOutlined, PlayCircleOutlined, ReloadOutlined } from '@ant-design/icons'
import type { components } from '@/api/generated'
import PageHeader from '../components/PageHeader'
import JobProgress from '../components/JobProgress'
import { apiRequest, errorMessage, newIdempotencyKey, shortID } from '../api'
import { useAppState } from '../context/AppContext'

type RevisionPage = components['schemas']['RevisionPage']
type ReleasePage = components['schemas']['ReleasePage']
type SimulationRequest = components['schemas']['CreateSimulationJobRequest']
type SimulationAccepted = components['schemas']['SimulationJobAccepted']
type SimulationRun = components['schemas']['SimulationRun']
type Metric = components['schemas']['SimulationMetricResult']

const scenes = [
  { id: 'single-target-30s', label: '30 秒单目标', description: '1 个目标 · opening-strike · 默认 seed 11' },
  { id: 'single-target-180s', label: '180 秒单目标', description: '1 个目标 · opening-strike · 默认 seed 12' },
  { id: 'three-target-60s', label: '60 秒三目标', description: '3 个目标 · 三次打击 · 默认 seed 13' },
  { id: 'extreme-stacking-60s', label: '60 秒极限叠层', description: '1 个目标 · 叠层压力场景 · 默认 seed 14' },
]
const metrics = [
  { value: 'metric-dps', label: 'DPS' }, { value: 'metric-healing', label: '治疗' }, { value: 'metric-survivability', label: '生存' },
  { value: 'metric-resource', label: '资源' }, { value: 'metric-control', label: '控制' },
]

export default function SimulationsPage() {
  const { project } = useAppState()
  const [form] = Form.useForm()
  const sourceKind = Form.useWatch('source_kind', form) ?? 'revision'
  const [jobID, setJobID] = useState(sessionStorage.getItem(`simulation-active-job:${project?.id}`) ?? '')
  const [runID, setRunID] = useState('')
  const [messageApi, contextHolder] = message.useMessage()
  const revisions = useQuery({ queryKey: ['revisions', project?.id], queryFn: () => apiRequest<RevisionPage>('/api/v1/revisions'), enabled: Boolean(project) })
  const releases = useQuery({ queryKey: ['releases', project?.id], queryFn: () => apiRequest<ReleasePage>('/api/v1/releases'), enabled: Boolean(project) })
  const run = useQuery({ queryKey: ['simulation-run', runID], queryFn: () => apiRequest<SimulationRun>(`/api/v1/simulation-runs/${encodeURIComponent(runID)}`), enabled: Boolean(runID) })

  useEffect(() => {
    const first = sourceKind === 'revision' ? revisions.data?.items[0]?.id : releases.data?.items[0]?.id
    if (first && !form.getFieldValue('source_id')) form.setFieldValue('source_id', first)
  }, [form, releases.data, revisions.data, sourceKind])

  const submit = useMutation({
    mutationFn: (values: Record<string, unknown>) => {
      const budget = {
        max_events: values.max_events as number | undefined,
        max_steps: values.max_steps as number | undefined,
        max_runtime_ms: values.max_runtime_ms as number | undefined,
      }
      const request: SimulationRequest = {
        source: values.source_kind === 'release' ? { release_id: String(values.source_id) } : { revision_id: String(values.source_id) },
        scene_id: String(values.scene_id), scene_version: 'v1',
        parameters: { '/actions/opening-strike/inputs/amount': String(values.amount) },
        metrics: (values.metrics as string[]).map(id => ({ id, version: 'v1' })),
        sample_count: Number(values.sample_count),
        ...(values.seed !== undefined && values.seed !== '' ? { seed: Number(values.seed) } : {}),
        ...(Object.values(budget).some(value => value !== undefined) ? { budget } : {}),
        ...(values.verify_run_id ? { verify_run_id: String(values.verify_run_id) } : {}),
      }
      return apiRequest<SimulationAccepted>('/api/v1/simulation-jobs', { method: 'POST', headers: { 'Idempotency-Key': newIdempotencyKey() }, body: JSON.stringify(request) })
    },
    onSuccess: accepted => { setJobID(accepted.job.id); sessionStorage.setItem(`simulation-active-job:${project?.id}`, accepted.job.id); messageApi.success('模拟任务已固定输入并进入队列') },
    onError: cause => messageApi.error(errorMessage(cause, '无法创建模拟任务')),
  })

  function reproduce(value: SimulationRun) {
    if (!value.input || !value.reproducible) return
    const opening = value.input.actions.find(action => action.id === 'opening-strike') as { inputs?: Array<{ id?: string; value?: unknown }> } | undefined
    const amount = opening?.inputs?.find(input => input.id === 'amount')?.value
    form.setFieldsValue({
      source_kind: 'revision', source_id: value.input.revision_id, scene_id: value.input.scene_id,
      metrics: value.input.metrics.map(metric => metric.id), sample_count: value.input.sample_count,
      seed: value.input.seed, amount: amount ?? '10', verify_run_id: value.id,
      ...value.input.budgets,
    })
    messageApi.info(`已重建 ${shortID(value.id)} 的捕获选项；提交后会验证结果 Hash。`)
    window.scrollTo({ top: 0, behavior: 'smooth' })
  }

  const sourceOptions = sourceKind === 'revision'
    ? revisions.data?.items.map(item => ({ value: item.id, label: `#${item.display_revision} · ${shortID(item.id)} · ${item.metadata.name || '未命名'}` }))
    : releases.data?.items.map(item => ({ value: item.id, label: `${shortID(item.id)} · revision ${shortID(item.revision_id)}` }))

  return <div className="page-container simulations-page">
    {contextHolder}
    <PageHeader title="模拟实验" eyebrow={<><ExperimentOutlined /> 可复现计算</>} description="使用固定场景和指标运行确定性模拟；服务端会捕获不可变输入、实现指纹和结果 Hash。" />
    <Row gutter={[16, 16]}>
      <Col xs={24} xl={15}>
        <Card className="section-card" title="创建模拟任务">
          <Form form={form} layout="vertical" initialValues={{ source_kind: 'revision', scene_id: scenes[0].id, amount: '10', metrics: ['metric-dps'], sample_count: 1000 }} onFinish={values => submit.mutate(values)}>
            <Form.Item name="source_kind" label="来源类型"><Radio.Group optionType="button" buttonStyle="solid" options={[{ value: 'revision', label: '不可变 Revision' }, { value: 'release', label: '已发布 Release' }]} onChange={() => form.setFieldValue('source_id', undefined)} /></Form.Item>
            <Form.Item name="source_id" label={sourceKind === 'revision' ? '不可变 Revision' : '已发布 Release'} rules={[{ required: true, message: '请选择来源' }]}><Select showSearch optionFilterProp="label" options={sourceOptions} placeholder="请选择" /></Form.Item>
            <Form.Item name="scene_id" label="固定场景" rules={[{ required: true }]}><Select options={scenes.map(scene => ({ value: scene.id, label: scene.label, title: scene.description }))} /></Form.Item>
            <Row gutter={16}>
              <Col xs={24} md={12}><Form.Item name="amount" label="opening-strike 伤害（points）" rules={[{ required: true }]}><Input inputMode="decimal" /></Form.Item></Col>
              <Col xs={24} md={12}><Form.Item name="sample_count" label="样本数" rules={[{ required: true }]}><InputNumber min={1} max={1000} style={{ width: '100%' }} /></Form.Item></Col>
            </Row>
            <Form.Item name="metrics" label="Metric" rules={[{ required: true, type: 'array', min: 1, message: '至少选择一个 Metric' }]}><Select mode="multiple" options={metrics} /></Form.Item>
            <Row gutter={16}>
              <Col xs={24} md={6}><Form.Item name="seed" label="Seed（可选）"><InputNumber style={{ width: '100%' }} /></Form.Item></Col>
              <Col xs={24} md={6}><Form.Item name="max_events" label="最大事件数"><InputNumber min={1} max={10_000} style={{ width: '100%' }} /></Form.Item></Col>
              <Col xs={24} md={6}><Form.Item name="max_steps" label="最大步骤数"><InputNumber min={1} max={10_000} style={{ width: '100%' }} /></Form.Item></Col>
              <Col xs={24} md={6}><Form.Item name="max_runtime_ms" label="最大时间（ms）"><InputNumber min={1} max={30_000} style={{ width: '100%' }} /></Form.Item></Col>
            </Row>
            <Form.Item name="verify_run_id" hidden><Input /></Form.Item>
            <Button type="primary" size="large" htmlType="submit" icon={<PlayCircleOutlined />} loading={submit.isPending}>运行模拟</Button>
          </Form>
        </Card>
        {jobID && <JobProgress jobId={jobID} title="模拟任务" onDone={job => { if (job.status === 'succeeded' && job.result_id) { setRunID(job.result_id); sessionStorage.removeItem(`simulation-active-job:${project?.id}`) } }} />}
      </Col>
      <Col xs={24} xl={9}>
        <Card className="section-card" title={<Space><HistoryOutlined />读取历史 Run</Space>}>
          <Space.Compact block><Input value={runID} onChange={event => setRunID(event.target.value)} placeholder="Run ID" aria-label="Run ID" /><Button icon={<ReloadOutlined />} onClick={() => void run.refetch()}>读取</Button></Space.Compact>
          <Typography.Paragraph type="secondary" style={{ marginTop: 12 }}>历史 Run 始终按其捕获的不可变输入呈现，不会使用当前工作配置重算。</Typography.Paragraph>
        </Card>
      </Col>
    </Row>

    {run.isError && <Alert className="block-alert" type="error" showIcon title={errorMessage(run.error, '无法读取历史 Run')} />}
    {run.data && <Card className="section-card result-card" title={`模拟结果 · ${shortID(run.data.id)}`} extra={<Tag color={run.data.reproducible ? 'success' : 'warning'}>{run.data.reproducible ? '当前可复现' : '当前不可复现'}</Tag>}>
      <Descriptions bordered size="small" column={{ xs: 1, md: 3 }}>
        <Descriptions.Item label="来源 Revision"><Typography.Text copyable>{shortID(run.data.revision_id)}</Typography.Text></Descriptions.Item>
        <Descriptions.Item label="场景">{run.data.input ? `${run.data.input.scene_id}@${run.data.input.scene_version}` : '捕获输入不可用'}</Descriptions.Item>
        <Descriptions.Item label="样本 / Seed">{run.data.input ? `${run.data.input.sample_count} / ${run.data.input.seed}` : '—'}</Descriptions.Item>
        <Descriptions.Item label="Input Hash"><Typography.Text copyable code>{shortID(run.data.input_hash)}</Typography.Text></Descriptions.Item>
        <Descriptions.Item label="Result Hash"><Typography.Text copyable code>{shortID(run.data.result_hash)}</Typography.Text></Descriptions.Item>
        <Descriptions.Item label="实现指纹"><Typography.Text copyable code>{shortID(run.data.fingerprint_hash)}</Typography.Text></Descriptions.Item>
      </Descriptions>
      {!run.data.reproducible && <Alert className="block-alert" type="warning" showIcon title="当前环境不能复现该 Run" description={run.data.reasons.join('；')} />}
      <Table<Metric> rowKey={metric => `${metric.id}:${metric.version}`} pagination={false} dataSource={run.data.metrics} columns={[
        { title: 'Metric', render: (_, metric) => <><Typography.Text strong>{metric.id}@{metric.version}</Typography.Text><br /><Typography.Text type="secondary">{metric.direction}</Typography.Text></> },
        { title: '状态', dataIndex: 'status', width: 110, render: value => <Tag color={value === 'available' ? 'success' : 'error'}>{value}</Tag> },
        { title: '结果', render: (_, metric) => metric.status === 'available' ? <><Typography.Text strong>{metric.value} {metric.unit}</Typography.Text><br />CI {metric.confidence_low}–{metric.confidence_high}</> : `${metric.unavailable?.code} · ${metric.unavailable?.message}` },
        { title: '样本', dataIndex: 'sample_count', width: 100 },
        { title: '假设', dataIndex: 'assumptions', render: value => value.join('；') || '—' },
      ]} />
      {run.data.input && run.data.reproducible && <Button className="result-action" icon={<ReloadOutlined />} onClick={() => reproduce(run.data!)}>用此 Run 发起复现验证</Button>}
    </Card>}
  </div>
}
