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
import { Skeleton } from '@/components/ui/skeleton'
import { Progress } from '@/components/ui/progress'
import {
    ArrowLeft,
    RefreshCw,
    FileText,
    Server,
    CalendarClock,
    HardDrive,
    XCircle,
    Ban,
} from '@lucide/vue'
import { api } from '@/services/api'
import { useWebSocket } from '@/services/websocket'
import type { ApiResponse, Job, JobLog, JobStatus, JobStatusPayload, JobLogPayload, ResticProgressEvent } from '@/types'
import { statusVariant, statusClass, statusLabel, statusIcon, formatDate, formatDuration, formatBytes } from '@/lib/jobUtils'

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

const progressData = ref<ResticProgressEvent | null>(null)
const cancelling = ref(false)
const PROGRESS_KEY = `arkeep:job-progress:${jobId}`

// Terminal job states. Once the job reaches one of these, an in-flight fetch
// started while it was still running must not revert it back to a live state.
const TERMINAL: JobStatus[] = ['succeeded', 'failed', 'cancelled', 'interrupted']
const terminalSeen = ref(false)

// ---------------------------------------------------------------------------
// Helpers — statusVariant/statusClass/statusLabel/statusIcon/formatDate/
//            formatDuration/formatBytes imported from @/lib/jobUtils
// ---------------------------------------------------------------------------

// logLevelVariant maps a log level to a Badge variant for visual distinction.
function logLevelVariant(level: string): 'default' | 'secondary' | 'destructive' | 'outline' {
    switch (level) {
        case 'error': return 'destructive'
        case 'warn': return 'outline'
        case 'info':
        default: return 'secondary'
    }
}

// formatTime returns just the time portion of an ISO timestamp for log lines.
function formatTime(iso: string): string {
    return new Date(iso).toLocaleTimeString(undefined, { timeStyle: 'medium' })
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
    progressData.value = null
    try {
        const res = await api<ApiResponse<Job>>(`/api/v1/jobs/${jobId}`)
        // Guard against a stale response: if a terminal WS status already
        // arrived, this GET may have been issued before the job finished and
        // would otherwise revert the badge to "running".
        if (terminalSeen.value && !TERMINAL.includes(res.data.status)) return
        if (TERMINAL.includes(res.data.status)) terminalSeen.value = true
        job.value = res.data

        // Restore cached progress so the bar is visible immediately on reload.
        if (isRunning.value) {
            try {
                const cached = sessionStorage.getItem(PROGRESS_KEY)
                if (cached) progressData.value = JSON.parse(cached)
            } catch { /* ignore */ }
        }

        // Fetch logs separately. For finished jobs this is the only source of
        // truth; for running jobs we load historic DB logs and then append live ones.
        await fetchLogs()
    } catch (e: any) {
        error.value = e?.message ?? 'Failed to load job.'
    } finally {
        loading.value = false
    }
}

// refreshJobMeta fetches only the job metadata and destinations without touching
// the logs array. Called on terminal WS status updates so that live WS log
// entries already accumulated in memory are not wiped by a potentially-stale
// DB fetch (the bulk log insert may not have completed yet at that point).
async function refreshJobMeta() {
    try {
        const res = await api<ApiResponse<Job>>(`/api/v1/jobs/${jobId}`)
        if (terminalSeen.value && !TERMINAL.includes(res.data.status)) return
        if (TERMINAL.includes(res.data.status)) terminalSeen.value = true
        job.value = res.data
    } catch {
        // Non-fatal — the current status is already updated optimistically.
    }
}

async function fetchLogs() {
    try {
        const res = await api<ApiResponse<JobLog[]>>(`/api/v1/jobs/${jobId}/logs`)
        logs.value = res.data.filter(l => !isResticJson(l.message))
    } catch {
        // Non-fatal — the job detail is still usable without logs.
    }
}

// isResticJson returns true for any restic JSON message (status/summary/error/exit_error).
// Used to filter these out of the displayed log list.
function isResticJson(msg: string): boolean {
    if (!msg || msg[0] !== '{') return false
    try { return !!JSON.parse(msg)?.message_type } catch { return false }
}

// tryParseProgress returns a ResticProgressEvent only for status/summary types,
// which carry meaningful percent_done/bytes data for the progress bar.
function tryParseProgress(msg: string): ResticProgressEvent | null {
    try {
        const parsed = JSON.parse(msg)
        if (parsed.message_type === 'status' || parsed.message_type === 'summary') {
            return parsed as ResticProgressEvent
        }
    } catch {
        // not a progress event
    }
    return null
}

async function cancelJob() {
    cancelling.value = true
    try {
        await api(`/api/v1/jobs/${jobId}/cancel`, { method: 'POST' })
        // Optimistic UI: status will be updated via WebSocket event
    } catch (e: any) {
        error.value = e?.message ?? 'Failed to cancel job.'
    } finally {
        cancelling.value = false
    }
}

// ---------------------------------------------------------------------------
// Live updates via WebSocket
// ---------------------------------------------------------------------------

// useWebSocket automatically unsubscribes on component unmount.
// We subscribe unconditionally; handlers guard on the live status.

useWebSocket<JobLogPayload>(`job:${jobId}`, (msg) => {
    // Append incoming log lines (live WS stream from agent via gRPC).
    if (msg.type === 'job.log' && msg.payload) {
        const p = msg.payload
        if (isResticJson(p.message)) {
            const progress = tryParseProgress(p.message)
            if (progress && progress.percent_done >= (progressData.value?.percent_done ?? 0)) {
                progressData.value = progress
                sessionStorage.setItem(PROGRESS_KEY, JSON.stringify(progress))
            }
        } else {
            logs.value.push({
                id: crypto.randomUUID(),
                level: p.level,
                message: p.message,
                timestamp: p.timestamp,
            })
        }
    }

    // Update job status when the server signals a state transition.
    // NOTE: do not guard the whole block on job.value — a fast-failing job can
    // emit its terminal status before the initial fetchJob() has resolved.
    // Dropping it there would leave the badge stuck on "running" until a manual
    // refresh. The optimistic in-memory update still requires job.value, but the
    // terminal refresh must run regardless.
    if (msg.type === 'job.status' && msg.payload) {
        const p = msg.payload as unknown as JobStatusPayload
        if (job.value) {
            job.value.status = p.status as JobStatus
            job.value.ended_at = p.finished_at ?? job.value.ended_at
        }

        // Once the job reaches a terminal state, refresh only the job metadata
        // (status, destinations, timestamps) without touching the logs array.
        // The bulk DB log insert may not have completed yet, so calling
        // fetchLogs() here would wipe live WS log entries with an empty result.
        if (TERMINAL.includes(p.status as JobStatus)) {
            terminalSeen.value = true
            sessionStorage.removeItem(PROGRESS_KEY)
            refreshJobMeta()
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
                <Button variant="ghost" size="icon" aria-label="Back to jobs" @click="router.push('/jobs')">
                    <ArrowLeft class="w-4 h-4" />
                </Button>
                <div>
                    <div class="flex items-center gap-2">
                        <h1 class="text-2xl font-semibold tracking-tight">Job Detail</h1>
                        <Badge v-if="job" :variant="statusVariant(job.status)" class="gap-1" :class="statusClass(job.status)">
                            <component :is="statusIcon(job.status)" class="w-3 h-3"
                                :class="{ 'animate-spin': job.status === 'running' }" />
                            {{ statusLabel(job.status) }}
                        </Badge>
                    </div>
                    <p class="mt-0.5 text-xs text-muted-foreground font-mono">{{ jobId }}</p>
                </div>
            </div>
            <div class="flex items-center gap-2">
                <Button
                    v-if="!loading && isRunning"
                    variant="outline"
                    size="sm"
                    :disabled="cancelling"
                    class="gap-1.5 text-destructive border-destructive/40 hover:bg-destructive/10"
                    @click="cancelJob"
                >
                    <XCircle class="w-3.5 h-3.5" />
                    {{ cancelling ? 'Cancelling…' : 'Cancel Job' }}
                </Button>
                <Button variant="outline" size="icon" aria-label="Refresh" :disabled="loading" @click="fetchJob">
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
            <div class="p-4 border rounded-md">
                <p class="text-xs text-muted-foreground uppercase tracking-wide mb-1">Policy</p>
                <Skeleton v-if="loading" class="h-5 w-3/4" />
                <div v-else class="flex items-center gap-2 text-sm font-medium">
                    <FileText class="w-4 h-4 text-muted-foreground shrink-0" />
                    <span class="truncate">{{ job?.policy_name ?? '—' }}</span>
                </div>
            </div>

            <!-- Agent -->
            <div class="p-4 border rounded-md">
                <p class="text-xs text-muted-foreground uppercase tracking-wide mb-1">Agent</p>
                <Skeleton v-if="loading" class="h-5 w-3/4" />
                <div v-else class="flex items-center gap-2 text-sm font-medium">
                    <Server class="w-4 h-4 text-muted-foreground shrink-0" />
                    <span class="truncate">{{ job?.agent_name ?? '—' }}</span>
                </div>
            </div>

            <!-- Duration -->
            <div class="p-4 border rounded-md">
                <p class="text-xs text-muted-foreground uppercase tracking-wide mb-1">Duration</p>
                <Skeleton v-if="loading" class="h-5 w-1/2" />
                <div v-else class="flex items-center gap-2 text-sm font-medium font-mono">
                    <CalendarClock class="w-4 h-4 text-muted-foreground shrink-0" />
                    <span>{{ formatDuration(job?.started_at, job?.ended_at) }}</span>
                </div>
            </div>

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
        <Alert v-if="!loading && job?.status === 'cancelled'" class="border-slate-300 dark:border-slate-700">
            <Ban class="w-4 h-4" />
            <AlertDescription>{{ job.error || 'Job was cancelled.' }}</AlertDescription>
        </Alert>

        <!-- ── Destinations ────────────────────────────────────────────────── -->
        <div v-if="job?.type !== 'restore'" class="flex flex-col gap-3">
            <p class="text-sm font-medium">Destinations</p>
            <div class="border rounded-md overflow-x-auto">
                <Table>
                    <TableHeader>
                        <TableRow>
                            <TableHead>Destination</TableHead>
                            <TableHead>Status</TableHead>
                            <TableHead>Added</TableHead>
                            <TableHead>Duration</TableHead>
                        </TableRow>
                    </TableHeader>
                    <TableBody>

                        <!-- Loading skeletons -->
                        <template v-if="loading">
                            <TableRow v-for="n in 5" :key="n">
                                <TableCell v-for="col in 4" :key="col">
                                    <Skeleton class="w-full h-4" />
                                </TableCell>
                            </TableRow>
                        </template>

                        <!-- Empty state -->
                        <template v-else-if="!job?.destinations?.length">
                            <TableRow>
                                <TableCell colspan="7">
                                    <div class="flex flex-col items-center justify-center gap-3 py-10 text-center">
                                        <div class="p-4 rounded-full bg-muted">
                                            <HardDrive class="w-10 h-10 text-muted-foreground" />
                                        </div>
                                        <div>
                                            <p class="font-medium">No destination data</p>
                                            <p class="mt-1 text-sm text-muted-foreground">
                                                Destination details will appear once the job runs.
                                            </p>
                                        </div>
                                    </div>
                                </TableCell>
                            </TableRow>
                        </template>

                        <!-- Data rows -->
                        <template v-else>
                            <TableRow v-for="dest in job.destinations" :key="dest.id">
                                <TableCell class="font-medium">{{ dest.destination_name }}</TableCell>
                                <TableCell>
                                    <Badge :variant="statusVariant(dest.status)" :class="statusClass(dest.status)">
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

        <!-- ── Command Sources ──────────────────────────────────────────────── -->
        <!-- Only shown when the policy actually has command-type sources — most
             jobs have none, so unlike Destinations there is no empty state. -->
        <div v-if="job?.command_sources?.length" class="flex flex-col gap-3">
            <p class="text-sm font-medium">Command Sources</p>
            <div class="border rounded-md overflow-x-auto">
                <Table>
                    <TableHeader>
                        <TableRow>
                            <TableHead>Destination</TableHead>
                            <TableHead>Source</TableHead>
                            <TableHead>Status</TableHead>
                            <TableHead>Added</TableHead>
                            <TableHead>Duration</TableHead>
                        </TableRow>
                    </TableHeader>
                    <TableBody>
                        <TableRow v-for="cmd in job.command_sources" :key="cmd.id">
                            <TableCell class="font-medium">{{ cmd.destination_name }}</TableCell>
                            <TableCell class="font-mono text-sm">{{ cmd.source_name }}</TableCell>
                            <TableCell>
                                <Badge :variant="statusVariant(cmd.status)" :class="statusClass(cmd.status)">
                                    {{ statusLabel(cmd.status) }}
                                </Badge>
                            </TableCell>
                            <TableCell class="text-sm font-mono text-muted-foreground">
                                {{ formatBytes(cmd.size_bytes) }}
                            </TableCell>
                            <TableCell class="text-sm font-mono text-muted-foreground">
                                {{ formatDuration(cmd.started_at, cmd.ended_at) }}
                            </TableCell>
                        </TableRow>
                    </TableBody>
                </Table>
            </div>
        </div>

        <!-- ── Progress (shown during execution) ─────────────────────────── -->
        <div v-if="!loading && isRunning" class="flex flex-col gap-3">
            <div class="flex items-center justify-between">
                <p class="text-sm font-medium">Progress</p>
                <div class="flex items-center gap-1.5 text-xs text-muted-foreground">
                    <span class="relative flex h-2 w-2">
                        <span class="animate-ping absolute inline-flex h-full w-full rounded-full bg-green-400 opacity-75" />
                        <span class="relative inline-flex rounded-full h-2 w-2 bg-green-500" />
                    </span>
                    Live
                </div>
            </div>
            <template v-if="progressData">
                <div class="flex items-center justify-between text-sm">
                    <span class="font-medium tabular-nums">{{ Math.round(progressData.percent_done * 100) }}%</span>
                    <span class="text-muted-foreground text-xs tabular-nums">
                        {{ formatBytes(progressData.bytes_done) }} / {{ formatBytes(progressData.total_bytes) }}
                        &mdash; {{ progressData.files_done }} / {{ progressData.total_files }} files
                    </span>
                </div>
                <Progress :model-value="progressData.percent_done * 100" />
            </template>
            <p v-else class="text-sm text-muted-foreground">Preparing...</p>
        </div>

        <!-- ── Logs ───────────────────────────────────────────────────────── -->
        <div class="flex flex-col gap-3">
            <p class="text-sm font-medium">Logs</p>

            <!-- Loading skeleton -->
            <template v-if="loading">
                <div class="flex flex-col gap-2">
                    <Skeleton v-for="n in 4" :key="n" class="h-5 w-full" />
                </div>
            </template>

            <!-- Placeholder while job is running -->
            <div v-else-if="isRunning"
                class="border rounded-md p-8 text-center text-sm text-muted-foreground">
                Logs will be available after job completion.
            </div>

            <!-- Log list after completion -->
            <template v-else>
                <div v-if="logs.length > 0"
                    class="border rounded-md bg-muted/30 font-mono text-xs overflow-y-auto max-h-[50vh] min-h-32 p-3 flex flex-col gap-1">
                    <div v-for="log in logs" :key="log.id" class="flex items-start gap-2">
                        <span class="text-muted-foreground shrink-0 pt-px">
                            {{ formatTime(log.timestamp) }}
                        </span>
                        <Badge :variant="logLevelVariant(log.level)" class="text-[10px] px-1 py-0 h-4 shrink-0">
                            {{ log.level.toUpperCase() }}
                        </Badge>
                        <span class="break-all leading-relaxed" :class="{
                            'text-destructive': log.level === 'error',
                            'text-yellow-600 dark:text-yellow-400': log.level === 'warn',
                        }">
                            {{ log.message }}
                        </span>
                    </div>
                </div>
                <div v-else class="border rounded-md p-8 text-center text-sm text-muted-foreground">
                    No log entries recorded for this job.
                </div>
            </template>
        </div>

    </div>
</template>