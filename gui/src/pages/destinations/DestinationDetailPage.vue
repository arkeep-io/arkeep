<script setup lang="ts">
import { ref, onMounted } from 'vue'
import { useRoute, useRouter } from 'vue-router'
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
    ArrowLeft,
    RefreshCw,
    PencilLine,
    Trash2,
    Play,
    Loader2,
    CalendarClock,
    AlertTriangle,
} from '@lucide/vue'
import { api } from '@/services/api'
import type { Destination, Job, ApiResponse } from '@/types'
import { statusVariant, statusClass, statusLabel, formatDate, formatBytes } from '@/lib/jobUtils'
import DestinationSheet from '@/components/destinations/DestinationSheet.vue'

defineOptions({ inheritAttrs: false })

// ---------------------------------------------------------------------------
// Route / Router
// ---------------------------------------------------------------------------

const route = useRoute()
const router = useRouter()
const authStore = useAuthStore()
const destinationId = route.params.id as string

// ---------------------------------------------------------------------------
// State — destination
// ---------------------------------------------------------------------------

const destination = ref<Destination | null>(null)
const loading = ref(true)
const error = ref<string | null>(null)

// ---------------------------------------------------------------------------
// State — jobs (retention runs + backups touching this destination)
// ---------------------------------------------------------------------------

interface JobListResponse { items: Job[]; total: number }

const jobs = ref<Job[]>([])
const jobsLoading = ref(true)

// ---------------------------------------------------------------------------
// Edit / Delete / Trigger
// ---------------------------------------------------------------------------

const editSheetOpen = ref(false)
const deleteDialogOpen = ref(false)
const deleteLoading = ref(false)
const triggerLoading = ref(false)

// ---------------------------------------------------------------------------
// Data fetching
// ---------------------------------------------------------------------------

async function fetchDestination() {
    loading.value = true
    error.value = null
    try {
        const res = await api<ApiResponse<Destination>>(`/api/v1/destinations/${destinationId}`)
        destination.value = res.data
    } catch (e: any) {
        error.value = e?.message ?? 'Failed to load destination'
    } finally {
        loading.value = false
    }
}

async function fetchJobs() {
    jobsLoading.value = true
    try {
        const res = await api<ApiResponse<JobListResponse>>(
            `/api/v1/jobs?destination_id=${destinationId}&limit=10&offset=0`
        )
        jobs.value = res.data.items
    } catch {
        // Non-fatal — jobs section stays empty
    } finally {
        jobsLoading.value = false
    }
}

// ---------------------------------------------------------------------------
// Actions
// ---------------------------------------------------------------------------

async function triggerRetention() {
    triggerLoading.value = true
    error.value = null
    try {
        await api(`/api/v1/destinations/${destinationId}/trigger-retention`, { method: 'POST' })
        setTimeout(fetchJobs, 800)
    } catch (e: any) {
        error.value = e?.data?.error?.message ?? e?.message ?? 'Failed to trigger retention'
    } finally {
        triggerLoading.value = false
    }
}

async function confirmDelete() {
    deleteLoading.value = true
    try {
        await api(`/api/v1/destinations/${destinationId}`, { method: 'DELETE' })
        router.push('/destinations')
    } catch (e: any) {
        error.value = e?.data?.error?.message ?? e?.message ?? 'Failed to delete destination'
    } finally {
        deleteLoading.value = false
        deleteDialogOpen.value = false
    }
}

function onSaved() {
    fetchDestination()
}

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

function scheduleLabel(cron: string): string {
    if (!cron) return 'Not scheduled'
    const presets: Record<string, string> = {
        '0 * * * *': 'Hourly',
        '0 2 * * *': 'Daily at 02:00',
        '0 0 * * *': 'Daily at midnight',
        '0 2 * * 0': 'Weekly (Sun)',
        '0 2 * * 1': 'Weekly (Mon)',
        '0 2 1 * *': 'Monthly',
    }
    return presets[cron] ?? cron
}

// ---------------------------------------------------------------------------
// Lifecycle
// ---------------------------------------------------------------------------

onMounted(() => Promise.all([fetchDestination(), fetchJobs()]))
</script>

<template>
    <div class="flex flex-col gap-6 p-6">

        <!-- ── Header ─────────────────────────────────────────────────────── -->
        <div class="flex items-start justify-between gap-4">
            <div class="flex items-center gap-3">
                <Button variant="ghost" size="icon" @click="router.push('/destinations')">
                    <ArrowLeft class="w-4 h-4" />
                </Button>
                <div>
                    <div v-if="loading" class="flex flex-col gap-1.5">
                        <Skeleton class="w-48 h-6" />
                        <Skeleton class="w-32 h-4" />
                    </div>
                    <template v-else-if="destination">
                        <div class="flex items-center gap-2.5 flex-wrap">
                            <h1 class="text-2xl font-semibold tracking-tight">{{ destination.name }}</h1>
                            <Badge :variant="destination.enabled ? 'default' : 'secondary'">
                                {{ destination.enabled ? 'Enabled' : 'Disabled' }}
                            </Badge>
                            <Badge v-if="destination.append_only" variant="outline">Append-only</Badge>
                            <Badge v-if="destination.retention_needs_review" variant="destructive">
                                Retention needs reconfiguration
                            </Badge>
                        </div>
                        <p class="mt-0.5 text-sm text-muted-foreground">
                            Type: <span class="font-medium text-foreground uppercase">{{ destination.type }}</span>
                            <span v-if="destination.policy_count > 0">
                                · Used by {{ destination.policy_count }} polic{{ destination.policy_count === 1 ? 'y' : 'ies' }}
                            </span>
                        </p>
                    </template>
                </div>
            </div>

            <!-- Actions -->
            <div v-if="!loading && destination" class="flex items-center gap-2">
                <Button variant="outline" size="icon" :disabled="loading" @click="fetchDestination(); fetchJobs()">
                    <RefreshCw class="w-4 h-4" />
                </Button>
                <Button v-if="authStore.isAdmin && destination.retention_enabled && !destination.append_only"
                    variant="outline" size="sm" :disabled="triggerLoading" @click="triggerRetention">
                    <Loader2 v-if="triggerLoading" class="w-4 h-4 mr-1.5 animate-spin" />
                    <Play v-else class="w-4 h-4 mr-1.5" />
                    Run Retention Now
                </Button>
                <Button variant="outline" size="sm" @click="editSheetOpen = true">
                    <PencilLine class="w-4 h-4 mr-1.5" />
                    Edit
                </Button>
                <Button v-if="authStore.isAdmin" variant="outline" size="sm"
                    class="text-destructive hover:text-destructive border-destructive/30 hover:bg-destructive/5"
                    @click="deleteDialogOpen = true">
                    <Trash2 class="w-4 h-4 mr-1.5" />
                    Delete
                </Button>
            </div>
        </div>

        <!-- Error banner -->
        <Alert v-if="error" variant="destructive">
            <AlertDescription>{{ error }}</AlertDescription>
        </Alert>

        <!-- ── Info cards ──────────────────────────────────────────────────── -->
        <div v-if="loading" class="grid grid-cols-2 gap-4 sm:grid-cols-3">
            <div v-for="n in 3" :key="n" class="p-4 border rounded-md">
                <Skeleton class="w-16 h-3 mb-2" />
                <Skeleton class="w-24 h-4" />
            </div>
        </div>
        <div v-else-if="destination" class="grid grid-cols-2 gap-4 sm:grid-cols-3">
            <div class="p-4 border rounded-md">
                <p class="text-xs text-muted-foreground uppercase tracking-wide mb-1">Repo Size</p>
                <p class="text-sm font-medium">{{ formatBytes(destination.repo_size_bytes) }}</p>
            </div>
            <div class="p-4 border rounded-md">
                <p class="text-xs text-muted-foreground uppercase tracking-wide mb-1">Retention Schedule</p>
                <p class="text-sm font-mono font-medium">
                    {{ destination.append_only || !destination.retention_enabled ? '—' : scheduleLabel(destination.retention_schedule) }}
                </p>
            </div>
            <div class="p-4 border rounded-md">
                <p class="text-xs text-muted-foreground uppercase tracking-wide mb-1">Created</p>
                <p class="text-sm font-medium">{{ formatDate(destination.created_at) }}</p>
            </div>
        </div>

        <!-- ── Retention ───────────────────────────────────────────────────── -->
        <div v-if="!loading && destination" class="border rounded-md p-4 flex flex-col gap-3">
            <h2 class="text-sm font-semibold">Retention</h2>

            <Alert v-if="destination.append_only">
                <AlertDescription class="text-sm text-muted-foreground">
                    This destination is append-only — retention cannot run here (`forget --prune`
                    can never succeed against storage that rejects deletes).
                </AlertDescription>
            </Alert>

            <template v-else-if="destination.retention_needs_review">
                <Alert variant="destructive">
                    <AlertTriangle class="h-4 w-4" />
                    <AlertDescription class="text-xs">
                        This destination was shared by multiple policies with different retention
                        settings before this update — retention was left disabled. Edit it to
                        configure retention explicitly.
                    </AlertDescription>
                </Alert>
                <Button variant="outline" size="sm" class="self-start" @click="editSheetOpen = true">
                    <PencilLine class="w-4 h-4 mr-1.5" />
                    Configure Retention
                </Button>
            </template>

            <template v-else-if="!destination.retention_enabled">
                <p class="text-sm text-muted-foreground">Retention is not configured for this destination.</p>
            </template>

            <template v-else>
                <div class="grid grid-cols-2 gap-x-4 gap-y-2 sm:grid-cols-3">
                    <div class="flex items-center justify-between">
                        <span class="text-xs text-muted-foreground">Last</span>
                        <span class="text-sm font-mono font-medium">{{ destination.retention_last }}</span>
                    </div>
                    <div class="flex items-center justify-between">
                        <span class="text-xs text-muted-foreground">Hourly</span>
                        <span class="text-sm font-mono font-medium">{{ destination.retention_hourly }}</span>
                    </div>
                    <div class="flex items-center justify-between">
                        <span class="text-xs text-muted-foreground">Daily</span>
                        <span class="text-sm font-mono font-medium">{{ destination.retention_daily }}</span>
                    </div>
                    <div class="flex items-center justify-between">
                        <span class="text-xs text-muted-foreground">Weekly</span>
                        <span class="text-sm font-mono font-medium">{{ destination.retention_weekly }}</span>
                    </div>
                    <div class="flex items-center justify-between">
                        <span class="text-xs text-muted-foreground">Monthly</span>
                        <span class="text-sm font-mono font-medium">{{ destination.retention_monthly }}</span>
                    </div>
                    <div class="flex items-center justify-between">
                        <span class="text-xs text-muted-foreground">Yearly</span>
                        <span class="text-sm font-mono font-medium">{{ destination.retention_yearly }}</span>
                    </div>
                </div>
                <p class="text-xs text-muted-foreground">
                    Agent: <span class="font-medium text-foreground">{{ destination.retention_agent_name || '—' }}</span>
                </p>
            </template>
        </div>
        <div v-else-if="loading" class="border rounded-md p-4 flex flex-col gap-3">
            <Skeleton class="w-24 h-4" />
            <Skeleton class="w-full h-8" />
        </div>

        <!-- ── Recent Jobs (backups + retention runs) ─────────────────────── -->
        <div class="flex flex-col gap-3">
            <div class="flex items-center justify-between">
                <h2 class="text-sm font-semibold">Recent Jobs</h2>
                <Button variant="ghost" size="icon" class="w-7 h-7" :disabled="jobsLoading" @click="fetchJobs">
                    <RefreshCw class="w-3.5 h-3.5" :class="{ 'animate-spin': jobsLoading }" />
                </Button>
            </div>

            <div class="border rounded-md">
                <Table>
                    <TableHeader>
                        <TableRow>
                            <TableHead>Type</TableHead>
                            <TableHead>Status</TableHead>
                            <TableHead>Started</TableHead>
                            <TableHead>Finished</TableHead>
                        </TableRow>
                    </TableHeader>
                    <TableBody>
                        <!-- Loading -->
                        <template v-if="jobsLoading">
                            <TableRow v-for="n in 3" :key="n">
                                <TableCell v-for="col in 4" :key="col">
                                    <Skeleton class="w-full h-4" />
                                </TableCell>
                            </TableRow>
                        </template>

                        <!-- Empty -->
                        <template v-else-if="jobs.length === 0">
                            <TableRow>
                                <TableCell colspan="4">
                                    <div class="flex flex-col items-center justify-center gap-3 py-12 text-center">
                                        <div class="p-3 rounded-full bg-muted">
                                            <CalendarClock class="w-6 h-6 text-muted-foreground" />
                                        </div>
                                        <div>
                                            <p class="font-medium text-sm">No jobs yet</p>
                                            <p class="mt-1 text-xs text-muted-foreground">
                                                Backups and retention sweeps against this destination will appear here.
                                            </p>
                                        </div>
                                    </div>
                                </TableCell>
                            </TableRow>
                        </template>

                        <!-- Rows -->
                        <template v-else>
                            <TableRow v-for="job in jobs" :key="job.id" class="cursor-pointer"
                                @click="router.push(`/jobs/${job.id}`)">
                                <TableCell class="text-sm text-muted-foreground capitalize">{{ job.type }}</TableCell>
                                <TableCell>
                                    <Badge :variant="statusVariant(job.status)" :class="statusClass(job.status)">
                                        {{ statusLabel(job.status) }}
                                    </Badge>
                                </TableCell>
                                <TableCell class="text-sm text-muted-foreground">
                                    {{ formatDate(job.started_at) }}
                                </TableCell>
                                <TableCell class="text-sm text-muted-foreground">
                                    {{ formatDate(job.ended_at) }}
                                </TableCell>
                            </TableRow>
                        </template>
                    </TableBody>
                </Table>
                <div class="flex justify-end px-4 py-2 border-t">
                    <RouterLink
                        :to="{ name: 'jobs', query: { destination_id: destinationId } }"
                        class="text-sm text-muted-foreground hover:text-foreground transition-colors"
                    >
                        View all jobs →
                    </RouterLink>
                </div>
            </div>
        </div>

    </div>

    <!-- Edit sheet -->
    <DestinationSheet v-if="destination" :destination="destination" :open="editSheetOpen"
        @update:open="editSheetOpen = $event" @saved="onSaved" />

    <!-- Delete dialog -->
    <AlertDialog :open="deleteDialogOpen" @update:open="deleteDialogOpen = $event">
        <AlertDialogContent>
            <AlertDialogHeader>
                <AlertDialogTitle>Delete destination?</AlertDialogTitle>
                <AlertDialogDescription>
                    <span v-if="destination">
                        <strong>{{ destination.name }}</strong> will be permanently deleted.
                        This does not delete the underlying repository data.
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
