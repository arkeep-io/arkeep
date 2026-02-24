// router/index.ts — Vue Router configuration with navigation guards.
//
// Route structure:
//   /login                 — public, redirects to / if already authenticated
//   /                      — protected shell layout
//   ├── (index)            → redirect to /dashboard
//   ├── /dashboard
//   ├── /agents            → list
//   ├── /agents/:id        → detail
//   ├── /policies          → list
//   ├── /policies/new      → create form
//   ├── /policies/:id      → detail
//   ├── /policies/:id/edit → edit form
//   ├── /destinations      → list
//   ├── /snapshots         → list
//   ├── /jobs              → list
//   ├── /jobs/:id          → detail with live logs
//   ├── /monitoring        → host metrics dashboard
//   └── /settings          → tabbed settings page
//        ├── /settings/general
//        ├── /settings/notifications
//        ├── /settings/oidc
//        └── /settings/users
//
// Auth guard:
//   - Protected routes require an authenticated session.
//   - On first load the guard waits for auth.initialize() to complete before
//     making the allow/redirect decision (silent refresh from httpOnly cookie).
//   - Role guard: admin-only routes return 403 page for non-admin users.

import {
  createRouter,
  createWebHistory,
  type RouteRecordRaw,
} from 'vue-router'
import { useAuthStore } from '@/stores/auth'

// ─── Route definitions ────────────────────────────────────────────────────────

const routes: RouteRecordRaw[] = [
  // ── Public ─────────────────────────────────────────────────────────────────
  /* {
    path: '/login',
    name: 'login',
    component: () => import('@/pages/LoginPage.vue'),
    meta: { public: true },
  }, */

  // OIDC callback — the server handles the actual OAuth exchange and redirects
  // here with the access token. This page reads the token from the URL, stores
  // it, and navigates to the dashboard.
  /* {
    path: '/auth/callback',
    name: 'oidc-callback',
    component: () => import('@/pages/OIDCCallbackPage.vue'),
    meta: { public: true },
  }, */

  // ── Protected shell ─────────────────────────────────────────────────────────
  /* {
    path: '/',
    component: () => import('@/components/shared/AppLayout.vue'),
    meta: { requiresAuth: true },
    children: [
      {
        path: '',
        redirect: '/dashboard',
      },
      {
        path: 'dashboard',
        name: 'dashboard',
        component: () => import('@/pages/DashboardPage.vue'),
      },

      // Agents
      {
        path: 'agents',
        name: 'agents',
        component: () => import('@/pages/AgentsPage.vue'),
      },
      {
        path: 'agents/:id',
        name: 'agent-detail',
        component: () => import('@/pages/AgentDetailPage.vue'),
        props: true,
      },

      // Policies
      {
        path: 'policies',
        name: 'policies',
        component: () => import('@/pages/PoliciesPage.vue'),
      },
      {
        path: 'policies/new',
        name: 'policy-create',
        component: () => import('@/pages/PolicyFormPage.vue'),
        meta: { requiresRole: 'admin' },
      },
      {
        path: 'policies/:id',
        name: 'policy-detail',
        component: () => import('@/pages/PolicyDetailPage.vue'),
        props: true,
      },
      {
        path: 'policies/:id/edit',
        name: 'policy-edit',
        component: () => import('@/pages/PolicyFormPage.vue'),
        props: true,
        meta: { requiresRole: 'admin' },
      },

      // Destinations
      {
        path: 'destinations',
        name: 'destinations',
        component: () => import('@/pages/DestinationsPage.vue'),
      },

      // Snapshots
      {
        path: 'snapshots',
        name: 'snapshots',
        component: () => import('@/pages/SnapshotsPage.vue'),
      },

      // Jobs
      {
        path: 'jobs',
        name: 'jobs',
        component: () => import('@/pages/JobsPage.vue'),
      },
      {
        path: 'jobs/:id',
        name: 'job-detail',
        component: () => import('@/pages/JobDetailPage.vue'),
        props: true,
      },

      // Monitoring
      {
        path: 'monitoring',
        name: 'monitoring',
        component: () => import('@/pages/MonitoringPage.vue'),
      },

      // Settings — tabbed layout with nested routes
      {
        path: 'settings',
        component: () => import('@/pages/SettingsPage.vue'),
        meta: { requiresRole: 'admin' },
        children: [
          {
            path: '',
            redirect: '/settings/general',
          },
          {
            path: 'general',
            name: 'settings-general',
            component: () => import('@/pages/settings/GeneralSettings.vue'),
          },
          {
            path: 'notifications',
            name: 'settings-notifications',
            component: () =>
              import('@/pages/settings/NotificationSettings.vue'),
          },
          {
            path: 'oidc',
            name: 'settings-oidc',
            component: () => import('@/pages/settings/OIDCSettings.vue'),
          },
          {
            path: 'users',
            name: 'settings-users',
            component: () => import('@/pages/settings/UsersSettings.vue'),
          },
        ],
      },
    ],
  }, */

  // ── Error pages ─────────────────────────────────────────────────────────────
  /* {
    path: '/403',
    name: 'forbidden',
    component: () => import('@/pages/ForbiddenPage.vue'),
    meta: { public: true },
  }, */
  /* {
    // Catch-all — must be last
    path: '/:pathMatch(.*)*',
    name: 'not-found',
    component: () => import('@/pages/NotFoundPage.vue'),
    meta: { public: true },
  }, */
]

// ─── Router instance ──────────────────────────────────────────────────────────

export const router = createRouter({
  history: createWebHistory(),
  routes,
  scrollBehavior(_, __, savedPosition) {
    // Restore scroll position on browser back/forward; scroll to top otherwise
    return savedPosition ?? { top: 0 }
  },
})

// ─── Navigation guard ─────────────────────────────────────────────────────────

router.beforeEach(async (to) => {
  const auth = useAuthStore()

  // Wait for the initial silent refresh to complete before making any
  // allow/redirect decision. This prevents a flash-redirect to /login on
  // hard reload when the user actually has a valid refresh token cookie.
  if (!auth.isInitialized) {
    await auth.initialize()
  }

  const isPublic = to.meta.public === true

  // Unauthenticated user trying to access a protected route
  if (!isPublic && !auth.isAuthenticated) {
    return {
      name: 'login',
      // Preserve the intended destination so we can redirect after login
      query: { redirect: to.fullPath },
    }
  }

  // Authenticated user trying to access login — send to dashboard
  if (to.name === 'login' && auth.isAuthenticated) {
    return { name: 'dashboard' }
  }

  // Role-based guard: check the most specific requiresRole in the matched chain
  const requiredRole = [...to.matched]
    .reverse()
    .find((r) => r.meta.requiresRole)?.meta.requiresRole

  if (requiredRole && auth.user?.role !== requiredRole) {
    return { name: 'forbidden' }
  }
})