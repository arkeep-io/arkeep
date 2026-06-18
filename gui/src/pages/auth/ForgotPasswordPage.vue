<script setup lang="ts">
// ForgotPasswordPage.vue — Self-service password reset request.
//
// Only local accounts can reset their password (OIDC users are managed by their
// identity provider). The flow depends on a configured SMTP server: on mount we
// query GET /api/v1/auth/password-reset/status and, when SMTP is not configured,
// show a "contact your administrator" message instead of the request form.
//
// The request response is intentionally generic regardless of whether the email
// maps to a local account, so this page never reveals which addresses exist.

import { onMounted, ref } from 'vue'
import { RouterLink } from 'vue-router'
import { useForm, useField } from 'vee-validate'
import { toTypedSchema } from '@vee-validate/zod'
import { z } from 'zod'
import { api } from '@/services/api'
import { Alert, AlertDescription } from '@/components/ui/alert'
import { Button } from '@/components/ui/button'
import { Card, CardContent } from '@/components/ui/card'
import {
    Field,
    FieldError,
    FieldGroup,
    FieldLabel,
} from '@/components/ui/field'
import { Input } from '@/components/ui/input'
import { AlertCircle, ArrowLeft, CheckCircle2, Loader2, MailWarning, Moon, Sun } from '@lucide/vue'
import { useTheme } from '@/composables/useTheme'

// ─── Validation schema ────────────────────────────────────────────────────────

const schema = toTypedSchema(
    z.object({
        email: z.email('Enter a valid email address'),
    }),
)

const { handleSubmit, isSubmitting } = useForm({ validationSchema: schema })
const { value: emailValue, errorMessage: emailError } = useField<string>('email')

// ─── State ────────────────────────────────────────────────────────────────────

const { isDark, cycle, modeLabel } = useTheme()

// null = still loading the SMTP status; true/false once known.
const smtpConfigured = ref<boolean | null>(null)
const submitted = ref(false)
const serverError = ref<string | null>(null)

// ─── Handlers ─────────────────────────────────────────────────────────────────

async function fetchStatus(): Promise<void> {
    try {
        const res = await api<{ data: { smtp_configured: boolean } }>(
            '/api/v1/auth/password-reset/status',
        )
        smtpConfigured.value = res.data.smtp_configured
    } catch {
        // If the status check fails, assume SMTP is unavailable so the user is
        // pointed to their administrator rather than a form that cannot work.
        smtpConfigured.value = false
    }
}

const onSubmit = handleSubmit(async (values) => {
    serverError.value = null
    try {
        await api('/api/v1/auth/password-reset/request', {
            method: 'POST',
            body: { email: values.email },
        })
        submitted.value = true
    } catch {
        serverError.value = 'Something went wrong. Please try again later.'
    }
})

onMounted(fetchStatus)
</script>

<template>
    <div class="relative flex flex-col items-center justify-center w-full p-6 min-h-svh md:p-10">
        <!-- Background grid -->
        <div class="absolute inset-0 z-0" :style="{
            backgroundImage: `
                linear-gradient(to right, ${isDark ? '#3f3f46' : '#d1d5db'} 1px, transparent 1px),
                linear-gradient(to bottom, ${isDark ? '#3f3f46' : '#d1d5db'} 1px, transparent 1px)
            `,
            backgroundSize: '32px 32px',
            WebkitMaskImage: 'radial-gradient(ellipse 60% 60% at 50% 50%, #000 30%, transparent 70%)',
            maskImage: 'radial-gradient(ellipse 60% 60% at 50% 50%, #000 30%, transparent 70%)',
        }" />

        <!-- Theme toggle -->
        <Button variant="ghost" size="icon"
            class="absolute z-10 top-4 right-4 text-muted-foreground hover:text-foreground" :aria-label="modeLabel"
            @click="cycle()">
            <Sun v-if="isDark" class="size-4" />
            <Moon v-else class="size-4" />
        </Button>

        <div class="relative z-10 w-full max-w-sm">
            <Card>
                <CardContent class="p-6 md:p-8">
                    <FieldGroup>
                        <!-- Title -->
                        <div class="flex flex-col items-center gap-2 text-center">
                            <h1 class="text-2xl font-bold">Forgot password</h1>
                            <p class="text-sm text-muted-foreground text-balance">
                                Reset the password for your local Arkeep account
                            </p>
                        </div>

                        <!-- Loading status -->
                        <div v-if="smtpConfigured === null" class="flex justify-center py-4 text-muted-foreground">
                            <Loader2 class="size-5 animate-spin" />
                        </div>

                        <!-- SMTP not configured — point the user to their administrator -->
                        <Alert v-else-if="smtpConfigured === false">
                            <MailWarning class="size-4" />
                            <AlertDescription>
                                This instance does not have an SMTP server configured, so password
                                reset emails cannot be sent. Please contact your system administrator
                                to reset your password.
                            </AlertDescription>
                        </Alert>

                        <!-- Request submitted — generic confirmation -->
                        <Alert v-else-if="submitted">
                            <CheckCircle2 class="size-4" />
                            <AlertDescription>
                                If an account with that email exists, a password reset link has been
                                sent. Check your inbox and follow the instructions.
                            </AlertDescription>
                        </Alert>

                        <!-- Request form -->
                        <form v-else novalidate @submit="onSubmit">
                            <FieldGroup>
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

                                <!-- Email -->
                                <Field>
                                    <FieldLabel for="email">Email</FieldLabel>
                                    <Input id="email" v-model="emailValue" type="email" placeholder="m@example.com"
                                        autocomplete="email" autofocus spellcheck="false"
                                        :class="emailError ? 'border-destructive focus-visible:ring-destructive/30' : ''" />
                                    <FieldError v-if="emailError">{{ emailError }}</FieldError>
                                </Field>

                                <!-- Submit -->
                                <Field>
                                    <Button type="submit" class="w-full" :disabled="isSubmitting">
                                        <Loader2 v-if="isSubmitting" class="size-4 animate-spin" />
                                        {{ isSubmitting ? 'Sending…' : 'Send reset link' }}
                                    </Button>
                                </Field>
                            </FieldGroup>
                        </form>

                        <!-- Back to login -->
                        <RouterLink to="/login"
                            class="inline-flex items-center justify-center gap-1 text-sm text-muted-foreground hover:text-foreground">
                            <ArrowLeft class="size-4" />
                            Back to login
                        </RouterLink>
                    </FieldGroup>
                </CardContent>
            </Card>
        </div>
    </div>

    <!-- Footer -->
    <p class="fixed bottom-0 left-0 right-0 text-center text-xs text-muted-foreground pb-6">
        Arkeep — open source backup management
    </p>
</template>
