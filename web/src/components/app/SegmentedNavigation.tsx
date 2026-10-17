import { LayoutGroup, motion, useReducedMotion } from 'motion/react'
import { useId } from 'react'
import type { ReactNode } from 'react'

export default function SegmentedNavigation({ label, items, className = '' }: {
  label: string
  items: { href: string; label: ReactNode; active: boolean }[]
  className?: string
}) {
  const id = useId()
  const reduced = useReducedMotion()
  return <LayoutGroup id={id}>
    <nav className={`segmented-navigation ${className}`} aria-label={label}>
      {items.map(item => <a key={item.href} href={item.href} aria-current={item.active ? 'page' : undefined}>
        {item.active && <motion.span className="segment-active" layoutId="selection" aria-hidden="true"
          transition={reduced ? { duration: 0 } : { type: 'spring', stiffness: 420, damping: 36 }} />}
        <span className="segment-label">{item.label}</span>
      </a>)}
    </nav>
  </LayoutGroup>
}
