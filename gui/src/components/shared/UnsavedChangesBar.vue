<script setup lang="ts">
import { onBeforeUnmount, onMounted, ref } from 'vue'
import { onBeforeRouteLeave } from 'vue-router'
import { Button } from '@/components/ui/button'
import {
    AlertDialog,
    AlertDialogContent,
    AlertDialogDescription,
    AlertDialogFooter,
    AlertDialogHeader,
    AlertDialogTitle,
} from '@/components/ui/alert-dialog'
import { AlertCircle, Loader2 } from '@lucide/vue'

// UnsavedChangesBar is the save bar of the settings pages: placed last on the
// page, it sticks to the bottom of the view while the form has changes not
// saved yet, or while the last save failed, whose error it shows. While there
// are changes, leaving the page asks for confirmation first.
const props = defineProps<{
    dirty: boolean
    saving: boolean
    error?: string | null
}>()

const emit = defineEmits<{
    save: []
    discard: []
}>()

// ---------------------------------------------------------------------------
// Leaving with unsaved changes
// ---------------------------------------------------------------------------

// The navigation waits on the dialog: resolveLeave settles it.
const leaveOpen = ref(false)
let resolveLeave: ((leave: boolean) => void) | null = null

function settleLeave(leave: boolean) {
    leaveOpen.value = false
    resolveLeave?.(leave)
    resolveLeave = null
}

onBeforeRouteLeave(() => {
    if (!props.dirty) return true
    leaveOpen.value = true
    return new Promise<boolean>((resolve) => { resolveLeave = resolve })
})

// Closing or reloading the tab: the browser shows its own prompt.
function onBeforeUnload(e: BeforeUnloadEvent) {
    if (props.dirty) e.preventDefault()
}

onMounted(() => window.addEventListener('beforeunload', onBeforeUnload))
onBeforeUnmount(() => window.removeEventListener('beforeunload', onBeforeUnload))
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

    <AlertDialog :open="leaveOpen" @update:open="(open: boolean) => { if (!open) settleLeave(false) }">
        <AlertDialogContent>
            <AlertDialogHeader>
                <AlertDialogTitle>Leave without saving?</AlertDialogTitle>
                <AlertDialogDescription>
                    You have unsaved changes on this page. If you leave now, they are lost.
                </AlertDialogDescription>
            </AlertDialogHeader>
            <AlertDialogFooter>
                <!-- Plain buttons: the dialog's own Action and Cancel close it first,
                     which would settle the navigation as a cancel. Escape and a
                     click outside still mean stay. -->
                <Button type="button" variant="outline" @click="settleLeave(false)">Stay on this page</Button>
                <Button type="button" variant="destructive" @click="settleLeave(true)">Leave without saving</Button>
            </AlertDialogFooter>
        </AlertDialogContent>
    </AlertDialog>
</template>
