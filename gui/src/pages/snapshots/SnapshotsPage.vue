<script setup lang="ts">
import { ref, computed, onMounted, watch } from 'vue'
import { useAuthStore } from '@/stores/auth'
import {
    Table,
    TableBody,
    TableCell,
    TableHead,
    TableHeader,
    TableRow,
} from '@/components/ui/table'
import { AsyncCombobox } from '@/components/ui/async-combobox'
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
import { Camera, Loader2, MoreHorizontal, RefreshCcw, RefreshCw, RotateCcw, Trash2 } from '@lucide/vue'
import { api, apiErrorMessage } from '@/services/api'
import type { ApiResponse, Destination, PaginatedResponse, Snapshot, SyncDestinationResponse } from '@/types'
import { summariseSync } from '@/lib/syncSummary'
import RestoreSheet from '@/components/snapshots/RestoreSheet.vue'

// ---------------------------------------------------------------------------
// Types
// ---------------------------------------------------------------------------

interface SnapshotListResponse {
    items: Snapshot[]
    total: number
}

// ---------------------------------------------------------------------------
// State
// ---------------------------------------------------------------------------

const authStore = useAuthStore()

const snapshots = ref<Snapshot[]>([])
const total = ref(0)
const loading = ref(true)
const error = ref<string | null>(null)

// Pagination
const page = ref(1)
const pageSize = 50
const offset = computed(() => (page.value - 1) * pageSize)
const totalPages = computed(() => Math.ceil(total.value / pageSize))

// Filters — empty string means "no filter"
const policyFilter = ref('')
const destinationFilter = ref('')

// Restore sheet
const restoreSheetOpen = ref(false)
const restoreSnapshot = ref<Snapshot | null>(null)

// Sync
const syncLoading = ref(false)
const syncMessage = ref<string | null>(null)

// Delete dialog
const deleteDialogOpen = ref(false)
const snapshotToDelete = ref<Snapshot | null>(null)
const deleteLoading = ref(false)

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

function formatBytes(bytes: number): string {
    if (bytes === 0) return '0 B'
    const k = 1024
    const sizes = ['B', 'KB', 'MB', 'GB', 'TB']
    const i = Math.floor(Math.log(bytes) / Math.log(k))
    return `${parseFloat((bytes / Math.pow(k, i)).toFixed(1))} ${sizes[i]}`
}

function formatDate(iso: string | null): string {
    if (!iso) return '—'
    return new Date(iso).toLocaleString(undefined, {
        dateStyle: 'medium',
        timeStyle: 'short',
    })
}

function abbreviate(id: string | undefined): string {
    if (!id) return '—'
    return id.slice(0, 8)
}

// ---------------------------------------------------------------------------
// Data fetching
// ---------------------------------------------------------------------------

async function fetchSnapshots() {
    loading.value = true
    error.value = null
    try {
        const params = new URLSearchParams({ limit: String(pageSize), offset: String(offset.value) })
        if (policyFilter.value) params.set('policy_id', policyFilter.value)
        if (destinationFilter.value) params.set('destination_id', destinationFilter.value)

        const res = await api<ApiResponse<SnapshotListResponse>>(`/api/v1/snapshots?${params}`)
        snapshots.value = res.data.items
        total.value = res.data.total
    } catch (e: any) {
        error.value = apiErrorMessage(e, 'Failed to load snapshots.')
    } finally {
        loading.value = false
    }
}

async function goToPage(p: number) {
    if (p < 1 || p > totalPages.value) return
    page.value = p
    await fetchSnapshots()
}

async function applyFilters() {
    page.value = 1
    await fetchSnapshots()
}

watch([policyFilter, destinationFilter], applyFilters)

// ---------------------------------------------------------------------------
// Restore
// ---------------------------------------------------------------------------

function openRestoreSheet(snapshot: Snapshot) {
    restoreSnapshot.value = snapshot
    restoreSheetOpen.value = true
}

// ---------------------------------------------------------------------------
// Delete
// ---------------------------------------------------------------------------

function openDeleteDialog(snapshot: Snapshot) {
    snapshotToDelete.value = snapshot
    deleteDialogOpen.value = true
}

async function confirmDelete() {
    if (!snapshotToDelete.value) return
    deleteLoading.value = true
    const deletedId = snapshotToDelete.value.id
    try {
        await api(`/api/v1/snapshots/${deletedId}`, { method: 'DELETE' })
        deleteDialogOpen.value = false
        snapshotToDelete.value = null
        if (snapshots.value.length === 1 && page.value > 1) {
            page.value--
        }
        await fetchSnapshots()
    } catch (e: any) {
        error.value = apiErrorMessage(e, 'Failed to delete snapshot.')
    } finally {
        deleteLoading.value = false
    }
}

// ---------------------------------------------------------------------------
// Sync
// ---------------------------------------------------------------------------

// enabledDestinationIds pages through every destination and keeps the enabled
// ones — the same set the server's periodic sync covers.
async function enabledDestinationIds(): Promise<string[]> {
    const ids: string[] = []
    for (let offset = 0; ; offset += 100) {
        const res = await api<ApiResponse<PaginatedResponse<Destination>>>(
            `/api/v1/destinations?limit=100&offset=${offset}`)
        ids.push(...res.data.items.filter(d => d.enabled).map(d => d.id))
        if (res.data.items.length === 0 || offset + res.data.items.length >= res.data.total) return ids
    }
}

// syncSnapshots re-reads the repository of the filtered destination — or of
// every enabled destination when no filter is set — so snapshots pruned or
// added outside Arkeep are reflected in the list (issue #288). Destinations
// that cannot be synced right now are reported without stopping the others.
async function syncSnapshots() {
    syncLoading.value = true
    error.value = null
    syncMessage.value = null
    const results: SyncDestinationResponse[] = []
    const failures: string[] = []
    try {
        const ids = destinationFilter.value ? [destinationFilter.value] : await enabledDestinationIds()
        for (const id of ids) {
            try {
                const res = await api<ApiResponse<SyncDestinationResponse>>(
                    `/api/v1/destinations/${id}/sync`, { method: 'POST' })
                results.push(res.data)
            } catch (e: any) {
                failures.push(apiErrorMessage(e, 'sync failed'))
            }
        }
        if (results.length > 0) {
            syncMessage.value = summariseSync(results)
            setTimeout(() => { syncMessage.value = null }, 6000)
        }
        if (failures.length > 0) {
            error.value = ids.length === 1
                ? failures[0]
                : `${failures.length} of ${ids.length} destinations could not be synced: ${failures.join('; ')}`
        }
        await fetchSnapshots()
    } catch (e: any) {
        error.value = apiErrorMessage(e, 'Failed to sync snapshots.')
    } finally {
        syncLoading.value = false
    }
}

onMounted(fetchSnapshots)
</script>

<template>
    <div class="flex flex-col gap-6 p-6">

        <!-- Page header -->
        <div class="flex items-center justify-between">
            <div>
                <h1 class="text-2xl font-semibold tracking-tight">Snapshots</h1>
                <p class="mt-1 text-sm text-muted-foreground">
                    Point-in-time restore points created by completed backup jobs.
                </p>
            </div>
            <div class="flex items-center gap-2">
                <Button v-if="authStore.isAdmin" variant="outline" size="sm" :disabled="syncLoading"
                    :title="destinationFilter ? 'Re-read this destination\'s repository' : 'Re-read the repository of every enabled destination'"
                    @click="syncSnapshots">
                    <Loader2 v-if="syncLoading" class="w-4 h-4 mr-1.5 animate-spin" />
                    <RefreshCcw v-else class="w-4 h-4 mr-1.5" />
                    {{ destinationFilter ? 'Sync Destination' : 'Sync All' }}
                </Button>
                <Button variant="outline" size="icon" aria-label="Refresh" :disabled="loading" @click="fetchSnapshots">
                    <RefreshCw class="w-4 h-4" :class="{ 'animate-spin': loading }" />
                </Button>
            </div>
        </div>

        <!-- Error banner -->
        <Alert v-if="error" variant="destructive">
            <AlertDescription>{{ error }}</AlertDescription>
        </Alert>
        <Alert v-if="syncMessage" class="border-emerald-500/30 bg-emerald-500/5 text-emerald-600 dark:text-emerald-400">
            <AlertDescription>{{ syncMessage }}</AlertDescription>
        </Alert>

        <!-- Filter bar -->
        <div class="flex items-center gap-3">
            <AsyncCombobox
                endpoint="/api/v1/policies"
                :model-value="policyFilter"
                placeholder="All policies"
                allow-clear
                class="w-44"
                @update:model-value="policyFilter = $event"
            />
            <AsyncCombobox
                endpoint="/api/v1/destinations"
                :model-value="destinationFilter"
                placeholder="All destinations"
                allow-clear
                class="w-48"
                @update:model-value="destinationFilter = $event"
            />
            <span v-if="!loading" class="text-sm text-muted-foreground">
                {{ total }} snapshot{{ total !== 1 ? 's' : '' }}
            </span>
        </div>

        <!-- Table -->
        <div class="border rounded-md overflow-x-auto">
            <Table>
                <TableHeader>
                    <TableRow>
                        <TableHead>Policy</TableHead>
                        <TableHead>Destination</TableHead>
                        <TableHead>Snapshot ID</TableHead>
                        <TableHead>Added</TableHead>
                        <TableHead>Created</TableHead>
                        <TableHead class="w-13" />
                    </TableRow>
                </TableHeader>

                <TableBody>

                    <!-- Loading skeletons -->
                    <template v-if="loading">
                        <TableRow v-for="n in 5" :key="n">
                            <TableCell v-for="col in 6" :key="col">
                                <Skeleton class="w-full h-4" />
                            </TableCell>
                        </TableRow>
                    </template>

                    <!-- Empty state -->
                    <template v-else-if="snapshots.length === 0">
                        <TableRow>
                            <TableCell colspan="6">
                                <div class="flex flex-col items-center justify-center gap-3 py-16 text-center">
                                    <div class="p-4 rounded-full bg-muted">
                                        <Camera class="w-10 h-10 text-muted-foreground" />
                                    </div>
                                    <div>
                                        <p class="font-medium">No snapshots found</p>
                                        <p class="mt-1 text-sm text-muted-foreground">
                                            Snapshots are created automatically when a backup job succeeds.
                                        </p>
                                    </div>
                                </div>
                            </TableCell>
                        </TableRow>
                    </template>

                    <!-- Data rows -->
                    <template v-else>
                        <TableRow v-for="snapshot in snapshots" :key="snapshot.id">
                            <TableCell class="font-medium">{{ snapshot.policy_name }}</TableCell>
                            <TableCell class="text-muted-foreground">
                                <div class="flex items-center gap-2">
                                    <span>{{ snapshot.destination_name }}</span>
                                    <Badge v-if="snapshot.destination_deleted" variant="outline"
                                        title="This destination was deleted and Arkeep erased its credentials. Restoring this snapshot requires entering them again.">
                                        Deleted
                                    </Badge>
                                </div>
                            </TableCell>
                            <TableCell>
                                <span v-if="snapshot.destination_deleted" class="font-mono text-sm"
                                    title="Browsing is not available because the destination was deleted. You can still restore the whole snapshot.">
                                    {{ abbreviate(snapshot.restic_snapshot_id) }}
                                </span>
                                <RouterLink v-else :to="{ name: 'snapshot-browse', params: { id: snapshot.id } }"
                                    class="font-mono text-sm underline-offset-4 hover:underline"
                                    :title="`Browse snapshot ${snapshot.restic_snapshot_id}`">
                                    {{ abbreviate(snapshot.restic_snapshot_id) }}
                                </RouterLink>
                            </TableCell>
                            <TableCell class="text-sm text-muted-foreground">
                                {{ formatBytes(snapshot.size_bytes) }}
                            </TableCell>
                            <TableCell class="text-sm text-muted-foreground">
                                {{ formatDate(snapshot.created_at) }}
                            </TableCell>

                            <!-- Actions dropdown — only admins can restore/delete -->
                            <TableCell>
                                <DropdownMenu v-if="authStore.isAdmin">
                                    <DropdownMenuTrigger as-child>
                                        <Button variant="ghost" size="icon" class="w-8 h-8">
                                            <MoreHorizontal class="w-4 h-4" />
                                            <span class="sr-only">Open actions</span>
                                        </Button>
                                    </DropdownMenuTrigger>
                                    <DropdownMenuContent align="end">
                                        <DropdownMenuItem @click="openRestoreSheet(snapshot)">
                                            <RotateCcw class="w-4 h-4 mr-2" />
                                            Restore
                                        </DropdownMenuItem>
                                        <DropdownMenuSeparator />
                                        <DropdownMenuItem class="text-destructive focus:text-destructive"
                                            @click="openDeleteDialog(snapshot)">
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
                Showing {{ offset + 1 }}–{{ Math.min(offset + pageSize, total) }} of {{ total }} snapshots
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

    <!-- Restore sheet -->
    <RestoreSheet :open="restoreSheetOpen" :snapshot="restoreSnapshot" @update:open="restoreSheetOpen = $event" />

    <!-- Delete confirmation dialog -->
    <AlertDialog :open="deleteDialogOpen" @update:open="deleteDialogOpen = $event">
        <AlertDialogContent>
            <AlertDialogHeader>
                <AlertDialogTitle>Delete snapshot?</AlertDialogTitle>
                <AlertDialogDescription>
                    <span v-if="snapshotToDelete">
                        Snapshot
                        <span class="font-mono">{{ abbreviate(snapshotToDelete.restic_snapshot_id) }}</span>
                        will be permanently removed. This action cannot be undone.
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