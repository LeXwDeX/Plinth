package catalog_test

import (
	"bytes"
	"encoding/json"
	"os"
	"strings"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/catalog"
)

// #375: the default Schema declares the visible heartbeat event and the client's
// silence watchdog thresholds, and the public projection carries all three.
func TestDefaultCatalogDeclaresStreamWatchdog(t *testing.T) {
	taskCatalog, err := catalog.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	chat := taskCatalog.Application().Chat
	if chat.Stream.EventTypes.Heartbeat != "heartbeat" ||
		chat.Frontend.StreamSilenceMilliseconds != 30000 ||
		chat.Frontend.StreamReconnectAttempts != 3 {
		t.Fatalf("heartbeat event = %q, silence = %d, attempts = %d",
			chat.Stream.EventTypes.Heartbeat, chat.Frontend.StreamSilenceMilliseconds, chat.Frontend.StreamReconnectAttempts)
	}
	if chat.Frontend.StreamSilenceMilliseconds <= chat.Stream.HeartbeatMilliseconds {
		t.Fatalf("silence %d does not outlast the heartbeat interval %d",
			chat.Frontend.StreamSilenceMilliseconds, chat.Stream.HeartbeatMilliseconds)
	}
	public := taskCatalog.Application().Public().Chat
	if public.Stream.EventTypes.Heartbeat != "heartbeat" ||
		public.Frontend.StreamSilenceMilliseconds != 30000 ||
		public.Frontend.StreamReconnectAttempts != 3 {
		t.Fatalf("public heartbeat event = %q, silence = %d, attempts = %d",
			public.Stream.EventTypes.Heartbeat, public.Frontend.StreamSilenceMilliseconds, public.Frontend.StreamReconnectAttempts)
	}
	// The wire carries them under the Schema's own key names.
	encoded, err := json.Marshal(taskCatalog.Application().Public())
	if err != nil {
		t.Fatal(err)
	}
	var wire map[string]any
	if err := json.Unmarshal(encoded, &wire); err != nil {
		t.Fatal(err)
	}
	for path, want := range map[string]any{
		"chat.frontend.stream_silence_milliseconds": float64(30000),
		"chat.frontend.stream_reconnect_attempts":   float64(3),
		"chat.stream.event_types.heartbeat":         "heartbeat",
	} {
		if got, ok := publicPath(wire, path); !ok || got != want {
			t.Errorf("public %s = %v (present %v), want %v", path, got, ok, want)
		}
	}
}

func TestStreamWatchdogIsValidated(t *testing.T) {
	source, err := os.ReadFile("default_catalog.json")
	if err != nil {
		t.Fatal(err)
	}
	const (
		silence   = `"stream_silence_milliseconds": 30000`
		attempts  = `"stream_reconnect_attempts": 3`
		heartbeat = `"heartbeat": "heartbeat"`
	)
	for name, test := range map[string]struct{ old, new, detail string }{
		"silence equal to the heartbeat interval": {silence, `"stream_silence_milliseconds": 10000`, "stream_silence_milliseconds"},
		"silence below the heartbeat interval":    {silence, `"stream_silence_milliseconds": 9999`, "stream_silence_milliseconds"},
		"silence zero":                            {silence, `"stream_silence_milliseconds": 0`, "stream_silence_milliseconds"},
		"silence negative":                        {silence, `"stream_silence_milliseconds": -1`, "stream_silence_milliseconds"},
		"attempts zero":                           {attempts, `"stream_reconnect_attempts": 0`, "stream_reconnect_attempts"},
		"attempts negative":                       {attempts, `"stream_reconnect_attempts": -2`, "stream_reconnect_attempts"},
		"heartbeat reuses done":                   {heartbeat, `"heartbeat": "done"`, "重名"},
		"heartbeat reuses text":                   {heartbeat, `"heartbeat": "text"`, "重名"},
		"heartbeat reuses thinking":               {heartbeat, `"heartbeat": "thinking"`, "重名"},
		"heartbeat with surrounding space":        {heartbeat, `"heartbeat": " heartbeat"`, "首尾空白"},
	} {
		changed := bytes.Replace(source, []byte(test.old), []byte(test.new), 1)
		if bytes.Equal(changed, source) {
			t.Fatalf("%s: fixture %q not found", name, test.old)
		}
		_, err := catalog.Load(bytes.NewReader(changed))
		if err == nil || !strings.Contains(err.Error(), test.detail) {
			t.Errorf("%s: error = %v, want %q", name, err, test.detail)
		}
	}
	// A silence window that only just outlasts the heartbeat interval is valid,
	// and so is a single reconnect attempt.
	for name, test := range map[string]struct{ old, new string }{
		"silence one millisecond past the interval": {silence, `"stream_silence_milliseconds": 10001`},
		"one reconnect attempt":                     {attempts, `"stream_reconnect_attempts": 1`},
	} {
		changed := bytes.Replace(source, []byte(test.old), []byte(test.new), 1)
		if bytes.Equal(changed, source) {
			t.Fatalf("%s: fixture %q not found", name, test.old)
		}
		if _, err := catalog.Load(bytes.NewReader(changed)); err != nil {
			t.Errorf("%s: %v", name, err)
		}
	}
	// The heartbeat event is optional: empty or left out keeps the comment
	// frame alone and stays valid.
	for name, changed := range map[string][]byte{
		"empty":    bytes.Replace(source, []byte(heartbeat), []byte(`"heartbeat": ""`), 1),
		"left out": bytes.Replace(source, []byte(",\n          "+heartbeat), nil, 1),
	} {
		if bytes.Equal(changed, source) {
			t.Fatalf("%s: fixture %q not found", name, heartbeat)
		}
		taskCatalog, err := catalog.Load(bytes.NewReader(changed))
		if err != nil {
			t.Errorf("%s: %v", name, err)
			continue
		}
		if got := taskCatalog.Application().Chat.Stream.EventTypes.Heartbeat; got != "" {
			t.Errorf("%s: heartbeat event = %q", name, got)
		}
		if _, ok := publicPath(publicWire(t, taskCatalog), "chat.stream.event_types.heartbeat"); ok {
			t.Errorf("%s: an undeclared heartbeat event reached the public projection", name)
		}
	}
}

func publicWire(t *testing.T, taskCatalog *catalog.Catalog) map[string]any {
	t.Helper()
	encoded, err := json.Marshal(taskCatalog.Application().Public())
	if err != nil {
		t.Fatal(err)
	}
	var wire map[string]any
	if err := json.Unmarshal(encoded, &wire); err != nil {
		t.Fatal(err)
	}
	return wire
}
