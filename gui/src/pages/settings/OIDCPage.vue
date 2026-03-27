<script setup lang="ts">
import { ref, onMounted } from 'vue'
import { z } from 'zod'
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
import { AlertCircle, Check, ClipboardCopy, Loader2, Pencil, Plus, RefreshCw, Trash2 } from 'lucide-vue-next'
import { api } from '@/services/api'
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
// Add / edit dialog
// ---------------------------------------------------------------------------

const dialogOpen = ref(false)
const dialogMode = ref<'create' | 'edit'>('create')
const editingProvider = ref<OIDCProvider | null>(null)

const formName = ref('')
const formIssuer = ref('')
const formClientId = ref('')
const formClientSecret = ref('')
const formScopes = ref('openid email profile')
const formEnabled = ref(true)
const formErrors = ref<Record<string, string>>({})
const formSubmitting = ref(false)
const formError = ref<string | null>(null)

const createSchema = z.object({
    name: z.string().min(1, 'Name is required'),
    issuer: z.string().url('Must be a valid URL'),
    client_id: z.string().min(1, 'Client ID is required'),
    client_secret: z.string().min(1, 'Client secret is required'),
})

const editSchema = z.object({
    name: z.string().min(1, 'Name is required'),
    issuer: z.string().url('Must be a valid URL'),
    client_id: z.string().min(1, 'Client ID is required'),
})

function openCreateDialog() {
    dialogMode.value = 'create'
    editingProvider.value = null
    formName.value = ''
    formIssuer.value = ''
    formClientId.value = ''
    formClientSecret.value = ''
    formScopes.value = 'openid email profile'
    formEnabled.value = true
    formErrors.value = {}
    formError.value = null
    dialogOpen.value = true
}

function openEditDialog(provider: OIDCProvider) {
    dialogMode.value = 'edit'
    editingProvider.value = provider
    formName.value = provider.name
    formIssuer.value = provider.issuer
    formClientId.value = provider.client_id
    formClientSecret.value = ''
    formScopes.value = provider.scopes || 'openid email profile'
    formEnabled.value = provider.enabled
    formErrors.value = {}
    formError.value = null
    dialogOpen.value = true
}

function validateForm(): boolean {
    formErrors.value = {}
    const schema = dialogMode.value === 'create' ? createSchema : editSchema
    const result = schema.safeParse({
        name: formName.value,
        issuer: formIssuer.value,
        client_id: formClientId.value,
        client_secret: formClientSecret.value,
    })
    if (!result.success) {
        for (const issue of result.error.issues) {
            formErrors.value[String(issue.path[0])] = issue.message
        }
        return false
    }
    return true
}

async function submitForm() {
    if (!validateForm()) return

    formSubmitting.value = true
    formError.value = null

    try {
        const body: Record<string, unknown> = {
            name: formName.value,
            issuer: formIssuer.value,
            client_id: formClientId.value,
            scopes: formScopes.value || 'openid email profile',
            enabled: formEnabled.value,
        }

        if (formClientSecret.value) {
            body.client_secret = formClientSecret.value
        }

        if (dialogMode.value === 'create') {
            await api('/api/v1/settings/oidc', { method: 'POST', body })
        } else {
            await api(`/api/v1/settings/oidc/${editingProvider.value!.id}`, { method: 'PUT', body })
        }

        dialogOpen.value = false
        await fetchOIDC()
    } catch (e: any) {
        formError.value = e?.data?.error?.message ?? e?.message ?? 'Failed to save provider'
    } finally {
        formSubmitting.value = false
    }
}

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
        deleteError.value = e?.data?.error?.message ?? e?.message ?? 'Failed to delete provider'
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
            <Button size="sm" @click="openCreateDialog">
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
                    <Button variant="ghost" size="icon" aria-label="Edit" @click="openEditDialog(provider)">
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

    <!-- Add / Edit dialog -->
    <Dialog :open="dialogOpen" @update:open="dialogOpen = $event">
        <DialogContent class="max-w-lg">
            <DialogHeader>
                <DialogTitle>{{ dialogMode === 'create' ? 'Add OIDC Provider' : 'Edit OIDC Provider' }}</DialogTitle>
                <DialogDescription>
                    {{ dialogMode === 'create'
                        ? 'Configure a new identity provider. Copy the redirect URI into your IdP after saving.'
                        : 'Update the provider configuration. Leave the client secret blank to keep the current value.'
                    }}
                </DialogDescription>
            </DialogHeader>

            <form novalidate @submit.prevent="submitForm">
                <FieldGroup class="flex flex-col gap-4 py-2">

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
                            :class="formErrors.name ? 'border-destructive focus-visible:ring-destructive/30' : ''" />
                        <FieldError v-if="formErrors.name">{{ formErrors.name }}</FieldError>
                    </Field>

                    <Field>
                        <FieldLabel for="form-issuer">Issuer URL</FieldLabel>
                        <Input id="form-issuer" v-model="formIssuer"
                            placeholder="https://accounts.google.com" autocomplete="off"
                            :class="formErrors.issuer ? 'border-destructive focus-visible:ring-destructive/30' : ''" />
                        <p class="text-xs text-muted-foreground">
                            Base URL of the identity provider. Endpoints are discovered automatically
                            from <span class="font-mono">/.well-known/openid-configuration</span>.
                        </p>
                        <FieldError v-if="formErrors.issuer">{{ formErrors.issuer }}</FieldError>
                    </Field>

                    <div class="grid grid-cols-2 gap-3">
                        <Field>
                            <FieldLabel for="form-client-id">Client ID</FieldLabel>
                            <Input id="form-client-id" v-model="formClientId" autocomplete="off"
                                :class="formErrors.client_id ? 'border-destructive focus-visible:ring-destructive/30' : ''" />
                            <FieldError v-if="formErrors.client_id">{{ formErrors.client_id }}</FieldError>
                        </Field>
                        <Field>
                            <FieldLabel for="form-client-secret">
                                Client Secret
                                <span v-if="dialogMode === 'edit'"
                                    class="text-muted-foreground font-normal">(optional)</span>
                            </FieldLabel>
                            <Input id="form-client-secret" v-model="formClientSecret" type="password"
                                :placeholder="dialogMode === 'edit' ? '(unchanged)' : ''" autocomplete="new-password"
                                :class="formErrors.client_secret ? 'border-destructive focus-visible:ring-destructive/30' : ''" />
                            <FieldError v-if="formErrors.client_secret">{{ formErrors.client_secret }}</FieldError>
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

                    <div class="flex items-center justify-between">
                        <div>
                            <p class="text-sm font-medium">Enabled</p>
                            <p class="text-xs text-muted-foreground">
                                Show this provider's button on the login page.
                            </p>
                        </div>
                        <Switch :model-value="formEnabled" @update:model-value="formEnabled = $event" />
                    </div>

                </FieldGroup>

                <DialogFooter class="mt-4">
                    <Button type="button" variant="outline" :disabled="formSubmitting"
                        @click="dialogOpen = false">Cancel</Button>
                    <Button type="submit" :disabled="formSubmitting">
                        <Loader2 v-if="formSubmitting" class="size-4 animate-spin" />
                        {{ formSubmitting ? 'Saving…' : (dialogMode === 'create' ? 'Add Provider' : 'Save Changes') }}
                    </Button>
                </DialogFooter>
            </form>
        </DialogContent>
    </Dialog>

    <!-- Delete confirmation dialog -->
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
