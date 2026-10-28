<script setup lang="ts">
import { ref, onMounted } from 'vue'
import { useForm, useField } from 'vee-validate'
import { toTypedSchema } from '@vee-validate/zod'
import { z } from 'zod'
import { integerInRange } from '@/lib/validation'
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
import { AlertCircle, Loader2, RefreshCw, X } from '@lucide/vue'
import { api, apiErrorMessage } from '@/services/api'
import type { ApiResponse, SMTPSettings } from '@/types'

// ---------------------------------------------------------------------------
// Form
// ---------------------------------------------------------------------------

const loading = ref(false)
const smtpExists = ref(false)
const submitError = ref<string | null>(null)
const success = ref(false)

const schema = toTypedSchema(
    z.object({
        host: z.string().min(1, 'Host is required'),
        port: integerInRange(1, 65535),
        tls: z.boolean(),
        auth_enabled: z.boolean(),
        username: z.string(),
        password: z.string(),
        from: z.email('Must be a valid email address'),
        from_name: z.string(),
        recipients: z.array(z.string()),
        // The address being typed in the recipients input: checked when it is
        // added to the list, not on submit.
        recipient_input: z.string(),
    }),
)

function defaultValues() {
    return {
        host: '',
        port: 587,
        tls: false,
        auth_enabled: false,
        username: '',
        password: '',
        from: '',
        from_name: '',
        recipients: [] as string[],
        recipient_input: '',
    }
}

const { handleSubmit, resetForm, isSubmitting } = useForm({
    validationSchema: schema,
    initialValues: defaultValues(),
})

// Connection
const { value: smtpHost, errorMessage: hostError } = useField<string>('host')
const { value: smtpPort, errorMessage: portError } = useField<number | string>('port')
const { value: smtpTLS } = useField<boolean>('tls')

// Auth
const { value: smtpAuthEnabled } = useField<boolean>('auth_enabled')
const { value: smtpUsername } = useField<string>('username')
const { value: smtpPassword } = useField<string>('password')

// From / recipients
const { value: smtpFrom, errorMessage: fromError } = useField<string>('from')
const { value: smtpFromName } = useField<string>('from_name')
const { value: smtpRecipients } = useField<string[]>('recipients')
const {
    value: recipientInput,
    errorMessage: recipientError,
    setErrors: setRecipientError,
} = useField<string>('recipient_input', undefined, { validateOnValueUpdate: false })

// ---------------------------------------------------------------------------
// Recipients
// ---------------------------------------------------------------------------

function addRecipient() {
    setRecipientError([])
    const email = recipientInput.value.trim()
    if (!email) return
    if (!z.email().safeParse(email).success) {
        setRecipientError('Invalid email address')
        return
    }
    if (smtpRecipients.value.includes(email)) {
        setRecipientError('Already in the list')
        return
    }
    smtpRecipients.value = [...smtpRecipients.value, email]
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
        resetForm({
            values: {
                ...defaultValues(),
                host: s.host ?? '',
                port: s.port ?? 587,
                tls: s.tls ?? false,
                auth_enabled: !!s.username,
                username: s.username ?? '',
                from: s.from ?? '',
                from_name: s.from_name ?? '',
                recipients: s.recipients ?? [],
            },
        })
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

const submit = handleSubmit(async (values) => {
    submitError.value = null
    success.value = false

    try {
        await api('/api/v1/settings/smtp', {
            method: 'PUT',
            body: {
                host: values.host,
                port: values.port,
                tls: values.tls,
                username: values.auth_enabled ? values.username : '',
                password: values.auth_enabled ? values.password : '',
                from: values.from,
                from_name: values.from_name,
                recipients: values.recipients,
            },
        })
        smtpExists.value = true
        smtpPassword.value = ''
        success.value = true
        setTimeout(() => { success.value = false }, 3000)
    } catch (e: any) {
        submitError.value = apiErrorMessage(e, 'Failed to save SMTP settings')
    }
})
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
    <form v-else novalidate @submit="submit">
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
                            :class="hostError ? 'border-destructive focus-visible:ring-destructive/30' : ''" />
                        <FieldError v-if="hostError">{{ hostError }}</FieldError>
                    </Field>
                    <Field>
                        <FieldLabel for="smtp-port">Port</FieldLabel>
                        <Input id="smtp-port" v-model="smtpPort" type="number" placeholder="587"
                            :class="portError ? 'border-destructive focus-visible:ring-destructive/30' : ''" />
                        <FieldError v-if="portError">{{ portError }}</FieldError>
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
                    :class="fromError ? 'border-destructive focus-visible:ring-destructive/30' : ''" />
                <p class="text-xs text-muted-foreground">The sender address shown in notification emails.</p>
                <FieldError v-if="fromError">{{ fromError }}</FieldError>
            </Field>

            <!-- ── From name ───────────────────────────────────────────────── -->
            <Field>
                <FieldLabel for="smtp-from-name">From Name</FieldLabel>
                <Input id="smtp-from-name" v-model="smtpFromName" placeholder="Arkeep" autocomplete="off" />
                <p class="text-xs text-muted-foreground">
                    Display name shown next to the sender address. Defaults to "Arkeep" if left empty.
                </p>
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
                        <FieldError v-if="recipientError" class="mt-1">{{ recipientError }}</FieldError>
                    </div>
                    <Button type="button" variant="outline" @click="addRecipient">Add</Button>
                </div>
            </div>

            <!-- ── Submit ──────────────────────────────────────────────────── -->
            <div class="flex justify-end pt-2">
                <Button type="submit" :disabled="isSubmitting">
                    <Loader2 v-if="isSubmitting" class="size-4 animate-spin" />
                    {{ isSubmitting ? 'Saving…' : 'Save Settings' }}
                </Button>
            </div>

        </FieldGroup>
    </form>
</template>
