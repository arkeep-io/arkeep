<script setup lang="ts">
import { ref, onMounted } from 'vue'
import { z } from 'zod'
import { Alert, AlertDescription } from '@/components/ui/alert'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Skeleton } from '@/components/ui/skeleton'
import {
    Field,
    FieldError,
    FieldLabel,
} from '@/components/ui/field'
import { Switch } from '@/components/ui/switch'
import { Separator } from '@/components/ui/separator'
import { AlertCircle, Loader2, RefreshCw } from '@lucide/vue'
import { api } from '@/services/api'
import type { ApiResponse, NotificationSettings, WebhookSettings } from '@/types'

// ---------------------------------------------------------------------------
// State
// ---------------------------------------------------------------------------

const loading = ref(false)
const submitting = ref(false)
const submitError = ref<string | null>(null)
const success = ref(false)

const settings = ref<NotificationSettings>({
    job_success: true,
    job_failure: true,
    agent_offline: true,
    agent_online: false,
})

// Webhook — see README's "Configuring notifications" section, which already
// documents this as living on this page.
const webhookExists = ref(false)
const webhookEnabled = ref(false)
const webhookUrl = ref('')
const webhookSecret = ref('')
const webhookUrlError = ref('')

// ---------------------------------------------------------------------------
// Validation
// ---------------------------------------------------------------------------

function validate(): boolean {
    webhookUrlError.value = ''
    if (!webhookEnabled.value) return true
    const result = z.string().url().safeParse(webhookUrl.value)
    if (!result.success) {
        webhookUrlError.value = 'Must be a valid URL'
        return false
    }
    return true
}

// ---------------------------------------------------------------------------
// Fetch
// ---------------------------------------------------------------------------

async function fetchSettings() {
    const res = await api<ApiResponse<NotificationSettings>>('/api/v1/settings/notifications')
    settings.value = res.data
}

async function fetchWebhook() {
    try {
        const res = await api<ApiResponse<WebhookSettings>>('/api/v1/settings/webhook')
        const s = res.data
        webhookExists.value = true
        webhookUrl.value = s.url ?? ''
        webhookSecret.value = ''
        webhookEnabled.value = s.enabled ?? false
    } catch (e: any) {
        if (e?.status === 404 || e?.response?.status === 404) {
            webhookExists.value = false
        }
    }
}

async function fetchAll() {
    loading.value = true
    try {
        await Promise.all([fetchSettings(), fetchWebhook()])
    } finally {
        loading.value = false
    }
}

onMounted(fetchAll)

// ---------------------------------------------------------------------------
// Submit
// ---------------------------------------------------------------------------

async function submit() {
    if (!validate()) return

    submitting.value = true
    submitError.value = null
    success.value = false

    try {
        await Promise.all([
            api('/api/v1/settings/notifications', {
                method: 'PUT',
                body: settings.value,
            }),
            api('/api/v1/settings/webhook', {
                method: 'PUT',
                body: {
                    url: webhookEnabled.value ? webhookUrl.value : '',
                    secret: webhookSecret.value,
                    enabled: webhookEnabled.value,
                },
            }),
        ])
        webhookExists.value = true
        webhookSecret.value = ''
        success.value = true
        setTimeout(() => { success.value = false }, 3000)
    } catch (e: any) {
        submitError.value = e?.data?.error?.message ?? e?.message ?? 'Failed to save notification settings'
    } finally {
        submitting.value = false
    }
}
</script>

<template>
    <!-- Section header -->
    <div class="flex items-start justify-between gap-4 mb-6">
        <div>
            <h2 class="text-base font-semibold">Notification Events</h2>
            <p class="mt-1 text-sm text-muted-foreground">
                Choose which events trigger external notifications (email and webhook).
                In-app notifications are always shown regardless of these settings.
            </p>
        </div>
        <Button variant="outline" size="icon" aria-label="Refresh" :disabled="loading" @click="fetchAll">
            <RefreshCw class="size-4" :class="{ 'animate-spin': loading }" />
        </Button>
    </div>

    <!-- Skeleton -->
    <template v-if="loading">
        <div class="flex flex-col gap-4">
            <Skeleton class="h-14 w-full rounded-lg" />
            <Skeleton class="h-14 w-full rounded-lg" />
            <Skeleton class="h-px w-full" />
            <Skeleton class="h-14 w-full rounded-lg" />
            <Skeleton class="h-14 w-full rounded-lg" />
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
                    <AlertDescription>Notification settings saved.</AlertDescription>
                </Alert>
            </Transition>

            <!-- ── Backup jobs ──────────────────────────────────────────────── -->
            <div class="flex flex-col gap-3">
                <p class="text-sm font-medium">Backup Jobs</p>

                <div class="flex items-center justify-between rounded-lg border px-4 py-3">
                    <div>
                        <p class="text-sm font-medium">Job failed</p>
                        <p class="text-xs text-muted-foreground">
                            Send a notification when a backup job fails.
                        </p>
                    </div>
                    <Switch :model-value="settings.job_failure" @update:model-value="settings.job_failure = $event" />
                </div>

                <div class="flex items-center justify-between rounded-lg border px-4 py-3">
                    <div>
                        <p class="text-sm font-medium">Job succeeded</p>
                        <p class="text-xs text-muted-foreground">
                            Send a notification when a backup job completes successfully.
                        </p>
                    </div>
                    <Switch :model-value="settings.job_success" @update:model-value="settings.job_success = $event" />
                </div>
            </div>

            <Separator />

            <!-- ── Agents ──────────────────────────────────────────────────── -->
            <div class="flex flex-col gap-3">
                <p class="text-sm font-medium">Agents</p>

                <div class="flex items-center justify-between rounded-lg border px-4 py-3">
                    <div>
                        <p class="text-sm font-medium">Agent went offline</p>
                        <p class="text-xs text-muted-foreground">
                            Send a notification when an agent stops responding.
                        </p>
                    </div>
                    <Switch :model-value="settings.agent_offline"
                        @update:model-value="settings.agent_offline = $event" />
                </div>

                <div class="flex items-center justify-between rounded-lg border px-4 py-3">
                    <div>
                        <p class="text-sm font-medium">Agent came back online</p>
                        <p class="text-xs text-muted-foreground">
                            Send a notification when an agent reconnects. Off by default.
                        </p>
                    </div>
                    <Switch :model-value="settings.agent_online"
                        @update:model-value="settings.agent_online = $event" />
                </div>
            </div>

            <Separator />

            <!-- ── Webhook ─────────────────────────────────────────────────── -->
            <div class="flex flex-col gap-4">
                <div class="flex items-center justify-between rounded-lg border px-4 py-3">
                    <div>
                        <p class="text-sm font-medium">Send webhook notifications</p>
                        <p class="text-xs text-muted-foreground">
                            HTTP POST a JSON payload to a URL on every enabled event above.
                        </p>
                    </div>
                    <Switch :model-value="webhookEnabled" @update:model-value="webhookEnabled = $event" />
                </div>

                <Transition enter-active-class="transition-all duration-200"
                    enter-from-class="opacity-0 -translate-y-1" leave-active-class="transition-all duration-150"
                    leave-to-class="opacity-0 -translate-y-1">
                    <div v-if="webhookEnabled" class="flex flex-col gap-4">
                        <Field>
                            <FieldLabel for="webhook-url">Webhook URL</FieldLabel>
                            <Input id="webhook-url" v-model="webhookUrl" placeholder="https://hooks.example.com/arkeep"
                                autocomplete="off"
                                :class="webhookUrlError ? 'border-destructive focus-visible:ring-destructive/30' : ''" />
                            <FieldError v-if="webhookUrlError">{{ webhookUrlError }}</FieldError>
                        </Field>
                        <Field>
                            <FieldLabel for="webhook-secret">
                                Webhook Secret
                                <span v-if="webhookExists" class="text-muted-foreground font-normal">(optional)</span>
                            </FieldLabel>
                            <Input id="webhook-secret" v-model="webhookSecret" type="password"
                                :placeholder="webhookExists ? '(unchanged)' : ''" autocomplete="new-password" />
                            <p class="text-xs text-muted-foreground">
                                Used to sign each request with an <code>X-Arkeep-Signature</code> header
                                (HMAC-SHA256) so the receiving endpoint can verify it came from Arkeep.
                            </p>
                        </Field>
                    </div>
                </Transition>
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
