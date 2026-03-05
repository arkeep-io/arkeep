<script setup lang="ts">
import { useRouter } from 'vue-router'
import { Button } from '@/components/ui/button'
import { ArrowLeft, Home, Moon, Sun } from 'lucide-vue-next'
import { useTheme } from '@/composables/useTheme'

const router = useRouter()
const { isDark, cycle, modeLabel } = useTheme()
</script>

<template>
    <div class="relative flex flex-col items-center justify-center w-full min-h-svh p-6">

        <!-- Background grid -->
        <div class="absolute inset-0 z-0" :style="{
            backgroundImage: `
        linear-gradient(to right, ${isDark ? '#3f3f46' : '#d1d5db'} 1px, transparent 1px),
        linear-gradient(to bottom, ${isDark ? '#3f3f46' : '#d1d5db'} 1px, transparent 1px)
      `,
            backgroundSize: '32px 32px',
            WebkitMaskImage: 'radial-gradient(ellipse 60% 60% at 50% 50%, #000 30%, transparent 70%)',
            maskImage: 'radial-gradient(ellipse 60% 60% at 50% 50%, #000 30%, transparent 70%)',
        }" />

        <!-- Theme toggle -->
        <Button variant="ghost" size="icon"
            class="absolute z-10 top-4 right-4 text-muted-foreground hover:text-foreground" :aria-label="modeLabel"
            @click="cycle()">
            <Sun v-if="isDark" class="size-4" />
            <Moon v-else class="size-4" />
        </Button>

        <!-- Content -->
        <div class="relative z-10 flex flex-col items-center gap-6 text-center">
            <p class="text-9xl font-medium uppercase tracking-widest text-destructive">403</p>
            <div class="flex flex-col gap-2">
                <h1 class="text-4xl font-bold tracking-tight">Access denied</h1>
                <p class="text-lg text-muted-foreground max-w-xs text-balance">
                    You don't have permission to view this page. Contact your administrator if you think this is a
                    mistake.
                </p>
            </div>
            <div class="flex items-center gap-3">
                <Button variant="outline" class="bg-background" @click="router.back()">
                    <ArrowLeft class="mr-2 size-4" />
                    Go back
                </Button>
                <Button @click="router.push('/dashboard')">
                    <Home class="mr-2 size-4" />
                    Dashboard
                </Button>
            </div>
        </div>
    </div>

    <!-- Footer — outside flex wrapper so it sits at true bottom -->
    <p class="fixed bottom-0 left-0 right-0 text-center text-xs text-muted-foreground pb-6">
        Arkeep — open source backup management
    </p>
</template>