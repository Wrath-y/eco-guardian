import type { components } from '@/api/generated'

export interface IssueNavigationTarget {
  fieldPath: string
  span: { start_byte: number; end_byte: number } | null
}

type ValidationIssue = components['schemas']['ValidationIssue']

export function normalizeFieldPath(path: string): string {
  if (path.startsWith('/')) return path
  return `/${path.split('.').map(segment => segment.replace(/~/g, '~0').replace(/\//g, '~1')).join('/')}`
}

export function issueNavigationQuery(issue: Pick<ValidationIssue, 'field_path' | 'formula_span'>): Record<string, string> {
  const query: Record<string, string> = { field_path: normalizeFieldPath(issue.field_path) }
  if (issue.formula_span) {
    query.span_start = String(issue.formula_span.start_byte)
    query.span_end = String(issue.formula_span.end_byte)
  }
  return query
}

// A validation panel already knows an entity's kind from its source listing.
// Keeping that lookup outside this adapter avoids putting display metadata into
// the immutable issue identity while still giving every panel one route shape.
export function issueNavigationRoute(kind: string, entityID: string, issue: Pick<ValidationIssue, 'field_path' | 'formula_span'>) {
  return { path: `/config/${encodeURIComponent(kind)}/${encodeURIComponent(entityID)}`, query: issueNavigationQuery(issue) }
}

export function readIssueNavigation(query: Record<string, unknown>): IssueNavigationTarget | null {
  const rawPath = query.field_path
  const fieldPath = Array.isArray(rawPath) ? rawPath[0] : rawPath
  if (typeof fieldPath !== 'string' || !fieldPath.startsWith('/')) return null
  const start = Number(Array.isArray(query.span_start) ? query.span_start[0] : query.span_start)
  const end = Number(Array.isArray(query.span_end) ? query.span_end[0] : query.span_end)
  const span = Number.isInteger(start) && Number.isInteger(end) && start >= 0 && end > start
    ? { start_byte: start, end_byte: end }
    : null
  return { fieldPath, span }
}
