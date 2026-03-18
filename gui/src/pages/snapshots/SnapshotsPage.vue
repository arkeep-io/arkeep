<script setup lang="ts">
import { ref, onMounted } from 'vue'
import {
    Table,
    TableBody,
    TableCell,
    TableHead,
    TableHeader,
    TableRow,
} from '@/components/ui/table'
import {
    Select,
    SelectContent,
    SelectItem,
    SelectTrigger,
    SelectValue,
} from '@/components/ui/select'
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
import { Button } from '@/components/ui/button'
import { Skeleton } from '@/components/ui/skeleton'
import { Camera, MoreHorizontal, RefreshCw, RotateCcw, Trash2 } from 'lucide-vue-next'
import { api } from '@/services/api'
import type { ApiResponse, Snapshot, Policy, Destination } from '@/types'
import RestoreSheet from '@/components/snapshots/RestoreSheet.vue'

// ---------------------------------------------------------------------------
// Types
// ---------------------------------------------------------------------------

interface SnapshotListResponse {
    items: Snapshot[]
    total: number
}

interface PolicyListResponse {
    items: Policy[]
    total: number
}

interface DestinationListResponse {
    items: Destination[]
    total: number
}

// ---------------------------------------------------------------------------
// State
// ---------------------------------------------------------------------------

const snapshots = ref<Snapshot[]>([])
const policies = ref<Policy[]>([])
const destinations = ref<Destination[]>([])

const loading = ref(true)
const error = ref<string | null>(null)

// Filter sentinels: shadcn-vue SelectItem does not accept empty string as value.
const policyFilter = ref<string>('all')
const destinationFilter = ref<string>('all')

// Restore sheet
const restoreSheetOpen = ref(false)
const restoreSnapshot = ref<Snapshot | null>(null)

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
        const params = new URLSearchParams({ limit: '50' })
        if (policyFilter.value !== 'all') params.set('policy_id', policyFilter.value)
        if (destinationFilter.value !== 'all') params.set('destination_id', destinationFilter.value)

        const res = await api<ApiResponse<SnapshotListResponse>>(`/api/v1/snapshots?${params}`)
        snapshots.value = res.data.items
    } catch (e: any) {
        error.value = e?.message ?? 'Failed to load snapshots.'
    } finally {
        loading.value = false
    }
}

async function fetchFilterOptions() {
    try {
        const [pRes, dRes] = await Promise.all([
            api<ApiResponse<PolicyListResponse>>('/api/v1/policies?limit=100'),
            api<ApiResponse<DestinationListResponse>>('/api/v1/destinations?limit=100'),
        ])
        policies.value = pRes.data.items
        destinations.value = dRes.data.items
    } catch {
        // Non-critical — filter options degrade gracefully to "All".
    }
}

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
    try {
        await api(`/api/v1/snapshots/${snapshotToDelete.value.id}`, { method: 'DELETE' })
        deleteDialogOpen.value = false
        snapshotToDelete.value = null
        snapshots.value = snapshots.value.filter((s) => s.id !== snapshotToDelete.value?.id)
        await fetchSnapshots()
    } catch (e: any) {
        error.value = e?.message ?? 'Failed to delete snapshot.'
    } finally {
        deleteLoading.value = false
    }
}

async function applyFilters() {
    await fetchSnapshots()
}

onMounted(async () => {
    await Promise.all([fetchFilterOptions(), fetchSnapshots()])
})
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
                <Button variant="outline" size="icon" :disabled="loading" @click="fetchSnapshots">
                    <RefreshCw class="w-4 h-4" :class="{ 'animate-spin': loading }" />
                </Button>
            </div>
        </div>

        <!-- Error banner -->
        <Alert v-if="error" variant="destructive">
            <AlertDescription>{{ error }}</AlertDescription>
        </Alert>

        <!-- Filter bar -->
        <div class="flex items-center gap-3">

            <Select v-model="policyFilter" @update:model-value="applyFilters">
                <SelectTrigger class="w-44">
                    <SelectValue placeholder="All policies" />
                </SelectTrigger>
                <SelectContent>
                    <SelectItem value="all">All policies</SelectItem>
                    <SelectItem v-for="p in policies" :key="p.id" :value="p.id">
                        {{ p.name }}
                    </SelectItem>
                </SelectContent>
            </Select>

            <Select v-model="destinationFilter" @update:model-value="applyFilters">
                <SelectTrigger class="w-48">
                    <SelectValue placeholder="All destinations" />
                </SelectTrigger>
                <SelectContent>
                    <SelectItem value="all">All destinations</SelectItem>
                    <SelectItem v-for="d in destinations" :key="d.id" :value="d.id">
                        {{ d.name }}
                    </SelectItem>
                </SelectContent>
            </Select>

            <span v-if="!loading" class="text-sm text-muted-foreground">
                {{ snapshots.length }} snapshot{{ snapshots.length !== 1 ? 's' : '' }}
            </span>

        </div>

        <!-- Table -->
        <div class="border rounded-md">
            <Table>
                <TableHeader>
                    <TableRow>
                        <TableHead>Policy</TableHead>
                        <TableHead>Destination</TableHead>
                        <TableHead>Snapshot ID</TableHead>
                        <TableHead>Size</TableHead>
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
                                        <Camera class="w-8 h-8 text-muted-foreground" />
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
                            <TableCell class="text-muted-foreground">{{ snapshot.destination_name }}</TableCell>
                            <TableCell>
                                <span class="font-mono text-sm">{{ abbreviate(snapshot.restic_snapshot_id) }}</span>
                            </TableCell>
                            <TableCell class="text-sm text-muted-foreground">
                                {{ formatBytes(snapshot.size_bytes) }}
                            </TableCell>
                            <TableCell class="text-sm text-muted-foreground">
                                {{ formatDate(snapshot.created_at) }}
                            </TableCell>

                            <!-- Actions dropdown -->
                            <TableCell>
                                <DropdownMenu>
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
                <AlertDialogAction class="bg-destructive text-destructive-foreground hover:bg-destructive/90"
                    :disabled="deleteLoading" @click="confirmDelete">
                    {{ deleteLoading ? 'Deleting…' : 'Delete' }}
                </AlertDialogAction>
            </AlertDialogFooter>
        </AlertDialogContent>
    </AlertDialog>
</template>