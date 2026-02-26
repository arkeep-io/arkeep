<script setup lang="ts">
import AppSidebar from "@/components/shared/AppSidebar.vue"
import {
    Breadcrumb,
    BreadcrumbItem,
    BreadcrumbLink,
    BreadcrumbList,
    BreadcrumbPage,
    BreadcrumbSeparator,
} from "@/components/ui/breadcrumb"
import { Separator } from "@/components/ui/separator"
import {
    SidebarInset,
    SidebarProvider,
    SidebarTrigger,
} from "@/components/ui/sidebar"
import { computed } from "vue"
import { useRoute } from "vue-router"

type Crumb = {
    label: string
    to?: { name?: string; path?: string; params?: Record<string, any>; query?: Record<string, any> }
}

const route = useRoute()


const breadcrumbs = computed<Crumb[]>(() => {
    return route.matched
        .filter((r) => r.meta?.breadcrumb)
        .map((r) => {
            const raw = r.meta.breadcrumb as string | ((route: any) => string)
            const label = typeof raw === "function" ? raw(route) : raw

            return {
                label,
                to: r.name
                    ? { name: r.name as string, params: route.params, query: route.query }
                    : { path: r.path, query: route.query },
            }
        })
})
</script>

<template>
    <SidebarProvider>
        <AppSidebar />
        <SidebarInset>
            <header class="flex items-center h-16 gap-2 shrink-0">
                <div class="flex items-center gap-2 px-4">
                    <SidebarTrigger class="-ml-1" />
                    <Separator orientation="vertical" class="mr-2 data-[orientation=vertical]:h-4" />
                    <Breadcrumb>
                        <BreadcrumbList>
                            <template v-for="(c, i) in breadcrumbs" :key="i">
                                <BreadcrumbItem>
                                    <BreadcrumbPage v-if="i === breadcrumbs.length - 1">
                                        {{ c.label }}
                                    </BreadcrumbPage>

                                    <BreadcrumbLink v-else>
                                        <RouterLink v-if="c.to" :to="c.to">
                                            {{ c.label }}
                                        </RouterLink>
                                        <span v-else>{{ c.label }}</span>
                                    </BreadcrumbLink>
                                </BreadcrumbItem>

                                <BreadcrumbSeparator v-if="i < breadcrumbs.length - 1" />
                            </template>
                        </BreadcrumbList>
                    </Breadcrumb>
                </div>
            </header>
            <div class="flex flex-col flex-1 gap-4 p-4 pt-0">
                <RouterView />
            </div>
        </SidebarInset>
    </SidebarProvider>
</template>
