import { useCallback, useEffect, useRef } from 'react'
import { useAppStore } from '../stores/appStore'
import type { SSEEvent } from '../types'

interface SSEOptions {
  onChunk?: (content: string) => void
  onDone?: (paperId: string) => void
  onError?: (error: string) => void
  onToolCall?: (toolName: string) => void
}

export function useSSE() {
  const { setIsStreaming, appendToStreamBuffer, clearStreamBuffer } = useAppStore()
  const abortRef = useRef<AbortController | null>(null)

  const abort = useCallback(() => {
    abortRef.current?.abort()
    abortRef.current = null
  }, [])

  // Auto-abort on unmount
  useEffect(() => () => abort(), [abort])

  const streamRequest = useCallback(async (
    url: string,
    body: unknown,
    options: SSEOptions = {},
  ) => {
    abort() // abort any previous in-flight request

    const controller = new AbortController()
    abortRef.current = controller

    setIsStreaming(true)
    clearStreamBuffer()

    try {
      const response = await fetch(url, {
        method: 'POST',
        headers: {
          'Content-Type': 'application/json',
          'Accept': 'text/event-stream',
        },
        body: JSON.stringify(body),
        signal: controller.signal,
      })

      if (!response.ok) {
        const errData = await response.json().catch(() => ({}))
        const errMsg = (errData as { error?: string }).error || `HTTP ${response.status}`
        options.onError?.(errMsg)
        setIsStreaming(false)
        return
      }

      if (!response.body) {
        options.onError?.('No response body')
        setIsStreaming(false)
        return
      }

      const reader = response.body.getReader()
      const decoder = new TextDecoder()
      let buffer = ''

      while (true) {
        const { done, value } = await reader.read()
        if (done) break

        buffer += decoder.decode(value, { stream: true })
        const lines = buffer.split('\n')
        buffer = lines.pop() || ''

        for (const line of lines) {
          const trimmed = line.trim()
          if (!trimmed || !trimmed.startsWith('data: ')) continue

          const jsonStr = trimmed.slice(6)
          try {
            const evt: SSEEvent = JSON.parse(jsonStr)
            switch (evt.type) {
              case 'chunk':
                if (evt.content) {
                  appendToStreamBuffer(evt.content)
                  options.onChunk?.(evt.content)
                }
                break
              case 'done':
                options.onDone?.(evt.paper_id || '')
                break
              case 'error':
                options.onError?.(evt.error || 'Unknown error')
                break
              case 'tool_call':
                if (evt.tool_name) {
                  options.onToolCall?.(evt.tool_name)
                }
                break
            }
          } catch {
            // skip
          }
        }
      }
    } catch (err) {
      if (err instanceof DOMException && err.name === 'AbortError') return
      const msg = err instanceof Error ? err.message : 'Network error'
      options.onError?.(msg)
    } finally {
      if (abortRef.current === controller) abortRef.current = null
      setIsStreaming(false)
    }
  }, [setIsStreaming, appendToStreamBuffer, clearStreamBuffer, abort])

  return { streamRequest, abort }
}
