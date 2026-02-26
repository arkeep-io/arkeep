<script setup lang="ts">
import { ref, watch } from 'vue'
import {
    Sheet,
    SheetContent,
    SheetDescription,
    SheetFooter,
    SheetHeader,
    SheetTitle,
} from '@/components/ui/sheet'
import { Alert, AlertDescription } from '@/components/ui/alert'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import {
    Field,
    FieldError,
    FieldGroup,
    FieldLabel,
} from '@/components/ui/field'
import { AlertCircle, Loader2 } from 'lucide-vue-next'
import { useForm, useField } from 'vee-validate'
import { toTypedSchema } from '@vee-validate/zod'
import { z } from 'zod'
import { api } from '@/services/api'
import type { Agent, ApiResponse } from '@/types'

// ---------------------------------------------------------------------------
// Props / emits
// ---------------------------------------------------------------------------

// AgentSheet is edit-only — agents register automatically via gRPC
// (auto-discovery). This sheet is used exclusively to rename an existing agent.
const props = defineProps<{
    agent: Agent
    open: boolean
}>()

const emit = defineEmits<{
    'update:open': [value: boolean]
    saved: []
}>()

// ---------------------------------------------------------------------------
// Form
// ---------------------------------------------------------------------------

const schema = toTypedSchema(
    z.object({
        name: z.string().min(1, 'Name is required'),
    })
)

const { handleSubmit, resetForm, setValues, isSubmitting } = useForm({
    validationSchema: schema,
})

const { value: name, errorMessage: nameError } = useField<string>('name')

// Prefill when the sheet opens.
// immediate: true is required because the component is recreated by v-if
// each time agentToEdit changes in the parent — props.open is already true
// at mount time so the transition would be missed without it.
watch(
    () => props.open,
    (open) => {
        if (open) setValues({ name: props.agent.name })
    },
    { immediate: true }
)

// ---------------------------------------------------------------------------
// State
// ---------------------------------------------------------------------------

const submitError = ref<string | null>(null)

// ---------------------------------------------------------------------------
// Handlers
// ---------------------------------------------------------------------------

const onSubmit = handleSubmit(async (values) => {
    submitError.value = null

    try {
        await api<ApiResponse<Agent>>(`/api/v1/agents/${props.agent.id}`, {
            method: 'PATCH',
            body: { name: values.name },
        })
        emit('update:open', false)
        emit('saved')
        resetForm()
    } catch (e: any) {
        submitError.value = e?.data?.error?.message ?? e?.message ?? 'Failed to save agent'
    }
})

function onOpenChange(value: boolean) {
    if (!value) {
        resetForm()
        submitError.value = null
    }
    emit('update:open', value)
}
</script>

<template>
    <Sheet :open="props.open" @update:open="onOpenChange">
        <SheetContent class="sm:max-w-md">
            <SheetHeader>
                <SheetTitle>Rename Agent</SheetTitle>
                <SheetDescription>
                    Update the display name for
                    <strong>{{ props.agent.hostname || props.agent.name }}</strong>.
                </SheetDescription>
            </SheetHeader>

            <form class="py-6 px-4" novalidate @submit.prevent="onSubmit">
                <FieldGroup>

                    <Transition enter-active-class="transition-all duration-200"
                        enter-from-class="-translate-y-1 opacity-0" leave-active-class="transition-all duration-150"
                        leave-to-class="-translate-y-1 opacity-0">
                        <Alert v-if="submitError" variant="destructive">
                            <AlertCircle class="size-4" />
                            <AlertDescription>{{ submitError }}</AlertDescription>
                        </Alert>
                    </Transition>

                    <Field>
                        <FieldLabel for="agent-name">Name</FieldLabel>
                        <Input id="agent-name" v-model="name" placeholder="e.g. prod-db-01" autocomplete="off"
                            :disabled="isSubmitting"
                            :class="nameError ? 'border-destructive focus-visible:ring-destructive/30' : ''" />
                        <FieldError v-if="nameError">{{ nameError }}</FieldError>
                    </Field>

                    <SheetFooter class="mt-2 px-0">
                        <Button type="button" variant="outline" :disabled="isSubmitting" @click="onOpenChange(false)">
                            Cancel
                        </Button>
                        <Button type="submit" :disabled="isSubmitting">
                            <Loader2 v-if="isSubmitting" class="size-4 animate-spin" />
                            {{ isSubmitting ? 'Saving…' : 'Save Changes' }}
                        </Button>
                    </SheetFooter>

                </FieldGroup>
            </form>
        </SheetContent>
    </Sheet>
</template>