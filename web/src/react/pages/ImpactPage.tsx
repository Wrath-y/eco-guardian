import { useEffect, useMemo, useState } from 'react'
import { useMutation, useQuery } from '@tanstack/react-query'
import { Alert, Button, Card, Col, Collapse, Descriptions, Flex, Form, Input, InputNumber, Radio, Row, Select, Space, Statistic, Switch, Table, Tag, Timeline, Typography, message } from 'antd'
import { ApartmentOutlined, BranchesOutlined, BulbOutlined, FileSearchOutlined, NodeIndexOutlined, PlayCircleOutlined } from '@ant-design/icons'
import { useSearchParams } from 'react-router-dom'
import type { components } from '@/api/generated'
import PageHeader from '../components/PageHeader'
import JobProgress from '../components/JobProgress'
import { apiRequest, errorMessage, newIdempotencyKey, shortID } from '../api'
import { useAppState } from '../context/AppContext'

type RevisionPage = components['schemas']['RevisionPage']
type ImpactRequest = components['schemas']['CreateImpactAnalysisRequest']
type ImpactAccepted = components['schemas']['ImpactJobAccepted']
type ImpactReport = components['schemas']['ImpactAnalysisReport']
type Affected = components['schemas']['ImpactAffectedEntity']
type ImpactPath = components['schemas']['ImpactPath']
type Expansion = components['schemas']['ImpactPathExpansion']
type Explanation = components['schemas']['ImpactExplanation']

const suspectedStateLabel: Record<string, string> = { disabled: '未启用', ready: '可用', degraded: '降级', rebuild_required: '需要重建索引', unavailable: '检索不可用', canceled: '已取消' }

export default function ImpactPage() {
  const { project } = useAppState()
  const [form] = Form.useForm()
  const [params, setParams] = useSearchParams()
  const [jobID, setJobID] = useState(sessionStorage.getItem(`impact-active-job:${project?.id}`) ?? '')
  const [reportInput, setReportInput] = useState(params.get('report') ?? '')
  const [selectedTarget, setSelectedTarget] = useState('')
  const [selectedPath, setSelectedPath] = useState<ImpactPath>()
  const [expansion, setExpansion] = useState<Expansion>()
  const [explanation, setExplanation] = useState<Explanation>()
  const [browseType, setBrowseType] = useState('')
  const [browseDepth, setBrowseDepth] = useState(6)
  const [messageApi, contextHolder] = message.useMessage()
  const reportID = params.get('report') ?? ''
  const revisions = useQuery({ queryKey: ['revisions', project?.id], queryFn: () => apiRequest<RevisionPage>('/api/v1/revisions'), enabled: Boolean(project) })
  const report = useQuery({ queryKey: ['impact-report', reportID], queryFn: () => apiRequest<ImpactReport>(`/api/v1/impact-analyses/${encodeURIComponent(reportID)}`), enabled: Boolean(reportID) })

  useEffect(() => {
    if (!revisions.data?.items.length) return
    const activeBaseline = revisions.data.items.find(item => item.status.includes('active_release'))?.id
    form.setFieldsValue({ base_revision_id: form.getFieldValue('base_revision_id') || activeBaseline, target_revision_id: form.getFieldValue('target_revision_id') || revisions.data.items[0].id })
  }, [form, revisions.data])
  useEffect(() => {
    const first = report.data?.deterministic_affected[0]
    if (first) { setSelectedTarget(first.node.id); setSelectedPath(first.default_path) }
    else { setSelectedTarget(''); setSelectedPath(undefined) }
    setExpansion(undefined); setExplanation(undefined)
  }, [report.data])

  const create = useMutation({
    mutationFn: (values: { base_revision_id: string; target_revision_id: string; direction: 'incoming' | 'outgoing' | 'both'; max_depth: number; max_nodes: number; suspected: boolean }) => {
      const request: ImpactRequest = {
        project_uuid: project!.id,
        base_revision_id: values.base_revision_id,
        target_revision_id: values.target_revision_id,
        filters: { relationship_kinds: ['explicit'], direction: values.direction, node_types: [], edge_types: [] },
        limits: { max_depth: values.max_depth, max_nodes: values.max_nodes, default_paths_per_target: 1, expanded_max_paths: 20 },
        suspected: { enabled: values.suspected, max_seeds: 20, max_results: 20, graph_max_depth: 2 },
      }
      return apiRequest<ImpactAccepted>('/api/v1/impact-analyses', { method: 'POST', headers: { 'Idempotency-Key': newIdempotencyKey() }, body: JSON.stringify(request) })
    },
    onSuccess: accepted => {
      if (accepted.result_id) openReport(accepted.result_id)
      else { setJobID(accepted.job.id); sessionStorage.setItem(`impact-active-job:${project?.id}`, accepted.job.id) }
      messageApi.success(accepted.cache_hit ? '命中精确缓存报告' : '影响分析任务已提交')
    },
    onError: cause => messageApi.error(errorMessage(cause, '无法创建影响分析')),
  })
  const expand = useMutation({
    mutationFn: () => apiRequest<Expansion>(`/api/v1/impact-analyses/${reportID}/path-expansions`, { method: 'POST', headers: { 'Idempotency-Key': `impact-expand:${reportID}:${selectedTarget}:20` }, body: JSON.stringify({ target_node_id: selectedTarget, max_paths: 20 }) }),
    onSuccess: value => { setExpansion(value); setSelectedPath(value.paths[0] ?? selectedPath) },
    onError: cause => messageApi.error(errorMessage(cause, '无法扩展证据路径')),
  })
  const explain = useMutation({
    mutationFn: (evidenceRef: string) => apiRequest<Explanation>(`/api/v1/impact-analyses/${reportID}/explanations`, { method: 'POST', headers: { 'Idempotency-Key': `impact-explain:${reportID}:${evidenceRef}` }, body: JSON.stringify({ evidence_refs: [evidenceRef] }) }),
    onSuccess: setExplanation,
    onError: cause => messageApi.error(errorMessage(cause, 'AI 解释不可用')),
  })

  function openReport(id: string) { setReportInput(id); setParams({ report: id }); setJobID(''); sessionStorage.removeItem(`impact-active-job:${project?.id}`) }
  function selectAffected(item: Affected) { setSelectedTarget(item.node.id); setSelectedPath(item.default_path); setExpansion(undefined); setExplanation(undefined) }

  const affectedTypes = useMemo(() => [...new Set(report.data?.deterministic_affected.map(item => item.node.type) ?? [])].sort(), [report.data])
  const visibleAffected = useMemo(() => report.data?.deterministic_affected.filter(item => (!browseType || item.node.type === browseType) && item.minimum_depth <= browseDepth) ?? [], [browseDepth, browseType, report.data])
  const selected = report.data?.deterministic_affected.find(item => item.node.id === selectedTarget)

  return <div className="page-container impact-page">
    {contextHolder}
    <PageHeader title="依赖影响分析" eyebrow={<><ApartmentOutlined /> 图谱证据</>} description="从固定 Target Graph Snapshot 计算确定性 Changed / Affected；相似检索与 AI 解释独立呈现。" />
    <Card className="section-card" title="Revision 对与分析边界">
      <Form form={form} layout="vertical" initialValues={{ direction: 'incoming', max_depth: 3, max_nodes: 500, suspected: false }} onFinish={values => create.mutate(values)}>
        <Row gutter={16}>
          <Col xs={24} md={12}><Form.Item name="base_revision_id" label="Base Revision" rules={[{ required: true }]}><Select showSearch optionFilterProp="label" options={revisions.data?.items.map(item => ({ value: item.id, label: `#${item.display_revision} · ${shortID(item.id)}` }))} /></Form.Item></Col>
          <Col xs={24} md={12}><Form.Item name="target_revision_id" label="Target Revision" dependencies={['base_revision_id']} rules={[{ required: true }, ({ getFieldValue }) => ({ validator: (_, value) => value && value === getFieldValue('base_revision_id') ? Promise.reject(new Error('Base 与 Target 必须不同')) : Promise.resolve() })]}><Select showSearch optionFilterProp="label" options={revisions.data?.items.map(item => ({ value: item.id, label: `#${item.display_revision} · ${shortID(item.id)}` }))} /></Form.Item></Col>
        </Row>
        <Row gutter={16} align="bottom">
          <Col xs={24} md={10}><Form.Item name="direction" label="方向"><Radio.Group optionType="button" buttonStyle="solid" options={[{ value: 'incoming', label: '反向依赖' }, { value: 'outgoing', label: '向外探索' }, { value: 'both', label: '双向探索' }]} /></Form.Item></Col>
          <Col xs={12} md={4}><Form.Item name="max_depth" label="最大深度"><InputNumber min={1} max={6} style={{ width: '100%' }} /></Form.Item></Col>
          <Col xs={12} md={4}><Form.Item name="max_nodes" label="最大节点"><InputNumber min={1} max={500} style={{ width: '100%' }} /></Form.Item></Col>
          <Col xs={24} md={6}><Form.Item name="suspected" label="Suspected 检索" valuePropName="checked"><Switch checkedChildren="启用" unCheckedChildren="关闭" /></Form.Item></Col>
        </Row>
        <Button type="primary" size="large" icon={<PlayCircleOutlined />} htmlType="submit" loading={create.isPending}>创建 Impact 任务</Button>
      </Form>
    </Card>
    {jobID && <JobProgress jobId={jobID} title="影响分析任务" onDone={job => { if (job.status === 'succeeded' && job.result_id) openReport(job.result_id) }} />}

    <Card className="section-card compact-history-card" title="读取不可变历史报告"><Space.Compact block><Input value={reportInput} onChange={event => setReportInput(event.target.value)} placeholder="Report ID" aria-label="Report ID" /><Button type="primary" onClick={() => reportInput.trim() && openReport(reportInput.trim())}>读取</Button></Space.Compact></Card>
    {report.isError && <Alert className="block-alert" type="error" showIcon title={errorMessage(report.error, '无法读取影响分析报告')} />}

    {report.data && <>
      <Card className="section-card report-summary-card">
        <Row gutter={[16, 16]}>
          <Col xs={12} lg={6}><Statistic title="Changed" value={report.data.changed_entities.length} prefix={<BranchesOutlined />} /></Col>
          <Col xs={12} lg={6}><Statistic title="Deterministic Affected" value={report.data.deterministic_affected.length} prefix={<NodeIndexOutlined />} /></Col>
          <Col xs={12} lg={6}><Statistic title="Suspected" value={report.data.suspected_associations.length} prefix={<FileSearchOutlined />} /></Col>
          <Col xs={12} lg={6}><Statistic title="Freshness" value={report.data.freshness.fresh ? '当前' : '陈旧'} valueStyle={{ color: report.data.freshness.fresh ? '#16a34a' : '#d97706' }} /></Col>
        </Row>
        <Descriptions size="small" column={{ xs: 1, md: 3 }} className="report-identities">
          <Descriptions.Item label="Report"><Typography.Text copyable>{shortID(report.data.id)}</Typography.Text></Descriptions.Item>
          <Descriptions.Item label="Base">{shortID(report.data.base.revision_id)}</Descriptions.Item>
          <Descriptions.Item label="Target">{shortID(report.data.target.revision_id)}</Descriptions.Item>
        </Descriptions>
        {!report.data.freshness.fresh && <Alert type="warning" showIcon title="历史结果已陈旧" description={report.data.freshness.reasons.join('；')} />}
        {report.data.truncated && <Alert type="info" showIcon title="确定性前缀已截断" description={`${report.data.truncation_reasons.join('、')}；这不表示没有更多影响。`} />}
      </Card>
      <Row gutter={[16, 16]}>
        <Col xs={24} xl={9}><Card className="section-card" title={`Changed（${report.data.changed_entities.length}）`}>{report.data.changed_entities.length ? <Flex vertical className="impact-changed-list">{report.data.changed_entities.map(item => <div className="impact-changed-row" key={item.entity_id}><Space><Tag color="processing">{item.change_kind}</Tag><Typography.Text strong>{item.kind}</Typography.Text></Space><Typography.Text copyable>{shortID(item.entity_id)}</Typography.Text><div>{item.field_paths.map(path => <Tag key={path}>{path}</Tag>)}</div></div>)}</Flex> : <EmptyState text="配置内容相同，这是有效空报告" />}</Card></Col>
        <Col xs={24} xl={15}><Card className="section-card" title={`确定性 Affected（${report.data.deterministic_affected.length}）`} extra={<Space><Select allowClear placeholder="全部类型" value={browseType || undefined} onChange={value => setBrowseType(value ?? '')} options={affectedTypes.map(value => ({ value }))} style={{ width: 140 }} /><InputNumber min={1} max={6} value={browseDepth} onChange={value => setBrowseDepth(value ?? 6)} addonBefore="深度≤" /></Space>}>
          <Table<Affected> rowKey={item => item.node.id} size="small" pagination={{ pageSize: 8 }} dataSource={visibleAffected} rowClassName={item => item.node.id === selectedTarget ? 'selected-table-row' : ''} onRow={item => ({ onClick: () => selectAffected(item) })} columns={[
            { title: '对象', render: (_, item) => <><Typography.Text strong>{item.node.label || item.node.id}</Typography.Text><br /><Typography.Text type="secondary">{item.node.type}</Typography.Text></> },
            { title: '深度', dataIndex: 'minimum_depth', width: 80 },
            { title: '影响', width: 140, render: (_, item) => <Space wrap><Tag color={item.direct ? 'error' : 'warning'}>{item.direct ? '直接影响' : '间接影响'}</Tag>{item.tag_rule && <Tag>标签规则</Tag>}</Space> },
          ]} />
        </Card></Col>
      </Row>

      {selected && <Card className="section-card evidence-card" title={`证据路径 · ${selected.node.label || selected.node.id}`} extra={<Space><Button loading={expand.isPending} onClick={() => expand.mutate()}>展开其他路径</Button><Button type="primary" icon={<BulbOutlined />} loading={explain.isPending} onClick={() => explain.mutate(selected.evidence_ref)}>AI 解释</Button></Space>}>
        {expansion?.paths.length ? <Select value={selectedPath ? expansion.paths.indexOf(selectedPath) : 0} onChange={index => setSelectedPath(expansion.paths[index])} options={expansion.paths.map((_, index) => ({ value: index, label: `路径 ${index + 1}` }))} style={{ width: 160, marginBottom: 16 }} /> : null}
        {selectedPath ? <Timeline items={selectedPath.nodes.map((node, index) => ({ color: index === 0 ? 'blue' : index === selectedPath.nodes.length - 1 ? 'red' : 'gray', children: <><Typography.Text strong>{node.label || node.id}</Typography.Text> <Tag>{node.type}</Tag>{selectedPath.edges[index] && <Typography.Paragraph type="secondary">↓ {selectedPath.edges[index].type} · {selectedPath.edges[index].relation_kind} · confidence {selectedPath.edges[index].confidence}</Typography.Paragraph>}</> }))} /> : <Alert type="info" showIcon title="该对象没有默认证据路径" />}
        {explanation && <Alert type={explanation.status === 'succeeded' ? 'success' : explanation.status === 'failed' ? 'error' : 'info'} showIcon title="AI generated 解释" description={explanation.text || explanation.diagnostic || `状态：${explanation.status}`} />}
      </Card>}

      <Collapse className="section-card" items={[{ key: 'suspected', label: `Suspected 关联（${report.data.suspected_associations.length}）· ${suspectedStateLabel[report.data.suspected_state]}`, children: <><Alert type="info" showIcon title="此区不属于 deterministic affected，不参与 Gate 或发布阻塞。" /><Flex vertical className="suspected-list">{report.data.suspected_associations.map(item => <div className="suspected-row" key={item.evidence_ref}><Typography.Text strong>#{item.rank} {item.node.label}</Typography.Text><Typography.Paragraph>{item.citation_text}</Typography.Paragraph><Typography.Text type="secondary">algorithm {item.algorithm_version} · {item.relationship_kinds.join('、')} · evidence {shortID(item.evidence_ref)}</Typography.Text></div>)}</Flex></> }]} />
    </>}
  </div>
}

function EmptyState({ text }: { text: string }) {
  return <div className="inline-empty"><Typography.Text type="secondary">{text}</Typography.Text></div>
}
