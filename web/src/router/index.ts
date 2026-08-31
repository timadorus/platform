import { createRouter, createWebHistory } from 'vue-router'

const router = createRouter({
  history: createWebHistory(),
  routes: [
    {
      path: '/login',
      name: 'login-callback',
      component: () => import('@/views/LoginCallbackView.vue'),
    },
    {
      path: '/',
      name: 'universe-picker',
      component: () => import('@/views/UniversePickerView.vue'),
    },
    {
      path: '/universes/:universeId',
      name: 'campaign-picker',
      component: () => import('@/views/CampaignPickerView.vue'),
      props: true,
    },
    {
      path: '/universes/:universeId/manage',
      name: 'universe-overview',
      component: () => import('@/views/UniverseOverviewPanel.vue'),
      props: true,
    },
    {
      path: '/universes/:universeId/campaigns/:campaignId',
      name: 'workspace',
      component: () => import('@/views/WorkspaceView.vue'),
      props: true,
      children: [
        {
          path: '',
          name: 'campaign-overview',
          component: () => import('@/views/CampaignOverviewPanel.vue'),
        },
        {
          path: 'characters/:characterId',
          name: 'character-detail',
          component: () => import('@/views/CharacterDetailView.vue'),
          props: true,
        },
        {
          path: 'entities/:entityId',
          name: 'entity-detail',
          component: () => import('@/views/EntityDetailView.vue'),
          props: true,
        },
        {
          path: 'objects/:objectId',
          name: 'object-detail',
          component: () => import('@/views/ObjectDetailView.vue'),
          props: true,
        },
      ],
    },
    {
      path: '/users',
      name: 'users-admin',
      component: () => import('@/views/UsersAdminView.vue'),
    },
  ],
})

export default router
