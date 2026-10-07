<script setup lang="ts">
import { ref, onMounted } from 'vue'
import { Alert, AlertDescription } from '@/components/ui/alert'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { Skeleton } from '@/components/ui/skeleton'
import { AlertCircle, Loader2, RefreshCw } from '@lucide/vue'
import { api, apiErrorMessage } from '@/services/api'
import type { ApiResponse, SnapshotSyncSettings } from '@/types'

// ---------------------------------------------------------------------------
// State
// ---------------------------------------------------------------------------

const loading = ref(false)
const submitting = ref(false)
const submitError = ref<string | null>(null)
const success = ref(false)

const settings = ref<SnapshotSyncSettings>({ interval_hours: 0 })

// ---------------------------------------------------------------------------
// Fetch
// ---------------------------------------------------------------------------

async function fetchSettings() {
    loading.value = true
    try {
        const res = await api<ApiResponse<SnapshotSyncSettings>>('/api/v1/settings/snapshot-sync')
        settings.value = res.data
    } finally {
        loading.value = false
    }
}

onMounted(fetchSettings)

// ---------------------------------------------------------------------------
// Submit
// ---------------------------------------------------------------------------

async function submit() {
    submitting.value = true
    submitError.value = null
    success.value = false

    try {
        await api('/api/v1/settings/snapshot-sync', {
            method: 'PUT',
            body: { interval_hours: Number(settings.value.interval_hours) || 0 },
        })
        success.value = true
        setTimeout(() => { success.value = false }, 3000)
    } catch (e: any) {
        submitError.value = apiErrorMessage(e, 'Failed to save snapshot sync settings')
    } finally {
        submitting.value = false
    }
}
</script>

<template>
    <!-- Section header -->
    <div class="flex items-start justify-between gap-4 mb-6">
        <div>
            <h2 class="text-base font-semibold">Snapshot Sync</h2>
            <p class="mt-1 text-sm text-muted-foreground">
                Periodically re-read every destination's repository so snapshots removed or added outside
                Arkeep — for example pruned by a cron job on an append-only server — are reflected here.
                Each destination can also be synced on demand from its page.
                Set the interval to <strong>0</strong> to disable the periodic sync.
            </p>
        </div>
        <Button variant="outline" size="icon" aria-label="Refresh" :disabled="loading" @click="fetchSettings">
            <RefreshCw class="size-4" :class="{ 'animate-spin': loading }" />
        </Button>
    </div>

    <!-- Skeleton -->
    <template v-if="loading">
        <Skeleton class="h-16 w-full rounded-lg" />
    </template>

    <!-- Form -->
    <form v-else novalidate @submit.prevent="submit">
        <div class="flex flex-col gap-6">

            <!-- Alerts -->
            <Transition enter-active-class="transition-all duration-200" enter-from-class="-translate-y-1 opacity-0"
                leave-active-class="transition-all duration-150" leave-to-class="-translate-y-1 opacity-0">
                <Alert v-if="submitError" variant="destructive">
                    <AlertCircle class="size-4" />
                    <AlertDescription>{{ submitError }}</AlertDescription>
                </Alert>
            </Transition>

            <Transition enter-active-class="transition-all duration-200" enter-from-class="-translate-y-1 opacity-0"
                leave-active-class="transition-all duration-150" leave-to-class="-translate-y-1 opacity-0">
                <Alert v-if="success"
                    class="border-emerald-500/30 bg-emerald-500/5 text-emerald-600 dark:text-emerald-400">
                    <AlertDescription>Snapshot sync settings saved.</AlertDescription>
                </Alert>
            </Transition>

            <!-- ── Interval ─────────────────────────────────────────────────── -->
            <div class="flex flex-col gap-2 rounded-lg border px-4 py-3">
                <Label for="interval_hours" class="text-sm font-medium">Sync every (hours)</Label>
                <p class="text-xs text-muted-foreground">
                    Lists each enabled destination's repository through a connected agent at this interval.
                    Destinations with no agent online, or busy with a backup, are retried on the next run.
                    Maximum 720 (30 days); 0 disables the periodic sync.
                </p>
                <Input id="interval_hours" type="number" min="0" :max="720" class="max-w-40"
                    :model-value="settings.interval_hours"
                    @update:model-value="settings.interval_hours = Number($event)" />
            </div>

            <!-- ── Submit ──────────────────────────────────────────────────── -->
            <div class="flex justify-end pt-2">
                <Button type="submit" :disabled="submitting">
                    <Loader2 v-if="submitting" class="size-4 animate-spin" />
                    {{ submitting ? 'Saving…' : 'Save Settings' }}
                </Button>
            </div>

        </div>
    </form>
</template>
