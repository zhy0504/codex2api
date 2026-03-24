import { useCallback, useEffect } from 'react'
import { Link } from 'react-router-dom'
import {
  Activity,
  AlertTriangle,
  ArrowRight,
  BarChart3,
  Clock3,
  Cpu,
  Database,
  HardDrive,
  RefreshCw,
  Server,
  Users,
  Workflow,
  Zap,
} from 'lucide-react'
import { api } from '../api'
import PageHeader from '../components/PageHeader'
import StateShell from '../components/StateShell'
import { useDataLoader } from '../hooks/useDataLoader'
import type { OpsOverviewResponse } from '../types'
import { Button } from '@/components/ui/button'
import { Card, CardContent } from '@/components/ui/card'

type MetricTone = 'normal' | 'warning' | 'danger' | 'info'

export default function Operations() {
  const loadOperationsData = useCallback(() => api.getOpsOverview(), [])

  const { data: overview, loading, error, reload, reloadSilently } = useDataLoader<OpsOverviewResponse | null>({
    initialData: null,
    load: loadOperationsData,
  })

  useEffect(() => {
    const timer = window.setInterval(() => {
      void reloadSilently()
    }, 15000)

    return () => window.clearInterval(timer)
  }, [reloadSilently])

  const updatedLabel = overview?.updated_at ? formatTimeLabel(overview.updated_at) : '--:--:--'

  return (
    <StateShell
      variant="page"
      loading={loading}
      error={error}
      onRetry={() => void reload()}
      loadingTitle="正在加载系统运维"
      loadingDescription="服务状态、连接池和实时流量正在同步。"
      errorTitle="系统运维页加载失败"
    >
      <>
        <PageHeader
          title="系统运维"
          description="查看服务运行状态、连接池压力和实时流量表现。"
          actions={
            <div className="flex items-center gap-3 max-sm:w-full max-sm:flex-col max-sm:items-stretch">
              <span className="text-sm text-muted-foreground max-sm:text-center">最后更新时间：{updatedLabel}</span>
              <Button variant="outline" onClick={() => void reload()}>
                <RefreshCw className="size-3.5" />
                刷新
              </Button>
            </div>
          }
        />

        {overview ? (
          <>
            <div className="grid grid-cols-[repeat(auto-fit,minmax(220px,1fr))] gap-4 mb-6">
              <SummaryPill label="运行时长" value={formatUptime(overview.uptime_seconds)} />
              <SummaryPill label="账号池" value={`${overview.runtime.available_accounts} / ${overview.runtime.total_accounts}`} />
              <SummaryPill label="今日请求" value={formatNumber(overview.traffic.today_requests)} />
              <SummaryPill label="今日错误率" value={`${overview.traffic.error_rate.toFixed(1)}%`} />
            </div>

            <Card className="mb-6 overflow-hidden border-primary/15 bg-[radial-gradient(circle_at_top_left,rgba(99,102,241,0.10),transparent_55%),linear-gradient(135deg,rgba(255,255,255,0.92),rgba(245,247,255,0.78))]">
              <CardContent className="p-6">
                <div className="flex items-start justify-between gap-5 max-lg:flex-col">
                  <div className="max-w-[720px]">
                    <div className="inline-flex items-center gap-2 rounded-full border border-primary/15 bg-primary/8 px-3 py-1 text-[12px] font-semibold text-primary">
                      <Workflow className="size-3.5" />
                      调度板块已独立
                    </div>
                    <h3 className="mt-4 text-[26px] font-semibold tracking-tight text-foreground">调度面板</h3>
                    <p className="mt-2 text-sm leading-7 text-muted-foreground">
                      账号健康分层、调度打分、风险账号筛选与近期 401 / 429 / Timeout 观察位，已经拆到独立界面。
                      现在系统运维页只保留资源、连接池和流量概览，避免不同视角混在同一个页面里。
                    </p>
                    <div className="mt-4 flex flex-wrap gap-2 text-[12px] font-semibold text-muted-foreground">
                      <span className="rounded-full border border-border bg-white/70 px-3 py-1">独立筛选</span>
                      <span className="rounded-full border border-border bg-white/70 px-3 py-1">风险优先排序</span>
                      <span className="rounded-full border border-border bg-white/70 px-3 py-1">15 秒自动刷新</span>
                    </div>
                  </div>
                  <div className="flex w-full max-w-[220px] flex-col gap-3 max-lg:max-w-none max-sm:w-full">
                    <Button asChild className="w-full">
                      <Link to="/ops/scheduler">
                        打开调度面板
                        <ArrowRight className="size-4" />
                      </Link>
                    </Button>
                    <span className="text-xs leading-6 text-muted-foreground">
                      适合排查当前号池风险分布、近期异常和调度得分变化。
                    </span>
                  </div>
                </div>
              </CardContent>
            </Card>

            <Card>
              <CardContent className="p-6">
                <div className="mb-5 flex items-center justify-between gap-4">
                  <div>
                    <h3 className="text-base font-semibold text-foreground">系统概览</h3>
                    <p className="mt-1 text-sm text-muted-foreground">按 15 秒自动刷新，适合快速查看当前服务健康度。</p>
                  </div>
                </div>

                <div className="grid grid-cols-1 gap-4 sm:grid-cols-2 lg:grid-cols-3 xl:grid-cols-5">
                  <OpsMetricCard
                    label="CPU"
                    value={`${overview.cpu.percent.toFixed(1)}%`}
                    sub={`核心数 ${overview.cpu.cores} 核`}
                    icon={<Cpu className="size-5" />}
                    tone={getPercentTone(overview.cpu.percent, 70, 90)}
                  />
                  <OpsMetricCard
                    label="内存"
                    value={`${overview.memory.percent.toFixed(1)}%`}
                    sub={`使用 ${formatBytes(overview.memory.used_bytes)} / ${formatBytes(overview.memory.total_bytes)}`}
                    icon={<HardDrive className="size-5" />}
                    tone={getPercentTone(overview.memory.percent, 75, 90)}
                  />
                  <OpsMetricCard
                    label="PostgreSQL"
                    value={`${overview.postgres.usage_percent.toFixed(1)}%`}
                    sub={`连接 ${overview.postgres.open} / ${overview.postgres.max_open || '∞'}`}
                    icon={<Database className="size-5" />}
                    tone={overview.postgres.healthy ? getPercentTone(overview.postgres.usage_percent, 75, 90) : 'danger'}
                  />
                  <OpsMetricCard
                    label="Redis"
                    value={`${overview.redis.usage_percent.toFixed(1)}%`}
                    sub={`连接 ${overview.redis.total_conns} / ${overview.redis.pool_size || '-'}`}
                    icon={<Server className="size-5" />}
                    tone={overview.redis.healthy ? getPercentTone(overview.redis.usage_percent, 70, 90) : 'danger'}
                  />
                  <OpsMetricCard
                    label="当前请求"
                    value={formatNumber(overview.requests.active)}
                    sub={`运行期累计 ${formatNumber(overview.requests.total)}`}
                    icon={<Activity className="size-5" />}
                    tone={overview.requests.active >= 20 ? 'warning' : 'normal'}
                  />
                  <OpsMetricCard
                    label="协程"
                    value={formatNumber(overview.runtime.goroutines)}
                    sub={`账号池 ${overview.runtime.available_accounts} / ${overview.runtime.total_accounts}`}
                    icon={<Users className="size-5" />}
                    tone={overview.runtime.goroutines >= 500 ? 'danger' : overview.runtime.goroutines >= 200 ? 'warning' : 'normal'}
                  />
                  <OpsMetricCard
                    label="QPS"
                    value={overview.traffic.qps.toFixed(1)}
                    sub={`峰值 ${overview.traffic.qps_peak.toFixed(1)}`}
                    icon={<BarChart3 className="size-5" />}
                    tone="info"
                  />
                  <OpsMetricCard
                    label="TPS"
                    value={formatNumber(Math.round(overview.traffic.tps))}
                    sub={`峰值 ${formatNumber(Math.round(overview.traffic.tps_peak))}`}
                    icon={<Zap className="size-5" />}
                    tone="info"
                  />
                  <OpsMetricCard
                    label="RPM"
                    value={formatNumber(Math.round(overview.traffic.rpm))}
                    sub={overview.traffic.rpm_limit > 0 ? `限额 ${formatNumber(overview.traffic.rpm_limit)}` : '未开启全局限流'}
                    icon={<Clock3 className="size-5" />}
                    tone={overview.traffic.rpm_limit > 0 && overview.traffic.rpm >= overview.traffic.rpm_limit * 0.8 ? 'warning' : 'normal'}
                  />
                  <OpsMetricCard
                    label="TPM"
                    value={formatNumber(Math.round(overview.traffic.tpm))}
                    sub={`今日 ${formatNumber(overview.traffic.today_tokens)}`}
                    icon={<AlertTriangle className="size-5" />}
                    tone={overview.traffic.error_rate >= 5 ? 'warning' : 'normal'}
                  />
                </div>
              </CardContent>
            </Card>
          </>
        ) : null}
      </>
    </StateShell>
  )
}

function OpsMetricCard({
  label,
  value,
  sub,
  icon,
  tone,
}: {
  label: string
  value: string
  sub: string
  icon: React.ReactNode
  tone: MetricTone
}) {
  const toneStyle = {
    normal: {
      badge: 'bg-[hsl(var(--success-bg))] text-[hsl(var(--success))]',
      dot: 'bg-emerald-500',
      icon: 'bg-[hsl(var(--success-bg))] text-[hsl(var(--success))]',
      label: '正常',
    },
    warning: {
      badge: 'bg-amber-500/10 text-amber-600',
      dot: 'bg-amber-500',
      icon: 'bg-amber-500/10 text-amber-600',
      label: '偏高',
    },
    danger: {
      badge: 'bg-destructive/10 text-destructive',
      dot: 'bg-destructive',
      icon: 'bg-destructive/10 text-destructive',
      label: '异常',
    },
    info: {
      badge: 'bg-primary/10 text-primary',
      dot: 'bg-primary',
      icon: 'bg-primary/10 text-primary',
      label: '实时',
    },
  }[tone]

  return (
    <Card className="py-0 transition-all duration-150 hover:-translate-y-0.5 hover:shadow-md">
      <CardContent className="p-4">
        <div className="flex items-center justify-between gap-3">
          <span className="text-[13px] font-semibold text-muted-foreground">{label}</span>
          <span className={`inline-flex items-center gap-1 rounded-full px-2.5 py-1 text-[12px] font-semibold ${toneStyle.badge}`}>
            <span className={`size-2 rounded-full ${toneStyle.dot}`} />
            {toneStyle.label}
          </span>
        </div>

        <div className="mt-5 flex items-end justify-between gap-3">
          <div className="min-w-0">
            <div className="text-[34px] font-bold leading-none tracking-tighter text-foreground">{value}</div>
            <div className="mt-3 text-[13px] leading-relaxed text-muted-foreground">{sub}</div>
          </div>
          <div className={`flex size-11 shrink-0 items-center justify-center rounded-2xl ${toneStyle.icon}`}>
            {icon}
          </div>
        </div>
      </CardContent>
    </Card>
  )
}

function SummaryPill({ label, value }: { label: string; value: string }) {
  return (
    <div className="rounded-2xl border border-border bg-white/65 px-4 py-3 shadow-[inset_0_1px_0_rgba(255,255,255,0.7)]">
      <div className="text-[12px] font-bold tracking-[0.14em] uppercase text-muted-foreground">{label}</div>
      <div className="mt-2 text-[20px] font-bold tracking-tight text-foreground">{value}</div>
    </div>
  )
}

function getPercentTone(value: number, warningThreshold: number, dangerThreshold: number): MetricTone {
  if (value >= dangerThreshold) return 'danger'
  if (value >= warningThreshold) return 'warning'
  return 'normal'
}

function formatBytes(bytes: number): string {
  if (!bytes) return '0 B'
  const units = ['B', 'KB', 'MB', 'GB', 'TB']
  let value = bytes
  let unitIndex = 0
  while (value >= 1024 && unitIndex < units.length - 1) {
    value /= 1024
    unitIndex++
  }
  return `${value.toFixed(unitIndex === 0 ? 0 : 1)} ${units[unitIndex]}`
}

function formatNumber(value: number): string {
  return value.toLocaleString()
}

function formatTimeLabel(iso: string): string {
  const date = new Date(iso)
  if (Number.isNaN(date.getTime())) {
    return '--:--:--'
  }
  return date.toLocaleTimeString('zh-CN', {
    hour12: false,
  })
}

function formatUptime(seconds: number): string {
  if (seconds <= 0) return '刚刚启动'

  const days = Math.floor(seconds / 86400)
  const hours = Math.floor((seconds % 86400) / 3600)
  const minutes = Math.floor((seconds % 3600) / 60)

  if (days > 0) {
    return `${days}天 ${hours}小时`
  }
  if (hours > 0) {
    return `${hours}小时 ${minutes}分钟`
  }
  return `${minutes}分钟`
}
