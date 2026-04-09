<script setup lang="ts">
import { ref, computed, onMounted, onUnmounted } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { useAuthStore } from '@/stores/auth'
import { VisXYContainer, VisArea, VisAxis } from '@unovis/vue'
import {
    Table,
    TableBody,
    TableCell,
    TableHead,
    TableHeader,
    TableRow,
} from '@/components/ui/table'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Skeleton } from '@/components/ui/skeleton'
import { Alert, AlertDescription } from '@/components/ui/alert'
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
import {
    ArrowLeft,
    RefreshCw,
    PencilLine,
    Trash2,
    WifiOff,
    Cpu,
    MemoryStick,
    HardDrive,
    ClipboardList,
} from 'lucide-vue-next'
import { api } from '@/services/api'
import { wsClient } from '@/services/websocket'
import type { Agent, AgentStatus, Job, ApiResponse } from '@/types'
import AgentSheet from '@/components/agents/AgentSheet.vue'
import {
    ChartContainer,
    ChartCrosshair,
    ChartTooltip,
    ChartTooltipContent,
    componentToString,
    type ChartConfig,
} from '@/components/ui/chart'

defineOptions({ inheritAttrs: false })

// ---------------------------------------------------------------------------
// Route / Router
// ---------------------------------------------------------------------------

const route = useRoute()
const router = useRouter()
const authStore = useAuthStore()
const agentId = route.params.id as string

// ---------------------------------------------------------------------------
// State — agent
// ---------------------------------------------------------------------------

const agent = ref<Agent | null>(null)
const loading = ref(true)
const error = ref<string | null>(null)
const liveStatus = ref<AgentStatus | null>(null)

const mergedAgent = computed(() => {
    if (!agent.value) return null
    return {
        ...agent.value,
        status: liveStatus.value ?? agent.value.status,
    }
})

// ---------------------------------------------------------------------------
// State — jobs
// ---------------------------------------------------------------------------

interface JobListResponse { items: Job[]; total: number }

const jobs = ref<Job[]>([])
const jobsLoading = ref(true)

// ---------------------------------------------------------------------------
// State — metrics history (rolling 10 points, one per heartbeat)
// ---------------------------------------------------------------------------

// Maximum number of data points to keep in the chart history.
const MAX_POINTS = 10

interface MetricPoint { cpu: number; mem: number; disk: number }

const metricsHistory = ref<MetricPoint[]>([])
const hasMetrics = computed(() => metricsHistory.value.length > 0)

// Reversed so newest is at index 0 (left). Label embedded so the tooltip
// cache key (which is based on the data object) naturally includes it.
const chartData = computed(() =>
    [...metricsHistory.value].reverse().map((p, i) => ({
        ...p,
        _label: `${i * 30}s ago`,
    }))
)

const chartTickValues = computed(() =>
    chartData.value.map((_, i) => i)
)

const crosshairSeriesColors = [
    'var(--color-cpu)',
    'var(--color-mem)',
    'var(--color-disk)',
]

const crosshairColor = (_d: unknown, i: number) =>
    crosshairSeriesColors[i] ?? 'var(--foreground)'

// ---------------------------------------------------------------------------
// Edit / Delete sheet
// ---------------------------------------------------------------------------

const editSheetOpen = ref(false)
const deleteDialogOpen = ref(false)
const deleteLoading = ref(false)

// ---------------------------------------------------------------------------
// Data fetching
// ---------------------------------------------------------------------------

async function fetchAgent() {
    loading.value = true
    error.value = null
    try {
        const res = await api<ApiResponse<Agent>>(`/api/v1/agents/${agentId}`)
        agent.value = res.data
    } catch (e: any) {
        error.value = e?.message ?? 'Failed to load agent'
    } finally {
        loading.value = false
    }
}

async function fetchJobs() {
    jobsLoading.value = true
    try {
        const res = await api<ApiResponse<JobListResponse>>(
            `/api/v1/jobs?agent_id=${agentId}&limit=5&offset=0`
        )
        jobs.value = res.data.items
    } catch {
        // Non-fatal — jobs section just stays empty
    } finally {
        jobsLoading.value = false
    }
}

// ---------------------------------------------------------------------------
// WebSocket subscription
// ---------------------------------------------------------------------------

let unsubscribe: (() => void) | null = null

function subscribe() {
    unsubscribe = wsClient.subscribe(`agent:${agentId}`, (msg: any) => {
        if (msg?.type === 'agent.status' && msg?.payload?.status) {
            liveStatus.value = msg.payload.status as AgentStatus
        }
        if (msg?.type === 'agent.metrics' && msg?.payload) {
            const p = msg.payload
            metricsHistory.value.push({
                cpu: Math.round(p.cpu_percent ?? 0),
                mem: Math.round(p.mem_percent ?? 0),
                disk: Math.round(p.disk_percent ?? 0),
            })
            // Keep only the last MAX_POINTS entries
            if (metricsHistory.value.length > MAX_POINTS) {
                metricsHistory.value.shift()
            }
        }
    })
}

// ---------------------------------------------------------------------------
// Delete
// ---------------------------------------------------------------------------

async function confirmDelete() {
    deleteLoading.value = true
    try {
        await api(`/api/v1/agents/${agentId}`, { method: 'DELETE' })
        router.push('/agents')
    } catch (e: any) {
        error.value = e?.message ?? 'Failed to delete agent'
    } finally {
        deleteLoading.value = false
        deleteDialogOpen.value = false
    }
}

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

function statusVariant(status: AgentStatus): 'default' | 'secondary' | 'outline' {
    switch (status) {
        case 'online': return 'outline'
        case 'offline': return 'secondary'
        default: return 'outline'
    }
}

function statusClass(status: AgentStatus): string {
    switch (status) {
        case 'online': return 'bg-green-500/10 text-green-700 dark:text-green-400 border-green-500/20'
        case 'unknown': return 'bg-amber-500/10 text-amber-700 dark:text-amber-400 border-amber-500/20'
        default: return ''
    }
}

function formatLastSeen(lastSeenAt: string | null): string {
    if (!lastSeenAt) return '—'
    const date = new Date(lastSeenAt)
    if (isNaN(date.getTime())) return '—'
    const diff = Math.floor((Date.now() - date.getTime()) / 1000)
    if (diff < 60) return `${diff}s ago`
    if (diff < 3600) return `${Math.floor(diff / 60)}m ago`
    if (diff < 86400) return `${Math.floor(diff / 3600)}h ago`
    return date.toLocaleDateString()
}

function jobStatusVariant(status: string): 'default' | 'secondary' | 'outline' | 'destructive' {
    switch (status) {
        case 'succeeded':
        case 'completed': return 'outline'
        case 'running': return 'outline'
        case 'failed': return 'destructive'
        default: return 'secondary'
    }
}

function jobStatusClass(status: string): string {
    switch (status) {
        case 'succeeded':
        case 'completed': return 'bg-green-500/10 text-green-700 dark:text-green-400 border-green-500/20'
        case 'running': return 'bg-blue-500/10 text-blue-700 dark:text-blue-400 border-blue-500/20'
        case 'pending': return 'bg-amber-500/10 text-amber-700 dark:text-amber-400 border-amber-500/20'
        default: return ''
    }
}

function formatDate(date: string | null): string {
    if (!date) return '—'
    return new Date(date).toLocaleString()
}

// ---------------------------------------------------------------------------
// Chart configuration — colours from the design-system palette
// ---------------------------------------------------------------------------

const metricsChartConfig = {
    cpu: { label: 'CPU', color: 'var(--chart-2)' }, // teal
    mem: { label: 'Memory', color: 'var(--chart-3)' }, // warm yellow
    disk: { label: 'Disk', color: 'var(--chart-4)' }, // violet
} satisfies ChartConfig

// componentToString must be called during setup (it calls useId internally).
const metricsTooltip = componentToString(metricsChartConfig, ChartTooltipContent, {
    config: metricsChartConfig,
    labelKey: '_label',
    valueFormatter: (v: number) => `${v}%`,
} as any)

// ---------------------------------------------------------------------------
// Latest metric values for the legend
// ---------------------------------------------------------------------------

const latestMetrics = computed(() => {
    if (!hasMetrics.value) return null
    return metricsHistory.value[metricsHistory.value.length - 1]
})

// ---------------------------------------------------------------------------
// Lifecycle
// ---------------------------------------------------------------------------

onMounted(async () => {
    await Promise.all([fetchAgent(), fetchJobs()])
    subscribe()
})

onUnmounted(() => {
    unsubscribe?.()
})
</script>

<template>
    <div class="flex flex-col gap-6 p-6">

        <!-- Back + Header -->
        <div class="flex items-start justify-between gap-4">
            <div class="flex items-center gap-3">
                <Button variant="ghost" size="icon" aria-label="Back to agents" @click="router.push('/agents')">
                    <ArrowLeft class="w-4 h-4" />
                </Button>
                <div>
                    <div v-if="loading" class="flex flex-col gap-1.5">
                        <Skeleton class="w-48 h-6" />
                        <Skeleton class="w-32 h-4" />
                    </div>
                    <template v-else-if="mergedAgent">
                        <div class="flex items-center gap-2.5">
                            <h1 class="text-2xl font-semibold tracking-tight">{{ mergedAgent.name }}</h1>
                            <Badge :variant="statusVariant(mergedAgent.status)" class="gap-1.5"
                                :class="statusClass(mergedAgent.status)">
                                <span class="inline-block h-1.5 w-1.5 rounded-full" :class="{
                                    'bg-emerald-400': mergedAgent.status === 'online',
                                    'bg-muted-foreground': mergedAgent.status === 'offline',
                                    'bg-yellow-400': mergedAgent.status === 'unknown',
                                }" />
                                {{ mergedAgent.status.charAt(0).toUpperCase() + mergedAgent.status.slice(1) }}
                            </Badge>
                        </div>
                        <p class="mt-0.5 text-sm font-mono text-muted-foreground">
                            {{ mergedAgent.hostname || '—' }}
                        </p>
                    </template>
                </div>
            </div>

            <!-- Actions -->
            <div v-if="!loading && mergedAgent" class="flex items-center gap-2">
                <Button variant="outline" size="icon" aria-label="Refresh" @click="fetchAgent(); fetchJobs()">
                    <RefreshCw class="w-4 h-4" />
                </Button>
                <Button variant="outline" size="sm" @click="editSheetOpen = true">
                    <PencilLine class="w-4 h-4 mr-1.5" />
                    Rename
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

        <!-- Offline banner -->
        <Alert v-if="!loading && mergedAgent?.status === 'offline'"
            class="border-amber-500/30 bg-amber-500/5 text-amber-600 dark:text-amber-400">
            <WifiOff class="h-4 w-4" />
            <AlertDescription>
                Agent is offline — live metrics are not available. Last seen {{ formatLastSeen(mergedAgent.last_seen_at)
                }}.
            </AlertDescription>
        </Alert>

        <!-- Info cards -->
        <div v-if="loading" class="grid grid-cols-2 gap-4 sm:grid-cols-4">
            <div v-for="n in 4" :key="n" class="p-4 border rounded-md">
                <Skeleton class="w-16 h-3 mb-2" />
                <Skeleton class="w-24 h-4" />
            </div>
        </div>
        <div v-else-if="mergedAgent" class="grid grid-cols-2 gap-4 sm:grid-cols-4">
            <div class="p-4 border rounded-md">
                <p class="text-xs text-muted-foreground uppercase tracking-wide mb-1">OS / Arch</p>
                <p class="text-sm font-medium">
                    {{ [mergedAgent.os, mergedAgent.arch].filter(Boolean).join(' / ') || '—' }}
                </p>
            </div>
            <div class="p-4 border rounded-md">
                <p class="text-xs text-muted-foreground uppercase tracking-wide mb-1">Version</p>
                <p class="text-sm font-mono font-medium">{{ mergedAgent.version || '—' }}</p>
            </div>
            <div class="p-4 border rounded-md">
                <p class="text-xs text-muted-foreground uppercase tracking-wide mb-1">Last Seen</p>
                <p class="text-sm font-medium">{{ formatLastSeen(mergedAgent.last_seen_at) }}</p>
            </div>
            <div class="p-4 border rounded-md">
                <p class="text-xs text-muted-foreground uppercase tracking-wide mb-1">Registered</p>
                <p class="text-sm font-medium">{{ formatDate(mergedAgent.created_at) }}</p>
            </div>
        </div>

        <!-- Metrics chart -->
        <div class="border rounded-md p-4 flex flex-col gap-3">
            <div class="flex items-center justify-between">
                <p class="text-sm font-medium">Resource Usage</p>
                <!-- Legend -->
                <div class="flex items-center gap-4 text-xs text-muted-foreground">
                    <div class="flex items-center gap-1.5">
                        <Cpu class="w-3.5 h-3.5" :style="{ color: 'var(--chart-2)' }" />
                        <span>CPU</span>
                        <span v-if="latestMetrics" class="font-mono font-medium text-foreground">
                            {{ latestMetrics.cpu }}%
                        </span>
                        <span v-else>—</span>
                    </div>
                    <div class="flex items-center gap-1.5">
                        <MemoryStick class="w-3.5 h-3.5" :style="{ color: 'var(--chart-3)' }" />
                        <span>Memory</span>
                        <span v-if="latestMetrics" class="font-mono font-medium text-foreground">
                            {{ latestMetrics.mem }}%
                        </span>
                        <span v-else>—</span>
                    </div>
                    <div class="flex items-center gap-1.5">
                        <HardDrive class="w-3.5 h-3.5" :style="{ color: 'var(--chart-4)' }" />
                        <span>Disk</span>
                        <span v-if="latestMetrics" class="font-mono font-medium text-foreground">
                            {{ latestMetrics.disk }}%
                        </span>
                        <span v-else>—</span>
                    </div>
                </div>
            </div>

            <!-- Chart or placeholder -->
            <div class="h-48 relative">
                <template v-if="hasMetrics">
                    <ChartContainer :config="metricsChartConfig" :cursor="true">
                        <VisXYContainer :data="chartData" :y-domain="[0, 100]">
                            <VisArea :x="(_d: any, i: number) => i" :y="(d: any) => d.cpu"
                                :color="() => 'var(--color-cpu)'" :opacity="0.15" :line="true" />
                            <VisArea :x="(_d: any, i: number) => i" :y="(d: any) => d.mem"
                                :color="() => 'var(--color-mem)'" :opacity="0.15" :line="true" />
                            <VisArea :x="(_d: any, i: number) => i" :y="(d: any) => d.disk"
                                :color="() => 'var(--color-disk)'" :opacity="0.15" :line="true" />

                            <VisAxis type="x" :tickValues="chartTickValues"
                                :tick-format="(v: number) => chartData[Math.round(v)]?._label ?? ''" />
                            <VisAxis type="y" :tickValues="[0, 20, 40, 60, 80, 100]"
                                :tick-format="(v: number) => `${v}%`" />
                            <ChartTooltip />
                            <ChartCrosshair :template="metricsTooltip" :color="crosshairColor" />
                        </VisXYContainer>
                    </ChartContainer>
                </template>
                <div v-else class="absolute inset-0 flex items-center justify-center text-sm text-muted-foreground">
                    Waiting for first heartbeat…
                </div>
            </div>
            <p class="text-xs text-muted-foreground">
                Updated every 30 seconds via heartbeat. Chart shows the last {{ MAX_POINTS }} readings.
            </p>
        </div>

        <!-- Recent jobs -->
        <div class="flex flex-col gap-3">
            <p class="text-sm font-medium">Recent Jobs</p>
            <div class="border rounded-md overflow-x-auto">
                <Table>
                    <TableHeader>
                        <TableRow>
                            <TableHead>Policy</TableHead>
                            <TableHead>Status</TableHead>
                            <TableHead>Started</TableHead>
                            <TableHead>Finished</TableHead>
                        </TableRow>
                    </TableHeader>
                    <TableBody>
                        <!-- Loading -->
                        <template v-if="jobsLoading">
                            <TableRow v-for="n in 5" :key="n">
                                <TableCell v-for="col in 4" :key="col">
                                    <Skeleton class="w-full h-4" />
                                </TableCell>
                            </TableRow>
                        </template>

                        <!-- Empty -->
                        <template v-else-if="jobs.length === 0">
                            <TableRow>
                                <TableCell colspan="4">
                                    <div class="flex flex-col items-center justify-center gap-3 py-10 text-center">
                                        <div class="p-4 rounded-full bg-muted">
                                            <ClipboardList class="w-10 h-10 text-muted-foreground" />
                                        </div>
                                        <div>
                                            <p class="font-medium">No jobs yet</p>
                                            <p class="mt-1 text-sm text-muted-foreground">
                                                No jobs have run on this agent yet.
                                            </p>
                                        </div>
                                    </div>
                                </TableCell>
                            </TableRow>
                        </template>

                        <!-- Rows -->
                        <template v-else>
                            <TableRow v-for="job in jobs" :key="job.id" class="cursor-pointer hover:bg-muted/50"
                                @click="router.push(`/jobs/${job.id}`)">
                                <TableCell class="font-medium">
                                    {{ (job as any).policy_name || job.policy_id }}
                                </TableCell>
                                <TableCell>
                                    <Badge :variant="jobStatusVariant(job.status)" :class="jobStatusClass(job.status)">
                                        {{ job.status }}
                                    </Badge>
                                </TableCell>
                                <TableCell class="text-sm text-muted-foreground">
                                    {{ formatDate((job as any).started_at) }}
                                </TableCell>
                                <TableCell class="text-sm text-muted-foreground">
                                    {{ formatDate((job as any).finished_at ?? (job as any).ended_at) }}
                                </TableCell>
                            </TableRow>
                        </template>
                    </TableBody>
                </Table>
            </div>
        </div>

    </div>

    <!-- Edit sheet -->
    <AgentSheet v-if="mergedAgent && editSheetOpen" :agent="mergedAgent" :open="editSheetOpen"
        @update:open="editSheetOpen = $event" @saved="fetchAgent" />

    <!-- Delete dialog -->
    <AlertDialog :open="deleteDialogOpen" @update:open="deleteDialogOpen = $event">
        <AlertDialogContent>
            <AlertDialogHeader>
                <AlertDialogTitle>Delete agent?</AlertDialogTitle>
                <AlertDialogDescription>
                    <span v-if="mergedAgent">
                        <strong>{{ mergedAgent.name }}</strong> will be soft-deleted. Existing jobs
                        and snapshots will be retained.
                    </span>
                </AlertDialogDescription>
            </AlertDialogHeader>
            <AlertDialogFooter>
                <AlertDialogCancel :disabled="deleteLoading">Cancel</AlertDialogCancel>
                <AlertDialogAction variant="destructive" :disabled="deleteLoading" @click="confirmDelete">
                    {{ deleteLoading ? 'Deleting…' : 'Delete' }}
                </AlertDialogAction>
            </AlertDialogFooter>
        </AlertDialogContent>
    </AlertDialog>
</template>