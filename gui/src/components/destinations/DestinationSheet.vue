<script setup lang="ts">
import { ref, watch, computed } from 'vue'
import { z } from 'zod'
import { api } from '@/services/api'
import type { Destination } from '@/types'
import {
    Sheet, SheetContent, SheetHeader, SheetTitle, SheetDescription, SheetFooter,
} from '@/components/ui/sheet'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import {
    Select, SelectContent, SelectItem, SelectTrigger, SelectValue,
} from '@/components/ui/select'
import { Switch } from '@/components/ui/switch'
import { Alert, AlertDescription } from '@/components/ui/alert'
import { Separator } from '@/components/ui/separator'

// ─── Props / Emits ───────────────────────────────────────────────────────────

const props = defineProps<{
    open: boolean
    destination: Destination | null
}>()

const emit = defineEmits<{
    'update:open': [value: boolean]
    saved: []
}>()

const isEdit = computed(() => !!props.destination)

// ─── Destination types ────────────────────────────────────────────────────────

type DestType = 'local' | 's3' | 'sftp' | 'rest' | 'rclone'

const DEST_TYPES: { value: DestType; label: string }[] = [
    { value: 'local', label: 'Local Path' },
    { value: 's3', label: 'S3 / Object Storage' },
    { value: 'sftp', label: 'SFTP' },
    { value: 'rest', label: 'REST Server' },
    { value: 'rclone', label: 'Rclone' },
]

// ─── Per-type Zod schemas ─────────────────────────────────────────────────────

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

// ─── Form state ───────────────────────────────────────────────────────────────

const selectedType = ref<DestType>('local')
const submitError = ref('')
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

// Per-field errors (simple approach — no vee-validate on dynamic fields)
const fieldErrors = ref<Record<string, string>>({})

// ─── Reset / populate ─────────────────────────────────────────────────────────

function resetFields() {
    name.value = ''
    nameError.value = ''
    enabled.value = true
    submitError.value = ''
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

    // Parse config JSON
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
    // Credentials are write-only — never populated from server
}

watch(() => props.open, (open) => {
    if (open) {
        resetFields()
        if (props.destination) {
            populateFromDestination(props.destination)
        } else {
            selectedType.value = 'local'
        }
    }
})

// Reset field errors when type changes
watch(selectedType, () => {
    fieldErrors.value = {}
    submitError.value = ''
})

// ─── Build payload ────────────────────────────────────────────────────────────

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

// ─── Validate ─────────────────────────────────────────────────────────────────

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
            fieldErrors.value[issue.path[0] as string] = issue.message
        }
        valid = false
    }
    if (!credsResult.success) {
        for (const issue of credsResult.error.issues) {
            fieldErrors.value[`cred_${issue.path[0]}`] = issue.message
        }
        valid = false
    }

    return valid
}

// ─── Submit ───────────────────────────────────────────────────────────────────

async function onSubmit() {
    if (!validate()) return

    submitting.value = true
    submitError.value = ''

    const { config, creds } = buildConfigAndCreds()

    try {
        if (isEdit.value && props.destination) {
            // PATCH — only send what changed; credentials always sent (write-only)
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
        emit('saved')
    } catch (err: any) {
        submitError.value = err?.data?.error ?? 'An unexpected error occurred.'
    } finally {
        submitting.value = false
    }
}
</script>

<template>
    <Sheet :open="open" @update:open="emit('update:open', $event)">
        <SheetContent class="sm:max-w-lg overflow-y-auto">
            <SheetHeader>
                <SheetTitle>{{ isEdit ? 'Edit Destination' : 'New Destination' }}</SheetTitle>
                <SheetDescription>
                    {{ isEdit ? 'Update the destination settings.' : 'Configure a new backup storage target.' }}
                </SheetDescription>
            </SheetHeader>

            <form class="flex flex-col gap-5 py-6" @submit.prevent="onSubmit">

                <!-- Name -->
                <div class="flex flex-col gap-1.5">
                    <Label for="dest-name">Name</Label>
                    <Input id="dest-name" v-model="name" placeholder="My S3 Bucket" />
                    <p v-if="nameError" class="text-destructive text-xs">{{ nameError }}</p>
                </div>

                <!-- Type (disabled in edit mode) -->
                <div class="flex flex-col gap-1.5">
                    <Label>Type</Label>
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
                    <p v-if="isEdit" class="text-muted-foreground text-xs">Type cannot be changed after creation.</p>
                </div>

                <Separator />

                <!-- ── Local ── -->
                <template v-if="selectedType === 'local'">
                    <div class="flex flex-col gap-1.5">
                        <Label for="local-path">Path</Label>
                        <Input id="local-path" v-model="localPath" placeholder="/mnt/backups" />
                        <p v-if="fieldErrors.path" class="text-destructive text-xs">{{ fieldErrors.path }}</p>
                    </div>
                </template>

                <!-- ── S3 ── -->
                <template v-if="selectedType === 's3'">
                    <div class="flex flex-col gap-4">
                        <div class="flex flex-col gap-1.5">
                            <Label for="s3-bucket">Bucket</Label>
                            <Input id="s3-bucket" v-model="s3Bucket" placeholder="my-backup-bucket" />
                            <p v-if="fieldErrors.bucket" class="text-destructive text-xs">{{ fieldErrors.bucket }}</p>
                        </div>
                        <div class="flex flex-col gap-1.5">
                            <Label for="s3-endpoint">Endpoint <span
                                    class="text-muted-foreground">(optional)</span></Label>
                            <Input id="s3-endpoint" v-model="s3Endpoint"
                                placeholder="https://s3.us-east-1.amazonaws.com" />
                        </div>
                        <div class="grid grid-cols-2 gap-3">
                            <div class="flex flex-col gap-1.5">
                                <Label for="s3-region">Region <span
                                        class="text-muted-foreground">(optional)</span></Label>
                                <Input id="s3-region" v-model="s3Region" placeholder="us-east-1" />
                            </div>
                            <div class="flex flex-col gap-1.5">
                                <Label for="s3-prefix">Prefix <span
                                        class="text-muted-foreground">(optional)</span></Label>
                                <Input id="s3-prefix" v-model="s3Prefix" placeholder="backups/" />
                            </div>
                        </div>
                    </div>

                    <Separator />
                    <p class="text-sm font-medium">Credentials</p>
                    <div class="flex flex-col gap-4">
                        <div class="flex flex-col gap-1.5">
                            <Label for="s3-access-key">Access Key ID</Label>
                            <Input id="s3-access-key" v-model="s3AccessKey" autocomplete="off" />
                            <p v-if="fieldErrors.cred_access_key" class="text-destructive text-xs">{{
                                fieldErrors.cred_access_key }}</p>
                        </div>
                        <div class="flex flex-col gap-1.5">
                            <Label for="s3-secret-key">Secret Access Key</Label>
                            <Input id="s3-secret-key" v-model="s3SecretKey" type="password" autocomplete="off" />
                            <p v-if="fieldErrors.cred_secret_key" class="text-destructive text-xs">{{
                                fieldErrors.cred_secret_key }}</p>
                        </div>
                    </div>
                </template>

                <!-- ── SFTP ── -->
                <template v-if="selectedType === 'sftp'">
                    <div class="flex flex-col gap-4">
                        <div class="grid grid-cols-3 gap-3">
                            <div class="col-span-2 flex flex-col gap-1.5">
                                <Label for="sftp-host">Host</Label>
                                <Input id="sftp-host" v-model="sftpHost" placeholder="backup.example.com" />
                                <p v-if="fieldErrors.host" class="text-destructive text-xs">{{ fieldErrors.host }}</p>
                            </div>
                            <div class="flex flex-col gap-1.5">
                                <Label for="sftp-port">Port <span class="text-muted-foreground">(opt.)</span></Label>
                                <Input id="sftp-port" v-model="sftpPort" placeholder="22" />
                            </div>
                        </div>
                        <div class="flex flex-col gap-1.5">
                            <Label for="sftp-user">Username</Label>
                            <Input id="sftp-user" v-model="sftpUser" placeholder="backup-user" />
                            <p v-if="fieldErrors.user" class="text-destructive text-xs">{{ fieldErrors.user }}</p>
                        </div>
                        <div class="flex flex-col gap-1.5">
                            <Label for="sftp-path">Remote Path</Label>
                            <Input id="sftp-path" v-model="sftpPath" placeholder="/home/backup-user/restic" />
                            <p v-if="fieldErrors.path" class="text-destructive text-xs">{{ fieldErrors.path }}</p>
                        </div>
                    </div>

                    <Separator />
                    <p class="text-sm font-medium">Authentication <span
                            class="text-muted-foreground font-normal">(password or private key)</span></p>
                    <div class="flex flex-col gap-4">
                        <div class="flex flex-col gap-1.5">
                            <Label for="sftp-password">Password <span
                                    class="text-muted-foreground">(optional)</span></Label>
                            <Input id="sftp-password" v-model="sftpPassword" type="password" autocomplete="off" />
                        </div>
                        <div class="flex flex-col gap-1.5">
                            <Label for="sftp-key">Private Key (PEM) <span
                                    class="text-muted-foreground">(optional)</span></Label>
                            <textarea id="sftp-key" v-model="sftpPrivateKey" rows="4"
                                placeholder="-----BEGIN OPENSSH PRIVATE KEY-----"
                                class="border-input bg-background placeholder:text-muted-foreground focus-visible:border-ring focus-visible:ring-ring/50 flex w-full rounded-md border px-3 py-2 text-sm shadow-xs transition-[color,box-shadow] focus-visible:ring-[3px] resize-none font-mono" />
                        </div>
                    </div>
                </template>

                <!-- ── REST ── -->
                <template v-if="selectedType === 'rest'">
                    <div class="flex flex-col gap-4">
                        <div class="flex flex-col gap-1.5">
                            <Label for="rest-url">REST Server URL</Label>
                            <Input id="rest-url" v-model="restUrl" placeholder="https://rest.example.com/repo" />
                            <p v-if="fieldErrors.url" class="text-destructive text-xs">{{ fieldErrors.url }}</p>
                        </div>
                    </div>

                    <Separator />
                    <p class="text-sm font-medium">Credentials <span
                            class="text-muted-foreground font-normal">(optional)</span></p>
                    <div class="flex flex-col gap-4">
                        <div class="flex flex-col gap-1.5">
                            <Label for="rest-user">Username</Label>
                            <Input id="rest-user" v-model="restUser" autocomplete="off" />
                        </div>
                        <div class="flex flex-col gap-1.5">
                            <Label for="rest-password">Password</Label>
                            <Input id="rest-password" v-model="restPassword" type="password" autocomplete="off" />
                        </div>
                    </div>
                </template>

                <!-- ── Rclone ── -->
                <template v-if="selectedType === 'rclone'">
                    <div class="flex flex-col gap-4">
                        <div class="flex flex-col gap-1.5">
                            <Label for="rclone-remote">Remote Name</Label>
                            <Input id="rclone-remote" v-model="rcloneRemote" placeholder="myremote" />
                            <p class="text-muted-foreground text-xs">Must match a remote configured in your rclone.conf
                                on the agent.</p>
                            <p v-if="fieldErrors.remote" class="text-destructive text-xs">{{ fieldErrors.remote }}</p>
                        </div>
                        <div class="flex flex-col gap-1.5">
                            <Label for="rclone-path">Path</Label>
                            <Input id="rclone-path" v-model="rclonePath" placeholder="bucket/backups" />
                            <p v-if="fieldErrors.path" class="text-destructive text-xs">{{ fieldErrors.path }}</p>
                        </div>
                    </div>
                </template>

                <Separator />

                <!-- Enabled toggle (edit only) -->
                <div v-if="isEdit" class="flex items-center justify-between">
                    <div>
                        <p class="text-sm font-medium">Enabled</p>
                        <p class="text-muted-foreground text-xs">Disabled destinations are skipped during backup jobs.
                        </p>
                    </div>
                    <Switch :model-value="enabled" @update:model-value="enabled = $event" />
                </div>

                <!-- Submit error -->
                <Alert v-if="submitError" variant="destructive">
                    <AlertDescription>{{ submitError }}</AlertDescription>
                </Alert>

                <!-- Footer -->
                <SheetFooter class="mt-2">
                    <Button type="button" variant="outline" @click="emit('update:open', false)">Cancel</Button>
                    <Button type="submit" :disabled="submitting">
                        {{ submitting ? 'Saving...' : (isEdit ? 'Save Changes' : 'Create Destination') }}
                    </Button>
                </SheetFooter>
            </form>
        </SheetContent>
    </Sheet>
</template>