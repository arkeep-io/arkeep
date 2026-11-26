import { afterEach, describe, expect, it } from 'vitest'
import { defineComponent, h } from 'vue'
import { mountPage, settle } from '@/test/mount'
import { listSortStorageKey, useListSort } from './useListSort'

// The columns of the agents list that can be sorted.
const keys = ['name', 'hostname', 'last_seen']

// A list page reduced to its sort state, which it exposes for the test.
function sortedList(defaults: { key?: string } = {}) {
  let api: ReturnType<typeof useListSort>
  const component = defineComponent({
    setup() {
      api = useListSort('agents', { keys, ...defaults })
      return () => h('div')
    },
  })
  return { component, sort: () => api }
}

async function mountList(path = '/agents', defaults: { key?: string } = { key: 'name' }) {
  const list = sortedList(defaults)
  const { router } = await mountPage(list.component, { path })
  return { sort: list.sort(), router }
}

describe('useListSort', () => {
  afterEach(() => localStorage.clear())

  it('remembers the sort chosen on a list for the next visit', async () => {
    const first = await mountList()
    first.sort.toggle('last_seen', 'desc')
    await settle()
    expect(first.router.currentRoute.value.query).toEqual({ sort: 'last_seen', order: 'desc' })
    expect(JSON.parse(localStorage.getItem(listSortStorageKey('agents'))!)).toEqual({ key: 'last_seen', order: 'desc' })

    // Coming back from the menu: no ?sort= in the URL.
    const second = await mountList('/agents')
    expect(second.sort.sortKey.value).toBe('last_seen')
    expect(second.sort.sortOrder.value).toBe('desc')
    expect(second.sort.queryParams.value).toBe('&sort=last_seen&order=desc')
  })

  it('lets a ?sort= in the URL win over the remembered sort', async () => {
    localStorage.setItem(listSortStorageKey('agents'), JSON.stringify({ key: 'last_seen', order: 'desc' }))
    const { sort } = await mountList('/agents?sort=hostname&order=asc')
    expect(sort.sortKey.value).toBe('hostname')
    expect(sort.queryParams.value).toBe('&sort=hostname&order=asc')
    // Opening a shared link does not change what is remembered.
    expect(JSON.parse(localStorage.getItem(listSortStorageKey('agents'))!).key).toBe('last_seen')
  })

  it('forgets the sort when a third click goes back to the default order', async () => {
    const { sort, router } = await mountList()
    sort.toggle('hostname')
    await settle()
    sort.toggle('hostname')
    await settle()
    expect(sort.sortOrder.value).toBe('desc')
    sort.toggle('hostname')
    await settle()
    expect(router.currentRoute.value.query).toEqual({})
    expect(localStorage.getItem(listSortStorageKey('agents'))).toBeNull()
    expect(sort.sortKey.value).toBe('name')
    expect(sort.queryParams.value).toBe('')
  })

  it('puts the remembered sort in the URL, so a copied link shows the same order', async () => {
    localStorage.setItem(listSortStorageKey('agents'), JSON.stringify({ key: 'last_seen', order: 'desc' }))
    const { sort, router } = await mountList('/agents?page=2')
    await settle()
    expect(router.currentRoute.value.query).toEqual({ page: '2', sort: 'last_seen', order: 'desc' })
    expect(sort.queryParams.value).toBe('&sort=last_seen&order=desc')
  })

  it('ignores a remembered value it cannot use', async () => {
    for (const value of [{ key: 'hostname', order: 'sideways' }, { key: 'removed_column', order: 'asc' }]) {
      localStorage.setItem(listSortStorageKey('agents'), JSON.stringify(value))
      const { sort, router } = await mountList()
      await settle()
      expect(sort.sortKey.value).toBe('name')
      expect(sort.queryParams.value).toBe('')
      expect(router.currentRoute.value.query).toEqual({})
    }
  })

  it('ignores a ?sort= on a column the list cannot sort by', async () => {
    const { sort } = await mountList('/agents?sort=removed_column&order=desc')
    expect(sort.sortKey.value).toBe('name')
    expect(sort.sortOrder.value).toBe('asc')
    expect(sort.queryParams.value).toBe('')
  })

  it('keeps each list separate', async () => {
    const { sort } = await mountList()
    sort.toggle('hostname')
    await settle()
    expect(localStorage.getItem(listSortStorageKey('policies'))).toBeNull()
  })
})
