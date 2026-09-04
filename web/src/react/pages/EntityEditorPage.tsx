import { useEffect, useMemo, useState } from 'react'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { Alert, AutoComplete, Button, Card, Col, Descriptions, Divider, Form, Input, Row, Select, Space, Table, Tag, Typography, message } from 'antd'
import { ArrowLeftOutlined, CheckCircleOutlined, CodeOutlined, CopyOutlined, DeleteOutlined, DownOutlined, ExperimentOutlined, QuestionCircleOutlined, SaveOutlined } from '@ant-design/icons'
import { useNavigate, useParams, useSearchParams } from 'react-router-dom'
import type { components } from '@/api/generated'
import { emptyDraft, entityKindLabels, preparePayload, validateDraft, type EntityKind, type EntitySchema, type FieldIssue } from '@/forms/registry'
import PageHeader from '../components/PageHeader'
import PayloadForm, { type EntityCatalogs, type EntityOption } from '../components/PayloadForm'
import { ApiError, apiRequest, errorMessage } from '../api'
import { useAppState } from '../context/AppContext'

type Entity = components['schemas']['Entity']
type ValidationRun = components['schemas']['ValidationRun']
type ValidationIssue = components['schemas']['ValidationIssue']
type LocalSummary = components['schemas']['LocalValidationSummary']

interface LoadedEntity { entity: Record<string, unknown>; etag: string }
interface EntityPage { items: EntityOption[]; next_cursor: string | null }

const allowedCommandFields = ['key', 'name', 'description', 'tag_ids', 'balance_group', 'payload', 'extensions']
const catalogKinds: EntityKind[] = ['attribute', 'tag', 'character', 'skill', 'item', 'effect']
const basicExamples: Record<EntityKind, { key: string; name: string; description: string; balanceGroup: string; tags: string }> = {
  attribute: { key: 'attack_power', name: '攻击力', description: '角色造成伤害时使用的基础攻击数值。', balanceGroup: 'combat_core', tags: '战斗、核心属性' },
  tag: { key: 'fire', name: '火焰', description: '标记火焰相关技能、物品与效果。', balanceGroup: 'elements', tags: '元素分类' },
  character: { key: 'starter_hero', name: '初始勇者', description: '玩家进入游戏时使用的基础角色。', balanceGroup: 'starter_characters', tags: '玩家、近战' },
  skill: { key: 'fireball', name: '火球术', description: '对主要目标造成火焰伤害并附加燃烧。', balanceGroup: 'mage_skills', tags: '火焰、主动技能' },
  item: { key: 'training_sword', name: '训练木剑', description: '新手使用的基础近战武器。', balanceGroup: 'starter_weapons', tags: '武器、近战' },
  effect: { key: 'burning', name: '燃烧', description: '在数个回合内持续造成火焰伤害。', balanceGroup: 'damage_over_time', tags: '火焰、持续伤害' },
}

function helpTooltip(title: string) {
  return { title, icon: <QuestionCircleOutlined aria-hidden /> }
}

export default function EntityEditorPage() {
  const { kind = 'attribute', id = 'new' } = useParams()
  const entityKind = kind as EntityKind
  const navigate = useNavigate()
  const { project } = useAppState()
  const [searchParams, setSearchParams] = useSearchParams()
  const client = useQueryClient()
  const [form] = Form.useForm()
  const [dirty, setDirty] = useState(false)
  const [issues, setIssues] = useState<FieldIssue[]>([])
  const [conflict, setConflict] = useState(false)
  const [dailyFailure, setDailyFailure] = useState<{ jobID: string; state: string }>()
  const [unprotectedDate, setUnprotectedDate] = useState('')
  const [localSummary, setLocalSummary] = useState<LocalSummary>()
  const [validationRun, setValidationRun] = useState<ValidationRun>()
  const [deletedBalanceGroups, setDeletedBalanceGroups] = useState<string[]>([])
  const [messageApi, contextHolder] = message.useMessage()
  const creating = id === 'new'
  const kindLabel = entityKindLabels[entityKind]
  const basicExample = basicExamples[entityKind]
  const editingTag = entityKind === 'tag'
  const balanceGroupStorageKey = project?.id ? `entity-editor:hidden-balance-groups:${project.id}:${entityKind}` : ''

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
  const catalogs = useQuery({
    queryKey: ['entity-reference-catalogs', kind, id],
    enabled: Boolean(entity.data),
    queryFn: async (): Promise<EntityCatalogs> => {
      const entries = await Promise.all(catalogKinds.map(async catalogKind => {
        const items: EntityOption[] = []
        let cursor = ''
        do {
          const params = new URLSearchParams({ limit: '200' })
          if (cursor) params.set('cursor', cursor)
          const page = await apiRequest<EntityPage>(`/api/v1/entities/${catalogKind}?${params}`)
          items.push(...page.items)
          cursor = page.next_cursor ?? ''
        } while (cursor)
        return [catalogKind, items] as const
      }))
      return Object.fromEntries(entries) as EntityCatalogs
    },
  })

  const balanceGroupOptions = useMemo(() => {
    const values = new Set<string>([basicExample.balanceGroup])
    for (const item of catalogs.data?.[entityKind] ?? []) {
      const value = item.balance_group?.trim()
      if (value) values.add(value)
    }
    const current = entity.data?.entity.balance_group
    if (typeof current === 'string' && current.trim()) values.add(current.trim())
    const hidden = new Set(deletedBalanceGroups)
    return [...values].filter(value => !hidden.has(value)).sort((left, right) => left.localeCompare(right, 'zh-CN')).map(value => ({ value }))
  }, [basicExample.balanceGroup, catalogs.data, deletedBalanceGroups, entity.data, entityKind])

  useEffect(() => {
    if (!balanceGroupStorageKey) { setDeletedBalanceGroups([]); return }
    try {
      const stored = JSON.parse(localStorage.getItem(balanceGroupStorageKey) ?? '[]') as unknown
      setDeletedBalanceGroups(Array.isArray(stored) ? stored.filter((value): value is string => typeof value === 'string') : [])
    } catch {
      setDeletedBalanceGroups([])
    }
  }, [balanceGroupStorageKey])

  useEffect(() => {
    if (!entity.data) return
    form.resetFields()
    form.setFieldsValue({
      key: entity.data.entity.key ?? '',
      name: entity.data.entity.name ?? '',
      description: entity.data.entity.description ?? '',
      tag_ids: entity.data.entity.tag_ids ?? [],
      balance_group: entity.data.entity.balance_group ?? '',
      payload: entity.data.entity.payload ?? emptyDraft(entityKind).payload,
    })
    setDirty(false)
    setIssues([])
  }, [entity.data, entityKind, form])

  useEffect(() => {
    const warn = (event: BeforeUnloadEvent) => { if (dirty) { event.preventDefault(); event.returnValue = '' } }
    window.addEventListener('beforeunload', warn)
    return () => window.removeEventListener('beforeunload', warn)
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
    const values = form.getFieldsValue(true)
    const payload = preparePayload(entityKind, values.payload)
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

  function updateDeletedBalanceGroups(update: (current: string[]) => string[]) {
    setDeletedBalanceGroups(current => {
      const next = update(current)
      if (balanceGroupStorageKey) {
        try { localStorage.setItem(balanceGroupStorageKey, JSON.stringify(next)) } catch { /* UI suggestions can still update in memory. */ }
      }
      return next
    })
  }

  function deleteBalanceGroupOption(value: string) {
    updateDeletedBalanceGroups(current => current.includes(value) ? current : [...current, value])
    messageApi.success(`已从下拉候选中删除「${value}」，已保存配置未改动`)
  }

  function restoreBalanceGroupOption(value: string) {
    const normalized = value.trim()
    if (!normalized || !deletedBalanceGroups.includes(normalized)) return
    updateDeletedBalanceGroups(current => current.filter(item => item !== normalized))
  }

  if (entity.isLoading) return <div className="route-loading"><Typography.Text>正在加载编辑器…</Typography.Text></div>
  if (entity.isError) return <div className="page-container"><Alert type="error" showIcon title={errorMessage(entity.error, '无法加载对象')} action={<Button onClick={() => navigate(`/config/${kind}`)}>返回列表</Button>} /></div>

  return <div className="page-container entity-editor-page">
    {contextHolder}
    <PageHeader
      eyebrow={<><CodeOutlined /> 结构化实体</>}
      title={creating ? `新建${kindLabel}` : `${String(entity.data?.entity.name ?? kindLabel)} · 编辑`}
      description={schema.isLoading ? '正在读取表单配置…' : creating ? `按字段填写${kindLabel}信息；每一项都附有填写示例。` : `按字段编辑${kindLabel}信息，保存后会创建新的配置修订。`}
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
              <Col xs={24} md={12}><Form.Item name="key" label="Key" tooltip={helpTooltip('配置在系统内的稳定标识，用于引用、版本比较和问题定位。')} rules={[{ required: true, message: '请输入 Key' }, { pattern: /^[a-z][a-z0-9_]*$/, message: '仅可使用小写字母、数字和下划线' }]} extra={creating ? `示例：${basicExample.key}；用于系统内唯一识别` : undefined}><Input placeholder={`例如 ${basicExample.key}`} data-field-path="/key" /></Form.Item></Col>
              <Col xs={24} md={12}><Form.Item name="name" label="名称" tooltip={helpTooltip('配置的显示名称，会出现在列表、选择器和分析结果中。')} rules={[{ required: true, message: '请输入名称' }]} extra={creating ? `示例：${basicExample.name}；用于页面展示` : undefined}><Input placeholder={`例如 ${basicExample.name}`} data-field-path="/name" /></Form.Item></Col>
            </Row>
            <Form.Item name="description" label="说明" tooltip={helpTooltip('记录这项配置的用途和设计意图，方便维护者理解何时使用它。')} extra={creating ? `示例：${basicExample.description}` : undefined}><Input.TextArea rows={3} placeholder={basicExample.description} data-field-path="/description" /></Form.Item>
            <Row gutter={16}>
              <Col xs={24} md={12}>
                <Form.Item
                  name="tag_ids"
                  label={editingTag ? '关联标签（可选）' : '标签'}
                  tooltip={helpTooltip(editingTag
                    ? '给当前标签附加用于检索的标签；这里不会建立父子层级。'
                    : '用于配置分类、筛选和规则匹配，可以选择多个标签。')}
                  extra={editingTag
                    ? '用于给当前标签附加检索标签，与下方“父标签”的层级关系不同；可留空。'
                    : creating ? `示例：${basicExample.tags}` : undefined}
                >
                  <Select
                    mode="multiple"
                    allowClear
                    showSearch
                    maxTagCount="responsive"
                    optionFilterProp="label"
                    loading={catalogs.isLoading}
                    options={(catalogs.data?.tag ?? []).map(item => ({ value: item.id, label: `${item.name}（${item.key}）` }))}
                    placeholder={editingTag ? '可选择已有标签，也可留空' : '请选择用于分类和检索的标签'}
                    notFoundContent={catalogs.isLoading
                      ? '正在加载标签…'
                      : editingTag ? '暂无已有标签，可留空创建当前标签' : '暂无标签，请先在标签配置中创建'}
                    data-field-path="/tag_ids"
                  />
                </Form.Item>
              </Col>
              <Col xs={24} md={12}>
                <Form.Item name="balance_group" label="平衡分组" tooltip={helpTooltip('把同类型且需要一起比较的配置归入一组，供平衡分析和风险评估使用。删除图标只移除下拉候选，不会修改已保存配置。')} extra={creating ? `示例：${basicExample.balanceGroup}；同组配置便于一起比较` : undefined}>
                  <AutoComplete
                    allowClear
                    options={balanceGroupOptions}
                    suffixIcon={<DownOutlined />}
                    placeholder={catalogs.isLoading ? '正在加载已有分组…' : '请选择已有分组，也可输入新分组'}
                    filterOption={(inputValue, option) => String(option?.value ?? '').toLowerCase().includes(inputValue.toLowerCase())}
                    notFoundContent="没有匹配的已有分组，可直接输入新分组"
                    onChange={restoreBalanceGroupOption}
                    optionRender={option => {
                      const value = String(option.value ?? '')
                      return <div className="deletable-option">
                        <span>{value}</span>
                        <Button
                          danger
                          type="text"
                          size="small"
                          icon={<DeleteOutlined />}
                          title="删除候选项（不修改已保存配置）"
                          aria-label={`删除平衡分组候选 ${value}`}
                          onMouseDown={event => { event.preventDefault(); event.stopPropagation() }}
                          onClick={event => { event.stopPropagation(); deleteBalanceGroupOption(value) }}
                        />
                      </div>
                    }}
                    data-field-path="/balance_group"
                  />
                </Form.Item>
              </Col>
            </Row>
            <Divider titlePlacement="start">配置字段</Divider>
            <Typography.Paragraph type="secondary">请按业务含义填写以下字段，系统会自动整理并校验数据，无需编写 JSON。</Typography.Paragraph>
            {catalogs.isError && <Alert className="block-alert" type="warning" showIcon title="引用选项加载失败" description="属性、标签、技能、物品或效果的选择列表暂不可用。" action={<Button onClick={() => void catalogs.refetch()}>重试</Button>} />}
            <PayloadForm kind={entityKind} form={form} catalogs={catalogs.data} catalogsLoading={catalogs.isLoading} dslUnits={schema.data?.dsl_registry?.units} showExamples={creating} />
            <Space className="form-sync-status"><Typography.Text type={dirty ? 'warning' : 'secondary'}>{dirty ? '有未保存修改' : '已与服务器同步'}</Typography.Text></Space>
          </Form>
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
