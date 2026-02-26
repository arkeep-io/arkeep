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
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import {
    Tabs,
    TabsContent,
    TabsList,
    TabsTrigger,
} from '@/components/ui/tabs'
import { Copy, Check, TriangleAlert, Plus } from 'lucide-vue-next'
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
    // Emitted after successful create or update so the parent can refresh.
    saved: []
}>()

// ---------------------------------------------------------------------------
// Mode
// ---------------------------------------------------------------------------

const isEdit = computed(() => !!props.agent)

// Internal open state used in new mode (self-contained trigger).
// In edit mode the parent controls open via the `open` prop.
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
// ---------------------------------------------------------------------------

const schema = computed(() =>
    toTypedSchema(
        z.object({
            name: z.string('Name is required').min(1, 'Name is required'),
            hostname: isEdit.value
                ? z.string().optional()
                : z.string('Hostname is required').min(1, 'Hostname is required'),
        })
    )
)

const { handleSubmit, resetForm, errors, setValues } = useForm({
    validationSchema: schema,
})

const { value: name } = useField<string>('name')
const { value: hostname } = useField<string>('hostname')

// Prefill in edit mode whenever the sheet opens.
watch(sheetOpen, (open) => {
    if (open && isEdit.value && props.agent) {
        setValues({ name: props.agent.name })
    }
})

// ---------------------------------------------------------------------------
// State
// ---------------------------------------------------------------------------

const submitLoading = ref(false)
const submitError = ref<string | null>(null)

// New mode only — token dialog shown after creation.
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
    submitLoading.value = true
    submitError.value = null

    try {
        if (isEdit.value && props.agent) {
            // Edit mode — PATCH, only name is editable.
            await api<ApiResponse<Agent>>(`/api/v1/agents/${props.agent.id}`, {
                method: 'PATCH',
                body: { name: values.name },
            })
            setOpen(false)
            emit('saved')
        } else {
            // New mode — POST, show token dialog on success.
            const res = await api<ApiResponse<CreateAgentResponse>>('/api/v1/agents', {
                method: 'POST',
                body: { name: values.name, hostname: values.hostname ?? '' },
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
    } finally {
        submitLoading.value = false
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

        <!-- Trigger only rendered in new mode — edit mode is controlled by parent -->
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
                        Update the display name for <strong>{{ props.agent?.hostname }}</strong>.
                    </template>
                    <template v-else>
                        Register a new agent. A one-time token will be generated — use it to
                        start the agent on the target machine.
                    </template>
                </SheetDescription>
            </SheetHeader>

            <form class="flex flex-col gap-5 py-6" @submit.prevent="onSubmit">

                <!-- Name -->
                <div class="flex flex-col gap-2 px-4">
                    <Label for="agent-name">Name</Label>
                    <Input id="agent-name" v-model="name" placeholder="e.g. prod-db-01" :disabled="submitLoading"
                        autocomplete="off" />
                    <p v-if="errors.name" class="text-sm text-destructive">{{ errors.name }}</p>
                </div>

                <!-- Hostname — new mode: editable, edit mode: read-only -->
                <div class="flex flex-col gap-2 px-4">
                    <Label for="agent-hostname">Hostname</Label>
                    <template v-if="isEdit">
                        <Input id="agent-hostname" :value="props.agent?.hostname" disabled
                            class="text-muted-foreground" />
                        <p class="text-xs text-muted-foreground">
                            Hostname is set by the agent and cannot be changed.
                        </p>
                    </template>
                    <template v-else>
                        <Input id="agent-hostname" v-model="hostname" placeholder="e.g. db-01.internal"
                            :disabled="submitLoading" autocomplete="off" />
                        <p v-if="errors.hostname" class="text-sm text-destructive">{{ errors.hostname }}</p>
                    </template>
                </div>

                <p v-if="submitError" class="text-sm text-destructive">{{ submitError }}</p>

                <SheetFooter class="mt-2">
                    <Button type="button" variant="outline" :disabled="submitLoading" @click="onOpenChange(false)">
                        Cancel
                    </Button>
                    <Button type="submit" :disabled="submitLoading">
                        {{ submitLoading ?
                            (isEdit ? 'Saving…' : 'Creating…') :
                            (isEdit ? 'Save Changes' : 'Create Agent')
                        }}
                    </Button>
                </SheetFooter>
            </form>
        </SheetContent>
    </Sheet>

    <!-- ── Token dialog (new mode only) ─────────────────────────────────────── -->
    <Dialog v-if="!isEdit" :open="tokenDialogOpen" @update:open="tokenDialogOpen = $event">
        <DialogContent>
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

            <div class="flex items-center gap-2">
                <code class="flex-1 px-3 py-2 font-mono text-sm break-all rounded-md select-all bg-muted">
                {{ registrationToken }}
            </code>
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
                        <pre class="px-4 py-3 overflow-x-auto font-mono text-xs whitespace-pre rounded-md bg-muted">{{
                            cliSnippet }}</pre>
                    </TabsContent>
                    <TabsContent value="env">
                        <pre class="px-4 py-3 overflow-x-auto font-mono text-xs whitespace-pre rounded-md bg-muted">{{
                            envSnippet }}</pre>
                    </TabsContent>
                    <TabsContent value="docker">
                        <pre class="px-4 py-3 overflow-x-auto font-mono text-xs whitespace-pre rounded-md bg-muted">{{
                            dockerSnippet }}</pre>
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
        </DialogContent>
    </Dialog>
</template>