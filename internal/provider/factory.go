package provider

import (
	"github.com/mirainya/Prism/internal/model"
)

func NewProvider(channel *model.Channel, account *model.ChannelAccount, endpoint *model.Endpoint) (Provider, error) {
	// 解析响应映射
	responseMapping, err := ParseResponseMapping(endpoint.ResponseMapping)
	if err != nil {
		return nil, err
	}

	// 解析回调映射
	callbackMapping, err := ParseResponseMapping(endpoint.CallbackMapping)
	if err != nil {
		return nil, err
	}

	// 解析轮询响应映射
	pollResponseMapping, err := ParseResponseMapping(endpoint.PollResponseMapping)
	if err != nil {
		return nil, err
	}

	apiKey := account.APIKey
	baseURL := channel.BaseURL

	// 认证配置默认值
	authLocation := endpoint.AuthLocation
	if authLocation == "" {
		authLocation = "header"
	}
	authKey := endpoint.AuthKey
	if authKey == "" {
		authKey = "Authorization"
	}
	authValuePrefix := endpoint.AuthValuePrefix
	if authValuePrefix == "" && authLocation == "header" {
		authValuePrefix = "Bearer "
	}

	base := &BaseProvider{
		Name:                channel.Type,
		BaseURL:             baseURL,
		APIKey:              apiKey,
		AuthLocation:        authLocation,
		AuthKey:             authKey,
		AuthValuePrefix:     authValuePrefix,
		ContentType:         endpoint.ContentType,
		RequestMethod:       endpoint.RequestMethod,
		PollMethod:          endpoint.PollMethod,
		SubmitPath:          endpoint.RequestPath,
		ProgressPath:        endpoint.PollPath,
		Converter:           NewDefaultConverter(),
		Parser:              NewDefaultParser(),
		ResponseMapping:     responseMapping,
		PollResponseMapping: pollResponseMapping,
		CallbackMapping:     callbackMapping,
		Timeout:             endpoint.Timeout,
	}

	return base, nil
}
