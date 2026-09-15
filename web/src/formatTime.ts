export function formatTime(value: string | number, seconds = true) {
  if (!value || (typeof value === 'string' && value.startsWith('0001'))) return '尚无记录'
  const date = new Date(typeof value === 'number' ? value * 1000 : value)
  if (Number.isNaN(date.getTime())) return '尚无记录'
  const now = new Date(), yesterday = new Date(now)
  yesterday.setDate(now.getDate() - 1)
  const day = date.toDateString() === now.toDateString() ? '今天' : date.toDateString() === yesterday.toDateString() ? '昨天' : date.toLocaleDateString('zh-CN', {year: date.getFullYear() !== now.getFullYear() ? 'numeric' : undefined,month:'numeric',day:'numeric'})
  return `${day} ${date.toLocaleTimeString('zh-CN', {hour12:false,hour:'2-digit',minute:'2-digit',second:seconds?'2-digit':undefined})}`
}
