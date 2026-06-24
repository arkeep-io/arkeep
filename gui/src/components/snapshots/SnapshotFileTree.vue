<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { ChevronRight, ChevronDown, Folder, FolderOpen, File, Loader2 } from '@lucide/vue'
import type { SnapshotFileEntry } from '@/types'

const props = defineProps<{
  // entries are the root-level children of the snapshot (its top directories).
  entries: SnapshotFileEntry[]
  modelValue: string[]
  // loadChildren fetches the direct children of a directory on demand. The tree
  // loads one level at a time so huge snapshots stay responsive.
  loadChildren: (path: string) => Promise<SnapshotFileEntry[]>
}>()

const emit = defineEmits<{
  'update:modelValue': [value: string[]]
}>()

// Lazy-loading state, keyed by directory path.
const childrenByPath = ref(new Map<string, SnapshotFileEntry[]>())
const expanded = ref(new Set<string>())
const loading = ref(new Set<string>())
const errors = ref(new Map<string, string>())

// Reset all internal state whenever a fresh root listing is loaded.
watch(() => props.entries, () => {
  childrenByPath.value = new Map()
  expanded.value = new Set()
  loading.value = new Set()
  errors.value = new Map()
})

function name(path: string): string {
  return path.split('/').pop() || path
}

function sortEntries(entries: SnapshotFileEntry[]): SnapshotFileEntry[] {
  return [...entries].sort((a, b) => {
    if (a.type !== b.type) return a.type === 'dir' ? -1 : 1
    return name(a.path).localeCompare(name(b.path))
  })
}

interface Row {
  entry: SnapshotFileEntry
  depth: number
}

// rows flattens the currently-expanded tree into ordered display rows. Depth is
// the walk level (not the path length) so deeply-rooted sources aren't
// over-indented.
const rows = computed<Row[]>(() => {
  const out: Row[] = []
  const walk = (entries: SnapshotFileEntry[], depth: number) => {
    for (const entry of sortEntries(entries)) {
      out.push({ entry, depth })
      if (entry.type === 'dir' && expanded.value.has(entry.path)) {
        const children = childrenByPath.value.get(entry.path)
        if (children) walk(children, depth + 1)
      }
    }
  }
  walk(props.entries, 0)
  return out
})

async function toggleDir(entry: SnapshotFileEntry) {
  const path = entry.path
  if (expanded.value.has(path)) {
    expanded.value.delete(path)
    expanded.value = new Set(expanded.value)
    return
  }

  expanded.value.add(path)
  expanded.value = new Set(expanded.value)

  // Fetch children the first time the directory is expanded.
  if (!childrenByPath.value.has(path) && !loading.value.has(path)) {
    loading.value.add(path)
    loading.value = new Set(loading.value)
    errors.value.delete(path)
    errors.value = new Map(errors.value)
    try {
      const children = await props.loadChildren(path)
      childrenByPath.value.set(path, children)
      childrenByPath.value = new Map(childrenByPath.value)
    } catch (e: any) {
      errors.value.set(path, e?.data?.error?.message ?? e?.message ?? 'Failed to load directory.')
      errors.value = new Map(errors.value)
      // Collapse again so the chevron offers a retry.
      expanded.value.delete(path)
      expanded.value = new Set(expanded.value)
    } finally {
      loading.value.delete(path)
      loading.value = new Set(loading.value)
    }
  }
}

// --- Selection ---------------------------------------------------------------
// Selecting a directory records only its own path: restic restore --include
// restores a directory recursively, so descendants need not be enumerated.

function ancestorSelected(path: string): boolean {
  return props.modelValue.some(p => path.startsWith(p + '/'))
}

function isChecked(path: string): boolean {
  return props.modelValue.includes(path) || ancestorSelected(path)
}

function isIndeterminate(entry: SnapshotFileEntry): boolean {
  if (entry.type !== 'dir' || isChecked(entry.path)) return false
  const prefix = entry.path + '/'
  return props.modelValue.some(p => p.startsWith(prefix))
}

function toggle(entry: SnapshotFileEntry) {
  const path = entry.path
  // A node whose ancestor is selected is locked; change the ancestor instead.
  if (ancestorSelected(path)) return

  const prefix = path + '/'
  if (props.modelValue.includes(path)) {
    emit('update:modelValue', props.modelValue.filter(p => p !== path && !p.startsWith(prefix)))
  } else {
    // Drop now-redundant descendant selections, then add this path.
    const next = props.modelValue.filter(p => !p.startsWith(prefix))
    next.push(path)
    emit('update:modelValue', [...new Set(next)])
  }
}
</script>

<template>
  <div class="font-mono text-xs">
    <div
      v-for="{ entry, depth } in rows"
      :key="entry.path"
      class="flex items-center gap-1 py-0.5 px-1 hover:bg-muted/50 select-none"
      :style="{ paddingLeft: `${8 + depth * 16}px` }"
    >
      <!-- dir chevron / loading spinner -->
      <Loader2 v-if="entry.type === 'dir' && loading.has(entry.path)" class="h-3.5 w-3.5 shrink-0 animate-spin text-muted-foreground" />
      <button
        v-else-if="entry.type === 'dir'"
        type="button"
        class="shrink-0 p-0 text-muted-foreground"
        @click.stop="toggleDir(entry)"
      >
        <ChevronDown v-if="expanded.has(entry.path)" class="h-3.5 w-3.5" />
        <ChevronRight v-else class="h-3.5 w-3.5" />
      </button>
      <span v-else class="w-3.5 shrink-0" />

      <!-- checkbox -->
      <input
        type="checkbox"
        :checked="isChecked(entry.path)"
        :indeterminate="isIndeterminate(entry)"
        :disabled="ancestorSelected(entry.path)"
        class="h-3.5 w-3.5 shrink-0 accent-primary cursor-pointer disabled:cursor-not-allowed disabled:opacity-50"
        @change="toggle(entry)"
      />

      <!-- icon -->
      <FolderOpen v-if="entry.type === 'dir' && expanded.has(entry.path)" class="h-3.5 w-3.5 shrink-0 text-muted-foreground" />
      <Folder v-else-if="entry.type === 'dir'" class="h-3.5 w-3.5 shrink-0 text-muted-foreground" />
      <File v-else class="h-3.5 w-3.5 shrink-0 text-muted-foreground" />

      <!-- name -->
      <span class="truncate cursor-pointer" @click="entry.type === 'dir' ? toggleDir(entry) : toggle(entry)">
        {{ name(entry.path) }}
      </span>

      <!-- per-directory load error -->
      <span v-if="errors.has(entry.path)" class="ml-2 text-destructive truncate">
        {{ errors.get(entry.path) }}
      </span>
    </div>
  </div>
</template>
