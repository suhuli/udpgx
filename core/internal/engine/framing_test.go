package engine

import (
	"bytes"
	"testing"
)

func TestFraming(t *testing.T) {
	var b bytes.Buffer
	want := openRequest{ServiceID: "hello"}
	if err := writeFrame(&b, want); err != nil {
		t.Fatal(err)
	}
	var got openRequest
	if err := readFrame(&b, &got); err != nil || got != want {
		t.Fatalf("%v %v", got, err)
	}
	bad := bytes.NewReader([]byte{0, 1, 0, 0})
	if readFrame(bad, &got) == nil {
		t.Fatal("oversized frame was accepted")
	}
}
