// useListSort.ts — Sort state of a server-sorted, paginated list (issue #320).
//
// The sort column and direction live in the URL query (?sort=&order=), so they
// survive a reload and travel with a shared link. Without ?sort= the server
// applies the list's default order; pass it as defaults so the matching header
// shows as active.
//
// The last sort chosen on a list is also kept in the browser (localStorage,
// under arkeep:list-sort:<list>), so coming back to the list from the menu,
// whose links carry no ?sort=, shows it sorted the same way. A ?sort= in the URL
// always wins: a shared link shows what its author saw.

import { computed } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { StorageSerializers, useLocalStorage } from '@vueuse/core'

export type SortOrder = 'asc' | 'desc'

interface StoredSort {
  key: string
  order: SortOrder
}

// listSortStorageKey is where the sort of list is remembered.
export function listSortStorageKey(list: string): string {
  return `arkeep:list-sort:${list}`
}

export function useListSort(list: string, defaults: { key?: string; order?: SortOrder } = {}) {
  const route = useRoute()
  const router = useRouter()
  const stored = useLocalStorage<StoredSort | null>(listSortStorageKey(list), null, {
    serializer: StorageSerializers.object,
  })

  const querySort = computed(() => (typeof route.query.sort === 'string' ? route.query.sort : ''))

  // The sort chosen for the list: the URL's, otherwise the remembered one.
  // Null while the list's default order applies.
  const chosen = computed<StoredSort | null>(() => {
    if (querySort.value) {
      return { key: querySort.value, order: route.query.order === 'desc' ? 'desc' : 'asc' }
    }
    const s = stored.value
    if (s && typeof s.key === 'string' && s.key && (s.order === 'asc' || s.order === 'desc')) {
      return s
    }
    return null
  })

  const sortKey = computed(() => chosen.value?.key || defaults.key || '')
  const sortOrder = computed<SortOrder>(() => chosen.value?.order ?? defaults.order ?? 'asc')

  // Appended to the list request; empty while the server default applies.
  const queryParams = computed(() =>
    chosen.value ? `&sort=${encodeURIComponent(chosen.value.key)}&order=${chosen.value.order}` : '',
  )

  // A column not sorted yet sorts in firstOrder, a second click reverses it, a
  // third goes back to the list's default order. The choice is remembered.
  function toggle(key: string, firstOrder: SortOrder = 'asc') {
    const rest = { ...route.query }
    delete rest.sort
    delete rest.order
    let next: StoredSort | null = null
    if (sortKey.value !== key) {
      next = { key, order: firstOrder }
    } else if (sortOrder.value === firstOrder) {
      next = { key, order: firstOrder === 'asc' ? 'desc' : 'asc' }
    }
    stored.value = next
    router.replace({ query: next ? { ...rest, sort: next.key, order: next.order } : rest })
  }

  return { sortKey, sortOrder, queryParams, toggle }
}
