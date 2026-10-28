<script setup lang="ts">
import { computed, ref, onMounted } from 'vue'
import { useForm, useField } from 'vee-validate'
import { toTypedSchema } from '@vee-validate/zod'
import { z } from 'zod'
import { integerInRange } from '@/lib/validation'
import { Alert, AlertDescription } from '@/components/ui/alert'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import {
    Dialog,
    DialogContent,
    DialogDescription,
    DialogFooter,
    DialogHeader,
    DialogTitle,
} from '@/components/ui/dialog'
import {
    Sheet,
    SheetContent,
    SheetDescription,
    SheetFooter,
    SheetHeader,
    SheetTitle,
} from '@/components/ui/sheet'
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
import { AlertCircle, Check, ClipboardCopy, Loader2, Pencil, Plus, RefreshCw, Trash2 } from '@lucide/vue'
import { api, apiErrorMessage } from '@/services/api'
import type { ApiResponse, OIDCProvider } from '@/types'

// ---------------------------------------------------------------------------
// List state
// ---------------------------------------------------------------------------

const oidcProviders = ref<OIDCProvider[]>([])
const oidcLoading = ref(false)
const copiedId = ref<string | null>(null)

async function fetchOIDC() {
    oidcLoading.value = true
    try {
        const res = await api<ApiResponse<OIDCProvider[]>>('/api/v1/settings/oidc')
        oidcProviders.value = res.data ?? []
    } catch {
        oidcProviders.value = []
    } finally {
        oidcLoading.value = false
    }
}

function copyCallbackURL(url: string, id: string) {
    navigator.clipboard.writeText(url).then(() => {
        copiedId.value = id
        setTimeout(() => { copiedId.value = null }, 2000)
    })
}

// ---------------------------------------------------------------------------
// Add / edit sheet
// ---------------------------------------------------------------------------

const sheetOpen = ref(false)
const sheetMode = ref<'create' | 'edit'>('create')
const editingProvider = ref<OIDCProvider | null>(null)

const formError = ref<string | null>(null)

const hasGroups = (list: string) => list.split(',').some((g) => g.trim() !== '')

// The client secret is required on create only: on edit, blank keeps the
// stored one. Group restrictions and session cap are validated as the server
// does.
const schema = computed(() => toTypedSchema(
    z.object({
        name: z.string().min(1, 'Name is required'),
        issuer: z.url('Must be a valid URL'),
        client_id: z.string().min(1, 'Client ID is required'),
        client_secret: sheetMode.value === 'create'
            ? z.string().min(1, 'Client secret is required')
            : z.string(),
        scopes: z.string(),
        enabled: z.boolean(),
        groups_claim: z.string(),
        allowed_groups: z.string(),
        admin_groups: z.string(),
        session_max_hours: integerInRange(1, 168),
    }).superRefine((data, ctx) => {
        if (!data.groups_claim.trim() && (hasGroups(data.allowed_groups) || hasGroups(data.admin_groups))) {
            ctx.addIssue({
                code: 'custom',
                path: ['groups_claim'],
                message: 'Groups claim is required when allowed or admin groups are set',
            })
        }
    }),
))

// formValues returns the form values for provider, or the defaults of a new
// provider.
function formValues(provider: OIDCProvider | null = null) {
    return {
        name: provider?.name ?? '',
        issuer: provider?.issuer ?? '',
        client_id: provider?.client_id ?? '',
        client_secret: '',
        scopes: provider?.scopes || 'openid email profile',
        enabled: provider?.enabled ?? true,
        groups_claim: provider?.groups_claim ?? '',
        allowed_groups: provider?.allowed_groups ?? '',
        admin_groups: provider?.admin_groups ?? '',
        session_max_hours: provider?.session_max_hours ?? 12,
    }
}

const { handleSubmit, resetForm, isSubmitting: formSubmitting } = useForm({
    validationSchema: schema,
    initialValues: formValues(),
})

const { value: formName, errorMessage: nameError } = useField<string>('name')
const { value: formIssuer, errorMessage: issuerError } = useField<string>('issuer')
const { value: formClientId, errorMessage: clientIdError } = useField<string>('client_id')
const { value: formClientSecret, errorMessage: clientSecretError } = useField<string>('client_secret')
const { value: formScopes } = useField<string>('scopes')
const { value: formEnabled } = useField<boolean>('enabled')
const { value: formGroupsClaim, errorMessage: groupsClaimError } = useField<string>('groups_claim')
const { value: formAllowedGroups } = useField<string>('allowed_groups')
const { value: formAdminGroups } = useField<string>('admin_groups')
const { value: formSessionMaxHours, errorMessage: sessionMaxHoursError } = useField<number | string>('session_max_hours')

function openCreateSheet() {
    sheetMode.value = 'create'
    editingProvider.value = null
    resetForm({ values: formValues() })
    formError.value = null
    sheetOpen.value = true
}

function openEditSheet(provider: OIDCProvider) {
    sheetMode.value = 'edit'
    editingProvider.value = provider
    resetForm({ values: formValues(provider) })
    formError.value = null
    sheetOpen.value = true
}

function onSheetOpenChange(value: boolean) {
    if (!value) {
        resetForm({ values: formValues() })
        formError.value = null
    }
    sheetOpen.value = value
}

const submitForm = handleSubmit(async (values) => {
    formError.value = null

    try {
        const body: Record<string, unknown> = {
            name: values.name,
            issuer: values.issuer,
            client_id: values.client_id,
            scopes: values.scopes || 'openid email profile',
            enabled: values.enabled,
            groups_claim: values.groups_claim,
            allowed_groups: values.allowed_groups,
            admin_groups: values.admin_groups,
            session_max_hours: values.session_max_hours,
        }

        if (values.client_secret) {
            body.client_secret = values.client_secret
        }

        if (sheetMode.value === 'create') {
            await api('/api/v1/settings/oidc', { method: 'POST', body })
        } else {
            await api(`/api/v1/settings/oidc/${editingProvider.value!.id}`, { method: 'PUT', body })
        }

        sheetOpen.value = false
        await fetchOIDC()
    } catch (e: any) {
        formError.value = apiErrorMessage(e, 'Failed to save provider')
    }
})

// ---------------------------------------------------------------------------
// Delete dialog
// ---------------------------------------------------------------------------

const deleteDialogOpen = ref(false)
const deletingProvider = ref<OIDCProvider | null>(null)
const deleteSubmitting = ref(false)
const deleteError = ref<string | null>(null)

function openDeleteDialog(provider: OIDCProvider) {
    deletingProvider.value = provider
    deleteError.value = null
    deleteDialogOpen.value = true
}

async function confirmDelete() {
    if (!deletingProvider.value) return

    deleteSubmitting.value = true
    deleteError.value = null

    try {
        await api(`/api/v1/settings/oidc/${deletingProvider.value.id}`, { method: 'DELETE' })
        deleteDialogOpen.value = false
        await fetchOIDC()
    } catch (e: any) {
        deleteError.value = apiErrorMessage(e, 'Failed to delete provider')
    } finally {
        deleteSubmitting.value = false
    }
}

onMounted(fetchOIDC)
</script>

<template>
    <!-- Section header -->
    <div class="flex items-start justify-between gap-4 mb-6">
        <div>
            <h2 class="text-base font-semibold">OpenID Connect Providers</h2>
            <p class="mt-1 text-sm text-muted-foreground">
                Allow users to log in with an external identity provider (Zitadel, Keycloak, Okta, Google Workspace,
                etc.). Multiple providers are supported.
            </p>
        </div>
        <div class="flex items-center gap-2 shrink-0">
            <Button variant="outline" size="icon" aria-label="Refresh" :disabled="oidcLoading" @click="fetchOIDC">
                <RefreshCw class="size-4" :class="{ 'animate-spin': oidcLoading }" />
            </Button>
            <Button size="sm" @click="openCreateSheet">
                <Plus class="size-4" />
                Add provider
            </Button>
        </div>
    </div>

    <!-- Skeleton -->
    <template v-if="oidcLoading">
        <div class="flex flex-col gap-3">
            <Skeleton class="h-28 w-full rounded-lg" />
            <Skeleton class="h-28 w-full rounded-lg" />
        </div>
    </template>

    <!-- Empty state -->
    <template v-else-if="oidcProviders.length === 0">
        <div class="rounded-lg border border-dashed p-10 text-center">
            <p class="text-sm text-muted-foreground">
                No OIDC providers configured yet.<br>
                Add one to enable SSO login on the login page.
            </p>
        </div>
    </template>

    <!-- Provider list -->
    <div v-else class="flex flex-col gap-3">
        <div v-for="provider in oidcProviders" :key="provider.id"
            class="rounded-lg border bg-card p-5 flex flex-col gap-3">

            <!-- Header row -->
            <div class="flex items-center justify-between gap-3">
                <div class="flex items-center gap-2 min-w-0">
                    <span class="font-medium truncate">{{ provider.name }}</span>
                    <Badge :variant="provider.enabled ? 'default' : 'secondary'">
                        {{ provider.enabled ? 'Enabled' : 'Disabled' }}
                    </Badge>
                </div>
                <div class="flex items-center gap-1 shrink-0">
                    <Button variant="ghost" size="icon" aria-label="Edit" @click="openEditSheet(provider)">
                        <Pencil class="size-4" />
                    </Button>
                    <Button variant="ghost" size="icon" aria-label="Delete"
                        class="text-destructive hover:text-destructive" @click="openDeleteDialog(provider)">
                        <Trash2 class="size-4" />
                    </Button>
                </div>
            </div>

            <!-- Issuer -->
            <p class="text-xs text-muted-foreground truncate">{{ provider.issuer }}</p>

            <!-- Callback URL -->
            <div>
                <p class="text-xs text-muted-foreground mb-1.5">Redirect URI — add this to your identity provider</p>
                <div class="flex items-center gap-2">
                    <div
                        class="flex-1 min-w-0 rounded-md border bg-muted/50 px-3 py-1.5 text-xs font-mono text-muted-foreground truncate select-all">
                        {{ provider.callback_url }}
                    </div>
                    <Button variant="outline" size="icon" class="size-8 shrink-0"
                        :aria-label="copiedId === provider.id ? 'Copied' : 'Copy callback URL'"
                        @click="copyCallbackURL(provider.callback_url, provider.id)">
                        <Check v-if="copiedId === provider.id" class="size-3.5 text-emerald-500" />
                        <ClipboardCopy v-else class="size-3.5" />
                    </Button>
                </div>
            </div>
        </div>
    </div>

    <!-- ── Add / Edit sheet ──────────────────────────────────────────────── -->
    <Sheet :open="sheetOpen" @update:open="onSheetOpenChange">
        <SheetContent class="sm:max-w-lg overflow-y-auto">
            <SheetHeader>
                <SheetTitle>{{ sheetMode === 'create' ? 'Add OIDC Provider' : 'Edit OIDC Provider' }}</SheetTitle>
                <SheetDescription>
                    {{ sheetMode === 'create'
                        ? 'Configure a new identity provider. Copy the redirect URI into your IdP after saving.'
                        : 'Update the provider configuration. Leave the client secret blank to keep the current value.'
                    }}
                </SheetDescription>
            </SheetHeader>

            <form class="py-6 px-4" novalidate @submit="submitForm">
                <FieldGroup class="flex flex-col gap-4">

                    <Transition enter-active-class="transition-all duration-200"
                        enter-from-class="-translate-y-1 opacity-0" leave-active-class="transition-all duration-150"
                        leave-to-class="-translate-y-1 opacity-0">
                        <Alert v-if="formError" variant="destructive">
                            <AlertCircle class="size-4" />
                            <AlertDescription>{{ formError }}</AlertDescription>
                        </Alert>
                    </Transition>

                    <Field>
                        <FieldLabel for="form-name">Display Name</FieldLabel>
                        <Input id="form-name" v-model="formName" placeholder="e.g. Company SSO" autocomplete="off"
                            :class="nameError ? 'border-destructive focus-visible:ring-destructive/30' : ''" />
                        <FieldError v-if="nameError">{{ nameError }}</FieldError>
                    </Field>

                    <Field>
                        <FieldLabel for="form-issuer">Issuer URL</FieldLabel>
                        <Input id="form-issuer" v-model="formIssuer"
                            placeholder="https://accounts.google.com" autocomplete="off"
                            :class="issuerError ? 'border-destructive focus-visible:ring-destructive/30' : ''" />
                        <p class="text-xs text-muted-foreground">
                            Base URL of the identity provider. Endpoints are discovered automatically
                            from <span class="font-mono">/.well-known/openid-configuration</span>.
                        </p>
                        <FieldError v-if="issuerError">{{ issuerError }}</FieldError>
                    </Field>

                    <div class="grid grid-cols-2 gap-3">
                        <Field>
                            <FieldLabel for="form-client-id">Client ID</FieldLabel>
                            <Input id="form-client-id" v-model="formClientId" autocomplete="off"
                                :class="clientIdError ? 'border-destructive focus-visible:ring-destructive/30' : ''" />
                            <FieldError v-if="clientIdError">{{ clientIdError }}</FieldError>
                        </Field>
                        <Field>
                            <FieldLabel for="form-client-secret">
                                Client Secret
                                <span v-if="sheetMode === 'edit'"
                                    class="text-muted-foreground font-normal">(optional)</span>
                            </FieldLabel>
                            <Input id="form-client-secret" v-model="formClientSecret" type="password"
                                :placeholder="sheetMode === 'edit' ? '(unchanged)' : ''" autocomplete="new-password"
                                :class="clientSecretError ? 'border-destructive focus-visible:ring-destructive/30' : ''" />
                            <FieldError v-if="clientSecretError">{{ clientSecretError }}</FieldError>
                        </Field>
                    </div>

                    <Field>
                        <FieldLabel for="form-scopes">
                            Scopes <span class="text-muted-foreground font-normal">(optional)</span>
                        </FieldLabel>
                        <Input id="form-scopes" v-model="formScopes" placeholder="openid email profile"
                            autocomplete="off" />
                        <p class="text-xs text-muted-foreground">
                            Space-separated. Defaults to <span class="font-mono">openid email profile</span>.
                        </p>
                    </Field>

                    <Separator />

                    <div>
                        <p class="text-sm font-medium">Access control</p>
                        <p class="text-xs text-muted-foreground">
                            Restrict sign-in and assign roles from the groups the identity provider reports.
                            Group names must match exactly what the provider sends.
                        </p>
                    </div>

                    <Field>
                        <FieldLabel for="form-groups-claim">
                            Groups claim <span class="text-muted-foreground font-normal">(optional)</span>
                        </FieldLabel>
                        <Input id="form-groups-claim" v-model="formGroupsClaim" placeholder="groups"
                            autocomplete="off"
                            :class="groupsClaimError ? 'border-destructive focus-visible:ring-destructive/30' : ''" />
                        <p class="text-xs text-muted-foreground">
                            Name of the ID token or UserInfo claim listing the user's groups.
                            Required when allowed or admin groups are set.
                        </p>
                        <FieldError v-if="groupsClaimError">{{ groupsClaimError }}</FieldError>
                    </Field>

                    <Field>
                        <FieldLabel for="form-allowed-groups">
                            Allowed groups <span class="text-muted-foreground font-normal">(optional)</span>
                        </FieldLabel>
                        <Input id="form-allowed-groups" v-model="formAllowedGroups" placeholder="arkeep-users"
                            autocomplete="off" />
                        <p class="text-xs text-muted-foreground">
                            Comma-separated. Only members of one of these groups, or of an admin group, can sign in.
                            Leave empty to allow every user of the provider.
                        </p>
                    </Field>

                    <Field>
                        <FieldLabel for="form-admin-groups">
                            Admin groups <span class="text-muted-foreground font-normal">(optional)</span>
                        </FieldLabel>
                        <Input id="form-admin-groups" v-model="formAdminGroups" placeholder="arkeep-admins"
                            autocomplete="off" />
                        <p class="text-xs text-muted-foreground">
                            Comma-separated. Members get the admin role and everyone else the user role,
                            re-evaluated on every login. Leave empty to manage roles in Arkeep.
                        </p>
                    </Field>

                    <Field>
                        <FieldLabel for="form-session-max-hours">Session duration (hours)</FieldLabel>
                        <Input id="form-session-max-hours" v-model="formSessionMaxHours" type="number" min="1"
                            max="168"
                            :class="sessionMaxHoursError ? 'border-destructive focus-visible:ring-destructive/30' : ''" />
                        <p class="text-xs text-muted-foreground">
                            How long a session lasts before the user must sign in at the provider again,
                            where the groups are checked. Between 1 and 168.
                        </p>
                        <FieldError v-if="sessionMaxHoursError">{{ sessionMaxHoursError }}</FieldError>
                    </Field>

                    <Separator />

                    <div class="flex items-center justify-between">
                        <div>
                            <p class="text-sm font-medium">Enabled</p>
                            <p class="text-xs text-muted-foreground">
                                Show this provider's button on the login page.
                            </p>
                        </div>
                        <Switch :model-value="formEnabled" @update:model-value="formEnabled = $event" />
                    </div>

                    <SheetFooter class="mt-2 px-0">
                        <Button type="button" variant="outline" :disabled="formSubmitting"
                            @click="onSheetOpenChange(false)">Cancel</Button>
                        <Button type="submit" :disabled="formSubmitting">
                            <Loader2 v-if="formSubmitting" class="size-4 animate-spin" />
                            {{ formSubmitting ? 'Saving…' : (sheetMode === 'create' ? 'Add Provider' : 'Save Changes') }}
                        </Button>
                    </SheetFooter>

                </FieldGroup>
            </form>
        </SheetContent>
    </Sheet>

    <!-- ── Delete confirmation dialog ───────────────────────────────────── -->
    <Dialog :open="deleteDialogOpen" @update:open="deleteDialogOpen = $event">
        <DialogContent class="max-w-sm">
            <DialogHeader>
                <DialogTitle>Delete provider?</DialogTitle>
                <DialogDescription>
                    <strong>{{ deletingProvider?.name }}</strong> will be permanently removed.
                    Users who logged in via this provider will no longer be able to use SSO.
                    This action cannot be undone.
                </DialogDescription>
            </DialogHeader>

            <Alert v-if="deleteError" variant="destructive" class="mt-2">
                <AlertCircle class="size-4" />
                <AlertDescription>{{ deleteError }}</AlertDescription>
            </Alert>

            <DialogFooter class="mt-4">
                <Button variant="outline" :disabled="deleteSubmitting"
                    @click="deleteDialogOpen = false">Cancel</Button>
                <Button variant="destructive" :disabled="deleteSubmitting" @click="confirmDelete">
                    <Loader2 v-if="deleteSubmitting" class="size-4 animate-spin" />
                    {{ deleteSubmitting ? 'Deleting…' : 'Delete' }}
                </Button>
            </DialogFooter>
        </DialogContent>
    </Dialog>
</template>
