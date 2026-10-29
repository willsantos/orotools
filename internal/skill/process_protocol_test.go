package skill_test

import (
	"fmt"
	"strings"
	"testing"

	"oroborus.dev/orotools/internal/skill"
)

func TestDecodeProcessRequestStrictAndVersioned(t *testing.T) {
	valid := `{"protocolVersion":1,"requestId":"req-1","operation":"capabilities"}`
	got, err := skill.DecodeProcessRequest(strings.NewReader(valid))
	if err != nil || got.RequestID != "req-1" {
		t.Fatalf("DecodeProcessRequest() = %+v, %v", got, err)
	}
	cases := map[string]string{
		"unknown field": `{"protocolVersion":1,"requestId":"req-1","operation":"capabilities","token":"secret"}`,
		"trailing json": valid + ` {}`,
		"unsupported":   `{"protocolVersion":2,"requestId":"req-1","operation":"capabilities"}`,
		"missing id":    `{"protocolVersion":1,"operation":"capabilities"}`,
		"bad operation": `{"protocolVersion":1,"requestId":"req-1","operation":2}`,
	}
	for name, payload := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := skill.DecodeProcessRequest(strings.NewReader(payload)); err == nil {
				t.Fatal("expected request rejection")
			}
		})
	}
	oversized := strings.NewReader(fmt.Sprintf(`{"padding":%q}`, strings.Repeat("x", skill.MaxProcessRequestBytes)))
	if _, err := skill.DecodeProcessRequest(oversized); err == nil {
		t.Fatal("oversized request accepted")
	}
}
