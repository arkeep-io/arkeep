<script setup lang="ts">
import { computed, ref } from 'vue'
import { ChevronRight, ChevronDown, Folder, FolderOpen, File } from 'lucide-vue-next'
import type { SnapshotFileEntry } from '@/types'

const props = defineProps<{
  entries: SnapshotFileEntry[]
  modelValue: string[]
}>()

const emit = defineEmits<{
  'update:modelValue': [value: string[]]
}>()

// collapsed keeps track of which directory paths are collapsed.
const collapsed = ref(new Set<string>())

function toggleDir(path: string) {
  if (collapsed.value.has(path)) {
    collapsed.value.delete(path)
  } else {
    collapsed.value.add(path)
  }
  // trigger reactivity
  collapsed.value = new Set(collapsed.value)
}

// A flat row is derived from the entry list: sort dirs before files at each level,
// then filter out rows whose ancestor dirs are collapsed.
const rows = computed(() => {
  const sorted = [...props.entries].sort((a, b) => {
    const pa = a.path.split('/')
    const pb = b.path.split('/')
    // compare level by level
    for (let i = 0; i < Math.min(pa.length, pb.length); i++) {
      if (pa[i] !== pb[i]) {
        return pa[i].localeCompare(pb[i])
      }
    }
    return pa.length - pb.length
  })

  // Stable sort: dirs before files within the same parent
  sorted.sort((a, b) => {
    const aParent = a.path.substring(0, a.path.lastIndexOf('/'))
    const bParent = b.path.substring(0, b.path.lastIndexOf('/'))
    if (aParent !== bParent) return 0
    if (a.type === b.type) return 0
    return a.type === 'dir' ? -1 : 1
  })

  return sorted.filter(e => {
    // hide if any ancestor directory is collapsed
    const parts = e.path.split('/')
    for (let i = 1; i < parts.length - 1; i++) {
      const ancestor = parts.slice(0, i + 1).join('/')
      if (collapsed.value.has(ancestor)) return false
    }
    return true
  })
})

function depth(path: string): number {
  return path.split('/').length - 2 // root "/" counts as -1
}

function name(path: string): string {
  return path.split('/').pop() ?? path
}

function isSelected(path: string): boolean {
  return props.modelValue.includes(path)
}

function isIndeterminate(entry: SnapshotFileEntry): boolean {
  if (entry.type !== 'dir' || isSelected(entry.path)) return false
  const prefix = entry.path + '/'
  const descendants = props.entries.filter(e => e.path.startsWith(prefix))
  const selectedCount = descendants.filter(e => props.modelValue.includes(e.path)).length
  return selectedCount > 0 && selectedCount < descendants.length
}

function toggle(entry: SnapshotFileEntry) {
  if (isSelected(entry.path)) {
    // deselect this entry and all its descendants
    const prefix = entry.path + '/'
    emit('update:modelValue', props.modelValue.filter(p => p !== entry.path && !p.startsWith(prefix)))
  } else {
    // select this entry and all its descendants
    const toAdd = entry.type === 'dir'
      ? props.entries.filter(e => e.path === entry.path || e.path.startsWith(entry.path + '/')).map(e => e.path)
      : [entry.path]
    emit('update:modelValue', [...new Set([...props.modelValue, ...toAdd])])
  }
}
</script>

<template>
  <div class="font-mono text-xs">
    <div
      v-for="entry in rows"
      :key="entry.path"
      class="flex items-center gap-1 py-0.5 px-1 hover:bg-muted/50 select-none"
      :style="{ paddingLeft: `${8 + depth(entry.path) * 16}px` }"
    >
      <!-- dir chevron -->
      <button
        v-if="entry.type === 'dir'"
        class="shrink-0 p-0 text-muted-foreground"
        @click.stop="toggleDir(entry.path)"
      >
        <ChevronDown v-if="!collapsed.has(entry.path)" class="h-3.5 w-3.5" />
        <ChevronRight v-else class="h-3.5 w-3.5" />
      </button>
      <span v-else class="w-3.5 shrink-0" />

      <!-- checkbox -->
      <input
        type="checkbox"
        :checked="isSelected(entry.path)"
        :indeterminate="isIndeterminate(entry)"
        class="h-3.5 w-3.5 shrink-0 accent-primary cursor-pointer"
        @change="toggle(entry)"
      />

      <!-- icon -->
      <FolderOpen v-if="entry.type === 'dir' && !collapsed.has(entry.path)" class="h-3.5 w-3.5 shrink-0 text-muted-foreground" />
      <Folder v-else-if="entry.type === 'dir'" class="h-3.5 w-3.5 shrink-0 text-muted-foreground" />
      <File v-else class="h-3.5 w-3.5 shrink-0 text-muted-foreground" />

      <!-- name -->
      <span class="truncate cursor-pointer" @click="entry.type === 'dir' ? toggleDir(entry.path) : toggle(entry)">
        {{ name(entry.path) }}
      </span>
    </div>
  </div>
</template>
