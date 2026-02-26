<script setup lang="ts">
import { ref, computed, watch } from 'vue'
import {
    Sheet,
    SheetContent,
    SheetDescription,
    SheetFooter,
    SheetHeader,
    SheetTitle,
    SheetTrigger,
} from '@/components/ui/sheet'
import {
    Dialog,
    DialogContent,
    DialogDescription,
    DialogFooter,
    DialogHeader,
    DialogTitle,
} from '@/components/ui/dialog'
import { Alert, AlertDescription } from '@/components/ui/alert'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import {
    Field,
    FieldError,
    FieldGroup,
    FieldLabel,
} from '@/components/ui/field'
import {
    Tabs,
    TabsContent,
    TabsList,
    TabsTrigger,
} from '@/components/ui/tabs'
import { AlertCircle, Copy, Check, TriangleAlert, Plus, Loader2 } from 'lucide-vue-next'
import { useForm, useField } from 'vee-validate'
import { toTypedSchema } from '@vee-validate/zod'
import { z } from 'zod'
import { api } from '@/services/api'
import type { Agent, ApiResponse } from '@/types'

// ---------------------------------------------------------------------------
// Props / emits
// ---------------------------------------------------------------------------

const props = defineProps<{
    // Edit mode: pass the agent to edit.
    // New mode: omit — the sheet renders its own trigger button.
    agent?: Agent
    // Required in edit mode to control open state from parent.
    open?: boolean
}>()

const emit = defineEmits<{
    'update:open': [value: boolean]
    saved: []
}>()

// ---------------------------------------------------------------------------
// Mode
// ---------------------------------------------------------------------------

const isEdit = computed(() => !!props.agent)

const internalOpen = ref(false)

const sheetOpen = computed(() => isEdit.value ? (props.open ?? false) : internalOpen.value)

function setOpen(value: boolean) {
    if (isEdit.value) {
        emit('update:open', value)
    } else {
        internalOpen.value = value
    }
}

// ---------------------------------------------------------------------------
// Form
//
// New mode:  name + hostname (both required — backend enforces hostname).
// Edit mode: name only (hostname is set by the agent at gRPC registration
//            and cannot be changed from the GUI).
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

// Prefill form when sheet opens in edit mode.
// immediate: true is required because the component is recreated by v-if
// each time agentToEdit changes — by the time the watch activates,
// props.open is already true and the transition would be missed without it.
watch(
    () => props.open,
    (open) => {
        if (open && isEdit.value && props.agent) {
            setValues({ name: props.agent.name })
        }
    },
    { immediate: true }
)

// ---------------------------------------------------------------------------
// State
// ---------------------------------------------------------------------------

const submitError = ref<string | null>(null)

// New mode only — token dialog shown after successful creation.
const tokenDialogOpen = ref(false)
const registrationToken = ref('')
const createdAgentName = ref('')
const copied = ref(false)

// ---------------------------------------------------------------------------
// Server address hint (new mode)
// ---------------------------------------------------------------------------

const serverAddr = computed(() => `${window.location.hostname}:9090`)

// ---------------------------------------------------------------------------
// Submit
// ---------------------------------------------------------------------------

interface CreateAgentResponse {
    id: string
    name: string
    registration_token: string
}

const onSubmit = handleSubmit(async (values) => {
    submitError.value = null

    try {
        if (isEdit.value && props.agent) {
            await api<ApiResponse<Agent>>(`/api/v1/agents/${props.agent.id}`, {
                method: 'PATCH',
                body: { name: values.name },
            })
            setOpen(false)
            emit('saved')
        } else {
            const res = await api<ApiResponse<CreateAgentResponse>>('/api/v1/agents', {
                method: 'POST',
                body: { name: values.name },
            })
            registrationToken.value = res.data.registration_token
            createdAgentName.value = res.data.name
            setOpen(false)
            tokenDialogOpen.value = true
            emit('saved')
        }

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
    setOpen(value)
}

// ---------------------------------------------------------------------------
// Copy token (new mode)
// ---------------------------------------------------------------------------

async function copyToken() {
    await navigator.clipboard.writeText(registrationToken.value)
    copied.value = true
    setTimeout(() => { copied.value = false }, 2000)
}

// ---------------------------------------------------------------------------
// Setup snippets (new mode)
// ---------------------------------------------------------------------------

const cliSnippet = computed(() =>
    `arkeep-agent \\\n  --server-addr=${serverAddr.value} \\\n  --token=${registrationToken.value}`
)

const envSnippet = computed(() =>
    `export ARKEEP_SERVER=${serverAddr.value}\nexport ARKEEP_TOKEN=${registrationToken.value}\n\narkeep-agent`
)

const dockerSnippet = computed(() =>
    `docker run -d \\\n  --name arkeep-agent \\\n  --restart unless-stopped \\\n  -e ARKEEP_SERVER=${serverAddr.value} \\\n  -e ARKEEP_TOKEN=${registrationToken.value} \\\n  -v /:/host:ro \\\n  ghcr.io/arkeep-io/arkeep-agent:latest`
)
</script>

<template>
    <!-- ── Sheet ────────────────────────────────────────────────────────────── -->
    <Sheet :open="sheetOpen" @update:open="onOpenChange">

        <SheetTrigger v-if="!isEdit" as-child>
            <Button>
                <Plus class="w-4 h-4" />
                Add Agent
            </Button>
        </SheetTrigger>

        <SheetContent class="sm:max-w-md">
            <SheetHeader>
                <SheetTitle>{{ isEdit ? 'Edit Agent' : 'Add Agent' }}</SheetTitle>
                <SheetDescription>
                    <template v-if="isEdit">
                        Update the display name for
                        <strong>{{ props.agent?.hostname }}</strong>.
                    </template>
                    <template v-else>
                        Register a new agent. A one-time token will be generated — use it to
                        start the agent on the target machine.
                    </template>
                </SheetDescription>
            </SheetHeader>

            <form class="py-6 px-4" novalidate @submit.prevent="onSubmit">
                <FieldGroup>
                    <!-- Server error -->
                    <Transition enter-active-class="transition-all duration-200"
                        enter-from-class="-translate-y-1 opacity-0" leave-active-class="transition-all duration-150"
                        leave-to-class="-translate-y-1 opacity-0">
                        <Alert v-if="submitError" variant="destructive">
                            <AlertCircle class="size-4" />
                            <AlertDescription>{{ submitError }}</AlertDescription>
                        </Alert>
                    </Transition>

                    <!-- Name -->
                    <Field>
                        <FieldLabel for="agent-name">Agent Name</FieldLabel>
                        <Input id="agent-name" v-model="name" placeholder="e.g. prod-db-01" autocomplete="off"
                            :disabled="isSubmitting"
                            :class="nameError ? 'border-destructive focus-visible:ring-destructive/30' : ''" />
                        <FieldError v-if="nameError">{{ nameError }}</FieldError>
                    </Field>


                    <!-- Actions -->
                    <SheetFooter class="mt-2 px-0">
                        <Button type="button" variant="outline" :disabled="isSubmitting" @click="onOpenChange(false)">
                            Cancel
                        </Button>
                        <Button type="submit" :disabled="isSubmitting">
                            <Loader2 v-if="isSubmitting" class="size-4 animate-spin" />
                            {{ isSubmitting
                                ? (isEdit ? 'Saving…' : 'Creating…')
                                : (isEdit ? 'Save Changes' : 'Create Agent') }}
                        </Button>
                    </SheetFooter>

                </FieldGroup>
            </form>
        </SheetContent>
    </Sheet>

    <!-- ── Token dialog (new mode only) ─────────────────────────────────────── -->
    <Dialog v-if="!isEdit" :open="tokenDialogOpen" @update:open="tokenDialogOpen = $event">
        <DialogContent class="sm:max-w-lg w-full flex flex-col max-h-[90vh] gap-0 p-0">
            <div class="overflow-y-auto flex flex-col gap-4 p-6">
                <DialogHeader>
                    <DialogTitle>Agent created</DialogTitle>
                    <DialogDescription>
                        <strong>{{ createdAgentName }}</strong> has been registered. Copy the token
                        below and use it to start the agent on the target machine.
                    </DialogDescription>
                </DialogHeader>

                <div
                    class="flex items-start gap-3 px-4 py-3 text-sm text-yellow-700 border rounded-md border-yellow-500/30 bg-yellow-500/10 dark:text-yellow-400">
                    <TriangleAlert class="h-4 w-4 mt-0.5 shrink-0" />
                    <span>
                        This token is shown <strong>only once</strong> and cannot be recovered.
                        Copy it before closing this dialog.
                    </span>
                </div>

                <div class="flex items-center gap-2 min-w-0">
                    <code class="flex-1 min-w-0 px-3 py-2 font-mono text-sm break-all rounded-md select-all bg-muted">{{
                        registrationToken }}</code>
                    <Button variant="outline" size="icon" @click="copyToken">
                        <Check v-if="copied" class="w-4 h-4 text-emerald-500" />
                        <Copy v-else class="w-4 h-4" />
                    </Button>
                </div>

                <div class="flex flex-col gap-2">
                    <p class="text-sm font-medium">Start the agent</p>
                    <Tabs default-value="cli">
                        <TabsList class="w-full">
                            <TabsTrigger value="cli" class="flex-1">CLI</TabsTrigger>
                            <TabsTrigger value="env" class="flex-1">Env vars</TabsTrigger>
                            <TabsTrigger value="docker" class="flex-1">Docker</TabsTrigger>
                        </TabsList>
                        <TabsContent value="cli">
                            <pre
                                class="px-4 py-3 overflow-x-auto font-mono text-xs whitespace-pre rounded-md bg-muted max-w-full">
                            {{ cliSnippet }}</pre>
                        </TabsContent>
                        <TabsContent value="env">
                            <pre
                                class="px-4 py-3 overflow-x-auto font-mono text-xs whitespace-pre rounded-md bg-muted max-w-full">
                            {{ envSnippet }}</pre>
                        </TabsContent>
                        <TabsContent value="docker">
                            <pre
                                class="px-4 py-3 overflow-x-auto font-mono text-xs whitespace-pre rounded-md bg-muted max-w-full">
                            {{ dockerSnippet }}</pre>
                            <p class="mt-2 text-xs text-muted-foreground">
                                Mount additional volumes with <code class="px-1 rounded bg-muted">-v</code> as needed.
                                The <code class="px-1 rounded bg-muted">-v /:/host:ro</code> above gives read-only
                                access to the entire host filesystem.
                            </p>
                        </TabsContent>
                    </Tabs>
                </div>

                <DialogFooter>
                    <Button @click="tokenDialogOpen = false">Done</Button>
                </DialogFooter>
            </div>
        </DialogContent>
    </Dialog>
</template>