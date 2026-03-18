<script setup lang="ts">
import { ref, computed, watch } from 'vue'
import { useRouter } from 'vue-router'
import { useForm, useField } from 'vee-validate'
import { toTypedSchema } from '@vee-validate/zod'
import { z } from 'zod'
import {
    Sheet,
    SheetContent,
    SheetDescription,
    SheetFooter,
    SheetHeader,
    SheetTitle,
} from '@/components/ui/sheet'
import {
    Select,
    SelectContent,
    SelectItem,
    SelectTrigger,
    SelectValue,
} from '@/components/ui/select'
import { Alert, AlertDescription } from '@/components/ui/alert'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import {
    Field,
    FieldError,
    FieldGroup,
    FieldLabel,
} from '@/components/ui/field'
import { AlertCircle, Loader2 } from 'lucide-vue-next'
import { api } from '@/services/api'
import type { Agent, ApiResponse, RestoreResponse, Snapshot } from '@/types'

// ---------------------------------------------------------------------------
// Props & emits
// ---------------------------------------------------------------------------

const props = defineProps<{
    open: boolean
    snapshot: Snapshot | null
}>()

const emit = defineEmits<{
    'update:open': [value: boolean]
}>()

// ---------------------------------------------------------------------------
// Schema
// ---------------------------------------------------------------------------

const schema = toTypedSchema(
    z.object({
        agent_id: z.string().min(1, 'Please select a target agent.'),
        restore_mode: z.enum(['custom', 'inplace']),
        target_path: z.string().optional(),
    }).superRefine((data, ctx) => {
        if (data.restore_mode === 'custom' && (!data.target_path || !data.target_path.trim())) {
            ctx.addIssue({
                code: 'custom',
                path: ['target_path'],
                message: 'Target path is required.',
            })
        }
    })
)

const { handleSubmit, resetForm, setValues, isSubmitting } = useForm({
    validationSchema: schema,
    initialValues: {
        agent_id: '',
        restore_mode: 'custom' as const,
        target_path: '/tmp/arkeep-restore',
    },
})

const { value: agentId, errorMessage: agentError } = useField<string>('agent_id')
const { value: restoreMode } = useField<'custom' | 'inplace'>('restore_mode')
const { value: targetPath, errorMessage: targetPathError } = useField<string>('target_path')

// resolvedTargetPath is what gets sent to the API.
// In-place restore uses "/" so restic writes files back to their original paths.
const resolvedTargetPath = computed(() =>
    restoreMode.value === 'inplace' ? '/' : targetPath.value?.trim() ?? ''
)

// selectedAgent is the full Agent object for the currently selected agent_id.
const selectedAgent = computed(() =>
    agents.value.find((a) => a.id === agentId.value) ?? null
)

// In-place restore is not supported on Windows — restic reconstructs paths
// from root which produces invalid paths (e.g. \C\Users\...) on Windows.
const inplaceDisabled = computed(() =>
    selectedAgent.value?.os === 'windows'
)

// defaultTargetPath returns the OS-appropriate default restore path.
// On Windows, C:\Users\Public is writable by all users without elevation.
// On Linux/macOS, /tmp/arkeep-restore is the standard temp location.
const defaultTargetPath = computed(() =>
    selectedAgent.value?.os === 'windows'
        ? 'C:\\Users\\Public\\arkeep-restore'
        : '/tmp/arkeep-restore'
)

// ---------------------------------------------------------------------------
// State
// ---------------------------------------------------------------------------

const router = useRouter()
const agents = ref<Agent[]>([])
const submitError = ref<string | null>(null)

// ---------------------------------------------------------------------------
// Watchers
// ---------------------------------------------------------------------------

// Reset form and fetch agents when the sheet opens.
watch(
    () => props.open,
    async (isOpen) => {
        if (!isOpen) return
        resetForm()
        setValues({
            agent_id: '',
            restore_mode: 'custom',
            target_path: '/tmp/arkeep-restore',
        })
        submitError.value = null
        await fetchAgents()
    },
)

// When the selected agent changes, update the default target path if the user
// has not yet customised it, and reset in-place mode on Windows agents.
watch(selectedAgent, (agent) => {
    // Reset to custom mode if a Windows agent is selected while in-place is active.
    if (agent?.os === 'windows' && restoreMode.value === 'inplace') {
        restoreMode.value = 'custom'
    }

    // Update target path only when it still matches a known default so we
    // don't overwrite a path the user has already typed.
    const current = targetPath.value?.trim()
    const isDefault =
        current === '/tmp/arkeep-restore' ||
        current === 'C:\\Users\\Public\\arkeep-restore' ||
        !current

    if (isDefault) {
        targetPath.value = agent?.os === 'windows'
            ? 'C:\\Users\\Public\\arkeep-restore'
            : '/tmp/arkeep-restore'
    }
})

// ---------------------------------------------------------------------------
// Data fetching
// ---------------------------------------------------------------------------

async function fetchAgents() {
    try {
        const res = await api<ApiResponse<{ items: Agent[]; total: number }>>('/api/v1/agents?limit=100')
        // Only show online agents — offline agents cannot receive a restore job.
        agents.value = res.data.items.filter((a) => a.status === 'online')
    } catch {
        agents.value = []
    }
}

// ---------------------------------------------------------------------------
// Handlers
// ---------------------------------------------------------------------------

const onSubmit = handleSubmit(async () => {
    if (!props.snapshot) return
    submitError.value = null

    try {
        const res = await api<ApiResponse<RestoreResponse>>(
            `/api/v1/snapshots/${props.snapshot.id}/restore`,
            {
                method: 'POST',
                body: JSON.stringify({
                    agent_id: agentId.value,
                    target_path: resolvedTargetPath.value,
                }),
            },
        )
        emit('update:open', false)
        router.push({ name: 'job-detail', params: { id: res.data.job_id } })
    } catch (e: any) {
        submitError.value = e?.data?.error?.message ?? e?.message ?? 'Failed to start restore.'
    }
})

function onOpenChange(value: boolean) {
    if (!value) {
        resetForm()
        submitError.value = null
    }
    emit('update:open', value)
}
</script>

<template>
    <Sheet :open="props.open" @update:open="onOpenChange">
        <SheetContent class="sm:max-w-md">
            <SheetHeader>
                <SheetTitle>Restore snapshot</SheetTitle>
                <SheetDescription>
                    Restore
                    <span class="font-mono">{{ snapshot?.restic_snapshot_id?.slice(0, 8) }}</span>
                    to a target agent.
                </SheetDescription>
            </SheetHeader>

            <form class="py-6 px-4" novalidate @submit.prevent="onSubmit">
                <FieldGroup>

                    <Transition enter-active-class="transition-all duration-200"
                        enter-from-class="-translate-y-1 opacity-0" leave-active-class="transition-all duration-150"
                        leave-to-class="-translate-y-1 opacity-0">
                        <Alert v-if="submitError" variant="destructive">
                            <AlertCircle class="size-4" />
                            <AlertDescription>{{ submitError }}</AlertDescription>
                        </Alert>
                    </Transition>

                    <!-- Agent selector -->
                    <Field>
                        <FieldLabel for="agent">Target agent</FieldLabel>
                        <Select :model-value="agentId ?? ''" :disabled="isSubmitting"
                            @update:model-value="agentId = $event as string">
                            <SelectTrigger id="agent"
                                :class="agentError ? 'border-destructive focus-visible:ring-destructive/30' : ''">
                                <SelectValue placeholder="Select an agent…" />
                            </SelectTrigger>
                            <SelectContent>
                                <SelectItem v-for="agent in agents" :key="agent.id" :value="agent.id">
                                    {{ agent.name }}
                                </SelectItem>
                                <div v-if="agents.length === 0"
                                    class="px-3 py-4 text-sm text-muted-foreground text-center">
                                    No online agents available.
                                </div>
                            </SelectContent>
                        </Select>
                        <FieldError v-if="agentError">{{ agentError }}</FieldError>
                    </Field>

                    <!-- Restore mode selector -->
                    <Field>
                        <FieldLabel>Restore mode</FieldLabel>
                        <Select :model-value="restoreMode" :disabled="isSubmitting"
                            @update:model-value="restoreMode = $event as 'custom' | 'inplace'">
                            <SelectTrigger>
                                <SelectValue />
                            </SelectTrigger>
                            <SelectContent>
                                <SelectItem value="custom">Custom path</SelectItem>
                                <SelectItem value="inplace" :disabled="inplaceDisabled">
                                    Original location
                                    <span v-if="inplaceDisabled" class="ml-1 text-xs text-muted-foreground">
                                        (not supported on Windows)
                                    </span>
                                </SelectItem>
                            </SelectContent>
                        </Select>
                    </Field>

                    <!-- Custom path input — shown only in custom mode -->
                    <Field v-if="restoreMode === 'custom'">
                        <FieldLabel for="target-path">Target path</FieldLabel>
                        <Input id="target-path" v-model="targetPath" :placeholder="defaultTargetPath" autocomplete="off"
                            :disabled="isSubmitting"
                            :class="targetPathError ? 'border-destructive focus-visible:ring-destructive/30' : ''" />
                        <FieldError v-if="targetPathError">{{ targetPathError }}</FieldError>
                        <p v-else class="text-xs text-muted-foreground mt-1">
                            Absolute path on the target agent where files will be written.
                            The original directory structure will be recreated inside this folder.
                        </p>
                    </Field>

                    <!-- In-place warning -->
                    <Alert v-if="restoreMode === 'inplace'" variant="destructive">
                        <AlertCircle class="size-4" />
                        <AlertDescription>
                            Files will be restored to their original paths and will overwrite
                            existing data. This action cannot be undone.
                        </AlertDescription>
                    </Alert>

                    <SheetFooter class="mt-2 px-0">
                        <Button type="button" variant="outline" :disabled="isSubmitting" @click="onOpenChange(false)">
                            Cancel
                        </Button>
                        <Button type="submit" :disabled="isSubmitting || agents.length === 0">
                            <Loader2 v-if="isSubmitting" class="size-4 animate-spin" />
                            {{ isSubmitting ? 'Starting…' : 'Start restore' }}
                        </Button>
                    </SheetFooter>

                </FieldGroup>
            </form>
        </SheetContent>
    </Sheet>
</template>