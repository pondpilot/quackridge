package quackridge

import (
	"context"
	"errors"
	"testing"
)

func TestClassifyError(t *testing.T) {
	tests := []struct {
		name string
		err  error
		code ErrorCode
		want string
	}{
		{"timeout", context.DeadlineExceeded, CodeTimeout, "QR_TIMEOUT: query timed out"},
		{"cancelled", context.Canceled, CodeCancelled, "QR_CANCELLED: query cancelled"},
		{"memory", errors.New("Out of Memory Error: secret query text"), CodeResourceExhausted, "QR_RESOURCE_EXHAUSTED: query resource limit exceeded"},
		{"policy", errors.New("Unauthorized query: secret query text"), CodeRejectedStatement, "QR_REJECTED_STATEMENT: statement rejected by policy"},
		{"unknown", errors.New("/private/path secret query text"), CodeInternal, "QR_INTERNAL: query failed"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got := ClassifyError(test.err)
			if !IsCode(got, test.code) || got.Error() != test.want {
				t.Fatalf("ClassifyError() = %v, want %s", got, test.want)
			}
		})
	}
}
