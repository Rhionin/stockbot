import { describe, expect, it } from 'vitest';
import fc from 'fast-check';
import { createSseParser, type SseMessage } from './sseParser';

function parseAll(chunks: string[]): SseMessage[] {
  const parser = createSseParser();
  return chunks.flatMap((chunk) => parser.push(chunk));
}

const serverFrame = 'id: 7\nevent: scan\ndata: {"id":"scan-1"}\n\n';

describe('createSseParser', () => {
  it('reads the frame the server writes', () => {
    expect(parseAll([serverFrame])).toEqual([{ id: '7', event: 'scan', data: '{"id":"scan-1"}' }]);
  });

  it('ignores the opening comment and keepalive pings', () => {
    expect(parseAll([': ok\n\n', ': ping\n\n', serverFrame, ': ping\n\n'])).toHaveLength(1);
  });

  it('waits for the blank line before emitting', () => {
    const parser = createSseParser();
    expect(parser.push('event: hint\ndata: {"type":"scans"}\n')).toEqual([]);
    expect(parser.push('\n')).toEqual([{ id: '', event: 'hint', data: '{"type":"scans"}' }]);
  });

  it('joins multi-line data with newlines', () => {
    expect(parseAll(['data: one\ndata: two\ndata:\ndata: four\n\n'])).toEqual([
      { id: '', event: 'message', data: 'one\ntwo\n\nfour' },
    ]);
  });

  it('strips exactly one leading space from a value', () => {
    expect(parseAll(['data:  two spaces\n\n', 'data:none\n\n'])).toEqual([
      { id: '', event: 'message', data: ' two spaces' },
      { id: '', event: 'message', data: 'none' },
    ]);
  });

  it('treats a field with no colon as an empty value', () => {
    expect(parseAll(['data\n\n'])).toEqual([{ id: '', event: 'message', data: '' }]);
  });

  it('defaults the event name to message and resets it between frames', () => {
    expect(parseAll(['event: scan\ndata: a\n\ndata: b\n\n'])).toEqual([
      { id: '', event: 'scan', data: 'a' },
      { id: '', event: 'message', data: 'b' },
    ]);
  });

  it('does not emit a frame that has no data', () => {
    expect(parseAll(['event: scan\n\n', 'id: 3\n\n'])).toEqual([]);
  });

  it('keeps the last id for later frames until a new id arrives', () => {
    const parser = createSseParser();
    const first = parser.push('id: 4\ndata: a\n\ndata: b\n\nid: 9\ndata: c\n\n');
    expect(first.map((message) => message.id)).toEqual(['4', '4', '9']);
    expect(parser.lastEventId).toBe('9');
  });

  it('ignores an id containing NUL', () => {
    const parser = createSseParser();
    parser.push('id: 1\ndata: a\n\nid: 2\u00003\ndata: b\n\n');
    expect(parser.lastEventId).toBe('1');
  });

  it('records retry only when it is all digits', () => {
    const parser = createSseParser();
    expect(parser.retryMs).toBeNull();
    parser.push('retry: 1500\n\n');
    expect(parser.retryMs).toBe(1500);
    parser.push('retry: soon\nretry: 12ms\n\n');
    expect(parser.retryMs).toBe(1500);
  });

  it('ignores unknown fields', () => {
    expect(parseAll(['foo: bar\ndata: a\n\n'])).toEqual([{ id: '', event: 'message', data: 'a' }]);
  });

  it('accepts CRLF, CR and LF line breaks', () => {
    const expected = [{ id: '', event: 'scan', data: 'a' }];
    expect(parseAll(['event: scan\r\ndata: a\r\n\r\n'])).toEqual(expected);
    expect(parseAll(['event: scan\rdata: a\r\r'])).toEqual([]);
    expect(parseAll(['event: scan\rdata: a\r\rx'])).toEqual(expected);
    expect(parseAll(['event: scan\ndata: a\n\n'])).toEqual(expected);
  });

  it('holds a trailing CR until it knows whether an LF follows', () => {
    const parser = createSseParser();
    expect(parser.push('data: a\r\n\r')).toEqual([]);
    expect(parser.push('\n')).toEqual([{ id: '', event: 'message', data: 'a' }]);
  });

  it('drops a byte order mark at the start of the stream only', () => {
    expect(parseAll(['﻿data: a\n\n'])).toEqual([{ id: '', event: 'message', data: 'a' }]);
    expect(parseAll(['﻿', 'data: a\n\n'])).toHaveLength(1);
    expect(parseAll(['data: a\n\n', '﻿data: b\n\n'])).toHaveLength(1);
  });

  it('reads a frame split in the middle of a field name', () => {
    expect(parseAll(['id: 7\neve', 'nt: scan\nda', 'ta: {"id":', '"scan-1"}\n', '\n'])).toEqual([
      { id: '7', event: 'scan', data: '{"id":"scan-1"}' },
    ]);
  });

  it('never emits a half-received frame at the end of the input', () => {
    expect(parseAll(['event: scan\ndata: a'])).toEqual([]);
  });

  it('reads multi-byte text split between chunks of whole characters', () => {
    expect(parseAll(['data: caf', 'é 🥫\n', '\n'])).toEqual([{ id: '', event: 'message', data: 'café 🥫' }]);
  });

  describe('split anywhere', () => {
    const lineBreak = fc.constantFrom('\n', '\r\n', '\r');
    const text = fc.string({ unit: fc.constantFrom('a', 'b', ' ', ':', '0', '1', 'é', '{', '}') });
    const line = fc.oneof(
      text.map((value) => `data: ${value}`),
      text.map((value) => `event: ${value}`),
      text.map((value) => `id: ${value}`),
      text.map((value) => `retry: ${value}`),
      text.map((value) => `: ${value}`),
      text.map((value) => value),
      fc.constant(''),
    );
    const stream = fc
      .array(fc.tuple(line, lineBreak), { maxLength: 30 })
      .map((rows) => rows.map(([content, terminator]) => content + terminator).join(''));

    it('yields the same messages however the text is cut into chunks', () => {
      fc.assert(
        fc.property(stream, fc.array(fc.nat({ max: 200 }), { maxLength: 8 }), (input, cuts) => {
          const whole = createSseParser();
          const expected = whole.push(input);
          const points = [...new Set(cuts.map((cut) => cut % (input.length + 1)))].sort((a, b) => a - b);
          const chunks: string[] = [];
          let from = 0;
          for (const point of points) {
            chunks.push(input.slice(from, point));
            from = point;
          }
          chunks.push(input.slice(from));
          const split = createSseParser();
          const actual = chunks.flatMap((chunk) => split.push(chunk));
          expect(actual).toEqual(expected);
          expect(split.lastEventId).toBe(whole.lastEventId);
          expect(split.retryMs).toBe(whole.retryMs);
        }),
        { numRuns: 300 },
      );
    });

    it('yields the same messages when fed one character at a time', () => {
      fc.assert(
        fc.property(stream, (input) => {
          const expected = createSseParser().push(input);
          const parser = createSseParser();
          const actual = [...input].flatMap((char) => parser.push(char));
          expect(actual).toEqual(expected);
        }),
        { numRuns: 200 },
      );
    });

    it('round-trips frames written the way the server writes them', () => {
      const frame = fc.record({
        id: fc.nat({ max: 100000 }),
        event: fc.constantFrom('scan', 'scanner_mode', 'inventory', 'hint', 'scan_processing'),
        data: fc.json(),
      });
      fc.assert(
        fc.property(fc.array(frame, { maxLength: 10 }), fc.nat({ max: 40 }), (frames, size) => {
          const wire = frames.map((f) => `id: ${f.id}\nevent: ${f.event}\ndata: ${f.data}\n\n`).join('');
          const parser = createSseParser();
          const step = size + 1;
          const out: SseMessage[] = [];
          for (let at = 0; at < wire.length; at += step) out.push(...parser.push(wire.slice(at, at + step)));
          expect(out).toEqual(frames.map((f) => ({ id: String(f.id), event: f.event, data: f.data })));
        }),
        { numRuns: 200 },
      );
    });
  });
});
