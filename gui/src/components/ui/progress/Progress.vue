<script setup lang="ts">
import type { ProgressRootProps } from 'reka-ui'
import { ProgressIndicator, ProgressRoot } from 'reka-ui'
import { cn } from '@/lib/utils'

const props = withDefaults(
    defineProps<ProgressRootProps & { class?: string }>(),
    { modelValue: 0 },
)
</script>

<template>
    <ProgressRoot
        data-slot="progress"
        :class="cn('relative h-2 w-full overflow-hidden rounded-full bg-secondary', props.class)"
        v-bind="props"
    >
        <ProgressIndicator
            class="h-full w-full flex-1 bg-primary transition-all duration-300"
            :style="{ transform: `translateX(-${100 - (props.modelValue ?? 0)}%)` }"
        />
    </ProgressRoot>
</template>
