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
import { AlertCircle, Loader2, RefreshCw } from 'lucide-vue-next'
import { api } from '@/services/api'
import type { ApiResponse, SMTPSettings } from '@/types'

// ---------------------------------------------------------------------------
// Form state
// ---------------------------------------------------------------------------

const loading = ref(false)
const smtpExists = ref(false)
const smtpSubmitting = ref(false)
const smtpSubmitError = ref<string | null>(null)
const smtpSuccess = ref(false)

const smtpHost = ref('')
const smtpPort = ref<number>(587)
const smtpUsername = ref('')
const smtpPassword = ref('')
const smtpFrom = ref('')
const smtpTLS = ref(false)

const smtpErrors = ref<Record<string, string>>({})

// Password is required only when creating a new SMTP config.
// On update (smtpExists), an empty password means "keep the existing one".
const smtpSchema = computed(() =>
    z.object({
        host: z.string().min(1, 'Host is required'),
        port: z
            .number()
            .int()
            .min(1, 'Port must be between 1 and 65535')
            .max(65535, 'Port must be between 1 and 65535'),
        password: smtpExists.value
            ? z.string()
            : z.string().min(1, 'Password is required'),
        from: z.string().email('Must be a valid email address'),
    })
)

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
        smtpUsername.value = s.username ?? ''
        smtpPassword.value = ''
        smtpFrom.value = s.from ?? ''
        smtpTLS.value = s.tls ?? false
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
// Validate + submit
// ---------------------------------------------------------------------------

function validateSMTP(): boolean {
    smtpErrors.value = {}
    const result = smtpSchema.value.safeParse({
        host: smtpHost.value,
        port: smtpPort.value,
        password: smtpPassword.value,
        from: smtpFrom.value,
    })
    if (!result.success) {
        for (const issue of result.error.issues) {
            smtpErrors.value[String(issue.path[0])] = issue.message
        }
        return false
    }
    return true
}

async function submitSMTP() {
    if (!validateSMTP()) return

    smtpSubmitting.value = true
    smtpSubmitError.value = null
    smtpSuccess.value = false

    try {
        await api('/api/v1/settings/smtp', {
            method: 'PUT',
            body: {
                host: smtpHost.value,
                port: smtpPort.value,
                username: smtpUsername.value,
                password: smtpPassword.value,
                from: smtpFrom.value,
                tls: smtpTLS.value,
            },
        })
        smtpExists.value = true
        smtpSuccess.value = true
        smtpPassword.value = ''
        setTimeout(() => { smtpSuccess.value = false }, 3000)
    } catch (e: any) {
        smtpSubmitError.value = e?.data?.error?.message ?? e?.message ?? 'Failed to save SMTP settings'
    } finally {
        smtpSubmitting.value = false
    }
}
</script>

<template>
    <!-- Section header -->
    <div class="flex items-start justify-between gap-4 mb-6">
        <div>
            <h2 class="text-base font-semibold">SMTP</h2>
            <p class="mt-1 text-sm text-muted-foreground">
                Configure an outbound SMTP server to send email notifications for backup successes, failures, and agent
                events.
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
        <div class="flex flex-col gap-4">
            <div class="grid grid-cols-3 gap-3">
                <Skeleton class="col-span-2 h-16 rounded-md" />
                <Skeleton class="h-16 rounded-md" />
            </div>
            <div class="grid grid-cols-2 gap-3">
                <Skeleton class="h-16 rounded-md" />
                <Skeleton class="h-16 rounded-md" />
            </div>
            <Skeleton class="h-16 w-full rounded-md" />
        </div>
    </template>

    <!-- Form -->
    <form v-else novalidate @submit.prevent="submitSMTP">
        <FieldGroup class="flex flex-col gap-4">

            <Transition enter-active-class="transition-all duration-200" enter-from-class="-translate-y-1 opacity-0"
                leave-active-class="transition-all duration-150" leave-to-class="-translate-y-1 opacity-0">
                <Alert v-if="smtpSubmitError" variant="destructive">
                    <AlertCircle class="size-4" />
                    <AlertDescription>{{ smtpSubmitError }}</AlertDescription>
                </Alert>
            </Transition>

            <Transition enter-active-class="transition-all duration-200" enter-from-class="-translate-y-1 opacity-0"
                leave-active-class="transition-all duration-150" leave-to-class="-translate-y-1 opacity-0">
                <Alert v-if="smtpSuccess"
                    class="border-emerald-500/30 bg-emerald-500/5 text-emerald-600 dark:text-emerald-400">
                    <AlertDescription>SMTP settings saved successfully.</AlertDescription>
                </Alert>
            </Transition>

            <div class="grid grid-cols-3 gap-3">
                <Field class="col-span-2">
                    <FieldLabel for="smtp-host">Host</FieldLabel>
                    <Input id="smtp-host" v-model="smtpHost" placeholder="smtp.example.com" autocomplete="off"
                        :class="smtpErrors.host ? 'border-destructive focus-visible:ring-destructive/30' : ''" />
                    <FieldError v-if="smtpErrors.host">{{ smtpErrors.host }}</FieldError>
                </Field>
                <Field>
                    <FieldLabel for="smtp-port">Port</FieldLabel>
                    <Input id="smtp-port" v-model.number="smtpPort" type="number" placeholder="587"
                        :class="smtpErrors.port ? 'border-destructive focus-visible:ring-destructive/30' : ''" />
                    <FieldError v-if="smtpErrors.port">{{ smtpErrors.port }}</FieldError>
                </Field>
            </div>

            <div class="grid grid-cols-2 gap-3">
                <Field>
                    <FieldLabel for="smtp-username">
                        Username <span class="text-muted-foreground font-normal">(optional)</span>
                    </FieldLabel>
                    <Input id="smtp-username" v-model="smtpUsername" autocomplete="off" />
                </Field>
                <Field>
                    <FieldLabel for="smtp-password">Password</FieldLabel>
                    <Input id="smtp-password" v-model="smtpPassword" type="password"
                        :placeholder="smtpExists ? '(unchanged)' : ''" autocomplete="new-password"
                        :class="smtpErrors.password ? 'border-destructive focus-visible:ring-destructive/30' : ''" />
                    <FieldError v-if="smtpErrors.password">{{ smtpErrors.password }}</FieldError>
                </Field>
            </div>

            <Field>
                <FieldLabel for="smtp-from">From Address</FieldLabel>
                <Input id="smtp-from" v-model="smtpFrom" placeholder="arkeep@example.com" autocomplete="off"
                    :class="smtpErrors.from ? 'border-destructive focus-visible:ring-destructive/30' : ''" />
                <p class="text-xs text-muted-foreground">
                    The sender address shown in notification emails.
                </p>
                <FieldError v-if="smtpErrors.from">{{ smtpErrors.from }}</FieldError>
            </Field>

            <Separator />

            <div class="flex items-center justify-between">
                <div>
                    <p class="text-sm font-medium">Implicit TLS</p>
                    <p class="text-xs text-muted-foreground">
                        Enable for SMTPS on port 465. Leave off for STARTTLS (port 587) or plaintext.
                    </p>
                </div>
                <Switch :model-value="smtpTLS" @update:model-value="smtpTLS = $event" />
            </div>

            <div class="flex justify-end pt-2">
                <Button type="submit" :disabled="smtpSubmitting">
                    <Loader2 v-if="smtpSubmitting" class="size-4 animate-spin" />
                    {{ smtpSubmitting ? 'Saving…' : 'Save SMTP Settings' }}
                </Button>
            </div>

        </FieldGroup>
    </form>
</template>
