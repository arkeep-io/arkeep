<script setup lang="ts">
import { KeyRound, Mail } from 'lucide-vue-next'
</script>

<template>
    <div class="flex flex-col gap-6 p-6">

        <!-- Page header -->
        <div>
            <h1 class="text-2xl font-semibold tracking-tight">Settings</h1>
            <p class="mt-1 text-sm text-muted-foreground">
                Manage authentication and notification configuration.
            </p>
        </div>

        <div class="flex gap-8 min-h-0">

            <!-- Left nav -->
            <aside class="w-52 shrink-0">
                <nav class="flex flex-col gap-1">
                    <RouterLink :to="{ name: 'settings-oidc' }"
                        class="flex items-center gap-3 rounded-md px-3 py-2 text-sm font-medium text-muted-foreground transition-colors hover:bg-accent hover:text-accent-foreground"
                        active-class="bg-accent text-accent-foreground">
                        <KeyRound class="size-4 shrink-0" />
                        OpenID Connect
                    </RouterLink>
                    <RouterLink :to="{ name: 'settings-smtp' }"
                        class="flex items-center gap-3 rounded-md px-3 py-2 text-sm font-medium text-muted-foreground transition-colors hover:bg-accent hover:text-accent-foreground"
                        active-class="bg-accent text-accent-foreground">
                        <Mail class="size-4 shrink-0" />
                        SMTP
                    </RouterLink>
                </nav>
            </aside>

            <!-- Sub-page content -->
            <div class="flex-1 min-w-0">
                <RouterView />
            </div>

        </div>
    </div>
</template>
