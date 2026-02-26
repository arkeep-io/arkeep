<script setup lang="ts">
// LoginPage.vue — Authentication entry point.
//
// Handles two auth flows:
//   1. Local: email/password validated with VeeValidate + Zod via <Field />
//   2. OIDC: full-page redirect to /api/v1/auth/oidc/login
//
// On success, redirects to ?redirect= destination or /dashboard.

import { ref, computed } from 'vue'
import { useRouter, useRoute } from 'vue-router'
import { useForm, Field, ErrorMessage } from 'vee-validate'
import { toTypedSchema } from '@vee-validate/zod'
import { z } from 'zod'
import { useAuthStore } from '@/stores/auth'
import { useTheme } from '@/composables/useTheme'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { Alert, AlertDescription } from '@/components/ui/alert'
import { Separator } from '@/components/ui/separator'
import {
    Eye,
    EyeOff,
    AlertCircle,
    Loader2,
    Moon,
    Sun,
    Monitor,
} from 'lucide-vue-next'

// ─── Validation ───────────────────────────────────────────────────────────────

const { handleSubmit, isSubmitting } = useForm({
    validationSchema: toTypedSchema(
        z.object({
            email: z.email('Enter a valid email address'),
            password: z
                .string('Password is required')
                .min(1, 'Password is required'),
        }),
    ),
})

// ─── State ────────────────────────────────────────────────────────────────────

const auth = useAuthStore()
const { mode, cycle } = useTheme()
const router = useRouter()
const route = useRoute()

const serverError = ref<string | null>(null)
const showPassword = ref(false)
const oidcLoading = ref(false)

const redirectTo = computed(() =>
    typeof route.query.redirect === 'string' ? route.query.redirect : '/dashboard',
)

// Icon and label for the current color mode state
const modeIcon = computed(() => {
    if (mode.value === 'dark') return Moon
    if (mode.value === 'light') return Sun
    return Monitor
})

const modeLabel = computed(() => {
    if (mode.value === 'dark') return 'Dark mode'
    if (mode.value === 'light') return 'Light mode'
    return 'System mode'
})

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
    // Full-page redirect — server handles OAuth flow and returns to
    // /?token=<access_token> which OIDCCallbackPage processes.
    window.location.href = '/api/v1/auth/oidc/login'
}
</script>

<template>
    <div class="relative min-h-dvh flex items-center justify-center bg-background p-4">

        <!-- Theme cycle button -->
        <Button variant="ghost" size="icon" class="absolute top-4 right-4 text-muted-foreground hover:text-foreground"
            :aria-label="modeLabel" @click="cycle()">
            <component :is="modeIcon" class="size-4" />
        </Button>

        <div class="w-full max-w-sm">

            <!-- Card -->
            <div class="rounded-lg border border-border bg-card text-card-foreground shadow-sm p-8">

                <!-- Header -->
                <div class="mb-8 text-center">
                    <div
                        class="inline-flex items-center justify-center size-10 rounded-lg bg-primary/10 border border-primary/20 mb-4">
                        <svg width="18" height="18" viewBox="0 0 16 16" fill="none" aria-hidden="true">
                            <path d="M8 2L14 5.5V10.5L8 14L2 10.5V5.5L8 2Z" stroke="currentColor" stroke-width="1.25"
                                stroke-linejoin="round" fill="none" class="text-primary" />
                            <circle cx="8" cy="8" r="2" fill="currentColor" class="text-primary" />
                        </svg>
                    </div>
                    <h1 class="text-lg font-semibold tracking-tight text-foreground">
                        Arkeep
                    </h1>
                    <p class="mt-1 text-sm text-muted-foreground">
                        Sign in to your workspace
                    </p>
                </div>

                <!-- Server error -->
                <Transition enter-active-class="transition-all duration-200" enter-from-class="opacity-0 -translate-y-1"
                    leave-active-class="transition-all duration-150" leave-to-class="opacity-0 -translate-y-1">
                    <Alert v-if="serverError" variant="destructive" class="mb-5">
                        <AlertCircle class="size-4" />
                        <AlertDescription>{{ serverError }}</AlertDescription>
                    </Alert>
                </Transition>

                <!-- Form -->
                <form class="space-y-4" novalidate @submit="onSubmit">

                    <!-- Email -->
                    <div class="space-y-1.5">
                        <Label for="email" class="text-xs font-medium uppercase tracking-wide text-muted-foreground">
                            Email
                        </Label>
                        <Field v-slot="{ field, errors: fieldErrors }" name="email">
                            <Input id="email" v-bind="field" type="email" placeholder="you@company.com"
                                autocomplete="email" autofocus spellcheck="false"
                                :class="fieldErrors.length ? 'border-destructive focus-visible:ring-destructive/30' : ''" />
                        </Field>
                        <ErrorMessage name="email" class="text-xs text-destructive" as="p" />
                    </div>

                    <!-- Password -->
                    <div class="space-y-1.5">
                        <Label for="password" class="text-xs font-medium uppercase tracking-wide text-muted-foreground">
                            Password
                        </Label>
                        <Field v-slot="{ field, errors: fieldErrors }" name="password">
                            <div class="relative">
                                <Input id="password" v-bind="field" :type="showPassword ? 'text' : 'password'"
                                    placeholder="••••••••" autocomplete="current-password" class="pr-10"
                                    :class="fieldErrors.length ? 'border-destructive focus-visible:ring-destructive/30' : ''" />
                                <button type="button"
                                    class="absolute right-3 top-1/2 -translate-y-1/2 text-muted-foreground hover:text-foreground transition-colors"
                                    :aria-label="showPassword ? 'Hide password' : 'Show password'"
                                    @click="showPassword = !showPassword">
                                    <EyeOff v-if="showPassword" class="size-4" />
                                    <Eye v-else class="size-4" />
                                </button>
                            </div>
                        </Field>
                        <ErrorMessage name="password" class="text-xs text-destructive" as="p" />
                    </div>

                    <Button type="submit" class="w-full mt-2" :disabled="isSubmitting">
                        <Loader2 v-if="isSubmitting" class="size-4 animate-spin" />
                        {{ isSubmitting ? 'Signing in…' : 'Sign in' }}
                    </Button>

                </form>

                <!-- Divider -->
                <div class="relative my-6 flex items-center gap-3">
                    <Separator class="flex-1" />
                    <span class="text-xs text-muted-foreground uppercase tracking-widest shrink-0">or</span>
                    <Separator class="flex-1" />
                </div>

                <!-- OIDC -->
                <Button type="button" variant="outline" class="w-full" :disabled="oidcLoading" @click="loginWithOIDC">
                    <Loader2 v-if="oidcLoading" class="size-4 animate-spin" />
                    Continue with SSO
                </Button>

            </div>

            <!-- Footer -->
            <p class="mt-6 text-center text-xs text-muted-foreground/50">
                Arkeep — open source backup management
            </p>

        </div>
    </div>
</template>