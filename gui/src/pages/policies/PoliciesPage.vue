<script setup lang="ts">
import { ref, onMounted, computed, watch } from 'vue'
import { RouterLink, useRouter } from 'vue-router'
import { useAuthStore } from '@/stores/auth'
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
    Copy,
    Trash2,
    Play,
    ShieldCheck,
    RefreshCw,
} from '@lucide/vue'
import { Tooltip, TooltipContent, TooltipTrigger } from '@/components/ui/tooltip'
import { api, apiErrorMessage } from '@/services/api'
import type { Policy, PolicyRecentJob, ApiResponse, TriggerResponse } from '@/types'
import { formatDate, statusDotClass, statusLabel } from '@/lib/jobUtils'
import PolicySheet from '@/components/policies/PolicySheet.vue'
import SortableTableHead from '@/components/shared/SortableTableHead.vue'
import { useListSort } from '@/composables/useListSort'

interface PolicyListResponse {
    items: Policy[]
    total: number
}

// ---------------------------------------------------------------------------
// State
// ---------------------------------------------------------------------------

const router = useRouter()
const authStore = useAuthStore()

const policies = ref<Policy[]>([])
const total = ref(0)
const loading = ref(true)
const error = ref<string | null>(null)

const page = ref(1)
const pageSize = 50
const offset = computed(() => (page.value - 1) * pageSize)
const totalPages = computed(() => Math.ceil(total.value / pageSize))

// Server-side column sort, kept in the URL; by name when unset (issue #320).
const { sortKey, sortOrder, queryParams: sortParams, toggle: toggleSort } = useListSort({ key: 'name' })

// Sheet
const sheetOpen = ref(false)
const editingPolicy = ref<Policy | null>(null)
// Source policy when duplicating; mutually exclusive with editingPolicy.
const cloningPolicy = ref<Policy | null>(null)

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

/** Number of recent runs shown as dots; the server returns at most this many. */
const RECENT_RUNS = 5

/** Most recent run first, as the API returns them. */
function recentJobs(policy: Policy): PolicyRecentJob[] {
    return policy.recent_jobs ?? []
}

/**
 * Recent runs oldest → newest, left to right, padded on the left with empty
 * slots so the latest run always sits in the same column.
 */
function runHistory(policy: Policy): (PolicyRecentJob | null)[] {
    const runs = recentJobs(policy).slice(0, RECENT_RUNS).reverse()
    return [...Array<null>(RECENT_RUNS - runs.length).fill(null), ...runs]
}

/** When the latest run started, or was queued if it has not started yet. */
function lastRunDate(policy: Policy): string | null {
    const last = recentJobs(policy)[0]
    return last ? (last.started_at ?? last.created_at) : null
}

// ---------------------------------------------------------------------------
// Data fetching
// ---------------------------------------------------------------------------

async function fetchPolicies() {
    loading.value = true
    error.value = null
    try {
        const res = await api<ApiResponse<PolicyListResponse>>(`/api/v1/policies?limit=${pageSize}&offset=${offset.value}${sortParams.value}`)
        policies.value = res.data.items
        total.value = res.data.total
    } catch (e: any) {
        error.value = apiErrorMessage(e, 'Failed to load policies')
    } finally {
        loading.value = false
    }
}

// A new sort starts again from the first page.
watch(sortParams, () => {
    page.value = 1
    fetchPolicies()
})

// ---------------------------------------------------------------------------
// Sheet
// ---------------------------------------------------------------------------

function openCreate() {
    editingPolicy.value = null
    cloningPolicy.value = null
    sheetOpen.value = true
}

function openEditSheet(policy: Policy) {
    cloningPolicy.value = null
    editingPolicy.value = policy
    sheetOpen.value = true
}

function openCloneSheet(policy: Policy) {
    editingPolicy.value = null
    cloningPolicy.value = policy
    sheetOpen.value = true
}

function onSaved() {
    fetchPolicies()
}

async function goToPage(p: number) {
    if (p < 1 || p > totalPages.value) return
    page.value = p
    await fetchPolicies()
}

// ---------------------------------------------------------------------------
// Trigger
// ---------------------------------------------------------------------------

async function triggerPolicy(policy: Policy) {
    triggeringId.value = policy.id
    try {
        const res = await api<ApiResponse<TriggerResponse>>(`/api/v1/policies/${policy.id}/trigger`, { method: 'POST' })
        router.push({ name: 'job-detail', params: { id: res.data.job_id } })
    } catch (e: any) {
        error.value = apiErrorMessage(e, `Failed to trigger "${policy.name}"`)
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
        if (policies.value.length === 1 && page.value > 1) {
            page.value--
        }
        await fetchPolicies()
    } catch (e: any) {
        error.value = apiErrorMessage(e, 'Failed to delete policy')
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
                <Button variant="outline" size="icon" aria-label="Refresh" :disabled="loading" @click="fetchPolicies">
                    <RefreshCw class="w-4 h-4" :class="{ 'animate-spin': loading }" />
                </Button>
                <Button v-if="authStore.isAdmin" @click="openCreate">
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
        <div class="border rounded-md overflow-x-auto">
            <Table>
                <TableHeader>
                    <TableRow>
                        <SortableTableHead label="Name" :active="sortKey === 'name'" :order="sortOrder"
                            @toggle="toggleSort('name')" />
                        <SortableTableHead label="Agent" :active="sortKey === 'agent'" :order="sortOrder"
                            @toggle="toggleSort('agent')" />
                        <TableHead>Schedule</TableHead>
                        <TableHead>Last Run</TableHead>
                        <TableHead>Recent Runs</TableHead>
                        <SortableTableHead label="Status" :active="sortKey === 'status'" :order="sortOrder"
                            @toggle="toggleSort('status', 'desc')" />
                        <TableHead class="w-13" />
                    </TableRow>
                </TableHeader>

                <TableBody>
                    <!-- Loading skeletons -->
                    <template v-if="loading">
                        <TableRow v-for="n in 5" :key="n">
                            <TableCell v-for="col in 7" :key="col">
                                <Skeleton class="w-full h-4" />
                            </TableCell>
                        </TableRow>
                    </template>

                    <!-- Empty state -->
                    <template v-else-if="policies.length === 0">
                        <TableRow>
                            <TableCell colspan="7">
                                <div class="flex flex-col items-center justify-center gap-3 py-16 text-center">
                                    <div class="p-4 rounded-full bg-muted">
                                        <ShieldCheck class="w-10 h-10 text-muted-foreground" />
                                    </div>
                                    <div>
                                        <p class="font-medium">No policies configured</p>
                                        <p class="mt-1 text-sm text-muted-foreground">
                                            Create a policy to start scheduling backups.
                                        </p>
                                    </div>
                                    <Button v-if="authStore.isAdmin" size="sm" @click="openCreate">
                                        <Plus class="w-4 h-4" />
                                        New Policy
                                    </Button>
                                </div>
                            </TableCell>
                        </TableRow>
                    </template>

                    <!-- Data rows -->
                    <template v-else>
                        <TableRow v-for="policy in policies" :key="policy.id"
                            class="cursor-pointer hover:bg-muted/50"
                            tabindex="0"
                            role="link"
                            :aria-label="`View policy ${policy.name}`"
                            @click="router.push(`/policies/${policy.id}`)"
                            @keyup.enter="router.push(`/policies/${policy.id}`)">
                            <TableCell class="font-medium">{{ policy.name }}</TableCell>
                            <TableCell class="text-sm text-muted-foreground">
                                {{ policy.agent_name }}
                            </TableCell>
                            <TableCell>
                                <span class="font-mono text-xs text-muted-foreground">
                                    {{ scheduleLabel(policy.schedule) }}
                                </span>
                            </TableCell>
                            <TableCell class="text-sm text-muted-foreground whitespace-nowrap" data-testid="last-run">
                                {{ lastRunDate(policy) ? formatDate(lastRunDate(policy)) : 'Never' }}
                            </TableCell>
                            <!-- Oldest to newest; each dot opens its job -->
                            <TableCell @click.stop>
                                <div class="flex items-center gap-1.5" role="list" aria-label="Recent runs">
                                    <template v-for="(run, i) in runHistory(policy)" :key="run?.id ?? `empty-${i}`">
                                        <Tooltip v-if="run">
                                            <TooltipTrigger as-child>
                                                <RouterLink :to="{ name: 'job-detail', params: { id: run.id } }"
                                                    role="listitem"
                                                    data-testid="run-dot"
                                                    :data-status="run.status"
                                                    class="block size-2.5 rounded-full transition-transform hover:scale-125 focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-ring"
                                                    :class="[statusDotClass(run.status), { 'animate-pulse': run.status === 'running' }]"
                                                    :aria-label="`${statusLabel(run.status)} — ${formatDate(run.started_at ?? run.created_at)}`" />
                                            </TooltipTrigger>
                                            <TooltipContent>
                                                {{ statusLabel(run.status) }} · {{ formatDate(run.started_at ?? run.created_at) }}
                                            </TooltipContent>
                                        </Tooltip>
                                        <span v-else role="listitem" aria-label="No run" data-testid="run-dot-empty"
                                            class="block size-2.5 rounded-full border border-dashed border-muted-foreground/40" />
                                    </template>
                                </div>
                            </TableCell>
                            <TableCell>
                                <Badge :variant="policy.enabled ? 'default' : 'secondary'">
                                    {{ policy.enabled ? 'Enabled' : 'Disabled' }}
                                </Badge>
                            </TableCell>

                            <!-- Actions dropdown -->
                            <TableCell @click.stop>
                                <!-- Every action changes or runs the policy: admins only -->
                                <DropdownMenu v-if="authStore.isAdmin">
                                    <DropdownMenuTrigger as-child>
                                        <Button variant="ghost" size="icon" class="w-8 h-8">
                                            <MoreHorizontal class="w-4 h-4" />
                                            <span class="sr-only">Open actions</span>
                                        </Button>
                                    </DropdownMenuTrigger>
                                    <DropdownMenuContent align="end">
                                        <!-- The wrapper receives hover while the disabled item has pointer-events: none -->
                                        <Tooltip v-if="authStore.isAdmin" :disabled="policy.enabled">
                                            <TooltipTrigger as-child>
                                                <div>
                                                    <DropdownMenuItem
                                                        :disabled="!policy.enabled || triggeringId === policy.id"
                                                        @click="triggerPolicy(policy)">
                                                        <Play class="w-4 h-4 mr-2" />
                                                        Run Now
                                                    </DropdownMenuItem>
                                                </div>
                                            </TooltipTrigger>
                                            <TooltipContent side="left">Enable the policy to run a backup.</TooltipContent>
                                        </Tooltip>
                                        <DropdownMenuSeparator v-if="authStore.isAdmin" />
                                        <DropdownMenuItem @click="openEditSheet(policy)">
                                            <PencilLine class="w-4 h-4 mr-2" />
                                            Edit
                                        </DropdownMenuItem>
                                        <DropdownMenuItem @click="openCloneSheet(policy)">
                                            <Copy class="w-4 h-4 mr-2" />
                                            Duplicate
                                        </DropdownMenuItem>
                                        <DropdownMenuSeparator v-if="authStore.isAdmin" />
                                        <DropdownMenuItem v-if="authStore.isAdmin"
                                            class="text-destructive focus:text-destructive"
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

        <!-- Pagination -->
        <div v-if="!loading && totalPages > 1" class="flex items-center justify-between text-sm text-muted-foreground">
            <span>
                Showing {{ offset + 1 }}–{{ Math.min(offset + pageSize, total) }} of {{ total }} policies
            </span>
            <div class="flex items-center gap-2">
                <Button variant="outline" size="sm" :disabled="page === 1" @click="goToPage(page - 1)">
                    Previous
                </Button>
                <span class="px-2">{{ page }} / {{ totalPages }}</span>
                <Button variant="outline" size="sm" :disabled="page === totalPages" @click="goToPage(page + 1)">
                    Next
                </Button>
            </div>
        </div>

    </div>

    <!-- Edit / create sheet -->
    <PolicySheet :policy="editingPolicy" :clone-from="cloningPolicy" :open="sheetOpen" @update:open="sheetOpen = $event" @saved="onSaved" />

    <!-- Delete confirmation dialog -->
    <AlertDialog :open="deleteDialogOpen" @update:open="deleteDialogOpen = $event">
        <AlertDialogContent>
            <AlertDialogHeader>
                <AlertDialogTitle>Delete policy?</AlertDialogTitle>
                <AlertDialogDescription>
                    <span v-if="policyToDelete">
                        <strong>{{ policyToDelete.name }}</strong> will be deleted and its stored repository password erased.
                        All scheduled runs for this policy will be removed.
                        Its existing snapshots stay listed, and the backup data on the storage is not touched.
                        This action cannot be undone.
                    </span>
                </AlertDialogDescription>
            </AlertDialogHeader>
            <AlertDialogFooter>
                <AlertDialogCancel :disabled="deleteLoading">Cancel</AlertDialogCancel>
                <AlertDialogAction variant="destructive"
                    :disabled="deleteLoading" @click="confirmDelete">
                    {{ deleteLoading ? 'Deleting…' : 'Delete' }}
                </AlertDialogAction>
            </AlertDialogFooter>
        </AlertDialogContent>
    </AlertDialog>
</template>