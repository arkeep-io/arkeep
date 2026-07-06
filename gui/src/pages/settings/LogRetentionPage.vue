<script setup lang="ts">
import { ref, onMounted } from 'vue'
import { Alert, AlertDescription } from '@/components/ui/alert'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { Skeleton } from '@/components/ui/skeleton'
import { Separator } from '@/components/ui/separator'
import { AlertCircle, Loader2, RefreshCw, Trash2 } from '@lucide/vue'
import { api } from '@/services/api'
import type { ApiResponse, LogRetentionSettings } from '@/types'

// ---------------------------------------------------------------------------
// State
// ---------------------------------------------------------------------------

const loading = ref(false)
const submitting = ref(false)
const pruning = ref(false)
const submitError = ref<string | null>(null)
const success = ref(false)
const pruneMessage = ref<string | null>(null)

const settings = ref<LogRetentionSettings>({
    info_days: 0,
    warn_error_days: 0,
})

// ---------------------------------------------------------------------------
// Fetch
// ---------------------------------------------------------------------------

async function fetchSettings() {
    loading.value = true
    try {
        const res = await api<ApiResponse<LogRetentionSettings>>('/api/v1/settings/logs')
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
        await api('/api/v1/settings/logs', {
            method: 'PUT',
            body: {
                info_days: Number(settings.value.info_days) || 0,
                warn_error_days: Number(settings.value.warn_error_days) || 0,
            },
        })
        success.value = true
        setTimeout(() => { success.value = false }, 3000)
    } catch (e: any) {
        submitError.value = e?.data?.error?.message ?? e?.message ?? 'Failed to save log retention settings'
    } finally {
        submitting.value = false
    }
}

// ---------------------------------------------------------------------------
// Manual prune
// ---------------------------------------------------------------------------

async function pruneNow() {
    pruning.value = true
    submitError.value = null
    pruneMessage.value = null

    try {
        const res = await api<ApiResponse<{ deleted: number }>>('/api/v1/settings/logs/prune', {
            method: 'POST',
        })
        pruneMessage.value = `Cleanup complete — ${res.data.deleted.toLocaleString()} log line(s) removed.`
        setTimeout(() => { pruneMessage.value = null }, 6000)
    } catch (e: any) {
        submitError.value = e?.data?.error?.message ?? e?.message ?? 'Failed to run cleanup'
    } finally {
        pruning.value = false
    }
}
</script>

<template>
    <!-- Section header -->
    <div class="flex items-start justify-between gap-4 mb-6">
        <div>
            <h2 class="text-base font-semibold">Log Retention</h2>
            <p class="mt-1 text-sm text-muted-foreground">
                Automatically prune old job log lines so the database does not grow without bound.
                Only the verbose log lines are removed — jobs and their outcomes are always kept.
                Set a value to <strong>0</strong> to keep those lines forever (disabled).
            </p>
        </div>
        <Button variant="outline" size="icon" aria-label="Refresh" :disabled="loading" @click="fetchSettings">
            <RefreshCw class="size-4" :class="{ 'animate-spin': loading }" />
        </Button>
    </div>

    <!-- Skeleton -->
    <template v-if="loading">
        <div class="flex flex-col gap-4">
            <Skeleton class="h-16 w-full rounded-lg" />
            <Skeleton class="h-16 w-full rounded-lg" />
        </div>
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
                    <AlertDescription>Log retention settings saved.</AlertDescription>
                </Alert>
            </Transition>

            <Transition enter-active-class="transition-all duration-200" enter-from-class="-translate-y-1 opacity-0"
                leave-active-class="transition-all duration-150" leave-to-class="-translate-y-1 opacity-0">
                <Alert v-if="pruneMessage"
                    class="border-emerald-500/30 bg-emerald-500/5 text-emerald-600 dark:text-emerald-400">
                    <AlertDescription>{{ pruneMessage }}</AlertDescription>
                </Alert>
            </Transition>

            <!-- ── Retention windows ────────────────────────────────────────── -->
            <div class="flex flex-col gap-4">
                <div class="flex flex-col gap-2 rounded-lg border px-4 py-3">
                    <Label for="info_days" class="text-sm font-medium">Info logs — keep for (days)</Label>
                    <p class="text-xs text-muted-foreground">
                        Deletes <code>info</code> log lines older than this many days. These are the bulk
                        of the logs. 0 keeps them forever.
                    </p>
                    <Input id="info_days" type="number" min="0" :max="3650" class="max-w-40"
                        :model-value="settings.info_days"
                        @update:model-value="settings.info_days = Number($event)" />
                </div>

                <div class="flex flex-col gap-2 rounded-lg border px-4 py-3">
                    <Label for="warn_error_days" class="text-sm font-medium">Warning &amp; error logs — keep for (days)</Label>
                    <p class="text-xs text-muted-foreground">
                        Deletes <code>warn</code> and <code>error</code> log lines older than this many days.
                        Leave at 0 to keep them forever (recommended — they are few and the most useful).
                    </p>
                    <Input id="warn_error_days" type="number" min="0" :max="3650" class="max-w-40"
                        :model-value="settings.warn_error_days"
                        @update:model-value="settings.warn_error_days = Number($event)" />
                </div>
            </div>

            <!-- ── Submit ──────────────────────────────────────────────────── -->
            <div class="flex justify-end pt-2">
                <Button type="submit" :disabled="submitting">
                    <Loader2 v-if="submitting" class="size-4 animate-spin" />
                    {{ submitting ? 'Saving…' : 'Save Settings' }}
                </Button>
            </div>

            <Separator />

            <!-- ── Manual cleanup ──────────────────────────────────────────── -->
            <div class="flex items-center justify-between rounded-lg border px-4 py-3">
                <div>
                    <p class="text-sm font-medium">Run cleanup now</p>
                    <p class="text-xs text-muted-foreground">
                        Immediately prune old logs using the saved retention windows, then reclaim disk space.
                        Useful if the database is already large. Save your settings first.
                    </p>
                </div>
                <Button type="button" variant="outline" :disabled="pruning" @click="pruneNow">
                    <Loader2 v-if="pruning" class="size-4 animate-spin" />
                    <Trash2 v-else class="size-4" />
                    {{ pruning ? 'Cleaning…' : 'Run cleanup' }}
                </Button>
            </div>

        </div>
    </form>
</template>
