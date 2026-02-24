<script setup lang="ts">
import { Button, buttonVariants } from '@/components/ui/button';
import { ref, type HTMLAttributes } from 'vue';
import { cn } from '@/lib/utils';
import Spinner from '@/components/ui/spinner/Spinner.vue';
import { Field, FieldDescription, FieldGroup, FieldLabel, FieldSeparator } from '@/components/ui/field';
import { Input } from '@/components/ui/input';
import { GitGraphIcon } from 'lucide-vue-next';

const props = defineProps<{
    class?: HTMLAttributes['class']
}>()

const isLoading = ref(false)

async function onSubmit(event: Event) {
    event.preventDefault()
    isLoading.value = (true)

    setTimeout(() => {
        isLoading.value = (false)
    }, 3000)
}
</script>

<template>
    <div class="md:hidden">
        <img src="#" width="1280" height="843" alt="Authentication" class="block dark:hidden" />
        <img src="#" width="1280" height="843" alt="Authentication" class="hidden dark:block" />
    </div>

    <div
        class="relative container hidden flex-1 shrink-0 items-center justify-center md:grid lg:max-w-none lg:grid-cols-2 lg:px-0">
        <RouterLink to="/examples/authentication" :class="cn(
            buttonVariants({ variant: 'ghost' }),
            'absolute top-4 right-4 md:top-8 md:right-8',
        )">
            Login
        </RouterLink>
        <div class="text-primary relative hidden h-full flex-col p-10 lg:flex dark:border-r">
            <div class="bg-primary/5 absolute inset-0" />
            <div class="relative z-20 flex items-center text-lg font-medium">
                <svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 24 24" fill="none" stroke="currentColor"
                    strokeWidth="2" strokeLinecap="round" strokeLinejoin="round" class="mr-2 h-6 w-6">
                    <path d="M15 6v12a3 3 0 1 0 3-3H6a3 3 0 1 0 3 3V6a3 3 0 1 0-3 3h12a3 3 0 1 0-3-3" />
                </svg>
                Acme Inc
            </div>
            <div class="relative z-20 mt-auto">
                <blockquote class="leading-normal text-balance">
                    &ldquo;This library has saved me countless hours of work and
                    helped me deliver stunning designs to my clients faster than ever
                    before.&rdquo; - Sofia Davis
                </blockquote>
            </div>
        </div>
        <div class="flex items-center justify-center lg:h-250 lg:p-8">
            <div class="mx-auto flex w-full flex-col justify-center gap-6 sm:w-87.5">
                <div class="flex flex-col gap-2 text-center">
                    <h1 class="text-2xl font-semibold tracking-tight">
                        Create an account
                    </h1>
                    <p class="text-muted-foreground text-sm">
                        Enter your email below to create your account
                    </p>
                </div>
                <div :class="cn('grid gap-6', props.class)">
                    <form @submit="onSubmit">
                        <FieldGroup>
                            <Field>
                                <FieldLabel class="sr-only" for="email">
                                    Email
                                </FieldLabel>
                                <Input id="email" placeholder="name@example.com" type="email" autocapitalize="none"
                                    autocomplete="email" autocorrect="off" :disabled="isLoading" />
                            </Field>
                            <Field>
                                <Button :disabled="isLoading">
                                    <Spinner v-if="isLoading" />
                                    Sign In with Email
                                </Button>
                            </Field>
                        </FieldGroup>
                    </form>
                    <FieldSeparator>Or continue with</FieldSeparator>
                    <Button variant="outline" type="button" disabled="{isLoading}">
                        <Spinner v-if="isLoading" />
                        <GitGraphIcon />

                        GitHub
                    </Button>
                </div>
                <FieldDescription class="px-6 text-center">
                    By clicking continue, you agree to our
                    <RouterLink to="/terms">
                        Terms of Service
                    </RouterLink> and
                    <RouterLink to="/privacy">
                        Privacy Policy
                    </RouterLink>.
                </FieldDescription>
            </div>
        </div>
    </div>
</template>