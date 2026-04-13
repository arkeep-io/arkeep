<script setup lang="ts">
import { onMounted } from 'vue'
import { Bell, CheckCheck, AlertTriangle, WifiOff } from 'lucide-vue-next'
import {
    DropdownMenu,
    DropdownMenuContent,
    DropdownMenuSeparator,
    DropdownMenuTrigger,
} from '@/components/ui/dropdown-menu'
import { Button } from '@/components/ui/button'
import { useNotificationStore } from '@/stores/notification'
import { useAuthStore } from '@/stores/auth'
import { useWebSocket } from '@/services/websocket'
import type { WSNotificationPayload } from '@/types'

const notifStore = useNotificationStore()
const auth = useAuthStore()

onMounted(() => {
    notifStore.fetchRecent()
})

// Subscribe to real-time notifications for the authenticated user.
// The server automatically adds notifications:<user_id> to the WebSocket
// stream from JWT claims, so we only need to register the local handler.
const userId = auth.user?.id ?? ''
useWebSocket<WSNotificationPayload>(`notifications:${userId}`, (msg) => {
    notifStore.prependFromWS(msg.payload)
})

// ─── Helpers ─────────────────────────────────────────────────────────────────

type IconComponent = typeof Bell

function typeIcon(type: string): IconComponent {
    if (type === 'job_success') return CheckCheck
    if (type === 'job_failure') return AlertTriangle
    if (type === 'agent_offline') return WifiOff
    return Bell
}

function typeIconClass(type: string): string {
    if (type === 'job_success') return 'text-green-500 dark:text-green-400'
    if (type === 'job_failure') return 'text-destructive'
    if (type === 'agent_offline') return 'text-orange-500 dark:text-orange-400'
    return 'text-muted-foreground'
}

function formatRelative(dateStr: string): string {
    const diffMs = Date.now() - new Date(dateStr).getTime()
    const minutes = Math.floor(diffMs / 60_000)
    if (minutes < 1) return 'just now'
    if (minutes < 60) return `${minutes}m ago`
    const hours = Math.floor(minutes / 60)
    if (hours < 24) return `${hours}h ago`
    return `${Math.floor(hours / 24)}d ago`
}
</script>

<template>
    <DropdownMenu>
        <DropdownMenuTrigger as-child>
            <Button variant="ghost" size="icon" class="relative" aria-label="Notifications">
                <Bell class="size-4" />
                <!-- Unread badge -->
                <span
                    v-if="notifStore.unreadCount > 0"
                    class="absolute -top-0.5 -right-0.5 flex items-center justify-center min-w-[1rem] h-4 px-1 text-[10px] font-bold leading-none rounded-full bg-destructive text-destructive-foreground">
                    {{ notifStore.unreadCount > 99 ? '99+' : notifStore.unreadCount }}
                </span>
            </Button>
        </DropdownMenuTrigger>

        <DropdownMenuContent align="end" :side-offset="8" class="w-80 p-0 rounded-lg">

            <!-- Header row -->
            <div class="flex items-center justify-between px-4 py-3">
                <span class="text-sm font-semibold">Notifications</span>
                <button
                    v-if="notifStore.unreadCount > 0"
                    class="text-xs text-muted-foreground hover:text-foreground transition-colors"
                    @click.stop="notifStore.markAllRead()">
                    Mark all as read
                </button>
            </div>

            <DropdownMenuSeparator class="my-0" />

            <!-- Loading -->
            <div v-if="notifStore.loading" class="px-4 py-8 text-center text-sm text-muted-foreground">
                Loading…
            </div>

            <!-- Empty state -->
            <div v-else-if="notifStore.items.length === 0"
                class="px-4 py-8 text-center text-sm text-muted-foreground">
                No notifications yet
            </div>

            <!-- Notification list -->
            <div v-else class="overflow-y-auto max-h-[20rem]">
                <div
                    v-for="n in notifStore.items"
                    :key="n.id"
                    class="flex gap-3 px-4 py-3 cursor-default hover:bg-accent transition-colors"
                    :class="{ 'cursor-pointer': n.read_at === null }"
                    @click="n.read_at === null ? notifStore.markRead(n.id) : undefined">

                    <!-- Type icon -->
                    <div class="mt-0.5 shrink-0">
                        <component
                            :is="typeIcon(n.type)"
                            class="size-4"
                            :class="typeIconClass(n.type)" />
                    </div>

                    <!-- Content -->
                    <div class="flex-1 min-w-0">
                        <p
                            class="text-sm leading-snug"
                            :class="n.read_at === null ? 'font-medium' : 'font-normal text-muted-foreground'">
                            {{ n.title }}
                        </p>
                        <p class="text-xs text-muted-foreground mt-0.5 line-clamp-2">{{ n.body }}</p>
                        <p class="text-[10px] text-muted-foreground/60 mt-1">
                            {{ formatRelative(n.created_at) }}
                        </p>
                    </div>

                    <!-- Unread dot -->
                    <div v-if="n.read_at === null" class="mt-2 shrink-0">
                        <span class="block size-2 rounded-full bg-primary" />
                    </div>
                </div>
            </div>

            <!-- Footer: item count -->
            <template v-if="notifStore.items.length > 0">
                <DropdownMenuSeparator class="my-0" />
                <div class="px-4 py-2 text-center">
                    <span class="text-xs text-muted-foreground">
                        Showing {{ notifStore.items.length }}
                        <template v-if="notifStore.total > notifStore.items.length">
                            of {{ notifStore.total }}
                        </template>
                    </span>
                </div>
            </template>

        </DropdownMenuContent>
    </DropdownMenu>
</template>
