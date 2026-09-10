package generic

import "testing"

func TestProviderMoneyIsExactAndBounded(t *testing.T) {
	for _, test := range []struct {
		body, want string
		invalid    bool
	}{
		{`{"cost":0.123456789123456789}`, "0.123456789123456789", false},
		{`{"cost":"9007199254740993.0001"}`, "9007199254740993.0001", false},
		{`{"cost":0}`, "0", false},
		{`{}`, "", false},
		{`{"cost":null}`, "", false},
		{`{"cost":true}`, "", true},
		{`{"cost":{}}`, "", true},
		{`{"cost":[]}`, "", true},
		{`{"cost":""}`, "", true},
		{`{"cost":"NaN"}`, "", true},
		{`{"cost":"Infinity"}`, "", true},
		{`{"cost":"1e1000000000"}`, "", true},
		{`{"cost":1e-8}`, "", true},
		{`{"cost":-0}`, "", true},
		{`{"cost":-1}`, "", true},
		{`{"cost":"0.0000000000000000001"}`, "", true},
		{`{"cost":"100000000000000000000"}`, "", true},
	} {
		t.Run(test.body, func(t *testing.T) {
			value, err := firstMoney([]byte(test.body), []string{"cost"})
			if (err != nil) != test.invalid {
				t.Fatalf("error=%v", err)
			}
			if test.invalid {
				return
			}
			if test.want == "" {
				if value != nil {
					t.Fatal("missing cost is not zero")
				}
				return
			}
			if value == nil || value.String() != test.want {
				t.Fatalf("cost=%v want=%s", value, test.want)
			}
		})
	}
}
