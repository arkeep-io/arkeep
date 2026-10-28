<script setup lang="ts">
import { ref, onMounted } from 'vue'
import { useForm, useField } from 'vee-validate'
import { toTypedSchema } from '@vee-validate/zod'
import { z } from 'zod'
import { isHttpURL } from '@/lib/validation'
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
import { api, apiErrorMessage } from '@/services/api'
import type { ApiResponse, NotificationSettings, WebhookSettings } from '@/types'

// ---------------------------------------------------------------------------
// Form
// ---------------------------------------------------------------------------

const loading = ref(false)
const submitError = ref<string | null>(null)
const success = ref(false)

// Webhook — see the Notifications guide in the docs (arkeep.io/docs/guides/notifications/),
// which already documents this as living on this page.
const webhookExists = ref(false)

// The webhook URL is checked only when the webhook is enabled: a disabled
// webhook is saved without one.
const schema = toTypedSchema(
    z.object({
        job_success: z.boolean(),
        job_failure: z.boolean(),
        agent_offline: z.boolean(),
        agent_online: z.boolean(),
        webhook_enabled: z.boolean(),
        webhook_url: z.string(),
        webhook_secret: z.string(),
    }).superRefine((data, ctx) => {
        if (data.webhook_enabled && !isHttpURL(data.webhook_url.trim())) {
            ctx.addIssue({ code: 'custom', path: ['webhook_url'], message: 'Must be an http(s) URL' })
        }
    }),
)

const { handleSubmit, resetForm, setValues, isSubmitting } = useForm({
    validationSchema: schema,
    initialValues: {
        job_success: true,
        job_failure: true,
        agent_offline: true,
        agent_online: false,
        webhook_enabled: false,
        webhook_url: '',
        webhook_secret: '',
    },
})

const { value: jobSuccess } = useField<boolean>('job_success')
const { value: jobFailure } = useField<boolean>('job_failure')
const { value: agentOffline } = useField<boolean>('agent_offline')
const { value: agentOnline } = useField<boolean>('agent_online')
const { value: webhookEnabled } = useField<boolean>('webhook_enabled')
const { value: webhookUrl, errorMessage: webhookUrlError } = useField<string>('webhook_url')
const { value: webhookSecret } = useField<string>('webhook_secret')

// ---------------------------------------------------------------------------
// Fetch
// ---------------------------------------------------------------------------

async function fetchSettings(): Promise<NotificationSettings> {
    const res = await api<ApiResponse<NotificationSettings>>('/api/v1/settings/notifications')
    return res.data
}

// fetchWebhook returns the stored webhook, or null when none is configured.
async function fetchWebhook(): Promise<WebhookSettings | null> {
    try {
        const res = await api<ApiResponse<WebhookSettings>>('/api/v1/settings/webhook')
        webhookExists.value = true
        return res.data
    } catch (e: any) {
        if (e?.status === 404 || e?.response?.status === 404) {
            webhookExists.value = false
        }
        return null
    }
}

async function fetchAll() {
    loading.value = true
    try {
        const [events, webhook] = await Promise.all([fetchSettings(), fetchWebhook()])
        resetForm({
            values: {
                ...events,
                webhook_enabled: webhook?.enabled ?? false,
                webhook_url: webhook?.url ?? '',
                webhook_secret: '',
            },
        })
    } finally {
        loading.value = false
    }
}

onMounted(fetchAll)

// ---------------------------------------------------------------------------
// Submit
// ---------------------------------------------------------------------------

const submit = handleSubmit(async (values) => {
    submitError.value = null
    success.value = false

    try {
        await Promise.all([
            api('/api/v1/settings/notifications', {
                method: 'PUT',
                body: {
                    job_success: values.job_success,
                    job_failure: values.job_failure,
                    agent_offline: values.agent_offline,
                    agent_online: values.agent_online,
                },
            }),
            api('/api/v1/settings/webhook', {
                method: 'PUT',
                body: {
                    url: values.webhook_enabled ? values.webhook_url.trim() : '',
                    secret: values.webhook_secret,
                    enabled: values.webhook_enabled,
                },
            }),
        ])
        webhookExists.value = true
        setValues({ webhook_secret: '' }, false)
        success.value = true
        setTimeout(() => { success.value = false }, 3000)
    } catch (e: any) {
        submitError.value = apiErrorMessage(e, 'Failed to save notification settings')
    }
})
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
    <form v-else novalidate @submit="submit">
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
                    <Switch :model-value="jobFailure" @update:model-value="jobFailure = $event" />
                </div>

                <div class="flex items-center justify-between rounded-lg border px-4 py-3">
                    <div>
                        <p class="text-sm font-medium">Job succeeded</p>
                        <p class="text-xs text-muted-foreground">
                            Send a notification when a backup job completes successfully.
                        </p>
                    </div>
                    <Switch :model-value="jobSuccess" @update:model-value="jobSuccess = $event" />
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
                    <Switch :model-value="agentOffline"
                        @update:model-value="agentOffline = $event" />
                </div>

                <div class="flex items-center justify-between rounded-lg border px-4 py-3">
                    <div>
                        <p class="text-sm font-medium">Agent came back online</p>
                        <p class="text-xs text-muted-foreground">
                            Send a notification when an agent reconnects. Off by default.
                        </p>
                    </div>
                    <Switch :model-value="agentOnline"
                        @update:model-value="agentOnline = $event" />
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
                <Button type="submit" :disabled="isSubmitting">
                    <Loader2 v-if="isSubmitting" class="size-4 animate-spin" />
                    {{ isSubmitting ? 'Saving…' : 'Save Settings' }}
                </Button>
            </div>

        </div>
    </form>
</template>
