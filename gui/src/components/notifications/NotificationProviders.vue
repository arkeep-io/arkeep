<script setup lang="ts">
import { computed, ref, onMounted } from 'vue'
import { useForm, useField } from 'vee-validate'
import { toTypedSchema } from '@vee-validate/zod'
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
import { Field, FieldError, FieldGroup, FieldLabel } from '@/components/ui/field'
import { Switch } from '@/components/ui/switch'
import { Separator } from '@/components/ui/separator'
import { AlertCircle, ArrowLeft, Loader2, Pencil, Plus, Send, Trash2 } from '@lucide/vue'
import { api, apiErrorMessage } from '@/services/api'
import { PROVIDER_TYPES, providerTypeDef, type ProviderTypeDef } from '@/lib/notificationProviders'
import type { ApiResponse, NotificationProvider, NotificationProviderPriorities } from '@/types'

// ---------------------------------------------------------------------------
// List
// ---------------------------------------------------------------------------

const providers = ref<NotificationProvider[]>([])
const loading = ref(false)

async function fetchProviders() {
    loading.value = true
    try {
        const res = await api<ApiResponse<NotificationProvider[]>>('/api/v1/settings/notification-providers')
        providers.value = res.data ?? []
    } catch {
        providers.value = []
    } finally {
        loading.value = false
    }
}

onMounted(fetchProviders)

// summary is the non-secret detail shown under a provider's name.
function summary(p: NotificationProvider): string {
    return p.config.url ?? p.config.chats ?? p.config.user ?? ''
}

// ---------------------------------------------------------------------------
// Add / edit sheet
// ---------------------------------------------------------------------------

const EVENTS: { key: keyof NotificationProviderPriorities; label: string }[] = [
    { key: 'job_failure', label: 'Job failed' },
    { key: 'job_success', label: 'Job succeeded' },
    { key: 'agent_offline', label: 'Agent went offline' },
    { key: 'agent_online', label: 'Agent came back online' },
]

const sheetOpen = ref(false)
const editing = ref<NotificationProvider | null>(null)
// selected is the service of the form; null shows the service picker.
const selected = ref<ProviderTypeDef | null>(null)
const formError = ref<string | null>(null)

// A secret field is required unless the provider being edited already stores
// it: blank keeps the stored value.
function secretStored(key: string): boolean {
    return (editing.value?.secrets_set.includes(key) ?? false) && !cleared.value.includes(key)
}

// cleared lists the stored optional secrets to remove on save.
const cleared = ref<string[]>([])

const schema = computed(() => toTypedSchema(
    z.object({
        name: z.string().trim().min(1, 'Name is required').max(100, 'At most 100 characters'),
        enabled: z.boolean(),
        fields: z.record(z.string(), z.string()),
        priorities: z.object({
            job_success: z.string().max(32, 'At most 32 characters'),
            job_failure: z.string().max(32, 'At most 32 characters'),
            agent_offline: z.string().max(32, 'At most 32 characters'),
            agent_online: z.string().max(32, 'At most 32 characters'),
        }),
    }).superRefine((data, ctx) => {
        for (const f of selected.value?.fields ?? []) {
            if (f.required && !(data.fields[f.key] ?? '').trim() && !(f.secret && secretStored(f.key))) {
                ctx.addIssue({ code: 'custom', path: ['fields', f.key], message: `${f.label} is required` })
            }
        }
    }),
))

const emptyPriorities = (): NotificationProviderPriorities => ({
    job_success: '', job_failure: '', agent_offline: '', agent_online: '',
})

// formValues returns the form values for provider of type def: its stored
// non-secret fields, blank secrets.
function formValues(def: ProviderTypeDef | null, provider: NotificationProvider | null = null) {
    const fields: Record<string, string> = {}
    for (const f of def?.fields ?? []) {
        fields[f.key] = f.secret ? '' : (provider?.config[f.key] ?? '')
    }
    return {
        name: provider?.name ?? def?.label ?? '',
        enabled: provider?.enabled ?? true,
        fields,
        priorities: provider ? { ...provider.priorities } : emptyPriorities(),
    }
}

// Every field key of every service, so each input has its own useField.
const FIELD_KEYS = [...new Set(PROVIDER_TYPES.flatMap((t) => t.fields.map((f) => f.key)))]

// initialFields gives every field key a value, as the form expects.
function initialFields(values: Record<string, string>): Record<string, string> {
    return Object.fromEntries(FIELD_KEYS.map((k) => [k, values[k] ?? '']))
}

const { handleSubmit, resetForm, isSubmitting } = useForm({
    validationSchema: schema,
    initialValues: { ...formValues(null), fields: initialFields({}) },
})

const { value: formName, errorMessage: nameError } = useField<string>('name')
const { value: formEnabled } = useField<boolean>('enabled')
const fieldInputs = Object.fromEntries(FIELD_KEYS.map((k) => [k, useField<string>(`fields.${k}`)]))
const priorityInputs = Object.fromEntries(EVENTS.map((ev) => [ev.key, useField<string>(`priorities.${ev.key}`)]))

function openCreate() {
    editing.value = null
    cleared.value = []
    selected.value = null
    formError.value = null
    testResult.value = null
    sheetOpen.value = true
}

// reset fills the form with the values of provider of type def.
function reset(def: ProviderTypeDef | null, provider: NotificationProvider | null = null) {
    const values = formValues(def, provider)
    resetForm({ values: { ...values, fields: initialFields(values.fields) } })
}

function choose(def: ProviderTypeDef) {
    selected.value = def
    reset(def)
}

function openEdit(p: NotificationProvider) {
    editing.value = p
    cleared.value = []
    selected.value = providerTypeDef(p.type) ?? null
    formError.value = null
    testResult.value = null
    reset(selected.value, p)
    sheetOpen.value = true
}

function onSheetOpenChange(value: boolean) {
    sheetOpen.value = value
}

// requestBody is the API body of the form values for the selected service.
function requestBody(values: { name: string; enabled: boolean; fields: Record<string, string>; priorities: NotificationProviderPriorities }) {
    const def = selected.value!
    const fields: Record<string, string> = {}
    for (const f of def.fields) {
        fields[f.key] = (values.fields[f.key] ?? '').trim()
    }
    const priorities = def.priorityHelp ? values.priorities : emptyPriorities()
    return {
        name: values.name.trim(), type: def.type, enabled: values.enabled, fields, priorities,
        clear_secrets: cleared.value.filter((k) => !fields[k]),
    }
}

const submit = handleSubmit(async (values) => {
    formError.value = null
    try {
        const body = requestBody(values)
        if (editing.value) {
            await api(`/api/v1/settings/notification-providers/${editing.value.id}`, { method: 'PUT', body })
        } else {
            await api('/api/v1/settings/notification-providers', { method: 'POST', body })
        }
        sheetOpen.value = false
        await fetchProviders()
    } catch (e: any) {
        formError.value = apiErrorMessage(e, 'Failed to save the notification provider')
    }
})

// ---------------------------------------------------------------------------
// Test
// ---------------------------------------------------------------------------

// The server sends a test message with the form, saved or not; for a saved
// provider it fills the blank secret fields with the stored ones.
const testing = ref(false)
const testResult = ref<{ ok: boolean; message: string } | null>(null)

const sendTest = handleSubmit(async (values) => {
    testResult.value = null
    testing.value = true
    try {
        await api('/api/v1/settings/notification-providers/test', {
            method: 'POST',
            body: { ...requestBody(values), id: editing.value?.id ?? '' },
        })
        testResult.value = { ok: true, message: 'Test notification sent.' }
    } catch (e: any) {
        testResult.value = { ok: false, message: apiErrorMessage(e, 'Failed to send the test notification') }
    } finally {
        testing.value = false
    }
})

// ---------------------------------------------------------------------------
// Delete
// ---------------------------------------------------------------------------

const deleteOpen = ref(false)
const deleting = ref<NotificationProvider | null>(null)
const deleteSubmitting = ref(false)
const deleteError = ref<string | null>(null)

function openDelete(p: NotificationProvider) {
    deleting.value = p
    deleteError.value = null
    deleteOpen.value = true
}

async function confirmDelete() {
    if (!deleting.value) return
    deleteSubmitting.value = true
    deleteError.value = null
    try {
        await api(`/api/v1/settings/notification-providers/${deleting.value.id}`, { method: 'DELETE' })
        deleteOpen.value = false
        await fetchProviders()
    } catch (e: any) {
        deleteError.value = apiErrorMessage(e, 'Failed to delete the notification provider')
    } finally {
        deleteSubmitting.value = false
    }
}
</script>

<template>
    <div class="flex flex-col gap-4">
        <div class="flex items-start justify-between gap-4">
            <div>
                <p class="text-sm font-medium">Notification providers</p>
                <p class="text-xs text-muted-foreground">
                    Push and chat services (Gotify, ntfy, Telegram, Discord…) that receive every enabled event
                    above, each with its own priority per event.
                </p>
            </div>
            <Button type="button" size="sm" class="shrink-0" @click="openCreate">
                <Plus class="size-4" />
                Add provider
            </Button>
        </div>

        <div v-if="loading" class="flex flex-col gap-2">
            <Skeleton class="h-14 w-full rounded-lg" />
        </div>

        <div v-else-if="providers.length === 0" class="rounded-lg border border-dashed p-6 text-center">
            <p class="text-sm text-muted-foreground">No notification providers configured.</p>
        </div>

        <div v-else class="flex flex-col gap-2">
            <div v-for="p in providers" :key="p.id" data-testid="notification-provider"
                class="flex items-center justify-between gap-3 rounded-lg border px-4 py-3">
                <div class="flex items-center gap-3 min-w-0">
                    <div class="flex size-8 shrink-0 items-center justify-center rounded-md bg-muted">
                        <svg v-if="providerTypeDef(p.type)?.iconPath" viewBox="0 0 24 24" class="size-4"
                            fill="currentColor" aria-hidden="true">
                            <path :d="providerTypeDef(p.type)!.iconPath" />
                        </svg>
                        <component :is="providerTypeDef(p.type)?.icon" v-else class="size-4" />
                    </div>
                    <div class="min-w-0">
                        <div class="flex items-center gap-2">
                            <span class="text-sm font-medium truncate">{{ p.name }}</span>
                            <Badge :variant="p.enabled ? 'default' : 'secondary'">
                                {{ p.enabled ? 'Enabled' : 'Disabled' }}
                            </Badge>
                        </div>
                        <p class="text-xs text-muted-foreground truncate">
                            {{ providerTypeDef(p.type)?.label ?? p.type }}<template v-if="summary(p)"> · {{ summary(p)
                            }}</template>
                        </p>
                    </div>
                </div>
                <div class="flex items-center gap-1 shrink-0">
                    <Button type="button" variant="ghost" size="icon" :aria-label="`Edit ${p.name}`"
                        @click="openEdit(p)">
                        <Pencil class="size-4" />
                    </Button>
                    <Button type="button" variant="ghost" size="icon" :aria-label="`Delete ${p.name}`"
                        class="text-destructive hover:text-destructive" @click="openDelete(p)">
                        <Trash2 class="size-4" />
                    </Button>
                </div>
            </div>
        </div>
    </div>

    <!-- ── Add / edit sheet ──────────────────────────────────────────────── -->
    <Sheet :open="sheetOpen" @update:open="onSheetOpenChange">
        <SheetContent class="sm:max-w-lg overflow-y-auto">
            <SheetHeader>
                <SheetTitle>
                    {{ editing ? 'Edit notification provider' : selected ? `Add ${selected.label}` : 'Add notification provider' }}
                </SheetTitle>
                <SheetDescription>
                    {{ selected
                        ? (editing ? 'Leave a secret field blank to keep its current value.' : 'Fill in what the service needs: Arkeep builds the rest.')
                        : 'Choose the service to send notifications to.' }}
                </SheetDescription>
            </SheetHeader>

            <!-- Service picker -->
            <div v-if="!selected" class="grid grid-cols-2 sm:grid-cols-3 gap-2 py-6 px-4">
                <button v-for="def in PROVIDER_TYPES" :key="def.type" type="button"
                    :data-testid="`provider-type-${def.type}`"
                    class="flex flex-col items-center gap-2 rounded-lg border p-4 text-sm transition-colors hover:bg-muted focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring"
                    @click="choose(def)">
                    <svg v-if="def.iconPath" viewBox="0 0 24 24" class="size-6" fill="currentColor"
                        aria-hidden="true">
                        <path :d="def.iconPath" />
                    </svg>
                    <component :is="def.icon" v-else class="size-6" />
                    <span class="text-center leading-tight">{{ def.label }}</span>
                </button>
            </div>

            <!-- Form -->
            <form v-else class="py-6 px-4" novalidate @submit="submit">
                <FieldGroup class="flex flex-col gap-4">
                    <Alert v-if="formError" variant="destructive">
                        <AlertCircle class="size-4" />
                        <AlertDescription>{{ formError }}</AlertDescription>
                    </Alert>

                    <Button v-if="!editing" type="button" variant="ghost" size="sm" class="self-start -ml-2"
                        @click="selected = null">
                        <ArrowLeft class="size-4" />
                        Choose another service
                    </Button>

                    <Field>
                        <FieldLabel for="provider-name">Name</FieldLabel>
                        <Input id="provider-name" v-model="formName" autocomplete="off"
                            :class="nameError ? 'border-destructive focus-visible:ring-destructive/30' : ''" />
                        <FieldError v-if="nameError">{{ nameError }}</FieldError>
                    </Field>

                    <Field v-for="f in selected.fields" :key="f.key">
                        <FieldLabel :for="`provider-field-${f.key}`">
                            {{ f.label }}
                            <span v-if="!f.required || (f.secret && secretStored(f.key))"
                                class="text-muted-foreground font-normal">(optional)</span>
                        </FieldLabel>
                        <Input :id="`provider-field-${f.key}`" v-model="fieldInputs[f.key]!.value.value"
                            :type="f.secret ? 'password' : 'text'"
                            :placeholder="f.secret && secretStored(f.key) ? '(unchanged)' : (f.placeholder ?? '')"
                            :autocomplete="f.secret ? 'new-password' : 'off'"
                            :class="fieldInputs[f.key]!.errorMessage.value ? 'border-destructive focus-visible:ring-destructive/30' : ''" />
                        <p v-if="f.help" class="text-xs text-muted-foreground">{{ f.help }}</p>
                        <Button v-if="f.secret && !f.required && secretStored(f.key)" type="button" variant="link"
                            size="sm" class="self-start h-auto p-0 text-xs" @click="cleared.push(f.key)">
                            Remove the saved {{ f.label.toLowerCase() }}
                        </Button>
                        <FieldError v-if="fieldInputs[f.key]!.errorMessage.value">{{ fieldInputs[f.key]!.errorMessage.value }}</FieldError>
                    </Field>

                    <template v-if="selected.priorityHelp">
                        <Separator />
                        <div>
                            <p class="text-sm font-medium">Priority per event</p>
                            <p class="text-xs text-muted-foreground">
                                {{ selected.priorityHelp }} Leave empty for the service default.
                            </p>
                        </div>
                        <div class="grid grid-cols-2 gap-3">
                            <Field v-for="ev in EVENTS" :key="ev.key">
                                <FieldLabel :for="`provider-priority-${ev.key}`">{{ ev.label }}</FieldLabel>
                                <Input :id="`provider-priority-${ev.key}`"
                                    v-model="priorityInputs[ev.key]!.value.value" autocomplete="off"
                                    :class="priorityInputs[ev.key]!.errorMessage.value ? 'border-destructive focus-visible:ring-destructive/30' : ''" />
                                <FieldError v-if="priorityInputs[ev.key]!.errorMessage.value">{{ priorityInputs[ev.key]!.errorMessage.value }}</FieldError>
                            </Field>
                        </div>
                    </template>

                    <Separator />

                    <div class="flex items-center justify-between">
                        <div>
                            <p class="text-sm font-medium">Enabled</p>
                            <p class="text-xs text-muted-foreground">Send the enabled events to this provider.</p>
                        </div>
                        <Switch :model-value="formEnabled" @update:model-value="formEnabled = $event" />
                    </div>

                    <div class="flex flex-col items-start gap-2">
                        <Button type="button" variant="outline" size="sm" :disabled="testing" @click="sendTest">
                            <Loader2 v-if="testing" class="size-4 animate-spin" />
                            <Send v-else class="size-4" />
                            {{ testing ? 'Sending…' : 'Send test notification' }}
                        </Button>
                        <p class="text-xs text-muted-foreground">Sent with the priority of "Job failed".</p>
                        <p v-if="testResult" data-testid="provider-test-result" class="text-xs"
                            :class="testResult.ok ? 'text-emerald-600 dark:text-emerald-400' : 'text-destructive'">
                            {{ testResult.message }}
                        </p>
                    </div>

                    <SheetFooter class="mt-2 px-0">
                        <Button type="button" variant="outline" :disabled="isSubmitting"
                            @click="onSheetOpenChange(false)">Cancel</Button>
                        <Button type="submit" :disabled="isSubmitting">
                            <Loader2 v-if="isSubmitting" class="size-4 animate-spin" />
                            {{ isSubmitting ? 'Saving…' : (editing ? 'Save Changes' : 'Add Provider') }}
                        </Button>
                    </SheetFooter>
                </FieldGroup>
            </form>
        </SheetContent>
    </Sheet>

    <!-- ── Delete confirmation ───────────────────────────────────────────── -->
    <Dialog :open="deleteOpen" @update:open="deleteOpen = $event">
        <DialogContent class="max-w-sm">
            <DialogHeader>
                <DialogTitle>Delete notification provider?</DialogTitle>
                <DialogDescription>
                    <strong>{{ deleting?.name }}</strong> will stop receiving notifications and be removed,
                    with its pending deliveries. This action cannot be undone.
                </DialogDescription>
            </DialogHeader>
            <Alert v-if="deleteError" variant="destructive" class="mt-2">
                <AlertCircle class="size-4" />
                <AlertDescription>{{ deleteError }}</AlertDescription>
            </Alert>
            <DialogFooter class="mt-4">
                <Button variant="outline" :disabled="deleteSubmitting" @click="deleteOpen = false">Cancel</Button>
                <Button variant="destructive" :disabled="deleteSubmitting" @click="confirmDelete">
                    <Loader2 v-if="deleteSubmitting" class="size-4 animate-spin" />
                    {{ deleteSubmitting ? 'Deleting…' : 'Delete' }}
                </Button>
            </DialogFooter>
        </DialogContent>
    </Dialog>
</template>
