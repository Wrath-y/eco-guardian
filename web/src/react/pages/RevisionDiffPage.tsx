import { useEffect, useState } from 'react'
import { useMutation, useQuery } from '@tanstack/react-query'
import { Alert, Button, Card, Col, Collapse, Descriptions, Form, Input, Row, Select, Space, Switch, Table, Tag, Typography, message } from 'antd'
import { ArrowLeftOutlined, CheckCircleOutlined, DiffOutlined, RocketOutlined, SafetyCertificateOutlined } from '@ant-design/icons'
import { useNavigate, useParams, useSearchParams } from 'react-router-dom'
import type { components } from '@/api/generated'
import PageHeader from '../components/PageHeader'
import GraphStatusCard from '../components/GraphStatusCard'
import JobProgress from '../components/JobProgress'
import { apiRequest, errorMessage, newIdempotencyKey, shortID } from '../api'
import { useAppState } from '../context/AppContext'

type RevisionDetail = components['schemas']['RevisionDetail']
type RevisionDiff = components['schemas']['RevisionDiff']
type FieldChange = components['schemas']['FieldChange']
type PolicyPage = components['schemas']['ReleasePolicyPage']
type ReleasePage = components['schemas']['ReleasePage']
type RuntimeCapabilities = components['schemas']['RuntimeCapabilities']
type CreateRelease = components['schemas']['CreateReleaseRequest']
type ReleaseAccepted = components['schemas']['ReleaseJobAccepted']

export default function RevisionDiffPage() {
  const { id = '' } = useParams()
  const [params, setParams] = useSearchParams()
  const navigate = useNavigate()
  const { project } = useAppState()
  const [baseDraft, setBaseDraft] = useState(params.get('base') ?? '')
  const [releaseJob, setReleaseJob] = useState('')
  const [form] = Form.useForm()
  const [messageApi, contextHolder] = message.useMessage()
  const baseID = params.get('base') ?? ''
  const detail = useQuery({ queryKey: ['revision', id], queryFn: () => apiRequest<RevisionDetail>(`/api/v1/revisions/${id}`), enabled: Boolean(id) })
  const diff = useQuery({ queryKey: ['revision-diff', baseID, id], queryFn: () => apiRequest<RevisionDiff>(`/api/v1/revisions/${id}/diff?base=${encodeURIComponent(baseID)}`), enabled: Boolean(baseID && id) })
  const policies = useQuery({ queryKey: ['release-policies', project?.id], queryFn: () => apiRequest<PolicyPage>('/api/v1/release-policies') })
  const releases = useQuery({ queryKey: ['releases', project?.id], queryFn: () => apiRequest<ReleasePage>('/api/v1/releases') })
  const capabilities = useQuery({ queryKey: ['runtime-capabilities'], queryFn: () => apiRequest<RuntimeCapabilities>('/api/v1/runtime/capabilities') })

  useEffect(() => {
    if (!policies.data?.items[0]) return
    form.setFieldsValue({ policy_id: policies.data.items[0].id, baseline_release_id: releases.data?.items[0]?.id ?? null, notes: '', acknowledge_warning: false, numeric_override: false, numeric_reason: '' })
  }, [form, policies.data, releases.data])

  const release = useMutation({
    mutationFn: (values: { policy_id: string; baseline_release_id?: string | null; notes?: string; acknowledge_warning?: boolean; numeric_override?: boolean; numeric_reason?: string }) => {
      const confirmations: CreateRelease['confirmations'] = []
      if (!values.baseline_release_id) confirmations.push({ kind: 'establish_baseline', confirmed: true })
      if (values.acknowledge_warning) confirmations.push({ kind: 'acknowledge_warning', confirmed: true })
      if (values.numeric_override) confirmations.push({ kind: 'numeric_override', confirmed: true, reason: values.numeric_reason })
      const request: CreateRelease = {
        candidate_revision_id: id,
        config_hash: detail.data!.config_hash,
        version_manifest_hash: detail.data!.metadata.version_manifest.hash,
        policy_id: values.policy_id,
        expected_baseline_release_id: values.baseline_release_id || null,
        notes: values.notes,
        confirmations,
      }
      return apiRequest<ReleaseAccepted>('/api/v1/releases', { method: 'POST', headers: { 'Idempotency-Key': newIdempotencyKey() }, body: JSON.stringify(request) })
    },
    onSuccess: accepted => { setReleaseJob(accepted.job_id); messageApi.success('发布任务已通过接收预检并进入队列') },
    onError: cause => messageApi.error(errorMessage(cause, '发布预检未通过')),
  })

  function compare() { const next = new URLSearchParams(params); if (baseDraft.trim()) next.set('base', baseDraft.trim()); else next.delete('base'); setParams(next) }

  return <div className="page-container diff-page">
    {contextHolder}
    <PageHeader title="版本差异与发布" eyebrow={<><DiffOutlined /> 候选审阅</>} description="比较不可变版本，查看字段级变化，并使用服务端权威 Gate 提交发布。" extra={<Button icon={<ArrowLeftOutlined />} onClick={() => navigate('/versions')}>返回版本历史</Button>} />
    {detail.isError && <Alert type="error" showIcon title={errorMessage(detail.error, '无法读取候选版本')} />}
    {detail.data && <Card className="section-card candidate-summary">
      <Descriptions column={{ xs: 1, md: 3 }}>
        <Descriptions.Item label="候选版本">#{detail.data.display_revision}</Descriptions.Item>
        <Descriptions.Item label="Config Hash"><Typography.Text code copyable>{shortID(detail.data.config_hash)}</Typography.Text></Descriptions.Item>
        <Descriptions.Item label="Manifest"><Typography.Text code copyable>{shortID(detail.data.metadata.version_manifest.hash)}</Typography.Text></Descriptions.Item>
      </Descriptions>
    </Card>}
    <GraphStatusCard revisionId={id} />

    <Card className="section-card" title="字段差异" extra={<Space.Compact><Input value={baseDraft} onChange={event => setBaseDraft(event.target.value)} placeholder="基准版本 ID" aria-label="基准版本 ID" style={{ width: 320 }} /><Button type="primary" onClick={compare}>比较</Button></Space.Compact>}>
      {!baseID && <Alert type="info" showIcon title="尚未选择不可变基准" description="首次发布不是“无变化”或通过比较，仍需完成全部 Gate 并明确建立基线。" />}
      {diff.isError && <Alert type="error" showIcon title={errorMessage(diff.error, '基准版本无效或不可读取')} />}
      {diff.data?.baseline_state === 'NO_BASELINE' && <Alert type="warning" showIcon title="当前项目没有正式版本基准" />}
      <Table<FieldChange>
        rowKey={row => `${row.entity_id}:${row.path}:${row.kind}`} loading={diff.isLoading} dataSource={diff.data?.changes ?? []} pagination={false}
        expandable={{ expandedRowRender: row => <Row gutter={16}><Col span={12}><Typography.Text strong>旧值</Typography.Text><pre>{JSON.stringify(row.old_value, null, 2) ?? '—'}</pre></Col><Col span={12}><Typography.Text strong>新值</Typography.Text><pre>{JSON.stringify(row.new_value, null, 2) ?? '—'}</pre></Col></Row> }}
        columns={[
          { title: '类型', dataIndex: 'kind', width: 100, render: value => <Tag color={value === 'DELETE' ? 'error' : value === 'ADD' ? 'success' : 'processing'}>{value}</Tag> },
          { title: '实体', render: (_, row) => <Button type="link" onClick={() => navigate(`/config/${row.entity_kind}/${row.entity_id}?field_path=${encodeURIComponent(row.path)}`)}>{row.entity_kind} · {shortID(row.entity_id)}</Button> },
          { title: '字段路径', dataIndex: 'path', render: value => <Typography.Text code>{value}</Typography.Text> },
          { title: '位置变化', width: 150, render: (_, row) => row.old_ordinal === undefined && row.new_ordinal === undefined ? '—' : `${row.old_ordinal ?? '—'} → ${row.new_ordinal ?? '—'}` },
        ]}
      />
    </Card>

    <Card className="section-card release-card" title={<Space><SafetyCertificateOutlined />发布检查</Space>}>
      {capabilities.data && <Alert className="block-alert" type={capabilities.data.release.enabled ? 'success' : 'warning'} showIcon icon={<CheckCircleOutlined />} title={capabilities.data.release.enabled ? '服务端发布入口当前可用' : '服务端发布入口当前不可用'} description={capabilities.data.release.disabled_reasons?.map(reason => `${reason.gate_id}: ${reason.code}`).join('；') || '最终 Gate 仍会在提交时重新校验。'} />}
      <Form form={form} layout="vertical" onFinish={values => release.mutate(values)}>
        <Row gutter={16}>
          <Col xs={24} md={12}><Form.Item name="policy_id" label="Release Policy" rules={[{ required: true }]}><Select options={policies.data?.items.map(item => ({ value: item.id, label: `Policy #${item.display_version} · ${shortID(item.id)}` }))} /></Form.Item></Col>
          <Col xs={24} md={12}><Form.Item name="baseline_release_id" label="预期基准版本"><Select allowClear placeholder="留空以建立首次基线" options={releases.data?.items.map(item => ({ value: item.id, label: `${shortID(item.id)} · revision ${shortID(item.revision_id)}` }))} /></Form.Item></Col>
        </Row>
        <Form.Item name="notes" label="发布说明"><Input.TextArea rows={3} /></Form.Item>
        <Row gutter={16}>
          <Col xs={24} md={12}><Form.Item name="acknowledge_warning" label="确认 WARNING" valuePropName="checked"><Switch /></Form.Item></Col>
          <Col xs={24} md={12}><Form.Item name="numeric_override" label="数值门禁覆盖" valuePropName="checked"><Switch /></Form.Item></Col>
        </Row>
        <Form.Item noStyle shouldUpdate={(before, after) => before.numeric_override !== after.numeric_override}>{({ getFieldValue }) => getFieldValue('numeric_override') && <Form.Item name="numeric_reason" label="数值覆盖原因" rules={[{ required: true, min: 8 }]}><Input.TextArea /></Form.Item>}</Form.Item>
        <Button type="primary" size="large" icon={<RocketOutlined />} htmlType="submit" loading={release.isPending} disabled={!capabilities.data?.release.enabled || !detail.data}>提交发布任务</Button>
      </Form>
      {releaseJob && <JobProgress jobId={releaseJob} title="发布任务" allowCancel />}
    </Card>
  </div>
}
