import { describe, expect, it } from 'vitest'
import { utf8ByteToUTF16, utf8SpanToUTF16 } from '@/forms/utf8-span'

describe('UTF-8 formula spans', () => {
  it('maps ASCII, Chinese, combining text, and emoji', () => {
    expect(utf8SpanToUTF16('abc', { start_byte: 1, end_byte: 3 })).toEqual({ start: 1, end: 3 })
    expect(utf8SpanToUTF16('甲乙', { start_byte: 0, end_byte: 3 })).toEqual({ start: 0, end: 1 })
    expect(utf8SpanToUTF16('e\u0301x', { start_byte: 1, end_byte: 3 })).toEqual({ start: 1, end: 2 })
    expect(utf8SpanToUTF16('A😀B', { start_byte: 1, end_byte: 5 })).toEqual({ start: 1, end: 3 })
  })
  it('refuses non-boundary and invalid spans for field-only fallback', () => {
    expect(utf8ByteToUTF16('甲', 1)).toBeNull()
    expect(utf8SpanToUTF16('abc', { start_byte: 2, end_byte: 2 })).toBeNull()
  })
})
