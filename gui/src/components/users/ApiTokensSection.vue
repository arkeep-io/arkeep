<script setup lang="ts">
import { onMounted, ref } from 'vue'
import { useForm, useField } from 'vee-validate'
import { toTypedSchema } from '@vee-validate/zod'
import { z } from 'zod'
import { Alert, AlertDescription } from '@/components/ui/alert'
import { Button } from '@/components/ui/button'
import { Field, FieldDescription, FieldError, FieldGroup, FieldLabel } from '@/components/ui/field'
import { Input } from '@/components/ui/input'
import {
    Select, SelectContent, SelectItem, SelectTrigger, SelectValue,
} from '@/components/ui/select'
import {
    Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle,
} from '@/components/ui/dialog'
import ApiTokenRevokeDialog from '@/components/users/ApiTokenRevokeDialog.vue'
import { AlertCircle, KeyRound, Loader2 } from '@lucide/vue'
import { api, apiErrorMessage } from '@/services/api'
import { formatDate } from '@/lib/jobUtils'
import type { ApiResponse, ApiToken, CreatedApiToken } from '@/types'

// ---------------------------------------------------------------------------
// List
// ---------------------------------------------------------------------------

const tokens = ref<ApiToken[]>([])
const loading = ref(true)
const loadError = ref<string | null>(null)

async function fetchTokens(): Promise<void> {
    loading.value = true
    loadError.value = null
    try {
        const res = await api<ApiResponse<ApiToken[]>>('/api/v1/users/me/api-tokens')
        tokens.value = res.data
    } catch (e: any) {
        loadError.value = apiErrorMessage(e, 'Failed to load API tokens.')
    } finally {
        loading.value = false
    }
}

onMounted(fetchTokens)

// ---------------------------------------------------------------------------
// Create
// ---------------------------------------------------------------------------

// Expiry choices, in days; "never" sends null.
const expiryOptions = [
    { value: '30', label: '30 days' },
    { value: '90', label: '90 days' },
    { value: '365', label: '1 year' },
    { value: 'never', label: 'No expiry' },
]

const createDialogOpen = ref(false)
const createError = ref<string | null>(null)
const createdToken = ref<CreatedApiToken | null>(null)
const copied = ref(false)

const createForm = useForm({
    validationSchema: toTypedSchema(z.object({
        name: z.string().trim().min(1, 'Name is required').max(100, 'At most 100 characters'),
        expiry: z.string(),
    })),
    initialValues: { name: '', expiry: '365' },
})
const { isSubmitting: creating } = createForm
const { value: name, errorMessage: nameError } = useField<string>('name', undefined, { form: createForm })
const { value: expiry } = useField<string>('expiry', undefined, { form: createForm })

function openCreateDialog(): void {
    createForm.resetForm()
    createError.value = null
    createdToken.value = null
    copied.value = false
    createDialogOpen.value = true
}

const submitCreate = createForm.handleSubmit(async (values) => {
    createError.value = null
    try {
        const res = await api<ApiResponse<CreatedApiToken>>('/api/v1/users/me/api-tokens', {
            method: 'POST',
            body: {
                name: values.name.trim(),
                expires_in_days: values.expiry === 'never' ? null : Number(values.expiry),
            },
        })
        createdToken.value = res.data
        await fetchTokens()
    } catch (e: any) {
        createError.value = apiErrorMessage(e, 'Failed to create the API token.')
    }
})

async function copyToken(): Promise<void> {
    if (!createdToken.value) return
    await navigator.clipboard.writeText(createdToken.value.token)
    copied.value = true
}

function closeCreateDialog(): void {
    createDialogOpen.value = false
    // The token is shown once: forget it as soon as the dialog closes.
    createdToken.value = null
}

// ---------------------------------------------------------------------------
// Revoke
// ---------------------------------------------------------------------------

const revoking = ref<ApiToken | null>(null)

async function onRevoked(): Promise<void> {
    revoking.value = null
    await fetchTokens()
}

function isExpired(t: ApiToken): boolean {
    return t.expires_at !== null && new Date(t.expires_at).getTime() <= Date.now()
}
</script>

<template>
    <div class="grid grid-cols-[280px_1fr] gap-12 py-8 border-b">
        <div>
            <h2 class="text-sm font-semibold">API Tokens</h2>
            <p class="mt-1 text-sm text-muted-foreground">
                Read-only tokens for programs that read Arkeep on your behalf, such as a Home Assistant
                integration. A token sees what the user role sees and never changes anything.
            </p>
        </div>

        <div class="flex flex-col items-start gap-4">
            <Alert v-if="loadError" variant="destructive">
                <AlertCircle class="size-4" />
                <AlertDescription>{{ loadError }}</AlertDescription>
            </Alert>

            <div v-if="loading" class="flex justify-center w-full py-4 text-muted-foreground">
                <Loader2 class="size-5 animate-spin" />
            </div>

            <p v-else-if="tokens.length === 0 && !loadError" class="text-sm text-muted-foreground">
                You have no API tokens.
            </p>

            <ul v-else class="w-full divide-y rounded-md border">
                <li v-for="t in tokens" :key="t.id" class="flex items-center gap-4 px-4 py-3">
                    <KeyRound class="size-4 shrink-0 text-muted-foreground" />
                    <div class="min-w-0 flex-1">
                        <p class="truncate text-sm font-medium">{{ t.name }}</p>
                        <p class="text-xs text-muted-foreground">
                            <code>{{ t.hint }}…</code>
                            · Created {{ formatDate(t.created_at) }}
                            · Last used {{ t.last_used_at ? formatDate(t.last_used_at) : 'never' }}
                            ·
                            <span :class="{ 'text-destructive': isExpired(t) }">
                                {{ t.expires_at ? (isExpired(t) ? 'Expired ' : 'Expires ') + formatDate(t.expires_at) : 'No expiry' }}
                            </span>
                        </p>
                    </div>
                    <Button variant="outline" size="sm" @click="revoking = t">Revoke</Button>
                </li>
            </ul>

            <Button variant="outline" @click="openCreateDialog">Create token</Button>
        </div>
    </div>

    <!-- Create, then show the token once -->
    <Dialog :open="createDialogOpen" @update:open="(v) => { if (!v) closeCreateDialog() }">
        <DialogContent>
            <template v-if="!createdToken">
                <DialogHeader>
                    <DialogTitle>Create an API token</DialogTitle>
                    <DialogDescription>
                        The token can read agents, destinations, policies, jobs and snapshots. It cannot change
                        anything, start a job or manage tokens.
                    </DialogDescription>
                </DialogHeader>
                <FieldGroup>
                    <Alert v-if="createError" variant="destructive">
                        <AlertCircle class="size-4" />
                        <AlertDescription>{{ createError }}</AlertDescription>
                    </Alert>
                    <Field>
                        <FieldLabel for="api-token-name">Name</FieldLabel>
                        <Input id="api-token-name" v-model="name" placeholder="Home Assistant"
                            :class="nameError ? 'border-destructive focus-visible:ring-destructive/30' : ''"
                            @keydown.enter.prevent="submitCreate()" />
                        <FieldError v-if="nameError">{{ nameError }}</FieldError>
                    </Field>
                    <Field>
                        <FieldLabel for="api-token-expiry">Expiry</FieldLabel>
                        <Select v-model="expiry">
                            <SelectTrigger id="api-token-expiry" class="w-40">
                                <SelectValue />
                            </SelectTrigger>
                            <SelectContent>
                                <SelectItem v-for="o in expiryOptions" :key="o.value" :value="o.value">
                                    {{ o.label }}
                                </SelectItem>
                            </SelectContent>
                        </Select>
                        <FieldDescription>You can revoke the token at any time.</FieldDescription>
                    </Field>
                </FieldGroup>
                <DialogFooter>
                    <Button variant="outline" :disabled="creating" @click="closeCreateDialog">Cancel</Button>
                    <Button :disabled="creating" @click="submitCreate()">
                        <Loader2 v-if="creating" class="size-4 animate-spin" />
                        {{ creating ? 'Creating…' : 'Create' }}
                    </Button>
                </DialogFooter>
            </template>

            <template v-else>
                <DialogHeader>
                    <DialogTitle>Copy your API token</DialogTitle>
                    <DialogDescription>
                        It will not be shown again. Store it where the program that uses it keeps its secrets.
                    </DialogDescription>
                </DialogHeader>
                <code data-testid="created-api-token"
                    class="block w-full break-all rounded-md border bg-muted/50 p-3 font-mono text-sm">{{ createdToken.token }}</code>
                <DialogFooter class="gap-2 sm:justify-between">
                    <Button variant="outline" @click="copyToken">{{ copied ? 'Copied!' : 'Copy' }}</Button>
                    <Button @click="closeCreateDialog">Done</Button>
                </DialogFooter>
            </template>
        </DialogContent>
    </Dialog>

    <ApiTokenRevokeDialog :token="revoking" endpoint="/api/v1/users/me/api-tokens"
        @cancel="revoking = null" @revoked="onRevoked" />
</template>
