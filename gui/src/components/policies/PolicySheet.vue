<script setup lang="ts">
/**
 * PolicySheet — slide-over panel for creating and editing backup policies.
 *
 * Sections:
 *  1. General      — name, agent, enabled toggle
 *  2. Repository   — repo_password (write-only, required on create)
 *  3. Sources      — dynamic list of directory / docker_volume entries
 *  4. Schedule     — cron expression with preset shortcuts
 *  5. Retention    — keep_daily / weekly / monthly / yearly
 *  6. Destinations — ordered checklist with up/down priority controls
 *  7. Hooks        — collapsible pre / post backup commands
 *
 * NOTE on type mapping:
 *  The frontend Policy type uses:
 *    - sources: Source[] (already parsed, SourceType uses "docker-volume" with a hyphen)
 *    - retention: { keep_daily, keep_weekly, keep_monthly, keep_yearly, ... }
 *    - hook_pre_backup / hook_post_backup: not present on the list payload —
 *      the sheet only writes them; it does not read them back on edit.
 *  The backend accepts flat fields: retention_daily, hook_pre_backup (JSON string), etc.
 */
import { ref, computed, watch } from 'vue'
import { useForm, useField, useFieldArray } from 'vee-validate'
import { toTypedSchema } from '@vee-validate/zod'
import * as z from 'zod'
import {
  Sheet, SheetContent, SheetHeader, SheetTitle, SheetDescription, SheetFooter,
} from '@/components/ui/sheet'
import { Field, FieldLabel, FieldError, FieldGroup } from '@/components/ui/field'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { Switch } from '@/components/ui/switch'
import { Alert, AlertDescription } from '@/components/ui/alert'
import { Separator } from '@/components/ui/separator'
import { Badge } from '@/components/ui/badge'
import {
  Select, SelectContent, SelectItem, SelectTrigger, SelectValue,
} from '@/components/ui/select'
import {
  Collapsible, CollapsibleContent, CollapsibleTrigger,
} from '@/components/ui/collapsible'
import {
  AlertCircle, Loader2, Plus, Trash2, ChevronUp, ChevronDown,
  ChevronRight, Eye, EyeOff,
} from 'lucide-vue-next'
import { api } from '@/services/api'
import type { ApiResponse, Policy, Agent, Destination } from '@/types'

// ---------------------------------------------------------------------------
// Props & emits
// ---------------------------------------------------------------------------

const props = defineProps<{
  open: boolean
  policy: Policy | null
}>()

const emit = defineEmits<{
  (e: 'update:open', value: boolean): void
  (e: 'saved'): void
}>()

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

const isEdit = computed(() => !!props.policy)

// ---------------------------------------------------------------------------
// Remote data — agents and destinations loaded when sheet opens
// ---------------------------------------------------------------------------

const agents                = ref<Agent[]>([])
const availableDestinations = ref<Destination[]>([])
const loadingData           = ref(false)

async function loadRemoteData() {
  loadingData.value = true
  try {
    const [agentsRes, destRes] = await Promise.all([
      api<ApiResponse<{ items: Agent[]; total: number }>>('/api/v1/agents'),
      api<ApiResponse<{ items: Destination[]; total: number }>>('/api/v1/destinations'),
    ])
    agents.value               = agentsRes.data.items ?? []
    availableDestinations.value = destRes.data.items  ?? []
  } catch {
    // Non-fatal — selects will render empty; user can close and reopen.
  } finally {
    loadingData.value = false
  }
}

// ---------------------------------------------------------------------------
// Zod schema
// ---------------------------------------------------------------------------

// Source type values as used by the frontend SourceType enum.
// Note: Docker volume uses a hyphen ("docker-volume"), not an underscore.
const SOURCE_TYPES = ['directory', 'docker-volume'] as const
type SourceTypeValue = typeof SOURCE_TYPES[number]

const sourceItemSchema = z.object({
  type:  z.enum(SOURCE_TYPES),
  path:  z.string().min(1, 'Path is required'),
  label: z.string().optional(),
})

const hookFieldSchema = z.object({
  enabled:      z.boolean(),
  name:         z.string().optional(),
  command:      z.string().optional(),
  // Args are stored as a single space-separated string for ease of editing;
  // they are split on submit before sending to the API.
  args:         z.string().optional(),
  timeout_secs: z.coerce.number().int().min(0).optional(),
})

const schema = z.object({
  name:     z.string().min(1, 'Name is required'),
  agent_id: z.string().min(1, 'Agent is required'),
  enabled:  z.boolean(),

  // Write-only — required on create, optional on edit (blank = keep existing).
  repo_password:         z.string().optional(),
  repo_password_confirm: z.string().optional(),

  schedule: z.string().min(1, 'Schedule is required'),

  sources: z.array(sourceItemSchema).min(1, 'At least one source is required'),

  // Retention keep counts — match Policy.retention nested shape.
  retention_keep_daily:   z.coerce.number().int().min(0),
  retention_keep_weekly:  z.coerce.number().int().min(0),
  retention_keep_monthly: z.coerce.number().int().min(0),
  retention_keep_yearly:  z.coerce.number().int().min(0),

  // Destination IDs ordered by priority (index 0 = priority 1).
  ordered_destination_ids: z.array(z.string()),

  hook_pre:  hookFieldSchema,
  hook_post: hookFieldSchema,
}).superRefine((data, ctx) => {
  if (!isEdit.value) {
    if (!data.repo_password || data.repo_password.length < 8) {
      ctx.addIssue({
        code: z.ZodIssueCode.custom,
        path: ['repo_password'],
        message: 'Repository password must be at least 8 characters',
      })
    }
    if (data.repo_password !== data.repo_password_confirm) {
      ctx.addIssue({
        code: z.ZodIssueCode.custom,
        path: ['repo_password_confirm'],
        message: 'Passwords do not match',
      })
    }
  } else if (data.repo_password && data.repo_password.length > 0) {
    if (data.repo_password.length < 8) {
      ctx.addIssue({
        code: z.ZodIssueCode.custom,
        path: ['repo_password'],
        message: 'Repository password must be at least 8 characters',
      })
    }
    if (data.repo_password !== data.repo_password_confirm) {
      ctx.addIssue({
        code: z.ZodIssueCode.custom,
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
const { value: nameValue,  errorMessage: nameError  } = useField<string>('name')
const { value: agentValue, errorMessage: agentError } = useField<string>('agent_id')
const { value: enabledValue }                          = useField<boolean>('enabled')

// Repository password
const { value: repoPassValue,        errorMessage: repoPassError        } = useField<string>('repo_password')
const { value: repoPassConfirmValue, errorMessage: repoPassConfirmError } = useField<string>('repo_password_confirm')
const showPassword = ref(false)

// Sources — dynamic array
const { fields: sourceFields, push: pushSource, remove: removeSource } =
  useFieldArray<z.infer<typeof sourceItemSchema>>('sources')

function addSource() {
  pushSource({ type: 'directory', path: '', label: '' })
}

// Schedule
const { value: scheduleValue, errorMessage: scheduleError } = useField<string>('schedule')

const SCHEDULE_PRESETS = [
  { label: 'Every hour',        value: '0 * * * *' },
  { label: 'Daily at 02:00',    value: '0 2 * * *' },
  { label: 'Daily at midnight', value: '0 0 * * *' },
  { label: 'Weekly (Sunday)',   value: '0 2 * * 0' },
  { label: 'Weekly (Monday)',   value: '0 2 * * 1' },
  { label: 'Monthly',           value: '0 2 1 * *' },
]

const selectedPreset = ref('')

function applyPreset(value: string) {
  scheduleValue.value  = value
  selectedPreset.value = value
}

// Retention
const { value: retDailyValue,   errorMessage: retDailyError   } = useField<number>('retention_keep_daily')
const { value: retWeeklyValue,  errorMessage: retWeeklyError  } = useField<number>('retention_keep_weekly')
const { value: retMonthlyValue, errorMessage: retMonthlyError } = useField<number>('retention_keep_monthly')
const { value: retYearlyValue,  errorMessage: retYearlyError  } = useField<number>('retention_keep_yearly')

// Destinations — ordered list
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

// Hooks (collapsible)
const { value: hookPreEnabled  } = useField<boolean>('hook_pre.enabled')
const { value: hookPreName     } = useField<string>('hook_pre.name')
const { value: hookPreCommand  } = useField<string>('hook_pre.command')
const { value: hookPreArgs     } = useField<string>('hook_pre.args')
const { value: hookPreTimeout  } = useField<number>('hook_pre.timeout_secs')

const { value: hookPostEnabled } = useField<boolean>('hook_post.enabled')
const { value: hookPostName    } = useField<string>('hook_post.name')
const { value: hookPostCommand } = useField<string>('hook_post.command')
const { value: hookPostArgs    } = useField<string>('hook_post.args')
const { value: hookPostTimeout } = useField<number>('hook_post.timeout_secs')

const hooksOpen = ref(false)

// ---------------------------------------------------------------------------
// Populate form when sheet opens
// ---------------------------------------------------------------------------

function defaultValues(): FormValues {
  return {
    name:                    '',
    agent_id:                '',
    enabled:                 true,
    repo_password:           '',
    repo_password_confirm:   '',
    schedule:                '0 2 * * *',
    sources:                 [{ type: 'directory', path: '', label: '' }],
    retention_keep_daily:   7,
    retention_keep_weekly:  4,
    retention_keep_monthly: 6,
    retention_keep_yearly:  1,
    ordered_destination_ids: [],
    hook_pre:  { enabled: false, name: '', command: '', args: '', timeout_secs: 30 },
    hook_post: { enabled: false, name: '', command: '', args: '', timeout_secs: 30 },
  }
}

watch(() => props.open, async (open) => {
  if (!open) return

  selectedPreset.value = ''
  hooksOpen.value      = false
  showPassword.value   = false

  await loadRemoteData()

  if (props.policy) {
    const p = props.policy

    // Build ordered destination IDs sorted by priority.
    const preDestIds = (p.destinations ?? [])
      .sort((a, b) => a.priority - b.priority)
      .map(d => d.destination_id)

    // Map Policy.sources (Source[]) to form items.
    // SourceType uses "docker-volume" (hyphen) so we pass it through directly.
    const mappedSources = (p.sources ?? []).map(s => ({
      type:  s.type as SourceTypeValue,
      path:  s.path  ?? '',
      label: (s as any).label as string | undefined ?? '',
    }))

    // hooks_pre_backup / hook_post_backup are not present on the list endpoint
    // payload — on edit we leave the hook fields disabled/blank.
    // The backend keeps the existing hook if hook_pre_backup is omitted from PATCH.

    setValues({
      name:                    p.name,
      agent_id:                p.agent_id,
      enabled:                 p.enabled,
      repo_password:           '',
      repo_password_confirm:   '',
      schedule:                p.schedule,
      sources:                 mappedSources.length > 0
        ? mappedSources
        : [{ type: 'directory', path: '', label: '' }],
      retention_keep_daily:   p.retention.keep_daily,
      retention_keep_weekly:  p.retention.keep_weekly,
      retention_keep_monthly: p.retention.keep_monthly,
      retention_keep_yearly:  p.retention.keep_yearly,
      ordered_destination_ids: preDestIds,
      hook_pre:  { enabled: false, name: '', command: '', args: '', timeout_secs: 30 },
      hook_post: { enabled: false, name: '', command: '', args: '', timeout_secs: 30 },
    } as unknown as FormValues)

    const match = SCHEDULE_PRESETS.find(s => s.value === p.schedule)
    selectedPreset.value = match?.value ?? ''
  } else {
    resetForm({ values: defaultValues() })
    selectedPreset.value = '0 2 * * *'
  }
})

// ---------------------------------------------------------------------------
// Submit
// ---------------------------------------------------------------------------

const submitting  = ref(false)
const submitError = ref<string | null>(null)

/**
 * Converts a hook form section to a JSON string for the API.
 * Returns an empty string when the hook is disabled or has no command.
 */
function serialiseHook(hook: z.infer<typeof hookFieldSchema>): string {
  if (!hook.enabled || !hook.command) return ''
  return JSON.stringify({
    name:         hook.name ?? '',
    command:      hook.command,
    args:         hook.args ? hook.args.split(/\s+/).filter(Boolean) : [],
    timeout_secs: hook.timeout_secs ?? 30,
  })
}

const onSubmit = handleSubmit(async (values) => {
  submitting.value  = true
  submitError.value = null

  try {
    const destinationsPayload = values.ordered_destination_ids.map((id, idx) => ({
      destination_id: id,
      priority:       idx + 1,
    }))

    // Sources sent as a JSON string — the backend stores them that way.
    const sourcesJson = JSON.stringify(values.sources)

    const body: Record<string, unknown> = {
      name:              values.name,
      agent_id:          values.agent_id,
      enabled:           values.enabled,
      schedule:          values.schedule,
      sources:           sourcesJson,
      retention_daily:   values.retention_keep_daily,
      retention_weekly:  values.retention_keep_weekly,
      retention_monthly: values.retention_keep_monthly,
      retention_yearly:  values.retention_keep_yearly,
      hook_pre_backup:   serialiseHook(values.hook_pre),
      hook_post_backup:  serialiseHook(values.hook_post),
      destinations:      destinationsPayload,
    }

    if (!isEdit.value) {
      body.repo_password = values.repo_password ?? ''
    } else if (values.repo_password) {
      // Only send on edit if the user typed a new password.
      body.repo_password = values.repo_password
    }

    if (isEdit.value && props.policy) {
      await api(`/api/v1/policies/${props.policy.id}`, { method: 'PATCH', body })
    } else {
      await api('/api/v1/policies', { method: 'POST', body })
    }

    emit('saved')
  } catch (e: any) {
    submitError.value = e?.data?.message ?? 'An unexpected error occurred.'
  } finally {
    submitting.value = false
  }
})

// ---------------------------------------------------------------------------
// Sheet close handler
// ---------------------------------------------------------------------------

function onOpenChange(value: boolean) {
  if (submitting.value) return
  emit('update:open', value)
}
</script>

<template>
  <Sheet :open="props.open" @update:open="onOpenChange">
    <SheetContent class="sm:max-w-2xl overflow-y-auto">
      <SheetHeader class="px-4 pt-6">
        <SheetTitle>{{ isEdit ? 'Edit Policy' : 'New Policy' }}</SheetTitle>
        <SheetDescription>
          {{ isEdit
            ? 'Update the backup policy configuration.'
            : 'Configure a new backup policy — what to back up, when, and where.' }}
        </SheetDescription>
      </SheetHeader>

      <form class="py-4 px-4" novalidate @submit.prevent="onSubmit">
        <FieldGroup class="flex flex-col gap-6">

          <!-- Error banner -->
          <Transition
            enter-active-class="transition-all duration-200 ease-out"
            enter-from-class="opacity-0 -translate-y-1"
            leave-active-class="transition-all duration-150 ease-in"
            leave-to-class="opacity-0 -translate-y-1"
          >
            <Alert v-if="submitError" variant="destructive">
              <AlertCircle class="size-4" />
              <AlertDescription>{{ submitError }}</AlertDescription>
            </Alert>
          </Transition>

          <!-- ══════════════════════════════════════════════════════════════
               1. GENERAL
          ══════════════════════════════════════════════════════════════ -->
          <div class="flex flex-col gap-4">
            <h3 class="text-sm font-semibold">General</h3>

            <Field>
              <FieldLabel for="name">Name <span class="text-destructive">*</span></FieldLabel>
              <Input
                id="name"
                v-model="nameValue"
                placeholder="e.g. Daily Database Backup"
                :class="nameError ? 'border-destructive focus-visible:ring-destructive/30' : ''"
              />
              <FieldError v-if="nameError">{{ nameError }}</FieldError>
            </Field>

            <Field>
              <FieldLabel for="agent">Agent <span class="text-destructive">*</span></FieldLabel>
              <Select
                :model-value="agentValue ?? ''"
                @update:model-value="agentValue = $event as string"
              >
                <SelectTrigger
                  id="agent"
                  :disabled="loadingData"
                  :class="agentError ? 'border-destructive focus-visible:ring-destructive/30' : ''"
                >
                  <SelectValue placeholder="Select an agent…" />
                </SelectTrigger>
                <SelectContent>
                  <SelectItem v-for="agent in agents" :key="agent.id" :value="agent.id">
                    {{ agent.name }}
                  </SelectItem>
                  <div
                    v-if="agents.length === 0"
                    class="px-3 py-4 text-sm text-muted-foreground text-center"
                  >
                    No agents available
                  </div>
                </SelectContent>
              </Select>
              <FieldError v-if="agentError">{{ agentError }}</FieldError>
            </Field>

            <div class="flex items-center justify-between rounded-lg border px-3 py-2.5">
              <div>
                <Label for="enabled" class="text-sm font-medium">Enabled</Label>
                <p class="text-xs text-muted-foreground mt-0.5">
                  When disabled, scheduled runs are paused.
                </p>
              </div>
              <Switch
                id="enabled"
                :checked="enabledValue ?? true"
                @update:checked="enabledValue = $event"
              />
            </div>
          </div>

          <Separator />

          <!-- ══════════════════════════════════════════════════════════════
               2. REPOSITORY PASSWORD
          ══════════════════════════════════════════════════════════════ -->
          <div class="flex flex-col gap-4">
            <div>
              <h3 class="text-sm font-semibold">Repository Password</h3>
              <p class="mt-0.5 text-xs text-muted-foreground">
                {{ isEdit
                  ? 'Leave blank to keep the existing password. Restic uses this to encrypt the repository.'
                  : 'Required. Restic uses this to encrypt the repository. Store it safely — it cannot be recovered.' }}
              </p>
            </div>

            <Field>
              <FieldLabel for="repo_password">
                Password <span v-if="!isEdit" class="text-destructive">*</span>
              </FieldLabel>
              <div class="relative">
                <Input
                  id="repo_password"
                  v-model="repoPassValue"
                  :type="showPassword ? 'text' : 'password'"
                  autocomplete="new-password"
                  placeholder="min. 8 characters"
                  :class="['pr-10', repoPassError ? 'border-destructive focus-visible:ring-destructive/30' : '']"
                />
                <button
                  type="button"
                  class="absolute right-3 top-1/2 -translate-y-1/2 text-muted-foreground hover:text-foreground transition-colors"
                  @click="showPassword = !showPassword"
                >
                  <EyeOff v-if="showPassword" class="size-4" />
                  <Eye v-else class="size-4" />
                </button>
              </div>
              <FieldError v-if="repoPassError">{{ repoPassError }}</FieldError>
            </Field>

            <Field>
              <FieldLabel for="repo_password_confirm">
                Confirm Password <span v-if="!isEdit" class="text-destructive">*</span>
              </FieldLabel>
              <Input
                id="repo_password_confirm"
                v-model="repoPassConfirmValue"
                :type="showPassword ? 'text' : 'password'"
                autocomplete="new-password"
                placeholder="repeat password"
                :class="repoPassConfirmError ? 'border-destructive focus-visible:ring-destructive/30' : ''"
              />
              <FieldError v-if="repoPassConfirmError">{{ repoPassConfirmError }}</FieldError>
            </Field>
          </div>

          <Separator />

          <!-- ══════════════════════════════════════════════════════════════
               3. SOURCES
          ══════════════════════════════════════════════════════════════ -->
          <div class="flex flex-col gap-3">
            <div class="flex items-center justify-between">
              <div>
                <h3 class="text-sm font-semibold">Sources</h3>
                <p class="mt-0.5 text-xs text-muted-foreground">
                  Directories or Docker volumes to include in the backup.
                </p>
              </div>
              <Button type="button" variant="outline" size="sm" @click="addSource">
                <Plus class="w-3.5 h-3.5 mr-1.5" />
                Add Source
              </Button>
            </div>

            <div
              v-if="sourceFields.length === 0"
              class="rounded-md border border-dashed px-4 py-6 text-center text-sm text-muted-foreground"
            >
              No sources added. Click <strong>Add Source</strong> to begin.
            </div>

            <div
              v-for="(field, idx) in sourceFields"
              :key="field.key"
              class="flex flex-col gap-2 rounded-md border p-3"
            >
              <div class="flex items-center justify-between">
                <span class="text-xs font-medium text-muted-foreground">Source {{ idx + 1 }}</span>
                <Button
                  type="button"
                  variant="ghost"
                  size="icon"
                  class="w-6 h-6 text-muted-foreground hover:text-destructive"
                  @click="removeSource(idx)"
                >
                  <Trash2 class="w-3.5 h-3.5" />
                </Button>
              </div>

              <div class="grid grid-cols-2 gap-2">
                <!-- Type -->
                <div class="flex flex-col gap-1">
                  <Label :for="`source-type-${idx}`" class="text-xs">Type</Label>
                  <Select
                    :model-value="(field.value as any).type as string"
                    @update:model-value="(field.value as any).type = $event as SourceTypeValue"
                  >
                    <SelectTrigger :id="`source-type-${idx}`" class="h-8 text-sm">
                      <SelectValue />
                    </SelectTrigger>
                    <SelectContent>
                      <SelectItem value="directory">Directory</SelectItem>
                      <SelectItem value="docker-volume">Docker Volume</SelectItem>
                    </SelectContent>
                  </Select>
                </div>

                <!-- Label (optional) -->
                <div class="flex flex-col gap-1">
                  <Label :for="`source-label-${idx}`" class="text-xs">
                    Label <span class="text-muted-foreground">(optional)</span>
                  </Label>
                  <Input
                    :id="`source-label-${idx}`"
                    :model-value="(field.value as any).label as string"
                    class="h-8 text-sm"
                    placeholder="e.g. postgres-data"
                    @update:model-value="(field.value as any).label = $event"
                  />
                </div>
              </div>

              <!-- Path / Volume name -->
              <div class="flex flex-col gap-1">
                <Label :for="`source-path-${idx}`" class="text-xs">
                  {{ (field.value as any).type === 'docker-volume' ? 'Volume Name' : 'Path' }}
                </Label>
                <Input
                  :id="`source-path-${idx}`"
                  :model-value="(field.value as any).path as string"
                  class="h-8 text-sm font-mono"
                  :placeholder="(field.value as any).type === 'docker-volume'
                    ? 'e.g. postgres_data'
                    : 'e.g. /var/lib/data'"
                  @update:model-value="(field.value as any).path = $event"
                />
              </div>
            </div>
          </div>

          <Separator />

          <!-- ══════════════════════════════════════════════════════════════
               4. SCHEDULE
          ══════════════════════════════════════════════════════════════ -->
          <div class="flex flex-col gap-4">
            <div>
              <h3 class="text-sm font-semibold">Schedule</h3>
              <p class="mt-0.5 text-xs text-muted-foreground">
                Cron expression defining when the backup runs (UTC).
              </p>
            </div>

            <div class="flex flex-wrap gap-2">
              <button
                v-for="preset in SCHEDULE_PRESETS"
                :key="preset.value"
                type="button"
                class="text-xs px-2.5 py-1 rounded-full border transition-colors"
                :class="selectedPreset === preset.value
                  ? 'bg-primary text-primary-foreground border-primary'
                  : 'border-border text-muted-foreground hover:border-foreground hover:text-foreground'"
                @click="applyPreset(preset.value)"
              >
                {{ preset.label }}
              </button>
            </div>

            <Field>
              <FieldLabel for="schedule">
                Cron Expression <span class="text-destructive">*</span>
              </FieldLabel>
              <Input
                id="schedule"
                v-model="scheduleValue"
                class="font-mono"
                placeholder="0 2 * * *"
                :class="scheduleError ? 'border-destructive focus-visible:ring-destructive/30' : ''"
                @input="selectedPreset = ''"
              />
              <p class="text-xs text-muted-foreground mt-1">
                Format: <code class="font-mono">minute hour day month weekday</code>
              </p>
              <FieldError v-if="scheduleError">{{ scheduleError }}</FieldError>
            </Field>
          </div>

          <Separator />

          <!-- ══════════════════════════════════════════════════════════════
               5. RETENTION
          ══════════════════════════════════════════════════════════════ -->
          <div class="flex flex-col gap-4">
            <div>
              <h3 class="text-sm font-semibold">Retention</h3>
              <p class="mt-0.5 text-xs text-muted-foreground">
                How many snapshots to keep per period.
                <code class="font-mono">0</code> uses the server default (7 / 4 / 6 / 1).
              </p>
            </div>

            <div class="grid grid-cols-2 gap-3 sm:grid-cols-4">
              <Field>
                <FieldLabel for="ret-daily">Daily</FieldLabel>
                <Input
                  id="ret-daily"
                  v-model="retDailyValue"
                  type="number"
                  min="0"
                  :class="retDailyError ? 'border-destructive focus-visible:ring-destructive/30' : ''"
                />
                <FieldError v-if="retDailyError">{{ retDailyError }}</FieldError>
              </Field>

              <Field>
                <FieldLabel for="ret-weekly">Weekly</FieldLabel>
                <Input
                  id="ret-weekly"
                  v-model="retWeeklyValue"
                  type="number"
                  min="0"
                  :class="retWeeklyError ? 'border-destructive focus-visible:ring-destructive/30' : ''"
                />
                <FieldError v-if="retWeeklyError">{{ retWeeklyError }}</FieldError>
              </Field>

              <Field>
                <FieldLabel for="ret-monthly">Monthly</FieldLabel>
                <Input
                  id="ret-monthly"
                  v-model="retMonthlyValue"
                  type="number"
                  min="0"
                  :class="retMonthlyError ? 'border-destructive focus-visible:ring-destructive/30' : ''"
                />
                <FieldError v-if="retMonthlyError">{{ retMonthlyError }}</FieldError>
              </Field>

              <Field>
                <FieldLabel for="ret-yearly">Yearly</FieldLabel>
                <Input
                  id="ret-yearly"
                  v-model="retYearlyValue"
                  type="number"
                  min="0"
                  :class="retYearlyError ? 'border-destructive focus-visible:ring-destructive/30' : ''"
                />
                <FieldError v-if="retYearlyError">{{ retYearlyError }}</FieldError>
              </Field>
            </div>
          </div>

          <Separator />

          <!-- ══════════════════════════════════════════════════════════════
               6. DESTINATIONS
          ══════════════════════════════════════════════════════════════ -->
          <div class="flex flex-col gap-3">
            <div>
              <h3 class="text-sm font-semibold">Destinations</h3>
              <p class="mt-0.5 text-xs text-muted-foreground">
                Select where to store backups. Use ↑↓ to set the priority order.
              </p>
            </div>

            <div
              v-if="availableDestinations.length === 0"
              class="rounded-md border border-dashed px-4 py-6 text-center text-sm text-muted-foreground"
            >
              No destinations available. Create one first.
            </div>

            <div v-else class="flex flex-col gap-1">
              <div
                v-for="dest in availableDestinations"
                :key="dest.id"
                class="flex items-center justify-between rounded-md border px-3 py-2 cursor-pointer transition-colors"
                :class="isDestSelected(dest.id)
                  ? 'border-primary/50 bg-primary/5'
                  : 'hover:bg-muted/50'"
                @click="toggleDest(dest.id)"
              >
                <div class="flex items-center gap-2.5">
                  <div
                    class="w-4 h-4 rounded border flex items-center justify-center shrink-0 transition-colors"
                    :class="isDestSelected(dest.id)
                      ? 'bg-primary border-primary'
                      : 'border-muted-foreground/40'"
                  >
                    <svg
                      v-if="isDestSelected(dest.id)"
                      class="w-2.5 h-2.5 text-primary-foreground"
                      viewBox="0 0 10 10"
                      fill="none"
                    >
                      <path
                        d="M1.5 5l2.5 2.5 4.5-4.5"
                        stroke="currentColor"
                        stroke-width="1.5"
                        stroke-linecap="round"
                        stroke-linejoin="round"
                      />
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
            <div
              v-if="orderedDestIds && orderedDestIds.length > 0"
              class="flex flex-col gap-1 mt-1"
            >
              <p class="text-xs font-medium text-muted-foreground mb-1">Priority Order</p>
              <div
                v-for="(id, idx) in orderedDestIds"
                :key="id"
                class="flex items-center gap-2 rounded-md bg-muted/50 px-3 py-1.5"
              >
                <span class="text-xs font-mono text-muted-foreground w-5 shrink-0">{{ idx + 1 }}.</span>
                <span class="text-sm flex-1">{{ destByIdName(id) }}</span>
                <div class="flex gap-0.5">
                  <Button
                    type="button"
                    variant="ghost"
                    size="icon"
                    class="w-6 h-6"
                    :disabled="idx === 0"
                    @click.stop="moveDestUp(idx)"
                  >
                    <ChevronUp class="w-3.5 h-3.5" />
                  </Button>
                  <Button
                    type="button"
                    variant="ghost"
                    size="icon"
                    class="w-6 h-6"
                    :disabled="idx === orderedDestIds.length - 1"
                    @click.stop="moveDestDown(idx)"
                  >
                    <ChevronDown class="w-3.5 h-3.5" />
                  </Button>
                </div>
              </div>
            </div>
          </div>

          <Separator />

          <!-- ══════════════════════════════════════════════════════════════
               7. HOOKS (collapsible)
          ══════════════════════════════════════════════════════════════ -->
          <Collapsible v-model:open="hooksOpen">
            <CollapsibleTrigger as-child>
              <button
                type="button"
                class="flex w-full items-center justify-between text-sm font-semibold hover:text-foreground/80 transition-colors"
              >
                <span>
                  Hooks
                  <span class="text-xs font-normal text-muted-foreground ml-1">(optional)</span>
                </span>
                <ChevronRight
                  class="w-4 h-4 text-muted-foreground transition-transform duration-200"
                  :class="hooksOpen ? 'rotate-90' : ''"
                />
              </button>
            </CollapsibleTrigger>

            <CollapsibleContent>
              <div class="flex flex-col gap-4 mt-4">

                <!-- Pre-backup hook -->
                <div class="rounded-md border p-3 flex flex-col gap-3">
                  <div class="flex items-center justify-between">
                    <Label class="text-sm font-medium">Pre-backup Hook</Label>
                    <Switch
                      :checked="hookPreEnabled ?? false"
                      @update:checked="hookPreEnabled = $event"
                    />
                  </div>
                  <template v-if="hookPreEnabled">
                    <div class="grid grid-cols-2 gap-2">
                      <div class="flex flex-col gap-1">
                        <Label for="hook-pre-name" class="text-xs">Name</Label>
                        <Input
                          id="hook-pre-name"
                          v-model="hookPreName"
                          class="h-8 text-sm"
                          placeholder="e.g. stop-container"
                        />
                      </div>
                      <div class="flex flex-col gap-1">
                        <Label for="hook-pre-timeout" class="text-xs">Timeout (sec)</Label>
                        <Input
                          id="hook-pre-timeout"
                          v-model="hookPreTimeout"
                          type="number"
                          min="0"
                          class="h-8 text-sm"
                        />
                      </div>
                    </div>
                    <div class="flex flex-col gap-1">
                      <Label for="hook-pre-cmd" class="text-xs">Command</Label>
                      <Input
                        id="hook-pre-cmd"
                        v-model="hookPreCommand"
                        class="h-8 text-sm font-mono"
                        placeholder="e.g. docker"
                      />
                    </div>
                    <div class="flex flex-col gap-1">
                      <Label for="hook-pre-args" class="text-xs">
                        Arguments <span class="text-muted-foreground">(space-separated)</span>
                      </Label>
                      <Input
                        id="hook-pre-args"
                        v-model="hookPreArgs"
                        class="h-8 text-sm font-mono"
                        placeholder="e.g. stop my-container"
                      />
                    </div>
                  </template>
                </div>

                <!-- Post-backup hook -->
                <div class="rounded-md border p-3 flex flex-col gap-3">
                  <div class="flex items-center justify-between">
                    <Label class="text-sm font-medium">Post-backup Hook</Label>
                    <Switch
                      :checked="hookPostEnabled ?? false"
                      @update:checked="hookPostEnabled = $event"
                    />
                  </div>
                  <template v-if="hookPostEnabled">
                    <div class="grid grid-cols-2 gap-2">
                      <div class="flex flex-col gap-1">
                        <Label for="hook-post-name" class="text-xs">Name</Label>
                        <Input
                          id="hook-post-name"
                          v-model="hookPostName"
                          class="h-8 text-sm"
                          placeholder="e.g. start-container"
                        />
                      </div>
                      <div class="flex flex-col gap-1">
                        <Label for="hook-post-timeout" class="text-xs">Timeout (sec)</Label>
                        <Input
                          id="hook-post-timeout"
                          v-model="hookPostTimeout"
                          type="number"
                          min="0"
                          class="h-8 text-sm"
                        />
                      </div>
                    </div>
                    <div class="flex flex-col gap-1">
                      <Label for="hook-post-cmd" class="text-xs">Command</Label>
                      <Input
                        id="hook-post-cmd"
                        v-model="hookPostCommand"
                        class="h-8 text-sm font-mono"
                        placeholder="e.g. docker"
                      />
                    </div>
                    <div class="flex flex-col gap-1">
                      <Label for="hook-post-args" class="text-xs">
                        Arguments <span class="text-muted-foreground">(space-separated)</span>
                      </Label>
                      <Input
                        id="hook-post-args"
                        v-model="hookPostArgs"
                        class="h-8 text-sm font-mono"
                        placeholder="e.g. start my-container"
                      />
                    </div>
                  </template>
                </div>

              </div>
            </CollapsibleContent>
          </Collapsible>

          <!-- Footer -->
          <SheetFooter class="mt-2 px-0 pt-2 border-t">
            <Button
              type="button"
              variant="outline"
              :disabled="submitting"
              @click="onOpenChange(false)"
            >
              Cancel
            </Button>
            <Button type="submit" :disabled="submitting || loadingData">
              <Loader2 v-if="submitting" class="size-4 animate-spin mr-2" />
              {{ submitting ? 'Saving…' : (isEdit ? 'Save Changes' : 'Create Policy') }}
            </Button>
          </SheetFooter>

        </FieldGroup>
      </form>
    </SheetContent>
  </Sheet>
</template>