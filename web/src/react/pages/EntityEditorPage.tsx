import { useEffect, useMemo, useState } from 'react'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { Alert, Button, Card, Col, Descriptions, Divider, Form, Input, Modal, Row, Space, Table, Tag, Typography, message } from 'antd'
import { ArrowLeftOutlined, CheckCircleOutlined, CodeOutlined, CopyOutlined, ExperimentOutlined, SaveOutlined } from '@ant-design/icons'
import { useNavigate, useParams, useSearchParams } from 'react-router-dom'
import type { components } from '@/api/generated'
import { emptyDraft, rendererMap, validateDraft, type EntityKind, type EntitySchema, type FieldIssue } from '@/forms/registry'
import PageHeader from '../components/PageHeader'
import { ApiError, apiRequest, errorMessage } from '../api'

type Entity = components['schemas']['Entity']
type ValidationRun = components['schemas']['ValidationRun']
type ValidationIssue = components['schemas']['ValidationIssue']
type LocalSummary = components['schemas']['LocalValidationSummary']

interface LoadedEntity { entity: Record<string, unknown>; etag: string }

const allowedCommandFields = ['key', 'name', 'description', 'tag_ids', 'balance_group', 'payload', 'extensions']

export default function EntityEditorPage() {
  const { kind = 'attribute', id = 'new' } = useParams()
  const entityKind = kind as EntityKind
  const navigate = useNavigate()
  const [searchParams, setSearchParams] = useSearchParams()
  const client = useQueryClient()
  const [form] = Form.useForm()
  const [payloadText, setPayloadText] = useState('{}')
  const [dirty, setDirty] = useState(false)
  const [issues, setIssues] = useState<FieldIssue[]>([])
  const [conflict, setConflict] = useState(false)
  const [dailyFailure, setDailyFailure] = useState<{ jobID: string; state: string }>()
  const [unprotectedDate, setUnprotectedDate] = useState('')
  const [localSummary, setLocalSummary] = useState<LocalSummary>()
  const [validationRun, setValidationRun] = useState<ValidationRun>()
  const [messageApi, contextHolder] = message.useMessage()
  const creating = id === 'new'

  const schema = useQuery({ queryKey: ['entity-schema', kind], queryFn: () => apiRequest<EntitySchema>(`/api/v1/schemas/entities/${kind}`) })
  const entity = useQuery({
    queryKey: ['entity', kind, id],
    queryFn: async (): Promise<LoadedEntity> => {
      if (creating) return { entity: emptyDraft(entityKind) as unknown as Record<string, unknown>, etag: '' }
      const response = await fetch(`/api/v1/entities/${kind}/${id}`, { headers: { Accept: 'application/json' } })
      if (!response.ok) throw new Error('无法加载对象')
      return { entity: await response.json() as Record<string, unknown>, etag: response.headers.get('ETag') ?? '' }
    },
  })

  useEffect(() => {
    if (!entity.data) return
    form.setFieldsValue({
      key: entity.data.entity.key ?? '',
      name: entity.data.entity.name ?? '',
      description: entity.data.entity.description ?? '',
      balance_group: entity.data.entity.balance_group ?? '',
    })
    setPayloadText(JSON.stringify(entity.data.entity.payload ?? {}, null, 2))
    setDirty(false)
    setIssues([])
  }, [entity.data, form])

  useEffect(() => {
    const warn = (event: BeforeUnloadEvent) => { if (dirty) { event.preventDefault(); event.returnValue = '' } }
    const closeRequest = (event: Event) => {
      const request = event as CustomEvent<{ resolve: (value: boolean) => void }>
      if (!dirty) { request.detail.resolve(true); return }
      Modal.confirm({
        title: '处理未保存修改',
        content: '切换或关闭项目前，请保存或放弃当前输入。',
        okText: '放弃修改并继续', cancelText: '继续编辑', okButtonProps: { danger: true },
        onOk: () => { setDirty(false); request.detail.resolve(true) },
        onCancel: () => request.detail.resolve(false),
      })
    }
    window.addEventListener('beforeunload', warn)
    window.addEventListener('eco-guardian:request-close', closeRequest)
    return () => { window.removeEventListener('beforeunload', warn); window.removeEventListener('eco-guardian:request-close', closeRequest) }
  }, [dirty])

  useEffect(() => {
    const path = searchParams.get('field_path')
    if (!path) return
    window.setTimeout(() => {
      const field = document.querySelector<HTMLElement>(`[data-field-path="${CSS.escape(path)}"]`)
      field?.focus()
      field?.scrollIntoView({ block: 'center' })
    }, 0)
  }, [searchParams])

  function buildCommand() {
    let payload: unknown
    try { payload = JSON.parse(payloadText) } catch { throw new Error('Payload 不是有效的 JSON，请修正语法后保存。') }
    const values = form.getFieldsValue()
    const source = { ...(entity.data?.entity ?? {}), ...values, payload }
    if (creating) return source
    return Object.fromEntries(allowedCommandFields.filter(field => field in source).map(field => [field, source[field]]))
  }

  const save = useMutation({
    mutationFn: async () => {
      await form.validateFields()
      const command = buildCommand()
      const localIssues = validateDraft(command, entityKind)
      setIssues(localIssues)
      if (localIssues.length) throw new Error('请修正标记的字段后再保存。')
      const response = await fetch(creating ? `/api/v1/entities/${kind}` : `/api/v1/entities/${kind}/${id}`, {
        method: creating ? 'POST' : 'PATCH',
        headers: { Accept: 'application/json', 'Content-Type': 'application/json', ...(creating ? {} : { 'If-Match': entity.data?.etag ?? '' }) },
        body: JSON.stringify(command),
      })
      if (!response.ok) {
        const problem = await response.json().catch(() => null) as { code?: string; title?: string; details?: Record<string, unknown> } | null
        throw new ApiError(problem?.title ?? '保存失败', response.status, problem as never)
      }
      return { body: await response.json() as { entity: Entity; revision: { validation: LocalSummary } }, etag: response.headers.get('ETag') ?? '' }
    },
    onSuccess: result => {
      setDirty(false); setConflict(false); setDailyFailure(undefined); setLocalSummary(result.body.revision.validation)
      client.setQueryData(['entity', kind, result.body.entity.id], { entity: result.body.entity, etag: result.etag })
      messageApi.success('保存成功，已创建新的配置修订')
      if (creating) navigate(`/config/${kind}/${result.body.entity.id}`, { replace: true })
      else void entity.refetch()
    },
    onError: cause => {
      if (cause instanceof ApiError && cause.status === 409) { setConflict(true); return }
      if (cause instanceof ApiError && cause.code === 'DAILY_BACKUP_REQUIRED') {
        const jobID = String(cause.details?.failed_backup_job_id ?? '')
        if (jobID) setDailyFailure({ jobID, state: String(cause.details?.state ?? 'awaiting_waiver') })
      }
      messageApi.error(errorMessage(cause, '保存失败'))
    },
  })

  const validation = useMutation({
    mutationFn: () => apiRequest<ValidationRun>('/api/v1/validation/runs', { method: 'POST', body: JSON.stringify({ source: { type: 'working' }, scope: 'FULL' }) }),
    onSuccess: value => { setValidationRun(value); messageApi.success('FULL 校验已完成') },
    onError: cause => messageApi.error(errorMessage(cause, '校验请求失败')),
  })

  const dailyRetry = useMutation({
    mutationFn: async () => { await apiRequest('/api/v1/backups/daily-retries', { method: 'POST', body: JSON.stringify({ failed_backup_job_id: dailyFailure?.jobID }) }); return save.mutateAsync() },
    onError: cause => messageApi.error(errorMessage(cause, '每日备份重试失败')),
  })
  const dailyWaiver = useMutation({
    mutationFn: async () => {
      const waiver = await apiRequest<{ local_date: string }>('/api/v1/backups/daily-waivers', { method: 'POST', body: JSON.stringify({ failed_backup_job_id: dailyFailure?.jobID, confirmation: 'CONTINUE_WITHOUT_BACKUP_TODAY' }) })
      setUnprotectedDate(waiver.local_date)
      return save.mutateAsync()
    },
    onError: cause => messageApi.error(errorMessage(cause, '无法记录当日无恢复点确认')),
  })

  const severityFilters = useMemo(() => ({ ERROR: 'error', BLOCK: 'error', WARNING: 'warning', INFO: 'processing' } as const), [])

  if (entity.isLoading) return <div className="route-loading"><Typography.Text>正在加载编辑器…</Typography.Text></div>
  if (entity.isError) return <div className="page-container"><Alert type="error" showIcon title={errorMessage(entity.error, '无法加载对象')} action={<Button onClick={() => navigate(`/config/${kind}`)}>返回列表</Button>} /></div>

  return <div className="page-container entity-editor-page">
    {contextHolder}
    <PageHeader
      eyebrow={<><CodeOutlined /> 结构化实体</>}
      title={creating ? `新建 ${kind}` : `${String(entity.data?.entity.name ?? kind)} · 编辑`}
      description={schema.data ? `表单契约：${schema.data.schema_id} · 固定渲染器：${Object.keys(rendererMap).join('、')}` : '正在读取表单契约…'}
      extra={<Space><Button icon={<ArrowLeftOutlined />} onClick={() => navigate(`/config/${kind}`)}>返回列表</Button><Button type="primary" icon={<SaveOutlined />} loading={save.isPending} onClick={() => save.mutate()}>保存</Button></Space>}
    />

    {issues.length > 0 && <Alert className="block-alert" type="error" showIcon title="保存未完成" description={<ul>{issues.map(issue => <li key={`${issue.path}:${issue.message}`}>{issue.path}：{issue.message}</li>)}</ul>} />}
    {conflict && <Alert className="block-alert" type="warning" showIcon title="服务器版本已更新，本地输入仍保留" action={<Space><Button onClick={() => void entity.refetch().then(() => setConflict(false))}>刷新服务器版本</Button><Button icon={<CopyOutlined />} onClick={() => void navigator.clipboard.writeText(JSON.stringify(buildCommand(), null, 2))}>复制本地输入</Button><Button onClick={() => setConflict(false)}>继续编辑</Button></Space>} />}
    {dailyFailure && <Alert className="block-alert" type="warning" showIcon title="今日编辑尚未受备份保护" description={`失败备份任务 ${dailyFailure.jobID}；系统不会自动接受 waiver。`} action={<Space orientation="vertical"><Button loading={dailyRetry.isPending} onClick={() => dailyRetry.mutate()}>重试每日备份并保存</Button><Button danger loading={dailyWaiver.isPending} onClick={() => dailyWaiver.mutate()}>明确继续：今天无恢复点</Button></Space>} />}
    {unprotectedDate && <Alert className="block-alert" type="info" showIcon title={`${unprotectedDate} 已明确选择无恢复点继续编辑`} />}

    <Row gutter={[16, 16]}>
      <Col xs={24} xl={15}>
        <Card className="section-card" title="基础信息">
          <Form form={form} layout="vertical" onValuesChange={() => setDirty(true)}>
            <Row gutter={16}>
              <Col xs={24} md={12}><Form.Item name="key" label="Key" rules={[{ required: true, message: '请输入 Key' }, { pattern: /^[a-z][a-z0-9_]*$/, message: '仅可使用小写字母、数字和下划线' }]}><Input data-field-path="/key" /></Form.Item></Col>
              <Col xs={24} md={12}><Form.Item name="name" label="名称" rules={[{ required: true, message: '请输入名称' }]}><Input data-field-path="/name" /></Form.Item></Col>
            </Row>
            <Form.Item name="description" label="说明"><Input.TextArea rows={3} data-field-path="/description" /></Form.Item>
            <Form.Item name="balance_group" label="平衡分组"><Input /></Form.Item>
          </Form>
          <Divider titlePlacement="start">配置字段（Payload）</Divider>
          <Typography.Paragraph type="secondary">Payload 使用服务端 Schema 约束。JSON 编辑器会完整保留当前版本支持的嵌套结构。</Typography.Paragraph>
          <Input.TextArea className="json-editor" value={payloadText} onChange={event => { setPayloadText(event.target.value); setDirty(true) }} rows={20} spellCheck={false} data-field-path="/payload" aria-label="Payload JSON" />
          <Space className="json-actions"><Button onClick={() => { try { setPayloadText(JSON.stringify(JSON.parse(payloadText), null, 2)) } catch { messageApi.error('Payload JSON 语法无效') } }}>格式化 JSON</Button><Typography.Text type={dirty ? 'warning' : 'secondary'}>{dirty ? '有未保存修改' : '已与服务器同步'}</Typography.Text></Space>
        </Card>
      </Col>
      <Col xs={24} xl={9}>
        <Card className="section-card" title="校验与质量门禁" extra={<Button icon={<ExperimentOutlined />} disabled={creating} loading={validation.isPending} onClick={() => validation.mutate()}>运行 FULL 校验</Button>}>
          {localSummary && <Alert className="block-alert" type={localSummary.error || localSummary.block ? 'warning' : 'success'} showIcon icon={<CheckCircleOutlined />} title="LOCAL 校验摘要" description={`ERROR ${localSummary.error} · BLOCK ${localSummary.block} · WARNING ${localSummary.warning} · INFO ${localSummary.info}`} />}
          {!validationRun ? <Typography.Text type="secondary">尚未运行 FULL 校验。</Typography.Text> : <>
            <Descriptions size="small" bordered column={2}>
              <Descriptions.Item label="范围">{validationRun.scope}</Descriptions.Item>
              <Descriptions.Item label="状态">{dirty ? <Tag color="warning">结果已过期</Tag> : <Tag color="success">当前</Tag>}</Descriptions.Item>
              <Descriptions.Item label="ERROR">{validationRun.summary.error}</Descriptions.Item>
              <Descriptions.Item label="BLOCK">{validationRun.summary.block}</Descriptions.Item>
              <Descriptions.Item label="WARNING">{validationRun.summary.warning}</Descriptions.Item>
              <Descriptions.Item label="INFO">{validationRun.summary.info}</Descriptions.Item>
            </Descriptions>
            <Table<ValidationIssue>
              className="validation-table" rowKey="fingerprint" size="small" pagination={false} dataSource={validationRun.issues}
              columns={[
                { title: '级别', dataIndex: 'severity', width: 92, render: value => <Tag color={severityFilters[value as keyof typeof severityFilters]}>{value}</Tag> },
                { title: '问题', render: (_, issue) => <Button type="link" className="issue-link" onClick={() => issue.entity_id === id ? setSearchParams({ field_path: issue.field_path }) : messageApi.warning('此问题属于其他实体')}>{issue.code}：{issue.message}</Button> },
              ]}
            />
          </>}
        </Card>
        {Boolean(entity.data?.entity.extensions) && Object.keys(entity.data!.entity.extensions as object).length > 0 && <Card className="section-card" title="只读扩展"><Typography.Paragraph type="secondary">此对象包含当前版本不支持的扩展，保存时会原样保留。</Typography.Paragraph><pre>{JSON.stringify(entity.data!.entity.extensions, null, 2)}</pre></Card>}
      </Col>
    </Row>
  </div>
}
