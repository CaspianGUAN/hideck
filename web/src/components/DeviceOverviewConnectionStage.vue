<script setup lang="ts">
import { computed } from 'vue'
import {
  Cellular4G24Regular,
  CellularData124Regular,
  Checkmark12Regular,
  Dismiss12Regular,
  Subtract12Regular,
  Wifi124Regular
} from '@vicons/fluent'
import type { DeviceOverviewItem } from '../types/api'
import { createOverviewConnectionPresentation } from '../utils/overviewConnectionPresentation'

const props = defineProps<{
  device: DeviceOverviewItem | null
}>()

const presentation = computed(() => createOverviewConnectionPresentation(props.device))
const stages = computed(() => presentation.value.stages)
const metrics = computed(() => presentation.value.metrics)
const pathIsFlowing = computed(() => presentation.value.pathIsFlowing)

function stageLabel(ready: boolean | undefined): string {
  if (ready === true) return '已就绪'
  if (ready === false) return '失败'
  return '等待状态'
}
</script>

<template>
  <section
    class="overview-connection-stage"
    :class="presentation.tone"
    aria-label="通话连接状态"
  >
    <div class="overview-connection-main">
      <span class="overview-eyebrow">{{ presentation.eyebrow }}</span>
      <h2>
        <el-icon aria-hidden="true">
          <Wifi124Regular v-if="presentation.kind === 'wifi'" />
          <Cellular4G24Regular v-else-if="presentation.kind === 'volte'" />
          <CellularData124Regular v-else />
        </el-icon>
        {{ presentation.title }}
      </h2>
      <p>{{ presentation.detail }}</p>

      <div
        class="overview-service-path"
        :class="{ 'is-flowing': pathIsFlowing }"
        :style="{ gridTemplateColumns: `repeat(${stages.length}, minmax(0, 1fr))` }"
        aria-label="通话服务链路"
      >
        <div class="overview-service-track" aria-hidden="true"><span /></div>
        <div
          v-for="stage in stages"
          :key="stage.key"
          class="overview-service-step"
          :class="{ 'is-ready': stage.ready === true, 'is-failed': stage.ready === false }"
          :aria-label="`${stage.key}：${stage.pending ? '待验证' : stageLabel(stage.ready)}`"
        >
          <i aria-hidden="true">
            <Checkmark12Regular v-if="stage.ready === true" />
            <Dismiss12Regular v-else-if="stage.ready === false" />
            <Subtract12Regular v-else />
          </i>
          <small>{{ stage.key }}<template v-if="stage.pending"> · 待验证</template></small>
        </div>
      </div>
    </div>

    <dl class="overview-connection-metrics">
      <div v-for="metric in metrics" :key="metric.label">
        <dt>{{ metric.label }}</dt>
        <dd :title="metric.value">{{ metric.value }}</dd>
        <small v-if="metric.hint">{{ metric.hint }}</small>
      </div>
    </dl>
  </section>
</template>

<style scoped>
.overview-connection-stage {
  min-height: 268px;
  padding: 26px;
  display: grid;
  grid-template-columns: minmax(0, 1fr) minmax(250px, 36%);
  gap: 26px;
  overflow: hidden;
  border: 1px solid var(--ui-border);
  border-radius: var(--ui-radius-xl);
  background:
    radial-gradient(circle at 72% 35%, color-mix(in srgb, var(--ui-primary) 10%, transparent), transparent 34%),
    var(--ui-surface-strong);
}

.overview-connection-main {
  min-width: 0;
}

.overview-eyebrow {
  color: var(--ui-primary);
  font: 700 var(--ui-font-caption) "v-mono", ui-monospace, monospace;
  letter-spacing: .16em;
}

.overview-connection-stage h2 {
  margin: 12px 0 0;
  display: flex;
  align-items: center;
  gap: 10px;
  color: var(--ui-text);
  font-size: clamp(24px, 3vw, 34px);
  font-weight: 600;
  letter-spacing: -.025em;
}

.overview-connection-stage h2 .el-icon {
  color: var(--ui-primary);
}

.overview-connection-stage > div > p {
  margin: 6px 0 0;
  color: var(--ui-text-muted);
  font-size: var(--ui-font-body-sm);
}

.overview-connection-stage.is-failed h2,
.overview-connection-stage.is-failed h2 .el-icon { color: var(--ui-danger); }
.overview-connection-stage.is-pending h2,
.overview-connection-stage.is-pending h2 .el-icon { color: var(--ui-warning); }
.overview-connection-stage.is-ready h2,
.overview-connection-stage.is-ready h2 .el-icon { color: var(--ui-primary); }

.overview-service-path {
  position: relative;
  max-width: 620px;
  margin-top: 48px;
  display: grid;
  grid-template-columns: repeat(5, minmax(0, 1fr));
  container-type: inline-size;
}

.overview-service-track {
  position: absolute;
  top: 17px;
  right: 9%;
  left: 9%;
  height: 1px;
  background: var(--ui-border);
}

.overview-service-track span {
  position: absolute;
  top: -3px;
  left: 0;
  width: 7px;
  height: 7px;
  border-radius: 50%;
  opacity: 0;
  background: var(--ui-primary);
  box-shadow: 0 0 12px var(--ui-primary);
}

.overview-service-path.is-flowing .overview-service-track span {
  animation: overview-service-flow 2.4s linear infinite;
}

.overview-service-step {
  position: relative;
  z-index: 1;
  min-width: 0;
  display: grid;
  place-items: center;
  gap: 7px;
  color: var(--ui-text-muted);
}

.overview-service-step i {
  width: 36px;
  height: 36px;
  display: grid;
  place-items: center;
  border: 1px solid var(--ui-border);
  border-radius: 50%;
  background: var(--ui-surface);
}

.overview-service-step svg { width: 13px; height: 13px; }
.overview-service-step small { font-size: var(--ui-font-caption); }
.overview-service-step.is-ready { color: var(--ui-primary); }
.overview-service-step.is-ready i { border-color: var(--ui-primary); }
.overview-service-step.is-failed { color: var(--ui-danger); }
.overview-service-step.is-failed i { border-color: var(--ui-danger); }

.overview-connection-metrics {
  margin: 0;
  padding: 4px 0;
  display: grid;
  grid-template-columns: repeat(2, minmax(0, 1fr));
  border: 1px solid var(--ui-border);
  border-radius: var(--ui-radius-xl);
  background: color-mix(in srgb, var(--ui-surface) 82%, transparent);
}

.overview-connection-metrics div {
  min-width: 0;
  padding: 14px 16px;
}

.overview-connection-metrics div:nth-child(odd) { border-right: 1px solid var(--ui-border); }
.overview-connection-metrics div { border-bottom: 1px solid var(--ui-border); }
.overview-connection-metrics div:nth-last-child(-n+2) { border-bottom: 0; }
.overview-connection-metrics dt { color: var(--ui-text-muted); font-size: var(--ui-font-caption); }
.overview-connection-metrics dd { margin: 6px 0 0; color: var(--ui-text); font: 13px "v-mono", ui-monospace, monospace; overflow-wrap: anywhere; }
.overview-connection-metrics small { color: var(--ui-primary); font-size: var(--ui-font-caption); }

@keyframes overview-service-flow {
  0% { opacity: 0; transform: translateX(0); }
  12%, 88% { opacity: 1; }
  100% { opacity: 0; transform: translateX(calc(82cqw - 7px)); }
}

@media (max-width: 860px) {
  .overview-connection-stage { grid-template-columns: minmax(0, 1fr); }
}

@media (max-width: 520px) {
  .overview-connection-stage { min-height: 0; padding: 20px 16px; }
  .overview-service-path { margin-top: 36px; }
  .overview-connection-metrics div { padding: 12px; }
}

@media (prefers-reduced-motion: reduce) {
  .overview-service-path.is-flowing .overview-service-track span {
    animation: none;
    opacity: .65;
  }
}
</style>
