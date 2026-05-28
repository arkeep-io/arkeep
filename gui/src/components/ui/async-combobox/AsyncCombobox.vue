<script setup lang="ts">
import { ref, computed, watch, onUnmounted } from 'vue'
import { useDebounceFn } from '@vueuse/core'
import { ChevronDown, Check, Search } from '@lucide/vue'
import { Input } from '@/components/ui/input'
import { Skeleton } from '@/components/ui/skeleton'
import { api } from '@/services/api'
import type { ApiResponse } from '@/types'
import { cn } from '@/lib/utils'

interface Item {
  id: string
  name: string
  [key: string]: unknown
}

const props = withDefaults(defineProps<{
  endpoint: string
  modelValue: string
  initialLabel?: string
  placeholder?: string
  disabled?: boolean
  allowClear?: boolean
  class?: string
}>(), {
  placeholder: 'Select...',
  disabled: false,
  allowClear: false,
})

const emit = defineEmits<{
  (e: 'update:modelValue', value: string): void
  (e: 'update:item', item: Item | null): void
}>()

const PAGE_SIZE = 20

const open = ref(false)
const search = ref('')
const items = ref<Item[]>([])
const loading = ref(false)
const hasMore = ref(false)
const currentPage = ref(1)
const displayLabel = ref(props.initialLabel ?? '')
const root = ref<HTMLElement | null>(null)
const inputRef = ref<HTMLInputElement | null>(null)

watch(() => props.initialLabel, (val) => {
  if (val !== undefined && val !== '') displayLabel.value = val
})

async function fetchItems(reset: boolean) {
  if (reset) {
    currentPage.value = 1
    items.value = []
  }
  loading.value = true
  try {
    const offset = (currentPage.value - 1) * PAGE_SIZE
    // Build URL: endpoint may already contain query params (e.g. ?status=online)
    const sep = props.endpoint.includes('?') ? '&' : '?'
    const params = new URLSearchParams({ limit: String(PAGE_SIZE), offset: String(offset) })
    if (search.value) params.set('search', search.value)
    const url = `${props.endpoint}${sep}${params}`
    const res = await api<ApiResponse<{ items: Item[]; total: number }>>(url)
    const fetched = res.data.items ?? []
    if (reset) {
      items.value = fetched
    } else {
      items.value = [...items.value, ...fetched]
    }
    hasMore.value = items.value.length < res.data.total
  } catch {
    // ignore — dropdown stays empty
  } finally {
    loading.value = false
  }
}

const debouncedSearch = useDebounceFn(() => fetchItems(true), 300)
watch(search, debouncedSearch)

function openDropdown() {
  if (props.disabled) return
  open.value = true
  fetchItems(true)
  // focus input on next tick
  setTimeout(() => inputRef.value?.focus(), 50)
}

function closeDropdown() {
  open.value = false
  search.value = ''
}

function toggle() {
  if (open.value) closeDropdown()
  else openDropdown()
}

function selectItem(item: Item) {
  displayLabel.value = item.name
  emit('update:modelValue', item.id)
  emit('update:item', item)
  closeDropdown()
}

function clearSelection() {
  displayLabel.value = ''
  emit('update:modelValue', '')
  emit('update:item', null)
}

function onScroll(e: Event) {
  const el = e.target as HTMLElement
  if (!loading.value && hasMore.value && el.scrollHeight - el.scrollTop - el.clientHeight < 50) {
    currentPage.value++
    fetchItems(false)
  }
}

function onMousedown(e: MouseEvent) {
  if (root.value && !root.value.contains(e.target as Node)) {
    closeDropdown()
  }
}

watch(open, (val) => {
  if (val) document.addEventListener('mousedown', onMousedown)
  else document.removeEventListener('mousedown', onMousedown)
})

onUnmounted(() => document.removeEventListener('mousedown', onMousedown))

const visibleItems = computed(() => items.value)
const triggerLabel = computed(() => props.modelValue ? (displayLabel.value || props.modelValue) : '')
</script>

<template>
  <div ref="root" class="relative w-full" :class="props.class">
    <!-- Trigger -->
    <button
      type="button"
      :disabled="disabled"
      :aria-expanded="open"
      :aria-haspopup="true"
      :class="cn(
        'border-input focus-visible:border-ring focus-visible:ring-ring/50 dark:bg-input/30 dark:hover:bg-input/50',
        'flex w-full items-center justify-between gap-2 rounded-md border bg-transparent px-3 py-2 text-sm',
        'shadow-xs transition-[color,box-shadow] outline-none focus-visible:ring-[3px]',
        'disabled:cursor-not-allowed disabled:opacity-50 h-9',
        open && 'border-ring ring-ring/50 ring-[3px]',
      )"
      @click="toggle"
    >
      <span :class="triggerLabel ? 'text-foreground' : 'text-muted-foreground'">
        {{ triggerLabel || placeholder }}
      </span>
      <ChevronDown class="size-4 shrink-0 opacity-50" :class="open && 'rotate-180'" />
    </button>

    <!-- Dropdown panel -->
    <div
      v-if="open"
      class="absolute z-50 mt-1 w-full min-w-50 rounded-md border bg-popover shadow-md"
    >
      <!-- Search input -->
      <div class="p-2 border-b">
        <div class="relative">
          <Search class="absolute left-2.5 top-1/2 -translate-y-1/2 size-3.5 text-muted-foreground pointer-events-none" />
          <Input
            ref="inputRef"
            v-model="search"
            class="pl-8 h-8 text-sm"
            placeholder="Search..."
            @keydown.escape="closeDropdown"
          />
        </div>
      </div>

      <!-- List -->
      <div class="max-h-60 overflow-y-auto py-1" @scroll="onScroll">
        <!-- Clear option -->
        <button
          v-if="allowClear"
          type="button"
          class="flex w-full items-center gap-2 px-3 py-1.5 text-sm text-muted-foreground hover:bg-accent hover:text-accent-foreground"
          @click="clearSelection(); closeDropdown()"
        >
          <span class="size-4 shrink-0" />
          — None —
        </button>

        <!-- Loading skeletons (first load) -->
        <template v-if="loading && visibleItems.length === 0">
          <div v-for="n in 4" :key="n" class="px-3 py-2">
            <Skeleton class="h-4 w-full" />
          </div>
        </template>

        <!-- Empty state -->
        <template v-else-if="!loading && visibleItems.length === 0">
          <p class="px-3 py-4 text-sm text-center text-muted-foreground">No results found.</p>
        </template>

        <!-- Items -->
        <template v-else>
          <button
            v-for="item in visibleItems"
            :key="item.id"
            type="button"
            class="flex w-full items-center gap-2 px-3 py-1.5 text-sm hover:bg-accent hover:text-accent-foreground"
            @click="selectItem(item)"
          >
            <Check
              class="size-4 shrink-0"
              :class="modelValue === item.id ? 'opacity-100' : 'opacity-0'"
            />
            {{ item.name }}
          </button>

          <!-- Load more indicator -->
          <div v-if="loading" class="px-3 py-2">
            <Skeleton class="h-4 w-full" />
          </div>
        </template>
      </div>
    </div>
  </div>
</template>
