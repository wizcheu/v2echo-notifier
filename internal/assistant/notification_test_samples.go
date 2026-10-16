package assistant

type PushTestSample struct {
	Kind      string `json:"kind"`
	Label     string `json:"label"`
	Title     string `json:"title"`
	Body      string `json:"body"`
	AvatarURL string `json:"avatar_url"`
}

func pushTestSamples() []PushTestSample {
	const avatar = "https://cdn.v2ex.com/avatar/17f5/9a32/136734_xlarge.png?m=1784141154"
	return []PushTestSample{
		{"reply", "回复", "测试用户 回复了你的主题", "这个思路很实用，我试了一下，效果很好。谢谢分享！", avatar},
		{"mention", "提及", "测试用户 提到了你", "这个方案你之前也讨论过，想听听你的建议。", avatar},
		{"thanks_topic", "感谢主题", "测试用户 感谢了你的主题", "分享一个让日常开发更轻松的小工具", avatar},
		{"thanks_reply", "感谢回复", "测试用户 感谢了你的回复", "可以先检查网络配置，再重启应用重试。", avatar},
		{"favorite", "收藏主题", "测试用户 收藏了你的主题", "分享一个让日常开发更轻松的小工具", avatar},
	}
}

func pushTestSample(kind string) (PushTestSample, bool) {
	for _, sample := range pushTestSamples() {
		if sample.Kind == kind {
			return sample, true
		}
	}
	return PushTestSample{}, false
}
