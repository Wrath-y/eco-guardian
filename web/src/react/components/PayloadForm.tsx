import type { ReactNode } from 'react'
import { Alert, Button, Card, Col, Empty, Form, Input, Row, Select, Space, Typography, type FormInstance, type SelectProps } from 'antd'
import { DeleteOutlined, PlusOutlined } from '@ant-design/icons'
import type { EntityKind } from '@/forms/registry'

export interface EntityOption {
  id: string
  key: string
  name: string
  entity_version: number
}

export type EntityCatalogs = Partial<Record<EntityKind, EntityOption[]>>

interface PayloadFormProps {
  kind: EntityKind
  form: FormInstance
  catalogs?: EntityCatalogs
  catalogsLoading?: boolean
  showExamples?: boolean
}

type PathPart = string | number

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
      <Form.Item name={[...itemName, 'type']} label="目标类型" rules={[{ required: true, message: '请选择目标类型' }]} extra={fieldExample(showExamples, '对单体技能选择“主要目标”')}>
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
  name, title, example, catalogs, loading, showExamples,
}: {
  name: 'attribute_values' | 'costs'
  title: string
  example: string
  catalogs?: EntityCatalogs
  loading?: boolean
  showExamples?: boolean
}) {
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
          <Form.Item name={[field.name, 'output_attribute_id']} label="属性" rules={[{ required: true, message: '请选择属性' }]} extra={fieldExample(showExamples, name === 'costs' ? '法力值' : '生命值')}>
            <ReferenceSelect entityKind="attribute" catalogs={catalogs} loading={loading} placeholder="请选择属性" fieldPath={`/payload/${name}/${index}/output_attribute_id`} />
          </Form.Item>
        </Col>
        <Col xs={24} md={14}>
          <Form.Item name={[field.name, 'expression']} label="数值或公式" rules={[{ required: true, message: '请输入数值或公式' }]} extra={fieldExample(showExamples, name === 'costs' ? '20' : '100 + ${scenario:level} * 10')}>
            <Input placeholder="可填写数字或公式" data-field-path={`/payload/${name}/${index}/expression`} />
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
          <Form.Item name={[field.name, 'attribute_id']} label="属性" rules={[{ required: true, message: '请选择属性' }]} extra={fieldExample(showExamples, '攻击力')}>
            <ReferenceSelect entityKind="attribute" catalogs={catalogs} loading={loading} placeholder="请选择要修改的属性" fieldPath={`/payload/${name}/${index}/attribute_id`} />
          </Form.Item>
        </Col>
        <Col xs={24} md={7}>
          <Form.Item name={[field.name, 'operation']} label="计算方式" rules={[{ required: true, message: '请选择计算方式' }]} extra={fieldExample(showExamples, '增加（Add）')}>
            <Select options={operationOptions} placeholder="请选择计算方式" data-field-path={`/payload/${name}/${index}/operation`} />
          </Form.Item>
        </Col>
        <Col xs={24} md={8}>
          <Form.Item name={[field.name, 'value']} label="数值" rules={[{ required: true, message: '请输入数值' }, decimalRule]} extra={fieldExample(showExamples, '10；减少 8 可填写 -8')}>
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
          <Form.Item name={[field.name, 'event']} label="触发时机" rules={[{ required: true, message: '请选择触发时机' }]} extra={fieldExample(showExamples, '命中时')}>
            <Select options={eventOptions} placeholder="请选择触发时机" data-field-path={`/payload/${name}/${index}/event`} />
          </Form.Item>
        </Col>
        <Col xs={24} md={16}>
          <Form.Item name={[field.name, 'condition']} label="触发条件（可选）" extra={fieldExample(showExamples, '${self:critical_rate} > 0.5；留空表示总是触发')}>
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
          <Form.Item name={[field.name, 'effect_ids']} label="触发效果（可选）" extra={fieldExample(showExamples, '燃烧、减速')}>
            <ReferenceSelect multiple entityKind="effect" catalogs={catalogs} loading={loading} placeholder="请选择要触发的效果" fieldPath={`/payload/${name}/${index}/effect_ids`} />
          </Form.Item>
        </Col>
        <Col xs={24} md={8}>
          <Form.Item name={[field.name, 'termination_budget']} label="最多触发次数（可选）" rules={[nonNegativeIntegerRule]} extra={fieldExample(showExamples, '3；留空表示不单独限制')}>
            <Input placeholder="请输入非负整数" data-field-path={`/payload/${name}/${index}/termination_budget`} />
          </Form.Item>
        </Col>
      </Row>
    </Card>}
  </PayloadList>
}

function AttributeFields({ showExamples }: Pick<PayloadFormProps, 'showExamples'>) {
  const form = Form.useFormInstance()
  const valueType = Form.useWatch(['payload', 'value_type'], form) ?? 'decimal'
  return <Row gutter={16}>
    <Col xs={24} md={8}>
      <Form.Item name={['payload', 'value_type']} label="数值类型" rules={[{ required: true, message: '请选择数值类型' }]} extra={fieldExample(showExamples, '伤害选“小数”，层数选“整数”，开关选“是 / 否”')}>
        <Select
          options={valueTypeOptions}
          data-field-path="/payload/value_type"
          onChange={next => {
            const current = form.getFieldValue(['payload', 'default'])
            if (next === 'boolean' && typeof current !== 'boolean') form.setFieldValue(['payload', 'default'], false)
            if (next !== 'boolean' && typeof current === 'boolean') form.setFieldValue(['payload', 'default'], '0')
          }}
        />
      </Form.Item>
    </Col>
    <Col xs={24} md={8}>
      <Form.Item name={['payload', 'dimension']} label="量纲" rules={[{ required: true, message: '请输入量纲' }]} extra={fieldExample(showExamples, 'damage；同一量纲的数值才能直接加减')}>
        <Input placeholder="例如 damage" data-field-path="/payload/dimension" />
      </Form.Item>
    </Col>
    <Col xs={24} md={8}>
      <Form.Item name={['payload', 'base_unit']} label="基础单位" rules={[{ required: true, message: '请输入基础单位' }]} extra={fieldExample(showExamples, 'point；百分比可填写 ratio')}>
        <Input placeholder="例如 point" data-field-path="/payload/base_unit" />
      </Form.Item>
    </Col>
    <Col xs={24} md={8}>
      <Form.Item name={['payload', 'default']} label="默认值" rules={[{ required: true, message: '请填写默认值' }, ...(valueType === 'boolean' ? [] : [decimalRule])]} extra={fieldExample(showExamples, valueType === 'boolean' ? '否' : '100')}>
        {valueType === 'boolean'
          ? <Select options={[{ value: true, label: '是' }, { value: false, label: '否' }]} data-field-path="/payload/default" />
          : <Input placeholder="例如 100" data-field-path="/payload/default" />}
      </Form.Item>
    </Col>
    {valueType !== 'boolean' && <>
      <Col xs={24} md={8}>
        <Form.Item name={['payload', 'min']} label="最小值（可选）" rules={[decimalRule]} extra={fieldExample(showExamples, '0')}>
          <Input placeholder="例如 0" data-field-path="/payload/min" />
        </Form.Item>
      </Col>
      <Col xs={24} md={8}>
        <Form.Item name={['payload', 'max']} label="最大值（可选）" rules={[decimalRule]} extra={fieldExample(showExamples, '9999')}>
          <Input placeholder="例如 9999" data-field-path="/payload/max" />
        </Form.Item>
      </Col>
      <Col xs={24} md={8}>
        <Form.Item name={['payload', 'display_scale']} label="显示倍率（可选）" rules={[decimalRule]} extra={fieldExample(showExamples, '1；把 0.25 显示成 25% 时填写 100')}>
          <Input placeholder="例如 1" data-field-path="/payload/display_scale" />
        </Form.Item>
      </Col>
    </>}
  </Row>
}

function TagFields({ catalogs, catalogsLoading, showExamples }: PayloadFormProps) {
  return <Row gutter={16}>
    <Col xs={24} md={12}>
      <Form.Item name={['payload', 'category']} label="分类" rules={[{ required: true, message: '请输入分类' }]} extra={fieldExample(showExamples, 'element、faction 或 gameplay')}>
        <Input placeholder="例如 element" data-field-path="/payload/category" />
      </Form.Item>
    </Col>
    <Col xs={24} md={12}>
      <Form.Item name={['payload', 'parent_tag_ids']} label="父标签" extra={fieldExample(showExamples, '“火焰”的父标签可选择“元素”')}>
        <ReferenceSelect multiple entityKind="tag" catalogs={catalogs} loading={catalogsLoading} placeholder="请选择父标签，可留空" emptyText="暂无父标签，可留空创建根标签" fieldPath="/payload/parent_tag_ids" />
      </Form.Item>
    </Col>
  </Row>
}

function CharacterFields(props: PayloadFormProps) {
  return <>
    <Row gutter={16}>
      <Col xs={24} md={12}>
        <Form.Item name={['payload', 'skill_ids']} label="拥有的技能" extra={fieldExample(props.showExamples, '普通攻击、火球术')}>
          <ReferenceSelect multiple entityKind="skill" catalogs={props.catalogs} loading={props.catalogsLoading} placeholder="请选择角色拥有的技能" fieldPath="/payload/skill_ids" />
        </Form.Item>
      </Col>
      <Col xs={24} md={12}>
        <Form.Item name={['payload', 'item_ids']} label="携带的物品" extra={fieldExample(props.showExamples, '训练木剑、治疗药水')}>
          <ReferenceSelect multiple entityKind="item" catalogs={props.catalogs} loading={props.catalogsLoading} placeholder="请选择角色携带的物品" fieldPath="/payload/item_ids" />
        </Form.Item>
      </Col>
    </Row>
    <FormulaBindings name="attribute_values" title="属性值" example="选择“生命值”，数值或公式填写 100 + ${scenario:level} * 10。" {...props} loading={props.catalogsLoading} />
    <TriggerRules name="rule_blocks" title="触发规则" {...props} loading={props.catalogsLoading} />
  </>
}

function SkillFields(props: PayloadFormProps) {
  return <>
    <Row gutter={16}>
      <Col xs={24} md={8}>
        <Form.Item name={['payload', 'cooldown']} label="冷却时间" rules={[{ required: true, message: '请输入冷却时间' }, nonNegativeIntegerRule]} extra={fieldExample(props.showExamples, '3；填写 0 表示没有冷却')}>
          <Input placeholder="例如 3" data-field-path="/payload/cooldown" />
        </Form.Item>
      </Col>
      <Col xs={24} md={16}>
        <Form.Item name={['payload', 'effect_ids']} label="直接效果" extra={fieldExample(props.showExamples, '火焰伤害、燃烧')}>
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
    <FormulaBindings name="costs" title="资源消耗" example="选择“法力值”，数值或公式填写 20。" {...props} loading={props.catalogsLoading} />
    <TriggerRules name="rule_blocks" title="触发规则" {...props} loading={props.catalogsLoading} />
  </>
}

function ItemFields(props: PayloadFormProps) {
  return <>
    <Row gutter={16}>
      <Col xs={24} md={8}>
        <Form.Item name={['payload', 'slot']} label="装备部位" rules={[{ required: true, message: '请输入装备部位' }]} extra={fieldExample(props.showExamples, 'weapon、armor 或 accessory')}>
          <Input placeholder="例如 weapon" data-field-path="/payload/slot" />
        </Form.Item>
      </Col>
      <Col xs={24} md={16}>
        <Form.Item name={['payload', 'effect_ids']} label="附带效果" extra={fieldExample(props.showExamples, '吸血、护盾')}>
          <ReferenceSelect multiple entityKind="effect" catalogs={props.catalogs} loading={props.catalogsLoading} placeholder="请选择物品附带的效果" fieldPath="/payload/effect_ids" />
        </Form.Item>
      </Col>
      <Col xs={24}>
        <Form.Item name={['payload', 'enhance_tag_ids']} label="强化标签" extra={fieldExample(props.showExamples, '火焰强化、近战强化')}>
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
        <Form.Item name={['payload', 'duration']} label="持续时间" rules={[{ required: true, message: '请输入持续时间' }, nonNegativeIntegerRule]} extra={fieldExample(props.showExamples, '5；填写 0 表示立即结算')}>
          <Input placeholder="例如 5" data-field-path="/payload/duration" />
        </Form.Item>
      </Col>
    </Row>
    <Card size="small" className="payload-object-card" title="叠加规则">
      <Typography.Paragraph type="secondary">设置重复获得该效果时的计算与刷新方式。</Typography.Paragraph>
      <Row gutter={16}>
        <Col xs={24} md={8}>
          <Form.Item name={['payload', 'stack_rule', 'operation']} label="叠加方式" rules={[{ required: true, message: '请选择叠加方式' }]} extra={fieldExample(props.showExamples, '增加（Add）')}>
            <Select options={operationOptions} data-field-path="/payload/stack_rule/operation" />
          </Form.Item>
        </Col>
        <Col xs={24} md={8}>
          <Form.Item name={['payload', 'stack_rule', 'max_stacks']} label="最大层数" rules={[{ required: true, message: '请输入最大层数' }, nonNegativeIntegerRule]} extra={fieldExample(props.showExamples, '3')}>
            <Input placeholder="例如 3" data-field-path="/payload/stack_rule/max_stacks" />
          </Form.Item>
        </Col>
        <Col xs={24} md={8}>
          <Form.Item name={['payload', 'stack_rule', 'refresh_policy']} label="重复获得时" rules={[{ required: true, message: '请选择刷新策略' }]} extra={fieldExample(props.showExamples, '刷新持续时间')}>
            <Select options={refreshPolicyOptions} data-field-path="/payload/stack_rule/refresh_policy" />
          </Form.Item>
        </Col>
        <Col xs={24} md={8}>
          <Form.Item name={['payload', 'stack_rule', 'priority']} label="优先级（可选）" rules={[integerRule]} extra={fieldExample(props.showExamples, '10；数值越大可代表优先级越高')}>
            <Input placeholder="例如 10" data-field-path="/payload/stack_rule/priority" />
          </Form.Item>
        </Col>
        <Col xs={24} md={8}>
          <Form.Item name={['payload', 'stack_rule', 'cap']} label="数值上限（可选）" rules={[decimalRule]} extra={fieldExample(props.showExamples, '100')}>
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
    {props.kind === 'attribute' && <AttributeFields showExamples={props.showExamples} />}
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
