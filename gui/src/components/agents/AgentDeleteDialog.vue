<script setup lang="ts">
// AgentDeleteDialog.vue — Confirms the deletion of an agent.
//
// An agent that policies back up on, or that destinations use as their
// maintenance agent, cannot be deleted: those would stop running without
// any error. The dialog loads what depends on the agent when it opens, lists
// it with links, and keeps Delete disabled until nothing is left. The server
// enforces the same rule (409).
import { ref, watch } from 'vue'
import { RouterLink } from 'vue-router'
import {
    AlertDialog, AlertDialogCancel, AlertDialogContent, AlertDialogDescription,
    AlertDialogFooter, AlertDialogHeader, AlertDialogTitle,
} from '@/components/ui/alert-dialog'
import { Alert, AlertDescription } from '@/components/ui/alert'
import { Button } from '@/components/ui/button'
import { Loader2 } from '@lucide/vue'
import { api, apiErrorMessage } from '@/services/api'
import type { ApiResponse } from '@/types'

interface UsageRef { id: string; name: string }
interface AgentUsage { policies: UsageRef[]; destinations: UsageRef[] }

const props = defineProps<{
    open: boolean
    agent: { id: string; name: string } | null
}>()

const emit = defineEmits<{
    'update:open': [value: boolean]
    deleted: []
}>()

const usage = ref<AgentUsage | null>(null)
const loading = ref(false)
const deleting = ref(false)
const error = ref<string | null>(null)

const inUse = () => !!usage.value && (usage.value.policies.length > 0 || usage.value.destinations.length > 0)

async function loadUsage(id: string): Promise<void> {
    usage.value = null
    error.value = null
    loading.value = true
    try {
        const res = await api<ApiResponse<AgentUsage>>(`/api/v1/agents/${id}/usage`)
        usage.value = res.data
    } catch (e) {
        error.value = apiErrorMessage(e, 'Failed to check what uses this agent.')
    } finally {
        loading.value = false
    }
}

watch(() => props.open, (open) => {
    if (open && props.agent) loadUsage(props.agent.id)
}, { immediate: true })

async function confirmDelete(): Promise<void> {
    if (!props.agent) return
    deleting.value = true
    error.value = null
    try {
        await api(`/api/v1/agents/${props.agent.id}`, { method: 'DELETE' })
        emit('update:open', false)
        emit('deleted')
    } catch (e) {
        // Something may have started using the agent meanwhile: reload the
        // list, then show why the deletion was refused.
        const message = apiErrorMessage(e, 'Failed to delete agent.')
        await loadUsage(props.agent.id)
        error.value = message
    } finally {
        deleting.value = false
    }
}
</script>

<template>
    <AlertDialog :open="props.open" @update:open="emit('update:open', $event)">
        <AlertDialogContent>
            <AlertDialogHeader>
                <AlertDialogTitle>Delete agent?</AlertDialogTitle>
                <AlertDialogDescription>
                    <span v-if="props.agent && !inUse()">
                        <strong>{{ props.agent.name }}</strong> will be deleted. Existing jobs and snapshots
                        are kept.
                    </span>
                    <span v-else-if="props.agent">
                        <strong>{{ props.agent.name }}</strong> is still in use and cannot be deleted. Move
                        these to another agent, or delete them, first.
                    </span>
                </AlertDialogDescription>
            </AlertDialogHeader>

            <Alert v-if="error" variant="destructive">
                <AlertDescription>{{ error }}</AlertDescription>
            </Alert>

            <div v-if="loading" class="flex items-center gap-2 text-sm text-muted-foreground">
                <Loader2 class="size-4 animate-spin" />
                Checking what uses this agent…
            </div>

            <div v-else-if="usage && inUse()" class="flex flex-col gap-3 text-sm" data-testid="agent-usage">
                <div v-if="usage.policies.length > 0">
                    <p class="font-medium">Policies that back up on this agent</p>
                    <ul class="mt-1 list-disc pl-5">
                        <li v-for="p in usage.policies" :key="p.id">
                            <RouterLink :to="`/policies/${p.id}`" class="hover:underline">{{ p.name }}</RouterLink>
                        </li>
                    </ul>
                </div>
                <div v-if="usage.destinations.length > 0">
                    <p class="font-medium">Destinations that use it as maintenance agent</p>
                    <ul class="mt-1 list-disc pl-5">
                        <li v-for="d in usage.destinations" :key="d.id">
                            <RouterLink :to="`/destinations/${d.id}`" class="hover:underline">{{ d.name }}</RouterLink>
                        </li>
                    </ul>
                </div>
            </div>

            <AlertDialogFooter>
                <AlertDialogCancel :disabled="deleting">Cancel</AlertDialogCancel>
                <Button variant="destructive" :disabled="loading || deleting || !usage || inUse()"
                    @click="confirmDelete">
                    {{ deleting ? 'Deleting…' : 'Delete' }}
                </Button>
            </AlertDialogFooter>
        </AlertDialogContent>
    </AlertDialog>
</template>
