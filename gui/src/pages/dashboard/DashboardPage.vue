<script setup lang="ts">
import { ref, computed, onMounted } from 'vue'
import { useRouter } from 'vue-router'
import { VisXYContainer, VisGroupedBar, VisAxis, VisArea } from '@unovis/vue'
import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card'
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
import { Server, ShieldCheck, BriefcaseBusiness, Camera, RefreshCw, AlertCircle } from 'lucide-vue-next'
import { api } from '@/services/api'
import type { ApiResponse, Job } from '@/types'
import {
    ChartContainer,
    ChartCrosshair,
    ChartTooltip,
    ChartTooltipContent,
    componentToString,
    type ChartConfig,
} from '@/components/ui/chart'

// ---------------------------------------------------------------------------
// Local types — mirror dashboardResponse in server/internal/api/dashboard.go
// ---------------------------------------------------------------------------

interface DayJobActivity {
    date: string        // "YYYY-MM-DD"
    succeeded: number
    failed: number
}

interface DaySizeActivity {
    date: string        // "YYYY-MM-DD"
    size_bytes: number
}

interface DashboardData {
    agents_total: number
    agents_online: number
    policies_total: number
    policies_active: number
    jobs_today_total: number
    jobs_today_succeeded: number
    jobs_today_failed: number
    snapshots_total: number
    snapshots_total_size: number  // bytes
    job_activity: DayJobActivity[]   // 7 entries, index 0 = oldest
    size_activity: DaySizeActivity[] // 7 entries, index 0 = oldest
}

// ---------------------------------------------------------------------------
// State
// ---------------------------------------------------------------------------

const router = useRouter()
const loading = ref(true)
const error = ref<string | null>(null)
const data = ref<DashboardData | null>(null)
const recentJobs = ref<Job[]>([])

// ---------------------------------------------------------------------------
// Chart configuration — colours come from the design-system CSS variables.
// ---------------------------------------------------------------------------

const jobsChartConfig = {
    succeeded: { label: 'Succeeded', color: '#22c55e' }, // semantic green
    failed: { label: 'Failed', color: '#ef4444' }, // semantic red
} satisfies ChartConfig

const sizeChartConfig = {
    size: { label: 'Backed up (GB)', color: 'var(--chart-1)' }, // brand primary
} satisfies ChartConfig

// ---------------------------------------------------------------------------
// Chart helpers
// ---------------------------------------------------------------------------

// shortLabel converts "YYYY-MM-DD" → "DD/MM" for the x-axis.
function shortLabel(iso: string): string {
    const [, m, d] = iso.split('-')
    return `${d}/${m}`
}

const jobsData = computed(() =>
    data.value?.job_activity.map(d => ({
        date: shortLabel(d.date),
        succeeded: d.succeeded,
        failed: d.failed,
    })) ?? []
)

const sizeData = computed(() =>
    data.value?.size_activity.map(d => ({
        date: shortLabel(d.date),
        size: parseFloat((d.size_bytes / 1073741824).toFixed(2)),
    })) ?? []
)

// componentToString must be called during setup (it calls useId internally).
const jobsTooltip = componentToString(jobsChartConfig, ChartTooltipContent, {
    config: jobsChartConfig,
    labelFormatter: (x: number | Date) => jobsData.value[x as number]?.date ?? '',
})

const sizeTooltip = componentToString(sizeChartConfig, ChartTooltipContent, {
    config: sizeChartConfig,
    labelFormatter: (x: number | Date) => sizeData.value[x as number]?.date ?? '',
})

// ---------------------------------------------------------------------------
// Table helpers — aligned with JobsPage
// ---------------------------------------------------------------------------

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

function statusLabel(status: string): string {
    return status.charAt(0).toUpperCase() + status.slice(1)
}

function formatDate(iso: string | null): string {
    if (!iso) return '—'
    return new Date(iso).toLocaleString(undefined, { dateStyle: 'medium', timeStyle: 'short' })
}

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

function formatBytes(bytes: number): string {
    if (bytes === 0) return '0 B'
    const k = 1024
    const sizes = ['B', 'KB', 'MB', 'GB', 'TB']
    const i = Math.floor(Math.log(bytes) / Math.log(k))
    return `${parseFloat((bytes / Math.pow(k, i)).toFixed(1))} ${sizes[i]}`
}

// ---------------------------------------------------------------------------
// Data fetching — dashboard aggregates + last 5 jobs in parallel
// ---------------------------------------------------------------------------

async function fetchAll() {
    loading.value = true
    error.value = null
    try {
        const [dashRes, jobsRes] = await Promise.all([
            api<ApiResponse<DashboardData>>('/api/v1/dashboard'),
            api<ApiResponse<{ items: Job[]; total: number }>>('/api/v1/jobs?limit=5'),
        ])
        data.value = dashRes.data
        recentJobs.value = jobsRes.data.items
    } catch (e: any) {
        error.value = e?.message ?? 'Failed to load dashboard data.'
    } finally {
        loading.value = false
    }
}

onMounted(fetchAll)
</script>

<template>
    <div class="flex flex-col gap-6 p-6">

        <!-- Page header -->
        <div class="flex items-center justify-between">
            <div>
                <h1 class="text-2xl font-semibold tracking-tight">Dashboard</h1>
                <p class="mt-1 text-sm text-muted-foreground">
                    Overview of your backup infrastructure.
                </p>
            </div>
            <Button variant="outline" size="icon" aria-label="Refresh" :disabled="loading" @click="fetchAll">
                <RefreshCw class="w-4 h-4" :class="{ 'animate-spin': loading }" />
            </Button>
        </div>

        <!-- Error banner -->
        <Alert v-if="error" variant="destructive">
            <AlertCircle class="size-4" />
            <AlertDescription>{{ error }}</AlertDescription>
        </Alert>

        <!-- ── Stat cards ──────────────────────────────────────────────────────── -->
        <div class="grid grid-cols-1 gap-4 sm:grid-cols-2 xl:grid-cols-4">

            <!-- Agents -->
            <Card>
                <CardHeader class="flex flex-row items-center justify-between pb-2">
                    <CardTitle class="text-sm font-medium text-muted-foreground">Agents</CardTitle>
                    <Server class="w-4 h-4 text-muted-foreground" />
                </CardHeader>
                <CardContent>
                    <template v-if="loading">
                        <Skeleton class="h-8 w-16 mb-1" />
                        <Skeleton class="h-3 w-24" />
                    </template>
                    <template v-else>
                        <p class="text-3xl font-bold tracking-tight">
                            {{ data?.agents_online }}
                            <span class="text-lg font-normal text-muted-foreground">/ {{ data?.agents_total }}</span>
                        </p>
                        <p class="mt-1 text-xs text-muted-foreground">online</p>
                    </template>
                </CardContent>
            </Card>

            <!-- Active policies -->
            <Card>
                <CardHeader class="flex flex-row items-center justify-between pb-2">
                    <CardTitle class="text-sm font-medium text-muted-foreground">Active policies</CardTitle>
                    <ShieldCheck class="w-4 h-4 text-muted-foreground" />
                </CardHeader>
                <CardContent>
                    <template v-if="loading">
                        <Skeleton class="h-8 w-16 mb-1" />
                        <Skeleton class="h-3 w-24" />
                    </template>
                    <template v-else>
                        <p class="text-3xl font-bold tracking-tight">{{ data?.policies_active }}</p>
                        <p class="mt-1 text-xs text-muted-foreground">of {{ data?.policies_total }} total</p>
                    </template>
                </CardContent>
            </Card>

            <!-- Jobs today -->
            <Card>
                <CardHeader class="flex flex-row items-center justify-between pb-2">
                    <CardTitle class="text-sm font-medium text-muted-foreground">Jobs today</CardTitle>
                    <BriefcaseBusiness class="w-4 h-4 text-muted-foreground" />
                </CardHeader>
                <CardContent>
                    <template v-if="loading">
                        <Skeleton class="h-8 w-16 mb-1" />
                        <Skeleton class="h-3 w-32" />
                    </template>
                    <template v-else>
                        <p class="text-3xl font-bold tracking-tight">{{ data?.jobs_today_total }}</p>
                        <p class="mt-1 text-xs text-muted-foreground">
                            <span class="text-green-500 font-medium">{{ data?.jobs_today_succeeded }} succeeded</span>
                            <span class="mx-1">·</span>
                            <span :class="(data?.jobs_today_failed ?? 0) > 0 ? 'text-red-500 font-medium' : ''">
                                {{ data?.jobs_today_failed }} failed
                            </span>
                        </p>
                    </template>
                </CardContent>
            </Card>

            <!-- Snapshots -->
            <Card>
                <CardHeader class="flex flex-row items-center justify-between pb-2">
                    <CardTitle class="text-sm font-medium text-muted-foreground">Snapshots</CardTitle>
                    <Camera class="w-4 h-4 text-muted-foreground" />
                </CardHeader>
                <CardContent>
                    <template v-if="loading">
                        <Skeleton class="h-8 w-16 mb-1" />
                        <Skeleton class="h-3 w-28" />
                    </template>
                    <template v-else>
                        <p class="text-3xl font-bold tracking-tight">{{ data?.snapshots_total }}</p>
                        <p class="mt-1 text-xs text-muted-foreground">
                            {{ formatBytes(data?.snapshots_total_size ?? 0) }} total
                        </p>
                    </template>
                </CardContent>
            </Card>

        </div>

        <!-- ── Charts ─────────────────────────────────────────────────────────── -->
        <div class="grid grid-cols-1 gap-4 lg:grid-cols-2">

            <!-- Jobs activity -->
            <Card>
                <CardHeader>
                    <CardTitle class="text-sm font-medium">Jobs — last 7 days</CardTitle>
                </CardHeader>
                <CardContent>
                    <Skeleton v-if="loading" class="h-44 w-full" />
                    <div v-else class="h-44">
                        <ChartContainer :config="jobsChartConfig" :cursor="true">
                            <VisXYContainer :data="jobsData">
                                <VisGroupedBar :x="(_d: any, i: number) => i"
                                    :y="[(d: any) => d.succeeded, (d: any) => d.failed]"
                                    :color="(_d: any, i: number) => i === 0 ? 'var(--color-succeeded)' : 'var(--color-failed)'"
                                    :rounded-corners="4" :barMinHeight="0" />
                                <VisAxis type="x" :tick-format="(v: number) => jobsData[Math.round(v)]?.date ?? ''" />
                                <VisAxis type="y" />
                                <ChartTooltip />
                                <ChartCrosshair :template="jobsTooltip"
                                    :color="(_d: any, i: number) => i === 0 ? 'var(--color-succeeded)' : 'var(--color-failed)'" />
                            </VisXYContainer>
                        </ChartContainer>
                    </div>
                </CardContent>
            </Card>

            <!-- Size backed up -->
            <Card>
                <CardHeader>
                    <CardTitle class="text-sm font-medium">Size backed up — last 7 days (GB)</CardTitle>
                </CardHeader>
                <CardContent>
                    <Skeleton v-if="loading" class="h-44 w-full" />
                    <div v-else class="h-44">
                        <ChartContainer :config="sizeChartConfig" :cursor="true">
                            <VisXYContainer :data="sizeData">
                                <VisArea :x="(_d: any, i: number) => i" :y="(d: any) => d.size"
                                    color="var(--color-size)" :opacity="0.3" :line="true" />
                                <VisAxis type="x" :tick-format="(v: number) => sizeData[Math.round(v)]?.date ?? ''" />
                                <VisAxis type="y" />
                                <ChartTooltip />
                                <ChartCrosshair :template="sizeTooltip" color="var(--color-primary)" />
                            </VisXYContainer>
                        </ChartContainer>
                    </div>
                </CardContent>
            </Card>

        </div>

        <!-- ── Recent jobs ────────────────────────────────────────────────────── -->
        <Card>
            <CardHeader>
                <CardTitle class="text-sm font-medium">Recent jobs</CardTitle>
            </CardHeader>
            <CardContent class="p-0 overflow-x-auto">
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
                        <template v-else-if="recentJobs.length === 0">
                            <TableRow>
                                <TableCell colspan="6">
                                    <div class="flex flex-col items-center justify-center gap-3 py-7 text-center">
                                        <div class="p-4 rounded-full bg-muted">
                                            <BriefcaseBusiness class="w-10 h-10 text-muted-foreground" />
                                        </div>
                                        <div>
                                            <p class="font-medium">No jobs yet</p>
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
                            <TableRow v-for="job in recentJobs" :key="job.id" class="cursor-pointer hover:bg-muted/50"
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
            </CardContent>
        </Card>

    </div>
</template>
