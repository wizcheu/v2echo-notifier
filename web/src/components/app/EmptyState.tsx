import type { ReactNode } from 'react'
import Icon from '../../Icon'

export default function EmptyState({ title, description, icon = 'notification', children }: {
  title: string; description: string; icon?: string; children?: ReactNode
}) {
  return <div className="empty">
    <span className="state-icon"><Icon name={icon} /></span>
    <h3>{title}</h3>
    <p>{description}</p>
    {children && <div className="empty-actions">{children}</div>}
  </div>
}
