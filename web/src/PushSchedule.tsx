export type PushSchedule = { mode: 'window' | 'all_day'; start: string; end: string }
export type PushScheduleStatus = { resting: boolean; server_time: string; next_start: string; window_end: string; timezone: string }
export const defaultPushSchedule: PushSchedule = { mode: 'window', start: '08:00', end: '24:00' }

export function minuteOf(value: string, end = false): number {
  if (end && value === '24:00') return 1440
  if (!/^([01]\d|2[0-3]):[0-5]\d$/.test(value)) return NaN
  const [hours, minutes] = value.split(':').map(Number)
  return hours * 60 + minutes
}
export function scheduleError(schedule: PushSchedule): string {
  const start = minuteOf(schedule.start), end = minuteOf(schedule.end, true)
  if (!Number.isFinite(start) || !Number.isFinite(end)) return '请输入 HH:mm 格式的时间；结束时间可填 24:00。'
  return schedule.mode === 'window' && start === end ? '开始与结束不能相同；如需全天运行，请选择「全天运行」。' : ''
}
export function scheduleDescription(schedule: PushSchedule): string {
  return schedule.mode === 'all_day' ? '全天运行' : `每日 ${schedule.start}–${minuteOf(schedule.end, true) < minuteOf(schedule.start) ? '次日 ' : ''}${schedule.end}`
}
export function beijingTime(value: string): string {
  if (!value || value.startsWith('0001')) return '等待下一运行时段'
  return new Intl.DateTimeFormat('zh-CN', { timeZone: 'UTC', month: '2-digit', day: '2-digit', hour: '2-digit', minute: '2-digit', hourCycle: 'h23' }).format(new Date(new Date(value).getTime() + 8 * 60 * 60 * 1000))
}

export default function PushScheduleFields({ value, disabled, paused, onChange }: {
  value: PushSchedule; disabled: boolean; paused: boolean; onChange: (value: PushSchedule) => void
}) {
  const start = minuteOf(value.start), end = minuteOf(value.end, true)
  const error = scheduleError(value)
  const fullDay = value.mode === 'all_day' || (start === 0 && end === 1440)
  const overnight = end < start
  const duration = fullDay ? 1440 : (end - start + 1440) % 1440
  const rest = fullDay ? '没有休息时段' : `休息 ${end === 1440 ? '00:00' : value.end}–${value.start === '00:00' ? '24:00' : value.start}`
  const segments = fullDay ? [{ start: 0, end: 1440, active: true }] : overnight
    ? [{ start: 0, end, active: true }, { start: end, end: start, active: false }, { start, end: 1440, active: true }]
    : [{ start: 0, end: start, active: false }, { start, end, active: true }, { start: end, end: 1440, active: false }]
  const hours = (minutes: number) => `${Math.floor(minutes / 60)} 小时${minutes % 60 ? ` ${minutes % 60} 分钟` : ''}`
  return <section className="push-schedule" aria-labelledby="push-schedule-heading">
    <div className="section-heading"><h2 id="push-schedule-heading">推送与休息时段</h2><span className="schedule-timezone">UTC+8 · 北京时间</span></div>
    <p className="caption">设置每天允许推送的时间，其余时间自动休息，停止检查与推送。</p>
    <fieldset className="schedule-fields" disabled={disabled}>
      <legend className="sr-only">每日推送时段</legend>
      <div className="mode-picker schedule-mode" role="radiogroup" aria-label="推送时段模式">
        {([['window', '指定时段'], ['all_day', '全天运行']] as const).map(([mode, label]) => <label key={mode}>
          <input type="radio" name="push-schedule-mode" checked={value.mode === mode} onChange={() => onChange({ ...value, mode })} /><span>{label}</span>
        </label>)}
      </div>
      <div className="schedule-times">
        {value.mode === 'window' && <>
          <label>允许推送 · 开始<input aria-describedby="schedule-error schedule-help" type="text" autoComplete="off" spellCheck={false} required pattern="([01][0-9]|2[0-3]):[0-5][0-9]" placeholder="08:00" maxLength={5} value={value.start} onChange={e => onChange({ ...value, start: e.target.value })} /></label>
          <label>允许推送 · 结束{overnight && !error ? '（次日）' : ''}<input aria-describedby="schedule-error schedule-help" aria-invalid={Boolean(error)} type="text" autoComplete="off" spellCheck={false} required pattern="([01][0-9]|2[0-3]):[0-5][0-9]|24:00" placeholder="24:00" maxLength={5} value={value.end} onChange={e => onChange({ ...value, end: e.target.value })} /></label>
        </>}
        <div className="schedule-summary" aria-live="polite"><strong>{error ? '设置每日允许推送的时间' : rest}</strong><small>{error ? '精确到分钟，支持跨午夜。' : fullDay ? '全天允许检查与推送' : `每天 ${hours(1440 - duration)} · 自动暂停检查与推送`}</small></div>
      </div>
      {error ? <p className="field-error" id="schedule-error" role="alert">{error}</p> : <div className="schedule-preview" aria-label={`${scheduleDescription(value)}；${rest}`}>
        <div className="schedule-track" aria-hidden="true">{segments.filter(s => s.end > s.start).map((s, i) => <span key={i} className={s.active ? 'allowed' : 'rest'} style={{ flexBasis: `${(s.end - s.start) / 14.4}%` }}><span>{s.end - s.start >= 180 ? s.active ? '允许检查与推送' : '休息' : ''}</span></span>)}</div>
        <div className="schedule-ticks" aria-hidden="true"><span>00:00</span><span>08:00</span><span>16:00</span><span>24:00</span></div>
      </div>}
    </fieldset>
    <p className="caption" id="schedule-help">{paused ? '同步已暂停；已保存的时段会保留，到点不会自动启用同步。' : '灰色为休息时段，紫色为允许推送时段。到达开始时间自动恢复；24:00 表示当天结束。'}</p>
    <details className="technical-details schedule-rules"><summary>休息期间会暂停哪些任务？</summary><p className="caption">自动网页检查、API 检查、凭据巡检和推送服务通信都会暂停；立即检查与测试推送也不可用。查看记录和编辑设置不受影响，时段跳过不生成检查记录。</p><p className="caption">恢复后先检查最新未读，再决定是否推送；原有额度、冷却、错误退避和事件有效期继续生效。已提交的消息无法撤回，设备可能稍后才显示。</p><p className="caption">每天重复一个时段，按固定 UTC+8 执行，不随浏览器或服务器时区变化。结束早于开始表示次日；全天运行请明确选择「全天运行」。</p></details>
  </section>
}
