// router/index.ts — Vue Router configuration with navigation guards.
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
import { useSetupStore } from '@/stores/setup'

declare module 'vue-router' {
  interface RouteMeta {
    public?: boolean
    requiresAuth?: boolean
    breadcrumb?: string
    requiresRole?: 'admin'
  }
}

// ─── Route definitions ────────────────────────────────────────────────────────

const routes: RouteRecordRaw[] = [
  // ── Public ─────────────────────────────────────────────────────────────────
  {
    path: '/login',
    name: 'login',
    component: () => import('@/pages/auth/LoginPage.vue'),
    meta: { public: true },
  },
  {
    path: '/setup',
    name: 'setup',
    component: () => import('@/pages/auth/SetupPage.vue'),
    meta: { public: true },
  },

  // OIDC callback — the server handles the actual OAuth exchange and redirects
  // here with the access token. This page reads the token from the URL, stores
  // it, and navigates to the dashboard.
  {
    path: '/auth/callback',
    name: 'oidc-callback',
    component: () => import('@/pages/auth/OIDCCallbackPage.vue'),
    meta: { public: true },
  },

  // ── Protected shell ─────────────────────────────────────────────────────────
  {
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
        component: () => import('@/pages/dashboard/DashboardPage.vue'),
        meta: { breadcrumb: "Dashboard" },
      },

      // Agents
      {
        path: "agents",
        meta: { breadcrumb: "Agents" },
        children: [
          {
            path: "",
            name: "agents",
            component: () => import("@/pages/agents/AgentsPage.vue"),
          },
          {
            path: ":id",
            name: "agent-detail",
            component: () => import("@/pages/agents/AgentDetailPage.vue"),
            props: true,
            meta: { breadcrumb: "Agent Details" },
          },
        ],
      },

      // Policies
      {
        path: 'policies',
        meta: { breadcrumb: "Policies" },
        children: [
          {
            path: "",
            name: 'policies',
            component: () => import('@/pages/policies/PoliciesPage.vue'),
          },
          {
            path: ":id",
            name: "policy-detail",
            component: () => import("@/pages/policies/PolicyDetailPage.vue"),
            props: true,
            meta: { breadcrumb: "Policy Details" },
          },
        ]
      },

      // Destinations
      {
        path: 'destinations',
        name: 'destinations',
        component: () => import('@/pages/destinations/DestinationsPage.vue'),
        meta: { breadcrumb: "Destinations" },
      },

      // Snapshots
      {
        path: 'snapshots',
        name: 'snapshots',
        component: () => import('@/pages/snapshots/SnapshotsPage.vue'),
        meta: { breadcrumb: "Snapshots" },
      },

      // Jobs
      {
        path: 'jobs',
        meta: { breadcrumb: "Jobs" },
        children: [
          {
            path: '',
            name: 'jobs',
            component: () => import('@/pages/jobs/JobsPage.vue'),
          },
          {
            path: ':id',
            name: 'job-detail',
            component: () => import('@/pages/jobs/JobDetailPage.vue'),
            props: true,
            meta: { breadcrumb: "Job Details" },
          },
        ]
      },

      // Users — admin only
      {
        path: 'users',
        name: 'users',
        component: () => import('@/pages/users/UsersPage.vue'),
        meta: { breadcrumb: 'Users', requiresRole: 'admin' },
      },

      // Settings — single page with OIDC + SMTP tabs, admin only
      {
        path: 'settings',
        name: 'settings',
        component: () => import('@/pages/settings/SettingsPage.vue'),
        meta: { breadcrumb: 'Settings', requiresRole: 'admin' },
      },

      // Profile — accessible to all authenticated users
      {
        path: 'profile',
        name: 'profile',
        component: () => import('@/pages/users/ProfilePage.vue'),
        meta: { breadcrumb: 'Profile' },
      },
    ],
  },

  // ── Error pages ─────────────────────────────────────────────────────────────
  {
    path: '/403',
    name: 'forbidden',
    component: () => import('@/pages/ForbiddenPage.vue'),
    meta: { public: true },
  },
  {
    // Catch-all — must be last
    path: '/:pathMatch(.*)*',
    name: 'not-found',
    component: () => import('@/pages/NotFoundPage.vue'),
    meta: { public: true },
  },
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

// ─── Title update ─────────────────────────────────────────────────────────────

router.afterEach((to) => {
  const breadcrumbs = to.matched
    .map((r) => r.meta.breadcrumb as string | undefined)
    .filter(Boolean) as string[]

  document.title = breadcrumbs.length > 0
    ? `Arkeep | ${breadcrumbs.at(-1)}`
    : 'Arkeep'
})

// ─── Navigation guard ─────────────────────────────────────────────────────────

router.beforeEach(async (to) => {
  const auth = useAuthStore()
  const setup = useSetupStore()

  // Check setup status first — if no admin user exists yet, every navigation
  // lands on /setup. This runs once per session; subsequent calls return the
  // cached value immediately without a network round-trip.
  const setupCompleted = await setup.fetchStatus()

  if (!setupCompleted) {
    // Allow /setup itself through; redirect everything else.
    if (to.name !== 'setup') return { name: 'setup' }
    return
  }

  // Setup is done — /setup is no longer accessible.
  if (to.name === 'setup') return { name: 'login' }

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
  const requiredRole = to.matched
    .findLast((r) => r.meta.requiresRole)?.meta.requiresRole

  if (requiredRole && auth.user?.role !== requiredRole) {
    return { name: 'forbidden' }
  }
})