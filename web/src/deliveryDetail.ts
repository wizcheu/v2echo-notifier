export function deliveryDetail(item: { status: string; submitted: boolean; detail: string }) {
  if (item.status === 'pending' && item.submitted && (!item.detail || item.detail === '服务端处理中，将在下一轮批量查询')) {
    return '已提交，等待下一轮查询推送结果；设备可能已经收到消息。'
  }
  return item.detail || '等待上报'
}
