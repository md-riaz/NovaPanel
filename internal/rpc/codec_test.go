package rpc

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
)

func TestDecodeLine(t *testing.T) {
	for _, s := range []string{"", "null", "[]", `{}`, `{"id":"01ARZ3NDEKTSV4RRFFQ69G5FAV","verb":"ping"}`, `{"id":"01ARZ3NDEKTSV4RRFFQ69G5FAV","verb":"ping","args":[]}`, `{"id":"01ARZ3NDEKTSV4RRFFQ69G5FAV","verb":"ping","args":null}`, `{"id":"01ARZ3NDEKTSV4RRFFQ69G5FAV","verb":"ping","args":{}} {}`, strings.Repeat("x", MaxRequestBytes+1)} {
		if _, err := DecodeLine([]byte(s)); err == nil {
			t.Errorf("accepted invalid frame %.80q", s)
		}
	}
	req, err := DecodeLine([]byte(`{"id":"01ARZ3NDEKTSV4RRFFQ69G5FAV","verb":"ping","args":{}}`))
	if err != nil || req.ID != "01ARZ3NDEKTSV4RRFFQ69G5FAV" || req.Verb != "ping" {
		t.Fatalf("decode: %+v %v", req, err)
	}
}
func TestEncodeBoundedReply(t *testing.T) {
	for _, data := range []any{map[string]string{"version": "test"}, strings.Repeat("x", MaxReplyBytes), make(chan int)} {
		var b bytes.Buffer
		r := OK(data)
		r.ID = "01ARZ3NDEKTSV4RRFFQ69G5FAV"
		if err := Encode(&b, r); err != nil {
			t.Fatal(err)
		}
		if b.Len() > MaxReplyBytes || bytes.Count(b.Bytes(), []byte{'\n'}) != 1 {
			t.Fatal("unbounded or unframed reply")
		}
		var got Reply
		if err := json.Unmarshal(bytes.TrimSpace(b.Bytes()), &got); err != nil {
			t.Fatal(err)
		}
		if got.ID != "01ARZ3NDEKTSV4RRFFQ69G5FAV" {
			t.Fatal("lost id")
		}
		if _, ok := data.(map[string]string); !ok && (got.OK || got.Code != "agent_error") {
			t.Fatalf("unsafe reply: %+v", got)
		}
	}
}
