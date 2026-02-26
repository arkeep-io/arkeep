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

defineProps<{
    items: {
        title: string
        items: {
            title: string
            url: string
            icon?: LucideIcon
            isActive?: boolean
            adminOnly?: boolean
        }[]
    }[]
}>()
</script>

<template>
    <SidebarGroup>
        <SidebarGroupContent>
            <SidebarMenu v-for="item in items" :key="item.title" as-child>
                <SidebarGroupLabel>{{ item.title }}</SidebarGroupLabel>
                <SidebarMenuItem v-for="obj in item.items" :key="obj.url">
                    <SidebarMenuButton as-child size="sm" :is-active="obj.isActive">
                        <a :href="obj.url">
                            <component :is="obj.icon" />
                            <span>{{ obj.title }}</span>
                        </a>
                    </SidebarMenuButton>
                </SidebarMenuItem>
            </SidebarMenu>
        </SidebarGroupContent>
    </SidebarGroup>
</template>
