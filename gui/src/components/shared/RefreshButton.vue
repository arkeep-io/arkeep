<script setup lang="ts">
// RefreshButton — the refresh button of a page. refresh is the page's own
// fetch function, called as on the first load (with the loading state).
//
// live marks a page that updates itself from the server's change events
// (useLiveRefresh): when those have been unavailable for a while, the button
// says so, since the page then only changes when refreshed by hand.
import { RefreshCw, WifiOff } from '@lucide/vue'
import { Button } from '@/components/ui/button'
import { liveUpdatesUnavailable } from '@/services/websocket'
import type { FetchOptions } from '@/composables/useLiveRefresh'

const props = defineProps<{
  refresh: (options: FetchOptions) => unknown
  // True while the page loads: the button spins and is disabled.
  loading?: boolean
  live?: boolean
}>()
</script>

<template>
  <div class="inline-flex shrink-0 items-center gap-2">
    <span v-if="props.live && liveUpdatesUnavailable" data-test="live-updates-unavailable"
      class="inline-flex items-center gap-1.5 text-xs text-muted-foreground"
      title="The live connection to the server keeps failing, so this page no longer updates by itself: use the refresh button. If it lasts, check that your reverse proxy passes WebSocket connections (/api/v1/ws).">
      <WifiOff class="w-3.5 h-3.5" />
      Live updates unavailable
    </span>
    <Button variant="outline" size="icon" aria-label="Refresh" :disabled="props.loading"
      @click="props.refresh({})">
      <RefreshCw class="w-4 h-4" :class="{ 'animate-spin': props.loading }" />
    </Button>
  </div>
</template>
