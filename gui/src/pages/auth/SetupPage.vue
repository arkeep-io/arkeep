<script setup lang="ts">
// SetupPage.vue — First-time server setup.
//
// Shown only when no users exist in the database (setup not yet completed).
// The router guard enforces this: once setup is done, /setup redirects to /login.
//
// Creates the first admin account via POST /api/v1/setup/complete, then
// calls setup.markCompleted() so the guard does not re-fetch the status,
// and redirects to /login.

import { ref } from 'vue'
import { useRouter } from 'vue-router'
import { useForm, useField } from 'vee-validate'
import { toTypedSchema } from '@vee-validate/zod'
import { z } from 'zod'
import { useSetupStore } from '@/stores/setup'
import { useAuthStore } from '@/stores/auth'
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
import { AlertCircle, Eye, EyeOff, Loader2, Moon, Sun } from 'lucide-vue-next'
import { useTheme } from '@/composables/useTheme'

// ─── Validation schema ────────────────────────────────────────────────────────

const schema = toTypedSchema(
    z.object({
        name: z.string().min(1, 'Name is required'),
        email: z.email('Enter a valid email address'),
        password: z.string().min(8, 'Password must be at least 8 characters'),
        confirmPassword: z.string().min(1, 'Please confirm your password'),
    }).refine((data) => data.password === data.confirmPassword, {
        message: 'Passwords do not match',
        path: ['confirmPassword'],
    }),
)

const { handleSubmit, isSubmitting } = useForm({ validationSchema: schema })

const { value: nameValue, errorMessage: nameError } = useField<string>('name')
const { value: emailValue, errorMessage: emailError } = useField<string>('email')
const { value: passwordValue, errorMessage: passwordError } = useField<string>('password')
const { value: confirmPasswordValue, errorMessage: confirmPasswordError } = useField<string>('confirmPassword')

// ─── State ────────────────────────────────────────────────────────────────────

const setup = useSetupStore()
const auth = useAuthStore()
const router = useRouter()
const { isDark, cycle, modeLabel } = useTheme()

const serverError = ref<string | null>(null)
const showPassword = ref(false)
const showConfirmPassword = ref(false)

// ─── Handlers ─────────────────────────────────────────────────────────────────

const onSubmit = handleSubmit(async (values) => {
    serverError.value = null
    try {
        await api('/api/v1/setup/complete', {
            method: 'POST',
            body: {
                name: values.name,
                email: values.email,
                password: values.password,
            },
        })
        setup.markCompleted()
        // Log in automatically so the user lands on the dashboard directly
        // without having to re-enter the credentials they just provided.
        await auth.login(values.email, values.password)
        router.push({ name: 'dashboard' })
    } catch {
        serverError.value = 'Setup failed. Please try again.'
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

        <div class="relative z-10 w-full max-w-sm md:max-w-4xl">
            <div class="flex flex-col gap-6">
                <Card class="p-0 overflow-hidden">
                    <CardContent class="grid p-0 md:grid-cols-2">

                        <!-- Form -->
                        <form class="p-6 md:p-8" novalidate @submit="onSubmit">
                            <FieldGroup>
                                <!-- Title -->
                                <div class="flex flex-col items-center gap-2 text-center">
                                    <h1 class="text-2xl font-bold">Welcome to Arkeep</h1>
                                    <p class="text-sm text-muted-foreground text-balance">
                                        Create your admin account to get started
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

                                <!-- Name -->
                                <Field>
                                    <FieldLabel for="name">Name</FieldLabel>
                                    <Input id="name" v-model="nameValue" type="text" placeholder="Jane Smith"
                                        autocomplete="new-password" autofocus spellcheck="false"
                                        :class="nameError ? 'border-destructive focus-visible:ring-destructive/30' : ''" />
                                    <FieldError v-if="nameError">{{ nameError }}</FieldError>
                                </Field>

                                <!-- Email -->
                                <Field>
                                    <FieldLabel for="email">Email</FieldLabel>
                                    <Input id="email" v-model="emailValue" type="email" placeholder="m@example.com"
                                        autocomplete="new-password" spellcheck="false"
                                        :class="emailError ? 'border-destructive focus-visible:ring-destructive/30' : ''" />
                                    <FieldError v-if="emailError">{{ emailError }}</FieldError>
                                </Field>

                                <!-- Password -->
                                <Field>
                                    <FieldLabel for="password">Password</FieldLabel>
                                    <div class="relative">
                                        <Input id="password" v-model="passwordValue"
                                            :type="showPassword ? 'text' : 'password'" placeholder="••••••••"
                                            autocomplete="new-password" class="pr-10"
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
                                    <FieldLabel for="confirmPassword">Confirm password</FieldLabel>
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
                                        {{ isSubmitting ? 'Creating account…' : 'Create account' }}
                                    </Button>
                                </Field>
                            </FieldGroup>
                        </form>

                        <!-- Decorative panel -->
                        <div class="relative hidden bg-black md:block overflow-hidden">
                            <video src="/login-bg.mp4" class="absolute inset-0 h-full w-full object-contain scale-130"
                                autoplay loop muted playsinline />
                        </div>

                    </CardContent>
                </Card>
            </div>
        </div>
    </div>

    <!-- Footer -->
    <p class="fixed bottom-0 left-0 right-0 text-center text-xs text-muted-foreground pb-6">
        Arkeep — open source backup management
    </p>
</template>