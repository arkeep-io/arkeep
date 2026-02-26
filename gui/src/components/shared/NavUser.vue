<script setup lang="ts">
import {
    BadgeCheck,
    Bell,
    ChevronsUpDown,
    LogOut,
    Moon,
    Sparkles,
    Sun,
} from "lucide-vue-next"

import {
    Avatar,
    AvatarFallback,
} from "@/components/ui/avatar"
import {
    DropdownMenu,
    DropdownMenuContent,
    DropdownMenuGroup,
    DropdownMenuItem,
    DropdownMenuLabel,
    DropdownMenuSeparator,
    DropdownMenuTrigger,
} from "@/components/ui/dropdown-menu"
import {
    SidebarMenu,
    SidebarMenuButton,
    SidebarMenuItem,
    useSidebar,
} from "@/components/ui/sidebar"
import { useTheme } from "@/composables/useTheme";
import { useAuthStore } from "@/stores/auth";
import { useRouter } from "vue-router";

const props = defineProps<{
    user: { display_name: string, email: string, user_initials: string }
}>()

const { isMobile } = useSidebar()
const auth = useAuthStore();
const { cycle, modeLabel, mode } = useTheme()
const router = useRouter()

async function logout(): Promise<void> {
    await auth.logout()
    router.push('/login')
}
</script>

<template>
    <SidebarMenu>
        <SidebarMenuItem>
            <DropdownMenu>
                <DropdownMenuTrigger as-child>
                    <SidebarMenuButton size="lg"
                        class="data-[state=open]:bg-sidebar-accent data-[state=open]:text-sidebar-accent-foreground">
                        <Avatar class="w-8 h-8 rounded-lg">
                            <AvatarFallback class="rounded-lg">
                                {{ user.user_initials }}
                            </AvatarFallback>
                        </Avatar>
                        <div class="grid flex-1 text-sm leading-tight text-left">
                            <span class="font-medium truncate">{{ user.display_name }}</span>
                            <span class="text-xs truncate">{{ user.email }}</span>
                        </div>
                        <ChevronsUpDown class="ml-auto size-4" />
                    </SidebarMenuButton>
                </DropdownMenuTrigger>
                <DropdownMenuContent class="w-[--reka-dropdown-menu-trigger-width] min-w-56 rounded-lg"
                    :side="isMobile ? 'bottom' : 'right'" align="end" :side-offset="4">
                    <DropdownMenuLabel class="p-0 font-normal">
                        <div class="flex items-center gap-2 px-1 py-1.5 text-left text-sm">
                            <Avatar class="w-8 h-8 rounded-lg">
                                <AvatarFallback class="rounded-lg">
                                    {{ user.user_initials }}
                                </AvatarFallback>
                            </Avatar>
                            <div class="grid flex-1 text-sm leading-tight text-left">
                                <span class="font-semibold truncate">{{ user.display_name }}</span>
                                <span class="text-xs truncate">{{ user.email }}</span>
                            </div>
                        </div>
                    </DropdownMenuLabel>
                    <DropdownMenuSeparator />
                    <DropdownMenuGroup>
                        <DropdownMenuItem>
                            <Sparkles />
                            Upgrade to Pro
                        </DropdownMenuItem>
                    </DropdownMenuGroup>
                    <DropdownMenuSeparator />
                    <DropdownMenuGroup>
                        <DropdownMenuItem @click="cycle()">
                            <Moon v-if="mode === 'light'" />
                            <Sun v-else />
                            {{ modeLabel }}
                        </DropdownMenuItem>
                    </DropdownMenuGroup>
                    <DropdownMenuSeparator />
                    <DropdownMenuGroup>
                        <DropdownMenuItem>
                            <BadgeCheck />
                            Account
                        </DropdownMenuItem>
                        <DropdownMenuItem>
                            <Bell />
                            Notifications
                        </DropdownMenuItem>
                    </DropdownMenuGroup>
                    <DropdownMenuSeparator />
                    <DropdownMenuItem variant="destructive" @click="logout()">
                        <LogOut />
                        Log out
                    </DropdownMenuItem>
                </DropdownMenuContent>
            </DropdownMenu>
        </SidebarMenuItem>
    </SidebarMenu>
</template>
