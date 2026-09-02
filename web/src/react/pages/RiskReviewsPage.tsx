import { useEffect, useMemo, useState } from 'react'
import { useMutation, useQuery } from '@tanstack/react-query'
import { Alert, Button, Card, Col, Collapse, Descriptions, Form, Input, Radio, Row, Select, Space, Statistic, Table, Tag, Typography, message } from 'antd'
import { FileSearchOutlined, LineChartOutlined, SafetyCertificateOutlined, ThunderboltOutlined } from '@ant-design/icons'
import { useNavigate, useSearchParams } from 'react-router-dom'
import type { components } from '@/api/generated'
import PageHeader from '../components/PageHeader'
import JobProgress from '../components/JobProgress'
import { apiRequest, errorMessage, newIdempotencyKey, shortID } from '../api'
import { useAppState } from '../context/AppContext'

type RevisionPage = components['schemas']['RevisionPage']
type RevisionDetail = components['schemas']['RevisionDetail']
type ReleasePage = components['schemas']['ReleasePage']
type PolicyPage = components['schemas']['ReleasePolicyPage']
type RiskCommand = components['schemas']['RiskReviewCommand']
type RiskAccepted = components['schemas']['RiskJobAccepted']
type RiskReview = components['schemas']['RiskReview']
type RiskItem = components['schemas']['RiskReviewItem']

interface Requirement { key: string; scene_id: string; scene_version: string; metric_id: string; metric_version: string; role: 'required' | 'optional' }

const gateColor: Record<string, string> = { PASS: 'success', WARNING: 'warning', BLOCK: 'error', UNAVAILABLE: 'default', STALE: 'warning' }

export default function RiskReviewsPage() {
  const { project } = useAppState()
  const [form] = Form.useForm()
  const [params, setParams] = useSearchParams()
  const navigate = useNavigate()
  const [reportInput, setReportInput] = useState(params.get('report') ?? '')
  const [jobID, setJobID] = useState(sessionStorage.getItem(`risk-active-job:${project?.id}`) ?? '')
  const [messageApi, contextHolder] = message.useMessage()
  const reportID = params.get('report') ?? ''
  const candidateID = Form.useWatch('candidate_revision_id', form) as string | undefined
  const baselineKind = Form.useWatch('baseline_kind', form) ?? 'BASELINE'
  const baselineReleaseID = Form.useWatch('baseline_release_id', form) as string | undefined
  const policyID = Form.useWatch('policy_id', form) as string | undefined
  const thresholdMode = Form.useWatch('threshold_mode', form) ?? 'starter'
  const thresholdConfirmed = Form.useWatch('threshold_confirmed', form)

  const revisions = useQuery({ queryKey: ['revisions', project?.id], queryFn: () => apiRequest<RevisionPage>('/api/v1/revisions') })
  const releases = useQuery({ queryKey: ['releases', project?.id], queryFn: () => apiRequest<ReleasePage>('/api/v1/releases') })
  const policies = useQuery({ queryKey: ['release-policies', project?.id], queryFn: () => apiRequest<PolicyPage>('/api/v1/release-policies') })
  const candidate = useQuery({ queryKey: ['revision', candidateID], queryFn: () => apiRequest<RevisionDetail>(`/api/v1/revisions/${candidateID}`), enabled: Boolean(candidateID) })
  const baselineRelease = releases.data?.items.find(item => item.id === baselineReleaseID)
  const baseline = useQuery({ queryKey: ['revision', baselineRelease?.revision_id], queryFn: () => apiRequest<RevisionDetail>(`/api/v1/revisions/${baselineRelease!.revision_id}`), enabled: baselineKind === 'BASELINE' && Boolean(baselineRelease) })
  const review = useQuery({ queryKey: ['risk-review', reportID], queryFn: () => apiRequest<RiskReview>(`/api/v1/risk-reviews/${encodeURIComponent(reportID)}`), enabled: Boolean(reportID) })
  const policy = policies.data?.items.find(item => item.id === policyID)
  const requirements = useMemo<Requirement[]>(() => policy?.scenes.flatMap(scene => scene.metrics.map(metric => ({ key: `${scene.id}:${scene.scene_version || 'v1'}:${metric.id}:v1`, scene_id: scene.id, scene_version: scene.scene_version || 'v1', metric_id: metric.id, metric_version: 'v1', role: scene.required && metric.required ? 'required' : 'optional' }))) ?? [], [policy])

  useEffect(() => {
    form.setFieldsValue({
      candidate_revision_id: form.getFieldValue('candidate_revision_id') || revisions.data?.items[0]?.id,
      baseline_kind: 'BASELINE', baseline_release_id: form.getFieldValue('baseline_release_id') || releases.data?.items[0]?.id,
      policy_id: form.getFieldValue('policy_id') || policies.data?.items[0]?.id,
      threshold_mode: 'starter', threshold_confirmed: false,
    })
  }, [form, policies.data, releases.data, revisions.data])

  const create = useMutation({
    mutationFn: (values: Record<string, unknown>) => {
      if (!candidate.data || !policy) throw new Error('候选 Revision 或 Release Policy 尚未加载完成')
      if (baselineKind === 'BASELINE' && (!baselineRelease || !baseline.data)) throw new Error('Baseline Release 身份尚未加载完成')
      const runs = (values.runs ?? []) as Array<{ candidate_id: string; candidate_hash: string; baseline_id?: string; baseline_hash?: string }>
      const simulationRuns: components['schemas']['RiskSimulationRef'][] = []
      const seen = new Set<string>()
      for (const run of runs) {
        for (const [id, hash] of [[run.candidate_id, run.candidate_hash], ...(baselineKind === 'BASELINE' ? [[run.baseline_id, run.baseline_hash]] : [])] as Array<[string | undefined, string | undefined]>) {
          if (id && hash && !seen.has(id)) { seen.add(id); simulationRuns.push({ run_id: id, result_hash: hash }) }
        }
      }
      let threshold: components['schemas']['RiskThresholdSelection']
      if (thresholdMode === 'existing') threshold = { type: 'EXISTING', threshold: { id: String(values.threshold_id), version: String(values.threshold_version), hash: String(values.threshold_hash) } }
      else if (thresholdMode === 'modified') threshold = { type: 'MODIFIED_STARTER', confirmed: true, body: { schema_version: 'v1', source: 'modified-starter', assumptions: ['metric-resource@v1 target_range [0.8, 1.2] inclusive'], entries: requirements.map(item => ({ scene_id: item.scene_id, scene_version: item.scene_version, metric_id: item.metric_id, metric_version: item.metric_version, balance_group: null, unit: 'ratio', direction: 'target_range', relative_warning: String(values.relative_warning), relative_block: String(values.relative_block), absolute_warning: null, absolute_block: null })), structural_rule_versions: [{ id: String(values.rule_id), version: String(values.rule_version), hash: String(values.rule_hash) }] } }
      else threshold = { type: 'STARTER', confirmed: true }
      const command: RiskCommand = {
        command: 'evaluate',
        candidate: { revision_id: candidate.data.id, config_hash: candidate.data.config_hash, version_manifest_hash: candidate.data.metadata.version_manifest.hash },
        baseline: baselineKind === 'NO_BASELINE' ? { type: 'NO_BASELINE' } : { type: 'BASELINE', release_id: baselineRelease!.id, revision: { revision_id: baseline.data!.id, config_hash: baseline.data!.config_hash, version_manifest_hash: baseline.data!.metadata.version_manifest.hash } },
        policy: { id: policy.id, version: String(policy.display_version), hash: policy.policy_hash },
        policy_requirements: requirements.map(({ key: _key, ...item }) => item),
        threshold,
        simulation_runs: simulationRuns,
      }
      return apiRequest<RiskAccepted>('/api/v1/risk-reviews', { method: 'POST', headers: { 'Idempotency-Key': newIdempotencyKey() }, body: JSON.stringify(command) })
    },
    onSuccess: accepted => { setJobID(accepted.job.id); sessionStorage.setItem(`risk-active-job:${project?.id}`, accepted.job.id); messageApi.success('风险复核任务已固定输入并进入队列') },
    onError: cause => messageApi.error(errorMessage(cause, '无法创建风险复核')),
  })
  const decision = useMutation({
    mutationFn: (value: { items: Array<{ item_id: string; item_hash: string }>; reason: string }) => {
      const command: RiskCommand = { command: 'record_numeric_decision', source_report_id: review.data!.id, source_calculation_hash: review.data!.calculation_hash, eligible_items: value.items, reason: value.reason }
      return apiRequest<RiskAccepted>('/api/v1/risk-reviews', { method: 'POST', headers: { 'Idempotency-Key': newIdempotencyKey() }, body: JSON.stringify(command) })
    },
    onSuccess: accepted => { setJobID(accepted.job.id); messageApi.success('不可变数值决策报告正在生成') },
    onError: cause => messageApi.error(errorMessage(cause, '无法创建决策报告')),
  })

  function openReport(id: string) { setReportInput(id); setParams({ report: id }); setJobID(''); sessionStorage.removeItem(`risk-active-job:${project?.id}`) }
  const eligible = review.data?.items.filter(item => item.overridable && item.severity === 'BLOCK').map(item => ({ item_id: item.id, item_hash: item.item_hash })) ?? []

  return <div className="page-container risk-page">
    {contextHolder}
    <PageHeader title="风险复核" eyebrow={<><FileSearchOutlined /> 服务端权威评估</>} description="服务器固定输入、执行精确 Decimal 比较并投影 Gate；浏览器只负责选择与呈现。" />
    <Form form={form} layout="vertical" onFinish={values => create.mutate(values)}>
      <Row gutter={[16, 16]}>
        <Col xs={24} xl={12}><Card className="section-card risk-form-card" title="Candidate、Baseline 与 Policy">
          <Form.Item name="candidate_revision_id" label="候选 Revision" rules={[{ required: true }]}><Select showSearch optionFilterProp="label" options={revisions.data?.items.map(item => ({ value: item.id, label: `#${item.display_revision} · ${shortID(item.id)}` }))} /></Form.Item>
          {candidate.data && <Alert className="block-alert" type="info" showIcon title={`Config ${shortID(candidate.data.config_hash)}`} description={`Manifest ${shortID(candidate.data.metadata.version_manifest.hash)}`} />}
          <Form.Item name="baseline_kind" label="Baseline 类型"><Radio.Group optionType="button" buttonStyle="solid" options={[{ value: 'BASELINE', label: '当前正式 Release' }, { value: 'NO_BASELINE', label: '首次发布（NO_BASELINE）' }]} /></Form.Item>
          {baselineKind === 'BASELINE' ? <Form.Item name="baseline_release_id" label="当前 Release" rules={[{ required: true }]}><Select options={releases.data?.items.map(item => ({ value: item.id, label: `${shortID(item.id)} · revision ${shortID(item.revision_id)}` }))} /></Form.Item> : <Alert className="block-alert" type="warning" showIcon title="NO_BASELINE 不等于 PASS" description="报告完成后发布仍需明确建立基线。" />}
          <Form.Item name="policy_id" label="Release Policy" rules={[{ required: true }]}><Select options={policies.data?.items.map(item => ({ value: item.id, label: `Policy #${item.display_version} · ${shortID(item.id)}` }))} /></Form.Item>
        </Card></Col>
        <Col xs={24} xl={12}><Card className="section-card risk-form-card" title="Threshold 选择">
          <Alert className="block-alert" type="info" showIcon title="Starter 边界" description="Relative WARNING 10%、BLOCK 25%；metric-resource@v1 目标区间 [0.8, 1.2] inclusive。" />
          <Form.Item name="threshold_mode" label="Threshold 来源"><Radio.Group options={[{ value: 'starter', label: '启用 Starter' }, { value: 'existing', label: '已有 Enabled Version' }, { value: 'modified', label: '修改 Starter' }]} /></Form.Item>
          {thresholdMode === 'existing' && <Row gutter={12}><Col span={8}><Form.Item name="threshold_id" label="ID" rules={[{ required: true }]}><Input /></Form.Item></Col><Col span={6}><Form.Item name="threshold_version" label="Version" rules={[{ required: true }]}><Input /></Form.Item></Col><Col span={10}><Form.Item name="threshold_hash" label="Body Hash" rules={[{ required: true, len: 64 }]}><Input /></Form.Item></Col></Row>}
          {thresholdMode === 'modified' && <><Alert className="block-alert" type={requirements.every(item => item.metric_id === 'metric-resource') ? 'warning' : 'error'} showIcon title={requirements.every(item => item.metric_id === 'metric-resource') ? '仅使用当前 Policy 的 metric-resource 权威元数据' : '当前 Policy 不支持浏览器修改 Starter，请选择其他模式'} /><Row gutter={12}><Col span={12}><Form.Item name="relative_warning" label="Relative WARNING" initialValue="0.1"><Input /></Form.Item></Col><Col span={12}><Form.Item name="relative_block" label="Relative BLOCK" initialValue="0.25"><Input /></Form.Item></Col><Col span={8}><Form.Item name="rule_id" label="结构规则 ID" initialValue="risk-structure"><Input /></Form.Item></Col><Col span={6}><Form.Item name="rule_version" label="Version" initialValue="v1"><Input /></Form.Item></Col><Col span={10}><Form.Item name="rule_hash" label="Hash" rules={[{ required: true, len: 64 }]}><Input /></Form.Item></Col></Row></>}
          <Form.Item name="threshold_confirmed" valuePropName="checked" rules={[{ validator: (_, value) => value ? Promise.resolve() : Promise.reject(new Error('必须明确确认启用 Threshold')) }]}><Radio checked={thresholdConfirmed} onClick={() => form.setFieldValue('threshold_confirmed', true)}>我明确确认创建并启用此 Threshold Version</Radio></Form.Item>
        </Card></Col>
      </Row>

      <Card className="section-card" title={`Policy Scene / Metric 与精确 Simulation Result（${requirements.length}）`}>
        {!requirements.length && <Alert type="warning" showIcon title="当前 Policy 没有可呈现的 Requirement" />}
        {requirements.map((item, index) => <Card key={item.key} size="small" className="requirement-card" title={`${item.scene_id}@${item.scene_version} / ${item.metric_id}@${item.metric_version}`} extra={<Tag>{item.role}</Tag>}>
          <Row gutter={12}>
            <Col xs={24} md={12}><Form.Item name={['runs', index, 'candidate_id']} label="Candidate Run ID" rules={[{ required: true }]}><Input /></Form.Item></Col>
            <Col xs={24} md={12}><Form.Item name={['runs', index, 'candidate_hash']} label="Candidate Result Hash" rules={[{ required: true, len: 64 }]}><Input /></Form.Item></Col>
            {baselineKind === 'BASELINE' && <><Col xs={24} md={12}><Form.Item name={['runs', index, 'baseline_id']} label="Baseline Run ID" rules={[{ required: true }]}><Input /></Form.Item></Col><Col xs={24} md={12}><Form.Item name={['runs', index, 'baseline_hash']} label="Baseline Result Hash" rules={[{ required: true, len: 64 }]}><Input /></Form.Item></Col></>}
          </Row>
        </Card>)}
        <Button type="primary" size="large" icon={<ThunderboltOutlined />} htmlType="submit" loading={create.isPending} disabled={!candidate.data || !policy || (thresholdMode === 'modified' && !requirements.every(item => item.metric_id === 'metric-resource'))}>创建风险复核任务</Button>
      </Card>
    </Form>
    {jobID && <JobProgress jobId={jobID} title="风险复核任务" onDone={job => { if (job.status === 'succeeded' && job.result_id) openReport(job.result_id) }} />}

    <Card className="section-card" title="不可变报告"><Space.Compact block><Input value={reportInput} onChange={event => setReportInput(event.target.value)} placeholder="Report ID" aria-label="Report ID" /><Button type="primary" onClick={() => reportInput.trim() && openReport(reportInput.trim())}>读取报告</Button></Space.Compact></Card>
    {review.isError && <Alert className="block-alert" type="error" showIcon title={errorMessage(review.error, '无法读取风险报告')} />}
    {review.data && <RiskReportView review={review.data} onOpenDiff={() => navigate(`/versions/${review.data!.candidate.revision_id}/diff?risk_review=${review.data!.id}`)} onDecision={(items, reason) => decision.mutate({ items, reason })} decisionLoading={decision.isPending} />}
  </div>
}

function RiskReportView({ review, onOpenDiff, onDecision, decisionLoading }: { review: RiskReview; onOpenDiff: () => void; onDecision: (items: Array<{ item_id: string; item_hash: string }>, reason: string) => void; decisionLoading: boolean }) {
  const [decisionForm] = Form.useForm()
  const eligible = review.items.filter(item => item.overridable && item.severity === 'BLOCK').map(item => ({ item_id: item.id, item_hash: item.item_hash }))
  return <div className="risk-report">
    <Card className="section-card report-summary-card" title={<Space><SafetyCertificateOutlined />不可变风险报告</Space>} extra={<Button onClick={onOpenDiff}>转到版本差异与发布确认</Button>}>
      <Row gutter={[16, 16]}>
        <Col xs={12} lg={6}><Statistic title="Gate" value={review.read_time.gate_state} valueStyle={{ color: review.read_time.gate_state === 'PASS' ? '#16a34a' : review.read_time.gate_state === 'BLOCK' ? '#dc2626' : '#d97706' }} /></Col>
        <Col xs={12} lg={6}><Statistic title="Freshness" value={review.read_time.freshness} /></Col>
        <Col xs={12} lg={6}><Statistic title="BLOCK" value={review.items.filter(item => item.severity === 'BLOCK').length} /></Col>
        <Col xs={12} lg={6}><Statistic title="WARNING" value={review.items.filter(item => item.severity === 'WARNING').length} /></Col>
      </Row>
      <Descriptions size="small" column={{ xs: 1, md: 3 }}>
        <Descriptions.Item label="报告"><Typography.Text copyable>{shortID(review.id)}</Typography.Text></Descriptions.Item>
        <Descriptions.Item label="类型"><Tag>{review.report_kind}</Tag></Descriptions.Item>
        <Descriptions.Item label="当前投影"><Tag color={gateColor[review.read_time.gate_state]}>{review.read_time.gate_state}</Tag></Descriptions.Item>
        <Descriptions.Item label="Candidate">{shortID(review.candidate.revision_id)}</Descriptions.Item>
        <Descriptions.Item label="Baseline">{review.baseline.type === 'BASELINE' ? shortID(review.baseline.release_id) : 'NO_BASELINE'}</Descriptions.Item>
        <Descriptions.Item label="Threshold">{review.threshold.identity.id}@{review.threshold.identity.version}</Descriptions.Item>
      </Descriptions>
      {review.read_time.freshness_reasons.length > 0 && <Alert type="warning" showIcon title="报告当前投影不是最新状态" description={review.read_time.freshness_reasons.join('；')} />}
    </Card>
    <Card className="section-card" title={<Space><LineChartOutlined />比较结果</Space>}>
      <Table<RiskItem> rowKey="id" dataSource={review.items} scroll={{ x: 1200 }} pagination={false} columns={[
        { title: 'Scene / Metric', width: 230, render: (_, item) => item.metric_evidence ? `${item.metric_evidence.scene_id}@${item.metric_evidence.scene_version} / ${item.metric_evidence.metric_id}@${item.metric_evidence.metric_version}` : item.rule.id },
        { title: '角色', dataIndex: 'role', width: 90 },
        { title: '比较状态', dataIndex: 'comparison_status', width: 150, render: value => <Tag>{value}</Tag> },
        { title: '严重度', dataIndex: 'severity', width: 110, render: value => value ? <Tag color={value === 'BLOCK' ? 'error' : value === 'WARNING' ? 'warning' : 'processing'}>{value}</Tag> : '—' },
        { title: 'Candidate', width: 200, render: (_, item) => item.metric_evidence ? `${item.metric_evidence.candidate.value ?? '不可用'} ${item.metric_evidence.unit}` : '结构证据' },
        { title: 'Baseline', width: 200, render: (_, item) => item.metric_evidence?.baseline ? `${item.metric_evidence.baseline.value ?? '不可用'} ${item.metric_evidence.unit}` : 'NO_BASELINE / 不适用' },
        { title: 'Signed / Risk / Relative', width: 220, render: (_, item) => item.metric_evidence ? `${item.metric_evidence.signed_delta ?? '—'} / ${item.metric_evidence.risk_delta ?? '—'} / ${item.metric_evidence.relative_risk ?? '—'}` : '不适用' },
        { title: '原因', dataIndex: 'reason', render: value => value || '—' },
      ]} />
    </Card>
    <Collapse className="section-card" items={review.items.map(item => ({ key: item.id, label: `${item.id} · 展开证据`, children: <pre>{JSON.stringify(item.metric_evidence ?? item.structural_evidence, null, 2)}</pre> }))} />
    {eligible.length > 0 && <Card className="section-card" title="数值 BLOCK 说明"><Alert className="block-alert" type="warning" showIcon title="原始 Gate 仍为 BLOCK" description="此操作只创建新的不可变 Decision Report；发布仍需第二次独立确认。" /><Form form={decisionForm} layout="vertical" onFinish={value => onDecision(eligible, value.reason)}><Form.Item name="reason" label="说明" rules={[{ required: true, min: 8, max: 2000 }]}><Input.TextArea rows={4} /></Form.Item><Button type="primary" htmlType="submit" loading={decisionLoading}>创建 Immutable Decision Report</Button></Form></Card>}
  </div>
}
