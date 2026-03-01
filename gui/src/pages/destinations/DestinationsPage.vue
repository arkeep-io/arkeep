<script setup lang="ts">
import { ref, onMounted } from 'vue'
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from '@/components/ui/table'
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuSeparator,
  DropdownMenuTrigger,
} from '@/components/ui/dropdown-menu'
import {
  AlertDialog,
  AlertDialogAction,
  AlertDialogCancel,
  AlertDialogContent,
  AlertDialogDescription,
  AlertDialogFooter,
  AlertDialogHeader,
  AlertDialogTitle,
} from '@/components/ui/alert-dialog'
import { Alert, AlertDescription } from '@/components/ui/alert'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Skeleton } from '@/components/ui/skeleton'
import {
  Plus,
  MoreHorizontal,
  PencilLine,
  Trash2,
  HardDrive,
  Server,
  Globe,
  Network,
  Cloud,
} from 'lucide-vue-next'
import { api } from '@/services/api'
import type { Destination, ApiResponse } from '@/types'
import DestinationSheet from '@/components/destinations/DestinationSheet.vue'

interface DestinationListResponse {
  items: Destination[]
  total: number
}

// ---------------------------------------------------------------------------
// State
// ---------------------------------------------------------------------------

const destinations = ref<Destination[]>([])
const total = ref(0)
const loading = ref(true)
const error = ref<string | null>(null)

// Sheet
const sheetOpen = ref(false)
const editingDestination = ref<Destination | null>(null)

// Delete dialog
const deleteDialogOpen = ref(false)
const destinationToDelete = ref<Destination | null>(null)
const deleteLoading = ref(false)

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

function typeIcon(type: string) {
  switch (type) {
    case 'local': return HardDrive
    case 's3': return Cloud
    case 'sftp': return Server
    case 'rest': return Globe
    case 'rclone': return Network
    default: return HardDrive
  }
}

function typeLabel(type: string): string {
  switch (type) {
    case 'local': return 'Local'
    case 's3': return 'S3'
    case 'sftp': return 'SFTP'
    case 'rest': return 'REST'
    case 'rclone': return 'Rclone'
    default: return type
  }
}

// ---------------------------------------------------------------------------
// Data fetching
// ---------------------------------------------------------------------------

async function fetchDestinations() {
  loading.value = true
  error.value = null
  try {
    const res = await api<ApiResponse<DestinationListResponse>>('/api/v1/destinations')
    destinations.value = res.data.items
    total.value = res.data.total
  } catch (e: any) {
    error.value = e?.message ?? 'Failed to load destinations'
  } finally {
    loading.value = false
  }
}

// ---------------------------------------------------------------------------
// Sheet
// ---------------------------------------------------------------------------

function openCreate() {
  editingDestination.value = null
  sheetOpen.value = true
}

function openEditSheet(dest: Destination) {
  editingDestination.value = dest
  sheetOpen.value = true
}

function onSaved() {
  fetchDestinations()
}

// ---------------------------------------------------------------------------
// Delete
// ---------------------------------------------------------------------------

function openDeleteDialog(dest: Destination) {
  destinationToDelete.value = dest
  deleteDialogOpen.value = true
}

async function confirmDelete() {
  if (!destinationToDelete.value) return
  deleteLoading.value = true
  try {
    await api(`/api/v1/destinations/${destinationToDelete.value.id}`, { method: 'DELETE' })
    deleteDialogOpen.value = false
    destinationToDelete.value = null
    await fetchDestinations()
  } catch (e: any) {
    error.value = e?.message ?? 'Failed to delete destination'
  } finally {
    deleteLoading.value = false
  }
}

// ---------------------------------------------------------------------------
// Lifecycle
// ---------------------------------------------------------------------------

onMounted(fetchDestinations)
</script>

<template>
  <div class="flex flex-col gap-6 p-6">

    <!-- Header -->
    <div class="flex items-center justify-between">
      <div>
        <h1 class="text-2xl font-semibold tracking-tight">Destinations</h1>
        <p class="mt-1 text-sm text-muted-foreground">
          Manage storage targets for your backups.
        </p>
      </div>
      <div class="flex items-center gap-2">
        <Button @click="openCreate">
          <Plus class="w-4 h-4" />
          New Destination
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
            <TableHead>Name</TableHead>
            <TableHead>Type</TableHead>
            <TableHead>Status</TableHead>
            <TableHead>Created</TableHead>
            <TableHead class="w-13" />
          </TableRow>
        </TableHeader>

        <TableBody>
          <!-- Loading skeletons -->
          <template v-if="loading">
            <TableRow v-for="n in 5" :key="n">
              <TableCell v-for="col in 5" :key="col">
                <Skeleton class="w-full h-4" />
              </TableCell>
            </TableRow>
          </template>

          <!-- Empty state -->
          <template v-else-if="destinations.length === 0">
            <TableRow>
              <TableCell colspan="5">
                <div class="flex flex-col items-center justify-center gap-3 py-16 text-center">
                  <div class="p-4 rounded-full bg-muted">
                    <HardDrive class="w-8 h-8 text-muted-foreground" />
                  </div>
                  <div>
                    <p class="font-medium">No destinations configured</p>
                    <p class="mt-1 text-sm text-muted-foreground">
                      Create a destination to start storing your backups.
                    </p>
                  </div>
                  <Button size="sm" @click="openCreate">
                    <Plus class="w-4 h-4" />
                    New Destination
                  </Button>
                </div>
              </TableCell>
            </TableRow>
          </template>

          <!-- Data rows -->
          <template v-else>
            <TableRow v-for="dest in destinations" :key="dest.id">
              <TableCell class="font-medium">{{ dest.name }}</TableCell>
              <TableCell>
                <div class="flex items-center gap-2 text-sm text-muted-foreground">
                  <component :is="typeIcon(dest.type)" class="w-4 h-4" />
                  {{ typeLabel(dest.type) }}
                </div>
              </TableCell>
              <TableCell>
                <Badge :variant="dest.enabled ? 'default' : 'secondary'">
                  {{ dest.enabled ? 'Enabled' : 'Disabled' }}
                </Badge>
              </TableCell>
              <TableCell class="text-sm text-muted-foreground">
                {{ new Date(dest.created_at).toLocaleDateString() }}
              </TableCell>

              <!-- Actions dropdown -->
              <TableCell>
                <DropdownMenu>
                  <DropdownMenuTrigger as-child>
                    <Button variant="ghost" size="icon" class="w-8 h-8">
                      <MoreHorizontal class="w-4 h-4" />
                      <span class="sr-only">Open actions</span>
                    </Button>
                  </DropdownMenuTrigger>
                  <DropdownMenuContent align="end">
                    <DropdownMenuItem @click="openEditSheet(dest)">
                      <PencilLine class="w-4 h-4 mr-2" />
                      Edit
                    </DropdownMenuItem>
                    <DropdownMenuSeparator />
                    <DropdownMenuItem class="text-destructive focus:text-destructive" @click="openDeleteDialog(dest)">
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

  <!-- Edit / create sheet -->
  <DestinationSheet :destination="editingDestination" :open="sheetOpen" @update:open="sheetOpen = $event"
    @saved="onSaved" />

  <!-- Delete confirmation dialog -->
  <AlertDialog :open="deleteDialogOpen" @update:open="deleteDialogOpen = $event">
    <AlertDialogContent>
      <AlertDialogHeader>
        <AlertDialogTitle>Delete destination?</AlertDialogTitle>
        <AlertDialogDescription>
          <span v-if="destinationToDelete">
            <strong>{{ destinationToDelete.name }}</strong> will be permanently deleted.
            This action cannot be undone.
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