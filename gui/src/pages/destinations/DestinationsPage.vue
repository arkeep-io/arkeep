<script setup lang="ts">
import { ref, onMounted } from 'vue'
import { Plus, HardDrive, Server, Globe, Network, Cloud, Pencil, Trash2 } from 'lucide-vue-next'
import { api } from '@/services/api'
import type { Destination, ApiResponse, PaginatedResponse } from '@/types'
import { Button } from '@/components/ui/button'
import { Badge } from '@/components/ui/badge'
import {
  Table, TableBody, TableCell, TableHead, TableHeader, TableRow,
} from '@/components/ui/table'
import {
  AlertDialog, AlertDialogAction, AlertDialogCancel, AlertDialogContent,
  AlertDialogDescription, AlertDialogFooter, AlertDialogHeader, AlertDialogTitle,
} from '@/components/ui/alert-dialog'
import { Alert, AlertDescription } from '@/components/ui/alert'
import DestinationSheet from '@/components/destinations/DestinationSheet.vue'

// ─── State ───────────────────────────────────────────────────────────────────

const destinations = ref<Destination[]>([])
const total = ref(0)
const loading = ref(false)
const error = ref('')

// Sheet
const sheetOpen = ref(false)
const editingDestination = ref<Destination | null>(null)

// Delete dialog
const deleteDialogOpen = ref(false)
const deletingDestination = ref<Destination | null>(null)
const deleteLoading = ref(false)
const deleteError = ref('')

// ─── Helpers ─────────────────────────────────────────────────────────────────

const typeIcon = (type: string) => {
  switch (type) {
    case 'local': return HardDrive
    case 's3': return Cloud
    case 'sftp': return Server
    case 'rest': return Globe
    case 'rclone': return Network
    default: return HardDrive
  }
}

const typeLabel = (type: string) => {
  switch (type) {
    case 'local': return 'Local'
    case 's3': return 'S3'
    case 'sftp': return 'SFTP'
    case 'rest': return 'REST'
    case 'rclone': return 'Rclone'
    default: return type
  }
}

// ─── Fetch ───────────────────────────────────────────────────────────────────

async function fetchDestinations() {
  loading.value = true
  error.value = ''
  try {
    const res = await api<ApiResponse<PaginatedResponse<Destination>>>('/api/v1/destinations')
    destinations.value = res.data.items
    total.value = res.data.total
  } catch {
    error.value = 'Failed to load destinations.'
  } finally {
    loading.value = false
  }
}

onMounted(fetchDestinations)

// ─── Sheet ───────────────────────────────────────────────────────────────────

function openCreate() {
  editingDestination.value = null
  sheetOpen.value = true
}

function openEdit(dest: Destination) {
  editingDestination.value = dest
  sheetOpen.value = true
}

function onSaved() {
  sheetOpen.value = false
  fetchDestinations()
}

// ─── Delete ──────────────────────────────────────────────────────────────────

function openDelete(dest: Destination) {
  deletingDestination.value = dest
  deleteError.value = ''
  deleteDialogOpen.value = true
}

async function confirmDelete() {
  if (!deletingDestination.value) return
  deleteLoading.value = true
  deleteError.value = ''
  try {
    await api(`/api/v1/destinations/${deletingDestination.value.id}`, { method: 'DELETE' })
    deleteDialogOpen.value = false
    fetchDestinations()
  } catch (err: any) {
    deleteError.value = err?.data?.error ?? 'Failed to delete destination.'
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
        <h1 class="text-2xl font-semibold tracking-tight">Destinations</h1>
        <p class="text-muted-foreground text-sm mt-1">
          Manage storage targets for your backups.
        </p>
      </div>
      <Button @click="openCreate">
        <Plus class="size-4 mr-2" />
        New Destination
      </Button>
    </div>

    <!-- Error -->
    <Alert v-if="error" variant="destructive">
      <AlertDescription>{{ error }}</AlertDescription>
    </Alert>

    <!-- Table -->
    <div class="rounded-lg border">
      <Table>
        <TableHeader>
          <TableRow>
            <TableHead>Name</TableHead>
            <TableHead>Type</TableHead>
            <TableHead>Status</TableHead>
            <TableHead>Created</TableHead>
            <TableHead class="w-20" />
          </TableRow>
        </TableHeader>
        <TableBody>
          <TableRow v-if="loading">
            <TableCell colspan="5" class="text-center text-muted-foreground py-10">
              Loading...
            </TableCell>
          </TableRow>
          <TableRow v-else-if="destinations.length === 0">
            <TableCell colspan="5" class="text-center text-muted-foreground py-10">
              No destinations yet. Create one to get started.
            </TableCell>
          </TableRow>
          <TableRow v-for="dest in destinations" :key="dest.id">
            <TableCell class="font-medium">{{ dest.name }}</TableCell>
            <TableCell>
              <div class="flex items-center gap-2 text-sm">
                <component :is="typeIcon(dest.type)" class="size-4 text-muted-foreground" />
                {{ typeLabel(dest.type) }}
              </div>
            </TableCell>
            <TableCell>
              <Badge :variant="dest.enabled ? 'default' : 'secondary'">
                {{ dest.enabled ? 'Enabled' : 'Disabled' }}
              </Badge>
            </TableCell>
            <TableCell class="text-muted-foreground text-sm">
              {{ new Date(dest.created_at).toLocaleDateString() }}
            </TableCell>
            <TableCell>
              <div class="flex items-center gap-1">
                <Button variant="ghost" size="icon" @click="openEdit(dest)">
                  <Pencil class="size-4" />
                </Button>
                <Button variant="ghost" size="icon" @click="openDelete(dest)">
                  <Trash2 class="size-4 text-destructive" />
                </Button>
              </div>
            </TableCell>
          </TableRow>
        </TableBody>
      </Table>
    </div>

    <!-- Delete Dialog -->
    <AlertDialog :open="deleteDialogOpen" @update:open="deleteDialogOpen = $event">
      <AlertDialogContent>
        <AlertDialogHeader>
          <AlertDialogTitle>Delete destination?</AlertDialogTitle>
          <AlertDialogDescription>
            <span class="font-medium">{{ deletingDestination?.name }}</span> will be permanently
            deleted. This action cannot be undone.
          </AlertDialogDescription>
        </AlertDialogHeader>
        <Alert v-if="deleteError" variant="destructive" class="mt-2">
          <AlertDescription>{{ deleteError }}</AlertDescription>
        </Alert>
        <AlertDialogFooter>
          <AlertDialogCancel :disabled="deleteLoading">Cancel</AlertDialogCancel>
          <AlertDialogAction
            class="bg-destructive text-destructive-foreground hover:bg-destructive/90"
            :disabled="deleteLoading"
            @click.prevent="confirmDelete"
          >
            {{ deleteLoading ? 'Deleting...' : 'Delete' }}
          </AlertDialogAction>
        </AlertDialogFooter>
      </AlertDialogContent>
    </AlertDialog>

    <!-- Sheet -->
    <DestinationSheet
      :open="sheetOpen"
      :destination="editingDestination"
      @update:open="sheetOpen = $event"
      @saved="onSaved"
    />
  </div>
</template>