<script setup lang="ts">
import { computed } from 'vue'
import {
    Sheet,
    SheetContent,
    SheetDescription,
    SheetHeader,
    SheetTitle,
} from '@/components/ui/sheet'
import { formatBytes, formatDate, formatDuration } from '@/lib/jobUtils'
import type { Snapshot } from '@/types'

// ---------------------------------------------------------------------------
// Props & emits
// ---------------------------------------------------------------------------

const props = defineProps<{
    open: boolean
    snapshot: Snapshot | null
}>()

const emit = defineEmits<{
    'update:open': [value: boolean]
}>()

// ---------------------------------------------------------------------------
// Sections
// ---------------------------------------------------------------------------

interface Row {
    label: string
    value: string
    mono?: boolean
}

interface Section {
    title: string
    rows: Row[]
}

function count(n: number): string {
    return n.toLocaleString()
}

// sections lists what is known about the snapshot. The backup and file
// figures come from the restic summary, so they are left out when it is
// unknown; what the snapshot added after compression is always recorded.
const sections = computed<Section[]>(() => {
    const s = props.snapshot
    if (!s) return []
    const sum = s.summary
    const out: Section[] = [{
        title: 'Snapshot',
        rows: [
            { label: 'Snapshot ID', value: s.restic_snapshot_id, mono: true },
            { label: 'Policy', value: s.policy_name || '—' },
            { label: 'Destination', value: s.destination_name || '—' },
            { label: 'Created', value: formatDate(s.created_at) },
        ],
    }]
    if (sum) {
        out.push({
            title: 'Backup',
            rows: [
                { label: 'Started', value: formatDate(sum.backup_start) },
                { label: 'Duration', value: formatDuration(sum.backup_start, sum.backup_end) },
            ],
        })
    }
    out.push({
        title: 'Data',
        rows: [
            ...(sum ? [
                { label: 'Processed', value: formatBytes(sum.total_bytes_processed) },
                { label: 'Added', value: formatBytes(sum.data_added) },
            ] : []),
            { label: 'Stored (compressed)', value: formatBytes(s.size_bytes) },
        ],
    })
    if (sum) {
        out.push({
            title: 'Files',
            rows: [
                { label: 'Processed', value: count(sum.total_files_processed) },
                { label: 'New', value: count(sum.files_new) },
                { label: 'Changed', value: count(sum.files_changed) },
                { label: 'Unmodified', value: count(sum.files_unmodified) },
            ],
        })
    }
    return out
})
</script>

<template>
    <Sheet :open="props.open" @update:open="emit('update:open', $event)">
        <SheetContent class="sm:max-w-md flex flex-col">
            <SheetHeader>
                <SheetTitle>Snapshot details</SheetTitle>
                <SheetDescription>
                    <span class="font-mono">{{ snapshot?.restic_snapshot_id?.slice(0, 8) }}</span>
                    as recorded by restic when the backup ran.
                </SheetDescription>
            </SheetHeader>

            <div class="flex flex-col gap-6 px-4 pb-6 flex-1 overflow-y-auto">
                <p v-if="snapshot && !snapshot.summary" data-testid="no-summary"
                    class="text-sm text-muted-foreground">
                    No backup details are recorded for this snapshot yet: the next snapshot sync of its
                    destination reads them from the repository. Snapshots made with restic older than 0.17
                    have none.
                </p>

                <section v-for="section in sections" :key="section.title">
                    <h3 class="mb-2 text-sm font-medium">{{ section.title }}</h3>
                    <dl class="grid grid-cols-[auto_1fr] gap-x-6 gap-y-1.5 text-sm">
                        <template v-for="row in section.rows" :key="row.label">
                            <dt class="text-muted-foreground">{{ row.label }}</dt>
                            <dd class="text-right" :class="row.mono ? 'font-mono text-xs break-all' : ''">
                                {{ row.value }}
                            </dd>
                        </template>
                    </dl>
                </section>
            </div>
        </SheetContent>
    </Sheet>
</template>
