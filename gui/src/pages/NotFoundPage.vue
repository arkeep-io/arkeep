<script setup lang="ts">
import { useRouter } from 'vue-router'
import { Button } from '@/components/ui/button'
import { ArrowLeft, FileQuestion } from 'lucide-vue-next'

const router = useRouter()
</script>

<template>
    <div class="flex flex-col items-center justify-center min-h-[60vh] gap-6 p-6 text-center">
        <div class="p-5 rounded-full bg-muted">
            <FileQuestion class="w-10 h-10 text-muted-foreground" />
        </div>
        <div class="flex flex-col gap-2">
            <h1 class="text-2xl font-semibold tracking-tight">Page not found</h1>
            <p class="text-sm text-muted-foreground max-w-sm">
                The page you're looking for doesn't exist or has been moved.
            </p>
        </div>
        <Button variant="outline" @click="router.back()">
            <ArrowLeft class="w-4 h-4 mr-2" />
            Go back
        </Button>
    </div>
</template>