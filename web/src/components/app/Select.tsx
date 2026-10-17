import type { ComponentProps } from 'react'
import Icon from '../../Icon'

export default function Select(props: ComponentProps<'select'>) {
  return <span className="select-control">
    <select {...props} />
    <Icon name="down" />
  </span>
}
