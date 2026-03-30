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
    useSidebar,
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
const { state } = useSidebar();

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
            <Card v-if="state === 'expanded'">
                <CardContent>
                    <div class="flex items-center justify-between text-xs text-muted-foreground">
                        <span>{{ updateStore.serverVersion ? `v${updateStore.serverVersion}` : 'Version not available'
                            }}</span>
                        <UpgradeIndicator :show="updateStore.updateAvailable" :version="updateStore.latestVersion"
                            tooltip-side="right" />
                    </div>
                </CardContent>
            </Card>
            <div v-else-if="updateStore.updateAvailable" class="flex justify-center py-1">
                <UpgradeIndicator :show="true" :version="updateStore.latestVersion" tooltip-side="right" />
            </div>
            <NavUser :user="{
                display_name: auth.user?.display_name ?? '',
                email: auth.user?.email ?? '',
                user_initials: userInitials
            }" />
        </SidebarFooter>
    </Sidebar>
</template>
