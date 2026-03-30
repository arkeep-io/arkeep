<script setup lang="ts">
import { CircleArrowUp } from 'lucide-vue-next'
import {
    Tooltip,
    TooltipContent,
    TooltipProvider,
    TooltipTrigger,
} from '@/components/ui/tooltip'

defineProps<{
    show: boolean
    version: string | null
    tooltipSide?: 'top' | 'right' | 'bottom' | 'left'
}>()
</script>

<template>
    <TooltipProvider v-if="show" :delay-duration="200" class="cursor-pointer">
        <Tooltip>
            <TooltipTrigger as-child>
                <CircleArrowUp class="size-4 text-primary shrink-0 cursor-default" aria-label="Update available" />
            </TooltipTrigger>
            <TooltipContent :side="tooltipSide ?? 'top'">
                v{{ version }} available
            </TooltipContent>
        </Tooltip>
    </TooltipProvider>
</template>
