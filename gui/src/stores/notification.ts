// notification.ts — Pinia store for in-app notification state.
//
// Responsibilities:
//   - Fetch the most recent notifications on mount via REST
//   - Accept real-time notifications pushed over WebSocket
//   - Track unread count for the bell badge
//   - Expose markRead / markAllRead actions
//
// The store is kept lean: it holds the last 20 items shown in the dropdown.
// A future full notifications page can extend this with pagination.

import { defineStore } from 'pinia'
import { ref, computed } from 'vue'
import { api } from '@/services/api'
import type { Notification, ApiResponse, WSNotificationPayload } from '@/types'

interface ListNotificationsResponse {
  items: Notification[]
  total: number
}

const MAX_ITEMS = 20

export const useNotificationStore = defineStore('notification', () => {
  // ─── State ──────────────────────────────────────────────────────────────────

  const items = ref<Notification[]>([])
  const total = ref(0)
  const loading = ref(false)

  // ─── Getters ─────────────────────────────────────────────────────────────────

  const unreadCount = computed(() =>
    items.value.filter((n) => n.read_at === null).length,
  )

  // ─── Actions ──────────────────────────────────────────────────────────────────

  async function fetchRecent(): Promise<void> {
    loading.value = true
    try {
      const res = await api<ApiResponse<ListNotificationsResponse>>(
        `/api/v1/notifications?limit=${MAX_ITEMS}&offset=0`,
      )
      items.value = res.data.items ?? []
      total.value = res.data.total ?? 0
    } catch (err) {
      console.warn('[notifications] failed to fetch', err)
    } finally {
      loading.value = false
    }
  }

  async function markRead(id: string): Promise<void> {
    try {
      await api(`/api/v1/notifications/${id}/read`, { method: 'PATCH' })
      const n = items.value.find((x) => x.id === id)
      if (n) n.read_at = new Date().toISOString()
    } catch (err) {
      console.warn('[notifications] failed to mark read', err)
    }
  }

  async function markAllRead(): Promise<void> {
    try {
      await api('/api/v1/notifications/read-all', { method: 'PATCH' })
      const now = new Date().toISOString()
      items.value.forEach((n) => {
        if (!n.read_at) n.read_at = now
      })
    } catch (err) {
      console.warn('[notifications] failed to mark all read', err)
    }
  }

  // prependFromWS is called when a WebSocket notification message arrives.
  // It adds the new notification to the front of the list, deduplicating
  // by ID and capping the list at MAX_ITEMS.
  function prependFromWS(payload: WSNotificationPayload): void {
    if (items.value.some((n) => n.id === payload.id)) return

    items.value.unshift({
      id: payload.id,
      type: payload.type,
      title: payload.title,
      body: payload.body,
      payload: '{}',
      read_at: null,
      created_at: payload.created_at,
    })
    total.value++

    if (items.value.length > MAX_ITEMS) {
      items.value.pop()
    }
  }

  return {
    items,
    total,
    loading,
    unreadCount,
    fetchRecent,
    markRead,
    markAllRead,
    prependFromWS,
  }
})
