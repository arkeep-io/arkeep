<script setup lang="ts">
// OIDCCallbackPage.vue — Handles the server-side OIDC redirect.
//
// After a successful OAuth2 exchange the server redirects here:
//   /auth/callback?token=<access_token>&expires_in=<seconds>
//
// This page stores the token in memory (via the auth store), replaces the
// URL so the token is not left in browser history or exposed via Referer,
// then navigates to the dashboard (or the originally-intended destination).

import { onMounted, ref } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { useAuthStore } from '@/stores/auth'
import { Loader2 } from 'lucide-vue-next'

const route = useRoute()
const router = useRouter()
const auth = useAuthStore()

const error = ref<string | null>(null)

onMounted(async () => {
  const token = route.query.token
  const expiresIn = route.query.expires_in

  if (typeof token !== 'string' || !token) {
    error.value = 'Authentication failed: missing token.'
    return
  }

  const expiresInSeconds = typeof expiresIn === 'string' ? parseInt(expiresIn, 10) : 900

  try {
    await auth.setTokenAndFetchUser(token, expiresInSeconds)

    // Remove the token from the URL immediately to prevent leakage via
    // browser history or the Referer header on subsequent navigations.
    // Validate the redirect target to prevent open-redirect attacks:
    // only allow paths that start with "/" and not "//" (protocol-relative URLs).
    const rawRedirect = route.query.redirect
    const redirectTo =
      typeof rawRedirect === 'string' && rawRedirect.startsWith('/') && !rawRedirect.startsWith('//')
        ? rawRedirect
        : '/dashboard'
    await router.replace(redirectTo)
  } catch {
    error.value = 'Authentication failed: could not load user profile.'
  }
})
</script>

<template>
  <div class="flex flex-col items-center justify-center min-h-svh gap-4">
    <template v-if="error">
      <p class="text-destructive text-sm">{{ error }}</p>
      <a href="/login" class="text-sm underline">Back to login</a>
    </template>
    <template v-else>
      <Loader2 class="size-6 animate-spin text-muted-foreground" />
      <p class="text-sm text-muted-foreground">Signing you in…</p>
    </template>
  </div>
</template>
