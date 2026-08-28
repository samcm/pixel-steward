package runtime

import (
	"encoding/json"
	"testing"
)

func TestOpenCodeUsageEvent(t *testing.T) {
	var event openCodeEvent
	raw := `{"type":"step_finish","part":{"messageID":"msg","reason":"stop","cost":0.02112,"tokens":{"input":10473,"output":1,"reasoning":28,"cache":{"write":3,"read":4}}}}`
	if err := json.Unmarshal([]byte(raw), &event); err != nil {
		t.Fatal(err)
	}
	if event.Part.Tokens.Input != 10473 || event.Part.Tokens.Cache.Read != 4 || event.Part.Cost != 0.02112 {
		t.Fatalf("unexpected event: %+v", event)
	}
}
