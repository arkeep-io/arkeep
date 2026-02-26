<script setup lang="ts">
// AppLayout.vue — Main application shell.
//
// Layout: collapsible sidebar (left) + topbar (top) + scrollable main area.
// Collapse state is persisted in localStorage.
//
// Navigation sections:
//   Overview:       Dashboard
//   Infrastructure: Agents, Policies, Destinations
//   Data:           Snapshots, Jobs
//   Operations:     Monitoring, Settings (admin only)

import { ref, computed } from 'vue'
import { useRouter } from 'vue-router'
import { useAuthStore } from '@/stores/auth'
import { useTheme } from '@/composables/useTheme'
import { Button } from '@/components/ui/button'
import { Separator } from '@/components/ui/separator'
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
    Circle,
    Moon,
    Sun,
    Monitor,
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
const { mode, cycle } = useTheme()
const router = useRouter()

const COLLAPSE_KEY = 'arkeep:sidebar-collapsed'
const collapsed = ref(localStorage.getItem(COLLAPSE_KEY) === 'true')

function toggleCollapse(): void {
    collapsed.value = !collapsed.value
    localStorage.setItem(COLLAPSE_KEY, String(collapsed.value))
}

const visibleNav = computed<NavSection[]>(() =>
    NAV.map((section) => ({
        ...section,
        items: section.items.filter((item) => !item.adminOnly || auth.isAdmin),
    })).filter((section) => section.items.length > 0),
)

const userInitials = computed(() =>
    (auth.user?.display_name ?? '')
        .split(' ')
        .map((w) => w[0])
        .slice(0, 2)
        .join('')
        .toUpperCase() || '?',
)

const modeIcon = computed(() => {
    if (mode.value === 'dark') return Moon
    if (mode.value === 'light') return Sun
    return Monitor
})

const modeLabel = computed(() => {
    if (mode.value === 'dark') return 'Dark'
    if (mode.value === 'light') return 'Light'
    return 'System'
})

async function logout(): Promise<void> {
    await auth.logout()
    router.push('/login')
}
</script>

<template>
    <TooltipProvider :delay-duration="400">
        <div class="flex h-dvh overflow-hidden bg-background text-foreground">

            <!-- ── Sidebar ──────────────────────────────────────────────────────── -->
            <aside
                class="relative flex flex-col border-r border-sidebar-border bg-sidebar transition-[width] duration-200 ease-in-out shrink-0"
                :class="collapsed ? 'w-14' : 'w-55'">
                <!-- Logo -->
                <div class="flex h-14 items-center border-b border-sidebar-border px-3 shrink-0"
                    :class="collapsed ? 'justify-center' : 'gap-2.5'">
                    <div
                        class="flex size-7 shrink-0 items-center justify-center rounded-md bg-primary/10 border border-primary/20">
                        <svg width="14" height="14" viewBox="0 0 16 16" fill="none" aria-hidden="true">
                            <path d="M8 2L14 5.5V10.5L8 14L2 10.5V5.5L8 2Z" stroke="currentColor" stroke-width="1.25"
                                stroke-linejoin="round" fill="none" class="text-primary" />
                            <circle cx="8" cy="8" r="2" fill="currentColor" class="text-primary" />
                        </svg>
                    </div>
                    <Transition enter-active-class="transition-all duration-150 delay-75"
                        enter-from-class="opacity-0 -translate-x-2" leave-active-class="transition-all duration-100"
                        leave-to-class="opacity-0">
                        <span v-if="!collapsed"
                            class="text-sm font-semibold tracking-tight text-sidebar-foreground whitespace-nowrap overflow-hidden">
                            arkeep
                        </span>
                    </Transition>
                </div>

                <!-- Nav -->
                <nav class="flex-1 overflow-y-auto overflow-x-hidden py-3 px-2 space-y-4">
                    <div v-for="section in visibleNav" :key="section.label">
                        <Transition enter-active-class="transition-all duration-150 delay-75"
                            enter-from-class="opacity-0" leave-active-class="transition-all duration-100"
                            leave-to-class="opacity-0">
                            <p v-if="!collapsed"
                                class="mb-1 px-2 text-[10px] font-medium uppercase tracking-widest text-muted-foreground whitespace-nowrap">
                                {{ section.label }}
                            </p>
                        </Transition>

                        <ul class="space-y-0.5">
                            <li v-for="item in section.items" :key="item.to">
                                <Tooltip :disabled="!collapsed">
                                    <TooltipTrigger as-child>
                                        <RouterLink :to="item.to"
                                            class="flex items-center gap-2.5 rounded-md px-2 py-1.5 text-sm text-sidebar-foreground/70 hover:text-sidebar-foreground hover:bg-sidebar-accent transition-colors outline-none focus-visible:ring-1 focus-visible:ring-sidebar-ring"
                                            :class="collapsed ? 'justify-center' : ''"
                                            active-class="bg-sidebar-accent text-sidebar-accent-foreground font-medium">
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
                                    <TooltipContent side="right" :side-offset="8">
                                        {{ item.label }}
                                    </TooltipContent>
                                </Tooltip>
                            </li>
                        </ul>
                    </div>
                </nav>

                <!-- Bottom: user menu -->
                <div class="shrink-0 border-t border-sidebar-border p-2 space-y-1">

                    <!-- Theme cycle -->
                    <Tooltip :disabled="!collapsed">
                        <TooltipTrigger as-child>
                            <button
                                class="flex w-full items-center gap-2.5 rounded-md px-2 py-1.5 text-sm text-sidebar-foreground/70 hover:text-sidebar-foreground hover:bg-sidebar-accent transition-colors outline-none focus-visible:ring-1 focus-visible:ring-sidebar-ring"
                                :class="collapsed ? 'justify-center' : ''" :aria-label="modeLabel" @click="cycle()">
                                <component :is="modeIcon" class="size-4 shrink-0" />
                                <Transition enter-active-class="transition-all duration-150 delay-75"
                                    enter-from-class="opacity-0 -translate-x-1"
                                    leave-active-class="transition-all duration-100" leave-to-class="opacity-0">
                                    <span v-if="!collapsed" class="whitespace-nowrap">
                                        {{ modeLabel }}
                                    </span>
                                </Transition>
                            </button>
                        </TooltipTrigger>
                        <TooltipContent side="right" :side-offset="8">
                            {{ modeLabel }}
                        </TooltipContent>
                    </Tooltip>

                    <Separator class="bg-sidebar-border" />

                    <!-- User dropdown -->
                    <DropdownMenu>
                        <DropdownMenuTrigger as-child>
                            <button
                                class="flex w-full items-center gap-2.5 rounded-md px-2 py-1.5 text-sm text-sidebar-foreground/70 hover:text-sidebar-foreground hover:bg-sidebar-accent transition-colors outline-none focus-visible:ring-1 focus-visible:ring-sidebar-ring"
                                :class="collapsed ? 'justify-center' : ''">
                                <div class="relative shrink-0">
                                    <div
                                        class="flex size-6 items-center justify-center rounded-full bg-primary/15 border border-primary/25 text-[10px] font-semibold text-primary">
                                        {{ userInitials }}
                                    </div>
                                    <Circle
                                        class="absolute -bottom-0.5 -right-0.5 size-2 fill-green-500 text-green-500" />
                                </div>
                                <Transition enter-active-class="transition-all duration-150 delay-75"
                                    enter-from-class="opacity-0 -translate-x-1"
                                    leave-active-class="transition-all duration-100" leave-to-class="opacity-0">
                                    <div v-if="!collapsed" class="min-w-0 text-left overflow-hidden">
                                        <p class="truncate text-xs font-medium text-sidebar-foreground">
                                            {{ auth.user?.display_name }}
                                        </p>
                                        <p class="truncate text-[10px] text-muted-foreground capitalize">
                                            {{ auth.user?.role }}
                                        </p>
                                    </div>
                                </Transition>
                            </button>
                        </DropdownMenuTrigger>

                        <DropdownMenuContent side="right" align="end" class="w-48">
                            <DropdownMenuLabel class="text-xs text-muted-foreground font-normal truncate">
                                {{ auth.user?.email }}
                            </DropdownMenuLabel>
                            <DropdownMenuSeparator />
                            <DropdownMenuItem class="gap-2 text-sm cursor-pointer"
                                @click="router.push('/settings/general')">
                                <User class="size-3.5" />
                                Profile
                            </DropdownMenuItem>
                            <DropdownMenuSeparator />
                            <DropdownMenuItem
                                class="gap-2 text-sm cursor-pointer text-destructive focus:text-destructive focus:bg-destructive/10"
                                @click="logout">
                                <LogOut class="size-3.5" />
                                Sign out
                            </DropdownMenuItem>
                        </DropdownMenuContent>
                    </DropdownMenu>
                </div>

                <!-- Collapse toggle -->
                <Button variant="outline" size="icon"
                    class="absolute -right-3 top-14.5 size-6 rounded-full bg-background border-border hover:bg-accent text-muted-foreground hover:text-foreground shadow-sm z-10"
                    @click="toggleCollapse">
                    <ChevronLeft v-if="!collapsed" class="size-3" />
                    <ChevronRight v-else class="size-3" />
                </Button>
            </aside>

            <!-- ── Main area ────────────────────────────────────────────────────── -->
            <div class="flex flex-1 flex-col min-w-0 overflow-hidden">

                <!-- Topbar -->
                <header
                    class="flex h-14 shrink-0 items-center justify-between border-b border-border bg-background/80 backdrop-blur-sm px-5">
                    <div class="flex items-center gap-2 min-w-0">
                        <slot name="topbar-left" />
                    </div>
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