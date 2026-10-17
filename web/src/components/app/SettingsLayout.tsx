import type { ReactNode } from 'react'
import { accountHref } from '@/routes'
import type { Page } from '@/routes'
import Icon from '@/Icon'
import SegmentedNavigation from './SegmentedNavigation'

export default function SettingsLayout({ accountID, page, children }: { accountID: string; page: Page; children: ReactNode }) {
  return <div className="settings-layout">
    <aside className="settings-navigation">
      <span className="section-kicker">账号设置</span>
      <SegmentedNavigation className="settings-tabs" label="设置分类" items={([
        ['settings', '账号凭据', 'lock'], ['device', '接收设备', 'send_plane'], ['sync', '同步偏好', 'settings_2'],
      ] as const).map(([key, label, icon]) => ({ href: accountHref(accountID, key), label: <><Icon name={icon} />{label}</>, active: page === key || (key === 'settings' && page === 'credentials') }))} />
      <p>每个账号独立配置。<br />切换分类会保留未保存的修改。</p>
    </aside>
    <div className="settings-main">{children}</div>
  </div>
}
