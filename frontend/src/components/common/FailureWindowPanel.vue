<script setup lang="ts">
import { computed } from 'vue'
import { AlertTriangle, CheckCircle2, Clock, TrendingDown } from 'lucide-vue-next'
import RiskBadge from './RiskBadge.vue'
import type { FailureProjection } from '../../types/coverage-evaluation'

const props = defineProps<{
  projection: FailureProjection | string | null | undefined
  windowDays?: number
  compact?: boolean
}>()

function parseProjection(): FailureProjection | null {
  if (!props.projection) return null
  try {
    const value = typeof props.projection === 'string'
      ? JSON.parse(props.projection) as FailureProjection | null
      : props.projection
    // Evaluations frozen before the failure-window feature persist an empty
    // object; render them as "no prediction carried" rather than a 0 day window.
    if (!value || !value.window_days) return null
    return value
  } catch { return null }
}

const projection = computed(() => parseProjection())
const days = computed(() => projection.value?.window_days ?? props.windowDays ?? 0)
const earliest = computed(() => projection.value?.earliest_escalation)
const events = computed(() => projection.value?.events ?? [])
const formatDate = (value: string) => new Date(value).toLocaleDateString('zh-CN', { year: 'numeric', month: '2-digit', day: '2-digit' })
const formatDateTime = (value: string) => new Date(value).toLocaleString('zh-CN', { year: 'numeric', month: '2-digit', day: '2-digit', hour: '2-digit', minute: '2-digit' })
const safeguardLabel = (ids: number[]) => ids.map((id) => `#${id}`).join('、')
</script>

<template>
  <section class="failure-window" :class="{ escalated: earliest }">
    <div class="failure-window-heading">
      <div class="failure-window-title">
        <Clock :size="16" />
        <div>
          <p class="eyebrow">FUTURE FAILURE WINDOW</p>
          <h3>未来失效窗口 · {{ days }} 天</h3>
        </div>
      </div>
      <span v-if="projection" class="window-range">至 {{ formatDate(projection.window_end) }}</span>
    </div>

    <div v-if="!projection" class="failure-none">
      <CheckCircle2 :size="17" />
      <span>该评估未携带失效窗口预测。</span>
    </div>

    <template v-else>
      <article v-if="earliest" class="escalation-band">
        <header><AlertTriangle :size="17" /><strong>最早风险等级上升</strong></header>
        <dl>
          <div><dt>日期</dt><dd>{{ formatDate(earliest.expires_at) }}（冻结点后第 {{ earliest.days_from_reference }} 天）</dd></div>
          <div><dt>到期保护层</dt><dd>{{ safeguardLabel(earliest.expiring_safeguard_ids) }}</dd></div>
          <div>
            <dt>风险等级</dt>
            <dd><RiskBadge :rank="earliest.risk_rank_before" /><span class="rank-arrow">→</span><RiskBadge :rank="earliest.risk_rank_after" /></dd>
          </div>
          <div><dt>覆盖分</dt><dd>{{ earliest.coverage_score }} / 100</dd></div>
          <div class="gap-block">
            <dt>缺口路径（{{ earliest.uncovered_paths.length }}）</dt>
            <dd>
              <ol>
                <li v-for="(path, index) in earliest.uncovered_paths" :key="index">
                  <strong>{{ path.path_id || `P-${index + 1}` }}</strong>
                  <span>{{ path.node_code ? `${path.node_code} · ` : '' }}{{ path.cause }} → {{ path.consequence }}</span>
                </li>
              </ol>
            </dd>
          </div>
        </dl>
      </article>

      <article v-else class="no-escalation-band">
        <CheckCircle2 :size="17" />
        <div>
          <strong>窗口内风险等级不上升：无</strong>
          <p>未来 {{ days }} 天内保护层到期不会使残余风险等级超过冻结点结果。</p>
        </div>
      </article>

      <div v-if="events.length" class="window-end-state">
        <TrendingDown :size="14" />
        <span>窗口末日预测：覆盖分 <strong>{{ projection.window_end_coverage_score }}</strong> · 残余风险 <RiskBadge :rank="projection.window_end_rank_after" /></span>
      </div>

      <div v-if="!compact && events.length" class="failure-events">
        <p class="eyebrow">EXPIRY TIMELINE</p>
        <ol>
          <li v-for="(event, index) in events" :key="index" :class="{ rises: event.risk_rank_rises }">
            <span class="event-date">{{ formatDateTime(event.expires_at) }}</span>
            <span class="event-offset">+{{ event.days_from_reference }} 天</span>
            <span class="event-layers">{{ safeguardLabel(event.expiring_safeguard_ids) }}</span>
            <span class="event-score">{{ event.coverage_score }} 分</span>
            <RiskBadge :rank="event.risk_rank_after" />
            <span class="event-flag">
              <template v-if="event.risk_rank_rises"><AlertTriangle :size="13" />等级上升</template>
              <template v-else><CheckCircle2 :size="13" />等级不变</template>
            </span>
          </li>
        </ol>
      </div>
    </template>
  </section>
</template>
