import * as Sentry from '@sentry/vue'
import { RPCError, type RPCHandler } from './rpc'

const spanStartMethod = 'trace/span.start'
const spanErrorMethod = 'trace/span.error'
const spanEndMethod = 'trace/span.end'

type TraceAttributeValue = string | number | boolean
type TraceAttributes = Record<string, TraceAttributeValue>

type SpanState = {
  span: Sentry.Span
  errorRecorded: boolean
}

export type SentryTraceAPI = Pick<
  typeof Sentry,
  'captureException' | 'getTraceData' | 'startInactiveSpan' | 'startNewTrace' | 'withActiveSpan' | 'withScope'
>

export function createSentryTraceAdapter(sentry: SentryTraceAPI = Sentry): RPCHandler {
  const spans = new Map<string, SpanState>()
  let closed = false

  const endSpan = (spanID: string) => {
    const state = spans.get(spanID)
    if (state == null) return
    spans.delete(spanID)
    try {
      state.span.end()
    } catch {
      // Sentry cleanup is best effort.
    }
  }

  const startSpan = (params: unknown, signal: AbortSignal) => {
    if (signal.aborted || closed) throw new RPCError(-32603, 'JSON-RPC internal error')
    const input = asRecord(params)
    const name = requiredString(input, 'name')
    const operation = requiredString(input, 'operation')
    const parentSpanID = optionalString(input, 'parentSpanId')
    const attributes = traceAttributes(input.attributes)

    let span: Sentry.Span
    if (parentSpanID == null) {
      span = sentry.startNewTrace(() =>
        sentry.startInactiveSpan({
          name,
          op: operation,
          forceTransaction: true,
          parentSpan: null,
          attributes
        })
      )
    } else {
      const parent = spans.get(parentSpanID)?.span
      if (parent == null) throw new RPCError(-32602, 'JSON-RPC invalid params')
      span = sentry.startInactiveSpan({ name, op: operation, parentSpan: parent, attributes })
      // Browser SDK currently ignores parentSpan when parentSpanIsAlwaysRootSpan
      // is enabled. Keep the child attached to the iSPX parent explicitly.
      ;(span as { _parentSpanId?: string })._parentSpanId = parent.spanContext().spanId
    }

    const spanID = span.spanContext().spanId
    spans.set(spanID, { span, errorRecorded: false })
    signal.addEventListener('abort', () => endSpan(spanID), { once: true })
    try {
      const traceData = sentry.getTraceData({ span })
      const headers: Record<string, string> = {}
      if (traceData['sentry-trace'] != null) headers['Sentry-Trace'] = traceData['sentry-trace']
      if (traceData.baggage != null) headers.Baggage = traceData.baggage
      return { spanId: spanID, headers }
    } catch (error) {
      endSpan(spanID)
      throw error
    }
  }

  const recordError = (params: unknown) => {
    const input = asRecord(params)
    const state = spans.get(requiredString(input, 'spanId'))
    if (state == null || state.errorRecorded) return
    state.errorRecorded = true
    const attributes = traceAttributes(input.attributes)
    setSpanAttributes(state.span, attributes)

    const error = new Error('iSPX operation failed')
    error.name = 'ISPXOperationError'
    sentry.withScope((scope) => {
      const category = attributes.category
      const reason = attributes.reason
      if (typeof category === 'string') scope.setTag('category', category)
      if (typeof reason === 'string') scope.setTag('reason', reason)
      sentry.withActiveSpan(state.span, () => sentry.captureException(error))
    })
  }

  const finishSpan = (params: unknown) => {
    const input = asRecord(params)
    const spanID = requiredString(input, 'spanId')
    const state = spans.get(spanID)
    if (state == null) return
    setSpanAttributes(state.span, traceAttributes(input.attributes))
    if (input.status === 'ok') state.span.setStatus({ code: 1 })
    else if (input.status === 'error') state.span.setStatus({ code: 2 })
    endSpan(spanID)
  }

  return {
    handle(method, params, signal) {
      if (closed) throw new RPCError(-32603, 'JSON-RPC internal error')
      switch (method) {
        case spanStartMethod:
          return startSpan(params, signal)
        case spanErrorMethod:
          recordError(params)
          return null
        case spanEndMethod:
          finishSpan(params)
          return null
        default:
          throw new RPCError(-32601, 'JSON-RPC method not found')
      }
    },
    close() {
      if (closed) return
      closed = true
      for (const spanID of [...spans.keys()].reverse()) endSpan(spanID)
    }
  }
}

function asRecord(value: unknown): Record<string, unknown> {
  if (value == null || typeof value !== 'object' || Array.isArray(value)) {
    throw new RPCError(-32602, 'JSON-RPC invalid params')
  }
  return value as Record<string, unknown>
}

function requiredString(record: Record<string, unknown>, key: string): string {
  const value = record[key]
  if (typeof value !== 'string' || value === '') throw new RPCError(-32602, 'JSON-RPC invalid params')
  return value
}

function optionalString(record: Record<string, unknown>, key: string): string | null {
  const value = record[key]
  if (value == null) return null
  if (typeof value !== 'string' || value === '') throw new RPCError(-32602, 'JSON-RPC invalid params')
  return value
}

function traceAttributes(value: unknown): TraceAttributes {
  if (value == null) return {}
  const input = asRecord(value)
  const attributes: TraceAttributes = {}
  for (const [key, attribute] of Object.entries(input)) {
    if (typeof attribute === 'string' || typeof attribute === 'number' || typeof attribute === 'boolean') {
      attributes[key] = attribute
    }
  }
  return attributes
}

function setSpanAttributes(span: Sentry.Span, attributes: TraceAttributes) {
  for (const [key, value] of Object.entries(attributes)) span.setAttribute(key, value)
}
