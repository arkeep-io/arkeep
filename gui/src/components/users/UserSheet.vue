<script setup lang="ts">
import { ref, computed } from 'vue'
import { z } from 'zod'
import {
    Sheet, SheetContent, SheetDescription, SheetFooter, SheetHeader, SheetTitle,
} from '@/components/ui/sheet'
import {
    Select, SelectContent, SelectItem, SelectTrigger, SelectValue,
} from '@/components/ui/select'
import {
    Field, FieldError, FieldGroup, FieldLabel,
} from '@/components/ui/field'
import { Alert, AlertDescription } from '@/components/ui/alert'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Switch } from '@/components/ui/switch'
import { AlertCircle, Loader2 } from 'lucide-vue-next'
import { api } from '@/services/api'
import type { User } from '@/types'

// ---------------------------------------------------------------------------
// Props / emits
// ---------------------------------------------------------------------------

const props = defineProps<{
    open: boolean
    // null = create mode, User = edit mode
    user: User | null
}>()

const emit = defineEmits<{
    'update:open': [value: boolean]
    // Emitted after a successful create or edit so the parent can refresh
    saved: []
}>()

// ---------------------------------------------------------------------------
// Derived state
// ---------------------------------------------------------------------------

const isEdit = computed(() => props.user !== null)

// ---------------------------------------------------------------------------
// Form state
// ---------------------------------------------------------------------------

const submitting = ref(false)
const submitError = ref<string | null>(null)

const fieldEmail = ref('')
const fieldDisplayName = ref('')
const fieldPassword = ref('')
const fieldRole = ref<'admin' | 'user'>('user')
const fieldIsActive = ref(true)
const fieldErrors = ref<Record<string, string>>({})

// ---------------------------------------------------------------------------
// Validation schemas
// ---------------------------------------------------------------------------

const createSchema = z.object({
    email: z.string().email('Must be a valid email address'),
    display_name: z.string().min(1, 'Display name is required'),
    password: z.string().min(8, 'Password must be at least 8 characters'),
    role: z.enum(['admin', 'user']),
})

const editSchema = z.object({
    display_name: z.string().min(1, 'Display name is required'),
    role: z.enum(['admin', 'user']),
})

// ---------------------------------------------------------------------------
// Reset / populate form when sheet opens or user changes
// ---------------------------------------------------------------------------

function reset() {
    fieldEmail.value = props.user?.email ?? ''
    fieldDisplayName.value = props.user?.display_name ?? ''
    fieldPassword.value = ''
    fieldRole.value = (props.user?.role as 'admin' | 'user') ?? 'user'
    fieldIsActive.value = props.user?.is_active ?? true
    fieldErrors.value = {}
    submitError.value = null
}

// Called by the parent via template ref when it opens the sheet
defineExpose({ reset })

// ---------------------------------------------------------------------------
// Sheet open/close passthrough
// ---------------------------------------------------------------------------

function onOpenChange(val: boolean) {
    emit('update:open', val)
}

// ---------------------------------------------------------------------------
// Validation
// ---------------------------------------------------------------------------

function validate(): boolean {
    fieldErrors.value = {}
    const schema = isEdit.value ? editSchema : createSchema
    const data = isEdit.value
        ? { display_name: fieldDisplayName.value, role: fieldRole.value }
        : {
            email: fieldEmail.value,
            display_name: fieldDisplayName.value,
            password: fieldPassword.value,
            role: fieldRole.value,
        }

    const result = schema.safeParse(data)
    if (!result.success) {
        for (const issue of result.error.issues) {
            fieldErrors.value[String(issue.path[0])] = issue.message
        }
        return false
    }
    return true
}

// ---------------------------------------------------------------------------
// Submit
// ---------------------------------------------------------------------------

async function onSubmit() {
    if (!validate()) return
    submitting.value = true
    submitError.value = null
    try {
        if (isEdit.value && props.user) {
            await api(`/api/v1/users/${props.user.id}`, {
                method: 'PATCH',
                body: {
                    display_name: fieldDisplayName.value,
                    role: fieldRole.value,
                    is_active: fieldIsActive.value,
                    // Only send password if the admin typed a new one
                    ...(fieldPassword.value ? { password: fieldPassword.value } : {}),
                },
            })
        } else {
            await api('/api/v1/users', {
                method: 'POST',
                body: {
                    email: fieldEmail.value,
                    display_name: fieldDisplayName.value,
                    password: fieldPassword.value,
                    role: fieldRole.value,
                },
            })
        }
        emit('update:open', false)
        emit('saved')
    } catch (e: any) {
        submitError.value = e?.data?.error?.message ?? e?.message ?? 'Failed to save user.'
    } finally {
        submitting.value = false
    }
}
</script>

<template>
    <Sheet :open="open" @update:open="onOpenChange">
        <SheetContent class="sm:max-w-md overflow-y-auto">
            <SheetHeader>
                <SheetTitle>{{ isEdit ? 'Edit User' : 'New User' }}</SheetTitle>
                <SheetDescription>
                    {{ isEdit ? 'Update account details and access level.' : 'Create a new local user account.' }}
                </SheetDescription>
            </SheetHeader>

            <form class="py-6 px-4" novalidate @submit.prevent="onSubmit">
                <FieldGroup class="flex flex-col gap-4">

                    <!-- Error banner -->
                    <Transition enter-active-class="transition-all duration-200"
                        enter-from-class="-translate-y-1 opacity-0" leave-active-class="transition-all duration-150"
                        leave-to-class="-translate-y-1 opacity-0">
                        <Alert v-if="submitError" variant="destructive">
                            <AlertCircle class="size-4" />
                            <AlertDescription>{{ submitError }}</AlertDescription>
                        </Alert>
                    </Transition>

                    <!-- Email — create only -->
                    <Field v-if="!isEdit">
                        <FieldLabel for="user-email">Email</FieldLabel>
                        <Input id="user-email" v-model="fieldEmail" type="email" placeholder="user@example.com"
                            autocomplete="off"
                            :class="fieldErrors.email ? 'border-destructive focus-visible:ring-destructive/30' : ''" />
                        <FieldError v-if="fieldErrors.email">{{ fieldErrors.email }}</FieldError>
                    </Field>

                    <!-- Email read-only in edit mode -->
                    <div v-else class="flex flex-col gap-1.5">
                        <p class="text-sm font-medium">Email</p>
                        <p class="text-sm text-muted-foreground font-mono">{{ fieldEmail }}</p>
                    </div>

                    <!-- Display name -->
                    <Field>
                        <FieldLabel for="user-display-name">Display Name</FieldLabel>
                        <Input id="user-display-name" v-model="fieldDisplayName" placeholder="Jane Doe"
                            autocomplete="off"
                            :class="fieldErrors.display_name ? 'border-destructive focus-visible:ring-destructive/30' : ''" />
                        <FieldError v-if="fieldErrors.display_name">{{ fieldErrors.display_name }}</FieldError>
                    </Field>

                    <!-- Password — create only; in edit mode the admin cannot set the password here.
               Users change their own password from the Profile page. -->
                    <template v-if="!isEdit">
                        <Field>
                            <FieldLabel for="user-password">Password</FieldLabel>
                            <Input id="user-password" v-model="fieldPassword" type="password"
                                autocomplete="new-password"
                                :class="fieldErrors.password ? 'border-destructive focus-visible:ring-destructive/30' : ''" />
                            <FieldError v-if="fieldErrors.password">{{ fieldErrors.password }}</FieldError>
                        </Field>
                    </template>

                    <!-- Role -->
                    <Field>
                        <FieldLabel>Role</FieldLabel>
                        <Select :model-value="fieldRole" @update:model-value="fieldRole = $event as 'admin' | 'user'">
                            <SelectTrigger>
                                <SelectValue />
                            </SelectTrigger>
                            <SelectContent>
                                <SelectItem value="user">User</SelectItem>
                                <SelectItem value="admin">Admin</SelectItem>
                            </SelectContent>
                        </Select>
                    </Field>

                    <!-- Active toggle — edit only -->
                    <div v-if="isEdit" class="flex items-center justify-between pt-1">
                        <div>
                            <p class="text-sm font-medium">Active</p>
                            <p class="text-xs text-muted-foreground">Inactive users cannot log in.</p>
                        </div>
                        <Switch :model-value="fieldIsActive" @update:model-value="fieldIsActive = $event" />
                    </div>

                    <SheetFooter class="mt-2 px-0">
                        <Button type="button" variant="outline" :disabled="submitting"
                            @click="emit('update:open', false)">
                            Cancel
                        </Button>
                        <Button type="submit" :disabled="submitting">
                            <Loader2 v-if="submitting" class="size-4 animate-spin" />
                            {{ submitting ? 'Saving…' : (isEdit ? 'Save Changes' : 'Create User') }}
                        </Button>
                    </SheetFooter>

                </FieldGroup>
            </form>
        </SheetContent>
    </Sheet>
</template>