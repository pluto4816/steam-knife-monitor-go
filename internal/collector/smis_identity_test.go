package collector

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"testing"
)

func TestFetchCandidateIdentity(t *testing.T) {
	client := testClient(t, func(response http.ResponseWriter, request *http.Request) {
		if request.Method != http.MethodGet || request.URL.Path != "/api/commodity/53" {
			t.Errorf("request = %s %s", request.Method, request.URL.Path)
		}
		writeResponse(response, http.StatusOK, "application/json", `{
			"code":200,"message":"success","data":{
				"id":53,"hashName":"AK-47 | Elite Build (Field-Tested)",
				"cnName":"AK-47 | 精英之作 (久经沙场)",
				"rarity":"军规级","quality":"普通","exterior":"久经沙场"
			}}`)
	})
	identity, err := client.FetchCandidateIdentity(context.Background(), 53)
	if err != nil {
		t.Fatal(err)
	}
	if identity.ItemID != 53 || identity.MarketHashName != "AK-47 | Elite Build (Field-Tested)" || identity.NameCN == "" {
		t.Fatalf("identity = %#v", identity)
	}
}

func TestFetchCandidateIdentityRejectsWrongCriteria(t *testing.T) {
	tests := []struct {
		field string
		value string
	}{
		{"rarity", "受限"},
		{"quality", "StatTrak™"},
		{"exterior", "略有磨损"},
	}
	for _, testCase := range tests {
		t.Run(testCase.field, func(t *testing.T) {
			body := fmt.Sprintf(`{
				"code":200,"data":{"id":53,"hashName":"Hash","cnName":"中文",
				"rarity":"军规级","quality":"普通","exterior":"久经沙场",%q:%q}}`,
				testCase.field, testCase.value,
			)
			client := testClient(t, func(response http.ResponseWriter, _ *http.Request) {
				writeResponse(response, http.StatusOK, "application/json", body)
			})
			_, err := client.FetchCandidateIdentity(context.Background(), 53)
			if err == nil || !strings.Contains(err.Error(), testCase.field) {
				t.Fatalf("error = %v, want %s", err, testCase.field)
			}
		})
	}
}
