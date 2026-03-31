<script setup lang="ts">
import { CircleArrowUp, ExternalLink } from 'lucide-vue-next'
import { HoverCard, HoverCardContent, HoverCardTrigger } from '../ui/hover-card';

defineProps<{
    show: boolean
    version: string | null
    tooltipSide?: 'top' | 'right' | 'bottom' | 'left'
}>()
</script>

<template>
    <HoverCard v-if="show">
        <HoverCardTrigger as-child>
            <CircleArrowUp class="size-4 text-primary shrink-0 cursor-pointer" aria-label="Update available" />
        </HoverCardTrigger>
        <HoverCardContent :side="tooltipSide ?? 'top'" class="w-72">
            <div class="flex flex-col space-y-3">
                <div class="flex items-center space-x-3">
                    <img src="/arkeep-icon.png" class="size-8 rounded-lg shrink-0" alt="Arkeep" />
                    <div>
                        <p class="text-sm font-semibold leading-none">Update available</p>
                        <p class="text-xs text-primary mt-1">v{{ version }}</p>
                    </div>
                </div>
                <p class="text-sm text-muted-foreground leading-relaxed">
                    A new version of Arkeep is available. Update to get the latest features and bug fixes.
                </p>
                <a href="https://github.com/arkeep-io/arkeep/releases" target="_blank" rel="noopener noreferrer"
                    class="inline-flex items-center gap-1.5 text-xs font-medium text-primary hover:underline">
                    <ExternalLink class="size-3" />
                    Download &amp; Changelog
                </a>
            </div>
        </HoverCardContent>
    </HoverCard>
</template>
