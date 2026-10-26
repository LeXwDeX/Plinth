package httpapi_test

import (
	"database/sql"
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/LeXwDeX/Plinth/internal/generation"
)

// 历史任务快照必须保持提交时的模型身份：目录升级到 2.5 后，旧任务
// 仍读取自身快照里的 gpt-image-2，而不是被映射为新模型。
func TestLegacyTaskReadKeepsHistoricalModelSnapshot(t *testing.T) {
	t.Parallel()
	_, handler, _, _ := newHTTPHarnessWithServices(
		t,
		nil,
		nil,
		nil,
		nil,
		nil,
		func(t *testing.T, path string) {
			database, err := sql.Open("sqlite", path)
			if err != nil {
				t.Fatal(err)
			}
			defer database.Close()
			now := time.Date(2026, 9, 8, 12, 0, 0, 0, time.UTC)
			// This is a pre-grant historical row, not a new paid submission.
			offering, err := json.Marshal(generation.Offering{
				ID:         "openai/gpt-image-2/text-to-image",
				Revision:   "2026-09-08.1",
				ProviderID: "openai",
				ModelID:    "gpt-image-2",
				ModelName:  "GPT Image 2 (OpenAI)",
				Capability: generation.TextToImage,
			})
			if err != nil {
				t.Fatal(err)
			}
			_, err = database.Exec(`INSERT INTO generation_tasks
				(id,owner_id,capability,offering_json,input_json,request_hash,state,
				progress,version,created_at,updated_at,finished_at)
				VALUES (?,?,?,?,?,?,?,?,?,?,?,?)`,
				"historical-task", "owner-a", generation.TextToImage, string(offering),
				`{"prompt":"historical prompt","quality":"high"}`, "historical-hash",
				generation.TaskSucceeded, 100, 1, now.Format(time.RFC3339Nano),
				now.Format(time.RFC3339Nano), now.Format(time.RFC3339Nano))
			if err != nil {
				t.Fatal(err)
			}
		},
	)
	response := serveLegacyRequest(
		handler,
		http.MethodGet,
		"/api/meshy/text-to-image/historical-task",
		nil,
	)
	if response.Code != http.StatusOK {
		t.Fatalf("read status = %d, body = %s", response.Code, response.Body.String())
	}
	var task map[string]any
	if err := json.Unmarshal(response.Body.Bytes(), &task); err != nil {
		t.Fatal(err)
	}
	if task["ai_model"] != "gpt-image-2" {
		t.Fatalf("historical ai_model = %#v", task["ai_model"])
	}
	if task["prompt"] != "historical prompt" {
		t.Fatalf("historical prompt = %#v", task["prompt"])
	}
}
