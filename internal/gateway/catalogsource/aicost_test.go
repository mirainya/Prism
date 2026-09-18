package catalogsource

import (
	"errors"
	"testing"
)

func TestParseAICostModelsNormalizesStableOrder(t *testing.T) {
	models, err := ParseAICostModels([]byte(`{"object":"list","data":[{"id":"z-model","object":"model"},{"id":"a-model","owned_by":"vendor"}]}`), "seedance2.0批发")
	if err != nil {
		t.Fatal(err)
	}
	if len(models) != 2 || models[0].Code != "a-model" || models[1].Code != "z-model" || models[0].Groups[0] != "seedance2.0批发" {
		t.Fatalf("models = %#v", models)
	}
}

func TestParseAICostPricingPreservesDecimalFacts(t *testing.T) {
	models, err := ParseAICostPricing([]byte(`{
		"success":true,"message":"","data":[{
			"model_name":"seedance2.5-xq-720p","description":"按秒收费","tags":"视频",
			"vendor_id":16,"quota_type":1,"model_ratio":0,"model_price":0.6500,
			"owner_by":"","completion_ratio":0,"enable_groups":["seedance2.0批发"],
			"supported_endpoint_types":["openai"],"pricing_version":7
		}]}`))
	if err != nil {
		t.Fatal(err)
	}
	if len(models) != 1 || models[0].ModelPrice == nil || *models[0].ModelPrice != "0.65" ||
		models[0].PricingVersion != "7" || models[0].ProviderQuotaType == nil || *models[0].ProviderQuotaType != 1 {
		t.Fatalf("model = %#v", models)
	}
}

func TestParseAICostPricingAcceptsProviderModelIdentifiers(t *testing.T) {
	models, err := ParseAICostPricing([]byte(`{
		"success":true,
		"data":[
			{"model_name":"seedance2.5-10图","description":"video","tags":"video","enable_groups":["g"],"supported_endpoint_types":["openai"]},
			{"model_name":"seedance2.0-480p-100%","description":"video","tags":"video","enable_groups":["g"],"supported_endpoint_types":["openai"]}
		]
	}`))
	if err != nil {
		t.Fatal(err)
	}
	if len(models) != 2 {
		t.Fatalf("models=%#v", models)
	}
}

func TestParseAICostPricingNormalizesDescriptionWhitespace(t *testing.T) {
	models, err := ParseAICostPricing([]byte(`{
		"success":true,
		"data":[{"model_name":"video-model","description":"first line\nsecond\tline","tags":" video ","enable_groups":["g"],"supported_endpoint_types":["openai"]}]
	}`))
	if err != nil {
		t.Fatal(err)
	}
	if len(models) != 1 || models[0].Description != "first line second line" || models[0].Tags != "video" {
		t.Fatalf("model=%#v", models)
	}
}

func TestAICostParsersRejectAmbiguousOrMalformedFacts(t *testing.T) {
	for _, test := range []struct {
		name string
		run  func() error
	}{
		{"duplicate model", func() error { _, err := ParseAICostModels([]byte(`{"data":[{"id":"m"},{"id":"m"}]}`), "g"); return err }},
		{"invalid price", func() error {
			_, err := ParseAICostPricing([]byte(`{"success":true,"data":[{"model_name":"m","quota_type":1,"model_price":"NaN","enable_groups":["g"],"supported_endpoint_types":["openai"]}]}`))
			return err
		}},
		{"missing groups", func() error {
			_, err := ParseAICostPricing([]byte(`{"success":true,"data":[{"model_name":"m","quota_type":1,"model_price":1,"enable_groups":[],"supported_endpoint_types":["openai"]}]}`))
			return err
		}},
		{"extra secret field", func() error {
			_, err := ParseAICostAccountSecret([]byte(`{"username":"u","password":"p","token":"x"}`))
			return err
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			if err := test.run(); !errors.Is(err, ErrInvalidSnapshot) {
				t.Fatalf("err = %v", err)
			}
		})
	}
}

func TestParseAICostLoginRequiresSuccessfulUser(t *testing.T) {
	login, err := ParseAICostLogin([]byte(`{"success":true,"data":{"id":5318,"username":"key"}}`))
	if err != nil || login.UserID != 5318 {
		t.Fatalf("login=%#v err=%v", login, err)
	}
	if _, err := ParseAICostLogin([]byte(`{"success":false,"data":null}`)); !errors.Is(err, ErrInvalidSnapshot) {
		t.Fatalf("failed login err = %v", err)
	}
}
