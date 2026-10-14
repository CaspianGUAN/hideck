import axios from 'axios'
import { api } from '../stores/auth'
import { callService } from './http'
import type {
  UpstreamProxy,
  UpstreamProxyCountry,
  UpstreamProxyCountryRule,
  UpstreamProxyCountryRulePayload,
  UpstreamProxyProbeResponse
} from '../types/api'

// 前置代理 API 服务层
export const upstreamProxyService = {
  // 列出所有前置代理
  list() {
    return callService(async () => {
      const res = await api.get('/upstream-proxies')
      return (res.data || []) as UpstreamProxy[]
    })
  },

  probe(id: string) {
    return callService(async () => {
      const proxyId = encodeURIComponent(id)
      try {
        const res = await api.post(`/upstream-proxies/${proxyId}/actions/probe`)
        return res.data as UpstreamProxyProbeResponse
      } catch (error) {
        // A completed probe can report UDP failure and still contain a valid
        // HTTPS exit observation. Preserve both, without treating it as healthy.
        if (axios.isAxiosError(error) && error.response?.status === 502) {
          const data = error.response.data as Partial<UpstreamProxyProbeResponse> | undefined
          if (data?.status === 'error' && typeof data.result?.udp_relay_ok === 'boolean') {
            return data as UpstreamProxyProbeResponse
          }
        }
        throw error
      }
    })
  },

  // 新增前置代理
  create(proxy: UpstreamProxy) {
    return callService(async () => {
      const res = await api.post('/upstream-proxies', proxy)
      return res.data as UpstreamProxyProbeResponse
    })
  },

  // 更新前置代理
  update(id: string, proxy: Partial<UpstreamProxy>) {
    return callService(async () => {
      const res = await api.put(`/upstream-proxies/${id}`, proxy)
      return res.data as UpstreamProxyProbeResponse
    })
  },

  // 删除前置代理
  remove(id: string) {
    return callService(async () => {
      await api.delete(`/upstream-proxies/${id}`)
      return true
    })
  },

  // 列出可配置国家及其 MCC 分组
  listCountries() {
    return callService(async () => {
      const res = await api.get('/upstream-proxy-countries')
      return (res.data || []) as UpstreamProxyCountry[]
    })
  },

  // 列出国家代理规则
  listCountryRules() {
    return callService(async () => {
      const res = await api.get('/upstream-proxy-country-rules')
      return (res.data || []) as UpstreamProxyCountryRule[]
    })
  },

  // 配置一个国家的前置代理规则
  upsertCountryRule(countryCode: string, payload: UpstreamProxyCountryRulePayload) {
    return callService(async () => {
      const res = await api.put(`/upstream-proxy-country-rules/${encodeURIComponent(countryCode)}`, payload)
      return res.data as UpstreamProxyCountryRule
    })
  },

  // 删除某条代理上的国家规则；该国没有其它节点时恢复直连
  deleteCountryRule(countryCode: string, proxyId = '') {
    return callService(async () => {
      await api.delete(`/upstream-proxy-country-rules/${encodeURIComponent(countryCode)}`, {
        params: proxyId ? { upstream_proxy_id: proxyId } : undefined
      })
      return true
    })
  }
}
