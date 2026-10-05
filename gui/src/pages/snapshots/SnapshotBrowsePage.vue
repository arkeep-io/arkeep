<script setup lang="ts">
// SnapshotBrowsePage — file browser for one snapshot.
//
// The tree lists the snapshot one directory at a time through an agent that
// can reach its repository (the snapshot's own agent by default; any other
// online agent can be chosen, e.g. when the original machine is gone).
// Admins can download a file, or a directory as a ZIP archive, and restore a
// selection.

import { onMounted, ref } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { ArchiveRestore, ArrowLeft, Download, Loader2, RefreshCw } from '@lucide/vue'
import { Alert, AlertDescription } from '@/components/ui/alert'
import { AsyncCombobox } from '@/components/ui/async-combobox'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Skeleton } from '@/components/ui/skeleton'
import RestoreSheet from '@/components/snapshots/RestoreSheet.vue'
import SnapshotFileTree from '@/components/snapshots/SnapshotFileTree.vue'
import { formatDate } from '@/lib/jobUtils'
import { api } from '@/services/api'
import { useAuthStore } from '@/stores/auth'
import type { ApiResponse, Snapshot, SnapshotBrowseResponse, SnapshotFileEntry } from '@/types'

defineOptions({ inheritAttrs: false })

const route = useRoute()
const router = useRouter()
const authStore = useAuthStore()
const snapshotId = route.params.id as string

const snapshot = ref<Snapshot | null>(null)
const loading = ref(true)
const error = ref<string | null>(null)

// The agent that reads the repository.
const agentId = ref('')
const agentName = ref('')

const entries = ref<SnapshotFileEntry[]>([])
const browsing = ref(false)
const browseError = ref<string | null>(null)
const selectedPaths = ref<string[]>([])

const downloadError = ref<string | null>(null)
const restoreOpen = ref(false)

async function fetchSnapshot() {
    loading.value = true
    error.value = null
    try {
        const res = await api<ApiResponse<Snapshot>>(`/api/v1/snapshots/${snapshotId}`)
        snapshot.value = res.data
        // An imported snapshot has no agent: the user picks one first.
        agentId.value = res.data.agent_id
        agentName.value = res.data.agent_name
        // A deleted destination's credentials were erased: there is nothing
        // to browse with, only a full restore with re-entered credentials.
        if (!res.data.destination_deleted) await browseRoot()
    } catch (e: any) {
        error.value = e?.data?.error?.message ?? e?.message ?? 'Failed to load snapshot.'
    } finally {
        loading.value = false
    }
}

// loadChildren fetches the direct children of one directory; an empty path
// lists the snapshot root.
async function loadChildren(path: string): Promise<SnapshotFileEntry[]> {
    const params = new URLSearchParams({ agent_id: agentId.value })
    if (path) params.set('path', path)
    const res = await api<ApiResponse<SnapshotBrowseResponse>>(`/api/v1/snapshots/${snapshotId}/browse?${params}`)
    return res.data.entries ?? []
}

async function browseRoot() {
    if (!agentId.value) return
    browsing.value = true
    browseError.value = null
    try {
        entries.value = await loadChildren('')
        selectedPaths.value = []
    } catch (e: any) {
        entries.value = []
        browseError.value = e?.data?.error?.message ?? e?.message ?? 'Failed to browse snapshot.'
    } finally {
        browsing.value = false
    }
}

function onAgentSelected(item: Record<string, unknown> | null) {
    agentName.value = (item?.name as string | undefined) ?? ''
    browseRoot()
}

// download asks for a single-use download URL and hands it to the browser,
// which streams the file itself. A null entry downloads the whole snapshot.
async function download(entry: SnapshotFileEntry | null) {
    downloadError.value = null
    try {
        const res = await api<ApiResponse<{ url: string }>>(`/api/v1/snapshots/${snapshotId}/download`, {
            method: 'POST',
            body: { path: entry?.path ?? '', type: entry?.type ?? 'dir', agent_id: agentId.value },
        })
        const a = document.createElement('a')
        a.href = res.data.url
        a.download = ''
        a.click()
    } catch (e: any) {
        downloadError.value = e?.data?.error?.message ?? e?.message ?? 'Failed to start the download.'
    }
}

onMounted(fetchSnapshot)
</script>

<template>
    <div class="flex flex-col gap-6 p-6">

        <!-- ── Header ─────────────────────────────────────────────────────── -->
        <div class="flex items-start justify-between gap-4">
            <div class="flex items-center gap-3">
                <Button variant="ghost" size="icon" @click="router.push('/snapshots')">
                    <ArrowLeft class="w-4 h-4" />
                </Button>
                <div>
                    <div v-if="loading" class="flex flex-col gap-1.5">
                        <Skeleton class="w-48 h-6" />
                        <Skeleton class="w-32 h-4" />
                    </div>
                    <template v-else-if="snapshot">
                        <div class="flex items-center gap-2.5 flex-wrap">
                            <h1 class="text-2xl font-semibold tracking-tight">
                                Snapshot <span class="font-mono">{{ snapshot.restic_snapshot_id.slice(0, 8) }}</span>
                            </h1>
                            <Badge v-if="snapshot.is_imported" variant="outline">Imported</Badge>
                            <Badge v-if="snapshot.destination_deleted" variant="outline">Destination deleted</Badge>
                        </div>
                        <p class="mt-0.5 text-sm text-muted-foreground">
                            <span v-if="snapshot.policy_name">{{ snapshot.policy_name }} · </span>
                            {{ snapshot.destination_name }} · {{ formatDate(snapshot.created_at) }}
                            <span v-if="snapshot.hostname"> · {{ snapshot.hostname }}</span>
                        </p>
                    </template>
                </div>
            </div>

            <div v-if="!loading && snapshot && authStore.isAdmin && !snapshot.destination_deleted" class="flex items-center gap-2">
                <Button variant="outline" size="sm" :disabled="!agentId || browsing" @click="download(null)">
                    <Download class="w-4 h-4 mr-1.5" />
                    Download all (ZIP)
                </Button>
                <Button variant="outline" size="sm" :disabled="selectedPaths.length === 0"
                    @click="restoreOpen = true">
                    <ArchiveRestore class="w-4 h-4 mr-1.5" />
                    Restore selected{{ selectedPaths.length > 0 ? ` (${selectedPaths.length})` : '' }}
                </Button>
            </div>
        </div>

        <Alert v-if="error" variant="destructive">
            <AlertDescription>{{ error }}</AlertDescription>
        </Alert>

        <!-- ── Deleted destination ─────────────────────────────────────── -->
        <div v-if="!loading && snapshot?.destination_deleted"
            class="flex flex-col items-center gap-3 rounded-md border p-10 text-center">
            <p class="font-medium">The destination of this snapshot was deleted</p>
            <p class="max-w-lg text-sm text-muted-foreground">
                Arkeep erased the stored credentials of <span class="font-medium">{{ snapshot.destination_name }}</span>,
                so it can no longer browse or download files from this snapshot. The backup data is still on the
                storage: you can restore the whole snapshot by entering the destination's credentials again.
            </p>
            <Button v-if="authStore.isAdmin" size="sm" @click="restoreOpen = true">
                <ArchiveRestore class="w-4 h-4 mr-1.5" />
                Restore snapshot
            </Button>
        </div>

        <template v-else-if="!loading && snapshot">
            <!-- ── Agent ──────────────────────────────────────────────────── -->
            <div class="flex flex-wrap items-end gap-2">
                <div class="w-72">
                    <p class="text-xs text-muted-foreground uppercase tracking-wide mb-1">Read with agent</p>
                    <AsyncCombobox
                        endpoint="/api/v1/agents?status=online"
                        :model-value="agentId"
                        :initial-label="agentName"
                        placeholder="Select an agent…"
                        @update:model-value="agentId = $event"
                        @update:item="onAgentSelected"
                    />
                </div>
                <Button variant="outline" size="icon" :disabled="!agentId || browsing" @click="browseRoot">
                    <RefreshCw class="w-4 h-4" />
                </Button>
            </div>

            <Alert v-if="browseError || downloadError" variant="destructive">
                <AlertDescription>{{ browseError ?? downloadError }}</AlertDescription>
            </Alert>

            <!-- ── Files ──────────────────────────────────────────────────── -->
            <div class="border rounded-md">
                <p v-if="!agentId" class="p-6 text-sm text-center text-muted-foreground">
                    Select an online agent that can reach this snapshot's repository to browse its files.
                </p>
                <div v-else-if="browsing" class="flex items-center justify-center gap-2 p-6 text-sm text-muted-foreground">
                    <Loader2 class="w-4 h-4 animate-spin" />
                    Opening the repository… this can take a while on a large remote repository.
                </div>
                <p v-else-if="entries.length === 0 && !browseError" class="p-6 text-sm text-center text-muted-foreground">
                    This snapshot is empty.
                </p>
                <SnapshotFileTree
                    v-else-if="entries.length > 0"
                    v-model="selectedPaths"
                    :entries="entries"
                    :load-children="loadChildren"
                    :selectable="authStore.isAdmin"
                    :downloadable="authStore.isAdmin"
                    class="max-h-[calc(100vh-18rem)] overflow-y-auto py-1"
                    @download="download"
                />
            </div>
        </template>

        <RestoreSheet
            v-if="authStore.isAdmin"
            :open="restoreOpen"
            :snapshot="snapshot"
            :preset-paths="selectedPaths"
            :preset-agent="agentId ? { id: agentId, name: agentName } : null"
            @update:open="restoreOpen = $event"
        />
    </div>
</template>
