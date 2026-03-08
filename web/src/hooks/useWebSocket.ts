import { useEffect, useRef, useCallback } from 'react'
import { useQueryClient } from '@tanstack/react-query'
import { useStore } from '../store'

interface WebSocketMessage {
  type: string
  data?: unknown
}

export function useWebSocket() {
  const wsRef = useRef<WebSocket | null>(null)
  const reconnectTimeoutRef = useRef<ReturnType<typeof setTimeout> | null>(null)
  const queryClient = useQueryClient()
  const addNotification = useStore((s) => s.addNotification)

  const connect = useCallback(() => {
    const proto = location.protocol === 'https:' ? 'wss:' : 'ws:'
    const url = `${proto}//${location.host}/ws`

    const ws = new WebSocket(url)
    wsRef.current = ws

    ws.onopen = () => {
      console.log('[WS] Connected')
    }

    ws.onmessage = (event) => {
      try {
        const msg: WebSocketMessage = JSON.parse(event.data)

        if (msg.type === 'refresh') {
          // Invalidate all queries to trigger refetch
          queryClient.invalidateQueries()

          // Create notification from event data if available
          if (msg.data && typeof msg.data === 'object') {
            const eventData = msg.data as Record<string, unknown>
            const eventType = eventData.event_type as string | undefined
            if (eventType) {
              addNotification({
                id: Date.now().toString(),
                type: eventType.includes('error') ? 'error' : 'info',
                message: formatEventMessage(eventType, eventData),
                timestamp: new Date().toISOString(),
                read: false,
              })
            }
          }
        }
      } catch {
        // Non-JSON message, ignore
      }
    }

    ws.onclose = () => {
      console.log('[WS] Disconnected, reconnecting in 3s...')
      reconnectTimeoutRef.current = setTimeout(connect, 3000)
    }

    ws.onerror = () => {
      ws.close()
    }
  }, [queryClient, addNotification])

  useEffect(() => {
    connect()
    return () => {
      if (reconnectTimeoutRef.current) clearTimeout(reconnectTimeoutRef.current)
      wsRef.current?.close()
    }
  }, [connect])
}

function formatEventMessage(type: string, data: Record<string, unknown>): string {
  const featureId = data.feature_id as string | undefined
  switch (type) {
    case 'feature.status_changed': {
      const parsed = typeof data.data === 'string' ? JSON.parse(data.data) : data.data
      return `Feature "${featureId}" moved to ${(parsed as Record<string, string>)?.to || 'unknown'}`
    }
    case 'cycle.advanced':
      return `Cycle advanced for "${featureId}"`
    case 'idea.approved':
      return `Idea approved: ${featureId}`
    case 'idea.rejected':
      return `Idea rejected: ${featureId}`
    default:
      return `Event: ${type}`
  }
}
