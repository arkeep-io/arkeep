<script setup lang="ts">
// SetPasswordDialog.vue — Admin action that sets another local user's
// password, e.g. when they forgot it and no SMTP server is configured for the
// reset link. The server signs the user out of every session. With "Require a
// new password" (the default) the user must choose their own at next sign-in.
import { ref, watch } from 'vue'
import { useForm, useField } from 'vee-validate'
import { toTypedSchema } from '@vee-validate/zod'
import { z } from 'zod'
import {
    Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle,
} from '@/components/ui/dialog'
import { Field, FieldError, FieldGroup, FieldLabel } from '@/components/ui/field'
import { Alert, AlertDescription } from '@/components/ui/alert'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Switch } from '@/components/ui/switch'
import { AlertCircle, Loader2 } from '@lucide/vue'
import { api, apiErrorMessage } from '@/services/api'
import { passwordSchema } from '@/lib/validation'
import type { User } from '@/types'

const props = defineProps<{
    open: boolean
    user: User | null
}>()

const emit = defineEmits<{
    'update:open': [value: boolean]
    // Emitted after the password was set so the parent can refresh.
    saved: []
}>()

// ─── Form ─────────────────────────────────────────────────────────────────────

const schema = toTypedSchema(
    z.object({
        password: passwordSchema,
        confirm: z.string(),
        must_change: z.boolean(),
    }).refine((d) => d.password === d.confirm, { path: ['confirm'], message: 'Passwords do not match' }),
)

const { handleSubmit, resetForm, isSubmitting } = useForm({
    validationSchema: schema,
    initialValues: { password: '', confirm: '', must_change: true },
})

const { value: passwordValue, errorMessage: passwordError } = useField<string>('password')
const { value: confirmValue, errorMessage: confirmError } = useField<string>('confirm')
const { value: mustChangeValue } = useField<boolean>('must_change')

const submitError = ref<string | null>(null)

// A fresh form every time the dialog opens: a password typed for one user must
// never be offered for the next.
watch(() => props.open, (open) => {
    if (open) {
        resetForm()
        submitError.value = null
    }
})

const onSubmit = handleSubmit(async (values) => {
    if (!props.user) return
    submitError.value = null
    try {
        await api(`/api/v1/users/${props.user.id}/password`, {
            method: 'POST',
            body: { password: values.password, must_change: values.must_change },
        })
        emit('update:open', false)
        emit('saved')
    } catch (e) {
        submitError.value = apiErrorMessage(e, 'Failed to set the password.')
    }
})
</script>

<template>
    <Dialog :open="props.open" @update:open="emit('update:open', $event)">
        <DialogContent class="sm:max-w-md">
            <DialogHeader>
                <DialogTitle>Set password</DialogTitle>
                <DialogDescription>
                    <span v-if="props.user">
                        Set a new password for <strong>{{ props.user.display_name }}</strong>
                        ({{ props.user.email }}). They will be signed out of all sessions.
                    </span>
                </DialogDescription>
            </DialogHeader>

            <form novalidate @submit="onSubmit">
                <FieldGroup class="flex flex-col gap-4">
                    <Alert v-if="submitError" variant="destructive">
                        <AlertCircle class="size-4" />
                        <AlertDescription>{{ submitError }}</AlertDescription>
                    </Alert>

                    <Field>
                        <FieldLabel for="set-password">New password</FieldLabel>
                        <Input id="set-password" v-model="passwordValue" type="password" autocomplete="new-password"
                            :class="passwordError ? 'border-destructive focus-visible:ring-destructive/30' : ''" />
                        <FieldError v-if="passwordError">{{ passwordError }}</FieldError>
                    </Field>

                    <Field>
                        <FieldLabel for="set-password-confirm">Confirm password</FieldLabel>
                        <Input id="set-password-confirm" v-model="confirmValue" type="password"
                            autocomplete="new-password"
                            :class="confirmError ? 'border-destructive focus-visible:ring-destructive/30' : ''" />
                        <FieldError v-if="confirmError">{{ confirmError }}</FieldError>
                    </Field>

                    <div class="flex items-center justify-between gap-4">
                        <div>
                            <p class="text-sm font-medium">Require a new password</p>
                            <p class="text-xs text-muted-foreground">
                                The user must choose their own password at next sign-in.
                            </p>
                        </div>
                        <Switch id="set-password-must-change" :model-value="mustChangeValue"
                            @update:model-value="mustChangeValue = $event" />
                    </div>

                    <DialogFooter>
                        <Button type="button" variant="outline" :disabled="isSubmitting"
                            @click="emit('update:open', false)">
                            Cancel
                        </Button>
                        <Button type="submit" :disabled="isSubmitting">
                            <Loader2 v-if="isSubmitting" class="size-4 animate-spin" />
                            {{ isSubmitting ? 'Saving…' : 'Set password' }}
                        </Button>
                    </DialogFooter>
                </FieldGroup>
            </form>
        </DialogContent>
    </Dialog>
</template>
