<script setup lang="ts">
import ThemeToggle from '@/components/ThemeToggle.vue'
import LangToggle from '@/components/LangToggle.vue'
import { Cloud } from 'lucide-vue-next'
import { ref } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { useAuth } from '@/stores/auth'
import { errorMessage } from '@/api/client'

const auth = useAuth()
const router = useRouter()
const route = useRoute()
const username = ref('')
const password = ref('')
const error = ref('')
const busy = ref(false)

async function submit() {
  busy.value = true
  error.value = ''
  try {
    await auth.login(username.value.trim().toLowerCase(), password.value)
    const next = typeof route.query.next === 'string' && route.query.next.startsWith('/') && !route.query.next.startsWith('//') ? route.query.next : '/files'
    router.replace(next)
  } catch (e) {
    error.value = errorMessage(e)
  } finally {
    busy.value = false
  }
}
</script>

<template>
  <div class="relative flex min-h-screen items-center justify-center p-4">
    <div class="absolute right-4 top-4 flex gap-1.5"><LangToggle /><ThemeToggle /></div>
    <form class="card w-full max-w-sm p-6 shadow-sm" @submit.prevent="submit">
      <div class="flex items-center gap-2 text-xl font-semibold"><Cloud class="h-7 w-7 text-brand-600" fill="currentColor" :stroke-width="1.5" /> EasyCloud</div>
      <p class="muted mt-1 text-sm">{{ $t('login.hint') }}</p>
      <input v-model="username" class="input mt-5" :placeholder="$t('login.username')" autocomplete="username" autocapitalize="off" autofocus />
      <input v-model="password" type="password" class="input mt-3" :placeholder="$t('login.password')" autocomplete="current-password" />
      <p v-if="error" class="mt-2 text-sm text-red-600" role="alert">{{ error }}</p>
      <button class="btn btn-primary mt-4 w-full justify-center" :disabled="busy || !password || !username">{{ busy ? $t('login.signingIn') : $t('login.signIn') }}</button>
    </form>
  </div>
</template>
