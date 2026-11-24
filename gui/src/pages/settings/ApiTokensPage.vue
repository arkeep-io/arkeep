<script setup lang="ts">
// ApiTokensPage.vue — Every user's API tokens, for admins (ADR 0027). Users
// create and revoke their own tokens on their profile; an admin can revoke any
// of them here, for instance when a program's host is compromised.
import { onMounted, ref } from 'vue'
import { Alert, AlertDescription } from '@/components/ui/alert'
import { Button } from '@/components/ui/button'
import { Skeleton } from '@/components/ui/skeleton'
import {
    Table, TableBody, TableCell, TableHead, TableHeader, TableRow,
} from '@/components/ui/table'
import ApiTokenRevokeDialog from '@/components/users/ApiTokenRevokeDialog.vue'
import { AlertCircle, KeyRound } from '@lucide/vue'
import { api, apiErrorMessage } from '@/services/api'
import { formatDate } from '@/lib/jobUtils'
import type { ApiResponse, ApiToken } from '@/types'

const tokens = ref<ApiToken[]>([])
const loading = ref(true)
const loadError = ref<string | null>(null)
const revoking = ref<ApiToken | null>(null)

async function fetchTokens(): Promise<void> {
    loading.value = true
    loadError.value = null
    try {
        const res = await api<ApiResponse<ApiToken[]>>('/api/v1/api-tokens')
        tokens.value = res.data
    } catch (e: any) {
        loadError.value = apiErrorMessage(e, 'Failed to load API tokens.')
    } finally {
        loading.value = false
    }
}

onMounted(fetchTokens)

async function onRevoked(): Promise<void> {
    revoking.value = null
    await fetchTokens()
}

function isExpired(t: ApiToken): boolean {
    return t.expires_at !== null && new Date(t.expires_at).getTime() <= Date.now()
}
</script>

<template>
    <div class="flex flex-col gap-6">
        <div>
            <h2 class="text-lg font-semibold">API Tokens</h2>
            <p class="mt-1 text-sm text-muted-foreground">
                Read-only tokens that users created on their profile for programs that read Arkeep, such as a
                Home Assistant integration. A token sees what the user role sees and never changes anything.
                Revoking one stops it at once.
            </p>
        </div>

        <Alert v-if="loadError" variant="destructive">
            <AlertCircle class="size-4" />
            <AlertDescription>{{ loadError }}</AlertDescription>
        </Alert>

        <!-- w-0 min-w-full: the table scrolls inside the page instead of widening it. -->
        <div class="w-0 min-w-full border rounded-md overflow-x-auto">
            <Table>
                <TableHeader>
                    <TableRow>
                        <TableHead>User</TableHead>
                        <TableHead>Name</TableHead>
                        <TableHead>Token</TableHead>
                        <TableHead>Created</TableHead>
                        <TableHead>Last used</TableHead>
                        <TableHead>Expires</TableHead>
                        <TableHead class="w-24"></TableHead>
                    </TableRow>
                </TableHeader>
                <TableBody>
                    <template v-if="loading">
                        <TableRow v-for="n in 3" :key="n">
                            <TableCell v-for="col in 7" :key="col">
                                <Skeleton class="w-full h-4" />
                            </TableCell>
                        </TableRow>
                    </template>
                    <TableRow v-else-if="tokens.length === 0">
                        <TableCell colspan="7">
                            <div class="flex flex-col items-center justify-center gap-3 py-12 text-center">
                                <div class="p-4 rounded-full bg-muted">
                                    <KeyRound class="w-8 h-8 text-muted-foreground" />
                                </div>
                                <p class="text-sm text-muted-foreground">No API tokens.</p>
                            </div>
                        </TableCell>
                    </TableRow>
                    <template v-else>
                        <TableRow v-for="t in tokens" :key="t.id">
                            <TableCell class="text-sm">{{ t.user_email }}</TableCell>
                            <TableCell class="text-sm font-medium">{{ t.name }}</TableCell>
                            <TableCell><code class="text-xs">{{ t.hint }}…</code></TableCell>
                            <TableCell class="text-sm text-muted-foreground whitespace-nowrap">{{ formatDate(t.created_at) }}</TableCell>
                            <TableCell class="text-sm text-muted-foreground whitespace-nowrap">
                                {{ t.last_used_at ? formatDate(t.last_used_at) : 'Never' }}
                            </TableCell>
                            <TableCell class="text-sm whitespace-nowrap"
                                :class="isExpired(t) ? 'text-destructive' : 'text-muted-foreground'">
                                {{ t.expires_at ? (isExpired(t) ? 'Expired ' : '') + formatDate(t.expires_at) : 'Never' }}
                            </TableCell>
                            <TableCell class="text-right">
                                <Button variant="outline" size="sm" @click="revoking = t">Revoke</Button>
                            </TableCell>
                        </TableRow>
                    </template>
                </TableBody>
            </Table>
        </div>
    </div>

    <ApiTokenRevokeDialog :token="revoking" endpoint="/api/v1/api-tokens"
        @cancel="revoking = null" @revoked="onRevoked" />
</template>
