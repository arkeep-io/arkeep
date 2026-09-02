<script setup lang="ts">
// Imports the snapshots of an existing Restic repository into a destination
// that already exists. The create form offers the same thing for a brand-new
// destination; this covers repointing or re-scanning an existing one — the last
// step when migrating a repository between storage providers.
import { ref, watch, computed } from 'vue'
import { api } from '@/services/api'
import { summariseImport } from '@/lib/importSummary'
import type { ApiResponse, Destination, ImportDestinationRequest, ImportDestinationResponse } from '@/types'
import { AsyncCombobox } from '@/components/ui/async-combobox'
import {
    Dialog,
    DialogContent,
    DialogDescription,
    DialogFooter,
    DialogHeader,
    DialogTitle,
} from '@/components/ui/dialog'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Field, FieldError, FieldLabel } from '@/components/ui/field'
import { AlertCircle, CheckCircle2, Loader2 } from '@lucide/vue'

const props = defineProps<{
    open: boolean
    destination: Destination | null
}>()

const emit = defineEmits<{
    'update:open': [value: boolean]
    imported: []
}>()

const agentId = ref('')
const agentError = ref('')
const repoPassword = ref('')
const passwordError = ref('')
const submitError = ref<string | null>(null)
const submitting = ref(false)
const result = ref<ImportDestinationResponse | null>(null)

const summary = computed(() => (result.value ? summariseImport(result.value) : null))

function reset() {
    agentId.value = ''
    agentError.value = ''
    repoPassword.value = ''
    passwordError.value = ''
    submitError.value = null
    result.value = null
}

watch(() => props.open, (open) => {
    if (open) reset()
})

function onOpenChange(value: boolean) {
    emit('update:open', value)
}

async function onSubmit() {
    if (!props.destination) return

    agentError.value = agentId.value ? '' : 'Please select an agent.'
    passwordError.value = repoPassword.value ? '' : 'Repository password is required.'
    if (agentError.value || passwordError.value) return

    submitting.value = true
    submitError.value = null
    try {
        const body: ImportDestinationRequest = {
            agent_id: agentId.value,
            repo_password: repoPassword.value,
        }
        const res = await api<ApiResponse<ImportDestinationResponse>>(
            `/api/v1/destinations/${props.destination.id}/import`,
            { method: 'POST', body },
        )
        result.value = res.data
        emit('imported')
    } catch (e: any) {
        submitError.value = e?.data?.error?.message ?? e?.message ?? 'Failed to import snapshots.'
    } finally {
        submitting.value = false
    }
}
</script>

<template>
    <Dialog :open="props.open" @update:open="onOpenChange">
        <DialogContent class="sm:max-w-md">
            <DialogHeader>
                <DialogTitle>Import snapshots</DialogTitle>
                <DialogDescription>
                    Reads the Restic repository at
                    <span class="font-medium">{{ props.destination?.name }}</span>
                    through the selected agent and records any snapshot not yet known.
                </DialogDescription>
            </DialogHeader>

            <!-- Outcome, shown once the import has run -->
            <div v-if="summary" class="flex items-center gap-3 py-2">
                <AlertCircle v-if="summary.tone === 'error'" class="size-5 shrink-0 text-destructive" />
                <CheckCircle2
                    v-else
                    class="size-5 shrink-0"
                    :class="summary.tone === 'success' ? 'text-green-500' : 'text-muted-foreground'"
                />
                <div>
                    <p class="text-sm font-medium">{{ summary.headline }}</p>
                    <p class="text-xs text-muted-foreground">{{ summary.detail }}</p>
                </div>
            </div>

            <form v-else class="space-y-4" novalidate @submit.prevent="onSubmit">
                <Field>
                    <FieldLabel for="import-dialog-agent">Agent</FieldLabel>
                    <AsyncCombobox
                        endpoint="/api/v1/agents"
                        :model-value="agentId"
                        placeholder="Select an agent"
                        :class="agentError ? '[&_button]:border-destructive [&_button]:focus-visible:ring-destructive/30' : ''"
                        @update:model-value="agentId = $event; agentError = ''"
                    />
                    <FieldError v-if="agentError">{{ agentError }}</FieldError>
                </Field>
                <Field>
                    <FieldLabel for="import-dialog-password">Repository Password</FieldLabel>
                    <Input
                        id="import-dialog-password"
                        v-model="repoPassword"
                        type="password"
                        autocomplete="off"
                        placeholder="Restic repository password"
                        :class="passwordError ? 'border-destructive focus-visible:ring-destructive/30' : ''"
                        @input="passwordError = ''"
                    />
                    <FieldError v-if="passwordError">{{ passwordError }}</FieldError>
                    <p class="text-muted-foreground text-xs">
                        Stored on the destination so the imported snapshots can be browsed and restored.
                    </p>
                </Field>
                <p v-if="submitError" class="text-xs text-destructive flex items-start gap-1.5">
                    <AlertCircle class="size-3.5 mt-0.5 shrink-0" />
                    {{ submitError }}
                </p>
            </form>

            <DialogFooter>
                <Button v-if="summary" @click="onOpenChange(false)">Done</Button>
                <template v-else>
                    <Button variant="outline" :disabled="submitting" @click="onOpenChange(false)">Cancel</Button>
                    <Button :disabled="submitting" @click="onSubmit">
                        <Loader2 v-if="submitting" class="mr-2 size-4 animate-spin" />
                        {{ submitting ? 'Importing…' : 'Import' }}
                    </Button>
                </template>
            </DialogFooter>
        </DialogContent>
    </Dialog>
</template>
