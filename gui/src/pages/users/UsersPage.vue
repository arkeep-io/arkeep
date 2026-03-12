<script setup lang="ts">
import { ref, onMounted, useTemplateRef, nextTick } from 'vue'
import {
    Table, TableBody, TableCell, TableHead, TableHeader, TableRow,
} from '@/components/ui/table'
import {
    DropdownMenu, DropdownMenuContent, DropdownMenuItem,
    DropdownMenuSeparator, DropdownMenuTrigger,
} from '@/components/ui/dropdown-menu'
import {
    AlertDialog, AlertDialogAction, AlertDialogCancel, AlertDialogContent,
    AlertDialogDescription, AlertDialogFooter, AlertDialogHeader, AlertDialogTitle,
} from '@/components/ui/alert-dialog'
import { Alert, AlertDescription } from '@/components/ui/alert'
import { Avatar, AvatarFallback } from '@/components/ui/avatar'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Skeleton } from '@/components/ui/skeleton'
import { MoreHorizontal, PencilLine, Plus, RefreshCw, Trash2, Users } from 'lucide-vue-next'
import UserSheet from '@/components/users/UserSheet.vue'
import { api } from '@/services/api'
import type { ApiResponse, User } from '@/types'

// ---------------------------------------------------------------------------
// Types
// ---------------------------------------------------------------------------

interface UserListResponse { items: User[]; total: number }

// ---------------------------------------------------------------------------
// State — list
// ---------------------------------------------------------------------------

const users = ref<User[]>([])
const loading = ref(true)
const error = ref<string | null>(null)

// ---------------------------------------------------------------------------
// State — sheet
// ---------------------------------------------------------------------------

const sheetOpen = ref(false)
const sheetUser = ref<User | null>(null)
const sheetRef = useTemplateRef<InstanceType<typeof UserSheet>>('sheetRef')

// ---------------------------------------------------------------------------
// State — delete
// ---------------------------------------------------------------------------

const deleteDialogOpen = ref(false)
const userToDelete = ref<User | null>(null)
const deleteLoading = ref(false)

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

function initials(name: string): string {
    return name.split(' ').map((w) => w[0]).slice(0, 2).join('').toUpperCase() || '?'
}

function roleVariant(role: string): 'default' | 'secondary' {
    return role === 'admin' ? 'default' : 'secondary'
}

function formatDate(iso: string | null): string {
    if (!iso) return '—'
    return new Date(iso).toLocaleString(undefined, { dateStyle: 'medium', timeStyle: 'short' })
}

// ---------------------------------------------------------------------------
// Data fetching
// ---------------------------------------------------------------------------

async function fetchUsers() {
    loading.value = true
    error.value = null
    try {
        const res = await api<ApiResponse<UserListResponse>>('/api/v1/users?limit=100')
        users.value = res.data.items
    } catch (e: any) {
        error.value = e?.message ?? 'Failed to load users.'
    } finally {
        loading.value = false
    }
}

onMounted(fetchUsers)

// ---------------------------------------------------------------------------
// Sheet open helpers
// ---------------------------------------------------------------------------

function openCreate() {
    sheetUser.value = null
    sheetOpen.value = true
    nextTick(() => sheetRef.value?.reset())
}

function openEdit(u: User) {
    sheetUser.value = u
    sheetOpen.value = true
    nextTick(() => sheetRef.value?.reset())
}

// ---------------------------------------------------------------------------
// Delete
// ---------------------------------------------------------------------------

function openDeleteDialog(u: User) {
    userToDelete.value = u
    deleteDialogOpen.value = true
}

async function confirmDelete() {
    if (!userToDelete.value) return
    deleteLoading.value = true
    try {
        await api(`/api/v1/users/${userToDelete.value.id}`, { method: 'DELETE' })
        deleteDialogOpen.value = false
        userToDelete.value = null
        await fetchUsers()
    } catch (e: any) {
        error.value = e?.message ?? 'Failed to delete user.'
    } finally {
        deleteLoading.value = false
    }
}
</script>

<template>
    <div class="flex flex-col gap-6 p-6">

        <!-- Header -->
        <div class="flex items-center justify-between">
            <div>
                <h1 class="text-2xl font-semibold tracking-tight">Users</h1>
                <p class="mt-1 text-sm text-muted-foreground">
                    Manage user accounts and access levels.
                </p>
            </div>
            <div class="flex items-center gap-2">
                <Button variant="outline" size="icon" :disabled="loading" @click="fetchUsers">
                    <RefreshCw class="w-4 h-4" :class="{ 'animate-spin': loading }" />
                </Button>
                <Button @click="openCreate">
                    <Plus class="w-4 h-4" />
                    New User
                </Button>
            </div>
        </div>

        <!-- Error banner -->
        <Alert v-if="error" variant="destructive">
            <AlertDescription>{{ error }}</AlertDescription>
        </Alert>

        <!-- Table -->
        <div class="border rounded-md">
            <Table>
                <TableHeader>
                    <TableRow>
                        <TableHead>User</TableHead>
                        <TableHead>Role</TableHead>
                        <TableHead>Status</TableHead>
                        <TableHead>Last Login</TableHead>
                        <TableHead class="w-13" />
                    </TableRow>
                </TableHeader>
                <TableBody>

                    <!-- Loading -->
                    <template v-if="loading">
                        <TableRow v-for="n in 5" :key="n">
                            <TableCell v-for="col in 5" :key="col">
                                <Skeleton class="w-full h-4" />
                            </TableCell>
                        </TableRow>
                    </template>

                    <!-- Empty -->
                    <template v-else-if="users.length === 0">
                        <TableRow>
                            <TableCell colspan="5">
                                <div class="flex flex-col items-center justify-center gap-3 py-16 text-center">
                                    <div class="p-4 rounded-full bg-muted">
                                        <Users class="w-8 h-8 text-muted-foreground" />
                                    </div>
                                    <div>
                                        <p class="font-medium">No users found</p>
                                        <p class="mt-1 text-sm text-muted-foreground">
                                            Create the first user account to get started.
                                        </p>
                                    </div>
                                </div>
                            </TableCell>
                        </TableRow>
                    </template>

                    <!-- Rows -->
                    <template v-else>
                        <TableRow v-for="u in users" :key="u.id">
                            <TableCell>
                                <div class="flex items-center gap-3">
                                    <Avatar class="w-8 h-8 rounded-lg shrink-0">
                                        <AvatarFallback class="rounded-lg text-xs">
                                            {{ initials(u.display_name) }}
                                        </AvatarFallback>
                                    </Avatar>
                                    <div>
                                        <p class="text-sm font-medium leading-none">{{ u.display_name }}</p>
                                        <p class="text-xs text-muted-foreground mt-0.5">{{ u.email }}</p>
                                    </div>
                                </div>
                            </TableCell>
                            <TableCell>
                                <Badge :variant="roleVariant(u.role)">{{ u.role }}</Badge>
                            </TableCell>
                            <TableCell>
                                <Badge :variant="u.is_active ? 'default' : 'secondary'">
                                    {{ u.is_active ? 'Active' : 'Inactive' }}
                                </Badge>
                            </TableCell>
                            <TableCell class="text-sm text-muted-foreground">
                                {{ formatDate(u.last_login_at) }}
                            </TableCell>
                            <TableCell @click.stop>
                                <DropdownMenu>
                                    <DropdownMenuTrigger as-child>
                                        <Button variant="ghost" size="icon" class="w-8 h-8">
                                            <MoreHorizontal class="w-4 h-4" />
                                            <span class="sr-only">Open actions</span>
                                        </Button>
                                    </DropdownMenuTrigger>
                                    <DropdownMenuContent align="end">
                                        <DropdownMenuItem @click="openEdit(u)">
                                            <PencilLine class="w-4 h-4 mr-2" />
                                            Edit
                                        </DropdownMenuItem>
                                        <DropdownMenuSeparator />
                                        <DropdownMenuItem class="text-destructive focus:text-destructive"
                                            @click="openDeleteDialog(u)">
                                            <Trash2 class="w-4 h-4 mr-2" />
                                            Delete
                                        </DropdownMenuItem>
                                    </DropdownMenuContent>
                                </DropdownMenu>
                            </TableCell>
                        </TableRow>
                    </template>

                </TableBody>
            </Table>
        </div>

    </div>

    <!-- User sheet -->
    <UserSheet ref="sheetRef" :open="sheetOpen" :user="sheetUser" @update:open="sheetOpen = $event"
        @saved="fetchUsers" />

    <!-- Delete dialog -->
    <AlertDialog :open="deleteDialogOpen" @update:open="deleteDialogOpen = $event">
        <AlertDialogContent>
            <AlertDialogHeader>
                <AlertDialogTitle>Delete user?</AlertDialogTitle>
                <AlertDialogDescription>
                    <span v-if="userToDelete">
                        <strong>{{ userToDelete.display_name }}</strong> ({{ userToDelete.email }}) will be
                        permanently deleted. This action cannot be undone.
                    </span>
                </AlertDialogDescription>
            </AlertDialogHeader>
            <AlertDialogFooter>
                <AlertDialogCancel :disabled="deleteLoading">Cancel</AlertDialogCancel>
                <AlertDialogAction class="bg-destructive text-destructive-foreground hover:bg-destructive/90"
                    :disabled="deleteLoading" @click="confirmDelete">
                    {{ deleteLoading ? 'Deleting…' : 'Delete' }}
                </AlertDialogAction>
            </AlertDialogFooter>
        </AlertDialogContent>
    </AlertDialog>
</template>