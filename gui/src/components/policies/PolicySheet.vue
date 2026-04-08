<script setup lang="ts">
import { ref, watch, computed } from 'vue'
import { useForm, useField, useFieldArray } from 'vee-validate'
import { toTypedSchema } from '@vee-validate/zod'
import * as z from 'zod'
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
import { Label } from '@/components/ui/label'
import { Switch } from '@/components/ui/switch'
import {
  Field,
  FieldError,
  FieldGroup,
  FieldLabel,
} from '@/components/ui/field'
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from '@/components/ui/select'
import { Separator } from '@/components/ui/separator'
import { Badge } from '@/components/ui/badge'
import {
  Collapsible,
  CollapsibleContent,
  CollapsibleTrigger,
} from '@/components/ui/collapsible'
import {
  AlertCircle,
  Loader2,
  Plus,
  Trash2,
  ChevronUp,
  ChevronDown,
  ChevronRight,
  Eye,
  EyeOff,
  RefreshCw,
} from 'lucide-vue-next'
import { api } from '@/services/api'
import type { ApiResponse, Policy, Agent, Destination, VolumeInfo } from '@/types'
import { useAuthStore } from '@/stores/auth'

const authStore = useAuthStore()

// ---------------------------------------------------------------------------
// Props / emits
// ---------------------------------------------------------------------------

const props = defineProps<{
  open: boolean
  policy: Policy | null
}>()

const emit = defineEmits<{
  'update:open': [value: boolean]
  saved: []
}>()

const isEdit = computed(() => !!props.policy)

// ---------------------------------------------------------------------------
// Remote data
// ---------------------------------------------------------------------------

const agents = ref<Agent[]>([])
const availableDestinations = ref<Destination[]>([])
const loadingData = ref(false)

async function loadRemoteData() {
  loadingData.value = true
  try {
    const [agentsRes, destRes] = await Promise.all([
      api<ApiResponse<{ items: Agent[]; total: number }>>('/api/v1/agents'),
      api<ApiResponse<{ items: Destination[]; total: number }>>('/api/v1/destinations'),
    ])
    agents.value = agentsRes.data.items ?? []
    availableDestinations.value = (destRes.data.items ?? []).filter(d => d.enabled)
  } catch {
    // Non-fatal — selects will render empty.
  } finally {
    loadingData.value = false
  }
}

// ---------------------------------------------------------------------------
// Docker volume auto-discovery
// ---------------------------------------------------------------------------

// Volumes are fetched lazily when the user selects a docker-volume source
// type. The list is cached per agent for the duration of the sheet session
// and cleared on close to avoid stale data.
const agentVolumes = ref<VolumeInfo[]>([])
const volumesLoading = ref(false)
const volumesError = ref('')

// selectedVolumes maps field.key -> Set of selected volume names.
// Used for the multi-select UI when source type is docker-volume.
// On submit, each source with multiple selected volumes is expanded into
// separate source entries so the backend receives one path per entry.
const selectedVolumes = ref<Record<string, Set<string>>>({})

function getSelectedVolumes(key: string): Set<string> {
  if (!selectedVolumes.value[key]) {
    selectedVolumes.value[key] = new Set()
  }
  return selectedVolumes.value[key]!
}

function toggleVolume(key: string, name: string) {
  const set = getSelectedVolumes(key)
  if (set.has(name)) set.delete(name)
  else set.add(name)
  // Trigger reactivity — replace the set reference
  selectedVolumes.value = { ...selectedVolumes.value, [key]: new Set(set) }
}


async function fetchVolumes() {
  if (!agentValue.value) return
  agentVolumes.value = []
  volumesError.value = ''
  volumesLoading.value = true
  try {
    const res = await api<ApiResponse<VolumeInfo[]>>(`/api/v1/agents/${agentValue.value}/volumes`)
    agentVolumes.value = res.data
  } catch (err: any) {
    const code = err?.data?.error?.code
    if (code === 'conflict') {
      volumesError.value = 'Agent is not connected'
    } else if (code === 'docker_unavailable') {
      volumesError.value = 'Docker is not available on this agent'
    } else if (code === 'timeout') {
      volumesError.value = 'Agent did not respond in time'
    } else {
      volumesError.value = 'Could not load volumes'
    }
  } finally {
    volumesLoading.value = false
  }
}


// ---------------------------------------------------------------------------
// Zod schema
// ---------------------------------------------------------------------------

// SourceType uses "docker-volume" (hyphen) to match the frontend SourceType enum.
const SOURCE_TYPES = ['directory', 'docker-volume'] as const
type SourceTypeValue = typeof SOURCE_TYPES[number]

const sourceItemSchema = z.object({
  type: z.enum(SOURCE_TYPES),
  // path is required for directory; for docker-volume the selection lives in
  // selectedVolumes and path stays empty until serialisation.
  path: z.string().optional().default(''),
  label: z.string().optional(),
}).superRefine((val, ctx) => {
  if (val.type === 'directory' && (!val.path || val.path.trim() === '')) {
    ctx.addIssue({ code: 'custom', path: ['path'], message: 'Path is required' })
  }
})

const hookFieldSchema = z.object({
  enabled: z.boolean(),
  name: z.string().optional(),
  command: z.string().optional(),
  // Args edited as a space-separated string and split on submit.
  args: z.string().optional(),
  timeout_secs: z.coerce.number().int().min(0).optional(),
})

const schema = z.object({
  name: z.string().min(1, 'Name is required'),
  agent_id: z.string().min(1, 'Agent is required'),
  enabled: z.boolean(),

  // Write-only — required on create, optional on edit (blank = keep existing).
  repo_password: z.string().optional(),
  repo_password_confirm: z.string().optional(),

  schedule: z.string().min(1, 'Schedule is required'),

  sources: z.array(sourceItemSchema).min(1, 'At least one source is required'),

  retention_keep_daily: z.coerce.number().int().min(0),
  retention_keep_weekly: z.coerce.number().int().min(0),
  retention_keep_monthly: z.coerce.number().int().min(0),
  retention_keep_yearly: z.coerce.number().int().min(0),

  // Destination IDs in priority order (index 0 = priority 1).
  ordered_destination_ids: z.array(z.string()),

  hook_pre: hookFieldSchema,
  hook_post: hookFieldSchema,
}).superRefine((data, ctx) => {
  if (!isEdit.value) {
    if (!data.repo_password || data.repo_password.length < 8) {
      ctx.addIssue({
        code: "custom",
        path: ['repo_password'],
        message: 'Repository password must be at least 8 characters',
      })
    }
    if (data.repo_password !== data.repo_password_confirm) {
      ctx.addIssue({
        code: "custom",
        path: ['repo_password_confirm'],
        message: 'Passwords do not match',
      })
    }
  } else if (data.repo_password && data.repo_password.length > 0) {
    if (data.repo_password.length < 8) {
      ctx.addIssue({
        code: "custom",
        path: ['repo_password'],
        message: 'Repository password must be at least 8 characters',
      })
    }
    if (data.repo_password !== data.repo_password_confirm) {
      ctx.addIssue({
        code: "custom",
        path: ['repo_password_confirm'],
        message: 'Passwords do not match',
      })
    }
  }
})

type FormValues = z.infer<typeof schema>

// ---------------------------------------------------------------------------
// Form setup
// ---------------------------------------------------------------------------

const { handleSubmit, resetForm, setValues } = useForm<FormValues>({
  validationSchema: toTypedSchema(schema),
})

// General
const { value: nameValue, errorMessage: nameError } = useField<string>('name')
const { value: agentValue, errorMessage: agentError } = useField<string>('agent_id')

// The agent currently selected in the form — used to decide whether
// to show the Docker Volume option and to fetch volumes on demand.
const selectedAgent = computed<Agent | null>(() =>
  agents.value.find(a => a.id === agentValue.value) ?? null
)

// When the agent selection changes, clear the cached volume list.
// It will be re-fetched on demand when the user opens a docker-volume source.
watch(
  () => agentValue.value,
  (newId) => {
    agentVolumes.value = []
    volumesError.value = ''
    selectedVolumes.value = {}
    // Auto-fetch if there's already a docker-volume source row
    if (newId && sourceFields.value.some(f => (f.value as any).type === 'docker-volume')) {
      fetchVolumes()
    }
  }
)

// Called when a source's type changes. Triggers a volume fetch if needed.
function onSourceTypeChange(idx: number, newType: string) {
  ; (sourceFields.value[idx]!.value as any).path = ''
  // Clear any previous volume selection for this row
  selectedVolumes.value = { ...selectedVolumes.value, [String(idx)]: new Set() }
  if (newType === 'docker-volume' && agentValue.value) {
    if (agentVolumes.value.length === 0 && !volumesLoading.value) {
      fetchVolumes()
    }
  }
}
const { value: enabledValue } = useField<boolean>('enabled')

// Repository password
const { value: repoPassValue, errorMessage: repoPassError } = useField<string>('repo_password')
const { value: repoPassConfirmValue, errorMessage: repoPassConfirmError } = useField<string>('repo_password_confirm')
const showPassword = ref(false)

// Sources
const { fields: sourceFields, push: pushSource, remove: removeSource } =
  useFieldArray<z.infer<typeof sourceItemSchema>>('sources')

function addSource() {
  pushSource({ type: 'directory', path: '', label: '' })
}

function removeSourceRow(idx: number) {
  removeSource(idx)
  // Re-index selectedVolumes: shift keys > idx down by one and drop the removed key.
  const rebuilt: Record<string, Set<string>> = {}
  for (const [k, v] of Object.entries(selectedVolumes.value)) {
    const n = Number(k)
    if (n < idx) rebuilt[k] = v
    else if (n > idx) rebuilt[String(n - 1)] = v
    // n === idx is dropped
  }
  selectedVolumes.value = rebuilt
}

// Schedule
const { value: scheduleValue, errorMessage: scheduleError } = useField<string>('schedule')

const SCHEDULE_PRESETS = [
  { label: 'Every hour', value: '0 * * * *' },
  { label: 'Daily at 02:00', value: '0 2 * * *' },
  { label: 'Daily at midnight', value: '0 0 * * *' },
  { label: 'Weekly (Sunday)', value: '0 2 * * 0' },
  { label: 'Weekly (Monday)', value: '0 2 * * 1' },
  { label: 'Monthly', value: '0 2 1 * *' },
]

const selectedPreset = ref('')

function applyPreset(value: string) {
  scheduleValue.value = value
  selectedPreset.value = value
}

// Retention
const { value: retDailyValue, errorMessage: retDailyError } = useField<number>('retention_keep_daily')
const { value: retWeeklyValue, errorMessage: retWeeklyError } = useField<number>('retention_keep_weekly')
const { value: retMonthlyValue, errorMessage: retMonthlyError } = useField<number>('retention_keep_monthly')
const { value: retYearlyValue, errorMessage: retYearlyError } = useField<number>('retention_keep_yearly')

// Destinations
const { value: orderedDestIds } = useField<string[]>('ordered_destination_ids')

function toggleDest(id: string) {
  const arr = [...(orderedDestIds.value ?? [])]
  const idx = arr.indexOf(id)
  if (idx === -1) arr.push(id)
  else arr.splice(idx, 1)
  orderedDestIds.value = arr
}

function moveDestUp(index: number) {
  const arr = [...(orderedDestIds.value ?? [])]
  if (index === 0) return
  const tmp = arr[index - 1] as string
  arr[index - 1] = arr[index] as string
  arr[index] = tmp
  orderedDestIds.value = arr
}

function moveDestDown(index: number) {
  const arr = [...(orderedDestIds.value ?? [])]
  if (index >= arr.length - 1) return
  const tmp = arr[index] as string
  arr[index] = arr[index + 1] as string
  arr[index + 1] = tmp
  orderedDestIds.value = arr
}

function isDestSelected(id: string): boolean {
  return orderedDestIds.value?.includes(id) ?? false
}

function destPriority(id: string): number {
  return (orderedDestIds.value?.indexOf(id) ?? -1) + 1
}

function destByIdName(id: string): string {
  return availableDestinations.value.find(d => d.id === id)?.name ?? id
}

// Hooks
const { value: hookPreEnabled } = useField<boolean>('hook_pre.enabled')
const { value: hookPreName } = useField<string>('hook_pre.name')
const { value: hookPreCommand } = useField<string>('hook_pre.command')
const { value: hookPreArgs } = useField<string>('hook_pre.args')
const { value: hookPreTimeout } = useField<number>('hook_pre.timeout_secs')

const { value: hookPostEnabled } = useField<boolean>('hook_post.enabled')
const { value: hookPostName } = useField<string>('hook_post.name')
const { value: hookPostCommand } = useField<string>('hook_post.command')
const { value: hookPostArgs } = useField<string>('hook_post.args')
const { value: hookPostTimeout } = useField<number>('hook_post.timeout_secs')

const hooksOpen = ref(false)

// ---------------------------------------------------------------------------
// Reset / populate
// ---------------------------------------------------------------------------

function defaultValues(): FormValues {
  return {
    name: '',
    agent_id: '',
    enabled: true,
    repo_password: '',
    repo_password_confirm: '',
    schedule: '0 2 * * *',
    sources: [{ type: 'directory', path: '', label: '' }],
    retention_keep_daily: 7,
    retention_keep_weekly: 4,
    retention_keep_monthly: 6,
    retention_keep_yearly: 1,
    ordered_destination_ids: [],
    hook_pre: { enabled: false, name: '', command: '', args: '', timeout_secs: 30 },
    hook_post: { enabled: false, name: '', command: '', args: '', timeout_secs: 30 },
  }
}

watch(
  () => props.open,
  async (open) => {
    if (!open) {
      // Clear volume cache on close to avoid stale data next time.
      agentVolumes.value = []
      volumesError.value = ''
      selectedVolumes.value = {}
      return
    }

    selectedPreset.value = ''
    hooksOpen.value = false
    showPassword.value = false

    // Reset the form immediately with whatever data is already available so
    // the sheet never shows stale values from a previous session while the
    // async fetches are in flight.
    if (props.policy) {
      populateForm(props.policy)
    } else {
      resetForm({ values: defaultValues() })
      selectedPreset.value = '0 2 * * *'
    }

    // Load agents/destinations and — for edit mode — the full policy record
    // (which includes destinations) concurrently to minimise total wait time.
    const remoteDataPromise = loadRemoteData()

    if (props.policy) {
      const [, fullRes] = await Promise.allSettled([
        remoteDataPromise,
        api<ApiResponse<Policy>>(`/api/v1/policies/${props.policy.id}`),
      ])

      // Re-populate with the complete record once both fetches are done.
      if (fullRes.status === 'fulfilled') {
        populateForm(fullRes.value.data)
      }
    } else {
      await remoteDataPromise
    }
  },
  { immediate: true }
)

// ---------------------------------------------------------------------------
// populateForm — fills the form from a Policy object.
// Called immediately on open with the list payload (fast, no destinations),
// then again once the full record arrives (adds destinations + volumes).
// ---------------------------------------------------------------------------

function populateForm(p: Policy) {
  let parsedSources: { type: string; path: string; label?: string }[] = []
  try {
    parsedSources = typeof p.sources === 'string' ? JSON.parse(p.sources) : (p.sources ?? [])
  } catch { /* fallback to empty */ }

  // Re-group docker-volume entries that were expanded on save back into a
  // single source row per group. Two entries belong to the same group when
  // they are adjacent, share the same type "docker-volume", and have the
  // same label. directory entries are kept as-is (one entry = one row).
  type RawSource = { type: string; path: string; label?: string }
  const grouped: RawSource[] = []
  const volumesByRowIndex: Record<number, string[]> = {}

  for (const src of parsedSources) {
    if (src.type !== 'docker-volume') {
      grouped.push(src)
      continue
    }
    // Find an existing docker-volume row with the same label to merge into.
    let existingIdx = -1
    for (let i = grouped.length - 1; i >= 0; i--) {
      if (grouped[i]!.type === 'docker-volume' && (grouped[i]!.label ?? '') === (src.label ?? '')) {
        existingIdx = i
        break
      }
    }
    if (existingIdx !== -1) {
      // Merge into existing row — accumulate volume name.
      if (!volumesByRowIndex[existingIdx]) volumesByRowIndex[existingIdx] = []
      volumesByRowIndex[existingIdx]!.push(src.path)
    } else {
      // New row — record this as a new group.
      const newIdx = grouped.length
      grouped.push({ type: src.type, path: '', label: src.label ?? '' })
      volumesByRowIndex[newIdx] = [src.path]
    }
  }

  const mappedSources = grouped.map(s => ({
    type: s.type as SourceTypeValue,
    path: s.path ?? '',
    label: s.label ?? '',
  }))

  // Build the new selectedVolumes map indexed by source row position.
  const newSelectedVolumes: Record<string, Set<string>> = {}
  for (const [idxStr, names] of Object.entries(volumesByRowIndex)) {
    newSelectedVolumes[idxStr] = new Set(names)
  }
  selectedVolumes.value = newSelectedVolumes

  const preDestIds = (p.destinations ?? [])
    .sort((a, b) => a.priority - b.priority)
    .map(d => d.destination_id)

  setValues({
    name: p.name,
    agent_id: p.agent_id,
    enabled: p.enabled,
    repo_password: '',
    repo_password_confirm: '',
    schedule: p.schedule,
    sources: mappedSources.length > 0
      ? mappedSources
      : [{ type: 'directory', path: '', label: '' }],
    retention_keep_daily: p.retention_daily ?? 7,
    retention_keep_weekly: p.retention_weekly ?? 4,
    retention_keep_monthly: p.retention_monthly ?? 6,
    retention_keep_yearly: p.retention_yearly ?? 1,
    ordered_destination_ids: preDestIds,
    hook_pre: { enabled: false, name: '', command: '', args: '', timeout_secs: 30 },
    hook_post: { enabled: false, name: '', command: '', args: '', timeout_secs: 30 },
  } as unknown as FormValues)

  const match = SCHEDULE_PRESETS.find(s => s.value === p.schedule)
  selectedPreset.value = match?.value ?? ''

  // Pre-fetch volumes if any source row is docker-volume type.
  if (mappedSources.some(s => s.type === 'docker-volume') && p.agent_id) {
    fetchVolumes()
  }
}

// ---------------------------------------------------------------------------
// Submit
// ---------------------------------------------------------------------------

const submitError = ref<string | null>(null)
const submitting = ref(false)

/**
 * Converts a hook form section to a JSON string for the API.
 * Returns an empty string when the hook is disabled or has no command.
 */
function serialiseHook(hook: z.infer<typeof hookFieldSchema>): string {
  if (!hook.enabled || !hook.command) return ''
  return JSON.stringify({
    name: hook.name ?? '',
    command: hook.command,
    args: hook.args ? hook.args.split(/\s+/).filter(Boolean) : [],
    timeout_secs: hook.timeout_secs ?? 30,
  })
}

const onSubmit = handleSubmit(async (values) => {
  // Validate that every docker-volume source has at least one volume selected.
  for (let idx = 0; idx < values.sources.length; idx++) {
    const s = values.sources[idx]
    if (s?.type === 'docker-volume') {
      const sel = selectedVolumes.value[String(idx)]
      if (!sel || sel.size === 0) {
        submitError.value = `Source ${idx + 1}: select at least one Docker volume.`
        return
      }
    }
  }

  submitting.value = true
  submitError.value = null

  try {
    const destinationsPayload = values.ordered_destination_ids.map((id, idx) => ({
      destination_id: id,
      priority: idx + 1,
    }))

    const body: Record<string, unknown> = {
      name: values.name,
      schedule: values.schedule,
      sources: JSON.stringify(
        values.sources.flatMap((s, idx) => {
          if (s.type !== 'docker-volume') return [s]
          const sel = selectedVolumes.value[String(idx)]
          if (!sel || sel.size === 0) return [{ ...s, path: s.path || '' }]
          // Expand each selected volume into its own source entry
          return Array.from(sel).map(name => ({ type: s.type, path: name, label: s.label ?? '' }))
        })
      ),
      retention_daily: values.retention_keep_daily,
      retention_weekly: values.retention_keep_weekly,
      retention_monthly: values.retention_keep_monthly,
      retention_yearly: values.retention_keep_yearly,
      hook_pre_backup: serialiseHook(values.hook_pre),
      hook_post_backup: serialiseHook(values.hook_post),
    }

    if (isEdit.value) {
      // PATCH-only: enabled, optional new password
      body.enabled = values.enabled
      if (values.repo_password) body.repo_password = values.repo_password
    } else {
      // POST-only: agent, password (required), destinations
      body.agent_id = values.agent_id
      body.repo_password = values.repo_password ?? ''
      body.destinations = destinationsPayload
    }

    if (isEdit.value && props.policy) {
      await api(`/api/v1/policies/${props.policy.id}`, { method: 'PATCH', body })
    } else {
      await api('/api/v1/policies', { method: 'POST', body })
    }

    emit('update:open', false)
    emit('saved')
  } catch (e: any) {
    submitError.value = e?.data?.error?.message ?? e?.message ?? 'Failed to save policy'
  } finally {
    submitting.value = false
  }
})

function onOpenChange(value: boolean) {
  if (!value) {
    submitError.value = null
  }
  emit('update:open', value)
}
</script>

<template>
  <Sheet :open="props.open" @update:open="onOpenChange">
    <SheetContent class="sm:max-w-2xl overflow-y-auto">
      <SheetHeader>
        <SheetTitle>{{ isEdit ? 'Edit Policy' : 'New Policy' }}</SheetTitle>
        <SheetDescription>
          {{ isEdit ? 'Update the policy settings.' : 'Configure a new backup policy.' }}
        </SheetDescription>
      </SheetHeader>

      <form class="py-6 px-4" novalidate @submit.prevent="onSubmit">
        <FieldGroup>

          <!-- Error banner -->
          <Transition enter-active-class="transition-all duration-200" enter-from-class="-translate-y-1 opacity-0"
            leave-active-class="transition-all duration-150" leave-to-class="-translate-y-1 opacity-0">
            <Alert v-if="submitError" variant="destructive">
              <AlertCircle class="size-4" />
              <AlertDescription>{{ submitError }}</AlertDescription>
            </Alert>
          </Transition>

          <!-- ══════════════════════════════════════════════════
                         1. GENERAL
                    ══════════════════════════════════════════════════ -->

          <!-- Name -->
          <Field>
            <FieldLabel for="policy-name">Name</FieldLabel>
            <Input id="policy-name" v-model="nameValue" placeholder="e.g. Daily Database Backup"
              autocomplete="new-password"
              :class="nameError ? 'border-destructive focus-visible:ring-destructive/30' : ''" />
            <FieldError v-if="nameError">{{ nameError }}</FieldError>
          </Field>

          <!-- Agent -->
          <Field>
            <FieldLabel for="agent">Agent</FieldLabel>
            <Select :model-value="agentValue ?? ''" :disabled="loadingData"
              @update:model-value="agentValue = $event as string">
              <SelectTrigger id="agent"
                :class="agentError ? 'border-destructive focus-visible:ring-destructive/30' : ''">
                <SelectValue placeholder="Select an agent…" />
              </SelectTrigger>
              <SelectContent>
                <SelectItem v-for="agent in agents" :key="agent.id" :value="agent.id">
                  {{ agent.name }}
                </SelectItem>
                <div v-if="agents.length === 0" class="px-3 py-4 text-sm text-muted-foreground text-center">
                  No agents available
                </div>
              </SelectContent>
            </Select>
            <FieldError v-if="agentError">{{ agentError }}</FieldError>
          </Field>

          <Separator />

          <!-- ══════════════════════════════════════════════════
                         2. REPOSITORY PASSWORD (create only)
                    ══════════════════════════════════════════════════ -->
          <template v-if="!isEdit">
            <p class="text-sm font-medium">Repository Password</p>
            <p class="text-muted-foreground text-xs -mt-3">
              Required. Restic uses this to encrypt the repository. Store it safely — it cannot be recovered.
            </p>

            <Field>
              <FieldLabel for="repo_password">Password <span class="text-destructive">*</span></FieldLabel>
              <div class="relative">
                <Input id="repo_password" v-model="repoPassValue" :type="showPassword ? 'text' : 'password'"
                  autocomplete="new-password" placeholder="min. 8 characters"
                  :class="['pr-10', repoPassError ? 'border-destructive focus-visible:ring-destructive/30' : '']" />
                <button type="button"
                  class="absolute right-3 top-1/2 -translate-y-1/2 text-muted-foreground hover:text-foreground transition-colors"
                  @click="showPassword = !showPassword">
                  <EyeOff v-if="showPassword" class="size-4" />
                  <Eye v-else class="size-4" />
                </button>
              </div>
              <FieldError v-if="repoPassError">{{ repoPassError }}</FieldError>
            </Field>

            <Field>
              <FieldLabel for="repo_password_confirm">Confirm Password <span class="text-destructive">*</span>
              </FieldLabel>
              <Input id="repo_password_confirm" v-model="repoPassConfirmValue"
                :type="showPassword ? 'text' : 'password'" autocomplete="new-password" placeholder="repeat password"
                :class="repoPassConfirmError ? 'border-destructive focus-visible:ring-destructive/30' : ''" />
              <FieldError v-if="repoPassConfirmError">{{ repoPassConfirmError }}</FieldError>
            </Field>

            <Separator />
          </template>

          <!-- ══════════════════════════════════════════════════
                         3. SOURCES
                    ══════════════════════════════════════════════════ -->
          <div class="flex items-center justify-between">
            <p class="text-sm font-medium">Sources</p>
            <Button type="button" variant="outline" size="sm" @click="addSource">
              <Plus class="w-3.5 h-3.5" />
              Add Source
            </Button>
          </div>

          <div v-if="sourceFields.length === 0"
            class="rounded-md border border-dashed px-4 py-6 text-center text-sm text-muted-foreground">
            No sources added. Click <strong>Add Source</strong> to begin.
          </div>

          <div v-for="(field, idx) in sourceFields" :key="field.key" class="flex flex-col gap-3 rounded-md border p-3">
            <div class="flex items-center justify-between">
              <span class="text-xs font-medium text-muted-foreground">Source {{ idx + 1 }}</span>
              <Button type="button" variant="ghost" size="icon"
                class="w-6 h-6 text-muted-foreground hover:text-destructive" @click="removeSourceRow(idx)">
                <Trash2 class="w-3.5 h-3.5" />
              </Button>
            </div>

            <!-- Type — full width -->
            <div class="flex flex-col gap-1.5">
              <Label :for="`source-type-${idx}`" class="text-sm">Type</Label>
              <Select :model-value="(field.value as any).type as string"
                @update:model-value="(v) => { (field.value as any).type = v as SourceTypeValue; onSourceTypeChange(idx, v as string) }">
                <SelectTrigger :id="`source-type-${idx}`" class="w-full">
                  <SelectValue />
                </SelectTrigger>
                <SelectContent>
                  <SelectItem value="directory">Directory</SelectItem>
                  <SelectItem value="docker-volume"
                    :disabled="selectedAgent !== null && !selectedAgent.docker_available">
                    Docker Volume
                    <span v-if="selectedAgent && !selectedAgent.docker_available"
                      class="text-xs text-muted-foreground ml-1">(unavailable)</span>
                  </SelectItem>
                </SelectContent>
              </Select>
            </div>

            <!-- Label — full width -->
            <div class="flex flex-col gap-1.5">
              <Label :for="`source-label-${idx}`" class="text-sm">
                Label
                <span class="text-muted-foreground font-normal">(optional)</span>
              </Label>
              <Input :id="`source-label-${idx}`" :model-value="(field.value as any).label as string" class="w-full"
                placeholder="e.g. postgres-data" @update:model-value="(field.value as any).label = $event" />
            </div>

            <!-- Path / Volumes — full width -->
            <div class="flex flex-col gap-1.5">
              <div class="flex items-center justify-between">
                <Label class="text-sm">
                  {{ (field.value as any).type === 'docker-volume' ? 'Volumes' : 'Path' }}
                </Label>
                <button v-if="(field.value as any).type === 'docker-volume' && agentValue" type="button"
                  class="flex items-center gap-1 text-xs text-muted-foreground hover:text-foreground transition-colors"
                  :disabled="volumesLoading" @click="fetchVolumes">
                  <RefreshCw class="w-3 h-3" :class="{ 'animate-spin': volumesLoading }" />
                  Refresh
                </button>
              </div>

              <!-- docker-volume -->
              <template v-if="(field.value as any).type === 'docker-volume'">

                <!-- Volumes loaded — multi-select checklist -->
                <template v-if="agentVolumes.length > 0">
                  <div class="flex flex-col gap-1 rounded-md border p-2">
                    <label v-for="vol in agentVolumes" :key="vol.name"
                      class="flex items-center gap-2.5 rounded px-2 py-1.5 cursor-pointer hover:bg-muted/50 transition-colors">
                      <div class="w-4 h-4 rounded border shrink-0 flex items-center justify-center transition-colors"
                        :class="getSelectedVolumes(String(idx)).has(vol.name)
                          ? 'bg-primary border-primary'
                          : 'border-muted-foreground/40'">
                        <svg v-if="getSelectedVolumes(String(idx)).has(vol.name)"
                          class="w-2.5 h-2.5 text-primary-foreground" viewBox="0 0 10 10" fill="none">
                          <path d="M1.5 5l2.5 2.5 4.5-4.5" stroke="currentColor" stroke-width="1.5"
                            stroke-linecap="round" stroke-linejoin="round" />
                        </svg>
                      </div>
                      <input type="checkbox" class="sr-only" :checked="getSelectedVolumes(String(idx)).has(vol.name)"
                        @change="toggleVolume(String(idx), vol.name)" />
                      <span class="font-mono text-sm truncate max-w-[60%]">{{ vol.name }}</span>
                      <span class="text-xs text-muted-foreground ml-auto shrink-0">{{ vol.driver }}</span>
                    </label>
                  </div>
                  <p v-if="getSelectedVolumes(String(idx)).size === 0" class="text-xs text-muted-foreground">
                    Select at least one volume.
                  </p>
                </template>

                <!-- Loading / error / no agent state -->
                <template v-else>
                  <div v-if="volumesLoading" class="flex items-center gap-2 text-sm text-muted-foreground py-2">
                    <Loader2 class="size-4 animate-spin" />
                    Loading volumes…
                  </div>
                  <template v-else>
                    <p v-if="volumesError" class="text-xs text-destructive">
                      {{ volumesError }}.
                      <button type="button" class="underline hover:no-underline" @click="fetchVolumes">Retry</button>
                      or enter names manually below.
                    </p>
                    <p v-else-if="!agentValue" class="text-xs text-muted-foreground">
                      Select an agent first to load available volumes.
                    </p>
                    <!-- Manual fallback input (always shown when no list) -->
                    <Input :id="`source-path-${idx}`" :model-value="(field.value as any).path as string"
                      class="font-mono w-full" placeholder="e.g. postgres_data"
                      @update:model-value="(field.value as any).path = $event" />
                  </template>
                </template>
              </template>

              <!-- directory: plain path input -->
              <template v-else>
                <Input :id="`source-path-${idx}`" :model-value="(field.value as any).path as string"
                  class="font-mono w-full" placeholder="e.g. /var/lib/data"
                  @update:model-value="(field.value as any).path = $event" />
              </template>
            </div>
          </div>

          <Separator />

          <!-- ══════════════════════════════════════════════════
                         4. SCHEDULE
                    ══════════════════════════════════════════════════ -->
          <p class="text-sm font-medium">Schedule</p>

          <!-- Preset buttons -->
          <div class="flex flex-wrap gap-1.5">
            <button v-for="preset in SCHEDULE_PRESETS" :key="preset.value" type="button"
              class="rounded-full border px-2.5 py-0.5 text-xs transition-colors" :class="selectedPreset === preset.value
                ? 'border-primary bg-primary text-primary-foreground'
                : 'border-border hover:border-primary/50 hover:bg-muted'" @click="applyPreset(preset.value)">
              {{ preset.label }}
            </button>
          </div>

          <Field>
            <FieldLabel for="schedule">Cron Expression</FieldLabel>
            <Input id="schedule" v-model="scheduleValue" class="font-mono" placeholder="0 2 * * *"
              :class="scheduleError ? 'border-destructive focus-visible:ring-destructive/30' : ''" />
            <FieldError v-if="scheduleError">{{ scheduleError }}</FieldError>
          </Field>

          <Separator />

          <!-- ══════════════════════════════════════════════════
                         5. RETENTION
                    ══════════════════════════════════════════════════ -->
          <p class="text-sm font-medium">Retention</p>
          <p class="text-muted-foreground text-xs -mt-3">
            Number of snapshots to keep per period. Set to 0 to disable that tier.
          </p>

          <div class="grid grid-cols-2 gap-3">
            <Field>
              <FieldLabel for="ret-daily">Daily</FieldLabel>
              <Input id="ret-daily" v-model="retDailyValue" type="number" min="0"
                :class="retDailyError ? 'border-destructive focus-visible:ring-destructive/30' : ''" />
              <FieldError v-if="retDailyError">{{ retDailyError }}</FieldError>
            </Field>
            <Field>
              <FieldLabel for="ret-weekly">Weekly</FieldLabel>
              <Input id="ret-weekly" v-model="retWeeklyValue" type="number" min="0"
                :class="retWeeklyError ? 'border-destructive focus-visible:ring-destructive/30' : ''" />
              <FieldError v-if="retWeeklyError">{{ retWeeklyError }}</FieldError>
            </Field>
            <Field>
              <FieldLabel for="ret-monthly">Monthly</FieldLabel>
              <Input id="ret-monthly" v-model="retMonthlyValue" type="number" min="0"
                :class="retMonthlyError ? 'border-destructive focus-visible:ring-destructive/30' : ''" />
              <FieldError v-if="retMonthlyError">{{ retMonthlyError }}</FieldError>
            </Field>
            <Field>
              <FieldLabel for="ret-yearly">Yearly</FieldLabel>
              <Input id="ret-yearly" v-model="retYearlyValue" type="number" min="0"
                :class="retYearlyError ? 'border-destructive focus-visible:ring-destructive/30' : ''" />
              <FieldError v-if="retYearlyError">{{ retYearlyError }}</FieldError>
            </Field>
          </div>

          <Separator />

          <!-- ══════════════════════════════════════════════════
                         6. DESTINATIONS
                    ══════════════════════════════════════════════════ -->
          <p class="text-sm font-medium">Destinations</p>
          <p class="text-muted-foreground text-xs -mt-3">
            Select where to store backups. Use ↑↓ to set the priority order.
          </p>

          <div v-if="availableDestinations.length === 0"
            class="rounded-md border border-dashed px-4 py-6 text-center text-sm text-muted-foreground">
            No destinations available. Create one first.
          </div>

          <div v-else class="flex flex-col gap-1">
            <div v-for="dest in availableDestinations" :key="dest.id"
              class="flex items-center justify-between rounded-md border px-3 py-2 cursor-pointer transition-colors"
              :class="isDestSelected(dest.id) ? 'border-primary/50 bg-primary/5' : 'hover:bg-muted/50'"
              @click="toggleDest(dest.id)">
              <div class="flex items-center gap-2.5">
                <!-- Custom checkbox indicator -->
                <div class="w-4 h-4 rounded border flex items-center justify-center shrink-0 transition-colors"
                  :class="isDestSelected(dest.id) ? 'bg-primary border-primary' : 'border-muted-foreground/40'">
                  <svg v-if="isDestSelected(dest.id)" class="w-2.5 h-2.5 text-primary-foreground" viewBox="0 0 10 10"
                    fill="none">
                    <path d="M1.5 5l2.5 2.5 4.5-4.5" stroke="currentColor" stroke-width="1.5" stroke-linecap="round"
                      stroke-linejoin="round" />
                  </svg>
                </div>
                <div>
                  <p class="text-sm font-medium leading-none">{{ dest.name }}</p>
                  <p class="text-xs text-muted-foreground mt-0.5">{{ dest.type }}</p>
                </div>
              </div>
              <Badge v-if="isDestSelected(dest.id)" variant="outline" class="text-xs tabular-nums">
                #{{ destPriority(dest.id) }}
              </Badge>
            </div>
          </div>

          <!-- Priority order list — shown only when ≥1 destination is selected -->
          <div v-if="orderedDestIds && orderedDestIds.length > 0" class="flex flex-col gap-1">
            <p class="text-xs font-medium text-muted-foreground">Priority Order</p>
            <div v-for="(id, idx) in orderedDestIds" :key="id"
              class="flex items-center gap-2 rounded-md bg-muted/50 px-3 py-1.5">
              <span class="text-xs font-mono text-muted-foreground w-5 shrink-0">{{ idx + 1 }}.</span>
              <span class="text-sm flex-1">{{ destByIdName(id) }}</span>
              <div class="flex gap-0.5">
                <Button type="button" variant="ghost" size="icon" class="w-6 h-6" :disabled="idx === 0"
                  @click.stop="moveDestUp(idx)">
                  <ChevronUp class="w-3.5 h-3.5" />
                </Button>
                <Button type="button" variant="ghost" size="icon" class="w-6 h-6"
                  :disabled="idx === orderedDestIds.length - 1" @click.stop="moveDestDown(idx)">
                  <ChevronDown class="w-3.5 h-3.5" />
                </Button>
              </div>
            </div>
          </div>

          <Separator />

          <!-- ══════════════════════════════════════════════════
                         7. HOOKS (collapsible)
                    ══════════════════════════════════════════════════ -->
          <Collapsible v-model:open="hooksOpen">
            <CollapsibleTrigger as-child>
              <button type="button"
                class="flex w-full items-center justify-between text-sm font-medium hover:text-foreground/80 transition-colors">
                <span>
                  Hooks
                  <span class="text-muted-foreground font-normal">(optional)</span>
                </span>
                <ChevronRight class="w-4 h-4 text-muted-foreground transition-transform duration-200"
                  :class="hooksOpen ? 'rotate-90' : ''" />
              </button>
            </CollapsibleTrigger>

            <CollapsibleContent>
              <div class="flex flex-col gap-4 mt-4">

                <!-- Admin-only warning -->
                <Alert v-if="!authStore.isAdmin" variant="default" class="border-amber-200 bg-amber-50 dark:border-amber-800 dark:bg-amber-950/30">
                  <AlertCircle class="h-4 w-4 text-amber-600 dark:text-amber-400" />
                  <AlertDescription class="text-amber-800 dark:text-amber-300 text-xs">
                    Hook commands run with agent process privileges. Only admins can configure hooks.
                  </AlertDescription>
                </Alert>

                <!-- Pre-backup hook -->
                <div class="rounded-md border p-3 flex flex-col gap-3">
                  <div class="flex items-center justify-between">
                    <p class="text-sm font-medium">Pre-backup</p>
                    <Switch :model-value="hookPreEnabled ?? false"
                      :disabled="!authStore.isAdmin"
                      @update:model-value="(v: boolean) => hookPreEnabled = v" />
                  </div>
                  <template v-if="hookPreEnabled">
                    <div class="grid grid-cols-2 gap-3">
                      <div class="flex flex-col gap-1.5">
                        <Label for="hook-pre-name" class="text-sm">Name</Label>
                        <Input id="hook-pre-name" v-model="hookPreName" placeholder="e.g. stop-container" :disabled="!authStore.isAdmin" />
                      </div>
                      <div class="flex flex-col gap-1.5">
                        <Label for="hook-pre-timeout" class="text-sm">Timeout (sec)</Label>
                        <Input id="hook-pre-timeout" v-model="hookPreTimeout" type="number" min="0" :disabled="!authStore.isAdmin" />
                      </div>
                    </div>
                    <div class="flex flex-col gap-1.5">
                      <Label for="hook-pre-cmd" class="text-sm">Command</Label>
                      <Input id="hook-pre-cmd" v-model="hookPreCommand" class="font-mono" placeholder="e.g. docker" :disabled="!authStore.isAdmin" />
                    </div>
                    <div class="flex flex-col gap-1.5">
                      <Label for="hook-pre-args" class="text-sm">
                        Arguments
                        <span class="text-muted-foreground font-normal">(space-separated)</span>
                      </Label>
                      <Input id="hook-pre-args" v-model="hookPreArgs" class="font-mono"
                        placeholder="e.g. stop my-container" :disabled="!authStore.isAdmin" />
                    </div>
                  </template>
                </div>

                <!-- Post-backup hook -->
                <div class="rounded-md border p-3 flex flex-col gap-3">
                  <div class="flex items-center justify-between">
                    <p class="text-sm font-medium">Post-backup</p>
                    <Switch :model-value="hookPostEnabled ?? false"
                      :disabled="!authStore.isAdmin"
                      @update:model-value="(v: boolean) => hookPostEnabled = v" />
                  </div>
                  <template v-if="hookPostEnabled">
                    <div class="grid grid-cols-2 gap-3">
                      <div class="flex flex-col gap-1.5">
                        <Label for="hook-post-name" class="text-sm">Name</Label>
                        <Input id="hook-post-name" v-model="hookPostName" placeholder="e.g. start-container" :disabled="!authStore.isAdmin" />
                      </div>
                      <div class="flex flex-col gap-1.5">
                        <Label for="hook-post-timeout" class="text-sm">Timeout (sec)</Label>
                        <Input id="hook-post-timeout" v-model="hookPostTimeout" type="number" min="0" :disabled="!authStore.isAdmin" />
                      </div>
                    </div>
                    <div class="flex flex-col gap-1.5">
                      <Label for="hook-post-cmd" class="text-sm">Command</Label>
                      <Input id="hook-post-cmd" v-model="hookPostCommand" class="font-mono" placeholder="e.g. docker" :disabled="!authStore.isAdmin" />
                    </div>
                    <div class="flex flex-col gap-1.5">
                      <Label for="hook-post-args" class="text-sm">
                        Arguments
                        <span class="text-muted-foreground font-normal">(space-separated)</span>
                      </Label>
                      <Input id="hook-post-args" v-model="hookPostArgs" class="font-mono"
                        placeholder="e.g. start my-container" :disabled="!authStore.isAdmin" />
                    </div>
                  </template>
                </div>

              </div>
            </CollapsibleContent>
          </Collapsible>

          <!-- Enabled toggle — edit mode only -->
          <template v-if="isEdit">
            <Separator />
            <div class="flex items-center justify-between">
              <div>
                <p class="text-sm font-medium">Enabled</p>
                <p class="text-muted-foreground text-xs">
                  Disabled policies are paused and won't run on schedule.
                </p>
              </div>
              <Switch :model-value="enabledValue ?? true" @update:model-value="enabledValue = $event" />
            </div>
          </template>

          <SheetFooter class="mt-2 px-0">
            <Button type="button" variant="outline" :disabled="submitting" @click="onOpenChange(false)">
              Cancel
            </Button>
            <Button type="submit" :disabled="submitting || loadingData">
              <Loader2 v-if="submitting" class="size-4 animate-spin" />
              {{ submitting ? 'Saving…' : (isEdit ? 'Save Changes' : 'Create Policy') }}
            </Button>
          </SheetFooter>
        </FieldGroup>
      </form>
    </SheetContent>
  </Sheet>
</template>