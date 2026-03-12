<script setup lang="ts">
import { ref, computed, onMounted } from 'vue'
import { useRouter } from 'vue-router'
import { Bar, Line } from 'vue-chartjs'
import {
    Chart as ChartJS,
    CategoryScale,
    LinearScale,
    BarElement,
    LineElement,
    PointElement,
    Filler,
    Tooltip,
    Legend,
} from 'chart.js'
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
import { Server, ShieldCheck, BriefcaseBusiness, Camera, RefreshCw } from 'lucide-vue-next'
import { api } from '@/services/api'
import { useTheme } from '@/composables/useTheme'
import type { ApiResponse, Job } from '@/types'

// ---------------------------------------------------------------------------
// Chart.js registration
// ---------------------------------------------------------------------------

ChartJS.register(
    CategoryScale,
    LinearScale,
    BarElement,
    LineElement,
    PointElement,
    Filler,
    Tooltip,
    Legend,
)

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
// Theme — chart colours update when the user toggles dark mode
// ---------------------------------------------------------------------------

const { isDark } = useTheme()

const gridColor = computed(() => isDark.value ? 'rgba(255,255,255,0.08)' : 'rgba(0,0,0,0.08)')
const labelColor = computed(() => isDark.value ? '#a1a1aa' : '#71717a')

// ---------------------------------------------------------------------------
// State
// ---------------------------------------------------------------------------

const router = useRouter()
const loading = ref(true)
const error = ref<string | null>(null)
const data = ref<DashboardData | null>(null)
const recentJobs = ref<Job[]>([])

// ---------------------------------------------------------------------------
// Chart helpers
// ---------------------------------------------------------------------------

// shortLabel converts "YYYY-MM-DD" → "DD/MM" for the x-axis.
function shortLabel(iso: string): string {
    const [, m, d] = iso.split('-')
    return `${d}/${m}`
}

const chartLabels = computed(() =>
    data.value?.job_activity.map(d => shortLabel(d.date)) ?? []
)

const jobsChartData = computed(() => ({
    labels: chartLabels.value,
    datasets: [
        {
            label: 'Succeeded',
            data: data.value?.job_activity.map(d => d.succeeded) ?? [],
            backgroundColor: '#22c55e',
            borderRadius: 4,
        },
        {
            label: 'Failed',
            data: data.value?.job_activity.map(d => d.failed) ?? [],
            backgroundColor: '#ef4444',
            borderRadius: 4,
        },
    ],
}))

const sizeChartData = computed(() => ({
    labels: chartLabels.value,
    datasets: [
        {
            label: 'Backed up (GB)',
            data: data.value?.size_activity.map(d =>
                parseFloat((d.size_bytes / 1073741824).toFixed(2))
            ) ?? [],
            borderColor: '#6366f1',
            backgroundColor: isDark.value ? 'rgba(99,102,241,0.15)' : 'rgba(99,102,241,0.10)',
            fill: true,
            tension: 0.4,
            pointRadius: 3,
        },
    ],
}))

// Shared options for both charts — grid and label colours react to the theme.
const chartOptions = computed(() => ({
    responsive: true,
    maintainAspectRatio: false,
    plugins: {
        legend: {
            labels: { color: labelColor.value, boxWidth: 12, padding: 16 },
        },
    },
    scales: {
        x: {
            grid: { color: gridColor.value },
            ticks: { color: labelColor.value },
        },
        y: {
            beginAtZero: true,
            grid: { color: gridColor.value },
            ticks: { color: labelColor.value },
        },
    },
}))

// ---------------------------------------------------------------------------
// Misc helpers
// ---------------------------------------------------------------------------

function statusVariant(status: string): 'default' | 'secondary' | 'destructive' | 'outline' {
    switch (status) {
        case 'succeeded': return 'default'
        case 'running': return 'outline'
        case 'failed': return 'destructive'
        default: return 'secondary'
    }
}

function formatDate(iso: string | null): string {
    if (!iso) return '—'
    return new Date(iso).toLocaleString(undefined, { dateStyle: 'medium', timeStyle: 'short' })
}

function formatBytes(bytes: number): string {
    if (bytes === 0) return '0 B'
    const k = 1024
    const sizes = ['B', 'KB', 'MB', 'GB', 'TB']
    const i = Math.floor(Math.log(bytes) / Math.log(k))
    return `${parseFloat((bytes / Math.pow(k, i)).toFixed(1))} ${sizes[i]}`
}

// ---------------------------------------------------------------------------
// Data fetching — dashboard aggregates + last 10 jobs in parallel
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
            <Button variant="outline" size="icon" :disabled="loading" @click="fetchAll">
                <RefreshCw class="w-4 h-4" :class="{ 'animate-spin': loading }" />
            </Button>
        </div>

        <!-- Error banner -->
        <Alert v-if="error" variant="destructive">
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
                        <Bar :data="jobsChartData" :options="chartOptions" />
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
                        <Line :data="sizeChartData" :options="chartOptions" />
                    </div>
                </CardContent>
            </Card>

        </div>

        <!-- ── Recent jobs ────────────────────────────────────────────────────── -->
        <Card>
            <CardHeader>
                <CardTitle class="text-sm font-medium">Recent jobs</CardTitle>
            </CardHeader>
            <CardContent class="p-0">
                <Table>
                    <TableHeader>
                        <TableRow>
                            <TableHead>Policy</TableHead>
                            <TableHead>Agent</TableHead>
                            <TableHead>Status</TableHead>
                            <TableHead>Started</TableHead>
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
                        <template v-else-if="recentJobs.length === 0">
                            <TableRow>
                                <TableCell colspan="4">
                                    <div class="flex flex-col items-center justify-center gap-2 py-10 text-center">
                                        <BriefcaseBusiness class="w-7 h-7 text-muted-foreground" />
                                        <p class="text-sm text-muted-foreground">No jobs yet.</p>
                                    </div>
                                </TableCell>
                            </TableRow>
                        </template>

                        <!-- Data rows -->
                        <template v-else>
                            <TableRow v-for="job in recentJobs" :key="job.id" class="cursor-pointer"
                                @click="router.push(`/jobs/${job.id}`)">
                                <TableCell class="font-medium">{{ job.policy_name }}</TableCell>
                                <TableCell class="text-muted-foreground">{{ job.agent_name }}</TableCell>
                                <TableCell>
                                    <Badge :variant="statusVariant(job.status)">
                                        {{ job.status.charAt(0).toUpperCase() + job.status.slice(1) }}
                                    </Badge>
                                </TableCell>
                                <TableCell class="text-sm text-muted-foreground">
                                    {{ formatDate(job.started_at) }}
                                </TableCell>
                            </TableRow>
                        </template>

                    </TableBody>
                </Table>
            </CardContent>
        </Card>

    </div>
</template>