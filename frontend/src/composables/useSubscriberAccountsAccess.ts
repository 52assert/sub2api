import { computed, ref } from 'vue'
import subscriptionsAPI from '@/api/subscriptions'
import { useAuthStore } from '@/stores'

const loaded = ref(false)
const loading = ref(false)
const hasActiveSubscription = ref(false)
let pendingLoad: Promise<boolean> | null = null

async function loadSubscriberAccountsAccess(force = false): Promise<boolean> {
  const authStore = useAuthStore()
  if (authStore?.isAdmin) {
    loaded.value = true
    hasActiveSubscription.value = true
    return true
  }
  if (!authStore?.isAuthenticated) {
    loaded.value = true
    hasActiveSubscription.value = false
    return false
  }

  if (loaded.value && !force) {
    return hasActiveSubscription.value
  }

  if (pendingLoad && !force) {
    return pendingLoad
  }

  loading.value = true
  pendingLoad = subscriptionsAPI
    .getActiveSubscriptions()
    .then((subscriptions) => {
      const hasActive = Array.isArray(subscriptions) && subscriptions.length > 0
      hasActiveSubscription.value = hasActive
      loaded.value = true
      return hasActive
    })
    .catch(() => {
      hasActiveSubscription.value = false
      loaded.value = false
      return false
    })
    .finally(() => {
      loading.value = false
      pendingLoad = null
    })

  return pendingLoad
}

export function useSubscriberAccountsAccess() {
  const authStore = useAuthStore()
  const canViewSubscriberAccounts = computed(
    () => Boolean(authStore?.isAdmin) || hasActiveSubscription.value
  )

  return {
    canViewSubscriberAccounts,
    subscriberAccountsAccessLoaded: computed(() => loaded.value),
    subscriberAccountsAccessLoading: computed(() => loading.value),
    refreshSubscriberAccountsAccess: loadSubscriberAccountsAccess
  }
}
