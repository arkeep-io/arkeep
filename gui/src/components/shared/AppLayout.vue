<script setup lang="ts">
// AppLayout.vue — Main application shell.
//
// Layout: fixed sidebar (left) + topbar (top) + scrollable main content area.
// The sidebar is collapsible — icon-only at 56px, expanded with labels at 220px.
// Collapse state is persisted in localStorage so it survives page refreshes.
//
// Navigation is grouped into sections matching the main feature areas:
//   - Core: Dashboard, Agents, Policies, Destinations
//   - Data: Snapshots, Jobs
//   - Ops: Monitoring, Settings (admin only)

import { ref, computed } from 'vue'
import { useRouter } from 'vue-router'
import { useAuthStore } from '@/stores/auth'
import { Button } from '@/components/ui/button'
import {
    DropdownMenu,
    DropdownMenuContent,
    DropdownMenuItem,
    DropdownMenuLabel,
    DropdownMenuSeparator,
    DropdownMenuTrigger,
} from '@/components/ui/dropdown-menu'
import {
    Tooltip,
    TooltipContent,
    TooltipProvider,
    TooltipTrigger,
} from '@/components/ui/tooltip'
import {
    LayoutDashboard,
    Server,
    Shield,
    HardDrive,
    Archive,
    ClipboardList,
    Activity,
    Settings,
    ChevronLeft,
    ChevronRight,
    LogOut,
    User,
    Bell,
    Circle,
} from 'lucide-vue-next'

// ─── Types ────────────────────────────────────────────────────────────────────

interface NavItem {
    label: string
    to: string
    icon: typeof LayoutDashboard
    adminOnly?: boolean
}

interface NavSection {
    label: string
    items: NavItem[]
}

// ─── Navigation config ────────────────────────────────────────────────────────

const NAV: NavSection[] = [
    {
        label: 'Overview',
        items: [
            { label: 'Dashboard', to: '/dashboard', icon: LayoutDashboard },
        ],
    },
    {
        label: 'Infrastructure',
        items: [
            { label: 'Agents', to: '/agents', icon: Server },
            { label: 'Policies', to: '/policies', icon: Shield },
            { label: 'Destinations', to: '/destinations', icon: HardDrive },
        ],
    },
    {
        label: 'Data',
        items: [
            { label: 'Snapshots', to: '/snapshots', icon: Archive },
            { label: 'Jobs', to: '/jobs', icon: ClipboardList },
        ],
    },
    {
        label: 'Operations',
        items: [
            { label: 'Monitoring', to: '/monitoring', icon: Activity },
            { label: 'Settings', to: '/settings', icon: Settings, adminOnly: true },
        ],
    },
]

// ─── State ────────────────────────────────────────────────────────────────────

const auth = useAuthStore()
const router = useRouter()

// Persist collapse state across reloads
const COLLAPSE_KEY = 'arkeep:sidebar-collapsed'
const collapsed = ref(localStorage.getItem(COLLAPSE_KEY) === 'true')

function toggleCollapse(): void {
    collapsed.value = !collapsed.value
    localStorage.setItem(COLLAPSE_KEY, String(collapsed.value))
}

// Filter nav sections — hide admin-only items for non-admins
const visibleNav = computed < NavSection[] > (() =>
    NAV.map((section) => ({
        ...section,
        items: section.items.filter((item) => !item.adminOnly || auth.isAdmin),
    })).filter((section) => section.items.length > 0),
)

const userInitials = computed(() => {
    const name = auth.user?.name ?? ''
    return name
        .split(' ')
        .map((w) => w[0])
        .slice(0, 2)
        .join('')
        .toUpperCase() || '?'
})

async function logout(): Promise<void> {
    await auth.logout()
    router.push('/login')
}
</script>

<template>
    <TooltipProvider :delay-duration="300">
        <div class="flex h-dvh overflow-hidden bg-zinc-950 text-zinc-100">

            <!-- ── Sidebar ──────────────────────────────────────────────────────── -->
            <aside
                class="relative flex flex-col border-r border-zinc-800/60 bg-zinc-900 transition-all duration-200 ease-in-out shrink-0"
                :class="collapsed ? 'w-14' : 'w-55'">
                <!-- Logo -->
                <div class="flex h-14 items-center border-b border-zinc-800/60 px-3 shrink-0"
                    :class="collapsed ? 'justify-center' : 'gap-2.5'">
                    <div
                        class="flex size-7 shrink-0 items-center justify-center rounded-md bg-blue-500/10 border border-blue-500/20">
                        <svg width="14" height="14" viewBox="0 0 16 16" fill="none" aria-hidden="true">
                            <path d="M8 2L14 5.5V10.5L8 14L2 10.5V5.5L8 2Z" stroke="#60a5fa" stroke-width="1.25"
                                stroke-linejoin="round" fill="none" />
                            <circle cx="8" cy="8" r="2" fill="#60a5fa" />
                        </svg>
                    </div>
                    <Transition enter-active-class="transition-all duration-150 delay-75"
                        enter-from-class="opacity-0 -translate-x-2" leave-active-class="transition-all duration-100"
                        leave-to-class="opacity-0">
                        <span v-if="!collapsed"
                            class="text-sm font-semibold tracking-tight text-zinc-100 whitespace-nowrap overflow-hidden">
                            arkeep
                        </span>
                    </Transition>
                </div>

                <!-- Nav -->
                <nav class="flex-1 overflow-y-auto overflow-x-hidden py-3 px-2 space-y-4">
                    <div v-for="section in visibleNav" :key="section.label">
                        <!-- Section label -->
                        <Transition enter-active-class="transition-all duration-150 delay-75"
                            enter-from-class="opacity-0" leave-active-class="transition-all duration-100"
                            leave-to-class="opacity-0">
                            <p v-if="!collapsed"
                                class="mb-1 px-2 text-[10px] font-medium uppercase tracking-widest text-zinc-600 whitespace-nowrap">
                                {{ section.label }}
                            </p>
                        </Transition>

                        <!-- Nav items -->
                        <ul class="space-y-0.5">
                            <li v-for="item in section.items" :key="item.to">
                                <Tooltip :disabled="!collapsed">
                                    <TooltipTrigger as-child>
                                        <RouterLink :to="item.to"
                                            class="flex items-center gap-2.5 rounded-md px-2 py-1.5 text-sm text-zinc-400 hover:text-zinc-100 hover:bg-zinc-800/70 transition-colors outline-none focus-visible:ring-1 focus-visible:ring-blue-500"
                                            :class="[collapsed && 'justify-center']"
                                            active-class="bg-zinc-800 text-zinc-100">
                                            <component :is="item.icon" class="size-4 shrink-0" />
                                            <Transition enter-active-class="transition-all duration-150 delay-75"
                                                enter-from-class="opacity-0 -translate-x-1"
                                                leave-active-class="transition-all duration-100"
                                                leave-to-class="opacity-0">
                                                <span v-if="!collapsed"
                                                    class="whitespace-nowrap overflow-hidden truncate">
                                                    {{ item.label }}
                                                </span>
                                            </Transition>
                                        </RouterLink>
                                    </TooltipTrigger>
                                    <TooltipContent side="right" class="bg-zinc-800 text-zinc-100 border-zinc-700">
                                        {{ item.label }}
                                    </TooltipContent>
                                </Tooltip>
                            </li>
                        </ul>
                    </div>
                </nav>

                <!-- Bottom: user menu -->
                <div class="shrink-0 border-t border-zinc-800/60 p-2">
                    <DropdownMenu>
                        <DropdownMenuTrigger as-child>
                            <button
                                class="flex w-full items-center gap-2.5 rounded-md px-2 py-1.5 text-sm text-zinc-400 hover:text-zinc-100 hover:bg-zinc-800/70 transition-colors outline-none focus-visible:ring-1 focus-visible:ring-blue-500"
                                :class="[collapsed && 'justify-center']">
                                <!-- Avatar -->
                                <div class="relative shrink-0">
                                    <div
                                        class="flex size-6 items-center justify-center rounded-full bg-blue-500/20 border border-blue-500/30 text-[10px] font-semibold text-blue-300">
                                        {{ userInitials }}
                                    </div>
                                    <!-- Online indicator -->
                                    <Circle
                                        class="absolute -bottom-0.5 -right-0.5 size-2 fill-emerald-500 text-emerald-500" />
                                </div>
                                <Transition enter-active-class="transition-all duration-150 delay-75"
                                    enter-from-class="opacity-0 -translate-x-1"
                                    leave-active-class="transition-all duration-100" leave-to-class="opacity-0">
                                    <div v-if="!collapsed" class="min-w-0 text-left overflow-hidden">
                                        <p class="truncate text-xs font-medium text-zinc-200">
                                            {{ auth.user?.name }}
                                        </p>
                                        <p class="truncate text-[10px] text-zinc-500">
                                            {{ auth.user?.role }}
                                        </p>
                                    </div>
                                </Transition>
                            </button>
                        </DropdownMenuTrigger>

                        <DropdownMenuContent side="right" align="end"
                            class="w-48 bg-zinc-900 border-zinc-700 text-zinc-200">
                            <DropdownMenuLabel class="text-xs text-zinc-500">
                                {{ auth.user?.email }}
                            </DropdownMenuLabel>
                            <DropdownMenuSeparator class="bg-zinc-800" />
                            <DropdownMenuItem class="gap-2 text-sm cursor-pointer focus:bg-zinc-800 focus:text-zinc-100"
                                @click="router.push('/settings/profile')">
                                <User class="size-3.5" />
                                Profile
                            </DropdownMenuItem>
                            <DropdownMenuItem class="gap-2 text-sm cursor-pointer focus:bg-zinc-800 focus:text-zinc-100"
                                @click="router.push('/notifications')">
                                <Bell class="size-3.5" />
                                Notifications
                            </DropdownMenuItem>
                            <DropdownMenuSeparator class="bg-zinc-800" />
                            <DropdownMenuItem
                                class="gap-2 text-sm cursor-pointer text-red-400 focus:bg-red-500/10 focus:text-red-400"
                                @click="logout">
                                <LogOut class="size-3.5" />
                                Sign out
                            </DropdownMenuItem>
                        </DropdownMenuContent>
                    </DropdownMenu>
                </div>

                <!-- Collapse toggle -->
                <Button variant="ghost" size="icon"
                    class="absolute -right-3 top-14.5 size-6 rounded-full border border-zinc-700 bg-zinc-900 hover:bg-zinc-800 text-zinc-500 hover:text-zinc-200 shadow-sm z-10"
                    @click="toggleCollapse">
                    <ChevronLeft v-if="!collapsed" class="size-3" />
                    <ChevronRight v-else class="size-3" />
                </Button>
            </aside>

            <!-- ── Main area ────────────────────────────────────────────────────── -->
            <div class="flex flex-1 flex-col min-w-0 overflow-hidden">

                <!-- Topbar -->
                <header
                    class="flex h-14 shrink-0 items-center justify-between border-b border-zinc-800/60 bg-zinc-900/50 backdrop-blur-sm px-5">
                    <!-- Breadcrumb via slot — pages can inject their own title/actions -->
                    <div class="flex items-center gap-2 min-w-0">
                        <slot name="topbar-left">
                            <RouterLink to="/dashboard"
                                class="text-xs text-zinc-600 hover:text-zinc-400 transition-colors">
                                Arkeep
                            </RouterLink>
                        </slot>
                    </div>

                    <!-- Right side actions -->
                    <div class="flex items-center gap-2">
                        <slot name="topbar-right" />
                    </div>
                </header>

                <!-- Page content -->
                <main class="flex-1 overflow-y-auto">
                    <RouterView />
                </main>

            </div>
        </div>
    </TooltipProvider>
</template>