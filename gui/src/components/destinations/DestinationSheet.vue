<script setup lang="ts">
import { ref, watch, computed } from 'vue'
import { z } from 'zod'
import { api } from '@/services/api'
import type { Destination } from '@/types'
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
import {
    Select,
    SelectContent,
    SelectItem,
    SelectTrigger,
    SelectValue,
} from '@/components/ui/select'
import { Switch } from '@/components/ui/switch'
import { Separator } from '@/components/ui/separator'
import { AlertCircle, Loader2 } from 'lucide-vue-next'

// ---------------------------------------------------------------------------
// Props / emits
// ---------------------------------------------------------------------------

const props = defineProps<{
    open: boolean
    destination: Destination | null
}>()

const emit = defineEmits<{
    'update:open': [value: boolean]
    saved: []
}>()

const isEdit = computed(() => !!props.destination)

// ---------------------------------------------------------------------------
// Destination types
// ---------------------------------------------------------------------------

type DestType = 'local' | 's3' | 'sftp' | 'rest' | 'rclone'

const DEST_TYPES: { value: DestType; label: string }[] = [
    { value: 'local', label: 'Local Path' },
    { value: 's3', label: 'S3 / Object Storage' },
    { value: 'sftp', label: 'SFTP' },
    { value: 'rest', label: 'REST Server' },
    { value: 'rclone', label: 'Rclone' },
]

// ---------------------------------------------------------------------------
// Per-type Zod schemas
// ---------------------------------------------------------------------------

// Config schemas (stored unencrypted)
const configSchemas: Record<DestType, z.ZodObject<any>> = {
    local: z.object({
        path: z.string().min(1, 'Path is required'),
    }),
    s3: z.object({
        bucket: z.string().min(1, 'Bucket is required'),
        endpoint: z.string().optional(),
        region: z.string().optional(),
        prefix: z.string().optional(),
    }),
    sftp: z.object({
        host: z.string().min(1, 'Host is required'),
        port: z.string().optional(),
        user: z.string().min(1, 'Username is required'),
        path: z.string().min(1, 'Path is required'),
    }),
    rest: z.object({
        url: z.string().url('Must be a valid URL'),
    }),
    rclone: z.object({
        remote: z.string().min(1, 'Remote name is required'),
        path: z.string().min(1, 'Path is required'),
    }),
}

// Credentials schemas (stored encrypted)
const credSchemas: Record<DestType, z.ZodObject<any>> = {
    local: z.object({}),
    s3: z.object({
        access_key: z.string().min(1, 'Access key is required'),
        secret_key: z.string().min(1, 'Secret key is required'),
    }),
    sftp: z.object({
        password: z.string().optional(),
        private_key: z.string().optional(),
    }),
    rest: z.object({
        user: z.string().optional(),
        password: z.string().optional(),
    }),
    rclone: z.object({}),
}

// ---------------------------------------------------------------------------
// Form state
// ---------------------------------------------------------------------------

const selectedType = ref<DestType>('local')
const submitError = ref<string | null>(null)
const submitting = ref(false)

// Base fields
const name = ref('')
const nameError = ref('')
const enabled = ref(true)

// Config fields — one ref per possible field
const localPath = ref('')
const s3Bucket = ref('')
const s3Endpoint = ref('')
const s3Region = ref('')
const s3Prefix = ref('')
const s3AccessKey = ref('')
const s3SecretKey = ref('')
const sftpHost = ref('')
const sftpPort = ref('')
const sftpUser = ref('')
const sftpPath = ref('')
const sftpPassword = ref('')
const sftpPrivateKey = ref('')
const restUrl = ref('')
const restUser = ref('')
const restPassword = ref('')
const rcloneRemote = ref('')
const rclonePath = ref('')

// Per-field errors — simple approach, no vee-validate on dynamic fields
const fieldErrors = ref<Record<string, string>>({})

// ---------------------------------------------------------------------------
// Reset / populate
// ---------------------------------------------------------------------------

function resetFields() {
    name.value = ''
    nameError.value = ''
    enabled.value = true
    submitError.value = null
    fieldErrors.value = {}
    localPath.value = ''
    s3Bucket.value = s3Endpoint.value = s3Region.value = s3Prefix.value = ''
    s3AccessKey.value = s3SecretKey.value = ''
    sftpHost.value = sftpPort.value = sftpUser.value = sftpPath.value = ''
    sftpPassword.value = sftpPrivateKey.value = ''
    restUrl.value = restUser.value = restPassword.value = ''
    rcloneRemote.value = rclonePath.value = ''
}

function populateFromDestination(dest: Destination) {
    selectedType.value = dest.type as DestType
    name.value = dest.name
    enabled.value = dest.enabled

    // Parse config JSON — credentials are write-only and never populated
    let config: Record<string, string> = {}
    try { config = JSON.parse(dest.config || '{}') } catch { /* ignore */ }

    switch (dest.type as DestType) {
        case 'local':
            localPath.value = config.path ?? ''
            break
        case 's3':
            s3Bucket.value = config.bucket ?? ''
            s3Endpoint.value = config.endpoint ?? ''
            s3Region.value = config.region ?? ''
            s3Prefix.value = config.prefix ?? ''
            break
        case 'sftp':
            sftpHost.value = config.host ?? ''
            sftpPort.value = config.port ?? ''
            sftpUser.value = config.user ?? ''
            sftpPath.value = config.path ?? ''
            break
        case 'rest':
            restUrl.value = config.url ?? ''
            break
        case 'rclone':
            rcloneRemote.value = config.remote ?? ''
            rclonePath.value = config.path ?? ''
            break
    }
}

// Prefill or reset when the sheet opens
watch(
    () => props.open,
    (open) => {
        if (open) {
            resetFields()
            if (props.destination) {
                populateFromDestination(props.destination)
            } else {
                selectedType.value = 'local'
            }
        }
    },
    { immediate: true }
)

// Clear field errors when type changes
watch(selectedType, () => {
    fieldErrors.value = {}
    submitError.value = null
})

// ---------------------------------------------------------------------------
// Build payload
// ---------------------------------------------------------------------------

function buildConfigAndCreds(): { config: Record<string, string>; creds: Record<string, string> } {
    switch (selectedType.value) {
        case 'local':
            return { config: { path: localPath.value }, creds: {} }
        case 's3':
            return {
                config: {
                    bucket: s3Bucket.value,
                    endpoint: s3Endpoint.value,
                    region: s3Region.value,
                    prefix: s3Prefix.value,
                },
                creds: { access_key: s3AccessKey.value, secret_key: s3SecretKey.value },
            }
        case 'sftp':
            return {
                config: { host: sftpHost.value, port: sftpPort.value, user: sftpUser.value, path: sftpPath.value },
                creds: { password: sftpPassword.value, private_key: sftpPrivateKey.value },
            }
        case 'rest':
            return {
                config: { url: restUrl.value },
                creds: { user: restUser.value, password: restPassword.value },
            }
        case 'rclone':
            return { config: { remote: rcloneRemote.value, path: rclonePath.value }, creds: {} }
    }
}

// ---------------------------------------------------------------------------
// Validate
// ---------------------------------------------------------------------------

function validate(): boolean {
    fieldErrors.value = {}
    nameError.value = ''
    let valid = true

    if (!name.value.trim()) {
        nameError.value = 'Name is required'
        valid = false
    }

    const { config, creds } = buildConfigAndCreds()
    const configResult = configSchemas[selectedType.value].safeParse(config)
    const credsResult = credSchemas[selectedType.value].safeParse(creds)

    if (!configResult.success) {
        for (const issue of configResult.error.issues) {
            fieldErrors.value[String(issue.path[0])] = issue.message
        }
        valid = false
    }
    if (!credsResult.success) {
        for (const issue of credsResult.error.issues) {
            fieldErrors.value[`cred_${String(issue.path[0])}`] = issue.message
        }
        valid = false
    }

    return valid
}

// ---------------------------------------------------------------------------
// Submit
// ---------------------------------------------------------------------------

async function onSubmit() {
    if (!validate()) return

    submitting.value = true
    submitError.value = null

    const { config, creds } = buildConfigAndCreds()

    try {
        if (isEdit.value && props.destination) {
            // PATCH — credentials always sent because they are write-only
            await api(`/api/v1/destinations/${props.destination.id}`, {
                method: 'PATCH',
                body: {
                    name: name.value,
                    config: JSON.stringify(config),
                    credentials: JSON.stringify(creds),
                    enabled: enabled.value,
                },
            })
        } else {
            await api('/api/v1/destinations', {
                method: 'POST',
                body: {
                    name: name.value,
                    type: selectedType.value,
                    config: JSON.stringify(config),
                    credentials: JSON.stringify(creds),
                },
            })
        }
        emit('update:open', false)
        emit('saved')
    } catch (e: any) {
        submitError.value = e?.data?.error?.message ?? e?.message ?? 'Failed to save destination'
    } finally {
        submitting.value = false
    }
}

function onOpenChange(value: boolean) {
    if (!value) {
        resetFields()
    }
    emit('update:open', value)
}
</script>

<template>
    <Sheet :open="props.open" @update:open="onOpenChange">
        <SheetContent class="sm:max-w-lg overflow-y-auto">
            <SheetHeader>
                <SheetTitle>{{ isEdit ? 'Edit Destination' : 'New Destination' }}</SheetTitle>
                <SheetDescription>
                    {{ isEdit ? 'Update the destination settings.' : 'Configure a new backup storage target.' }}
                </SheetDescription>
            </SheetHeader>

            <form class="py-6 px-4" novalidate @submit.prevent="onSubmit">
                <FieldGroup>

                    <!-- Error banner -->
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
                        <FieldLabel for="dest-name">Name</FieldLabel>
                        <Input id="dest-name" v-model="name" placeholder="e.g. Primary S3 Bucket" autocomplete="off"
                            :class="nameError ? 'border-destructive focus-visible:ring-destructive/30' : ''" />
                        <FieldError v-if="nameError">{{ nameError }}</FieldError>
                    </Field>

                    <!-- Type — disabled in edit mode -->
                    <Field>
                        <FieldLabel>Type</FieldLabel>
                        <Select :model-value="selectedType" :disabled="isEdit"
                            @update:model-value="selectedType = $event as DestType">
                            <SelectTrigger>
                                <SelectValue placeholder="Select type" />
                            </SelectTrigger>
                            <SelectContent>
                                <SelectItem v-for="t in DEST_TYPES" :key="t.value" :value="t.value">
                                    {{ t.label }}
                                </SelectItem>
                            </SelectContent>
                        </Select>
                        <p v-if="isEdit" class="text-muted-foreground text-xs">
                            Type cannot be changed after creation.
                        </p>
                    </Field>

                    <Separator />

                    <!-- ── Local ── -->
                    <template v-if="selectedType === 'local'">
                        <Field>
                            <FieldLabel for="local-path">Path</FieldLabel>
                            <Input id="local-path" v-model="localPath" placeholder="/mnt/backups"
                                :class="fieldErrors.path ? 'border-destructive focus-visible:ring-destructive/30' : ''" />
                            <FieldError v-if="fieldErrors.path">{{ fieldErrors.path }}</FieldError>
                        </Field>
                    </template>

                    <!-- ── S3 ── -->
                    <template v-if="selectedType === 's3'">
                        <Field>
                            <FieldLabel for="s3-bucket">Bucket</FieldLabel>
                            <Input id="s3-bucket" v-model="s3Bucket" placeholder="my-backup-bucket"
                                :class="fieldErrors.bucket ? 'border-destructive focus-visible:ring-destructive/30' : ''" />
                            <FieldError v-if="fieldErrors.bucket">{{ fieldErrors.bucket }}</FieldError>
                        </Field>
                        <Field>
                            <FieldLabel for="s3-endpoint">
                                Endpoint <span class="text-muted-foreground font-normal">(optional)</span>
                            </FieldLabel>
                            <Input id="s3-endpoint" v-model="s3Endpoint"
                                placeholder="https://s3.us-east-1.amazonaws.com" />
                        </Field>
                        <div class="grid grid-cols-2 gap-3">
                            <Field>
                                <FieldLabel for="s3-region">
                                    Region <span class="text-muted-foreground font-normal">(optional)</span>
                                </FieldLabel>
                                <Input id="s3-region" v-model="s3Region" placeholder="us-east-1" />
                            </Field>
                            <Field>
                                <FieldLabel for="s3-prefix">
                                    Prefix <span class="text-muted-foreground font-normal">(optional)</span>
                                </FieldLabel>
                                <Input id="s3-prefix" v-model="s3Prefix" placeholder="backups/" />
                            </Field>
                        </div>

                        <Separator />
                        <p class="text-sm font-medium">Credentials</p>

                        <Field>
                            <FieldLabel for="s3-access-key">Access Key ID</FieldLabel>
                            <Input id="s3-access-key" v-model="s3AccessKey" autocomplete="off"
                                :class="fieldErrors.cred_access_key ? 'border-destructive focus-visible:ring-destructive/30' : ''" />
                            <FieldError v-if="fieldErrors.cred_access_key">{{ fieldErrors.cred_access_key }}
                            </FieldError>
                        </Field>
                        <Field>
                            <FieldLabel for="s3-secret-key">Secret Access Key</FieldLabel>
                            <Input id="s3-secret-key" v-model="s3SecretKey" type="password" autocomplete="off"
                                :class="fieldErrors.cred_secret_key ? 'border-destructive focus-visible:ring-destructive/30' : ''" />
                            <FieldError v-if="fieldErrors.cred_secret_key">{{ fieldErrors.cred_secret_key }}
                            </FieldError>
                        </Field>
                    </template>

                    <!-- ── SFTP ── -->
                    <template v-if="selectedType === 'sftp'">
                        <div class="grid grid-cols-3 gap-3">
                            <Field class="col-span-2">
                                <FieldLabel for="sftp-host">Host</FieldLabel>
                                <Input id="sftp-host" v-model="sftpHost" placeholder="backup.example.com"
                                    :class="fieldErrors.host ? 'border-destructive focus-visible:ring-destructive/30' : ''" />
                                <FieldError v-if="fieldErrors.host">{{ fieldErrors.host }}</FieldError>
                            </Field>
                            <Field>
                                <FieldLabel for="sftp-port">
                                    Port <span class="text-muted-foreground font-normal">(opt.)</span>
                                </FieldLabel>
                                <Input id="sftp-port" v-model="sftpPort" placeholder="22" />
                            </Field>
                        </div>
                        <Field>
                            <FieldLabel for="sftp-user">Username</FieldLabel>
                            <Input id="sftp-user" v-model="sftpUser" placeholder="backup-user"
                                :class="fieldErrors.user ? 'border-destructive focus-visible:ring-destructive/30' : ''" />
                            <FieldError v-if="fieldErrors.user">{{ fieldErrors.user }}</FieldError>
                        </Field>
                        <Field>
                            <FieldLabel for="sftp-path">Remote Path</FieldLabel>
                            <Input id="sftp-path" v-model="sftpPath" placeholder="/home/backup-user/restic"
                                :class="fieldErrors.path ? 'border-destructive focus-visible:ring-destructive/30' : ''" />
                            <FieldError v-if="fieldErrors.path">{{ fieldErrors.path }}</FieldError>
                        </Field>

                        <Separator />
                        <p class="text-sm font-medium">
                            Authentication
                            <span class="text-muted-foreground font-normal"> (password or private key)</span>
                        </p>

                        <Field>
                            <FieldLabel for="sftp-password">
                                Password <span class="text-muted-foreground font-normal">(optional)</span>
                            </FieldLabel>
                            <Input id="sftp-password" v-model="sftpPassword" type="password" autocomplete="off" />
                        </Field>
                        <Field>
                            <FieldLabel for="sftp-key">
                                Private Key (PEM) <span class="text-muted-foreground font-normal">(optional)</span>
                            </FieldLabel>
                            <textarea id="sftp-key" v-model="sftpPrivateKey" rows="4"
                                placeholder="-----BEGIN OPENSSH PRIVATE KEY-----"
                                class="border-input bg-background placeholder:text-muted-foreground focus-visible:border-ring focus-visible:ring-ring/50 flex w-full rounded-md border px-3 py-2 text-sm shadow-xs transition-[color,box-shadow] focus-visible:ring-[3px] resize-none font-mono" />
                        </Field>
                    </template>

                    <!-- ── REST ── -->
                    <template v-if="selectedType === 'rest'">
                        <Field>
                            <FieldLabel for="rest-url">REST Server URL</FieldLabel>
                            <Input id="rest-url" v-model="restUrl" placeholder="https://rest.example.com/repo"
                                :class="fieldErrors.url ? 'border-destructive focus-visible:ring-destructive/30' : ''" />
                            <FieldError v-if="fieldErrors.url">{{ fieldErrors.url }}</FieldError>
                        </Field>

                        <Separator />
                        <p class="text-sm font-medium">
                            Credentials
                            <span class="text-muted-foreground font-normal"> (optional)</span>
                        </p>

                        <Field>
                            <FieldLabel for="rest-user">Username</FieldLabel>
                            <Input id="rest-user" v-model="restUser" autocomplete="off" />
                        </Field>
                        <Field>
                            <FieldLabel for="rest-password">Password</FieldLabel>
                            <Input id="rest-password" v-model="restPassword" type="password" autocomplete="off" />
                        </Field>
                    </template>

                    <!-- ── Rclone ── -->
                    <template v-if="selectedType === 'rclone'">
                        <Field>
                            <FieldLabel for="rclone-remote">Remote Name</FieldLabel>
                            <Input id="rclone-remote" v-model="rcloneRemote" placeholder="myremote"
                                :class="fieldErrors.remote ? 'border-destructive focus-visible:ring-destructive/30' : ''" />
                            <p class="text-muted-foreground text-xs">
                                Must match a remote configured in rclone.conf on the agent.
                            </p>
                            <FieldError v-if="fieldErrors.remote">{{ fieldErrors.remote }}</FieldError>
                        </Field>
                        <Field>
                            <FieldLabel for="rclone-path">Path</FieldLabel>
                            <Input id="rclone-path" v-model="rclonePath" placeholder="bucket/backups"
                                :class="fieldErrors.path ? 'border-destructive focus-visible:ring-destructive/30' : ''" />
                            <FieldError v-if="fieldErrors.path">{{ fieldErrors.path }}</FieldError>
                        </Field>
                    </template>

                    <!-- Enabled toggle — edit mode only -->
                    <template v-if="isEdit">
                        <Separator />
                        <div class="flex items-center justify-between">
                            <div>
                                <p class="text-sm font-medium">Enabled</p>
                                <p class="text-muted-foreground text-xs">
                                    Disabled destinations are skipped during backup jobs.
                                </p>
                            </div>
                            <Switch :model-value="enabled" @update:model-value="enabled = $event" />
                        </div>
                    </template>

                    <SheetFooter class="mt-2 px-0">
                        <Button type="button" variant="outline" :disabled="submitting" @click="onOpenChange(false)">
                            Cancel
                        </Button>
                        <Button type="submit" :disabled="submitting">
                            <Loader2 v-if="submitting" class="size-4 animate-spin" />
                            {{ submitting ? 'Saving…' : (isEdit ? 'Save Changes' : 'Create Destination') }}
                        </Button>
                    </SheetFooter>

                </FieldGroup>
            </form>
        </SheetContent>
    </Sheet>
</template>