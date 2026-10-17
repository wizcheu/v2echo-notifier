import Icon from '../../Icon'

export default function Brand() {
  return <span className="brand">
    <span className="brand-mark" aria-hidden="true"><Icon name="notification" /></span>
    <span className="brand-name">V2Echo<small>自托管通知助手</small></span>
  </span>
}
