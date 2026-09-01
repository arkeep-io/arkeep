<script setup lang="ts">
import type { HTMLAttributes } from "vue"
import type { PinInputRootEmits, PinInputRootProps } from "reka-ui"
import { computed } from "vue"
import { PinInputRoot, useForwardPropsEmits } from "reka-ui"
import { cn } from "@/lib/utils"

const props = defineProps<PinInputRootProps & { class?: HTMLAttributes["class"] }>()
const emits = defineEmits<PinInputRootEmits>()

const delegatedProps = computed(() => {
  const { class: _class, ...rest } = props
  return rest
})
const forwarded = useForwardPropsEmits(delegatedProps, emits)
</script>

<template>
  <PinInputRoot
    data-slot="pin-input"
    v-bind="forwarded"
    :class="cn('flex items-center gap-2', props.class)"
  >
    <slot />
  </PinInputRoot>
</template>
