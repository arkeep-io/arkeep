// requestOrder numbers the requests of a page so that an answer older than the
// one shown is dropped: a live refresh started before a filter, sort or page
// change may answer after the request of the change. A later request that
// fails leaves the earlier answer in place.
//
//   const beginRequest = requestOrder()
//   async function fetchItems() {
//     const request = beginRequest()
//     try {
//       const res = await api(...)
//       if (!request.show()) return
//       items.value = res.data.items
//     } catch (e) {
//       if (request.stale) return
//       error.value = ...
//     }
//   }
export function requestOrder() {
  let last = 0
  let shown = 0
  return function beginRequest() {
    const n = ++last
    return {
      // show records this answer as the one shown and returns true, or
      // returns false when a later request's answer is already shown.
      show(): boolean {
        if (n < shown) return false
        shown = n
        return true
      },
      // stale is true when a later request's answer is already shown.
      get stale(): boolean {
        return n < shown
      },
    }
  }
}
