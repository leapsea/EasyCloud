import { createRouter, createWebHistory } from 'vue-router'
import { useAuth } from '@/stores/auth'

export const router = createRouter({
  history: createWebHistory(),
  routes: [
    { path: '/', redirect: '/files' },
    { path: '/login', component: () => import('@/features/auth/LoginView.vue'), meta: { bare: true, public: true } },
    { path: '/files', component: () => import('@/features/files/FilesView.vue') },
    { path: '/category/:kind(image|video|audio|doc|other)', component: () => import('@/features/category/CategoryView.vue') },
    { path: '/trash', component: () => import('@/features/trash/TrashView.vue') },
    { path: '/account', component: () => import('@/features/account/AccountView.vue') },
    { path: '/shares', component: () => import('@/features/share/MySharesView.vue') },
    { path: '/s/:id', component: () => import('@/features/share/PublicShareView.vue'), meta: { bare: true, public: true, anonymous: true } },
    { path: '/admin/shares', component: () => import('@/features/share/MySharesView.vue'), meta: { admin: true } },
    { path: '/admin', redirect: '/admin/dashboard' },
    { path: '/admin/dashboard', component: () => import('@/features/admin/DashboardView.vue'), meta: { admin: true } },
    { path: '/admin/users', component: () => import('@/features/admin/UsersView.vue'), meta: { admin: true } },
    { path: '/admin/groups', component: () => import('@/features/admin/GroupsView.vue'), meta: { admin: true } },
    { path: '/admin/grants', component: () => import('@/features/admin/GrantsView.vue'), meta: { admin: true } },
    { path: '/admin/audit', component: () => import('@/features/admin/AuditView.vue'), meta: { admin: true } },
    { path: '/admin/webdav', component: () => import('@/features/admin/WebDavView.vue'), meta: { admin: true } },
    { path: '/admin/plugins', component: () => import('@/features/admin/PluginsView.vue'), meta: { admin: true } },
    { path: '/office', component: () => import('@/features/office/OfficeView.vue'), meta: { bare: true } },
    { path: '/admin/settings', component: () => import('@/features/admin/SettingsView.vue'), meta: { admin: true } },
    { path: '/:pathMatch(.*)*', redirect: '/files' },
  ],
})

router.beforeEach(async (to) => {
  const auth = useAuth()
  if (to.meta.anonymous) return true // share pages never require or check a login
  if (!auth.checked) await auth.check()
  if (to.meta.public) return auth.user && to.path === '/login' ? '/files' : true
  if (!auth.user) return { path: '/login', query: { next: to.fullPath } }
  if (to.meta.admin && !auth.user.isAdmin) return '/files'
  return true
})
