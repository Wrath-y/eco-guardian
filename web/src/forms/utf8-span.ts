// The server stores formula locations as UTF-8 byte ranges while browser text
// inputs select UTF-16 code units. Invalid/non-boundary positions return null
// so callers can safely fall back to focusing the field.
export interface UTF8Span { start_byte: number; end_byte: number }
export interface UTF16Selection { start: number; end: number }

const encoder = new TextEncoder()

export function utf8ByteToUTF16(source: string, byteOffset: number): number | null {
  if (byteOffset < 0) return null
  let bytes = 0
  let utf16 = 0
  for (const codePoint of source) {
    if (bytes === byteOffset) return utf16
    const width = encoder.encode(codePoint).length
    if (byteOffset > bytes && byteOffset < bytes + width) return null
    bytes += width
    utf16 += codePoint.length
  }
  return bytes === byteOffset ? utf16 : null
}

export function utf8SpanToUTF16(source: string, span: UTF8Span): UTF16Selection | null {
  if (span.end_byte <= span.start_byte) return null
  const start = utf8ByteToUTF16(source, span.start_byte)
  const end = utf8ByteToUTF16(source, span.end_byte)
  return start === null || end === null ? null : { start, end }
}
