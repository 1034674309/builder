import { describe, expect, it } from 'vitest'
import { RPCError } from './rpc'
import { createSentryTraceAdapter, type SentryTraceAPI } from './sentry-trace-adapter'

type StartOptions = Parameters<SentryTraceAPI['startInactiveSpan']>[0]

class FakeSpan {
  readonly attributes: Record<string, unknown>
  status: unknown = null
  ended = false
  _parentSpanId?: string

  constructor(
    readonly id: string,
    readonly options: StartOptions
  ) {
    this.attributes = { ...options.attributes }
  }

  spanContext() {
    return { spanId: this.id, traceId: 'trace-id', traceFlags: 1 }
  }

  setAttribute(key: string, value: unknown) {
    this.attributes[key] = value
    return this
  }

  setStatus(status: unknown) {
    this.status = status
    return this
  }

  end() {
    this.ended = true
  }
}

function createFakeSentry() {
  const spans: FakeSpan[] = []
  const captures: Array<{ error: Error; span: FakeSpan | null; tags: Record<string, string> }> = []
  let activeSpan: FakeSpan | null = null
  let activeTags: Record<string, string> = {}
  const api = {
    startNewTrace<T>(callback: () => T) {
      return callback()
    },
    startInactiveSpan(options: StartOptions) {
      const span = new FakeSpan(`span-${spans.length + 1}`, options)
      spans.push(span)
      return span
    },
    getTraceData({ span }: { span: FakeSpan }) {
      return {
        'sentry-trace': `trace-${span.id}`,
        baggage: `meta-${span.id}`
      }
    },
    withScope(callback: (scope: { setTag(key: string, value: string): void }) => void) {
      const previous = activeTags
      activeTags = {}
      callback({
        setTag(key, value) {
          activeTags[key] = value
        }
      })
      activeTags = previous
    },
    withActiveSpan<T>(span: FakeSpan, callback: () => T) {
      const previous = activeSpan
      activeSpan = span
      try {
        return callback()
      } finally {
        activeSpan = previous
      }
    },
    captureException(error: Error) {
      captures.push({ error, span: activeSpan, tags: { ...activeTags } })
      return 'event-id'
    }
  } as unknown as SentryTraceAPI
  return { api, spans, captures }
}

describe('createSentryTraceAdapter', () => {
  it('creates linked spans and returns propagation headers', async () => {
    const { api, spans } = createFakeSentry()
    const adapter = createSentryTraceAdapter(api)
    const signal = new AbortController().signal

    const root = (await adapter.handle(
      'trace/span.start',
      {
        name: 'ai.think',
        operation: 'ai.think',
        attributes: { game_session_id: 'session-1' }
      },
      signal
    )) as { spanId: string; headers: Record<string, string> }
    const child = (await adapter.handle(
      'trace/span.start',
      {
        parentSpanId: root.spanId,
        name: 'POST /ai-interaction/turns',
        operation: 'http.client'
      },
      signal
    )) as { spanId: string; headers: Record<string, string> }

    expect(root).toEqual({
      spanId: 'span-1',
      headers: { 'Sentry-Trace': 'trace-span-1', Baggage: 'meta-span-1' }
    })
    expect(child.spanId).toBe('span-2')
    expect(spans[0].options).toMatchObject({ name: 'ai.think', op: 'ai.think', forceTransaction: true })
    expect(spans[0].attributes).toEqual({ game_session_id: 'session-1' })
    expect(spans[1].options.parentSpan).toBe(spans[0])
    expect(spans[1]._parentSpanId).toBe('span-1')

    await adapter.handle('trace/span.end', { spanId: child.spanId, status: 'error' }, signal)
    await adapter.handle('trace/span.end', { spanId: root.spanId, status: 'ok' }, signal)
    expect(spans[0].status).toEqual({ code: 1 })
    expect(spans[1].status).toEqual({ code: 2 })
    expect(spans.every((span) => span.ended)).toBe(true)
  })

  it('records at most one fixed exception with category and reason', async () => {
    const { api, spans, captures } = createFakeSentry()
    const adapter = createSentryTraceAdapter(api)
    const signal = new AbortController().signal
    const root = (await adapter.handle('trace/span.start', { name: 'ai.think', operation: 'ai.think' }, signal)) as {
      spanId: string
    }

    const error = {
      spanId: root.spanId,
      attributes: { category: 'transport_failure', reason: 'network' }
    }
    await adapter.handle('trace/span.error', error, signal)
    await adapter.handle('trace/span.error', error, signal)

    expect(captures).toHaveLength(1)
    expect(captures[0].error.name).toBe('ISPXOperationError')
    expect(captures[0].error.message).toBe('iSPX operation failed')
    expect(captures[0].span).toBe(spans[0])
    expect(captures[0].tags).toEqual({ category: 'transport_failure', reason: 'network' })
  })

  it('ends remaining spans on cancellation and close', async () => {
    const { api, spans } = createFakeSentry()
    const adapter = createSentryTraceAdapter(api)
    const controller = new AbortController()
    await adapter.handle('trace/span.start', { name: 'ai.think', operation: 'ai.think' }, controller.signal)
    controller.abort()
    expect(spans[0].ended).toBe(true)

    await adapter.handle(
      'trace/span.start',
      { name: 'ai.archive', operation: 'ai.archive' },
      new AbortController().signal
    )
    adapter.close?.()
    adapter.close?.()
    expect(spans[1].ended).toBe(true)
    await expect(
      Promise.resolve().then(() =>
        adapter.handle('trace/span.start', { name: 'ai.think', operation: 'ai.think' }, new AbortController().signal)
      )
    ).rejects.toBeInstanceOf(RPCError)
  })
})
