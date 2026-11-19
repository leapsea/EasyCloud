import { createApp } from 'vue'
import { createPinia } from 'pinia'
import App from './App.vue'
import { router } from './router'
import { setUnauthorizedHandler } from './api/client'
import { useAuth } from './stores/auth'
import './styles/main.css'
import './theme/theme' // applies and tracks the appearance preference
import { i18n } from './i18n'

const app = createApp(App).use(createPinia()).use(router).use(i18n)

setUnauthorizedHandler(() => {
  const auth = useAuth()
  auth.user = null
  if (router.currentRoute.value.path !== '/login') {
    router.replace({ path: '/login', query: { next: router.currentRoute.value.fullPath } })
  }
})

app.mount('#app')
