import { describe, expect, it } from 'vitest'
import { issueNavigationQuery, issueNavigationRoute, normalizeFieldPath, readIssueNavigation } from '@/forms/issue-navigation'
import { emptyDraft, validateDraft } from '@/forms/registry'
import { utf8ByteToUTF16, utf8SpanToUTF16 } from '@/forms/utf8-span'

describe('React authoring form helpers', () => {
  it('keeps all six fixed entity draft shapes available', () => {
    for (const kind of ['attribute', 'tag', 'character', 'skill', 'item', 'effect'] as const) {
      const draft = emptyDraft(kind)
      expect(draft.payload).toBeTruthy()
      const paths = validateDraft(draft as unknown as Record<string, unknown>, kind).map(issue => issue.path)
      expect(paths).toContain('key')
      expect(paths).toContain('name')
    }
  })

  it('preserves RFC 6901 paths and only exposes complete byte spans', () => {
    expect(normalizeFieldPath('payload.attribute_values.0.expression')).toBe('/payload/attribute_values/0/expression')
    expect(issueNavigationQuery({ field_path: '/payload/attribute_values/0/expression', formula_span: { start_byte: 1, end_byte: 5 } })).toEqual({ field_path: '/payload/attribute_values/0/expression', span_start: '1', span_end: '5' })
    expect(issueNavigationRoute('character', '01948c1e-0000-7000-8000-000000000000', { field_path: '/payload/attribute_values/0/expression', formula_span: null })).toEqual({ path: '/config/character/01948c1e-0000-7000-8000-000000000000', query: { field_path: '/payload/attribute_values/0/expression' } })
    expect(readIssueNavigation({ field_path: '/payload/attribute_values/0/expression', span_start: '1', span_end: '5' })).toEqual({ fieldPath: '/payload/attribute_values/0/expression', span: { start_byte: 1, end_byte: 5 } })
  })

  it('maps server UTF-8 spans to browser UTF-16 selections', () => {
    expect(utf8SpanToUTF16('甲乙', { start_byte: 0, end_byte: 3 })).toEqual({ start: 0, end: 1 })
    expect(utf8SpanToUTF16('A😀B', { start_byte: 1, end_byte: 5 })).toEqual({ start: 1, end: 3 })
    expect(utf8ByteToUTF16('甲', 1)).toBeNull()
  })
})
