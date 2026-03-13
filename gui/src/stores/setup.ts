// stores/setup.ts — tracks whether the initial server setup has been completed.
//
// Setup is considered complete when at least one user exists in the database.
// This store is checked by the router guard on every navigation to decide
// whether to redirect to /setup instead of /login.
//
// The status is fetched once per session (lazy, on first access) and cached
// in-memory. It is never persisted to localStorage because it only matters
// for the brief window before the first admin account is created — after that,
// completed is always true and the setup route becomes permanently inaccessible.

import { ref } from 'vue'
import { defineStore } from 'pinia'
import { api } from '@/services/api'
import type { ApiResponse } from '@/types'

interface SetupStatusResponse {
  completed: boolean
}

export const useSetupStore = defineStore('setup', () => {
  // null = not yet fetched, true/false = known state
  const completed = ref<boolean | null>(null)
  const loading = ref(false)

  // fetchStatus calls GET /api/v1/setup/status and caches the result.
  // Subsequent calls are no-ops — the value is immutable once true.
  // On network error we optimistically assume setup is complete to avoid
  // trapping the user on /setup when the server is temporarily unreachable.
  async function fetchStatus(): Promise<boolean> {
    if (completed.value !== null) return completed.value

    loading.value = true
    try {
      const res = await api<ApiResponse<SetupStatusResponse>>('/api/v1/setup/status')
      completed.value = res.data.completed
    } catch {
      completed.value = true
    } finally {
      loading.value = false
    }

    return completed.value
  }

  // markCompleted is called by SetupPage after a successful POST /setup/complete
  // so the router guard does not need to re-fetch the status on the redirect
  // to /login that immediately follows.
  function markCompleted() {
    completed.value = true
  }

  return { completed, loading, fetchStatus, markCompleted }
})