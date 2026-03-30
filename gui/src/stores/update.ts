import { computed, ref } from 'vue'
import { defineStore } from 'pinia'
import { api } from '@/services/api'
import type { ApiResponse, VersionInfo } from '@/types'

export const useUpdateStore = defineStore('update', () => {
    const info = ref<VersionInfo | null>(null)

    async function fetch() {
        if (info.value) return
        try {
            const res = await api<ApiResponse<VersionInfo>>('/api/v1/version')
            info.value = res.data
        } catch {
            // Non-critical — silently ignore network errors or GitHub rate limits.
        }
    }

    const updateAvailable = computed(() => info.value?.update_available ?? false)
    const latestVersion   = computed(() => info.value?.latest_version  ?? null)
    const serverVersion   = computed(() => info.value?.server_version  ?? null)

    return { info, fetch, updateAvailable, latestVersion, serverVersion }
})
