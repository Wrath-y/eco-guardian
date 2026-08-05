import { describe, expect, it } from 'vitest'
import { issueNavigationQuery, issueNavigationRoute, normalizeFieldPath, readIssueNavigation } from '@/forms/issue-navigation'

describe('validation issue navigation', () => {
  it('preserves RFC 6901 paths and only exposes complete byte spans', () => {
    expect(normalizeFieldPath('payload.attribute_values.0.expression')).toBe('/payload/attribute_values/0/expression')
    expect(issueNavigationQuery({ field_path: '/payload/attribute_values/0/expression', formula_span: { start_byte: 1, end_byte: 5 } })).toEqual({
      field_path: '/payload/attribute_values/0/expression', span_start: '1', span_end: '5',
    })
    expect(issueNavigationRoute('character', '01948c1e-0000-7000-8000-000000000000', { field_path: '/payload/attribute_values/0/expression', formula_span: null })).toEqual({
      path: '/config/character/01948c1e-0000-7000-8000-000000000000', query: { field_path: '/payload/attribute_values/0/expression' },
    })
    expect(readIssueNavigation({ field_path: '/payload/attribute_values/0/expression', span_start: '1', span_end: '5' })).toEqual({
      fieldPath: '/payload/attribute_values/0/expression', span: { start_byte: 1, end_byte: 5 },
    })
    expect(readIssueNavigation({ field_path: '/payload/attribute_values/0/expression', span_start: '1' })).toEqual({
      fieldPath: '/payload/attribute_values/0/expression', span: null,
    })
  })
})
