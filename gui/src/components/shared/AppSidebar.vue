<script setup lang="ts">
import type { SidebarProps } from "@/components/ui/sidebar"

import {
    Activity,
    Archive,
    ClipboardList,
    Command,
    HardDrive,
    LayoutDashboard,
    Server,
    Settings,
    Shield,
} from "lucide-vue-next"

import NavMain from "@/components/shared/NavMain.vue"
import NavUser from "@/components/shared/NavUser.vue"
import {
    Sidebar,
    SidebarContent,
    SidebarFooter,
    SidebarHeader,
    SidebarMenu,
    SidebarMenuButton,
    SidebarMenuItem,
} from "@/components/ui/sidebar"
import { useAuthStore } from "@/stores/auth"
import { computed } from "vue"

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
            { title: 'Monitoring', url: '/monitoring', icon: Activity },
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
                        <a href="#">
                            <div
                                class="flex items-center justify-center rounded-lg aspect-square size-8 bg-sidebar-primary text-sidebar-primary-foreground">
                                <Command class="size-4" />
                            </div>
                            <div class="grid flex-1 text-sm leading-tight text-left">
                                <span class="font-medium truncate">Arkeep</span>
                            </div>
                        </a>
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
        </SidebarFooter>
    </Sidebar>
</template>
