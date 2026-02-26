<script setup lang="ts">
// LoginPage.vue — Authentication entry point.
//
// Form pattern: shadcn-vue Field/FieldLabel/FieldError for layout and styling,
// vee-validate useField() for validation binding — no <Field> component from
// vee-validate in the template to avoid naming conflicts with shadcn Field.
//
// Auth flows:
//   1. Local: email/password → POST /api/v1/auth/login
//   2. OIDC:  full-page redirect to /api/v1/auth/oidc/login

import { computed, ref } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { useForm, useField } from 'vee-validate'
import { toTypedSchema } from '@vee-validate/zod'
import { z } from 'zod'
import { useAuthStore } from '@/stores/auth'
import { Alert, AlertDescription } from '@/components/ui/alert'
import { Button } from '@/components/ui/button'
import { Card, CardContent } from '@/components/ui/card'
import {
    Field,
    FieldError,
    FieldGroup,
    FieldLabel,
    FieldSeparator,
} from '@/components/ui/field'
import { Input } from '@/components/ui/input'
import { AlertCircle, Eye, EyeOff, Loader2, Moon, Sun } from 'lucide-vue-next'
import { useTheme } from '@/composables/useTheme'

// ─── Validation schema ────────────────────────────────────────────────────────

const schema = toTypedSchema(
    z.object({
        email: z.email('Enter a valid email address'),
        password: z.string('Password is required').min(1, 'Password is required'),
    }),
)

const { handleSubmit, isSubmitting } = useForm({ validationSchema: schema })

// useField binds each input to vee-validate
const { value: emailValue, errorMessage: emailError } = useField<string>('email')
const { value: passwordValue, errorMessage: passwordError } = useField<string>('password')

// ─── State ────────────────────────────────────────────────────────────────────

const auth = useAuthStore()
const router = useRouter()
const route = useRoute()
const { isDark, cycle, modeLabel } = useTheme()

const serverError = ref<string | null>(null)
const showPassword = ref(false)
const oidcLoading = ref(false)

const redirectTo = computed(() =>
    typeof route.query.redirect === 'string' ? route.query.redirect : '/dashboard',
)

// ─── Handlers ─────────────────────────────────────────────────────────────────

const onSubmit = handleSubmit(async (values) => {
    serverError.value = null
    try {
        await auth.login(values.email, values.password)
        router.push(redirectTo.value)
    } catch {
        serverError.value = 'Invalid email or password'
    }
})

function loginWithOIDC(): void {
    oidcLoading.value = true
    window.location.href = '/api/v1/auth/oidc/login'
}
</script>

<template>
    <div class="relative flex flex-col items-center justify-center w-full p-6 min-h-svh md:p-10">
        <!-- Background -->
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
                                    <h1 class="text-2xl font-bold">Welcome back</h1>
                                    <p class="text-sm text-muted-foreground text-balance">
                                        Login to your Arkeep account
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

                                <!-- Email -->
                                <Field>
                                    <FieldLabel for="email">Email</FieldLabel>
                                    <Input id="email" v-model="emailValue" type="email" placeholder="m@example.com"
                                        autocomplete="email" autofocus spellcheck="false"
                                        :class="emailError ? 'border-destructive focus-visible:ring-destructive/30' : ''" />
                                    <FieldError v-if="emailError">{{ emailError }}</FieldError>
                                </Field>

                                <!-- Password -->
                                <Field>
                                    <div class="flex items-center">
                                        <FieldLabel for="password">Password</FieldLabel>
                                        <a href="#" class="ml-auto text-sm underline-offset-2 hover:underline">
                                            Forgot password?
                                        </a>
                                    </div>
                                    <div class="relative">
                                        <Input id="password" v-model="passwordValue"
                                            :type="showPassword ? 'text' : 'password'" placeholder="••••••••"
                                            autocomplete="current-password" class="pr-10"
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

                                <!-- Submit -->
                                <Field>
                                    <Button type="submit" :disabled="isSubmitting">
                                        <Loader2 v-if="isSubmitting" class="size-4 animate-spin" />
                                        {{ isSubmitting ? 'Signing in…' : 'Sign in' }}
                                    </Button>
                                </Field>

                                <!-- Separator -->
                                <FieldSeparator class="*:data-[slot=field-separator-content]:bg-card">
                                    Or continue with
                                </FieldSeparator>

                                <!-- OIDC -->
                                <Field>
                                    <Button type="button" variant="outline" :disabled="oidcLoading"
                                        @click="loginWithOIDC">
                                        <Loader2 v-if="oidcLoading" class="size-4 animate-spin" />
                                        Continue with SSO
                                    </Button>
                                </Field>
                            </FieldGroup>
                        </form>

                        <!-- Decorative panel -->
                        <div class="relative hidden bg-muted md:block">
                            <img src="https://placehold.co/600x400" alt="Image"
                                class="absolute inset-0 h-full w-full object-cover dark:brightness-[0.2] dark:grayscale" />
                        </div>

                    </CardContent>
                </Card>
                <p class="px-6 text-sm text-center">
                    Arkeep — open source backup management
                </p>
            </div>
        </div>
    </div>
</template>