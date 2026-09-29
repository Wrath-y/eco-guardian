import { describe, expect, it } from 'vitest'
import { evaluateExcelExpression, ExcelFormulaError } from '../src/tfd/excelExpression'
import { TFDCalculator, workbookFromSourceTag, type TFDEntity, type WorkbookModel } from '../src/tfd/calculator'

describe('TFD workbook calculator', () => {
  it('evaluates the workbook arithmetic and conditional forms', () => {
    const cells: Record<string, number | string> = { A1: 8, B1: 2, C1: 4, A2: 6 }
    const read = (cell: string) => cells[cell] ?? null
    expect(evaluateExcelExpression('=IF(A1>5,ROUNDUP(A1/B1,1),0)', read)).toBe(4)
    expect(evaluateExcelExpression('=SUM(A1:B1,C1:C1,A2:A2)', read)).toBe(20)
    expect(evaluateExcelExpression('=3.14*A1^2+10%', read)).toBeCloseTo(201.06)
    expect(evaluateExcelExpression('=_xlfn.SWITCH(INT(A1/5),0,"A",1,"B","")', read)).toBe('B')
    expect(evaluateExcelExpression('=TEXT(TRUNC(0.156,2),"0.0%")&"血"', read)).toBe('15.0%血')
    expect(() => evaluateExcelExpression('=A1+#REF!', read)).toThrow(ExcelFormulaError)
  })

  it('recalculates derived values when project item modifiers change', () => {
    const workbook: WorkbookModel = { sha256: 'source', formula_model_version: 1, sheets: [
      { name: '配装公式', cells: { A2: '体力', B2: 'B', C2: '体力[+]', D2: '体力[+]', E2: '体力[*]' } },
      { name: '测试角色', cells: { A1: '测试角色', B2: '体力', B3: { formula: '=_xlfn.LET(配装公式!$A:$E)', cached: 156 }, B4: { formula: '=B3*2', cached: 312 } } },
    ] }
    const entities: TFDEntity[] = [
      { id: 'base', kind: 'attribute', key: 'base', name: '体力[+]', payload: {} },
      { id: 'mult', kind: 'attribute', key: 'mult', name: '体力[*]', payload: {} },
      { id: 'item', kind: 'item', key: 'item', name: '体力模块', payload: { attribute_modifiers: [
        { attribute_id: 'base', operation: 'Add', value: '20' },
        { attribute_id: 'mult', operation: 'Multiply', value: '1.3' },
      ] } },
      { id: 'character', kind: 'character', key: 'tfd_hero_test', name: '测试角色', payload: { attribute_values: [{ output_attribute_id: 'base', expression: '100[health_point]' }], item_ids: ['item'] } },
    ]
    const source: TFDEntity = { id: 'source', kind: 'tag', key: 'tfd_source', name: '来源', payload: {}, extensions: { 'tfd/workbook': workbook } }
    const first = new TFDCalculator(workbookFromSourceTag(source), entities)
    expect(first.readCell('测试角色', 'B3')).toBe(156)
    expect(first.readCell('测试角色', 'B4')).toBe(312)
    expect(first.evaluateSheet('测试角色').every(cell => cell.status === 'matched')).toBe(true)
    const revised = structuredClone(entities)
    const modifiers = revised[2].payload.attribute_modifiers as Array<{ value: string }>
    modifiers[0].value = '30'
    const second = new TFDCalculator(workbook, revised)
    expect(second.readCell('测试角色', 'B3')).toBe(169)
    expect(second.readCell('测试角色', 'B4')).toBe(338)
    expect(second.evaluateSheet('测试角色').every(cell => cell.status === 'changed')).toBe(true)
  })
})
