<script setup lang="ts">
import { ref, onMounted } from 'vue'
import { z } from 'zod'
import { Alert, AlertDescription } from '@/components/ui/alert'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
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
import type { ApiResponse, SMTPSettings, OIDCProvider } from '@/types'

// ---------------------------------------------------------------------------
// Tabs
// ---------------------------------------------------------------------------

// Active tab — 'oidc' is shown first as it controls the login flow.
const activeTab = ref<'oidc' | 'smtp'>('oidc')

// ---------------------------------------------------------------------------
// Shared state
// ---------------------------------------------------------------------------

const loading = ref(false)

// ---------------------------------------------------------------------------
// OIDC — form state
// ---------------------------------------------------------------------------

// The API returns 404 when no OIDC provider is configured yet. We track
// whether one exists so the form can show a "not configured" hint.
const oidcExists = ref(false)
const oidcSubmitting = ref(false)
const oidcSubmitError = ref<string | null>(null)
const oidcSuccess = ref(false)

const oidcName = ref('')
const oidcIssuer = ref('')
const oidcClientId = ref('')
const oidcClientSecret = ref('')
const oidcRedirectUrl = ref('')
const oidcScopes = ref('openid email profile')
const oidcEnabled = ref(true)

const oidcErrors = ref<Record<string, string>>({})

// Zod schema for OIDC form validation.
const oidcSchema = z.object({
    name: z.string().min(1, 'Name is required'),
    issuer: z.string().url('Issuer must be a valid URL'),
    client_id: z.string().min(1, 'Client ID is required'),
    client_secret: z.string().min(1, 'Client secret is required'),
    redirect_url: z.string().url('Redirect URL must be a valid URL'),
})

// ---------------------------------------------------------------------------
// SMTP — form state
// ---------------------------------------------------------------------------

// smtpExists tracks whether SMTP settings are already stored in the DB.
// 404 on GET means unconfigured; we show a hint but still allow saving.
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

// Zod schema for SMTP form validation.
const smtpSchema = z.object({
    host: z.string().min(1, 'Host is required'),
    port: z
        .number('Port must be a number')
        .int()
        .min(1, 'Port must be between 1 and 65535')
        .max(65535, 'Port must be between 1 and 65535'),
    password: z.string().min(1, 'Password is required'),
    from: z.string().email('Must be a valid email address'),
})

// ---------------------------------------------------------------------------
// Data fetching — load both configs in parallel on mount
// ---------------------------------------------------------------------------

async function fetchOIDC() {
    try {
        const res = await api<ApiResponse<OIDCProvider>>('/api/v1/settings/oidc')
        const p = res.data
        oidcExists.value = true
        oidcName.value = p.display_name ?? ''
        oidcIssuer.value = p.issuer_url ?? ''
        oidcClientId.value = p.client_id ?? ''
        // client_secret is always masked — leave blank so the user can re-enter
        oidcClientSecret.value = ''
        // redirect_url is not in the OIDCProvider type; populated from issuer
        oidcRedirectUrl.value = (p as any).redirect_url ?? ''
        oidcScopes.value = (p as any).scopes ?? 'openid email profile'
        oidcEnabled.value = p.enabled ?? true
    } catch (e: any) {
        if (e?.status === 404 || e?.response?.status === 404) {
            // Not configured yet — form stays empty, user can create from scratch.
            oidcExists.value = false
        }
        // Other errors are silently ignored at load time — the form is still usable.
    }
}

async function fetchSMTP() {
    try {
        const res = await api<ApiResponse<SMTPSettings>>('/api/v1/settings/smtp')
        const s = res.data
        smtpExists.value = true
        smtpHost.value = s.host ?? ''
        smtpPort.value = s.port ?? 587
        smtpUsername.value = s.username ?? ''
        smtpPassword.value = '' // always masked on read — user must re-enter
        smtpFrom.value = s.from ?? ''
        smtpTLS.value = s.tls ?? false
    } catch (e: any) {
        if (e?.status === 404 || e?.response?.status === 404) {
            smtpExists.value = false
        }
    }
}

async function fetchAll() {
    loading.value = true
    await Promise.all([fetchOIDC(), fetchSMTP()])
    loading.value = false
}

onMounted(fetchAll)

// ---------------------------------------------------------------------------
// OIDC — validate + submit
// ---------------------------------------------------------------------------

function validateOIDC(): boolean {
    oidcErrors.value = {}
    const result = oidcSchema.safeParse({
        name: oidcName.value,
        issuer: oidcIssuer.value,
        client_id: oidcClientId.value,
        client_secret: oidcClientSecret.value,
        redirect_url: oidcRedirectUrl.value,
    })
    if (!result.success) {
        for (const issue of result.error.issues) {
            oidcErrors.value[String(issue.path[0])] = issue.message
        }
        return false
    }
    return true
}

async function submitOIDC() {
    if (!validateOIDC()) return

    oidcSubmitting.value = true
    oidcSubmitError.value = null
    oidcSuccess.value = false

    try {
        await api('/api/v1/settings/oidc', {
            method: 'PUT',
            body: {
                name: oidcName.value,
                issuer: oidcIssuer.value,
                client_id: oidcClientId.value,
                client_secret: oidcClientSecret.value,
                redirect_url: oidcRedirectUrl.value,
                scopes: oidcScopes.value || 'openid email profile',
                enabled: oidcEnabled.value,
            },
        })
        oidcExists.value = true
        oidcSuccess.value = true
        // Clear the secret field — always write-only
        oidcClientSecret.value = ''
        setTimeout(() => { oidcSuccess.value = false }, 3000)
    } catch (e: any) {
        oidcSubmitError.value = e?.data?.error?.message ?? e?.message ?? 'Failed to save OIDC settings'
    } finally {
        oidcSubmitting.value = false
    }
}

// ---------------------------------------------------------------------------
// SMTP — validate + submit
// ---------------------------------------------------------------------------

function validateSMTP(): boolean {
    smtpErrors.value = {}
    const result = smtpSchema.safeParse({
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
    <div class="flex flex-col gap-6 p-6">

        <!-- Header -->
        <div class="flex items-center justify-between">
            <div>
                <h1 class="text-2xl font-semibold tracking-tight">Settings</h1>
                <p class="mt-1 text-sm text-muted-foreground">
                    Manage authentication and notification configuration.
                </p>
            </div>
            <Button variant="outline" size="icon" :disabled="loading" @click="fetchAll">
                <RefreshCw class="w-4 h-4" :class="{ 'animate-spin': loading }" />
            </Button>
        </div>

        <!-- 2-column layout: nav sidebar + form content -->
        <div class="flex gap-8">

            <!-- ── Left nav ───────────────────────────────────────────────────────── -->
            <nav class="w-44 shrink-0">
                <ul class="flex flex-col gap-1">
                    <li>
                        <button class="w-full text-left px-3 py-1.5 rounded-md text-sm transition-colors" :class="activeTab === 'oidc'
                            ? 'bg-accent text-accent-foreground font-medium'
                            : 'text-muted-foreground hover:text-foreground hover:bg-accent/50'"
                            @click="activeTab = 'oidc'">
                            OpenID Connect
                        </button>
                    </li>
                    <li>
                        <button class="w-full text-left px-3 py-1.5 rounded-md text-sm transition-colors" :class="activeTab === 'smtp'
                            ? 'bg-accent text-accent-foreground font-medium'
                            : 'text-muted-foreground hover:text-foreground hover:bg-accent/50'"
                            @click="activeTab = 'smtp'">
                            SMTP
                        </button>
                    </li>
                </ul>
            </nav>

            <!-- ── Right content ─────────────────────────────────────────────────── -->
            <div class="flex-1 min-w-0">

                <!-- ── OIDC section ─────────────────────────────────────────────────── -->
                <div v-if="activeTab === 'oidc'" class="flex flex-col gap-6 max-w-xl">

                    <div>
                        <h2 class="text-sm font-semibold">OpenID Connect</h2>
                        <p class="mt-1 text-sm text-muted-foreground">
                            Allow users to log in with an external identity provider (Keycloak, Okta,
                            Google Workspace, etc.). Only one provider is supported at a time.
                        </p>
                        <p v-if="!oidcExists" class="mt-2 text-xs text-amber-600 dark:text-amber-400">
                            No OIDC provider configured yet. Fill in the form below to enable SSO.
                        </p>
                    </div>

                    <form novalidate @submit.prevent="submitOIDC">
                        <FieldGroup class="flex flex-col gap-4">

                            <Transition enter-active-class="transition-all duration-200"
                                enter-from-class="-translate-y-1 opacity-0"
                                leave-active-class="transition-all duration-150"
                                leave-to-class="-translate-y-1 opacity-0">
                                <Alert v-if="oidcSubmitError" variant="destructive">
                                    <AlertCircle class="size-4" />
                                    <AlertDescription>{{ oidcSubmitError }}</AlertDescription>
                                </Alert>
                            </Transition>

                            <Transition enter-active-class="transition-all duration-200"
                                enter-from-class="-translate-y-1 opacity-0"
                                leave-active-class="transition-all duration-150"
                                leave-to-class="-translate-y-1 opacity-0">
                                <Alert v-if="oidcSuccess"
                                    class="border-emerald-500/30 bg-emerald-500/5 text-emerald-600 dark:text-emerald-400">
                                    <AlertDescription>OIDC settings saved successfully.</AlertDescription>
                                </Alert>
                            </Transition>

                            <Field>
                                <FieldLabel for="oidc-name">Display Name</FieldLabel>
                                <Input id="oidc-name" v-model="oidcName" placeholder="e.g. Company SSO"
                                    autocomplete="off"
                                    :class="oidcErrors.name ? 'border-destructive focus-visible:ring-destructive/30' : ''" />
                                <FieldError v-if="oidcErrors.name">{{ oidcErrors.name }}</FieldError>
                            </Field>

                            <Field>
                                <FieldLabel for="oidc-issuer">Issuer URL</FieldLabel>
                                <Input id="oidc-issuer" v-model="oidcIssuer" placeholder="https://accounts.google.com"
                                    autocomplete="off"
                                    :class="oidcErrors.issuer ? 'border-destructive focus-visible:ring-destructive/30' : ''" />
                                <p class="text-xs text-muted-foreground">
                                    The base URL of the identity provider. Must expose a
                                    <span class="font-mono">/.well-known/openid-configuration</span> endpoint.
                                </p>
                                <FieldError v-if="oidcErrors.issuer">{{ oidcErrors.issuer }}</FieldError>
                            </Field>

                            <div class="grid grid-cols-2 gap-3">
                                <Field>
                                    <FieldLabel for="oidc-client-id">Client ID</FieldLabel>
                                    <Input id="oidc-client-id" v-model="oidcClientId" autocomplete="off"
                                        :class="oidcErrors.client_id ? 'border-destructive focus-visible:ring-destructive/30' : ''" />
                                    <FieldError v-if="oidcErrors.client_id">{{ oidcErrors.client_id }}</FieldError>
                                </Field>
                                <Field>
                                    <FieldLabel for="oidc-client-secret">Client Secret</FieldLabel>
                                    <Input id="oidc-client-secret" v-model="oidcClientSecret" type="password"
                                        :placeholder="oidcExists ? '(unchanged)' : ''" autocomplete="new-password"
                                        :class="oidcErrors.client_secret ? 'border-destructive focus-visible:ring-destructive/30' : ''" />
                                    <FieldError v-if="oidcErrors.client_secret">{{ oidcErrors.client_secret }}
                                    </FieldError>
                                </Field>
                            </div>

                            <Field>
                                <FieldLabel for="oidc-redirect">Redirect URL</FieldLabel>
                                <Input id="oidc-redirect" v-model="oidcRedirectUrl"
                                    placeholder="https://arkeep.example.com/auth/callback" autocomplete="off"
                                    :class="oidcErrors.redirect_url ? 'border-destructive focus-visible:ring-destructive/30' : ''" />
                                <p class="text-xs text-muted-foreground">
                                    Must match the redirect URI registered in your identity provider.
                                </p>
                                <FieldError v-if="oidcErrors.redirect_url">{{ oidcErrors.redirect_url }}</FieldError>
                            </Field>

                            <Field>
                                <FieldLabel for="oidc-scopes">
                                    Scopes <span class="text-muted-foreground font-normal">(optional)</span>
                                </FieldLabel>
                                <Input id="oidc-scopes" v-model="oidcScopes" placeholder="openid email profile"
                                    autocomplete="off" />
                                <p class="text-xs text-muted-foreground">
                                    Space-separated list. Defaults to <span class="font-mono">openid email
                                        profile</span>.
                                </p>
                            </Field>

                            <Separator />

                            <div class="flex items-center justify-between">
                                <div>
                                    <p class="text-sm font-medium">Enabled</p>
                                    <p class="text-xs text-muted-foreground">
                                        When disabled, the SSO login button is hidden from the login page.
                                    </p>
                                </div>
                                <Switch :model-value="oidcEnabled" @update:model-value="oidcEnabled = $event" />
                            </div>

                            <div class="flex justify-end pt-2">
                                <Button type="submit" :disabled="oidcSubmitting">
                                    <Loader2 v-if="oidcSubmitting" class="size-4 animate-spin" />
                                    {{ oidcSubmitting ? 'Saving…' : 'Save OIDC Settings' }}
                                </Button>
                            </div>

                        </FieldGroup>
                    </form>
                </div>

                <!-- ── SMTP section ─────────────────────────────────────────────────── -->
                <div v-if="activeTab === 'smtp'" class="flex flex-col gap-6 max-w-xl">

                    <div>
                        <h2 class="text-sm font-semibold">SMTP</h2>
                        <p class="mt-1 text-sm text-muted-foreground">
                            Configure an outbound SMTP server to send email notifications for backup
                            successes, failures, and agent events.
                        </p>
                        <p v-if="!smtpExists" class="mt-2 text-xs text-amber-600 dark:text-amber-400">
                            No SMTP server configured yet. Email notifications are disabled.
                        </p>
                    </div>

                    <form novalidate @submit.prevent="submitSMTP">
                        <FieldGroup class="flex flex-col gap-4">

                            <Transition enter-active-class="transition-all duration-200"
                                enter-from-class="-translate-y-1 opacity-0"
                                leave-active-class="transition-all duration-150"
                                leave-to-class="-translate-y-1 opacity-0">
                                <Alert v-if="smtpSubmitError" variant="destructive">
                                    <AlertCircle class="size-4" />
                                    <AlertDescription>{{ smtpSubmitError }}</AlertDescription>
                                </Alert>
                            </Transition>

                            <Transition enter-active-class="transition-all duration-200"
                                enter-from-class="-translate-y-1 opacity-0"
                                leave-active-class="transition-all duration-150"
                                leave-to-class="-translate-y-1 opacity-0">
                                <Alert v-if="smtpSuccess"
                                    class="border-emerald-500/30 bg-emerald-500/5 text-emerald-600 dark:text-emerald-400">
                                    <AlertDescription>SMTP settings saved successfully.</AlertDescription>
                                </Alert>
                            </Transition>

                            <div class="grid grid-cols-3 gap-3">
                                <Field class="col-span-2">
                                    <FieldLabel for="smtp-host">Host</FieldLabel>
                                    <Input id="smtp-host" v-model="smtpHost" placeholder="smtp.example.com"
                                        autocomplete="off"
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
                                <Input id="smtp-from" v-model="smtpFrom" placeholder="arkeep@example.com"
                                    autocomplete="off"
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
                </div>

            </div>
        </div>

    </div>
</template>