import { useEffect, useMemo, useState } from 'react'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { Alert, Button, Card, Checkbox, Col, Collapse, Descriptions, Form, Input, InputNumber, Modal, Row, Select, Space, Statistic, Switch, Table, Tag, Typography, message, type FormInstance } from 'antd'
import { CloudOutlined, DeleteOutlined, PlusOutlined, RobotOutlined, SafetyCertificateOutlined, SendOutlined } from '@ant-design/icons'
import type { components } from '@/api/generated'
import PageHeader from '../components/PageHeader'
import JobProgress from '../components/JobProgress'
import { apiRequest, errorMessage, newIdempotencyKey, patchJSON, shortID } from '../api'
import { useAppState } from '../context/AppContext'

type EntityKind = components['schemas']['EntityKind']
type Entity = components['schemas']['Entity']
type EntityPage = components['schemas']['EntityPage']
type RevisionPage = components['schemas']['RevisionPage']
type Settings = components['schemas']['SettingsResource']
type SettingsResult = components['schemas']['SettingsUpdateResult']
type Capabilities = components['schemas']['RuntimeCapabilities']
type AIRequest = components['schemas']['CreateAIDesignJobRequest']
type AIAccepted = components['schemas']['AIDesignJobAccepted']
type DraftPatch = components['schemas']['DraftPatchResource']
type DraftTarget = components['schemas']['AIDraftTarget']

const kinds: EntityKind[] = ['attribute', 'tag', 'character', 'skill', 'item', 'effect']
const pathOptions: Record<EntityKind, string[]> = {
  attribute: ['/payload/default', '/payload/min', '/payload/max', '/payload/display_scale'], tag: ['/payload/category', '/payload/parent_tag_ids'],
  character: ['/payload/attribute_values', '/payload/skill_ids', '/payload/item_ids', '/payload/rule_blocks'], skill: ['/payload/cooldown', '/payload/costs', '/payload/effect_ids', '/payload/rule_blocks'],
  item: ['/payload/slot', '/payload/effect_ids', '/payload/attribute_modifiers', '/payload/enhance_tag_ids', '/payload/rule_blocks'], effect: ['/payload/duration', '/payload/modifiers', '/payload/trigger_blocks', '/payload/stack_rule'],
}

export default function AIDesignPage() {
  const { project, refreshRuntime } = useAppState()
  const [form] = Form.useForm()
  const client = useQueryClient()
  const [jobID, setJobID] = useState(sessionStorage.getItem(`ai-design-active-job:${project?.id}`) ?? '')
  const [patchID, setPatchID] = useState('')
  const [messageApi, contextHolder] = message.useMessage()
  const revisions = useQuery({ queryKey: ['revisions', project?.id], queryFn: () => apiRequest<RevisionPage>('/api/v1/revisions') })
  const settings = useQuery({ queryKey: ['settings'], queryFn: () => apiRequest<Settings>('/api/v1/settings') })
  const capabilities = useQuery({ queryKey: ['runtime-capabilities'], queryFn: () => apiRequest<Capabilities>('/api/v1/runtime/capabilities') })
  const catalogs = useQuery({
    queryKey: ['entity-catalogs', project?.id],
    queryFn: async () => Object.fromEntries(await Promise.all(kinds.map(async kind => [kind, (await apiRequest<EntityPage>(`/api/v1/entities/${kind}?limit=200`)).items] as const))) as Record<EntityKind, Entity[]>,
  })

  useEffect(() => {
    if (!revisions.data?.items[0]) return
    form.setFieldsValue({
      base_revision_id: form.getFieldValue('base_revision_id') || revisions.data.items[0].id,
      goal_id: 'balance', metric_id: 'metric-dps', metric_version: 'v1', direction: 'minimize', unit: 'points_per_second',
      scene: 'single-target-30s',
      targets: form.getFieldValue('targets')?.length ? form.getFieldValue('targets') : [{ kind: 'skill', entity_id: '', path: '/payload/cooldown', operations: ['replace'] }],
    })
  }, [form, revisions.data])

  const generate = useMutation({
    mutationFn: (values: Record<string, unknown>) => {
      const constraintID = String(values.constraint_id ?? '').trim()
      let constraintValue: unknown = values.constraint_value
      if (constraintID && typeof values.constraint_value === 'string' && values.constraint_value.trim()) constraintValue = JSON.parse(values.constraint_value)
      const targets = values.targets as Array<{ kind: EntityKind; entity_id: string; path: string; operations: Array<'replace' | 'add' | 'remove'> }>
      const request: AIRequest = {
        base_revision_id: String(values.base_revision_id),
        goals: [{ id: String(values.goal_id).trim(), description: String(values.goal).trim() }],
        metrics: [{ metric_id: String(values.metric_id).trim(), version: String(values.metric_version).trim(), direction: values.direction as 'minimize' | 'maximize' | 'target', ...(values.metric_target ? { target: String(values.metric_target) } : {}), unit: String(values.unit).trim() }],
        constraints: constraintID ? [{ id: constraintID, path: String(values.constraint_path).trim(), operator: values.constraint_operator as components['schemas']['AIConstraint']['operator'], value: constraintValue }] : [],
        scenes: [String(values.scene)],
        allowed_targets: targets.map(target => ({ entity_id: target.entity_id, kind: target.kind, expected_entity_version: catalogs.data?.[target.kind].find(item => item.id === target.entity_id)?.entity_version ?? 0, paths: [{ path: target.path, operations: target.operations }] })),
      }
      return apiRequest<AIAccepted>('/api/v1/ai-design-jobs', { method: 'POST', headers: { 'Idempotency-Key': newIdempotencyKey() }, body: JSON.stringify(request) })
    },
    onSuccess: accepted => {
      setJobID(accepted.job.id)
      if (accepted.job.result_type === 'draft_patch' && accepted.job.result_id) setPatchID(accepted.job.result_id)
      sessionStorage.setItem(`ai-design-active-job:${project?.id}`, accepted.job.id)
      messageApi.success('AI 设计任务已固定输入并进入受限工作流')
    },
    onError: cause => messageApi.error(errorMessage(cause, '无法生成 DraftPatch')),
  })
  const providerSave = useMutation({
    mutationFn: (values: components['schemas']['PatchAIProviderSettings']) => patchJSON<SettingsResult>('/api/v1/settings', { ai: values }),
    onSuccess: result => { client.setQueryData(['settings'], result.settings); void Promise.all([capabilities.refetch(), refreshRuntime()]); messageApi.success('Provider 非敏感设置已保存') },
    onError: cause => messageApi.error(errorMessage(cause, '无法保存 Provider 设置')),
  })
  const credential = useMutation({
    mutationFn: (value: { credential: string }) => apiRequest('/api/v1/settings/credentials/openai-compatible', { method: 'PUT', body: JSON.stringify(value) }),
    onSuccess: () => { messageApi.success('凭据已写入系统凭据管理器，页面不会保留其内容'); void Promise.all([settings.refetch(), capabilities.refetch()]) },
    onError: cause => messageApi.error(errorMessage(cause, '无法写入 Provider 凭据')),
  })
  const clearCredential = useMutation({
    mutationFn: () => apiRequest('/api/v1/settings/credentials/openai-compatible', { method: 'DELETE' }),
    onSuccess: () => { messageApi.success('持久化凭据已清除'); void Promise.all([settings.refetch(), capabilities.refetch()]) },
    onError: cause => messageApi.error(errorMessage(cause, '无法清除凭据')),
  })

  const capability = capabilities.data?.ai
  const canGenerate = capability?.state === 'available' || capability?.state === 'degraded'

  return <div className="page-container ai-page">
    {contextHolder}
    <PageHeader title="AI 平衡设计" eyebrow={<><RobotOutlined /> 有界智能辅助</>} description="AI 只能生成可审阅 DraftPatch；没有 Repository、发布或 Graph Activation 权限。" />
    <ProviderPanel settings={settings.data} capability={capability} onSave={values => providerSave.mutate(values)} saving={providerSave.isPending} onCredential={value => credential.mutate({ credential: value })} credentialSaving={credential.isPending} onClear={() => clearCredential.mutate()} clearSaving={clearCredential.isPending} onTest={() => void capabilities.refetch()} />

    <Card className="section-card" title="冻结输入与允许范围">
      {!revisions.isLoading && !revisions.data?.items.length && <Alert className="block-alert" type="error" showIcon title="没有可选择的不可变 Base Revision" />}
      {capability && !canGenerate && <Alert className="block-alert" type="warning" showIcon title={`Provider 状态：${capability.state}`} description={capability.reasons.join('；')} />}
      <Form form={form} layout="vertical" onFinish={values => generate.mutate(values)}>
        <Form.Item name="base_revision_id" label="Base Revision" rules={[{ required: true }]}><Select options={revisions.data?.items.map(item => ({ value: item.id, label: `#${item.display_revision} · ${shortID(item.id)}` }))} /></Form.Item>
        <Row gutter={16}><Col xs={24} md={8}><Form.Item name="goal_id" label="目标 ID" rules={[{ required: true }]}><Input maxLength={128} /></Form.Item></Col><Col xs={24} md={16}><Form.Item name="goal" label="目标说明" rules={[{ required: true, min: 4 }]}><Input.TextArea rows={3} maxLength={4000} showCount /></Form.Item></Col></Row>
        <Card size="small" className="nested-form-card" title="Metric">
          <Row gutter={12}>
            <Col xs={24} md={6}><Form.Item name="metric_id" label="ID" rules={[{ required: true }]}><Input /></Form.Item></Col>
            <Col xs={12} md={4}><Form.Item name="metric_version" label="Version"><Input /></Form.Item></Col>
            <Col xs={12} md={5}><Form.Item name="direction" label="方向"><Select options={['minimize', 'maximize', 'target'].map(value => ({ value }))} /></Form.Item></Col>
            <Col xs={12} md={4}><Form.Item name="metric_target" label="Target"><Input /></Form.Item></Col>
            <Col xs={12} md={5}><Form.Item name="unit" label="Unit"><Input /></Form.Item></Col>
          </Row>
        </Card>
        <Collapse className="nested-form-card" items={[{ key: 'constraint', label: 'Constraint（可选 Typed JSON）', children: <Row gutter={12}>
          <Col xs={24} md={5}><Form.Item name="constraint_id" label="ID"><Input /></Form.Item></Col>
          <Col xs={24} md={7}><Form.Item name="constraint_path" label="JSON Pointer"><Input placeholder="/payload/cooldown" /></Form.Item></Col>
          <Col xs={24} md={5}><Form.Item name="constraint_operator" label="Operator" initialValue="equal"><Select options={['equal', 'not_equal', 'less', 'less_or_equal', 'greater', 'greater_or_equal', 'in', 'range'].map(value => ({ value }))} /></Form.Item></Col>
          <Col xs={24} md={7}><Form.Item name="constraint_value" label="JSON Value"><Input placeholder='"10"' /></Form.Item></Col>
        </Row> }]} />
        <Form.Item name="scene" label="固定 Scene"><Select options={[{ value: 'single-target-30s' }, { value: 'multi-target-60s' }]} /></Form.Item>
        <Form.List name="targets">
          {(fields, { add, remove }) => <Card size="small" className="nested-form-card" title="允许目标、字段与操作" extra={<Button type="dashed" icon={<PlusOutlined />} onClick={() => add({ kind: 'skill', entity_id: '', path: '/payload/cooldown', operations: ['replace'] })}>添加目标</Button>}>
            {fields.map((field, index) => <AllowedTargetRow key={field.key} field={field} index={index} form={form} catalogs={catalogs.data} canRemove={fields.length > 1} onRemove={() => remove(field.name)} />)}
          </Card>}
        </Form.List>
        {capability && <Alert className="block-alert" type="info" showIcon title="服务端固定预算" description={`最多 ${capability.limits.max_format_repairs} 轮 Repair、${capability.limits.max_tool_calls} 次工具调用、${capability.limits.max_search_candidates} 个搜索候选；客户端不能扩大。`} />}
        <Button type="primary" size="large" htmlType="submit" icon={<SendOutlined />} loading={generate.isPending} disabled={!canGenerate}>生成 DraftPatch 候选</Button>
      </Form>
    </Card>
    {jobID && <JobProgress jobId={jobID} title="AI 设计任务" onDone={job => { if (job.status === 'succeeded' && job.result_id) { setPatchID(job.result_id); sessionStorage.removeItem(`ai-design-active-job:${project?.id}`) } }} />}
    {patchID ? <DraftPatchReview patchID={patchID} /> : !jobID && <Card className="section-card empty-result-card"><Typography.Text type="secondary">尚无候选。配置 Provider、选择冻结输入和允许范围后再生成。</Typography.Text></Card>}
  </div>
}

function AllowedTargetRow({ field, index, form, catalogs, canRemove, onRemove }: { field: { key: number; name: number; fieldKey?: number }; index: number; form: FormInstance; catalogs?: Record<EntityKind, Entity[]>; canRemove: boolean; onRemove: () => void }) {
  const kind = (Form.useWatch(['targets', index, 'kind'], form) ?? 'skill') as EntityKind
  return <Row gutter={12} align="bottom" className="target-row">
    <Col xs={24} md={4}><Form.Item {...field} name={[field.name, 'kind']} label="Kind" rules={[{ required: true }]}><Select options={kinds.map(value => ({ value }))} onChange={value => { form.setFieldValue(['targets', index, 'entity_id'], undefined); form.setFieldValue(['targets', index, 'path'], pathOptions[value as EntityKind][0]) }} /></Form.Item></Col>
    <Col xs={24} md={7}><Form.Item {...field} name={[field.name, 'entity_id']} label="Stable Entity" rules={[{ required: true }]}><Select showSearch optionFilterProp="label" options={catalogs?.[kind].map(item => ({ value: item.id, label: `${item.name}（${item.key}，v${item.entity_version}）` }))} /></Form.Item></Col>
    <Col xs={24} md={6}><Form.Item {...field} name={[field.name, 'path']} label="允许字段" rules={[{ required: true }]}><Select options={pathOptions[kind].map(value => ({ value }))} /></Form.Item></Col>
    <Col xs={20} md={6}><Form.Item {...field} name={[field.name, 'operations']} label="操作" rules={[{ required: true, type: 'array', min: 1 }]}><Checkbox.Group options={['replace', 'add', 'remove']} /></Form.Item></Col>
    <Col xs={4} md={1}><Button danger type="text" aria-label={`移除目标 ${index + 1}`} icon={<DeleteOutlined />} disabled={!canRemove} onClick={onRemove} /></Col>
  </Row>
}

function ProviderPanel({ settings, capability, onSave, saving, onCredential, credentialSaving, onClear, clearSaving, onTest }: { settings?: Settings; capability?: Capabilities['ai']; onSave: (value: components['schemas']['PatchAIProviderSettings']) => void; saving: boolean; onCredential: (value: string) => void; credentialSaving: boolean; onClear: () => void; clearSaving: boolean; onTest: () => void }) {
  const [providerForm] = Form.useForm()
  const [credentialForm] = Form.useForm()
  const allowCloud = Form.useWatch('allow_cloud', providerForm)
  useEffect(() => { if (settings) providerForm.setFieldsValue(settings.ai) }, [providerForm, settings])
  return <Card className="section-card provider-card" title={<Space><CloudOutlined />Provider 设置与就绪状态</Space>} extra={capability && <Tag color={capability.state === 'available' ? 'success' : capability.state === 'degraded' ? 'warning' : 'error'}>{capability.state}</Tag>}>
    <Row gutter={[24, 16]}>
      <Col xs={24} xl={15}><Form form={providerForm} layout="vertical" onFinish={onSave}>
        <Row gutter={12}><Col xs={24} md={5}><Form.Item name="enabled" label="启用 AI 设计" valuePropName="checked"><Switch /></Form.Item></Col><Col xs={24} md={11}><Form.Item name="endpoint" label="OpenAI-compatible Endpoint" rules={[{ type: 'url', warningOnly: true }]}><Input placeholder="http://127.0.0.1:11434/v1" /></Form.Item></Col><Col xs={24} md={8}><Form.Item name="model" label="Model"><Input autoComplete="off" /></Form.Item></Col></Row>
        <Row gutter={12}><Col xs={24} md={8}><Form.Item name="request_timeout_seconds" label="请求超时（秒）"><InputNumber min={1} max={600} style={{ width: '100%' }} /></Form.Item></Col><Col xs={24} md={8}><Form.Item name="allow_cloud" label="允许 Cloud Endpoint" valuePropName="checked"><Switch /></Form.Item></Col></Row>
        {allowCloud && <Alert className="block-alert" type="warning" showIcon title="Cloud 数据披露" description="冻结目标、目标/约束及有界证据 Citation 会发送到配置的 HTTPS Provider；凭据、完整项目数据库和隐藏推理不会发送。" />}
        <Button htmlType="submit" loading={saving}>保存非敏感设置</Button>
      </Form></Col>
      <Col xs={24} xl={9}><Form form={credentialForm} layout="vertical" onFinish={value => { onCredential(value.credential); credentialForm.resetFields() }}><Form.Item name="credential" label="新凭据（仅写入）" rules={[{ required: true }]}><Input.Password autoComplete="new-password" /></Form.Item><Space wrap><Button type="primary" htmlType="submit" loading={credentialSaving}>设置/替换凭据</Button><Button danger disabled={!settings?.ai.credential_present} loading={clearSaving} onClick={onClear}>清除持久化凭据</Button></Space></Form>
        {capability && <Descriptions size="small" column={1} className="provider-capabilities"><Descriptions.Item label="Structured Output">{capability.structured_output ? '支持' : '不支持'}</Descriptions.Item><Descriptions.Item label="Tool Calls">{capability.tool_calls ? '支持' : '不支持'}</Descriptions.Item><Descriptions.Item label="Streaming">{capability.streaming ? '支持' : '不支持'}</Descriptions.Item></Descriptions>}
        <Button type="link" onClick={onTest}>测试 Provider 能力</Button>
      </Col>
    </Row>
  </Card>
}

function DraftPatchReview({ patchID }: { patchID: string }) {
  const client = useQueryClient()
  const [discardReason, setDiscardReason] = useState('')
  const [messageApi, contextHolder] = message.useMessage()
  const patch = useQuery({ queryKey: ['draft-patch', patchID], queryFn: () => apiRequest<DraftPatch>(`/api/v1/draft-patches/${encodeURIComponent(patchID)}`) })
  const decision = useMutation({
    mutationFn: async (kind: 'accept' | 'discard') => {
      const value = patch.data!
      const body = kind === 'accept'
        ? { patch_hash: value.patch_hash, base_revision_id: value.base_revision_id, targets: value.targets.map(target => ({ entity_id: target.entity_id, expected_entity_version: target.expected_entity_version })) }
        : { patch_hash: value.patch_hash, ...(discardReason.trim() ? { reason: discardReason.trim() } : {}) }
      return apiRequest(value.links[kind], { method: 'POST', headers: { 'Idempotency-Key': newIdempotencyKey() }, body: JSON.stringify(body) })
    },
    onSuccess: (_, kind) => { messageApi.success(kind === 'accept' ? '候选已接受并创建新 Revision；尚未发布' : '候选已放弃'); void client.invalidateQueries({ queryKey: ['draft-patch', patchID] }); void client.invalidateQueries({ queryKey: ['revisions'] }) },
    onError: cause => messageApi.error(errorMessage(cause, '决策未完成，Patch 与表单内容均未清除')),
  })
  if (patch.isLoading) return <Card className="section-card" loading />
  if (patch.isError) return <Alert type="error" showIcon title={errorMessage(patch.error, '无法读取 DraftPatch')} />
  const value = patch.data!
  const canAccept = !value.decision && value.freshness.state === 'fresh' && Boolean(value.preview?.acceptable && value.preview.evidence.length)
  return <Card className="section-card patch-review-card" title={<Space><SafetyCertificateOutlined />DraftPatch 审阅</Space>} extra={<Space><Tag>{value.freshness.state}</Tag><Tag color={value.preview?.acceptable ? 'success' : 'error'}>{value.preview?.acceptable ? 'Preview Acceptable' : 'Preview Blocked'}</Tag></Space>}>
    {contextHolder}
    <Descriptions size="small" column={{ xs: 1, md: 3 }}>
      <Descriptions.Item label="Patch"><Typography.Text copyable>{shortID(value.id)}</Typography.Text></Descriptions.Item>
      <Descriptions.Item label="Hash"><Typography.Text copyable code>{shortID(value.patch_hash)}</Typography.Text></Descriptions.Item>
      <Descriptions.Item label="Base Revision">{shortID(value.base_revision_id)}</Descriptions.Item>
      <Descriptions.Item label="状态">{value.decision?.kind ?? 'pending'}</Descriptions.Item>
      <Descriptions.Item label="创建时间">{new Date(value.created_at).toLocaleString()}</Descriptions.Item>
      <Descriptions.Item label="冲突目标">{value.freshness.conflicting_targets.length}</Descriptions.Item>
    </Descriptions>
    {value.freshness.state === 'stale' && <Alert className="block-alert" type="warning" showIcon title="候选已 Stale，接受已禁用" description={`冲突目标：${value.freshness.conflicting_targets.join('、') || '工作状态已变化'}`} />}
    <Typography.Title level={4}>多实体规范 Patch</Typography.Title>
    {value.targets.map(target => <DraftTargetTable key={target.entity_id} target={target} />)}
    <Row gutter={[16, 16]}>
      <Col xs={24} xl={12}><Card size="small" title="事实证据与 Advisory Preview"><Alert className="block-alert" type={value.preview?.acceptable ? 'success' : 'error'} showIcon title={value.preview ? `Advisory · acceptable=${value.preview.acceptable}` : '没有完整 Preview'} description={value.preview?.issues.join('；')} />{value.preview?.evidence.map(evidence => <Card key={evidence.id} size="small" className="evidence-item" title={`${evidence.kind} · ${evidence.id}`}><Typography.Paragraph>{evidence.citation || '无 Citation'}</Typography.Paragraph><Tag>{evidence.mode || 'n/a'}</Tag>{evidence.degraded && <Tag color="warning">degraded</Tag>}</Card>)}</Card></Col>
      <Col xs={24} xl={12}><Card size="small" title="AI 生成说明（不是事实证据）"><Typography.Paragraph><strong>Rationale：</strong>{value.rationale}</Typography.Paragraph><ul>{value.assumptions.map(item => <li key={item}>{item}</li>)}</ul><Statistic title="Attempt 数" value={value.attempts.length} /></Card></Col>
    </Row>
    <Card size="small" className="decision-card" title="服务器权威决策"><Typography.Paragraph>接受会原子创建一个新 Revision，但不会发布；发布仍需在版本页独立确认。</Typography.Paragraph>{value.decision ? <Alert type="success" showIcon title={`已${value.decision.kind}`} description={`${value.decision.actor} · ${new Date(value.decision.decided_at).toLocaleString()}${value.decision.accepted_revision_id ? ` · revision ${value.decision.accepted_revision_id}` : ''}`} /> : <Space wrap><Button type="primary" disabled={!canAccept} loading={decision.isPending && decision.variables === 'accept'} onClick={() => Modal.confirm({ title: '接受 DraftPatch 并创建 Revision？', content: '此操作不会发布版本。', onOk: () => decision.mutateAsync('accept') })}>接受并创建 Revision（不发布）</Button><Input value={discardReason} onChange={event => setDiscardReason(event.target.value)} placeholder="放弃原因（可选）" style={{ width: 300 }} maxLength={1000} /><Button danger loading={decision.isPending && decision.variables === 'discard'} onClick={() => decision.mutate('discard')}>放弃候选</Button></Space>}</Card>
  </Card>
}

function DraftTargetTable({ target }: { target: DraftTarget }) {
  return <Card size="small" className="draft-target-card" title={`${target.kind} · ${shortID(target.entity_id)}`} extra={<Tag>entity v{target.expected_entity_version}</Tag>}><Table rowKey="ordinal" size="small" pagination={false} dataSource={target.operations} columns={[
    { title: '序号 / 操作', width: 130, render: (_, operation) => <Space><span>#{operation.ordinal}</span><Tag color="processing">{operation.kind}</Tag></Space> },
    { title: '字段路径', dataIndex: 'path', render: path => <Typography.Text code>{path}</Typography.Text> },
    { title: '规范候选值', dataIndex: 'value', render: value => <pre>{JSON.stringify(value, null, 2) ?? '—'}</pre> },
    { title: '证据 ID', dataIndex: 'evidence', render: evidence => evidence.map((id: string) => <Tag key={id}>{id}</Tag>) },
  ]} /></Card>
}
