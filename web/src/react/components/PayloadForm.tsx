import { useState, type ReactNode } from 'react'
import { Alert, AutoComplete, Button, Card, Col, Empty, Form, Input, Row, Select, Space, Typography, type AutoCompleteProps, type FormInstance, type SelectProps } from 'antd'
import { DeleteOutlined, DownOutlined, PlusOutlined, QuestionCircleOutlined } from '@ant-design/icons'
import type { components } from '@/api/generated'
import type { EntityKind } from '@/forms/registry'
import { apiRequest, errorMessage } from '../api'

export interface EntityOption {
  id: string
  key: string
  name: string
  entity_version: number
  balance_group?: string | null
  payload?: {
    value_type?: string
    dimension?: string
    base_unit?: string
  }
}

export type EntityCatalogs = Partial<Record<EntityKind, EntityOption[]>>

interface DslUnitOption {
  name: string
  value_type: string
  dimension: string
  base: string
}

interface PayloadFormProps {
  kind: EntityKind
  form: FormInstance
  catalogs?: EntityCatalogs
  catalogsLoading?: boolean
  dslUnits?: DslUnitOption[]
  showExamples?: boolean
}

type PathPart = string | number
type FormulaValidationResult = components['schemas']['FormulaValidationResult']
type FormulaDiagnostic = components['schemas']['FormulaDiagnostic']

const decimalRule = { pattern: /^-?(0|[1-9][0-9]*)(\.[0-9]+)?$/, message: '请输入数字，例如 12、-3 或 0.25' }
const integerRule = { pattern: /^-?(0|[1-9][0-9]*)$/, message: '请输入整数，例如 0、3 或 -1' }
const nonNegativeIntegerRule = { pattern: /^(0|[1-9][0-9]*)$/, message: '请输入非负整数，例如 0 或 3' }

const valueTypeOptions = [
  { value: 'integer', label: '整数（integer）' },
  { value: 'decimal', label: '小数（decimal）' },
  { value: 'boolean', label: '是 / 否（boolean）' },
]

const operationOptions = [
  { value: 'Add', label: '增加（Add）' },
  { value: 'Multiply', label: '乘算（Multiply）' },
  { value: 'Override', label: '覆盖（Override）' },
  { value: 'Max', label: '取较大值（Max）' },
  { value: 'Min', label: '取较小值（Min）' },
]

const targetTypeOptions = [
  { value: 'self', label: '自身' },
  { value: 'source', label: '效果来源' },
  { value: 'primary_target', label: '主要目标' },
  { value: 'all_targets', label: '所有目标' },
  { value: 'targets_with_tag', label: '带指定标签的目标' },
]

const eventOptions = [
  { value: 'on_use', label: '使用时' },
  { value: 'on_hit', label: '命中时' },
  { value: 'on_damage', label: '造成伤害时' },
  { value: 'on_heal', label: '治疗时' },
  { value: 'on_interval', label: '每个间隔' },
  { value: 'on_effect_applied', label: '效果附加时' },
]

const refreshPolicyOptions = [
  { value: 'refresh', label: '刷新持续时间' },
  { value: 'replace', label: '替换旧效果' },
  { value: 'ignore', label: '忽略新效果' },
]

const fieldHelp = {
  targetType: '决定技能或触发规则会把效果应用到自身、来源、主要目标、全部目标或指定标签的目标。',
  targetTag: '当目标类型为“带指定标签的目标”时，用这个标签筛选实际作用对象。',
  formulaAttribute: '选择这条数值或公式最终要设置或消耗的属性。',
  modifierAttribute: '选择这条修改规则要改变的属性。',
  modifierOperation: '决定新数值与原属性值的组合方式，例如增加、乘算、覆盖或取极值。',
  modifierValue: '作为计算方式的操作数；填写负数可以表示减少。',
  triggerEvent: '选择系统在什么事件发生时检查并执行这条规则。',
  triggerCondition: '进一步限制规则何时执行；留空时，只要触发事件发生就会执行。',
  triggerEffects: '选择规则触发后要施加的一个或多个效果。',
  terminationBudget: '限制这条规则在一次触发链中的执行次数，防止效果循环无限触发。',
  valueType: '决定属性保存和校验为整数、小数还是布尔值，并影响默认值的填写方式。',
  dimension: '描述数值的物理或业务维度；只有兼容量纲的数值才能在公式中直接运算。',
  baseUnit: '该属性计算和存储时使用的标准单位，公式中的单位会以它为基准校验。',
  defaultValue: '没有角色、场景或其他配置覆盖时，这个属性采用的初始值。',
  minValue: '限制该属性允许的最小值，并用于配置校验。',
  maxValue: '限制该属性允许的最大值，并用于配置校验。',
  displayScale: '只在展示数值时应用的倍率，例如填 100 可把 0.25 显示为 25。',
  tagCategory: '用于按业务领域归类标签，例如元素、阵营或玩法。',
  parentTags: '建立标签的上级层级，便于按标签树组织和继承分类关系。',
  characterSkills: '选择角色可以使用的技能。',
  characterItems: '选择角色默认携带或装备的物品。',
  cooldown: '技能使用后再次可用前需要等待的时间单位；0 表示没有冷却。',
  directEffects: '技能执行时直接施加的效果，不需要额外触发条件。',
  itemSlot: '标识物品占用的装备部位或物品类型，用于装备和规则判断。',
  itemEffects: '物品被使用或装备时直接附带的效果。',
  enhanceTags: '标记该物品会强化哪些类别的对象，供规则和分析按标签匹配。',
  duration: '效果保持的时间单位；0 表示立即结算。',
  stackOperation: '重复获得效果时，各层数值采用的组合方式。',
  maxStacks: '同一效果允许同时存在的最大层数。',
  refreshPolicy: '已有同名效果时，决定新效果刷新时长、替换旧效果还是被忽略。',
  stackPriority: '多个效果发生覆盖或竞争时使用的排序值。',
  stackCap: '叠加计算后的数值上限，用于防止效果无限增长。',
} as const

function helpTooltip(title: ReactNode) {
  return { title, icon: <QuestionCircleOutlined aria-hidden /> }
}

function formulaExpressionTooltip(name: 'attribute_values' | 'costs') {
  const isCost = name === 'costs'
  const expression = isCost ? '20[resource_point]' : '80[health_point]'
  const amount = isCost ? '20' : '80'
  const unit = isCost ? 'resource_point' : 'health_point'
  const owner = isCost ? '当前技能配置中的“资源消耗”规则' : '当前角色配置中的“属性值”规则'
  const attribute = isCost ? '左侧选择的资源属性配置' : '左侧选择的“生命值”属性配置'
  const first = Number(amount)
  const second = isCost ? 10 : 20
  const quantity = (value: number | string) => `${value}[${unit}]`
  const operationExamples = [
    { operation: '加法 +', formula: `${quantity(first)} + ${quantity(second)}`, result: quantity(first + second) },
    { operation: '减法 -', formula: `${quantity(first)} - ${quantity(second)}`, result: quantity(first - second) },
    { operation: '乘法 *', formula: `${quantity(first)} * 2[scalar]`, result: quantity(first * 2) },
    { operation: '除法 /', formula: `${quantity(first)} / 2[scalar]`, result: quantity(first / 2) },
    { operation: '小于 <', formula: `${quantity(first)} < ${quantity(100)}`, result: 'true' },
    { operation: '小于等于 <=', formula: `${quantity(first)} <= ${quantity(100)}`, result: 'true' },
    { operation: '大于 >', formula: `${quantity(first)} > ${quantity(100)}`, result: 'false' },
    { operation: '大于等于 >=', formula: `${quantity(first)} >= ${quantity(100)}`, result: 'false' },
    { operation: '等于 ==', formula: `${quantity(first)} == ${quantity(first)}`, result: 'true' },
    { operation: '不等于 !=', formula: `${quantity(first)} != ${quantity(100)}`, result: 'true' },
    { operation: '逻辑与 &&', formula: 'true && false', result: 'false' },
    { operation: '逻辑或 ||', formula: 'true || false', result: 'true' },
    { operation: '逻辑非 !', formula: '!false', result: 'true' },
    { operation: '条件 if', formula: `if(${quantity(first)} < ${quantity(100)}, ${quantity(first)}, ${quantity(100)})`, result: quantity(first) },
    { operation: '最小值 min', formula: `min(${quantity(first)}, ${quantity(100)})`, result: quantity(first) },
    { operation: '最大值 max', formula: `max(${quantity(first)}, ${quantity(100)})`, result: quantity(100) },
    { operation: '区间限制 clamp', formula: `clamp(${quantity(120)}, ${quantity(0)}, ${quantity(100)})`, result: quantity(100) },
    { operation: '绝对值 abs', formula: `abs(-${quantity(first)})`, result: quantity(first) },
    { operation: '向下取整 floor', formula: `floor(${quantity(`${first}.8`)})`, result: quantity(first) },
    { operation: '向上取整 ceil', formula: `ceil(${quantity(`${first}.2`)})`, result: quantity(first + 1) },
    { operation: '四舍六入五成双 round', formula: `round(${quantity(`${first}.6`)})`, result: quantity(first + 1) },
  ]
  return {
    ...helpTooltip(<div className="formula-help">
      <strong>公式写法示例：<code>{expression}</code></strong>
      <p><code>{amount}</code> 是数值，<code>[{unit}]</code> 为它声明单位。这里没有其他运算符，因此计算结果就是 <strong>{amount} {unit}</strong>。</p>
      <div>这个结果会用到：</div>
      <ol>
        <li>{owner}，它决定这条公式属于哪个角色或技能；</li>
        <li>{attribute}，它决定结果写入或扣除哪个属性，并应与公式的数值类型、量纲匹配；</li>
        <li>DSL 单位注册表中的 <code>{unit}</code>，它定义该单位所属的量纲和基础单位。</li>
      </ol>
      <p>这类常量公式不会读取技能、物品或其他属性。需要读取配置值时，使用属性 Key 编写 <code>{'${self:属性_key}'}</code>、<code>{'${source:属性_key}'}</code>、<code>{'${target:属性_key}'}</code>，或使用 <code>{'${scenario:变量名}'}</code>；这些引用才会成为额外的计算输入。</p>
      <strong className="formula-operation-title">支持的操作与示例</strong>
      <div className="formula-example-scroll">
        <table className="formula-example-table" aria-label="公式支持的操作示例">
          <thead><tr><th>操作</th><th>写法</th><th>结果</th></tr></thead>
          <tbody>{operationExamples.map(example => <tr key={example.operation}>
            <td>{example.operation}</td>
            <td><code>{example.formula}</code></td>
            <td><code>{example.result}</code></td>
          </tr>)}</tbody>
        </table>
      </div>
      <p><strong>量纲规则：</strong>加减、比较、<code>if</code> 的两个结果及函数参数必须使用相同量纲；乘除示例中的 <code>scalar</code> 是无量纲系数。比如 <code>80[health_point] + 10[damage_point]</code> 会因量纲不同而校验失败。</p>
    </div>),
    styles: { root: { maxWidth: 760 }, container: { maxHeight: '75vh', overflowY: 'auto' as const } },
  }
}

function filterAutocompleteOption(inputValue: string, option?: { value?: unknown }) {
  return String(option?.value ?? '').toLowerCase().includes(inputValue.toLowerCase())
}

const formulaDiagnosticLabels: Record<FormulaDiagnostic['code'], string> = {
  FORMULA_SYNTAX_INVALID: '公式语法不正确',
  FORMULA_UNKNOWN_VARIABLE: '公式引用了未知变量或属性 Key',
  FORMULA_UNKNOWN_FUNCTION: '公式使用了不支持的函数',
  FORMULA_TYPE_MISMATCH: '公式结果类型与目标属性不匹配',
  FORMULA_UNIT_MISMATCH: '公式中的单位或量纲不兼容；加减和比较两边必须使用相同量纲',
  FORMULA_DIVISION_BY_ZERO: '公式不能除以零',
  NUMERIC_NON_FINITE: '公式产生了无效数值',
  NUMERIC_OUT_OF_RANGE: '公式结果超出允许的数值范围',
}

function formulaDiagnosticMessage(diagnostic: FormulaDiagnostic) {
  const position = diagnostic.end_byte > diagnostic.start_byte ? `（位置 ${diagnostic.start_byte + 1}–${diagnostic.end_byte}）` : ''
  return `${formulaDiagnosticLabels[diagnostic.code]}${position}`
}

async function validateFormulaExpression(expression: string, outputAttributeID?: string) {
  try {
    const result = await apiRequest<FormulaValidationResult>('/api/v1/formula-validations', {
      method: 'POST',
      body: JSON.stringify({ expression, ...(outputAttributeID ? { output_attribute_id: outputAttributeID } : {}) }),
    })
    if (!result.valid && result.diagnostics.length > 0) throw new Error(formulaDiagnosticMessage(result.diagnostics[0]))
  } catch (cause) {
    if (cause instanceof Error && !('status' in cause)) throw cause
    throw new Error(errorMessage(cause, '公式校验服务暂不可用'))
  }
}

function formulaSuggestions(name: 'attribute_values' | 'costs', attribute?: EntityOption, attributes: EntityOption[] = [], dslUnits: DslUnitOption[] = []) {
  const options = new Map<string, string>()
  const valueType = attribute?.payload?.value_type
  const preferredUnit = attribute?.payload?.base_unit
    ?? dslUnits.find(unit => unit.name === (name === 'costs' ? 'resource_point' : 'health_point'))?.name

  if (valueType === 'boolean') {
    options.set('true', 'true · 固定值：是')
    options.set('false', 'false · 固定值：否')
    options.set('if(true, false, true)', 'if(true, false, true) · 条件公式')
  } else {
    const values = name === 'costs' ? ['0', '1', '10', '20', '50', '100'] : ['0', '1', '10', '50', '80', '100']
    for (const value of values) {
      const expression = preferredUnit ? `${value}[${preferredUnit}]` : value
      options.set(expression, `${expression} · 固定值${preferredUnit ? `（${preferredUnit}）` : ''}`)
    }
    const low = preferredUnit ? `0[${preferredUnit}]` : '0'
    const current = preferredUnit ? `${name === 'costs' ? '20' : '80'}[${preferredUnit}]` : name === 'costs' ? '20' : '80'
    const high = preferredUnit ? `100[${preferredUnit}]` : '100'
    options.set(`min(${current}, ${high})`, `min(${current}, ${high}) · 取较小值`)
    options.set(`max(${low}, ${current})`, `max(${low}, ${current}) · 取较大值`)
    options.set(`clamp(${current}, ${low}, ${high})`, `clamp(${current}, ${low}, ${high}) · 限制在区间内`)
  }

  const compatibleAttributes = attribute ? attributes.filter(item => {
    if (!item.payload || !attribute.payload) return true
    return item.payload.value_type === attribute.payload.value_type && item.payload.dimension === attribute.payload.dimension
  }) : attributes
  for (const item of compatibleAttributes) {
    const suffix = ` · ${item.name}（${item.key}）`
    options.set(`\${self:${item.key}}`, `\${self:${item.key}} · 读取自身${suffix}`)
  }
  if (attribute) {
    options.set(`\${source:${attribute.key}}`, `\${source:${attribute.key}} · 读取效果来源 · ${attribute.name}（${attribute.key}）`)
    options.set(`\${target:${attribute.key}}`, `\${target:${attribute.key}} · 读取目标 · ${attribute.name}（${attribute.key}）`)
  }
  return [...options].map(([value, label]) => ({ value, label }))
}

function FormulaExpressionInput({
  name, watchName, catalogs, dslUnits, fieldPath, onClick, onFocus, onSearch, onSelect, ...inputProps
}: Omit<AutoCompleteProps<string>, 'options'> & {
  name: 'attribute_values' | 'costs'
  watchName: PathPart[]
  catalogs?: EntityCatalogs
  dslUnits?: DslUnitOption[]
  fieldPath: string
}) {
  const form = Form.useFormInstance()
  const [filtering, setFiltering] = useState(false)
  const outputAttributeID = Form.useWatch(watchName, form)
  const attributes = catalogs?.attribute ?? []
  const outputAttribute = attributes.find(item => item.id === outputAttributeID)
  const options = formulaSuggestions(name, outputAttribute, attributes, dslUnits)
  return <AutoComplete
    {...inputProps}
    allowClear
    options={options}
    suffixIcon={<DownOutlined />}
    placeholder="请输入公式，或点击选择常用公式"
    popupMatchSelectWidth={520}
    filterOption={(inputValue, option) => !filtering || `${String(option?.value ?? '')} ${String(option?.label ?? '')}`.toLowerCase().includes(inputValue.toLowerCase())}
    notFoundContent="没有匹配的公式，可继续直接输入"
    onClick={event => { setFiltering(false); onClick?.(event) }}
    onFocus={event => { setFiltering(false); onFocus?.(event) }}
    onSearch={value => { setFiltering(true); onSearch?.(value) }}
    onSelect={(value, option) => { setFiltering(false); onSelect?.(value, option) }}
    data-field-path={fieldPath}
  />
}

const payloadExamples: Record<EntityKind, string> = {
  attribute: '“攻击力”可设置为小数类型，量纲填写 damage，基础单位填写 point，默认值填写 100。',
  tag: '“火焰”可归入 element 分类，并把“元素”选为父标签。',
  character: '“初始勇者”可设置基础生命值 100，并关联“普通攻击”技能和“木剑”物品。',
  skill: '“火球术”可设置冷却 3，目标选“主要目标”，再关联“火焰伤害”效果。',
  item: '“训练木剑”可把部位填写为 weapon，并添加一条“攻击力增加 10”的属性修改。',
  effect: '“燃烧”可设置持续 5，添加“生命值减少 8”的修改，并允许最多叠加 3 层。',
}

function fieldExample(enabled: boolean | undefined, text: string) {
  return enabled ? <span className="field-example">示例：{text}</span> : undefined
}

type ReferenceSelectProps = Omit<SelectProps, 'mode' | 'options' | 'loading' | 'disabled' | 'placeholder' | 'notFoundContent'> & {
  entityKind: EntityKind
  catalogs?: EntityCatalogs
  loading?: boolean
  multiple?: boolean
  placeholder: string
  fieldPath: string
  disabled?: boolean
  emptyText?: ReactNode
}

function ReferenceSelect({
  entityKind, catalogs, loading, multiple, placeholder, fieldPath, disabled, emptyText, ...selectProps
}: ReferenceSelectProps) {
  const options = (catalogs?.[entityKind] ?? []).map(item => ({
    value: item.id,
    label: `${item.name}（${item.key}）`,
  }))
  return <Select
    {...selectProps}
    mode={multiple ? 'multiple' : undefined}
    allowClear
    showSearch
    maxTagCount="responsive"
    optionFilterProp="label"
    options={options}
    loading={loading}
    disabled={disabled}
    placeholder={placeholder}
    notFoundContent={loading ? '正在加载可选项…' : emptyText ?? '暂无可选项，请先在配置中心创建'}
    data-field-path={fieldPath}
  />
}

function PayloadList({
  name, title, description, example, addLabel, createItem, children,
}: {
  name: string
  title: string
  description: string
  example?: string
  addLabel: string
  createItem: () => unknown
  children: (field: { key: number; name: number }, index: number, remove: (index: number | number[]) => void) => ReactNode
}) {
  return <Form.List name={['payload', name]}>
    {(fields, { add, remove }) => <section className="payload-list-block" data-field-path={`/payload/${name}`}>
      <div className="payload-list-header">
        <div>
          <Typography.Text strong>{title}</Typography.Text>
          <Typography.Paragraph type="secondary">{description}</Typography.Paragraph>
          {example && <Typography.Paragraph className="field-example">示例：{example}</Typography.Paragraph>}
        </div>
        <Button type="dashed" icon={<PlusOutlined />} onClick={() => add(createItem())}>{addLabel}</Button>
      </div>
      {fields.length === 0
        ? <Empty className="payload-list-empty" image={Empty.PRESENTED_IMAGE_SIMPLE} description={`尚未添加${title}`} />
        : fields.map((field, index) => children(field, index, remove))}
    </section>}
  </Form.List>
}

function TargetSelectorFields({
  itemName, watchName, path, catalogs, loading, showExamples,
}: {
  itemName: PathPart[]
  watchName: PathPart[]
  path: string
  catalogs?: EntityCatalogs
  loading?: boolean
  showExamples?: boolean
}) {
  const form = Form.useFormInstance()
  const targetType = Form.useWatch([...watchName, 'type'], form)
  return <>
    <Col xs={24} md={12}>
      <Form.Item name={[...itemName, 'type']} label="目标类型" tooltip={helpTooltip(fieldHelp.targetType)} rules={[{ required: true, message: '请选择目标类型' }]} extra={fieldExample(showExamples, '对单体技能选择“主要目标”')}>
        <Select
          options={targetTypeOptions}
          placeholder="请选择目标类型"
          data-field-path={`${path}/type`}
          onChange={value => { if (value !== 'targets_with_tag') form.setFieldValue([...watchName, 'tag_id'], undefined) }}
        />
      </Form.Item>
    </Col>
    <Col xs={24} md={12}>
      <Form.Item
        name={[...itemName, 'tag_id']}
        label="目标标签"
        tooltip={helpTooltip(fieldHelp.targetTag)}
        dependencies={[[...watchName, 'type']]}
        rules={[({ getFieldValue }) => ({
          validator: (_, value) => getFieldValue([...watchName, 'type']) !== 'targets_with_tag' || value
            ? Promise.resolve()
            : Promise.reject(new Error('请选择目标标签')),
        })]}
        extra={fieldExample(showExamples, '目标类型为“带指定标签的目标”时，选择“敌人”')}
      >
        <ReferenceSelect entityKind="tag" catalogs={catalogs} loading={loading} disabled={targetType !== 'targets_with_tag'} placeholder="请选择用于筛选目标的标签" fieldPath={`${path}/tag_id`} />
      </Form.Item>
    </Col>
  </>
}

function FormulaBindings({
  name, title, example, catalogs, loading, dslUnits, showExamples,
}: {
  name: 'attribute_values' | 'costs'
  title: string
  example: string
  catalogs?: EntityCatalogs
  loading?: boolean
  dslUnits?: DslUnitOption[]
  showExamples?: boolean
}) {
  const form = Form.useFormInstance()
  return <PayloadList
    name={name}
    title={title}
    description="选择一个属性，并用公式填写它的值。"
    example={showExamples ? example : undefined}
    addLabel={`添加${title}`}
    createItem={() => ({ output_attribute_id: undefined, expression: '' })}
  >
    {(field, index, remove) => <Card
      key={field.key}
      size="small"
      className="payload-item-card"
      title={`${title} ${index + 1}`}
      extra={<Button danger type="text" icon={<DeleteOutlined />} aria-label={`删除${title} ${index + 1}`} onClick={() => remove(field.name)} />}
    >
      <Row gutter={16}>
        <Col xs={24} md={10}>
          <Form.Item name={[field.name, 'output_attribute_id']} label="属性" tooltip={helpTooltip(fieldHelp.formulaAttribute)} rules={[{ required: true, message: '请选择属性' }]} extra={fieldExample(showExamples, name === 'costs' ? '法力值' : '生命值')}>
            <ReferenceSelect entityKind="attribute" catalogs={catalogs} loading={loading} placeholder="请选择属性" fieldPath={`/payload/${name}/${index}/output_attribute_id`} />
          </Form.Item>
        </Col>
        <Col xs={24} md={14}>
          <Form.Item
            name={[field.name, 'expression']}
            label="数值或公式"
            tooltip={formulaExpressionTooltip(name)}
            validateTrigger="onBlur"
            rules={[
              { required: true, message: '请输入数值或公式' },
              { validator: async (_, value) => {
                if (typeof value !== 'string' || !value.trim()) return
                const outputAttributeID = form.getFieldValue(['payload', name, field.name, 'output_attribute_id']) as string | undefined
                await validateFormulaExpression(value, outputAttributeID)
              } },
            ]}
            extra={fieldExample(showExamples, name === 'costs' ? '20[resource_point]' : '80[health_point]')}
          >
            <FormulaExpressionInput
              name={name}
              watchName={['payload', name, field.name, 'output_attribute_id']}
              catalogs={catalogs}
              dslUnits={dslUnits}
              fieldPath={`/payload/${name}/${index}/expression`}
            />
          </Form.Item>
        </Col>
      </Row>
    </Card>}
  </PayloadList>
}

function Modifiers({
  name, title, catalogs, loading, showExamples,
}: {
  name: 'attribute_modifiers' | 'modifiers'
  title: string
  catalogs?: EntityCatalogs
  loading?: boolean
  showExamples?: boolean
}) {
  return <PayloadList
    name={name}
    title={title}
    description="选择要修改的属性、计算方式和数值。"
    example={showExamples ? '选择“攻击力”，方式选“增加”，数值填写 10。' : undefined}
    addLabel={`添加${title}`}
    createItem={() => ({ attribute_id: undefined, operation: 'Add', value: '0' })}
  >
    {(field, index, remove) => <Card
      key={field.key}
      size="small"
      className="payload-item-card"
      title={`${title} ${index + 1}`}
      extra={<Button danger type="text" icon={<DeleteOutlined />} aria-label={`删除${title} ${index + 1}`} onClick={() => remove(field.name)} />}
    >
      <Row gutter={16}>
        <Col xs={24} md={9}>
          <Form.Item name={[field.name, 'attribute_id']} label="属性" tooltip={helpTooltip(fieldHelp.modifierAttribute)} rules={[{ required: true, message: '请选择属性' }]} extra={fieldExample(showExamples, '攻击力')}>
            <ReferenceSelect entityKind="attribute" catalogs={catalogs} loading={loading} placeholder="请选择要修改的属性" fieldPath={`/payload/${name}/${index}/attribute_id`} />
          </Form.Item>
        </Col>
        <Col xs={24} md={7}>
          <Form.Item name={[field.name, 'operation']} label="计算方式" tooltip={helpTooltip(fieldHelp.modifierOperation)} rules={[{ required: true, message: '请选择计算方式' }]} extra={fieldExample(showExamples, '增加（Add）')}>
            <Select options={operationOptions} placeholder="请选择计算方式" data-field-path={`/payload/${name}/${index}/operation`} />
          </Form.Item>
        </Col>
        <Col xs={24} md={8}>
          <Form.Item name={[field.name, 'value']} label="数值" tooltip={helpTooltip(fieldHelp.modifierValue)} rules={[{ required: true, message: '请输入数值' }, decimalRule]} extra={fieldExample(showExamples, '10；减少 8 可填写 -8')}>
            <Input placeholder="请输入数字" data-field-path={`/payload/${name}/${index}/value`} />
          </Form.Item>
        </Col>
      </Row>
    </Card>}
  </PayloadList>
}

function TriggerRules({
  name, title, catalogs, loading, showExamples,
}: {
  name: 'rule_blocks' | 'trigger_blocks'
  title: string
  catalogs?: EntityCatalogs
  loading?: boolean
  showExamples?: boolean
}) {
  return <PayloadList
    name={name}
    title={title}
    description="定义何时触发、作用于谁，以及触发哪些效果。"
    example={showExamples ? '命中时，对主要目标施加“燃烧”效果；条件可填写 ${self:critical_rate} > 0.5。' : undefined}
    addLabel={`添加${title}`}
    createItem={() => ({ event: 'on_use', condition: '', target: { type: 'self' }, effect_ids: [], termination_budget: '' })}
  >
    {(field, index, remove) => <Card
      key={field.key}
      size="small"
      className="payload-item-card"
      title={`${title} ${index + 1}`}
      extra={<Button danger type="text" icon={<DeleteOutlined />} aria-label={`删除${title} ${index + 1}`} onClick={() => remove(field.name)} />}
    >
      <Row gutter={16}>
        <Col xs={24} md={8}>
          <Form.Item name={[field.name, 'event']} label="触发时机" tooltip={helpTooltip(fieldHelp.triggerEvent)} rules={[{ required: true, message: '请选择触发时机' }]} extra={fieldExample(showExamples, '命中时')}>
            <Select options={eventOptions} placeholder="请选择触发时机" data-field-path={`/payload/${name}/${index}/event`} />
          </Form.Item>
        </Col>
        <Col xs={24} md={16}>
          <Form.Item name={[field.name, 'condition']} label="触发条件（可选）" tooltip={helpTooltip(fieldHelp.triggerCondition)} extra={fieldExample(showExamples, '${self:critical_rate} > 0.5；留空表示总是触发')}>
            <Input placeholder="留空表示总是触发" data-field-path={`/payload/${name}/${index}/condition`} />
          </Form.Item>
        </Col>
        <TargetSelectorFields
          itemName={[field.name, 'target']}
          watchName={['payload', name, index, 'target']}
          path={`/payload/${name}/${index}/target`}
          catalogs={catalogs}
          loading={loading}
          showExamples={showExamples}
        />
        <Col xs={24} md={16}>
          <Form.Item name={[field.name, 'effect_ids']} label="触发效果（可选）" tooltip={helpTooltip(fieldHelp.triggerEffects)} extra={fieldExample(showExamples, '燃烧、减速')}>
            <ReferenceSelect multiple entityKind="effect" catalogs={catalogs} loading={loading} placeholder="请选择要触发的效果" fieldPath={`/payload/${name}/${index}/effect_ids`} />
          </Form.Item>
        </Col>
        <Col xs={24} md={8}>
          <Form.Item name={[field.name, 'termination_budget']} label="最多触发次数（可选）" tooltip={helpTooltip(fieldHelp.terminationBudget)} rules={[nonNegativeIntegerRule]} extra={fieldExample(showExamples, '3；留空表示不单独限制')}>
            <Input placeholder="请输入非负整数" data-field-path={`/payload/${name}/${index}/termination_budget`} />
          </Form.Item>
        </Col>
      </Row>
    </Card>}
  </PayloadList>
}

function AttributeFields({ showExamples, dslUnits = [] }: Pick<PayloadFormProps, 'showExamples' | 'dslUnits'>) {
  const form = Form.useFormInstance()
  const valueType = Form.useWatch(['payload', 'value_type'], form) ?? 'decimal'
  const dimension = Form.useWatch(['payload', 'dimension'], form) ?? ''
  const baseUnit = Form.useWatch(['payload', 'base_unit'], form) ?? ''
  const compatibleUnits = dslUnits.filter(unit => valueType === 'boolean' || unit.value_type === valueType)
  const dimensionValues = new Set(compatibleUnits.map(unit => unit.dimension))
  if (dimension) dimensionValues.add(dimension)
  const dimensionOptions = [...dimensionValues].sort().map(value => ({ value }))
  const baseUnitValues = new Map<string, string>()
  for (const unit of compatibleUnits) {
    if (!dimension || unit.dimension === dimension) baseUnitValues.set(unit.base, unit.dimension)
  }
  if (baseUnit) baseUnitValues.set(baseUnit, dimension)
  const baseUnitOptions = [...baseUnitValues].sort(([left], [right]) => left.localeCompare(right)).map(([value, unitDimension]) => ({
    value,
    label: unitDimension ? `${value}（${unitDimension}）` : value,
  }))
  return <Row gutter={16}>
    <Col xs={24} md={8}>
      <Form.Item name={['payload', 'value_type']} label="数值类型" tooltip={helpTooltip(fieldHelp.valueType)} rules={[{ required: true, message: '请选择数值类型' }]} extra={fieldExample(showExamples, '伤害选“小数”，层数选“整数”，开关选“是 / 否”')}>
        <Select
          options={valueTypeOptions}
          data-field-path="/payload/value_type"
          onChange={next => {
            const current = form.getFieldValue(['payload', 'default'])
            if (next === 'boolean' && typeof current !== 'boolean') form.setFieldValue(['payload', 'default'], false)
            if (next !== 'boolean' && typeof current === 'boolean') form.setFieldValue(['payload', 'default'], '0')
            const currentUnit = dslUnits.find(unit => unit.base === form.getFieldValue(['payload', 'base_unit']))
            if (currentUnit && currentUnit.value_type !== next) form.setFieldValue(['payload', 'base_unit'], '')
          }}
        />
      </Form.Item>
    </Col>
    <Col xs={24} md={8}>
      <Form.Item name={['payload', 'dimension']} label="量纲" tooltip={helpTooltip(fieldHelp.dimension)} rules={[{ required: true, message: '请输入量纲' }]} extra={fieldExample(showExamples, 'damage；同一量纲的数值才能直接加减')}>
        <AutoComplete
          allowClear
          options={dimensionOptions}
          suffixIcon={<DownOutlined />}
          placeholder="请选择量纲，也可输入新量纲"
          filterOption={filterAutocompleteOption}
          notFoundContent="没有匹配的量纲，可直接输入"
          onSelect={next => {
            const currentUnit = dslUnits.find(unit => unit.base === form.getFieldValue(['payload', 'base_unit']))
            if (currentUnit && currentUnit.dimension !== next) form.setFieldValue(['payload', 'base_unit'], '')
          }}
          data-field-path="/payload/dimension"
        />
      </Form.Item>
    </Col>
    <Col xs={24} md={8}>
      <Form.Item name={['payload', 'base_unit']} label="基础单位" tooltip={helpTooltip(fieldHelp.baseUnit)} rules={[{ required: true, message: '请输入基础单位' }]} extra={fieldExample(showExamples, 'point；百分比可填写 ratio')}>
        <AutoComplete
          allowClear
          options={baseUnitOptions}
          suffixIcon={<DownOutlined />}
          placeholder={dimension ? '请选择与量纲匹配的基础单位' : '请选择基础单位（选择量纲后自动筛选）'}
          filterOption={filterAutocompleteOption}
          notFoundContent="没有匹配的基础单位，可直接输入"
          data-field-path="/payload/base_unit"
        />
      </Form.Item>
    </Col>
    <Col xs={24} md={8}>
      <Form.Item name={['payload', 'default']} label="默认值" tooltip={helpTooltip(fieldHelp.defaultValue)} rules={[{ required: true, message: '请填写默认值' }, ...(valueType === 'boolean' ? [] : [decimalRule])]} extra={fieldExample(showExamples, valueType === 'boolean' ? '否' : '100')}>
        {valueType === 'boolean'
          ? <Select options={[{ value: true, label: '是' }, { value: false, label: '否' }]} data-field-path="/payload/default" />
          : <Input placeholder="例如 100" data-field-path="/payload/default" />}
      </Form.Item>
    </Col>
    {valueType !== 'boolean' && <>
      <Col xs={24} md={8}>
        <Form.Item name={['payload', 'min']} label="最小值（可选）" tooltip={helpTooltip(fieldHelp.minValue)} rules={[decimalRule]} extra={fieldExample(showExamples, '0')}>
          <Input placeholder="例如 0" data-field-path="/payload/min" />
        </Form.Item>
      </Col>
      <Col xs={24} md={8}>
        <Form.Item name={['payload', 'max']} label="最大值（可选）" tooltip={helpTooltip(fieldHelp.maxValue)} rules={[decimalRule]} extra={fieldExample(showExamples, '9999')}>
          <Input placeholder="例如 9999" data-field-path="/payload/max" />
        </Form.Item>
      </Col>
      <Col xs={24} md={8}>
        <Form.Item name={['payload', 'display_scale']} label="显示倍率（可选）" tooltip={helpTooltip(fieldHelp.displayScale)} rules={[decimalRule]} extra={fieldExample(showExamples, '1；把 0.25 显示成 25% 时填写 100')}>
          <Input placeholder="例如 1" data-field-path="/payload/display_scale" />
        </Form.Item>
      </Col>
    </>}
  </Row>
}

function TagFields({ catalogs, catalogsLoading, showExamples }: PayloadFormProps) {
  return <Row gutter={16}>
    <Col xs={24} md={12}>
      <Form.Item name={['payload', 'category']} label="分类" tooltip={helpTooltip(fieldHelp.tagCategory)} rules={[{ required: true, message: '请输入分类' }]} extra={fieldExample(showExamples, 'element、faction 或 gameplay')}>
        <Input placeholder="例如 element" data-field-path="/payload/category" />
      </Form.Item>
    </Col>
    <Col xs={24} md={12}>
      <Form.Item name={['payload', 'parent_tag_ids']} label="父标签" tooltip={helpTooltip(fieldHelp.parentTags)} extra={fieldExample(showExamples, '“火焰”的父标签可选择“元素”')}>
        <ReferenceSelect multiple entityKind="tag" catalogs={catalogs} loading={catalogsLoading} placeholder="请选择父标签，可留空" emptyText="暂无父标签，可留空创建根标签" fieldPath="/payload/parent_tag_ids" />
      </Form.Item>
    </Col>
  </Row>
}

function CharacterFields(props: PayloadFormProps) {
  return <>
    <Row gutter={16}>
      <Col xs={24} md={12}>
        <Form.Item name={['payload', 'skill_ids']} label="拥有的技能" tooltip={helpTooltip(fieldHelp.characterSkills)} extra={fieldExample(props.showExamples, '普通攻击、火球术')}>
          <ReferenceSelect multiple entityKind="skill" catalogs={props.catalogs} loading={props.catalogsLoading} placeholder="请选择角色拥有的技能" fieldPath="/payload/skill_ids" />
        </Form.Item>
      </Col>
      <Col xs={24} md={12}>
        <Form.Item name={['payload', 'item_ids']} label="携带的物品" tooltip={helpTooltip(fieldHelp.characterItems)} extra={fieldExample(props.showExamples, '训练木剑、治疗药水')}>
          <ReferenceSelect multiple entityKind="item" catalogs={props.catalogs} loading={props.catalogsLoading} placeholder="请选择角色携带的物品" fieldPath="/payload/item_ids" />
        </Form.Item>
      </Col>
    </Row>
    <FormulaBindings name="attribute_values" title="属性值" example="选择“生命值”，填写 80[health_point]；也可从下拉中选择函数或属性引用。" {...props} loading={props.catalogsLoading} />
    <TriggerRules name="rule_blocks" title="触发规则" {...props} loading={props.catalogsLoading} />
  </>
}

function SkillFields(props: PayloadFormProps) {
  return <>
    <Row gutter={16}>
      <Col xs={24} md={8}>
        <Form.Item name={['payload', 'cooldown']} label="冷却时间" tooltip={helpTooltip(fieldHelp.cooldown)} rules={[{ required: true, message: '请输入冷却时间' }, nonNegativeIntegerRule]} extra={fieldExample(props.showExamples, '3；填写 0 表示没有冷却')}>
          <Input placeholder="例如 3" data-field-path="/payload/cooldown" />
        </Form.Item>
      </Col>
      <Col xs={24} md={16}>
        <Form.Item name={['payload', 'effect_ids']} label="直接效果" tooltip={helpTooltip(fieldHelp.directEffects)} extra={fieldExample(props.showExamples, '火焰伤害、燃烧')}>
          <ReferenceSelect multiple entityKind="effect" catalogs={props.catalogs} loading={props.catalogsLoading} placeholder="请选择技能直接产生的效果" fieldPath="/payload/effect_ids" />
        </Form.Item>
      </Col>
    </Row>
    <Card size="small" className="payload-object-card" title="默认目标">
      <Typography.Paragraph type="secondary">选择技能默认作用的目标。</Typography.Paragraph>
      <Row gutter={16}>
        <TargetSelectorFields itemName={['payload', 'target_selector']} watchName={['payload', 'target_selector']} path="/payload/target_selector" catalogs={props.catalogs} loading={props.catalogsLoading} showExamples={props.showExamples} />
      </Row>
    </Card>
    <FormulaBindings name="costs" title="资源消耗" example="选择“法力值”，数值或公式填写 20[resource_point]。" {...props} loading={props.catalogsLoading} />
    <TriggerRules name="rule_blocks" title="触发规则" {...props} loading={props.catalogsLoading} />
  </>
}

function ItemFields(props: PayloadFormProps) {
  return <>
    <Row gutter={16}>
      <Col xs={24} md={8}>
        <Form.Item name={['payload', 'slot']} label="装备部位" tooltip={helpTooltip(fieldHelp.itemSlot)} rules={[{ required: true, message: '请输入装备部位' }]} extra={fieldExample(props.showExamples, 'weapon、armor 或 accessory')}>
          <Input placeholder="例如 weapon" data-field-path="/payload/slot" />
        </Form.Item>
      </Col>
      <Col xs={24} md={16}>
        <Form.Item name={['payload', 'effect_ids']} label="附带效果" tooltip={helpTooltip(fieldHelp.itemEffects)} extra={fieldExample(props.showExamples, '吸血、护盾')}>
          <ReferenceSelect multiple entityKind="effect" catalogs={props.catalogs} loading={props.catalogsLoading} placeholder="请选择物品附带的效果" fieldPath="/payload/effect_ids" />
        </Form.Item>
      </Col>
      <Col xs={24}>
        <Form.Item name={['payload', 'enhance_tag_ids']} label="强化标签" tooltip={helpTooltip(fieldHelp.enhanceTags)} extra={fieldExample(props.showExamples, '火焰强化、近战强化')}>
          <ReferenceSelect multiple entityKind="tag" catalogs={props.catalogs} loading={props.catalogsLoading} placeholder="请选择被该物品强化的标签" fieldPath="/payload/enhance_tag_ids" />
        </Form.Item>
      </Col>
    </Row>
    <Modifiers name="attribute_modifiers" title="属性修改" {...props} loading={props.catalogsLoading} />
    <TriggerRules name="rule_blocks" title="触发规则" {...props} loading={props.catalogsLoading} />
  </>
}

function EffectFields(props: PayloadFormProps) {
  return <>
    <Row gutter={16}>
      <Col xs={24} md={8}>
        <Form.Item name={['payload', 'duration']} label="持续时间" tooltip={helpTooltip(fieldHelp.duration)} rules={[{ required: true, message: '请输入持续时间' }, nonNegativeIntegerRule]} extra={fieldExample(props.showExamples, '5；填写 0 表示立即结算')}>
          <Input placeholder="例如 5" data-field-path="/payload/duration" />
        </Form.Item>
      </Col>
    </Row>
    <Card size="small" className="payload-object-card" title="叠加规则">
      <Typography.Paragraph type="secondary">设置重复获得该效果时的计算与刷新方式。</Typography.Paragraph>
      <Row gutter={16}>
        <Col xs={24} md={8}>
          <Form.Item name={['payload', 'stack_rule', 'operation']} label="叠加方式" tooltip={helpTooltip(fieldHelp.stackOperation)} rules={[{ required: true, message: '请选择叠加方式' }]} extra={fieldExample(props.showExamples, '增加（Add）')}>
            <Select options={operationOptions} data-field-path="/payload/stack_rule/operation" />
          </Form.Item>
        </Col>
        <Col xs={24} md={8}>
          <Form.Item name={['payload', 'stack_rule', 'max_stacks']} label="最大层数" tooltip={helpTooltip(fieldHelp.maxStacks)} rules={[{ required: true, message: '请输入最大层数' }, nonNegativeIntegerRule]} extra={fieldExample(props.showExamples, '3')}>
            <Input placeholder="例如 3" data-field-path="/payload/stack_rule/max_stacks" />
          </Form.Item>
        </Col>
        <Col xs={24} md={8}>
          <Form.Item name={['payload', 'stack_rule', 'refresh_policy']} label="重复获得时" tooltip={helpTooltip(fieldHelp.refreshPolicy)} rules={[{ required: true, message: '请选择刷新策略' }]} extra={fieldExample(props.showExamples, '刷新持续时间')}>
            <Select options={refreshPolicyOptions} data-field-path="/payload/stack_rule/refresh_policy" />
          </Form.Item>
        </Col>
        <Col xs={24} md={8}>
          <Form.Item name={['payload', 'stack_rule', 'priority']} label="优先级（可选）" tooltip={helpTooltip(fieldHelp.stackPriority)} rules={[integerRule]} extra={fieldExample(props.showExamples, '10；数值越大可代表优先级越高')}>
            <Input placeholder="例如 10" data-field-path="/payload/stack_rule/priority" />
          </Form.Item>
        </Col>
        <Col xs={24} md={8}>
          <Form.Item name={['payload', 'stack_rule', 'cap']} label="数值上限（可选）" tooltip={helpTooltip(fieldHelp.stackCap)} rules={[decimalRule]} extra={fieldExample(props.showExamples, '100')}>
            <Input placeholder="例如 100" data-field-path="/payload/stack_rule/cap" />
          </Form.Item>
        </Col>
      </Row>
    </Card>
    <Modifiers name="modifiers" title="属性修改" {...props} loading={props.catalogsLoading} />
    <TriggerRules name="trigger_blocks" title="触发规则" {...props} loading={props.catalogsLoading} />
  </>
}

export default function PayloadForm(props: PayloadFormProps) {
  return <div className="payload-form">
    {props.showExamples && <Alert className="payload-example-alert" type="info" showIcon title={`${payloadExamples[props.kind]}`} />}
    {props.kind === 'attribute' && <AttributeFields showExamples={props.showExamples} dslUnits={props.dslUnits} />}
    {props.kind === 'tag' && <TagFields {...props} />}
    {props.kind === 'character' && <CharacterFields {...props} />}
    {props.kind === 'skill' && <SkillFields {...props} />}
    {props.kind === 'item' && <ItemFields {...props} />}
    {props.kind === 'effect' && <EffectFields {...props} />}
    <Space className="payload-form-note" size="small">
      <Typography.Text type="secondary">系统会按当前表单自动生成配置数据（Payload），无需编写 JSON。</Typography.Text>
    </Space>
  </div>
}
