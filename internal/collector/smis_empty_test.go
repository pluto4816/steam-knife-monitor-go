package collector

import (
	"context"
	"errors"
	"net/http"
	"testing"
)

func TestSMISEmptySuccessfulResponseIsAccessRestriction(t *testing.T) {
	client := testClient(t, func(response http.ResponseWriter, _ *http.Request) {
		response.Header().Set("Content-Type", "application/json")
		response.WriteHeader(http.StatusOK)
		_, _ = response.Write([]byte(" \n\t"))
	})
	_, err := client.FetchCandidatePage(context.Background(), 1)
	if !errors.Is(err, ErrAccessRestricted) {
		t.Fatalf("error = %v, want ErrAccessRestricted", err)
	}
}
