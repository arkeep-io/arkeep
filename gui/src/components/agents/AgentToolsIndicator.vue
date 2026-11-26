<script setup lang="ts">
import { computed } from 'vue'
import { ExternalLink, Package } from '@lucide/vue'
import { HoverCard, HoverCardContent, HoverCardTrigger } from '@/components/ui/hover-card'

// Shows the versions of the restic and rclone binaries embedded in an agent.
// Agents that do not report them render nothing.
const props = defineProps<{
    resticVersion: string
    rcloneVersion: string
}>()

const tools = computed(() =>
    [
        { name: 'restic', version: props.resticVersion, repo: 'restic/restic' },
        { name: 'rclone', version: props.rcloneVersion, repo: 'rclone/rclone' },
    ].filter((t) => t.version),
)

const label = computed(() => 'Embedded ' + tools.value.map((t) => `${t.name} ${t.version}`).join(', '))
</script>

<template>
    <HoverCard v-if="tools.length" :open-delay="100">
        <HoverCardTrigger as-child>
            <button type="button" :aria-label="label" data-testid="agent-tools-indicator"
                class="inline-flex text-primary cursor-pointer outline-none">
                <Package class="size-4 shrink-0" />
            </button>
        </HoverCardTrigger>
        <HoverCardContent side="top" class="w-64 animate-none">
            <div class="flex flex-col space-y-3">
                <div class="flex items-center space-x-2">
                    <Package class="size-4 text-muted-foreground shrink-0" />
                    <p class="text-sm font-semibold leading-none">Embedded tools</p>
                </div>
                <div class="flex flex-col space-y-1.5">
                    <div v-for="t in tools" :key="t.name" class="flex items-center justify-between text-sm">
                        <span class="text-muted-foreground">{{ t.name }}</span>
                        <a :href="`https://github.com/${t.repo}/releases/tag/v${t.version}`" target="_blank"
                            rel="noopener noreferrer"
                            class="inline-flex items-center gap-1.5 font-mono text-xs font-medium text-primary hover:underline">
                            {{ t.version }}
                            <ExternalLink class="size-3" />
                        </a>
                    </div>
                </div>
            </div>
        </HoverCardContent>
    </HoverCard>
</template>
