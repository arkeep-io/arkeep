<script setup lang="ts">
import { Button } from '@/components/ui/button'
import { AlertCircle, Loader2 } from '@lucide/vue'

// UnsavedChangesBar is the save bar of the settings pages: placed last on the
// page, it sticks to the bottom of the view while the form has changes not
// saved yet, or while the last save failed, whose error it shows.
defineProps<{
    dirty: boolean
    saving: boolean
    error?: string | null
}>()

const emit = defineEmits<{
    save: []
    discard: []
}>()
</script>

<template>
    <Transition enter-active-class="transition-all duration-200" enter-from-class="translate-y-2 opacity-0"
        leave-active-class="transition-all duration-150" leave-to-class="translate-y-2 opacity-0">
        <div v-if="dirty || error" class="sticky bottom-4 z-10 mt-6" data-testid="unsaved-changes-bar">
            <div role="region" aria-label="Unsaved changes"
                class="flex flex-wrap items-center justify-between gap-3 rounded-lg border bg-background px-4 py-3 shadow-lg">
                <p v-if="error" class="flex items-center gap-2 text-sm text-destructive">
                    <AlertCircle class="size-4 shrink-0" />
                    {{ error }}
                </p>
                <p v-else class="text-sm">You have unsaved changes.</p>
                <div class="flex gap-2">
                    <Button type="button" variant="outline" size="sm" :disabled="saving || !dirty"
                        @click="emit('discard')">
                        Discard
                    </Button>
                    <Button type="button" size="sm" :disabled="saving" @click="emit('save')">
                        <Loader2 v-if="saving" class="size-4 animate-spin" />
                        {{ saving ? 'Saving…' : 'Save' }}
                    </Button>
                </div>
            </div>
        </div>
    </Transition>
</template>
