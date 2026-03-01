<script setup lang="ts">
import { ref, onMounted } from 'vue'
import { useRouter } from 'vue-router'
import {
    Table,
    TableBody,
    TableCell,
    TableHead,
    TableHeader,
    TableRow,
} from '@/components/ui/table'
import {
    DropdownMenu,
    DropdownMenuContent,
    DropdownMenuItem,
    DropdownMenuSeparator,
    DropdownMenuTrigger,
} from '@/components/ui/dropdown-menu'
import {
    AlertDialog,
    AlertDialogAction,
    AlertDialogCancel,
    AlertDialogContent,
    AlertDialogDescription,
    AlertDialogFooter,
    AlertDialogHeader,
    AlertDialogTitle,
} from '@/components/ui/alert-dialog'
import { Alert, AlertDescription } from '@/components/ui/alert'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Skeleton } from '@/components/ui/skeleton'
import {
    Plus,
    MoreHorizontal,
    PencilLine,
    Trash2,
    Play,
    ShieldCheck,
} from 'lucide-vue-next'
import { api } from '@/services/api'
import type { Policy, ApiResponse } from '@/types'
import PolicySheet from '@/components/policies/PolicySheet.vue'

interface PolicyListResponse {
    items: Policy[]
    total: number
}

// ---------------------------------------------------------------------------
// State
// ---------------------------------------------------------------------------

const router = useRouter()

const policies = ref<Policy[]>([])
const total = ref(0)
const loading = ref(true)
const error = ref<string | null>(null)

// Sheet
const sheetOpen = ref(false)
const editingPolicy = ref<Policy | null>(null)

// Delete dialog
const deleteDialogOpen = ref(false)
const policyToDelete = ref<Policy | null>(null)
const deleteLoading = ref(false)

// Trigger
const triggeringId = ref<string | null>(null)

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

/**
 * Returns a human-readable label for common cron expressions.
 * Falls back to the raw expression for custom schedules.
 */
function scheduleLabel(cron: string): string {
    const presets: Record<string, string> = {
        '0 * * * *': 'Hourly',
        '0 2 * * *': 'Daily at 02:00',
        '0 0 * * *': 'Daily at midnight',
        '0 2 * * 0': 'Weekly (Sun)',
        '0 2 * * 1': 'Weekly (Mon)',
        '0 2 1 * *': 'Monthly',
        '@daily': 'Daily',
        '@weekly': 'Weekly',
        '@monthly': 'Monthly',
        '@hourly': 'Hourly',
    }
    return presets[cron] ?? cron
}

// ---------------------------------------------------------------------------
// Data fetching
// ---------------------------------------------------------------------------

async function fetchPolicies() {
    loading.value = true
    error.value = null
    try {
        const res = await api<ApiResponse<PolicyListResponse>>('/api/v1/policies')
        policies.value = res.data.items
        total.value = res.data.total
    } catch (e: any) {
        error.value = e?.message ?? 'Failed to load policies'
    } finally {
        loading.value = false
    }
}

// ---------------------------------------------------------------------------
// Sheet
// ---------------------------------------------------------------------------

function openCreate() {
    editingPolicy.value = null
    sheetOpen.value = true
}

function openEditSheet(policy: Policy) {
    editingPolicy.value = policy
    sheetOpen.value = true
}

function onSaved() {
    fetchPolicies()
}

// ---------------------------------------------------------------------------
// Trigger
// ---------------------------------------------------------------------------

async function triggerPolicy(policy: Policy) {
    triggeringId.value = policy.id
    try {
        await api(`/api/v1/policies/${policy.id}/trigger`, { method: 'POST' })
    } catch (e: any) {
        error.value = e?.message ?? `Failed to trigger "${policy.name}"`
    } finally {
        triggeringId.value = null
    }
}

// ---------------------------------------------------------------------------
// Delete
// ---------------------------------------------------------------------------

function openDeleteDialog(policy: Policy) {
    policyToDelete.value = policy
    deleteDialogOpen.value = true
}

async function confirmDelete() {
    if (!policyToDelete.value) return
    deleteLoading.value = true
    try {
        await api(`/api/v1/policies/${policyToDelete.value.id}`, { method: 'DELETE' })
        deleteDialogOpen.value = false
        policyToDelete.value = null
        await fetchPolicies()
    } catch (e: any) {
        error.value = e?.message ?? 'Failed to delete policy'
    } finally {
        deleteLoading.value = false
    }
}

// ---------------------------------------------------------------------------
// Lifecycle
// ---------------------------------------------------------------------------

onMounted(fetchPolicies)
</script>

<template>
    <div class="flex flex-col gap-6 p-6">

        <!-- Header -->
        <div class="flex items-center justify-between">
            <div>
                <h1 class="text-2xl font-semibold tracking-tight">Policies</h1>
                <p class="mt-1 text-sm text-muted-foreground">
                    Manage backup policies — what to back up, when, and where.
                </p>
            </div>
            <div class="flex items-center gap-2">
                <Button @click="openCreate">
                    <Plus class="w-4 h-4" />
                    New Policy
                </Button>
            </div>
        </div>

        <!-- Error banner -->
        <Alert v-if="error" variant="destructive">
            <AlertDescription>{{ error }}</AlertDescription>
        </Alert>

        <!-- Table -->
        <div class="border rounded-md">
            <Table>
                <TableHeader>
                    <TableRow>
                        <TableHead>Name</TableHead>
                        <TableHead>Agent</TableHead>
                        <TableHead>Schedule</TableHead>
                        <TableHead>Status</TableHead>
                        <TableHead class="w-13" />
                    </TableRow>
                </TableHeader>

                <TableBody>
                    <!-- Loading skeletons -->
                    <template v-if="loading">
                        <TableRow v-for="n in 5" :key="n">
                            <TableCell v-for="col in 5" :key="col">
                                <Skeleton class="w-full h-4" />
                            </TableCell>
                        </TableRow>
                    </template>

                    <!-- Empty state -->
                    <template v-else-if="policies.length === 0">
                        <TableRow>
                            <TableCell colspan="5">
                                <div class="flex flex-col items-center justify-center gap-3 py-16 text-center">
                                    <div class="p-4 rounded-full bg-muted">
                                        <ShieldCheck class="w-8 h-8 text-muted-foreground" />
                                    </div>
                                    <div>
                                        <p class="font-medium">No policies configured</p>
                                        <p class="mt-1 text-sm text-muted-foreground">
                                            Create a policy to start scheduling backups.
                                        </p>
                                    </div>
                                    <Button size="sm" @click="openCreate">
                                        <Plus class="w-4 h-4" />
                                        New Policy
                                    </Button>
                                </div>
                            </TableCell>
                        </TableRow>
                    </template>

                    <!-- Data rows -->
                    <template v-else>
                        <TableRow v-for="policy in policies" :key="policy.id" class="cursor-pointer"
                            @click="router.push(`/policies/${policy.id}`)">
                            <TableCell class="font-medium">{{ policy.name }}</TableCell>
                            <TableCell class="text-sm text-muted-foreground">
                                {{ policy.agent_name }}
                            </TableCell>
                            <TableCell>
                                <span class="font-mono text-xs text-muted-foreground">
                                    {{ scheduleLabel(policy.schedule) }}
                                </span>
                            </TableCell>
                            <TableCell>
                                <Badge :variant="policy.enabled ? 'default' : 'secondary'">
                                    {{ policy.enabled ? 'Enabled' : 'Disabled' }}
                                </Badge>
                            </TableCell>

                            <!-- Actions dropdown -->
                            <TableCell @click.stop>
                                <DropdownMenu>
                                    <DropdownMenuTrigger as-child>
                                        <Button variant="ghost" size="icon" class="w-8 h-8">
                                            <MoreHorizontal class="w-4 h-4" />
                                            <span class="sr-only">Open actions</span>
                                        </Button>
                                    </DropdownMenuTrigger>
                                    <DropdownMenuContent align="end">
                                        <DropdownMenuItem :disabled="triggeringId === policy.id"
                                            @click="triggerPolicy(policy)">
                                            <Play class="w-4 h-4 mr-2" />
                                            Run Now
                                        </DropdownMenuItem>
                                        <DropdownMenuSeparator />
                                        <DropdownMenuItem @click="openEditSheet(policy)">
                                            <PencilLine class="w-4 h-4 mr-2" />
                                            Edit
                                        </DropdownMenuItem>
                                        <DropdownMenuSeparator />
                                        <DropdownMenuItem class="text-destructive focus:text-destructive"
                                            @click="openDeleteDialog(policy)">
                                            <Trash2 class="w-4 h-4 mr-2" />
                                            Delete
                                        </DropdownMenuItem>
                                    </DropdownMenuContent>
                                </DropdownMenu>
                            </TableCell>
                        </TableRow>
                    </template>
                </TableBody>
            </Table>
        </div>

    </div>

    <!-- Edit / create sheet -->
    <PolicySheet :policy="editingPolicy" :open="sheetOpen" @update:open="sheetOpen = $event" @saved="onSaved" />

    <!-- Delete confirmation dialog -->
    <AlertDialog :open="deleteDialogOpen" @update:open="deleteDialogOpen = $event">
        <AlertDialogContent>
            <AlertDialogHeader>
                <AlertDialogTitle>Delete policy?</AlertDialogTitle>
                <AlertDialogDescription>
                    <span v-if="policyToDelete">
                        <strong>{{ policyToDelete.name }}</strong> will be permanently deleted.
                        All scheduled runs for this policy will be removed.
                        This action cannot be undone.
                    </span>
                </AlertDialogDescription>
            </AlertDialogHeader>
            <AlertDialogFooter>
                <AlertDialogCancel :disabled="deleteLoading">Cancel</AlertDialogCancel>
                <AlertDialogAction class="bg-destructive text-destructive-foreground hover:bg-destructive/90"
                    :disabled="deleteLoading" @click="confirmDelete">
                    {{ deleteLoading ? 'Deleting…' : 'Delete' }}
                </AlertDialogAction>
            </AlertDialogFooter>
        </AlertDialogContent>
    </AlertDialog>
</template>