<script setup lang="ts">
import { ref, onMounted, watch } from 'vue'
import { useForm, useField } from 'vee-validate'
import { toTypedSchema } from '@vee-validate/zod'
import { z } from 'zod'
import { integerInRange, isHttpURL } from '@/lib/validation'
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
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from '@/components/ui/select'
import { Separator } from '@/components/ui/separator'
import { Loader2, Mail, RefreshCw, Send, Webhook } from '@lucide/vue'
import { api, apiErrorMessage } from '@/services/api'
import NotificationProviders from '@/components/notifications/NotificationProviders.vue'
import UnsavedChangesBar from '@/components/shared/UnsavedChangesBar.vue'
import type { ApiResponse, NotificationSettings, ReportFrequency, WebhookSettings } from '@/types'

// ---------------------------------------------------------------------------
// Form
// ---------------------------------------------------------------------------

const loading = ref(false)
const submitError = ref<string | null>(null)
const success = ref(false)

// Webhook — see the Notifications guide in the docs (arkeep.io/docs/guides/notifications/),
// which already documents this as living on this page.
const webhookExists = ref(false)

// The server's time zone (e.g. "CET"), in which the daily job report time is.
const serverTimezone = ref('')

// The webhook URL is checked only when the webhook is enabled: a disabled
// webhook is saved without one.
const schema = toTypedSchema(
    z.object({
        job_success: z.boolean(),
        job_failure: z.boolean(),
        agent_offline: z.boolean(),
        agent_online: z.boolean(),
        email_enabled: z.boolean(),
        report_frequency: z.enum(['off', 'hourly', 'daily']),
        report_time: z.string().regex(/^([01][0-9]|2[0-3]):[0-5][0-9]$/, 'Must be a time (HH:MM)'),
        report_minute: integerInRange(0, 59),
        webhook_enabled: z.boolean(),
        webhook_url: z.string(),
        webhook_secret: z.string(),
    }).superRefine((data, ctx) => {
        if (data.webhook_enabled && !isHttpURL(data.webhook_url.trim())) {
            ctx.addIssue({ code: 'custom', path: ['webhook_url'], message: 'Must be an http(s) URL' })
        }
    }),
)

const { handleSubmit, resetForm, isSubmitting, meta } = useForm({
    validationSchema: schema,
    initialValues: {
        job_success: true,
        job_failure: true,
        agent_offline: true,
        agent_online: false,
        email_enabled: true,
        report_frequency: 'off' as ReportFrequency,
        report_time: '07:00',
        report_minute: 0,
        webhook_enabled: false,
        webhook_url: '',
        webhook_secret: '',
    },
})

const { value: jobSuccess } = useField<boolean>('job_success')
const { value: jobFailure } = useField<boolean>('job_failure')
const { value: agentOffline } = useField<boolean>('agent_offline')
const { value: agentOnline } = useField<boolean>('agent_online')
const { value: emailEnabled } = useField<boolean>('email_enabled')
const { value: reportFrequency } = useField<ReportFrequency>('report_frequency')
const { value: reportTime, errorMessage: reportTimeError, resetField: resetReportTime } = useField<string>('report_time')
const {
    value: reportMinute,
    errorMessage: reportMinuteError,
    resetField: resetReportMinute,
} = useField<number | string>('report_minute')

// Only the field of the chosen frequency is shown. The other one goes back to
// its saved value, so an unsaved, invalid entry in a hidden field cannot block
// saving without a visible error.
watch(reportFrequency, (frequency) => {
    if (frequency !== 'hourly') resetReportMinute()
    if (frequency !== 'daily') resetReportTime()
})
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
        const [{ server_timezone, ...events }, webhook] = await Promise.all([fetchSettings(), fetchWebhook()])
        serverTimezone.value = server_timezone
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
                    email_enabled: values.email_enabled,
                    report_frequency: values.report_frequency,
                    report_time: values.report_time,
                    report_minute: values.report_minute,
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
        // The saved values, without the secret the server keeps, become the
        // ones Discard returns to.
        resetForm({ values: { ...values, webhook_secret: '' } })
        success.value = true
        setTimeout(() => { success.value = false }, 3000)
    } catch (e: any) {
        submitError.value = apiErrorMessage(e, 'Failed to save notification settings')
    }
})

// ---------------------------------------------------------------------------
// Test webhook
// ---------------------------------------------------------------------------

// The server posts a "test" event to the URL in the form, saved or not, signed
// with the secret in the form or, when it is blank, with the saved one.
const testingWebhook = ref(false)
const webhookTestResult = ref<{ ok: boolean; message: string } | null>(null)

const sendTestWebhook = handleSubmit(async (values) => {
    webhookTestResult.value = null
    testingWebhook.value = true
    try {
        await api('/api/v1/settings/webhook/test', {
            method: 'POST',
            body: { url: values.webhook_url.trim(), secret: values.webhook_secret },
        })
        webhookTestResult.value = { ok: true, message: 'Test webhook delivered.' }
    } catch (e: any) {
        webhookTestResult.value = { ok: false, message: apiErrorMessage(e, 'Failed to send the test webhook') }
    } finally {
        testingWebhook.value = false
    }
})

// discard returns the form to the saved values.
function discard() {
    submitError.value = null
    resetForm()
}
</script>

<template>
    <!-- Section header -->
    <div class="flex items-start justify-between gap-4 mb-6">
        <div>
            <h2 class="text-base font-semibold">Notifications</h2>
            <p class="mt-1 text-sm text-muted-foreground">
                Which events Arkeep reports, and the channels it sends them to. In-app notifications are always
                shown regardless of these settings.
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

    <template v-else>
        <form novalidate @submit="submit">
            <div class="flex flex-col gap-6">

                <Transition enter-active-class="transition-all duration-200" enter-from-class="-translate-y-1 opacity-0"
                    leave-active-class="transition-all duration-150" leave-to-class="-translate-y-1 opacity-0">
                    <Alert v-if="success"
                        class="border-emerald-500/30 bg-emerald-500/5 text-emerald-600 dark:text-emerald-400">
                        <AlertDescription>Notification settings saved.</AlertDescription>
                    </Alert>
                </Transition>

                <!-- ── Events ──────────────────────────────────────────────── -->
                <section class="flex flex-col gap-3">
                    <div>
                        <h3 class="text-sm font-medium">Events</h3>
                        <p class="text-xs text-muted-foreground">Sent to every enabled channel below.</p>
                    </div>

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
                </section>

                <Separator />

                <!-- ── Channels: email and webhook ─────────────────────────── -->
                <section class="flex flex-col gap-3">
                    <div>
                        <h3 class="text-sm font-medium">Channels</h3>
                        <p class="text-xs text-muted-foreground">Where Arkeep sends the events above.</p>
                    </div>

                    <div class="flex items-center justify-between gap-4 rounded-lg border px-4 py-3">
                        <div class="flex items-center gap-3 min-w-0">
                            <div class="flex size-8 shrink-0 items-center justify-center rounded-md bg-muted">
                                <Mail class="size-4" />
                            </div>
                            <div class="min-w-0">
                                <p class="text-sm font-medium">Email</p>
                                <p class="text-xs text-muted-foreground">
                                    To the notification recipients set in SMTP settings. Password reset emails are
                                    sent either way.
                                </p>
                            </div>
                        </div>
                        <Switch :model-value="emailEnabled" @update:model-value="emailEnabled = $event" />
                    </div>

                    <div class="rounded-lg border">
                        <div class="flex items-center justify-between gap-4 px-4 py-3">
                            <div class="flex items-center gap-3 min-w-0">
                                <div class="flex size-8 shrink-0 items-center justify-center rounded-md bg-muted">
                                    <Webhook class="size-4" />
                                </div>
                                <div class="min-w-0">
                                    <p class="text-sm font-medium">Webhook</p>
                                    <p class="text-xs text-muted-foreground">
                                        An HTTP POST with a JSON payload to a URL of yours.
                                    </p>
                                </div>
                            </div>
                            <Switch :model-value="webhookEnabled" @update:model-value="webhookEnabled = $event" />
                        </div>

                        <Transition enter-active-class="transition-all duration-200"
                            enter-from-class="opacity-0 -translate-y-1" leave-active-class="transition-all duration-150"
                            leave-to-class="opacity-0 -translate-y-1">
                            <div v-if="webhookEnabled" class="flex flex-col gap-4 border-t px-4 py-4">
                                <Field>
                                    <FieldLabel for="webhook-url">Webhook URL</FieldLabel>
                                    <Input id="webhook-url" v-model="webhookUrl"
                                        placeholder="https://hooks.example.com/arkeep" autocomplete="off"
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
                                <div class="flex flex-col items-start gap-2">
                                    <Button type="button" variant="outline" size="sm" :disabled="testingWebhook"
                                        @click="sendTestWebhook">
                                        <Loader2 v-if="testingWebhook" class="size-4 animate-spin" />
                                        <Send v-else class="size-4" />
                                        {{ testingWebhook ? 'Sending…' : 'Send test webhook' }}
                                    </Button>
                                    <p v-if="webhookTestResult" data-testid="webhook-test-result" class="text-xs"
                                        :class="webhookTestResult.ok ? 'text-emerald-600 dark:text-emerald-400' : 'text-destructive'">
                                        {{ webhookTestResult.message }}
                                    </p>
                                </div>
                            </div>
                        </Transition>
                    </div>
                </section>

            </div>
        </form>

        <!-- Providers are saved on their own, from their sheet, outside the form above. -->
        <div class="mt-3">
            <NotificationProviders />
        </div>

        <Separator class="my-6" />

        <!-- Saved with the form above, from the bar below. -->
        <!-- ── Job report ──────────────────────────────────────────── -->
        <section class="flex flex-col gap-3">
            <div>
                <h3 class="text-sm font-medium">Job report</h3>
                <p class="text-xs text-muted-foreground">
                    One email listing all jobs that finished since the previous report, sent to the
                    notification recipients. Skipped when no job finished.
                </p>
            </div>

            <div class="rounded-lg border">
                <div class="flex items-center justify-between gap-4 px-4 py-3">
                    <div class="min-w-0">
                        <p class="text-sm font-medium">Frequency</p>
                        <p class="text-xs text-muted-foreground">
                            Hourly at a set minute, or daily at a set time.
                        </p>
                    </div>
                    <Select :model-value="reportFrequency"
                        @update:model-value="reportFrequency = $event as ReportFrequency">
                        <SelectTrigger id="report-frequency" class="w-32 shrink-0" aria-label="Report frequency">
                            <SelectValue />
                        </SelectTrigger>
                        <SelectContent>
                            <SelectItem value="off">Off</SelectItem>
                            <SelectItem value="hourly">Hourly</SelectItem>
                            <SelectItem value="daily">Daily</SelectItem>
                        </SelectContent>
                    </Select>
                </div>

                <!-- The error spans the row, right-aligned under the input, so it is not
                     wrapped to the width of the input. -->
                <div v-if="reportFrequency === 'hourly'" class="flex flex-col gap-1.5 border-t px-4 py-3">
                    <div class="flex items-center justify-between gap-4">
                        <div class="min-w-0">
                            <FieldLabel for="report-minute" class="text-sm font-medium">Minute</FieldLabel>
                            <p class="text-xs text-muted-foreground">Minutes past every hour, e.g. 30 for XX:30.</p>
                        </div>
                        <Input id="report-minute" v-model="reportMinute" type="number" min="0" max="59"
                            class="w-32 shrink-0"
                            :class="reportMinuteError ? 'border-destructive focus-visible:ring-destructive/30' : ''" />
                    </div>
                    <FieldError v-if="reportMinuteError" class="text-right">{{ reportMinuteError }}</FieldError>
                </div>

                <div v-if="reportFrequency === 'daily'" class="flex flex-col gap-1.5 border-t px-4 py-3">
                    <div class="flex items-center justify-between gap-4">
                        <div class="min-w-0">
                            <FieldLabel for="report-time" class="text-sm font-medium">Time</FieldLabel>
                            <p class="text-xs text-muted-foreground">
                                Server time ({{ serverTimezone || 'unknown zone' }}).
                            </p>
                        </div>
                        <!-- The browser draws the picker icon: dark:scheme-dark makes it light in dark mode. -->
                        <Input id="report-time" v-model="reportTime" type="time" class="w-32 shrink-0 dark:scheme-dark"
                            :class="reportTimeError ? 'border-destructive focus-visible:ring-destructive/30' : ''" />
                    </div>
                    <FieldError v-if="reportTimeError" class="text-right">{{ reportTimeError }}</FieldError>
                </div>
            </div>
        </section>

        <!-- Saves the events, email, webhook and job report. -->
        <UnsavedChangesBar :dirty="meta.dirty" :saving="isSubmitting" :error="submitError"
            @save="submit" @discard="discard" />
    </template>
</template>
