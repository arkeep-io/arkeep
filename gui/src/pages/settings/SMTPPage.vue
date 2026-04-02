<script setup lang="ts">
import { ref, computed, onMounted } from 'vue'
import { z } from 'zod'
import { Alert, AlertDescription } from '@/components/ui/alert'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Skeleton } from '@/components/ui/skeleton'
import {
    Field,
    FieldError,
    FieldGroup,
    FieldLabel,
} from '@/components/ui/field'
import { Switch } from '@/components/ui/switch'
import { Separator } from '@/components/ui/separator'
import { AlertCircle, Loader2, RefreshCw, X } from 'lucide-vue-next'
import { api } from '@/services/api'
import type { ApiResponse, SMTPSettings } from '@/types'

// ---------------------------------------------------------------------------
// Form state
// ---------------------------------------------------------------------------

const loading = ref(false)
const smtpExists = ref(false)
const submitting = ref(false)
const submitError = ref<string | null>(null)
const success = ref(false)

// Connection
const smtpHost = ref('')
const smtpPort = ref<number>(587)
const smtpTLS = ref(false)

// Auth
const smtpAuthEnabled = ref(false)
const smtpUsername = ref('')
const smtpPassword = ref('')

// From / recipients
const smtpFrom = ref('')
const smtpRecipients = ref<string[]>([])
const recipientInput = ref('')
const recipientError = ref('')

const errors = ref<Record<string, string>>({})

// ---------------------------------------------------------------------------
// Validation
// ---------------------------------------------------------------------------

const schema = computed(() =>
    z.object({
        host: z.string().min(1, 'Host is required'),
        port: z
            .number()
            .int()
            .min(1, 'Port must be between 1 and 65535')
            .max(65535, 'Port must be between 1 and 65535'),
        from: z.string().email('Must be a valid email address'),
    })
)

function validate(): boolean {
    errors.value = {}
    const result = schema.value.safeParse({
        host: smtpHost.value,
        port: smtpPort.value,
        from: smtpFrom.value,
    })
    if (!result.success) {
        for (const issue of result.error.issues) {
            errors.value[String(issue.path[0])] = issue.message
        }
        return false
    }
    return true
}

// ---------------------------------------------------------------------------
// Recipients
// ---------------------------------------------------------------------------

function addRecipient() {
    recipientError.value = ''
    const email = recipientInput.value.trim()
    if (!email) return
    const parsed = z.string().email().safeParse(email)
    if (!parsed.success) {
        recipientError.value = 'Invalid email address'
        return
    }
    if (smtpRecipients.value.includes(email)) {
        recipientError.value = 'Already in the list'
        return
    }
    smtpRecipients.value.push(email)
    recipientInput.value = ''
}

function removeRecipient(email: string) {
    smtpRecipients.value = smtpRecipients.value.filter(r => r !== email)
}

// ---------------------------------------------------------------------------
// Data fetching
// ---------------------------------------------------------------------------

async function fetchSMTP() {
    loading.value = true
    try {
        const res = await api<ApiResponse<SMTPSettings>>('/api/v1/settings/smtp')
        const s = res.data
        smtpExists.value = true
        smtpHost.value = s.host ?? ''
        smtpPort.value = s.port ?? 587
        smtpTLS.value = s.tls ?? false
        smtpUsername.value = s.username ?? ''
        smtpPassword.value = ''
        smtpAuthEnabled.value = !!s.username
        smtpFrom.value = s.from ?? ''
        smtpRecipients.value = s.recipients ?? []
    } catch (e: any) {
        if (e?.status === 404 || e?.response?.status === 404) {
            smtpExists.value = false
        }
    } finally {
        loading.value = false
    }
}

onMounted(fetchSMTP)

// ---------------------------------------------------------------------------
// Submit
// ---------------------------------------------------------------------------

async function submit() {
    if (!validate()) return

    submitting.value = true
    submitError.value = null
    success.value = false

    try {
        await api('/api/v1/settings/smtp', {
            method: 'PUT',
            body: {
                host: smtpHost.value,
                port: smtpPort.value,
                tls: smtpTLS.value,
                username: smtpAuthEnabled.value ? smtpUsername.value : '',
                password: smtpAuthEnabled.value ? smtpPassword.value : '',
                from: smtpFrom.value,
                recipients: smtpRecipients.value,
            },
        })
        smtpExists.value = true
        smtpPassword.value = ''
        success.value = true
        setTimeout(() => { success.value = false }, 3000)
    } catch (e: any) {
        submitError.value = e?.data?.error?.message ?? e?.message ?? 'Failed to save SMTP settings'
    } finally {
        submitting.value = false
    }
}
</script>

<template>
    <!-- Section header -->
    <div class="flex items-start justify-between gap-4 mb-6">
        <div>
            <h2 class="text-base font-semibold">SMTP</h2>
            <p class="mt-1 text-sm text-muted-foreground">
                Configure an outbound mail server to deliver backup and agent notifications by email.
            </p>
            <p v-if="!loading && !smtpExists" class="mt-2 text-xs text-amber-600 dark:text-amber-400">
                No SMTP server configured yet. Email notifications are disabled.
            </p>
        </div>
        <Button variant="outline" size="icon" aria-label="Refresh" :disabled="loading" @click="fetchSMTP">
            <RefreshCw class="size-4" :class="{ 'animate-spin': loading }" />
        </Button>
    </div>

    <!-- Skeleton -->
    <template v-if="loading">
        <div class="flex flex-col gap-6">
            <Skeleton class="h-20 w-full rounded-lg" />
            <Skeleton class="h-16 w-full rounded-lg" />
            <Skeleton class="h-16 w-full rounded-lg" />
            <Skeleton class="h-28 w-full rounded-lg" />
        </div>
    </template>

    <!-- Form -->
    <form v-else novalidate @submit.prevent="submit">
        <FieldGroup class="flex flex-col gap-6">

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
                    <AlertDescription>SMTP settings saved successfully.</AlertDescription>
                </Alert>
            </Transition>

            <!-- ── Connection ──────────────────────────────────────────────── -->
            <div class="flex flex-col gap-4">
                <p class="text-sm font-medium">Connection</p>

                <div class="grid grid-cols-3 gap-3">
                    <Field class="col-span-2">
                        <FieldLabel for="smtp-host">Host</FieldLabel>
                        <Input id="smtp-host" v-model="smtpHost" placeholder="smtp.example.com" autocomplete="off"
                            :class="errors.host ? 'border-destructive focus-visible:ring-destructive/30' : ''" />
                        <FieldError v-if="errors.host">{{ errors.host }}</FieldError>
                    </Field>
                    <Field>
                        <FieldLabel for="smtp-port">Port</FieldLabel>
                        <Input id="smtp-port" v-model.number="smtpPort" type="number" placeholder="587"
                            :class="errors.port ? 'border-destructive focus-visible:ring-destructive/30' : ''" />
                        <FieldError v-if="errors.port">{{ errors.port }}</FieldError>
                    </Field>
                </div>

                <div class="flex items-center justify-between rounded-lg border px-4 py-3">
                    <div>
                        <p class="text-sm font-medium">Implicit TLS</p>
                        <p class="text-xs text-muted-foreground">
                            Enable for SMTPS on port 465. Leave off for STARTTLS (587) or plaintext.
                        </p>
                    </div>
                    <Switch :model-value="smtpTLS" @update:model-value="smtpTLS = $event" />
                </div>
            </div>

            <Separator />

            <!-- ── Authentication ──────────────────────────────────────────── -->
            <div class="flex flex-col gap-4">
                <div class="flex items-center justify-between rounded-lg border px-4 py-3">
                    <div>
                        <p class="text-sm font-medium">Authentication</p>
                        <p class="text-xs text-muted-foreground">
                            Enable if your SMTP server requires credentials.
                        </p>
                    </div>
                    <Switch :model-value="smtpAuthEnabled" @update:model-value="smtpAuthEnabled = $event" />
                </div>

                <Transition enter-active-class="transition-all duration-200"
                    enter-from-class="opacity-0 -translate-y-1" leave-active-class="transition-all duration-150"
                    leave-to-class="opacity-0 -translate-y-1">
                    <div v-if="smtpAuthEnabled" class="grid grid-cols-2 gap-3">
                        <Field>
                            <FieldLabel for="smtp-username">Username</FieldLabel>
                            <Input id="smtp-username" v-model="smtpUsername" autocomplete="off" />
                        </Field>
                        <Field>
                            <FieldLabel for="smtp-password">
                                Password
                                <span v-if="smtpExists" class="text-muted-foreground font-normal">(optional)</span>
                            </FieldLabel>
                            <Input id="smtp-password" v-model="smtpPassword" type="password"
                                :placeholder="smtpExists ? '(unchanged)' : ''" autocomplete="new-password" />
                        </Field>
                    </div>
                </Transition>
            </div>

            <Separator />

            <!-- ── From address ────────────────────────────────────────────── -->
            <Field>
                <FieldLabel for="smtp-from">From Address</FieldLabel>
                <Input id="smtp-from" v-model="smtpFrom" placeholder="arkeep@example.com" autocomplete="off"
                    :class="errors.from ? 'border-destructive focus-visible:ring-destructive/30' : ''" />
                <p class="text-xs text-muted-foreground">The sender address shown in notification emails.</p>
                <FieldError v-if="errors.from">{{ errors.from }}</FieldError>
            </Field>

            <Separator />

            <!-- ── Notification recipients ─────────────────────────────────── -->
            <div class="flex flex-col gap-3">
                <div>
                    <p class="text-sm font-medium">Notification Recipients</p>
                    <p class="text-xs text-muted-foreground mt-0.5">
                        Email addresses that receive backup and agent notifications.
                        When empty, all active admin accounts are notified.
                    </p>
                </div>

                <!-- Chips -->
                <div v-if="smtpRecipients.length" class="flex flex-wrap gap-1.5">
                    <span v-for="email in smtpRecipients" :key="email"
                        class="inline-flex items-center gap-1 rounded-full border bg-secondary px-2.5 py-0.5 text-xs font-medium text-secondary-foreground">
                        {{ email }}
                        <button type="button" :aria-label="`Remove ${email}`"
                            class="ml-0.5 rounded-full text-muted-foreground transition-colors hover:text-foreground focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring"
                            @click="removeRecipient(email)">
                            <X class="size-3" />
                        </button>
                    </span>
                </div>

                <!-- Add input -->
                <div class="flex gap-2">
                    <div class="flex-1">
                        <Input id="smtp-recipient-input" v-model="recipientInput" type="email"
                            placeholder="admin@company.com" autocomplete="off"
                            :class="recipientError ? 'border-destructive focus-visible:ring-destructive/30' : ''"
                            @keydown.enter.prevent="addRecipient" />
                        <p v-if="recipientError" class="mt-1 text-xs text-destructive">{{ recipientError }}</p>
                    </div>
                    <Button type="button" variant="outline" @click="addRecipient">Add</Button>
                </div>
            </div>

            <!-- ── Submit ──────────────────────────────────────────────────── -->
            <div class="flex justify-end pt-2">
                <Button type="submit" :disabled="submitting">
                    <Loader2 v-if="submitting" class="size-4 animate-spin" />
                    {{ submitting ? 'Saving…' : 'Save Settings' }}
                </Button>
            </div>

        </FieldGroup>
    </form>
</template>
