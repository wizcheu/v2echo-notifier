import { useId, useRef, useState } from 'react'
import Icon from './Icon'

export default function SecretField({ label, value, onChange, placeholder, maxLength = 4096, disabled = false, readOnly = false, required = false }: {
  label: string; value: string; onChange?: (value: string) => void; placeholder?: string
  maxLength?: number; disabled?: boolean; readOnly?: boolean; required?: boolean
}) {
  const id = useId()
  const [visible, setVisible] = useState(false)
  const [feedback, setFeedback] = useState('')
  const input = useRef<HTMLInputElement>(null)

  async function copy() {
    setFeedback('')
    try {
      if (navigator.clipboard?.writeText) {
        await navigator.clipboard.writeText(value)
      } else {
        // LAN deployments can use HTTP, where the Clipboard API is unavailable.
        const field = document.createElement('textarea')
        const focused = document.activeElement
        field.value = value
        field.className = 'clipboard-copy-target'
        field.setAttribute('readonly', '')
        document.body.appendChild(field)
        try {
          field.select()
          if (!document.execCommand('copy')) throw new Error('copy unavailable')
        } finally {
          field.remove()
          if (focused instanceof HTMLElement) focused.focus()
        }
      }
      setFeedback('已复制')
    } catch {
      setVisible(true)
      setFeedback('无法自动复制，请选中后手动复制。')
      input.current?.focus()
      input.current?.select()
    }
  }

  return <div className="secret-field">
    <label htmlFor={id}>{label}{required && <span className="required-marker" aria-hidden="true"> · 必填</span>}</label>
    <div className="secret-field-controls">
      <input ref={input} id={id} type={visible ? 'text' : 'password'} value={value} autoComplete="off" spellCheck={false}
        placeholder={placeholder} maxLength={maxLength} disabled={disabled} readOnly={readOnly} required={required}
        aria-describedby={feedback ? `${id}-feedback` : undefined}
        onChange={event => { setFeedback(''); onChange?.(event.target.value) }} />
      <div className="secret-field-actions">
        <button type="button" disabled={disabled || !value} aria-controls={id} aria-pressed={visible}
          aria-label={`${visible ? '隐藏' : '显示'}${label}`} onClick={() => setVisible(v => !v)}><Icon name={visible ? 'eye_close' : 'eye'} /></button>
        <button type="button" disabled={disabled || !value} aria-label={`复制${label}`} onClick={() => void copy()}>复制</button>
      </div>
    </div>
    <p className="secret-field-feedback caption" id={`${id}-feedback`} role="status">{feedback}</p>
  </div>
}
