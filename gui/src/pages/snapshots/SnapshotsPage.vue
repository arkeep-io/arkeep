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
    AlertDialog,
    AlertDialogAction,
    AlertDialogCancel,
    AlertDialogContent,
    AlertDialogDescription,
    AlertDialogFooter,
    AlertDialogHeader,
    AlertDialogTitle,
    AlertDialogTrigger,
} from '@/components/ui/alert-dialog'
import { Alert, AlertDescription } from '@/components/ui/alert'
import { Button } from '@/components/ui/button'
import { Skeleton } from '@/components/ui/skeleton'
import { Camera, RefreshCw, Trash2 } from 'lucide-vue-next'
import { api } from '@/services/api'
import type { ApiResponse, Snapshot, Policy, Destination } from '@/types'

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
const deletingId = ref<string | null>(null)

// Filter sentinels: shadcn-vue SelectItem does not accept empty string as value.
const policyFilter = ref<string>('all')
const destinationFilter = ref<string>('all')

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

// formatBytes converts a raw byte count to a human-readable string with the
// most appropriate unit (B, KB, MB, GB, TB).
function formatBytes(bytes: number): string {
    if (bytes === 0) return '0 B'
    const k = 1024
    const sizes = ['B', 'KB', 'MB', 'GB', 'TB']
    const i = Math.floor(Math.log(bytes) / Math.log(k))
    return `${parseFloat((bytes / Math.pow(k, i)).toFixed(1))} ${sizes[i]}`
}

// formatDate returns a locale-formatted date+time string, or an em-dash if
// the value is absent.
function formatDate(iso: string | null): string {
    if (!iso) return '—'
    return new Date(iso).toLocaleString(undefined, {
        dateStyle: 'medium',
        timeStyle: 'short',
    })
}

// abbreviate returns the first 8 characters of a string for compact display
// of long IDs (e.g. restic snapshot hashes).
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
        // Build query params — omit filter keys when set to the sentinel 'all'.
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

// fetchFilterOptions loads the policy and destination lists once on mount so
// the filter selects are populated. Failures are silently swallowed — the
// selects simply remain limited to "All".
async function fetchFilterOptions() {
    try {
        const [pRes, dRes] = await Promise.all([
            api<ApiResponse<PolicyListResponse>>('/api/v1/policies?limit=100'),
            api<ApiResponse<DestinationListResponse>>('/api/v1/destinations?limit=100'),
        ])
        policies.value = pRes.data.items
        destinations.value = dRes.data.items
    } catch {
        // Non-critical — filter options degraded gracefully to "All".
    }
}

// ---------------------------------------------------------------------------
// Delete
// ---------------------------------------------------------------------------

async function handleDelete(id: string) {
    deletingId.value = id
    try {
        await api(`/api/v1/snapshots/${id}`, { method: 'DELETE' })
        // Remove from local list immediately to avoid a full refetch.
        snapshots.value = snapshots.value.filter((s) => s.id !== id)
    } catch (e: any) {
        error.value = e?.message ?? 'Failed to delete snapshot.'
    } finally {
        deletingId.value = null
    }
}

// ---------------------------------------------------------------------------
// Filter change handler — re-fetch with new server-side params
// ---------------------------------------------------------------------------

async function applyFilters() {
    await fetchSnapshots()
}

onMounted(async () => {
    // Load filter options and snapshot list in parallel.
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

            <!-- Policy filter -->
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

            <!-- Destination filter -->
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

            <!-- Result count hint, shown once data is loaded -->
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
                        <TableHead class="w-12" />
                    </TableRow>
                </TableHeader>

                <TableBody>

                    <!-- Loading skeletons — 5 placeholder rows while the API call is in flight -->
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
                                <!-- Monospace abbreviated restic snapshot hash for compact display. -->
                                <span class="font-mono text-sm">{{ abbreviate(snapshot.restic_snapshot_id) }}</span>
                            </TableCell>
                            <TableCell class="text-sm text-muted-foreground">
                                {{ formatBytes(snapshot.size_bytes) }}
                            </TableCell>
                            <TableCell class="text-sm text-muted-foreground">
                                {{ formatDate(snapshot.created_at) }}
                            </TableCell>
                            <TableCell>
                                <!-- Delete action — wrapped in AlertDialog to require explicit confirmation. -->
                                <AlertDialog>
                                    <AlertDialogTrigger as-child>
                                        <Button variant="ghost" size="icon" :disabled="deletingId === snapshot.id"
                                            class="text-muted-foreground hover:text-destructive">
                                            <Trash2 class="w-4 h-4" />
                                        </Button>
                                    </AlertDialogTrigger>
                                    <AlertDialogContent>
                                        <AlertDialogHeader>
                                            <AlertDialogTitle>Delete snapshot?</AlertDialogTitle>
                                            <AlertDialogDescription>
                                                This will permanently remove snapshot
                                                <span class="font-mono">{{ abbreviate(snapshot.restic_snapshot_id)
                                                }}</span>
                                                from the destination. This action cannot be undone.
                                            </AlertDialogDescription>
                                        </AlertDialogHeader>
                                        <AlertDialogFooter>
                                            <AlertDialogCancel>Cancel</AlertDialogCancel>
                                            <AlertDialogAction @click="handleDelete(snapshot.id)">
                                                Delete
                                            </AlertDialogAction>
                                        </AlertDialogFooter>
                                    </AlertDialogContent>
                                </AlertDialog>
                            </TableCell>
                        </TableRow>
                    </template>

                </TableBody>
            </Table>
        </div>

    </div>
</template>