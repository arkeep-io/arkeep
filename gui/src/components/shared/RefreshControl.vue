<script setup lang="ts">
// RefreshControl — the refresh button of a page, with a menu to refresh it
// automatically every few seconds. The interval is remembered per page in the
// browser (useAutoRefresh).
//
// refresh is the page's own fetch function. Called by the button it runs as
// usual; called by the timer it gets { quiet: true } and should update the
// data in place, without the loading skeletons that would make the page blink
// at every tick.
import { computed } from 'vue'
import { ChevronDown, RefreshCw } from '@lucide/vue'
import { Button } from '@/components/ui/button'
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuLabel,
  DropdownMenuRadioGroup,
  DropdownMenuRadioItem,
  DropdownMenuSeparator,
  DropdownMenuTrigger,
} from '@/components/ui/dropdown-menu'
import { AUTO_REFRESH_INTERVALS, useAutoRefresh } from '@/composables/useAutoRefresh'

const props = defineProps<{
  // Key of the page's preference, e.g. "agents" or "job-detail".
  page: string
  refresh: (options: { quiet: boolean }) => unknown
  // True while the page loads on its own (first load, refresh button, page
  // change): the timer skips its tick meanwhile.
  loading?: boolean
}>()

const { interval, enabled, refreshing } = useAutoRefresh(
  props.page,
  () => props.refresh({ quiet: true }),
  { busy: () => !!props.loading },
)

// The radio group works with strings.
const selected = computed({
  get: () => String(interval.value),
  set: (v: string) => {
    interval.value = Number(v)
  },
})

function shortLabel(seconds: number): string {
  return seconds < 60 ? `${seconds}s` : `${seconds / 60}m`
}

function longLabel(seconds: number): string {
  if (seconds === 0) return 'Off'
  if (seconds < 60) return `Every ${seconds} seconds`
  return seconds === 60 ? 'Every minute' : `Every ${seconds / 60} minutes`
}

const spinning = computed(() => !!props.loading || refreshing.value)
</script>

<template>
  <div class="inline-flex shrink-0">
    <Button variant="outline" size="icon" class="rounded-r-none" aria-label="Refresh" :disabled="loading"
      @click="refresh({ quiet: false })">
      <RefreshCw class="w-4 h-4" :class="{ 'animate-spin': spinning }" />
    </Button>
    <DropdownMenu>
      <DropdownMenuTrigger as-child>
        <Button variant="outline" class="h-9 rounded-l-none border-l-0 px-2 gap-1"
          :aria-label="enabled ? `Auto-refresh: ${longLabel(interval).toLowerCase()}` : 'Auto-refresh: off'">
          <span v-if="enabled" class="text-xs tabular-nums" data-test="auto-refresh-interval">
            {{ shortLabel(interval) }}
          </span>
          <ChevronDown class="w-3.5 h-3.5" />
        </Button>
      </DropdownMenuTrigger>
      <DropdownMenuContent align="end" class="w-44">
        <DropdownMenuLabel>Auto-refresh</DropdownMenuLabel>
        <DropdownMenuSeparator />
        <DropdownMenuRadioGroup v-model="selected">
          <DropdownMenuRadioItem v-for="s in AUTO_REFRESH_INTERVALS" :key="s" :value="String(s)">
            {{ longLabel(s) }}
          </DropdownMenuRadioItem>
        </DropdownMenuRadioGroup>
      </DropdownMenuContent>
    </DropdownMenu>
  </div>
</template>
