<script setup lang="ts">
import type { LucideIcon } from "lucide-vue-next"
import {
    SidebarGroup,
    SidebarGroupContent,
    SidebarGroupLabel,
    SidebarMenu,
    SidebarMenuButton,
    SidebarMenuItem,
} from "@/components/ui/sidebar"
import { useRoute } from "vue-router";

defineProps<{
    items: {
        title: string
        items: {
            title: string
            url: string
            icon?: LucideIcon
            adminOnly?: boolean
        }[]
    }[]
}>()

const route = useRoute()

// Returns true if the current route starts with the item's url.
// startsWith instead of exact match so /agents/123 keeps Agents active.
function isActive(url: string): boolean {
    return route.path === url || route.path.startsWith(url + '/')
}
</script>

<template>
    <SidebarGroup>
        <SidebarGroupContent>
            <SidebarMenu v-for="item in items" :key="item.title" as-child>
                <SidebarGroupLabel>{{ item.title }}</SidebarGroupLabel>
                <SidebarMenuItem v-for="obj in item.items" :key="obj.url">
                    <SidebarMenuButton as-child :is-active="isActive(obj.url)">
                        <RouterLink :to="obj.url">
                            <component :is="obj.icon" />
                            <span>{{ obj.title }}</span>
                        </RouterLink>
                    </SidebarMenuButton>
                </SidebarMenuItem>
            </SidebarMenu>
        </SidebarGroupContent>
    </SidebarGroup>
</template>
