<script setup lang="ts">
import type { SidebarProps } from "@/components/ui/sidebar"

import {
    Archive,
    ClipboardList,
    HardDrive,
    LayoutDashboard,
    Server,
    Settings,
    Shield,
    Users,
} from "lucide-vue-next"

import NavMain from "@/components/shared/NavMain.vue"
import NavUser from "@/components/shared/NavUser.vue"
import UpgradeIndicator from "@/components/shared/UpgradeIndicator.vue"
import {
    Sidebar,
    SidebarContent,
    SidebarFooter,
    SidebarHeader,
    SidebarMenu,
    SidebarMenuButton,
    SidebarMenuItem,
} from "@/components/ui/sidebar"
import { Card, CardContent } from "@/components/ui/card"
import { useAuthStore } from "@/stores/auth"
import { useUpdateStore } from "@/stores/update"
import { computed, onMounted } from "vue"

interface NavSection {
    title: string
    items: NavItem[]
}

interface NavItem {
    title: string
    url: string
    icon: typeof LayoutDashboard
    adminOnly?: boolean
}

const auth = useAuthStore();
const updateStore = useUpdateStore();

onMounted(() => updateStore.fetch())

const props = withDefaults(defineProps<SidebarProps>(), {
    collapsible: "icon",
    variant: "inset",
})

const nav: NavSection[] = [
    {
        title: 'Overview',
        items: [
            { title: 'Dashboard', url: '/dashboard', icon: LayoutDashboard },
        ],
    },
    {
        title: 'Infrastructure',
        items: [
            { title: 'Agents', url: '/agents', icon: Server },
            { title: 'Policies', url: '/policies', icon: Shield },
            { title: 'Destinations', url: '/destinations', icon: HardDrive },
        ],
    },
    {
        title: 'Data',
        items: [
            { title: 'Snapshots', url: '/snapshots', icon: Archive },
            { title: 'Jobs', url: '/jobs', icon: ClipboardList },
        ],
    },
    {
        title: 'Operations',
        items: [
            { title: 'Users', url: '/users', icon: Users, adminOnly: true },
            { title: 'Settings', url: '/settings', icon: Settings, adminOnly: true },
        ],
    },
]

const visibleNav = computed<NavSection[]>(() =>
    nav.map((section) => ({
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
</script>

<template>
    <Sidebar v-bind="props">
        <SidebarHeader>
            <SidebarMenu>
                <SidebarMenuItem>
                    <SidebarMenuButton size="lg" as-child>
                        <RouterLink to="/dashboard">
                            <img src="/arkeep-icon.png" class="size-8 rounded-lg shrink-0" alt="Arkeep" />
                            <div class="grid flex-1 text-sm leading-tight text-left">
                                <span class="font-medium truncate">Arkeep</span>
                            </div>
                        </RouterLink>
                    </SidebarMenuButton>
                </SidebarMenuItem>
            </SidebarMenu>
        </SidebarHeader>
        <SidebarContent>
            <NavMain :items="visibleNav" />
        </SidebarContent>
        <SidebarFooter>
            <NavUser :user="{
                display_name: auth.user?.display_name ?? '',
                email: auth.user?.email ?? '',
                user_initials: userInitials
            }" />
            <!-- Version card: fades and collapses when sidebar goes to icon mode.
                 Uses the same duration/easing as the sidebar's own transitions. -->
            <div class="overflow-hidden transition-[max-height,opacity] duration-200 ease-linear
                        max-h-16 opacity-100
                        group-data-[collapsible=icon]:max-h-0 group-data-[collapsible=icon]:opacity-0">
                <Card class="py-2">
                    <CardContent class="px-3">
                        <div class="flex items-center justify-between text-xs">
                            <span class="text-muted-foreground">Server</span>
                            <div class="flex gap-1.5">
                                {{ updateStore.serverVersion ? `v${updateStore.serverVersion}` : 'Version not available'
                                }}
                                <UpgradeIndicator :show="updateStore.updateAvailable"
                                    :version="updateStore.latestVersion" tooltip-side="right" />
                            </div>
                        </div>
                    </CardContent>
                </Card>
            </div>
            <!-- Collapsed icon: only rendered when an update is available;
                 fades in when sidebar is collapsed, hidden when expanded. -->
            <div v-if="updateStore.updateAvailable" class="flex justify-center overflow-hidden transition-[max-height,opacity] duration-200 ease-linear
                        max-h-0 opacity-0
                        group-data-[collapsible=icon]:max-h-8 group-data-[collapsible=icon]:opacity-100">
                <UpgradeIndicator :show="true" :version="updateStore.latestVersion" tooltip-side="right" />
            </div>
        </SidebarFooter>
    </Sidebar>
</template>
