import { createApp } from 'vue'
import { createPinia, setActivePinia } from 'pinia'
import App from './App.vue'
import router from './router'
import { loadRuntimeConfig } from './api/runtimeConfig'
import { initApiClients, setAccessTokenGetter } from './api/client'
import { useAuthStore } from './stores/auth'
import './style.css'

async function bootstrap() {
  const cfg = await loadRuntimeConfig()
  initApiClients(cfg)

  const pinia = createPinia()
  setActivePinia(pinia)

  const auth = useAuthStore()
  auth.init(cfg)
  setAccessTokenGetter(() => auth.accessToken)
  await auth.restore()

  router.beforeEach((to) => {
    if (to.name === 'login-callback') return true
    if (!auth.isAuthenticated) {
      auth.login(to.fullPath)
      return false
    }
    return true
  })

  const app = createApp(App)
  app.use(pinia)
  app.use(router)
  app.mount('#app')
}

bootstrap()
