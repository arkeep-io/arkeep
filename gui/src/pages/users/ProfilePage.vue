<script setup lang="ts">
import { ref, computed } from 'vue'
import { useForm, useField } from 'vee-validate'
import { toTypedSchema } from '@vee-validate/zod'
import { z } from 'zod'
import { passwordSchema } from '@/lib/validation'
import { Alert, AlertDescription } from '@/components/ui/alert'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Field, FieldError, FieldGroup, FieldLabel } from '@/components/ui/field'
import { Avatar, AvatarFallback } from '@/components/ui/avatar'
import { Badge } from '@/components/ui/badge'
import { AlertCircle, Loader2 } from '@lucide/vue'
import { api, apiErrorMessage } from '@/services/api'
import { useAuthStore } from '@/stores/auth'
import TwoFactorSection from '@/components/users/TwoFactorSection.vue'
import type { ApiResponse, User } from '@/types'

// ---------------------------------------------------------------------------
// Auth store — source of truth for current user
// ---------------------------------------------------------------------------

const auth = useAuthStore()

const userInitials = computed(() =>
    (auth.user?.display_name ?? '')
        .split(' ')
        .map((w) => w[0])
        .slice(0, 2)
        .join('')
        .toUpperCase() || '?',
)

// OIDC users cannot change their password — it is managed by the IdP.
const isOIDC = computed(() => auth.user?.is_oidc ?? false)

// ---------------------------------------------------------------------------
// Profile form
// ---------------------------------------------------------------------------

// The page holds two forms: each field names the form it belongs to, since
// useField would otherwise attach to the last form created in the component.
const profileForm = useForm({
    validationSchema: toTypedSchema(z.object({
        display_name: z.string().min(1, 'Display name is required'),
    })),
    initialValues: { display_name: auth.user?.display_name ?? '' },
})

const { value: fieldDisplayName, errorMessage: displayNameError } =
    useField<string>('display_name', undefined, { form: profileForm })

const { isSubmitting: profileSubmitting } = profileForm
const profileError = ref<string | null>(null)
const profileSuccess = ref(false)

const submitProfile = profileForm.handleSubmit(async (values) => {
    profileError.value = null
    profileSuccess.value = false

    try {
        const res = await api<ApiResponse<User>>('/api/v1/users/me', {
            method: 'PATCH',
            body: { display_name: values.display_name },
        })
        // Update the store so the sidebar and NavUser reflect the change immediately.
        if (auth.user) auth.user.display_name = res.data.display_name
        profileSuccess.value = true
        setTimeout(() => { profileSuccess.value = false }, 3000)
    } catch (e: any) {
        profileError.value = apiErrorMessage(e, 'Failed to update profile.')
    }
})

// ---------------------------------------------------------------------------
// Password form (local accounts only)
// ---------------------------------------------------------------------------

const passwordForm = useForm({
    validationSchema: toTypedSchema(z.object({
        current_password: z.string().min(1, 'Current password is required'),
        new_password: passwordSchema,
        confirm_password: z.string(),
    }).refine((d) => d.new_password === d.confirm_password, {
        message: 'Passwords do not match',
        path: ['confirm_password'],
    })),
    initialValues: { current_password: '', new_password: '', confirm_password: '' },
})

const { value: fieldCurrentPassword, errorMessage: currentPasswordError } =
    useField<string>('current_password', undefined, { form: passwordForm })
const { value: fieldNewPassword, errorMessage: newPasswordError } =
    useField<string>('new_password', undefined, { form: passwordForm })
const { value: fieldConfirmPassword, errorMessage: confirmPasswordError } =
    useField<string>('confirm_password', undefined, { form: passwordForm })

const { isSubmitting: passwordSubmitting } = passwordForm
const passwordError = ref<string | null>(null)
const passwordSuccess = ref(false)

const submitPassword = passwordForm.handleSubmit(async (values) => {
    passwordError.value = null
    passwordSuccess.value = false

    try {
        // The server checks the current password and signs out every other
        // session once the new one is set.
        await api('/api/v1/users/me', {
            method: 'PATCH',
            body: { password: values.new_password, current_password: values.current_password },
        })
        passwordForm.resetForm()
        passwordSuccess.value = true
        setTimeout(() => { passwordSuccess.value = false }, 3000)
    } catch (e: any) {
        passwordError.value = apiErrorMessage(e, 'Failed to update password.')
    }
})
</script>

<template>
    <div class="flex flex-col p-6">

        <!-- Avatar identity row -->
        <div class="flex items-center gap-5 pb-8 border-b">
            <Avatar class="w-16 h-16 rounded-xl shrink-0">
                <AvatarFallback class="rounded-xl text-xl font-semibold">
                    {{ userInitials }}
                </AvatarFallback>
            </Avatar>
            <div class="min-w-0">
                <h1 class="text-2xl font-semibold tracking-tight truncate">{{ auth.user?.display_name }}</h1>
                <p class="text-sm text-muted-foreground truncate">{{ auth.user?.email }}</p>
                <div class="flex items-center gap-2 mt-1">
                    <Badge variant="outline" class="capitalize text-xs">{{ auth.user?.role }}</Badge>
                    <Badge v-if="isOIDC" variant="secondary" class="text-xs">SSO</Badge>
                </div>
            </div>
        </div>

        <!-- ── Display Name section ──────────────────────────────────────────── -->
        <div class="grid grid-cols-[280px_1fr] gap-12 py-8 border-b">
            <div>
                <h2 class="text-sm font-semibold">Display Name</h2>
                <p class="mt-1 text-sm text-muted-foreground">
                    This name is shown in the sidebar, notifications, and anywhere your account is referenced.
                </p>
            </div>

            <form novalidate @submit="submitProfile">
                <FieldGroup class="flex flex-col gap-4">

                    <Transition enter-active-class="transition-all duration-200"
                        enter-from-class="-translate-y-1 opacity-0" leave-active-class="transition-all duration-150"
                        leave-to-class="-translate-y-1 opacity-0">
                        <Alert v-if="profileError" variant="destructive">
                            <AlertCircle class="size-4" />
                            <AlertDescription>{{ profileError }}</AlertDescription>
                        </Alert>
                    </Transition>

                    <Transition enter-active-class="transition-all duration-200"
                        enter-from-class="-translate-y-1 opacity-0" leave-active-class="transition-all duration-150"
                        leave-to-class="-translate-y-1 opacity-0">
                        <Alert v-if="profileSuccess"
                            class="border-emerald-500/30 bg-emerald-500/5 text-emerald-600 dark:text-emerald-400">
                            <AlertDescription>Display name updated successfully.</AlertDescription>
                        </Alert>
                    </Transition>

                    <Field>
                        <FieldLabel for="display-name">Display Name</FieldLabel>
                        <Input id="display-name" v-model="fieldDisplayName" placeholder="Jane Doe" autocomplete="off"
                            :class="displayNameError ? 'border-destructive focus-visible:ring-destructive/30' : ''" />
                        <FieldError v-if="displayNameError">{{ displayNameError }}</FieldError>
                    </Field>

                    <div class="flex justify-end">
                        <Button type="submit" :disabled="profileSubmitting">
                            <Loader2 v-if="profileSubmitting" class="size-4 animate-spin" />
                            {{ profileSubmitting ? 'Saving…' : 'Save Name' }}
                        </Button>
                    </div>

                </FieldGroup>
            </form>
        </div>

        <!-- ── Password section ─────────────────────────────────────────────── -->
        <div class="grid grid-cols-[280px_1fr] gap-12 py-8 border-b">
            <div>
                <h2 class="text-sm font-semibold">Password</h2>
                <p class="mt-1 text-sm text-muted-foreground">
                    <template v-if="isOIDC">
                        Your account is managed by an external identity provider.
                        Password changes must be made there.
                    </template>
                    <template v-else>
                        Choose a strong password of at least 8 characters. Your other sessions will be signed out.
                    </template>
                </p>
            </div>

            <div v-if="isOIDC"
                class="flex items-center justify-center rounded-lg border border-dashed p-8 text-sm text-muted-foreground">
                Password is managed by your identity provider.
            </div>

            <form v-else novalidate @submit="submitPassword">
                <FieldGroup class="flex flex-col gap-4">

                    <Transition enter-active-class="transition-all duration-200"
                        enter-from-class="-translate-y-1 opacity-0" leave-active-class="transition-all duration-150"
                        leave-to-class="-translate-y-1 opacity-0">
                        <Alert v-if="passwordError" variant="destructive">
                            <AlertCircle class="size-4" />
                            <AlertDescription>{{ passwordError }}</AlertDescription>
                        </Alert>
                    </Transition>

                    <Transition enter-active-class="transition-all duration-200"
                        enter-from-class="-translate-y-1 opacity-0" leave-active-class="transition-all duration-150"
                        leave-to-class="-translate-y-1 opacity-0">
                        <Alert v-if="passwordSuccess"
                            class="border-emerald-500/30 bg-emerald-500/5 text-emerald-600 dark:text-emerald-400">
                            <AlertDescription>Password updated successfully.</AlertDescription>
                        </Alert>
                    </Transition>

                    <Field>
                        <FieldLabel for="current-password">Current Password</FieldLabel>
                        <Input id="current-password" v-model="fieldCurrentPassword" type="password"
                            autocomplete="current-password"
                            :class="currentPasswordError ? 'border-destructive focus-visible:ring-destructive/30' : ''" />
                        <FieldError v-if="currentPasswordError">{{ currentPasswordError }}</FieldError>
                    </Field>

                    <div class="grid grid-cols-2 gap-3">
                        <Field>
                            <FieldLabel for="new-password">New Password</FieldLabel>
                            <Input id="new-password" v-model="fieldNewPassword" type="password"
                                autocomplete="new-password"
                                :class="newPasswordError ? 'border-destructive focus-visible:ring-destructive/30' : ''" />
                            <FieldError v-if="newPasswordError">{{ newPasswordError }}</FieldError>
                        </Field>
                        <Field>
                            <FieldLabel for="confirm-password">Confirm Password</FieldLabel>
                            <Input id="confirm-password" v-model="fieldConfirmPassword" type="password"
                                autocomplete="new-password"
                                :class="confirmPasswordError ? 'border-destructive focus-visible:ring-destructive/30' : ''" />
                            <FieldError v-if="confirmPasswordError">{{ confirmPasswordError }}</FieldError>
                        </Field>
                    </div>

                    <div class="flex justify-end">
                        <Button type="submit" :disabled="passwordSubmitting">
                            <Loader2 v-if="passwordSubmitting" class="size-4 animate-spin" />
                            {{ passwordSubmitting ? 'Saving…' : 'Update Password' }}
                        </Button>
                    </div>

                </FieldGroup>
            </form>
        </div>

        <TwoFactorSection />

    </div>
</template>
