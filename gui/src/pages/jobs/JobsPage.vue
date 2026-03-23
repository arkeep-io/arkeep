<script setup lang="ts">
import { ref, onMounted, computed } from 'vue'
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
    Select,
    SelectContent,
    SelectItem,
    SelectTrigger,
    SelectValue,
} from '@/components/ui/select'
import { Alert, AlertDescription } from '@/components/ui/alert'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Skeleton } from '@/components/ui/skeleton'
import { BriefcaseBusiness, RefreshCw } from 'lucide-vue-next'
import { api } from '@/services/api'
import type { ApiResponse, Job, JobStatus, JobType } from '@/types'

// ---------------------------------------------------------------------------
// Types
// ---------------------------------------------------------------------------

interface JobListResponse {
    items: Job[]
    total: number
}

// ---------------------------------------------------------------------------
// State
// ---------------------------------------------------------------------------

const router = useRouter()

const jobs = ref<Job[]>([])
const total = ref(0)
const loading = ref(true)
const error = ref<string | null>(null)

// Filter: 'all' is a sentinel value meaning no filter applied.
// SelectItem does not accept empty string as a value in shadcn-vue.
const statusFilter = ref<JobStatus | 'all'>('all')
const typeFilter = ref<JobType | 'all'>('all')

// ---------------------------------------------------------------------------
// Derived list
// ---------------------------------------------------------------------------

// Client-side filter on the already-fetched jobs. The API supports server-side
// filtering too, but since we load the last 50 jobs in one shot, filtering
// locally avoids an extra round-trip on every select change.
const filteredJobs = computed(() => {
    let result = jobs.value
    if (statusFilter.value !== 'all') result = result.filter((j) => j.status === statusFilter.value)
    if (typeFilter.value !== 'all') result = result.filter((j) => j.type === typeFilter.value)
    return result
})

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

// statusVariant maps a JobStatus to the appropriate shadcn Badge variant.
function statusVariant(status: string): 'default' | 'secondary' | 'destructive' | 'outline' {
    switch (status) {
        case 'succeeded': return 'outline'
        case 'running': return 'outline'
        case 'failed': return 'destructive'
        case 'pending': return 'outline'
        default: return 'secondary'
    }
}

function statusClass(status: string): string {
    switch (status) {
        case 'succeeded': return 'bg-green-500/10 text-green-700 dark:text-green-400 border-green-500/20'
        case 'running': return 'bg-blue-500/10 text-blue-700 dark:text-blue-400 border-blue-500/20'
        case 'pending': return 'bg-amber-500/10 text-amber-700 dark:text-amber-400 border-amber-500/20'
        default: return ''
    }
}

// statusLabel returns a capitalised display string for a job status.
function statusLabel(status: string): string {
    return status.charAt(0).toUpperCase() + status.slice(1)
}

// formatDate returns a locale date+time string, or a dash if the value is null.
function formatDate(iso: string | null): string {
    if (!iso) return '—'
    return new Date(iso).toLocaleString(undefined, {
        dateStyle: 'medium',
        timeStyle: 'short',
    })
}

// formatDuration returns a human-readable duration between two ISO timestamps.
// If the end timestamp is absent (job still running), returns "—".
function formatDuration(startedAt: string | null, finishedAt: string | null): string {
    if (!startedAt || !finishedAt) return '—'
    const ms = new Date(finishedAt).getTime() - new Date(startedAt).getTime()
    if (ms < 0) return '—'
    const s = Math.floor(ms / 1000)
    if (s < 60) return `${s}s`
    const m = Math.floor(s / 60)
    if (m < 60) return `${m}m ${s % 60}s`
    const h = Math.floor(m / 60)
    return `${h}h ${m % 60}m`
}

// ---------------------------------------------------------------------------
// Data fetching
// ---------------------------------------------------------------------------

async function fetchJobs() {
    loading.value = true
    error.value = null
    try {
        // Load the 50 most-recent jobs regardless of status. Client-side filtering
        // handles the status select without additional API calls.
        const res = await api<ApiResponse<JobListResponse>>('/api/v1/jobs?limit=50')
        jobs.value = res.data.items
        total.value = res.data.total
    } catch (e: any) {
        error.value = e?.message ?? 'Failed to load jobs.'
    } finally {
        loading.value = false
    }
}

onMounted(fetchJobs)
</script>

<template>
    <div class="flex flex-col gap-6 p-6">

        <!-- Page header -->
        <div class="flex items-center justify-between">
            <div>
                <h1 class="text-2xl font-semibold tracking-tight">Jobs</h1>
                <p class="mt-1 text-sm text-muted-foreground">
                    Recent backup executions across all policies.
                </p>
            </div>
            <div class="flex items-center gap-2">
                <Button variant="outline" size="icon" aria-label="Refresh" :disabled="loading" @click="fetchJobs">
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
            <Select v-model="statusFilter">
                <SelectTrigger class="w-40">
                    <SelectValue placeholder="All statuses" />
                </SelectTrigger>
                <SelectContent>
                    <SelectItem value="all">All statuses</SelectItem>
                    <SelectItem value="pending">Pending</SelectItem>
                    <SelectItem value="running">Running</SelectItem>
                    <SelectItem value="succeeded">Succeeded</SelectItem>
                    <SelectItem value="failed">Failed</SelectItem>
                </SelectContent>
            </Select>

            <Select v-model="typeFilter">
                <SelectTrigger class="w-40">
                    <SelectValue placeholder="All types" />
                </SelectTrigger>
                <SelectContent>
                    <SelectItem value="all">All types</SelectItem>
                    <SelectItem value="backup">Backup</SelectItem>
                    <SelectItem value="restore">Restore</SelectItem>
                </SelectContent>
            </Select>

            <!-- Total count hint — only shown when all data is loaded -->
            <span v-if="!loading" class="text-sm text-muted-foreground">
                {{ filteredJobs.length }} job{{ filteredJobs.length !== 1 ? 's' : '' }}
                <template v-if="total > 50"> (showing last 50)</template>
            </span>
        </div>

        <!-- Table -->
        <div class="border rounded-md overflow-x-auto">
            <Table>
                <TableHeader>
                    <TableRow>
                        <TableHead>Policy</TableHead>
                        <TableHead>Type</TableHead>
                        <TableHead>Agent</TableHead>
                        <TableHead>Status</TableHead>
                        <TableHead>Started</TableHead>
                        <TableHead>Duration</TableHead>
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
                    <template v-else-if="filteredJobs.length === 0">
                        <TableRow>
                            <TableCell colspan="6">
                                <div class="flex flex-col items-center justify-center gap-3 py-16 text-center">
                                    <div class="p-4 rounded-full bg-muted">
                                        <BriefcaseBusiness class="w-10 h-10 text-muted-foreground" />
                                    </div>
                                    <div>
                                        <p class="font-medium">No jobs found</p>
                                        <p class="mt-1 text-sm text-muted-foreground">
                                            Jobs will appear here once a policy has been triggered.
                                        </p>
                                    </div>
                                </div>
                            </TableCell>
                        </TableRow>
                    </template>

                    <!-- Data rows -->
                    <template v-else>
                        <TableRow v-for="job in filteredJobs" :key="job.id" class="cursor-pointer hover:bg-muted/50"
                            @click="router.push(`/jobs/${job.id}`)">
                            <TableCell class="font-medium">{{ job.policy_name }}</TableCell>
                            <TableCell>
                                <Badge variant="outline" class="capitalize">{{ job.type }}</Badge>
                            </TableCell>
                            <TableCell class="text-muted-foreground">{{ job.agent_name }}</TableCell>
                            <TableCell>
                                <Badge :variant="statusVariant(job.status)" :class="statusClass(job.status)">
                                    {{ statusLabel(job.status) }}
                                </Badge>
                            </TableCell>
                            <TableCell class="text-sm text-muted-foreground">
                                {{ formatDate(job.started_at) }}
                            </TableCell>
                            <TableCell class="text-sm text-muted-foreground font-mono">
                                {{ formatDuration(job.started_at, job.ended_at) }}
                            </TableCell>
                        </TableRow>
                    </template>
                </TableBody>
            </Table>
        </div>

    </div>
</template>