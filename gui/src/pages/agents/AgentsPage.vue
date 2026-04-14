<script setup lang="ts">
import { ref, onMounted, onUnmounted, computed } from 'vue'
import { useRouter } from 'vue-router'
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
    DropdownMenu,
    DropdownMenuContent,
    DropdownMenuItem,
    DropdownMenuSeparator,
    DropdownMenuTrigger,
} from '@/components/ui/dropdown-menu'
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
    MoreHorizontal,
    PencilLine,
    Trash2,
    Server,
    RefreshCw,
    Eye,
} from 'lucide-vue-next'
import { api } from '@/services/api'
import { wsClient } from '@/services/websocket'
import type { Agent, AgentStatus, ApiResponse } from '@/types'
import AgentSheet from '@/components/agents/AgentSheet.vue'
import UpgradeIndicator from '@/components/shared/UpgradeIndicator.vue'
import { useUpdateStore } from '@/stores/update'

// The list endpoint returns { items, total } — not aligned with PaginatedResponse
// in types/index.ts which reflects a different shape. We type it inline here.
interface AgentListResponse {
    items: Agent[]
    total: number
}

// ---------------------------------------------------------------------------
// State
// ---------------------------------------------------------------------------

const router = useRouter()
const updateStore = useUpdateStore()
const authStore = useAuthStore()

const agents = ref<Agent[]>([])
const total = ref(0)
const loading = ref(true)
const error = ref<string | null>(null)

const page = ref(1)
const pageSize = 20

// Tracks live status overrides received via WebSocket.
// Keyed by agent ID so we only override the field that changed without
// re-fetching the full list.
const liveStatus = ref<Record<string, AgentStatus>>({})

// Delete confirmation dialog
const deleteDialogOpen = ref(false)
const agentToDelete = ref<Agent | null>(null)
const deleteLoading = ref(false)

// Edit sheet
const editSheetOpen = ref(false)
const agentToEdit = ref<Agent | null>(null)

// WebSocket unsubscribe handles — one per visible agent.
const unsubscribers = ref<Array<() => void>>([])

// ---------------------------------------------------------------------------
// Computed
// ---------------------------------------------------------------------------

const offset = computed(() => (page.value - 1) * pageSize)
const totalPages = computed(() => Math.ceil(total.value / pageSize))

// Merge fetched agents with live status overrides.
const mergedAgents = computed(() =>
    agents.value.map((a) => ({
        ...a,
        status: liveStatus.value[a.id] ?? a.status,
    }))
)

// ---------------------------------------------------------------------------
// Data fetching
// ---------------------------------------------------------------------------

async function fetchAgents() {
    loading.value = true
    error.value = null
    try {
        const res = await api<ApiResponse<AgentListResponse>>(
            `/api/v1/agents?limit=${pageSize}&offset=${offset.value}`
        )
        agents.value = res.data.items
        total.value = res.data.total
        subscribeToAgents()
    } catch (e: any) {
        error.value = e?.message ?? 'Failed to load agents'
    } finally {
        loading.value = false
    }
}

// ---------------------------------------------------------------------------
// WebSocket subscriptions
// ---------------------------------------------------------------------------

// Subscribe to agent:<id> for every agent currently in the list.
// Previous subscriptions are torn down first to avoid duplicates on page change.
function subscribeToAgents() {
    teardownSubscriptions()

    for (const agent of agents.value) {
        const topic = `agent:${agent.id}`
        const unsub = wsClient.subscribe(topic, (msg: any) => {
            if (msg?.type === 'agent.status' && msg?.payload?.status) {
                liveStatus.value = {
                    ...liveStatus.value,
                    [agent.id]: msg.payload.status as AgentStatus,
                }
            }
        })
        unsubscribers.value.push(unsub)
    }
}

function teardownSubscriptions() {
    for (const unsub of unsubscribers.value) unsub()
    unsubscribers.value = []
}

// ---------------------------------------------------------------------------
// Pagination
// ---------------------------------------------------------------------------

async function goToPage(p: number) {
    if (p < 1 || p > totalPages.value) return
    page.value = p
    await fetchAgents()
}

// ---------------------------------------------------------------------------
// Delete
// ---------------------------------------------------------------------------

function openDeleteDialog(agent: Agent) {
    agentToDelete.value = agent
    deleteDialogOpen.value = true
}

async function confirmDelete() {
    if (!agentToDelete.value) return
    deleteLoading.value = true
    try {
        await api(`/api/v1/agents/${agentToDelete.value.id}`, { method: 'DELETE' })
        deleteDialogOpen.value = false
        agentToDelete.value = null
        // If we deleted the last item on a page, go back one page.
        if (agents.value.length === 1 && page.value > 1) {
            page.value--
        }
        await fetchAgents()
    } catch (e: any) {
        error.value = e?.message ?? 'Failed to delete agent'
    } finally {
        deleteLoading.value = false
    }
}

// ---------------------------------------------------------------------------
// Navigation
// ---------------------------------------------------------------------------

function goToDetail(agent: Agent) {
    router.push(`/agents/${agent.id}`)
}

function openEditSheet(agent: Agent) {
    agentToEdit.value = agent
    editSheetOpen.value = true
}

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

// Maps agent status string to a Badge variant.
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

function statusLabel(status: AgentStatus): string {
    switch (status) {
        case 'online': return 'Online'
        case 'offline': return 'Offline'
        case 'unknown': return 'Unknown'
        default: return status
    }
}

function formatLastSeen(lastSeenAt: string | null): string {
    if (!lastSeenAt) return '—'
    const date = new Date(lastSeenAt)
    if (isNaN(date.getTime())) return '—'
    const now = Date.now()
    const diff = Math.floor((now - date.getTime()) / 1000)
    if (diff < 60) return `${diff}s ago`
    if (diff < 3600) return `${Math.floor(diff / 60)}m ago`
    if (diff < 86400) return `${Math.floor(diff / 3600)}h ago`
    return date.toLocaleDateString()
}

// Returns true when the agent's version is older than the latest known release.
function isAgentOutdated(agentVersion: string): boolean {
    const latest = updateStore.latestVersion
    if (!latest || !agentVersion) return false
    return compareVersions(agentVersion, latest) < 0
}

// Compares two semver strings (with or without leading 'v').
// Returns -1 if a < b, 0 if equal, 1 if a > b.
function compareVersions(a: string, b: string): number {
    const parse = (v: string) =>
        v.replace(/^v/, '').split('.').map((p) => parseInt(p.split('-')[0] ?? '0', 10))
    const pa = parse(a)
    const pb = parse(b)
    for (let i = 0; i < Math.max(pa.length, pb.length); i++) {
        const diff = (pa[i] ?? 0) - (pb[i] ?? 0)
        if (diff !== 0) return diff < 0 ? -1 : 1
    }
    return 0
}

// ---------------------------------------------------------------------------
// Lifecycle
// ---------------------------------------------------------------------------

onMounted(fetchAgents)

onUnmounted(teardownSubscriptions)
</script>

<template>
    <div class="flex flex-col gap-6 p-6">

        <!-- Header -->
        <div class="flex items-center justify-between">
            <div>
                <h1 class="text-2xl font-semibold tracking-tight">Agents</h1>
                <p class="mt-1 text-sm text-muted-foreground">
                    Machines registered for backup management
                </p>
            </div>
            <Button variant="outline" size="icon" aria-label="Refresh" :disabled="loading" @click="fetchAgents">
                <RefreshCw class="w-4 h-4" :class="{ 'animate-spin': loading }" />
            </Button>
        </div>

        <!-- Error banner -->
        <Alert v-if="error" variant="destructive">
            <AlertDescription>{{ error }}</AlertDescription>
        </Alert>

        <!-- Table -->
        <div class="border rounded-md overflow-x-auto">
            <Table>
                <TableHeader>
                    <TableRow>
                        <TableHead>Name</TableHead>
                        <TableHead>Hostname</TableHead>
                        <TableHead>OS / Arch</TableHead>
                        <TableHead>Version</TableHead>
                        <TableHead>Status</TableHead>
                        <TableHead>Last Seen</TableHead>
                        <TableHead class="w-13" />
                    </TableRow>
                </TableHeader>

                <TableBody>
                    <!-- Loading skeletons -->
                    <template v-if="loading">
                        <TableRow v-for="n in 5" :key="n">
                            <TableCell v-for="col in 7" :key="col">
                                <Skeleton class="w-full h-4" />
                            </TableCell>
                        </TableRow>
                    </template>

                    <!-- Empty state -->
                    <template v-else-if="mergedAgents.length === 0">
                        <TableRow>
                            <TableCell colspan="7">
                                <div class="flex flex-col items-center justify-center gap-3 py-16 text-center">
                                    <div class="p-4 rounded-full bg-muted">
                                        <Server class="w-10 h-10 text-muted-foreground" />
                                    </div>
                                    <div>
                                        <p class="font-medium">No agents connected</p>
                                        <p class="mt-1 text-sm text-muted-foreground">
                                            Install and start the agent on a machine to see it appear here.
                                        </p>
                                    </div>
                                </div>
                            </TableCell>
                        </TableRow>
                    </template>

                    <!-- Data rows -->
                    <template v-else>
                        <TableRow v-for="agent in mergedAgents" :key="agent.id"
                            class="cursor-pointer hover:bg-muted/50"
                            tabindex="0"
                            role="link"
                            :aria-label="`View agent ${agent.name}`"
                            @click="goToDetail(agent)"
                            @keyup.enter="goToDetail(agent)">
                            <TableCell class="font-medium">{{ agent.name }}</TableCell>
                            <TableCell class="font-mono text-sm text-muted-foreground">
                                {{ agent.hostname || '—' }}
                            </TableCell>
                            <TableCell class="text-sm text-muted-foreground">
                                <span v-if="agent.os || agent.arch">
                                    {{ [agent.os, agent.arch].filter(Boolean).join(' / ') }}
                                </span>
                                <span v-else>—</span>
                            </TableCell>
                            <TableCell class="font-mono text-sm text-muted-foreground">
                                <div class="flex items-center gap-1.5">
                                    <span>{{ agent.version || '—' }}</span>
                                    <UpgradeIndicator
                                        v-if="agent.version"
                                        :show="isAgentOutdated(agent.version)"
                                        :version="updateStore.latestVersion"
                                        tooltip-side="top"
                                    />
                                </div>
                            </TableCell>
                            <TableCell>
                                <!-- Live status dot + badge -->
                                <Badge :variant="statusVariant(agent.status)" class="gap-1.5" :class="statusClass(agent.status)">
                                    <span class="inline-block h-1.5 w-1.5 rounded-full" :class="{
                                        'bg-emerald-400': agent.status === 'online',
                                        'bg-muted-foreground': agent.status === 'offline',
                                        'bg-yellow-400': agent.status === 'unknown',
                                    }" />
                                    {{ statusLabel(agent.status) }}
                                </Badge>
                            </TableCell>
                            <TableCell class="text-sm text-muted-foreground">
                                {{ formatLastSeen(agent.last_seen_at) }}
                            </TableCell>

                            <!-- Actions dropdown — stopPropagation so row click doesn't fire -->
                            <TableCell @click.stop>
                                <DropdownMenu>
                                    <DropdownMenuTrigger as-child>
                                        <Button variant="ghost" size="icon" class="w-8 h-8">
                                            <MoreHorizontal class="w-4 h-4" />
                                            <span class="sr-only">Open actions</span>
                                        </Button>
                                    </DropdownMenuTrigger>
                                    <DropdownMenuContent align="end">
                                        <DropdownMenuItem @click="goToDetail(agent)">
                                            <Eye class="w-4 h-4 mr-2" />
                                            View
                                        </DropdownMenuItem>
                                        <DropdownMenuItem @click="openEditSheet(agent)">
                                            <PencilLine class="w-4 h-4 mr-2" />
                                            Edit
                                        </DropdownMenuItem>
                                        <DropdownMenuSeparator v-if="authStore.isAdmin" />
                                        <DropdownMenuItem v-if="authStore.isAdmin"
                                            class="text-destructive focus:text-destructive"
                                            @click="openDeleteDialog(agent)">
                                            <Trash2 class="w-4 h-4 mr-2" />
                                            Delete
                                        </DropdownMenuItem>
                                    </DropdownMenuContent>
                                </DropdownMenu>
                            </TableCell>
                        </TableRow>
                    </template>
                </TableBody>
            </Table>
        </div>

        <!-- Pagination -->
        <div v-if="!loading && totalPages > 1" class="flex items-center justify-between text-sm text-muted-foreground">
            <span>
                Showing {{ offset + 1 }}–{{ Math.min(offset + pageSize, total) }} of {{ total }} agents
            </span>
            <div class="flex items-center gap-2">
                <Button variant="outline" size="sm" :disabled="page === 1" @click="goToPage(page - 1)">
                    Previous
                </Button>
                <span class="px-2">{{ page }} / {{ totalPages }}</span>
                <Button variant="outline" size="sm" :disabled="page === totalPages" @click="goToPage(page + 1)">
                    Next
                </Button>
            </div>
        </div>

    </div>

    <!-- Edit agent sheet -->
    <AgentSheet v-if="agentToEdit" :agent="agentToEdit" :open="editSheetOpen" @update:open="editSheetOpen = $event"
        @saved="fetchAgents" />

    <!-- Delete confirmation dialog -->
    <AlertDialog :open="deleteDialogOpen" @update:open="deleteDialogOpen = $event">
        <AlertDialogContent>
            <AlertDialogHeader>
                <AlertDialogTitle>Delete agent?</AlertDialogTitle>
                <AlertDialogDescription>
                    <span v-if="agentToDelete">
                        <strong>{{ agentToDelete.name }}</strong> will be soft-deleted. Existing jobs and
                        snapshots associated with this agent will be retained.
                    </span>
                </AlertDialogDescription>
            </AlertDialogHeader>
            <AlertDialogFooter>
                <AlertDialogCancel :disabled="deleteLoading">Cancel</AlertDialogCancel>
                <AlertDialogAction variant="destructive"
                    :disabled="deleteLoading" @click="confirmDelete">
                    {{ deleteLoading ? 'Deleting…' : 'Delete' }}
                </AlertDialogAction>
            </AlertDialogFooter>
        </AlertDialogContent>
    </AlertDialog>
</template>