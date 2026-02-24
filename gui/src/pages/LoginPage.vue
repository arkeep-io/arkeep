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
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { Alert, AlertDescription } from '@/components/ui/alert'
import { Separator } from '@/components/ui/separator'
import { Eye, EyeOff, AlertCircle, Loader2, Shield } from 'lucide-vue-next'

// ─── Validation ───────────────────────────────────────────────────────────────

const { handleSubmit, isSubmitting } = useForm({
    validationSchema: toTypedSchema(
        z.object({
            email: z.email({ error: 'Enter a valid email address' }),
            password: z
                .string({ error: 'Password is required' })
                .min(1, 'Password is required'),
        }),
    ),
})

// ─── State ────────────────────────────────────────────────────────────────────

const auth = useAuthStore()
const router = useRouter()
const route = useRoute()

const serverError = ref<string | null>(null)
const showPassword = ref(false)

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
    // Full-page redirect — server handles OAuth flow and returns to
    // /?token=<access_token> which OIDCCallbackPage processes.
    window.location.href = '/api/v1/auth/oidc/login'
}
</script>

<template>
    <div class="min-h-dvh bg-zinc-950 flex items-center justify-center p-4">
        <!-- Subtle grid background -->
        <div class="pointer-events-none fixed inset-0 bg-[linear-gradient(rgba(255,255,255,0.015)_1px,transparent_1px),linear-gradient(90deg,rgba(255,255,255,0.015)_1px,transparent_1px)] bg-size-[48px_48px] mask-[radial-gradient(ellipse_80%_80%_at_50%_50%,black_30%,transparent_100%)]"
            aria-hidden="true" />

        <div class="relative w-full max-w-sm">
            <div
                class="rounded-xl border border-zinc-800 bg-zinc-900/80 backdrop-blur-sm shadow-2xl shadow-black/50 p-8">

                <!-- Header -->
                <div class="mb-8 text-center">
                    <div
                        class="inline-flex items-center justify-center size-10 rounded-lg bg-blue-500/10 border border-blue-500/20 mb-4">
                        <Shield class="size-5 text-blue-400" />
                    </div>
                    <h1 class="text-lg font-semibold tracking-tight text-zinc-100">
                        Arkeep
                    </h1>
                    <p class="mt-1 text-sm text-zinc-500">
                        Sign in to your workspace
                    </p>
                </div>

                <!-- Server error -->
                <Transition enter-active-class="transition-all duration-200" enter-from-class="opacity-0 -translate-y-1"
                    leave-active-class="transition-all duration-150" leave-to-class="opacity-0 -translate-y-1">
                    <Alert v-if="serverError" variant="destructive"
                        class="mb-5 border-red-500/30 bg-red-500/10 text-red-400">
                        <AlertCircle class="size-4" />
                        <AlertDescription>{{ serverError }}</AlertDescription>
                    </Alert>
                </Transition>

                <!-- Form -->
                <form class="space-y-4" novalidate @submit="onSubmit">

                    <!-- Email -->
                    <div class="space-y-1.5">
                        <Label for="email" class="text-zinc-400 text-xs uppercase tracking-wide">
                            Email
                        </Label>
                        <Field v-slot="{ field, errors: fieldErrors }" name="email">
                            <Input id="email" v-bind="field" type="email" placeholder="you@company.com"
                                autocomplete="email" autofocus spellcheck="false"
                                class="bg-zinc-800/60 border-zinc-700 text-zinc-100 placeholder:text-zinc-600 focus-visible:ring-blue-500/40 focus-visible:border-blue-500/60"
                                :class="fieldErrors.length ? 'border-red-500/60' : ''" />
                        </Field>
                        <ErrorMessage name="email" class="text-xs text-red-400" as="p" />
                    </div>

                    <!-- Password -->
                    <div class="space-y-1.5">
                        <Label for="password" class="text-zinc-400 text-xs uppercase tracking-wide">
                            Password
                        </Label>
                        <Field v-slot="{ field, errors: fieldErrors }" name="password">
                            <div class="relative">
                                <Input id="password" v-bind="field" :type="showPassword ? 'text' : 'password'"
                                    placeholder="••••••••" autocomplete="current-password"
                                    class="bg-zinc-800/60 border-zinc-700 text-zinc-100 placeholder:text-zinc-600 focus-visible:ring-blue-500/40 focus-visible:border-blue-500/60 pr-10"
                                    :class="fieldErrors.length ? 'border-red-500/60' : ''" />
                                <button type="button"
                                    class="absolute right-3 top-1/2 -translate-y-1/2 text-zinc-500 hover:text-zinc-300 transition-colors"
                                    :aria-label="showPassword ? 'Hide password' : 'Show password'"
                                    @click="showPassword = !showPassword">
                                    <EyeOff v-if="showPassword" class="size-4" />
                                    <Eye v-else class="size-4" />
                                </button>
                            </div>
                        </Field>
                        <ErrorMessage name="password" class="text-xs text-red-400" as="p" />
                    </div>

                    <Button type="submit" class="w-full bg-blue-600 hover:bg-blue-500 text-white border-0 mt-2"
                        :disabled="isSubmitting">
                        <Loader2 v-if="isSubmitting" class="size-4 animate-spin" />
                        {{ isSubmitting ? 'Signing in…' : 'Sign in' }}
                    </Button>

                </form>

                <!-- Divider -->
                <div class="relative my-6">
                    <Separator class="bg-zinc-800" />
                    <span
                        class="absolute left-1/2 top-1/2 -translate-x-1/2 -translate-y-1/2 bg-zinc-900 px-2 text-xs text-zinc-600 uppercase tracking-widest">
                        or
                    </span>
                </div>

                <!-- OIDC -->
                <Button type="button" variant="outline"
                    class="w-full border-zinc-700 bg-zinc-800/40 text-zinc-300 hover:bg-zinc-800 hover:text-zinc-100 hover:border-zinc-600"
                    @click="loginWithOIDC">
                    Continue with SSO
                </Button>

            </div>

            <p class="mt-6 text-center text-xs text-zinc-700">
                Arkeep — open source backup management
            </p>
        </div>
    </div>
</template>