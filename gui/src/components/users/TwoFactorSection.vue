<script setup lang="ts">
import { computed, onMounted, ref } from 'vue'
import QRCode from 'qrcode'
import { Alert, AlertDescription } from '@/components/ui/alert'
import { Button } from '@/components/ui/button'
import { Badge } from '@/components/ui/badge'
import { Field, FieldGroup, FieldLabel } from '@/components/ui/field'
import { Input } from '@/components/ui/input'
import { PinInput, PinInputGroup, PinInputSlot } from '@/components/ui/pin-input'
import {
    Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle,
} from '@/components/ui/dialog'
import { AlertCircle, Loader2, ShieldCheck } from '@lucide/vue'
import { api } from '@/services/api'
import { useAuthStore } from '@/stores/auth'
import type {
    ApiResponse, TwoFactorRecoveryCodesResponse, TwoFactorSetupResponse, TwoFactorStatus,
} from '@/types'

const auth = useAuthStore()

const isOIDC = computed(() => auth.user?.is_oidc ?? false)

// ---------------------------------------------------------------------------
// Status
// ---------------------------------------------------------------------------

const status = ref<TwoFactorStatus | null>(null)
const statusLoading = ref(true)

async function fetchStatus(): Promise<void> {
    statusLoading.value = true
    try {
        const res = await api<ApiResponse<TwoFactorStatus>>('/api/v1/auth/2fa/status')
        status.value = res.data
    } finally {
        statusLoading.value = false
    }
}

onMounted(fetchStatus)

// ---------------------------------------------------------------------------
// Enrollment (setup → verify)
// ---------------------------------------------------------------------------

const enrolling = ref(false)
const setupData = ref<TwoFactorSetupResponse | null>(null)
const qrDataUrl = ref('')
const verifyCode = ref<string[]>([])
const enrollError = ref<string | null>(null)
const enrollSubmitting = ref(false)

async function startEnrollment(): Promise<void> {
    enrollError.value = null
    enrollSubmitting.value = true
    try {
        const res = await api<ApiResponse<TwoFactorSetupResponse>>('/api/v1/auth/2fa/setup', {
            method: 'POST',
        })
        setupData.value = res.data
        qrDataUrl.value = await QRCode.toDataURL(res.data.otpauth_url, { width: 200, margin: 1 })
        verifyCode.value = []
        enrolling.value = true
    } catch (e: any) {
        enrollError.value = e?.data?.error?.message ?? e?.message ?? 'Failed to start enrollment.'
    } finally {
        enrollSubmitting.value = false
    }
}

function cancelEnrollment(): void {
    enrolling.value = false
    setupData.value = null
    qrDataUrl.value = ''
    verifyCode.value = []
    enrollError.value = null
}

async function submitVerify(): Promise<void> {
    const code = verifyCode.value.join('')
    if (code.length !== 6) return

    enrollError.value = null
    enrollSubmitting.value = true
    try {
        const res = await api<ApiResponse<TwoFactorRecoveryCodesResponse>>('/api/v1/auth/2fa/verify', {
            method: 'POST',
            body: { code },
        })
        if (auth.user) auth.user.two_factor_enabled = true
        enrolling.value = false
        setupData.value = null
        qrDataUrl.value = ''
        recoveryCodes.value = res.data.recovery_codes
        recoveryCodesDialogOpen.value = true
        await fetchStatus()
    } catch (e: any) {
        enrollError.value = e?.data?.error?.message ?? e?.message ?? 'Invalid code.'
    } finally {
        enrollSubmitting.value = false
    }
}

// ---------------------------------------------------------------------------
// Recovery codes dialog — shown once after verify or regenerate
// ---------------------------------------------------------------------------

const recoveryCodesDialogOpen = ref(false)
const recoveryCodes = ref<string[]>([])
const codesCopied = ref(false)

async function copyRecoveryCodes(): Promise<void> {
    await navigator.clipboard.writeText(recoveryCodes.value.join('\n'))
    codesCopied.value = true
    setTimeout(() => { codesCopied.value = false }, 2000)
}

function downloadRecoveryCodes(): void {
    const blob = new Blob([recoveryCodes.value.join('\n') + '\n'], { type: 'text/plain' })
    const url = URL.createObjectURL(blob)
    const a = document.createElement('a')
    a.href = url
    a.download = 'arkeep-recovery-codes.txt'
    a.click()
    URL.revokeObjectURL(url)
}

// ---------------------------------------------------------------------------
// Disable
// ---------------------------------------------------------------------------

const disableDialogOpen = ref(false)
const disablePassword = ref('')
const disableError = ref<string | null>(null)
const disableLoading = ref(false)

function openDisableDialog(): void {
    disablePassword.value = ''
    disableError.value = null
    disableDialogOpen.value = true
}

async function confirmDisable(): Promise<void> {
    disableError.value = null
    disableLoading.value = true
    try {
        await api('/api/v1/auth/2fa/disable', {
            method: 'POST',
            body: { password: disablePassword.value },
        })
        if (auth.user) auth.user.two_factor_enabled = false
        disableDialogOpen.value = false
        await fetchStatus()
    } catch (e: any) {
        disableError.value = e?.data?.error?.message ?? e?.message ?? 'Incorrect password.'
    } finally {
        disableLoading.value = false
    }
}

// ---------------------------------------------------------------------------
// Regenerate recovery codes
// ---------------------------------------------------------------------------

const regenerateDialogOpen = ref(false)
const regeneratePassword = ref('')
const regenerateError = ref<string | null>(null)
const regenerateLoading = ref(false)

function openRegenerateDialog(): void {
    regeneratePassword.value = ''
    regenerateError.value = null
    regenerateDialogOpen.value = true
}

async function confirmRegenerate(): Promise<void> {
    regenerateError.value = null
    regenerateLoading.value = true
    try {
        const res = await api<ApiResponse<TwoFactorRecoveryCodesResponse>>('/api/v1/auth/2fa/recovery-codes/regenerate', {
            method: 'POST',
            body: { password: regeneratePassword.value },
        })
        regenerateDialogOpen.value = false
        recoveryCodes.value = res.data.recovery_codes
        recoveryCodesDialogOpen.value = true
        await fetchStatus()
    } catch (e: any) {
        regenerateError.value = e?.data?.error?.message ?? e?.message ?? 'Incorrect password.'
    } finally {
        regenerateLoading.value = false
    }
}
</script>

<template>
    <div class="grid grid-cols-[280px_1fr] gap-12 py-8 border-b">
        <div>
            <h2 class="text-sm font-semibold">Two-Factor Authentication</h2>
            <p class="mt-1 text-sm text-muted-foreground">
                Require a code from an authenticator app, in addition to your password, when signing in.
            </p>
        </div>

        <!-- OIDC accounts are managed by their identity provider -->
        <div v-if="isOIDC"
            class="flex items-center justify-center rounded-lg border border-dashed p-8 text-sm text-muted-foreground">
            Two-factor authentication is managed by your identity provider.
        </div>

        <div v-else-if="statusLoading || !status" class="flex justify-center py-4 text-muted-foreground">
            <Loader2 class="size-5 animate-spin" />
        </div>

        <!-- Enrollment in progress -->
        <div v-else-if="enrolling" class="flex flex-col gap-4">
            <Transition enter-active-class="transition-all duration-200" enter-from-class="-translate-y-1 opacity-0"
                leave-active-class="transition-all duration-150" leave-to-class="-translate-y-1 opacity-0">
                <Alert v-if="enrollError" variant="destructive">
                    <AlertCircle class="size-4" />
                    <AlertDescription>{{ enrollError }}</AlertDescription>
                </Alert>
            </Transition>

            <div class="flex flex-col items-center gap-3 sm:flex-row sm:items-start">
                <img v-if="qrDataUrl" :src="qrDataUrl" alt="QR code for authenticator app enrollment"
                    class="rounded-md border" width="200" height="200">
                <div class="text-sm text-muted-foreground">
                    <p>Scan this QR code with your authenticator app, or enter the setup key manually:</p>
                    <code class="mt-2 block break-all rounded bg-muted px-2 py-1 text-xs">{{ setupData?.secret }}</code>
                </div>
            </div>

            <FieldGroup class="flex flex-col gap-4">
                <Field class="items-center">
                    <FieldLabel>Enter the 6-digit code to confirm</FieldLabel>
                    <PinInput v-model="verifyCode" otp @complete="submitVerify">
                        <PinInputGroup>
                            <PinInputSlot v-for="i in 6" :key="i" :index="i - 1" />
                        </PinInputGroup>
                    </PinInput>
                </Field>
                <div class="flex justify-end gap-2">
                    <Button variant="outline" :disabled="enrollSubmitting" @click="cancelEnrollment">
                        Cancel
                    </Button>
                    <Button :disabled="enrollSubmitting || verifyCode.join('').length !== 6" @click="submitVerify">
                        <Loader2 v-if="enrollSubmitting" class="size-4 animate-spin" />
                        {{ enrollSubmitting ? 'Verifying…' : 'Verify and enable' }}
                    </Button>
                </div>
            </FieldGroup>
        </div>

        <!-- Enabled -->
        <div v-else-if="status.enabled" class="flex flex-col gap-4">
            <div class="flex items-center gap-2">
                <Badge variant="outline" class="gap-1">
                    <ShieldCheck class="size-3" />
                    Enabled
                </Badge>
                <span class="text-sm text-muted-foreground">
                    {{ status.recovery_codes_remaining }} recovery code{{ status.recovery_codes_remaining === 1 ? '' : 's' }} remaining
                </span>
            </div>
            <div class="flex gap-2">
                <Button variant="outline" @click="openRegenerateDialog">Regenerate recovery codes</Button>
                <Button variant="outline" @click="openDisableDialog">Disable</Button>
            </div>
        </div>

        <!-- Disabled -->
        <div v-else class="flex flex-col items-start gap-4">
            <Transition enter-active-class="transition-all duration-200" enter-from-class="-translate-y-1 opacity-0"
                leave-active-class="transition-all duration-150" leave-to-class="-translate-y-1 opacity-0">
                <Alert v-if="enrollError" variant="destructive">
                    <AlertCircle class="size-4" />
                    <AlertDescription>{{ enrollError }}</AlertDescription>
                </Alert>
            </Transition>
            <Button :disabled="enrollSubmitting" @click="startEnrollment">
                <Loader2 v-if="enrollSubmitting" class="size-4 animate-spin" />
                {{ enrollSubmitting ? 'Starting…' : 'Enable two-factor authentication' }}
            </Button>
        </div>
    </div>

    <!-- Recovery codes — shown once after verify or regenerate -->
    <Dialog :open="recoveryCodesDialogOpen" @update:open="recoveryCodesDialogOpen = $event">
        <DialogContent>
            <DialogHeader>
                <DialogTitle>Save your recovery codes</DialogTitle>
                <DialogDescription>
                    Each code can be used once to sign in if you lose access to your authenticator app. Store
                    them somewhere safe — they will not be shown again.
                </DialogDescription>
            </DialogHeader>
            <div class="grid grid-cols-2 gap-2 rounded-md border bg-muted/50 p-4 font-mono text-sm">
                <span v-for="code in recoveryCodes" :key="code">{{ code }}</span>
            </div>
            <DialogFooter class="gap-2 sm:justify-between">
                <div class="flex gap-2">
                    <Button variant="outline" @click="copyRecoveryCodes">{{ codesCopied ? 'Copied!' : 'Copy' }}</Button>
                    <Button variant="outline" @click="downloadRecoveryCodes">Download</Button>
                </div>
                <Button @click="recoveryCodesDialogOpen = false">Done</Button>
            </DialogFooter>
        </DialogContent>
    </Dialog>

    <!-- Disable confirmation -->
    <Dialog :open="disableDialogOpen" @update:open="disableDialogOpen = $event">
        <DialogContent>
            <DialogHeader>
                <DialogTitle>Disable two-factor authentication?</DialogTitle>
                <DialogDescription>
                    Your account will only require a password to sign in. Enter your password to confirm.
                </DialogDescription>
            </DialogHeader>
            <FieldGroup>
                <Transition enter-active-class="transition-all duration-200" enter-from-class="-translate-y-1 opacity-0"
                    leave-active-class="transition-all duration-150" leave-to-class="-translate-y-1 opacity-0">
                    <Alert v-if="disableError" variant="destructive">
                        <AlertCircle class="size-4" />
                        <AlertDescription>{{ disableError }}</AlertDescription>
                    </Alert>
                </Transition>
                <Field>
                    <FieldLabel for="disable-password">Password</FieldLabel>
                    <Input id="disable-password" v-model="disablePassword" type="password"
                        autocomplete="current-password" @keydown.enter.prevent="confirmDisable" />
                </Field>
            </FieldGroup>
            <DialogFooter>
                <Button variant="outline" :disabled="disableLoading" @click="disableDialogOpen = false">Cancel</Button>
                <Button variant="destructive" :disabled="disableLoading || !disablePassword" @click="confirmDisable">
                    <Loader2 v-if="disableLoading" class="size-4 animate-spin" />
                    {{ disableLoading ? 'Disabling…' : 'Disable' }}
                </Button>
            </DialogFooter>
        </DialogContent>
    </Dialog>

    <!-- Regenerate confirmation -->
    <Dialog :open="regenerateDialogOpen" @update:open="regenerateDialogOpen = $event">
        <DialogContent>
            <DialogHeader>
                <DialogTitle>Regenerate recovery codes?</DialogTitle>
                <DialogDescription>
                    Your existing recovery codes will stop working. Enter your password to confirm.
                </DialogDescription>
            </DialogHeader>
            <FieldGroup>
                <Transition enter-active-class="transition-all duration-200" enter-from-class="-translate-y-1 opacity-0"
                    leave-active-class="transition-all duration-150" leave-to-class="-translate-y-1 opacity-0">
                    <Alert v-if="regenerateError" variant="destructive">
                        <AlertCircle class="size-4" />
                        <AlertDescription>{{ regenerateError }}</AlertDescription>
                    </Alert>
                </Transition>
                <Field>
                    <FieldLabel for="regenerate-password">Password</FieldLabel>
                    <Input id="regenerate-password" v-model="regeneratePassword" type="password"
                        autocomplete="current-password" @keydown.enter.prevent="confirmRegenerate" />
                </Field>
            </FieldGroup>
            <DialogFooter>
                <Button variant="outline" :disabled="regenerateLoading" @click="regenerateDialogOpen = false">Cancel</Button>
                <Button :disabled="regenerateLoading || !regeneratePassword" @click="confirmRegenerate">
                    <Loader2 v-if="regenerateLoading" class="size-4 animate-spin" />
                    {{ regenerateLoading ? 'Regenerating…' : 'Regenerate' }}
                </Button>
            </DialogFooter>
        </DialogContent>
    </Dialog>
</template>
