<script setup lang="ts">
import ThemeToggle from '@/components/ThemeToggle.vue'
import { Building2, HardDrive, LogOut, PanelLeftClose, PanelLeftOpen, CircleUserRound, Cloud, FolderOpen, Globe, LayoutDashboard, Puzzle, ScrollText, Settings, Share2, ShieldCheck, Trash2, Users } from 'lucide-vue-next'
import { computed, provide, ref, watch } from 'vue'
import { useMediaQuery } from '@vueuse/core'
import { RouterLink, RouterView, useRoute, useRouter } from 'vue-router'
import { useAuth } from '@/stores/auth'
import { realtime } from '@/api/realtime'
import Toasts from '@/components/Toasts.vue'
import UploadPanel from '@/features/upload/UploadPanel.vue'
import QuotaBar from '@/components/QuotaBar.vue'

const auth = useAuth()
const route = useRoute()
const router = useRouter()
const bare = computed(() => route.meta.bare === true)
const navOpen = ref(false)
provide('openNav', () => (navOpen.value = true)) // PageHeader shows the menu button on phones

// Desktop sidebar can collapse to an icon rail; the mobile drawer always shows labels.
const readCollapsed = () => { try { return localStorage.getItem('ec.sidebar') === 'collapsed' } catch { return false } }
const collapsed = ref(readCollapsed())
watch(collapsed, (c) => { try { localStorage.setItem('ec.sidebar', c ? 'collapsed' : 'expanded') } catch { /* private mode */ } })
const desktop = useMediaQuery('(min-width: 768px)')
const isCollapsed = computed(() => collapsed.value && desktop.value)

watch(() => auth.user, (u) => (u && !route.meta.anonymous ? realtime.start() : realtime.stop()), { immediate: true })
watch(() => route.fullPath, () => (navOpen.value = false))

async function logout() { await auth.logout(); router.replace('/login') }

const nav = [
  { to: '/files', label: 'nav.files', icon: FolderOpen },
  { to: '/shares', label: 'nav.shares', icon: Share2 },
  { to: '/trash', label: 'nav.trash', icon: Trash2 },
  { to: '/account', label: 'nav.account', icon: CircleUserRound },
]
const adminNav = [
  { to: '/admin/dashboard', label: 'nav.dashboard', icon: LayoutDashboard },
  { to: '/admin/users', label: 'nav.users', icon: Users },
  { to: '/admin/groups', label: 'nav.groups', icon: Building2 },
  { to: '/admin/grants', label: 'nav.grants', icon: ShieldCheck },
  { to: '/admin/audit', label: 'nav.audit', icon: ScrollText },
  { to: '/admin/shares', label: 'nav.allShares', icon: Globe },
  { to: '/admin/webdav', label: 'nav.webdav', icon: HardDrive },
  { to: '/admin/plugins', label: 'nav.plugins', icon: Puzzle },
  { to: '/admin/settings', label: 'nav.settings', icon: Settings },
]
</script>

<template>
  <RouterView v-if="bare" />
  <div v-else class="flex h-screen">
    <aside
      class="fixed inset-y-0 left-0 z-30 flex w-56 flex-col border-r border-[var(--border)] bg-[var(--surface)] py-3 transition-[transform,width] duration-200 ease-out md:static md:translate-x-0"
      :class="[navOpen ? 'translate-x-0' : '-translate-x-full', isCollapsed ? 'md:w-[3.75rem] px-2' : 'px-3']"
    >
      <!-- Brand + collapse toggle. Collapsed, the logo turns into the expand button on hover. -->
      <div v-if="!isCollapsed" class="flex h-9 items-center gap-2 pl-2">
        <Cloud class="h-6 w-6 shrink-0 text-brand-600" fill="currentColor" :stroke-width="1.5" />
        <span class="flex-1 truncate text-lg font-semibold">EasyCloud</span>
        <button class="btn btn-sm hidden !border-transparent !bg-transparent !px-1.5 text-[var(--text-muted)] hover:!bg-[var(--surface-muted)] hover:text-[var(--text)] md:inline-flex"
          :title="$t('nav.collapse')" :aria-label="$t('nav.collapse')" @click="collapsed = true"><PanelLeftClose class="h-4 w-4" /></button>
      </div>
      <button v-else class="group mx-auto flex h-9 w-9 items-center justify-center rounded-lg hover:bg-[var(--surface-muted)]"
        :title="$t('nav.expand')" :aria-label="$t('nav.expand')" @click="collapsed = false">
        <Cloud class="h-6 w-6 text-brand-600 group-hover:hidden" fill="currentColor" :stroke-width="1.5" />
        <PanelLeftOpen class="hidden h-4 w-4 text-[var(--text-muted)] group-hover:block" />
      </button>

      <nav class="no-scrollbar mt-4 flex flex-col gap-1 overflow-y-auto overflow-x-hidden">
        <RouterLink
          v-for="n in nav" :key="n.to" :to="n.to" :title="isCollapsed ? $t(n.label) : undefined"
          class="flex h-9 items-center gap-2 rounded-lg text-sm hover:bg-[var(--surface-muted)]" :class="isCollapsed ? 'justify-center' : 'px-3'"
          active-class="!bg-brand-50 text-brand-700"
        ><component :is="n.icon" class="h-4 w-4 shrink-0 opacity-80" :stroke-width="1.75" aria-hidden="true" /><span :class="isCollapsed ? 'sr-only' : 'truncate'">{{ $t(n.label) }}</span></RouterLink>
        <template v-if="auth.user?.isAdmin">
          <p v-if="!isCollapsed" class="muted mt-4 px-3 text-xs">{{ $t('nav.admin') }}</p>
          <hr v-else class="mx-2 my-2 border-[var(--border)]" />
          <RouterLink
            v-for="n in adminNav" :key="n.to" :to="n.to" :title="isCollapsed ? $t(n.label) : undefined"
            class="flex h-9 items-center gap-2 rounded-lg text-sm hover:bg-[var(--surface-muted)]" :class="isCollapsed ? 'justify-center' : 'px-3'"
            active-class="!bg-brand-50 text-brand-700"
          ><component :is="n.icon" class="h-4 w-4 shrink-0 opacity-80" :stroke-width="1.75" aria-hidden="true" /><span :class="isCollapsed ? 'sr-only' : 'truncate'">{{ $t(n.label) }}</span></RouterLink>
        </template>
      </nav>

      <div class="mt-auto flex flex-col gap-2 pt-3">
        <template v-if="!isCollapsed">
          <QuotaBar />
          <div class="flex items-center gap-1.5 px-1 text-sm">
            <span class="min-w-0 flex-1 truncate">{{ auth.user?.displayName }}</span>
            <ThemeToggle />
            <button class="btn btn-sm" @click="logout">{{ $t('nav.signOut') }}</button>
          </div>
        </template>
        <div v-else class="flex flex-col items-center gap-1.5">
          <ThemeToggle />
          <button class="btn btn-sm !px-1.5" :title="$t('nav.signOut')" :aria-label="$t('nav.signOut')" @click="logout"><LogOut class="h-3.5 w-3.5" /></button>
        </div>
      </div>
    </aside>
    <div v-if="navOpen" class="fixed inset-0 z-20 bg-black/20 md:hidden" @click="navOpen = false" />
    <main class="flex min-w-0 flex-1 flex-col px-4 pb-4 pt-1 md:px-6 md:pb-5 md:pt-2">
      <div class="-m-1 min-h-0 flex-1 overflow-auto p-1">
        <RouterView v-slot="{ Component }">
          <Transition name="fade" mode="out-in"><component :is="Component" /></Transition>
        </RouterView>
      </div>
    </main>
    <UploadPanel />
  </div>
  <Toasts />
</template>
