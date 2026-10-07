<script setup lang="ts">
import { ref, onMounted } from 'vue'
import { Alert, AlertDescription } from '@/components/ui/alert'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { Skeleton } from '@/components/ui/skeleton'
import { AlertCircle, Loader2, RefreshCw } from '@lucide/vue'
import { api, apiErrorMessage } from '@/services/api'
import type { ApiResponse, JobQueueSettings } from '@/types'

// Mirrors destqueue.MaxTimeoutMinutes on the server (30 days).
const MAX_TIMEOUT_MINUTES = 30 * 24 * 60

// ---------------------------------------------------------------------------
// State
// ---------------------------------------------------------------------------

const loading = ref(false)
const submitting = ref(false)
const submitError = ref<string | null>(null)
const success = ref(false)

const settings = ref<JobQueueSettings>({ timeout_minutes: 1440 })

// ---------------------------------------------------------------------------
// Fetch
// ---------------------------------------------------------------------------

async function fetchSettings() {
    loading.value = true
    try {
        const res = await api<ApiResponse<JobQueueSettings>>('/api/v1/settings/jobs-queue')
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
        await api('/api/v1/settings/jobs-queue', {
            method: 'PUT',
            body: { timeout_minutes: Number(settings.value.timeout_minutes) || 0 },
        })
        success.value = true
        setTimeout(() => { success.value = false }, 3000)
    } catch (e: any) {
        submitError.value = apiErrorMessage(e, 'Failed to save job queue settings')
    } finally {
        submitting.value = false
    }
}
</script>

<template>
    <!-- Section header -->
    <div class="flex items-start justify-between gap-4 mb-6">
        <div>
            <h2 class="text-base font-semibold">Job Queue</h2>
            <p class="mt-1 text-sm text-muted-foreground">
                Only one backup or retention sweep runs against a destination at a time. Jobs that find
                a destination busy wait in the queue and start automatically, oldest first, as soon as
                all their destinations are free.
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
                    <AlertDescription>Job queue settings saved.</AlertDescription>
                </Alert>
            </Transition>

            <!-- ── Timeout ─────────────────────────────────────────────────── -->
            <div class="flex flex-col gap-2 rounded-lg border px-4 py-3">
                <Label for="timeout_minutes" class="text-sm font-medium">Queue timeout (minutes)</Label>
                <p class="text-xs text-muted-foreground">
                    A job still waiting after this long is marked as failed and reported like any other
                    failed backup. 1440 is 24 hours. 0 lets jobs wait indefinitely.
                </p>
                <Input id="timeout_minutes" type="number" min="0" :max="MAX_TIMEOUT_MINUTES" class="max-w-40"
                    :model-value="settings.timeout_minutes"
                    @update:model-value="settings.timeout_minutes = Number($event)" />
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
