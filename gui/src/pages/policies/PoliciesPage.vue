<script setup lang="ts">
import { ref, onMounted } from 'vue'
import { useRouter } from 'vue-router'
import {
    Table, TableBody, TableCell, TableHead, TableHeader, TableRow,
} from '@/components/ui/table'
import { Button } from '@/components/ui/button'
import { Badge } from '@/components/ui/badge'
import { Skeleton } from '@/components/ui/skeleton'
import { Alert, AlertDescription } from '@/components/ui/alert'
import {
    DropdownMenu, DropdownMenuContent, DropdownMenuItem,
    DropdownMenuSeparator, DropdownMenuTrigger,
} from '@/components/ui/dropdown-menu'
import {
    AlertDialog, AlertDialogAction, AlertDialogCancel, AlertDialogContent,
    AlertDialogDescription, AlertDialogFooter, AlertDialogHeader, AlertDialogTitle,
} from '@/components/ui/alert-dialog'
import {
    ShieldCheck, Plus, RefreshCw, MoreHorizontal, PencilLine, Trash2, Play,
} from 'lucide-vue-next'
import { api } from '@/services/api'
import type { ApiResponse, Policy } from '@/types'
import PolicySheet from '@/components/policies/PolicySheet.vue'

// ---------------------------------------------------------------------------
// State
// ---------------------------------------------------------------------------

const router = useRouter()

const policies = ref<Policy[]>([])
const total = ref(0)
const loading = ref(false)
const error = ref<string | null>(null)

const sheetOpen = ref(false)
const editingPolicy = ref<Policy | null>(null)

const deleteDialogOpen = ref(false)
const deletingPolicy = ref<Policy | null>(null)
const deleteError = ref<string | null>(null)
const deleting = ref(false)

const triggeringId = ref<string | null>(null)

// ---------------------------------------------------------------------------
// Data fetching
// ---------------------------------------------------------------------------

interface PolicyListResponse { items: Policy[]; total: number }

async function fetchPolicies() {
    loading.value = true
    error.value = null
    try {
        const res = await api<ApiResponse<PolicyListResponse>>('/api/v1/policies')
        policies.value = res.data.items ?? []
        total.value = res.data.total ?? 0
    } catch (e: any) {
        error.value = e?.data?.message ?? 'Failed to load policies.'
    } finally {
        loading.value = false
    }
}

onMounted(fetchPolicies)

// ---------------------------------------------------------------------------
// Sheet (create / edit)
// ---------------------------------------------------------------------------

function openCreate() {
    editingPolicy.value = null
    sheetOpen.value = true
}

function openEdit(policy: Policy) {
    editingPolicy.value = policy
    sheetOpen.value = true
}

function onSaved() {
    sheetOpen.value = false
    fetchPolicies()
}

// ---------------------------------------------------------------------------
// Delete
// ---------------------------------------------------------------------------

function openDelete(policy: Policy) {
    deletingPolicy.value = policy
    deleteError.value = null
    deleteDialogOpen.value = true
}

async function confirmDelete() {
    if (!deletingPolicy.value) return
    deleting.value = true
    deleteError.value = null
    try {
        await api(`/api/v1/policies/${deletingPolicy.value.id}`, { method: 'DELETE' })
        deleteDialogOpen.value = false
        fetchPolicies()
    } catch (e: any) {
        deleteError.value = e?.data?.message ?? 'Failed to delete policy.'
    } finally {
        deleting.value = false
    }
}

// ---------------------------------------------------------------------------
// Trigger
// ---------------------------------------------------------------------------

async function triggerPolicy(policy: Policy) {
    triggeringId.value = policy.id
    try {
        await api(`/api/v1/policies/${policy.id}/trigger`, { method: 'POST' })
    } catch (e: any) {
        error.value = e?.data?.message ?? `Failed to trigger "${policy.name}".`
    } finally {
        triggeringId.value = null
    }
}

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

/**
 * Converts a cron expression to a short human-readable label.
 * Falls back to the raw expression for unrecognised schedules.
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

const N_COLS = 5
</script>

<template>
    <div class="flex flex-col gap-6 p-6">

        <!-- Page header -->
        <div class="flex items-center justify-between">
            <div>
                <h1 class="text-2xl font-semibold tracking-tight">Policies</h1>
                <p class="mt-1 text-sm text-muted-foreground">
                    Backup policies define what to back up, when, and where.
                </p>
            </div>
            <div class="flex items-center gap-2">
                <Button variant="outline" size="icon" :disabled="loading" @click="fetchPolicies">
                    <RefreshCw class="w-4 h-4" :class="{ 'animate-spin': loading }" />
                </Button>
                <Button @click="openCreate">
                    <Plus class="w-4 h-4" />
                    New Policy
                </Button>
            </div>
        </div>

        <!-- Error banner -->
        <Transition enter-active-class="transition-all duration-200 ease-out"
            enter-from-class="opacity-0 -translate-y-1" leave-active-class="transition-all duration-150 ease-in"
            leave-to-class="opacity-0 -translate-y-1">
            <Alert v-if="error" variant="destructive">
                <AlertDescription>{{ error }}</AlertDescription>
            </Alert>
        </Transition>

        <!-- Table -->
        <div class="border rounded-md">
            <Table>
                <TableHeader>
                    <TableRow>
                        <TableHead>Name</TableHead>
                        <TableHead>Agent</TableHead>
                        <TableHead>Schedule</TableHead>
                        <TableHead>Status</TableHead>
                        <TableHead class="w-12" />
                    </TableRow>
                </TableHeader>
                <TableBody>

                    <!-- Loading skeleton -->
                    <template v-if="loading">
                        <TableRow v-for="n in 4" :key="n">
                            <TableCell v-for="col in N_COLS" :key="col">
                                <Skeleton class="w-full h-4" />
                            </TableCell>
                        </TableRow>
                    </template>

                    <!-- Empty state -->
                    <template v-else-if="policies.length === 0">
                        <TableRow>
                            <TableCell :colspan="N_COLS">
                                <div class="flex flex-col items-center justify-center gap-3 py-16 text-center">
                                    <div class="p-4 rounded-full bg-muted">
                                        <ShieldCheck class="w-8 h-8 text-muted-foreground" />
                                    </div>
                                    <div>
                                        <p class="font-medium">No policies yet</p>
                                        <p class="mt-1 text-sm text-muted-foreground">
                                            Create your first backup policy to get started.
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
                            <!-- Name -->
                            <TableCell class="font-medium">{{ policy.name }}</TableCell>

                            <!-- Agent name -->
                            <TableCell class="text-sm text-muted-foreground">
                                {{ policy.agent_name }}
                            </TableCell>

                            <!-- Schedule -->
                            <TableCell>
                                <span class="font-mono text-xs text-muted-foreground">
                                    {{ scheduleLabel(policy.schedule) }}
                                </span>
                            </TableCell>

                            <!-- Status badge -->
                            <TableCell>
                                <Badge :variant="policy.enabled ? 'default' : 'secondary'">
                                    {{ policy.enabled ? 'Enabled' : 'Disabled' }}
                                </Badge>
                            </TableCell>

                            <!-- Actions -->
                            <TableCell @click.stop>
                                <DropdownMenu>
                                    <DropdownMenuTrigger as-child>
                                        <Button variant="ghost" size="icon" class="w-8 h-8">
                                            <MoreHorizontal class="w-4 h-4" />
                                        </Button>
                                    </DropdownMenuTrigger>
                                    <DropdownMenuContent align="end">
                                        <DropdownMenuItem :disabled="triggeringId === policy.id"
                                            @click="triggerPolicy(policy)">
                                            <Play class="w-4 h-4 mr-2" />
                                            Run Now
                                        </DropdownMenuItem>
                                        <DropdownMenuSeparator />
                                        <DropdownMenuItem @click="openEdit(policy)">
                                            <PencilLine class="w-4 h-4 mr-2" />
                                            Edit
                                        </DropdownMenuItem>
                                        <DropdownMenuSeparator />
                                        <DropdownMenuItem class="text-destructive focus:text-destructive"
                                            @click="openDelete(policy)">
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

        <!-- Footer count -->
        <p v-if="!loading && total > 0" class="text-sm text-muted-foreground">
            {{ total }} {{ total === 1 ? 'policy' : 'policies' }}
        </p>

    </div>

    <!-- Create / Edit sheet -->
    <PolicySheet :open="sheetOpen" :policy="editingPolicy" @update:open="sheetOpen = $event" @saved="onSaved" />

    <!-- Delete confirmation dialog -->
    <AlertDialog :open="deleteDialogOpen" @update:open="deleteDialogOpen = $event">
        <AlertDialogContent>
            <AlertDialogHeader>
                <AlertDialogTitle>Delete Policy</AlertDialogTitle>
                <AlertDialogDescription>
                    Are you sure you want to delete
                    <span class="font-semibold">{{ deletingPolicy?.name }}</span>?
                    This action cannot be undone. Scheduled runs will be removed.
                </AlertDialogDescription>
            </AlertDialogHeader>

            <Alert v-if="deleteError" variant="destructive" class="mt-2">
                <AlertDescription>{{ deleteError }}</AlertDescription>
            </Alert>

            <AlertDialogFooter>
                <AlertDialogCancel :disabled="deleting">Cancel</AlertDialogCancel>
                <AlertDialogAction class="bg-destructive text-destructive-foreground hover:bg-destructive/90"
                    :disabled="deleting" @click.prevent="confirmDelete">
                    {{ deleting ? 'Deleting…' : 'Delete' }}
                </AlertDialogAction>
            </AlertDialogFooter>
        </AlertDialogContent>
    </AlertDialog>
</template>