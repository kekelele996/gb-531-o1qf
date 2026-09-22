<script setup lang="ts">
import { computed, onMounted, ref } from 'vue'
import { ElMessage } from 'element-plus'
import { CheckCircle2, AlertTriangle, FileCheck2, GitCompare, LoaderCircle, Network, Play, RefreshCw, XCircle } from 'lucide-vue-next'
import AppShell from '../components/common/AppShell.vue'
import PageHeader from '../components/common/PageHeader.vue'
import RiskBadge from '../components/common/RiskBadge.vue'
import ScenarioStateTimeline from '../components/common/ScenarioStateTimeline.vue'
import EvidenceDrawer from '../components/common/EvidenceDrawer.vue'
import { useCoverageRun } from '../hooks/useCoverageRun'
import { useAuth } from '../hooks/useAuth'
import { useCoverageEvaluationStore } from '../stores/coverage-evaluation'
import { useDeviationScenarioStore } from '../stores/deviation-scenario'
import { useSafeguardStore } from '../stores/safeguard'
import { errorMessage } from '../api/client'
import { coverageStateLabels } from '../types/enums/coverage-state'
import type { CoverageSnapshot, EvaluationExplanation, FailureWindowEvent, PathEvidence, ScoringStep } from '../types/coverage-evaluation'
import { DEFAULT_FAILURE_WINDOW_DAYS, MAX_FAILURE_WINDOW_DAYS, MIN_FAILURE_WINDOW_DAYS, firstEscalation, formatForecastDate, normalizeWindowDays } from '../utils/forecast'

const evaluations = useCoverageEvaluationStore()
const scenarios = useDeviationScenarioStore()
const safeguards = useSafeguardStore()
const { canEdit, canReview } = useAuth()
const runner = useCoverageRun()
const scenarioId = ref<number>()
const failureWindowDays = ref<number>(DEFAULT_FAILURE_WINDOW_DAYS)
const drawer = ref(false)
const compareId = ref<number>()
const selected = computed(() => evaluations.selected)
const scenario = computed(() => scenarios.items.find((x) => x.id === selected.value?.scenario_id || x.id === scenarioId.value))
const scenarioSafeguards = computed(() => safeguards.items.filter((x) => x.target_scenario_id === scenario.value?.id))
const comparable = computed(() => evaluations.items.filter((x) => x.id !== selected.value?.id && x.scenario_id === selected.value?.scenario_id))
const comparison = computed(() => evaluations.items.find((x) => x.id === compareId.value))

function parse<T>(value: T | string, fallback: T): T { if (typeof value !== 'string') return value; try { return JSON.parse(value) as T } catch { return fallback } }
const explanation = computed<EvaluationExplanation>(() => parse(selected.value?.explanation ?? '', { summary: '', paths: [], score_steps: [], deduplicated_safeguards: [], boundary_note: '', reference_time: '' }))
const paths = computed<PathEvidence[]>(() => explanation.value.paths?.length ? explanation.value.paths : parse(selected.value?.uncovered_paths ?? [], [] as PathEvidence[]))
const steps = computed<ScoringStep[]>(() => {
  return explanation.value.score_steps ?? []
})
const inputSnapshot = computed<CoverageSnapshot>(() => parse(selected.value?.input_snapshot ?? '', {} as CoverageSnapshot))
const uncoveredCount = computed(() => paths.value.filter((path) => !(path.covered ?? path.protected)).length)
const scoreClass = computed(() => (selected.value?.coverage_score ?? 0) >= 80 ? 'good' : (selected.value?.coverage_score ?? 0) >= 55 ? 'warning' : 'danger')
const selectedWindowDays = computed(() => normalizeWindowDays(selected.value?.failure_window_days))
const escalation = computed<FailureWindowEvent | undefined>(() => firstEscalation(selected.value))
const forecastEvents = computed(() => selected.value?.failure_forecast?.events ?? [])
const windowEndLabel = computed(() => formatForecastDate(selected.value?.failure_forecast?.window_end))
const comparisonEscalationLabel = (days?: number) => days === undefined ? '无升级' : `第 ${days} 天`
const scenarioLabel = (id: number) => { const item = scenarios.items.find((x) => x.id === id); return item ? `#${item.id} ${item.guideword.toUpperCase()} ${item.parameter}` : `#${id}` }

async function refresh() { try { await Promise.all([evaluations.load(), scenarios.load(), safeguards.load()]); scenarioId.value ??= scenarios.items[0]?.id } catch (error) { ElMessage.error(errorMessage(error)) } }
async function run() {
  if (!scenarioId.value) return ElMessage.warning('请先选择偏差场景')
  const days = normalizeWindowDays(failureWindowDays.value)
  failureWindowDays.value = days
  try {
    const result = await runner.launch(scenarioId.value, days)
    ElMessage.success(result.evaluation_state === 'failed' ? '评估完成但计算失败' : '覆盖评估已生成')
  } catch (error) { ElMessage.error(errorMessage(error)) }
}
async function changeState(kind: 'confirm' | 'void') { if (!selected.value) return; try { kind === 'confirm' ? await evaluations.confirm(selected.value.id) : await evaluations.voidRun(selected.value.id); ElMessage.success(kind === 'confirm' ? '评估已确认' : '评估已作废') } catch (error) { ElMessage.error(errorMessage(error)) } }
onMounted(refresh)
</script>

<template>
  <AppShell>
    <PageHeader eyebrow="DETERMINISTIC COVERAGE REPLAY" title="覆盖推演" description="冻结偏差与保护层输入，构建原因到后果路径，按独立性键去重并保留评分依据；同时推演未来失效窗口，标出最早风险升级日期与缺口。">
      <el-button :loading="evaluations.loading" @click="refresh"><RefreshCw :size="16" />刷新</el-button>
    </PageHeader>
    <section class="run-launcher">
      <div><p class="eyebrow">NEW EVALUATION</p><h2>选择场景并冻结输入</h2><p>每次运行使用唯一幂等键；重复请求返回同一评估。</p></div>
      <div class="launcher-inputs">
        <el-select v-model="scenarioId" placeholder="选择偏差场景"><el-option v-for="item in scenarios.items" :key="item.id" :label="scenarioLabel(item.id)" :value="item.id" /></el-select>
        <el-input-number v-model="failureWindowDays" :min="MIN_FAILURE_WINDOW_DAYS" :max="MAX_FAILURE_WINDOW_DAYS" :step="1" controls-position="right" aria-label="未来失效窗口天数" class="window-input" />
        <small class="window-hint">未来失效窗口（天，{{ MIN_FAILURE_WINDOW_DAYS }}–{{ MAX_FAILURE_WINDOW_DAYS }}，默认 {{ DEFAULT_FAILURE_WINDOW_DAYS }}），随快照冻结</small>
      </div>
      <el-button v-if="canEdit || canReview" type="primary" :loading="runner.running.value" @click="run"><Play :size="16" />运行覆盖评估</el-button>
    </section>
    <section class="run-selector" aria-label="历史评估">
      <button v-for="item in evaluations.items" :key="item.id" :class="{ selected: evaluations.selectedId === item.id }" @click="evaluations.selectedId = item.id">
        <span>#{{ item.id }}</span><strong>{{ scenarioLabel(item.scenario_id) }}</strong><small>{{ item.algorithm_version }} · {{ new Date(item.evaluated_at).toLocaleString('zh-CN') }}</small><span class="state-label" :class="item.evaluation_state">{{ coverageStateLabels[item.evaluation_state] }}</span>
      </button>
      <div v-if="!evaluations.items.length" class="empty-inline">暂无评估历史</div>
    </section>
    <template v-if="selected">
      <section class="coverage-summary" :class="scoreClass">
        <div><p class="eyebrow">COVERAGE SCORE</p><strong class="score-value">{{ Math.round(selected.coverage_score) }}</strong><span>/ 100</span></div>
        <div class="risk-change"><span>评估前<RiskBadge :rank="selected.risk_rank_before" /></span><i>→</i><span>评估后<RiskBadge :rank="selected.risk_rank_after" /></span></div>
        <div><span class="state-label" :class="selected.evaluation_state">{{ coverageStateLabels[selected.evaluation_state] }}</span><small>{{ selected.algorithm_version }}</small></div>
        <div v-if="runner.polling.value" class="polling"><LoaderCircle :size="16" />正在读取计算状态</div>
      </section>
      <section class="failure-window" :class="{ escalating: escalation }">
        <div class="section-heading">
          <div><p class="eyebrow">FUTURE FAILURE WINDOW</p><h2>未来失效窗口 · {{ selectedWindowDays }} 天</h2></div>
          <span>按保护层到期日逐点重算 · 截止 {{ windowEndLabel }}</span>
        </div>
        <article v-if="escalation" class="escalation-band">
          <AlertTriangle :size="22" />
          <div>
            <strong>最早风险升级：第 {{ escalation.horizon_days }} 天 · {{ formatForecastDate(escalation.expires_at) }}</strong>
            <p>
              到期保护层 #{{ escalation.expired_safeguard_ids.join('、#') }}；
              覆盖分降至 {{ Math.round(escalation.coverage_score) }}，残余风险升至
              <RiskBadge :rank="escalation.risk_rank_after" />
            </p>
            <ul class="gap-paths">
              <li v-for="(gap, index) in escalation.uncovered_paths" :key="`gap-${index}`">
                缺口路径 {{ gap.path_id || index + 1 }}：{{ gap.cause }} → {{ gap.consequence }}
                （保护度 {{ Math.round((gap.combined_protection ?? 0) * 100) }}%，{{ gap.reason }}）
              </li>
            </ul>
          </div>
        </article>
        <article v-else class="no-escalation-band">
          <CheckCircle2 :size="22" />
          <div>
            <strong>窗口内无风险等级上升</strong>
            <p>{{ forecastEvents.length ? `未来 ${selectedWindowDays} 天内有 ${forecastEvents.length} 个保护层到期，但残余风险等级保持为 ${selected.risk_rank_after}。` : `未来 ${selectedWindowDays} 天内无在保保护层到期，残余风险等级保持为 ${selected.risk_rank_after}。` }}</p>
          </div>
        </article>
        <ol v-if="forecastEvents.length" class="window-events">
          <li v-for="event in forecastEvents" :key="`event-${event.horizon_days}`" :class="{ raised: event.risk_rank_increased }">
            <span class="event-day">D+{{ event.horizon_days }}</span>
            <span class="event-date">{{ formatForecastDate(event.expires_at) }}</span>
            <span class="event-layers">#{{ event.expired_safeguard_ids.join('、#') }}</span>
            <span class="event-score">{{ Math.round(event.coverage_score) }} 分</span>
            <span class="event-rank"><RiskBadge :rank="event.risk_rank_after" /></span>
            <span class="event-gaps">{{ event.uncovered_paths.length }} 条缺口</span>
          </li>
        </ol>
      </section>
      <ScenarioStateTimeline v-if="scenario" :state="scenario.scenario_state" />
      <div class="coverage-grid">
        <section class="path-workbench">
          <div class="section-heading"><div><p class="eyebrow">CAUSE → CONSEQUENCE PATHS</p><h2>路径与保护缺口</h2></div><span>{{ paths.length }} 条路径 · {{ uncoveredCount }} 条未覆盖</span></div>
          <div class="path-graph">
            <article v-if="!paths.length" class="protected-path"><CheckCircle2 :size="20" /><div><strong>快照中没有可计算路径</strong><span>请检查场景原因、后果与保护层输入。</span></div></article>
            <article v-for="(path, index) in paths" :key="index" class="path-row">
              <div class="path-node cause"><small>原因</small><strong>{{ path.cause }}</strong></div><span class="path-edge"><i />{{ path.safeguard_names?.length || path.safeguard_ids?.length || 0 }} 层 · {{ Math.round((path.combined_protection ?? 0) * 100) }}%</span><div class="path-node consequence"><small>后果</small><strong>{{ path.consequence }}</strong></div><span class="gap-marker" :class="{ covered: path.covered ?? path.protected }"><CheckCircle2 v-if="path.covered ?? path.protected" :size="15" /><XCircle v-else :size="15" />{{ (path.covered ?? path.protected) ? '已覆盖' : '未覆盖' }}</span>
            </article>
          </div>
          <div class="section-heading"><div><p class="eyebrow">SCORING TRACE</p><h2>评分解释</h2></div><span>相同快照可确定性重放</span></div>
          <ol class="scoring-trace"><li v-for="(step, index) in steps" :key="index"><span>{{ step.step ?? index + 1 }}</span><div><strong>{{ step.rule || step.label }} · {{ step.contribution ?? step.value ?? '-' }}</strong><p>{{ step.explanation || step.detail }}<template v-if="step.running_score !== undefined"> · 累计 {{ step.running_score }}</template></p></div></li></ol>
        </section>
        <aside class="coverage-evidence">
          <div class="section-heading"><h2>冻结证据</h2><el-tooltip content="查看完整输入快照"><el-button circle text aria-label="查看输入快照" @click="drawer = true"><FileCheck2 :size="17" /></el-button></el-tooltip></div>
          <dl class="evidence-pairs"><div><dt>场景</dt><dd>{{ scenarioLabel(selected.scenario_id) }}</dd></div><div><dt>保护措施</dt><dd>{{ scenarioSafeguards.length }} 项</dd></div><div><dt>独立性键</dt><dd>{{ new Set(scenarioSafeguards.map((x) => x.independence_key)).size }} 个</dd></div><div><dt>失效窗口</dt><dd>{{ selectedWindowDays }} 天 · 截止 {{ windowEndLabel }}</dd></div><div><dt>输入哈希</dt><dd><code>{{ selected.input_hash || inputSnapshot.input_hash || '见快照' }}</code></dd></div></dl>
          <div v-if="selected.deduplicated_safeguards?.length" class="dedupe-note"><strong>去重措施</strong><span v-for="item in selected.deduplicated_safeguards" :key="`${item.independence_key}-${item.kept_id}`">{{ item.independence_key }}：保留 #{{ item.kept_id }}，忽略 {{ item.ignored_ids.join(', ') }}</span></div>
          <div class="compare-tools"><GitCompare :size="16" /><el-select v-model="compareId" placeholder="选择版本对比" clearable><el-option v-for="item in comparable" :key="item.id" :label="`#${item.id} · ${item.coverage_score} 分 · 窗口 ${item.failure_window_days ?? 30} 天`" :value="item.id" /></el-select></div>
          <div v-if="comparison" class="comparison-band">
            <div><span>覆盖分</span><strong>{{ comparison.coverage_score }} → {{ selected.coverage_score }}</strong></div>
            <div><span>风险级别</span><strong>{{ comparison.risk_rank_after }} → {{ selected.risk_rank_after }}</strong></div>
            <div><span>最早风险升级</span><strong>{{ comparisonEscalationLabel(firstEscalation(comparison)?.horizon_days) }} → {{ comparisonEscalationLabel(escalation?.horizon_days) }}</strong></div>
          </div>
          <div v-if="canReview && selected.evaluation_state === 'completed'" class="review-strip"><strong>人工结论</strong><p>确认仅表示已完成离线证据复核，不代表可执行控制。</p><div><el-button type="success" @click="changeState('confirm')"><CheckCircle2 :size="15" />确认评估</el-button><el-button type="danger" plain @click="changeState('void')"><XCircle :size="15" />作废</el-button></div></div>
        </aside>
      </div>
      <EvidenceDrawer v-model="drawer" title="不可变评估输入快照" :note="`评估 #${selected.id} · ${selected.algorithm_version}`" :evidence="selected.input_snapshot" />
    </template>
    <section v-else class="empty-state"><Network :size="30" /><h2>选择或运行一条评估</h2><p>评分、未覆盖路径和证据快照将在此显示。</p></section>
  </AppShell>
</template>
