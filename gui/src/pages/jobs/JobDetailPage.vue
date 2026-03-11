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
import { Alert, AlertDescription } from '@/components/ui/alert'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card'
import { Skeleton } from '@/components/ui/skeleton'
import { Separator } from '@/components/ui/separator'
import {
    ArrowLeft,
    RefreshCw,
    FileText,
    Server,
    CalendarClock,
    CheckCircle,
    XCircle,
    Clock,
    Loader,
} from 'lucide-vue-next'
import { api } from '@/services/api'
import { useWebSocket } from '@/services/websocket'
import type { ApiResponse, Job, JobLog, JobStatus, JobStatusPayload, JobLogPayload } from '@/types'

// ---------------------------------------------------------------------------
// Route
// ---------------------------------------------------------------------------

const route = useRoute()
const router = useRouter()
const jobId = route.params.id as string

// ---------------------------------------------------------------------------
// State
// ---------------------------------------------------------------------------

const job = ref<Job | null>(null)
const logs = ref<JobLog[]>([])
const loading = ref(true)
const error = ref<string | null>(null)

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

// statusVariant maps a JobStatus to the appropriate shadcn Badge variant.
function statusVariant(status: string): 'default' | 'secondary' | 'destructive' | 'outline' {
    switch (status) {
        case 'succeeded': return 'default'
        case 'running': return 'outline'
        case 'failed': return 'destructive'
        case 'pending':
        default: return 'secondary'
    }
}

// statusLabel returns a capitalised display string for a job status.
function statusLabel(status: string): string {
    return status.charAt(0).toUpperCase() + status.slice(1)
}

// statusIcon returns the appropriate Lucide icon component for a job status.
function statusIcon(status: string) {
    switch (status) {
        case 'succeeded': return CheckCircle
        case 'running': return Loader
        case 'failed': return XCircle
        case 'pending':
        default: return Clock
    }
}

// logLevelVariant maps a log level to a Badge variant for visual distinction.
function logLevelVariant(level: string): 'default' | 'secondary' | 'destructive' | 'outline' {
    switch (level) {
        case 'error': return 'destructive'
        case 'warn': return 'outline'
        case 'info':
        default: return 'secondary'
    }
}

// formatDate returns a locale-formatted date+time string, or a dash if null.
function formatDate(iso: string | null | undefined): string {
    if (!iso) return '—'
    return new Date(iso).toLocaleString(undefined, {
        dateStyle: 'medium',
        timeStyle: 'medium',
    })
}

// formatTime returns just the time portion of an ISO timestamp for log lines.
function formatTime(iso: string): string {
    return new Date(iso).toLocaleTimeString(undefined, { timeStyle: 'medium' })
}

// formatDuration calculates elapsed time between two ISO timestamps.
function formatDuration(startedAt: string | null | undefined, finishedAt: string | null | undefined): string {
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

// formatBytes returns a human-readable byte size string.
function formatBytes(bytes: number): string {
    if (bytes === 0) return '0 B'
    const units = ['B', 'KB', 'MB', 'GB', 'TB']
    const i = Math.floor(Math.log(bytes) / Math.log(1024))
    return `${(bytes / Math.pow(1024, i)).toFixed(1)} ${units[i]}`
}

// isRunning is true while the job has not yet reached a terminal state.
// Used to decide whether to subscribe to live WebSocket updates.
const isRunning = computed(() =>
    job.value?.status === 'running' || job.value?.status === 'pending',
)

// ---------------------------------------------------------------------------
// Data fetching
// ---------------------------------------------------------------------------

async function fetchJob() {
    loading.value = true
    error.value = null
    try {
        const res = await api<ApiResponse<Job>>(`/api/v1/jobs/${jobId}`)
        job.value = res.data

        // Fetch logs separately. For finished jobs this is the only source of
        // truth; for running jobs we load historic logs and then append live ones.
        await fetchLogs()
    } catch (e: any) {
        error.value = e?.message ?? 'Failed to load job.'
    } finally {
        loading.value = false
    }
}

async function fetchLogs() {
    try {
        const res = await api<ApiResponse<JobLog[]>>(`/api/v1/jobs/${jobId}/logs`)
        logs.value = res.data
    } catch {
        // Non-fatal — the job detail is still usable without logs.
    }
}

// ---------------------------------------------------------------------------
// Live updates via WebSocket
// ---------------------------------------------------------------------------

// useWebSocket automatically unsubscribes on component unmount.
// We subscribe unconditionally; handlers guard on the live status.

useWebSocket<JobLogPayload>(`job:${jobId}`, (msg) => {
    // Append incoming log lines only while the job is running.
    if (msg.type === 'job.log' && msg.payload) {
        const p = msg.payload
        logs.value.push({
            id: crypto.randomUUID(),
            level: p.level,
            message: p.message,
            timestamp: p.timestamp,
        })
    }

    // Update job status when the server signals a state transition.
    if (msg.type === 'job.status' && msg.payload && job.value) {
        const p = msg.payload as unknown as JobStatusPayload
        job.value.status = p.status as JobStatus
        job.value.ended_at = p.finished_at ?? job.value.ended_at

        // Once the job transitions to a terminal state, reload the full detail
        // so destinations are updated with final byte counts and statuses.
        if (p.status === 'succeeded' || p.status === 'failed') {
            fetchJob()
        }
    }
})

// ---------------------------------------------------------------------------
// Mount
// ---------------------------------------------------------------------------

onMounted(fetchJob)
</script>

<template>
    <div class="flex flex-col gap-6 p-6">

        <!-- Page header -->
        <div class="flex items-center justify-between">
            <div class="flex items-center gap-3">
                <Button variant="ghost" size="icon" @click="router.push('/jobs')">
                    <ArrowLeft class="w-4 h-4" />
                </Button>
                <div>
                    <div class="flex items-center gap-2">
                        <h1 class="text-2xl font-semibold tracking-tight">Job Detail</h1>
                        <Badge v-if="job" :variant="statusVariant(job.status)" class="gap-1">
                            <component :is="statusIcon(job.status)" class="w-3 h-3"
                                :class="{ 'animate-spin': job.status === 'running' }" />
                            {{ statusLabel(job.status) }}
                        </Badge>
                    </div>
                    <p class="mt-0.5 text-xs text-muted-foreground font-mono">{{ jobId }}</p>
                </div>
            </div>
            <div class="flex items-center gap-2">
                <Button variant="outline" size="icon" :disabled="loading" @click="fetchJob">
                    <RefreshCw class="w-4 h-4" :class="{ 'animate-spin': loading }" />
                </Button>
            </div>
        </div>

        <!-- Error banner -->
        <Alert v-if="error" variant="destructive">
            <AlertDescription>{{ error }}</AlertDescription>
        </Alert>

        <!-- ── Info cards ──────────────────────────────────────────────────── -->
        <div class="grid grid-cols-2 sm:grid-cols-3 gap-4">

            <!-- Policy -->
            <Card>
                <CardHeader class="pb-2">
                    <CardTitle class="text-xs font-medium text-muted-foreground uppercase tracking-wider">
                        Policy
                    </CardTitle>
                </CardHeader>
                <CardContent>
                    <Skeleton v-if="loading" class="h-5 w-3/4" />
                    <div v-else class="flex items-center gap-2 text-sm font-medium">
                        <FileText class="w-4 h-4 text-muted-foreground shrink-0" />
                        <span class="truncate">{{ job?.policy_name ?? '—' }}</span>
                    </div>
                </CardContent>
            </Card>

            <!-- Agent -->
            <Card>
                <CardHeader class="pb-2">
                    <CardTitle class="text-xs font-medium text-muted-foreground uppercase tracking-wider">
                        Agent
                    </CardTitle>
                </CardHeader>
                <CardContent>
                    <Skeleton v-if="loading" class="h-5 w-3/4" />
                    <div v-else class="flex items-center gap-2 text-sm font-medium">
                        <Server class="w-4 h-4 text-muted-foreground shrink-0" />
                        <span class="truncate">{{ job?.agent_name ?? '—' }}</span>
                    </div>
                </CardContent>
            </Card>

            <!-- Duration -->
            <Card>
                <CardHeader class="pb-2">
                    <CardTitle class="text-xs font-medium text-muted-foreground uppercase tracking-wider">
                        Duration
                    </CardTitle>
                </CardHeader>
                <CardContent>
                    <Skeleton v-if="loading" class="h-5 w-1/2" />
                    <div v-else class="flex items-center gap-2 text-sm font-medium font-mono">
                        <CalendarClock class="w-4 h-4 text-muted-foreground shrink-0" />
                        <span>{{ formatDuration(job?.started_at, job?.ended_at) }}</span>
                    </div>
                </CardContent>
            </Card>

        </div>

        <!-- Started / Finished timestamps (full width, subtle) -->
        <div v-if="!loading && job" class="flex items-center gap-6 text-sm text-muted-foreground -mt-2">
            <span>Started: <span class="text-foreground">{{ formatDate(job.started_at) }}</span></span>
            <span>Finished: <span class="text-foreground">{{ formatDate(job.ended_at) }}</span></span>
        </div>

        <!-- Error message (only for failed jobs) -->
        <Alert v-if="!loading && job?.status === 'failed' && job.error" variant="destructive">
            <XCircle class="w-4 h-4" />
            <AlertDescription>{{ job.error }}</AlertDescription>
        </Alert>

        <Separator />

        <!-- ── Destinations ────────────────────────────────────────────────── -->
        <div>
            <h2 class="text-base font-semibold mb-3">Destinations</h2>
            <div class="border rounded-md">
                <Table>
                    <TableHeader>
                        <TableRow>
                            <TableHead>Destination</TableHead>
                            <TableHead>Status</TableHead>
                            <TableHead>Total Size</TableHead>
                            <TableHead>Duration</TableHead>
                        </TableRow>
                    </TableHeader>
                    <TableBody>

                        <!-- Loading skeletons -->
                        <template v-if="loading">
                            <TableRow v-for="n in 2" :key="n">
                                <TableCell v-for="col in 7" :key="col">
                                    <Skeleton class="w-full h-4" />
                                </TableCell>
                            </TableRow>
                        </template>

                        <!-- Empty state -->
                        <template v-else-if="!job?.destinations?.length">
                            <TableRow>
                                <TableCell colspan="7">
                                    <p class="text-center text-sm text-muted-foreground py-8">
                                        No destination data available yet.
                                    </p>
                                </TableCell>
                            </TableRow>
                        </template>

                        <!-- Data rows -->
                        <template v-else>
                            <TableRow v-for="dest in job.destinations" :key="dest.id">
                                <TableCell class="font-medium">{{ dest.destination_name }}</TableCell>
                                <TableCell>
                                    <Badge :variant="statusVariant(dest.status)">
                                        {{ statusLabel(dest.status) }}
                                    </Badge>
                                </TableCell>
                                <TableCell class="text-sm font-mono text-muted-foreground">
                                    {{ formatBytes(dest.size_bytes) }}
                                </TableCell>
                                <TableCell class="text-sm font-mono text-muted-foreground">
                                    {{ formatDuration(dest.started_at, dest.ended_at) }}
                                </TableCell>
                            </TableRow>
                        </template>

                    </TableBody>
                </Table>
            </div>
        </div>

        <Separator />

        <!-- ── Logs ───────────────────────────────────────────────────────── -->
        <div>
            <div class="flex items-center justify-between mb-3">
                <h2 class="text-base font-semibold">Logs</h2>
                <!-- Live indicator shown while the job is still running -->
                <div v-if="isRunning" class="flex items-center gap-1.5 text-xs text-muted-foreground">
                    <span class="relative flex h-2 w-2">
                        <span
                            class="animate-ping absolute inline-flex h-full w-full rounded-full bg-green-400 opacity-75" />
                        <span class="relative inline-flex rounded-full h-2 w-2 bg-green-500" />
                    </span>
                    Live
                </div>
            </div>

            <!-- Loading skeleton for logs -->
            <template v-if="loading">
                <div class="flex flex-col gap-2">
                    <Skeleton v-for="n in 4" :key="n" class="h-5 w-full" />
                </div>
            </template>

            <!-- Log list — fixed height, scrollable -->
            <template v-else>
                <div v-if="logs.length > 0"
                    class="border rounded-md bg-muted/30 font-mono text-xs overflow-y-auto max-h-96 p-3 flex flex-col gap-1">
                    <div v-for="log in logs" :key="log.id" class="flex items-start gap-2">
                        <!-- Timestamp -->
                        <span class="text-muted-foreground shrink-0 pt-px">
                            {{ formatTime(log.timestamp) }}
                        </span>
                        <!-- Level badge -->
                        <Badge :variant="logLevelVariant(log.level)" class="text-[10px] px-1 py-0 h-4 shrink-0">
                            {{ log.level.toUpperCase() }}
                        </Badge>
                        <!-- Message — preserves whitespace and allows wrapping -->
                        <span class="break-all leading-relaxed" :class="{
                            'text-destructive': log.level === 'error',
                            'text-yellow-600 dark:text-yellow-400': log.level === 'warn',
                        }">
                            {{ log.message }}
                        </span>
                    </div>
                </div>

                <!-- Empty log state -->
                <div v-else class="border rounded-md p-8 text-center text-sm text-muted-foreground">
                    No log entries recorded for this job.
                </div>
            </template>

        </div>

    </div>
</template>