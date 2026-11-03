<script setup lang="ts">
// PasswordChangeForm.vue — Login step of an account whose password was set by
// an admin with "require a new password": the credentials (and the code, on a
// two-factor account) are verified, but no session exists until a new
// password is chosen. Emits `verified` once the tokens are issued and `back`
// when the user returns to the credentials step.
import { ref } from 'vue'
import { useForm, useField } from 'vee-validate'
import { toTypedSchema } from '@vee-validate/zod'
import { z } from 'zod'
import { useAuthStore } from '@/stores/auth'
import { apiErrorMessage } from '@/services/api'
import { passwordSchema } from '@/lib/validation'
import { Alert, AlertDescription } from '@/components/ui/alert'
import { Button } from '@/components/ui/button'
import { Field, FieldError, FieldGroup, FieldLabel } from '@/components/ui/field'
import { Input } from '@/components/ui/input'
import { AlertCircle, ArrowLeft, Loader2 } from '@lucide/vue'

const props = defineProps<{
    challengeToken: string
}>()

const emit = defineEmits<{
    verified: []
    back: []
}>()

// ─── Validation schema ────────────────────────────────────────────────────────

const schema = toTypedSchema(
    z.object({
        password: passwordSchema,
        confirm: z.string(),
    }).refine((d) => d.password === d.confirm, { path: ['confirm'], message: 'Passwords do not match' }),
)

const { handleSubmit, isSubmitting } = useForm({
    validationSchema: schema,
    initialValues: { password: '', confirm: '' },
})

const { value: passwordValue, errorMessage: passwordError } = useField<string>('password')
const { value: confirmValue, errorMessage: confirmError } = useField<string>('confirm')

// ─── State ────────────────────────────────────────────────────────────────────

const auth = useAuthStore()
const serverError = ref<string | null>(null)

// saving stays true after success, while the parent navigates away.
const saving = ref(false)

// ─── Handlers ─────────────────────────────────────────────────────────────────

const onSubmit = handleSubmit(async (values) => {
    serverError.value = null
    saving.value = true
    try {
        await auth.completePasswordChange(props.challengeToken, values.password)
        emit('verified')
    } catch (e) {
        saving.value = false
        serverError.value = apiErrorMessage(e, 'Could not set the new password. Sign in again.')
    }
})
</script>

<template>
    <form class="p-6 md:p-8" novalidate @submit="onSubmit">
        <FieldGroup>
            <!-- Title -->
            <div class="flex flex-col items-center gap-2 text-center">
                <h1 class="text-2xl font-bold">Choose a new password</h1>
                <p class="text-sm text-muted-foreground text-balance">
                    An administrator set your password. Choose your own to continue.
                </p>
            </div>

            <!-- Server error -->
            <Transition enter-active-class="transition-all duration-200"
                enter-from-class="-translate-y-1 opacity-0"
                leave-active-class="transition-all duration-150"
                leave-to-class="-translate-y-1 opacity-0">
                <Alert v-if="serverError" variant="destructive">
                    <AlertCircle class="size-4" />
                    <AlertDescription>{{ serverError }}</AlertDescription>
                </Alert>
            </Transition>

            <!-- New password -->
            <Field>
                <FieldLabel for="new-password">New password</FieldLabel>
                <Input id="new-password" v-model="passwordValue" type="password" autocomplete="new-password"
                    autofocus :disabled="saving"
                    :class="passwordError ? 'border-destructive focus-visible:ring-destructive/30' : ''" />
                <FieldError v-if="passwordError">{{ passwordError }}</FieldError>
            </Field>

            <!-- Confirmation -->
            <Field>
                <FieldLabel for="confirm-password">Confirm new password</FieldLabel>
                <Input id="confirm-password" v-model="confirmValue" type="password" autocomplete="new-password"
                    :disabled="saving"
                    :class="confirmError ? 'border-destructive focus-visible:ring-destructive/30' : ''" />
                <FieldError v-if="confirmError">{{ confirmError }}</FieldError>
            </Field>

            <!-- Submit -->
            <Field>
                <Button type="submit" class="w-full" :disabled="isSubmitting || saving">
                    <Loader2 v-if="isSubmitting || saving" class="size-4 animate-spin" />
                    {{ isSubmitting || saving ? 'Saving…' : 'Set password and sign in' }}
                </Button>
            </Field>

            <!-- Back to sign in -->
            <button type="button" :disabled="saving"
                class="inline-flex items-center justify-center gap-1 text-sm text-muted-foreground hover:text-foreground disabled:pointer-events-none disabled:opacity-50"
                @click="emit('back')">
                <ArrowLeft class="size-4" />
                Back to sign in
            </button>
        </FieldGroup>
    </form>
</template>
