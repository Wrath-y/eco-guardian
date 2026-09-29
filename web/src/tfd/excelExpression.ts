export type ExcelValue = number | string | boolean | null
export type FormulaValue = ExcelValue | ExcelValue[]

type Node =
  | { kind: 'literal'; value: ExcelValue }
  | { kind: 'reference'; cell: string }
  | { kind: 'range'; start: string; end: string }
  | { kind: 'unary'; op: string; right: Node }
  | { kind: 'postfix'; op: '%'; left: Node }
  | { kind: 'binary'; op: string; left: Node; right: Node }
  | { kind: 'call'; name: string; args: Node[] }

type Token = { kind: 'number' | 'string' | 'reference' | 'identifier' | 'operator' | 'end'; text: string }

export class ExcelFormulaError extends Error {
  constructor(message: string) { super(message); this.name = 'ExcelFormulaError' }
}

function tokenize(source: string): Token[] {
  const input = source.trim().replace(/^=/, '')
  const result: Token[] = []
  let at = 0
  while (at < input.length) {
    const rest = input.slice(at)
    const space = /^\s+/.exec(rest)
    if (space) { at += space[0].length; continue }
    if (rest.startsWith('#REF!')) throw new ExcelFormulaError('来源公式包含 #REF!')
    if (rest[0] === '"') {
      let end = 1
      let value = ''
      while (end < rest.length) {
        if (rest[end] === '"') {
          if (rest[end + 1] === '"') { value += '"'; end += 2; continue }
          break
        }
        value += rest[end++]
      }
      if (end >= rest.length) throw new ExcelFormulaError('字符串没有结束引号')
      result.push({ kind: 'string', text: value }); at += end + 1; continue
    }
    const reference = /^\$?[A-Za-z]{1,3}\$?\d+(?![A-Za-z_\d])/.exec(rest)
    if (reference) { result.push({ kind: 'reference', text: reference[0].replaceAll('$', '').toUpperCase() }); at += reference[0].length; continue }
    const number = /^(?:\d+(?:\.\d*)?|\.\d+)(?:[eE][+-]?\d+)?/.exec(rest)
    if (number) { result.push({ kind: 'number', text: number[0] }); at += number[0].length; continue }
    const identifier = /^[A-Za-z_][A-Za-z0-9_.]*/.exec(rest)
    if (identifier) { result.push({ kind: 'identifier', text: identifier[0].toUpperCase().replace(/^_XLFN\./, '') }); at += identifier[0].length; continue }
    const operator = /^(?:<=|>=|<>|[+\-*/^&%=<>()!,:])/.exec(rest)
    if (operator) { result.push({ kind: 'operator', text: operator[0] }); at += operator[0].length; continue }
    throw new ExcelFormulaError(`不支持的公式字符：${rest[0]}`)
  }
  result.push({ kind: 'end', text: '' })
  return result
}

const precedences: Record<string, number> = { ':': 80, '^': 60, '*': 50, '/': 50, '+': 40, '-': 40, '&': 35, '=': 30, '<>': 30, '<': 30, '<=': 30, '>': 30, '>=': 30 }

class Parser {
  private at = 0
  constructor(private readonly tokens: Token[]) {}
  private peek() { return this.tokens[this.at] }
  private take() { return this.tokens[this.at++] }
  private expect(text: string) { if (this.peek().text !== text) throw new ExcelFormulaError(`预期 ${text}，实际 ${this.peek().text || '结尾'}`); this.take() }

  parse(): Node {
    const node = this.expression(0)
    if (this.peek().kind !== 'end') throw new ExcelFormulaError(`公式有多余内容：${this.peek().text}`)
    return node
  }

  private expression(minimum: number): Node {
    let left = this.prefix()
    while (true) {
      const next = this.peek()
      if (next.text === '%' && 70 >= minimum) { this.take(); left = { kind: 'postfix', op: '%', left }; continue }
      const priority = precedences[next.text]
      if (priority === undefined || priority < minimum) break
      const op = this.take().text
      const right = this.expression(priority + (op === '^' ? 0 : 1))
      if (op === ':') {
        if (left.kind !== 'reference' || right.kind !== 'reference') throw new ExcelFormulaError('范围两端必须是单元格')
        left = { kind: 'range', start: left.cell, end: right.cell }
      } else left = { kind: 'binary', op, left, right }
    }
    return left
  }

  private prefix(): Node {
    const token = this.take()
    if (token.kind === 'number') return { kind: 'literal', value: Number(token.text) }
    if (token.kind === 'string') return { kind: 'literal', value: token.text }
    if (token.kind === 'reference') return { kind: 'reference', cell: token.text }
    if (token.text === '+' || token.text === '-') return { kind: 'unary', op: token.text, right: this.expression(55) }
    if (token.text === '(') { const node = this.expression(0); this.expect(')'); return node }
    if (token.kind === 'identifier') {
      if (token.text === 'TRUE') return { kind: 'literal', value: true }
      if (token.text === 'FALSE') return { kind: 'literal', value: false }
      this.expect('(')
      const args: Node[] = []
      while (this.peek().text !== ')') {
        args.push(this.expression(0))
        if (this.peek().text !== ',') break
        this.take()
      }
      this.expect(')')
      return { kind: 'call', name: token.text, args }
    }
    throw new ExcelFormulaError(`无法解析公式：${token.text || '结尾'}`)
  }
}

export function parseExcelExpression(source: string): Node { return new Parser(tokenize(source)).parse() }

function scalar(value: FormulaValue): ExcelValue {
  if (Array.isArray(value)) throw new ExcelFormulaError('此处不能使用单元格范围')
  return value
}
function numeric(value: FormulaValue): number {
  const item = scalar(value)
  if (item === null || item === '') return 0
  if (typeof item === 'boolean') return item ? 1 : 0
  const number = Number(item)
  if (!Number.isFinite(number)) throw new ExcelFormulaError(`无法转为数值：${String(item)}`)
  return number
}
function truthy(value: FormulaValue): boolean { return Boolean(scalar(value)) }
function excelEqual(left: ExcelValue, right: ExcelValue): boolean {
  if (typeof left === 'number' || typeof right === 'number') {
    try { return numeric(left) === numeric(right) } catch { return false }
  }
  return String(left ?? '').toLowerCase() === String(right ?? '').toLowerCase()
}
function compare(left: ExcelValue, right: ExcelValue): number {
  if (typeof left === 'number' || typeof right === 'number') return numeric(left) - numeric(right)
  return String(left ?? '').localeCompare(String(right ?? ''), 'en')
}

export function evaluateExcelExpression(source: string, readCell: (cell: string) => ExcelValue): ExcelValue {
  const root = parseExcelExpression(source)
  const evaluate = (node: Node): FormulaValue => {
    switch (node.kind) {
      case 'literal': return node.value
      case 'reference': return readCell(node.cell)
      case 'range': {
        const start = /^([A-Z]+)(\d+)$/.exec(node.start)
        const end = /^([A-Z]+)(\d+)$/.exec(node.end)
        if (!start || !end) throw new ExcelFormulaError('无效单元格范围')
        const column = (text: string) => [...text].reduce((n, letter) => n * 26 + letter.charCodeAt(0) - 64, 0)
        const label = (index: number) => { let text = ''; while (index > 0) { index--; text = String.fromCharCode(65 + index % 26) + text; index = Math.floor(index / 26) } return text }
        const values: ExcelValue[] = []
        for (let row = Number(start[2]); row <= Number(end[2]); row++) for (let col = column(start[1]); col <= column(end[1]); col++) values.push(readCell(`${label(col)}${row}`))
        return values
      }
      case 'unary': return node.op === '-' ? -numeric(evaluate(node.right)) : numeric(evaluate(node.right))
      case 'postfix': return numeric(evaluate(node.left)) / 100
      case 'binary': {
        const left = scalar(evaluate(node.left)); const right = scalar(evaluate(node.right))
        switch (node.op) {
          case '+': return numeric(left) + numeric(right)
          case '-': return numeric(left) - numeric(right)
          case '*': return numeric(left) * numeric(right)
          case '/': if (numeric(right) === 0) throw new ExcelFormulaError('除以零'); return numeric(left) / numeric(right)
          case '^': return Math.pow(numeric(left), numeric(right))
          case '&': return String(left ?? '') + String(right ?? '')
          case '=': return excelEqual(left, right)
          case '<>': return !excelEqual(left, right)
          case '>': return compare(left, right) > 0
          case '<': return compare(left, right) < 0
          case '>=': return compare(left, right) >= 0
          case '<=': return compare(left, right) <= 0
        }
        throw new ExcelFormulaError(`不支持运算符 ${node.op}`)
      }
      case 'call': {
        const args = node.args
        const at = (index: number) => evaluate(args[index] ?? { kind: 'literal', value: null })
        switch (node.name) {
          case 'IF': return truthy(at(0)) ? at(1) : at(2)
          case 'IFERROR': try { return at(0) } catch { return at(1) }
          case 'OR': return args.some((_, index) => truthy(at(index)))
          case 'AND': return args.every((_, index) => truthy(at(index)))
          case 'SWITCH': {
            const target = scalar(at(0))
            for (let index = 1; index + 1 < args.length; index += 2) if (excelEqual(target, scalar(at(index)))) return at(index + 1)
            return args.length % 2 === 0 ? at(args.length - 1) : null
          }
          case 'INT': return Math.floor(numeric(at(0)))
          case 'TRUNC': { const places = numeric(at(1)); const factor = Math.pow(10, places); return Math.trunc(numeric(at(0)) * factor) / factor }
          case 'ROUND': { const places = numeric(at(1)); const factor = Math.pow(10, places); const value = numeric(at(0)); return Math.sign(value) * Math.round(Math.abs(value) * factor) / factor }
          case 'ROUNDUP': { const places = numeric(at(1)); const factor = Math.pow(10, places); const value = numeric(at(0)); return Math.sign(value) * Math.ceil(Math.abs(value) * factor) / factor }
          case 'SUM': return args.flatMap((_, index) => { const value = at(index); return Array.isArray(value) ? value : [value] }).reduce<number>((sum, value) => sum + (typeof value === 'number' ? value : 0), 0)
          case 'MIN': return Math.min(...args.map((_, index) => numeric(at(index))))
          case 'MAX': return Math.max(...args.map((_, index) => numeric(at(index))))
          case 'ABS': return Math.abs(numeric(at(0)))
          case 'TEXT': {
            const value = numeric(at(0)); const format = String(scalar(at(1)))
            if (format === '0.0%') return `${(value * 100).toFixed(1)}%`
            if (format === '0.0') return value.toFixed(1)
            throw new ExcelFormulaError(`不支持 TEXT 格式 ${format}`)
          }
          default: throw new ExcelFormulaError(`不支持函数 ${node.name}`)
        }
      }
    }
  }
  const result = scalar(evaluate(root))
  if (typeof result === 'number' && !Number.isFinite(result)) throw new ExcelFormulaError('计算结果不是有限数值')
  return result
}
