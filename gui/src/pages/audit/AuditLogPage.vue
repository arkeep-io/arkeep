<script setup lang="ts">
import { ref, computed, onMounted, watch } from 'vue'
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
import { Input } from '@/components/ui/input'
import { Skeleton } from '@/components/ui/skeleton'
import { ChevronLeft, ChevronRight, RefreshCw, ScrollText } from 'lucide-vue-next'
import { api } from '@/services/api'
import type { ApiResponse, AuditLog } from '@/types'

// ---------------------------------------------------------------------------
// State
// ---------------------------------------------------------------------------

const entries = ref<AuditLog[]>([])
const total = ref(0)
const loading = ref(true)
const error = ref<string | null>(null)
const expandedRow = ref<string | null>(null)

// Filters
const actionFilter = ref('all')
const emailFilter = ref('')
let emailDebounceTimer: ReturnType<typeof setTimeout> | null = null

// Pagination
const page = ref(1)
const pageSize = 50

const offset = computed(() => (page.value - 1) * pageSize)
const totalPages = computed(() => Math.max(1, Math.ceil(total.value / pageSize)))

// ---------------------------------------------------------------------------
// Action groups for the filter select
// ---------------------------------------------------------------------------

const ACTION_OPTIONS = [
    { value: 'all',           label: 'All actions' },
    { value: 'auth.',         label: 'Auth (login / logout)' },
    { value: 'policy.',       label: 'Policies' },
    { value: 'snapshot.',     label: 'Snapshots' },
    { value: 'agent.',        label: 'Agents' },
    { value: 'destination.',  label: 'Destinations' },
    { value: 'user.',         label: 'Users' },
    { value: 'settings.',     label: 'Settings' },
]

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

// actionBadgeClass returns Tailwind colour classes for the action category.
function actionBadgeClass(action: string): string {
    if (action.startsWith('auth.'))        return 'bg-green-500/10 text-green-700 dark:text-green-400 border-green-500/20'
    if (action.startsWith('policy.'))      return 'bg-blue-500/10 text-blue-700 dark:text-blue-400 border-blue-500/20'
    if (action.startsWith('snapshot.'))    return 'bg-violet-500/10 text-violet-700 dark:text-violet-400 border-violet-500/20'
    if (action.startsWith('agent.'))       return 'bg-cyan-500/10 text-cyan-700 dark:text-cyan-400 border-cyan-500/20'
    if (action.startsWith('destination.')) return 'bg-amber-500/10 text-amber-700 dark:text-amber-400 border-amber-500/20'
    if (action.startsWith('user.'))        return 'bg-rose-500/10 text-rose-700 dark:text-rose-400 border-rose-500/20'
    if (action.startsWith('settings.'))    return 'bg-orange-500/10 text-orange-700 dark:text-orange-400 border-orange-500/20'
    return 'bg-muted text-muted-foreground border-border'
}

function formatDate(iso: string): string {
    return new Date(iso).toLocaleString(undefined, { dateStyle: 'medium', timeStyle: 'short' })
}

// Abbreviate a UUID for display: show first 8 chars followed by ellipsis.
function shortID(id: string): string {
    if (!id) return '—'
    return id.length > 8 ? id.slice(0, 8) + '…' : id
}

function prettyDetails(details: Record<string, unknown>): string {
    try {
        return JSON.stringify(details, null, 2)
    } catch {
        return ''
    }
}

function toggleExpand(id: string) {
    expandedRow.value = expandedRow.value === id ? null : id
}

function hasDetails(entry: AuditLog): boolean {
    return Object.keys(entry.details).length > 0
}

// ---------------------------------------------------------------------------
// Data fetching
// ---------------------------------------------------------------------------

async function fetchEntries() {
    loading.value = true
    error.value = null
    try {
        const params = new URLSearchParams({
            limit: String(pageSize),
            offset: String(offset.value),
        })
        if (actionFilter.value !== 'all') params.set('action', actionFilter.value)
        if (emailFilter.value.trim()) {
            // The API doesn't have an email filter, so we use user_id workaround.
            // For now we filter client-side after fetch — or pass action only.
            // Note: the server filters by user_id UUID, not email. Email filter
            // is applied client-side on the loaded page.
        }

        const res = await api<ApiResponse<{ items: AuditLog[]; total: number }>>(
            `/api/v1/audit?${params.toString()}`
        )
        entries.value = res.data.items
        total.value = res.data.total
    } catch (e: any) {
        error.value = e?.message ?? 'Failed to load audit log.'
    } finally {
        loading.value = false
    }
}

// Client-side email filter applied on top of server-side action filter.
const filteredEntries = computed(() => {
    const q = emailFilter.value.trim().toLowerCase()
    if (!q) return entries.value
    return entries.value.filter(e => e.user_email.toLowerCase().includes(q))
})

// Reset to page 1 when filters change.
watch(actionFilter, () => { page.value = 1; fetchEntries() })
watch(emailFilter, () => {
    if (emailDebounceTimer) clearTimeout(emailDebounceTimer)
    emailDebounceTimer = setTimeout(() => { page.value = 1; fetchEntries() }, 400)
})
watch(page, fetchEntries)

onMounted(fetchEntries)
</script>

<template>
    <div class="flex flex-col gap-6 p-6">

        <!-- Page header -->
        <div class="flex items-center justify-between">
            <div>
                <h1 class="text-2xl font-semibold tracking-tight">Audit Log</h1>
                <p class="mt-1 text-sm text-muted-foreground">
                    Record of all significant admin actions.
                </p>
            </div>
            <Button variant="outline" size="icon" aria-label="Refresh" :disabled="loading" @click="fetchEntries">
                <RefreshCw class="w-4 h-4" :class="{ 'animate-spin': loading }" />
            </Button>
        </div>

        <!-- Error banner -->
        <Alert v-if="error" variant="destructive">
            <AlertDescription>{{ error }}</AlertDescription>
        </Alert>

        <!-- Filter bar -->
        <div class="flex items-center gap-3 flex-wrap">
            <Select v-model="actionFilter">
                <SelectTrigger class="w-52">
                    <SelectValue placeholder="All actions" />
                </SelectTrigger>
                <SelectContent>
                    <SelectItem v-for="opt in ACTION_OPTIONS" :key="opt.value" :value="opt.value">
                        {{ opt.label }}
                    </SelectItem>
                </SelectContent>
            </Select>

            <Input
                v-model="emailFilter"
                placeholder="Filter by user email…"
                class="w-60"
            />

            <span v-if="!loading" class="text-sm text-muted-foreground ml-auto">
                {{ total }} event{{ total !== 1 ? 's' : '' }}
            </span>
        </div>

        <!-- Table -->
        <div class="border rounded-md overflow-x-auto">
            <Table>
                <TableHeader>
                    <TableRow>
                        <TableHead class="w-40">Timestamp</TableHead>
                        <TableHead>User</TableHead>
                        <TableHead class="w-48">Action</TableHead>
                        <TableHead>Resource</TableHead>
                        <TableHead class="w-32">IP</TableHead>
                        <TableHead class="w-10"></TableHead>
                    </TableRow>
                </TableHeader>

                <TableBody>
                    <!-- Loading skeletons -->
                    <template v-if="loading">
                        <TableRow v-for="n in 8" :key="n">
                            <TableCell v-for="col in 6" :key="col">
                                <Skeleton class="w-full h-4" />
                            </TableCell>
                        </TableRow>
                    </template>

                    <!-- Empty state -->
                    <template v-else-if="filteredEntries.length === 0">
                        <TableRow>
                            <TableCell colspan="6">
                                <div class="flex flex-col items-center justify-center gap-3 py-16 text-center">
                                    <div class="p-4 rounded-full bg-muted">
                                        <ScrollText class="w-10 h-10 text-muted-foreground" />
                                    </div>
                                    <div>
                                        <p class="font-medium">No audit events yet</p>
                                        <p class="mt-1 text-sm text-muted-foreground">
                                            Admin actions will be recorded here automatically.
                                        </p>
                                    </div>
                                </div>
                            </TableCell>
                        </TableRow>
                    </template>

                    <!-- Data rows -->
                    <template v-else>
                        <template v-for="entry in filteredEntries" :key="entry.id">
                            <TableRow
                                :class="{ 'cursor-pointer hover:bg-muted/50': hasDetails(entry) }"
                                @click="hasDetails(entry) && toggleExpand(entry.id)"
                            >
                                <TableCell class="text-sm text-muted-foreground whitespace-nowrap">
                                    {{ formatDate(entry.created_at) }}
                                </TableCell>
                                <TableCell class="text-sm">{{ entry.user_email }}</TableCell>
                                <TableCell>
                                    <Badge variant="outline" :class="actionBadgeClass(entry.action)">
                                        {{ entry.action }}
                                    </Badge>
                                </TableCell>
                                <TableCell class="text-sm text-muted-foreground">
                                    <span v-if="entry.resource_type" class="font-medium text-foreground">{{ entry.resource_type }}</span>
                                    <span v-if="entry.resource_id" class="ml-1 font-mono text-xs">{{ shortID(entry.resource_id) }}</span>
                                    <span v-if="!entry.resource_type && !entry.resource_id">—</span>
                                </TableCell>
                                <TableCell class="text-xs text-muted-foreground font-mono">
                                    {{ entry.ip_address || '—' }}
                                </TableCell>
                                <TableCell class="text-right">
                                    <span v-if="hasDetails(entry)" class="text-xs text-muted-foreground select-none">
                                        {{ expandedRow === entry.id ? '▲' : '▼' }}
                                    </span>
                                </TableCell>
                            </TableRow>

                            <!-- Expanded details row -->
                            <TableRow v-if="expandedRow === entry.id" :key="entry.id + '-details'">
                                <TableCell colspan="6" class="bg-muted/30 p-0">
                                    <pre class="p-4 text-xs font-mono text-muted-foreground whitespace-pre-wrap break-all">{{ prettyDetails(entry.details) }}</pre>
                                </TableCell>
                            </TableRow>
                        </template>
                    </template>
                </TableBody>
            </Table>
        </div>

        <!-- Pagination -->
        <div v-if="totalPages > 1" class="flex items-center justify-between">
            <span class="text-sm text-muted-foreground">
                Page {{ page }} of {{ totalPages }}
            </span>
            <div class="flex items-center gap-2">
                <Button variant="outline" size="icon" :disabled="page <= 1 || loading" @click="page--">
                    <ChevronLeft class="w-4 h-4" />
                </Button>
                <Button variant="outline" size="icon" :disabled="page >= totalPages || loading" @click="page++">
                    <ChevronRight class="w-4 h-4" />
                </Button>
            </div>
        </div>

    </div>
</template>
