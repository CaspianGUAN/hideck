<script setup lang="ts">
import {
  Delete24Regular,
  Edit24Regular,
  Earth24Regular,
  Link24Regular
} from '@vicons/fluent'
import type { UpstreamProxyPresentation } from '../../utils/proxyPresentation'
import ProxyInventoryShell from './ProxyInventoryShell.vue'
import ProxyStatusBadge from './ProxyStatusBadge.vue'

defineProps<{
  loading: boolean
  refreshing: boolean
  rows: readonly UpstreamProxyPresentation[]
}>()

const autoProbe = defineModel<boolean>('autoProbe', { required: true })

defineEmits<{
  add: []
  delete: [id: string]
  edit: [id: string]
  refresh: []
  rules: [id: string]
}>()
</script>

<template>
  <ProxyInventoryShell
    add-label="新增代理"
    :empty="rows.length === 0"
    empty-subtitle="创建 Socks5 前置代理后，按 SIM 归属国家配置 VoWiFi 路由。同一国家可绑多条，未配置则直连。"
    empty-title="暂无前置代理"
    kicker="ROAMING PROXY INVENTORY"
    :loading="loading"
    :refreshing="refreshing"
    subtitle="SIM 国家规则用于选择代理；出口国家由 Cloudflare HTTPS 实测，UDP 路径可能不同。VoWiFi 仍由实际 ePDG/IKE 建链确认。"
    title="漫游前置代理"
    title-id="upstream-inventory-title"
    tone="communication"
    @add="$emit('add')"
    @refresh="$emit('refresh')"
  >
    <template #icon><el-icon><Earth24Regular /></el-icon></template>

    <div class="proxy-probe-toolbar">
      <label><input v-model="autoProbe" type="checkbox" />每分钟自动检测</label>
      <span>{{ autoProbe ? '打开此页时更新；关闭页面后停止' : '自动检测已暂停，可点击刷新重新检测' }}</span>
    </div>

    <div class="proxy-table-wrap">
      <table class="proxy-inventory-table">
        <thead>
          <tr>
            <th scope="col">代理名称</th>
            <th scope="col">地址（SOCKS5）</th>
            <th scope="col">启用状态</th>
            <th scope="col">公共 DNS UDP</th>
            <th scope="col">实测出口（HTTPS）</th>
            <th scope="col">SIM 国家规则</th>
            <th scope="col"><span class="sr-only">操作</span></th>
          </tr>
        </thead>
        <tbody>
          <tr v-for="row in rows" :key="row.id">
            <td data-label="代理名称">
              <strong>{{ row.name }}</strong>
              <small class="proxy-row-id">{{ row.id || '无 ID' }}</small>
            </td>
            <td data-label="地址（SOCKS5）">
              <code>{{ row.address }}</code>
              <small class="proxy-observation-meta">{{ row.authenticationLabel }}</small>
            </td>
            <td data-label="启用状态">
              <ProxyStatusBadge :label="row.enabledLabel" :tone="row.enabledTone" />
            </td>
            <td data-label="UDP 健康">
              <ProxyStatusBadge
                :detail="row.healthDetail"
                :label="row.healthLabel"
                :tone="row.healthTone"
              />
              <small v-if="row.healthTone === 'danger'" class="proxy-health-detail">
                {{ row.healthDetail }}
              </small>
            </td>
            <td data-label="实测出口（HTTPS）">
              <ProxyStatusBadge :label="row.egress.label" :tone="row.egress.tone" :detail="row.egress.detail" />
              <code v-if="row.egress.ip" class="proxy-egress-ip">{{ row.egress.ip }}</code>
              <small v-if="row.egress.checkedAt" class="proxy-observation-meta">检测于 {{ row.egress.checkedAt }}</small>
              <small v-if="row.egress.tone === 'warning'" class="proxy-egress-warning">{{ row.egress.detail }}</small>
              <small v-if="row.egress.change" class="proxy-egress-warning">最近出口变化：{{ row.egress.change }}</small>
            </td>
            <td data-label="SIM 国家规则">
              <button
                type="button"
                class="proxy-rule-button"
                :aria-label="`查看 ${row.name} 的 ${row.ruleCount} 条国家规则`"
                @click="$emit('rules', row.id)"
              >
                <Link24Regular aria-hidden="true" />
                <span>{{ row.ruleCount }}</span>
                <small>条规则</small>
              </button>
              <small v-if="row.ruleCountries" class="proxy-observation-meta">{{ row.ruleCountries }}</small>
            </td>
            <td data-label="操作">
              <span class="proxy-row-actions">
                <button type="button" :aria-label="`编辑 ${row.name}`" :title="`编辑 ${row.name}`" @click="$emit('edit', row.id)">
                  <Edit24Regular aria-hidden="true" />
                </button>
                <button type="button" class="is-danger" :aria-label="`删除 ${row.name}`" :title="`删除 ${row.name}`" @click="$emit('delete', row.id)">
                  <Delete24Regular aria-hidden="true" />
                </button>
              </span>
            </td>
          </tr>
        </tbody>
      </table>
    </div>
  </ProxyInventoryShell>
</template>

<style scoped>
.proxy-table-wrap { min-width: 0; }
.proxy-probe-toolbar { padding: 8px 16px; display: flex; flex-wrap: wrap; align-items: center; gap: 8px 16px; border-bottom: 1px solid var(--ui-border); color: var(--ui-text-muted); font-size: var(--ui-font-body-sm); }
.proxy-probe-toolbar label { min-height: 44px; display: inline-flex; align-items: center; gap: 8px; color: var(--ui-text); cursor: pointer; }
.proxy-probe-toolbar input { width: 18px; height: 18px; accent-color: var(--ui-communication); }
.proxy-observation-meta { display: block; margin-top: 5px; color: var(--ui-text-muted); font-size: var(--ui-font-caption); line-height: 1.5; }
.proxy-egress-ip { display: block; margin-top: 6px; }
.proxy-egress-warning { display: block; margin-top: 6px; color: var(--ui-warning); font-size: var(--ui-font-caption); line-height: 1.5; }
.proxy-inventory-table { width: 100%; border-collapse: collapse; table-layout: fixed; }
.proxy-inventory-table th { height: 40px; padding: 0 12px; border-bottom: 1px solid var(--ui-border); color: var(--ui-text-muted); font-size: var(--ui-font-caption); font-weight: 600; text-align: left; }
.proxy-inventory-table td { min-width: 0; min-height: 58px; padding: 11px 12px; border-bottom: 1px solid var(--ui-border-muted); color: var(--ui-text); font-size: var(--ui-font-body-sm); vertical-align: middle; overflow-wrap: anywhere; }
.proxy-inventory-table tr:last-child td { border-bottom: 0; }
.proxy-inventory-table th:nth-child(1) { width: 12%; }
.proxy-inventory-table th:nth-child(2) { width: 18%; }
.proxy-inventory-table th:nth-child(3) { width: 10%; }
.proxy-inventory-table th:nth-child(4) { width: 16%; }
.proxy-inventory-table th:nth-child(5) { width: 23%; }
.proxy-inventory-table th:nth-child(6) { width: 12%; }
.proxy-inventory-table th:nth-child(7) { width: 96px; }
.proxy-inventory-table strong { display: block; font-weight: 650; }
.proxy-row-id { display: block; margin-top: 3px; color: var(--ui-text-muted); font: var(--ui-font-caption) "v-mono", ui-monospace, monospace; }
.proxy-health-detail { max-width: 100%; margin-top: 5px; display: -webkit-box; overflow: hidden; color: var(--ui-danger); font-size: var(--ui-font-caption); line-height: 1.35; -webkit-box-orient: vertical; -webkit-line-clamp: 2; }
.proxy-inventory-table code { color: var(--ui-text); font: var(--ui-font-body-sm)/1.5 "v-mono", ui-monospace, monospace; }
.proxy-rule-button { min-height: 34px; padding: 0 8px; display: inline-flex; align-items: center; gap: 5px; border: 1px solid transparent; border-radius: var(--ui-radius-sm); background: transparent; color: var(--ui-communication); cursor: pointer; }
.proxy-rule-button:hover,
.proxy-rule-button:focus-visible { border-color: var(--ui-border); background: var(--ui-surface-muted); }
.proxy-rule-button svg { width: 15px; height: 15px; }
.proxy-rule-button small { color: var(--ui-text-muted); font-size: var(--ui-font-caption); }
.proxy-row-actions { display: flex; justify-content: flex-end; gap: 4px; }
.proxy-row-actions button { width: 36px; height: 36px; display: grid; place-items: center; border: 1px solid transparent; border-radius: var(--ui-radius-sm); background: transparent; color: var(--ui-text-muted); cursor: pointer; }
.proxy-row-actions button:hover,
.proxy-row-actions button:focus-visible { border-color: var(--ui-border); background: var(--ui-surface-muted); color: var(--ui-text); }
.proxy-row-actions button.is-danger:hover,
.proxy-row-actions button.is-danger:focus-visible { color: var(--ui-danger); }
.proxy-row-actions svg { width: 17px; height: 17px; }

@media (max-width: 900px) {
  .proxy-inventory-table thead { display: none; }
  .proxy-inventory-table,
  .proxy-inventory-table tbody { display: grid; gap: 10px; }
  .proxy-inventory-table tr { margin: 0 12px; display: grid; grid-template-columns: repeat(2, minmax(0, 1fr)); border: 1px solid var(--ui-border); border-radius: var(--ui-radius-lg); overflow: hidden; background: var(--ui-surface-strong); }
  .proxy-inventory-table tr:first-child { margin-top: 12px; }
  .proxy-inventory-table tr:last-child { margin-bottom: 12px; }
  .proxy-inventory-table td { min-height: 66px; padding: 11px 12px; display: grid; align-content: center; gap: 6px; border-bottom: 1px solid var(--ui-border-muted) !important; }
  .proxy-inventory-table td:nth-child(odd) { border-right: 1px solid var(--ui-border-muted); }
  .proxy-inventory-table td::before { content: attr(data-label); color: var(--ui-text-muted); font: 700 var(--ui-font-caption) "v-mono", ui-monospace, monospace; letter-spacing: .06em; }
  .proxy-inventory-table td:nth-last-child(-n+2) { border-bottom: 0 !important; }
  .proxy-inventory-table td:last-child { border-right: 0; }
  .proxy-rule-button { min-height: 44px; width: fit-content; padding-inline: 0; }
  .proxy-row-actions { justify-content: flex-start; }
  .proxy-row-actions button { width: 44px; height: 44px; }
}

@media (max-width: 460px) {
  .proxy-inventory-table tr { grid-template-columns: minmax(0, 1fr); }
  .proxy-inventory-table td:nth-child(odd) { border-right: 0; }
  .proxy-inventory-table td:nth-last-child(2) { border-bottom: 1px solid var(--ui-border-muted) !important; }
}
</style>
