<script setup lang="ts">
// ResetPasswordPage.vue — Set a new password from a reset link.
//
// Reached via the link emailed by the forgot-password flow:
//   {origin}/auth/reset-password?token=<raw>
//
// Submits POST /api/v1/auth/password-reset/confirm with the token and the new
// password. On success the user's other sessions are invalidated server-side
// and they are sent back to the login page.

import { computed, ref } from 'vue'
import { RouterLink, useRoute } from 'vue-router'
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
import { AlertCircle, ArrowLeft, CheckCircle2, Eye, EyeOff, Loader2, Moon, Sun } from '@lucide/vue'
import { useTheme } from '@/composables/useTheme'

// ─── Validation schema ────────────────────────────────────────────────────────

const schema = toTypedSchema(
    z.object({
        password: z.string().min(8, 'Password must be at least 8 characters'),
        confirmPassword: z.string().min(1, 'Please confirm your password'),
    }).refine((data) => data.password === data.confirmPassword, {
        message: 'Passwords do not match',
        path: ['confirmPassword'],
    }),
)

const { handleSubmit, isSubmitting } = useForm({ validationSchema: schema })
const { value: passwordValue, errorMessage: passwordError } = useField<string>('password')
const { value: confirmPasswordValue, errorMessage: confirmPasswordError } = useField<string>('confirmPassword')

// ─── State ────────────────────────────────────────────────────────────────────

const route = useRoute()
const { isDark, cycle, modeLabel } = useTheme()

const token = computed(() =>
    typeof route.query.token === 'string' ? route.query.token : '',
)

const succeeded = ref(false)
const serverError = ref<string | null>(null)
const showPassword = ref(false)
const showConfirmPassword = ref(false)

// ─── Handlers ─────────────────────────────────────────────────────────────────

const onSubmit = handleSubmit(async (values) => {
    serverError.value = null
    try {
        await api('/api/v1/auth/password-reset/confirm', {
            method: 'POST',
            body: { token: token.value, password: values.password },
        })
        succeeded.value = true
    } catch (err: any) {
        // The most common failure is an invalid/expired token (400). Surface a
        // clear message with a path to request a fresh link.
        serverError.value = err?.status === 400 || err?.response?.status === 400
            ? 'This reset link is invalid or has expired. Request a new one.'
            : 'Something went wrong. Please try again later.'
    }
})
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
                            <h1 class="text-2xl font-bold">Reset password</h1>
                            <p class="text-sm text-muted-foreground text-balance">
                                Choose a new password for your Arkeep account
                            </p>
                        </div>

                        <!-- Missing token — the link is malformed -->
                        <template v-if="!token">
                            <Alert variant="destructive">
                                <AlertCircle class="size-4" />
                                <AlertDescription>
                                    This reset link is invalid. Please request a new one.
                                </AlertDescription>
                            </Alert>
                            <RouterLink to="/auth/forgot-password"
                                class="inline-flex items-center justify-center gap-1 text-sm text-muted-foreground hover:text-foreground">
                                <ArrowLeft class="size-4" />
                                Request a new link
                            </RouterLink>
                        </template>

                        <!-- Success — password updated -->
                        <template v-else-if="succeeded">
                            <Alert>
                                <CheckCircle2 class="size-4" />
                                <AlertDescription>
                                    Your password has been updated. You can now sign in with your
                                    new password.
                                </AlertDescription>
                            </Alert>
                            <Button as-child class="w-full">
                                <RouterLink to="/login">Back to login</RouterLink>
                            </Button>
                        </template>

                        <!-- Reset form -->
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

                                <!-- Password -->
                                <Field>
                                    <FieldLabel for="password">New password</FieldLabel>
                                    <div class="relative">
                                        <Input id="password" v-model="passwordValue"
                                            :type="showPassword ? 'text' : 'password'" placeholder="••••••••"
                                            autocomplete="new-password" autofocus class="pr-10"
                                            :class="passwordError ? 'border-destructive focus-visible:ring-destructive/30' : ''" />
                                        <button type="button"
                                            class="absolute transition-colors -translate-y-1/2 right-3 top-1/2 text-muted-foreground hover:text-foreground"
                                            :aria-label="showPassword ? 'Hide password' : 'Show password'"
                                            @click="showPassword = !showPassword">
                                            <EyeOff v-if="showPassword" class="size-4" />
                                            <Eye v-else class="size-4" />
                                        </button>
                                    </div>
                                    <FieldError v-if="passwordError">{{ passwordError }}</FieldError>
                                </Field>

                                <!-- Confirm password -->
                                <Field>
                                    <FieldLabel for="confirmPassword">Confirm new password</FieldLabel>
                                    <div class="relative">
                                        <Input id="confirmPassword" v-model="confirmPasswordValue"
                                            :type="showConfirmPassword ? 'text' : 'password'" placeholder="••••••••"
                                            autocomplete="new-password" class="pr-10"
                                            :class="confirmPasswordError ? 'border-destructive focus-visible:ring-destructive/30' : ''" />
                                        <button type="button"
                                            class="absolute transition-colors -translate-y-1/2 right-3 top-1/2 text-muted-foreground hover:text-foreground"
                                            :aria-label="showConfirmPassword ? 'Hide password' : 'Show password'"
                                            @click="showConfirmPassword = !showConfirmPassword">
                                            <EyeOff v-if="showConfirmPassword" class="size-4" />
                                            <Eye v-else class="size-4" />
                                        </button>
                                    </div>
                                    <FieldError v-if="confirmPasswordError">{{ confirmPasswordError }}</FieldError>
                                </Field>

                                <!-- Submit -->
                                <Field>
                                    <Button type="submit" class="w-full" :disabled="isSubmitting">
                                        <Loader2 v-if="isSubmitting" class="size-4 animate-spin" />
                                        {{ isSubmitting ? 'Updating…' : 'Update password' }}
                                    </Button>
                                </Field>

                                <!-- Back to login -->
                                <RouterLink to="/login"
                                    class="inline-flex items-center justify-center gap-1 text-sm text-muted-foreground hover:text-foreground">
                                    <ArrowLeft class="size-4" />
                                    Back to login
                                </RouterLink>
                            </FieldGroup>
                        </form>
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
