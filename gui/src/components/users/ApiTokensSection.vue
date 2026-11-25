<script setup lang="ts">
import { computed, onMounted, ref } from 'vue'
import { getLocalTimeZone, parseDate, today } from '@internationalized/date'
import type { DateValue } from 'reka-ui'
import { useForm, useField } from 'vee-validate'
import { toTypedSchema } from '@vee-validate/zod'
import { z } from 'zod'
import { Alert, AlertDescription } from '@/components/ui/alert'
import { Button } from '@/components/ui/button'
import { Field, FieldDescription, FieldError, FieldGroup, FieldLabel } from '@/components/ui/field'
import { Input } from '@/components/ui/input'
import { Calendar } from '@/components/ui/calendar'
import { Popover, PopoverContent, PopoverTrigger } from '@/components/ui/popover'
import {
    Select, SelectContent, SelectItem, SelectTrigger, SelectValue,
} from '@/components/ui/select'
import {
    Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle,
} from '@/components/ui/dialog'
import ApiTokenRevokeDialog from '@/components/users/ApiTokenRevokeDialog.vue'
import { AlertCircle, CalendarIcon, KeyRound, Loader2 } from '@lucide/vue'
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

// Expiry choices, in days; "custom" sends the end of the chosen day, "never"
// sends no expiry.
const expiryOptions = [
    { value: '7', label: '7 days' },
    { value: '30', label: '30 days' },
    { value: '90', label: '90 days' },
    { value: '365', label: '1 year' },
    { value: 'custom', label: 'Custom' },
    { value: 'never', label: 'No expiry' },
]

// The range of a custom expiry date: from tomorrow, up to the last day whose
// end is within the server's limit of 3650 days.
const minExpiryDate = today(getLocalTimeZone()).add({ days: 1 })
const maxExpiryDate = today(getLocalTimeZone()).add({ days: 3649 })

// endOfDay is the last second of the day "YYYY-MM-DD" in the browser's time
// zone: a token with a custom date works for the whole of that day.
function endOfDay(day: string): Date {
    return new Date(parseDate(day).add({ days: 1 }).toDate(getLocalTimeZone()).getTime() - 1000)
}

const createDialogOpen = ref(false)
const createError = ref<string | null>(null)
const createdToken = ref<CreatedApiToken | null>(null)
const copied = ref(false)

const createForm = useForm({
    validationSchema: toTypedSchema(z.object({
        name: z.string().trim().min(1, 'Name is required').max(100, 'At most 100 characters'),
        expiry: z.string(),
        // The chosen day as "YYYY-MM-DD" when expiry is "custom".
        expiry_date: z.string(),
    }).superRefine((data, ctx) => {
        if (data.expiry === 'custom' && !data.expiry_date) {
            ctx.addIssue({ code: 'custom', path: ['expiry_date'], message: 'Pick a date' })
        }
    })),
    initialValues: { name: '', expiry: '365', expiry_date: '' },
})
const { isSubmitting: creating } = createForm
const { value: name, errorMessage: nameError } = useField<string>('name', undefined, { form: createForm })
const { value: expiry } = useField<string>('expiry', undefined, { form: createForm })
const { value: expiryDate, errorMessage: expiryDateError } = useField<string>('expiry_date', undefined, { form: createForm })

const datePickerOpen = ref(false)

// The calendar's value: the chosen day, or none yet.
const calendarDate = computed<DateValue | undefined>({
    get: () => (expiryDate.value ? parseDate(expiryDate.value) : undefined),
    set: (v) => {
        expiryDate.value = v ? v.toString() : ''
        datePickerOpen.value = false
    },
})

// The date the token will expire, as the form stands; null for no expiry or
// a custom date not picked yet.
const expiresOn = computed<Date | null>(() => {
    if (expiry.value === 'never') return null
    if (expiry.value === 'custom') return expiryDate.value ? endOfDay(expiryDate.value) : null
    return new Date(Date.now() + Number(expiry.value) * 24 * 60 * 60 * 1000)
})

function formatDay(d: Date): string {
    return d.toLocaleDateString(undefined, { dateStyle: 'long' })
}

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
                ...(values.expiry === 'custom'
                    ? { expires_at: endOfDay(values.expiry_date).toISOString() }
                    : { expires_in_days: values.expiry === 'never' ? null : Number(values.expiry) }),
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
                        <FieldDescription v-if="expiry !== 'custom'">
                            {{ expiresOn ? `The token will expire on ${formatDay(expiresOn)}.` : 'The token will never expire.' }}
                            You can revoke it at any time.
                        </FieldDescription>
                    </Field>
                    <Field v-if="expiry === 'custom'">
                        <FieldLabel for="api-token-expiry-date">Expiry date</FieldLabel>
                        <Popover v-model:open="datePickerOpen">
                            <PopoverTrigger as-child>
                                <Button id="api-token-expiry-date" variant="outline"
                                    class="w-56 justify-start font-normal"
                                    :class="[
                                        expiresOn ? '' : 'text-muted-foreground',
                                        expiryDateError ? 'border-destructive focus-visible:ring-destructive/30' : '',
                                    ]">
                                    <CalendarIcon class="size-4" />
                                    {{ expiresOn ? formatDay(expiresOn) : 'Pick a date' }}
                                </Button>
                            </PopoverTrigger>
                            <PopoverContent class="w-auto p-0" align="start">
                                <Calendar v-model="calendarDate" :min-value="minExpiryDate"
                                    :max-value="maxExpiryDate" initial-focus />
                            </PopoverContent>
                        </Popover>
                        <FieldError v-if="expiryDateError">{{ expiryDateError }}</FieldError>
                        <FieldDescription v-else>
                            The token works until the end of that day. You can revoke it at any time.
                        </FieldDescription>
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
