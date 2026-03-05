<script setup lang="ts">
import { useRouter } from 'vue-router'
import { Button } from '@/components/ui/button'
import { ArrowLeft, ShieldOff } from 'lucide-vue-next'

const router = useRouter()
</script>

<template>
    <div class="flex flex-col items-center justify-center min-h-[60vh] gap-6 p-6 text-center">
        <div class="p-5 rounded-full bg-destructive/10">
            <ShieldOff class="w-10 h-10 text-destructive" />
        </div>
        <div class="flex flex-col gap-2">
            <h1 class="text-2xl font-semibold tracking-tight">Access denied</h1>
            <p class="text-sm text-muted-foreground max-w-sm">
                You don't have permission to view this page.
                Contact your administrator if you think this is a mistake.
            </p>
        </div>
        <Button variant="outline" @click="router.back()">
            <ArrowLeft class="w-4 h-4 mr-2" />
            Go back
        </Button>
    </div>
</template>