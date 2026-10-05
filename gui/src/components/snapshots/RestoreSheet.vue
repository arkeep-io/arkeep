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
import { AsyncCombobox } from '@/components/ui/async-combobox'
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
import { AlertCircle, Loader2, TriangleAlert } from '@lucide/vue'
import { Separator } from '@/components/ui/separator'
import { api } from '@/services/api'
import type { Agent, ApiResponse, RestoreResponse, Snapshot, SnapshotFileEntry } from '@/types'
import SnapshotFileTree from '@/components/snapshots/SnapshotFileTree.vue'

// ---------------------------------------------------------------------------
// Props & emits
// ---------------------------------------------------------------------------

const props = defineProps<{
    open: boolean
    snapshot: Snapshot | null
    // presetPaths and presetAgent carry a selection made in the snapshot
    // browser: those paths are restored and the sheet does not browse again.
    presetPaths?: string[]
    presetAgent?: { id: string; name: string } | null
}>()

const emit = defineEmits<{
    'update:open': [value: boolean]
}>()

// ---------------------------------------------------------------------------
// Schema
// ---------------------------------------------------------------------------

// A snapshot whose destination was deleted: Arkeep erased the destination's
// credentials, so they must be entered again for this one restore (they are
// not saved), and only a full restore is possible.
const destinationDeleted = computed(() => !!props.snapshot?.destination_deleted)
const destinationType = computed(() => props.snapshot?.destination_type ?? '')
const repoPasswordRequired = computed(() => !!props.snapshot?.repo_password_required)

const schema = toTypedSchema(
    z.object({
        agent_id: z.string().min(1, 'Please select a target agent.'),
        restore_mode: z.enum(['custom', 'inplace']),
        target_path: z.string().optional(),
        access_key: z.string().optional(),
        secret_key: z.string().optional(),
        cred_user: z.string().optional(),
        cred_password: z.string().optional(),
        private_key: z.string().optional(),
        repo_password: z.string().optional(),
    }).superRefine((data, ctx) => {
        if (data.restore_mode === 'custom' && (!data.target_path || !data.target_path.trim())) {
            ctx.addIssue({
                code: 'custom',
                path: ['target_path'],
                message: 'Target path is required.',
            })
        }
        if (!destinationDeleted.value) return
        if (destinationType.value === 's3') {
            if (!data.access_key?.trim()) {
                ctx.addIssue({ code: 'custom', path: ['access_key'], message: 'Access key is required.' })
            }
            if (!data.secret_key?.trim()) {
                ctx.addIssue({ code: 'custom', path: ['secret_key'], message: 'Secret key is required.' })
            }
        }
        if (repoPasswordRequired.value && !data.repo_password) {
            ctx.addIssue({ code: 'custom', path: ['repo_password'], message: 'Repository password is required.' })
        }
    })
)

const { handleSubmit, resetForm, setValues, isSubmitting } = useForm({
    validationSchema: schema,
    initialValues: {
        agent_id: '',
        restore_mode: 'custom' as const,
        target_path: '/tmp/arkeep-restore',
        access_key: '',
        secret_key: '',
        cred_user: '',
        cred_password: '',
        private_key: '',
        repo_password: '',
    },
})

const { value: agentId, errorMessage: agentError } = useField<string>('agent_id')
const { value: restoreMode } = useField<'custom' | 'inplace'>('restore_mode')
const { value: targetPath, errorMessage: targetPathError } = useField<string>('target_path')
const { value: accessKey, errorMessage: accessKeyError } = useField<string>('access_key')
const { value: secretKey, errorMessage: secretKeyError } = useField<string>('secret_key')
const { value: credUser } = useField<string>('cred_user')
const { value: credPassword } = useField<string>('cred_password')
const { value: privateKey } = useField<string>('private_key')
const { value: repoPassword, errorMessage: repoPasswordError } = useField<string>('repo_password')

// suppliedCredentials builds the credentials JSON for a deleted destination,
// with the same keys the destination form stores for each type.
function suppliedCredentials(): Record<string, string> {
    switch (destinationType.value) {
        case 's3':
            return { access_key: accessKey.value ?? '', secret_key: secretKey.value ?? '' }
        case 'sftp':
            return { password: credPassword.value ?? '', private_key: privateKey.value ?? '' }
        case 'rest':
            return { user: credUser.value ?? '', password: credPassword.value ?? '' }
        default:
            return {}
    }
}

// resolvedTargetPath is what gets sent to the API.
// In-place restore uses "/" so restic writes files back to their original paths.
const resolvedTargetPath = computed(() =>
    restoreMode.value === 'inplace' ? '/' : targetPath.value?.trim() ?? ''
)

// selectedAgent holds the full Agent object for the currently selected agent_id.
// Updated via the AsyncCombobox update:item emit.
const selectedAgent = ref<Agent | null>(null)

function onAgentSelected(item: Record<string, unknown> | null) {
    selectedAgent.value = item as Agent | null
}

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
const submitError = ref<string | null>(null)
const isBrowsing = ref(false)
const browseError = ref<string | null>(null)
const browseEntries = ref<SnapshotFileEntry[]>([])
const selectedPaths = ref<string[]>([])

// ---------------------------------------------------------------------------
// Watchers
// ---------------------------------------------------------------------------

// Reset form when the sheet opens; pre-select the snapshot's original agent.
watch(
    () => props.open,
    (isOpen) => {
        if (!isOpen) {
            selectedAgent.value = null
            return
        }
        resetForm()
        setValues({
            agent_id: props.presetAgent?.id || props.snapshot?.agent_id || '',
            restore_mode: 'custom',
            target_path: '/tmp/arkeep-restore',
        })
        // resetForm restores the initial (empty) credential values, so nothing
        // typed for a previous snapshot survives into this one.
        submitError.value = null
        browseEntries.value = []
        selectedPaths.value = props.presetPaths ?? []
        browseError.value = null
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

// browseSnapshot loads the snapshot's top-level directories. Deeper levels are
// fetched lazily by the file tree via loadChildren as the user expands them.
async function browseSnapshot() {
    if (!props.snapshot) return
    isBrowsing.value = true
    browseError.value = null
    try {
        browseEntries.value = await loadChildren('')
        selectedPaths.value = []
    } catch (e: any) {
        browseError.value = e?.data?.error?.message ?? e?.message ?? 'Failed to browse snapshot.'
    } finally {
        isBrowsing.value = false
    }
}

// loadChildren fetches the direct children of one directory within the snapshot.
// An empty path lists the snapshot root.
async function loadChildren(path: string): Promise<SnapshotFileEntry[]> {
    if (!props.snapshot) return []
    const params = new URLSearchParams()
    if (path) params.set('path', path)
    // An imported snapshot has no policy, so the server cannot work out which
    // agent to run the listing on: the selected target agent is used instead.
    if (agentId.value) params.set('agent_id', agentId.value)
    const query = params.size > 0 ? `?${params}` : ''
    const res = await api<{ data: { entries: SnapshotFileEntry[] } }>(
        `/api/v1/snapshots/${props.snapshot.id}/browse${query}`,
    )
    return res.data.entries ?? []
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
                    ...(selectedPaths.value.length > 0 && { include_paths: selectedPaths.value }),
                    ...(destinationDeleted.value && { credentials: suppliedCredentials() }),
                    ...(destinationDeleted.value && repoPasswordRequired.value && { repo_password: repoPassword.value }),
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
        browseEntries.value = []
        selectedPaths.value = []
        browseError.value = null
    }
    emit('update:open', value)
}
</script>

<template>
    <Sheet :open="props.open" @update:open="onOpenChange">
        <SheetContent class="sm:max-w-md flex flex-col">
            <SheetHeader>
                <SheetTitle>Restore snapshot</SheetTitle>
                <SheetDescription>
                    Restore
                    <span class="font-mono">{{ snapshot?.restic_snapshot_id?.slice(0, 8) }}</span>
                    to a target agent.
                </SheetDescription>
            </SheetHeader>

            <form class="py-6 px-4 flex-1 overflow-y-auto" novalidate @submit.prevent="onSubmit">
                <FieldGroup>

                    <Transition enter-active-class="transition-all duration-200"
                        enter-from-class="-translate-y-1 opacity-0" leave-active-class="transition-all duration-150"
                        leave-to-class="-translate-y-1 opacity-0">
                        <Alert v-if="submitError" variant="destructive">
                            <AlertCircle class="size-4" />
                            <AlertDescription>{{ submitError }}</AlertDescription>
                        </Alert>
                    </Transition>

                    <!-- Deleted destination: credentials must be entered again -->
                    <Alert v-if="destinationDeleted">
                        <TriangleAlert class="size-4" />
                        <AlertDescription>
                            The destination <span class="font-medium">{{ snapshot?.destination_name }}</span>
                            of this snapshot was deleted, and Arkeep erased its stored credentials.
                            Enter them below to restore: they are used only for this restore and are not saved.
                            Only the whole snapshot can be restored, and the target agent must be online.
                        </AlertDescription>
                    </Alert>

                    <!-- Agent selector — only online agents can receive a restore job -->
                    <Field>
                        <FieldLabel for="agent">Target agent</FieldLabel>
                        <AsyncCombobox
                            endpoint="/api/v1/agents?status=online"
                            :model-value="agentId ?? ''"
                            :initial-label="props.presetAgent?.name ?? props.snapshot?.agent_name"
                            placeholder="Select an agent…"
                            :disabled="isSubmitting"
                            :class="agentError ? '[&_button]:border-destructive [&_button]:focus-visible:ring-destructive/30' : ''"
                            @update:model-value="agentId = $event"
                            @update:item="onAgentSelected"
                        />
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

                    <!-- Credentials of a deleted destination -->
                    <template v-if="destinationDeleted">
                        <Separator />
                        <p class="text-sm font-medium">Destination credentials</p>

                        <template v-if="destinationType === 's3'">
                            <Field>
                                <FieldLabel for="restore-access-key">Access key</FieldLabel>
                                <Input id="restore-access-key" v-model="accessKey" autocomplete="off"
                                    :disabled="isSubmitting"
                                    :class="accessKeyError ? 'border-destructive focus-visible:ring-destructive/30' : ''" />
                                <FieldError v-if="accessKeyError">{{ accessKeyError }}</FieldError>
                            </Field>
                            <Field>
                                <FieldLabel for="restore-secret-key">Secret key</FieldLabel>
                                <Input id="restore-secret-key" v-model="secretKey" type="password" autocomplete="off"
                                    :disabled="isSubmitting"
                                    :class="secretKeyError ? 'border-destructive focus-visible:ring-destructive/30' : ''" />
                                <FieldError v-if="secretKeyError">{{ secretKeyError }}</FieldError>
                            </Field>
                        </template>

                        <template v-else-if="destinationType === 'sftp'">
                            <Field>
                                <FieldLabel for="restore-sftp-password">Password</FieldLabel>
                                <Input id="restore-sftp-password" v-model="credPassword" type="password"
                                    autocomplete="off" :disabled="isSubmitting" />
                            </Field>
                            <Field>
                                <FieldLabel for="restore-sftp-key">Private key</FieldLabel>
                                <textarea id="restore-sftp-key" v-model="privateKey" rows="4" :disabled="isSubmitting"
                                    placeholder="-----BEGIN OPENSSH PRIVATE KEY-----"
                                    class="border-input bg-background placeholder:text-muted-foreground focus-visible:border-ring focus-visible:ring-ring/50 flex w-full rounded-md border px-3 py-2 text-sm shadow-xs transition-[color,box-shadow] focus-visible:ring-[3px] resize-none font-mono" />
                                <p class="text-xs text-muted-foreground mt-1">
                                    Enter a password, a private key, or leave both empty if the agent's own SSH key has access.
                                </p>
                            </Field>
                        </template>

                        <template v-else-if="destinationType === 'rest'">
                            <Field>
                                <FieldLabel for="restore-rest-user">Username</FieldLabel>
                                <Input id="restore-rest-user" v-model="credUser" autocomplete="off"
                                    :disabled="isSubmitting" />
                            </Field>
                            <Field>
                                <FieldLabel for="restore-rest-password">Password</FieldLabel>
                                <Input id="restore-rest-password" v-model="credPassword" type="password"
                                    autocomplete="off" :disabled="isSubmitting" />
                                <p class="text-xs text-muted-foreground mt-1">
                                    Leave empty if the REST server does not require authentication.
                                </p>
                            </Field>
                        </template>

                        <p v-else class="text-xs text-muted-foreground">
                            This destination type needs no stored credentials: the target agent must be able to
                            reach the repository on its own.
                        </p>

                        <Field v-if="repoPasswordRequired">
                            <FieldLabel for="restore-repo-password">Repository password</FieldLabel>
                            <Input id="restore-repo-password" v-model="repoPassword" type="password"
                                autocomplete="off" :disabled="isSubmitting"
                                :class="repoPasswordError ? 'border-destructive focus-visible:ring-destructive/30' : ''" />
                            <FieldError v-if="repoPasswordError">{{ repoPasswordError }}</FieldError>
                            <p v-else class="text-xs text-muted-foreground mt-1">
                                The restic password of the repository. Arkeep no longer has it, because the
                                policy that created this snapshot was deleted too (or the snapshot was imported).
                            </p>
                        </Field>
                    </template>

                    <!-- File selection — a deleted destination only allows a full restore -->
                    <template v-if="!destinationDeleted">
                        <Separator />
                        <div v-if="presetPaths?.length" class="space-y-2">
                            <p class="text-sm font-medium">Files to restore</p>
                            <ul class="max-h-64 overflow-y-auto rounded border p-2 font-mono text-xs space-y-0.5">
                                <li v-for="path in presetPaths" :key="path" class="truncate">{{ path }}</li>
                            </ul>
                        </div>
                        <div v-else class="space-y-2">
                            <p class="text-sm font-medium">Files to restore</p>
                            <p class="text-xs text-muted-foreground">
                                Leave empty to restore the entire snapshot, or browse to select specific files.
                            </p>
                            <Button
                                type="button"
                                variant="outline"
                                size="sm"
                                :disabled="isBrowsing || isSubmitting || !agentId"
                                @click="browseSnapshot"
                            >
                                <Loader2 v-if="isBrowsing" class="mr-2 h-4 w-4 animate-spin" />
                                {{ browseEntries.length > 0 ? 'Refresh file list' : 'Browse files' }}
                            </Button>
                            <p v-if="browseError" class="text-xs text-destructive">{{ browseError }}</p>
                            <div v-if="browseEntries.length > 0" class="space-y-1">
                                <p v-if="selectedPaths.length > 0" class="text-xs text-muted-foreground">
                                    {{ selectedPaths.length }} item(s) selected
                                </p>
                                <SnapshotFileTree
                                    v-model="selectedPaths"
                                    :entries="browseEntries"
                                    :load-children="loadChildren"
                                    class="max-h-64 overflow-y-auto rounded border"
                                />
                            </div>
                        </div>
                    </template>

                    <SheetFooter class="mt-2 px-0">
                        <Button type="button" variant="outline" :disabled="isSubmitting" @click="onOpenChange(false)">
                            Cancel
                        </Button>
                        <Button type="submit" :disabled="isSubmitting">
                            <Loader2 v-if="isSubmitting" class="size-4 animate-spin" />
                            {{ isSubmitting ? 'Starting…' : 'Start restore' }}
                        </Button>
                    </SheetFooter>

                </FieldGroup>
            </form>
        </SheetContent>
    </Sheet>
</template>