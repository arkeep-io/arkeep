<script setup lang="ts">
import { ref, computed, onMounted } from 'vue'
import { useRoute, useRouter } from 'vue-router'
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
import { Separator } from '@/components/ui/separator'
import {
    ArrowLeft,
    RefreshCw,
    PencilLine,
    Trash2,
    Play,
    Loader2,
    FolderOpen,
    Container,
    CalendarClock,
} from 'lucide-vue-next'
import { api } from '@/services/api'
import type { Policy, Job, ApiResponse } from '@/types'
import PolicySheet from '@/components/policies/PolicySheet.vue'

// ---------------------------------------------------------------------------
// Route / Router
// ---------------------------------------------------------------------------

const route = useRoute()
const router = useRouter()
const policyId = route.params.id as string

// ---------------------------------------------------------------------------
// State — policy
// ---------------------------------------------------------------------------

const policy = ref<Policy | null>(null)
const loading = ref(true)
const error = ref<string | null>(null)

// ---------------------------------------------------------------------------
// State — jobs
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

async function fetchPolicy() {
    loading.value = true
    error.value = null
    try {
        const res = await api<ApiResponse<Policy>>(`/api/v1/policies/${policyId}`)
        policy.value = res.data
    } catch (e: any) {
        error.value = e?.message ?? 'Failed to load policy'
    } finally {
        loading.value = false
    }
}

async function fetchJobs() {
    jobsLoading.value = true
    try {
        const res = await api<ApiResponse<JobListResponse>>(
            `/api/v1/policies/${policyId}/jobs?limit=10&offset=0`
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

async function triggerPolicy() {
    triggerLoading.value = true
    error.value = null
    try {
        await api(`/api/v1/policies/${policyId}/trigger`, { method: 'POST' })
        // Refresh jobs after a short delay to pick up the new pending job
        setTimeout(fetchJobs, 800)
    } catch (e: any) {
        error.value = e?.message ?? 'Failed to trigger policy'
    } finally {
        triggerLoading.value = false
    }
}

async function confirmDelete() {
    deleteLoading.value = true
    try {
        await api(`/api/v1/policies/${policyId}`, { method: 'DELETE' })
        router.push('/policies')
    } catch (e: any) {
        error.value = e?.message ?? 'Failed to delete policy'
    } finally {
        deleteLoading.value = false
        deleteDialogOpen.value = false
    }
}

function onSaved() {
    fetchPolicy()
}

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

/**
 * Parses the sources JSON string into an array of source objects.
 * Returns an empty array on parse failure so the UI degrades gracefully.
 */
const parsedSources = computed(() => {
    if (!policy.value?.sources) return []
    try {
        const raw = typeof policy.value.sources === 'string'
            ? JSON.parse(policy.value.sources)
            : policy.value.sources
        return raw as { type: string; path: string; label?: string }[]
    } catch {
        return []
    }
})

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

function formatDate(date: string | null | undefined): string {
    if (!date) return '—'
    return new Date(date).toLocaleString()
}

function jobStatusVariant(status: string): 'default' | 'secondary' | 'outline' | 'destructive' {
    switch (status) {
        case 'succeeded': return 'default'
        case 'running': return 'outline'
        case 'failed': return 'destructive'
        default: return 'secondary'
    }
}

// ---------------------------------------------------------------------------
// Lifecycle
// ---------------------------------------------------------------------------

onMounted(() => Promise.all([fetchPolicy(), fetchJobs()]))
</script>

<template>
    <div class="flex flex-col gap-6 p-6">

        <!-- ── Header ─────────────────────────────────────────────────────── -->
        <div class="flex items-start justify-between gap-4">
            <div class="flex items-center gap-3">
                <Button variant="ghost" size="icon" @click="router.push('/policies')">
                    <ArrowLeft class="w-4 h-4" />
                </Button>
                <div>
                    <div v-if="loading" class="flex flex-col gap-1.5">
                        <Skeleton class="w-48 h-6" />
                        <Skeleton class="w-32 h-4" />
                    </div>
                    <template v-else-if="policy">
                        <div class="flex items-center gap-2.5">
                            <h1 class="text-2xl font-semibold tracking-tight">{{ policy.name }}</h1>
                            <Badge :variant="policy.enabled ? 'default' : 'secondary'">
                                {{ policy.enabled ? 'Enabled' : 'Disabled' }}
                            </Badge>
                        </div>
                        <p class="mt-0.5 text-sm text-muted-foreground">
                            Agent: <span class="font-medium text-foreground">{{ policy.agent_name || '—' }}</span>
                        </p>
                    </template>
                </div>
            </div>

            <!-- Actions -->
            <div v-if="!loading && policy" class="flex items-center gap-2">
                <Button variant="outline" size="icon" :disabled="loading" @click="fetchPolicy(); fetchJobs()">
                    <RefreshCw class="w-4 h-4" />
                </Button>
                <Button variant="outline" size="sm" :disabled="triggerLoading" @click="triggerPolicy">
                    <Loader2 v-if="triggerLoading" class="w-4 h-4 mr-1.5 animate-spin" />
                    <Play v-else class="w-4 h-4 mr-1.5" />
                    Run Now
                </Button>
                <Button variant="outline" size="sm" @click="editSheetOpen = true">
                    <PencilLine class="w-4 h-4 mr-1.5" />
                    Edit
                </Button>
                <Button variant="outline" size="sm"
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
        <div v-if="loading" class="grid grid-cols-2 gap-4 sm:grid-cols-4">
            <div v-for="n in 4" :key="n" class="p-4 border rounded-md">
                <Skeleton class="w-16 h-3 mb-2" />
                <Skeleton class="w-24 h-4" />
            </div>
        </div>
        <div v-else-if="policy" class="grid grid-cols-2 gap-4 sm:grid-cols-4">
            <div class="p-4 border rounded-md">
                <p class="text-xs text-muted-foreground uppercase tracking-wide mb-1">Schedule</p>
                <p class="text-sm font-mono font-medium">{{ scheduleLabel(policy.schedule) }}</p>
            </div>
            <div class="p-4 border rounded-md">
                <p class="text-xs text-muted-foreground uppercase tracking-wide mb-1">Last Run</p>
                <p class="text-sm font-medium">{{ formatDate(policy.last_run_at) }}</p>
            </div>
            <div class="p-4 border rounded-md">
                <p class="text-xs text-muted-foreground uppercase tracking-wide mb-1">Next Run</p>
                <p class="text-sm font-medium">{{ formatDate(policy.next_run_at) }}</p>
            </div>
            <div class="p-4 border rounded-md">
                <p class="text-xs text-muted-foreground uppercase tracking-wide mb-1">Created</p>
                <p class="text-sm font-medium">{{ formatDate(policy.created_at) }}</p>
            </div>
        </div>

        <!-- ── Sources + Retention + Destinations ─────────────────────────── -->
        <div v-if="!loading && policy" class="grid grid-cols-1 gap-4 sm:grid-cols-2">

            <!-- Sources -->
            <div class="border rounded-md p-4 flex flex-col gap-3">
                <h2 class="text-sm font-semibold">Sources</h2>
                <div v-if="parsedSources.length === 0" class="text-sm text-muted-foreground">No sources configured.
                </div>
                <div v-else class="flex flex-col gap-2">
                    <div v-for="(src, idx) in parsedSources" :key="idx"
                        class="flex items-start gap-2.5 rounded-md bg-muted/50 px-3 py-2">
                        <Container v-if="src.type === 'docker-volume'"
                            class="w-4 h-4 mt-0.5 shrink-0 text-muted-foreground" />
                        <FolderOpen v-else class="w-4 h-4 mt-0.5 shrink-0 text-muted-foreground" />
                        <div class="min-w-0">
                            <p class="text-sm font-mono truncate">{{ src.path }}</p>
                            <p v-if="src.label" class="text-xs text-muted-foreground">{{ src.label }}</p>
                            <p v-else class="text-xs text-muted-foreground">{{ src.type }}</p>
                        </div>
                    </div>
                </div>
            </div>

            <!-- Retention -->
            <div class="border rounded-md p-4 flex flex-col gap-3">
                <h2 class="text-sm font-semibold">Retention</h2>
                <div class="grid grid-cols-2 gap-x-4 gap-y-2">
                    <div class="flex items-center justify-between">
                        <span class="text-xs text-muted-foreground">Daily</span>
                        <span class="text-sm font-mono font-medium">{{ policy.retention_daily }}</span>
                    </div>
                    <div class="flex items-center justify-between">
                        <span class="text-xs text-muted-foreground">Weekly</span>
                        <span class="text-sm font-mono font-medium">{{ policy.retention_weekly }}</span>
                    </div>
                    <div class="flex items-center justify-between">
                        <span class="text-xs text-muted-foreground">Monthly</span>
                        <span class="text-sm font-mono font-medium">{{ policy.retention_monthly }}</span>
                    </div>
                    <div class="flex items-center justify-between">
                        <span class="text-xs text-muted-foreground">Yearly</span>
                        <span class="text-sm font-mono font-medium">{{ policy.retention_yearly }}</span>
                    </div>
                </div>

                <Separator />

                <!-- Destinations -->
                <h2 class="text-sm font-semibold">Destinations</h2>
                <div v-if="!policy.destinations || policy.destinations.length === 0"
                    class="text-sm text-muted-foreground">No
                    destinations configured.</div>
                <div v-else class="flex flex-col gap-1.5">
                    <div v-for="dest in policy.destinations" :key="dest.destination_id"
                        class="flex items-center gap-2 rounded-md bg-muted/50 px-3 py-1.5">
                        <span class="text-xs font-mono text-muted-foreground w-5 shrink-0">
                            {{ dest.priority }}.
                        </span>
                        <span class="text-sm flex-1 truncate">
                            {{ dest.destination_name || dest.destination_id }}
                        </span>
                    </div>
                </div>
            </div>
        </div>

        <!-- Skeletons for sources/retention while loading -->
        <div v-else-if="loading" class="grid grid-cols-1 gap-4 sm:grid-cols-2">
            <div v-for="n in 2" :key="n" class="border rounded-md p-4 flex flex-col gap-3">
                <Skeleton class="w-24 h-4" />
                <Skeleton class="w-full h-8" />
                <Skeleton class="w-full h-8" />
            </div>
        </div>

        <!-- ── Recent Jobs ─────────────────────────────────────────────────── -->
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
                            <TableHead>Status</TableHead>
                            <TableHead>Trigger</TableHead>
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
                                                Run the policy manually or wait for the next scheduled run.
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
                                <TableCell>
                                    <Badge :variant="jobStatusVariant(job.status)">
                                        {{ job.status }}
                                    </Badge>
                                </TableCell>
                                <TableCell class="text-sm text-muted-foreground capitalize">
                                    {{ job.triggered_by }}
                                </TableCell>
                                <TableCell class="text-sm text-muted-foreground">
                                    {{ formatDate(job.started_at) }}
                                </TableCell>
                                <TableCell class="text-sm text-muted-foreground">
                                    {{ formatDate(job.finished_at) }}
                                </TableCell>
                            </TableRow>
                        </template>
                    </TableBody>
                </Table>
            </div>
        </div>

    </div>

    <!-- Edit sheet -->
    <PolicySheet v-if="policy" :policy="policy" :open="editSheetOpen" @update:open="editSheetOpen = $event"
        @saved="onSaved" />

    <!-- Delete dialog -->
    <AlertDialog :open="deleteDialogOpen" @update:open="deleteDialogOpen = $event">
        <AlertDialogContent>
            <AlertDialogHeader>
                <AlertDialogTitle>Delete policy?</AlertDialogTitle>
                <AlertDialogDescription>
                    <span v-if="policy">
                        <strong>{{ policy.name }}</strong> will be permanently deleted.
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