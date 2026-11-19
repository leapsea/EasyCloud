import { defineStore } from 'pinia'
import { ref } from 'vue'
import { authApi, type Me } from '@/api/files'

export const useAuth = defineStore('auth', () => {
  const user = ref<Me | null>(null)
  const checked = ref(false)

  async function check() {
    try { user.value = await authApi.me() } catch { user.value = null }
    checked.value = true
  }
  async function login(username: string, password: string) { user.value = await authApi.login(username, password) }
  async function logout() { await authApi.logout().catch(() => {}); user.value = null }

  return { user, checked, check, login, logout }
})
