import { evaluateExcelExpression, ExcelFormulaError, type ExcelValue } from './excelExpression'

// The workbook's repeated LET lookups are resolved against current project
// entities. The remaining cell formulas run through the bounded Excel subset.

export interface TFDEntity {
  id: string
  kind: string
  key: string
  name: string
  payload: Record<string, unknown>
  extensions?: Record<string, unknown>
}

export interface WorkbookSheet { name: string; cells: Record<string, unknown> }
export interface WorkbookModel { sha256: string; sheets: WorkbookSheet[]; formula_model_version?: number }
export interface CalculationCell { address: string; label: string; formula: string; value: ExcelValue; cached: ExcelValue; status: 'matched' | 'changed' | 'source_error' | 'error'; message?: string }

interface FormulaCell { formula: string; cached: ExcelValue }
interface Rule { type: string; base: string; add: string; multiply: string }

function formulaCell(value: unknown): value is FormulaCell {
  return typeof value === 'object' && value !== null && 'formula' in value && typeof (value as FormulaCell).formula === 'string'
}
function excelValue(value: unknown): ExcelValue {
  if (value === null || typeof value === 'string' || typeof value === 'number' || typeof value === 'boolean') return value
  return null
}
function decimalLiteral(expression: unknown): number {
  if (typeof expression !== 'string') throw new ExcelFormulaError('角色基础值缺少公式')
  const literal = /^(-?(?:\d+(?:\.\d*)?|\.\d+))(?:\[[a-z_]+\])?$/.exec(expression.trim())
  if (!literal) throw new ExcelFormulaError(`当前基础值不是可计算的数值常量：${expression}`)
  return Number(literal[1])
}
function cellAbove(address: string): string {
  const match = /^([A-Z]+)(\d+)$/.exec(address)
  if (!match || Number(match[2]) < 2) throw new ExcelFormulaError(`单元格 ${address} 上方没有字段名`)
  return `${match[1]}${Number(match[2]) - 1}`
}
function numberValue(value: ExcelValue): number {
  if (typeof value !== 'number' || !Number.isFinite(value)) throw new ExcelFormulaError(`需要数值，得到 ${String(value)}`)
  return value
}

export function workbookFromSourceTag(source: TFDEntity): WorkbookModel {
  const workbook = source.extensions?.['tfd/workbook']
  if (typeof workbook !== 'object' || workbook === null) throw new ExcelFormulaError('项目中没有 TFD 工作簿来源数据')
  const model = workbook as WorkbookModel
  if (!Array.isArray(model.sheets) || model.formula_model_version !== 1) throw new ExcelFormulaError('请先更新 TFD 工作簿公式模型')
  return model
}

export class TFDCalculator {
  private readonly sheets = new Map<string, WorkbookSheet>()
  private readonly entitiesByID = new Map<string, TFDEntity>()
  private readonly itemsByName = new Map<string, TFDEntity>()
  private readonly charactersByName = new Map<string, TFDEntity>()
  private readonly memo = new Map<string, ExcelValue>()
  private readonly pending = new Set<string>()
  private readonly rules = new Map<string, Rule>()

  constructor(readonly workbook: WorkbookModel, entities: TFDEntity[]) {
    for (const sheet of workbook.sheets) this.sheets.set(sheet.name, sheet)
    for (const entity of entities) {
      this.entitiesByID.set(entity.id, entity)
      if (entity.kind === 'item') this.itemsByName.set(entity.name, entity)
      if (entity.kind === 'character' && entity.key.startsWith('tfd_hero_')) this.charactersByName.set(entity.name, entity)
    }
    const formulaSheet = this.sheets.get('配装公式')
    if (!formulaSheet) throw new ExcelFormulaError('缺少「配装公式」工作表')
    for (let row = 2; row <= 47; row++) {
      const name = excelValue(formulaSheet.cells[`A${row}`])
      if (typeof name !== 'string' || !name) continue
      this.rules.set(name, {
        type: String(excelValue(formulaSheet.cells[`B${row}`]) ?? ''),
        base: String(excelValue(formulaSheet.cells[`C${row}`]) ?? ''),
        add: String(excelValue(formulaSheet.cells[`D${row}`]) ?? ''),
        multiply: String(excelValue(formulaSheet.cells[`E${row}`]) ?? ''),
      })
    }
  }

  characterNames(): string[] { return [...this.charactersByName.keys()].sort((a, b) => a.localeCompare(b, 'zh-CN')) }
  character(name: string): TFDEntity | undefined { return this.charactersByName.get(name) }
  hasSheet(name: string): boolean { return this.sheets.has(name) }

  private sheet(name: string): WorkbookSheet {
    const sheet = this.sheets.get(name)
    if (!sheet) throw new ExcelFormulaError(`缺少工作表「${name}」`)
    return sheet
  }

  readCell(sheetName: string, address: string): ExcelValue {
    const key = `${sheetName}!${address}`
    if (this.memo.has(key)) return this.memo.get(key)!
    if (this.pending.has(key)) throw new ExcelFormulaError(`公式循环引用：${key}`)
    const raw = this.sheet(sheetName).cells[address]
    if (!formulaCell(raw)) return excelValue(raw)
    this.pending.add(key)
    try {
      let value: ExcelValue
      if (raw.formula.includes('配装公式!$A:$E')) value = this.evaluateBuildFormula(sheetName, address, raw.formula)
      else if (raw.formula.includes('_xlpm.m_text')) value = this.evaluateAreaText(sheetName, address, raw.formula)
      else value = evaluateExcelExpression(raw.formula, cell => this.readCell(sheetName, cell))
      this.memo.set(key, value)
      return value
    } finally { this.pending.delete(key) }
  }

  private selectedItems(sheetName: string): TFDEntity[] {
    const character = this.charactersByName.get(sheetName)
    if (character) {
      const ids = character.payload.item_ids
      if (!Array.isArray(ids)) throw new ExcelFormulaError(`${sheetName} 的物品引用无效`)
      return ids.map(id => {
        const item = this.entitiesByID.get(String(id))
        if (!item || item.kind !== 'item') throw new ExcelFormulaError(`${sheetName} 引用了不存在的装备 ${String(id)}`)
        return item
      })
    }
    const sheet = this.sheet(sheetName)
    const selected: TFDEntity[] = []
    for (let row = 1; row <= 4; row++) for (const col of 'BCDEFGH') {
      const name = excelValue(sheet.cells[`${col}${row}`])
      const item = typeof name === 'string' ? this.itemsByName.get(name) : undefined
      if (item) selected.push(item)
    }
    return selected
  }

  private baseValue(characterName: string, attributeName: string): number {
    if (!attributeName) return 0
    const character = this.charactersByName.get(characterName)
    if (!character) throw new ExcelFormulaError(`项目中没有角色「${characterName}」`)
    const bindings = character.payload.attribute_values
    if (!Array.isArray(bindings)) throw new ExcelFormulaError(`${characterName} 的属性值无效`)
    for (const binding of bindings) {
      if (typeof binding !== 'object' || binding === null) continue
      const value = binding as { output_attribute_id?: string; expression?: string }
      if (this.entitiesByID.get(value.output_attribute_id ?? '')?.name === attributeName) return decimalLiteral(value.expression)
    }
    return 0
  }

  private itemBonus(item: TFDEntity, attributeName: string): number {
    if (!attributeName) return 0
    const modifiers = item.payload.attribute_modifiers
    if (!Array.isArray(modifiers)) return 0
    let sum = 0
    for (const modifier of modifiers) {
      if (typeof modifier !== 'object' || modifier === null) continue
      const value = modifier as { attribute_id?: string; operation?: string; value?: string }
      if (this.entitiesByID.get(value.attribute_id ?? '')?.name !== attributeName) continue
      const numeric = Number(value.value)
      if (!Number.isFinite(numeric)) throw new ExcelFormulaError(`装备「${item.name}」的${attributeName}数值无效`)
      // Imported [*] values are stored as factors (1 + workbook bonus).
      if (value.operation === 'Multiply') sum += numeric - 1
      else if (value.operation === 'Add') sum += numeric
      else throw new ExcelFormulaError(`装备「${item.name}」的运算 ${value.operation} 无法映射到 Excel 配装公式`)
    }
    return sum
  }

  private evaluateBuildFormula(sheetName: string, address: string, source: string): number {
    const label = this.readCell(sheetName, cellAbove(address))
    if (typeof label !== 'string') throw new ExcelFormulaError(`${sheetName}!${address} 缺少属性名称`)
    const rule = this.rules.get(label)
    if (!rule) throw new ExcelFormulaError(`配装公式没有「${label}」规则`)
    const characterName = this.readCell(sheetName, 'A1')
    if (typeof characterName !== 'string') throw new ExcelFormulaError(`${sheetName} 没有角色名称`)
    const base = this.baseValue(characterName, rule.base)
    const selected = this.selectedItems(sheetName)
    const add = selected.reduce((total, item) => total + this.itemBonus(item, rule.add), 0)
    const multiply = selected.reduce((total, item) => total + this.itemBonus(item, rule.multiply), 0)
    let result: number
    if (rule.type === 'A') result = base + add + multiply
    else if (rule.type === 'B') result = (base + add) * (1 + multiply)
    else throw new ExcelFormulaError(`未知配装规则类型 ${rule.type}`)
    const templateAt = source.indexOf('_xlfn.LET')
    const prefix = source.slice(0, templateAt).trim().replace(/\+\s*$/, '')
    if (prefix !== '=') result += numberValue(evaluateExcelExpression(prefix, cell => this.readCell(sheetName, cell)))
    if (!Number.isFinite(result)) throw new ExcelFormulaError(`${sheetName}!${address} 计算结果无效`)
    return result
  }

  private evaluateAreaText(sheetName: string, address: string, source: string): string {
    const match = /_xlpm\.m_text\s*,\s*([A-Z]+\d+)/.exec(source)
    if (!match) throw new ExcelFormulaError(`${sheetName}!${address} 范围文本缺少来源`)
    const sourceText = this.readCell(sheetName, match[1])
    if (typeof sourceText !== 'string') throw new ExcelFormulaError(`${sheetName}!${match[1]} 不是范围文本`)
    const factors = /^\s*(-?\d+(?:\.\d+)?)\s*\*\s*(-?\d+(?:\.\d+)?)\s*$/.exec(sourceText)
    if (!factors) throw new ExcelFormulaError(`${sheetName}!${match[1]} 范围格式无效`)
    const cap = sheetName === '维艾莎' ? 1.5 : 1
    const ratio = numberValue(this.readCell(sheetName, 'C7'))
    const scale = 1 + Math.min(ratio, cap)
    return `${(scale * Number(factors[1])).toFixed(1)}*${(scale * Number(factors[2])).toFixed(1)}`
  }

  evaluateSheet(sheetName: string): CalculationCell[] {
    const cells = this.sheet(sheetName).cells
    const results: CalculationCell[] = []
    for (const [address, raw] of Object.entries(cells)) {
      if (!formulaCell(raw)) continue
      const cached = excelValue(raw.cached)
      const row = /^([A-Z]+)(\d+)$/.exec(address)
      const label = row ? String(excelValue(cells[`A${row[2]}`]) ?? excelValue(cells[cellAbove(address)]) ?? '') : ''
      if (raw.formula.includes('#REF!') || cached === '#REF!') {
        results.push({ address, label, formula: raw.formula, value: null, cached, status: 'source_error', message: '原 Excel 引用已失效' })
        continue
      }
      try {
        const value = this.readCell(sheetName, address)
        const matched = typeof value === 'number' && typeof cached === 'number'
          ? Math.abs(value - cached) <= Math.max(1e-7, Math.abs(cached) * 1e-9)
          : value === cached
        results.push({ address, label, formula: raw.formula, value, cached, status: matched ? 'matched' : 'changed' })
      } catch (cause) {
        results.push({ address, label, formula: raw.formula, value: null, cached, status: 'error', message: cause instanceof Error ? cause.message : String(cause) })
      }
    }
    return results.sort((a, b) => {
      const left = /^([A-Z]+)(\d+)$/.exec(a.address)!
      const right = /^([A-Z]+)(\d+)$/.exec(b.address)!
      return Number(left[2]) - Number(right[2]) || left[1].localeCompare(right[1])
    })
  }
}
