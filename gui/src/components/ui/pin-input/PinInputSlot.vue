<script setup lang="ts">
import type { HTMLAttributes } from "vue"
import type { PinInputInputProps } from "reka-ui"
import { computed } from "vue"
import { PinInputInput } from "reka-ui"
import { cn } from "@/lib/utils"

const props = defineProps<PinInputInputProps & { class?: HTMLAttributes["class"] }>()

const delegatedProps = computed(() => {
  const { class: _class, ...rest } = props
  return rest
})
</script>

<template>
  <PinInputInput
    data-slot="pin-input-slot"
    v-bind="delegatedProps"
    :class="cn(
      'dark:bg-input/30 border-input relative flex h-10 w-10 items-center justify-center rounded-md border bg-transparent text-center text-base shadow-xs outline-none transition-[color,box-shadow] disabled:pointer-events-none disabled:cursor-not-allowed disabled:opacity-50',
      'focus-visible:border-ring focus-visible:ring-ring/50 focus-visible:ring-[3px]',
      'aria-invalid:ring-destructive/20 dark:aria-invalid:ring-destructive/40 aria-invalid:border-destructive',
      props.class,
    )"
  />
</template>
