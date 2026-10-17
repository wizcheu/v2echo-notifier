import { formatTime } from './formatTime'
import Icon from './Icon'
import EmptyState from './components/app/EmptyState'

const machineTime = (timestamp: number) => {
  const date = new Date(timestamp * 1000)
  return Number.isFinite(date.getTime()) ? date.toISOString() : undefined
}

type Notification = { id: number; title: string; body: string; created: number }

export default function NotificationList({ items, grouped = false }: { items: Notification[]; grouped?: boolean }) {
  if (!items.length) return <EmptyState title="还没有同步到通知" description="启用同步后，助手会按检查计划自动更新。已有通知会保留在这里。" />
  const byDate: Record<string, Notification[]> = {}
  for (const item of items) {
    const day = grouped ? formatTime(item.created, false).split(' ')[0] : ''
    ;(byDate[day] ??= []).push(item)
  }
  return <>
    {Object.entries(byDate).map(([day, records]) => <div key={day} className="notification-group">
      {day && <h2 className="date-heading">{day}<small>{records.length} 条</small></h2>}
      <ul className="notification-list">
        {records.map(item => <li key={item.id}>
          <span className="notification-mark"><Icon name="notification" /></span>
          <div className="notification-copy">
            <div className="notification-heading"><h3>{item.title || 'V2EX 提醒'}</h3>
              <time dateTime={machineTime(item.created)}>{grouped ? formatTime(item.created, false).split(' ').slice(1).join(' ') : formatTime(item.created, false)}</time>
            </div>
            {item.body && <p>{item.body}</p>}
            {grouped && <small>通知 #{item.id}</small>}
          </div>
        </li>)}
      </ul>
    </div>)}
  </>
}
