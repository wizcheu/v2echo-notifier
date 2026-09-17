import { useState } from 'react'

export default function AccountAvatar({ username, url }: { username: string; url?: string }) {
  const [failedURL, setFailedURL] = useState('')
  return (
    <span className="avatar" aria-hidden="true">
      {url && url !== failedURL ? (
        <img src={url} alt="" referrerPolicy="no-referrer" onError={() => setFailedURL(url)} />
      ) : username.slice(0, 1).toUpperCase() || 'V'}
    </span>
  )
}
