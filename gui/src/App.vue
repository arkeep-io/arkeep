<script setup lang="ts">
import { useTheme } from '@/composables/useTheme'

// Initialize color mode globally
useTheme()
</script>

<template>
  <RouterView />
</template>
