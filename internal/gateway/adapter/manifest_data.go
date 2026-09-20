package adapter

import "github.com/mirainya/Prism/internal/gateway/billing"

// commonBillingVars are observable on every call regardless of capability. They
// are declared once and shared, so an expression means the same thing whichever
// adapter serves it.
func commonBillingVars() []BillingVar {
	boolDomain := &VarDomainSpec{Kind: HintBool}
	return []BillingVar{
		{Name: "success", Desc: "调用是否成功 0/1", Domain: boolDomain},
		{Name: "duration_ms", Desc: "调用耗时毫秒", Domain: &VarDomainSpec{Kind: HintInt, Min: "0", Max: "3600000"}},
		// The engine caps route attempts at 3, so the domain is the real one
		// rather than an arbitrary ceiling.
		{Name: "attempt_count", Desc: "路由尝试次数", Domain: &VarDomainSpec{Kind: HintInt, Min: "1", Max: "3"}},
	}
}

// videoBillingVars are the variables any video adapter exposes. resolution and
// seconds read their domains from the variant's own hints, so narrowing a hint
// narrows the proven upper bound.
func videoBillingVars() []BillingVar {
	return []BillingVar{
		{Name: "seconds", Desc: "生成视频秒数", From: "duration"},
		{Name: "resolution", Desc: "输出分辨率枚举", From: "resolution"},
		{Name: "has_audio", Desc: "是否含音频 0/1", From: "generate_audio"},
	}
}

func boolPointer(value bool) *bool { return &value }

// seedanceManifest declares the three commercial shapes seedance is sold in.
// They differ in accepted duration, accepted resolution and which optional
// features the upstream honours, which is exactly why one adapter needs three
// variants instead of three adapters.
func seedanceManifest() Manifest {
	return Manifest{
		Adapter:         "seedance",
		AdapterVersion:  1,
		Capability:      "video",
		DownstreamPaths: []string{"/v1/videos/generations"},
		BillingFuncs:    []string{"tier", "param", "in"},
		BillingVars: map[varLayer][]BillingVar{
			LayerCommon:     commonBillingVars(),
			LayerCapability: videoBillingVars(),
			LayerAdapter: {
				{Name: "has_video_ref", Desc: "是否含视频参考素材 0/1", Domain: &VarDomainSpec{Kind: HintBool}},
				{Name: "priority", Desc: "优先级档位 1/2/3/4", Domain: &VarDomainSpec{Kind: HintEnum, Numbers: []string{"1", "2", "3", "4"}}},
				{Name: "task_mode", Desc: "任务模式枚举", From: "task_mode"},
			},
		},
		Variants: []Variant{seedanceOfficialVariant(), seedanceHChannelVariant(), seedance25Variant()},
	}
}

func seedanceOfficialVariant() Variant {
	return Variant{
		Code: "official", Display: "官方满血渠道",
		Models: []string{"seedance-2.0", "seedance-2.0-fast"},
		ParamHints: map[string]ParamHint{
			"resolution":     {Kind: HintEnum, Options: []string{"480p", "720p", "1080p", "4k"}, Default: "720p"},
			"duration":       {Kind: HintInt, Min: "5", Max: "10"},
			"generate_audio": {Kind: HintBool, Default: "false"},
			"last_frame":     {Kind: HintBool, Allowed: boolPointer(true)},
			"web_search":     {Kind: HintBool, Allowed: boolPointer(true)},
		},
		RefMedia: map[string]RefMediaLimit{
			"image": {Max: 3},
			"video": {Max: 1, ItemSeconds: [2]int{2, 15}, TotalSeconds: 15},
			"audio": {Max: 1, ItemSeconds: [2]int{2, 15}, TotalSeconds: 15},
		},
		PricingHints: PricingHints{DefaultMode: "expression", Example: "seconds * 0.30"},
	}
}

func seedanceHChannelVariant() Variant {
	return Variant{
		Code: "h_channel", Display: "H渠道",
		Models: []string{"seedance-h-720p", "seedance-h-1080p"},
		ParamHints: map[string]ParamHint{
			// The upstream sells a different duration window per resolution, so
			// the two hints are linked rather than independent.
			"resolution": {Kind: HintConditionalEnum, Cases: []HintCase{
				{Value: "720p", Dependent: "duration", Min: "4", Max: "15"},
				{Value: "1080p", Dependent: "duration", Min: "4", Max: "8"},
			}},
			"duration": {Kind: HintInt, Min: "4", Max: "8"},
		},
		HardConstraints: []HardConstraint{
			{Field: "last_frame", MustBe: "false"},
			{Field: "web_search", MustBe: "false"},
		},
		PricingHints: PricingHints{DefaultMode: "flat", Example: "1.20"},
	}
}

func seedance25Variant() Variant {
	return Variant{
		Code: "seedance25", Display: "Seedance 2.5",
		Models: []string{"seedance-2.5"},
		ParamHints: map[string]ParamHint{
			"task_mode":  {Kind: HintEnum, Options: []string{"references"}, Required: true, Default: "references"},
			"resolution": {Kind: HintEnum, Options: []string{"480p", "720p"}, Default: "720p"},
			"duration":   {Kind: HintInt, Min: "4", Max: "30"},
		},
		HardConstraints: []HardConstraint{
			{Field: "generate_audio", MustBe: "false"},
			{Field: "cancel_allowed", MustBe: "false"},
		},
		PricingHints: PricingHints{
			DefaultMode: "expression",
			Example:     "seconds * 0.20 * (priority==4 ? 1.5 : 1) * (has_video_ref==1 ? 1.2 : 1)",
		},
	}
}

// imageBillingVars are the result counters emitted by OpenAIImages. Token
// counters are optional at runtime because not every compatible provider
// returns usage, but their finite domains let token-priced variants prove a
// reservation bound before a request is sent.
func imageBillingVars() []BillingVar {
	return []BillingVar{
		{Name: "count", Desc: "生成图片数量", Domain: &VarDomainSpec{Kind: HintInt, Min: "1", Max: "10"}},
		{Name: "input_tokens", Desc: "输入 token 数", Domain: &VarDomainSpec{Kind: HintInt, Min: "0", Max: "2000000"}},
		{Name: "output_tokens", Desc: "输出 token 数", Domain: &VarDomainSpec{Kind: HintInt, Min: "0", Max: "2000000"}},
	}
}

// openAIImagesManifest covers the synchronous OpenAI-compatible generation
// and edit paths served by OpenAIImages. Operation-specific requirements such
// as an edit needing at least one image remain enforced by the request codec.
func openAIImagesManifest() Manifest {
	return Manifest{
		Adapter:         "openai_images",
		AdapterVersion:  1,
		Capability:      "image",
		DownstreamPaths: []string{"/v1/images/generations", "/v1/images/edits"},
		BillingFuncs:    []string{"tier", "param", "in"},
		BillingVars: map[varLayer][]BillingVar{
			LayerCommon:     commonBillingVars(),
			LayerCapability: imageBillingVars(),
		},
		Variants: []Variant{{
			Code: DefaultVariant, Display: "OpenAI Images 兼容格式",
			Models: []string{
				"doubao-seedream-5-0", "gemini-3-pro-image-preview", "grok-imagine-image", "gpt-image-2",
				"gpt-image-2-adobe", "gpt-image-2-auto", "gpt-image-2-c", "gpt-image-2-high", "gpt-image-2.5",
				"gpt-image-2.5-flare", "gpt-image-2.5-sunburst",
			},
			ParamHints: map[string]ParamHint{
				"n":                  {Kind: HintInt, Min: "1", Max: "10", Default: "1"},
				"response_format":    {Kind: HintEnum, Options: []string{"url", "b64_json"}, Default: "url"},
				"output_compression": {Kind: HintInt, Min: "0", Max: "100"},
				"stream":             {Kind: HintBool, Default: "false"},
				"partial_images":     {Kind: HintInt, Min: "0", Max: "3"},
			},
			RefMedia: map[string]RefMediaLimit{
				"image": {Max: maxImageInputs},
				"mask":  {Max: 1},
			},
		}},
	}
}

// chatBillingVars are the token counters every conversational adapter reports.
// The maxima are domain ceilings for the bound proof, not request limits: a
// price expression must be boundable even though the real cap is per-model.
func chatBillingVars() []BillingVar {
	counter := func(name, desc string) BillingVar {
		return BillingVar{Name: name, Desc: desc, Domain: &VarDomainSpec{Kind: HintInt, Min: "0", Max: "2000000"}}
	}
	return []BillingVar{
		counter("input_tokens", "输入 token 数"),
		counter("output_tokens", "输出 token 数"),
		counter("cache_read_tokens", "命中缓存读取的 token 数"),
		counter("cache_write_tokens", "写入缓存的 token 数"),
		counter("reasoning_tokens", "推理 token 数"),
	}
}

// chatManifest builds the manifest for one conversational upstream transport.
// Every conversational transport passes through the canonical codec layer, so
// each can serve all three supported public protocol endpoints.
func chatManifest(code, capability string, models []string) Manifest {
	return Manifest{
		Adapter:         code,
		AdapterVersion:  1,
		Capability:      capability,
		DownstreamPaths: []string{"/v1/chat/completions", "/v1/responses", "/v1/messages"},
		BillingFuncs:    []string{"tier", "param", "in"},
		BillingVars: map[varLayer][]BillingVar{
			LayerCommon:     commonBillingVars(),
			LayerCapability: chatBillingVars(),
		},
		Variants: []Variant{{
			Code: DefaultVariant, Display: "标准", Models: models,
			ParamHints: map[string]ParamHint{
				"stream":      {Kind: HintBool, Default: "false"},
				"max_tokens":  {Kind: HintInt, Min: "1", Max: "200000"},
				"temperature": {Kind: HintEnum, Options: []string{"0", "0.5", "1", "1.5", "2"}},
			},
			PricingHints: PricingHints{
				DefaultMode: "flat",
				Example:     "input_tokens * 0.000003 + output_tokens * 0.000015",
			},
			Tiers: map[string][]billing.ExprTier{
				// A long-context surcharge table, referenced as
				// tier(input_tokens, 'context'). Declared here so the bound proof
				// can place its breakpoints on the token axis.
				"context": {{UpTo: "200000", Value: "1"}, {UpTo: "", Value: "2"}},
			},
		}},
	}
}

// processManifests is the manifest set this binary serves. It mirrors the
// descriptor table: every entry must have a descriptor, and a descriptor without
// a manifest simply cannot be priced by expression yet.
func processManifests() []Manifest {
	return []Manifest{
		seedanceManifest(),
		openAIImagesManifest(),
		chatManifest("openai_chat", "chat",
			[]string{"gpt-4.1", "gpt-4.1-mini", "gpt-4o"}),
		chatManifest("openai_responses", "chat",
			[]string{"gpt-4.1", "o3", "o4-mini"}),
		chatManifest("anthropic_messages", "chat",
			[]string{"claude-opus-5", "claude-sonnet-5", "claude-haiku-4-5"}),
		chatManifest("google_generate_content", "chat",
			[]string{"gemini-2.5-pro", "gemini-2.5-flash"}),
	}
}
