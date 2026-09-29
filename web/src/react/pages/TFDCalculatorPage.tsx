import { useEffect, useMemo, useState } from 'react'
import { useQuery } from '@tanstack/react-query'
import { Alert, Button, Card, Empty, Modal, Segmented, Select, Space, Statistic, Table, Tag, Typography } from 'antd'
import { CalculatorOutlined, EditOutlined, ReloadOutlined } from '@ant-design/icons'
import { useNavigate } from 'react-router-dom'
import PageHeader from '../components/PageHeader'
import { apiRequest, errorMessage } from '../api'
import { useAppState } from '../context/AppContext'
import { TFDCalculator, workbookFromSourceTag, type CalculationCell, type TFDEntity } from '@/tfd/calculator'

type EntityPage = { items: TFDEntity[]; next_cursor: string | null }
type View = 'panel' | 'skills' | 'all'

async function loadKind(kind: string): Promise<TFDEntity[]> {
  const entities: TFDEntity[] = []
  let cursor = ''
  do {
    const params = new URLSearchParams({ limit: '200' })
    if (cursor) params.set('cursor', cursor)
    const page = await apiRequest<EntityPage>(`/api/v1/entities/${kind}?${params}`)
    entities.push(...page.items)
    cursor = page.next_cursor ?? ''
  } while (cursor)
  return entities
}

async function loadCalculator(): Promise<TFDCalculator> {
  const parts = await Promise.all(['tag', 'attribute', 'item', 'character'].map(loadKind))
  const entities = parts.flat()
  const source = entities.find(entity => entity.kind === 'tag' && entity.key === 'tfd_source')
  if (!source) throw new Error('当前项目没有 TFD v1.04 工作簿数据')
  return new TFDCalculator(workbookFromSourceTag(source), entities)
}

function format(value: unknown): string {
  if (value === null || value === undefined) return '—'
  if (typeof value === 'number') return Number.isInteger(value) ? String(value) : Number(value.toPrecision(12)).toString()
  return String(value)
}

function rowNumber(address: string) { return Number(/\d+$/.exec(address)?.[0] ?? 0) }

export default function TFDCalculatorPage() {
  const { project } = useAppState()
  const navigate = useNavigate()
  const [selected, setSelected] = useState('')
  const [view, setView] = useState<View>('panel')
  const [formula, setFormula] = useState<CalculationCell>()
  const query = useQuery({ queryKey: ['tfd-calculator', project?.id], queryFn: loadCalculator, enabled: Boolean(project) })
  const names = useMemo(() => {
    const values = query.data?.characterNames() ?? []
    if (query.data?.hasSheet('伤害公式验证')) values.push('伤害公式验证')
    return values
  }, [query.data])
  useEffect(() => { if (names.length && !names.includes(selected)) setSelected(names.includes('蒂雅') ? '蒂雅' : names[0]) }, [names, selected])

  const cells = useMemo(() => selected && query.data ? query.data.evaluateSheet(selected) : [], [query.data, selected])
  const visible = useMemo(() => cells.filter(cell => view === 'all' || (view === 'panel' ? rowNumber(cell.address) >= 6 && rowNumber(cell.address) <= 11 : rowNumber(cell.address) > 11)), [cells, view])
  const matched = cells.filter(cell => cell.status === 'matched').length
  const changed = cells.filter(cell => cell.status === 'changed').length
  const sourceErrors = cells.filter(cell => cell.status === 'source_error').length
  const calculationErrors = cells.filter(cell => cell.status === 'error').length
  const selectedCharacter = selected === '伤害公式验证' ? query.data?.character('伊内兹') : query.data?.character(selected)

  return <div className="page-container">
    <PageHeader
      eyebrow={<><CalculatorOutlined /> 第一后裔工作簿计算</>}
      title="配装与伤害计算"
      description="用项目里的角色基础值和装备数值运行 TFD v1.04 的计算规则，并与原工作簿缓存值对照。"
      extra={<Button icon={<ReloadOutlined />} loading={query.isFetching} onClick={() => void query.refetch()}>重新读取项目数据</Button>}
    />
    {query.isError && <Alert className="block-alert" type="error" showIcon title={errorMessage(query.error, '计算模型加载失败')} />}
    {query.isLoading && <Card loading className="section-card" />}
    {query.data && <>
      <Card className="section-card" style={{ marginBottom: 16 }}>
        <Space wrap size="middle" style={{ width: '100%', justifyContent: 'space-between' }}>
          <Space wrap>
            <Select
              aria-label="选择角色或验证方案"
              style={{ width: 250 }}
              value={selected || undefined}
              options={names.map(name => ({ value: name, label: name === '伤害公式验证' ? '伊内兹 · 伤害公式验证方案' : name }))}
              onChange={value => { setSelected(value); setView('panel') }}
            />
            <Segmented<View> value={view} onChange={setView} options={[{ label: '面板', value: 'panel' }, { label: '技能与伤害', value: 'skills' }, { label: '全部', value: 'all' }]} />
          </Space>
          {selectedCharacter && <Button icon={<EditOutlined />} onClick={() => navigate(`/config/character/${selectedCharacter.id}`)}>编辑角色配置</Button>}
        </Space>
      </Card>
      <Space wrap size="large" style={{ marginBottom: 16 }}>
        <Statistic title="与原表一致" value={matched} suffix={`/ ${cells.length}`} />
        <Statistic title="当前配置有变化" value={changed} />
        <Statistic title="原表引用错误" value={sourceErrors} />
        <Statistic title="计算错误" value={calculationErrors} />
      </Space>
      {sourceErrors > 0 && <Alert className="block-alert" type="warning" showIcon title="原工作簿有失效引用" description={`「${selected}」工作表的 ${sourceErrors} 个结果依赖 #REF!，已标记为来源错误，未推测缺失引用。`} />}
      {selected === '伤害公式验证' && <Alert className="block-alert" type="info" showIcon title="此页使用原工作簿的验证配装" description="验证方案与伊内兹主配装不同；角色基础值仍从项目配置读取。" />}
      {changed > 0 && <Alert className="block-alert" type="info" showIcon title="当前项目计算值与原工作簿不同" description="修改项目角色或装备数值后，计算值会随之更新；原表缓存值保留用于对照。" />}
      <Card className="section-card">
        <Table<CalculationCell>
          rowKey="address"
          dataSource={visible}
          pagination={{ pageSize: 25, showSizeChanger: true }}
          locale={{ emptyText: <Empty description="本视图没有公式" /> }}
          columns={[
            { title: '单元格', dataIndex: 'address', width: 110, render: value => <Typography.Text code>{value}</Typography.Text> },
            { title: '行说明', dataIndex: 'label', ellipsis: true, render: value => value || '—' },
            { title: '系统计算', dataIndex: 'value', width: 175, render: value => <Typography.Text copyable={value === null ? undefined : { text: String(value) }}>{format(value)}</Typography.Text> },
            { title: '原表缓存', dataIndex: 'cached', width: 175, render: value => format(value) },
            { title: '状态', dataIndex: 'status', width: 145, render: (_, cell) => <Tag color={cell.status === 'matched' ? 'success' : cell.status === 'changed' ? 'processing' : 'error'}>{cell.status === 'matched' ? '一致' : cell.status === 'changed' ? '已变化' : cell.status === 'source_error' ? '来源错误' : '计算错误'}</Tag> },
            { title: '公式', key: 'formula', width: 95, render: (_, cell) => <Button type="link" onClick={() => setFormula(cell)}>查看</Button> },
          ]}
        />
      </Card>
      <Typography.Paragraph type="secondary" style={{ marginTop: 12 }}>工作簿的下拉候选由系统角色与物品目录提供。公式结果按当前项目数据即时计算；原表缓存用于核对。</Typography.Paragraph>
    </>}
    <Modal open={Boolean(formula)} title={`${selected}!${formula?.address ?? ''} 原工作簿公式`} footer={null} width={760} onCancel={() => setFormula(undefined)}>
      {formula?.message && <Alert type={formula.status === 'source_error' ? 'warning' : 'error'} showIcon title={formula.message} style={{ marginBottom: 12 }} />}
      <Typography.Paragraph copyable={{ text: formula?.formula ?? '' }} style={{ whiteSpace: 'pre-wrap', overflowWrap: 'anywhere' }}>{formula?.formula}</Typography.Paragraph>
    </Modal>
  </div>
}
